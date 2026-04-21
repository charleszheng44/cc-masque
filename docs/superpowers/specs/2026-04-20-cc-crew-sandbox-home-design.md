# cc-crew sandbox — Home directory isolation

**Date:** 2026-04-20

## Summary

Make `cc-crew sandbox` isolated from the host's `~/.claude` by default. Each
sandbox invocation gets a persistent per-repo home directory under
`~/.cache/cc-crew/sandbox-home/<repo>/` that is bind-mounted at `/home/claude`,
and the container runs as `--user $(id -u):$(id -g)` so writes inside that home
are owned by the host user. A new opt-in flag, `--use-host-claude`, restores the
current behavior of bind-mounting host `~/.claude` into the sandbox.

## Motivation

Two problems with today's `cc-crew sandbox`:

1. **`/plugin` (and other writes under `/home/claude`) fail with `EACCES`** when
   the host UID does not match the image's baked-in `claude` user (UID 1000).
   The container runs without `--user`, so the running EUID is 1000; the
   bind-mounted host `~/.claude` is owned by the host UID. Mismatched UIDs →
   permission denied on any new file or directory under the bind mount.
2. **The "sandbox" silently shares state with host Claude Code.** Plugins,
   skills, MCP servers, and chat history all live in `~/.claude` and are
   read-write inside the sandbox. That defeats much of the point of an isolated
   container — a misbehaving plugin or session installed inside the sandbox
   leaks to the host.

The dispatch path (implementer / reviewer in `internal/scheduler/lifecycle.go`)
already solved the UID problem in issue #41 by creating an empty
`<worktree>/.cc-home` per task, bind-mounting it as `/home/claude`, and passing
`--user host-uid:host-gid`. The sandbox path never adopted that pattern. This
design unifies the two while adding an explicit opt-in for the "share host
config" mode that some users want.

## Non-goals

- No change to the dispatch path (`internal/scheduler/lifecycle.go`,
  `scripts/cc-crew-run`).
- No change to `Dockerfile` or `Dockerfile.ubuntu`.
- No `--isolated` flag, no read-only mode (`--use-host-claude=ro`), no
  three-way enum. YAGNI — add later if requested.
- No migration handling for users who have an existing
  `~/.cache/cc-crew/sandbox-home/<repo>/` from a prior version. The directory
  simply starts populating fresh on first run after upgrade.

## New files and changes

| File | Change |
|------|--------|
| `cmd/cc-crew/sandbox.go` | Refactor `runSandbox`; extract `buildSandboxRunArgs`, add `sandboxHomeDir`, parse `--use-host-claude` flag |
| `cmd/cc-crew/sandbox_test.go` | New — unit tests for the extracted helpers |
| `cmd/cc-crew/main.go` | Update `usage()` to mention the new flag |

## Behavior

### Default (`cc-crew sandbox`)

- A persistent host-side directory is created at
  `${XDG_CACHE_HOME:-$HOME/.cache}/cc-crew/sandbox-home/<repo>/` with mode
  `0o755` (using `os.MkdirAll`).
- That directory is bind-mounted at `/home/claude` inside the container,
  completely covering the image's baked-in `/home/claude`.
- The container runs as `--user $(id -u):$(id -g)` so anything written under
  `/home/claude` is host-UID-owned on disk.
- Host `~/.claude` is **not** mounted. Plugins, skills, MCP servers, and chat
  history from host Claude Code are not visible inside the sandbox.
- Per-repo persistence: state from a previous sandbox session in the same repo
  (installed plugins, command history, etc.) carries over. State does not cross
  repo boundaries.

### Opt-in (`cc-crew sandbox --use-host-claude`)

- Same as default, plus an additional bind-mount: host `~/.claude` →
  `/home/claude/.claude` (read-write).
- The persistent sandbox-home dir is still mounted at `/home/claude` (so the
  onboarding seed lives there), but the `.claude` subdirectory is overridden by
  the host mount.
- Plugin installs, MCP server changes, etc. inside the sandbox write back to
  the host `~/.claude`.

### Onboarding seed

The image bakes `/home/claude/.claude.json` with the onboarding-skip JSON
(`Dockerfile.ubuntu:28-30`). Once we mount our own dir over `/home/claude`,
that file is hidden. `sandboxHomeDir` writes the same JSON to
`<sandbox-home>/.claude.json` on first run if it does not already exist.
Subsequent runs leave it alone.

## Code structure

### `sandboxHomeDir`

```go
func sandboxHomeDir(repoName string) (string, error)
```

- Resolves `${XDG_CACHE_HOME:-$HOME/.cache}/cc-crew/sandbox-home/<repoName>`.
- `os.MkdirAll(dir, 0o755)`.
- Stats `<dir>/.claude.json`; if it does not exist, writes the onboarding-skip
  seed (same content as `Dockerfile.ubuntu:28-30`).
- Returns the absolute path of the dir, or an error.

### `sandboxOpts` and `buildSandboxRunArgs`

Pure function (no I/O), mirroring `internal/docker.BuildRunArgs`:

```go
type sandboxOpts struct {
    name          string
    image         string
    cwd           string
    sandboxHome   string            // host path bind-mounted at /home/claude
    hostClaudeDir string            // empty unless --use-host-claude
    uid, gid      int
    env           map[string]string // already filtered for empty values
}

func buildSandboxRunArgs(o sandboxOpts) []string
```

Returns the full argv after the `docker` binary:
`["run", "-d", "--rm", "--name", <name>, "--user", "U:G", "-v", ..., "-e", ..., <image>]`.

Mount order matters when `--use-host-claude` is set — the parent
(`sandboxHome:/home/claude`) must come before the nested
(`hostClaudeDir:/home/claude/.claude`). Env vars are emitted in sorted order so
the argv is stable for tests.

### `runSandbox`

1. Parse args with a `flag.FlagSet`. Define `--use-host-claude` (bool, default
   false). On error, the `FlagSet` prints usage; return exit code 2.
2. `gitRepoName()` → `<repo>`. (Unchanged.)
3. `sandboxHomeDir(<repo>)` → `<sandboxHome>`. On error, print
   `cc-crew sandbox: prepare sandbox home: <err>` to stderr and return 1.
4. Build the `sandboxOpts`:
   - `cwd` from `os.Getwd()` (existing).
   - `hostClaudeDir = filepath.Join(home, ".claude")` only when the flag is
     set; otherwise empty.
   - `uid, gid = os.Getuid(), os.Getgid()`.
   - `env` populated with the same keys as today: `CLAUDE_CODE_OAUTH_TOKEN`,
     `ANTHROPIC_API_KEY`, `GH_TOKEN` (with the `GH_TOKEN_IMPLEMENTER` fallback),
     `GIT_AUTHOR_NAME` / `GIT_COMMITTER_NAME`, `GIT_AUTHOR_EMAIL` /
     `GIT_COMMITTER_EMAIL`. Empty values are filtered.
5. `docker run` with `buildSandboxRunArgs(opts)`. Then `docker exec -it
   <name> claude --dangerously-skip-permissions`. Then `docker stop <name>`.
   This sequence is unchanged from today.

### CLI

```
cc-crew sandbox [flags]

Run an interactive Claude Code session in a per-repo sandbox container.

Flags:
  --use-host-claude   Bind-mount your host ~/.claude into the sandbox so
                      plugins, skills, MCP servers, and history are shared
                      with host Claude Code. Default: isolated sandbox with
                      its own persistent ~/.cache/cc-crew/sandbox-home/<repo>.
```

## Testing

`cmd/cc-crew/sandbox_test.go`:

- `TestBuildSandboxRunArgs_Default` — no `--use-host-claude`: argv contains
  `--user U:G`, exactly one `-v <sandboxHome>:/home/claude`, and no
  `-v <host>/.claude:/home/claude/.claude`.
- `TestBuildSandboxRunArgs_UseHostClaude` — both mounts present, with the
  parent (`sandboxHome:/home/claude`) emitted before the nested
  (`hostClaude:/home/claude/.claude`).
- `TestBuildSandboxRunArgs_EnvSorted` — env vars emitted in sorted key order.
- `TestSandboxHomeDir_CreatesAndSeeds` — point `XDG_CACHE_HOME` at a
  `t.TempDir()`, call `sandboxHomeDir("foo")`, assert dir exists and
  `.claude.json` contains the seed body.
- `TestSandboxHomeDir_DoesNotOverwriteExistingSeed` — pre-create
  `.claude.json` with custom content, call `sandboxHomeDir` again, assert the
  file is unchanged.
- `TestParseSandboxFlags` — `[]` parses to default opts; `["--use-host-claude"]`
  sets the flag; `["--bogus"]` returns an error.

No integration test that actually launches Docker — keeps the suite hermetic.
The existing `internal/docker/docker_test.go` already covers the lower-level
arg-construction patterns.

## Error handling

- `sandboxHomeDir` mkdir or seed-write failure → print
  `cc-crew sandbox: prepare sandbox home: <err>` to stderr; exit 1.
- `flag.Parse` failure → `FlagSet`'s default behavior (writes usage to stderr);
  exit 2.
- All existing failure paths (`docker run`, `docker exec`, `gitRepoName`,
  `os.UserHomeDir`) keep their current behavior and exit codes.
- No retry loops, no chmod fallbacks. If Docker fails, surface the error.

## Backwards compatibility

- Users running `cc-crew sandbox` after the upgrade get the new isolated
  behavior with no flag changes. Their host `~/.claude` is no longer mounted.
- Users who depended on host-mounted plugins / skills / history can re-enable
  by adding `--use-host-claude`. Surface this in the release notes / changelog.
- The new persistent dir is created lazily on first run; no manual setup
  needed.
- No on-disk format changes inside the persistent dir — Claude Code populates
  it the same way it would populate any `~/.claude`.
