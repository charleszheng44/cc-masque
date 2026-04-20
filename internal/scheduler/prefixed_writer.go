package scheduler

import (
	"bytes"
	"io"
	"os"
	"sync"
)

// lockedWriter serializes concurrent writes to an underlying io.Writer so that
// prefix+line pairs emitted by PrefixedWriter are never interleaved across
// goroutines sharing the same destination.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}

// lockedStdout and lockedStderr are shared by all PrefixedWriter instances in
// a process so that concurrent containers cannot interleave their output.
var (
	lockedStdout = &lockedWriter{w: os.Stdout}
	lockedStderr = &lockedWriter{w: os.Stderr}
)

var ansiColors = []string{
	"\x1b[36m", // cyan
	"\x1b[32m", // green
	"\x1b[33m", // yellow
	"\x1b[35m", // magenta
	"\x1b[34m", // blue
	"\x1b[97m", // bright-white
}

const ansiReset = "\x1b[0m"

// PrefixedWriter prepends a prefix string to each line written to the
// underlying writer. Partial lines (writes not ending in '\n') are buffered
// until the newline arrives. Close flushes any buffered partial line.
type PrefixedWriter struct {
	w      io.Writer
	prefix string // fully-formatted prefix (with or without ANSI color)
	buf    []byte // incomplete line buffered across Write calls
}

// NewPrefixedWriter returns a PrefixedWriter that prepends prefix to every
// line. If the env var NO_COLOR is set to a non-empty value, ANSI color
// escapes are omitted; otherwise the prefix is wrapped in the color selected
// by num%6.
func NewPrefixedWriter(w io.Writer, rawPrefix string, num int) *PrefixedWriter {
	prefix := rawPrefix
	if os.Getenv("NO_COLOR") == "" {
		color := ansiColors[num%len(ansiColors)]
		prefix = color + rawPrefix + ansiReset
	}
	return &PrefixedWriter{w: w, prefix: prefix}
}

func (p *PrefixedWriter) Write(data []byte) (int, error) {
	total := len(data)
	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\n')
		if idx == -1 {
			// No newline — buffer the rest.
			p.buf = append(p.buf, data...)
			break
		}
		// Combine prefix + buffered bytes + current chunk into one write so
		// that the lockedWriter's mutex covers both atomically.
		combined := make([]byte, 0, len(p.prefix)+len(p.buf)+idx+1)
		combined = append(combined, p.prefix...)
		combined = append(combined, p.buf...)
		combined = append(combined, data[:idx+1]...)
		p.buf = p.buf[:0]
		if _, err := p.w.Write(combined); err != nil {
			return 0, err
		}
		data = data[idx+1:]
	}
	return total, nil
}

// Close flushes any partial line still held in the buffer.
func (p *PrefixedWriter) Close() error {
	if len(p.buf) > 0 {
		combined := make([]byte, 0, len(p.prefix)+len(p.buf))
		combined = append(combined, p.prefix...)
		combined = append(combined, p.buf...)
		p.buf = p.buf[:0]
		if _, err := p.w.Write(combined); err != nil {
			return err
		}
	}
	return nil
}
