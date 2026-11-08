package budget

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// The build lease (D1543, F19, F22): an image build reserves its estimated
// cost before it is submitted, the way a run reserves its own headroom.
//
// It lives under a key of its own — buildleases/<slug>/<id>, never runs/ — so
// no record reader (runstore, watch, the console) mistakes it for a run, and
// it carries the same three amounts a run's ledger does: what it reserved,
// what the build really spent, and what was given back. It is judged by
// Evaluate, the predicate the database rules are generated from, so a build
// can never be bounded by something a run is not.
//
// The lease is taken, and given back, by the CLI that submits the build, on
// the identity that submits it (a person's own credentials, or the catalog
// check job's account). A lease the CLI never closes — the process died
// mid-build — is abandoned at its deadline and reclaimed, so a reservation
// cannot be held forever; reclaiming gives all of it back, and the lease
// records the deadline it was taken under.
//
// Where the lease's money lives: taking one writes the reservation into the
// day counters a run's lease writes (spend/<day>/repos/<slug>/counted and
// spend/<day>/global/counted), settling it moves the build's real spend into
// the day's spent leaves and gives the rest back to the counters. The ledger is
// this node; the counters are the repository's and the project's.

// BuildLeasesRoot is the build lease family's root, a key space of its own.
const BuildLeasesRoot = "buildleases"

// PathBuildLeases is every lease a repository holds.
func PathBuildLeases(slug string) string { return BuildLeasesRoot + "/" + seg("slug", slug) }

// PathBuildLease is one build lease, keyed by its own id and never by a run
// id: nothing outside this family reads it (F22).
func PathBuildLease(slug, id string) string {
	return PathBuildLeases(slug) + "/" + seg("build lease id", id)
}

// The three states a lease is in. An open lease holds its reservation; a
// settled one has recorded what the build spent and given the rest back; a
// released one gave everything back, either because the build never started or
// because it was abandoned and reclaimed.
const (
	BuildLeaseOpen     = "open"
	BuildLeaseSettled  = "settled"
	BuildLeaseReleased = "released"
)

// BuildLeaseGrace is how long past the build's own timeout a lease stays open
// before it is abandoned: the submit and the status reads take time of their
// own, and reclaiming a lease under a build that is still running would let a
// second build reserve the same money.
const BuildLeaseGrace = 10 * time.Minute

// BuildLeaseDeadline is when a lease taken at now is abandoned.
func BuildLeaseDeadline(now time.Time) time.Time {
	return now.Add(BuildLeaseGrace + MaxBuildSeconds*time.Second)
}

// BuildLease is /buildleases/<slug>/<id>. Times are epoch milliseconds, as
// everywhere else in the database.
type BuildLease struct {
	State string `json:"state"`
	// Repo is the slug the lease belongs to, and ID the id it was taken under:
	// both are the node's own key (PathBuildLease), written into the value so a
	// lease read on its own says which repository and which submission it is,
	// and so a writer that was handed the value alone can name the path it
	// belongs at. A writer refuses a lease whose two names do not match the
	// path it is about to write.
	Repo string `json:"repo"`
	ID   string `json:"id,omitempty"`
	// By is the identity that took the lease, for a person reading the day's
	// counter back.
	By string `json:"by,omitempty"`
	// MachineType and Region are what was priced, and SKUs the rows the rate
	// came from: a later price change cannot rewrite what was reserved
	// (spike §4).
	MachineType string   `json:"machineType,omitempty"`
	Region      string   `json:"region,omitempty"`
	SKUs        []string `json:"skus,omitempty"`
	// PriceReadAt is when the catalog was read for this rate.
	PriceReadAt int64 `json:"priceReadAt,omitempty"`
	// Reserved is the estimate; Settled what the build really spent; Released
	// what came back. Reserved - Released is what the lease still counts.
	Reserved Micros `json:"reserved,omitempty"`
	Settled  Micros `json:"settled,omitempty"`
	Released Micros `json:"released,omitempty"`
	// StartedAt is when the lease was taken, and Exp when an open one is
	// abandoned.
	StartedAt int64 `json:"startedAt,omitempty"`
	Exp       int64 `json:"exp,omitempty"`
}

// Outstanding is what the lease still counts against the day and per-run caps:
// reserved less released.
func (l BuildLease) Outstanding() Micros { return l.Reserved - l.Released }

// Path is the lease's own node, from its two names. A lease whose names are
// empty is at no node at all, which is what a writer gets for a value that did
// not come from this package and refuses rather than guesses about.
func (l BuildLease) Path() string {
	if l.Repo == "" || l.ID == "" {
		return ""
	}
	return PathBuildLease(l.Repo, l.ID)
}

// Day is the UTC day the lease's money is counted in: the day it was taken.
// A build that crosses midnight is settled into the day it reserved from, not
// into the day it ended on, because that is the counter the reservation is in
// (the predicate lets a release name today or yesterday for the same reason).
func (l BuildLease) Day() int64 { return Day(time.UnixMilli(l.StartedAt)) }

// Unsettled is what the lease holds and has not spent: reserved less released
// less settled. A settle or a release may take at most this.
func (l BuildLease) Unsettled() Micros { return l.Reserved - l.Released - l.Settled }

// Ledger is the lease as the ledger Evaluate judges: the build's own
// reservation, spend and expiry.
func (l BuildLease) Ledger() RunLedger {
	return RunLedger{Reserved: l.Reserved, Released: l.Released, Spent: l.Settled, Exp: l.Exp, Crashed: l.Abandoned(time.Now())}
}

// Abandoned reports whether the CLI that took the lease never closed it: the
// lease is open and past its deadline. A lease with no deadline is never
// abandoned — refusing to guess is the fail-closed side, and every lease this
// package builds carries one.
func (l BuildLease) Abandoned(now time.Time) bool {
	return l.State == BuildLeaseOpen && l.Exp != 0 && now.UnixMilli() > l.Exp
}

// Settle records what the build really spent and gives the rest back. actual
// may be zero — a build that failed before Cloud Build started one — and never
// more than the lease holds.
func (l BuildLease) Settle(actual Micros) (BuildLease, error) {
	if l.State != BuildLeaseOpen {
		return BuildLease{}, fmt.Errorf("a build lease is settled once: this one is %s", l.State)
	}
	if actual < 0 || actual > l.Unsettled() {
		return BuildLease{}, fmt.Errorf("settling %s against a lease holding %s unspent", dollars(actual), dollars(l.Unsettled()))
	}
	rest := l.Unsettled() - actual
	l.Settled = actual
	l.Released += rest
	l.State = BuildLeaseSettled
	return l, nil
}

// Release gives the whole reservation back: the build never ran, or its
// submission failed before Cloud Build could bill anything.
func (l BuildLease) Release() (BuildLease, error) {
	if l.State != BuildLeaseOpen {
		return BuildLease{}, fmt.Errorf("a build lease is released once: this one is %s", l.State)
	}
	l.Released += l.Unsettled()
	l.State = BuildLeaseReleased
	return l, nil
}

// Reclaim releases a lease the CLI abandoned. Only an open lease past its
// deadline is reclaimable: a settled lease has already accounted for its
// spend, and a lease with no deadline may still be running.
func (l BuildLease) Reclaim() (BuildLease, error) {
	if l.State != BuildLeaseOpen {
		return BuildLease{}, fmt.Errorf("only an open build lease is reclaimed: this one is %s", l.State)
	}
	if l.Exp == 0 {
		return BuildLease{}, errors.New("this build lease has no deadline, so whether its build is still running is not something the database can answer")
	}
	return l.Release()
}

// BuildState is what a build lease is judged against: the day counters the
// lease's money moves through. The lease's own ledger stands in for a run's,
// so there is no run to read.
type BuildState struct {
	Repo   Counters
	Global Counters
}

// EvaluateBuild judges a build lease's reserve, settle or release with the
// same predicate the rules are generated from, so a build is bounded by the
// per-run cap, the day caps, maxReserve and the switches exactly as a job is,
// and observe mode returns the cap refusal as an advisory instead of a denial
// (D1548).
//
// w.Amount is the write's amount — the reservation when the lease is taken,
// the day's release when it is settled or given back — and w.Day is the day
// node that write touches.
func EvaluateBuild(l BuildLease, w Write, st BuildState, caps Caps, kills Kills, now time.Time) Decision {
	return Evaluate(State{Run: l.Ledger(), DayRun: l.Ledger(), Repo: st.Repo, Global: st.Global}, w, caps, kills, now)
}

// BuildRead is ReadBuildState's answer: the caps, switches and counters a
// build lease is judged against.
type BuildRead struct {
	Caps  Caps
	Kills Kills
	State BuildState
}

// ReadBuildState reads what a build lease is judged against from the same
// nodes the rules read, for the day the lease names. An unreadable node is an
// error and a missing one stays unset, which the predicate then refuses — a
// build is never allowed because a node could not be read (fail closed, the
// shape Precheck uses).
//
// It does not check which project the database belongs to: a launch already
// does that (Precheck), and this read happens on an installation the caller is
// already talking to.
func ReadBuildState(ctx context.Context, r Reader, slug string, day int64) (BuildRead, error) {
	var out BuildRead
	get := func(path string, v any) error {
		if _, err := r.Get(ctx, path, v); err != nil {
			return fmt.Errorf("reading /%s: %w", path, err)
		}
		return nil
	}
	if err := get(PathMode, &out.Caps.Mode); err != nil {
		return BuildRead{}, err
	}
	var gc GlobalCaps
	if found, err := r.Get(ctx, PathCapsGlobal, &gc); err != nil {
		return BuildRead{}, fmt.Errorf("reading /%s: %w", PathCapsGlobal, err)
	} else if found {
		out.Caps.Global = &gc
	}
	var dc DefaultCaps
	if found, err := r.Get(ctx, PathCapsDefaults, &dc); err != nil {
		return BuildRead{}, fmt.Errorf("reading /%s: %w", PathCapsDefaults, err)
	} else if found {
		out.Caps.Defaults = &dc
	}
	var rc RepoCaps
	if found, err := r.Get(ctx, PathCapsRepo(slug), &rc); err != nil {
		return BuildRead{}, fmt.Errorf("reading /%s: %w", PathCapsRepo(slug), err)
	} else if found {
		out.Caps.Repo = &rc
	}
	var lim Limits
	if found, err := r.Get(ctx, PathLimits, &lim); err != nil {
		return BuildRead{}, fmt.Errorf("reading /%s: %w", PathLimits, err)
	} else if found {
		out.Caps.Limits = &lim
	}
	var kg Kill
	if found, err := r.Get(ctx, PathKillGlobal, &kg); err != nil {
		return BuildRead{}, fmt.Errorf("reading /%s: %w", PathKillGlobal, err)
	} else if found {
		out.Kills.Global = &kg
	}
	var kr Kill
	if found, err := r.Get(ctx, PathKillRepo(slug), &kr); err != nil {
		return BuildRead{}, fmt.Errorf("reading /%s: %w", PathKillRepo(slug), err)
	} else if found {
		out.Kills.Repo = &kr
	}
	if err := get(PathSpendGlobal(day), &out.State.Global); err != nil {
		return BuildRead{}, err
	}
	if err := get(PathSpendRepo(day, slug), &out.State.Repo); err != nil {
		return BuildRead{}, err
	}
	return out, nil
}
