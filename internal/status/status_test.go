package status

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/github"
)

func TestRenderBasic(t *testing.T) {
	s := Snapshot{
		Implementers: []Item{{Kind: claim.KindImplementer, Number: 42, Title: "bug", State: "queued"}},
	}
	var buf bytes.Buffer
	Render(&buf, s)
	if !strings.Contains(buf.String(), "#42") || !strings.Contains(buf.String(), "queued") {
		t.Fatalf("render output:\n%s", buf.String())
	}
}

func TestFetchContinuousSection(t *testing.T) {
	f := github.NewFake()
	repo := github.Repo{Owner: "a", Name: "b"}
	f.PRs[70] = &github.PullRequest{
		Number: 70, State: "open", HeadRefName: "claude/issue-70",
		HeadRefOid: "sha-new",
		Labels:     []string{"claude-reviewed"},
	}
	f.Refs["refs/cc-crew/rereviewed/pr-70/sha-old"] = "sha-old"
	f.Refs["refs/cc-crew/addressed/pr-70/500"] = "sha"

	o := Options{
		GH: f, Docker: nil, Repo: repo,
		ContinuousEnabled: true, MaxCycles: 3,
	}
	s, err := Fetch(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Continuous) != 1 {
		t.Fatalf("Continuous len = %d", len(s.Continuous))
	}
	it := s.Continuous[0]
	if it.PRNumber != 70 || it.HeadSHA != "sha-new" ||
		it.LastRereviewedSHA != "sha-old" || it.AddressedCycles != 1 {
		t.Fatalf("%+v", it)
	}
}
