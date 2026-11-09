package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// managed_gate.go's tests: the ONE helper every command in the gate's set calls
// (M6, D1015, D1017, D1024, D1025).
//
// What these hold, and why each one is a test rather than a comment. The four
// things the helper does are the four things a command cannot be trusted to
// remember: read both marks per command, ask the ownership question first,
// re-read the marks immediately before the write, and record an admission and a
// refusal alike. Three of the four are properties of a *failure to act* — a
// command that cached the marks, or that skipped the record, does exactly what
// it would have done if the code were right — so nothing but a test that makes
// the world change underneath the helper can tell them apart. The revocation
// test below is that test: it unflags the project between the two reads, which
// is the window D1024 narrows.
//
// Every world here is hermetic: internal/gcpfake's GCS, Resource Manager and
// RTDB fakes, t.TempDir() for the one file a test writes, and the credentials
// read off a path in that directory so a developer's own Application Default
// Credentials can never decide what a record says. No test contacts a cloud,
// and none of them needs an account.

// managedGateTestNow is when every record in this file is written: a fixed day,
// so the listing a test reads back is asked about that day and not about
// whenever the test ran.
var managedGateTestNow = time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)

// managedGateUser is the person the credentials in force belong to in these
// tests, and the address a record is expected to name when the service
// account's own file is not what answered. The domain is the reserved
// example.invalid, so no real mailbox can be behind it.
const (
	managedGateUser     = "operator@example.invalid"
	managedGateDeployer = "fugaro-deployer@proj-1234.iam.gserviceaccount.com"
)

// managedGateRig is one managed installation: both marks present, the clients
// over the fakes, and the installation's own trusted configuration (the GCP
// project comes from here and never from a repository — the read's own rule).
type managedGateRig struct {
	gcs *gcpfake.GCS
	crm *gcpfake.CRM
	s   *storage.Service
	c   *crm.Service
	lc  *localcfg.Config
}

func newManagedGateRig(t *testing.T) *managedGateRig {
	t.Helper()
	dir := t.TempDir()
	// The credentials are isolated like the home directory is: a file that
	// happened to be on the machine would otherwise name a real address in a
	// record, and in this test's failure output.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "no-application-default-credentials.json"))
	t.Setenv("HOME", dir)
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "gcloud"))
	oldUser := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return managedGateUser, nil }
	t.Cleanup(func() { authenticatedUser = oldUser })
	oldNow := managedGateNow
	managedGateNow = func() time.Time { return managedGateTestNow }
	t.Cleanup(func() { managedGateNow = oldNow })

	g := gcpfake.NewGCS(t)
	g.AddBucket(managedPropertyBucket(), managedPropertyNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	g.Put(managedPropertyBucket(), managed.MarkerObject, managedGateMarker())
	c := gcpfake.NewCRM(t)
	c.AddProject(managedPropertyGCP, managedPropertyNumber)
	labelled(c, managedPropertyGCP)
	return &managedGateRig{
		gcs: g, crm: c,
		s:  managedStorage(t, g),
		c:  managedCRM(t, c),
		lc: &localcfg.Config{Name: managedPropertyProject, GCPProject: managedPropertyGCP, RunsBucket: managedPropertyBucket()},
	}
}

// managedGateMarker is the creation fact a managed installation's runs bucket
// holds: version 1, naming the GCP project the read is asked about. The
// versions that record a budget or a build bound are M5's, and this task's
// record only has to name whichever one was in force.
func managedGateMarker() []byte {
	return []byte(`{"version":1,"gcp_project":"` + managedPropertyGCP + `"}`)
}

func (r *managedGateRig) env() managedGateEnv {
	return managedGateEnv{lc: r.lc, s: r.s, crm: r.c}
}

func (r *managedGateRig) facts() managedGateFacts {
	return managedGateFacts{
		Subcommand:  "config publish",
		Argv:        []string{"config", "publish", "fugaro.yaml"},
		Declined:    "nothing was published",
		Identity:    "user:" + managedGateUser,
		AgentMarker: "CLAUDECODE",
	}
}

// agent is a coding agent's session: the conditions under which the managed
// case is the difference between proceeding and refusing (initflow.AgentRefusal
// is what a step of an unmanaged project answers).
func (r *managedGateRig) agent() initflow.Conditions {
	return initflow.Conditions{Agent: "CLAUDECODE"}
}

// auditKeys is every object of the audit family in the runs bucket, so a test
// can say "nothing was written" about an empty listing rather than about a
// record it did not think to look for.
func (r *managedGateRig) auditKeys(t *testing.T) []string {
	t.Helper()
	list, err := r.s.Objects.List(managedPropertyBucket()).Prefix(managedmode.AuditPrefix).Do()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(list.Items))
	for _, o := range list.Items {
		out = append(out, o.Name)
	}
	return out
}

// audits is the day's records, read back through the reader a surface uses:
// what a test asserts about is what doctor and report would print.
func (r *managedGateRig) audits(t *testing.T) []managedmode.AuditRecord {
	t.Helper()
	got, err := managedmode.ListAuditRecords(context.Background(), r.s, managedPropertyBucket(), managedmode.AuditDate(managedGateTestNow))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// markerGeneration is the creation fact object's own generation right now, read
// off the same answer the helper's read sees. It is the oracle for the record's
// marker_generation: a test that asked the helper's own field would agree with
// a helper that never re-read anything.
func (r *managedGateRig) markerGeneration(t *testing.T) int64 {
	t.Helper()
	resp, err := r.s.Objects.Get(managedPropertyBucket(), managed.MarkerObject).Download()
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	gen, err := strconv.ParseInt(resp.Header.Get("X-Goog-Generation"), 10, 64)
	if err != nil {
		t.Fatalf("the fake answered the marker without a generation: %v", err)
	}
	return gen
}

// gateDigest is the oracle for plan_digest: the sha256 of the bytes the gate
// was handed, computed here rather than by the code under test (the same
// oracle internal/managed's own audit tests use, one package up).
func gateDigest(doc []byte) string {
	sum := sha256.Sum256(doc)
	return hex.EncodeToString(sum[:])
}

// The admission: a converged plan in a managed project is confirmed under a
// coding agent's marker, and the record of it is written AFTER the command's
// write and says exactly which document was judged.
func TestManagedGateAdmitsAConvergedStepAndRecordsIt(t *testing.T) {
	doc := []byte(`{"version":1,"keys":{"rigor":"standard"}}`)
	const rule = "every key is in the registry"
	r := newManagedGateRig(t)
	gate, err := readManagedGate(context.Background(), r.env(), r.facts())
	if err != nil {
		t.Fatalf("reading the gate of a managed project: %v", err)
	}
	if !gate.managed {
		t.Fatal("both marks are in place and the gate did not read the project as managed")
	}
	m, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeAdmitted(doc, initflow.YesOrTyped, []string{rule}))
	if err != nil {
		t.Fatalf("a plan every rule passed was refused: %v", err)
	}
	if !m.Managed || !m.Admitted || m.Fallback {
		t.Errorf("the gate answered %+v, want a managed project whose plan was admitted", m)
	}
	if !initflow.CanConfirmManaged(initflow.YesOrTyped, r.agent(), m) {
		t.Error("an admitted step of a managed project is still refused under a coding agent's marker: the confirmation the gate exists to remove was not removed")
	}
	// The record is the command's OWN write's record: nothing is filed before
	// it, so an apply that never happened cannot look like one that did.
	if keys := r.auditKeys(t); len(keys) != 0 {
		t.Errorf("a record exists before the command wrote anything: %q", keys)
	}
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatalf("recording the admission: %v", err)
	}
	got := r.audits(t)
	if len(got) != 1 {
		t.Fatalf("the day holds %d records, want exactly the one admission", len(got))
	}
	rec := got[0]
	if rec.Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q", rec.Verdict, managedmode.AuditAdmitted)
	}
	if rec.PlanDigest != gateDigest(doc) {
		t.Errorf("the record's plan_digest is %q, want the sha256 of the bytes the gate read (%q)", rec.PlanDigest, gateDigest(doc))
	}
	if !slices.Contains(rec.Rules, rule) {
		t.Errorf("the record's rules are %q, and the rule that admitted is not among them", rec.Rules)
	}
	if !strings.HasSuffix(rec.Command, " config publish") {
		t.Errorf("the record's command is %q, want the command a person types", rec.Command)
	}
	if rec.Project != managedPropertyProject || rec.GCPProject != managedPropertyGCP {
		t.Errorf("the record names %q in %q, want %q in %q", rec.Project, rec.GCPProject, managedPropertyProject, managedPropertyGCP)
	}
	if rec.Identity != "user:"+managedGateUser {
		t.Errorf("the record's identity is %q, want the account the write ran as", rec.Identity)
	}
	if rec.AgentMarker != "CLAUDECODE" {
		t.Errorf("the record's agent_marker is %q, want the session's own claim, recorded verbatim", rec.AgentMarker)
	}
	if rec.MarkerVersion != 1 {
		t.Errorf("the record's marker_version is %d, want 1, the version of the fact that was in force", rec.MarkerVersion)
	}
	if want := strconv.FormatInt(r.markerGeneration(t), 10); rec.MarkerGeneration != want {
		t.Errorf("the record's marker_generation is %q, want %q, the generation of the fact the write ran under", rec.MarkerGeneration, want)
	}
	if rec.DeleteListGeneration != "" {
		t.Errorf("the record's delete_list_generation is %q, and this step judged no removal", rec.DeleteListGeneration)
	}
	if !rec.At.Equal(managedGateTestNow) {
		t.Errorf("the record's time is %s, want %s", rec.At, managedGateTestNow)
	}
	// One admission is one record: a command that asks a second time (a
	// deferred call beside an explicit one, say) must not file two objects
	// for one applied command.
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.audits(t)); n != 1 {
		t.Errorf("the day holds %d records after the admission was recorded twice, want 1", n)
	}
}

// The refusal: a plan a rule rejected is refused with the gate's own sentence,
// the record of it is written where the refusal happens, and the step stops.
func TestManagedGateRefusesAStepOutsideItsRulesAndRecordsTheRefusal(t *testing.T) {
	doc := []byte(`{"version":1,"keys":{}}`)
	const rule = "the layer names credentials.api_key, which is a reserved key"
	r := newManagedGateRig(t)
	gate, err := readManagedGate(context.Background(), r.env(), r.facts())
	if err != nil {
		t.Fatal(err)
	}
	m, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeRefused(doc, initflow.YesOrTyped, []string{rule}))
	if err == nil {
		t.Fatal("a plan a rule refused was admitted")
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Errorf("the refusal is %v, want the gate's own sentence (managed.ErrNotConverged)", err)
	}
	if !strings.Contains(err.Error(), rule) {
		t.Errorf("the refusal does not name the rule that fired, so the session cannot act on it: %v", err)
	}
	if !m.Managed || m.Admitted {
		t.Errorf("the gate answered %+v: the project is managed and THIS plan was not admitted", m)
	}
	if initflow.CanConfirmManaged(initflow.YesOrTyped, r.agent(), m) {
		t.Error("a refused plan still confirmed the step: the refusal an agent's session must read was removed")
	}
	got := r.audits(t)
	if len(got) != 1 {
		t.Fatalf("the refusal left %d records, want exactly one (D1025: a refusal is recorded too)", len(got))
	}
	if got[0].Verdict != managedmode.AuditRefused {
		t.Errorf("the record's verdict is %q, want %q", got[0].Verdict, managedmode.AuditRefused)
	}
	if !slices.Contains(got[0].Rules, rule) {
		t.Errorf("the record's rules are %q, and the rule that refused is not among them", got[0].Rules)
	}
	// The refused step wrote nothing, so there is no second record to write:
	// one verdict, one object.
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.audits(t)); n != 1 {
		t.Errorf("the day holds %d records after a refusal, want 1: a verdict is recorded once", n)
	}
}

// One admission at a time. A document a call admitted belongs to the step that
// call was made for, and a later call that admits another one — or admits
// nothing — ends it: a recorded() reached afterwards must not file an object
// carrying the digest of a plan the step never applied. The step that is judged
// twice is real (`fugaro update`'s propagation retry) and it is why the second
// call's verdict, not the first's, is the one the record carries.
func TestManagedGateRecordsOnlyWhatTheLastCallAdmitted(t *testing.T) {
	admitted := []byte(`{"version":1,"keys":{"rigor":"standard"}}`)
	refused := []byte(`{"version":1,"keys":{}}`)
	const rule = "the layer names credentials.api_key, which is a reserved key"
	r := newManagedGateRig(t)
	gate, err := readManagedGate(context.Background(), r.env(), r.facts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeAdmitted(admitted, initflow.YesOrTyped, []string{rule})); err != nil {
		t.Fatalf("a plan every rule passed was refused: %v", err)
	}
	if _, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeRefused(refused, initflow.YesOrTyped, []string{rule})); err == nil {
		t.Fatal("the plan a rule refused was admitted")
	}
	// The refusal is the day's one record, written where it happened. The
	// admission the call before it made is the record this must not add: the
	// step it belonged to is not the step this record is about.
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := r.audits(t)
	if len(got) != 1 {
		t.Fatalf("the day holds %d records, want the refusal's own and no other: %+v", len(got), got)
	}
	if got[0].Verdict != managedmode.AuditRefused || got[0].PlanDigest != gateDigest(refused) {
		t.Errorf("the record is %+v, want the refusal of the document the second call judged", got[0])
	}
	if got[0].PlanDigest == gateDigest(admitted) {
		t.Errorf("the record is about the document the first call admitted (%s), which is not the one this step was judged for", gateDigest(admitted))
	}
}

// A project that is not managed is not this design's: the helper answers the
// zero Managed — which is what every caller of D55's rule answered before the
// managed case existed — and writes nothing at all.
func TestManagedGateWritesNothingForAProjectThatIsNotManaged(t *testing.T) {
	r := newManagedGateRig(t)
	// One mark of the two, the way a half-removed project is: the object is in
	// the bucket and the project's own label says something else.
	r.crm.SetLabels(managedPropertyGCP, map[string]string{managed.ProjectLabelKey: "no"})
	gate, err := readManagedGate(context.Background(), r.env(), r.facts())
	if err != nil {
		t.Fatalf("a project that is not managed is an answer and not a failure: %v", err)
	}
	if gate.managed {
		t.Fatal("a project whose label says something else was read as managed")
	}
	m, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))
	if err != nil {
		t.Fatalf("the gate refused a plan in a project it is not the gate of: %v", err)
	}
	if m != (initflow.Managed{}) {
		t.Errorf("the gate answered %+v for a project that is not managed, want the zero value", m)
	}
	// The zero value is the property, and it is held against every condition
	// rather than the agent's alone: for each of them the answer has to be the
	// one CanConfirm gave before the managed case existed.
	for _, cond := range []initflow.Conditions{{}, {Yes: true}, {Terminal: true}, {NonInteractive: true}, {JSON: true}, {Agent: "CLAUDECODE"}, {Agent: "CURSOR_AGENT", Yes: true, Terminal: true}} {
		for _, class := range []initflow.ConfirmClass{initflow.Typed, initflow.YesOrTyped} {
			if got, want := initflow.CanConfirmManaged(class, cond, m), initflow.CanConfirm(class, cond); got != want {
				t.Errorf("%v under %+v answers %v with the gate and %v without it: a project that is not managed must keep today's answer exactly", class, cond, got, want)
			}
		}
	}
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatalf("recording nothing: %v", err)
	}
	if keys := r.auditKeys(t); len(keys) != 0 {
		t.Errorf("a project that is not managed wrote %q: D1025 records a managed project's steps and no other's", keys)
	}
}

// The gate can only narrow (D1026): a class it does not cover keeps the
// confirmation it has today, whatever the plan looked like — and nothing is
// recorded, because the gate judged no step.
func TestManagedGateDoesNotConfirmAClassItDoesNotCover(t *testing.T) {
	r := newManagedGateRig(t)
	gate, err := readManagedGate(context.Background(), r.env(), r.facts())
	if err != nil {
		t.Fatal(err)
	}
	m, err := gate.admit(context.Background(), initflow.Typed, managedmode.ConvergeAdmitted([]byte("{}"), initflow.Typed, nil))
	if err != nil {
		t.Fatalf("the gate refused a step of a class it does not judge: %v", err)
	}
	if !m.Managed {
		t.Error("the project is managed and the gate did not say so: the class rule and the project's fact are not the same answer")
	}
	if m.Removes(initflow.Typed) {
		t.Error("a Typed confirmation was removed by a plan verdict: D1026's rule is that the class wins over the gate")
	}
	if m.Admitted {
		t.Error("a document the gate never judges is reported as admitted")
	}
	if initflow.CanConfirmManaged(initflow.Typed, r.agent(), m) {
		t.Error("a coding agent's session confirmed a Typed step of a managed project")
	}
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if keys := r.auditKeys(t); len(keys) != 0 {
		t.Errorf("a step the gate does not cover left %q: the record is the gate's, and this gate judged nothing", keys)
	}
}

// The whole-project inventory's fallback (D59) is the fourth field of the
// answer, and it is the one a command cannot get from the plan: the document
// still converged — the fallback is not a verdict on it — and the step's
// confirmation is kept because the inventory could not clear the project.
func TestManagedGateCarriesTheInventoryFallback(t *testing.T) {
	r := newManagedGateRig(t)
	facts := r.facts()
	facts.InventoryFallback = true
	gate, err := readManagedGate(context.Background(), r.env(), facts)
	if err != nil {
		t.Fatal(err)
	}
	m, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))
	if err != nil {
		t.Fatalf("an inventory that fell back was reported as a refusal of the plan: it is not a verdict on the document: %v", err)
	}
	if !m.Managed || !m.Admitted {
		t.Errorf("the gate answered %+v, want a managed project whose plan it admitted", m)
	}
	if !m.Fallback {
		t.Fatal("the answer does not carry the inventory's fallback, so nothing downstream can keep the confirmation")
	}
	if m.Removes(initflow.YesOrTyped) {
		t.Error("a fallback removed the coding agent's refusal: D59 keeps every confirmation the inventory could not clear")
	}
	if initflow.CanConfirmManaged(initflow.YesOrTyped, r.agent(), m) {
		t.Error("a coding agent's session confirmed a step whose whole-project inventory fell back")
	}
	// A person at a terminal is not the case D59 is about, and the fallback is
	// not a second rule beside theirs: for them the answer is the one
	// CanConfirm gives with no gate at all.
	if got, want := initflow.CanConfirmManaged(initflow.YesOrTyped, initflow.Conditions{Terminal: true}, m), initflow.CanConfirm(initflow.YesOrTyped, initflow.Conditions{Terminal: true}); got != want {
		t.Errorf("a fallback changed a person's own confirmation (%v, want %v): it keeps the confirmation a coding agent's session cannot give and no other", got, want)
	}
	// And the record is not the fallback's to drop: the person's apply DID
	// happen, so the admission is still held here and the command's `recorded`
	// files it. A gate that suppressed it would lose the trail of a managed
	// project's applied step (D1025).
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatalf("recording the admission of a step whose inventory fell back: %v", err)
	}
	got := r.audits(t)
	if len(got) != 1 {
		t.Fatalf("the day holds %d records, want the one admission of the apply that happened", len(got))
	}
	if got[0].Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q", got[0].Verdict, managedmode.AuditAdmitted)
	}
}

// The mark is read twice, and the second read is what decides (D1024): an owner
// who unflags the project between the read and the write takes the admission
// away, and the step keeps the confirmation a project that is not managed has.
func TestManagedGateReReadsTheMarksBeforeTheWrite(t *testing.T) {
	r := newManagedGateRig(t)
	gate, err := readManagedGate(context.Background(), r.env(), r.facts())
	if err != nil {
		t.Fatal(err)
	}
	if !gate.managed {
		t.Fatal("the gate did not read a fully marked project as managed")
	}
	// The owner unflags it, in the window between the plan being read and the
	// write: exactly the revocation D1024 narrows and does not close.
	r.crm.SetLabels(managedPropertyGCP, map[string]string{managed.ProjectLabelKey: "no"})
	m, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))
	if err != nil {
		t.Fatalf("the revocation was reported as a failure: %v", err)
	}
	if m != (initflow.Managed{}) {
		t.Errorf("the gate answered %+v after the project was unflagged: the mark read once and cached is the window D1024 exists to narrow", m)
	}
	if initflow.CanConfirmManaged(initflow.YesOrTyped, r.agent(), m) {
		t.Error("a coding agent's session was admitted to a step after the owner revoked the mark")
	}
	// The gate's own answer is the second read's and not the first's: the read
	// that found the marks gone is the last word on whether the project is
	// managed, so a later call on this gate finds the un-managed project the
	// second read found and not the managed one the first read saw.
	if gate.managed {
		t.Error("the gate still reports the project as managed after the read that found the mark gone: the stale answer is the one a later call would act on")
	}
	if keys := r.auditKeys(t); len(keys) != 0 {
		t.Errorf("a revoked project left %q: nothing was judged under a mark that is no longer there", keys)
	}
}

// The other half of the re-read: the record names the fact that was in force at
// the WRITE, so an owner who replaced the marker between the two reads still
// sees which one the apply ran under.
func TestManagedGateRecordsTheFactInForceAtTheWrite(t *testing.T) {
	r := newManagedGateRig(t)
	gate, err := readManagedGate(context.Background(), r.env(), r.facts())
	if err != nil {
		t.Fatal(err)
	}
	first := gate.marks.Generation
	if first == 0 {
		t.Fatal("the first read placed no generation, so this test cannot tell the two reads apart")
	}
	r.gcs.Put(managedPropertyBucket(), managed.MarkerObject, managedGateMarker())
	now := r.markerGeneration(t)
	if now == first {
		t.Fatalf("the fake answered generation %d twice, so this test cannot tell the two reads apart", now)
	}
	if _, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil)); err != nil {
		t.Fatal(err)
	}
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := r.audits(t)
	if len(got) != 1 {
		t.Fatalf("the day holds %d records, want one", len(got))
	}
	if want := strconv.FormatInt(now, 10); got[0].MarkerGeneration != want {
		t.Errorf("the record names marker_generation %q, and the fact in force at the write is %q: a record must name the fact the write ran under (D1024)", got[0].MarkerGeneration, want)
	}
}

// A record that could not be written fails the command (D1014), and it fails it
// with the exit code the cause deserves: a missing grant on the operator's own
// account is theirs to fix, an outage is not. The fake refuses every write under
// fugaro/, as a bucket whose grant is conditioned elsewhere answers.
func TestManagedGateFailsTheStepWhenTheRecordCannotBeWritten(t *testing.T) {
	t.Run("an admission", func(t *testing.T) {
		r := newManagedGateRig(t)
		gate, err := readManagedGate(context.Background(), r.env(), r.facts())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil)); err != nil {
			t.Fatal(err)
		}
		r.gcs.DenyWrites(managedPropertyBucket(), managedmode.AuditPrefix)
		err = gate.recorded(context.Background())
		if err == nil {
			t.Fatal("an apply whose record could not be written was reported as done")
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("the failure exits %d, want %d: a grant the operator adds is not an outage: %v", ExitCode(err), ExitUserError, err)
		}
		if !strings.Contains(err.Error(), managedmode.AuditPrefix) {
			t.Errorf("the failure does not name where the record goes, so nobody can grant it: %v", err)
		}
		if keys := r.auditKeys(t); len(keys) != 0 {
			t.Errorf("the family holds %q after a refused write, want nothing", keys)
		}
	})
	t.Run("a refusal", func(t *testing.T) {
		r := newManagedGateRig(t)
		gate, err := readManagedGate(context.Background(), r.env(), r.facts())
		if err != nil {
			t.Fatal(err)
		}
		r.gcs.DenyWrites(managedPropertyBucket(), managedmode.AuditPrefix)
		const rule = "the plan removes a bucket"
		_, err = gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeRefused([]byte("{}"), initflow.YesOrTyped, []string{rule}))
		if err == nil {
			t.Fatal("a refused step whose refusal could not be recorded was reported as refused")
		}
		// Both sentences: the session reads why nothing was applied, and the
		// operator's own failure is not swallowed to make the message shorter.
		if !strings.Contains(err.Error(), managedmode.NotConverged) {
			t.Errorf("the failure does not carry the gate's refusal: %v", err)
		}
		// And the gate's refusal is still in the CHAIN and not only in the
		// text: a caller that has to tell a plan the gate refused from any
		// other failure asks errors.Is, and a sentence it can only read in
		// English is not an answer it can act on.
		if !errors.Is(err, managedmode.ErrNotConverged) {
			t.Errorf("the failure's chain does not reach the gate's own refusal (managed.ErrNotConverged), so nothing can tell this refusal from any other failure: %v", err)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("the failure exits %d, want %d (the write's own cause): %v", ExitCode(err), ExitUserError, err)
		}
	})
}

// The ownership question is asked FIRST, before any plan is judged (D1017), and
// it is the same question every other writer asks. A managed project whose
// config names a database that is not the installation's is refused the way it
// always was, and the gate judges nothing: no plan is read against a database
// the command is about to refuse, and no record is written for a plan nobody
// read.
func TestManagedGateAsksTheOwnershipQuestionBeforeThePlanIsJudged(t *testing.T) {
	for _, tc := range []struct {
		what string
		// seed is what the database holds, and firebaseProject what the config
		// names: an empty database under a separate Firebase project, and the
		// installation's own project's database carrying another
		// installation's mark.
		seed            map[string]any
		firebaseProject string
		want            string
	}{
		{"an empty database under a Firebase project of its own",
			nil, "", "shared-fp-1"},
		{"the installation's own project's database carrying another installation's mark",
			birchMark(), managedPropertyGCP, "birch"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedGateRig(t)
			f := gcpfake.NewRTDB(t)
			if tc.seed != nil {
				f.Set(budget.PathMark, tc.seed)
			}
			client, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "" }})
			if err != nil {
				t.Fatal(err)
			}
			env := r.env()
			firebase := tc.firebaseProject
			if firebase == "" {
				firebase = "shared-fp-1"
			}
			lc := *env.lc
			lc.Budget = &localcfg.Budget{Mode: "observe", FirebaseProject: firebase, RTDBURL: f.URL}
			env.lc, env.db = &lc, client

			gate, err := readManagedGate(context.Background(), env, r.facts())
			if err == nil {
				t.Fatal("the gate let a plan be judged against a database this installation may not write")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("the refusal exits %d, want %d: it is the operator's to fix: %v", ExitCode(err), ExitUserError, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the fact that is wrong (%q): %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), r.facts().Declined) {
				t.Errorf("the refusal does not say what was not done (%q): %v", r.facts().Declined, err)
			}
			if gate.managed {
				t.Error("the gate is managed after the ownership question refused: nothing may be admitted from here")
			}
			if err := gate.recorded(context.Background()); err != nil {
				t.Fatalf("recording nothing: %v", err)
			}
			if keys := r.auditKeys(t); len(keys) != 0 {
				t.Errorf("an ownership refusal left %q: it is asked before the gate, so no plan was judged and there is no digest to record", keys)
			}
		})
	}
}

// The record names the account the write ran as, and a command that cannot
// establish one fails rather than filing a record that does not say who ran it:
// "who" is the first thing an owner asks of a trail.
func TestManagedGateNamesTheAccountTheWriteRanAs(t *testing.T) {
	write := func(t *testing.T, r *managedGateRig) error {
		t.Helper()
		facts := r.facts()
		facts.Identity = ""
		gate, err := readManagedGate(context.Background(), r.env(), facts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gate.admit(context.Background(), initflow.YesOrTyped, managedmode.ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil)); err != nil {
			t.Fatal(err)
		}
		return gate.recorded(context.Background())
	}

	t.Run("the credentials' own service account", func(t *testing.T) {
		r := newManagedGateRig(t)
		path := filepath.Join(t.TempDir(), "application_default_credentials.json")
		body, err := json.Marshal(map[string]string{"type": "service_account", "client_email": managedGateDeployer})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
		if err := write(t, r); err != nil {
			t.Fatal(err)
		}
		got := r.audits(t)
		if len(got) != 1 {
			t.Fatalf("the day holds %d records, want one", len(got))
		}
		if want := "serviceAccount:" + managedGateDeployer; got[0].Identity != want {
			t.Errorf("the record's identity is %q, want %q", got[0].Identity, want)
		}
	})
	t.Run("a person's credentials", func(t *testing.T) {
		r := newManagedGateRig(t)
		if err := write(t, r); err != nil {
			t.Fatal(err)
		}
		got := r.audits(t)
		if len(got) != 1 {
			t.Fatalf("the day holds %d records, want one", len(got))
		}
		if want := "user:" + managedGateUser; got[0].Identity != want {
			t.Errorf("the record's identity is %q, want %q", got[0].Identity, want)
		}
	})
	t.Run("neither, and the command fails", func(t *testing.T) {
		r := newManagedGateRig(t)
		// The rig's own stub is replaced here and put back on the way out, so
		// this subtest's answer cannot leak into the one after it whichever
		// order they run in — the rig's cleanup would restore it too, but only
		// when the whole test ends.
		oldUser := authenticatedUser
		authenticatedUser = func(context.Context, *localcfg.Config) (string, error) {
			return "", errors.New("no application default credentials")
		}
		defer func() { authenticatedUser = oldUser }()
		err := write(t, r)
		if err == nil {
			t.Fatal("a step whose account could not be established was reported as done")
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("the failure exits %d, want %d: the fix is the operator's to run: %v", ExitCode(err), ExitUserError, err)
		}
		if !strings.Contains(err.Error(), "application-default login") {
			t.Errorf("the failure does not name what to run: %v", err)
		}
		if keys := r.auditKeys(t); len(keys) != 0 {
			t.Errorf("an unnamed apply left %q, want nothing: a trail that does not say who wrote it is the half of the question an owner asks first", keys)
		}
	})
}
