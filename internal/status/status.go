package status

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
)

type Item struct {
	Kind         claim.Kind
	Number       int
	Title        string
	State        string // "queued", "claimed", "running", "done"
	ContainerAge time.Duration
	ClaimAge     time.Duration
}

type ContinuousItem struct {
	PRNumber          int
	HeadSHA           string
	LastRereviewedSHA string
	AddressedCycles   int
	MaxCycles         int
	PendingReviews    int
}

type Snapshot struct {
	Implementers []Item
	Reviewers    []Item
	Continuous   []ContinuousItem
}

type Options struct {
	GH     github.Client
	Docker *docker.Runner
	Repo   github.Repo

	TaskLabel       string
	ProcessingLabel string
	ReviewLabel     string
	ReviewingLabel  string

	Now func() time.Time

	ContinuousEnabled bool
	MaxCycles         int
}

func Fetch(ctx context.Context, o Options) (Snapshot, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	var s Snapshot

	issues, err := o.GH.ListIssues(ctx, o.Repo, []string{o.TaskLabel}, nil)
	if err != nil {
		return s, err
	}
	for _, i := range issues {
		it := Item{Kind: claim.KindImplementer, Number: i.Number, Title: i.Title, State: "queued"}
		if containsLabel(i.Labels, o.ProcessingLabel) {
			it.State = "claimed"
		}
		s.Implementers = append(s.Implementers, it)
	}

	prs, err := o.GH.ListPRs(ctx, o.Repo, []string{o.ReviewLabel}, nil)
	if err != nil {
		return s, err
	}
	for _, p := range prs {
		it := Item{Kind: claim.KindReviewer, Number: p.Number, Title: p.Title, State: "queued"}
		if containsLabel(p.Labels, o.ReviewingLabel) {
			it.State = "claimed"
		}
		s.Reviewers = append(s.Reviewers, it)
	}

	c := claim.New(o.GH, o.Repo)
	c.Now = o.Now
	for i := range s.Implementers {
		if s.Implementers[i].State != "claimed" {
			continue
		}
		age, ok, err := c.OldestTagAge(ctx, claim.KindImplementer, s.Implementers[i].Number)
		if err == nil && ok {
			s.Implementers[i].ClaimAge = age
		}
	}
	for i := range s.Reviewers {
		if s.Reviewers[i].State != "claimed" {
			continue
		}
		age, ok, err := c.OldestTagAge(ctx, claim.KindReviewer, s.Reviewers[i].Number)
		if err == nil && ok {
			s.Reviewers[i].ClaimAge = age
		}
	}

	if o.Docker != nil {
		entries, err := o.Docker.PS(ctx, map[string]string{"cc-crew.repo": o.Repo.String()})
		if err == nil {
			for _, e := range entries {
				num := 0
				if v := e.Labels["cc-crew.issue"]; v != "" {
					fmt.Sscanf(v, "%d", &num)
				}
				if num == 0 {
					if v := e.Labels["cc-crew.pr"]; v != "" {
						fmt.Sscanf(v, "%d", &num)
					}
				}
				if num == 0 {
					continue
				}
				role := e.Labels["cc-crew.role"]
				if role == "implementer" {
					for i := range s.Implementers {
						if s.Implementers[i].Number == num {
							s.Implementers[i].State = "running"
						}
					}
				} else if role == "reviewer" {
					for i := range s.Reviewers {
						if s.Reviewers[i].Number == num {
							s.Reviewers[i].State = "running"
						}
					}
				}
			}
		}
	}

	if o.ContinuousEnabled {
		allPRs, err := o.GH.ListPRs(ctx, o.Repo, nil, nil)
		if err != nil {
			return s, err
		}
		for _, pr := range allPRs {
			if pr.State != "open" || !strings.HasPrefix(pr.HeadRefName, "claude/issue-") {
				continue
			}
			item := ContinuousItem{
				PRNumber:  pr.Number,
				HeadSHA:   pr.HeadRefOid,
				MaxCycles: o.MaxCycles,
			}

			rRefs, _ := o.GH.ListMatchingRefs(ctx, o.Repo, fmt.Sprintf("tags/cc-crew-rereviewed/pr-%d/", pr.Number))
			for _, rr := range rRefs {
				parts := strings.Split(rr.Name, "/")
				if len(parts) > 0 {
					item.LastRereviewedSHA = parts[len(parts)-1]
				}
			}
			aRefs, _ := o.GH.ListMatchingRefs(ctx, o.Repo, fmt.Sprintf("tags/cc-crew-addressed/pr-%d/", pr.Number))
			item.AddressedCycles = len(aRefs)

			reviews, _ := o.GH.ListReviews(ctx, o.Repo, pr.Number)
			addressed := map[int]bool{}
			for _, r := range aRefs {
				parts := strings.Split(r.Name, "/")
				if len(parts) == 0 {
					continue
				}
				if id, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
					addressed[id] = true
				}
			}
			for _, rv := range reviews {
				if (rv.State == "COMMENTED" || rv.State == "CHANGES_REQUESTED") && !addressed[rv.ID] {
					item.PendingReviews++
				}
			}

			s.Continuous = append(s.Continuous, item)
		}
		sort.Slice(s.Continuous, func(i, j int) bool {
			return s.Continuous[i].PRNumber < s.Continuous[j].PRNumber
		})
	}

	sortItems(s.Implementers)
	sortItems(s.Reviewers)
	return s, nil
}

func sortItems(xs []Item) {
	sort.Slice(xs, func(i, j int) bool { return xs[i].Number < xs[j].Number })
}

func containsLabel(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func Render(w io.Writer, s Snapshot) {
	tw := tabwriter.NewWriter(w, 2, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PERSONA\tNUMBER\tSTATE\tCLAIM-AGE\tTITLE")
	for _, it := range s.Implementers {
		fmt.Fprintf(tw, "implementer\t#%d\t%s\t%s\t%s\n", it.Number, it.State, fmtAge(it.ClaimAge), it.Title)
	}
	for _, it := range s.Reviewers {
		fmt.Fprintf(tw, "reviewer\t#%d\t%s\t%s\t%s\n", it.Number, it.State, fmtAge(it.ClaimAge), it.Title)
	}
	tw.Flush()

	if len(s.Continuous) > 0 {
		fmt.Fprintln(w)
		tw2 := tabwriter.NewWriter(w, 2, 2, 2, ' ', 0)
		fmt.Fprintln(tw2, "CONTINUOUS-PR\tHEAD\tLAST-REREVIEWED\tCYCLES\tPENDING")
		for _, c := range s.Continuous {
			rev := c.LastRereviewedSHA
			if rev == "" {
				rev = "-"
			}
			fmt.Fprintf(tw2, "#%d\t%s\t%s\t%d/%d\t%d\n",
				c.PRNumber, short(c.HeadSHA), short(rev), c.AddressedCycles, c.MaxCycles, c.PendingReviews)
		}
		tw2.Flush()
	}
}

func fmtAge(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.Truncate(time.Second).String()
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
