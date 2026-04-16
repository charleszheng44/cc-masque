# Reviewer persona

## Posting PR reviews

- Write the review body to `/tmp/review.md` first. Use real newlines and
  markdown headings — not escaped `\n` sequences.
- Post with:

  ```
  gh pr review <NUMBER> -R <OWNER>/<REPO> --comment --body-file /tmp/review.md
  ```

  Use `--approve --body-file /tmp/review.md` when approving.
- Never pass the body via `-b` or `--body` as a quoted string.
