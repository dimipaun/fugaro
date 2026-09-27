// Package logtail keeps the last lines written to it, bounded in count and
// in length, for the log tail a draft PR carries (design §4.5).
package logtail

import (
	"bytes"
	"strings"
	"sync"
)

// Bounds used for draft PR log tails: at most 40 lines of at most 300 bytes,
// so a tail never adds more than about 12 KB to a PR comment.
const (
	DefaultLines     = 40
	DefaultLineBytes = 300
)

// cutMark ends a line that was clipped.
const cutMark = " …"

// Clip returns line clipped to at most maxBytes bytes, without splitting a
// rune, and marked as cut when anything was removed. Callers that must
// redact a line clip it only after redacting it, so a secret straddling
// the cut is still recognized whole.
func Clip(line string, maxBytes int) string {
	if len(line) <= maxBytes {
		return line
	}
	return strings.ToValidUTF8(line[:maxBytes], "") + cutMark
}

// Writer is an io.Writer that remembers the last lines written to it. It is
// safe for concurrent use, so one Writer can take a command's stdout and
// stderr at once. Memory stays bounded however long a line is.
type Writer struct {
	mu       sync.Mutex
	maxLines int
	maxLine  int
	lines    []string
	partial  []byte
	cut      bool // the current line was longer than maxLine
}

// New returns a Writer keeping maxLines lines of at most maxLineBytes bytes each.
func New(maxLines, maxLineBytes int) *Writer {
	return &Writer{maxLines: maxLines, maxLine: maxLineBytes}
}

// Write implements io.Writer. It never fails.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if len(w.partial)+len(chunk) > w.maxLine {
			w.cut = true
		}
		if room := w.maxLine - len(w.partial); room > 0 {
			w.partial = append(w.partial, chunk[:min(room, len(chunk))]...)
		}
		if i < 0 {
			break
		}
		w.push()
		p = p[i+1:]
	}
	return n, nil
}

// push ends the current line. Only the text after the last carriage return
// is kept, so a progress bar redrawn with \r shows its final state.
func (w *Writer) push() {
	w.lines = append(w.lines, w.current())
	if len(w.lines) > w.maxLines {
		w.lines = append(w.lines[:0], w.lines[len(w.lines)-w.maxLines:]...)
	}
	w.partial, w.cut = w.partial[:0], false
}

func (w *Writer) current() string {
	line := strings.TrimRight(string(w.partial), "\r")
	if j := strings.LastIndexByte(line, '\r'); j >= 0 {
		line = line[j+1:]
	}
	if w.cut {
		line = strings.ToValidUTF8(line, "") + cutMark
	}
	return line
}

// Lines returns the kept lines, oldest first, including an unfinished last line.
func (w *Writer) Lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]string(nil), w.lines...)
	if len(w.partial) > 0 || w.cut {
		out = append(out, w.current())
		if len(out) > w.maxLines {
			out = out[len(out)-w.maxLines:]
		}
	}
	return out
}

// String returns Lines joined by newlines.
func (w *Writer) String() string { return strings.Join(w.Lines(), "\n") }
