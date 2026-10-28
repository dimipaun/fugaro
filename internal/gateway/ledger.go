package gateway

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// How a call was priced and settled, as the log line says.
const (
	pricedTable    = "table"    // the serving model's rates
	pricedMax      = "max"      // an unknown serving model: the table's maximum rates
	pricedSurprise = "surprise" // a dimension the request didn't allow: maximum rates × SurpriseMultiplier
	pricedTier     = "tier"     // a service tier no one prices: the call's own rates × ConservativeTierMultiplier

	settledUsage    = "usage"    // the usage a complete response reported
	settledPartial  = "partial"  // input and cache from message_start, plus the reserved output
	settledReserved = "reserved" // the full reservation: the gateway couldn't tell
	settledZero     = "zero"     // an upstream error status, or nothing was sent
)

// haltedMessage is the refusal every call gets once the run halted.
func (s *Server) haltedMessage() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.haltMsg, s.halt != nil
}

// reserve holds w for a call, and returns 0. In enforce mode a call whose
// worst case can't fit under the cap even with nothing else in flight
// (used + w > cap) is refused with 403 and halts the run: it and every
// later call get the halt's message. A call that would fit but for other
// calls' reservations, which over-count what those calls will cost, is
// refused with a retryable 429 and no halt: the cap may well hold once
// they settle. In observe mode a call is counted and logged instead,
// never refused.
func (s *Server) reserve(ctx context.Context, st *stageState, w pricing.Micros) (refusal string, status int) {
	if s.o.Lease != nil {
		return s.reserveLease(ctx, st, w)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.halt != nil {
		return s.haltMsg, http.StatusForbidden
	}
	over := s.o.Cap > 0 && satAdd(s.used, w) > s.o.Cap
	if over || (s.o.Mode == Observe && s.wouldHalted) {
		if s.o.Mode == Enforce {
			h := Halt{
				Reason: "run_cap",
				Detail: fmt.Sprintf("run cap %s reached (%s spent, %s needed)", dollars(s.o.Cap), dollars(s.used), dollars(w)),
				At:     time.Now().UTC(),
			}
			s.haltMsg = fmt.Sprintf("fugaro: budget halted: run cap %s reached (%s spent)", dollars(s.o.Cap), dollars(s.used))
			if s.used == 0 {
				// Nothing was spent, so the cap wasn't "reached": the call
				// alone is bigger than it.
				h.Detail = fmt.Sprintf("run cap %s cannot hold a call that needs up to %s (nothing spent)", dollars(s.o.Cap), dollars(w))
				s.haltMsg = fmt.Sprintf("fugaro: budget halted: run cap %s cannot hold a call that needs up to %s", dollars(s.o.Cap), dollars(w))
			}
			s.haltLocked(h, s.haltMsg, st.st.Name, true)
			return s.haltMsg, http.StatusForbidden
		}
		s.wouldHalted = true
		st.rep.WouldHalt++
		s.log.Warn("budget: would halt (observe)", "stage", st.st.Name,
			"cap_micros", int64(s.o.Cap), "used_micros", int64(s.used), "reserved_micros", int64(s.reserved), "needed_micros", int64(w))
	} else if s.o.Mode == Enforce && s.o.Cap > 0 && satAdd(satAdd(s.used, s.reserved), w) > s.o.Cap {
		st.rep.Waited++
		s.log.Info("budget: call waits for calls in flight", "stage", st.st.Name,
			"cap_micros", int64(s.o.Cap), "used_micros", int64(s.used), "reserved_micros", int64(s.reserved), "needed_micros", int64(w))
		return "fugaro: the run's budget is held by calls still in flight; retry shortly", http.StatusTooManyRequests
	}
	s.reserved = satAdd(s.reserved, w)
	s.checkLocked()
	return "", 0
}

// charge is how a call settles.
type charge struct {
	amount       pricing.Micros
	unreconciled pricing.Micros
	model        string // the serving model, for ByModel
	unparsed     bool   // a 2xx settled at its reservation
	tokens       int64  // reported tokens
	sent         bool   // the call reached the upstream (it counts as a call)
	violation    string // a surprise pricing dimension
	// serviceTier is the usage.service_tier value the response reported and
	// the call was charged conservatively for, as a message shows it
	// (`"priority"`, `12` for a value that isn't a string). It is recorded
	// on the stage and in the call log, and it is not a violation: the
	// provider's own answer, not the agent's request, is where it came
	// from, and the conservative charge is what bounds it. "" for every
	// call the table priced, the standard tier included.
	serviceTier string
	route       string         // the provider route the call went to; "" is Anthropic
	reported    pricing.Micros // the cost the provider reported, never charged
	// agentID is Claude Code's x-claude-code-agent-id for this call: the
	// sub-agent that made it, or "" for the main session. The stage's
	// report counts the distinct ones.
	agentID string
	// tier is the credentials service's tier for this call (TierA, TierB,
	// TierC), read off the answer that served it (C1.15); "" for every call
	// that did not go through the service, which settles as it always did.
	// It decides whether the call is notional or money (C1.16, D140).
	tier string
	// cacheRead is the call's reported cache-read tokens, which is what the
	// next switch's cache-miss cost is estimated from (F24). It is kept for
	// every call, whatever served it: the call a switch follows is as often
	// an ordinary one as a tier A one. It means something only when usageRead
	// says the call reported usage at all.
	cacheRead int64
	// cacheWrite is the call's reported cache-write tokens, both TTLs
	// together. It is summed into the run's total and read for nothing else:
	// it is what the run's own record needs to answer design §3's
	// prompt-caching risk (a compressed session's cache prefix can change,
	// turning cheap reads into dearer writes), and like cacheRead it means
	// something only when usageRead is set.
	cacheWrite int64
	// usageRead is true for a call whose usage this gateway read (its report,
	// complete or partial). A call that settled with no usage -- one that
	// never reached the upstream, a relayed model or provider error, a
	// redirect, a 2xx whose usage did not parse -- says nothing about what
	// the run had cached, and that is not zero (F24).
	usageRead bool
}

// settle releases a call's reservation w and charges it. An amount above
// w is charged whole and counted as overrun.
//
// A tier-A call is charged as notional (C1.16, D140): it is what the run's
// subscription paid, not money, so it never enters the ledger's spend, the
// stage's Used, an overrun or the by-tier split, and no cap can be reached
// by it. It is still counted — the calls, the tokens, the model split and
// the total figure are what a reader wants of an OAuth run — which is what
// distinguishes it from a call that was not charged at all.
func (s *Server) settle(st *stageState, w pricing.Micros, c charge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserved -= w
	if s.reserved < 0 { // never: every settle matches one reserve
		s.log.Error("budget: a settle released more than was reserved", "stage", st.st.Name, "reserved_micros", int64(s.reserved), "released_micros", int64(w))
		s.reserved = 0
	}
	real := c.tier != TierA
	if real {
		s.used = satAdd(s.used, c.amount)
	} else {
		s.notional = satAdd(s.notional, c.amount)
	}
	var over pricing.Micros
	if real && c.amount > w {
		over = c.amount - w
		s.overrun = satAdd(s.overrun, over)
	}
	// The last call's cache reads and model, for the estimate a switch
	// carries (F24): the next call's answer may say it moved credentials,
	// and what that costs is this call's cached context re-read on the new
	// tier. Only a call that reported usage speaks for the run's cache: a
	// call that settled with none at all leaves the figure unknown, which is
	// not zero -- the call before it may well have read a cached context,
	// and a switch after it must record itself unmeasured rather than as a
	// miss that cost nothing (F24).
	if c.usageRead {
		s.lastCacheRead, s.lastCacheKnown = c.cacheRead, true
		// The run's cache totals are of the calls whose usage was read, for
		// the same reason the switch estimate is: a call that reported none
		// says nothing about what the run had cached, and adding its zero
		// would read as a cache that did nothing (P12, F24).
		s.cacheRead = int64(satAdd(pricing.Micros(s.cacheRead), pricing.Micros(c.cacheRead)))
		s.cacheWrite = int64(satAdd(pricing.Micros(s.cacheWrite), pricing.Micros(c.cacheWrite)))
	} else {
		s.lastCacheRead, s.lastCacheKnown = 0, false
	}
	s.lastModel = c.model
	r := &st.rep
	if c.sent {
		r.Calls++
	}
	// A call the gateway couldn't send still names an agent that asked for
	// it, and a sub-agent is spawned whether or not its call left.
	if c.agentID != "" {
		if _, seen := st.agents[c.agentID]; !seen {
			st.agents[c.agentID] = struct{}{}
			r.Agents = len(st.agents)
		}
	}
	if real {
		r.Used = satAdd(r.Used, c.amount)
		if c.amount > 0 && (c.tier == TierB || c.tier == TierC) {
			r.ByTier[c.tier] = satAdd(r.ByTier[c.tier], c.amount)
		}
	} else {
		r.Notional = satAdd(r.Notional, c.amount)
	}
	if c.amount > 0 {
		r.ByModel[c.model] = satAdd(r.ByModel[c.model], c.amount)
		if c.route != "" {
			r.ByRoute[c.route] = satAdd(r.ByRoute[c.route], c.amount)
		}
	}
	r.Reported = satAdd(r.Reported, c.reported)
	r.Unreconciled = satAdd(r.Unreconciled, c.unreconciled)
	r.Overrun = satAdd(r.Overrun, over)
	if c.unparsed {
		r.UsageUnparsed++
	}
	r.Tokens = int64(satAdd(pricing.Micros(r.Tokens), pricing.Micros(c.tokens)))
	if c.violation != "" && !contains(r.Violations, c.violation) {
		r.Violations = append(r.Violations, c.violation)
	}
	if c.serviceTier != "" {
		// Recorded the way a violation is, field and value, so a reader can
		// tell the two apart at a glance: this one was charged, not refused.
		what := "usage.service_tier " + c.serviceTier
		if !contains(r.ServiceTiers, what) {
			r.ServiceTiers = append(r.ServiceTiers, what)
		}
	}
	if over > 0 {
		s.log.Warn("budget: call cost more than its reservation", "stage", st.st.Name, "model", logValue(c.model),
			"reserved_micros", int64(w), "charged_micros", int64(c.amount), "overrun_micros", int64(over))
	}
	s.checkLocked()
}

// settleProbe charges a probe the credentials service made while answering
// this run's call (C1.12), by the tier the probe was made on (C1.16): a
// probe on tier A is notional, a probe on any other tier is money, exactly as
// the call that owed it settles.
//
// It is not a settle: there is no reservation to release. A probe is made
// inside the service's answer to one client request, and that request's own
// reservation is what covers the service's work (C1.11) — the probe is a
// second upstream call inside it, so the run pays for it over and above what
// the request reserved and the amount is charged whole rather than against
// w. The overrun bookkeeping is left alone for the same reason: an overrun is
// a reservation the call outgrew, and this is not a reservation.
//
// It leaves the last call's cache state untouched too (F24): what the run had
// cached is the answering call's own usage, and a probe -- a minimal call the
// service built, whose answer is closed unread -- says nothing about it.
func (s *Server) settleProbe(st *stageState, tier, model string, amount pricing.Micros) {
	if amount <= 0 || tier == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	real := tier != TierA
	if real {
		s.used = satAdd(s.used, amount)
	} else {
		s.notional = satAdd(s.notional, amount)
	}
	r := &st.rep
	r.Calls++ // it is a call: it reached the upstream and somebody paid for it
	if real {
		r.Used = satAdd(r.Used, amount)
		if tier == TierB || tier == TierC {
			r.ByTier[tier] = satAdd(r.ByTier[tier], amount)
		}
	} else {
		r.Notional = satAdd(r.Notional, amount)
	}
	r.ByModel[model] = satAdd(r.ByModel[model], amount)
	s.log.Info("budget: a probe settled", "stage", st.st.Name, "tier", tier,
		"model", logValue(model), "micros", int64(amount), "notional", !real)
	s.checkLocked()
}

// SwitchCost is what one credential switch is estimated to cost (F24,
// D140): the cache the last call had already read, re-read as a miss on the
// tier that answered it. It is a figure about the switch, never about a
// call, and the run records it beside the switch.
type SwitchCost struct {
	// USD is the estimate: (the cached input tokens of the last call) ×
	// (the input rate − the cache-read rate) of the model that call was
	// served by. Zero when Unmeasured.
	USD float64
	// Unmeasured says this gateway cannot estimate the miss, and is what
	// the record shows in place of a figure: the call is moving to tier C,
	// where Claude's cache behaviour through the provider is unmeasured
	// (F24, Checks 46 and 50); or the last call's model has no price in
	// the run's table; or no call has reported usage yet, so what the miss
	// would re-read is not known. A zero would read as free.
	Unmeasured bool
}

// switchCost estimates what moving to tier costs, from the last settled
// call's cache reads (F24). tier is the tier that answered: the new one.
//
// The rate is the run's own price table's row for the model that call was
// served by, which is the same list price on tiers A and B (the credential
// changes, the model does not). It is an estimate and says so: the miss is
// what the cached context costs at the input rate instead of the cache-read
// rate, and the calls that follow it are not in it.
func (s *Server) switchCost(tier string) SwitchCost {
	if tier == TierC {
		return SwitchCost{Unmeasured: true}
	}
	s.mu.Lock()
	model, tokens, known := s.lastModel, s.lastCacheRead, s.lastCacheKnown
	s.mu.Unlock()
	if !known {
		return SwitchCost{Unmeasured: true}
	}
	m, ok := s.o.Prices.Lookup(model)
	if !ok {
		return SwitchCost{Unmeasured: true}
	}
	// µ$ per cached token: what reading it costs as a miss rather than as a
	// hit. A table whose cache-read rate is at or above its input rate (both
	// are validated on their own, so it can be) is a miss that costs
	// nothing, which is what the arithmetic says and not a floor to argue
	// with. The result is an estimate in µ$, the unit the ledger counts in.
	per := m.Rates.InputPerM * (1 - m.Rates.CacheRead)
	u := float64(tokens) * per
	switch {
	case u <= 0:
		return SwitchCost{}
	case u >= math.MaxInt64:
		u = math.MaxInt64
	}
	return SwitchCost{USD: pricing.Micros(math.Round(u)).USD()}
}

func (s *Server) checkLocked() {
	if s.onLedger != nil {
		s.onLedger(s.ledgerLocked(), s.overrun)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// dollars formats µ$ as "$12.34", with more digits below a cent so a
// small cap isn't printed as $0.00.
func dollars(m pricing.Micros) string {
	if m > 0 && m.USD() < 0.01 {
		return strings.TrimRight(fmt.Sprintf("$%.6f", m.USD()), "0")
	}
	return fmt.Sprintf("$%.2f", m.USD())
}

// satAdd adds two amounts, saturating at the largest Micros; negative
// amounts count as 0.
func satAdd(a, b pricing.Micros) pricing.Micros {
	a, b = max(a, 0), max(b, 0)
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// satMul multiplies an amount by n ≥ 0, saturating.
func satMul(a pricing.Micros, n int64) pricing.Micros {
	a, n = max(a, 0), max(n, 0)
	if a == 0 || n == 0 {
		return 0
	}
	if int64(a) > math.MaxInt64/n {
		return math.MaxInt64
	}
	return a * pricing.Micros(n)
}
