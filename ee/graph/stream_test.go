// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

// The framing of `graph --watch --json` (CG7 of
// docs/cli-output-console-graph/implementation-plan-graph.md), which is what is
// left of the stream once the database is taken away: the identity a child
// draws once and counts within, the two clocks that decide when a document is
// due, and the line a document is written as.
//
// What these tests cannot hold is the loop that feeds them — a change arriving,
// a transport falling over, a reader that stops reading — and that is
// ee/graph/cli/watch_test.go's, which drives the whole child against a fake
// database.

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestStreamDrawsOneIdentityAndCountsWithinIt is H4 on the framing side: a
// child's stream_id is drawn once and printed in every document it writes, and
// the sequence rises by one per document within that id. The pair is what a
// reader compares: a new stream_id is a whole new snapshot whatever numbers the
// two carry, which is why a document that starts a stream must not be confused
// with one that continues it.
func TestStreamDrawsOneIdentityAndCountsWithinIt(t *testing.T) {
	first, err := NewStream()
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	second, err := NewStream()
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	// Two children are two streams: the id names a process, so two children of
	// one project must not share one.
	if first.ID == second.ID {
		t.Errorf("two streams drew the same id %q", first.ID)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(first.ID) {
		t.Errorf("the stream id is %q, want 16 hex characters", first.ID)
	}
	if first.Floor != Floor || first.Keep != KeepAlive {
		t.Errorf("NewStream's clocks are %s and %s, want the contract's %s and %s", first.Floor, first.Keep, Floor, KeepAlive)
	}

	// Three documents of one stream: one identity, and 1, 2, 3.
	base := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	var seen []string
	for i := 1; i <= 3; i++ {
		now := base.Add(time.Duration(i) * time.Second)
		doc := first.Next(Document{Sources: []SourceState{{Source: "coordinator", State: SourceUp}}}, now)
		if doc.StreamID != first.ID {
			t.Errorf("document %d carries stream_id %q, want the stream's own %q", i, doc.StreamID, first.ID)
		}
		if doc.Seq != uint64(i) {
			t.Errorf("document %d carries seq %d, want %d", i, doc.Seq, i)
		}
		if doc.Version != DocumentVersion {
			t.Errorf("document %d carries version %d, want %d", i, doc.Version, DocumentVersion)
		}
		if !doc.GeneratedAt.Equal(now) {
			t.Errorf("document %d was stamped %s, want the instant it was given %s", i, doc.GeneratedAt, now)
		}
		seen = append(seen, doc.StreamID)
	}
	if len(seen) != 3 || seen[0] != seen[2] {
		t.Errorf("the documents' stream ids are %v, want one value three times", seen)
	}
}

// TestStreamDueIsAFloorAndThenAKeepAlive: a stream owes its first document at
// once, a change once the floor has passed, and — with nothing to say at all —
// a keep-alive once the interval has passed. The floor is what keeps a burst of
// changes from being a burst of documents (H5), and it delays rather than
// drops: a change inside it is still owed on the next ask.
func TestStreamDueIsAFloorAndThenAKeepAlive(t *testing.T) {
	s, err := NewStream()
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	s.Floor, s.Keep = 10*time.Second, 30*time.Second
	start := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

	if !s.Due(start, false) {
		t.Errorf("the first document was not due: a stream owes one whatever the sources say")
	}
	s.Next(Document{}, start)

	for _, tc := range []struct {
		after   time.Duration
		changed bool
		want    bool
		why     string
	}{
		{0, true, false, "a change at the instant of the last document is inside the floor"},
		{9 * time.Second, true, false, "a change 9s later is still inside a 10s floor"},
		{10 * time.Second, true, true, "a change at the floor is due"},
		{10 * time.Second, false, false, "nothing changed, and the keep-alive is 30s away"},
		{29 * time.Second, false, false, "a quiet stream at 29s is not due a keep-alive"},
		{30 * time.Second, false, true, "a quiet stream at 30s is due its keep-alive"},
	} {
		if got := s.Due(start.Add(tc.after), tc.changed); got != tc.want {
			t.Errorf("Due(+%s, changed=%v) = %v, want %v: %s", tc.after, tc.changed, got, tc.want, tc.why)
		}
	}

	// A keep-alive is an ordinary document of the stream, not a shape of its
	// own: it takes the next sequence, so a reader that sees a gap knows it
	// missed one (F20).
	quiet := s.Next(Document{}, start.Add(30*time.Second))
	if quiet.Seq != 2 {
		t.Errorf("the keep-alive took seq %d, want 2: a keep-alive is a document of the stream like any other", quiet.Seq)
	}
}

// TestStreamWritesOneCompactDocumentPerLine is the shape a console reads: one
// JSON object per line, compact, with every list an array — an empty list is a
// fact and a null is not — and the error of a writer that refuses returned
// rather than swallowed, because that error is how a closed stdout ends the
// child (F11).
func TestStreamWritesOneCompactDocumentPerLine(t *testing.T) {
	var buf strings.Builder
	doc := Document{Version: DocumentVersion, StreamID: "abc", Seq: 1,
		Sources: []SourceState{{Source: "coordinator", State: SourceDown}}}
	if err := WriteDoc(&buf, doc); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}
	line := buf.String()
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("the document is not one line: %q", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("one document wrote %d lines: %q", strings.Count(line, "\n"), line)
	}
	if strings.Contains(line, "\n  ") {
		t.Errorf("the document is indented, want one compact object per line: %q", line)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &raw); err != nil {
		t.Fatalf("the line is not one JSON object: %v: %q", err, line)
	}
	// The lists a caller left out are arrays, never null: the same rule the
	// one-shot read's MarshalJSON keeps, because the same reader reads both.
	for _, key := range []string{"nodes", "edges", "warnings"} {
		if got := string(raw[key]); got != "[]" {
			t.Errorf("the document's %s is %s, want []", key, got)
		}
	}

	// A writer that refuses — a stdout whose reader is gone — is the child's
	// own end, so its error comes back rather than being swallowed.
	boom := errors.New("write: broken pipe")
	if err := WriteDoc(failingWriter{err: boom}, doc); !errors.Is(err, boom) {
		t.Errorf("WriteDoc over a refusing writer returned %v, want %v", err, boom)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

var _ io.Writer = failingWriter{}
