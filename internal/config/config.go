package config

import (
	"errors"
	"time"
)

type Config struct {
	RepoDir string

	MaxImplementers int
	MaxReviewers    int
	MaxMergers      int

	PollInterval      time.Duration
	ReclaimAfter      time.Duration
	ImplTaskTimeout   time.Duration
	ReviewTaskTimeout time.Duration

	TaskLabel       string
	ProcessingLabel string
	DoneLabel       string
	ReviewLabel     string
	ReviewingLabel  string
	ReviewedLabel   string

	// Continuous addressing feature (spec 2026-04-17).
	AddressLabel    string
	AddressingLabel string
	AddressedLabel  string
	MaxCycles       int
	Continuous      bool

	// Auto-merger feature (spec 2026-04-20).
	MergeLabel           string
	MergingLabel         string
	ResolveConflictLabel string
	ResolvingLabel       string
	ConflictBlockedLabel string

	AutoReview bool

	ImplMaxTurns   int
	ReviewMaxTurns int

	Image string
	Model string

	ImplementerGHToken  string
	ReviewerGHToken     string
	OrchestratorGHToken string
	ClaudeOAuthToken    string
	AnthropicAPIKey     string
	ImplementerGitName  string
	ImplementerGitEmail string
	ReviewerGitName     string
	ReviewerGitEmail    string

	BaseBranch string
}

func Defaults() Config {
	return Config{
		MaxImplementers:   3,
		MaxReviewers:      2,
		MaxMergers:        2,
		PollInterval:      60 * time.Second,
		ReclaimAfter:      30 * time.Minute,
		ImplTaskTimeout:   60 * time.Minute,
		ReviewTaskTimeout: 15 * time.Minute,

		TaskLabel:       "claude-task",
		ProcessingLabel: "claude-processing",
		DoneLabel:       "claude-done",
		ReviewLabel:     "claude-review",
		ReviewingLabel:  "claude-reviewing",
		ReviewedLabel:   "claude-reviewed",

		AddressLabel:    "claude-address",
		AddressingLabel: "claude-addressing",
		AddressedLabel:  "claude-addressed",
		MaxCycles:       3,
		Continuous:      true,

		MergeLabel:           "claude-merge",
		MergingLabel:         "claude-merging",
		ResolveConflictLabel: "claude-resolve-conflict",
		ResolvingLabel:       "claude-resolving",
		ConflictBlockedLabel: "claude-conflict-blocked",

		// 0 = unlimited; only enforced when the user explicitly sets a cap.
		ImplMaxTurns:   0,
		ReviewMaxTurns: 0,

		Image: "ghcr.io/charleszheng44/cc-crew:latest",
		Model: "claude-opus-4-7",
	}
}

// Validate returns an error if the config is not usable for `up`.
func (c Config) Validate() error {
	if c.RepoDir == "" {
		return errors.New("RepoDir is required")
	}
	if c.MaxImplementers < 0 || c.MaxReviewers < 0 || c.MaxMergers < 0 {
		return errors.New("max-implementers, max-reviewers and max-mergers must be >= 0")
	}
	if c.MaxImplementers == 0 && c.MaxReviewers == 0 {
		return errors.New("at least one of max-implementers/max-reviewers must be > 0")
	}
	if c.PollInterval < 5*time.Second {
		return errors.New("poll-seconds must be >= 5")
	}
	if c.OrchestratorGHToken == "" {
		return errors.New("GH_TOKEN (or per-persona equivalent) is required")
	}
	if c.MaxImplementers > 0 {
		if c.ImplementerGHToken == "" {
			return errors.New("GH_TOKEN_IMPLEMENTER or GH_TOKEN is required when implementer is enabled")
		}
		if c.ImplementerGitName == "" || c.ImplementerGitEmail == "" {
			return errors.New("IMPLEMENTER_GIT_NAME and IMPLEMENTER_GIT_EMAIL are required when implementer is enabled")
		}
	}
	if c.MaxReviewers > 0 {
		if c.ReviewerGHToken == "" {
			return errors.New("GH_TOKEN_REVIEWER or GH_TOKEN is required when reviewer is enabled")
		}
		if c.ReviewerGitName == "" || c.ReviewerGitEmail == "" {
			return errors.New("REVIEWER_GIT_NAME and REVIEWER_GIT_EMAIL are required when reviewer is enabled")
		}
	}
	if c.MaxMergers > 0 {
		if c.ReviewerGHToken == "" {
			return errors.New("GH_TOKEN_REVIEWER is required when merger is enabled")
		}
	}
	if c.ClaudeOAuthToken == "" && c.AnthropicAPIKey == "" {
		return errors.New("one of CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY is required")
	}
	return nil
}
