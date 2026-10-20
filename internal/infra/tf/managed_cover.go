package tf

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
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
	"google_project_service":                         "inherited: a managed plan may enable an API. The deployer holds serviceusage.services.enable, get and list only, never disable, so a plan that disables a service by deleting its google_project_service cannot be applied — and the delete needs the owner's own list whatever the type is. Accepted exposure: enabling an API can make Google itself add project-level IAM grants for that API's default service accounts (the Compute Engine default account's Editor, a service agent's role), grants no plan shows; not usable today, because no type the list admits can run as those accounts — a job or a scheduler must run as a prefixed account of the plan's own, and a grant on an account is conditioned on the prefix",
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
	"google_compute_firewall":                "Fugaro's own compute-backend network (modules/compute/network.tf, main's compute root — the Cloud Batch backend, excluded from 0.9.0), applied by the owner-run compute root: a firewall rule in an agent-authored plan would open or close the network every run's VM stands on. Dropped on the merge of main into next-0.8.5 as the fail-closed default; keeping it is the owner's decision",
	"google_compute_router":                  "Fugaro's own compute-backend network (modules/compute/network.tf), applied by the owner-run compute root; dropped on the merge of main into next-0.8.5 as the fail-closed default, keeping it is the owner's decision",
	"google_compute_router_nat":              "Fugaro's own compute-backend network (modules/compute/network.tf): the NAT is the runs' egress path, applied by the owner-run compute root; dropped on the merge of main into next-0.8.5 as the fail-closed default, keeping it is the owner's decision",
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
// What it adds to Cover is the managed answer to six questions, each of which
// is a refusal rather than an inheritance:
//
//   - the delete (the design's D72/D74 — the plan carries no task of its own
//     for this rule, and Tasks 1.2 and 1.4 are where its material is): a delete
//     or a replace is admitted only for an entry of the owner's list that names
//     the change's address AND the identity of the object the change removes
//     (tf.Approves, plan Task 6.3 — an address on its own admits nothing), and
//     only when the resource it removes or creates is one the rules below
//     admit — the list alone never admits one (NotCovered);
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
//     list live (Task 1.4);
//   - the attributes, two of them and no more (managedProject, managedBilling
//     and managedProviderProject, which is the same project rule read on the
//     provider block that decides the project of a resource left to it, and
//     which admits that block's project written out and nothing else): the
//     project every resource is in, kept as the wider rule Cover.Projects is,
//     and the billing account the review showed. Every other attribute check
//     init's cover makes is re-justified there, and the ones that only make
//     sense for Fugaro's own embedded modules are dropped rather than
//     inherited.
//
// Nor does a managed plan lock anything: a bucket's locked retention policy or
// an object's Locked retention can never be undone, so it is refused whatever
// the delete rule would later allow (managedBucketLock, managedObjectLock).
//
// And one rule under all six: every value the cover reads — a name, the
// account a job, a scheduler or a grant is on, a member, a role — must be
// known at plan time. A computed one is refused, never trusted for what it
// references (managedNames says why).
//
// And one rule before all six, because a managed apply runs as the operator
// (D75: the operator's shell impersonates the deployer): nothing in the plan
// runs at apply besides Google's own provider making the resources' API
// calls. No provisioner anywhere, no provider but Google's two, no data
// source, and no object uploaded from a local path — read from the plan's
// configuration and each change's mode and provider, which is why a plan
// without a configuration is refused (managedConfiguration). Nor does the
// plan choose who applies, who is billed or where the requests go: a provider
// block sets project, region, zone and labels alone (managedProviderAllowed),
// and its project always, written out (managedProviderProject).
// No action, deferred change or ephemeral resource either, and any key of the
// plan, the configuration or a module the cover does not know is refused
// rather than read as inert (managedPlanFields). What the plan JSON does not
// show at all — the backend, provider_meta, an unreferenced ephemeral block —
// is Task 3.1's to refuse before init.
type ManagedCover struct {
	Cover
	// AllowDelete is the owner's delete list: the address and the identity of
	// the object that was there when the owner approved it, handed over by
	// WithDeleteEntries beside the plan — the same list tf.GuardEntries is
	// handed, so the two cannot be given different ones. It is a copy of the
	// caller's slice, so a cover cannot edit the list the guard was given.
	// Absent and empty are the same thing here: no delete and no replace at
	// all.
	AllowDelete []DeleteEntry
}

// Deletable is a cover that can be told the owner's delete list, and the one
// thing a cover must be for a delete or a replace to be judged at all. The
// list is an input of the gate beside the plan (design D72/D74), so
// a cover that cannot be told it cannot judge a delete — and
// managed.Spec.notCovered refuses a listed one rather than letting the address
// alone admit a resource nothing read.
type Deletable interface {
	NotCovered(p *Plan, imports []ImportKey) []string
	WithDeleteEntries(entries []DeleteEntry) Deletable
}

// WithDeleteEntries is the managed cover with the owner's delete list, which
// is what a managed apply's gate hands it. A copy, so the list the guard is
// handed and the list this cover holds are the same list rather than two that
// can drift.
func (c ManagedCover) WithDeleteEntries(entries []DeleteEntry) Deletable {
	c.AllowDelete = slices.Clone(entries)
	return c
}

// NotCovered lists why a managed project's plan is not covered, empty when it
// is. The configuration comes first (managedConfiguration) and the provider
// blocks' own projects with it (managedProviderProject), then each change's
// mode and provider, and then Cover.NotCovered's own walk — the action set,
// the authoritative IAM resources, the imports and the two halves in that
// order — with the four
// managed rules above in place of init's type list and grant table, and with
// the two attribute checks in place of the embedded modules' own: the billing
// account a resource names (managedBilling, read before the type rule so that
// it is read at all) and the project it is in (managedProject).
//
// A delete and a replace are the one part of that walk the list
// changes: they are admitted for an entry of the owner's list that names the
// change's address and the identity of the object it removes, the pair
// tf.RemoveIdentityText prints — managedActions says which action sets they
// are — and the resource is then judged by the same chain as a create, on the
// view of it the change shows (removed says which), so a listed address whose
// resource is not one a managed plan may carry is refused for the resource.
// An address alone admits nothing (Task 6.3, tf.Approves): the entry the owner
// pastes from a refusal carries both halves, so an approval that outlived the
// object it was given for, or a plan that put another object under the
// address, is refused here. The list is what managed.Spec.notCovered hands
// this cover beside the plan, the same entries tf.GuardEntries is handed, and
// a cover that was handed none admits no delete at all.
//
// The whole chain is read on the removed view, and the two lock rules with it
// (managedBucketLock, managedObjectLock): a listed delete of a bucket or an
// object whose retention is already locked is refused. The lock is then a fact
// of the state in before rather than a setting the plan writes, which is not
// what those rules were written about — they are about what a managed plan
// locks — and the refusal is deliberate rather than incidental: a lock can
// never be undone and holds the resource, its bucket and the project, so a
// resource in that state is not one the allowlist admits, and reading the chain
// in full is the fail-closed direction. A narrower reading would be a rule
// loosened for deletes, which is what this task must not do. The refusal falls
// back to the confirmation, as every refusal does.
func (c ManagedCover) NotCovered(p *Plan, imports []ImportKey) []string {
	if p == nil {
		return []string{"no plan"}
	}
	out := append(managedPlanFields(p), managedConfiguration(p)...)
	out = append(out, c.managedProviderProject(p)...)
	declared := managedConfigResources(p.Configuration)
	add := func(rc ResourceChange, why string) { out = append(out, safetext.Field(rc.Address)+" ("+why+")") }
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		// What runs besides the resource's API calls, before its actions: a
		// no-op still loads its provider, and a read is a data source.
		switch {
		case rc.Mode != "managed":
			add(rc, managedModeReason(rc.Mode))
			continue
		case !managedProviders[rc.ProviderName]:
			add(rc, managedProviderReason(rc.ProviderName))
			continue
		}
		// The actions, and then the resource. A delete, or a replace in either
		// of terraform's two orders, is admitted only for an entry of the
		// owner's list that names its address AND the identity of the object
		// the change removes (D72/D74, plan Task 6.3) — the same list
		// tf.GuardEntries is handed, compared the same way: no prefix, module
		// or instance key stands in for an address, so a listed x[0] admits
		// nothing of x[1], and an entry that names the address for another
		// object admits nothing at all (tf.Approves). The list is not the
		// decision about the resource: the resource is judged below exactly as
		// a create is, and a resource that is not one a managed plan may carry
		// is refused although its address is listed — otherwise the list would
		// be a way past every rule about the resource, which is the failure
		// this gate exists to avoid.
		// acts is one of managedActions' sets past the switch below, so a set
		// that names "delete" is a destroy or a replace, and the two are told
		// apart by how many actions it has.
		removes := slices.Contains(acts, "delete")
		destroy := slices.Equal(acts, []string{"delete"})
		switch {
		case len(acts) == 0:
			add(rc, "no actions")
			continue
		case !managedActions(acts):
			add(rc, "actions "+strings.Join(acts, ", "))
			continue
		case removes && !slices.ContainsFunc(c.AllowDelete, func(e DeleteEntry) bool { return e.Address == rc.Address }):
			// The identity of the object the plan removes is printed beside
			// the refusal, because the person who can approve it is the owner
			// and this is where they read which object the entry must name
			// (tf.RemoveIdentityText): without it the loop a refusal starts —
			// the owner reads the address, runs fugaro allow-delete — has
			// nothing to record but the address, which is no longer an
			// approval.
			add(rc, "actions "+strings.Join(acts, ", ")+": an address that is not on the owner's delete list (the plan removes "+RemoveIdentityText(rc)+")")
			continue
		// The list names an address, and the object a removal takes away is
		// the one in before: with a moved block that object was at another
		// address, so a listed address would admit the delete of an unlisted
		// one. Refused, as tf.Guard refuses it (removesUnderAnotherAddress).
		case removes && removesUnderAnotherAddress(rc):
			add(rc, "actions "+strings.Join(acts, ", ")+": "+movedRemovalReason(rc))
			continue
		case removes && !managedRemovalReasons[rc.ActionReason]:
			add(rc, "actions "+strings.Join(acts, ", ")+": action_reason "+rc.ActionReason+", which the managed delete rule does not know")
			continue
		// And the last of the list's own rules: the entry names the address,
		// and it must name the object the plan removes as well — an approval
		// that survived the delete it was given for, or a plan whose object
		// was replaced under the address, is refused here (Task 6.3).
		case removes:
			if ok, why := Approves(c.AllowDelete, rc); !ok {
				add(rc, "actions "+strings.Join(acts, ", ")+": "+why)
				continue
			}
		}
		if allOf(acts, "no-op", "read") && rc.Change.Importing == nil {
			continue
		}
		// Both resources of a replace are judged — the one it creates, read
		// from after as a create is, and the one it removes, read from before
		// (removed) — while a destroy has nothing in after to read and is
		// judged on the resource it removes alone.
		if !destroy {
			if why := c.notCoveredResource(rc, declared, imports); why != "" {
				add(rc, why)
				continue
			}
		}
		if removes {
			if why := c.notCoveredResource(removed(rc), declared, imports); why != "" {
				add(rc, why)
			}
		}
	}
	return out
}

// managedRemovalReasons are the action_reason values a listed delete or
// replace may carry, every one of them a removal of the address's own object:
// none (a plain destroy), the four replace reasons, and the deletes of an
// address whose configuration went away — its resource block, its module, its
// count or for_each, or one of its instance keys. delete_because_no_move_target
// is not here: it is a moved object with nowhere to go, the object of another
// address (removesUnderAnotherAddress refuses it first). A reason a later
// terraform adds is refused until it is read and added, fail closed like the
// action sets.
var managedRemovalReasons = map[string]bool{
	"":                                  true,
	"replace_because_tainted":           true,
	"replace_because_cannot_update":     true,
	"replace_by_request":                true,
	"replace_by_triggers":               true,
	"delete_because_no_resource_config": true,
	"delete_because_no_module":          true,
	"delete_because_wrong_repetition":   true,
	"delete_because_count_index":        true,
	"delete_because_each_key":           true,
}

// managedActions: the plan action sets a managed plan may carry. Terraform's
// own sets, whole and in order, and nothing else: create, update, no-op and
// read change or read a resource without removing it, and a delete or one of
// the two orders of a replace is admitted only with the owner's list (read
// beside this in NotCovered). Any other set — an action this list does not
// carry, a delete of a resource that is not shown as one, a set no version of
// terraform writes — is refused rather than read as one of these.
//
// forget is the one set worth naming, because it is a real terraform action
// that tf.Guard deliberately allows: a `removed` block takes a resource out of
// the state without destroying it, so the guard has nothing to refuse
// (guard.go:12-14). A managed plan may not carry it. The managed root's state
// is Fugaro's own (D77), and a forget writes it with no cloud change beside
// it: the resource would leave the plan while staying in the project, which is
// exactly the kind of change none of the rules here can judge — every one of
// them reads a resource's type, name, project and attributes, and none says
// what leaving the state means. Refused, fail closed like the rest: a plan
// that needs it is refused and falls back to the confirmation.
func managedActions(acts []string) bool {
	return allOf(acts, "create", "update", "no-op", "read") ||
		slices.Equal(acts, []string{"delete"}) ||
		slices.Equal(acts, []string{"create", "delete"}) ||
		slices.Equal(acts, []string{"delete", "create"})
}

// removed is the change as the resource it takes away: terraform writes a
// destroy with the resource in before and null in after, and a replace with
// both. Reading the rules on this view is what judges the removed resource —
// its name, its type, the account it is on — rather than reading the empty
// after a delete has.
func removed(rc ResourceChange) ResourceChange {
	rc.Change.After = rc.Change.Before
	rc.Change.AfterUnknown = nil
	// An import is about the resource the plan takes over, which is the after
	// view's; a removed resource is imported by nothing.
	rc.Change.Importing = nil
	return rc
}

// notCoveredResource is why the resource a change's view shows is not one a
// managed plan may contain, "" when it is: the rule chain NotCovered reads a
// create through, which a delete and both halves of a replace are judged by as
// well. rc is either the change itself (the resource the plan creates, in
// after) or removed(rc) (the resource it removes, in before).
func (c ManagedCover) notCoveredResource(rc ResourceChange, declared map[string]ConfigResource, imports []ImportKey) string {
	t := rc.Type
	switch {
	case strings.HasSuffix(t, "_iam_binding") || strings.HasSuffix(t, "_iam_policy") || strings.HasSuffix(t, "_iam_audit_config"):
		return "an authoritative IAM resource"
	case t == "google_service_account_key":
		return "a service account key"
	case c.managedBilling(rc) != "":
		// Before the type rule: google_billing_budget is off the managed
		// list, so read after it the billing account would never be read
		// (managedBilling).
		return c.managedBilling(rc)
	case !managedTypes[t]:
		return c.managedTypeReason(t)
	case rc.Change.Importing != nil && !c.importOK(rc.Change.Importing.ID):
		return "imports " + rc.Change.Importing.ID + ", which is not in the managed project's projects"
	case rc.Change.Importing != nil && !slices.Contains(imports, ImportKey{rc.Address, rc.Change.Importing.ID}):
		return "imports " + rc.Change.Importing.ID + ", which this run's discovery did not find"
	case c.managedResource(rc) != "":
		return c.managedResource(rc)
	case c.managedProject(rc, declared) != "":
		return c.managedProject(rc, declared)
	case strings.HasSuffix(t, "_iam_member"):
		return c.managedGrant(rc)
	}
	return ""
}

// managedProviders are the providers a managed plan may use: Google's own two,
// by their full source address. Any other is a binary the plan's author chose,
// run at apply with the credentials of whoever applies — under D75, the
// operator's shell impersonating the deployer.
var managedProviders = map[string]bool{
	"registry.terraform.io/hashicorp/google":      true,
	"registry.terraform.io/hashicorp/google-beta": true,
}

// managedProviderReason says why a provider is not a managed plan's.
func managedProviderReason(full string) string {
	if full == "" {
		return "a provider the plan does not name: a managed plan uses registry.terraform.io/hashicorp/google or google-beta alone"
	}
	return "provider " + full + ", which is not registry.terraform.io/hashicorp/google or google-beta: a managed plan runs no other provider"
}

// managedModeReason says why a change of a mode other than "managed" is not a
// managed plan's. No data source is on the managed list: one is code the
// provider runs to read, and data.external is a program the plan names, so a
// data source is refused whatever its type (the list of admitted ones is
// empty, and adding one is a decision for this file).
func managedModeReason(mode string) string {
	if mode == "data" {
		return "a data source, which a managed plan does not read"
	}
	return fmt.Sprintf("mode %q, which is not a managed resource", mode)
}

// managedConfiguration says why a plan's configuration is not one a managed
// plan may carry: a provisioner anywhere, in the root module or any module it
// calls however deep, a provider outside managedProviders, or a data source.
// resource_changes shows none of these — a provisioner is not an attribute, a
// data source the plan already read has no change, and a provider the
// configuration requires need not be the one any change names — so they are
// read here, and a plan with resource changes and no configuration to read
// them from is refused rather than taken to have none.
//
// What this does not cover is what ran before the gate: providers start at
// init and plan, file() and templatefile() read at plan, and a data source is
// read at plan when it can be. Refusing those plans here stops their apply,
// not their plan; plan-time execution is Task 3.x's.
func managedConfiguration(p *Plan) []string {
	cfg := p.Configuration
	// An empty configuration is no configuration. terraform's own
	// `show -json` always writes a configuration with a root_module (and a
	// provider_config), so "{}" is a document nothing here wrote — and read
	// as a configuration, it has no provisioner, no provider and no data
	// source, so every rule below passes and the plan is taken to have none.
	// That is the one reading this function exists to refuse.
	//
	// raw is set only by UnmarshalJSON, so it is what tells a configuration
	// read out of a document — the only one that can be the empty object —
	// from one a fixture built in Go, which carries no keys for the same
	// reason: it never went through the decoder.
	if cfg == nil || (cfg.raw != nil && len(cfg.fields) == 0) {
		if len(p.ResourceChanges) == 0 {
			return nil
		}
		return []string{"the plan (no configuration: a managed plan's provisioners, providers and data sources are read from it, so a plan without one is refused rather than read as having none)"}
	}
	out := unknownFields("configuration", cfg.fields, managedConfigurationKeys)
	for _, key := range slices.Sorted(maps.Keys(cfg.ProviderConfig)) {
		pc := cfg.ProviderConfig[key]
		if !managedProviders[pc.FullName] {
			out = append(out, "provider_config."+key+" ("+managedProviderReason(pc.FullName)+")")
		}
		for _, why := range managedProviderSettings(pc) {
			out = append(out, "provider_config."+key+" ("+why+")")
		}
	}
	out = append(out, managedEphemeralReferences(cfg.raw)...)
	var walk func(prefix string, m ConfigModule)
	walk = func(prefix string, m ConfigModule) {
		where := "configuration.root_module"
		if prefix != "" {
			where = strings.TrimSuffix(prefix, ".")
		}
		out = append(out, unknownFields(where, m.fields, managedModuleKeys)...)
		for _, r := range m.Resources {
			addr := prefix + r.Address
			switch {
			case len(r.Provisioners) > 0:
				out = append(out, safetext.Field(addr)+" (a provisioner, which runs a command or a file copy at apply on the machine that applies: a managed plan declares none)")
			case r.Mode != "managed":
				out = append(out, safetext.Field(addr)+" ("+managedModeReason(r.Mode)+")")
			}
		}
		for _, name := range slices.Sorted(maps.Keys(m.ModuleCalls)) {
			call := m.ModuleCalls[name]
			// A call's own keys are read here, at the call, before the
			// module it calls: what the call passes is not an attribute of
			// any resource, so nothing further in the walk sees it.
			at := where + ".module_calls." + name
			out = append(out, unknownFields(at, call.fields, managedModuleCallKeys)...)
			out = append(out, managedModuleCallArguments(at, call.fields)...)
			walk(prefix+"module."+name+".", call.Module)
		}
	}
	walk("", cfg.RootModule)
	return out
}

// managedInertPlanKeys are the top-level keys of terraform's plan JSON that
// carry nothing a managed apply runs: the values, the changes the cover reads,
// the state before, the configuration it reads, and terraform's bookkeeping.
// Any other key carrying anything is refused (managedPlanFields): an action
// runs at apply and shows only in action_invocations, a deferred change is a
// resource the plan does not show yet, and a key a later terraform adds is one
// this cover has not read.
var managedInertPlanKeys = map[string]bool{
	"format_version": true, "terraform_version": true, "variables": true, "planned_values": true,
	"resource_drift": true, "resource_changes": true, "relevant_attributes": true, "output_changes": true,
	"prior_state": true, "configuration": true, "checks": true, "timestamp": true,
	"applyable": true, "complete": true, "errored": true,
}

// managedConfigurationKeys and managedModuleKeys are the keys of the
// configuration and of each of its modules the cover reads or knows inert;
// any other, such as a module's "actions", is refused when it carries
// anything.
var (
	managedConfigurationKeys = map[string]bool{"provider_config": true, "root_module": true}
	managedModuleKeys        = map[string]bool{"resources": true, "module_calls": true, "outputs": true, "variables": true}
	// managedModuleCallKeys are the keys of a module call this cover reads or
	// knows inert: what it calls (source, module) and the arguments it passes
	// (expressions), which are read where they land — a resource's own
	// expressions, or the variable a module's block declares — rather than at
	// the call. Anything else, a version constraint among them, is refused
	// when it carries anything.
	managedModuleCallKeys = map[string]bool{"source": true, "module": true, "expressions": true}
	// managedModuleCallMetaKeys are the keys of a call's expressions that are
	// not arguments: the meta-arguments that decide how many instances the
	// call makes (count, for_each), which provider configurations the called
	// module's resources use (providers) and what the call waits for
	// (depends_on). The cover reads none of them — it reads the provider
	// blocks by key, the resources by address, and nothing at all that a call
	// multiplies itself by — so a call carrying one is refused rather than
	// read as inert.
	managedModuleCallMetaKeys = map[string]bool{"count": true, "for_each": true, "providers": true, "depends_on": true}
)

// managedPlanFields says why the plan carries a top-level key a managed plan
// may not: one outside managedInertPlanKeys that is not empty.
func managedPlanFields(p *Plan) []string {
	return unknownFields("the plan", p.fields, managedInertPlanKeys)
}

// unknownFields lists, as refusals at where, the keys of fields outside known
// that carry anything: absent, null, [] and {} carry nothing.
func unknownFields(where string, fields map[string]json.RawMessage, known map[string]bool) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		if known[k] || emptyJSON(fields[k]) {
			continue
		}
		out = append(out, fmt.Sprintf("%s (carries %q, which the managed cover does not read: an action runs at apply and a deferred change is a resource the plan does not show, so a key it does not know is refused rather than read as inert)", where, k))
	}
	return out
}

// managedModuleCallArguments lists, as refusals at at, the meta-arguments a
// module call's expressions carry: the keys of managedModuleCallMetaKeys that
// carry anything. count and for_each decide how many instances the call makes,
// providers decides which provider configuration the called module's resources
// use, and depends_on decides what the apply waits for — none of them is an
// attribute of a resource the cover reads, and a plan whose shape the cover
// cannot see is refused rather than admitted on the assumption that the
// instances it multiplies into are all the ones the resource changes show.
//
// An expressions block that does not decode as an object is refused too: a
// call's arguments the cover cannot read are not read as carrying no
// meta-argument. Empty is nothing — absent, null, [] and {} — as in
// unknownFields.
func managedModuleCallArguments(at string, fields map[string]json.RawMessage) []string {
	raw, ok := fields["expressions"]
	if !ok || emptyJSON(raw) {
		return nil
	}
	var exprs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &exprs); err != nil {
		return []string{fmt.Sprintf("%s.expressions (does not decode as a module call's arguments: the managed cover reads them for the meta-arguments it refuses, so what it cannot read it does not call inert)", at)}
	}
	var out []string
	for _, k := range slices.Sorted(maps.Keys(exprs)) {
		if managedModuleCallMetaKeys[k] && !emptyJSON(exprs[k]) {
			out = append(out, fmt.Sprintf("%s.expressions.%s (a module call's meta-argument: it decides what the call makes rather than what any resource in it is, and a managed plan carries none)", at, k))
		}
	}
	return out
}

// emptyJSON: the value is null, an empty list or an empty object.
func emptyJSON(raw json.RawMessage) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch v := v.(type) {
	case nil:
		return true
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// managedProviderKeys are the fields of a provider_config entry the cover
// reads or knows inert. An alias is read, and refused (managedProviderSettings).
var managedProviderKeys = map[string]bool{
	"name": true, "full_name": true, "alias": true, "module_address": true, "version_constraint": true, "expressions": true,
}

// managedProviderAllowed are the settings a managed plan's provider block may
// carry, in any module: where its resources land by default and the labels
// they carry, nothing else. Every other setting of Google's provider decides
// who applies (credentials, access_token, impersonate_service_account and its
// delegates), who is billed (billing_project, user_project_override), or
// where every request — the applier's bearer token with it — is sent (the
// *_custom_endpoint settings, universe_domain), and is refused.
var managedProviderAllowed = map[string]bool{
	"project": true, "region": true, "zone": true, "default_labels": true,
	"add_terraform_attribution_label": true, "terraform_attribution_label_addition_strategy": true,
}

// ManagedProviderSetting reports whether a managed root's provider block may
// carry the setting name: the same list the cover reads the plan's provider
// configuration with (managedProviderAllowed), for the managed apply's
// preflight, which reads the root's own files BEFORE init — the provider is
// configured at plan time with the deployer's credential, so a custom endpoint
// or a credential setting has to be refused before the plan the cover reads
// exists (internal/managed's preflight).
func ManagedProviderSetting(name string) bool { return managedProviderAllowed[name] }

// managedProviderSettings says why a provider block carries what a managed
// plan's may not: an alias (a second configuration of the provider is a
// second set of settings a module can be handed), a field of the entry the
// cover does not know, a setting outside managedProviderAllowed, or an
// allowed one whose value is neither written out nor read from variables
// alone — a local, a resource, a data source or an ephemeral resource can
// carry what the plan does not show, and a function call of no variable shows
// as neither. (One whose arguments read a variable shows as that variable,
// which is why the project, the one setting whose value decides where the
// resources land, is admitted written out alone: managedProviderProject.)
func managedProviderSettings(pc ProviderConfig) []string {
	var out []string
	if pc.Alias != "" {
		out = append(out, fmt.Sprintf("alias %q: a managed plan configures each provider once, without an alias", pc.Alias))
	}
	for _, k := range slices.Sorted(maps.Keys(pc.fields)) {
		if !managedProviderKeys[k] {
			out = append(out, fmt.Sprintf("carries %q, a field of a provider block the managed cover does not read", k))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(pc.Expressions)) {
		if !managedProviderAllowed[k] {
			out = append(out, fmt.Sprintf("setting %s, which a managed plan's provider block may not carry: it sets only %s", k, strings.Join(slices.Sorted(maps.Keys(managedProviderAllowed)), ", ")))
			continue
		}
		if !managedSettingForm(pc.Expressions[k]) {
			fix := "write it out, or read it from a variable"
			if k == "project" {
				fix = "write it out"
			}
			out = append(out, fmt.Sprintf("setting %s from something other than a literal or variables alone: %s", k, fix))
		}
	}
	return out
}

// managedExpr is one expression as terraform's configuration JSON writes it.
type managedExpr struct {
	ConstantValue json.RawMessage `json:"constant_value"`
	References    []string        `json:"references"`
}

// managedSettingForm: the expression is written out, or read from variables
// alone. A function call with no reference writes neither and fails it; one
// whose arguments read a variable writes that variable's reference and
// nothing else, exactly as the variable itself does, so for the project this
// is the form check alone and managedProviderProject admits a literal only.
func managedSettingForm(raw json.RawMessage) bool {
	var e managedExpr
	if json.Unmarshal(raw, &e) != nil {
		return false
	}
	if e.ConstantValue != nil {
		return len(e.References) == 0
	}
	for _, r := range e.References {
		if !strings.HasPrefix(r, "var.") {
			return false
		}
	}
	return len(e.References) > 0
}

// managedEphemeralReferences says which ephemeral resources an expression of
// the configuration references, anywhere: a resource's, a module call's, a
// provider's, an output's. An ephemeral resource is opened at apply with the
// applier's credentials (google_service_account_access_token mints a token
// for an account the applier may impersonate), and terraform leaves the
// block itself out of the plan's configuration, so a reference to one is
// where a plan shows it. One that nothing references, or that only a local
// does, is not in the plan at all: Task 3.1 refuses the block in the working
// directory's .tf files. Nor is one named only in a depends_on: this walk
// reads an expression's references and nothing else, and a resource's or a
// module's depends_on is a list of its own, so an ephemeral resource a plan
// names there alone is still opened at apply. Task 3.1's refusal of every
// ephemeral block in the .tf and .tf.json files is what covers it, and its
// required residuals say so.
func managedEphemeralReferences(raw json.RawMessage) []string {
	var doc any
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if refs, ok := v["references"].([]any); ok {
				for _, r := range refs {
					if s, _ := r.(string); strings.HasPrefix(s, "ephemeral.") {
						// The block's own address: terraform lists the
						// reference, its instance and the block.
						parts := strings.SplitN(s, ".", 4)
						if len(parts) >= 3 {
							s = strings.Join(parts[:2], ".") + "." + strings.SplitN(parts[2], "[", 2)[0]
						}
						seen[s] = true
					}
				}
			}
			for _, e := range v {
				walk(e)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(doc)
	var out []string
	for _, r := range slices.Sorted(maps.Keys(seen)) {
		out = append(out, "configuration (references "+r+": an ephemeral resource is opened at apply with the credentials of whoever applies, and a managed plan opens none)")
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
	case "google_storage_bucket":
		if why := managedBucketLock(rc.Change); why != "" {
			return why
		}
	case "google_storage_bucket_object":
		if why := managedObjectLock(rc.Change); why != "" {
			return why
		}
		// source is a path on the machine that applies — the operator's, under
		// D75 — and the provider uploads whatever file it names. A managed
		// object carries its content in the plan or none; a computed source is
		// refused for managedNames' reason.
		if unknown(rc.Change, "source") || str(a["source"]) != "" {
			return "an object uploaded from source, a local path on the machine that applies: write its content in the plan instead"
		}
	case "google_artifact_registry_repository":
		// A virtual repository reads through to an upstream repository, which
		// can be Fugaro's own fugaro-base, and a remote one through to an
		// outside registry with a password from a secret, which can be
		// Fugaro's: the prefix covers the repository and not what it reads.
		// The provider writes the default mode into the plan when it is unset.
		if m := str(a["mode"]); m != "STANDARD_REPOSITORY" {
			return fmt.Sprintf("a repository of mode %q, which is not STANDARD_REPOSITORY: a virtual or remote repository reads from an upstream the prefix does not cover", m)
		}
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

// managedProject says why a managed resource is in a project that is not this
// run's, or in one the plan cannot prove is. "" when it is in one, when it sets
// none, and when it is known only after apply because the resource leaves it to
// its provider block. It is the first of Task 1.2's two retained attribute checks, and this
// comment is the whole re-derivation of Cover.attributes (cover.go:713-827)
// for an agent-authored plan: each of init's checks is re-justified rather
// than inherited, the ones that only make sense for Fugaro's own embedded
// modules are dropped and said to be dropped, and no check outside this set is
// added — D61 fixes the checkable set and closes it, so a rule that is not on
// it is a new decision rather than an implementation detail.
//
//   - the project every resource is in: KEPT, and kept as the WIDER rule it
//     is in Cover.Projects — the review's projects, not the managed project
//     alone — so a resource in a second project Fugaro owns is refused too.
//     This is the spec's step 3 and D61's own second entry. A project the plan
//     knows only after apply is two cases the plan's value cannot tell apart,
//     because terraform writes after_unknown.project true for both: a resource
//     that leaves project to its provider block
//     (testdata/managed_provider_clean_plan.json), and one that sets it to
//     something computed — project = "${substr(
//     google_service_account.a.unique_id,0,0)}proj-2", or a module's var.p
//     fed such a value, applies into proj-2
//     (testdata/managed_resource_computed_project_plan.json and its module and
//     instances siblings, terraform's own output). The configuration tells
//     them apart: an unknown project is admitted only when the resource's
//     configuration entry carries no project expression at all, and refused
//     when it carries one, whatever its form, for managedNames' reason — or
//     when the entry cannot be found, which fails closed. A resource left to
//     its provider block lands in the project that block decides, which is
//     why that block's own project is read too (managedProviderProject)
//     rather than left to Task 1.1's form check: the escape this rule would
//     otherwise have is a plan that moves the project one line up.
//   - the billing account the review showed: KEPT, in managedBilling beside
//     this, which says why it is read before the type rule rather than after.
//   - the image a job runs, the command and args the modules set and the
//     FUGARO_CHECK_SPEC it carries: dropped. They are Fugaro's own images,
//     commands and check specs — init's cover reads them because its plans are
//     the embedded modules' — and a managed plan's workload is its own.
//   - the job a scheduler calls: dropped. init's cover requires the run's own
//     project and region; a managed scheduler's account is checked by
//     managedResource instead, which requires the account it calls as to be a
//     prefixed account of the managed project, so it calls as nothing of
//     Fugaro's.
//   - the sink's project: dropped with its type, which the managed list does
//     not carry (managedDropped: Fugaro's own log plumbing).
//   - a bucket's public access prevention and uniform access: dropped. The
//     rule is about Fugaro's own buckets, which hold the installation's state,
//     runs and build records; a managed plan's bucket is the plan's own, and
//     the bucket rules this gate needs are Task 1.1's — the name carries the
//     prefix and the runs bucket is refused by name.
//   - the two API targets of Fugaro's web key: dropped. init's cover requires
//     a key to be that key's shape; a managed plan's key is its own, and the
//     prefix covers the ID the deployer's apikeys.keys.* permissions are
//     conditioned on.
//   - the bucket or the repository a grant or an object is on: dropped as
//     subsumed, because Task 1.1 checks the name every one of them carries —
//     the runs bucket by name, everything else by the prefix.
//
// What Task 1.1 left this task, from its fourth review (PR #483), is stated
// where it would go rather than left to be found: an Artifact Registry
// repository's virtual_repository_config.upstream_policies[*].repository and
// remote_repository_config.*.upstream_credentials are not read here, because
// no virtual or remote repository is admitted at all — managedResource refuses
// every mode but STANDARD_REPOSITORY — so there is nothing for them to be read
// on. Admitting those modes is the decision that would owe those checks (a
// prefixed repository of the managed project, and a prefixed secret), and it
// is the owner's rather than this cover's.
//
// declared is the configuration's resources by address
// (managedConfigResources).
func (c ManagedCover) managedProject(rc ResourceChange, declared map[string]ConfigResource) string {
	if unknown(rc.Change, "project") {
		r, found := declared[managedConfigAddress(rc.Address)]
		switch _, sets := r.Expressions["project"]; {
		case !found:
			return "a project known only after apply, of a resource the configuration does not declare, so the plan does not show whether the resource sets it: it is not proven to be this run's"
		case sets:
			return "a project the resource sets to something known only after apply, which the plan cannot prove is this run's: write it out, or leave it to the provider block"
		}
		return ""
	}
	if p := str(rc.Change.After["project"]); p != "" && !slices.Contains(c.Projects, p) {
		return "in project " + p + ", which is not this run's"
	}
	return ""
}

// managedConfigResources are the managed resources of a configuration, in
// every module however deep, by the address a resource change carries with
// its instance keys removed (managedConfigAddress): module.a.module.b.<type>.<name>.
// Empty when there is no configuration, which managedConfiguration refuses.
func managedConfigResources(cfg *Configuration) map[string]ConfigResource {
	out := map[string]ConfigResource{}
	if cfg == nil {
		return out
	}
	var walk func(prefix string, m ConfigModule)
	walk = func(prefix string, m ConfigModule) {
		for _, r := range m.Resources {
			if r.Mode == "managed" {
				out[prefix+r.Address] = r
			}
		}
		for name, call := range m.ModuleCalls {
			walk(prefix+"module."+name+".", call.Module)
		}
	}
	walk("", cfg.RootModule)
	return out
}

// managedConfigAddress is a resource change's address with every instance key
// removed — a count's [0], a for_each's ["key"], a module's own included —
// which is the address its configuration entry is found under. A key's quoted
// text is read as text, so a bracket, a dot or an escaped quote inside it ends
// nothing.
func managedConfigAddress(addr string) string {
	var b strings.Builder
	depth, quoted, escaped := 0, false, false
	for _, r := range addr {
		switch {
		case depth == 0 && r == '[':
			depth = 1
		case depth == 0:
			b.WriteRune(r)
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case !quoted && r == ']':
			depth = 0
		}
	}
	return b.String()
}

// managedBilling says why a resource names a billing account that is not the
// one the review showed. "" when it names none and when it names the review's.
// It is the second of Task 1.2's two retained attribute checks, and it is read
// before the type rule in NotCovered because google_billing_budget — the
// resource that carries a billing account — is off the managed list
// (managedDropped: a billing-account permission no project role can hold).
// Read after it, this rule could never fire: every budget would be refused for
// its type first and the account would go unread, which is a rule that exists
// only on paper. Read before it, a plan that moves billing is refused for the
// account it moves it to, which is this check's own reason for existing; a
// budget on the review's own account is still refused, for its type.
//
// It reads the attribute rather than the type, so it holds for whatever
// resource a later decision lets carry a billing account: a plan naming one
// outside the review is refused whether or not its type is admitted.
func (c ManagedCover) managedBilling(rc ResourceChange) string {
	a := rc.Change.After
	raw, ok := a["billing_account"]
	if !ok {
		return ""
	}
	acct := strings.TrimPrefix(str(raw), "billingAccounts/")
	switch {
	case acct == "":
		return "a billing account the plan does not name"
	case !slices.Contains(c.Billing, acct):
		return "billing account " + acct + ", which the review did not show"
	}
	return ""
}

// managedProviderProject says why a provider configuration may land its
// resources in a project that is not this run's, one refusal per
// configuration: one whose project is written out and is another, and one
// whose project the plan does not prove — read from anything, or not set at
// all. One whose project's form managedProviderSettings already refuses is
// left to that refusal.
//
// It is the second half of managedProject, and the reason that rule is not
// bypassed by moving a project one line up: a resource that leaves its project
// to its provider block is admitted there (terraform writes the attribute as
// unknown), so the block's project is the one that decides — and
// managedProviderSettings reads only the setting's form, never its value.
//
// A project is proven only when the block writes it out: the expression is a
// constant_value, with no reference, that is a string naming one of this
// run's projects. Nothing read from a variable is admitted, however plainly
// the plan shows the variable's value, because the plan cannot show that the
// block reads the variable bare: terraform writes an expression's references
// and nothing else, so replace(var.project, "1", "2"), "${var.project}-x",
// lower(var.project), tostring(var.project) and var.project == "a" ? "b" :
// "c" are each written exactly as var.project is, {"references":
// ["var.project"]} (terraform 1.16.4's own output for all six is
// testdata/managed_provider_wrapped_project_plan.json). Resolving the
// variable would read the function's argument as its result, so an earlier
// revision that followed var.project through module calls to its value
// admitted a block landing its resources in proj-2. A module call's argument
// is not read for the same reason: it feeds a provider block only through a
// reference, which is already refused. A refused block falls back to the
// typed confirmation, and the agent can always write the project out — a
// constant_value is the value terraform evaluated, templates over literals
// included.
//
// And a configuration with no project at all is refused, the one terraform
// configures by itself when the plan declares no provider block included: its
// resources land in whatever project the environment of whoever applies names
// (GOOGLE_PROJECT and its aliases, then gcloud's configuration), which the
// plan does not show and the review did not.
func (c ManagedCover) managedProviderProject(p *Plan) []string {
	cfg := p.Configuration
	if cfg == nil {
		return nil
	}
	var out []string
	for _, key := range slices.Sorted(maps.Keys(cfg.ProviderConfig)) {
		where := "provider_config." + key + " (lands resources in "
		raw, set := cfg.ProviderConfig[key].Expressions["project"]
		if !set {
			// Terraform's JSON gives a provider config of a module nested two
			// or more deep (module.m.module.n:google) no expressions at all, so
			// it lands here too: a known conservative refusal that fails closed
			// (the run asks for the typed confirmation), never a pass.
			out = append(out, "provider_config."+key+" (sets no project, so it lands resources in whatever project the environment of whoever applies names, which the plan does not show: it is not proven to be this run's — write the project out)")
			continue
		}
		if !managedSettingForm(raw) {
			continue
		}
		var e managedExpr
		_ = json.Unmarshal(raw, &e) // managedSettingForm decoded it
		var proj string
		switch {
		case len(e.References) > 0:
			out = append(out, where+"a project read from "+strings.Join(e.References, ", ")+", not written out: terraform writes a function or a template over a variable exactly as it writes the variable, so the plan does not prove the value and it is not proven to be this run's — write the project out)")
		case json.Unmarshal(e.ConstantValue, &proj) != nil || proj == "":
			out = append(out, where+"project "+string(e.ConstantValue)+", which is not a string the plan names, so it is not proven to be this run's)")
		case !slices.Contains(c.Projects, proj):
			out = append(out, where+"project "+proj+", which is not this run's)")
		}
	}
	return out
}

// managedBucketLock says why a bucket's retention policy is one a managed
// plan may not set: a locked one, or one whose lock the plan does not know.
// Locking a retention policy cannot be undone — the bucket cannot be deleted
// until its last object's retention has run out, which can be decades,
// whatever the owner's delete list says — and a locked bucket can place a
// lien on the project, which then cannot be deleted either. An unlocked
// policy can be changed or removed later, and is admitted.
func managedBucketLock(ch Change) string {
	if blockUnknown(ch.AfterUnknown, "retention_policy", "is_locked") {
		return "a bucket whose retention policy lock is not known at plan time: write is_locked = false, or leave it out"
	}
	for _, p := range objects(ch.After["retention_policy"]) {
		if l, set := p["is_locked"]; set && l != nil && l != false {
			return "a bucket whose retention policy is locked: a lock can never be undone, so the bucket cannot be deleted until its retention runs out and can hold a lien on the project — a managed plan locks nothing"
		}
	}
	return ""
}

// managedObjectLock says why an object's retention is one a managed plan may
// not set: mode Locked, read without regard to case so no spelling of it slips
// past, or a mode the plan does not know. A locked retention can be
// neither shortened nor removed, and the object, its bucket and the project
// with it cannot be deleted before it runs out. Unlocked retention is admitted.
func managedObjectLock(ch Change) string {
	if blockUnknown(ch.AfterUnknown, "retention", "mode") {
		return "an object whose retention mode is not known at plan time: write mode = \"Unlocked\", or leave retention out"
	}
	for _, r := range objects(ch.After["retention"]) {
		if strings.EqualFold(strings.TrimSpace(str(r["mode"])), "Locked") {
			return "an object whose retention mode is Locked: a locked retention can never be shortened or removed, so the object, its bucket and the project cannot be deleted before it runs out — a managed plan locks nothing"
		}
	}
	return ""
}

// blockUnknown: the plan knows the nested block, any element of it, or attr in
// any element only after apply. after_unknown writes a block as a list of
// objects (or true for the whole of it), and an element as true or an object.
func blockUnknown(au any, block, attr string) bool {
	m, _ := au.(map[string]any)
	v := m[block]
	if v == true {
		return true
	}
	l, _ := v.([]any)
	for _, e := range l {
		if e == true {
			return true
		}
		if em, ok := e.(map[string]any); ok && em[attr] == true {
			return true
		}
	}
	return false
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
	case !managedNameChars.MatchString(n):
		return fmt.Sprintf("%s %q carries a character outside [a-z0-9._-]: write the resource's own short ID, never a path or an import ID", attr, n)
	}
	return ""
}

// managedNameChars is every character a managed name may carry. The provider
// reads an IAM resource's parent as an import ID when it has the shape of one,
// so "fmgd-x/projects/<p>/secrets/<id>" starts with the prefix and names the
// secret its path names — Fugaro's own, the runs bucket, a job or a
// repository. A name of these characters alone is the resource's own ID: no
// '/', no ':', no capital (a bucket, an account, a job or a repository takes
// none, and a secret or a scheduler written in capitals asks for a
// confirmation rather than being read).
var managedNameChars = regexp.MustCompile(`^[a-z0-9._-]+$`)

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
		// The lists the member has to be on are the installation's own — the
		// launchers, operators and budget admins its configuration records
		// (internal/cli's managedCover, the only place a ManagedCover is built)
		// — and the refusal names them rather than a review screen, which a
		// managed apply has none of: the operator of `fugaro managed apply`
		// never saw one, and the sentence has to say which list to edit.
		return fmt.Sprintf("grants to %s, who is on none of the lists this installation's configuration records (launchers, operators, budget admins) and is not a service account of the managed project", m)
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
