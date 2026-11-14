package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/squadtemplate"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// What an uninstall does, or did, with one resource. A kept resource also
// carries the reason it stays.
const (
	squadTemplateArchive = "archive" // squad or agent: archived
	squadTemplateDelete  = "delete"  // skill: deleted
	squadTemplateAbsent  = "absent"  // nothing active under that name
)

// Why an uninstall keeps a resource. Only "modified" yields to the caller
// asking for modified resources to go too; the others protect something the
// uninstall has no claim on.
const (
	// The squad's or agent's instructions, the squad's membership or the
	// skill's content is no longer what the template set up. That covers both
	// an edit made after the install and a namesake the install merely reused.
	squadTemplateKeptModified = "modified"
	// The agent belongs to a squad that stays.
	squadTemplateKeptInSquad = "in_squad"
	// The agent has unfinished issues, counting those its squad hands it when
	// the squad is archived.
	squadTemplateKeptOpenIssues = "open_issues"
	// An agent that stays carries the skill.
	squadTemplateKeptInUse = "in_use"
	// No template installed the skill, so it is someone's own.
	squadTemplateKeptNotInstalled = "not_installed"
)

type SquadTemplateRemoval struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	// Reason is set when Action is "keep".
	Reason string `json:"reason,omitempty"`
	ID     string `json:"id,omitempty"`
}

// UninstallSquadTemplateResponse is the removal plan for a template in this
// workspace. A dry run returns what an uninstall would do; an uninstall
// returns what it did.
type UninstallSquadTemplateResponse struct {
	Template string                 `json:"template"`
	DryRun   bool                   `json:"dry_run"`
	Squad    SquadTemplateRemoval   `json:"squad"`
	Agents   []SquadTemplateRemoval `json:"agents"`
	Skills   []SquadTemplateRemoval `json:"skills"`
}

type uninstallSquadTemplateRequest struct {
	DryRun bool `json:"dry_run"`
	// RemoveModified also removes what the plan would keep as "modified".
	RemoveModified bool `json:"remove_modified"`
}

// UninstallSquadTemplate — POST /api/squad-templates/{key}/uninstall
//
// Removes what the template set up and nobody has a further claim on: it
// archives the squad and its agents and deletes the skills, each through the
// handler a person would use, so archiving behaves exactly as it does from the
// Squads and Agents pages. Like an install it goes by name, and a failed or
// repeated uninstall picks up where the last one stopped.
func (h *Handler) UninstallSquadTemplate(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "workspaceId")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	var req uninstallSquadTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	catalog, err := squadTemplateCatalog()
	if err != nil {
		slog.Error("squad templates: catalog failed to load", "error", err)
		writeError(w, http.StatusInternalServerError, "squad templates are unavailable")
		return
	}
	tpl, found := catalog.Template(chi.URLParam(r, "key"))
	if !found {
		writeError(w, http.StatusNotFound, "squad template not found")
		return
	}

	plan, err := h.planSquadTemplateRemoval(r.Context(), wsUUID, tpl, req.RemoveModified)
	if err != nil {
		slog.Warn("squad templates: removal plan failed", append(logger.RequestAttrs(r), "error", err, "template", tpl.Key)...)
		writeError(w, http.StatusInternalServerError, "failed to plan the template uninstall")
		return
	}
	plan.DryRun = req.DryRun
	if req.DryRun {
		writeJSON(w, http.StatusOK, plan)
		return
	}

	// The squad goes first: archiving it hands its issues to the leader, and an
	// agent must not be archived while it still leads an active squad.
	if plan.Squad.Action == squadTemplateArchive {
		if status, msg := callHandler(r, h.DeleteSquad, http.MethodDelete, nil, nil, "id", plan.Squad.ID); status != http.StatusNoContent {
			writeError(w, status, fmt.Sprintf("squad %q: %s", plan.Squad.Name, msg))
			return
		}
	}
	for _, agent := range plan.Agents {
		if agent.Action != squadTemplateArchive {
			continue
		}
		if status, msg := callHandler(r, h.ArchiveAgent, http.MethodPost, nil, nil, "id", agent.ID); status != http.StatusOK {
			writeError(w, status, fmt.Sprintf("agent %q: %s", agent.Name, msg))
			return
		}
	}
	for _, skill := range plan.Skills {
		if skill.Action != squadTemplateDelete {
			continue
		}
		if status, msg := callHandler(r, h.DeleteSkill, http.MethodDelete, nil, nil, "id", skill.ID); status != http.StatusNoContent {
			writeError(w, status, fmt.Sprintf("skill %q: %s", skill.Name, msg))
			return
		}
	}
	slog.Info("squad template uninstalled", append(logger.RequestAttrs(r), "template", tpl.Key)...)
	writeJSON(w, http.StatusOK, plan)
}

// planSquadTemplateRemoval works out what an uninstall may remove. It only
// reads.
func (h *Handler) planSquadTemplateRemoval(ctx context.Context, wsUUID pgtype.UUID, tpl squadtemplate.Template, removeModified bool) (UninstallSquadTemplateResponse, error) {
	plan := UninstallSquadTemplateResponse{
		Template: tpl.Key,
		Squad:    SquadTemplateRemoval{Name: tpl.SquadName, Action: squadTemplateAbsent},
	}
	// keepUnlessAsked settles a resource that differs from the template.
	keepUnlessAsked := func(item *SquadTemplateRemoval, removal string) {
		if removeModified {
			item.Action = removal
			return
		}
		item.Action, item.Reason = squadTemplateKeep, squadTemplateKeptModified
	}

	allAgents, err := h.Queries.ListAllAgents(ctx, wsUUID)
	if err != nil {
		return plan, err
	}
	activeAgents := map[string]db.Agent{} // by name
	agentNames := map[string]string{}     // by id
	for _, a := range allAgents {
		agentNames[uuidToString(a.ID)] = a.Name
		if !a.ArchivedAt.Valid {
			activeAgents[a.Name] = a
		}
	}
	templateAgents := map[string]squadtemplate.Agent{}
	for _, a := range tpl.Agents {
		templateAgents[a.Name] = a
	}

	// The squad, by the same rule an install finds it: the oldest active squad
	// of the template's name.
	var squad db.Squad
	squads, err := h.Queries.ListSquads(ctx, wsUUID)
	if err != nil {
		return plan, err
	}
	for _, candidate := range squads {
		if candidate.Name != tpl.SquadName {
			continue
		}
		squad = candidate
		plan.Squad.ID = uuidToString(squad.ID)
		members, err := h.Queries.ListSquadMembers(ctx, squad.ID)
		if err != nil {
			return plan, err
		}
		modified := squad.Instructions != tpl.SquadInstructions
		for _, m := range members {
			// A person, or an agent the template does not ship, was added.
			if _, ours := templateAgents[agentNames[uuidToString(m.MemberID)]]; m.MemberType != "agent" || !ours {
				modified = true
			}
		}
		if modified {
			keepUnlessAsked(&plan.Squad, squadTemplateArchive)
		} else {
			plan.Squad.Action = squadTemplateArchive
		}
		break
	}
	squadGoes := plan.Squad.Action == squadTemplateArchive

	// Unfinished issues per assignee, for the squad and every agent at once.
	var assignees []pgtype.UUID
	if squad.ID.Valid {
		assignees = append(assignees, squad.ID)
	}
	for _, a := range tpl.Agents {
		if existing, ok := activeAgents[a.Name]; ok {
			assignees = append(assignees, existing.ID)
		}
	}
	openIssues := map[string]int{}
	if len(assignees) > 0 {
		terminal, err := h.terminalIssueStatusKeys(ctx, wsUUID)
		if err != nil {
			return plan, err
		}
		issues, err := h.Queries.ListOpenIssues(ctx, db.ListOpenIssuesParams{
			WorkspaceID:        wsUUID,
			TerminalStatusKeys: terminal,
			AssigneeIds:        assignees,
		})
		if err != nil {
			return plan, err
		}
		for _, issue := range issues {
			openIssues[uuidToString(issue.AssigneeID)]++
		}
	}

	archivedAgents := map[string]bool{} // by id: agents this uninstall archives
	for _, agent := range tpl.Agents {
		item := SquadTemplateRemoval{Name: agent.Name, Action: squadTemplateAbsent}
		existing, ok := activeAgents[agent.Name]
		if !ok {
			plan.Agents = append(plan.Agents, item)
			continue
		}
		item.ID = uuidToString(existing.ID)

		memberOf, err := h.Queries.ListSquadsByMember(ctx, db.ListSquadsByMemberParams{
			WorkspaceID: wsUUID,
			MemberType:  "agent",
			MemberID:    existing.ID,
		})
		if err != nil {
			return plan, err
		}
		inSquadThatStays := false
		for _, s := range memberOf {
			if !s.ArchivedAt.Valid && !(squadGoes && s.ID == squad.ID) {
				inSquadThatStays = true
			}
		}
		unfinished := openIssues[item.ID]
		if squadGoes && squad.LeaderID == existing.ID {
			unfinished += openIssues[plan.Squad.ID]
		}

		switch {
		case inSquadThatStays:
			item.Action, item.Reason = squadTemplateKeep, squadTemplateKeptInSquad
		case unfinished > 0:
			item.Action, item.Reason = squadTemplateKeep, squadTemplateKeptOpenIssues
		case existing.Instructions != agent.Instructions:
			keepUnlessAsked(&item, squadTemplateArchive)
		default:
			item.Action = squadTemplateArchive
		}
		if item.Action == squadTemplateArchive {
			archivedAgents[item.ID] = true
		}
		plan.Agents = append(plan.Agents, item)
	}

	// A skill is in use while any agent that stays active carries it, template
	// agent or not.
	bindings, err := h.Queries.ListAgentSkillsByWorkspace(ctx, wsUUID)
	if err != nil {
		return plan, err
	}
	inUse := map[string]bool{} // by skill id
	for _, b := range bindings {
		agentID := uuidToString(b.AgentID)
		if _, active := activeAgents[agentNames[agentID]]; active && !archivedAgents[agentID] {
			inUse[uuidToString(b.ID)] = true
		}
	}
	for _, skill := range tpl.Skills {
		item := SquadTemplateRemoval{Name: skill.Name, Action: squadTemplateAbsent}
		existing, err := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: wsUUID,
			Name:        skill.Name,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			plan.Skills = append(plan.Skills, item)
			continue
		}
		if err != nil {
			return plan, err
		}
		item.ID = uuidToString(existing.ID)

		installed := installedTemplateSkillDigest(existing.Config)
		switch {
		case installed == "":
			item.Action, item.Reason = squadTemplateKeep, squadTemplateKeptNotInstalled
		case inUse[item.ID]:
			item.Action, item.Reason = squadTemplateKeep, squadTemplateKeptInUse
		default:
			current, err := h.storedSkillDigest(ctx, existing)
			if err != nil {
				return plan, err
			}
			if current != installed {
				keepUnlessAsked(&item, squadTemplateDelete)
			} else {
				item.Action = squadTemplateDelete
			}
		}
		plan.Skills = append(plan.Skills, item)
	}
	return plan, nil
}
