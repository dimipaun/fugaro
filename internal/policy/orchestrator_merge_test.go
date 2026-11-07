package policy

import "testing"

// The orchestrator's guardrails (D1239) are merged by the rule every other
// bound of this package is: the smaller number is the stricter one, so a layer
// under the ceiling it was handed can only lower a cap. The one key that is not
// a number is the model a decision class runs on (D1234), and it tightens by
// the tier order instead: haiku < sonnet < opus, so a layer may raise a class's
// model and never lower it, and the security-sensitive class is floored at the
// top of that order.

func TestPolicyDefaultsAreConservative(t *testing.T) {
	d := Orchestrator{}.WithDefaults(20)
	for _, tc := range []struct {
		name string
		got  int64
		want int64
	}{
		{KeyOrchestratorMaxRunning, d.MaxRunning, 20},
		{KeyOrchestratorBatchSize, d.BatchSize, DefaultBatchSize},
		{KeyOrchestratorSpawnPerMinute, d.SpawnPerMinute, DefaultSpawnPerMinute},
		{KeyOrchestratorSpawnPerHour, d.SpawnPerHour, DefaultSpawnPerHour},
		{KeyOrchestratorSpawnPerDay, d.SpawnPerDay, DefaultSpawnPerDay},
	} {
		if tc.got != tc.want {
			t.Errorf("the default of %s is %d, want %d", tc.name, tc.got, tc.want)
		}
	}
	// The two figures D1239 fixes by name are pinned to their values here, so
	// a later edit to a constant is a test that fails rather than a number
	// nobody re-decided: the spec's batch of 50 launches 50 real runs before
	// anyone is asked, and the per-minute rate is what a looping cycle spends.
	if DefaultBatchSize != 10 {
		t.Errorf("the batch default is %d: D1239's conservative default is 10", DefaultBatchSize)
	}
	if DefaultSpawnPerMinute != 2 {
		t.Errorf("the per-minute spawn default is %d: D1239 asks for the conservative rate", DefaultSpawnPerMinute)
	}
	// The model side of the same block: the routine class starts at the bottom
	// of the order, which is the only place a tightening-only key can start —
	// a higher default would be a value no layer could ever loosen — and the
	// sensitive class starts at, and never leaves, the top.
	if got := d.DecisionModelPerClass[DecisionRoutine]; got != "haiku" {
		t.Errorf("the routine class defaults to %q, want haiku (the bottom of the order: a default any layer can only raise)", got)
	}
	if got := d.DecisionModelPerClass[DecisionSensitive]; got != "opus" {
		t.Errorf("the sensitive class defaults to %q, want opus (its floor, D1234)", got)
	}
	// A class the caller's map did not carry at all, and a caller that passed
	// no map: WithDefaults fills both, and never hands back the map it was
	// given to a caller who may write it.
	if got := d.DecisionModelPerClass[DecisionClass("future")]; got != "" {
		t.Errorf("WithDefaults invented a model for %q: %q", "future", got)
	}
	if (Orchestrator{}).WithDefaults(1).DecisionModelPerClass[DecisionRoutine] != "haiku" {
		t.Error("WithDefaults left the routine class unset for a caller that named no model")
	}
	// The person's own launch limit is the running cap and nothing else: the
	// other four bounds are Fugaro's figures, whatever the person allows.
	if (Orchestrator{BatchSize: 3}).WithDefaults(7).BatchSize != 3 {
		t.Error("WithDefaults overwrote a key the layer set")
	}
	if (Orchestrator{BatchSize: 3}).WithDefaults(7).MaxRunning != 7 {
		t.Error("WithDefaults did not fill the running cap from the person's max_parallel")
	}
}

func TestSpawnPerMinuteHasADefault(t *testing.T) {
	// A project and a person who write no orchestrator block at all still
	// spawn at the rate Fugaro ships: two a minute, not the five a second an
	// unset zero would be read as.
	e := Merge(Layer{Orchestrator: Orchestrator{}.WithDefaults(20)})
	if e.Orchestrator.SpawnPerMinute != DefaultSpawnPerMinute {
		t.Fatalf("the per-minute spawn cap is %d with no block written, want %d", e.Orchestrator.SpawnPerMinute, DefaultSpawnPerMinute)
	}
	if got := e.Sources[KeyOrchestratorSpawnPerMinute]; got != SourceCeiling {
		t.Errorf("the per-minute spawn cap is sourced %q, want %q (it is the ceiling's own default)", got, SourceCeiling)
	}
	// A layer that writes its own rate tightens it, and a layer that writes a
	// higher one does not raise the ceiling's.
	tight := Merge(Layer{Orchestrator: Orchestrator{}.WithDefaults(20)},
		Layer{Orchestrator: Orchestrator{SpawnPerMinute: 1}})
	if tight.Orchestrator.SpawnPerMinute != 1 {
		t.Errorf("a project that spawns once a minute got %d", tight.Orchestrator.SpawnPerMinute)
	}
	loose := Merge(Layer{Orchestrator: Orchestrator{}.WithDefaults(20)},
		Layer{Orchestrator: Orchestrator{SpawnPerMinute: 60}})
	if loose.Orchestrator.SpawnPerMinute != DefaultSpawnPerMinute {
		t.Errorf("a layer that writes 60 a minute raised the cap to %d", loose.Orchestrator.SpawnPerMinute)
	}
	if len(loose.Ignored) != 1 || loose.Ignored[0].Key != KeyOrchestratorSpawnPerMinute {
		t.Errorf("the raised rate was not recorded as ignored: %+v", loose.Ignored)
	}
}

func TestMergeNeverRaisesTheRunningCap(t *testing.T) {
	// The person's max_parallel is the cap the daemon starts from (D1239).
	ceiling := Layer{Orchestrator: Orchestrator{}.WithDefaults(20)}
	// The project's own number is a bound on it, and the largest value it may
	// write is the one that does not want to.
	project := Layer{Orchestrator: Orchestrator{MaxRunning: 4}}
	local := Layer{Orchestrator: Orchestrator{MaxRunning: 8}}
	e := Merge(ceiling, project, local)

	if e.Orchestrator.MaxRunning != 4 {
		t.Fatalf("max_running is %d: the project's 4 and a local 8 above it are written, and the smaller one is the stricter one", e.Orchestrator.MaxRunning)
	}
	if got := e.Sources[KeyOrchestratorMaxRunning]; got != SourceDefaultBranch {
		t.Errorf("max_running is sourced %q, want %q (the project's layer wrote the value that stands)", got, SourceDefaultBranch)
	}
	var ignored []Ignored
	for _, ig := range e.Ignored {
		if ig.Key == KeyOrchestratorMaxRunning {
			ignored = append(ignored, ig)
		}
	}
	if len(ignored) != 1 {
		t.Fatalf("the local layer's 8 is not recorded once in Ignored: %+v", e.Ignored)
	}
	// The record names the looser value and the one that stands, both as text,
	// and the layer the standing value came from: a reader that prints why a
	// cap is what it is has everything it needs from the entry itself.
	want := Ignored{Key: KeyOrchestratorMaxRunning, Value: "8", Effective: "4", Source: SourceDefaultBranch}
	if ignored[0] != want {
		t.Errorf("the ignored entry is %+v, want %+v", ignored[0], want)
	}
	// The ceiling's own value is the person's limit and is never a loser: a
	// layer under it can equal or lower it, and the ceiling is sourced as the
	// ceiling however many layers wrote a looser number.
	if e.Orchestrator.MaxRunning > 20 {
		t.Error("a layer raised the running cap above the person's max_parallel")
	}
	// The same test with no project layer at all: the local number is then the
	// one the ceiling's own 20 is compared against, and 8 is stricter than the
	// person's limit, so it stands — a project or a person may only lower what
	// they were given.
	if got := Merge(ceiling, local).Orchestrator.MaxRunning; got != 8 {
		t.Errorf("with no project block the local 8 gave max_running %d, want 8", got)
	}
	// Equal is not ignored: a local layer that writes the project's own number
	// agrees with it, and the record of the layer that decided it does not
	// move.
	same := Merge(ceiling, project, Layer{Orchestrator: Orchestrator{MaxRunning: 4}})
	if same.Orchestrator.MaxRunning != 4 || len(same.Ignored) != 0 {
		t.Errorf("an equal value was read as a loosening: max_running %d, ignored %+v", same.Orchestrator.MaxRunning, same.Ignored)
	}
}

func TestModelKeyOnlyTightens(t *testing.T) {
	ceiling := Layer{Orchestrator: Orchestrator{}.WithDefaults(20)}

	// A project may raise the routine class's model: the class exists so the
	// project can say that its routine decisions deserve a stronger model.
	raised := Merge(ceiling, Layer{Orchestrator: Orchestrator{
		DecisionModelPerClass: map[DecisionClass]string{DecisionRoutine: "sonnet"},
	}})
	if got := raised.Orchestrator.DecisionModelPerClass[DecisionRoutine]; got != "sonnet" {
		t.Fatalf("the routine class is %q after a layer raised it to sonnet", got)
	}
	if got := raised.Sources[DecisionModelKey(DecisionRoutine)]; got != SourceDefaultBranch {
		t.Errorf("the routine class's model is sourced %q, want %q", got, SourceDefaultBranch)
	}

	// And it may never lower the security-sensitive class, whose floor is the
	// top of the order (D1234, G6). The name is one of the vocabulary, so the
	// merge compared it and dropped it — recorded, not silently unset.
	lowered := Merge(ceiling, Layer{Orchestrator: Orchestrator{
		DecisionModelPerClass: map[DecisionClass]string{DecisionSensitive: "sonnet"},
	}})
	if got := lowered.Orchestrator.DecisionModelPerClass[DecisionSensitive]; got != "opus" {
		t.Fatalf("the sensitive class is %q after a layer wrote sonnet", got)
	}
	want := Ignored{Key: DecisionModelKey(DecisionSensitive), Value: "sonnet", Effective: "opus", Source: SourceCeiling}
	if len(lowered.Ignored) != 1 || lowered.Ignored[0] != want {
		t.Errorf("the lowered model is recorded as %+v, want one entry %+v", lowered.Ignored, want)
	}

	// A local layer over both: it keeps the raised routine model and cannot
	// lower it back, and the sensitive class is what it was.
	e := Merge(ceiling,
		Layer{Orchestrator: Orchestrator{DecisionModelPerClass: map[DecisionClass]string{DecisionRoutine: "opus"}}},
		Layer{Orchestrator: Orchestrator{DecisionModelPerClass: map[DecisionClass]string{
			DecisionRoutine:   "haiku",
			DecisionSensitive: "haiku",
		}}},
	)
	if got := e.Orchestrator.DecisionModelPerClass[DecisionRoutine]; got != "opus" {
		t.Errorf("the routine class is %q: a lower layer lowered a raised model", got)
	}
	if got := e.Orchestrator.DecisionModelPerClass[DecisionSensitive]; got != "opus" {
		t.Errorf("the sensitive class is %q", got)
	}
	// Two looser values, one per class, and the value the sensitive one was
	// dropped against is the ceiling's own.
	if len(e.Ignored) != 2 {
		t.Errorf("the two looser models are recorded as %+v", e.Ignored)
	}

	// An unknown name is not on the order at all, so it carries no value: the
	// merge treats it as unset and records nothing (the writers refuse it
	// where it is written, ValidOrchestrator).
	unknown := Merge(ceiling, Layer{Orchestrator: Orchestrator{
		DecisionModelPerClass: map[DecisionClass]string{DecisionRoutine: "sonnet-4.5", DecisionSensitive: "haiku-9000"},
	}})
	if got := unknown.Orchestrator.DecisionModelPerClass[DecisionRoutine]; got != "haiku" {
		t.Errorf("an unknown model name raised the routine class to %q", got)
	}
	if len(unknown.Ignored) != 0 {
		t.Errorf("an unknown model name was recorded as a loser rather than read as unset: %+v", unknown.Ignored)
	}

	// The merge never writes into the layers it was given: the maps a caller
	// holds are not the result's.
	layer := Layer{Orchestrator: Orchestrator{DecisionModelPerClass: map[DecisionClass]string{DecisionRoutine: "sonnet"}}}
	merged := Merge(ceiling, layer)
	merged.Orchestrator.DecisionModelPerClass[DecisionRoutine] = "opus"
	if layer.Orchestrator.DecisionModelPerClass[DecisionRoutine] != "sonnet" {
		t.Error("the merge aliased the layer's model map")
	}
}

func TestValidOrchestratorRefusesUnknownNames(t *testing.T) {
	// The block's values, as a writer must hold them: a name outside the tier
	// order, and a model under the floor of the class it is written for.
	for _, tc := range []struct {
		name  string
		block Orchestrator
		ok    bool
	}{
		{"empty", Orchestrator{}, true},
		{"a value per key", Orchestrator{MaxRunning: 4, BatchSize: 10, SpawnPerMinute: 2, SpawnPerHour: 12, SpawnPerDay: 60}, true},
		{"the routine class raised", Orchestrator{DecisionModelPerClass: map[DecisionClass]string{DecisionRoutine: "sonnet"}}, true},
		{"a negative count", Orchestrator{BatchSize: -1}, false},
		{"an unknown model", Orchestrator{DecisionModelPerClass: map[DecisionClass]string{DecisionRoutine: "gpt"}}, false},
		{"the sensitive class lowered", Orchestrator{DecisionModelPerClass: map[DecisionClass]string{DecisionSensitive: "sonnet"}}, false},
		{"an unknown class", Orchestrator{DecisionModelPerClass: map[DecisionClass]string{"routine-decisions": "haiku"}}, false},
	} {
		err := ValidOrchestrator(&tc.block)
		if tc.ok && err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}
