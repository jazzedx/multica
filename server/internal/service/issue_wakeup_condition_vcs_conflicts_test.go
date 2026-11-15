package service

import (
	"context"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// A "wake on merge conflict" rule follows the verdict the server reads from a
// Forgejo / Gitea / GitLab instance the way it follows a GitHub snapshot.
func TestWakeupConditionPRConflictsCoversVCSProviders(t *testing.T) {
	for _, provider := range []string{"forgejo", "gitea", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			f, s, issue, agent := conditionFixture(t)
			if s.Tasks.PRSnapshotsEnabled {
				t.Fatal("fixture must start without the GitHub snapshot client")
			}
			pr := linkVCSPR(t, f, issue, newConnection(t, f), provider, 7, "a")
			f.Cleanup(t, "DELETE FROM vcs_pull_request_mergeability WHERE pull_request_id=$1", pr)
			w := wakeCreate(t, f, s, issue, WakeupInput{AgentID: agent, Kind: "event", Mode: "continuous", Instruction: "Resolve the conflict",
				Condition: condition(t, map[string]any{"type": "pull_request", "event": "conflicts"})})
			finishRuns := func() {
				f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE context->>'wakeup_id'=$1 AND status<>'completed'", util.UUIDToString(w.ID))
			}
			// verdict records what was read for verdictHead while the pull
			// request's head is `head`.
			verdict := func(head, verdictHead, state string) db.IssueWakeup {
				t.Helper()
				f.Exec(t, "UPDATE vcs_pull_request SET head_sha=$2 WHERE id=$1", pr, head)
				f.Exec(t, `INSERT INTO vcs_pull_request_mergeability(pull_request_id,head_sha,state) VALUES($1,$2,$3)
					ON CONFLICT (pull_request_id) DO UPDATE SET head_sha=EXCLUDED.head_sha,state=EXCLUDED.state`, pr, verdictHead, state)
				return wakeTick(t, f, s, w.ID)
			}
			expectRuns := func(want int, step string) {
				t.Helper()
				if n := wakeRuns(t, f, w.ID); n != want {
					t.Fatalf("%s: %d runs, want %d", step, n, want)
				}
			}

			// Nothing read yet: the rule waits and says why.
			got := wakeTick(t, f, s, w.ID)
			expectRuns(0, "no verdict")
			if !got.LastError.Valid || got.LastError.String != conditionDiagnosticPrefix+errPRSnapshotPending.Error() {
				t.Fatalf("diagnostic while no verdict has been read = %+v", got.LastError)
			}

			got = verdict("a", "a", "mergeable")
			expectRuns(0, "mergeable")
			if got.LastError.Valid {
				t.Fatalf("a mergeable pull request left a diagnostic: %+v", got.LastError)
			}
			got = verdict("a", "a", "conflicting")
			expectRuns(1, "conflict")
			task, err := f.q.GetAgentTask(context.Background(), got.LastTaskID)
			if err != nil {
				t.Fatal(err)
			}
			for _, fact := range []string{`"mergeable":"conflicting"`, `"head_sha":"a"`, `"provider":"` + provider + `"`} {
				if !strings.Contains(task.HandoffNote.String, fact) {
					t.Fatalf("conflict fact %s missing from the run: %s", fact, task.HandoffNote.String)
				}
			}
			finishRuns()
			// The provider checking again after another base move is not a new conflict.
			verdict("a", "a", "unknown")
			verdict("a", "a", "conflicting")
			expectRuns(1, "recheck")
			// A pushed fix: the verdict for the old head no longer counts.
			got = verdict("b", "a", "conflicting")
			expectRuns(1, "pushed head not read yet")
			if !got.LastError.Valid {
				t.Fatal("a pushed head with no verdict left no diagnostic")
			}
			verdict("b", "b", "mergeable")
			expectRuns(1, "resolved")
			verdict("b", "b", "conflicting")
			expectRuns(2, "conflict again")
			finishRuns()
			f.Exec(t, "UPDATE vcs_pull_request SET state='merged' WHERE id=$1", pr)
			wakeTick(t, f, s, w.ID)
			expectRuns(2, "merged pull request")
		})
	}
}
