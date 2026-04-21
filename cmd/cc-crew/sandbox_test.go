package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSandboxHomeDir_CreatesAndSeeds(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	dir, err := sandboxHomeDir("acme-widget")
	if err != nil {
		t.Fatalf("sandboxHomeDir: %v", err)
	}
	want := filepath.Join(cache, "cc-crew", "sandbox-home", "acme-widget")
	if dir != want {
		t.Fatalf("got %q want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got %v", info.Mode())
	}
	seed, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	const wantBody = `{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true,"theme":"dark"}`
	if string(seed) != wantBody {
		t.Fatalf("seed mismatch:\n got %q\nwant %q", seed, wantBody)
	}
}

func TestSandboxHomeDir_DoesNotOverwriteExistingSeed(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	dir, err := sandboxHomeDir("acme-widget")
	if err != nil {
		t.Fatalf("first sandboxHomeDir: %v", err)
	}
	custom := []byte(`{"hasCompletedOnboarding":true,"theme":"light"}`)
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), custom, 0o644); err != nil {
		t.Fatalf("write custom seed: %v", err)
	}
	if _, err := sandboxHomeDir("acme-widget"); err != nil {
		t.Fatalf("second sandboxHomeDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if string(got) != string(custom) {
		t.Fatalf("seed was overwritten:\n got %q\nwant %q", got, custom)
	}
}

func TestBuildSandboxRunArgs_Default(t *testing.T) {
	args := buildSandboxRunArgs(sandboxOpts{
		name:        "ctr",
		image:       "img:tag",
		cwd:         "/workspace-host",
		sandboxHome: "/sbx-home",
		uid:         1234,
		gid:         5678,
		env:         map[string]string{"FOO": "bar"},
	})
	want := []string{
		"run", "-d", "--rm",
		"--name", "ctr",
		"--user", "1234:5678",
		"-v", "/workspace-host:/workspace",
		"-v", "/sbx-home:/home/claude",
		"-e", "FOO=bar",
		"img:tag",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v\nwant %v", args, want)
	}
}

func TestBuildSandboxRunArgs_UseHostClaude(t *testing.T) {
	args := buildSandboxRunArgs(sandboxOpts{
		name:          "ctr",
		image:         "img:tag",
		cwd:           "/workspace-host",
		sandboxHome:   "/sbx-home",
		hostClaudeDir: "/host/.claude",
		uid:           1234,
		gid:           5678,
	})
	// Mount order: parent (/home/claude) before nested (/home/claude/.claude).
	want := []string{
		"run", "-d", "--rm",
		"--name", "ctr",
		"--user", "1234:5678",
		"-v", "/workspace-host:/workspace",
		"-v", "/sbx-home:/home/claude",
		"-v", "/host/.claude:/home/claude/.claude",
		"img:tag",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v\nwant %v", args, want)
	}
}

func TestBuildSandboxRunArgs_EnvSortedAndEmptyFiltered(t *testing.T) {
	args := buildSandboxRunArgs(sandboxOpts{
		name:        "ctr",
		image:       "img",
		cwd:         "/cwd",
		sandboxHome: "/sbx",
		uid:         1, gid: 2,
		env: map[string]string{
			"ZED":   "z",
			"ALPHA": "a",
			"EMPTY": "",
		},
	})
	want := []string{
		"run", "-d", "--rm",
		"--name", "ctr",
		"--user", "1:2",
		"-v", "/cwd:/workspace",
		"-v", "/sbx:/home/claude",
		"-e", "ALPHA=a",
		"-e", "ZED=z",
		"img",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v\nwant %v", args, want)
	}
}

func TestParseSandboxFlags_Default(t *testing.T) {
	f, err := parseSandboxFlags(nil)
	if err != nil {
		t.Fatalf("parseSandboxFlags: %v", err)
	}
	if f.useHostClaude {
		t.Fatalf("default should be useHostClaude=false, got true")
	}
}

func TestParseSandboxFlags_UseHostClaude(t *testing.T) {
	f, err := parseSandboxFlags([]string{"--use-host-claude"})
	if err != nil {
		t.Fatalf("parseSandboxFlags: %v", err)
	}
	if !f.useHostClaude {
		t.Fatalf("expected useHostClaude=true")
	}
}

func TestParseSandboxFlags_UnknownFlagErrors(t *testing.T) {
	if _, err := parseSandboxFlags([]string{"--bogus"}); err == nil {
		t.Fatalf("expected error for unknown flag, got nil")
	}
}

func TestParseSandboxFlags_PositionalArgsError(t *testing.T) {
	if _, err := parseSandboxFlags([]string{"extra"}); err == nil {
		t.Fatalf("expected error for positional arg, got nil")
	}
}

func TestParseSandboxFlags_HelpReturnsErrHelp(t *testing.T) {
	_, err := parseSandboxFlags([]string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
}
