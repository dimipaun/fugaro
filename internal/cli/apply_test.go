package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// applyWorld is the world every test here starts from: the installation
// updateBudgetRig builds, its rules generation deployed, and a working
// directory that is no repository's checkout (updateRig chdirs to a temp
// directory). The record is the machine's own — nothing here goes through a
// management command, because what those queue is internal/apply's test: this
// file is about what runs the steps.
func applyWorld(t *testing.T) (*initRig, *gcpfake.RTDB) {
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

// TestApplyUnderAgentMarker: a coding agent's session applies nothing. The
// cloud-writing steps answer with the session's own refusal — the one every
// door's steps answer with — and they stay owed, because a refusal is not an
// answer (D1571).
func TestApplyUnderAgentMarker(t *testing.T) {
	_, db := applyWorld(t)
	before := db.Value(projectpolicy.PathShared())
	t.Setenv("CLAUDECODE", "1")
	owed(t, initflow.Policy, apply.Change{Kind: apply.Setting, Key: "layer.defaults.agent.model"})

	out, errOut, err := executeStdin(t, "", "apply")
	if err == nil {
		t.Fatalf("a coding agent's session ran the policy step:\n%s", out+errOut)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the refusal is not the session's own:\n%s", got)
	}
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
	if err := applyCatalogBuild(cmd, lc); err == nil || !strings.Contains(ErrorText(err), initflow.AgentRefusal("CLAUDECODE")) {
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
