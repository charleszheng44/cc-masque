package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// Semaphore is a simple bounded semaphore with try-acquire semantics.
type Semaphore struct {
	slots chan struct{}
}

func NewSemaphore(n int) *Semaphore {
	s := &Semaphore{slots: make(chan struct{}, n)}
	for i := 0; i < n; i++ {
		s.slots <- struct{}{}
	}
	return s
}

func (s *Semaphore) TryAcquire() bool {
	select {
	case <-s.slots:
		return true
	default:
		return false
	}
}

func (s *Semaphore) Release() { s.slots <- struct{}{} }

func (s *Semaphore) Free() int { return len(s.slots) }

// Dispatcher runs the per-task lifecycle after a claim is won.
// Implemented by scheduler.Lifecycle in Task 7.2.
type Dispatcher interface {
	Dispatch(ctx context.Context, number int)
}

// Scheduler owns the tick loop for one persona (implementer or reviewer).
type Scheduler struct {
	Kind       claim.Kind
	Sem        *Semaphore
	Claimer    *claim.Claimer
	GH         github.Client
	Repo       github.Repo
	Dispatcher Dispatcher
	Log        *slog.Logger

	QueueLabel string
	LockLabel  string
}

// Tick runs one polling iteration.
func (s *Scheduler) Tick(ctx context.Context) error {
	candidates, err := s.listCandidates(ctx)
	if err != nil {
		return err
	}
	for _, n := range candidates {
		if !s.Sem.TryAcquire() {
			return nil
		}
		won, _, err := s.tryClaimOne(ctx, n)
		if err != nil {
			s.Sem.Release()
			s.Log.Warn("claim error; skipping", "number", n, "err", err)
			continue
		}
		if !won {
			s.Sem.Release()
			continue
		}
		if err := s.GH.AddLabel(ctx, s.Repo, n, s.LockLabel); err != nil {
			s.Log.Warn("add lock label failed (non-fatal)", "number", n, "err", err)
		}
		num := n
		go func() {
			defer s.Sem.Release()
			s.Dispatcher.Dispatch(ctx, num)
		}()
	}
	return nil
}

func (s *Scheduler) listCandidates(ctx context.Context) ([]int, error) {
	switch s.Kind {
	case claim.KindImplementer:
		issues, err := s.GH.ListIssues(ctx, s.Repo, []string{s.QueueLabel}, []string{s.LockLabel})
		if err != nil {
			return nil, err
		}
		nums := make([]int, 0, len(issues))
		for _, i := range issues {
			count, err := s.GH.CountOpenBlockers(ctx, s.Repo, i.Number)
			if err != nil {
				return nil, err
			}
			if count > 0 {
				s.Log.Debug("skipping blocked issue", "number", i.Number, "blockers", count)
				continue
			}
			nums = append(nums, i.Number)
		}
		sortAsc(nums)
		return nums, nil
	case claim.KindReviewer, claim.KindAddresser:
		prs, err := s.GH.ListPRs(ctx, s.Repo, []string{s.QueueLabel}, []string{s.LockLabel})
		if err != nil {
			return nil, err
		}
		nums := make([]int, 0, len(prs))
		for _, p := range prs {
			nums = append(nums, p.Number)
		}
		sortAsc(nums)
		return nums, nil
	}
	return nil, nil
}

func (s *Scheduler) tryClaimOne(ctx context.Context, n int) (bool, string, error) {
	switch s.Kind {
	case claim.KindImplementer:
		defBranch, err := s.GH.DefaultBranch(ctx, s.Repo)
		if err != nil {
			return false, "", err
		}
		ref, err := s.GH.GetRef(ctx, s.Repo, "refs/heads/"+defBranch)
		if err != nil {
			return false, "", err
		}
		won, err := s.Claimer.TryClaim(ctx, claim.KindImplementer, n, ref.SHA)
		return won, ref.SHA, err
	case claim.KindReviewer, claim.KindAddresser:
		pr, err := s.GH.GetPR(ctx, s.Repo, n)
		if err != nil {
			return false, "", err
		}
		won, err := s.Claimer.TryClaim(ctx, s.Kind, n, pr.HeadRefOid)
		return won, pr.HeadRefOid, err
	}
	return false, "", nil
}

func sortAsc(xs []int) {
	for i := 1; i < len(xs); i++ {
		v := xs[i]
		j := i - 1
		for j >= 0 && xs[j] > v {
			xs[j+1] = xs[j]
			j--
		}
		xs[j+1] = v
	}
}

// Run starts the tick loop; blocks until ctx is canceled.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	if err := s.Tick(ctx); err != nil {
		s.Log.Warn("tick error", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := s.Tick(ctx); err != nil {
				s.Log.Warn("tick error", "err", err)
			}
		}
	}
}
