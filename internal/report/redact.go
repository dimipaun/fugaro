package report

import (
	"bytes"
	"io"
	"sort"
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
// Three limits follow from matching a value inside a line, and all three are
// the caller's to know. A value that spans lines is never matched whole: a
// caller holding one holds its lines too (initsecrets.hold does, for a PEM),
// and the whole value is then still caught, line by line. A value that is a
// substring of Redacted — `TED`, `EDACT` — survives inside the marker. And a
// value that contains the marker — `a[redacted]` — can be put back together by
// a shorter value's replacement on the same line, as `aXb` becomes
// `a[redacted]b` when `X` is held. The last two both follow from the marker
// being written out verbatim, which no fixed marker can avoid, and neither is
// a shape a slot holds.
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

// redact replaces every value the run holds at this moment. Every value is
// matched against the line as it arrived and the matches are merged before
// anything is written, because two values can cover the same stretch of a line
// without either containing the other — an account id and the token that
// follows it, or two values that meet at a separator. Replacing them one value
// at a time would let the first replacement destroy the second value's match
// and leave the rest of it in the log, where nothing would ever look for it
// again.
func (r *Redactor) redact(line string) string {
	if r.secrets == nil {
		return line
	}
	spans := matchSpans(line, r.secrets())
	if len(spans) == 0 {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	at := 0
	for _, s := range spans {
		b.WriteString(line[at:s.start])
		b.WriteString(Redacted)
		at = s.end
	}
	b.WriteString(line[at:])
	return b.String()
}

// span is one stretch of a line to replace, as byte offsets.
type span struct{ start, end int }

// matchSpans returns the stretches of line that the held values cover, in
// ascending order and never overlapping: matches that overlap, or that touch,
// are one stretch, so one replacement covers both.
//
// Every value is matched against the original line rather than against what an
// earlier replacement left, which is what keeps a value whose match overlaps
// another's from being missed.
func matchSpans(line string, values []string) []span {
	var found []span
	for _, v := range values {
		// An empty value is a substring of every string, and matching one
		// would put the marker between every character of every line. It is
		// also the normal state of a converge slot no stage has reached yet.
		if v == "" {
			continue
		}
		for at := 0; ; {
			i := strings.Index(line[at:], v)
			if i < 0 {
				break
			}
			found = append(found, span{at + i, at + i + len(v)})
			// One byte on, not len(v): a value can overlap itself, and the
			// occurrence that starts inside this one is a match too.
			at += i + 1
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].start != found[j].start {
			return found[i].start < found[j].start
		}
		return found[i].end < found[j].end
	})
	merged := make([]span, 0, len(found))
	for _, s := range found {
		if n := len(merged); n > 0 && s.start <= merged[n-1].end {
			if s.end > merged[n-1].end {
				merged[n-1].end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}
