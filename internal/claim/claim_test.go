package claim

import (
	"context"
	"strings"
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
	if _, ok := f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"]; !ok {
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
	if _, ok := f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("unexpected timestamp tag")
	}
}

func TestReleaseDeletesTagsAndOptionallyLock(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "x"
	f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"] = "x"
	c := New(f, github.Repo{Owner: "a", Name: "b"})
	if err := c.Release(context.Background(), KindImplementer, 42, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"]; ok {
		t.Fatal("timestamp tag should be gone")
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock branch should remain (deleteLock=false)")
	}
}

func TestOldestTagAge(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/cc-crew/claim/issue-42/20260417T120000Z"] = "x"
	f.Refs["refs/cc-crew/claim/issue-42/20260417T120500Z"] = "x"
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

func TestPathsForAddresser(t *testing.T) {
	p := PathsFor(KindAddresser, 42)
	if p.LockRef != "refs/cc-crew/address-lock/pr-42" {
		t.Fatalf("LockRef = %q", p.LockRef)
	}
	if p.RefPrefix != "cc-crew/address-claim/pr-42/" {
		t.Fatalf("RefPrefix = %q", p.RefPrefix)
	}
}

func TestTryClaimAddresser(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	c := New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }

	won, err := c.TryClaim(context.Background(), KindAddresser, 42, "sha-head")
	if err != nil || !won {
		t.Fatalf("first claim: won=%v err=%v", won, err)
	}
	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-42"]; !ok {
		t.Fatal("address-lock ref not created")
	}
	if _, ok := f.Refs["refs/cc-crew/address-claim/pr-42/20260417T120000Z"]; !ok {
		t.Fatal("address-claim timestamp ref not created")
	}

	// Second orchestrator racing — lose.
	won2, err := c.TryClaim(context.Background(), KindAddresser, 42, "sha-head")
	if err != nil || won2 {
		t.Fatalf("second claim: won=%v err=%v (expected lost)", won2, err)
	}
}

func TestReleaseAddresserDeletesBothTags(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	c := New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }

	_, _ = c.TryClaim(context.Background(), KindAddresser, 7, "sha")
	if err := c.Release(context.Background(), KindAddresser, 7, true); err != nil {
		t.Fatal(err)
	}
	for ref := range f.Refs {
		if strings.Contains(ref, "address-lock/pr-7") || strings.Contains(ref, "address-claim/pr-7") {
			t.Fatalf("ref still present after release: %s", ref)
		}
	}
}
