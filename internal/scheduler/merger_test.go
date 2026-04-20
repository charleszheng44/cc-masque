package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func newMergerLifecycle(f *github.FakeClient, repo github.Repo) *Lifecycle {
	return &Lifecycle{
		Kind: claim.KindMerger, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:                  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:           "claude-merge",
		LockLabel:            "claude-merging",
		ResolveConflictLabel: "claude-resolve-conflict",
		ConflictBlockedLabel: "claude-conflict-blocked",
	}
}

func TestMergerCleanMergesAndClearsLabels(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[1] = &github.PullRequest{
		Number: 1, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "CLEAN",
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 1)
	if f.PRs[1].State != "closed" || !f.PRs[1].Merged {
		t.Errorf("PR not merged: %+v", f.PRs[1])
	}
	if containsLabel(f.PRs[1].Labels, "claude-merging") || containsLabel(f.PRs[1].Labels, "claude-merge") {
		t.Errorf("queue/lock labels not cleared: %v", f.PRs[1].Labels)
	}
}

func TestMergerBehindCallsUpdateBranchAndReleases(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[2] = &github.PullRequest{
		Number: 2, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "BEHIND",
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 2)
	if !f.UpdateBranchCalled[2] {
		t.Error("UpdateBranch not called")
	}
	if !containsLabel(f.PRs[2].Labels, "claude-merge") {
		t.Errorf("claude-merge should stay for next-tick retry: %v", f.PRs[2].Labels)
	}
	if containsLabel(f.PRs[2].Labels, "claude-merging") {
		t.Errorf("claude-merging should be released: %v", f.PRs[2].Labels)
	}
}

func TestMergerDirtyDispatchesResolverAndReleases(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[3] = &github.PullRequest{
		Number: 3, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "DIRTY",
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 3)
	if !containsLabel(f.PRs[3].Labels, "claude-resolve-conflict") {
		t.Errorf("claude-resolve-conflict not added: %v", f.PRs[3].Labels)
	}
	if !containsLabel(f.PRs[3].Labels, "claude-merge") {
		t.Errorf("claude-merge should stay so merger retries after resolve: %v", f.PRs[3].Labels)
	}
	if containsLabel(f.PRs[3].Labels, "claude-merging") {
		t.Errorf("claude-merging should be released: %v", f.PRs[3].Labels)
	}
}

func TestMergerTerminalMergeStates(t *testing.T) {
	// Each state in this table must route to the terminal branch per spec:
	// BLOCKED/DRAFT/UNKNOWN (and empty) are terminal, as is any unrecognized
	// state value (default arm).
	cases := []struct {
		name  string
		state string
	}{
		{"blocked", "BLOCKED"},
		{"draft", "DRAFT"},
		{"unknown", "UNKNOWN"},
		{"empty", ""},
		{"bogus", "NOT_A_REAL_STATE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := github.NewFake()
			repo := github.Repo{Owner: "o", Name: "n"}
			f.PRs[4] = &github.PullRequest{
				Number: 4, State: "open", HeadRefOid: "sha", BaseRefName: "main",
				Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: tc.state,
			}
			lc := newMergerLifecycle(f, repo)
			lc.dispatchMerger(context.Background(), slog.Default(), 4)
			if !containsLabel(f.PRs[4].Labels, "claude-conflict-blocked") {
				t.Errorf("state %q: claude-conflict-blocked not added: %v", tc.state, f.PRs[4].Labels)
			}
			if containsLabel(f.PRs[4].Labels, "claude-merge") {
				t.Errorf("state %q: claude-merge should be removed on terminal: %v", tc.state, f.PRs[4].Labels)
			}
			if len(f.Comments[4]) == 0 {
				t.Errorf("state %q: expected escalation comment on PR", tc.state)
			}
		})
	}
}

// If ResolveConflictLabel is not configured, a DIRTY PR cannot hand off to
// a resolver — silently no-op'ing would strand the PR in a loop. Verify
// the merger fails loud by routing to terminal instead.
func TestMergerDirtyWithoutResolveLabelIsTerminal(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[9] = &github.PullRequest{
		Number: 9, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "DIRTY",
	}
	lc := newMergerLifecycle(f, repo)
	lc.ResolveConflictLabel = ""
	lc.dispatchMerger(context.Background(), slog.Default(), 9)
	if !containsLabel(f.PRs[9].Labels, "claude-conflict-blocked") {
		t.Errorf("expected terminal escalation when resolver label unconfigured: %v", f.PRs[9].Labels)
	}
	if containsLabel(f.PRs[9].Labels, "claude-merge") {
		t.Errorf("claude-merge should be removed on terminal: %v", f.PRs[9].Labels)
	}
	if len(f.Comments[9]) == 0 {
		t.Error("expected escalation comment on PR")
	}
}

func TestMergerUnstableWithFailingCheckIsTerminal(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[5] = &github.PullRequest{
		Number: 5, State: "open", HeadRefOid: "sha5", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "UNSTABLE",
	}
	f.CheckRuns["sha5"] = []github.CheckRun{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "lint", Status: "completed", Conclusion: "failure"},
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 5)
	if !containsLabel(f.PRs[5].Labels, "claude-conflict-blocked") {
		t.Errorf("claude-conflict-blocked not added: %v", f.PRs[5].Labels)
	}
}

func TestMergerUnstableWithPendingCheckIsRetry(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[6] = &github.PullRequest{
		Number: 6, State: "open", HeadRefOid: "sha6", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "UNSTABLE",
	}
	f.CheckRuns["sha6"] = []github.CheckRun{
		{Name: "build", Status: "in_progress", Conclusion: ""},
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 6)
	if containsLabel(f.PRs[6].Labels, "claude-conflict-blocked") {
		t.Errorf("should not be terminal for pending checks: %v", f.PRs[6].Labels)
	}
	if !containsLabel(f.PRs[6].Labels, "claude-merge") {
		t.Errorf("claude-merge should remain for retry: %v", f.PRs[6].Labels)
	}
	if containsLabel(f.PRs[6].Labels, "claude-merging") {
		t.Errorf("claude-merging should be released: %v", f.PRs[6].Labels)
	}
}

func TestMergerMergeReturnsConflictDispatchesResolver(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[7] = &github.PullRequest{
		Number: 7, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "CLEAN",
	}
	f.MergePRHook = func(n int) error { return github.ErrMergeConflict }
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 7)
	if !containsLabel(f.PRs[7].Labels, "claude-resolve-conflict") {
		t.Errorf("expected claude-resolve-conflict after race-condition conflict: %v", f.PRs[7].Labels)
	}
}

func TestMergerMergeReturnsOtherErrorIsTerminal(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[8] = &github.PullRequest{
		Number: 8, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "CLEAN",
	}
	f.MergePRHook = func(n int) error { return errors.New("permission denied") }
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 8)
	if !containsLabel(f.PRs[8].Labels, "claude-conflict-blocked") {
		t.Errorf("expected claude-conflict-blocked: %v", f.PRs[8].Labels)
	}
}
