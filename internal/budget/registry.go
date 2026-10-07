package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// maxEntryString is the rules' bound on every registry string.
const maxEntryString = 200

// clipString makes s one clean line of at most 200 bytes: the rules refuse
// longer strings, and registry text is untrusted anyway.
func clipString(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) > maxEntryString {
		s = strings.ToValidUTF8(s[:maxEntryString], "")
	}
	return s
}

func clipEntry(e *AgentEntry) {
	for _, p := range []*string{&e.Repo, &e.Workflow, &e.Title, &e.Stage, &e.Verify, &e.Coder, &e.Reviewer, &e.Recipe, &e.Auth, &e.PRURL, &e.Halted} {
		*p = clipString(*p)
	}
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
		s.log.Warn("budget: the database's rules predate recipes, so the registry entry goes without the recipe; run fugaro init to update the rules")
		entry.Recipe = ""
		s.mu.Lock()
		s.entry, s.noRecipe = entry, true
		s.mu.Unlock()
		err = s.retryBoot(ctx, "registry", write)
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
	clipEntry(&s.entry)
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
	e := s.snapshotEntry()
	_ = s.flush(ctx, map[string]any{PathAgent(s.cfg.Slug, s.cfg.Run): e}, "heartbeat")
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
