package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// Lease is where a gateway's budget comes from when it isn't a static cap
// (design §5.3, level 2). The gateway reserves each call's worst case
// against what the lease granted, and asks for more only when a call
// doesn't fit. Every method may be called from any goroutine; Grant is
// never called twice at once.
type Lease interface {
	// Grant returns an amount of at least need that the run may now spend
	// (the implementation picks the size: more than need when the caps
	// leave room, exactly need near a cap), or an error and 0. A
	// *Refusal says the budget refuses; any other error means the budget
	// is unavailable (the session owns the 3-minute grace). The amount
	// returned counts against the run from the moment it was written, so
	// an implementation returns an error only when nothing was granted.
	// ctx ends when Close gives up waiting, never because one call's
	// client left.
	Grant(ctx context.Context, need pricing.Micros) (pricing.Micros, error)
	// Report says what a finished stage's calls cost. A failure is logged,
	// never fatal.
	Report(ctx context.Context, rep StageReport) error
	// Release returns unused > 0 of what was granted, once, at Close.
	Release(ctx context.Context, unused pricing.Micros) error
}

// Refusal is the error Grant returns when the budget says no: a cap
// reached, a kill switch on, no cap set. Reason is the halt's reason
// (run_cap, repo_daily_cap, global_daily_cap, no_cap, kill_switch, ...),
// Scope run, repo or global, Detail the sentence the report shows.
type Refusal struct {
	Reason, Scope, Detail string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("budget refused (%s, %s): %s", r.Reason, r.Scope, r.Detail)
}

// capReason says whether a refusal is one observe mode relaxes and a call
// in flight may explain. Everything else (a kill switch above all) halts
// at once, in every mode.
func capReason(reason string) bool {
	switch reason {
	case "run_cap", "repo_daily_cap", "global_daily_cap", "no_cap":
		return true
	}
	return false
}

const (
	// maxTopUps bounds the grants one call waits through (each grant may
	// be shared with others and be smaller than this call needs).
	maxTopUps     = 5
	reportTimeout = 30 * time.Second
	// releaseTimeout bounds the release at Close even when Close's own
	// context is spent: a release that never happens leaves the lease
	// counted until the sweeper.
	releaseTimeout = 10 * time.Second

	unavailableMsg = "fugaro: the budget backend is unavailable; retry shortly"
	closingMsg     = "fugaro: the budget gateway is closing"
)

// topUp is the one grant in flight: every call that needs more waits on
// it instead of asking for its own.
type topUp struct {
	done chan struct{}
	err  error // set before done closes
}

// freeLocked is what the lease still holds unspent and unreserved.
func (s *Server) freeLocked() pricing.Micros {
	held := satAdd(s.used, s.reserved)
	if held >= s.granted {
		return 0
	}
	return s.granted - held
}

// reserveLease holds w from the lease, topping it up when it doesn't
// fit. No lock is held across Grant; Granted rises only by grants, so
// used + reserved <= granted holds (but for an overrun, as in M9a).
func (s *Server) reserveLease(ctx context.Context, st *stageState, w pricing.Micros) (string, int) {
	for attempt := 0; ; attempt++ {
		s.mu.Lock()
		if s.halt != nil {
			msg := s.haltMsg
			s.mu.Unlock()
			return msg, http.StatusForbidden
		}
		if s.base.Err() != nil {
			s.mu.Unlock()
			return closingMsg, http.StatusServiceUnavailable
		}
		free := s.freeLocked()
		if free >= w {
			s.reserved = satAdd(s.reserved, w)
			s.checkLocked()
			s.mu.Unlock()
			return "", 0
		}
		if attempt >= maxTopUps {
			s.log.Warn("budget: the lease keeps granting less than a call needs", "stage", st.st.Name, "needed_micros", int64(w))
			s.mu.Unlock()
			return unavailableMsg, http.StatusServiceUnavailable
		}
		f := s.flight
		if f == nil {
			f = &topUp{done: make(chan struct{})}
			s.flight = f
			s.grants.Add(1)
			go s.runTopUp(f, w-free)
		}
		s.mu.Unlock()
		select {
		case <-f.done:
		case <-s.hctx.Done(): // a halt (or Close): the loop reads which
			continue
		case <-ctx.Done():
			return "fugaro: the call was cancelled", http.StatusServiceUnavailable
		}
		if f.err != nil {
			return s.leaseFailure(st, w, f.err)
		}
	}
}

// runTopUp is the single grant. It runs on its own goroutine so its
// result always lands in the ledger, even when every caller left.
func (s *Server) runTopUp(f *topUp, need pricing.Micros) {
	defer s.grants.Done()
	g, err := s.o.Lease.Grant(s.gctx, need)
	s.mu.Lock()
	switch {
	case err != nil:
		f.err = err
	case g <= 0:
		f.err = errors.New("the lease granted nothing")
	default:
		s.granted = satAdd(s.granted, g)
		s.checkLocked()
	}
	s.flight = nil
	s.mu.Unlock()
	close(f.done)
}

// leaseFailure turns a failed grant into what the call is told. Only a
// *Refusal can halt; anything else is the backend being unavailable, a
// retryable 503 that never halts here (the session's grace decides).
func (s *Server) leaseFailure(st *stageState, w pricing.Micros, err error) (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.halt != nil { // another call or the session got there first
		return s.haltMsg, http.StatusForbidden
	}
	var ref *Refusal
	if !errors.As(err, &ref) {
		s.log.Warn("budget: the lease is unavailable", "stage", st.st.Name, "error", logValue(err.Error()))
		return unavailableMsg, http.StatusServiceUnavailable
	}
	capR := capReason(ref.Reason)
	switch {
	case capR && s.o.Mode == Observe:
		// A cap never refuses in observe, and an observe lease isn't
		// supposed to say so. The call can't be served unbacked, so it
		// waits, and the gap is counted.
		s.wouldHalted = true
		st.rep.WouldHalt++
		s.log.Warn("budget: would halt (observe)", "stage", st.st.Name, "reason", logValue(ref.Reason), "detail", logValue(ref.Detail),
			"granted_micros", int64(s.granted), "used_micros", int64(s.used), "reserved_micros", int64(s.reserved), "needed_micros", int64(w))
		return unavailableMsg, http.StatusServiceUnavailable
	case capR && s.reserved > 0 && satAdd(s.used, w) <= s.granted:
		// What blocks the call is the lease held by calls in flight, which
		// over-count what they will cost: the cap may well hold once they
		// settle (M9a's 429 rule, with the lease as the cap).
		st.rep.Waited++
		s.log.Info("budget: call waits for calls in flight", "stage", st.st.Name, "reason", logValue(ref.Reason),
			"granted_micros", int64(s.granted), "used_micros", int64(s.used), "reserved_micros", int64(s.reserved), "needed_micros", int64(w))
		return "fugaro: the run's budget is held by calls still in flight; retry shortly", http.StatusTooManyRequests
	}
	h := Halt{Reason: ref.Reason, Scope: ref.Scope, Detail: ref.Detail, At: time.Now().UTC()}
	s.haltLocked(h, "fugaro: budget halted: "+haltText(h), st.st.Name, true)
	return s.haltMsg, http.StatusForbidden
}

// haltLocked records the halt, once. deliver sends it on Halted(); a halt
// the session raised itself isn't echoed back to it.
func (s *Server) haltLocked(h Halt, msg, stage string, deliver bool) {
	if h.Scope == "" {
		h.Scope = "run"
	}
	s.halt, s.haltMsg = &h, msg
	if deliver {
		s.haltCh <- h
		close(s.haltCh)
	}
	s.log.Warn("budget: halted", "reason", logValue(h.Reason), "scope", h.Scope, "detail", logValue(h.Detail), "stage", stage)
}

// HaltExternal halts the run for a cause the gateway can't see (a kill
// switch, the backend gone for the grace period): every later call is
// refused with 403 and x-should-retry: false, and calls in flight are
// cancelled (each settles by M9a's rules for a call cut short: usage seen
// so far plus the reserved output, or the whole reservation). It reports
// whether it took effect; the first halt of any kind stands. The halt is
// not echoed on Halted(): the caller already knows.
func (s *Server) HaltExternal(h Halt) bool {
	s.mu.Lock()
	if s.halt != nil || s.base.Err() != nil {
		s.mu.Unlock()
		return false
	}
	if h.At.IsZero() {
		h.At = time.Now().UTC()
	}
	if h.Reason == "" {
		h.Reason = "halted"
	}
	s.haltLocked(h, "fugaro: budget halted: "+haltText(h), s.stageNameLocked(), false)
	s.mu.Unlock()
	s.hstop()
	return true
}

func (s *Server) stageNameLocked() string {
	if s.stage == nil {
		return ""
	}
	return s.stage.st.Name
}

// haltText is what the agent is told: the halt's detail (or its reason),
// one line, clipped.
func haltText(h Halt) string {
	t := h.Detail
	if strings.TrimSpace(t) == "" {
		t = h.Reason
	}
	t = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, t)
	if len(t) > 300 {
		t = strings.ToValidUTF8(t[:300], "")
	}
	return t
}

// reportStage hands a finished stage to the lease.
func (s *Server) reportStage(rep StageReport) {
	if s.o.Lease == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.gctx, reportTimeout)
	defer cancel()
	if err := s.o.Lease.Report(ctx, rep); err != nil {
		s.log.Warn("budget: reporting the stage failed", "error", logValue(err.Error()))
	}
}

// releaseUnused gives back what the run was granted and neither spent nor
// still holds, once (Close's Once). A release that fails leaves the amount
// counted, which errs high; Close reports it.
func (s *Server) releaseUnused(ctx context.Context) error {
	if s.o.Lease == nil {
		return nil
	}
	s.mu.Lock()
	held := satAdd(s.used, s.reserved)
	var unused pricing.Micros
	if s.granted > held {
		unused = s.granted - held
	}
	s.mu.Unlock()
	if unused == 0 {
		return nil
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.o.Lease.Release(rctx, unused); err != nil {
		return fmt.Errorf("gateway: releasing the unused lease (%s): %w", dollars(unused), err)
	}
	s.mu.Lock()
	s.granted -= unused
	s.checkLocked()
	s.mu.Unlock()
	return nil
}
