package localcfg

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// The owner's orchestrator block (plan O4, D1239, D1234): the daemon's
// guardrails, written here and published into the policy node by the one keep
// rule both writers apply (OrchestratorEffective). The block's values are held
// to the node's own rule — policy.ValidOrchestrator, which projectpolicy's
// decoder applies to the node's block too — so the file an owner writes and the
// node a run reads cannot disagree about what a written block means.

// orchestratorYAML is the block in the shape the documents write it: every count
// and the two decision classes, one of them raised to a stronger model.
const orchestratorYAML = `orchestrator:
  max_running: 4
  batch_size: 3
  spawn_per_minute: 1
  spawn_per_hour: 6
  spawn_per_day: 30
  decision_model_per_class: { routine: sonnet, sensitive: opus }
`

func TestOrchestratorBlockIsReadAndHeldToTheNodesRule(t *testing.T) {
	c, err := Parse([]byte(sample + orchestratorYAML))
	if err != nil {
		t.Fatal(err)
	}
	if c.Orchestrator == nil {
		t.Fatal("Orchestrator = nil, want the block read")
	}
	o := c.Orchestrator
	if o.MaxRunning != 4 || o.BatchSize != 3 || o.SpawnPerMinute != 1 || o.SpawnPerHour != 6 || o.SpawnPerDay != 30 {
		t.Errorf("the counts are %+v, want the five the file writes", o)
	}
	if got := o.DecisionModelPerClass[policy.DecisionRoutine]; got != "sonnet" {
		t.Errorf("the routine class is %q, want sonnet", got)
	}
	if got := o.DecisionModelPerClass[policy.DecisionSensitive]; got != "opus" {
		t.Errorf("the sensitive class is %q, want opus", got)
	}
	// A config that says nothing about the block states no guardrail: the
	// publish keeps the node's (the keep rule below), so the absent key and a
	// written block that sets nothing are the one answer the field needs.
	silent, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if silent.Orchestrator != nil {
		t.Errorf("a config that writes no block = %+v, want nil", silent.Orchestrator)
	}

	for _, tc := range []struct {
		name  string
		block string
		want  string // a substring the refusal must carry
	}{
		{
			// The block decodes strictly like every other block of this file,
			// and the refusal is the yaml decoder's: it names the member and the
			// type it was looked for in, not the key path above it.
			name:  "a member the block does not have",
			block: "orchestrator: {max_running_count: 4}\n",
			want:  "field max_running_count not found in type policy.Orchestrator",
		},
		{
			name:  "a negative count",
			block: "orchestrator: {batch_size: -1}\n",
			want:  "is not a number of runs",
		},
		{
			name:  "a model the vocabulary does not hold",
			block: "orchestrator: {decision_model_per_class: {routine: gpt-5}}\n",
			want:  "is not a model this Fugaro knows",
		},
		{
			name:  "the sensitive class below its floor",
			block: "orchestrator: {decision_model_per_class: {sensitive: sonnet}}\n",
			want:  "below the floor of this class (opus)",
		},
		{
			name:  "a class the vocabulary does not hold",
			block: "orchestrator: {decision_model_per_class: {routine-decisions: haiku}}\n",
			want:  "is not a decision class",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(sample + tc.block))
			if err == nil {
				t.Fatalf("the block %q was read", tc.block)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

// TestOrchestratorEffectiveKeepsTheNodesBlock: the one keep rule both writers
// apply, stated once because the block reaches the daemon through the node: a
// machine whose config predates the key must not publish a node without the
// project's guardrails, which is the state a publish that dropped them would
// leave every cap in — Fugaro's own defaults, on a machine nobody meant to
// change anything on.
//
// The rule parts from the catalog's in one place: a written block that sets no
// key is not a statement (SetsNothing), so it keeps the node's too. A guardrail
// is a ceiling, and no file of a person's may lift or empty one.
func TestOrchestratorEffectiveKeepsTheNodesBlock(t *testing.T) {
	published := &policy.Orchestrator{MaxRunning: 4, SpawnPerMinute: 1}
	silent, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if got := silent.OrchestratorEffective(published); got != published {
		t.Errorf("a silent config takes %+v, want the published %+v", got, published)
	}
	empty, err := Parse([]byte(sample + "orchestrator: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := empty.OrchestratorEffective(published); got != published {
		t.Errorf("a written block that sets no key takes %+v, want the published %+v", got, published)
	}
	stated, err := Parse([]byte(sample + "orchestrator: {max_running: 2}\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := stated.OrchestratorEffective(published)
	if got == published || got.MaxRunning != 2 || got.SpawnPerMinute != 0 {
		t.Errorf("a config that states a block takes %+v, want its own (max_running 2 and no other key carried over)", got)
	}
	// The other direction: neither side writes one, so the publish writes no
	// member at all and the node is byte for byte what it was before the key.
	if got := silent.OrchestratorEffective(nil); got != nil {
		t.Errorf("neither side writes a block and the rule answers %+v, want nil", got)
	}
}

// TestOrchestratorNotInShared: the block is the owner's policy, and the shared
// config is the file any launcher with objectAdmin on the runs bucket can
// write — a launcher who could write the running cap or the spawn rates would
// be raising the guardrails that exist to bound it (D1239). It is dropped here
// for review_floor's reason, and internal/cli's ParseShared refuses the key
// besides.
func TestOrchestratorNotInShared(t *testing.T) {
	c, err := Parse([]byte(sample + orchestratorYAML))
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]*Config{"shared": c.Shared(), "merged": MergeShared(nil, c)} {
		if got.Orchestrator != nil {
			t.Errorf("%s: orchestrator = %+v", name, got.Orchestrator)
		}
		out, err := got.Marshal()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(out), "orchestrator") {
			t.Errorf("%s: output carries the block:\n%s", name, out)
		}
	}
	if c.Orchestrator == nil || c.Orchestrator.MaxRunning != 4 {
		t.Error("Shared/MergeShared cleared the local block")
	}
}
