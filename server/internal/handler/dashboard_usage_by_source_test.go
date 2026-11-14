package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestGetDashboardUsageBySource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	agentID := createHandlerTestAgent(t, "usage by source agent", nil)
	// Private to the workspace owner: a plain member may not see it.
	privateAgentID := dbfx.Agent(t, "usage by source private agent", runtimeID)
	projectID := dbfx.Project(t, "usage by source project")
	issueID := dbfx.Issue(t, "usage by source issue", testutil.Cols{"project_id": projectID})
	sessionID := dbfx.ChatSession(t, agentID)
	privateSessionID := dbfx.ChatSession(t, privateAgentID)
	autopilotID := dbfx.Insert(t, "autopilot", testutil.Cols{
		"workspace_id":    testWorkspaceID,
		"title":           "usage by source autopilot",
		"assignee_id":     agentID,
		"execution_mode":  "run_only",
		"created_by_type": "member",
		"created_by_id":   testUserID,
	})
	runID := dbfx.Insert(t, "autopilot_run", testutil.Cols{
		"autopilot_id": autopilotID,
		"source":       "manual",
		"status":       "completed",
	})

	// The workspace is shared with every other test in the package, so these
	// rows carry a model of their own and the assertions read only that model.
	const model = "usage-by-source-test-model"
	run := func(agent string, over testutil.Cols, createdAt string, input int, costTicks any) {
		cols := testutil.Cols{"runtime_id": runtimeID, "status": "completed"}
		for k, v := range over {
			cols[k] = v
		}
		taskID := dbfx.Task(t, agent, cols)
		dbfx.Exec(t, `
			INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cost_usd_ticks, created_at)
			VALUES ($1, 'Anthropic', $2, $3, 0, $4, `+createdAt+`)
		`, taskID, model, input, costTicks)
	}
	recent := "now() - interval '2 hours'"
	run(agentID, testutil.Cols{"chat_session_id": sessionID}, recent, 100, nil)
	run(agentID, testutil.Cols{"chat_session_id": sessionID}, recent, 20, 7_000)
	run(agentID, testutil.Cols{"issue_id": issueID}, recent, 1_000, nil)
	// An autopilot run on an issue is the autopilot's work, not the issue's.
	run(agentID, testutil.Cols{"autopilot_run_id": runID, "issue_id": issueID}, recent, 10_000, nil)
	// Quick create: no chat, autopilot or issue behind it.
	run(agentID, nil, recent, 5, nil)
	// Outside any window this test asks for.
	run(agentID, testutil.Cols{"chat_session_id": sessionID}, "now() - interval '400 days'", 900_000, nil)
	run(privateAgentID, testutil.Cols{"chat_session_id": privateSessionID}, recent, 40, nil)
	run(privateAgentID, testutil.Cols{"issue_id": issueID}, recent, 3_000, nil)

	type row struct {
		Source              string `json:"source"`
		AgentID             string `json:"agent_id"`
		Provider            string `json:"provider"`
		Model               string `json:"model"`
		InputTokens         int64  `json:"input_tokens"`
		CostUSDTicks        int64  `json:"cost_usd_ticks"`
		UncostedInputTokens int64  `json:"uncosted_input_tokens"`
		TaskCount           int32  `json:"task_count"`
	}
	type cell struct{ source, agent string }
	usageBySource := func(userID, query string) map[cell]row {
		t.Helper()
		var rows []row
		req := newRequestAs(userID, http.MethodGet, "/api/dashboard/usage/by-source?"+query, nil)
		testutil.Call(t, testHandler.GetDashboardUsageBySource, req).Want(http.StatusOK).JSON(&rows)
		out := map[cell]row{}
		for _, r := range rows {
			if r.Model != model {
				continue
			}
			key := cell{r.Source, r.AgentID}
			if _, dup := out[key]; dup {
				t.Fatalf("%+v appears in more than one row for the same provider and model", key)
			}
			out[key] = r
		}
		return out
	}
	expect := func(what string, got map[cell]row, want map[cell]row) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: rows = %+v, want exactly %d of them", what, got, len(want))
		}
		for key, w := range want {
			w.Source, w.AgentID, w.Provider, w.Model = key.source, key.agent, "anthropic", model
			if got[key] != w {
				t.Errorf("%s: %+v = %+v, want %+v", what, key, got[key], w)
			}
		}
	}

	window := "days=7&" + dashboardFixtureTZParam
	// The workspace owner sees every agent by name.
	asOwner := usageBySource(testUserID, window)
	expect("owner", asOwner, map[cell]row{
		// Two chat runs fold into one row: the priced one contributes its
		// charge, the other its tokens to the estimated half.
		{"chat", agentID}:         {InputTokens: 120, CostUSDTicks: 7_000, UncostedInputTokens: 100, TaskCount: 2},
		{"issue", agentID}:        {InputTokens: 1_000, UncostedInputTokens: 1_000, TaskCount: 1},
		{"autopilot", agentID}:    {InputTokens: 10_000, UncostedInputTokens: 10_000, TaskCount: 1},
		{"other", agentID}:        {InputTokens: 5, UncostedInputTokens: 5, TaskCount: 1},
		{"chat", privateAgentID}:  {InputTokens: 40, UncostedInputTokens: 40, TaskCount: 1},
		{"issue", privateAgentID}: {InputTokens: 3_000, UncostedInputTokens: 3_000, TaskCount: 1},
	})

	// The rows are a split of what the rollup-backed endpoints report, so for
	// the same window they add up to it.
	dbfx.Exec(t, `SELECT rollup_task_usage_hourly_window('1970-01-01'::timestamptz, now() + interval '1 hour')`)
	var agentRows []struct {
		Model       string `json:"model"`
		InputTokens int64  `json:"input_tokens"`
	}
	testutil.Call(t, testHandler.GetDashboardUsageByAgent,
		newRequest(http.MethodGet, "/api/dashboard/usage/by-agent?"+window, nil),
	).Want(http.StatusOK).JSON(&agentRows)
	var rollupTotal, sourceTotal int64
	for _, r := range agentRows {
		if r.Model == model {
			rollupTotal += r.InputTokens
		}
	}
	for _, r := range asOwner {
		sourceTotal += r.InputTokens
	}
	if sourceTotal != rollupTotal || sourceTotal != 14_165 {
		t.Errorf("sources add up to %d input tokens, the per-agent rollup to %d; want both 14165", sourceTotal, rollupTotal)
	}

	// A plain member is not told the private agent exists: its rows arrive on
	// one unnamed bucket per source, so every source still adds up.
	memberID := createPlainMember(t, "usage-by-source-member@multica.test")
	asMember := usageBySource(memberID, window)
	if _, named := asMember[cell{"chat", privateAgentID}]; named {
		t.Fatalf("a plain member was shown the private agent by id: %+v", asMember)
	}
	if got := asMember[cell{"chat", restrictedAgentsRowID}]; got.InputTokens != 40 {
		t.Errorf("restricted chat bucket = %+v, want the private agent's 40 tokens", got)
	}
	if got := asMember[cell{"issue", restrictedAgentsRowID}]; got.InputTokens != 3_000 {
		t.Errorf("restricted issue bucket = %+v, want the private agent's 3000 tokens", got)
	}
	if got := asMember[cell{"chat", agentID}]; got.InputTokens != 120 {
		t.Errorf("a visible agent's chat row = %+v, want it untouched at 120 tokens", got)
	}

	// Visibility is judged when the dashboard is read, not when the run
	// happened: once the shared agent is made private, the member sees its past
	// usage in the same unnamed bucket, and the source still adds up.
	dbfx.Exec(t, `UPDATE agent SET visibility = 'private', permission_mode = 'private' WHERE id = $1`, agentID)
	dbfx.Exec(t, `DELETE FROM agent_invocation_target WHERE agent_id = $1`, agentID)
	afterRevoke := usageBySource(memberID, window)
	if _, named := afterRevoke[cell{"chat", agentID}]; named {
		t.Fatalf("the member still sees the agent by id after losing access: %+v", afterRevoke)
	}
	if got := afterRevoke[cell{"chat", restrictedAgentsRowID}]; got.InputTokens != 160 || got.TaskCount != 3 {
		t.Errorf("restricted chat bucket after the revoke = %+v, want both agents' 160 tokens over 3 runs", got)
	}
	// The owner's view is unchanged by it.
	if got := usageBySource(testUserID, window)[cell{"chat", agentID}]; got.InputTokens != 120 {
		t.Errorf("owner's view of the agent after the revoke = %+v, want it named with 120 tokens", got)
	}

	// A project owns issues, so its filter leaves the issue-bound runs.
	expect("project", usageBySource(testUserID, window+"&project_id="+projectID), map[cell]row{
		{"issue", agentID}:        {InputTokens: 1_000, UncostedInputTokens: 1_000, TaskCount: 1},
		{"autopilot", agentID}:    {InputTokens: 10_000, UncostedInputTokens: 10_000, TaskCount: 1},
		{"issue", privateAgentID}: {InputTokens: 3_000, UncostedInputTokens: 3_000, TaskCount: 1},
	})
}
