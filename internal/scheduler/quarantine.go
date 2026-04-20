package scheduler

import (
	"sync"

	"github.com/charleszheng44/cc-crew/internal/claim"
)

// Quarantine tracks consecutive dispatch failures per (kind, number) so the
// scheduler can stop claiming an item that keeps failing early (bad token,
// missing binary, persistent worktree error, etc.). After Threshold
// consecutive failures the caller is expected to add Label to the
// issue/PR — from then on the listCandidates filter excludes it, stopping
// the lock-label flapping described in issue #43.
//
// State is in-memory and not persisted across orchestrator restarts — that
// is out of scope for this issue. Restart clears the counter, but the label
// survives on GitHub, so the filter still holds until a human removes it.
type Quarantine struct {
	Threshold int

	mu      sync.Mutex
	counts  map[quarantineKey]int
	lastErr map[quarantineKey]string
	labeled map[quarantineKey]bool
}

type quarantineKey struct {
	kind   claim.Kind
	number int
}

// NewQuarantine returns a tracker that fires once count >= threshold. A
// threshold of 0 or less disables quarantine entirely (RecordFailure never
// reports shouldLabel=true).
func NewQuarantine(threshold int) *Quarantine {
	return &Quarantine{
		Threshold: threshold,
		counts:    map[quarantineKey]int{},
		lastErr:   map[quarantineKey]string{},
		labeled:   map[quarantineKey]bool{},
	}
}

// RecordFailure increments the counter for (kind, number). reason is stored
// as the most recent error so the caller can include it in a quarantine
// comment. Returns (count, shouldLabel): shouldLabel is true on the first
// call that crosses the threshold, false thereafter — keeping the label +
// comment application to exactly once per quarantine streak.
func (q *Quarantine) RecordFailure(k claim.Kind, number int, reason string) (int, bool) {
	if q == nil || q.Threshold <= 0 {
		return 0, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	key := quarantineKey{k, number}
	q.counts[key]++
	if reason != "" {
		q.lastErr[key] = reason
	}
	if q.counts[key] >= q.Threshold && !q.labeled[key] {
		q.labeled[key] = true
		return q.counts[key], true
	}
	return q.counts[key], false
}

// RecordSuccess clears all state for (kind, number). Used when a dispatch
// completes successfully so flaky-but-recoverable failures don't accumulate
// toward a false quarantine.
func (q *Quarantine) RecordSuccess(k claim.Kind, number int) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	key := quarantineKey{k, number}
	delete(q.counts, key)
	delete(q.lastErr, key)
	delete(q.labeled, key)
}

// LastErr returns the most recent reason recorded for (kind, number), or ""
// if none.
func (q *Quarantine) LastErr(k claim.Kind, number int) string {
	if q == nil {
		return ""
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.lastErr[quarantineKey{k, number}]
}

// Count returns the current consecutive-failure count for (kind, number).
func (q *Quarantine) Count(k claim.Kind, number int) int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.counts[quarantineKey{k, number}]
}

// WasLabeled reports whether we have already applied the quarantine label
// for the current streak. The scheduler uses this to detect "candidate
// reappeared despite previous quarantine" — i.e. a human removed the label
// — and resets state so a new quarantine window starts from zero.
func (q *Quarantine) WasLabeled(k claim.Kind, number int) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.labeled[quarantineKey{k, number}]
}
