package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// driftRig is doctorPolicyRig with the database's clock pinned to the instant
// the rig's own clock is pinned to (pinCatalogClock): the check reads the
// window's day from the database (rtdb.Client.ServerNow), so a test that seeded
// its counters at budgetNow() and let the fake answer with the wall clock would
// be seeding one day and reading another.
func driftRig(t *testing.T) *gcpfake.RTDB {
	t.Helper()
	_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
	at := budgetNow()
	db.SetClock(func() time.Time { return at })
	return db
}

// driftSeed writes one model's day-counter leaves, as a run's gateway wrote
// them: micros is what the model has spent, and rep/tlo/thi are the provider's
// reported cost and the table's charge at the two ends of the tier that served
// the calls, over the calls that carried a report (docs/spend-accuracy/
// day-leaves.md).
func driftSeed(t *testing.T, db *gcpfake.RTDB, day int64, slug, model string, leaves map[string]any) {
	t.Helper()
	p := budget.PathByModel(day, slug, model)
	for leaf, v := range leaves {
		db.Set(p+"/"+leaf, v)
	}
}

// driftChecks runs doctor against the rig and returns its drift checks, so a
// test asserts what the alarm said and not the order it said it in. It is a
// slice rather than a map: the check is one per drifting model, all of them
// under the one ID.
func driftChecks(t *testing.T) []doctorCheck {
	t.Helper()
	out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	var checks []doctorCheck
	for _, c := range o.Checks {
		if c.ID == spendDriftCheckID {
			checks = append(checks, c)
		}
	}
	return checks
}

// TestDriftAlarmOutsideBandOnly is R6 in doctor (SA5.3): the provider's
// reported cost is read against the price table's band over today and
// yesterday, summed per model across every repository, and only a model with
// at least $1 of spend in that window is named — as a warning, never a
// failure, because nothing `fugaro update` runs puts a price table right. Each
// day here is inside the band on its own and only the two together leave it,
// which is the window F30 settles; the model beside it the table still agrees
// with is silent.
func TestDriftAlarmOutsideBandOnly(t *testing.T) {
	const model = "openai/gpt-5.1"
	db := driftRig(t)
	day := budget.Day(budgetNow())
	driftSeed(t, db, day, "gh-acme-app", model, map[string]any{
		"micros": 500000, "rep": 700000, "tlo": 250000, "thi": 300000})
	driftSeed(t, db, day-1, "gh-acme-lib", model, map[string]any{
		"micros": 600000, "rep": 400000, "tlo": 500000, "thi": 600000})
	driftSeed(t, db, day, "gh-acme-app", "openai/gpt-5.1-mini", map[string]any{
		"micros": 2000000, "rep": 1100000, "tlo": 1000000, "thi": 1200000})

	checks := driftChecks(t)
	if len(checks) != 1 {
		t.Fatalf("the alarm said %d things, want the one model whose report left the band: %+v", len(checks), checks)
	}
	c := checks[0]
	if c.ID != spendDriftCheckID || c.OK || c.Severity != "warning" {
		t.Fatalf("the drift check is %+v, want a warning that fails `--strict` alone", c)
	}
	// $1.10 reported against a band of $0.75-$0.90, over the two days and the
	// two repositories: the model and the factor it left the dearest end by are
	// what the line is for (R6).
	for _, want := range []string{model, "1.22x", "dearest end", "$1.10", "$0.75-$0.90"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the problem does not name %q: %q", want, c.Problem)
		}
	}
	if !strings.Contains(c.Fix, "model_prices") {
		t.Errorf("the fix does not name what sets a model's price: %q", c.Fix)
	}
	if c.Class != doctorClassActionable {
		t.Errorf("the drift check is classed %q, want the default view's own class", c.Class)
	}
	// And the one screen the owner reads carries both of them, which is why the
	// check has no short form in doctor_render.go's maps: a sentence keyed by
	// the ID alone cannot name a model or a factor.
	line, _, _ := execute(t, "doctor", "--dir", t.TempDir())
	head := "warning " + spendDriftCheckID + ": "
	if !strings.Contains(line, head) {
		t.Fatalf("the default view does not print the drift:\n%s", line)
	}
	shown := line[strings.Index(line, head):]
	if !strings.Contains(shown, model) || !strings.Contains(shown, "1.22x") {
		t.Errorf("the default view's drift line does not name the model and the factor: %q", shown)
	}
}

// TestDriftAlarmBelowOneDollarQuiet: under $1 of spend in the window the alarm
// stays quiet however far the report is from the band — below the floor a ratio
// says more about a rounding error than about the price table (D2649). The same
// leaves at the floor itself are a line, so the silence is the floor and not
// leaves nothing reads.
func TestDriftAlarmBelowOneDollarQuiet(t *testing.T) {
	db := driftRig(t)
	day, model := budget.Day(budgetNow()), "openai/gpt-5.1"
	leaves := map[string]any{"micros": 1000000, "rep": 5000000, "tlo": 900000, "thi": 1000000}
	driftSeed(t, db, day, "gh-acme-app", model, leaves)
	if checks := driftChecks(t); len(checks) != 1 {
		t.Fatalf("the alarm said %d things about $5.00 reported against a $1.00 band, want one: %+v", len(checks), checks)
	}
	driftSeed(t, db, day, "gh-acme-app", model, map[string]any{"micros": 999999})
	if checks := driftChecks(t); len(checks) != 0 {
		t.Fatalf("the alarm spoke under the floor: %+v", checks)
	}
}

// TestDriftAlarmOldDayQuiet: the counters are per UTC day, so the window is
// today and yesterday and nothing older (F30) — and a day whose counters carry
// no report is quiet too, which is what today's node is below.
func TestDriftAlarmOldDayQuiet(t *testing.T) {
	const model = "openai/gpt-5.1"
	db := driftRig(t)
	day := budget.Day(budgetNow())
	driftSeed(t, db, day, "gh-acme-app", model, map[string]any{"micros": 2000000})
	driftSeed(t, db, day-2, "gh-acme-app", model, map[string]any{
		"micros": 2000000, "rep": 700000, "tlo": 1000000, "thi": 1200000})
	if checks := driftChecks(t); len(checks) != 0 {
		t.Fatalf("the alarm read a day outside the window: %+v", checks)
	}
	// The same leaves one day nearer are yesterday, which the window carries:
	// the silence above is the window and not a check that never fires.
	driftSeed(t, db, day-1, "gh-acme-app", model, map[string]any{
		"micros": 2000000, "rep": 700000, "tlo": 1000000, "thi": 1200000})
	checks := driftChecks(t)
	if len(checks) != 1 {
		t.Fatalf("the alarm said %d things about yesterday's report, want one: %+v", len(checks), checks)
	}
	if !strings.Contains(checks[0].Problem, "cheapest end") || !strings.Contains(checks[0].Problem, "0.70x") {
		t.Errorf("the report below the band is not named as such: %q", checks[0].Problem)
	}
}

// TestDriftAlarmEmptyDayQuiet: a project that has spent nothing has no node
// under today's counters at all, and the alarm reads that absence as a day of
// no counters rather than as the unread note a refused read draws — the two
// outcomes look alike from outside and only one of them is honest (D2649 has
// nothing to compare over a day nobody wrote). The check does speak once the
// node is there, which is what makes the silence above a read of the absence
// and not a check that never ran.
func TestDriftAlarmEmptyDayQuiet(t *testing.T) {
	db := driftRig(t)
	if checks := driftChecks(t); len(checks) != 0 {
		t.Fatalf("the alarm spoke about a database with no counters: %+v", checks)
	}
	driftSeed(t, db, budget.Day(budgetNow()), "gh-acme-app", "openai/gpt-5.1", map[string]any{
		"micros": 2000000, "rep": 700000, "tlo": 1000000, "thi": 1000000})
	checks := driftChecks(t)
	if len(checks) != 1 {
		t.Fatalf("the alarm said %d things once today's counters were written, want one: %+v", len(checks), checks)
	}
	if c := checks[0]; c.OK || c.Severity != "warning" {
		t.Errorf("the report outside the band is %+v, want the warning", c)
	}
}

// TestDriftAlarmUnreadDayIsNotAPass: a day's counters the rules do not let
// doctor read are not a day without a drift. The line says so at an info
// severity, carries the unread marker — so no view files it as a measurement
// and the default view counts it, which is the ruling the class rules exist
// under (doctorClassOf, TestDoctorReadFailureIsCounted) — and names the path
// that could not be read.
func TestDriftAlarmUnreadDayIsNotAPass(t *testing.T) {
	db := driftRig(t)
	day := budget.Day(budgetNow())
	driftSeed(t, db, day, "gh-acme-app", "openai/gpt-5.1", map[string]any{
		"micros": 2000000, "rep": 700000, "tlo": 1000000, "thi": 1000000})
	db.DenyRead("spend/" + budget.DayKey(day) + "/repos")

	checks := driftChecks(t)
	if len(checks) != 1 {
		t.Fatalf("the alarm said %d things about a day it could not read, want the one note: %+v", len(checks), checks)
	}
	c := checks[0]
	if c.OK || c.Severity != "info" {
		t.Fatalf("the unread day is %+v, want an info note and not a warning about a comparison nobody made", c)
	}
	// The class is the unread marker's, which is unexported and so is read here
	// where it is decided: a failed read is a finding the default view counts,
	// never a measurement (doctorClassOf).
	if c.Class != doctorClassAudit {
		t.Errorf("the unread day is classed %q, want %q", c.Class, doctorClassAudit)
	}
	node := "spend/" + budget.DayKey(day) + "/repos"
	if !strings.Contains(c.Problem, node) {
		t.Errorf("the note does not name the node that could not be read: %q", c.Problem)
	}
	counted, _, _ := execute(t, "doctor", "--dir", t.TempDir())
	if strings.Contains(counted, spendDriftCheckID) {
		t.Errorf("the default view prints the unread note instead of counting it:\n%s", counted)
	}
	all, _, _ := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if !strings.Contains(all, node) {
		t.Errorf("--all does not print the note:\n%s", all)
	}
}

// TestDriftAlarmUnreadYesterdayIsNotAPass: yesterday is the window's other
// half, and the same ruling holds for it — a read of it that did not answer is
// unknown and not silence, so the note names the node nobody could read and
// never passes the half it could not see off as nothing to report. The counters
// under it are the ones that would have drifted, so the day the reader is told
// nothing about is the day there was something to say about.
func TestDriftAlarmUnreadYesterdayIsNotAPass(t *testing.T) {
	db := driftRig(t)
	day := budget.Day(budgetNow())
	node := "spend/" + budget.DayKey(day-1) + "/repos"
	driftSeed(t, db, day-1, "gh-acme-app", "openai/gpt-5.1", map[string]any{
		"micros": 2000000, "rep": 700000, "tlo": 1000000, "thi": 1000000})
	db.DenyRead(node)

	checks := driftChecks(t)
	if len(checks) != 1 {
		t.Fatalf("the alarm said %d things about yesterday, want the one note: %+v", len(checks), checks)
	}
	c := checks[0]
	if c.OK || c.Severity != "info" {
		t.Fatalf("the unread yesterday is %+v, want an info note and not a warning about a comparison nobody made", c)
	}
	if c.Class != doctorClassAudit {
		t.Errorf("the unread yesterday is classed %q, want %q", c.Class, doctorClassAudit)
	}
	if !strings.Contains(c.Problem, node) {
		t.Errorf("the note does not name the node that could not be read: %q", c.Problem)
	}
}

// TestDriftAlarmDayIsTheDatabases: the two days the window covers are the
// database's, not this host's (rtdb.Client.ServerNow, the clock `fugaro watch`
// reads) — otherwise a host clock off by a day would have doctor sum two days
// that are not today and yesterday while its own sentence says they are, and
// answer differently from the screen it is copied from. The database's day is a
// day ahead of the rig's clock here and the counters settle which two days were
// read: the leaves two days behind the database's today are outside the window,
// though the host clock would have called them yesterday, and the ones under
// the database's own today are the report the line is about.
func TestDriftAlarmDayIsTheDatabases(t *testing.T) {
	const model = "openai/gpt-5.1"
	db := driftRig(t)
	host := budget.Day(budgetNow())
	server := budgetNow().Add(26 * time.Hour)
	db.SetClock(func() time.Time { return server })
	driftSeed(t, db, host-1, "gh-acme-app", model, map[string]any{
		"micros": 2000000, "rep": 700000, "tlo": 1000000, "thi": 1000000})
	if checks := driftChecks(t); len(checks) != 0 {
		t.Fatalf("the alarm read a day outside the database's window: %+v", checks)
	}
	driftSeed(t, db, budget.Day(server), "gh-acme-app", model, map[string]any{
		"micros": 3000000, "rep": 1500000, "tlo": 1000000, "thi": 1000000})
	checks := driftChecks(t)
	if len(checks) != 1 {
		t.Fatalf("the alarm said %d things about the database's own today, want the one report the window holds: %+v", len(checks), checks)
	}
	if p := checks[0].Problem; !strings.Contains(p, "1.50x") || !strings.Contains(p, "dearest end") {
		t.Errorf("the check is not the report the database's today holds: %q", p)
	}
}

// TestDriftAlarmOfflineSkipped: `--offline` checks the update class and
// nothing else (D945, D952), and the drift alarm is not that class — a report
// outside the band is a fact about the price table, which no `fugaro update`
// puts right — so an offline run neither prints the check nor records a skip
// for it, exactly as it treats the budget mode and the build budget, its
// neighbours on the same database. It makes no request either.
func TestDriftAlarmOfflineSkipped(t *testing.T) {
	db := driftRig(t)
	driftSeed(t, db, budget.Day(budgetNow()), "gh-acme-app", "openai/gpt-5.1", map[string]any{
		"micros": 2000000, "rep": 700000, "tlo": 1000000, "thi": 1000000})
	before := len(db.Requests())
	out, _, _ := execute(t, "doctor", "--offline", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("doctor --offline --json is not JSON: %v\n%s", err, out)
	}
	for _, c := range o.Checks {
		if c.ID == spendDriftCheckID {
			t.Errorf("--offline ran the drift alarm: %+v", c)
		}
	}
	if after := len(db.Requests()); after != before {
		t.Fatalf("--offline made %d database requests", after-before)
	}
}
