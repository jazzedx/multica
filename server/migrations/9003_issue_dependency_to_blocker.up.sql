-- This fork stored "issue waits for issue" in issue_dependency (type
-- 'blocked_by') before upstream shipped blockers. Upstream's issue_blocker is
-- now the only edge the platform reads, so the fork's edges move there.
--
-- Each edge goes in on its own so the blocker graph checks on issue_blocker
-- see every one: an edge they refuse (a cycle through stages, a parent and its
-- descendant) is skipped and reported instead of failing the migration. The
-- triggers also arm the release wakeup of an issue that still waits. An issue
-- whose blocker already ended is not started here; it was held only because
-- the fork never released on cancellation, and is left as it is.
-- issue_dependency itself is left in place; nothing reads it any more.
DO $$
DECLARE
  edge record;
  moved int := 0;
  skipped int := 0;
BEGIN
  FOR edge IN
    -- In issue order, so which edge of a refused pair survives is stable.
    SELECT DISTINCT i.workspace_id, i.number, b.number AS blocker_number,
      d.issue_id, d.depends_on_issue_id AS blocker_id, i.creator_type, i.creator_id
    FROM issue_dependency d
    JOIN issue i ON i.id = d.issue_id
    JOIN issue b ON b.id = d.depends_on_issue_id AND b.workspace_id = i.workspace_id
    WHERE d.type = 'blocked_by' AND d.issue_id <> d.depends_on_issue_id
    ORDER BY i.workspace_id, i.number, b.number
  LOOP
    BEGIN
      INSERT INTO issue_blocker (workspace_id, issue_id, blocker_issue_id, created_by_type, created_by_id)
      VALUES (edge.workspace_id, edge.issue_id, edge.blocker_id, edge.creator_type, edge.creator_id)
      ON CONFLICT DO NOTHING;
      moved := moved + 1;
    EXCEPTION WHEN check_violation THEN
      skipped := skipped + 1;
      RAISE NOTICE 'issue % no longer waits for %: %', edge.issue_id, edge.blocker_id, SQLERRM;
    END;
  END LOOP;
  RAISE NOTICE 'moved % issue dependencies to issue_blocker, skipped %', moved, skipped;
END $$;
