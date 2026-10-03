package budget

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

const day = int64(20000)

func baseInput() RollupInput {
	return RollupInput{
		Day: day,
		Repos: map[string]RepoDay{
			"a-1": {Counters: Counters{Spent: 700, Notional: 50, Calls: 7, ByModel: map[string]ModelUse{
				Key("claude-3.5"): {Micros: 700, In: 10, Out: 5, CR: 1, CW: 2}}}, Repo: "acme/a", CapDaily: mp(5000)},
			"b-1": {Counters: Counters{Spent: 300, Calls: 3}},
		},
		Shares: map[string]map[string]RunLedger{
			"a-1": {"r1": {Spent: 400, Notional: 50}, "r2": {Spent: 300}},
			"b-1": {"r3": {Spent: 300}},
		},
		Outcomes: map[string]map[string]Outcome{
			"a-1": {"r1": {Status: "succeeded", RequestedBy: "ann@x.io"}, "r2": {Status: "failed", RequestedBy: "bob@x.io"}},
			"b-1": {"r3": {Status: "halted", RequestedBy: "ann@x.io"}},
		},
		Runs: []RunFact{
			{Slug: "a-1", Run: "r1", StartDay: day, RequestedBy: "ann@x.io", ComputeUSD: 0.25, ComputeEstimated: true, Hours: 1.5},
			{Slug: "a-1", Run: "r2", StartDay: day, RequestedBy: "bob@x.io", Hours: 0.5},
			{Slug: "b-1", Run: "r3", StartDay: day, RequestedBy: "ann@x.io"},
		},
	}
}

func byslug(t *testing.T, rs []DayRecord, slug string) DayRecord {
	t.Helper()
	for _, r := range rs {
		if r.Slug == slug {
			return r
		}
	}
	t.Fatalf("no record for %s in %v", slug, rs)
	return DayRecord{}
}

func TestRollupMatchesCounters(t *testing.T) {
	in := baseInput()
	recs := Rollup(in)
	if len(recs) != 2 {
		t.Fatalf("%d records", len(recs))
	}
	var spent, notional Micros
	var calls int64
	for _, r := range recs {
		spent += r.SpentMicros
		notional += r.NotionalMicros
		calls += r.Calls
		var pm Micros
		for _, p := range r.ByPerson {
			pm += p.Micros
		}
		if pm != r.SpentMicros {
			t.Errorf("%s: byPerson sums %d, spent %d", r.Slug, pm, r.SpentMicros)
		}
	}
	if spent != 1000 || notional != 50 || calls != 10 {
		t.Fatalf("sums spent=%d notional=%d calls=%d", spent, notional, calls)
	}
	a := byslug(t, recs, "a-1")
	if a.Repo != "acme/a" || b2(a.CapDailyMicros) != 5000 || a.Date != DayDate(day) || a.Version != 1 || a.Final {
		t.Fatalf("a = %+v", a)
	}
	if a.ByPerson[Key("ann@x.io")].Micros != 400 || a.ByPerson[Key("bob@x.io")].Runs != 1 || a.Outcomes["succeeded"] != 1 || a.Outcomes["failed"] != 1 {
		t.Fatalf("a = %+v", a)
	}
	if a.ByModel["claude-3%2E5"].Micros != 700 || a.RunHours != 2 || a.Runs != 2 {
		t.Fatalf("a model/hours = %+v", a)
	}
	if byslug(t, recs, "b-1").Repo != "b-1" {
		t.Fatal("repo falls back to the slug")
	}
}

func b2(m *Micros) Micros {
	if m == nil {
		return -1
	}
	return *m
}

func TestRolloverIdempotent(t *testing.T) {
	if a, b := Rollup(baseInput()), Rollup(baseInput()); !reflect.DeepEqual(a, b) {
		t.Fatalf("not deterministic:\n%+v\n%+v", a, b)
	}
}

func TestMidnightRunCharged(t *testing.T) {
	// r1 starts on day, spends on day and day+1, finishes on day+1.
	d0 := RollupInput{
		Day:     day,
		Repos:   map[string]RepoDay{"a-1": {Counters: Counters{Spent: 100, Calls: 1}}},
		Shares:  map[string]map[string]RunLedger{"a-1": {"r1": {Spent: 100}}},
		NextDay: map[string]map[string]Outcome{"a-1": {"r1": {Status: "succeeded", RequestedBy: "ann@x.io"}}},
		Runs:    []RunFact{{Slug: "a-1", Run: "r1", StartDay: day, RequestedBy: "ann@x.io", ComputeUSD: 2, ComputeEstimated: true, Hours: 3}},
		Ledgers: map[string]map[string]LifetimeRun{"a-1": {"r1": {RunLedger: RunLedger{Overrun: 9}, LastShareDay: day + 1}}},
	}
	d1 := RollupInput{
		Day:      day + 1,
		Repos:    map[string]RepoDay{"a-1": {Counters: Counters{Spent: 40, Calls: 1}}},
		Shares:   map[string]map[string]RunLedger{"a-1": {"r1": {Spent: 40}}},
		Outcomes: map[string]map[string]Outcome{"a-1": {"r1": {Status: "succeeded", RequestedBy: "ann@x.io"}}},
		Runs:     nil, // the start day owns the fact
		Ledgers:  d0.Ledgers,
	}
	r0, r1 := Rollup(d0)[0], Rollup(d1)[0]
	if r0.SpentMicros != 100 || r1.SpentMicros != 40 {
		t.Fatalf("model dollars stay on the lease days: %d %d", r0.SpentMicros, r1.SpentMicros)
	}
	if r0.ComputeMicros != 2_000_000 || r0.RunHours != 3 || r0.Outcomes["succeeded"] != 1 || r0.Runs != 1 {
		t.Fatalf("start day: %+v", r0)
	}
	// Day 1 sees the same run only as a fact-less share: its outcome is in its own node.
	if r1.ComputeMicros != 0 || r1.RunHours != 0 {
		t.Fatalf("compute must not repeat: %+v", r1)
	}
	if r0.OverrunMicros != 0 || r1.OverrunMicros != 9 {
		t.Fatalf("overrun goes to the latest day: %d %d", r0.OverrunMicros, r1.OverrunMicros)
	}
	// With the fact known (StartDay = day) day 1 must not count the run or the outcome.
	d1.Runs = d0.Runs
	r1 = Rollup(d1)[0]
	if r1.Outcomes["succeeded"] != 0 || r1.Runs != 0 || r1.ComputeMicros != 0 {
		t.Fatalf("a run is counted on its start day only: %+v", r1)
	}
	if r1.SpentMicros != 40 {
		t.Fatalf("dollars still charged: %+v", r1)
	}
}

func TestOauthIsNotionalOnly(t *testing.T) {
	in := RollupInput{
		Day:    day,
		Repos:  map[string]RepoDay{"a-1": {Counters: Counters{Notional: 900, Calls: 4}}},
		Shares: map[string]map[string]RunLedger{"a-1": {"r1": {Notional: 900}}},
		Runs:   []RunFact{{Slug: "a-1", Run: "r1", StartDay: day, RequestedBy: "ann@x.io"}},
	}
	r := Rollup(in)[0]
	if r.SpentMicros != 0 || r.NotionalMicros != 900 || r.ByPerson[Key("ann@x.io")].Micros != 0 || r.ByPerson[Key("ann@x.io")].NotionalMicros != 900 {
		t.Fatalf("%+v", r)
	}
	if r.ComputeKnown() || r.ComputeMicros != 0 {
		t.Fatalf("compute was not estimated, so n/a: %+v", r)
	}
}

func TestComputeNotEstimatedIsNA(t *testing.T) {
	in := baseInput()
	in.Runs = in.Runs[1:] // r2 and r3 are not estimated
	r := Rollup(in)
	if byslug(t, r, "a-1").ComputeMicros != 0 || byslug(t, r, "b-1").ComputeKnown() {
		t.Fatal("not estimated must stay n/a")
	}
	in.Runs = baseInput().Runs
	if a := byslug(t, Rollup(in), "a-1"); !a.ComputeKnown() || a.ComputeMicros != 250_000 {
		t.Fatalf("%+v", a)
	}
}

func TestUnreconciledFromCrashed(t *testing.T) {
	in := baseInput()
	in.Ledgers = map[string]map[string]LifetimeRun{"a-1": {
		"r1": {RunLedger: RunLedger{Reserved: 1000, Released: 100, Spent: 400, Crashed: true}, LastShareDay: day},
		"r2": {RunLedger: RunLedger{Reserved: 1000, Spent: 300}, LastShareDay: day},                         // not crashed
		"r9": {RunLedger: RunLedger{Reserved: 1000, Crashed: true}, LastShareDay: day + 1},                  // later day
		"r8": {RunLedger: RunLedger{Reserved: 10, Spent: 50, Crashed: true, Overrun: 7}, LastShareDay: day}, // over-spent: unsettled negative
	}}
	a := byslug(t, Rollup(in), "a-1")
	if a.UnreconciledMicros != 500 || a.OverrunMicros != 7 {
		t.Fatalf("unreconciled=%d overrun=%d", a.UnreconciledMicros, a.OverrunMicros)
	}
}

func TestEmptyDayIsNoRecords(t *testing.T) {
	for name, in := range map[string]RollupInput{
		"zero":     {Day: day},
		"empty":    {Day: day, Repos: map[string]RepoDay{"a-1": {}}},
		"noshares": {Day: day, Shares: map[string]map[string]RunLedger{}},
	} {
		if r := Rollup(in); len(r) != 0 {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestMissingAndNegativeFields(t *testing.T) {
	in := RollupInput{
		Day:   day,
		Repos: map[string]RepoDay{"a-1": {Counters: Counters{Spent: -5, Notional: -1, Calls: -3, ByModel: map[string]ModelUse{"m": {Micros: -9, In: -1}}}}},
		// share and outcome without a fact or requester
		Shares:   map[string]map[string]RunLedger{"a-1": {"r1": {Spent: -2}}},
		Outcomes: map[string]map[string]Outcome{"a-1": {"r1": {}}},
	}
	r := Rollup(in)
	if len(r) != 1 {
		t.Fatalf("%+v", r)
	}
	if r[0].SpentMicros != 0 || r[0].Calls != 0 || r[0].Outcomes["other"] != 1 || r[0].ByPerson[UnknownPerson].Runs != 1 {
		t.Fatalf("%+v", r[0])
	}
}

func TestSaturation(t *testing.T) {
	in := RollupInput{
		Day:    day,
		Repos:  map[string]RepoDay{"a-1": {Counters: Counters{Spent: math.MaxInt64}}},
		Shares: map[string]map[string]RunLedger{"a-1": {"r1": {Spent: math.MaxInt64}, "r2": {Spent: math.MaxInt64}}},
		Runs: []RunFact{
			{Slug: "a-1", Run: "r1", StartDay: day, ComputeUSD: 1e300, ComputeEstimated: true, Hours: math.Inf(1)},
			{Slug: "a-1", Run: "r2", StartDay: day, ComputeUSD: 1e300, ComputeEstimated: true, Hours: math.NaN()},
			{Slug: "a-1", Run: "r3", StartDay: day, ComputeUSD: math.NaN(), ComputeEstimated: true},
			{Slug: "a-1", Run: "r4", StartDay: day, ComputeUSD: -1, ComputeEstimated: true},
		},
	}
	r := Rollup(in)[0]
	if r.ComputeMicros != math.MaxInt64 || r.ByPerson[UnknownPerson].Micros != math.MaxInt64 || r.ComputeEstimatedRuns != 2 {
		t.Fatalf("%+v", r)
	}
	if r.RunHours != 0 || math.IsNaN(r.RunHours) {
		t.Fatalf("hours %v", r.RunHours)
	}
}

func TestHostileKeysEscaped(t *testing.T) {
	evil := "a.b$c#d[e]f/g\x1b[31m\n%zz"
	in := RollupInput{
		Day: day,
		Repos: map[string]RepoDay{
			"a-1":   {Counters: Counters{Spent: 10, ByModel: map[string]ModelUse{Key("m.1"): {Micros: 10}, "%ZZ": {Micros: 1}, "": {Micros: 1}}}},
			"../x":  {Counters: Counters{Spent: 1}}, // not a document-ID-safe slug
			"a%2Fb": {Counters: Counters{Spent: 1}}, // unescapes to a/b
			"":      {Counters: Counters{Spent: 1}},
		},
		Shares:   map[string]map[string]RunLedger{"a-1": {"r1": {Spent: 10}}},
		Outcomes: map[string]map[string]Outcome{"a-1": {"r1": {Status: evil, RequestedBy: evil}}},
	}
	recs := Rollup(in)
	if len(recs) != 1 {
		t.Fatalf("unsafe slugs must be skipped: %+v", recs)
	}
	r := recs[0]
	for k := range r.ByPerson {
		if strings.ContainsAny(k, ".$#[]/\x1b\n") {
			t.Errorf("person key %q is not escaped", k)
		}
		if p, err := Unkey(k); err != nil || (k != UnknownPerson && p != evil) {
			t.Errorf("person key %q does not round-trip: %q %v", k, p, err)
		}
	}
	if r.Outcomes["other"] != 1 || len(r.Outcomes) != len(outcomeKeys) {
		t.Fatalf("hostile status must count as other: %v", r.Outcomes)
	}
	for k := range r.ByModel {
		if strings.ContainsAny(k, ".$#[]/") || k == "" {
			t.Errorf("model key %q", k)
		}
	}
	if r.ByModel["m%2E1"].Micros != 10 {
		t.Fatalf("%v", r.ByModel)
	}
	long := strings.Repeat("é", 500)
	if k := personKey(long); len(Key(long)) > 0 && len(k) > 3*maxPersonBytes {
		t.Fatalf("person key not clipped: %d", len(k))
	}
}

func TestDayRecordFieldRoundTrip(t *testing.T) {
	r := Rollup(baseInput())[0]
	r.Final = true
	r.ArchivedAt = time.Date(2026, 10, 3, 0, 30, 0, 0, time.UTC)
	r.WrittenAt = r.ArchivedAt.Add(time.Hour)
	f := r.ToFields()
	for _, k := range []string{"repo", "slug", "date", "spentMicros", "notionalMicros", "computeMicros", "unreconciledMicros", "overrunMicros", "runHours", "calls", "runs", "outcomes", "byModel", "byPerson", "capDailyMicros", "final", "version", "archivedAt", "writtenAt"} {
		if _, ok := f[k]; !ok {
			t.Errorf("missing field %s", k)
		}
	}
	back, err := FromFields(f)
	if err != nil || !reflect.DeepEqual(back, r) {
		t.Fatalf("err=%v\n%+v\n%+v", err, back, r)
	}
	// No cap: the field is absent and decodes to nil.
	r.CapDailyMicros = nil
	f = r.ToFields()
	if _, ok := f["capDailyMicros"]; ok {
		t.Fatal("nil cap must be omitted")
	}
	if back, _ = FromFields(f); back.CapDailyMicros != nil {
		t.Fatal("nil cap")
	}
}

func TestFromFieldsMalformed(t *testing.T) {
	good := func() map[string]any {
		r := Rollup(baseInput())[0]
		return r.ToFields()
	}
	for name, mut := range map[string]func(map[string]any){
		"version":     func(f map[string]any) { f["version"] = int64(2) },
		"noversion":   func(f map[string]any) { delete(f, "version") },
		"stringmicro": func(f map[string]any) { f["spentMicros"] = "12" },
		"mapnotmap":   func(f map[string]any) { f["byModel"] = "x" },
		"badmodel":    func(f map[string]any) { f["byModel"] = map[string]any{"m": int64(3)} },
		"badperson":   func(f map[string]any) { f["byPerson"] = map[string]any{"p": map[string]any{"runs": 1.5}} },
		"badtime":     func(f map[string]any) { f["writtenAt"] = "now" },
		"badfinal":    func(f map[string]any) { f["final"] = "yes" },
		"noslug":      func(f map[string]any) { delete(f, "slug") },
	} {
		f := good()
		mut(f)
		if _, err := FromFields(f); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// Missing optional fields decode as zero.
	f := map[string]any{"slug": "a-1", "date": "2026-10-03", "version": int64(1)}
	r, err := FromFields(f)
	if err != nil || r.SpentMicros != 0 || r.Final || r.CapDailyMicros != nil {
		t.Fatalf("%+v %v", r, err)
	}
}
