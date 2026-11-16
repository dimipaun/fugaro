package cli

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/settings"
)

// write_publish_test.go drives the after-write hook (N3, D2602): the stages a
// semantic command's write owes, run in process after the write landed.
//
// Four things it pins, one test each: the policy stage publishes the node and
// the local change is live (TestAfterWritePublishesThePolicyStage); a stage that
// does not leave it live keeps the change and ends the command on the one
// sentence (TestAfterWriteFailureKeepsTheLocalChange, and the sentence itself in
// TestAfterWriteSentenceIsOneHelper); a coding agent's session publishes nothing
// a plan gate may not judge, whatever the gate's marks say
// (TestAfterWriteUnderTheAgentMarkerPublishesNothing); and the stages a write
// owes come from the key's own registry row and the layer the command wrote in,
// which is what decides the layer stage for a project-layer key
// (TestAfterWriteRunsTheLayerStageForAProjectLayerKey and
// TestAfterWriteStagesFollowTheRegistryRow).
//
// Every test is hermetic: the gcpfake Realtime Database of the budget rig, the
// rig's own local config, and no cloud.

// afterWriteCmd is the command a test opens the hook by: the real sub-command
// the person's own line names, so the door the run takes and the record it would
// file are the ones a real invocation gives it. out collects everything the run
// writes — the hook routes the command's stdout onto its stderr for the length
// of the run, because the run's lines are not the command's answer — and the
// input is an empty reader: a command that has already written has nothing left
// to answer, and nothing here may wait on a prompt.
func afterWriteCmd(t *testing.T, args ...string) (*cobra.Command, *syncBuf) {
	t.Helper()
	root := NewRootCmd()
	out := &syncBuf{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetIn(strings.NewReader(""))
	cmd, _, err := root.Find(args)
	if err != nil {
		t.Fatalf("finding %v in the command tree: %v", args, err)
	}
	return cmd, out
}

// afterWriteFixture is the hook's world: the budget rig, the rules marker an
// installation that has converged carries, a version-3 node published, and a
// local config stating an orchestrator ceiling the node does not carry — which
// is a publish the stage makes and not a loosening of anything the node states.
func afterWriteFixture(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r, db := updateBudgetRig(t)
	r.appendConfig(t, "orchestrator: { max_running: 7 }\n")
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), v3Node(nil))
	return r, db
}

// TestAfterWritePublishesThePolicyStage is the hook's whole point: the command
// wrote a key the published node serves, and by the time it returns the node
// carries it — nobody is left a second command to run.
func TestAfterWritePublishesThePolicyStage(t *testing.T) {
	_, db := afterWriteFixture(t)
	cmd, out := afterWriteCmd(t, "settings", "set")
	change := afterWriteChange{Key: "orchestrator.max_running", Store: settings.StoreLocal}
	if err := publishAfterWrite(t.Context(), cmd, change); err != nil {
		t.Fatalf("the hook reported %q although the stage publishes it:\n%s", ErrorText(err), out.String())
	}
	if v := nodeVersionOn(t, db); v != projectpolicy.Version {
		t.Errorf("the published node is version %d, want the %d the stage migrates it to:\n%s", v, projectpolicy.Version, out.String())
	}
	node, ok := db.Value(projectpolicy.PathShared()).(map[string]any)
	if !ok {
		t.Fatalf("the node at %s is not an object: %v", projectpolicy.PathShared(), db.Value(projectpolicy.PathShared()))
	}
	orch, ok := node["orchestrator"].(map[string]any)
	if !ok {
		t.Fatalf("the published node carries no orchestrator block, so the write is not on it: %v", node)
	}
	// The fake hands the value back in whatever number type it decoded it into,
	// so it is spelled rather than compared as one.
	if got := fmt.Sprint(orch["max_running"]); got != "7" {
		t.Errorf("the published node's orchestrator.max_running is %q, want the 7 the local config states", got)
	}
}

// TestAfterWritePublishesTheCommandAimedInstallation is the selection: a write
// aimed at another installation with --config is that installation's write, so
// the stage runs over that installation's config and publishes that project's
// node — and the machine's own default project, which nothing wrote, is left
// exactly where it was.
func TestAfterWritePublishesTheCommandAimedInstallation(t *testing.T) {
	aimed, aimedDB := afterWriteFixture(t)
	_, machineDB := afterWriteFixture(t)
	cmd, out := afterWriteCmd(t, "settings", "set")
	if err := cmd.Flags().Set("config", aimed.cfg); err != nil {
		t.Fatal(err)
	}
	change := afterWriteChange{Key: "orchestrator.max_running", Store: settings.StoreLocal}
	if err := publishAfterWrite(t.Context(), cmd, change); err != nil {
		t.Fatalf("the aimed installation's node was not published: %q\n%s", ErrorText(err), out.String())
	}
	if v := nodeVersionOn(t, aimedDB); v != projectpolicy.Version {
		t.Errorf("the installation the command was aimed at is at version %d, want %d:\n%s", v, projectpolicy.Version, out.String())
	}
	if v := nodeVersionOn(t, machineDB); v != 3 {
		t.Errorf("the machine's own project moved to version %d although nothing was written there:\n%s", v, out.String())
	}
}

// TestAfterWriteFailureKeepsTheLocalChange is the other half of the contract:
// a stage that fails rolls nothing back. The command's own write stays on the
// machine, the node does not move, and the command ends non-zero on the one
// sentence that says so (D2614).
func TestAfterWriteFailureKeepsTheLocalChange(t *testing.T) {
	r, db := afterWriteFixture(t)
	// The node's write is refused, as a database the reader may not write
	// refuses it: the stage fails inside its own write, after the plan and the
	// confirmation.
	db.DenyNext(1)
	cmd, out := afterWriteCmd(t, "settings", "set")
	change := afterWriteChange{Key: "orchestrator.max_running", Store: settings.StoreLocal}
	err := publishAfterWrite(t.Context(), cmd, change)
	if err == nil {
		t.Fatalf("the hook reported the change live although the stage's write was refused:\n%s", out.String())
	}
	if got, want := ErrorText(err), ErrorText(afterWriteNotLive(change)); got != want {
		t.Errorf("a failed stage ends the command on %q, want the one sentence %q", got, want)
	}
	if v := nodeVersionOn(t, db); v != 3 {
		t.Errorf("the node moved to version %d although the stage's write was refused:\n%s", v, out.String())
	}
	b, rerr := os.ReadFile(r.cfg)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(b), "max_running: 7") {
		t.Errorf("the change the command wrote is gone from %s, and the hook takes nothing back:\n%s", r.cfg, string(b))
	}
}

// TestAfterWriteUnderTheAgentMarkerPublishesNothing is the trust boundary: a
// coding agent's session may not publish the two member classes no plan gate may
// judge (D1026) — a published ceiling loosened, and an account-choice member set
// — whatever the gate's marks say. Both are refused, the node does not move, and
// nothing is recorded, because a step the gate may not judge is one it records
// nothing about.
//
// The project is managed and its marks stand, which is the whole point: an
// unmanaged project refuses every layer write for a reason of its own, and a
// test that ran there would prove nothing about this fence. Both sub-tests are
// the case the hook's own gate read exists for — a session that reaches the
// stage with a verdict that would admit an ordinary publish.
func TestAfterWriteUnderTheAgentMarkerPublishesNothing(t *testing.T) {
	// The admitted publish is the control: the same world with the same gate, and
	// a change that loosens nothing, publishes. Without it the two refusals below
	// would be a project that refuses everything for reasons of its own.
	t.Run("a change that loosens nothing publishes", func(t *testing.T) {
		r, db := updateBudgetRig(t)
		managedForTheHook(t, r)
		r.appendConfig(t, "orchestrator: { max_running: 7 }\n")
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		db.Set(projectpolicy.PathShared(), v3Node(nil))
		cmd, out := afterWriteCmd(t, "settings", "set")
		change := afterWriteChange{Key: "orchestrator.max_running", Store: settings.StoreLocal}
		if err := publishAfterWrite(t.Context(), cmd, change); err != nil {
			t.Fatalf("a session the gate admitted was refused %q:\n%s", ErrorText(err), out.String())
		}
		if v := nodeVersionOn(t, db); v != projectpolicy.Version {
			t.Errorf("the admitted publish left the node at version %d:\n%s", v, out.String())
		}
	})

	t.Run("a loosened ceiling", func(t *testing.T) {
		r, db := updateBudgetRig(t)
		managedForTheHook(t, r)
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		// The node states a per-run ceiling of 1 and this machine's config states
		// 5: the publish would raise what every run of the project may spend, which
		// is D1026's money and no plan's to admit.
		db.Set(projectpolicy.PathShared(), v3Node(map[string]any{
			"budget": map[string]any{"mode": "enforce", "per_run_usd": 1},
		}))
		cmd, out := afterWriteCmd(t, "settings", "set")
		change := afterWriteChange{Key: "budget.per_run_usd", Store: settings.StoreLocal}
		agentPublishedNothing(t, r, db, cmd, out, change)
	})

	t.Run("a grant member", func(t *testing.T) {
		r, db := updateBudgetRig(t)
		managedForTheHook(t, r)
		// A member the node states nothing for and this machine's config now names:
		// the publish would hand every stage of the project a credential the node
		// did not name, which is the account-choice widening D1026 puts beside the
		// ceilings.
		r.appendConfig(t, stageAccountAccountsYAML+"default_claude_account: acme-overflow\n")
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		db.Set(projectpolicy.PathShared(), v3Node(nil))
		cmd, out := afterWriteCmd(t, "settings", "set")
		change := afterWriteChange{Key: "default_claude_account", Store: settings.StoreLocal}
		agentPublishedNothing(t, r, db, cmd, out, change)
	})
}

// managedForTheHook is the world a coding agent's session meets a managed
// project in: the two marks, the credentials isolated, and the marker set
// (update_managed_test.go's updateManagedRig, which is the same world the
// update door is tested in).
func managedForTheHook(t *testing.T, r *initRig) {
	t.Helper()
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
}

// agentPublishedNothing is what both D1026 sub-tests assert: the hook refused
// with the one sentence, the node is where it was, and no record was written —
// nothing about a step the gate may not judge is recorded, which is what makes
// a coding agent's session unable to leave the audit log claiming a widening.
func agentPublishedNothing(t *testing.T, r *initRig, db *gcpfake.RTDB, cmd *cobra.Command, out *syncBuf, change afterWriteChange) {
	t.Helper()
	err := publishAfterWrite(t.Context(), cmd, change)
	if err == nil {
		t.Fatalf("a coding agent's session published %s:\n%s", change.Key, out.String())
	}
	if got, want := ErrorText(err), ErrorText(afterWriteNotLive(change)); got != want {
		t.Errorf("the refused session ends the command on %q, want the one sentence %q", got, want)
	}
	if v := nodeVersionOn(t, db); v != 3 {
		t.Errorf("the node moved to version %d under a coding agent's session:\n%s", v, out.String())
	}
	if recs := audits(t, r); len(recs) != 0 {
		t.Errorf("a step the gate may not judge was recorded: %+v", recs)
	}
}

// TestAfterWriteSentenceIsOneHelper is D2614's sentence: one helper words it,
// every way a stage can stop short ends the command on it, and it names the
// command that finishes the job — `fugaro update`, which is the whole upgrade
// until AP3 repoints this one helper.
func TestAfterWriteSentenceIsOneHelper(t *testing.T) {
	isolateGateCredentials(t)
	change := afterWriteChange{Key: "budget.per_run_usd", Store: settings.StoreLocal}
	const want = "budget.per_run_usd is saved and is not live yet: run `fugaro update`, which publishes it"
	if got := ErrorText(afterWriteNotLive(change)); got != want {
		t.Errorf("the one sentence reads %q, want %q", got, want)
	}

	// A stage that FAILED, on a world the gate never sees.
	t.Run("a failed stage", func(t *testing.T) {
		_, db := afterWriteFixture(t)
		db.DenyNext(1)
		cmd, out := afterWriteCmd(t, "settings", "set")
		err := publishAfterWrite(t.Context(), cmd, change)
		if err == nil {
			t.Fatalf("the refused write was reported live:\n%s", out.String())
		}
		if got := ErrorText(err); got != want {
			t.Errorf("a failed stage says %q, want the one sentence %q", got, want)
		}
	})

	// And a stage a coding agent's session refused, which is a different stop in
	// a different world: one wording either way.
	t.Run("a refused session", func(t *testing.T) {
		r, db := updateBudgetRig(t)
		managedForTheHook(t, r)
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		db.Set(projectpolicy.PathShared(), v3Node(map[string]any{
			"budget": map[string]any{"mode": "enforce", "per_run_usd": 1},
		}))
		cmd, out := afterWriteCmd(t, "settings", "set")
		err := publishAfterWrite(t.Context(), cmd, change)
		if err == nil {
			t.Fatalf("the refused publish was reported live:\n%s", out.String())
		}
		if got := ErrorText(err); got != want {
			t.Errorf("a refused session says %q, want the one sentence %q", got, want)
		}
	})
}

// TestAfterWriteRunsTheLayerStageForAProjectLayerKey is the other stage of
// D2615's rule: a key the project layer file carries is one the policy stage has
// nothing to say about, so the run is the layer stage and the layer stage is
// what runs.
func TestAfterWriteRunsTheLayerStageForAProjectLayerKey(t *testing.T) {
	updateBudgetRig(t)
	cmd, out := afterWriteCmd(t, "settings", "set")
	change := afterWriteChange{Key: "layer.defaults.agent.model", Store: settings.StoreProject}
	if err := publishAfterWrite(t.Context(), cmd, change); err != nil {
		t.Fatalf("the layer stage's own answer was reported as a change not live: %q\n%s", ErrorText(err), out.String())
	}
	// The layer stage ran and said what it found: this rig's storage endpoint is
	// a fake, so the stage's own answer is the skip naming the bucket it may not
	// read — a line no other stage of the run prints.
	if got := out.String(); !strings.Contains(got, "the project layer's bucket") {
		t.Errorf("the layer stage's own line is not in the run's output:\n%s", got)
	}
}

// TestAfterWriteStagesFollowTheRegistryRow is D2615's stage choice, key by key:
// the row says what the key is and the store says where this write landed, and
// a pool is the pair that needs both — its argv writes the machine's own config
// while the published node is what serves it.
func TestAfterWriteStagesFollowTheRegistryRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		store settings.Store
		want  []string
	}{
		{"a key the machine's own config carries", "budget.mode", settings.StoreLocal, []string{initflow.Policy}},
		{"a pool, written locally and served by the node", "pools.dev.members", settings.StoreLocal, []string{initflow.Policy}},
		{"a key the command wrote into the node itself", "budget.mode", settings.StoreNode, []string{initflow.Policy}},
		{"a key only the project layer file carries", "layer.defaults.agent.model", settings.StoreProject, []string{initflow.Layer}},
		{"a project-layer row a command wrote in a checkout", "agent.model", settings.StoreRepo, []string{initflow.Layer}},
		{"a key the registry knows nothing about", "no.such.key", settings.StoreLocal, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := afterWriteStages(afterWriteChange{Key: tc.key, Store: tc.store})
			if !slices.Equal(got, tc.want) {
				t.Errorf("the stages of %s written in %s = %v, want %v", tc.key, tc.store, got, tc.want)
			}
		})
	}

	// A key that touches both layers owes both stages, and the run's own order is
	// what orders them: the hook names them and stagesNamed answers in
	// initflow's order (the policy node before the layer, its `needs`).
	t.Run("a key that touches both layers", func(t *testing.T) {
		names := afterWriteStagesFor(settings.Row{
			Key:    "both.layers",
			Layers: []string{settings.LayerProject, settings.LayerLocal},
		}, settings.StoreProject)
		if !slices.Equal(names, []string{initflow.Policy, initflow.Layer}) {
			t.Fatalf("the stages of a key that touches both layers = %v, want both", names)
		}
		e := prodEngine(initOptions{})
		ordered, unanswered := stagesNamed(e, names[1], names[0])
		if len(unanswered) != 0 {
			t.Fatalf("the door answered no stage for %v", unanswered)
		}
		if got := namesOf(ordered); !slices.Equal(got, []string{initflow.Policy, initflow.Layer}) {
			t.Errorf("the two stages came back as %v, want initflow's own order", got)
		}
	})
}
