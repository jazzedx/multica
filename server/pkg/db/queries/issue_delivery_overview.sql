-- Delivery overview: what it took to deliver an issue and everything under it.

-- name: ListIssueSubtree :many
-- The issue and all its descendants. The depth bound keeps a malformed parent
-- chain from running away.
WITH RECURSIVE tree AS (
    SELECT i.id, i.status, i.project_id, i.created_at, 0 AS depth
    FROM issue i
    WHERE i.id = sqlc.arg('issue_id')::uuid AND i.workspace_id = sqlc.arg('workspace_id')::uuid
    UNION ALL
    SELECT c.id, c.status, c.project_id, c.created_at, t.depth + 1
    FROM issue c
    JOIN tree t ON c.parent_issue_id = t.id
    WHERE c.workspace_id = sqlc.arg('workspace_id')::uuid AND t.depth < 20
)
SELECT id, status, project_id, created_at, depth::int AS depth FROM tree;

-- name: CountRunsByIssues :one
SELECT
    count(*)::bigint AS total,
    count(*) FILTER (WHERE status = 'failed')::bigint AS failed,
    count(*) FILTER (WHERE status IN ('queued', 'dispatched', 'running'))::bigint AS active
FROM agent_task_queue
WHERE issue_id = ANY(sqlc.arg('issue_ids')::uuid[]);

-- name: ListStatusMovesByIssues :many
-- Every recorded status change of these issues, oldest first.
SELECT
    issue_id,
    COALESCE(details->>'from', '')::text AS from_status,
    COALESCE(details->>'to', '')::text AS to_status,
    created_at
FROM activity_log
WHERE issue_id = ANY(sqlc.arg('issue_ids')::uuid[])
  AND action = 'status_changed'
ORDER BY created_at;

-- name: CountMemberActionsByIssues :one
-- What people did on these issues: their recorded changes and their comments.
-- Filing the root issue is the request itself, not an intervention in it.
SELECT (
    (SELECT count(*) FROM activity_log a
     WHERE a.issue_id = ANY(sqlc.arg('issue_ids')::uuid[])
       AND a.actor_type = 'member'
       AND NOT (a.action = 'created' AND a.issue_id = sqlc.arg('root_id')::uuid))
    +
    (SELECT count(*) FROM comment c
     WHERE c.issue_id = ANY(sqlc.arg('issue_ids')::uuid[])
       AND c.author_type = 'member')
)::bigint;

-- name: CountMergedPullRequestsByIssues :one
SELECT (
    (SELECT count(DISTINCT pr.id) FROM issue_pull_request l
     JOIN github_pull_request pr ON pr.id = l.pull_request_id
     WHERE l.issue_id = ANY(sqlc.arg('issue_ids')::uuid[]) AND pr.state = 'merged')
    +
    (SELECT count(DISTINCT pr.id) FROM issue_vcs_pull_request l
     JOIN vcs_pull_request pr ON pr.id = l.pull_request_id
     WHERE l.issue_id = ANY(sqlc.arg('issue_ids')::uuid[]) AND pr.state = 'merged')
)::bigint;
