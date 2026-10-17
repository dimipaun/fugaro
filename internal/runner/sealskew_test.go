package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestAOneBitMarkerIsALauncherThatPredatesTheSeal is the runner's half of the
// mixed-fleet rule (D19): a marker of "1" is written only by a launcher from
// before the seal, which wrote the token unsealed under runs/. This runner
// reads only a sealed object under tokens/, so it refuses at once, with an
// error that says which side is old and what to do — never a fallback to a
// plaintext object, under tokens/ or anywhere — and before any git credential
// is asked for or any stage runs. A plaintext token planted at the run's own
// tokens/ key does not change that.
//
// Mutation (run, restored): drop the marker-without-key refusal from
// takeBudgetToken — the run then reads tokens/ with no key, and refuses
// with the generic sealed-object error instead: the "predates the seal" and
// "fugaro upgrade" checks fail.
func TestAOneBitMarkerIsALauncherThatPredatesTheSeal(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "enforce", "50", "", capScript(3)...)
	b.deps.Backend.TokenMarked, b.deps.Backend.TokenKey = true, nil
	ctx := context.Background()
	if err := token.DeleteObject(ctx, b.bucket, repoSlug, runID); err != nil {
		t.Fatal(err)
	}
	// A plaintext token where a sealed one would be: nothing Fugaro writes,
	// and nothing this run may read.
	plain := mintPlain(t, b, repoSlug, runID)
	if _, err := b.bucket.Create(ctx, token.ObjectKey(repoSlug, runID), []byte(plain), "text/plain"); err != nil {
		t.Fatal(err)
	}
	rec, err := b.run(t, implement("feature"))
	if err == nil || rec == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("a run marked by a pre-seal launcher ran: rec = %+v, err = %v", rec, err)
	}
	for _, want := range []string{"predates the seal", "fugaro upgrade", "start a new run (fugaro run with the same task)"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("the refusal does not say %q: %q", want, rec.Reason)
		}
	}
	// The run has launched (launch.json exists), so --retry reports it as
	// already launched and starts nothing: it is not the cure.
	if strings.Contains(rec.Reason, "--retry") {
		t.Errorf("the refusal names --retry, which never relaunches a launched run: %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, plain) || b.itk.Exchanges() != 0 {
		t.Fatal("the plaintext token was read")
	}
	if *b.auths != 0 || len(b.agent.calls) != 0 {
		t.Fatalf("the run went on: %d git credentials, %d stages", *b.auths, len(b.agent.calls))
	}
}
