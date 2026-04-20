package github

import "time"

type Issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	State  string   `json:"state"`
	Labels []string `json:"labels"`
}

type PullRequest struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	State       string   `json:"state"`
	Labels      []string `json:"labels"`
	HeadRefOid  string   `json:"headRefOid"`
	HeadRefName string   `json:"headRefName"`
	BaseRefName string   `json:"baseRefName"`
}

type Ref struct {
	Name string // e.g. "refs/heads/claude/issue-42" or "refs/cc-crew/claim/issue-42/20260417T120000Z"
	SHA  string
}

type Review struct {
	ID     int    // review ID from the GitHub API
	Author string // login
	State  string // COMMENTED, APPROVED, CHANGES_REQUESTED, DISMISSED
	At     time.Time
}

type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }
