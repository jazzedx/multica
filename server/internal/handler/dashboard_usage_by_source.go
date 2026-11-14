package handler

import (
	"net/http"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DashboardUsageBySourceResponse is one (source, agent, provider, model) row.
// Source is "chat", "issue", "autopilot" or "other"; the rest is the shape
// DashboardUsageByAgentResponse uses, so the client prices it the same way and
// folds it by source, by agent, or both.
type DashboardUsageBySourceResponse struct {
	Source                   string `json:"source"`
	AgentID                  string `json:"agent_id"`
	Provider                 string `json:"provider"`
	Model                    string `json:"model"`
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	CacheReadTokens          int64  `json:"cache_read_tokens"`
	CacheWriteTokens         int64  `json:"cache_write_tokens"`
	CostUSDTicks             int64  `json:"cost_usd_ticks"`
	UncostedInputTokens      int64  `json:"uncosted_input_tokens"`
	UncostedOutputTokens     int64  `json:"uncosted_output_tokens"`
	UncostedCacheReadTokens  int64  `json:"uncosted_cache_read_tokens"`
	UncostedCacheWriteTokens int64  `json:"uncosted_cache_write_tokens"`
	TaskCount                int32  `json:"task_count"`
}

// GetDashboardUsageBySource — GET /api/dashboard/usage/by-source
//
// Splits each agent's usage by where the work came from. It takes the same
// ?days, ?tz and ?project_id as the other dashboard endpoints and, having no
// date dimension, the exact-N-days cutoff the per-agent rollups use, so it
// covers the window the KPIs beside it cover.
//
// It names agents, so it applies the per-agent rollups' visibility rule: rows
// of an agent the viewer may not see are folded onto one bucket per source
// (see foldRestrictedAgents). A chat's content stays private either way; how
// much an agent spent in chats is a figure its per-agent total already holds.
func (h *Handler) GetDashboardUsageBySource(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	projectID, ok := parseProjectIDParam(w, r)
	if !ok {
		return
	}
	if !h.dashboardProjectVisible(w, r, workspaceID, projectID) {
		return
	}
	restricted, ok := h.dashboardRestrictedAgents(w, r, workspaceID, member.Role)
	if !ok {
		return
	}
	tz := h.resolveViewingTZ(r)
	since := parseExactSinceParamInTZ(r, 30, tz)

	rows, err := h.Queries.ListDashboardUsageBySource(r.Context(), db.ListDashboardUsageBySourceParams{
		WorkspaceID: parseUUID(workspaceID),
		Since:       since,
		ProjectID:   projectID,
	})
	if err != nil {
		if writeQueryCancellation(w, r, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to list usage by source")
		return
	}
	resp := make([]DashboardUsageBySourceResponse, len(rows))
	for i, row := range rows {
		resp[i] = DashboardUsageBySourceResponse{
			Source:                   row.Source,
			AgentID:                  uuidToString(row.AgentID),
			Provider:                 row.Provider,
			Model:                    row.Model,
			InputTokens:              row.InputTokens,
			OutputTokens:             row.OutputTokens,
			CacheReadTokens:          row.CacheReadTokens,
			CacheWriteTokens:         row.CacheWriteTokens,
			CostUSDTicks:             row.CostUsdTicks,
			UncostedInputTokens:      row.UncostedInputTokens,
			UncostedOutputTokens:     row.UncostedOutputTokens,
			UncostedCacheReadTokens:  row.UncostedCacheReadTokens,
			UncostedCacheWriteTokens: row.UncostedCacheWriteTokens,
			TaskCount:                row.TaskCount,
		}
	}
	writeJSON(w, http.StatusOK, foldRestrictedUsageBySource(resp, restricted))
}

// foldRestrictedUsageBySource is foldRestrictedUsageByAgent with the source
// kept: the bucket stays split by source as well as by (provider, model), so
// each source still adds up after the fold.
func foldRestrictedUsageBySource(
	rows []DashboardUsageBySourceResponse,
	restricted map[string]struct{},
) []DashboardUsageBySourceResponse {
	type key struct {
		source string
		providerModelKey
	}
	return foldRestrictedAgents(
		rows,
		restricted,
		func(row DashboardUsageBySourceResponse) string { return row.AgentID },
		func(row DashboardUsageBySourceResponse) (DashboardUsageBySourceResponse, key) {
			k := key{source: row.Source, providerModelKey: providerModelKey{provider: row.Provider, model: row.Model}}
			row.AgentID = restrictedAgentsRowID
			return row, k
		},
		func(dst, src DashboardUsageBySourceResponse) DashboardUsageBySourceResponse {
			dst.InputTokens += src.InputTokens
			dst.OutputTokens += src.OutputTokens
			dst.CacheReadTokens += src.CacheReadTokens
			dst.CacheWriteTokens += src.CacheWriteTokens
			dst.CostUSDTicks += src.CostUSDTicks
			dst.UncostedInputTokens += src.UncostedInputTokens
			dst.UncostedOutputTokens += src.UncostedOutputTokens
			dst.UncostedCacheReadTokens += src.UncostedCacheReadTokens
			dst.UncostedCacheWriteTokens += src.UncostedCacheWriteTokens
			dst.TaskCount += src.TaskCount
			return dst
		},
	)
}
