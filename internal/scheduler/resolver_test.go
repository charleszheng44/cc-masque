package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

// fakeDockerRunner short-circuits docker.Run in resolver tests.
type fakeDockerRunner struct {
	exitCode int
	err      error
	called   bool
}

func (f *fakeDockerRunner) run(exitCode int, err error) func() (int, error) {
	f.exitCode = exitCode
	f.err = err
	return func() (int, error) {
		f.called = true
		return f.exitCode, f.err
	}
}

func TestResolverSuccessReQueuesReviewer(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[1] = &github.PullRequest{
		Number: 1, State: "open", HeadRefOid: "sha", HeadRefName: "claude/issue-1", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-resolve-conflict", "claude-resolving", "claude-reviewed"},
	}
	fake := &fakeDockerRunner{}
	lc := &Lifecycle{
		Kind: claim.KindResolver, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:                  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:           "claude-resolve-conflict",
		LockLabel:            "claude-resolving",
		ReviewLabel:          "claude-review",
		DoneLabel:            "claude-reviewed",
		ConflictBlockedLabel: "claude-conflict-blocked",
		MergeLabel:           "claude-merge",
	}
	lc.resolverDockerRunFn = fake.run(0, nil)
	lc.dispatchResolver(context.Background(), slog.Default(), 1)
	if !fake.called {
		t.Fatal("docker run not invoked")
	}
	labels := f.PRs[1].Labels
	if containsLabel(labels, "claude-resolve-conflict") || containsLabel(labels, "claude-resolving") {
		t.Errorf("resolver labels not cleared: %v", labels)
	}
	if !containsLabel(labels, "claude-review") {
		t.Errorf("expected claude-review re-added for reviewer pickup: %v", labels)
	}
	if containsLabel(labels, "claude-reviewed") {
		t.Errorf("claude-reviewed should be removed to re-trigger reviewer: %v", labels)
	}
}

func TestResolverFailureAppliesConflictBlocked(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[2] = &github.PullRequest{
		Number: 2, State: "open", HeadRefOid: "sha", HeadRefName: "claude/issue-2", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-resolve-conflict", "claude-resolving"},
	}
	fake := &fakeDockerRunner{}
	lc := &Lifecycle{
		Kind: claim.KindResolver, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:                  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:           "claude-resolve-conflict",
		LockLabel:            "claude-resolving",
		ReviewLabel:          "claude-review",
		DoneLabel:            "claude-reviewed",
		ConflictBlockedLabel: "claude-conflict-blocked",
		MergeLabel:           "claude-merge",
	}
	lc.resolverDockerRunFn = fake.run(1, nil)
	lc.dispatchResolver(context.Background(), slog.Default(), 2)
	labels := f.PRs[2].Labels
	if !containsLabel(labels, "claude-conflict-blocked") {
		t.Errorf("expected claude-conflict-blocked: %v", labels)
	}
	if containsLabel(labels, "claude-merge") {
		t.Errorf("claude-merge should be removed on terminal: %v", labels)
	}
	if containsLabel(labels, "claude-resolve-conflict") || containsLabel(labels, "claude-resolving") {
		t.Errorf("resolver queue/lock labels not cleared on failure: %v", labels)
	}
	if len(f.Comments[2]) == 0 {
		t.Error("expected escalation comment")
	}
}

// fakeGHGetPRErr wraps a github.Fake to make GetPR return a transport-style
// error, simulating a GitHub API blip before the container runs.
type fakeGHGetPRErr struct {
	github.Client
	err error
}

func (f *fakeGHGetPRErr) GetPR(ctx context.Context, repo github.Repo, number int) (github.PullRequest, error) {
	return github.PullRequest{}, f.err
}

// TestResolverPreContainerErrorIsSoftFail exercises the GetPR failure branch.
// A GitHub API blip is not the same as a container-observed merge conflict:
// the resolver must route through the generic failCleanup path so the claim
// releases and claude-resolving drops, but the PR must NOT gain
// claude-conflict-blocked or lose claude-merge, and the resolver must NOT
// post the "could not rebase" escalation comment.
func TestResolverPreContainerErrorIsSoftFail(t *testing.T) {
	inner := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	inner.PRs[3] = &github.PullRequest{
		Number: 3, State: "open", HeadRefOid: "sha", HeadRefName: "claude/issue-3", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-resolve-conflict", "claude-resolving"},
	}
	gh := &fakeGHGetPRErr{Client: inner, err: errors.New("api blip")}
	lc := &Lifecycle{
		Kind: claim.KindResolver, Claimer: claim.New(gh, repo), GH: gh, Repo: repo,
		Log:                  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:           "claude-resolve-conflict",
		LockLabel:            "claude-resolving",
		ReviewLabel:          "claude-review",
		DoneLabel:            "claude-reviewed",
		ConflictBlockedLabel: "claude-conflict-blocked",
		MergeLabel:           "claude-merge",
	}
	lc.dispatchResolver(context.Background(), slog.Default(), 3)

	labels := inner.PRs[3].Labels
	if containsLabel(labels, "claude-conflict-blocked") {
		t.Errorf("pre-container error must not stamp claude-conflict-blocked: %v", labels)
	}
	if !containsLabel(labels, "claude-merge") {
		t.Errorf("pre-container error must not remove claude-merge: %v", labels)
	}
	if !containsLabel(labels, "claude-resolve-conflict") {
		t.Errorf("queue label must remain so next tick retries: %v", labels)
	}
	if containsLabel(labels, "claude-resolving") {
		t.Errorf("lock label must be cleared on soft-fail: %v", labels)
	}
	if len(inner.Comments[3]) != 0 {
		t.Errorf("pre-container error must not post escalation comment: %v", inner.Comments[3])
	}
}

// TestBuildResolverRunSpec pins the env, labels, name, and mounts on the
// returned RunSpec. The dispatcher tests short-circuit via resolverDockerRunFn
// so without this, the surface that actually distinguishes the resolver
// container from the reviewer/addresser ones would be uncovered.
func TestBuildResolverRunSpec(t *testing.T) {
	repoDir := t.TempDir()
	lc := &Lifecycle{
		Repo:            github.Repo{Owner: "My-Org", Name: "co.ol/repo"},
		WT:              &worktree.Manager{RepoDir: repoDir},
		Image:           "cc-crew-impl:latest",
		Model:           "claude-opus-4",
		MaxTurns:        42,
		RoleGHToken:     "gh-token",
		ClaudeOAuth:     "oauth-token",
		AnthropicAPIKey: "anthropic-key",
		GitName:         "cc-crew",
		GitEmail:        "cc-crew@example.com",
	}

	spec := lc.buildResolverRunSpec(7, "main", "claude/issue-7", "/tmp/wt")

	wantName := "cc-crew-resolve-My-Org-co-ol-repo-7"
	if spec.Name != wantName {
		t.Errorf("name = %q, want %q", spec.Name, wantName)
	}
	if spec.Image != "cc-crew-impl:latest" {
		t.Errorf("image = %q", spec.Image)
	}

	wantLabels := map[string]string{
		"cc-crew.repo": "My-Org/co.ol/repo",
		"cc-crew.role": "resolver",
		"cc-crew.pr":   "7",
	}
	for k, v := range wantLabels {
		if got := spec.Labels[k]; got != v {
			t.Errorf("label %s = %q, want %q", k, got, v)
		}
	}

	wantEnv := map[string]string{
		"CC_ROLE":                 "resolver",
		"CC_MODEL":                "claude-opus-4",
		"CC_REPO":                 "My-Org/co.ol/repo",
		"CC_PR_NUM":               "7",
		"CC_BASE_BRANCH":          "main",
		"CC_HEAD_BRANCH":          "claude/issue-7",
		"CC_MAX_TURNS":            "42",
		"GH_TOKEN":                "gh-token",
		"CLAUDE_CODE_OAUTH_TOKEN": "oauth-token",
		"ANTHROPIC_API_KEY":       "anthropic-key",
		"GIT_AUTHOR_NAME":         "cc-crew",
		"GIT_AUTHOR_EMAIL":        "cc-crew@example.com",
		"GIT_COMMITTER_NAME":      "cc-crew",
		"GIT_COMMITTER_EMAIL":     "cc-crew@example.com",
		"IS_SANDBOX":              "1",
	}
	for k, v := range wantEnv {
		if got := spec.Env[k]; got != v {
			t.Errorf("env %s = %q, want %q", k, got, v)
		}
	}

	if len(spec.Mounts) != 2 {
		t.Fatalf("mounts = %d, want 2", len(spec.Mounts))
	}
	if spec.Mounts[0].HostPath != "/tmp/wt" || spec.Mounts[0].ContainerPath != "/workspace" {
		t.Errorf("workspace mount = %+v", spec.Mounts[0])
	}
	wantGit := filepath.Join(repoDir, ".git")
	if spec.Mounts[1].HostPath != wantGit || spec.Mounts[1].ContainerPath != wantGit {
		t.Errorf("git mount = %+v, want host/container = %q", spec.Mounts[1], wantGit)
	}
}

// TestBuildResolverRunSpecOmitsMaxTurnsWhenZero verifies that CC_MAX_TURNS is
// only set when the operator opted in; absent means "unlimited" (matches the
// reviewer/implementer run specs).
func TestBuildResolverRunSpecOmitsMaxTurnsWhenZero(t *testing.T) {
	lc := &Lifecycle{
		Repo: github.Repo{Owner: "o", Name: "n"},
		WT:   &worktree.Manager{RepoDir: t.TempDir()},
	}
	spec := lc.buildResolverRunSpec(1, "main", "claude/issue-1", "/tmp/wt")
	if _, ok := spec.Env["CC_MAX_TURNS"]; ok {
		t.Errorf("CC_MAX_TURNS must be unset when MaxTurns==0; got %q", spec.Env["CC_MAX_TURNS"])
	}
}
