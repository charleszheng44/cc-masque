# Auto-merger & conflict-resolver — design

- **Date**: 2026-04-20
- **Status**: Draft (awaiting user review)
- **Scope**: Close the cc-crew loop so an approved PR merges itself; resolve merge conflicts with an LLM when GitHub's native update-branch isn't enough.

## 1. Problem

Today's pipeline is `implementer → reviewer → addresser`, then a **human merges**. That last hop is the only non-autonomous step. When multiple PRs stack up behind the human, throughput is gated by attention, not by the agents. We want:

1. The reviewer's APPROVED verdict to trigger an automatic merge.
2. Rebase conflicts to be resolved by an agent rather than blocking humans.
3. A clear escalation when the agent genuinely can't resolve, so humans see only the hard cases.

## 2. Goals / non-goals

**Goals**

- New **merger** role: orchestrator-side logic that merges a PR when (a) reviewer posted APPROVED, (b) all required status checks are `SUCCESS`. Rebase strategy, branch deleted after merge.
- New **resolver** role: reuses the implementer Docker image to rebase and push on conflict.
- Clean escalation to `claude-conflict-blocked` with a comment; no silent stalls.
- Respect existing claim/lock semantics (SHA-pinned claims, reclaim sweeper).

**Non-goals**

- Merge queues / batching.
- Auto-merging PRs not originated by cc-crew (no `claude-review` history).
- Having the resolver fix test regressions beyond conflict markers.
- Squash / merge-commit strategies (rebase only).
- New GitHub token scopes — roles reuse existing tokens.

## 3. Architecture

Two new `claim.Kind` values join `KindImplementer`, `KindReviewer`, `KindAddresser`:

| Kind | Container | Slot source | Token |
|---|---|---|---|
| `KindMerger` | No — pure Go in the orchestrator. | New `--max-mergers` semaphore (default `2`). `0` disables merger and resolver. | `GH_TOKEN_REVIEWER` (same scope of writes as the reviewer role). |
| `KindResolver` | Yes — **reuses the implementer Docker image** with a new persona prompt. | **Shares the implementer semaphore** (per brainstorm Q5-C). | `GH_TOKEN_IMPLEMENTER`. |

Two scheduler instances are added next to the existing three in `cmd/cc-crew/up.go`. The merger's `Lifecycle.Dispatch` path does not invoke `docker.Runner`; its logic is a small state machine over GitHub API calls.

## 4. Label taxonomy

Five new labels, following the existing queue → lock → done pattern:

| Label | Added by | Removed by | Meaning |
|---|---|---|---|
| `claude-merge` | reviewer (when review state == `APPROVED`) | merger on success or terminal failure | Queue for merger. |
| `claude-merging` | merger | merger (always, on exit) | Lock. |
| `claude-resolve-conflict` | merger (when merge reports conflict) | resolver on success or on its own terminal failure | Queue for resolver. |
| `claude-resolving` | resolver | resolver (always, on exit) | Lock. |
| `claude-conflict-blocked` | merger or resolver on terminal failure | human only | Human attention needed; merger will not retry. |

`claude-merged` is **not** introduced — a merged PR's `closed + merged == true` state is canonical.

`cmd/cc-crew/init.go` extends `DefaultLabels()` to create the five. `reset.go` and `status.go` reference them so `cc-crew reset` and `cc-crew status` stay coherent.

## 5. Flow — reviewer extension

The reviewer today adds `claude-reviewed` after posting any review. Extend it so the review state dictates an additional queue label:

- `APPROVED` → add `claude-merge`, remove `claude-address` if present (verdict flipped from prior CHANGES_REQUESTED).
- `CHANGES_REQUESTED` → add `claude-address`, remove `claude-merge` if present (verdict flipped from prior APPROVED).
- `COMMENTED` → add neither; touch no existing queue labels. PR sits with `claude-reviewed` until human intervenes.

The reviewer passes the review state to its lifecycle; the flip-and-set is done in the same GitHub API round-trip that adds `claude-reviewed`. No new config.

## 6. Flow — merger tick

One iteration per open PR carrying `claude-merge` and not `claude-merging`:

1. **List.** `scheduler.listCandidates(KindMerger)` filters PRs with `claude-merge`, excludes `claude-merging`.
2. **Claim.** Existing `claim.Claimer` against the PR's current `headRefOid` — a new push mid-merge invalidates the claim.
3. **Lock.** Add `claude-merging`.
4. **Fetch PR state.** Read `mergeable`, `mergeStateStatus`, plus required status checks via `gh api repos/:o/:n/commits/:sha/check-runs` (filtering to the set in branch-protection).
5. **Gate on checks.** For each required check, classify its `conclusion`:
   - `SUCCESS`, `NEUTRAL`, `SKIPPED` → count as passing.
   - Conclusion still `null` (run in `QUEUED` / `IN_PROGRESS` / `WAITING` / `PENDING`) → pending. Release claim, leave `claude-merge` on, retry next tick.
   - `FAILURE`, `TIMED_OUT`, `CANCELLED`, `ACTION_REQUIRED`, `STARTUP_FAILURE`, `STALE` → terminal failure: add `claude-conflict-blocked`, comment with the check name and conclusion, remove `claude-merge`, exit.
   Only proceed to step 6 when every required check is passing.
6. **Gate on mergeability.**
   - `CLEAN` → step 7.
   - `BEHIND` (branch is behind base but no conflict) → call `PUT /repos/:o/:r/pulls/:n/update-branch` with `expected_head_sha` and `update_method: "rebase"`. Release claim, retry next tick once GitHub reports updated.
   - `DIRTY` (true conflict) → **dispatch resolver path**: add `claude-resolve-conflict`, remove `claude-merging`, release the merger claim. Leave `claude-merge` on so the next merger tick retries automatically once the resolver succeeds.
   - Anything else (`BLOCKED`, `DRAFT`, `UNKNOWN`) → terminal failure as in step 5's FAILURE branch.
7. **Merge.** `gh pr merge <n> --rebase --delete-branch`. On success: remove `claude-merging` and `claude-merge` (the PR is closed by GitHub). On error whose message does not indicate conflict → terminal failure. On conflict-indicating error → resolver path as in step 6.

## 7. Flow — resolver tick

One iteration per PR carrying `claude-resolve-conflict` and not `claude-resolving`:

1. **List & claim** (SHA-pinned on the PR head).
2. **Lock.** Add `claude-resolving`.
3. **Dispatch container** via `docker.Runner.Run` with:
   - Image = implementer's (same `l.Image`).
   - Env: `CC_MODE=resolve`, `CC_PR_NUM=<n>`, `CC_BASE_BRANCH=<base>`, plus the usual `GH_TOKEN`, `GIT_*`, `CC_MODEL`, workspace mount.
   - Prompt file: `personas/resolver.md` (new), instructing the agent to `git fetch origin`, `git checkout <head>`, `git rebase origin/<base>`, resolve markers, run `go build ./...` and `go test ./...`, and `git push --force-with-lease`.
4. **On container exit 0**:
   - Remove `claude-resolve-conflict` and `claude-resolving`.
   - **Re-trigger reviewer** (per brainstorm caveat resolution): remove `claude-reviewed`, re-add `claude-review`. Covers repos with "dismiss stale reviews on push" branch protection, and is harmless on repos without it (reviewer re-approves quickly).
   - `claude-merge` stays off until the reviewer re-approves, at which point the normal merger tick resumes.
5. **On container exit non-zero** (or context timeout):
   - Remove `claude-resolve-conflict` and `claude-resolving`.
   - Add `claude-conflict-blocked`, post a comment summarising what the agent tried (captured from the container's prefixed stdout tail).
   - Remove `claude-merge` so no future tick retries.

## 8. Configuration

New fields on `internal/config/config.Config`:

| Flag | Env | Default | Notes |
|---|---|---|---|
| `--max-mergers` | `MAX_MERGERS` | `2` | `0` disables merger **and** resolver. |
| (no new max for resolver) | — | — | Resolver uses implementer semaphore. |
| (no new label overrides in v1) | — | — | All five new labels are literal; overridable later if needed. |

`Config.Validate()` requires `GH_TOKEN_REVIEWER` to be set when `MaxMergers > 0` (merger calls the merge API); no new token is added.

## 9. Failure modes (consolidated)

| Trigger | Action |
|---|---|
| Required check failed / timed out | `claude-conflict-blocked`, comment names the check, merger exits. |
| Mergeable state `BLOCKED`/`DRAFT`/`UNKNOWN` | `claude-conflict-blocked`, comment reports state, merger exits. |
| `gh pr merge` non-conflict error (e.g., permission) | `claude-conflict-blocked`, comment includes stderr, merger exits. |
| Resolver container non-zero exit | `claude-conflict-blocked`, comment includes agent summary, `claude-merge` removed. |
| Resolver container timeout | same as non-zero exit. |
| Claim lost mid-run (new push invalidated SHA) | existing `claim.Claimer` semantics — current agent releases, next tick re-evaluates. |

## 10. Testing

**Unit**

- `scheduler.listCandidates(KindMerger)` picks only PRs with `claude-merge` and without `claude-merging`.
- `Lifecycle.Dispatch(KindMerger)` against a fake `github.Client` covers each of the six branches in §6 (clean merge, checks pending, check failed, update-branch, dirty, non-conflict error). No Docker involved.
- `Lifecycle.Dispatch(KindResolver)` happy path: fake `docker.Runner` exit 0 → labels flipped, reviewer re-triggered. Failure path: exit 1 → `claude-conflict-blocked`, `claude-merge` removed.
- `cmd/cc-crew/init.go` creates the five new labels with expected colors.
- `internal/reset` clears the new lock labels.

**Manual integration** (scratch repo)

- Happy path: open issue → implementer opens PR → reviewer approves → merger merges.
- Out-of-date path: land a commit on base after approval → merger calls update-branch → next tick merges.
- Conflict path: create a PR with a conflicting change → merger dispatches resolver → resolver resolves → reviewer re-approves → merger merges.
- Blocked path: create an unresolvable conflict (agent gives up) → `claude-conflict-blocked` + comment appears; no further ticks retry.

## 11. Out of scope (explicit)

- Merge-queue / batching multiple PRs.
- Auto-merging PRs without cc-crew's label history.
- Resolver fixing test regressions beyond conflict markers.
- Squash or merge-commit alternatives.
- Reviewer voting (requiring N approvals) — one reviewer-agent approval is the contract.
- Cross-repo dependency resolution.
