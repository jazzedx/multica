-- Issue dependencies: "issue_id waits for depends_on_issue_id". Rows carry the
-- one type 'blocked_by'; the table's other types are unused.

-- name: AddIssueBlocker :execrows
-- Idempotent: the table has no unique constraint, so the guard is here.
INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
SELECT sqlc.arg('issue_id')::uuid, sqlc.arg('blocker_id')::uuid, 'blocked_by'
WHERE NOT EXISTS (
    SELECT 1 FROM issue_dependency
    WHERE issue_id = sqlc.arg('issue_id')::uuid
      AND depends_on_issue_id = sqlc.arg('blocker_id')::uuid
      AND type = 'blocked_by'
);

-- name: RemoveIssueBlocker :execrows
DELETE FROM issue_dependency
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND depends_on_issue_id = sqlc.arg('blocker_id')::uuid
  AND type = 'blocked_by';

-- name: ListIssueBlockers :many
-- The issues this one waits for, oldest first.
SELECT b.id, b.workspace_id, b.number, b.title, b.status, b.project_id
FROM issue_dependency d
JOIN issue b ON b.id = d.depends_on_issue_id
WHERE d.issue_id = sqlc.arg('issue_id')::uuid
  AND d.type = 'blocked_by'
  AND b.workspace_id = sqlc.arg('workspace_id')::uuid
ORDER BY b.number;

-- name: ListIssueDependents :many
-- The issues waiting for this one, oldest first.
SELECT i.id, i.workspace_id, i.number, i.title, i.status, i.project_id
FROM issue_dependency d
JOIN issue i ON i.id = d.issue_id
WHERE d.depends_on_issue_id = sqlc.arg('blocker_id')::uuid
  AND d.type = 'blocked_by'
  AND i.workspace_id = sqlc.arg('workspace_id')::uuid
ORDER BY i.number;

-- name: IssueDependsOn :one
-- Whether `issue_id` waits for `target_id`, directly or through other issues.
-- Adding "target waits for issue" when this is true would close a cycle.
WITH RECURSIVE upstream AS (
    SELECT d.depends_on_issue_id AS id
    FROM issue_dependency d
    WHERE d.issue_id = sqlc.arg('issue_id')::uuid AND d.type = 'blocked_by'
    UNION
    SELECT d.depends_on_issue_id
    FROM issue_dependency d
    JOIN upstream u ON d.issue_id = u.id
    WHERE d.type = 'blocked_by'
)
SELECT EXISTS (SELECT 1 FROM upstream WHERE id = sqlc.arg('target_id')::uuid);

-- name: ListSubIssueBlockers :many
-- What each sub-issue of one parent waits for: the edges of the parent's
-- graph, with the issue each edge points at.
SELECT d.issue_id, b.id, b.workspace_id, b.number, b.title, b.status, b.project_id
FROM issue_dependency d
JOIN issue i ON i.id = d.issue_id
JOIN issue b ON b.id = d.depends_on_issue_id
WHERE i.parent_issue_id = sqlc.arg('parent_issue_id')::uuid
  AND i.workspace_id = sqlc.arg('workspace_id')::uuid
  AND b.workspace_id = sqlc.arg('workspace_id')::uuid
  AND d.type = 'blocked_by'
ORDER BY i.number, b.number;
