-- name: ListTasksByChatSession :many
-- Every run a chat session produced, newest first: the chat counterpart of
-- ListTasksByIssue. Served by idx_agent_task_queue_chat_session.
SELECT * FROM agent_task_queue
WHERE chat_session_id = $1
ORDER BY created_at DESC;

-- name: ListChatSessionTaskUsage :many
-- Per-(task, provider, model) usage rows for every run of one chat session,
-- the chat counterpart of ListIssueTaskUsage. The model dimension stays on the
-- wire for the same reason: cost is priced client-side per model.
SELECT
    tu.task_id,
    tu.provider,
    tu.model,
    tu.input_tokens,
    tu.output_tokens,
    tu.cache_read_tokens,
    tu.cache_write_tokens,
    tu.cost_usd_ticks,
    tu.credits_micros
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
WHERE atq.chat_session_id = $1
ORDER BY tu.task_id, tu.model;
