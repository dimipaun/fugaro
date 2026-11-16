package bitbucket

import (
	"context"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// This adapter's side of gitprov.Labeler: Bitbucket Cloud pull requests have
// no labels (D2431), so there is nothing here to perform and the capability is
// implemented as a silent no-op — every method answers success and says
// nothing (R7).
//
// Silent is the point, and it is why these methods do not warn the way
// warnLabels does: that warning is about git.pr.labels, a label a person wrote
// into the repository's own fugaro.yaml and asked to be applied to a pull
// request, which is dropped once and is worth a line in the run's log. The
// Orchestrator's daemon instead calls this every tick, for every repository it
// watches, for the whole life of an installation: a warning here would be a
// line per tick about something no one can act on, so the syncer reads
// Supported and leaves this provider alone, and a caller that asks anyway gets
// a success that changed nothing.
//
// The methods also take no lock and make no request: a no-op has no state to
// protect and nothing to ask the API.

var _ gitprov.Labeler = (*Provider)(nil)

// Supported implements gitprov.Labeler: this host has no pull request labels.
func (p *Provider) Supported() bool { return false }

// EnsureLabel implements gitprov.Labeler as a no-op.
func (p *Provider) EnsureLabel(_ context.Context, _, _, _ string) error { return nil }

// AddLabels implements gitprov.Labeler as a no-op.
func (p *Provider) AddLabels(_ context.Context, _ int, _ string, _ []string) error { return nil }

// RemoveLabels implements gitprov.Labeler as a no-op.
func (p *Provider) RemoveLabels(_ context.Context, _ int, _ string, _ []string) error { return nil }

// ListOpenPRLabels implements gitprov.Labeler as a no-op: no pull request is
// reported as open, so no number reaches the map. It is not the answer to read
// anything from — Supported is false, and a caller that skipped it has been
// told the host has no labels — but it does not invent a state either, and an
// empty listing cannot be mistaken for a repository whose pull requests carry
// no labels, which would have every one of them in the map with an empty list.
func (p *Provider) ListOpenPRLabels(_ context.Context) (map[int][]string, error) { return nil, nil }

// PRLabels implements gitprov.Labeler as a no-op. Its state is the empty one,
// which is no state a pull request has: this host cannot answer, and answering
// "open" would be the one thing worse than not answering at all.
func (p *Provider) PRLabels(_ context.Context, _ int) (gitprov.PRState, []string, error) {
	return "", nil, nil
}
