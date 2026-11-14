package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/squadtemplate"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// squadTemplateCatalog returns the squad templates this server offers. Tests
// replace it with a small catalog.
var squadTemplateCatalog = squadtemplate.FromEnv

// skillOriginSquadTemplate marks a skill a template installed. Its config.origin
// also carries the digest the template had then, which is how a later install
// tells an untouched skill from one someone edited.
const skillOriginSquadTemplate = "squad_template"

// What an install does, or did, with one resource.
const (
	squadTemplateCreate    = "create"    // missing: created
	squadTemplateUpdate    = "update"    // older or overwritten copy: replaced
	squadTemplateUnchanged = "unchanged" // already what the template ships
	squadTemplateKeep      = "keep"      // differs from the template and was not installed by it: left alone
	squadTemplateExists    = "exists"    // already there: reused, with anything missing attached
	squadTemplateRestore   = "restore"   // archived agent of that name: brought back and reused like an existing one
)

type SquadTemplateAgentResponse struct {
	Name   string   `json:"name"`
	Role   string   `json:"role"`
	Skills []string `json:"skills"`
}

type SquadTemplateResponse struct {
	Key              string                       `json:"key"`
	Recommended      bool                         `json:"recommended"`
	Title            squadtemplate.LocalizedText  `json:"title"`
	Summary          squadtemplate.LocalizedText  `json:"summary"`
	SquadName        string                       `json:"squad_name"`
	SquadDescription string                       `json:"squad_description"`
	Leader           string                       `json:"leader"`
	Agents           []SquadTemplateAgentResponse `json:"agents"`
	Skills           []string                     `json:"skills"`
}

type SquadTemplateSkillPlan struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	ID     string `json:"id,omitempty"`
}

type SquadTemplateAgentPlan struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Action string `json:"action"`
	ID     string `json:"id,omitempty"`
	// AddSkills counts the template's skills an existing agent does not carry yet.
	AddSkills int `json:"add_skills"`
}

type SquadTemplateSquadPlan struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	ID     string `json:"id,omitempty"`
	// AddMembers counts the template's agents an existing squad does not have yet.
	AddMembers int `json:"add_members"`
}

// InstallSquadTemplateResponse is the plan for a template in this workspace.
// A dry run returns what an install would do; an install returns what it did.
type InstallSquadTemplateResponse struct {
	Template string                   `json:"template"`
	DryRun   bool                     `json:"dry_run"`
	Skills   []SquadTemplateSkillPlan `json:"skills"`
	Agents   []SquadTemplateAgentPlan `json:"agents"`
	Squad    SquadTemplateSquadPlan   `json:"squad"`
}

type installSquadTemplateRequest struct {
	// RuntimeID is the runtime new agents run on. Required once the plan
	// creates at least one agent.
	RuntimeID string `json:"runtime_id"`
	DryRun    bool   `json:"dry_run"`
	// OverwriteModifiedSkills replaces skills the plan would otherwise keep.
	OverwriteModifiedSkills bool `json:"overwrite_modified_skills"`
}

func squadTemplateToResponse(t squadtemplate.Template) SquadTemplateResponse {
	resp := SquadTemplateResponse{
		Key:              t.Key,
		Recommended:      t.Recommended,
		Title:            t.Title,
		Summary:          t.Summary,
		SquadName:        t.SquadName,
		SquadDescription: t.SquadDescription,
		Leader:           t.Leader,
		Agents:           make([]SquadTemplateAgentResponse, 0, len(t.Agents)),
		Skills:           make([]string, 0, len(t.Skills)),
	}
	for _, a := range t.Agents {
		resp.Agents = append(resp.Agents, SquadTemplateAgentResponse{Name: a.Name, Role: a.Role, Skills: a.Skills})
	}
	for _, s := range t.Skills {
		resp.Skills = append(resp.Skills, s.Name)
	}
	return resp
}

// ListSquadTemplates — GET /api/squad-templates
func (h *Handler) ListSquadTemplates(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "workspaceId")
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}
	catalog, err := squadTemplateCatalog()
	if err != nil {
		slog.Error("squad templates: catalog failed to load", "error", err)
		writeError(w, http.StatusInternalServerError, "squad templates are unavailable")
		return
	}
	resp := make([]SquadTemplateResponse, 0, len(catalog.Templates))
	for _, t := range catalog.Templates {
		resp = append(resp, squadTemplateToResponse(t))
	}
	writeJSON(w, http.StatusOK, resp)
}

// squadTemplateInstall is one template resolved against one workspace: the plan
// shown to the caller plus the rows the apply step needs.
type squadTemplateInstall struct {
	template squadtemplate.Template
	wsUUID   pgtype.UUID
	plan     InstallSquadTemplateResponse

	// existingSkills holds the stored row of every template skill the workspace
	// already has, by name.
	existingSkills map[string]db.Skill
	// agentSkillNames holds, per existing agent name, the skills it carries.
	agentSkillNames map[string]map[string]bool
	// squadAgentIDs holds the agent members of the existing squad.
	squadAgentIDs map[string]bool
}

// InstallSquadTemplate — POST /api/squad-templates/{key}/install
//
// Installs by name: a skill, agent or squad the workspace already has under
// the template's name is reused, never duplicated, so a failed or repeated
// install picks up where the last one stopped. Existing agents and squads keep
// their instructions; they only gain the skills and members they lack. An
// archived agent of that name is restored and treated the same way.
func (h *Handler) InstallSquadTemplate(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "workspaceId")
	// One install adds a dozen workspace-visible agents, so it is an
	// administrative act rather than something any member does.
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	var req installSquadTemplateRequest
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

	install, err := h.planSquadTemplate(r.Context(), wsUUID, tpl, req.OverwriteModifiedSkills)
	if err != nil {
		slog.Warn("squad templates: plan failed", append(logger.RequestAttrs(r), "error", err, "template", tpl.Key)...)
		writeError(w, http.StatusInternalServerError, "failed to plan the template install")
		return
	}
	install.plan.DryRun = req.DryRun
	if req.DryRun {
		writeJSON(w, http.StatusOK, install.plan)
		return
	}

	if install.createsAgents() && req.RuntimeID == "" {
		writeError(w, http.StatusBadRequest, "runtime_id is required")
		return
	}

	if status, msg := h.applySquadTemplate(r, member, install, req.RuntimeID); status != 0 {
		writeError(w, status, msg)
		return
	}
	slog.Info("squad template installed", append(logger.RequestAttrs(r), "template", tpl.Key, "squad_id", install.plan.Squad.ID)...)
	writeJSON(w, http.StatusOK, install.plan)
}

func (in *squadTemplateInstall) createsAgents() bool {
	for _, a := range in.plan.Agents {
		if a.Action == squadTemplateCreate {
			return true
		}
	}
	return false
}

// planSquadTemplate compares the template with the workspace. It only reads.
func (h *Handler) planSquadTemplate(ctx context.Context, wsUUID pgtype.UUID, tpl squadtemplate.Template, overwriteModified bool) (*squadTemplateInstall, error) {
	in := &squadTemplateInstall{
		template:        tpl,
		wsUUID:          wsUUID,
		plan:            InstallSquadTemplateResponse{Template: tpl.Key},
		existingSkills:  map[string]db.Skill{},
		agentSkillNames: map[string]map[string]bool{},
		squadAgentIDs:   map[string]bool{},
	}

	for _, skill := range tpl.Skills {
		item := SquadTemplateSkillPlan{Name: skill.Name, Action: squadTemplateCreate}
		existing, err := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: wsUUID,
			Name:        skill.Name,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, err
		default:
			action, err := h.planExistingTemplateSkill(ctx, existing, skill, overwriteModified)
			if err != nil {
				return nil, err
			}
			item.Action = action
			item.ID = uuidToString(existing.ID)
			in.existingSkills[skill.Name] = existing
		}
		in.plan.Skills = append(in.plan.Skills, item)
	}

	// Agent names are unique per workspace across archived rows too, so the
	// only way to have the template's agent while an archived one holds its
	// name is to bring that one back.
	agents, err := h.Queries.ListAllAgents(ctx, wsUUID)
	if err != nil {
		return nil, err
	}
	agentsByName := make(map[string]db.Agent, len(agents))
	for _, a := range agents {
		agentsByName[a.Name] = a
	}
	existingAgentIDs := map[string]string{}
	for _, agent := range tpl.Agents {
		item := SquadTemplateAgentPlan{Name: agent.Name, Role: agent.Role, Action: squadTemplateCreate}
		if existing, ok := agentsByName[agent.Name]; ok {
			item.ID = uuidToString(existing.ID)
			item.Action = squadTemplateExists
			if existing.ArchivedAt.Valid {
				item.Action = squadTemplateRestore
			}
			existingAgentIDs[agent.Name] = item.ID
			carried, err := h.Queries.ListAgentSkills(ctx, existing.ID)
			if err != nil {
				return nil, err
			}
			names := make(map[string]bool, len(carried))
			for _, s := range carried {
				names[s.Name] = true
			}
			in.agentSkillNames[agent.Name] = names
			for _, name := range agent.Skills {
				if !names[name] {
					item.AddSkills++
				}
			}
		}
		in.plan.Agents = append(in.plan.Agents, item)
	}

	in.plan.Squad = SquadTemplateSquadPlan{Name: tpl.SquadName, Action: squadTemplateCreate}
	// Squad names are not unique and an archived squad is gone for good, so
	// only an active squad counts as the template's; with several of that
	// name, the oldest does.
	squads, err := h.Queries.ListSquads(ctx, wsUUID)
	if err != nil {
		return nil, err
	}
	for _, squad := range squads {
		if squad.Name != tpl.SquadName {
			continue
		}
		in.plan.Squad.ID = uuidToString(squad.ID)
		in.plan.Squad.Action = squadTemplateExists
		members, err := h.Queries.ListSquadMembers(ctx, squad.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if m.MemberType == "agent" {
				in.squadAgentIDs[uuidToString(m.MemberID)] = true
			}
		}
		for _, agent := range tpl.Agents {
			if id, ok := existingAgentIDs[agent.Name]; !ok || !in.squadAgentIDs[id] {
				in.plan.Squad.AddMembers++
			}
		}
		break
	}
	return in, nil
}

// planExistingTemplateSkill decides what to do with a skill the workspace
// already has under a template skill's name.
func (h *Handler) planExistingTemplateSkill(ctx context.Context, existing db.Skill, skill squadtemplate.Skill, overwriteModified bool) (string, error) {
	current, err := h.storedSkillDigest(ctx, existing)
	if err != nil {
		return "", err
	}
	if current == skill.Digest {
		return squadTemplateUnchanged, nil
	}
	// Still exactly what an earlier template version installed: nobody's edits
	// are lost by moving it to the current version.
	if installedTemplateSkillDigest(existing.Config) == current || overwriteModified {
		return squadTemplateUpdate, nil
	}
	return squadTemplateKeep, nil
}

// storedSkillDigest is the digest of a skill as the workspace holds it now.
func (h *Handler) storedSkillDigest(ctx context.Context, skill db.Skill) (string, error) {
	rows, err := h.Queries.ListSkillFiles(ctx, skill.ID)
	if err != nil {
		return "", err
	}
	files := make([]squadtemplate.File, 0, len(rows))
	for _, f := range rows {
		files = append(files, squadtemplate.File{Path: f.Path, Content: f.Content})
	}
	return squadtemplate.SkillDigest(skill.Description, skill.Content, files), nil
}

// installedTemplateSkillDigest returns the digest a template install recorded
// on the skill, or "" when the skill did not come from a template.
func installedTemplateSkillDigest(config []byte) string {
	var decoded struct {
		Origin struct {
			Type   string `json:"type"`
			Digest string `json:"digest"`
		} `json:"origin"`
	}
	if json.Unmarshal(config, &decoded) != nil || decoded.Origin.Type != skillOriginSquadTemplate {
		return ""
	}
	return decoded.Origin.Digest
}

// templateSkillConfig returns base with its origin replaced by the template's.
func templateSkillConfig(base []byte, digest string) map[string]any {
	config := map[string]any{}
	if len(base) > 0 {
		// A config that does not decode is replaced rather than kept.
		_ = json.Unmarshal(base, &config)
	}
	config["origin"] = map[string]any{"type": skillOriginSquadTemplate, "digest": digest}
	return config
}

// applySquadTemplate carries out the plan and fills in the ids of what it
// touched. It returns a status and message when a step fails; everything
// applied before that step stays, and the next install continues from there.
func (h *Handler) applySquadTemplate(r *http.Request, member db.Member, in *squadTemplateInstall, runtimeID string) (int, string) {
	ctx := r.Context()
	workspaceID := uuidToString(in.wsUUID)
	userID := uuidToString(member.UserID)

	skillIDs := map[string]string{}
	for i := range in.plan.Skills {
		item := &in.plan.Skills[i]
		skill := in.template.Skills[i]
		files := make([]CreateSkillFileRequest, 0, len(skill.Files))
		for _, f := range skill.Files {
			files = append(files, CreateSkillFileRequest{Path: f.Path, Content: f.Content})
		}
		switch item.Action {
		case squadTemplateCreate:
			created, err := h.createSkillWithFiles(ctx, skillCreateInput{
				WorkspaceID:   in.wsUUID,
				CreatorID:     member.UserID,
				Name:          skill.Name,
				Description:   skill.Description,
				Content:       skill.Content,
				Config:        templateSkillConfig(nil, skill.Digest),
				Files:         files,
				VersionSource: skillVersionSourceImport,
			})
			if err != nil {
				if isUniqueViolation(err) {
					return http.StatusConflict, fmt.Sprintf("skill %q was created by someone else meanwhile; install again", skill.Name)
				}
				slog.Warn("squad templates: create skill failed", append(logger.RequestAttrs(r), "error", err, "skill", skill.Name)...)
				return http.StatusInternalServerError, fmt.Sprintf("failed to create skill %q", skill.Name)
			}
			item.ID = created.ID
			h.publish(protocol.EventSkillCreated, workspaceID, "member", userID, map[string]any{"skill": created})
		case squadTemplateUpdate:
			existing := in.existingSkills[skill.Name]
			updated, err := h.overwriteSkillWithFiles(ctx, skillOverwriteInput{
				WorkspaceID:    in.wsUUID,
				TargetSkillID:  existing.ID,
				UserID:         userID,
				ExpectedName:   skill.Name,
				AllowOverwrite: func(string, db.Skill) bool { return true },
				Description:    skill.Description,
				Content:        skill.Content,
				Config:         templateSkillConfig(existing.Config, skill.Digest),
				Files:          files,
			})
			if err != nil {
				slog.Warn("squad templates: update skill failed", append(logger.RequestAttrs(r), "error", err, "skill", skill.Name)...)
				return http.StatusInternalServerError, fmt.Sprintf("failed to update skill %q", skill.Name)
			}
			h.publish(protocol.EventSkillUpdated, workspaceID, "member", userID, map[string]any{"skill": updated})
		}
		skillIDs[skill.Name] = item.ID
	}

	agentIDs := map[string]string{}
	for i := range in.plan.Agents {
		item := &in.plan.Agents[i]
		agent := in.template.Agents[i]
		switch item.Action {
		case squadTemplateCreate:
			ids := make([]string, 0, len(agent.Skills))
			for _, name := range agent.Skills {
				ids = append(ids, skillIDs[name])
			}
			var created AgentResponse
			status, msg := callHandler(r, h.CreateAgent, http.MethodPost, map[string]any{
				"name":         agent.Name,
				"instructions": agent.Instructions,
				"runtime_id":   runtimeID,
				"visibility":   "workspace",
				"skill_ids":    ids,
			}, &created)
			if status != http.StatusCreated {
				return status, fmt.Sprintf("agent %q: %s", agent.Name, msg)
			}
			item.ID = created.ID
		case squadTemplateExists, squadTemplateRestore:
			if item.Action == squadTemplateRestore {
				status, msg := callHandler(r, h.RestoreAgent, http.MethodPost, nil, nil, "id", item.ID)
				if status != http.StatusOK {
					return status, fmt.Sprintf("agent %q: %s", agent.Name, msg)
				}
			}
			if item.AddSkills > 0 {
				carried := in.agentSkillNames[agent.Name]
				var missing []string
				for _, name := range agent.Skills {
					if !carried[name] {
						missing = append(missing, skillIDs[name])
					}
				}
				status, msg := callHandler(r, h.AddAgentSkills, http.MethodPost,
					map[string]any{"skill_ids": missing}, nil, "id", item.ID)
				if status != http.StatusOK {
					return status, fmt.Sprintf("agent %q: %s", agent.Name, msg)
				}
			}
		}
		agentIDs[agent.Name] = item.ID
	}

	squad := &in.plan.Squad
	if squad.Action == squadTemplateCreate {
		var created SquadResponse
		status, msg := callHandler(r, h.CreateSquad, http.MethodPost, map[string]any{
			"name":        in.template.SquadName,
			"description": in.template.SquadDescription,
			"leader_id":   agentIDs[in.template.Leader],
		}, &created)
		if status != http.StatusCreated {
			return status, fmt.Sprintf("squad %q: %s", in.template.SquadName, msg)
		}
		squad.ID = created.ID
		// The leader joins with the squad; record it so the member loop skips it.
		in.squadAgentIDs[agentIDs[in.template.Leader]] = true

		status, msg = callHandler(r, h.UpdateSquad, http.MethodPut,
			map[string]any{"instructions": in.template.SquadInstructions}, nil, "id", squad.ID)
		if status != http.StatusOK {
			return status, fmt.Sprintf("squad %q: %s", in.template.SquadName, msg)
		}
	}
	for _, agent := range in.template.Agents {
		id := agentIDs[agent.Name]
		if in.squadAgentIDs[id] {
			continue
		}
		status, msg := callHandler(r, h.AddSquadMember, http.MethodPost, map[string]any{
			"member_type": "agent",
			"member_id":   id,
			"role":        agent.Role,
		}, nil, "id", squad.ID)
		// Someone adding the same member meanwhile leaves the squad as wanted.
		if status != http.StatusCreated && status != http.StatusConflict {
			return status, fmt.Sprintf("squad member %q: %s", agent.Name, msg)
		}
	}
	return 0, ""
}

// capturedResponse records what a handler wrote instead of sending it.
type capturedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *capturedResponse) Header() http.Header { return c.header }

func (c *capturedResponse) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *capturedResponse) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(p)
}

// callHandler runs another endpoint's handler as the caller of r and returns
// its status, decoding a JSON body into out when out is non-nil. On a failure
// status the second result is the handler's error message.
//
// Installing a template goes through the ordinary create handlers this way so
// that each agent and squad passes the same permission checks, takes the same
// locks and emits the same events as one created by hand.
//
// urlParams are alternating chi path-parameter names and values; the caller's
// own path parameters stay visible to fn.
func callHandler(r *http.Request, fn http.HandlerFunc, method string, body, out any, urlParams ...string) (int, string) {
	payload, err := json.Marshal(body)
	if err != nil {
		return http.StatusInternalServerError, "failed to encode request"
	}

	rctx := chi.NewRouteContext()
	if current := chi.RouteContext(r.Context()); current != nil {
		for i, key := range current.URLParams.Keys {
			rctx.URLParams.Add(key, current.URLParams.Values[i])
		}
	}
	for i := 0; i+1 < len(urlParams); i += 2 {
		rctx.URLParams.Add(urlParams[i], urlParams[i+1])
	}

	sub := r.Clone(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	sub.Method = method
	sub.Body = io.NopCloser(bytes.NewReader(payload))
	sub.ContentLength = int64(len(payload))

	rec := &capturedResponse{header: http.Header{}}
	fn(rec, sub)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}

	if rec.status >= http.StatusBadRequest {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(rec.body.Bytes(), &failure) != nil || failure.Error == "" {
			failure.Error = http.StatusText(rec.status)
		}
		return rec.status, failure.Error
	}
	if out != nil {
		if err := json.Unmarshal(rec.body.Bytes(), out); err != nil {
			return http.StatusInternalServerError, "failed to read the created resource"
		}
	}
	return rec.status, ""
}
