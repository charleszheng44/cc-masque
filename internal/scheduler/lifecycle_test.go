package scheduler

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func TestSuccessCleanupImplementer(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	f.Refs["refs/heads/claude/issue-42"] = "sha"
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "sha"

	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	l := &Lifecycle{
		Kind: claim.KindImplementer, Claimer: c, GH: f, Repo: r,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing", DoneLabel: "claude-done",
	}
	l.successCleanup(context.Background(), 42)

	i := f.Issues[42]
	if contains(i.Labels, "claude-task") || contains(i.Labels, "claude-processing") {
		t.Fatalf("queue/lock labels should be gone: %v", i.Labels)
	}
	if !contains(i.Labels, "claude-done") {
		t.Fatalf("done label missing: %v", i.Labels)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock branch should remain for PR reference")
	}
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("timestamp tag should be cleared")
	}
}

func TestFailCleanupDropsLockAndKeepsQueueLabel(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Issues[42] = &github.Issue{Number: 42, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	f.Refs["refs/heads/claude/issue-42"] = "sha"
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "sha"

	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	l := &Lifecycle{
		Kind: claim.KindImplementer, Claimer: c, GH: f, Repo: r,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing", DoneLabel: "claude-done",
	}
	l.failCleanup(context.Background(), 42)

	i := f.Issues[42]
	if !contains(i.Labels, "claude-task") {
		t.Fatalf("queue label should remain for retry: %v", i.Labels)
	}
	if contains(i.Labels, "claude-processing") {
		t.Fatalf("lock label should be gone: %v", i.Labels)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; ok {
		t.Fatal("lock branch should be deleted so work retriggers")
	}
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
