package status

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charleszheng44/cc-crew/internal/claim"
)

func TestRenderBasic(t *testing.T) {
	s := Snapshot{
		Implementers: []Item{{Kind: claim.KindImplementer, Number: 42, Title: "bug", State: "queued"}},
	}
	var buf bytes.Buffer
	Render(&buf, s)
	if !strings.Contains(buf.String(), "#42") || !strings.Contains(buf.String(), "queued") {
		t.Fatalf("render output:\n%s", buf.String())
	}
}
