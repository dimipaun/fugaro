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
// SERVICE — the account the workspace's own bundle runs as and the one grant
// that reaches the workspace's own database
// (deploy/terraform/gcp/roots/workspace).
//
// This is a cover of its own rather than rows in cover.go's tables, because the
// question is different in kind. cover.go asks "is this plan the installation's
// modules' plan", over a tree of hundreds of resources and dozens of grant rows;
// this asks "is this plan the workspace's, and nothing else" over two resource
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
	// WorkspaceDatabaseRole is the role the workspace's own service account
	// holds on the workspace's own database: the whole-database role, because
	// Realtime Database IAM has no path-level scope and no condition can fence
	// one (spec §8). It is the read the workspace needs — the record, the
	// admitted-token hashes, the member ledgers the alarm compares — and the
	// write it needs, because the meter is a reserve and a settle (W7).
	WorkspaceDatabaseRole = "roles/firebasedatabase.admin"
	// WorkspaceServiceAccountID is the account id of the workspace's own
	// service account. It is deliberately not among FugaroAccounts: as one of
	// them, an installation table's toAccounts row could have granted it a
	// job's role, and this account's reach is one database grant and nothing
	// else.
	WorkspaceServiceAccountID = "fugaro-workspace"
	// workspaceAccountType and workspaceGrantType are the root's two resource
	// types: the account, and the member resource that binds its one role. A
	// type outside them is refused by name below.
	workspaceAccountType = "google_service_account"
	workspaceGrantType   = "google_project_iam_member"
)

// WorkspaceNotCovered lists why the workspace root's plan is not the workspace
// design's plan, empty when it is: every resource is one the root declares, the
// account is the workspace's own service account in the workspace's own project,
// and the single grant binds the one database role to that account and to no
// other principal.
//
// It allows none of: a resource type outside the root (a job, a bucket, a
// repository — spec §9), a data source or another mode, an authoritative IAM
// resource, a service account key, a delete or a replace, a grant of another
// role, a grant to another principal, or an account or a grant in another
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
		case rc.Type == workspaceAccountType:
			if why := workspaceAccount(rc.Change, project, accountID); why != "" {
				add(rc, why)
			}
		case rc.Type == workspaceGrantType:
			if why := workspaceGrant(rc.Change, project, wantAccount, own); why != "" {
				add(rc, why)
			}
		default:
			add(rc, fmt.Sprintf("a %s, and the workspace's own project carries no resource but its own service account and that account's one grant: a workspace holds no job, no runs bucket and no repository (spec §9)", safetext.Field(rc.Type)))
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
	if got := str(ch.After["role"]); got != WorkspaceDatabaseRole {
		return fmt.Sprintf("a grant of %q, and the workspace's own service account holds %s and nothing else: a second role here is a reach nobody decided on", safetext.Field(got), WorkspaceDatabaseRole)
	}
	if got := str(ch.After["project"]); got != project {
		return fmt.Sprintf("a grant in project %q, and the workspace's own database is %s's", safetext.Field(got), safetext.Field(project))
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
