# cc-crew orchestrator — design

Status: approved for planning
Date: 2026-04-16
Supersedes: the inline `spec.md` thoughts that seeded this work

## 1. Overview

cc-crew turns GitHub labels into a local work queue. A single Go binary (`cc-crew`) watches a repo, atomically claims labeled issues and PRs via git refs, and spawns one-shot Docker containers to process each item. Two personas are supported:

- **Implementer** — picks up issues labeled `claude-task`, implements the change, opens a PR.
- **Reviewer** — picks up PRs labeled `claude-review`, posts a review.

The project extends the existing cc-crew repo (to be renamed to cc-crew), which already ships a persona-based Docker image.

## 2. Goals

- Trigger Claude Code by labeling GitHub issues or PRs on the target repo.
- Single local orchestrator process; no public infrastructure, outbound network only.
- Per-persona concurrency caps (default 3 implementers, 2 reviewers), configurable.
- Polling-only event source (60s default tick), no webhook/smee relay.
- Atomic claim via git refs, so a second orchestrator on a second machine stays safe.
- Stale-claim reclaim after a configurable timeout, so a crashed orchestrator does not block work indefinitely.

## 3. Non-goals (v1)

- Egress network isolation. Accepted risk: with `--dangerously-skip-permissions`, a prompt-injection payload in an issue body could exfiltrate the persona's GitHub token. Token is fine-grained per the README, so blast radius is the persona's permitted repos, not the whole account. An HTTP forward-proxy design with a host allowlist is documented as a known follow-up.
- Cross-repo orchestration. Run one `cc-crew` per repo.
- Priority queues. FIFO by issue/PR number only.
- Daemon mode / `cc-crew down`. Only `cc-crew up` (foreground) and `cc-crew status` ship in v1.
- Sub-dispatching one big issue across multiple containers. If Claude needs to split the work, it uses its own Agent tool inside the single task container.
- Inline-line PR review comments. Reviewer posts a single top-level review body.
- Webhook / smee push. Polling only.
- Priority between implementer and reviewer semaphores. Independent caps.

## 4. Architecture

```
 Host (where the user runs `cc-crew up`)
 ┌──────────────────────────────────────────────────────────┐
 │  Orchestrator (Go binary, single process)                │
 │  ┌────────────────────────────────────────────────────┐  │
 │  │ Poller (60s tick)                                  │  │
 │  │ Claim layer  (GitHub git-ref atomic create)        │  │
 │  │ Reclaim sweeper (stale lock GC)                    │  │
 │  │ Scheduler  (per-persona semaphores)                │  │
 │  │ Runner     (docker run --rm per task)              │  │
 │  └────────────────────────────────────────────────────┘  │
 │          spawns per task                                 │
 │                 │                                        │
 │                 ▼                                        │
 │  ┌──────────────────┐   ┌──────────────────┐             │
 │  │ Implementer ctr  │   │ Reviewer ctr     │             │
 │  │  cc-crew image   │   │  cc-crew image   │             │
 │  │ --dangerously-…  │   │ scoped perms     │             │
 │  └──────────────────┘   └──────────────────┘             │
 └──────────────────────────────────────────────────────────┘
                                │
                                ▼
                            GitHub API
```

Three layers:

1. **Source of truth** — GitHub: labels mark queued work; refs encode locks and timestamps.
2. **Event source** — the orchestrator's 60s poller. No push channel.
3. **Workers** — one-shot Docker containers spawned by the orchestrator per claimed task.

## 5. Design choices & rationale

**Why polling instead of webhooks.** The only cost is latency (worst-case 60s). Polling needs no inbound connection, no smee relay, and no webhook-secret management. Rate-limit budget is trivial: ~4 API requests per tick against a 5000/hour authenticated limit.

**Why git-ref atomic locks even for a single local orchestrator.** Two reasons. First, it's the only GitHub primitive with strict create-or-fail semantics (`POST /git/refs` → 201 or 422). Labels are read-then-write and race; refs don't. Second, the lock contract lives in GitHub, so a second orchestrator on another machine is safe to add later with no code change.

**Why timestamp-in-separate-tag, not in the lock name.** The lock ref must have a fixed name for atomic-create to arbitrate. Encoding the timestamp into the lock name breaks atomicity (different agents generate different timestamps, all create calls succeed). A separate tag under a parallel prefix (`refs/tags/claim/issue-N/<ts>`) carries the timestamp; the agent that already won the lock creates it, so there is no contention for it.

**Why a branch for implementer, a tag for reviewer.** The implementer needs a branch anyway to push code — the branch doubles as the lock for free. The reviewer does not push code, so a branch lock would clutter `git branch -a` for no benefit. A tag under a dedicated prefix gives identical atomic semantics while staying out of the way.

**Why host-side git worktree bind-mounted into the container.** Cheapest option: no network clone per task, shares host's `.git` objects, leaves a browsable checkout on the host if the task fails.

**Why Go for the orchestrator.** Single static binary, no host runtime dep, ergonomic concurrency primitives for the tick-loop + per-task goroutines, matches the project's existing `gofmt`-aware tooling.

## 6. Vocabulary

### 6.1 Labels

| Label | Applied to | Meaning |
|---|---|---|
| `claude-task` | Issue | Queued for implementer |
| `claude-processing` | Issue | Implementer holds lock (cosmetic) |
| `claude-done` | Issue | Implementer finished, PR opened |
| `claude-review` | PR | Queued for reviewer |
| `claude-reviewing` | PR | Reviewer holds lock (cosmetic) |
| `claude-reviewed` | PR | Reviewer finished |

Failure has no dedicated label. A failed run drops the lock and leaves the queue label (`claude-task` / `claude-review`) intact so the task is retried on the next tick. If a task fails persistently, the user removes the queue label manually to stop retrying.

Labels are visible in the GitHub UI. Refs are the source of truth — no behavior depends on a label being correctly set.

### 6.2 Refs

| Ref | Created by | Purpose |
|---|---|---|
| `refs/heads/claude/issue-{N}` | Implementer (via orchestrator) | Lock + work branch |
| `refs/tags/claim/issue-{N}/{ts}` | Implementer (via orchestrator) | Claim timestamp |
| `refs/tags/review-lock/pr-{N}` | Reviewer (via orchestrator) | Lock |
| `refs/tags/review-claim/pr-{N}/{ts}` | Reviewer (via orchestrator) | Claim timestamp |

Timestamps use the form `YYYYMMDDTHHMMSSZ` (parses with `time.Parse("20060102T150405Z", ...)`).

## 7. Invocation UX

```
cc-crew up                                      # uses $PWD
cc-crew up --repo ~/Work/myproject              # explicit path
cc-crew up --max-implementers 3 --max-reviewers 8
cc-crew up --task-label needs-claude            # override claude-task
cc-crew up --auto-review                        # auto-label implementer PRs for reviewer
cc-crew status                                  # stateless snapshot
cc-crew reset [--yes]                           # destructive cleanup
```

`cc-crew up` runs foreground with streaming logs. Ctrl-C (SIGINT) is a clean shutdown: stop polling, `docker kill` all running task containers, release their locks, exit 0.

`cc-crew status` is stateless: it reads from GitHub (labels, refs, timestamp tags) and from `docker ps --filter label=cc-crew.repo=<owner/name>`. It works whether any orchestrator is running and shows a joined view: running containers, queued tasks waiting on a semaphore, and claimed-but-unfinished tasks (with claim age).

`cc-crew reset` is the bulk-cleanup escape hatch — see §7.3.

### 7.1 Config surface

All flags have env fallbacks.

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--repo <path>` | `CC_REPO` | `$PWD` | Local repo path (must be a git clone with an `origin` remote) |
| `--max-implementers N` | `CC_MAX_IMPLEMENTERS` | `5` | Implementer semaphore |
| `--max-reviewers N` | `CC_MAX_REVIEWERS` | `5` | Reviewer semaphore |
| `--poll-seconds N` | `CC_POLL_SECONDS` | `60` | Tick interval |
| `--task-label L` | `CC_TASK_LABEL` | `claude-task` | Implementer queue label |
| `--review-label L` | `CC_REVIEW_LABEL` | `claude-review` | Reviewer queue label |
| `--reclaim-seconds N` | `CC_RECLAIM_SECONDS` | `1800` | Stale-lock age threshold |
| `--impl-task-seconds N` | `CC_IMPL_TASK_SECONDS` | `3600` | Per-container wall-clock for implementer |
| `--review-task-seconds N` | `CC_REVIEW_TASK_SECONDS` | `900` | Per-container wall-clock for reviewer |
| `--auto-review` | `CC_AUTO_REVIEW` | `false` | Implementer PRs get `claude-review` applied automatically |
| `--base-branch B` | `CC_BASE_BRANCH` | default branch from GitHub | PR base |
| `--image I` | `CC_IMAGE` | `ghcr.io/charleszheng44/cc-crew:latest` | Task container image |
| `--model M` | `CC_MODEL` | `claude-sonnet-4-6` | Passed to `claude -p --model` |

### 7.2 Credentials (env only)

| Env | Required | Purpose |
|---|---|---|
| `GH_TOKEN_IMPLEMENTER` | yes* | Passed into implementer containers |
| `GH_TOKEN_REVIEWER` | yes* | Passed into reviewer containers |
| `GH_TOKEN` | fallback | Used for both personas when per-persona vars unset; also used by the orchestrator's own API calls |
| `CLAUDE_CODE_OAUTH_TOKEN` | one of these two | Passed into all containers |
| `ANTHROPIC_API_KEY` | one of these two | Passed into all containers |
| `IMPLEMENTER_GIT_NAME` | yes (if implementer enabled) | `GIT_AUTHOR_NAME` + `GIT_COMMITTER_NAME` in implementer containers |
| `IMPLEMENTER_GIT_EMAIL` | yes (if implementer enabled) | `GIT_AUTHOR_EMAIL` + `GIT_COMMITTER_EMAIL` in implementer containers |
| `REVIEWER_GIT_NAME` | yes (if reviewer enabled) | Same, for reviewer |
| `REVIEWER_GIT_EMAIL` | yes (if reviewer enabled) | Same, for reviewer |

\* At least one of `GH_TOKEN_<ROLE>` or `GH_TOKEN` must be set. Per-persona tokens let the two personas act as distinct GitHub identities (the original motivation for cc-crew). A single shared `GH_TOKEN` is supported for simpler setups.

### 7.3 Reset — bulk cleanup

`cc-crew reset [--yes]` is the destructive escape hatch. It restores the target repo to a clean "all work queued" position, useful when things get wedged — orphaned locks, containers killed out-of-band, manual label edits that left the state incoherent.

What it does, in order:

1. **Kill running task containers** for this repo: every container labeled `cc-crew.repo=<owner/name>` is `docker kill`ed.
2. **Delete implementer lock branches**: enumerate `refs/heads/claude/issue-*`; for each, parse the issue number `N`, `DELETE /git/refs/heads/claude/issue-N`.
3. **Delete implementer claim tags**: enumerate `refs/tags/claim/issue-*/*`; delete each.
4. **Delete reviewer lock tags**: enumerate `refs/tags/review-lock/pr-*`; delete each.
5. **Delete reviewer claim tags**: enumerate `refs/tags/review-claim/pr-*/*`; delete each.
6. **Restore issue labels**: for each issue that had a cc-crew ref deleted above, and whose GitHub state is `open`:
   - Remove `claude-processing` if present.
   - Ensure `claude-task` is present.
7. **Restore PR labels**: for each PR that had a cc-crew ref deleted above, and whose state is `open`:
   - Remove `claude-reviewing` if present.
   - Ensure `claude-review` is present.
8. **Prune host worktrees**: `git worktree remove --force` every path under `.claude-worktrees/` in the target repo; then `git worktree prune`.

Safety:
- **Scope is strict.** Reset only touches refs under the four cc-crew prefixes (`refs/heads/claude/issue-*`, `refs/tags/claim/issue-*`, `refs/tags/review-lock/pr-*`, `refs/tags/review-claim/pr-*`) and the six cc-crew labels. Any other branches, tags, or labels are untouched.
- **Dry-run default.** Without `--yes`, reset prints the plan (counts + examples) and exits without making any change. `--yes` proceeds.
- **Running orchestrator.** If `cc-crew up` is running against the same repo, reset still works — the orchestrator's next tick observes the restored state and picks up the requeued work. For deterministic behavior, stop the orchestrator first (Ctrl-C); the CLI prints a warning if it detects running cc-crew containers.
- **Closed issues / merged PRs.** Step 6/7 skip issues and PRs that are not open — we never re-add `claude-task` to a closed issue or `claude-review` to a merged PR.

## 8. Orchestrator internals

### 8.1 Tick loop

One tick runs every `--poll-seconds`, in a single goroutine:

```
tick():
    reclaim_stale_implementer_locks(RECLAIM_SECS)
    reclaim_stale_reviewer_locks(RECLAIM_SECS)

    candidate_issues = gh.list_issues(
        label=TASK_LABEL, state=open,
        not_label=PROCESSING_LABEL, sort=number_asc)
    candidate_prs = gh.list_prs(
        label=REVIEW_LABEL, state=open,
        not_label=REVIEWING_LABEL, sort=number_asc)

    for issue in candidate_issues:
        if not impl_sem.try_acquire(): break
        if not try_claim_issue(issue):
            impl_sem.release(); continue
        go dispatch_implementer(issue)          # releases semaphore on exit

    for pr in candidate_prs:
        if not review_sem.try_acquire(): break
        if not try_claim_pr(pr):
            review_sem.release(); continue
        go dispatch_reviewer(pr)
```

Backpressure: if the semaphore is full, candidate work stays labeled in GitHub and is reconsidered on the next tick. Nothing is buffered in orchestrator memory.

### 8.2 Atomic claim

```
POST /repos/{owner}/{repo}/git/refs
  body: { "ref": "<full_ref_name>", "sha": "<base_sha>" }

  201 → claimed, proceed
  422 + "Reference already exists" → another orchestrator got it, skip
  other status → surfaced, logged, next tick retries
```

On successful claim, the orchestrator immediately creates the timestamp tag under the parallel prefix. If tag creation fails (network blip), the next orchestrator that sees an orphan lock re-creates the tag atomically — see §8.3 step 2.

### 8.3 Reclaim sweeper

Per tick, for each `refs/heads/claude/issue-*` and `refs/tags/review-lock/pr-*`:

1. List timestamp tags under the matching prefix.
2. If no timestamp tag exists (creator crashed between lock creation and tag creation), create one now pointing at the lock's SHA. This resets the reclaim window rather than racing to delete; the tag is atomically create-or-fail, so two orchestrators observing the same orphan lock converge on a single tag.
3. Otherwise find the oldest timestamp tag by parsing the trailing `YYYYMMDDTHHMMSSZ` in its name.
4. If the age is below `RECLAIM_SECS`, leave it alone.
5. If at or above: check the "already done" condition first (see below). If the work has actually completed, do not reap.
6. Otherwise delete all timestamp tags under the prefix, then delete the lock.

"Already done" checks:
- Implementer: a PR exists whose head is the lock branch (`gh pr list --head <branch>`).
- Reviewer: a review has been posted on the PR by the reviewer persona's GitHub login. The login is resolved once at startup via `gh api user -q .login` using `GH_TOKEN_REVIEWER` (falling back to `GH_TOKEN`), and compared against each entry in `gh pr view N --json reviews`.

### 8.4 Per-task lifecycle (implementer)

```
1. Claim issue (ref + tag created).
2. Add claude-processing label (best-effort; failure is non-fatal).
3. Host: git fetch origin claude/issue-N
         git worktree add .claude-worktrees/issue-N claude/issue-N
4. docker run --rm (blocks on container exit, up to IMPL_TASK_SECONDS).
5. On exit 0:
     remove claude-task, claude-processing; add claude-done.
     delete timestamp tag. Do NOT delete the lock branch — the open PR
     references it, and deleting the branch would auto-close the PR.
     if --auto-review: find the PR number via
       `gh pr list --head claude/issue-<N> --json number -q '.[0].number'`
     then `gh pr edit <PR> --add-label claude-review`. The next poll tick
     picks it up for review.
6. On nonzero exit / timeout:
     remove claude-processing (leave claude-task intact for retry).
     delete all timestamp tags under claim/issue-N/.
     delete the lock branch refs/heads/claude/issue-N.
     (No failure label. The issue returns to the queue and is picked up
     on the next tick, possibly by a different orchestrator.)
7. Always: git worktree remove --force .claude-worktrees/issue-N.
8. Release semaphore slot.
```

Reviewer lifecycle is symmetric:
- Success: the container uses `--permission-mode plan` (read-only) and posts the review via `gh pr review --body-file`; labels transition `claude-review`/`claude-reviewing` → `claude-reviewed`.
- Failure / timeout: remove `claude-reviewing`, delete all timestamp tags under `review-claim/pr-N/`, delete the lock tag `refs/tags/review-lock/pr-N`; leave `claude-review` intact for retry.

### 8.5 Docker invocation shape (implementer)

```
docker run --rm \
  --name cc-crew-impl-<owner>-<repo>-<N> \
  --label cc-crew.repo=<owner>/<repo> \
  --label cc-crew.role=implementer \
  --label cc-crew.issue=<N> \
  -e CC_ROLE=implementer \
  -e CC_ISSUE_NUM=<N> \
  -e CC_BASE_BRANCH=<base> \
  -e CC_MODEL=<model> \
  -e CC_MAX_TURNS=25 \
  -e GH_TOKEN=<impl token> \
  -e CLAUDE_CODE_OAUTH_TOKEN=<token> \
  -e GIT_AUTHOR_NAME=<impl git name> \
  -e GIT_AUTHOR_EMAIL=<impl git email> \
  -e GIT_COMMITTER_NAME=<impl git name> \
  -e GIT_COMMITTER_EMAIL=<impl git email> \
  -v <host-worktree>:/workspace:rw \
  -v <host-repo>/.git:<host-repo>/.git:ro \
  <image>
```

The `.git` mount is read-only. The container's pushes reach GitHub directly over HTTPS via gh's credential helper; the shared `.git` is only referenced by the worktree for objects/refs.

## 9. Personas

### 9.1 Layout

```
personas/
├── reviewer/                    # existing
│   ├── CLAUDE.md
│   └── settings.json
└── implementer/                 # NEW
    ├── CLAUDE.md
    └── settings.json            # retained for documentation + fallback
```

### 9.2 Implementer CLAUDE.md (intent)

Instructions loaded as user memory inside the implementer container. Prescriptive, short:

- You are resolving a single GitHub issue in a worktree already checked out on `claude/issue-<N>`.
- The issue body is at `/tmp/issue.md`.
- Implement the change following existing repo conventions; run obvious tests/lints if available.
- Commit once with `Resolve #<N>: <title>`.
- `git push origin HEAD`.
- `gh pr create --base <BASE> --head claude/issue-<N> --title "Resolve #<N>: <title>" --body "Closes #<N>"`.
- Do not push to any other branch, force-push, rewrite history, merge the PR, or touch unrelated files.

### 9.3 Reviewer CLAUDE.md

The existing reviewer persona is retained as-is.

### 9.4 Container entrypoint (`scripts/cc-crew-run`)

A small shell script baked into the image. Responsibility split: the entrypoint sets up the environment, Claude does the work end-to-end.

The entrypoint:

1. Configures git identity from `GIT_AUTHOR_NAME/EMAIL` (and sets `GIT_COMMITTER_*` to the same values).
2. Runs `gh auth setup-git` so `git push` uses gh as the HTTPS credential helper.
3. Writes the issue or PR body to `/tmp/issue.md` or `/tmp/pr.md` via `gh issue view` / `gh pr view`.
4. `exec`s `claude -p "<role prompt>"` with:
   - `--dangerously-skip-permissions` for implementer
   - `--permission-mode plan` for reviewer
   - `--model "$CC_MODEL"` and `--max-turns "$CC_MAX_TURNS"` for both

Claude's CLAUDE.md (per-persona) instructs it to do the role-specific work itself: the implementer commits, pushes, and runs `gh pr create`; the reviewer runs `gh pr review --body-file`. The entrypoint does not wrap or post-process this — it simply propagates `claude`'s exit code. The orchestrator reads that exit code to decide between the success path (`claude-done` / `claude-reviewed`) and the retry path (drop the lock, leave the queue label intact).

## 10. File layout

```
cc-crew/                                # renamed from cc-crew
├── Dockerfile                          # extended: COPY scripts/cc-crew-run /usr/local/bin/
├── README.md                           # extended with orchestrator usage
├── personas/
│   ├── reviewer/                       # existing
│   └── implementer/                    # NEW
├── scripts/
│   └── cc-crew-run                     # NEW, in-container entrypoint
├── cmd/
│   └── cc-crew/
│       └── main.go                     # NEW, CLI entrypoint (up, status)
├── internal/
│   ├── config/                         # flag/env parsing, defaults
│   ├── github/                         # gh + REST wrappers
│   ├── claim/                          # try_claim, release, list tags, claim age
│   ├── reclaim/                        # stale-lock sweeper
│   ├── docker/                         # docker run/ps/kill shims
│   ├── scheduler/                      # semaphores, tick loop
│   ├── worktree/                       # git worktree add/remove on host
│   └── status/                         # read-only status rendering
├── go.mod
├── go.sum
└── docs/
    └── superpowers/
        └── specs/
            └── 2026-04-16-cc-crew-orchestrator-design.md   # this file
```

## 11. Failure modes & recovery

| Failure | Detection | Recovery |
|---|---|---|
| Container exits non-zero | `docker wait` nonzero | Remove `claude-processing`/`claude-reviewing`, delete lock ref + all timestamp tags, remove worktree, release semaphore. Queue label (`claude-task`/`claude-review`) stays, so work is retried on next tick. |
| Container hangs | `MAX_TASK_SECONDS` expires | `docker kill`, then same as above. |
| Orchestrator crashes mid-task | N/A (dead process) | Reclaim reaps locks older than `RECLAIM_SECS`; queue label is untouched, so the next orchestrator picks the work back up. |
| GitHub API unreachable / rate-limited | API call errors | Log, back off one tick, do not touch labels; work stays queued in GitHub. |
| Two orchestrators race on same issue | `POST /git/refs` returns 422 | Loser releases semaphore slot, continues. |
| Lock created but timestamp tag creation fails | Reclaim sees lock with no timestamp | Any orchestrator re-creates the timestamp tag atomically pointing at the lock SHA (§8.3 step 2). No data loss; reclaim window restarts. |
| Claude finishes but `gh pr create` fails | Entrypoint exits nonzero | Lock released, queue label intact → retried on next tick. If the repeat also fails (same issue), user investigates and removes `claude-task` to stop the loop. |
| Claude force-pushes or touches other branches | Not prevented by design | CLAUDE.md forbids it; follow-up could add a pre-push hook. |
| Prompt injection exfiltrates GH token | Not prevented (no egress proxy) | Accepted risk for v1; scoped token limits blast radius. |
| User Ctrl-C's `cc-crew up` | SIGINT handler | Stop polling; `docker kill` all in-flight task containers; for each killed task remove `claude-processing`/`claude-reviewing` and delete its lock ref + timestamp tag (work re-appears on the next orchestrator start); wait for container exits; exit 0. |
| Host disk fills (worktrees accumulate) | Worktree create fails | Surface error, release lock, queue label intact for retry; operator runs `git worktree prune` or `cc-crew reset`. |
| PR force-pushed mid-review | Reviewer reads stale diff | Review posts against old lines; re-label `claude-review` to retrigger. |
| Issue re-labeled after completion | Reclaim's "already-done" check returns true | Leave branch; next tick sees `claude-done` and skips. |
| State wedged (orphaned locks, manual mis-labels) | User observes | `cc-crew reset` (§7.3) drops all cc-crew state and requeues every open issue/PR that had one. |

## 12. Implementation order

Incremental so each step is runnable and testable:

1. `internal/github` — thin wrappers around `gh` and `gh api`: list issues, list PRs, get/create/delete refs, list matching-refs, add/remove labels, get PR head SHA, list PR reviews. Unit-test against a scratch repo.
2. `internal/claim` — `try_claim`, `release`, `list_claim_tags`, `claim_age`. Pure API; testable with a scratch repo.
3. `internal/reclaim` — stale-lock sweeper on top of claim.
4. `internal/worktree` — `git worktree add` / `remove` shims; test against a local scratch repo.
5. `internal/docker` — `docker run` / `ps` / `kill` wrappers with context-cancel semantics.
6. `internal/scheduler` — tick loop + per-persona semaphores; wire 1–5 together.
7. `internal/config` — flag/env parsing.
8. `cmd/cc-crew/main.go` — `up`, `status`, and `reset` subcommands.
9. `scripts/cc-crew-run` — in-container entrypoint; add to `Dockerfile`.
10. `personas/implementer/` — CLAUDE.md + settings.json.
11. End-to-end: open a labeled issue in a scratch repo, observe claim → branch → PR → label transition. Then label the PR `claude-review`, observe review post. Then run `cc-crew reset --yes` against the scratch repo and confirm all cc-crew refs are gone and labels are restored.

## 13. Migration from cc-crew

Out-of-band, not part of this spec: rename the GitHub repo `charleszheng44/cc-crew` → `charleszheng44/cc-crew`, update the Docker image name in `docker-publish.yml`, update README references. No code behavior changes.
