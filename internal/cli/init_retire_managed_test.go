package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/initflow"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/task"
)

// init_retire_managed_test.go: plain `fugaro update` on a MANAGED project
// retires the repository's own derived addresses by itself — no prompt, no
// typed confirmation, no `--retire` (issue #1794, the owner's ruling of
// 2026-11-06, and CLAUDE.md's HARD RULE that `fugaro update` is the whole
// upgrade). The retirement's own flow is pinned by init_retire_test.go; what
// these tests are for is the one difference a managed project makes — who
// confirms — and, in the same breath, everything it does NOT change: an
// unmanaged project, an agent's session, and any address that holds data.
//
// Every one of them runs the command the user runs, `fugaro update` in the
// checkout, which is the converge. That is not a shortcut: the retirement is
// reached by the Repository stage's repoEngine call, and the managed gate is
// read by the converge alone — `update --repo` is the repo engine's own door
// and reads no mark at all, so a retirement there can never be the managed
// project's own. The rig is built for the other door, so the two things the
// converge needs and the repo engine does not have are added below: the
// repository's secrets stored (its secrets step runs before the repository
// one) and the Cloud Build seam faked, because the billable first image build
// a run reaches after the retirement is one this project's owner is no longer
// asked to type (NP3): the build is submitted and the run finishes, which is
// what these tests read at the end of it.

// newManagedRetireRig is newRetireRig on a managed project, run by its owner:
// the two marks (the runs bucket's object and the project's label), a fake
// terminal with no agent marker (newRetireRig clears them), the repository's
// secrets stored, and the credentials an audit record is written with
// isolated.
//
// The Cloud Build seam is faked too, and that is a consequence of NP3 rather
// than rig plumbing: the run's first image builds are billable and no longer
// held for a person at a terminal in a managed project (buildAsksNothing), so
// a run that gets past the repository stage submits one. Without the fake the
// run would reach the real Cloud Build endpoint and the test would fail on the
// network rather than on what it is about.
func newManagedRetireRig(t *testing.T, o retireOpts) *retireRig {
	t.Helper()
	r := newRetireRig(t, o)
	useFakeBuilder(t)
	// The repository root's plan is read more than once in a run that builds,
	// and the rig scripts the sequence (faketerraform consumes a list in order,
	// its last entry repeating). The retirement reads its own plan file twice
	// and they are one document: the summary and the delete list come from
	// terraform's `show` of it, the gate's admission and the record's digest
	// from `show -json` of the same file. Both are the bytes retireOpts built.
	// The read after them is the re-plan a build forces (init.go's step 6),
	// which exists to deploy the image just built: by then the retirement has
	// applied, so it holds the built image's job update and none of the
	// deletes — the plan an ordinary step is judged by, and the one a managed
	// gate admits.
	retirement := r.script["show@repo"].(map[string]any)
	postRetirement := map[string]any{"stdout": planJSON(t, retireJobUpdate())}
	r.script["show@repo"] = []map[string]any{retirement, retirement, postRetirement}
	r.save(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	storedRepoSecrets(t, r)
	return r
}

// storedRepoSecrets gives the rig's repository the values its jobs mount,
// carrying this installation's own marks: without them a plain `fugaro
// update` stops at its secrets step, before the repository stage these tests
// are about. The marks are the same on an unmanaged project — the gate's read
// is what makes a run a managed one, not a container's labels.
func storedRepoSecrets(t *testing.T, r *retireRig) {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bitbucket-token", "claude-oauth-token"} {
		r.sm.Seed(gcp.SecretID(slug, name), map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: r.label, gcp.LabelSecret: name,
		}, []byte("v"))
	}
}

// retirementRecords are the audit records of the retirement's own document.
// The converge judges other documents too (the installation root's plan is
// one), and what these tests are about is the admission of this one, so the
// records are told apart by the rules the retirement ran: they are the only
// ones that say "derived", which is the word the ruling turns on and the cover
// of the embedded roots never uses for a plan that deletes.
func retirementRecords(t *testing.T, r *retireRig) []managedmode.AuditRecord {
	t.Helper()
	var out []managedmode.AuditRecord
	for _, rec := range audits(t, r.initRig) {
		if strings.Contains(strings.Join(rec.Rules, " "), "derived") {
			out = append(out, rec)
		}
	}
	return out
}

// firstRepoPlan is the repository root's plan as the run FIRST read it: the
// retirement's own, which is the document the gate admitted and the digest the
// record carries. A run that builds reads the root's plan a second time
// (newManagedRetireRig scripts the two, in order), so the read a digest is of
// is named rather than assumed.
func firstRepoPlan(t *testing.T, r *retireRig) string {
	t.Helper()
	plans, ok := r.script["show@repo"].([]map[string]any)
	if !ok || len(plans) == 0 {
		t.Fatalf("the repository root's plan is scripted as %T, want the retirement's then the re-plan's", r.script["show@repo"])
	}
	return plans[0]["stdout"].(string)
}

// repoSteps are the REPOSITORY root's own terraform steps, in order, "state
// rm" as one. The converge runs the installation's terraform in the same fake
// and in the same run, so an order read off the whole log would compare two
// roots' steps; the fake logs the working directory, and the root's workdir is
// the one named after it (infra.Workdir.Root).
func (r *retireRig) repoSteps(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<24)
	for s.Scan() {
		var c struct {
			Args []string `json:"args"`
			Dir  string   `json:"dir"`
		}
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		if filepath.Base(c.Dir) != "repo" || len(c.Args) == 0 {
			continue
		}
		name := c.Args[0]
		if name == "state" && len(c.Args) > 1 {
			name += " " + c.Args[1]
		}
		out = append(out, name)
	}
	return out
}

// after is the first step of sub strictly after from, or -1. The repository
// root plans for other reasons before the retirement does (the run's own
// ordinary plan), so the retirement's plan is the one after its state removal.
func after(steps []string, from int, sub string) int {
	if from < 0 {
		return -1
	}
	for i := from + 1; i < len(steps); i++ {
		if steps[i] == sub {
			return i
		}
	}
	return -1
}

// retirementOrder is the repository root's own state removal, then the plan it
// makes possible, then the apply, and it is the whole reason the state removal
// exists: the plan in between is the one no apply could make before it.
func retirementOrder(t *testing.T, r *retireRig, text string) (int, int, int) {
	t.Helper()
	steps := r.repoSteps(t)
	rm := slices.Index(steps, "state rm")
	plan, apply := after(steps, rm, "plan"), -1
	if plan != -1 {
		apply = after(steps, plan, "apply")
	}
	if rm == -1 || plan == -1 || apply == -1 {
		t.Fatalf("the repository root's steps %v have no state rm -> plan -> apply\n%s", steps, text)
	}
	return rm, plan, apply
}

// noRetirementRan is the whole of "nothing happened": the retirement's own
// state removal, its apply and the API delete, and the three lines that are
// printed only when it runs. No other step of the converge makes any of them
// (the state removal is the retirement's alone, and so is a job delete), so a
// retirement that must not run is pinned by their absence.
func noRetirementRan(t *testing.T, r *retireRig, text string) {
	t.Helper()
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("state rm ran: %v", calls)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("deletes attempted: %v", deletes)
	}
	for _, want := range []string{
		"nothing was destroyed",
		"the plan above is the retirement's",
		"deleted the orphaned Cloud Run job",
	} {
		if strings.Contains(text, want) {
			t.Errorf("the retirement ran (%q):\n%s", want, text)
		}
	}
}

// The managed project's whole retirement, and both halves of the ruling in one
// run: the derived addresses go away with nothing asked and nothing typed —
// the state removal takes the protected job out, the plan that could not run
// before runs, it applies, and the orphaned job is deleted through the Cloud
// Run API — and the step is the gate's, so it is on the record.
func TestUpdateManagedRetiresItsOwnDerivedResources(t *testing.T) {
	r := newManagedRetireRig(t, retireOpts{protection: true, keptAsNote: true, member: true})
	out, errOut, err := executeStdin(t, "", "update")
	text := out + errOut
	// The whole run, not only its retirement: an empty stdin types nothing, so
	// a run that ends nonzero here is a step that asked for something (NP3's
	// billable build is no longer one of them on a managed project).
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, text)
	}
	// Nothing was asked. The retirement's own first confirmation opens with
	// its prediction ("retire 3 addresses of proj-1234: remove 1 from
	// Terraform's state...") and the line that stands in for the gate's
	// verdict is printed instead.
	if strings.Contains(text, "addresses of "+initProject+": remove") {
		t.Errorf("the managed retirement asked its first typed confirmation:\n%s", text)
	}
	if !strings.Contains(text, initflow.GateProceeded()) {
		t.Errorf("the output does not say the verdict is the confirmation here:\n%s", text)
	}
	retirementOrder(t, r, text)
	// The plan, its own counts and its full delete list are still printed.
	for _, want := range []string{"the plan above is the retirement's", "delete " + retireSvcAddr, "delete " + retireMemberAddr} {
		if !strings.Contains(text, want) {
			t.Errorf("the output does not say %q:\n%s", want, text)
		}
	}
	// The state removal has one address: the protected job, which no apply
	// could delete (the kept container is a note in this rig, so it is out of
	// the state already).
	if call := strings.Join(flatten(r.ran(t, "state rm")), " "); !strings.Contains(call, retireJobAddr) {
		t.Errorf("the state removal did not take the protected job's address out: %s", call)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 1 || !strings.Contains(deletes[0], retireJobName) {
		t.Errorf("job deletes = %v, want the retired variant's job through the API", deletes)
	}
	if r.run.HasJob(retireJobName) {
		t.Errorf("the orphaned job is still in the cloud:\n%s", text)
	}
	// And it is on the record, once, and the record is of THIS document: the
	// digest is the sha256 of the plan the retirement's own confirmation would
	// have shown, which is the bytes the fake returns for that read.
	recs := retirementRecords(t, r)
	if len(recs) != 1 {
		t.Fatalf("%d retirement records, want one: %+v", len(recs), recs)
	}
	rec := recs[0]
	if rec.Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q: %+v", rec.Verdict, managedmode.AuditAdmitted, rec)
	}
	if rec.Command != "fugaro update" {
		t.Errorf("the record names %q, want the command that ran", rec.Command)
	}
	sum := sha256.Sum256([]byte(firstRepoPlan(t, r)))
	if want := hex.EncodeToString(sum[:]); rec.PlanDigest != want {
		t.Errorf("the record's digest is %q, want the retirement's own plan %q", rec.PlanDigest, want)
	}
	// The rules are the retirement's, not the ordinary stage's: a retirement
	// deletes by design, so the cover of the embedded roots is not one of them
	// (tf.NotCovered refuses every delete), and a record that claimed it would
	// be a record of a plan the gate never saw.
	rules := strings.Join(rec.Rules, " ")
	if !strings.Contains(rules, "derived") || strings.Contains(rules, "the cover of the embedded roots admitted") {
		t.Errorf("the record's rules are not the retirement's own: %q", rules)
	}
}

// The other half of the ruling: a retirement whose set holds anything but the
// derived addresses stops exactly where it stops today, names the address that
// stopped it and why, and keeps today's refusal beside it. The two cases are
// the two ways an address is refused by the same decision: a type the
// allowlist retained (a bucket, whose stop is that table's verdict and nothing
// else's) and an address that leaves Terraform's state without being deleted
// (a secret container, which the ruling names as data although no plan
// destroys one).
func TestUpdateManagedStopsAtADataHoldingAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    retireOpts
		addr string
		why  string
	}{
		// keptAsNote keeps the secret container out of the picture (it is live
		// with this repository's labels, so it is adopted rather than retired),
		// so the bucket is the ONLY address of the set that is not derived: the
		// stop below is this table's decision and nothing else's.
		{"a bucket", retireOpts{protection: true, keptAsNote: true, bucket: true}, retireBucketAddr, "a bucket holds objects, and an object is data"},
		{"a secret container", retireOpts{protection: true}, retireKeptAddr, "fugaro never deletes a secret or a secret version"},
		// A type the allowlist allows, of the installation's root rather than
		// the repository's: what stops it is the bound, not the type.
		{"another root's address", retireOpts{protection: true, keptAsNote: true, foreign: true}, retireForeignAddr, "it is not an address of module.repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedRetireRig(t, tc.o)
			out, errOut, err := executeStdin(t, "", "update")
			text := out + errOut
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d, err %v\n%s; want the stop a plain run has always had", ExitCode(err), err, text)
			}
			for _, want := range []string{
				// The sentence the stop opens with, pinned here: it is what
				// tells an owner this is the managed project's own rule and
				// not a failure.
				"a managed project's own update retires the derived resources it created and no others",
				tc.addr, tc.why,
				// Today's refusal, unchanged, for the protected job beside it.
				"no apply can delete", retireNextCommand,
			} {
				if !strings.Contains(text, want) {
					t.Errorf("the stop does not say %q:\n%s", want, text)
				}
			}
			noRetirementRan(t, r, text)
			if recs := retirementRecords(t, r); len(recs) != 0 {
				t.Errorf("a retirement that was not made wrote %d record(s): %+v", len(recs), recs)
			}
		})
	}
}

// The same set a managed project retires by itself, on an UNMANAGED project:
// nothing changed. The refusal is the one it has always been, nothing of the
// retirement runs, and no record of it is written — the gate is the managed
// project's. The run is given --yes because on an unmanaged project the steps
// before the repository one ask their own confirmations, and a question
// answered in the wrong place is not what this test is about: what it is about
// is that `--retire` is the only thing that retires here, exactly as before.
func TestUpdateUnmanagedStillRefusesTheSameRetirement(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, keptAsNote: true, member: true})
	storedRepoSecrets(t, r)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	text := out + errOut
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s; want the refusal an unmanaged project has always had", ExitCode(err), err, text)
	}
	for _, want := range []string{"no apply can delete", retireNextCommand} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "a managed project's own update retires") {
		t.Errorf("an unmanaged run read the managed project's own line:\n%s", text)
	}
	noRetirementRan(t, r, text)
	if recs := retirementRecords(t, r); len(recs) != 0 {
		t.Errorf("an unmanaged project wrote %d retirement record(s): %+v", len(recs), recs)
	}
}

// And a coding agent's session on a managed project: never a retirement of its
// own. The marker is the only difference — the marks, the terminal and the
// secrets are exactly what the managed test above has — and what the session
// reads is the refusal it has always read, with the retirement untouched.
func TestUpdateManagedAgentSessionIsNotRetired(t *testing.T) {
	r := newManagedRetireRig(t, retireOpts{protection: true, keptAsNote: true, member: true})
	t.Setenv("CLAUDECODE", "1")
	out, errOut, err := executeStdin(t, "", "update")
	text := out + errOut
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s; want the refusal a session has always read", ExitCode(err), err, text)
	}
	if !strings.Contains(text, "no apply can delete") || !strings.Contains(text, retireNextCommand) {
		t.Errorf("the session does not read the refusal that names --retire:\n%s", text)
	}
	noRetirementRan(t, r, text)
	if recs := retirementRecords(t, r); len(recs) != 0 {
		t.Errorf("a session the gate did not admit wrote %d retirement record(s): %+v", len(recs), recs)
	}
}

// The deletion-protected job, in the order the ruling names: the state
// removal, then the apply, then the delete through the Cloud Run API. Two
// facts pin it, because no one stream holds all three. The repository root's
// own terraform calls hold the first two (retirementOrder, which test 1 also
// reads), and the third is the API call the run makes only once the apply has
// succeeded: the retirement's apply is the one that fails here, and a delete
// that happened anyway — before the plan it belongs to, or after an apply that
// never landed — is exactly what this test would catch. The successful order,
// where the delete does happen, is test 1's.
func TestUpdateManagedRetiresTheProtectedJobInOrder(t *testing.T) {
	r := newManagedRetireRig(t, retireOpts{protection: true, keptAsNote: true, member: true})
	// The run's first apply is the installation root's; the list answers each
	// apply in turn, and its last step stands for every later one, so the
	// second apply — the retirement's — is the one that fails.
	r.script["apply"] = []map[string]any{{}, {"exit": 1, "stderr": "Error: the retirement's apply failed\n"}}
	r.save(t)
	out, errOut, err := executeStdin(t, "", "update")
	text := out + errOut
	if !strings.Contains(text, "the retirement's apply failed") {
		t.Fatalf("the retirement's apply did not run, or its failure was swallowed (exit %d, err %v):\n%s", ExitCode(err), err, text)
	}
	retirementOrder(t, r, text)
	if !strings.Contains(text, "nothing was destroyed") {
		t.Errorf("the state removal did not run before the apply:\n%s", text)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("a job was deleted although the apply that must come first failed: %v", deletes)
	}
	if !r.run.HasJob(retireJobName) {
		t.Errorf("the orphaned job is gone although the apply failed:\n%s", text)
	}
	if strings.Contains(text, "deleted the orphaned Cloud Run job") {
		t.Errorf("the API delete ran although the apply failed:\n%s", text)
	}
}

// flatten is the recorded terraform calls as lines, so a test asserts about
// the arguments of every one of them at once.
func flatten(calls [][]string) []string {
	var out []string
	for _, c := range calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// TestUpdateManagedRetirementDoesNotCarryALooseningIn: the plan the automatic
// retirement applies is judged by the cover an ordinary step is, less the
// retirement's own deletes, so an owner binding riding in the same plan asks
// the typed confirmation instead of going through under the retirement's name.
func TestUpdateManagedRetirementDoesNotCarryALooseningIn(t *testing.T) {
	r := newManagedRetireRig(t, retireOpts{protection: true, keptAsNote: true, member: true, loosen: true})
	out, errOut, err := executeStdin(t, "", "update")
	text := out + errOut
	if !strings.Contains(text, "outside what the review announced") {
		t.Fatalf("the uncovered create was not named (exit %d, err %v):\n%s", ExitCode(err), err, text)
	}
	if !strings.Contains(text, "Type aurora to confirm") {
		t.Errorf("the typed confirmation was not asked for a plan carrying an uncovered create:\n%s", text)
	}
	if recs := retirementRecords(t, r); len(recs) != 0 {
		t.Errorf("an uncovered plan was recorded as admitted: %+v", recs)
	}
}
