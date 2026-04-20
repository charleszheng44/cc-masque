package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/docker"
)

// dispatchResolver dispatches a container on the implementer image with
// CC_ROLE=resolver. On exit 0: clear resolver labels, re-queue the reviewer
// (remove DoneLabel, add ReviewLabel) because the resolver force-pushes and
// any prior approval may be dismissed by branch protection. On container
// non-zero exit or timeout (resolverFailCleanup): apply ConflictBlockedLabel,
// remove MergeLabel, and post an escalation comment so a human can take over.
// Pre-container errors (GitHub API blips, worktree setup failures) use the
// generic failCleanup path — release the claim, drop the lock label, and
// leave claude-resolve-conflict in place so the next tick retries. The
// terminal labels are reserved for failures where the container actually
// ran and could not rebase, per spec §9.
func (l *Lifecycle) dispatchResolver(ctx context.Context, log *slog.Logger, number int) {
	pr, err := l.GH.GetPR(ctx, l.Repo, number)
	if err != nil {
		log.Error("get PR failed", "err", err)
		l.failCleanup(ctx, number)
		return
	}
	if pr.HeadRefOid == "" {
		log.Error("PR head SHA empty", "pr", number)
		l.failCleanup(ctx, number)
		return
	}

	// Test seam short-circuits worktree + docker so unit tests can assert
	// label transitions without a real git repo or docker daemon.
	if l.resolverDockerRunFn != nil {
		code, runErr := l.resolverDockerRunFn()
		if runErr != nil || code != 0 {
			log.Warn("resolver exited non-zero", "code", code, "err", runErr)
			l.resolverFailCleanup(ctx, log, number)
			return
		}
		l.resolverSuccessCleanup(ctx, log, number)
		return
	}

	wtPath, err := l.WT.AddDetached(ctx, fmt.Sprintf("resolve-%d", number), pr.HeadRefOid)
	if err != nil {
		log.Error("worktree add detached failed", "err", err)
		l.failCleanup(ctx, number)
		return
	}
	defer func() { _ = l.WT.Remove(ctx, fmt.Sprintf("resolve-%d", number)) }()

	spec := l.buildResolverRunSpec(number, pr.BaseRefName, pr.HeadRefName, wtPath)
	runCtx, cancel := context.WithTimeout(ctx, l.TaskTimeout)
	defer cancel()

	code, err := l.Docker.Run(runCtx, spec)
	if err != nil || code != 0 {
		log.Warn("resolver exited non-zero", "code", code, "err", err)
		l.resolverFailCleanup(ctx, log, number)
		return
	}
	l.resolverSuccessCleanup(ctx, log, number)
}

func (l *Lifecycle) buildResolverRunSpec(prNumber int, baseBranch, headBranch, wtPath string) docker.RunSpec {
	name := fmt.Sprintf("cc-crew-resolve-%s-%s-%d",
		safeName(l.Repo.Owner), safeName(l.Repo.Name), prNumber)
	labels := map[string]string{
		"cc-crew.repo": l.Repo.String(),
		"cc-crew.role": "resolver",
		"cc-crew.pr":   fmt.Sprint(prNumber),
	}
	env := map[string]string{
		"CC_ROLE":                 "resolver",
		"CC_MODEL":                l.Model,
		"CC_REPO":                 l.Repo.String(),
		"CC_PR_NUM":               fmt.Sprint(prNumber),
		"CC_BASE_BRANCH":          baseBranch,
		"CC_HEAD_BRANCH":          headBranch,
		"GH_TOKEN":                l.RoleGHToken,
		"CLAUDE_CODE_OAUTH_TOKEN": l.ClaudeOAuth,
		"ANTHROPIC_API_KEY":       l.AnthropicAPIKey,
		"GIT_AUTHOR_NAME":         l.GitName,
		"GIT_AUTHOR_EMAIL":        l.GitEmail,
		"GIT_COMMITTER_NAME":      l.GitName,
		"GIT_COMMITTER_EMAIL":     l.GitEmail,
		"IS_SANDBOX":              "1",
	}
	if l.MaxTurns > 0 {
		env["CC_MAX_TURNS"] = fmt.Sprint(l.MaxTurns)
	}
	prefix := fmt.Sprintf("[pr-%d] ", prNumber)
	return docker.RunSpec{
		Image:  l.Image,
		Name:   name,
		Labels: labels,
		Env:    env,
		Stdout: NewPrefixedWriter(lockedStdout, prefix, prNumber),
		Stderr: NewPrefixedWriter(lockedStderr, prefix, prNumber),
		Mounts: []docker.Mount{
			{HostPath: wtPath, ContainerPath: "/workspace"},
			{
				HostPath:      filepath.Join(l.WT.RepoDir, ".git"),
				ContainerPath: filepath.Join(l.WT.RepoDir, ".git"),
			},
		},
	}
}

func (l *Lifecycle) resolverSuccessCleanup(ctx context.Context, log *slog.Logger, number int) {
	log.Info("resolver succeeded", "pr", number)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel)
	// Force-push invalidates any prior approval under branch protection's
	// "dismiss stale reviews" rule; flip labels back so the reviewer
	// scheduler re-queues this PR for a fresh pass. Add ReviewLabel BEFORE
	// removing DoneLabel so the PR is never momentarily absent both labels
	// (which would otherwise let the merger detector observe claude-merge
	// alone).
	if l.ReviewLabel != "" {
		_ = l.GH.AddLabel(ctx, l.Repo, number, l.ReviewLabel)
	}
	if l.DoneLabel != "" {
		_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.DoneLabel)
	}
	_ = l.releaseWithRetry(ctx, claim.KindResolver, number, true)
}

func (l *Lifecycle) resolverFailCleanup(ctx context.Context, log *slog.Logger, number int) {
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel)
	if l.ConflictBlockedLabel != "" {
		_ = l.GH.AddLabel(ctx, l.Repo, number, l.ConflictBlockedLabel)
	}
	if l.MergeLabel != "" {
		_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.MergeLabel)
	}
	body := fmt.Sprintf("🚫 cc-crew resolver could not rebase this PR. Resolve the conflicts manually and remove `%s` to resume automation.",
		l.ConflictBlockedLabel)
	if err := l.GH.CreateComment(ctx, l.Repo, number, body); err != nil {
		log.Warn("create terminal comment failed", "err", err)
	}
	_ = l.releaseWithRetry(ctx, claim.KindResolver, number, true)
}
