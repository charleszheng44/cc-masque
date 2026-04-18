# cc-crew init — design

Status: approved for planning
Date: 2026-04-18

## 1. Overview

`cc-crew init` creates the GitHub labels the orchestrator relies on. A new
user pointing cc-crew at a fresh repo runs `cc-crew init` once; the command
creates the nine lifecycle labels (implementer, reviewer, addressing) on the
remote, then exits. Re-running is safe: labels that already exist are left
alone and reported as `exists:`.

## 2. Goals

- One-shot, idempotent setup step a user runs before `cc-crew up`.
- Uses the same repo resolution and the same env-var label overrides as the
  existing subcommands (`up`, `status`, `reset`), so a user who customizes
  `CC_TASK_LABEL` etc. gets the right labels created.
- Clear per-label output so the user knows what happened.
- No new external dependencies. Talks to GitHub through the existing
  `github.Client` abstraction.

## 3. Non-goals

- Creating branch protections, webhooks, issue templates, or any other repo
  metadata. Labels only.
- Updating existing labels' color or description. If a label already exists,
  we leave it as-is — we never stomp on what the user has set by hand.
- Removing labels. Cleanup is the job of `cc-crew reset`, which already
  handles the refs/containers/worktrees but deliberately does not touch
  label *definitions* (only their application to issues/PRs).
- A `--dry-run` flag. The operation is already safe and idempotent; the
  output of a real run tells the user everything a dry-run would.

## 4. CLI surface

```
cc-crew init [--repo <path>]
```

- `--repo` defaults to `$CC_REPO` then `$PWD` — identical to `status` and
  `reset`.
- Wired into `cmd/cc-crew/main.go` alongside the existing subcommands, with
  a line added to `usage()`.
- Env-var overrides honored: `CC_TASK_LABEL`, `CC_PROCESSING_LABEL`,
  `CC_DONE_LABEL`, `CC_REVIEW_LABEL`, `CC_REVIEWING_LABEL`,
  `CC_REVIEWED_LABEL`, `CC_ADDRESS_LABEL`, `CC_ADDRESSING_LABEL`,
  `CC_ADDRESSED_LABEL`. Any unset override falls back to `config.Defaults()`.

## 5. `github.Client` addition

One new method on the existing interface (`internal/github/client.go`):

```go
CreateLabel(ctx context.Context, r Repo, name, color, description string) error
```

- Returns a new sentinel `ErrLabelExists` when GitHub responds 422
  `already_exists`. This mirrors the existing `ErrRefExists` contract on
  `CreateRef` — callers switch on the sentinel to distinguish "lost the
  race / already there" from real errors.
- `color` is the 6-char hex string GitHub expects (no leading `#`).
- `description` is capped at GitHub's 100-char limit; the caller is
  responsible for staying under.

### 5.1 `ghClient` implementation (`internal/github/gh.go`)

Same shape as the existing `CreateRef`:

```go
func (c *ghClient) CreateLabel(ctx context.Context, r Repo, name, color, desc string) error {
    body := fmt.Sprintf(`{"name":%q,"color":%q,"description":%q}`, name, color, desc)
    cmd := exec.CommandContext(ctx, c.ghBin, "api", "-X", "POST",
        fmt.Sprintf("repos/%s/labels", r.String()), "--input", "-")
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

### 5.2 `FakeClient` implementation (`internal/github/fake.go`)

Add a `Labels map[string]struct{}` field, initialize in `NewFake`, and
implement `CreateLabel` to return `ErrLabelExists` on duplicate insertion.

## 6. Label catalog

Built at runtime inside `cmd/cc-crew/init.go`, because the names come from
config but the colors and descriptions are canonical:

| Role       | Color    | Description                                            |
|------------|----------|--------------------------------------------------------|
| task       | `1d76db` | Queue an issue for the cc-crew implementer            |
| processing | `0366d6` | Implementer is working on this issue                  |
| done       | `0e8a16` | Implementer opened a PR for this issue                |
| review     | `6f42c1` | Queue a PR for the cc-crew reviewer                   |
| reviewing  | `8a63d2` | Reviewer is working on this PR                        |
| reviewed   | `5319e7` | Reviewer posted a review on this PR                   |
| address    | `d93f0b` | Queue a PR for the implementer to address feedback    |
| addressing | `e99695` | Implementer is addressing review feedback             |
| addressed  | `fbca04` | Implementer pushed updates addressing the review      |

Three visually distinct hue families — implementer = blue/green, reviewer =
purple, addressing = orange/yellow — so a glance at the issues list shows
which lifecycle an item is in.

## 7. Output

Per-label, one line each, fixed-width prefix for scanability:

```
created: claude-task
exists:  claude-processing
created: claude-done
created: claude-review
...
9 labels: 7 created, 2 existed
```

## 8. Error handling

- `ErrLabelExists` → print `exists:  <name>`, continue.
- Successful create → print `created: <name>`, continue.
- Any other error (auth, network, 403) → print the error to stderr and
  exit 1 immediately. The user re-runs `init` after fixing the cause; the
  labels that already succeeded are skipped on the next run.

## 9. Testing

Unit tests against `FakeClient`:

1. Fresh repo (no labels) — expect 9 `created:` lines, summary `9 labels: 9 created, 0 existed`, exit 0.
2. Re-run against an already-initialized repo — expect 9 `exists:` lines, summary `9 labels: 0 created, 9 existed`, exit 0.
3. Mixed (3 labels pre-seeded) — expect 6 `created:` + 3 `exists:`, summary `9 labels: 6 created, 3 existed`.
4. Error path — `FakeClient` with a `CreateLabelHook` that errors on the 3rd call → command exits 1, first two label lines already printed, no summary line.

Custom-name tests: one case sets `CC_TASK_LABEL=foo` and verifies the
created label is `foo`, not `claude-task`.
