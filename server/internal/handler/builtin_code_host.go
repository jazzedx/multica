package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/giteaadmin"
	"github.com/multica-ai/multica/server/internal/integrations/vcs"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// BuiltinCodeHostConfig describes a Gitea deployed beside this server (the
// self-host stack's gitea add-on). When URL is set, the server links every
// workspace to it without anyone pasting tokens or webhook secrets: it creates
// an organization for the workspace, a connection, a webhook, and adds the
// organization's repositories to the workspace's list.
type BuiltinCodeHostConfig struct {
	// URL is where this server reaches the instance's API, usually an address
	// inside the deployment (http://gitea:3000). Empty turns the feature off.
	URL string
	// PublicURL is where people open the instance, used for links only.
	PublicURL string
	// WebhookBaseURL is where the instance reaches this server
	// (http://backend:8080). Falls back to Config.PublicURL.
	WebhookBaseURL string
	// Owner is the login of the person who administers the instance. It is
	// added to the owners of every workspace's organization, so the
	// organizations show up for them like ones they created. Optional.
	Owner string
	// Token is an admin access token of the service account. TokenFile is read
	// on every use instead, when set: the stack writes it after this server has
	// started.
	Token     string
	TokenFile string
}

// builtinCodeHostTeam is the team a workspace's organization gets, whose
// members can push to every repository of the organization.
const builtinCodeHostTeam = "developers"

// builtinCodeHostOwnersTeam is the team Gitea gives every organization, whose
// members own it.
const builtinCodeHostOwnersTeam = "Owners"

// builtinCodeHostTimeout bounds one background reconcile of a workspace.
const builtinCodeHostTimeout = 30 * time.Second

// builtinCodeHostHookEvents are the events the organization webhook delivers:
// what the pull-request mirror reads, plus repository changes for the
// workspace's repository list.
var builtinCodeHostHookEvents = []string{"pull_request", "status", "repository"}

// builtinCodeHostLocks serializes the reconcile of one workspace, which is
// triggered from several places at once (startup, the sweep, a webhook).
var builtinCodeHostLocks sync.Map

func (h *Handler) builtinCodeHostURL() string {
	return vcs.NormalizeInstanceURL(h.cfg.BuiltinCodeHost.URL)
}

func (h *Handler) builtinCodeHostEnabled() bool {
	return h.builtinCodeHostURL() != "" && h.isVCSAvailable() && h.isVCSConfigured()
}

func (h *Handler) builtinCodeHostToken() string {
	cfg := h.cfg.BuiltinCodeHost
	if cfg.TokenFile != "" {
		if raw, err := os.ReadFile(cfg.TokenFile); err == nil {
			if token := strings.TrimSpace(string(raw)); token != "" {
				return token
			}
		}
	}
	return strings.TrimSpace(cfg.Token)
}

// isBuiltinCodeHostConnection reports whether conn is the one this server
// keeps for the bundled instance, as opposed to one a member connected.
func (h *Handler) isBuiltinCodeHostConnection(conn db.VcsConnection) bool {
	builtin := h.builtinCodeHostURL()
	return builtin != "" && conn.InstanceUrl == builtin
}

// builtinCodeHostOrgURL is the organization's page for people, or "" when the
// public address is not configured.
func (h *Handler) builtinCodeHostOrgURL(org string) string {
	base := strings.TrimRight(strings.TrimSpace(h.cfg.BuiltinCodeHost.PublicURL), "/")
	if base == "" || org == "" {
		return ""
	}
	return base + "/" + org
}

func (h *Handler) builtinCodeHostWebhookURL(connID string) string {
	base := strings.TrimRight(strings.TrimSpace(h.cfg.BuiltinCodeHost.WebhookBaseURL), "/")
	if base == "" {
		base = strings.TrimRight(h.cfg.PublicURL, "/")
	}
	if base == "" {
		return ""
	}
	return base + h.vcsWebhookPath(connID)
}

// ReconcileBuiltinCodeHost links every workspace to the bundled instance.
// refresh also rewrites webhooks that already exist, which repairs one whose
// secret no longer matches; the periodic sweep leaves them alone. It reports
// false when the instance could not be worked on yet (no service token, or it
// does not answer), so the caller can try again soon.
func (h *Handler) ReconcileBuiltinCodeHost(ctx context.Context, refresh bool) bool {
	if !h.builtinCodeHostEnabled() {
		return true
	}
	token := h.builtinCodeHostToken()
	if token == "" {
		slog.Info("builtin code host: no service token yet")
		return false
	}
	if _, err := giteaadmin.New(h.builtinCodeHostURL(), token).CurrentLogin(ctx); err != nil {
		slog.Info("builtin code host: instance not ready", "err", err)
		return false
	}
	if _, err := h.Queries.DeleteOrphanBuiltinCodeHostAccounts(ctx); err != nil {
		slog.Warn("builtin code host: drop orphan accounts failed", "err", err)
	}
	workspaces, err := h.Queries.ListWorkspacesForBuiltinCodeHost(ctx)
	if err != nil {
		slog.Warn("builtin code host: list workspaces failed", "err", err)
		return false
	}
	for _, ws := range workspaces {
		if ctx.Err() != nil {
			return false
		}
		if err := h.reconcileBuiltinCodeHostWorkspace(ctx, ws.ID, ws.Name, ws.Slug, refresh); err != nil {
			slog.Warn("builtin code host: reconcile failed", "workspace_id", uuidToString(ws.ID), "err", err)
		}
	}
	return true
}

// reconcileBuiltinCodeHostWorkspaceAsync links one workspace in the
// background, for callers that must not wait on the instance (a request that
// just created the workspace, a webhook delivery).
func (h *Handler) reconcileBuiltinCodeHostWorkspaceAsync(wsID pgtype.UUID) {
	if !h.builtinCodeHostEnabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), builtinCodeHostTimeout)
		defer cancel()
		ws, err := h.Queries.GetWorkspace(ctx, wsID)
		if err != nil {
			return
		}
		if err := h.reconcileBuiltinCodeHostWorkspace(ctx, ws.ID, ws.Name, ws.Slug, false); err != nil {
			slog.Warn("builtin code host: reconcile failed", "workspace_id", uuidToString(ws.ID), "err", err)
		}
	}()
}

func (h *Handler) reconcileBuiltinCodeHostWorkspace(ctx context.Context, wsID pgtype.UUID, name, slug string, refresh bool) error {
	token := h.builtinCodeHostToken()
	if token == "" {
		return errors.New("no service token")
	}
	lock := builtinCodeHostWorkspaceLock(wsID)
	lock.Lock()
	defer lock.Unlock()

	client := giteaadmin.New(h.builtinCodeHostURL(), token)

	org, err := h.ensureBuiltinCodeHostOrg(ctx, client, wsID, name, slug)
	if err != nil {
		return err
	}
	if err := client.EnsureTeam(ctx, org, builtinCodeHostTeam, giteaadmin.PermissionWrite); err != nil {
		return fmt.Errorf("ensure team: %w", err)
	}
	if owner := strings.TrimSpace(h.cfg.BuiltinCodeHost.Owner); owner != "" {
		// The rest does not depend on it: a renamed or removed administrator
		// must not stop the workspace from being linked.
		if err := client.EnsureTeamMember(ctx, org, builtinCodeHostOwnersTeam, owner); err != nil {
			slog.Warn("builtin code host: add owner failed", "org", org, "owner", owner, "err", err)
		}
	}
	conn, created, err := h.ensureBuiltinCodeHostConnection(ctx, client, wsID, token)
	if err != nil {
		return err
	}
	if err := h.ensureBuiltinCodeHostHook(ctx, client, org, conn, created || refresh); err != nil {
		return err
	}
	if err := h.syncBuiltinCodeHostRepos(ctx, client, wsID, org); err != nil {
		return err
	}
	if err := h.ensureBuiltinCodeHostReader(ctx, client, wsID, org); err != nil {
		return err
	}
	return h.reconcileBuiltinCodeHostAgents(ctx, client, wsID, org)
}

// ensureBuiltinCodeHostOrg returns the workspace's organization, creating it
// on first use. It is named after the workspace's slug; when Gitea refuses
// that name (reserved, or taken by a user or another organization) a prefixed
// one is tried, and then one made unique by the workspace's id. The
// name is stored, so a later slug change does not move the workspace to
// another organization.
//
// A workspace only ever gets an organization it created: one that already
// exists under its slug belongs to someone else, typically a deleted workspace
// whose repositories were kept, and linking it would hand those repositories
// to this workspace's members.
func (h *Handler) ensureBuiltinCodeHostOrg(ctx context.Context, client *giteaadmin.Client, wsID pgtype.UUID, name, slug string) (string, error) {
	state, err := h.Queries.GetBuiltinCodeHostWorkspace(ctx, wsID)
	switch {
	case err == nil:
		if err := client.EnsureOrg(ctx, state.OrgName, name); err != nil {
			return "", fmt.Errorf("ensure organization: %w", err)
		}
		return state.OrgName, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("load state: %w", err)
	}

	var lastErr error
	for _, candidate := range []string{slug, "ws-" + slug, slug + "-" + uuidToString(wsID)[:8]} {
		lastErr = client.CreateOrg(ctx, candidate, name)
		if lastErr == nil {
			if _, err := h.Queries.UpsertBuiltinCodeHostWorkspace(ctx, db.UpsertBuiltinCodeHostWorkspaceParams{
				WorkspaceID: wsID,
				OrgName:     candidate,
			}); err != nil {
				return "", fmt.Errorf("save state: %w", err)
			}
			return candidate, nil
		}
		if !errors.Is(lastErr, giteaadmin.ErrNameUnavailable) {
			break
		}
	}
	return "", fmt.Errorf("ensure organization: %w", lastErr)
}

// ensureBuiltinCodeHostConnection returns the workspace's connection to the
// bundled instance, creating it or replacing its token when the service token
// changed. created reports that the webhook secret is new, so the webhook has
// to be rewritten with it.
func (h *Handler) ensureBuiltinCodeHostConnection(ctx context.Context, client *giteaadmin.Client, wsID pgtype.UUID, token string) (conn db.VcsConnection, created bool, err error) {
	instanceURL := h.builtinCodeHostURL()
	existing, err := h.Queries.GetVCSConnectionByWorkspaceAndInstance(ctx, db.GetVCSConnectionByWorkspaceAndInstanceParams{
		WorkspaceID: wsID,
		InstanceUrl: instanceURL,
	})
	switch {
	case err == nil:
		if stored, openErr := h.openVCSSecret(existing.AccessTokenEncrypted); openErr == nil && stored == token {
			return existing, false, nil
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return db.VcsConnection{}, false, fmt.Errorf("load connection: %w", err)
	}

	login, err := client.CurrentLogin(ctx)
	if err != nil {
		return db.VcsConnection{}, false, fmt.Errorf("check service token: %w", err)
	}
	secret, err := newVCSWebhookSecret()
	if err != nil {
		return db.VcsConnection{}, false, err
	}
	tokenEnc, err := h.sealVCSSecret(token)
	if err != nil {
		return db.VcsConnection{}, false, err
	}
	secretEnc, err := h.sealVCSSecret(secret)
	if err != nil {
		return db.VcsConnection{}, false, err
	}
	conn, err = h.Queries.UpsertVCSConnection(ctx, db.UpsertVCSConnectionParams{
		WorkspaceID:            wsID,
		Provider:               string(vcs.KindGitea),
		InstanceUrl:            instanceURL,
		AccountLogin:           login,
		AccessTokenEncrypted:   tokenEnc,
		WebhookSecretEncrypted: secretEnc,
	})
	if err != nil {
		return db.VcsConnection{}, false, fmt.Errorf("save connection: %w", err)
	}
	h.publish(protocol.EventVCSConnectionCreated, uuidToString(wsID), "system", "", map[string]any{
		"id": uuidToString(conn.ID),
	})
	return conn, true, nil
}

func (h *Handler) ensureBuiltinCodeHostHook(ctx context.Context, client *giteaadmin.Client, org string, conn db.VcsConnection, refresh bool) error {
	hookURL := h.builtinCodeHostWebhookURL(uuidToString(conn.ID))
	if hookURL == "" {
		return errors.New("no address for the code host to reach this server (MULTICA_BUILTIN_GITEA_WEBHOOK_BASE_URL)")
	}
	secret, err := h.openVCSSecret(conn.WebhookSecretEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt webhook secret: %w", err)
	}
	if err := client.EnsureOrgHook(ctx, org, giteaadmin.Hook{
		URL:    hookURL,
		Secret: secret,
		Events: builtinCodeHostHookEvents,
	}, refresh); err != nil {
		return fmt.Errorf("ensure webhook: %w", err)
	}
	return nil
}

// syncBuiltinCodeHostRepos adds the organization's repositories to the
// workspace's list. Each repository is added once: one a member later removes
// from the list stays removed, and one deleted on the instance is left for a
// member to remove.
func (h *Handler) syncBuiltinCodeHostRepos(ctx context.Context, client *giteaadmin.Client, wsID pgtype.UUID, org string) error {
	repos, err := client.ListOrgRepositories(ctx, org)
	if err != nil {
		return fmt.Errorf("list repositories: %w", err)
	}
	if len(repos) == 0 {
		return nil
	}
	state, err := h.Queries.GetBuiltinCodeHostWorkspace(ctx, wsID)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	ws, err := h.Queries.GetWorkspace(ctx, wsID)
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}
	var listed []workspaceRepoRef
	if len(ws.Repos) > 0 {
		_ = json.Unmarshal(ws.Repos, &listed)
	}
	known := make(map[string]struct{}, len(state.SyncedRepos)+len(repos))
	for _, fullName := range state.SyncedRepos {
		known[fullName] = struct{}{}
	}
	inList := make(map[string]struct{}, len(listed))
	for _, repo := range listed {
		inList[repo.URL] = struct{}{}
	}

	var seen []string
	var added []workspaceRepoRef
	for _, repo := range repos {
		url := h.builtinCodeHostCloneURL(repo)
		if url == "" || repo.FullName == "" {
			continue
		}
		if _, ok := known[repo.FullName]; ok {
			continue
		}
		known[repo.FullName] = struct{}{}
		seen = append(seen, repo.FullName)
		if _, ok := inList[url]; ok {
			continue
		}
		added = append(added, workspaceRepoRef{URL: url, Description: strings.TrimSpace(repo.Description)})
	}
	if len(seen) == 0 {
		return nil
	}
	if len(added) > 0 {
		raw, err := json.Marshal(added)
		if err != nil {
			return err
		}
		updated, err := h.Queries.AppendWorkspaceRepos(ctx, db.AppendWorkspaceReposParams{ID: wsID, Repos: raw})
		if err != nil {
			return fmt.Errorf("add repositories: %w", err)
		}
		h.publish(protocol.EventWorkspaceUpdated, uuidToString(wsID), "system", "", map[string]any{
			"workspace": h.workspaceToResponse(updated),
		})
	}
	if err := h.Queries.AddBuiltinCodeHostSyncedRepos(ctx, db.AddBuiltinCodeHostSyncedReposParams{
		WorkspaceID: wsID,
		Repos:       seen,
	}); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

// builtinCodeHostCloneURL is the address runtimes clone repo from. The
// instance reports clone URLs for the address it was asked on, which is the
// one inside the deployment, so the public address is used when configured.
func (h *Handler) builtinCodeHostCloneURL(repo giteaadmin.Repository) string {
	base := strings.TrimRight(strings.TrimSpace(h.cfg.BuiltinCodeHost.PublicURL), "/")
	if base == "" || repo.FullName == "" {
		return strings.TrimSpace(repo.CloneURL)
	}
	return base + "/" + repo.FullName + ".git"
}

// builtinCodeHostOrgs returns the organization of each workspace connection
// in rows that belongs to the bundled instance, keyed by connection id.
func (h *Handler) builtinCodeHostOrgs(ctx context.Context, rows []db.VcsConnection) map[string]string {
	out := map[string]string{}
	for _, row := range rows {
		if !h.isBuiltinCodeHostConnection(row) {
			continue
		}
		org := ""
		if state, err := h.Queries.GetBuiltinCodeHostWorkspace(ctx, row.WorkspaceID); err == nil {
			org = state.OrgName
		}
		out[uuidToString(row.ID)] = org
	}
	return out
}
