package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/multica-ai/multica/server/internal/squadtemplate"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// squadTemplateFixture is a two-agent template whose names carry a per-test
// prefix, so installs from different tests never meet in the shared database.
type squadTemplateFixture struct {
	prefix string
	files  fstest.MapFS
}

func (f *squadTemplateFixture) name(s string) string { return f.prefix + s }

// newSquadTemplateFixture serves the fixture as the server's catalog for the
// rest of the test and removes whatever the test installs from it.
func newSquadTemplateFixture(t *testing.T, prefix string) *squadTemplateFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := &squadTemplateFixture{prefix: prefix}
	f.files = fstest.MapFS{
		"manifest.json": {Data: []byte(`{
			"root": "src",
			"templates": [{
				"key": "pair",
				"title": {"en": "Pair", "zh": "结对"},
				"summary": {"en": "Two agents", "zh": "两个智能体"},
				"squad": {"name": "` + f.name("Squad") + `", "description": "Pair squad", "instructions_file": "squad.md", "leader": "lead"},
				"agents": ["lead", "dev"]
			}],
			"agents": {
				"lead": {"name": "` + f.name("Lead") + `", "role": "Leader", "skills": ["` + f.name("check") + `"]},
				"dev": {"name": "` + f.name("Dev") + `", "role": "Developer", "skills": ["` + f.name("check") + `", "` + f.name("build") + `"]}
			}
		}`)},
		"src/agents/lead.md": {Data: []byte("```text\nRoute work.\n```\n")},
		"src/agents/dev.md":  {Data: []byte("```text\nWrite the code.\n```\n")},
		"src/squad.md":       {Data: []byte("```text\nLead routes, Dev builds.\n```\n")},
	}
	f.setSkill("check", "Verify the work", "# Check v1\n")
	f.files["src/skills/check/scripts/run.sh"] = &fstest.MapFile{Data: []byte("echo ok\n")}
	f.setSkill("build", "Build it", "# Build\n")

	previous := squadTemplateCatalog
	squadTemplateCatalog = func() (*squadtemplate.Catalog, error) { return squadtemplate.Load(f.files) }
	t.Cleanup(func() {
		squadTemplateCatalog = previous
		ctx := context.Background()
		like := prefix + "%"
		testPool.Exec(ctx, `DELETE FROM squad_member WHERE squad_id IN (SELECT id FROM squad WHERE workspace_id = $1 AND name LIKE $2)`, testWorkspaceID, like)
		testPool.Exec(ctx, `DELETE FROM squad WHERE workspace_id = $1 AND name LIKE $2`, testWorkspaceID, like)
		testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name LIKE $2`, testWorkspaceID, like)
		testPool.Exec(ctx, `DELETE FROM skill WHERE workspace_id = $1 AND name LIKE $2`, testWorkspaceID, like)
	})
	return f
}

// setSkill writes the SKILL.md the catalog ships for a skill.
func (f *squadTemplateFixture) setSkill(name, description, body string) {
	f.files["src/skills/"+name+"/SKILL.md"] = &fstest.MapFile{
		Data: []byte("---\nname: " + f.name(name) + "\ndescription: " + description + "\n---\n\n" + body),
	}
}

// install posts to the install endpoint as userID ("" is the workspace owner).
func (f *squadTemplateFixture) install(t *testing.T, userID, key string, body map[string]any) *testutil.Response {
	t.Helper()
	req := squadScopeReq(userID, http.MethodPost, "/api/squad-templates/"+key+"/install", body, map[string]string{"key": key})
	return testutil.Call(t, testHandler.InstallSquadTemplate, req)
}

func (f *squadTemplateFixture) plan(t *testing.T, body map[string]any) InstallSquadTemplateResponse {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["dry_run"] = true
	var plan InstallSquadTemplateResponse
	f.install(t, "", "pair", body).Want(http.StatusOK).JSON(&plan)
	return plan
}

func (f *squadTemplateFixture) mustInstall(t *testing.T, body map[string]any) InstallSquadTemplateResponse {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["runtime_id"] = handlerTestRuntimeID(t)
	var result InstallSquadTemplateResponse
	f.install(t, "", "pair", body).Want(http.StatusOK).JSON(&result)
	return result
}

func skillActions(plan InstallSquadTemplateResponse) string {
	parts := make([]string, 0, len(plan.Skills))
	for _, s := range plan.Skills {
		parts = append(parts, s.Action)
	}
	return strings.Join(parts, ",")
}

func agentActions(plan InstallSquadTemplateResponse) string {
	parts := make([]string, 0, len(plan.Agents))
	for _, a := range plan.Agents {
		parts = append(parts, a.Action)
	}
	return strings.Join(parts, ",")
}

func TestInstallSquadTemplate_CreatesTheSquad(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplCreate ")

	plan := f.plan(t, nil)
	if skillActions(plan) != "create,create" || agentActions(plan) != "create,create" || plan.Squad.Action != squadTemplateCreate {
		t.Fatalf("plan for an empty workspace = %+v, want everything created", plan)
	}
	var planned int
	dbfx.QueryRow(t, `SELECT count(*) FROM skill WHERE workspace_id = $1 AND name LIKE $2`, testWorkspaceID, f.prefix+"%").Scan(&planned)
	if planned != 0 {
		t.Fatalf("a dry run created %d skills", planned)
	}

	result := f.mustInstall(t, nil)

	var skillContent, runScript string
	dbfx.QueryRow(t, `SELECT content FROM skill WHERE id = $1`, result.Skills[0].ID).Scan(&skillContent)
	dbfx.QueryRow(t, `SELECT content FROM skill_file WHERE skill_id = $1 AND path = 'scripts/run.sh'`, result.Skills[0].ID).Scan(&runScript)
	if !strings.Contains(skillContent, "# Check v1") || runScript != "echo ok\n" {
		t.Errorf("installed skill content = %q, script = %q", skillContent, runScript)
	}

	var instructions, runtimeID string
	var devSkills int
	dev := result.Agents[1]
	dbfx.QueryRow(t, `SELECT instructions, runtime_id FROM agent WHERE id = $1`, dev.ID).Scan(&instructions, &runtimeID)
	dbfx.QueryRow(t, `SELECT count(*) FROM agent_skill WHERE agent_id = $1`, dev.ID).Scan(&devSkills)
	if instructions != "Write the code." || runtimeID != handlerTestRuntimeID(t) || devSkills != 2 {
		t.Errorf("Dev: instructions = %q, runtime = %s, skills = %d", instructions, runtimeID, devSkills)
	}

	var squadInstructions, leaderID, devRole string
	var members int
	dbfx.QueryRow(t, `SELECT instructions, leader_id FROM squad WHERE id = $1`, result.Squad.ID).Scan(&squadInstructions, &leaderID)
	dbfx.QueryRow(t, `SELECT count(*) FROM squad_member WHERE squad_id = $1`, result.Squad.ID).Scan(&members)
	dbfx.QueryRow(t, `SELECT role FROM squad_member WHERE squad_id = $1 AND member_id = $2`, result.Squad.ID, dev.ID).Scan(&devRole)
	if squadInstructions != "Lead routes, Dev builds." || leaderID != result.Agents[0].ID || members != 2 || devRole != "Developer" {
		t.Errorf("squad: instructions = %q, leader = %s, members = %d, Dev role = %q", squadInstructions, leaderID, members, devRole)
	}

	again := f.plan(t, nil)
	if skillActions(again) != "unchanged,unchanged" || agentActions(again) != "exists,exists" ||
		again.Squad.Action != squadTemplateExists || again.Squad.AddMembers != 0 || again.Agents[1].AddSkills != 0 {
		t.Errorf("plan after an install = %+v, want nothing left to do", again)
	}
}

func TestInstallSquadTemplate_FillsGapsAndKeepsEdits(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplGaps ")
	first := f.mustInstall(t, nil)
	dev := first.Agents[1]

	dbfx.Exec(t, `UPDATE agent SET instructions = 'Edited by the team.' WHERE id = $1`, dev.ID)
	dbfx.Exec(t, `UPDATE squad SET instructions = 'Edited squad.' WHERE id = $1`, first.Squad.ID)
	dbfx.Exec(t, `DELETE FROM agent_skill WHERE agent_id = $1 AND skill_id = $2`, dev.ID, first.Skills[1].ID)
	dbfx.Exec(t, `DELETE FROM squad_member WHERE squad_id = $1 AND member_id = $2`, first.Squad.ID, dev.ID)

	plan := f.plan(t, nil)
	if plan.Agents[1].AddSkills != 1 || plan.Squad.AddMembers != 1 {
		t.Fatalf("plan = %+v, want one skill and one member to add", plan)
	}

	f.mustInstall(t, nil)

	var instructions, squadInstructions string
	var devSkills, members int
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, dev.ID).Scan(&instructions)
	dbfx.QueryRow(t, `SELECT instructions FROM squad WHERE id = $1`, first.Squad.ID).Scan(&squadInstructions)
	dbfx.QueryRow(t, `SELECT count(*) FROM agent_skill WHERE agent_id = $1`, dev.ID).Scan(&devSkills)
	dbfx.QueryRow(t, `SELECT count(*) FROM squad_member WHERE squad_id = $1`, first.Squad.ID).Scan(&members)
	if devSkills != 2 || members != 2 {
		t.Errorf("after reinstall: Dev skills = %d, squad members = %d, want 2 and 2", devSkills, members)
	}
	if instructions != "Edited by the team." || squadInstructions != "Edited squad." {
		t.Errorf("reinstall overwrote instructions: agent = %q, squad = %q", instructions, squadInstructions)
	}
}

func TestInstallSquadTemplate_UpdatesOnlyUntouchedSkills(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplSkills ")
	first := f.mustInstall(t, nil)
	checkID, buildID := first.Skills[0].ID, first.Skills[1].ID

	// The template moves on for both skills; the team has edited one of them.
	f.setSkill("check", "Verify the work", "# Check v2\n")
	f.setSkill("build", "Build it", "# Build v2\n")
	dbfx.Exec(t, `UPDATE skill SET content = 'Our own build steps.' WHERE id = $1`, buildID)

	if got := skillActions(f.plan(t, nil)); got != "update,keep" {
		t.Fatalf("skill plan = %s, want update,keep", got)
	}
	f.mustInstall(t, nil)

	var check, build string
	var checkVersions int
	dbfx.QueryRow(t, `SELECT content FROM skill WHERE id = $1`, checkID).Scan(&check)
	dbfx.QueryRow(t, `SELECT content FROM skill WHERE id = $1`, buildID).Scan(&build)
	dbfx.QueryRow(t, `SELECT count(*) FROM skill_version WHERE skill_id = $1`, checkID).Scan(&checkVersions)
	if !strings.Contains(check, "# Check v2") || checkVersions != 2 {
		t.Errorf("untouched skill: content = %q, versions = %d, want v2 recorded as a second version", check, checkVersions)
	}
	if build != "Our own build steps." {
		t.Errorf("edited skill was overwritten: %q", build)
	}

	if got := skillActions(f.plan(t, map[string]any{"overwrite_modified_skills": true})); got != "unchanged,update" {
		t.Fatalf("skill plan with overwrite = %s, want unchanged,update", got)
	}
	f.mustInstall(t, map[string]any{"overwrite_modified_skills": true})
	dbfx.QueryRow(t, `SELECT content FROM skill WHERE id = $1`, buildID).Scan(&build)
	if !strings.Contains(build, "# Build v2") {
		t.Errorf("overwrite left the edited skill as %q", build)
	}
}

func TestInstallSquadTemplate_KeepsASkillItDidNotInstall(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplForeign ")
	dbfx.Exec(t, `INSERT INTO skill (workspace_id, name, description, content) VALUES ($1, $2, 'Hand-written', 'Ours.')`,
		testWorkspaceID, f.name("check"))

	if got := skillActions(f.plan(t, nil)); got != "keep,create" {
		t.Fatalf("skill plan = %s, want keep,create", got)
	}
	result := f.mustInstall(t, nil)

	var content string
	var bound int
	dbfx.QueryRow(t, `SELECT content FROM skill WHERE id = $1`, result.Skills[0].ID).Scan(&content)
	dbfx.QueryRow(t, `SELECT count(*) FROM agent_skill WHERE skill_id = $1`, result.Skills[0].ID).Scan(&bound)
	if content != "Ours." || bound != 2 {
		t.Errorf("hand-written skill: content = %q, bound to %d agents; want it kept and bound to both", content, bound)
	}
}

// Squad names are not unique, and an archived squad cannot be restored or
// renamed from the UI, so one must never stand in the way of a fresh install.
func TestInstallSquadTemplate_IgnoresAnArchivedSquad(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplArchived ")
	first := f.mustInstall(t, nil)
	dbfx.Exec(t, `UPDATE squad SET archived_at = now() WHERE id = $1`, first.Squad.ID)

	plan := f.plan(t, nil)
	if plan.Squad.Action != squadTemplateCreate || agentActions(plan) != "exists,exists" {
		t.Fatalf("plan after archiving the squad = %+v, want a new squad with the same agents", plan)
	}

	second := f.mustInstall(t, nil)
	if second.Squad.ID == "" || second.Squad.ID == first.Squad.ID {
		t.Fatalf("reinstall returned squad %q, want a new one (archived: %s)", second.Squad.ID, first.Squad.ID)
	}
	var members int
	var archived bool
	dbfx.QueryRow(t, `SELECT count(*) FROM squad_member WHERE squad_id = $1`, second.Squad.ID).Scan(&members)
	dbfx.QueryRow(t, `SELECT archived_at IS NOT NULL FROM squad WHERE id = $1`, first.Squad.ID).Scan(&archived)
	if members != 2 || !archived {
		t.Errorf("new squad has %d members (want 2); old squad archived = %v (want true)", members, archived)
	}
}

func TestInstallSquadTemplate_Refusals(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplRefuse ")

	t.Run("plain member", func(t *testing.T) {
		memberID := createPlainMember(t, "squad-template-member@multica.test")
		f.install(t, memberID, "pair", map[string]any{"dry_run": true}).Want(http.StatusForbidden)
	})
	t.Run("unknown template", func(t *testing.T) {
		f.install(t, "", "missing", map[string]any{"dry_run": true}).Want(http.StatusNotFound)
	})
	t.Run("no runtime for new agents", func(t *testing.T) {
		f.install(t, "", "pair", map[string]any{}).Want(http.StatusBadRequest)
	})
}

// Agent names stay taken while the agent is archived, so the template can only
// have its agent back by restoring the one that holds the name.
func TestInstallSquadTemplate_RestoresAnArchivedAgent(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplRestore ")
	first := f.mustInstall(t, nil)
	dev := first.Agents[1]
	dbfx.Exec(t, `UPDATE agent SET archived_at = now(), instructions = 'Edited by the team.' WHERE id = $1`, dev.ID)

	plan := f.plan(t, nil)
	if agentActions(plan) != "exists,restore" || plan.Squad.Action != squadTemplateExists {
		t.Fatalf("plan with an archived agent = %+v, want it restored into the existing squad", plan)
	}

	// Every agent already exists, so no runtime is needed.
	var result InstallSquadTemplateResponse
	f.install(t, "", "pair", map[string]any{}).Want(http.StatusOK).JSON(&result)

	var archived bool
	var instructions string
	dbfx.QueryRow(t, `SELECT archived_at IS NOT NULL, instructions FROM agent WHERE id = $1`, dev.ID).Scan(&archived, &instructions)
	if result.Agents[1].ID != dev.ID || archived || instructions != "Edited by the team." {
		t.Errorf("after install: agent %s (want %s), archived = %v, instructions = %q", result.Agents[1].ID, dev.ID, archived, instructions)
	}
}

func TestListSquadTemplates(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplList ")

	var templates []SquadTemplateResponse
	req := squadScopeReq("", http.MethodGet, "/api/squad-templates", nil, nil)
	testutil.Call(t, testHandler.ListSquadTemplates, req).Want(http.StatusOK).JSON(&templates)

	if len(templates) != 1 {
		t.Fatalf("got %d templates, want 1", len(templates))
	}
	got := templates[0]
	if got.Key != "pair" || got.Title.ZH != "结对" || got.Leader != f.name("Lead") || len(got.Agents) != 2 || len(got.Skills) != 2 {
		t.Errorf("template = %+v", got)
	}
	if strings.Join(got.Agents[1].Skills, ",") != f.name("check")+","+f.name("build") {
		t.Errorf("Dev skills = %v", got.Agents[1].Skills)
	}
}
