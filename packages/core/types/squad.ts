export type SquadMemberType = "agent" | "member";

export type SquadActivityOutcome = "action" | "no_action" | "failed";

/**
 * How work moves between squad members. "leader" (the default) routes every
 * member reply back through the leader; "direct" lets a member hand the issue
 * to the next member, with fixed guardrails sending it back to the leader.
 */
export type SquadHandoffMode = "leader" | "direct";

/** Fixed guardrails of the direct mode; mirrors the server constants. */
export const SQUAD_HANDOFF_ROUND_LIMIT = 3;
export const SQUAD_HANDOFF_DAILY_LIMIT = 20;

export interface SquadMemberPreview {
  member_type: SquadMemberType;
  member_id: string;
  role: string;
}

export interface Squad {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  instructions: string;
  avatar_url: string | null;
  leader_id: string;
  creator_id: string;
  created_at: string;
  updated_at: string;
  archived_at: string | null;
  archived_by: string | null;
  member_count?: number;
  member_preview?: SquadMemberPreview[];
  handoff_mode?: SquadHandoffMode;
}

export interface SquadMember {
  id: string;
  squad_id: string;
  member_type: SquadMemberType;
  member_id: string;
  role: string;
  created_at: string;
}

export interface SquadActivityLog {
  id: string;
  squad_id: string;
  issue_id: string;
  trigger_comment_id: string | null;
  leader_id: string;
  outcome: SquadActivityOutcome;
  details: unknown;
  created_at: string;
}

export interface CreateSquadRequest {
  name: string;
  description?: string;
  leader_id: string;
  avatar_url?: string;
}

export interface UpdateSquadRequest {
  name?: string;
  description?: string;
  instructions?: string;
  leader_id?: string;
  avatar_url?: string;
  handoff_mode?: SquadHandoffMode;
}

export interface AddSquadMemberRequest {
  member_type: SquadMemberType;
  member_id: string;
  role?: string;
}

export interface RemoveSquadMemberRequest {
  member_type: SquadMemberType;
  member_id: string;
}

export interface UpdateSquadMemberRoleRequest {
  member_type: SquadMemberType;
  member_id: string;
  role: string;
}

export interface CreateSquadActivityLogRequest {
  squad_id: string;
  issue_id: string;
  trigger_comment_id?: string;
  outcome: SquadActivityOutcome;
  details?: unknown;
}

// SquadMemberStatus mirrors the five-way bucket the back-end derives in
// handler/squad.go::deriveSquadMemberStatus. Kept as a string union here
// (rather than re-derived from snapshot data) so the squad page can render
// the freshest server-side judgement without re-fetching the agent
// snapshot / runtime list. `archived` wins over every runtime/task signal.
export type SquadMemberStatusValue =
  | "working"
  | "idle"
  | "offline"
  | "unstable"
  | "archived";

export interface SquadActiveIssueBrief {
  issue_id: string;
  identifier: string;
  title: string;
  issue_status: string;
}

export interface SquadMemberStatus {
  member_type: SquadMemberType;
  member_id: string;
  // Human members are returned with status === null so the UI can render
  // them in the same list without showing a status pill (v1 has no
  // presence signal for humans).
  status: SquadMemberStatusValue | null;
  active_issues: SquadActiveIssueBrief[];
  last_active_at: string | null;
}

export interface SquadMemberStatusListResponse {
  members: SquadMemberStatus[];
}

/** The squad context one agent receives on an issue assigned to the squad. */
export interface SquadBrief {
  member_type: SquadMemberType;
  member_id: string;
  is_leader: boolean;
  brief: string;
}

export interface SquadBriefsResponse {
  handoff_mode: SquadHandoffMode;
  briefs: SquadBrief[];
}

/** Copy for the template picker, in the two languages the catalog carries. */
export interface SquadTemplateText {
  en: string;
  zh: string;
}

export interface SquadTemplateAgent {
  name: string;
  role: string;
  skills: string[];
}

/** A ready-made squad the server ships: its agents and the skills they carry. */
export interface SquadTemplate {
  key: string;
  recommended: boolean;
  title: SquadTemplateText;
  summary: SquadTemplateText;
  squad_name: string;
  squad_description: string;
  leader: string;
  agents: SquadTemplateAgent[];
  skills: string[];
}

/**
 * What an install does with one resource: `create`, `update`, `unchanged`,
 * `keep` (differs from the template and is left alone), `exists` (reused) or
 * `restore` (an archived agent brought back and reused). An uninstall uses
 * `archive`, `delete`, `keep` and `absent`. A string because a newer server
 * may add one.
 */
export type SquadTemplateAction = string;

export interface SquadTemplateSkillPlan {
  name: string;
  action: SquadTemplateAction;
  id?: string;
}

export interface SquadTemplateAgentPlan {
  name: string;
  role: string;
  action: SquadTemplateAction;
  id?: string;
  /** Template skills an existing agent does not carry yet. */
  add_skills: number;
}

export interface SquadTemplateSquadPlan {
  name: string;
  action: SquadTemplateAction;
  id?: string;
  /** Template agents an existing squad does not have yet. */
  add_members: number;
}

/** A dry run says what an install would do; an install says what it did. */
export interface SquadTemplatePlan {
  template: string;
  dry_run: boolean;
  skills: SquadTemplateSkillPlan[];
  agents: SquadTemplateAgentPlan[];
  squad: SquadTemplateSquadPlan;
}

export interface SquadTemplateRemoval {
  name: string;
  action: SquadTemplateAction;
  /**
   * Why a kept resource stays: `modified`, `in_squad`, `open_issues`, `in_use`
   * or `not_installed`. Only `modified` yields to `remove_modified`.
   */
  reason?: string;
  id?: string;
}

/** A dry run says what an uninstall would do; an uninstall says what it did. */
export interface SquadTemplateRemovalPlan {
  template: string;
  dry_run: boolean;
  squad: SquadTemplateRemoval;
  agents: SquadTemplateRemoval[];
  skills: SquadTemplateRemoval[];
}
