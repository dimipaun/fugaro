package cli

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The property this feature's safety rests on, and the one place it is
// asserted once and exhaustively rather than per task: **a project that is
// not managed never proceeds without its confirmation, and an applying step
// in a coding agent's session in a project that is not managed never proceeds
// at all.** D55's answer of 2026-10-16 removes the refusal for a managed
// project and for nothing else, so this is the direction that must not move:
// the change that would be catastrophic is not a managed project applying the
// wrong thing, it is an ordinary project quietly stopping asking.
//
// It is a test of the program's decision and not of a UI, so it needs no
// cloud, no terminal and no run: the confirmation a step needs is
// initflow.CanConfirm's over initflow.Conditions (the flags, the terminal and
// the coding agent's marker, exactly the fields initRun.conditions builds
// from), and the only thing that may ever replace a confirmation is the
// managed-mode read of internal/infra/managed (Task 0.2), whose every answer
// is taken from the read itself over internal/gcpfake. The product is
// enumerated — every combination of the conditions, both confirmation classes
// and every answer the read can give — and never sampled, and the enumeration
// fails the day it stops matching the declarations it is built from: a new
// field on initflow.Conditions, a new ConfirmClass, or a coding agent's
// marker added to agentMarkers.
//
// Task 3.1 extends the property with the managed direction — a managed
// project may apply, but only what the deterministic gate admitted — and
// internal/initflow/stage.go gains the managed case in the same commit. Until
// then nothing here skips anything, and the predicate below is where the rule
// the apply path must use is written down.

// conditionsFields names the boolean conditions the product enumerates, in
// the order the mask below assigns their bits. Agent is a string rather than
// a flag, and is enumerated separately: every marker in agentMarkers, and
// none.
var conditionsFields = []string{"Yes", "NonInteractive", "JSON", "Terminal"}

// conditionsFromMask is one point of that product: bit i of mask is field i
// of conditionsFields, and a coding agent's marker is left to the caller.
func conditionsFromMask(mask int) initflow.Conditions {
	return initflow.Conditions{
		Yes:            mask&1 != 0,
		NonInteractive: mask&2 != 0,
		JSON:           mask&4 != 0,
		Terminal:       mask&8 != 0,
	}
}

// allConditions is every combination of the conditions a run can be in:
// 2^len(conditionsFields) rows of flags, each under every coding agent's
// marker and under none. Enumerated rather than sampled, because the whole
// claim is that no combination is exempt.
func allConditions() []initflow.Conditions {
	agents := append([]string{""}, agentMarkers...)
	out := make([]initflow.Conditions, 0, (1<<len(conditionsFields))*len(agents))
	for mask := 0; mask < 1<<len(conditionsFields); mask++ {
		c := conditionsFromMask(mask)
		for _, agent := range agents {
			c.Agent = agent
			out = append(out, c)
		}
	}
	return out
}

// confirmClasses is every confirmation class the product runs over, by the
// name internal/initflow/stage.go declares it under.
var confirmClasses = []struct {
	name  string
	class initflow.ConfirmClass
}{
	{"YesOrTyped", initflow.YesOrTyped},
	{"Typed", initflow.Typed},
}

// declaredConfirmClasses reads the ConfirmClass constants out of
// internal/initflow/stage.go, in declaration order.
//
// Go cannot enumerate a constant set at run time, and the product above must
// not be narrowed silently when one is added — a class with no case in it is
// a class whose confirmations the property stops covering — so the
// declaration itself is the completeness check's source of truth. The file is
// this package's neighbour and is read by path, the way this repository's
// docs tests read the documents they pin.
func declaredConfirmClasses(t *testing.T) []string {
	t.Helper()
	const src = "../initflow/stage.go"
	f, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}
	var names []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		// A const spec with neither a type nor a value repeats the one before
		// it, which is how `Typed` follows `YesOrTyped ConfirmClass = iota`:
		// the block is tracked rather than each spec, or the second class
		// would be missed and the check would pass while enumerating half the
		// type.
		var class bool
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				id, ok := vs.Type.(*ast.Ident)
				class = ok && id.Name == "ConfirmClass"
			}
			if class {
				for _, n := range vs.Names {
					names = append(names, n.Name)
				}
			}
		}
	}
	return names
}

// managedRead is one answer the marker read can give, as a call site sees it:
// the three results of managed.IsManaged together, and the state that
// produced them, for a failure message a person can reproduce.
type managedRead struct {
	state  string
	ok     bool
	reason string
	err    error
}

// The installation the reads below are asked about. The Fugaro project's name
// is deliberately neither the GCP project's nor a substring of the bucket's,
// so a reason that names one is saying something rather than matching.
const (
	managedPropertyProject = "sandbox"
	managedPropertyGCP     = "fugaro-managed-property"
	managedPropertyNumber  = 424242
)

// managedPropertyBucket is where the read looks: the name the installation
// gives its runs bucket, and the one init's marker write insists on.
func managedPropertyBucket() string { return "fugaro-runs-" + managedPropertyGCP }

// managedPropertyWorld is one state the installation can be in, as the read's
// two clients see it.
type managedPropertyWorld struct {
	name  string
	build func(t *testing.T) (*storage.Service, *crm.Service)
}

// managedPropertyWorlds is every state the read has an answer for: the fact
// present and believed, and each way of not being managed — no fact, one that
// cannot be read, one about another project, a bucket that is not this
// project's, no bucket at all, and a read that failed outright.
func managedPropertyWorlds(t *testing.T) []managedPropertyWorld {
	t.Helper()
	bucket := managedPropertyBucket()
	labels := map[string]string{"fugaro": "managed"}
	fact := func(gcpProject string) []byte {
		return []byte(`{"version":1,"gcp_project":"` + gcpProject + `"}`)
	}
	return []managedPropertyWorld{
		{
			name: "the creation fact is there",
			build: func(t *testing.T) (*storage.Service, *crm.Service) {
				f := gcpfake.NewGCS(t)
				f.AddBucket(bucket, managedPropertyNumber, labels)
				f.Put(bucket, managed.MarkerObject, fact(managedPropertyGCP))
				cf := gcpfake.NewCRM(t)
				cf.AddProject(managedPropertyGCP, managedPropertyNumber)
				return managedStorage(t, f), managedCRM(t, cf)
			},
		},
		{
			name: "no creation fact at all",
			build: func(t *testing.T) (*storage.Service, *crm.Service) {
				f := gcpfake.NewGCS(t)
				f.AddBucket(bucket, managedPropertyNumber, labels)
				cf := gcpfake.NewCRM(t)
				cf.AddProject(managedPropertyGCP, managedPropertyNumber)
				return managedStorage(t, f), managedCRM(t, cf)
			},
		},
		{
			name: "a creation fact that cannot be read",
			build: func(t *testing.T) (*storage.Service, *crm.Service) {
				f := gcpfake.NewGCS(t)
				f.AddBucket(bucket, managedPropertyNumber, labels)
				f.Put(bucket, managed.MarkerObject, fact(managedPropertyGCP))
				f.RefusePath(managed.MarkerObject, http.StatusForbidden, "PERMISSION_DENIED", "", "no")
				cf := gcpfake.NewCRM(t)
				cf.AddProject(managedPropertyGCP, managedPropertyNumber)
				return managedStorage(t, f), managedCRM(t, cf)
			},
		},
		{
			name: "a creation fact about another project",
			build: func(t *testing.T) (*storage.Service, *crm.Service) {
				f := gcpfake.NewGCS(t)
				f.AddBucket(bucket, managedPropertyNumber, labels)
				f.Put(bucket, managed.MarkerObject, fact("someone-elses-project"))
				cf := gcpfake.NewCRM(t)
				cf.AddProject(managedPropertyGCP, managedPropertyNumber)
				return managedStorage(t, f), managedCRM(t, cf)
			},
		},
		{
			name: "a runs bucket in another project",
			build: func(t *testing.T) (*storage.Service, *crm.Service) {
				f := gcpfake.NewGCS(t)
				f.AddBucket(bucket, managedPropertyNumber+1, labels)
				f.Put(bucket, managed.MarkerObject, fact(managedPropertyGCP))
				cf := gcpfake.NewCRM(t)
				cf.AddProject(managedPropertyGCP, managedPropertyNumber)
				return managedStorage(t, f), managedCRM(t, cf)
			},
		},
		{
			name: "no runs bucket at all",
			build: func(t *testing.T) (*storage.Service, *crm.Service) {
				f := gcpfake.NewGCS(t)
				cf := gcpfake.NewCRM(t)
				cf.AddProject(managedPropertyGCP, managedPropertyNumber)
				return managedStorage(t, f), managedCRM(t, cf)
			},
		},
		{
			name: "a read that failed",
			build: func(t *testing.T) (*storage.Service, *crm.Service) {
				f := gcpfake.NewGCS(t)
				f.AddBucket(bucket, managedPropertyNumber, labels)
				f.Put(bucket, managed.MarkerObject, fact(managedPropertyGCP))
				f.Refuse(http.StatusInternalServerError, "INTERNAL", "", "backend error")
				cf := gcpfake.NewCRM(t)
				cf.AddProject(managedPropertyGCP, managedPropertyNumber)
				return managedStorage(t, f), managedCRM(t, cf)
			},
		},
	}
}

// managedReads is what managed.IsManaged answers in each of those states,
// taken from the read itself — no live cloud: internal/gcpfake serves it —
// plus one answer the read never gives, and which the predicate still has to
// refuse.
func managedReads(t *testing.T) []managedRead {
	t.Helper()
	var out []managedRead
	for _, w := range managedPropertyWorlds(t) {
		s, c := w.build(t)
		ok, reason, err := managed.IsManaged(context.Background(), s, c, managedPropertyProject, managedPropertyGCP)
		out = append(out, managedRead{state: w.name, ok: ok, reason: reason, err: err})
	}
	out = append(out, managedRead{
		state: "a read that answered managed and then failed", ok: true, err: errors.New("the read failed"),
	})
	return out
}

// managedStorage and managedCRM are the read's two clients over a fake, the
// way internal/infra/managed's own test builds them.
func managedStorage(t *testing.T, f *gcpfake.GCS) *storage.Service {
	t.Helper()
	s, err := storage.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/storage/v1/"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func managedCRM(t *testing.T, f *gcpfake.CRM) *crm.Service {
	t.Helper()
	s, err := crm.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// managedMode is the rule the apply path must use for the three results of
// managed.IsManaged: a confirmation may be replaced only by a fact, so only
// by a read that answered true and did not fail. The reason is never a
// signal — it is what a person is told, and it is empty only when there was
// nothing to say — and an error is never a reason to proceed: a read that
// failed answers "not managed" in the fail-closed direction (Global
// Constraint 3).
//
// Task 3.1 wires this rule into the decision for a managed project, and
// internal/initflow/stage.go gains the managed case there. Until then this
// file is where the rule is written down, and the product below is what that
// case has to keep true.
func managedMode(ok bool, reason string, err error) bool { return ok && err == nil }

// nonManaged is whether a read's answer means "not managed", by the read's
// own contract rather than by whichever predicate is being tested: the fact
// is there only when the read answered true and did not fail. Everything else
// — no bucket, another project's bucket, an absent object, one that cannot be
// read or cannot be parsed, a version or a project this build does not know,
// an API that did not answer — is a project nothing recorded.
func nonManaged(r managedRead) bool { return !(r.ok && r.err == nil) }

// managedViolations returns every combination in which mode breaks the
// property: the confirmation of a project that is not managed skipped, or a
// coding agent's session confirming anything at all. It is empty for the rule
// above, and it is what catches each mutation the test below feeds it.
func managedViolations(mode func(ok bool, reason string, err error) bool, reads []managedRead) []string {
	var bad []string
	for _, class := range confirmClasses {
		for _, c := range allConditions() {
			// An agent's environment marker confirms nothing, whatever else
			// is set — --yes included.
			if c.Agent != "" && initflow.CanConfirm(class.class, c) {
				bad = append(bad, fmt.Sprintf("%s is confirmed in a coding agent's session (%s)", class.name, conditionsString(c)))
			}
			for _, r := range reads {
				// The managed direction is Task 3.1's; what is asserted here
				// is that a read that is not managed changes nothing at all.
				if !nonManaged(r) {
					continue
				}
				if mode(r.ok, r.reason, r.err) {
					bad = append(bad, fmt.Sprintf("%s skips its confirmation with %s (%s) although it is not managed: %s",
						class.name, conditionsString(c), r.state, r.reason))
				}
			}
		}
	}
	for _, c := range allConditions() {
		// The run's own confirmation — the project's name typed once for all
		// the ordinary steps — is a Typed one, so an agent's session never
		// takes it either.
		if c.Agent != "" && initflow.CanConfirmRun(c) {
			bad = append(bad, fmt.Sprintf("the run's one confirmation is taken in a coding agent's session (%s)", conditionsString(c)))
		}
	}
	return bad
}

// conditionsString spells a run's conditions for a failure message: every
// field of initflow.Conditions, whatever the enumeration above holds.
func conditionsString(c initflow.Conditions) string {
	var b strings.Builder
	v := reflect.ValueOf(c)
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s=%v", typ.Field(i).Name, v.Field(i).Interface())
	}
	return b.String()
}

// The property itself, over the whole product: no combination of the
// conditions, of the two confirmation classes and of the answers the marker
// read can give lets a project that is not managed proceed without its
// confirmation, and none lets a coding agent's session proceed at all.
//
// The table is asserted to be worth testing before it is used: a row set that
// never produced a managed answer would make the managed half vacuous, and
// one that never produced a refusal would make the other half vacuous — the
// failure mode an earlier fixture corpus in this repository had, where every
// row passed for a reason nobody had checked.
func TestManagedNonManagedProjectNeverSkipsAConfirmation(t *testing.T) {
	conditions, reads := allConditions(), managedReads(t)
	if want := (1 << len(conditionsFields)) * (len(agentMarkers) + 1); len(conditions) != want {
		t.Fatalf("the product has %d condition sets, want %d: the enumeration is not the product", len(conditions), want)
	}
	var managedRows, notManaged int
	for _, r := range reads {
		if nonManaged(r) {
			notManaged++
		} else {
			managedRows++
		}
	}
	if managedRows == 0 || notManaged == 0 {
		t.Fatalf("the marker read answered %d managed and %d not managed: the property needs both, or it tests one direction", managedRows, notManaged)
	}

	if bad := managedViolations(managedMode, reads); len(bad) != 0 {
		t.Fatalf("a project that is not managed changed its decision in %d combination(s):\n%s", len(bad), firstFew(bad))
	}
}

// The property has to have teeth. Each predicate below is a mutation of the
// rule above that would skip the confirmation of a project nothing recorded,
// and each must be reported: the day the property stops reporting one of
// them, it has stopped testing anything. (The other direction is deliberately
// not tested — a predicate that never skips is fail-closed, which is the
// direction Global Constraint 3 asks for.)
func TestManagedPropertyCatchesAWrongPredicate(t *testing.T) {
	reads := managedReads(t)
	for _, tc := range []struct {
		name string
		why  string
		mode func(ok bool, reason string, err error) bool
	}{
		{
			name: "an answer with no error is managed",
			why:  "the fact that could not be read answers false with no error, and that is a refusal, not a fact",
			mode: func(ok bool, reason string, err error) bool { return err == nil },
		},
		{
			name: "every answer is managed",
			why:  "a read that failed, or one about another project, is not a fact either",
			mode: func(ok bool, reason string, err error) bool { return true },
		},
		{
			name: "the read's first result alone is managed",
			why:  "managed is true only with a nil error, and a caller that drops the error skips the confirmation on a failed read",
			mode: func(ok bool, reason string, err error) bool { return ok },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := managedViolations(tc.mode, reads)
			if len(bad) == 0 {
				t.Fatalf("the property accepted a predicate that reads a project nothing recorded as managed: %s", tc.why)
			}
		})
	}
}

// firstFew is the head of a violation list: the first failure is the one that
// explains the rest, and a product of two thousand combinations can produce
// two thousand of them.
func firstFew(bad []string) string {
	const most = 5
	if len(bad) > most {
		return strings.Join(bad[:most], "\n") + fmt.Sprintf("\n... and %d more", len(bad)-most)
	}
	return strings.Join(bad, "\n")
}

// A condition added to initflow.Conditions is a condition the product does
// not enumerate, and a property that does not enumerate every condition is
// the property this feature cannot afford. The fields are compared to the
// ones the mask builds, and the mask is checked field by field, so the
// enumeration cannot drift from the type it claims to cover.
func TestManagedConditionEnumerationIsComplete(t *testing.T) {
	c := initflow.Conditions{}
	typ := reflect.TypeOf(c)
	declared := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		declared = append(declared, typ.Field(i).Name)
	}
	want := append(append([]string{}, conditionsFields...), "Agent")
	if !slices.Equal(declared, want) {
		t.Fatalf("initflow.Conditions is %v and the product enumerates %v: a condition with no case here is a condition the property does not cover", declared, want)
	}

	// Bit i of the mask is field i, and Agent is left to the caller: the type
	// order and the mask order have to be the same order, or the product is
	// enumerating something other than the fields it names.
	for i := range conditionsFields {
		v := reflect.ValueOf(conditionsFromMask(1 << i))
		for j := range conditionsFields {
			if got, want := v.Field(j).Bool(), j == i; got != want {
				t.Errorf("conditionsFromMask(1<<%d) has %s=%v, want %v", i, typ.Field(j).Name, got, want)
			}
		}
		if agent := v.Field(len(conditionsFields)).String(); agent != "" {
			t.Errorf("conditionsFromMask(1<<%d) sets Agent=%q: the mask builds the flags, and the product sets the marker", i, agent)
		}
	}

	// Every marker a coding agent's session can set is in the product, and so
	// is the absence of one.
	seen := map[string]bool{}
	for _, c := range allConditions() {
		seen[c.Agent] = true
	}
	for _, marker := range append([]string{""}, agentMarkers...) {
		if !seen[marker] {
			t.Errorf("no combination of the product has Agent=%q, so a marker agentMarkers names is never exercised", marker)
		}
	}
}

// The same, for the confirmation classes: the enumeration is checked against
// the declaration in internal/initflow/stage.go, so a class added there fails
// here until it has a case in the product, and the values are checked to be
// the declaration's own iota order.
func TestManagedConfirmClassEnumerationIsComplete(t *testing.T) {
	declared := declaredConfirmClasses(t)
	got := make([]string, 0, len(confirmClasses))
	for _, c := range confirmClasses {
		got = append(got, c.name)
	}
	if !slices.Equal(declared, got) {
		t.Fatalf("initflow declares the confirmation classes %v and the product enumerates %v: a class with no case here is a class whose confirmations the property does not cover", declared, got)
	}
	for i, c := range confirmClasses {
		if int(c.class) != i {
			t.Errorf("the product's %s is value %d, and a class declared %d place(s) into an iota run is %d", c.name, int(c.class), i, i)
		}
	}
}
