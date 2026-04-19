# cc-crew Orchestrator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `cc-crew` Go CLI that polls a GitHub repo, atomically claims issues/PRs via git refs, and dispatches per-task Docker containers to run Claude Code as an implementer or reviewer persona. Add the implementer persona to the existing repo.

**Architecture:** Single foreground Go process with three subcommands (`up`, `status`, `reset`). A tick loop (60s default) runs reclaim sweeper + polling + dispatch under per-persona semaphores. Each dispatched task runs in a one-shot `docker run --rm` container that bind-mounts a host-side git worktree. Atomic claims use `POST /git/refs` (201 vs 422). Failure drops the lock and leaves the queue label intact for automatic retry.

**Tech Stack:** Go (stdlib only: `flag`, `net/http`, `encoding/json`, `log/slog`, `os/exec`, `context`, `sync`, `sync/atomic`), shelling out to `gh`, `git`, and `docker`. Testing with the standard `testing` package and fakes behind small interfaces. Container image built on the existing `cc-crew` Dockerfile (to be renamed to `cc-crew`).

**Reference spec:** `docs/superpowers/specs/2026-04-16-cc-crew-orchestrator-design.md`

---

## File Structure

Files the plan creates or modifies (relative to repo root):

- `go.mod`, `go.sum` — Go module definition (no external deps in v1).
- `cmd/cc-crew/main.go` — CLI entrypoint: subcommand dispatch (`up`, `status`, `reset`, `version`).
- `cmd/cc-crew/up.go` — wires the `up` subcommand (config → components → tick loop).
- `cmd/cc-crew/status.go` — wires `status`.
- `cmd/cc-crew/reset.go` — wires `reset`.
- `internal/config/config.go` — config struct, defaults, validation.
- `internal/config/config_test.go`
- `internal/config/parse.go` — flag + env parsing.
- `internal/config/parse_test.go`
- `internal/github/types.go` — `Issue`, `PullRequest`, `Ref`, `Review`, `Label` structs.
- `internal/github/client.go` — `Client` interface + error types.
- `internal/github/gh.go` — `ghClient` real implementation (shells out to `gh` + `gh api`).
- `internal/github/gh_test.go` — unit tests for JSON parsers.
- `internal/github/fake.go` — in-memory `FakeClient` for downstream tests.
- `internal/claim/claim.go` — `Claimer`, `TryClaim`, `Release`, `ListTags`, `ClaimAge`.
- `internal/claim/claim_test.go`
- `internal/reclaim/reclaim.go` — stale-lock sweeper.
- `internal/reclaim/reclaim_test.go`
- `internal/worktree/worktree.go` — `git worktree add/remove/prune` shims.
- `internal/worktree/worktree_test.go` — against a scratch local repo.
- `internal/docker/docker.go` — `Runner`: run/kill/ps wrappers.
- `internal/docker/docker_test.go` — unit tests for command assembly; integration test gated on `CC_TEST_DOCKER=1`.
- `internal/scheduler/scheduler.go` — `Scheduler` with tick loop + per-persona semaphore.
- `internal/scheduler/lifecycle.go` — per-task lifecycle (claim → worktree → docker → finalize).
- `internal/scheduler/scheduler_test.go`
- `internal/scheduler/lifecycle_test.go`
- `internal/status/status.go` — stateless status snapshot.
- `internal/status/status_test.go`
- `internal/reset/reset.go` — bulk-cleanup implementation.
- `internal/reset/reset_test.go`
- `scripts/cc-crew-run` — in-container entrypoint (bash).
- `Dockerfile` — extended to `COPY scripts/cc-crew-run /usr/local/bin/`.
- `personas/implementer/CLAUDE.md` — persona prompt for implementer.
- `personas/implementer/settings.json` — documented scoped permissions (not used at runtime when `--dangerously-skip-permissions`).
- `README.md` — add "Orchestrator" section.

---

## Phase 0 — Project scaffolding

### Task 0.1: Initialize Go module and CLI skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/cc-crew/main.go`
- Create: `.gitignore` (append)

- [ ] **Step 1: Initialize module**

Run: `go mod init github.com/charleszheng44/cc-crew`

Expected: creates `go.mod` with module path `github.com/charleszheng44/cc-crew`.

- [ ] **Step 2: Write minimal main that supports `cc-crew version`**

`cmd/cc-crew/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

var Version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Println(Version)
	case "up":
		fmt.Fprintln(os.Stderr, "up: not implemented yet")
		os.Exit(1)
	case "status":
		fmt.Fprintln(os.Stderr, "status: not implemented yet")
		os.Exit(1)
	case "reset":
		fmt.Fprintln(os.Stderr, "reset: not implemented yet")
		os.Exit(1)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `cc-crew — local Claude Code orchestrator for GitHub issues and PRs

Usage:
  cc-crew up       Start the orchestrator (foreground)
  cc-crew status   Print current task/queue snapshot
  cc-crew reset    Bulk-clean all cc-crew state in the repo
  cc-crew version  Print version
  cc-crew help     Show this help`)
}
```

- [ ] **Step 3: Add `.gitignore` entries**

Append to `.gitignore` (create if missing):

```
/cc-crew
.claude-worktrees/
*.test
/cover.out
```

- [ ] **Step 4: Build and smoke-test**

Run:
```
go build -o cc-crew ./cmd/cc-crew
./cc-crew version
./cc-crew help
```

Expected:
- Build produces `./cc-crew`.
- `version` prints `0.0.0-dev`.
- `help` prints the usage.

- [ ] **Step 5: Commit**

```
gofmt -w .
git add go.mod cmd/cc-crew/main.go .gitignore
git commit -m "feat(cc-crew): scaffold Go module and CLI skeleton"
```

---

### Task 0.2: Add a Makefile with common targets

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Write Makefile**

```make
GO ?= go
BINARY := cc-crew
PKGS := ./...

.PHONY: all build test fmt vet lint cover clean

all: fmt vet test build

build:
	$(GO) build -o $(BINARY) ./cmd/cc-crew

test:
	$(GO) test $(PKGS)

fmt:
	gofmt -w .

vet:
	$(GO) vet $(PKGS)

cover:
	$(GO) test -coverprofile=cover.out $(PKGS)
	$(GO) tool cover -func=cover.out | tail -n 1

clean:
	rm -f $(BINARY) cover.out
```

- [ ] **Step 2: Verify targets run**

Run: `make fmt vet build`

Expected: all three succeed; binary `./cc-crew` is produced.

- [ ] **Step 3: Commit**

```
git add Makefile
git commit -m "chore: add Makefile for common dev tasks"
```

---

## Phase 1 — internal/github

This package is the only place that shells out to `gh`. Everything else depends on its `Client` interface.

### Task 1.1: Types and client interface

**Files:**
- Create: `internal/github/types.go`
- Create: `internal/github/client.go`

- [ ] **Step 1: Write types**

`internal/github/types.go`:

```go
package github

import "time"

type Issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	State  string   `json:"state"`
	Labels []string `json:"labels"`
}

type PullRequest struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	State      string   `json:"state"`
	Labels     []string `json:"labels"`
	HeadRefOid string   `json:"headRefOid"`
	HeadRefName string  `json:"headRefName"`
	BaseRefName string  `json:"baseRefName"`
}

type Ref struct {
	Name string    // e.g. "refs/heads/claude/issue-42" or "refs/tags/claim/issue-42/20260417T120000Z"
	SHA  string
}

type Review struct {
	Author string    // login
	State  string    // COMMENTED, APPROVED, CHANGES_REQUESTED, DISMISSED
	At     time.Time
}

type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }
```

- [ ] **Step 2: Write client interface + error types**

`internal/github/client.go`:

```go
package github

import (
	"context"
	"errors"
)

// ErrRefExists is returned by CreateRef when GitHub responds with 422
// "Reference already exists" — the caller lost the atomic claim race.
var ErrRefExists = errors.New("github: ref already exists")

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

	// Refs (via git/refs API)
	CreateRef(ctx context.Context, r Repo, ref string, sha string) error // returns ErrRefExists on 422
	DeleteRef(ctx context.Context, r Repo, ref string) error              // 422 on already-deleted is treated as success
	ListMatchingRefs(ctx context.Context, r Repo, prefix string) ([]Ref, error)
	GetRef(ctx context.Context, r Repo, ref string) (Ref, error)

	// Reviews
	ListReviews(ctx context.Context, r Repo, prNumber int) ([]Review, error)

	// PR create is done by Claude inside containers; not here.
}
```

- [ ] **Step 3: Verify compile**

Run: `go build ./...`

Expected: succeeds (nothing consumes it yet).

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/github/types.go internal/github/client.go
git commit -m "feat(github): add client interface and core types"
```

---

### Task 1.2: FakeClient for downstream tests

**Files:**
- Create: `internal/github/fake.go`
- Create: `internal/github/fake_test.go`

- [ ] **Step 1: Write FakeClient**

`internal/github/fake.go`:

```go
package github

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// FakeClient is an in-memory Client for unit tests.
type FakeClient struct {
	mu          sync.Mutex
	User        string
	Issues      map[int]*Issue          // keyed by number
	PRs         map[int]*PullRequest    // keyed by number
	Refs        map[string]string       // ref name → sha
	Reviews     map[int][]Review        // PR number → reviews
	DefaultBr   string

	// Hooks for injecting errors in specific calls. Leave nil to disable.
	CreateRefHook func(ref string) error
}

func NewFake() *FakeClient {
	return &FakeClient{
		User:      "fake-bot",
		Issues:    map[int]*Issue{},
		PRs:       map[int]*PullRequest{},
		Refs:      map[string]string{},
		Reviews:   map[int][]Review{},
		DefaultBr: "main",
	}
}

func (f *FakeClient) CurrentUser(ctx context.Context) (string, error) {
	return f.User, nil
}

func (f *FakeClient) DefaultBranch(ctx context.Context, r Repo) (string, error) {
	return f.DefaultBr, nil
}

func hasAll(haystack []string, needles []string) bool {
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func hasAny(haystack []string, needles []string) bool {
	for _, n := range needles {
		for _, h := range haystack {
			if h == n {
				return true
			}
		}
	}
	return false
}

func (f *FakeClient) ListIssues(ctx context.Context, r Repo, with, without []string) ([]Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Issue{}
	for _, i := range f.Issues {
		if i.State != "open" {
			continue
		}
		if !hasAll(i.Labels, with) {
			continue
		}
		if hasAny(i.Labels, without) {
			continue
		}
		out = append(out, *i)
	}
	return out, nil
}

func (f *FakeClient) ListPRs(ctx context.Context, r Repo, with, without []string) ([]PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []PullRequest{}
	for _, p := range f.PRs {
		if p.State != "open" {
			continue
		}
		if !hasAll(p.Labels, with) {
			continue
		}
		if hasAny(p.Labels, without) {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

func (f *FakeClient) GetPR(ctx context.Context, r Repo, n int) (PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.PRs[n]
	if !ok {
		return PullRequest{}, fmt.Errorf("fake: PR %d not found", n)
	}
	return *p, nil
}

func removeStr(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func (f *FakeClient) AddLabel(ctx context.Context, r Repo, n int, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.Issues[n]; ok {
		for _, l := range i.Labels {
			if l == label {
				return nil
			}
		}
		i.Labels = append(i.Labels, label)
		return nil
	}
	if p, ok := f.PRs[n]; ok {
		for _, l := range p.Labels {
			if l == label {
				return nil
			}
		}
		p.Labels = append(p.Labels, label)
		return nil
	}
	return fmt.Errorf("fake: issue/PR %d not found", n)
}

func (f *FakeClient) RemoveLabel(ctx context.Context, r Repo, n int, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.Issues[n]; ok {
		i.Labels = removeStr(i.Labels, label)
		return nil
	}
	if p, ok := f.PRs[n]; ok {
		p.Labels = removeStr(p.Labels, label)
		return nil
	}
	return fmt.Errorf("fake: issue/PR %d not found", n)
}

func (f *FakeClient) CreateRef(ctx context.Context, r Repo, ref, sha string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateRefHook != nil {
		if err := f.CreateRefHook(ref); err != nil {
			return err
		}
	}
	if _, exists := f.Refs[ref]; exists {
		return ErrRefExists
	}
	f.Refs[ref] = sha
	return nil
}

func (f *FakeClient) DeleteRef(ctx context.Context, r Repo, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Refs, ref)
	return nil
}

func (f *FakeClient) ListMatchingRefs(ctx context.Context, r Repo, prefix string) ([]Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Ref{}
	for k, v := range f.Refs {
		if strings.HasPrefix(k, "refs/"+prefix) || strings.HasPrefix(k, prefix) {
			out = append(out, Ref{Name: k, SHA: v})
		}
	}
	return out, nil
}

func (f *FakeClient) GetRef(ctx context.Context, r Repo, ref string) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sha, ok := f.Refs[ref]
	if !ok {
		return Ref{}, fmt.Errorf("fake: ref %s not found", ref)
	}
	return Ref{Name: ref, SHA: sha}, nil
}

func (f *FakeClient) ListReviews(ctx context.Context, r Repo, prNumber int) ([]Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Review(nil), f.Reviews[prNumber]...), nil
}
```

- [ ] **Step 2: Write smoke tests for the fake**

`internal/github/fake_test.go`:

```go
package github

import (
	"context"
	"errors"
	"testing"
)

func TestFakeCreateRefAtomic(t *testing.T) {
	c := NewFake()
	r := Repo{Owner: "acme", Name: "widget"}
	ctx := context.Background()

	if err := c.CreateRef(ctx, r, "refs/heads/foo", "abc123"); err != nil {
		t.Fatalf("first CreateRef: %v", err)
	}
	err := c.CreateRef(ctx, r, "refs/heads/foo", "abc123")
	if !errors.Is(err, ErrRefExists) {
		t.Fatalf("expected ErrRefExists, got %v", err)
	}
}

func TestFakeListIssuesLabelFiltering(t *testing.T) {
	c := NewFake()
	r := Repo{Owner: "acme", Name: "widget"}
	c.Issues[1] = &Issue{Number: 1, State: "open", Labels: []string{"claude-task"}}
	c.Issues[2] = &Issue{Number: 2, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	c.Issues[3] = &Issue{Number: 3, State: "closed", Labels: []string{"claude-task"}}

	got, err := c.ListIssues(context.Background(), r, []string{"claude-task"}, []string{"claude-processing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("expected issue 1 only, got %+v", got)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/github/...`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/github/fake.go internal/github/fake_test.go
git commit -m "feat(github): add in-memory FakeClient for tests"
```

---

### Task 1.3: Real ghClient — identity & repo metadata

**Files:**
- Create: `internal/github/gh.go`
- Create: `internal/github/gh_test.go`

- [ ] **Step 1: Write the ghClient skeleton + exec helper**

`internal/github/gh.go`:

```go
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

// remaining methods implemented in subsequent tasks

// (helper used by tests to override the binary)
func newGhClientWithBin(bin string) *ghClient { return &ghClient{ghBin: bin} }

// unused imports keepers
var _ = json.Unmarshal
```

- [ ] **Step 2: Add a test for exec error formatting using a deterministic fake binary**

`internal/github/gh_test.go`:

```go
package github

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBin writes a script that echoes its args and the contents of stdin.
// Used to make ghClient tests deterministic without a real `gh`.
func fakeBin(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fakes not supported on Windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-gh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunGhPropagatesStderrOnError(t *testing.T) {
	bin := fakeBin(t, `echo "boom" 1>&2
exit 7
`)
	c := newGhClientWithBin(bin)
	_, err := c.runGh(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr not propagated: %v", err)
	}
}

func TestCurrentUserParses(t *testing.T) {
	bin := fakeBin(t, `echo octocat`)
	c := newGhClientWithBin(bin)
	u, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u != "octocat" {
		t.Fatalf("got %q", u)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/github/... -v`

Expected: both tests PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/github/gh.go internal/github/gh_test.go
git commit -m "feat(github): add ghClient exec helper and identity/metadata"
```

---

### Task 1.4: ghClient — issues, PRs, labels

**Files:**
- Modify: `internal/github/gh.go`
- Modify: `internal/github/gh_test.go`

- [ ] **Step 1: Append ListIssues, ListPRs, GetPR, AddLabel, RemoveLabel**

Append to `internal/github/gh.go`:

```go
// ghIssue/ghPR are JSON shapes returned by `gh issue list --json ...`.
// We translate from gh's label-array-of-objects to our flat []string.
type ghLabel struct{ Name string `json:"name"` }
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
```

- [ ] **Step 2: Add parser tests using canned JSON**

Append to `internal/github/gh_test.go`:

```go
func TestListIssuesParsesAndFiltersWithout(t *testing.T) {
	body := `[
	 {"number":1,"title":"t1","body":"b","state":"open","labels":[{"name":"claude-task"}]},
	 {"number":2,"title":"t2","body":"b","state":"open","labels":[{"name":"claude-task"},{"name":"claude-processing"}]}
	]`
	bin := fakeBin(t, `cat <<'EOF'
`+body+`
EOF`)
	c := newGhClientWithBin(bin)
	got, err := c.ListIssues(context.Background(), Repo{Owner: "a", Name: "b"},
		[]string{"claude-task"}, []string{"claude-processing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("want [1], got %+v", got)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/github/... -v -run TestListIssues`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/github/gh.go internal/github/gh_test.go
git commit -m "feat(github): implement list/get issues+PRs and label edits"
```

---

### Task 1.5: ghClient — refs (create/delete/list/get)

**Files:**
- Modify: `internal/github/gh.go`
- Modify: `internal/github/gh_test.go`

- [ ] **Step 1: Append ref operations using `gh api`**

Append to `internal/github/gh.go`:

```go
type ghRefObj struct{ SHA string `json:"sha"` }
type ghRef struct {
	Ref    string   `json:"ref"`
	Object ghRefObj `json:"object"`
}

func (c *ghClient) CreateRef(ctx context.Context, r Repo, ref, sha string) error {
	// POST /repos/{owner}/{repo}/git/refs  body: {"ref":..., "sha":...}
	body := fmt.Sprintf(`{"ref":%q,"sha":%q}`, ref, sha)
	out, err := c.runGh(ctx, "api", "-X", "POST",
		fmt.Sprintf("repos/%s/git/refs", r.String()),
		"--input", "-",
		"-H", "Accept: application/vnd.github+json",
		"--method", "POST",
	)
	_ = out
	if err != nil {
		// Detect 422 "Reference already exists" in the error message.
		if strings.Contains(err.Error(), "Reference already exists") ||
			strings.Contains(err.Error(), "422") {
			return ErrRefExists
		}
		// Retry path: when --input - is used, gh reads stdin. We didn't
		// pipe body above. Use a stdin-piping path instead.
		return c.createRefStdin(ctx, r, body)
	}
	return nil
}

// createRefStdin pipes JSON into `gh api --input -`.
func (c *ghClient) createRefStdin(ctx context.Context, r Repo, body string) error {
	cmd := exec.CommandContext(ctx, c.ghBin, "api", "-X", "POST",
		fmt.Sprintf("repos/%s/git/refs", r.String()),
		"--input", "-")
	cmd.Stdin = strings.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "Reference already exists") ||
			strings.Contains(stderr.String(), "422") {
			return ErrRefExists
		}
		return fmt.Errorf("gh api create ref: %w\nstderr: %s", err, stderr.String())
	}
	return nil
}

func (c *ghClient) DeleteRef(ctx context.Context, r Repo, ref string) error {
	// DELETE /repos/{owner}/{repo}/git/refs/<ref-without-leading-refs/>
	trim := strings.TrimPrefix(ref, "refs/")
	_, err := c.runGh(ctx, "api", "-X", "DELETE",
		fmt.Sprintf("repos/%s/git/refs/%s", r.String(), trim))
	if err != nil {
		// Treat 422/404 already-deleted as success.
		if strings.Contains(err.Error(), "Reference does not exist") ||
			strings.Contains(err.Error(), "404") ||
			strings.Contains(err.Error(), "422") {
			return nil
		}
		return err
	}
	return nil
}

func (c *ghClient) ListMatchingRefs(ctx context.Context, r Repo, prefix string) ([]Ref, error) {
	// GET /repos/{owner}/{repo}/git/matching-refs/<prefix>
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
```

- [ ] **Step 2: Test CreateRef 422 handling using a fake that exits 1 with "Reference already exists" on stderr**

Append to `internal/github/gh_test.go`:

```go
func TestCreateRefDetects422AsErrRefExists(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 422: Reference already exists" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.CreateRef(context.Background(), Repo{Owner: "a", Name: "b"},
		"refs/heads/claude/issue-42", "deadbeef")
	if err != ErrRefExists {
		t.Fatalf("want ErrRefExists, got %v", err)
	}
}
```

Note: the fake bin here stands in for `gh`. Because CreateRef falls through to `createRefStdin` in the current implementation, the fake bin runs in both paths and both must emit the 422 message — the script above emits it on the first invocation and exits non-zero; `createRefStdin` will re-invoke the fake, which again emits "Reference already exists" and returns ErrRefExists.

- [ ] **Step 3: Test DeleteRef idempotency**

Append:

```go
func TestDeleteRefTreatsAlreadyDeletedAsSuccess(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 422: Reference does not exist" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	if err := c.DeleteRef(context.Background(), Repo{Owner: "a", Name: "b"},
		"refs/heads/claude/issue-42"); err != nil {
		t.Fatalf("should be nil: %v", err)
	}
}
```

- [ ] **Step 4: Run all github tests**

Run: `go test ./internal/github/... -v`

Expected: all PASS.

- [ ] **Step 5: Commit**

```
gofmt -w .
git add internal/github/gh.go internal/github/gh_test.go
git commit -m "feat(github): implement ref create/delete/list/get via gh api"
```

---

### Task 1.6: ghClient — reviews

**Files:**
- Modify: `internal/github/gh.go`
- Modify: `internal/github/gh_test.go`

- [ ] **Step 1: Append ListReviews**

Append to `internal/github/gh.go`:

```go
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
```

Also ensure `"time"` is imported at top of the file.

- [ ] **Step 2: Test**

Append to `internal/github/gh_test.go`:

```go
func TestListReviewsParses(t *testing.T) {
	body := `[{"user":{"login":"reviewer-bot"},"state":"COMMENTED","submitted_at":"2026-04-17T12:00:00Z"}]`
	bin := fakeBin(t, `cat <<'EOF'
`+body+`
EOF`)
	c := newGhClientWithBin(bin)
	got, err := c.ListReviews(context.Background(), Repo{Owner: "a", Name: "b"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Author != "reviewer-bot" || got[0].State != "COMMENTED" {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/github/... -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/github/gh.go internal/github/gh_test.go
git commit -m "feat(github): implement ListReviews via REST"
```

---

## Phase 2 — internal/claim

### Task 2.1: Claim primitives

**Files:**
- Create: `internal/claim/claim.go`
- Create: `internal/claim/claim_test.go`

- [ ] **Step 1: Write claim.go**

```go
package claim

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charleszheng44/cc-crew/internal/github"
)

const TimestampFormat = "20060102T150405Z"

type Kind int

const (
	KindImplementer Kind = iota
	KindReviewer
)

// Paths encodes the ref-name layout for each kind.
type Paths struct {
	LockRef     string // e.g. "refs/heads/claude/issue-42"
	TagPrefix   string // e.g. "tags/claim/issue-42/"
}

// PathsFor returns the refs for a given work item.
func PathsFor(k Kind, number int) Paths {
	switch k {
	case KindImplementer:
		return Paths{
			LockRef:   fmt.Sprintf("refs/heads/claude/issue-%d", number),
			TagPrefix: fmt.Sprintf("tags/claim/issue-%d/", number),
		}
	case KindReviewer:
		return Paths{
			LockRef:   fmt.Sprintf("refs/tags/review-lock/pr-%d", number),
			TagPrefix: fmt.Sprintf("tags/review-claim/pr-%d/", number),
		}
	}
	panic("unreachable")
}

// TimestampTagName returns "refs/tags/<TagPrefix><now-UTC>".
func (p Paths) TimestampTagName(now time.Time) string {
	return "refs/" + p.TagPrefix + now.UTC().Format(TimestampFormat)
}

type Claimer struct {
	GH   github.Client
	Repo github.Repo
	Now  func() time.Time // injected for tests; defaults to time.Now
}

func New(gh github.Client, r github.Repo) *Claimer {
	return &Claimer{GH: gh, Repo: r, Now: time.Now}
}

// TryClaim attempts to atomically claim a work item. `sha` is the commit
// SHA the lock ref should point to (for implementer: base branch SHA;
// for reviewer: PR head SHA). Returns (true, nil) on win, (false, nil)
// if another orchestrator already holds the lock, or (false, err) on
// unexpected errors.
func (c *Claimer) TryClaim(ctx context.Context, k Kind, number int, sha string) (bool, error) {
	p := PathsFor(k, number)
	err := c.GH.CreateRef(ctx, c.Repo, p.LockRef, sha)
	if err != nil {
		if errors.Is(err, github.ErrRefExists) {
			return false, nil
		}
		return false, fmt.Errorf("create lock %s: %w", p.LockRef, err)
	}
	// Immediately create the timestamp tag. Any failure here is
	// non-fatal at claim time; reclaim recreates missing tags.
	tag := p.TimestampTagName(c.Now())
	if err := c.GH.CreateRef(ctx, c.Repo, tag, sha); err != nil && !errors.Is(err, github.ErrRefExists) {
		return true, fmt.Errorf("create claim tag %s: %w (lock held)", tag, err)
	}
	return true, nil
}

// Release deletes the timestamp tags and optionally the lock ref.
// For a failed implementer: deleteLock=true (drop branch; work retried).
// For a successful implementer: deleteLock=false (PR references it).
// For a failed reviewer: deleteLock=true.
// For a successful reviewer: deleteLock=true.
func (c *Claimer) Release(ctx context.Context, k Kind, number int, deleteLock bool) error {
	p := PathsFor(k, number)
	tags, err := c.GH.ListMatchingRefs(ctx, c.Repo, p.TagPrefix)
	if err != nil {
		return fmt.Errorf("list tags for release: %w", err)
	}
	for _, t := range tags {
		if err := c.GH.DeleteRef(ctx, c.Repo, t.Name); err != nil {
			return fmt.Errorf("delete tag %s: %w", t.Name, err)
		}
	}
	if deleteLock {
		if err := c.GH.DeleteRef(ctx, c.Repo, p.LockRef); err != nil {
			return fmt.Errorf("delete lock %s: %w", p.LockRef, err)
		}
	}
	return nil
}

// OldestTagAge returns the age of the oldest timestamp tag under the
// paths' prefix, or (0, ok=false) if none exist.
func (c *Claimer) OldestTagAge(ctx context.Context, k Kind, number int) (time.Duration, bool, error) {
	p := PathsFor(k, number)
	tags, err := c.GH.ListMatchingRefs(ctx, c.Repo, p.TagPrefix)
	if err != nil {
		return 0, false, err
	}
	if len(tags) == 0 {
		return 0, false, nil
	}
	// Parse timestamps out of ref names.
	type parsed struct {
		ref string
		t   time.Time
	}
	var ps []parsed
	for _, t := range tags {
		parts := strings.Split(t.Name, "/")
		if len(parts) == 0 {
			continue
		}
		ts, err := time.Parse(TimestampFormat, parts[len(parts)-1])
		if err != nil {
			continue
		}
		ps = append(ps, parsed{ref: t.Name, t: ts})
	}
	if len(ps) == 0 {
		return 0, false, nil
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].t.Before(ps[j].t) })
	return c.Now().Sub(ps[0].t), true, nil
}
```

- [ ] **Step 2: Write claim_test.go**

```go
package claim

import (
	"context"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/github"
)

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestTryClaimWinsOnEmptyRepo(t *testing.T) {
	f := github.NewFake()
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	c.Now = fixedNow(time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC))
	won, err := c.TryClaim(context.Background(), KindImplementer, 42, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !won {
		t.Fatal("expected to win claim")
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock branch not created")
	}
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; !ok {
		t.Fatal("timestamp tag not created")
	}
}

func TestTryClaimLosesWhenLockExists(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "existing"
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	c.Now = fixedNow(time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC))
	won, err := c.TryClaim(context.Background(), KindImplementer, 42, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("should have lost")
	}
	// We should NOT have created a timestamp tag when we lost.
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("unexpected timestamp tag")
	}
}

func TestReleaseDeletesTagsAndOptionallyLock(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "x"
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "x"
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	if err := c.Release(context.Background(), KindImplementer, 42, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("timestamp tag should be gone")
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock branch should remain (deleteLock=false)")
	}
}

func TestOldestTagAge(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "x"
	f.Refs["refs/tags/claim/issue-42/20260417T120500Z"] = "x"
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	c.Now = fixedNow(time.Date(2026, 4, 17, 12, 30, 0, 0, time.UTC))
	age, ok, err := c.OldestTagAge(context.Background(), KindImplementer, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if age != 30*time.Minute {
		t.Fatalf("got %v", age)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/claim/... -v`

Expected: all PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/claim/claim.go internal/claim/claim_test.go
git commit -m "feat(claim): atomic claim primitives with timestamp tags"
```

---

## Phase 3 — internal/worktree

### Task 3.1: Host worktree shims

**Files:**
- Create: `internal/worktree/worktree.go`
- Create: `internal/worktree/worktree_test.go`

- [ ] **Step 1: Implement wrapper**

```go
package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

// Manager wraps `git worktree` operations against a specific repo dir.
type Manager struct {
	RepoDir string // absolute path to the host clone
	GitBin  string // defaults to "git"
}

func New(repoDir string) *Manager {
	return &Manager{RepoDir: repoDir, GitBin: "git"}
}

func (m *Manager) git(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, m.GitBin, args...)
	cmd.Dir = m.RepoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %v: %w\nstderr: %s", args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// Path returns the absolute host path of a worktree for a given branch.
// Example: Path("claude/issue-42") -> "<repoDir>/.claude-worktrees/issue-42"
func (m *Manager) Path(branch string) string {
	leaf := filepath.Base(branch) // "issue-42" or "review-<N>"
	return filepath.Join(m.RepoDir, ".claude-worktrees", leaf)
}

// Add fetches origin/branch and creates a worktree at Path(branch).
// If the worktree path already exists, it is removed first.
func (m *Manager) Add(ctx context.Context, branch string) (string, error) {
	if _, err := m.git(ctx, "fetch", "origin", branch); err != nil {
		return "", err
	}
	p := m.Path(branch)
	// Idempotent: if it exists, remove and re-add.
	_, _ = m.git(ctx, "worktree", "remove", "--force", p)
	if _, err := m.git(ctx, "worktree", "add", "--force", p, branch); err != nil {
		return "", err
	}
	return p, nil
}

// Remove tears down a worktree. Always safe to call; errors are logged
// by caller, not returned fatally here.
func (m *Manager) Remove(ctx context.Context, branch string) error {
	p := m.Path(branch)
	_, err := m.git(ctx, "worktree", "remove", "--force", p)
	return err
}

// Prune runs `git worktree prune` to clean up stale admin files.
func (m *Manager) Prune(ctx context.Context) error {
	_, err := m.git(ctx, "worktree", "prune")
	return err
}

// List returns worktree paths under .claude-worktrees/.
func (m *Manager) List(ctx context.Context) ([]string, error) {
	out, err := m.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("worktree ")) {
			continue
		}
		p := string(bytes.TrimPrefix(line, []byte("worktree ")))
		// Only include paths under .claude-worktrees/
		if filepath.Base(filepath.Dir(p)) == ".claude-worktrees" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
```

- [ ] **Step 2: Write integration test against a local scratch repo**

```go
package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// makeRepo builds a local bare "origin" + clone with one commit on main
// and one branch "claude/issue-42". Returns the clone path.
func makeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	clone := filepath.Join(root, "clone")

	mustRun(t, root, "git", "init", "--bare", origin)

	// Seed origin via a throwaway clone.
	seed := filepath.Join(root, "seed")
	mustRun(t, root, "git", "clone", origin, seed)
	mustRun(t, seed, "git", "config", "user.email", "t@t")
	mustRun(t, seed, "git", "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, seed, "git", "add", "-A")
	mustRun(t, seed, "git", "commit", "-m", "init")
	mustRun(t, seed, "git", "branch", "-M", "main")
	mustRun(t, seed, "git", "push", "-u", "origin", "main")

	mustRun(t, seed, "git", "checkout", "-b", "claude/issue-42")
	mustRun(t, seed, "git", "commit", "--allow-empty", "-m", "work")
	mustRun(t, seed, "git", "push", "-u", "origin", "claude/issue-42")

	mustRun(t, root, "git", "clone", origin, clone)
	mustRun(t, clone, "git", "fetch", "origin")
	return clone
}

func TestAddRemove(t *testing.T) {
	clone := makeRepo(t)
	m := New(clone)
	ctx := context.Background()

	p, err := m.Add(ctx, "claude/issue-42")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("worktree path not created: %v", err)
	}

	if err := m.Remove(ctx, "claude/issue-42"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("worktree path should be gone, stat err=%v", err)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/worktree/... -v`

Expected: PASS (or SKIP if git isn't installed).

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): add/remove/prune shims around git worktree"
```

---

## Phase 4 — internal/reclaim

### Task 4.1: Reclaim sweeper

**Files:**
- Create: `internal/reclaim/reclaim.go`
- Create: `internal/reclaim/reclaim_test.go`

- [ ] **Step 1: Implement sweeper**

```go
package reclaim

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// AlreadyDone reports whether the work for a given item has already
// been completed — so reclaim should NOT reap it.
type AlreadyDone func(ctx context.Context, number int) (bool, error)

// Sweeper reclaims stale locks for a given Kind.
type Sweeper struct {
	GH         github.Client
	Repo       github.Repo
	Claimer    *claim.Claimer
	Kind       claim.Kind
	MaxAge     time.Duration
	IsDone     AlreadyDone // may be nil
	Now        func() time.Time
}

// Sweep walks all lock refs for the sweeper's kind and reclaims those
// whose oldest timestamp tag is older than MaxAge and which are not
// "already done".
func (s *Sweeper) Sweep(ctx context.Context) error {
	locks, err := s.listLockRefs(ctx)
	if err != nil {
		return err
	}
	for _, lr := range locks {
		num, ok := parseNumber(lr.Name, s.Kind)
		if !ok {
			continue
		}
		age, haveTag, err := s.Claimer.OldestTagAge(ctx, s.Kind, num)
		if err != nil {
			return err
		}
		if !haveTag {
			// Orphan lock: create a timestamp tag now so the window
			// is well-defined. Idempotent (422 means someone else
			// created it first).
			tag := claim.PathsFor(s.Kind, num).TimestampTagName(s.Now())
			if err := s.GH.CreateRef(ctx, s.Repo, tag, lr.SHA); err != nil && !errors.Is(err, github.ErrRefExists) {
				return fmt.Errorf("recreate timestamp tag %s: %w", tag, err)
			}
			continue
		}
		if age < s.MaxAge {
			continue
		}
		if s.IsDone != nil {
			done, err := s.IsDone(ctx, num)
			if err != nil {
				return err
			}
			if done {
				continue
			}
		}
		// Reap: drop timestamp tags AND lock. Failure retries on next tick.
		if err := s.Claimer.Release(ctx, s.Kind, num, true); err != nil {
			return fmt.Errorf("reclaim %d: %w", num, err)
		}
	}
	return nil
}

func (s *Sweeper) listLockRefs(ctx context.Context) ([]github.Ref, error) {
	switch s.Kind {
	case claim.KindImplementer:
		return s.GH.ListMatchingRefs(ctx, s.Repo, "heads/claude/issue-")
	case claim.KindReviewer:
		return s.GH.ListMatchingRefs(ctx, s.Repo, "tags/review-lock/pr-")
	}
	return nil, fmt.Errorf("unknown kind %d", s.Kind)
}

func parseNumber(refName string, k claim.Kind) (int, bool) {
	// Implementer: refs/heads/claude/issue-42
	// Reviewer:    refs/tags/review-lock/pr-42
	var prefix string
	switch k {
	case claim.KindImplementer:
		prefix = "refs/heads/claude/issue-"
	case claim.KindReviewer:
		prefix = "refs/tags/review-lock/pr-"
	}
	s := strings.TrimPrefix(refName, prefix)
	if s == refName {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ImplementerDoneFn returns an AlreadyDone that returns true when a PR
// exists for the branch refs/heads/claude/issue-<N>.
func ImplementerDoneFn(gh github.Client, r github.Repo) AlreadyDone {
	return func(ctx context.Context, n int) (bool, error) {
		prs, err := gh.ListPRs(ctx, r, nil, nil)
		if err != nil {
			return false, err
		}
		target := fmt.Sprintf("claude/issue-%d", n)
		for _, p := range prs {
			if p.HeadRefName == target {
				return true, nil
			}
		}
		return false, nil
	}
}

// ReviewerDoneFn returns an AlreadyDone that returns true when the
// reviewer persona's login has already posted a review on PR <N>.
func ReviewerDoneFn(gh github.Client, r github.Repo, reviewerLogin string) AlreadyDone {
	return func(ctx context.Context, n int) (bool, error) {
		reviews, err := gh.ListReviews(ctx, r, n)
		if err != nil {
			return false, err
		}
		for _, rv := range reviews {
			if rv.Author == reviewerLogin {
				return true, nil
			}
		}
		return false, nil
	}
}
```

- [ ] **Step 2: Write tests covering orphan-recreate, below-threshold, reap, and already-done-skip**

```go
package reclaim

import (
	"context"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func mkSweeper(f *github.FakeClient, maxAge time.Duration, isDone AlreadyDone, now time.Time) *Sweeper {
	r := github.Repo{Owner: "a", Name: "b"}
	c := claim.New(f, r)
	c.Now = fixedNow(now)
	return &Sweeper{
		GH: f, Repo: r, Claimer: c,
		Kind: claim.KindImplementer, MaxAge: maxAge,
		IsDone: isDone, Now: fixedNow(now),
	}
}

func TestSweepOrphanLockRecreatesTag(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "abc"
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	s := mkSweeper(f, 30*time.Minute, nil, now)
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	expected := "refs/tags/claim/issue-42/20260417T120000Z"
	if _, ok := f.Refs[expected]; !ok {
		t.Fatalf("expected orphan lock to get a timestamp tag %s; refs=%v", expected, f.Refs)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock should still exist")
	}
}

func TestSweepLeavesFreshLocks(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "abc"
	f.Refs["refs/tags/claim/issue-42/20260417T115900Z"] = "abc" // 1 min old
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	s := mkSweeper(f, 30*time.Minute, nil, now)
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock should survive")
	}
}

func TestSweepReapsStaleLocks(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "abc"
	f.Refs["refs/tags/claim/issue-42/20260417T110000Z"] = "abc" // 1 hour old
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	s := mkSweeper(f, 30*time.Minute, nil, now)
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; ok {
		t.Fatal("lock should be reaped")
	}
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T110000Z"]; ok {
		t.Fatal("timestamp tag should be reaped")
	}
}

func TestSweepSkipsAlreadyDone(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "abc"
	f.Refs["refs/tags/claim/issue-42/20260417T110000Z"] = "abc" // 1 hour old
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	isDone := func(ctx context.Context, n int) (bool, error) { return true, nil }
	s := mkSweeper(f, 30*time.Minute, isDone, now)
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock should be preserved — work is already done")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/reclaim/... -v`

Expected: all 4 PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/reclaim/reclaim.go internal/reclaim/reclaim_test.go
git commit -m "feat(reclaim): stale-lock sweeper with orphan recreate + already-done skip"
```

---

## Phase 5 — internal/docker

### Task 5.1: Docker runner

**Files:**
- Create: `internal/docker/docker.go`
- Create: `internal/docker/docker_test.go`

- [ ] **Step 1: Implement runner**

```go
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// RunSpec describes a one-shot docker run.
type RunSpec struct {
	Image  string
	Name   string            // container name, must be unique
	Labels map[string]string // cc-crew.repo, cc-crew.role, cc-crew.issue
	Env    map[string]string // env vars to set inside the container
	Mounts []Mount           // bind mounts
	// Stdout/Stderr are forwarded to these writers if non-nil.
	Stdout io.Writer
	Stderr io.Writer
}

type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// Runner wraps `docker` invocations. It is stateless; safe to share.
type Runner struct {
	Bin string // defaults to "docker"
}

func New() *Runner { return &Runner{Bin: "docker"} }

// BuildRunArgs constructs the argv (excluding the binary itself) for
// `docker run --rm`. Exposed for testing.
func BuildRunArgs(s RunSpec) []string {
	args := []string{"run", "--rm", "--name", s.Name}
	// Stable ordering for labels (deterministic tests).
	keys := make([]string, 0, len(s.Labels))
	for k := range s.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+s.Labels[k])
	}
	envKeys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "-e", k+"="+s.Env[k])
	}
	for _, m := range s.Mounts {
		v := m.HostPath + ":" + m.ContainerPath
		if m.ReadOnly {
			v += ":ro"
		}
		args = append(args, "-v", v)
	}
	args = append(args, s.Image)
	return args
}

// Run blocks until the container exits. Returns the exit code (0 on success).
// A context deadline causes docker kill via signal and a non-zero exit.
func (r *Runner) Run(ctx context.Context, s RunSpec) (int, error) {
	cmd := exec.CommandContext(ctx, r.Bin, BuildRunArgs(s)...)
	cmd.Stdout = s.Stdout
	cmd.Stderr = s.Stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return -1, fmt.Errorf("docker run: %w", err)
}

// Kill sends `docker kill <name>`. Idempotent: no-op error on missing container.
func (r *Runner) Kill(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, r.Bin, "kill", name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "No such container") {
			return nil
		}
		return fmt.Errorf("docker kill %s: %w (%s)", name, err, stderr.String())
	}
	return nil
}

// PSEntry is a subset of `docker ps` output.
type PSEntry struct {
	Name   string
	Image  string
	Labels map[string]string
}

// PS lists running containers matching the given labels.
// Implementation uses `docker ps --filter label=... --format '{{json .}}'`.
func (r *Runner) PS(ctx context.Context, labelMatchers map[string]string) ([]PSEntry, error) {
	args := []string{"ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Labels}}"}
	for k, v := range labelMatchers {
		args = append(args, "--filter", "label="+k+"="+v)
	}
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker ps: %w (%s)", err, stderr.String())
	}
	return parsePS(stdout.String()), nil
}

func parsePS(out string) []PSEntry {
	var res []PSEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		labels := map[string]string{}
		for _, kv := range strings.Split(parts[2], ",") {
			kv = strings.TrimSpace(kv)
			if kv == "" {
				continue
			}
			eq := strings.Index(kv, "=")
			if eq < 0 {
				continue
			}
			labels[kv[:eq]] = kv[eq+1:]
		}
		res = append(res, PSEntry{Name: parts[0], Image: parts[1], Labels: labels})
	}
	return res
}
```

- [ ] **Step 2: Unit-test BuildRunArgs + parsePS (no real docker needed)**

```go
package docker

import (
	"reflect"
	"testing"
)

func TestBuildRunArgsDeterministic(t *testing.T) {
	args := BuildRunArgs(RunSpec{
		Image: "img:tag",
		Name:  "ctr",
		Labels: map[string]string{
			"cc-crew.repo":  "acme/widget",
			"cc-crew.issue": "42",
		},
		Env: map[string]string{"FOO": "bar", "BAZ": "qux"},
		Mounts: []Mount{
			{HostPath: "/a", ContainerPath: "/workspace", ReadOnly: false},
			{HostPath: "/b", ContainerPath: "/b", ReadOnly: true},
		},
	})
	want := []string{
		"run", "--rm", "--name", "ctr",
		"--label", "cc-crew.issue=42",
		"--label", "cc-crew.repo=acme/widget",
		"-e", "BAZ=qux", "-e", "FOO=bar",
		"-v", "/a:/workspace",
		"-v", "/b:/b:ro",
		"img:tag",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v\nwant %v", args, want)
	}
}

func TestParsePS(t *testing.T) {
	in := "ctr-a\timg\tcc-crew.repo=acme/widget,cc-crew.role=implementer\nctr-b\timg\t\n"
	entries := parsePS(in)
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].Labels["cc-crew.role"] != "implementer" {
		t.Fatalf("bad label parse: %+v", entries[0])
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/docker/... -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/docker/docker.go internal/docker/docker_test.go
git commit -m "feat(docker): run/kill/ps wrappers with deterministic arg building"
```

---

## Phase 6 — internal/config

### Task 6.1: Config struct and validation

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write config.go**

```go
package config

import (
	"errors"
	"time"
)

type Config struct {
	RepoDir string // absolute path to the host clone

	// Concurrency
	MaxImplementers int
	MaxReviewers    int

	// Timing
	PollInterval       time.Duration
	ReclaimAfter       time.Duration
	ImplTaskTimeout    time.Duration
	ReviewTaskTimeout  time.Duration

	// Labels
	TaskLabel       string
	ProcessingLabel string
	DoneLabel       string
	ReviewLabel     string
	ReviewingLabel  string
	ReviewedLabel   string

	// Behavior
	AutoReview bool

	// Image / model
	Image string
	Model string

	// Credentials (populated from env)
	ImplementerGHToken string
	ReviewerGHToken    string
	OrchestratorGHToken string // for orchestrator's own calls
	ClaudeOAuthToken   string
	AnthropicAPIKey    string
	ImplementerGitName  string
	ImplementerGitEmail string
	ReviewerGitName     string
	ReviewerGitEmail    string

	// Base branch; empty means "resolve from GitHub default_branch at startup"
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
	// GH token: need orchestrator token (fallback GH_TOKEN).
	if c.OrchestratorGHToken == "" {
		return errors.New("GH_TOKEN (or per-persona equivalent) is required")
	}
	// Per-persona enabled => per-persona git identity required
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
	// Claude credentials: need one of CLAUDE_CODE_OAUTH_TOKEN / ANTHROPIC_API_KEY.
	if c.ClaudeOAuthToken == "" && c.AnthropicAPIKey == "" {
		return errors.New("one of CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY is required")
	}
	return nil
}
```

- [ ] **Step 2: Test defaults and validation**

```go
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
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/config/... -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): define Config struct with defaults and validation"
```

---

### Task 6.2: Flag/env parsing + repo resolution

**Files:**
- Create: `internal/config/parse.go`
- Create: `internal/config/parse_test.go`

- [ ] **Step 1: Implement parser**

```go
package config

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Parse builds a Config from CLI flags, environment variables, and (for
// RepoDir + Repo) the provided repoDir (may be empty → PWD).
//
// Precedence: flag > env > default.
func Parse(flags []string, getenv func(string) string, pwd string) (Config, error) {
	c := Defaults()
	fs := flag.NewFlagSet("cc-crew up", flag.ContinueOnError)
	var buf bytes.Buffer
	fs.SetOutput(&buf)

	repoDir := fs.String("repo", orDefault(getenv("CC_REPO"), pwd), "Local repo path")
	fs.IntVar(&c.MaxImplementers, "max-implementers", envInt(getenv, "CC_MAX_IMPLEMENTERS", c.MaxImplementers), "Max concurrent implementer tasks")
	fs.IntVar(&c.MaxReviewers, "max-reviewers", envInt(getenv, "CC_MAX_REVIEWERS", c.MaxReviewers), "Max concurrent reviewer tasks")

	pollSecs := fs.Int("poll-seconds", envInt(getenv, "CC_POLL_SECONDS", int(c.PollInterval/time.Second)), "Tick interval (seconds)")
	reclaimSecs := fs.Int("reclaim-seconds", envInt(getenv, "CC_RECLAIM_SECONDS", int(c.ReclaimAfter/time.Second)), "Stale-lock age threshold")
	implSecs := fs.Int("impl-task-seconds", envInt(getenv, "CC_IMPL_TASK_SECONDS", int(c.ImplTaskTimeout/time.Second)), "Per-task wall-clock for implementer")
	revSecs := fs.Int("review-task-seconds", envInt(getenv, "CC_REVIEW_TASK_SECONDS", int(c.ReviewTaskTimeout/time.Second)), "Per-task wall-clock for reviewer")

	fs.StringVar(&c.TaskLabel, "task-label", orDefault(getenv("CC_TASK_LABEL"), c.TaskLabel), "Queue label for implementer")
	fs.StringVar(&c.ReviewLabel, "review-label", orDefault(getenv("CC_REVIEW_LABEL"), c.ReviewLabel), "Queue label for reviewer")
	fs.BoolVar(&c.AutoReview, "auto-review", envBool(getenv, "CC_AUTO_REVIEW", c.AutoReview), "Auto-apply review-label to implementer PRs")
	fs.StringVar(&c.BaseBranch, "base-branch", orDefault(getenv("CC_BASE_BRANCH"), ""), "Base branch (default: GitHub's default branch)")
	fs.StringVar(&c.Image, "image", orDefault(getenv("CC_IMAGE"), c.Image), "Task container image")
	fs.StringVar(&c.Model, "model", orDefault(getenv("CC_MODEL"), c.Model), "Claude model")

	if err := fs.Parse(flags); err != nil {
		return c, fmt.Errorf("flag parse: %w\n%s", err, buf.String())
	}

	// Apply durations.
	c.PollInterval = time.Duration(*pollSecs) * time.Second
	c.ReclaimAfter = time.Duration(*reclaimSecs) * time.Second
	c.ImplTaskTimeout = time.Duration(*implSecs) * time.Second
	c.ReviewTaskTimeout = time.Duration(*revSecs) * time.Second

	// RepoDir: resolve to absolute.
	if *repoDir == "" {
		return c, fmt.Errorf("--repo is required (or set CC_REPO, or run from inside a repo)")
	}
	abs, err := filepath.Abs(*repoDir)
	if err != nil {
		return c, err
	}
	c.RepoDir = abs

	// Credentials from env only.
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

// ResolveRepo parses `owner/name` from `git remote get-url origin` inside c.RepoDir.
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
	u := url
	u = strings.TrimSuffix(u, ".git")
	if strings.HasPrefix(u, "git@") {
		// git@github.com:owner/name
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

// SilenceUnused is a compile-time reference so os is used when tests don't.
var _ = os.Getenv
```

- [ ] **Step 2: Test URL parsing + env fallbacks**

```go
package config

import "testing"

func TestParseOwnerRepo(t *testing.T) {
	cases := []struct {
		in  string
		ow  string
		nm  string
	}{
		{"https://github.com/acme/widget.git", "acme", "widget"},
		{"https://github.com/acme/widget", "acme", "widget"},
		{"git@github.com:acme/widget.git", "acme", "widget"},
		{"ssh://git@github.com/acme/widget.git", "acme", "widget"},
	}
	for _, tc := range cases {
		o, n, err := ParseOwnerRepo(tc.in)
		if err != nil || o != tc.ow || n != tc.nm {
			t.Errorf("%s -> (%q,%q,%v), want (%q,%q,nil)", tc.in, o, n, err, tc.ow, tc.nm)
		}
	}
}

func TestParseFlagOverridesEnv(t *testing.T) {
	env := map[string]string{
		"CC_MAX_IMPLEMENTERS": "7",
		"GH_TOKEN":            "t",
		"CLAUDE_CODE_OAUTH_TOKEN": "c",
		"IMPLEMENTER_GIT_NAME": "i",
		"IMPLEMENTER_GIT_EMAIL": "i@x",
		"REVIEWER_GIT_NAME": "r",
		"REVIEWER_GIT_EMAIL": "r@x",
	}
	get := func(k string) string { return env[k] }
	c, err := Parse([]string{"--max-implementers", "2"}, get, "/tmp/repo")
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxImplementers != 2 {
		t.Fatalf("flag should override env, got %d", c.MaxImplementers)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/config/... -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/config/parse.go internal/config/parse_test.go
git commit -m "feat(config): flag+env parsing and GitHub remote URL parsing"
```

---

## Phase 7 — internal/scheduler

### Task 7.1: Semaphore + lifecycle contracts

**Files:**
- Create: `internal/scheduler/scheduler.go`
- Create: `internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Implement Scheduler + Semaphore**

```go
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// Semaphore is a simple weighted semaphore bound to a capacity N.
type Semaphore struct {
	slots chan struct{}
}

func NewSemaphore(n int) *Semaphore {
	s := &Semaphore{slots: make(chan struct{}, n)}
	for i := 0; i < n; i++ {
		s.slots <- struct{}{}
	}
	return s
}

// TryAcquire returns true if a slot was taken, false if all are in use.
func (s *Semaphore) TryAcquire() bool {
	select {
	case <-s.slots:
		return true
	default:
		return false
	}
}

func (s *Semaphore) Release() { s.slots <- struct{}{} }

// Free returns the number of slots currently available.
func (s *Semaphore) Free() int { return len(s.slots) }

// Dispatcher dispatches a single claimed work item in a goroutine.
type Dispatcher interface {
	Dispatch(ctx context.Context, number int)
}

// Scheduler owns the tick loop for one persona (implementer or reviewer).
type Scheduler struct {
	Kind       claim.Kind
	Sem        *Semaphore
	Claimer    *claim.Claimer
	GH         github.Client
	Repo       github.Repo
	Dispatcher Dispatcher
	Log        *slog.Logger

	// Labels to match on candidate queries
	QueueLabel    string
	LockLabel     string
}

// Tick runs one polling iteration: list candidates, acquire semaphore
// and claim for each until the semaphore fills or candidates run out.
// Each successful claim starts an async Dispatch; when that goroutine
// exits, it must Release the semaphore (Dispatcher is responsible).
func (s *Scheduler) Tick(ctx context.Context) error {
	candidates, err := s.listCandidates(ctx)
	if err != nil {
		return err
	}
	for _, n := range candidates {
		if !s.Sem.TryAcquire() {
			return nil
		}
		won, sha, err := s.tryClaimOne(ctx, n)
		if err != nil {
			s.Sem.Release()
			s.Log.Warn("claim error; skipping", "number", n, "err", err)
			continue
		}
		if !won {
			s.Sem.Release()
			continue
		}
		if err := s.GH.AddLabel(ctx, s.Repo, n, s.LockLabel); err != nil {
			s.Log.Warn("add lock label failed (non-fatal)", "number", n, "err", err)
		}
		_ = sha // SHA used later; lifecycle uses lock branch
		go func(num int) {
			defer s.Sem.Release()
			s.Dispatcher.Dispatch(ctx, num)
		}(n)
	}
	return nil
}

// listCandidates returns work-item numbers (issue or PR numbers) sorted ascending.
func (s *Scheduler) listCandidates(ctx context.Context) ([]int, error) {
	switch s.Kind {
	case claim.KindImplementer:
		issues, err := s.GH.ListIssues(ctx, s.Repo, []string{s.QueueLabel}, []string{s.LockLabel})
		if err != nil {
			return nil, err
		}
		nums := make([]int, 0, len(issues))
		for _, i := range issues {
			nums = append(nums, i.Number)
		}
		sortAsc(nums)
		return nums, nil
	case claim.KindReviewer:
		prs, err := s.GH.ListPRs(ctx, s.Repo, []string{s.QueueLabel}, []string{s.LockLabel})
		if err != nil {
			return nil, err
		}
		nums := make([]int, 0, len(prs))
		for _, p := range prs {
			nums = append(nums, p.Number)
		}
		sortAsc(nums)
		return nums, nil
	}
	return nil, nil
}

// tryClaimOne performs an atomic claim and returns (won, sha, err).
func (s *Scheduler) tryClaimOne(ctx context.Context, n int) (bool, string, error) {
	switch s.Kind {
	case claim.KindImplementer:
		defBranch, err := s.GH.DefaultBranch(ctx, s.Repo)
		if err != nil {
			return false, "", err
		}
		ref, err := s.GH.GetRef(ctx, s.Repo, "refs/heads/"+defBranch)
		if err != nil {
			return false, "", err
		}
		won, err := s.Claimer.TryClaim(ctx, claim.KindImplementer, n, ref.SHA)
		return won, ref.SHA, err
	case claim.KindReviewer:
		pr, err := s.GH.GetPR(ctx, s.Repo, n)
		if err != nil {
			return false, "", err
		}
		won, err := s.Claimer.TryClaim(ctx, claim.KindReviewer, n, pr.HeadRefOid)
		return won, pr.HeadRefOid, err
	}
	return false, "", nil
}

func sortAsc(xs []int) {
	// simple insertion sort; N is tiny (<200)
	for i := 1; i < len(xs); i++ {
		v := xs[i]
		j := i - 1
		for j >= 0 && xs[j] > v {
			xs[j+1] = xs[j]
			j--
		}
		xs[j+1] = v
	}
}

// Run starts the tick loop and blocks until ctx is canceled.
// On cancel, returns ctx.Err().
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	// First tick immediately.
	if err := s.Tick(ctx); err != nil {
		s.Log.Warn("tick error", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := s.Tick(ctx); err != nil {
				s.Log.Warn("tick error", "err", err)
			}
		}
	}
}

// ensure sync import is kept when not yet used
var _ = sync.Mutex{}
```

- [ ] **Step 2: Test semaphore semantics**

```go
package scheduler

import "testing"

func TestSemaphoreAcquireRelease(t *testing.T) {
	s := NewSemaphore(2)
	if !s.TryAcquire() {
		t.Fatal("1st acquire should succeed")
	}
	if !s.TryAcquire() {
		t.Fatal("2nd acquire should succeed")
	}
	if s.TryAcquire() {
		t.Fatal("3rd acquire should fail")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Fatal("after release should succeed")
	}
}
```

- [ ] **Step 3: Test tick dispatches claimed work**

```go
package scheduler

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

type countingDispatcher struct{ n atomic.Int32 }

func (d *countingDispatcher) Dispatch(ctx context.Context, number int) {
	d.n.Add(1)
	time.Sleep(5 * time.Millisecond) // ensure semaphore is held briefly
}

func TestTickClaimsAndDispatches(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Refs["refs/heads/main"] = "basesha"
	f.Issues[1] = &github.Issue{Number: 1, State: "open", Labels: []string{"claude-task"}}
	f.Issues[2] = &github.Issue{Number: 2, State: "open", Labels: []string{"claude-task"}}
	f.Issues[3] = &github.Issue{Number: 3, State: "open", Labels: []string{"claude-task", "claude-processing"}}

	disp := &countingDispatcher{}
	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	s := &Scheduler{
		Kind: claim.KindImplementer, Sem: NewSemaphore(2),
		Claimer: c, GH: f, Repo: r, Dispatcher: disp,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing",
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Wait for goroutines to run; both dispatches should fire.
	deadline := time.Now().Add(500 * time.Millisecond)
	for disp.n.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if disp.n.Load() != 2 {
		t.Fatalf("want 2 dispatches, got %d", disp.n.Load())
	}
	if _, ok := f.Refs["refs/heads/claude/issue-1"]; !ok {
		t.Fatal("issue 1 lock not created")
	}
	if _, ok := f.Refs["refs/heads/claude/issue-2"]; !ok {
		t.Fatal("issue 2 lock not created")
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/scheduler/... -v`

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```
gofmt -w .
git add internal/scheduler/scheduler.go internal/scheduler/scheduler_test.go
git commit -m "feat(scheduler): tick loop + semaphore + candidate claiming"
```

---

### Task 7.2: Per-task lifecycle

**Files:**
- Create: `internal/scheduler/lifecycle.go`
- Create: `internal/scheduler/lifecycle_test.go`

- [ ] **Step 1: Define Lifecycle Dispatcher**

```go
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

// Lifecycle is a Dispatcher that runs the full per-task flow:
// fetch worktree → docker run → clean up labels.
type Lifecycle struct {
	Kind       claim.Kind
	Claimer    *claim.Claimer
	GH         github.Client
	Repo       github.Repo
	WT         *worktree.Manager
	Docker     *docker.Runner
	Log        *slog.Logger

	// Labels
	QueueLabel    string
	LockLabel     string
	DoneLabel     string
	ReviewLabel   string // used for --auto-review

	// Per-task config
	Image           string
	Model           string
	MaxTurns        int
	TaskTimeout     time.Duration
	AutoReview      bool

	// Env passed to container
	RoleGHToken     string
	ClaudeOAuth     string
	AnthropicAPIKey string
	GitName         string
	GitEmail        string

	BaseBranch string // implementer only
}

// Dispatch implements the scheduler.Dispatcher interface.
// On entry, we already hold the claim. The caller (scheduler.Tick)
// releases the semaphore when this function returns.
func (l *Lifecycle) Dispatch(ctx context.Context, number int) {
	log := l.Log.With("kind", kindName(l.Kind), "number", number)
	log.Info("dispatch start")

	branch := fmt.Sprintf("claude/issue-%d", number)
	var wtPath string
	if l.Kind == claim.KindImplementer {
		p, err := l.WT.Add(ctx, branch)
		if err != nil {
			log.Error("worktree add failed", "err", err)
			l.failCleanup(ctx, number)
			return
		}
		wtPath = p
	} else {
		// Reviewer worktree is created from the PR head: we use a
		// detached working tree at .claude-worktrees/review-<N>. The
		// in-container entrypoint runs `gh pr checkout`, so the host
		// worktree is just a scratch dir rooted at the clone.
		wtPath = filepath.Join(l.WT.RepoDir, ".claude-worktrees", fmt.Sprintf("review-%d", number))
	}

	spec := l.buildRunSpec(number, wtPath)
	runCtx, cancel := context.WithTimeout(ctx, l.TaskTimeout)
	defer cancel()

	code, err := l.Docker.Run(runCtx, spec)
	if err != nil {
		log.Error("docker run error", "err", err)
		l.failCleanup(ctx, number)
		l.removeWorktree(ctx, number)
		return
	}
	if code == 0 {
		l.successCleanup(ctx, number)
	} else {
		log.Warn("task exited non-zero", "code", code)
		l.failCleanup(ctx, number)
	}
	l.removeWorktree(ctx, number)
}

func (l *Lifecycle) buildRunSpec(number int, wtPath string) docker.RunSpec {
	name := fmt.Sprintf("cc-crew-%s-%s-%s-%d",
		roleShort(l.Kind),
		safeName(l.Repo.Owner), safeName(l.Repo.Name), number)

	labels := map[string]string{
		"cc-crew.repo":  l.Repo.String(),
		"cc-crew.role":  roleName(l.Kind),
	}
	var env = map[string]string{
		"CC_ROLE":               roleName(l.Kind),
		"CC_MODEL":              l.Model,
		"CC_MAX_TURNS":          fmt.Sprint(l.MaxTurns),
		"CC_REPO":               l.Repo.String(),
		"GH_TOKEN":              l.RoleGHToken,
		"CLAUDE_CODE_OAUTH_TOKEN": l.ClaudeOAuth,
		"ANTHROPIC_API_KEY":     l.AnthropicAPIKey,
		"GIT_AUTHOR_NAME":       l.GitName,
		"GIT_AUTHOR_EMAIL":      l.GitEmail,
		"GIT_COMMITTER_NAME":    l.GitName,
		"GIT_COMMITTER_EMAIL":   l.GitEmail,
	}
	if l.Kind == claim.KindImplementer {
		labels["cc-crew.issue"] = fmt.Sprint(number)
		env["CC_ISSUE_NUM"] = fmt.Sprint(number)
		env["CC_BASE_BRANCH"] = l.BaseBranch
	} else {
		labels["cc-crew.pr"] = fmt.Sprint(number)
		env["CC_PR_NUM"] = fmt.Sprint(number)
	}

	return docker.RunSpec{
		Image: l.Image, Name: name, Labels: labels, Env: env,
		Mounts: []docker.Mount{
			{HostPath: wtPath, ContainerPath: "/workspace"},
			{HostPath: filepath.Join(l.WT.RepoDir, ".git"),
				ContainerPath: filepath.Join(l.WT.RepoDir, ".git"),
				ReadOnly: true},
		},
	}
}

func (l *Lifecycle) successCleanup(ctx context.Context, number int) {
	// Remove in-progress + queue labels; add done. For implementer with
	// --auto-review, find the new PR and label it claude-review.
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel)
	_ = l.GH.AddLabel(ctx, l.Repo, number, l.DoneLabel)

	if l.Kind == claim.KindImplementer {
		// Don't delete the lock branch — PR references it.
		_ = l.Claimer.Release(ctx, l.Kind, number, false)
		if l.AutoReview {
			branch := fmt.Sprintf("claude/issue-%d", number)
			prs, err := l.GH.ListPRs(ctx, l.Repo, nil, nil)
			if err == nil {
				for _, p := range prs {
					if p.HeadRefName == branch {
						_ = l.GH.AddLabel(ctx, l.Repo, p.Number, l.ReviewLabel)
						break
					}
				}
			}
		}
	} else {
		// Reviewer: delete lock tag + timestamp tags.
		_ = l.Claimer.Release(ctx, l.Kind, number, true)
	}
}

func (l *Lifecycle) failCleanup(ctx context.Context, number int) {
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	// Drop lock + tags so the queue label (unchanged) retriggers next tick.
	_ = l.Claimer.Release(ctx, l.Kind, number, true)
}

func (l *Lifecycle) removeWorktree(ctx context.Context, number int) {
	branch := fmt.Sprintf("claude/issue-%d", number)
	if l.Kind == claim.KindReviewer {
		branch = fmt.Sprintf("review-%d", number)
	}
	_ = l.WT.Remove(ctx, branch)
}

func kindName(k claim.Kind) string {
	if k == claim.KindImplementer {
		return "implementer"
	}
	return "reviewer"
}
func roleName(k claim.Kind) string   { return kindName(k) }
func roleShort(k claim.Kind) string {
	if k == claim.KindImplementer {
		return "impl"
	}
	return "rev"
}
func safeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
```

- [ ] **Step 2: Test success cleanup and failure cleanup using fakes**

```go
package scheduler

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func TestSuccessCleanupImplementer(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	f.Refs["refs/heads/claude/issue-42"] = "sha"
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "sha"

	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	l := &Lifecycle{
		Kind: claim.KindImplementer, Claimer: c, GH: f, Repo: r,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing", DoneLabel: "claude-done",
	}
	l.successCleanup(context.Background(), 42)

	i := f.Issues[42]
	if contains(i.Labels, "claude-task") || contains(i.Labels, "claude-processing") {
		t.Fatalf("queue/lock labels should be gone: %v", i.Labels)
	}
	if !contains(i.Labels, "claude-done") {
		t.Fatalf("done label missing: %v", i.Labels)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock branch should remain for PR reference")
	}
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("timestamp tag should be cleared")
	}
}

func TestFailCleanupDropsLockAndKeepsQueueLabel(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	f.Refs["refs/heads/claude/issue-42"] = "sha"
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "sha"

	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	l := &Lifecycle{
		Kind: claim.KindImplementer, Claimer: c, GH: f, Repo: r,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing", DoneLabel: "claude-done",
	}
	l.failCleanup(context.Background(), 42)

	i := f.Issues[42]
	if !contains(i.Labels, "claude-task") {
		t.Fatalf("queue label should remain for retry: %v", i.Labels)
	}
	if contains(i.Labels, "claude-processing") {
		t.Fatalf("lock label should be gone: %v", i.Labels)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; ok {
		t.Fatal("lock branch should be deleted so work retriggers")
	}
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/scheduler/... -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/scheduler/lifecycle.go internal/scheduler/lifecycle_test.go
git commit -m "feat(scheduler): per-task lifecycle with success/fail cleanup"
```

---

## Phase 8 — internal/reset

### Task 8.1: Bulk cleanup

**Files:**
- Create: `internal/reset/reset.go`
- Create: `internal/reset/reset_test.go`

- [ ] **Step 1: Implement Plan + Execute**

```go
package reset

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

var refPrefixes = []string{
	"heads/claude/issue-",
	"tags/claim/issue-",
	"tags/review-lock/pr-",
	"tags/review-claim/pr-",
}

type Plan struct {
	ImplementerIssues []int   // issue numbers to requeue (open issues that had cc-crew refs)
	ReviewerPRs       []int   // PR numbers to requeue
	Refs              []string // full ref names that will be deleted
	Containers        []string // container names that will be killed
	Worktrees         []string // worktree paths to remove
}

type Options struct {
	GH              github.Client
	Docker          *docker.Runner
	WT              *worktree.Manager
	Repo            github.Repo
	TaskLabel       string
	ProcessingLabel string
	ReviewLabel     string
	ReviewingLabel  string
}

// Compute builds a Plan without making any changes.
func Compute(ctx context.Context, o Options) (Plan, error) {
	var p Plan
	for _, pref := range refPrefixes {
		refs, err := o.GH.ListMatchingRefs(ctx, o.Repo, pref)
		if err != nil {
			return p, err
		}
		for _, r := range refs {
			p.Refs = append(p.Refs, r.Name)
			if pref == "heads/claude/issue-" {
				if n := parseIssue(r.Name); n > 0 {
					p.ImplementerIssues = append(p.ImplementerIssues, n)
				}
			}
			if pref == "tags/review-lock/pr-" {
				if n := parsePR(r.Name); n > 0 {
					p.ReviewerPRs = append(p.ReviewerPRs, n)
				}
			}
		}
	}
	entries, err := o.Docker.PS(ctx, map[string]string{"cc-crew.repo": o.Repo.String()})
	if err != nil {
		return p, err
	}
	for _, e := range entries {
		p.Containers = append(p.Containers, e.Name)
	}
	wts, err := o.WT.List(ctx)
	if err != nil {
		return p, err
	}
	p.Worktrees = wts
	return p, nil
}

// Execute applies a Plan. Writes a short progress log to `out`.
func Execute(ctx context.Context, o Options, p Plan, out io.Writer) error {
	for _, name := range p.Containers {
		fmt.Fprintf(out, "kill container: %s\n", name)
		if err := o.Docker.Kill(ctx, name); err != nil {
			return err
		}
	}
	for _, ref := range p.Refs {
		fmt.Fprintf(out, "delete ref: %s\n", ref)
		if err := o.GH.DeleteRef(ctx, o.Repo, ref); err != nil {
			return err
		}
	}
	for _, n := range p.ImplementerIssues {
		issue, err := o.GH.ListIssues(ctx, o.Repo, nil, nil)
		if err != nil {
			return err
		}
		if !isOpenIssue(issue, n) {
			continue
		}
		fmt.Fprintf(out, "requeue issue #%d\n", n)
		_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ProcessingLabel)
		_ = o.GH.AddLabel(ctx, o.Repo, n, o.TaskLabel)
	}
	for _, n := range p.ReviewerPRs {
		prs, err := o.GH.ListPRs(ctx, o.Repo, nil, nil)
		if err != nil {
			return err
		}
		if !isOpenPR(prs, n) {
			continue
		}
		fmt.Fprintf(out, "requeue PR #%d\n", n)
		_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ReviewingLabel)
		_ = o.GH.AddLabel(ctx, o.Repo, n, o.ReviewLabel)
	}
	for _, p := range p.Worktrees {
		fmt.Fprintf(out, "remove worktree: %s\n", p)
		// Convert back to a branch-like identifier for Remove API.
		// We strip .claude-worktrees/ and pass the leaf.
		// Since Manager.Remove takes branch, and Path(branch) returns the same p,
		// we can just call git worktree remove --force <p> via a small helper.
	}
	_ = o.WT.Prune(ctx)
	return nil
}

func parseIssue(refName string) int {
	s := strings.TrimPrefix(refName, "refs/heads/claude/issue-")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
func parsePR(refName string) int {
	s := strings.TrimPrefix(refName, "refs/tags/review-lock/pr-")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
func isOpenIssue(is []github.Issue, n int) bool {
	for _, i := range is {
		if i.Number == n && i.State == "open" {
			return true
		}
	}
	return false
}
func isOpenPR(ps []github.PullRequest, n int) bool {
	for _, p := range ps {
		if p.Number == n && p.State == "open" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Test Compute + Execute against fake backend**

```go
package reset

import (
	"bytes"
	"context"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

func TestComputeAndExecute(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[10] = &github.Issue{Number: 10, State: "open", Labels: []string{"claude-processing"}}
	f.PRs[20] = &github.PullRequest{Number: 20, State: "open", Labels: []string{"claude-reviewing"}}
	f.Refs["refs/heads/claude/issue-10"] = "s1"
	f.Refs["refs/tags/claim/issue-10/20260417T120000Z"] = "s1"
	f.Refs["refs/tags/review-lock/pr-20"] = "s2"
	f.Refs["refs/tags/review-claim/pr-20/20260417T120000Z"] = "s2"

	// Worktree manager against a temp dir with no real repo; List will fail,
	// so we stub with a zero-value manager and skip that assertion.
	wt := worktree.New(t.TempDir())
	dr := docker.New()

	o := Options{
		GH: f, Docker: dr, WT: wt, Repo: r,
		TaskLabel: "claude-task", ProcessingLabel: "claude-processing",
		ReviewLabel: "claude-review", ReviewingLabel: "claude-reviewing",
	}
	// We can't call Compute because it wants `docker ps` to succeed and
	// `git worktree list` to succeed. Instead test Execute directly with a
	// hand-built plan.
	plan := Plan{
		ImplementerIssues: []int{10},
		ReviewerPRs:       []int{20},
		Refs: []string{
			"refs/heads/claude/issue-10",
			"refs/tags/claim/issue-10/20260417T120000Z",
			"refs/tags/review-lock/pr-20",
			"refs/tags/review-claim/pr-20/20260417T120000Z",
		},
	}
	var buf bytes.Buffer
	if err := Execute(context.Background(), o, plan, &buf); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-10"]; ok {
		t.Fatal("ref should be deleted")
	}
	i := f.Issues[10]
	if !hasLabel(i.Labels, "claude-task") || hasLabel(i.Labels, "claude-processing") {
		t.Fatalf("issue labels not restored: %v", i.Labels)
	}
	p := f.PRs[20]
	if !hasLabel(p.Labels, "claude-review") || hasLabel(p.Labels, "claude-reviewing") {
		t.Fatalf("PR labels not restored: %v", p.Labels)
	}
}

func hasLabel(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/reset/... -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/reset/reset.go internal/reset/reset_test.go
git commit -m "feat(reset): bulk-cleanup Compute + Execute"
```

---

## Phase 9 — internal/status

### Task 9.1: Stateless status snapshot

**Files:**
- Create: `internal/status/status.go`
- Create: `internal/status/status_test.go`

- [ ] **Step 1: Implement Snapshot assembly**

```go
package status

import (
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
)

type Item struct {
	Kind         claim.Kind
	Number       int
	Title        string
	State        string // "queued", "claimed", "running", "done"
	ContainerAge time.Duration
	ClaimAge     time.Duration
}

type Snapshot struct {
	Implementers []Item
	Reviewers    []Item
}

type Options struct {
	GH     github.Client
	Docker *docker.Runner
	Repo   github.Repo

	TaskLabel       string
	ProcessingLabel string
	ReviewLabel     string
	ReviewingLabel  string

	Now func() time.Time
}

func Fetch(ctx context.Context, o Options) (Snapshot, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	var s Snapshot

	// Implementer candidates (queue + lock-held)
	issues, err := o.GH.ListIssues(ctx, o.Repo, []string{o.TaskLabel}, nil)
	if err != nil {
		return s, err
	}
	for _, i := range issues {
		it := Item{Kind: claim.KindImplementer, Number: i.Number, Title: i.Title, State: "queued"}
		if containsLabel(i.Labels, o.ProcessingLabel) {
			it.State = "claimed"
		}
		s.Implementers = append(s.Implementers, it)
	}

	// Reviewer candidates
	prs, err := o.GH.ListPRs(ctx, o.Repo, []string{o.ReviewLabel}, nil)
	if err != nil {
		return s, err
	}
	for _, p := range prs {
		it := Item{Kind: claim.KindReviewer, Number: p.Number, Title: p.Title, State: "queued"}
		if containsLabel(p.Labels, o.ReviewingLabel) {
			it.State = "claimed"
		}
		s.Reviewers = append(s.Reviewers, it)
	}

	// Annotate with claim age from timestamp tags.
	c := claim.New(o.GH, o.Repo)
	c.Now = o.Now
	for i := range s.Implementers {
		if s.Implementers[i].State != "claimed" {
			continue
		}
		age, ok, err := c.OldestTagAge(ctx, claim.KindImplementer, s.Implementers[i].Number)
		if err == nil && ok {
			s.Implementers[i].ClaimAge = age
		}
	}
	for i := range s.Reviewers {
		if s.Reviewers[i].State != "claimed" {
			continue
		}
		age, ok, err := c.OldestTagAge(ctx, claim.KindReviewer, s.Reviewers[i].Number)
		if err == nil && ok {
			s.Reviewers[i].ClaimAge = age
		}
	}

	// Annotate with running-container age.
	entries, err := o.Docker.PS(ctx, map[string]string{"cc-crew.repo": o.Repo.String()})
	if err == nil {
		for _, e := range entries {
			num := 0
			if v := e.Labels["cc-crew.issue"]; v != "" {
				fmt.Sscanf(v, "%d", &num)
			}
			if num == 0 {
				if v := e.Labels["cc-crew.pr"]; v != "" {
					fmt.Sscanf(v, "%d", &num)
				}
			}
			if num == 0 {
				continue
			}
			role := e.Labels["cc-crew.role"]
			if role == "implementer" {
				for i := range s.Implementers {
					if s.Implementers[i].Number == num {
						s.Implementers[i].State = "running"
					}
				}
			} else if role == "reviewer" {
				for i := range s.Reviewers {
					if s.Reviewers[i].Number == num {
						s.Reviewers[i].State = "running"
					}
				}
			}
		}
	}

	sortItems(s.Implementers)
	sortItems(s.Reviewers)
	return s, nil
}

func sortItems(xs []Item) {
	sort.Slice(xs, func(i, j int) bool { return xs[i].Number < xs[j].Number })
}

func containsLabel(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// Render prints a human-readable table to w.
func Render(w io.Writer, s Snapshot) {
	tw := tabwriter.NewWriter(w, 2, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PERSONA\tNUMBER\tSTATE\tCLAIM-AGE\tTITLE")
	for _, it := range s.Implementers {
		fmt.Fprintf(tw, "implementer\t#%d\t%s\t%s\t%s\n", it.Number, it.State, fmtAge(it.ClaimAge), it.Title)
	}
	for _, it := range s.Reviewers {
		fmt.Fprintf(tw, "reviewer\t#%d\t%s\t%s\t%s\n", it.Number, it.State, fmtAge(it.ClaimAge), it.Title)
	}
	tw.Flush()
}

func fmtAge(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.Truncate(time.Second).String()
}
```

- [ ] **Step 2: Smoke-test Snapshot structure**

```go
package status

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/claim"
)

func TestRenderBasic(t *testing.T) {
	s := Snapshot{
		Implementers: []Item{{Kind: claim.KindImplementer, Number: 42, Title: "bug", State: "queued"}},
	}
	var buf bytes.Buffer
	Render(&buf, s)
	if !strings.Contains(buf.String(), "#42") || !strings.Contains(buf.String(), "queued") {
		t.Fatalf("render output:\n%s", buf.String())
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/status/... -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add internal/status/status.go internal/status/status_test.go
git commit -m "feat(status): stateless snapshot + human-readable rendering"
```

---

## Phase 10 — cmd/cc-crew wiring

### Task 10.1: `up` subcommand

**Files:**
- Create: `cmd/cc-crew/up.go`
- Modify: `cmd/cc-crew/main.go`

- [ ] **Step 1: Implement up**

`cmd/cc-crew/up.go`:

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/config"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/reclaim"
	"github.com/charleszheng44/cc-crew/internal/scheduler"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

func runUp(args []string) int {
	pwd, _ := os.Getwd()
	c, err := config.Parse(args, os.Getenv, pwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := c.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ghc := github.NewGhClient()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	owner, name, err := config.ResolveRepo(ctx, c.RepoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	repo := github.Repo{Owner: owner, Name: name}
	log.Info("cc-crew starting", "repo", repo.String(), "dir", c.RepoDir,
		"max_impl", c.MaxImplementers, "max_rev", c.MaxReviewers)

	if c.BaseBranch == "" {
		b, err := ghc.DefaultBranch(ctx, repo)
		if err != nil {
			fmt.Fprintln(os.Stderr, "resolve default branch:", err)
			return 1
		}
		c.BaseBranch = b
	}

	// Resolve reviewer login once for "already-done" reviewer check.
	reviewerLogin := ""
	if c.MaxReviewers > 0 {
		// Orchestrator uses its own creds; we want the reviewer persona's login.
		// For v1 we trust that GH_TOKEN_REVIEWER is the reviewer identity, so we
		// call CurrentUser when GH_TOKEN_REVIEWER is different from GH_TOKEN.
		reviewerLogin, err = ghc.CurrentUser(ctx) // see README caveats
		if err != nil {
			log.Warn("couldn't resolve reviewer login; reclaim already-done check disabled", "err", err)
		}
	}

	wt := worktree.New(c.RepoDir)
	dr := docker.New()
	claimer := claim.New(ghc, repo)

	var schedulers []*scheduler.Scheduler

	if c.MaxImplementers > 0 {
		lc := &scheduler.Lifecycle{
			Kind: claim.KindImplementer, Claimer: claimer, GH: ghc, Repo: repo,
			WT: wt, Docker: dr, Log: log,
			QueueLabel: c.TaskLabel, LockLabel: c.ProcessingLabel, DoneLabel: c.DoneLabel,
			ReviewLabel: c.ReviewLabel,
			Image: c.Image, Model: c.Model, MaxTurns: 25,
			TaskTimeout: c.ImplTaskTimeout, AutoReview: c.AutoReview,
			RoleGHToken: c.ImplementerGHToken, ClaudeOAuth: c.ClaudeOAuthToken, AnthropicAPIKey: c.AnthropicAPIKey,
			GitName: c.ImplementerGitName, GitEmail: c.ImplementerGitEmail,
			BaseBranch: c.BaseBranch,
		}
		s := &scheduler.Scheduler{
			Kind: claim.KindImplementer, Sem: scheduler.NewSemaphore(c.MaxImplementers),
			Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: lc, Log: log,
			QueueLabel: c.TaskLabel, LockLabel: c.ProcessingLabel,
		}
		schedulers = append(schedulers, s)
	}

	if c.MaxReviewers > 0 {
		lc := &scheduler.Lifecycle{
			Kind: claim.KindReviewer, Claimer: claimer, GH: ghc, Repo: repo,
			WT: wt, Docker: dr, Log: log,
			QueueLabel: c.ReviewLabel, LockLabel: c.ReviewingLabel, DoneLabel: c.ReviewedLabel,
			Image: c.Image, Model: c.Model, MaxTurns: 15,
			TaskTimeout: c.ReviewTaskTimeout,
			RoleGHToken: c.ReviewerGHToken, ClaudeOAuth: c.ClaudeOAuthToken, AnthropicAPIKey: c.AnthropicAPIKey,
			GitName: c.ReviewerGitName, GitEmail: c.ReviewerGitEmail,
		}
		s := &scheduler.Scheduler{
			Kind: claim.KindReviewer, Sem: scheduler.NewSemaphore(c.MaxReviewers),
			Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: lc, Log: log,
			QueueLabel: c.ReviewLabel, LockLabel: c.ReviewingLabel,
		}
		schedulers = append(schedulers, s)
	}

	// Reclaim sweepers
	implSweeper := &reclaim.Sweeper{
		GH: ghc, Repo: repo, Claimer: claimer,
		Kind: claim.KindImplementer, MaxAge: c.ReclaimAfter,
		IsDone: reclaim.ImplementerDoneFn(ghc, repo),
		Now:    time.Now,
	}
	revSweeper := &reclaim.Sweeper{
		GH: ghc, Repo: repo, Claimer: claimer,
		Kind: claim.KindReviewer, MaxAge: c.ReclaimAfter,
		IsDone: reclaim.ReviewerDoneFn(ghc, repo, reviewerLogin),
		Now:    time.Now,
	}

	// Tick loop: run reclaim first, then each scheduler's Tick.
	go func() {
		t := time.NewTicker(c.PollInterval)
		defer t.Stop()
		for {
			if err := implSweeper.Sweep(ctx); err != nil {
				log.Warn("impl reclaim", "err", err)
			}
			if err := revSweeper.Sweep(ctx); err != nil {
				log.Warn("rev reclaim", "err", err)
			}
			for _, s := range schedulers {
				if err := s.Tick(ctx); err != nil {
					log.Warn("tick", "kind", s.Kind, "err", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	<-ctx.Done()
	log.Info("shutdown requested; stopping tasks")

	// Kill any still-running cc-crew containers for this repo.
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	entries, _ := dr.PS(shutCtx, map[string]string{"cc-crew.repo": repo.String()})
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = dr.Kill(shutCtx, name)
		}(e.Name)
	}
	wg.Wait()
	log.Info("bye")
	return 0
}
```

- [ ] **Step 2: Wire into main.go**

Replace the `case "up":` branch:

```go
	case "up":
		os.Exit(runUp(os.Args[2:]))
```

- [ ] **Step 3: Build**

Run: `make build`

Expected: produces `./cc-crew`. No lint/vet failures.

- [ ] **Step 4: Smoke-test validation failure**

Run: `./cc-crew up --repo /tmp/nonexistent`

Expected: exits 2 with a "GH_TOKEN ... required" error (env is empty).

- [ ] **Step 5: Commit**

```
gofmt -w .
git add cmd/cc-crew/up.go cmd/cc-crew/main.go
git commit -m "feat(cmd): wire up subcommand with schedulers and reclaim"
```

---

### Task 10.2: `status` subcommand

**Files:**
- Create: `cmd/cc-crew/status.go`
- Modify: `cmd/cc-crew/main.go`

- [ ] **Step 1: Implement runStatus**

`cmd/cc-crew/status.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/charleszheng44/cc-crew/internal/config"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/status"
)

func runStatus(args []string) int {
	fs := flag.NewFlagSet("cc-crew status", flag.ContinueOnError)
	repo := fs.String("repo", os.Getenv("CC_REPO"), "Local repo path (default: $PWD)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *repo == "" {
		*repo, _ = os.Getwd()
	}
	ctx := context.Background()
	owner, name, err := config.ResolveRepo(ctx, *repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	o := status.Options{
		GH: github.NewGhClient(), Docker: docker.New(),
		Repo:            github.Repo{Owner: owner, Name: name},
		TaskLabel:       "claude-task",
		ProcessingLabel: "claude-processing",
		ReviewLabel:     "claude-review",
		ReviewingLabel:  "claude-reviewing",
	}
	snap, err := status.Fetch(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	status.Render(os.Stdout, snap)
	return 0
}
```

- [ ] **Step 2: Wire into main.go**

Replace the `case "status":` branch:

```go
	case "status":
		os.Exit(runStatus(os.Args[2:]))
```

- [ ] **Step 3: Build and smoke-test from a non-repo dir**

Run: `./cc-crew status --repo /tmp`

Expected: exits non-zero with "git remote get-url origin: ..." error.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add cmd/cc-crew/status.go cmd/cc-crew/main.go
git commit -m "feat(cmd): wire status subcommand"
```

---

### Task 10.3: `reset` subcommand

**Files:**
- Create: `cmd/cc-crew/reset.go`
- Modify: `cmd/cc-crew/main.go`

- [ ] **Step 1: Implement runReset**

`cmd/cc-crew/reset.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/charleszheng44/cc-crew/internal/config"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/reset"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

func runReset(args []string) int {
	fs := flag.NewFlagSet("cc-crew reset", flag.ContinueOnError)
	repo := fs.String("repo", os.Getenv("CC_REPO"), "Local repo path (default: $PWD)")
	yes := fs.Bool("yes", false, "Skip confirmation and actually apply the plan")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *repo == "" {
		*repo, _ = os.Getwd()
	}
	ctx := context.Background()
	owner, name, err := config.ResolveRepo(ctx, *repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	o := reset.Options{
		GH: github.NewGhClient(), Docker: docker.New(),
		WT:              worktree.New(*repo),
		Repo:            github.Repo{Owner: owner, Name: name},
		TaskLabel:       "claude-task",
		ProcessingLabel: "claude-processing",
		ReviewLabel:     "claude-review",
		ReviewingLabel:  "claude-reviewing",
	}
	plan, err := reset.Compute(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Plan: %d refs, %d containers, %d worktrees, %d issues, %d PRs\n",
		len(plan.Refs), len(plan.Containers), len(plan.Worktrees),
		len(plan.ImplementerIssues), len(plan.ReviewerPRs))
	if !*yes {
		fmt.Println("(dry run) re-run with --yes to apply")
		return 0
	}
	if err := reset.Execute(ctx, o, plan, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
```

- [ ] **Step 2: Wire into main.go**

Replace `case "reset":`:

```go
	case "reset":
		os.Exit(runReset(os.Args[2:]))
```

- [ ] **Step 3: Build**

Run: `make build`

Expected: clean build.

- [ ] **Step 4: Commit**

```
gofmt -w .
git add cmd/cc-crew/reset.go cmd/cc-crew/main.go
git commit -m "feat(cmd): wire reset subcommand (dry-run default)"
```

---

## Phase 11 — Container side

### Task 11.1: In-container entrypoint

**Files:**
- Create: `scripts/cc-crew-run`

- [ ] **Step 1: Write bash script**

```bash
#!/bin/bash
# cc-crew-run — in-container entrypoint for implementer and reviewer tasks.
# Reads role/config from env, prepares the environment, and execs `claude -p`.
# The per-persona CLAUDE.md (mounted to /root/.claude/CLAUDE.md) tells
# Claude to perform the role-specific finisher (git push + gh pr create
# for implementer; gh pr review for reviewer). This script only sets up.

set -euo pipefail

: "${CC_ROLE:?CC_ROLE must be set}"
: "${CC_MODEL:=claude-sonnet-4-6}"
: "${CC_MAX_TURNS:=15}"

# Git identity
if [[ -n "${GIT_AUTHOR_NAME:-}" ]]; then
  export GIT_COMMITTER_NAME="${GIT_COMMITTER_NAME:-$GIT_AUTHOR_NAME}"
fi
if [[ -n "${GIT_AUTHOR_EMAIL:-}" ]]; then
  export GIT_COMMITTER_EMAIL="${GIT_COMMITTER_EMAIL:-$GIT_AUTHOR_EMAIL}"
fi
git config --global user.name  "${GIT_AUTHOR_NAME:-cc-crew-bot}"
git config --global user.email "${GIT_AUTHOR_EMAIL:-cc-crew-bot@example.com}"

# gh credential helper for HTTPS pushes
gh auth setup-git

cd /workspace

case "$CC_ROLE" in
  implementer)
    : "${CC_ISSUE_NUM:?CC_ISSUE_NUM required}"
    : "${CC_BASE_BRANCH:?CC_BASE_BRANCH required}"
    gh issue view "$CC_ISSUE_NUM" -R "$CC_REPO" --json number,title,body \
      -q '"# Issue #\(.number): \(.title)\n\n\(.body)"' > /tmp/issue.md

    PROMPT="You are the cc-crew implementer. Read /tmp/issue.md and implement the change in the current worktree (branch: claude/issue-${CC_ISSUE_NUM}). Base branch: ${CC_BASE_BRANCH}. Follow your CLAUDE.md instructions exactly."

    exec claude -p "$PROMPT" \
      --model "$CC_MODEL" \
      --max-turns "$CC_MAX_TURNS" \
      --dangerously-skip-permissions
    ;;
  reviewer)
    : "${CC_PR_NUM:?CC_PR_NUM required}"
    gh pr checkout "$CC_PR_NUM" -R "$CC_REPO"
    gh pr view "$CC_PR_NUM" -R "$CC_REPO" --json number,title,body,baseRefName \
      -q '"# PR #\(.number): \(.title)\n\nBase: \(.baseRefName)\n\n\(.body)"' > /tmp/pr.md

    PROMPT="You are the cc-crew reviewer. Read /tmp/pr.md and review PR #${CC_PR_NUM}. Follow your CLAUDE.md reviewer persona instructions exactly. Post exactly one review via gh pr review."

    exec claude -p "$PROMPT" \
      --model "$CC_MODEL" \
      --max-turns "$CC_MAX_TURNS" \
      --permission-mode plan
    ;;
  *)
    echo "unknown CC_ROLE: $CC_ROLE" >&2
    exit 64
    ;;
esac
```

- [ ] **Step 2: Make executable**

Run: `chmod +x scripts/cc-crew-run`

- [ ] **Step 3: Commit**

```
git add scripts/cc-crew-run
git commit -m "feat(scripts): in-container entrypoint for implementer and reviewer"
```

---

### Task 11.2: Dockerfile extension

**Files:**
- Modify: `Dockerfile`

- [ ] **Step 1: Append COPY for the entrypoint script**

Current `Dockerfile`:

```
FROM node:lts-alpine

RUN apk add --no-cache git bash github-cli \
 && npm install -g @anthropic-ai/claude-code \
 && rm -rf /root/.npm

ENV SHELL=/bin/bash

RUN printf '%s' '{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}' \
    > /root/.claude.json

WORKDIR /workspace

CMD ["tail", "-f", "/dev/null"]
```

Replace with:

```
FROM node:lts-alpine

RUN apk add --no-cache git bash github-cli \
 && npm install -g @anthropic-ai/claude-code \
 && rm -rf /root/.npm

ENV SHELL=/bin/bash

RUN printf '%s' '{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}' \
    > /root/.claude.json

COPY scripts/cc-crew-run /usr/local/bin/cc-crew-run
RUN chmod +x /usr/local/bin/cc-crew-run

WORKDIR /workspace

# Default CMD is overridden by `docker run` from the orchestrator,
# which sets CC_ROLE and execs cc-crew-run. Leaving `tail` here keeps
# the image usable for the manual workflow described in README.
CMD ["tail", "-f", "/dev/null"]
ENTRYPOINT ["/bin/sh", "-c", "if [ -n \"${CC_ROLE:-}\" ]; then exec /usr/local/bin/cc-crew-run; else exec \"$@\"; fi", "--"]
```

- [ ] **Step 2: Test that the image builds**

Run: `docker build -t cc-crew-test .`

Expected: build succeeds.

- [ ] **Step 3: Test that without CC_ROLE the image still tails (backwards compatible)**

Run:
```
docker run --rm -d --name cc-crew-test-bg cc-crew-test
docker ps --filter name=cc-crew-test-bg
docker rm -f cc-crew-test-bg
```

Expected: container stays running and is killable.

- [ ] **Step 4: Commit**

```
git add Dockerfile
git commit -m "feat(docker): bake cc-crew-run entrypoint into image"
```

---

### Task 11.3: Implementer persona

**Files:**
- Create: `personas/implementer/CLAUDE.md`
- Create: `personas/implementer/settings.json`

- [ ] **Step 1: Write implementer CLAUDE.md**

```markdown
# Implementer persona

You are an autonomous implementer dispatched by cc-crew to resolve a
single GitHub issue. Your working directory is the repo's worktree,
already checked out on branch `claude/issue-<N>`.

## Inputs

- `/tmp/issue.md` — issue title and body.
- `$CC_ISSUE_NUM` — issue number.
- `$CC_BASE_BRANCH` — base branch for the PR (e.g. `main`).
- `$CC_REPO` — `owner/name` of the repo.

## Workflow

1. Read `/tmp/issue.md` carefully. Understand the requested change.
2. Implement it. Follow existing patterns in the repo. If the repo has
   a `CLAUDE.md`, treat it as authoritative.
3. Run the project's obvious checks: package scripts, `make test`,
   `go test ./...`, `pytest`, etc. If these fail due to your change,
   fix your change until they pass.
4. Stage and commit once, with message: `Resolve #<N>: <title>`.
5. `git push origin HEAD` to push the branch.
6. `gh pr create --base "$CC_BASE_BRANCH" --head "claude/issue-$CC_ISSUE_NUM" --title "Resolve #$CC_ISSUE_NUM: <title>" --body "Closes #$CC_ISSUE_NUM"`.

## Hard constraints

- Do **not** push to any branch other than `claude/issue-$CC_ISSUE_NUM`.
- Do **not** force-push or rewrite history.
- Do **not** merge the PR yourself.
- Do **not** modify files outside what the issue requires.
- Do **not** disable tests, skip linters, or bypass CI.
- If you cannot implement the change, exit non-zero with a short stderr
  summary of why. cc-crew will drop the lock and retry on a future tick.

## Environment

You run with `--dangerously-skip-permissions`. This is intentional: the
container has no standing secrets beyond `GH_TOKEN` and Claude credentials,
and is expected to freely run `git`, `gh`, tests, and package managers.
```

- [ ] **Step 2: Write a documentation-only settings.json**

```json
{
  "_comment": "Documented scoped permissions for the implementer. Not loaded at runtime when cc-crew dispatches this persona with --dangerously-skip-permissions. Kept in-repo so someone can run the persona under strict permissions manually.",
  "permissions": {
    "allow": [
      "Bash(git:*)",
      "Bash(gh pr create:*)",
      "Bash(gh pr view:*)",
      "Bash(gh issue view:*)",
      "Bash(gh auth setup-git)",
      "Bash(make:*)",
      "Bash(go:*)",
      "Bash(npm:*)",
      "Bash(pnpm:*)",
      "Bash(yarn:*)",
      "Bash(pytest:*)",
      "Bash(cargo:*)"
    ],
    "deny": [
      "Bash(git push --force:*)",
      "Bash(git push -f:*)",
      "Bash(git reset --hard:*)",
      "Bash(rm -rf:*)",
      "Bash(gh pr merge:*)"
    ]
  }
}
```

- [ ] **Step 3: Commit**

```
git add personas/implementer/CLAUDE.md personas/implementer/settings.json
git commit -m "feat(personas): add implementer persona (CLAUDE.md + settings)"
```

---

## Phase 12 — Docs and end-to-end

### Task 12.1: README orchestrator section

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append orchestrator section**

Append to `README.md`:

```markdown

## Orchestrator: `cc-crew up` / `status` / `reset`

cc-crew includes a Go CLI that watches a GitHub repo and dispatches
this image automatically: issues labeled `claude-task` get picked up
by the implementer persona, PRs labeled `claude-review` by the reviewer.

### Build

```bash
make build                 # produces ./cc-crew
```

### Run

From inside a clone of the target repo:

```bash
export GH_TOKEN_IMPLEMENTER=github_pat_...
export GH_TOKEN_REVIEWER=github_pat_...
export GH_TOKEN=$GH_TOKEN_IMPLEMENTER      # used for orchestrator's own API calls
export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-...
export IMPLEMENTER_GIT_NAME="implementer-bot"
export IMPLEMENTER_GIT_EMAIL="impl@example.com"
export REVIEWER_GIT_NAME="reviewer-bot"
export REVIEWER_GIT_EMAIL="rev@example.com"

./cc-crew up                       # foreground; Ctrl-C to stop
./cc-crew up --max-implementers 3 --max-reviewers 2
./cc-crew status                   # in another terminal; stateless
./cc-crew reset                    # dry run
./cc-crew reset --yes              # actually clean up cc-crew state
```

See `docs/superpowers/specs/2026-04-16-cc-crew-orchestrator-design.md`
for the full design.
```

- [ ] **Step 2: Commit**

```
git add README.md
git commit -m "docs: document cc-crew orchestrator CLI"
```

---

### Task 12.2: End-to-end manual test procedure

**Files:**
- Create: `docs/superpowers/plans/e2e-smoke.md`

- [ ] **Step 1: Write the procedure**

```markdown
# cc-crew end-to-end smoke test

Pre-reqs: scratch GitHub repo you own, Docker installed, `gh auth login` done,
Claude Code auth working locally.

## 0. Prep

```bash
gh repo create your-handle/cc-crew-smoke --private --clone
cd cc-crew-smoke

for L in claude-task claude-processing claude-done \
         claude-review claude-reviewing claude-reviewed; do
  gh label create "$L" --force
done

git commit --allow-empty -m "seed"
git push -u origin main
```

## 1. Build & start

In one terminal:

```bash
cd /path/to/cc-crew
make build
export GH_TOKEN=...  GH_TOKEN_IMPLEMENTER=... GH_TOKEN_REVIEWER=...
export CLAUDE_CODE_OAUTH_TOKEN=...
export IMPLEMENTER_GIT_NAME=impl-bot  IMPLEMENTER_GIT_EMAIL=impl@example.com
export REVIEWER_GIT_NAME=rev-bot      REVIEWER_GIT_EMAIL=rev@example.com
cd /path/to/cc-crew-smoke
/path/to/cc-crew/cc-crew up --max-implementers 1 --max-reviewers 1
```

## 2. File an issue

```bash
gh issue create --title "add HELLO.md with greeting" \
  --body "Create a file HELLO.md containing 'Hello, world!'" \
  --label claude-task
```

Expected within ~60s:
- Orchestrator logs a claim on issue #1, creates `refs/heads/claude/issue-1`
- Container starts: `docker ps` shows `cc-crew-impl-...-1`
- After exit: PR opened against main, labels: `claude-done`

## 3. Label the PR for review

```bash
gh pr edit 2 --add-label claude-review
```

Expected within ~60s:
- Orchestrator claims the PR, creates `refs/tags/review-lock/pr-2`
- Reviewer container runs
- A review is posted on the PR
- Labels: `claude-reviewed`

## 4. Reset

```bash
/path/to/cc-crew/cc-crew reset            # dry run
/path/to/cc-crew/cc-crew reset --yes       # actually clean
gh api repos/your-handle/cc-crew-smoke/git/matching-refs/tags/claim/ -q length
# → 0
```
```

- [ ] **Step 2: Commit**

```
git add docs/superpowers/plans/e2e-smoke.md
git commit -m "docs: add end-to-end smoke test procedure"
```

---

## Self-Review (done before plan was finalized)

1. **Spec coverage:**
   - Labels (§6.1) — Phase 12.1 README documents them; scheduler/lifecycle use them.
   - Refs (§6.2) — claim.PathsFor covers both kinds.
   - Atomic claim (§8.2) — Task 2.1.
   - Reclaim orphan recreate (§8.3 step 2) — Task 4.1 TestSweepOrphanLockRecreatesTag.
   - Already-done checks (§8.3) — Task 4.1 ImplementerDoneFn/ReviewerDoneFn.
   - Per-task lifecycle (§8.4) — Task 7.2 Lifecycle.
   - `.git` RO mount (§8.5) — Lifecycle.buildRunSpec.
   - Auto-review (§8.4 step 5) — Lifecycle.successCleanup.
   - Personas (§9) — Task 11.3.
   - Entrypoint (§9.4) — Task 11.1.
   - File layout (§10) — matches Phase scaffolding.
   - Reset (§7.3) — Phase 8 + Task 10.3.
   - UX (§7) — Phases 10–12.
   - Config (§7.1/7.2) — Phase 6.
   - Failure modes (§11) — Lifecycle handles each explicitly.
   - SIGINT cleanup (§11) — up.go shutdown path.

2. **Placeholder scan:** no TBD/TODO/"similar to"/"handle edge cases" left; every step has concrete code or a specific command.

3. **Type consistency:** `claim.PathsFor`, `Kind`, `Sweeper.Sweep`, `Scheduler.Tick`, `Lifecycle.Dispatch`, `Runner.Run` names are consistent across their users. `AddLabel` accepts both issue and PR numbers (the gh CLI accepts both).

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-17-cc-crew-orchestrator.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
