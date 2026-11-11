package fake

import (
	"context"
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// head is the commit a test's fake pull request is at, full-length so the
// abbreviation tolerance of SameCommit is exercised by the cases that mean to
// exercise it and not by the ones that do not.
const (
	headOne = "1111111111111111111111111111111111111111"
	headTwo = "2222222222222222222222222222222222222222"
)

// withPR is a fake holding one open pull request at headOne.
func withPR(t *testing.T) *Provider {
	t.Helper()
	p := &Provider{}
	if _, err := p.EnsurePR(context.Background(), gitprov.PRSpec{Branch: "fugaro/x", Title: "T", Draft: true}); err != nil {
		t.Fatal(err)
	}
	p.State.PRs[0].Head = headOne
	return p
}

// TestFakeMergeRefusesOnHeadChange is the E8 red step: a Merge that ignores the
// head it was given merges a commit nobody reviewed, and the fake is where that
// shows up first. The pull request is at headOne and the call names headTwo, so
// the merge must not happen and the state must still be open.
func TestFakeMergeRefusesOnHeadChange(t *testing.T) {
	ctx := context.Background()
	p := withPR(t)
	err := p.Merge(ctx, 1, headTwo)
	if !errors.Is(err, gitprov.ErrHeadMoved) {
		t.Fatalf("Merge at a moved head = %v, want ErrHeadMoved", err)
	}
	info, err := p.PullRequest(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != gitprov.PROpen {
		t.Fatalf("the pull request is %s after a refused merge; a head-bound merge must not land", info.State)
	}
	// The merge at the head it is actually at does land, so the refusal above
	// is the head check and not a Merge that never merges anything.
	if err := p.Merge(ctx, 1, headOne[:12]); err != nil {
		t.Fatalf("Merge at the current head = %v", err)
	}
	if info, err = p.PullRequest(ctx, 1); err != nil || info.State != gitprov.PRMerged {
		t.Fatalf("PullRequest = %+v, %v; want merged", info, err)
	}
}

func TestFakeMarkReadyRefusesOnHeadChange(t *testing.T) {
	ctx := context.Background()
	p := withPR(t)
	err := p.MarkReady(ctx, 1, headTwo)
	if !errors.Is(err, gitprov.ErrHeadMoved) {
		t.Fatalf("MarkReady at a moved head = %v, want ErrHeadMoved", err)
	}
	if info, err := p.PullRequest(ctx, 1); err != nil || !info.Draft {
		t.Fatalf("PullRequest = %+v, %v; a refused MarkReady must leave the draft alone", info, err)
	}
	if err := p.MarkReady(ctx, 1, headOne); err != nil {
		t.Fatalf("MarkReady at the current head = %v", err)
	}
	if info, err := p.PullRequest(ctx, 1); err != nil || info.Draft {
		t.Fatalf("PullRequest = %+v, %v; want ready", info, err)
	}
}

// TestFakeHeadBoundCallWithoutAHeadRefuses covers the state a test can forget
// to write: no head recorded means no head the call can be checked against, and
// a mock that answered "yes, that head" would pass a gate the real provider
// would refuse.
func TestFakeHeadBoundCallWithoutAHeadRefuses(t *testing.T) {
	ctx := context.Background()
	p := &Provider{}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x", Title: "T"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"Merge", func() error { return p.Merge(ctx, 1, headOne) }},
		{"MarkReady", func() error { return p.MarkReady(ctx, 1, headOne) }},
	} {
		if err := c.call(); err == nil {
			t.Errorf("%s with no recorded head did not refuse", c.name)
		}
	}
}

// TestFakeHeadBoundCallRefusesAClosedOrMergedPR keeps the two write calls on
// the same footing as every other write: a pull request that is not open is
// never written.
func TestFakeHeadBoundCallRefusesAClosedOrMergedPR(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		call func(p *Provider) error
	}{
		{"Merge", func(p *Provider) error { return p.Merge(ctx, 1, headOne) }},
		{"MarkReady", func(p *Provider) error { return p.MarkReady(ctx, 1, headOne) }},
	} {
		p := withPR(t)
		p.State.PRs[0].State = gitprov.PRClosed
		if err := c.call(p); !errors.Is(err, gitprov.ErrPRNotOpen) {
			t.Errorf("%s on a closed pull request = %v, want ErrPRNotOpen", c.name, err)
		}
	}
}

// TestFakeChecksReadsTheState pins what the checks read answers: the runs
// recorded at that commit, a copy of them (a caller that appends does not
// change the state), and nothing at all for a commit the state does not name —
// which is not the same as a commit whose checks passed, and is why the caller
// has ChecksState rather than a boolean.
func TestFakeChecksReadsTheState(t *testing.T) {
	ctx := context.Background()
	p := withPR(t)
	p.State.Checks = map[string][]gitprov.CheckRun{
		headOne: {{Status: "completed", Conclusion: "success"}},
	}
	runs, err := p.Checks(ctx, 1, headOne)
	if err != nil || len(runs) != 1 || runs[0].Conclusion != "success" {
		t.Fatalf("Checks = %+v, %v", runs, err)
	}
	if gitprov.ChecksState(runs) != gitprov.CIGreen {
		t.Fatalf("ChecksState(%+v) = %v, want green", runs, gitprov.ChecksState(runs))
	}
	runs[0].Conclusion = "failure"
	if again, _ := p.Checks(ctx, 1, headOne); again[0].Conclusion != "success" {
		t.Fatal("Checks returned the state's own slice; a caller could change it")
	}
	if unknown, err := p.Checks(ctx, 1, headTwo); err != nil || len(unknown) != 0 {
		t.Fatalf("Checks at a commit with none = %+v, %v; want an empty set", unknown, err)
	} else if gitprov.ChecksState(unknown) != gitprov.CIUnknown {
		t.Fatal("a commit with no checks read as something other than unknown")
	}
	if _, err := p.Checks(ctx, 99, headOne); err == nil {
		t.Fatal("Checks on a pull request that does not exist did not fail")
	}
}
