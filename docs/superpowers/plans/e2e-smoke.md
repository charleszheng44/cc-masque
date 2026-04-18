# cc-crew end-to-end smoke test

Pre-reqs: scratch GitHub repo you own, Docker installed, `gh auth login` done,
Claude Code auth working locally.

## 0. Prep

\`\`\`bash
gh repo create your-handle/cc-crew-smoke --private --clone
cd cc-crew-smoke

for L in claude-task claude-processing claude-done \
         claude-review claude-reviewing claude-reviewed; do
  gh label create "$L" --force
done

git commit --allow-empty -m "seed"
git push -u origin main
\`\`\`

## 1. Build & start

In one terminal:

\`\`\`bash
cd /path/to/cc-crew
make build
export GH_TOKEN=...  GH_TOKEN_IMPLEMENTER=... GH_TOKEN_REVIEWER=...
export CLAUDE_CODE_OAUTH_TOKEN=...
export IMPLEMENTER_GIT_NAME=impl-bot  IMPLEMENTER_GIT_EMAIL=impl@example.com
export REVIEWER_GIT_NAME=rev-bot      REVIEWER_GIT_EMAIL=rev@example.com
cd /path/to/cc-crew-smoke
/path/to/cc-crew/cc-crew up --max-implementers 1 --max-reviewers 1
\`\`\`

## 2. File an issue

\`\`\`bash
gh issue create --title "add HELLO.md with greeting" \
  --body "Create a file HELLO.md containing 'Hello, world!'" \
  --label claude-task
\`\`\`

Expected within ~60s:
- Orchestrator logs a claim on issue #1, creates `refs/heads/claude/issue-1`
- Container starts: `docker ps` shows `cc-crew-impl-...-1`
- After exit: PR opened against main, labels: `claude-done`

## 3. Label the PR for review

\`\`\`bash
gh pr edit 2 --add-label claude-review
\`\`\`

Expected within ~60s:
- Orchestrator claims the PR, creates `refs/tags/review-lock/pr-2`
- Reviewer container runs
- A review is posted on the PR
- Labels: `claude-reviewed`

## 4. Reset

\`\`\`bash
/path/to/cc-crew/cc-crew reset            # dry run
/path/to/cc-crew/cc-crew reset --yes       # actually clean
gh api repos/your-handle/cc-crew-smoke/git/matching-refs/tags/claim/ -q length
# → 0
\`\`\`

## 5. Continuous addressing (requires feature build)

Assumes §1–§3 finished: an implementer opened PR #X against `main`,
labeled `claude-reviewed` (after reviewer ran in §3).

### 5.1 Re-review on push

Push a trivial commit directly to the PR branch:

\`\`\`bash
cd .claude-worktrees/issue-<N>            # or a fresh checkout of claude/issue-<N>
echo "# note" >> HELLO.md
git add HELLO.md && git commit -m "tweak"
git push origin HEAD
\`\`\`

Expected within ~1 tick:
- Orchestrator log: `continuous  reviews_flipped=1 address_labeled=0`
- PR labels flip `claude-reviewed` → `claude-review`
- Reviewer claims the PR again
- New review posted; PR ends back at `claude-reviewed`
- New marker: `refs/tags/cc-crew-rereviewed/pr-X/<new-sha>`

### 5.2 Address a non-approval review

Post a top-level non-approval review by hand:

\`\`\`bash
gh pr review X -R <owner/repo> --comment --body-file - <<'EOF'
Please rename HELLO.md to GREETING.md and keep the same contents.
EOF
\`\`\`

Expected within ~1 tick:
- Orchestrator log: `continuous ... address_labeled=1`
- PR gains `claude-address`
- Address-scheduler claims: `refs/tags/address-lock/pr-X` + timestamp tag
- Container: `cc-crew-addr-<owner>-<repo>-X` runs, streams Claude turns
- On success: `claude-address`/`claude-addressing` removed, `claude-addressed` added
- Marker: `refs/tags/cc-crew-addressed/pr-X/<review-id>`
- New commits pushed to `claude/issue-<N>` → triggers §5.1 loop automatically

### 5.3 Cycle cap

Re-run §5.2 three times total; the fourth non-approval review should
NOT cause `claude-address` to be applied (detector sees 3 addressed
markers, skips). Confirm with:

\`\`\`bash
./cc-crew status        # shows cycles 3/3 for that PR
\`\`\`

To reset cycles:

\`\`\`bash
gh api repos/<owner/repo>/git/matching-refs/tags/cc-crew-addressed/pr-X --jq '.[].ref' | \
  xargs -I{} gh api -X DELETE 'repos/<owner/repo>/git/refs/{}' --raw
\`\`\`

### 5.4 Reset covers the new state

\`\`\`bash
./cc-crew reset            # dry run — plan should include the PR
./cc-crew reset --yes
gh api repos/<owner/repo>/git/matching-refs/tags/address-lock/ --jq length  # 0
gh api repos/<owner/repo>/git/matching-refs/tags/cc-crew-addressed/ --jq length  # 0
gh api repos/<owner/repo>/git/matching-refs/tags/cc-crew-rereviewed/ --jq length  # 0
\`\`\`
