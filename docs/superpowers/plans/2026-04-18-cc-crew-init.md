# cc-crew init Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `cc-crew init` subcommand that creates the nine cc-crew lifecycle labels on the target GitHub repo, idempotently, honoring env-var overrides.

**Architecture:** Add a `CreateLabel` method (plus `ErrLabelExists` sentinel) to the existing `github.Client` interface, implement it on `*ghClient` (production, via `gh api`) and `*FakeClient` (tests). Add a new `cmd/cc-crew/init.go` that builds a label catalog at runtime from `config.Defaults()` + env vars, iterates the catalog calling `CreateLabel`, and prints a per-label result line plus a summary.

**Tech Stack:** Go 1.x stdlib only. `gh` CLI is invoked via `exec.CommandContext`, identical to the existing `CreateRef` path. Tests use the shell-script fake binary pattern already established in `internal/github/gh_test.go`.

**Spec:** `docs/superpowers/specs/2026-04-18-cc-crew-init-design.md`

---

## File Structure

**New files:**
- `cmd/cc-crew/init.go` — subcommand entry point, label catalog builder, `doInit` core
- `cmd/cc-crew/init_test.go` — tests `doInit` against `FakeClient`

**Modified files:**
- `internal/github/client.go` — add `ErrLabelExists`, `CreateLabel` method to interface
- `internal/github/gh.go` — add `(*ghClient).CreateLabel`
- `internal/github/gh_test.go` — tests for `ghClient.CreateLabel` (success + already_exists + unrelated error)
- `internal/github/fake.go` — add `Labels` map, `CreateLabelHook`, `(*FakeClient).CreateLabel`
- `internal/github/fake_test.go` — test for `FakeClient.CreateLabel` (idempotency)
- `cmd/cc-crew/main.go` — dispatch `init`, update `usage()`
- `README.md` — document `cc-crew init`

---

## Task 1: Add `CreateLabel` to ghClient (TDD)

**Files:**
- Modify: `internal/github/client.go` — add `ErrLabelExists` sentinel
- Modify: `internal/github/gh.go` — add `(*ghClient).CreateLabel`
- Modify: `internal/github/gh_test.go` — three new tests

- [ ] **Step 1.1: Add the `ErrLabelExists` sentinel**

Edit `internal/github/client.go`. Replace:

```go
// ErrRefExists is returned by CreateRef when GitHub responds with 422
// "Reference already exists" — the caller lost the atomic claim race.
var ErrRefExists = errors.New("github: ref already exists")
```

with:

```go
// ErrRefExists is returned by CreateRef when GitHub responds with 422
// "Reference already exists" — the caller lost the atomic claim race.
var ErrRefExists = errors.New("github: ref already exists")

// ErrLabelExists is returned by CreateLabel when GitHub responds with 422
// "already_exists". Signals the caller that the label is already present
// and no action is needed.
var ErrLabelExists = errors.New("github: label already exists")
```

- [ ] **Step 1.2: Write three failing tests for `ghClient.CreateLabel`**

Append to `internal/github/gh_test.go`:

```go
func TestCreateLabelSuccess(t *testing.T) {
	bin := fakeBin(t, `exit 0`)
	c := newGhClientWithBin(bin)
	err := c.CreateLabel(context.Background(), Repo{Owner: "a", Name: "b"},
		"claude-task", "1d76db", "Queue an issue for the implementer")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestCreateLabelDetects422AsErrLabelExists(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 422: Validation Failed (already_exists)" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.CreateLabel(context.Background(), Repo{Owner: "a", Name: "b"},
		"claude-task", "1d76db", "desc")
	if err != ErrLabelExists {
		t.Fatalf("want ErrLabelExists, got %v", err)
	}
}

func TestCreateLabelDoesNotMapOtherErrorsToErrLabelExists(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 403: Forbidden" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.CreateLabel(context.Background(), Repo{Owner: "a", Name: "b"},
		"claude-task", "1d76db", "desc")
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if err == ErrLabelExists {
		t.Fatalf("must NOT map 403 to ErrLabelExists: %v", err)
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("error should propagate stderr: %v", err)
	}
}
```

- [ ] **Step 1.3: Run tests to confirm failure**

Run: `go test ./internal/github/ -run 'TestCreateLabel'`
Expected: compile error — `c.CreateLabel undefined`.

- [ ] **Step 1.4: Implement `(*ghClient).CreateLabel`**

Append to `internal/github/gh.go` (just before the `newGhClientWithBin` helper at the end):

```go
// CreateLabel posts to /repos/<r>/labels with a JSON body. If GitHub
// returns 422 with "already_exists", we map that to ErrLabelExists so
// callers can distinguish an existing label from a real error.
func (c *ghClient) CreateLabel(ctx context.Context, r Repo, name, color, description string) error {
	body := fmt.Sprintf(`{"name":%q,"color":%q,"description":%q}`, name, color, description)
	cmd := exec.CommandContext(ctx, c.ghBin, "api", "-X", "POST",
		fmt.Sprintf("repos/%s/labels", r.String()),
		"--input", "-")
	cmd.Stdin = strings.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "already_exists") {
			return ErrLabelExists
		}
		return fmt.Errorf("gh api create label %s: %w\nstderr: %s", name, err, stderr.String())
	}
	return nil
}
```

- [ ] **Step 1.5: Run tests to confirm pass**

Run: `go test ./internal/github/ -run 'TestCreateLabel' -v`
Expected: all three PASS.

- [ ] **Step 1.6: Run `gofmt` and commit**

```bash
gofmt -w .
git add internal/github/client.go internal/github/gh.go internal/github/gh_test.go
git commit -m "$(cat <<'EOF'
feat(github): add CreateLabel with ErrLabelExists sentinel

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Implement `CreateLabel` on `FakeClient` (TDD)

**Files:**
- Modify: `internal/github/fake.go` — add `Labels`, `CreateLabelHook`, `CreateLabel`
- Modify: `internal/github/fake_test.go` — add idempotency test

- [ ] **Step 2.1: Write the failing test**

Append to `internal/github/fake_test.go`:

```go
func TestFakeCreateLabelIdempotent(t *testing.T) {
	c := NewFake()
	r := Repo{Owner: "acme", Name: "widget"}
	ctx := context.Background()

	if err := c.CreateLabel(ctx, r, "claude-task", "1d76db", "desc"); err != nil {
		t.Fatalf("first CreateLabel: %v", err)
	}
	err := c.CreateLabel(ctx, r, "claude-task", "1d76db", "desc")
	if !errors.Is(err, ErrLabelExists) {
		t.Fatalf("expected ErrLabelExists on second create, got %v", err)
	}
	if _, ok := c.Labels["claude-task"]; !ok {
		t.Fatalf("label not recorded in FakeClient.Labels")
	}
}

func TestFakeCreateLabelHookCanInjectError(t *testing.T) {
	c := NewFake()
	sentinel := errors.New("boom")
	c.CreateLabelHook = func(name string) error {
		if name == "claude-done" {
			return sentinel
		}
		return nil
	}
	ctx := context.Background()
	r := Repo{Owner: "acme", Name: "widget"}
	if err := c.CreateLabel(ctx, r, "claude-task", "1d76db", "d"); err != nil {
		t.Fatalf("claude-task should succeed: %v", err)
	}
	if err := c.CreateLabel(ctx, r, "claude-done", "0e8a16", "d"); err != sentinel {
		t.Fatalf("want sentinel, got %v", err)
	}
}
```

- [ ] **Step 2.2: Run tests to confirm failure**

Run: `go test ./internal/github/ -run 'TestFakeCreateLabel' -v`
Expected: compile error — `c.Labels undefined`, `c.CreateLabelHook undefined`, `c.CreateLabel undefined`.

- [ ] **Step 2.3: Add fields and method to `FakeClient`**

Edit `internal/github/fake.go`.

Extend the struct. Replace:

```go
type FakeClient struct {
	mu        sync.Mutex
	User      string
	Issues    map[int]*Issue       // keyed by number
	PRs       map[int]*PullRequest // keyed by number
	Refs      map[string]string    // ref name → sha
	Reviews   map[int][]Review     // PR number → reviews
	DefaultBr string

	// Hooks for injecting errors in specific calls. Leave nil to disable.
	CreateRefHook func(ref string) error
	DeleteRefHook func(ref string) error
}
```

with:

```go
type FakeClient struct {
	mu        sync.Mutex
	User      string
	Issues    map[int]*Issue       // keyed by number
	PRs       map[int]*PullRequest // keyed by number
	Refs      map[string]string    // ref name → sha
	Labels    map[string]struct{}  // label name → presence
	Reviews   map[int][]Review     // PR number → reviews
	DefaultBr string

	// Hooks for injecting errors in specific calls. Leave nil to disable.
	CreateRefHook   func(ref string) error
	DeleteRefHook   func(ref string) error
	CreateLabelHook func(name string) error
}
```

Extend `NewFake`. Replace:

```go
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
```

with:

```go
func NewFake() *FakeClient {
	return &FakeClient{
		User:      "fake-bot",
		Issues:    map[int]*Issue{},
		PRs:       map[int]*PullRequest{},
		Refs:      map[string]string{},
		Labels:    map[string]struct{}{},
		Reviews:   map[int][]Review{},
		DefaultBr: "main",
	}
}
```

Add `CreateLabel` method. Insert immediately after the existing `RemoveLabel` method:

```go
func (f *FakeClient) CreateLabel(ctx context.Context, r Repo, name, color, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateLabelHook != nil {
		if err := f.CreateLabelHook(name); err != nil {
			return err
		}
	}
	if _, exists := f.Labels[name]; exists {
		return ErrLabelExists
	}
	f.Labels[name] = struct{}{}
	return nil
}
```

- [ ] **Step 2.4: Run tests to confirm pass**

Run: `go test ./internal/github/ -run 'TestFakeCreateLabel' -v`
Expected: both PASS.

- [ ] **Step 2.5: Run `gofmt` and commit**

```bash
gofmt -w .
git add internal/github/fake.go internal/github/fake_test.go
git commit -m "$(cat <<'EOF'
feat(github): implement CreateLabel on FakeClient

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Add `CreateLabel` to the `Client` interface

**Files:**
- Modify: `internal/github/client.go` — add method to interface

- [ ] **Step 3.1: Add the interface method**

Edit `internal/github/client.go`. Replace:

```go
	// Labels
	AddLabel(ctx context.Context, r Repo, issueOrPRNumber int, label string) error
	RemoveLabel(ctx context.Context, r Repo, issueOrPRNumber int, label string) error
```

with:

```go
	// Labels
	AddLabel(ctx context.Context, r Repo, issueOrPRNumber int, label string) error
	RemoveLabel(ctx context.Context, r Repo, issueOrPRNumber int, label string) error
	CreateLabel(ctx context.Context, r Repo, name, color, description string) error // returns ErrLabelExists on 422 already_exists
```

- [ ] **Step 3.2: Verify both compile-time assertions still hold**

Both `var _ Client = (*ghClient)(nil)` (gh.go:14) and `var _ Client = (*FakeClient)(nil)` (fake.go:11) must still compile. They will, because Tasks 1 and 2 added the methods first.

Run: `go build ./...`
Expected: exits 0 with no output.

Run: `go test ./internal/github/ -v`
Expected: all existing tests plus the 5 new ones from Tasks 1 & 2 PASS.

- [ ] **Step 3.3: Commit**

```bash
git add internal/github/client.go
git commit -m "$(cat <<'EOF'
feat(github): add CreateLabel to Client interface

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Write `doInit` core logic (TDD)

**Files:**
- Create: `cmd/cc-crew/init.go`
- Create: `cmd/cc-crew/init_test.go`

The subcommand is split into a pure function `doInit` (testable via `FakeClient`) and a shell `runInit` (Task 5) that wires up flag parsing and the real `github.NewGhClient`.

- [ ] **Step 4.1: Write the failing test**

Create `cmd/cc-crew/init_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/github"
)

func specs() []labelSpec {
	return []labelSpec{
		{Name: "claude-task", Color: "1d76db", Description: "task"},
		{Name: "claude-processing", Color: "0366d6", Description: "proc"},
		{Name: "claude-done", Color: "0e8a16", Description: "done"},
	}
}

func TestDoInitAllCreated(t *testing.T) {
	fake := github.NewFake()
	var out bytes.Buffer
	code := doInit(context.Background(), initOptions{
		GH: fake, Repo: github.Repo{Owner: "a", Name: "b"},
		Specs: specs(), Out: &out, Errout: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d", code)
	}
	text := out.String()
	for _, want := range []string{
		"created: claude-task",
		"created: claude-processing",
		"created: claude-done",
		"3 labels: 3 created, 0 existed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestDoInitAllExist(t *testing.T) {
	fake := github.NewFake()
	for _, s := range specs() {
		fake.Labels[s.Name] = struct{}{}
	}
	var out bytes.Buffer
	code := doInit(context.Background(), initOptions{
		GH: fake, Repo: github.Repo{Owner: "a", Name: "b"},
		Specs: specs(), Out: &out, Errout: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d", code)
	}
	text := out.String()
	for _, want := range []string{
		"exists:  claude-task",
		"exists:  claude-processing",
		"exists:  claude-done",
		"3 labels: 0 created, 3 existed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestDoInitMixed(t *testing.T) {
	fake := github.NewFake()
	fake.Labels["claude-task"] = struct{}{}
	var out bytes.Buffer
	code := doInit(context.Background(), initOptions{
		GH: fake, Repo: github.Repo{Owner: "a", Name: "b"},
		Specs: specs(), Out: &out, Errout: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d", code)
	}
	text := out.String()
	if !strings.Contains(text, "exists:  claude-task") {
		t.Fatalf("want exists for claude-task:\n%s", text)
	}
	if !strings.Contains(text, "created: claude-processing") {
		t.Fatalf("want created for claude-processing:\n%s", text)
	}
	if !strings.Contains(text, "3 labels: 2 created, 1 existed") {
		t.Fatalf("bad summary:\n%s", text)
	}
}

func TestDoInitBailsOnFirstNonConflictError(t *testing.T) {
	fake := github.NewFake()
	fake.CreateLabelHook = func(name string) error {
		if name == "claude-processing" {
			return errors.New("forbidden")
		}
		return nil
	}
	var out, errout bytes.Buffer
	code := doInit(context.Background(), initOptions{
		GH: fake, Repo: github.Repo{Owner: "a", Name: "b"},
		Specs: specs(), Out: &out, Errout: &errout,
	})
	if code != 1 {
		t.Fatalf("exit code: want 1, got %d", code)
	}
	if !strings.Contains(out.String(), "created: claude-task") {
		t.Fatalf("first label should have been reported as created:\n%s", out.String())
	}
	if strings.Contains(out.String(), "claude-done") {
		t.Fatalf("should not have attempted claude-done after bail:\n%s", out.String())
	}
	if !strings.Contains(errout.String(), "forbidden") {
		t.Fatalf("stderr should carry the underlying error: %s", errout.String())
	}
	if strings.Contains(out.String(), "3 labels:") {
		t.Fatalf("summary must be skipped on error:\n%s", out.String())
	}
}

func TestBuildLabelSpecsHonorsEnvOverrides(t *testing.T) {
	getenv := func(k string) string {
		if k == "CC_TASK_LABEL" {
			return "foo"
		}
		return ""
	}
	got := buildLabelSpecs(getenv)
	found := false
	for _, s := range got {
		if s.Name == "foo" {
			found = true
		}
		if s.Name == "claude-task" {
			t.Fatalf("env override ignored: %+v", got)
		}
	}
	if !found {
		t.Fatalf("expected a spec named foo, got %+v", got)
	}
	if len(got) != 9 {
		t.Fatalf("expected 9 specs, got %d", len(got))
	}
}
```

- [ ] **Step 4.2: Run tests to confirm failure**

Run: `go test ./cmd/cc-crew/ -run 'TestDoInit|TestBuildLabelSpecs' -v`
Expected: compile errors — `doInit`, `initOptions`, `labelSpec`, `buildLabelSpecs` all undefined.

- [ ] **Step 4.3: Implement `cmd/cc-crew/init.go`**

Create `cmd/cc-crew/init.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/charleszheng44/cc-crew/internal/config"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// labelSpec is one GitHub label to create: effective name (post env
// override), canonical color, canonical description.
type labelSpec struct {
	Name        string
	Color       string
	Description string
}

// initOptions collects everything doInit needs. Split from runInit so
// tests can inject a FakeClient and capture output.
type initOptions struct {
	GH     github.Client
	Repo   github.Repo
	Specs  []labelSpec
	Out    io.Writer
	Errout io.Writer
}

// buildLabelSpecs returns the nine label specs with their canonical colors
// and descriptions, using the provided getenv to honor the same CC_*_LABEL
// overrides as the other subcommands. Pass os.Getenv in production.
func buildLabelSpecs(getenv func(string) string) []labelSpec {
	d := config.Defaults()
	return []labelSpec{
		{Name: firstNonEmpty(getenv("CC_TASK_LABEL"), d.TaskLabel),
			Color: "1d76db", Description: "Queue an issue for the cc-crew implementer"},
		{Name: firstNonEmpty(getenv("CC_PROCESSING_LABEL"), d.ProcessingLabel),
			Color: "0366d6", Description: "Implementer is working on this issue"},
		{Name: firstNonEmpty(getenv("CC_DONE_LABEL"), d.DoneLabel),
			Color: "0e8a16", Description: "Implementer opened a PR for this issue"},
		{Name: firstNonEmpty(getenv("CC_REVIEW_LABEL"), d.ReviewLabel),
			Color: "6f42c1", Description: "Queue a PR for the cc-crew reviewer"},
		{Name: firstNonEmpty(getenv("CC_REVIEWING_LABEL"), d.ReviewingLabel),
			Color: "8a63d2", Description: "Reviewer is working on this PR"},
		{Name: firstNonEmpty(getenv("CC_REVIEWED_LABEL"), d.ReviewedLabel),
			Color: "5319e7", Description: "Reviewer posted a review on this PR"},
		{Name: firstNonEmpty(getenv("CC_ADDRESS_LABEL"), d.AddressLabel),
			Color: "d93f0b", Description: "Queue a PR for the implementer to address feedback"},
		{Name: firstNonEmpty(getenv("CC_ADDRESSING_LABEL"), d.AddressingLabel),
			Color: "e99695", Description: "Implementer is addressing review feedback"},
		{Name: firstNonEmpty(getenv("CC_ADDRESSED_LABEL"), d.AddressedLabel),
			Color: "fbca04", Description: "Implementer pushed updates addressing the review"},
	}
}

// doInit creates each label in o.Specs via o.GH. It prints one line per
// label ("created: X" or "exists:  X") to o.Out, plus a summary line on
// success. On any non-conflict error, it writes the error to o.Errout
// and returns 1 without printing the summary — the user re-runs after
// fixing the cause, and idempotency skips the work already done.
func doInit(ctx context.Context, o initOptions) int {
	created, existed := 0, 0
	for _, s := range o.Specs {
		err := o.GH.CreateLabel(ctx, o.Repo, s.Name, s.Color, s.Description)
		if errors.Is(err, github.ErrLabelExists) {
			fmt.Fprintf(o.Out, "exists:  %s\n", s.Name)
			existed++
			continue
		}
		if err != nil {
			fmt.Fprintln(o.Errout, err)
			return 1
		}
		fmt.Fprintf(o.Out, "created: %s\n", s.Name)
		created++
	}
	fmt.Fprintf(o.Out, "%d labels: %d created, %d existed\n",
		len(o.Specs), created, existed)
	return 0
}

// runInit is the CLI entry point wired into main.go.
func runInit(args []string) int {
	fs := flag.NewFlagSet("cc-crew init", flag.ContinueOnError)
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
	return doInit(ctx, initOptions{
		GH:     github.NewGhClient(),
		Repo:   github.Repo{Owner: owner, Name: name},
		Specs:  buildLabelSpecs(os.Getenv),
		Out:    os.Stdout,
		Errout: os.Stderr,
	})
}
```

Note: `firstNonEmpty` is already defined in `cmd/cc-crew/reset.go:53` and is in the same `package main`, so no new definition is needed.

- [ ] **Step 4.4: Run tests to confirm pass**

Run: `go test ./cmd/cc-crew/ -run 'TestDoInit|TestBuildLabelSpecs' -v`
Expected: all 5 PASS.

- [ ] **Step 4.5: Run `gofmt` and commit**

```bash
gofmt -w .
git add cmd/cc-crew/init.go cmd/cc-crew/init_test.go
git commit -m "$(cat <<'EOF'
feat(init): add cc-crew init core logic

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Wire `init` into `main.go`

**Files:**
- Modify: `cmd/cc-crew/main.go`

- [ ] **Step 5.1: Register the subcommand**

Edit `cmd/cc-crew/main.go`. Replace:

```go
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Println(Version)
	case "up":
		os.Exit(runUp(os.Args[2:]))
	case "status":
		os.Exit(runStatus(os.Args[2:]))
	case "reset":
		os.Exit(runReset(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
```

with:

```go
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Println(Version)
	case "init":
		os.Exit(runInit(os.Args[2:]))
	case "up":
		os.Exit(runUp(os.Args[2:]))
	case "status":
		os.Exit(runStatus(os.Args[2:]))
	case "reset":
		os.Exit(runReset(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
```

- [ ] **Step 5.2: Update the `usage()` help text**

In the same file, replace:

```go
Usage:
  cc-crew up       Start the orchestrator (foreground)
  cc-crew status   Print current task/queue snapshot
  cc-crew reset    Bulk-clean all cc-crew state in the repo
  cc-crew version  Print version
  cc-crew help     Show this help
```

with:

```go
Usage:
  cc-crew init     Create the cc-crew GitHub labels on the target repo
  cc-crew up       Start the orchestrator (foreground)
  cc-crew status   Print current task/queue snapshot
  cc-crew reset    Bulk-clean all cc-crew state in the repo
  cc-crew version  Print version
  cc-crew help     Show this help
```

- [ ] **Step 5.3: Build and verify the full test suite passes**

Run: `go build ./... && go test ./...`
Expected: exits 0, all tests PASS.

- [ ] **Step 5.4: Smoke-test the help output**

Run: `./cc-crew help`
Expected: output includes the line `cc-crew init     Create the cc-crew GitHub labels on the target repo`.

(If `./cc-crew` is stale, rebuild first: `make build` or `go build -o cc-crew ./cmd/cc-crew`.)

- [ ] **Step 5.5: Commit**

```bash
gofmt -w .
git add cmd/cc-crew/main.go
git commit -m "$(cat <<'EOF'
feat(init): wire init subcommand into main dispatch and help text

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Document `cc-crew init` in the README

**Files:**
- Modify: `README.md`

- [ ] **Step 6.1: Add an `init` subsection under "Run"**

Edit `README.md`. The "Run" section currently shows `./cc-crew up / status / reset`. Find the line:

```
./cc-crew up                       # foreground; Ctrl-C to stop
```

Insert, immediately before it:

```
./cc-crew init                     # create the 9 cc-crew labels on the remote (idempotent)
```

- [ ] **Step 6.2: Add a short descriptive paragraph**

Find the section header `### Run` in `README.md` (around line 180). Between the existing opening line (`From inside a clone of the target repo:`) and the code block that follows it, insert this paragraph:

```
Before the first `up`, run `cc-crew init` once per repo to create the nine lifecycle labels on the remote (`claude-task`, `claude-processing`, `claude-done`, `claude-review`, `claude-reviewing`, `claude-reviewed`, `claude-address`, `claude-addressing`, `claude-addressed`). It's safe to re-run — already-present labels are reported and skipped.
```

- [ ] **Step 6.3: Verify the README edit rendered sensibly**

Run: `git diff README.md`
Expected: only the two additions above, nothing else.

- [ ] **Step 6.4: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs: document cc-crew init in the README

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Final verification

- [ ] **Run the full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Run `go vet`**

Run: `go vet ./...`
Expected: no output.

- [ ] **Verify help text**

Run: `go run ./cmd/cc-crew help`
Expected: `init` line present.
