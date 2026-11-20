package tf

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// The workspace root's plan cover (docs/workspaces/implementation-plan.md W3,
// docs/workspace-service/implementation-plan.md WS6).
//
// The workspace's own project is the project the workspace is named after
// (D1290, D1291): its Firebase, its database and its signer reach it through the
// installation's roots, and what the workspace root adds is the workspace's own
// SERVICES — the account the workspace's own bundle runs as, the one role that
// names what that account reaches, the one grant that binds them, the
// workspace's own credentials service running as that account (D2980), the ONE
// bundle accessor that account reads its bundle through (F4) and one invoke
// grant per member job account (F17)
// (deploy/terraform/gcp/roots/workspace).
//
// This is a cover of its own rather than rows in cover.go's tables, because the
// question is different in kind. cover.go asks "is this plan the installation's
// modules' plan", over a tree of hundreds of resources and dozens of grant rows;
// this asks "is this plan the workspace's, and nothing else" over six resource
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
//
// What the second half of the root is for: the workspace's credentials service
// is the SAME image as an installation's, running the same code, told what it is
// by its environment (D2980) — the workload runs as
// google_service_account.workspace, reads the ONE bundle container the
// workspace's own project holds, and is invoked by a member's own job accounts
// and by nobody else. So the plan that deploys it is held to the identity that
// reads every model credential the workspace holds, which is why the cover pins
// the runtime account, the container and the members rather than reading them
// out of the plan.
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
	// job's role, and this account's reach is one database grant, the one
	// bundle accessor and nothing else.
	WorkspaceServiceAccountID = "fugaro-workspace"
	// workspaceRoleType, workspaceAccountType and workspaceGrantType are the
	// root's first three resource types: the role, the account, and the member
	// resource that binds them. workspaceServiceType, workspaceAccessorType and
	// workspaceInvokerType are the other three: the workspace's own credentials
	// service, the ONE accessor grant on its bundle container, and the invoke
	// grant a member's job account reaches that service through. A type outside
	// these six is refused by name below.
	workspaceRoleType     = "google_project_iam_custom_role"
	workspaceAccountType  = "google_service_account"
	workspaceGrantType    = "google_project_iam_member"
	workspaceServiceType  = "google_cloud_run_v2_service"
	workspaceAccessorType = "google_secret_manager_secret_iam_member"
	workspaceInvokerType  = "google_cloud_run_v2_service_iam_member"
	// WorkspaceBundleAccessorRole is the one role the workspace's own account
	// holds on the workspace's own bundle container, and WorkspaceInvokerRole
	// the one a member's job account holds on the workspace's service. Both are
	// predefined and not custom, for the same reason the installation's are:
	// each is exactly the surface needed and nothing more — the service reads
	// the container's current version and no other operation on it, and a
	// member's run invokes the service and no other operation on it
	// (.claude/rules/terraform.md).
	WorkspaceBundleAccessorRole = "roles/secretmanager.secretAccessor"
	WorkspaceInvokerRole        = "roles/run.invoker"
	// WorkspaceModeEnv is the name that tells the workspace's service from an
	// installation's: the same image, the same code, one process, and the
	// workspace's half of it switched on by this variable alone (D2980). It is
	// the name the workspace root writes and the process reads (C2 owns the
	// spelling — the plan's Interfaces row: "builds C2's Terraform side (the
	// environment names)" — and the value is the workspace's own name, because
	// the process has to know which workspace it is serving and the name is the
	// one thing about it that is not already in FUGARO_PROJECT's spelling).
	WorkspaceModeEnv = "FUGARO_WORKSPACE_MODE"
	// WorkspaceMaxInstances and WorkspaceConcurrency are R9's limits for the
	// workspace's service, written out literally in the root and pinned here:
	// a maximum of ten instances and a concurrency of forty per instance. They
	// are first values — the load check at a hundred concurrent runs confirms
	// or corrects them (D2986, reconciliation O1) — and they are stated rather
	// than left to the provider because an attribute terraform computes is
	// unknown at plan time, which a managed gate reads as unpriceable.
	WorkspaceMaxInstances = 10
	WorkspaceConcurrency  = 40
)

// WorkspaceServiceEnv are the environment names the workspace's service's
// container may carry, and the whole of them: the workspace root's own six
// (which are the installation's FUGARO_PROJECT, FUGARO_GCP_PROJECT,
// FUGARO_REGION, FUGARO_RTDB_URL, FUGARO_FIREBASE_PROJECT and
// FUGARO_CREDENTIALS_SERVICE_URL — the same values an installation's service
// gets, because it is the same process) plus the one that switches its
// workspace half on.
//
// It is deliberately NOT credentialsServiceEnv: that list is the installation
// service's, and the names it carries beyond these six are the installation's
// CALLER CLASSES — the per-repository job table, the verifier, person,
// coordinator, launcher, shared-job, build and orchestrator sets. A workspace's
// service admits a member's job account through its own published node and the
// invoke grant this cover pins, and an environment that carried a class list
// here would be a second admission path nobody decided on.
//
// No secret ID and no secret value is among them, for the installation's own
// reason (cover.go's credentialsServiceEnv says it at length): the loader
// derives the container from the workspace's name
// (credsvc.WorkspaceBundleSecretID, the same derivation the accessor below is
// held to) and reads it as the service's own account.
var WorkspaceServiceEnv = []string{
	"FUGARO_PROJECT", "FUGARO_GCP_PROJECT", "FUGARO_REGION",
	"FUGARO_RTDB_URL", "FUGARO_FIREBASE_PROJECT",
	"FUGARO_CREDENTIALS_SERVICE_URL",
	WorkspaceModeEnv,
}

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
// the workspace's own service account in the workspace's own project, the
// single grant binds that role to that account and to no other principal, the
// workspace's service is the one bundle-reading process the workspace holds —
// running as that account, digest-pinned, invoked by member job accounts alone
// and inside R9's limits — its one accessor is on the workspace's own bundle
// container, and every invoke grant is on that service for a member of the
// table.
//
// It allows none of: a resource type outside the root (a job, a bucket, a
// repository — spec §9), a second service, a data source or another mode, an
// authoritative IAM resource, a service account key, a replace, a custom role
// of another id or with another permission, a grant of another role, a grant to
// another principal, a role, an account or a grant in another project, a
// service in another project, running as another identity or on an image that
// is not digest-pinned, an environment outside the names the process reads, an
// accessor on another container or with another role, or an invoke grant on
// another service, with another role or to anything but a member's own job
// account.
//
// It allows exactly ONE deletion (F25): a member's invoke grant. Removing a
// member from the table deletes its grants, and the workspace's plan refusing
// that would leave an operator repair — a state this design has none of
// (the plan's own constraint "Removing a member needs no operator repair",
// pinned by TestWorkspacePlanAdmitsRemovingAMembersInvokeGrant). Everything
// else — the workspace's service, its accessor, its account, its role and its
// database grant — is what the whole group's record and meter are kept by, and
// a plan that deletes or replaces one of them is refused here by name.
//
// project is the workspace's own project, accountID its account id,
// bundleSecret the Secret Manager ID of the workspace's own bundle container
// (credsvc.WorkspaceBundleSecretID of the workspace's name) and memberAccounts
// the job service accounts of the workspace's member table. All four are GIVEN
// rather than derived from the plan being judged: a rule that read the account's
// own address, the container or the members out of the plan would accept
// whatever the plan named. An empty bundleSecret admits no accessor and an
// empty memberAccounts admits no invoke grant, which is the fail-closed
// direction: a caller that knows neither the container nor the members is a
// caller reading a plan that holds no service at all (WS6's own state, before
// WS7 wires both from the workspace's config).
//
// The receiver is the run's own cover, and it carries the fourth thing no plan
// holds: the image identity the service is held to (Registry, Ghcr, Owner), for
// the same reason and from the same source as init's review and managed_apply
// build a Cover — the owner of a released pin is a recorded fact of the run's
// config, and a rule that read it out of the reference being judged would accept
// any owner.
func (c Cover) WorkspaceNotCovered(p *Plan, project, accountID, bundleSecret string, memberAccounts []string) []string {
	if p == nil {
		return []string{"no plan"}
	}
	wantAccount := accountID + "@" + project + ".iam.gserviceaccount.com"
	// own is whether the plan itself creates the workspace's own account: a
	// member terraform knows only at apply is accepted for the account this
	// plan creates, and for nothing else.
	own := planWorkspaceAccount(p, project, accountID)
	// service is the name the plan's own service carries: what every invoke
	// grant of the plan is held to, so a grant on another service of the same
	// project is refused rather than admitted by its type.
	service := planWorkspaceServiceName(p)
	var out []string
	add := func(rc ResourceChange, why string) { out = append(out, safetext.Field(rc.Address)+" ("+why+")") }
	services := 0
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		switch {
		case len(acts) == 0:
			add(rc, "no actions")
			continue
		case allOf(acts, "delete"):
			// F25's one admitted deletion, and only this one: a member's
			// invoke grant, read from the state the workspace holds. Every
			// other type falls through to the refusal below, the workspace's
			// service and its accessor included.
			if rc.Type == workspaceInvokerType {
				if why := workspaceInvokeGrant(rc.Change, true, project, memberAccounts, service); why != "" {
					add(rc, why)
				}
				continue
			}
			add(rc, "actions "+strings.Join(acts, ", "))
			continue
		case !allOf(acts, "create", "update", "no-op", "read"):
			// A replace is not the one deletion either: the workspace's
			// service account, its grant, its service and its accessor are
			// what the whole group's record, meter and credentials are read
			// with, and tf.Guard refuses a delete anyway. Saying it here as
			// well is what keeps the sentence the operator reads about the
			// workspace rather than about terraform.
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
		case rc.Type == workspaceServiceType:
			// The shape first and the count second: a second service that is
			// itself broken is refused for what is wrong with it, and only a
			// second service that would otherwise be covered is refused for
			// being the second — both are refusals, and the sentence an
			// operator reads names the one that is true of the plan.
			services++
			why := c.workspaceService(rc.Change, project, wantAccount, memberAccounts)
			if why == "" && services > 1 {
				why = "a second service in the workspace's own project, and the workspace deploys one service: its own credentials service, which reads the workspace's own bundle and refuses every route a member's project's service would serve it (R14, D2980). A second one here is a second process holding a bundle the workspace's whole group pays for"
			}
			if why != "" {
				add(rc, why)
			}
		case rc.Type == workspaceAccessorType:
			if why := workspaceBundleAccessor(rc.Change, project, wantAccount, bundleSecret); why != "" {
				add(rc, why)
			}
		case rc.Type == workspaceInvokerType:
			if why := workspaceInvokeGrant(rc.Change, false, project, memberAccounts, service); why != "" {
				add(rc, why)
			}
		default:
			add(rc, fmt.Sprintf("a %s, and the workspace's own project carries no resource but the workspace's own role, its own service account and that account's one grant, the workspace's credentials service, its one bundle accessor and its members' invoke grants: a workspace holds no job, no runs bucket and no repository (spec §9)", safetext.Field(rc.Type)))
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

// planWorkspaceServiceName is the name of the workspace's own credentials
// service as the plan writes it, "" when the plan holds none (or holds one
// whose name terraform knows only at apply). It is what every invoke grant of
// the plan is held to: the root binds a member's job account to ONE service, and
// a grant on another service of the same project would otherwise be admitted by
// its type alone.
func planWorkspaceServiceName(p *Plan) string {
	for _, rc := range p.ResourceChanges {
		if rc.Type != workspaceServiceType || slices.Contains(rc.Change.Actions, "delete") {
			continue
		}
		if unknown(rc.Change, "name") {
			return ""
		}
		return str(rc.Change.After["name"])
	}
	return ""
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
	case workspaceIsAccount(m, wantAccount):
		return ""
	}
	return fmt.Sprintf("a grant to %s, and the workspace's own database is reached by %s and by nobody else: a member's account, a person, a group, a domain or a public principal here is a client that can read the record and the admitted-token hashes", safetext.Field(m), wantAccount)
}

// workspaceService says why the Cloud Run service a plan creates or updates is
// not the workspace's own credentials service (WS6, D2980):
//
//   - its project, name or location is terraform's to compute, in a root that
//     writes all three;
//   - it is in another project: the workspace's service runs beside the
//     workspace's database and the workspace's bundle, and the identity it runs
//     as is one this root creates;
//   - it does not require IAM (invoker_iam_disabled): this setting is the
//     fence every caller passes through, and the process's own ID-token check
//     is layered on top of it, never instead of it (the installation's own
//     rule, credentials.tf). With it off, anyone on the internet reaches the
//     process that reads every model credential the workspace holds;
//   - it states no zero minimum, at the service level or in its template: the
//     managed gate prices a Cloud Run service by its minimum instance count at
//     BOTH levels and an attribute the provider computes is unknown at plan
//     time, so a service that states only one is refused as unpriceable at the
//     other — and zero is also the shape this service has (nothing keeps an
//     instance warm, and a member's run pays a cold start rather than the
//     workspace paying around the clock);
//   - it runs as anybody but the workspace's own service account: that account
//     is the ONE the accessor below grants the workspace's bundle, so any other
//     runtime identity here reads every credential the workspace holds. A
//     member's own job account is named as its own mistake, because that is the
//     one a reader would mistake for the right answer (the workspace admits
//     member job accounts — as CALLERS of the service, through an invoke grant
//     and an ID token, and never as its identity);
//   - its image is not the released pin an installation's own credentials
//     service runs — the same image, through this run's remote repository, with
//     the owner the run's config records, a fugaro-<kind> name, a release
//     version tag and a digest (credentialsImageOK, whose whole argument the
//     image case below carries) — or its container sets a command or args: the
//     image's own entrypoint is the service, and an override is a hand-edited
//     tfvars running something else as the identity that reads the bundle;
//   - its environment carries a name the process does not read, takes a value
//     from a source, or does not carry the workspace-mode name at all: the
//     environment is the one path a credential could reach this process without
//     a Secret Manager read, and the mode name is the single thing that tells
//     the workspace's service from an installation's (the same image, the same
//     code);
//   - it states a maximum instance count, a concurrency or a template minimum
//     other than R9's, which are three numbers a person decides here.
//
// memberAccounts is the member table the plan is judged against, so that the
// member case above can name it. The image case is the one rule here that is
// cover.go's rather than this file's (credentialsImageOK): it is the closure an
// independent security review gave the installation's credentials service, and
// the workspace's service is that same image running the same code (D2980),
// read as the account that reads every model credential the workspace holds.
// The cover's receiver is what carries the half of it no plan holds — the
// registry, the remote repository and the owner the run's config records.
func (c Cover) workspaceService(ch Change, project, wantAccount string, memberAccounts []string) string {
	a, unk := ch.After, ch.AfterUnknown
	if unknown(ch, "project") || unknown(ch, "name") || unknown(ch, "location") {
		return "a service whose project, name or location terraform knows only at apply, in a root that writes all three"
	}
	if got := str(a["project"]); got != project {
		return fmt.Sprintf("a service in project %q, and the workspace's own credentials service runs in %s: a service in another project reads another project's bundle under an identity this root does not create", safetext.Field(got), safetext.Field(project))
	}
	if str(a["name"]) == "" {
		return "a service with no name, in a root that writes the name it was given"
	}
	if str(a["location"]) == "" {
		return "a service with no location, in a root that writes the region it was given"
	}
	if unknown(ch, "invoker_iam_disabled") {
		return "an IAM setting that is not known"
	}
	if a["invoker_iam_disabled"] != false {
		return "a service that does not require IAM, which would let anyone invoke the process that reads every model credential the workspace holds: the service's own ID-token check is layered on top of this setting and never instead of it"
	}
	if why := workspaceZeroMinInstances(a, unk); why != "" {
		return why
	}
	if unknownAt(unk, "template") {
		return "a template that is not known"
	}
	tpl := objects(at(a, "template"))
	if len(tpl) != 1 {
		return fmt.Sprintf("a service with %d templates, and the workspace's own has one: the account it runs as, its concurrency and its environment are read here", len(tpl))
	}
	t := tpl[0]
	if unknownAt(unk, "template", 0, "service_account") {
		return "a runtime account that is not known"
	}
	acct := str(t["service_account"])
	switch {
	case acct == "":
		return "a service with no runtime account, and the workspace's own service runs as " + wantAccount
	case workspaceIsAccount(acct, wantAccount):
	case workspaceIsMemberOf(acct, memberAccounts):
		return fmt.Sprintf("a service running as %s, and a member's own job account reaches the workspace's service as a CALLER and never as its identity: the workspace's own service runs as %s, which is the one account the bundle accessor below grants, and a member's job running as it would read the workspace's own credentials directly", safetext.Field(acct), wantAccount)
	default:
		return fmt.Sprintf("a service running as %s, and the workspace's own service runs as %s and as nobody else: the accessor below grants that account the workspace's own bundle, so any other runtime identity here reads every model credential the workspace holds", safetext.Field(acct), wantAccount)
	}
	if why := workspaceTemplateLimits(t, unk); why != "" {
		return why
	}
	if unknownAt(unk, "template", 0, "containers") {
		return "containers that are not known"
	}
	cs := objects(at(a, "template", "containers"))
	if len(cs) != 1 {
		return fmt.Sprintf("a service with %d containers, and the workspace's own has one: the image and the environment below are read there", len(cs))
	}
	ctr := cs[0]
	for _, attr := range []string{"image", "command", "args"} {
		if unknownAt(unk, "template", 0, "containers", 0, attr) {
			return "a container " + attr + " that is not known"
		}
	}
	if img := str(ctr["image"]); !c.credentialsImageOK(img, project) {
		return fmt.Sprintf("runs the image %q, and the workspace's own service runs the released pin an installation's credentials service runs and nothing else (%s%s/<owner>/fugaro-<kind>:<version>@sha256:<64 lower-case hex digits>): every instance of it reads every model credential the workspace holds, and every operator holds roles/artifactregistry.writer on this project's own repositories, so a digest is not enough — the registry, the remote repository and the owner are read against this run's own recorded config", safetext.Field(img), c.registryPrefix(project), c.Ghcr)
	}
	if cmd, args := strList(ctr["command"]), strList(ctr["args"]); len(cmd) != 0 || len(args) != 0 {
		return "a container that sets a command or args, which the workspace root does not: the image's own entrypoint is the service"
	}
	if unknownAt(unk, "template", 0, "containers", 0, "env") {
		return "an environment that is not known"
	}
	mode := false
	for i, e := range objects(ctr["env"]) {
		ep := []any{"template", 0, "containers", 0, "env", i}
		for _, attr := range []string{"name", "value", "value_source"} {
			if unknownAt(unk, append(slices.Clone(ep), attr)...) {
				return "an environment variable that is not known"
			}
		}
		n := str(e["name"])
		if !slices.Contains(WorkspaceServiceEnv, n) {
			return fmt.Sprintf("an environment variable %q, which the workspace's service does not read: its container sets only %s (%s)", safetext.Field(n), strings.Join(WorkspaceServiceEnv, ", "), "the installation service's own class lists are not among them, because a member's job is admitted by the published node and the invoke grant, not by an environment")
		}
		if n == WorkspaceModeEnv {
			mode = true
		}
		// The rule is the block's CONTENTS and not the key's presence, for the
		// installation's own reason (cover.go's credentials service case): the
		// provider renders an unset nested block of list nesting mode as an
		// empty list rather than omitting it, so every env entry of a plan the
		// root writes carries `"value_source": []`.
		if refs := objects(e["value_source"]); len(refs) > 0 {
			return "an environment variable whose value comes from a source rather than the root, and the workspace's service reads its own names with the values the root writes: a value_source is a read of a container the plan does not name, and the workspace's bundle is read by the account's own accessor below rather than by an environment variable"
		}
	}
	if !mode {
		return fmt.Sprintf("a container that does not carry %s, and the workspace's own service is the same image and the same code as an installation's: %s is the one name that tells the two apart, so a service without it is an installation service running in the workspace's own project", WorkspaceModeEnv, WorkspaceModeEnv)
	}
	return ""
}

// workspaceZeroMinInstances holds the service level's scaling block to a
// literal zero: it is what the managed gate prices a Cloud Run service by, and
// an attribute the provider computes is unknown at plan time, so a service that
// does not state it is refused as unpriceable. The template's own block is held
// by workspaceTemplateLimits, beside R9's maximum: they are two different
// numbers with two different readers.
func workspaceZeroMinInstances(a map[string]any, unk any) string {
	if unknownAt(unk, "scaling") || unknownAt(unk, "scaling", 0, "min_instance_count") {
		return "a service-level scaling that is not known"
	}
	blocks := objects(at(a, "scaling"))
	if len(blocks) != 1 {
		return fmt.Sprintf("a service-level scaling that states %d blocks, and the workspace's own states one: the managed gate prices a service by its minimum instance count at both levels, so a service that states neither is refused as unpriceable", len(blocks))
	}
	if n, ok := blocks[0]["min_instance_count"].(float64); !ok || n != 0 {
		return fmt.Sprintf("a service-level scaling whose minimum instance count is %v, and the workspace's own service keeps no instance warm: the managed gate prices a service by its minimum at both levels, and an attribute the provider computes is unknown at plan time", blocks[0]["min_instance_count"])
	}
	return ""
}

// workspaceTemplateLimits holds the template's scaling block to R9: a maximum of
// WorkspaceMaxInstances instances and a minimum of zero, both written out
// literally in the root (the plan's acceptance names all three numbers, with the
// service level's zero above and the concurrency below).
func workspaceTemplateLimits(t map[string]any, unk any) string {
	if unknownAt(unk, "template", 0, "max_instance_request_concurrency") {
		return "a concurrency that is not known"
	}
	if n, ok := t["max_instance_request_concurrency"].(float64); !ok || n != float64(WorkspaceConcurrency) {
		return fmt.Sprintf("a concurrency is %v, and R9 states %d per instance: the number is a first value the load check at a hundred concurrent runs confirms or corrects (D2986), so it is a decision a person makes here rather than one terraform computes", t["max_instance_request_concurrency"], WorkspaceConcurrency)
	}
	if unknownAt(unk, "template", 0, "scaling") || unknownAt(unk, "template", 0, "scaling", 0, "min_instance_count") || unknownAt(unk, "template", 0, "scaling", 0, "max_instance_count") {
		return "a template scaling that is not known"
	}
	blocks := objects(at(t, "scaling"))
	if len(blocks) != 1 {
		return fmt.Sprintf("a template that states no scaling, and the workspace's own states one: R9's maximum of %d instances and the zero minimum a member's run pays a cold start for", WorkspaceMaxInstances)
	}
	if n, ok := blocks[0]["min_instance_count"].(float64); !ok || n != 0 {
		return fmt.Sprintf("a template whose minimum instance count is %v, and the workspace's service scales to zero: nothing keeps an instance of it warm, and a managed project's gate refuses a service whose floor it cannot read at either level", blocks[0]["min_instance_count"])
	}
	if n, ok := blocks[0]["max_instance_count"].(float64); !ok || n != float64(WorkspaceMaxInstances) {
		return fmt.Sprintf("a maximum instance count is %v, and R9 fixes the workspace's own service at %d instances: they are a first value the load check at a hundred concurrent runs confirms or corrects (D2986, reconciliation O1), and each instance holds every model credential the workspace does", blocks[0]["max_instance_count"], WorkspaceMaxInstances)
	}
	return ""
}

// workspaceBundleAccessor says why the member resource a plan creates or updates
// is not the ONE accessor on the workspace's own bundle container (F4, Q6): the
// container is not the one the caller derived, the role is not the read, the
// project is not the workspace's, or the principal is not the workspace's own
// account.
//
// bundleSecret is GIVEN, not read out of the plan, and that is the whole point
// of the parameter: the container's name is a function of the workspace's name
// (credsvc.WorkspaceBundleSecretID, which is gcp.ProjectSecretID of the logical
// name "creds-bundle"), no plan carries the derivation, and a rule that read the
// container out of the plan would accept whatever container the plan named —
// which is the one thing this grant must not do, because the value behind it is
// every model credential the workspace holds.
func workspaceBundleAccessor(ch Change, project, wantAccount, bundleSecret string) string {
	if bundleSecret == "" {
		return "a bundle accessor, and this plan is read by a caller that named no bundle container: the workspace's own bundle is derived from the workspace's name (credsvc.WorkspaceBundleSecretID), and a plan read without it admits no accessor at all"
	}
	if unknown(ch, "project") || unknown(ch, "secret_id") || unknown(ch, "role") || unknown(ch, "member") {
		return "a grant whose project, container, role or member terraform knows only at apply, in a root that writes all four"
	}
	if got := str(ch.After["project"]); got != project {
		return fmt.Sprintf("a bundle accessor in project %q, and the workspace's own bundle is %s's: the container this grant names is read in the workspace's own project and nowhere else", safetext.Field(got), safetext.Field(project))
	}
	if got := str(ch.After["secret_id"]); got != bundleSecret {
		return fmt.Sprintf("a grant on the container %q, and the workspace's own bundle is the container %s: the service reads the one container its own project holds, and a second one here is a secret read nobody decided on", safetext.Field(got), safetext.Field(bundleSecret))
	}
	if got := str(ch.After["role"]); got != WorkspaceBundleAccessorRole {
		return fmt.Sprintf("a grant of role %q, and the workspace's own service reads its bundle as %s and no other: what it reads is every model credential the workspace holds (Q6: one container, one accessor, no per-credential grant and no condition over the container)", safetext.Field(got), WorkspaceBundleAccessorRole)
	}
	m := str(ch.After["member"])
	switch {
	case m == "":
		return "no member"
	case workspaceIsAccount(m, wantAccount):
		return ""
	}
	return fmt.Sprintf("a bundle accessor for %s, and the workspace's own bundle is read by %s and by nobody else: a member's job account, a person, a group, a domain or a public principal here is a client that reads every model credential the workspace holds", safetext.Field(m), wantAccount)
}

// workspaceInvokeGrant says why an invoke grant of a plan is not a member's own
// (F17), in either of the two states the plan can hold it in: a create or an
// update, read from the change; or the ONE deletion this cover admits (F25),
// read from the state the workspace held. Both are held to the same four facts —
// the project, the role, the service and the principal — and the deletion is
// admitted only for the same member's account, so removing a member converges
// with no operator repair while a deletion of anything else is refused.
//
// service is the name of the plan's own workspace service
// (planWorkspaceServiceName): a member's grant is on THAT service and on no
// other, so a plan that binds the same role on a second service of the same
// project is refused rather than admitted by its type. An empty one — a plan
// with an invoke grant and no service — admits nothing.
func workspaceInvokeGrant(ch Change, deletion bool, project string, memberAccounts []string, service string) string {
	if deletion {
		if ch.Before == nil {
			return "a deletion that carries no state, and the one deletion this root's plan makes is a member's invoke grant as the workspace holds it"
		}
		b := ch.Before
		if got := str(b["project"]); got != project {
			return fmt.Sprintf("an invoke grant in project %q deleted, and the workspace's own service is %s's: a deletion in another project is another workspace's grant, and this root's cover admits no change to it", safetext.Field(got), safetext.Field(project))
		}
		if got := str(b["role"]); got != WorkspaceInvokerRole {
			return fmt.Sprintf("a grant of role %q deleted, and a member's run invokes the workspace's service as %s and no other: the invoke grant is the whole of what a member's job needs from Cloud Run, so a deletion here is a reach being taken away by a hand", safetext.Field(got), WorkspaceInvokerRole)
		}
		if got := str(b["name"]); got != service || service == "" {
			return fmt.Sprintf("an invoke grant on the service %q deleted, and the workspace's own service is this plan's own workspace service (%s): a grant on another service is another service's caller table", safetext.Field(got), safetext.Field(service))
		}
		m := str(b["member"])
		if email, ok := workspaceAccountOf(m); !ok || !slices.Contains(memberAccounts, email) {
			return fmt.Sprintf("an invoke grant deleted for %s, which is not a member's own job account of this plan: the ONE change this root's cover admits beyond its own resources is a member leaving the workspace's table, and a deletion of anything else — the workspace's own service, its bundle accessor, its account or its role — is a repair nobody chose (F25)", safetext.Field(m))
		}
		return ""
	}
	if unknown(ch, "project") || unknown(ch, "location") || unknown(ch, "name") || unknown(ch, "role") || unknown(ch, "member") {
		return "a grant whose project, location, service, role or member terraform knows only at apply, in a root that writes all five"
	}
	if got := str(ch.After["project"]); got != project {
		return fmt.Sprintf("an invoke grant in project %q, and the workspace's own service is %s's: a grant in another project admits another workspace's caller", safetext.Field(got), safetext.Field(project))
	}
	if service == "" {
		return "an invoke grant in a plan that holds no workspace service: a member's job account is admitted to the workspace's own service and to nothing else"
	}
	if got := str(ch.After["name"]); got != service {
		return fmt.Sprintf("an invoke grant on the service %q, and the workspace's own service is this plan's own workspace service (%s): a member's job account is admitted to the workspace's service and to no other", safetext.Field(got), safetext.Field(service))
	}
	if got := str(ch.After["role"]); got != WorkspaceInvokerRole {
		return fmt.Sprintf("a grant of role %q, and a member's run invokes the workspace's service as %s and no other: the invoke grant is the whole of what a member's job needs from Cloud Run — workspace mode serves the model and forge routes and refuses every other (R14) — so a second role here is a reach nobody decided on", safetext.Field(got), WorkspaceInvokerRole)
	}
	m := str(ch.After["member"])
	switch {
	case m == "":
		return "no member"
	case workspaceIsMemberOf(m, memberAccounts):
		return ""
	}
	return fmt.Sprintf("an invoke grant to %s, and the workspace's own service is invoked by a member's own job accounts alone: a person, a group, a domain, allUsers or another project's job here reaches the process that reads every model credential the workspace holds", safetext.Field(m))
}

// workspaceIsAccount reports whether an IAM member or a service's runtime
// account names the account address want, which a plan may spell with the
// serviceAccount: prefix or without it.
func workspaceIsAccount(got, want string) bool {
	e, ok := workspaceAccountOf(got)
	return ok && e == want
}

// workspaceIsMemberOf reports whether an IAM member names one of the workspace's
// member job accounts, in either spelling.
func workspaceIsMemberOf(got string, members []string) bool {
	e, ok := workspaceAccountOf(got)
	return ok && slices.Contains(members, e)
}

// workspaceAccountOf reads an IAM member as a service account address: the bare
// address, or the serviceAccount:-prefixed one. Anything else — a person, a
// group, a domain, allUsers, a principalSet — is not an account and reads as
// not one.
func workspaceAccountOf(member string) (string, bool) {
	if e, ok := strings.CutPrefix(member, "serviceAccount:"); ok {
		return e, e != ""
	}
	if member == "" || strings.Contains(member, ":") {
		return "", false
	}
	return member, true
}
