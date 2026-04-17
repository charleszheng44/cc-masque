package reset

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

var refPrefixes = []string{
	"heads/claude/issue-",
	"tags/claim/issue-",
	"tags/review-lock/pr-",
	"tags/review-claim/pr-",
}

type Plan struct {
	ImplementerIssues []int
	ReviewerPRs       []int
	Refs              []string
	Containers        []string
	Worktrees         []string
}

type Options struct {
	GH              github.Client
	Docker          *docker.Runner
	WT              *worktree.Manager
	Repo            github.Repo
	TaskLabel       string
	ProcessingLabel string
	ReviewLabel     string
	ReviewingLabel  string
}

// Compute builds a Plan without making any changes.
func Compute(ctx context.Context, o Options) (Plan, error) {
	var p Plan
	for _, pref := range refPrefixes {
		refs, err := o.GH.ListMatchingRefs(ctx, o.Repo, pref)
		if err != nil {
			return p, err
		}
		for _, r := range refs {
			p.Refs = append(p.Refs, r.Name)
			if pref == "heads/claude/issue-" {
				if n := parseIssue(r.Name); n > 0 {
					p.ImplementerIssues = append(p.ImplementerIssues, n)
				}
			}
			if pref == "tags/review-lock/pr-" {
				if n := parsePR(r.Name); n > 0 {
					p.ReviewerPRs = append(p.ReviewerPRs, n)
				}
			}
		}
	}
	entries, err := o.Docker.PS(ctx, map[string]string{"cc-crew.repo": o.Repo.String()})
	if err != nil {
		return p, err
	}
	for _, e := range entries {
		p.Containers = append(p.Containers, e.Name)
	}
	wts, err := o.WT.List(ctx)
	if err != nil {
		return p, err
	}
	p.Worktrees = wts
	return p, nil
}

// Execute applies a Plan. Writes a short progress log to `out`.
func Execute(ctx context.Context, o Options, p Plan, out io.Writer) error {
	for _, name := range p.Containers {
		fmt.Fprintf(out, "kill container: %s\n", name)
		if err := o.Docker.Kill(ctx, name); err != nil {
			return err
		}
	}
	for _, ref := range p.Refs {
		fmt.Fprintf(out, "delete ref: %s\n", ref)
		if err := o.GH.DeleteRef(ctx, o.Repo, ref); err != nil {
			return err
		}
	}
	issues, err := o.GH.ListIssues(ctx, o.Repo, nil, nil)
	if err != nil {
		return err
	}
	for _, n := range p.ImplementerIssues {
		if !isOpenIssue(issues, n) {
			continue
		}
		fmt.Fprintf(out, "requeue issue #%d\n", n)
		_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ProcessingLabel)
		_ = o.GH.AddLabel(ctx, o.Repo, n, o.TaskLabel)
	}
	prs, err := o.GH.ListPRs(ctx, o.Repo, nil, nil)
	if err != nil {
		return err
	}
	for _, n := range p.ReviewerPRs {
		if !isOpenPR(prs, n) {
			continue
		}
		fmt.Fprintf(out, "requeue PR #%d\n", n)
		_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ReviewingLabel)
		_ = o.GH.AddLabel(ctx, o.Repo, n, o.ReviewLabel)
	}
	for _, wt := range p.Worktrees {
		fmt.Fprintf(out, "remove worktree: %s\n", wt)
	}
	_ = o.WT.Prune(ctx)
	return nil
}

func parseIssue(refName string) int {
	s := strings.TrimPrefix(refName, "refs/heads/claude/issue-")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func parsePR(refName string) int {
	s := strings.TrimPrefix(refName, "refs/tags/review-lock/pr-")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func isOpenIssue(is []github.Issue, n int) bool {
	for _, i := range is {
		if i.Number == n && i.State == "open" {
			return true
		}
	}
	return false
}

func isOpenPR(ps []github.PullRequest, n int) bool {
	for _, p := range ps {
		if p.Number == n && p.State == "open" {
			return true
		}
	}
	return false
}
