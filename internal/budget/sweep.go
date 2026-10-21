package budget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The sweeper (design D12, §6.5) is the history job's --sweep mode. It holds
// database admin rights, so it is written to need very little from what runs
// wrote:
//
//   - A registry entry goes when the run has no live execution and its
//     heartbeat is not fresh. The heartbeat time and every other field of the
//     entry or the ledger are run-written: a time in the future or none counts
//     as stale, and an outcome or an expiry written by the run never excuses it
//     from the sweep. The only evidence the sweeper acts on is the platform's
//     list of executions, and only when that list was read in full.
//   - It never releases anything. A crashed run's outstanding amount stays
//     counted in the day's counters (errs high, never low): the sweeper writes
//     no counter, no cap, no switch, no mode, nothing under /config. Its
//     writes are the leaves runs/<slug>/<run>/crashed, outcomes/<day>/<slug>/<run>
//     (create only, so a recorded outcome is kept) and the deletion of
//     agents/<slug>/<run>, one atomic update per run.
//   - It deletes only Firebase users named r~<slug>~<run> that the Identity
//     Toolkit says were created more than UserMaxAge ago and whose run is not
//     in the registry any more or still live.

// Sweep defaults.
const (
	// DefaultStaleAfter is how long an entry may go without a heartbeat
	// (every 15 s) before an execution-less run is swept. It is far above
	// the heartbeat and the time a finished run takes to delete its entry.
	DefaultStaleAfter = 5 * time.Minute
	// DefaultUserMaxAge is the age after which a run's Firebase user is
	// deleted: runs last at most a day, their tokens an hour.
	DefaultUserMaxAge = 48 * time.Hour
	// futureSkew is how far ahead of the sweeper's clock a heartbeat may be
	// and still count as fresh.
	futureSkew = time.Minute
	// wrongTypedAfter is how old an entry whose fields have the wrong types
	// must be before it is swept: runs last at most a day.
	wrongTypedAfter = 26 * time.Hour
	// StatusInfraError is the outcome the sweeper records for a run that
	// vanished.
	StatusInfraError = "infra_error"
)

// Executions lists the project's Fugaro executions; *gcp.Backend does.
type Executions interface {
	List(ctx context.Context, f backend.ListFilter) ([]backend.Execution, error)
}

// Users is the Firebase user directory; *AuthAdmin is the real one.
type Users interface {
	ListUsers(ctx context.Context) ([]AuthUser, error)
	// DeleteUsers returns the users it was told were not deleted.
	DeleteUsers(ctx context.Context, uids []string) ([]UserFailure, error)
}

// Sweeper is one sweep's wiring.
type Sweeper struct {
	DB    *rtdb.Client
	Execs Executions
	Auth  Users
	// JobOf names the Cloud Run job of a repository's workflow (gcp.JobName).
	JobOf func(slug, workflow string) string
	Now   func() time.Time
	// StaleAfter and UserMaxAge default to DefaultStaleAfter and
	// DefaultUserMaxAge.
	StaleAfter, UserMaxAge time.Duration
	// Warn receives what was skipped; nil discards it.
	Warn func(string)
}

// SweepReport says what a sweep did.
type SweepReport struct {
	Removed      []string // "<slug>/<run>" of the removed entries
	Kept         int      // registry entries left alone
	UsersDeleted int
}

// RunUID is the Firebase uid of a run (token.UID; a test holds them equal).
func RunUID(slug, run string) string { return "r~" + slug + "~" + run }

const uidPrefix = "r~"

func (s *Sweeper) warnf(format string, args ...any) {
	if s.Warn != nil {
		s.Warn(fmt.Sprintf(format, args...))
	}
}

// Sweep does one pass. A failure to list executions or to read the registry
// stops it before anything is written; a failure on one run or on the user
// cleanup is reported at the end, after the rest was done.
func (s *Sweeper) Sweep(ctx context.Context) (SweepReport, error) {
	var rep SweepReport
	now := s.Now()
	stale := s.StaleAfter
	if stale <= 0 {
		stale = DefaultStaleAfter
	}
	maxAge := s.UserMaxAge
	if maxAge <= 0 {
		maxAge = DefaultUserMaxAge
	}

	execs, err := s.Execs.List(ctx, backend.ListFilter{})
	if err != nil {
		return rep, fmt.Errorf("listing executions: %w", err)
	}
	var tree map[string]map[string]json.RawMessage
	if _, err := s.DB.Get(ctx, "agents", &tree); err != nil {
		return rep, fmt.Errorf("reading the registry: %w", err)
	}

	live := liveExecutions(execs)
	var errs []error
	keep := map[string]bool{} // uids of runs still in the registry
	slugs := make([]string, 0, len(tree))
	for k := range tree {
		slugs = append(slugs, k)
	}
	sort.Strings(slugs)
	for _, sk := range slugs {
		runs := make([]string, 0, len(tree[sk]))
		for k := range tree[sk] {
			runs = append(runs, k)
		}
		sort.Strings(runs)
		for _, rk := range runs {
			slug, err1 := Unkey(sk)
			run, err2 := Unkey(rk)
			if err1 != nil || err2 != nil || slug == "" || run == "" {
				s.warnf("skipping registry entry %s/%s: not a run's entry", sk, rk)
				rep.Kept++
				continue
			}
			var e AgentEntry
			entryStale := stale
			if json.Unmarshal(tree[sk][rk], &e) != nil {
				// A run wrote fields of the wrong types. Read what can be
				// read, and sweep it once it is old enough that no run can
				// still be using it.
				e = looseEntry(tree[sk][rk])
				if e.UpdatedAt == 0 && e.StartedAt == 0 {
					e.StartedAt = runIDTime(run).UnixMilli()
				}
				entryStale = wrongTypedAfter
				s.warnf("registry entry %s/%s has fields of the wrong types", sk, rk)
			}
			if s.isLive(live, slug, run, e, now, entryStale) {
				keep[RunUID(slug, run)] = true
				rep.Kept++
				continue
			}
			if err := s.remove(ctx, slug, run, e, now); err != nil {
				errs = append(errs, fmt.Errorf("sweeping %s/%s: %w", slug, run, err))
				keep[RunUID(slug, run)] = true
				rep.Kept++
				continue
			}
			rep.Removed = append(rep.Removed, slug+"/"+run)
		}
	}

	n, err := s.deleteUsers(ctx, now, maxAge, keep)
	rep.UsersDeleted = n
	if err != nil {
		errs = append(errs, err)
	}
	return rep, errors.Join(errs...)
}

// executionIndex is what the active executions say about who is alive.
type executionIndex struct {
	runs map[string]bool // "<slug>/<run>" of active executions that name their run
	anon map[string]bool // jobs with an active execution that does not
}

func liveExecutions(execs []backend.Execution) executionIndex {
	ix := executionIndex{runs: map[string]bool{}, anon: map[string]bool{}}
	for _, x := range execs {
		if x.State.Terminal() {
			continue
		}
		if x.Run != "" {
			ix.runs[x.Run] = true
		} else {
			ix.anon[x.Job] = true
		}
	}
	return ix
}

// isLive reports whether the entry must stay. Everything about the entry is
// run-written, so it can only keep itself by being plausible: a fresh
// heartbeat, or an active execution (named by the platform) that is its own.
// An execution that does not say which run it runs counts for every run of
// its job, which errs towards keeping.
func (s *Sweeper) isLive(ix executionIndex, slug, run string, e AgentEntry, now time.Time, stale time.Duration) bool {
	if ix.runs[slug+"/"+run] {
		return true
	}
	if len(ix.anon) > 0 {
		if e.Workflow == "" || s.JobOf == nil {
			return true
		}
		// JobOf names the plain workflow's job, never
		// gcp.WorkflowVariant(e.Workflow, token): a named run's anonymous
		// execution (one the platform doesn't say which run it belongs to)
		// would be looked up under the wrong job here. Backlog item, not
		// fixed here (review, named oauth tokens Task 6 round 2): in
		// practice near-zero impact, since every execution this package
		// starts sets FUGARO_RUN, so ix.anon only ever matters for an
		// execution some other process started, which a named run's own
		// variant job never has reason to see.
		if ix.anon[s.JobOf(slug, e.Workflow)] {
			return true
		}
	}
	hb := e.UpdatedAt
	if hb == 0 {
		hb = e.StartedAt
	}
	if hb <= 0 {
		return false
	}
	age := now.Sub(time.UnixMilli(hb))
	return age < stale && age > -futureSkew
}

// looseEntry reads the fields of an entry one by one, ignoring those of the
// wrong type.
func looseEntry(raw json.RawMessage) AgentEntry {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	var e AgentEntry
	get := func(k string, v any) { _ = json.Unmarshal(m[k], v) }
	get("workflow", &e.Workflow)
	get("requestedBy", &e.RequestedBy)
	get("startedAt", &e.StartedAt)
	get("updatedAt", &e.UpdatedAt)
	return e
}

// runIDTime is the UTC time named by the head of a run id
// (20060102-150405-xxxx), and the zero time when the id is shorter than that
// head or the head is not such a timestamp. It reads the head only — the rest
// of the id is not checked here, and callers compare the result against a day
// they have, treating the zero time as "no launch day", which is the stale end
// of that comparison. Review nit: the doc claimed to be "the time a run id was
// made", which is more than this parses.
func runIDTime(run string) time.Time {
	if len(run) < 15 {
		return time.Time{}
	}
	t, err := time.Parse("20060102-150405", run[:15])
	if err != nil {
		return time.Time{}
	}
	return t
}

// remove records the run's crash and drops its entry in one atomic update.
func (s *Sweeper) remove(ctx context.Context, slug, run string, e AgentEntry, now time.Time) error {
	ledger := PathRun(slug, run)
	var raw json.RawMessage
	haveLedger, err := s.DB.Get(ctx, ledger, &raw)
	if err != nil {
		return err
	}
	// The outcome's day is the entry's last heartbeat when that is plausible
	// (so it does not move between sweeps), else today — and never before the
	// run id's own launch day. The heartbeat is run-written (the rules' AMOUNT
	// && RBOK allows any non-negative number), so a run can date it before it
	// launched and land its outcome on a day no liveness scan reads, which
	// would keep it admitted until its fx: W2, reached through the sweeper.
	// The launch day is the run id's, which no run token writes.
	//
	// A run id dated ahead of this clock is the one branch where the launch
	// day cannot be the day: the rules' DAYOK refuses a future day, and the
	// liveness scan reads the database's window around today (today+1 down to
	// today-1; checkLive) — so the outcome goes on today, which is inside it,
	// rather than falling back to the older, run-written heartbeat day —
	// otherwise a run id dated ahead of the clock and a heartbeat days old
	// would put the outcome outside the window again, from the far side.
	today := Day(now)
	day := today
	if hb := time.UnixMilli(e.UpdatedAt); e.UpdatedAt > 0 && hb.Before(now) && now.Sub(hb) < 8*24*time.Hour {
		day = Day(hb)
	}
	if launch := runIDTime(run); !launch.IsZero() {
		switch ld := Day(launch); {
		case ld > today:
			day = today
		case ld > day:
			day = ld
		}
	}
	oc := PathOutcome(day, slug, run)
	var existing json.RawMessage
	etag, haveOutcome, err := s.DB.GetETag(ctx, oc, &existing)
	if err != nil {
		return err
	}
	created := false
	if !haveOutcome {
		by := strings.TrimSpace(e.RequestedBy)
		if by == "" {
			by = "unknown"
		}
		// Only if still absent: a run's own outcome, written meanwhile, wins.
		switch err := s.DB.PutIfMatch(ctx, oc, etag, Outcome{Status: StatusInfraError, RequestedBy: clip(by, 200)}); {
		case err == nil:
			created = true
		case !errors.Is(err, rtdb.ErrPrecondition):
			return err
		}
	}
	updates := map[string]any{PathAgent(slug, run): nil}
	// A run that already has an outcome ended in a way it recorded: the
	// sweeper does not call it crashed.
	if created && haveLedger && string(raw) != "null" {
		updates[ledger+"/crashed"] = true
	}
	return s.DB.Patch(ctx, "", updates)
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// deleteUsers deletes the runs' Firebase users older than maxAge.
func (s *Sweeper) deleteUsers(ctx context.Context, now time.Time, maxAge time.Duration, keep map[string]bool) (int, error) {
	users, err := s.Auth.ListUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing Firebase users: %w", err)
	}
	var del []string
	for _, u := range users {
		if strings.HasPrefix(u.UID, uidPrefix) && !keep[u.UID] && now.Sub(u.Created) > maxAge {
			del = append(del, u.UID)
		}
	}
	sort.Strings(del)
	if len(del) == 0 {
		return 0, nil
	}
	failed, err := s.Auth.DeleteUsers(ctx, del)
	for _, f := range failed {
		s.warnf("Firebase user %s was not deleted: %s", f.UID, f.Message)
	}
	if err != nil {
		return 0, fmt.Errorf("deleting Firebase users: %w", err)
	}
	return max(len(del)-len(failed), 0), nil
}
