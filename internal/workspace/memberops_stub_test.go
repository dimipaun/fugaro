package workspace

// C2's shape test: the member-operations contract's interfaces, the declared
// zero shape each stub answers, and the names the contract fixes. It is a test
// OF the stubs (memberops_stub.go), so I1 deletes it with them, in the change
// that wires the concrete sides onto the contract. Nothing else in the package
// names a stub: a task's own tests use fakes declared in its own `_test.go`
// files (the plan's conventions), which is what lets I1 delete the stubs without
// rewriting anything but this file.
//
// What the test does NOT do is check behaviour -- a stub has none. It holds the
// declared shapes: the exact set of methods each interface carries (a name
// added, removed or renamed here is a change to what WU5-WU24 may call), that
// every one of them answers the zero value of every type it declares, and the
// three strings the contract fixes by name.

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// c2Contract is one interface of C2 with the stub that implements it and the
// methods the contract declares, in the order reflect reports them (sorted).
type c2Contract struct {
	name    string
	iface   reflect.Type
	stub    any
	methods []string
}

func TestMemberOpsStubShapes(t *testing.T) {
	for _, c := range []c2Contract{
		{
			name:  "MemberOps",
			iface: reflect.TypeOf((*MemberOps)(nil)).Elem(),
			stub:  StubMemberOps{},
			methods: []string{
				"Audit", "Bindings", "Offboard", "PendingApply",
				"ReadGrants", "ReadMirror", "WriteGrants", "WriteMirror",
			},
		},
		{
			name:  "Authority",
			iface: reflect.TypeOf((*Authority)(nil)).Elem(),
			stub:  StubAuthority{},
			methods: []string{
				"AddAdmin", "AdminBindings", "Holds", "Kind",
				"Link", "RemoveAdmin", "Unlink",
			},
		},
		{
			name:    "StateWriter",
			iface:   reflect.TypeOf((*StateWriter)(nil)).Elem(),
			stub:    StubStateWriter{},
			methods: []string{"LastSaved", "Save"},
		},
		{
			name:    "CredentialProbe",
			iface:   reflect.TypeOf((*CredentialProbe)(nil)).Elem(),
			stub:    StubCredentialProbe{},
			methods: []string{"Probe"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for i := 0; i < c.iface.NumMethod(); i++ {
				got = append(got, c.iface.Method(i).Name)
			}
			if !slices.Equal(got, c.methods) {
				t.Fatalf("%s declares the methods %v; the contract declares %v", c.name, got, c.methods)
			}
			v := reflect.ValueOf(c.stub)
			for i := 0; i < c.iface.NumMethod(); i++ {
				name := c.iface.Method(i).Name
				method := v.MethodByName(name)
				if !method.IsValid() {
					// The `var _ Interface = Stub{}` assertion in memberops_stub.go
					// is what catches a missing method at build time; this is the
					// test saying the same thing where it is read.
					t.Fatalf("%s: the stub implements no %s", c.name, name)
				}
				c2ZeroShape(t, c.name+"."+name, method)
			}
		})
	}

	// Drift is a function type and not an interface: its declared shape is its
	// signature, which the stub's own `var _ Drift = StubDrift` holds, and what
	// this checks is the answer it gives.
	t.Run("Drift", func(t *testing.T) {
		var d Drift = StubDrift
		report, err := d(context.Background(), MemberTarget{Project: "project-a", Firebase: "project-a-firebase"})
		if err != nil || !reflect.DeepEqual(report, DriftReport{}) {
			t.Errorf("StubDrift answered (%#v, %v); the stub answers the declared zero shape (DriftReport{}, nil)", report, err)
		}
	})

	// The names the contract fixes are what a provider and a consumer agree on
	// without an edge between them: WU3's root writes the output, WU20's
	// authority grants with the account it names, and WU4, WU9 and WU12 write and
	// read the two member nodes.
	t.Run("fixed names", func(t *testing.T) {
		for _, want := range []struct{ what, got, fixed string }{
			{"the workspace IAM account's output", WorkspaceIAMAccountOutput, "workspace_iam_account"},
			{"the member's mirror node", PathMemberMirror, "config/admin/workspace-members"},
			{"the member's audit node", PathMemberAudit, "config/admin/workspace-audit"},
		} {
			if want.got != want.fixed {
				t.Errorf("%s is %q; the contract fixes %q", want.what, want.got, want.fixed)
			}
		}
		// Both member nodes sit under config/admin, where the member's own roster
		// does (internal/access's PathNode, which cannot be named from here: that
		// package reaches this one through internal/projectpolicy). Its
		// `.read: false` and `.write: false` are what keep every client token -- a
		// run's, a phone's, a signed-in user's -- out of the three, and a node
		// outside the fence would be readable by whoever the installation's rules
		// admit instead. It is the fence and not a secret that holds: IAM bypasses
		// it, and a member's own launchers, operators and viewers read their
		// member's mirror over IAM (threat-model.md §3).
		const fence = "config/admin/"
		for _, node := range []struct{ what, node string }{
			{"the member's mirror node", PathMemberMirror},
			{"the member's audit node", PathMemberAudit},
		} {
			if !strings.HasPrefix(node.node, fence) {
				t.Errorf("%s is %q, outside %s: the contract puts a member's nodes under the fence access.PathNode sits in", node.what, node.node, fence)
			}
		}
	})
}

// c2ZeroShape calls fn with a zero argument for every parameter and holds every
// result to the zero value of the type it declares. A nil context is the zero
// one and every stub ignores it: what is under test is the shape a caller can
// rely on, not what a call does.
func c2ZeroShape(t *testing.T, what string, fn reflect.Value) {
	t.Helper()
	ft := fn.Type()
	args := make([]reflect.Value, ft.NumIn())
	for i := range args {
		args[i] = reflect.Zero(ft.In(i))
	}
	got := fn.Call(args)
	for i, r := range got {
		want := reflect.Zero(ft.Out(i))
		if !reflect.DeepEqual(r.Interface(), want.Interface()) {
			t.Errorf("%s: result %d is %#v; the stub answers the declared zero shape %#v", what, i, r.Interface(), want.Interface())
		}
	}
}
