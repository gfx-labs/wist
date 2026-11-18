package ask

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// Colors for stderr lines.
const (
	plain  = ""
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
	reset  = "\x1b[0m"
)

// progress writes stderr diagnostics. On a terminal it keeps a spinner on
// the last line, erased before anything else is written. mu also guards
// the runner's turn state, since updates and output interleave.
type progress struct {
	mu     sync.Mutex
	w      io.Writer
	tty    bool // w is a terminal: spinner and \r erasing
	color  bool
	shared bool // stdout is a terminal too, so text and spinner share the screen
	label  string
	shown  bool // a spinner frame is on screen
	frame  int
	// pause is set by the runner while stdout text is mid-line on a
	// shared terminal; the spinner then stays hidden.
	pause bool
	quit  chan struct{}
	once  sync.Once
}

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newProgress(w io.Writer, tty, color, shared bool) *progress {
	p := &progress{w: w, tty: tty, color: color, shared: shared, quit: make(chan struct{})}
	if tty {
		go p.tick()
	}
	return p
}

func (p *progress) tick() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.quit:
			return
		case <-t.C:
			p.mu.Lock()
			p.draw()
			p.mu.Unlock()
		}
	}
}

// draw shows the next spinner frame; mu is held.
func (p *progress) draw() {
	if !p.tty || p.label == "" || p.pause {
		return
	}
	p.frame++
	f := frames[p.frame%len(frames)]
	if p.color {
		fmt.Fprintf(p.w, "\r\x1b[2K%s%s %s%s", cyan, f, p.label, reset)
	} else {
		fmt.Fprintf(p.w, "\r\x1b[2K%s %s", f, p.label)
	}
	p.shown = true
}

// clear erases the spinner; mu is held.
func (p *progress) clear() {
	if p.shown {
		fmt.Fprint(p.w, "\r\x1b[2K")
		p.shown = false
	}
}

// spin sets the spinner label.
func (p *progress) spin(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.label = label
}

// line prints one diagnostic line.
func (p *progress) line(color, s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lineLocked(color, s)
}

func (p *progress) lineLocked(color, s string) {
	p.clear()
	s = strings.TrimRight(s, "\n")
	if p.color && color != "" {
		fmt.Fprintf(p.w, "%s%s%s\n", color, s, reset)
	} else {
		fmt.Fprintln(p.w, s)
	}
}

func (p *progress) stop() {
	p.once.Do(func() {
		close(p.quit)
		p.mu.Lock()
		p.label = ""
		p.clear()
		p.mu.Unlock()
	})
}
