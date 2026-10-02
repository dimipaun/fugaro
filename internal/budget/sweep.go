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
	DeleteUsers(ctx context.Context, uids []string) error
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
			var e AgentEntry
			if err1 != nil || err2 != nil || slug == "" || run == "" || json.Unmarshal(tree[sk][rk], &e) != nil {
				s.warnf("skipping registry entry %s/%s: not a run's entry", sk, rk)
				rep.Kept++
				continue
			}
			if s.isLive(live, slug, run, e, now, stale) {
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

// remove records the run's crash and drops its entry in one atomic update.
func (s *Sweeper) remove(ctx context.Context, slug, run string, e AgentEntry, now time.Time) error {
	ledger := PathRun(slug, run)
	var raw json.RawMessage
	haveLedger, err := s.DB.Get(ctx, ledger, &raw)
	if err != nil {
		return err
	}
	// The outcome's day is the entry's last heartbeat when that is plausible
	// (so it does not move between sweeps), else today.
	day := Day(now)
	if hb := time.UnixMilli(e.UpdatedAt); e.UpdatedAt > 0 && hb.Before(now) && now.Sub(hb) < 8*24*time.Hour {
		day = Day(hb)
	}
	oc := PathOutcome(day, slug, run)
	var existing json.RawMessage
	haveOutcome, err := s.DB.Get(ctx, oc, &existing)
	if err != nil {
		return err
	}
	updates := map[string]any{PathAgent(slug, run): nil}
	if haveLedger && string(raw) != "null" {
		updates[ledger+"/crashed"] = true
	}
	if !haveOutcome {
		by := strings.TrimSpace(e.RequestedBy)
		if by == "" {
			by = "unknown"
		}
		updates[oc] = Outcome{Status: StatusInfraError, RequestedBy: clip(by, 200)}
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
	if err := s.Auth.DeleteUsers(ctx, del); err != nil {
		return 0, fmt.Errorf("deleting Firebase users: %w", err)
	}
	return len(del), nil
}
