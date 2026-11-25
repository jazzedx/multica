-- The bundled Gitea is gone: Multica connects to code hosts deployed on their
-- own, so what the server kept for linking workspaces to that Gitea has no
-- reader. The Gitea's own data (its database and volume) is not touched.
DROP TABLE IF EXISTS builtin_code_host_account;
DROP TABLE IF EXISTS builtin_code_host_workspace;
