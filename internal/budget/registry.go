package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// maxEntryString is the rules' bound on every registry string.
const maxEntryString = 200

// MaxEntryTokens bounds AgentEntry.Tokens; it must equal
// rules.MaxTokensPerWrite (internal/budget/rules/rules.go). This package
// can't import that one: its own emulator and parity tests import
// internal/budget, so the import would cycle. TestMaxEntryTokensMatchesRules
// (internal/budget/registry_test.go, package budget_test, which can import
// both) pins the two together.
const MaxEntryTokens = 1_000_000_000

// MaxEntryTimestamp bounds AgentEntry.WaitingUntil; it must equal
// rules.MaxTimestamp (internal/budget/rules/rules.go), the bound the template's
// TIMESTAMP validator applies to `waiting_until`. It is duplicated for the same
// import-cycle reason as MaxEntryTokens, and TestMaxEntryTimestampMatchesRules
// (package budget_test, which can import both) pins the two together.
const MaxEntryTimestamp int64 = 1<<53 - 1

// clipString makes s one clean line of at most 200 bytes: the rules refuse
// longer strings, and registry text is untrusted anyway. safetext.Strip
// drops every control, BiDi override and zero-width character (not just
// unicode.IsControl's narrower set), since a dashboard reader's terminal
// trusts this text as much as any other line it prints.
func clipString(s string) string {
	s = safetext.Strip(s)
	if len(s) > maxEntryString {
		s = strings.ToValidUTF8(s[:maxEntryString], "")
	}
	return s
}

func clipEntry(e *AgentEntry) {
	for _, p := range []*string{&e.Repo, &e.Workflow, &e.Title, &e.Stage, &e.Verify, &e.Coder, &e.Reviewer, &e.Recipe, &e.Auth, &e.PRURL, &e.Halted, &e.Action, &e.Credential} {
		*p = clipString(*p)
	}
	// The rules refuse the whole entry for a tokens value outside
	// [0, MaxEntryTokens] or a non-integer; clamp here so a stage that
	// miscounts (or a negative figure from a bug) can't silently stop every
	// later heartbeat the way an unclamped value would (review focus 4).
	e.Tokens = min(max(e.Tokens, 0), MaxEntryTokens)
	// waiting_until is bounded the same way and for the same reason: the
	// template's TIMESTAMP validator refuses the entire entry for an instant
	// outside [0, MaxEntryTimestamp], and the writer that records it (C1.16)
	// would otherwise freeze the run's registry on one bad clock reading. Zero
	// means nothing is pending, so a negative instant clamps to an absent node.
	e.WaitingUntil = min(max(e.WaitingUntil, 0), MaxEntryTimestamp)
}

// Start creates the run's registry entry and its ledger's exp, then begins
// the heartbeat, the ID token refresh, the kill watch and the grace watch.
// The caller's context ends the start only. entry holds what the run knows
// about itself; Start stamps requestedBy from the token.
func (s *Session) Start(ctx context.Context, entry AgentEntry) error {
	entry.RequestedBy = s.claims.RB
	entry.UpdatedAt = s.now().UnixMilli()
	entry.StartedAt = entry.UpdatedAt
	clipEntry(&entry)
	s.mu.Lock()
	s.entry = entry
	s.mu.Unlock()
	write := func() error {
		return s.db.Patch(ctx, "", map[string]any{PathAgent(s.cfg.Slug, s.cfg.Run): entry})
	}
	err := s.retryBoot(ctx, "registry", write)
	if errors.Is(err, ErrPermissionDenied) && entry.Recipe != "" {
		// Rules from before 0.5.0 have no recipe key, and their $other
		// refuses the whole entry: go on without it for the whole session.
		// Warn only once the second write succeeded: a second refusal is a
		// genuinely bad credential, not old rules.
		entry.Recipe = ""
		s.mu.Lock()
		s.entry, s.noRecipe = entry, true
		s.mu.Unlock()
		err = s.retryBoot(ctx, "registry", write)
		if err == nil {
			s.log.Warn("budget: the database's rules predate recipes, so the registry entry goes without the recipe; run fugaro init --firebase <firebase-project-id> to update the rules")
		}
	}
	if err != nil {
		return err
	}
	if !s.cfg.Deadline.IsZero() {
		// Written once: a denial means an earlier execution wrote it.
		err := s.db.Patch(ctx, "", map[string]any{PathRun(s.cfg.Slug, s.cfg.Run) + "/exp": s.cfg.Deadline.UnixMilli()})
		if err != nil && !errors.Is(err, rtdb.ErrPermission) && ctx.Err() == nil {
			s.log.Warn("budget: writing the ledger's exp failed", "error", err.Error())
		}
	}
	bctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancel, s.started = cancel, true
	s.mu.Unlock()
	s.goSafely(bctx, "token refresh", func() {
		if err := s.tok.Keep(bctx); err != nil && bctx.Err() == nil {
			s.halt(Halt{Reason: ReasonBudgetUnavailable, Scope: ScopeRun, Detail: "the run's budget identity was refused and cannot be renewed"})
		}
	})
	s.goSafely(bctx, "grace", func() {
		select {
		case <-s.grace.Expired():
			src, err := s.grace.Cause()
			detail := fmt.Sprintf("the budget backend could not be reached for %s (%s", s.grace.d, src)
			if err != nil {
				detail += ": " + clipString(err.Error())
			}
			s.halt(Halt{Reason: ReasonBudgetUnavailable, Scope: ScopeRun, Detail: detail + ")"})
		case <-bctx.Done():
		}
	})
	s.goSafely(bctx, "heartbeat", func() {
		t := time.NewTicker(s.cfg.HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-bctx.Done():
				return
			case <-t.C:
				s.safely("heartbeat", func() { s.heartbeat(bctx) })
			}
		}
	})
	s.watchKills(bctx)
	return nil
}

// goSafely runs fn on a tracked goroutine of the session.
func (s *Session) goSafely(ctx context.Context, what string, fn func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.safely(what, fn)
	}()
}

// Update changes the run's registry entry (stage, round, ...); the next
// heartbeat sends it.
func (s *Session) Update(fn func(*AgentEntry)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.entry)
	if s.noRecipe {
		s.entry.Recipe = ""
	}
	if s.newKeysRefused {
		s.entry.Action, s.entry.Tokens = "", 0
	}
	clipEntry(&s.entry)
}

// checkNewKeys probes, once per session, whether the rules accept Action and
// Tokens (design generic-tool §10.2, review focus 4). It runs on every
// heartbeat until it reaches a definite answer: the entry carries either
// field, the rules accept them (done, newKeysChecked), or a write with them
// stripped succeeds where one with them present did not (refused: dropped
// for the rest of the run, newKeysChecked and newKeysRefused). A probe uses
// snapshotEntry, not the Start-time entry, so it carries the run's current
// spent and updatedAt rather than resurrecting stale ones.
//
// Two denials in a row are deliberately left ambiguous rather than called a
// bad credential: unlike Start (which runs once, so a second denial right
// after the first means the same credential failed twice under the same
// conditions), a heartbeat's two attempts can straddle an unrelated blip (a
// token refresh, a slow rules deploy), so newKeysChecked is left false and
// the next heartbeat's call probes again from scratch. heartbeat's own
// fallback (below) also re-arms this probe if a later, already-"accepted"
// write to the registry is refused.
func (s *Session) checkNewKeys(ctx context.Context) {
	s.mu.Lock()
	checked := s.newKeysChecked
	hasKeys := s.entry.Action != "" || s.entry.Tokens != 0
	s.mu.Unlock()
	if checked || !hasKeys {
		return
	}
	e := s.snapshotEntry()

	path := PathAgent(s.cfg.Slug, s.cfg.Run)
	write := func(v AgentEntry) error {
		return s.db.Patch(ctx, "", map[string]any{path: v})
	}
	if err := write(e); err == nil {
		s.mu.Lock()
		s.newKeysChecked = true
		s.mu.Unlock()
		s.dbOK()
		return
	} else if !errors.Is(err, rtdb.ErrPermission) {
		return // not a permission matter; the regular heartbeat reports it
	}
	e.Action, e.Tokens = "", 0
	err := write(e)
	if err != nil {
		return // both attempts denied: ambiguous, try again next beat
	}
	s.mu.Lock()
	s.newKeysChecked, s.newKeysRefused = true, true
	s.entry.Action, s.entry.Tokens = "", 0
	s.mu.Unlock()
	s.dbOK()
	s.log.Warn("budget: the dashboard rules predate 0.7.0, so the last action and token count are not shown; run fugaro init --firebase <firebase-project-id> to update them")
}

// checkCascadeKeys is checkNewKeys's twin for the credentials cascade's
// children of the run's own entry — `credential` and `waiting_until` (C1.3)
// — and it runs only after a heartbeat that carried one of them was refused
// with a permission error.
//
// It tells the benign refusal from a real one the only way a run can, by
// probing both spellings of the same entry: the entry as the heartbeat sent
// it, then the same entry without the two children. With them refused and
// without them accepted, the children are what the rules do not know — the
// rules downgrade C1.3's `credsRules` marker cannot catch, because an older
// Fugaro redeploying its own document leaves the marker at a generation it
// does not know — and the run halts named (C1.17) instead of dropping the
// write: a run whose credential state is silently absent is a run the
// dashboard shows as holding no credential at all, which is worse than a
// named failure. The write is not retried: the halt is the answer, and the
// operator's redeploy is what changes it.
//
// Only a run that wrote one of the children can meet this, and only a
// cascade run writes them: the credential's name comes from the service's
// answer (C1.10, C1.16) and `waiting_until` from a wait the service's
// exhausted-until started (C1.17). A non-cascade run's entry carries
// neither, so this probe never runs for it.
//
// Like checkNewKeys, the probe caches only a definite answer about the
// children — the entry without them refused too, which says they are not the
// cause — and stays armed otherwise. An outage, a 503 or any other
// non-permission error says nothing about the rules, and neither does a
// write that succeeds: the refusal that summoned the probe was a blip, but
// the next one may not be, and a probe that stood down after the first would
// leave the rest of the run writing an entry the rules silently strip —
// exactly the state the named halt exists to prevent. Nothing is lost by
// asking again: the probe runs only after a heartbeat the rules refused.
func (s *Session) checkCascadeKeys(ctx context.Context) {
	s.mu.Lock()
	probed := s.credsKeysProbed
	s.mu.Unlock()
	if probed {
		return
	}
	e := s.snapshotEntry()
	if e.Credential == "" && e.WaitingUntil == 0 {
		return
	}
	path := PathAgent(s.cfg.Slug, s.cfg.Run)
	write := func(v AgentEntry) error { return s.db.Patch(ctx, "", map[string]any{path: v}) }
	switch out := probeCascadeKeys(write, e); {
	case out == cascadeProbeChildrenRefused:
		s.dbOK()
		s.halt(Halt{Reason: ReasonCredsRulesDenied, Scope: ScopeRun, Detail: credsRulesDeniedDetail})
	case out.definite():
		// The children are not the cause: a definite answer, and the only one
		// this probe caches.
		s.mu.Lock()
		s.credsKeysProbed = true
		s.mu.Unlock()
	case out == cascadeProbeAccepted:
		// The entry the rules took carried the children, so this says nothing
		// about them: the probe stays armed. The write did land, so the
		// database is reachable.
		s.dbOK()
	}
}

// cascadeProbe is what one run of the probe learned about the deployed rules
// and the two children of the run's own entry.
type cascadeProbe int

const (
	// cascadeProbeAccepted: the entry was taken as the heartbeat sent it, with
	// the children in it. The refusal that summoned the probe was a blip or a
	// stale race with another writer of the same entry — and no answer about
	// the children at all.
	cascadeProbeAccepted cascadeProbe = iota
	// cascadeProbeUnavailable: a write failed for something that is not a
	// permission refusal (an outage), or the second write did not settle what
	// the first raised. The rules never answered.
	cascadeProbeUnavailable
	// cascadeProbeRefusedWithoutTheChildren: refused as the heartbeat sent it
	// and refused without the children too, so the children are not the cause.
	cascadeProbeRefusedWithoutTheChildren
	// cascadeProbeChildrenRefused: refused as the heartbeat sent it and
	// accepted without them: they are what the rules do not know.
	cascadeProbeChildrenRefused
)

// definite reports whether the probe reached an answer about the children,
// which is the only kind it may cache (M11). Every other outcome — the entry
// accepted with them, an outage, a second write that settled nothing — leaves
// the probe armed, so the next refused heartbeat asks again.
func (p cascadeProbe) definite() bool { return p == cascadeProbeRefusedWithoutTheChildren }

// probeCascadeKeys asks the deployed rules the one question a run can ask
// them: the entry as the heartbeat sent it, then the same entry without the
// cascade's children. write is the run's own patch of that entry.
func probeCascadeKeys(write func(AgentEntry) error, e AgentEntry) cascadeProbe {
	if err := write(e); err == nil {
		return cascadeProbeAccepted
	} else if !errors.Is(err, rtdb.ErrPermission) {
		return cascadeProbeUnavailable // not a permission matter; the heartbeat reports it
	}
	e.Credential, e.WaitingUntil = "", 0
	switch err := write(e); {
	case err == nil:
		return cascadeProbeChildrenRefused
	case errors.Is(err, rtdb.ErrPermission):
		return cascadeProbeRefusedWithoutTheChildren
	default:
		// The second write failed the way the first did not settle: an
		// outage in the middle of the probe tells it nothing about the
		// children.
		return cascadeProbeUnavailable
	}
}

// snapshotEntry is the entry as the next heartbeat sends it.
func (s *Session) snapshotEntry() AgentEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entry
	e.UpdatedAt = s.now().UnixMilli()
	switch {
	case s.used != nil:
		e.Spent = max(s.used(), 0)
	default:
		e.Spent = s.notionalTotal
	}
	return e
}

// heartbeat is one beat (R8): the kill switches are re-read as a backstop to
// the stream, and the registry entry goes out in the same update as the
// usage report.
func (s *Session) heartbeat(ctx context.Context) {
	s.checkKills(ctx, "kill-poll")
	s.checkNewKeys(ctx)
	e := s.snapshotEntry()
	err := s.flush(ctx, map[string]any{PathAgent(s.cfg.Slug, s.cfg.Run): e}, "heartbeat")
	if err != nil && errors.Is(err, rtdb.ErrPermission) && (e.Credential != "" || e.WaitingUntil != 0) {
		// The bundled write carried a cascade child into the refusal: find
		// out, with the two writes of its own, whether the deployed rules
		// are the reason, and halt named if they are (C1.17).
		s.checkCascadeKeys(ctx)
	}
	if err != nil && errors.Is(err, rtdb.ErrPermission) && (e.Action != "" || e.Tokens != 0) {
		// The bundled write just carried action or tokens into a permission
		// refusal: re-arm the dedicated probe so the next beat finds out,
		// with its own isolated write, whether they are really the cause
		// (checkNewKeys never blames the shared usage counters for this).
		s.mu.Lock()
		if !s.newKeysRefused {
			s.newKeysChecked = false
		}
		s.mu.Unlock()
	}
}

// outcomeStatuses are the statuses the rules accept in an outcome.
var outcomeStatuses = map[string]bool{"succeeded": true, "failed": true, "halted": true, "cancelled": true, "infra_error": true, "none": true}

// Finish ends the session: it stops the loops, tries again to release what
// an earlier release left, reports the last usage, writes the run's outcome
// once and deletes its registry entry. Every step is best effort within ctx:
// what cannot be done is left for the sweeper, which sees the entry. It is
// safe to call on a session that never started.
func (s *Session) Finish(ctx context.Context, status string) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	cancel, started := s.cancel, s.started
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.safely("finish", func() {
		s.mu.Lock()
		again := s.unrel
		s.unrel = 0
		s.mu.Unlock()
		if again > 0 {
			if err := s.release(ctx, again); err != nil {
				s.log.Warn("budget: the lease could not be released; it stays counted, which errs high, until the day ends", "error", err.Error())
			}
		}
		if err := s.flush(ctx, nil, "report"); err != nil && ctx.Err() == nil {
			s.log.Warn("budget: the last usage report failed", "error", err.Error())
		}
		if !outcomeStatuses[status] {
			status = "infra_error"
		}
		day := Day(s.now())
		out := Outcome{Status: status, RequestedBy: s.claims.RB}
		if err := s.db.Patch(ctx, "", map[string]any{PathOutcome(day, s.cfg.Slug, s.cfg.Run): out}); err != nil && ctx.Err() == nil {
			// A denial means the outcome is already there (rules: once).
			s.log.Warn("budget: writing the outcome failed", "error", err.Error())
		}
		if started {
			if err := s.db.Patch(ctx, "", map[string]any{PathAgent(s.cfg.Slug, s.cfg.Run): nil}); err != nil && ctx.Err() == nil {
				s.log.Warn("budget: removing the registry entry failed; the sweeper will", "error", err.Error())
			}
		}
	})
	s.grace.Stop()
}
