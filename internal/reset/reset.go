package reset

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

var refPrefixes = []string{
	// cc-crew-owned implementer branch (unchanged namespace)
	"heads/claude/issue-",
	// NEW cc-crew refs
	"cc-crew/claim/issue-",
	"cc-crew/review-lock/pr-",
	"cc-crew/review-claim/pr-",
	"cc-crew/address-lock/pr-",
	"cc-crew/address-claim/pr-",
	"cc-crew/addressed/pr-",
	"cc-crew/rereviewed/pr-",
	"cc-crew/merge-lock/pr-",
	"cc-crew/merge-claim/pr-",
	"cc-crew/resolve-lock/pr-",
	"cc-crew/resolve-claim/pr-",
	// LEGACY paths from before the 2026-04-18 cc-crew namespace
	// migration. Harmless to keep listing — ListMatchingRefs returns
	// an empty slice when nothing exists under the prefix. Remove in
	// a future release once no live repo has legacy state.
	"tags/claim/issue-",
	"tags/review-lock/pr-",
	"tags/review-claim/pr-",
	"tags/address-lock/pr-",
	"tags/address-claim/pr-",
	"tags/cc-crew-addressed/pr-",
	"tags/cc-crew-rereviewed/pr-",
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
	DoneLabel       string
	ReviewLabel     string
	ReviewingLabel  string
	ReviewedLabel   string
	AddressLabel    string
	AddressingLabel string
	AddressedLabel  string

	MergeLabel           string
	MergingLabel         string
	ResolveConflictLabel string
	ResolvingLabel       string
	ConflictBlockedLabel string

	QuarantineLabel string
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
			if pref == "cc-crew/review-lock/pr-" || pref == "tags/review-lock/pr-" {
				if n := parsePR(r.Name); n > 0 {
					p.ReviewerPRs = append(p.ReviewerPRs, n)
				}
			}
		}
	}
	// Also pick up issues/PRs whose cc-crew labels (processing or done) were
	// left behind without a corresponding ref — partial cleanup, manual ref
	// deletion, etc. Without this reset can't clean the orphaned label.
	for _, label := range []string{o.ProcessingLabel, o.DoneLabel, o.QuarantineLabel} {
		if label == "" {
			continue
		}
		orphans, err := o.GH.ListIssues(ctx, o.Repo, []string{label}, nil)
		if err != nil {
			return p, err
		}
		for _, is := range orphans {
			if !containsInt(p.ImplementerIssues, is.Number) {
				p.ImplementerIssues = append(p.ImplementerIssues, is.Number)
			}
		}
	}
	for _, label := range []string{
		o.ReviewingLabel, o.ReviewedLabel,
		o.AddressingLabel, o.AddressedLabel,
		o.MergingLabel, o.ResolvingLabel, o.ConflictBlockedLabel,
		o.QuarantineLabel,
	} {
		if label == "" {
			continue
		}
		orphans, err := o.GH.ListPRs(ctx, o.Repo, []string{label}, nil)
		if err != nil {
			return p, err
		}
		for _, pr := range orphans {
			if !containsInt(p.ReviewerPRs, pr.Number) {
				p.ReviewerPRs = append(p.ReviewerPRs, pr.Number)
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

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
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
		if o.DoneLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.DoneLabel)
		}
		if o.QuarantineLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.QuarantineLabel)
		}
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
		if o.ReviewedLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ReviewedLabel)
		}
		if o.AddressLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.AddressLabel)
		}
		if o.AddressingLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.AddressingLabel)
		}
		if o.AddressedLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.AddressedLabel)
		}
		if o.MergingLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.MergingLabel)
		}
		if o.ResolvingLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ResolvingLabel)
		}
		if o.ConflictBlockedLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ConflictBlockedLabel)
		}
		if o.MergeLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.MergeLabel)
		}
		if o.ResolveConflictLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ResolveConflictLabel)
		}
		if o.QuarantineLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.QuarantineLabel)
		}
		_ = o.GH.AddLabel(ctx, o.Repo, n, o.ReviewLabel)
	}
	for _, wt := range p.Worktrees {
		fmt.Fprintf(out, "remove worktree: %s\n", wt)
		if err := o.WT.Remove(ctx, filepath.Base(wt)); err != nil {
			// Best-effort; log and continue.
			fmt.Fprintf(out, "  (warning: remove worktree %s: %v)\n", wt, err)
		}
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
	for _, prefix := range []string{
		"refs/cc-crew/review-lock/pr-",
		"refs/tags/review-lock/pr-",
	} {
		s := strings.TrimPrefix(refName, prefix)
		if s != refName {
			n, err := strconv.Atoi(s)
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
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
