package claim

import (
	"context"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/github"
)

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestTryClaimWinsOnEmptyRepo(t *testing.T) {
	f := github.NewFake()
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	c.Now = fixedNow(time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC))
	won, err := c.TryClaim(context.Background(), KindImplementer, 42, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !won {
		t.Fatal("expected to win claim")
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock branch not created")
	}
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; !ok {
		t.Fatal("timestamp tag not created")
	}
}

func TestTryClaimLosesWhenLockExists(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "existing"
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	c.Now = fixedNow(time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC))
	won, err := c.TryClaim(context.Background(), KindImplementer, 42, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("should have lost")
	}
	// We should NOT have created a timestamp tag when we lost.
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("unexpected timestamp tag")
	}
}

func TestReleaseDeletesTagsAndOptionallyLock(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "x"
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "x"
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	if err := c.Release(context.Background(), KindImplementer, 42, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/tags/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("timestamp tag should be gone")
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock branch should remain (deleteLock=false)")
	}
}

func TestOldestTagAge(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/tags/claim/issue-42/20260417T120000Z"] = "x"
	f.Refs["refs/tags/claim/issue-42/20260417T120500Z"] = "x"
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	c.Now = fixedNow(time.Date(2026, 4, 17, 12, 30, 0, 0, time.UTC))
	age, ok, err := c.OldestTagAge(context.Background(), KindImplementer, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if age != 30*time.Minute {
		t.Fatalf("got %v", age)
	}
}
