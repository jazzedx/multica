package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/giteaadmin"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The bundled code host gives out two kinds of accounts, so that no runtime
// host needs Git credentials of its own:
//
//   - one read-only account per workspace, handed to the workspace's daemons
//     for cloning and fetching the organization's repositories;
//   - one account per agent, a member of the organization's developers team,
//     whose token travels with each of the agent's tasks. Agents push and open
//     pull requests as themselves, which is also what lets one agent review
//     another's pull request: Gitea does not let an author approve their own.

// builtinCodeHostReadersTeam is the team of the workspace's read-only account.
const builtinCodeHostReadersTeam = "readers"

// builtinCodeHostClaimTimeout bounds creating an agent's account, or replacing
// its token, while a daemon waits to claim that agent's task.
const builtinCodeHostClaimTimeout = 8 * time.Second

// builtinCodeHostTokenCheckTimeout bounds asking the instance whether a token
// still works. When the instance does not answer in time the token is used as
// it is.
const builtinCodeHostTokenCheckTimeout = 3 * time.Second

var (
	// read:user is what lets the server ask whether a token still works.
	builtinCodeHostReaderScopes = []string{"read:repository", "read:user"}
	builtinCodeHostAgentScopes  = []string{"write:repository", "write:issue", "read:user", "read:organization"}
)

// GitCredential tells a daemon how to authenticate git against URLs under
// URL (an address prefix ending in "/").
type GitCredential struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

func builtinCodeHostWorkspaceLock(wsID pgtype.UUID) *sync.Mutex {
	lock, _ := builtinCodeHostLocks.LoadOrStore(uuidToString(wsID), &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (h *Handler) builtinCodeHostPublicURL() string {
	return strings.TrimRight(strings.TrimSpace(h.cfg.BuiltinCodeHost.PublicURL), "/")
}

// builtinCodeHostUsername turns a display name into an account name Gitea
// accepts: lower-case letters, digits and single dashes, starting and ending
// with a letter or digit.
func builtinCodeHostUsername(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 30 {
		out = strings.TrimRight(out[:30], "-")
	}
	return out
}

// createBuiltinCodeHostUser creates an account under the first of candidates
// Gitea accepts and returns its name.
func createBuiltinCodeHostUser(ctx context.Context, client *giteaadmin.Client, candidates []string, fullName, email string) (string, error) {
	var lastErr error
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		lastErr = client.CreateUser(ctx, candidate, fullName, email)
		if lastErr == nil {
			return candidate, nil
		}
		if !errors.Is(lastErr, giteaadmin.ErrNameUnavailable) {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no usable account name")
	}
	return "", lastErr
}

// ensureBuiltinCodeHostReader makes sure the workspace has its read-only
// account. The account's name is stored before its token is issued, so a pass
// that fails in between resumes with the same account.
func (h *Handler) ensureBuiltinCodeHostReader(ctx context.Context, client *giteaadmin.Client, wsID pgtype.UUID, org string) error {
	state, err := h.Queries.GetBuiltinCodeHostWorkspace(ctx, wsID)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if state.ReaderUsername != "" && state.ReaderTokenEncrypted != "" {
		if !h.builtinCodeHostTokenRejected(ctx, state.ReaderTokenEncrypted) {
			return nil
		}
		slog.Info("builtin code host: the reader's token no longer works; issuing a new one", "org", org)
	}
	if err := client.EnsureTeam(ctx, org, builtinCodeHostReadersTeam, giteaadmin.PermissionRead); err != nil {
		return fmt.Errorf("ensure readers team: %w", err)
	}
	username := state.ReaderUsername
	if username == "" {
		short := strings.ReplaceAll(uuidToString(wsID), "-", "")[:6]
		username, err = createBuiltinCodeHostUser(ctx, client,
			[]string{org + "-reader", org + "-reader-" + short},
			org+" (read-only)", "reader-"+uuidToString(wsID)+"@multica.local")
		if err != nil {
			return fmt.Errorf("create reader account: %w", err)
		}
		if err := h.Queries.SetBuiltinCodeHostReader(ctx, db.SetBuiltinCodeHostReaderParams{
			WorkspaceID: wsID, Username: username,
		}); err != nil {
			return fmt.Errorf("save reader account: %w", err)
		}
	}
	if err := client.EnsureTeamMember(ctx, org, builtinCodeHostReadersTeam, username); err != nil {
		return fmt.Errorf("add reader to its team: %w", err)
	}
	token, err := client.IssueToken(ctx, username, builtinCodeHostReaderScopes)
	if err != nil {
		return fmt.Errorf("issue reader token: %w", err)
	}
	tokenEnc, err := h.sealVCSSecret(token)
	if err != nil {
		return err
	}
	if err := h.Queries.SetBuiltinCodeHostReader(ctx, db.SetBuiltinCodeHostReaderParams{
		WorkspaceID: wsID, Username: username, TokenEncrypted: tokenEnc,
	}); err != nil {
		return fmt.Errorf("save reader token: %w", err)
	}
	return nil
}

// ensureBuiltinCodeHostAgentAccount makes sure the agent has an account with a
// token and belongs to the developers team. Like the reader's, the account's
// name is stored before its token is issued.
func (h *Handler) ensureBuiltinCodeHostAgentAccount(ctx context.Context, client *giteaadmin.Client, wsID pgtype.UUID, org string, agentID pgtype.UUID, agentName string) (db.BuiltinCodeHostAccount, error) {
	account, err := h.Queries.GetBuiltinCodeHostAccount(ctx, agentID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.BuiltinCodeHostAccount{}, fmt.Errorf("load account: %w", err)
	}
	if err == nil && account.TokenEncrypted != "" && account.InTeam {
		return account, nil
	}
	username := account.Username
	if username == "" {
		base := builtinCodeHostUsername(agentName)
		if base == "" {
			base = "agent"
		}
		short := strings.ReplaceAll(uuidToString(agentID), "-", "")[:6]
		username, err = createBuiltinCodeHostUser(ctx, client,
			[]string{base, base + "-" + short},
			agentName, "agent-"+uuidToString(agentID)+"@multica.local")
		if err != nil {
			return db.BuiltinCodeHostAccount{}, fmt.Errorf("create account: %w", err)
		}
		if account, err = h.Queries.UpsertBuiltinCodeHostAccount(ctx, db.UpsertBuiltinCodeHostAccountParams{
			AgentID: agentID, WorkspaceID: wsID, Username: username,
		}); err != nil {
			return db.BuiltinCodeHostAccount{}, fmt.Errorf("save account: %w", err)
		}
	}
	if !account.InTeam {
		// A restored agent's account was blocked when the agent was archived.
		if err := client.SetLoginProhibited(ctx, username, false); err != nil {
			return db.BuiltinCodeHostAccount{}, fmt.Errorf("unblock account: %w", err)
		}
	}
	if err := client.EnsureTeamMember(ctx, org, builtinCodeHostTeam, username); err != nil {
		return db.BuiltinCodeHostAccount{}, fmt.Errorf("add account to the developers team: %w", err)
	}
	tokenEnc := account.TokenEncrypted
	if tokenEnc == "" {
		token, err := client.IssueToken(ctx, username, builtinCodeHostAgentScopes)
		if err != nil {
			return db.BuiltinCodeHostAccount{}, fmt.Errorf("issue token: %w", err)
		}
		if tokenEnc, err = h.sealVCSSecret(token); err != nil {
			return db.BuiltinCodeHostAccount{}, err
		}
	}
	account, err = h.Queries.UpsertBuiltinCodeHostAccount(ctx, db.UpsertBuiltinCodeHostAccountParams{
		AgentID: agentID, WorkspaceID: wsID, Username: username, TokenEncrypted: tokenEnc,
	})
	if err != nil {
		return db.BuiltinCodeHostAccount{}, fmt.Errorf("save token: %w", err)
	}
	return account, nil
}

// reconcileBuiltinCodeHostAgents gives every active agent of the workspace its
// account. An archived agent's account leaves the developers team and is
// blocked, which disables its token; the account itself stays, so the agent's
// commits and pull requests keep their author. One agent's failure does not
// stop the others.
func (h *Handler) reconcileBuiltinCodeHostAgents(ctx context.Context, client *giteaadmin.Client, wsID pgtype.UUID, org string) error {
	agents, err := h.Queries.ListAgentsForBuiltinCodeHost(ctx, wsID)
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	var firstErr error
	for _, agent := range agents {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var err error
		switch {
		case agent.Archived:
			if agent.Username == "" || !agent.InTeam {
				continue
			}
			if err = client.RemoveTeamMember(ctx, org, builtinCodeHostTeam, agent.Username); err == nil {
				err = client.SetLoginProhibited(ctx, agent.Username, true)
			}
			if err == nil {
				err = h.Queries.SetBuiltinCodeHostAccountInTeam(ctx, db.SetBuiltinCodeHostAccountInTeamParams{AgentID: agent.ID, InTeam: false})
			}
		case agent.HasToken && agent.InTeam:
			continue
		default:
			_, err = h.ensureBuiltinCodeHostAgentAccount(ctx, client, wsID, org, agent.ID, agent.Name)
		}
		if err != nil {
			slog.Warn("builtin code host: agent account failed", "agent_id", uuidToString(agent.ID), "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// builtinCodeHostReaderCredentials is what a daemon of the workspace needs to
// clone and fetch the organization's repositories, or nil when the workspace
// is not linked (yet) or the instance has no public address.
func (h *Handler) builtinCodeHostReaderCredentials(ctx context.Context, wsID pgtype.UUID) []GitCredential {
	base := h.builtinCodeHostPublicURL()
	if !h.builtinCodeHostEnabled() || base == "" {
		return nil
	}
	state, err := h.Queries.GetBuiltinCodeHostWorkspace(ctx, wsID)
	if err != nil || state.ReaderUsername == "" || state.ReaderTokenEncrypted == "" {
		return nil
	}
	token, err := h.openVCSSecret(state.ReaderTokenEncrypted)
	if err != nil || token == "" {
		return nil
	}
	return []GitCredential{{
		URL:      base + "/" + state.OrgName + "/",
		Username: state.ReaderUsername,
		Token:    token,
	}}
}

// withBuiltinCodeHostEnv adds the agent's own code host credentials to the
// environment its task runs with: variables for the API, and git settings
// that authenticate pushes and fetches without anything on the runtime host.
// The agent's configured variables win, and nothing is added for a workspace
// that is not linked. The agent's account is created here when the task
// arrives before the background pass got to it.
func (h *Handler) withBuiltinCodeHostEnv(ctx context.Context, agent db.Agent, env map[string]string) map[string]string {
	base := h.builtinCodeHostPublicURL()
	if !h.builtinCodeHostEnabled() || base == "" || agent.Kind != "user" {
		return env
	}
	state, err := h.Queries.GetBuiltinCodeHostWorkspace(ctx, agent.WorkspaceID)
	if err != nil {
		return env
	}
	account, err := h.Queries.GetBuiltinCodeHostAccount(ctx, agent.ID)
	if err != nil || account.TokenEncrypted == "" || !account.InTeam {
		account, err = h.ensureBuiltinCodeHostAgentAccountNow(ctx, agent, state.OrgName)
		if err != nil {
			slog.Warn("builtin code host: no account for the task's agent", "agent_id", uuidToString(agent.ID), "err", err)
			return env
		}
	}
	if h.builtinCodeHostTokenRejected(ctx, account.TokenEncrypted) {
		// Someone deleted the token on the instance. Without this the agent
		// would only find out when its push is refused.
		account, err = h.reissueBuiltinCodeHostAgentToken(ctx, agent, state.OrgName, account.TokenEncrypted)
		if err != nil {
			slog.Warn("builtin code host: could not replace a rejected token", "agent_id", uuidToString(agent.ID), "err", err)
			return env
		}
	}
	token, err := h.openVCSSecret(account.TokenEncrypted)
	if err != nil || token == "" {
		return env
	}

	out := make(map[string]string, len(env)+8)
	for k, v := range env {
		out[k] = v
	}
	add := func(key, value string) {
		if _, set := out[key]; !set {
			out[key] = value
		}
	}
	add("GITEA_URL", base)
	add("GITEA_API_URL", base+"/api/v1")
	add("GITEA_ORG", state.OrgName)
	add("GITEA_USER", account.Username)
	add("GITEA_TOKEN", token)
	// Git reads settings from these variables. An agent that sets its own is
	// left alone: the numbered entries cannot be merged safely from here.
	if _, own := out["GIT_CONFIG_COUNT"]; !own {
		basic := base64.StdEncoding.EncodeToString([]byte(account.Username + ":" + token))
		out["GIT_CONFIG_COUNT"] = "1"
		out["GIT_CONFIG_KEY_0"] = "http." + base + "/.extraheader"
		out["GIT_CONFIG_VALUE_0"] = "Authorization: Basic " + basic
	}
	return out
}

func (h *Handler) ensureBuiltinCodeHostAgentAccountNow(ctx context.Context, agent db.Agent, org string) (db.BuiltinCodeHostAccount, error) {
	token := h.builtinCodeHostToken()
	if token == "" {
		return db.BuiltinCodeHostAccount{}, errors.New("no service token")
	}
	ctx, cancel := context.WithTimeout(ctx, builtinCodeHostClaimTimeout)
	defer cancel()
	lock := builtinCodeHostWorkspaceLock(agent.WorkspaceID)
	lock.Lock()
	defer lock.Unlock()
	client := giteaadmin.New(h.builtinCodeHostURL(), token)
	return h.ensureBuiltinCodeHostAgentAccount(ctx, client, agent.WorkspaceID, org, agent.ID, agent.Name)
}

// builtinCodeHostTokenRejected reports whether the instance refuses the stored
// token. A token that cannot be checked right now counts as working.
func (h *Handler) builtinCodeHostTokenRejected(ctx context.Context, tokenEncrypted string) bool {
	token, err := h.openVCSSecret(tokenEncrypted)
	if err != nil || token == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, builtinCodeHostTokenCheckTimeout)
	defer cancel()
	rejected, err := giteaadmin.New(h.builtinCodeHostURL(), token).TokenRejected(ctx)
	return err == nil && rejected
}

// reissueBuiltinCodeHostAgentToken replaces the agent's token. rejected is the
// stored token the caller found refused: when another request has replaced it
// in the meantime, that newer token is returned instead of issuing a third.
func (h *Handler) reissueBuiltinCodeHostAgentToken(ctx context.Context, agent db.Agent, org, rejected string) (db.BuiltinCodeHostAccount, error) {
	serviceToken := h.builtinCodeHostToken()
	if serviceToken == "" {
		return db.BuiltinCodeHostAccount{}, errors.New("no service token")
	}
	ctx, cancel := context.WithTimeout(ctx, builtinCodeHostClaimTimeout)
	defer cancel()
	lock := builtinCodeHostWorkspaceLock(agent.WorkspaceID)
	lock.Lock()
	defer lock.Unlock()

	account, err := h.Queries.GetBuiltinCodeHostAccount(ctx, agent.ID)
	if err != nil {
		return db.BuiltinCodeHostAccount{}, fmt.Errorf("load account: %w", err)
	}
	if account.TokenEncrypted != "" && account.TokenEncrypted != rejected {
		return account, nil
	}
	client := giteaadmin.New(h.builtinCodeHostURL(), serviceToken)
	// The token may be refused because the account is blocked rather than
	// because it was deleted; an active agent's account must not be.
	if err := client.SetLoginProhibited(ctx, account.Username, false); err != nil {
		return db.BuiltinCodeHostAccount{}, fmt.Errorf("unblock account: %w", err)
	}
	if err := client.EnsureTeamMember(ctx, org, builtinCodeHostTeam, account.Username); err != nil {
		return db.BuiltinCodeHostAccount{}, fmt.Errorf("add account to the developers team: %w", err)
	}
	token, err := client.IssueToken(ctx, account.Username, builtinCodeHostAgentScopes)
	if err != nil {
		return db.BuiltinCodeHostAccount{}, fmt.Errorf("issue token: %w", err)
	}
	tokenEnc, err := h.sealVCSSecret(token)
	if err != nil {
		return db.BuiltinCodeHostAccount{}, err
	}
	account, err = h.Queries.UpsertBuiltinCodeHostAccount(ctx, db.UpsertBuiltinCodeHostAccountParams{
		AgentID: agent.ID, WorkspaceID: agent.WorkspaceID, Username: account.Username, TokenEncrypted: tokenEnc,
	})
	if err != nil {
		return db.BuiltinCodeHostAccount{}, fmt.Errorf("save token: %w", err)
	}
	slog.Info("builtin code host: replaced a rejected agent token", "agent_id", uuidToString(agent.ID))
	return account, nil
}
