package main

import (
	"os"
	"path/filepath"
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
