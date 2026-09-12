package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink inside the skill directory must not become a way out of it.
func TestReadFileRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("classified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("bundled"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Skill{Name: "s", Dir: root}

	if b, err := s.ReadFile("link.txt"); err == nil {
		t.Fatalf("symlink escape allowed, read %q", b)
	}
	if _, err := s.ReadFile("../" + filepath.Base(outside)); err == nil {
		t.Fatal("parent traversal allowed")
	}
	// Bundled files still readable — the root itself is often behind a symlink
	// (macOS /var), so resolving must not lock the skill out of its own dir.
	b, err := s.ReadFile("ok.txt")
	if err != nil || string(b) != "bundled" {
		t.Fatalf("ReadFile(ok.txt) = %q, %v", b, err)
	}
}
