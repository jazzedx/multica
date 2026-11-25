package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The fork's issue_dependency edges move to issue_blocker. Edges the blocker
// graph refuses are skipped rather than failing the migration, and an issue
// that still waits gets the wakeup that releases it.
func TestIssueDependencyToBlockerMigration(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to Postgres: %v", err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	ws := uuid.NewString()
	creator := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO workspace (id, name, slug) VALUES ($1, 'dependency move', $2)`, ws, "dep-move-"+ws[:8]); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	number := 0
	issue := func(title, status string, parent *string) string {
		t.Helper()
		id := uuid.NewString()
		number++
		if _, err := tx.Exec(ctx, `
			INSERT INTO issue (id, workspace_id, number, title, status, creator_type, creator_id, parent_issue_id)
			VALUES ($1, $2, $3, $4, $5, 'member', $6, $7)`, id, ws, number, title, status, creator, parent); err != nil {
			t.Fatalf("insert issue %s: %v", title, err)
		}
		return id
	}
	waits := func(issueID, blockerID string) {
		t.Helper()
		if _, err := tx.Exec(ctx, `INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type) VALUES ($1, $2, 'blocked_by')`, issueID, blockerID); err != nil {
			t.Fatalf("insert dependency: %v", err)
		}
	}
	design := issue("Design", "in_progress", nil)
	build := issue("Build", "todo", nil)
	dropped := issue("Dropped", "cancelled", nil)
	stranded := issue("Stranded", "todo", nil)
	first := issue("First", "todo", nil)
	second := issue("Second", "todo", nil)
	parent := issue("Parent", "todo", nil)
	child := issue("Child", "todo", &parent)
	waits(build, design)
	waits(build, design) // the fork's table had no unique constraint
	waits(stranded, dropped)
	waits(second, first) // closes a cycle with the next edge, which goes first: refused
	waits(first, second) // moved first: edges go in issue order
	waits(child, parent) // a child waiting for its parent: refused

	applyMigrationFile(t, ctx, tx, "9003_issue_dependency_to_blocker.up.sql")

	edges := map[[2]string]bool{}
	rows, err := tx.Query(ctx, `SELECT issue_id::text, blocker_issue_id::text FROM issue_blocker WHERE workspace_id = $1`, ws)
	if err != nil {
		t.Fatalf("list blockers: %v", err)
	}
	for rows.Next() {
		var e [2]string
		if err := rows.Scan(&e[0], &e[1]); err != nil {
			t.Fatalf("scan blocker: %v", err)
		}
		edges[e] = true
	}
	rows.Close()
	want := [][2]string{{build, design}, {stranded, dropped}, {first, second}}
	if len(edges) != len(want) {
		t.Fatalf("moved %d edges, want %d: %v", len(edges), len(want), edges)
	}
	for _, e := range want {
		if !edges[e] {
			t.Fatalf("edge %v was not moved; got %v", e, edges)
		}
	}

	var armed int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM issue_wakeup WHERE issue_id = $1 AND system_rule = 'blockers_done'`, build).Scan(&armed); err != nil {
		t.Fatalf("read wakeup: %v", err)
	}
	if armed != 1 {
		t.Fatalf("an issue that still waits has %d release wakeups, want 1", armed)
	}

	applyMigrationFile(t, ctx, tx, "9003_issue_dependency_to_blocker.down.sql")
	var left int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM issue_blocker WHERE workspace_id = $1`, ws).Scan(&left); err != nil {
		t.Fatalf("count blockers: %v", err)
	}
	if left != 0 {
		t.Fatalf("down left %d moved blockers", left)
	}
}
