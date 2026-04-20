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
