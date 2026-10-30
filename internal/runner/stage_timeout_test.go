package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The stage tripwire's enforcing tests moved to ee/governance with the code
// (plan task E2.3): an untagged build applies no governance bound, so what
// stays here is what the core owns whatever the edition — the registry's
// deadline, which is the bound the runner's stage loop actually gave the
// stage, driven below through a test-local pair rather than through a policy
// the core no longer has.

// stageTimeoutCfg is the fixture repository's file with the workflow's
// timeouts replaced and a governance: block appended. The block is decoded and
// ignored by an untagged build, which is what the core tests that use it are
// about (governance_hook_test.go).
func stageTimeoutCfg(t *testing.T, timeouts, governance string) string {
	t.Helper()
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }", timeouts, 1)
	return cfg + governance
}

// blockedStage is a stage that runs until the context it was handed ends and
// reports the cost it had reached: what ends it is a bound the run resolved and
// never the agent's own work, and the checkpoint of a halt carries the spend
// the stage had reached.
func blockedStage(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	res, err := blockUntilDone(t, ctx, req)
	res.CostUSD = 1
	return res, err
}

// TestRegistryShowsTheStagesOwnDeadline: the deadline the registry carries for
// a stage — and so the deadline the console draws — is the one the stage is
// actually under. A stage hook whose bound is tighter than the run's own
// timeouts.stage bounds the stage, and a registry naming the run's later bound
// would tell the owner the stage has time it does not have (T7.6).
//
// The pair is the test's own and not a policy: the core ships no governance,
// so the bound has to come from a hook to be exercised at all, and a fixed
// eight seconds is simpler than a config that would only be read to be
// ignored.
func TestRegistryShowsTheStagesOwnDeadline(t *testing.T) {
	cfg := strings.Replace(gwConfig(t, ""),
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 5m, stage: 30s, verify: 1m, finalize_reserve: 30s }", 1)
	b := newBK(t, cfg, "enforce", "", capScript(1)...)
	runner.ResetStageHook(t)
	if err := runner.SetStageHook(runner.StageFuncs{
		Timeout: func(v runner.StageView) time.Duration {
			if v.Stage == "implement" {
				return 8 * time.Second
			}
			return 0
		},
		TimedOut: func(runner.StageView) runner.StageDecision { return runner.StageDecision{} },
	}); err != nil {
		t.Fatal(err)
	}
	var seen map[string]any
	inStage := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		waitUntil(t, "the registry to show the implement stage", func() bool {
			seen = b.entry()
			return seen != nil && seen["stage"] == "implement"
		})
		return implement("feature")(t, ctx, req)
	}
	rec, err := b.run(t, inStage, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	started, ok1 := seen["stageStartedAt"].(json.Number)
	deadline, ok2 := seen["stageDeadline"].(json.Number)
	if !ok1 || !ok2 {
		t.Fatalf("the registry entry carries no stage deadline: %v", seen)
	}
	from, _ := started.Int64()
	to, _ := deadline.Int64()
	// The hook's own bound is 8s and the run's is 30s: anything past 8s plus
	// slack can only be the run's own.
	if d := time.Duration(to-from) * time.Millisecond; d <= 0 || d > 10*time.Second {
		t.Fatalf("the registry shows a stage deadline %v away, want the stage's own 8s bound", d)
	}
}
