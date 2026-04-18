package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/charleszheng44/cc-crew/internal/config"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/reset"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func runReset(args []string) int {
	fs := flag.NewFlagSet("cc-crew reset", flag.ContinueOnError)
	repo := fs.String("repo", os.Getenv("CC_REPO"), "Local repo path (default: $PWD)")
	yes := fs.Bool("yes", false, "Skip confirmation and actually apply the plan")
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
	defaults := config.Defaults()
	o := reset.Options{
		GH: github.NewGhClient(), Docker: docker.New(),
		WT:              worktree.New(*repo),
		Repo:            github.Repo{Owner: owner, Name: name},
		TaskLabel:       firstNonEmpty(os.Getenv("CC_TASK_LABEL"), defaults.TaskLabel),
		ProcessingLabel: firstNonEmpty(os.Getenv("CC_PROCESSING_LABEL"), defaults.ProcessingLabel),
		DoneLabel:       firstNonEmpty(os.Getenv("CC_DONE_LABEL"), defaults.DoneLabel),
		ReviewLabel:     firstNonEmpty(os.Getenv("CC_REVIEW_LABEL"), defaults.ReviewLabel),
		ReviewingLabel:  firstNonEmpty(os.Getenv("CC_REVIEWING_LABEL"), defaults.ReviewingLabel),
		ReviewedLabel:   firstNonEmpty(os.Getenv("CC_REVIEWED_LABEL"), defaults.ReviewedLabel),
		AddressLabel:    firstNonEmpty(os.Getenv("CC_ADDRESS_LABEL"), defaults.AddressLabel),
		AddressingLabel: firstNonEmpty(os.Getenv("CC_ADDRESSING_LABEL"), defaults.AddressingLabel),
		AddressedLabel:  firstNonEmpty(os.Getenv("CC_ADDRESSED_LABEL"), defaults.AddressedLabel),
	}
	plan, err := reset.Compute(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Plan: %d refs, %d containers, %d worktrees, %d issues, %d PRs\n",
		len(plan.Refs), len(plan.Containers), len(plan.Worktrees),
		len(plan.ImplementerIssues), len(plan.ReviewerPRs))
	if !*yes {
		fmt.Println("(dry run) re-run with --yes to apply")
		return 0
	}
	if err := reset.Execute(ctx, o, plan, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
