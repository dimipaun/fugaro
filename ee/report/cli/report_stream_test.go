// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	fcli "github.com/dimipaun/fugaro/internal/cli"
)

// reportStreamFixture is the window the feed's tests report on: three final
// days over two people and, by model, two models, so the feed carries more
// than one row, rows of both the by-day and the by-model groupings, and a
// totals row that is no row's own figure. Every day is final, so the report
// reads the history and the RTDB fake answers nothing for the window's
// unarchived days: the same bytes on every run.
func reportStreamFixture(t *testing.T) {
	t.Helper()
	f := newReportFixture(t)
	f.doc("2026-10-14", repOpts{spent: 1_500_000, compute: 200_000, estimated: 1, final: true, person: "a@x.io", model: "m1"})
	f.doc("2026-10-15", repOpts{spent: 2_000_000, notional: 700_000, final: true, person: "b@x.io", model: "m2"})
	f.doc("2026-10-16", repOpts{spent: 500_000, final: true, person: "a@x.io", model: "m1"})
}

// reportStreamArgs is the command the feed's tests run: --json, like every
// consumer of the feed, over the fixture's window.
func reportStreamArgs() []string {
	return []string{"report", "--json", "--stream", "--since", "2026-10-10", "--until", "2026-10-20"}
}

// reportStreamEnd is the feed's end line as a consumer reads it. The totals are
// raw JSON, so a test can hold them to the document's own bytes rather than to
// a second decoding of them.
type reportStreamEnd struct {
	Type    string          `json:"type"`
	Count   int             `json:"count"`
	Version int             `json:"version"`
	Totals  json.RawMessage `json:"totals"`
}

// reportStreamFeed is a feed read back as a consumer reads it: the rows it
// carried in the order it carried them, the end line it ended with (if it
// did), and the sentence it stopped with (if it stopped).
type reportStreamFeed struct {
	rows []json.RawMessage
	end  *reportStreamEnd
	fail string
	last string // the type of the last line, "" for an empty feed
}

// readReportFeed reads a feed the way a consumer does -- one line at a time --
// and fails on anything outside the three line kinds: a line the shape does not
// have is a feed no CLI wrote, and a test that skipped it would pass on a feed
// nothing could read.
func readReportFeed(t *testing.T, out string) reportStreamFeed {
	t.Helper()
	var f reportStreamFeed
	for i, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			t.Fatalf("line %d of the feed is not JSON: %v:\n%s", i+1, err, line)
		}
		switch head.Type {
		case streamLineRow:
			var l struct {
				Type string          `json:"type"`
				Row  json.RawMessage `json:"row"`
			}
			if err := json.Unmarshal([]byte(line), &l); err != nil {
				t.Fatalf("line %d is a row line the shape does not have: %v:\n%s", i+1, err, line)
			}
			if len(l.Row) == 0 {
				t.Fatalf("line %d is a row line carrying no row:\n%s", i+1, line)
			}
			if f.end != nil || f.fail != "" {
				t.Fatalf("line %d is a row after the feed was closed:\n%s", i+1, line)
			}
			f.rows = append(f.rows, l.Row)
		case streamLineEnd:
			if f.end != nil || f.fail != "" {
				t.Fatalf("line %d is a second closing line:\n%s", i+1, line)
			}
			var end reportStreamEnd
			if err := json.Unmarshal([]byte(line), &end); err != nil {
				t.Fatalf("line %d is an end line the shape does not have: %v:\n%s", i+1, err, line)
			}
			f.end = &end
		case streamLineError:
			var l struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &l); err != nil {
				t.Fatalf("line %d is an error line the shape does not have: %v:\n%s", i+1, err, line)
			}
			if l.Error == "" {
				t.Fatalf("line %d is an error line carrying no sentence:\n%s", i+1, line)
			}
			f.fail = l.Error
		default:
			t.Fatalf("line %d has type %q, want one of %s, %s, %s:\n%s", i+1, head.Type,
				streamLineRow, streamLineEnd, streamLineError, line)
		}
		f.last = head.Type
	}
	return f
}

// reportDocJSON is today's whole --json document for the fixture's window, as
// a consumer of it reads the two parts the feed carries: the elements of its
// rows array and its totals member.
func reportDocJSON(t *testing.T, args ...string) (rows []json.RawMessage, totals json.RawMessage) {
	t.Helper()
	out, errOut, err := execute(t, args...)
	if err != nil {
		t.Fatalf("%v\nstderr: %s", err, errOut)
	}
	var doc struct {
		Rows   []json.RawMessage `json:"rows"`
		Totals json.RawMessage   `json:"totals"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	if len(doc.Totals) == 0 {
		t.Fatalf("the document carries no totals: %s", out)
	}
	return doc.Rows, doc.Totals
}

// compactJSON is raw JSON with the insignificant whitespace removed, so a row
// of the indented document and the same row of the feed compare as the values
// the encoder wrote rather than as two decodings that could differ in a member
// neither decoding reads.
func compactJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return b.String()
}

// TestReportStreamRows: --stream carries the document's rows, one per line, in
// the document's own order, each byte for byte the element --json's rows array
// holds. It is the whole of what the flag promises a consumer: the same rows,
// in the same order, under a key of their own (D2906).
func TestReportStreamRows(t *testing.T) {
	reportStreamFixture(t)

	// Two groupings, because a row is whatever the grouping made of it: a
	// period row and a model row are different members of the same document,
	// and the feed carries whichever the command was asked for.
	for _, by := range []string{"day", "model"} {
		t.Run(by, func(t *testing.T) {
			docArgs := []string{"report", "--json", "--since", "2026-10-10", "--until", "2026-10-20", "--by", by}
			docRows, _ := reportDocJSON(t, docArgs...)
			out, errOut, err := execute(t, append(docArgs, "--stream")...)
			if err != nil {
				t.Fatalf("report --stream --by %s: %v (%s)", by, err, errOut)
			}
			feed := readReportFeed(t, out)
			if len(docRows) == 0 {
				t.Fatal("the fixture grouped no rows, so this test proves nothing")
			}
			if len(feed.rows) != len(docRows) {
				t.Fatalf("the feed carried %d rows, want the document's %d:\n%s", len(feed.rows), len(docRows), out)
			}
			for i := range docRows {
				if got, want := compactJSON(t, feed.rows[i]), compactJSON(t, docRows[i]); got != want {
					t.Errorf("row %d of the feed is not the document's row:\n got %s\nwant %s", i, got, want)
				}
			}
		})
	}

	// The document's order is what a reader sees in the table, so it is what a
	// consumer drawing the feed line by line has to get: the days ascending.
	out, _, err := execute(t, reportStreamArgs()...)
	if err != nil {
		t.Fatal(err)
	}
	feed := readReportFeed(t, out)
	if len(feed.rows) == 0 {
		t.Fatalf("the feed carried no row:\n%s", out)
	}
	if got := string(feed.rows[0]); !strings.Contains(got, `"key":"2026-10-14"`) {
		t.Errorf("the feed's first row is not the window's first day:\n%s", got)
	}
	if got := string(feed.rows[len(feed.rows)-1]); !strings.Contains(got, `"key":"2026-10-16"`) {
		t.Errorf("the feed's last row is not the window's last day:\n%s", got)
	}
}

// TestReportStreamEndCarriesTotals: the feed's last line is the end line, it
// counts the rows that went before it, and it carries the document's totals and
// the version of the shape the feed is written in. The totals are the one part
// of the report that is last by construction -- they sum the rows -- so a
// consumer that has them has the whole report; without the line an empty window
// and a read that was cut off are the same bytes.
func TestReportStreamEndCarriesTotals(t *testing.T) {
	reportStreamFixture(t)

	_, docTotals := reportDocJSON(t, "report", "--json", "--since", "2026-10-10", "--until", "2026-10-20")
	out, errOut, err := execute(t, reportStreamArgs()...)
	if err != nil {
		t.Fatalf("report --stream: %v (%s)", err, errOut)
	}
	feed := readReportFeed(t, out)
	if feed.end == nil {
		t.Fatalf("the feed ends with %q and carries no end line, so a reader cannot tell a report that finished from one that was interrupted:\n%s", feed.last, out)
	}
	if feed.last != streamLineEnd {
		t.Errorf("the feed's last line is %q, want the end line", feed.last)
	}
	if feed.end.Type != streamLineEnd {
		t.Errorf("the end line's type is %q, want %q", feed.end.Type, streamLineEnd)
	}
	if feed.end.Count != len(feed.rows) {
		t.Errorf("the end line counts %d rows, want the %d the feed carried:\n%s", feed.end.Count, len(feed.rows), out)
	}
	if feed.end.Version != reportStreamDocVersion {
		t.Errorf("the end line's version is %d, want reportStreamDocVersion (%d)", feed.end.Version, reportStreamDocVersion)
	}
	if got, want := compactJSON(t, feed.end.Totals), compactJSON(t, docTotals); got != want {
		t.Errorf("the end line's totals are not the document's:\n got %s\nwant %s", got, want)
	}
	if feed.fail != "" {
		t.Errorf("a feed that ran to completion carries an error line: %q", feed.fail)
	}
}

// TestReportJSONUnchanged: `report --json` prints the document it printed
// before --stream existed, byte for byte. The flag is additive or it is a
// retyping of a document the console's decoder, the phone and §4 of the JSON
// contract already hold (CLAUDE.md's field-never-changes-meaning rule).
func TestReportJSONUnchanged(t *testing.T) {
	reportStreamFixture(t)

	out, errOut, err := execute(t, "report", "--json", "--since", "2026-10-10", "--until", "2026-10-20")
	if err != nil {
		t.Fatalf("report --json: %v (%s)", err, errOut)
	}
	if out != reportStreamDocJSON {
		t.Errorf("report --json no longer prints the document it printed before --stream:\n got:\n%s\nwant:\n%s", out, reportStreamDocJSON)
	}
}

// TestReportStreamNeedsJSON: the feed's lines are JSON objects, one per row of
// --json's rows array, so the flag is accepted only with --json -- and the
// refusal names it rather than printing a report in a shape the document and
// the table both lack.
func TestReportStreamNeedsJSON(t *testing.T) {
	reportStreamFixture(t)
	for _, args := range [][]string{
		{"report", "--stream", "--since", "2026-10-10"},
		{"report", "--csv", "--stream", "--since", "2026-10-10"},
	} {
		_, _, err := execute(t, args...)
		if err == nil || !strings.Contains(err.Error(), "--stream") {
			t.Errorf("%v: err = %v, want a refusal naming --stream", args, err)
		}
	}
}

// failingWriter is a consumer's pipe that breaks: it takes every write up to
// failFrom, refuses the ones up to failUntil, and takes what comes after --
// which is how a feed is tested from the middle, with the line a failing
// command writes about itself still able to get out.
type failingWriter struct {
	buf              bytes.Buffer
	writes, failFrom int
	failUntil        int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failFrom && w.writes <= w.failUntil {
		return 0, errors.New("the feed's pipe is gone")
	}
	return w.buf.Write(p)
}

// TestReportStreamErrorLineAfterARow: a feed that fails once a row has gone out
// does not simply stop. A consumer holding rows and no end line cannot tell a
// report that broke from one that is still being read, so the sentence the
// command would have printed is written as the feed's own error line -- in one
// line, since a newline inside it would forge a second line of the feed -- and
// the command fails, so the process exits non-zero.
func TestReportStreamErrorLineAfterARow(t *testing.T) {
	reportStreamFixture(t)

	// The second write is the second row: the first row lands, the failure
	// happens with a row already out, and the error line is the write after it.
	out := &failingWriter{failFrom: 2, failUntil: 2}
	cmd := reportTestRoot()
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(fcli.NormalizeArgs(reportStreamArgs()))
	if err := cmd.Execute(); err == nil {
		t.Fatal("a feed whose write failed returned no error: the command must exit non-zero")
	}
	feed := readReportFeed(t, out.buf.String())
	if len(feed.rows) != 1 {
		t.Fatalf("the feed carried %d rows before the write failed, want the one that landed:\n%s", len(feed.rows), out.buf.String())
	}
	if feed.fail != "the feed's pipe is gone" {
		t.Fatalf("the feed stopped with %q, want the failing write's own sentence:\n%s", feed.fail, out.buf.String())
	}
	if strings.Contains(feed.fail, "\n") {
		t.Errorf("the error line is %q, want the CLI's sentence in one line", feed.fail)
	}
	if feed.end != nil {
		t.Errorf("a feed that failed carries an end line: %+v", feed.end)
	}

	// A failure before the first row writes no line at all: there is nothing a
	// consumer could have drawn, and the command's own error is the answer.
	broken := &failingWriter{failFrom: 1, failUntil: 1}
	cmd = reportTestRoot()
	cmd.SetOut(broken)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(fcli.NormalizeArgs(reportStreamArgs()))
	if err := cmd.Execute(); err == nil {
		t.Fatal("a feed that failed on its first write returned no error")
	}
	if got := broken.buf.String(); got != "" {
		t.Errorf("a feed that failed before any row wrote %q, want nothing", got)
	}
}

// compileReportStreamSchema compiles schemas/report-stream.schema.json, the
// contract the console's decoder is written against.
func compileReportStreamSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "report-stream.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/report-stream.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// TestReportStreamSchema validates every line of the recorded feed against
// schemas/report-stream.schema.json, and holds the schema to refusing the lines
// the shape does not have -- a schema that accepted everything would be a
// comment on the feed rather than its contract.
func TestReportStreamSchema(t *testing.T) {
	sch := compileReportStreamSchema(t)
	data, err := os.ReadFile(filepath.Join("testdata", "report_stream.golden.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		lines++
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(line))
		if err != nil {
			t.Fatalf("line %d of the golden is not JSON: %v:\n%s", i+1, err, line)
		}
		if err := sch.Validate(inst); err != nil {
			t.Errorf("line %d of the golden does not match schemas/report-stream.schema.json: %v\n%s", i+1, err, line)
		}
	}
	if lines == 0 {
		t.Fatal("the golden carries no line")
	}

	for _, bad := range []string{
		`{"type":"end","count":1,"version":1}`,               // an end line with no totals
		`{"type":"end","count":1,"totals":{}}`,               // and one that does not say which shape it ends
		`{"type":"end","version":1,"totals":{}}`,             // and one that does not count the rows it carried
		`{"type":"end","count":"1","version":1,"totals":{}}`, // a count that is not a number
		`{"type":"row"}`,            // a row line with no row
		`{"type":"row","row":1}`,    // a row that is not an element of a document's array
		`{"type":"error"}`,          // an error line with no sentence
		`{"type":"delta","row":{}}`, // a line kind that is not in the shape
		`{"row":{}}`,                // a line that does not say what it is
		`{"type":"end","count":1,"version":1,"totals":1}`, // a totals member that is not a report row
	} {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(bad))
		if err != nil {
			t.Fatalf("%s is not JSON: %v", bad, err)
		}
		if err := sch.Validate(inst); err == nil {
			t.Errorf("the schema accepted %s, a line the feed's shape does not have", bad)
		}
	}
}

var updateReportStreamGolden = flag.Bool("update-report-stream", false, "rewrite testdata/report_stream.golden.ndjson")

// TestReportStreamGolden pins the feed the CLI writes -- the line kinds, their
// members and the rows' own bytes -- as the file the console's decoder is
// compared with. The window and the clock are pinned, so the golden is the same
// file tomorrow.
func TestReportStreamGolden(t *testing.T) {
	reportStreamFixture(t)

	out, errOut, err := execute(t, reportStreamArgs()...)
	if err != nil {
		t.Fatalf("report --stream: %v (%s)", err, errOut)
	}
	path := filepath.Join("testdata", "report_stream.golden.ndjson")
	if *updateReportStreamGolden {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file: run go test ./ee/report/cli -run TestReportStreamGolden -update-report-stream")
	}
	if string(want) != out {
		t.Fatalf("the feed changed (go test ./ee/report/cli -run TestReportStreamGolden -update-report-stream to accept):\n%s\nwant:\n%s", out, want)
	}
	// The console reads this file as bytes and decodes it, so it is NDJSON and
	// nothing else: no blank line, no comment, no second document.
	feed := readReportFeed(t, string(want))
	if feed.end == nil || len(feed.rows) != feed.end.Count {
		t.Errorf("the golden is not a feed that ran to completion: %d rows, end %+v", len(feed.rows), feed.end)
	}
	if feed.last != streamLineEnd {
		t.Errorf("the golden's last line is %q, want %q", feed.last, streamLineEnd)
	}
}

// reportStreamDocJSON is today's whole --json document for the fixture's
// window, recorded before --stream existed. Adding a flag may not move a byte
// of it: the console's decoder, the phone and §4 of the JSON contract read it,
// and a member that appeared, moved or was retyped is a different document.
const reportStreamDocJSON = `{
  "project": "aurora",
  "source": "firestore",
  "degraded": false,
  "by": "day",
  "since": "2026-10-10",
  "until": "2026-10-20",
  "weeks": "iso-utc-monday",
  "rows": [
    {
      "key": "2026-10-14",
      "start": "2026-10-14",
      "end": "2026-10-14",
      "partial": false,
      "model_micros": 1500000,
      "model_usd": "1.500000",
      "notional_micros": 0,
      "notional_usd": "0.000000",
      "compute_micros": 200000,
      "compute_usd": "0.200000",
      "compute_estimated": true,
      "run_hours": 1.5,
      "runs": 1,
      "calls": 3
    },
    {
      "key": "2026-10-15",
      "start": "2026-10-15",
      "end": "2026-10-15",
      "partial": false,
      "model_micros": 2000000,
      "model_usd": "2.000000",
      "notional_micros": 700000,
      "notional_usd": "0.700000",
      "compute_micros": null,
      "compute_usd": null,
      "compute_estimated": false,
      "run_hours": null,
      "runs": 1,
      "calls": 3
    },
    {
      "key": "2026-10-16",
      "start": "2026-10-16",
      "end": "2026-10-16",
      "partial": false,
      "model_micros": 500000,
      "model_usd": "0.500000",
      "notional_micros": 0,
      "notional_usd": "0.000000",
      "compute_micros": null,
      "compute_usd": null,
      "compute_estimated": false,
      "run_hours": null,
      "runs": 1,
      "calls": 3
    }
  ],
  "totals": {
    "key": "TOTAL",
    "end": "2026-10-16",
    "partial": false,
    "model_micros": 4000000,
    "model_usd": "4.000000",
    "notional_micros": 700000,
    "notional_usd": "0.700000",
    "compute_micros": 200000,
    "compute_usd": "0.200000",
    "compute_estimated": true,
    "run_hours": 4.5,
    "runs": 3,
    "calls": 9
  },
  "warnings": [
    "COMPUTE $ is an estimate, priced from each run's own price table: a VM run's figure leaves the VM's boot disk and Cloud NAT out",
    "A/B shadow spend is included in these figures: a challenger run's money is spent like any run's, and no figure here tells a challenger's spend from its champion's (` + "`" + `fugaro ab` + "`" + ` is where the two arms are told apart)"
  ]
}
`
