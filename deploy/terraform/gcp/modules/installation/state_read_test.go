// The front door's reach into the installation's database, held at the plan's
// own level (O16b, D2020 (a)): the state group reads the orchestrator's node as
// the front door's own account `fugaro-mcp`, and the account may read the
// database and change nothing in it — apply_patch is served by nothing (D2021),
// so the read is the whole of what the door reaches the state with.
//
// The tests read this module's own Terraform as text, the way
// coordinator_service_test.go's do, and for the same reason: the subject is the
// role's permission list and the binding's four parts, and a plan assertion
// only ever sees the inputs a test hands it. The two halves are the task's own:
//
//   - TestFrontDoorStateRoleHoldsNoDatabaseWrite is the named one. The role
//     holds firebasedatabase.instances.get and NOT .update, so a later change
//     that gave the front door the whole database fails here first.
//   - TestTheStateReadGrantIsTheFrontDoorsOwnAccountAndNoOneElses is the
//     binding: one grant, of that role, to that account, in the coordinator's
//     own Firebase project, under the front door's opt-in.
package installation

import (
	"regexp"
	"strings"
	"testing"
)

// stateReadTF is the file that carries the role and its one binding.
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

// TestFrontDoorStateRoleHoldsNoDatabaseWrite: the front door's own role on the
// installation's database is the instance's READ and nothing else, and that is
// the whole of D2020 (a)'s answer rather than one of its halves.
//
// Realtime Database IAM has no path-level scope, so the role is the only place
// the read/write direction can be decided: whatever this list holds, the
// account holds over the WHOLE database — every run's state, the policy, the
// kill switches and the credentials' own nodes included. The write is therefore
// not withheld by a handler or by a scope (`state:write` gates the catalogue,
// which registers no handler for apply_patch at all, D2021): it is withheld by
// the absence of the permission, so a later revision of the front door cannot
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
		t.Errorf("%s: the front door's database role holds %v, want exactly %v: RTDB IAM has no path-level scope, so every permission in this list is held over the whole database, and the front door reads the orchestrator's node and writes nothing (D2020 (a), D1221)\n%s", stateReadTF, got, want, role)
	}
	for _, never := range []string{"firebasedatabase.instances.update", "firebasedatabase.instances.create", "firebasedatabase.instances.delete", "firebasedatabase.instances.disable", "firebasedatabase.instances.reenable", "firebasedatabase.instances.undelete", "firebasedatabase.instances.list"} {
		if strings.Contains(role, never) {
			t.Errorf("%s: the front door's role names %s: the write (a node write is an update) and the instance's lifecycle keep the state group's writes with `fugaro orchestrator patch` and the person's own credential (D1223, D1241), and list is a reach no tool of this group makes", stateReadTF, never)
		}
	}
}

// TestTheStateReadGrantIsTheFrontDoorsOwnAccountAndNoOneElses: the grant is one
// binding of that role — the front door's own account, the coordinator's
// Firebase project (where the database lives, D6), no condition, and the front
// door's own opt-in.
//
// Each of the four parts is a way the grant could be wider than D2020 (a)
// answers. Another member is another identity reading every node of the
// installation's database; a condition is refused rather than accepted because
// RTDB cannot express a path prefix in IAM, so one would read like a fence and
// not be one; a project other than the coordinator's own is a role declared
// where the database is not; and an unguarded resource would hand a database
// read to an installation that never asked for a front door (D934).
//
// The mutations this fails on: a second member, a condition block, the
// installation's project instead of the coordinator's Firebase project, a
// count that is not the opt-in, and the role's name written as a reference
// instead of the literal.
func TestTheStateReadGrantIsTheFrontDoorsOwnAccountAndNoOneElses(t *testing.T) {
	src := tfSource(t, stateReadTF)
	role := tfResource(t, src, "google_project_iam_custom_role", "state_read")
	grant := tfResource(t, src, "google_project_iam_member", "state_read")

	if !regexp.MustCompile(`(?m)^\s*count\s*=\s*local\.mcp_server_on\b`).MatchString(role) {
		t.Errorf("%s: the role is not created on the front door's opt-in and on nothing else:\n%s", stateReadTF, role)
	}
	for _, want := range []struct{ what, re string }{
		{"the front door's opt-in", `(?m)^\s*count\s*=\s*local\.mcp_server_on\b`},
		{"the coordinator's Firebase project, where the database lives", `(?m)^\s*project\s*=\s*var\.coordinator\.firebase_project\s*$`},
		{"the role as a plan-time literal", `(?m)^\s*role\s*=\s*"projects/\$\{var\.coordinator\.firebase_project\}/roles/fugaroStateRead"\s*$`},
		{"the front door's own account as the member", `(?m)^\s*member\s*=\s*google_service_account\.mcp_server\[0\]\.member\s*$`},
	} {
		if !regexp.MustCompile(want.re).MatchString(grant) {
			t.Errorf("%s: the state-read binding sets no %s (D2020 (a): the front door's own account, on the coordinator's own project, under the front door's own opt-in):\n%s", stateReadTF, want.what, grant)
		}
	}
	if strings.Contains(grant, "condition") {
		t.Errorf("%s: the state-read binding carries a condition: RTDB IAM cannot express a path prefix, so a condition here would look like a fence and not be one — what narrows this role is that it holds the read alone:\n%s", stateReadTF, grant)
	}
	if strings.Contains(grant, "google_service_account.coordinator") {
		t.Errorf("%s: the state-read binding names the sweep's own account: the sweep reads runs and writes the database through the Firebase root's admin role, and the front door's read is the front door's own (D920, D935):\n%s", stateReadTF, grant)
	}

	// One grant of the role, in this file and in the module: a second binding —
	// under another member, in another project, with a condition — is a reader
	// nobody reviewed, and the module's other files may not carry one either.
	if got := strings.Count(src, "roles/fugaroStateRead"); got != 1 {
		t.Errorf("%s names %s %d times, want exactly 1 (the binding's role): the role is granted once, to the front door's account", stateReadTF, "fugaroStateRead", got)
	}
	files := tfFiles(t)
	if named := tfFilesNaming(t, files, "fugaroStateRead", "state_read", "google_project_iam_member.state_read"); len(named) != 1 || named[0] != stateReadTF {
		t.Errorf("the state-read role and its binding are named by %v, want exactly [%s]: every appearance elsewhere is a grant of the database's read this design has not decided", named, stateReadTF)
	}
}
