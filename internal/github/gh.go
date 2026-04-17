package github

import (
	"bytes"
	"context"
	"encoding/json"
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

// remaining methods (CreateRef, DeleteRef, ListMatchingRefs, GetRef,
// ListReviews) are implemented in Tasks 1.5–1.6.

// ghIssue/ghPR are JSON shapes returned by `gh issue list --json ...`.
// We translate from gh's label-array-of-objects to our flat []string.
type ghLabel struct {
	Name string `json:"name"`
}
type ghIssue struct {
	Number int       `json:"number"`
	Title  string    `json:"title"`
	Body   string    `json:"body"`
	State  string    `json:"state"`
	Labels []ghLabel `json:"labels"`
}
type ghPR struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	Labels      []ghLabel `json:"labels"`
	HeadRefOid  string    `json:"headRefOid"`
	HeadRefName string    `json:"headRefName"`
	BaseRefName string    `json:"baseRefName"`
}

func flattenLabels(l []ghLabel) []string {
	out := make([]string, 0, len(l))
	for _, x := range l {
		out = append(out, x.Name)
	}
	return out
}

func (c *ghClient) ListIssues(ctx context.Context, r Repo, with, without []string) ([]Issue, error) {
	args := []string{"issue", "list", "-R", r.String(), "--state", "open",
		"--json", "number,title,body,state,labels", "--limit", "200"}
	for _, l := range with {
		args = append(args, "--label", l)
	}
	out, err := c.runGh(ctx, args...)
	if err != nil {
		return nil, err
	}
	var raw []ghIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse issues: %w", err)
	}
	issues := make([]Issue, 0, len(raw))
OUTER:
	for _, g := range raw {
		labels := flattenLabels(g.Labels)
		for _, bad := range without {
			for _, lb := range labels {
				if lb == bad {
					continue OUTER
				}
			}
		}
		issues = append(issues, Issue{
			Number: g.Number, Title: g.Title, Body: g.Body, State: g.State,
			Labels: labels,
		})
	}
	return issues, nil
}

func (c *ghClient) ListPRs(ctx context.Context, r Repo, with, without []string) ([]PullRequest, error) {
	args := []string{"pr", "list", "-R", r.String(), "--state", "open",
		"--json", "number,title,body,state,labels,headRefOid,headRefName,baseRefName",
		"--limit", "200"}
	for _, l := range with {
		args = append(args, "--label", l)
	}
	out, err := c.runGh(ctx, args...)
	if err != nil {
		return nil, err
	}
	var raw []ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse prs: %w", err)
	}
	prs := make([]PullRequest, 0, len(raw))
OUTER:
	for _, g := range raw {
		labels := flattenLabels(g.Labels)
		for _, bad := range without {
			for _, lb := range labels {
				if lb == bad {
					continue OUTER
				}
			}
		}
		prs = append(prs, PullRequest{
			Number: g.Number, Title: g.Title, Body: g.Body, State: g.State,
			Labels: labels, HeadRefOid: g.HeadRefOid,
			HeadRefName: g.HeadRefName, BaseRefName: g.BaseRefName,
		})
	}
	return prs, nil
}

func (c *ghClient) GetPR(ctx context.Context, r Repo, n int) (PullRequest, error) {
	out, err := c.runGh(ctx, "pr", "view", fmt.Sprint(n), "-R", r.String(),
		"--json", "number,title,body,state,labels,headRefOid,headRefName,baseRefName")
	if err != nil {
		return PullRequest{}, err
	}
	var g ghPR
	if err := json.Unmarshal(out, &g); err != nil {
		return PullRequest{}, err
	}
	return PullRequest{
		Number: g.Number, Title: g.Title, Body: g.Body, State: g.State,
		Labels: flattenLabels(g.Labels), HeadRefOid: g.HeadRefOid,
		HeadRefName: g.HeadRefName, BaseRefName: g.BaseRefName,
	}, nil
}

func (c *ghClient) AddLabel(ctx context.Context, r Repo, n int, label string) error {
	// Works for both issues and PRs.
	_, err := c.runGh(ctx, "issue", "edit", fmt.Sprint(n), "-R", r.String(), "--add-label", label)
	return err
}

func (c *ghClient) RemoveLabel(ctx context.Context, r Repo, n int, label string) error {
	_, err := c.runGh(ctx, "issue", "edit", fmt.Sprint(n), "-R", r.String(), "--remove-label", label)
	return err
}

// (helper used by tests to override the binary)
func newGhClientWithBin(bin string) *ghClient { return &ghClient{ghBin: bin} }
