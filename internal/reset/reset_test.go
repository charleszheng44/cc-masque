package reset

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

func TestExecuteRequeuesOpenIssuesAndPRs(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[10] = &github.Issue{Number: 10, State: "open", Labels: []string{"claude-processing", "claude-done"}}
	f.PRs[20] = &github.PullRequest{Number: 20, State: "open", Labels: []string{"claude-reviewing", "claude-reviewed"}}
	f.Refs["refs/heads/claude/issue-10"] = "s1"
	f.Refs["refs/cc-crew/claim/issue-10/20260417T120000Z"] = "s1"
	f.Refs["refs/cc-crew/review-lock/pr-20"] = "s2"
	f.Refs["refs/cc-crew/review-claim/pr-20/20260417T120000Z"] = "s2"

	wt := worktree.New(t.TempDir())
	dr := docker.New()

	o := Options{
		GH: f, Docker: dr, WT: wt, Repo: r,
		TaskLabel: "claude-task", ProcessingLabel: "claude-processing", DoneLabel: "claude-done",
		ReviewLabel: "claude-review", ReviewingLabel: "claude-reviewing", ReviewedLabel: "claude-reviewed",
	}
	plan := Plan{
		ImplementerIssues: []int{10},
		ReviewerPRs:       []int{20},
		Refs: []string{
			"refs/heads/claude/issue-10",
			"refs/cc-crew/claim/issue-10/20260417T120000Z",
			"refs/cc-crew/review-lock/pr-20",
			"refs/cc-crew/review-claim/pr-20/20260417T120000Z",
		},
	}
	var buf bytes.Buffer
	if err := Execute(context.Background(), o, plan, &buf); err != nil {
		// WT.Prune will fail because t.TempDir() isn't a git repo; ignore that
		// and check the label/ref state regardless.
		t.Logf("Execute error (likely from Prune on non-repo tmpdir): %v", err)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-10"]; ok {
		t.Fatal("ref should be deleted")
	}
	i := f.Issues[10]
	if !hasLabel(i.Labels, "claude-task") ||
		hasLabel(i.Labels, "claude-processing") ||
		hasLabel(i.Labels, "claude-done") {
		t.Fatalf("issue labels not restored: %v", i.Labels)
	}
	p := f.PRs[20]
	if !hasLabel(p.Labels, "claude-review") ||
		hasLabel(p.Labels, "claude-reviewing") ||
		hasLabel(p.Labels, "claude-reviewed") {
		t.Fatalf("PR labels not restored: %v", p.Labels)
	}
}

func TestComputePicksUpOrphanLockLabels(t *testing.T) {
	// Issue #11 has claude-processing but no ref (orphaned label). Reset must
	// still include it so the label gets cleared.
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[11] = &github.Issue{Number: 11, State: "open", Labels: []string{"claude-processing"}}
	f.PRs[22] = &github.PullRequest{Number: 22, State: "open", Labels: []string{"claude-reviewing"}}

	repoDir := t.TempDir()
	if err := exec.Command("git", "-C", repoDir, "init", "-q").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	wt := worktree.New(repoDir)
	dr := docker.New()
	o := Options{
		GH: f, Docker: dr, WT: wt, Repo: r,
		TaskLabel: "claude-task", ProcessingLabel: "claude-processing", DoneLabel: "claude-done",
		ReviewLabel: "claude-review", ReviewingLabel: "claude-reviewing", ReviewedLabel: "claude-reviewed",
	}
	p, err := Compute(context.Background(), o)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !containsInt(p.ImplementerIssues, 11) {
		t.Fatalf("orphan-labeled issue not picked up: %+v", p.ImplementerIssues)
	}
	if !containsInt(p.ReviewerPRs, 22) {
		t.Fatalf("orphan-labeled PR not picked up: %+v", p.ReviewerPRs)
	}

	var buf bytes.Buffer
	if err := Execute(context.Background(), o, p, &buf); err != nil {
		t.Logf("Execute error (likely from Prune on non-repo tmpdir): %v", err)
	}
	if hasLabel(f.Issues[11].Labels, "claude-processing") ||
		!hasLabel(f.Issues[11].Labels, "claude-task") {
		t.Fatalf("issue #11 labels not restored: %v", f.Issues[11].Labels)
	}
	if hasLabel(f.PRs[22].Labels, "claude-reviewing") ||
		!hasLabel(f.PRs[22].Labels, "claude-review") {
		t.Fatalf("PR #22 labels not restored: %v", f.PRs[22].Labels)
	}
}

func TestComputePicksUpOrphanDoneLabels(t *testing.T) {
	// Issue has claude-done (and claude-task) but no ref — the lock branch
	// was manually deleted. Reset must still pick it up to clear claude-done.
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[35] = &github.Issue{Number: 35, State: "open", Labels: []string{"claude-task", "claude-done"}}
	f.PRs[40] = &github.PullRequest{Number: 40, State: "open", Labels: []string{"claude-review", "claude-reviewed"}}

	repoDir := t.TempDir()
	if err := exec.Command("git", "-C", repoDir, "init", "-q").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	wt := worktree.New(repoDir)
	dr := docker.New()
	o := Options{
		GH: f, Docker: dr, WT: wt, Repo: r,
		TaskLabel: "claude-task", ProcessingLabel: "claude-processing", DoneLabel: "claude-done",
		ReviewLabel: "claude-review", ReviewingLabel: "claude-reviewing", ReviewedLabel: "claude-reviewed",
	}
	p, err := Compute(context.Background(), o)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !containsInt(p.ImplementerIssues, 35) {
		t.Fatalf("done-labeled issue not picked up: %+v", p.ImplementerIssues)
	}
	if !containsInt(p.ReviewerPRs, 40) {
		t.Fatalf("reviewed-labeled PR not picked up: %+v", p.ReviewerPRs)
	}

	var buf bytes.Buffer
	if err := Execute(context.Background(), o, p, &buf); err != nil {
		t.Logf("Execute error (likely from Prune on non-repo tmpdir): %v", err)
	}
	if hasLabel(f.Issues[35].Labels, "claude-done") {
		t.Fatalf("claude-done not cleared: %v", f.Issues[35].Labels)
	}
	if hasLabel(f.PRs[40].Labels, "claude-reviewed") {
		t.Fatalf("claude-reviewed not cleared: %v", f.PRs[40].Labels)
	}
}

func hasLabel(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func TestResetRemovesAddresserLabelsAndRefs(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.PRs[88] = &github.PullRequest{
		Number: 88, State: "open", HeadRefName: "claude/issue-88",
		HeadRefOid: "sha",
		Labels:     []string{"claude-address", "claude-addressing", "claude-addressed"},
	}
	f.Refs["refs/cc-crew/address-lock/pr-88"] = "sha"
	f.Refs["refs/cc-crew/address-claim/pr-88/20260417T120000Z"] = "sha"
	f.Refs["refs/cc-crew/addressed/pr-88/900"] = "sha"
	f.Refs["refs/cc-crew/addressed/pr-88/901"] = "sha"
	f.Refs["refs/cc-crew/rereviewed/pr-88/sha"] = "sha"

	repoDir := t.TempDir()
	if err := exec.Command("git", "-C", repoDir, "init", "-q").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	wt := worktree.New(repoDir)
	dr := docker.New()
	o := Options{
		GH: f, Docker: dr, WT: wt, Repo: r,
		TaskLabel: "claude-task", ProcessingLabel: "claude-processing", DoneLabel: "claude-done",
		ReviewLabel: "claude-review", ReviewingLabel: "claude-reviewing", ReviewedLabel: "claude-reviewed",
		AddressLabel: "claude-address", AddressingLabel: "claude-addressing", AddressedLabel: "claude-addressed",
	}
	p, err := Compute(context.Background(), o)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	var buf bytes.Buffer
	if err := Execute(context.Background(), o, p, &buf); err != nil {
		t.Logf("Execute error (likely from Prune on non-repo tmpdir): %v", err)
	}

	for ref := range f.Refs {
		if strings.HasPrefix(ref, "refs/cc-crew/address-lock/pr-88") ||
			strings.HasPrefix(ref, "refs/cc-crew/address-claim/pr-88/") ||
			strings.HasPrefix(ref, "refs/cc-crew/addressed/pr-88/") ||
			strings.HasPrefix(ref, "refs/cc-crew/rereviewed/pr-88/") {
			t.Fatalf("address/marker ref not cleaned: %s", ref)
		}
	}
	lbls := f.PRs[88].Labels
	if hasLabel(lbls, "claude-address") ||
		hasLabel(lbls, "claude-addressing") ||
		hasLabel(lbls, "claude-addressed") {
		t.Fatalf("addresser labels still present: %v", lbls)
	}
}

// TestResetCleansLegacyTagsNamespace verifies that a post-upgrade
// `cc-crew reset` still removes stale refs/tags/* state from before the
// 2026-04-18 migration.
func TestResetCleansLegacyTagsNamespace(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.PRs[99] = &github.PullRequest{
		Number: 99, State: "open", HeadRefName: "claude/issue-99",
		HeadRefOid: "sha",
		Labels:     []string{"claude-addressing"},
	}
	// Seed legacy refs/tags/* paths (pre-migration state).
	f.Refs["refs/tags/address-lock/pr-99"] = "sha"
	f.Refs["refs/tags/address-claim/pr-99/20260417T120000Z"] = "sha"
	f.Refs["refs/tags/cc-crew-addressed/pr-99/700"] = "sha"
	f.Refs["refs/tags/cc-crew-rereviewed/pr-99/sha"] = "sha"

	repoDir := t.TempDir()
	if err := exec.Command("git", "-C", repoDir, "init", "-q").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	wt := worktree.New(repoDir)
	dr := docker.New()
	o := Options{
		GH: f, Docker: dr, WT: wt, Repo: r,
		TaskLabel: "claude-task", ProcessingLabel: "claude-processing", DoneLabel: "claude-done",
		ReviewLabel: "claude-review", ReviewingLabel: "claude-reviewing", ReviewedLabel: "claude-reviewed",
		AddressLabel: "claude-address", AddressingLabel: "claude-addressing", AddressedLabel: "claude-addressed",
	}
	p, err := Compute(context.Background(), o)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	var buf bytes.Buffer
	if err := Execute(context.Background(), o, p, &buf); err != nil {
		t.Logf("Execute error (likely from Prune on non-repo tmpdir): %v", err)
	}
	for ref := range f.Refs {
		if strings.HasPrefix(ref, "refs/tags/address-lock/pr-99") ||
			strings.HasPrefix(ref, "refs/tags/address-claim/pr-99/") ||
			strings.HasPrefix(ref, "refs/tags/cc-crew-addressed/pr-99/") ||
			strings.HasPrefix(ref, "refs/tags/cc-crew-rereviewed/pr-99/") {
			t.Fatalf("legacy ref not cleaned: %s", ref)
		}
	}
}
