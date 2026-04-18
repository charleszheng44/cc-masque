package reclaim

import (
	"context"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func mkSweeper(f *github.FakeClient, maxAge time.Duration, isDone AlreadyDone, now time.Time) *Sweeper {
	r := github.Repo{Owner: "a", Name: "b"}
	c := claim.New(f, r)
	c.Now = fixedNow(now)
	return &Sweeper{
		GH: f, Repo: r, Claimer: c,
		Kind: claim.KindImplementer, MaxAge: maxAge,
		IsDone: isDone, Now: fixedNow(now),
	}
}

func TestSweepOrphanLockRecreatesTag(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "abc"
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	s := mkSweeper(f, 30*time.Minute, nil, now)
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	expected := "refs/cc-crew/claim/issue-42/20260417T120000Z"
	if _, ok := f.Refs[expected]; !ok {
		t.Fatalf("expected orphan lock to get a timestamp tag %s; refs=%v", expected, f.Refs)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock should still exist")
	}
}

func TestSweepLeavesFreshLocks(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "abc"
	f.Refs["refs/cc-crew/claim/issue-42/20260417T115900Z"] = "abc" // 1 min old
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	s := mkSweeper(f, 30*time.Minute, nil, now)
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock should survive")
	}
}

func TestSweepReapsStaleLocks(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "abc"
	f.Refs["refs/cc-crew/claim/issue-42/20260417T110000Z"] = "abc" // 1 hour old
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	s := mkSweeper(f, 30*time.Minute, nil, now)
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; ok {
		t.Fatal("lock should be reaped")
	}
	if _, ok := f.Refs["refs/cc-crew/claim/issue-42/20260417T110000Z"]; ok {
		t.Fatal("timestamp tag should be reaped")
	}
}

func TestSweepSkipsAlreadyDone(t *testing.T) {
	f := github.NewFake()
	f.Refs["refs/heads/claude/issue-42"] = "abc"
	f.Refs["refs/cc-crew/claim/issue-42/20260417T110000Z"] = "abc" // 1 hour old
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	isDone := func(ctx context.Context, n int) (bool, error) { return true, nil }
	s := mkSweeper(f, 30*time.Minute, isDone, now)
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/heads/claude/issue-42"]; !ok {
		t.Fatal("lock should be preserved — work is already done")
	}
}

func TestSweeperReapsStaleAddresserLock(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	old := time.Now().Add(-35 * time.Minute).UTC().Format(claim.TimestampFormat)
	f.Refs["refs/cc-crew/address-lock/pr-99"] = "sha"
	f.Refs["refs/cc-crew/address-claim/pr-99/"+old] = "sha"

	sw := &Sweeper{
		GH: f, Repo: repo, Claimer: claim.New(f, repo),
		Kind:   claim.KindAddresser,
		MaxAge: 30 * time.Minute,
		IsDone: func(ctx context.Context, n int) (bool, error) { return false, nil },
		Now:    time.Now,
	}
	if err := sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-99"]; ok {
		t.Fatalf("stale address-lock not reaped; remaining refs = %v", f.Refs)
	}
}
