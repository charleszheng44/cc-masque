package github

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// compile-time assertion: *FakeClient must satisfy Client.
var _ Client = (*FakeClient)(nil)

// FakeClient is an in-memory Client for unit tests.
type FakeClient struct {
	mu        sync.Mutex
	User      string
	Issues    map[int]*Issue       // keyed by number
	PRs       map[int]*PullRequest // keyed by number
	Refs      map[string]string    // ref name → sha
	Labels    map[string]struct{}  // label name → presence
	Reviews   map[int][]Review     // PR number → reviews
	DefaultBr string

	// Hooks for injecting errors in specific calls. Leave nil to disable.
	CreateRefHook   func(ref string) error
	DeleteRefHook   func(ref string) error
	CreateLabelHook func(name string) error
}

func NewFake() *FakeClient {
	return &FakeClient{
		User:      "fake-bot",
		Issues:    map[int]*Issue{},
		PRs:       map[int]*PullRequest{},
		Refs:      map[string]string{},
		Labels:    map[string]struct{}{},
		Reviews:   map[int][]Review{},
		DefaultBr: "main",
	}
}

func (f *FakeClient) CurrentUser(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.User, nil
}

func (f *FakeClient) DefaultBranch(ctx context.Context, r Repo) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.DefaultBr, nil
}

func hasAll(haystack []string, needles []string) bool {
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func hasAny(haystack []string, needles []string) bool {
	for _, n := range needles {
		for _, h := range haystack {
			if h == n {
				return true
			}
		}
	}
	return false
}

func (f *FakeClient) ListIssues(ctx context.Context, r Repo, with, without []string) ([]Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Issue{}
	for _, i := range f.Issues {
		if i.State != "open" {
			continue
		}
		if !hasAll(i.Labels, with) {
			continue
		}
		if hasAny(i.Labels, without) {
			continue
		}
		cp := *i
		cp.Labels = append([]string(nil), i.Labels...)
		out = append(out, cp)
	}
	return out, nil
}

func (f *FakeClient) ListPRs(ctx context.Context, r Repo, with, without []string) ([]PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []PullRequest{}
	for _, p := range f.PRs {
		if p.State != "open" {
			continue
		}
		if !hasAll(p.Labels, with) {
			continue
		}
		if hasAny(p.Labels, without) {
			continue
		}
		cp := *p
		cp.Labels = append([]string(nil), p.Labels...)
		out = append(out, cp)
	}
	return out, nil
}

func (f *FakeClient) GetPR(ctx context.Context, r Repo, n int) (PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.PRs[n]
	if !ok {
		return PullRequest{}, fmt.Errorf("fake: PR %d not found", n)
	}
	cp := *p
	cp.Labels = append([]string(nil), p.Labels...)
	return cp, nil
}

func removeStr(s []string, v string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func (f *FakeClient) AddLabel(ctx context.Context, r Repo, n int, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.Issues[n]; ok {
		for _, l := range i.Labels {
			if l == label {
				return nil
			}
		}
		i.Labels = append(i.Labels, label)
		return nil
	}
	if p, ok := f.PRs[n]; ok {
		for _, l := range p.Labels {
			if l == label {
				return nil
			}
		}
		p.Labels = append(p.Labels, label)
		return nil
	}
	return fmt.Errorf("fake: issue/PR %d not found", n)
}

func (f *FakeClient) RemoveLabel(ctx context.Context, r Repo, n int, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.Issues[n]; ok {
		i.Labels = removeStr(i.Labels, label)
		return nil
	}
	if p, ok := f.PRs[n]; ok {
		p.Labels = removeStr(p.Labels, label)
		return nil
	}
	return fmt.Errorf("fake: issue/PR %d not found", n)
}

func (f *FakeClient) CreateLabel(ctx context.Context, r Repo, name, color, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateLabelHook != nil {
		if err := f.CreateLabelHook(name); err != nil {
			return err
		}
	}
	if _, exists := f.Labels[name]; exists {
		return ErrLabelExists
	}
	f.Labels[name] = struct{}{}
	return nil
}

func (f *FakeClient) CreateRef(ctx context.Context, r Repo, ref, sha string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateRefHook != nil {
		if err := f.CreateRefHook(ref); err != nil {
			return err
		}
	}
	if _, exists := f.Refs[ref]; exists {
		return ErrRefExists
	}
	f.Refs[ref] = sha
	return nil
}

func (f *FakeClient) DeleteRef(ctx context.Context, r Repo, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteRefHook != nil {
		if err := f.DeleteRefHook(ref); err != nil {
			return err
		}
	}
	delete(f.Refs, ref)
	return nil
}

func (f *FakeClient) ListMatchingRefs(ctx context.Context, r Repo, prefix string) ([]Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Ref{}
	for k, v := range f.Refs {
		if strings.HasPrefix(k, "refs/"+prefix) || strings.HasPrefix(k, prefix) {
			out = append(out, Ref{Name: k, SHA: v})
		}
	}
	return out, nil
}

func (f *FakeClient) GetRef(ctx context.Context, r Repo, ref string) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sha, ok := f.Refs[ref]
	if !ok {
		return Ref{}, fmt.Errorf("fake: ref %s not found", ref)
	}
	return Ref{Name: ref, SHA: sha}, nil
}

func (f *FakeClient) ListReviews(ctx context.Context, r Repo, prNumber int) ([]Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Review(nil), f.Reviews[prNumber]...), nil
}
