// Package creds is the credential state of the cascade: the tiers, what state
// each credential is in, and which one a run's next call goes to.
//
// It is a pure package on purpose. It reads and writes the shared state under
// creds/ and it decides nothing else: no HTTP, no model key, no Anthropic
// request, no policy file, no runner. Its callers are the service
// (internal/credsvc) and the operator's CLI (internal/cli, whose `fugaro
// accounts` commands read the marks and clear a broken one, C1.21).
//
// Convention 11 is what that list is about, and it is narrower than "no run can
// reach this code": no run token, no runner and no in-runner gateway writes
// under creds/ (O1 — the database rules deny every run identity the subtree, and
// this package's writers are the service's admin account and the operator's).
// The boundary test below is a second layer over that, an allow-list of the
// packages that may import this one, and it is worth being exact about what it
// does not say: cmd/fugaro links internal/cli and the runner into ONE binary, so
// the operator commands — and this state above them — are in the binary a run's
// image carries. What a run cannot do is hold the identity those commands write
// with. See internal/creds/boundary_test.go, whose doc states this in full.
//
// # Who writes
//
// Every write here is the service's, as its own service account, which is an
// admin in the database: the rules deny every run token the whole subtree, and
// they cannot constrain this writer (F17), so the subtree's safety rests on IAM
// and on this package rather than on a .validate expression. That the rules
// deny every run token is pinned separately (C0.6, C1.3); what this package
// owes is that no *other* Fugaro identity writes here, which is why the state
// is reached through Store and not through a raw client.
//
// # What is stored
//
// Four small nodes, all under creds/:
//
//   - creds/marks/<name> — one credential's Mark (available, exhausted until T,
//     backed off, broken);
//   - creds/current/<slug>/<run> — the credential a run is on, which is D154's
//     stickiness: a run keeps its prompt cache by keeping its credential;
//   - creds/leases/<slug>/<run> — a run's expiring hold on the credential it is
//     on, which is how D142's per-token concurrency cap is counted without a
//     registry to sweep (the reader sweeps the lapsed ones it finds: Leases);
//   - creds/roundrobin — the counter D154 breaks ties with.
//
// The pair of a run's current credential and its lease is one placement, written
// atomically as one Patch (SetPlacement), so the two never disagree about where
// a run is.
//
// Keys are escaped with budget.Key, the repository's one implementation of the
// escaping RTDB paths need, and read back with budget.Unkey, so a credential
// name that carries a dot or a slash is one key and not a path of its own, and
// so that this package's escaping cannot drift from the rest of the database's.
// A name that is empty where a path is built is a programming error and panics,
// as budget's own path builders do: names come from the bundle and the policy,
// which validate them long before a path exists.
//
// # Time
//
// Nothing here reads the clock. A caller passes now to every decision
// (Selector.Now, Mark.Usable, Mark.Ready), so a test states the time it means
// and a service has one clock per call rather than one per function.
package creds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Node paths, relative to the database root, without a leading slash and
// already key-escaped. Callers pass raw names; the builders escape them.
const (
	// pathRoot is the subtree the rules deny to every run token (O1), and the
	// root one placement is patched under (SetPlacement).
	pathRoot       = "creds"
	pathMarks      = "creds/marks"
	pathLeases     = "creds/leases"
	pathCurrent    = "creds/current"
	pathRoundRobin = "creds/roundrobin"
)

// PathMark is one credential's mark node.
func PathMark(name string) string { return pathMarks + "/" + seg("credential name", name) }

// PathCurrent is the credential one run is on.
func PathCurrent(run Run) string {
	return pathCurrent + "/" + seg("slug", run.Slug) + "/" + seg("run id", run.ID)
}

// PathLease is one run's lease.
func PathLease(run Run) string {
	return pathLeases + "/" + seg("slug", run.Slug) + "/" + seg("run id", run.ID)
}

// PathRoundRobin is the tie-breaking counter.
func PathRoundRobin() string { return pathRoundRobin }

// seg escapes one name for a path and panics on an empty one, exactly as
// budget.seg does and for the same reason: an empty segment collapses the path
// onto a different node (creds//r1 is creds/r1 after URL cleaning), and an
// empty name never reaches here from the bundle or the policy.
func seg(what, v string) string {
	if v == "" {
		panic("creds: empty " + what + " in a database path")
	}
	return budget.Key(v)
}

// Fallback is the paid-fallback policy in force, spelled as the project policy
// spells it (projectpolicy.FallbackOAuthOnly / FallbackAuto, D152). The zero
// value is oauth-only: a Settings built without it is the protective one, and
// only a written `auto` reaches the paid tiers.
type Fallback string

const (
	OAuthOnly Fallback = "oauth-only"
	Auto      Fallback = "auto"
)

// Spends reports whether this fallback may move a call off the shared
// credentials and onto a paid tier.
func (f Fallback) Spends() bool { return f == Auto }

// Defaults, taken from the project policy's own so the two cannot drift. C1.11
// reads them from the policy's accessors; these are what a Settings with a
// member left at zero means.
const (
	DefaultWaitThreshold       = projectpolicy.DefaultWaitThreshold
	DefaultBackoffInitial      = projectpolicy.DefaultBackoffInitial
	DefaultBackoffCap          = projectpolicy.DefaultBackoffCap
	DefaultConcurrencyPerToken = projectpolicy.DefaultConcurrencyPerToken

	// DefaultLeaseTTL is how long a run's hold on a credential stands without
	// being refreshed, and therefore how long a slot stays taken after an
	// instance dies mid-call. A working run refreshes it on every call — every
	// selection writes the lease again — so it bounds a *dead* instance's hold
	// rather than a live one's.
	//
	// It is also the length of two other windows, and both are worth knowing.
	// One is the over-admission window: a run whose call is still streaming when
	// its lease lapses holds no slot on paper, so a selection may take one more
	// than the cap for as long as that call runs (on top of the concurrency the
	// cap's best-effort read admits at all: Lease). The other is the tail of a
	// run that has finished: nothing deletes its lease, so it keeps its slot
	// until this lapses unless the service deletes it at the run's end. Ten
	// minutes covers a call that is streaming; a service whose request timeout is
	// shorter or longer should set Settings.LeaseTTL from it (C1.11), which is
	// what the member is for.
	DefaultLeaseTTL = 10 * time.Minute

	// DefaultLeaseResidue and DefaultLeasePrune are the two numbers of the lease
	// sweep: how many lapsed nodes the reader tolerates, and how many of the
	// oldest it deletes in one read once there are more than that (Leases).
	//
	// A residue of 64 dead nodes is a few kilobytes and costs a scan nothing, so
	// the common project — one whose runs are live or recent — pays no writes at
	// all on the read path. Above it the read prunes, so the subtree the next
	// read decodes stays at (residue + the runs live within one lease TTL)
	// instead of growing with the project's history.
	DefaultLeaseResidue = 64
	DefaultLeasePrune   = 8
)

// ptrTo is the address of a copy of v, for the two settings members whose
// absent-key meaning is not their zero value. It is how Settings.forTier writes
// a control off: an explicit false or 0, which is a *written* opt-out and not a
// lost member.
func ptrTo[T any](v T) *T { return &v }

// Settings are the cascade's settings as this package enforces them: C1.2's
// project-policy `credentials` block, read through its accessors, plus the two
// this package owns (Window and LeaseTTL, which the block does not carry, with
// the defaults above).
//
// The members are named and typed as the policy's own block is, pointers
// included, so C1.11 reads the policy's accessors into this struct member for
// member and a reader can see the mapping. Every member left at zero therefore
// means what the policy's absent key means — which is the *protective*
// configuration and never the permissive one: oauth-only, the default threshold
// and backoffs, one run per token, and (the two this package owns) a five-hour
// window and a ten-minute lease. That is deliberate: the two places a settings
// value can be lost — a caller that forgets a member, a policy read that came
// back empty — both have to fail toward doing less.
//
// The two pointers are the reason this is a struct of pointers rather than of
// values. For both of D142's controls the zero value is the wrong answer: false
// is the opt-out of a control whose absent key means on, and 0 is the written
// "no cap" where an absent key means one run at a time. So neither can be a
// plain bool or int, and a caller that loses the member loses it toward the
// protective reading rather than the spending one.
type Settings struct {
	// Fallback is the paid-fallback policy; the zero value is oauth-only.
	Fallback Fallback
	// WaitThreshold is how long a run waits for an exhausted credential's reset
	// before failing over to the next tier. Under oauth-only it is ignored
	// (D152): there is nothing to fail over to.
	WaitThreshold time.Duration
	// Window is the subscription window D142's first control holds an exhausted
	// token for; it applies only when the control is on (stops) and only to the
	// shared tier (forTier).
	//
	// The policy's block has no such member (C1.2), so there is no pointer to
	// mirror and no written value to read: a member left unset *is* the policy's
	// backoff cap (withDefaults), which is the value the spec names twice over —
	// a probe's wait is "capped at the subscription window (5 hours)" and a
	// backoff is capped in the same place. A project that raises or lowers its
	// cap therefore moves this with it, and a caller that wants the window
	// separated from the cap sets this member.
	Window time.Duration
	// StopAfterExhaustionPerWindow is D142's first control, projectpolicy's
	// member under its own name: nil is the absent key, which means *true*
	// (stops).
	StopAfterExhaustionPerWindow *bool
	// ConcurrencyPerToken is D142's second control: at most this many runs may
	// hold one shared credential at a time, projectpolicy's member under its own
	// name. nil is the absent key — one run at a time — and is deliberately not
	// the written 0, which is no cap at all (concurrency).
	ConcurrencyPerToken *int
	// BackoffInitial and BackoffCap are the wait a credential with no reset time
	// is held for, and the longest that wait may double to. They are the
	// policy's two Backoff members, already parsed: that block carries duration
	// strings, and parsing one is the policy's job, not a selection's.
	BackoffInitial time.Duration
	BackoffCap     time.Duration
	// LeaseTTL is how long a run's hold stands without being refreshed.
	LeaseTTL time.Duration
}

// stops is D142's first control as it is in force: true unless the caller wrote
// false, word for word what projectpolicy.Credentials.StopsAfterExhaustion
// answers for the policy. A Settings whose member was never filled in stops a
// token after one exhaustion per window, which is the protective direction.
func (s Settings) stops() bool {
	return s.StopAfterExhaustionPerWindow == nil || *s.StopAfterExhaustionPerWindow
}

// concurrency is D142's second control as it is in force: the written cap, or
// one run at a time when the member was never filled in, word for word what
// projectpolicy.Credentials.ConcurrencyCap answers for the policy. A negative
// value is handed on rather than clamped: the policy refuses one and Free reads
// it as no room at all, so a hand-built Settings fails closed.
func (s Settings) concurrency() int {
	if s.ConcurrencyPerToken == nil {
		return DefaultConcurrencyPerToken
	}
	return *s.ConcurrencyPerToken
}

// withDefaults fills every unset member with its default. It is applied at each
// use rather than to the caller's Settings, so a caller's struct is never
// rewritten and a zero Settings is always the safe one.
func (s Settings) withDefaults() Settings {
	if s.WaitThreshold <= 0 {
		s.WaitThreshold = DefaultWaitThreshold
	}
	if s.BackoffInitial <= 0 {
		s.BackoffInitial = DefaultBackoffInitial
	}
	if s.BackoffCap <= 0 {
		s.BackoffCap = DefaultBackoffCap
	}
	// The policy refuses a cap below the initial wait (validCredentials). A
	// hand-built Settings can still hold one, and a cap below the initial would
	// be a wait that never doubles; the initial wins, which is the slower of
	// the two and the one the first exhaustion has already been promised.
	if s.BackoffCap < s.BackoffInitial {
		s.BackoffCap = s.BackoffInitial
	}
	// The window is the backoff cap unless it was set: see the member's doc. It
	// is filled in HERE, after the cap is known, rather than written as a
	// constant of its own — a project that writes a two-hour cap gets a
	// two-hour window, which is what the member promises.
	if s.Window <= 0 {
		s.Window = s.BackoffCap
	}
	if s.LeaseTTL <= 0 {
		s.LeaseTTL = DefaultLeaseTTL
	}
	// The two controls are deliberately not defaulted here: stops() and
	// concurrency() are their accessors, so a nil pointer is filled in where it
	// is read and a *written* false or 0 is never overwritten. A negative
	// concurrency is left as it is too: the policy refuses one, and "fewer than
	// zero runs may hold this" is a state in which nothing is selectable, which
	// fails closed. Reading it as "no cap" would be the one mistake this member
	// exists to prevent.
	return s
}

// forTier is the settings as they apply to one tier's credentials, and the
// answer for tier B is a change of two members rather than a check somewhere
// below.
//
// D142's two controls are owner headroom over the SHARED credentials: the window
// is the subscription window an exhaustion costs a donated token (D142's first
// control, F20's "stop after one exhaustion per window"), and the cap is "the
// number of runs that may hold one *shared* credential at a time"
// (projectpolicy.Credentials.ConcurrencyCap). Neither has a subject on tier B,
// which is the project's own paid API key: a rate limit that named a one-minute
// reset must not hold that key for a five-hour subscription window, and "one run
// at a time" on a key the project pays for is a self-inflicted outage rather
// than headroom. Tier B's spend is bounded where it is metered, by the budget
// caps the gateway enforces on this project's own calls — not by a control
// written for someone else's donated quota.
//
// So on tier B both controls are off — a written false and a written 0, which
// are exactly the opt-outs the policy spells — and the marks and leases of tier
// B behave the way every credential did before D142: a usage limit holds it
// until its reset, a cap of 0 is no cap.
func (s Settings) forTier(t Tier) Settings {
	if t == TierA {
		return s
	}
	s.StopAfterExhaustionPerWindow = ptrTo(false)
	s.ConcurrencyPerToken = ptrTo(0)
	return s
}

// DefaultAttempts bounds the ETag retries of one conditional write.
const DefaultAttempts = 5

// DB is the part of the database the state needs; *rtdb.Client is one, checked
// by the compiler rather than left to the first service that wires the two
// together (state_test.go's `var _ DB = (*rtdb.Client)(nil)`, until C1.10
// constructs an RTDBStore over a real client). It is
// the interface budget.SetKill and the CLI's other conditional writes already
// use, deliberately: the state is written the same way a kill switch is, by
// reading the node's ETag and writing under it, because RTDB has no
// transactions (F17) and a lost update to a shared mark is exactly the failure
// this state cannot have.
type DB interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
	// Patch writes several locations under root in one atomic request (nil
	// deletes), which is how a run's placement — its current credential and its
	// lease — is written as one thing rather than two writes that can be torn
	// apart by a failure between them (SetPlacement).
	Patch(ctx context.Context, root string, updates map[string]any) error
}

// Store is the cascade's shared state as selection reads and writes it.
//
// It is an interface for two reasons, and both matter: the only implementation
// that talks to a database is the service's, and the selection logic has to be
// testable without one. It is not a framework — every method is one node, read
// whole or written whole — and the whole contract is that a reader may trust the
// values it hands back, a mark is never written without the compare-and-set that
// keeps another writer's change (UpdateMark), and no write leaves the state
// holding two facts that disagree (SetPlacement).
type Store interface {
	// Marks reads every credential's mark in one call: the subtree is one short
	// node per credential and a selection needs the marks of all its candidates
	// at once, so reading them one at a time would be one round trip per
	// candidate on every call. A credential with no mark is absent from the map.
	Marks(ctx context.Context) (map[string]Mark, error)
	// UpdateMark reads one credential's mark and writes what f answers,
	// conditionally on the value read; a lost race re-reads the node and calls f
	// again, so f must be a function of its arguments only and may be called
	// more than once. The mark written is returned.
	UpdateMark(ctx context.Context, name string, f func(prev Mark, found bool) (Mark, error)) (Mark, error)
	// Leases reads every lease — live and lapsed — in one call, for the reason
	// Marks reads whole: one scan answers the cap for every candidate. now is
	// the clock the reader uses to tell a lapsed hold from a live one while it
	// sweeps (an implementation may sweep; see RTDBStore.Leases for what that
	// means and why the read is bounded by it).
	Leases(ctx context.Context, now time.Time) ([]Lease, error)
	// SetPlacement writes one run's placement — the credential it is on and the
	// lease it holds — as one write, atomically: the two are one fact about the
	// run, and a failure between two separate writes would leave them disagreeing
	// about it (SetPlacement, RTDBStore). It is not conditional, and needs no
	// condition: the placement of a run is the last selection's, and two
	// instances placing the same run at once are answering the same call.
	SetPlacement(ctx context.Context, run Run, name string, l Lease) error
	// Current reads the credential a run is on, if it is on one: D154's
	// stickiness. found=false is a run that has not been placed yet.
	Current(ctx context.Context, run Run) (name string, found bool, err error)
	// RoundRobin returns the tie-breaking counter's current value and advances
	// it by one, conditionally on the value it read, so that two instances
	// breaking a tie at once get two different values rather than one value
	// twice.
	RoundRobin(ctx context.Context) (int64, error)
}

// RTDBStore is the Store the service uses: the shared state read and written as
// the service's own account.
type RTDBStore struct {
	// DB is the database; *rtdb.Client is one.
	DB DB
	// Attempts bounds the ETag retries of one conditional write. 0 is
	// DefaultAttempts.
	Attempts int
}

// Marks implements Store. A node that is not an object, or an object carrying a
// member this release does not model, fails the decode and is an error rather
// than an empty set: a selection that cannot read the marks must fail closed,
// never read "no marks" as "everything is available".
//
// The read goes through a raw message and decodeStrict rather than straight
// into the map (as the previous shape did), and the difference is not
// cosmetic: encoding/json ignores a key it does not know, so a mark node a
// newer release wrote differently — {"status":"broken"} where this release
// reads {"state":...} — would decode to the ZERO Mark, which is an available
// and usable credential. That is the exact fail-open this method's contract
// forbids, and DisallowUnknownFields is what refuses it. projectpolicy decodes
// its own documents the same way, for the same reason.
//
// A key that is not the canonical spelling of the name it unescapes is refused
// for the same reason and by the same argument. Key writes uppercase escapes
// (%02X) and Unkey reads either case, so "a%2Fb" and "a%2fb" both unescape to
// a/b — and two members of one JSON object assigning one map key is decided by
// Go's map iteration order, so which mark a selection saw would flap between
// runs. Nothing in this package writes a lowercase escape, so such a node is
// one this release did not write, and reading it as a name is the fail-open
// direction. Requiring the key to be Key(name) again is what refuses it, and it
// refuses the collision with it: Key is injective onto its own output, so two
// distinct names cannot produce one key.
func (s *RTDBStore) Marks(ctx context.Context) (map[string]Mark, error) {
	raw := json.RawMessage{}
	if _, _, err := s.db().GetETag(ctx, pathMarks, &raw); err != nil {
		return nil, fmt.Errorf("creds: reading the marks: %w", err)
	}
	if len(raw) == 0 {
		return map[string]Mark{}, nil
	}
	read, err := decodeStrict[map[string]Mark](raw)
	if err != nil {
		return nil, fmt.Errorf("creds: reading the marks: %w", err)
	}
	out := make(map[string]Mark, len(read))
	for k, m := range read {
		name, err := budget.Unkey(k)
		if err != nil {
			return nil, fmt.Errorf("creds: a mark node is keyed %q, which is not an escaped name: %w", k, err)
		}
		if name == "" {
			// An empty key unescapes to an empty name, and an empty name is not a
			// credential: this package's paths panic on one (seg), so a node keyed
			// "" must stop the read rather than travel onward as a name.
			return nil, fmt.Errorf("creds: a mark node is keyed %q, which unescapes to no name at all", k)
		}
		if budget.Key(name) != k {
			// A key that unescapes to a name but is not that name's key: "a%2fb"
			// for a/b, which Key spells "a%2Fb". It is refused rather than read
			// because a subtree carrying both spellings — a hand-edit, or a
			// second writer that escapes differently — otherwise has its two
			// marks assigned to one map key in whatever order the range over
			// `read` happens to take, and a broken mark reads as an available
			// one on a random subset of selections. The canonical-key test
			// closes the lone odd key and the collision together, because Key is
			// injective.
			return nil, fmt.Errorf("creds: a mark node is keyed %q, which is not how this release escapes %q (want %q)", k, name, budget.Key(name))
		}
		out[name] = m
	}
	return out, nil
}

// UpdateMark implements Store.
func (s *RTDBStore) UpdateMark(ctx context.Context, name string, f func(prev Mark, found bool) (Mark, error)) (Mark, error) {
	m, err := cas(ctx, s.db(), PathMark(name), s.attempts(), f)
	if err != nil {
		return Mark{}, fmt.Errorf("creds: writing the mark of %q: %w", name, err)
	}
	return m, nil
}

// Leases implements Store. The lease nodes are keyed by slug and run id, and
// the reader fills both back into the value, so a caller gets the whole lease
// and not just its half.
//
// # The read sweeps, because a read that cannot finish is a project that cannot run
//
// This subtree grows with the runs a project has ever had: one node per run,
// written at every selection, and (until a later task deletes them) nothing
// removes one when the run ends. An unbounded subtree is not merely slow here.
// The database caps one response at 16 MiB (rtdb/client.go), so a project whose
// history reached that size would be answered with a truncated document, this
// decode would fail, and EVERY selection in the project would fail closed — the
// whole project stopped, by its own history, with no attacker anywhere in it.
// The read therefore cannot be left unbounded, and the bound has to be applied
// by the reader, because a lapsed lease is nobody's work to clean up: the run it
// belonged to is gone and no other caller knows it exists.
//
// So a read prunes the dead nodes it finds: once more than DefaultLeaseResidue
// of them have lapsed, it deletes the oldest DefaultLeasePrune of them. The next
// read then decodes (residue + the runs live within one lease TTL) nodes rather
// than every run in the project's history. The residue is deliberate — 64 dead
// nodes are a few kilobytes and cost a scan nothing, and pruning on *every* read
// would turn the hot path into a write path for no benefit.
//
// The sweep is best effort in both directions, and both are load-bearing:
//
//   - It re-reads each candidate and writes under the ETag it finds there, so a
//     run that refreshed its own lease between this read and the sweep keeps its
//     node: PutIfMatch refuses a stale ETag rather than deleting a live hold.
//     Deleting a hold that had just been refreshed would put a run back under a
//     cap it had already taken a slot against.
//   - A failure in it — a lost race, a transient write error, a node that
//     disappeared — is not returned. The leases this read answered with are
//     still the leases as they were, a lapsed one holds nothing either way, and
//     failing a selection because a sweep of another run's dead node did not
//     land would let the sweep decide the call.
func (s *RTDBStore) Leases(ctx context.Context, now time.Time) ([]Lease, error) {
	raw := json.RawMessage{}
	if _, _, err := s.db().GetETag(ctx, pathLeases, &raw); err != nil {
		return nil, fmt.Errorf("creds: reading the leases: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	read, err := decodeStrict[map[string]map[string]Lease](raw)
	if err != nil {
		return nil, fmt.Errorf("creds: reading the leases: %w", err)
	}
	out := make([]Lease, 0, len(read))
	var lapsed []Lease
	for sk, runs := range read {
		slug, err := budget.Unkey(sk)
		if err != nil {
			return nil, fmt.Errorf("creds: a lease node is keyed %q, which is not an escaped slug: %w", sk, err)
		}
		if slug == "" {
			return nil, fmt.Errorf("creds: a lease node is keyed %q, which unescapes to no slug at all", sk)
		}
		for rk, l := range runs {
			id, err := budget.Unkey(rk)
			if err != nil {
				return nil, fmt.Errorf("creds: the leases of %q are keyed %q, which is not an escaped run id: %w", slug, rk, err)
			}
			if id == "" {
				// An empty run id is not a run: the sweep deletes through the path
				// builders, which panic on an empty segment, and a panic in the
				// read path would be a hand-written node stopping the service
				// rather than failing one read closed.
				return nil, fmt.Errorf("creds: the leases of %q are keyed %q, which unescapes to no run id at all", slug, rk)
			}
			l.Slug, l.Run = slug, id
			out = append(out, l)
			if !l.Live(now) {
				lapsed = append(lapsed, l)
			}
		}
	}
	s.sweepLeases(ctx, now, lapsed)
	return out, nil
}

// sweepLeases is the delete half of Leases: the oldest
// (DefaultLeasePrune-bounded) part of what has lapsed past DefaultLeaseResidue.
// Every error is dropped; the reason is in Leases, where the decision to sweep
// is made and where a reader can see what it costs.
func (s *RTDBStore) sweepLeases(ctx context.Context, now time.Time, lapsed []Lease) {
	if len(lapsed) <= DefaultLeaseResidue {
		return
	}
	// Oldest first, with the path breaking ties, so a run of sweeps across
	// instances converges on the same nodes rather than each picking its own.
	sort.Slice(lapsed, func(i, j int) bool {
		if !lapsed[i].Until.Equal(lapsed[j].Until) {
			return lapsed[i].Until.Before(lapsed[j].Until)
		}
		if lapsed[i].Slug != lapsed[j].Slug {
			return lapsed[i].Slug < lapsed[j].Slug
		}
		return lapsed[i].Run < lapsed[j].Run
	})
	n := len(lapsed) - DefaultLeaseResidue
	if n > DefaultLeasePrune {
		n = DefaultLeasePrune
	}
	for _, l := range lapsed[:n] {
		path := PathLease(Run{Slug: l.Slug, ID: l.Run})
		var cur Lease
		etag, found, err := s.db().GetETag(ctx, path, &cur)
		if err != nil || !found || cur.Until.After(now) {
			// Gone, or refreshed since the read: not dead after all, and a hold
			// that has been renewed is not this sweep's to delete.
			continue
		}
		_ = s.db().PutIfMatch(ctx, path, etag, nil)
	}
}

// SetPlacement implements Store: a run's current credential and its lease are
// one placement, written as one atomic Patch.
//
// Writing them as two conditional writes — which is what this used to do — was
// the shape the review called compare-and-set in name only, and it was right
// about what the condition bought: neither write's value is computed from the
// value it read, so the ETag it retried under guarded nothing, and the only
// thing that mattered about the pair was that both landed. Two writes can be
// torn apart by a crash or a refused request between them, and then the run's
// current credential and the credential it holds a slot on disagree — the cap
// counting a hold on a credential the run is not on, until its next selection
// happens to put them back in step. One Patch cannot tear: either the run is
// placed or it is not.
func (s *RTDBStore) SetPlacement(ctx context.Context, run Run, name string, l Lease) error {
	// The run is the path, not the value: the node's own copy would be a second
	// place for it to be wrong.
	l.Slug, l.Run = "", ""
	updates := map[string]any{
		strings.TrimPrefix(PathLease(run), pathRoot+"/"):   l,
		strings.TrimPrefix(PathCurrent(run), pathRoot+"/"): name,
	}
	if err := s.db().Patch(ctx, pathRoot, updates); err != nil {
		return fmt.Errorf("creds: placing %s/%s on %q: %w", run.Slug, run.ID, name, err)
	}
	return nil
}

// Current implements Store.
func (s *RTDBStore) Current(ctx context.Context, run Run) (string, bool, error) {
	var name string
	_, found, err := s.db().GetETag(ctx, PathCurrent(run), &name)
	if err != nil {
		return "", false, fmt.Errorf("creds: reading the current credential of %s/%s: %w", run.Slug, run.ID, err)
	}
	return name, found, nil
}

// RoundRobin implements Store.
func (s *RTDBStore) RoundRobin(ctx context.Context) (int64, error) {
	var used int64
	_, err := cas(ctx, s.db(), pathRoundRobin, s.attempts(), func(cur int64, _ bool) (int64, error) {
		used = cur // the value this writer consumed, not the one it leaves
		return cur + 1, nil
	})
	if err != nil {
		return 0, fmt.Errorf("creds: advancing the round-robin counter: %w", err)
	}
	return used, nil
}

// decodeStrict decodes a node this package owns, refusing any member it does
// not model. It exists because the client's own GetETag is a plain
// json.Unmarshal, which silently drops a key it does not know — and a dropped
// key is a zero field, which for a Mark means "available". A node written by a
// newer release, or by hand, must stop the read instead (projectpolicy decodes
// its documents with DisallowUnknownFields for the same reason).
func decodeStrict[T any](raw json.RawMessage) (T, error) {
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("the node carries something this release does not model: %w", err)
	}
	return v, nil
}

func (s *RTDBStore) db() DB { return s.DB }

func (s *RTDBStore) attempts() int {
	if s.Attempts <= 0 {
		return DefaultAttempts
	}
	return s.Attempts
}

// cas reads the node at path, hands f what it decoded (the zero value when the
// node is absent), and writes what f answers under the ETag it read. A lost
// race — the database answering ErrPrecondition, which is another writer having
// moved the node between the read and the write — re-reads and calls f again,
// so a lost update is impossible rather than unlikely: the value that finally
// lands was computed from the value that was actually there.
//
// f is called again after a conflict, so it must answer only from its
// arguments. After attempts conflicts the write is refused (ErrContention) and
// nothing is written: there is no unconditional fallback, because an
// unconditional write is exactly the lost update this loop exists to prevent.
//
// The node is read as a raw message and decoded with decodeStrict rather than
// straight into T by the client, which is a plain json.Unmarshal. The difference
// is the one that matters most on this path: a node carrying a member this
// release does not model would decode to a T whose unknown fields are zero, and
// f — which is fed that value and answers the next one — would then write a
// state derived from a state it did not understand. A mark node written
// differently by a newer release is the case: {"status":"broken"} decodes to the
// zero Mark, which is an available and usable credential, so the exhaustion
// recorded on top of it would take a refused token off. decodeStrict refuses the
// node instead, and the write does not happen.
func cas[T any](ctx context.Context, db DB, path string, attempts int, f func(cur T, found bool) (T, error)) (T, error) {
	var zero T
	if attempts <= 0 {
		attempts = DefaultAttempts
	}
	for i := 0; i < attempts; i++ {
		var raw json.RawMessage
		etag, found, err := db.GetETag(ctx, path, &raw)
		if err != nil {
			return zero, err
		}
		var cur T
		if len(raw) > 0 {
			cur, err = decodeStrict[T](raw)
			if err != nil {
				return zero, fmt.Errorf("creds: reading %s: %w", path, err)
			}
		}
		next, err := f(cur, found)
		if err != nil {
			return zero, err
		}
		err = db.PutIfMatch(ctx, path, etag, next)
		if errors.Is(err, rtdb.ErrPrecondition) {
			continue
		}
		if err != nil {
			return zero, err
		}
		return next, nil
	}
	return zero, ErrContention
}
