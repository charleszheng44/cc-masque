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
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "sha"

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
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("timestamp tag should be cleared")
	}
}

func TestFailCleanupDropsLockAndKeepsQueueLabel(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	f.Refs["refs/heads/claude/issue-42"] = "sha"
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "sha"

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
	f.Refs["refs/tags/review-lock/pr-42"] = "sha-abc123"
	f.Refs["refs/tags/review-claim/pr-42/20260417T120000Z"] = "sha-abc123"

	l := &Lifecycle{
		Kind: claim.KindReviewer, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-review",
		LockLabel:  "claude-reviewing",
		DoneLabel:  "claude-reviewed",
	}
	l.successCleanupReviewer(context.Background(), 42, "sha-abc123")

	if _, ok := f.Refs["refs/tags/cc-crew-rereviewed/pr-42/sha-abc123"]; !ok {
		t.Fatalf("cc-crew-rereviewed marker not created; refs = %v", keys(f.Refs))
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
	f.Refs["refs/tags/address-lock/pr-55"] = "sha-5"
	f.Refs["refs/tags/address-claim/pr-55/20260417T130000Z"] = "sha-5"

	l := &Lifecycle{
		Kind: claim.KindAddresser, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
		DoneLabel:  "claude-addressed",
	}
	l.successCleanupAddresser(context.Background(), 55, []int{901, 902})

	for _, id := range []int{901, 902} {
		ref := fmt.Sprintf("refs/tags/cc-crew-addressed/pr-55/%d", id)
		if _, ok := f.Refs[ref]; !ok {
			t.Fatalf("marker %s missing; refs = %v", ref, keys(f.Refs))
		}
	}
	if _, ok := f.Refs["refs/tags/address-lock/pr-55"]; ok {
		t.Fatal("address-lock not released")
	}
	if _, ok := f.Refs["refs/tags/address-claim/pr-55/20260417T130000Z"]; ok {
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
	f.Refs["refs/tags/address-lock/pr-56"] = "sha-6"
	f.Refs["refs/tags/address-claim/pr-56/20260417T130000Z"] = "sha-6"

	l := &Lifecycle{
		Kind: claim.KindAddresser, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
		DoneLabel:  "claude-addressed",
	}
	l.failCleanup(context.Background(), 56)

	if _, ok := f.Refs["refs/tags/address-lock/pr-56"]; ok {
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

func containsLabel(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
