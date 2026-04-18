package reclaim

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

// AlreadyDone reports whether the work for a given item has already
// been completed — so reclaim should NOT reap it.
type AlreadyDone func(ctx context.Context, number int) (bool, error)

// Sweeper reclaims stale locks for a given Kind.
type Sweeper struct {
	GH      github.Client
	Repo    github.Repo
	Claimer *claim.Claimer
	Kind    claim.Kind
	MaxAge  time.Duration
	IsDone  AlreadyDone // may be nil
	Now     func() time.Time
}

// Sweep walks all lock refs for the sweeper's kind and reclaims those
// whose oldest timestamp tag is older than MaxAge and which are not
// "already done".
func (s *Sweeper) Sweep(ctx context.Context) error {
	locks, err := s.listLockRefs(ctx)
	if err != nil {
		return err
	}
	for _, lr := range locks {
		num, ok := parseNumber(lr.Name, s.Kind)
		if !ok {
			continue
		}
		age, haveTag, err := s.Claimer.OldestTagAge(ctx, s.Kind, num)
		if err != nil {
			return err
		}
		if !haveTag {
			// Orphan lock: create a timestamp tag now so the window
			// is well-defined. Idempotent (422 means someone else
			// created it first).
			tag := claim.PathsFor(s.Kind, num).TimestampTagName(s.Now())
			if err := s.GH.CreateRef(ctx, s.Repo, tag, lr.SHA); err != nil && !errors.Is(err, github.ErrRefExists) {
				return fmt.Errorf("recreate timestamp tag %s: %w", tag, err)
			}
			continue
		}
		if age < s.MaxAge {
			continue
		}
		if s.IsDone != nil {
			done, err := s.IsDone(ctx, num)
			if err != nil {
				return err
			}
			if done {
				continue
			}
		}
		if err := s.Claimer.Release(ctx, s.Kind, num, true); err != nil {
			return fmt.Errorf("reclaim %d: %w", num, err)
		}
	}
	return nil
}

func (s *Sweeper) listLockRefs(ctx context.Context) ([]github.Ref, error) {
	switch s.Kind {
	case claim.KindImplementer:
		return s.GH.ListMatchingRefs(ctx, s.Repo, "heads/claude/issue-")
	case claim.KindReviewer:
		return s.GH.ListMatchingRefs(ctx, s.Repo, "cc-crew/review-lock/pr-")
	case claim.KindAddresser:
		return s.GH.ListMatchingRefs(ctx, s.Repo, "cc-crew/address-lock/pr-")
	}
	return nil, fmt.Errorf("unknown kind %d", s.Kind)
}

func parseNumber(refName string, k claim.Kind) (int, bool) {
	var prefix string
	switch k {
	case claim.KindImplementer:
		prefix = "refs/heads/claude/issue-"
	case claim.KindReviewer:
		prefix = "refs/cc-crew/review-lock/pr-"
	case claim.KindAddresser:
		prefix = "refs/cc-crew/address-lock/pr-"
	}
	s := strings.TrimPrefix(refName, prefix)
	if s == refName {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ImplementerDoneFn returns an AlreadyDone that returns true when a PR
// exists for the branch refs/heads/claude/issue-<N>.
func ImplementerDoneFn(gh github.Client, r github.Repo) AlreadyDone {
	return func(ctx context.Context, n int) (bool, error) {
		prs, err := gh.ListPRs(ctx, r, nil, nil)
		if err != nil {
			return false, err
		}
		target := fmt.Sprintf("claude/issue-%d", n)
		for _, p := range prs {
			if p.HeadRefName == target {
				return true, nil
			}
		}
		return false, nil
	}
}

// ReviewerDoneFn returns an AlreadyDone that returns true when the
// reviewer persona's login has already posted a review on PR <N>.
func ReviewerDoneFn(gh github.Client, r github.Repo, reviewerLogin string) AlreadyDone {
	return func(ctx context.Context, n int) (bool, error) {
		reviews, err := gh.ListReviews(ctx, r, n)
		if err != nil {
			return false, err
		}
		for _, rv := range reviews {
			if rv.Author == reviewerLogin {
				return true, nil
			}
		}
		return false, nil
	}
}
