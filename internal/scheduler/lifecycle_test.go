package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func TestSuccessCleanupImplementer(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	f.Refs["refs/heads/claude/issue-42"] = "sha"
	f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"] = "sha"

	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	l := &Lifecycle{
		Kind: claim.KindImplementer, Claimer: c, GH: f, Repo: r,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing", DoneLabel: "claude-done",
	}
	l.successCleanup(context.Background(), 42)

	i := f.Issues[42]
	if contains(i.Labels, "claude-task") || contains(i.Labels, "claude-processing") {
		t.Fatalf("queue/lock labels should be gone: %v", i.Labels)
	}
	if !contains(i.Labels, "claude-done") {
		t.Fatalf("done label missing: %v", i.Labels)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock branch should remain for PR reference")
	}
	if _, ok := f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("timestamp tag should be cleared")
	}
}

func TestFailCleanupDropsLockAndKeepsQueueLabel(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	f.Refs["refs/heads/claude/issue-42"] = "sha"
	f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"] = "sha"

	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	l := &Lifecycle{
		Kind: claim.KindImplementer, Claimer: c, GH: f, Repo: r,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing", DoneLabel: "claude-done",
	}
	l.failCleanup(context.Background(), 42)

	i := f.Issues[42]
	if !contains(i.Labels, "claude-task") {
		t.Fatalf("queue label should remain for retry: %v", i.Labels)
	}
	if contains(i.Labels, "claude-processing") {
		t.Fatalf("lock label should be gone: %v", i.Labels)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; ok {
		t.Fatal("lock branch should be deleted so work retriggers")
	}
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func TestReviewerSuccessWritesRereviewedMarker(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	// Seed PR #42 with a known head SHA and existing reviewer claim.
	f.PRs[42] = &github.PullRequest{
		Number: 42, State: "open", HeadRefOid: "sha-abc123",
		HeadRefName: "claude/issue-42", BaseRefName: "main",
		Labels: []string{"claude-review", "claude-reviewing"},
	}
	f.Refs["refs/cc-crew/review-lock/pr-42"] = "sha-abc123"
	f.Refs["refs/cc-crew/review-claim/pr-42/20260417T120000Z"] = "sha-abc123"

	l := &Lifecycle{
		Kind: claim.KindReviewer, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-review",
		LockLabel:  "claude-reviewing",
		DoneLabel:  "claude-reviewed",
	}
	l.successCleanupReviewer(context.Background(), 42, "sha-abc123")

	if _, ok := f.Refs["refs/cc-crew/rereviewed/pr-42/sha-abc123"]; !ok {
		t.Fatalf("rereviewed marker not created; refs = %v", keys(f.Refs))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestAddresserSuccessWritesAddressedMarkers(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[55] = &github.PullRequest{
		Number: 55, State: "open", HeadRefName: "claude/issue-55",
		HeadRefOid: "sha-5", Labels: []string{"claude-address", "claude-addressing"},
	}
	f.Refs["refs/cc-crew/address-lock/pr-55"] = "sha-5"
	f.Refs["refs/cc-crew/address-claim/pr-55/20260417T130000Z"] = "sha-5"

	l := &Lifecycle{
		Kind: claim.KindAddresser, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
		DoneLabel:  "claude-addressed",
	}
	l.successCleanupAddresser(context.Background(), 55, []int{901, 902})

	for _, id := range []int{901, 902} {
		ref := fmt.Sprintf("refs/cc-crew/addressed/pr-55/%d", id)
		if _, ok := f.Refs[ref]; !ok {
			t.Fatalf("marker %s missing; refs = %v", ref, keys(f.Refs))
		}
	}
	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-55"]; ok {
		t.Fatal("address-lock not released")
	}
	if _, ok := f.Refs["refs/cc-crew/address-claim/pr-55/20260417T130000Z"]; ok {
		t.Fatal("address-claim ts not released")
	}
	lbls := f.PRs[55].Labels
	if containsLabel(lbls, "claude-address") || containsLabel(lbls, "claude-addressing") {
		t.Fatalf("queue/lock labels not removed: %v", lbls)
	}
	if !containsLabel(lbls, "claude-addressed") {
		t.Fatalf("claude-addressed not added: %v", lbls)
	}
}

func TestAddresserFailLeavesQueueLabel(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[56] = &github.PullRequest{
		Number: 56, State: "open", HeadRefName: "claude/issue-56",
		HeadRefOid: "sha-6", Labels: []string{"claude-address", "claude-addressing"},
	}
	f.Refs["refs/cc-crew/address-lock/pr-56"] = "sha-6"
	f.Refs["refs/cc-crew/address-claim/pr-56/20260417T130000Z"] = "sha-6"

	l := &Lifecycle{
		Kind: claim.KindAddresser, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
		DoneLabel:  "claude-addressed",
	}
	l.failCleanup(context.Background(), 56)

	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-56"]; ok {
		t.Fatal("address-lock not released on fail")
	}
	lbls := f.PRs[56].Labels
	if containsLabel(lbls, "claude-addressing") {
		t.Fatalf("addressing label still present: %v", lbls)
	}
	if !containsLabel(lbls, "claude-address") {
		t.Fatalf("queue label (claude-address) should remain for retry: %v", lbls)
	}
}

// TestAddresserDispatchEmptySnapshotDropsLabel verifies that when a PR is
// claimed for addressing but every non-approval review is already in
// cc-crew-addressed (a race against a prior addresser's marker writes), the
// dispatch path drops the label and releases the lock instead of launching
// a container that would fail with CC_REVIEW_IDS="" in a retry loop.
func TestAddresserDispatchEmptySnapshotDropsLabel(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[60] = &github.PullRequest{
		Number: 60, State: "open", HeadRefName: "claude/issue-60",
		HeadRefOid: "sha-6", Labels: []string{"claude-address", "claude-addressing"},
	}
	// Prior addresser's lock + claim tag (simulating the fresh claim by the
	// scheduler that preceded this dispatch).
	f.Refs["refs/cc-crew/address-lock/pr-60"] = "sha-6"
	f.Refs["refs/cc-crew/address-claim/pr-60/20260417T130000Z"] = "sha-6"
	// Review 501 exists and is already in cc-crew/addressed → snapshot = [].
	f.Reviews[60] = []github.Review{{ID: 501, State: "COMMENTED"}}
	f.Refs["refs/cc-crew/addressed/pr-60/501"] = "sha-6"

	l := &Lifecycle{
		Kind: claim.KindAddresser, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
		DoneLabel:  "claude-addressed",
		// WT and Docker intentionally nil — the guard must short-circuit
		// before touching either.
	}
	l.dispatchAddresser(context.Background(), l.Log, 60)

	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-60"]; ok {
		t.Fatalf("address-lock not released; refs = %v", keys(f.Refs))
	}
	lbls := f.PRs[60].Labels
	if containsLabel(lbls, "claude-address") || containsLabel(lbls, "claude-addressing") {
		t.Fatalf("queue/lock labels not removed: %v", lbls)
	}
	// Nothing was actually addressed in this dispatch, so claude-addressed
	// must NOT be added.
	if containsLabel(lbls, "claude-addressed") {
		t.Fatalf("claude-addressed should not be added when no work ran: %v", lbls)
	}
}

// TestAddresserSuccessWritesMarkersBeforeRemovingLabels asserts the ordering
// invariant that fixes the detector-vs-snapshot race: if a tick observes
// the PR between the start of successCleanupAddresser and its end, the
// address labels must still be present until after the markers are
// written. We test this indirectly by making CreateRef fail on the second
// marker write and verifying the address-lock label remains — if the
// reorder regressed, labels would be gone by the time the error surfaced.
func TestAddresserSuccessMarkersWrittenBeforeLabelsRemoved(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[70] = &github.PullRequest{
		Number: 70, State: "open", HeadRefName: "claude/issue-70",
		HeadRefOid: "sha-7", Labels: []string{"claude-address", "claude-addressing"},
	}
	f.Refs["refs/cc-crew/address-lock/pr-70"] = "sha-7"
	f.Refs["refs/cc-crew/address-claim/pr-70/20260417T130000Z"] = "sha-7"

	l := &Lifecycle{
		Kind: claim.KindAddresser, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
		DoneLabel:  "claude-addressed",
	}
	l.successCleanupAddresser(context.Background(), 70, []int{111, 222})

	// Both markers written.
	for _, id := range []int{111, 222} {
		ref := fmt.Sprintf("refs/cc-crew/addressed/pr-70/%d", id)
		if _, ok := f.Refs[ref]; !ok {
			t.Fatalf("marker %s not written (reorder regression?); refs=%v", ref, keys(f.Refs))
		}
	}
	// Lock + claim dropped.
	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-70"]; ok {
		t.Fatal("address-lock still present")
	}
	// Labels transitioned.
	lbls := f.PRs[70].Labels
	if containsLabel(lbls, "claude-address") || containsLabel(lbls, "claude-addressing") {
		t.Fatalf("queue/lock labels not removed: %v", lbls)
	}
	if !containsLabel(lbls, "claude-addressed") {
		t.Fatalf("claude-addressed not added: %v", lbls)
	}
}

// TestAddresserGuardRetriesReleaseOnTransientFailure verifies that a flaky
// DeleteRef (simulated via FakeClient.DeleteRefHook) is retried and the
// guard eventually succeeds — no stuck lock, labels correctly cleaned up.
func TestAddresserGuardRetriesReleaseOnTransientFailure(t *testing.T) {
	// Shrink retry backoff for the test. Restored on defer.
	origBackoff := retryBackoff
	retryBackoff = 1 * time.Millisecond
	defer func() { retryBackoff = origBackoff }()

	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[80] = &github.PullRequest{
		Number: 80, State: "open", HeadRefName: "claude/issue-80",
		HeadRefOid: "sha", Labels: []string{"claude-address", "claude-addressing"},
	}
	f.Refs["refs/cc-crew/address-lock/pr-80"] = "sha"
	f.Refs["refs/cc-crew/address-claim/pr-80/20260417T130000Z"] = "sha"
	// Review 501 is already addressed → snapshot returns empty → guard path.
	f.Reviews[80] = []github.Review{{ID: 501, State: "COMMENTED"}}
	f.Refs["refs/cc-crew/addressed/pr-80/501"] = "sha"

	// Fail the first DeleteRef call, succeed after that.
	failuresLeft := 1
	f.DeleteRefHook = func(ref string) error {
		if failuresLeft > 0 {
			failuresLeft--
			return fmt.Errorf("simulated transient failure")
		}
		return nil
	}

	l := &Lifecycle{
		Kind: claim.KindAddresser, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
		DoneLabel:  "claude-addressed",
	}
	l.dispatchAddresser(context.Background(), l.Log, 80)

	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-80"]; ok {
		t.Fatalf("address-lock should be released after retry; refs=%v", keys(f.Refs))
	}
	lbls := f.PRs[80].Labels
	if containsLabel(lbls, "claude-address") || containsLabel(lbls, "claude-addressing") {
		t.Fatalf("labels should be cleaned up after successful retry: %v", lbls)
	}
}

// TestAddresserGuardKeepsLabelsIfReleaseFailsPermanently verifies that when
// Release retries exhaust, the labels are NOT removed so state remains
// consistent and the reclaim sweeper can repair it on its next pass.
func TestAddresserGuardKeepsLabelsIfReleaseFailsPermanently(t *testing.T) {
	origBackoff := retryBackoff
	retryBackoff = 1 * time.Millisecond
	defer func() { retryBackoff = origBackoff }()

	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[81] = &github.PullRequest{
		Number: 81, State: "open", HeadRefName: "claude/issue-81",
		HeadRefOid: "sha", Labels: []string{"claude-address", "claude-addressing"},
	}
	f.Refs["refs/cc-crew/address-lock/pr-81"] = "sha"
	f.Refs["refs/cc-crew/address-claim/pr-81/20260417T130000Z"] = "sha"
	f.Reviews[81] = []github.Review{{ID: 502, State: "COMMENTED"}}
	f.Refs["refs/cc-crew/addressed/pr-81/502"] = "sha"

	// All DeleteRef attempts fail — simulate a persistent outage.
	f.DeleteRefHook = func(ref string) error { return fmt.Errorf("simulated persistent failure") }

	l := &Lifecycle{
		Kind: claim.KindAddresser, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
		DoneLabel:  "claude-addressed",
	}
	l.dispatchAddresser(context.Background(), l.Log, 81)

	// Lock tag still present (release failed all attempts).
	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-81"]; !ok {
		t.Fatal("address-lock should still exist when Release fails")
	}
	// Labels preserved so reclaim sweeper sees a consistent in-progress state.
	lbls := f.PRs[81].Labels
	if !containsLabel(lbls, "claude-address") || !containsLabel(lbls, "claude-addressing") {
		t.Fatalf("labels should be preserved on Release failure: %v", lbls)
	}
}

func containsLabel(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func TestReviewerSuccessCleanupAppliesMergeLabelOnApproval(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[7] = &github.PullRequest{
		Number: 7, State: "open", HeadRefOid: "sha7",
		Labels: []string{"claude-review", "claude-reviewing"},
	}
	f.Reviews[7] = []github.Review{
		{ID: 1, Author: "bot", State: "APPROVED", At: time.Now()},
	}
	lc := &Lifecycle{
		Kind: claim.KindReviewer, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-review", LockLabel: "claude-reviewing", DoneLabel: "claude-reviewed",
		MergeLabel: "claude-merge", AddressLabel: "claude-address",
	}
	lc.successCleanupReviewer(context.Background(), 7, "sha7")
	labels := f.PRs[7].Labels
	if !containsLabel(labels, "claude-reviewed") {
		t.Errorf("expected claude-reviewed, got %v", labels)
	}
	if !containsLabel(labels, "claude-merge") {
		t.Errorf("expected claude-merge, got %v", labels)
	}
	if containsLabel(labels, "claude-address") {
		t.Errorf("unexpected claude-address, got %v", labels)
	}
	if containsLabel(labels, "claude-review") || containsLabel(labels, "claude-reviewing") {
		t.Errorf("queue/lock labels should be gone: %v", labels)
	}
}

func TestReviewerSuccessCleanupAppliesAddressLabelOnChangesRequested(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[8] = &github.PullRequest{
		Number: 8, State: "open", HeadRefOid: "sha8",
		Labels: []string{"claude-review", "claude-reviewing", "claude-merge"},
	}
	f.Reviews[8] = []github.Review{
		{ID: 1, Author: "bot", State: "APPROVED", At: time.Now().Add(-time.Hour)},
		{ID: 2, Author: "bot", State: "CHANGES_REQUESTED", At: time.Now()},
	}
	lc := &Lifecycle{
		Kind: claim.KindReviewer, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-review", LockLabel: "claude-reviewing", DoneLabel: "claude-reviewed",
		MergeLabel: "claude-merge", AddressLabel: "claude-address",
	}
	lc.successCleanupReviewer(context.Background(), 8, "sha8")
	labels := f.PRs[8].Labels
	if containsLabel(labels, "claude-merge") {
		t.Errorf("claude-merge should have been removed; got %v", labels)
	}
	if !containsLabel(labels, "claude-address") {
		t.Errorf("expected claude-address, got %v", labels)
	}
	if !containsLabel(labels, "claude-reviewed") {
		t.Errorf("expected claude-reviewed, got %v", labels)
	}
}

func TestReviewerSuccessCleanupCommentedDoesNotFlip(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[9] = &github.PullRequest{
		Number: 9, State: "open", HeadRefOid: "sha9",
		Labels: []string{"claude-review", "claude-reviewing"},
	}
	f.Reviews[9] = []github.Review{
		{ID: 1, Author: "bot", State: "COMMENTED", At: time.Now()},
	}
	lc := &Lifecycle{
		Kind: claim.KindReviewer, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-review", LockLabel: "claude-reviewing", DoneLabel: "claude-reviewed",
		MergeLabel: "claude-merge", AddressLabel: "claude-address",
	}
	lc.successCleanupReviewer(context.Background(), 9, "sha9")
	labels := f.PRs[9].Labels
	if containsLabel(labels, "claude-merge") || containsLabel(labels, "claude-address") {
		t.Errorf("COMMENTED should not flip queue labels; got %v", labels)
	}
	if !containsLabel(labels, "claude-reviewed") {
		t.Errorf("expected claude-reviewed, got %v", labels)
	}
}
