package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/budget"
)

// budgetStreamAt is the instant the feed's fixture is read at. The database
// fake stamps it in the Date header of every answer, and `budget show` takes
// its clock from there, so the day the counters are read under, the day the
// document names and the staleness of a registry entry are all the fixture's
// and the golden is the same file tomorrow.
var budgetStreamAt = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// budgetStreamFixture is the listing the feed's tests write: two repositories
// of the two shapes a row has (one the config names with caps of its own and a
// switch of its own, one only the database knows, bound by the default cap), a
// run in flight in the first, and the project's kill switch over both. The
// counters add up to the project's and the mode is set, so the document the
// listing prints carries no warning: the feed's rows are what is under test
// here, and the warnings have their own tests.
func budgetStreamFixture(t *testing.T) *budgetFixture {
	t.Helper()
	f := newBudgetFixture(t, "")
	f.db.SetClock(func() time.Time { return budgetStreamAt })
	day := budget.Day(budgetStreamAt)
	other := mustSlug("github", "acme/other")

	f.db.Set(budget.PathMode, budget.ModeObserve)
	seedCaps(f, 100, 20)
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"repo": "acme/app", "dailyMicros": 25 * usd1, "perRunMicros": 4 * usd1})
	f.db.Set(budget.PathSpendGlobal(day), map[string]any{"counted": 4*usd1 + 500_000, "spent": 2*usd1 + 250_000, "notional": 500_000})
	f.db.Set(budget.PathSpendRepo(day, appSlug), map[string]any{"counted": 3*usd1 + 500_000, "spent": usd1 + 250_000, "notional": 500_000})
	f.db.Set(budget.PathSpendRepo(day, other), map[string]any{"counted": usd1, "spent": usd1})
	f.db.Set(budget.PathKillRepo(appSlug), map[string]any{"on": true, "by": "dana@example.com",
		"at": budgetStreamAt.Add(-2 * time.Hour).UnixMilli(), "reason": "a run looped on the same failing test"})
	f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "dana@example.com",
		"at": budgetStreamAt.Add(-time.Hour).UnixMilli(), "reason": "the day's spend reached the cap"})
	f.db.Set(budget.PathAgent(appSlug, "20261109-090000-aaaa"), map[string]any{
		"repo": "acme/app", "workflow": "web", "title": "add the export page", "stage": "implement", "round": 2,
		"startedAt": budgetStreamAt.Add(-45 * time.Minute).UnixMilli(), "updatedAt": budgetStreamAt.Add(-20 * time.Second).UnixMilli(),
	})
	return f
}

// budgetStreamRow is a row of the feed read the way the console's decoder reads
// it: the raw JSON, so a test can hold a row to the document's own bytes rather
// than to a second decoding of it.
type budgetStreamRow struct {
	Slug string          `json:"slug"`
	Raw  json.RawMessage `json:"-"`
}

// readBudgetFeed reads a budget feed the way a consumer does -- one line at a
// time, through the one reader of the line shape every feed shares -- and
// answers the rows with the slug each carries.
func readBudgetFeed(t *testing.T, out string) ([]budgetStreamRow, streamFeed) {
	t.Helper()
	feed := readFeed(t, out)
	rows := make([]budgetStreamRow, 0, len(feed.rows))
	for i, raw := range feed.rows {
		var head budgetStreamRow
		head.Raw = raw
		if err := json.Unmarshal(raw, &head); err != nil {
			t.Fatalf("row %d of the feed is not a row object: %v:\n%s", i+1, err, raw)
		}
		if head.Slug == "" {
			t.Fatalf("row %d of the feed carries no slug, so a consumer cannot place it:\n%s", i+1, raw)
		}
		rows = append(rows, head)
	}
	return rows, feed
}

// TestBudgetShowStreamRows: --stream carries the caps document's repositories,
// one per line, in the document's own order, each byte for byte the element
// --json's repos array holds, and then the project's kill switch as a row of
// its own -- the one thing about the project's caps that is read on its own
// (D2906, O3).
func TestBudgetShowStreamRows(t *testing.T) {
	budgetStreamFixture(t)

	docOut, _, err := execute(t, "budget", "show", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	feedOut, errOut, err := execute(t, "budget", "show", "--all", "--json", "--stream")
	if err != nil {
		t.Fatalf("budget show --stream: %v (%s)", err, errOut)
	}
	var doc struct {
		Global struct {
			Kill json.RawMessage `json:"kill"`
		} `json:"global"`
		Repos []json.RawMessage `json:"repos"`
	}
	if err := json.Unmarshal([]byte(docOut), &doc); err != nil {
		t.Fatalf("%v: %s", err, docOut)
	}
	rows, _ := readBudgetFeed(t, feedOut)
	if len(doc.Repos) < 2 {
		t.Fatalf("the fixture listed %d repositories, so this test proves nothing about the rows:\n%s", len(doc.Repos), docOut)
	}
	if len(rows) != len(doc.Repos)+1 {
		t.Fatalf("the feed carried %d rows, want the document's %d repositories and the kill switch:\n%s", len(rows), len(doc.Repos), feedOut)
	}
	for i := range doc.Repos {
		if got, want := compact(t, rows[i].Raw), compact(t, doc.Repos[i]); got != want {
			t.Errorf("row %d of the feed is not the document's repository:\n got %s\nwant %s", i, got, want)
		}
	}
	kill := rows[len(rows)-1]
	if kill.Slug != budgetKillRowSlug {
		t.Errorf("the feed's last row is %q, want the project's kill switch under %q", kill.Slug, budgetKillRowSlug)
	}
	var row struct {
		Slug string          `json:"slug"`
		Kill json.RawMessage `json:"kill"`
	}
	if err := json.Unmarshal(kill.Raw, &row); err != nil {
		t.Fatalf("%v: %s", err, kill.Raw)
	}
	if got, want := compact(t, row.Kill), compact(t, doc.Global.Kill); got != want {
		t.Errorf("the kill row carries %s, want the document's global kill:\n%s", got, want)
	}
	if !strings.Contains(string(doc.Global.Kill), "reached the cap") {
		t.Errorf("the fixture's project kill switch is not the one the feed carries: %s", doc.Global.Kill)
	}
}

// TestBudgetShowStreamEnd: the feed's last line is the end line, and it carries
// the count of every row that went before it -- the project's kill switch
// included, which is a row like any other. Without it a listing this credential
// may see nothing of and a read that was cut off are the same bytes, which is
// the one thing the flag exists to tell apart.
func TestBudgetShowStreamEnd(t *testing.T) {
	budgetStreamFixture(t)

	out, errOut, err := execute(t, "budget", "show", "--all", "--json", "--stream")
	if err != nil {
		t.Fatalf("budget show --stream: %v (%s)", err, errOut)
	}
	rows, feed := readBudgetFeed(t, out)
	if feed.end == nil {
		t.Fatalf("the feed ends with %q and carries no end line, so a reader cannot tell a listing that finished from one that was interrupted:\n%s", feed.last, out)
	}
	if feed.last != streamLineEnd {
		t.Errorf("the feed's last line is %q, want the end line", feed.last)
	}
	if feed.end.Count != len(rows) {
		t.Errorf("the end line counts %d rows, want the %d the feed carried:\n%s", feed.end.Count, len(rows), out)
	}
	if feed.end.Version != budgetStreamDocVersion {
		t.Errorf("the end line's version is %d, want budgetStreamDocVersion (%d)", feed.end.Version, budgetStreamDocVersion)
	}
	if feed.fail != "" {
		t.Errorf("a feed that ran to completion carries an error line: %q", feed.fail)
	}

	// --repo narrows the feed the way it narrows the document: one repository,
	// then the project's switch -- and the count is the feed's own, so it falls
	// with the rows.
	one, _, err := execute(t, "budget", "show", "--repo", "acme/app", "--json", "--stream")
	if err != nil {
		t.Fatal(err)
	}
	rows, feed = readBudgetFeed(t, one)
	if len(rows) != 2 || rows[0].Slug != appSlug || rows[1].Slug != budgetKillRowSlug {
		t.Fatalf("--repo carried %v, want the repository and the kill switch:\n%s", slugsOf(rows), one)
	}
	if feed.end == nil || feed.end.Count != 2 {
		t.Fatalf("--repo's feed ends with %+v, want an end line counting its two rows:\n%s", feed.end, one)
	}
}

func slugsOf(rows []budgetStreamRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Slug)
	}
	return out
}

// TestBudgetShowStreamKillRowAlwaysComes: the project's kill switch is a row of
// the feed whether or not it is on. A row that vanished with the switch would
// leave a consumer unable to tell a switch nobody fired from a feed that never
// carried one -- and the feed would then have no row saying which is which.
func TestBudgetShowStreamKillRowAlwaysComes(t *testing.T) {
	f := budgetStreamFixture(t)
	f.db.Set(budget.PathKillGlobal, nil)
	f.db.Set(budget.PathKillRepo(appSlug), nil)

	out, errOut, err := execute(t, "budget", "show", "--all", "--json", "--stream")
	if err != nil {
		t.Fatalf("budget show --stream: %v (%s)", err, errOut)
	}
	rows, feed := readBudgetFeed(t, out)
	if len(rows) == 0 || rows[len(rows)-1].Slug != budgetKillRowSlug {
		t.Fatalf("the feed's rows are %v, want the project's kill switch last:\n%s", slugsOf(rows), out)
	}
	last := rows[len(rows)-1]
	var row struct {
		Kill json.RawMessage `json:"kill"`
	}
	if err := json.Unmarshal(last.Raw, &row); err != nil {
		t.Fatal(err)
	}
	if string(row.Kill) != "null" {
		t.Errorf("a switch that is not on is carried as %s, want null:\n%s", row.Kill, last.Raw)
	}
	if feed.end == nil || feed.end.Count != len(rows) {
		t.Errorf("the feed does not end over its rows: %+v", feed.end)
	}
}

// TestBudgetShowStreamCompute: --compute means in the feed what it means in the
// document. The figure is the repository row's own member, so a row drawn as it
// arrives carries the compute of the day it was read on, null where the day
// recorded no estimate -- and the rows are the document's, member for member.
func TestBudgetShowStreamCompute(t *testing.T) {
	budgetStreamFixture(t)

	docOut, _, err := execute(t, "budget", "show", "--all", "--json", "--compute")
	if err != nil {
		t.Fatal(err)
	}
	feedOut, errOut, err := execute(t, "budget", "show", "--all", "--json", "--compute", "--stream")
	if err != nil {
		t.Fatalf("budget show --compute --stream: %v (%s)", err, errOut)
	}
	var doc struct {
		Repos []map[string]any `json:"repos"`
	}
	if err := json.Unmarshal([]byte(docOut), &doc); err != nil {
		t.Fatalf("%v: %s", err, docOut)
	}
	rows, feed := readBudgetFeed(t, feedOut)
	if feed.end == nil || feed.end.Count != len(rows) {
		t.Fatalf("the feed of a --compute listing does not end over its rows: %+v\n%s", feed.end, feedOut)
	}
	if len(doc.Repos) == 0 {
		t.Fatal("the fixture listed no repository, so this test proves nothing")
	}
	for i, want := range doc.Repos {
		if _, ok := want["compute_usd"]; !ok {
			t.Fatalf("--compute left no compute_usd on the document's row %d, so the flag no longer means what it did:\n%s", i, docOut)
		}
		var got map[string]any
		if err := json.Unmarshal(rows[i].Raw, &got); err != nil {
			t.Fatal(err)
		}
		if _, ok := got["compute_usd"]; !ok {
			t.Errorf("--compute left no compute_usd on feed row %d:\n%s", i, rows[i].Raw)
		}
	}
}

// TestBudgetShowStreamNeedsJSON: the flag writes JSON lines of the document's
// repos array, so it is accepted only with --json -- and it is refused before
// anything is opened, since a command that opened the database to refuse a flag
// would have read a project's caps to answer nothing.
func TestBudgetShowStreamNeedsJSON(t *testing.T) {
	newBudgetFixture(t, "")
	_, _, err := execute(t, "budget", "show", "--stream")
	if err == nil || !strings.Contains(err.Error(), "--stream") {
		t.Fatalf("err = %v, want a refusal naming --stream", err)
	}
}

// budgetShowJSON is today's whole document for the feed's fixture, recorded
// before --stream existed. Adding a flag may not move a byte of it: the
// console's rail, gauge and caps screens read it, and a member that appeared,
// moved or was retyped is a different document (docs/console-improvements
// implementation plan, RS4; CLAUDE.md's field-never-changes-meaning rule).
const budgetShowJSON = `{
  "project": "aurora",
  "gcp_project": "proj-1234",
  "mode": "observe",
  "day": "2026-11-09",
  "max_reserve_usd": null,
  "global": {
    "daily_cap_usd": 100,
    "per_run_cap_usd": 20,
    "counted_usd": 4.5,
    "spent_usd": 2.25,
    "notional_usd": 0.5,
    "leased_usd": 2.25,
    "headroom_usd": 95.5,
    "kill": {
      "by": "dana@example.com",
      "at": "2026-11-09T11:00:00Z",
      "reason": "the day's spend reached the cap"
    }
  },
  "defaults": {
    "repo_daily_cap_usd": null,
    "repo_per_run_cap_usd": null
  },
  "repos": [
    {
      "slug": "acme-app-dc4d4e59e884fc44",
      "repo": "acme/app",
      "daily_cap_usd": 25,
      "daily_cap_source": "repo",
      "per_run_cap_usd": 4,
      "per_run_cap_source": "repo",
      "counted_usd": 3.5,
      "spent_usd": 1.25,
      "notional_usd": 0.5,
      "leased_usd": 2.25,
      "headroom_usd": 21.5,
      "kill": {
        "by": "dana@example.com",
        "at": "2026-11-09T10:00:00Z",
        "reason": "a run looped on the same failing test"
      },
      "running": [
        {
          "slug": "acme-app-dc4d4e59e884fc44",
          "run": "20261109-090000-aaaa",
          "repo": "acme/app",
          "workflow": "web",
          "title": "add the export page",
          "stage": "implement",
          "round": 2,
          "spent_usd": 0,
          "started_at": "2026-11-09T11:15:00Z",
          "updated_at": "2026-11-09T11:59:40Z"
        }
      ]
    },
    {
      "slug": "acme-other-1140789cbb83e234",
      "daily_cap_usd": null,
      "daily_cap_source": "none",
      "per_run_cap_usd": 20,
      "per_run_cap_source": "global",
      "counted_usd": 1,
      "spent_usd": 1,
      "notional_usd": 0,
      "leased_usd": 0,
      "headroom_usd": 95.5,
      "kill": null,
      "running": []
    }
  ],
  "running": [
    {
      "slug": "acme-app-dc4d4e59e884fc44",
      "run": "20261109-090000-aaaa",
      "repo": "acme/app",
      "workflow": "web",
      "title": "add the export page",
      "stage": "implement",
      "round": 2,
      "spent_usd": 0,
      "started_at": "2026-11-09T11:15:00Z",
      "updated_at": "2026-11-09T11:59:40Z"
    }
  ],
  "warnings": []
}
`

// TestBudgetShowJSONUnchanged: `budget show --json` prints the document it
// printed before --stream existed, byte for byte. The flag is additive or it is
// a retyping of a document the console already holds.
func TestBudgetShowJSONUnchanged(t *testing.T) {
	budgetStreamFixture(t)

	out, errOut, err := execute(t, "budget", "show", "--all", "--json")
	if err != nil {
		t.Fatalf("budget show --json: %v (%s)", err, errOut)
	}
	if out != budgetShowJSON {
		t.Errorf("budget show --json no longer prints the document it printed before --stream:\n got:\n%s\nwant:\n%s", out, budgetShowJSON)
	}
}

// compileStreamSchema compiles a --stream feed's schema from schemas/, the
// contract the console's decoder is written against.
func compileStreamSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatalf("schemas/%s has no $id", name)
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

// TestBudgetStreamSchema validates every line of the recorded feed against
// schemas/budget-stream.schema.json, and holds the schema to refusing the lines
// the shape does not have -- a schema that accepted everything would be a
// comment on the feed rather than its contract.
func TestBudgetStreamSchema(t *testing.T) {
	sch := compileStreamSchema(t, "budget-stream.schema.json")
	data, err := os.ReadFile(filepath.Join("testdata", "budget_stream.golden.ndjson"))
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
			t.Errorf("line %d of the golden does not match schemas/budget-stream.schema.json: %v\n%s", i+1, err, line)
		}
	}
	if lines == 0 {
		t.Fatal("the golden carries no line")
	}

	for _, bad := range []string{
		`{"type":"end","count":1}`,               // an end line that does not say which shape it ends
		`{"type":"end","version":1}`,             // and one that does not count the rows it carried
		`{"type":"end","count":"1","version":1}`, // a count that is not a number
		`{"type":"row"}`,                         // a row line with no row
		`{"type":"row","row":1}`,                 // a row that is not an object
		`{"type":"row","row":{}}`,                // a row nothing can place: no scope at all
		`{"type":"row","row":{"slug":""}}`,       // and one with an empty scope
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

var updateBudgetStreamGolden = flag.Bool("update-budget-stream", false, "rewrite testdata/budget_stream.golden.ndjson")

// TestBudgetStreamGolden pins the feed the CLI writes -- the line kinds, their
// members and the rows' own bytes -- as the file the console's decoder is
// compared with. The fixture's clock is the database fake's, so the golden is
// the same file tomorrow.
func TestBudgetStreamGolden(t *testing.T) {
	budgetStreamFixture(t)

	out, errOut, err := execute(t, "budget", "show", "--all", "--json", "--stream")
	if err != nil {
		t.Fatalf("budget show --stream: %v (%s)", err, errOut)
	}
	path := filepath.Join("testdata", "budget_stream.golden.ndjson")
	if *updateBudgetStreamGolden {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file: run go test ./internal/cli -run TestBudgetStreamGolden -update-budget-stream")
	}
	if string(want) != out {
		t.Fatalf("the feed changed (go test ./internal/cli -run TestBudgetStreamGolden -update-budget-stream to accept):\n%s\nwant:\n%s", out, want)
	}
	// The console reads this file as bytes and decodes it, so it is NDJSON and
	// nothing else: no blank line, no comment, no second document.
	rows, feed := readBudgetFeed(t, string(want))
	if feed.end == nil || len(rows) != feed.end.Count {
		t.Errorf("the golden is not a feed that ran to completion: %d rows, end %+v", len(rows), feed.end)
	}
	if feed.last != streamLineEnd {
		t.Errorf("the golden's last line is %q, want %q", feed.last, streamLineEnd)
	}
	if len(rows) == 0 || rows[len(rows)-1].Slug != budgetKillRowSlug {
		t.Errorf("the golden does not end its rows with the project's kill switch: %v", slugsOf(rows))
	}
}
