-- Removes the blockers the up migration copied. A blocker added since with
-- the same endpoints cannot be told apart and goes too.
DELETE FROM issue_blocker b
USING issue_dependency d
WHERE d.type = 'blocked_by'
  AND b.issue_id = d.issue_id
  AND b.blocker_issue_id = d.depends_on_issue_id;
