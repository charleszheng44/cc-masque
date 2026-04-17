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
