// Package creds is the credential state of the cascade: the tiers, what state
// each credential is in, and which one a run's next call goes to.
//
// It is a pure package on purpose. It reads and writes the shared state under
// creds/ and it decides nothing else: no HTTP, no model key, no Anthropic
// request, no policy file, no runner. The service (internal/credsvc) is its one
// caller, and nothing here is ever linked into the runner or the in-runner
// gateway (Convention 11) — a run token holds no write on creds/ at all (O1),
// so a package that could write it must not be reachable from a run.
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
//     registry to sweep;
//   - creds/roundrobin — the counter D154 breaks ties with.
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
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Node paths, relative to the database root, without a leading slash and
// already key-escaped. Callers pass raw names; the builders escape them.
const (
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

	// DefaultWindow is the subscription window D142's first control measures
	// from: one exhaustion costs a shared token the rest of its window.
	//
	// The plan's `credentials` block has no window member of its own (C1.2),
	// and the spec names the value twice over: a probe's wait is "capped at the
	// subscription window (5 hours)", and a backoff is capped at the same place.
	// So the window is the policy's backoff cap, and a project that raises or
	// lowers its cap moves this with it. It is a named constant of its own here
	// rather than a silent reuse, so a later task that wants the two apart has
	// one place to separate them.
	DefaultWindow = projectpolicy.DefaultBackoffCap

	// DefaultLeaseTTL is how long a run's hold on a credential stands without
	// being refreshed, and therefore how long a slot stays taken after an
	// instance dies mid-call. Ten minutes is longer than any single model call
	// and shorter than any window a run would wait out; a working run refreshes
	// it on every call, so it bounds a *dead* instance's hold, not a live one's.
	DefaultLeaseTTL = 10 * time.Minute
)

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
	// token for; it applies only when the control is on (stops).
	//
	// The policy's block has no such member (C1.2), so there is no pointer to
	// mirror: DefaultWindow is what a zero here means, and a caller that wants
	// the window separated from the backoff cap sets it.
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
	if s.Window <= 0 {
		s.Window = DefaultWindow
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

// DefaultAttempts bounds the ETag retries of one conditional write.
const DefaultAttempts = 5

// DB is the part of the database the state needs; *rtdb.Client is one. It is
// the interface budget.SetKill and the CLI's other conditional writes already
// use, deliberately: the state is written the same way a kill switch is, by
// reading the node's ETag and writing under it, because RTDB has no
// transactions (F17) and a lost update to a shared mark is exactly the failure
// this state cannot have.
type DB interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// Store is the cascade's shared state as selection reads and writes it.
//
// It is an interface for two reasons, and both matter: the only implementation
// that talks to a database is the service's, and the selection logic has to be
// testable without one. It is not a framework — every method is one node, read
// whole or written conditionally — and the whole contract is that a reader may
// trust the values it hands back and a writer never lands a lost update.
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
	// Marks reads whole: one scan answers the cap for every candidate.
	Leases(ctx context.Context) ([]Lease, error)
	// SetLease writes one run's lease, conditionally; a lost race re-reads.
	SetLease(ctx context.Context, run Run, l Lease) error
	// Current reads the credential a run is on, if it is on one: D154's
	// stickiness. found=false is a run that has not been placed yet.
	Current(ctx context.Context, run Run) (name string, found bool, err error)
	// SetCurrent records the credential a run is on.
	SetCurrent(ctx context.Context, run Run, name string) error
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
func (s *RTDBStore) Leases(ctx context.Context) ([]Lease, error) {
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
	for sk, runs := range read {
		slug, err := budget.Unkey(sk)
		if err != nil {
			return nil, fmt.Errorf("creds: a lease node is keyed %q, which is not an escaped slug: %w", sk, err)
		}
		for rk, l := range runs {
			id, err := budget.Unkey(rk)
			if err != nil {
				return nil, fmt.Errorf("creds: the leases of %q are keyed %q, which is not an escaped run id: %w", slug, rk, err)
			}
			l.Slug, l.Run = slug, id
			out = append(out, l)
		}
	}
	return out, nil
}

// SetLease implements Store.
func (s *RTDBStore) SetLease(ctx context.Context, run Run, l Lease) error {
	// The run is the path, not the value: the node's own copy would be a second
	// place for it to be wrong.
	l.Slug, l.Run = "", ""
	_, err := cas(ctx, s.db(), PathLease(run), s.attempts(), func(Lease, bool) (Lease, error) { return l, nil })
	if err != nil {
		return fmt.Errorf("creds: writing the lease of %s/%s: %w", run.Slug, run.ID, err)
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

// SetCurrent implements Store.
func (s *RTDBStore) SetCurrent(ctx context.Context, run Run, name string) error {
	_, err := cas(ctx, s.db(), PathCurrent(run), s.attempts(), func(string, bool) (string, error) { return name, nil })
	if err != nil {
		return fmt.Errorf("creds: writing the current credential of %s/%s: %w", run.Slug, run.ID, err)
	}
	return nil
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
func cas[T any](ctx context.Context, db DB, path string, attempts int, f func(cur T, found bool) (T, error)) (T, error) {
	var zero T
	if attempts <= 0 {
		attempts = DefaultAttempts
	}
	for i := 0; i < attempts; i++ {
		var cur T
		etag, found, err := db.GetETag(ctx, path, &cur)
		if err != nil {
			return zero, err
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
