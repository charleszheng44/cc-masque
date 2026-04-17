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
