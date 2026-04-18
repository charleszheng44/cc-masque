package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// compile-time assertion: *ghClient must satisfy Client.
var _ Client = (*ghClient)(nil)

type ghClient struct {
	ghBin string // defaults to "gh" via PATH
}

// NewGhClient returns a Client backed by the `gh` CLI and GitHub REST API.
// The caller must ensure `gh` is installed and authenticated (gh auth status).
func NewGhClient() Client { return &ghClient{ghBin: "gh"} }

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

type ghRefObj struct {
	SHA string `json:"sha"`
}
type ghRef struct {
	Ref    string   `json:"ref"`
	Object ghRefObj `json:"object"`
}

// CreateRef posts to /repos/<r>/git/refs with a JSON body. If GitHub
// returns 422 with "Reference already exists", we map that to ErrRefExists
// so callers can detect a lost atomic-claim race.
func (c *ghClient) CreateRef(ctx context.Context, r Repo, ref, sha string) error {
	body := fmt.Sprintf(`{"ref":%q,"sha":%q}`, ref, sha)
	cmd := exec.CommandContext(ctx, c.ghBin, "api", "-X", "POST",
		fmt.Sprintf("repos/%s/git/refs", r.String()),
		"--input", "-")
	cmd.Stdin = strings.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "Reference already exists") {
			return ErrRefExists
		}
		return fmt.Errorf("gh api create ref %s: %w\nstderr: %s", ref, err, stderr.String())
	}
	return nil
}

// DeleteRef is idempotent — "Reference does not exist" / "Not Found" is treated as success.
func (c *ghClient) DeleteRef(ctx context.Context, r Repo, ref string) error {
	trim := strings.TrimPrefix(ref, "refs/")
	_, err := c.runGh(ctx, "api", "-X", "DELETE",
		fmt.Sprintf("repos/%s/git/refs/%s", r.String(), trim))
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "Reference does not exist") ||
			strings.Contains(msg, "Not Found") {
			return nil
		}
		return err
	}
	return nil
}

func (c *ghClient) ListMatchingRefs(ctx context.Context, r Repo, prefix string) ([]Ref, error) {
	// GET /repos/{owner}/{repo}/git/matching-refs/<prefix>
	// Callers pass the prefix WITHOUT the leading "refs/" (GitHub's convention).
	out, err := c.runGh(ctx, "api",
		fmt.Sprintf("repos/%s/git/matching-refs/%s", r.String(), prefix))
	if err != nil {
		return nil, err
	}
	var raw []ghRef
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse matching-refs: %w", err)
	}
	refs := make([]Ref, 0, len(raw))
	for _, g := range raw {
		refs = append(refs, Ref{Name: g.Ref, SHA: g.Object.SHA})
	}
	return refs, nil
}

func (c *ghClient) GetRef(ctx context.Context, r Repo, ref string) (Ref, error) {
	trim := strings.TrimPrefix(ref, "refs/")
	out, err := c.runGh(ctx, "api",
		fmt.Sprintf("repos/%s/git/ref/%s", r.String(), trim))
	if err != nil {
		return Ref{}, err
	}
	var g ghRef
	if err := json.Unmarshal(out, &g); err != nil {
		return Ref{}, err
	}
	return Ref{Name: g.Ref, SHA: g.Object.SHA}, nil
}

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
			Number: g.Number, Title: g.Title, Body: g.Body,
			State:  strings.ToLower(g.State),
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
			Number: g.Number, Title: g.Title, Body: g.Body,
			State:  strings.ToLower(g.State),
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
		Number: g.Number, Title: g.Title, Body: g.Body,
		State:  strings.ToLower(g.State),
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

type ghReview struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	State       string `json:"state"`        // APPROVED, COMMENTED, ...
	SubmittedAt string `json:"submitted_at"` // RFC3339
}

func (c *ghClient) ListReviews(ctx context.Context, r Repo, pr int) ([]Review, error) {
	out, err := c.runGh(ctx, "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", r.String(), pr))
	if err != nil {
		return nil, err
	}
	var raw []ghReview
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	reviews := make([]Review, 0, len(raw))
	for _, g := range raw {
		t, _ := time.Parse(time.RFC3339, g.SubmittedAt)
		reviews = append(reviews, Review{Author: g.User.Login, State: g.State, At: t})
	}
	return reviews, nil
}

// (helper used by tests to override the binary)
func newGhClientWithBin(bin string) *ghClient { return &ghClient{ghBin: bin} }
