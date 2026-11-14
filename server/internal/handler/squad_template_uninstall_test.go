package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// uninstall posts to the uninstall endpoint as userID ("" is the workspace owner).
func (f *squadTemplateFixture) uninstall(t *testing.T, userID, key string, body map[string]any) *testutil.Response {
	t.Helper()
	req := squadScopeReq(userID, http.MethodPost, "/api/squad-templates/"+key+"/uninstall", body, map[string]string{"key": key})
	return testutil.Call(t, testHandler.UninstallSquadTemplate, req)
}

func (f *squadTemplateFixture) removalPlan(t *testing.T, removeModified bool) UninstallSquadTemplateResponse {
	t.Helper()
	var plan UninstallSquadTemplateResponse
	f.uninstall(t, "", "pair", map[string]any{"dry_run": true, "remove_modified": removeModified}).Want(http.StatusOK).JSON(&plan)
	return plan
}

func (f *squadTemplateFixture) mustUninstall(t *testing.T, removeModified bool) UninstallSquadTemplateResponse {
	t.Helper()
	var result UninstallSquadTemplateResponse
	f.uninstall(t, "", "pair", map[string]any{"remove_modified": removeModified}).Want(http.StatusOK).JSON(&result)
	return result
}

// removals renders a plan as "squad | Lead, Dev | check, build", each resource
// as its action, with the reason when it is kept.
func removals(plan UninstallSquadTemplateResponse) string {
	one := func(item SquadTemplateRemoval) string {
		if item.Reason != "" {
			return item.Action + "(" + item.Reason + ")"
		}
		return item.Action
	}
	many := func(items []SquadTemplateRemoval) string {
		parts := make([]string, 0, len(items))
		for _, item := range items {
			parts = append(parts, one(item))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%s | %s | %s", one(plan.Squad), many(plan.Agents), many(plan.Skills))
}

// installed reports what is left of an install: whether the squad and each
// agent are archived, and how many of the two skills exist.
func (f *squadTemplateFixture) installed(t *testing.T, install InstallSquadTemplateResponse) string {
	t.Helper()
	state := func(table, id string) string {
		var archived bool
		dbfx.QueryRow(t, `SELECT archived_at IS NOT NULL FROM `+table+` WHERE id = $1`, id).Scan(&archived)
		if archived {
			return "archived"
		}
		return "active"
	}
	var skills int
	dbfx.QueryRow(t, `SELECT count(*) FROM skill WHERE workspace_id = $1 AND name LIKE $2`, testWorkspaceID, f.prefix+"%").Scan(&skills)
	return fmt.Sprintf("squad %s, Lead %s, Dev %s, %d skills",
		state("squad", install.Squad.ID), state("agent", install.Agents[0].ID), state("agent", install.Agents[1].ID), skills)
}

func TestUninstallSquadTemplate_RemovesAnUntouchedInstall(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplRemove ")
	install := f.mustInstall(t, nil)

	if got, want := removals(f.removalPlan(t, false)), "archive | archive, archive | delete, delete"; got != want {
		t.Fatalf("removal plan = %s, want %s", got, want)
	}
	if got, want := f.installed(t, install), "squad active, Lead active, Dev active, 2 skills"; got != want {
		t.Fatalf("a dry run changed the workspace: %s", got)
	}

	f.mustUninstall(t, false)
	if got, want := f.installed(t, install), "squad archived, Lead archived, Dev archived, 0 skills"; got != want {
		t.Fatalf("after uninstall: %s, want %s", got, want)
	}
	if got, want := removals(f.removalPlan(t, false)), "absent | absent, absent | absent, absent"; got != want {
		t.Errorf("removal plan after uninstall = %s, want %s", got, want)
	}

	// Installing again brings the same agents back rather than failing on
	// their names, and gives them a fresh squad and fresh skills.
	plan := f.plan(t, nil)
	if skillActions(plan) != "create,create" || agentActions(plan) != "restore,restore" || plan.Squad.Action != squadTemplateCreate {
		t.Fatalf("plan after uninstall = %+v, want the agents restored and the rest created", plan)
	}
	again := f.mustInstall(t, nil)
	var devSkills, members int
	dbfx.QueryRow(t, `SELECT count(*) FROM agent_skill WHERE agent_id = $1`, again.Agents[1].ID).Scan(&devSkills)
	dbfx.QueryRow(t, `SELECT count(*) FROM squad_member WHERE squad_id = $1`, again.Squad.ID).Scan(&members)
	if again.Agents[1].ID != install.Agents[1].ID || again.Squad.ID == install.Squad.ID || devSkills != 2 || members != 2 {
		t.Errorf("reinstall: Dev %s (was %s), squad %s (was %s), Dev skills %d, members %d",
			again.Agents[1].ID, install.Agents[1].ID, again.Squad.ID, install.Squad.ID, devSkills, members)
	}
}

// The squad was archived by hand first, which is what leaves agents and skills
// behind with nothing pointing at them.
func TestUninstallSquadTemplate_CleansUpAfterAnArchivedSquad(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplLeftover ")
	install := f.mustInstall(t, nil)
	dbfx.Exec(t, `UPDATE squad SET archived_at = now() WHERE id = $1`, install.Squad.ID)

	if got, want := removals(f.removalPlan(t, false)), "absent | archive, archive | delete, delete"; got != want {
		t.Fatalf("removal plan = %s, want %s", got, want)
	}
	f.mustUninstall(t, false)
	if got, want := f.installed(t, install), "squad archived, Lead archived, Dev archived, 0 skills"; got != want {
		t.Errorf("after uninstall: %s, want %s", got, want)
	}
}

func TestUninstallSquadTemplate_KeepsWhatWasChanged(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, f *squadTemplateFixture, install InstallSquadTemplateResponse)
		// plan is the removal plan by default; left is the workspace after
		// uninstalling with that default.
		plan string
		left string
	}{
		{
			name: "agent instructions",
			change: func(t *testing.T, f *squadTemplateFixture, install InstallSquadTemplateResponse) {
				dbfx.Exec(t, `UPDATE agent SET instructions = 'Edited by the team.' WHERE id = $1`, install.Agents[1].ID)
			},
			// Dev stays, and so do the skills Dev carries.
			plan: "archive | archive, keep(modified) | keep(in_use), keep(in_use)",
			left: "squad archived, Lead archived, Dev active, 2 skills",
		},
		{
			name: "skill content",
			change: func(t *testing.T, f *squadTemplateFixture, install InstallSquadTemplateResponse) {
				dbfx.Exec(t, `UPDATE skill SET content = 'Our own build steps.' WHERE id = $1`, install.Skills[1].ID)
			},
			plan: "archive | archive, archive | delete, keep(modified)",
			left: "squad archived, Lead archived, Dev archived, 1 skills",
		},
		{
			name: "skill file",
			change: func(t *testing.T, f *squadTemplateFixture, install InstallSquadTemplateResponse) {
				dbfx.Exec(t, `UPDATE skill_file SET content = 'echo ours' WHERE skill_id = $1`, install.Skills[0].ID)
			},
			plan: "archive | archive, archive | keep(modified), delete",
			left: "squad archived, Lead archived, Dev archived, 1 skills",
		},
		{
			name: "squad instructions",
			change: func(t *testing.T, f *squadTemplateFixture, install InstallSquadTemplateResponse) {
				dbfx.Exec(t, `UPDATE squad SET instructions = 'Edited squad.' WHERE id = $1`, install.Squad.ID)
			},
			// A squad that stays keeps its agents, and they keep their skills.
			plan: "keep(modified) | keep(in_squad), keep(in_squad) | keep(in_use), keep(in_use)",
			left: "squad active, Lead active, Dev active, 2 skills",
		},
		{
			name: "squad member added",
			change: func(t *testing.T, f *squadTemplateFixture, install InstallSquadTemplateResponse) {
				extra := createHandlerTestAgent(t, f.name("Extra"), nil)
				dbfx.Exec(t, `INSERT INTO squad_member (squad_id, member_type, member_id, role) VALUES ($1, 'agent', $2, 'Helper')`, install.Squad.ID, extra)
			},
			plan: "keep(modified) | keep(in_squad), keep(in_squad) | keep(in_use), keep(in_use)",
			left: "squad active, Lead active, Dev active, 2 skills",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSquadTemplateFixture(t, fmt.Sprintf("TplChanged%d ", i))
			install := f.mustInstall(t, nil)
			tc.change(t, f, install)

			if got := removals(f.removalPlan(t, false)); got != tc.plan {
				t.Fatalf("removal plan = %s, want %s", got, tc.plan)
			}
			f.mustUninstall(t, false)
			if got := f.installed(t, install); got != tc.left {
				t.Fatalf("after uninstall: %s, want %s", got, tc.left)
			}

			// Asked to, the uninstall takes the changed resources too.
			f.mustUninstall(t, true)
			if got, want := f.installed(t, install), "squad archived, Lead archived, Dev archived, 0 skills"; got != want {
				t.Errorf("after uninstall with remove_modified: %s, want %s", got, want)
			}
		})
	}
}

// These protect something the uninstall has no claim on, so asking for
// modified resources to go does not override them.
func TestUninstallSquadTemplate_KeepsWhatIsStillNeeded(t *testing.T) {
	t.Run("agent in another squad", func(t *testing.T) {
		f := newSquadTemplateFixture(t, "TplShared ")
		install := f.mustInstall(t, nil)
		createSquadAs(t, "", f.name("Other squad"), install.Agents[0].ID)

		// Lead stays for the other squad, with the skill Lead carries.
		want := "archive | keep(in_squad), archive | keep(in_use), delete"
		if got := removals(f.removalPlan(t, true)); got != want {
			t.Fatalf("removal plan = %s, want %s", got, want)
		}
		f.mustUninstall(t, true)
		if got, want := f.installed(t, install), "squad archived, Lead active, Dev archived, 1 skills"; got != want {
			t.Errorf("after uninstall: %s, want %s", got, want)
		}
	})

	t.Run("unfinished issues", func(t *testing.T) {
		f := newSquadTemplateFixture(t, "TplBusy ")
		install := f.mustInstall(t, nil)
		// One issue is the squad's, which archiving hands to the leader; a
		// finished one of Dev's does not count.
		squadIssue := dbfx.Issue(t, "Squad work", testutil.Cols{"assignee_type": "squad", "assignee_id": install.Squad.ID})
		dbfx.Issue(t, "Finished work", testutil.Cols{"assignee_type": "agent", "assignee_id": install.Agents[1].ID, "status": "done"})

		want := "archive | keep(open_issues), archive | keep(in_use), delete"
		if got := removals(f.removalPlan(t, true)); got != want {
			t.Fatalf("removal plan = %s, want %s", got, want)
		}
		f.mustUninstall(t, true)
		if got, want := f.installed(t, install), "squad archived, Lead active, Dev archived, 1 skills"; got != want {
			t.Errorf("after uninstall: %s, want %s", got, want)
		}
		var assignee string
		dbfx.QueryRow(t, `SELECT assignee_id FROM issue WHERE id = $1`, squadIssue).Scan(&assignee)
		if assignee != install.Agents[0].ID {
			t.Errorf("the squad's issue went to %s, want the leader %s", assignee, install.Agents[0].ID)
		}
	})

	t.Run("skill no template installed", func(t *testing.T) {
		f := newSquadTemplateFixture(t, "TplOwn ")
		dbfx.Exec(t, `INSERT INTO skill (workspace_id, name, description, content) VALUES ($1, $2, 'Hand-written', 'Ours.')`,
			testWorkspaceID, f.name("check"))
		install := f.mustInstall(t, nil)

		want := "archive | archive, archive | keep(not_installed), delete"
		if got := removals(f.removalPlan(t, true)); got != want {
			t.Fatalf("removal plan = %s, want %s", got, want)
		}
		f.mustUninstall(t, true)
		if got, want := f.installed(t, install), "squad archived, Lead archived, Dev archived, 1 skills"; got != want {
			t.Errorf("after uninstall: %s, want %s", got, want)
		}
	})

	t.Run("skill another agent uses", func(t *testing.T) {
		f := newSquadTemplateFixture(t, "TplBorrowed ")
		install := f.mustInstall(t, nil)
		other := createHandlerTestAgent(t, f.name("Borrower"), nil)
		dbfx.Exec(t, `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, other, install.Skills[1].ID)

		want := "archive | archive, archive | delete, keep(in_use)"
		if got := removals(f.removalPlan(t, true)); got != want {
			t.Fatalf("removal plan = %s, want %s", got, want)
		}
	})
}

func TestUninstallSquadTemplate_Refusals(t *testing.T) {
	f := newSquadTemplateFixture(t, "TplNoRemove ")
	install := f.mustInstall(t, nil)

	memberID := createPlainMember(t, "squad-template-uninstall@multica.test")
	f.uninstall(t, memberID, "pair", map[string]any{}).Want(http.StatusForbidden)
	f.uninstall(t, "", "missing", map[string]any{}).Want(http.StatusNotFound)

	if got, want := f.installed(t, install), "squad active, Lead active, Dev active, 2 skills"; got != want {
		t.Errorf("a refused uninstall changed the workspace: %s", got)
	}
}
