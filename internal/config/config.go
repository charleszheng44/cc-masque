package config

import (
	"errors"
	"time"
)

type Config struct {
	RepoDir string

	MaxImplementers int
	MaxReviewers    int

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

	AutoReview bool

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

		Image: "ghcr.io/charleszheng44/cc-crew:latest",
		Model: "claude-sonnet-4-6",
	}
}

// Validate returns an error if the config is not usable for `up`.
func (c Config) Validate() error {
	if c.RepoDir == "" {
		return errors.New("RepoDir is required")
	}
	if c.MaxImplementers < 0 || c.MaxReviewers < 0 {
		return errors.New("max-implementers and max-reviewers must be >= 0")
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
	if c.ClaudeOAuthToken == "" && c.AnthropicAPIKey == "" {
		return errors.New("one of CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY is required")
	}
	return nil
}
