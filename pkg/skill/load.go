package skill

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestName is the file a skill directory must contain.
const ManifestName = "SKILL.md"

// LoadDir loads every skill under dir. Each immediate subdirectory containing
// a SKILL.md becomes one skill; if dir itself contains a SKILL.md, it is
// loaded as a single skill instead. Results are sorted by name; a directory
// with a malformed manifest fails the whole load, since a silently missing
// skill is the worst failure mode for something wired at startup.
func LoadDir(dir string) ([]*Skill, error) {
	if _, err := os.Stat(filepath.Join(dir, ManifestName)); err == nil {
		s, err := Load(dir)
		if err != nil {
			return nil, err
		}
		return []*Skill{s}, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skill: read dir %q: %w", dir, err)
	}
	var out []*Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(sub, ManifestName)); err != nil {
			continue // not a skill directory
		}
		s, err := Load(sub)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Load loads a single skill from a directory containing SKILL.md, or from a
// path to the manifest itself. The manifest starts with a front-matter block:
//
//	---
//	name: invoice
//	description: Generate PDF invoices that follow company policy.
//	---
//	(markdown body = Instructions)
//
// name falls back to the directory name when omitted; every other
// front-matter key is kept verbatim in Meta.
func Load(path string) (*Skill, error) {
	dir := path
	manifest := filepath.Join(path, ManifestName)
	if fi, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("skill: %w", err)
	} else if !fi.IsDir() {
		manifest = path
		dir = filepath.Dir(path)
	}

	raw, err := os.ReadFile(manifest)
	if err != nil {
		return nil, fmt.Errorf("skill: %w", err)
	}
	front, body, err := splitFrontMatter(string(raw))
	if err != nil {
		return nil, fmt.Errorf("skill: %s: %w", manifest, err)
	}

	s := Skill{
		Name:         front["name"],
		Description:  front["description"],
		Instructions: strings.TrimSpace(body),
		Dir:          dir,
		Meta:         front,
	}
	delete(s.Meta, "name")
	delete(s.Meta, "description")
	if len(s.Meta) == 0 {
		s.Meta = nil
	}
	if s.Name == "" {
		s.Name = filepath.Base(dir)
	}
	sk, err := New(s)
	if err != nil {
		return nil, fmt.Errorf("skill: %s: %w", manifest, err)
	}
	return sk, nil
}

// splitFrontMatter parses a leading "---" fenced block of "key: value" lines.
// It is deliberately a subset of YAML — flat string keys only, '#' comments
// and blank lines ignored, values optionally single- or double-quoted. Skills
// needing richer metadata should carry it in the body or in resource files.
func splitFrontMatter(doc string) (map[string]string, string, error) {
	sc := bufio.NewScanner(strings.NewReader(doc))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !sc.Scan() {
		return nil, "", fmt.Errorf("empty manifest")
	}
	first := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
	if first != "---" {
		// No front matter: whole document is the body.
		return map[string]string{}, doc, nil
	}

	front := map[string]string{}
	closed := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, "", fmt.Errorf("front matter line %q is not key: value", trimmed)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		front[key] = value
	}
	if !closed {
		return nil, "", fmt.Errorf("front matter not closed with ---")
	}

	var body strings.Builder
	for sc.Scan() {
		body.WriteString(sc.Text())
		body.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return nil, "", err
	}
	return front, body.String(), nil
}
