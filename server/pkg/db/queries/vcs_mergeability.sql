-- Mergeability of mirrored Forgejo / Gitea / GitLab pull requests.

-- name: UpsertVCSPullRequestMergeability :exec
INSERT INTO vcs_pull_request_mergeability (pull_request_id, head_sha, state, checked_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (pull_request_id) DO UPDATE SET
    head_sha   = EXCLUDED.head_sha,
    state      = EXCLUDED.state,
    checked_at = now();

-- name: GetVCSPullRequestMergeability :one
SELECT * FROM vcs_pull_request_mergeability WHERE pull_request_id = $1;

-- name: ListVCSPullRequestMergeability :many
SELECT * FROM vcs_pull_request_mergeability
WHERE pull_request_id = ANY(sqlc.arg('pull_request_ids')::uuid[]);

-- name: ListOpenLinkedVCSPullRequestsInRepo :many
-- The other open pull requests of a repository that some issue is linked to.
-- A merge into the repository can make any of them conflict.
SELECT pr.* FROM vcs_pull_request AS pr
WHERE pr.connection_id = $1 AND pr.repo_owner = $2 AND pr.repo_name = $3
  AND pr.id <> $4
  AND pr.state IN ('open', 'draft')
  AND EXISTS (SELECT 1 FROM issue_vcs_pull_request AS ipr WHERE ipr.pull_request_id = pr.id)
ORDER BY pr.pr_number
LIMIT 200;

-- name: ListStaleConflictWatchedVCSPullRequests :many
-- Open pull requests an enabled "wake on merge conflict" rule watches, whose
-- verdict is missing, is for an older head, or was read before older_than. A
-- push to the base branch gives them no event, so they are read again on a
-- timer. Least recently read first, so a batch that keeps failing cannot hold
-- the rest back.
SELECT pr.* FROM vcs_pull_request AS pr
LEFT JOIN vcs_pull_request_mergeability AS m ON m.pull_request_id = pr.id
WHERE pr.state IN ('open', 'draft')
  AND (m.pull_request_id IS NULL OR m.head_sha <> pr.head_sha OR m.checked_at < sqlc.arg('older_than'))
  AND pr.id IN (
      SELECT ipr.pull_request_id
      FROM issue_wakeup AS w
      JOIN issue_vcs_pull_request AS ipr ON ipr.issue_id = w.issue_id
      WHERE w.enabled
        AND w.condition->>'type' = 'pull_request'
        AND w.condition->>'event' = 'conflicts'
  )
ORDER BY m.checked_at NULLS FIRST, pr.id
LIMIT sqlc.arg('max_rows');

-- name: DeleteOrphanVCSPullRequestMergeability :execrows
DELETE FROM vcs_pull_request_mergeability AS m
WHERE NOT EXISTS (SELECT 1 FROM vcs_pull_request AS pr WHERE pr.id = m.pull_request_id);
