package github

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBin writes a script that echoes its args and the contents of stdin.
// Used to make ghClient tests deterministic without a real `gh`.
func fakeBin(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fakes not supported on Windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-gh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunGhPropagatesStderrOnError(t *testing.T) {
	bin := fakeBin(t, `echo "boom" 1>&2
exit 7
`)
	c := newGhClientWithBin(bin)
	_, err := c.runGh(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr not propagated: %v", err)
	}
}

func TestCurrentUserParses(t *testing.T) {
	bin := fakeBin(t, `echo octocat`)
	c := newGhClientWithBin(bin)
	u, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u != "octocat" {
		t.Fatalf("got %q", u)
	}
}

func TestListIssuesParsesAndFiltersWithout(t *testing.T) {
	body := `[
	 {"number":1,"title":"t1","body":"b","state":"open","labels":[{"name":"claude-task"}]},
	 {"number":2,"title":"t2","body":"b","state":"open","labels":[{"name":"claude-task"},{"name":"claude-processing"}]}
	]`
	bin := fakeBin(t, `cat <<'EOF'
`+body+`
EOF`)
	c := newGhClientWithBin(bin)
	got, err := c.ListIssues(context.Background(), Repo{Owner: "a", Name: "b"},
		[]string{"claude-task"}, []string{"claude-processing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("want [1], got %+v", got)
	}
}

func TestCreateRefDetects422AsErrRefExists(t *testing.T) {
	// Fake gh writes "HTTP 422: Reference already exists" to stderr and exits 1.
	bin := fakeBin(t, `echo "HTTP 422: Reference already exists" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.CreateRef(context.Background(), Repo{Owner: "a", Name: "b"},
		"refs/heads/claude/issue-42", "deadbeef")
	if err != ErrRefExists {
		t.Fatalf("want ErrRefExists, got %v", err)
	}
}

func TestDeleteRefTreatsAlreadyDeletedAsSuccess(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 422: Reference does not exist" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	if err := c.DeleteRef(context.Background(), Repo{Owner: "a", Name: "b"},
		"refs/heads/claude/issue-42"); err != nil {
		t.Fatalf("should be nil: %v", err)
	}
}

func TestCreateRefDoesNotMapOtherErrorsToErrRefExists(t *testing.T) {
	// Unrelated 422 (bad SHA) must NOT be mapped to ErrRefExists.
	bin := fakeBin(t, `echo "HTTP 422: Object does not exist" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.CreateRef(context.Background(), Repo{Owner: "a", Name: "b"},
		"refs/heads/claude/issue-42", "badsha")
	if err == nil {
		t.Fatal("expected an error for bad SHA")
	}
	if err == ErrRefExists {
		t.Fatalf("must NOT map unrelated 422 to ErrRefExists: %v", err)
	}
	if !strings.Contains(err.Error(), "Object does not exist") {
		t.Fatalf("error should propagate original stderr: %v", err)
	}
}

func TestDeleteRefDoesNotMapUnrelatedErrorsToSuccess(t *testing.T) {
	// A 422 for a different reason must not become nil.
	bin := fakeBin(t, `echo "HTTP 422: Protected branch" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	if err := c.DeleteRef(context.Background(), Repo{Owner: "a", Name: "b"},
		"refs/heads/protected"); err == nil {
		t.Fatal("expected error for protected branch, got nil")
	}
}

func TestDeleteRefTreatsNotFoundAsSuccess(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 404: Not Found" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	if err := c.DeleteRef(context.Background(), Repo{Owner: "a", Name: "b"},
		"refs/heads/ghost"); err != nil {
		t.Fatalf("404 should be idempotent success: %v", err)
	}
}

func TestListReviewsParses(t *testing.T) {
	body := `[{"user":{"login":"reviewer-bot"},"state":"COMMENTED","submitted_at":"2026-04-17T12:00:00Z"}]`
	bin := fakeBin(t, `cat <<'EOF'
`+body+`
EOF`)
	c := newGhClientWithBin(bin)
	got, err := c.ListReviews(context.Background(), Repo{Owner: "a", Name: "b"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Author != "reviewer-bot" || got[0].State != "COMMENTED" {
		t.Fatalf("got %+v", got)
	}
}

func TestCreateLabelSuccess(t *testing.T) {
	bin := fakeBin(t, `exit 0`)
	c := newGhClientWithBin(bin)
	err := c.CreateLabel(context.Background(), Repo{Owner: "a", Name: "b"},
		"claude-task", "1d76db", "Queue an issue for the implementer")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

// Real gh CLI behavior on a duplicate label: the JSON response body (which
// carries the "already_exists" code) goes to stdout, while stderr only has a
// short "gh: Validation Failed (HTTP 422)" line. CreateLabel must detect the
// already-exists case from either stream.
func TestCreateLabelDetects422AsErrLabelExists(t *testing.T) {
	bin := fakeBin(t, `printf '%s' '{"message":"Validation Failed","errors":[{"resource":"Label","code":"already_exists","field":"name"}],"status":"422"}'
echo "gh: Validation Failed (HTTP 422)" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.CreateLabel(context.Background(), Repo{Owner: "a", Name: "b"},
		"claude-task", "1d76db", "desc")
	if err != ErrLabelExists {
		t.Fatalf("want ErrLabelExists, got %v", err)
	}
}

func TestCreateLabelDoesNotMapOtherErrorsToErrLabelExists(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 403: Forbidden" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.CreateLabel(context.Background(), Repo{Owner: "a", Name: "b"},
		"claude-task", "1d76db", "desc")
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if err == ErrLabelExists {
		t.Fatalf("must NOT map 403 to ErrLabelExists: %v", err)
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("error should propagate stderr: %v", err)
	}
}

func TestGetPRParsesMergeableFields(t *testing.T) {
	body := `{"number":42,"title":"t","body":"b","state":"OPEN","labels":[],"headRefOid":"abc","headRefName":"claude/issue-42","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN"}`
	bin := fakeBin(t, `cat <<'EOF'
`+body+`
EOF`)
	c := newGhClientWithBin(bin)
	got, err := c.GetPR(context.Background(), Repo{Owner: "a", Name: "b"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mergeable != "MERGEABLE" {
		t.Fatalf("Mergeable: want MERGEABLE, got %q", got.Mergeable)
	}
	if got.MergeStateStatus != "CLEAN" {
		t.Fatalf("MergeStateStatus: want CLEAN, got %q", got.MergeStateStatus)
	}
}

func TestListPRsParsesMergeableFields(t *testing.T) {
	body := `[{"number":7,"title":"t","body":"b","state":"open","labels":[],"headRefOid":"abc","headRefName":"h","baseRefName":"main","mergeable":"CONFLICTING","mergeStateStatus":"DIRTY"}]`
	bin := fakeBin(t, `cat <<'EOF'
`+body+`
EOF`)
	c := newGhClientWithBin(bin)
	got, err := c.ListPRs(context.Background(), Repo{Owner: "a", Name: "b"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 PR, got %d", len(got))
	}
	if got[0].Mergeable != "CONFLICTING" || got[0].MergeStateStatus != "DIRTY" {
		t.Fatalf("want CONFLICTING/DIRTY, got %q/%q", got[0].Mergeable, got[0].MergeStateStatus)
	}
}

func TestCountOpenBlockers(t *testing.T) {
	body := `[{"state":"open"},{"state":"closed"},{"state":"open"}]`
	bin := fakeBin(t, `cat <<'EOF'
`+body+`
EOF`)
	c := newGhClientWithBin(bin)
	got, err := c.CountOpenBlockers(context.Background(), Repo{Owner: "a", Name: "b"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("want 2 open blockers, got %d", got)
	}
}

func TestCountOpenBlockersEmpty(t *testing.T) {
	bin := fakeBin(t, `echo '[]'`)
	c := newGhClientWithBin(bin)
	got, err := c.CountOpenBlockers(context.Background(), Repo{Owner: "a", Name: "b"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("want 0 blockers, got %d", got)
	}
}

func TestMergePRSuccess(t *testing.T) {
	// Capture args to verify --rebase / --delete-branch / --match-head-commit.
	bin := fakeBin(t, `echo "$@" >"$(dirname "$0")/args"
exit 0
`)
	c := newGhClientWithBin(bin)
	err := c.MergePR(context.Background(), Repo{Owner: "a", Name: "b"}, 42,
		"deadbeef", MergeMethodRebase, true)
	if err != nil {
		t.Fatalf("MergePR: %v", err)
	}
	argsFile := filepath.Join(filepath.Dir(bin), "args")
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"pr", "merge", "42", "-R", "a/b", "--rebase", "--delete-branch", "--match-head-commit", "deadbeef"} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q: %s", want, got)
		}
	}
}

func TestMergePRMapsConflictStderrToErrMergeConflict(t *testing.T) {
	cases := []string{
		`echo "failed to merge: merge conflict between base and head" 1>&2
exit 1
`,
		`echo "Pull request is not mergeable: the merge commit cannot be cleanly created" 1>&2
exit 1
`,
		`echo "GraphQL: Pull request is not in a mergeable state (mergePullRequest)" 1>&2
exit 1
`,
	}
	for i, body := range cases {
		bin := fakeBin(t, body)
		c := newGhClientWithBin(bin)
		err := c.MergePR(context.Background(), Repo{Owner: "a", Name: "b"}, 1,
			"sha", MergeMethodRebase, true)
		if err != ErrMergeConflict {
			t.Errorf("case %d: want ErrMergeConflict, got %v", i, err)
		}
	}
}

func TestMergePRDoesNotMapUnrelatedErrorsToErrMergeConflict(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 403: Resource not accessible by integration" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.MergePR(context.Background(), Repo{Owner: "a", Name: "b"}, 1,
		"sha", MergeMethodRebase, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if err == ErrMergeConflict {
		t.Fatalf("must NOT map 403 to ErrMergeConflict: %v", err)
	}
	if !strings.Contains(err.Error(), "Resource not accessible") {
		t.Fatalf("stderr should be propagated: %v", err)
	}
}

func TestMergePRRejectsUnknownMethod(t *testing.T) {
	bin := fakeBin(t, `exit 0`)
	c := newGhClientWithBin(bin)
	err := c.MergePR(context.Background(), Repo{Owner: "a", Name: "b"}, 1,
		"", MergeMethod("bogus"), false)
	if err == nil {
		t.Fatal("expected error for unknown merge method")
	}
	if !strings.Contains(err.Error(), "unknown merge method") {
		t.Fatalf("want unknown-method error, got %v", err)
	}
}

func TestUpdateBranchSendsExpectedHeadSha(t *testing.T) {
	// Capture stdin so we can verify the JSON body.
	bin := fakeBin(t, `cat - >"$(dirname "$0")/stdin"
exit 0
`)
	c := newGhClientWithBin(bin)
	err := c.UpdateBranch(context.Background(), Repo{Owner: "a", Name: "b"}, 42,
		"deadbeef", UpdateMethodRebase)
	if err != nil {
		t.Fatalf("UpdateBranch: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"expected_head_sha":"deadbeef"`) {
		t.Errorf("body missing expected_head_sha: %s", body)
	}
	if !strings.Contains(body, `"update_method":"rebase"`) {
		t.Errorf("body missing update_method: %s", body)
	}
}

func TestUpdateBranchPropagatesError(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 422: Validation failed" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.UpdateBranch(context.Background(), Repo{Owner: "a", Name: "b"}, 42,
		"deadbeef", UpdateMethodRebase)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Validation failed") {
		t.Fatalf("stderr should be propagated: %v", err)
	}
}

func TestGetCheckRunsParses(t *testing.T) {
	body := `{"total_count":2,"check_runs":[
	  {"name":"build","status":"completed","conclusion":"success"},
	  {"name":"lint","status":"in_progress","conclusion":null}
	]}`
	bin := fakeBin(t, `cat <<'EOF'
`+body+`
EOF`)
	c := newGhClientWithBin(bin)
	got, err := c.GetCheckRuns(context.Background(), Repo{Owner: "a", Name: "b"}, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 runs, got %d", len(got))
	}
	if got[0].Name != "build" || got[0].Status != "completed" || got[0].Conclusion != "success" {
		t.Errorf("runs[0] = %+v", got[0])
	}
	if got[1].Name != "lint" || got[1].Conclusion != "" {
		t.Errorf("runs[1] = %+v", got[1])
	}
}

func TestGetCheckRunsEmpty(t *testing.T) {
	bin := fakeBin(t, `echo '{"total_count":0,"check_runs":[]}'`)
	c := newGhClientWithBin(bin)
	got, err := c.GetCheckRuns(context.Background(), Repo{Owner: "a", Name: "b"}, "sha")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestGetCheckRunsPropagatesError(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 404: Not Found" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	_, err := c.GetCheckRuns(context.Background(), Repo{Owner: "a", Name: "b"}, "sha")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("stderr should be propagated: %v", err)
	}
}

func TestCreateCommentSendsBody(t *testing.T) {
	bin := fakeBin(t, `cat - >"$(dirname "$0")/stdin"
exit 0
`)
	c := newGhClientWithBin(bin)
	err := c.CreateComment(context.Background(), Repo{Owner: "a", Name: "b"}, 42, "hello world")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"body":"hello world"`) {
		t.Errorf("stdin missing body: %s", string(raw))
	}
}

func TestCreateCommentPropagatesError(t *testing.T) {
	bin := fakeBin(t, `echo "HTTP 403: Forbidden" 1>&2
exit 1
`)
	c := newGhClientWithBin(bin)
	err := c.CreateComment(context.Background(), Repo{Owner: "a", Name: "b"}, 42, "body")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("stderr should be propagated: %v", err)
	}
}
