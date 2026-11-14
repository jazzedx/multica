/** The other end of a dependency between two issues. */
export interface IssueDependencyRef {
  id: string;
  identifier: string;
  title: string;
  status: string;
  /**
   * Whether the issue is completed, which is what a waiting issue needs from
   * it. A cancelled issue is not done.
   */
  done: boolean;
}

/**
 * What an issue waits for and what waits for it. An issue that waits starts
 * no run until every `blocked_by` entry is done, then starts on its own.
 */
export interface IssueDependencies {
  blocked_by: IssueDependencyRef[];
  blocks: IssueDependencyRef[];
}

/** One edge of a parent's graph: a sub-issue and an issue it waits for. */
export interface SubIssueBlocker {
  issue_id: string;
  blocked_by: IssueDependencyRef;
}

export interface SubIssueBlockers {
  edges: SubIssueBlocker[];
}
