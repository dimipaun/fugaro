package budget

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Lease sizes (design §5.3): 5% of the smallest headroom, between these.
const (
	minLease Micros = 250_000
	maxLease Micros = 2_000_000
)

// Per-write bounds baked into the rules (internal/budget/rules).
const (
	maxTokensPerWrite = 1_000_000_000
	maxCallsPerWrite  = 10_000
	maxModelKey       = 100
)

// leaseSource is the gateway.Lease of one run.
type leaseSource struct{ s *Session }

func (l *leaseSource) Grant(ctx context.Context, need Micros) (Micros, error) {
	var (
		amount Micros
		err    error
	)
	l.s.safely("lease", func() { amount, err = l.s.grant(ctx, need) })
	if err == nil && amount <= 0 {
		err = errors.New("budget: the lease panicked")
	}
	return amount, err
}

func (l *leaseSource) Report(ctx context.Context, rep gateway.StageReport) error {
	var err error
	l.s.safely("report", func() { err = l.s.report(ctx, rep) })
	return err
}

func (l *leaseSource) Release(ctx context.Context, unused Micros) error {
	var err error
	l.s.safely("release", func() { err = l.s.release(ctx, unused) })
	return err
}

func refusal(d Decision) *gateway.Refusal {
	return &gateway.Refusal{Reason: string(d.Reason), Scope: string(d.Scope), Detail: d.Detail}
}

// sleep waits d, or returns the context error / the grace's expiry.
func (s *Session) sleep(ctx context.Context, d time.Duration) error {
	return s.wait(ctx, d)
}

// jitter is the wait before the n-th retry of a denied write: the base grows
// with n and is spread out so that runs which collided do not collide again.
func (s *Session) jitter(n int) time.Duration {
	base := s.cfg.StaleBackoff * time.Duration(n)
	return base/2 + time.Duration(rand.Int64N(int64(base)+1))
}

// policyBinds reports whether the committed caps of fugaro.yaml (per run and
// per day) bind: when the run's own merged mode or the project's is enforce.
// A job-level observe must not loosen what the repository committed under an
// enforcing project.
func (s *Session) policyBinds(c Caps) bool { return s.cfg.LocalEnforce || c.Enforcing() }

// leaseSize picks the size of the next lease: clamp(5% of the smallest
// headroom, $0.25, $2.00), never less than need, never more than the largest
// single lease; when that size fits no cap but need does, need itself.
func (s *Session) leaseSize(need Micros, sn Snapshot, st State) Micros {
	head := Micros(math.MaxInt64)
	use := func(h Micros) {
		if h < head {
			head = max(h, 0)
		}
	}
	if sn.Caps.Enforcing() {
		if v, ok := sn.Caps.PerRun(); ok {
			use(v - st.Run.Outstanding())
		}
		if v, ok := sn.Caps.RepoDaily(); ok {
			use(v - st.Repo.Counted)
		}
		if v, ok := sn.Caps.GlobalDaily(); ok {
			use(v - st.Global.Counted)
		}
	}
	if s.policyBinds(sn.Caps) {
		if s.cfg.CommittedDaily > 0 {
			use(s.cfg.CommittedDaily - st.Repo.Counted)
		}
		if s.cfg.PolicyCap > 0 {
			use(s.cfg.PolicyCap - st.Run.Outstanding())
		}
	}
	l := min(max(head/20, minLease), maxLease)
	l = max(l, need)
	if l > head && need <= head {
		l = need
	}
	if m, ok := sn.Caps.MaxReserve(); ok {
		l = min(l, m)
	}
	return l
}

// grant takes a lease of at least need (R3). It reads the run's slice of the
// database, judges the write with Evaluate and sends it as one atomic
// multi-path update of absolute values: if another run moved a counter in
// between, the rules deny it and the next round re-reads. Whether a denial was
// stale or a real refusal is what Evaluate says about the fresh read.
func (s *Session) grant(ctx context.Context, need Micros) (Micros, error) {
	waited := false
	stale := 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		sn, err := s.readConfig(ctx)
		if err != nil {
			return 0, s.observe("lease", err)
		}
		now := s.now()
		day := Day(now)
		s.wmu.Lock()
		amount, rf, again, err := s.tryGrant(ctx, need, sn, day, now, &waited)
		s.wmu.Unlock()
		switch {
		case err != nil:
			return 0, s.observe("lease", err)
		case rf != nil:
			// A refusal is the budget answering, not an outage.
			s.dbOK()
			return 0, rf
		case again == retryWait:
			if err := s.sleep(ctx, s.cfg.CapRetryWait); err != nil {
				return 0, err
			}
			continue
		case again == retryStale:
			stale++
			if stale >= maxStale {
				return 0, s.observe("lease", fmt.Errorf("budget: %d lease writes in a row were denied although the rules' conditions held on a fresh read", stale))
			}
			if err := s.sleep(ctx, s.jitter(stale)); err != nil {
				return 0, err
			}
			continue
		}
		s.dbOK()
		return amount, nil
	}
}

type retry int

const (
	noRetry retry = iota
	retryWait
	retryStale
)

// tryGrant is one round of grant under the write lock.
func (s *Session) tryGrant(ctx context.Context, need Micros, sn Snapshot, day int64, now time.Time, waited *bool) (amount Micros, rf *gateway.Refusal, again retry, err error) {
	st, err := s.readState(ctx, day)
	if err != nil {
		return 0, nil, noRetry, err
	}
	amount = s.leaseSize(need, sn, st)
	w := Write{Op: OpLease, Day: day, Amount: amount, CommittedDaily: s.cfg.CommittedDaily, CommittedEnforce: s.cfg.LocalEnforce}
	d := Evaluate(st, w, sn.Caps, sn.Kills, now)
	if !d.Allow {
		switch d.Reason {
		case ReasonNotToday:
			return 0, nil, retryStale, nil
		case ReasonKillSwitch:
			return 0, refusal(d), noRetry, nil
		case ReasonRepoDailyCap, ReasonGlobalDailyCap:
			// Other runs' releases may land in a moment: wait once before
			// halting on a day cap.
			if !*waited {
				*waited = true
				return 0, nil, retryWait, nil
			}
			return 0, refusal(d), noRetry, nil
		case ReasonMaxReserve:
			// The lease was clamped to the limit, so the limit is unset.
			return 0, &gateway.Refusal{Reason: string(ReasonNoCap), Scope: string(ScopeGlobal), Detail: d.Detail}, noRetry, nil
		case ReasonRunCap, ReasonNoCap:
			return 0, refusal(d), noRetry, nil
		}
		return 0, nil, noRetry, fmt.Errorf("budget: the lease predicate rejected a write it sized itself (%s): %s", d.Reason, d.Detail)
	}
	if d.Advisory != nil {
		s.advise(d.Advisory)
	}
	// The run's own per-run cap from the policy merge, which the rules
	// cannot see.
	if c := s.cfg.PolicyCap; c > 0 && add(st.Run.Outstanding(), amount) > c {
		msg := fmt.Sprintf("run cap %s reached (%s held, %s needed)", dollars(c), dollars(st.Run.Outstanding()), dollars(amount))
		if s.policyBinds(sn.Caps) {
			return 0, &gateway.Refusal{Reason: string(ReasonRunCap), Scope: string(ScopeRun), Detail: msg}, noRetry, nil
		}
		s.advise(&Decision{Reason: ReasonRunCap, Scope: ScopeRun, Detail: msg})
	}
	upd := map[string]any{
		PathRun(s.cfg.Slug, s.cfg.Run) + "/reserved":           add(st.Run.Reserved, amount),
		PathSpendRun(day, s.cfg.Slug, s.cfg.Run) + "/reserved": add(st.DayRun.Reserved, amount),
		PathSpendRepo(day, s.cfg.Slug) + "/counted":            add(st.Repo.Counted, amount),
		PathSpendGlobal(day) + "/counted":                      add(st.Global.Counted, amount),
	}
	if err := s.db.Patch(ctx, "", upd); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return 0, nil, retryStale, nil
		}
		return 0, nil, noRetry, err
	}
	s.mu.Lock()
	s.leased[day] = true
	s.granted = add(s.granted, amount)
	s.mu.Unlock()
	s.log.Info("budget: lease granted", "amount", dollars(amount), "needed", dollars(need), "day", day,
		"run_outstanding", dollars(add(st.Run.Outstanding(), amount)), "repo_counted", dollars(add(st.Repo.Counted, amount)), "global_counted", dollars(add(st.Global.Counted, amount)))
	return amount, nil, noRetry, nil
}

// advise logs a cap refusal that observe mode let through, once per reason.
func (s *Session) advise(d *Decision) {
	s.mu.Lock()
	if s.advised == nil {
		s.advised = map[Reason]bool{}
	}
	first := !s.advised[d.Reason]
	s.advised[d.Reason] = true
	s.mu.Unlock()
	if first {
		s.log.Warn("budget: would halt (observe)", "reason", string(d.Reason), "scope", string(d.Scope), "detail", d.Detail)
	}
}

// pending is what the run has to report and has not.
type pending struct {
	calls    int64
	tokens   int64
	overrun  Micros
	byModel  map[string]Micros   // gateway runs: settled spend by serving model
	byTier   map[string]Micros   // cascade runs: settled money by the tier that paid for it (C1.16)
	notional Micros              // a subscription's figure: Claude Code's own, or the gateway's tier A (C1.16)
	nby      map[string]ModelUse // oauth runs: usage by model
}

func (p pending) empty() bool {
	return p.calls == 0 && p.tokens == 0 && p.overrun == 0 && len(p.byModel) == 0 &&
		len(p.byTier) == 0 && p.notional == 0 && len(p.nby) == 0
}

func (p pending) clone() pending {
	q := p
	q.byModel = map[string]Micros{}
	for k, v := range p.byModel {
		q.byModel[k] = v
	}
	q.byTier = map[string]Micros{}
	for k, v := range p.byTier {
		q.byTier[k] = v
	}
	q.nby = map[string]ModelUse{}
	for k, v := range p.nby {
		q.nby[k] = v
	}
	return q
}

// tierKey maps the tier a stage report carries to the database's spelling:
// the credentials service's answer says "B" and "C" (the gateway's TierB and
// TierC, C1.15) and the rules' byTier node says "b" and "c". ok is false for
// anything else, and the split drops such a tier rather than guessing at it.
//
// It is the untrusted-input end of the same rule the path builders enforce by
// panicking (a tier they have no leaf for) and planByTier enforces by asking
// for b and c and nothing else. Two things make the check worth its line: a
// tier that is not the contract must never become a key the database is asked
// to hold, and a key nothing ever writes would sit in the run's pending
// figures for the rest of the run, keeping it from ever reporting itself
// settled (empty) -- reads for a row that can never be written. The money is
// in the totals either way: only the display split loses a row.
func tierKey(tier string) (string, bool) {
	k := strings.ToLower(tier)
	return k, k == TierB || k == TierC
}

// modelName makes a model name usable as a key.
func modelName(m string) string {
	if m == "" {
		return "unknown"
	}
	if len(Key(m)) > maxModelKey {
		return "other"
	}
	return m
}

// report queues a finished stage's figures and sends them.
func (s *Session) report(ctx context.Context, rep gateway.StageReport) error {
	s.mu.Lock()
	s.pend.calls += int64(rep.Calls)
	s.pend.tokens += rep.Tokens
	s.pend.overrun = add(s.pend.overrun, rep.Overrun)
	if s.pend.byModel == nil {
		s.pend.byModel = map[string]Micros{}
	}
	for m, v := range rep.ByModel {
		s.pend.byModel[modelName(m)] = add(s.pend.byModel[modelName(m)], v)
	}
	// The split of the settled money by the tier that paid for it (C1.16,
	// D140). Only a call that went through the credentials service carries a
	// tier, so only a cascade run reports one and no other run writes a
	// byTier leaf: that is what "written only by a cascade run" means here.
	for t, v := range rep.ByTier {
		k, ok := tierKey(t)
		if !ok || v <= 0 {
			continue
		}
		if s.pend.byTier == nil {
			s.pend.byTier = map[string]Micros{}
		}
		s.pend.byTier[k] = add(s.pend.byTier[k], v)
	}
	// A tier-A call is the run's subscription, not money (D140): it settles
	// as notional, which is the figure an oauth stage's own total_cost_usd
	// settles as (AddNotional), so it lands on the same leaves and feeds no
	// cap. notionalTotal is not raised here: it is what the registry shows a
	// run whose spend nobody else reports, and a gateway run's entry shows
	// the gateway's own settled figure instead (snapshotEntry).
	s.pend.notional = add(s.pend.notional, rep.Notional)
	s.mu.Unlock()
	return s.flush(ctx, nil, "report")
}

// AddNotional queues an oauth stage's notional spend (Claude Code's
// total_cost_usd) and usage by model, and sends it. It is increase-only and
// feeds no cap (R7).
func (s *Session) AddNotional(ctx context.Context, notional Micros, byModel map[string]ModelUse) error {
	s.mu.Lock()
	s.pend.notional = add(s.pend.notional, notional)
	s.notionalTotal = add(s.notionalTotal, notional)
	if s.pend.nby == nil {
		s.pend.nby = map[string]ModelUse{}
	}
	for m, u := range byModel {
		k := modelName(m)
		c := s.pend.nby[k]
		c.Micros, c.In, c.Out, c.CR, c.CW = add(c.Micros, u.Micros), c.In+u.In, c.Out+u.Out, c.CR+u.CR, c.CW+u.CW
		s.pend.nby[k] = c
	}
	s.mu.Unlock()
	var err error
	s.safely("notional", func() { err = s.flush(ctx, nil, "report") })
	return err
}

// plan reads what a report needs and builds the update of one round. took is
// what the update carries, to be taken off the pending figures once it has
// landed; more says a bound cut it short.
func (s *Session) plan(ctx context.Context) (upd map[string]any, took pending, more bool, err error) {
	s.mu.Lock()
	p := s.pend.clone()
	used := s.used
	caps := s.caps
	leased := map[int64]bool{}
	for d := range s.leased {
		leased[d] = true
	}
	s.mu.Unlock()
	upd = map[string]any{}
	took = pending{byModel: map[string]Micros{}, byTier: map[string]Micros{}, nby: map[string]ModelUse{}}
	// The per-write bound of every amount is the largest single lease. An
	// oauth run is admitted without one, so it is needed only to report.
	var maxRes Micros
	var maxErr error
	if p.empty() && used == nil {
		return upd, took, false, nil
	}
	if m, ok := caps.MaxReserve(); ok {
		maxRes = m
	} else if sn, err := s.readConfig(ctx); err == nil {
		if m, ok := sn.Caps.MaxReserve(); ok {
			maxRes = m
		}
	}
	if maxRes <= 0 {
		maxErr = errors.New("budget: limits.maxReserveMicros is not set, so nothing can be reported")
	}
	today := Day(s.now())
	slug, run := s.cfg.Slug, s.cfg.Run

	cache := map[int64]*dayCounters{}
	counterAt := func(day int64) (*dayCounters, error) {
		if c, ok := cache[day]; ok {
			return c, nil
		}
		c := &dayCounters{}
		if _, err := s.db.Get(ctx, PathSpendRepo(day, slug), &c.repo); err != nil {
			return nil, err
		}
		if _, err := s.db.Get(ctx, PathSpendGlobal(day), &c.global); err != nil {
			return nil, err
		}
		cache[day] = c
		return c, nil
	}

	var ledger RunLedger
	haveLedger := false
	getLedger := func() error {
		if haveLedger {
			return nil
		}
		if _, err := s.db.Get(ctx, PathRun(slug, run), &ledger); err != nil {
			return err
		}
		haveLedger = true
		return nil
	}

	// Settled spend moves from outstanding to spent without touching
	// `counted`, onto the oldest day with a lease to spend it against.
	if used != nil {
		if err := getLedger(); err != nil {
			return nil, took, false, err
		}
		target := min(used(), ledger.Reserved-ledger.Released)
		delta := target - ledger.Spent
		if delta > 0 && maxErr != nil {
			return nil, took, false, maxErr
		}
		if delta > maxRes {
			delta, more = maxRes, true
		}
		var total Micros
		for _, day := range []int64{today - 1, today} {
			if delta <= 0 {
				break
			}
			if day != today && !leased[day] {
				continue
			}
			var dr RunLedger
			if _, err := s.db.Get(ctx, PathSpendRun(day, slug, run), &dr); err != nil {
				return nil, took, false, err
			}
			take := min(delta, dr.Reserved-dr.Released-dr.Spent)
			if take <= 0 {
				continue
			}
			c, err := counterAt(day)
			if err != nil {
				return nil, took, false, err
			}
			upd[PathSpendRun(day, slug, run)+"/spent"] = add(dr.Spent, take)
			upd[PathSpendRepo(day, slug)+"/spent"] = add(c.repo.Spent, take)
			upd[PathSpendGlobal(day)+"/spent"] = add(c.global.Spent, take)
			total, delta = add(total, take), delta-take
		}
		if total > 0 {
			upd[PathRun(slug, run)+"/spent"] = add(ledger.Spent, total)
		}
	}

	// The rest is increase-only and feeds no decision.
	runLeaf := func(leaf string, cur, add_ Micros) {
		upd[PathRun(slug, run)+"/"+leaf] = add(cur, add_)
	}
	if !p.empty() && maxErr != nil {
		return nil, took, false, maxErr
	}
	needRun := p.tokens > 0 || p.overrun > 0 || p.notional > 0
	if needRun {
		if err := getLedger(); err != nil {
			return nil, took, false, err
		}
	}
	if p.tokens > 0 {
		t := min(p.tokens, maxTokensPerWrite)
		upd[PathRun(slug, run)+"/tokens"] = ledger.Tokens + t
		took.tokens = t
		more = more || t < p.tokens
	}
	if p.overrun > 0 {
		o := min(p.overrun, maxRes)
		runLeaf("overrun", ledger.Overrun, o)
		took.overrun = o
		more = more || o < p.overrun
	}
	if p.notional > 0 {
		n := min(p.notional, maxRes)
		runLeaf("notional", ledger.Notional, n)
		took.notional = n
		more = more || n < p.notional
	}
	if p.calls > 0 || len(p.byModel) > 0 || p.notional > 0 || len(p.nby) > 0 {
		c, err := counterAt(today)
		if err != nil {
			return nil, took, false, err
		}
		if p.calls > 0 {
			n := min(p.calls, maxCallsPerWrite)
			upd[PathSpendRepo(today, slug)+"/calls"] = c.repo.Calls + n
			upd[PathSpendGlobal(today)+"/calls"] = c.global.Calls + n
			took.calls = n
			more = more || n < p.calls
		}
		if took.notional > 0 {
			upd[PathSpendRepo(today, slug)+"/notional"] = add(c.repo.Notional, took.notional)
			upd[PathSpendGlobal(today)+"/notional"] = add(c.global.Notional, took.notional)
		}
		for m, v := range p.byModel {
			n := min(v, maxRes)
			cur := c.repo.ByModel[Key(m)]
			upd[PathByModel(today, slug, m)+"/micros"] = add(cur.Micros, n)
			took.byModel[m] = n
			more = more || n < v
		}
		for m, u := range p.nby {
			cur := c.repo.ByModel[Key(m)]
			n := ModelUse{Micros: min(u.Micros, maxRes), In: min(u.In, maxTokensPerWrite), Out: min(u.Out, maxTokensPerWrite),
				CR: min(u.CR, maxTokensPerWrite), CW: min(u.CW, maxTokensPerWrite)}
			base := PathByModel(today, slug, m)
			if n.Micros > 0 {
				upd[base+"/micros"] = add(cur.Micros, n.Micros)
			}
			if n.In > 0 {
				upd[base+"/in"] = cur.In + n.In
			}
			if n.Out > 0 {
				upd[base+"/out"] = cur.Out + n.Out
			}
			if n.CR > 0 {
				upd[base+"/cr"] = cur.CR + n.CR
			}
			if n.CW > 0 {
				upd[base+"/cw"] = cur.CW + n.CW
			}
			took.nby[m] = n
			more = more || n != u
		}
	}
	if len(p.byTier) > 0 {
		if err := getLedger(); err != nil {
			return nil, took, false, err
		}
	}
	if err := s.planByTier(ctx, p, ledger, leased, today, maxRes, maxErr, counterAt, upd, &took, &more); err != nil {
		return nil, took, false, err
	}
	return upd, took, more, nil
}

// dayCounters are one day's repository and project counters, which every
// usage write reads together.
type dayCounters struct{ repo, global Counters }

// byTierIn is the path segment every by-tier leaf shares, which is what an
// update is asked about and stripped by.
const byTierIn = "/byTier/"

func carriesByTier(upd map[string]any) bool {
	for k := range upd {
		if strings.Contains(k, byTierIn) {
			return true
		}
	}
	return false
}

// probeByTierKeys is checkCascadeKeys's twin for the by-tier split (C1.16),
// and it runs only when an update carrying the split was refused with a
// permission error: it asks the deployed rules the one question a run can
// ask them, by writing the very same update without the split's leaves.
//
// Refused with them and accepted without them, the split's counters are what
// the deployed document does not know -- C1.3's `credsRules` marker cannot
// catch that, because an older Fugaro redeploying its own document leaves the
// marker at a generation it does not know -- and the run halts named (C1.17)
// rather than dropping the split silently: a run whose real spend is
// accounted for under no tier is a run nobody can attribute spend to, which
// is the one thing the split exists for. It is not retried: the redeploy is
// the fix, and it is what the halt's detail names.
//
// The write that landed is the run's own usage without the split, so the run
// keeps its totals and loses only the by-tier rows: that is the fail-safe
// direction, and it is why this probe may write at all.
//
// Anything else -- refused without them too (the stale race the flush already
// retries), or a failure that is not a permission refusal (an outage, which
// the flush's own error carries) -- says nothing about the split, and the
// answer is not cached: the next refusal asks again, exactly as the registry
// probe does (M11).
func (s *Session) probeByTierKeys(ctx context.Context, upd map[string]any) (refused bool, err error) {
	rest := make(map[string]any, len(upd))
	for k, v := range upd {
		if !strings.Contains(k, byTierIn) {
			rest[k] = v
		}
	}
	if len(rest) == 0 {
		// The split was all there was: a write of nothing proves nothing
		// about the rules, and an empty patch is a no-op either way.
		return false, nil
	}
	switch err := s.db.Patch(ctx, "", rest); {
	case err == nil:
		s.dbOK()
		s.halt(Halt{Reason: ReasonCredsRulesDenied, Scope: ScopeRun, Detail: byTierDeniedDetail})
		return true, fmt.Errorf("%w: %s", rtdb.ErrPermission, byTierDeniedDetail)
	case errors.Is(err, rtdb.ErrPermission):
		return false, nil
	default:
		return false, nil
	}
}

// planByTier adds the by-tier split of the run's settled money to the update
// (C1.16, D140): the same quantity `spent` is, on the run's lifetime ledger,
// the run's share of each day it holds a lease on, and the repository's and
// the project's day counters. It is written only by a run whose calls
// carried a tier -- a cascade run -- and every other run calls it with
// nothing pending and leaves all four leaves alone.
//
// Each leaf is validated on its own (C1.3's rules): increase-only, whole
// micro-dollars, and, on the two leaves under the run's own nodes, inside
// that node's reservation -- the lifetime one against the run's whole
// reservation, the day one against that day's. The two shared counters
// bound one write by limits.maxReserveMicros. So the amount one tier moves
// in a round is the smallest of those rooms: the four leaves of a round
// move together and none can be refused for the others' sake. A tier with
// no room keeps its amount pending and the round says more, and the split
// is never tied to the total it splits: nothing decides anything on it
// (admissions, switches and kill switches read spent and notional), so
// b + c above spent is a latitude this code does not create and does not
// have to repair.
func (s *Session) planByTier(ctx context.Context, p pending, ledger RunLedger, leased map[int64]bool, today int64, maxRes Micros, maxErr error, counterAt func(int64) (*dayCounters, error), upd map[string]any, took *pending, more *bool) error {
	if len(p.byTier) == 0 {
		return nil
	}
	if maxErr != nil {
		return maxErr
	}
	slug, run := s.cfg.Slug, s.cfg.Run
	// The days this run may split onto: today always, and yesterday only
	// while it holds a lease there (the same rule the totals settle by).
	days := []int64{today}
	if leased[today-1] {
		days = append(days, today-1)
	}
	shares := make(map[int64]RunLedger, len(days))
	for _, day := range days {
		var dr RunLedger
		if _, err := s.db.Get(ctx, PathSpendRun(day, slug, run), &dr); err != nil {
			return err
		}
		shares[day] = dr
	}
	for _, tier := range []string{TierB, TierC} {
		want := p.byTier[tier]
		if want <= 0 {
			continue
		}
		cur := ledger.ByTier[tier].Spent
		// The run's own lifetime ledger needs no bound of its own: every
		// lease raises the run's reservation and the day's by the same
		// amount, so the run's room is the sum of the days' and a day's room
		// is never the larger of the two. What bounds the split there is
		// this loop.
		n := min(want, maxRes)
		for _, day := range days {
			if n <= 0 {
				break
			}
			dr := shares[day]
			n = min(n, dr.Reserved-dr.Released-dr.ByTier[tier].Spent)
		}
		if n <= 0 {
			// Nothing to move onto this round: the run's own reservation is
			// what the split waits for, and a lease that raises it (or the
			// day turning) unblocks it. The money itself is unaffected.
			*more = true
			continue
		}
		upd[PathRunByTier(slug, run, tier)+"/spent"] = add(cur, n)
		left := n
		for _, day := range days {
			if left <= 0 {
				break
			}
			dr := shares[day]
			c, err := counterAt(day)
			if err != nil {
				return err
			}
			take := min(left, dr.Reserved-dr.Released-dr.ByTier[tier].Spent)
			if take <= 0 {
				continue
			}
			upd[PathSpendRunByTier(day, slug, run, tier)+"/spent"] = add(dr.ByTier[tier].Spent, take)
			upd[PathSpendRepoByTier(day, slug, tier)+"/spent"] = add(c.repo.ByTier[tier].Spent, take)
			upd[PathSpendGlobalByTier(day, tier)+"/spent"] = add(c.global.ByTier[tier].Spent, take)
			left -= take
		}
		took.byTier[tier] = n
		*more = *more || n < want
	}
	return nil
}

// commit takes what an update carried off the pending figures.
func (s *Session) commit(took pending) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &s.pend
	p.calls -= took.calls
	p.tokens -= took.tokens
	p.overrun -= took.overrun
	p.notional -= took.notional
	for m, v := range took.byModel {
		if p.byModel[m] -= v; p.byModel[m] <= 0 {
			delete(p.byModel, m)
		}
	}
	for tier, v := range took.byTier {
		if p.byTier[tier] -= v; p.byTier[tier] <= 0 {
			delete(p.byTier, tier)
		}
	}
	for m, u := range took.nby {
		c := p.nby[m]
		c.Micros, c.In, c.Out, c.CR, c.CW = c.Micros-u.Micros, c.In-u.In, c.Out-u.Out, c.CR-u.CR, c.CW-u.CW
		if c == (ModelUse{}) {
			delete(p.nby, m)
		} else {
			p.nby[m] = c
		}
	}
}

// flush sends the run's usage and, in the same atomic update, extra (the
// heartbeat's registry entry). A denied write is retried against a fresh
// read; a usage write that keeps being denied still lets extra through on
// its own, so a busy day counter cannot silence the registry.
func (s *Session) flush(ctx context.Context, extra map[string]any, source string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	stale := 0
	probed := false
	for rounds := 0; rounds < 20; {
		upd, took, more, err := s.plan(ctx)
		if err != nil {
			return s.observe(source, err)
		}
		merged := make(map[string]any, len(upd)+len(extra))
		for k, v := range upd {
			merged[k] = v
		}
		for k, v := range extra {
			merged[k] = v
		}
		if len(merged) == 0 {
			s.dbOK()
			return nil
		}
		err = s.db.Patch(ctx, "", merged)
		switch {
		case err == nil:
			s.commit(took)
			s.dbOK()
			extra = nil
			rounds++
			if !more {
				return nil
			}
		case errors.Is(err, rtdb.ErrPermission):
			if !probed && carriesByTier(merged) {
				// This update carried the by-tier split into the refusal:
				// ask the rules, with one write of its own, whether the
				// split is what they do not know (C1.16). The probe is not
				// repeated in this flush: a refusal that is not the split's
				// is the stale race the loop already retries.
				probed = true
				if refused, herr := s.probeByTierKeys(ctx, merged); refused {
					// The totals landed without the split. Take everything
					// off the pending figures -- the totals because they are
					// written, the split because it is not retried -- and end
					// the run named.
					s.commit(took)
					return s.observe(source, herr)
				}
			}
			stale++
			if stale >= maxStale {
				werr := fmt.Errorf("budget: %d usage writes in a row were denied", stale)
				if len(extra) > 0 {
					if perr := s.db.Patch(ctx, "", extra); perr == nil {
						s.dbOK()
					} else if errors.Is(perr, rtdb.ErrPermission) {
						// extra (the registry heartbeat) was denied on its
						// own too, not only alongside the usage counters:
						// keep that signal so a caller can tell the registry
						// write itself apart from an unrelated stale race.
						werr = fmt.Errorf("%w: %v", rtdb.ErrPermission, werr)
					}
				}
				return s.observe(source, werr)
			}
			if werr := s.sleep(ctx, s.jitter(stale)); werr != nil {
				return werr
			}
		default:
			return s.observe(source, err)
		}
	}
	return nil
}

// release gives unused back (R3, §5.3): released rises on the run and on its
// share of the day, and both shared counters fall by exactly the same
// amount, per day (today's lease first, then yesterday's). What cannot be
// released stays counted, which errs high, and is tried once more at Finish.
func (s *Session) release(ctx context.Context, unused Micros) error {
	if unused <= 0 {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	remaining := unused
	stale := 0
	today := Day(s.now())
	for _, day := range []int64{today, today - 1} {
		s.mu.Lock()
		held := s.leased[day]
		s.mu.Unlock()
		if day != today && !held {
			continue
		}
		for remaining > 0 {
			st, err := s.readState(ctx, day)
			if err != nil {
				return s.fail(unused, remaining, s.observe("lease", err))
			}
			amt := min(remaining, st.Run.Unsettled(), st.DayRun.Unsettled())
			if amt <= 0 {
				break
			}
			if d := Evaluate(st, Write{Op: OpRelease, Day: day, Amount: amt}, Caps{}, Kills{}, s.now()); !d.Allow {
				s.log.Warn("budget: a release would be refused", "reason", string(d.Reason), "detail", d.Detail)
				break
			}
			upd := map[string]any{
				PathRun(s.cfg.Slug, s.cfg.Run) + "/released":           add(st.Run.Released, amt),
				PathSpendRun(day, s.cfg.Slug, s.cfg.Run) + "/released": add(st.DayRun.Released, amt),
				PathSpendRepo(day, s.cfg.Slug) + "/counted":            st.Repo.Counted - amt,
				PathSpendGlobal(day) + "/counted":                      st.Global.Counted - amt,
			}
			if st.Repo.Counted < amt || st.Global.Counted < amt {
				// The counters cannot be lower than the run's own share; do
				// not write a negative.
				s.log.Warn("budget: a day counter holds less than this run's release", "day", day)
				break
			}
			switch err := s.db.Patch(ctx, "", upd); {
			case err == nil:
				remaining -= amt
				s.mu.Lock()
				s.released = add(s.released, amt)
				s.mu.Unlock()
				s.dbOK()
			case errors.Is(err, rtdb.ErrPermission):
				if stale++; stale >= maxStale {
					return s.fail(unused, remaining, s.observe("lease", fmt.Errorf("budget: %d release writes in a row were denied", stale)))
				}
				if werr := s.sleep(ctx, s.jitter(stale)); werr != nil {
					return s.fail(unused, remaining, werr)
				}
			default:
				return s.fail(unused, remaining, s.observe("lease", err))
			}
		}
	}
	if remaining > 0 {
		return s.fail(unused, remaining, fmt.Errorf("budget: %s of the lease could not be released and stays counted (it errs high until the day ends)", dollars(remaining)))
	}
	return nil
}

// fail remembers a release that did not complete.
func (s *Session) fail(unused, remaining Micros, err error) error {
	s.mu.Lock()
	s.unrel = add(s.unrel, remaining)
	s.mu.Unlock()
	return err
}
