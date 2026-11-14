-- name: ListDashboardUsageBySource :many
-- Per-(source, agent, provider, model) token aggregates: what each agent spent
-- in chats, on issues and in autopilots. The source is read with the precedence
-- every other task classifier uses, chat > autopilot > issue; a run with none
-- of the three (quick create) is 'other'.
--
-- task_usage_hourly has no source dimension, so this reads task_usage itself.
-- It mirrors rollup_task_usage_hourly_window on purpose — the same joins, the
-- same hour bucket on tu.created_at, the same cost split — so the sources add
-- up to the totals the rollup-backed endpoints report for the same window. It
-- only runs ahead of them by the rollup's lag.
--
-- The bare created_at bound is implied by the bucket bound and is there for
-- idx_task_usage_created_at; @since is a raw timestamptz the Go layer already
-- computed in the viewer's tz, as for ListDashboardUsageByAgent.
SELECT
    (CASE
        WHEN atq.chat_session_id IS NOT NULL THEN 'chat'
        WHEN atq.autopilot_run_id IS NOT NULL THEN 'autopilot'
        WHEN atq.issue_id IS NOT NULL THEN 'issue'
        ELSE 'other'
    END)::text AS source,
    atq.agent_id,
    LOWER(tu.provider)::text AS provider,
    tu.model,
    SUM(tu.input_tokens)::bigint       AS input_tokens,
    SUM(tu.output_tokens)::bigint      AS output_tokens,
    SUM(tu.cache_read_tokens)::bigint  AS cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
    COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int    AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
JOIN agent            a   ON a.id   = atq.agent_id
LEFT JOIN issue       i   ON i.id   = atq.issue_id
WHERE a.workspace_id = $1
  AND tu.created_at >= @since::timestamptz
  AND task_usage_hour_bucket(tu.created_at) >= @since::timestamptz
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
GROUP BY 1, 2, 3, 4
ORDER BY 1, 2, 3, 4;
