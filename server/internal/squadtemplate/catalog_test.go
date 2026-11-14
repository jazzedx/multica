package squadtemplate

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestDefaultCatalogIsInstallable(t *testing.T) {
	catalog, err := Default()
	if err != nil {
		t.Fatalf("load built-in catalog: %v", err)
	}
	if len(catalog.Templates) == 0 {
		t.Fatal("built-in catalog has no templates")
	}
	for _, tpl := range catalog.Templates {
		if tpl.Title.EN == "" || tpl.Title.ZH == "" || tpl.Summary.EN == "" || tpl.Summary.ZH == "" {
			t.Errorf("%s: title and summary need both languages", tpl.Key)
		}
		if tpl.SquadInstructions == "" {
			t.Errorf("%s: squad has no instructions", tpl.Key)
		}
		carried := map[string]bool{}
		for _, skill := range tpl.Skills {
			carried[skill.Name] = true
			if skill.Description == "" || skill.Content == "" {
				t.Errorf("%s: skill %s lacks a description or content", tpl.Key, skill.Name)
			}
		}
		for _, agent := range tpl.Agents {
			// Squad instructions address members as @Name, so a name that an
			// install had to change would break routing.
			if agent.Name != tpl.Leader && !strings.Contains(tpl.SquadInstructions, "@"+agent.Name) {
				t.Errorf("%s: squad instructions never mention @%s", tpl.Key, agent.Name)
			}
			for _, name := range agent.Skills {
				if !carried[name] {
					t.Errorf("%s: agent %s uses skill %s that the template does not carry", tpl.Key, agent.Name, name)
				}
			}
		}
	}
}

func testCatalog() fstest.MapFS {
	return fstest.MapFS{
		"manifest.json": {Data: []byte(`{
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
				"dev": {"name": "Dev", "role": "Developer", "skills": ["check", "build"]}
			}
		}`)},
		"src/agents/lead.md":                     {Data: []byte("# Lead\n\nNotes for maintainers.\n\n```text\nRoute work to @Dev.\n```\n")},
		"src/agents/dev.md":                      {Data: []byte("Write the code.\n")},
		"src/squad/pair/squad.md":                {Data: []byte("```text\nLead routes, @Dev builds.\n```\n")},
		"src/skills/a/check/SKILL.md":            {Data: []byte("---\nname: check\ndescription: Verify the work\n---\n\n# Check\n")},
		"src/skills/a/check/scripts/run.sh":      {Data: []byte("echo ok\n")},
		"src/skills/a/check/scripts/__init__.py": {Data: []byte("")},
		"src/skills/a/check/.env.example":        {Data: []byte("TOKEN=\n")},
		"src/skills/a/check/LICENSE":             {Data: []byte("MIT\n")},
		"src/skills/b/build/SKILL.md":            {Data: []byte("---\nname: build\ndescription: Build it\n---\n")},
	}
}

func TestLoadResolvesTemplates(t *testing.T) {
	catalog, err := Load(testCatalog())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tpl, ok := catalog.Template("pair")
	if !ok {
		t.Fatal("template pair not found")
	}
	if tpl.Leader != "Lead" {
		t.Errorf("Leader = %q, want Lead", tpl.Leader)
	}
	if got := tpl.SquadInstructions; got != "Lead routes, @Dev builds." {
		t.Errorf("SquadInstructions = %q", got)
	}
	if got := tpl.Agents[0].Instructions; got != "Route work to @Dev." {
		t.Errorf("instructions from a text block = %q", got)
	}
	if got := tpl.Agents[1].Instructions; got != "Write the code." {
		t.Errorf("instructions without a text block = %q", got)
	}

	if len(tpl.Skills) != 2 || tpl.Skills[0].Name != "check" || tpl.Skills[1].Name != "build" {
		t.Fatalf("Skills = %+v, want check then build, each once", tpl.Skills)
	}
	var paths []string
	for _, f := range tpl.Skills[0].Files {
		paths = append(paths, f.Path)
	}
	if got, want := strings.Join(paths, ","), "scripts/__init__.py,scripts/run.sh"; got != want {
		t.Errorf("skill files = %s, want %s", got, want)
	}
}

func TestLoadRejectsBrokenCatalogs(t *testing.T) {
	cases := map[string]func(fstest.MapFS){
		"unknown skill": func(fsys fstest.MapFS) {
			delete(fsys, "src/skills/b/build/SKILL.md")
		},
		"missing agent document": func(fsys fstest.MapFS) {
			delete(fsys, "src/agents/dev.md")
		},
		"missing squad instructions": func(fsys fstest.MapFS) {
			delete(fsys, "src/squad/pair/squad.md")
		},
		"skill without a name": func(fsys fstest.MapFS) {
			fsys["src/skills/b/build/SKILL.md"] = &fstest.MapFile{Data: []byte("# Build\n")}
		},
		"duplicate skill name": func(fsys fstest.MapFS) {
			fsys["src/skills/c/again/SKILL.md"] = &fstest.MapFile{Data: []byte("---\nname: build\n---\n")}
		},
		"binary skill file": func(fsys fstest.MapFS) {
			fsys["src/skills/b/build/logo.png"] = &fstest.MapFile{Data: []byte{0x89, 0x50}}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			fsys := testCatalog()
			breakIt(fsys)
			if _, err := Load(fsys); err == nil {
				t.Fatal("Load succeeded on a broken catalog")
			}
		})
	}
}

func TestSkillDigest(t *testing.T) {
	files := []File{{Path: "a.sh", Content: "1"}, {Path: "b.sh", Content: "2"}}
	base := SkillDigest("desc", "body", files)

	if got := SkillDigest("desc", "body", []File{files[1], files[0]}); got != base {
		t.Error("digest depends on file order")
	}
	changed := map[string]string{
		"description": SkillDigest("other", "body", files),
		"content":     SkillDigest("desc", "other", files),
		"file body":   SkillDigest("desc", "body", []File{{Path: "a.sh", Content: "1"}, {Path: "b.sh", Content: "3"}}),
		"file set":    SkillDigest("desc", "body", files[:1]),
		// The same bytes split differently between two fields.
		"field boundary": SkillDigest("descb", "ody", files),
	}
	for what, got := range changed {
		if got == base {
			t.Errorf("digest ignores a change to the %s", what)
		}
	}
}
