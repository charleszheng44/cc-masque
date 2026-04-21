package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const sandboxImage = "ghcr.io/charleszheng44/cc-crew-sandbox"

func runSandbox(_ []string) int {
	repoName, err := gitRepoName()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: %v\n", err)
		return 1
	}

	name := fmt.Sprintf("cc-crew-sandbox-%s-%d", repoName, time.Now().Unix())

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: getwd: %v\n", err)
		return 1
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: home dir: %v\n", err)
		return 1
	}

	runArgs := []string{
		"run", "-d", "--rm",
		"--name", name,
		"-v", cwd + ":/workspace",
		"-v", home + "/.claude:/home/claude/.claude",
	}
	runArgs = appendEnv(runArgs, "CLAUDE_CODE_OAUTH_TOKEN", os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"))
	runArgs = appendEnv(runArgs, "ANTHROPIC_API_KEY", os.Getenv("ANTHROPIC_API_KEY"))
	// Implementer GitHub token: prefer GH_TOKEN_IMPLEMENTER, fall back to GH_TOKEN.
	ghToken := os.Getenv("GH_TOKEN_IMPLEMENTER")
	if ghToken == "" {
		ghToken = os.Getenv("GH_TOKEN")
	}
	runArgs = appendEnv(runArgs, "GH_TOKEN", ghToken)
	gitName := os.Getenv("IMPLEMENTER_GIT_NAME")
	runArgs = appendEnv(runArgs, "GIT_AUTHOR_NAME", gitName)
	runArgs = appendEnv(runArgs, "GIT_COMMITTER_NAME", gitName)
	gitEmail := os.Getenv("IMPLEMENTER_GIT_EMAIL")
	runArgs = appendEnv(runArgs, "GIT_AUTHOR_EMAIL", gitEmail)
	runArgs = appendEnv(runArgs, "GIT_COMMITTER_EMAIL", gitEmail)
	runArgs = append(runArgs, sandboxImage)

	start := exec.Command("docker", runArgs...)
	start.Stderr = os.Stderr
	if err := start.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cc-crew sandbox: docker run failed: %v\n", err)
		return 1
	}

	execCmd := exec.Command("docker", "exec", "-it", name, "claude", "--dangerously-skip-permissions")
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execErr := execCmd.Run()

	stop := exec.Command("docker", "stop", name)
	stop.Stderr = os.Stderr
	_ = stop.Run()

	if execErr != nil {
		return 1
	}
	return 0
}

func gitRepoName() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	toplevel := strings.TrimSpace(string(out))
	return sandboxSafeName(filepath.Base(toplevel)), nil
}

func appendEnv(args []string, key, val string) []string {
	if val == "" {
		return args
	}
	return append(args, "-e", key+"="+val)
}

// sandboxOpts is the set of inputs needed to build the `docker run` argv for
// `cc-crew sandbox`. All fields are required except hostClaudeDir, which is
// empty unless the user passed --use-host-claude.
type sandboxOpts struct {
	name          string
	image         string
	cwd           string
	sandboxHome   string
	hostClaudeDir string
	uid, gid      int
	env           map[string]string
}

// buildSandboxRunArgs constructs the argv (excluding the `docker` binary) for
// the sandbox container. Pure: no I/O, no env reads. Mount order matters when
// hostClaudeDir is set — the parent (/home/claude) must come before the nested
// (/home/claude/.claude). Env vars are emitted in sorted key order; empty
// values are filtered.
func buildSandboxRunArgs(o sandboxOpts) []string {
	args := []string{
		"run", "-d", "--rm",
		"--name", o.name,
		"--user", fmt.Sprintf("%d:%d", o.uid, o.gid),
		"-v", o.cwd + ":/workspace",
		"-v", o.sandboxHome + ":/home/claude",
	}
	if o.hostClaudeDir != "" {
		args = append(args, "-v", o.hostClaudeDir+":/home/claude/.claude")
	}
	keys := make([]string, 0, len(o.env))
	for k := range o.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := o.env[k]
		if v == "" {
			continue
		}
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, o.image)
	return args
}

// sandboxHomeDir returns the persistent host directory bind-mounted at
// /home/claude inside the sandbox container. The directory is created on
// first use and seeded with the onboarding-skip JSON so the in-container
// `claude` CLI does not prompt for setup. Subsequent calls reuse the existing
// directory and leave any existing seed file alone.
func sandboxHomeDir(repoName string) (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("user home dir: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "cc-crew", "sandbox-home", repoName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	seed := filepath.Join(dir, ".claude.json")
	if _, err := os.Stat(seed); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", seed, err)
		}
		const body = `{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}`
		if err := os.WriteFile(seed, []byte(body), 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", seed, err)
		}
	}
	return dir, nil
}

func sandboxSafeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
