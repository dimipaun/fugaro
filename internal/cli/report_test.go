package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

type reportFixture struct {
	*cloudFixture
	fs  *gcpfake.Firestore
	db  *gcpfake.RTDB
	now time.Time
}

// newReportFixture is a project with a Firebase backend: an RTDB fake, a
// Firestore fake holding the history mark, and the clock at 2026-10-20 12:00.
func newReportFixture(t *testing.T) *reportFixture { return newReportFixtureOpt(t, true) }

func newReportFixtureOpt(t *testing.T, mark bool) *reportFixture {
	t.Helper()
	fs := gcpfake.NewFirestore(t)
	f := &reportFixture{cloudFixture: newCloudFixture(t, "firestore: "+fs.URL), fs: fs, db: gcpfake.NewRTDB(t),
		now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+f.db.URL+", firebase_project: fugaro-fp-1 }\n")
	f.db.Set("fugaro/project", "aurora")
	if mark {
		fs.Set("meta", "installation", map[string]any{"project": "aurora", "version": int64(1)})
	}
	old := budgetNow
	budgetNow = func() time.Time { return f.now }
	t.Cleanup(func() { budgetNow = old })
	return f
}

type repOpts struct {
	spent, notional, compute int64
	person, model            string
	final                    bool
	slug, repo               string
	runs, estimated          int64
}

func (f *reportFixture) doc(date string, o repOpts) {
	if o.slug == "" {
		o.slug, o.repo = appSlug, "acme/app"
	}
	if o.person == "" {
		o.person = "dev@example.com"
	}
	if o.model == "" {
		o.model = "claude-sonnet"
	}
	if o.runs == 0 {
		o.runs = 1
	}
	r := budget.DayRecord{
		Repo: o.repo, Slug: o.slug, Date: date, Version: 1, Final: o.final,
		SpentMicros: budget.Micros(o.spent), NotionalMicros: budget.Micros(o.notional), ComputeMicros: budget.Micros(o.compute),
		ComputeEstimatedRuns: o.estimated, Runs: o.runs, Calls: 3, RunHours: 1.5,
		Outcomes: map[string]int64{"succeeded": o.runs},
		ByModel:  map[string]budget.ModelTotals{budget.Key(o.model): {Micros: budget.Micros(o.spent), In: 100, Out: 50}},
		ByPerson: map[string]budget.PersonTotals{budget.Key(o.person): {Micros: budget.Micros(o.spent), NotionalMicros: budget.Micros(o.notional), Runs: o.runs}},
	}
	f.fs.Set("spendDaily", r.DocID(), r.ToFields())
}

type repOut struct {
	Project  string   `json:"project"`
	Source   string   `json:"source"`
	Degraded bool     `json:"degraded"`
	Warnings []string `json:"warnings"`
	Rows     []struct {
		Key           string  `json:"key"`
		Partial       bool    `json:"partial"`
		ModelMicros   int64   `json:"model_micros"`
		ModelUSD      string  `json:"model_usd"`
		NotionalUSD   *string `json:"notional_usd"`
		ComputeUSD    *string `json:"compute_usd"`
		Runs          int64   `json:"runs"`
		ComputeEstArg bool    `json:"compute_estimated"`
	} `json:"rows"`
	Totals struct {
		ModelMicros int64 `json:"model_micros"`
		Runs        int64 `json:"runs"`
	} `json:"totals"`
}

func (f *reportFixture) json(t *testing.T, args ...string) repOut {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"report", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("%v\nstderr: %s", err, errOut)
	}
	var o repOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	return o
}

func TestReportRangeDefaults(t *testing.T) {
	today := budget.Day(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	from, to, err := reportRange("30d", "", today)
	if err != nil || to != today || to-from+1 != 30 {
		t.Fatalf("default: %d..%d %v", from, to, err)
	}
	from, to, err = reportRange("2026-10-01", "2026-12-31", today)
	if err != nil || to != today || budget.DayDate(from) != "2026-10-01" {
		t.Fatalf("future until clamps to today: %d %d %v", from, to, err)
	}
	from, to, err = reportRange("12w", "", today)
	if err != nil || to != today || to-from+1 != 84 {
		t.Fatalf("12w: %d..%d %v", from, to, err)
	}
	for _, bad := range [][2]string{{"0w", ""}, {"-1w", ""}, {"w", ""}, {"1.5w", ""}, {"9999w", ""}, {"0d", ""}, {"x", ""}, {"2026-10-05", "2026-10-01"}, {"2026-13-01", ""}, {"30d", "nope"}, {"99999d", ""}} {
		if _, _, err := reportRange(bad[0], bad[1], today); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestReportFromHistoryWithLiveToday(t *testing.T) {
	f := newReportFixture(t)
	f.doc("2026-10-15", repOpts{spent: 1_500_000, final: true, compute: 200_000, estimated: 1})
	f.doc("2026-10-16", repOpts{spent: 2_000_000, notional: 700_000, final: true})
	f.doc("2026-10-19", repOpts{spent: 500_000}) // provisional, no RTDB node left
	d := budget.Day(f.now)
	f.db.Set(budget.PathSpendGlobal(d), map[string]any{"spent": 3_000_000, "counted": 3_000_000, "calls": 4})
	f.db.Set(budget.PathSpendRepo(d, appSlug), map[string]any{"spent": 3_000_000, "counted": 3_000_000, "calls": 4,
		"byModel": map[string]any{"claude-sonnet": map[string]any{"micros": 3_000_000, "in": 10, "out": 5}}})
	f.db.Set("spend/"+budget.DayKey(d)+"/runs/"+appSlug+"/20261020-100000-aaaa", map[string]any{"reserved": 3_000_000, "spent": 3_000_000})

	o := f.json(t, "--since", "2026-10-14")
	if o.Project != "aurora" || o.Source != "firestore" || o.Degraded {
		t.Fatalf("%+v", o)
	}
	got := map[string]string{}
	partial := map[string]bool{}
	for _, r := range o.Rows {
		got[r.Key] = r.ModelUSD
		partial[r.Key] = r.Partial
	}
	if got["2026-10-15"] != "1.500000" || got["2026-10-16"] != "2.000000" || got["2026-10-19"] != "0.500000" || got["2026-10-20"] != "3.000000" {
		t.Fatalf("rows %v", got)
	}
	if partial["2026-10-15"] || partial["2026-10-16"] || !partial["2026-10-19"] || !partial["2026-10-20"] {
		t.Fatalf("partial %v", partial)
	}
	if o.Totals.ModelMicros != 7_000_000 {
		t.Fatalf("total %d", o.Totals.ModelMicros)
	}
	for _, r := range o.Rows {
		if r.Key == "2026-10-16" && (r.NotionalUSD == nil || *r.NotionalUSD != "0.700000" || r.ModelUSD != "2.000000") {
			t.Fatalf("notional leaked into model: %+v", r)
		}
		if r.Key == "2026-10-16" && r.ComputeUSD != nil {
			t.Fatalf("compute not estimated must be null: %+v", r)
		}
		if r.Key == "2026-10-15" && (r.ComputeUSD == nil || *r.ComputeUSD != "0.200000") {
			t.Fatalf("%+v", r)
		}
	}

	out, errOut, err := execute(t, "report", "--since", "2026-10-14")
	if err != nil || !strings.Contains(out, "2026-10-20 (partial)") || !strings.Contains(out, "(partial)") || strings.ContainsRune(out, 0x1b) {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(errOut, "project: aurora") {
		t.Fatalf("stderr %q", errOut)
	}
}

func TestReportPartialIsLiveFromRTDB(t *testing.T) {
	f := newReportFixture(t)
	// A provisional document says 1.00 but the database now holds 4.00: the
	// live figure wins.
	yesterday := budget.Day(f.now) - 1
	f.doc(budget.DayDate(yesterday), repOpts{spent: 1_000_000})
	f.db.Set(budget.PathSpendGlobal(yesterday), map[string]any{"spent": 4_000_000, "calls": 1})
	f.db.Set(budget.PathSpendRepo(yesterday, appSlug), map[string]any{"spent": 4_000_000, "calls": 1})
	o := f.json(t, "--since", "2d")
	if len(o.Rows) != 1 || o.Rows[0].ModelUSD != "4.000000" || !o.Rows[0].Partial {
		t.Fatalf("%+v", o.Rows)
	}
}

func TestReportByWeekMonthYearCrossingYear(t *testing.T) {
	f := newReportFixture(t)
	f.now = time.Date(2027, 1, 10, 12, 0, 0, 0, time.UTC)
	for date, spent := range map[string]int64{"2026-12-27": 1_000_000, "2026-12-28": 2_000_000, "2027-01-03": 4_000_000, "2027-01-04": 8_000_000} {
		f.doc(date, repOpts{spent: spent, final: true})
	}
	rows := func(by string) map[string]string {
		m := map[string]string{}
		for _, r := range f.json(t, "--since", "2026-12-01", "--by", by).Rows {
			m[r.Key] = r.ModelUSD
		}
		return m
	}
	if w := rows("week"); len(w) != 3 || w["2026-W52"] != "1.000000" || w["2026-W53"] != "6.000000" || w["2027-W01"] != "8.000000" {
		t.Fatalf("week %v", w)
	}
	if m := rows("month"); len(m) != 2 || m["2026-12"] != "3.000000" || m["2027-01"] != "12.000000" {
		t.Fatalf("month %v", m)
	}
	if y := rows("year"); len(y) != 2 || y["2026"] != "3.000000" || y["2027"] != "12.000000" {
		t.Fatalf("year %v", y)
	}
	if d := rows("day"); len(d) != 4 {
		t.Fatalf("day %v", d)
	}
}

func TestReportRepoFilterAndGroupings(t *testing.T) {
	f := newReportFixture(t)
	f.doc("2026-10-15", repOpts{spent: 1_000_000, final: true, person: "a@x.io", model: "m1"})
	f.doc("2026-10-15", repOpts{spent: 5_000_000, final: true, slug: "github-other-repo", repo: "other/repo", person: "b@x.io", model: "m2"})
	o := f.json(t, "--by", "repo")
	if len(o.Rows) != 2 || o.Rows[0].Key != "other/repo" || o.Rows[1].Key != "acme/app" {
		t.Fatalf("repo %+v", o.Rows)
	}
	o = f.json(t, "--by", "person", "--repo", "acme/app")
	if len(o.Rows) != 1 || o.Rows[0].Key != "a@x.io" || o.Rows[0].ModelUSD != "1.000000" {
		t.Fatalf("person %+v", o.Rows)
	}
	o = f.json(t, "--by", "model")
	if len(o.Rows) != 2 || o.Rows[0].Key != "m2" {
		t.Fatalf("model %+v", o.Rows)
	}
}

func TestReportHostileStringsAndOddDocs(t *testing.T) {
	f := newReportFixture(t)
	f.doc("2026-10-15", repOpts{spent: 1_000_000, final: true, person: "evil\x1b]8;;http://x\x07@x.io", model: "m\x1b[2Jx", repo: "o/\x1b[31mr", slug: "github-o-r"})
	// Missing fields only: zeros, still shown.
	f.fs.Set("spendDaily", "2026-10-16_github-sparse", map[string]any{"slug": "github-sparse", "date": "2026-10-16", "version": int64(1)})
	// Odd types, a bad date and an out-of-range date: left out with a warning.
	f.fs.Set("spendDaily", "2026-10-17_github-odd", map[string]any{"slug": "github-odd", "date": "2026-10-17", "version": int64(1), "spentMicros": "lots"})
	f.fs.Set("spendDaily", "x_github-baddate", map[string]any{"slug": "github-baddate", "date": "2026-10-18\x1b", "version": int64(1)})
	for _, args := range [][]string{{"--by", "person"}, {"--by", "model"}, {"--by", "repo"}, {"--by", "day"}} {
		for _, mode := range []string{"", "--json", "--csv"} {
			a := append([]string{"report", "--since", "2026-10-01"}, args...)
			if mode != "" {
				a = append(a, mode)
			}
			out, errOut, err := execute(t, a...)
			if err != nil {
				t.Fatalf("%v: %v", a, err)
			}
			for _, s := range []string{out, errOut} {
				if strings.ContainsAny(s, "\x1b\x07") {
					t.Fatalf("%v: control character in %q", a, s)
				}
			}
			if !strings.Contains(out+errOut, "could not be read") && mode != "--csv" {
				t.Fatalf("%v: no warning about the unreadable documents:\n%s", a, out)
			}
		}
	}
	o := f.json(t, "--since", "2026-10-01")
	if len(o.Rows) != 2 || o.Rows[0].Key != "2026-10-15" || o.Rows[1].Key != "2026-10-16" || o.Rows[1].ModelUSD != "0.000000" {
		t.Fatalf("%+v", o.Rows)
	}
	out, _, _ := execute(t, "report", "--since", "2026-10-01", "--by", "person")
	if !strings.Contains(out, "evil]8;;http://x@x.io") && !strings.Contains(out, "evil") {
		t.Fatalf("person lost:\n%s", out)
	}
}

func TestReportEmptyRange(t *testing.T) {
	f := newReportFixture(t)
	_ = f
	out, _, err := execute(t, "report", "--since", "2026-09-01", "--until", "2026-09-05")
	if err != nil || !strings.Contains(out, "no spend recorded between 2026-09-01 and 2026-09-05") {
		t.Fatalf("%v\n%s", err, out)
	}
	if o := f.json(t, "--since", "2026-09-01", "--until", "2026-09-05"); o.Rows == nil || len(o.Rows) != 0 {
		t.Fatalf("%+v", o)
	}
}

func TestReportCSVCLI(t *testing.T) {
	f := newReportFixture(t)
	f.doc("2026-10-15", repOpts{spent: 1_234_567, final: true, repo: "=cmd|' /C calc'!A0", slug: "github-evil"})
	out, _, err := execute(t, "report", "--csv", "--by", "repo", "--since", "2026-10-01")
	if err != nil || !strings.Contains(out, "'=cmd") || !strings.Contains(out, "1.234567") || !strings.Contains(out, "\r\n") {
		t.Fatalf("%v\n%q", err, out)
	}
	if _, _, err := execute(t, "report", "--csv", "--json"); err == nil {
		t.Fatal("--csv with --json accepted")
	}
	if _, _, err := execute(t, "report", "--by", "decade"); err == nil || !strings.Contains(err.Error(), "--by") || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("%v", err)
	}
}

// seedModeRun writes one run's result.json under slug, with the rigor mode the
// run resolved ("" for a record that carries no rigor block), so a test can
// plant the one fact --by mode groups by.
func seedModeRun(t *testing.T, f *cloudFixture, slug, id, mode string, c runstore.Cost) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	st, err := runstore.RunTime(id)
	if err != nil {
		t.Fatal(err)
	}
	fin := st.Add(time.Hour)
	rec := &runstore.Record{RunID: id, Repo: "acme/app", Status: "succeeded", StartedAt: st, FinishedAt: &fin, Cost: &c, CostUSD: c.ModelUSD}
	if mode != "" {
		rec.Rigor = &runstore.RigorRecord{Mode: rigor.Mode(mode)}
	}
	if err := runstore.Open(b, slug, id).WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// seedCorruptRun writes a result.json the bucket holds and this build cannot
// read: the run did start, and its cost is unknown, which is not the same as a
// run that never started and not the same as one that spent nothing.
func seedCorruptRun(t *testing.T, f *cloudFixture, slug, id string) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.WriteAll(ctx, runstore.Open(b, slug, id).Prefix()+"result.json", []byte(`{"version": 1, "run_id": `), nil); err != nil {
		t.Fatal(err)
	}
}

// withReportMaxRuns lowers the by-mode read's cap for one test, so a window too
// large to read whole can be driven with three runs rather than thousands.
func withReportMaxRuns(t *testing.T, n int) {
	t.Helper()
	old := reportMaxRuns
	reportMaxRuns = n
	t.Cleanup(func() { reportMaxRuns = old })
}

// modeRows reads a document's rows into the map a by-mode test asserts on.
func modeRows(o repOut) map[string]modeRow {
	by := map[string]modeRow{}
	for _, r := range o.Rows {
		notional := "-"
		if r.NotionalUSD != nil {
			notional = *r.NotionalUSD
		}
		by[r.Key] = modeRow{model: r.ModelUSD, notional: notional, runs: fmt.Sprint(r.Runs), compute: r.ComputeUSD}
	}
	return by
}

// modeRow is one by-mode row as a test reads it.
type modeRow struct {
	model, notional, runs string
	compute               *string
}

// TestReportByModeGroupsRuns: the mode is the run's own record's, each run
// counts once, an unknown mode reads as none and never as the raw text, and
// the money and the notional stay apart.
func TestReportByModeGroupsRuns(t *testing.T) {
	f := newReportFixture(t)
	seedModeRun(t, f.cloudFixture, appSlug, "20261015-120000-aaaa", "pro", runstore.NewCost(1.50, 0.25, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, appSlug, "20261016-120000-bbbb", "critical", runstore.NewCost(2.25, 0.10, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, appSlug, "20261017-120000-cccc", "", runstore.ModelOnlyCost(3.00, runstore.BasisSubscription))
	seedModeRun(t, f.cloudFixture, appSlug, "20261018-120000-dddd", "turbo", runstore.ModelOnlyCost(0.50, runstore.BasisAPIList))

	o := f.json(t, "--by", "mode", "--since", "2026-10-01")
	if o.Source != "run-records" || o.Degraded {
		t.Fatalf("source = %q, degraded = %v", o.Source, o.Degraded)
	}
	if len(o.Rows) != 3 {
		t.Fatalf("rows %+v", o.Rows)
	}
	by := modeRows(o)
	if len(by) != 3 {
		t.Fatalf("an unknown mode did not read as none: %+v", o.Rows)
	}
	if p, ok := by["pro"]; !ok || p.model != "1.500000" || p.runs != "1" || p.notional != "0.000000" || p.compute == nil || *p.compute != "0.250000" {
		t.Fatalf("pro %+v", by)
	}
	if c, ok := by["critical"]; !ok || c.model != "2.250000" || c.runs != "1" || c.compute == nil || *c.compute != "0.100000" {
		t.Fatalf("critical %+v", by)
	}
	// none: the record with no rigor block and the record whose mode is not one
	// of the vocabulary's, each counted once; the subscription run's figure is
	// notional and never model money, and no run of the row estimated compute,
	// so the column reads n/a rather than 0.
	n, ok := by["none"]
	if !ok || n.model != "0.500000" || n.runs != "2" || n.notional != "3.000000" || n.compute != nil {
		t.Fatalf("none %+v", by)
	}
	if o.Totals.ModelMicros != 4_250_000 {
		t.Fatalf("total %d", o.Totals.ModelMicros)
	}
	out, errOut, err := execute(t, "report", "--by", "mode", "--since", "2026-10-01")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{"by mode", "pro", "critical", "none", "run records", "n/a"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table lacks %q:\n%s", want, out)
		}
	}
	// The key column is MODE, which the bare word cannot show: the "MODEL $"
	// column beside it satisfies a search for "MODE". The header's own fields
	// are the fact.
	var head []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "MODE" {
			head = f
			break
		}
	}
	if len(head) == 0 || head[1] != "MODEL" {
		t.Errorf("the table's key column is not MODE beside MODEL $: its header reads %q\n%s", strings.Join(head, " "), out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("the table carries an escape:\n%q", out)
	}
	// A run record carries no run hours, so no row of this grouping has one to
	// show: run_hours is null on every row and in the totals -- never the 0 a
	// reader would take for a measured hour -- though the pro row above has a
	// known compute. The key itself stays, as the contract pins
	// (JSONLine.RunHours carries no omitempty), so an omitempty added later
	// fails here rather than passing quietly.
	raw, _, err := execute(t, "report", "--json", "--by", "mode", "--since", "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rows   []map[string]json.RawMessage `json:"rows"`
		Totals map[string]json.RawMessage   `json:"totals"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%v in %s", err, raw)
	}
	lines := append(doc.Rows, doc.Totals)
	if len(lines) < 2 {
		t.Fatalf("rows %+v, totals %+v: the document carries no run_hours to read", doc.Rows, doc.Totals)
	}
	for _, l := range lines {
		if got := string(l["run_hours"]); got != "null" {
			t.Errorf("run_hours = %q, want null on every row: a by-mode row knows no run hours, and the key is pinned present with no figure in it", got)
		}
	}
}

// TestReportByModeRange: a run outside --since/--until is in no row, and --repo
// filters as it does everywhere else.
func TestReportByModeRange(t *testing.T) {
	f := newReportFixture(t)
	seedModeRun(t, f.cloudFixture, appSlug, "20261010-120000-aaaa", "pro", runstore.NewCost(9.00, 0, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, appSlug, "20261015-120000-bbbb", "pro", runstore.NewCost(1.00, 0, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, appSlug, "20261020-120000-cccc", "pro", runstore.NewCost(5.00, 0, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, "github-other-repo", "20261015-120000-dddd", "pro", runstore.NewCost(7.00, 0, runstore.BasisAPIList))

	// The window: the run before --since and the run after --until are in no
	// row.
	o := f.json(t, "--by", "mode", "--since", "2026-10-14", "--until", "2026-10-16", "--repo", "acme/app")
	if len(o.Rows) != 1 || o.Rows[0].Key != "pro" || o.Rows[0].ModelUSD != "1.000000" || o.Rows[0].Runs != 1 {
		t.Fatalf("range rows %+v", o.Rows)
	}
	// Both repositories of the bucket, over the whole window.
	if o = f.json(t, "--by", "mode", "--since", "2026-10-01"); o.Totals.ModelMicros != 22_000_000 {
		t.Fatalf("the whole window = %d, want every repository's runs", o.Totals.ModelMicros)
	}
	// --repo names one repository's runs: three of the four.
	o = f.json(t, "--by", "mode", "--since", "2026-10-01", "--repo", "acme/app")
	if o.Totals.ModelMicros != 15_000_000 || len(o.Rows) != 1 || o.Rows[0].Runs != 3 {
		t.Fatalf("--repo acme/app = %+v, want this repository's three runs", o)
	}
}

// TestReportByModePastWindow: --until may end behind the newest run of the
// bucket, and the runs after it are dropped before the read's cap rather than
// after it. Dropped after, a couple of runs launched since would use up the
// read and the window's own runs would be reported as nothing.
func TestReportByModePastWindow(t *testing.T) {
	f := newReportFixture(t)
	withReportMaxRuns(t, 2)
	seedModeRun(t, f.cloudFixture, appSlug, "20261015-120000-aaaa", "pro", runstore.NewCost(1.00, 0, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, appSlug, "20261018-120000-bbbb", "critical", runstore.NewCost(8.00, 0, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, appSlug, "20261019-120000-cccc", "critical", runstore.NewCost(9.00, 0, runstore.BasisAPIList))

	o := f.json(t, "--by", "mode", "--since", "2026-10-14", "--until", "2026-10-16")
	if len(o.Rows) != 1 || o.Rows[0].Key != "pro" || o.Rows[0].ModelUSD != "1.000000" || o.Rows[0].Runs != 1 {
		t.Fatalf("a window ending in the past = %+v, want the one run it holds", o.Rows)
	}
	// The window holds one run, so nothing was left unread and the report says
	// nothing about a truncation: the runs it dropped were never the window's.
	for _, w := range o.Warnings {
		if strings.Contains(w, "more runs than") {
			t.Errorf("warnings = %q, want no truncation for a window of one run", o.Warnings)
		}
	}
}

// TestReportByModeTruncation: a window holding more runs than the read opens is
// reported to its newest runs, and says which, rather than publishing figures a
// reader would take for the whole window.
func TestReportByModeTruncation(t *testing.T) {
	f := newReportFixture(t)
	withReportMaxRuns(t, 2)
	seedModeRun(t, f.cloudFixture, appSlug, "20261015-120000-aaaa", "pro", runstore.NewCost(1.00, 0, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, appSlug, "20261016-120000-bbbb", "pro", runstore.NewCost(2.00, 0, runstore.BasisAPIList))
	seedModeRun(t, f.cloudFixture, appSlug, "20261017-120000-cccc", "pro", runstore.NewCost(4.00, 0, runstore.BasisAPIList))

	o := f.json(t, "--by", "mode", "--since", "2026-10-01")
	by := modeRows(o)
	if len(by) != 1 || by["pro"].runs != "2" || o.Totals.ModelMicros != 6_000_000 || o.Totals.Runs != 2 {
		t.Fatalf("an over-cap window = %+v, want its newest two runs", o)
	}
	warnings := strings.Join(o.Warnings, "\n")
	if !strings.Contains(warnings, "more runs than the 2") || !strings.Contains(warnings, "newest 2") {
		t.Errorf("warnings = %q, want the truncation said", o.Warnings)
	}
}

// TestReportByModeUnreadableRecord: a run whose result.json the bucket holds and
// this build cannot read did launch, so it counts once among the runs and adds
// nothing to the money: the row it lands in reads 0 in the money columns, and
// nothing on the wire marks it.
func TestReportByModeUnreadableRecord(t *testing.T) {
	f := newReportFixture(t)
	seedModeRun(t, f.cloudFixture, appSlug, "20261016-120000-aaaa", "pro", runstore.NewCost(1.50, 0.25, runstore.BasisAPIList))
	seedCorruptRun(t, f.cloudFixture, appSlug, "20261017-120000-bbbb")

	o := f.json(t, "--by", "mode", "--since", "2026-10-01")
	by := modeRows(o)
	if p := by["pro"]; p.runs != "1" || p.model != "1.500000" || p.compute == nil || *p.compute != "0.250000" {
		t.Fatalf("pro %+v", by)
	}
	if n, ok := by["none"]; !ok || n.runs != "1" || n.model != "0.000000" || n.notional != "0.000000" || n.compute != nil {
		t.Fatalf("the unreadable run's row = %+v, want it counted once and priced nowhere", by)
	}
	if o.Totals.Runs != 2 || o.Totals.ModelMicros != 1_500_000 {
		t.Fatalf("totals = %+v, want both runs and one run's money", o.Totals)
	}
}

// TestReportByModeJSONSource: the document names the run records as its source,
// --csv works, and the breakdown needs neither the spend history nor the budget
// database: both fake backends are taken away.
func TestReportByModeJSONSource(t *testing.T) {
	f := newReportFixture(t)
	f.fs.RemoveDatabase()
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	seedModeRun(t, f.cloudFixture, appSlug, "20261019-120000-aaaa", "hotfix", runstore.NewCost(2.00, 0.50, runstore.BasisAPIList))

	o := f.json(t, "--by", "mode", "--since", "2026-10-01")
	if o.Source != "run-records" || o.Degraded || len(o.Rows) != 1 || o.Rows[0].Key != "hotfix" ||
		o.Rows[0].ModelUSD != "2.000000" || o.Rows[0].ComputeUSD == nil || *o.Rows[0].ComputeUSD != "0.500000" {
		t.Fatalf("%+v", o)
	}
	if !strings.Contains(strings.Join(o.Warnings, "\n"), "run records") {
		t.Errorf("warnings = %q, want the run-record source named", o.Warnings)
	}
	out, _, err := execute(t, "report", "--csv", "--by", "mode", "--since", "2026-10-01")
	if err != nil || !strings.Contains(out, "hotfix") || !strings.Contains(out, "2.000000") || !strings.Contains(out, "\r\n") {
		t.Fatalf("%v\n%q", err, out)
	}
}

func TestReportViewerRoleRefusalText(t *testing.T) {
	f := newReportFixture(t)
	f.fs.Refuse(403, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Missing or insufficient permissions.")
	_, _, err := execute(t, "report")
	if err == nil || !strings.Contains(err.Error(), "roles/datastore.viewer") || !strings.Contains(err.Error(), "fugaro-fp-1") {
		t.Fatalf("err = %v", err)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitUserError {
		t.Fatalf("exit %v", err)
	}
}

func TestReportRefusesForeignDatabase(t *testing.T) {
	f := newReportFixture(t)
	f.fs.Set("meta", "installation", map[string]any{"project": "someone-else", "version": int64(1)})
	if _, _, err := execute(t, "report"); err == nil || !strings.Contains(err.Error(), "someone-else") {
		t.Fatalf("err = %v", err)
	}
}

func seedCostRun(t *testing.T, f *cloudFixture, id, who string, c runstore.Cost) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	if err := s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", RequestedBy: who}); err != nil {
		t.Fatal(err)
	}
	st, _ := runstore.RunTime(id)
	fin := st.Add(time.Hour)
	if err := s.WriteRecord(ctx, &runstore.Record{RunID: id, Repo: "acme/app", Status: "succeeded", StartedAt: st, FinishedAt: &fin, Cost: &c, CostUSD: c.ModelUSD}); err != nil {
		t.Fatal(err)
	}
}

func degradedRuns(t *testing.T, f *cloudFixture) {
	t.Helper()
	c1 := runstore.NewCost(1.5, 0.25, runstore.BasisAPIList)
	c2 := runstore.NewCost(2, 0.1, runstore.BasisSubscription)
	seedCostRun(t, f, "20261019-100000-aaaa", "a@x.io", c1)
	seedCostRun(t, f, "20261020-100000-bbbb", "b@x.io", c2)
}

func TestDegradedFromRuns(t *testing.T) {
	f := newCloudFixture(t) // no budget block: no Firebase backend
	old := budgetNow
	budgetNow = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { budgetNow = old })
	degradedRuns(t, f)
	out, errOut, err := execute(t, "report", "--since", "2026-10-01")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out, "spend history is not enabled") || !strings.Contains(out, "run records") || !strings.Contains(out, "not budget history") ||
		!strings.Contains(out, "2026-10-19") || !strings.Contains(out, "$1.50") || !strings.Contains(out, "$2.00") || strings.ContainsRune(out, 0x1b) {
		t.Fatalf("out:\n%s", out)
	}
	if !strings.HasPrefix(errOut, "project: aurora") {
		t.Fatalf("stderr %q", errOut)
	}
	o := f.jsonDegraded(t)
	if !o.Degraded || o.Source != "run-records" {
		t.Fatalf("%+v", o)
	}
	var sub *string
	var model string
	for _, r := range o.Rows {
		if r.Key == "2026-10-20" {
			sub, model = r.NotionalUSD, r.ModelUSD
		}
	}
	if sub == nil || *sub != "2.000000" || model != "0.000000" {
		t.Fatalf("subscription run must be notional only: %+v", o.Rows)
	}
}

func (f *cloudFixture) jsonDegraded(t *testing.T) repOut {
	t.Helper()
	out, _, err := execute(t, "report", "--json", "--since", "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	var o repOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestNoHistoryDatabaseMessage(t *testing.T) {
	f := newReportFixture(t)
	degradedRuns(t, f.cloudFixture)
	f.fs.RemoveDatabase()
	out, _, err := execute(t, "report", "--since", "2026-10-01")
	if err != nil || !strings.Contains(out, "spend history is not enabled") || !strings.Contains(out, "no Firestore database") || !strings.Contains(out, "$1.50") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// A database whose history mark is missing is "not enabled" too.
func TestNoHistoryMarkMessage(t *testing.T) {
	f := newReportFixtureOpt(t, false)
	degradedRuns(t, f.cloudFixture)
	out, _, err := execute(t, "report", "--since", "2026-10-01")
	if err != nil || !strings.Contains(out, "spend history is not enabled") || !strings.Contains(out, "no history mark") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestReportDimParsing(t *testing.T) {
	for _, s := range []string{"day", "week", "month", "year", "repo", "model", "person", "mode"} {
		if _, err := report.ParseDim(s); err != nil {
			t.Error(s, err)
		}
	}
}

// With no_auth and no Firestore endpoint the report must not fall back to the
// real service.
func TestReportNoAuthNeverReachesRealFirestore(t *testing.T) {
	lc := &localcfg.Config{}
	lc.Endpoints.NoAuth = true
	lc.Budget = &localcfg.Budget{FirebaseProject: "aurora-fp"}
	if _, err := newReportFirestore(context.Background(), lc); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
}

func TestInitHelpMentionsFirestoreStep(t *testing.T) {
	out, _, err := execute(t, "setup", "--help")
	if err != nil {
		t.Fatal(err)
	}
	out = strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"Firestore", "us-east5", "permanent", "type the location"} {
		if !strings.Contains(out, want) {
			t.Errorf("fugaro setup --help lacks %q", want)
		}
	}
}

// The report's compute source line is about the runs the report shows: a
// VM-priced run outside --until is not a figure in this report, so nothing
// warns about the table it came from (design compute-backend.md §5.2, C17).
func TestDegradedReportComputeSourceFollowsTheRange(t *testing.T) {
	f := newCloudFixture(t) // no budget block: the run-record fallback
	old := budgetNow
	budgetNow = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { budgetNow = old })
	vm := runstore.NewCost(1.5, 0.25, runstore.BasisAPIList)
	vm.ComputeSource = "Compute Engine VM list price, e2-standard family, us-central1 (2026-10-15, UNVERIFIED); boot disk and Cloud NAT are not in this figure (design C17)"
	seedCostRun(t, f, "20261020-100000-aaaa", "a@x.io", vm)
	seedCostRun(t, f, "20261019-100000-bbbb", "b@x.io", runstore.NewCost(2, 0.1, runstore.BasisAPIList))

	// --until the day before the VM run: the report has no VM figure, so it
	// says nothing about the VM table.
	out, _, err := execute(t, "report", "--json", "--since", "2026-10-01", "--until", "2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	var o repOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	if len(o.Rows) != 1 || o.Rows[0].Key != "2026-10-19" {
		t.Fatalf("rows = %+v, want only 2026-10-19", o.Rows)
	}
	for _, w := range o.Warnings {
		if strings.Contains(w, "Compute Engine VM list price") {
			t.Errorf("a report without the VM run warned about its table: %q", w)
		}
	}

	// The whole range: the VM run is in the report, and the table is named.
	out, _, err = execute(t, "report", "--json", "--since", "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	o = repOut{}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(o.Warnings, "\n"), "compute_usd is an estimate priced from "+vm.ComputeSource) {
		t.Errorf("warnings = %q, want the VM table's source line", o.Warnings)
	}
}

// The history-backed report says what its COMPUTE column is, too. The
// history keeps the figure and not the price table behind it (design C17),
// so the note names the estimate and what a VM run's figure leaves out; a
// report whose days estimated no compute says nothing, exactly as before
// compute existed.
func TestReportHistoryNamesComputeAsAnEstimate(t *testing.T) {
	f := newReportFixture(t)
	f.doc("2026-10-18", repOpts{spent: 2_000_000, compute: 250_000, estimated: 1, final: true})
	f.doc("2026-10-19", repOpts{spent: 1_000_000, final: true})

	o := f.json(t, "--since", "2026-10-18", "--until", "2026-10-18")
	if !strings.Contains(strings.Join(o.Warnings, "\n"), reportComputeEstimateNote) {
		t.Errorf("a day with an estimated compute figure warned %q, want %q", o.Warnings, reportComputeEstimateNote)
	}
	o = f.json(t, "--since", "2026-10-19", "--until", "2026-10-19")
	for _, w := range o.Warnings {
		if strings.Contains(w, "COMPUTE $ is an estimate") {
			t.Errorf("a report with no estimated compute warned about one: %q", w)
		}
	}
}
