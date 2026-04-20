package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/charleszheng44/cc-crew/internal/config"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// labelSpec is one GitHub label to create: effective name (post env
// override), canonical color, canonical description.
type labelSpec struct {
	Name        string
	Color       string
	Description string
}

// initOptions collects everything doInit needs. Split from runInit so
// tests can inject a FakeClient and capture output.
type initOptions struct {
	GH     github.Client
	Repo   github.Repo
	Specs  []labelSpec
	Out    io.Writer
	Errout io.Writer
}

// buildLabelSpecs returns the fourteen label specs with their canonical
// colors and descriptions, using the provided getenv to honor the same
// CC_*_LABEL overrides as the other subcommands. Pass os.Getenv in
// production.
func buildLabelSpecs(getenv func(string) string) []labelSpec {
	d := config.Defaults()
	return []labelSpec{
		{Name: firstNonEmpty(getenv("CC_TASK_LABEL"), d.TaskLabel),
			Color: "1d76db", Description: "Queue an issue for the cc-crew implementer"},
		{Name: firstNonEmpty(getenv("CC_PROCESSING_LABEL"), d.ProcessingLabel),
			Color: "0366d6", Description: "Implementer is working on this issue"},
		{Name: firstNonEmpty(getenv("CC_DONE_LABEL"), d.DoneLabel),
			Color: "0e8a16", Description: "Implementer opened a PR for this issue"},
		{Name: firstNonEmpty(getenv("CC_REVIEW_LABEL"), d.ReviewLabel),
			Color: "6f42c1", Description: "Queue a PR for the cc-crew reviewer"},
		{Name: firstNonEmpty(getenv("CC_REVIEWING_LABEL"), d.ReviewingLabel),
			Color: "8a63d2", Description: "Reviewer is working on this PR"},
		{Name: firstNonEmpty(getenv("CC_REVIEWED_LABEL"), d.ReviewedLabel),
			Color: "5319e7", Description: "Reviewer posted a review on this PR"},
		{Name: firstNonEmpty(getenv("CC_ADDRESS_LABEL"), d.AddressLabel),
			Color: "d93f0b", Description: "Queue a PR for the implementer to address feedback"},
		{Name: firstNonEmpty(getenv("CC_ADDRESSING_LABEL"), d.AddressingLabel),
			Color: "e99695", Description: "Implementer is addressing review feedback"},
		{Name: firstNonEmpty(getenv("CC_ADDRESSED_LABEL"), d.AddressedLabel),
			Color: "fbca04", Description: "Implementer pushed updates addressing the review"},
		{Name: firstNonEmpty(getenv("CC_MERGE_LABEL"), d.MergeLabel),
			Color: "2e7d32", Description: "Queue an approved PR for the cc-crew merger"},
		{Name: firstNonEmpty(getenv("CC_MERGING_LABEL"), d.MergingLabel),
			Color: "1b5e20", Description: "Merger is working on this PR"},
		{Name: firstNonEmpty(getenv("CC_RESOLVE_CONFLICT_LABEL"), d.ResolveConflictLabel),
			Color: "bf360c", Description: "Queue a PR for the resolver to fix merge conflicts"},
		{Name: firstNonEmpty(getenv("CC_RESOLVING_LABEL"), d.ResolvingLabel),
			Color: "e65100", Description: "Resolver is working on this PR"},
		{Name: firstNonEmpty(getenv("CC_CONFLICT_BLOCKED_LABEL"), d.ConflictBlockedLabel),
			Color: "b71c1c", Description: "Conflict resolution failed; human attention needed"},
	}
}

// doInit creates each label in o.Specs via o.GH. It prints one line per
// label ("created: X" or "exists:  X") to o.Out, plus a summary line on
// success. On any non-conflict error, it writes the error to o.Errout
// and returns 1 without printing the summary — the user re-runs after
// fixing the cause, and idempotency skips the work already done.
func doInit(ctx context.Context, o initOptions) int {
	created, existed := 0, 0
	for _, s := range o.Specs {
		err := o.GH.CreateLabel(ctx, o.Repo, s.Name, s.Color, s.Description)
		if errors.Is(err, github.ErrLabelExists) {
			fmt.Fprintf(o.Out, "exists:  %s\n", s.Name)
			existed++
			continue
		}
		if err != nil {
			fmt.Fprintln(o.Errout, err)
			return 1
		}
		fmt.Fprintf(o.Out, "created: %s\n", s.Name)
		created++
	}
	fmt.Fprintf(o.Out, "%d labels: %d created, %d existed\n",
		len(o.Specs), created, existed)
	return 0
}

// runInit is the CLI entry point wired into main.go.
func runInit(args []string) int {
	fs := flag.NewFlagSet("cc-crew init", flag.ContinueOnError)
	repo := fs.String("repo", os.Getenv("CC_REPO"), "Local repo path (default: $PWD)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *repo == "" {
		*repo, _ = os.Getwd()
	}
	ctx := context.Background()
	owner, name, err := config.ResolveRepo(ctx, *repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return doInit(ctx, initOptions{
		GH:     github.NewGhClient(),
		Repo:   github.Repo{Owner: owner, Name: name},
		Specs:  buildLabelSpecs(os.Getenv),
		Out:    os.Stdout,
		Errout: os.Stderr,
	})
}
