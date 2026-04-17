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
