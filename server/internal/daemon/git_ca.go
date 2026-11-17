package daemon

import "github.com/multica-ai/multica/server/internal/daemon/repocache"

// gitCACertSetter is implemented by repocache.Cache. It is optional so test
// doubles of repoCacheBackend do not have to implement it.
type gitCACertSetter interface {
	SetExtraCACerts(pemCerts string) error
}

// applyGitCACerts hands the server's extra CA certificates to the repo cache,
// which makes git trust them for clone, fetch, and agents' push. It runs on
// registration and on every workspace repo refresh (which precedes each
// checkout), so a CA rotated on the server reaches the daemon without a
// restart. An empty value removes a previously applied bundle.
func (d *Daemon) applyGitCACerts(pemCerts string) {
	cache, ok := d.repoCache.(gitCACertSetter)
	if !ok {
		return
	}
	err := cache.SetExtraCACerts(pemCerts)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if prev := d.gitCALastErr.Swap(&msg); err != nil && (prev == nil || *prev != msg) {
		d.logger.Warn("git CA: could not apply server CA certificates; repositories on hosts with a private CA may fail to sync", "error", err)
	}
}

// GitCredential is a credential the server hands out for its bundled code
// host: it authenticates git against URLs under URL.
type GitCredential struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

// applyGitCredentials hands the server's credentials for its bundled code
// host to the repo cache, so cloning and fetching the workspace's
// repositories needs no credentials on this machine. It runs wherever
// applyGitCACerts does. Agents push with their own credentials, which arrive
// with each task.
func (d *Daemon) applyGitCredentials(creds []GitCredential) {
	if len(creds) == 0 {
		return
	}
	converted := make([]repocache.GitCredential, 0, len(creds))
	for _, cred := range creds {
		converted = append(converted, repocache.GitCredential{URL: cred.URL, Username: cred.Username, Token: cred.Token})
	}
	repocache.SetGitCredentials(converted)
}
