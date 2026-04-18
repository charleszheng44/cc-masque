# cc-crew continuous addressing — design

Status: approved for planning
Date: 2026-04-17
Builds on: `2026-04-16-cc-crew-orchestrator-design.md`

## 1. Overview

The cc-crew orchestrator today drives each issue/PR through a single pass:
an issue gets implemented once, a PR gets reviewed once. This feature adds
two continuous feedback loops so a PR opened by cc-crew can converge on an
approved state without human intervention on each round:

- **Address loop** — when a PR has a new non-approval review, dispatch an
  implementer (in addresser mode) to amend the PR with commits that respond
  to the review.
- **Re-review loop** — when a PR has new commits since the last cc-crew
  review, dispatch the reviewer again.

Everything is label-mediated: the orchestrator detects and applies labels;
existing claim/scheduler machinery does the actual dispatch.

## 2. Goals

- Auto-converge cc-crew-owned PRs on reviewer approval.
- Bounded cycles per PR (default 3) to cap token spend on picky reviewers.
- Reuse the existing claim/scheduler/worker architecture; no new persona,
  no new semaphore, no new container image role.
- Detection is observable: every state transition goes through a label.
- Multi-orchestrator-safe: markers live in GitHub refs, claims are atomic.

## 3. Non-goals (v1)

- Scope beyond cc-crew-owned PRs. Human-opened PRs are not addressed (can be
  added later as an opt-in).
- Inline review comments (`pulls/N/comments`) or PR issue comments as
  triggers. Only top-level PR reviews (`pulls/N/reviews`) count.
- Differentiating human vs reviewer-persona reviews. Any non-approval review
  counts toward the cycle cap.
- Back-pressure between the two loops. They run independently each tick.

## 4. Vocabulary additions

### 4.1 Labels (applied to PRs only)

| Label | Meaning |
|---|---|
| `claude-address` | Queued for implementer (addresser mode) |
| `claude-addressing` | Addresser holds lock (cosmetic) |
| `claude-addressed` | Addresser finished; commits pushed |

No new labels for the re-review loop — it reuses `claude-review` and
`claude-reviewed`. The loop detects a mismatch and flips the label.

### 4.2 Refs

| Ref | Created by | Purpose |
|---|---|---|
| `refs/tags/address-lock/pr-{N}` | Orchestrator (on claim) | Atomic lock tag (fixed name) for addresser dispatch |
| `refs/tags/address-claim/pr-{N}/{ts}` | Orchestrator (on claim) | Claim timestamp for age tracking / reclaim sweeper |
| `refs/tags/cc-crew-addressed/pr-{N}/{review-id}` | Orchestrator (on addresser success) | Marker: review ID has been addressed |
| `refs/tags/cc-crew-rereviewed/pr-{N}/{head-sha}` | Orchestrator (on reviewer success) | Marker: head SHA has been reviewed |

The addresser does its work on the **existing** `refs/heads/claude/issue-{M}`
branch (the PR's source branch, alive for the PR's lifetime) — no new
branch is created. The atomic claim is arbitrated on
`refs/tags/address-lock/pr-{N}` (fixed name, `POST /git/refs` returns 422
if it exists), mirroring the reviewer's `review-lock` + `review-claim`
split (spec §5).

Timestamps use `YYYYMMDDTHHMMSSZ` (same as existing claim tags).

## 5. Architecture

### 5.1 Tick-loop changes

```
tick():
    reclaim_stale_implementer_locks()     (existing)
    reclaim_stale_reviewer_locks()        (existing)

    if cfg.Continuous:
        detect_continuous()                (new; §6)

    impl_scheduler.Tick()                 (existing)
    review_scheduler.Tick()                (existing)
    address_scheduler.Tick()               (new; §7)
```

`detect_continuous()` runs **before** scheduler ticks, so any labels it
applies are claimed within the same tick.

### 5.2 Schedulers

| Scheduler | Queue label | Lock label | Semaphore | Done label |
|---|---|---|---|---|
| implementer | `claude-task` | `claude-processing` | `--max-implementers` | `claude-done` |
| reviewer | `claude-review` | `claude-reviewing` | `--max-reviewers` | `claude-reviewed` |
| **address (new)** | `claude-address` | `claude-addressing` | **shares `--max-implementers`** | `claude-addressed` |

The address-scheduler is structurally a reviewer-shaped scheduler (operates
on PRs, uses `pr.headRefOid` as the claim SHA) dispatching to an
implementer-shaped worker (amends the existing branch). It shares the
implementer semaphore because addresser work is the same class as
implementer work (writes code, pushes commits).

## 6. Detection logic

Runs once per tick, before the scheduler ticks:

```
detect_continuous():
    prs = gh.ListPRs(repo, state=open)
    for pr in prs:
        # Scope: cc-crew-owned PRs only.
        if not pr.headRefName.startswith("claude/issue-"):
            continue

        # --- feature 2: re-review on new commit ---
        rereviewed_refs = gh.ListMatchingRefs(
            "tags/cc-crew-rereviewed/pr-{}/".format(pr.number))
        reviewed_shas = {r.name.rsplit("/", 1)[1] for r in rereviewed_refs}

        if (pr.headRefOid not in reviewed_shas
                and "claude-reviewed" in pr.labels
                and "claude-review" not in pr.labels
                and "claude-reviewing" not in pr.labels):
            gh.RemoveLabel(pr.number, "claude-reviewed")
            gh.AddLabel(pr.number, "claude-review")

        # --- feature 1: address non-approval reviews ---
        addressed_refs = gh.ListMatchingRefs(
            "tags/cc-crew-addressed/pr-{}/".format(pr.number))
        addressed_ids = {int(r.name.rsplit("/", 1)[1]) for r in addressed_refs}

        # Cycle cap: stop auto-labeling once hit.
        if len(addressed_ids) >= cfg.MaxCycles:
            continue

        reviews = gh.ListReviews(pr.number)
        # GitHub review states we care about:
        #   APPROVED         → terminal-positive, never triggers
        #   CHANGES_REQUESTED → triggers
        #   COMMENTED        → triggers
        #   DISMISSED        → withdrawn by author/admin, skip
        #   PENDING          → not-yet-submitted draft, skip
        trigger_states = {"COMMENTED", "CHANGES_REQUESTED"}
        unaddressed = [
            r for r in reviews
            if r.state in trigger_states and r.id not in addressed_ids
        ]
        if (unaddressed
                and "claude-address" not in pr.labels
                and "claude-addressing" not in pr.labels):
            gh.AddLabel(pr.number, "claude-address")
```

### 6.1 API cost

`1 + 2N` calls per tick where `N = |cc-crew-owned open PRs|`:

- 1 × `ListPRs`
- N × `ListMatchingRefs(cc-crew-rereviewed/pr-N/)`
- N × `ListReviews` (only for PRs under the cycle cap; skipped otherwise)

Well below the 5000/h authenticated rate limit for any realistic N.

### 6.2 Marker writes

Markers are written by the **worker lifecycle on success**, not the
detector. This keeps the detector read-only for GitHub refs, which matches
the rest of the orchestrator's design.

- **Reviewer `successCleanup`** — additionally creates
  `refs/tags/cc-crew-rereviewed/pr-{N}/{headRefOid}`. The SHA is captured at
  dispatch time (same value used for the review-lock claim) so there is no
  race with a push landing during the review.

- **Addresser `successCleanup`** — for each review ID in the
  dispatch-time snapshot (the unaddressed IDs as seen at claim time),
  creates `refs/tags/cc-crew-addressed/pr-{N}/{id}`. The addresser does not
  communicate back to the host which reviews it actually addressed. The
  semantic is: *"the addresser got one shot at this batch; if it missed
  something, the user can re-comment to re-queue."* This removes any
  container-to-host signalling and keeps the failure story simple.

## 7. Address-scheduler dispatch flow

```
1. detect_continuous() labeled PR #N with claude-address.

2. address-scheduler.Tick():
   - candidates = gh.ListPRs(repo,
                             with=[claude-address],
                             without=[claude-addressing])
   - for each pr in candidates:
       if not impl_sem.try_acquire(): break
       won, _, err = try_claim_address(pr.number, pr.headRefOid)
       if err or not won:
           impl_sem.release(); continue
       add_label(pr.number, claude-addressing)   # best-effort
       snapshot_review_ids = [r.id for r in gh.ListReviews(pr.number)
                              if r.state in {COMMENTED, CHANGES_REQUESTED}
                              and r.id not in addressed_ids(pr.number)]
       go dispatch_addresser(pr.number, pr.headRefOid, snapshot_review_ids)

3. try_claim_address(N, head_sha):
   POST /git/refs  {ref: refs/tags/address-lock/pr-N, sha: head_sha}
     201 → won the atomic arbitration
     422 "already exists" → lost race, skip
   if won:
     POST /git/refs  {ref: refs/tags/address-claim/pr-N/{ts}, sha: head_sha}
     (separate timestamp tag for age/reclaim; failure is non-fatal —
      the reclaim sweeper re-creates missing ts tags atomically, §8.3)

4. dispatch_addresser(N, head_sha, review_ids):
   host: git fetch origin claude/issue-M
         git worktree add .claude-worktrees/address-N <head_sha>
   docker run --rm
     env:
       CC_ROLE=implementer
       CC_TASK_KIND=address                (NEW)
       CC_PR_NUM=N                          (NEW for this mode)
       CC_ISSUE_NUM=M                       (parsed from claude/issue-M)
       CC_REVIEW_IDS=id1,id2,...            (comma-joined snapshot)
       (GH_TOKEN, git identity, Claude creds — same as implementer today)
     mounts:
       <host-worktree>:/workspace
       <host-repo>/.git:<host-repo>/.git:rw

5. on exit 0 (successCleanup):
   - for each id in review_ids:
       create refs/tags/cc-crew-addressed/pr-N/{id}
   - delete refs/tags/address-claim/pr-N/{ts}
   - delete refs/tags/address-lock/pr-N
   - remove labels: claude-address, claude-addressing
   - add label: claude-addressed
   - DO NOT delete refs/heads/claude/issue-M (PR still open)

6. on nonzero exit / timeout (failCleanup):
   - remove label: claude-addressing
   - delete refs/tags/address-claim/pr-N/{ts}
   - delete refs/tags/address-lock/pr-N
   - leave claude-address on the PR (queue re-processes on next tick)
   - DO NOT create cc-crew-addressed markers

7. always:
   - git worktree remove --force .claude-worktrees/address-N
   - release implementer semaphore slot
```

## 8. Container entrypoint — `scripts/cc-crew-run`

A new case branch for `CC_ROLE=implementer` + `CC_TASK_KIND=address`:

```bash
if [[ "${CC_ROLE:-}" == "implementer" && "${CC_TASK_KIND:-}" == "address" ]]; then
  : "${CC_PR_NUM:?CC_PR_NUM required}"
  : "${CC_REVIEW_IDS:?CC_REVIEW_IDS required}"

  # Build /tmp/reviews.md — PR body + each requested review body.
  gh pr view "$CC_PR_NUM" -R "$CC_REPO" --json number,title,body \
    -q '"# PR #\(.number): \(.title)\n\n\(.body)\n\n---\n"' > /tmp/reviews.md

  IFS=',' read -ra IDS <<<"$CC_REVIEW_IDS"
  for id in "${IDS[@]}"; do
    gh api "repos/$CC_REPO/pulls/$CC_PR_NUM/reviews/$id" \
      -q '"## Review \(.id) (state: \(.state))\n\n\(.body // "(no body)")\n"' \
      >> /tmp/reviews.md
  done

  PROMPT="You are addressing review feedback on PR #${CC_PR_NUM}. Read /tmp/reviews.md. Make targeted commits on the current branch (claude/issue-${CC_ISSUE_NUM}) that respond to the feedback. Do NOT run gh pr create; the PR already exists. Just git push origin HEAD when done."

  exec claude -p "$PROMPT" \
    --model "$CC_MODEL" \
    "${max_turns_args[@]}" \
    --dangerously-skip-permissions
fi
```

The existing `CC_ROLE=implementer` (default, `CC_TASK_KIND` unset or
`create`) and `CC_ROLE=reviewer` branches are unchanged.

### 8.1 Implementer persona — `personas/implementer/CLAUDE.md`

One new section appended:

```md
## If addressing review feedback (CC_TASK_KIND=address)

You are amending an existing PR, not creating a new one. Do NOT run
`gh pr create`. Read `/tmp/reviews.md`, make the smallest set of commits
that responds to the feedback, and `git push origin HEAD`. Do not modify
files unrelated to the review feedback.
```

## 9. Config additions

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--max-cycles N` | `CC_MAX_CYCLES` | `3` | Max address dispatches per PR before the detector stops auto-labeling |
| `--continuous` | `CC_CONTINUOUS` | `true` | Master switch for both loops. Set `false` to run cc-crew in v1 one-shot mode. |
| `--address-label L` | `CC_ADDRESS_LABEL` | `claude-address` | Queue label override |
| `--addressing-label L` | `CC_ADDRESSING_LABEL` | `claude-addressing` | Lock label override |
| `--addressed-label L` | `CC_ADDRESSED_LABEL` | `claude-addressed` | Done label override |

No new timeout — addresser reuses `--impl-task-seconds`.

## 10. Subcommand impact

### 10.1 `cc-crew up`

- Tick loop gains `detect_continuous()` call before scheduler ticks, gated
  on `cfg.Continuous`.
- New `address-scheduler` goroutine (`scheduler.Run`) running alongside the
  existing two. Sharing `--max-implementers` with the implementer scheduler.
- `reviewer.successCleanup` extended: create
  `cc-crew-rereviewed/pr-{N}/{head-sha}` before returning.

### 10.2 `cc-crew status`

A new "Continuous" section showing, per cc-crew-owned open PR:

```
PR #39 head=abc123ef  last-rereviewed=abc123ef  addressed=2/3  pending-reviews=0
PR #41 head=77aa11bb  last-rereviewed=62cc33dd  addressed=1/3  pending-reviews=1
```

All values derived from existing state (labels, refs, `ListReviews`). No
new state source.

### 10.3 `cc-crew reset`

Extends in three places:

- **Strip-label set** now includes `claude-address`, `claude-addressing`,
  `claude-addressed`.
- **Ref prefixes** to enumerate-and-delete extend to:
  - `tags/address-lock/pr-*`
  - `tags/address-claim/pr-*`
  - `tags/cc-crew-addressed/pr-*`
  - `tags/cc-crew-rereviewed/pr-*`
- **Orphan-label discovery** (added during smoke testing) extends its
  label sweep to `claude-address*`, so reset picks up PRs whose
  `claude-addressing`/`claude-addressed` label lingered without refs.
- **Reviewed → review flip** is unchanged (a reset wants the reviewer to
  re-run from scratch on requeue).

## 11. Failure modes (additions to orchestrator spec §11)

| Failure | Detection | Recovery |
|---|---|---|
| Addresser exits non-zero | `docker wait` nonzero | Remove `claude-addressing`, delete `address-claim/pr-N/{ts}`. Leave `claude-address` for retry on next tick. |
| Addresser hangs | `--impl-task-seconds` expires | `docker kill`, same as above. |
| New comment during addresser run | Comment post-dates dispatch snapshot | Not addressed this cycle; detector picks it up next tick (still within cycle cap). |
| Cycle cap hit, PR still has unaddressed reviews | Detector skips PR silently each tick | Documented: user deletes `cc-crew-addressed/pr-N/*` tags (or runs `cc-crew reset`) to reset cycles, then re-triggers by re-commenting. |
| Two orchestrators race on same address dispatch | `POST /git/refs` on `address-claim` returns 422 | Loser releases semaphore, continues. |
| Push lands during reviewer run | `headRefOid` at dispatch differs from post-run head | Reviewer's marker records the SHA it saw at dispatch; detector catches the mismatch on a later tick and re-queues. No data loss. |
| Addresser succeeds but introduces a reviewer-unfriendly change | Reviewer re-reviews, posts CHANGES_REQUESTED | Counts as a new cycle. If the loop diverges, cycle cap terminates it. |

## 12. Implementation order

Incremental so each step is runnable and testable:

1. `internal/continuous` — new package housing `detect_continuous()` + unit
   tests using the existing `github.FakeClient`.
2. `internal/claim` — extend with `TryClaimAddress` / `ReleaseAddress`
   variants (reuse the existing atomic-claim primitive with the new tag
   prefix).
3. `internal/scheduler` — new address-scheduler that shares the implementer
   semaphore; address-mode `Dispatcher`.
4. `internal/scheduler/lifecycle.go` — addresser lifecycle (success/fail
   cleanup, marker creation).
5. `internal/scheduler/lifecycle.go` — reviewer `successCleanup` creates
   `cc-crew-rereviewed` marker.
6. `internal/config` — new flags/env.
7. `cmd/cc-crew/up.go` — wire detector + address-scheduler.
8. `cmd/cc-crew/status.go` — continuous section.
9. `cmd/cc-crew/reset.go` + `internal/reset` — label/ref extensions.
10. `scripts/cc-crew-run` — new `CC_TASK_KIND=address` branch; rebuild image.
11. `personas/implementer/CLAUDE.md` — addressing block.
12. End-to-end: open an issue, let cc-crew PR it, post a non-approval review
    manually, observe address dispatch → new commits → re-review auto-run.
    Then push a commit directly to the PR branch and observe re-review
    without needing to touch labels.
