package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// RunSpec describes a one-shot docker run.
type RunSpec struct {
	Image  string
	Name   string
	Labels map[string]string
	Env    map[string]string
	Mounts []Mount
	Stdout io.Writer
	Stderr io.Writer
	// UID/GID, when both non-nil, render as `--user UID:GID`. Set by callers
	// to the host's uid/gid so files the container writes into bind-mounted
	// host paths (notably .git/objects) are owned by the host user, not root.
	UID *int
	GID *int
}

type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// Runner wraps `docker` invocations. It is stateless; safe to share.
type Runner struct {
	Bin string
}

func New() *Runner { return &Runner{Bin: "docker"} }

// BuildRunArgs constructs the argv (excluding the binary itself) for
// `docker run --rm`. Exposed for testing.
func BuildRunArgs(s RunSpec) []string {
	args := []string{"run", "--rm", "--name", s.Name}
	if s.UID != nil && s.GID != nil {
		args = append(args, "--user", fmt.Sprintf("%d:%d", *s.UID, *s.GID))
	}
	keys := make([]string, 0, len(s.Labels))
	for k := range s.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+s.Labels[k])
	}
	envKeys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "-e", k+"="+s.Env[k])
	}
	for _, m := range s.Mounts {
		v := m.HostPath + ":" + m.ContainerPath
		if m.ReadOnly {
			v += ":ro"
		}
		args = append(args, "-v", v)
	}
	args = append(args, s.Image)
	return args
}

// Run blocks until the container exits. Returns the exit code (0 on success).
// If the context is cancelled or times out, ctx.Err() is returned as the error
// so callers can distinguish cancellation from a genuine container failure.
func (r *Runner) Run(ctx context.Context, s RunSpec) (int, error) {
	cmd := exec.CommandContext(ctx, r.Bin, BuildRunArgs(s)...)
	cmd.Stdout = s.Stdout
	cmd.Stderr = s.Stderr
	err := cmd.Run()
	if c, ok := s.Stdout.(io.Closer); ok {
		_ = c.Close()
	}
	if c, ok := s.Stderr.(io.Closer); ok {
		_ = c.Close()
	}
	if ctx.Err() != nil {
		// Context canceled (timeout or caller cancellation).
		return -1, ctx.Err()
	}
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return -1, fmt.Errorf("docker run: %w", err)
}

// Kill sends `docker kill <name>`. Idempotent: no-op error on missing container.
func (r *Runner) Kill(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, r.Bin, "kill", name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "No such container") {
			return nil
		}
		return fmt.Errorf("docker kill %s: %w (%s)", name, err, stderr.String())
	}
	return nil
}

// PSEntry is a subset of `docker ps` output.
type PSEntry struct {
	Name   string
	Image  string
	Labels map[string]string
}

// PS lists running containers matching the given labels.
func (r *Runner) PS(ctx context.Context, labelMatchers map[string]string) ([]PSEntry, error) {
	args := []string{"ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Labels}}"}
	for k, v := range labelMatchers {
		args = append(args, "--filter", "label="+k+"="+v)
	}
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker ps: %w (%s)", err, stderr.String())
	}
	return parsePS(stdout.String()), nil
}

func parsePS(out string) []PSEntry {
	var res []PSEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		var rawLabels string
		if len(parts) >= 3 {
			rawLabels = parts[2]
		}
		labels := map[string]string{}
		for _, kv := range strings.Split(rawLabels, ",") {
			kv = strings.TrimSpace(kv)
			if kv == "" {
				continue
			}
			eq := strings.Index(kv, "=")
			if eq < 0 {
				continue
			}
			labels[kv[:eq]] = kv[eq+1:]
		}
		res = append(res, PSEntry{Name: parts[0], Image: parts[1], Labels: labels})
	}
	return res
}
