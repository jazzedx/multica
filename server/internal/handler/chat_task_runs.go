package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/logger"
)

// ListChatSessionTaskRuns — GET /api/chat/sessions/{sessionId}/task-runs
//
// The execution history behind a chat: every run the session produced, newest
// first, each with the usage it reported. It is the chat counterpart of an
// issue's task runs, and carries the same shape so the same transcript and
// usage views read it.
//
// A chat is private to the member who started it, so this goes through the
// gate every other read of the session uses; a run's content never leaves it.
func (h *Handler) ListChatSessionTaskRuns(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	session, ok := h.gatePublicChatSessionForUser(w, r, userID, workspaceID, chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}

	tasks, err := h.Queries.ListTasksByChatSession(r.Context(), session.ID)
	if err != nil {
		if writeQueryCancellation(w, r, err) {
			return
		}
		slog.Warn("list chat task runs failed", append(logger.RequestAttrs(r), "error", err, "chat_session_id", uuidToString(session.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to list chat task runs")
		return
	}
	tasks = visibleTaskHistory(tasks)
	resp := make([]AgentTaskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = taskToResponse(t, workspaceID)
	}
	h.hydrateTaskAttributions(r.Context(), attributionsOf(resp))

	if len(resp) > 0 {
		rows, err := h.Queries.ListChatSessionTaskUsage(r.Context(), session.ID)
		if err != nil {
			if writeQueryCancellation(w, r, err) {
				return
			}
			slog.Warn("list chat task usage failed", append(logger.RequestAttrs(r), "error", err, "chat_session_id", uuidToString(session.ID))...)
			writeError(w, http.StatusInternalServerError, "failed to list chat task usage")
			return
		}
		byTask := make(map[string][]TaskUsageData, len(resp))
		for _, row := range rows {
			appendTaskUsage(byTask, row.TaskID, row.Provider, row.Model,
				row.InputTokens, row.OutputTokens, row.CacheReadTokens,
				row.CacheWriteTokens, row.CostUsdTicks, row.CreditsMicros)
		}
		attachTaskUsage(resp, byTask)
	}
	writeJSON(w, http.StatusOK, resp)
}
