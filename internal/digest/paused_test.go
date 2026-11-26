package digest

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestDigestCountsPausedApart is model-credentials-routing PR11's half for this
// package: a run that suspended itself to be resumed by its chain
// (contract-pause, R15) is neither a failure nor a success, so the report names
// it `paused` in its own row, counts it in no class of the failure table and
// raises no halt flag for it. The report counts no success at all, so the row's
// own word is the whole of what it says about the ending.
func TestDigestCountsPausedApart(t *testing.T) {
	const paused = string(runstore.StatusPaused)

	rec := run(t, "20261021-090000-a1b2", "2026-10-21T09:00:00Z", 1.50, 0)
	rec.Status = runstore.StatusPaused
	rec.Outcome = runstore.OutcomeDraft
	rec.BaseBranch = ""
	rec.Reason = "a credential is exhausted, resume at the reset"

	d := Build([]runstore.Record{rec}, week43(t))

	if d.Total.Runs != 1 {
		t.Fatalf("the period counts %d runs, want the paused one: it is a run of the period like any other", d.Total.Runs)
	}
	if len(d.Runs) != 1 {
		t.Fatalf("the report shows %d rows, want one: %+v", len(d.Runs), d.Runs)
	}
	if got := d.Runs[0].Status; got != paused {
		t.Errorf("the row reads %q, want %q: the report names the word its record carries rather than folding it into another", got, paused)
	}
	// The failure table is the vocabulary's own and closed over it: a paused
	// run names no class (runview.ClassOf), so it is in no row of it.
	if len(d.Failures) != 0 {
		t.Errorf("the failure table holds %+v, want nothing: a paused run did not fail", d.Failures)
	}
	// A pause is not a refusal: only a halted status or a halt of its own
	// raises the flag, and the paused run carries neither.
	for _, f := range d.Runs[0].Flags {
		if f.Name == FlagHalt {
			t.Errorf("the paused row carries the halt flag %+v, want none", f)
		}
	}
	if d.Total.Halts != 0 || d.Total.Flagged != 0 {
		t.Errorf("the period reads halts=%d flagged=%d, want 0 and 0: a run that suspended itself was not refused", d.Total.Halts, d.Total.Flagged)
	}
	// The machine-readable block is the same figures: no class, no halt.
	s := d.Summary()
	if s.Halts != 0 || len(s.Failures) != 0 {
		t.Errorf("the summary block reads halts=%d failures=%v, want 0 and none", s.Halts, s.Failures)
	}
	if s.Runs != 1 {
		t.Errorf("the summary counts %d runs, want the paused one", s.Runs)
	}
	// What a person reads: the paused row is drawn with its own word, and the
	// report nowhere calls it succeeded.
	md := string(Markdown(d))
	if !strings.Contains(md, "| paused |") {
		t.Errorf("the report does not draw the paused run with its own word:\n%s", md)
	}
	if strings.Contains(md, "| succeeded |") {
		t.Errorf("the report draws a run as succeeded, and this period holds only a paused one:\n%s", md)
	}
}
