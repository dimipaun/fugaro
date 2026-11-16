package fake

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// labelRun is the id of the run a label write carries: the value the
// credentials service corroborates a pull request's head against before it
// writes (F17, O3). The fake records it, because it is not the fake's — a
// test of the syncer reads it back to check the syncer sent its own (T5).
const labelRun = "20261115-101500-abcd"

// labelCallsOf returns the logged calls of op; an op the log does not hold
// fails the test, so a test that expects a call cannot pass on a fake that
// never recorded one.
func labelCallsOf(t *testing.T, p *Provider, op string) []LabelCall {
	t.Helper()
	var out []LabelCall
	for _, c := range p.Snapshot().LabelCalls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no %s call was logged; the log holds %+v", op, p.Snapshot().LabelCalls)
	}
	return out
}

// TestFakeLabeler pins the fake's side of gitprov.Labeler, which the
// orchestrator's syncer and the daemon's wiring are tested against: it
// supports labels, every call is recorded with its arguments — the run id
// included — in the order it was made, the writes land on the pull request's
// own labels, and a test can make any one method fail.
func TestFakeLabeler(t *testing.T) {
	ctx := context.Background()
	p := &Provider{}
	var l gitprov.Labeler = p
	if !l.Supported() {
		t.Fatal("Supported() = false: the fake stands in for a host that has labels, and the syncer is tested against it")
	}
	// #1 is open and #2 is merged: the open listing must hold the one and not
	// the other, and a read by number must answer for both (R5).
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/one", Title: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/two", Title: "two"}); err != nil {
		t.Fatal(err)
	}
	p.State.PRs[1].State = gitprov.PRMerged

	// EnsureLabel records the name, the colour and the description, because
	// those are the service's constants (D2437) and the fake is where a test
	// checks the syncer asked for the right ones.
	for i := 0; i < 2; i++ {
		if err := l.EnsureLabel(ctx, "fugaro:running", "1D76DB", "A Fugaro job is working on this pull request"); err != nil {
			t.Fatalf("EnsureLabel = %v", err)
		}
	}
	ensures := labelCallsOf(t, p, "EnsureLabel")
	if len(ensures) != 2 {
		t.Fatalf("EnsureLabel calls = %d, want 2: every call is logged", len(ensures))
	}
	if c := ensures[0]; c.Name != "fugaro:running" || c.Colour != "1D76DB" ||
		c.Description != "A Fugaro job is working on this pull request" {
		t.Fatalf("EnsureLabel logged as %+v", c)
	}

	if err := l.AddLabels(ctx, 1, labelRun, []string{"fugaro:running", "spec:forge-labels"}); err != nil {
		t.Fatalf("AddLabels = %v", err)
	}
	if got := p.State.PRs[0].Labels; len(got) != 2 || got[0] != "fugaro:running" || got[1] != "spec:forge-labels" {
		t.Fatalf("labels after AddLabels = %v", got)
	}
	adds := labelCallsOf(t, p, "AddLabels")
	if c := adds[0]; c.PR != 1 || c.RunID != labelRun || len(c.Names) != 2 {
		t.Fatalf("AddLabels logged as %+v, want number 1, run %s and both names", c, labelRun)
	}

	// A name the pull request already carries, in another spelling, is not
	// added twice: the host matches label names case-insensitively and keeps
	// the spelling it holds (F23).
	if err := l.AddLabels(ctx, 1, labelRun, []string{"FUGARO:RUNNING"}); err != nil {
		t.Fatalf("AddLabels of a name the pull request carries = %v", err)
	}
	if got := p.State.PRs[0].Labels; len(got) != 2 {
		t.Fatalf("labels after a case-insensitive re-add = %v, want the two already there", got)
	}

	// An empty set sends nothing, as the GitHub adapter's own check does, so
	// neither the state nor the log changes.
	before := len(p.Snapshot().LabelCalls)
	if err := l.AddLabels(ctx, 1, labelRun, nil); err != nil {
		t.Fatalf("AddLabels with no names = %v", err)
	}
	if err := l.RemoveLabels(ctx, 1, labelRun, []string{}); err != nil {
		t.Fatalf("RemoveLabels with no names = %v", err)
	}
	if after := len(p.Snapshot().LabelCalls); after != before {
		t.Fatalf("an empty set logged %d calls, want none: it sends nothing", after-before)
	}
	if got := p.State.PRs[0].Labels; len(got) != 2 {
		t.Fatalf("labels after an empty set = %v, want the two already there", got)
	}

	// A label that is not on the pull request is removed as success, for the
	// reason the GitHub adapter gives: the caller writes only differences.
	if err := l.RemoveLabels(ctx, 2, labelRun, []string{"fugaro:running"}); err != nil {
		t.Fatalf("RemoveLabels of an absent label = %v", err)
	}
	removes := labelCallsOf(t, p, "RemoveLabels")
	if c := removes[0]; c.PR != 2 || c.RunID != labelRun || len(c.Names) != 1 {
		t.Fatalf("RemoveLabels logged as %+v, want number 2, run %s and the one name", c, labelRun)
	}

	// The removal matches the name case-insensitively, as the host does.
	if err := l.RemoveLabels(ctx, 1, labelRun, []string{"FUGARO:RUNNING"}); err != nil {
		t.Fatalf("RemoveLabels of another spelling = %v", err)
	}
	if got := p.State.PRs[0].Labels; len(got) != 1 || got[0] != "spec:forge-labels" {
		t.Fatalf("labels after RemoveLabels = %v, want only the spec label", got)
	}

	// One read of the repository, keyed by number: the open pull request is
	// there with the labels it carries, and the merged one is not.
	open, err := l.ListOpenPRLabels(ctx)
	if err != nil {
		t.Fatalf("ListOpenPRLabels = %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("the open listing = %v, want only pull request 1", open)
	}
	if got := open[1]; len(got) != 1 || got[0] != "spec:forge-labels" {
		t.Fatalf("open[1] = %v, want the spec label", got)
	}
	// An open pull request with no labels reads as an empty list, not as a
	// number the map does not hold (the listing's own contract).
	p.State.PRs = append(p.State.PRs, PRState{PR: gitprov.PR{Number: 3, URL: "https://example.invalid/pr/3"}})
	open, err = l.ListOpenPRLabels(ctx)
	if err != nil {
		t.Fatalf("ListOpenPRLabels = %v", err)
	}
	if got, ok := open[3]; !ok || got == nil || len(got) != 0 {
		t.Fatalf("an open pull request with no labels read as %v (present %t), want an empty list", got, ok)
	}

	// A number the listing did not report is read back by itself, state and
	// labels both, and a merged pull request is reported rather than refused
	// (R5, R8).
	state, names, err := l.PRLabels(ctx, 2)
	if err != nil || state != gitprov.PRMerged {
		t.Fatalf("PRLabels(2) = %s, %v, %v; want merged", state, names, err)
	}
	if state, names, err = l.PRLabels(ctx, 1); err != nil || state != gitprov.PROpen || len(names) != 1 {
		t.Fatalf("PRLabels(1) = %s, %v, %v; want open with its one label", state, names, err)
	}
	if _, _, err := l.PRLabels(ctx, 99); err == nil {
		t.Fatal("PRLabels of a pull request the fake does not hold = nil, want an error")
	}
	// Snapshot clones the log, names included, so a test reading it while the
	// syncer runs cannot change the provider through the copy.
	snap := p.Snapshot()
	cloned := false
	for i := range snap.LabelCalls {
		if len(snap.LabelCalls[i].Names) > 0 {
			snap.LabelCalls[i].Names[0] = "bug"
			cloned = true
			break
		}
	}
	if !cloned {
		t.Fatal("the snapshot holds no label call with names")
	}
	for _, c := range p.Snapshot().LabelCalls {
		if slices.Contains(c.Names, "bug") {
			t.Fatalf("a change through the snapshot reached the provider: %+v", c)
		}
	}

	// Each method can be made to fail, and a failed call changes nothing (the
	// labels the pull request starts with are where the two writes are
	// checked) and is logged anyway, as every other injected failure in this
	// package is.
	for _, c := range []struct {
		method string
		labels []string // what the pull request carries before the call
		inject func(*Provider)
		call   func(gitprov.Labeler) error
	}{
		{"EnsureLabel", []string{"spec:forge-labels"},
			func(p *Provider) { p.FailEnsureLabel = 1 },
			func(l gitprov.Labeler) error {
				return l.EnsureLabel(ctx, "fugaro:failed", "D73A4A", "The last Fugaro job on this pull request failed")
			}},
		{"AddLabels", []string{"spec:forge-labels"},
			func(p *Provider) { p.FailAddLabels = 1 },
			func(l gitprov.Labeler) error { return l.AddLabels(ctx, 1, labelRun, []string{"fugaro:failed"}) }},
		{"RemoveLabels", []string{"fugaro:failed", "spec:forge-labels"},
			func(p *Provider) { p.FailRemoveLabels = 1 },
			func(l gitprov.Labeler) error { return l.RemoveLabels(ctx, 1, labelRun, []string{"fugaro:failed"}) }},
		{"ListOpenPRLabels", []string{"spec:forge-labels"},
			func(p *Provider) { p.FailListOpenPRLabels = 1 },
			func(l gitprov.Labeler) error { _, err := l.ListOpenPRLabels(ctx); return err }},
		{"PRLabels", []string{"spec:forge-labels"},
			func(p *Provider) { p.FailPRLabels = 1 },
			func(l gitprov.Labeler) error { _, _, err := l.PRLabels(ctx, 1); return err }},
	} {
		t.Run("injected failure: "+c.method, func(t *testing.T) {
			p := &Provider{}
			spec := gitprov.PRSpec{Branch: "fugaro/one", Title: "one", Labels: c.labels}
			if _, err := p.EnsurePR(ctx, spec); err != nil {
				t.Fatal(err)
			}
			c.inject(p)
			err := c.call(p)
			if err == nil || !strings.Contains(err.Error(), "injected "+c.method) {
				t.Fatalf("%s = %v, want the injected failure", c.method, err)
			}
			if got := p.State.PRs[0].Labels; !slices.Equal(got, c.labels) {
				t.Fatalf("labels after a failed %s = %v, want %v", c.method, got, c.labels)
			}
			if n := len(labelCallsOf(t, p, c.method)); n != 1 {
				t.Fatalf("%s logged %d calls, want 1: a failed call is still a call", c.method, n)
			}
			if err := c.call(p); err != nil {
				t.Fatalf("%s after the injected failure = %v, want it to succeed", c.method, err)
			}
		})
	}
}
