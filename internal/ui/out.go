package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Out renders what a command reports, from one description and for two audiences (AGENTS.md, "Two
// audiences"). For a person at a terminal: steps that redraw in place while they run and collapse
// to one line when done, colour for warnings and errors, and tables that fit the width. For
// anything else, a pipe, a log or any agent session: plain, stable lines, the same text without
// colour or cursor movement, one line per finished step.
//
// Everything a step said also goes to the run's log when there is one, so a failure can name it.
type Out struct {
	w     io.Writer
	live  bool // redraw running steps in place
	color bool
	width int

	mu      sync.Mutex
	running []*Step
	drawn   int // lines of the live region on screen
	stop    chan struct{}
	logf    *os.File
	logPath string
}

// agentMarkers are the environment variables harnesses set in their tool calls. An agent reads
// what it is given: redrawn lines and colour codes only cost it, and can mislead it.
var agentMarkers = []string{"CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CODEX_THREAD_ID", "GROK_SESSION_ID", "OPENCODE_SESSION_ID"}

// Agent reports whether this process runs in an agent's tool call, as far as the environment says.
func Agent() bool {
	for _, m := range agentMarkers {
		if os.Getenv(m) != "" {
			return true
		}
	}
	return false
}

// NewOut renders to w: live and in colour when w is a terminal and no agent is reading it.
func NewOut(w io.Writer) *Out {
	o := &Out{w: w}
	if f, ok := w.(*os.File); ok && !Agent() {
		o.color = Color(f)
		o.live = o.color && IsTerminal(f)
		o.width = Width(f)
	}
	return o
}

// Plain renders to w without colour or redrawing, whatever w is.
func Plain(w io.Writer) *Out { return &Out{w: w} }

// Live reports whether running steps are redrawn in place.
func (o *Out) Live() bool { return o.live }

// LogTo keeps everything this run says, plain, in dir/<name>-<time>.log, and keeps only the newest
// keep of them (and none older than maxAge). It returns the log's path, or "" when none could be
// made: a log is a help, never a reason to fail.
func (o *Out) LogTo(dir, name string, keep int, maxAge time.Duration) string {
	if os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%d.log", name, time.Now().Format("20060102-150405.000"), os.Getpid()))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return ""
	}
	o.mu.Lock()
	o.logf, o.logPath = f, path
	o.mu.Unlock()
	pruneLogs(dir, keep, maxAge)
	return path
}

// LogPath is the run's log, or "".
func (o *Out) LogPath() string { return o.logPath }

func pruneLogs(dir string, keep int, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var logs []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e)
		}
	}
	slices.SortFunc(logs, func(a, b os.DirEntry) int { return mtime(b).Compare(mtime(a)) })
	for i, e := range logs {
		if i >= keep || (maxAge > 0 && time.Since(mtime(e)) > maxAge) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func mtime(e os.DirEntry) time.Time {
	if info, err := e.Info(); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// Close stops redrawing and closes the log.
func (o *Out) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.clearRegion()
	if o.stop != nil {
		close(o.stop)
		o.stop = nil
	}
	if o.logf != nil {
		o.logf.Close()
		o.logf = nil
	}
}

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
)

func (o *Out) paint(code, s string) string {
	if !o.color {
		return s
	}
	return code + s + reset
}

// emit prints finished lines above the live region, and logs them.
func (o *Out) emit(lines ...string) {
	o.clearRegion()
	for _, l := range lines {
		fmt.Fprintln(o.w, l)
	}
	o.drawRegion()
}

func (o *Out) log(format string, args ...any) {
	if o.logf != nil {
		fmt.Fprintf(o.logf, format+"\n", args...)
	}
}

// Heading starts a part of the command's work, so that a reader can see where one concern ends
// and the next begins.
func (o *Out) Heading(text string) {
	text = Clean(text)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log("== %s", text)
	if o.color {
		o.emit("", o.paint(bold, text))
		return
	}
	o.emit(text)
}

// Line prints a line of ordinary output.
func (o *Out) Line(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log("%s", text)
	o.emit(text)
}

// Warn prints a warning: something reversible and worth knowing, which did not stop the command.
func (o *Out) Warn(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log("warning: %s", text)
	o.emit(o.paint(yellow, "warning: ") + text)
}

// Error prints a problem: something that failed and needs a decision.
func (o *Out) Error(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log("error: %s", text)
	o.emit(o.paint(red, "error: ") + text)
}

// Summary ends a command with what happened and what needs the reader, so that the last lines on a
// terminal are the ones that matter. It prints nothing without lines.
func (o *Out) Summary(lines ...string) {
	if len(lines) == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := []string{""}
	for _, l := range lines {
		o.log("summary: %s", l)
		out = append(out, l)
	}
	o.emit(out...)
}

// Table prints rows in columns that fit the width: every column but flex keeps its widest cell,
// and flex, usually a purpose or a path, is cut to what is left. Rows do not wrap.
func (o *Out) Table(rows [][]string, flex int) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) {
				widths[i] = max(widths[i], utf8.RuneCountInString(Clean(c)))
			}
		}
	}
	if o.width > 0 && flex >= 0 && flex < len(widths) {
		used := 0
		for i, w := range widths {
			if i != flex {
				used += w + 2
			}
		}
		widths[flex] = max(10, min(widths[flex], o.width-used))
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var lines []string
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			c = Clean(c)
			if i == len(r)-1 {
				cells[i] = Fit(c, widths[i]) // no padding after the last column
				continue
			}
			c = Fit(c, widths[i])
			cells[i] = c + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c))
		}
		line := strings.TrimRight(strings.Join(cells, "  "), " ")
		o.log("%s", line)
		lines = append(lines, Fit(line, o.width))
	}
	o.emit(lines...)
}

// Step is one piece of a command's work that takes a while, such as installing a project's
// dependencies.
type Step struct {
	o       *Out
	name    string
	detail  string
	started time.Time
	beat    time.Time // when plain output last said it was still going
}

// plainBeat is how often plain output says a step is still running.
const plainBeat = 30 * time.Second

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Step starts one. On a terminal it is shown, with a spinner and its latest detail, until it ends;
// elsewhere nothing is printed until it ends, except a line every 30 seconds while it runs.
func (o *Out) Step(name string) *Step {
	s := &Step{o: o, name: Clean(name), started: time.Now()}
	s.beat = s.started
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log("-> %s", s.name)
	o.running = append(o.running, s)
	if o.live {
		o.clearRegion()
		o.drawRegion()
		if o.stop == nil {
			o.stop = make(chan struct{})
			go o.animate(o.stop)
		}
		return s
	}
	if o.stop == nil {
		o.stop = make(chan struct{})
		go o.animate(o.stop)
	}
	return s
}

func (o *Out) animate(stop chan struct{}) {
	tick := 100 * time.Millisecond
	if !o.live {
		tick = time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			o.mu.Lock()
			if o.live {
				o.clearRegion()
				o.drawRegion()
			} else {
				for _, s := range o.running {
					if time.Since(s.beat) >= plainBeat {
						s.beat = time.Now()
						line := fmt.Sprintf("%s: still running (%s)", s.name, since(s.started))
						o.log("%s", line)
						fmt.Fprintln(o.w, line)
					}
				}
			}
			o.mu.Unlock()
		}
	}
}

// Detail is what the step is doing now, such as the last line of a command's output. It is shown
// on a terminal and kept in the log.
func (s *Step) Detail(line string) {
	line = strings.TrimSpace(Clean(line))
	if line == "" {
		return
	}
	o := s.o
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log("   %s", line)
	s.detail = line
}

// Done ends the step as a success, with what it did.
func (s *Step) Done(note string) { s.end(true, note) }

// Skip ends the step without having to do anything.
func (s *Step) Skip(note string) { s.end(true, note) }

// Fail ends the step as a failure, with why. The run's log, if there is one, is named.
func (s *Step) Fail(why string) { s.end(false, why) }

func (s *Step) end(ok bool, note string) {
	o := s.o
	o.mu.Lock()
	defer o.mu.Unlock()
	o.running = slices.DeleteFunc(o.running, func(r *Step) bool { return r == s })
	took := ""
	if d := time.Since(s.started); d >= time.Second {
		took = " (" + since(s.started) + ")"
	}
	note = Clean(note)
	line := s.name
	if note != "" {
		line += ": " + note
	}
	if ok {
		o.log("ok %s%s", line, took)
	} else {
		o.log("FAILED %s%s", line, took)
	}
	switch {
	case o.color && ok:
		o.emit(o.paint(green, "✓ ") + line + o.paint(dim, took))
	case o.color:
		o.emit(o.paint(red, "✗ ") + line + o.paint(dim, took))
	case ok:
		o.emit(line + took)
	default:
		o.emit("error: " + line + took)
	}
	if len(o.running) == 0 && o.stop != nil {
		close(o.stop)
		o.stop = nil
	}
}

func since(t time.Time) string { return time.Since(t).Round(time.Second).String() }

// clearRegion removes the live region from the screen; the caller holds o.mu.
func (o *Out) clearRegion() {
	if !o.live || o.drawn == 0 {
		return
	}
	fmt.Fprintf(o.w, "\033[%dA", o.drawn)
	for i := 0; i < o.drawn; i++ {
		fmt.Fprint(o.w, "\r\033[K\n")
	}
	fmt.Fprintf(o.w, "\033[%dA", o.drawn)
	o.drawn = 0
}

// drawRegion draws a line for each running step; the caller holds o.mu.
func (o *Out) drawRegion() {
	if !o.live || len(o.running) == 0 {
		return
	}
	frame := spinner[int(time.Now().UnixMilli()/100)%len(spinner)]
	for _, s := range o.running {
		line := fmt.Sprintf("%s %s", frame, s.name)
		if s.detail != "" {
			line += o.paint(dim, " · "+s.detail)
		}
		line += o.paint(dim, " ("+since(s.started)+")")
		// The region must not wrap, or redrawing it would leave lines behind.
		fmt.Fprintf(o.w, "\r\033[K%s\n", fitVisible(line, o.width))
	}
	o.drawn = len(o.running)
}

// fitVisible cuts a line with colour codes to width visible runes.
func fitVisible(s string, width int) string {
	if width <= 0 {
		return s
	}
	var b strings.Builder
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case r == '\033':
			inEsc = true
		case inEsc:
			if r >= '@' && r <= '~' && r != '[' {
				inEsc = false
			}
		default:
			if n >= width-1 {
				b.WriteString("…" + reset)
				return b.String()
			}
			n++
		}
		b.WriteRune(r)
	}
	return b.String()
}
