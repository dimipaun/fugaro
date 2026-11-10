package projectpolicy

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// The published orchestrator block (plan O4, D1239, D1234): the daemon's
// guardrails, held to the same rules the local config's own block is
// (policy.ValidOrchestrator), and additive to the node — a member set in a
// release newer than the reader is a key the reader lists in UnknownKeys and
// decodes past, which is why this task moves no policy version.

func TestOrchestratorBlockDecodesStrictly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
		want  string // a substring the refusal must carry, "" for a block that is read
	}{
		{name: "every key", block: `{"max_running":4,"batch_size":10,"spawn_per_minute":2,"spawn_per_hour":12,"spawn_per_day":60,"decision_model_per_class":{"routine":"sonnet","sensitive":"opus"}}`},
		{name: "one key", block: `{"batch_size":3}`},
		{name: "an empty block", block: `{}`},
		{name: "null", block: `null`},
		{name: "a class the vocabulary does not hold", block: `{"decision_model_per_class":{"routine-decisions":"haiku"}}`, want: "is not a decision class"},
		{name: "a class written twice", block: `{"decision_model_per_class":{"routine":"haiku","routine":"sonnet"}}`, want: "names \"routine\" twice"},
		{name: "a model the vocabulary does not hold", block: `{"decision_model_per_class":{"routine":"gpt-5"}}`, want: "is not a model this Fugaro knows"},
		{name: "the sensitive class below its floor", block: `{"decision_model_per_class":{"sensitive":"sonnet"}}`, want: "is below the floor of this class (opus)"},
		{name: "a class with no model", block: `{"decision_model_per_class":{"routine":""}}`, want: "no model is named"},
		{name: "a negative count", block: `{"batch_size":-1}`, want: "is not a number of runs"},
		{name: "a member the block does not have", block: `{"max_running_count":4}`, want: "unknown field \"max_running_count\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(`{"version":4,"orchestrator":` + tc.block + `}`))
			if tc.want != "" {
				if err == nil {
					t.Fatalf("the block %s was read: %+v", tc.block, p.Orchestrator)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("the refusal %q does not name %q", err, tc.want)
				}
				return
			}
			if err != nil || !found || p == nil {
				t.Fatalf("the block %s = (%v, %v, %v), want a policy", tc.block, p, found, err)
			}
			if len(p.UnknownKeys) != 0 {
				t.Errorf("UnknownKeys = %v, want none: orchestrator is a key this Fugaro reads", p.UnknownKeys)
			}
		})
	}
}

// TestOrchestratorBlockNormalizesAnEmptyOne: an absent member and a written
// empty one are the same policy (SetsNothing), so both decode to nil — the
// answer that keeps a project which sets no guardrail writing no member at all,
// the rule an empty image_catalog is normalized by.
func TestOrchestratorBlockNormalizesAnEmptyOne(t *testing.T) {
	for _, raw := range []string{
		`{"version":4}`,
		`{"version":4,"orchestrator":{}}`,
		`{"version":4,"orchestrator":null}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err != nil || !found || p == nil {
			t.Fatalf("%s = (%v, %v, %v), want a policy", raw, p, found, err)
		}
		if p.Orchestrator != nil {
			t.Errorf("%s decodes Orchestrator = %+v, want nil: nothing is set", raw, p.Orchestrator)
		}
	}

	// A block that does set one key is kept exactly as written, and the keys it
	// leaves out stay zero for the merge's defaults to fill.
	p, _, err := Decode([]byte(`{"version":4,"orchestrator":{"batch_size":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Orchestrator == nil || p.Orchestrator.BatchSize != 3 {
		t.Fatalf("Orchestrator = %+v, want batch_size 3", p.Orchestrator)
	}
	if p.Orchestrator.MaxRunning != 0 || p.Orchestrator.SpawnPerMinute != 0 || p.Orchestrator.DecisionModelPerClass != nil {
		t.Errorf("a key the block leaves out was filled by the decoder: %+v", p.Orchestrator)
	}
}

// TestOrchestratorMemberNamesAreSpelledAsTheBlockSpellsThem: a member written
// in another case is read as the member it folds to, so which value this
// Fugaro reads would depend on the order the keys were written in — the rule
// every block of this node is held to.
func TestOrchestratorMemberNamesAreSpelledAsTheBlockSpellsThem(t *testing.T) {
	_, _, err := Decode([]byte(`{"version":4,"orchestrator":{"Max_Running":4,"max_running":8}}`))
	if err == nil {
		t.Fatal("a block naming Max_Running and max_running was read")
	}
	if !strings.Contains(err.Error(), `spells max_running as "Max_Running"`) {
		t.Errorf("the refusal %q does not name the folded member", err)
	}
}

// TestOrchestratorIsUnknownToAReaderThatPredatesIt: the block is additive and
// optional, so no policy Version moves for it (plan O4's red-step question
// against version.go). The same node is decoded twice — once under this
// Fugaro's keys and once under a copy of the list as it stood before the key —
// and the reader that predates it lists the key and goes on, which is the node
// a CLI of that age refuses and a runner of it logs (P8).
//
// Version moves when a *runner* must enforce something it does not know
// (version.go). Nothing here is enforced by a runner: the block's reader is the
// orchestrator daemon, which ships in the same release as the key. The keys
// added to this node since version 4 (verifier_accounts, review_floor,
// image_catalog) moved no version either, for the same reason.
func TestOrchestratorIsUnknownToAReaderThatPredatesIt(t *testing.T) {
	if Version != 4 {
		t.Fatalf("the policy Version is %d, and this test's node is written for 4: update both together, deliberately", Version)
	}
	raw := []byte(`{"version":4,"budget":{"mode":"observe"},"orchestrator":{"max_running":4,"decision_model_per_class":{"routine":"sonnet"}}}`)
	p, found, err := Decode(raw)
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: orchestrator is a key this Fugaro reads", p.UnknownKeys)
	}
	if p.Orchestrator == nil || p.Orchestrator.MaxRunning != 4 || p.Orchestrator.DecisionModelPerClass[policy.DecisionRoutine] != "sonnet" {
		t.Errorf("Orchestrator = %+v, want the block read", p.Orchestrator)
	}

	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, "orchestrator")
	q, found, err := decodeWithKeys(raw, preKey)
	if err != nil || !found {
		t.Fatalf("the reader that predates the key refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{"orchestrator"}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("the reader that predates the key lists UnknownKeys = %v, want %v: that is the node a CLI of that age refuses and a runner of it logs", q.UnknownKeys, want)
	}
	if q.Budget.Mode != "observe" {
		t.Errorf("Budget.Mode = %q, want observe: the rest of the policy stays in force beside a key the reader does not know", q.Budget.Mode)
	}
}
