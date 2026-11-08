// Package timing records how long each phase of a command takes, for AW_DEBUG=1 and aw doctor
// --timings (speed.md, "Enforcement"). It is on the hot path, so it costs a time.Now per phase.
package timing

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Recorder records phases in order.
type Recorder struct {
	start  time.Time
	last   time.Time
	phases []Phase
}

// Phase is one named step and its duration.
type Phase struct {
	Name     string        `json:"name"`
	Duration time.Duration `json:"duration_ns"`
}

// Start begins recording now.
func Start() *Recorder {
	now := time.Now()
	return &Recorder{start: now, last: now}
}

// Mark ends the current phase and names it.
func (r *Recorder) Mark(name string) {
	now := time.Now()
	r.phases = append(r.phases, Phase{Name: name, Duration: now.Sub(r.last)})
	r.last = now
}

// Phases returns the recorded phases.
func (r *Recorder) Phases() []Phase { return r.phases }

// Total is the time since Start.
func (r *Recorder) Total() time.Duration { return time.Since(r.start) }

// Debug reports whether AW_DEBUG asks for timings.
func Debug() bool { return os.Getenv("AW_DEBUG") == "1" }

// Report writes one line: "aw: timings: <command> load=1.2ms parse=0.1ms total=2.0ms".
func (r *Recorder) Report(w io.Writer, command string) {
	parts := make([]string, 0, len(r.phases)+1)
	for _, p := range r.phases {
		parts = append(parts, fmt.Sprintf("%s=%s", p.Name, ms(p.Duration)))
	}
	parts = append(parts, "total="+ms(r.Total()))
	fmt.Fprintf(w, "aw: timings: %s %s\n", command, strings.Join(parts, " "))
}

func ms(d time.Duration) string { return fmt.Sprintf("%.3fms", float64(d.Microseconds())/1000) }
