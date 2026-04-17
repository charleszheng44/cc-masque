package reset

import (
	"bytes"
	"context"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

func TestExecuteRequeuesOpenIssuesAndPRs(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[10] = &github.Issue{Number: 10, State: "open", Labels: []string{"claude-processing"}}
	f.PRs[20] = &github.PullRequest{Number: 20, State: "open", Labels: []string{"claude-reviewing"}}
	f.Refs["refs/heads/claude/issue-10"] = "s1"
	f.Refs["refs/tags/claim/issue-10/20260417T120000Z"] = "s1"
	f.Refs["refs/tags/review-lock/pr-20"] = "s2"
	f.Refs["refs/tags/review-claim/pr-20/20260417T120000Z"] = "s2"

	wt := worktree.New(t.TempDir())
	dr := docker.New()

	o := Options{
		GH: f, Docker: dr, WT: wt, Repo: r,
		TaskLabel: "claude-task", ProcessingLabel: "claude-processing",
		ReviewLabel: "claude-review", ReviewingLabel: "claude-reviewing",
	}
	plan := Plan{
		ImplementerIssues: []int{10},
		ReviewerPRs:       []int{20},
		Refs: []string{
			"refs/heads/claude/issue-10",
			"refs/tags/claim/issue-10/20260417T120000Z",
			"refs/tags/review-lock/pr-20",
			"refs/tags/review-claim/pr-20/20260417T120000Z",
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
	if !hasLabel(i.Labels, "claude-task") || hasLabel(i.Labels, "claude-processing") {
		t.Fatalf("issue labels not restored: %v", i.Labels)
	}
	p := f.PRs[20]
	if !hasLabel(p.Labels, "claude-review") || hasLabel(p.Labels, "claude-reviewing") {
		t.Fatalf("PR labels not restored: %v", p.Labels)
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
