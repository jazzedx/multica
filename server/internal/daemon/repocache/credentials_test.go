package repocache

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

func resetGitCredentials(t *testing.T) {
	t.Helper()
	clear := func() {
		gitCredentials.Lock()
		gitCredentials.byURL = nil
		gitCredentials.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// Real git must send a credential to the repositories under its prefix and to
// nothing else on the same host.
func TestGitCredentialsReachOnlyTheirPrefix(t *testing.T) {
	resetGitCredentials(t)

	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	SetGitCredentials([]GitCredential{{URL: srv.URL + "/acme/", Username: "acme-reader", Token: "tok"}})
	// The repositories do not exist; only the requests matter.
	_ = newGitCommand("ls-remote", srv.URL+"/acme/kernel.git").Run()
	_ = newGitCommand("ls-remote", srv.URL+"/other/kernel.git").Run()

	mu.Lock()
	defer mu.Unlock()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("acme-reader:tok"))
	if got, ok := seen["/acme/kernel.git/info/refs"]; !ok || got != want {
		t.Errorf("request under the prefix carried %q (requested: %v), want the credential", got, ok)
	}
	if got, ok := seen["/other/kernel.git/info/refs"]; !ok || got != "" {
		t.Errorf("request outside the prefix carried %q (requested: %v), want no credential", got, ok)
	}
}

func TestSetGitCredentialsKeepsOtherWorkspaces(t *testing.T) {
	resetGitCredentials(t)
	SetGitCredentials([]GitCredential{{URL: "http://git.test/one/", Username: "one-reader", Token: "a"}})
	SetGitCredentials([]GitCredential{
		{URL: "http://git.test/two/", Username: "two-reader", Token: "b"},
		{URL: "http://git.test/empty/", Username: "x", Token: ""},
	})
	SetGitCredentials([]GitCredential{{URL: "http://git.test/one/", Username: "one-reader", Token: "rotated"}})

	settings := credentialConfig()
	if len(settings) != 2 {
		t.Fatalf("settings = %v, want one per workspace with a token", settings)
	}
	rotated := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("one-reader:rotated"))
	if settings[0][0] != "http.http://git.test/one/.extraheader" || settings[0][1] != rotated {
		t.Errorf("first setting = %v, want the replaced credential of the first workspace", settings[0])
	}
}

func TestAppendGitConfigEnvContinuesExistingSettings(t *testing.T) {
	env := []string{"PATH=/bin", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=a", "GIT_CONFIG_KEY_1=b"}
	got := appendGitConfigEnv(env, [][2]string{{"safe.directory", "*"}, {"http.x.extraheader", "h"}})

	for _, want := range []string{
		"GIT_CONFIG_KEY_2=safe.directory", "GIT_CONFIG_VALUE_2=*",
		"GIT_CONFIG_KEY_3=http.x.extraheader", "GIT_CONFIG_VALUE_3=h",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("env is missing %q: %v", want, got)
		}
	}
	// The last GIT_CONFIG_COUNT is the one git reads.
	if got[len(got)-1] != "GIT_CONFIG_COUNT=4" {
		t.Errorf("last entry = %q, want the new count", got[len(got)-1])
	}
	if unchanged := appendGitConfigEnv(env, nil); len(unchanged) != len(env) {
		t.Errorf("no settings must leave the env alone, got %v", unchanged)
	}
}
