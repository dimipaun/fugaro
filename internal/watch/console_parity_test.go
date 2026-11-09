package watch

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// The console's copy of what watch shows (short-run-ids plan, SR10, D1704).
//
// The console draws its own table -- the approved Flight Deck one, whose columns
// are not watch's -- and follows watch in the three things a reader compares
// across the two windows: the order of the rows, the short ID each row is named
// by, and the words a row's state is said in. "When watch changes, the live tab
// follows" is a process claim, and D1704 asks for a test to hold it rather than
// a promise: this file regenerates the console's fixture from watch's own
// renderers over one fixed view, so a change to either renderer fails here until
// the fixture is recorded again, and the console's live-parity test
// (console/test/unit/live-parity.test.ts) then fails until the live tab draws
// the new thing.
//
// Two fixtures and one table:
//
//   - console/test/fixtures/watch-parity.json -- one repository's frame as
//     `watch --json` writes it (BuildJSON) beside the same frame as the plain
//     renderer draws it (RenderPlain). The JSON is the order and the values; the
//     plain frame is the wording and the endings, drawn by the code that draws
//     them for a person.
//   - console/test/fixtures/shortid_cases.json -- the table SR1 wrote
//     (internal/task/testdata/shortid_cases.json), copied rather than
//     transcribed, so the console's helper and the Go one cannot answer a case
//     differently. The console's own test runs its helper over it:
//     console/test/unit/shortid.test.ts reads this file and answers every
//     `short[]` and `display[]` case of it, so the table is executed on that
//     side and not only held equal here.
//
// The fixtures are recorded, not hand-written: `go test ./internal/watch -run
// Parity -update-console-parity` rewrites them, and a reviewer reads the diff.
var updateConsoleParity = flag.Bool("update-console-parity", false,
	"rewrite console/test/fixtures/watch-parity.json from watch's own renderers")

const (
	consoleParityFixture = "../../console/test/fixtures/watch-parity.json"
	consoleShortIDCases  = "../../console/test/fixtures/shortid_cases.json"
	goShortIDCases       = "../task/testdata/shortid_cases.json"
)

// consoleParityFixtureDoc is the fixture's own shape: the note saying what it is,
// the frame, and the text frame the same view draws. It is a document of this
// test and of the console's test, not of any wire: nothing reads one.
type consoleParityFixtureDoc struct {
	Note     string  `json:"note"`
	Document JSONDoc `json:"document"`
	Plain    string  `json:"plain"`
}

// TestConsoleParityFixtureMatchesWatch regenerates the console's fixture from
// watch's renderers over one fixed view and fails when the checked-in copy
// differs. The fix is to record it again (the flag above) and to bring the live
// tab to the new words, so the console never draws a frame watch no longer
// produces.
func TestConsoleParityFixtureMatchesWatch(t *testing.T) {
	v := consoleParityView()
	plain := new(strings.Builder)
	if err := RenderPlain(plain, "acme", v, ReadyRowsOf(v), PlainOptions{}); err != nil {
		t.Fatalf("RenderPlain: %v", err)
	}
	want, err := consoleParityBytes(consoleParityFixtureDoc{
		Note: "recorded from internal/watch's own renderers over one fixed view " +
			"(internal/watch/console_parity_test.go). Invents every value: no installation is recorded here.",
		Document: BuildJSON("acme", v),
		Plain:    plain.String(),
	})
	if err != nil {
		t.Fatalf("marshal the fixture: %v", err)
	}
	if *updateConsoleParity {
		if err := os.WriteFile(consoleParityFixture, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", consoleParityFixture, err)
		}
		return
	}
	got, err := os.ReadFile(consoleParityFixture)
	if err != nil {
		t.Fatalf("%v (record it with go test ./internal/watch -run Parity -update-console-parity)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is not what watch draws now: record it again with\n"+
			"  go test ./internal/watch -run Parity -update-console-parity\n"+
			"and bring the console's live tab to the new frame.\nwant:\n%s\ngot:\n%s",
			consoleParityFixture, want, got)
	}
}

// TestConsoleParityShortIDCasesMatchTheGoTable holds the console's copy of SR1's
// case table to the table itself: one rule, one set of cases, two languages
// (D1706). A case added to the Go table and not to the console's copy fails
// here, and console/test/unit/shortid.test.ts runs the console's helper over
// every case the copy carries -- so an entry reaches a check on both sides, and
// this test only has to say that the two files are the same case table.
func TestConsoleParityShortIDCasesMatchTheGoTable(t *testing.T) {
	want, err := os.ReadFile(goShortIDCases)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(consoleShortIDCases)
	if err != nil {
		t.Fatalf("%v (copy %s to it)", err, goShortIDCases)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is not %s: the console's copy of the short-ID cases drifted from the Go table\nwant:\n%s\ngot:\n%s",
			consoleShortIDCases, goShortIDCases, want, got)
	}
}

// consoleParityBytes is the fixture file as it is written: the document indented
// so a diff is readable, and one trailing newline.
func consoleParityBytes(doc consoleParityFixtureDoc) ([]byte, error) {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// consoleParityView is the one frame the fixture is recorded from: one
// repository, five runs -- a queued one, a lost one, two that share their last
// four characters (so the fixture carries the collision rule as watch draws it)
// and one plain run. Every value is invented (the plan's conventions: fake run
// IDs only) and every field a renderer reads is set, because the console decodes
// this document strictly.
//
// The runs are ordered by sortRuns rather than by hand, so the fixture's order
// is watch's own order and not this file's opinion of it.
func consoleParityView() View {
	at := func(min int) int64 {
		return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute).UnixMilli()
	}
	runs := []RunRow{
		{
			Run: "20261108-113000-9c3d", Slug: "acme-app", Title: "an artifact run", Stage: "-",
			Round: "-", Auth: "oauth", Notional: true, Queued: true, HasAge: true, Age: 90 * time.Second,
			StartedAt: at(0),
		},
		{
			Run: "20261108-114500-aa02", Slug: "acme-app", Title: "the run that stopped reporting",
			Stage: "implement", Round: "1", Verify: "-", Models: "sonnet", Auth: "api", Spent: budget.Micros(1_500_000),
			HasSpent: true, HasAge: true, Age: 14 * time.Minute, Health: HealthLost, StartedAt: at(15),
			LaunchedBy: "someone@example.invalid",
		},
		{
			Run: "20261107-010101-3bb2", Slug: "acme-app", Title: "a run from yesterday", Stage: "verify",
			Round: "2", Verify: "-", Models: "opus", Auth: "api", Spent: budget.Micros(4_250_000),
			HasSpent: true, HasAge: true, Age: 71 * time.Minute, StartedAt: at(20),
		},
		{
			Run: "20261108-115248-3bb2", Slug: "acme-app", Title: "the run beside it", Stage: "implement",
			Round: "1", Verify: "-", Models: "opus", Auth: "api", Spent: budget.Micros(900_000),
			HasSpent: true, HasAge: true, Age: 12 * time.Minute, StartedAt: at(25),
		},
		{
			Run: "20261108-121500-aa01", Slug: "acme-app", Title: "the run in review", Stage: "review",
			Round: "2", Verify: "-", Models: "sonnet", Auth: "api", Spent: budget.Micros(2_000_000),
			HasSpent: true, HasAge: true, Age: 6 * time.Minute, StartedAt: at(30),
			LaunchedBy: "someone@example.invalid",
		},
	}
	sortRuns(runs)
	return View{
		Now:  time.Date(2026, 11, 8, 12, 31, 0, 0, time.UTC),
		Day:  "2026-11-08",
		Mode: "enforce",
		Conn: Connection{Kind: ConnLive, Age: 2 * time.Second},
		Project: ProjectLine{
			Counted: budget.Micros(8_650_000), Spent: budget.Micros(8_650_000), Notional: budget.Micros(1_500_000),
			Bar:  Bar{Has: true, Used: budget.Micros(8_650_000), Cap: budget.Micros(50_000_000), Percent: 17.3},
			Burn: Burn{Known: true, PerMin: budget.Micros(12_000)},
			Runs: len(runs), RunHours: 1.7,
		},
		Repos: []RepoBlock{{
			Slug: "acme-app", Name: "acme/app",
			Counted: budget.Micros(8_650_000), Spent: budget.Micros(8_650_000), Notional: budget.Micros(1_500_000),
			Bar:  Bar{Has: true, Used: budget.Micros(8_650_000), Cap: budget.Micros(50_000_000), Percent: 17.3},
			Burn: Burn{Known: true, PerMin: budget.Micros(12_000)},
			Runs: runs,
		}},
	}
}
