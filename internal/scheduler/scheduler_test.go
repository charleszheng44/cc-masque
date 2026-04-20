package scheduler

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func TestSemaphoreAcquireRelease(t *testing.T) {
	s := NewSemaphore(2)
	if !s.TryAcquire() {
		t.Fatal("1st acquire should succeed")
	}
	if !s.TryAcquire() {
		t.Fatal("2nd acquire should succeed")
	}
	if s.TryAcquire() {
		t.Fatal("3rd acquire should fail")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Fatal("after release should succeed")
	}
}

type countingDispatcher struct{ n atomic.Int32 }

func (d *countingDispatcher) Dispatch(ctx context.Context, number int) {
	d.n.Add(1)
	time.Sleep(5 * time.Millisecond)
}

func TestTickClaimsAndDispatches(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Refs["refs/heads/main"] = "basesha"
	f.Issues[1] = &github.Issue{Number: 1, State: "open", Labels: []string{"claude-task"}}
	f.Issues[2] = &github.Issue{Number: 2, State: "open", Labels: []string{"claude-task"}}
	f.Issues[3] = &github.Issue{Number: 3, State: "open", Labels: []string{"claude-task", "claude-processing"}}

	disp := &countingDispatcher{}
	c := claim.New(f, r)
	c.Now = func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }
	s := &Scheduler{
		Kind: claim.KindImplementer, Sem: NewSemaphore(2),
		Claimer: c, GH: f, Repo: r, Dispatcher: disp,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing",
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for disp.n.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if disp.n.Load() != 2 {
		t.Fatalf("want 2 dispatches, got %d", disp.n.Load())
	}
	if _, ok := f.Refs["refs/heads/claude/issue-1"]; !ok {
		t.Fatal("issue 1 lock not created")
	}
	if _, ok := f.Refs["refs/heads/claude/issue-2"]; !ok {
		t.Fatal("issue 2 lock not created")
	}
}

type fakeDispatcher struct {
	mu sync.Mutex
	n  []int
}

func (d *fakeDispatcher) Dispatch(ctx context.Context, number int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.n = append(d.n, number)
}

func (d *fakeDispatcher) calls() []int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]int, len(d.n))
	copy(out, d.n)
	return out
}

func TestListCandidatesSkipsBlockedIssues(t *testing.T) {
	f := github.NewFake()
	r := github.Repo{Owner: "a", Name: "b"}
	f.Refs["refs/heads/main"] = "basesha"
	f.Issues[5] = &github.Issue{Number: 5, State: "open", Labels: []string{"claude-task"}}
	f.Issues[6] = &github.Issue{Number: 6, State: "open", Labels: []string{"claude-task"}}
	f.BlockedBy[6] = 1 // issue 6 is blocked by one open issue

	s := &Scheduler{
		Kind: claim.KindImplementer, Sem: NewSemaphore(2),
		Claimer: claim.New(f, r), GH: f, Repo: r,
		Dispatcher: &countingDispatcher{},
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-task", LockLabel: "claude-processing",
	}
	got, err := s.listCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 5 {
		t.Fatalf("want [5], got %v", got)
	}
}

func TestSchedulerAddresserListsAndClaims(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[7] = &github.PullRequest{
		Number: 7, State: "open", HeadRefName: "claude/issue-7",
		HeadRefOid: "sha-7", Labels: []string{"claude-address"},
	}
	disp := &fakeDispatcher{}
	s := &Scheduler{
		Kind: claim.KindAddresser, Sem: NewSemaphore(1),
		Claimer:    claim.New(f, repo),
		GH:         f,
		Repo:       repo,
		Dispatcher: disp,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-address",
		LockLabel:  "claude-addressing",
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Tick dispatches in a goroutine — wait briefly for it.
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(disp.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := disp.calls()
	if len(got) != 1 || got[0] != 7 {
		t.Fatalf("dispatched calls = %v, want [7]", got)
	}
	if _, ok := f.Refs["refs/cc-crew/address-lock/pr-7"]; !ok {
		t.Fatalf("address-lock not created; refs = %v", keys(f.Refs))
	}
}

func TestSchedulerMergerListsAndClaims(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[11] = &github.PullRequest{
		Number: 11, State: "open", HeadRefOid: "sha11",
		Labels: []string{"claude-merge"},
	}
	f.PRs[12] = &github.PullRequest{
		Number: 12, State: "open", HeadRefOid: "sha12",
		Labels: []string{"claude-merge", "claude-merging"}, // already locked — must be skipped
	}
	disp := &fakeDispatcher{}
	s := &Scheduler{
		Kind: claim.KindMerger, Sem: NewSemaphore(1),
		Claimer: claim.New(f, repo), GH: f, Repo: repo, Dispatcher: disp,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-merge", LockLabel: "claude-merging",
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(disp.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := disp.calls()
	if len(got) != 1 || got[0] != 11 {
		t.Fatalf("dispatched = %v, want [11]", got)
	}
	if _, ok := f.Refs["refs/cc-crew/merge-lock/pr-11"]; !ok {
		t.Fatalf("merge-lock not created; refs = %v", keys(f.Refs))
	}
}

func TestSchedulerResolverListsAndClaims(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[21] = &github.PullRequest{
		Number: 21, State: "open", HeadRefOid: "sha21",
		Labels: []string{"claude-resolve-conflict"},
	}
	disp := &fakeDispatcher{}
	s := &Scheduler{
		Kind: claim.KindResolver, Sem: NewSemaphore(1),
		Claimer: claim.New(f, repo), GH: f, Repo: repo, Dispatcher: disp,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		QueueLabel: "claude-resolve-conflict", LockLabel: "claude-resolving",
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(disp.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := disp.calls()
	if len(got) != 1 || got[0] != 21 {
		t.Fatalf("dispatched = %v, want [21]", got)
	}
	if _, ok := f.Refs["refs/cc-crew/resolve-lock/pr-21"]; !ok {
		t.Fatalf("resolve-lock not created; refs = %v", keys(f.Refs))
	}
}
