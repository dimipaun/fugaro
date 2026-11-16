package bitbucket

import (
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// TestBitbucketLabelsSilentNoOp pins R7 on the host that has no labels at
// all: Bitbucket Cloud pull requests have none (D2431), so the capability is
// implemented as a no-op — Supported is false and every method answers
// success without asking the API anything — and it says nothing while doing
// it. The daemon's syncer runs this every tick, so a warning here would be a
// log line per tick per repository about something no one can act on.
//
// The fixture server holds no exchanges, so a request of any kind fails the
// test twice over: httpfixture reports it as unexpected, and the provider
// would have to return the error it got.
func TestBitbucketLabelsSilentNoOp(t *testing.T) {
	var mu sync.Mutex
	var warnings []string
	p := openInline(t, func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, msg)
	})
	var l gitprov.Labeler = p
	if l.Supported() {
		t.Fatal("Supported() = true: Bitbucket Cloud pull requests have no labels")
	}
	// Two ticks' worth of every operation: no warning per tick is the whole
	// of R7, and one per call is what its absence has to be checked against.
	for tick := 0; tick < 2; tick++ {
		if err := l.EnsureLabel(ctx, "fugaro:running", "1D76DB", "A Fugaro job is working on this pull request"); err != nil {
			t.Fatalf("EnsureLabel = %v, want a silent no-op", err)
		}
		if err := l.AddLabels(ctx, 12, "20261115-101500-abcd", []string{"fugaro:running"}); err != nil {
			t.Fatalf("AddLabels = %v, want a silent no-op", err)
		}
		if err := l.RemoveLabels(ctx, 12, "20261115-101500-abcd", []string{"fugaro:running"}); err != nil {
			t.Fatalf("RemoveLabels = %v, want a silent no-op", err)
		}
		if open, err := l.ListOpenPRLabels(ctx); err != nil || len(open) != 0 {
			t.Fatalf("ListOpenPRLabels = %v, %v; want nothing and no error", open, err)
		}
		if state, names, err := l.PRLabels(ctx, 12); err != nil || state != "" || len(names) != 0 {
			t.Fatalf("PRLabels = %q, %v, %v; want nothing and no error", state, names, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none: a provider without the capability is a silent no-op", warnings)
	}
}
