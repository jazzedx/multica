package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func listChatSessionTaskRunsAs(t *testing.T, userID, sessionID string) *testutil.Response {
	t.Helper()
	req := withURLParam(newRequestAs(userID, http.MethodGet, "/api/chat/sessions/"+sessionID+"/task-runs", nil), "sessionId", sessionID)
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{}))
	return testutil.Call(t, testHandler.ListChatSessionTaskRuns, req)
}

func TestListChatSessionTaskRuns(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerID := dbfx.User(t, "Chat runs owner", "chat-task-runs-owner@example.test")
	dbfx.Member(t, testWorkspaceID, ownerID, "member")
	runtimeID := handlerTestRuntimeID(t)
	agentID := dbfx.Agent(t, "chat task runs agent", runtimeID, testutil.Cols{
		"owner_id":        ownerID,
		"visibility":      "workspace",
		"permission_mode": "public_to",
	})
	chat := func() string {
		return dbfx.ChatSession(t, agentID, testutil.Cols{
			"creator_id":            ownerID,
			"explicitly_created_at": testutil.Raw("now()"),
		})
	}
	run := func(sessionID, createdAt string, over testutil.Cols) string {
		cols := testutil.Cols{
			"runtime_id":      runtimeID,
			"chat_session_id": sessionID,
			"status":          "completed",
			"created_at":      testutil.Raw(createdAt),
			"started_at":      testutil.Raw(createdAt),
			"completed_at":    testutil.Raw("now()"),
		}
		for k, v := range over {
			cols[k] = v
		}
		return dbfx.Task(t, agentID, cols)
	}
	usage := func(taskID, model string, input, output int) {
		dbfx.Exec(t, `INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens) VALUES ($1, 'anthropic', $2, $3, $4)`,
			taskID, model, input, output)
	}

	sessionID := chat()
	older := run(sessionID, "now() - interval '2 hours'", nil)
	usage(older, "claude-opus-5", 1000, 200)
	// A run that spilled over to a second model keeps both rows: each is priced
	// at its own rate.
	usage(older, "claude-sonnet-5", 300, 40)
	newer := run(sessionID, "now() - interval '1 hour'", testutil.Cols{"status": "failed", "error": "ran out of turns"})
	unmetered := run(sessionID, "now() - interval '3 hours'", nil)

	// Another chat with the same agent, and an issue run of the same agent,
	// are not this session's history.
	otherSession := chat()
	usage(run(otherSession, "now()", nil), "claude-opus-5", 9000, 9000)
	issueID := dbfx.Issue(t, "chat task runs issue")
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": "completed"})

	var runs []AgentTaskResponse
	listChatSessionTaskRunsAs(t, ownerID, sessionID).Want(http.StatusOK).JSON(&runs)

	if len(runs) != 3 || runs[0].ID != newer || runs[1].ID != older || runs[2].ID != unmetered {
		ids := make([]string, len(runs))
		for i, r := range runs {
			ids[i] = r.ID
		}
		t.Fatalf("runs = %v, want this session's three, newest first: %s, %s, %s", ids, newer, older, unmetered)
	}
	if runs[0].Status != "failed" || runs[0].ChatSessionID != sessionID {
		t.Errorf("newest run = status %q in session %q, want the failed run of %s", runs[0].Status, runs[0].ChatSessionID, sessionID)
	}
	if got := runs[1].Usage; len(got) != 2 ||
		got[0].Model != "claude-opus-5" || got[0].InputTokens != 1000 || got[0].OutputTokens != 200 ||
		got[1].Model != "claude-sonnet-5" || got[1].InputTokens != 300 {
		t.Errorf("usage of the metered run = %+v, want its two model rows", got)
	}
	if len(runs[0].Usage) != 0 || len(runs[2].Usage) != 0 {
		t.Errorf("runs that reported nothing carry usage: %+v, %+v", runs[0].Usage, runs[2].Usage)
	}

	// The chat is its creator's; the workspace owner is a bystander here, so an
	// admin role is not what grants access.
	listChatSessionTaskRunsAs(t, testUserID, sessionID).Want(http.StatusForbidden)
}

// A chat's creator reads its runs only while they may still see its agent.
func TestListChatSessionTaskRuns_CreatorWhoLostTheAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	memberID := createPlainMember(t, "chat-task-runs-revoked@multica.test")
	runtimeID := handlerTestRuntimeID(t)
	// Shared with the workspace, owned by the workspace owner.
	agentID := createHandlerTestAgent(t, "chat task runs shared agent", nil)
	sessionID := dbfx.ChatSession(t, agentID, testutil.Cols{
		"creator_id":            memberID,
		"explicitly_created_at": testutil.Raw("now()"),
	})
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "chat_session_id": sessionID, "status": "completed"})

	var runs []AgentTaskResponse
	listChatSessionTaskRunsAs(t, memberID, sessionID).Want(http.StatusOK).JSON(&runs)
	if len(runs) != 1 {
		t.Fatalf("creator sees %d runs of their chat, want 1", len(runs))
	}

	dbfx.Exec(t, `UPDATE agent SET visibility = 'private', permission_mode = 'private' WHERE id = $1`, agentID)
	dbfx.Exec(t, `DELETE FROM agent_invocation_target WHERE agent_id = $1`, agentID)
	listChatSessionTaskRunsAs(t, memberID, sessionID).Want(http.StatusForbidden)
	// The agent's owner is an admin here and still may not read someone else's chat.
	listChatSessionTaskRunsAs(t, testUserID, sessionID).Want(http.StatusForbidden)
}
