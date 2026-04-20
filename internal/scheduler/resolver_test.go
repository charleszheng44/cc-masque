package scheduler

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// fakeDockerRunner short-circuits docker.Run in resolver tests.
type fakeDockerRunner struct {
	exitCode int
	err      error
	called   bool
}

func (f *fakeDockerRunner) run(exitCode int, err error) func() (int, error) {
	f.exitCode = exitCode
	f.err = err
	return func() (int, error) {
		f.called = true
		return f.exitCode, f.err
	}
}

func TestResolverSuccessReQueuesReviewer(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[1] = &github.PullRequest{
		Number: 1, State: "open", HeadRefOid: "sha", HeadRefName: "claude/issue-1", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-resolve-conflict", "claude-resolving", "claude-reviewed"},
	}
	fake := &fakeDockerRunner{}
	lc := &Lifecycle{
		Kind: claim.KindResolver, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:                  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:           "claude-resolve-conflict",
		LockLabel:            "claude-resolving",
		ReviewLabel:          "claude-review",
		DoneLabel:            "claude-reviewed",
		ConflictBlockedLabel: "claude-conflict-blocked",
		MergeLabel:           "claude-merge",
	}
	lc.dockerRunFn = fake.run(0, nil)
	lc.dispatchResolver(context.Background(), slog.Default(), 1)
	if !fake.called {
		t.Fatal("docker run not invoked")
	}
	labels := f.PRs[1].Labels
	if containsLabel(labels, "claude-resolve-conflict") || containsLabel(labels, "claude-resolving") {
		t.Errorf("resolver labels not cleared: %v", labels)
	}
	if !containsLabel(labels, "claude-review") {
		t.Errorf("expected claude-review re-added for reviewer pickup: %v", labels)
	}
	if containsLabel(labels, "claude-reviewed") {
		t.Errorf("claude-reviewed should be removed to re-trigger reviewer: %v", labels)
	}
}

func TestResolverFailureAppliesConflictBlocked(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[2] = &github.PullRequest{
		Number: 2, State: "open", HeadRefOid: "sha", HeadRefName: "claude/issue-2", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-resolve-conflict", "claude-resolving"},
	}
	fake := &fakeDockerRunner{}
	lc := &Lifecycle{
		Kind: claim.KindResolver, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:                  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:           "claude-resolve-conflict",
		LockLabel:            "claude-resolving",
		ReviewLabel:          "claude-review",
		DoneLabel:            "claude-reviewed",
		ConflictBlockedLabel: "claude-conflict-blocked",
		MergeLabel:           "claude-merge",
	}
	lc.dockerRunFn = fake.run(1, nil)
	lc.dispatchResolver(context.Background(), slog.Default(), 2)
	labels := f.PRs[2].Labels
	if !containsLabel(labels, "claude-conflict-blocked") {
		t.Errorf("expected claude-conflict-blocked: %v", labels)
	}
	if containsLabel(labels, "claude-merge") {
		t.Errorf("claude-merge should be removed on terminal: %v", labels)
	}
	if containsLabel(labels, "claude-resolve-conflict") || containsLabel(labels, "claude-resolving") {
		t.Errorf("resolver queue/lock labels not cleared on failure: %v", labels)
	}
	if len(f.Comments[2]) == 0 {
		t.Error("expected escalation comment")
	}
}
