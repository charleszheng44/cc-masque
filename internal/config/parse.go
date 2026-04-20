package config

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Parse builds a Config from CLI flags, environment variables, and (for
// RepoDir + Repo) the provided pwd (may be empty). Precedence: flag > env > default.
func Parse(flags []string, getenv func(string) string, pwd string) (Config, error) {
	c := Defaults()
	fs := flag.NewFlagSet("cc-crew up", flag.ContinueOnError)
	var buf bytes.Buffer
	fs.SetOutput(&buf)

	repoDir := fs.String("repo", orDefault(getenv("CC_REPO"), pwd), "Local repo path")
	fs.IntVar(&c.MaxImplementers, "max-implementers", envInt(getenv, "CC_MAX_IMPLEMENTERS", c.MaxImplementers), "Max concurrent implementer tasks")
	fs.IntVar(&c.MaxReviewers, "max-reviewers", envInt(getenv, "CC_MAX_REVIEWERS", c.MaxReviewers), "Max concurrent reviewer tasks")
	fs.IntVar(&c.MaxMergers, "max-mergers", envInt(getenv, "CC_MAX_MERGERS", c.MaxMergers), "Max concurrent merger tasks (0 disables)")

	pollSecs := fs.Int("poll-seconds", envInt(getenv, "CC_POLL_SECONDS", int(c.PollInterval/time.Second)), "Tick interval (seconds)")
	reclaimSecs := fs.Int("reclaim-seconds", envInt(getenv, "CC_RECLAIM_SECONDS", int(c.ReclaimAfter/time.Second)), "Stale-lock age threshold")
	implSecs := fs.Int("impl-task-seconds", envInt(getenv, "CC_IMPL_TASK_SECONDS", int(c.ImplTaskTimeout/time.Second)), "Per-task wall-clock for implementer")
	revSecs := fs.Int("review-task-seconds", envInt(getenv, "CC_REVIEW_TASK_SECONDS", int(c.ReviewTaskTimeout/time.Second)), "Per-task wall-clock for reviewer")

	fs.StringVar(&c.TaskLabel, "task-label", orDefault(getenv("CC_TASK_LABEL"), c.TaskLabel), "Queue label for implementer")
	fs.StringVar(&c.ReviewLabel, "review-label", orDefault(getenv("CC_REVIEW_LABEL"), c.ReviewLabel), "Queue label for reviewer")
	fs.StringVar(&c.AddressLabel, "address-label", orDefault(getenv("CC_ADDRESS_LABEL"), c.AddressLabel), "Queue label for addresser")
	fs.StringVar(&c.AddressingLabel, "addressing-label", orDefault(getenv("CC_ADDRESSING_LABEL"), c.AddressingLabel), "Lock label for addresser")
	fs.StringVar(&c.AddressedLabel, "addressed-label", orDefault(getenv("CC_ADDRESSED_LABEL"), c.AddressedLabel), "Done label for addresser")
	fs.IntVar(&c.MaxCycles, "max-cycles", envInt(getenv, "CC_MAX_CYCLES", c.MaxCycles), "Max address dispatches per PR before the detector stops auto-labeling")
	fs.BoolVar(&c.Continuous, "continuous", envBool(getenv, "CC_CONTINUOUS", c.Continuous), "Enable continuous addressing (address + re-review loops)")
	fs.BoolVar(&c.AutoReview, "auto-review", envBool(getenv, "CC_AUTO_REVIEW", c.AutoReview), "Auto-apply review-label to implementer PRs")
	fs.StringVar(&c.BaseBranch, "base-branch", orDefault(getenv("CC_BASE_BRANCH"), ""), "Base branch (default: GitHub's default branch)")
	fs.IntVar(&c.ImplMaxTurns, "impl-max-turns", envInt(getenv, "CC_IMPL_MAX_TURNS", c.ImplMaxTurns), "Max Claude turns per implementer task")
	fs.IntVar(&c.ReviewMaxTurns, "review-max-turns", envInt(getenv, "CC_REVIEW_MAX_TURNS", c.ReviewMaxTurns), "Max Claude turns per reviewer task")
	fs.StringVar(&c.Image, "image", orDefault(getenv("CC_IMAGE"), c.Image), "Task container image")
	fs.StringVar(&c.Model, "model", orDefault(getenv("CC_MODEL"), c.Model), "Claude model")

	if err := fs.Parse(flags); err != nil {
		return c, fmt.Errorf("flag parse: %w\n%s", err, buf.String())
	}

	c.PollInterval = time.Duration(*pollSecs) * time.Second
	c.ReclaimAfter = time.Duration(*reclaimSecs) * time.Second
	c.ImplTaskTimeout = time.Duration(*implSecs) * time.Second
	c.ReviewTaskTimeout = time.Duration(*revSecs) * time.Second

	if *repoDir == "" {
		return c, fmt.Errorf("--repo is required (or set CC_REPO, or run from inside a repo)")
	}
	abs, err := filepath.Abs(*repoDir)
	if err != nil {
		return c, err
	}
	c.RepoDir = abs

	c.OrchestratorGHToken = firstNonEmpty(getenv("GH_TOKEN"), getenv("GH_TOKEN_IMPLEMENTER"), getenv("GH_TOKEN_REVIEWER"))
	c.ImplementerGHToken = firstNonEmpty(getenv("GH_TOKEN_IMPLEMENTER"), getenv("GH_TOKEN"))
	c.ReviewerGHToken = firstNonEmpty(getenv("GH_TOKEN_REVIEWER"), getenv("GH_TOKEN"))
	c.ClaudeOAuthToken = getenv("CLAUDE_CODE_OAUTH_TOKEN")
	c.AnthropicAPIKey = getenv("ANTHROPIC_API_KEY")
	c.ImplementerGitName = getenv("IMPLEMENTER_GIT_NAME")
	c.ImplementerGitEmail = getenv("IMPLEMENTER_GIT_EMAIL")
	c.ReviewerGitName = getenv("REVIEWER_GIT_NAME")
	c.ReviewerGitEmail = getenv("REVIEWER_GIT_EMAIL")

	return c, nil
}

// ResolveRepo parses `owner/name` from `git remote get-url origin` inside repoDir.
func ResolveRepo(ctx context.Context, repoDir string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "remote", "get-url", "origin")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("git remote get-url origin: %w (%s)", err, stderr.String())
	}
	return ParseOwnerRepo(strings.TrimSpace(out.String()))
}

// ParseOwnerRepo extracts "owner" and "name" from a GitHub remote URL.
// Accepts SSH (git@github.com:owner/name.git) and HTTPS
// (https://github.com/owner/name[.git]) forms.
func ParseOwnerRepo(url string) (string, string, error) {
	u := strings.TrimSuffix(url, ".git")
	if strings.HasPrefix(u, "git@") {
		i := strings.Index(u, ":")
		if i < 0 {
			return "", "", fmt.Errorf("can't parse ssh URL: %s", url)
		}
		parts := strings.SplitN(u[i+1:], "/", 2)
		if len(parts) != 2 {
			return "", "", fmt.Errorf("can't parse ssh URL path: %s", url)
		}
		return parts[0], parts[1], nil
	}
	for _, p := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/"} {
		if strings.HasPrefix(u, p) {
			path := strings.TrimPrefix(u, p)
			parts := strings.SplitN(path, "/", 2)
			if len(parts) != 2 {
				return "", "", fmt.Errorf("can't parse URL path: %s", url)
			}
			return parts[0], parts[1], nil
		}
	}
	return "", "", fmt.Errorf("not a recognized GitHub URL: %s", url)
}

func orDefault(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func envInt(getenv func(string) string, key string, dflt int) int {
	s := getenv(key)
	if s == "" {
		return dflt
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return dflt
	}
	return v
}

func envBool(getenv func(string) string, key string, dflt bool) bool {
	s := getenv(key)
	if s == "" {
		return dflt
	}
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return dflt
}
