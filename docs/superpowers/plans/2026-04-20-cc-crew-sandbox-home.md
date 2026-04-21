# cc-crew sandbox home directory isolation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Isolate `cc-crew sandbox` from host `~/.claude` by default with a per-repo persistent home directory; add `--use-host-claude` flag to opt back into sharing host config.

**Architecture:** Refactor `cmd/cc-crew/sandbox.go` into testable pure helpers (`sandboxHomeDir`, `buildSandboxRunArgs`, `parseSandboxFlags`) plus a thin orchestration function `runSandbox`. Always pass `--user $(id -u):$(id -g)` to `docker run`; bind-mount a persistent host dir at `/home/claude`; conditionally also mount host `~/.claude` at `/home/claude/.claude` when the flag is set. Image and dispatch path are not touched.

**Tech Stack:** Go (stdlib only — `flag`, `os`, `path/filepath`, `errors`, `io/fs`, `sort`, `fmt`), Docker CLI.

**Spec:** `docs/superpowers/specs/2026-04-20-cc-crew-sandbox-home-design.md`

**Pre-flight (every task):** Before any `git add` that touches Go files, run `gofmt -w .` from the repo root. It's a no-op when files are already formatted.

---

## File Structure

| File | Purpose | Status |
|------|---------|--------|
| `cmd/cc-crew/sandbox.go` | Orchestrates the sandbox run; new helpers + flag parsing live here | Modify |
| `cmd/cc-crew/sandbox_test.go` | Unit tests for the pure helpers | Create |
| `cmd/cc-crew/main.go` | Mention the new flag in `usage()` | Modify (1 line) |

All new helpers stay in `cmd/cc-crew/sandbox.go` — same package, same file. Splitting into a separate package is unnecessary at this size.

---

## Task 1: `sandboxHomeDir` — persistent host home with onboarding seed

**Files:**
- Modify: `cmd/cc-crew/sandbox.go` — add `sandboxHomeDir`
- Create: `cmd/cc-crew/sandbox_test.go` — add tests

- [ ] **Step 1: Write the failing tests**

Create `cmd/cc-crew/sandbox_test.go` with this content:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxHomeDir_CreatesAndSeeds(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	dir, err := sandboxHomeDir("acme-widget")
	if err != nil {
		t.Fatalf("sandboxHomeDir: %v", err)
	}
	want := filepath.Join(cache, "cc-crew", "sandbox-home", "acme-widget")
	if dir != want {
		t.Fatalf("got %q want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got %v", info.Mode())
	}
	seed, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	const wantBody = `{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}`
	if string(seed) != wantBody {
		t.Fatalf("seed mismatch:\n got %q\nwant %q", seed, wantBody)
	}
}

func TestSandboxHomeDir_DoesNotOverwriteExistingSeed(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	dir, err := sandboxHomeDir("acme-widget")
	if err != nil {
		t.Fatalf("first sandboxHomeDir: %v", err)
	}
	custom := []byte(`{"hasCompletedOnboarding":true,"theme":"light"}`)
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), custom, 0o644); err != nil {
		t.Fatalf("write custom seed: %v", err)
	}
	if _, err := sandboxHomeDir("acme-widget"); err != nil {
		t.Fatalf("second sandboxHomeDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if string(got) != string(custom) {
		t.Fatalf("seed was overwritten:\n got %q\nwant %q", got, custom)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail with "undefined: sandboxHomeDir"**

```bash
cd /home/zc/Work/cc-crew
go test ./cmd/cc-crew/ -run TestSandboxHomeDir -v
```

Expected: compile error mentioning `sandboxHomeDir` is undefined.

- [ ] **Step 3: Add the implementation to `cmd/cc-crew/sandbox.go`**

Add these imports if missing (the file already imports `fmt`, `os`, `path/filepath`):

```go
import (
	// ... existing imports ...
	"errors"
	"io/fs"
)
```

Add this function at the bottom of `sandbox.go`:

```go
// sandboxHomeDir returns the persistent host directory bind-mounted at
// /home/claude inside the sandbox container. The directory is created on
// first use and seeded with the onboarding-skip JSON so the in-container
// `claude` CLI does not prompt for setup. Subsequent calls reuse the existing
// directory and leave any existing seed file alone.
func sandboxHomeDir(repoName string) (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("user home dir: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "cc-crew", "sandbox-home", repoName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	seed := filepath.Join(dir, ".claude.json")
	if _, err := os.Stat(seed); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", seed, err)
		}
		const body = `{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}`
		if err := os.WriteFile(seed, []byte(body), 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", seed, err)
		}
	}
	return dir, nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
go test ./cmd/cc-crew/ -run TestSandboxHomeDir -v
```

Expected: `--- PASS: TestSandboxHomeDir_CreatesAndSeeds` and `--- PASS: TestSandboxHomeDir_DoesNotOverwriteExistingSeed`.

- [ ] **Step 5: Format and commit**

```bash
gofmt -w .
git add cmd/cc-crew/sandbox.go cmd/cc-crew/sandbox_test.go
git commit -m "Add sandboxHomeDir helper for cc-crew sandbox isolation"
```

---

## Task 2: `buildSandboxRunArgs` — pure docker-args constructor

**Files:**
- Modify: `cmd/cc-crew/sandbox.go` — add `sandboxOpts` type and `buildSandboxRunArgs`
- Modify: `cmd/cc-crew/sandbox_test.go` — add three tests

- [ ] **Step 1: Write the failing tests**

Append to `cmd/cc-crew/sandbox_test.go`:

```go
import (
	// at the top of the file, add to the existing import block:
	"reflect"
)

func TestBuildSandboxRunArgs_Default(t *testing.T) {
	args := buildSandboxRunArgs(sandboxOpts{
		name:        "ctr",
		image:       "img:tag",
		cwd:         "/workspace-host",
		sandboxHome: "/sbx-home",
		uid:         1234,
		gid:         5678,
		env:         map[string]string{"FOO": "bar"},
	})
	want := []string{
		"run", "-d", "--rm",
		"--name", "ctr",
		"--user", "1234:5678",
		"-v", "/workspace-host:/workspace",
		"-v", "/sbx-home:/home/claude",
		"-e", "FOO=bar",
		"img:tag",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v\nwant %v", args, want)
	}
}

func TestBuildSandboxRunArgs_UseHostClaude(t *testing.T) {
	args := buildSandboxRunArgs(sandboxOpts{
		name:          "ctr",
		image:         "img:tag",
		cwd:           "/workspace-host",
		sandboxHome:   "/sbx-home",
		hostClaudeDir: "/host/.claude",
		uid:           1234,
		gid:           5678,
	})
	// Mount order: parent (/home/claude) before nested (/home/claude/.claude).
	want := []string{
		"run", "-d", "--rm",
		"--name", "ctr",
		"--user", "1234:5678",
		"-v", "/workspace-host:/workspace",
		"-v", "/sbx-home:/home/claude",
		"-v", "/host/.claude:/home/claude/.claude",
		"img:tag",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v\nwant %v", args, want)
	}
}

func TestBuildSandboxRunArgs_EnvSortedAndEmptyFiltered(t *testing.T) {
	args := buildSandboxRunArgs(sandboxOpts{
		name:        "ctr",
		image:       "img",
		cwd:         "/cwd",
		sandboxHome: "/sbx",
		uid:         1, gid: 2,
		env: map[string]string{
			"ZED":   "z",
			"ALPHA": "a",
			"EMPTY": "",
		},
	})
	want := []string{
		"run", "-d", "--rm",
		"--name", "ctr",
		"--user", "1:2",
		"-v", "/cwd:/workspace",
		"-v", "/sbx:/home/claude",
		"-e", "ALPHA=a",
		"-e", "ZED=z",
		"img",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v\nwant %v", args, want)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail with "undefined: sandboxOpts" / "undefined: buildSandboxRunArgs"**

```bash
go test ./cmd/cc-crew/ -run TestBuildSandboxRunArgs -v
```

Expected: compile errors.

- [ ] **Step 3: Add `sandboxOpts` and `buildSandboxRunArgs` to `cmd/cc-crew/sandbox.go`**

Add `"sort"` to the import block. Then append:

```go
// sandboxOpts is the set of inputs needed to build the `docker run` argv for
// `cc-crew sandbox`. All fields are required except hostClaudeDir, which is
// empty unless the user passed --use-host-claude.
type sandboxOpts struct {
	name          string
	image         string
	cwd           string
	sandboxHome   string
	hostClaudeDir string
	uid, gid      int
	env           map[string]string
}

// buildSandboxRunArgs constructs the argv (excluding the `docker` binary) for
// the sandbox container. Pure: no I/O, no env reads. Mount order matters when
// hostClaudeDir is set — the parent (/home/claude) must come before the nested
// (/home/claude/.claude). Env vars are emitted in sorted key order; empty
// values are filtered.
func buildSandboxRunArgs(o sandboxOpts) []string {
	args := []string{
		"run", "-d", "--rm",
		"--name", o.name,
		"--user", fmt.Sprintf("%d:%d", o.uid, o.gid),
		"-v", o.cwd + ":/workspace",
		"-v", o.sandboxHome + ":/home/claude",
	}
	if o.hostClaudeDir != "" {
		args = append(args, "-v", o.hostClaudeDir+":/home/claude/.claude")
	}
	keys := make([]string, 0, len(o.env))
	for k := range o.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := o.env[k]
		if v == "" {
			continue
		}
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, o.image)
	return args
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
go test ./cmd/cc-crew/ -run TestBuildSandboxRunArgs -v
```

Expected: all three tests pass.

- [ ] **Step 5: Format and commit**

```bash
gofmt -w .
git add cmd/cc-crew/sandbox.go cmd/cc-crew/sandbox_test.go
git commit -m "Add buildSandboxRunArgs pure constructor for sandbox docker args"
```

---

## Task 3: `parseSandboxFlags` — `--use-host-claude` parsing

**Files:**
- Modify: `cmd/cc-crew/sandbox.go` — add `sandboxFlags` type and `parseSandboxFlags`
- Modify: `cmd/cc-crew/sandbox_test.go` — add tests

- [ ] **Step 1: Write the failing tests**

Append to `cmd/cc-crew/sandbox_test.go`:

```go
func TestParseSandboxFlags_Default(t *testing.T) {
	f, err := parseSandboxFlags(nil)
	if err != nil {
		t.Fatalf("parseSandboxFlags: %v", err)
	}
	if f.useHostClaude {
		t.Fatalf("default should be useHostClaude=false, got true")
	}
}

func TestParseSandboxFlags_UseHostClaude(t *testing.T) {
	f, err := parseSandboxFlags([]string{"--use-host-claude"})
	if err != nil {
		t.Fatalf("parseSandboxFlags: %v", err)
	}
	if !f.useHostClaude {
		t.Fatalf("expected useHostClaude=true")
	}
}

func TestParseSandboxFlags_UnknownFlagErrors(t *testing.T) {
	if _, err := parseSandboxFlags([]string{"--bogus"}); err == nil {
		t.Fatalf("expected error for unknown flag, got nil")
	}
}

func TestParseSandboxFlags_PositionalArgsError(t *testing.T) {
	if _, err := parseSandboxFlags([]string{"extra"}); err == nil {
		t.Fatalf("expected error for positional arg, got nil")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail with "undefined: parseSandboxFlags"**

```bash
go test ./cmd/cc-crew/ -run TestParseSandboxFlags -v
```

Expected: compile errors.

- [ ] **Step 3: Add `sandboxFlags` and `parseSandboxFlags` to `cmd/cc-crew/sandbox.go`**

Add `"flag"` and `"io"` to the import block. Then append:

```go
// sandboxFlags is the parsed `cc-crew sandbox` CLI flag set.
type sandboxFlags struct {
	useHostClaude bool
}

// parseSandboxFlags parses CLI args for `cc-crew sandbox`. Returns an error on
// unknown flags or unexpected positional arguments. The error from the
// underlying FlagSet already includes a usage hint; the caller is expected to
// surface it to stderr verbatim.
func parseSandboxFlags(args []string) (sandboxFlags, error) {
	fs := flag.NewFlagSet("cc-crew sandbox", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // silence the FlagSet's own auto-print; caller decides what to show
	var f sandboxFlags
	fs.BoolVar(&f.useHostClaude, "use-host-claude", false,
		"Bind-mount host ~/.claude into the sandbox so plugins, skills, MCP servers, and history are shared with host Claude Code.")
	if err := fs.Parse(args); err != nil {
		return sandboxFlags{}, err
	}
	if fs.NArg() > 0 {
		return sandboxFlags{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return f, nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
go test ./cmd/cc-crew/ -run TestParseSandboxFlags -v
```

Expected: all four tests pass.

- [ ] **Step 5: Format and commit**

```bash
gofmt -w .
git add cmd/cc-crew/sandbox.go cmd/cc-crew/sandbox_test.go
git commit -m "Add parseSandboxFlags for --use-host-claude opt-in"
```

---

## Task 4: Wire `runSandbox` to use new helpers + update usage

**Files:**
- Modify: `cmd/cc-crew/sandbox.go` — rewrite `runSandbox`
- Modify: `cmd/cc-crew/main.go` — extend `usage()` text

This task has no new unit tests — `runSandbox` is exercised end-to-end by the
manual smoke test in step 5 (it shells out to `docker run`/`docker exec`/`docker
stop`, which is unsuitable for hermetic unit tests). The pure helpers it
composes are already covered by Tasks 1-3.

- [ ] **Step 1: Replace `runSandbox` in `cmd/cc-crew/sandbox.go`**

Replace the existing `runSandbox` function with:

```go
func runSandbox(args []string) int {
	flags, err := parseSandboxFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: %v\n\n", err)
		fmt.Fprintln(os.Stderr, sandboxUsage)
		return 2
	}

	repoName, err := gitRepoName()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: %v\n", err)
		return 1
	}
	name := fmt.Sprintf("cc-crew-sandbox-%s-%d", repoName, time.Now().Unix())

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: getwd: %v\n", err)
		return 1
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: home dir: %v\n", err)
		return 1
	}

	sandboxHome, err := sandboxHomeDir(repoName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: prepare sandbox home: %v\n", err)
		return 1
	}

	ghToken := os.Getenv("GH_TOKEN_IMPLEMENTER")
	if ghToken == "" {
		ghToken = os.Getenv("GH_TOKEN")
	}
	gitName := os.Getenv("IMPLEMENTER_GIT_NAME")
	gitEmail := os.Getenv("IMPLEMENTER_GIT_EMAIL")

	opts := sandboxOpts{
		name:        name,
		image:       sandboxImage,
		cwd:         cwd,
		sandboxHome: sandboxHome,
		uid:         os.Getuid(),
		gid:         os.Getgid(),
		env: map[string]string{
			"CLAUDE_CODE_OAUTH_TOKEN": os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"),
			"ANTHROPIC_API_KEY":       os.Getenv("ANTHROPIC_API_KEY"),
			"GH_TOKEN":                ghToken,
			"GIT_AUTHOR_NAME":         gitName,
			"GIT_COMMITTER_NAME":      gitName,
			"GIT_AUTHOR_EMAIL":        gitEmail,
			"GIT_COMMITTER_EMAIL":     gitEmail,
		},
	}
	if flags.useHostClaude {
		opts.hostClaudeDir = filepath.Join(home, ".claude")
	}

	start := exec.Command("docker", buildSandboxRunArgs(opts)...)
	start.Stderr = os.Stderr
	if err := start.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: docker run failed: %v\n", err)
		return 1
	}

	execCmd := exec.Command("docker", "exec", "-it", name, "claude", "--dangerously-skip-permissions")
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execErr := execCmd.Run()

	stop := exec.Command("docker", "stop", name)
	stop.Stderr = os.Stderr
	_ = stop.Run()

	if execErr != nil {
		return 1
	}
	return 0
}

const sandboxUsage = `Usage: cc-crew sandbox [flags]

Run an interactive Claude Code session in a per-repo sandbox container.

Flags:
  --use-host-claude   Bind-mount your host ~/.claude into the sandbox so
                      plugins, skills, MCP servers, and history are shared
                      with host Claude Code. Default: isolated sandbox with
                      its own persistent ~/.cache/cc-crew/sandbox-home/<repo>.`
```

Delete the old `appendEnv` helper if it is no longer referenced. (Search the
package for `appendEnv` before removing — it should now be unused.)

- [ ] **Step 2: Update `cmd/cc-crew/main.go` `usage()` text**

In the `usage()` function string, change the sandbox line to:

```
  cc-crew sandbox  Launch an interactive Claude Code session (use --use-host-claude to share host ~/.claude)
```

- [ ] **Step 3: Run all tests + go vet + go build**

```bash
go vet ./...
go build ./...
go test ./cmd/cc-crew/ -v
```

Expected: vet clean, build clean, all tests pass (the existing `init_test.go`
tests should still pass alongside the new sandbox tests).

- [ ] **Step 4: Manual smoke test — default isolated mode**

In a separate shell, with the sandbox image already built locally (or pull
ghcr.io/charleszheng44/cc-crew-sandbox if you have access):

```bash
go install ./cmd/cc-crew
cd <some-git-repo>
cc-crew sandbox
# Inside the sandbox:
ls -la ~/        # ~ should be /home/claude, owned by your host UID
cat ~/.claude.json   # onboarding seed should be present
ls ~/.claude/    # should be empty or only contain whatever Claude wrote on first run
/plugin          # should NOT error with "Permission denied"; marketplaces should clone
exit
```

Verify on host: `ls -la ~/.cache/cc-crew/sandbox-home/<repo>/` shows the dir,
host-UID-owned, with a `.claude/plugins/` subdirectory created by the `/plugin`
command.

- [ ] **Step 5: Manual smoke test — opt-in mode**

```bash
cd <some-git-repo>
cc-crew sandbox --use-host-claude
# Inside the sandbox:
ls ~/.claude/plugins/    # should show host plugins
exit
```

Verify on host: any plugin marketplace cloned during the session is now
visible at `~/.claude/plugins/marketplaces/` on the host.

- [ ] **Step 6: Format and commit**

```bash
gofmt -w .
git add cmd/cc-crew/sandbox.go cmd/cc-crew/main.go
git commit -m "Wire cc-crew sandbox to use isolated host dir; add --use-host-claude flag"
```

---

## Self-Review (run before handoff)

**Spec coverage:** Skim the spec section by section and confirm each is implemented.

| Spec section | Implemented in |
|---|---|
| Default isolated behavior | Task 4 (uses `sandboxHomeDir` always, only mounts host `.claude` when flag set) |
| `--use-host-claude` opt-in | Task 3 (parsing) + Task 4 (wiring) |
| Per-repo persistent home under `~/.cache/cc-crew/sandbox-home/<repo>` | Task 1 |
| Onboarding seed at `<sandbox-home>/.claude.json` | Task 1 |
| `--user host-uid:host-gid` always passed | Task 2 (always emitted) + Task 4 (uid/gid wired from `os.Getuid/Getgid`) |
| Mount order parent-before-nested | Task 2 (constructor enforces order; test asserts it) |
| Env vars sorted, empty filtered | Task 2 (`TestBuildSandboxRunArgs_EnvSortedAndEmptyFiltered`) |
| `flag.FlagSet`-based CLI parsing | Task 3 |
| Help text mentions the flag | Task 4 (`sandboxUsage` const + `main.go` usage line) |
| Error handling: `sandboxHomeDir` failure → exit 1 with prefix | Task 4 (`fmt.Fprintf(os.Stderr, "cc-crew sandbox: prepare sandbox home: %v\n", err)`) |
| Error handling: bad flags → exit 2 with usage | Task 4 (returns 2, prints `sandboxUsage`) |
| No Dockerfile change | (none of the tasks edit `Dockerfile.ubuntu`) |
| No dispatch / `cc-crew-run` change | (none of the tasks edit `internal/scheduler/lifecycle.go` or `scripts/cc-crew-run`) |
| Hermetic tests (no Docker) | Task 1-3 use only stdlib + `t.TempDir()` + `t.Setenv` |

**Type consistency check:**
- `sandboxOpts` field names (`name`, `image`, `cwd`, `sandboxHome`, `hostClaudeDir`, `uid`, `gid`, `env`) match between Task 2 (definition + tests) and Task 4 (consumer).
- `sandboxFlags.useHostClaude` matches between Task 3 (definition + tests) and Task 4 (consumer).
- Mount paths (`/workspace`, `/home/claude`, `/home/claude/.claude`) match between spec and Task 2.

**Placeholder scan:** No `TBD`, `TODO`, "appropriate error handling", or hand-wavy steps — every code-changing step contains the actual code.
