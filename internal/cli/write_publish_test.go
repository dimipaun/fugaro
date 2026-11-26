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
//
// The project is a managed one, because the publish under a command the fence
// classifies exempt is a write to the project all the same and asks the read
// for itself (publishAfterWrite): a world that answered "not managed" would
// refuse every publish below for that reason rather than the test's own. The
// unmanaged world is afterWriteUnmanagedFixture's, and what happens there is
// TestWritePublishRefusesUnmanaged's.
func afterWriteFixture(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r, db := afterWriteUnmanagedFixture(t)
	r.managedRuns(t, initProjectName, initProject)
	return r, db
}

// afterWriteUnmanagedFixture is the same world with a project carrying no
// mark: the one explicit unmanaged case the refusals are read in.
func afterWriteUnmanagedFixture(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r, db := updateBudgetRig(t)
	// The rig's own default is a managed installation (newInitRig), and this
	// world is the one explicit unmanaged case: the project's own half of the
	// fact goes, so the read answers "not managed" from the label alone.
	r.removeManagedInstallation(t)
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
		r, db := afterWriteFixture(t)
		// A project whose marks cannot be read, which is the one world where the
		// run is built at all on an installation the read did not answer "managed"
		// for (the fence warns and goes on, D2218) and the run's own gate keeps
		// every confirmation it had — so the session is judged by its marker here,
		// which is the classification this sub-test is about.
		r.crm.FailGet(initProject, 500, "INTERNAL", "the marks could not be read")
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
	r, _ := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
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
// own state — the policy node, and the layer file the node is read through —
// and its clients come from the init engine rather than from the two seams the
// fence is free at (openCloud, openCloudFor), so it asks for itself, on the
// world's own unmanaged project. Nothing is published either way, and what the
// classification decides is what the command's own refusal would have said — not
// what the person reads here.
//
// Both commands are answered at the hook's own moment, which is after the
// calling command's write: a command the classification fences reaches it too —
// `fugaro secrets set NAME --expires` writes `secret_expires` into the machine's
// own config, whose row declares the policy stage (settings.KeySteps, AP5), and
// then arrives here — so the fence's own refusal, that sentence saying nothing
// was stored, may not be what the person reads about a record that IS written
// and a value they never passed. A command the classification exempts is no
// licence for the publish that runs under it either (`settings set` writes this
// machine's file and names no project, and the stage behind it writes the
// project's node). What either gets is what every stage that cannot leave a
// change live gets: nothing is published, the steps stay owed, and the command
// ends on the one sentence that points `fugaro apply` at them (afterWriteOwed,
// D2614).
func TestWritePublishRefusesUnmanaged(t *testing.T) {
	fugaroAtItsOwnName(t)

	t.Run("a fenced command is not denied the write that landed", func(t *testing.T) {
		_, db := afterWriteUnmanagedFixture(t)
		cmd, out := afterWriteCmd(t, "secrets", "set")
		change := afterWriteChange{Key: "secret_expires", Store: secretsSettingsStore("secret_expires")}
		names := afterWriteStages(change)
		if len(names) == 0 {
			t.Fatalf("a write of %s in %s owes no stage, so this test would be about a hook that returned before its fence", change.Key, change.Store)
		}

		err := publishAfterWrite(t.Context(), cmd, change)
		if err == nil {
			t.Fatalf("the publish of a fenced command's write was reported live on a project that is not managed:\n%s", out.String())
		}
		said := ErrorText(err)
		if got, want := said, ErrorText(afterWriteNotLive(change)); got != want {
			t.Errorf("the unmanaged publish ends the command on %q, want the one sentence %q", got, want)
		}
		for _, never := range []string{managedFencedCommands["secrets set"], managedFlagCommand()} {
			if strings.Contains(said, never) {
				t.Errorf("the command reads the fence's own refusal (%q) about a write that has landed:\n%s", never, said)
			}
		}
		if v := nodeVersionOn(t, db); v != 3 {
			t.Errorf("the node is version %d after a refused run, want the 3 the fixture published: the hook published a project that is not managed", v)
		}
		// The record is what makes the sentence true: `fugaro apply` reads it
		// and runs the steps once the project is flagged managed.
		if got := owedStepsAfterTheHook(t); !slices.Equal(got, names) {
			t.Errorf("the refused publish left %v owed, want %v", got, names)
		}
	})

	t.Run("an exempt command publishes nothing", func(t *testing.T) {
		_, db := afterWriteUnmanagedFixture(t)
		cmd, out := afterWriteCmd(t, "settings", "set")
		change := afterWriteChange{Key: "orchestrator.max_running", Store: settings.StoreLocal}
		if names := afterWriteStages(change); len(names) == 0 {
			t.Fatalf("a write of %s in %s owes no stage, so this test would be about a hook that returned before its fence", change.Key, change.Store)
		}

		err := publishAfterWrite(t.Context(), cmd, change)
		if err == nil {
			t.Fatalf("the publish of an exempt command's write was reported live on a project that is not managed:\n%s", out.String())
		}
		if got, want := ErrorText(err), ErrorText(afterWriteNotLive(change)); got != want {
			t.Errorf("the refused publish ends the command on %q, want the one sentence %q", got, want)
		}
		if v := nodeVersionOn(t, db); v != 3 {
			t.Errorf("the node is version %d after a refused run, want the 3 the fixture published: the hook published a project that is not managed", v)
		}
		// The steps stay owed, which is what makes the sentence true: `fugaro
		// apply` runs them once the project is flagged managed, and it reaches
		// the same fence until it is.
		if got, want := owedStepsAfterTheHook(t), []string{initflow.Policy}; !slices.Equal(got, want) {
			t.Errorf("the refused publish left %v owed, want %v", got, want)
		}
	})
}
