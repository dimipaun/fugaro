package tf

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// The workspace root's plan cover (docs/workspaces/implementation-plan.md W3).
//
// The workspace's own project is the project the workspace is named after
// (D1290, D1291): its Firebase, its database and its signer reach it through the
// installation's roots, and what the workspace root adds is the workspace's own
// SERVICE — the account the workspace's own bundle runs as, the one role that
// names what that account reaches and the one grant that binds them
// (deploy/terraform/gcp/roots/workspace).
//
// This is a cover of its own rather than rows in cover.go's tables, because the
// question is different in kind. cover.go asks "is this plan the installation's
// modules' plan", over a tree of hundreds of resources and dozens of grant rows;
// this asks "is this plan the workspace's, and nothing else" over three resource
// types. Putting it in the first table would mean a workspace root's plan judged
// by rules written for jobs, buckets and repositories — the things a workspace
// must never carry.
//
// Why "nothing else" is security and not tidiness (spec §9): a job, a runs
// bucket or a repository deployed into the workspace's own project would be a
// workspace holding a member's work, and the workspace is the one project every
// member of the group holds a credential against. An installation's converge
// would be what put it there, under one confirmation naming the account and its
// grant.
const (
	// WorkspaceDatabaseRoleID is the id of the custom role the workspace root
	// creates: the workspace's own service's reach over the workspace's own
	// database, and the only role that account holds. It is a custom role and
	// not roles/firebasedatabase.admin because Realtime Database IAM has no
	// path-level scope and no condition can fence one (spec §8), so the only
	// thing left to narrow is WHICH OPERATIONS the account may make — and
	// admin adds the instance lifecycle (list, create, delete, disable,
	// reenable, undelete) to the two data permissions the workspace's own
	// service uses. Nothing in this design creates, deletes or disables a
	// database, and the account that holds the record and the admitted-token
	// hashes must not be the one that can destroy them
	// (.claude/rules/terraform.md: "list the exact permissions and use the
	// narrowest custom role").
	//
	// It is deliberately NOT among FugaroRoles: that table is the
	// installation's modules' roles, each pinned in RolePermissions and
	// covered by cover.go's grantRules for an installation's plan, and a
	// grant of one of them is a widening the run's one --yes may cover. This
	// role belongs to the workspace root alone, and that root's plan is judged
	// by this cover, which pins the id and the permissions below.
	WorkspaceDatabaseRoleID = "fugaroWorkspaceDatabase"
	// WorkspaceServiceAccountID is the account id of the workspace's own
	// service account. It is deliberately not among FugaroAccounts: as one of
	// them, an installation table's toAccounts row could have granted it a
	// job's role, and this account's reach is one database grant and nothing
	// else.
	WorkspaceServiceAccountID = "fugaro-workspace"
	// workspaceRoleType, workspaceAccountType and workspaceGrantType are the
	// root's three resource types: the role, the account, and the member
	// resource that binds them. A type outside them is refused by name below.
	workspaceRoleType    = "google_project_iam_custom_role"
	workspaceAccountType = "google_service_account"
	workspaceGrantType   = "google_project_iam_member"
)

// WorkspaceDatabasePermissions is EXACTLY what that role holds: the instance's
// two data permissions, and no list and no lifecycle permission. get is the
// read — the record at workspaces/<name>, the admitted-token hashes, the member
// ledgers the alarm compares — and update the write, because a Realtime
// Database node has no separate create or delete and the meter is a reserve and
// a settle (W7). The same pair is what the credentials service's database role
// holds, which is the reviewed precedent for it
// (deploy/terraform/gcp/modules/installation/credentials.tf); a third
// permission is a decision a person makes here and in the root together.
var WorkspaceDatabasePermissions = []string{"firebasedatabase.instances.get", "firebasedatabase.instances.update"}

// WorkspaceDatabaseRole is how the workspace root's grant names that role: the
// literal path of the workspace's own project, which is what terraform plans
// when the project is a variable and what this cover reads out of the plan. The
// root writes the same string (roots/workspace/main.tf): the role resource's
// own name attribute would be a value the plan does not carry, and a grant
// whose role a cover cannot read is a grant it cannot hold to the id above.
func WorkspaceDatabaseRole(project string) string {
	return "projects/" + project + "/roles/" + WorkspaceDatabaseRoleID
}

// WorkspaceNotCovered lists why the workspace root's plan is not the workspace
// design's plan, empty when it is: every resource is one the root declares, the
// custom role is the one role with the two pinned permissions, the account is
// the workspace's own service account in the workspace's own project, and the
// single grant binds that role to that account and to no other principal.
//
// It allows none of: a resource type outside the root (a job, a bucket, a
// repository — spec §9), a data source or another mode, an authoritative IAM
// resource, a service account key, a delete or a replace, a custom role of
// another id or with another permission, a grant of another role, a grant to
// another principal, or a role, an account or a grant in another
// project. project is the workspace's own project and accountID its account id,
// so nothing is derived from the plan being judged: a rule that read the
// account's own address out of the plan would accept whatever the plan named.
func WorkspaceNotCovered(p *Plan, project, accountID string) []string {
	if p == nil {
		return []string{"no plan"}
	}
	wantAccount := accountID + "@" + project + ".iam.gserviceaccount.com"
	// own is whether the plan itself creates the workspace's own account: a
	// member terraform knows only at apply is accepted for the account this
	// plan creates, and for nothing else.
	own := planWorkspaceAccount(p, project, accountID)
	var out []string
	add := func(rc ResourceChange, why string) { out = append(out, safetext.Field(rc.Address)+" ("+why+")") }
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		switch {
		case len(acts) == 0:
			add(rc, "no actions")
			continue
		case !allOf(acts, "create", "update", "no-op", "read"):
			// A delete or a replace is a change this root never makes: the
			// workspace's service account and its grant are what the whole
			// group's record and meter are read with, and tf.Guard refuses a
			// delete anyway. Saying it here as well is what keeps the sentence
			// the operator reads about the workspace rather than about
			// terraform.
			add(rc, "actions "+strings.Join(acts, ", "))
			continue
		case rc.Mode != "managed":
			add(rc, managedModeReason(rc.Mode))
			continue
		}
		if allOf(acts, "no-op", "read") && rc.Change.Importing == nil {
			continue
		}
		switch {
		case strings.HasSuffix(rc.Type, "_iam_binding") || strings.HasSuffix(rc.Type, "_iam_policy") || strings.HasSuffix(rc.Type, "_iam_audit_config"):
			add(rc, "an authoritative IAM resource")
		case rc.Type == "google_service_account_key":
			add(rc, "a service account key")
		case rc.Type == workspaceRoleType:
			if why := workspaceCustomRole(rc.Change, project); why != "" {
				add(rc, why)
			}
		case rc.Type == workspaceAccountType:
			if why := workspaceAccount(rc.Change, project, accountID); why != "" {
				add(rc, why)
			}
		case rc.Type == workspaceGrantType:
			if why := workspaceGrant(rc.Change, project, wantAccount, own); why != "" {
				add(rc, why)
			}
		default:
			add(rc, fmt.Sprintf("a %s, and the workspace's own project carries no resource but its own role, its own service account and that account's one grant: a workspace holds no job, no runs bucket and no repository (spec §9)", safetext.Field(rc.Type)))
		}
	}
	return out
}

// planWorkspaceAccount is whether a plan creates the workspace's own service
// account: an account resource, not deleted, in the workspace's own project and
// with the workspace's account id. What it is for is the plan's computed member
// (workspaceGrant): terraform knows the address <id>@<project>.iam... only at
// apply, so the member of the root's own grant arrives unknown, and the account
// this plan creates is what makes that member readable rather than refused.
func planWorkspaceAccount(p *Plan, project, accountID string) bool {
	for _, rc := range p.ResourceChanges {
		if rc.Type != workspaceAccountType || slices.Contains(rc.Change.Actions, "delete") {
			continue
		}
		if workspaceAccount(rc.Change, project, accountID) == "" {
			return true
		}
	}
	return false
}

// workspaceAccount says why the service account a plan creates or updates is not
// the workspace's own: its project or its id. It is the empty string for an
// account the plan has to compute (its id or project unknown), and
// planWorkspaceAccount is where such an account is weighed instead.
func workspaceAccount(ch Change, project, accountID string) string {
	if unknown(ch, "account_id") || unknown(ch, "project") {
		return "an account whose id or project terraform knows only at apply, in a root that writes both as the names it was given"
	}
	if got := str(ch.After["project"]); got != project {
		return fmt.Sprintf("an account in project %q, and the workspace's own is %s: an account in another project is another project's identity", safetext.Field(got), safetext.Field(project))
	}
	if got := str(ch.After["account_id"]); got != accountID {
		return fmt.Sprintf("the account %q, and the workspace's own service account is %s", safetext.Field(got), safetext.Field(accountID))
	}
	return ""
}

// workspaceCustomRole says why the custom role a plan creates or updates is not
// the workspace's one role: another project, another id, or a permission list
// that is not exactly the pinned two (a list terraform knows only at apply, or
// one of another length counts as not).
//
// The role is in the cover because the plan that grants it is also the plan that
// can widen it: a permission added here — the instance lifecycle admin carries,
// or the list — would be a reach nobody decided on, granted in the same apply
// the typed confirmation describes as "its one grant" (spec §8: Realtime
// Database IAM has no path-level scope, so the operations a role allows ARE the
// grant).
func workspaceCustomRole(ch Change, project string) string {
	pinned := strings.Join(WorkspaceDatabasePermissions, ", ")
	if unknown(ch, "role_id") || unknown(ch, "project") || unknown(ch, "permissions") {
		return "a custom role whose id, project or permissions terraform knows only at apply, in a root that writes all three"
	}
	if got := str(ch.After["project"]); got != project {
		return fmt.Sprintf("a custom role in project %q, and the workspace's own database is in %s: a role in another project is another project's reach", safetext.Field(got), safetext.Field(project))
	}
	if got := str(ch.After["role_id"]); got != WorkspaceDatabaseRoleID {
		return fmt.Sprintf("the custom role %q, and the workspace's own service holds %s and no other role", safetext.Field(got), WorkspaceDatabaseRoleID)
	}
	got := strList(ch.After["permissions"])
	if len(got) != len(WorkspaceDatabasePermissions) {
		return fmt.Sprintf("a custom role holding %d permissions, and the workspace's own service holds exactly two (%s): a third is a reach nobody decided on", len(got), pinned)
	}
	have := map[string]bool{}
	for _, p := range got {
		have[p] = true
	}
	for _, p := range WorkspaceDatabasePermissions {
		if !have[p] {
			return fmt.Sprintf("a custom role that does not hold %s, and the workspace's own service holds exactly %s", p, pinned)
		}
	}
	return ""
}

// workspaceGrant says why the one member resource of a plan is not the
// workspace's one grant: another role, another project, or another principal.
//
// A member the plan does not know is the root's own shape — terraform computes
// the address the account will have — and it is accepted only while the same
// plan creates that account (own), whose project and id workspaceAccount has
// already held. A member that is known must be that account's address, bare or
// prefixed serviceAccount: as the root's own file writes it, so a plan that
// names a person, a domain, allUsers or another account is refused by name.
func workspaceGrant(ch Change, project, wantAccount string, own bool) string {
	if unknown(ch, "role") || unknown(ch, "project") {
		return "a grant whose role or project terraform knows only at apply, in a root that writes both as the names it was given"
	}
	// The project is read before the role: a grant moved to another project
	// arrives with that project's own role path, and "the workspace's own
	// database is <project>'s" is the sentence that names what happened —
	// the role check would answer "another role", which is true and less
	// useful.
	if got := str(ch.After["project"]); got != project {
		return fmt.Sprintf("a grant in project %q, and the workspace's own database is %s's", safetext.Field(got), safetext.Field(project))
	}
	if want := WorkspaceDatabaseRole(project); str(ch.After["role"]) != want {
		return fmt.Sprintf("a grant of %q, and the workspace's own service account holds %s and nothing else: a second role here is a reach nobody decided on", safetext.Field(str(ch.After["role"])), want)
	}
	if unknown(ch, "member") {
		if !own {
			return fmt.Sprintf("a grant whose member terraform knows only at apply, and this plan does not create the workspace's own service account: the member could be anyone, and the workspace's database is reached by %s alone", wantAccount)
		}
		return ""
	}
	m := str(ch.After["member"])
	switch {
	case m == "":
		return "no member"
	case m == wantAccount, m == "serviceAccount:"+wantAccount:
		return ""
	}
	return fmt.Sprintf("a grant to %s, and the workspace's own database is reached by %s and by nobody else: a member's account, a person, a group, a domain or a public principal here is a client that can read the record and the admitted-token hashes", safetext.Field(m), wantAccount)
}
