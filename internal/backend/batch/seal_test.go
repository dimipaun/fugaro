package batch

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
)

// TestASealedBatchLaunchIsRefused is BR1's fence under the ticket (F21 and F28
// of docs/batch-run-identity): a Batch launch carries the run's ticket and
// nothing else, and a launch that carries a sealed entry is still refused
// before anything is created. The rule is AB9's and it does not weaken -- the
// marker's value IS the seal key, and a job's variables are the job's
// definition -- but what it refuses is no longer the state of every Batch
// launch: a launcher of this release mints nothing here, so the launch that
// meets this is one of a release behind or one built by hand.
//
// The entry's VALUE is not a credential and is not shaped like one: what the
// assertions below are about is the name, which is the most an operator may be
// shown (R7).
func TestASealedBatchLaunchIsRefused(t *testing.T) {
	const value = "not-a-credential-this-test-invented"
	ctx := context.Background()

	// A launch that carries no secret entry is untouched: this helper is the
	// seal's fence and not a gate on Batch launches as such.
	b, _, _, _ := newTestBackend(t, nil)
	full := string(b.LaunchJobName(backendtest.Slug, backendtest.Workflow, "20260927-100000-abcd"))
	if got, err := b.sealedVars(full, nil); err != nil || got != nil {
		t.Fatalf("sealedVars of a launch with no secret entry = %v, %v, want no variables and no refusal", got, err)
	}

	// The refusal itself, and the sentence the two design documents carry.
	err := func() error {
		_, err := b.sealedVars(full, []backend.EnvVar{{Name: budgetTokenEnv, Value: value, Secret: true}})
		return err
	}()
	if err == nil {
		t.Fatal("a Batch launch carrying the budget token's seal marker was admitted: a job's variables are the job's definition (AB9, R5)")
	}
	msg := err.Error()
	for _, want := range []string{
		full,
		budgetTokenEnv,
		// The carrier limit: the reason this is a limit and not a missing
		// mechanism.
		"the job's definition",
		// What BR1 changed: the identity a Batch launch carries now.
		"Batch launch carries a ticket and not a seal",
		// And the fix, which is the claim's (F28, R17): an installation opts
		// in, and that selection applies the installation's update. The
		// backend names no command, because the CLI's name is the CLI's own
		// (internal/cli's selfCommand) and a backend never prints one.
		"opt this installation in",
		// The citation an operator follows.
		"docs/budget-token-channel/spec.md",
		// Nothing was created, and the sentence says so.
		"nothing was created",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: the documents and the message an operator sees say one thing (F28)\n\nmessage: %s", want, msg)
		}
	}
	if strings.Contains(msg, value) {
		t.Errorf("the refusal quotes the seal key's value: the marker's name is the most an operator may be shown (R7)\n\nmessage: %s", msg)
	}
	if strings.Contains(msg, "cannot launch any run on Batch") || strings.Contains(msg, "budget-on path") {
		t.Errorf("the refusal still says no run of a minting installation can go to Batch: since BR1 a Batch launch mints nothing and carries a ticket (R1, F28)\n\nmessage: %s", msg)
	}

	// And through the backend's own door: a launch carrying the marker creates
	// no job at all, whatever the entry is spelled as.
	for _, kv := range []string{"FUGARO_BUDGET_TOKEN=" + value, "FUGARO_BUDGET_TOKEN"} {
		be, bf, _, warns := newTestBackend(t, nil)
		s := spec()
		s.Env = []string{kv}
		if _, err := be.Launch(ctx, s); err == nil {
			t.Fatalf("Launch admitted a job carrying %q: a Batch job has no carrier for the seal key (AB9, D3021)", kv)
		}
		if got := bf.IDs(); len(got) != 0 {
			t.Errorf("a refused launch created %v: the refusal is before jobs.create", got)
		}
		for _, w := range *warns {
			if strings.Contains(w, value) {
				t.Errorf("a warning quotes the seal key's value: %s", w)
			}
		}
	}
}
