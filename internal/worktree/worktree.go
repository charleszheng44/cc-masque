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

// Add fetches origin/branch and creates a worktree at Path(branch).
// If the worktree path already exists, it is removed first.
func (m *Manager) Add(ctx context.Context, branch string) (string, error) {
	if _, err := m.git(ctx, "fetch", "origin", branch); err != nil {
		return "", err
	}
	p := m.Path(branch)
	_, _ = m.git(ctx, "worktree", "remove", "--force", p)
	if _, err := m.git(ctx, "worktree", "add", "--force", p, branch); err != nil {
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
