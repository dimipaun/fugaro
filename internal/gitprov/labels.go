package gitprov

import (
	"context"
	"strings"
)

// The label capability: the status and spec labels the orchestrator's daemon
// keeps on the pull requests of the runs it launched (docs/forge-labels,
// D2431, D2432).
//
// It is a separate interface rather than methods on Provider because pull
// request labels are not something every host has — Bitbucket Cloud has none
// (D2431) — and a provider without the capability must cost its caller
// nothing: a provider that does not implement Labeler, or whose Supported
// reports false, is a silent no-op, not a warning each tick (R7). Callers
// find it the way they find MintExempt, by a type assertion on the provider
// they hold.
//
// Nothing here decides who may write a label. The daemon asks the credentials
// service, which holds the token, enforces the namespaces and corroborates the
// pull request against the run id every write carries (D2439, F17); this
// interface is what the service performs its own writes through, and
// OwnedLabel is the namespace test both sides answer with.

// Labeler is a provider's label capability.
//
// A caller that has found this interface must still ask Supported before it
// relies on an answer: a provider whose host has no labels implements the
// methods below as no-ops rather than refusing (R7), so a caller that
// skipped the check would read a silent nothing as done.
type Labeler interface {
	// Supported reports whether this provider's host has pull request
	// labels at all. It is a property of the host, not of one call: it does
	// not report whether a token may write a label, which only a write can
	// answer.
	Supported() bool
	// EnsureLabel creates label name with colour and description, and treats
	// a label that is already there as success (D2440): the daemon creates
	// its labels on first use, so a second tick, a second repository or a
	// restarted daemon meets its own earlier work and must not fail on it.
	//
	// The colour and description are the service's constants (D2437) and are
	// passed through as given; a label that already exists is left as it is,
	// which is what keeps Fugaro from repainting a label a person made in one
	// of its own namespaces.
	EnsureLabel(ctx context.Context, name, colour, description string) error
	// AddLabels adds names to pull request number.
	//
	// runID is the id of the run that opened the pull request, and it is the
	// value the credentials service corroborates the pull request's head
	// against before it writes (F17, O3): the number and the run id come from
	// the daemon's own records, so a host adapter has nothing to check and
	// ignores it — the GitHub adapter does.
	AddLabels(ctx context.Context, number int, runID string, names []string) error
	// RemoveLabels removes names from pull request number. A name that is not
	// on the pull request is success: the caller writes only differences, and
	// a difference it computed from a listing that has since moved is not a
	// failure.
	//
	// runID has the meaning AddLabels gives it.
	RemoveLabels(ctx context.Context, number int, runID string, names []string) error
	// ListOpenPRLabels returns every open pull request's labels, keyed by
	// pull request number, in one paged read of the repository.
	//
	// An open pull request with no labels is present with an empty list, and
	// a number that is absent from the map is a pull request this host did
	// not report as open — merged, closed, or gone. The two are different
	// answers to the caller, which reads a number absent from the map back by
	// itself rather than treating it as clean (R5).
	ListOpenPRLabels(ctx context.Context) (map[int][]string, error)
	// PRLabels returns one pull request's state and its labels. It is the
	// read for a pull request the open listing did not report, so it answers
	// for a closed or merged one as well, and reports its state rather than
	// refusing it: whether a merged pull request keeps a label is the
	// caller's rule and not this read's.
	PRLabels(ctx context.Context, number int) (PRState, []string, error)
}

// The label namespaces Fugaro owns. A label outside both is a person's, and
// the daemon never writes it and never removes it (F23).
const (
	labelNamespaceStatus = "fugaro:"
	labelNamespaceSpec   = "spec:"
)

// OwnedLabel reports whether name is a label in a namespace Fugaro writes.
//
// The name is compared case-insensitively, because that is how the host
// compares label names (F23), and the fold is over A–Z and nothing else:
// R3's ASCII-before-lowering rule. Unicode folding maps characters that only
// look like ASCII onto it — the Kelvin sign K onto "k", the dotted capital I
// onto "i" — so lowering a name first can turn one the host never matches
// into one that reads as Fugaro's own. Folding A–Z cannot: ſpec:forge-labels
// stays ſpec:forge-labels and is not owned.
//
// Neither namespace holds a k or an i, so for these two prefixes the answer
// is the one strings.ToLower would have given as well; the fold is written
// out because the rule belongs to every comparison of a name Fugaro did not
// write, and R3's shape check for the spec name is where it bites (a name
// spelled with the Kelvin sign would pass as one beginning "k").
//
// What it answers is the namespace and nothing else. The shape of the part
// after the colon is the caller's check (R3's expression is the syncer's and
// the service's), so "spec:" alone is owned here and skipped there, and a
// non-ASCII spec name — which that check refuses anyway — is owned too.
func OwnedLabel(name string) bool {
	folded := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		folded[i] = c
	}
	lower := string(folded)
	return strings.HasPrefix(lower, labelNamespaceStatus) || strings.HasPrefix(lower, labelNamespaceSpec)
}
