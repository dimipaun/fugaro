package budget

import (
	"fmt"
	"time"
)

// Op is the kind of write Evaluate judges.
type Op int

const (
	// OpLease reserves Amount more for the run: the run's lifetime and
	// day-share `reserved`, and the repository and global day counters, each
	// rise by exactly Amount.
	OpLease Op = iota + 1
	// OpRelease gives Amount of the run's unspent reservation back: `released`
	// rises and both counters fall by exactly Amount.
	OpRelease
)

// Reason says why Evaluate refuses. The values that can halt a run are the
// halt reasons of result.json (kill_switch, run_cap, repo_daily_cap,
// global_daily_cap, no_cap); the others describe a write that is malformed
// for this moment (a client bug, midnight) and never a spend decision.
type Reason string

const (
	ReasonKillSwitch     Reason = "kill_switch"
	ReasonRunCap         Reason = "run_cap"
	ReasonRepoDailyCap   Reason = "repo_daily_cap"
	ReasonGlobalDailyCap Reason = "global_daily_cap"
	// ReasonNoCap: a cap the rules compare against is not set, so the rules
	// deny (a missing node reads null; N <= null is false).
	ReasonNoCap Reason = "no_cap"
	// ReasonMaxReserve: the lease is over limits.maxReserveMicros, or that
	// limit is not set.
	ReasonMaxReserve Reason = "max_reserve"
	// ReasonNotToday: a lease names a day other than the current UTC day.
	// The caller takes the lease again against today's nodes.
	ReasonNotToday Reason = "not_today"
	// ReasonOldDay: a release names a day before yesterday (or in the future).
	ReasonOldDay Reason = "old_day"
	// ReasonOverRelease: a release larger than the run still holds unspent.
	ReasonOverRelease Reason = "release_exceeds_outstanding"
	// ReasonInvalid: a non-positive amount or an unknown Op.
	ReasonInvalid Reason = "invalid_write"
)

// Scope is the extent a refusal speaks for; the same values as halt.scope.
type Scope string

const (
	ScopeRun    Scope = "run"
	ScopeRepo   Scope = "repo"
	ScopeGlobal Scope = "global"
)

// State is what a write is judged against: the database as read just before
// the write, for the day the write names (Write.Day).
type State struct {
	Run    RunLedger // /runs/<slug>/<run>, the lifetime ledger
	DayRun RunLedger // /spend/<Write.Day>/runs/<slug>/<run>, the run's share of that day
	Repo   Counters  // /spend/<Write.Day>/repos/<slug>
	Global Counters  // /spend/<Write.Day>/global
}

// Write is one lease or release the run would make.
type Write struct {
	Op     Op
	Day    int64 // the day node written: Day(now) for a lease; today or yesterday for a release
	Amount Micros
	// CommittedDaily is the committed per_day_usd of fugaro.yaml (0: none).
	// The rules cannot see git, so only the client enforces it, against the
	// repository's day counter alone (ruling R1). It never applies to a
	// release, and is advisory in observe like every cap.
	CommittedDaily Micros
}

// Decision is Evaluate's verdict.
type Decision struct {
	Allow  bool
	Reason Reason // set when !Allow
	Scope  Scope  // set when !Allow
	Detail string // set when !Allow: human text, in dollars
	// Advisory is, on an allowed lease, the cap refusal the project's
	// observe mode let through (nil otherwise): the would-be halt to log.
	Advisory *Decision
}

// Evaluate is the pure mirror of the database rules (design §6.4) for a
// lease or a release by one run. It answers "would a correctly formed write of
// w.Amount be accepted against this state?"; the delta equations (a counter
// moves by exactly the run's own delta) are what make a stale read fail in
// the database and are not part of the state predicate, and the run's
// identity and fx deadline are authentication, not state.
//
// A lease is refused when, in this order: a switch is on (global, then the
// repository's; honoured in every mode); the day is not today; the amount is
// over maxReserve (a missing limit refuses); and, only when caps.Mode is
// "enforce" (anything else, absent included, is observe), the run's lifetime
// reservation (reserved - released) would pass min(repository per-run cap or
// default, global per-run cap), the repository's day counter would pass its
// cap (or default) or the committed daily cap, or the global day counter
// would pass the global cap; a cap that is not set refuses with ReasonNoCap.
// A cap equal to the new value passes. In observe the first such cap
// refusal is returned as Decision.Advisory on an allowed decision.
//
// A release is refused when the day is older than yesterday or in the future,
// or the amount exceeds what the run holds unspent (reserved - released -
// spent) in its lifetime ledger or in the day's share. Kill switches and caps
// never stop a release: giving money back is always safe.
func Evaluate(st State, w Write, caps Caps, kills Kills, now time.Time) Decision {
	if w.Amount <= 0 || (w.Op != OpLease && w.Op != OpRelease) {
		return deny(ReasonInvalid, ScopeRun, "amount %s must be positive and the operation a lease or a release", dollars(w.Amount))
	}
	today := Day(now)
	if w.Op == OpRelease {
		if w.Day != today && w.Day != today-1 {
			return deny(ReasonOldDay, ScopeRun, "a release may name today or yesterday, not day %d (today is %d)", w.Day, today)
		}
		if w.Amount > st.Run.Unsettled() {
			return deny(ReasonOverRelease, ScopeRun, "release %s exceeds the %s the run holds unspent", dollars(w.Amount), dollars(st.Run.Unsettled()))
		}
		if w.Amount > st.DayRun.Unsettled() {
			return deny(ReasonOverRelease, ScopeRun, "release %s exceeds the %s the run holds unspent on day %d", dollars(w.Amount), dollars(st.DayRun.Unsettled()), w.Day)
		}
		return Decision{Allow: true}
	}

	if kills.Global != nil && kills.Global.On {
		return deny(ReasonKillSwitch, ScopeGlobal, "the global kill switch is on%s", killNote(*kills.Global))
	}
	if kills.Repo != nil && kills.Repo.On {
		return deny(ReasonKillSwitch, ScopeRepo, "this repository's kill switch is on%s", killNote(*kills.Repo))
	}
	if w.Day != today {
		return deny(ReasonNotToday, ScopeRun, "a lease names the current UTC day %d, not %d", today, w.Day)
	}
	maxReserve, ok := caps.MaxReserve()
	if !ok {
		return deny(ReasonMaxReserve, ScopeRun, "limits.maxReserveMicros is not set")
	}
	if w.Amount > maxReserve {
		return deny(ReasonMaxReserve, ScopeRun, "lease %s is over the largest single lease, %s", dollars(w.Amount), dollars(maxReserve))
	}

	var refusal *Decision
	check := func(d Decision) {
		if refusal == nil {
			refusal = &d
		}
	}
	// Per-run: the lifetime outstanding after the lease.
	switch perRun, ok := caps.PerRun(); {
	case !ok:
		check(noCap(caps, "per-run"))
	case st.Run.Outstanding()+w.Amount > perRun:
		check(deny(ReasonRunCap, ScopeRun, "run cap %s reached (%s held, %s needed)", dollars(perRun), dollars(st.Run.Outstanding()), dollars(w.Amount)))
	}
	// The repository's day counter, against the database cap, then the
	// committed one.
	switch cap, ok := caps.RepoDaily(); {
	case !ok:
		check(Decision{Reason: ReasonNoCap, Scope: ScopeRepo, Detail: "no daily cap is set for this repository and there is no default (fugaro budget set --defaults --repo-daily)"})
	case st.Repo.Counted+w.Amount > cap:
		check(deny(ReasonRepoDailyCap, ScopeRepo, "this repository's daily cap %s reached (%s counted, %s needed)", dollars(cap), dollars(st.Repo.Counted), dollars(w.Amount)))
	}
	if w.CommittedDaily > 0 && st.Repo.Counted+w.Amount > w.CommittedDaily {
		check(deny(ReasonRepoDailyCap, ScopeRepo, "per_day_usd %s of fugaro.yaml reached (%s counted today, %s needed)", dollars(w.CommittedDaily), dollars(st.Repo.Counted), dollars(w.Amount)))
	}
	switch cap, ok := caps.GlobalDaily(); {
	case !ok:
		check(Decision{Reason: ReasonNoCap, Scope: ScopeGlobal, Detail: "no global daily cap is set (fugaro budget set --global --daily)"})
	case st.Global.Counted+w.Amount > cap:
		check(deny(ReasonGlobalDailyCap, ScopeGlobal, "the project's daily cap %s reached (%s counted, %s needed)", dollars(cap), dollars(st.Global.Counted), dollars(w.Amount)))
	}
	if refusal == nil {
		return Decision{Allow: true}
	}
	if caps.Enforcing() {
		return *refusal
	}
	return Decision{Allow: true, Advisory: refusal}
}

// noCap names which side of the per-run cap is missing.
func noCap(c Caps, what string) Decision {
	repoSide := (c.Repo != nil && c.Repo.PerRunMicros != nil) || (c.Defaults != nil && c.Defaults.RepoPerRunMicros != nil)
	if !repoSide {
		return Decision{Reason: ReasonNoCap, Scope: ScopeRepo, Detail: "no " + what + " cap is set for this repository and there is no default (fugaro budget set --defaults --repo-per-run)"}
	}
	return Decision{Reason: ReasonNoCap, Scope: ScopeGlobal, Detail: "no global " + what + " cap is set (fugaro budget set --global --per-run)"}
}

func deny(r Reason, s Scope, format string, args ...any) Decision {
	return Decision{Reason: r, Scope: s, Detail: fmt.Sprintf(format, args...)}
}

func killNote(k Kill) string {
	switch {
	case k.By != "" && k.Reason != "":
		return " (" + k.By + ": " + k.Reason + ")"
	case k.By != "":
		return " (" + k.By + ")"
	case k.Reason != "":
		return " (" + k.Reason + ")"
	}
	return ""
}

func dollars(m Micros) string { return fmt.Sprintf("$%.2f", m.USD()) }
