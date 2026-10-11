//go:build firebase

package rules

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const usd = budget.Micros(1_000_000)

func dollars(n float64) budget.Micros   { return budget.Micros(n * 1e6) }
func mp(m budget.Micros) *budget.Micros { return &m }

// world is a database state plus the configuration around it.
type world struct {
	slug, run string
	day       int64 // the day the write names (and the state is for)
	caps      budget.Caps
	kills     budget.Kills
	st        budget.State
}

func today() int64 { return budget.Day(time.Now()) }

// stdWorld is the design's example: global daily $150, repository daily $60,
// per-run $20, maxReserve $5, enforce mode.
func stdWorld() world {
	return world{
		slug: "aurora", run: "r1", day: today(),
		caps: budget.Caps{
			Global:   &budget.GlobalCaps{DailyMicros: mp(150 * usd), PerRunMicros: mp(20 * usd)},
			Defaults: &budget.DefaultCaps{RepoDailyMicros: mp(60 * usd), RepoPerRunMicros: mp(20 * usd)},
			Limits:   &budget.Limits{MaxReserveMicros: mp(5 * usd)},
			Mode:     budget.ModeEnforce,
		},
	}
}

// seed replaces the database with the world, with admin rights.
func (h *harness) seed(w world) {
	h.t.Helper()
	h.wipe()
	u := map[string]any{}
	put := func(path string, v any) {
		u[path] = v
	}
	if w.caps.Global != nil {
		put(budget.PathCapsGlobal, w.caps.Global)
	}
	if w.caps.Defaults != nil {
		put(budget.PathCapsDefaults, w.caps.Defaults)
	}
	if w.caps.Repo != nil {
		put(budget.PathCapsRepo(w.slug), w.caps.Repo)
	}
	if w.caps.Limits != nil {
		put(budget.PathLimits, w.caps.Limits)
	}
	if w.caps.Mode != "" {
		put(budget.PathMode, w.caps.Mode)
	}
	if w.kills.Global != nil {
		put(budget.PathKillGlobal, w.kills.Global)
	}
	if w.kills.Repo != nil {
		put(budget.PathKillRepo(w.slug), w.kills.Repo)
	}
	ledger := func(prefix string, l budget.RunLedger) {
		for f, v := range map[string]budget.Micros{"reserved": l.Reserved, "released": l.Released, "spent": l.Spent} {
			if v != 0 {
				put(prefix+"/"+f, int64(v))
			}
		}
	}
	ledger(budget.PathRun(w.slug, w.run), w.st.Run)
	ledger(budget.PathSpendRun(w.day, w.slug, w.run), w.st.DayRun)
	if w.st.Repo.Counted != 0 {
		put(budget.PathSpendRepo(w.day, w.slug)+"/counted", int64(w.st.Repo.Counted))
	}
	if w.st.Global.Counted != 0 {
		put(budget.PathSpendGlobal(w.day)+"/counted", int64(w.st.Global.Counted))
	}
	if len(u) > 0 {
		h.patch(u)
	}
}

// leaseUpdates is the multi-path write of a lease of amt on day: absolute new
// values computed from the state the client read.
func leaseUpdates(w world, amt budget.Micros) map[string]any {
	return map[string]any{
		budget.PathRun(w.slug, w.run) + "/reserved":             int64(w.st.Run.Reserved + amt),
		budget.PathSpendRun(w.day, w.slug, w.run) + "/reserved": int64(w.st.DayRun.Reserved + amt),
		budget.PathSpendRepo(w.day, w.slug) + "/counted":        int64(w.st.Repo.Counted + amt),
		budget.PathSpendGlobal(w.day) + "/counted":              int64(w.st.Global.Counted + amt),
	}
}

// releaseUpdates gives amt of the unspent reservation back on w.day.
func releaseUpdates(w world, amt budget.Micros) map[string]any {
	return map[string]any{
		budget.PathRun(w.slug, w.run) + "/released":             int64(w.st.Run.Released + amt),
		budget.PathSpendRun(w.day, w.slug, w.run) + "/released": int64(w.st.DayRun.Released + amt),
		budget.PathSpendRepo(w.day, w.slug) + "/counted":        int64(w.st.Repo.Counted - amt),
		budget.PathSpendGlobal(w.day) + "/counted":              int64(w.st.Global.Counted - amt),
	}
}

func (w world) client(h *harness) *rtdb.Client {
	return h.as(claims{slug: budget.Key(w.slug), run: budget.Key(w.run)})
}

func (h *harness) wantValue(path string, want string) {
	h.t.Helper()
	if got := h.value(path); got != want {
		h.t.Fatalf("/%s = %s, want %s", path, got, want)
	}
}

// leased is a world in which the run already holds a lease of amt today.
func leased(amt budget.Micros) world {
	w := stdWorld()
	w.st = budget.State{
		Run:    budget.RunLedger{Reserved: amt},
		DayRun: budget.RunLedger{Reserved: amt},
		Repo:   budget.Counters{Counted: amt},
		Global: budget.Counters{Counted: amt},
	}
	return w
}

func TestAllowedPaths(t *testing.T) {
	ctx := context.Background()
	t.Run("reserve", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		h.seed(w)
		h.mustAllow(w.client(h), "a $2 lease", leaseUpdates(w, 2*usd))
		h.wantValue(budget.PathRun("aurora", "r1")+"/reserved", "2000000")
		h.wantValue(budget.PathSpendGlobal(w.day)+"/counted", "2000000")
		h.wantValue(budget.PathSpendRepo(w.day, "aurora")+"/counted", "2000000")
		h.wantValue(budget.PathSpendRun(w.day, "aurora", "r1")+"/reserved", "2000000")
	})
	t.Run("reserve exactly at maxReserve and at each cap", func(t *testing.T) {
		for name, mod := range map[string]func(*world) budget.Micros{
			"maxReserve": func(w *world) budget.Micros { return 5 * usd },
			"per-run cap": func(w *world) budget.Micros {
				w.st.Run, w.st.DayRun = budget.RunLedger{Reserved: 18 * usd}, budget.RunLedger{Reserved: 18 * usd}
				w.st.Repo.Counted, w.st.Global.Counted = 18*usd, 18*usd
				return 2 * usd
			},
			"repository daily cap": func(w *world) budget.Micros { w.st.Repo.Counted = 58 * usd; return 2 * usd },
			"global daily cap":     func(w *world) budget.Micros { w.st.Global.Counted = 148 * usd; return 2 * usd },
		} {
			t.Run(name, func(t *testing.T) {
				h := newHarness(t)
				w := stdWorld()
				amt := mod(&w)
				h.seed(w)
				h.mustAllow(w.client(h), name, leaseUpdates(w, amt))
			})
		}
	})
	t.Run("report", func(t *testing.T) {
		h := newHarness(t)
		w := leased(3 * usd)
		h.seed(w)
		c := w.client(h)
		day := w.day
		h.mustAllow(c, "a usage report", map[string]any{
			budget.PathRun("aurora", "r1") + "/spent":                   int64(1 * usd),
			budget.PathRun("aurora", "r1") + "/overrun":                 int64(usd / 10),
			budget.PathRun("aurora", "r1") + "/tokens":                  int64(12345),
			budget.PathSpendRun(day, "aurora", "r1") + "/spent":         int64(1 * usd),
			budget.PathSpendRepo(day, "aurora") + "/spent":              int64(1 * usd),
			budget.PathSpendRepo(day, "aurora") + "/calls":              3,
			budget.PathSpendGlobal(day) + "/spent":                      int64(1 * usd),
			budget.PathSpendGlobal(day) + "/calls":                      3,
			budget.PathByModel(day, "aurora", "claude-3.5") + "/micros": int64(1 * usd),
			budget.PathByModel(day, "aurora", "claude-3.5") + "/in":     1000,
			budget.PathByModel(day, "aurora", "claude-3.5") + "/out":    200,
			budget.PathByModel(day, "aurora", "claude-3.5") + "/cr":     5000,
			budget.PathByModel(day, "aurora", "claude-3.5") + "/cw":     50,
		})
		h.wantValue(budget.PathRun("aurora", "r1")+"/spent", "1000000")
	})
	t.Run("release", func(t *testing.T) {
		h := newHarness(t)
		w := leased(3 * usd)
		w.st.Run.Spent = 1 * usd
		h.seed(w)
		h.mustAllow(w.client(h), "release the $2 unspent", releaseUpdates(w, 2*usd))
		h.wantValue(budget.PathSpendGlobal(w.day)+"/counted", "1000000")
		h.wantValue(budget.PathRun("aurora", "r1")+"/released", "2000000")
	})
	t.Run("release is never stopped by a kill switch or an exhausted cap", func(t *testing.T) {
		h := newHarness(t)
		w := leased(3 * usd)
		w.kills = budget.Kills{Global: &budget.Kill{On: true}, Repo: &budget.Kill{On: true}}
		w.caps.Global.DailyMicros = mp(0)
		h.seed(w)
		h.mustAllow(w.client(h), "release under a kill", releaseUpdates(w, 3*usd))
	})
	t.Run("notional (oauth) is increase-only and uncapped", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		w.kills = budget.Kills{Global: &budget.Kill{On: true}}
		w.caps.Global.DailyMicros = mp(0)
		h.seed(w)
		day := w.day
		h.mustAllow(w.client(h), "notional", map[string]any{
			budget.PathRun("aurora", "r1") + "/notional":                   int64(4 * usd),
			budget.PathSpendRepo(day, "aurora") + "/notional":              int64(4 * usd),
			budget.PathSpendGlobal(day) + "/notional":                      int64(4 * usd),
			budget.PathByModel(day, "aurora", "claude-opus-5") + "/micros": int64(4 * usd),
		})
	})
	t.Run("heartbeat and registry", func(t *testing.T) {
		h := newHarness(t)
		w := leased(2 * usd)
		h.seed(w)
		c := w.client(h)
		entry := map[string]any{"repo": "aurora/web", "workflow": "implement", "title": "fix the thing", "stage": "code", "round": 1,
			"auth": "api-key", "startedAt": time.Now().UnixMilli(), "updatedAt": time.Now().UnixMilli(), "spent": 0, "requestedBy": "alice@example.invalid"}
		h.mustAllow(c, "create the entry", map[string]any{budget.PathAgent("aurora", "r1"): entry})
		h.mustAllow(c, "an entry with a recipe", map[string]any{budget.PathAgent("aurora", "r1") + "/recipe": "claude-solo"})
		recipeEntry := map[string]any{}
		for k, v := range entry {
			recipeEntry[k] = v
		}
		recipeEntry["recipe"] = "claude-solo"
		h.mustAllow(c, "a whole entry carrying a recipe", map[string]any{budget.PathAgent("aurora", "r1"): recipeEntry})
		h.mustDeny(c, "a recipe over 200 characters", map[string]any{budget.PathAgent("aurora", "r1") + "/recipe": strings.Repeat("r", 201)})
		h.mustDeny(c, "a recipe that is not a string", map[string]any{budget.PathAgent("aurora", "r1") + "/recipe": 7})
		h.mustAllow(c, "a heartbeat with the usage report in the same write", map[string]any{
			budget.PathAgent("aurora", "r1") + "/stage":     "verify",
			budget.PathAgent("aurora", "r1") + "/updatedAt": time.Now().UnixMilli(),
			budget.PathAgent("aurora", "r1") + "/spent":     int64(usd),
			budget.PathRun("aurora", "r1") + "/spent":       int64(usd),
		})
		h.mustAllow(c, "delete the entry at the end of the run", map[string]any{budget.PathAgent("aurora", "r1"): nil})
		h.wantValue(budget.PathAgent("aurora", "r1"), "null")
	})
	// generic-tool task 3 review item 4: action and tokens have no cases of
	// their own yet. action is STR && RBOK, like every other registry
	// string; tokens is bounded, integer and RBOK.
	t.Run("action and tokens", func(t *testing.T) {
		h := newHarness(t)
		w := leased(2 * usd)
		h.seed(w)
		c := w.client(h)
		agent := budget.PathAgent("aurora", "r1")
		h.mustAllow(c, "create the entry", map[string]any{agent: map[string]any{"repo": "aurora/web", "requestedBy": "alice@example.invalid"}})
		h.mustAllow(c, "action at the 200 character limit", map[string]any{agent + "/action": strings.Repeat("a", 200)})
		h.mustDeny(c, "action over 200 characters", map[string]any{agent + "/action": strings.Repeat("a", 201)})
		h.mustDeny(c, "action that is not a string", map[string]any{agent + "/action": 7})
		h.mustAllow(c, "tokens at 0", map[string]any{agent + "/tokens": 0})
		h.mustAllow(c, "tokens at 1200", map[string]any{agent + "/tokens": 1200})
		h.mustAllow(c, "tokens at the 1e9 limit", map[string]any{agent + "/tokens": 1_000_000_000})
		h.mustDeny(c, "tokens one over the limit", map[string]any{agent + "/tokens": 1_000_000_001})
		h.mustDeny(c, "negative tokens", map[string]any{agent + "/tokens": -1})
		h.mustDeny(c, "tokens as a string", map[string]any{agent + "/tokens": "5"})
		h.mustDeny(c, "tokens not an integer", map[string]any{agent + "/tokens": 1.5})
		h.mustDeny(c, "an unknown field next to action and tokens", map[string]any{agent: map[string]any{
			"repo": "x", "requestedBy": "alice@example.invalid", "actionLog": "x",
		}})
		h.mustAllow(c, "a whole entry carrying both new keys", map[string]any{agent: map[string]any{
			"repo": "aurora/web", "requestedBy": "alice@example.invalid", "action": "tool Bash: go test", "tokens": 1200,
		}})
		// RBOK on the new keys specifically: a leaf write from a token whose
		// rb does not own the entry is refused. This is the plan's own
		// mutation check for this review (remove RBOK from action's
		// validate rule) — it must fail here, not only in the generated
		// rules' golden.
		mallory := h.as(claims{slug: "aurora", run: "r1", rb: "mallory@example.invalid"})
		h.mustDeny(mallory, "an action write from a token whose rb does not own the entry", map[string]any{agent + "/action": "tool Bash: rm -rf /"})
		h.mustDeny(mallory, "a tokens write from a token whose rb does not own the entry", map[string]any{agent + "/tokens": 1})
		norb := h.as(claims{slug: "aurora", run: "r1", rb: "-"})
		h.mustDeny(norb, "an action write from a token without rb", map[string]any{agent + "/action": "tool Bash: go test"})
		h.mustDeny(norb, "a tokens write from a token without rb", map[string]any{agent + "/tokens": 1})
		// Another run's or repository's entry: this run's token never
		// satisfies RUN for a different $slug/$run, whatever the field.
		h.mustDeny(c, "an action write to another run's entry", map[string]any{budget.PathAgent("aurora", "r9") + "/action": "tool Bash: go test"})
		h.mustDeny(c, "an action write to another repository's entry", map[string]any{budget.PathAgent("other-repo", "r1") + "/action": "tool Bash: go test"})
	})
	t.Run("outcome is written once", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		h.seed(w)
		c := w.client(h)
		path := budget.PathOutcome(w.day, "aurora", "r1")
		h.mustAllow(c, "the outcome", map[string]any{path: map[string]any{"status": "succeeded", "requestedBy": "alice@example.invalid"}})
		h.mustDeny(c, "a second outcome", map[string]any{path: map[string]any{"status": "failed", "requestedBy": "alice@example.invalid"}})
		h.mustDeny(c, "an outcome overwrite by leaf", map[string]any{path + "/status": "failed"})
		h.mustDeny(c, "an outcome delete", map[string]any{path: nil})
		_ = ctx
	})
	t.Run("a run reads its own slice and nothing of others", func(t *testing.T) {
		h := newHarness(t)
		w := leased(2 * usd)
		w.caps.Repo = &budget.RepoCaps{DailyMicros: mp(60 * usd)}
		h.seed(w)
		h.patch(map[string]any{
			budget.PathCapsRepo("other"):                      map[string]any{"dailyMicros": 1},
			budget.PathKillRepo("other") + "/on":              false,
			budget.PathSpendRepo(w.day, "other") + "/counted": 7,
			budget.PathRun("other", "r9") + "/reserved":       7,
			budget.PathAgent("other", "r9") + "/repo":         "x",
			budget.PathAgent("aurora", "r1") + "/repo":        "x",
		})
		c := w.client(h)
		for _, p := range []string{budget.PathCapsGlobal, budget.PathCapsDefaults, budget.PathLimits, budget.PathMode, budget.PathKillGlobal,
			budget.PathCapsRepo("aurora"), budget.PathKillRepo("aurora"), budget.PathSpendGlobal(w.day), budget.PathSpendRepo(w.day, "aurora"),
			budget.PathRun("aurora", "r1"), budget.PathSpendRun(w.day, "aurora", "r1"), budget.PathAgent("aurora", "r1")} {
			h.mustRead(c, p)
		}
		for _, p := range []string{"", "config", "config/caps", "config/caps/repos", "config/kill", budget.PathCapsRepo("other"), budget.PathKillRepo("other"),
			budget.PathSpendRepo(w.day, "other"), "spend/" + budget.DayKey(w.day), "spend", "runs", "runs/other", budget.PathRun("other", "r9"),
			budget.PathRun("aurora", "r2"), "runs/aurora", "agents", "agents/other", budget.PathAgent("other", "r9"), "agents/aurora", "outcomes",
			budget.PathProject, budget.PathMark, budget.PathSpendMeta(w.day), budget.PathSpendRun(w.day, "other", "r9")} {
			h.mustReadDenied(c, p)
		}
	})
}

// denialRow is one row of the design's table (§6.4), a named test.
func TestDenialTable(t *testing.T) {
	t.Run("row01_lower_the_global_counter_alone", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		w.st.Global.Counted = 10 * usd
		h.seed(w)
		h.mustDeny(w.client(h), "global counted 10 -> 5", map[string]any{budget.PathSpendGlobal(w.day) + "/counted": int64(5 * usd)})
		h.mustDeny(w.client(h), "global counted 10 -> 0 by delete", map[string]any{budget.PathSpendGlobal(w.day) + "/counted": nil})
	})
	t.Run("row02_release_more_than_is_outstanding", func(t *testing.T) {
		h := newHarness(t)
		w := leased(2 * usd)
		w.st.Global.Counted, w.st.Repo.Counted = 10*usd, 5*usd
		h.seed(w)
		h.mustDeny(w.client(h), "release $5 with $2 outstanding", releaseUpdates(w, 5*usd))
		h.mustAllow(w.client(h), "release exactly the $2", releaseUpdates(w, 2*usd))
	})
	t.Run("row03_write_another_runs_or_repos_state", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		h.seed(w)
		c := w.client(h)
		day := w.day
		h.mustDeny(c, "run B's ledger", map[string]any{budget.PathRun("aurora", "rB") + "/reserved": int64(usd)})
		h.mustDeny(c, "run B's day share", map[string]any{budget.PathSpendRun(day, "aurora", "rB") + "/reserved": int64(usd)})
		h.mustDeny(c, "another repository's ledger", map[string]any{budget.PathRun("other", "r1") + "/reserved": int64(usd)})
		h.mustDeny(c, "another repository's counter", map[string]any{budget.PathSpendRepo(day, "other") + "/counted": int64(usd)})
		h.mustDeny(c, "another repository's registry", map[string]any{budget.PathAgent("other", "r1") + "/repo": "x"})
		h.mustDeny(c, "another run's registry", map[string]any{budget.PathAgent("aurora", "rB") + "/repo": "x"})
		// Fields that tie to nothing else: only the identity check protects them.
		for _, p := range []string{budget.PathRun("other", "r1"), budget.PathRun("aurora", "rB")} {
			h.mustDeny(c, "notional on "+p, map[string]any{p + "/notional": 1})
			h.mustDeny(c, "tokens on "+p, map[string]any{p + "/tokens": 1})
		}
		entry := map[string]any{"repo": "x", "requestedBy": "alice@example.invalid"}
		h.mustDeny(c, "a well-formed registry entry of another repository's run", map[string]any{budget.PathAgent("other", "r1"): entry})
		h.mustDeny(c, "a well-formed registry entry of another run", map[string]any{budget.PathAgent("aurora", "rB"): entry})
		h.mustDeny(c, "an outcome of another repository's run", map[string]any{budget.PathOutcome(day, "other", "r1"): map[string]any{"status": "failed", "requestedBy": "alice@example.invalid"}})
		h.mustDeny(c, "an outcome of another run", map[string]any{budget.PathOutcome(day, "aurora", "rB"): map[string]any{"status": "failed", "requestedBy": "alice@example.invalid"}})
		// A lease for run B written with A's token, counters included.
		wb := w
		wb.run = "rB"
		h.mustDeny(c, "a whole lease for run B", leaseUpdates(wb, usd))
		// ... and for another repository.
		wo := w
		wo.slug = "other"
		h.mustDeny(c, "a whole lease for another repository", leaseUpdates(wo, usd))
		// The attack that the counter's own-slug check alone stops: the run's
		// delta is legitimate, and it also moves another repository's counter
		// by the same amount (the delta equation reads the run's own share).
		u := leaseUpdates(w, usd)
		u[budget.PathSpendRepo(day, "other")+"/counted"] = int64(usd)
		h.mustDeny(c, "an honest lease that also inflates another repository's counter", u)
		u = leaseUpdates(w, usd)
		delete(u, budget.PathSpendRepo(day, "aurora")+"/counted")
		u[budget.PathSpendRepo(day, "other")+"/counted"] = int64(usd)
		h.mustDeny(c, "a lease charged to another repository instead of its own", u)
	})
	t.Run("row04_reserve_over_maxReserve", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		h.seed(w)
		h.mustDeny(w.client(h), "$6 against maxReserve $5", leaseUpdates(w, 6*usd))
		h.mustAllow(w.client(h), "$5 is the boundary", leaseUpdates(w, 5*usd))
	})
	t.Run("row05_reserve_past_the_per_run_cap", func(t *testing.T) {
		h := newHarness(t)
		w := leased(18 * usd)
		h.seed(w)
		h.mustDeny(w.client(h), "18 + 3 > 20", leaseUpdates(w, 3*usd))
		// Released money frees headroom: 18 - 4 released + 5 = 19.
		w2 := leased(18 * usd)
		w2.st.Run.Released, w2.st.DayRun.Released = 4*usd, 4*usd
		w2.st.Repo.Counted, w2.st.Global.Counted = 14*usd, 14*usd
		h.seed(w2)
		h.mustAllow(w2.client(h), "released money counts back", leaseUpdates(w2, 5*usd))
	})
	t.Run("row06_reserve_past_the_repository_daily_cap", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		w.st.Repo.Counted = 59 * usd
		h.seed(w)
		h.mustDeny(w.client(h), "59 + 2 > 60", leaseUpdates(w, 2*usd))
		h.mustAllow(w.client(h), "59 + 1 = 60", leaseUpdates(w, 1*usd))
	})
	t.Run("row06b_reserve_past_the_global_daily_cap", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		w.st.Global.Counted = 149 * usd
		h.seed(w)
		h.mustDeny(w.client(h), "149 + 2 > 150", leaseUpdates(w, 2*usd))
	})
	t.Run("row07_reserve_against_another_days_node", func(t *testing.T) {
		h := newHarness(t)
		for name, day := range map[string]int64{"yesterday": today() - 1, "tomorrow": today() + 1, "a week ago": today() - 7} {
			w := stdWorld()
			w.day = day
			h.seed(w)
			h.mustDeny(w.client(h), "a lease on "+name, leaseUpdates(w, usd))
		}
		// The share and the counters alone, on yesterday, without the ledger.
		w := stdWorld()
		w.day = today() - 1
		h.seed(w)
		u := leaseUpdates(w, usd)
		delete(u, budget.PathRun("aurora", "r1")+"/reserved")
		h.mustDeny(w.client(h), "share and counters only, yesterday", u)
	})
	t.Run("row08_move_a_reservation_without_the_shared_counters", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		h.seed(w)
		c := w.client(h)
		full := leaseUpdates(w, 2*usd)
		for _, drop := range []string{
			budget.PathSpendGlobal(w.day) + "/counted",
			budget.PathSpendRepo(w.day, "aurora") + "/counted",
			budget.PathSpendRun(w.day, "aurora", "r1") + "/reserved",
			budget.PathRun("aurora", "r1") + "/reserved",
		} {
			u := map[string]any{}
			for k, v := range full {
				if k != drop {
					u[k] = v
				}
			}
			h.mustDeny(c, "a lease without "+drop, u)
		}
		for k, v := range full {
			h.mustDeny(c, "only "+k, map[string]any{k: v})
		}
		// A counter moving by more or less than the share.
		u := leaseUpdates(w, 2*usd)
		u[budget.PathSpendGlobal(w.day)+"/counted"] = int64(3 * usd)
		h.mustDeny(c, "global counted by $3 for a $2 lease", u)
		u = leaseUpdates(w, 2*usd)
		u[budget.PathSpendRepo(w.day, "aurora")+"/counted"] = int64(1 * usd)
		h.mustDeny(c, "repository counted by $1 for a $2 lease", u)
		// The ledger moving by more than its share.
		u = leaseUpdates(w, 2*usd)
		u[budget.PathRun("aurora", "r1")+"/reserved"] = int64(3 * usd)
		h.mustDeny(c, "ledger $3 for a $2 share", u)
		h.mustAllow(c, "the honest lease", full)
	})
	t.Run("row09_set_a_kill_switch_off_or_raise_a_cap", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		w.kills = budget.Kills{Global: &budget.Kill{On: true}, Repo: &budget.Kill{On: true}}
		h.seed(w)
		c := w.client(h)
		for _, p := range []string{budget.PathKillGlobal + "/on", budget.PathKillRepo("aurora") + "/on", budget.PathCapsGlobal + "/dailyMicros",
			budget.PathCapsDefaults + "/repoDailyMicros", budget.PathLimits + "/maxReserveMicros", budget.PathMode, budget.PathProject, budget.PathMark,
			budget.PathCapsRepo("aurora") + "/dailyMicros"} {
			var v any = int64(999 * usd)
			if strings.HasSuffix(p, "/on") {
				v = false
			}
			h.mustDeny(c, "write /"+p, map[string]any{p: v})
		}
		h.mustDeny(c, "delete the kill switch", map[string]any{budget.PathKillGlobal: nil})
		h.mustDeny(c, "replace all of config", map[string]any{"config": map[string]any{}})
		h.rawDenied(claims{slug: "aurora", run: "r1"}, http.MethodPut, "", "null")
		h.rawDenied(claims{slug: "aurora", run: "r1"}, http.MethodPut, "config", "{}")
		h.rawDenied(claims{slug: "aurora", run: "r1"}, http.MethodDelete, "fugaro/project", "")
	})
	t.Run("row10_reserve_after_a_kill_switch", func(t *testing.T) {
		for name, k := range map[string]budget.Kills{
			"global": {Global: &budget.Kill{On: true}},
			"repo":   {Repo: &budget.Kill{On: true}},
		} {
			t.Run(name, func(t *testing.T) {
				h := newHarness(t)
				w := stdWorld()
				w.kills = k
				h.seed(w)
				h.mustDeny(w.client(h), "a lease under the "+name+" switch", leaseUpdates(w, usd))
			})
		}
		// A switch that is off, or a node without "on", is not a kill.
		h := newHarness(t)
		w := stdWorld()
		w.kills = budget.Kills{Global: &budget.Kill{On: false}, Repo: &budget.Kill{On: false, By: "alice"}}
		h.seed(w)
		h.mustAllow(w.client(h), "switches present but off", leaseUpdates(w, usd))
	})
	t.Run("row11_use_the_token_after_its_deadline", func(t *testing.T) {
		h := newHarness(t)
		w := leased(2 * usd)
		h.seed(w)
		expired := h.as(claims{slug: "aurora", run: "r1", fx: time.Now().Add(-time.Minute).UnixMilli()})
		h.mustDeny(expired, "a lease", leaseUpdates(w, usd))
		h.mustDeny(expired, "a release", releaseUpdates(w, usd))
		h.mustDeny(expired, "a registry write", map[string]any{budget.PathAgent("aurora", "r1") + "/repo": "x"})
		h.mustDeny(expired, "an outcome", map[string]any{budget.PathOutcome(w.day, "aurora", "r1"): map[string]any{"status": "failed", "requestedBy": "alice@example.invalid"}})
		for _, p := range []string{budget.PathCapsGlobal, budget.PathMode, budget.PathRun("aurora", "r1"), budget.PathSpendGlobal(w.day)} {
			h.mustReadDenied(expired, p)
		}
		noFx := h.as(claims{slug: "aurora", run: "r1", fx: 1})
		h.mustDeny(noFx, "fx in 1970", leaseUpdates(w, usd))
	})
	t.Run("row12_delete_the_own_ledger", func(t *testing.T) {
		h := newHarness(t)
		w := leased(18 * usd)
		h.seed(w)
		c := w.client(h)
		h.mustDeny(c, "the ledger node", map[string]any{budget.PathRun("aurora", "r1"): nil})
		h.mustDeny(c, "the reserved field", map[string]any{budget.PathRun("aurora", "r1") + "/reserved": nil})
		h.mustDeny(c, "the ledger replaced by an empty object", map[string]any{budget.PathRun("aurora", "r1"): map[string]any{}})
		h.mustDeny(c, "the ledger replaced by a smaller one", map[string]any{budget.PathRun("aurora", "r1"): map[string]any{"reserved": 0}})
		h.mustDeny(c, "the whole runs tree", map[string]any{"runs": nil})
	})
}

func TestDeleteOwnLedgerDenied(t *testing.T) {
	h := newHarness(t)
	w := leased(4 * usd)
	w.st.Run.Spent, w.st.DayRun.Spent = usd, usd
	h.seed(w)
	c := w.client(h)
	day := w.day
	h.patch(map[string]any{
		budget.PathRun("aurora", "r1") + "/notional":       7,
		budget.PathSpendRepo(day, "aurora") + "/spent":     7,
		budget.PathByModel(day, "aurora", "m") + "/micros": 7,
		budget.PathSpendGlobal(day) + "/calls":             7,
	})
	for _, p := range []string{
		budget.PathRun("aurora", "r1"), "runs/aurora", "runs",
		budget.PathRun("aurora", "r1") + "/reserved", budget.PathRun("aurora", "r1") + "/released", budget.PathRun("aurora", "r1") + "/spent",
		budget.PathRun("aurora", "r1") + "/notional",
		budget.PathSpendRun(day, "aurora", "r1"), budget.PathSpendRun(day, "aurora", "r1") + "/reserved", budget.PathSpendRun(day, "aurora", "r1") + "/spent",
		budget.PathSpendRepo(day, "aurora"), budget.PathSpendRepo(day, "aurora") + "/counted", budget.PathSpendRepo(day, "aurora") + "/spent",
		budget.PathByModel(day, "aurora", "m") + "/micros", budget.PathByModel(day, "aurora", "m"),
		budget.PathSpendGlobal(day), budget.PathSpendGlobal(day) + "/counted", budget.PathSpendGlobal(day) + "/calls",
		"spend/" + budget.DayKey(day), "spend",
	} {
		h.mustDeny(c, "delete /"+p, map[string]any{p: nil})
	}
	// Fields the rules do not know cannot be created anywhere in the money tree.
	for _, p := range []string{budget.PathRun("aurora", "r1") + "/crashed", budget.PathRun("aurora", "r1") + "/extra",
		budget.PathSpendRun(day, "aurora", "r1") + "/notional", budget.PathSpendRepo(day, "aurora") + "/archived",
		budget.PathSpendGlobal(day) + "/archived", budget.PathSpendMeta(day) + "/archived", budget.PathByModel(day, "aurora", "m") + "/extra",
		budget.PathCapsGlobal + "/extra", "spend/" + budget.DayKey(day) + "/extra"} {
		h.mustDeny(c, "create /"+p, map[string]any{p: 1})
	}
	h.mustDeny(c, "a model key over 100 characters", map[string]any{budget.PathByModel(day, "aurora", strings.Repeat("m", 101)) + "/micros": 1})
	h.mustAllow(c, "a model key of 100 characters", map[string]any{budget.PathByModel(day, "aurora", strings.Repeat("m", 100)) + "/micros": 1})
	// A whole-node write too: it would reset the fields it leaves out.
	h.mustDeny(c, "replace the ledger", map[string]any{budget.PathRun("aurora", "r1"): map[string]any{"reserved": int64(4 * usd), "released": 0, "spent": 0}})
	h.mustDeny(c, "replace the global counters", map[string]any{budget.PathSpendGlobal(day): map[string]any{"counted": 0}})
}

func TestObserveLeniencyOnlyCaps(t *testing.T) {
	for _, mode := range []string{budget.ModeObserve, "", "enforced", "ENFORCE"} {
		name := mode
		if name == "" {
			name = "absent"
		}
		t.Run("mode_"+name, func(t *testing.T) {
			h := newHarness(t)
			base := func(mod func(*world)) world {
				w := stdWorld()
				w.caps.Mode = mode
				if mod != nil {
					mod(&w)
				}
				return w
			}
			allowed := map[string]func(*world) budget.Micros{
				"over the per-run cap": func(w *world) budget.Micros {
					w.st.Run.Reserved, w.st.DayRun.Reserved = 19*usd, 19*usd
					w.st.Repo.Counted, w.st.Global.Counted = 19*usd, 19*usd
					return 2 * usd
				},
				"over the repository daily cap": func(w *world) budget.Micros { w.st.Repo.Counted = 59 * usd; return 2 * usd },
				"over the global daily cap":     func(w *world) budget.Micros { w.st.Global.Counted = 149 * usd; return 2 * usd },
				"no caps at all":                func(w *world) budget.Micros { w.caps.Global, w.caps.Defaults = nil, nil; return 2 * usd },
				"a zero cap":                    func(w *world) budget.Micros { w.caps.Global.DailyMicros = mp(0); return 2 * usd },
			}
			for what, mod := range allowed {
				w := base(nil)
				amt := mod(&w)
				h.seed(w)
				h.mustAllow(w.client(h), what+" is only advisory outside enforce", leaseUpdates(w, amt))
			}
			denied := map[string]func(*world) budget.Micros{
				"a global kill":     func(w *world) budget.Micros { w.kills.Global = &budget.Kill{On: true}; return usd },
				"a repository kill": func(w *world) budget.Micros { w.kills.Repo = &budget.Kill{On: true}; return usd },
				"over maxReserve":   func(w *world) budget.Micros { return 6 * usd },
				"no maxReserve":     func(w *world) budget.Micros { w.caps.Limits = nil; return usd },
				"another day":       func(w *world) budget.Micros { w.day = today() - 1; return usd },
			}
			for what, mod := range denied {
				w := base(nil)
				amt := mod(&w)
				h.seed(w)
				h.mustDeny(w.client(h), what+" still denies outside enforce", leaseUpdates(w, amt))
			}
			// The delta equations and the identity checks do not relax.
			w := base(nil)
			h.seed(w)
			c := w.client(h)
			u := leaseUpdates(w, usd)
			u[budget.PathSpendGlobal(w.day)+"/counted"] = int64(2 * usd)
			h.mustDeny(c, "a wrong delta", u)
			wo := w
			wo.slug = "other"
			h.mustDeny(c, "a foreign slug", leaseUpdates(wo, usd))
			h.patch(map[string]any{budget.PathSpendGlobal(w.day) + "/counted": int64(5 * usd)})
			h.mustDeny(c, "lowering a counter", map[string]any{budget.PathSpendGlobal(w.day) + "/counted": 0})
			// Releases are unchanged.
			wr := leased(3 * usd)
			wr.caps.Mode = mode
			h.seed(wr)
			h.mustDeny(wr.client(h), "an over-release", releaseUpdates(wr, 4*usd))
			h.mustAllow(wr.client(h), "a release", releaseUpdates(wr, 3*usd))
		})
	}
	t.Run("enforce_binds", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		w.st.Repo.Counted = 59 * usd
		h.seed(w)
		h.mustDeny(w.client(h), "enforce", leaseUpdates(w, 2*usd))
	})
}

func TestNullCapDenies(t *testing.T) {
	cases := map[string]func(*world){
		"no global daily cap":       func(w *world) { w.caps.Global.DailyMicros = nil },
		"no global per-run cap":     func(w *world) { w.caps.Global.PerRunMicros = nil },
		"no global caps node":       func(w *world) { w.caps.Global = nil },
		"no repository daily cap":   func(w *world) { w.caps.Defaults.RepoDailyMicros = nil },
		"no repository per-run cap": func(w *world) { w.caps.Defaults.RepoPerRunMicros = nil },
		"no defaults node":          func(w *world) { w.caps.Defaults = nil },
		"no maxReserve":             func(w *world) { w.caps.Limits = nil },
		"limits without the field":  func(w *world) { w.caps.Limits = &budget.Limits{} },
		"a zero global daily cap":   func(w *world) { w.caps.Global.DailyMicros = mp(0) },
		"a zero repository own cap": func(w *world) { w.caps.Repo = &budget.RepoCaps{DailyMicros: mp(0)} },
		"a zero own per-run cap":    func(w *world) { w.caps.Repo = &budget.RepoCaps{PerRunMicros: mp(0)} },
		"a zero global per-run cap": func(w *world) { w.caps.Global.PerRunMicros = mp(0) },
		"a zero maxReserve":         func(w *world) { w.caps.Limits.MaxReserveMicros = mp(0) },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			w := stdWorld()
			mod(&w)
			h.seed(w)
			h.mustDeny(w.client(h), name, leaseUpdates(w, usd))
		})
	}
	t.Run("a repository's own node beats the default, per field", func(t *testing.T) {
		h := newHarness(t)
		w := stdWorld()
		w.caps.Repo = &budget.RepoCaps{DailyMicros: mp(10 * usd)} // no own per-run: the default applies
		w.st.Repo.Counted = 9 * usd
		h.seed(w)
		h.mustAllow(w.client(h), "9 + 1 <= its own 10", leaseUpdates(w, usd))
		w.st.Repo.Counted = 10 * usd
		h.seed(w)
		h.mustDeny(w.client(h), "10 + 1 > its own 10 although the default is 60", leaseUpdates(w, usd))
	})
	t.Run("a zero cap with room left on a release", func(t *testing.T) {
		h := newHarness(t)
		w := leased(2 * usd)
		w.caps.Global.DailyMicros = mp(0)
		h.seed(w)
		h.mustAllow(w.client(h), "release does not look at caps", releaseUpdates(w, 2*usd))
	})
}

func TestForeignProjectClaimDenied(t *testing.T) {
	h := newHarness(t)
	w := leased(2 * usd)
	h.seed(w)
	for name, c := range map[string]claims{
		"another project's token": {slug: "aurora", run: "r1", fp: "other-fp"},
		"a token without fp":      {slug: "aurora", run: "r1", fp: "-"},
	} {
		cl := h.as(c)
		h.mustDeny(cl, name+": lease", leaseUpdates(w, usd))
		h.mustDeny(cl, name+": registry", map[string]any{budget.PathAgent("aurora", "r1"): map[string]any{"repo": "x", "requestedBy": "alice@example.invalid"}})
		h.mustDeny(cl, name+": counters", map[string]any{budget.PathSpendGlobal(w.day) + "/notional": 1})
		for _, p := range []string{budget.PathCapsGlobal, budget.PathLimits, budget.PathRun("aurora", "r1"), budget.PathSpendGlobal(w.day), budget.PathSpendRepo(w.day, "aurora")} {
			h.mustReadDenied(cl, p)
		}
	}
	// With no /fugaro/project at all, nobody matches (not even a token with no fp: null != null).
	h.adminDo("DELETE", budget.PathProject, nil)
	h.mustDeny(w.client(h), "the project is not marked", leaseUpdates(w, usd))
	h.mustDeny(h.as(claims{slug: "aurora", run: "r1", fp: "-"}), "no mark and no fp", leaseUpdates(w, usd))
	h.mustReadDenied(w.client(h), budget.PathLimits)
	// A token with no claims at all (a plain Firebase user) is nothing.
	plain := h.client(rtdb.Auth{IDToken: func() string { return unsignedToken(map[string]any{"uid": "someone"}) }})
	h.mustDeny(plain, "a user without claims", leaseUpdates(w, usd))
	h.mustReadDenied(plain, budget.PathLimits)
	// No token at all.
	anon := h.client(rtdb.Auth{IDToken: func() string { return "" }})
	h.mustReadDenied(anon, budget.PathLimits)
}

func TestRbMismatchDenied(t *testing.T) {
	h := newHarness(t)
	w := stdWorld()
	h.seed(w)
	c := w.client(h)
	agent := budget.PathAgent("aurora", "r1")
	h.mustDeny(c, "requestedBy of someone else", map[string]any{agent: map[string]any{"repo": "x", "requestedBy": "mallory@example.invalid"}})
	h.mustDeny(c, "no requestedBy", map[string]any{agent: map[string]any{"repo": "x"}})
	h.mustDeny(c, "a leaf without requestedBy on a new entry", map[string]any{agent + "/stage": "code"})
	h.mustDeny(c, "requestedBy of the wrong type", map[string]any{agent: map[string]any{"repo": "x", "requestedBy": 7}})
	h.mustDeny(c, "an unknown field", map[string]any{agent: map[string]any{"repo": "x", "requestedBy": "alice@example.invalid", "admin": true}})
	h.mustDeny(c, "a string over 200 characters", map[string]any{agent: map[string]any{"repo": strings.Repeat("x", 201), "requestedBy": "alice@example.invalid"}})
	h.mustAllow(c, "200 characters is the limit", map[string]any{agent: map[string]any{"repo": strings.Repeat("x", 200), "requestedBy": "alice@example.invalid"}})
	h.mustDeny(c, "changing requestedBy later", map[string]any{agent + "/requestedBy": "mallory@example.invalid"})
	h.mustAllow(c, "a leaf update leaves it", map[string]any{agent + "/stage": "code"})
	// A token without rb can write no registry entry and no outcome.
	norb := h.as(claims{slug: "aurora", run: "r1", rb: "-"})
	h.mustDeny(norb, "an entry from a token without rb", map[string]any{agent + "2": nil, agent + "/stage": "verify"})
	h.mustDeny(norb, "an entry with requestedBy but no rb claim", map[string]any{budget.PathAgent("aurora", "r1") + "/requestedBy": "alice@example.invalid"})
	// generic-tool task 3 review item 4: action and tokens are no different.
	h.mustDeny(norb, "an action write from a token without rb", map[string]any{agent + "/action": "tool Bash: go test"})
	h.mustDeny(norb, "a tokens write from a token without rb", map[string]any{agent + "/tokens": 1200})
	out := budget.PathOutcome(w.day, "aurora", "r1")
	h.mustDeny(c, "an outcome of someone else", map[string]any{out: map[string]any{"status": "failed", "requestedBy": "mallory@example.invalid"}})
	h.mustDeny(c, "an outcome without requestedBy", map[string]any{out: map[string]any{"status": "failed"}})
	h.mustDeny(c, "an outcome without status", map[string]any{out: map[string]any{"requestedBy": "alice@example.invalid"}})
	h.mustDeny(c, "an outcome with an unknown status", map[string]any{out: map[string]any{"status": "great", "requestedBy": "alice@example.invalid"}})
	h.mustDeny(norb, "an outcome from a token without rb", map[string]any{out: map[string]any{"status": "failed", "requestedBy": "alice@example.invalid"}})
	h.mustDeny(c, "an outcome for another day", map[string]any{budget.PathOutcome(w.day-5, "aurora", "r1"): map[string]any{"status": "failed", "requestedBy": "alice@example.invalid"}})
	h.mustAllow(c, "the right outcome", map[string]any{out: map[string]any{"status": "halted", "requestedBy": "alice@example.invalid"}})
}

// TestTodayExpression is the live half of the day-key test (the boundary half
// is TestGoMirrorOfTheDayKeys): the rules' day key for now is the decimal
// epoch-day budget.Day gives (assumption A14), and the neighbours are not it.
func TestTodayExpression(t *testing.T) {
	h := newHarness(t)
	w := stdWorld()
	h.seed(w)
	h.mustAllow(w.client(h), "a lease on budget.Day(now)", leaseUpdates(w, usd))
	for name, d := range map[string]int64{"yesterday": today() - 1, "tomorrow": today() + 1} {
		wd := stdWorld()
		wd.day = d
		h.seed(wd)
		h.mustDeny(wd.client(h), "a lease on "+name, leaseUpdates(wd, usd))
	}
	// A leading zero or a float is not the day key.
	w2 := stdWorld()
	h.seed(w2)
	u := leaseUpdates(w2, usd)
	delete(u, budget.PathSpendRun(w2.day, "aurora", "r1")+"/reserved")
	delete(u, budget.PathSpendRepo(w2.day, "aurora")+"/counted")
	delete(u, budget.PathSpendGlobal(w2.day)+"/counted")
	u["spend/0"+budget.DayKey(w2.day)+"/runs/aurora/r1/reserved"] = int64(usd)
	u["spend/0"+budget.DayKey(w2.day)+"/repos/aurora/counted"] = int64(usd)
	u["spend/0"+budget.DayKey(w2.day)+"/global/counted"] = int64(usd)
	h.mustDeny(w2.client(h), "a zero-padded day key", u)
}

func TestOldDayAllowsOnlySpentAndReleased(t *testing.T) {
	h := newHarness(t)
	y := today() - 1
	mk := func() world {
		w := stdWorld()
		w.day = y
		w.st = budget.State{
			Run:    budget.RunLedger{Reserved: 4 * usd},
			DayRun: budget.RunLedger{Reserved: 4 * usd},
			Repo:   budget.Counters{Counted: 4 * usd},
			Global: budget.Counters{Counted: 4 * usd},
		}
		return w
	}
	w := mk()
	h.seed(w)
	c := w.client(h)
	h.mustDeny(c, "reserve more against yesterday", leaseUpdates(w, usd))
	h.mustDeny(c, "reserve on yesterday's share only", map[string]any{budget.PathSpendRun(y, "aurora", "r1") + "/reserved": int64(5 * usd)})
	h.mustAllow(c, "spend: report against yesterday's counters", map[string]any{
		budget.PathRun("aurora", "r1") + "/spent":         int64(usd),
		budget.PathSpendRun(y, "aurora", "r1") + "/spent": int64(usd),
		budget.PathSpendRepo(y, "aurora") + "/spent":      int64(usd),
		budget.PathSpendGlobal(y) + "/spent":              int64(usd),
	})
	w.st.Run.Spent, w.st.DayRun.Spent = usd, usd
	h.mustAllow(c, "release the unspent $3 against yesterday", releaseUpdates(w, 3*usd))
	h.wantValue(budget.PathSpendGlobal(y)+"/counted", "1000000")
	// Two days old: nothing moves any more.
	w2 := mk()
	w2.day = today() - 2
	h.seed(w2)
	h.mustDeny(w2.client(h), "release against two days ago", releaseUpdates(w2, usd))
	h.mustDeny(w2.client(h), "report against two days ago", map[string]any{budget.PathSpendRepo(w2.day, "aurora") + "/spent": 1})
	// A release split across today's and yesterday's shares is one release.
	w3 := mk()
	h.seed(w3)
	h.patch(map[string]any{
		budget.PathSpendRun(today(), "aurora", "r1") + "/reserved": int64(2 * usd),
		budget.PathSpendRepo(today(), "aurora") + "/counted":       int64(2 * usd),
		budget.PathSpendGlobal(today()) + "/counted":               int64(2 * usd),
		budget.PathRun("aurora", "r1") + "/reserved":               int64(6 * usd),
	})
	h.mustAllow(w3.client(h), "release 1 on each day", map[string]any{
		budget.PathRun("aurora", "r1") + "/released":               int64(2 * usd),
		budget.PathSpendRun(y, "aurora", "r1") + "/released":       int64(usd),
		budget.PathSpendRepo(y, "aurora") + "/counted":             int64(3 * usd),
		budget.PathSpendGlobal(y) + "/counted":                     int64(3 * usd),
		budget.PathSpendRun(today(), "aurora", "r1") + "/released": int64(usd),
		budget.PathSpendRepo(today(), "aurora") + "/counted":       int64(1 * usd),
		budget.PathSpendGlobal(today()) + "/counted":               int64(1 * usd),
	})
	// The ledger releasing more than its shares did is refused.
	h.mustDeny(w3.client(h), "a ledger release with no share release", map[string]any{budget.PathRun("aurora", "r1") + "/released": int64(3 * usd)})
	h.mustDeny(w3.client(h), "a share release with no ledger release", map[string]any{
		budget.PathSpendRun(y, "aurora", "r1") + "/released": int64(2 * usd),
		budget.PathSpendRepo(y, "aurora") + "/counted":       int64(2 * usd),
		budget.PathSpendGlobal(y) + "/counted":               int64(2 * usd),
	})
}

func TestReleaseBoundedByOutstanding(t *testing.T) {
	h := newHarness(t)
	w := leased(4 * usd)
	w.st.Run.Spent = 3 * usd // 1 is unsettled
	h.seed(w)
	h.mustDeny(w.client(h), "release 2 with 1 unsettled", releaseUpdates(w, 2*usd))
	h.mustDeny(w.client(h), "release exactly outstanding although $3 of it was spent", releaseUpdates(w, 4*usd))
	h.mustAllow(w.client(h), "release the 1 unsettled", releaseUpdates(w, 1*usd))
	// Spending more than was reserved is not a report, it is an overrun.
	h.mustDeny(w.client(h), "spent beyond reserved", map[string]any{budget.PathRun("aurora", "r1") + "/spent": int64(4 * usd)})
	h.mustAllow(w.client(h), "overrun is its own field", map[string]any{budget.PathRun("aurora", "r1") + "/overrun": int64(usd)})
	// The day share bounds it too: the ledger holds 4, but this day's share only 1.
	h2 := newHarness(t)
	y := today() - 1
	w2 := stdWorld()
	w2.day = y
	w2.st = budget.State{Run: budget.RunLedger{Reserved: 4 * usd}, DayRun: budget.RunLedger{Reserved: usd}, Repo: budget.Counters{Counted: usd}, Global: budget.Counters{Counted: usd}}
	h2.seed(w2)
	h2.mustDeny(w2.client(h2), "release 2 against a share of 1", releaseUpdates(w2, 2*usd))
	h2.mustAllow(w2.client(h2), "release 1 against it", releaseUpdates(w2, usd))
	// No negative refunds, no counter below zero.
	h3 := newHarness(t)
	w3 := leased(2 * usd)
	h3.seed(w3)
	u := releaseUpdates(w3, 2*usd)
	u[budget.PathRun("aurora", "r1")+"/released"] = int64(-usd)
	h3.mustDeny(w3.client(h3), "a negative release", u)
	h3.patch(map[string]any{budget.PathRun("aurora", "r1") + "/released": int64(usd)})
	h3.mustDeny(w3.client(h3), "released lowered", map[string]any{budget.PathRun("aurora", "r1") + "/released": 0})
	// A fractional amount.
	h3.mustDeny(w3.client(h3), "a fractional release", map[string]any{budget.PathRun("aurora", "r1") + "/released": 1.5})
}

// TestConcurrentLeasesNeverExceedCaps: 50 runs of two repositories race to
// lease $1 at a time with the client's stale-versus-refused loop (read, ask
// Evaluate, write, on a denial re-read and decide), and no cap is ever passed.
func TestConcurrentLeasesNeverExceedCaps(t *testing.T) {
	h := newHarness(t)
	day := today()
	const perRun, repoCap, globalCap = 2, 12, 30
	w := stdWorld()
	w.caps = budget.Caps{
		Global:   &budget.GlobalCaps{DailyMicros: mp(globalCap * usd), PerRunMicros: mp(perRun * usd)},
		Defaults: &budget.DefaultCaps{RepoDailyMicros: mp(repoCap * usd), RepoPerRunMicros: mp(perRun * usd)},
		Limits:   &budget.Limits{MaxReserveMicros: mp(1 * usd)},
		Mode:     budget.ModeEnforce,
	}
	h.seed(w)
	slugs := []string{"alpha", "beta", "gamma"}
	type result struct {
		slug, run string
		granted   int
		stale     int
	}
	var wg sync.WaitGroup
	results := make(chan result, 50)
	for i := 0; i < 50; i++ {
		slug, run := slugs[i%3], fmt.Sprintf("run-%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := h.as(claims{slug: slug, run: run})
			r := result{slug: slug, run: run}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			for attempts := 0; attempts < 400; attempts++ {
				st, caps, kills, err := readState(ctx, c, slug, run, day)
				if err != nil {
					t.Errorf("%s/%s: reading the state: %v", slug, run, err)
					return
				}
				wr := budget.Write{Op: budget.OpLease, Day: day, Amount: usd}
				d := budget.Evaluate(st, wr, caps, kills, time.Now())
				if !d.Allow {
					break // a real refusal
				}
				cw := world{slug: slug, run: run, day: day, st: st}
				err = c.Patch(ctx, "", leaseUpdates(cw, usd))
				switch {
				case err == nil:
					r.granted++
				case errors.Is(err, rtdb.ErrPermission):
					r.stale++ // re-read and decide again
				default:
					t.Errorf("%s/%s: %v", slug, run, err)
					return
				}
				time.Sleep(time.Duration(rand.Intn(3)) * time.Millisecond)
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	granted, stale := 0, 0
	perSlug := map[string]int{}
	for r := range results {
		if r.granted > perRun {
			t.Errorf("%s/%s holds %d leases, per-run cap %d", r.slug, r.run, r.granted, perRun)
		}
		granted += r.granted
		stale += r.stale
		perSlug[r.slug] += r.granted
	}
	var global budget.Counters
	mustGet(t, h, budget.PathSpendGlobal(day), &global)
	if int(global.Counted/usd) != granted || granted > globalCap {
		t.Fatalf("global counted $%d for %d granted leases, cap %d", global.Counted/usd, granted, globalCap)
	}
	for _, s := range slugs {
		var rc budget.Counters
		mustGet(t, h, budget.PathSpendRepo(day, s), &rc)
		if int(rc.Counted/usd) != perSlug[s] || perSlug[s] > repoCap {
			t.Errorf("%s: counted $%d for %d granted, cap %d", s, rc.Counted/usd, perSlug[s], repoCap)
		}
	}
	if granted != globalCap {
		t.Errorf("only %d of the %d dollars of the global cap were handed out although 50 runs wanted $100", granted, globalCap)
	}
	t.Logf("%d granted, %d stale retries across 50 runs", granted, stale)
}
