package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, dir, manifest string) {
	t.Helper()
	p := filepath.Join(root, dir)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDir(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "invoice", `---
name: invoice
description: Generate PDF invoices.
version: "2"
---
# Invoice skill

Use template A. 中文正文也没问题。
`)
	writeSkill(t, root, "reporting", `---
description: Build weekly reports.
---
Body here.
`)
	// noise: a subdirectory without a manifest must be skipped
	if err := os.MkdirAll(filepath.Join(root, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("loaded %d skills", len(skills))
	}
	inv := skills[0]
	if inv.Name != "invoice" || inv.Description != "Generate PDF invoices." {
		t.Fatalf("invoice = %+v", inv)
	}
	if !strings.Contains(inv.Instructions, "中文正文也没问题") {
		t.Fatalf("instructions = %q", inv.Instructions)
	}
	if inv.Meta["version"] != "2" {
		t.Fatalf("meta = %v", inv.Meta)
	}
	// name falls back to the directory name
	if skills[1].Name != "reporting" {
		t.Fatalf("fallback name = %q", skills[1].Name)
	}
}

func TestLoadDirSingleSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, ".", "---\nname: solo\ndescription: One skill.\n---\nbody")
	skills, err := LoadDir(root)
	if err != nil || len(skills) != 1 || skills[0].Name != "solo" {
		t.Fatalf("skills = %v, err = %v", skills, err)
	}
}

func TestLoadRejectsBadManifests(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "broken", "---\nname: broken\ndescription: x\n") // unclosed
	if _, err := LoadDir(root); err == nil {
		t.Fatal("expected error for unclosed front matter")
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Skill{Name: "", Description: "d"}); err == nil {
		t.Fatal("empty name must fail")
	}
	if _, err := New(Skill{Name: "bad name", Description: "d"}); err == nil {
		t.Fatal("space in name must fail")
	}
	if _, err := New(Skill{Name: "ok", Description: " "}); err == nil {
		t.Fatal("blank description must fail")
	}
}

func TestReadFileConfinement(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "s", "---\nname: s\ndescription: d\n---\n")
	if err := os.WriteFile(filepath.Join(root, "s", "res.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, err := Load(filepath.Join(root, "s"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := sk.ReadFile("res.txt")
	if err != nil || string(b) != "hello" {
		t.Fatalf("read: %q %v", b, err)
	}
	if _, err := sk.ReadFile("../../etc/passwd"); err == nil {
		t.Fatal("path escape must fail")
	}

	ft := sk.FileTool()
	res, err := ft.Invoke(context.Background(), []byte(`{"path":"res.txt"}`))
	if err != nil || res.Content != "hello" {
		t.Fatalf("file tool: %+v %v", res, err)
	}
}

func TestAsTool(t *testing.T) {
	sk := MustNew(Skill{Name: "x", Description: "desc", Instructions: "long body"})
	tl := sk.AsTool()
	res, err := tl.Invoke(context.Background(), nil)
	if err != nil || res.Content != "long body" {
		t.Fatalf("%+v %v", res, err)
	}
}
