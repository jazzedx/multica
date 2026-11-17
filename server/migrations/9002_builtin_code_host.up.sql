-- What the bundled code host (a Gitea deployed beside the server) holds for a
-- workspace: the organization created for it; the repositories of that
-- organization already added to the workspace's list once, so that removing
-- one from the list is not undone by the next sync; and the read-only account
-- daemons clone and fetch with.
CREATE TABLE IF NOT EXISTS builtin_code_host_workspace (
    workspace_id           UUID PRIMARY KEY,
    org_name               TEXT NOT NULL,
    synced_repos           TEXT[] NOT NULL DEFAULT '{}',
    reader_username        TEXT NOT NULL DEFAULT '',
    reader_token_encrypted TEXT NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The account an agent pushes and opens pull requests with on the bundled
-- code host. in_team records whether it currently belongs to the
-- organization's developers team: an archived agent's account is taken out
-- and put back if the agent is restored.
CREATE TABLE IF NOT EXISTS builtin_code_host_account (
    agent_id        UUID PRIMARY KEY,
    workspace_id    UUID NOT NULL,
    username        TEXT NOT NULL,
    token_encrypted TEXT NOT NULL,
    in_team         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
