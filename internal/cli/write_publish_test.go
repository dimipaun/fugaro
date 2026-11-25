package cli

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/settings"
)

// write_publish_test.go drives the after-write hook (N3, D2602): the stages a
// semantic command's write owes, run in process after the write landed.
//
// Seven things it pins: the variable C1's seam is held in points at this file's
// hook and not at the stub's no-op (TestAfterWriteIsWiredToTheHook); the policy
// stage publishes the node and the local change is live
// (TestAfterWritePublishesThePolicyStage), and it is the installation the
// command was aimed at (TestAfterWritePublishesTheCommandAimedInstallation); a
// stage that does not leave it live keeps the change and ends the command on the
// one sentence (TestAfterWriteFailureKeepsTheLocalChange, and the sentence itself
// in TestAfterWriteSentenceIsOneHelper); a coding agent's session publishes
// nothing a plan gate may not judge, whatever the gate's marks say
// (TestAfterWriteUnderTheAgentMarkerPublishesNothing), while the Claude Code IDE
// extension's own marker at a terminal is a person's own session whose write is
// published (TestAfterWriteFromAnIDETerminalPublishes); and the stages a write
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

// TestAfterWriteIsWiredToTheHook is the wiring itself, which every other test in
// this file takes for granted: they call publishAfterWrite directly, so they
// stay green if the init in write_publish.go is lost or a later file reassigns
// the variable — and then every semantic command would run the stub's no-op and
// publish nothing, silently. What decides that the init wins over
// after_write_stub.go's is the order two files' inits run in, which this checks
// rather than assumes: the target first (the pointer says which function, and
// says the variable is not nil), then one call through the variable carrying a
// change to the node, which is the path a command takes.
func TestAfterWriteIsWiredToTheHook(t *testing.T) {
	if runAfterWrite == nil {
		t.Fatal("runAfterWrite is nil: a semantic command's write would publish nothing and report nothing")
	}
	if got, want := reflect.ValueOf(runAfterWrite).Pointer(), reflect.ValueOf(publishAfterWrite).Pointer(); got != want {
		t.Fatalf("runAfterWrite holds %#x and publishAfterWrite is %#x: the hook a semantic command calls is not this file's, so its write would publish nothing", got, want)
	}
	_, db := afterWriteFixture(t)
	cmd, out := afterWriteCmd(t, "settings", "set")
	change := afterWriteChange{Key: "orchestrator.max_running", Store: settings.StoreLocal}
	if err := runAfterWrite(t.Context(), cmd, change); err != nil {
		t.Fatalf("the hook the variable holds reported %q although the stage publishes it:\n%s", ErrorText(err), out.String())
	}
	if v := nodeVersionOn(t, db); v != projectpolicy.Version {
		t.Errorf("the node is version %d after a write through the variable, want the %d the stage migrates it to:\n%s", v, projectpolicy.Version, out.String())
	}
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
// the audit log carries nothing, because a step the gate may not judge is one it
// audits nothing about.
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

// TestAfterWriteFromAnIDETerminalPublishes is that boundary's other side, and
// the reason the hook empties the reader its own run reads from rather than the
// command's stdin. The Claude Code IDE extension sets CLAUDE_CODE_SSE_PORT in a
// person's integrated terminal too, and agentenv.Session reads that marker as a
// coding agent's session exactly when the run has no terminal on its input
// (agentenv's package doc) — so a hook that handed its run an emptied command
// input would tell the loop nobody is there, and the person who just typed
// `fugaro settings set` in VS Code would be refused as an agent and left with a
// write that is "saved and is not live yet". With the marker set and a terminal
// on the command's input the write is published; the same marker with no
// terminal is still an agent's session, which is the classification read and not
// deleted.
func TestAfterWriteFromAnIDETerminalPublishes(t *testing.T) {
	isolateGateCredentials(t)

	t.Run("a person at the IDE extension's terminal", func(t *testing.T) {
		t.Setenv(agentenv.IDEMarker, "1")
		_, db := afterWriteFixture(t)
		cmd, out := afterWriteCmd(t, "settings", "set")
		ideTerminalStdin(t, cmd)
		change := afterWriteChange{Key: "orchestrator.max_running", Store: settings.StoreLocal}
		if err := publishAfterWrite(t.Context(), cmd, change); err != nil {
			t.Fatalf("a person's own IDE terminal was refused %q:\n%s", ErrorText(err), out.String())
		}
		if v := nodeVersionOn(t, db); v != projectpolicy.Version {
			t.Errorf("the published node is version %d, want the %d the stage migrates it to:\n%s", v, projectpolicy.Version, out.String())
		}
		if got := out.String(); strings.Contains(got, "a coding agent's session") {
			t.Errorf("the person's own terminal was read as a coding agent's session:\n%s", got)
		}
	})

	t.Run("the same marker with no terminal", func(t *testing.T) {
		noTerminal(t)
		t.Setenv(agentenv.IDEMarker, "1")
		_, db := afterWriteFixture(t)
		cmd, out := afterWriteCmd(t, "settings", "set")
		change := afterWriteChange{Key: "orchestrator.max_running", Store: settings.StoreLocal}
		err := publishAfterWrite(t.Context(), cmd, change)
		if err == nil {
			t.Fatalf("a session with no terminal published the change:\n%s", out.String())
		}
		if got, want := ErrorText(err), ErrorText(afterWriteNotLive(change)); got != want {
			t.Errorf("the refused session ends the command on %q, want the one sentence %q", got, want)
		}
		if v := nodeVersionOn(t, db); v != 3 {
			t.Errorf("the node moved to version %d in a session with no terminal:\n%s", v, out.String())
		}
		if got := out.String(); !strings.Contains(got, agentenv.IDEMarker) {
			t.Errorf("the refusal names no marker, so it is not the classification's:\n%s", got)
		}
	})
}

// ideTerminalStdin stubs stdinIsTerminal with the one fact its real body reads
// (init.go: the reader is a file whose descriptor is a terminal): the answer is
// true for the reader the command was handed and false for every other, which is
// what that descriptor says about a reader this process made itself.
// fakeTerminal's stub — true for any reader — cannot tell the command's own
// input from one a run swapped in, and telling those two apart is the whole of
// what the test above is about: the emptied reader the hook gives its own run
// must not be read as the command's terminal, and the command's own input must
// not be read as nobody.
func ideTerminalStdin(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	in := cmd.InOrStdin()
	old := stdinIsTerminal
	stdinIsTerminal = func(r io.Reader) bool { return r == in }
	t.Cleanup(func() { stdinIsTerminal = old })
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
// with the one sentence, the node is where it was, and the audit log carries
// nothing — nothing about a step the gate may not judge is recorded there,
// which is what makes a coding agent's session unable to leave the log claiming
// a widening. The step itself stays owed in the pending record, which is not
// the audit log and names a change and never a value (D1570): the change the
// session already wrote is not live, and `fugaro apply` reaches the same fence
// when the session runs it.
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
// command that finishes the job — `fugaro apply`. It named `fugaro update`
// while that command did not exist, and docs/contracts/config-ownership.md §7
// left the wording in this one helper to be repointed the day AP3 landed it.
//
// The sentence is true because the arm that prints it leaves the steps the run
// did not answer in the record `fugaro apply` reads (afterWriteOwed, D1563's
// "mark pending"): a sentence naming that command over an empty record would
// send the person to a command with nothing to do. Both sub-tests below read
// the record back for that reason.
func TestAfterWriteSentenceIsOneHelper(t *testing.T) {
	isolateGateCredentials(t)
	change := afterWriteChange{Key: "budget.per_run_usd", Store: settings.StoreLocal}
	const want = "budget.per_run_usd is saved and is not live yet: run `fugaro apply` to finish the steps it can answer"
	if got := ErrorText(afterWriteNotLive(change)); got != want {
		t.Errorf("the one sentence reads %q, want %q", got, want)
	}
	owed := []string{initflow.Policy}

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
		if got := owedStepsAfterTheHook(t); !slices.Equal(got, owed) {
			t.Errorf("the failed stage left %v owed, want the step it did not answer %v", got, owed)
		}
	})

	// And a stage a coding agent's session refused, which is a different stop in
	// a different world: one wording either way, and the step is owed all the
	// same. What a session may not do is publish that step — which is the write
	// path's fence and not the record's, and `fugaro apply` reaches the same
	// fence when the session runs it (contract §9, D1026).
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
		if got := owedStepsAfterTheHook(t); !slices.Equal(got, owed) {
			t.Errorf("the refused session left %v owed, want the step it did not answer %v", got, owed)
		}
	})
}

// owedStepsAfterTheHook is the record the hook left behind, read exactly as
// `fugaro apply` reads it (apply.LoadPending over this installation's own
// document): the steps the sentence the command ended on points that command at.
func owedStepsAfterTheHook(t *testing.T) []string {
	t.Helper()
	p, err := apply.LoadPending(os.Getenv, initProjectName, initProject)
	if err != nil {
		t.Fatalf("reading the record the hook left: %v", err)
	}
	var steps []string
	for _, s := range p.Steps {
		steps = append(steps, s.Step)
	}
	return steps
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
		{"a project-layer row a command wrote in a checkout", "agent.model", settings.StoreRepo, nil},
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
	//
	// No command writes one that way today — a key the machine's own config
	// carries is refused `--layer project`, because that layer is not one of its
	// own — so the case is reached from here: the machine's own key written into
	// the project layer file, which is what the store half of the rule is for.
	t.Run("a key that touches both layers", func(t *testing.T) {
		names := apply.WriteSteps(apply.Change{Kind: apply.Setting, Key: "budget.mode"}, settings.StoreProject)
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

// TestWritePublishRefusesUnmanaged is D1541's fence on this file (T7c): the
// after-write hook is where a semantic command's write reaches the project's
// own state — the policy node, and the layer file the node is read through — so
// a command the fence applies to is refused before the run is built, and nothing
// is published and no stage of the update runs.
//
// The hook opens its clients through the init engine rather than through the two
// seams the fence is free at (openCloud, openCloudFor), so it asks for itself,
// and it asks about the command that called (publishAfterWrite's own cmd): the
// selection and the door are that command's, and so is the answer. The command
// here is the one fenced command that really reaches this hook —
// `fugaro secrets set NAME --expires` writes `secret_expires` into the machine's
// own config, whose own layers declare the policy stage (settings.KeySteps, AP5)
// — so the pair of key and command is the one a person's line makes, and the
// refusal is the one that line meets.
//
// The world is the hook's own fixture, whose project carries no mark: the node
// stays at the version it was published at, which is the assertion that nothing
// was published, and `fugaro apply`'s sentence is nowhere, because no step was
// left owed for it to run.
func TestWritePublishRefusesUnmanaged(t *testing.T) {
	fugaroAtItsOwnName(t)
	_, db := afterWriteFixture(t)
	cmd, out := afterWriteCmd(t, "secrets", "set")
	change := afterWriteChange{Key: "secret_expires", Store: secretsSettingsStore("secret_expires")}
	if names := afterWriteStages(change); len(names) == 0 {
		t.Fatalf("a write of %s in %s owes no stage, so this test would be about a hook that returned before its fence", change.Key, change.Store)
	}

	err := publishAfterWrite(t.Context(), cmd, change)
	checkFenceRefusal(t, err, "secrets set")
	if v := nodeVersionOn(t, db); v != 3 {
		t.Errorf("the node is version %d after a refused run, want the 3 the fixture published: the hook published a project that is not managed", v)
	}
	if said := out.String(); strings.Contains(said, "is not live yet") {
		t.Errorf("the refused run said the change is saved and owes steps, so it filed a record for `fugaro apply`:\n%s", said)
	}
}
