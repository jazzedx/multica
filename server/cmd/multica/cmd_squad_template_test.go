package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSquadTemplateDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func squadTemplateFiles(squadInstructions string) map[string]string {
	return map[string]string{
		"manifest.json": `{
			"root": "src",
			"templates": [{
				"key": "pair",
				"title": {"en": "Pair", "zh": "结对"},
				"summary": {"en": "Two agents", "zh": "两个智能体"},
				"squad": {"name": "Pair squad", "instructions_file": "squad/pair/squad.md", "leader": "lead"},
				"agents": ["lead", "dev"]
			}],
			"agents": {
				"lead": {"name": "Lead", "role": "Leader", "skills": ["check"]},
				"dev": {"name": "Dev", "role": "Developer", "skills": ["check"]}
			}
		}`,
		"src/agents/lead.md":          "Route the work.\n",
		"src/agents/dev.md":           "Write the code.\n",
		"src/squad/pair/squad.md":     squadInstructions,
		"src/skills/a/check/SKILL.md": "---\nname: check\ndescription: Verify the work\n---\n\n# Check\n",
	}
}

func runSquadTemplateCheck(t *testing.T, dir string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	squadTemplateCheckCmd.SetOut(&out)
	t.Cleanup(func() { squadTemplateCheckCmd.SetOut(nil) })
	err := squadTemplateCheckCmd.RunE(squadTemplateCheckCmd, []string{dir})
	return out.String(), err
}

func TestSquadTemplateCheckListsASoundDirectory(t *testing.T) {
	out, err := runSquadTemplateCheck(t, writeSquadTemplateDir(t, squadTemplateFiles("Lead routes, @Dev builds.\n")))
	if err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	if want := "pair: 2 agents, 1 skills, led by Lead"; !strings.Contains(out, want) {
		t.Fatalf("output %q lacks %q", out, want)
	}
}

func TestSquadTemplateCheckFailsOnProblems(t *testing.T) {
	out, err := runSquadTemplateCheck(t, writeSquadTemplateDir(t, squadTemplateFiles("Lead does everything.\n")))
	if err == nil {
		t.Fatalf("a squad that never addresses a member passed:\n%s", out)
	}
	if want := "problem: pair: squad instructions never mention @Dev"; !strings.Contains(out, want) {
		t.Fatalf("output %q lacks %q", out, want)
	}

	if _, err := runSquadTemplateCheck(t, t.TempDir()); err == nil {
		t.Fatal("a directory without a manifest passed")
	}
}
