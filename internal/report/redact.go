package report

import (
	"bytes"
	"io"
	"slices"
	"strings"
)

// Redacted is what every value the run holds is replaced with in the log: the
// spelling the CLI's own output already uses, in the converge's lines
// (internal/initflow/loop.go) and in the cloud-side redactors (gateway, rtdb,
// firestore, credsvc, bitbucket). A failed run prints the path of this log, so
// the person reads the terminal and the log side by side and both have to say
// the same word. internal/agent's redactor spells it [REDACTED] for the
// agent's own stream, which is a different surface and not one this writes.
const Redacted = "[redacted]"

// Redactor is the hook between a run's own output and the log: the lines are
// made safe before they are kept. It buffers until a newline and redacts
// whole lines, because a value can arrive split across two writes — Terraform
// and the stages write in whatever chunks the child produced — and a
// redactor that worked per write would let that value through in two halves
// (F6).
//
// The values are read from secrets at each line rather than once, because the
// converge's list is a set of live slots: the secrets stage fills a slot in
// the middle of the run, and the log has to hold what the run holds at the
// moment the line was written. It is not internal/agent's Redactor for that
// reason and one more: this package is the CLI's output layer and must not
// carry the agent's dependencies.
//
// Two limits follow from matching a value inside a line, and both are the
// caller's to know. A value that spans lines is never matched whole: a caller
// holding one holds its lines too (initsecrets.hold does, for a PEM), and the
// whole value is then still caught, line by line. And a value that is a
// substring of Redacted — `TED`, `EDACT` — survives inside the marker, which
// no fixed marker can avoid; a value of that shape is not one a slot holds.
type Redactor struct {
	out     io.Writer
	secrets func() []string
	buf     []byte
}

// NewRedactor returns a Redactor writing to out, with secrets read at each
// line. A nil secrets is a run with no value to hide.
func NewRedactor(out io.Writer, secrets func() []string) *Redactor {
	return &Redactor{out: out, secrets: secrets}
}

// Write buffers p and writes back every complete line it completes.
func (r *Redactor) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		if err := r.emit(r.buf[:i+1]); err != nil {
			return 0, err
		}
		r.buf = r.buf[i+1:]
	}
}

// Flush writes the partial line a run ended in the middle of, which is what
// keeps the last line of a failed step in the log.
func (r *Redactor) Flush() error {
	if len(r.buf) == 0 {
		return nil
	}
	err := r.emit(r.buf)
	r.buf = nil
	return err
}

func (r *Redactor) emit(line []byte) error {
	_, err := io.WriteString(r.out, r.redact(string(line)))
	return err
}

// redact replaces every value the run holds at this moment, the longest value
// first: one value is often inside another — an account id inside the
// connection string that holds it — and replacing the shorter first would
// leave the tail of the longer one in the log, where nothing would ever look
// for it again. The values are copied rather than sorted in place, because
// what secrets returns is the caller's own slice, still live for the stages
// that fill it.
func (r *Redactor) redact(line string) string {
	if r.secrets == nil {
		return line
	}
	vals := slices.Clone(r.secrets())
	slices.SortStableFunc(vals, func(a, b string) int { return len(b) - len(a) })
	for _, v := range vals {
		// An empty value is a substring of every string, and replacing one
		// would put the marker between every character of every line. It is
		// also the normal state of a converge slot no stage has reached yet.
		if v == "" {
			continue
		}
		line = strings.ReplaceAll(line, v, Redacted)
	}
	return line
}
