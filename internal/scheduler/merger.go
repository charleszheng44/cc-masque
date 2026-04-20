package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// dispatchMerger runs the merger state machine for one open PR carrying
// claude-merge. It assumes the scheduler has already claimed the PR and
// added l.LockLabel; on exit it always removes l.LockLabel and either
// leaves claude-merge in place (for retry paths) or removes it (on merge
// success / terminal failure).
func (l *Lifecycle) dispatchMerger(ctx context.Context, log *slog.Logger, number int) {
	defer func() {
		_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
		// deleteLock=true: unlike the implementer claim — whose lock branch
		// (refs/heads/claude/issue-N) is the PR's head and must survive —
		// the merger lock ref (refs/cc-crew/merge-lock/pr-N) is disposable
		// state owned solely by the merger and should be cleared on every
		// exit path.
		_ = l.releaseWithRetry(ctx, claim.KindMerger, number, true)
	}()

	pr, err := l.GH.GetPR(ctx, l.Repo, number)
	if err != nil {
		log.Error("get PR failed", "err", err)
		return
	}

	switch pr.MergeStateStatus {
	case "CLEAN", "HAS_HOOKS":
		l.mergerAttemptMerge(ctx, log, &pr)
	case "BEHIND":
		l.mergerUpdateBranch(ctx, log, &pr)
	case "DIRTY":
		l.mergerHandoffResolver(ctx, log, &pr, "PR is DIRTY; dispatching resolver")
	case "UNSTABLE":
		l.mergerHandleUnstable(ctx, log, &pr)
	case "BLOCKED":
		l.mergerTerminal(ctx, log, &pr, "PR is BLOCKED by branch-protection rules; merger cannot proceed")
	case "DRAFT":
		l.mergerTerminal(ctx, log, &pr, "PR is still a draft; mark ready-for-review to merge")
	case "UNKNOWN", "":
		// Per spec (docs/superpowers/specs/2026-04-20-auto-merger-design.md §6, §9):
		// BLOCKED/DRAFT/UNKNOWN are terminal. Empty string falls through here
		// too — a malformed/missing mergeStateStatus escalates rather than
		// silently looping.
		l.mergerTerminal(ctx, log, &pr, fmt.Sprintf("mergeStateStatus is %q; merger cannot proceed", pr.MergeStateStatus))
	default:
		l.mergerTerminal(ctx, log, &pr, fmt.Sprintf("unknown mergeStateStatus: %s", pr.MergeStateStatus))
	}
}

// mergerAttemptMerge calls MergePR; on success clears claude-merge, on
// ErrMergeConflict hands off to resolver, on any other error marks terminal.
func (l *Lifecycle) mergerAttemptMerge(ctx context.Context, log *slog.Logger, pr *github.PullRequest) {
	err := l.GH.MergePR(ctx, l.Repo, pr.Number, pr.HeadRefOid, github.MergeMethodRebase, true)
	if err == nil {
		log.Info("merged", "pr", pr.Number)
		_ = l.GH.RemoveLabel(ctx, l.Repo, pr.Number, l.QueueLabel)
		return
	}
	if errors.Is(err, github.ErrMergeConflict) {
		l.mergerHandoffResolver(ctx, log, pr, "gh pr merge reported conflict; dispatching resolver")
		return
	}
	l.mergerTerminal(ctx, log, pr, fmt.Sprintf("merge failed: %v", err))
}

// mergerUpdateBranch calls the rebase update-branch endpoint. Success:
// leave claude-merge on for next-tick retry. Failure: terminal.
func (l *Lifecycle) mergerUpdateBranch(ctx context.Context, log *slog.Logger, pr *github.PullRequest) {
	if err := l.GH.UpdateBranch(ctx, l.Repo, pr.Number, pr.HeadRefOid, github.UpdateMethodRebase); err != nil {
		l.mergerTerminal(ctx, log, pr, fmt.Sprintf("update-branch failed: %v", err))
		return
	}
	log.Info("update-branch called; will retry merge next tick", "pr", pr.Number)
}

// mergerHandoffResolver adds the resolver queue label and leaves
// claude-merge on so the merger re-tries after the resolver succeeds.
// If ResolveConflictLabel is not configured we cannot hand off — the PR
// would loop forever with claude-merge on and no visible escalation —
// so fail loud by routing to the terminal branch.
func (l *Lifecycle) mergerHandoffResolver(ctx context.Context, log *slog.Logger, pr *github.PullRequest, reason string) {
	if l.ResolveConflictLabel == "" {
		l.mergerTerminal(ctx, log, pr, "resolver handoff requested but ResolveConflictLabel is not configured; "+reason)
		return
	}
	log.Info("handoff to resolver", "pr", pr.Number, "reason", reason)
	_ = l.GH.AddLabel(ctx, l.Repo, pr.Number, l.ResolveConflictLabel)
}

// mergerHandleUnstable inspects check runs: any hard-failed check → terminal;
// only pending → retry.
func (l *Lifecycle) mergerHandleUnstable(ctx context.Context, log *slog.Logger, pr *github.PullRequest) {
	runs, err := l.GH.GetCheckRuns(ctx, l.Repo, pr.HeadRefOid)
	if err != nil {
		log.Warn("get check runs failed; leaving claude-merge for retry", "err", err)
		return
	}
	var failed []string
	anyPending := false
	for _, cr := range runs {
		if cr.Status != "completed" {
			anyPending = true
			continue
		}
		switch cr.Conclusion {
		case "success", "neutral", "skipped":
		case "failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale":
			failed = append(failed, fmt.Sprintf("%s=%s", cr.Name, cr.Conclusion))
		default:
			// Conservative: treat unrecognized conclusions as pending so we
			// don't spuriously escalate. Warn so a new GitHub terminal
			// conclusion doesn't silently stall the merger.
			log.Warn("unrecognized check conclusion; treating as pending",
				"pr", pr.Number, "check", cr.Name, "conclusion", cr.Conclusion)
			anyPending = true
		}
	}
	if len(failed) > 0 {
		l.mergerTerminal(ctx, log, pr, fmt.Sprintf("required checks failed: %v", failed))
		return
	}
	if anyPending {
		log.Info("checks still pending; leaving claude-merge for retry", "pr", pr.Number)
		return
	}
	l.mergerAttemptMerge(ctx, log, pr)
}

// mergerTerminal posts a comment, applies claude-conflict-blocked, and
// removes claude-merge so nothing retries.
func (l *Lifecycle) mergerTerminal(ctx context.Context, log *slog.Logger, pr *github.PullRequest, reason string) {
	log.Warn("merger terminal", "pr", pr.Number, "reason", reason)
	body := fmt.Sprintf("cc-crew merger cannot proceed: %s\n\nLeaving this PR for human attention.", reason)
	if l.ConflictBlockedLabel != "" {
		body += fmt.Sprintf(" Remove `%s` after resolving to resume automation.", l.ConflictBlockedLabel)
	}
	if err := l.GH.CreateComment(ctx, l.Repo, pr.Number, body); err != nil {
		log.Warn("create terminal comment failed", "err", err)
	}
	if l.ConflictBlockedLabel != "" {
		_ = l.GH.AddLabel(ctx, l.Repo, pr.Number, l.ConflictBlockedLabel)
	}
	_ = l.GH.RemoveLabel(ctx, l.Repo, pr.Number, l.QueueLabel)
}
