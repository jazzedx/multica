-- name: CountIssueStatusMoves :one
-- How many times an issue has moved from one status to another: what a
-- workflow step's cap on sending an issue back counts.
SELECT count(*)::bigint
FROM activity_log
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND action = 'status_changed'
  AND details->>'from' = sqlc.arg('from_status')::text
  AND details->>'to' = sqlc.arg('to_status')::text;
