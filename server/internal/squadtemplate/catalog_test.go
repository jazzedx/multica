package squadtemplate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// writeCatalog puts a catalog on disk the way an operator's directory holds it.
func writeCatalog(t *testing.T, files fstest.MapFS) string {
	t.Helper()
	dir := t.TempDir()
	for name, file := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, file.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFromEnvOffersNothingWithoutADirectory(t *testing.T) {
	t.Setenv(EnvDir, " ")
	catalog, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if len(catalog.Templates) != 0 {
		t.Fatalf("templates = %d, want none", len(catalog.Templates))
	}
}

func TestFromEnvReadsTheDirectoryOnEveryCall(t *testing.T) {
	dir := writeCatalog(t, testCatalog())
	t.Setenv(EnvDir, dir)

	catalog, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	tpl, ok := catalog.Template("pair")
	if !ok {
		t.Fatal("template pair is missing")
	}
	before := tpl.Skills[0].Digest

	// A dot file and a package marker are part of a skill on disk too.
	if len(tpl.Skills[0].Files) == 0 {
		t.Fatal("skill check lost its supporting files")
	}

	skill := filepath.Join(dir, "src", "skills", "a", "check", "SKILL.md")
	if err := os.WriteFile(skill, []byte("---\nname: check\ndescription: Verify the work twice\n---\n\n# Check\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv after an update: %v", err)
	}
	tpl, _ = catalog.Template("pair")
	if tpl.Skills[0].Description != "Verify the work twice" || tpl.Skills[0].Digest == before {
		t.Fatalf("updated skill was not picked up: %+v", tpl.Skills[0])
	}
}

func TestFromEnvReportsABrokenDirectory(t *testing.T) {
	t.Setenv(EnvDir, filepath.Join(t.TempDir(), "missing"))
	if _, err := FromEnv(); err == nil {
		t.Fatal("a directory without a manifest loaded")
	}
}

func TestLint(t *testing.T) {
	catalog, err := Load(testCatalog())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if problems := Lint(catalog); len(problems) != 0 {
		t.Fatalf("a sound catalog was reported: %v", problems)
	}

	tpl := &catalog.Templates[0]
	tpl.Summary.ZH = ""
	tpl.SquadInstructions = "Lead routes the work."
	tpl.Skills[0].Content = ""
	problems := strings.Join(Lint(catalog), "\n")
	for _, want := range []string{
		"pair: title and summary need both languages",
		"pair: squad instructions never mention @Dev",
		"pair: skill check lacks a description or content",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("missing %q in:\n%s", want, problems)
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
