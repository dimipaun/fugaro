// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// The stream's own rules: `graph --watch --json` prints whole documents rather
// than event deltas (D1360), so this file is the identity of one child's
// stream, the sequence it counts and the two clocks that decide when it owes a
// document. It is CG7 of
// docs/cli-output-console-graph/implementation-plan-graph.md.
//
// It is pure, like the rest of this package: it reads nothing, opens nothing
// and holds no clock of its own — a caller passes the time in and owns the
// writer — so the whole of the framing can be tested without a database, a
// terminal or a goroutine.
//
// Three of the guarantees the console's contract carries (§6.22, H4, H5) are
// here rather than in the command, because they are properties of the document
// rather than of the read that filled it:
//
//   - **A whole document per change, and a floor between two of them.** A
//     reader that loses one needs no repair: the next one is the whole graph
//     (F19). The floor is `watch --json`'s own (one second, the value
//     `watchJSONGap` holds and §10 of the contract describes), so a project
//     that changes in a burst costs one document a second rather than one per
//     event.
//   - **A keep-alive, because a quiet graph prints nothing.** A console that
//     was hard-killed leaves a child that exits on its next stdout write
//     (F11), which for a graph that does not change could be minutes — so the
//     stream writes a document on a timer even when nothing changed (F20).
//   - **The identity of the stream, and a sequence within it.** `stream_id` is
//     drawn once per child and `seq` rises within it, so a reader compares a
//     `seq` only against the same `stream_id` and reads a new one as a whole
//     new snapshot, whatever numbers the two carry (H4).

// KeepAlive is how long a stream may be quiet before it writes a document with
// nothing to say. It bounds how long an orphaned child survives: the child
// notices that its reader is gone on its next write, and this is the longest
// it waits to make one.
const KeepAlive = 30 * time.Second

// Floor is the least time between two documents of one stream: `watch --json`'s
// own floor, one second, which §6.22 pins as the stream's too. It is a rate
// limit rather than a loss: a change inside the floor is held and printed when
// it passes, never dropped.
const Floor = time.Second

// Stream is one child's stream: the identity every document it writes carries,
// the sequence that rises within it, and the two intervals that decide when a
// document is due. It holds no clock — every method takes the time — so a
// caller can drive it from a timer, a test from a fixed instant.
//
// The zero Stream is not useful: a stream with no Floor writes every change at
// once and one with no Keep never keeps alive. NewStream fills both.
type Stream struct {
	// ID is the stream_id, printed in every document this stream writes.
	ID string
	// Floor is the least time between two documents.
	Floor time.Duration
	// Keep is how long the stream may be quiet before a keep-alive is due.
	Keep time.Duration

	seq  uint64
	last time.Time
}

// NewStream returns a stream with a fresh identity and the two intervals the
// contract pins. The identity is drawn once per child and never again: a
// reconnect inside the child keeps it — the transport re-establishes its own
// subscription and the next document still belongs to this stream — and only a
// new process is a new stream_id (H4).
func NewStream() (Stream, error) {
	id, err := NewStreamID()
	if err != nil {
		return Stream{}, err
	}
	return Stream{ID: id, Floor: Floor, Keep: KeepAlive}, nil
}

// NewStreamID draws one child's stream_id: eight random bytes, as hex. It
// names a process rather than a thing — it is compared only against itself,
// and nothing outside a reader's own memory is keyed by it — so it needs to be
// unlikely to repeat and nothing more.
func NewStreamID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("graph: drawing a stream id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Due reports whether the stream owes a document at now: the first one always,
// a change once the floor since the last document has passed, and a keep-alive
// once the interval has passed with nothing to say.
//
// changed says whether the sources have moved since the last document; a
// change inside the floor is not due yet and is not lost — the caller keeps it
// and asks again.
func (s *Stream) Due(now time.Time, changed bool) bool {
	if s.last.IsZero() {
		return true
	}
	if changed {
		return now.Sub(s.last) >= s.Floor
	}
	return now.Sub(s.last) >= s.Keep
}

// Next stamps doc as this stream's next document and returns it: the version,
// the stream's identity, the next sequence and the time. Every field it writes
// is the stream's rather than the read's, so the caller hands it a document
// built by whichever source and gets back one a reader of the stream can place
// — which is what makes a keep-alive an ordinary document rather than a shape
// of its own (F20).
//
// It counts the document whether or not anything changed: a keep-alive takes
// the next seq like any other, so a reader that sees a gap has missed one.
func (s *Stream) Next(doc Document, now time.Time) Document {
	s.seq++
	doc.Version = DocumentVersion
	doc.StreamID = s.ID
	doc.Seq = s.seq
	doc.GeneratedAt = now
	s.last = now
	return doc
}

// WriteDoc writes doc as the stream's own line: compact JSON, one object per
// line. That is the shape §10 of the contract describes for `watch --json`, so
// its guarantees carry over unchanged — a slow reader makes the child block and
// never drops a document, and a writer that refuses (a stdout whose reader is
// gone) returns its error, which is how a closed stdout ends the child.
func WriteDoc(w io.Writer, doc Document) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("graph: writing the document: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}
