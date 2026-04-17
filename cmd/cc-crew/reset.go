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
	o := reset.Options{
		GH: github.NewGhClient(), Docker: docker.New(),
		WT:              worktree.New(*repo),
		Repo:            github.Repo{Owner: owner, Name: name},
		TaskLabel:       "claude-task",
		ProcessingLabel: "claude-processing",
		ReviewLabel:     "claude-review",
		ReviewingLabel:  "claude-reviewing",
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
