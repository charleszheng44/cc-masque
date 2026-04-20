package github

import (
	"context"
	"errors"
	"testing"
)

func TestFakeCreateRefAtomic(t *testing.T) {
	c := NewFake()
	r := Repo{Owner: "acme", Name: "widget"}
	ctx := context.Background()

	if err := c.CreateRef(ctx, r, "refs/heads/foo", "abc123"); err != nil {
		t.Fatalf("first CreateRef: %v", err)
	}
	err := c.CreateRef(ctx, r, "refs/heads/foo", "abc123")
	if !errors.Is(err, ErrRefExists) {
		t.Fatalf("expected ErrRefExists, got %v", err)
	}
}

func TestFakeListIssuesLabelFiltering(t *testing.T) {
	c := NewFake()
	r := Repo{Owner: "acme", Name: "widget"}
	c.Issues[1] = &Issue{Number: 1, State: "open", Labels: []string{"claude-task"}}
	c.Issues[2] = &Issue{Number: 2, State: "open", Labels: []string{"claude-task", "claude-processing"}}
	c.Issues[3] = &Issue{Number: 3, State: "closed", Labels: []string{"claude-task"}}

	got, err := c.ListIssues(context.Background(), r, []string{"claude-task"}, []string{"claude-processing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("expected issue 1 only, got %+v", got)
	}
}

func TestFakeConcurrentReadsDoNotObserveTornLabels(t *testing.T) {
	c := NewFake()
	r := Repo{Owner: "acme", Name: "widget"}
	c.Issues[1] = &Issue{Number: 1, State: "open", Labels: []string{"a", "b", "c", "d"}}

	done := make(chan struct{})
	// Mutator: repeatedly add and remove a label.
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			_ = c.AddLabel(context.Background(), r, 1, "x")
			_ = c.RemoveLabel(context.Background(), r, 1, "x")
		}
	}()
	// Reader: repeatedly snapshot and inspect labels.
	for i := 0; i < 1000; i++ {
		issues, err := c.ListIssues(context.Background(), r, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(issues))
		}
		// The snapshot must always contain at least a,b,c,d
		// regardless of the mutator's in-flight state.
		for _, want := range []string{"a", "b", "c", "d"} {
			found := false
			for _, got := range issues[0].Labels {
				if got == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("snapshot missing baseline label %q: %v", want, issues[0].Labels)
			}
		}
	}
	<-done
}

func TestFakeCreateLabelIdempotent(t *testing.T) {
	c := NewFake()
	r := Repo{Owner: "acme", Name: "widget"}
	ctx := context.Background()

	if err := c.CreateLabel(ctx, r, "claude-task", "1d76db", "desc"); err != nil {
		t.Fatalf("first CreateLabel: %v", err)
	}
	err := c.CreateLabel(ctx, r, "claude-task", "1d76db", "desc")
	if !errors.Is(err, ErrLabelExists) {
		t.Fatalf("expected ErrLabelExists on second create, got %v", err)
	}
	if _, ok := c.Labels["claude-task"]; !ok {
		t.Fatalf("label not recorded in FakeClient.Labels")
	}
}

func TestFakeGetPRRoundTripsMergeableFields(t *testing.T) {
	c := NewFake()
	r := Repo{Owner: "acme", Name: "widget"}
	c.PRs[9] = &PullRequest{
		Number: 9, State: "open",
		Mergeable: "MERGEABLE", MergeStateStatus: "BEHIND",
	}

	got, err := c.GetPR(context.Background(), r, 9)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mergeable != "MERGEABLE" || got.MergeStateStatus != "BEHIND" {
		t.Fatalf("GetPR: want MERGEABLE/BEHIND, got %q/%q", got.Mergeable, got.MergeStateStatus)
	}

	list, err := c.ListPRs(context.Background(), r, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 PR, got %d", len(list))
	}
	if list[0].Mergeable != "MERGEABLE" || list[0].MergeStateStatus != "BEHIND" {
		t.Fatalf("ListPRs: want MERGEABLE/BEHIND, got %q/%q", list[0].Mergeable, list[0].MergeStateStatus)
	}
}

func TestFakeCreateLabelHookCanInjectError(t *testing.T) {
	c := NewFake()
	sentinel := errors.New("boom")
	c.CreateLabelHook = func(name string) error {
		if name == "claude-done" {
			return sentinel
		}
		return nil
	}
	ctx := context.Background()
	r := Repo{Owner: "acme", Name: "widget"}
	if err := c.CreateLabel(ctx, r, "claude-task", "1d76db", "d"); err != nil {
		t.Fatalf("claude-task should succeed: %v", err)
	}
	if err := c.CreateLabel(ctx, r, "claude-done", "0e8a16", "d"); err != sentinel {
		t.Fatalf("want sentinel, got %v", err)
	}
}
