package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/giteaadmin"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// builtinCodeHostHoldings is what a workspace holds on the bundled instance
// besides its organization and repositories.
type builtinCodeHostHoldings struct {
	org     string
	reader  string
	agents  []string
	hookURL string
}

// builtinCodeHostHoldingsOf reads what a workspace holds on the bundled
// instance. It has to run before the workspace's rows are deleted, because
// they are the only record of it. ok is false when the workspace was never
// linked.
func (h *Handler) builtinCodeHostHoldingsOf(ctx context.Context, wsID pgtype.UUID) (builtinCodeHostHoldings, bool) {
	if !h.builtinCodeHostEnabled() {
		return builtinCodeHostHoldings{}, false
	}
	state, err := h.Queries.GetBuiltinCodeHostWorkspace(ctx, wsID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("builtin code host: load state before delete failed", "workspace_id", uuidToString(wsID), "err", err)
		}
		return builtinCodeHostHoldings{}, false
	}
	held := builtinCodeHostHoldings{org: state.OrgName, reader: state.ReaderUsername}
	if agents, err := h.Queries.ListBuiltinCodeHostAccountUsernames(ctx, wsID); err == nil {
		held.agents = agents
	} else {
		slog.Warn("builtin code host: list accounts before delete failed", "workspace_id", uuidToString(wsID), "err", err)
	}
	if conn, err := h.Queries.GetVCSConnectionByWorkspaceAndInstance(ctx, db.GetVCSConnectionByWorkspaceAndInstanceParams{
		WorkspaceID: wsID,
		InstanceUrl: h.builtinCodeHostURL(),
	}); err == nil {
		held.hookURL = h.builtinCodeHostWebhookURL(uuidToString(conn.ID))
	}
	return held, true
}

// releaseBuiltinCodeHostAsync takes back what a deleted workspace held on the
// bundled instance. The organization and its repositories stay, owned by the
// administrator, so deleting a workspace never deletes code. What only served
// the workspace goes: its webhook and read-only account are removed, and its
// agents' accounts lose write access and may no longer sign in. Those accounts
// are kept, like an archived agent's, so what they wrote stays attributed.
func (h *Handler) releaseBuiltinCodeHostAsync(held builtinCodeHostHoldings) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), builtinCodeHostTimeout)
		defer cancel()
		h.releaseBuiltinCodeHost(ctx, held)
	}()
}

func (h *Handler) releaseBuiltinCodeHost(ctx context.Context, held builtinCodeHostHoldings) {
	token := h.builtinCodeHostToken()
	if token == "" {
		slog.Warn("builtin code host: release skipped, no service token", "org", held.org)
		return
	}
	client := giteaadmin.New(h.builtinCodeHostURL(), token)
	warn := func(what string, err error) {
		if err != nil {
			slog.Warn("builtin code host: release of a deleted workspace incomplete", "org", held.org, "step", what, "err", err)
		}
	}
	if held.hookURL != "" {
		warn("delete webhook", client.DeleteOrgHook(ctx, held.org, held.hookURL))
	}
	if held.reader != "" {
		// Gitea refuses to delete an account that still belongs to an
		// organization.
		if err := client.RemoveTeamMember(ctx, held.org, builtinCodeHostReadersTeam, held.reader); err != nil && !errors.Is(err, giteaadmin.ErrNotFound) {
			warn("remove read-only account from team", err)
		}
		warn("delete read-only account", client.DeleteUser(ctx, held.reader))
	}
	for _, agent := range held.agents {
		if err := client.RemoveTeamMember(ctx, held.org, builtinCodeHostTeam, agent); err != nil && !errors.Is(err, giteaadmin.ErrNotFound) {
			warn("remove agent from team", err)
		}
		if err := client.SetLoginProhibited(ctx, agent, true); err != nil && !errors.Is(err, giteaadmin.ErrNotFound) {
			warn("block agent account", err)
		}
	}
}
