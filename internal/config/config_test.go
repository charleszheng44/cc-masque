package config

import (
	"strings"
	"testing"
)

func baseValid() Config {
	c := Defaults()
	c.RepoDir = "/tmp/repo"
	c.OrchestratorGHToken = "t"
	c.ImplementerGHToken = "t"
	c.ReviewerGHToken = "t"
	c.ClaudeOAuthToken = "t"
	c.ImplementerGitName = "impl"
	c.ImplementerGitEmail = "i@x"
	c.ReviewerGitName = "rev"
	c.ReviewerGitEmail = "r@x"
	return c
}

func TestValidateHappyPath(t *testing.T) {
	if err := baseValid().Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateNoTokens(t *testing.T) {
	c := baseValid()
	c.OrchestratorGHToken = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Fatalf("expected GH_TOKEN error, got %v", err)
	}
}

func TestValidateNoClaudeCreds(t *testing.T) {
	c := baseValid()
	c.ClaudeOAuthToken = ""
	c.AnthropicAPIKey = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("expected claude creds error, got %v", err)
	}
}

func TestValidateAllowsImplementerOnly(t *testing.T) {
	c := baseValid()
	c.MaxReviewers = 0
	c.MaxMergers = 0
	c.ReviewerGHToken = ""
	c.ReviewerGitName = ""
	c.ReviewerGitEmail = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("should allow implementer-only: %v", err)
	}
}

func TestDefaultsIncludeContinuousAndAddressLabels(t *testing.T) {
	d := Defaults()
	if d.MaxCycles != 3 {
		t.Fatalf("MaxCycles = %d, want 3", d.MaxCycles)
	}
	if !d.Continuous {
		t.Fatal("Continuous = false, want true")
	}
	if d.AddressLabel != "claude-address" ||
		d.AddressingLabel != "claude-addressing" ||
		d.AddressedLabel != "claude-addressed" {
		t.Fatalf("address labels: %q %q %q",
			d.AddressLabel, d.AddressingLabel, d.AddressedLabel)
	}
}

func TestDefaultsIncludeMergerLabelsAndCap(t *testing.T) {
	d := Defaults()
	if d.MaxMergers != 2 {
		t.Fatalf("MaxMergers = %d, want 2", d.MaxMergers)
	}
	if d.MergeLabel != "claude-merge" ||
		d.MergingLabel != "claude-merging" ||
		d.ResolveConflictLabel != "claude-resolve-conflict" ||
		d.ResolvingLabel != "claude-resolving" ||
		d.ConflictBlockedLabel != "claude-conflict-blocked" {
		t.Fatalf("merger labels: %q %q %q %q %q",
			d.MergeLabel, d.MergingLabel, d.ResolveConflictLabel,
			d.ResolvingLabel, d.ConflictBlockedLabel)
	}
}

func TestValidateRequiresReviewerTokenWhenMergerEnabled(t *testing.T) {
	c := baseValid()
	// Disable reviewer so the reviewer-branch error doesn't fire first.
	c.MaxReviewers = 0
	c.ReviewerGitName = ""
	c.ReviewerGitEmail = ""
	c.ReviewerGHToken = ""
	// Merger enabled by default in baseValid via Defaults().
	if c.MaxMergers <= 0 {
		t.Fatalf("precondition: MaxMergers should be > 0 by default, got %d", c.MaxMergers)
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "GH_TOKEN_REVIEWER") {
		t.Fatalf("expected GH_TOKEN_REVIEWER error, got %v", err)
	}
}

func TestDefaultsIncludeQuarantine(t *testing.T) {
	d := Defaults()
	if d.QuarantineLabel != "claude-failed" {
		t.Fatalf("QuarantineLabel = %q, want claude-failed", d.QuarantineLabel)
	}
	if d.QuarantineThreshold != 3 {
		t.Fatalf("QuarantineThreshold = %d, want 3", d.QuarantineThreshold)
	}
}

func TestValidateAllowsMergerDisabled(t *testing.T) {
	c := baseValid()
	c.MaxMergers = 0
	c.MaxReviewers = 0
	c.ReviewerGitName = ""
	c.ReviewerGitEmail = ""
	c.ReviewerGHToken = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("disabling merger and reviewer should validate: %v", err)
	}
}
