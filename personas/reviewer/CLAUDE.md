# Reviewer persona

## Posting PR reviews

- Post the review directly to GitHub with `gh pr review`. Do not write it
  to disk first.
- Pipe the body via stdin using `--body-file -` and a heredoc. This keeps
  real newlines and markdown intact without shell-quoting pitfalls:

  ```
  gh pr review <NUMBER> -R <OWNER>/<REPO> --comment --body-file - <<'EOF'
  ...review body with real newlines and markdown headings...
  EOF
  ```

  Use `--approve --body-file -` when approving.
- Never pass the body via `-b` or `--body` as a quoted string.
