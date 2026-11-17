package repocache

import (
	"encoding/base64"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// GitCredential authenticates git against URLs under URL, an address prefix.
type GitCredential struct {
	URL      string
	Username string
	Token    string
}

// gitCredentials holds the credentials the server handed this daemon for
// cloning and fetching, keyed by URL prefix. They are process-wide, like the
// CA bundle: every git subprocess that contacts a remote gets them through
// its environment, so nothing is written to disk or shown in a process list.
var gitCredentials struct {
	sync.RWMutex
	byURL map[string]GitCredential
}

// SetGitCredentials adds or replaces the credentials for their URL prefixes.
// Prefixes that are not mentioned keep what they had: a daemon serves several
// workspaces, and each refresh carries only one workspace's.
func SetGitCredentials(creds []GitCredential) {
	gitCredentials.Lock()
	defer gitCredentials.Unlock()
	if gitCredentials.byURL == nil {
		gitCredentials.byURL = map[string]GitCredential{}
	}
	for _, cred := range creds {
		cred.URL = strings.TrimSpace(cred.URL)
		if cred.URL == "" || cred.Token == "" {
			continue
		}
		gitCredentials.byURL[cred.URL] = cred
	}
}

// credentialConfig returns git settings (key, value pairs) that send each
// stored credential to the URLs under its prefix.
func credentialConfig() [][2]string {
	gitCredentials.RLock()
	defer gitCredentials.RUnlock()
	if len(gitCredentials.byURL) == 0 {
		return nil
	}
	urls := make([]string, 0, len(gitCredentials.byURL))
	for url := range gitCredentials.byURL {
		urls = append(urls, url)
	}
	sort.Strings(urls)
	out := make([][2]string, 0, len(urls))
	for _, url := range urls {
		cred := gitCredentials.byURL[url]
		basic := base64.StdEncoding.EncodeToString([]byte(cred.Username + ":" + cred.Token))
		out = append(out, [2]string{"http." + url + ".extraheader", "Authorization: Basic " + basic})
	}
	return out
}

// appendGitConfigEnv appends settings as GIT_CONFIG_* variables after the
// env-scoped settings env already carries.
func appendGitConfigEnv(env []string, settings [][2]string) []string {
	if len(settings) == 0 {
		return env
	}
	existing := 0
	for _, e := range env {
		if value, ok := strings.CutPrefix(e, "GIT_CONFIG_COUNT="); ok {
			if n, err := strconv.Atoi(value); err == nil {
				existing = n
			}
		}
	}
	for i, setting := range settings {
		idx := strconv.Itoa(existing + i)
		env = append(env, "GIT_CONFIG_KEY_"+idx+"="+setting[0], "GIT_CONFIG_VALUE_"+idx+"="+setting[1])
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(existing+len(settings)))
}
