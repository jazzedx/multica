package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/giteaadmin/giteaadmintest"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// builtinCodeHostFixture points the test handler at a fake Gitea as its
// bundled code host and links the test workspace to it on demand.
type builtinCodeHostFixture struct {
	t    *testing.T
	fake *giteaadmintest.Server
	ws   db.Workspace
}

func newBuiltinCodeHostFixture(t *testing.T) builtinCodeHostFixture {
	t.Helper()
	ctx := context.Background()
	withVCSBox(t)
	fake := giteaadmintest.New(t)

	ws, err := testHandler.Queries.GetWorkspace(ctx, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	prevCfg := testHandler.cfg.BuiltinCodeHost
	testHandler.cfg.BuiltinCodeHost = BuiltinCodeHostConfig{
		URL:            fake.URL,
		PublicURL:      "http://git.example/",
		WebhookBaseURL: "http://backend:8080",
		Token:          fake.Token,
	}
	t.Cleanup(func() {
		testHandler.cfg.BuiltinCodeHost = prevCfg
		dbfx.Exec(t, `DELETE FROM builtin_code_host_workspace WHERE workspace_id = $1`, testWorkspaceID)
		dbfx.Exec(t, `DELETE FROM builtin_code_host_account WHERE workspace_id = $1`, testWorkspaceID)
		dbfx.Exec(t, `DELETE FROM vcs_connection WHERE workspace_id = $1 AND instance_url = $2`, testWorkspaceID, fake.URL)
		dbfx.Exec(t, `UPDATE workspace SET repos = $1 WHERE id = $2`, ws.Repos, testWorkspaceID)
	})
	return builtinCodeHostFixture{t: t, fake: fake, ws: ws}
}

func (f builtinCodeHostFixture) reconcile(refresh bool) {
	f.t.Helper()
	if err := testHandler.reconcileBuiltinCodeHostWorkspace(context.Background(), f.ws.ID, f.ws.Name, f.ws.Slug, refresh); err != nil {
		f.t.Fatalf("reconcile: %v", err)
	}
}

func (f builtinCodeHostFixture) connection() db.VcsConnection {
	f.t.Helper()
	conn, err := testHandler.Queries.GetVCSConnectionByWorkspaceAndInstance(context.Background(), db.GetVCSConnectionByWorkspaceAndInstanceParams{
		WorkspaceID: f.ws.ID,
		InstanceUrl: f.fake.URL,
	})
	if err != nil {
		f.t.Fatalf("load connection: %v", err)
	}
	return conn
}

// cloneURL is the address a repository is listed under: the public one, not
// the one the server used to reach the instance.
func (f builtinCodeHostFixture) cloneURL(org, name string) string {
	return "http://git.example/" + org + "/" + name + ".git"
}

func (f builtinCodeHostFixture) repoURLs() []string {
	f.t.Helper()
	ws, err := testHandler.Queries.GetWorkspace(context.Background(), f.ws.ID)
	if err != nil {
		f.t.Fatalf("load workspace: %v", err)
	}
	var repos []workspaceRepoRef
	if err := json.Unmarshal(ws.Repos, &repos); err != nil {
		f.t.Fatalf("decode repos: %v", err)
	}
	urls := make([]string, 0, len(repos))
	for _, repo := range repos {
		urls = append(urls, repo.URL)
	}
	return urls
}

func TestBuiltinCodeHostLinksWorkspace(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	f.reconcile(false)

	org := f.ws.Slug
	if !f.fake.HasOrg(org) {
		t.Fatalf("no organization named after the workspace slug %q", org)
	}
	teams := f.fake.Teams(org)
	if !slices.Contains(teams, builtinCodeHostTeam) {
		t.Errorf("teams = %v, want a %q team", teams, builtinCodeHostTeam)
	}

	conn := f.connection()
	if conn.Provider != "gitea" || conn.AccountLogin != f.fake.Login {
		t.Errorf("connection = %s as %q, want gitea as the service account", conn.Provider, conn.AccountLogin)
	}
	token, _ := testHandler.openVCSSecret(conn.AccessTokenEncrypted)
	secret, _ := testHandler.openVCSSecret(conn.WebhookSecretEncrypted)
	if token != f.fake.Token {
		t.Errorf("stored token = %q, want the service token", token)
	}

	hooks := f.fake.Hooks(org)
	if len(hooks) != 1 {
		t.Fatalf("hooks = %+v, want one", hooks)
	}
	wantURL := "http://backend:8080/api/webhooks/vcs/" + uuidToString(conn.ID)
	if hooks[0].URL != wantURL || hooks[0].Secret != secret || secret == "" {
		t.Errorf("hook = %s, want %s signed with the connection's secret", hooks[0].URL, wantURL)
	}
	for _, event := range []string{"pull_request", "status", "repository"} {
		if !slices.Contains(hooks[0].Events, event) {
			t.Errorf("hook events %v are missing %q", hooks[0].Events, event)
		}
	}

	// A second pass finds everything in place and changes nothing.
	f.reconcile(false)
	again := f.connection()
	if again.WebhookSecretEncrypted != conn.WebhookSecretEncrypted {
		t.Error("a second pass replaced the webhook secret")
	}
	if hooks := f.fake.Hooks(org); len(hooks) != 1 || hooks[0].Writes != 1 {
		t.Errorf("a second pass rewrote the hook: %+v", hooks)
	}
	if after := f.fake.Teams(org); len(after) != len(teams) {
		t.Errorf("a second pass added a team: %v, was %v", after, teams)
	}
}

func TestBuiltinCodeHostMakesTheAdministratorAnOwner(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	f.fake.AddUser("lead")
	testHandler.cfg.BuiltinCodeHost.Owner = "lead"

	f.reconcile(false)
	f.reconcile(false)
	if owners := f.fake.TeamMembers(f.ws.Slug, builtinCodeHostOwnersTeam); len(owners) != 1 || owners[0] != "lead" {
		t.Fatalf("owners = %v, want the administrator once", owners)
	}
}

func TestBuiltinCodeHostLinksWithoutTheAdministrator(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	// The configured account was renamed or never existed.
	testHandler.cfg.BuiltinCodeHost.Owner = "gone"

	f.reconcile(false)
	if hooks := f.fake.Hooks(f.ws.Slug); len(hooks) != 1 {
		t.Fatalf("a missing administrator must not stop the link, hooks = %+v", hooks)
	}
}

func TestBuiltinCodeHostFallsBackWhenTheSlugIsTaken(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	f.fake.TakeName(f.ws.Slug)
	f.reconcile(false)

	fallback := "ws-" + f.ws.Slug
	if f.fake.HasOrg(f.ws.Slug) || !f.fake.HasOrg(fallback) {
		t.Fatalf("want the organization %q when the slug is unavailable", fallback)
	}
	state, err := testHandler.Queries.GetBuiltinCodeHostWorkspace(context.Background(), f.ws.ID)
	if err != nil || state.OrgName != fallback {
		t.Fatalf("stored organization = %q (%v), want %q", state.OrgName, err, fallback)
	}
	// The stored name wins from then on, even though the slug is free again.
	f.reconcile(false)
	if hooks := f.fake.Hooks(fallback); len(hooks) != 1 {
		t.Errorf("hooks on %q = %+v, want one", fallback, hooks)
	}
}

func TestBuiltinCodeHostAddsEachRepositoryOnce(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	org := f.ws.Slug
	f.reconcile(false)
	before := f.repoURLs()

	f.fake.AddRepo(org, "kernel", "Geometry kernel")
	f.reconcile(false)
	kernel := f.cloneURL(org, "kernel")
	if urls := f.repoURLs(); len(urls) != len(before)+1 || !slices.Contains(urls, kernel) {
		t.Fatalf("repos = %v, want %v plus %s", urls, before, kernel)
	}

	// Removing it from the workspace's list sticks.
	dbfx.Exec(t, `UPDATE workspace SET repos = $1 WHERE id = $2`, f.ws.Repos, testWorkspaceID)
	f.fake.AddRepo(org, "modeler", "")
	f.reconcile(false)
	urls := f.repoURLs()
	if slices.Contains(urls, kernel) {
		t.Errorf("a repository removed from the list came back: %v", urls)
	}
	if !slices.Contains(urls, f.cloneURL(org, "modeler")) {
		t.Errorf("a repository created later was not added: %v", urls)
	}

	// One a member already listed by hand is not listed twice.
	manual := f.cloneURL(org, "plm")
	raw, _ := json.Marshal([]workspaceRepoRef{{URL: manual}})
	dbfx.Exec(t, `UPDATE workspace SET repos = $1 WHERE id = $2`, raw, testWorkspaceID)
	f.fake.AddRepo(org, "plm", "")
	f.reconcile(false)
	count := 0
	for _, url := range f.repoURLs() {
		if url == manual {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%s is listed %d times, want once", manual, count)
	}
}

func TestBuiltinCodeHostFollowsANewServiceToken(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	f.reconcile(false)
	before := f.connection()

	f.fake.SetToken("rotated-token")
	testHandler.cfg.BuiltinCodeHost.Token = "rotated-token"
	f.reconcile(false)

	after := f.connection()
	if after.ID != before.ID {
		t.Fatal("a new token must update the connection in place")
	}
	token, _ := testHandler.openVCSSecret(after.AccessTokenEncrypted)
	secret, _ := testHandler.openVCSSecret(after.WebhookSecretEncrypted)
	if token != "rotated-token" {
		t.Errorf("stored token = %q, want the new one", token)
	}
	hooks := f.fake.Hooks(f.ws.Slug)
	if len(hooks) != 1 || hooks[0].Secret != secret {
		t.Errorf("the webhook must be rewritten with the connection's current secret, got %+v", hooks)
	}
}

func TestBuiltinCodeHostConnectionIsMarkedAndProtected(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	f.reconcile(false)
	conn := f.connection()
	connID := uuidToString(conn.ID)

	w := httptest.NewRecorder()
	testHandler.ListVCSConnections(w, vcsHandlerRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/vcs/connections", nil, ""))
	var listed struct {
		Connections []VCSConnectionResponse `json:"connections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found *VCSConnectionResponse
	for i := range listed.Connections {
		if listed.Connections[i].ID == connID {
			found = &listed.Connections[i]
		}
	}
	if found == nil {
		t.Fatalf("the built-in connection is not listed: %s", w.Body.String())
	}
	if !found.Builtin || found.WebURL != "http://git.example/"+f.ws.Slug {
		t.Errorf("builtin = %v, web_url = %q; want it marked and linked to the organization", found.Builtin, found.WebURL)
	}

	w = httptest.NewRecorder()
	testHandler.DeleteVCSConnection(w, vcsHandlerRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/vcs/connections/"+connID, nil, connID))
	if w.Code != http.StatusConflict {
		t.Errorf("delete = %d, want 409", w.Code)
	}
	w = httptest.NewRecorder()
	testHandler.RotateVCSConnectionWebhook(w, vcsHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/vcs/connections/"+connID+"/rotate-webhook", nil, connID))
	if w.Code != http.StatusConflict {
		t.Errorf("rotate = %d, want 409", w.Code)
	}
	if after := f.connection(); after.WebhookSecretEncrypted != conn.WebhookSecretEncrypted {
		t.Error("the rejected rotate changed the secret")
	}
}

func TestBuiltinCodeHostRepositoryWebhookAddsTheRepository(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	f.reconcile(false)
	conn := f.connection()
	secret, _ := testHandler.openVCSSecret(conn.WebhookSecretEncrypted)

	f.fake.AddRepo(f.ws.Slug, "viewer", "")
	raw := []byte(`{"action":"created"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	w := httptest.NewRecorder()
	testHandler.HandleVCSWebhook(w, vcsWebhookReq(uuidToString(conn.ID), map[string]string{
		"X-Gitea-Event": "repository", "X-Gitea-Signature": hex.EncodeToString(mac.Sum(nil)),
	}, raw))
	if w.Code != http.StatusAccepted {
		t.Fatalf("webhook = %d: %s", w.Code, w.Body.String())
	}

	want := f.cloneURL(f.ws.Slug, "viewer")
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(f.repoURLs(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the repository was not added after its webhook: %v", f.repoURLs())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBuiltinCodeHostIsOffWithoutAnInstance(t *testing.T) {
	prev := testHandler.cfg.BuiltinCodeHost
	testHandler.cfg.BuiltinCodeHost = BuiltinCodeHostConfig{}
	t.Cleanup(func() { testHandler.cfg.BuiltinCodeHost = prev })
	withVCSBox(t)

	if testHandler.builtinCodeHostEnabled() {
		t.Fatal("the feature must be off when no instance is configured")
	}
	if !testHandler.ReconcileBuiltinCodeHost(context.Background(), true) {
		t.Error("with the feature off there is nothing to retry")
	}
	var count int
	dbfx.QueryRow(t, `SELECT count(*) FROM builtin_code_host_workspace WHERE workspace_id = $1`, testWorkspaceID).Scan(&count)
	if count != 0 {
		t.Errorf("state rows = %d, want none", count)
	}
}

// agent adds an agent to the test workspace and returns its row.
func (f builtinCodeHostFixture) agent(name string) db.Agent {
	f.t.Helper()
	id := dbfx.Agent(f.t, name, testRuntimeID)
	agent, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(id))
	if err != nil {
		f.t.Fatalf("load agent: %v", err)
	}
	return agent
}

func (f builtinCodeHostFixture) account(agent db.Agent) db.BuiltinCodeHostAccount {
	f.t.Helper()
	account, err := testHandler.Queries.GetBuiltinCodeHostAccount(context.Background(), agent.ID)
	if err != nil {
		f.t.Fatalf("load account of %s: %v", agent.Name, err)
	}
	return account
}

func TestBuiltinCodeHostGivesDaemonsAReadOnlyAccount(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	if creds := testHandler.builtinCodeHostReaderCredentials(context.Background(), f.ws.ID); creds != nil {
		t.Fatalf("an unlinked workspace has credentials: %+v", creds)
	}
	f.reconcile(false)
	f.reconcile(false)

	org := f.ws.Slug
	reader := org + "-reader"
	if members := f.fake.TeamMembers(org, builtinCodeHostReadersTeam); len(members) != 1 || members[0] != reader {
		t.Fatalf("readers = %v, want %s", members, reader)
	}
	if n := f.fake.TokenCount(reader); n != 1 {
		t.Errorf("the reader has %d tokens after two passes, want 1", n)
	}

	creds := testHandler.builtinCodeHostReaderCredentials(context.Background(), f.ws.ID)
	if len(creds) != 1 {
		t.Fatalf("credentials = %+v, want one", creds)
	}
	if creds[0].URL != "http://git.example/"+org+"/" || creds[0].Username != reader {
		t.Errorf("credential = %s as %s, want it scoped to the organization's public address", creds[0].URL, creds[0].Username)
	}
	scopes, ok := f.fake.TokenScopes(reader, creds[0].Token)
	if !ok || !slices.Contains(scopes, "read:repository") {
		t.Errorf("reader token scopes = %v (%v), want read access to repositories", scopes, ok)
	}
	for _, scope := range scopes {
		if strings.HasPrefix(scope, "write:") {
			t.Errorf("the reader's token can write: %v", scopes)
		}
	}

	// A token deleted on the instance is replaced by the next pass.
	f.fake.RevokeTokens(reader)
	f.reconcile(false)
	renewed := testHandler.builtinCodeHostReaderCredentials(context.Background(), f.ws.ID)
	if len(renewed) != 1 || renewed[0].Token == creds[0].Token {
		t.Fatalf("credentials after the token was deleted = %+v, want a new token", renewed)
	}
	if _, ok := f.fake.TokenScopes(reader, renewed[0].Token); !ok {
		t.Error("the stored reader token is not one the instance knows")
	}
}

func TestBuiltinCodeHostGivesEachAgentItsOwnAccount(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	dev := f.agent("L4 Dev")
	review := f.agent("L4 Review")
	f.reconcile(false)
	f.reconcile(false)

	devAccount, reviewAccount := f.account(dev), f.account(review)
	if devAccount.Username != "l4-dev" || reviewAccount.Username != "l4-review" {
		t.Fatalf("accounts = %q and %q, want names derived from the agents'", devAccount.Username, reviewAccount.Username)
	}
	members := f.fake.TeamMembers(f.ws.Slug, builtinCodeHostTeam)
	if !slices.Contains(members, "l4-dev") || !slices.Contains(members, "l4-review") {
		t.Errorf("developers = %v, want both agents", members)
	}
	if n := f.fake.TokenCount("l4-dev"); n != 1 {
		t.Errorf("l4-dev has %d tokens after two passes, want 1", n)
	}
	token, _ := testHandler.openVCSSecret(devAccount.TokenEncrypted)
	if scopes, ok := f.fake.TokenScopes("l4-dev", token); !ok || !slices.Contains(scopes, "write:repository") {
		t.Errorf("stored token scopes = %v (%v), want one that can push", scopes, ok)
	}
}

func TestBuiltinCodeHostAccountForANameWithoutLatinLetters(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	agent := f.agent("评审员")
	f.reconcile(false)

	username := f.account(agent).Username
	short := strings.ReplaceAll(uuidToString(agent.ID), "-", "")[:6]
	if username != "agent-"+short {
		t.Fatalf("account = %q, want agent-%s rather than a bare \"agent\" another agent could have", username, short)
	}
}

func TestBuiltinCodeHostAccountNameAvoidsTakenNames(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	f.fake.AddUser("dev")
	agent := f.agent("Dev")
	f.reconcile(false)

	username := f.account(agent).Username
	if username == "dev" || !strings.HasPrefix(username, "dev-") {
		t.Fatalf("account = %q, want a suffixed name because dev belongs to someone else", username)
	}
	if n := f.fake.TokenCount("dev"); n != 0 {
		t.Errorf("the other account got %d tokens, want it untouched", n)
	}
}

func TestBuiltinCodeHostArchivedAgentLosesWriteAccess(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	agent := f.agent("Sunset")
	f.reconcile(false)
	inTeam := func() bool {
		return slices.Contains(f.fake.TeamMembers(f.ws.Slug, builtinCodeHostTeam), "sunset")
	}
	if !inTeam() {
		t.Fatal("an active agent must be in the developers team")
	}

	dbfx.Exec(t, `UPDATE agent SET archived_at = now() WHERE id = $1`, uuidToString(agent.ID))
	f.reconcile(false)
	if inTeam() || f.account(agent).InTeam {
		t.Fatal("an archived agent's account must leave the developers team")
	}
	if !f.fake.LoginProhibited("sunset") {
		t.Fatal("an archived agent's account must be blocked, which disables its token")
	}
	if !f.fake.HasUser("sunset") {
		t.Fatal("the account must stay, so the agent's commits keep their author")
	}

	dbfx.Exec(t, `UPDATE agent SET archived_at = NULL WHERE id = $1`, uuidToString(agent.ID))
	f.reconcile(false)
	if !inTeam() || !f.account(agent).InTeam {
		t.Fatal("a restored agent's account must be put back")
	}
	if f.fake.LoginProhibited("sunset") {
		t.Fatal("a restored agent's account must be unblocked")
	}
	if n := f.fake.TokenCount("sunset"); n != 1 {
		t.Errorf("restoring issued a new token (%d), want the stored one kept", n)
	}
}

func TestBuiltinCodeHostCredentialsTravelWithTheTask(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	f.reconcile(false)
	// Created after the pass: its account is made when its task is claimed.
	agent := f.agent("Late Joiner")

	env := testHandler.withBuiltinCodeHostEnv(context.Background(), agent, map[string]string{"FOO": "1"})
	account := f.account(agent)
	token, _ := testHandler.openVCSSecret(account.TokenEncrypted)
	want := map[string]string{
		"FOO":                 "1",
		"GITEA_URL":           "http://git.example",
		"GITEA_API_URL":       "http://git.example/api/v1",
		"GITEA_ORG":           f.ws.Slug,
		"GITEA_USER":          "late-joiner",
		"GITEA_TOKEN":         token,
		"GIT_AUTHOR_NAME":     "Late Joiner",
		"GIT_AUTHOR_EMAIL":    builtinCodeHostAgentEmail(agent.ID),
		"GIT_COMMITTER_NAME":  "Late Joiner",
		"GIT_COMMITTER_EMAIL": builtinCodeHostAgentEmail(agent.ID),
		"GIT_CONFIG_COUNT":    "1",
		"GIT_CONFIG_KEY_0":    "http.http://git.example/.extraheader",
		"GIT_CONFIG_VALUE_0": "Authorization: Basic " +
			base64.StdEncoding.EncodeToString([]byte("late-joiner:"+token)),
	}
	if token == "" {
		t.Fatal("no token was issued for the task's agent")
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("%s = %q, want %q", key, env[key], value)
		}
	}
	if len(env) != len(want) {
		t.Errorf("env has %d variables, want %d: %v", len(env), len(want), env)
	}

	// What the agent configured itself is left alone.
	own := testHandler.withBuiltinCodeHostEnv(context.Background(), agent, map[string]string{
		"GITEA_USER":       "mine",
		"GIT_AUTHOR_NAME":  "Someone",
		"GIT_CONFIG_COUNT": "1",
		"GIT_CONFIG_KEY_0": "user.name",
	})
	if own["GITEA_USER"] != "mine" || own["GIT_AUTHOR_NAME"] != "Someone" ||
		own["GIT_CONFIG_KEY_0"] != "user.name" || own["GIT_CONFIG_VALUE_0"] != "" {
		t.Errorf("the agent's own variables were overridden: %v", own)
	}
	if own["GITEA_TOKEN"] != token {
		t.Errorf("variables the agent did not set must still be added: %v", own)
	}
}

func TestBuiltinCodeHostAddsNoCredentialsWithoutALink(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	agent := f.agent("Unlinked")
	// No reconcile: the workspace has no organization yet.
	env := testHandler.withBuiltinCodeHostEnv(context.Background(), agent, map[string]string{"FOO": "1"})
	if len(env) != 1 || env["FOO"] != "1" {
		t.Errorf("env = %v, want it unchanged for a workspace that is not linked", env)
	}
	if f.fake.HasUser("unlinked") {
		t.Error("an account was created for a workspace that is not linked")
	}
}

func TestBuiltinCodeHostUsername(t *testing.T) {
	cases := map[string]string{
		"L4-Dev":          "l4-dev",
		"Product Manager": "product-manager",
		"  QA__Bot!! ":    "qa-bot",
		"评审员":             "",
		"评审 Reviewer 2":   "reviewer-2",
		"a-very-long-agent-name-that-goes-past-the-limit": "a-very-long-agent-name-that-go",
	}
	for name, want := range cases {
		if got := builtinCodeHostUsername(name); got != want {
			t.Errorf("builtinCodeHostUsername(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestBuiltinCodeHostReplacesADeletedAgentToken(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	agent := f.agent("Phoenix")
	f.reconcile(false)
	first := testHandler.withBuiltinCodeHostEnv(context.Background(), agent, nil)["GITEA_TOKEN"]
	if first == "" {
		t.Fatal("no token for the agent")
	}
	// A working token is left alone, however often tasks are claimed.
	if again := testHandler.withBuiltinCodeHostEnv(context.Background(), agent, nil)["GITEA_TOKEN"]; again != first {
		t.Fatalf("a working token was replaced: %q -> %q", first, again)
	}

	f.fake.RevokeTokens("phoenix")
	env := testHandler.withBuiltinCodeHostEnv(context.Background(), agent, nil)
	second := env["GITEA_TOKEN"]
	if second == "" || second == first {
		t.Fatalf("token after deletion = %q, want a new one", second)
	}
	if _, ok := f.fake.TokenScopes("phoenix", second); !ok {
		t.Error("the task was handed a token the instance does not know")
	}
	if want := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("phoenix:"+second)); env["GIT_CONFIG_VALUE_0"] != want {
		t.Error("git was not given the new token")
	}
	stored, _ := testHandler.openVCSSecret(f.account(agent).TokenEncrypted)
	if stored != second {
		t.Error("the new token was not stored")
	}
}

func TestBuiltinCodeHostKeepsTheTokenWhenTheInstanceIsDown(t *testing.T) {
	f := newBuiltinCodeHostFixture(t)
	agent := f.agent("Steady")
	f.reconcile(false)
	before := testHandler.withBuiltinCodeHostEnv(context.Background(), agent, nil)["GITEA_TOKEN"]

	// Point the server at an address nothing listens on.
	testHandler.cfg.BuiltinCodeHost.URL = "http://127.0.0.1:1"
	dbfx.Exec(t, `UPDATE vcs_connection SET instance_url = $1 WHERE workspace_id = $2 AND instance_url = $3`,
		"http://127.0.0.1:1", testWorkspaceID, f.fake.URL)
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM vcs_connection WHERE workspace_id = $1 AND instance_url = $2`, testWorkspaceID, "http://127.0.0.1:1")
	})
	after := testHandler.withBuiltinCodeHostEnv(context.Background(), agent, nil)["GITEA_TOKEN"]
	if after != before || before == "" {
		t.Errorf("token with the instance unreachable = %q, want the stored %q", after, before)
	}
}
