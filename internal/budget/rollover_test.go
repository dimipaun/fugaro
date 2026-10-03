package budget_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const rollSlug = "acme-app"

type fakeFacts struct {
	mu     sync.Mutex
	facts  map[int64][]budget.RunFact
	repos  map[string]string
	err    error
	calls  int
	onCall func(n int) error // called before each read; may fail it
}

func (f *fakeFacts) Facts(ctx context.Context, from, to int64) (map[int64][]budget.RunFact, map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.onCall != nil {
		if err := f.onCall(f.calls); err != nil {
			return nil, nil, err
		}
	}
	return f.facts, f.repos, f.err
}

type rollEnv struct {
	t     *testing.T
	db    *gcpfake.RTDB
	fs    *gcpfake.Firestore
	facts *fakeFacts
	now   time.Time
	r     *budget.Roller
	warns []string
	store budget.DocStore
}

var rollNow = time.Date(2026, 10, 20, 0, 30, 0, 0, time.UTC)

func newRollEnv(t *testing.T) *rollEnv {
	t.Helper()
	e := &rollEnv{t: t, db: gcpfake.NewRTDB(t), fs: gcpfake.NewFirestore(t), facts: &fakeFacts{facts: map[int64][]budget.RunFact{}}, now: rollNow}
	e.db.Set("fugaro/project", "aurora")
	e.db.Set("config/mode", "enforce")
	e.db.Set("config/caps/global/dailyMicros", 50_000_000)
	e.db.Set("config/kill/global/on", false)
	e.fs.Set("meta", "installation", map[string]any{"project": "aurora", "version": int64(1)})
	c, err := rtdb.New(e.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	fc, err := firestore.New(e.fs.URL, "aurora-fp", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	e.store = fc
	e.r = &budget.Roller{DB: c, FS: fc, Facts: e.facts, Project: "aurora", Now: func() time.Time { return e.now },
		Warn: func(s string) { e.warns = append(e.warns, s) }}
	return e
}

func (e *rollEnv) today() int64 { return budget.Day(e.now) }

// seedDay gives repo slug spend on day d: one run, one model, one outcome.
func (e *rollEnv) seedDay(d int64, slug string, spent int64, run string) {
	e.t.Helper()
	k := budget.DayKey(d)
	prev, _ := e.db.Value("spend/" + k + "/global").(map[string]any)
	gs, gc := int64(0), int64(0)
	if prev != nil {
		gs, _ = asInt(prev["spent"])
		gc, _ = asInt(prev["calls"])
	}
	e.db.Set("spend/"+k+"/global", map[string]any{"spent": gs + spent, "counted": gs + spent, "calls": gc + 1})
	e.db.Set("spend/"+k+"/repos/"+slug, map[string]any{"spent": spent, "counted": spent, "calls": 1,
		"byModel": map[string]any{"claude-x": map[string]any{"micros": spent, "in": 10, "out": 5, "cr": 0, "cw": 0}}})
	e.db.Set("spend/"+k+"/runs/"+slug+"/"+run, map[string]any{"reserved": spent, "released": 0, "spent": spent})
	e.db.Set("spend/"+k+"/meta", map[string]any{"date": budget.DayDate(d)})
	e.db.Set("runs/"+slug+"/"+run, map[string]any{"reserved": spent, "spent": spent})
	e.db.Set("outcomes/"+k+"/"+slug+"/"+run, map[string]any{"status": "succeeded", "requestedBy": "dimi@example.invalid"})
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

func (e *rollEnv) roll(opt budget.RolloverOptions) (budget.RolloverReport, error) {
	e.t.Helper()
	return e.r.Rollover(context.Background(), opt)
}

func (e *rollEnv) mustRoll(opt budget.RolloverOptions) budget.RolloverReport {
	e.t.Helper()
	rep, err := e.roll(opt)
	if err != nil {
		e.t.Fatalf("rollover: %v", err)
	}
	return rep
}

func (e *rollEnv) doc(d int64, slug string) map[string]any {
	e.t.Helper()
	m, _ := e.fs.Value("spendDaily", budget.DayDate(d)+"_"+slug)
	return m
}

func (e *rollEnv) patches() int {
	n := 0
	for _, r := range e.fs.Requests() {
		if r.Method == "PATCH" {
			n++
		}
	}
	return n
}

func (e *rollEnv) dayInDB(d int64) bool {
	k := budget.DayKey(d)
	return e.db.Value("spend/"+k) != nil || e.db.Value("outcomes/"+k) != nil
}

func isFinal(m map[string]any) bool { b, _ := m["final"].(bool); return b }

func TestLookbackSevenDays(t *testing.T) {
	e := newRollEnv(t)
	today := e.today()
	for d := today - 7; d <= today; d++ {
		e.seedDay(d, rollSlug, 1_000_000+d, fmt.Sprintf("20261001-0000%02d-aaaa", d-today+8))
	}
	rep := e.mustRoll(budget.RolloverOptions{})
	if len(rep.Days) != 7 {
		t.Fatalf("days = %d, want 7 (today-7..today-1)", len(rep.Days))
	}
	for d := today - 7; d < today; d++ {
		if e.doc(d, rollSlug) == nil {
			t.Errorf("day %s has no document", budget.DayDate(d))
		}
	}
	if e.doc(today, rollSlug) != nil {
		t.Error("today was written: it has not ended")
	}
	if e.db.Value("spend/"+budget.DayKey(today)) == nil {
		t.Error("today's counters were touched")
	}
}

func TestProvisionalThenFinal(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 1
	e.seedDay(d, rollSlug, 2_500_000, "20261019-100000-aaaa")
	e.mustRoll(budget.RolloverOptions{})
	m := e.doc(d, rollSlug)
	if m == nil || isFinal(m) || m["spentMicros"] != int64(2_500_000) {
		t.Fatalf("provisional doc = %v", m)
	}
	if _, ok := m["archivedAt"]; ok {
		t.Error("a provisional document has archivedAt")
	}
	e.now = e.now.Add(24 * time.Hour) // D+2, 00:30
	e.mustRoll(budget.RolloverOptions{})
	m = e.doc(d, rollSlug)
	if !isFinal(m) || m["archivedAt"] == nil {
		t.Fatalf("final doc = %v", m)
	}
	if !e.dayInDB(d) {
		t.Error("the day left the database on the day it became final")
	}
}

// A day is final exactly at 00:00 UTC of D+2, never a nanosecond before.
func TestFinalBoundaryAndClockEdges(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 1 // 2026-10-19
	e.seedDay(d, rollSlug, 1_000_000, "20261019-100000-aaaa")
	e.now = time.Date(2026, 10, 20, 23, 59, 59, 999_999_999, time.UTC)
	e.mustRoll(budget.RolloverOptions{})
	if isFinal(e.doc(d, rollSlug)) {
		t.Fatal("final before D+2")
	}
	e.now = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	e.mustRoll(budget.RolloverOptions{})
	if !isFinal(e.doc(d, rollSlug)) {
		t.Fatal("not final at 00:00 of D+2")
	}
	// A clock that jumps back never turns a final document provisional.
	e.now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	e.seedDay(d, rollSlug, 9_000_000, "20261019-110000-bbbb")
	if _, err := e.roll(budget.RolloverOptions{}); err != nil {
		t.Fatal(err)
	}
	m := e.doc(d, rollSlug)
	if !isFinal(m) || m["spentMicros"] != int64(1_000_000) {
		t.Fatalf("doc after clock rollback = %v", m)
	}
	// ...and a day that has not ended cannot be named.
	if _, err := e.roll(budget.RolloverOptions{Day: ptrTo(budget.Day(e.now))}); !errors.Is(err, budget.ErrRefused) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func ptrTo[T any](v T) *T { return &v }

func TestClockSkewRefused(t *testing.T) {
	e := newRollEnv(t)
	e.seedDay(e.today()-1, rollSlug, 1_000_000, "20261019-100000-aaaa")
	e.r.MaxSkew = 5 * time.Minute
	e.db.SetClock(func() time.Time { return e.now.Add(-time.Hour) }) // the database's clock
	if _, err := e.roll(budget.RolloverOptions{}); !errors.Is(err, budget.ErrRefused) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if len(e.fs.IDs("spendDaily")) != 0 {
		t.Fatalf("documents written under a skewed clock: %v", e.fs.IDs("spendDaily"))
	}
}

func TestRerunNoOp(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 2
	e.seedDay(d, rollSlug, 1_000_000, "20261018-100000-aaaa")
	e.mustRoll(budget.RolloverOptions{})
	before := e.doc(d, rollSlug)
	n := e.patches()
	e.now = e.now.Add(time.Hour)
	rep := e.mustRoll(budget.RolloverOptions{})
	if e.patches() != n || !reflect.DeepEqual(e.doc(d, rollSlug), before) {
		t.Fatalf("a rerun wrote: patches %d -> %d", n, e.patches())
	}
	if rep.Days[0].Written != 0 {
		t.Fatalf("report = %+v", rep.Days[0])
	}
}

func TestFinalDocNeverRewritten(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 3
	e.seedDay(d, rollSlug, 1_000_000, "20261017-100000-aaaa")
	kept := map[string]any{"repo": "x/y", "slug": rollSlug, "date": budget.DayDate(d), "version": int64(1), "final": true,
		"spentMicros": int64(7), "archivedAt": rollNow.Add(-24 * time.Hour)}
	e.fs.Set("spendDaily", budget.DayDate(d)+"_"+rollSlug, kept)
	if _, err := e.roll(budget.RolloverOptions{}); err != nil {
		t.Fatal(err)
	}
	got := e.doc(d, rollSlug)
	if got["spentMicros"] != int64(7) || e.patches() != 0 {
		t.Fatalf("a final document was rewritten: %v (patches %d)", got, e.patches())
	}
}

func TestBackfillForceOnlyWithDay(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 3
	e.seedDay(d, rollSlug, 1_000_000, "20261017-100000-aaaa")
	archived := rollNow.Add(-24 * time.Hour).Truncate(time.Microsecond)
	e.fs.Set("spendDaily", budget.DayDate(d)+"_"+rollSlug, map[string]any{"repo": "x/y", "slug": rollSlug, "date": budget.DayDate(d),
		"version": int64(1), "final": true, "spentMicros": int64(7), "archivedAt": archived})
	if _, err := e.roll(budget.RolloverOptions{Force: true}); !errors.Is(err, budget.ErrRefused) {
		t.Fatalf("--force without --day: err = %v, want a refusal", err)
	}
	if _, err := e.roll(budget.RolloverOptions{Day: &d}); err != nil {
		t.Fatal(err)
	}
	if e.doc(d, rollSlug)["spentMicros"] != int64(7) {
		t.Fatal("--day alone rewrote a final document")
	}
	e.mustRoll(budget.RolloverOptions{Day: &d, Force: true})
	got := e.doc(d, rollSlug)
	if got["spentMicros"] != int64(1_000_000) || !isFinal(got) {
		t.Fatalf("forced doc = %v", got)
	}
	if at, _ := got["archivedAt"].(time.Time); !at.Equal(archived) {
		t.Fatalf("archivedAt = %v, want the original %v", got["archivedAt"], archived)
	}
	// Only the named day is touched.
	other := e.today() - 4
	e.seedDay(other, rollSlug, 5, "20261016-100000-aaaa")
	e.mustRoll(budget.RolloverOptions{Day: &d, Force: true})
	if e.doc(other, rollSlug) != nil {
		t.Fatal("--day touched another day")
	}
}

func TestLateWriteIntoYesterday(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 1
	e.seedDay(d, rollSlug, 1_000_000, "20261019-230000-aaaa")
	e.mustRoll(budget.RolloverOptions{})
	// A run that leased on D writes again after midnight (the rules allow it).
	e.db.Set("spend/"+budget.DayKey(d)+"/global", map[string]any{"spent": 1_800_000, "counted": 1_800_000, "calls": 2})
	e.db.Set("spend/"+budget.DayKey(d)+"/repos/"+rollSlug, map[string]any{"spent": 1_800_000, "counted": 1_800_000, "calls": 2,
		"byModel": map[string]any{"claude-x": map[string]any{"micros": 1_800_000, "in": 20, "out": 10}}})
	e.now = e.now.Add(time.Hour)
	e.mustRoll(budget.RolloverOptions{})
	if m := e.doc(d, rollSlug); m["spentMicros"] != int64(1_800_000) || isFinal(m) {
		t.Fatalf("provisional not refreshed: %v", m)
	}
	e.now = e.now.Add(24 * time.Hour)
	e.mustRoll(budget.RolloverOptions{})
	if m := e.doc(d, rollSlug); m["spentMicros"] != int64(1_800_000) || !isFinal(m) {
		t.Fatalf("final = %v", m)
	}
}

func TestMidnightRunComputeOnStartDay(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 2
	run := "20261018-235950-aaaa"
	e.seedDay(d, rollSlug, 3_000_000, run)
	// The run started on d, finished after midnight: its outcome is written on d+1.
	e.db.Set("outcomes/"+budget.DayKey(d), nil)
	e.db.Set("outcomes/"+budget.DayKey(d+1)+"/"+rollSlug+"/"+run, map[string]any{"status": "succeeded", "requestedBy": "dimi@example.invalid"})
	e.facts.facts[d] = []budget.RunFact{{Slug: rollSlug, Run: run, StartDay: d, ComputeUSD: 0.5, ComputeEstimated: true, Hours: 0.5}}
	e.mustRoll(budget.RolloverOptions{})
	m := e.doc(d, rollSlug)
	if m["computeMicros"] != int64(500_000) || m["runs"] != int64(1) {
		t.Fatalf("day d doc = %v", m)
	}
	if outs, _ := m["outcomes"].(map[string]any); outs["succeeded"] != int64(1) {
		t.Fatalf("outcomes = %v", m["outcomes"])
	}
	if e.doc(d+1, rollSlug) != nil && e.doc(d+1, rollSlug)["runs"] != int64(0) {
		t.Fatalf("the run was also counted on d+1: %v", e.doc(d+1, rollSlug))
	}
}

func TestPruneKeepsFreshDays(t *testing.T) {
	e := newRollEnv(t)
	today := e.today()
	e.seedDay(today-8, rollSlug, 1_000_000, "20261012-100000-aaaa")
	e.seedDay(today-9, rollSlug, 2_000_000, "20261011-100000-aaaa")
	rep := e.mustRoll(budget.RolloverOptions{})
	if e.dayInDB(today-9) || !e.dayInDB(today-8) {
		t.Fatalf("day -9 in db: %v, day -8 in db: %v (rep %+v)", e.dayInDB(today-9), e.dayInDB(today-8), rep.Days)
	}
	if e.doc(today-9, rollSlug) == nil || !isFinal(e.doc(today-9, rollSlug)) || e.doc(today-8, rollSlug) == nil {
		t.Fatal("documents missing")
	}
}

func TestPruneNeedsFinalDocAndReadBack(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 12
	e.seedDay(d, rollSlug, 1_000_000, "20261008-100000-aaaa")
	before := e.db.Value("")
	// Firestore refuses the write: nothing may be deleted.
	e.fs.DenyNext(5)
	if _, err := e.roll(budget.RolloverOptions{}); err == nil || budget.OnlyRefusals(err) {
		t.Fatalf("err = %v, want a backend failure", err)
	}
	if !reflect.DeepEqual(e.db.Value(""), before) {
		t.Fatal("the database changed although the write failed")
	}
	e.fs.DenyNext(0)
	e.mustRoll(budget.RolloverOptions{})
	if e.dayInDB(d) || !isFinal(e.doc(d, rollSlug)) {
		t.Fatal("not archived and pruned after the write succeeded")
	}
}

func TestFailedWriteKeepsDayAndFails(t *testing.T) { TestPruneNeedsFinalDocAndReadBack(t) }

func TestReadBackMismatchKeepsDay(t *testing.T) {
	for name, mut := range map[string]func(m map[string]any){
		"spent":    func(m map[string]any) { m["spentMicros"] = int64(1) },
		"notional": func(m map[string]any) { m["notionalMicros"] = int64(1) },
		"calls":    func(m map[string]any) { m["calls"] = int64(9) },
		"runs":     func(m map[string]any) { m["runs"] = int64(9) },
		"model":    func(m map[string]any) { m["byModel"] = map[string]any{} },
		"notfinal": func(m map[string]any) { m["final"] = false },
	} {
		t.Run(name, func(t *testing.T) {
			e := newRollEnv(t)
			d := e.today() - 12
			e.seedDay(d, rollSlug, 1_000_000, "20261008-100000-aaaa")
			e.now = e.now.Add(-4 * 24 * time.Hour) // the day is final but too young to prune
			e.mustRoll(budget.RolloverOptions{})
			e.now = rollNow
			// The stored document no longer equals the database's figures.
			m := e.doc(d, rollSlug)
			mut(m)
			e.fs.Set("spendDaily", budget.DayDate(d)+"_"+rollSlug, m)
			before := e.db.Value("")
			_, err := e.roll(budget.RolloverOptions{})
			if name == "notfinal" {
				// A provisional document is made final by the pass itself: fine.
				if err != nil || !isFinal(e.doc(d, rollSlug)) || e.dayInDB(d) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errors.Is(err, budget.ErrRefused) || !budget.OnlyRefusals(err) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if !reflect.DeepEqual(e.db.Value(""), before) {
				t.Fatal("the database changed")
			}
		})
	}
}

func TestPruneOnlyOwnDatabase(t *testing.T) {
	t.Run("foreign rtdb mark", func(t *testing.T) {
		e := newRollEnv(t)
		e.seedDay(e.today()-12, rollSlug, 1_000_000, "20261008-100000-aaaa")
		for _, mark := range []any{"birch", nil, 7} {
			e.db.Set("fugaro/project", mark)
			before := e.db.Value("")
			if _, err := e.roll(budget.RolloverOptions{}); !errors.Is(err, budget.ErrRefused) {
				t.Fatalf("mark %v: err = %v", mark, err)
			}
			if !reflect.DeepEqual(e.db.Value(""), before) || len(e.fs.IDs("spendDaily")) != 0 {
				t.Fatal("something was written")
			}
		}
	})
	t.Run("foreign firestore mark", func(t *testing.T) {
		e := newRollEnv(t)
		e.seedDay(e.today()-12, rollSlug, 1_000_000, "20261008-100000-aaaa")
		e.fs.Set("meta", "installation", map[string]any{"project": "birch", "version": int64(1)})
		before := e.db.Value("")
		if _, err := e.roll(budget.RolloverOptions{}); !errors.Is(err, budget.ErrRefused) {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(e.db.Value(""), before) || len(e.fs.IDs("spendDaily")) != 0 {
			t.Fatal("something was written")
		}
	})
	t.Run("missing firestore mark", func(t *testing.T) {
		e := newRollEnv(t)
		fc, _ := firestore.New(gcpfake.NewFirestore(t).URL, "aurora-fp", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"}))
		e.r.FS = fc // a database with no meta/installation document
		e.seedDay(e.today()-12, rollSlug, 1, "20261008-100000-aaaa")
		before := e.db.Value("")
		if _, err := e.roll(budget.RolloverOptions{}); !errors.Is(err, budget.ErrRefused) {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(e.db.Value(""), before) {
			t.Fatal("the database changed")
		}
	})
	t.Run("no firestore database", func(t *testing.T) {
		e := newRollEnv(t)
		e.fs.RemoveDatabase()
		e.seedDay(e.today()-12, rollSlug, 1_000_000, "20261008-100000-aaaa")
		before := e.db.Value("")
		rep, err := e.roll(budget.RolloverOptions{})
		if err != nil || !rep.NoFirestore || !strings.Contains(rep.Summary(), "no Firestore database") {
			t.Fatalf("rep = %+v, err = %v", rep, err)
		}
		if !reflect.DeepEqual(e.db.Value(""), before) {
			t.Fatal("the database changed")
		}
		if e.facts.calls != 0 {
			t.Fatal("the bucket was read")
		}
	})
}

func TestPruneDeletesOnlyTheDay(t *testing.T) {
	e := newRollEnv(t)
	today := e.today()
	old := today - 12
	e.seedDay(old, rollSlug, 1_000_000, "20261008-100000-aaaa")
	// A run whose share continues on a later (young) day keeps its ledger.
	e.seedDay(today-3, rollSlug, 2_000_000, "20261008-100000-aaaa")
	e.seedDay(today-1, rollSlug, 4_000_000, "20261019-100000-cccc")
	e.db.Set("agents/"+rollSlug+"/20261019-100000-cccc", map[string]any{"repo": "acme/app", "requestedBy": "dimi@example.invalid"})
	e.db.Set("config/caps/repos/"+rollSlug, map[string]any{"dailyMicros": 9_000_000, "repo": "acme/app"})
	e.db.Set("config/kill/repos/"+rollSlug, map[string]any{"on": true})
	e.db.Set("spend/"+budget.DayKey(today), map[string]any{"global": map[string]any{"spent": 1}})
	before := map[string]any{}
	for _, p := range []string{"config", "agents", "fugaro", "spend/" + budget.DayKey(today), "spend/" + budget.DayKey(today-3),
		"spend/" + budget.DayKey(today-1), "outcomes/" + budget.DayKey(today-3), "runs/" + rollSlug + "/20261019-100000-cccc"} {
		before[p] = e.db.Value(p)
	}
	e.mustRoll(budget.RolloverOptions{})
	if e.dayInDB(old) {
		t.Fatal("the old day is still there")
	}
	if e.db.Value("runs/"+rollSlug+"/20261008-100000-aaaa") == nil {
		t.Fatal("a ledger whose last share is on a later day was deleted")
	}
	for p, v := range before {
		if !reflect.DeepEqual(e.db.Value(p), v) {
			t.Errorf("%s changed", p)
		}
	}
	// The ledger of a run whose last share was the pruned day goes with it.
	e.seedDay(old-1, rollSlug, 5, "20261007-100000-dddd")
	e.mustRoll(budget.RolloverOptions{})
	if e.db.Value("runs/"+rollSlug+"/20261007-100000-dddd") != nil {
		t.Fatal("the ledger of a pruned day's last run was kept")
	}
	if e.doc(old, rollSlug)["slug"] != rollSlug {
		t.Fatal("document missing")
	}
	// The cap in force was recorded.
	if e.doc(today-1, rollSlug)["capDailyMicros"] != int64(9_000_000) || e.doc(today-1, rollSlug)["repo"] != "acme/app" {
		t.Fatalf("doc = %v", e.doc(today-1, rollSlug))
	}
}

func TestPruneWaitsForTheDayBefore(t *testing.T) {
	e := newRollEnv(t)
	a, b := e.today()-13, e.today()-12
	e.seedDay(a, rollSlug, 1_000_000, "20261007-100000-aaaa")
	e.seedDay(b, rollSlug, 2_000_000, "20261008-100000-bbbb")
	// day a's document is wrong, so a stays; b must stay too (its outcomes feed a).
	e.fs.Set("spendDaily", budget.DayDate(a)+"_"+rollSlug, map[string]any{"repo": "x", "slug": rollSlug, "date": budget.DayDate(a),
		"version": int64(1), "final": true, "spentMicros": int64(3)})
	_, err := e.roll(budget.RolloverOptions{})
	if !errors.Is(err, budget.ErrRefused) {
		t.Fatalf("err = %v", err)
	}
	if !e.dayInDB(a) || !e.dayInDB(b) {
		t.Fatal("a day was pruned behind a day that could not be")
	}
}

func TestGlobalCounterMustBeExplained(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 12
	e.seedDay(d, rollSlug, 1_000_000, "20261008-100000-aaaa")
	// Spend the repository records do not account for (an unusable slug).
	e.db.Set("spend/"+budget.DayKey(d)+"/global", map[string]any{"spent": 3_000_000, "counted": 3_000_000, "calls": 2})
	e.db.Set("spend/"+budget.DayKey(d)+"/repos/Bad.Slug", map[string]any{"spent": 2_000_000, "calls": 1})
	if _, err := e.roll(budget.RolloverOptions{}); !errors.Is(err, budget.ErrRefused) {
		t.Fatalf("err = %v", err)
	}
	if !e.dayInDB(d) || e.db.Value("spend/"+budget.DayKey(d)+"/repos/Bad.Slug") == nil {
		t.Fatal("unarchived spend was deleted")
	}
}

func TestPruneRefusesWhenNodesChange(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 12
	e.seedDay(d, rollSlug, 1_000_000, "20261008-100000-aaaa")
	hook := &hookStore{DocStore: e.store}
	// A write lands in the day between the documents and the delete.
	hook.afterGetCalls = 99
	hook.onGet = func(n int) {
		if n == 3 {
			e.db.Set("spend/"+budget.DayKey(d)+"/repos/"+rollSlug+"/spent", 5_000_000)
		}
	}
	e.r.FS = hook
	_, err := e.roll(budget.RolloverOptions{})
	if !errors.Is(err, budget.ErrRefused) || !e.dayInDB(d) {
		t.Fatalf("err = %v, day in db %v", err, e.dayInDB(d))
	}
}

type hookStore struct {
	budget.DocStore
	mu            sync.Mutex
	gets          int
	afterGetCalls int
	onGet         func(n int)
	beforePatch   func(n int)
	patches       int
}

func (h *hookStore) Get(ctx context.Context, coll, id string) (*firestore.Doc, error) {
	h.mu.Lock()
	h.gets++
	n := h.gets
	h.mu.Unlock()
	d, err := h.DocStore.Get(ctx, coll, id)
	if h.onGet != nil {
		h.onGet(n)
	}
	return d, err
}

func (h *hookStore) Patch(ctx context.Context, coll, id string, f map[string]any, o ...firestore.PatchOption) (*firestore.Doc, error) {
	h.mu.Lock()
	h.patches++
	n := h.patches
	h.mu.Unlock()
	if h.beforePatch != nil {
		h.beforePatch(n)
	}
	return h.DocStore.Patch(ctx, coll, id, f, o...)
}

// A pass that read a provisional document must not overwrite the final one
// another pass wrote before its own write.
func TestProvisionalNeverOverwritesFinal(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 1
	e.seedDay(d, rollSlug, 1_000_000, "20261019-100000-aaaa")
	e.mustRoll(budget.RolloverOptions{}) // provisional exists
	e.db.Set("spend/"+budget.DayKey(d)+"/repos/"+rollSlug+"/spent", 1_200_000)
	e.db.Set("spend/"+budget.DayKey(d)+"/global/spent", 1_200_000)
	hook := &hookStore{DocStore: e.store}
	id := budget.DayDate(d) + "_" + rollSlug
	hook.beforePatch = func(n int) {
		if n == 1 {
			e.fs.Set("spendDaily", id, map[string]any{"repo": "r", "slug": rollSlug, "date": budget.DayDate(d), "version": int64(1),
				"final": true, "spentMicros": int64(1_100_000), "archivedAt": time.Now()})
		}
	}
	e.r.FS = hook
	e.mustRoll(budget.RolloverOptions{})
	m := e.doc(d, rollSlug)
	if !isFinal(m) || m["spentMicros"] != int64(1_100_000) {
		t.Fatalf("a provisional write overwrote the final document: %v", m)
	}
}

func TestPartialFirestoreFailure(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 12
	e.seedDay(d, "acme-app", 1_000_000, "20261008-100000-aaaa")
	e.seedDay(d, "acme-web", 2_000_000, "20261008-100000-bbbb")
	// Repair the global counter the helper overwrote per call.
	e.db.Set("spend/"+budget.DayKey(d)+"/global", map[string]any{"spent": 3_000_000, "counted": 3_000_000, "calls": 2})
	before := e.db.Value("")
	hook := &hookStore{DocStore: e.store}
	hook.beforePatch = func(n int) {
		if n == 2 {
			e.fs.DenyNext(1)
		}
	}
	e.r.FS = hook
	rep, err := e.roll(budget.RolloverOptions{})
	if err == nil || budget.OnlyRefusals(err) {
		t.Fatalf("err = %v, want a backend failure", err)
	}
	if len(e.fs.IDs("spendDaily")) != 1 || rep.Days[0].Pruned || !reflect.DeepEqual(e.db.Value(""), before) {
		t.Fatalf("docs %v, pruned %v: a partial failure must keep the day", e.fs.IDs("spendDaily"), rep.Days[0].Pruned)
	}
	e.mustRoll(budget.RolloverOptions{})
	if len(e.fs.IDs("spendDaily")) != 2 || e.dayInDB(d) {
		t.Fatal("the retry did not finish the day")
	}
}

func TestBucketFailureWritesNothing(t *testing.T) {
	e := newRollEnv(t)
	e.seedDay(e.today()-12, rollSlug, 1_000_000, "20261008-100000-aaaa")
	e.facts.err = errors.New("bucket down")
	before := e.db.Value("")
	_, err := e.roll(budget.RolloverOptions{})
	if err == nil || budget.OnlyRefusals(err) || len(e.fs.IDs("spendDaily")) != 0 || !reflect.DeepEqual(e.db.Value(""), before) {
		t.Fatalf("err = %v", err)
	}
}

// Two executions of the job overlapping converge on the same state.
func TestConcurrentRollovers(t *testing.T) {
	e := newRollEnv(t)
	today := e.today()
	for d := today - 14; d < today; d++ {
		e.seedDay(d, rollSlug, 1_000_000+d, fmt.Sprintf("20260901-%06d-aaaa", d-today+20))
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.r.Rollover(context.Background(), budget.RolloverOptions{})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !budget.OnlyRefusals(err) {
			t.Fatalf("a concurrent pass failed: %v", err)
		}
	}
	e.mustRoll(budget.RolloverOptions{})
	for d := today - 14; d < today; d++ {
		m := e.doc(d, rollSlug)
		if m == nil || m["spentMicros"] != 1_000_000+d {
			t.Fatalf("day %s: %v", budget.DayDate(d), m)
		}
		if isFinal(m) != (d <= today-2) {
			t.Fatalf("day %s final = %v", budget.DayDate(d), isFinal(m))
		}
		if e.dayInDB(d) != (d >= today-8) {
			t.Fatalf("day %s in db = %v", budget.DayDate(d), e.dayInDB(d))
		}
	}
}

func TestOlderStuckDaysCatchUp(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 30 // the job was down for a month
	e.seedDay(d, rollSlug, 1_000_000, "20260920-100000-aaaa")
	e.mustRoll(budget.RolloverOptions{})
	if !isFinal(e.doc(d, rollSlug)) || e.dayInDB(d) {
		t.Fatal("an old day was neither archived nor pruned")
	}
}

func TestHostileNodeIsRefusedNotPruned(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 12
	e.seedDay(d, rollSlug, 1_000_000, "20261008-100000-aaaa")
	e.db.Set("spend/"+budget.DayKey(d)+"/repos/"+rollSlug+"/spent", "lots")
	_, err := e.roll(budget.RolloverOptions{})
	if !errors.Is(err, budget.ErrRefused) || !e.dayInDB(d) || len(e.fs.IDs("spendDaily")) != 0 {
		t.Fatalf("err = %v", err)
	}
}

// A late write after the final document was written (the review's loss
// window): the prune compares the whole freshly derived record, so it refuses
// and the document is left alone.
func TestLateChangeAfterFinalBlocksPrune(t *testing.T) {
	for name, late := range map[string]func(e *rollEnv, d int64){
		"outcome": func(e *rollEnv, d int64) {
			e.db.Set("outcomes/"+budget.DayKey(d)+"/"+rollSlug+"/20261008-120000-zzzz", map[string]any{"status": "infra_error", "requestedBy": "x@example.invalid"})
		},
		"outcome on the next day for a run started on d": func(e *rollEnv, d int64) {
			e.db.Set("outcomes/"+budget.DayKey(d+1)+"/"+rollSlug+"/20261008-235900-yyyy", map[string]any{"status": "failed", "requestedBy": "x@example.invalid"})
			e.facts.facts[d] = append(e.facts.facts[d], budget.RunFact{Slug: rollSlug, Run: "20261008-235900-yyyy", StartDay: d})
		},
		"crashed ledger": func(e *rollEnv, d int64) {
			e.db.Set("runs/"+rollSlug+"/20261008-100000-aaaa", map[string]any{"reserved": 1_000_000, "spent": 400_000, "crashed": true})
		},
		"late share": func(e *rollEnv, d int64) {
			e.db.Set("spend/"+budget.DayKey(d)+"/runs/"+rollSlug+"/20261008-100000-aaaa/notional", 77)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newRollEnv(t)
			d := e.today() - 12
			e.seedDay(d, rollSlug, 1_000_000, "20261008-100000-aaaa")
			e.now = e.now.Add(-4 * 24 * time.Hour) // final, too young to prune
			e.mustRoll(budget.RolloverOptions{})
			e.now = rollNow
			stored := e.doc(d, rollSlug)
			late(e, d)
			before := e.db.Value("")
			_, err := e.roll(budget.RolloverOptions{})
			if !errors.Is(err, budget.ErrRefused) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if !reflect.DeepEqual(e.db.Value(""), before) || !reflect.DeepEqual(e.doc(d, rollSlug), stored) {
				t.Fatal("database or document changed")
			}
			// A person reconciles with --day --force, and then the day goes.
			e.mustRoll(budget.RolloverOptions{Day: &d, Force: true})
			if e.dayInDB(d) {
				t.Fatal("not pruned after the forced rewrite")
			}
		})
	}
}

func TestPruneKeepsRegisteredLedger(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 12
	run := "20261008-100000-aaaa"
	e.seedDay(d, rollSlug, 1_000_000, run)
	e.db.Set("agents/"+rollSlug+"/"+run, map[string]any{"repo": "acme/app", "requestedBy": "dimi@example.invalid"})
	e.mustRoll(budget.RolloverOptions{})
	if e.db.Value("spend/"+budget.DayKey(d)) != nil {
		t.Fatal("day not pruned")
	}
	if e.db.Value("runs/"+rollSlug+"/"+run) == nil || e.db.Value("agents/"+rollSlug+"/"+run) == nil {
		t.Fatal("a registered run's ledger was removed")
	}
}

func TestSkewNeedsServerTimeAndUsesTheEarlierClock(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 1
	e.seedDay(d, rollSlug, 1_000_000, "20261019-100000-aaaa")
	e.r.MaxSkew = 10 * time.Minute
	e.r.ServerNow = func() (time.Time, bool) { return time.Time{}, false }
	before := e.db.Value("")
	if _, err := e.roll(budget.RolloverOptions{}); !errors.Is(err, budget.ErrRefused) || len(e.fs.IDs("spendDaily")) != 0 || !reflect.DeepEqual(e.db.Value(""), before) {
		t.Fatalf("err = %v: a job that cannot see the database's clock must refuse", err)
	}
	// The job's clock is 00:01 on D+2 but the database says 23:58: not final.
	e.now = time.Date(2026, 10, 21, 0, 1, 0, 0, time.UTC)
	e.r.ServerNow = func() (time.Time, bool) { return e.now.Add(-3 * time.Minute), true }
	e.mustRoll(budget.RolloverOptions{})
	if isFinal(e.doc(d, rollSlug)) {
		t.Fatal("finalized by a clock that runs ahead of the database's")
	}
	// Agreeing clocks past midnight finalize.
	e.r.ServerNow = func() (time.Time, bool) { return e.now, true }
	e.mustRoll(budget.RolloverOptions{})
	if !isFinal(e.doc(d, rollSlug)) {
		t.Fatal("not final once both clocks are past 00:00 of D+2")
	}
}

// The bucket is slow: the pass has finished the days before the one it was cut
// off at (each day's facts are read just before the day is written).
func TestCutOffPassKeepsEarlierDays(t *testing.T) {
	e := newRollEnv(t)
	today := e.today()
	for d := today - 12; d < today-8; d++ {
		e.seedDay(d, rollSlug, 1_000_000+d, fmt.Sprintf("20261001-%06d-aaaa", d-today+20))
	}
	wall := time.Now()
	e.r.Wall = func() time.Time { return wall }
	e.r.StartLimit = wall.Add(25 * time.Minute)
	e.facts.onCall = func(n int) error { wall = wall.Add(10 * time.Minute); return nil } // each day takes 10 minutes
	rep, err := e.roll(budget.RolloverOptions{})
	if err != nil || !rep.More || len(rep.Days) != 3 {
		t.Fatalf("rep = %+v, err = %v: want 3 days then more remain", rep.Days, err)
	}
	if !strings.Contains(rep.Summary(), "more days remain") {
		t.Fatal(rep.Summary())
	}
	for d := today - 12; d < today-9; d++ {
		if !isFinal(e.doc(d, rollSlug)) || e.dayInDB(d) {
			t.Fatalf("day %s was not finished", budget.DayDate(d))
		}
	}
	if e.doc(today-9, rollSlug) != nil {
		t.Fatal("a day was started after the limit")
	}
	// The next pass continues.
	e.facts.onCall = nil
	e.r.StartLimit = time.Time{}
	e.mustRoll(budget.RolloverOptions{})
	if !isFinal(e.doc(today-9, rollSlug)) || e.dayInDB(today-9) {
		t.Fatal("the next pass did not continue")
	}
}

// A bucket that fails on the third day (killed or down) leaves the first two written.
func TestBucketFailureMidwayKeepsEarlierDays(t *testing.T) {
	e := newRollEnv(t)
	today := e.today()
	for d := today - 12; d < today-9; d++ {
		e.seedDay(d, rollSlug, 1_000_000+d, fmt.Sprintf("20261001-%06d-aaaa", d-today+20))
	}
	e.facts.onCall = func(n int) error {
		if n == 3 {
			return errors.New("bucket killed")
		}
		return nil
	}
	_, err := e.roll(budget.RolloverOptions{})
	if err == nil || budget.OnlyRefusals(err) {
		t.Fatalf("err = %v", err)
	}
	if !isFinal(e.doc(today-12, rollSlug)) || !isFinal(e.doc(today-11, rollSlug)) || e.doc(today-10, rollSlug) != nil || !e.dayInDB(today-10) {
		t.Fatal("earlier days were not kept, or the failed day was touched")
	}
}

func TestRemovedCapIsClearedAndRerunWritesNothing(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 1
	e.seedDay(d, rollSlug, 1_000_000, "20261019-100000-aaaa")
	e.db.Set("config/caps/repos/"+rollSlug, map[string]any{"dailyMicros": 9_000_000})
	e.mustRoll(budget.RolloverOptions{})
	if e.doc(d, rollSlug)["capDailyMicros"] != int64(9_000_000) {
		t.Fatal("cap not recorded")
	}
	e.db.Set("config/caps/repos/"+rollSlug, nil)
	e.mustRoll(budget.RolloverOptions{})
	if _, ok := e.doc(d, rollSlug)["capDailyMicros"]; ok {
		t.Fatal("a removed cap stayed in the document")
	}
	n := e.patches()
	e.mustRoll(budget.RolloverOptions{})
	if e.patches() != n {
		t.Fatal("an unchanged document was rewritten")
	}
}
