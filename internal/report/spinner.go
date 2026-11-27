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

// heartbeatInterval is how often a step with no line in flight says it is
// still running: a minute, which is how long silence takes to read as a hang
// (D3436). A variable for the same reason spinInterval is.
var heartbeatInterval = time.Minute

// startSpinner draws the step in flight on the terminal's own line and keeps
// redrawing it until the step ends — and where no line may be drawn (G4), off
// a terminal, under --verbose, in --json or with colour off, it prints one
// "still …" line a minute instead, so neither reader faces minutes of silence
// (D3436).
//
// It is called with the reporter's lock held and the goroutine it starts
// takes that same lock for every line it draws, so a line can never land
// inside a step's own line.
func (r *Reporter) startSpinner() {
	if r.spin {
		r.drawRunning()
	}
	stop := make(chan struct{})
	r.spinStop = stop
	// The interval is read here, once, rather than inside the goroutine: it is
	// a variable a test replaces, and a read of it after the test restored the
	// old value would race with that write.
	interval := heartbeatInterval
	if r.spin {
		interval = spinInterval
	}
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
					if r.spin {
						r.spinFrame++
						r.drawRunning()
					} else {
						r.beat()
					}
				}
				r.mu.Unlock()
			}
		}
	}()
}

// drawRunning redraws the line, with how long the step has been running. A
// carriage return is all it takes, because the line it returns to holds
// nothing but the previous frame. Called with the lock held.
func (r *Reporter) drawRunning() {
	frame := spinFrames[r.spinFrame%len(spinFrames)]
	fmt.Fprintf(r.o.Out, "\r%s %s (%s)", r.painted(frame, colourRunning), r.spinName, r.elapsed())
}

// beat prints the one line a step with no line in flight says: that it is
// still running, and for how long. It goes to the human writer and not to the
// log, because it is the screen's liveness rather than something the run did —
// the log holds the step's own output. Called with the lock held.
func (r *Reporter) beat() {
	fmt.Fprintf(r.human, "still %s (%s)\n", r.spinName, r.elapsed())
}

// elapsed is how long the step in flight has been running, read from the
// reporter's clock. Called with the lock held.
func (r *Reporter) elapsed() string {
	return elapsedText(r.o.Now().Sub(r.spinSince))
}

// stopSpinner ends the redrawing and clears the line, so the step's result —
// or the closing line, which is shorter — takes its place. It is called with
// the lock held and is safe to call when nothing is drawn. Only a line that
// was drawn is cleared: a heartbeat's line ended on its own newline, and a
// carriage return is not allowed on a writer no spinner may be drawn on.
func (r *Reporter) stopSpinner() {
	if r.spinStop == nil {
		return
	}
	close(r.spinStop)
	r.spinStop = nil
	if r.spin {
		fmt.Fprint(r.o.Out, "\r\x1b[K")
	}
}

// elapsedText is how long a step has been running, as its own line reads it:
// whole seconds under a minute, then minutes and seconds. It is a clock a
// person glances at, not a stopwatch.
func elapsedText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
