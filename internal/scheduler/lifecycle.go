package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
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

	// Merger/resolver-aware reviewer fields. Only read when Kind == KindReviewer.
	MergeLabel   string
	AddressLabel string

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

	switch l.Kind {
	case claim.KindImplementer:
		l.dispatchImplementer(ctx, log, number)
	case claim.KindReviewer:
		l.dispatchReviewer(ctx, log, number)
	case claim.KindAddresser:
		l.dispatchAddresser(ctx, log, number)
	}
}

func (l *Lifecycle) dispatchImplementer(ctx context.Context, log *slog.Logger, number int) {
	branch := fmt.Sprintf("claude/issue-%d", number)
	wtPath, err := l.WT.Add(ctx, branch)
	if err != nil {
		log.Error("worktree add failed", "err", err)
		l.failCleanup(ctx, number)
		return
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

func (l *Lifecycle) dispatchReviewer(ctx context.Context, log *slog.Logger, number int) {
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
	wtPath, err := l.WT.AddDetached(ctx, fmt.Sprintf("review-%d", number), headSha)
	if err != nil {
		log.Error("worktree add detached failed", "err", err)
		l.failCleanup(ctx, number)
		return
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
		l.successCleanupReviewer(ctx, number, headSha)
	} else {
		log.Warn("task exited non-zero", "code", code)
		l.failCleanup(ctx, number)
	}
	l.removeWorktree(ctx, number)
}

func (l *Lifecycle) dispatchAddresser(ctx context.Context, log *slog.Logger, number int) {
	pr, err := l.GH.GetPR(ctx, l.Repo, number)
	if err != nil {
		log.Error("get PR failed", "err", err)
		l.failCleanup(ctx, number)
		return
	}
	if pr.HeadRefOid == "" {
		log.Error("PR head SHA is empty", "pr", number)
		l.failCleanup(ctx, number)
		return
	}
	issueNum, ok := issueNumFromBranch(pr.HeadRefName)
	if !ok {
		log.Error("head branch is not claude/issue-*", "branch", pr.HeadRefName)
		l.failCleanup(ctx, number)
		return
	}

	reviewIDs, err := l.snapshotUnaddressedReviews(ctx, number)
	if err != nil {
		log.Error("snapshot reviews failed", "err", err)
		l.failCleanup(ctx, number)
		return
	}
	if len(reviewIDs) == 0 {
		// Defensive: the detector should have skipped this PR, but a race
		// (e.g., a concurrent dispatch finishing its marker writes between
		// detector tick and our claim) can leave the label in place with
		// every review already addressed. Don't run a container that would
		// fail with CC_REVIEW_IDS=""; drop the label and release the lock.
		log.Warn("no unaddressed reviews at dispatch time; dropping address label without running container")
		// Release first: if it fails after retries, KEEP the labels so the
		// PR stays in a consistent "address in progress" state. Scheduler
		// won't re-claim (claude-addressing excludes it from candidates)
		// and detector won't re-label (claude-address triggers skip-guard)
		// — reclaim sweeper repairs on its next pass. Removing labels
		// first would strand the orphan lock with no visible PR state,
		// causing every subsequent tick to silently fail TryClaim.
		if err := l.releaseWithRetry(ctx, claim.KindAddresser, number, true); err != nil {
			log.Warn("guard: release failed; leaving labels so reclaim sweeper can repair state", "err", err)
			return
		}
		if err := l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel); err != nil {
			log.Warn("guard: remove addressing label failed", "err", err)
		}
		if err := l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel); err != nil {
			log.Warn("guard: remove address label failed", "err", err)
		}
		return
	}

	wtPath, err := l.WT.AddDetached(ctx, fmt.Sprintf("address-%d", number), pr.HeadRefOid)
	if err != nil {
		log.Error("worktree add detached failed", "err", err)
		l.failCleanup(ctx, number)
		return
	}

	spec := l.buildAddresserRunSpec(number, issueNum, reviewIDs, wtPath)
	runCtx, cancel := context.WithTimeout(ctx, l.TaskTimeout)
	defer cancel()

	code, err := l.Docker.Run(runCtx, spec)
	if err != nil {
		log.Warn("task timed out or cancelled", "err", err)
		l.failCleanup(ctx, number)
		l.removeAddresserWorktree(ctx, number)
		return
	}
	if code == 0 {
		l.successCleanupAddresser(ctx, number, reviewIDs)
	} else {
		log.Warn("task exited non-zero", "code", code)
		l.failCleanup(ctx, number)
	}
	l.removeAddresserWorktree(ctx, number)
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
	var prefix string
	if l.Kind == claim.KindImplementer {
		labels["cc-crew.issue"] = fmt.Sprint(number)
		env["CC_ISSUE_NUM"] = fmt.Sprint(number)
		env["CC_BASE_BRANCH"] = l.BaseBranch
		prefix = fmt.Sprintf("[issue-%d] ", number)
	} else {
		labels["cc-crew.pr"] = fmt.Sprint(number)
		env["CC_PR_NUM"] = fmt.Sprint(number)
		prefix = fmt.Sprintf("[pr-%d] ", number)
	}

	return docker.RunSpec{
		Image:  l.Image,
		Name:   name,
		Labels: labels,
		Env:    env,
		Stdout: NewPrefixedWriter(lockedStdout, prefix, number),
		Stderr: NewPrefixedWriter(lockedStderr, prefix, number),
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

func (l *Lifecycle) snapshotUnaddressedReviews(ctx context.Context, prNumber int) ([]int, error) {
	reviews, err := l.GH.ListReviews(ctx, l.Repo, prNumber)
	if err != nil {
		return nil, err
	}
	addressed, err := l.GH.ListMatchingRefs(ctx, l.Repo, fmt.Sprintf("cc-crew/addressed/pr-%d/", prNumber))
	if err != nil {
		return nil, err
	}
	seen := map[int]struct{}{}
	for _, ref := range addressed {
		parts := strings.Split(ref.Name, "/")
		if len(parts) == 0 {
			continue
		}
		if id, e := strconv.Atoi(parts[len(parts)-1]); e == nil {
			seen[id] = struct{}{}
		}
	}
	var out []int
	for _, r := range reviews {
		if r.State != "COMMENTED" && r.State != "CHANGES_REQUESTED" {
			continue
		}
		if _, ok := seen[r.ID]; ok {
			continue
		}
		out = append(out, r.ID)
	}
	return out, nil
}

func (l *Lifecycle) buildAddresserRunSpec(prNumber, issueNum int, reviewIDs []int, wtPath string) docker.RunSpec {
	name := fmt.Sprintf("cc-crew-addr-%s-%s-%d",
		safeName(l.Repo.Owner), safeName(l.Repo.Name), prNumber)

	ids := make([]string, len(reviewIDs))
	for i, id := range reviewIDs {
		ids[i] = fmt.Sprint(id)
	}

	labels := map[string]string{
		"cc-crew.repo": l.Repo.String(),
		"cc-crew.role": "implementer",
		"cc-crew.mode": "address",
		"cc-crew.pr":   fmt.Sprint(prNumber),
	}
	env := map[string]string{
		"CC_ROLE":                 "implementer",
		"CC_TASK_KIND":            "address",
		"CC_MODEL":                l.Model,
		"CC_REPO":                 l.Repo.String(),
		"CC_PR_NUM":               fmt.Sprint(prNumber),
		"CC_ISSUE_NUM":            fmt.Sprint(issueNum),
		"CC_REVIEW_IDS":           strings.Join(ids, ","),
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
	prPrefix := fmt.Sprintf("[pr-%d] ", prNumber)
	return docker.RunSpec{
		Image:  l.Image,
		Name:   name,
		Labels: labels,
		Env:    env,
		Stdout: NewPrefixedWriter(lockedStdout, prPrefix, prNumber),
		Stderr: NewPrefixedWriter(lockedStderr, prPrefix, prNumber),
		Mounts: []docker.Mount{
			{HostPath: wtPath, ContainerPath: "/workspace"},
			{
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

	// Implementer: keep the lock branch (PR references it).
	_ = l.releaseWithRetry(ctx, l.Kind, number, false)
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
}

func (l *Lifecycle) successCleanupReviewer(ctx context.Context, number int, headSha string) {
	// Order matters: write the rereviewed marker BEFORE flipping labels or
	// releasing the lock. The continuous detector's skip-guard checks for
	// claude-review/claude-reviewing on the PR; as long as one of those is
	// present, the detector won't re-queue. Removing them first opens a
	// window where the detector sees claude-reviewed present + no marker
	// for the current head SHA, and flips the PR back to claude-review.
	if headSha != "" {
		ref := fmt.Sprintf("refs/cc-crew/rereviewed/pr-%d/%s", number, headSha)
		_ = l.createRefWithRetry(ctx, ref, headSha)
	}

	verdict := l.latestReviewVerdict(ctx, number)

	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel)
	_ = l.GH.AddLabel(ctx, l.Repo, number, l.DoneLabel)

	switch verdict {
	case "APPROVED":
		if l.MergeLabel != "" {
			_ = l.GH.AddLabel(ctx, l.Repo, number, l.MergeLabel)
		}
		if l.AddressLabel != "" {
			_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.AddressLabel)
		}
	case "CHANGES_REQUESTED":
		if l.AddressLabel != "" {
			_ = l.GH.AddLabel(ctx, l.Repo, number, l.AddressLabel)
		}
		if l.MergeLabel != "" {
			_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.MergeLabel)
		}
	}

	_ = l.releaseWithRetry(ctx, l.Kind, number, true)
}

// latestReviewVerdict returns the state of the most recent non-COMMENTED
// review on the PR ("APPROVED" | "CHANGES_REQUESTED" | ""). Returns "" on
// any error or when only COMMENTED reviews exist.
func (l *Lifecycle) latestReviewVerdict(ctx context.Context, prNumber int) string {
	reviews, err := l.GH.ListReviews(ctx, l.Repo, prNumber)
	if err != nil {
		l.Log.Warn("list reviews for verdict failed", "pr", prNumber, "err", err)
		return ""
	}
	var latest *github.Review
	for i := range reviews {
		r := &reviews[i]
		if r.State != "APPROVED" && r.State != "CHANGES_REQUESTED" {
			continue
		}
		if latest == nil || r.At.After(latest.At) {
			latest = r
		}
	}
	if latest == nil {
		return ""
	}
	return latest.State
}

func (l *Lifecycle) successCleanupAddresser(ctx context.Context, prNumber int, reviewIDs []int) {
	// Order matters: write markers BEFORE flipping labels or releasing the
	// lock. The detector's skip-guard holds while claude-address /
	// claude-addressing are still on the PR. If we remove them before the
	// markers are written, the detector sees the same reviews as
	// unaddressed and re-queues the PR — and by the time the next dispatch
	// snapshots, the markers HAVE been written, so snapshot returns empty
	// and the container fails with CC_REVIEW_IDS="" in a loop.
	markerSha := ""
	if pr, err := l.GH.GetPR(ctx, l.Repo, prNumber); err == nil {
		markerSha = pr.HeadRefOid
	}
	for _, id := range reviewIDs {
		ref := fmt.Sprintf("refs/cc-crew/addressed/pr-%d/%d", prNumber, id)
		_ = l.createRefWithRetry(ctx, ref, markerSha)
	}
	_ = l.GH.RemoveLabel(ctx, l.Repo, prNumber, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, prNumber, l.QueueLabel)
	_ = l.GH.AddLabel(ctx, l.Repo, prNumber, l.DoneLabel)
	// Release address-lock + address-claim ts tags last.
	_ = l.releaseWithRetry(ctx, claim.KindAddresser, prNumber, true)
}

func (l *Lifecycle) failCleanup(ctx context.Context, number int) {
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.releaseWithRetry(ctx, l.Kind, number, true)
}

func (l *Lifecycle) removeWorktree(ctx context.Context, number int) {
	branch := fmt.Sprintf("claude/issue-%d", number)
	if l.Kind == claim.KindReviewer {
		branch = fmt.Sprintf("review-%d", number)
	}
	_ = l.WT.Remove(ctx, branch)
}

func (l *Lifecycle) removeAddresserWorktree(ctx context.Context, prNumber int) {
	_ = l.WT.Remove(ctx, fmt.Sprintf("address-%d", prNumber))
}

func issueNumFromBranch(branch string) (int, bool) {
	const pfx = "claude/issue-"
	if !strings.HasPrefix(branch, pfx) {
		return 0, false
	}
	n, err := strconv.Atoi(branch[len(pfx):])
	if err != nil {
		return 0, false
	}
	return n, true
}

func kindName(k claim.Kind) string {
	switch k {
	case claim.KindImplementer:
		return "implementer"
	case claim.KindAddresser:
		return "addresser"
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

// retryBackoff is the base delay between attempts. Exponentiates: 500ms, 1s, 2s.
// Package-level vars so tests can shrink them.
var (
	retryAttempts = 3
	retryBackoff  = 500 * time.Millisecond
)

// releaseWithRetry wraps Claimer.Release with exponential backoff. Returns the
// last error after all attempts are exhausted. Callers that must NOT proceed
// with subsequent cleanup on failure (e.g. the empty-snapshot guard) should
// check the returned error and keep the PR's address/review labels intact so
// the reclaim sweeper can repair state on its next pass.
func (l *Lifecycle) releaseWithRetry(ctx context.Context, k claim.Kind, number int, deleteLock bool) error {
	backoff := retryBackoff
	var err error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		err = l.Claimer.Release(ctx, k, number, deleteLock)
		if err == nil {
			return nil
		}
		l.Log.Warn("release failed; will retry",
			"kind", kindName(k), "number", number, "attempt", attempt, "err", err)
		if attempt < retryAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	l.Log.Error("release failed after retries; reclaim sweeper will need to clean up",
		"kind", kindName(k), "number", number, "err", err)
	return err
}

// createRefWithRetry wraps GH.CreateRef with exponential backoff for markers.
// An "already exists" result counts as success (idempotent).
func (l *Lifecycle) createRefWithRetry(ctx context.Context, ref, sha string) error {
	backoff := retryBackoff
	var err error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		err = l.GH.CreateRef(ctx, l.Repo, ref, sha)
		if err == nil || errors.Is(err, github.ErrRefExists) {
			return nil
		}
		l.Log.Warn("create ref failed; will retry",
			"ref", ref, "attempt", attempt, "err", err)
		if attempt < retryAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	l.Log.Error("create ref failed after retries", "ref", ref, "err", err)
	return err
}
