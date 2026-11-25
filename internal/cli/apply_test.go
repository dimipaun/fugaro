package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// applyWorld is the world every test here starts from: the installation
// updateBudgetRig builds, its rules generation deployed, and a working
// directory that is no repository's checkout (updateRig chdirs to a temp
// directory). The record is the machine's own — nothing here goes through a
// management command, because what those queue is internal/apply's test: this
// file is about what runs the steps.
//
// The project is a managed one, because `fugaro apply` writes to it: D2217's
// fence refuses an unmanaged project before the record is read, so a world
// without the marks is a world no step here is ever reached in
// (applyWorldUnmanaged is that world, and managed_require_test.go is the file
// about the refusal it answers with).
func applyWorld(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r, db := applyWorldUnmanaged(t)
	r.managedRuns(t, initProjectName, initProject)
	// A managed project's step is judged and recorded under the account the
	// credentials in force belong to (D1014), and a test binary has none.
	actingAccount(t)
	return r, db
}

// applyWorldUnmanaged is applyWorld in a project whose marks say nothing: the
// same installation, the same record and the same steps, and the fence's
// refusal in front of all of them.
func applyWorldUnmanaged(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r, db := updateBudgetRig(t)
	updating(t, r)
	// The installation's rules are the ones this binary deploys, so the policy
	// stage's publisher gate is not what any of these tests are about.
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	// It has published a node and the local config has moved since: the node
	// states a ceiling of 9 and a repository list of its own, the config states
	// 5 and this installation's repositories. A step that publishes therefore
	// has something to do, and a step whose work is already done is one the
	// stage's own read answers (D1575).
	//
	// A project that has NEVER published a node is the one state these tests
	// cannot start from: a first publish is the owner's own step — the typed
	// name, refused without a terminal and refused to a coding agent's session
	// (looseningWhy) — so `fugaro apply` can never make one, and a step queued
	// for it stays owed with the stage's own sentence.
	db.Set(projectpolicy.PathShared(), staleBudgetNode())
	return r, db
}

// owed queues one step as a management command that was told --no-apply leaves
// it: an entry in this machine's record, bound to the project and the GCP
// project the command acts on, with the change that asked for the step beside
// it.
func owed(t *testing.T, step string, c apply.Change) {
	t.Helper()
	p := record(t)
	p.Add(apply.PendingStep{Step: step, Project: initProjectName, GCPProject: initProject, Change: c})
	if err := apply.SavePending(os.Getenv, initProjectName, p); err != nil {
		t.Fatal(err)
	}
}

// record is what this machine owes, read the way the command reads it: an
// empty document for a machine that has no record at all.
func record(t *testing.T) *apply.Pending {
	t.Helper()
	p, err := apply.LoadPending(os.Getenv, initProjectName, initProject)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// recordPath is where that record lives.
func recordPath(t *testing.T) string {
	t.Helper()
	path, err := apply.PendingPath(os.Getenv, initProjectName)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// applyDocOf is the command's answer as its own document: the JSON arm of every
// test here reads it through the type the command writes it with, so a field
// renamed in one place and not the other is a failure rather than a pass.
func applyDocOf(t *testing.T, out string) applyDoc {
	t.Helper()
	var doc applyDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the answer is not this command's document: %v\n%s", err, out)
	}
	return doc
}

// applySteps is what the document says is owed, as step names.
func applySteps(rows []applyRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Step)
	}
	return out
}

// TestApplyRunsEachPendingStepOnce is AP3's red step: two queued changes that
// name the same step. The record keeps one entry for them, so the step runs
// once — a command that ran one step per queued change would publish the policy
// node twice, and a stage run twice is a stage that applied twice (spec rule 3).
//
// The order the steps run in is initflow's and not the record's, which is why
// the second step is queued first here: the rules generation is what the policy
// node is read under, so it runs ahead of the node however it was queued.
func TestApplyRunsEachPendingStepOnce(t *testing.T) {
	_, db := applyWorld(t)
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "image_catalog.entries.web.name"})
	owed(t, initflow.Policy, apply.Change{Kind: apply.Resource, Key: apply.ResourceImage})
	owed(t, initflow.Rules, apply.Change{Kind: apply.Resource, Key: apply.ResourceRepo})
	if n := len(record(t).Steps); n != 2 {
		t.Fatalf("the record holds %d entries for three changes that owe two steps, want 2", n)
	}

	out, errOut, err := executeStdin(t, "", "apply", "--json")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out+errOut)
	}
	doc := applyDocOf(t, out)
	if want := []string{initflow.Rules, initflow.Policy}; !slices.Equal(doc.Applied, want) {
		t.Errorf("the run applied %v, want %v", doc.Applied, want)
	}
	if len(doc.Pending) != 0 {
		t.Errorf("the record still owes %v after the run that answered it", applySteps(doc.Pending))
	}
	if db.Value(projectpolicy.PathShared()) == nil {
		t.Fatalf("the policy step published no node\n%s", out+errOut)
	}
	if v := nodeVersionOn(t, db); v != projectpolicy.Version {
		t.Errorf("the published policy node is version %d, want %d: the step did not run", v, projectpolicy.Version)
	}
	if p := record(t); len(p.Steps) != 0 || p.LastApply == "" {
		t.Errorf("the record after the run is %+v, want no steps and the config's stamp", p)
	}
	if !doc.Recorded || !doc.Current {
		t.Errorf("the answer reads recorded=%v current=%v, want both true after a run that finished the job", doc.Recorded, doc.Current)
	}
}

// TestApplyNothingPending: a machine that owes nothing runs nothing, says the
// one sentence, and is not given a record it never had — a write there would be
// this command inventing state (D1568).
func TestApplyNothingPending(t *testing.T) {
	r, _ := applyWorld(t)
	out, errOut, err := executeStdin(t, "", "apply")
	if err != nil {
		t.Fatalf("a machine that owes nothing exited non-zero: %v\n%s", err, out+errOut)
	}
	if !strings.Contains(out, "Nothing to apply.") {
		t.Errorf("the answer does not say nothing is owed: %q", out)
	}
	if _, err := os.Stat(recordPath(t)); err == nil {
		t.Errorf("the run wrote a record for a machine that owes nothing: %s", recordPath(t))
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("a machine that owes nothing ran terraform: %q", calls)
	}

	// The document is written on this arm too, and the empty list in it is the
	// answer "nothing is owed" — which is a different answer from a document
	// that could not be read, and the one a reader of the fence acts on (D1569).
	out, errOut, err = executeStdin(t, "", "apply", "--json")
	if err != nil {
		t.Fatalf("apply --json: %v\n%s", err, out+errOut)
	}
	doc := applyDocOf(t, out)
	if doc.Version != applyStatusVersion || doc.Project != initProjectName || doc.GCPProject != initProject {
		t.Errorf("the document is %+v, want version %d for %s in %s", doc, applyStatusVersion, initProjectName, initProject)
	}
	if doc.Recorded || doc.Current || len(doc.Pending) != 0 || len(doc.Applied) != 0 {
		t.Errorf("the document reads recorded=%v current=%v pending=%v applied=%v, want nothing recorded, nothing owed and nothing run",
			doc.Recorded, doc.Current, applySteps(doc.Pending), doc.Applied)
	}
	if !strings.Contains(out, `"pending": []`) {
		t.Errorf("the empty list is not written, so a reader cannot tell it from no answer: %s", out)
	}

	// --status is the same answer and runs nothing: it is the one read the
	// console is admitted to (D1569).
	out, errOut, err = executeStdin(t, "", "apply", "--status")
	if err != nil {
		t.Fatalf("apply --status: %v\n%s", err, out+errOut)
	}
	if !strings.Contains(out, "Nothing to apply.") {
		t.Errorf("--status does not say nothing is owed: %q", out)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("--status ran terraform: %q", calls)
	}
}

// TestApplyFailureKeepsPending: a step that fails leaves its change saved and
// the step owed, and the command says so and exits non-zero — and the run that
// follows finishes the job (D1563).
func TestApplyFailureKeepsPending(t *testing.T) {
	r, _ := applyWorld(t)
	r.script["apply"] = map[string]any{"exit": 1, "stderr": "Error: the apply did not land\n"}
	r.save(t)
	owed(t, initflow.Installation, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a step that failed exited 0:\n%s", out+errOut)
	}
	if p := record(t); len(p.Steps) != 1 || p.Steps[0].Step != initflow.Installation {
		t.Errorf("the record after the failure is %+v, want the installation step still owed", p.Steps)
	}
	if p := record(t); p.LastApply != "" {
		t.Error("the run that failed stamped the config as applied")
	}

	// The change is still saved and the step is still owed, so the next run
	// finishes it: the failure cost the run and not the work.
	r.script["apply"] = map[string]any{"exit": 0}
	r.save(t)
	out, errOut, err = executeStdin(t, "", "apply", "--json")
	if err != nil {
		t.Fatalf("the run after the failure: %v\n%s", err, out+errOut)
	}
	doc := applyDocOf(t, out)
	if !slices.Equal(doc.Applied, []string{initflow.Installation}) || len(doc.Pending) != 0 {
		t.Errorf("the second run applied %v and left %v owed, want the installation step and nothing", doc.Applied, applySteps(doc.Pending))
	}
	if p := record(t); len(p.Steps) != 0 || p.LastApply == "" {
		t.Errorf("the record after the second run is %+v, want no steps and the config's stamp", p)
	}
}

// TestApplyUnderAgentMarker: a coding agent's session applies nothing, and on
// a project that is not managed neither does a person: the run is stopped at
// the door, before the first step is reached (D2217). What the steps answer
// with on the way here is the session's own refusal, and this command is
// refused before it is any step's turn — so what the run says is the fence's,
// and what the record says is that the step is still owed, because a refusal
// is not an answer (D1571).
//
// The world is asked for by name (applyWorldUnmanaged) rather than reached
// through applyWorld: a managed project is where every other test in this file
// runs, and this is the refusal that world cannot reach.
func TestApplyUnderAgentMarker(t *testing.T) {
	_, db := applyWorldUnmanaged(t)
	before := db.Value(projectpolicy.PathShared())
	t.Setenv("CLAUDECODE", "1")
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a coding agent's session ran the policy step:\n%s", out+errOut)
	}
	// The one sentence every fenced command prints, checked the way the fence's
	// own tests check it: this run is refused by the same door, in the same
	// words, with a session's marker set and without one.
	checkFenceRefusal(t, err, "apply")
	// The node is the one that was there: the session published nothing. The
	// config states a ceiling of 5 and the node carries 9, so a publish would
	// show.
	if after := db.Value(projectpolicy.PathShared()); !reflect.DeepEqual(before, after) {
		t.Errorf("a coding agent's session published a node:\nbefore %v\nafter  %v", before, after)
	}
	if p := record(t); len(p.Steps) != 1 {
		t.Errorf("the record after the refused run is %+v, want the step still owed", p.Steps)
	}

	// The catalog build is the same answer, and its refusal comes before the
	// build: the step is billable and a session may not spend it (D1571, F6).
	// It is called here rather than through the command because the build
	// itself needs a real Cloud Build, and what this arm is about is that the
	// refusal stands in front of it — with a config that names no catalog
	// image the check first answers nothing is owed, which is the other half.
	r, _ := applyWorld(t)
	r.appendConfig(t, catalogBuildYAML)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cmd := newApplyCmd()
	cmd.SetOut(&bytes.Buffer{})
	// No run: this arm is the refusal, which comes before the build opens
	// anything at all.
	if err := applyCatalogBuild(cmd, lc, nil); err == nil || !strings.Contains(ErrorText(err), initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the catalog build in a coding agent's session answered %v, want the session's refusal", err)
	}
}

// TestApplyPrintsEachStepsDiffAndRejudgesLoosening: the step's own diff is
// printed, and a publish that would loosen a ceiling is refused at this door
// too (D1573, D1576). The change was saved when it was made; what `apply` adds
// is that the write it makes is judged now, against the node that is there now.
func TestApplyPrintsEachStepsDiffAndRejudgesLoosening(t *testing.T) {
	_, db := applyWorld(t)
	// The node states 3 and the local config states 5: publishing this run's
	// node raises what every run of the project is admitted by, which is the
	// widening D846 reserves for the project's own hand.
	db.Set(projectpolicy.PathShared(), staleBudgetNodeAt(3))
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "budget.per_run_usd"})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a publish that loosens a ceiling exited 0:\n%s", out+errOut)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, "per_run_usd") {
		t.Errorf("the step's diff is not in the answer:\n%s", got)
	}
	if !strings.Contains(got, "5") {
		t.Errorf("the diff does not name what the publish would write:\n%s", got)
	}
	if v := nodeVersionOn(t, db); v == 0 {
		t.Error("the refused publish wrote a node")
	}
	if p := record(t); len(p.Steps) != 1 || p.Steps[0].Step != initflow.Policy {
		t.Errorf("the record after the refused publish is %+v, want the policy step still owed", p.Steps)
	}
}

// TestApplyDetectsADeletedPendingFile: a record that is gone is not silently
// "nothing owed". The two read the same — nothing is recorded as owed — and the
// answer says which of the two this machine can tell apart (D1575).
func TestApplyDetectsADeletedPendingFile(t *testing.T) {
	_, _ = applyWorld(t)
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	if _, errOut, err := executeStdin(t, "", "apply"); err != nil {
		t.Fatalf("apply: %v\n%s", err, errOut)
	}
	path := recordPath(t)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the run that answered everything left no record: %v", err)
	}

	// A record that is there and owes nothing is the quiet arm: the stamp says
	// the config is the one that was applied, so there is nothing to warn about.
	out, _, err := executeStdin(t, "", "apply")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nothing to apply.") {
		t.Errorf("a record that owes nothing does not say so: %q", out)
	}
	if strings.Contains(out, "No record") {
		t.Errorf("a record that is there is reported as missing: %q", out)
	}

	// The same run with the record deleted reads the same answer and says the
	// difference, because nothing on this machine recomputes what the config
	// still needs.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out, _, err = executeStdin(t, "", "apply")
	if err != nil {
		t.Fatalf("a machine with no record exited non-zero: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing to apply.") || !strings.Contains(out, "No record of what this machine owes is on disk") {
		t.Errorf("the deleted record is not said:\n%s", out)
	}
	// And the document carries the same half of the answer as a flag, so a
	// reader that is not a person is not left to read a sentence.
	out, _, err = executeStdin(t, "", "apply", "--status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if doc := applyDocOf(t, out); doc.Recorded {
		t.Errorf("the document reads recorded=%v for a machine whose record was deleted", doc.Recorded)
	}
}

// TestApplyRedeploysOnlyTheCheckoutsRepository: the redeploy is that
// repository's own Terraform root (D1574). A machine with no checkout of the
// repository the change was made for cannot run it, the step stays owed, and
// the answer says where to run the command rather than sending the person back
// to the same machine.
func TestApplyRedeploysOnlyTheCheckoutsRepository(t *testing.T) {
	r, _ := applyWorld(t)
	owed(t, initflow.Repository, apply.Change{Kind: apply.Resource, Key: apply.ResourceRepo})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a redeploy ran with no checkout of the repository:\n%s", out+errOut)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, initflow.Repository+" still owed") {
		t.Errorf("the redeploy is not reported as still owed:\n%s", got)
	}
	if !strings.Contains(got, "checkout of the repository") {
		t.Errorf("the answer does not say where the redeploy has to run:\n%s", got)
	}
	// The sentence and not the two words in it: the stage's own detail says
	// "checkout of the repository" too, and what this arm of the answer adds is
	// that the command has to be run in one — instead of the general sentence,
	// which would send the person back to this machine.
	if want := "Run `fugaro apply` in a checkout of the repository the change was made for:"; !strings.Contains(out, want) {
		t.Errorf("the answer does not name the checkout the redeploy needs:\n%q", out)
	}
	if strings.Contains(out, "Run `fugaro apply` again") {
		t.Errorf("a redeploy this machine has no checkout for is sent back to this machine:\n%q", out)
	}
	if calls := r.ran(t, "apply"); len(calls) != 0 {
		t.Errorf("a repository with no checkout here was redeployed: %q", calls)
	}
	if p := record(t); len(p.Steps) != 1 || p.Steps[0].Step != initflow.Repository {
		t.Errorf("the record after the run is %+v, want the redeploy still owed", p.Steps)
	}

	out, _, err = executeStdin(t, "", "apply", "--status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	doc := applyDocOf(t, out)
	if !slices.Equal(applySteps(doc.Pending), []string{initflow.Repository}) {
		t.Errorf("the document owes %v, want the redeploy", applySteps(doc.Pending))
	}
	if len(doc.Applied) != 0 {
		t.Errorf("--status reports %v as applied, and it runs nothing", doc.Applied)
	}
}

// TestApplyEachStepChecksFirstAndDoesNothingWhenNothingIsOwed: the record is a
// hint and never authority (D1575). Every step reads the state before it does
// anything, so a step whose work is already done — or which this installation
// does not owe at all — costs a read and no change.
func TestApplyEachStepChecksFirstAndDoesNothingWhenNothingIsOwed(t *testing.T) {
	t.Run("a catalog image this installation does not build", func(t *testing.T) {
		_, _ = applyWorld(t)
		owed(t, apply.CatalogBuild, apply.Change{Kind: apply.Resource, Key: apply.ResourceImage})
		out, errOut, err := executeStdin(t, "", "apply", "--json")
		if err != nil {
			t.Fatalf("apply: %v\n%s", err, out+errOut)
		}
		doc := applyDocOf(t, out)
		if !slices.Equal(doc.Applied, []string{apply.CatalogBuild}) || len(doc.Pending) != 0 {
			t.Errorf("the run applied %v and left %v owed, want the catalog build answered and nothing owed",
				doc.Applied, applySteps(doc.Pending))
		}
	})

	t.Run("a policy node that is already current", func(t *testing.T) {
		_, db := applyWorld(t)
		owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
		if _, errOut, err := executeStdin(t, "", "apply"); err != nil {
			t.Fatalf("the first run: %v\n%s", err, errOut)
		}
		before := db.Value(projectpolicy.PathShared())
		// The same step queued again by a later change: the stage reads the node
		// first and finds nothing to do, so the entry clears and the node is the
		// one that was already there.
		owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
		out, errOut, err := executeStdin(t, "", "apply", "--json")
		if err != nil {
			t.Fatalf("the second run: %v\n%s", err, out+errOut)
		}
		doc := applyDocOf(t, out)
		if !slices.Equal(doc.Applied, []string{initflow.Policy}) || len(doc.Pending) != 0 {
			t.Errorf("the second run applied %v and left %v owed, want the step answered by its own check",
				doc.Applied, applySteps(doc.Pending))
		}
		if after := db.Value(projectpolicy.PathShared()); !reflect.DeepEqual(before, after) {
			t.Errorf("a step with nothing to do rewrote the node:\nbefore %v\nafter  %v", before, after)
		}
	})
}

// unreadableLayerBucket is the layer stage's read failing: the bucket this
// machine would publish the project layer into cannot be read, so nothing of
// the stage is decided. It is the one skip that is not "nothing to do"
// (init_layer_stage.go's skipUndone), and the seam is the one the layer stage's
// own tests use.
func unreadableLayerBucket(t *testing.T) {
	t.Helper()
	read := layerPublishedRead
	t.Cleanup(func() { layerPublishedRead = read })
	layerPublishedRead = func(context.Context, *localcfg.Config, string) ([]byte, int64, bool, error) {
		return nil, 0, false, errors.New("the bucket is not answering")
	}
}

// TestApplyKeepsAStepOwedWhenItsStageSkipsWithoutDoingTheWork: a skip is not
// always an answer. The layer stage skips when the bucket it would publish into
// cannot be read, and nothing was published — so the step stays owed, the answer
// does not report it as applied, and the record still names it (D1567, D1575).
// The other skip, a stage that found nothing to do, is the answer of its own
// test above.
func TestApplyKeepsAStepOwedWhenItsStageSkipsWithoutDoingTheWork(t *testing.T) {
	_, _ = applyWorld(t)
	owed(t, initflow.Layer, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	unreadableLayerBucket(t)

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a step whose stage skipped without doing its work exited 0:\n%s", out+errOut)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, "saved, not live yet: "+initflow.Layer+" still owed") {
		t.Errorf("the answer does not keep the step owed:\n%s", got)
	}
	if strings.Contains(got, "applied "+initflow.Layer) {
		t.Errorf("the answer reports the step as applied, and nothing was published:\n%s", got)
	}

	out, errOut, err = executeStdin(t, "", "apply", "--json")
	if err == nil {
		t.Fatalf("the run answered a step its stage never did:\n%s", out+errOut)
	}
	doc := applyDocOf(t, out)
	if !slices.Equal(applySteps(doc.Pending), []string{initflow.Layer}) {
		t.Errorf("the document owes %v, want the layer step: nothing was published", applySteps(doc.Pending))
	}
	if len(doc.Applied) != 0 {
		t.Errorf("the document reports %v as applied, and the stage published nothing", doc.Applied)
	}
	applySchemaCheck(t, []string{"apply", "--json"}, out)
	if p := record(t); !slices.Equal(pendingSteps(p), []string{initflow.Layer}) {
		t.Errorf("the record after the run is %v, want the layer step still owed", pendingSteps(p))
	}
	if p := record(t); p.LastApply != "" {
		t.Error("the run that answered nothing stamped the config as applied")
	}
}

// TestApplyKeepsAWorkspaceStepOwedWhenItsStageSkipsWithoutDoingTheWork: the
// workspace record stage skips in three states, and not one of them publishes
// anything — a config that names no database to publish the record in, a
// database this installation never adopted, and a workspace that lists no
// member. All three are read off the same answer (update_workspace.go's
// workspaceSkip), and all three are a step still owed rather than a step
// answered (D1563, D1567, D1575): the record stage's own half of the policy
// stage's rule.
//
// The config here is the first of the three, and the one an installation
// reaches first: the owner's half of a membership declared (workspace_members
// and workspace_cap) on a project that has no budget backend yet.
func TestApplyKeepsAWorkspaceStepOwedWhenItsStageSkipsWithoutDoingTheWork(t *testing.T) {
	r := updateRig(t)
	updating(t, r)
	// The project's two marks, and the account its steps are recorded under
	// (D1014): a project that is not managed is refused at the door (D2217), and
	// a step that never ran is not the state this test is about. The config
	// itself stays as the fixture wrote it — this arm is a config that names no
	// database to publish the record in.
	r.managedRuns(t, initProjectName, initProject)
	actingAccount(t)
	r.appendConfig(t, wsOwnerTable+wsOwnerCap)
	owed(t, initflow.Workspace, apply.Change{Kind: apply.Setting, Key: "workspace_cap.monthly"})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a workspace step whose stage published nothing exited 0:\n%s", out+errOut)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, "saved, not live yet: "+initflow.Workspace+" still owed") {
		t.Errorf("the answer does not keep the workspace step owed:\n%s", got)
	}
	if !strings.Contains(got, "no Firebase budget backend") {
		t.Errorf("the run did not skip on the state this test is about — a config that names no database to publish the record in:\n%s", got)
	}
	if strings.Contains(got, "applied "+initflow.Workspace) {
		t.Errorf("the answer reports the workspace step as applied, and nothing was published:\n%s", got)
	}

	out, errOut, err = executeStdin(t, "", "apply", "--json")
	if err == nil {
		t.Fatalf("the run answered a step its stage never did:\n%s", out+errOut)
	}
	doc := applyDocOf(t, out)
	if !slices.Equal(applySteps(doc.Pending), []string{initflow.Workspace}) {
		t.Errorf("the document owes %v, want the workspace step: nothing was published", applySteps(doc.Pending))
	}
	if len(doc.Applied) != 0 {
		t.Errorf("the document reports %v as applied, and the stage published nothing", doc.Applied)
	}
	applySchemaCheck(t, []string{"apply", "--json"}, out)
	if p := record(t); !slices.Equal(pendingSteps(p), []string{initflow.Workspace}) {
		t.Errorf("the record after the run is %v, want the workspace step still owed", pendingSteps(p))
	}
	if p := record(t); p.LastApply != "" {
		t.Error("the run that answered nothing stamped the config as applied")
	}
}

// queuedDuringRun makes a change land while an `apply` run is going. The run
// reads the record twice — once to know what to run, once to settle it when the
// run ends — and the second read is the one such a change has to survive
// (applyRecordRead, the seam that exists for exactly this), so add runs just
// before it. What add writes is what a management command told `--no-apply`
// writes: an entry in this machine's record, for the project the command acts
// on.
func queuedDuringRun(t *testing.T, add func()) {
	t.Helper()
	read := applyRecordRead
	reads := 0
	t.Cleanup(func() { applyRecordRead = read })
	applyRecordRead = func(lc *localcfg.Config) (*apply.Pending, bool, error) {
		reads++
		if reads == 2 {
			add()
		}
		return read(lc)
	}
}

// TestApplySettlesTheRecordItFindsWhenItsRunEnds: the record is a running
// machine's file, and a run is minutes of Terraform, so what is cleared is the
// entry that was read and nothing else (D1563). A step the same run answered and
// that a later change queued again — the same step, a change made after its
// stage ran — is a step still owed, and the config is not stamped as applied:
// the stamp says everything owed for the config as it stands has been published,
// and this run never published the new change (D1575).
//
// The other half is the entry for a step this run never had: a record written
// back whole from the copy the run read would drop it, and the change behind it
// would never be published and never be owed.
func TestApplySettlesTheRecordItFindsWhenItsRunEnds(t *testing.T) {
	_, _ = applyWorld(t)
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "budget.per_run_usd"})
	queuedDuringRun(t, func() {
		owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "budget.max_run_tokens"})
		owed(t, initflow.Layer, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	})

	out, errOut, err := executeStdin(t, "", "apply", "--json")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out+errOut)
	}
	doc := applyDocOf(t, out)
	if !slices.Equal(doc.Applied, []string{initflow.Policy}) {
		t.Errorf("the run applied %v, want the policy step it read", doc.Applied)
	}
	if got := applySteps(doc.Pending); !slices.Equal(got, []string{initflow.Policy, initflow.Layer}) {
		t.Errorf("the document owes %v, want the step queued again while the run was going and the step it never had", got)
	}
	if doc.Current {
		t.Error("the document reads the config as applied although a change queued during the run was never published")
	}
	applySchemaCheck(t, []string{"apply", "--json"}, out)

	p := record(t)
	if got := pendingSteps(p); !slices.Equal(got, []string{initflow.Policy, initflow.Layer}) {
		t.Errorf("the record after the run is %v, want both entries the run did not answer", got)
	}
	if p.Steps[0].Step != initflow.Policy || p.Steps[0].Change.Key != "budget.max_run_tokens" {
		t.Errorf("the record holds the policy entry the run read (%+v) instead of the one queued while it was going", p.Steps[0])
	}
	if p.LastApply != "" {
		t.Error("a run that never published the change queued during it stamped the config as applied")
	}
}

// TestApplyKeepsAStepRequeuedWithTheSameKeyWhileItsRunWasGoing: the record
// names the change that asked for a step and never its value (D1570), so a
// setting edited a second time while the run was going leaves an entry the run
// read in every member — the step, the project pair and the setting's key — and
// one member of its own: the queue sequence Add stamped it with. Settling by
// value alone would drop that second change as the step the run answered, and
// the run would then stamp the config as applied with the change unpublished
// and nothing owed (D1563, D1575).
//
// The other entry is the control: the rules step was read and answered, nothing
// re-queued it, and it is cleared — so the record is settled and not left alone.
func TestApplyKeepsAStepRequeuedWithTheSameKeyWhileItsRunWasGoing(t *testing.T) {
	_, _ = applyWorld(t)
	owed(t, initflow.Rules, apply.Change{Kind: apply.Resource, Key: apply.ResourceRepo})
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "budget.per_run_usd"})
	queuedDuringRun(t, func() {
		owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "budget.per_run_usd"})
	})

	out, errOut, err := executeStdin(t, "", "apply", "--json")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out+errOut)
	}
	doc := applyDocOf(t, out)
	if want := []string{initflow.Rules, initflow.Policy}; !slices.Equal(doc.Applied, want) {
		t.Errorf("the run applied %v, want %v", doc.Applied, want)
	}
	if got := applySteps(doc.Pending); !slices.Equal(got, []string{initflow.Policy}) {
		t.Errorf("the document owes %v, want the policy step re-queued while the run was going", got)
	}
	if doc.Current {
		t.Error("the document reads the config as applied although the change queued during the run was never published")
	}
	if p := record(t); p.LastApply != "" {
		t.Error("a run that never published the change queued during it stamped the config as applied")
	}
}

// TestUpdateLeavesAStepOwedWhenItsStageSkipsWithoutDoingTheWork: the rule is
// one rule for both doors (D1567). `update` clears the entries of the stages it
// ran successfully, and a stage that skipped because the bucket it publishes
// into could not be read is not one of them: the step stays owed, for the run
// that can do it.
func TestUpdateLeavesAStepOwedWhenItsStageSkipsWithoutDoingTheWork(t *testing.T) {
	r := updateManagedRig(t)
	updating(t, r)
	owed(t, initflow.Layer, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	unreadableLayerBucket(t)

	got, _ := updateRun(t, r, "update")
	if steps := pendingSteps(record(t)); !slices.Contains(steps, initflow.Layer) {
		t.Errorf("update cleared the layer step its own stage never answered: %v\n%s", steps, got)
	}
}

// TestUpdateClearsOnlyTheStagesItRan: `update` runs every stage, so the stages
// it answered are steps nothing owes any more and their entries are cleared —
// and the catalog build, which `update` never runs, stays owed (D1567, D1577).
func TestUpdateClearsOnlyTheStagesItRan(t *testing.T) {
	// The managed project and the session are what let a full `update` run in
	// this rig at all: the gate admits the steps a run with no terminal would
	// otherwise leave for the person, the installation's own local-config write
	// among them (update_managed_test.go's own note).
	r := updateManagedRig(t)
	updating(t, r)
	owed(t, initflow.Installation, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	owed(t, apply.CatalogBuild, apply.Change{Kind: apply.Resource, Key: apply.ResourceImage})

	got, err := updateRun(t, r, "update")
	if !strings.Contains(got, markDone+" "+initflow.Installation) {
		t.Fatalf("the run did not answer the installation step (%v):\n%s", err, got)
	}
	p := record(t)
	if steps := pendingSteps(p); slices.Contains(steps, initflow.Installation) {
		t.Errorf("update answered the installation step and left it owed: %v", steps)
	}
	if !slices.Contains(pendingSteps(p), apply.CatalogBuild) {
		t.Errorf("update cleared the catalog build, which it never runs: %v", pendingSteps(p))
	}
	if p.LastApply != "" {
		t.Error("update stamped the config as applied, which is the apply command's own record of its own run")
	}
}

// TestUpdateKeepsAStepQueuedWhileItsRunWasGoing: the record is a machine's file
// and an `update` is minutes of Terraform, so a management command run with
// --no-apply in another terminal queues into it while the update goes. The step
// it queued is a change this run never published — the stage that would have
// published it had already gone past — and update clears only the entries it
// read before its own first stage, so that step stays owed (D1563). The entry
// the run itself read and answered is still cleared, which is the half that
// says the record is settled rather than left alone.
func TestUpdateKeepsAStepQueuedWhileItsRunWasGoing(t *testing.T) {
	// The managed project and the session are what let a full `update` run in
	// this rig at all (update_managed_test.go's own note).
	r := updateManagedRig(t)
	updating(t, r)
	owed(t, initflow.Installation, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	queuedDuringRun(t, func() {
		owed(t, initflow.Plugin, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	})

	got, err := updateRun(t, r, "update")
	if !strings.Contains(got, markDone+" "+initflow.Installation) || !strings.Contains(got, markDone+" "+initflow.Plugin) {
		t.Fatalf("the run did not answer the steps this test owes (%v):\n%s", err, got)
	}
	steps := pendingSteps(record(t))
	if slices.Contains(steps, initflow.Installation) {
		t.Errorf("update answered the installation step and left it owed: %v", steps)
	}
	if !slices.Contains(steps, initflow.Plugin) {
		t.Errorf("update cleared the step queued while it was going, whose change it never published: %v\n%s", steps, got)
	}
}

// TestUpdateKeepsAStepRequeuedWithTheSameKeyWhileItsRunWasGoing: the same edit
// twice while an update is going, and the record holds the change's subject and
// never its value (D1570) — so the entry is the one the run read in every
// member but the queue sequence Add stamped it with, and clearing by value
// would drop the second change as the step the run answered. The
// installation-2 entry is the control: read, answered, nothing re-queued it,
// and cleared.
func TestUpdateKeepsAStepRequeuedWithTheSameKeyWhileItsRunWasGoing(t *testing.T) {
	r := updateManagedRig(t)
	updating(t, r)
	owed(t, initflow.Installation2, apply.Change{Kind: apply.Setting, Key: "budget.per_run_usd"})
	owed(t, initflow.Installation, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	queuedDuringRun(t, func() {
		owed(t, initflow.Installation, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	})

	got, err := updateRun(t, r, "update")
	if !strings.Contains(got, markDone+" "+initflow.Installation) || !strings.Contains(got, markDone+" "+initflow.Installation2) {
		t.Fatalf("the run did not answer the steps this test owes (%v):\n%s", err, got)
	}
	steps := pendingSteps(record(t))
	if slices.Contains(steps, initflow.Installation2) {
		t.Errorf("update answered the installation-2 step and left it owed: %v", steps)
	}
	if !slices.Contains(steps, initflow.Installation) {
		t.Errorf("update cleared the step another terminal re-queued while it was going, whose change it never published: %v\n%s", steps, got)
	}
}

// TestUpdateLeavesTheRedeployOwedWhenItHasNoCheckout: the redeploy is that
// repository's own Terraform root (D1574), so an `update` run where there is no
// checkout of it is not a run that answered the step — its stage skips, and the
// skip is the one skip the clearing rule singles out, because a stage that
// skipped and did nothing is an answer everywhere else (applyAnswered). The
// step stays owed for the machine that has the checkout, with applyKeptLine's
// own sentence saying so.
func TestUpdateLeavesTheRedeployOwedWhenItHasNoCheckout(t *testing.T) {
	r := updateManagedRig(t)
	updating(t, r)
	owed(t, initflow.Repository, apply.Change{Kind: apply.Resource, Key: apply.ResourceRepo})

	got, _ := updateRun(t, r, "update")
	if steps := pendingSteps(record(t)); !slices.Contains(steps, initflow.Repository) {
		t.Errorf("update cleared the redeploy of a repository it has no checkout of: %v\n%s", steps, got)
	}
}

// TestOnlyTheUpdateDoorSettlesTheRecord: the record is settled by the command
// whose job is to bring an installation up to date (D1567) — read before the
// first stage and cleared at the end — and a second door doing either would
// settle a record it did not write and cannot date. Both halves are pinned
// here, at the seam rather than through a run: `setup` converges the same
// stages and reaches readOwedBefore and clearAnsweredSteps at all
// (init_stages.go's converge calls both), and a setup that cleared what it
// answered would drop steps the run had published nothing for.
//
// The redeploy is in the result beside the installation for a second reason:
// the clearing rule drops a stage that skipped and did nothing everywhere
// except that one (D1574), and this is where both halves of that rule are
// read without a run's own stages having to skip the way the entry says.
func TestOnlyTheUpdateDoorSettlesTheRecord(t *testing.T) {
	r := updateRig(t)
	updating(t, r)
	owed(t, initflow.Installation, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})
	owed(t, initflow.Repository, apply.Change{Kind: apply.Resource, Key: apply.ResourceRepo})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	read := record(t)
	answered := &initflow.Result{Stages: []initflow.StageResult{
		{Name: initflow.Installation, State: initflow.Changed},
		{Name: initflow.Repository, State: initflow.Skipped},
	}}

	e, _ := stageEngine(t, "", &initOptions{door: applyName})
	e.lc = lc
	e.readOwedBefore()
	if e.owedBefore != nil {
		t.Error("a door that is not update read the record as the one that clears it")
	}
	// The read is in place and it is still not this door's to clear: what
	// stops it is the door and not a record that was never read.
	e.owedBefore = read
	e.clearAnsweredSteps(answered)
	if got := pendingSteps(record(t)); !slices.Equal(got, pendingSteps(read)) {
		t.Errorf("a door that is not update cleared the record: %v, want %v", got, pendingSteps(read))
	}

	// And the door whose job it is does both.
	e.r.o.door = updateDoor.name
	e.owedBefore = nil
	e.readOwedBefore()
	if e.owedBefore == nil {
		t.Fatal("update read nothing before its first stage, so nothing would be cleared")
	}
	e.clearAnsweredSteps(answered)
	got := pendingSteps(record(t))
	if slices.Contains(got, initflow.Installation) {
		t.Errorf("update left the step its own stage answered owed: %v", got)
	}
	if !slices.Contains(got, initflow.Repository) {
		t.Errorf("update cleared the redeploy its own stage skipped without doing the work: %v", got)
	}
}

// TestApplyKeptLineNeverPromisesASecondRunForAStepItCannotAnswer: this sentence
// is the whole of what the reader of a kept step is given, and a second
// `fugaro apply` is not the fix for a step this command can never answer — the
// workspace's own project confirms its grant by a name typed at a terminal no
// run of apply has, so the command named has to be the one that reaches that
// terminal.
func TestApplyKeptLineNeverPromisesASecondRunForAStepItCannotAnswer(t *testing.T) {
	var b strings.Builder
	applyKeptLine(&b, []string{initflow.WorkspaceProject})
	if got := b.String(); strings.Contains(got, "Run `fugaro apply` again") {
		t.Errorf("a kept workspace-project is sent back to the command that left it:\n%s", got)
	}
	if !strings.Contains(b.String(), "Run `fugaro update`") {
		t.Errorf("a kept workspace-project does not name the door that can answer it:\n%s", b.String())
	}
}

// TestApplyKeptLinePromisesOnlyTheStepsASecondRunFinishes: the sentence every
// other kept step gets is the general one, and the steps that reach it are not
// all steps a second run finishes — a name this door registers no stage under,
// the workspace's own project's among them, is kept however often the command
// runs (applyRunSteps), and the record is a file nothing fences, so a name no
// build knows reaches it too (TestApplyKeptLineEscapesAStepNameTheRecordCarries
// is that same line). Naming this command is D1563's, and promising the job
// would send the reader of such a step back to this machine for the same
// sentence and no more work done.
func TestApplyKeptLinePromisesOnlyTheStepsASecondRunFinishes(t *testing.T) {
	for _, name := range []string{initflow.Project, initflow.Services, initflow.Compute, "no-such-stage"} {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			applyKeptLine(&b, []string{name})
			got := b.String()
			if strings.Contains(got, "to finish the job") {
				t.Errorf("a kept step this command has no stage for is promised the job:\n%s", got)
			}
			if !strings.Contains(got, "Run `fugaro apply` again") {
				t.Errorf("the kept line does not name the command that runs the steps it has:\n%s", got)
			}
		})
	}
}

// TestApplyKeptLineForAStepItHasNoStageFor is the same sentence through the
// command, on the step that reaches it in a real record: the project stage is
// `setup`'s (initStages registers it for a first run and for nothing else), so
// an entry naming it is kept for ever — and the answer says what this command
// will do about it rather than that the job is one run away.
func TestApplyKeptLineForAStepItHasNoStageFor(t *testing.T) {
	_, _ = applyWorld(t)
	owed(t, initflow.Project, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a run that could not answer an owed step exited 0:\n%s", out+errOut)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(out, "Run `fugaro apply` again to finish the steps it can answer") {
		t.Errorf("the answer does not name the command that runs the steps it has:\n%s", out)
	}
	// And the run's own error says the same: it is the sentence a script sees,
	// and the one a person who piped the answer away is left with.
	if !strings.Contains(got, "still owed; run `fugaro apply` again to finish the steps it can answer") {
		t.Errorf("the error does not name the command that runs the steps it has:\n%q", got)
	}
	if strings.Contains(got, "to finish the job") {
		t.Errorf("a step this command has no stage for is promised the job:\n%s", got)
	}
}

// TestApplyKeptLineEscapesAStepNameTheRecordCarries: the record is a file in
// the machine's own config directory and nothing fences it, so a step name in it
// is a value this command does not own — and a name this build registers no
// stage for is kept and named rather than dropped (LoadPending checks the
// entry's binding and not its name, D1575). The kept line is a line of this
// program's own, and the one that says the change is not live yet: an escape
// sequence in a name would overwrite exactly that sentence on the owner's
// terminal. The status table escapes the same names; this is the run's answer.
func TestApplyKeptLineEscapesAStepNameTheRecordCarries(t *testing.T) {
	_, _ = applyWorld(t)
	hostile := "no-such-stage\x1b[2K\x07-and-more"
	owed(t, hostile, apply.Change{Kind: apply.Setting, Key: "budget.per_run_usd"})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("an owed step this build cannot run exited 0:\n%s", out+errOut)
	}
	if got := out + errOut; strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("the record's own bytes reached the terminal:\n%q", got)
	}
	if want := "saved, not live yet: " + safetext.Field(hostile) + " still owed"; !strings.Contains(out, want) {
		t.Errorf("the answer does not name the step it kept, escaped to %q:\n%q", want, out)
	}
}

// pendingSteps is a record's entries as step names, in the order they are
// written: what a test about clearing reads, where the order the steps run in
// is not what it is asking about.
func pendingSteps(p *apply.Pending) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.Step)
	}
	return out
}

// TestEachCloudWritingStepWritesItsAuditRecordEvenIfALaterStepFails: the record
// of a managed project's gated write is written by the step that made it, so a
// run that fails at a later step still has the earlier step's record (D1545).
// A record written once at the end of the run would be a record a failure
// erases, and the step that failed — whose write never landed — would be the
// one thing left looking as though it had.
func TestEachCloudWritingStepWritesItsAuditRecordEvenIfALaterStepFails(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	updating(t, r)
	// Two gated cloud writes, and the second one's write is refused by the
	// database: the rules generation is deployed and recorded first, and the
	// policy step fails after it.
	db.Set(projectpolicy.PathShared(), staleBudgetNode())
	db.Deny(projectpolicy.PathShared())
	owed(t, initflow.Rules, apply.Change{Kind: apply.Resource, Key: apply.ResourceRepo})
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "budget.per_run_usd"})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a step whose write was refused exited 0:\n%s", out+errOut)
	}
	recs := audits(t, r)
	if len(recs) == 0 {
		t.Fatalf("the step that ran wrote no audit record, so a failure erased it:\n%s", out+errOut)
	}
	named := false
	for _, rec := range recs {
		if rec.Command == "fugaro apply" {
			named = true
		}
	}
	if !named {
		t.Errorf("no record names the command that made the write: %+v", recs)
	}
	steps := pendingSteps(record(t))
	if !slices.Contains(steps, initflow.Policy) {
		t.Errorf("the step whose write was refused is not still owed: %v", steps)
	}
	if slices.Contains(steps, initflow.Rules) {
		t.Errorf("the step that ran is still owed: %v", steps)
	}
}

// TestApplyJudgesItsTerraformStepsAgainstTheRunsOwnCover: a chain builds no
// review, so the gate judges the plan of a step `apply` runs against the cover
// the review would have printed — the run's own — and not against the refusing
// cover of a step whose review was never built. A managed project's Terraform
// steps would otherwise stay owed forever, refused by a cover that says the
// review was not available, which is the opposite of what this command is for.
func TestApplyJudgesItsTerraformStepsAgainstTheRunsOwnCover(t *testing.T) {
	r := updateManagedRig(t)
	updating(t, r)
	owed(t, initflow.Installation, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})

	out, errOut, err := executeStdin(t, "", "apply", "--json")
	if err != nil {
		t.Fatalf("apply in a managed project: %v\n%s", err, out+errOut)
	}
	doc := applyDocOf(t, out)
	if !slices.Equal(doc.Applied, []string{initflow.Installation}) || len(doc.Pending) != 0 {
		t.Errorf("the run applied %v and left %v owed, want the installation step and nothing",
			doc.Applied, applySteps(doc.Pending))
	}
	if calls := r.ran(t, "apply"); len(calls) == 0 {
		t.Errorf("the step was answered without applying its plan: %q", r.calls(t))
	}
	// And the step that applied is recorded, under the door that ran: the
	// record is what an owner reads afterwards, and a chain's write filed as an
	// update's would name a command that did not run (D1014).
	recs := audits(t, r)
	if len(recs) == 0 {
		t.Fatalf("the step that applied wrote no audit record:\n%s", out+errOut)
	}
	for _, rec := range recs {
		if rec.Command != "fugaro "+applyName {
			t.Errorf("a record of this run names the command %q", rec.Command)
		}
	}
}

// TestApplyStatusJSONMatchesSchema validates the document every arm of this
// command writes against schemas/apply-status.schema.json, the contract a
// reader outside this binary reads it by (D1569, F9).
func TestApplyStatusJSONMatchesSchema(t *testing.T) {
	_, _ = applyWorld(t)
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})

	for _, args := range [][]string{
		{"apply", "--status", "--json"},
		{"apply", "--json"},
	} {
		out, errOut, err := executeStdin(t, "", args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out+errOut)
		}
		applySchemaCheck(t, args, out)
	}
}

// TestApplyJSONAnswersItsDocumentWhenTheCatalogBuildFails: --json answers one
// document on every outcome (G2). The catalog image is the one step that is not
// a stage, and a build this run may not make returns from applyRunSteps before
// the answer the loop prints — so the document is printed on that arm too,
// holding the record as it was read: nothing applied, every step still owed. A
// run that printed nothing would leave a script's stdout empty and the exit
// code the only thing to read, which is the one shape no other failure arm of
// this command has.
func TestApplyJSONAnswersItsDocumentWhenTheCatalogBuildFails(t *testing.T) {
	r, _ := applyWorld(t)
	// A catalog this installation builds: a config that names none answers that
	// nothing is owed before the build is reached, which is the other arm.
	r.appendConfig(t, catalogBuildYAML)
	owed(t, apply.CatalogBuild, apply.Change{Kind: apply.Resource, Key: apply.ResourceImage})
	// The one build refusal this test can reach without Cloud Build: the step is
	// billable and a coding agent's session may not spend it (D1571).
	t.Setenv("CLAUDECODE", "1")

	out, errOut, err := executeStdin(t, "", "apply", "--json")
	if err == nil {
		t.Fatalf("a coding agent's session built a billable catalog image:\n%s", out+errOut)
	}
	if got := out + errOut + ErrorText(err); !strings.Contains(got, initflow.AgentRefusal("CLAUDECODE")) {
		t.Fatalf("the run did not reach the catalog build's own refusal:\n%s", got)
	}
	doc := applyDocOf(t, out)
	if !slices.Equal(applySteps(doc.Pending), []string{apply.CatalogBuild}) {
		t.Errorf("the document owes %v, want the catalog build: a build that failed leaves every step owed", applySteps(doc.Pending))
	}
	if len(doc.Applied) != 0 {
		t.Errorf("the document reports %v as applied, and this run answered nothing", doc.Applied)
	}
	if p := record(t); len(p.Steps) != 1 {
		t.Errorf("the record after the failed run is %+v, want the step still owed", p.Steps)
	}
	applySchemaCheck(t, []string{"apply", "--json"}, out)
}

// applyCatalogOneYAML is a catalog whose one entry is written in project state:
// a whole-catalog build of it submits one build and reads no repository source,
// and so no provider credential (catalogProviderEnv), which is all a test about
// WHICH installation a catalog build acts on needs.
const applyCatalogOneYAML = `image_catalog:
  entries:
    - name: tools
      tags: [node]
      source: { kind: inline, inline: "FROM example.invalid/base\nRUN true\n" }
`

// applyCatalogBaseImage is the base a catalog build is built on, pinned into the
// config these tests run against: `apply` runs the build itself and so cannot
// pass `--base` the way a person's `image build` does, and a development build
// has no published base to fall back on.
const applyCatalogBaseImage = "base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }\n"

// otherProjectConfig writes a second project's config beside this machine's own
// and answers its path: the same config in every other respect — the fakes'
// endpoints, the budget block, the catalog — under another name, GCP project
// and region, so the two installations differ in exactly what a submission and
// a lease name.
func otherProjectConfig(t *testing.T, name, gcpProject, region string) string {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, r := range [][2]string{
		{"name: " + initProjectName + "\n", "name: " + name + "\n"},
		{"gcp_project: " + initProject + "\n", "gcp_project: " + gcpProject + "\n"},
		{"region: us-east5\n", "region: " + region + "\n"},
	} {
		if !strings.Contains(body, r[0]) {
			t.Fatalf("this machine's own config no longer carries %q, so the second project's config cannot be built out of it:\n%s", r[0], body)
		}
		body = strings.Replace(body, r[0], r[1], 1)
	}
	out := filepath.Join(filepath.Dir(path), name+".yaml")
	if err := os.WriteFile(out, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestApplyCatalogBuildsTheCatalogOfTheProjectItWasAimedAt: `fugaro apply` can
// be aimed at a project the working directory is not a checkout of — its own
// selection is checkout-blind (settingsAimedSelection, --project and --config
// being localcfg.Select's steps 2 and 1) — while the build command it runs
// inside itself selects the other way round: the flags first and the working
// directory's checkout at step 3 (selectProject). So the aim has to reach the
// build. A build left to select for itself reads the checkout, builds THAT
// project's catalog, leases the Cloud Build against that installation's budget
// — and returns success, so this run then clears the aimed project's entry as
// answered: the aimed project's catalog is never built and its record says
// otherwise.
func TestApplyCatalogBuildsTheCatalogOfTheProjectItWasAimedAt(t *testing.T) {
	// The checkout belongs to vega, whose config this machine holds too:
	// another installation, of another GCP project and another region, so a
	// build that followed the checkout cannot produce the aimed project's
	// images by accident.
	checkout := strings.Replace(bitbucketYAML, "project: "+initProjectName, "project: vega", 1)
	fb, _, f := catalogBuildCheckout(t, checkout, applyCatalogOneYAML)
	// A catalog build in a development build needs the installation's base
	// pinned: the flag is not there to be passed (this command runs the build
	// itself), and `fugaro dev` has no published base to fall back on.
	f.appendConfig(t, applyCatalogBaseImage)
	stubCatalogProvider(t, nil)
	vega := otherProjectConfig(t, "vega", "proj-7777", "europe-west1")
	// vega's installation is one this machine has read the name of before, so
	// the check a build makes of it is answered from the cache: a build that
	// followed the checkout into vega gets past that read — which here would go
	// to a bucket nothing serves — and is refused by what is left, naming vega
	// rather than the installation this command was aimed at.
	vlc, err := localcfg.Load(vega)
	if err != nil {
		t.Fatal(err)
	}
	if err := localcfg.SaveNameCheck(os.Getenv, "vega", localcfg.NameCheck{GCPProject: "proj-7777", RunsBucket: vlc.BucketURL(), CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	owed(t, apply.CatalogBuild, apply.Change{Kind: apply.Resource, Key: apply.ResourceImage})

	out, errOut, err := executeStdin(t, "", "apply", "--project", initProjectName, "--json")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out+errOut)
	}
	if got := buildPosts(fb); got != 1 {
		t.Fatalf("the run submitted %d builds, want the aimed project's one catalog entry:\n%s", got, out+errOut)
	}
	if subs := catalogBuildSubs(t, fb); subs["_IMAGE"] != catalogBuildImage(t, "tools") {
		t.Errorf("the build was submitted for %v, want the aimed installation's image %v", subs["_IMAGE"], catalogBuildImage(t, "tools"))
	}
	doc := applyDocOf(t, out)
	if !slices.Equal(doc.Applied, []string{apply.CatalogBuild}) || len(doc.Pending) != 0 {
		t.Errorf("the run applied %v and left %v owed, want the aimed project's catalog build answered and nothing owed",
			doc.Applied, applySteps(doc.Pending))
	}
	if p := record(t); len(p.Steps) != 0 {
		t.Errorf("the record after the run is %+v, want nothing owed", p.Steps)
	}
}

// TestApplyJSONWithAnOwedCatalogBuildPrintsOnlyItsDocument: --json answers one
// document and no line of anything else (G2). The catalog build is the one owed
// step this command runs itself rather than through the loop, and it opened its
// own run on the command's stdout: its step lines, its progress and its closing
// line landed ahead of the document, so a script or the console reading this
// arm could not parse it — and the run filed a second log beside the command's
// own for one command's work (D1352, D1354). The build runs inside this
// command's own run instead, which is the account the document is printed on
// top of.
func TestApplyJSONWithAnOwedCatalogBuildPrintsOnlyItsDocument(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, applyCatalogOneYAML)
	f.appendConfig(t, applyCatalogBaseImage)
	stubCatalogProvider(t, nil)
	owed(t, apply.CatalogBuild, apply.Change{Kind: apply.Resource, Key: apply.ResourceImage})

	out, errOut, err := executeStdin(t, "", "apply", "--json")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out+errOut)
	}
	if got := buildPosts(fb); got != 1 {
		t.Fatalf("the run submitted %d builds, want the owed catalog build to have run:\n%s", got, out+errOut)
	}
	// The whole of stdout is the document: a step line, a progress line or the
	// build's own summary before it makes this read fail, which is exactly what
	// a script or the console would do with it.
	doc := applyDocOf(t, out)
	if !slices.Equal(doc.Applied, []string{apply.CatalogBuild}) || len(doc.Pending) != 0 {
		t.Errorf("the run applied %v and left %v owed, want the catalog build answered and nothing owed",
			doc.Applied, applySteps(doc.Pending))
	}
	// And it is one run's work, filed once: an `image-build` log beside this
	// command's own would be a second account of one command.
	onlyLog(t, applyName)
	entries, err := os.ReadDir(logDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "image-build-") {
			t.Errorf("the run filed %s beside its own log: the catalog build opened a run of its own", e.Name())
		}
	}
}

// TestApplyNeverOnboardsAnUnlistedRepository: this door runs the steps a
// management change saved on an answer of its own rather than a person's — a
// pipe, a script, `--json` and the console all reach it (D1572) — so an owed
// redeploy of a repository the local config does not list is left for the user,
// with the line naming `fugaro setup`, exactly as `update`'s converge leaves it
// (D5). Nothing a context can pass this command makes it enrol a repository,
// which is what its --no-apply half exists to keep out of a script in the first
// place.
func TestApplyNeverOnboardsAnUnlistedRepository(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	e, _ := stageEngine(t, "", &initOptions{door: applyName, yes: true, nonInteractive: true})
	unknownRepo(e)
	fakeTerminal(t)
	rs := newRepositoryStage(e)
	rs.engineStage.run = neverRun(t)
	res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	if got := stateOf(res, initflow.Repository); got != initflow.NeedsYou {
		t.Fatalf("the repository stage is %v, want needs-you: this door onboarded a repository nobody opted in\n%+v", got, res.Stages)
	}
	if len(res.Left) != 1 || res.Left[0].Kind != initflow.LeftCommand || res.Left[0].Text != selfCommand()+" "+setupDoor.name {
		t.Fatalf("the run leaves %+v, want the one line naming the door that enrols", res.Left)
	}
	if d := stageDetailOf(res, initflow.Repository); !strings.Contains(d, selfCommand()+" "+applyName+" does not enrol one by asking") {
		t.Errorf("the stage's own line is %q, want this door's, not a door that does enrol", d)
	}
}

// applySchemaPath is schemas/apply-status.schema.json, resolved from this
// file's own location: every test here chdirs into a rig's world, so a relative
// path would look for the schema beside a temporary directory.
func applySchemaPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "schemas", "apply-status.schema.json")
}

// applySchemaCheck validates one printed document against the schema file.
func applySchemaCheck(t *testing.T, args []string, out string) {
	t.Helper()
	raw, err := os.ReadFile(applySchemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/apply-status.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v printed no document: %v\n%s", args, err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("%v does not match schemas/apply-status.schema.json: %v\n%s", args, err, out)
	}
}
