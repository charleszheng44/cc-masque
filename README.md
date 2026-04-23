# cc-crew

Run multiple [Claude Code](https://docs.anthropic.com/en/docs/claude-code) sessions locally as distinct GitHub personas (implementer, reviewer, merger, resolver) that cooperate on your repo through GitHub labels. Label an issue `claude-task` — the implementer picks it up, opens a PR, the reviewer reviews it, the implementer addresses feedback, and (optionally) the merger merges it. Everything runs in local Docker containers with scoped tokens.

![cc-crew architecture](docs/images/cc-crew.png)

## How it works

`cc-crew up` polls your repo on a fixed interval. Each persona has a queue label and a lock label:

| Persona | Queue label | Lock label | Done label | Trigger |
|---|---|---|---|---|
| Implementer | `claude-task` | `claude-processing` | `claude-done` | Issue labeled `claude-task` |
| Reviewer | `claude-review` | `claude-reviewing` | `claude-reviewed` | PR labeled `claude-review` (auto-applied by default) |
| Addresser | `claude-address` | `claude-addressing` | `claude-addressed` | Reviewer requested changes (auto-detected) |
| Merger | `claude-merge` | `claude-merging` | — | PR labeled `claude-merge` |
| Resolver | `claude-resolve-conflict` | `claude-resolving` | — | Merger hits a conflict |

On each tick the orchestrator claims work (adds the lock label), checks out a git worktree, launches a container running `claude -p` with the persona's `CLAUDE.md` as memory, and removes the lock when the container exits.

---

## Prerequisites

- Docker (for the task containers)
- Go 1.22+ (to build the `cc-crew` binary)
- `git` and the `gh` CLI on the host
- A GitHub repo you own (or can label on)
- An Anthropic account: a **Max/Pro subscription** (recommended — generates an OAuth token) or an **API key** (billed per-token)

---

## Setup

### 1. Clone and build the orchestrator

```bash
git clone https://github.com/charleszheng44/cc-crew.git
cd cc-crew
make build          # produces ./cc-crew
```

### 2. Pull (or build) the task image

The orchestrator dispatches a prebuilt image by default: `ghcr.io/charleszheng44/cc-crew:latest`. To use it, just let Docker pull it on first `up`. To build locally:

```bash
make docker-build              # builds ghcr.io/charleszheng44/cc-crew:latest
make docker-build-sandbox      # optional — Ubuntu-based image for `cc-crew sandbox`
```

### 3. Get your Claude credential

**Max/Pro (recommended)** — on any machine where you're logged into Claude Code:

```bash
claude setup-token
```

Export the resulting token:

```bash
export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-...
```

**API key instead** — `export ANTHROPIC_API_KEY=sk-ant-...`. This bills per token against API credits and does **not** use your Max/Pro subscription.

### 4. Get GitHub tokens (one per persona)

Each persona operates as its own GitHub account, so create **one fine-grained token per persona** (implementer and reviewer — plus merger, which reuses the reviewer token). This keeps commits, PRs, and reviews visibly attributed to different identities.

For each token:

1. Go to https://github.com/settings/personal-access-tokens/new
2. **Resource owner:** your own username (not an org) — scopes the token to personal repos only
3. **Repository access:** *Only select repositories* — pick the target repo
4. **Repository permissions:** Contents, Pull requests, Issues = **Read and write**. Workflows = **Read and write** if you expect PRs to touch `.github/workflows/`.
5. Leave **Account permissions** at "No access"

Avoid classic PATs — they're account-wide and include every org you belong to.

Then:

```bash
export GH_TOKEN_IMPLEMENTER=github_pat_...
export GH_TOKEN_REVIEWER=github_pat_...
export GH_TOKEN=$GH_TOKEN_IMPLEMENTER         # used for the orchestrator's own API calls

export IMPLEMENTER_GIT_NAME="implementer-bot"
export IMPLEMENTER_GIT_EMAIL="impl@example.com"
export REVIEWER_GIT_NAME="reviewer-bot"
export REVIEWER_GIT_EMAIL="rev@example.com"
```

### 5. Create the lifecycle labels on the target repo

From inside a clone of your target repo, run `cc-crew init` **once per repo** to create the 15 labels cc-crew uses. It's idempotent — already-present labels are reported and skipped.

```bash
cd /path/to/your/repo
/path/to/cc-crew/cc-crew init
```

You should see `created:` lines for each label (or `exists:` if rerunning).

---

## Usage

### Start the orchestrator

From inside a clone of the target repo:

```bash
cc-crew up
```

This runs in the foreground and logs to stderr. Ctrl-C to stop — any in-flight containers are killed and their locks released so the next start can pick work back up.

Common flags:

```bash
cc-crew up --max-implementers 3 --max-reviewers 2 --max-mergers 2
cc-crew up --auto-review=false          # don't auto-apply claude-review to new PRs
cc-crew up --continuous=false           # disable address+re-review loops
cc-crew up --poll-seconds 30            # faster polling (default: 60s)
cc-crew up --base-branch develop        # default: repo's GitHub default branch
cc-crew up --model claude-sonnet-4-6    # default: claude-opus-4-7
```

See `cc-crew up --help` for the full list.

### Dispatch work: label an issue

Open an issue describing the change you want. Add the `claude-task` label. Within one poll interval the implementer will:

1. Take the lock (`claude-processing`)
2. Check out a worktree on `claude/issue-<N>`
3. Launch a container running Claude Code with the implementer persona
4. When Claude finishes: commit, push, open a PR titled `Resolve #<N>: <title>`
5. Drop the lock; apply `claude-done` to the issue; apply `claude-review` to the new PR (when `--auto-review` is on)

The reviewer then picks up the PR, posts a single review. If changes are requested, cc-crew auto-labels it `claude-address` and the implementer returns to the same branch to respond.

### Merging

PRs don't auto-merge. Approve one and add the `claude-merge` label to hand it to the merger. If the merger can't fast-forward, it applies `claude-resolve-conflict` and the resolver rebases + force-pushes; on success the PR goes back into the merge queue.

### Check what's running

```bash
cc-crew status
```

Stateless snapshot of: pending issues/PRs per queue, active containers, worktrees, and recent lifecycle label counts.

### Reset (emergency cleanup)

If the orchestrator crashed mid-dispatch, or you want a clean slate:

```bash
cc-crew reset          # dry run — prints every container, worktree, ref, and label it would touch
cc-crew reset --yes    # actually apply: kills containers, removes worktrees, strips lock labels, requeues
```

### Teardown

Stopping the orchestrator (`Ctrl-C`) already cleans up containers and lock labels. If you want to also remove the labels themselves from the remote, do that manually via `gh label delete`.

---

## Interactive mode: `cc-crew sandbox`

For ad-hoc exploration or manual prompting — not driven by GitHub labels — run a per-repo interactive Claude session in a container:

```bash
cc-crew sandbox                          # isolated sandbox with its own ~/.cache/cc-crew/sandbox-home/<repo>
cc-crew sandbox --use-host-claude        # share your host ~/.claude (plugins, skills, MCP, history)
```

The sandbox bind-mounts your current directory at `/workspace` and runs as your host UID/GID so any files Claude writes stay owned by you. Reads the same `CLAUDE_CODE_OAUTH_TOKEN` / `GH_TOKEN_IMPLEMENTER` env as `cc-crew up`.

---

## Personas (customising behaviour)

Each persona is a directory under `personas/<role>/` containing a `CLAUDE.md` (loaded as user-level memory every invocation) and a `settings.json` (scoped permissions). The docker image bakes these in, so `cc-crew up` needs no persona mounts — edit the files and rebuild the image to change behaviour, or mount an override at runtime.

- `personas/implementer/` — resolves issues and opens PRs
- `personas/reviewer/` — reviews PRs; posts exactly one review
- `personas/resolver/` — rebases and force-pushes-with-lease on conflicts

---

## Environment variable reference

| Variable | Required when | Purpose |
|---|---|---|
| `CLAUDE_CODE_OAUTH_TOKEN` | Always (or `ANTHROPIC_API_KEY`) | Claude auth |
| `ANTHROPIC_API_KEY` | Alternative to OAuth token | Claude auth (per-token billing) |
| `GH_TOKEN` | Always | Orchestrator's own GitHub API calls |
| `GH_TOKEN_IMPLEMENTER` | `--max-implementers > 0` | Implementer's commits / PRs |
| `GH_TOKEN_REVIEWER` | `--max-reviewers > 0` or `--max-mergers > 0` | Reviewer's reviews; merger's merges |
| `IMPLEMENTER_GIT_NAME` / `..._EMAIL` | Implementer enabled | Commit identity |
| `REVIEWER_GIT_NAME` / `..._EMAIL` | Reviewer enabled | Commit identity |

All CLI flags also accept equivalent `CC_*` env vars (e.g. `CC_MAX_IMPLEMENTERS=3`, `CC_POLL_SECONDS=30`, `CC_MODEL=claude-sonnet-4-6`). Flag > env > default.

---

## Troubleshooting

- **`gh auth status` fails inside a container** — the orchestrator forwards `GH_TOKEN` per-persona; if you're `docker exec`ing into an image manually, export it yourself.
- **Claude hangs in `-p` mode** — it likely hit a permission prompt. Either use `--dangerously-skip-permissions` (default for dispatched tasks) or add the command to the persona's `settings.json` allow list.
- **Issue got labeled `claude-failed`** — cc-crew quarantines after 3 consecutive dispatch failures (configurable via `--quarantine-threshold`). Remove the label to re-enable dispatch.
- **Stale locks after a crash** — `cc-crew reset --yes` clears them; alternatively, `up` auto-reclaims locks older than `--reclaim-seconds` (30m default).

Full design notes live under `docs/superpowers/specs/`.
