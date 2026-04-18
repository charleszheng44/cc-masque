package continuous

import (
	"context"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/github"
)

func baseOpts(f *github.FakeClient) Options {
	return Options{
		GH:        f,
		Repo:      github.Repo{Owner: "a", Name: "b"},
		MaxCycles: 3,
		Labels: Labels{
			Review:     "claude-review",
			Reviewing:  "claude-reviewing",
			Reviewed:   "claude-reviewed",
			Address:    "claude-address",
			Addressing: "claude-addressing",
		},
	}
}

func TestDetectSkipsNonCcCrewPR(t *testing.T) {
	f := github.NewFake()
	f.PRs[1] = &github.PullRequest{
		Number: 1, State: "open", HeadRefName: "feature/unrelated",
		HeadRefOid: "sha-x", Labels: []string{"claude-reviewed"},
	}
	r, err := Detect(context.Background(), baseOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.AddressLabeled != 0 || r.ReviewFlipped != 0 {
		t.Fatalf("unexpected action: %+v", r)
	}
}

func TestDetectFlipsReviewedToReviewWhenHeadChanged(t *testing.T) {
	f := github.NewFake()
	f.PRs[10] = &github.PullRequest{
		Number: 10, State: "open", HeadRefName: "claude/issue-10",
		HeadRefOid: "sha-new", Labels: []string{"claude-reviewed"},
	}
	f.Refs["refs/tags/cc-crew-rereviewed/pr-10/sha-old"] = "sha-old"

	r, err := Detect(context.Background(), baseOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.ReviewFlipped != 1 {
		t.Fatalf("expected 1 flip, got %+v", r)
	}
	labels := f.PRs[10].Labels
	if containsLabel(labels, "claude-reviewed") || !containsLabel(labels, "claude-review") {
		t.Fatalf("labels after flip: %v", labels)
	}
}

func TestDetectSkipsReviewFlipWhenAlreadyRereviewed(t *testing.T) {
	f := github.NewFake()
	f.PRs[11] = &github.PullRequest{
		Number: 11, State: "open", HeadRefName: "claude/issue-11",
		HeadRefOid: "sha-same", Labels: []string{"claude-reviewed"},
	}
	f.Refs["refs/tags/cc-crew-rereviewed/pr-11/sha-same"] = "sha-same"

	r, err := Detect(context.Background(), baseOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.ReviewFlipped != 0 {
		t.Fatalf("unexpected flip: %+v", r)
	}
}

func TestDetectLabelsAddressOnUnaddressedReview(t *testing.T) {
	f := github.NewFake()
	f.PRs[20] = &github.PullRequest{
		Number: 20, State: "open", HeadRefName: "claude/issue-20",
		HeadRefOid: "sha-z", Labels: []string{"claude-reviewed"},
	}
	f.Refs["refs/tags/cc-crew-rereviewed/pr-20/sha-z"] = "sha-z"
	f.Reviews[20] = []github.Review{
		{ID: 501, State: "CHANGES_REQUESTED"},
	}

	r, err := Detect(context.Background(), baseOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.AddressLabeled != 1 {
		t.Fatalf("expected 1 address label, got %+v", r)
	}
	if !containsLabel(f.PRs[20].Labels, "claude-address") {
		t.Fatalf("claude-address not applied: %v", f.PRs[20].Labels)
	}
}

func TestDetectIgnoresApprovedAndDismissedReviews(t *testing.T) {
	f := github.NewFake()
	f.PRs[21] = &github.PullRequest{
		Number: 21, State: "open", HeadRefName: "claude/issue-21",
		HeadRefOid: "sha", Labels: []string{"claude-reviewed"},
	}
	f.Refs["refs/tags/cc-crew-rereviewed/pr-21/sha"] = "sha"
	f.Reviews[21] = []github.Review{
		{ID: 1, State: "APPROVED"},
		{ID: 2, State: "DISMISSED"},
		{ID: 3, State: "PENDING"},
	}
	r, err := Detect(context.Background(), baseOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.AddressLabeled != 0 {
		t.Fatalf("unexpected address label on non-trigger states: %+v", r)
	}
}

func TestDetectSkipsAddressAtCycleCap(t *testing.T) {
	f := github.NewFake()
	f.PRs[30] = &github.PullRequest{
		Number: 30, State: "open", HeadRefName: "claude/issue-30",
		HeadRefOid: "sha", Labels: []string{"claude-reviewed"},
	}
	f.Refs["refs/tags/cc-crew-rereviewed/pr-30/sha"] = "sha"
	// Three addressed markers → at cap.
	f.Refs["refs/tags/cc-crew-addressed/pr-30/100"] = "sha"
	f.Refs["refs/tags/cc-crew-addressed/pr-30/101"] = "sha"
	f.Refs["refs/tags/cc-crew-addressed/pr-30/102"] = "sha"
	f.Reviews[30] = []github.Review{{ID: 200, State: "CHANGES_REQUESTED"}}

	r, err := Detect(context.Background(), baseOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.AddressLabeled != 0 {
		t.Fatalf("cycle cap not enforced: %+v", r)
	}
}

func TestDetectSkipsAddressWhenAlreadyLabeled(t *testing.T) {
	f := github.NewFake()
	f.PRs[40] = &github.PullRequest{
		Number: 40, State: "open", HeadRefName: "claude/issue-40",
		HeadRefOid: "sha",
		Labels:     []string{"claude-reviewed", "claude-address"}, // already queued
	}
	f.Refs["refs/tags/cc-crew-rereviewed/pr-40/sha"] = "sha"
	f.Reviews[40] = []github.Review{{ID: 300, State: "COMMENTED"}}

	r, err := Detect(context.Background(), baseOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.AddressLabeled != 0 {
		t.Fatalf("should not re-label already-queued PR: %+v", r)
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
