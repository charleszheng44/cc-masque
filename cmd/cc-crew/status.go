package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charleszheng44/cc-crew/internal/config"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/status"
)

func runStatus(args []string) int {
	fs := flag.NewFlagSet("cc-crew status", flag.ContinueOnError)
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
	defaults := config.Defaults()
	o := status.Options{
		GH: github.NewGhClient(), Docker: docker.New(),
		Repo:              github.Repo{Owner: owner, Name: name},
		TaskLabel:         firstNonEmpty(os.Getenv("CC_TASK_LABEL"), defaults.TaskLabel),
		ProcessingLabel:   firstNonEmpty(os.Getenv("CC_PROCESSING_LABEL"), defaults.ProcessingLabel),
		ReviewLabel:       firstNonEmpty(os.Getenv("CC_REVIEW_LABEL"), defaults.ReviewLabel),
		ReviewingLabel:    firstNonEmpty(os.Getenv("CC_REVIEWING_LABEL"), defaults.ReviewingLabel),
		ContinuousEnabled: envBoolDefault("CC_CONTINUOUS", defaults.Continuous),
		MaxCycles:         envIntDefault("CC_MAX_CYCLES", defaults.MaxCycles),
	}
	snap, err := status.Fetch(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	status.Render(os.Stdout, snap)
	return 0
}

func envBoolDefault(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envIntDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}
