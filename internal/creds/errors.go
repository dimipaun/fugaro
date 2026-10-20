package creds

import (
	"errors"
	"time"
)

// ExhaustedUntil is the typed answer a selection gives when no candidate can be
// used now and the run must wait for one: every candidate is exhausted, backed
// off or held, and the earliest of them becomes usable at At.
//
// It carries a time and a wait, never a credential's value: the answer is
// rendered on the wire (C1.10) and written to the audit line (C1.13), and a
// subscription token must not reach either (F32). The names this answer is
// about are the caller's own — it passed them in and knows them.
//
// The wait-or-switch rule is applied before this is returned (Select): it is
// the answer for a wait inside the project's threshold, and for any wait at all
// under `fallback: oauth-only`, where there is nothing to fail over to (D152).
// A wait beyond the threshold under `auto` is not answered with this error —
// the selection fails over to the next tier instead.
type ExhaustedUntil struct {
	// At is the earliest time one of the candidates becomes usable. With Probe
	// set it is the time the credential's wait has passed: usable then, if the
	// probe succeeds.
	At time.Time
	// Wait is At minus "now", never negative. It is returned alongside the
	// error for a caller that only wants "how long", so that the one subtraction
	// happens here and every caller answers with the same number.
	Wait time.Duration
	// Probe reports that the earliest candidate is a backed-off one whose wait
	// has passed and that becomes usable by being probed, not by waiting
	// (C1.12): a caller that probes nothing must not route real work to it.
	// It is false when the wait is real.
	Probe bool
}

func (e *ExhaustedUntil) Error() string {
	if e.Probe {
		return "no credential is usable now: the earliest is backed off with its wait passed, and is usable once a probe succeeds (it is not usable by waiting)"
	}
	return "no credential is usable now: the earliest becomes usable in " +
		e.Wait.Round(time.Second).String() + " (at " + e.At.UTC().Format(time.RFC3339) + ")"
}

// ErrNoCredential is what a selection answers when no candidate can be used and
// none becomes usable by waiting: every candidate is broken, and clearing a
// broken mark is the operator's, not a run's (C1.21). It is a refusal, never a
// fallback: the caller answers it typed and issues no credential (D143).
var ErrNoCredential = errors.New("creds: no candidate is usable and none becomes usable: every candidate is broken (clearing a broken mark is the operator's)")

// ErrFallbackForbidden is what a selection answers when no shared subscription
// credential can serve the call and the caller offered one that the
// paid-fallback policy does not admit: the project's API key, under
// `fallback: oauth-only` (the default, D152). Reaching for it would start
// spending the project's money, so the call is refused instead.
//
// It is kept apart from ErrNoCredential deliberately, because the two tell an
// operator different things: ErrNoCredential says a token was refused and needs
// replacing or clearing (C1.21), and this says the platform declined to spend
// and that only a written `fallback: auto` changes that. A subscription token
// that has died is exactly the case where the difference matters most: the run
// halts and says why, rather than quietly failing over to the paid key.
var ErrFallbackForbidden = errors.New("creds: no shared subscription credential can serve this call, and the project's paid key is not spent without a written `fallback: auto` (D152)")

// ErrNoCandidates is what a selection answers when it was offered nothing to
// select from. It is a refusal rather than an empty answer, because an empty
// candidate set is a caller's mistake (a policy read that found no account,
// an admission that dropped every one) and answering it like an exhausted
// account would hide the mistake behind a wait.
var ErrNoCandidates = errors.New("creds: no candidate credentials were offered, so there is nothing to select from")

// ErrContention is what a conditional write answers when the node kept changing
// under it: the retry bound was reached without the write landing. The caller
// fails closed — it never writes unconditionally (the state is the service's
// alone, and a blind write would erase a mark another call just made).
var ErrContention = errors.New("creds: the state kept changing under the write")
