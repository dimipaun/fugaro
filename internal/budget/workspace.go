package budget

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The workspace's own spend meter (docs/workspaces/spec.md §5; D1301 as amended
// on 2026-11-04, after the post-merge security review, and reconciliation.md
// §8's O2). The figure the workspace's cap is measured against is taken where
// the money is spent — in the workspace's service, on the calls the workspace's
// own credentials pay for — by RESERVING the room for a call before it is made
// and SETTLING the call's usage when the response comes back. Reserve and
// settle are each ONE conditional write of ONE node in the workspace's own
// database, so two calls at the cap boundary cannot both take the last of the
// room.
//
// It is not a member's ledger and must never be read as one. The member's
// ledger (PathRun, PathSpendRepo) stays authoritative for the member's own
// caps: the two are different quantities, each governing its own cap, and
// neither is renamed into the other. What the two are compared by is the
// reconciliation alarm (workspace_reconcile.go), which reports a disagreement
// rather than resolving it in either direction.
//
// The node is keyed by the calendar month, because the workspace's cap is
// monthly (`cap.monthly_micros`, projectpolicy.WorkspaceCap): a new month reads
// an absent node, so the figure starts empty with no reset step and the months
// before it stay where they are as history. A call reserved at the last second
// of a month settles against the month it was RESERVED in — the month whose cap
// admitted it — which is why a reservation's handle carries its month rather
// than the settler recomputing one.
//
// Money is integer micro-dollars, as everywhere in this package.
//
// The subtree is the one the workspace's own database deploys its rules for
// (internal/budget/rules.WorkspaceMeterPath, which owns that database's shape):
// a second spelling of it would be a node the deployed document does not name.
// The name is spelled out rather than read from that package because that
// package's own tests import this one, so an import here is a cycle; the two
// are held to each other by TestWorkspaceMeterLivesUnderTheSubtreeTheRulesDeny.
const pathWorkspaceSpend = "meter"

// The meter's retries under contention. Every reserve and every settle of a
// workspace is a read-then-conditional-write of one month's node, so parallel
// runs in one workspace meet there; the waits are jittered rather than fixed
// because the collisions are simultaneous by construction.
const (
	// WorkspaceSettleAttempts bounds a settle's conditional writes: three times
	// a reserve's (SetAttempts). The two halves are not symmetric under
	// contention — a reserve that gives up refuses a call that has not been made
	// and costs nothing, while a settle that gives up refuses a call that HAS
	// been paid for, and its usage is then recorded only by a later attempt or
	// left to age out (WorkspaceReservationTTL) — so the settle retries harder.
	WorkspaceSettleAttempts = 3 * SetAttempts
	// workspaceRetryBackoff is the first wait between two attempts, and
	// workspaceRetryBackoffMax the last: the wait grows with each lost write.
	workspaceRetryBackoff    = 20 * time.Millisecond
	workspaceRetryBackoffMax = 250 * time.Millisecond
)

// WorkspaceReservationTTL is how long room may stay taken for a call that never
// settles. A reservation is the meter's whole hold on room, and the process
// that took it can be gone with it — a crash, a restart, a disconnect, or a
// settle that was refused and that nothing retried — which is why the hold
// cannot be for the month: a leaked one would shrink the workspace's cap and
// widen OvershootBound and the reconciliation alarm's bound until the month
// turned, and the cap would be under a ceiling nobody set.
//
// The age is stated rather than tuned: the call the workspace's service pays
// for is one proxied model request, and no model request is in flight for
// hours, so a hold that old is a hold whose call cannot still be in flight. An
// expired hold is released by the READ as well as by the next write
// (WorkspaceSpend.Live), so a workspace with no traffic after a crash still
// reads its own room.
const WorkspaceReservationTTL = 6 * time.Hour

// monthRE is the spelling of a month segment: MonthKey's own output, and
// nothing else. A month is a path segment here, so a value that is not one
// would put the node somewhere else in the tree.
var monthRE = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// MonthKey is the month a moment falls in, in UTC, as the meter's path segment:
// the workspace's cap is monthly and the workspace's service bills in UTC, so
// the boundary is UTC midnight on the first of the month.
func MonthKey(t time.Time) string { return t.UTC().Format("2006-01") }

// PathWorkspaceSpend is one workspace's meter for one month, relative to the
// workspace's own database root and without a leading slash. The caller passes
// the raw workspace name (validated long before a path is built, as
// projectpolicy.PathWorkspace's is) and a month from MonthKey.
//
// The name is a segment of the path for the reason the record's own path
// carries one (projectpolicy.PathWorkspace), and the rules the database runs
// under deny the whole subtree rather than a node of a fixed depth, so a
// writer below it is denied with the subtree (internal/budget/rules).
func PathWorkspaceSpend(name, month string) string {
	if !monthRE.MatchString(month) {
		panic("budget: " + month + " is not a month from MonthKey")
	}
	return pathWorkspaceSpend + "/" + seg("workspace", name) + "/" + month
}

// WorkspaceCeiling is the workspace's own spend ceiling for the month, as the
// workspace's record states it. A ceiling that is not set is NOT a ceiling of
// zero: a ceiling of zero would refuse every call the workspace's credentials
// pay for, which is not what a workspace that states no cap means.
type WorkspaceCeiling struct {
	Monthly Micros
	Set     bool
}

// WorkspaceReservation is one call in flight: the member the call belongs to,
// the room it took and when it was taken. It is what a settle finds by the
// call's id.
type WorkspaceReservation struct {
	Member string `json:"member"`
	Amount Micros `json:"amount"`
	At     int64  `json:"at"` // Unix milliseconds, the moment the room was taken
}

// WorkspaceSpend is the meter node: one month of the workspace's own figure.
// Members holds what the calls that have settled cost, per member; Reservations
// holds the room taken for the calls that are still in flight. A member with no
// entry has spent nothing, and the two maps are written as empty objects rather
// than left out, so a reader never has to tell "no entry" from "not read".
type WorkspaceSpend struct {
	Month        string                          `json:"month"`
	Members      map[string]Micros               `json:"members"`
	Reservations map[string]WorkspaceReservation `json:"reservations"`
}

// Settled is what the calls that have settled cost, over every member, as one
// figure: the workspace's own count, and the half of it a reconciliation alarm
// compares against a member's ledger.
func (s WorkspaceSpend) Settled() Micros {
	var total Micros
	for _, m := range s.Members {
		total = add(total, m)
	}
	return total
}

// Reserved is the room the reservations the node holds count, as the node
// states them. It is not what a figure is reported from: a reservation whose
// call can no longer be in flight is not room taken any more, and Live is where
// that is decided.
func (s WorkspaceSpend) Reserved() Micros {
	var total Micros
	for _, r := range s.Reservations {
		total = add(total, r.Amount)
	}
	return total
}

// ReservedBy is the room the calls in flight hold for ONE member. It is what a
// bound on that member's own figures is made of: what another member has in
// flight is not a difference this member's ledger can account for.
func (s WorkspaceSpend) ReservedBy(member string) Micros {
	var total Micros
	for _, r := range s.Reservations {
		if r.Member == member {
			total = add(total, r.Amount)
		}
	}
	return total
}

// Live is the month as it stands at now: a reservation taken longer ago than
// WorkspaceReservationTTL is released, and the node it is released out of is
// what a figure is computed from. A reservation the node holds with no moment
// at all is released too — it cannot be placed in time, and room held for a
// call nobody can date is room held until the month ends.
//
// A moment that is not stated expires nothing: a caller with no clock is
// handed the node as it was read rather than one it guessed an age for.
func (s WorkspaceSpend) Live(now time.Time) WorkspaceSpend {
	if now.IsZero() {
		return s
	}
	stale := false
	for _, r := range s.Reservations {
		if s.expired(r, now) {
			stale = true
			break
		}
	}
	if !stale {
		return s
	}
	live := s
	live.Reservations = make(map[string]WorkspaceReservation, len(s.Reservations))
	for call, r := range s.Reservations {
		if !s.expired(r, now) {
			live.Reservations[call] = r
		}
	}
	return live
}

// expired reports whether a reservation's call can no longer be in flight.
func (s WorkspaceSpend) expired(r WorkspaceReservation, now time.Time) bool {
	return now.UnixMilli()-r.At >= WorkspaceReservationTTL.Milliseconds()
}

// OvershootBound is how far past its ceiling the workspace's settled figure can
// still get, stated as a number rather than as an adjective: the reservations in
// flight at now. It is that and not something else because a reservation is only
// ever taken out of room the ceiling had left (ReserveWorkspaceSpend refuses
// otherwise) and a settle only ever turns one of those reservations into a
// settled amount, so `Settled` cannot pass the ceiling except through the calls
// still in flight — and each of those was admitted at the call's own worst case
// (WorkspaceReserve.Amount, which is what the service reserves), so no usage of
// that call can exceed it without the caller having named a worst case the call
// went past. A settle that records more than the reservation it ends is the one
// way past this bound, and it is past it by the difference: the figure is the
// call's real cost either way, and counting the difference is the safe
// direction (the reconciliation alarm reports it rather than hiding it). A
// workspace that states no ceiling has no bound to state.
func (s WorkspaceSpend) OvershootBound(now time.Time) Micros { return s.Live(now).Reserved() }

// Room is the workspace's figure at one moment, as a caller reports it.
type WorkspaceRoom struct {
	// Ceiling is the ceiling the room was computed against; Set false when the
	// workspace states none, and then Headroom is meaningless.
	Ceiling WorkspaceCeiling
	// Settled is what the calls that have settled cost this month.
	Settled Micros
	// Reserved is the room the calls in flight hold.
	Reserved Micros
	// Headroom is what the ceiling still leaves: Settled + Reserved + Headroom
	// is the ceiling, and it is 0 when the ceiling is reached or passed. It is
	// the number the service answers a member with, and it is advisory: the
	// member's gateway holds it for the run and may stop one early on it, while
	// the cap itself is never the gateway's to decide (W8 owns the stop).
	Headroom Micros
}

// Fits reports whether a reservation of amount may be taken: it does when the
// workspace states no ceiling, and otherwise only while the settled figure, the
// room in flight and the new amount together stay within the ceiling. It is the
// whole of the enforcement, and it is one comparison because the room in flight
// is part of it: a call is refused before it is made rather than discovered
// afterwards.
func (r WorkspaceRoom) Fits(amount Micros) bool {
	if !r.Ceiling.Set {
		return true
	}
	return add(r.Settled, add(r.Reserved, amount)) <= r.Ceiling.Monthly
}

// Room is the figure the meter node holds at now, read against a ceiling: it is
// the LIVE node (Live), so a hold whose call can no longer be in flight is not
// room taken any more.
func (s WorkspaceSpend) Room(c WorkspaceCeiling, now time.Time) WorkspaceRoom {
	live := s.Live(now)
	r := WorkspaceRoom{Ceiling: c, Settled: live.Settled(), Reserved: live.Reserved()}
	if c.Set {
		if used := add(r.Settled, r.Reserved); used < c.Monthly {
			r.Headroom = c.Monthly - used
		}
	}
	return r
}

// WorkspaceHold is the handle a reservation returns and a settle needs: which
// workspace, which month the room was taken out of, and which call. A settle
// takes the month from here rather than from its own clock, so a call taken at
// the last second of a month settles against the month whose cap admitted it.
type WorkspaceHold struct {
	Name   string
	Month  string
	Call   string
	Member string
	Amount Micros
}

// WorkspaceReserve is one reservation to take. Now decides the month and stamps
// the reservation; it is required, so a caller cannot reserve against a month it
// did not mean.
type WorkspaceReserve struct {
	Name    string
	Call    string
	Member  string
	Amount  Micros
	Ceiling WorkspaceCeiling
	Now     time.Time
}

// WorkspaceSettle is one settle to make: what the call actually cost, the
// ceiling the room is reported against (the workspace's record may have been
// narrowed since the reservation was taken, so it is read afresh rather than
// carried in the hold), and the moment it is made, which is what decides
// whether the hold it ends is still one. It is required, like a reservation's
// own moment: a settle that names no time is a settle that cannot say whether
// its call can still be in flight.
type WorkspaceSettle struct {
	Actual  Micros
	Ceiling WorkspaceCeiling
	Now     time.Time
}

// WorkspaceSpendDB is the part of a database the meter needs: the workspace's
// OWN database. *rtdb.Client is one. It is a read with the ETag and one
// conditional write, and nothing else: the meter takes the whole node and puts
// it back, so the reservation and the settle are each one write and the
// workspace's service needs no path-level permission of its own.
type WorkspaceSpendDB interface {
	WorkspaceSpendReader
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// WorkspaceSpendReader is the read-only half of the same database, for a caller
// that must hold no write at all: the headroom answer, and the reconciliation
// alarm's read of the service's own count.
type WorkspaceSpendReader interface {
	Get(ctx context.Context, path string, out any) (found bool, err error)
}

var (
	// ErrWorkspaceCeilingReached refuses the call that would pass the
	// workspace's own ceiling. It is a refusal BEFORE the call is made, which
	// is the whole of the enforcement.
	ErrWorkspaceCeilingReached = errors.New("the workspace's own ceiling would be passed by this call")
	// ErrWorkspaceCallReserved refuses a second reservation for a call that
	// already holds one: one call, one reservation, or the same usage could be
	// paid for twice.
	ErrWorkspaceCallReserved = errors.New("this call already holds a reservation in the workspace's meter")
	// ErrWorkspaceReservationUnknown refuses a settle whose call holds no
	// reservation. It is a refusal and not a no-op: a usage the meter cannot
	// place is a usage the service cannot record, and a call whose cost cannot
	// be recorded spends against a cap the service cannot see.
	ErrWorkspaceReservationUnknown = errors.New("no reservation for this call in the workspace's meter")
	// ErrWorkspaceSpendContention: the meter kept changing under every
	// conditional write the caller's half of the meter is allowed
	// (SetAttempts for a reserve, WorkspaceSettleAttempts for a settle).
	ErrWorkspaceSpendContention = errors.New("the workspace's meter kept changing under the write")
	// ErrWorkspaceSpendUnreadable: the node is not a figure the meter can read
	// (a negative amount, a call or a member with no name, a month that is not
	// the one its path names). It is refused rather than repaired: a negative
	// amount read as room would fail open.
	ErrWorkspaceSpendUnreadable = errors.New("the workspace's meter node is not a figure this Fugaro can read")
)

// ReadWorkspaceSpend reads one month of a workspace's meter. found is false for
// a month with no node, which is the state every month starts in and not a
// refusal.
//
// The node is returned as it is STORED, expired reservations included: this
// read holds no write, so it cannot clean up after a call that never settled. A
// caller that reports a figure prunes with Live first, and a caller that writes
// the node prunes it on the way (updateWorkspaceSpend), so the stale entries
// last only until the next write to that month.
func ReadWorkspaceSpend(ctx context.Context, db WorkspaceSpendReader, name, month string) (WorkspaceSpend, bool, error) {
	path := PathWorkspaceSpend(name, month)
	var s WorkspaceSpend
	found, err := db.Get(ctx, path, &s)
	if err != nil {
		return WorkspaceSpend{}, false, err
	}
	if !found {
		return WorkspaceSpend{}, false, nil
	}
	if s.Month != "" && s.Month != month {
		return WorkspaceSpend{}, true, fmt.Errorf("%w: its month is %q and its path names %q", ErrWorkspaceSpendUnreadable, s.Month, month)
	}
	s.Month = month
	if err := s.validate(); err != nil {
		return WorkspaceSpend{}, true, err
	}
	return s, true, nil
}

// ReadWorkspaceRoom answers the headroom question for one moment: what the
// workspace's own ceiling still leaves, this month. It is the number the service
// delivers to a member and the number the reconciliation alarm counts against a
// member's ledger, read through the read-only interface so a caller that only
// asks holds no write.
func ReadWorkspaceRoom(ctx context.Context, db WorkspaceSpendReader, name string, c WorkspaceCeiling, now time.Time) (WorkspaceRoom, error) {
	s, _, err := ReadWorkspaceSpend(ctx, db, name, MonthKey(now))
	if err != nil {
		return WorkspaceRoom{}, err
	}
	return s.Room(c, now), nil
}

// validate refuses a node the meter cannot read. A member or a call with no
// name, or a negative amount, is not a figure: RTDB can hold one, and reading it
// as room would fail open, which is the same reading negativeState gives a run's
// own ledger.
func (s WorkspaceSpend) validate() error {
	for member, spent := range s.Members {
		if member == "" {
			return fmt.Errorf("%w: a member has no name", ErrWorkspaceSpendUnreadable)
		}
		if spent < 0 {
			return fmt.Errorf("%w: member %q holds %d micro-dollars", ErrWorkspaceSpendUnreadable, member, spent)
		}
	}
	for call, r := range s.Reservations {
		if call == "" || r.Member == "" {
			return fmt.Errorf("%w: a reservation names no call or no member", ErrWorkspaceSpendUnreadable)
		}
		if r.Amount <= 0 {
			return fmt.Errorf("%w: the reservation for %q holds %d micro-dollars", ErrWorkspaceSpendUnreadable, call, r.Amount)
		}
	}
	return nil
}

// ReserveWorkspaceSpend takes the room for one call out of the workspace's own
// month, in one conditional write: the node is read with its ETag, the room is
// checked and the reservation added, and the node is put back only if nothing
// else wrote it meanwhile. A lost race is re-read, with a jittered wait between
// attempts, up to SetAttempts times: a reserve gives up more readily than a
// settle (WorkspaceSettleAttempts), whose call has already been paid for.
//
// ErrWorkspaceCeilingReached is the refusal of a call that would pass the
// ceiling; the room returned beside it is the figure as it was read, so a caller
// may still answer a member's headroom question. Any other error means the
// figure could not be taken at all, and the room is not to be used.
func ReserveWorkspaceSpend(ctx context.Context, db WorkspaceSpendDB, r WorkspaceReserve) (WorkspaceHold, WorkspaceRoom, error) {
	if r.Name == "" || r.Call == "" || r.Member == "" {
		return WorkspaceHold{}, WorkspaceRoom{}, errors.New("budget: a reservation names the workspace, the call and the member")
	}
	if r.Amount <= 0 {
		return WorkspaceHold{}, WorkspaceRoom{}, fmt.Errorf("budget: a reservation of %d micro-dollars takes no room: the amount is the call's worst case and is positive", r.Amount)
	}
	if r.Now.IsZero() {
		return WorkspaceHold{}, WorkspaceRoom{}, errors.New("budget: a reservation states when it was taken: the month it is taken out of is derived from it")
	}
	month := MonthKey(r.Now)
	var hold WorkspaceHold
	spend, err := updateWorkspaceSpend(ctx, db, r.Name, month, r.Now, SetAttempts, func(s *WorkspaceSpend) error {
		if _, ok := s.Reservations[r.Call]; ok {
			return ErrWorkspaceCallReserved
		}
		if !s.Room(r.Ceiling, r.Now).Fits(r.Amount) {
			return ErrWorkspaceCeilingReached
		}
		s.Reservations[r.Call] = WorkspaceReservation{Member: r.Member, Amount: r.Amount, At: r.Now.UnixMilli()}
		hold = WorkspaceHold{Name: r.Name, Month: month, Call: r.Call, Member: r.Member, Amount: r.Amount}
		return nil
	})
	room := spend.Room(r.Ceiling, r.Now)
	if err != nil {
		if errors.Is(err, ErrWorkspaceCeilingReached) {
			return WorkspaceHold{}, room, err
		}
		return WorkspaceHold{}, WorkspaceRoom{}, err
	}
	return hold, room, nil
}

// SettleWorkspaceSpend records what one call cost, in one conditional write: the
// reservation is removed and the amount is added to the member's settled figure,
// so the room the call held becomes money spent in the same transaction that
// ends it. A settle that cannot be written is an error the caller refuses the
// call on: a call whose cost the service cannot record spends against a cap it
// cannot see.
//
// The amount recorded is the response's own usage and not the reservation: the
// reservation is a worst case, and the figure the cap is measured against is
// what the call really cost. A settle never refuses on the ceiling — the money
// is spent either way, and a meter that refused to record it would be a meter
// that under-counts — so the ceiling it is reported against is the one the
// caller passes.
//
// The room it reports is the room of the month the call was counted against,
// which is the month its reservation was taken in: a settle that crosses the
// month boundary reports the month the call belonged to, and the new month's
// room is a fresh read (ReadWorkspaceRoom at the new month) rather than a figure
// this settle wrote.
func SettleWorkspaceSpend(ctx context.Context, db WorkspaceSpendDB, h WorkspaceHold, s WorkspaceSettle) (WorkspaceRoom, error) {
	if h.Name == "" || h.Month == "" || h.Call == "" {
		return WorkspaceRoom{}, errors.New("budget: a settle needs the handle a reservation returned")
	}
	if s.Actual < 0 {
		return WorkspaceRoom{}, fmt.Errorf("budget: a settle records what the call cost, and %d micro-dollars is negative", s.Actual)
	}
	if s.Now.IsZero() {
		return WorkspaceRoom{}, errors.New("budget: a settle states when it is made: a hold whose call cannot still be in flight is not the hold it ends")
	}
	spend, err := updateWorkspaceSpend(ctx, db, h.Name, h.Month, s.Now, WorkspaceSettleAttempts, func(sp *WorkspaceSpend) error {
		res, ok := sp.Reservations[h.Call]
		if !ok {
			return ErrWorkspaceReservationUnknown
		}
		if h.Member != "" && res.Member != h.Member {
			return fmt.Errorf("%w: the reservation for %q belongs to member %q and the handle names %q", ErrWorkspaceReservationUnknown, h.Call, res.Member, h.Member)
		}
		delete(sp.Reservations, h.Call)
		sp.Members[res.Member] = add(sp.Members[res.Member], s.Actual)
		return nil
	})
	if err != nil {
		return WorkspaceRoom{}, err
	}
	return spend.Room(s.Ceiling, s.Now), nil
}

// updateWorkspaceSpend is the one transaction both halves of the meter use: read
// the month's node with its ETag, drop the holds whose calls can no longer be in
// flight (Live), let f change it, and put the whole node back only if nothing
// wrote it meanwhile. RTDB has no server-side transaction, so this is the
// compare-and-set the budget's other conditional writes use (SetKill), and it is
// what makes a reservation and a settle each atomic with the workspace's own
// database.
//
// Pruning here is what makes an expired hold leave the node rather than linger
// to the month's end: the read that reports a figure treats it as gone, and the
// next write to that month removes it. A refusal writes nothing, so the drop is
// then the read's alone.
//
// The callback must return BEFORE it changes anything when it refuses, so a
// refusal writes nothing: the node the caller is handed back is the LIVE one
// (f runs on the node Live returned), which is the figure the refusal was made
// against and the figure the caller reports.
func updateWorkspaceSpend(ctx context.Context, db WorkspaceSpendDB, name, month string, now time.Time, attempts int, f func(*WorkspaceSpend) error) (WorkspaceSpend, error) {
	path := PathWorkspaceSpend(name, month)
	for attempt := 1; attempt <= attempts; attempt++ {
		var cur WorkspaceSpend
		etag, found, err := db.GetETag(ctx, path, &cur)
		if err != nil {
			return WorkspaceSpend{}, err
		}
		if found && cur.Month != "" && cur.Month != month {
			return WorkspaceSpend{}, fmt.Errorf("%w: its month is %q and its path names %q", ErrWorkspaceSpendUnreadable, cur.Month, month)
		}
		if !found {
			cur = WorkspaceSpend{}
		}
		cur.Month = month
		if err := cur.validate(); err != nil {
			return WorkspaceSpend{}, err
		}
		cur = cur.Live(now)
		if cur.Members == nil {
			cur.Members = map[string]Micros{}
		}
		if cur.Reservations == nil {
			cur.Reservations = map[string]WorkspaceReservation{}
		}
		if err := f(&cur); err != nil {
			return cur, err
		}
		err = db.PutIfMatch(ctx, path, etag, cur)
		if errors.Is(err, rtdb.ErrPrecondition) {
			if attempt == attempts {
				break
			}
			if werr := workspaceWait(ctx, workspaceJitter(attempt)); werr != nil {
				return WorkspaceSpend{}, werr
			}
			continue
		}
		if err != nil {
			return WorkspaceSpend{}, err
		}
		return cur, nil
	}
	return WorkspaceSpend{}, ErrWorkspaceSpendContention
}

// workspaceJitter is the wait before the attempt after the n-th lost write: the
// base grows with n and is spread out, the shape Session.jitter gives a denied
// write, so two calls that collided on the month's node do not collide again.
func workspaceJitter(n int) time.Duration {
	base := min(workspaceRetryBackoff*time.Duration(n), workspaceRetryBackoffMax)
	return base/2 + time.Duration(rand.Int64N(int64(base)+1))
}

// workspaceWait waits d, or returns the context's error: a service that is
// shutting down does not keep retrying a write nobody will serve.
func workspaceWait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
