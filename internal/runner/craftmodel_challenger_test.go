package runner_test

import (
	"strings"
	"testing"
)

// armCraftCfg is the arm fixture's configuration with the repository's own
// crafts naming a coder model.
func armCraftCfg(t *testing.T) string {
	t.Helper()
	cfg := gwConfig(t, "  crafts: { coder: { model: "+haiku+" } }\n")
	out := strings.Replace(cfg, "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)
	if out == cfg {
		t.Fatal("fixture has no project line")
	}
	return out
}

// armCraftLayer is the owner's layer of an arm run whose crafts name the coder
// model: armLayer plus the layer's own crafts, which is the layer a craft's
// model may be set in (config.Scopes: a model is the project's, an access is
// not).
func armCraftLayer() string {
	return strings.Replace(armLayer, "ab:\n", "defaults:\n  agent:\n    crafts:\n      coder: { model: "+haiku+" }\nab:\n", 1)
}

// TestAChallengerArmRefusesACraftCoderModel: a craft that names the coder model
// and a task that is a challenger arm say two different things about the one
// model the arm exists to name, and the run refuses the arm rather than let the
// craft's model land on it — applyChallenger's own rule for a model grade, and
// for the same reason (D555, D556, D559).
//
// Letting the craft win would not settle it either: the arm's champion coder is
// read off the configuration before the crafts resolve (championCoder), so the
// champion's own run — the one this arm is compared with — would run the craft's
// model while the arm's plan and its record were derived from, and name, another
// one. Nothing here can choose between the two, so the arm is refused by name.
//
// The crafts are read where they are resolved, so both layers that can carry a
// coder model are refused: the repository's own file, and the owner's layer,
// which a check at the arm's own admission could not see.
func TestAChallengerArmRefusesACraftCoderModel(t *testing.T) {
	for _, tt := range []struct {
		name  string
		cfg   string
		layer string
	}{
		{"the repository's crafts", armCraftCfg(t), armLayer},
		{"the project layer's crafts", armCfg(t), armCraftLayer()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, _ := armGWSpec(t, tt.cfg, "50", armTask(challengerModel), tt.layer)
			pushes := countPushes(t, g.harness)
			rec, err := g.run(t, implement("feature"), review("ship", 0))
			if err == nil {
				t.Fatal("a challenger arm whose coder craft names a model was run: its calls would go out on the craft's model while its record names the challenger")
			}
			refusedArm(t, rec)
			for _, want := range []string{"is a challenger arm", "coder craft", haiku, "ab: block"} {
				if !strings.Contains(rec.Reason, want) {
					t.Fatalf("the reason does not name %q: %q", want, rec.Reason)
				}
			}
			if len(g.agent.calls) != 0 || pushes() != 0 || len(g.provider.State.PRs) != 0 {
				t.Fatalf("a refused arm ran a stage or published: calls %d, pushes %d, PRs %d",
					len(g.agent.calls), pushes(), len(g.provider.State.PRs))
			}
		})
	}
}
