package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestAddDetached(t *testing.T) {
	clone := makeRepo(t)
	m := New(clone)
	ctx := context.Background()

	// Get the SHA of claude/issue-42 tip from origin.
	cmd := exec.Command("git", "rev-parse", "origin/claude/issue-42")
	cmd.Dir = clone
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	sha := strings.TrimSpace(string(out))

	p, err := m.AddDetached(ctx, "review-42", sha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("worktree path not created: %v", err)
	}
	// HEAD should be detached at the given SHA.
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = p
	headOut, err := headCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if got := strings.TrimSpace(string(headOut)); got != sha {
		t.Fatalf("HEAD = %s, want %s", got, sha)
	}
}

// TestAddForceSyncsLocalBranchToOrigin reproduces the scenario where a
// previous dispatch's local branch is stale compared to origin (e.g. the
// remote ref was reset back to main after orchestrator reset). Manager.Add
// must force-sync local refs/heads/<branch> from origin, or the subsequent
// worktree would check out the stale local state and any new commits
// would layer on top of it when pushed.
func TestAddForceSyncsLocalBranchToOrigin(t *testing.T) {
	clone := makeRepo(t)
	m := New(clone)
	ctx := context.Background()

	// 1. Initial Add — local branch gets populated at whatever origin had.
	if _, err := m.Add(ctx, "claude/issue-42"); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	// Remove the worktree so the local branch ref is free for a forced update.
	if err := m.Remove(ctx, "claude/issue-42"); err != nil {
		t.Fatal(err)
	}

	// 2. Simulate "reset then recreate" — reset origin/claude/issue-42 to
	//    main's SHA (discarding the old `work` commit). In real cc-crew
	//    this is what `reset` + a new TryClaim does.
	origin := filepath.Join(filepath.Dir(clone), "origin.git")
	mainSHA := func() string {
		cmd := exec.Command("git", "rev-parse", "main")
		cmd.Dir = origin
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("rev-parse main: %v", err)
		}
		return strings.TrimSpace(string(out))
	}()
	mustRun(t, origin, "git", "update-ref", "refs/heads/claude/issue-42", mainSHA)

	// 3. Second Add — should force-sync local to origin (= mainSHA).
	p, err := m.Add(ctx, "claude/issue-42")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}

	// 4. HEAD of the worktree should now be mainSHA, not the stale `work` commit.
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = p
	out, err := headCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD in worktree: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != mainSHA {
		t.Fatalf("worktree HEAD = %s, want mainSHA %s (stale local branch not force-synced)", got, mainSHA)
	}
}

// TestAddStaleDirectory verifies that Add succeeds when a plain directory
// already exists at the worktree path but git has no record of it.
func TestAddStaleDirectory(t *testing.T) {
	clone := makeRepo(t)
	m := New(clone)
	ctx := context.Background()

	p := m.Path("claude/issue-42")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := m.Add(ctx, "claude/issue-42")
	if err != nil {
		t.Fatalf("Add with stale directory: %v", err)
	}
	if got != p {
		t.Fatalf("returned path = %q, want %q", got, p)
	}
	headCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	headCmd.Dir = p
	out, err := headCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if branch := strings.TrimSpace(string(out)); branch != "claude/issue-42" {
		t.Fatalf("HEAD branch = %q, want claude/issue-42", branch)
	}
}

// TestAddStaleRegisteredWorktree verifies that Add succeeds when git has a
// registered worktree entry for the path but the directory was deleted.
func TestAddStaleRegisteredWorktree(t *testing.T) {
	clone := makeRepo(t)
	m := New(clone)
	ctx := context.Background()

	p, err := m.Add(ctx, "claude/issue-42")
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}
	// Delete the directory but leave the git worktree admin entry stale.
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	if _, err := m.Add(ctx, "claude/issue-42"); err != nil {
		t.Fatalf("Add with stale registered worktree: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("worktree path missing after Add: %v", err)
	}
}

// TestCleanWorktreePathGuard verifies that cleanWorktreePath refuses to
// operate on any path outside <RepoDir>/.claude-worktrees/.
func TestCleanWorktreePathGuard(t *testing.T) {
	clone := makeRepo(t)
	m := New(clone)
	ctx := context.Background()

	outside := filepath.Join(clone, "not-worktrees", "escape")
	if err := m.cleanWorktreePath(ctx, outside); err == nil {
		t.Fatal("expected error for path outside allowed tree, got nil")
	}
	// Also guard against traversal attempts.
	traversal := filepath.Join(m.RepoDir, ".claude-worktrees", "..", "escape")
	if err := m.cleanWorktreePath(ctx, traversal); err == nil {
		t.Fatal("expected error for traversal path, got nil")
	}
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
