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
	model        string         // the serving model, for ByModel
	unparsed     bool           // a 2xx settled at its reservation
	tokens       int64          // reported tokens
	sent         bool           // the call reached the upstream (it counts as a call)
	violation    string         // a surprise pricing dimension
	route        string         // the provider route the call went to; "" is Anthropic
	reported     pricing.Micros // the cost the provider reported, never charged
}

// settle releases a call's reservation w and charges it. An amount above
// w is charged whole and counted as overrun.
func (s *Server) settle(st *stageState, w pricing.Micros, c charge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserved -= w
	if s.reserved < 0 { // never: every settle matches one reserve
		s.log.Error("budget: a settle released more than was reserved", "stage", st.st.Name, "reserved_micros", int64(s.reserved), "released_micros", int64(w))
		s.reserved = 0
	}
	s.used = satAdd(s.used, c.amount)
	var over pricing.Micros
	if c.amount > w {
		over = c.amount - w
		s.overrun = satAdd(s.overrun, over)
	}
	r := &st.rep
	if c.sent {
		r.Calls++
	}
	r.Used = satAdd(r.Used, c.amount)
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
	if over > 0 {
		s.log.Warn("budget: call cost more than its reservation", "stage", st.st.Name, "model", logValue(c.model),
			"reserved_micros", int64(w), "charged_micros", int64(c.amount), "overrun_micros", int64(over))
	}
	s.checkLocked()
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
