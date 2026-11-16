//go:build terraform

package infra

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestUpdateGrantsTheStateReadFromThePreviousRelease is O16b's convergence
// test, and it is the HARD RULE's: `fugaro update` is the whole upgrade, so an
// installation whose front door is already on at the release before this one
// must reach this release in one update — no second command, no operator step —
// and the update after that one must write nothing at all.
//
// The release before this one is in the tree as a plan of it: the embedded root
// with this task's one module file removed (state_read.tf, the two resources
// that are the whole of O16b's plan change), which is the installation that
// holds the front door's account, its store, its service and the runs bucket's
// read, and holds no database grant for that account at all.
//
// Four claims, and each is a real comparison of the two plans rather than a
// restatement of the file:
//
//   - The delta is exactly the two rows, both creates: the update adds the
//     state read and rewrites nothing the installation already holds, so one
//     apply leaves a converged installation.
//   - The run's one confirmation covers both. An update that refused an item
//     per row would ask the owner for a typed confirmation of its own over an
//     IAM loosening — the operator step the HARD RULE forbids — and the release
//     note says what it asks for instead.
//   - The role is the read and only the read: firebasedatabase.instances.get on
//     the coordinator's Firebase project, no write permission to a later
//     revision of the front door to reach for.
//   - Every attribute of both rows is decided by this plan rather than by the
//     apply — the account's member, the project, the role's literal path and
//     the permission list are all literals or plan-time-known references, and
//     this test names each one by value. That is what "a second update writes
//     nothing" rests on: the state the first update writes is the document this
//     plan shows, so the next plan of the same configuration has no change for
//     either row. The test cannot apply to prove it (no agent run applies
//     anything, .claude/rules/terraform.md), and a value that reached the plan
//     unknown would make the assertion below fail rather than pass silently.
func TestUpdateGrantsTheStateReadFromThePreviousRelease(t *testing.T) {
	const (
		roleAddr  = "module.installation.google_project_iam_custom_role.state_read[0]"
		grantAddr = "module.installation.google_project_iam_member.state_read[0]"
		roleID    = "fugaroStateRead"
		// The coordinator's Firebase project, which AgentSpec/Coordinator0 hands
		// the spec and which is where the database lives (D6).
		firebaseProject = "aurora-fp"
		// The front door's own account, which the mock provider computes the way
		// the real one does: account_id at var.project.
		readMember = "serviceAccount:fugaro-mcp@proj-1234.iam.gserviceaccount.com"
	)

	spec := specWithTheFrontDoor(t)
	afterPlan, after := installationPlan(t, spec)
	before := installationPlanFor(t, spec, "state_read.tf")

	var added []string
	for addr, rc := range after {
		prev, ok := before[addr]
		if !ok {
			added = append(added, addr)
			continue
		}
		if rc.json() != prev.json() {
			t.Errorf("%s: the update plans %s where the previous release held %s: an installation that already runs the front door converges by additions alone, and a row that changes hands is the rewrite the HARD RULE forbids", addr, rc.json(), prev.json())
		}
	}
	slices.Sort(added)
	if !slices.Equal(added, []string{roleAddr, grantAddr}) {
		t.Errorf("the update adds %v, want exactly [%s %s]: O16b's plan change is the front door's database read and nothing else, and a third address is either a wider release than this task or a grant nobody reviewed", added, roleAddr, grantAddr)
	}
	for addr := range before {
		if _, ok := after[addr]; !ok {
			t.Errorf("%s leaves the plan: the update retires nothing from an installation that already runs the front door", addr)
		}
	}

	for _, addr := range []string{roleAddr, grantAddr} {
		rc, ok := after[addr]
		if !ok {
			t.Fatalf("the plan of an installation that passed --mcp-server carries no %s: a turned-on front door whose account holds no database grant serves get_state and get_events and reads nothing (O16b, D2020 (a))", addr)
		}
		if !slices.Equal(rc.Actions, []string{"create"}) {
			t.Errorf("%s: the plan's actions are %v, want [create]: the state read is made by this update with the owner's own rights", addr, rc.Actions)
		}
		if rc.Importing != nil {
			t.Errorf("%s: the plan imports the resource (%v): the role and its binding are created by this installation and never adopted from somebody else's", addr, rc.Importing)
		}
	}

	role := after[roleAddr]
	for _, want := range []struct {
		what     string
		got, exp any
	}{
		{"project", role.After["project"], firebaseProject},
		{"role_id", role.After["role_id"], roleID},
	} {
		if want.got != want.exp {
			t.Errorf("%s: %s is %v, want %v", roleAddr, want.what, want.got, want.exp)
		}
	}
	// The permission list is the whole of the role, and it is read back through
	// the plan's own JSON so a second permission — the write above all — is a
	// failure here and not a wider role nobody noticed.
	perms, err := json.Marshal(role.After["permissions"])
	if err != nil {
		t.Fatal(err)
	}
	if string(perms) != `["firebasedatabase.instances.get"]` {
		t.Errorf("%s: permissions is %s, want [\"firebasedatabase.instances.get\"]: the front door reads the orchestrator's node (D2020 (a)) and writes nothing, and firebasedatabase.instances.update would be that write", roleAddr, perms)
	}

	grant := after[grantAddr]
	for _, want := range []struct {
		what     string
		got, exp any
	}{
		{"project", grant.After["project"], firebaseProject},
		{"role", grant.After["role"], "projects/" + firebaseProject + "/roles/" + roleID},
		{"member", grant.After["member"], readMember},
	} {
		if want.got != want.exp {
			t.Errorf("%s: %s is %v, want %v", grantAddr, want.what, want.got, want.exp)
		}
	}
	// No condition, which RTDB IAM cannot use to fence a path: a condition here
	// would be a fence that looks like one and is not.
	if conds, ok := grant.After["condition"].([]any); ok && len(conds) != 0 {
		t.Errorf("%s: the grant carries %d conditions, want none: Realtime Database IAM has no path-level scope, so a condition on this binding fences nothing", grantAddr, len(conds))
	}

	// The two rows are the whole of it: a third row naming the role is a grant
	// of it beyond the front door's own account.
	var naming []string
	for _, rc := range after {
		if rc.Address == roleAddr || rc.Address == grantAddr {
			continue
		}
		if strings.Contains(rc.json(), roleID) {
			naming = append(naming, rc.Address)
		}
	}
	if len(naming) != 0 {
		t.Errorf("the plan's rows naming %s beyond its own two are %v: the role is bound to the front door's account and to nobody else (coordinator.tf's own rule)", roleID, naming)
	}

	// And the run's one confirmation covers both rows, which is what makes the
	// update apply without an operator step of its own.
	for _, item := range theRunsConfirmation(t, spec).NotCovered(afterPlan, nil) {
		if strings.Contains(item, roleID) || strings.Contains(item, grantAddr) {
			t.Errorf("the run's one confirmation refuses the state read: %s\n`fugaro update` adds the front door's database role and its one binding, so that step must not ask the owner for a confirmation of its own over them", item)
		}
	}
}
