package managed

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/safetext"
)

const (
	project    = "aurora"
	gcpProject = "proj-1234"
	// planBucket is a resource the cover admits (a storage bucket with the
	// module's own attributes) and planVM one it refuses (a type outside the
	// installation's modules), so a test can aim each gate separately.
	planBucket = "module.managed.google_storage_bucket.data"
	planVM     = "module.managed.google_compute_instance.vm"
)

// admitAll is a cover that admits everything and that the owner's delete list
// can be handed to: it is what a test of the guard's own refusal reaches for,
// because it never refuses a delete itself, so the refusal the verdict carries
// is the guard's. It is not the cover a managed apply hands Gate — that is
// tf.ManagedCover, which judges the resource (managedCover below) — and its
// WithDeleteEntries records the list Gate handed it, so a test can see the
// wiring.
type admitAll struct{ deletes *[]tf.DeleteEntry }

func (admitAll) NotCovered(*tf.Plan, []tf.ImportKey) []string { return nil }

func (a admitAll) WithDeleteEntries(allowDelete []tf.DeleteEntry) tf.Deletable {
	if a.deletes != nil {
		*a.deletes = slices.Clone(allowDelete)
	}
	return a
}

// judgesNothing is a cover that admits everything and cannot be told the
// owner's delete list at all: the fail-closed case of the delete rule. A listed
// address must not be admitted through it, because the resource is never
// judged — the address on the owner's list is not a judgement of the resource.
type judgesNothing struct{}

func (judgesNothing) NotCovered(*tf.Plan, []tf.ImportKey) []string { return nil }

// cover is the scope-and-attribute gate of the managed project the tests judge
// for: the installation's tf.Cover with the inputs the review would have
// shown. It is the real gate, and the refusals below are its own.
func cover() tf.Cover {
	return tf.Cover{Projects: []string{gcpProject}, Registry: "us-east5-docker.pkg.dev", Region: "us-east5",
		Buckets: []string{"fugaro-runs-" + gcpProject}, Billing: []string{"AAAA-BBBB"}}
}

// managedCover is the gate a managed apply really hands Gate: Task 1.1's
// managed allowlist over the same review the installation's cover would have
// been given. A test of the delete rule uses it, because it is the cover that
// judges the resource (D72/D74); admitAll judges nothing.
func managedCover() tf.ManagedCover {
	return tf.ManagedCover{Cover: cover()}
}

// change is one entry of a plan's resource_changes.
func change(addr, typ string, actions []string, after map[string]any) map[string]any {
	if after == nil {
		after = map[string]any{}
	}
	return map[string]any{"address": addr, "type": typ, "mode": "managed",
		"change": map[string]any{"actions": actions, "before": map[string]any{}, "after": after}}
}

// managedChange is one entry of a managed plan's resource_changes, under
// Google's provider: a change with no provider name is refused by the managed
// cover before any rule about the resource is read, so a test of a managed
// rule names it.
func managedChange(addr, typ string, actions []string, before, after map[string]any) map[string]any {
	ch := change(addr, typ, actions, after)
	ch["provider_name"] = "registry.terraform.io/hashicorp/google"
	ch["change"].(map[string]any)["before"] = before
	return ch
}

// bucketCreated is the time_created a storage bucket's state carries: the
// per-instance value the delete rule binds an owner's entry to
// (tf.removalInstanceAttributes), which terraform's state always has, so that
// a bucket fixture here is a state terraform wrote. A fixture that left it out
// would be a removal the rule refuses for identifying nothing, which is not
// the rule those fixtures are about (TestGateRefusesARemovalTheRuleCannotIdentify
// is where that refusal is exercised, on purpose).
const bucketCreated = "2026-01-02T03:04:05.678Z"

// deleted is one entry of a managed plan's resource_changes for a resource the
// plan removes: the resource in before and null in after, which is what the
// managed cover judges the resource itself on. The null is set here rather
// than left to change, which writes an empty object in place of a missing
// after: a destroy's after is null in terraform's own JSON, and a fixture that
// put a resource there would not be the shape the rule is written about.
func deleted(addr, typ string, before map[string]any) map[string]any {
	ch := managedChange(addr, typ, []string{"delete"}, before, nil)
	ch["change"].(map[string]any)["after"] = nil
	return ch
}

// removing is change with the object the plan takes away in before: the shape
// of a destroy and of a replace's before view, which is what the owner's list
// is matched against (tf.DeleteEntry, plan Task 6.3). change's own before is
// empty — it is the fixture for creates — and a removal whose before view
// identifies no object is refused however the list is written, so a test of
// the delete rule builds its removals here.
func removing(addr, typ string, actions []string, before, after map[string]any) map[string]any {
	ch := change(addr, typ, actions, after)
	ch["change"].(map[string]any)["before"] = before
	return ch
}

// entry is an approval spelled out rather than computed from the plan: for a
// test whose point is an address the plan does not carry (a stale entry) or an
// entry bound to another object than the one the plan removes. A test that
// means "this removal is approved" calls rig.approving instead, which reads the
// entry off the plan.
func entry(address, before string) tf.DeleteEntry {
	return tf.DeleteEntry{Address: address, Before: before}
}

// bucket is a storage bucket as the installation's module writes it: the one
// resource the cover admits without a run's own inputs (an image, a job's
// account, a log sink).
func bucket(addr string) map[string]any {
	return change(addr, "google_storage_bucket", []string{"create"},
		map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true})
}

// vm is a resource type outside the installation's modules: the cover refuses
// it, the guard does not.
func vm(addr string) map[string]any {
	return change(addr, "google_compute_instance", []string{"create"}, map[string]any{})
}

var (
	fakeOnce sync.Once
	fakeBin  string
	fakeErr  error
)

// fakeTerraform builds the fake terraform once for the package's tests: the
// same stand-in internal/infra/tf and internal/cli use. The gate reads its
// plan through tf.TF.Show exactly as an apply does, and the fake's call log is
// what proves the gate applied nothing.
func fakeTerraform(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fugaro-managed-faketerraform-")
		if err != nil {
			fakeErr = err
			return
		}
		fakeBin = filepath.Join(dir, "terraform")
		out, err := exec.Command("go", "build", "-o", fakeBin, "github.com/dimipaun/fugaro/internal/infra/tf/faketerraform").CombinedOutput()
		if err != nil {
			fakeErr = fmt.Errorf("building the fake terraform: %v\n%s", err, out)
		}
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	return fakeBin
}

// rig is a workdir, the fake's script and the log of every call it recorded.
type rig struct {
	dir    string
	log    string
	script map[string]any
}

func newRig(t *testing.T) *rig {
	t.Helper()
	root := t.TempDir()
	r := &rig{dir: filepath.Join(root, "work"), log: filepath.Join(root, "calls.jsonl"), script: map[string]any{}}
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return r
}

// tf is a terraform running the fake, with the rig's script and log. The
// fake's own two variables are added after tf.Env, which drops them: they are
// not on the allowlist, and a real terraform never sees them.
func (r *rig) tf(t *testing.T) *tf.TF {
	t.Helper()
	env, err := tf.Env(withoutImpersonation(os.Environ()), r.dir, filepath.Join(r.dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	script, err := json.Marshal(r.script)
	if err != nil {
		t.Fatal(err)
	}
	env = append(env, "FAKE_TERRAFORM_LOG="+r.log, "FAKE_TERRAFORM_SCRIPT="+string(script))
	tfbin, err := tf.New(fakeTerraform(t), r.dir, env)
	if err != nil {
		t.Fatal(err)
	}
	return tfbin
}

// withoutImpersonation drops the one variable tf.Env refuses rather than
// drops, so a test runs whatever the environment around it holds.
func withoutImpersonation(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		return strings.HasPrefix(kv, "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT=")
	})
}

// planFile is the saved plan the gate is pointed at. The fake prints its
// scripted plan whatever the file holds — a real saved plan is a binary no
// test writes — so what the file is, is a name.
func (r *rig) planFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(r.dir, "fugaro.tfplan")
	if err := os.WriteFile(p, []byte("the saved plan, as the fake reports it"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// plan is the plan the rig has scripted for the fake's show.
func (r *rig) plan(t *testing.T) *tf.Plan {
	t.Helper()
	sh, ok := r.script["show"].(map[string]any)
	if !ok {
		t.Fatal("the rig has no scripted plan")
	}
	var p tf.Plan
	if err := json.Unmarshal([]byte(sh["stdout"].(string)), &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

// approving is the entry the owner's delete list holds for the removal at
// address in the plan this rig has scripted: the address and the identity of
// the object the plan shows there, which is what `fugaro allow-delete` records
// (tf.ApprovalFor). A test that means "this removal is approved" computes its
// entry from the plan rather than spelling it out, so a fixture cannot approve
// an object the plan does not show.
func (r *rig) approving(t *testing.T, address string) tf.DeleteEntry {
	t.Helper()
	for _, rc := range r.plan(t).ResourceChanges {
		if rc.Address == address {
			return tf.ApprovalFor(rc)
		}
	}
	t.Fatalf("the rig's plan carries no change at %s", address)
	return tf.DeleteEntry{}
}

// show scripts the fake's `show -json <plan>` with a plan of these changes.
func (r *rig) show(t *testing.T, changes ...map[string]any) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"format_version": "1.2", "resource_changes": changes})
	if err != nil {
		t.Fatal(err)
	}
	r.script["show"] = map[string]any{"stdout": string(b)}
}

// managedShow scripts the fake's `show` with a plan that carries the
// configuration a managed plan must: Google's provider with its project
// written out. The managed cover refuses a plan with changes and no
// configuration to read them from, so a test of a managed rule goes through
// here.
func (r *rig) managedShow(t *testing.T, changes ...map[string]any) {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"format_version":   "1.2",
		"resource_changes": changes,
		"configuration": map[string]any{"provider_config": map[string]any{"google": map[string]any{
			"name": "google", "full_name": "registry.terraform.io/hashicorp/google",
			"expressions": map[string]any{"project": map[string]any{"constant_value": gcpProject}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.script["show"] = map[string]any{"stdout": string(b)}
}

// failShow scripts a `show` that fails, as an unreadable plan file does.
func (r *rig) failShow() {
	r.script["show"] = map[string]any{"exit": 1, "stderr": "no such file"}
}

// applied is every apply the fake was asked to run: the gate reads a plan and
// returns, and this is how a test says so.
func (r *rig) applied(t *testing.T) []string {
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
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var c struct{ Args []string }
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		if len(c.Args) > 0 && c.Args[0] == "apply" {
			out = append(out, strings.Join(c.Args, " "))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// spec is the managed project's gate spec: the project's names, the cover and
// the owner's delete list, as the entries the owner's object holds (Task 6.3).
func spec(t *testing.T, r *rig, c Cover, allow ...tf.DeleteEntry) Spec {
	t.Helper()
	return Spec{Project: project, GCPProject: gcpProject, Cover: c, AllowDelete: allow, TF: r.tf(t)}
}

// gate runs Gate and fails the test on an error, which is the "could not read"
// answer and never an admission.
func gate(t *testing.T, r *rig, s Spec) Verdict {
	t.Helper()
	v, err := Gate(context.Background(), r.planFile(t), s)
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	return v
}

// A plan both gates admit is admitted, and nothing was applied: the gate's
// whole output is a verdict.
func TestGateAdmitsAPlanBothGatesAdmit(t *testing.T) {
	r := newRig(t)
	r.show(t, bucket(planBucket))
	v := gate(t, r, spec(t, r, cover()))
	if !v.OK {
		t.Fatalf("a plan both gates admit is not admitted: %v", v.Refusals)
	}
	if len(v.Refusals) != 0 {
		t.Errorf("an admitted plan carries refusals: %v", v.Refusals)
	}
	if got := r.applied(t); len(got) != 0 {
		t.Errorf("the gate applied something: %v", got)
	}
}

// The red step's case: the guard refuses and the cover admits, and the verdict
// still says so. A Gate that returned only the last gate's verdict returns OK
// here, which is a plan that would delete a resource the owner never approved.
func TestGateReportsTheGuardRefusalEvenWhenTheCoverAdmits(t *testing.T) {
	r := newRig(t)
	r.show(t, change(planBucket, "google_storage_bucket", []string{"delete"}, nil))
	v := gate(t, r, spec(t, r, admitAll{}))
	if v.OK {
		t.Fatal("a plan the guard refuses was admitted")
	}
	if len(v.Refusals) != 1 {
		t.Fatalf("refusals %v, want the guard's one", v.Refusals)
	}
	got := v.Refusals[0]
	if got.Gate != guardGate || got.Address != planBucket || !strings.Contains(got.Why, "delete") {
		t.Errorf("refusal %+v, want the guard's refusal of %s with its actions", got, planBucket)
	}
	if applied := r.applied(t); len(applied) != 0 {
		t.Errorf("the gate applied something: %v", applied)
	}
}

// And the other way: the cover refuses a plan the guard admits, with the real
// cover and its own rule as the reason.
func TestGateReportsTheCoverRefusalWhenTheGuardAdmits(t *testing.T) {
	r := newRig(t)
	r.show(t, vm(planVM))
	v := gate(t, r, spec(t, r, cover()))
	if v.OK {
		t.Fatal("a plan the cover refuses was admitted")
	}
	if len(v.Refusals) != 1 {
		t.Fatalf("refusals %v, want the cover's one", v.Refusals)
	}
	got := v.Refusals[0]
	if got.Gate != coverGate || got.Address != planVM || !strings.Contains(got.Why, "outside the installation's modules") {
		t.Errorf("refusal %+v, want the cover's refusal of %s and its rule", got, planVM)
	}
}

// Both gates refusing carries both refusals: the verdict lists every refusal
// rather than the first, which is the shape the guard itself already uses.
func TestGateReportsBothGatesRefusals(t *testing.T) {
	r := newRig(t)
	r.show(t, change(planBucket, "google_storage_bucket", []string{"delete"}, nil), vm(planVM))
	v := gate(t, r, spec(t, r, cover()))
	if v.OK {
		t.Fatal("a plan both gates refuse was admitted")
	}
	var guard, coverRefusals int
	for _, ref := range v.Refusals {
		switch {
		case ref.Gate == guardGate && ref.Address == planBucket:
			guard++
		case ref.Gate == coverGate && ref.Address == planVM:
			coverRefusals++
		}
	}
	if guard != 1 || coverRefusals != 1 {
		t.Fatalf("refusals %v, want the guard's for %s and the cover's for %s", v.Refusals, planBucket, planVM)
	}
	// The guard's refusals come before the cover's, as Gate documents.
	seenCover := false
	for _, ref := range v.Refusals {
		if ref.Gate == coverGate {
			seenCover = true
		} else if seenCover {
			t.Fatalf("refusals %v: a guard refusal follows a cover refusal", v.Refusals)
		}
	}
}

// The verdict never carries the guard's advice to name an address with
// --allow-delete: in a managed project that flag is the one thing the delete
// rule forbids (D74), and a refusal that named it would be read by the session
// it refuses as a thing to pass.
func TestGateRefusalNeverNamesTheAllowDeleteFlag(t *testing.T) {
	r := newRig(t)
	r.show(t, change(planBucket, "google_storage_bucket", []string{"delete"}, nil))
	v := gate(t, r, spec(t, r, admitAll{}))
	if v.OK {
		t.Fatal("a plan the guard refuses was admitted")
	}
	for _, ref := range v.Refusals {
		if strings.Contains(ref.Why, "--allow-delete") || strings.Contains(ref.Address, "--allow-delete") {
			t.Errorf("refusal %+v tells the reader to pass --allow-delete", ref)
		}
	}
}

// A spec with no cover refuses every plan rather than admitting it: the gate's
// second half is what admits a managed plan, and a missing one is not a pass.
func TestGateRefusesWhenTheCoverIsMissing(t *testing.T) {
	r := newRig(t)
	r.show(t, bucket(planBucket))
	s := spec(t, r, nil)
	s.Cover = nil
	v := gate(t, r, s)
	if v.OK {
		t.Fatal("a plan with no cover at all was admitted")
	}
	if len(v.Refusals) != 1 || v.Refusals[0].Gate != coverGate || !strings.Contains(v.Refusals[0].Why, "no cover") {
		t.Fatalf("refusals %v, want the missing cover's own refusal", v.Refusals)
	}
}

// A plan the gate cannot read is an error and never a verdict: the caller must
// not read "no verdict" as "admitted", and the zero Verdict is not OK either.
func TestGatePlanThatCannotBeReadIsAnError(t *testing.T) {
	t.Run("no terraform", func(t *testing.T) {
		r := newRig(t)
		s := spec(t, r, cover())
		s.TF = nil
		v, err := Gate(context.Background(), r.planFile(t), s)
		if err == nil {
			t.Fatal("Gate read a plan with no terraform")
		}
		if v.OK {
			t.Error("a plan that was not read is OK")
		}
	})
	t.Run("show fails", func(t *testing.T) {
		r := newRig(t)
		r.failShow()
		v, err := Gate(context.Background(), r.planFile(t), spec(t, r, cover()))
		if err == nil {
			t.Fatal("Gate read a plan terraform could not show")
		}
		if v.OK {
			t.Error("a plan that was not read is OK")
		}
		if got := r.applied(t); len(got) != 0 {
			t.Errorf("the gate applied something: %v", got)
		}
	})
}

// The refusals carry the address and the rule apart, and a gate that refused
// without an address keeps its own words rather than being dropped.
func TestPlanRefusalsCarryTheAddressAndTheRule(t *testing.T) {
	got := refusals(coverGate, []string{
		`module.m.google_storage_bucket.x (runs the image "x", pinned (us-east5-docker.pkg.dev/p/r/fugaro-ghcr/o/fugaro-build:1.2.3@sha256:ab))`,
		"",
	})
	want := Refusal{Gate: coverGate, Address: "module.m.google_storage_bucket.x",
		Why: `runs the image "x", pinned (us-east5-docker.pkg.dev/p/r/fugaro-ghcr/o/fugaro-build:1.2.3@sha256:ab)`}
	if len(got) != 1 || got[0] != want {
		t.Errorf("refusals %+v, want %+v", got, want)
	}
	got = refusals(guardGate, guardLines("the plan would delete or replace 1 resource(s):\n  module.m.x (delete)"))
	want = Refusal{Gate: guardGate, Address: "module.m.x", Why: "delete"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("refusals %+v, want the guard's address alone", got)
	}
	got = refusals(guardGate, guardLines("guard: no plan"))
	want = Refusal{Gate: guardGate, Why: "guard: no plan"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("refusals %+v, want the refusal whole", got)
	}
}

// The entries rule's refusal is rendered whole, and this is the test that pins
// it: a guard item is "<address> (<actions>): <why>", and the why this rule
// writes — the list's own entry and the object the plan removes, each in
// parentheses (tf.Approves) — ends in a ")" of its own. Reading the two shapes
// apart by the item's LAST character instead took the actions as the why and
// cut that closing parenthesis off with them: the verdict carried a Why
// starting "delete): " and losing the sentence's end, while the address beside
// it was right, so nothing else failed. Both gates' copies are pinned here,
// and the why is read back from tf.Approves rather than spelled out, so this
// test and the rule cannot drift.
func TestGateRendersTheEntriesRefusalWhole(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.fmgd_data"
	r := newRig(t)
	// The plan removes an object the owner's entry is not about: the entry
	// names the address for the object that is gone.
	r.managedShow(t, deleted(addr, "google_storage_bucket", map[string]any{"name": "fmgd-other", "id": "fmgd-other", "time_created": bucketCreated}))
	stale := entry(addr, "type=google_storage_bucket project="+gcpProject+" name=fmgd-gone id=fmgd-gone")
	v := gate(t, r, spec(t, r, managedCover(), stale))
	if v.OK {
		t.Fatal("a removal the owner's entry is not about was admitted")
	}
	plan := r.plan(t)
	var rc tf.ResourceChange
	for i := range plan.ResourceChanges {
		if plan.ResourceChanges[i].Address == addr {
			rc = plan.ResourceChanges[i]
		}
	}
	if rc.Address == "" {
		t.Fatalf("the rig's plan carries no change at %s", addr)
	}
	if ok, why := tf.Approves([]tf.DeleteEntry{stale}, rc); ok || why == "" {
		t.Fatalf("tf.Approves(%v) = %v, %q, want a refusal with its own sentence", stale, ok, why)
	}
	_, why := tf.Approves([]tf.DeleteEntry{stale}, rc)
	for gate, want := range map[string]string{
		guardGate: "delete: " + why,
		coverGate: "actions delete: " + why,
	} {
		ref, ok := findRefusal(v.Refusals, gate, addr)
		if !ok {
			t.Fatalf("the %s refused %s with no refusal at its address: %+v", gate, addr, v.Refusals)
		}
		if ref.Why != want {
			t.Errorf("the %s refusal for %s carries\n%q\nwant\n%q", gate, addr, ref.Why, want)
		}
		if !strings.HasSuffix(ref.Why, ")") {
			t.Errorf("the %s refusal for %s lost the sentence's closing parenthesis: %q", gate, addr, ref.Why)
		}
		if strings.Contains(ref.Why, "delete): ") {
			t.Errorf("the %s refusal for %s carries the actions as its why: %q", gate, addr, ref.Why)
		}
	}
	// The address is one a new entry would admit, so the advice stands.
	if !slices.Contains(v.Listable, addr) {
		t.Errorf("Listable %v does not name %s, whose refusal a new entry would clear", v.Listable, addr)
	}
}

// TestGateRefusesARemovalTheRuleCannotIdentify: a before view of a type the
// provider gives a per-instance value to, without that value, is not a state
// terraform writes — and the rule refuses it however the owner's list is
// written, because the name such a view carries is known not to be enough to
// tell one instance of it from the next (tf.Identifies). The refusal says
// which value is missing rather than that the plan names nothing, and the
// address is not offered as listable: no entry can be matched against that
// view, so sending the owner to record one would lead nowhere.
func TestGateRefusesARemovalTheRuleCannotIdentify(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.fmgd_data"
	r := newRig(t)
	before := map[string]any{"name": "fmgd-data", "id": "fmgd-data", "project": gcpProject}
	r.managedShow(t, removing(addr, "google_storage_bucket", []string{"delete"}, before, nil))
	// The owner's list names the address, and its entry is the strongest one
	// the view could support: the type, the project, the name and the id are
	// all exactly what the plan shows.
	listed := entry(addr, "type=google_storage_bucket project="+gcpProject+" name=fmgd-data id=fmgd-data")
	v := gate(t, r, spec(t, r, managedCover(), listed))
	if v.OK {
		t.Fatal("a removal whose before view carries no per-instance value was admitted")
	}
	ref, ok := findRefusal(v.Refusals, guardGate, addr)
	if !ok {
		t.Fatalf("the guard refused with no refusal at %s: %+v", addr, v.Refusals)
	}
	if !strings.Contains(ref.Why, "carries no time_created") {
		t.Errorf("the guard's refusal is %q, want it to name the missing time_created", ref.Why)
	}
	if slices.Contains(v.Listable, addr) {
		t.Errorf("Listable %v names %s, which no entry can be matched against", v.Listable, addr)
	}
}

// The identity a refusal prints is the identity an entry has to hold, however
// long it is. The removal rules print it for the owner to copy into fugaro
// allow-delete, the entry they record is matched against the plan's own
// identity byte for byte (tf.DeleteEntry, tf.Approves) — and a why rendered
// through safetext.Field is cut at FieldMax, which for this sentence falls in
// the middle of the identity: the entries rule names every entry the list holds
// at the address BEFORE the object the plan removes (tf.Approves), so the
// identity sits at the sentence's end and past rune 512. What the owner would
// copy then is a prefix, the entry would match nothing, and every later apply
// would refuse the same removal with the same sentence: a loop that cannot
// close. The bound is refusalWhyMax instead, which is the parts' bound and not
// a bound on the identity, and this test walks the whole way the owner does —
// the verdict's refusal, the text the apply returns, and the identity read back
// out of it into an entry that approves.
func TestTheIdentityARefusalPrintsIsOneAnEntryCanHold(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.fmgd_data"
	long := strings.Repeat("n", 300)
	r := newRig(t)
	r.managedShow(t, deleted(addr, "google_storage_bucket", map[string]any{
		"name": long, "id": long, "project": gcpProject, "time_created": bucketCreated}))
	// The entry names the address for another object, which is the refusal that
	// prints what the plan removes beside what the list holds.
	stale := entry(addr, "type=google_storage_bucket project="+gcpProject+" name=fmgd-gone id=fmgd-gone")
	v := gate(t, r, spec(t, r, managedCover(), stale))
	if v.OK {
		t.Fatal("a removal the owner's entry is not about was admitted")
	}
	var rc tf.ResourceChange
	for _, c := range r.plan(t).ResourceChanges {
		if c.Address == addr {
			rc = c
		}
	}
	if rc.Address == "" {
		t.Fatalf("the rig's plan carries no change at %s", addr)
	}
	want := tf.RemovedIdentity(rc).String()
	// The fixture has to be an identity Field would change, or the test proves
	// nothing about the renderers below: one Field leaves alone reads the same
	// either way.
	if safetext.Field(want) == want {
		t.Fatalf("the fixture's identity of %d runes is one safetext.Field leaves alone", utf8.RuneCountInString(want))
	}
	for _, gate := range []string{guardGate, coverGate} {
		ref, ok := findRefusal(v.Refusals, gate, addr)
		if !ok {
			t.Fatalf("the %s refused %s with no refusal at its address: %+v", gate, addr, v.Refusals)
		}
		if !strings.Contains(ref.Why, want) {
			t.Errorf("the %s refusal for %s does not carry the identity whole:\n%q\nwant it to hold\n%q", gate, addr, ref.Why, want)
		}
		if strings.Contains(ref.Why, "…") {
			t.Errorf("the %s refusal for %s is cut: %q", gate, addr, ref.Why)
		}
	}
	// What the apply returns is the text a person reads, and the identity it
	// carries is the one the owner pastes back.
	msg := (&RefusedError{Verdict: v}).Error()
	if !strings.Contains(msg, want) {
		t.Errorf("the refusal the apply returns does not carry the identity whole:\n%s\nwant it to hold\n%s", msg, want)
	}
	// The owner copies the printed text out of the sentence, so what the copy
	// holds is the identity AND the sentence's own closing bracket, and nothing
	// else: the command that records it takes that one bracket off (internal/cli's
	// approveArgument, TestAllowDeleteCommandTakesTheSentenceBracketOffAPaste),
	// which is only a rule it can have because an identity holds no parenthesis
	// of its own (tf.identityValue). Read any deeper into the sentence — or with
	// the bracket left on — and the entry that comes out matches nothing, which
	// is the loop that cannot close.
	i := strings.LastIndex(msg, "and the plan removes ")
	if i < 0 {
		t.Fatalf("the refusal names no object:\n%s", msg)
	}
	copied := msg[i+len("and the plan removes "):]
	if copied != want+")" {
		t.Errorf("the refusal's copy is\n%q\nwant the plan's identity %q followed by the sentence's own bracket", copied, want)
	}
	if ok, why := tf.Approves([]tf.DeleteEntry{{Address: addr, Before: want}}, rc); !ok {
		t.Errorf("the identity a refusal printed approves nothing (%s), so the loop the delete list works by cannot close", why)
	}
}

// A why past refusalWhyMax is not one of the removal rules' sentences, whose
// every part is bounded where the rule built it (refusalWhy): it is cut, with
// safetext.Field's own marker, rather than printed unbounded — the looser bound
// is for the identity the owner has to be able to paste, not a licence for a
// plan or a cover to write a screenful.
func TestARefusalWhyPastItsBoundIsCut(t *testing.T) {
	item := "module.managed.google_storage_bucket.x (an address the rule refused " + strings.Repeat("a", refusalWhyMax) + ")"
	got := refusals(coverGate, []string{item})
	if len(got) != 1 {
		t.Fatalf("refusals %+v, want one refusal", got)
	}
	if n := utf8.RuneCountInString(got[0].Why); n > refusalWhyMax {
		t.Errorf("the why is %d runes, past the %d a verdict carries", n, refusalWhyMax)
	}
	if !strings.HasSuffix(got[0].Why, "…") {
		t.Errorf("the why %d runes long is not marked as cut: %q", utf8.RuneCountInString(got[0].Why), got[0].Why)
	}
}

// findRefusal is the refusal one gate made about one address, and whether it
// made one.
func findRefusal(rs []Refusal, gate, address string) (Refusal, bool) {
	for _, r := range rs {
		if r.Gate == gate && r.Address == address {
			return r, true
		}
	}
	return Refusal{}, false
}

// A guard that errs fails closed even when its message says nothing: OK is not
// read off the refusals the message turns into.
func TestGateGuardErrorWithEmptyMessageIsNotOK(t *testing.T) {
	for _, msg := range []string{"", "  \n "} {
		old := guard
		guard = func(*tf.Plan, []tf.DeleteEntry) error { return errors.New(msg) }
		r := newRig(t)
		r.show(t, bucket(planBucket))
		v := gate(t, r, spec(t, r, admitAll{}))
		guard = old
		if v.OK {
			t.Errorf("a guard that refused with %q was admitted", msg)
		}
	}
}

// The red step of the delete rule, and the rule it is about: the owner's list
// is one half of the decision and the resource is the other. A cover that
// judges no delete at all — the shape a cover would have to have for the
// address alone to decide, and the fail-closed case the wiring exists for —
// must not have a listed delete admitted through it: the resource is never
// read, and an address on the owner's list is not a judgement of it. Before
// the rule, this verdict is OK.
func TestGateRefusesAListedDeleteTheCoverDoesNotJudge(t *testing.T) {
	r := newRig(t)
	r.show(t, removing(planBucket, "google_storage_bucket", []string{"delete"},
		map[string]any{"name": "fugaro-runs-" + gcpProject, "id": "bucket-0001", "time_created": bucketCreated}, nil))
	v := gate(t, r, spec(t, r, judgesNothing{}, r.approving(t, planBucket)))
	if v.OK {
		t.Fatal("a delete of an address on the owner's list was admitted by a cover that judges no delete")
	}
	if len(v.Refusals) != 1 {
		t.Fatalf("refusals %v, want the cover's one", v.Refusals)
	}
	got := v.Refusals[0]
	if got.Gate != coverGate || got.Address != planBucket || !strings.Contains(got.Why, "not judged") {
		t.Errorf("refusal %+v, want the cover's refusal of %s saying the resource was not judged", got, planBucket)
	}
	// And only a listed address is that case: the same unjudging cover with an
	// address that is not on the list leaves the guard's own refusal as the
	// only one, which is the rule the guard was already making.
	if v := gate(t, r, spec(t, r, judgesNothing{})); len(v.Refusals) != 1 || v.Refusals[0].Gate != guardGate {
		t.Errorf("refusals %v, want the guard's one refusal for an address that is not listed", v.Refusals)
	}
}

// A replace is a delete here as it is everywhere else in the rule: the remove
// half of either of terraform's two orders is what the list is read for, so a
// cover that judges nothing refuses a listed replace exactly as it refuses a
// listed destroy — the created half being read elsewhere is not the point.
func TestGateRefusesAListedReplaceTheCoverDoesNotJudge(t *testing.T) {
	for _, order := range [][]string{{"delete", "create"}, {"create", "delete"}} {
		t.Run(strings.Join(order, ","), func(t *testing.T) {
			r := newRig(t)
			r.show(t, removing(planBucket, "google_storage_bucket", order,
				map[string]any{"name": "fugaro-runs-" + gcpProject, "id": "bucket-0001", "time_created": bucketCreated},
				map[string]any{"name": "fugaro-runs-" + gcpProject}))
			v := gate(t, r, spec(t, r, judgesNothing{}, r.approving(t, planBucket)))
			if v.OK {
				t.Fatal("a replace of an address on the owner's list was admitted by a cover that judges no delete")
			}
			if len(v.Refusals) != 1 {
				t.Fatalf("refusals %v, want the cover's one", v.Refusals)
			}
			got := v.Refusals[0]
			if got.Gate != coverGate || got.Address != planBucket || !strings.Contains(got.Why, "not judged") {
				t.Errorf("refusal %+v, want the cover's refusal of %s saying the resource was not judged", got, planBucket)
			}
		})
	}
}

// Gate hands the cover the same list it hands the guard, so the two cannot be
// given different ones — which is how a delete could be admitted by the list
// one of them was told and refused by the other's.
func TestGateHandsTheCoverTheSameDeleteListAsTheGuard(t *testing.T) {
	var covered, guarded []tf.DeleteEntry
	old := guard
	guard = func(_ *tf.Plan, allowDelete []tf.DeleteEntry) error { guarded = slices.Clone(allowDelete); return nil }
	t.Cleanup(func() { guard = old })
	r := newRig(t)
	r.show(t, removing(planBucket, "google_storage_bucket", []string{"delete"},
		map[string]any{"name": "fugaro-runs-" + gcpProject, "id": "bucket-0001", "time_created": bucketCreated}, nil))
	approved := r.approving(t, planBucket)
	if v := gate(t, r, spec(t, r, admitAll{deletes: &covered}, approved)); !v.OK {
		t.Fatalf("a listed delete both gates admit is refused: %v", v.Refusals)
	}
	if !slices.Equal(guarded, []tf.DeleteEntry{approved}) {
		t.Errorf("the guard was handed %v, want the spec's own list", guarded)
	}
	if !slices.Equal(covered, guarded) {
		t.Errorf("the guard was handed %v and the cover %v, want the same list", guarded, covered)
	}
}

// The delete rule through Gate with the real managed cover: a listed delete of a
// resource the managed allowlist admits is admitted, and the same listed
// address admits nothing whose resource is not one a managed plan may carry.
func TestGateAdmitsAListedDeleteOnlyInsideTheManagedAllowlist(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.fmgd_data"
	allowed := func() map[string]any {
		return deleted(addr, "google_storage_bucket", map[string]any{"name": "fmgd-data", "time_created": bucketCreated})
	}
	r := newRig(t)
	r.managedShow(t, allowed())
	if v := gate(t, r, spec(t, r, managedCover(), r.approving(t, addr))); !v.OK {
		t.Errorf("a listed delete of a resource the allowlist admits is refused: %v", v.Refusals)
	}
	// The resource judged although the address is listed: the plan's own
	// bucket carries the prefix, this one is Fugaro's state bucket.
	r.managedShow(t, deleted(addr, "google_storage_bucket", map[string]any{"name": "fugaro-tfstate-" + gcpProject, "time_created": bucketCreated}))
	v := gate(t, r, spec(t, r, managedCover(), r.approving(t, addr)))
	if v.OK {
		t.Fatal("a listed delete of a resource outside the managed prefix was admitted")
	}
	if len(v.Refusals) != 1 || v.Refusals[0].Gate != coverGate || !strings.Contains(v.Refusals[0].Why, "outside the managed prefix") {
		t.Errorf("refusals %v, want the cover's prefix refusal of %s", v.Refusals, addr)
	}
	// And a resource whose type the allowlist does not carry: the address is
	// listed, so the guard admits it and the cover's own rule is the refusal.
	r.managedShow(t, deleted(addr, "google_compute_instance", map[string]any{"name": "fmgd-data", "time_created": bucketCreated}))
	v = gate(t, r, spec(t, r, managedCover(), r.approving(t, addr)))
	if v.OK {
		t.Fatal("a listed delete of a type outside the managed allowlist was admitted")
	}
	if len(v.Refusals) != 1 || v.Refusals[0].Gate != coverGate || !strings.Contains(v.Refusals[0].Why, "outside the managed project's allowlist") {
		t.Errorf("refusals %v, want the cover's type refusal of %s", v.Refusals, addr)
	}
	// The other half: the resource is the plan's own and the address is not on
	// the list, which is the guard's refusal to make and the verdict names it.
	r.managedShow(t, allowed())
	v = gate(t, r, spec(t, r, managedCover()))
	if v.OK {
		t.Fatal("a delete with an empty list was admitted")
	}
	guarded := false
	for _, ref := range v.Refusals {
		guarded = guarded || (ref.Gate == guardGate && ref.Address == addr && strings.Contains(ref.Why, "delete"))
	}
	if !guarded {
		t.Errorf("refusals %v, want the guard's refusal of %s with its actions", v.Refusals, addr)
	}
	if v := gate(t, r, spec(t, r, managedCover(), entry("module.managed.google_storage_bucket.other", "type=google_storage_bucket id=other"))); v.OK {
		t.Error("a delete of an address not listed was admitted")
	}
}

// The rule Task 6.3 exists for, at the managed Gate: an approval is of the
// object the owner was shown, not a standing permission to delete whatever is
// at the address later. The plan removes the object the entry was given for
// and the removal is admitted; the same address carrying ANOTHER object — a
// bucket under another name, which is what a state rebuilt from a backup or a
// `state mv` leaves there — is an object the entry is not about, and both
// halves of the gate refuse it, naming the object the plan removes, which is
// what the owner needs to approve the deletion they now mean.
//
// The identity is the one the provider really writes for this type — for a
// bucket `id` IS the name, so the two views differ in both — rather than an
// id a fixture invents to make the rule look stronger than it is: a same-NAMED
// re-creation of a bucket carries the same identity and is still approved,
// which is the residual §6 item 5 and internal/infra/tf/identity.go record and
// TestAnEntryApprovesASameNamedObjectReCreatedAtTheAddress pins.
func TestGateRefusesARemovalAtAnAddressApprovedForAnotherObject(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.fmgd_data"
	r := newRig(t)
	r.managedShow(t, deleted(addr, "google_storage_bucket", map[string]any{"name": "fmgd-data", "id": "fmgd-data", "time_created": bucketCreated}))
	approved := r.approving(t, addr)
	if v := gate(t, r, spec(t, r, managedCover(), approved)); !v.OK {
		t.Fatalf("the removal the owner's entry was given for is refused: %v", v.Refusals)
	}
	// The address now carries another object: a bucket under another name,
	// whose identity the entry does not hold. The entry is about the object
	// that is gone, so it approves nothing.
	r.managedShow(t, deleted(addr, "google_storage_bucket", map[string]any{"name": "fmgd-other", "id": "fmgd-other", "time_created": bucketCreated}))
	v := gate(t, r, spec(t, r, managedCover(), approved))
	if v.OK {
		t.Fatal("the entry approved a removal of an object it was not given for")
	}
	if len(v.Refusals) == 0 {
		t.Fatal("the refused removal carried no refusal")
	}
	named := false
	for _, ref := range v.Refusals {
		if ref.Address == addr && strings.Contains(ref.Why, "another object") {
			named = true
		}
	}
	if !named {
		t.Errorf("refusals %v, want one refusing %s for another object", v.Refusals, addr)
	}
	// The person who can change the answer is the owner, and the refusal says
	// what they would approve: the identity the plan removes, which is the
	// object now at the address and not the one the entry holds.
	if v := gate(t, r, spec(t, r, managedCover(), r.approving(t, addr))); !v.OK {
		t.Errorf("the entry for the object the plan removes is refused: %v", v.Refusals)
	}
}

// A listed address with no resource in the plan changes nothing: the list is
// read per change, so a stale entry — an address the plan no longer carries —
// neither admits nor refuses anything.
func TestGateAdmitsAListedAddressThePlanDoesNotCarry(t *testing.T) {
	r := newRig(t)
	r.managedShow(t, managedChange("module.managed.google_storage_bucket.data", "google_storage_bucket",
		[]string{"create"}, nil, map[string]any{"name": "fmgd-data", "time_created": bucketCreated}))
	if v := gate(t, r, spec(t, r, managedCover(), entry("module.managed.google_compute_instance.gone", "type=google_compute_instance id=gone"))); !v.OK {
		t.Errorf("a listed address the plan does not carry changed the verdict: %v", v.Refusals)
	}
}

// A list never authorizes a create: the delete list is read for the change that
// removes something and for nothing else, so a create at an address on it is
// judged by the allowlist exactly as one at an address that is not — the same
// plan, admitted or refused by the resource alone.
func TestGateListNeverAuthorizesACreate(t *testing.T) {
	const gone = "module.managed.google_compute_instance.gone"
	r := newRig(t)
	r.managedShow(t, managedChange(gone, "google_compute_instance", []string{"create"}, nil, map[string]any{}))
	v := gate(t, r, spec(t, r, managedCover(), r.approving(t, gone)))
	if v.OK {
		t.Fatal("a create at an address on the delete list was admitted although its type is not on the allowlist")
	}
	if len(v.Refusals) != 1 || v.Refusals[0].Gate != coverGate || !strings.Contains(v.Refusals[0].Why, "outside the managed project's allowlist") {
		t.Errorf("refusals %v, want the cover's type refusal of %s", v.Refusals, gone)
	}
	// And the same plan without the address listed: the list changed nothing.
	if v := gate(t, r, spec(t, r, managedCover())); len(v.Refusals) != 1 || v.Refusals[0].Why !=
		"a resource type outside the managed project's allowlist" {
		t.Errorf("refusals %v, want the same single refusal with no list at all", v.Refusals)
	}
}

// The same rule through the cover that cannot be told the list: an address on
// the owner's list is read for the change that removes something and for
// nothing else there either, so a create at a listed address is admitted
// exactly as one at an address that is not. It is one rule, and unjudgedDeletes
// reads the actions to apply it — a predicate that refused every listed address
// whatever the change would fail here.
func TestGateListNeverAuthorizesACreateThroughACoverThatJudgesNothing(t *testing.T) {
	r := newRig(t)
	r.show(t, change(planBucket, "google_storage_bucket", []string{"create"}, map[string]any{"name": "fugaro-runs-" + gcpProject}))
	for _, allow := range [][]tf.DeleteEntry{nil, {r.approving(t, planBucket)}} {
		if v := gate(t, r, spec(t, r, judgesNothing{}, allow...)); !v.OK || len(v.Refusals) != 0 {
			t.Errorf("a create with %v listed gives %v, want it admitted: the list is read for a remove and for nothing else", allow, v.Refusals)
		}
	}
}

// The managed path receives Spec.Imports: the cover a managed apply hands Gate
// is told the list beside the plan as tf.Deletable, and an import discovery did
// not find is refused through that branch exactly as through a plain tf.Cover.
// TestGateImportsMustBeOnTheSpecList above takes the other branch, because
// tf.Cover is not Deletable and only that test's cover is.
func TestGateManagedCoverReceivesSpecImports(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.data"
	id := gcpProject + "/data-bucket"
	r := newRig(t)
	imp := managedChange(addr, "google_storage_bucket", []string{"no-op"}, nil, map[string]any{"name": "fmgd-data", "time_created": bucketCreated})
	imp["change"].(map[string]any)["importing"] = map[string]any{"id": id}
	r.managedShow(t, imp)
	s := spec(t, r, managedCover())
	v := gate(t, r, s)
	if v.OK {
		t.Fatal("an import discovery did not find was admitted by the managed cover")
	}
	if len(v.Refusals) != 1 || v.Refusals[0].Gate != coverGate || !strings.Contains(v.Refusals[0].Why, "which this run's discovery did not find") {
		t.Fatalf("refusals %v, want the managed cover's missing-import refusal of %s", v.Refusals, addr)
	}
	// The same plan with the key discovery wrote: the list is what admitted it,
	// so the branch that hands the cover its inputs is the one under test.
	s.Imports = []tf.ImportKey{{Address: addr, ID: id}}
	if v := gate(t, r, s); !v.OK {
		t.Errorf("an import discovery found was refused by the managed cover: %v", v.Refusals)
	}
}

// An address on the owner's AllowDelete list is admitted for a delete only
// because the cover the test hands Gate admits the resource too; one not on
// the list is refused, and an empty list refuses every delete. What makes a
// listed delete of a resource the allowlist refuses fail is
// TestGateAdmitsAListedDeleteOnlyInsideTheManagedAllowlist.
func TestGateAdmitsADeleteOnlyForAListedAddress(t *testing.T) {
	r := newRig(t)
	r.show(t, removing(planBucket, "google_storage_bucket", []string{"delete"},
		map[string]any{"name": "fugaro-runs-" + gcpProject, "id": "bucket-0001", "time_created": bucketCreated}, nil))
	if v := gate(t, r, spec(t, r, admitAll{}, r.approving(t, planBucket))); !v.OK {
		t.Errorf("a delete of a listed address was refused: %v", v.Refusals)
	}
	if v := gate(t, r, spec(t, r, admitAll{}, entry("module.managed.google_storage_bucket.other", "type=google_storage_bucket id=other"))); v.OK {
		t.Error("a delete of an address not listed was admitted")
	}
	if v := gate(t, r, spec(t, r, admitAll{})); v.OK {
		t.Error("a delete with an empty list was admitted")
	}
}

// The cover receives Spec.Imports: an import discovery did not list is refused,
// one it listed is admitted.
func TestGateImportsMustBeOnTheSpecList(t *testing.T) {
	id := gcpProject + "/data-bucket"
	imp := bucket(planBucket)
	imp["change"].(map[string]any)["importing"] = map[string]any{"id": id}
	imp["change"].(map[string]any)["actions"] = []string{"no-op"}
	r := newRig(t)
	r.show(t, imp)
	s := spec(t, r, cover())
	if v := gate(t, r, s); v.OK {
		t.Error("an import discovery did not find was admitted")
	}
	s.Imports = []tf.ImportKey{{Address: planBucket, ID: id}}
	if v := gate(t, r, s); !v.OK {
		t.Errorf("an import discovery found was refused: %v", v.Refusals)
	}
}

// The cost list (Task 1.3) is enforced here, in the verdict, and nowhere else:
// a plan the guard and the cover both admit but that states a blocked cost
// attribute comes back not OK, with the cost gate's own refusal. Dropping the
// wiring from Gate turns this red.
func TestGateRefusesABlockedCostAttribute(t *testing.T) {
	const svc = "module.managed.google_cloud_run_v2_service.api"
	r := newRig(t)
	r.show(t, change(svc, "google_cloud_run_v2_service", []string{"create"}, map[string]any{
		"template": []any{map[string]any{"scaling": []any{map[string]any{"min_instance_count": 3}}}}}))
	v := gate(t, r, spec(t, r, admitAll{}))
	if v.OK {
		t.Fatal("a plan with a blocked cost attribute was admitted")
	}
	if len(v.Refusals) != 1 {
		t.Fatalf("refusals %v, want the cost gate's one", v.Refusals)
	}
	got := v.Refusals[0]
	if got.Gate != costGate || got.Address != svc || !strings.Contains(got.Why, "cloud-run-min-instances") {
		t.Errorf("refusal %+v, want the cost gate's refusal of %s with the entry's words", got, svc)
	}
	if applied := r.applied(t); len(applied) != 0 {
		t.Errorf("the gate applied something: %v", applied)
	}
}

// And the ordinary plan beside it: a service with min instances 0 fires no
// entry, so the cost gate does not refuse ordinary work.
func TestGateAdmitsAPlanBelowTheCostLines(t *testing.T) {
	r := newRig(t)
	r.show(t, change("module.managed.google_cloud_run_v2_service.api", "google_cloud_run_v2_service", []string{"create"}, map[string]any{
		"template": []any{map[string]any{"scaling": []any{map[string]any{"min_instance_count": 0}}}}}))
	if v := gate(t, r, spec(t, r, admitAll{})); !v.OK {
		t.Fatalf("a plan below the cost lines is refused: %v", v.Refusals)
	}
}

// A plan the guard refuses and that also states a blocked cost attribute
// returns every refusal, the cost one included: the "every refusal" contract.
func TestGateReturnsTheCostRefusalAlongsideTheGuards(t *testing.T) {
	const svc = "module.managed.google_cloud_run_v2_service.api"
	r := newRig(t)
	r.show(t,
		change(planBucket, "google_storage_bucket", []string{"delete"}, nil),
		change(svc, "google_cloud_run_v2_service", []string{"create"}, map[string]any{
			"template": []any{map[string]any{"scaling": []any{map[string]any{"min_instance_count": 3}}}}}))
	v := gate(t, r, spec(t, r, admitAll{}))
	var guarded, costed bool
	for _, ref := range v.Refusals {
		guarded = guarded || (ref.Gate == guardGate && ref.Address == planBucket)
		costed = costed || (ref.Gate == costGate && ref.Address == svc)
	}
	if v.OK || !guarded || !costed {
		t.Errorf("verdict %+v, want not OK with the guard's and the cost gate's refusals", v)
	}
}

// A flag-only finding is carried in Verdict.Flags, shown to whoever confirms,
// and never makes the plan not OK.
func TestGateCarriesAFlagOnlyFindingWithoutRefusing(t *testing.T) {
	const addr = "module.managed.google_cloud_run_v2_service.api"
	old := costFindings
	costFindings = func(*tf.Plan) []tf.ManagedCostFinding {
		return []tf.ManagedCostFinding{{Entry: "test-flag", Address: addr, Detail: "the size is large", Block: false}}
	}
	t.Cleanup(func() { costFindings = old })
	r := newRig(t)
	r.show(t, bucket(planBucket))
	v := gate(t, r, spec(t, r, admitAll{}))
	if !v.OK {
		t.Errorf("a flag-only finding made the plan not OK: %+v", v)
	}
	if len(v.Refusals) != 0 {
		t.Errorf("refusals %v, want none", v.Refusals)
	}
	if len(v.Flags) != 1 || v.Flags[0].Gate != costGate || v.Flags[0].Address != addr ||
		!strings.Contains(v.Flags[0].Why, "test-flag") {
		t.Errorf("flags %+v, want the finding", v.Flags)
	}
}

// The guard Gate calls carries no advice of its own (Task 3.2's review,
// finding 1): tf.Guard's sentence names the flag a managed apply refuses, and
// the sentence a managed refusal ends with is the apply's, chosen from the
// verdict. guardLines drops the header as well; this holds the source.
func TestGateGuardCarriesNoAdviceOfItsOwn(t *testing.T) {
	p := &tf.Plan{ResourceChanges: []tf.ResourceChange{{Address: planBucket, Change: tf.Change{Actions: []string{"delete"}}}}}
	err := guard(p, nil)
	if err == nil {
		t.Fatal("the guard admitted an unlisted delete")
	}
	if strings.Contains(err.Error(), "allow-delete") || strings.Contains(err.Error(), tf.AllowDeleteAdvice) {
		t.Errorf("the gate's guard carries tf.Guard's advice:\n%s", err)
	}
}
