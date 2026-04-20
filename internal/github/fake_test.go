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

func TestFakeMergePR(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[1] = &PullRequest{Number: 1, State: "open", HeadRefOid: "sha"}
	if err := f.MergePR(context.Background(), r, 1, "sha", MergeMethodRebase, true); err != nil {
		t.Fatalf("MergePR: %v", err)
	}
	if f.PRs[1].State != "closed" {
		t.Errorf("state = %q, want closed", f.PRs[1].State)
	}
	if !f.PRs[1].Merged {
		t.Error("Merged flag not set")
	}
}

func TestFakeMergePRConflictHook(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[2] = &PullRequest{Number: 2, State: "open", HeadRefOid: "sha", MergeStateStatus: "DIRTY"}
	f.MergePRHook = func(n int) error { return ErrMergeConflict }
	err := f.MergePR(context.Background(), r, 2, "sha", MergeMethodRebase, true)
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("want ErrMergeConflict, got %v", err)
	}
	if f.PRs[2].Merged {
		t.Error("PR should not be marked merged when hook rejects")
	}
}

func TestFakeMergePRRejectsShaMismatch(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[10] = &PullRequest{Number: 10, State: "open", HeadRefOid: "newsha"}
	err := f.MergePR(context.Background(), r, 10, "oldsha", MergeMethodRebase, true)
	if err == nil {
		t.Fatal("expected SHA-mismatch error, got nil")
	}
	if f.PRs[10].Merged {
		t.Error("PR should not be merged when SHA mismatches")
	}
}

func TestFakeUpdateBranch(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[3] = &PullRequest{Number: 3, State: "open", HeadRefOid: "old"}
	if err := f.UpdateBranch(context.Background(), r, 3, "old", UpdateMethodRebase); err != nil {
		t.Fatalf("UpdateBranch: %v", err)
	}
	if !f.UpdateBranchCalled[3] {
		t.Error("UpdateBranchCalled not recorded")
	}
}

func TestFakeUpdateBranchHookError(t *testing.T) {
	f := NewFake()
	sentinel := errors.New("boom")
	f.UpdateBranchHook = func(int) error { return sentinel }
	err := f.UpdateBranch(context.Background(), Repo{Owner: "o", Name: "n"}, 99, "sha", UpdateMethodRebase)
	if err != sentinel {
		t.Fatalf("want sentinel, got %v", err)
	}
	if f.UpdateBranchCalled[99] {
		t.Error("UpdateBranchCalled should stay false when hook errors")
	}
}

func TestFakeCreateComment(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.PRs[4] = &PullRequest{Number: 4, State: "open"}
	if err := f.CreateComment(context.Background(), r, 4, "hello"); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if got := f.Comments[4]; len(got) != 1 || got[0] != "hello" {
		t.Errorf("Comments[4] = %v, want [hello]", got)
	}
}

func TestFakeCreateCommentAppends(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	if err := f.CreateComment(context.Background(), r, 5, "first"); err != nil {
		t.Fatal(err)
	}
	if err := f.CreateComment(context.Background(), r, 5, "second"); err != nil {
		t.Fatal(err)
	}
	got := f.Comments[5]
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Errorf("Comments[5] = %v, want [first second]", got)
	}
}

func TestFakeCreateCommentHookError(t *testing.T) {
	f := NewFake()
	sentinel := errors.New("nope")
	f.CreateCommentHook = func(int) error { return sentinel }
	err := f.CreateComment(context.Background(), Repo{Owner: "o", Name: "n"}, 6, "x")
	if err != sentinel {
		t.Fatalf("want sentinel, got %v", err)
	}
	if len(f.Comments[6]) != 0 {
		t.Error("no comment should be recorded when hook errors")
	}
}

func TestFakeGetCheckRuns(t *testing.T) {
	f := NewFake()
	r := Repo{Owner: "o", Name: "n"}
	f.CheckRuns["sha"] = []CheckRun{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "lint", Status: "in_progress", Conclusion: ""},
	}
	got, err := f.GetCheckRuns(context.Background(), r, "sha")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d runs, want 2", len(got))
	}
	if got[0].Name != "build" || got[0].Conclusion != "success" {
		t.Errorf("runs[0] = %+v", got[0])
	}
}

func TestFakeGetCheckRunsEmpty(t *testing.T) {
	f := NewFake()
	got, err := f.GetCheckRuns(context.Background(), Repo{Owner: "o", Name: "n"}, "unknown")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}
