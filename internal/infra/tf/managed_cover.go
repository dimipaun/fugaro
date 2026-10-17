package tf

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The managed project's instantiation of Cover (Task 1.1 of
// docs/plans/2026-10-16-managed-projects.md).
//
// init's own cover (cover.go) answers "what does Fugaro's own installation
// need": its resource types are the ones the embedded modules declare, and its
// grant and role tables are the ones those modules write. A managed project's
// plan is written by someone else — the agent in the managed project — so the
// question is a different one: what may an agent-authored plan contain in a
// project Fugaro owns, given that the identity applying it (D75's deployer
// service account, Task 1.5) holds exactly what the managed allowlist admits
// and nothing wider. The list below is derived from allowedTypes and is
// deliberately NOT identical to it; deriving it is a decision rather than a
// copy (design finding 22, D61).
//
// Two directions decide an entry, and leaving one out is the safe one: a
// refused plan falls back to a confirmation, which is a working program, while
// a permitted plan that should not have been is the failure this gate exists
// to avoid. So the list is smaller than init's — the twelve entries in
// managedDropped are Fugaro's own plumbing, the project-level IAM authority
// the deployer must not hold, or a type whose resources GCP names on the
// server's side, where no prefix condition can tell the agent's from Fugaro's.

// ManagedPrefix is the name prefix every resource a managed plan creates must
// carry: the deployer's scoped role is bound under an IAM condition admitting
// this prefix and nothing else (D75 (iii), Task 1.5), so a resource outside it
// is one the deployer cannot apply, and a resource inside it is one the plan
// created itself.
//
// It deliberately matches none of Fugaro's own names: the runs bucket
// (fugaro-runs-<gcp-project>), the dedicated state bucket of D77
// (fugaro-managed-tfstate-<gcp-project> — which is why "fugaro-managed-" is not
// a usable prefix), the installation's state bucket
// (fugaro-tfstate-<gcp-project>), the fixed accounts (fugaro-scheduler,
// fugaro-history, fugaro-token-signer), the per-job accounts
// (fugaro-<name>-<8 hex>), the project's default compute account
// (<project-number>-compute), the installation's repositories (fugaro-base,
// fugaro-ghcr) and the Firebase web key (fugaro-web).
// TestManagedPrefixDoesNotMatchFugarosOwnNames holds that.
const ManagedPrefix = "fmgd-"

// managedTypes are the resource types a plan in a managed project may contain
// — Task 1.1's decision, one entry of managedTypeReasons per line. Every entry
// is also an entry of allowedTypes: a type no module declares has no place on
// either list, and adding one here without deciding it there fails
// TestManagedAllowedTypesIsDecided.
var managedTypes = map[string]bool{
	"google_apikeys_key":                             true,
	"google_artifact_registry_repository":            true,
	"google_artifact_registry_repository_iam_member": true,
	"google_cloud_run_v2_job":                        true,
	"google_cloud_run_v2_job_iam_member":             true,
	"google_cloud_scheduler_job":                     true,
	"google_project_service":                         true,
	"google_secret_manager_secret":                   true,
	"google_secret_manager_secret_iam_member":        true,
	"google_service_account":                         true,
	"google_service_account_iam_member":              true,
	"google_storage_bucket":                          true,
	"google_storage_bucket_iam_member":               true,
	"google_storage_bucket_object":                   true,
}

// managedTypeReasons is the justification of every entry the managed list
// keeps: inherited from allowedTypes where the modules' reason still holds for
// an agent-authored plan, and stated where the managed reason differs from it.
var managedTypeReasons = map[string]string{
	"google_apikeys_key":                             "inherited: a managed project may create its own API keys, and the deployer creates one unconditioned and updates or deletes only an ID carrying the prefix, so Fugaro's own web key (fugaro-web) is out of reach. Accepted exposure: creation is unconditioned, so an agent can create an unrestricted key — a cost exposure on the project's billing, not an escalation, because a key carries no IAM",
	"google_artifact_registry_repository":            "inherited: a managed project's own images need a repository; the deployer creates one unconditioned and updates or deletes only a prefixed one, so Fugaro's own fugaro-base and fugaro-ghcr are out of reach",
	"google_artifact_registry_repository_iam_member": "inherited: a grant on a repository the plan itself created; the deployer's artifactregistry.repositories.setIamPolicy is conditioned on the prefix",
	"google_cloud_run_v2_job":                        "inherited: the workload a managed project runs. Its name must carry the prefix and its service account must be set and be one of the managed project's own — a job with no account runs as the project's default compute account, which holds Editor by default",
	"google_cloud_run_v2_job_iam_member":             "inherited: invoker grants on the plan's own jobs; the deployer's run.jobs.setIamPolicy is conditioned on the prefix",
	"google_cloud_scheduler_job":                     "inherited: the schedule a managed project runs; its name and the OIDC or OAuth account it calls as must both carry the prefix, so it can neither reach Fugaro's own jobs nor run as an account the plan did not create",
	"google_project_service":                         "inherited: a managed plan may enable an API. The deployer holds serviceusage.services.enable, get and list only, never disable, so a plan that disables a service by deleting its google_project_service cannot be applied — and the delete needs the owner's own list whatever the type is",
	"google_secret_manager_secret":                   "inherited: the container, never a version. The deployer holds no secretmanager.versions.access, so a managed plan can create a secret and cannot read one, and update or delete only a prefixed one",
	"google_secret_manager_secret_iam_member":        "inherited: a grant on a secret the plan itself created; the deployer's secretmanager.secrets.setIamPolicy is conditioned on the prefix",
	"google_service_account":                         "inherited: a managed project's workloads need accounts of their own; the deployer creates one unconditioned and updates or deletes only a prefixed account_id",
	"google_service_account_iam_member":              "inherited: a grant on an account the plan itself created; the deployer's iam.serviceAccounts.setIamPolicy is conditioned on the prefix, so the default compute account and Fugaro's own accounts stay out of reach",
	"google_storage_bucket":                          "inherited: a managed project's own storage; its name must carry the prefix, which keeps the runs bucket, the installation's state bucket and D77's managed state bucket out of the plan",
	"google_storage_bucket_iam_member":               "inherited: a grant on a bucket the plan itself created. The runs bucket is refused by name before the prefix is even read, and the managed state bucket by the prefix",
	"google_storage_bucket_object":                   "inherited: an object in a bucket the plan itself created — refused on the runs bucket by name, where the marker and the owner's delete list live, and on the state buckets by the prefix",
}

// managedDropped is why each entry of allowedTypes the managed list leaves out
// is not on it. A reason is required per entry (TestManagedAllowedTypesIsDecided),
// so an exclusion is a decision and not an omission, and a type added to
// allowedTypes by a later module fails that test until one of the two maps
// decides it.
var managedDropped = map[string]string{
	"google_billing_budget":                  "a billing-account permission no project role can hold: Fugaro's own budget is created by the owner-run installation apply (fugaro init --budget)",
	"google_firebase_project":                "the owner-run creating path (infra.AddFirebase) and the owner-run Firebase root own it, and adding Firebase is never by itself what makes a project managed (D73)",
	"google_firebase_database_instance":      "the owner-run Firebase root owns it, as its project does",
	"google_logging_log_view":                "Fugaro's own log plumbing (installation/logging.tf), applied by the owner-run installation root",
	"google_logging_log_view_iam_member":     "Fugaro's own log plumbing, applied by the owner-run installation root",
	"google_logging_project_bucket_config":   "Fugaro's own log plumbing, applied by the owner-run installation root",
	"google_logging_project_exclusion":       "Fugaro's own log plumbing: an exclusion in an agent-authored plan would hide the project's logs, applied by the owner-run installation root",
	"google_logging_project_sink":            "Fugaro's own log plumbing: a sink in an agent-authored plan would reroute the project's logs, applied by the owner-run installation root",
	"google_monitoring_alert_policy":         "their resource names are server-assigned (projects/<p>/alertPolicies/<id>), so no prefix condition can tell the agent's from Fugaro's own: an update or a delete would reach the policy the installation's alerts use (D78)",
	"google_monitoring_notification_channel": "their resource names are server-assigned (projects/<p>/notificationChannels/<id>), so no prefix condition can tell the agent's from Fugaro's own: an update or a delete would reach the email channel Fugaro's alerts use (D78)",
	"google_project_iam_custom_role":         "the owner-run setup creates the deployer's two roles and no managed plan may: the deployer holds no iam.roles.* (D75 (iii)), so it could not apply it, and an account that can edit its own role can widen itself",
	"google_project_iam_member":              "a managed plan binds no project-level IAM at all: the deployer holds no project-level IAM permission (D75 (iii)), which is what keeps it out of the authority that could grant itself object access to the runs bucket and forge the marker",
}

// ManagedAllowedTypes are the resource types a plan in a managed project may
// contain, sorted: the managed allowlist itself, the list a managed apply's
// cover judges a plan by.
func ManagedAllowedTypes() []string {
	return slices.Sorted(maps.Keys(managedTypes))
}

// managedDroppedTypes are the entries of allowedTypes the managed list leaves
// out, sorted.
func managedDroppedTypes() []string {
	return slices.Sorted(maps.Keys(managedDropped))
}

// managedNames is the attribute a managed type's name — its own, or the name
// of the resource it is on — is read from. The plan must spell every one of
// them out: a name known only after apply is refused, whatever the plan
// creates beside it.
//
// Why no computed value is trusted, here or anywhere in this cover: a saved
// plan records which resources an expression references, never the expression
// itself, so "computed from a resource the plan creates" cannot be proved from
// it. bucket = "${substr(google_storage_bucket.b.name,0,0)}fugaro-runs-<p>"
// is unknown at plan time, references a created bucket, and applies as the
// runs bucket. init's own Cover may trust such a value (cover.go:255-263)
// because its plans are Fugaro's own modules; a managed plan is the agent's.
// The refusal falls back to the confirmation path, and the agent can always
// write the name literally — every name here is one the plan chooses.
var managedNames = map[string]string{
	"google_apikeys_key":                             "name",
	"google_artifact_registry_repository":            "repository_id",
	"google_artifact_registry_repository_iam_member": "repository",
	"google_cloud_run_v2_job":                        "name",
	"google_cloud_run_v2_job_iam_member":             "name",
	"google_cloud_scheduler_job":                     "name",
	"google_secret_manager_secret":                   "secret_id",
	"google_secret_manager_secret_iam_member":        "secret_id",
	"google_service_account":                         "account_id",
	"google_storage_bucket":                          "name",
	"google_storage_bucket_iam_member":               "bucket",
	"google_storage_bucket_object":                   "bucket",
}

// managedGrantRules are the *_iam_member types a managed plan may grant a role
// through: the five the managed list keeps. Every other *_iam_member type is
// refused for its type before a grant is looked at.
var managedGrantRules = map[string]bool{
	"google_artifact_registry_repository_iam_member": true,
	"google_cloud_run_v2_job_iam_member":             true,
	"google_secret_manager_secret_iam_member":        true,
	"google_service_account_iam_member":              true,
	"google_storage_bucket_iam_member":               true,
}

// ManagedCover is the managed project's instantiation of tf.Cover: the gate a
// managed apply hands managed.Spec.Cover (internal/managed/plan.go:40), with
// Cover's own inputs — the projects, the members the review listed, the
// region, the buckets, the registry and the billing account — taken from it
// unchanged, because they are the review's and not this list's.
//
// What it adds to Cover is the managed answer to four questions, each of which
// is a refusal rather than an inheritance:
//
//   - the type: managedTypes rather than allowedTypes, so the twelve entries
//     of managedDropped ask for a confirmation instead;
//   - the resource: every name a managed resource carries must carry
//     ManagedPrefix, and the accounts a job or a scheduler runs as must be the
//     managed project's own, so a plan cannot reach Fugaro's resources, the
//     runs bucket or the default compute account;
//   - the grant: a role is granted only on such a resource, only to a member
//     the review listed or a managed account, and only with the role spelled
//     out — a managed plan creates no custom role, so a computed role could be
//     one the deployer holds no iam.roles.* to set;
//   - the runs bucket, by name: a bucket grant on it is refused before the
//     prefix is read, because that is where the marker and the owner's delete
//     list live (Task 1.4).
//
// And one rule under all four: every value the cover reads — a name, the
// account a job, a scheduler or a grant is on, a member, a role — must be
// known at plan time. A computed one is refused, never trusted for what it
// references (managedNames says why).
type ManagedCover struct {
	Cover
}

// NotCovered lists why a managed project's plan is not covered, empty when it
// is. It is Cover.NotCovered's own walk — the action set, the authoritative
// IAM resources, the imports and the two halves in that order — with the four
// managed rules above in place of init's type list and grant table, and with
// no attribute check of the embedded modules': what a managed resource must
// *point at* rather than what it must be named is Task 1.2's, and its
// re-derivation of those checks belongs there rather than here. The one hole
// that leaves is stated rather than left to be found: until Task 1.2 lands,
// this cover does not check the project a resource is in, so a plan naming a
// second project is refused by nothing here. It is refused one step later all
// the same — the deployer's IAM reaches the managed project alone, so such a
// plan cannot be applied — and closing it in the gate is Task 1.2's first
// check rather than a second implementation of it here.
func (c ManagedCover) NotCovered(p *Plan, imports []ImportKey) []string {
	if p == nil {
		return []string{"no plan"}
	}
	var out []string
	add := func(rc ResourceChange, why string) { out = append(out, rc.Address+" ("+why+")") }
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		switch {
		case len(acts) == 0:
			add(rc, "no actions")
			continue
		case !allOf(acts, "create", "update", "no-op", "read"):
			add(rc, "actions "+strings.Join(acts, ", "))
			continue
		}
		if allOf(acts, "no-op", "read") && rc.Change.Importing == nil {
			continue
		}
		t := rc.Type
		switch {
		case strings.HasSuffix(t, "_iam_binding") || strings.HasSuffix(t, "_iam_policy") || strings.HasSuffix(t, "_iam_audit_config"):
			add(rc, "an authoritative IAM resource")
		case t == "google_service_account_key":
			add(rc, "a service account key")
		case !managedTypes[t]:
			add(rc, c.managedTypeReason(t))
		case rc.Change.Importing != nil && !c.importOK(rc.Change.Importing.ID):
			add(rc, "imports "+rc.Change.Importing.ID+", which is not in the managed project's projects")
		case rc.Change.Importing != nil && !slices.Contains(imports, ImportKey{rc.Address, rc.Change.Importing.ID}):
			add(rc, "imports "+rc.Change.Importing.ID+", which this run's discovery did not find")
		case c.managedResource(rc) != "":
			add(rc, c.managedResource(rc))
		case strings.HasSuffix(t, "_iam_member"):
			if why := c.managedGrant(rc); why != "" {
				add(rc, why)
			}
		}
	}
	return out
}

// managedTypeReason says why a type is not a managed plan's, naming the
// decision when the managed list dropped an entry allowedTypes allows and the
// plain fact when the type was never on either list.
func (c ManagedCover) managedTypeReason(t string) string {
	if why, ok := managedDropped[t]; ok {
		return "a resource type the managed project's allowlist leaves out: " + why
	}
	return "a resource type outside the managed project's allowlist"
}

// managedResource says why a managed resource is not one the plan may contain:
// its own name must carry the prefix, the accounts it runs as must be the
// managed project's own, and its bucket must not be the runs bucket — which is
// refused by name, before the prefix rule answers a different question about
// it. "" when it is the plan's own resource.
func (c ManagedCover) managedResource(rc ResourceChange) string {
	a := rc.Change.After
	if a == nil {
		a = map[string]any{}
	}
	// The name first: a computed bucket reads as "" and would pass the by-name
	// rule below unseen, so it is refused before that rule reads it.
	if attr, ok := managedNames[rc.Type]; ok && unknown(rc.Change, attr) {
		return rc.Type + " whose " + attr + " is not known at plan time: write it out literally, starting " + ManagedPrefix
	}
	// Rule (b): the installation's own runs bucket, by name, wherever a plan
	// points at it. The marker and the owner's delete list live at its root,
	// so it is the one bucket a managed plan never touches.
	if rc.Type == "google_storage_bucket_iam_member" || rc.Type == "google_storage_bucket_object" {
		if b := str(a["bucket"]); slices.Contains(c.Buckets, b) {
			return "on bucket " + b + ", which is not the managed project's"
		}
	}
	if attr, ok := managedNames[rc.Type]; ok {
		if why := c.managedName(rc, attr); why != "" {
			return why
		}
	}
	switch rc.Type {
	case "google_cloud_run_v2_job":
		// A job with no service account runs as the project's default compute
		// account, which holds Editor by default: unset is refused, not read
		// as "no account to check".
		if unknownAt(rc.Change.AfterUnknown, "template", 0, "template", 0, "service_account") {
			return "a job whose service account is not known"
		}
		email := str(at(a["template"], "template", "service_account"))
		switch {
		case email == "":
			return "a job with no service account, which would run as the project's default compute account"
		case !c.managedAccount("serviceAccount:" + email):
			return "runs as " + email + ", which is not a service account of the managed project carrying the prefix " + ManagedPrefix
		}
	case "google_cloud_scheduler_job":
		ht := at(a["http_target"])
		if unknown(rc.Change, "http_target") ||
			unknownAt(rc.Change.AfterUnknown, "http_target", 0, "oauth_token", 0, "service_account_email") ||
			unknownAt(rc.Change.AfterUnknown, "http_target", 0, "oidc_token", 0, "service_account_email") {
			return "a scheduler whose service account is not known"
		}
		// Either token may carry the account; GCP takes one, and every one
		// the plan sets must be a managed account.
		var emails []string
		for _, tok := range []string{"oauth_token", "oidc_token"} {
			if e := str(at(ht, tok, "service_account_email")); e != "" {
				emails = append(emails, e)
			}
		}
		if len(emails) == 0 {
			return "a scheduler with no service account, which would call as the project's default compute account"
		}
		for _, email := range emails {
			if !c.managedAccount("serviceAccount:" + email) {
				return "calls as " + email + ", which is not a service account of the managed project carrying the prefix " + ManagedPrefix
			}
		}
	case "google_service_account_iam_member":
		// The account the grant is on, not the member it names: refused when
		// computed, for managedNames' reason.
		if unknown(rc.Change, "service_account_id") {
			return "a grant on an account that is not known at plan time: write service_account_id out literally, as projects/<project>/serviceAccounts/" + ManagedPrefix + "<id>@<project>.iam.gserviceaccount.com"
		}
		id := str(a["service_account_id"])
		switch {
		case id == "":
			return "a service account grant on no account"
		case !c.managedAccount(accountMember(id)):
			return "on " + id + ", which is not a service account of the managed project carrying the prefix " + ManagedPrefix
		}
	}
	return ""
}

// managedName says why a managed resource's name, known at plan time
// (managedResource refuses a computed one first), is not one a managed plan may
// carry: empty, or outside the managed prefix.
func (c ManagedCover) managedName(rc ResourceChange, attr string) string {
	n := str(rc.Change.After[attr])
	switch {
	case n == "":
		return rc.Type + " with no " + attr
	case !strings.HasPrefix(n, ManagedPrefix):
		return fmt.Sprintf("%s %q, outside the managed prefix %s", attr, n, ManagedPrefix)
	}
	return ""
}

// managedGrant says why a role a managed plan grants is not one it may grant:
// the resource it is on is managedResource's, the member must be one the
// review listed or a managed account of the managed project, and the role must
// be spelled out. "" when the grant is one a managed plan may make.
func (c ManagedCover) managedGrant(rc ResourceChange) string {
	if !managedGrantRules[rc.Type] {
		return "a grant of a type a managed plan may not make"
	}
	ch := rc.Change
	// A computed member is refused even beside an account the plan creates,
	// for managedNames' reason: the plan cannot show that it names that
	// account and not allUsers. A grant to a created account names it.
	if unknown(ch, "member") {
		return "a member that is not known at plan time: name the account literally, as serviceAccount:" + ManagedPrefix + "<id>@<project>.iam.gserviceaccount.com"
	}
	m := str(ch.After["member"])
	switch {
	case m == "":
		return "no member"
	case m == "allUsers" || m == "allAuthenticatedUsers":
		return fmt.Sprintf("grants to %s, which is everyone", m)
	case strings.HasPrefix(m, "domain:"):
		return fmt.Sprintf("grants to %s, a whole domain", m)
	case c.managedAccount(m):
	case slices.Contains(c.Listed, m):
		if !strings.HasPrefix(m, "user:") && !strings.HasPrefix(m, "group:") {
			return fmt.Sprintf("grants to %s, which is not a user or a group", m)
		}
	default:
		return fmt.Sprintf("grants to %s, who is not on the review screen and is not a service account of the managed project", m)
	}
	if unknown(ch, "role") {
		return "a role that is not known"
	}
	switch r := str(ch.After["role"]); {
	case r == "":
		return "no role"
	case slices.Contains(PrimitiveRoles(), r):
		// The project's basic roles are project-level authority whatever they
		// are bound on, and a managed plan binds none: the deployer holds no
		// project-level IAM permission at all (D75 (iii)).
		return fmt.Sprintf("the project's basic role %s, which a managed plan does not grant", r)
	}
	return ""
}

// PrimitiveRoles are the project's basic roles — owner, editor and viewer —
// the roles that mean "the project" rather than "this resource", whichever
// resource they are written on. It is the one enumeration of them: the
// managed cover refuses each, and the modules' own test
// (deploy/terraform/terraform_test.go, checkLiteralRole) refuses each in the
// embedded modules. A fresh slice on every call, so no caller can edit it.
func PrimitiveRoles() []string {
	return []string{"roles/owner", "roles/editor", "roles/viewer"}
}

// accountMember reads a service account id as a member string: the plan writes
// one as the account's email or as projects/<p>/serviceAccounts/<email>.
func accountMember(id string) string {
	if i := strings.Index(id, "/serviceAccounts/"); i >= 0 {
		id = id[i+len("/serviceAccounts/"):]
	}
	return "serviceAccount:" + id
}

// managedAccount: the member is a service account of the managed project whose
// ID carries the managed prefix — the shape every account a managed plan may
// grant on, run as or call as has.
func (c ManagedCover) managedAccount(m string) bool {
	sm := saMember.FindStringSubmatch(m)
	return sm != nil && slices.Contains(c.Projects, sm[2]) && strings.HasPrefix(sm[1], ManagedPrefix)
}
