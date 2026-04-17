package github

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type ghClient struct {
	ghBin string // defaults to "gh" via PATH
}

// NewGhClient returns a gh-backed client. Methods are added incrementally
// across Tasks 1.3–1.6; once Task 1.6 is complete, *ghClient satisfies
// github.Client. Callers that need the interface should convert at that point.
func NewGhClient() *ghClient { return &ghClient{ghBin: "gh"} }

// runGh runs `gh <args>` and returns stdout. Non-zero exit is an error
// whose message includes stderr for debugging.
func (c *ghClient) runGh(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.ghBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %w\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (c *ghClient) CurrentUser(ctx context.Context) (string, error) {
	out, err := c.runGh(ctx, "api", "user", "-q", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *ghClient) DefaultBranch(ctx context.Context, r Repo) (string, error) {
	out, err := c.runGh(ctx, "api", "repos/"+r.String(), "-q", ".default_branch")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// remaining methods (ListIssues, ListPRs, GetPR, AddLabel, RemoveLabel,
// CreateRef, DeleteRef, ListMatchingRefs, GetRef, ListReviews) are
// implemented in Tasks 1.4–1.6.

// (helper used by tests to override the binary)
func newGhClientWithBin(bin string) *ghClient { return &ghClient{ghBin: bin} }
