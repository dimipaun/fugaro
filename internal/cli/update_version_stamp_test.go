package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// stampRig is an installation `fugaro update` converges, with an adopted budget
// database to write the required-CLI node in and no agent marker in the
// environment: whether the machine running the tests is a coding agent's
// session is not what these tests are about (clearAgentMarkers).
func stampRig(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r := updateRig(t) // a release build, 0.2.0
	db := buildBudgetForRig(t, r)
	clearAgentMarkers(t)
	return r, db
}

// publishedSeam answers the release lookup the stage asks before it writes
// (publishedRelease, D3269): ok true is this release being published, and a
// case that leaves the seam alone gets the production one's "lookup
// unavailable".
func publishedSeam(t *testing.T, ok bool) {
	t.Helper()
	was := publishedRelease
	publishedRelease = func(context.Context, string) (bool, error) { return ok, nil }
	t.Cleanup(func() { publishedRelease = was })
}

// seedUpdateStamp puts a stamp on the node, as a previous release's update or a hand
// edit would have left it.
func seedUpdateStamp(t *testing.T, db *gcpfake.RTDB, min string) {
	t.Helper()
	db.Set(versionstamp.Path, map[string]any{
		"schema": versionstamp.SchemaVersion, "min": min, "set_at": "2026-11-01T00:00:00Z",
	})
}

// stampOn reads the node back as the stamp package does, so a test asserts the
// value a reader of any release would see rather than the fake's own tree.
func stampOn(t *testing.T, e *initEngine) (versionstamp.Stamp, bool) {
	t.Helper()
	db := rigBudgetClient(t, e)
	var raw json.RawMessage
	found, err := db.Get(context.Background(), versionstamp.Path, &raw)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		return versionstamp.Stamp{}, false
	}
	stamp, present, err := versionstamp.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return stamp, present
}

// stampNodeWrites is how many writes the fake database was sent for the node:
// the one conditional PUT a raise is, and nothing at all for a run that left
// the node where it was.
func stampNodeWrites(db *gcpfake.RTDB) int {
	n := 0
	for _, rq := range db.Requests() {
		if rq.Method != "GET" && strings.TrimSuffix(rq.Path, ".json") == "/"+versionstamp.Path {
			n++
		}
	}
	return n
}

// credentialsImageAt is a credentials service image reference naming a release.
// The pin's own pattern reads the release out of it (:X.Y.Z@sha256:), so this
// is the value servicesImageStale compares this binary's version against.
func credentialsImageAt(ver string) string {
	return "us-east5-docker.pkg.dev/proj-1234/fugaro/credentials:" + ver + "@sha256:" + strings.Repeat("a", 64)
}

// seedInstallationTfvars writes the installation root's tfvars where this
// program reads them (infra.InstallationWorkdir, keyed by the GCP project
// rather than the installation's name), carrying a credentials service on
// image: the document credentialsMoveHeld and servicesImageStale are both asked
// about.
func seedInstallationTfvars(t *testing.T, e *initEngine, image string) {
	t.Helper()
	dir, err := infra.InstallationWorkdir(os.Getenv, e.lc.GCPProject)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(map[string]any{
		"credentials_service": map[string]any{"image": image},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := infra.WorkdirVarsPath(dir, "installation")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
}

// stampWorkdir prepares the installation root's workdir, which is the state
// the credentials-service stage leaves behind once it has had its turn: the
// tree is rebuilt, and the release behind's tfvars are the snapshot
// PrepareWorkdir took before it (Workdir.PrevVars, which
// recordedCredentialsImage reads). A run that has not reached that stage — the
// preview's pass, where every stage's check runs before any has run — has no
// workdir at all, and that is the state this distinguishes.
func stampWorkdir(t *testing.T, e *initEngine) {
	t.Helper()
	dir, err := infra.InstallationWorkdir(os.Getenv, e.lc.GCPProject)
	if err != nil {
		t.Fatal(err)
	}
	wd, err := infra.PrepareWorkdir(dir, "installation")
	if err != nil {
		t.Fatal(err)
	}
	e.wd = wd
}

// stampEngine is the engine one of these tests drives the stage with: the rig's
// own config and `update`'s door unless the case says otherwise.
func stampEngine(t *testing.T, r *initRig, o *initOptions) *initEngine {
	t.Helper()
	if o.door == "" {
		o.door = updateDoor.name
	}
	return rigEngine(t, r, o)
}

// checkStamp is one Check of the stage on its own, which is what the states
// that leave the node alone are asserted on: the loop's mode table and its
// confirmations are the write's business, and a hold is the stage's answer
// before anything else happens.
func checkStamp(t *testing.T, e *initEngine) (initflow.Status, error) {
	t.Helper()
	clearAgentMarkers(t)
	return newVersionStampStage(e).Check(t.Context())
}

// runStampStage converges the one stage, which is Check, Plan, Apply and Verify
// in the loop's own order.
func runStampStage(t *testing.T, r *initRig, o *initOptions) (*initflow.Result, string, error) {
	t.Helper()
	e := stampEngine(t, r, o)
	out := atTerminal(t, e, "")
	res, err := initflow.Run(t.Context(), []initflow.Stage{newVersionStampStage(e)}, e.options())
	return res, out.String(), err
}

// TestUpdateRaisesTheStampToItsRelease is the stage's whole point: a run of a
// published release raises the node to it, with the two other members the node
// carries, in one conditional write, and prints the one line naming where the
// fence moved.
func TestUpdateRaisesTheStampToItsRelease(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	res, out, err := runStampStage(t, r, &initOptions{door: updateDoor.name, yes: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	st := res.Stages[0]
	if st.State != initflow.Changed {
		t.Fatalf("stage = %+v, want the raise as the stage's own outcome:\n%s", st, out)
	}
	if want := "required CLI: none → 0.2.0 (members below it are refused until they upgrade)"; !strings.Contains(st.Detail, want) {
		t.Errorf("the line is %q, want %q in it:\n%s", st.Detail, want, out)
	}
	e := stampEngine(t, r, &initOptions{door: updateDoor.name})
	stamp, present := stampOn(t, e)
	if !present {
		t.Fatalf("the run wrote no stamp into %s:\n%s", versionstamp.Path, out)
	}
	if stamp.Min != "0.2.0" || stamp.Schema != versionstamp.SchemaVersion || stamp.SetAt.IsZero() {
		t.Errorf("the node holds %+v, want this release in a schema-%d stamp with a set_at", stamp, versionstamp.SchemaVersion)
	}
	if got := stampNodeWrites(db); got != 1 {
		t.Errorf("the node was written %d times, want the one conditional PUT a raise is", got)
	}
}

// TestOlderUpdateNeverLowersTheStamp: a node naming a release above this
// binary's is a fence a newer update raised, and an older binary may not take
// it back down. D3262 is that the node only ever moves up.
func TestOlderUpdateNeverLowersTheStamp(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	seedUpdateStamp(t, db, "9.9.9")
	res, out, err := runStampStage(t, r, &initOptions{door: updateDoor.name, yes: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if st := res.Stages[0]; st.State != initflow.Done {
		t.Errorf("stage = %+v, want done: the node is above this release", st)
	}
	e := stampEngine(t, r, &initOptions{door: updateDoor.name})
	stamp, present := stampOn(t, e)
	if !present || stamp.Min != "9.9.9" {
		t.Errorf("the node holds %+v, want the 9.9.9 another release wrote: an older update lowered the stamp", stamp)
	}
	if got := stampNodeWrites(db); got != 0 {
		t.Errorf("the node was written %d times, want none: nothing may lower it", got)
	}
}

// TestStampLineSaysCurrent: a node already at this release's version is the
// state every run after the first one leaves behind, and it is one word about
// the node rather than a diff of a move that is not there.
func TestStampLineSaysCurrent(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	seedUpdateStamp(t, db, "0.2.0")
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	st, err := checkStamp(t, e)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done || st.Detail != stampCurrentLine {
		t.Errorf("Check = %+v, want done with %q", st, stampCurrentLine)
	}
	res, out, err := runStampStage(t, r, &initOptions{door: updateDoor.name, yes: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := res.Stages[0].Detail; got != stampCurrentLine {
		t.Errorf("the run's line is %q, want %q", got, stampCurrentLine)
	}
	if got := stampNodeWrites(db); got != 0 {
		t.Errorf("the node was written %d times, want none: it is already this release's", got)
	}
}

// TestLostRaceWritesNothingAndSaysSo: the write is one conditional PUT and
// never a retry. Another writer landing between the read and the write is a
// node this run does not hold, and the run says so instead of suppressing a
// change nobody read.
func TestLostRaceWritesNothingAndSaysSo(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	s := newVersionStampStage(e)

	// Apply makes two requests to the node: the read that takes the ETag and
	// the PUT conditional on it. Holding both puts another writer exactly
	// between them.
	stampedRead, releaseRead := db.HoldNext(versionstamp.Path)
	stampedWrite, releaseWrite := db.HoldNext(versionstamp.Path)
	var outcome initflow.Outcome
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		outcome, err = s.Apply(context.Background(), initflow.Env{Yes: true})
	}()
	<-stampedRead
	releaseRead()
	<-stampedWrite
	seedUpdateStamp(t, db, "9.9.9")
	releaseWrite()
	<-done

	if err != nil {
		t.Fatalf("a lost race failed the run: %v", err)
	}
	for _, want := range []string{"changed while this update was running", "nothing was written"} {
		if !strings.Contains(outcome.Detail, want) {
			t.Errorf("the line does not say %q: %q", want, outcome.Detail)
		}
	}
	stamp, present := stampOn(t, e)
	if !present || stamp.Min != "9.9.9" {
		t.Errorf("the node holds %+v, want the other writer's 9.9.9: the lost race wrote something", stamp)
	}
	if s.p != nil && s.p.wroteIt {
		t.Error("the stage recorded a write that did not land")
	}
}

// TestStampStageRefusesAMalformedStamp: a node this binary cannot read is the
// one read failure the fence fails closed on, and a writer must never overwrite
// a node somebody meant (versionstamp.MalformedError).
func TestStampStageRefusesAMalformedStamp(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	db.Set(versionstamp.Path, map[string]any{"schema": 1, "min": "not-a-version", "set_at": "2026-11-01T00:00:00Z"})
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	st, err := checkStamp(t, e)
	if err == nil {
		t.Fatalf("a malformed stamp was accepted: %+v", st)
	}
	var malformed *versionstamp.MalformedError
	if !errors.As(err, &malformed) {
		t.Fatalf("the refusal is not the stamp package's own: %v", err)
	}
	if !strings.Contains(err.Error(), versionstamp.Path) {
		t.Errorf("the refusal does not name the node: %v", err)
	}
	if got := stampNodeWrites(db); got != 0 {
		t.Errorf("a malformed node was written %d times: it must never be overwritten", got)
	}
}

// TestFailedPolicyStageLeavesTheStampUnchanged: the stamp stage sits behind the
// policy one in the run's order, and a run that stops at the policy stage never
// reaches it — an installation whose published node could not be moved is not
// one whose fence may move over it.
func TestFailedPolicyStageLeavesTheStampUnchanged(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	// A policy node the migration refuses: its document is not an object, so
	// the policy stage fails the run and the stamp stage behind it never runs.
	db.Set(projectpolicy.PathShared(), "not a policy node")
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	atTerminal(t, e, "")
	res, err := initflow.Run(t.Context(), []initflow.Stage{newVersionStampStage(e), newPolicyMigrateStage(e)}, e.options())
	if err != nil {
		t.Fatalf("a refused policy node took the whole run down: %v", err)
	}
	if res.Failed == nil || res.Failed.Stage != initflow.Policy {
		t.Fatalf("the run did not stop at the policy stage: %+v", res.Failed)
	}
	if _, present := stampOn(t, e); present {
		t.Error("the stamp was raised by a run whose policy stage failed")
	}
	if got := stampNodeWrites(db); got != 0 {
		t.Errorf("the node was written %d times by a run that failed at the policy stage", got)
	}
}

// TestDevBuildLeavesTheStampAlone: a development or pre-release binary names no
// release, so it has nothing to require of the project and says so without
// reading the node at all.
func TestDevBuildLeavesTheStampAlone(t *testing.T) {
	r, db := stampRig(t)
	useVersion(t, "dev")
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	st, err := checkStamp(t, e)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Skipped || st.Undone {
		t.Errorf("Check = %+v, want skipped and not owed: a dev build never owes a stamp", st)
	}
	if !strings.Contains(st.Detail, "development build") {
		t.Errorf("the skip does not say why: %q", st.Detail)
	}
	if _, present := stampOn(t, e); present {
		t.Error("a development build wrote a stamp")
	}
	if got := stampNodeWrites(db); got != 0 {
		t.Errorf("a development build wrote the node %d times", got)
	}
}

// TestPlanOnlyWritesNoStamp: --plan-only is the review screen, so the line goes
// up and nothing is written — the loop asks Plan and never Apply.
func TestPlanOnlyWritesNoStamp(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	res, out, err := runStampStage(t, r, &initOptions{door: updateDoor.name, planOnly: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !res.PlanOnly {
		t.Fatal("the run was not a plan-only run")
	}
	if !strings.Contains(out, "required CLI: none → 0.2.0") {
		t.Errorf("the plan view does not carry the line the write would print:\n%s", out)
	}
	e := stampEngine(t, r, &initOptions{door: updateDoor.name})
	if _, present := stampOn(t, e); present {
		t.Error("--plan-only wrote a stamp")
	}
	if got := stampNodeWrites(db); got != 0 {
		t.Errorf("--plan-only wrote the node %d times", got)
	}
}

// TestStampHoldsWhilePolicyIsHeld: a coding agent's session deploys no
// credentials service, so the node that service reads may not move in it either
// (credentialsMoveHeld, D3267/F20). The stamp holds with the sentence the
// policy stage holds the policy node with, and names the door that clears it.
func TestStampHoldsWhilePolicyIsHeld(t *testing.T) {
	r, _ := stampRig(t)
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	seedInstallationTfvars(t, e, credentialsImageAt("0.2.0"))
	t.Setenv(agentenv.AgentMarkers[0], "1")
	st, err := newVersionStampStage(e).Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Skipped || !st.Undone {
		t.Fatalf("Check = %+v, want skipped and owed: the stamp is a step the next update has to finish", st)
	}
	for _, want := range []string{credentialsSessionHeld, "holds it there", selfCommand() + " update"} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("the held line does not say %q: %q", want, st.Detail)
		}
	}
}

// TestStampHoldsWhileCredentialsServiceIsBehind: even in the owner's own run, a
// recorded credentials service image that is not this release's is a process
// that stays where it is — the servicesImageStale predicate the policy stage's
// own raise precondition uses (credentialsResolveBeforeRaise). The workdir is
// the credentials-service stage's own turn (stampWorkdir): this is the state
// the run reaches this slot in after a move that did not happen, not the one
// its review screen is drawn in.
func TestStampHoldsWhileCredentialsServiceIsBehind(t *testing.T) {
	r, _ := stampRig(t)
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	seedInstallationTfvars(t, e, credentialsImageAt("0.1.0"))
	stampWorkdir(t, e)
	clearAgentMarkers(t)
	st, err := newVersionStampStage(e).Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Skipped || !st.Undone {
		t.Fatalf("Check = %+v, want skipped and owed", st)
	}
	if !strings.Contains(st.Detail, "credentials service image is not this release's") {
		t.Errorf("the held line does not name the fact it waits for: %q", st.Detail)
	}
}

// TestReviewScreenDoesNotHoldOnTheReleaseBehindSImage: the preview's pass runs
// every stage's check before any stage has run, so the tfvars on disk still
// carry the release behind's credentials service — the very process this run
// replaces. Reporting the stamp as held there would leave the owner a step the
// same run then does itself: the review screen and the run must agree, and the
// run has not yet given the credentials-service stage its turn.
func TestReviewScreenDoesNotHoldOnTheReleaseBehindSImage(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	e := stampEngine(t, r, &initOptions{door: updateDoor.name})
	seedInstallationTfvars(t, e, credentialsImageAt("0.1.0"))
	st, err := checkStamp(t, e)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("Check = %+v, want todo: the image on disk is the one this run replaces", st)
	}
	if strings.Contains(st.Detail, "holds it there") {
		t.Errorf("the review screen holds the stamp for a service the run replaces: %q", st.Detail)
	}
	res, out, err := runStampStage(t, r, &initOptions{door: updateDoor.name, planOnly: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "required CLI: none → 0.2.0") {
		t.Errorf("the plan view says %q, want the raise the run would make", d)
	}
	if _, present := stampOn(t, e); present {
		t.Error("the review screen wrote a stamp")
	}
	if got := stampNodeWrites(db); got != 0 {
		t.Errorf("the review screen wrote the node %d times", got)
	}
}

// TestStampStageSkipsAnUnadoptedDatabase: the ownership predicate every writer
// into the budget database asks (budgetDBUnadopted, F22). The node lives in
// that database, so a database another installation marked is one this run must
// not stamp — and the skip is that function's own sentence, naming the mark it
// found and the command that adopts a database.
func TestStampStageSkipsAnUnadoptedDatabase(t *testing.T) {
	r, db := stampRig(t)
	db.Set(budget.PathMark, map[string]any{
		"managed_by": "fugaro", "project": "workspace-a", "gcp_project": "proj-9999", "version": 1,
	})
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	st, err := checkStamp(t, e)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Skipped || !st.Undone {
		t.Fatalf("Check = %+v, want skipped and owed: the stamp is still a step this installation owes", st)
	}
	for _, want := range []string{"not project aurora", "this stage writes no required CLI there"} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("the skip does not carry %q: %q", want, st.Detail)
		}
	}
	if _, present := stampOn(t, e); present {
		t.Error("a database that is not this installation's was stamped")
	}
}

// TestStampStageFailsOpenWhenTheReadFails: a node the run cannot read is not a
// failure of the one command that brings the installation forward (spec, "Read
// failures fail open"; D3264). The door's own guard makes the same read and
// warns on stderr; the stage says the same thing in its own line, leaves the
// node exactly where it is, and reports the raise as still owed, so the next
// update takes it. A malformed node is the one read that refuses, and the test
// beside this one pins that.
func TestStampStageFailsOpenWhenTheReadFails(t *testing.T) {
	r, db := stampRig(t)
	publishedSeam(t, true)
	db.RefusePath("required_cli", 503, "UNAVAILABLE", "", "down")
	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	st, err := checkStamp(t, e)
	if err != nil {
		t.Fatalf("a node that could not be read failed the stage: %v", err)
	}
	if st.State != initflow.Skipped || !st.Undone {
		t.Fatalf("Check = %+v, want skipped and owed: the stamp is still a step this installation owes", st)
	}
	if !strings.Contains(st.Detail, "could not be read") {
		t.Errorf("the skip does not say the node could not be read: %q", st.Detail)
	}
	if db.Value(versionstamp.Path) != nil {
		t.Error("a node this run could not read was written")
	}
}
