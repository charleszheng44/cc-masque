package continuous

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charleszheng44/cc-crew/internal/github"
)

// Labels names the labels the detector reads and writes.
type Labels struct {
	Review     string
	Reviewing  string
	Reviewed   string
	Address    string
	Addressing string
}

// Options configures one call to Detect.
type Options struct {
	GH        github.Client
	Repo      github.Repo
	MaxCycles int
	Labels    Labels
}

// Result reports what Detect did this tick (for logging).
type Result struct {
	ReviewFlipped  int // PRs whose claude-reviewed was replaced with claude-review
	AddressLabeled int // PRs that gained claude-address
}

// States that count as "unaddressed non-approval reviews".
var triggerStates = map[string]bool{
	"COMMENTED":         true,
	"CHANGES_REQUESTED": true,
}

// Detect examines open PRs on claude/issue-* branches and applies labels to
// enqueue address or re-review work. It NEVER dispatches directly; the
// existing scheduler claims the labels on subsequent ticks.
func Detect(ctx context.Context, o Options) (Result, error) {
	var r Result
	prs, err := o.GH.ListPRs(ctx, o.Repo, nil, nil)
	if err != nil {
		return r, fmt.Errorf("continuous: list PRs: %w", err)
	}

	for _, pr := range prs {
		if pr.State != "open" {
			continue
		}
		if !strings.HasPrefix(pr.HeadRefName, "claude/issue-") {
			continue
		}

		flipped, err := maybeFlipToReview(ctx, o, pr)
		if err != nil {
			return r, err
		}
		if flipped {
			r.ReviewFlipped++
		}

		labeled, err := maybeLabelAddress(ctx, o, pr)
		if err != nil {
			return r, err
		}
		if labeled {
			r.AddressLabeled++
		}
	}
	return r, nil
}

func maybeFlipToReview(ctx context.Context, o Options, pr github.PullRequest) (bool, error) {
	if !has(pr.Labels, o.Labels.Reviewed) {
		return false, nil
	}
	if has(pr.Labels, o.Labels.Review) || has(pr.Labels, o.Labels.Reviewing) {
		return false, nil
	}
	prefix := fmt.Sprintf("tags/cc-crew-rereviewed/pr-%d/", pr.Number)
	refs, err := o.GH.ListMatchingRefs(ctx, o.Repo, prefix)
	if err != nil {
		return false, fmt.Errorf("continuous: list rereviewed refs pr-%d: %w", pr.Number, err)
	}
	for _, ref := range refs {
		if strings.HasSuffix(ref.Name, "/"+pr.HeadRefOid) {
			return false, nil
		}
	}
	if err := o.GH.RemoveLabel(ctx, o.Repo, pr.Number, o.Labels.Reviewed); err != nil {
		return false, fmt.Errorf("continuous: remove reviewed pr-%d: %w", pr.Number, err)
	}
	if err := o.GH.AddLabel(ctx, o.Repo, pr.Number, o.Labels.Review); err != nil {
		return false, fmt.Errorf("continuous: add review pr-%d: %w", pr.Number, err)
	}
	return true, nil
}

func maybeLabelAddress(ctx context.Context, o Options, pr github.PullRequest) (bool, error) {
	if has(pr.Labels, o.Labels.Address) || has(pr.Labels, o.Labels.Addressing) {
		return false, nil
	}
	addressed, err := addressedReviewIDs(ctx, o.GH, o.Repo, pr.Number)
	if err != nil {
		return false, err
	}
	if o.MaxCycles > 0 && len(addressed) >= o.MaxCycles {
		return false, nil
	}
	reviews, err := o.GH.ListReviews(ctx, o.Repo, pr.Number)
	if err != nil {
		return false, fmt.Errorf("continuous: list reviews pr-%d: %w", pr.Number, err)
	}
	hasUnaddressed := false
	for _, rv := range reviews {
		if !triggerStates[rv.State] {
			continue
		}
		if _, seen := addressed[rv.ID]; seen {
			continue
		}
		hasUnaddressed = true
		break
	}
	if !hasUnaddressed {
		return false, nil
	}
	if err := o.GH.AddLabel(ctx, o.Repo, pr.Number, o.Labels.Address); err != nil {
		return false, fmt.Errorf("continuous: add address pr-%d: %w", pr.Number, err)
	}
	return true, nil
}

func addressedReviewIDs(ctx context.Context, gh github.Client, repo github.Repo, pr int) (map[int]struct{}, error) {
	prefix := fmt.Sprintf("tags/cc-crew-addressed/pr-%d/", pr)
	refs, err := gh.ListMatchingRefs(ctx, repo, prefix)
	if err != nil {
		return nil, fmt.Errorf("continuous: list addressed refs pr-%d: %w", pr, err)
	}
	out := make(map[int]struct{}, len(refs))
	for _, ref := range refs {
		parts := strings.Split(ref.Name, "/")
		if len(parts) == 0 {
			continue
		}
		id, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil {
			continue
		}
		out[id] = struct{}{}
	}
	return out, nil
}

func has(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
