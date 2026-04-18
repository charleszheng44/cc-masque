package status

import (
	"context"
	"fmt"
	"io"
	"sort"
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

type Snapshot struct {
	Implementers []Item
	Reviewers    []Item
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
}

func fmtAge(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.Truncate(time.Second).String()
}
