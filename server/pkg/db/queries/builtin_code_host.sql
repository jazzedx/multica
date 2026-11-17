-- =====================
-- Bundled code host (a Gitea deployed beside the server)
-- =====================

-- name: GetBuiltinCodeHostWorkspace :one
SELECT * FROM builtin_code_host_workspace
WHERE workspace_id = $1;

-- name: UpsertBuiltinCodeHostWorkspace :one
INSERT INTO builtin_code_host_workspace (workspace_id, org_name)
VALUES ($1, $2)
ON CONFLICT (workspace_id) DO UPDATE SET
    org_name   = EXCLUDED.org_name,
    updated_at = now()
RETURNING *;

-- name: AddBuiltinCodeHostSyncedRepos :exec
UPDATE builtin_code_host_workspace
SET synced_repos = synced_repos || sqlc.arg('repos')::text[],
    updated_at   = now()
WHERE workspace_id = sqlc.arg('workspace_id');

-- name: SetBuiltinCodeHostReader :exec
UPDATE builtin_code_host_workspace
SET reader_username        = sqlc.arg('username'),
    reader_token_encrypted = sqlc.arg('token_encrypted'),
    updated_at             = now()
WHERE workspace_id = sqlc.arg('workspace_id');

-- name: GetBuiltinCodeHostAccount :one
SELECT * FROM builtin_code_host_account
WHERE agent_id = $1;

-- name: UpsertBuiltinCodeHostAccount :one
INSERT INTO builtin_code_host_account (agent_id, workspace_id, username, token_encrypted)
VALUES ($1, $2, $3, $4)
ON CONFLICT (agent_id) DO UPDATE SET
    username        = EXCLUDED.username,
    token_encrypted = EXCLUDED.token_encrypted,
    in_team         = TRUE,
    updated_at      = now()
RETURNING *;

-- name: SetBuiltinCodeHostAccountInTeam :exec
UPDATE builtin_code_host_account
SET in_team = $2, updated_at = now()
WHERE agent_id = $1;

-- name: ListAgentsForBuiltinCodeHost :many
-- Every agent a member created, with the account it already has, if any.
-- Archived agents are included so their accounts can lose write access.
SELECT a.id, a.name, (a.archived_at IS NOT NULL)::boolean AS archived,
       COALESCE(acct.username, '')::text AS username,
       COALESCE(acct.token_encrypted <> '', FALSE)::boolean AS has_token,
       COALESCE(acct.in_team, FALSE)::boolean AS in_team
FROM agent a
LEFT JOIN builtin_code_host_account acct ON acct.agent_id = a.id
WHERE a.workspace_id = $1 AND a.kind = 'user'
ORDER BY a.created_at ASC;

-- name: DeleteOrphanBuiltinCodeHostAccounts :execrows
DELETE FROM builtin_code_host_account AS acct
WHERE NOT EXISTS (SELECT 1 FROM agent a WHERE a.id = acct.agent_id);

-- name: ListWorkspacesForBuiltinCodeHost :many
SELECT id, name, slug FROM workspace
ORDER BY created_at ASC;

-- name: GetVCSConnectionByWorkspaceAndInstance :one
SELECT * FROM vcs_connection
WHERE workspace_id = $1 AND instance_url = $2;

-- name: AppendWorkspaceRepos :one
-- Appends in one statement, so a concurrent edit of the list is not lost.
UPDATE workspace
SET repos      = repos || sqlc.arg('repos')::jsonb,
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING *;
