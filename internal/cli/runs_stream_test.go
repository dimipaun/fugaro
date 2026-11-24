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
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// pinLsClock pins runs ls's clock, so a feed's rows are the same bytes on every
// run: a row carries the run's own mint time (runview.Row.Created, read from
// the run id) and the window --since measures back from lsNow.
func pinLsClock(t *testing.T) {
	t.Helper()
	old := lsNow
	t.Cleanup(func() { lsNow = old })
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	lsNow = func() time.Time { return at }
}

// runsStreamWindow is the window the feed's tests list: three runs -- two that
// ended with a failure class and a spend, one that never launched -- newest
// first, so the feed has rows of both shapes to carry.
func runsStreamWindow(t *testing.T) {
	t.Helper()
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	seedClassedRun(t, f, recentRunID(t, 3*time.Minute, "aaaa"), failure.TestsFailed, 4)
	seedClassedRun(t, f, recentRunID(t, 2*time.Minute, "bbbb"), failure.NoCommits, 1)
	seedRun(t, f, recentRunID(t, time.Minute, "cccc"), "", "someone@example.com", false)
}

// streamFeed is a --stream feed read back as a consumer reads it: the rows it
// carried in the order it carried them, the end line it ended with (if it did),
// and the sentence it stopped with (if it stopped). The rows are raw JSON, so a
// test can hold a line's row to the document's own bytes rather than to a
// second decoding of it.
type streamFeed struct {
	rows []json.RawMessage
	end  *streamEndLine
	fail string
	last string // the type of the last line, "" for an empty feed
}

// readFeed reads a feed the way a consumer does -- one line at a time -- and
// fails on anything outside the three line kinds: a line the shape does not
// have is a feed no CLI wrote, and a test that skipped it would pass on a feed
// nothing could read.
func readFeed(t *testing.T, out string) streamFeed {
	t.Helper()
	var f streamFeed
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
			var end streamEndLine
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

// compact is raw JSON with the insignificant whitespace removed, so a row of
// the indented document and the same row of the feed compare as the bytes the
// encoder wrote rather than as two decoded values that could differ in a member
// neither decoding reads.
func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return b.String()
}

// TestRunsLsStreamRows: --stream carries the document's rows, one per line, in
// the document's own order, each byte for byte the element --json's runs array
// holds. It is the whole of what the flag promises a consumer: the same rows,
// in the same order, under a key of their own (D2906).
func TestRunsLsStreamRows(t *testing.T) {
	pinLsClock(t)
	runsStreamWindow(t)

	docOut, _, err := execute(t, "runs", "ls", "--json", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	feedOut, errOut, err := execute(t, "runs", "ls", "--json", "--stream", "--since", "0")
	if err != nil {
		t.Fatalf("runs ls --stream: %v (%s)", err, errOut)
	}
	var doc struct {
		Runs []json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal([]byte(docOut), &doc); err != nil {
		t.Fatalf("%v: %s", err, docOut)
	}
	feed := readFeed(t, feedOut)
	if len(doc.Runs) == 0 {
		t.Fatal("the fixture listed no runs, so this test proves nothing")
	}
	if len(feed.rows) != len(doc.Runs) {
		t.Fatalf("the feed carried %d rows, want the document's %d:\n%s", len(feed.rows), len(doc.Runs), feedOut)
	}
	for i := range doc.Runs {
		if got, want := compact(t, feed.rows[i]), compact(t, doc.Runs[i]); got != want {
			t.Errorf("row %d of the feed is not the document's row:\n got %s\nwant %s", i, got, want)
		}
	}
	// The document's order is what a reader sees in the table, so it is what a
	// consumer drawing the feed line by line has to get: newest first.
	if got := feed.rows[0]; !strings.Contains(string(got), "115900-cccc") {
		t.Errorf("the feed's first row is not the newest run:\n%s", got)
	}
}

// TestRunsLsStreamEndsWithCount: the feed's last line is the end line, and it
// carries the count of the rows that went before it. Without it an empty window
// and a read that was cut off are the same bytes, which is the one thing the
// flag exists to tell apart.
func TestRunsLsStreamEndsWithCount(t *testing.T) {
	pinLsClock(t)
	runsStreamWindow(t)

	out, errOut, err := execute(t, "runs", "ls", "--json", "--stream", "--since", "0")
	if err != nil {
		t.Fatalf("runs ls --stream: %v (%s)", err, errOut)
	}
	feed := readFeed(t, out)
	if feed.end == nil {
		t.Fatalf("the feed ends with %q and carries no end line, so a reader cannot tell a listing that finished from one that was interrupted:\n%s", feed.last, out)
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
	if feed.end.Version != runsStreamDocVersion {
		t.Errorf("the end line's version is %d, want runsStreamDocVersion (%d)", feed.end.Version, runsStreamDocVersion)
	}
	if feed.fail != "" {
		t.Errorf("a feed that ran to completion carries an error line: %q", feed.fail)
	}

	// A page is a feed of the page: --limit shortens the rows and the count
	// with them, because count is what the feed carried and not what the window
	// totalled (the totals stay the window's, as they do in the document).
	paged, _, err := execute(t, "runs", "ls", "--json", "--stream", "--since", "0", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	page := readFeed(t, paged)
	if len(page.rows) != 2 || page.end == nil || page.end.Count != 2 {
		t.Errorf("--limit 2 carried %d rows and the end line %+v, want two rows counted as two:\n%s",
			len(page.rows), page.end, paged)
	}
}

// TestRunsLsStreamCarriesTheIndexFacts: a feed of a page the run index answered
// carries what the index said of itself on the line that ends it (D3215), so a
// consumer drawing rows as they arrive never draws a page of an index that is
// still building as the whole history -- the shape most able to print a
// silently short list is the one that reads no whole document to check against
// (the plan's cross-cutting pin and IR1's acceptance item 4).
//
// Mutation caught: writing count and version alone, which is what a console's
// Live tab reads today: the feed then says nothing about the page being short.
func TestRunsLsStreamCarriesTheIndexFacts(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	lsIndexRun(t, f, idx, 30*time.Minute, runstore.StatusSucceeded, "aaaa")
	folded := time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC)
	idx.summary = runindex.Summary{Version: runindex.Version, Complete: false, FoldedThrough: &folded,
		States: map[string]int64{}}

	out, errOut, err := execute(t, "runs", "ls", "--json", "--stream")
	if err != nil {
		t.Fatalf("runs ls --stream: %v (%s)", err, errOut)
	}
	feed := readFeed(t, out)
	if feed.end == nil {
		t.Fatalf("the feed ends with %q and carries no end line:\n%s", feed.last, out)
	}
	if feed.end.Version != runsStreamDocVersion {
		t.Errorf("the end line's version is %d, want runsStreamDocVersion (%d)", feed.end.Version, runsStreamDocVersion)
	}
	if feed.end.IndexComplete == nil || *feed.end.IndexComplete {
		t.Errorf("the end line's index_complete is %v, want false: the listing read an index that has not finished a walk, and a consumer that drew these rows would be drawing a short list as the whole history", feed.end.IndexComplete)
	}
	if feed.end.FoldedThrough == nil || !feed.end.FoldedThrough.Equal(folded) {
		t.Errorf("the end line's folded_through is %v, want %s", feed.end.FoldedThrough, folded)
	}
}

// TestRunsLsStreamEmptyWindowStillEnds: a window with no run writes no row and
// still ends, with a count of zero. It is the case the count exists for: a
// consumer that read no row and no end line cannot tell an empty week from a
// listing that died.
func TestRunsLsStreamEmptyWindowStillEnds(t *testing.T) {
	pinLsClock(t)
	newCloudFixture(t)

	out, _, err := execute(t, "runs", "ls", "--json", "--stream", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	feed := readFeed(t, out)
	if len(feed.rows) != 0 {
		t.Fatalf("an empty window carried %d rows:\n%s", len(feed.rows), out)
	}
	if feed.end == nil || feed.end.Count != 0 {
		t.Fatalf("a feed of an empty window is %q with end %+v, want an end line counting zero:\n%s", feed.last, feed.end, out)
	}
}

// runsLsJSON is today's whole document for the tests' window, recorded before
// --stream existed. Adding a flag may not move a byte of it: the console, the
// phone's degraded screen (whose body embeds this shape) and every consumer of
// §5 of the JSON contract read it, and a member that appeared, moved or was
// retyped is a different document (docs/console-improvements implementation
// plan, RS1; CLAUDE.md's field-never-changes-meaning rule).
const runsLsJSON = `{
  "project": "aurora",
  "runs": [
    {
      "run": "acme-app-dc4d4e59e884fc44/20261109-115900-cccc",
      "repo": "acme/app",
      "workflow": "web",
      "title": "x",
      "run_id": "20261109-115900-cccc",
      "status": "unlaunched",
      "requested_by": "someone@example.com",
      "created": "2026-11-09T11:59:00Z",
      "cost": {
        "model_usd": 0,
        "compute_usd": 0,
        "compute_estimated": true,
        "total_usd": 0,
        "estimate": true,
        "model_basis": "api-list"
      },
      "terminal": false,
      "settled": true,
      "pushed": false,
      "follow_up": false
    },
    {
      "run": "acme-app-dc4d4e59e884fc44/20261109-115800-bbbb",
      "repo": "acme/app",
      "workflow": "web",
      "title": "x",
      "run_id": "20261109-115800-bbbb",
      "status": "failed",
      "stage": "verify",
      "reason": "the run did not finish its work",
      "failure_class": "no_commits",
      "requested_by": "someone@example.com",
      "created": "2026-11-09T11:58:00Z",
      "cost": {
        "model_usd": 1,
        "compute_usd": 0,
        "compute_estimated": true,
        "total_usd": 1,
        "estimate": true,
        "model_basis": "api-list"
      },
      "terminal": true,
      "settled": true,
      "pushed": false,
      "follow_up": false
    },
    {
      "run": "acme-app-dc4d4e59e884fc44/20261109-115700-aaaa",
      "repo": "acme/app",
      "workflow": "web",
      "title": "x",
      "run_id": "20261109-115700-aaaa",
      "status": "failed",
      "stage": "verify",
      "reason": "the run did not finish its work",
      "failure_class": "tests_failed",
      "requested_by": "someone@example.com",
      "created": "2026-11-09T11:57:00Z",
      "cost": {
        "model_usd": 4,
        "compute_usd": 0,
        "compute_estimated": true,
        "total_usd": 4,
        "estimate": true,
        "model_basis": "api-list"
      },
      "terminal": true,
      "settled": true,
      "pushed": false,
      "follow_up": false
    }
  ],
  "totals": {
    "runs": 3,
    "model_usd": 5,
    "model_notional_usd": 0,
    "compute_usd": 0,
    "total_usd": 5,
    "compute_not_estimated": 0
  },
  "warnings": []
}
`

// TestRunsLsJSONUnchanged: `runs ls --json` prints the document it printed
// before --stream existed, byte for byte. The flag is additive or it is a
// retyping of a document three readers already hold (§5's contract, the watch
// feed's degraded body, the phone).
func TestRunsLsJSONUnchanged(t *testing.T) {
	pinLsClock(t)
	runsStreamWindow(t)

	out, errOut, err := execute(t, "runs", "ls", "--json", "--since", "0")
	if err != nil {
		t.Fatalf("runs ls --json: %v (%s)", err, errOut)
	}
	if out != runsLsJSON {
		t.Errorf("runs ls --json no longer prints the document it printed before --stream:\n got:\n%s\nwant:\n%s", out, runsLsJSON)
	}
}

// failedWriter is a consumer's pipe that breaks: it takes every write up to
// failFrom, refuses the ones up to failUntil, and takes what comes after --
// which is how a feed is tested from the middle, with the line a failing
// command writes about itself still able to get out.
type failedWriter struct {
	buf              bytes.Buffer
	writes, failFrom int
	failUntil        int
}

func (w *failedWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failFrom && w.writes <= w.failUntil {
		return 0, errors.New("the feed's pipe is gone")
	}
	return w.buf.Write(p)
}

// TestRunsLsStreamErrorLineAfterARow: a feed that fails once a row has gone out
// does not simply stop. A consumer holding rows and no end line cannot tell a
// listing that broke from one that is still being read, so the sentence the
// command would have printed is written as the feed's own error line -- in one
// line, since a newline inside it would forge a second line of the feed -- and
// the command fails, so the process exits non-zero.
func TestRunsLsStreamErrorLineAfterARow(t *testing.T) {
	pinLsClock(t)
	runsStreamWindow(t)

	// The second write is the second row: the first row lands, the failure
	// happens with a row already out, and the error line is the write after it.
	out := &failedWriter{failFrom: 2, failUntil: 2}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(NormalizeArgs([]string{"runs", "ls", "--json", "--stream", "--since", "0"}))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a feed whose write failed returned no error: the command must exit non-zero")
	}
	feed := readFeed(t, out.buf.String())
	if len(feed.rows) != 1 {
		t.Fatalf("the feed carried %d rows before the write failed, want the one that landed:\n%s", len(feed.rows), out.buf.String())
	}
	if feed.fail != "the feed's pipe is gone" {
		t.Fatalf("the feed stopped with %q, want the failing write's own sentence:\n%s", feed.fail, out.buf.String())
	}
	if !strings.Contains(feed.fail, "pipe") || strings.Contains(feed.fail, "\n") {
		t.Errorf("the error line is %q, want the CLI's sentence in one line", feed.fail)
	}
	if feed.end != nil {
		t.Errorf("a feed that failed carries an end line: %+v", feed.end)
	}

	// A failure before the first row writes no line at all: there is nothing a
	// consumer could have drawn, and the command's own error is the answer.
	broken := &failedWriter{failFrom: 1, failUntil: 1}
	cmd = NewRootCmd()
	cmd.SetOut(broken)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(NormalizeArgs([]string{"runs", "ls", "--json", "--stream", "--since", "0"}))
	if err := cmd.Execute(); err == nil {
		t.Fatal("a feed that failed on its first write returned no error")
	}
	if got := broken.buf.String(); got != "" {
		t.Errorf("a feed that failed before any row wrote %q, want nothing", got)
	}
}

// TestRunsLsStreamRefusesWhatItCannotWrite: the flag writes JSON lines of the
// document's runs array, so it is accepted only with --json, and not with the
// two flags whose listing is not that array: --watch redraws the whole
// listing, and --group-by's block has no line in the feed.
func TestRunsLsStreamRefusesWhatItCannotWrite(t *testing.T) {
	newCloudFixture(t)
	for _, args := range [][]string{
		{"runs", "ls", "--stream"},
		{"runs", "ls", "--json", "--stream", "--watch"},
		{"runs", "ls", "--json", "--stream", "--group-by", "failure_class"},
	} {
		_, _, err := execute(t, args...)
		if err == nil || !strings.Contains(err.Error(), "--stream") {
			t.Errorf("%v: err = %v, want a refusal naming --stream", args, err)
		}
	}
}

// compileRunsStreamSchema compiles schemas/runs-stream.schema.json, the
// contract the console's decoder is written against. The budget feed's schema
// is compiled the same way (compileStreamSchema, budget_stream_test.go).
func compileRunsStreamSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	return compileStreamSchema(t, "runs-stream.schema.json")
}

// TestRunsStreamSchema validates every line of the recorded feed against
// schemas/runs-stream.schema.json, and holds the schema to refusing the lines
// the shape does not have -- a schema that accepted everything would be a
// comment on the feed rather than its contract.
func TestRunsStreamSchema(t *testing.T) {
	sch := compileRunsStreamSchema(t)
	data, err := os.ReadFile(filepath.Join("testdata", "runs_stream.golden.ndjson"))
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
			t.Errorf("line %d of the golden does not match schemas/runs-stream.schema.json: %v\n%s", i+1, err, line)
		}
	}
	if lines == 0 {
		t.Fatal("the golden carries no line")
	}

	for _, bad := range []string{
		`{"type":"end","count":1}`,               // an end line that does not say which shape it ends
		`{"type":"end","version":2}`,             // and one that does not count the rows it carried
		`{"type":"end","count":"1","version":2}`, // a count that is not a number
		`{"type":"row"}`,                         // a row line with no row
		`{"type":"row","row":1}`,                 // a row that is not an element of a document's array
		`{"type":"error"}`,                       // an error line with no sentence
		`{"type":"delta","row":{}}`,              // a line kind that is not in the shape
		`{"row":{}}`,                             // a line that does not say what it is
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

var updateRunsStreamGolden = flag.Bool("update-runs-stream", false, "rewrite testdata/runs_stream.golden.ndjson")

// TestRunsStreamGolden pins the feed the CLI writes -- the line kinds, their
// members and the rows' own bytes -- as the file the console's decoder is
// compared with. The window and the clock are pinned, so the golden is the same
// file tomorrow: a run id carries its mint time and a row its own.
func TestRunsStreamGolden(t *testing.T) {
	pinLsClock(t)
	runsStreamWindow(t)

	out, errOut, err := execute(t, "runs", "ls", "--json", "--stream", "--since", "0")
	if err != nil {
		t.Fatalf("runs ls --stream: %v (%s)", err, errOut)
	}
	path := filepath.Join("testdata", "runs_stream.golden.ndjson")
	if *updateRunsStreamGolden {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file: run go test ./internal/cli -run TestRunsStreamGolden -update-runs-stream")
	}
	if string(want) != out {
		t.Fatalf("the feed changed (go test ./internal/cli -run TestRunsStreamGolden -update-runs-stream to accept):\n%s\nwant:\n%s", out, want)
	}
	// The console reads this file as bytes and decodes it, so it is NDJSON and
	// nothing else: no blank line, no comment, no second document.
	feed := readFeed(t, string(want))
	if feed.end == nil || len(feed.rows) != feed.end.Count {
		t.Errorf("the golden is not a feed that ran to completion: %d rows, end %+v", len(feed.rows), feed.end)
	}
	if feed.last != streamLineEnd {
		t.Errorf("the golden's last line is %q, want %q", feed.last, streamLineEnd)
	}
	// The listing here read the window (the index is the stub), so the end line
	// carries no index fact, exactly as the same listing's document does not.
	if feed.end.IndexComplete != nil || feed.end.FoldedThrough != nil {
		t.Errorf("the golden's end line carries an index fact, want none: the listing read the window:\n%s", want)
	}
}
