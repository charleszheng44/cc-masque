package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func makeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	clone := filepath.Join(root, "clone")

	mustRun(t, root, "git", "init", "--bare", origin)

	seed := filepath.Join(root, "seed")
	mustRun(t, root, "git", "clone", origin, seed)
	mustRun(t, seed, "git", "config", "user.email", "t@t")
	mustRun(t, seed, "git", "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, seed, "git", "add", "-A")
	mustRun(t, seed, "git", "commit", "-m", "init")
	mustRun(t, seed, "git", "branch", "-M", "main")
	mustRun(t, seed, "git", "push", "-u", "origin", "main")

	mustRun(t, seed, "git", "checkout", "-b", "claude/issue-42")
	mustRun(t, seed, "git", "commit", "--allow-empty", "-m", "work")
	mustRun(t, seed, "git", "push", "-u", "origin", "claude/issue-42")

	mustRun(t, root, "git", "clone", origin, clone)
	mustRun(t, clone, "git", "fetch", "origin")
	return clone
}

func TestAddRemove(t *testing.T) {
	clone := makeRepo(t)
	m := New(clone)
	ctx := context.Background()

	p, err := m.Add(ctx, "claude/issue-42")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("worktree path not created: %v", err)
	}

	if err := m.Remove(ctx, "claude/issue-42"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("worktree path should be gone, stat err=%v", err)
	}
}
