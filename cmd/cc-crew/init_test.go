package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/github"
)

func specs() []labelSpec {
	return []labelSpec{
		{Name: "claude-task", Color: "1d76db", Description: "task"},
		{Name: "claude-processing", Color: "0366d6", Description: "proc"},
		{Name: "claude-done", Color: "0e8a16", Description: "done"},
	}
}

func TestDoInitAllCreated(t *testing.T) {
	fake := github.NewFake()
	var out bytes.Buffer
	code := doInit(context.Background(), initOptions{
		GH: fake, Repo: github.Repo{Owner: "a", Name: "b"},
		Specs: specs(), Out: &out, Errout: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d", code)
	}
	text := out.String()
	for _, want := range []string{
		"created: claude-task",
		"created: claude-processing",
		"created: claude-done",
		"3 labels: 3 created, 0 existed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestDoInitAllExist(t *testing.T) {
	fake := github.NewFake()
	for _, s := range specs() {
		fake.Labels[s.Name] = struct{}{}
	}
	var out bytes.Buffer
	code := doInit(context.Background(), initOptions{
		GH: fake, Repo: github.Repo{Owner: "a", Name: "b"},
		Specs: specs(), Out: &out, Errout: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d", code)
	}
	text := out.String()
	for _, want := range []string{
		"exists:  claude-task",
		"exists:  claude-processing",
		"exists:  claude-done",
		"3 labels: 0 created, 3 existed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestDoInitMixed(t *testing.T) {
	fake := github.NewFake()
	fake.Labels["claude-task"] = struct{}{}
	var out bytes.Buffer
	code := doInit(context.Background(), initOptions{
		GH: fake, Repo: github.Repo{Owner: "a", Name: "b"},
		Specs: specs(), Out: &out, Errout: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d", code)
	}
	text := out.String()
	if !strings.Contains(text, "exists:  claude-task") {
		t.Fatalf("want exists for claude-task:\n%s", text)
	}
	if !strings.Contains(text, "created: claude-processing") {
		t.Fatalf("want created for claude-processing:\n%s", text)
	}
	if !strings.Contains(text, "3 labels: 2 created, 1 existed") {
		t.Fatalf("bad summary:\n%s", text)
	}
}

func TestDoInitBailsOnFirstNonConflictError(t *testing.T) {
	fake := github.NewFake()
	fake.CreateLabelHook = func(name string) error {
		if name == "claude-processing" {
			return errors.New("forbidden")
		}
		return nil
	}
	var out, errout bytes.Buffer
	code := doInit(context.Background(), initOptions{
		GH: fake, Repo: github.Repo{Owner: "a", Name: "b"},
		Specs: specs(), Out: &out, Errout: &errout,
	})
	if code != 1 {
		t.Fatalf("exit code: want 1, got %d", code)
	}
	if !strings.Contains(out.String(), "created: claude-task") {
		t.Fatalf("first label should have been reported as created:\n%s", out.String())
	}
	if strings.Contains(out.String(), "claude-done") {
		t.Fatalf("should not have attempted claude-done after bail:\n%s", out.String())
	}
	if !strings.Contains(errout.String(), "forbidden") {
		t.Fatalf("stderr should carry the underlying error: %s", errout.String())
	}
	if strings.Contains(out.String(), "3 labels:") {
		t.Fatalf("summary must be skipped on error:\n%s", out.String())
	}
}

func TestBuildLabelSpecsHonorsEnvOverrides(t *testing.T) {
	getenv := func(k string) string {
		if k == "CC_TASK_LABEL" {
			return "foo"
		}
		return ""
	}
	got := buildLabelSpecs(getenv)
	found := false
	for _, s := range got {
		if s.Name == "foo" {
			found = true
		}
		if s.Name == "claude-task" {
			t.Fatalf("env override ignored: %+v", got)
		}
	}
	if !found {
		t.Fatalf("expected a spec named foo, got %+v", got)
	}
	if len(got) != 9 {
		t.Fatalf("expected 9 specs, got %d", len(got))
	}
}
