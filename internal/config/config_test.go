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
