package lanes

import (
	"strings"
	"testing"
	"time"
)

func TestBeatRepeatsUntilStopped(t *testing.T) {
	defer func(d time.Duration) { beatEvery = d }(beatEvery)
	beatEvery = 5 * time.Millisecond
	lines := make(chan string, 100)
	stop := Beat(func(l string) { lines <- l }, "still going")
	first := <-lines
	if !strings.HasPrefix(first, "still going (") {
		t.Errorf("got %q", first)
	}
	stop()
	Beat(nil, "nobody is listening")()
}
