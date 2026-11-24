package creds

import (
	"context"
	"fmt"
)

// pathObserved is the subtree one credential's last answer is recorded under,
// beside the marks and read by nothing on the call path.
const pathObserved = "creds/observed"

// PathObservation is the node one credential's last answer is recorded in.
func PathObservation(name string) string { return pathObserved + "/" + seg("credential name", name) }

// Observation is what one credential's last answer reported about rate limits:
// the NAMES of the headers of that answer that said something about a rate
// limit, and never one of their values.
//
// # Why it is not a member of Mark
//
// The marks are read by every release and decoded strictly (readNameTree,
// decodeStrict), so a member added to Mark is a member a release behind refuses
// the whole node over: an older CLI, console or doctor would fail to read every
// mark the service has written since — `fugaro review`, `doctor`, `accounts ls`
// and `accounts clear` all read this subtree, and a refused node is an error
// there, not a warning. That trade is not worth an observation no selection
// reads, so it is written to a node of its own, and the four members a
// selection needs keep their exact shape (TestModelCredentialMarksUnchanged).
//
// Nothing reads this node but the operator's own Check 880, which asks U1
// (D3178): whether an answer on a subscription token carries a utilisation
// figure at all. The names answer that question — a name is either a
// utilisation figure's or it is not — so no value of any header is needed, and
// none belongs here: a rate-limit header's value can carry an organization id,
// an account's usage, a subscription figure and, in a malformed answer, the
// credential itself, and this node is read by an operator's command and printed
// by a script.
//
// The node's own existence is the fact that an answer was recorded, which is why
// Headers is written even when it is empty: a credential whose last answer
// carried no rate-limit header reads as "recorded, none", and a credential no
// release has recorded since reads as absent. The two are told apart, and Check
// 880 reports both as UNVERIFIED rather than as anything it could conclude from.
type Observation struct {
	// Headers are the names of the rate-limit headers the last answer carried,
	// in a stable order (cascade's rateLimitHeaderNames sorts them).
	Headers []string `json:"headers"`
}

// Observed returns the observation one answer leaves: the names of the
// rate-limit headers it carried, copied so the node does not share the caller's
// slice — the caller usually built it while walking an answer's headers and
// goes on using it.
//
// An answer that named nothing is written as an empty list and not as a null:
// both say "recorded, and it named none", and a list is what a reader expects of
// a field called Headers.
func Observed(names []string) Observation {
	return Observation{Headers: append(make([]string, 0, len(names)), names...)}
}

// ObservationStore records what a credential's last answer reported.
//
// It is an interface of its own and not a method on Store, deliberately:
// CascadeState's six methods are pinned as the whole of what a selection may
// reach (TestASelectionReachesTheCascadeStateAndNotTheAuthRecord), and this is
// not a fact a selection reads, writes or may reach. A store that can do it
// says so by satisfying this; a deployment whose store cannot records nothing,
// and Check 880 reads that source as one it cannot answer for.
type ObservationStore interface {
	// UpdateObservation reads one credential's observation and writes what f
	// answers, conditionally on the value read; a lost race re-reads the node
	// and calls f again, so f must be a function of its arguments only and may
	// be called more than once. The observation written is returned.
	UpdateObservation(ctx context.Context, name string, f func(prev Observation, found bool) (Observation, error)) (Observation, error)
}

// UpdateObservation implements ObservationStore.
func (s *RTDBStore) UpdateObservation(ctx context.Context, name string, f func(prev Observation, found bool) (Observation, error)) (Observation, error) {
	o, err := cas(ctx, s.db(), PathObservation(name), s.attempts(), f)
	if err != nil {
		return Observation{}, fmt.Errorf("creds: writing the observation of %q: %w", name, err)
	}
	return o, nil
}
