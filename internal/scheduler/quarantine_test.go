package scheduler

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func TestQuarantineRecordFailureIncrementsAndFiresOnce(t *testing.T) {
	q := NewQuarantine(3)

	c1, label1 := q.RecordFailure(claim.KindImplementer, 1, "err a")
	if c1 != 1 || label1 {
		t.Fatalf("first fail: count=%d label=%v, want 1 false", c1, label1)
	}
	c2, label2 := q.RecordFailure(claim.KindImplementer, 1, "err b")
	if c2 != 2 || label2 {
		t.Fatalf("second fail: count=%d label=%v, want 2 false", c2, label2)
	}
	c3, label3 := q.RecordFailure(claim.KindImplementer, 1, "err c")
	if c3 != 3 || !label3 {
		t.Fatalf("third fail: count=%d label=%v, want 3 true", c3, label3)
	}
	// Subsequent failures past threshold must NOT re-fire the label signal.
	c4, label4 := q.RecordFailure(claim.KindImplementer, 1, "err d")
	if c4 != 4 || label4 {
		t.Fatalf("fourth fail: count=%d label=%v, want 4 false", c4, label4)
	}
	if got := q.LastErr(claim.KindImplementer, 1); got != "err d" {
		t.Fatalf("LastErr = %q, want err d", got)
	}
}

func TestQuarantineRecordSuccessResets(t *testing.T) {
	q := NewQuarantine(3)
	q.RecordFailure(claim.KindImplementer, 7, "boom")
	q.RecordFailure(claim.KindImplementer, 7, "boom2")
	if q.Count(claim.KindImplementer, 7) != 2 {
		t.Fatalf("count=%d, want 2", q.Count(claim.KindImplementer, 7))
	}
	q.RecordSuccess(claim.KindImplementer, 7)
	if q.Count(claim.KindImplementer, 7) != 0 {
		t.Fatalf("success should reset count; got %d", q.Count(claim.KindImplementer, 7))
	}
	// After reset, hitting the threshold again must re-fire labeling.
	q.RecordFailure(claim.KindImplementer, 7, "x")
	q.RecordFailure(claim.KindImplementer, 7, "x")
	_, label := q.RecordFailure(claim.KindImplementer, 7, "x")
	if !label {
		t.Fatal("reset + 3 fails should re-fire label signal")
	}
}

func TestQuarantineZeroThresholdDisabled(t *testing.T) {
	q := NewQuarantine(0)
	for i := 0; i < 10; i++ {
		if _, label := q.RecordFailure(claim.KindReviewer, 1, "x"); label {
			t.Fatalf("zero threshold must never fire (iter %d)", i)
		}
	}
}

func TestQuarantineKeysIsolatedByKind(t *testing.T) {
	q := NewQuarantine(2)
	// Same number, different kinds: each gets its own counter.
	q.RecordFailure(claim.KindImplementer, 5, "a")
	q.RecordFailure(claim.KindReviewer, 5, "b")
	if c := q.Count(claim.KindImplementer, 5); c != 1 {
		t.Fatalf("implementer count = %d, want 1", c)
	}
	if c := q.Count(claim.KindReviewer, 5); c != 1 {
		t.Fatalf("reviewer count = %d, want 1", c)
	}
}

// TestFailCleanupAppliesQuarantineAtThreshold exercises the integration
// between failCleanup and the Quarantine tracker: three failures must leave
// the issue with the quarantine label and a comment summarising the last
// failure, and subsequent failures must NOT re-post.
func TestFailCleanupAppliesQuarantineAtThreshold(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task"}}

	q := NewQuarantine(3)
	l := &Lifecycle{
		Kind: claim.KindImplementer, Claimer: claim.New(f, r), GH: f, Repo: r,
		Log:             slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:      "claude-task",
		LockLabel:       "claude-processing",
		DoneLabel:       "claude-done",
		QuarantineLabel: "claude-failed",
		Quarantine:      q,
	}

	// Two failures: counter below threshold, no label yet.
	l.failCleanup(context.Background(), 42, "first")
	l.failCleanup(context.Background(), 42, "second")
	if contains(f.Issues[42].Labels, "claude-failed") {
		t.Fatal("quarantine label applied before threshold")
	}
	if len(f.Comments[42]) != 0 {
		t.Fatalf("comment posted before threshold: %v", f.Comments[42])
	}

	// Third failure: threshold hit, expect label + one comment with last error.
	l.failCleanup(context.Background(), 42, "third: worktree add failed: boom")
	if !contains(f.Issues[42].Labels, "claude-failed") {
		t.Fatalf("quarantine label not applied: %v", f.Issues[42].Labels)
	}
	if len(f.Comments[42]) != 1 {
		t.Fatalf("want 1 quarantine comment, got %d: %v", len(f.Comments[42]), f.Comments[42])
	}
	body := f.Comments[42][0]
	for _, want := range []string{"quarantined", "worktree add failed: boom", "claude-failed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment missing %q; body:\n%s", want, body)
		}
	}

	// Fourth failure: no second comment, label already present and stays.
	l.failCleanup(context.Background(), 42, "fourth")
	if len(f.Comments[42]) != 1 {
		t.Fatalf("quarantine comment re-posted; got %d", len(f.Comments[42]))
	}
}

// TestSuccessCleanupResetsQuarantineCounter ensures flaky-but-recoverable
// failures don't accumulate: two failures followed by a success leaves the
// counter at zero, so the next failure starts over from 1.
func TestSuccessCleanupResetsQuarantineCounter(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	f.Refs["refs/heads/claude/issue-42"] = "sha"
	f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"] = "sha"

	q := NewQuarantine(3)
	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	l := &Lifecycle{
		Kind: claim.KindImplementer, Claimer: c, GH: f, Repo: r,
		Log:             slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:      "claude-task",
		LockLabel:       "claude-processing",
		DoneLabel:       "claude-done",
		QuarantineLabel: "claude-failed",
		Quarantine:      q,
	}

	l.failCleanup(context.Background(), 42, "flaky 1")
	l.failCleanup(context.Background(), 42, "flaky 2")
	if got := q.Count(claim.KindImplementer, 42); got != 2 {
		t.Fatalf("pre-success count = %d, want 2", got)
	}
	l.successCleanup(context.Background(), 42)
	if got := q.Count(claim.KindImplementer, 42); got != 0 {
		t.Fatalf("post-success count = %d, want 0 (reset)", got)
	}
}

// TestListCandidatesExcludesQuarantinedIssues verifies the scheduler's
// listCandidates filter: an issue carrying the quarantine label is not
// returned, so the scheduler never claims it again and the lock-label
// flapping described in #43 stops.
func TestListCandidatesExcludesQuarantinedIssues(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Refs["refs/heads/main"] = "basesha"
	f.Issues[1] = &github.Issue{Number: 1, State: "open", Labels: []string{"claude-task"}}
	f.Issues[2] = &github.Issue{Number: 2, State: "open", Labels: []string{"claude-task", "claude-failed"}}

	s := &Scheduler{
		Kind: claim.KindImplementer, Sem: NewSemaphore(2),
		Claimer: claim.New(f, r), GH: f, Repo: r,
		Dispatcher:      &countingDispatcher{},
		Log:             slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:      "claude-task",
		LockLabel:       "claude-processing",
		QuarantineLabel: "claude-failed",
	}
	got, err := s.listCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("quarantined issue not excluded; got %v, want [1]", got)
	}
}

// TestListCandidatesExcludesQuarantinedPRs same thing, but for the PR path
// (reviewer/addresser/merger/resolver all share this branch).
func TestListCandidatesExcludesQuarantinedPRs(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.PRs[10] = &github.PullRequest{Number: 10, State: "open", HeadRefOid: "x", Labels: []string{"claude-review"}}
	f.PRs[11] = &github.PullRequest{Number: 11, State: "open", HeadRefOid: "y", Labels: []string{"claude-review", "claude-failed"}}

	s := &Scheduler{
		Kind: claim.KindReviewer, Sem: NewSemaphore(2),
		Claimer: claim.New(f, r), GH: f, Repo: r,
		Dispatcher:      &countingDispatcher{},
		Log:             slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:      "claude-review",
		LockLabel:       "claude-reviewing",
		QuarantineLabel: "claude-failed",
	}
	got, err := s.listCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 10 {
		t.Fatalf("quarantined PR not excluded; got %v, want [10]", got)
	}
}

// TestTickResetsQuarantineWhenHumanRemovesLabel models the re-enable flow:
// after quarantine fires, a human removes the label, and on the next tick
// the item is a candidate again. The scheduler must reset the in-memory
// counter so a fresh quarantine window starts — otherwise a single
// subsequent failure would immediately re-apply the label.
func TestTickResetsQuarantineWhenHumanRemovesLabel(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Refs["refs/heads/main"] = "basesha"
	f.Issues[7] = &github.Issue{Number: 7, State: "open", Labels: []string{"claude-task"}}

	q := NewQuarantine(3)
	// Simulate three prior failures so count=3 and labeled=true.
	for i := 0; i < 3; i++ {
		q.RecordFailure(claim.KindImplementer, 7, "boom")
	}
	if !q.WasLabeled(claim.KindImplementer, 7) {
		t.Fatal("precondition: should be labeled after 3 failures")
	}

	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	s := &Scheduler{
		Kind: claim.KindImplementer, Sem: NewSemaphore(1),
		Claimer: c, GH: f, Repo: r,
		Dispatcher:      &countingDispatcher{},
		Log:             slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:      "claude-task",
		LockLabel:       "claude-processing",
		QuarantineLabel: "claude-failed",
		Quarantine:      q,
	}
	// Note: issue #7 has no quarantine label on the fake — models a human
	// having just removed it — so listCandidates returns it.
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := q.Count(claim.KindImplementer, 7); got != 0 {
		t.Fatalf("counter not reset after human-cleared label; got %d", got)
	}
	if q.WasLabeled(claim.KindImplementer, 7) {
		t.Fatal("labeled flag not reset after human-cleared label")
	}
}
