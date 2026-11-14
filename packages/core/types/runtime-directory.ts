/** Why a path cannot be a project's local directory. */
export type RuntimeDirectoryProblem =
  | "not_absolute"
  | "not_found"
  | "not_a_directory"
  | "not_readable"
  | "not_writable"
  | "protected"
  | "error";

/** What a runtime's machine found at a path. */
export interface RuntimeDirectoryListing {
  /** The absolute path that was read; the machine's home directory when none was asked for. */
  path: string;
  separator: "/" | "\\";
  /** Names of the directories inside `path`. */
  entries: string[];
  truncated: boolean;
  is_git_repo: boolean;
  /** Empty when nothing was found wrong. A protected path still lists its entries. */
  problem: RuntimeDirectoryProblem | "";
  detail: string;
}

export type RuntimeDirectoryStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "timeout";

export interface RuntimeDirectoryRequest {
  id: string;
  runtime_id: string;
  status: RuntimeDirectoryStatus;
  path: string;
  listing: RuntimeDirectoryListing | null;
  error: string;
}
