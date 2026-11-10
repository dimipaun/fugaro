package tf

// W7's Terraform half: the ONE grant a member's own `fugaro update` makes so
// that the workspace's credentials service can read the member's ledger
// (docs/workspaces/spec.md §5, O1's answer; task W7 of
// docs/workspaces/implementation-plan.md).
//
// These are text checks against the module, because what matters — which
// principal, which role, on what, and under which guard — is what a reviewer
// has to read, and a plan assertion only ever sees the inputs a test hands it.
// The two named tests are the task's own:
//
//   - TestMemberUpdateBindsTheWorkspaceGrantToThePinnedAccount: one binding,
//     of the read-only role, to the account the member's own config pins, and
//     none at all for a config that pins no account.
//   - TestMemberUpdateBindsNoWriteOnTheMembersDatabase: the role holds
//     firebasedatabase.instances.get and NOT .update, so a later change that
//     added the write back fails here first.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// wsModuleDir is the Firebase module, whose workspace_access.tf holds the
// member's binding. The tf package cannot import internal/infra (which imports
// it), so the Go side of the same grant is pinned by
// TestWorkspaceAccessGoAndModuleAgree below rather than by a call into it.
var wsModuleDir = filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules", "firebase")

var (
	wsBlockRE = regexp.MustCompile(`(?s)resource "([a-z0-9_]+)" "([a-z0-9_]+)" \{(.*?)\n\}`)
	wsVarRE   = regexp.MustCompile(`(?m)^variable "([a-z0-9_]+)"`)
	wsRefRE   = regexp.MustCompile(`var\.([a-z0-9_]+)`)
	wsListRE  = regexp.MustCompile(`(?s)permissions\s*=\s*\[([^\]]*)\]`)
	wsStrRE   = regexp.MustCompile(`"([^"]+)"`)
)

// wsAccess is the member's binding as the module writes it.
func wsAccess(t *testing.T) (path, src string) {
	t.Helper()
	path = filepath.Join(wsModuleDir, "workspace_access.tf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the Firebase module's workspace access file: %v", err)
	}
	return path, string(b)
}

// wsBlocks are a file's resource blocks of one type, as (name, body).
func wsBlocks(src, typ string) map[string]string {
	out := map[string]string{}
	for _, m := range wsBlockRE.FindAllStringSubmatch(src, -1) {
		if m[1] == typ {
			out[m[2]] = m[3]
		}
	}
	return out
}

// wsSquash is a block as one line, so a single Contains can state what the
// block must hold.
func wsSquash(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestMemberUpdateBindsTheWorkspaceGrantToThePinnedAccount: the member's own
// `fugaro update` creates ONE binding — the read-only database role — and
// binds it to the service account its config pins. A config whose pin names no
// account creates none, and no bucket binding is created at all.
func TestMemberUpdateBindsTheWorkspaceGrantToThePinnedAccount(t *testing.T) {
	path, src := wsAccess(t)

	// The account is a LOCAL input and nothing else. It is the member's own
	// workspace_pin's account (localcfg's WorkspacePin.ServiceAccount, beside
	// the issuer, the Firebase project and the assertion key), never a value
	// read from the workspace's published record: §2's record names no service
	// account, for the same reason it names no issuer, so nothing the workspace
	// publishes can move the member's grant to another identity.
	declared := map[string]bool{}
	for _, m := range wsVarRE.FindAllStringSubmatch(src, -1) {
		declared[m[1]] = true
	}
	want := map[string]bool{WorkspaceAccessVariable: true}
	if !slices.Equal(sortedKeys(declared), sortedKeys(want)) {
		t.Fatalf("%s declares the variables %v, want exactly %v: the pinned workspace account and no other input (a record's name, a member table or a bucket is not one)", path, sortedKeys(declared), sortedKeys(want))
	}
	used := map[string]bool{}
	for _, m := range wsRefRE.FindAllStringSubmatch(src, -1) {
		used[m[1]] = true
	}
	if !slices.Equal(sortedKeys(used), []string{"project", WorkspaceAccessVariable}) {
		t.Fatalf("%s reads var.%v, want exactly var.project (the Firebase project the database belongs to) and var.%s", path, sortedKeys(used), WorkspaceAccessVariable)
	}

	// One binding, guarded on the pin: an installation that names no workspace
	// passes "" and creates nothing. The absent pin is not an account.
	grants := wsBlocks(src, "google_project_iam_member")
	if len(grants) != 1 {
		t.Fatalf("%s declares %d project grants, want exactly one — the workspace's read of this database", path, len(grants))
	}
	var names []string
	for name := range grants {
		names = append(names, name)
	}
	grant := wsSquash(grants[names[0]])
	for _, want := range []string{
		`member = "serviceAccount:${var.` + WorkspaceAccessVariable + `}"`,
		`role = "projects/${var.project}/roles/` + WorkspaceReadRole + `"`,
		`project = var.project`,
		`count = var.` + WorkspaceAccessVariable + ` == "" ? 0 : 1`,
	} {
		if !strings.Contains(grant, want) {
			t.Errorf("%s: the grant does not hold %q:\n%s", path, want, grant)
		}
	}
	// And the role is created under the same guard, so a pin of "" leaves the
	// project without even an unused custom role.
	role := wsBlocks(src, "google_project_iam_custom_role")
	if len(role) != 1 {
		t.Fatalf("%s declares %d custom roles, want exactly one — the read-only database role", path, len(role))
	}
	for name, body := range role {
		if !strings.Contains(wsSquash(body), `count = var.`+WorkspaceAccessVariable+` == "" ? 0 : 1`) {
			t.Errorf("%s: %s is not guarded on the pin, so an installation in no workspace still creates it", path, name)
		}
	}

	// No bucket binding, and no second read of the member's artifacts: the old
	// rollup's roles/storage.objectViewer grant is gone, and the comparison the
	// cap is measured against needs no bucket at all.
	if strings.Contains(strings.ToLower(src), "storage") {
		t.Errorf("%s names storage: the reconciliation comparison reads the member's ledger and needs no bucket", path)
	}
	entries, err := os.ReadDir(wsModuleDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(wsModuleDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for typ := range wsBlocks(string(b), "google_storage_bucket_iam_member") {
			t.Errorf("%s: the Firebase module declares a bucket grant (%s); the member's workspace grant is one project binding and no bucket", e.Name(), typ)
		}
	}
}

// TestMemberUpdateBindsNoWriteOnTheMembersDatabase: the role the binding grants
// holds firebasedatabase.instances.get and NOT .update. The write direction of
// "a member's database is the member's" is IAM's here rather than the service's
// code, so a change that added the write back fails this test first — there is
// no write permission for a later revision of the service to reach for.
func TestMemberUpdateBindsNoWriteOnTheMembersDatabase(t *testing.T) {
	path, src := wsAccess(t)

	roles := wsBlocks(src, "google_project_iam_custom_role")
	if len(roles) != 1 {
		t.Fatalf("%s declares %d custom roles, want exactly one", path, len(roles))
	}
	var perms []string
	for _, body := range roles {
		list := wsListRE.FindStringSubmatch(body)
		if list == nil {
			t.Fatalf("%s: the role has no permissions list:\n%s", path, body)
		}
		for _, m := range wsStrRE.FindAllStringSubmatch(list[1], -1) {
			if !strings.Contains(m[1], ".") {
				continue // the list's own trailing comma artefacts and comments
			}
			perms = append(perms, m[1])
		}
	}
	sort.Strings(perms)
	want := slices.Clone(WorkspaceReadPermissions)
	sort.Strings(want)
	if !slices.Equal(perms, want) {
		t.Fatalf("%s: the workspace's role holds %v, want exactly %v: get is the instance's only read, and the workspace's own cap is measured in the workspace's database, not the member's", path, perms, want)
	}
	if slices.Contains(perms, "firebasedatabase.instances.update") {
		t.Fatalf("%s: the workspace's role holds the write: a member's database is the member's, and this grant is a read", path)
	}

	// The Go side of the same two facts, which is what a confirmation reads: a
	// module that grew a permission and a cover that accepted one cannot drift
	// apart.
	if pin := RolePermissions[WorkspaceReadRole]; !slices.Equal(pin, want) {
		t.Errorf("RolePermissions[%s] = %v, want exactly %v", WorkspaceReadRole, pin, want)
	}
	if !slices.Contains(FugaroRoles, WorkspaceReadRole) {
		t.Errorf("FugaroRoles does not name %s, so a grant of this role is refused", WorkspaceReadRole)
	}
	if slices.Contains(RolePermissions[WorkspaceReadRole], "firebasedatabase.instances.update") {
		t.Errorf("RolePermissions[%s] holds the write", WorkspaceReadRole)
	}
}

// A managed project's plan gate refuses this binding, and that is the correct
// answer rather than a limitation: the member hands read access to a principal
// that is not this installation's own, which is a loosening, and the cover
// admits no such grant. The stage therefore asks its own typed confirmation,
// which the member's owner gives at their own terminal once per member.
func TestTheWorkspaceBindingIsNotCovered(t *testing.T) {
	account := "fugaro-credentials@ws-1234.iam.gserviceaccount.com"
	role := "projects/fp-2/roles/" + WorkspaceReadRole
	p := &Plan{ResourceChanges: []ResourceChange{
		rc("google_project_iam_custom_role", map[string]any{
			"role_id": WorkspaceReadRole, "project": "fp-2",
			"permissions": []any{"firebasedatabase.instances.get"}, "title": "Fugaro workspace (database read only)",
		}, map[string]any{}, "create"),
		grant("google_project_iam_member", "serviceAccount:"+account, role),
	}}
	why := testCover.NotCovered(p, nil)
	if len(why) == 0 {
		t.Fatalf("the cover admits a plan that binds %s to %s: a grant of read access to another project's account is a loosening, and the cover admits none", role, account)
	}
	if !slices.ContainsFunc(why, func(s string) bool { return strings.Contains(s, "iam_member") || strings.Contains(s, "member") }) {
		t.Fatalf("the cover refuses the plan for %v, and none of it is the binding: the refusal has to name what the operator is being asked to confirm", why)
	}
	// The role's own creation is admitted — it grants nothing on its own — so
	// what is refused is the binding and not the file.
	for _, w := range why {
		if strings.Contains(w, "custom_role") {
			t.Fatalf("the cover refuses the role's creation (%q): the role grants nothing until it is bound", w)
		}
	}

	// And under a MANAGED project the binding is refused by the allowlist
	// itself, which keeps no project-level IAM type at all: the member's owner
	// runs this step at their own terminal rather than by a covered apply.
	mp := managedPlan(mrc("google_project_iam_member", map[string]any{"project": "proj-1", "role": role, "member": "serviceAccount:" + account}))
	mwhy := managedTestCover().NotCovered(mp, nil)
	if !slices.ContainsFunc(mwhy, func(s string) bool { return strings.Contains(s, "project-level IAM") }) {
		t.Fatalf("the managed cover does not refuse a project-level IAM grant for the reason it exists: %v", mwhy)
	}
}

// sortedKeys is a map's keys, sorted, for a readable failure.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
