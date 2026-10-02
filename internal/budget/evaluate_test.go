package budget

import (
	"math"
	"strings"
	"testing"
	"time"
)

const usd = Micros(1_000_000)

var evalNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func mp(v Micros) *Micros { return &v }

// base is the design's worked example (§6.4): global daily $150, repository
// daily $60, per-run $20, maxReserve $5, enforce; run A holds nothing yet.
func base() (State, Write, Caps, Kills) {
	today := Day(evalNow)
	return State{},
		Write{Op: OpLease, Day: today, Amount: 2 * usd},
		Caps{
			Global:   &GlobalCaps{DailyMicros: mp(150 * usd), PerRunMicros: mp(20 * usd)},
			Defaults: &DefaultCaps{RepoDailyMicros: mp(60 * usd), RepoPerRunMicros: mp(20 * usd)},
			Repo:     nil,
			Limits:   &Limits{MaxReserveMicros: mp(5 * usd)},
			Mode:     ModeEnforce,
		},
		Kills{}
}

func TestEvaluateTable(t *testing.T) {
	today := Day(evalNow)
	type tweak func(*State, *Write, *Caps, *Kills)
	for _, tc := range []struct {
		name   string
		tweak  tweak
		allow  bool
		reason Reason
		scope  Scope
		advise Reason // the cap an observe run was let through
	}{
		// Allowed.
		{"lease within every cap", nil, true, "", "", ""},
		{"lease exactly at maxReserve", func(_ *State, w *Write, _ *Caps, _ *Kills) { w.Amount = 5 * usd }, true, "", "", ""},
		{"lease to exactly the run cap", func(s *State, w *Write, _ *Caps, _ *Kills) {
			s.Run = RunLedger{Reserved: 18 * usd}
			w.Amount = 2 * usd
		}, true, "", "", ""},
		{"lease to exactly the repo daily cap", func(s *State, w *Write, _ *Caps, _ *Kills) { s.Repo.Counted = 58 * usd }, true, "", "", ""},
		{"lease to exactly the global daily cap", func(s *State, w *Write, _ *Caps, _ *Kills) { s.Global.Counted = 148 * usd }, true, "", "", ""},
		{"released money frees the run cap", func(s *State, w *Write, _ *Caps, _ *Kills) {
			s.Run = RunLedger{Reserved: 30 * usd, Released: 12 * usd, Spent: 10 * usd}
		}, true, "", "", ""},
		{"repo node overrides the default", func(s *State, w *Write, c *Caps, _ *Kills) {
			c.Repo = &RepoCaps{DailyMicros: mp(100 * usd), PerRunMicros: mp(20 * usd)}
			s.Repo.Counted = 90 * usd
		}, true, "", "", ""},
		{"kill switch present but off", func(_ *State, _ *Write, _ *Caps, k *Kills) {
			k.Global = &Kill{On: false}
			k.Repo = &Kill{On: false, By: "x"}
		}, true, "", "", ""},

		// Row 10: kill switches.
		{"global kill", func(_ *State, _ *Write, _ *Caps, k *Kills) { k.Global = &Kill{On: true} }, false, ReasonKillSwitch, ScopeGlobal, ""},
		{"repo kill", func(_ *State, _ *Write, _ *Caps, k *Kills) { k.Repo = &Kill{On: true} }, false, ReasonKillSwitch, ScopeRepo, ""},
		{"both kills name the global one", func(_ *State, _ *Write, _ *Caps, k *Kills) {
			k.Global = &Kill{On: true}
			k.Repo = &Kill{On: true}
		}, false, ReasonKillSwitch, ScopeGlobal, ""},
		{"kill beats a would-be cap refusal", func(s *State, _ *Write, _ *Caps, k *Kills) {
			k.Repo = &Kill{On: true}
			s.Repo.Counted = 60 * usd
		}, false, ReasonKillSwitch, ScopeRepo, ""},
		{"kill in observe still denies", func(_ *State, _ *Write, c *Caps, k *Kills) {
			c.Mode = ModeObserve
			k.Global = &Kill{On: true}
		}, false, ReasonKillSwitch, ScopeGlobal, ""},
		{"kill with an unknown mode still denies", func(_ *State, _ *Write, c *Caps, k *Kills) {
			c.Mode = ""
			k.Repo = &Kill{On: true}
		}, false, ReasonKillSwitch, ScopeRepo, ""},

		// Row 4: maxReserve.
		{"lease over maxReserve", func(_ *State, w *Write, _ *Caps, _ *Kills) { w.Amount = 5*usd + 1 }, false, ReasonMaxReserve, ScopeRun, ""},
		{"no limits node denies", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Limits = nil }, false, ReasonMaxReserve, ScopeRun, ""},
		{"maxReserve unset denies", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Limits = &Limits{} }, false, ReasonMaxReserve, ScopeRun, ""},
		{"maxReserve over in observe still denies", func(_ *State, w *Write, c *Caps, _ *Kills) {
			c.Mode = ModeObserve
			w.Amount = 6 * usd
		}, false, ReasonMaxReserve, ScopeRun, ""},

		// Row 5: the run's lifetime cap.
		{"over the run cap", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Run = RunLedger{Reserved: 18*usd + 1} }, false, ReasonRunCap, ScopeRun, ""},
		{"run cap is the lower of repo and global", func(s *State, _ *Write, c *Caps, _ *Kills) {
			c.Repo = &RepoCaps{DailyMicros: mp(60 * usd), PerRunMicros: mp(10 * usd)}
			s.Run = RunLedger{Reserved: 9 * usd}
		}, false, ReasonRunCap, ScopeRun, ""},
		{"global per-run lower than the repo's", func(s *State, _ *Write, c *Caps, _ *Kills) {
			c.Global.PerRunMicros = mp(8 * usd)
			s.Run = RunLedger{Reserved: 7 * usd}
		}, false, ReasonRunCap, ScopeRun, ""},
		{"run cap in observe is advisory", func(s *State, _ *Write, c *Caps, _ *Kills) {
			c.Mode = ModeObserve
			s.Run = RunLedger{Reserved: 19 * usd}
		}, true, "", "", ReasonRunCap},

		// Row 6: repository daily.
		{"over the repo daily cap", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Repo.Counted = 59 * usd }, false, ReasonRepoDailyCap, ScopeRepo, ""},
		{"repo cap from its node", func(s *State, _ *Write, c *Caps, _ *Kills) {
			c.Repo = &RepoCaps{DailyMicros: mp(10 * usd), PerRunMicros: mp(20 * usd)}
			s.Repo.Counted = 9 * usd
		}, false, ReasonRepoDailyCap, ScopeRepo, ""},
		{"repo daily in observe is advisory", func(s *State, _ *Write, c *Caps, _ *Kills) {
			c.Mode = ModeObserve
			s.Repo.Counted = 100 * usd
		}, true, "", "", ReasonRepoDailyCap},
		{"repo daily with the mode absent is advisory", func(s *State, _ *Write, c *Caps, _ *Kills) {
			c.Mode = ""
			s.Repo.Counted = 100 * usd
		}, true, "", "", ReasonRepoDailyCap},

		// Global daily.
		{"over the global daily cap", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Global.Counted = 149 * usd }, false, ReasonGlobalDailyCap, ScopeGlobal, ""},
		{"global daily in observe is advisory", func(s *State, _ *Write, c *Caps, _ *Kills) {
			c.Mode = ModeObserve
			s.Global.Counted = 500 * usd
		}, true, "", "", ReasonGlobalDailyCap},
		{"repo refusal is named before the global one", func(s *State, _ *Write, _ *Caps, _ *Kills) {
			s.Repo.Counted = 59 * usd
			s.Global.Counted = 149 * usd
		}, false, ReasonRepoDailyCap, ScopeRepo, ""},

		// Null caps deny (design §6.4: N <= null is false).
		{"no global caps node", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Global = nil }, false, ReasonNoCap, ScopeGlobal, ""},
		{"no global daily cap", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Global.DailyMicros = nil }, false, ReasonNoCap, ScopeGlobal, ""},
		{"no global per-run cap", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Global.PerRunMicros = nil }, false, ReasonNoCap, ScopeGlobal, ""},
		{"no repo cap and no defaults", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Defaults = nil }, false, ReasonNoCap, ScopeRepo, ""},
		{"defaults without a repo daily cap", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Defaults.RepoDailyMicros = nil }, false, ReasonNoCap, ScopeRepo, ""},
		{"defaults without a repo per-run cap", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Defaults.RepoPerRunMicros = nil }, false, ReasonNoCap, ScopeRepo, ""},
		{"nothing set at all", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Global, c.Defaults = nil, nil }, false, ReasonNoCap, ScopeRepo, ""},
		{"a zero cap is a cap, not a null", func(_ *State, _ *Write, c *Caps, _ *Kills) { c.Global.DailyMicros = mp(0) }, false, ReasonGlobalDailyCap, ScopeGlobal, ""},
		{"null caps are fine in observe", func(_ *State, _ *Write, c *Caps, _ *Kills) {
			c.Mode = ModeObserve
			c.Global, c.Defaults = nil, nil
		}, true, "", "", ReasonNoCap},

		// Day (A14, row 7).
		{"lease against yesterday", func(_ *State, w *Write, _ *Caps, _ *Kills) { w.Day = today - 1 }, false, ReasonNotToday, ScopeRun, ""},
		{"lease against tomorrow", func(_ *State, w *Write, _ *Caps, _ *Kills) { w.Day = today + 1 }, false, ReasonNotToday, ScopeRun, ""},
		{"lease against yesterday in observe still denies", func(_ *State, w *Write, c *Caps, _ *Kills) {
			c.Mode = ModeObserve
			w.Day = today - 1
		}, false, ReasonNotToday, ScopeRun, ""},

		// Overflow and corrupt state: never fail open.
		{"counter near MaxInt64 does not wrap", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Repo.Counted = math.MaxInt64 - 1 }, false, ReasonRepoDailyCap, ScopeRepo, ""},
		{"global counter near MaxInt64", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Global.Counted = math.MaxInt64 }, false, ReasonGlobalDailyCap, ScopeGlobal, ""},
		{"run reserved near MaxInt64", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Run.Reserved = math.MaxInt64 - 1 }, false, ReasonRunCap, ScopeRun, ""},
		{"huge amount cannot wrap a counter", func(s *State, w *Write, c *Caps, _ *Kills) {
			c.Limits.MaxReserveMicros = mp(math.MaxInt64)
			w.Amount = math.MaxInt64
			s.Repo.Counted = 10
		}, false, ReasonRunCap, ScopeRun, ""},
		{"negative repo counter grants no headroom", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Repo.Counted = -100 * usd }, false, ReasonInvalid, ScopeRepo, ""},
		{"negative global counter", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Global.Counted = -1 }, false, ReasonInvalid, ScopeGlobal, ""},
		{"negative reserved", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Run.Reserved = -usd }, false, ReasonInvalid, ScopeRun, ""},
		{"negative released", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.Run = RunLedger{Reserved: 10 * usd, Released: -5 * usd} }, false, ReasonInvalid, ScopeRun, ""},
		{"negative day share spent", func(s *State, _ *Write, _ *Caps, _ *Kills) { s.DayRun.Spent = -1 }, false, ReasonInvalid, ScopeRun, ""},
		{"negative state in observe still denies", func(s *State, _ *Write, c *Caps, _ *Kills) {
			c.Mode = ModeObserve
			s.Repo.Counted = -1
		}, false, ReasonInvalid, ScopeRepo, ""},
		{"release with a negative spent", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount = OpRelease, 3*usd
			s.Run = RunLedger{Reserved: 1 * usd, Spent: -2 * usd}
			s.DayRun = s.Run
		}, false, ReasonInvalid, ScopeRun, ""},
		{"release near MaxInt64 reserved", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount = OpRelease, 3*usd
			s.Run = RunLedger{Reserved: math.MaxInt64, Released: 0, Spent: math.MaxInt64}
			s.DayRun = s.Run
		}, false, ReasonOverRelease, ScopeRun, ""},

		// Amount.
		{"zero lease", func(_ *State, w *Write, _ *Caps, _ *Kills) { w.Amount = 0 }, false, ReasonInvalid, ScopeRun, ""},
		{"negative lease", func(_ *State, w *Write, _ *Caps, _ *Kills) { w.Amount = -1 }, false, ReasonInvalid, ScopeRun, ""},
		{"unknown op", func(_ *State, w *Write, _ *Caps, _ *Kills) { w.Op = 0 }, false, ReasonInvalid, ScopeRun, ""},

		// Releases (row 2): bounded by the run's own outstanding, never by caps or kills.
		{"release what is unspent", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount = OpRelease, 3*usd
			s.Run = RunLedger{Reserved: 5 * usd, Spent: 2 * usd}
			s.DayRun = s.Run
		}, true, "", "", ""},
		{"release more than outstanding", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount = OpRelease, 3*usd+1
			s.Run = RunLedger{Reserved: 5 * usd, Spent: 2 * usd}
			s.DayRun = s.Run
		}, false, ReasonOverRelease, ScopeRun, ""},
		{"release counts earlier releases", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount = OpRelease, 2*usd
			s.Run = RunLedger{Reserved: 5 * usd, Released: 2 * usd, Spent: 2 * usd}
			s.DayRun = s.Run
		}, false, ReasonOverRelease, ScopeRun, ""},
		{"release while only the day share is short", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount = OpRelease, 2*usd
			s.Run = RunLedger{Reserved: 10 * usd}
			s.DayRun = RunLedger{Reserved: 1 * usd}
		}, false, ReasonOverRelease, ScopeRun, ""},
		{"release against yesterday", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount, w.Day = OpRelease, 1*usd, today-1
			s.Run = RunLedger{Reserved: 5 * usd}
			s.DayRun = s.Run
		}, true, "", "", ""},
		{"release against the day before yesterday", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount, w.Day = OpRelease, 1*usd, today-2
			s.Run = RunLedger{Reserved: 5 * usd}
			s.DayRun = s.Run
		}, false, ReasonOldDay, ScopeRun, ""},
		{"release against tomorrow", func(s *State, w *Write, _ *Caps, _ *Kills) {
			w.Op, w.Amount, w.Day = OpRelease, 1*usd, today+1
			s.Run = RunLedger{Reserved: 5 * usd}
			s.DayRun = s.Run
		}, false, ReasonOldDay, ScopeRun, ""},
		{"release ignores kills, caps and maxReserve", func(s *State, w *Write, c *Caps, k *Kills) {
			w.Op, w.Amount = OpRelease, 8*usd
			s.Run = RunLedger{Reserved: 8 * usd}
			s.DayRun = s.Run
			k.Global, k.Repo = &Kill{On: true}, &Kill{On: true}
			c.Global, c.Defaults, c.Limits = nil, nil, nil
			s.Repo.Counted, s.Global.Counted = 999*usd, 999*usd
		}, true, "", "", ""},
		{"zero release", func(s *State, w *Write, _ *Caps, _ *Kills) { w.Op, w.Amount = OpRelease, 0 }, false, ReasonInvalid, ScopeRun, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, c, k := base()
			if tc.tweak != nil {
				tc.tweak(&s, &w, &c, &k)
			}
			d := Evaluate(s, w, c, k, evalNow)
			if d.Allow != tc.allow || d.Reason != tc.reason || d.Scope != tc.scope {
				t.Fatalf("Evaluate = %+v, want allow=%v reason=%q scope=%q", d, tc.allow, tc.reason, tc.scope)
			}
			if !d.Allow && d.Detail == "" {
				t.Errorf("a refusal needs a detail: %+v", d)
			}
			if tc.advise == "" && d.Advisory != nil {
				t.Errorf("unexpected advisory %+v", d.Advisory)
			}
			if tc.advise != "" {
				if d.Advisory == nil || d.Advisory.Reason != tc.advise {
					t.Errorf("advisory = %+v, want %q", d.Advisory, tc.advise)
				}
			}
		})
	}
}

func TestEvaluateCommittedDayCap(t *testing.T) {
	for _, tc := range []struct {
		name      string
		committed Micros
		counted   Micros
		mode      string
		allow     bool
		detail    string
	}{
		{"no committed cap", 0, 58 * usd, ModeEnforce, true, ""},
		{"under", 10 * usd, 7 * usd, ModeEnforce, true, ""},
		{"exactly at the cap", 10 * usd, 8 * usd, ModeEnforce, true, ""},
		{"over by one micro", 10 * usd, 8*usd + 1, ModeEnforce, false, "fugaro.yaml"},
		{"tighter than the RTDB cap wins", 5 * usd, 4 * usd, ModeEnforce, false, "fugaro.yaml"},
		{"looser than the RTDB cap changes nothing", 100 * usd, 59 * usd, ModeEnforce, false, ""}, // RTDB repo daily $60 refuses
		{"observe makes it advisory", 10 * usd, 9 * usd, ModeObserve, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, c, k := base()
			s.Repo.Counted = tc.counted
			c.Mode = tc.mode
			w.CommittedDaily = tc.committed
			d := Evaluate(s, w, c, k, evalNow)
			if d.Allow != tc.allow {
				t.Fatalf("Evaluate = %+v, want allow=%v", d, tc.allow)
			}
			if !tc.allow {
				if d.Reason != ReasonRepoDailyCap || d.Scope != ScopeRepo {
					t.Errorf("reason/scope = %q/%q", d.Reason, d.Scope)
				}
				if tc.detail != "" && !strings.Contains(d.Detail, tc.detail) {
					t.Errorf("detail %q does not name %s", d.Detail, tc.detail)
				}
				if tc.detail == "" && strings.Contains(d.Detail, "fugaro.yaml") {
					t.Errorf("the RTDB cap refused, detail %q must not blame fugaro.yaml", d.Detail)
				}
			}
		})
	}
	// The committed cap speaks for the repository's counter only, never the
	// project's: a global counter far above it does not matter.
	s, w, c, k := base()
	w.CommittedDaily = 10 * usd
	s.Global.Counted = 140 * usd
	if d := Evaluate(s, w, c, k, evalNow); !d.Allow {
		t.Fatalf("committed cap leaked into the global counter: %+v", d)
	}
	// And it never applies to releases.
	s, w, c, k = base()
	w.Op, w.Amount, w.CommittedDaily = OpRelease, usd, usd
	s.Run = RunLedger{Reserved: 2 * usd}
	s.DayRun = s.Run
	s.Repo.Counted = 50 * usd
	if d := Evaluate(s, w, c, k, evalNow); !d.Allow {
		t.Fatalf("release refused by the committed cap: %+v", d)
	}
}

// The day boundary: a lease is today's only, judged by the instant given.
func TestEvaluateDayBoundary(t *testing.T) {
	s, w, c, k := base()
	justBefore := time.Date(2026, 10, 2, 23, 59, 59, 999e6, time.UTC)
	justAfter := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	w.Day = Day(justBefore)
	if d := Evaluate(s, w, c, k, justBefore); !d.Allow {
		t.Fatalf("lease on today at 23:59:59.999: %+v", d)
	}
	if d := Evaluate(s, w, c, k, justAfter); d.Allow || d.Reason != ReasonNotToday {
		t.Fatalf("lease on yesterday at 00:00:00.000: %+v", d)
	}
	w.Op = OpRelease
	s.Run = RunLedger{Reserved: 2 * usd}
	s.DayRun = s.Run
	if d := Evaluate(s, w, c, k, justAfter); !d.Allow {
		t.Fatalf("release on yesterday just after midnight: %+v", d)
	}
}

// Evaluate never lets a lease through that the caps refuse, for any mix of
// states (a cheap property pinning the "never fail-open" invariant).
func TestEvaluateNeverExceedsEnforcedCaps(t *testing.T) {
	for run := Micros(0); run <= 25*usd; run += 5 * usd {
		for repo := Micros(0); repo <= 70*usd; repo += 10 * usd {
			for glob := Micros(0); glob <= 160*usd; glob += 20 * usd {
				for amt := Micros(1); amt <= 5*usd; amt += usd {
					s, w, c, k := base()
					s.Run.Reserved, s.Repo.Counted, s.Global.Counted, w.Amount = run, repo, glob, amt
					d := Evaluate(s, w, c, k, evalNow)
					over := run+amt > 20*usd || repo+amt > 60*usd || glob+amt > 150*usd
					if d.Allow == over {
						t.Fatalf("run=%d repo=%d glob=%d amt=%d: allow=%v but over=%v", run, repo, glob, amt, d.Allow, over)
					}
				}
			}
		}
	}
}

// The committed cap is the repository's own guardrail: it bites when the run's
// effective mode (strictest of the project's RTDB mode and the repository's
// own) is enforce, while RTDB-side caps follow the RTDB mode alone.
func TestEvaluateCommittedEnforceFollowsEffectiveMode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rtdbMode     string
		repoEnforce  bool
		counted      Micros
		wantAllow    bool
		wantAdvisory Reason
	}{
		{"both enforce", ModeEnforce, true, 9 * usd, false, ""},
		{"repo enforces under an observe project: committed bites", ModeObserve, true, 9 * usd, false, ""},
		{"repo enforces, project mode absent", "", true, 9 * usd, false, ""},
		{"neither enforces: advisory", ModeObserve, false, 9 * usd, true, ReasonRepoDailyCap},
		{"project enforces, repo observes: committed still bites", ModeEnforce, false, 9 * usd, false, ""},
		{"repo enforces and the cap holds", ModeObserve, true, 8 * usd, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, c, k := base()
			c.Mode = tc.rtdbMode
			w.CommittedDaily, w.CommittedEnforce = 10*usd, tc.repoEnforce
			s.Repo.Counted = tc.counted
			d := Evaluate(s, w, c, k, evalNow)
			if d.Allow != tc.wantAllow {
				t.Fatalf("%+v", d)
			}
			if !d.Allow && (d.Reason != ReasonRepoDailyCap || !strings.Contains(d.Detail, "fugaro.yaml")) {
				t.Errorf("refusal = %+v", d)
			}
			if tc.wantAdvisory != "" && (d.Advisory == nil || d.Advisory.Reason != tc.wantAdvisory) {
				t.Errorf("advisory = %+v", d.Advisory)
			}
		})
	}
	// An RTDB-side cap stays advisory under an observe project even when the
	// repository enforces its own committed cap.
	s, w, c, k := base()
	c.Mode = ModeObserve
	w.CommittedDaily, w.CommittedEnforce = 100*usd, true
	s.Repo.Counted = 100 * usd // over the RTDB repo cap of $60, under the committed $100? no: 102 > 100
	s.Repo.Counted = 90 * usd
	d := Evaluate(s, w, c, k, evalNow)
	if !d.Allow || d.Advisory == nil || d.Advisory.Reason != ReasonRepoDailyCap {
		t.Fatalf("RTDB cap must stay advisory: %+v", d)
	}
}
