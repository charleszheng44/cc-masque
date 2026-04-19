# cc-crew sandbox — Design

**Date:** 2026-04-18

## Summary

Add a `cc-crew sandbox` subcommand that launches a fresh Ubuntu Docker container with the current directory bind-mounted, then execs an interactive Claude Code session (`--dangerously-skip-permissions`) inside it. The container acts as an OS-level sandbox so Claude cannot affect the host filesystem or system packages.

## Motivation

The existing `cc-crew up` workflow runs Claude non-interactively inside the lightweight Alpine image, targeted at unattended orchestration tasks. Users also want a way to run an interactive Claude Code session in a sandboxed environment — particularly one based on Ubuntu (with `apt`) so Claude can install packages during the session without touching the host OS.

## New files and changes

| File | Change |
|------|--------|
| `Dockerfile.ubuntu` | New Ubuntu-based sandbox image |
| `cmd/cc-crew/sandbox.go` | New `runSandbox()` implementation |
| `cmd/cc-crew/main.go` | Wire `sandbox` subcommand; update `usage()` |

## `Dockerfile.ubuntu`

- Base image: `ubuntu:24.04`
- Installs: `git`, `curl`, `gh` (GitHub CLI via official apt source), Node.js LTS (via NodeSource), `@anthropic-ai/claude-code` (via npm)
- Pre-seeds `/root/.claude.json` with `{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}` so the first run skips the interactive onboarding wizard
- No `cc-crew-run` entrypoint — this image is not used by the orchestrator
- Default `CMD`: `["tail", "-f", "/dev/null"]` — keeps the container alive for `docker exec`
- `WORKDIR /workspace`
- Published tag: `ghcr.io/charleszheng44/cc-crew:ubuntu`

## `cmd/cc-crew/sandbox.go`

`runSandbox()` flow:

1. **Repo name** — run `git rev-parse --show-toplevel`, take `filepath.Base()` of the result, sanitize with `safeName()` (reused from `lifecycle.go`) → `<gitrepo>`
2. **Container name** — `cc-crew-sandbox-<gitrepo>-<unix-timestamp>` (unique per invocation; human-readable in `docker ps`)
3. **Start container** — `docker run -d --rm --name <name> -v <cwd>:/workspace [-e CLAUDE_CODE_OAUTH_TOKEN=<token>] ghcr.io/charleszheng44/cc-crew:ubuntu`
   - `--rm` so the container is automatically removed after it stops
   - `CLAUDE_CODE_OAUTH_TOKEN` is forwarded from the host env if set; omitted if unset (Claude Code will prompt for interactive login)
   - No other env vars are forwarded
4. **Exec session** — `docker exec -it <name> claude --dangerously-skip-permissions`
   - `os.Stdin`, `os.Stdout`, `os.Stderr` wired directly so Claude Code gets a proper in-container PTY
5. **Stop container** — `docker stop <name>` after exec exits (the `--rm` flag on `docker run` then removes it)
6. **Error handling** — if `docker run` fails, print error and return non-zero; if `docker exec` fails, still attempt `docker stop` before returning

### Container name format

```
cc-crew-sandbox-<gitrepo>-<unix-timestamp>
```

Example: `cc-crew-sandbox-cc-masque-1745001234`

Each invocation creates a fresh container. No re-attachment across sessions; packages installed in one session do not persist to the next.

## `cmd/cc-crew/main.go`

Add `case "sandbox"` dispatch and extend `usage()`:

```
cc-crew sandbox  Launch an interactive Claude Code session in a sandboxed Ubuntu container
```

## Non-goals

- No flags (`--model`, `--image`, `--dir`) — always uses defaults
- No container reuse / re-attachment across sessions
- No `.git` mount — sandbox works on the live working directory, not a worktree
- Not used by the orchestrator (`up`) — the existing Alpine image remains for orchestration
