package claim

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charleszheng44/cc-crew/internal/github"
)

const TimestampFormat = "20060102T150405Z"

type Kind int

const (
	KindImplementer Kind = iota
	KindReviewer
	KindAddresser
)

// Paths encodes the ref-name layout for each kind.
type Paths struct {
	LockRef   string // e.g. "refs/heads/claude/issue-42"
	TagPrefix string // e.g. "tags/claim/issue-42/"
}

// PathsFor returns the refs for a given work item.
func PathsFor(k Kind, number int) Paths {
	switch k {
	case KindImplementer:
		return Paths{
			LockRef:   fmt.Sprintf("refs/heads/claude/issue-%d", number),
			TagPrefix: fmt.Sprintf("tags/claim/issue-%d/", number),
		}
	case KindReviewer:
		return Paths{
			LockRef:   fmt.Sprintf("refs/tags/review-lock/pr-%d", number),
			TagPrefix: fmt.Sprintf("tags/review-claim/pr-%d/", number),
		}
	case KindAddresser:
		return Paths{
			LockRef:   fmt.Sprintf("refs/tags/address-lock/pr-%d", number),
			TagPrefix: fmt.Sprintf("tags/address-claim/pr-%d/", number),
		}
	}
	panic("unreachable")
}

// TimestampTagName returns "refs/tags/<TagPrefix><now-UTC>".
func (p Paths) TimestampTagName(now time.Time) string {
	return "refs/" + p.TagPrefix + now.UTC().Format(TimestampFormat)
}

type Claimer struct {
	GH   github.Client
	Repo github.Repo
	Now  func() time.Time // injected for tests; defaults to time.Now
}

func New(gh github.Client, r github.Repo) *Claimer {
	return &Claimer{GH: gh, Repo: r, Now: time.Now}
}

// TryClaim attempts to atomically claim a work item. `sha` is the commit
// SHA the lock ref should point to (for implementer: base branch SHA;
// for reviewer: PR head SHA). Returns (true, nil) on win, (false, nil)
// if another orchestrator already holds the lock, or (false, err) on
// unexpected errors.
func (c *Claimer) TryClaim(ctx context.Context, k Kind, number int, sha string) (bool, error) {
	p := PathsFor(k, number)
	err := c.GH.CreateRef(ctx, c.Repo, p.LockRef, sha)
	if err != nil {
		if errors.Is(err, github.ErrRefExists) {
			return false, nil
		}
		return false, fmt.Errorf("create lock %s: %w", p.LockRef, err)
	}
	// Immediately create the timestamp tag. Any failure here is
	// non-fatal at claim time; reclaim recreates missing tags.
	tag := p.TimestampTagName(c.Now())
	if err := c.GH.CreateRef(ctx, c.Repo, tag, sha); err != nil && !errors.Is(err, github.ErrRefExists) {
		return true, fmt.Errorf("create claim tag %s: %w (lock held)", tag, err)
	}
	return true, nil
}

// Release deletes the timestamp tags and optionally the lock ref.
// For a failed implementer: deleteLock=true (drop branch; work retried).
// For a successful implementer: deleteLock=false (PR references it).
// For a failed reviewer: deleteLock=true.
// For a successful reviewer: deleteLock=true.
func (c *Claimer) Release(ctx context.Context, k Kind, number int, deleteLock bool) error {
	p := PathsFor(k, number)
	tags, err := c.GH.ListMatchingRefs(ctx, c.Repo, p.TagPrefix)
	if err != nil {
		return fmt.Errorf("list tags for release: %w", err)
	}
	for _, t := range tags {
		if err := c.GH.DeleteRef(ctx, c.Repo, t.Name); err != nil {
			return fmt.Errorf("delete tag %s: %w", t.Name, err)
		}
	}
	if deleteLock {
		if err := c.GH.DeleteRef(ctx, c.Repo, p.LockRef); err != nil {
			return fmt.Errorf("delete lock %s: %w", p.LockRef, err)
		}
	}
	return nil
}

// OldestTagAge returns the age of the oldest timestamp tag under the
// paths' prefix, or (0, ok=false) if none exist.
func (c *Claimer) OldestTagAge(ctx context.Context, k Kind, number int) (time.Duration, bool, error) {
	p := PathsFor(k, number)
	tags, err := c.GH.ListMatchingRefs(ctx, c.Repo, p.TagPrefix)
	if err != nil {
		return 0, false, err
	}
	if len(tags) == 0 {
		return 0, false, nil
	}
	// Parse timestamps out of ref names.
	type parsed struct {
		ref string
		t   time.Time
	}
	var ps []parsed
	for _, t := range tags {
		parts := strings.Split(t.Name, "/")
		if len(parts) == 0 {
			continue
		}
		ts, err := time.Parse(TimestampFormat, parts[len(parts)-1])
		if err != nil {
			continue
		}
		ps = append(ps, parsed{ref: t.Name, t: ts})
	}
	if len(ps) == 0 {
		return 0, false, nil
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].t.Before(ps[j].t) })
	return c.Now().Sub(ps[0].t), true, nil
}
