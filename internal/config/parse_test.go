package config

import "testing"

func TestParseOwnerRepo(t *testing.T) {
	cases := []struct {
		in string
		ow string
		nm string
	}{
		{"https://github.com/acme/widget.git", "acme", "widget"},
		{"https://github.com/acme/widget", "acme", "widget"},
		{"git@github.com:acme/widget.git", "acme", "widget"},
		{"ssh://git@github.com/acme/widget.git", "acme", "widget"},
	}
	for _, tc := range cases {
		o, n, err := ParseOwnerRepo(tc.in)
		if err != nil || o != tc.ow || n != tc.nm {
			t.Errorf("%s -> (%q,%q,%v), want (%q,%q,nil)", tc.in, o, n, err, tc.ow, tc.nm)
		}
	}
}

func TestParseFlagOverridesEnv(t *testing.T) {
	env := map[string]string{
		"CC_MAX_IMPLEMENTERS":     "7",
		"GH_TOKEN":                "t",
		"CLAUDE_CODE_OAUTH_TOKEN": "c",
		"IMPLEMENTER_GIT_NAME":    "i",
		"IMPLEMENTER_GIT_EMAIL":   "i@x",
		"REVIEWER_GIT_NAME":       "r",
		"REVIEWER_GIT_EMAIL":      "r@x",
	}
	get := func(k string) string { return env[k] }
	c, err := Parse([]string{"--max-implementers", "2"}, get, "/tmp/repo")
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxImplementers != 2 {
		t.Fatalf("flag should override env, got %d", c.MaxImplementers)
	}
}
