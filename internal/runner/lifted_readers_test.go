package runner_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// lifted_readers_test.go is I1's half of lift L8 on the runner: every reader of
// a repository's file there drops every lifted key. ResolveConfig — the one
// resolution a run's bootstrap, its ref arm and every reader around it goes
// through (S1) — is called directly on a file that writes every key C1 lists,
// and readBaseConfig, the default branch's read for a follow-up, is driven
// through a real first run and follow-up whose base writes them too: the two
// readers the plan's acceptance names, over the same walk internal/cli's
// TestEveryReaderDropsLiftedKeys makes (docs/repo-override-rulings/implementation-plan.md, I1).

// liftedSentinel is the model every lifted model key in liftedKeys names. The
// pins layer the harness installs names the catalog's models instead, so a run
// that calls a stage as this one read a lifted key, or resolves a
// configuration carrying it.
const liftedSentinel = "repo-9"

// liftedAgentKeys is the lifted keys C1 lists under agent: — the model a role,
// a craft and a review level runs as — in the indentation the fixture's own
// agent block uses.
const liftedAgentKeys = `  model: repo-9
  models: { background: repo-9, coder: repo-9, diagnose: repo-9, reviewer: repo-9, vision: repo-9 }
  crafts:
    coder: { model: repo-9 }
    merge-judge: { model: repo-9 }
    review: { model: repo-9 }
    security-review: { model: repo-9 }
    senior-review: { model: repo-9 }
  levels:
    l1: { model: repo-9 }
    l2: { model: repo-9 }
    l3: { model: repo-9 }
`

// liftedTopKeys is the lifted keys C1 lists at the file's own top level: ab.rate,
// the escalation grades' models, and the rigor default and its areas. They are
// appended at the end, where a top-level block belongs.
const liftedTopKeys = `ab: { rate: 0 }
model_grades:
  strong: { model: repo-9 }
  strongest: { model: repo-9 }
rigor:
  default: hotfix
  areas:
    - { name: repo-area, match: 'src/**', level: pro }
`

// liftedFixture writes every lifted key into a repository's file: cfg is the
// fixture's own file. The agent keys go inside the agent block it already has —
// a second agent: block is a file the strict decode refuses — and the rest after
// it, so the fixture's own lines stay where they were.
func liftedFixture(t *testing.T, cfg string) string {
	t.Helper()
	out := strings.Replace(cfg, "  auth: api-key\n", "  auth: api-key\n"+liftedAgentKeys, 1)
	if out == cfg {
		t.Fatal("the fixture no longer writes the agent.auth line the lifted keys are inserted after")
	}
	return out + liftedTopKeys
}

// TestEveryReaderDropsLiftedKeys reads a repository's file that writes every
// lifted key through the runner's two readers and fails if any of their values
// reaches the configuration they answer.
func TestEveryReaderDropsLiftedKeys(t *testing.T) {
	t.Run("ResolveConfig (the bootstrap and the ref arm)", func(t *testing.T) {
		base := testutil.FixtureFiles(t)["fugaro.yaml"]
		anchored, pins := testutil.FixturePins(base, config.LiftedKeyPaths())
		if pins == "" {
			t.Fatal("the fixture's own file named no lifted key for a pins layer to carry")
		}
		layer := &task.ProjectLayer{YAML: pins}

		lifted := liftedFixture(t, anchored)
		want := config.LiftedKeyNotes([]byte(lifted))
		if len(want) != len(config.LiftedKeys) {
			t.Fatalf("the file writes %d lifted keys, want all %d", len(want), len(config.LiftedKeys))
		}

		rc := runner.ResolveConfig([]byte(lifted), layer, "aurora", "main")
		if rc.LayerErr != nil || len(rc.Problems) > 0 || !rc.Applied {
			t.Fatal(rc.LayerErr, rc.Problems, rc.Applied)
		}
		assertNoLiftedValue(t, "ResolveConfig", rc.Cfg)

		// The resolution names every key it dropped, in the file's own paths.
		var got, wantPaths []string
		for _, n := range rc.Res.Lifted {
			got = append(got, n.Path)
		}
		for _, n := range want {
			wantPaths = append(wantPaths, n.Path)
		}
		if strings.Join(got, ",") != strings.Join(wantPaths, ",") {
			t.Errorf("the resolution named %v as dropped, want the file's lifted keys (%v)", got, wantPaths)
		}

		// And the file that writes them resolves to the configuration of the
		// same file without them: the drop leaves nothing of the keys behind.
		// The rigor block is the one thing it keeps, so the plain arm writes it
		// empty too (the block's presence is the repository opting in).
		plain := runner.ResolveConfig([]byte(anchored+"rigor: {}\n"), layer, "aurora", "main")
		if len(plain.Problems) > 0 || !plain.Applied {
			t.Fatal(plain.Problems, plain.Applied)
		}
		if rc.Cfg.SHA256() != plain.Cfg.SHA256() {
			t.Errorf("a file that writes every lifted key resolves to %s, the same file without them to %s",
				rc.Cfg.SHA256(), plain.Cfg.SHA256())
		}
	})

	t.Run("a first run and a follow-up over a default branch that writes them", func(t *testing.T) {
		// The default branch is where a follow-up reads the file (readBaseConfig)
		// and the ref a first run checks out: both are made to write every lifted
		// key. The task's project layer, the harness's pins, keeps naming the
		// models a priced run needs — where an installation's arrive from.
		prep := func(h *harness) {
			advanceBranch(t, h.remote, "main", map[string]string{"fugaro.yaml": liftedFixture(t, h.files["fugaro.yaml"])})
		}
		h := followUpHarness(t, "", prep)
		assertNoLiftedCall(t, "the first run", h.agent.calls)

		h.followUp(t, "20261127-100001-bcde", runID, "Please also cover the empty case.")
		first := len(h.agent.calls)
		rec, err := h.run(t, withAnswer(implement("cover"), "Covered the empty case."), review("ship", 0))
		mustReady(t, rec, err)
		if rec.FollowUp == nil {
			t.Fatalf("the run is not a follow-up: %+v", rec)
		}
		assertNoLiftedCall(t, "the follow-up", h.agent.calls[first:])
	})
}

// assertNoLiftedValue fails when the resolved configuration still carries a
// lifted key's value. The whole struct is marshalled and searched rather than
// its fields read one by one, so a slot this test did not think of is caught
// too.
func assertNoLiftedValue(t *testing.T, who string, cfg *config.Config) {
	t.Helper()
	if cfg == nil {
		t.Fatalf("%s resolved no configuration", who)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(liftedSentinel)) {
		t.Errorf("%s resolved a configuration carrying %q, a lifted key's value: %s", who, liftedSentinel, out)
	}
}

// assertNoLiftedCall fails when any stage of a run was asked to run as the model
// a lifted key named, or was handed the repository's rigor block: either is a
// reader that resolved the file outside config.Resolve.
func assertNoLiftedCall(t *testing.T, who string, calls []agent.Request) {
	t.Helper()
	if len(calls) == 0 {
		t.Fatalf("%s made no agent call at all", who)
	}
	for i, c := range calls {
		if c.Model == liftedSentinel {
			t.Errorf("%s ran stage %d as %q, a lifted key's value", who, i+1, c.Model)
		}
		if bytes.Contains([]byte(c.Prompt), []byte("repo-area")) {
			t.Errorf("%s handed stage %d the repository's own rigor area, a lifted key's value", who, i+1)
		}
	}
}
