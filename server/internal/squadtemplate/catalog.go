// Package squadtemplate holds the squad templates this server ships: ready-made
// squads with their agents, instructions and skills, which a workspace installs
// from the Squads page.
//
// catalog/<root>/ is a verbatim copy of the template tree named by
// manifest.json's "source", so a newer upstream revision replaces it wholesale.
// manifest.json is the only file written here: it names the templates and says
// which agents each one uses and which skills each agent carries.
package squadtemplate

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	skillpkg "github.com/multica-ai/multica/server/internal/skill"
)

// "all:" keeps files whose names start with "_" or ".", which a plain directory
// pattern drops: skills ship Python packages with an __init__.py.
//
//go:embed all:catalog
var embedded embed.FS

// LocalizedText is copy shown in the template picker.
type LocalizedText struct {
	EN string `json:"en"`
	ZH string `json:"zh"`
}

// File is one supporting file of a skill, addressed relative to its SKILL.md.
type File struct {
	Path    string
	Content string
}

// Skill is a skill as a workspace stores it.
type Skill struct {
	Name        string
	Description string
	Content     string
	Files       []File
	// Digest identifies this exact description, content and file set.
	Digest string
}

// Agent is one squad member.
type Agent struct {
	Name         string
	Role         string
	Instructions string
	Skills       []string
}

// Template is one installable squad.
type Template struct {
	Key               string
	Recommended       bool
	Title             LocalizedText
	Summary           LocalizedText
	SquadName         string
	SquadDescription  string
	SquadInstructions string
	// Leader is the name of the agent that leads the squad; it is one of Agents.
	Leader string
	Agents []Agent
	// Skills are the distinct skills the agents carry, in first-use order.
	Skills []Skill
}

// Catalog is every template the server ships.
type Catalog struct {
	Templates []Template
}

// Template returns the template with the given key.
func (c *Catalog) Template(key string) (Template, bool) {
	for _, t := range c.Templates {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}

// Default returns the catalog built into this server.
var Default = sync.OnceValues(func() (*Catalog, error) {
	sub, err := fs.Sub(embedded, "catalog")
	if err != nil {
		return nil, err
	}
	return Load(sub)
})

type manifest struct {
	Root      string `json:"root"`
	Templates []struct {
		Key         string        `json:"key"`
		Recommended bool          `json:"recommended"`
		Title       LocalizedText `json:"title"`
		Summary     LocalizedText `json:"summary"`
		Squad       struct {
			Name             string `json:"name"`
			Description      string `json:"description"`
			InstructionsFile string `json:"instructions_file"`
			Leader           string `json:"leader"`
		} `json:"squad"`
		Agents []string `json:"agents"`
	} `json:"templates"`
	Agents map[string]struct {
		Name   string   `json:"name"`
		Role   string   `json:"role"`
		Skills []string `json:"skills"`
	} `json:"agents"`
}

// Load reads a catalog from fsys, which holds manifest.json and the template
// tree it points at. It fails on anything an install could not apply, so a
// broken catalog is caught when it is built rather than when it is used.
func Load(fsys fs.FS) (*Catalog, error) {
	raw, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Root == "" {
		return nil, fmt.Errorf("manifest: root is required")
	}

	skills, err := loadSkills(fsys, path.Join(m.Root, "skills"))
	if err != nil {
		return nil, err
	}

	agents := make(map[string]Agent, len(m.Agents))
	for stem, spec := range m.Agents {
		if spec.Name == "" {
			return nil, fmt.Errorf("agent %q: name is required", stem)
		}
		instructions, err := readInstructions(fsys, path.Join(m.Root, "agents", stem+".md"))
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", stem, err)
		}
		for _, name := range spec.Skills {
			if _, ok := skills[name]; !ok {
				return nil, fmt.Errorf("agent %q: unknown skill %q", stem, name)
			}
		}
		agents[stem] = Agent{Name: spec.Name, Role: spec.Role, Instructions: instructions, Skills: spec.Skills}
	}

	catalog := &Catalog{}
	seenKeys := map[string]bool{}
	for _, spec := range m.Templates {
		if spec.Key == "" || seenKeys[spec.Key] {
			return nil, fmt.Errorf("template key %q is empty or repeated", spec.Key)
		}
		seenKeys[spec.Key] = true
		if spec.Squad.Name == "" {
			return nil, fmt.Errorf("template %q: squad name is required", spec.Key)
		}
		instructions, err := readInstructions(fsys, path.Join(m.Root, spec.Squad.InstructionsFile))
		if err != nil {
			return nil, fmt.Errorf("template %q: %w", spec.Key, err)
		}

		t := Template{
			Key:               spec.Key,
			Recommended:       spec.Recommended,
			Title:             spec.Title,
			Summary:           spec.Summary,
			SquadName:         spec.Squad.Name,
			SquadDescription:  spec.Squad.Description,
			SquadInstructions: instructions,
		}
		seenNames := map[string]bool{}
		seenSkills := map[string]bool{}
		for _, stem := range spec.Agents {
			agent, ok := agents[stem]
			if !ok {
				return nil, fmt.Errorf("template %q: unknown agent %q", spec.Key, stem)
			}
			if seenNames[agent.Name] {
				return nil, fmt.Errorf("template %q: agent name %q is repeated", spec.Key, agent.Name)
			}
			seenNames[agent.Name] = true
			if stem == spec.Squad.Leader {
				t.Leader = agent.Name
			}
			t.Agents = append(t.Agents, agent)
			for _, name := range agent.Skills {
				if !seenSkills[name] {
					seenSkills[name] = true
					t.Skills = append(t.Skills, skills[name])
				}
			}
		}
		if t.Leader == "" {
			return nil, fmt.Errorf("template %q: leader %q is not one of its agents", spec.Key, spec.Squad.Leader)
		}
		catalog.Templates = append(catalog.Templates, t)
	}
	if len(catalog.Templates) == 0 {
		return nil, fmt.Errorf("manifest: no templates")
	}
	return catalog, nil
}

// textBlock is the fenced block an agent or squad document keeps its
// instructions in; the prose around it is for the people maintaining it.
var textBlock = regexp.MustCompile("(?s)```text[ \t]*\r?\n(.*?)```")

// readInstructions returns the document's instructions block, or the whole
// document when it has none.
func readInstructions(fsys fs.FS, name string) (string, error) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", err
	}
	text := string(raw)
	if match := textBlock.FindStringSubmatch(text); match != nil {
		text = match[1]
	}
	text = strings.Trim(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s has no instructions", name)
	}
	return text, nil
}

// loadSkills indexes every skill under dir by the name in its SKILL.md.
func loadSkills(fsys fs.FS, dir string) (map[string]Skill, error) {
	skills := map[string]Skill{}
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		skill, err := loadSkill(fsys, path.Dir(p))
		if err != nil {
			return err
		}
		if _, dup := skills[skill.Name]; dup {
			return fmt.Errorf("skill %q is defined more than once", skill.Name)
		}
		skills[skill.Name] = skill
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load skills: %w", err)
	}
	return skills, nil
}

func loadSkill(fsys fs.FS, dir string) (Skill, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, err
	}
	skill := Skill{Content: string(raw)}
	skill.Name, skill.Description = skillpkg.ParseSkillFrontmatter(skill.Content)
	if skill.Name == "" {
		return Skill{}, fmt.Errorf("%s/SKILL.md has no name", dir)
	}

	err = fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if skillpkg.IsReservedContentPath(rel) || skippedSkillFile(rel) {
			return nil
		}
		content, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if !skillpkg.IsSafeFilePath(rel) || skillpkg.IsLikelyBinaryFilePath(rel) || !utf8.Valid(content) {
			return fmt.Errorf("%s cannot be stored as a skill file", p)
		}
		skill.Files = append(skill.Files, File{Path: rel, Content: string(content)})
		return nil
	})
	if err != nil {
		return Skill{}, err
	}
	skill.Digest = SkillDigest(skill.Description, skill.Content, skill.Files)
	return skill, nil
}

// skippedSkillFile reports whether a file in a skill's directory stays out of
// the installed skill: dotfiles (credential examples among them), caches and
// licence texts. It matches the upstream sync script, so an install here and a
// sync from the source repository produce the same skill.
func skippedSkillFile(rel string) bool {
	segments := strings.Split(rel, "/")
	for _, seg := range segments {
		if strings.HasPrefix(seg, ".") || seg == "__MACOSX" || seg == "__pycache__" {
			return true
		}
	}
	switch strings.ToLower(segments[len(segments)-1]) {
	case "license", "license.md", "license.txt":
		return true
	}
	return false
}

// SkillDigest identifies a skill's description, content and file set. Two
// skills with the same digest are the same to an agent using them.
func SkillDigest(description, content string, files []File) string {
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	h := sha256.New()
	field := func(s string) {
		// Length-prefixed, so no two different field sequences hash alike.
		fmt.Fprintf(h, "%d:", len(s))
		io.WriteString(h, s)
	}
	field(description)
	field(content)
	for _, f := range sorted {
		field(f.Path)
		field(f.Content)
	}
	return hex.EncodeToString(h.Sum(nil))
}
