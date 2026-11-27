// The state readers' reach into the installation's database, held at the plan's
// own level (P5.1c's finding A for the resident orchestrator; O16b, D2020 (a)
// for the front door; P5.3.3 for the voice hop): the tick reads
// orchestrator/<id>/{state,lease} as the orchestrator's own account
// `fugaro-orch`, the front door reads the same node as its own account
// `fugaro-mcp`, and the hop reads the device node and the project's voice policy
// as its own account, and none of the three may change anything in the database
// — apply_patch is served by nothing (D2021), so the read is the whole of what
// any of them reaches the state with.
//
// The tests read this module's own Terraform as text, the way
// coordinator_service_test.go's do, and for the same reason: the subject is the
// role's permission list and each binding's four parts, and a plan assertion
// only ever sees the inputs a test hands it. The two halves are the task's own:
//
//   - TestFrontDoorStateRoleHoldsNoDatabaseWrite is the named one. The role
//     holds firebasedatabase.instances.get and NOT .update, so a later change
//     that gave any reader the whole database fails here first.
//   - TestTheStateReadGrantIsTheThreeReadersOwnAccountsAndNoOneElses is the
//     bindings: one role, created when any reader is on, granted three times —
//     once to the front door's own account under the door's opt-in, once to the
//     hop's under the hop's, and once to the resident orchestrator's under the
//     orchestrator's (that one in orchestrator.tf, section 4.3's own file) —
//     and to no other member in this module.
package installation

import (
	"regexp"
	"strings"
	"testing"
)

// stateReadTF is the file that carries the role and its two bindings.
const stateReadTF = "state_read.tf"

// tfPermissions reads a custom role block's permission list out of a file's
// source, comments already stripped by tfSource.
func tfPermissions(t *testing.T, src, resource string) []string {
	t.Helper()
	block := tfResource(t, src, "google_project_iam_custom_role", resource)
	m := regexp.MustCompile(`(?s)permissions\s*=\s*\[([^\]]*)\]`).FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("%s: %s declares no permissions list, so the role would be created with none and every read of the node would be refused", stateReadTF, resource)
	}
	var out []string
	for _, s := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, s[1])
	}
	return out
}

// TestFrontDoorStateRoleHoldsNoDatabaseWrite: the readers' role on the
// installation's database is the instance's READ and nothing else, and that is
// the whole of D2020 (a)'s answer rather than one of its halves.
//
// Realtime Database IAM has no path-level scope, so the role is the only place
// the read/write direction can be decided: whatever this list holds, all three
// readers hold over the WHOLE database — every run's state, the policy, the
// kill switches and the credentials' own nodes included. The write is therefore
// not withheld by a handler or by a scope (`state:write` gates the catalogue,
// which registers no handler for apply_patch at all, D2021): it is withheld by
// the absence of the permission, so a later revision of any reader cannot
// reach for it. firebasedatabase.instances.update is the only write — a node
// write is an update, and there is no separate create or delete — so it is the
// permission this test exists for.
//
// The mutations this fails on: instances.update added to the list (the one the
// task's own mutation check runs), any second permission beside the read, and a
// permissions list that is not a literal (a variable, local or concat, which
// could carry the write into the plan unread).
func TestFrontDoorStateRoleHoldsNoDatabaseWrite(t *testing.T) {
	src := tfSource(t, stateReadTF)
	role := tfResource(t, src, "google_project_iam_custom_role", "state_read")

	if !regexp.MustCompile(`(?m)^\s*role_id\s*=\s*"fugaroStateRead"\s*$`).MatchString(role) {
		t.Errorf("%s: the role's ID is not the literal fugaroStateRead: internal/infra/tf/cover.go pins this role's permissions by that ID, and a role the review cannot name is a role it cannot refuse a widening of:\n%s", stateReadTF, role)
	}
	got := tfPermissions(t, src, "state_read")
	want := []string{"firebasedatabase.instances.get"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("%s: the database role holds %v, want exactly %v: RTDB IAM has no path-level scope, so every permission in this list is held over the whole database, and both readers read and write nothing (D2020 (a), D1221)\n%s", stateReadTF, got, want, role)
	}
	for _, never := range []string{"firebasedatabase.instances.update", "firebasedatabase.instances.create", "firebasedatabase.instances.delete", "firebasedatabase.instances.disable", "firebasedatabase.instances.reenable", "firebasedatabase.instances.undelete", "firebasedatabase.instances.list"} {
		if strings.Contains(role, never) {
			t.Errorf("%s: the role names %s: the write (a node write is an update) and the instance's lifecycle keep the state group's writes with `fugaro orchestrator patch` and the person's own credential (D1223, D1241), and list is a reach no reader of this role makes", stateReadTF, never)
		}
	}
}

// TestTheStateReadGrantIsTheThreeReadersOwnAccountsAndNoOneElses: the grant is
// one role — created when the resident orchestrator, the front door OR the hop
// is on, and never on one switch alone, because a binding whose role the same
// plan does not create is a plan that cannot apply — and it is bound exactly
// three times: the front door's own account under the door's opt-in, the hop's
// own account under the hop's, and the resident orchestrator's own account
// under the orchestrator's (the last of them in orchestrator.tf, because
// section 4.3's table is that file's alone).
//
// Each of the parts is a way the grant could be wider than D2020 (a), P5.3.3
// and P5.1c answer. Another member is another identity reading every node of
// the installation's database; a condition is refused rather than accepted
// because RTDB cannot express a path prefix in IAM, so one would read like a
// fence and not be one; a project other than the database's own is a role
// declared where the database is not (the local is what names it, and it is the
// same expression on the role and on every binding, or a binding would grant a
// role of a project the role is not in); and a binding that outlived its
// reader's opt-in would hand a database read to an installation that never
// asked for that reader.
//
// The mutations this fails on: a fourth member, a condition block, the
// installation's project instead of the database's, a role created on one
// switch instead of the three, a binding on another reader's switch, and the
// role's name written as a reference instead of the literal local.
func TestTheStateReadGrantIsTheThreeReadersOwnAccountsAndNoOneElses(t *testing.T) {
	src := tfSource(t, stateReadTF)
	role := tfResource(t, src, "google_project_iam_custom_role", "state_read")
	frontDoor := tfResource(t, src, "google_project_iam_member", "state_read")
	voice := tfResource(t, src, "google_project_iam_member", "voice_state_read")

	for _, want := range []struct{ what, re string }{
		{"the three opt-ins that make it, so no reader binds a role its own plan does not create", `(?m)^\s*count\s*=\s*local\.mcp_server_on\s*\|\|\s*local\.voice_on\s*\|\|\s*local\.orchestrator_on\s*\?\s*1\s*:\s*0\s*$`},
		{"the database's own project, named by the local the bindings name too", `(?m)^\s*project\s*=\s*local\.state_read_firebase_project\s*$`},
	} {
		if !regexp.MustCompile(want.re).MatchString(role) {
			t.Errorf("%s: the state-read role sets no %s:\n%s", stateReadTF, want.what, role)
		}
	}
	for _, want := range []struct{ what, re string }{
		{"the front door's opt-in and not the hop's", `(?m)^\s*count\s*=\s*local\.mcp_server_on\s*\?\s*1\s*:\s*0\s*$`},
		{"the database's own project, named by the local", `(?m)^\s*project\s*=\s*local\.state_read_firebase_project\s*$`},
		{"the role as a plan-time literal", `(?m)^\s*role\s*=\s*"projects/\$\{local\.state_read_firebase_project\}/roles/fugaroStateRead"\s*$`},
		{"the front door's own account as the member", `(?m)^\s*member\s*=\s*google_service_account\.mcp_server\[0\]\.member\s*$`},
		{"the role as its dependency", `(?m)^\s*depends_on\s*=\s*\[google_project_iam_custom_role\.state_read\]\s*$`},
	} {
		if !regexp.MustCompile(want.re).MatchString(frontDoor) {
			t.Errorf("%s: the front door's state-read binding sets no %s (D2020 (a): the front door's own account, on the database's own project, under the front door's own opt-in):\n%s", stateReadTF, want.what, frontDoor)
		}
	}
	for _, want := range []struct{ what, re string }{
		{"the hop's opt-in and not the front door's", `(?m)^\s*count\s*=\s*local\.voice_on\s*\?\s*1\s*:\s*0\s*$`},
		{"the database's own project, named by the local", `(?m)^\s*project\s*=\s*local\.state_read_firebase_project\s*$`},
		{"the same role as a plan-time literal", `(?m)^\s*role\s*=\s*"projects/\$\{local\.state_read_firebase_project\}/roles/fugaroStateRead"\s*$`},
		{"the hop's own account as the member", `(?m)^\s*member\s*=\s*google_service_account\.voice\[0\]\.member\s*$`},
		{"the role as its dependency", `(?m)^\s*depends_on\s*=\s*\[google_project_iam_custom_role\.state_read\]\s*$`},
	} {
		if !regexp.MustCompile(want.re).MatchString(voice) {
			t.Errorf("%s: the voice hop's state-read binding sets no %s (P5.3.3: the hop's own account, on the same database's project, under the hop's own opt-in):\n%s", stateReadTF, want.what, voice)
		}
	}
	// The orchestrator's own binding is section 4.3's file's (orchestrator.tf),
	// because that table may be named by no other file: the role and the project
	// are the same two expressions, and the member is that account's own written
	// out address.
	orchestrator := tfBlock(tfSource(t, orchestratorTF), "google_project_iam_member", "orchestrator_state_read")
	for _, want := range []struct{ what, re string }{
		{"the orchestrator's opt-in and not the other readers'", `(?m)^\s*count\s*=\s*local\.orchestrator_on\s*\?\s*1\s*:\s*0\s*$`},
		{"the database's own project, named by the local", `(?m)^\s*project\s*=\s*local\.state_read_firebase_project\s*$`},
		{"the same role as a plan-time literal", `(?m)^\s*role\s*=\s*"projects/\$\{local\.state_read_firebase_project\}/roles/fugaroStateRead"\s*$`},
		{"the orchestrator's own account as the member", `(?m)^\s*member\s*=\s*` + regexp.QuoteMeta(orchestratorMemberExpr) + `\s*$`},
		{"the role as its dependency", `(?m)^\s*depends_on\s*=\s*\[google_service_account\.orchestrator, google_project_iam_custom_role\.state_read\]\s*$`},
	} {
		if !regexp.MustCompile(want.re).MatchString(orchestrator) {
			t.Errorf("%s: the resident orchestrator's state-read binding sets no %s (P5.1c's finding A: the orchestrator's own account, on the database's own project, under the orchestrator's own opt-in):\n%s", orchestratorTF, want.what, orchestrator)
		}
	}
	for _, b := range []struct {
		who   string
		block string
	}{
		{"the front door's state-read binding", frontDoor},
		{"the voice hop's state-read binding", voice},
		{"the resident orchestrator's state-read binding", orchestrator},
	} {
		if strings.Contains(b.block, "condition") {
			t.Errorf("%s: %s carries a condition: RTDB IAM cannot express a path prefix, so a condition here would look like a fence and not be one — what narrows this role is that it holds the read alone:\n%s", stateReadTF, b.who, b.block)
		}
		if strings.Contains(b.block, "google_service_account.coordinator") {
			t.Errorf("%s: %s names the sweep's own account: the sweep reads runs and writes the database through the Firebase root's admin role, and the front door's read is the front door's own (D920, D935):\n%s", stateReadTF, b.who, b.block)
		}
	}

	// One role, bound exactly twice in this file and once more in the module:
	// another binding — another member, another project, a condition — is a
	// reader nobody reviewed, and the module's other files may not carry one
	// either.
	if got := strings.Count(src, "roles/fugaroStateRead"); got != 2 {
		t.Errorf("%s names %s %d times, want exactly 2 (the front door's binding and the hop's): the role is granted once to each reader's own account in this file, and a third appearance here is a reader this design has not decided", stateReadTF, "fugaroStateRead", got)
	}
	if got := strings.Count(tfSource(t, orchestratorTF), "roles/fugaroStateRead"); got != 1 {
		t.Errorf("%s names %s %d times, want exactly 1 (the resident orchestrator's binding): the account reads this installation's database through the one role this tree declares for the node, and a second appearance is a grant the design has not decided", orchestratorTF, "fugaroStateRead", got)
	}
	files := tfFiles(t)
	if named := tfFilesNaming(t, files, "fugaroStateRead", "state_read", "google_project_iam_member.state_read", "google_project_iam_member.voice_state_read", "google_project_iam_member.orchestrator_state_read"); len(named) != 2 || named[0] != orchestratorTF || named[1] != stateReadTF {
		t.Errorf("the state-read role and its bindings are named by %v, want exactly [%s %s]: every appearance elsewhere is a grant of the database's read this design has not decided", named, orchestratorTF, stateReadTF)
	}
}
