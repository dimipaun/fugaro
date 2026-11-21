// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"encoding/json"
	"errors"
	"io"

	report "github.com/dimipaun/fugaro/ee/report"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// report --stream (docs/console-improvements spec §7, D2906 as widened by O3).
// `report --json` prints one document, and a consumer of it can render nothing
// until the last byte of it has arrived; --stream prints the same report as
// newline-delimited JSON instead, one record per line, so a consumer renders
// each row as it arrives and reads the document it already knew from a CLI
// that takes the old flag.
//
// The lines are RS1's shape (internal/cli/runs_stream.go, schemas/
// runs-stream.schema.json), and this package writes its own: that writer is
// unexported in internal/cli, which ee/ imports only through exported names,
// and a core package may not hold a seam for a paid one. The shape is the same
// three kinds and the same words:
//
//   - `row`  one element of the document's own `rows` array, unchanged, under
//     the line's `row` key.
//   - `end`  the last line of a feed that ran to completion: how many rows it
//     carried, the version of the line shape they were written in, and the
//     document's `totals`.
//   - `error` the feed stopped after at least one row: the sentence the
//     command would have printed, one line.
//
// A feed with no `end` line is an interrupted read, and the count is what tells
// a consumer how many rows it lost: an empty window and a truncated read are
// otherwise the same bytes.
//
// What the feed does not carry is the document's header -- project, source,
// degraded, notice, by, since, until, weeks -- and its warnings. Those are not
// rows and no line of the shape holds them; a consumer that needs them reads
// `report --json`, which is where they are unchanged.

// reportStreamDocVersion is the version of the feed's own line shape: the
// shape of the lines `report --stream` writes, which the `end` line carries for
// a reader to check before it reads a feed.
//
// It is not the document's version, because the document has none to carry:
// `report --json` prints the document it printed before this flag existed,
// member for member and byte for byte, so no member -- a version among them --
// could be added to it. A feed written by lines is a document of its own, and
// its first version is 1.
const reportStreamDocVersion = 1

// The line kinds, the words the `type` member carries. They are RS1's own
// (internal/cli/runs_stream.go): one shape, one vocabulary, whichever command
// writes it.
const (
	streamLineRow   = "row"
	streamLineEnd   = "end"
	streamLineError = "error"
)

// streamRowLine is one element of the document's own `rows` array, unchanged,
// under the feed's own key. The row is the document's row and not a second
// shape of it: report.JSONLines is the one conversion from a Report to these
// values, so a consumer that read the document reads the same members here, in
// the same order.
type streamRowLine struct {
	Type string `json:"type"`
	Row  any    `json:"row"`
}

// streamEndLine closes a feed that ran to completion. Count is how many `row`
// lines the feed carried, Version is reportStreamDocVersion, and Totals is the
// document's own `totals` member -- the one part of the report that is last by
// construction, since it sums the rows that went before it.
type streamEndLine struct {
	Type    string          `json:"type"`
	Count   int             `json:"count"`
	Version int             `json:"version"`
	Totals  report.JSONLine `json:"totals"`
}

// streamErrorLine is a feed that stopped after at least one row. Error is the
// sentence the command would have printed on stderr, made safe and one line: a
// newline kept inside it would forge a second line of the feed, and the text
// can quote a value the database's writer wrote (safetext).
type streamErrorLine struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

// reportStreamWriter writes a `report --stream` feed: one compact JSON object
// per line, and the count of the rows that went out. Nothing is buffered --
// each line is one write of the encoder to the stream a consumer reads, which
// is what makes the feed arrive line by line rather than in one flushed block
// at the end.
type reportStreamWriter struct {
	w    io.Writer
	enc  *json.Encoder
	rows int
}

func newReportStreamWriter(w io.Writer) *reportStreamWriter {
	return &reportStreamWriter{w: w, enc: json.NewEncoder(w)}
}

// row writes one element of the document's own `rows` array, unchanged.
func (s *reportStreamWriter) row(v any) error {
	if err := s.enc.Encode(streamRowLine{Type: streamLineRow, Row: v}); err != nil {
		return err
	}
	s.rows++
	return nil
}

// end writes the line that says the feed ran to completion: the rows it
// carried, the version of the shape they were written in, and the totals.
func (s *reportStreamWriter) end(totals report.JSONLine) error {
	return s.enc.Encode(streamEndLine{Type: streamLineEnd, Count: s.rows, Version: reportStreamDocVersion, Totals: totals})
}

// failed closes a feed that did not run to completion, and answers the failure
// the command returns -- so the process exits non-zero whatever the feed could
// write.
//
// Once a row has gone out, a consumer holds rows with no end line and no way
// to tell a feed that stopped from a read that is still going, so the failure
// is written as the feed's own `error` line: the sentence the command would
// have printed, in one line. A failure before the first row writes no line at
// all -- there is nothing a consumer could have drawn from the feed, and the
// command's own error is the whole answer.
//
// The error line is attempted even when what failed was a write of a row, and
// through an encoder of its own: an encoder that has seen a write fail keeps
// the failure and refuses every line after it, which is the behaviour a feed
// wants of a row and not of the line that says the feed is over.
func (s *reportStreamWriter) failed(err error) error {
	if s.rows == 0 {
		return err
	}
	line := streamErrorLine{Type: streamLineError, Error: safetext.OneLine(fcli.ErrorText(err))}
	if werr := json.NewEncoder(s.w).Encode(line); werr != nil {
		return errors.Join(err, werr)
	}
	return err
}

// streamReport writes rep as a `--stream` feed: one line per element of the
// document's `rows` array, in the document's order, then the end line carrying
// the document's totals.
//
// The rows are written after the window has been read, and they cannot be
// written before it: a report's rows are grouped over every record of the
// window and ordered over them all -- by period, or by spend descending -- and
// its totals are the sum of the rows, which is why the totals are the one part
// of the document that is last by construction. What the flag buys a consumer
// is a report it draws as its lines arrive, rather than one it must hold whole
// before it can draw the first row.
func streamReport(w io.Writer, rep report.Report) error {
	rows, totals := report.JSONLines(rep)
	sw := newReportStreamWriter(w)
	for _, r := range rows {
		if err := sw.row(r); err != nil {
			return sw.failed(err)
		}
	}
	if err := sw.end(totals); err != nil {
		return sw.failed(err)
	}
	return nil
}
