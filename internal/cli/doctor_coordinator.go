package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// coordinatorOverridePermission is the one permission the owner's ruling of
// 2026-11-09 takes away from the coordinator: with it a caller of the front
// door can rewrite a run's execution — its container arguments included —
// rather than only narrow it, which is why the coordinator starts a run with
// the start-only role (infra.RoleJobStarter) and per-launch data travels in
// the signed task in the runs bucket instead (spec.md §8, reconciliation §4b).
const coordinatorOverridePermission = "run.jobs.runWithOverrides"

// coordinatorOverrideRoles are the predefined roles whose permission set
// carries coordinatorOverridePermission. It is the same set of built-in roles
// launcherRoleStartsJobs names: a Fugaro launch always carries overrides, so a
// built-in role that starts one has this permission, and the two lists part
// company only on the custom side, where the installation's job runner role
// has it and the start-only role does not.
var coordinatorOverrideRoles = []string{"roles/owner", "roles/editor", "roles/run.admin", "roles/run.developer", "roles/run.jobsExecutorWithOverrides"}

// doctorCoordinatorOverrides is the owner's ruling of 2026-11-09, read back
// off the project: neither account the coordinator work creates may hold a
// role that carries coordinatorOverridePermission. The two are the sweep's own
// (infra.CoordinatorAccountID) and the front door's (infra.MCPServerAccountID,
// M4), because the launch grant D931 describes is on the front door's account
// — the identity M9 declares the launch role for — and a project binding that
// reaches either one is what the ruling forbids.
//
// The check is driven by the policy and not by the local config: a grant that
// reaches one of these accounts is a finding whether or not this checkout
// records the opt-in that created it, and an installation whose policy names
// neither account gets no line at all, because then there is no fact to
// report.
//
// It reads what the permission is, never who granted it: a custom role is
// resolved through the IAM API (the same reader doctor's secret sweep uses),
// so a role this installation never created is judged by its own permissions
// rather than by its name. A custom role whose read fails is reported as
// unknown and not as fine.
func doctorCoordinatorOverrides(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck {
	const id = "coordinator-overrides"
	if c == nil {
		return nil
	}
	members := []string{coordinatorAccountMember(lc.GCPProject, infra.CoordinatorAccountID),
		coordinatorAccountMember(lc.GCPProject, infra.MCPServerAccountID)}
	p, err := c.Projects.GetIamPolicy(lc.GCPProject, &crm.GetIamPolicyRequest{
		Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if isAccessDenied(err) {
		return []doctorCheck{{ID: id, Severity: "warning", unread: true,
			Problem: "could not read the IAM policy of " + lc.GCPProject + " to check that the coordinator holds no role that can override a run: this credential does not hold resourcemanager.projects.getIamPolicy there",
			Fix:     "an operator runs fugaro doctor, which can read the project's policy"}}
	}
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "warning", unread: true,
			Problem: "could not read the IAM policy of " + lc.GCPProject + " to check that the coordinator holds no role that can override a run: " + oneLineCLI(err.Error()),
			Fix:     "an operator runs fugaro doctor, which can read the project's policy"}}
	}
	roles := newIAMRoles(ctx, lc)
	var found, unknown []string
	seen := false
	for _, b := range p.Bindings {
		if b == nil {
			continue
		}
		held := slices.Compact(slices.Sorted(slices.Values(b.Members)))
		held = slices.DeleteFunc(held, func(m string) bool { return !slices.Contains(members, m) })
		if len(held) == 0 {
			continue
		}
		seen = true
		overrides, why := coordinatorRoleOverrides(roles, b.Role, lc.GCPProject)
		switch {
		case why != "":
			unknown = append(unknown, fmt.Sprintf("%s (%s: %s)", b.Role, strings.Join(held, ", "), why))
		case overrides:
			found = append(found, fmt.Sprintf("%s (%s)", b.Role, strings.Join(held, ", ")))
		}
	}
	switch {
	case len(found) > 0:
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: "the coordinator holds a role that carries " + coordinatorOverridePermission + " in " + lc.GCPProject + ": " + strings.Join(found, "; ") + ". The owner's ruling of 2026-11-09 is that the coordinator never holds it — it starts a run with the start-only role (" + infra.RoleJobStarter + ", " + coordinatorStarterPermission + " alone) and per-launch data travels in the signed task in the runs bucket, never in an execution override. With this permission a caller of the front door can rewrite what a run does, its container arguments included, rather than only narrow it (docs/mcp-server/spec.md §8)",
			Fix:     "remove the grant from the coordinator's account in " + lc.GCPProject + " and grant it " + infra.RoleJobStarter + " where it must start a run; fugaro update writes the start-only role"}}
	case len(unknown) > 0:
		return []doctorCheck{{ID: id, Severity: "warning", unread: true,
			Problem: "could not tell whether the coordinator's roles in " + lc.GCPProject + " carry " + coordinatorOverridePermission + ": " + strings.Join(unknown, "; ") + ". An unknown is not a pass, so this is reported rather than read as fine",
			Fix:     "an operator runs fugaro doctor, or grants the coordinator the start-only role (" + infra.RoleJobStarter + ") instead"}}
	case seen:
		return []doctorCheck{{ID: id, OK: true}}
	}
	// No binding names either account: there is no grant to judge, and a
	// check printed over one would be a line about nothing.
	return nil
}

// coordinatorStarterPermission is what the start-only role holds: the whole of
// it, and the reason the ruling is a split rather than a removal.
const coordinatorStarterPermission = "run.jobs.run"

// coordinatorAccountMember is a service account's own ID as an IAM binding
// spells it.
func coordinatorAccountMember(project, accountID string) string {
	return "serviceAccount:" + accountID + "@" + project + ".iam.gserviceaccount.com"
}

// coordinatorRoleOverrides reports whether role carries
// coordinatorOverridePermission, and why it could not be told when it could
// not. The installation's own job runner role is answered by name — it is the
// custom role the modules create with exactly run.jobs.run and
// coordinatorOverridePermission, and internal/infra/tf pins that — and any
// other custom role is resolved through the IAM API, so a role of another
// installation is judged by its permissions and not by its name. A role this
// cannot read at all answers unknown, never false.
func coordinatorRoleOverrides(roles *iamRoles, role, project string) (bool, string) {
	switch {
	case slices.Contains(coordinatorOverrideRoles, role):
		return true, ""
	case role == "projects/"+project+"/roles/"+infra.RoleJobRunner:
		return true, ""
	case role == "projects/"+project+"/roles/"+infra.RoleJobStarter:
		return false, ""
	case !isCustomRole(role):
		// A predefined role outside the list above: no built-in role carries
		// coordinatorOverridePermission and is not named there.
		return false, ""
	}
	perms, why := roles.permissions(role)
	if why != "" {
		return false, why
	}
	return slices.Contains(perms, coordinatorOverridePermission), ""
}
