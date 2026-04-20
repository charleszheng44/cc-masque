# Auto-merger & Conflict-Resolver Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the cc-crew pipeline so reviewer-APPROVED PRs merge themselves, with an LLM-driven conflict resolver when GitHub's native rebase-update isn't enough.

**Architecture:** Two new `claim.Kind` values. `KindMerger` runs orchestrator-side Go logic (no Docker) — a small state machine over GitHub API calls driven by the PR's `mergeStateStatus`. `KindResolver` reuses the implementer Docker image with a new persona prompt; it shares the implementer semaphore.

**Tech Stack:** Go 1.22+, `gh` CLI, existing cc-crew packages (`internal/{claim,github,scheduler,config,reset}`, `cmd/cc-crew`). Uses GitHub's native issue-merge API, rebase update-branch, and the existing SHA-pinned `claim.Claimer`.

**Spec:** [`docs/superpowers/specs/2026-04-20-auto-merger-design.md`](../specs/2026-04-20-auto-merger-design.md)

---

## Conventions

- **Run `gofmt -w .`** from repo root before every `git add`. Non-negotiable — CI enforces `gofmt -l`.
- **Co-author trailer** on every commit: `Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>`.
- **Commit message style**: match `git log --oneline -10` — conventional commits (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`).
- **Test placement**: every `foo.go` gets colocated `foo_test.go`. Use existing `github.NewFake()` / `slog.New(slog.NewTextHandler(os.Stderr, nil))` patterns.

---

## Task 1: Add `KindMerger` and `KindResolver` to the claim package

**Files:**
- Modify: `internal/claim/claim.go`
- Modify: `internal/claim/claim_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/claim/claim_test.go`:

```go
func TestPathsForMergerAndResolver(t *testing.T) {
	cases := []struct {
		name     string
		kind     Kind
		number   int
		wantLock string
		wantPfx  string
	}{
		{"merger", KindMerger, 12, "refs/cc-crew/merge-lock/pr-12", "cc-crew/merge-claim/pr-12/"},
		{"resolver", KindResolver, 34, "refs/cc-crew/resolve-lock/pr-34", "cc-crew/resolve-claim/pr-34/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := PathsFor(tc.kind, tc.number)
			if p.LockRef != tc.wantLock {
				t.Errorf("LockRef = %q, want %q", p.LockRef, tc.wantLock)
			}
			if p.RefPrefix != tc.wantPfx {
				t.Errorf("RefPrefix = %q, want %q", p.RefPrefix, tc.wantPfx)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/claim/ -run TestPathsForMergerAndResolver -v`
Expected: FAIL with `undefined: KindMerger` and `undefined: KindResolver`.

- [ ] **Step 3: Implement the two new kinds**

Edit `internal/claim/claim.go` — extend the const block and `PathsFor`:

```go
const (
	KindImplementer Kind = iota
	KindReviewer
	KindAddresser
	KindMerger
	KindResolver
)
```

Then inside `PathsFor`, add two cases before the `panic`:

```go
	case KindMerger:
		return Paths{
			LockRef:   fmt.Sprintf("refs/cc-crew/merge-lock/pr-%d", number),
			RefPrefix: fmt.Sprintf("cc-crew/merge-claim/pr-%d/", number),
		}
	case KindResolver:
		return Paths{
			LockRef:   fmt.Sprintf("refs/cc-crew/resolve-lock/pr-%d", number),
			RefPrefix: fmt.Sprintf("cc-crew/resolve-claim/pr-%d/", number),
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/claim/ -run TestPathsForMergerAndResolver -v`
Expected: PASS.

- [ ] **Step 5: Run full claim package tests**

Run: `go test ./internal/claim/ -v`
Expected: all PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add internal/claim/claim.go internal/claim/claim_test.go
git commit -m "$(cat <<'EOF'
feat(claim): add KindMerger and KindResolver

Introduce two new claim kinds with their own refspec namespaces so the
merger (cc-crew/merge-claim/...) and resolver (cc-crew/resolve-claim/...)
can coexist with implementer/reviewer/addresser claims on the same PR.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Add the five new label defaults to `config.Defaults`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go` (new test file section — check existing layout first)

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestDefaultsIncludesMergerAndResolverLabels(t *testing.T) {
	d := Defaults()
	if d.MergeLabel != "claude-merge" {
		t.Errorf("MergeLabel = %q, want %q", d.MergeLabel, "claude-merge")
	}
	if d.MergingLabel != "claude-merging" {
		t.Errorf("MergingLabel = %q, want %q", d.MergingLabel, "claude-merging")
	}
	if d.ResolveConflictLabel != "claude-resolve-conflict" {
		t.Errorf("ResolveConflictLabel = %q, want %q", d.ResolveConflictLabel, "claude-resolve-conflict")
	}
	if d.ResolvingLabel != "claude-resolving" {
		t.Errorf("ResolvingLabel = %q, want %q", d.ResolvingLabel, "claude-resolving")
	}
	if d.ConflictBlockedLabel != "claude-conflict-blocked" {
		t.Errorf("ConflictBlockedLabel = %q, want %q", d.ConflictBlockedLabel, "claude-conflict-blocked")
	}
	if d.MaxMergers != 2 {
		t.Errorf("MaxMergers = %d, want 2", d.MaxMergers)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDefaultsIncludesMergerAndResolverLabels -v`
Expected: FAIL with `d.MergeLabel undefined`.

- [ ] **Step 3: Add fields to `Config` struct**

In `internal/config/config.go`, extend the struct — insert after the `AddressedLabel` / `Continuous` block, before the `AutoReview` line:

```go
	// Auto-merger feature (spec 2026-04-20).
	MergeLabel           string
	MergingLabel         string
	ResolveConflictLabel string
	ResolvingLabel       string
	ConflictBlockedLabel string
	MaxMergers           int
```

- [ ] **Step 4: Populate defaults**

In `Defaults()`, add after the `AddressedLabel: "claude-addressed",` line (before `MaxCycles`):

```go
		MergeLabel:           "claude-merge",
		MergingLabel:         "claude-merging",
		ResolveConflictLabel: "claude-resolve-conflict",
		ResolvingLabel:       "claude-resolving",
		ConflictBlockedLabel: "claude-conflict-blocked",
		MaxMergers:           2,
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestDefaultsIncludesMergerAndResolverLabels -v`
Expected: PASS.

- [ ] **Step 6: Run full config package tests**

Run: `go test ./internal/config/ -v`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w .
git add internal/config/config.go internal/config/config_test.go
git commit -m "$(cat <<'EOF'
feat(config): add merger/resolver label defaults and MaxMergers

Introduce the five new labels (claude-merge, claude-merging,
claude-resolve-conflict, claude-resolving, claude-conflict-blocked)
and MaxMergers=2 default for the auto-merger feature.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Wire `--max-mergers` flag and `MAX_MERGERS` env; update `Validate`

**Files:**
- Modify: `internal/config/parse.go`
- Modify: `internal/config/config.go` (Validate)
- Modify: `internal/config/parse_test.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test for flag parsing**

Add to `internal/config/parse_test.go` (append):

```go
func TestParseMaxMergersFlag(t *testing.T) {
	env := map[string]string{
		"GH_TOKEN":                "t",
		"CLAUDE_CODE_OAUTH_TOKEN": "o",
	}
	getenv := func(k string) string { return env[k] }
	c, err := Parse([]string{"--max-mergers", "5"}, getenv, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxMergers != 5 {
		t.Fatalf("MaxMergers = %d, want 5", c.MaxMergers)
	}
}

func TestParseMaxMergersEnv(t *testing.T) {
	env := map[string]string{
		"GH_TOKEN":                "t",
		"CLAUDE_CODE_OAUTH_TOKEN": "o",
		"CC_MAX_MERGERS":          "7",
	}
	getenv := func(k string) string { return env[k] }
	c, err := Parse(nil, getenv, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxMergers != 7 {
		t.Fatalf("MaxMergers = %d, want 7", c.MaxMergers)
	}
}
```

- [ ] **Step 2: Write the failing validation test**

Add to `internal/config/config_test.go`:

```go
func TestValidateRequiresReviewerTokenWhenMergerEnabled(t *testing.T) {
	c := Defaults()
	c.RepoDir = "/tmp"
	c.MaxImplementers = 0
	c.MaxReviewers = 0
	c.MaxMergers = 1
	c.OrchestratorGHToken = "t"
	c.ClaudeOAuthToken = "o"
	// ReviewerGHToken intentionally empty.
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "GH_TOKEN_REVIEWER") {
		t.Fatalf("want GH_TOKEN_REVIEWER error, got %v", err)
	}
}

func TestValidateAcceptsMergerOnlyWithReviewerToken(t *testing.T) {
	c := Defaults()
	c.RepoDir = "/tmp"
	c.MaxImplementers = 0
	c.MaxReviewers = 0
	c.MaxMergers = 1
	c.OrchestratorGHToken = "t"
	c.ReviewerGHToken = "t"
	c.ClaudeOAuthToken = "o"
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/config/ -run "TestParseMaxMergers|TestValidateRequiresReviewerToken|TestValidateAcceptsMergerOnly" -v`
Expected: FAIL (flag not wired; Validate not updated).

- [ ] **Step 4: Wire the flag**

In `internal/config/parse.go`, add after the existing `MaxReviewers` flag line:

```go
	fs.IntVar(&c.MaxMergers, "max-mergers", envInt(getenv, "CC_MAX_MERGERS", c.MaxMergers), "Max concurrent merger tasks (0 disables merger+resolver)")
```

- [ ] **Step 5: Update `Validate`**

In `internal/config/config.go`, extend `Validate()`. Replace the existing condition:

```go
	if c.MaxImplementers == 0 && c.MaxReviewers == 0 {
		return errors.New("at least one of max-implementers/max-reviewers must be > 0")
	}
```

with:

```go
	if c.MaxImplementers == 0 && c.MaxReviewers == 0 && c.MaxMergers == 0 {
		return errors.New("at least one of max-implementers/max-reviewers/max-mergers must be > 0")
	}
	if c.MaxMergers < 0 {
		return errors.New("max-mergers must be >= 0")
	}
	if c.MaxMergers > 0 && c.ReviewerGHToken == "" {
		return errors.New("GH_TOKEN_REVIEWER or GH_TOKEN is required when merger is enabled")
	}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w .
git add internal/config/parse.go internal/config/config.go internal/config/parse_test.go internal/config/config_test.go
git commit -m "$(cat <<'EOF'
feat(config): wire --max-mergers flag and validate reviewer token

Add --max-mergers / CC_MAX_MERGERS (default 2) and require
GH_TOKEN_REVIEWER when the merger is enabled — the merger calls the
merge API with reviewer credentials.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Add the five new label specs to `cmd/cc-crew/init.go`

**Files:**
- Modify: `cmd/cc-crew/init.go`
- Modify: `cmd/cc-crew/init_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/cc-crew/init_test.go`:

```go
func TestBuildLabelSpecsIncludesMergerAndResolverLabels(t *testing.T) {
	getenv := func(string) string { return "" }
	specs := buildLabelSpecs(getenv)
	want := map[string]bool{
		"claude-merge":            false,
		"claude-merging":          false,
		"claude-resolve-conflict": false,
		"claude-resolving":        false,
		"claude-conflict-blocked": false,
	}
	for _, s := range specs {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("label %q missing from buildLabelSpecs", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cc-crew/ -run TestBuildLabelSpecsIncludesMergerAndResolverLabels -v`
Expected: FAIL (missing labels).

- [ ] **Step 3: Extend `buildLabelSpecs`**

In `cmd/cc-crew/init.go`, extend the returned slice — add these five entries at the end (before the closing `}`):

```go
		{Name: firstNonEmpty(getenv("CC_MERGE_LABEL"), d.MergeLabel),
			Color: "2e7d32", Description: "Queue an approved PR for the cc-crew merger"},
		{Name: firstNonEmpty(getenv("CC_MERGING_LABEL"), d.MergingLabel),
			Color: "1b5e20", Description: "Merger is working on this PR"},
		{Name: firstNonEmpty(getenv("CC_RESOLVE_CONFLICT_LABEL"), d.ResolveConflictLabel),
			Color: "bf360c", Description: "Queue a PR for the resolver to fix merge conflicts"},
		{Name: firstNonEmpty(getenv("CC_RESOLVING_LABEL"), d.ResolvingLabel),
			Color: "e65100", Description: "Resolver is working on this PR"},
		{Name: firstNonEmpty(getenv("CC_CONFLICT_BLOCKED_LABEL"), d.ConflictBlockedLabel),
			Color: "b71c1c", Description: "Conflict resolution failed; human attention needed"},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cc-crew/ -run TestBuildLabelSpecsIncludesMergerAndResolverLabels -v`
Expected: PASS.

- [ ] **Step 5: Run full init tests**

Run: `go test ./cmd/cc-crew/ -v`
Expected: all PASS. If an existing "counts labels" test asserts a specific count (e.g. `9 labels`), update it to `14 labels` inline.

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add cmd/cc-crew/init.go cmd/cc-crew/init_test.go
git commit -m "$(cat <<'EOF'
feat(init): create the five new merger/resolver labels

cc-crew init now provisions claude-merge, claude-merging,
claude-resolve-conflict, claude-resolving, and claude-conflict-blocked
so a fresh repo can run the auto-merger pipeline without manual setup.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Extend `github.PullRequest` with `Mergeable` and `MergeStateStatus`

**Files:**
- Modify: `internal/github/types.go`
- Modify: `internal/github/gh.go` (ghPR + GetPR + ListPRs)
- Modify: `internal/github/fake.go` (no-op — fields auto-copy)
- Modify: `internal/github/gh_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/github/gh_test.go` (or insert a focused test file if preferred):

```go
func TestGetPRParsesMergeStateFields(t *testing.T) {
	stdout := `{"number":42,"title":"t","body":"b","state":"OPEN","labels":[],"headRefOid":"sha","headRefName":"claude/issue-42","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN"}`
	c := fakeGhClient(t, stdout)
	pr, err := c.GetPR(context.Background(), Repo{Owner: "o", Name: "r"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Mergeable != "MERGEABLE" {
		t.Errorf("Mergeable = %q, want MERGEABLE", pr.Mergeable)
	}
	if pr.MergeStateStatus != "CLEAN" {
		t.Errorf("MergeStateStatus = %q, want CLEAN", pr.MergeStateStatus)
	}
}
```

Note: `fakeGhClient(t, stdout)` is the existing helper in `gh_test.go` that shims the `gh` binary. If it isn't already named that, locate the equivalent helper and use it — the test file already has one for other parsing tests.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/github/ -run TestGetPRParsesMergeStateFields -v`
Expected: FAIL (fields unknown / undefined).

- [ ] **Step 3: Add fields to `PullRequest`**

In `internal/github/types.go`, extend the struct:

```go
type PullRequest struct {
	Number           int      `json:"number"`
	Title            string   `json:"title"`
	Body             string   `json:"body"`
	State            string   `json:"state"`
	Labels           []string `json:"labels"`
	HeadRefOid       string   `json:"headRefOid"`
	HeadRefName      string   `json:"headRefName"`
	BaseRefName      string   `json:"baseRefName"`
	Mergeable        string   `json:"mergeable"`        // MERGEABLE | CONFLICTING | UNKNOWN
	MergeStateStatus string   `json:"mergeStateStatus"` // CLEAN | BEHIND | DIRTY | BLOCKED | UNSTABLE | HAS_HOOKS | DRAFT | UNKNOWN
}
```

- [ ] **Step 4: Extend ghPR parser and include fields in gh list/view**

In `internal/github/gh.go`, update `ghPR`:

```go
type ghPR struct {
	Number           int       `json:"number"`
	Title            string    `json:"title"`
	Body             string    `json:"body"`
	State            string    `json:"state"`
	Labels           []ghLabel `json:"labels"`
	HeadRefOid       string    `json:"headRefOid"`
	HeadRefName      string    `json:"headRefName"`
	BaseRefName      string    `json:"baseRefName"`
	Mergeable        string    `json:"mergeable"`
	MergeStateStatus string    `json:"mergeStateStatus"`
}
```

Update the `--json` comma list in both `ListPRs` and `GetPR` to include `mergeable,mergeStateStatus`. Example for `GetPR`:

```go
	out, err := c.runGh(ctx, "pr", "view", fmt.Sprint(n), "-R", r.String(),
		"--json", "number,title,body,state,labels,headRefOid,headRefName,baseRefName,mergeable,mergeStateStatus")
```

Same addition for `ListPRs`. Then in both functions' build-loops, populate:

```go
		Mergeable:        g.Mergeable,
		MergeStateStatus: g.MergeStateStatus,
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/github/ -v`
Expected: all PASS (FakeClient already copies struct fields by value).

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add internal/github/types.go internal/github/gh.go internal/github/gh_test.go
git commit -m "$(cat <<'EOF'
feat(github): expose Mergeable and MergeStateStatus on PullRequest

The merger relies on GitHub's mergeStateStatus (CLEAN/BEHIND/DIRTY/...)
to decide between merge/update-branch/resolve/terminal paths. Request
the field from gh pr view/list and plumb it through.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Add `MergePR`, `UpdateBranch`, `GetCheckRuns`, `CreateComment` to the `Client` interface

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/gh.go`
- Modify: `internal/github/fake.go`
- Modify: `internal/github/fake_test.go`

- [ ] **Step 1: Write the failing fake tests**

Append to `internal/github/fake_test.go`:

```go
func TestFakeMergePR(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[1] = &PullRequest{Number: 1, State: "open", HeadRefOid: "sha"}
	if err := f.MergePR(context.Background(), r, 1, "sha", MergeMethodRebase, true); err != nil {
		t.Fatalf("MergePR: %v", err)
	}
	if f.PRs[1].State != "closed" {
		t.Errorf("state = %q, want closed", f.PRs[1].State)
	}
	if !f.PRs[1].Merged {
		t.Error("Merged flag not set")
	}
}

func TestFakeMergePRConflictHook(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[2] = &PullRequest{Number: 2, State: "open", HeadRefOid: "sha", MergeStateStatus: "DIRTY"}
	f.MergePRHook = func(n int) error { return ErrMergeConflict }
	err := f.MergePR(context.Background(), r, 2, "sha", MergeMethodRebase, true)
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("want ErrMergeConflict, got %v", err)
	}
}

func TestFakeUpdateBranch(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[3] = &PullRequest{Number: 3, State: "open", HeadRefOid: "old"}
	if err := f.UpdateBranch(context.Background(), r, 3, "old", UpdateMethodRebase); err != nil {
		t.Fatalf("UpdateBranch: %v", err)
	}
	if !f.UpdateBranchCalled[3] {
		t.Error("UpdateBranchCalled not recorded")
	}
}

func TestFakeCreateComment(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[4] = &PullRequest{Number: 4, State: "open"}
	if err := f.CreateComment(context.Background(), r, 4, "hello"); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if got := f.Comments[4]; len(got) != 1 || got[0] != "hello" {
		t.Errorf("Comments[4] = %v, want [hello]", got)
	}
}

func TestFakeGetCheckRuns(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.CheckRuns["sha"] = []CheckRun{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "lint", Status: "in_progress", Conclusion: ""},
	}
	got, err := f.GetCheckRuns(context.Background(), r, "sha")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d runs, want 2", len(got))
	}
}
```

Add the needed imports (`errors`, `context`) at the top if not present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/github/ -run "TestFakeMergePR|TestFakeUpdateBranch|TestFakeCreateComment|TestFakeGetCheckRuns" -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Add types, constants, errors to `client.go`**

In `internal/github/client.go`, extend. First add near the top-level errors:

```go
// ErrMergeConflict is returned by MergePR when GitHub rejects the merge
// because the PR has unresolved conflicts against the base branch.
var ErrMergeConflict = errors.New("github: merge conflict")
```

Then declare method enums and a struct:

```go
type MergeMethod string

const (
	MergeMethodRebase MergeMethod = "rebase"
	MergeMethodSquash MergeMethod = "squash"
	MergeMethodMerge  MergeMethod = "merge"
)

type UpdateMethod string

const (
	UpdateMethodRebase UpdateMethod = "rebase"
	UpdateMethodMerge  UpdateMethod = "merge"
)

type CheckRun struct {
	Name       string // display name, e.g. "build"
	Status     string // queued | in_progress | completed | waiting | pending
	Conclusion string // success | failure | neutral | cancelled | skipped | timed_out | action_required | stale | startup_failure | "" (if not completed)
}
```

Then extend the `Client` interface — insert after the existing Reviews block:

```go
	// Merge / branch updates
	MergePR(ctx context.Context, r Repo, number int, expectedHeadSha string, method MergeMethod, deleteBranch bool) error // returns ErrMergeConflict on conflict
	UpdateBranch(ctx context.Context, r Repo, number int, expectedHeadSha string, method UpdateMethod) error

	// Status checks
	GetCheckRuns(ctx context.Context, r Repo, sha string) ([]CheckRun, error)

	// Comments
	CreateComment(ctx context.Context, r Repo, issueOrPRNumber int, body string) error
```

- [ ] **Step 4: Implement on `FakeClient`**

In `internal/github/fake.go`, extend `FakeClient` struct to add fields:

```go
	Comments           map[int][]string          // issue/PR number → posted comments
	CheckRuns          map[string][]CheckRun     // SHA → check runs
	UpdateBranchCalled map[int]bool              // PR number → update-branch was called
	MergePRHook        func(number int) error    // hook to inject errors
	UpdateBranchHook   func(number int) error
	CreateCommentHook  func(number int) error
```

In `NewFake()`, initialize the maps:

```go
	return &FakeClient{
		// ...existing fields...
		Comments:           map[int][]string{},
		CheckRuns:          map[string][]CheckRun{},
		UpdateBranchCalled: map[int]bool{},
	}
```

Also extend `PullRequest` (in `types.go`) with `Merged bool`:

```go
	Merged bool `json:"-"` // populated only by FakeClient.MergePR for tests
```

Then implement the methods on `FakeClient`:

```go
func (f *FakeClient) MergePR(ctx context.Context, r Repo, n int, expectedSha string, method MergeMethod, deleteBranch bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.MergePRHook != nil {
		if err := f.MergePRHook(n); err != nil {
			return err
		}
	}
	p, ok := f.PRs[n]
	if !ok {
		return fmt.Errorf("fake: PR %d not found", n)
	}
	if expectedSha != "" && p.HeadRefOid != expectedSha {
		return fmt.Errorf("fake: head SHA mismatch: got %s, expected %s", p.HeadRefOid, expectedSha)
	}
	p.State = "closed"
	p.Merged = true
	return nil
}

func (f *FakeClient) UpdateBranch(ctx context.Context, r Repo, n int, expectedSha string, method UpdateMethod) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UpdateBranchHook != nil {
		if err := f.UpdateBranchHook(n); err != nil {
			return err
		}
	}
	f.UpdateBranchCalled[n] = true
	return nil
}

func (f *FakeClient) GetCheckRuns(ctx context.Context, r Repo, sha string) ([]CheckRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]CheckRun(nil), f.CheckRuns[sha]...), nil
}

func (f *FakeClient) CreateComment(ctx context.Context, r Repo, n int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateCommentHook != nil {
		if err := f.CreateCommentHook(n); err != nil {
			return err
		}
	}
	f.Comments[n] = append(f.Comments[n], body)
	return nil
}
```

- [ ] **Step 5: Implement on `ghClient` (real gh CLI)**

In `internal/github/gh.go`, add (use the REST API via `gh api`):

```go
func (c *ghClient) MergePR(ctx context.Context, r Repo, n int, expectedSha string, method MergeMethod, deleteBranch bool) error {
	args := []string{"pr", "merge", fmt.Sprint(n), "-R", r.String()}
	switch method {
	case MergeMethodRebase:
		args = append(args, "--rebase")
	case MergeMethodSquash:
		args = append(args, "--squash")
	case MergeMethodMerge:
		args = append(args, "--merge")
	default:
		return fmt.Errorf("unknown merge method: %s", method)
	}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	if expectedSha != "" {
		args = append(args, "--match-head-commit", expectedSha)
	}
	cmd := exec.CommandContext(ctx, c.ghBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		// gh pr merge reports conflicts with phrases like:
		//   "Pull request is not mergeable: the merge commit cannot be cleanly created"
		//   "merge conflict"
		//   "not in a mergeable state"
		low := strings.ToLower(msg)
		if strings.Contains(low, "merge conflict") ||
			strings.Contains(low, "not mergeable") ||
			strings.Contains(low, "not in a mergeable state") ||
			strings.Contains(low, "cannot be cleanly") {
			return ErrMergeConflict
		}
		return fmt.Errorf("gh pr merge %d: %w\nstderr: %s", n, err, msg)
	}
	return nil
}

func (c *ghClient) UpdateBranch(ctx context.Context, r Repo, n int, expectedSha string, method UpdateMethod) error {
	body := fmt.Sprintf(`{"expected_head_sha":%q,"update_method":%q}`, expectedSha, string(method))
	cmd := exec.CommandContext(ctx, c.ghBin, "api", "-X", "PUT",
		fmt.Sprintf("repos/%s/pulls/%d/update-branch", r.String(), n),
		"--input", "-")
	cmd.Stdin = strings.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh api update-branch PR %d: %w\nstderr: %s", n, err, stderr.String())
	}
	return nil
}

type ghCheckRunsResp struct {
	CheckRuns []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"check_runs"`
}

func (c *ghClient) GetCheckRuns(ctx context.Context, r Repo, sha string) ([]CheckRun, error) {
	out, err := c.runGh(ctx, "api",
		fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", r.String(), sha))
	if err != nil {
		return nil, err
	}
	var raw ghCheckRunsResp
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse check-runs: %w", err)
	}
	runs := make([]CheckRun, 0, len(raw.CheckRuns))
	for _, cr := range raw.CheckRuns {
		runs = append(runs, CheckRun{Name: cr.Name, Status: cr.Status, Conclusion: cr.Conclusion})
	}
	return runs, nil
}

func (c *ghClient) CreateComment(ctx context.Context, r Repo, n int, body string) error {
	bodyJSON := fmt.Sprintf(`{"body":%q}`, body)
	cmd := exec.CommandContext(ctx, c.ghBin, "api", "-X", "POST",
		fmt.Sprintf("repos/%s/issues/%d/comments", r.String(), n),
		"--input", "-")
	cmd.Stdin = strings.NewReader(bodyJSON)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh api create comment %d: %w\nstderr: %s", n, err, stderr.String())
	}
	return nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/github/ -v`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w .
git add internal/github/client.go internal/github/gh.go internal/github/fake.go internal/github/types.go internal/github/fake_test.go
git commit -m "$(cat <<'EOF'
feat(github): add merge, update-branch, check-runs, comment helpers

Four new Client methods the merger needs: MergePR (with ErrMergeConflict
classification), UpdateBranch (rebase mode), GetCheckRuns (per-SHA list),
and CreateComment (for terminal-failure escalation messages).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Extend reviewer `successCleanup` to flip `claude-merge` / `claude-address` based on latest review state

**Files:**
- Modify: `internal/scheduler/lifecycle.go`
- Modify: `internal/scheduler/lifecycle_test.go`
- Modify: `cmd/cc-crew/up.go` (plumb new labels into the reviewer Lifecycle)

- [ ] **Step 1: Write the failing tests**

Append to `internal/scheduler/lifecycle_test.go`:

```go
func TestReviewerSuccessCleanupAppliesMergeLabelOnApproval(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[7] = &github.PullRequest{
		Number: 7, State: "open", HeadRefOid: "sha7",
		Labels: []string{"claude-review", "claude-reviewing"},
	}
	f.Reviews[7] = []github.Review{
		{ID: 1, Author: "bot", State: "APPROVED", At: time.Now()},
	}
	lc := &Lifecycle{
		Kind: claim.KindReviewer, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-review", LockLabel: "claude-reviewing", DoneLabel: "claude-reviewed",
		MergeLabel: "claude-merge", AddressLabel: "claude-address",
	}
	lc.successCleanupReviewer(context.Background(), 7, "sha7")
	labels := f.PRs[7].Labels
	if !containsLabel(labels, "claude-reviewed") {
		t.Errorf("expected claude-reviewed, got %v", labels)
	}
	if !containsLabel(labels, "claude-merge") {
		t.Errorf("expected claude-merge, got %v", labels)
	}
	if containsLabel(labels, "claude-address") {
		t.Errorf("unexpected claude-address, got %v", labels)
	}
}

func TestReviewerSuccessCleanupAppliesAddressLabelOnChangesRequested(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[8] = &github.PullRequest{
		Number: 8, State: "open", HeadRefOid: "sha8",
		Labels: []string{"claude-review", "claude-reviewing", "claude-merge"}, // stale merge label from prior APPROVED
	}
	f.Reviews[8] = []github.Review{
		{ID: 1, Author: "bot", State: "APPROVED", At: time.Now().Add(-time.Hour)},
		{ID: 2, Author: "bot", State: "CHANGES_REQUESTED", At: time.Now()}, // latest
	}
	lc := &Lifecycle{
		Kind: claim.KindReviewer, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-review", LockLabel: "claude-reviewing", DoneLabel: "claude-reviewed",
		MergeLabel: "claude-merge", AddressLabel: "claude-address",
	}
	lc.successCleanupReviewer(context.Background(), 8, "sha8")
	labels := f.PRs[8].Labels
	if containsLabel(labels, "claude-merge") {
		t.Errorf("claude-merge should have been removed; got %v", labels)
	}
	if !containsLabel(labels, "claude-address") {
		t.Errorf("expected claude-address, got %v", labels)
	}
}

func TestReviewerSuccessCleanupCommentedDoesNotFlip(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[9] = &github.PullRequest{
		Number: 9, State: "open", HeadRefOid: "sha9",
		Labels: []string{"claude-review", "claude-reviewing"},
	}
	f.Reviews[9] = []github.Review{
		{ID: 1, Author: "bot", State: "COMMENTED", At: time.Now()},
	}
	lc := &Lifecycle{
		Kind: claim.KindReviewer, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-review", LockLabel: "claude-reviewing", DoneLabel: "claude-reviewed",
		MergeLabel: "claude-merge", AddressLabel: "claude-address",
	}
	lc.successCleanupReviewer(context.Background(), 9, "sha9")
	labels := f.PRs[9].Labels
	if containsLabel(labels, "claude-merge") || containsLabel(labels, "claude-address") {
		t.Errorf("COMMENTED should not flip queue labels; got %v", labels)
	}
}

// helper — add if not already present in the test file
func containsLabel(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/ -run TestReviewerSuccessCleanup -v`
Expected: FAIL — `MergeLabel` field unknown, cleanup doesn't flip.

- [ ] **Step 3: Extend `Lifecycle` struct**

In `internal/scheduler/lifecycle.go`, add to the struct (after the existing `ReviewLabel string` field):

```go
	// Merger/resolver-aware reviewer fields. Only read when Kind == KindReviewer.
	MergeLabel   string // added on APPROVED, removed on CHANGES_REQUESTED flip
	AddressLabel string // added on CHANGES_REQUESTED, removed on APPROVED flip
```

- [ ] **Step 4: Extend `successCleanupReviewer`**

Replace the existing `successCleanupReviewer` function with:

```go
func (l *Lifecycle) successCleanupReviewer(ctx context.Context, number int, headSha string) {
	// Order matters: write the rereviewed marker BEFORE flipping labels or
	// releasing the lock (see detailed comment below).
	if headSha != "" {
		ref := fmt.Sprintf("refs/cc-crew/rereviewed/pr-%d/%s", number, headSha)
		_ = l.createRefWithRetry(ctx, ref, headSha)
	}

	// Determine the latest verdict (APPROVED or CHANGES_REQUESTED; ignore
	// COMMENTED). Used to flip claude-merge / claude-address.
	verdict := l.latestReviewVerdict(ctx, number)

	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel)
	_ = l.GH.AddLabel(ctx, l.Repo, number, l.DoneLabel)

	switch verdict {
	case "APPROVED":
		if l.MergeLabel != "" {
			_ = l.GH.AddLabel(ctx, l.Repo, number, l.MergeLabel)
		}
		if l.AddressLabel != "" {
			_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.AddressLabel)
		}
	case "CHANGES_REQUESTED":
		if l.AddressLabel != "" {
			_ = l.GH.AddLabel(ctx, l.Repo, number, l.AddressLabel)
		}
		if l.MergeLabel != "" {
			_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.MergeLabel)
		}
	}

	_ = l.releaseWithRetry(ctx, l.Kind, number, true)
}

// latestReviewVerdict returns the state of the most recent non-COMMENTED
// review on the PR ("APPROVED" | "CHANGES_REQUESTED" | ""). Returns "" on
// any error or when only COMMENTED reviews exist.
func (l *Lifecycle) latestReviewVerdict(ctx context.Context, prNumber int) string {
	reviews, err := l.GH.ListReviews(ctx, l.Repo, prNumber)
	if err != nil {
		l.Log.Warn("list reviews for verdict failed", "pr", prNumber, "err", err)
		return ""
	}
	var latest *github.Review
	for i := range reviews {
		r := &reviews[i]
		if r.State != "APPROVED" && r.State != "CHANGES_REQUESTED" {
			continue
		}
		if latest == nil || r.At.After(latest.At) {
			latest = r
		}
	}
	if latest == nil {
		return ""
	}
	return latest.State
}
```

- [ ] **Step 5: Plumb `MergeLabel` / `AddressLabel` from config into the reviewer Lifecycle**

In `cmd/cc-crew/up.go`, inside the `if c.MaxReviewers > 0` block, extend the `Lifecycle` literal:

```go
			RoleGHToken: c.ReviewerGHToken, ClaudeOAuth: c.ClaudeOAuthToken, AnthropicAPIKey: c.AnthropicAPIKey,
			GitName: c.ReviewerGitName, GitEmail: c.ReviewerGitEmail,
			MergeLabel:   c.MergeLabel,
			AddressLabel: c.AddressLabel,
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/scheduler/ -v`
Expected: all PASS.
Run: `go build ./...`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
gofmt -w .
git add internal/scheduler/lifecycle.go internal/scheduler/lifecycle_test.go cmd/cc-crew/up.go
git commit -m "$(cat <<'EOF'
feat(reviewer): flip claude-merge/claude-address from latest verdict

After the reviewer posts its review, the lifecycle inspects the latest
non-COMMENTED review state and queues the PR for either the merger
(APPROVED → claude-merge) or the addresser (CHANGES_REQUESTED →
claude-address), removing the opposite stale label on verdict flips.
COMMENTED-only reviews leave queue labels untouched.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Extend `scheduler.listCandidates` and `tryClaimOne` for `KindMerger` and `KindResolver`

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Modify: `internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/scheduler/scheduler_test.go`:

```go
func TestSchedulerMergerListsAndClaims(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[11] = &github.PullRequest{
		Number: 11, State: "open", HeadRefOid: "sha11",
		Labels: []string{"claude-merge"},
	}
	f.PRs[12] = &github.PullRequest{
		Number: 12, State: "open", HeadRefOid: "sha12",
		Labels: []string{"claude-merge", "claude-merging"}, // already locked — must be skipped
	}
	disp := &fakeDispatcher{}
	s := &Scheduler{
		Kind: claim.KindMerger, Sem: NewSemaphore(1),
		Claimer: claim.New(f, repo), GH: f, Repo: repo, Dispatcher: disp,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-merge", LockLabel: "claude-merging",
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(disp.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := disp.calls()
	if len(got) != 1 || got[0] != 11 {
		t.Fatalf("dispatched = %v, want [11]", got)
	}
	if _, ok := f.Refs["refs/cc-crew/merge-lock/pr-11"]; !ok {
		t.Fatalf("merge-lock not created; refs = %v", keys(f.Refs))
	}
}

func TestSchedulerResolverListsAndClaims(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[21] = &github.PullRequest{
		Number: 21, State: "open", HeadRefOid: "sha21",
		Labels: []string{"claude-resolve-conflict"},
	}
	disp := &fakeDispatcher{}
	s := &Scheduler{
		Kind: claim.KindResolver, Sem: NewSemaphore(1),
		Claimer: claim.New(f, repo), GH: f, Repo: repo, Dispatcher: disp,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-resolve-conflict", LockLabel: "claude-resolving",
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(disp.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := disp.calls()
	if len(got) != 1 || got[0] != 21 {
		t.Fatalf("dispatched = %v, want [21]", got)
	}
	if _, ok := f.Refs["refs/cc-crew/resolve-lock/pr-21"]; !ok {
		t.Fatalf("resolve-lock not created; refs = %v", keys(f.Refs))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/ -run "TestSchedulerMerger|TestSchedulerResolver" -v`
Expected: FAIL — listCandidates / tryClaimOne don't handle these Kinds, returns `nil, nil`.

- [ ] **Step 3: Extend `listCandidates`**

Edit `internal/scheduler/scheduler.go` — extend the switch in `listCandidates`. Replace the single `case claim.KindReviewer, claim.KindAddresser:` arm with one that also includes the new Kinds:

```go
	case claim.KindReviewer, claim.KindAddresser, claim.KindMerger, claim.KindResolver:
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
```

- [ ] **Step 4: Extend `tryClaimOne`**

In `internal/scheduler/scheduler.go`, replace the existing `case claim.KindReviewer, claim.KindAddresser:` with:

```go
	case claim.KindReviewer, claim.KindAddresser, claim.KindMerger, claim.KindResolver:
		pr, err := s.GH.GetPR(ctx, s.Repo, n)
		if err != nil {
			return false, "", err
		}
		won, err := s.Claimer.TryClaim(ctx, s.Kind, n, pr.HeadRefOid)
		return won, pr.HeadRefOid, err
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/scheduler/ -v`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add internal/scheduler/scheduler.go internal/scheduler/scheduler_test.go
git commit -m "$(cat <<'EOF'
feat(scheduler): list & SHA-pinned claim for KindMerger/KindResolver

Both new Kinds operate on PRs (not issues), so listCandidates and
tryClaimOne route them through ListPRs + PR-head-SHA-pinned claims,
reusing the existing reviewer/addresser plumbing.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Implement the merger `Dispatch` state machine

**Files:**
- Create: `internal/scheduler/merger.go`
- Create: `internal/scheduler/merger_test.go`
- Modify: `internal/scheduler/lifecycle.go` (route KindMerger in `Dispatch`; add new fields)

- [ ] **Step 1: Write the failing tests**

Create `internal/scheduler/merger_test.go`:

```go
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func newMergerLifecycle(f *github.FakeClient, repo github.Repo) *Lifecycle {
	return &Lifecycle{
		Kind: claim.KindMerger, Claimer: claim.New(f, repo), GH: f, Repo: repo,
		Log:                  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel:           "claude-merge",
		LockLabel:            "claude-merging",
		ResolveConflictLabel: "claude-resolve-conflict",
		ConflictBlockedLabel: "claude-conflict-blocked",
	}
}

func TestMergerCleanMergesAndClearsLabels(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[1] = &github.PullRequest{
		Number: 1, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "CLEAN",
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 1)
	if f.PRs[1].State != "closed" || !f.PRs[1].Merged {
		t.Errorf("PR not merged: %+v", f.PRs[1])
	}
	if containsLabel(f.PRs[1].Labels, "claude-merging") || containsLabel(f.PRs[1].Labels, "claude-merge") {
		t.Errorf("queue/lock labels not cleared: %v", f.PRs[1].Labels)
	}
}

func TestMergerBehindCallsUpdateBranchAndReleases(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[2] = &github.PullRequest{
		Number: 2, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "BEHIND",
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 2)
	if !f.UpdateBranchCalled[2] {
		t.Error("UpdateBranch not called")
	}
	if !containsLabel(f.PRs[2].Labels, "claude-merge") {
		t.Errorf("claude-merge should stay for next-tick retry: %v", f.PRs[2].Labels)
	}
	if containsLabel(f.PRs[2].Labels, "claude-merging") {
		t.Errorf("claude-merging should be released: %v", f.PRs[2].Labels)
	}
}

func TestMergerDirtyDispatchesResolverAndReleases(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[3] = &github.PullRequest{
		Number: 3, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "DIRTY",
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 3)
	if !containsLabel(f.PRs[3].Labels, "claude-resolve-conflict") {
		t.Errorf("claude-resolve-conflict not added: %v", f.PRs[3].Labels)
	}
	if !containsLabel(f.PRs[3].Labels, "claude-merge") {
		t.Errorf("claude-merge should stay so merger retries after resolve: %v", f.PRs[3].Labels)
	}
	if containsLabel(f.PRs[3].Labels, "claude-merging") {
		t.Errorf("claude-merging should be released: %v", f.PRs[3].Labels)
	}
}

func TestMergerBlockedIsTerminal(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[4] = &github.PullRequest{
		Number: 4, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "BLOCKED",
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 4)
	if !containsLabel(f.PRs[4].Labels, "claude-conflict-blocked") {
		t.Errorf("claude-conflict-blocked not added: %v", f.PRs[4].Labels)
	}
	if containsLabel(f.PRs[4].Labels, "claude-merge") {
		t.Errorf("claude-merge should be removed on terminal: %v", f.PRs[4].Labels)
	}
	if len(f.Comments[4]) == 0 {
		t.Error("expected escalation comment on PR")
	}
}

func TestMergerUnstableWithFailingCheckIsTerminal(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[5] = &github.PullRequest{
		Number: 5, State: "open", HeadRefOid: "sha5", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "UNSTABLE",
	}
	f.CheckRuns["sha5"] = []github.CheckRun{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "lint", Status: "completed", Conclusion: "failure"},
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 5)
	if !containsLabel(f.PRs[5].Labels, "claude-conflict-blocked") {
		t.Errorf("claude-conflict-blocked not added: %v", f.PRs[5].Labels)
	}
}

func TestMergerUnstableWithPendingCheckIsRetry(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[6] = &github.PullRequest{
		Number: 6, State: "open", HeadRefOid: "sha6", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "UNSTABLE",
	}
	f.CheckRuns["sha6"] = []github.CheckRun{
		{Name: "build", Status: "in_progress", Conclusion: ""},
	}
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 6)
	if containsLabel(f.PRs[6].Labels, "claude-conflict-blocked") {
		t.Errorf("should not be terminal for pending checks: %v", f.PRs[6].Labels)
	}
	if !containsLabel(f.PRs[6].Labels, "claude-merge") {
		t.Errorf("claude-merge should remain for retry: %v", f.PRs[6].Labels)
	}
	if containsLabel(f.PRs[6].Labels, "claude-merging") {
		t.Errorf("claude-merging should be released: %v", f.PRs[6].Labels)
	}
}

func TestMergerMergeReturnsConflictDispatchesResolver(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[7] = &github.PullRequest{
		Number: 7, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "CLEAN",
	}
	f.MergePRHook = func(n int) error { return github.ErrMergeConflict }
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 7)
	if !containsLabel(f.PRs[7].Labels, "claude-resolve-conflict") {
		t.Errorf("expected claude-resolve-conflict after race-condition conflict: %v", f.PRs[7].Labels)
	}
}

func TestMergerMergeReturnsOtherErrorIsTerminal(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "o", Name: "n"}
	f.PRs[8] = &github.PullRequest{
		Number: 8, State: "open", HeadRefOid: "sha", BaseRefName: "main",
		Labels: []string{"claude-merge", "claude-merging"}, MergeStateStatus: "CLEAN",
	}
	f.MergePRHook = func(n int) error { return errors.New("permission denied") }
	lc := newMergerLifecycle(f, repo)
	lc.dispatchMerger(context.Background(), slog.Default(), 8)
	if !containsLabel(f.PRs[8].Labels, "claude-conflict-blocked") {
		t.Errorf("expected claude-conflict-blocked: %v", f.PRs[8].Labels)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/ -run TestMerger -v`
Expected: FAIL — `dispatchMerger` undefined, struct missing fields.

- [ ] **Step 3: Add fields to `Lifecycle`**

In `internal/scheduler/lifecycle.go`, extend the struct (near the MergeLabel/AddressLabel fields added in Task 7):

```go
	// Merger-only fields. Consumed when Kind == KindMerger.
	ResolveConflictLabel string // queue label for the resolver, set by merger on DIRTY
	ConflictBlockedLabel string // terminal-failure label
```

Add the routing in `Dispatch`:

```go
func (l *Lifecycle) Dispatch(ctx context.Context, number int) {
	log := l.Log.With("kind", kindName(l.Kind), "number", number)
	log.Info("dispatch start")

	switch l.Kind {
	case claim.KindImplementer:
		l.dispatchImplementer(ctx, log, number)
	case claim.KindReviewer:
		l.dispatchReviewer(ctx, log, number)
	case claim.KindAddresser:
		l.dispatchAddresser(ctx, log, number)
	case claim.KindMerger:
		l.dispatchMerger(ctx, log, number)
	case claim.KindResolver:
		l.dispatchResolver(ctx, log, number)
	}
}
```

Also update `kindName` to add the new cases:

```go
func kindName(k claim.Kind) string {
	switch k {
	case claim.KindImplementer:
		return "implementer"
	case claim.KindAddresser:
		return "addresser"
	case claim.KindMerger:
		return "merger"
	case claim.KindResolver:
		return "resolver"
	}
	return "reviewer"
}
```

- [ ] **Step 4: Implement the merger state machine**

Create `internal/scheduler/merger.go`:

```go
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// dispatchMerger runs the merger state machine for one open PR carrying
// claude-merge. It assumes the scheduler has already claimed the PR and
// added l.LockLabel; on exit it always removes l.LockLabel and either
// leaves claude-merge in place (for retry paths) or removes it (on merge
// success / terminal failure).
func (l *Lifecycle) dispatchMerger(ctx context.Context, log *slog.Logger, number int) {
	// Always release lock label and claimer on any return.
	defer func() {
		_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
		_ = l.releaseWithRetry(ctx, claim.KindMerger, number, true)
	}()

	pr, err := l.GH.GetPR(ctx, l.Repo, number)
	if err != nil {
		log.Error("get PR failed", "err", err)
		return // retry next tick
	}

	switch pr.MergeStateStatus {
	case "CLEAN", "HAS_HOOKS":
		l.mergerAttemptMerge(ctx, log, &pr)
	case "BEHIND":
		l.mergerUpdateBranch(ctx, log, &pr)
	case "DIRTY":
		l.mergerHandoffResolver(ctx, log, &pr, "PR is DIRTY; dispatching resolver")
	case "UNSTABLE":
		l.mergerHandleUnstable(ctx, log, &pr)
	case "UNKNOWN", "":
		log.Info("mergeStateStatus UNKNOWN; leaving claude-merge for retry", "pr", number)
	case "BLOCKED":
		l.mergerTerminal(ctx, log, &pr, "PR is BLOCKED by branch-protection rules; merger cannot proceed")
	case "DRAFT":
		l.mergerTerminal(ctx, log, &pr, "PR is still a draft; mark ready-for-review to merge")
	default:
		l.mergerTerminal(ctx, log, &pr, fmt.Sprintf("unknown mergeStateStatus: %s", pr.MergeStateStatus))
	}
}

// mergerAttemptMerge calls MergePR; on success clears claude-merge, on
// ErrMergeConflict hands off to resolver, on any other error marks terminal.
func (l *Lifecycle) mergerAttemptMerge(ctx context.Context, log *slog.Logger, pr *github.PullRequest) {
	err := l.GH.MergePR(ctx, l.Repo, pr.Number, pr.HeadRefOid, github.MergeMethodRebase, true)
	if err == nil {
		log.Info("merged", "pr", pr.Number)
		_ = l.GH.RemoveLabel(ctx, l.Repo, pr.Number, l.QueueLabel)
		return
	}
	if errors.Is(err, github.ErrMergeConflict) {
		l.mergerHandoffResolver(ctx, log, pr, "gh pr merge reported conflict; dispatching resolver")
		return
	}
	l.mergerTerminal(ctx, log, pr, fmt.Sprintf("merge failed: %v", err))
}

// mergerUpdateBranch calls the rebase update-branch endpoint. Success:
// leave claude-merge on for next-tick retry. Failure: terminal.
func (l *Lifecycle) mergerUpdateBranch(ctx context.Context, log *slog.Logger, pr *github.PullRequest) {
	if err := l.GH.UpdateBranch(ctx, l.Repo, pr.Number, pr.HeadRefOid, github.UpdateMethodRebase); err != nil {
		l.mergerTerminal(ctx, log, pr, fmt.Sprintf("update-branch failed: %v", err))
		return
	}
	log.Info("update-branch called; will retry merge next tick", "pr", pr.Number)
}

// mergerHandoffResolver adds the resolver queue label and leaves
// claude-merge on so the merger re-tries after the resolver succeeds.
func (l *Lifecycle) mergerHandoffResolver(ctx context.Context, log *slog.Logger, pr *github.PullRequest, reason string) {
	log.Info("handoff to resolver", "pr", pr.Number, "reason", reason)
	if l.ResolveConflictLabel != "" {
		_ = l.GH.AddLabel(ctx, l.Repo, pr.Number, l.ResolveConflictLabel)
	}
	// Do NOT remove claude-merge — merger picks it up again after resolver clears
	// claude-resolve-conflict and the reviewer re-approves.
}

// mergerHandleUnstable inspects check runs: any hard-failed check → terminal;
// only pending → retry.
func (l *Lifecycle) mergerHandleUnstable(ctx context.Context, log *slog.Logger, pr *github.PullRequest) {
	runs, err := l.GH.GetCheckRuns(ctx, l.Repo, pr.HeadRefOid)
	if err != nil {
		log.Warn("get check runs failed; leaving claude-merge for retry", "err", err)
		return
	}
	var failed []string
	anyPending := false
	for _, cr := range runs {
		if cr.Status != "completed" {
			anyPending = true
			continue
		}
		switch cr.Conclusion {
		case "success", "neutral", "skipped":
			// ok
		case "failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale":
			failed = append(failed, fmt.Sprintf("%s=%s", cr.Name, cr.Conclusion))
		default:
			// Unknown conclusions: treat as pending (conservative).
			anyPending = true
		}
	}
	if len(failed) > 0 {
		l.mergerTerminal(ctx, log, pr, fmt.Sprintf("required checks failed: %v", failed))
		return
	}
	if anyPending {
		log.Info("checks still pending; leaving claude-merge for retry", "pr", pr.Number)
		return
	}
	// All checks passed despite UNSTABLE — try merge anyway.
	l.mergerAttemptMerge(ctx, log, pr)
}

// mergerTerminal posts a comment, applies claude-conflict-blocked, and
// removes claude-merge so nothing retries.
func (l *Lifecycle) mergerTerminal(ctx context.Context, log *slog.Logger, pr *github.PullRequest, reason string) {
	log.Warn("merger terminal", "pr", pr.Number, "reason", reason)
	body := fmt.Sprintf("🚫 cc-crew merger cannot proceed: %s\n\nLeaving this PR for human attention. Remove `%s` after resolving to resume automation.",
		reason, l.ConflictBlockedLabel)
	if err := l.GH.CreateComment(ctx, l.Repo, pr.Number, body); err != nil {
		log.Warn("create terminal comment failed", "err", err)
	}
	if l.ConflictBlockedLabel != "" {
		_ = l.GH.AddLabel(ctx, l.Repo, pr.Number, l.ConflictBlockedLabel)
	}
	_ = l.GH.RemoveLabel(ctx, l.Repo, pr.Number, l.QueueLabel)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/scheduler/ -v`
Expected: all PASS (the eight merger tests + existing).

- [ ] **Step 6: Run vet + build**

Run: `go vet ./... && go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
gofmt -w .
git add internal/scheduler/lifecycle.go internal/scheduler/merger.go internal/scheduler/merger_test.go
git commit -m "$(cat <<'EOF'
feat(merger): add orchestrator-side merger state machine

Dispatcher branches on mergeStateStatus: CLEAN/HAS_HOOKS → rebase merge;
BEHIND → rebase update-branch then retry; DIRTY → handoff to resolver;
UNSTABLE → inspect checks (failed → terminal, pending → retry);
BLOCKED/DRAFT/unknown → terminal with escalation comment.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Implement the resolver `Dispatch` (reuses implementer Docker image)

**Files:**
- Create: `internal/scheduler/resolver.go`
- Create: `internal/scheduler/resolver_test.go`
- Modify: `internal/scheduler/lifecycle.go` (add Resolver-aware fields if missing)

- [ ] **Step 1: Write the failing tests**

Create `internal/scheduler/resolver_test.go`:

```go
package scheduler

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// fakeDockerRunner lets us short-circuit docker.Run in resolver tests.
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
	lc.dockerRunFn = fake.run(0, nil)
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
	lc.dockerRunFn = fake.run(1, nil)
	lc.dispatchResolver(context.Background(), slog.Default(), 2)
	labels := f.PRs[2].Labels
	if !containsLabel(labels, "claude-conflict-blocked") {
		t.Errorf("expected claude-conflict-blocked: %v", labels)
	}
	if containsLabel(labels, "claude-merge") {
		t.Errorf("claude-merge should be removed on terminal: %v", labels)
	}
	if len(f.Comments[2]) == 0 {
		t.Error("expected escalation comment")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/ -run TestResolver -v`
Expected: FAIL — `dockerRunFn`, `dispatchResolver` undefined.

- [ ] **Step 3: Add the `dockerRunFn` seam to `Lifecycle`**

In `internal/scheduler/lifecycle.go`, add field to the struct (after `Docker *docker.Runner`):

```go
	// Test-only seam. When nil, the resolver calls l.Docker.Run via
	// the real docker.Runner; tests can substitute a stub that returns
	// a predetermined (code, err).
	dockerRunFn func() (int, error)
```

- [ ] **Step 4: Implement `dispatchResolver`**

Create `internal/scheduler/resolver.go`:

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
)

// dispatchResolver dispatches a container on the implementer image with
// CC_ROLE=resolver. On exit 0: clear resolver labels, re-queue the reviewer
// (remove claude-reviewed, add claude-review). On failure or timeout: apply
// claude-conflict-blocked, remove claude-merge, post a comment.
func (l *Lifecycle) dispatchResolver(ctx context.Context, log *slog.Logger, number int) {
	pr, err := l.GH.GetPR(ctx, l.Repo, number)
	if err != nil {
		log.Error("get PR failed", "err", err)
		l.resolverFailCleanup(ctx, log, number)
		return
	}
	if pr.HeadRefOid == "" {
		log.Error("PR head SHA empty")
		l.resolverFailCleanup(ctx, log, number)
		return
	}

	wtPath, err := l.WT.AddDetached(ctx, fmt.Sprintf("resolve-%d", number), pr.HeadRefOid)
	if err != nil {
		log.Error("worktree add detached failed", "err", err)
		l.resolverFailCleanup(ctx, log, number)
		return
	}
	defer func() { _ = l.WT.Remove(ctx, fmt.Sprintf("resolve-%d", number)) }()

	spec := l.buildResolverRunSpec(number, pr.BaseRefName, pr.HeadRefName, wtPath)
	runCtx, cancel := context.WithTimeout(ctx, l.TaskTimeout)
	defer cancel()

	runFn := l.dockerRunFn
	if runFn == nil {
		runFn = func() (int, error) { return l.Docker.Run(runCtx, spec) }
	}
	code, err := runFn()

	if err != nil || code != 0 {
		log.Warn("resolver exited non-zero", "code", code, "err", err)
		l.resolverFailCleanup(ctx, log, number)
		return
	}
	l.resolverSuccessCleanup(ctx, log, number)
}

func (l *Lifecycle) buildResolverRunSpec(prNumber int, baseBranch, headBranch, wtPath string) docker.RunSpec {
	name := fmt.Sprintf("cc-crew-resolve-%s-%s-%d",
		safeName(l.Repo.Owner), safeName(l.Repo.Name), prNumber)
	labels := map[string]string{
		"cc-crew.repo": l.Repo.String(),
		"cc-crew.role": "resolver",
		"cc-crew.pr":   fmt.Sprint(prNumber),
	}
	env := map[string]string{
		"CC_ROLE":                 "resolver",
		"CC_MODEL":                l.Model,
		"CC_REPO":                 l.Repo.String(),
		"CC_PR_NUM":               fmt.Sprint(prNumber),
		"CC_BASE_BRANCH":          baseBranch,
		"CC_HEAD_BRANCH":          headBranch,
		"GH_TOKEN":                l.RoleGHToken,
		"CLAUDE_CODE_OAUTH_TOKEN": l.ClaudeOAuth,
		"ANTHROPIC_API_KEY":       l.AnthropicAPIKey,
		"GIT_AUTHOR_NAME":         l.GitName,
		"GIT_AUTHOR_EMAIL":        l.GitEmail,
		"GIT_COMMITTER_NAME":      l.GitName,
		"GIT_COMMITTER_EMAIL":     l.GitEmail,
		"IS_SANDBOX":              "1",
	}
	if l.MaxTurns > 0 {
		env["CC_MAX_TURNS"] = fmt.Sprint(l.MaxTurns)
	}
	prefix := fmt.Sprintf("[pr-%d] ", prNumber)
	return docker.RunSpec{
		Image:  l.Image,
		Name:   name,
		Labels: labels,
		Env:    env,
		Stdout: NewPrefixedWriter(lockedStdout, prefix, prNumber),
		Stderr: NewPrefixedWriter(lockedStderr, prefix, prNumber),
		Mounts: []docker.Mount{
			{HostPath: wtPath, ContainerPath: "/workspace"},
			{
				HostPath:      filepath.Join(l.WT.RepoDir, ".git"),
				ContainerPath: filepath.Join(l.WT.RepoDir, ".git"),
			},
		},
	}
}

func (l *Lifecycle) resolverSuccessCleanup(ctx context.Context, log *slog.Logger, number int) {
	log.Info("resolver succeeded", "pr", number)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel)
	// Re-trigger reviewer: the resolver force-pushed, so any prior approval
	// may have been dismissed by branch-protection. Flip labels so the
	// reviewer scheduler picks this PR up again.
	if l.DoneLabel != "" {
		_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.DoneLabel)
	}
	if l.ReviewLabel != "" {
		_ = l.GH.AddLabel(ctx, l.Repo, number, l.ReviewLabel)
	}
	_ = l.releaseWithRetry(ctx, claim.KindResolver, number, true)
}

func (l *Lifecycle) resolverFailCleanup(ctx context.Context, log *slog.Logger, number int) {
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.LockLabel)
	_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.QueueLabel)
	if l.ConflictBlockedLabel != "" {
		_ = l.GH.AddLabel(ctx, l.Repo, number, l.ConflictBlockedLabel)
	}
	if l.MergeLabel != "" {
		_ = l.GH.RemoveLabel(ctx, l.Repo, number, l.MergeLabel)
	}
	body := fmt.Sprintf("🚫 cc-crew resolver could not rebase this PR onto `%s`. Resolve the conflicts manually and remove `%s` to resume automation.",
		"base branch", l.ConflictBlockedLabel)
	if err := l.GH.CreateComment(ctx, l.Repo, number, body); err != nil {
		log.Warn("create terminal comment failed", "err", err)
	}
	_ = l.releaseWithRetry(ctx, claim.KindResolver, number, true)
	// Sleep 0 just to silence unused time import when this file gets refactored.
	_ = time.Nanosecond
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/scheduler/ -v`
Expected: all PASS.

- [ ] **Step 6: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
gofmt -w .
git add internal/scheduler/resolver.go internal/scheduler/resolver_test.go internal/scheduler/lifecycle.go
git commit -m "$(cat <<'EOF'
feat(resolver): add conflict-resolver dispatch using implementer image

Resolver dispatches a container with CC_ROLE=resolver on a detached
worktree pinned to the PR head. On exit 0, re-triggers the reviewer
(removes claude-reviewed, adds claude-review). On failure, marks the
PR with claude-conflict-blocked, removes claude-merge, and comments.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Create resolver persona (`personas/resolver/CLAUDE.md` + `settings.json`)

**Files:**
- Create: `personas/resolver/CLAUDE.md`
- Create: `personas/resolver/settings.json`

- [ ] **Step 1: Create `personas/resolver/CLAUDE.md`**

```markdown
# Resolver persona

You are an autonomous conflict-resolver dispatched by cc-crew to rebase
a single GitHub PR branch onto its base and resolve any merge conflicts.

## Inputs

- `/tmp/pr.md` — PR title, head branch, base branch, body (prepared by the entrypoint).
- `$CC_PR_NUM` — PR number.
- `$CC_BASE_BRANCH` — base branch name (e.g., `main`).
- `$CC_HEAD_BRANCH` — PR head branch name (e.g., `claude/issue-42`).
- `$CC_REPO` — `owner/name` of the repo.

The worktree is already checked out at the current PR head SHA.

## Workflow

1. `git fetch origin`
2. `git checkout "$CC_HEAD_BRANCH"` (or stay on detached HEAD and branch it; use whichever is cleanest).
3. `git rebase "origin/$CC_BASE_BRANCH"`.
4. If the rebase reports no conflicts, skip to step 7.
5. For each conflicted file, resolve the markers by understanding what each side intended. Preserve the PR's functional changes while incorporating changes from the base. If you cannot reason about the conflict safely, exit non-zero — do not guess.
6. `git add <files>` and `git rebase --continue` until the rebase completes.
7. Run the repo's obvious checks (`go test ./...`, `make test`, `pytest`, etc.) that were runnable before. If tests fail solely because of your conflict resolution, fix your resolution. If tests fail for reasons unrelated to the conflict (flaky tests, broken base branch), stop and exit non-zero — that is out of scope for the resolver.
8. `git push --force-with-lease origin "$CC_HEAD_BRANCH"`.

## Hard constraints

- Do **not** rewrite commits unrelated to the rebase (no `git rebase -i`, no squashing, no amending).
- Do **not** push to any branch other than `$CC_HEAD_BRANCH`.
- Do **not** merge the PR yourself.
- Do **not** disable tests, skip linters, or modify CI configuration.
- Do **not** attempt to fix bugs in the PR or the base — your only job is conflict resolution.
- If you cannot resolve a conflict with high confidence, exit non-zero with a short stderr summary of which files you could not resolve and why. cc-crew will escalate the PR to a human.

## Environment

You run with `--dangerously-skip-permissions`. `git push --force-with-lease` is allowed; `git push --force` (non-lease) is not. The reviewer will re-review the PR after you push.
```

- [ ] **Step 2: Create `personas/resolver/settings.json`**

```json
{
  "_comment": "Documented scoped permissions for the resolver. Not loaded at runtime when cc-crew dispatches this persona with --dangerously-skip-permissions.",
  "permissions": {
    "allow": [
      "Bash(git:*)",
      "Bash(gh pr view:*)",
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
      "Bash(gh pr merge:*)",
      "Bash(gh pr create:*)"
    ]
  }
}
```

- [ ] **Step 3: Commit**

```bash
git add personas/resolver/CLAUDE.md personas/resolver/settings.json
git commit -m "$(cat <<'EOF'
feat(personas): add resolver persona

New persona prompt + scoped permissions for the conflict resolver. The
resolver rebases the PR head onto base, resolves conflicts, and
force-pushes with --force-with-lease.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Extend `scripts/cc-crew-run` with `CC_ROLE=resolver` case

**Files:**
- Modify: `scripts/cc-crew-run`

- [ ] **Step 1: Add the resolver branch**

In `scripts/cc-crew-run`, after the closing `;;` of the `reviewer)` case and before the `*)` default, insert:

```bash
  resolver)
    : "${CC_PR_NUM:?CC_PR_NUM required}"
    : "${CC_BASE_BRANCH:?CC_BASE_BRANCH required}"
    : "${CC_HEAD_BRANCH:?CC_HEAD_BRANCH required}"

    gh pr view "$CC_PR_NUM" -R "$CC_REPO" --json number,title,body,baseRefName,headRefName \
      -q '"# PR #\(.number): \(.title)\n\nBase: \(.baseRefName)\nHead: \(.headRefName)\n\n\(.body)"' > /tmp/pr.md

    PROMPT="You are the cc-crew resolver. Read /tmp/pr.md. Rebase ${CC_HEAD_BRANCH} onto origin/${CC_BASE_BRANCH}, resolve any merge conflicts, and git push --force-with-lease. Follow your CLAUDE.md instructions exactly."

    exec claude -p "$PROMPT" \
      --model "$CC_MODEL" \
      "${max_turns_args[@]}" \
      --dangerously-skip-permissions
    ;;
```

- [ ] **Step 2: Sanity-check with bash**

Run: `bash -n scripts/cc-crew-run`
Expected: no syntax errors.

- [ ] **Step 3: Commit**

```bash
git add scripts/cc-crew-run
git commit -m "$(cat <<'EOF'
feat(entrypoint): add CC_ROLE=resolver branch to cc-crew-run

Wires the resolver persona into the container entrypoint: prepare
/tmp/pr.md, invoke claude -p with the resolver prompt, rely on the
persona CLAUDE.md for behavior.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Wire merger + resolver schedulers in `cmd/cc-crew/up.go`

**Files:**
- Modify: `cmd/cc-crew/up.go`

- [ ] **Step 1: Add the merger scheduler block**

In `cmd/cc-crew/up.go`, after the existing `addrSched` block (just before the `implSweeper` declaration), insert:

```go
	if c.MaxMergers > 0 {
		mergerLC := &scheduler.Lifecycle{
			Kind: claim.KindMerger, Claimer: claimer, GH: ghc, Repo: repo,
			Log:                  log,
			QueueLabel:           c.MergeLabel,
			LockLabel:            c.MergingLabel,
			ResolveConflictLabel: c.ResolveConflictLabel,
			ConflictBlockedLabel: c.ConflictBlockedLabel,
			RoleGHToken:          c.ReviewerGHToken,
		}
		mergerS := &scheduler.Scheduler{
			Kind: claim.KindMerger, Sem: scheduler.NewSemaphore(c.MaxMergers),
			Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: mergerLC, Log: log,
			QueueLabel: c.MergeLabel, LockLabel: c.MergingLabel,
		}
		schedulers = append(schedulers, mergerS)

		// Resolver: requires implementer to be enabled (shares its semaphore
		// and uses its image). Otherwise conflicts cannot be resolved.
		if c.MaxImplementers > 0 {
			resolverLC := &scheduler.Lifecycle{
				Kind: claim.KindResolver, Claimer: claimer, GH: ghc, Repo: repo,
				WT: wt, Docker: dr, Log: log,
				QueueLabel:           c.ResolveConflictLabel,
				LockLabel:            c.ResolvingLabel,
				ReviewLabel:          c.ReviewLabel,
				DoneLabel:            c.ReviewedLabel,
				MergeLabel:           c.MergeLabel,
				ConflictBlockedLabel: c.ConflictBlockedLabel,
				Image:                c.Image,
				Model:                c.Model,
				MaxTurns:             c.ImplMaxTurns,
				TaskTimeout:          c.ImplTaskTimeout,
				RoleGHToken:          c.ImplementerGHToken,
				ClaudeOAuth:          c.ClaudeOAuthToken,
				AnthropicAPIKey:      c.AnthropicAPIKey,
				GitName:              c.ImplementerGitName,
				GitEmail:             c.ImplementerGitEmail,
			}
			resolverS := &scheduler.Scheduler{
				Kind: claim.KindResolver, Sem: schedulers[0].Sem, // share implementer semaphore
				Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: resolverLC, Log: log,
				QueueLabel: c.ResolveConflictLabel, LockLabel: c.ResolvingLabel,
			}
			schedulers = append(schedulers, resolverS)
		} else {
			log.Warn("max-mergers > 0 but max-implementers == 0; resolver disabled (conflicts will be terminal)")
		}
	}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 3: Run full suite**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
gofmt -w .
git add cmd/cc-crew/up.go
git commit -m "$(cat <<'EOF'
feat(up): wire merger and resolver schedulers

When --max-mergers > 0, spawn a dedicated merger scheduler (pure Go,
no Docker, uses reviewer token). When implementer is also enabled,
spawn a resolver scheduler sharing the implementer semaphore and image.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Extend `internal/reset` to clear the new labels

**Files:**
- Modify: `internal/reset/reset.go`
- Modify: `internal/reset/reset_test.go`
- Modify: `cmd/cc-crew/reset.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/reset/reset_test.go`:

```go
func TestResetClearsMergerAndResolverLabels(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[50] = &github.PullRequest{
		Number: 50, State: "open",
		Labels: []string{"claude-merging", "claude-resolving", "claude-conflict-blocked"},
	}
	o := Options{
		GH: f, Repo: repo,
		TaskLabel: "claude-task", ProcessingLabel: "claude-processing", DoneLabel: "claude-done",
		ReviewLabel: "claude-review", ReviewingLabel: "claude-reviewing", ReviewedLabel: "claude-reviewed",
		AddressLabel: "claude-address", AddressingLabel: "claude-addressing", AddressedLabel: "claude-addressed",
		MergeLabel: "claude-merge", MergingLabel: "claude-merging",
		ResolveConflictLabel: "claude-resolve-conflict", ResolvingLabel: "claude-resolving",
		ConflictBlockedLabel: "claude-conflict-blocked",
	}
	p, err := Compute(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Execute(context.Background(), o, p, &buf); err != nil {
		t.Fatal(err)
	}
	labels := f.PRs[50].Labels
	for _, stale := range []string{"claude-merging", "claude-resolving", "claude-conflict-blocked"} {
		if containsLabelReset(labels, stale) {
			t.Errorf("%q not cleared: %v", stale, labels)
		}
	}
}

func containsLabelReset(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
```

(Imports needed: `bytes`, `context`, already present in file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reset/ -run TestResetClearsMergerAndResolverLabels -v`
Expected: FAIL — Options fields missing.

- [ ] **Step 3: Extend `Options` and reset logic**

In `internal/reset/reset.go`, add fields to the `Options` struct:

```go
	MergeLabel           string
	MergingLabel         string
	ResolveConflictLabel string
	ResolvingLabel       string
	ConflictBlockedLabel string
```

Then in `Compute`, the orphan-PR scan — extend the label list from:

```go
	for _, label := range []string{o.ReviewingLabel, o.ReviewedLabel, o.AddressingLabel, o.AddressedLabel} {
```

to:

```go
	for _, label := range []string{
		o.ReviewingLabel, o.ReviewedLabel,
		o.AddressingLabel, o.AddressedLabel,
		o.MergingLabel, o.ResolvingLabel, o.ConflictBlockedLabel,
	} {
```

In `Execute`, the PR-requeue block, after removing `AddressedLabel`, add:

```go
		if o.MergingLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.MergingLabel)
		}
		if o.ResolvingLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ResolvingLabel)
		}
		if o.ConflictBlockedLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ConflictBlockedLabel)
		}
		if o.MergeLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.MergeLabel)
		}
		if o.ResolveConflictLabel != "" {
			_ = o.GH.RemoveLabel(ctx, o.Repo, n, o.ResolveConflictLabel)
		}
```

Also add the three new claim-ref prefixes to `refPrefixes`:

```go
	"cc-crew/merge-lock/pr-",
	"cc-crew/merge-claim/pr-",
	"cc-crew/resolve-lock/pr-",
	"cc-crew/resolve-claim/pr-",
```

- [ ] **Step 4: Plumb fields in `cmd/cc-crew/reset.go`**

In `cmd/cc-crew/reset.go`, find where `Options` is built and add the five new fields (follow the pattern used for existing labels — `firstNonEmpty(os.Getenv("CC_MERGE_LABEL"), defaults.MergeLabel)` etc.). Read the existing file to find the exact insertion point:

```go
		MergeLabel:           firstNonEmpty(os.Getenv("CC_MERGE_LABEL"), defaults.MergeLabel),
		MergingLabel:         firstNonEmpty(os.Getenv("CC_MERGING_LABEL"), defaults.MergingLabel),
		ResolveConflictLabel: firstNonEmpty(os.Getenv("CC_RESOLVE_CONFLICT_LABEL"), defaults.ResolveConflictLabel),
		ResolvingLabel:       firstNonEmpty(os.Getenv("CC_RESOLVING_LABEL"), defaults.ResolvingLabel),
		ConflictBlockedLabel: firstNonEmpty(os.Getenv("CC_CONFLICT_BLOCKED_LABEL"), defaults.ConflictBlockedLabel),
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/reset/ -v && go test ./cmd/cc-crew/ -v`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add internal/reset/reset.go internal/reset/reset_test.go cmd/cc-crew/reset.go
git commit -m "$(cat <<'EOF'
feat(reset): clear merger and resolver labels + ref prefixes

cc-crew reset now recognizes the five new merger/resolver labels and
the corresponding cc-crew/{merge,resolve}-{lock,claim}/pr- ref prefixes
so a reset fully cleans up in-flight merger/resolver state.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: Extend SIGINT cleanup in `up.go` for merger and resolver

**Files:**
- Modify: `cmd/cc-crew/up.go`

- [ ] **Step 1: Add `merger` and `resolver` cases to the shutdown switch**

In the SIGINT cleanup block at the end of `runUp`, extend the `switch role` statement. After the `case "reviewer":` arm, add:

```go
		case "resolver":
			prStr := e.Labels["cc-crew.pr"]
			var n int
			if _, err := fmt.Sscan(prStr, &n); err != nil || n == 0 {
				continue
			}
			if err := ghc.RemoveLabel(shutCtx, repo, n, c.ResolvingLabel); err != nil {
				log.Warn("sigint cleanup: remove resolving label", "pr", n, "err", err)
			}
			if err := claimer.Release(shutCtx, claim.KindResolver, n, true); err != nil {
				log.Warn("sigint cleanup: release resolver claim", "pr", n, "err", err)
			}
```

(The merger has no Docker container, so no `docker.PS` entry exists for it; SIGINT cleanup for merger claims is handled by the reclaim sweeper, which works fine.)

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
gofmt -w .
git add cmd/cc-crew/up.go
git commit -m "$(cat <<'EOF'
feat(up): clean up resolver containers and claims on SIGINT

On orchestrator shutdown, kill resolver containers and release their
claims + resolving label so the next start doesn't wait for the reclaim
sweeper to recover orphaned locks.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: Final integration check

**Files:** none (verification only)

- [ ] **Step 1: Run full suite**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 2: Run vet**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 3: Run gofmt check**

Run: `gofmt -l .`
Expected: empty output (no files need reformatting).

- [ ] **Step 4: Build the binary**

Run: `go build -o /tmp/cc-crew ./cmd/cc-crew && /tmp/cc-crew up --help 2>&1 | grep -i max-mergers`
Expected: a line showing `--max-mergers` with its default.

- [ ] **Step 5: Verify init output**

Run: `/tmp/cc-crew up --help 2>&1 | head -40`
Expected: help text renders without error; no dangling placeholders.

- [ ] **Step 6: Manual integration (scratch repo — document only)**

Optional but recommended before a real rollout. Against a scratch repo with `cc-crew init`:

1. **Happy path** — open an issue → implementer opens PR → reviewer APPROVES → verify merger merges automatically.
2. **Out-of-date path** — land a commit on `main` after approval → verify merger calls update-branch and next tick merges.
3. **Conflict path** — create a PR with a deliberate conflict → verify resolver container runs, pushes, reviewer re-approves, merger merges.
4. **Blocked path** — create an unresolvable conflict (e.g., resolver prompt exits non-zero) → verify `claude-conflict-blocked` label and comment appear and no further ticks retry.

- [ ] **Step 7: Final commit (only if manual changes needed)**

No-op unless steps 1–6 surfaced issues. If a fix is required, `go test ./... && gofmt -w . && git add <files> && git commit`.

---

## File map (for orientation)

**Created:**

- `internal/scheduler/merger.go`
- `internal/scheduler/merger_test.go`
- `internal/scheduler/resolver.go`
- `internal/scheduler/resolver_test.go`
- `personas/resolver/CLAUDE.md`
- `personas/resolver/settings.json`

**Modified:**

- `internal/claim/claim.go` — `KindMerger`, `KindResolver`, `PathsFor`
- `internal/claim/claim_test.go`
- `internal/config/config.go` — 5 label fields, `MaxMergers`, `Validate`
- `internal/config/parse.go` — `--max-mergers` flag, `CC_MAX_MERGERS`
- `internal/config/config_test.go`, `internal/config/parse_test.go`
- `internal/github/client.go` — `MergePR`, `UpdateBranch`, `GetCheckRuns`, `CreateComment`, types
- `internal/github/gh.go` — corresponding implementations + `mergeable,mergeStateStatus` fields
- `internal/github/fake.go` — fake implementations, hooks, `Merged` flag
- `internal/github/types.go` — `Mergeable`, `MergeStateStatus`, `Merged`
- `internal/github/fake_test.go`, `internal/github/gh_test.go`
- `internal/scheduler/lifecycle.go` — `MergeLabel`, `AddressLabel`, `ResolveConflictLabel`, `ConflictBlockedLabel`, `dockerRunFn`, `successCleanupReviewer` with verdict flip, `Dispatch` routing, `kindName` cases
- `internal/scheduler/lifecycle_test.go`
- `internal/scheduler/scheduler.go` — `listCandidates` + `tryClaimOne` routing for new Kinds
- `internal/scheduler/scheduler_test.go`
- `internal/reset/reset.go` — new `Options` fields, reset of new labels + ref prefixes
- `internal/reset/reset_test.go`
- `cmd/cc-crew/up.go` — merger/resolver scheduler wire-up, SIGINT cleanup for resolver, reviewer Lifecycle MergeLabel/AddressLabel plumbing
- `cmd/cc-crew/init.go` — 5 new label specs
- `cmd/cc-crew/init_test.go`
- `cmd/cc-crew/reset.go` — plumb 5 new labels
- `scripts/cc-crew-run` — `CC_ROLE=resolver` branch
