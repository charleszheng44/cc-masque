package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

// Manager wraps `git worktree` operations against a specific repo dir.
type Manager struct {
	RepoDir string
	GitBin  string
}

func New(repoDir string) *Manager {
	return &Manager{RepoDir: repoDir, GitBin: "git"}
}

func (m *Manager) git(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, m.GitBin, args...)
	cmd.Dir = m.RepoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %v: %w\nstderr: %s", args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// Path returns the absolute host path of a worktree for a given branch.
// Example: Path("claude/issue-42") -> "<repoDir>/.claude-worktrees/issue-42"
func (m *Manager) Path(branch string) string {
	return filepath.Join(m.RepoDir, ".claude-worktrees", filepath.Base(branch))
}

// Add fetches origin/branch, force-syncs the local branch ref to match
// origin, and creates a worktree at Path(branch). Removing the worktree
// first (if any) releases any git lock on the local branch so the forced
// fetch can update it.
//
// Forcing the ref update matters: a prior dispatch that reset + redeployed
// the same issue number can leave a stale local `refs/heads/<branch>` on
// the host (reset only deletes the remote ref, not the host-local ref).
// A plain `git fetch origin <branch>` only updates FETCH_HEAD, not the
// local branch — so the worktree would end up checked out at the old SHA,
// and any new commits would layer on top of the stale history when pushed.
func (m *Manager) Add(ctx context.Context, branch string) (string, error) {
	p := m.Path(branch)
	// Release the local branch (if it's checked out in a stale worktree)
	// before force-updating its ref.
	_, _ = m.git(ctx, "worktree", "remove", "--force", p)
	// Force-sync the local branch to origin's current tip. The leading `+`
	// in the refspec forces the update even when it isn't fast-forward.
	refspec := fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branch, branch)
	if _, err := m.git(ctx, "fetch", "--force", "origin", refspec); err != nil {
		return "", err
	}
	if _, err := m.git(ctx, "worktree", "add", "--force", p, branch); err != nil {
		return "", err
	}
	return p, nil
}

// AddDetached creates a detached worktree at .claude-worktrees/<name> checked
// out at the given SHA.  If the SHA is not present locally, a bare
// `git fetch origin` is attempted first.  Idempotent: any existing worktree at
// the same path is removed before re-adding.
func (m *Manager) AddDetached(ctx context.Context, name, sha string) (string, error) {
	p := filepath.Join(m.RepoDir, ".claude-worktrees", name)
	// Ensure the object is available locally.
	if _, err := m.git(ctx, "cat-file", "-e", sha+"^{commit}"); err != nil {
		if _, ferr := m.git(ctx, "fetch", "origin"); ferr != nil {
			return "", fmt.Errorf("fetch origin: %w", ferr)
		}
	}
	_, _ = m.git(ctx, "worktree", "remove", "--force", p)
	if _, err := m.git(ctx, "worktree", "add", "--force", "--detach", p, sha); err != nil {
		return "", err
	}
	return p, nil
}

func (m *Manager) Remove(ctx context.Context, branch string) error {
	_, err := m.git(ctx, "worktree", "remove", "--force", m.Path(branch))
	return err
}

func (m *Manager) Prune(ctx context.Context) error {
	_, err := m.git(ctx, "worktree", "prune")
	return err
}

// List returns worktree paths under .claude-worktrees/.
func (m *Manager) List(ctx context.Context) ([]string, error) {
	out, err := m.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("worktree ")) {
			continue
		}
		p := string(bytes.TrimPrefix(line, []byte("worktree ")))
		if filepath.Base(filepath.Dir(p)) == ".claude-worktrees" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
