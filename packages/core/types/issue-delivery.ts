/**
 * What it took to deliver an issue and everything under it. Token use and run
 * time are not here: the execution log already sums them for a sub-tree.
 */
export interface IssueDeliveryOverview {
  sub_issues: { total: number; done: number; cancelled: number };
  runs: { total: number; failed: number; active: number };
  /** Status changes that sent an issue back to an earlier place on its board. */
  rework_rounds: number;
  /** What members did on these issues after the root was filed: changes and comments. */
  human_actions: number;
  pull_requests_merged: number;
  started_at: string;
  /** When the root last became done, while it still is. */
  finished_at: string | null;
}
