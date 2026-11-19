package giteaadmin_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/giteaadmin"
	"github.com/multica-ai/multica/server/internal/integrations/giteaadmin/giteaadmintest"
)

func TestEnsureOrg(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL+"/", fake.Token)

	if err := client.EnsureOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !fake.HasOrg("acme") {
		t.Fatal("organization was not created")
	}
	if err := client.EnsureOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("an existing organization should be accepted: %v", err)
	}

	fake.TakeName("admin")
	if err := client.EnsureOrg(ctx, "admin", "Admin"); !errors.Is(err, giteaadmin.ErrNameUnavailable) {
		t.Fatalf("err = %v, want ErrNameUnavailable", err)
	}
}

func TestEnsureTeamCreatesOnce(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)
	if err := client.EnsureOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := client.EnsureTeam(ctx, "acme", "developers", giteaadmin.PermissionWrite); err != nil {
			t.Fatal(err)
		}
	}
	if teams := fake.Teams("acme"); len(teams) != 2 || teams[1] != "developers" {
		t.Fatalf("teams = %v, want the owners team and one developers team", teams)
	}
}

func TestEnsureTeamMember(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)
	if err := client.EnsureOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	fake.AddUser("lead")

	for range 2 {
		if err := client.EnsureTeamMember(ctx, "acme", "Owners", "lead"); err != nil {
			t.Fatal(err)
		}
	}
	if members := fake.TeamMembers("acme", "Owners"); len(members) != 1 || members[0] != "lead" {
		t.Fatalf("owners = %v, want lead once", members)
	}

	if err := client.EnsureTeamMember(ctx, "acme", "Owners", "nobody"); !errors.Is(err, giteaadmin.ErrNotFound) {
		t.Errorf("an account that does not exist: err = %v, want ErrNotFound", err)
	}
	if err := client.EnsureTeamMember(ctx, "acme", "no-such-team", "lead"); !errors.Is(err, giteaadmin.ErrNotFound) {
		t.Errorf("a team that does not exist: err = %v, want ErrNotFound", err)
	}
}

func TestEnsureOrgHook(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)
	if err := client.EnsureOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	hook := giteaadmin.Hook{URL: "http://backend:8080/hook", Secret: "one", Events: []string{"pull_request"}}

	if err := client.EnsureOrgHook(ctx, "acme", hook, false); err != nil {
		t.Fatal(err)
	}
	hook.Secret = "two"
	if err := client.EnsureOrgHook(ctx, "acme", hook, false); err != nil {
		t.Fatal(err)
	}
	hooks := fake.Hooks("acme")
	if len(hooks) != 1 || hooks[0].Secret != "one" || hooks[0].Writes != 1 {
		t.Fatalf("without refresh an existing hook must be left alone, got %+v", hooks)
	}

	if err := client.EnsureOrgHook(ctx, "acme", hook, true); err != nil {
		t.Fatal(err)
	}
	hooks = fake.Hooks("acme")
	if len(hooks) != 1 || hooks[0].Secret != "two" {
		t.Fatalf("refresh must rewrite the hook in place, got %+v", hooks)
	}
}

func TestListOrgRepositoriesReadsEveryPage(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)
	if err := client.EnsureOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	const total = 120
	for i := range total {
		fake.AddRepo("acme", fmt.Sprintf("repo-%03d", i), "")
	}
	repos, err := client.ListOrgRepositories(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != total {
		t.Fatalf("got %d repositories, want %d", len(repos), total)
	}
	if repos[0].CloneURL != fake.CloneURL("acme", "repo-000") {
		t.Errorf("clone url = %q", repos[0].CloneURL)
	}
}

func TestRejectedTokenIsAnError(t *testing.T) {
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, "wrong")
	if _, err := client.CurrentLogin(context.Background()); err == nil {
		t.Fatal("a rejected token must not look like success")
	}
}

func TestCreateUserAndIssueToken(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)

	if err := client.CreateUser(ctx, "dev", "Dev", "dev@multica.local"); err != nil {
		t.Fatal(err)
	}
	if err := client.CreateUser(ctx, "dev", "Dev", "dev2@multica.local"); !errors.Is(err, giteaadmin.ErrNameUnavailable) {
		t.Fatalf("a taken name: err = %v, want ErrNameUnavailable", err)
	}

	scopes := []string{"write:repository", "read:user"}
	first, err := client.IssueToken(ctx, "dev", scopes)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.IssueToken(ctx, "dev", scopes)
	if err != nil {
		t.Fatalf("a second token for the same account: %v", err)
	}
	if first == "" || first == second {
		t.Fatalf("tokens = %q and %q, want two different ones", first, second)
	}
	if got, ok := fake.TokenScopes("dev", second); !ok || len(got) != 2 || got[0] != "write:repository" {
		t.Errorf("stored scopes = %v (%v), want the requested ones", got, ok)
	}
	// Issuing replaces: the account is left with the newest token only.
	if _, ok := fake.TokenScopes("dev", first); ok || fake.TokenCount("dev") != 1 {
		t.Errorf("the earlier token still works (%d tokens), want it deleted", fake.TokenCount("dev"))
	}

	if _, err := client.IssueToken(ctx, "nobody", scopes); !errors.Is(err, giteaadmin.ErrNotFound) {
		t.Errorf("an account that does not exist: err = %v, want ErrNotFound", err)
	}
}

func TestRemoveTeamMember(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)
	if err := client.EnsureOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureTeam(ctx, "acme", "readers", giteaadmin.PermissionRead); err != nil {
		t.Fatal(err)
	}
	fake.AddUser("dev")
	if err := client.EnsureTeamMember(ctx, "acme", "readers", "dev"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := client.RemoveTeamMember(ctx, "acme", "readers", "dev"); err != nil {
			t.Fatalf("removing, also when already removed: %v", err)
		}
	}
	if members := fake.TeamMembers("acme", "readers"); len(members) != 0 {
		t.Errorf("members = %v, want none", members)
	}
}

func TestTokenRejected(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	admin := giteaadmin.New(fake.URL, fake.Token)
	if err := admin.CreateUser(ctx, "dev", "Dev", "dev@multica.local"); err != nil {
		t.Fatal(err)
	}
	token, err := admin.IssueToken(ctx, "dev", []string{"read:user"})
	if err != nil {
		t.Fatal(err)
	}
	own := giteaadmin.New(fake.URL, token)
	rejected := func() bool {
		t.Helper()
		got, err := own.TokenRejected(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if rejected() {
		t.Fatal("a working token was reported as rejected")
	}
	if err := admin.SetLoginProhibited(ctx, "dev", true); err != nil {
		t.Fatal(err)
	}
	if !rejected() {
		t.Error("a blocked account's token must be reported as rejected")
	}
	if err := admin.SetLoginProhibited(ctx, "dev", false); err != nil {
		t.Fatal(err)
	}
	if rejected() {
		t.Error("unblocking the account must make its token work again")
	}
	fake.RevokeTokens("dev")
	if !rejected() {
		t.Error("a deleted token must be reported as rejected")
	}

	// An instance that does not answer says nothing about the token.
	fake.Close()
	if got, err := own.TokenRejected(ctx); err == nil || got {
		t.Errorf("unreachable instance: rejected = %v, err = %v; want an error and no verdict", got, err)
	}
}

func TestCreateOrgRefusesAnExistingOrganization(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)

	if err := client.CreateOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := client.CreateOrg(ctx, "acme", "Acme"); !errors.Is(err, giteaadmin.ErrNameUnavailable) {
		t.Fatalf("an organization that exists: err = %v, want ErrNameUnavailable", err)
	}
}

func TestDeleteUser(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)
	fake.AddUser("reader")

	for range 2 {
		if err := client.DeleteUser(ctx, "reader"); err != nil {
			t.Fatalf("deleting, also when already gone: %v", err)
		}
	}
	if fake.HasUser("reader") {
		t.Fatal("the account is still there")
	}
}

func TestDeleteOrgHook(t *testing.T) {
	ctx := context.Background()
	fake := giteaadmintest.New(t)
	client := giteaadmin.New(fake.URL, fake.Token)
	if err := client.EnsureOrg(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"http://backend:8080/one", "http://backend:8080/two"} {
		if err := client.EnsureOrgHook(ctx, "acme", giteaadmin.Hook{URL: url, Secret: "s"}, false); err != nil {
			t.Fatal(err)
		}
	}

	for range 2 {
		if err := client.DeleteOrgHook(ctx, "acme", "http://backend:8080/one"); err != nil {
			t.Fatalf("deleting, also when already gone: %v", err)
		}
	}
	if hooks := fake.Hooks("acme"); len(hooks) != 1 || hooks[0].URL != "http://backend:8080/two" {
		t.Fatalf("hooks = %+v, want only the other one left", hooks)
	}
	if err := client.DeleteOrgHook(ctx, "no-such-org", "http://backend:8080/one"); err != nil {
		t.Errorf("an organization that does not exist: %v", err)
	}
}
