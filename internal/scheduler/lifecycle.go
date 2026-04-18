package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

// Lifecycle is a Dispatcher that runs the full per-task flow:
// fetch worktree → docker run → clean up labels.
type Lifecycle struct {
	Kind    claim.Kind
	Claimer *claim.Claimer
	GH      github.Client
	Repo    github.Repo
	WT      *worktree.Manager
	Docker  *docker.Runner
	Log     *slog.Logger

	QueueLabel  string
	LockLabel   string
	DoneLabel   string
	ReviewLabel string

	Image       string
	Model       string
	MaxTurns    int
	TaskTimeout time.Duration
	AutoReview  bool

	RoleGHToken     string
	ClaudeOAuth     string
	AnthropicAPIKey string
	GitName         string
	GitEmail        string

	BaseBranch string
}

// Dispatch implements scheduler.Dispatcher.
func (l *Lifecycle) Dispatch(ctx context.Context, number int) {
	log := l.Log.With("kind", kindName(l.Kind), "number", number)
	log.Info("dispatch start")

	branch := fmt.Sprintf("claude/issue-%d", number)
	var wtPath string
	if l.Kind == claim.KindImplementer {
		p, err := l.WT.Add(ctx, branch)
		if err != nil {
			log.Error("worktree add failed", "err", err)
			l.failCleanup(ctx, number)
			return
		}
		wtPath = p
	} else {
		pr, err := l.GH.GetPR(ctx, l.Repo, number)
		if err != nil {
			log.Error("get PR failed", "err", err)
			l.failCleanup(ctx, number)
			return
		}
		headSha := pr.HeadRefOid
		if headSha == "" {
			log.Error("PR head SHA is empty", "pr", number)
			l.failCleanup(ctx, number)
			return
		}
		p, err := l.WT.AddDetached(ctx, fmt.Sprintf("review-%d", number), headSha)
		if err != nil {
			log.Error("worktree add detached failed", "err", err)
			l.failCleanup(ctx, number)
			return
		}
		wtPath = p
	}

	spec := l.buildRunSpec(number, wtPath)
	runCtx, cancel := context.WithTimeout(ctx, l.TaskTimeout)
	defer cancel()

	code, err := l.Docker.Run(runCtx, spec)
	if err != nil {
		log.Warn("task timed out or cancelled", "err", err)
		l.failCleanup(ctx, number)
		l.removeWorktree(ctx, number)
		return
	}
	if code == 0 {
		l.successCleanup(ctx, number)
	} else {
		log.Warn("task exited non-zero", "code", code)
		l.failCleanup(ctx, number)
	}
	l.removeWorktree(ctx, number)
}

func (l *Lifecycle) buildRunSpec(number int, wtPath string) docker.RunSpec {
	name := fmt.Sprintf("cc-crew-%s-%s-%s-%d",
		roleShort(l.Kind),
		safeName(l.Repo.Owner), safeName(l.Repo.Name), number)

	labels := map[string]string{
		"cc-crew.repo": l.Repo.String(),
		"cc-crew.role": kindName(l.Kind),
	}
	env := map[string]string{
		"CC_ROLE":                 kindName(l.Kind),
		"CC_MODEL":                l.Model,
		"CC_REPO":                 l.Repo.String(),
		"GH_TOKEN":                l.RoleGHToken,
		"CLAUDE_CODE_OAUTH_TOKEN": l.ClaudeOAuth,
		"ANTHROPIC_API_KEY":       l.AnthropicAPIKey,
		"GIT_AUTHOR_NAME":         l.GitName,
		"GIT_AUTHOR_EMAIL":        l.GitEmail,
		"GIT_COMMITTER_NAME":      l.GitName,
		"GIT_COMMITTER_EMAIL":     l.GitEmail,
		// Containers are ephemeral (--rm) with a bind-mounted worktree; this
		// lets `claude --dangerously-skip-permissions` run as root inside.
		"IS_SANDBOX": "1",
	}
	// Only cap turns when the operator asked for a cap; absent = unlimited.
	if l.MaxTurns > 0 {
		env["CC_MAX_TURNS"] = fmt.Sprint(l.MaxTurns)
	}
	if l.Kind == claim.KindImplementer {
		labels["cc-crew.issue"] = fmt.Sprint(number)
		env["CC_ISSUE_NUM"] = fmt.Sprint(number)
		env["CC_BASE_BRANCH"] = l.BaseBranch
	} else {
		labels["cc-crew.pr"] = fmt.Sprint(number)
		env["CC_PR_NUM"] = fmt.Sprint(number)
	}

	return docker.RunSpec{
		Image:  l.Image,
		Name:   name,
		Labels: labels,
		Env:    env,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Mounts: []docker.Mount{
			{HostPath: wtPath, ContainerPath: "/workspace"},
			{
				// Mount .git read-write so that commit operations inside the
				// worktree can write to the shared .git/objects and per-worktree
				// admin files (COMMIT_EDITMSG, index, etc.).
				HostPath:      filepath.Join(l.WT.RepoDir, ".git"),
				ContainerPath: filepath.Join(l.WT.RepoDir, ".git"),
			},
		},
	}
}

func (l *Lifecycle) successCleanup(ctx context.Context, number int) {
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel)
	_ = l.GH.AddLabel(ctx, l.Repo, number, l.DoneLabel)

	if l.Kind == claim.KindImplementer {
		// Do not delete the lock branch — PR references it.
		_ = l.Claimer.Release(ctx, l.Kind, number, false)
		if l.AutoReview {
			branch := fmt.Sprintf("claude/issue-%d", number)
			prs, err := l.GH.ListPRs(ctx, l.Repo, nil, nil)
			if err == nil {
				for _, p := range prs {
					if p.HeadRefName == branch {
						_ = l.GH.AddLabel(ctx, l.Repo, p.Number, l.ReviewLabel)
						break
					}
				}
			}
		}
	} else {
		// Reviewer: drop lock tag + all timestamp tags.
		_ = l.Claimer.Release(ctx, l.Kind, number, true)
	}
}

func (l *Lifecycle) failCleanup(ctx context.Context, number int) {
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.Claimer.Release(ctx, l.Kind, number, true)
}

func (l *Lifecycle) removeWorktree(ctx context.Context, number int) {
	branch := fmt.Sprintf("claude/issue-%d", number)
	if l.Kind == claim.KindReviewer {
		branch = fmt.Sprintf("review-%d", number)
	}
	_ = l.WT.Remove(ctx, branch)
}

func kindName(k claim.Kind) string {
	if k == claim.KindImplementer {
		return "implementer"
	}
	return "reviewer"
}

func roleShort(k claim.Kind) string {
	if k == claim.KindImplementer {
		return "impl"
	}
	return "rev"
}

func safeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
