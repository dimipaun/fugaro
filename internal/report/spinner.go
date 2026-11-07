package report

import (
	"fmt"
	"time"
)

// spinFrames are the frames of the line a step in flight is drawn on, in
// order: the plainest alphabet there is, so a narrow font and a console that
// is not quite a terminal both draw it.
var spinFrames = []string{"|", "/", "-", "\\"}

// spinInterval is how fast that line advances. It is a variable rather than a
// constant so a test can put the ticker out of its own way.
var spinInterval = 100 * time.Millisecond

// startSpinner draws the step in flight on the terminal's own line and keeps
// redrawing it until the step ends. This is the whole of the spinner: a
// carriage-return writer of our own, and nothing at all where the rules
// forbid one (G4) — off a terminal, under --verbose, in --json, or with
// colour off, the step prints only its one result line.
//
// It is called with the reporter's lock held and the goroutine it starts
// takes that same lock for every frame, so a frame can never land inside a
// step's own line.
func (r *Reporter) startSpinner(name string) {
	if !r.spin {
		return
	}
	r.spinName, r.spinFrame = name, 0
	r.drawRunning()
	stop := make(chan struct{})
	r.spinStop = stop
	// The interval is read here, once, rather than inside the goroutine: it is
	// a variable a test replaces, and a read of it after the test restored the
	// old value would race with that write.
	interval := spinInterval
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				r.mu.Lock()
				if r.spinStop == stop { // still the step in flight
					r.spinFrame++
					r.drawRunning()
				}
				r.mu.Unlock()
			}
		}
	}()
}

// drawRunning redraws the line. A carriage return is all it takes, because
// the line it returns to holds nothing but the previous frame. Called with
// the lock held.
func (r *Reporter) drawRunning() {
	frame := spinFrames[r.spinFrame%len(spinFrames)]
	fmt.Fprintf(r.o.Out, "\r%s %s", r.painted(frame, colourRunning), r.spinName)
}

// stopSpinner ends the redrawing and clears the line, so the step's result —
// or the closing line, which is shorter — takes its place. It is called with
// the lock held and is safe to call when nothing is drawn.
func (r *Reporter) stopSpinner() {
	if r.spinStop == nil {
		return
	}
	close(r.spinStop)
	r.spinStop = nil
	fmt.Fprint(r.o.Out, "\r\x1b[K")
}
