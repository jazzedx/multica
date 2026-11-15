-- Whether a mirrored Forgejo / Gitea / GitLab pull request merges cleanly, as
-- last read from the provider. The webhooks do not carry it reliably, and a
-- pull request starts conflicting when its base moves, with no event of its
-- own, so the server reads it through the connection's token.
--
-- head_sha is the head the verdict was read for: one for an older head says
-- nothing about a pushed one. The row is kept beside vcs_pull_request rather
-- than in it, and carries no foreign key; rows whose pull request is gone are
-- removed by the refresh sweep.
--
-- This fork numbers its own migrations from 9001, so they never share a
-- numeric prefix with a migration merged from upstream.
CREATE TABLE IF NOT EXISTS vcs_pull_request_mergeability (
    pull_request_id UUID PRIMARY KEY,
    head_sha        TEXT NOT NULL,
    state           TEXT NOT NULL
        CHECK (state IN ('mergeable', 'conflicting', 'unknown')),
    checked_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
