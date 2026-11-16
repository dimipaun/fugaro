package fake

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// The fake's side of gitprov.Labeler: the double the orchestrator's syncer
// and the daemon's wiring are tested against (T5, T7). It carries the GitHub
// adapter's semantics without a host behind them — the writes land on the pull
// request's own labels, a name the pull request does not carry is removed as
// success, an empty set sends nothing — plus the two things a test needs from
// any double: every call recorded with its arguments, and a count of injected
// failures per method.

var _ gitprov.Labeler = (*Provider)(nil)

// LabelCall is one entry of the label call log, which holds every EnsureLabel,
// AddLabels, RemoveLabels, ListOpenPRLabels and PRLabels call — the reads
// included, which State.Calls does not hold — in the order it was made, with
// the arguments it was made with.
//
// The run id is recorded because it is not this adapter's: a write carries the
// id of the run that opened the pull request, which the credentials service
// corroborates the pull request's head against before it writes (F17, O3), so
// a test of the syncer reads it back to check the syncer sent its own.
type LabelCall struct {
	Op string `json:"op"`
	// Name, Colour and Description are EnsureLabel's arguments.
	Name        string `json:"name,omitempty"`
	Colour      string `json:"colour,omitempty"`
	Description string `json:"description,omitempty"`
	// PR, RunID and Names are AddLabels' and RemoveLabels' arguments, and PR
	// is also the number PRLabels was asked for.
	PR    int      `json:"pr,omitempty"`
	RunID string   `json:"run_id,omitempty"`
	Names []string `json:"names,omitempty"`
}

// logLabelLocked appends c to the label call log, cloning the names: the log
// outlives the call, and a caller that reuses the slice it passed must not be
// able to change what it is recorded as having asked for. Callers hold p.mu.
func (p *Provider) logLabelLocked(c LabelCall) {
	c.Names = slices.Clone(c.Names)
	p.State.LabelCalls = append(p.State.LabelCalls, c)
}

// Supported implements gitprov.Labeler: a fake host has pull request labels,
// which is what lets the syncer's tests run against it. A test that needs the
// other answer — a provider whose host has none — is Bitbucket's.
func (p *Provider) Supported() bool { return true }

// EnsureLabel implements gitprov.Labeler. The fake keeps no repository label
// catalogue, so every call succeeds and a second call for a name is the same
// success the first was, as the host's "already exists" is (D2440); what a
// test watches here is the call log, which is where the colour and the
// description the service applies (D2437) are read back.
func (p *Provider) EnsureLabel(_ context.Context, name, colour, description string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	p.logLabelLocked(LabelCall{Op: "EnsureLabel", Name: name, Colour: colour, Description: description})
	defer func() { _ = p.save() }()
	if p.FailEnsureLabel > 0 {
		p.FailEnsureLabel--
		return errors.New("fake provider: injected EnsureLabel failure")
	}
	return nil
}

// AddLabels implements gitprov.Labeler: the names are added to the pull
// request's own labels, and one it already carries — in any spelling, since
// that is how the host compares label names (F23) — is left as it is.
//
// An empty set sends nothing, as the GitHub adapter's own check does, so it
// changes neither the state nor the log: the log is what this host was asked
// to do, and an empty add asks for nothing.
func (p *Provider) AddLabels(_ context.Context, number int, runID string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	p.logLabelLocked(LabelCall{Op: "AddLabels", PR: number, RunID: runID, Names: names})
	defer func() { _ = p.save() }()
	if p.FailAddLabels > 0 {
		p.FailAddLabels--
		return errors.New("fake provider: injected AddLabels failure")
	}
	st, err := p.findLocked(number)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !hasLabel(st.Labels, name) {
			st.Labels = append(st.Labels, name)
		}
	}
	return nil
}

// RemoveLabels implements gitprov.Labeler: every label of the pull request
// that matches one of the names, compared as the host compares them (F23), is
// removed, and a name none of them matches is success — the caller writes only
// differences, and a difference it computed from a listing that has since
// moved is not a failure.
//
// The pull request's state is not checked: a merged or closed pull request
// still carries its labels, and removing them from one that closed is exactly
// what the caller removes them for (R5, R8).
func (p *Provider) RemoveLabels(_ context.Context, number int, runID string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	p.logLabelLocked(LabelCall{Op: "RemoveLabels", PR: number, RunID: runID, Names: names})
	defer func() { _ = p.save() }()
	if p.FailRemoveLabels > 0 {
		p.FailRemoveLabels--
		return errors.New("fake provider: injected RemoveLabels failure")
	}
	st, err := p.findLocked(number)
	if err != nil {
		return err
	}
	st.Labels = slices.DeleteFunc(st.Labels, func(l string) bool { return hasLabel(names, l) })
	return nil
}

// ListOpenPRLabels implements gitprov.Labeler: the open pull requests, keyed
// by number. A number absent from the map is one this host does not report as
// open — merged, closed, or never there — which is a different answer to the
// caller from an open pull request with no labels, and the difference is what
// makes it read such a number back by itself (R5).
func (p *Provider) ListOpenPRLabels(_ context.Context) (map[int][]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return nil, err
	}
	p.logLabelLocked(LabelCall{Op: "ListOpenPRLabels"})
	defer func() { _ = p.save() }()
	if p.FailListOpenPRLabels > 0 {
		p.FailListOpenPRLabels--
		return nil, errors.New("fake provider: injected ListOpenPRLabels failure")
	}
	out := make(map[int][]string, len(p.State.PRs))
	for i := range p.State.PRs {
		st := &p.State.PRs[i]
		if stateOf(st) != gitprov.PROpen {
			continue
		}
		out[st.Number] = labelList(st.Labels)
	}
	return out, nil
}

// PRLabels implements gitprov.Labeler: one pull request's state and labels,
// whatever that state is. A number the state does not hold fails, as the
// host's 404 would; a pull request that is not open is reported rather than
// refused, because clearing the status labels of one that closed is what the
// caller reads it for (R5, R8).
func (p *Provider) PRLabels(_ context.Context, number int) (gitprov.PRState, []string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return "", nil, err
	}
	p.logLabelLocked(LabelCall{Op: "PRLabels", PR: number})
	defer func() { _ = p.save() }()
	if p.FailPRLabels > 0 {
		p.FailPRLabels--
		return "", nil, errors.New("fake provider: injected PRLabels failure")
	}
	st, err := p.findLocked(number)
	if err != nil {
		return "", nil, err
	}
	return stateOf(st), labelList(st.Labels), nil
}

// labelList is a pull request's labels as a read hands them back: a fresh
// non-nil slice, so a caller that appends to it cannot reach the state and an
// open pull request with no labels reads as an empty list rather than as nil,
// which is what the listing's own contract says it is.
func labelList(labels []string) []string {
	out := make([]string, 0, len(labels))
	return append(out, labels...)
}

// hasLabel reports whether list holds name, compared the way the host compares
// label names: case-insensitively (F23).
func hasLabel(list []string, name string) bool {
	return slices.ContainsFunc(list, func(l string) bool { return strings.EqualFold(l, name) })
}
