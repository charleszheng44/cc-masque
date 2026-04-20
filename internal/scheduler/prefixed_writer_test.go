package scheduler

import (
	"strings"
	"testing"
)

func TestPrefixedWriter_SplitWrite(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf strings.Builder
	pw := NewPrefixedWriter(&buf, "[test] ", 0)

	// First write ends mid-line — no output expected yet.
	n, err := pw.Write([]byte("hel"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("want n=3, got %d", n)
	}
	if buf.Len() != 0 {
		t.Fatalf("want no output for partial line, got %q", buf.String())
	}

	// Second write completes the first line and starts a second partial.
	n, err = pw.Write([]byte("lo\nwor"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("want n=6, got %d", n)
	}

	got := buf.String()
	if got != "[test] hello\n" {
		t.Fatalf("want %q, got %q", "[test] hello\n", got)
	}

	// Third write completes the second line.
	_, err = pw.Write([]byte("ld\n"))
	if err != nil {
		t.Fatal(err)
	}

	got = buf.String()
	want := "[test] hello\n[test] world\n"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestPrefixedWriter_NoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf strings.Builder
	pw := NewPrefixedWriter(&buf, "[issue-42] ", 42)

	if _, err := pw.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if strings.Contains(got, "\x1b") {
		t.Fatalf("NO_COLOR=1 but got ANSI escape in %q", got)
	}
	if got != "[issue-42] hello\n" {
		t.Fatalf("want %q, got %q", "[issue-42] hello\n", got)
	}
}

func TestPrefixedWriter_ColorEnabled(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	var buf strings.Builder
	pw := NewPrefixedWriter(&buf, "[issue-42] ", 0) // num=0 → cyan

	if _, err := pw.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if !strings.HasPrefix(got, "\x1b[36m") {
		t.Fatalf("want cyan prefix, got %q", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("want reset after prefix, got %q", got)
	}
	// Line body should not be colored (reset appears before the body).
	if !strings.Contains(got, "[issue-42] \x1b[0m") {
		t.Fatalf("want reset immediately after label, got %q", got)
	}
}

func TestPrefixedWriter_CloseFlushesPartial(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf strings.Builder
	pw := NewPrefixedWriter(&buf, "[pr-99] ", 99)

	if _, err := pw.Write([]byte("no newline")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("want no premature output, got %q", buf.String())
	}

	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if got != "[pr-99] no newline" {
		t.Fatalf("want %q, got %q", "[pr-99] no newline", got)
	}
}
