package cli

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/runview"
)

// The --stream feeds. A listing that is read whole prints one document, and a
// consumer of it can render nothing until the last byte of it has arrived;
// --stream prints the same listing as newline-delimited JSON instead, one
// record per line, so a consumer renders each record as it arrives and reads
// the document it already knew from a CLI that takes the old flag
// (docs/console-improvements spec §7, D2906).
//
// Three kinds of line, and a reader may rely on nothing else:
//
//   - `row`  one element of the whole document's own array, unchanged, under
//     the line's `row` key.
//   - `end`  the last line of a feed that ran to completion: how many rows it
//     carried and the version of the line shape they were written in.
//   - `error` the feed stopped after at least one row: the sentence the
//     command would have printed, one line.
//
// A feed with no `end` line is an interrupted read -- a consumer that compares
// the rows it read with the `end`'s count knows it lost some -- and that is the
// whole reason the count is carried: an empty window and a truncated read are
// otherwise the same bytes.

// runsStreamDocVersion is the version of the stream's own document: the shape
// of the lines `runs ls --stream` writes, which the `end` line carries for a
// reader to check before it reads a feed.
//
// It is not lsDoc's version, because lsDoc has none to carry: the document
// `runs ls --json` prints without --stream is unchanged by this flag, member
// for member and byte for byte, so no member -- a version among them -- could
// be added to it. A stream written by lines is a document of its own, and its
// first version is 1.
//
// Version 2 added the page's own completeness to the end line (D3215): the
// index's two facts, where a version-1 consumer could read them only from the
// whole document. A feed is the shape a consumer draws as its lines arrive, so
// it is the shape most able to print an incomplete page as a whole history,
// which is the one thing the plan's cross-cutting pin forbids a reader
// (docs/console-fixes/implementation-plan.md, IR1's acceptance item 4).
const runsStreamDocVersion = 2

// The line kinds, the words the `type` member carries.
const (
	streamLineRow   = "row"
	streamLineEnd   = "end"
	streamLineError = "error"
)

// streamRowLine is one element of a document's own array, unchanged, under the
// feed's own key. The row is the document's row and not a second shape of it:
// a consumer that read the whole document before reads the same members here,
// in the same order, and the row types are the ones the document's schema and
// the JSON contract already hold.
type streamRowLine struct {
	Type string `json:"type"`
	Row  any    `json:"row"`
}

// streamEndLine closes a feed that ran to completion. Count is how many `row`
// lines the feed carried -- not what the document's totals block says about
// the window, which a feed of a page counts differently -- and Version is
// runsStreamDocVersion, the shape these lines are written in.
//
// The two index members are the document's own (lsDoc), absent for a listing
// the window answered as they are absent there: they say whether the page the
// feed just wrote is the whole history, which is what a consumer drawing rows
// as they arrive cannot otherwise know, and what tells it not to draw an
// incomplete page as a complete one.
type streamEndLine struct {
	Type          string     `json:"type"`
	Count         int        `json:"count"`
	Version       int        `json:"version"`
	IndexComplete *bool      `json:"index_complete,omitempty"`
	FoldedThrough *time.Time `json:"folded_through,omitempty"`
}

// streamErrorLine is a feed that stopped after at least one row. Error is the
// sentence the command would have printed on stderr, made safe and one line:
// a newline kept inside it would forge a second line of the feed, and the text
// can quote an object the bucket's writer wrote (safetext).
type streamErrorLine struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

// streamWriter writes a --stream feed, one compact JSON object per line, the
// shape every line-delimited document in the CLI is written in (the JSON
// contract's section on the line-delimited documents). It is the one writer of
// that shape: the report and budget feeds write through it rather than each
// keeping its own copy of the encoder's rules -- compact, one object per line,
// and the count of the rows that went out.
//
// Nothing is buffered: each line is one write of the encoder to the stream a
// consumer reads, which is what makes the feed arrive line by line rather than
// in one flushed block at the end.
type streamWriter struct {
	w    io.Writer
	enc  *json.Encoder
	rows int
}

func newStreamWriter(w io.Writer) *streamWriter {
	return &streamWriter{w: w, enc: json.NewEncoder(w)}
}

// row writes one element of the whole document's own array, unchanged.
func (s *streamWriter) row(v any) error {
	if err := s.enc.Encode(streamRowLine{Type: streamLineRow, Row: v}); err != nil {
		return err
	}
	s.rows++
	return nil
}

// end writes the line that says the feed ran to completion: the rows it
// carried, the version of the shape they were written in, and what the index
// said of itself. index is nil for a listing the window answered, and for the
// feeds that carry no listing of runs at all (budgetStreamDocVersion's own),
// both of which have nothing to say about an index.
func (s *streamWriter) end(version int, index *lsIndexState) error {
	line := streamEndLine{Type: streamLineEnd, Count: s.rows, Version: version}
	if index != nil {
		complete := index.complete
		line.IndexComplete, line.FoldedThrough = &complete, index.foldedThrough
	}
	return s.enc.Encode(line)
}

// failed closes a feed that did not run to completion, and answers the failure
// the command returns -- so the process exits non-zero whatever the feed could
// write.
//
// Once a row has gone out, a consumer holds a listing with no end line and no
// way to tell a feed that stopped from a read that is still going, so the
// failure is written as the feed's own `error` line: the sentence the command
// would have printed, in one line. A failure before the first row writes no
// line at all -- there is nothing a consumer could have drawn from the feed,
// and the command's own error is the whole answer.
//
// The error line is attempted even when what failed was a write of a row: a
// writer that broke on one line is not evidence that it cannot take another,
// and a feed that cannot carry its own `error` line is still a feed that must
// fail loudly (the answer joins the write failure in). It is written through an
// encoder of its own, because an encoder that has seen a write fail keeps the
// failure and refuses every line after it -- which is the behaviour a feed
// wants of a row and not of the line that says the feed is over.
func (s *streamWriter) failed(err error) error {
	if s.rows == 0 {
		return err
	}
	line := streamErrorLine{Type: streamLineError, Error: oneLine(ErrorText(err))}
	if werr := json.NewEncoder(s.w).Encode(line); werr != nil {
		return errors.Join(err, werr)
	}
	return err
}

// streamRows writes a listing as a --stream feed: one line per row, in the
// order the whole document's runs array has, then the end line. The rows are
// the page the listing prints, so a feed of a page and the document of that
// page carry the same rows, and index -- the index the page came from, nil for
// a listing the window answered -- rides the end line, so a feed never draws a
// page of an index that is still building as the whole history (D3215).
//
// The rows are written after the window has been read, and they cannot be
// written before it: a row carries the state of the pull request it is on
// (runview.Row.PRState), which is read from every run of that pull request the
// listing read -- a fact about rows that arrive later -- and the listing's own
// order is over the window, not over the reads. What the flag buys a consumer
// is a document it renders as its lines arrive, rather than one it must hold
// whole before it can draw the first row.
func streamRows(w io.Writer, rows []runview.Row, index *lsIndexState) error {
	sw := newStreamWriter(w)
	for _, r := range rows {
		if err := sw.row(r); err != nil {
			return sw.failed(err)
		}
	}
	if err := sw.end(runsStreamDocVersion, index); err != nil {
		return sw.failed(err)
	}
	return nil
}
