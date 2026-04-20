package github

import (
	"context"
	"errors"
)

// ErrRefExists is returned by CreateRef when GitHub responds with 422
// "Reference already exists" — the caller lost the atomic claim race.
var ErrRefExists = errors.New("github: ref already exists")

// ErrLabelExists is returned by CreateLabel when GitHub responds with 422
// "already_exists". Signals the caller that the label is already present
// and no action is needed.
var ErrLabelExists = errors.New("github: label already exists")

// ErrMergeConflict is returned by MergePR when GitHub rejects the merge
// because the PR has unresolved conflicts against the base branch.
var ErrMergeConflict = errors.New("github: merge conflict")

// MergeMethod selects the strategy used when merging a PR.
type MergeMethod string

const (
	MergeMethodRebase MergeMethod = "rebase"
	MergeMethodSquash MergeMethod = "squash"
	MergeMethodMerge  MergeMethod = "merge"
)

// UpdateMethod selects the strategy used when updating a PR branch
// against its base.
type UpdateMethod string

const (
	UpdateMethodRebase UpdateMethod = "rebase"
	UpdateMethodMerge  UpdateMethod = "merge"
)

// CheckRun is a GitHub check run attached to a commit SHA.
type CheckRun struct {
	Name       string // display name, e.g. "build"
	Status     string // queued | in_progress | completed | waiting | pending
	Conclusion string // success | failure | neutral | cancelled | skipped | timed_out | action_required | stale | startup_failure | "" (if not completed)
}

// Client is the surface area the rest of cc-crew depends on.
// Implementations: *ghClient (production), *FakeClient (tests).
type Client interface {
	// Authentication / identity
	CurrentUser(ctx context.Context) (string, error) // gh api user -q .login

	// Repo metadata
	DefaultBranch(ctx context.Context, r Repo) (string, error)

	// Issues & PRs
	ListIssues(ctx context.Context, r Repo, withLabels []string, withoutLabels []string) ([]Issue, error)
	ListPRs(ctx context.Context, r Repo, withLabels []string, withoutLabels []string) ([]PullRequest, error)
	GetPR(ctx context.Context, r Repo, number int) (PullRequest, error)

	// Labels
	AddLabel(ctx context.Context, r Repo, issueOrPRNumber int, label string) error
	RemoveLabel(ctx context.Context, r Repo, issueOrPRNumber int, label string) error
	CreateLabel(ctx context.Context, r Repo, name, color, description string) error // returns ErrLabelExists on 422 already_exists

	// Refs (via git/refs API)
	CreateRef(ctx context.Context, r Repo, ref string, sha string) error // returns ErrRefExists on 422
	DeleteRef(ctx context.Context, r Repo, ref string) error             // 422 on already-deleted is treated as success
	ListMatchingRefs(ctx context.Context, r Repo, prefix string) ([]Ref, error)
	GetRef(ctx context.Context, r Repo, ref string) (Ref, error)

	// Reviews
	ListReviews(ctx context.Context, r Repo, prNumber int) ([]Review, error)

	// Merge / branch updates
	MergePR(ctx context.Context, r Repo, number int, expectedHeadSha string, method MergeMethod, deleteBranch bool) error // returns ErrMergeConflict on conflict
	UpdateBranch(ctx context.Context, r Repo, number int, expectedHeadSha string, method UpdateMethod) error

	// Status checks
	GetCheckRuns(ctx context.Context, r Repo, sha string) ([]CheckRun, error)

	// Comments
	CreateComment(ctx context.Context, r Repo, issueOrPRNumber int, body string) error

	// Dependencies
	CountOpenBlockers(ctx context.Context, r Repo, issueNumber int) (int, error)

	// PR create is done by Claude inside containers; not here.
}
