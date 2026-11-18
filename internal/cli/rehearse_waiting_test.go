package cli

import (
	"bytes"
	"testing"
)

// TestWaitingLines pins what aw rehearse passes on from a step's output while it runs (#110): only
// the lines that say it waits on the human, whole, however the output arrives, and named by lane.
func TestWaitingLines(t *testing.T) {
	var out bytes.Buffer
	w := &waitingLines{r: &rehearsal{progress: &out}, lane: "rehearse-first"}
	for _, chunk := range []string{"aw: setting up web\naw: web.setup: wait", "ing on you: prerequisite vpn is not met → connect the VPN\n", "noise\n", "partial"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	want := "aw: lane rehearse-first: web.setup: waiting on you: prerequisite vpn is not met → connect the VPN\n"
	if out.String() != want {
		t.Errorf("passed on %q, want %q", out.String(), want)
	}
}
