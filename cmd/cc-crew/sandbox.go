package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	runArgs := []string{
		"run", "-d", "--rm",
		"--name", name,
		"-v", cwd + ":/workspace",
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
