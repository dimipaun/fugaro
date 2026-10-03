package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/report"
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
	if _, _, err := execute(t, "report", "--by", "decade"); err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("%v", err)
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
	for _, s := range []string{"day", "week", "month", "year", "repo", "model", "person"} {
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
	out, _, err := execute(t, "init", "--help")
	if err != nil {
		t.Fatal(err)
	}
	out = strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"Firestore", "us-east5", "permanent", "type the location"} {
		if !strings.Contains(out, want) {
			t.Errorf("init --help lacks %q", want)
		}
	}
}
