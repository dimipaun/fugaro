package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// AB1a: every run opens its budget session. The mode the run used to be turned
// off with is gone (D3009), a mode left unset is observe (D3009, R3), and what
// is left to refuse is a job that carries no database to open a session
// against — which stops the run at its start, with the one fix, instead of
// letting it spend on a ceiling nobody published.

// TestTheRunReadsUnsetAsObserve (C1, D3009, R3): a job that names no mode at
// all, and one that still carries the word that was a mode, are accounted for
// as observe — the same answers the run gives for an observe job, because both
// are asked through C1 rather than by comparing the string. The string itself
// survives exactly as the job wrote it: the gateway's own question still reads
// it, and AB1b is the task that makes that question unconditional.
func TestTheRunReadsUnsetAsObserve(t *testing.T) {
	unset := ceilingOf(t)
	observe := ceilingOf(t, runner.BudgetModeEnv, "observe")
	if !unset.On() {
		t.Fatal("a job that names no mode is not accounted for: the run opens no session and enforces nothing")
	}
	if unset.Mode != "" {
		t.Errorf("the mode a job with no mode resolved to is %q, want the empty string the job wrote: the gateway's question reads this string, and only AB1b stops asking it", unset.Mode)
	}
	if unset.Prices == nil {
		t.Error("a job that names no mode is unpriced")
	}
	if unset.Cap != observe.Cap || unset.MaxRunTokens != observe.MaxRunTokens || (unset.Prices == nil) != (observe.Prices == nil) {
		t.Errorf("unset %+v and observe %+v are not accounted for alike", unset, observe)
	}
	// The same three answers for the word that was a mode: a job of an older
	// release still carries it, and nothing may read it as a run that spends
	// outside the budget (D3009).
	removed := ceilingOf(t, runner.BudgetModeEnv, "off")
	if !removed.On() || removed.Mode != "off" {
		t.Errorf("the word that was a mode resolved to %+v, want it kept and accounted for as observe", removed)
	}

	// End to end: the mode the run is accounted for is what its budget record
	// carries, and the record's own enum holds the two live modes and nothing
	// else — a run that wrote the empty string there would write a record no
	// reader accepts.
	b := newBK(t, gwConfig(t, ""), "", "")
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Budget == nil || rec.Budget.Mode != "observe" {
		t.Fatalf("budget record = %+v, want the observe session an unset mode opens", rec.Budget)
	}
}

// TestEveryRunOpensItsSession is AB1a's red step: a run whose job names no mode
// opened no session at all — startBudgetBackend returned before it took the
// run's identity — so the run held no cap, no lease and no kill switch while
// its job's database sat there configured for it. The session is the run's own
// identity: the token object is taken once and exchanged, and the database is
// told about the run.
func TestEveryRunOpensItsSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
	}{
		// The job names no mode: what fugaro update wrote before this release,
		// and what a job without a mode entry carries (AB5 writes one).
		{"no mode", ""},
		// An older release's job, whose environment still carries the word
		// that was a mode. Its database node is the observe one, because the
		// database never had the word: what is under test is the job's own
		// environment.
		{"the word that was a mode", "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBK(t, gwConfig(t, ""), "observe", "")
			if tc.mode != "observe" {
				b.deps.Spend.Mode = tc.mode
			}
			rec, err := b.run(t, implement("feature"), review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if rec.Budget == nil || rec.Budget.Backend != "rtdb" || rec.Budget.Mode != "observe" || rec.Budget.Day != b.day() {
				t.Fatalf("budget record = %+v, want the session this run opened", rec.Budget)
			}
			// The session is the run's own identity, not a report of one: the
			// take happened twice — the project policy's read and the session's
			// own Open (projectpolicy.go) — and the database answered this run
			// as itself.
			if b.itk.Exchanges() != 2 {
				t.Fatalf("exchanges = %d, want the policy read's and the session's", b.itk.Exchanges())
			}
			if len(b.db.Credentials()) == 0 {
				t.Fatal("the run never talked to its job's database: it reported a session it did not have")
			}
			if !b.tokenObjectKept() {
				t.Fatal("the run removed the token object: its grant on tokens/ is read-only")
			}
		})
	}
}

// TestARunWithNoDatabaseIsRefusedWithTheFix (AB1a, M2): a job that carries no
// database address, and whose launcher's own evidence says the project has a
// shared budget or a published policy, cannot open the session every run now
// opens. The run stops at its start — before admission, before any git
// credential and before the clone — and the refusal names the one fix, which is
// the update that re-applies this repository's job.
func TestARunWithNoDatabaseIsRefusedWithTheFix(t *testing.T) {
	for _, tc := range []struct {
		name string
		mint func(t *testing.T, g *gw)
	}{
		{
			// The launcher's marker: the seal key it wrote into the
			// execution's environment, which only a launcher that minted a
			// token writes.
			"the launcher's marker", func(_ *testing.T, g *gw) {
				g.deps.Backend = runner.Backend{TokenMarked: true}
			},
		},
		{
			// The token object itself, in the runs bucket, for a job whose
			// environment carries no marker either.
			"the token object in the runs bucket", func(t *testing.T, g *gw) {
				gcs, b := noListBucket(t)
				gcs.Put("runs", token.ObjectKey(repoSlug, runID), []byte("sealed"))
				g.deps.Bucket = b
				g.deps.Backend = runner.Backend{}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGW(t, gwConfig(t, ""), "", "")
			tc.mint(t, g)
			rec, err := g.run(t, implement("feature"))
			if err == nil {
				t.Fatalf("a run with no database in its job must stop: rec = %+v", rec)
			}
			for _, want := range []string{runner.RTDBURLEnv, "fugaro update", "launch the run again"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if len(g.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone {
				t.Fatalf("a stage ran or an outcome was written: calls = %d, rec = %+v", len(g.agent.calls), rec)
			}
		})
	}
}
