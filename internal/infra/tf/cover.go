package tf

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// allowedTypes are the resource types our embedded modules declare
// (deploy/terraform/gcp). A plan is covered by init's one confirmation only if
// every resource in it is one of them: TestAllowedTypesCoverTheModules fails
// when a module adds a type that is not listed here, so adding one is a
// decision, not an accident.
var allowedTypes = map[string]bool{
	"google_apikeys_key":                             true,
	"google_artifact_registry_repository":            true,
	"google_artifact_registry_repository_iam_member": true,
	"google_billing_budget":                          true,
	"google_cloud_run_v2_job":                        true,
	"google_cloud_run_v2_job_iam_member":             true,
	"google_cloud_scheduler_job":                     true,
	// The Batch backend's own root (design compute-backend §8.1 task 6) is a
	// root of its own, applied by its own stage behind --provision-batch and
	// its own typed confirmation: these three types are the NAT, its router
	// and the deny-all ingress rule on the run-class accounts, and they are
	// listed here because every module the tree embeds has to be decided on.
	// The installation root does not include that module, so no plan this one
	// confirmation covers can carry them. What the compute stage does run
	// over its own plan is GrantsNotCovered: the grant rules below, the
	// launchers' Batch Job Editor condition among them.
	"google_compute_firewall":                 true,
	"google_compute_router":                   true,
	"google_compute_router_nat":               true,
	"google_firebase_database_instance":       true,
	"google_firebase_project":                 true,
	"google_logging_log_view":                 true,
	"google_logging_log_view_iam_member":      true,
	"google_logging_project_bucket_config":    true,
	"google_logging_project_exclusion":        true,
	"google_logging_project_sink":             true,
	"google_monitoring_alert_policy":          true,
	"google_monitoring_notification_channel":  true,
	"google_project_iam_custom_role":          true,
	"google_project_iam_member":               true,
	"google_project_service":                  true,
	"google_secret_manager_secret":            true,
	"google_secret_manager_secret_iam_member": true,
	"google_service_account":                  true,
	"google_service_account_iam_member":       true,
	"google_storage_bucket":                   true,
	"google_storage_bucket_iam_member":        true,
	"google_storage_bucket_object":            true,
}

// who says what kind of principal a module grants a role to: People are the
// members the review screen lists (launchers, operators, admins), Accounts the
// Fugaro service accounts of this run, and Deployer the deployer account of
// D75 alone — its own kind, so that its eight roles name it and nothing else,
// and no row meant for Fugaro's accounts accepts it.
type who uint8

const (
	toPeople who = 1 << iota
	toAccounts
	toDeployer
)

// customKey is how grantRules names a custom role of the installation (by its
// role ID, whichever of this run's projects it lives in).
const customKey = "custom:"

// grantRules says, per *_iam_member resource type, which roles the modules
// grant through it and to which kind of principal: the predefined roles by
// name, the installation's custom roles as "custom:<role ID>". A grant outside
// its type's row is not covered even when the role is granted elsewhere (a
// role that is harmless on a bucket may not be on the project), and a role
// meant for the job accounts is not covered for a person. A role the table
// does not name for the type is refused: the table is an allowlist.
// TestGrantRulesAreTheModules fails when a module's grants and this table
// differ.
var grantRules = map[string]map[string]who{
	"google_project_iam_member": {
		customKey + "fugaroLauncher":       toPeople,
		customKey + "fugaroBuildSubmitter": toPeople | toAccounts,
		customKey + "fugaroHistory":        toAccounts,
		customKey + "fugaroReaper":         toAccounts,
		customKey + "fugaroPreloadBuilder": toAccounts,
		// The deployer's eight roles, bound to the deployer account and to
		// nothing else (deployer.tf's eight bindings; deployerGrant below is
		// what checks their member, project and conditions, which grantRules
		// cannot see). A role of the deployer's that is missing from these
		// rows is refused outright rather than falling through, which is what
		// makes adding one a deliberate act: TestGrantRulesAreTheModules pins
		// these rows to the module's own grants, and
		// TestDeployerBindingsAreTheModules pins the eight bindings (one per
		// role, the seven conditioned ones under their own condition and the
		// unconditioned one under none) to the roles named here.
		customKey + DeployerRole:           toDeployer,
		customKey + DeployerStorageRole:    toDeployer,
		customKey + DeployerSecretRole:     toDeployer,
		customKey + DeployerAccountRole:    toDeployer,
		customKey + DeployerAPIKeyRole:     toDeployer,
		customKey + DeployerRepositoryRole: toDeployer,
		customKey + DeployerRunJobRole:     toDeployer,
		customKey + DeployerSchedulerRole:  toDeployer,
		// The whole-project inventory's read role (Task 4.1a, the owner's
		// ruling of 2026-10-17): the installation's own custom role, granted
		// to the operators who run a managed apply. The predefined
		// roles/cloudasset.viewer is deliberately absent — a plan granting it,
		// or any other Cloud Asset role, is refused, because viewer carries
		// cloudasset.assets.listResource and the read makes no ListAssets call.
		customKey + CloudAssetRole:                toPeople,
		"roles/aiplatform.user":                   toAccounts,
		"roles/batch.agentReporter":               toAccounts,
		"roles/batch.jobsEditor":                  toPeople, // only under the Fugaro job-name condition (jobsCondition); never an account
		"roles/datastore.user":                    toAccounts,
		"roles/datastore.viewer":                  toPeople,
		"roles/firebaseauth.admin":                toAccounts,
		"roles/firebasedatabase.admin":            toPeople | toAccounts,
		"roles/firebasedatabase.viewer":           toPeople,
		"roles/logging.logWriter":                 toAccounts,
		"roles/serviceusage.serviceUsageConsumer": toPeople | toAccounts,
	},
	"google_service_account_iam_member": {
		customKey + "fugaroTokenMinter": toPeople,
		"roles/iam.serviceAccountUser":  toPeople | toAccounts,
		// D75's one-time impersonation setup: the operators, and nobody else,
		// may assume the deployer account. On an account, for people — the
		// grant that makes the managed apply possible and the only grant of
		// this role anywhere (deployer.tf). Which account it is on is what
		// deployerGrant checks, since this table sees only the member.
		DeployerTokenCreatorRole: toPeople,
	},
	"google_storage_bucket_iam_member": {
		"roles/storage.objectAdmin":  toPeople,
		"roles/storage.objectUser":   toPeople | toAccounts, // people only under the launchers' condition (bucketCondition)
		"roles/storage.objectViewer": toPeople | toAccounts,
	},
	"google_artifact_registry_repository_iam_member": {
		customKey + "fugaroTagMover":    toAccounts,
		"roles/artifactregistry.reader": toPeople | toAccounts,
		"roles/artifactregistry.writer": toPeople | toAccounts,
	},
	"google_secret_manager_secret_iam_member": {
		"roles/secretmanager.secretAccessor":     toAccounts,
		"roles/secretmanager.secretVersionAdder": toPeople,
		"roles/secretmanager.viewer":             toPeople,
	},
	"google_cloud_run_v2_job_iam_member": {
		customKey + "fugaroJobRunner": toPeople | toAccounts,
		"roles/run.invoker":           toAccounts,
	},
	"google_logging_log_view_iam_member": {
		"roles/logging.viewAccessor": toPeople,
	},
}

// allowedRoles are the predefined roles any row of grantRules names.
var allowedRoles = func() map[string]bool {
	m := map[string]bool{}
	for _, row := range grantRules {
		for r := range row {
			if !strings.HasPrefix(r, customKey) {
				m[r] = true
			}
		}
	}
	return m
}()

// Cover says which plans init's one confirmation may cover.
type Cover struct {
	// Projects are the GCP projects of this run (the installation's first, then
	// a verified Firebase project): their own fugaro service accounts and
	// custom roles are Fugaro's.
	Projects []string
	// Listed are the IAM members the review screen listed (launchers,
	// operators, budget admins): the only people a plan may grant a role to.
	Listed []string
	// Registry is the Artifact Registry host of this run ("us-east5-docker.pkg.dev"),
	// Region the Cloud Run region, Buckets the run's own storage buckets (the
	// runs bucket) and Billing the billing account the review showed, if any.
	Registry, Region string
	Buckets          []string
	Billing          []string
	// Ghcr is the remote (pull-through) repository's ID in this run's
	// registry ("fugaro-ghcr") and Owner the GitHub owner a base pin through
	// it is trusted for: the owner half of the local config's recorded
	// image_source, which its caller reads from the config — never from the
	// reference being covered, which would make the rule vacuous.
	//
	// The remote repository proxies all of ghcr.io, so a reference under its
	// ID has to match the pin's closed shape rather than merely sit under
	// this run's registry (baseImageOK). Both are parameters, and an empty
	// Owner refuses every remote path, so a caller that forgets one cannot
	// reopen the hole.
	Ghcr, Owner string
	// Accounts are service accounts (IAM members) a plan may grant to as
	// Fugaro's own although their IDs have none of the shapes fugaroAccount
	// knows: the compute root's run accounts, which it reads from the project
	// by their Fugaro job display name, and the project's Cloud Build and
	// Cloud Scheduler service agents, which act as the preload account. The
	// installation's cover names none.
	Accounts []string
}

// CloudAssetRole is the installation's whole-project inventory read role
// (Task 4.1a of docs/plans/2026-10-16-managed-projects.md, the owner's ruling
// of 2026-10-17): a custom role of the installation holding exactly
// cloudasset.assets.searchAllResources, the one permission the whole-project
// read's SearchAllResources call needs. The predefined roles/cloudasset.viewer
// Task 4.1 first granted is deliberately not it — viewer also carries
// cloudasset.assets.listResource and more — so the identity that runs the
// inventory (the operator applying a managed plan, and through Task 1.5 the
// deployer it impersonates) gains the read and no write.
//
// The module declares it beside the launcher's and job runner's roles
// (installation/iam.tf) and grants it to the operators; RolePermissions pins
// its one permission, so a plan that creates or updates the role with any
// other is refused, and TestRolePinsAreTheModules fails when the module's list
// and this one differ.
const CloudAssetRole = "fugaroCloudAssetReader"

// FugaroRoles are the custom role IDs the modules create; FugaroAccounts the
// fixed service account IDs a toAccounts row accepts. Job and build accounts
// are fugaro-<readable>-<8 hex>. The deployer account (DeployerAccountID) is
// deliberately NOT among FugaroAccounts: it holds its roles and nothing else,
// so it is its own kind of principal (toDeployer).
var (
	FugaroRoles    = []string{"fugaroLauncher", "fugaroJobRunner", "fugaroBuildSubmitter", "fugaroTagMover", "fugaroTokenMinter", "fugaroHistory", "fugaroReaper", "fugaroPreloadBuilder", CloudAssetRole, DeployerRole, DeployerStorageRole, DeployerSecretRole, DeployerAccountRole, DeployerAPIKeyRole, DeployerRepositoryRole, DeployerRunJobRole, DeployerSchedulerRole}
	FugaroAccounts = []string{"fugaro-scheduler", "fugaro-history", "fugaro-token-signer", "fugaro-reaper", "fugaro-preload"}
)

// The deployer account of D75, and the condition that fences the half of it
// that may touch an existing resource (Task 1.5 of
// docs/plans/2026-10-16-managed-projects.md).
//
// A managed apply does not run as the operator: it assumes the deployer
// account the installation module creates, which holds eight custom roles,
// split so that a condition on one binding cannot be defeated by an
// unconditioned binding of the same role (D75). fugaroDeployer holds only
// create permissions, read permissions and serviceusage.services.enable, and
// is bound to the account with no condition; the seven conditioned roles hold
// every update, delete, run and pause permission, every *.setIamPolicy and
// iam.serviceAccounts.actAs, one role per service, and each is bound to the
// account exactly once under its own service's condition (deployerServices
// below) — so the authority that could reach an existing resource is authority
// the deployer holds only over names carrying ManagedPrefix. That prefix is what keeps the deployer out of the runs bucket
// (where the marker and the owner's delete list are), out of Fugaro's own
// accounts, jobs, secrets and repositories, and off the project's default
// compute account, which holds Editor by default.
//
// Nothing here holds project-level IAM authority (no resourcemanager.projects.*)
// and nothing here may edit an IAM role (no iam.roles.*): an account with
// either could grant itself object access to the runs bucket and forge the
// marker, which is the fact a managed project's confirmations are skipped on.
const (
	// DeployerAccountID is the deployer's fixed service account ID: one
	// account per installation, in the installation module (C8's answer,
	// 2026-10-16), created by the owner-run creating apply and by nothing
	// else.
	DeployerAccountID = "fugaro-deployer"
	// DeployerRole is the unconditioned role's ID: create permissions, reads
	// and serviceusage.services.enable.
	DeployerRole = "fugaroDeployer"
	// The seven conditioned roles' IDs, one per service, each holding that
	// service's slice of every update, delete, run and pause permission, every
	// *.setIamPolicy and actAs, and each bound to the deployer account exactly
	// once under its own condition (deployerServices below). Until 0.9.5 this
	// was ONE role, fugaroDeployerScoped, under one condition; that condition
	// could not be applied (17 logical operators where IAM allows 12, a
	// resource.name.contains() which is not a supported function, and
	// resource.name unsupported for five of the nine types it named), so the
	// fence was gated off and redesigned — docs/design/managed-deployer-fence.md.
	// The module creates them, and their bindings, only with
	// managed_deployer_scoped, which nothing sets. A plan without them has
	// nothing of them to cover; one with them is covered binding by binding.
	DeployerStorageRole    = "fugaroDeployerStorage"
	DeployerSecretRole     = "fugaroDeployerSecret"
	DeployerAccountRole    = "fugaroDeployerAccount"
	DeployerAPIKeyRole     = "fugaroDeployerApiKey"
	DeployerRepositoryRole = "fugaroDeployerRepository"
	DeployerRunJobRole     = "fugaroDeployerRunJob"
	DeployerSchedulerRole  = "fugaroDeployerSchedulerJob"
	// DeployerConditionTitle is the title of every one of those seven
	// conditions. It names the fence rather than the service, so the project's
	// policy listing shows one fence around the deployer; the role is what
	// says which service a binding fences. The modules never set a condition
	// description, and the plan review compares the title and the expression
	// byte for byte.
	DeployerConditionTitle = "fugaro-managed-prefix"
	// DeployerTokenCreatorRole is the one role that lets an operator assume
	// the account (D75's one-time setup, step 2 of Task 1.5). It is granted on
	// the deployer account and on no other: deployerGrant checks which account
	// a plan grants it on.
	DeployerTokenCreatorRole = "roles/iam.serviceAccountTokenCreator"
)

// DeployerClause is one clause of one service's condition: the resource type
// it admits, and the resource.name test it admits it under. The type term is
// not optional — a clause with no type term applies to every type the role
// reaches, so a resource whose name happens to carry the prefix could
// otherwise reach a permission meant for another service (a bucket named
// fmgd- is not a service account).
//
// Op is "startsWith" or "extract", the two forms the split fence uses:
//
//   - "startsWith" tests the front of the whole resource name, and Value is
//     that front, with ManagedPrefix spelled into it. Cloud Storage names a
//     bucket projects/_/buckets/<bucket> and an object
//     projects/_/buckets/<bucket>/objects/<object>, so
//     "projects/_/buckets/fmgd-" is a prefix of the name, and this is the one
//     form the tree has measured live (Check 32, RUN 2026-10-14). An IAM
//     service account's name is
//     projects/<project>/serviceAccounts/<account>@<project>
//     .iam.gserviceaccount.com, whose front is the project — a plan-time
//     literal (var.project) — so that service takes this form too.
//   - "extract" tests the resource's own LAST segment: Value is the name's
//     template, placeholders and all, and Starts is what the extracted segment
//     must begin with. It is what the remaining five services need, because
//     the segment before the resource's own name carries a value the module
//     does not have at plan time: Secret Manager names a secret with the
//     project NUMBER rather than the id, and Artifact Registry, Cloud Run and
//     Cloud Scheduler carry the location, which a managed plan chooses and
//     which is not even constant inside one installation (the installation's
//     own scheduler runs in another region from its jobs).
//
// Value is never ManagedPrefix alone: a value that admitted any name merely
// containing "fmgd-" would admit a fugaro-* resource in a container whose name
// contains the prefix.
//
// The extract form depends on Google behaviour this repository cannot check:
// that a name not matching the template makes the condition false rather than
// true (docs/design/managed-deployer-fence.md, C7). It is used only where
// startsWith cannot be written, and Check 68 measures the two directions that
// CAN be constructed — a prefixed name and a non-prefixed one, including a
// name that contains the prefix without starting with it. The template
// mismatch itself is out of reach: every conditioned type's real resource name
// matches its own template, so a mismatching name of the same type cannot be
// created, and C7 stays UNVERIFIED (the design's §8 point 2 is the owner's to
// answer before the five extract services are relied on).
type DeployerClause struct {
	Type, Op, Value, Starts string
}

// String is the clause as the condition writes it, and as the module writes it
// byte for byte (TestDeployerBindingsAreTheModules pins the two together).
func (c DeployerClause) String() string {
	typ := "resource.type == " + strconv.Quote(c.Type)
	if c.Op == "extract" {
		return "(" + typ + " && resource.name.extract(" + strconv.Quote(c.Value) + ").startsWith(" + strconv.Quote(c.Starts) + "))"
	}
	return "(" + typ + " && resource.name." + c.Op + "(" + strconv.Quote(c.Value) + "))"
}

// DeployerService is one service's slice of the deployer's scoped authority:
// the custom role that holds that service's permissions, and the clauses of
// the one condition its one binding carries.
//
// The split is what makes the fence expressible at all (F1 of
// docs/design/managed-deployer-fence.md): one condition covering all nine
// types needs nine clauses and 17 logical operators where IAM allows 12, a
// resource.name.contains() that is not a supported function, and resource.name
// forms five of those services do not have. One condition per service needs
// one or two clauses, three operators at most, and a name test written the way
// that service's own name is shaped. It also shrinks the blast radius of a
// wrong clause: a clause too wide for Cloud Scheduler cannot widen storage,
// because storage's permissions are in another role under another binding.
type DeployerService struct {
	// Role is the custom role's ID, "fugaroDeployer<Service>".
	Role string
	// Clauses are the condition's clauses, in the order the module writes them.
	Clauses []DeployerClause
}

// Expression is the service's condition expression: its clauses joined with
// " || ", the string the module's own local holds and a plan carries.
func (s DeployerService) Expression() string {
	parts := make([]string, 0, len(s.Clauses))
	for _, c := range s.Clauses {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, " || ")
}

// deployerServices is the scoped half of D75, one entry per service, and the
// source of every role, clause and expression the cover compares a plan
// against. The module writes the same strings in deployer.tf
// (local.deployer_condition_<service>) and TestDeployerBindingsAreTheModules
// pins the two together, byte for byte.
//
// What is NOT in any of them, and why: the runs bucket, the installation's
// state bucket and D77's managed state bucket (whose object access is a
// bucket-level binding of its own, Task 1.5 (3)), Fugaro's own accounts, jobs,
// scheduler jobs, secrets, repositories and API key, and the project's default
// compute account — none of their names carries the prefix, and that is the
// whole fence.
//
// Where GCP does not honour resource.name for one of these types, the
// permission that needs it is not moved into the unconditioned role: the task
// that would drop the matching type is Task 1.1's list, and the live check
// that measures each form is Check 68. Until it is run, the forms below are
// what the IAM Conditions documentation says these services name their
// resources.
//
// Every role here is bound exactly once, under this expression, and the 31
// permissions D75 gives the scoped half are partitioned across them with none
// added, dropped or moved into the unconditioned role
// (TestDeployerScopedRolesAreD75Partitioned).
func deployerServices(project string) []DeployerService {
	saPrefix := "projects/" + project + "/serviceAccounts/" + ManagedPrefix
	return []DeployerService{
		{Role: DeployerStorageRole, Clauses: []DeployerClause{
			{Type: "storage.googleapis.com/Bucket", Op: "startsWith", Value: "projects/_/buckets/" + ManagedPrefix},
			{Type: "storage.googleapis.com/Object", Op: "startsWith", Value: "projects/_/buckets/" + ManagedPrefix},
		}},
		// One clause, the container's own type: a secret VERSION's name does
		// not match the secret's template, so no clause admits one, and no
		// permission of this role acts on one — the old shape's
		// secretmanager.googleapis.com/SecretVersion clause was defensive, and
		// under extract it would be dead weight rather than a second fence.
		// secretmanager.versions.* is nowhere in the deployer, and the role pin
		// refuses it, so a plan that added one would find it unusable rather
		// than unfenced.
		{Role: DeployerSecretRole, Clauses: []DeployerClause{
			{Type: "secretmanager.googleapis.com/Secret", Op: "extract", Value: "projects/{project}/secrets/{secret}", Starts: ManagedPrefix},
		}},
		{Role: DeployerAccountRole, Clauses: []DeployerClause{
			{Type: "iam.googleapis.com/ServiceAccount", Op: "startsWith", Value: saPrefix},
		}},
		{Role: DeployerAPIKeyRole, Clauses: []DeployerClause{
			{Type: "apikeys.googleapis.com/Key", Op: "extract", Value: "projects/{project}/locations/{location}/keys/{key}", Starts: ManagedPrefix},
		}},
		{Role: DeployerRepositoryRole, Clauses: []DeployerClause{
			{Type: "artifactregistry.googleapis.com/Repository", Op: "extract", Value: "projects/{project}/locations/{location}/repositories/{repository}", Starts: ManagedPrefix},
		}},
		{Role: DeployerRunJobRole, Clauses: []DeployerClause{
			{Type: "run.googleapis.com/Job", Op: "extract", Value: "projects/{project}/locations/{location}/jobs/{job}", Starts: ManagedPrefix},
		}},
		{Role: DeployerSchedulerRole, Clauses: []DeployerClause{
			{Type: "cloudscheduler.googleapis.com/Job", Op: "extract", Value: "projects/{project}/locations/{location}/jobs/{job}", Starts: ManagedPrefix},
		}},
	}
}

// DeployerServiceRoles are the seven conditioned roles' IDs, in the order the
// module creates them. An account holding any of them holds it only where that
// service's condition admits the resource.
func DeployerServiceRoles() []string {
	roles := make([]string, 0, len(deployerServices("")))
	for _, s := range deployerServices("") {
		roles = append(roles, s.Role)
	}
	return roles
}

// deployerService is the service whose role is role, and whether there is one.
func deployerService(project, role string) (DeployerService, bool) {
	for _, s := range deployerServices(project) {
		if s.Role == role {
			return s, true
		}
	}
	return DeployerService{}, false
}

// DeployerConditionClauses is the clauses of the conditioned role `role`'s
// condition in project, in order, a copy so no caller can widen the fence. ok
// is false when role is not one of the deployer's seven.
func DeployerConditionClauses(project, role string) ([]DeployerClause, bool) {
	s, ok := deployerService(project, role)
	if !ok {
		return nil, false
	}
	return slices.Clone(s.Clauses), true
}

// DeployerConditionExpression is the condition expression of the conditioned
// role `role` in project — its clauses joined with " || " — and whether role
// is one of the deployer's seven. A binding of one of them whose expression is
// not exactly this is not covered (deployerGrant), because the plan review
// cannot tell a narrowed expression from a widened one.
func DeployerConditionExpression(project, role string) (string, bool) {
	s, ok := deployerService(project, role)
	if !ok {
		return "", false
	}
	return s.Expression(), true
}

// DeployerAccount is the email of the deployer account in gcpProject: the
// module's fixed account ID (DeployerAccountID), in the project the module is
// applied to. It is what deployerGrant compares a token-creator grant's
// service_account_id against, and what internal/managed assumes.
func DeployerAccount(gcpProject string) string {
	return DeployerAccountID + "@" + gcpProject + ".iam.gserviceaccount.com"
}

// deployerProject is the project whose deployer account this run's plans may
// name: the installation's (Projects[0]), the one project the installation
// module — and so deployer.tf — is applied to. "" when the cover has none.
func (c Cover) deployerProject() string {
	if len(c.Projects) == 0 || c.Projects[0] == "" {
		return ""
	}
	return c.Projects[0]
}

// isDeployerMember is whether m is exactly this run's deployer account as an
// IAM member: "serviceAccount:" + DeployerAccount(the installation's project).
func (c Cover) isDeployerMember(m string) bool {
	p := c.deployerProject()
	return p != "" && m == "serviceAccount:"+DeployerAccount(p)
}

// namesDeployerAccount is whether a grant's service_account_id is a deployer
// account at all — any project, any spelling the account's resource name has —
// so that a grant on it is checked even when it is not this run's.
func namesDeployerAccount(id string) bool {
	if m := saName.FindStringSubmatch(id); m != nil {
		return m[2] == DeployerAccountID
	}
	return strings.Contains(id, DeployerAccountID+"@")
}

var (
	saMember   = regexp.MustCompile(`^serviceAccount:([a-z][a-z0-9-]*)@([a-z0-9-]+)\.iam\.gserviceaccount\.com$`)
	jobAccount = regexp.MustCompile(`^fugaro-[a-z0-9-]+-[0-9a-f]{8}$`)
	repoID     = regexp.MustCompile(`^fugaro(-[a-z0-9-]+)?$`)
	// saName is a service account's own resource name, the form
	// google_service_account.<x>.name has and the form the modules write as a
	// literal when a grant has to name its account at plan time.
	saName = regexp.MustCompile(`^projects/([a-z0-9-]+)/serviceAccounts/([a-z][a-z0-9-]*)@([a-z0-9-]+)\.iam\.gserviceaccount\.com$`)
)

// RolePermissions pins the permissions of every custom role the modules
// create, by role ID: a plan creates or updates such a role only with exactly
// these. A role that gained a permission could otherwise make every grant of it
// (which the review screen shows as a plain "launcher") reach further.
// TestRolePinsAreTheModules fails when a module's list and this one differ.
var RolePermissions = map[string][]string{
	"fugaroLauncher": {"run.jobs.get", "run.jobs.list", "run.executions.get", "run.executions.list", "run.executions.cancel", "run.operations.get",
		"secretmanager.secrets.list", "secretmanager.versions.list"},
	"fugaroJobRunner":      {"run.jobs.run", "run.jobs.runWithOverrides"},
	"fugaroBuildSubmitter": {"cloudbuild.builds.create", "cloudbuild.builds.get"},
	"fugaroTagMover":       {"artifactregistry.tags.delete"},
	"fugaroTokenMinter":    {"iam.serviceAccounts.signJwt"},
	"fugaroHistory":        {"run.executions.list", "run.executions.get", "run.jobs.get", "run.jobs.list", "run.operations.get"},
	// The reaper's five, and no others (design compute-backend §3.7): it is
	// never roles/batch.jobsEditor and never roles/compute.instanceAdmin.v1,
	// both of which carry the create permissions a reaper must not have. It
	// named six until 0.9.5, the sixth being batch.jobs.cancel -- a permission
	// Google does not define, which is why the role could not be created and
	// --provision-batch was refused. The queue deadline deletes a job that
	// never left QUEUED instead (internal/backend/batch/reap.go).
	"fugaroReaper": {"batch.jobs.list", "batch.jobs.get", "batch.jobs.delete", "compute.instances.list", "compute.instances.delete"},
	// The preloaded boot image's build: boot one COS VM, read its serial
	// output, stop it, image its disk, delete it — and nothing else, setMetadata
	// (how SSH keys reach a VM) among the nothing else.
	"fugaroPreloadBuilder": {"compute.instances.create", "compute.instances.setMetadata", "compute.instances.setLabels",
		"compute.instances.setServiceAccount", "compute.instances.get", "compute.instances.list", "compute.instances.getSerialPortOutput",
		"compute.instances.stop", "compute.instances.delete", "compute.disks.create", "compute.subnetworks.use",
		"compute.images.useReadOnly", "compute.zoneOperations.get", "compute.disks.useReadOnly", "compute.images.create",
		"compute.images.setLabels", "compute.images.get", "compute.images.getFromFamily", "compute.images.list",
		"compute.images.delete", "compute.globalOperations.get"},
	// The whole-project inventory's read role (Task 4.1a, the owner's ruling
	// of 2026-10-17): the one permission SearchAllResources needs, and nothing
	// else — no write, and no cloudasset.assets.listResource, which belongs to
	// the wider predefined role this replaced.
	CloudAssetRole: {"cloudasset.assets.searchAllResources"},
	// The deployer's eight roles (D75, Task 1.5, redesigned for 0.9.5). They
	// are not installation roles: nothing a run does needs them, and their
	// bound is the split between them — creation, reads and
	// serviceusage.services.enable in fugaroDeployer, and every update, delete,
	// run, pause, *.setIamPolicy and actAs split one role per service across
	// the seven below, each bound exactly once and only under its own
	// DeployerConditionExpression (when the installation opts in with
	// managed_deployer_scoped; off by default in 0.8.6). The seven lists
	// partition D75's 31 scoped permissions and nothing else: see
	// TestDeployerScopedRolesAreD75Partitioned. Widening any list is a
	// decision: TestDeployerRolesHoldOnlyWhatD75Names states what each may not
	// hold.
	DeployerRole: {
		"apikeys.keys.create", "apikeys.keys.get", "apikeys.keys.list",
		"artifactregistry.repositories.create", "artifactregistry.repositories.get", "artifactregistry.repositories.getIamPolicy", "artifactregistry.repositories.list",
		"cloudscheduler.jobs.create", "cloudscheduler.jobs.get", "cloudscheduler.jobs.list",
		"iam.serviceAccounts.create", "iam.serviceAccounts.get", "iam.serviceAccounts.getIamPolicy", "iam.serviceAccounts.list",
		"run.jobs.create", "run.jobs.get", "run.jobs.getIamPolicy", "run.jobs.list",
		"secretmanager.secrets.create", "secretmanager.secrets.get", "secretmanager.secrets.getIamPolicy", "secretmanager.secrets.list",
		"serviceusage.services.enable", "serviceusage.services.get", "serviceusage.services.list",
		"storage.buckets.create",
	},
	// The nine storage permissions D75 names for an existing bucket and its
	// objects, and no other storage permission: storage.buckets.create stays
	// in the unconditioned role, where creating a bucket cannot touch one that
	// exists.
	DeployerStorageRole: {
		"storage.buckets.delete", "storage.buckets.get", "storage.buckets.setIamPolicy", "storage.buckets.update",
		"storage.objects.create", "storage.objects.delete", "storage.objects.get", "storage.objects.list", "storage.objects.update",
	},
	DeployerSecretRole: {
		"secretmanager.secrets.delete", "secretmanager.secrets.setIamPolicy", "secretmanager.secrets.update",
	},
	DeployerAccountRole: {
		"iam.serviceAccounts.actAs", "iam.serviceAccounts.delete", "iam.serviceAccounts.setIamPolicy", "iam.serviceAccounts.update",
	},
	DeployerAPIKeyRole: {
		"apikeys.keys.delete", "apikeys.keys.update",
	},
	DeployerRepositoryRole: {
		"artifactregistry.repositories.delete", "artifactregistry.repositories.setIamPolicy", "artifactregistry.repositories.update",
	},
	DeployerRunJobRole: {
		"run.jobs.delete", "run.jobs.run", "run.jobs.runWithOverrides", "run.jobs.setIamPolicy", "run.jobs.update",
	},
	DeployerSchedulerRole: {
		"cloudscheduler.jobs.delete", "cloudscheduler.jobs.enable", "cloudscheduler.jobs.pause", "cloudscheduler.jobs.run", "cloudscheduler.jobs.update",
	},
}

// knownCommands are the container command and args the modules set: none for
// a workflow's job, the history sweep and the image check.
var knownCommands = [][2][]string{
	{nil, nil},
	{{"/usr/local/bin/fugaro"}, {"budget", "history", "--sweep"}},
	{{"fugaro"}, {"image", "check", "--job"}},
}

// APITargets are the two services the Firebase web key may call, sorted;
// a fresh slice each call, so no caller can change the rule.
func APITargets() []string {
	return []string{"identitytoolkit.googleapis.com", "securetoken.googleapis.com"}
}

// NotCovered lists why the plan is not covered, empty when it is. A plan that
// is nil, or that holds anything but creates and in-place updates of the
// module resources, whose attributes are not the modules', that grants a role
// outside the modules' roles, grants to someone who is not on the review screen
// or a Fugaro service account of this run, or widens a custom role, is not
// covered: its stage asks its own typed confirmation. It never allows: iam
// bindings, iam policies, audit configs, service account keys, an unknown
// resource type, allUsers and domain members.
//
// imports are the import blocks the root's discovery wrote: an import is
// covered only when its address and ID are exactly one of them (and its ID is
// in one of the run's projects), so a stale imports.tf.json or a hand edit in
// the workdir never adopts something discovery did not check. An import is
// otherwise checked as any other change (an import and update included).
func (c Cover) NotCovered(p *Plan, imports []ImportKey) []string {
	if p == nil {
		return []string{"no plan"}
	}
	var out []string
	add := func(rc ResourceChange, why string) { out = append(out, safetext.Field(rc.Address)+" ("+why+")") }
	creates := creates(p)
	createdSAIDs, heldSAIDs := planAccounts(p)
	dep := planDeployer(p)
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
		case !allowedTypes[t]:
			add(rc, "a resource type outside the installation's modules")
		case t == "google_project_iam_custom_role" && c.customRole(rc) != "":
			add(rc, c.customRole(rc))
		case rc.Change.Importing != nil && !c.importOK(rc.Change.Importing.ID):
			add(rc, "imports "+rc.Change.Importing.ID+", which is not in this run's projects")
		case rc.Change.Importing != nil && !slices.Contains(imports, ImportKey{rc.Address, rc.Change.Importing.ID}):
			add(rc, "imports "+rc.Change.Importing.ID+", which this run's discovery did not find")
		case c.attributes(rc, creates) != "":
			add(rc, c.attributes(rc, creates))
		case strings.HasSuffix(t, "_iam_member"):
			if why := c.grant(rc, creates, createdSAIDs, heldSAIDs, dep); why != "" {
				add(rc, why)
			}
		}
	}
	return out
}

// creates is what a plan creates, by module and type: a computed member or
// role is trusted only when the resource it comes from is created in the same
// module.
func creates(p *Plan) map[string]bool {
	out := map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if slices.Contains(rc.Change.Actions, "create") {
			out[modulePrefix(rc)+"|"+rc.Type] = true
		}
	}
	return out
}

// planAccounts are the job accounts of a plan, by module and account ID:
// created, the accounts this plan creates (a bucket grant whose member is
// computed belongs to one of them, and its condition is accepted only against
// the account the title names, bucketCondition), and held, the accounts each
// module holds after this plan — created, updated or unchanged, never deleted
// (a bucket grant that names its member is a project job's widened one only
// for the account its own module holds, projectJobCondition).
func planAccounts(p *Plan) (created, held map[string]bool) {
	created, held = map[string]bool{}, map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if rc.Type != "google_service_account" {
			continue
		}
		id := str(rc.Change.After["account_id"])
		if id == "" {
			continue
		}
		if len(rc.Change.Actions) > 0 && !slices.Contains(rc.Change.Actions, "delete") {
			held[modulePrefix(rc)+"|"+id] = true
		}
		if slices.Contains(rc.Change.Actions, "create") {
			created[modulePrefix(rc)+"|"+id] = true
		}
	}
	return created, held
}

// deployerPlan is what a plan does about the deployer (D75, Task 1.5).
type deployerPlan struct {
	// createsRole is whether this plan creates or replaces one of the
	// deployer's eight custom roles. While it does, a project grant whose role
	// is computed is refused: the computed role could be one of the seven
	// conditioned ones, whose IAM condition is the whole fence around the
	// account, and a plan cannot be read as if it were not (Task 1.5 step 5).
	// This is why the module writes its project grants' roles as literals.
	createsRole bool
	// accounts are the modules in which this plan creates (or holds) the
	// deployer account: a service account grant there whose account is
	// computed could be on the deployer, and only the operators' token
	// creation may be (deployerGrant).
	accounts map[string]bool
}

// planDeployer reads a plan's deployerPlan.
func planDeployer(p *Plan) deployerPlan {
	d := deployerPlan{accounts: map[string]bool{}}
	for _, rc := range p.ResourceChanges {
		if rc.Type == "google_service_account" && !slices.Contains(rc.Change.Actions, "delete") && str(rc.Change.After["account_id"]) == DeployerAccountID {
			d.accounts[modulePrefix(rc)] = true
		}
		if rc.Type == "google_project_iam_custom_role" && !allOf(rc.Change.Actions, "no-op", "read") {
			if id := str(rc.Change.After["role_id"]); id == DeployerRole || slices.Contains(DeployerServiceRoles(), id) {
				d.createsRole = true
			}
		}
	}
	return d
}

// grant says why one *_iam_member change is not the modules', "" when it is:
// the type's grant rules, then a bucket grant's or a Batch Job Editor
// grant's condition, then the deployer's own grants (deployerGrant).
//
// createdSAIDs and heldSAIDs are planAccounts': a project job's widened bucket
// condition is accepted only for the project module's own account
// (projectJobCondition). dep is planDeployer's.
func (c Cover) grant(rc ResourceChange, creates, createdSAIDs, heldSAIDs map[string]bool, dep deployerPlan) string {
	t := rc.Type
	why := c.checkGrant(t, rc.Change, creates[modulePrefix(rc)+"|google_service_account"], creates[modulePrefix(rc)+"|google_project_iam_custom_role"], dep.createsRole)
	if why == "" && t == "google_storage_bucket_iam_member" {
		why = c.bucketCondition(rc, createdSAIDs, heldSAIDs)
	}
	if why == "" && t == "google_project_iam_member" && !unknown(rc.Change, "role") && str(rc.Change.After["role"]) == "roles/batch.jobsEditor" {
		why = c.jobsCondition(rc.Change)
	}
	if why == "" {
		why = c.deployerGrant(t, rc.Change, dep.accounts[modulePrefix(rc)])
	}
	return why
}

// GrantsNotCovered lists why the plan's grants are not the modules', empty
// when they are: NotCovered's IAM half alone, for the compute root, whose plan
// the run's one confirmation never covers (its stage always asks its own typed
// confirmation) but whose grants must still be the module's. Every
// *_iam_member a plan creates or updates is checked exactly as NotCovered
// checks it — the type's grant rules, its project and target, and the
// launchers' Batch Job Editor condition (jobsCondition) for the cover's own
// project and region — and an authoritative IAM resource or a service account
// key is refused. A resource that is not a grant is not this function's to
// judge, and neither is a delete (tf.Guard refuses those). A nil plan is not
// covered.
func (c Cover) GrantsNotCovered(p *Plan) []string {
	if p == nil {
		return []string{"no plan"}
	}
	var out []string
	add := func(rc ResourceChange, why string) { out = append(out, safetext.Field(rc.Address)+" ("+why+")") }
	made := creates(p)
	createdSAIDs, heldSAIDs := planAccounts(p)
	dep := planDeployer(p)
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if allOf(acts, "no-op", "read", "delete") && rc.Change.Importing == nil {
			continue
		}
		t := rc.Type
		switch {
		case strings.HasSuffix(t, "_iam_binding") || strings.HasSuffix(t, "_iam_policy") || strings.HasSuffix(t, "_iam_audit_config"):
			add(rc, "an authoritative IAM resource")
		case t == "google_service_account_key":
			add(rc, "a service account key")
		case strings.HasSuffix(t, "_iam_member"):
			if why := c.attributes(rc, made); why != "" {
				add(rc, why)
			} else if why := c.grant(rc, made, createdSAIDs, heldSAIDs, dep); why != "" {
				add(rc, why)
			}
		}
	}
	return out
}

func allOf(acts []string, ok ...string) bool {
	for _, a := range acts {
		if !slices.Contains(ok, a) {
			return false
		}
	}
	return true
}

func samePermissions(c Change) bool {
	b, ok1 := c.Before["permissions"].([]any)
	a, ok2 := c.After["permissions"].([]any)
	if !ok1 || !ok2 {
		return false
	}
	if unknown(c, "permissions") {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// customRole says why a custom role's create or update is not the modules':
// its ID is not one of theirs, it is not in one of this run's projects, its
// permissions are not exactly the pinned ones (known only after apply counts
// as not), or an update changes them. "" when it is.
func (c Cover) customRole(rc ResourceChange) string {
	ch := rc.Change
	if unknown(ch, "role_id") || unknown(ch, "project") || unknown(ch, "permissions") {
		return "a custom role whose ID, project or permissions are not known"
	}
	id, _ := ch.After["role_id"].(string)
	pinned, ok := RolePermissions[id]
	if !ok {
		return fmt.Sprintf("a custom role %q, which the installation's modules do not create", id)
	}
	if p := str(ch.After["project"]); !slices.Contains(c.Projects, p) {
		return fmt.Sprintf("a custom role in project %q, which is not this run's", p)
	}
	got, isList := ch.After["permissions"].([]any)
	if !isList {
		return "a custom role with no known permissions"
	}
	have := map[string]bool{}
	for _, g := range got {
		s, isStr := g.(string)
		if !isStr {
			return "a custom role with a permission that is not a string"
		}
		have[s] = true
	}
	if len(have) != len(pinned) || len(got) != len(pinned) {
		return "changes a custom role's permissions from the modules' (" + id + ")"
	}
	for _, p := range pinned {
		if !have[p] {
			return "changes a custom role's permissions from the modules' (" + id + ")"
		}
	}
	if slices.Contains(ch.Actions, "update") && !samePermissions(ch) {
		return "changes a custom role's permissions"
	}
	return ""
}

// unknown reports whether the plan knows the attribute only after apply.
func unknown(c Change, attr string) bool {
	m, ok := c.AfterUnknown.(map[string]any)
	return ok && m[attr] == true
}

// unknownAt reports whether the plan knows the value at path (map keys and
// list indexes, in the shape of after_unknown) only after apply: true if the
// value or any block above it is unknown. A path the plan does not mention is
// known.
func unknownAt(v any, path ...any) bool {
	for _, k := range path {
		if v == true {
			return true
		}
		switch k := k.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return false
			}
			v = m[k]
		case int:
			l, ok := v.([]any)
			if !ok || k >= len(l) {
				return false
			}
			v = l[k]
		}
	}
	return v == true
}

func (c Cover) checkGrant(typ string, ch Change, createsSA, createsRole, createsDeployerRole bool) string {
	rules := grantRules[typ]
	if rules == nil {
		return "a grant of a type the modules do not use"
	}
	// The principal. A member computed from a service account this module
	// creates is an account.
	kind := toAccounts
	switch {
	case unknown(ch, "member"):
		if !createsSA { // a member computed from a service account this module creates
			return "a member that is not known and no service account is created with it"
		}
	default:
		m, _ := ch.After["member"].(string)
		switch {
		case m == "":
			return "no member"
		case strings.HasPrefix(m, "domain:"):
			return fmt.Sprintf("grants to %s, a whole domain", m)
		case c.isDeployerMember(m):
			// Before the listed people: the deployer is never a person, even
			// if the review screen listed its address.
			kind = toDeployer
		case slices.Contains(c.Listed, m):
			if !strings.HasPrefix(m, "user:") && !strings.HasPrefix(m, "group:") && !strings.HasPrefix(m, "serviceAccount:") {
				return fmt.Sprintf("grants to %s, which is not a user, group or service account", m)
			}
			kind = toPeople
		case c.fugaroAccount(m):
		default:
			return fmt.Sprintf("grants to %s, who is not on the review screen", m)
		}
	}
	// The role.
	if unknown(ch, "role") {
		if kind == toDeployer {
			return "a computed role to the deployer account: it holds its eight roles, named, and nothing else"
		}
		if createsDeployerRole && typ == "google_project_iam_member" {
			// The plan is creating one of the deployer's roles, so a computed
			// PROJECT role here could be one of the seven conditioned ones —
			// whose single binding carries the IAM condition that is D75's
			// whole
			// fence. A condition cannot be checked against a role nobody can
			// name.
			//
			// The type matters, and this is why: only a project binding of one
			// of the deployer's conditioned roles carries the fence. A grant of
			// the same role at a narrower scope (a Cloud Run job, say) grants
			// its permissions on that one resource, and refusing every computed
			// role on every *_iam_member type would refuse the job runner's own
			// grant (history.tf's google_cloud_run_v2_job_iam_member, whose
			// role is the custom role being created beside it) on every
			// installation plan that deploys the history job.
			return "a computed project role in a plan that creates one of the deployer's conditioned roles (" + strings.Join(DeployerServiceRoles(), ", ") + "), which could be it: those roles are bound only under their managed-prefix conditions, and a plan must say which role this is"
		}
		if !createsRole {
			return "a role that is not known and no custom role is created with it"
		}
		// Which custom role is not known, but the table still bounds it: the
		// type's row must name some custom role for this kind of principal.
		for key, w := range rules {
			if strings.HasPrefix(key, customKey) && w&kind != 0 {
				return ""
			}
		}
		return fmt.Sprintf("a computed role on a %s for a kind of principal to which the installation's modules grant no custom role there", typ)
	}
	role, _ := ch.After["role"].(string)
	key, ok := c.roleKey(role)
	if !ok {
		return fmt.Sprintf("grants the role %q, which the installation's modules do not use", role)
	}
	w, ok := rules[key]
	if !ok {
		return fmt.Sprintf("grants the role %q on a %s, which the installation's modules do not do", role, typ)
	}
	if w&kind == 0 {
		return fmt.Sprintf("grants the role %q to a kind of principal the installation's modules do not grant it to", role)
	}
	return ""
}

// grantConditions reads a grant's condition blocks, or says why they cannot
// be trusted (what names the grant in that reason): a condition known only
// after apply, a condition that is not a block or a list of them, a list
// holding anything else, or more than one block.
func grantConditions(ch Change, what string) ([]map[string]any, string) {
	if unknown(ch, "condition") || unknownAt(ch.AfterUnknown, "condition", 0, "title") || unknownAt(ch.AfterUnknown, "condition", 0, "expression") || unknownAt(ch.AfterUnknown, "condition", 0, "description") {
		return nil, what + " whose condition is not known"
	}
	condRaw := ch.After["condition"]
	switch v := condRaw.(type) {
	case nil, map[string]any:
	case []any:
		// objects() silently drops a list entry that is not itself a condition
		// block: without this check, a condition such as ["true"] would read as
		// zero conditions (objects() returns none of it), covering a grant that
		// is not in fact unconditioned.
		if len(objects(v)) != len(v) {
			return nil, what + " whose condition list holds something that is not a condition block"
		}
	default:
		return nil, what + " whose condition is not a condition block or a list of them"
	}
	conds := objects(condRaw)
	if len(conds) > 1 {
		return nil, what + " with more than one condition"
	}
	return conds, ""
}

// jobsCondition says why a roles/batch.jobsEditor grant's condition is not
// the compute module's, "" when it is. The role lets its holder create a
// Batch job with any image, command and secrets, as any account it has
// actAs on, so it is covered only under exactly gcp.LauncherJobsCondition for
// the grant's own project and this run's region, with the module's title and
// description: no condition, a different one, and one that merely contains
// or prefixes it are all refused, as is a cover with no region to check it
// against.
func (c Cover) jobsCondition(ch Change) string {
	conds, why := grantConditions(ch, "a Batch Job Editor grant")
	if why != "" {
		return why
	}
	if len(conds) == 0 {
		return "grants roles/batch.jobsEditor with no condition, which is every Batch job in the project"
	}
	project := str(ch.After["project"])
	if !slices.Contains(c.Projects, project) || !gcp.ValidJobsConditionScope(project, c.Region) {
		return "grants roles/batch.jobsEditor where the Fugaro job-name condition cannot be checked (project or region)"
	}
	cd := conds[0]
	if str(cd["title"]) != gcp.LauncherJobsConditionTitle || str(cd["description"]) != gcp.LauncherJobsConditionDescription ||
		str(cd["expression"]) != gcp.LauncherJobsCondition(project, c.Region) {
		return "grants roles/batch.jobsEditor under a condition that is not the compute module's Fugaro job-name condition"
	}
	return ""
}

// deployerGrant says why a grant that is about the deployer account is not the
// one D75 describes, "" when it is, and "" for every other grant in the plan.
// Two of the module's grants are its business, and each is the whole of a
// property the account's safety rests on:
//
//   - a project binding of one of the deployer's eight roles (deployer.tf's
//     bindings (1) and (2)-(8)), whose MEMBER, PROJECT and CONDITION this
//     checks:
//     the member is exactly serviceAccount:fugaro-deployer@<installation
//     project> (a job account, the scheduler, the history account or a
//     computed member holding either role is refused even under the exact
//     condition), and the role and binding are in that same project. It is the only check
//     in this file that looks at a condition on anything but a bucket grant,
//     and it is here because a project binding of a conditioned role without
//     its condition — or with a wider one, or with another service's — hands
//     the deployer every update, delete, setIamPolicy and actAs permission of
//     that role project-wide,
//     which is the account that could then reach the runs bucket and forge the
//     marker.
//   - the operators' impersonation grant, DeployerTokenCreatorRole on
//     google_service_account_iam_member, whose ACCOUNT this checks: grantRules
//     sees a member and a role and not the account the role is on, so without
//     this a plan granting a listed person token creation on any account —
//     the scheduler's, or a stranger's — would ride the same confirmation as
//     the one grant that makes a managed apply possible. Every OTHER grant on
//     the deployer account (actAs above all) is refused (onDeployer), and the
//     deployer itself is a principal of its own kind (toDeployer) that no
//     other grantRules row accepts.
//
// The condition rule is the module's own shape, keyed per role: fugaroDeployer
// is bound with NO condition (binding (1)); each of the seven conditioned roles
// only under DeployerConditionTitle and exactly that role's own
// DeployerConditionExpression — its own, not another service's, because a
// storage binding carrying the Cloud Scheduler condition would fence objects by
// a template no bucket name matches, which fails closed here and is a mistake
// worth refusing rather than debugging later. Anything else is refused — a
// second binding of one of the seven under another condition would widen the
// account's authority just as much as an unconditioned one, and a narrowed
// expression is not something this review could tell from a widened one. A
// condition known only after apply, a description the modules never set, and
// more than one condition block are refused the way bucketCondition refuses
// them.
func (c Cover) deployerGrant(typ string, ch Change, createsDeployer bool) string {
	if typ == "google_service_account_iam_member" {
		if why := c.onDeployer(ch, createsDeployer); why != "" {
			return why
		}
		return c.deployerTokenCreator(ch)
	}
	if typ != "google_project_iam_member" {
		return ""
	}
	role := str(ch.After["role"])
	key, ok := c.roleKey(role)
	if !ok {
		return ""
	}
	dp := c.deployerProject()
	plain := key == customKey+DeployerRole
	svc, scoped := deployerService(dp, strings.TrimPrefix(key, customKey))
	if !plain && !scoped {
		return ""
	}
	// The member: this run's deployer account, named, and nothing else. The
	// table already holds every deployer role to toDeployer; this says so
	// again where the condition is checked, so the one place that reads a
	// deployer binding reads all of it.
	member := str(ch.After["member"])
	if unknown(ch, "member") || !c.isDeployerMember(member) {
		want := "this run's deployer account"
		if p := c.deployerProject(); p != "" {
			want = "serviceAccount:" + DeployerAccount(p)
		}
		return "grants " + role + " to " + member + ", which is not " + want + ": the deployer's roles are held by the deployer account and by nothing else (D75)"
	}
	// The project: the deployer's own, the role's and the binding's alike.
	if !strings.HasPrefix(role, "projects/"+dp+"/roles/") || unknown(ch, "project") {
		return "grants " + role + " outside the deployer's own project " + dp
	}
	if p := str(ch.After["project"]); p != "" && p != dp {
		return "grants " + role + " in project " + p + ", which is not the deployer's own project " + dp
	}
	if unknown(ch, "condition") || unknownAt(ch.AfterUnknown, "condition", 0, "title") || unknownAt(ch.AfterUnknown, "condition", 0, "expression") || unknownAt(ch.AfterUnknown, "condition", 0, "description") {
		return "a " + role + " grant whose condition is not known: " + DeployerConditionTitle + " is what fences the deployer, and a condition decided during apply is not one"
	}
	condRaw := ch.After["condition"]
	switch v := condRaw.(type) {
	case nil, map[string]any:
	case []any:
		if len(objects(v)) != len(v) {
			return "a " + role + " grant whose condition list holds something that is not a condition block"
		}
	default:
		return "a " + role + " grant whose condition is not a condition block or a list of them"
	}
	conds := objects(condRaw)
	if len(conds) > 1 {
		return "a " + role + " grant with more than one condition"
	}
	var title, expr string
	if len(conds) == 1 {
		if str(conds[0]["description"]) != "" {
			return "a " + role + " grant whose condition has a description, which the modules never set"
		}
		title, expr = str(conds[0]["title"]), str(conds[0]["expression"])
	}
	if plain {
		if len(conds) != 0 {
			return "grants " + DeployerRole + " under a condition, which the modules never do: that role is bound with none (D75 (1)), and a condition here would read as narrower than the binding beside it"
		}
		return ""
	}
	if title != DeployerConditionTitle || expr != svc.Expression() {
		return "grants " + svc.Role + " under a condition that is not " + DeployerConditionTitle + " on the managed prefix: " + svc.Expression() + " is the whole fence around that role, byte for byte (D75 (2), one binding per service since 0.9.5)"
	}
	return ""
}

// onDeployer says why a grant on a service account is a grant ON the deployer
// account that D75 does not make, "" otherwise. The operators' token creation
// is the one grant on it (deployerTokenCreator checks that one); any other role
// on it — actAs for a job account above all, which is a job that runs as the
// deployer — is refused, on any project's deployer. A grant whose account is
// computed, in a module that creates the deployer account, could be on it, and
// is refused unless it is the token creation (which deployerTokenCreator then
// refuses for being computed).
func (c Cover) onDeployer(ch Change, createsDeployer bool) string {
	role := str(ch.After["role"])
	if unknown(ch, "service_account_id") {
		if createsDeployer && (unknown(ch, "role") || role != DeployerTokenCreatorRole) {
			return "a service account grant whose account is not known, in a module that creates the deployer account: it could be on the deployer, which only the operators' token creation is"
		}
		return ""
	}
	id := str(ch.After["service_account_id"])
	if namesDeployerAccount(id) && (unknown(ch, "role") || role != DeployerTokenCreatorRole) {
		return "grants " + role + " on " + id + ": the only grant on the deployer account is the operators' " + DeployerTokenCreatorRole + " (D75)"
	}
	return ""
}

// deployerTokenCreator says why a token-creator grant is not the operators'
// one-time impersonation setup (Task 1.5 step 2), "" when it is and "" for
// every other grant on that type. The role is granted on the deployer account
// and on nothing else; grantRules has already checked that the member is a
// person on the review screen, so what is left is which account.
//
// The account has to be named, and named as this run's deployer account — the
// one in the installation's project (Projects[0]), never in a verified
// Firebase project the installation module is not applied to: a
// plan that says "an account, decided during apply" is a plan whose account
// nobody can check, and the account this grant is on is the difference between
// an operator who can assume the deployer and one who can mint a token for the
// scheduler (which can reach the budget backend) or for any other identity.
func (c Cover) deployerTokenCreator(ch Change) string {
	if str(ch.After["role"]) != DeployerTokenCreatorRole {
		return ""
	}
	if unknown(ch, "service_account_id") {
		return "a " + DeployerTokenCreatorRole + " grant whose account is not known: the module grants this role on the deployer account of this run and on nothing else, and a plan must say which account this is on"
	}
	id, _ := ch.After["service_account_id"].(string)
	if id == "" {
		return "a " + DeployerTokenCreatorRole + " grant on no account"
	}
	m := saName.FindStringSubmatch(id)
	dp := c.deployerProject()
	if m != nil && dp != "" && m[1] == dp && m[3] == dp && m[2] == DeployerAccountID {
		return ""
	}
	want := "the deployer account of this run"
	if dp != "" {
		want = DeployerAccount(dp)
	}
	return fmt.Sprintf("grants %s on %s, which is not %s: D75's impersonation grant is on that account alone", DeployerTokenCreatorRole, id, want)
}

// accountClause is one clause of a job or build account's bucket condition,
// as gcp.BucketCondition writes it.
var accountClause = regexp.MustCompile(`^resource\.name\.startsWith\("projects/_/buckets/([a-z0-9][a-z0-9._-]*)/objects/(runs|cache|locks|builds)/([a-z0-9][a-z0-9_-]*)/"\)$`)

// tokenClause is the one clause of a job account's read-only grant on its
// repository's budget token objects, as gcp.TokenBucketCondition writes it:
// tokens/<slug>/ of a bucket, nothing before it and nothing after it.
var tokenClause = regexp.MustCompile(`^resource\.name\.startsWith\("projects/_/buckets/([a-z0-9][a-z0-9._-]*)/objects/` + gcp.TokenBucketPrefix + `/([a-z0-9][a-z0-9_-]*)/"\)$`)

// bucketCondition says why a bucket grant's condition is not the modules'
// (docs/design/bucket-iam.md H6), "" when it is: objectAdmin carries none; an
// objectViewer carries none, except an account's, which may carry exactly
// one tokens/<slug>/ clause on one of this run's buckets (the job's read of
// its own budget token objects, budget-token-channel.md §7.2) — and then only
// for a Fugaro job account of this run that the grant's own module holds or
// creates, under the condition title built from that account
// (jobAccountGrant) — or, in the
// project module and for an account that module creates, exactly one
// whole-tokens/ clause (gcp.ProjectTokenBucketCondition, the shared job's
// read of every repository's sealed token, plan task 3.2 step 2); a person's
// objectUser carries exactly the launchers' condition on one of this run's
// buckets; an account's objectUser carries either a project job's widened
// condition (gcp.ProjectBucketCondition, byte for byte, in the project
// module, with the title of an account that module creates) or clauses of
// gcp.BucketCondition's shape on runs/, cache/, locks/ or builds/ — never
// tokens/ — each on one of this run's buckets. The modules never set a
// description, and a condition known only after apply is not covered.
//
// What a tokens/<slug>/ clause is *not* checked against is the grant's own
// repository. A job account's ID is derived and does not read back: a project
// job's is the profile/provider/account triple (gcp.ProjectServiceAccountID),
// a per-repository one the slug and workflow sanitised and clipped
// (gcp.ServiceAccountID). So what this can hold is the clause's shape —
// exactly one clause, tokens/ with one slug segment, no trailing text, on one
// of this run's buckets — and not the pairing: a grant to one repository's job
// account whose clause names a *different* repository's tokens/<slug>/ is
// accepted here, because nothing in the plan says which repository the
// account serves. That pairing is a fact about internal/infra's own writers
// (projectjobs.go, build.go), which is where it is checked; nothing a plan
// alone can read makes it checkable here.
//
// createdSAIDs are the (module, account ID) pairs this plan creates and
// heldSAIDs the pairs each module holds after it: the widened condition is
// accepted only for the project module's own account, so a plan that widened
// a per-repository job's grant — or a stranger's — is still stopped.
func (c Cover) bucketCondition(rc ResourceChange, createdSAIDs, heldSAIDs map[string]bool) string {
	ch := rc.Change
	conds, why := grantConditions(ch, "a bucket grant")
	if why != "" {
		return why
	}
	var title, expr string
	if len(conds) == 1 {
		if str(conds[0]["description"]) != "" {
			return "a bucket grant whose condition has a description, which the modules never set"
		}
		title, expr = str(conds[0]["title"]), str(conds[0]["expression"])
	}
	role := str(ch.After["role"])
	person := !unknown(ch, "member") && slices.Contains(c.Listed, str(ch.After["member"]))
	bucket := str(ch.After["bucket"])
	switch {
	case role == "roles/storage.objectViewer" && len(conds) != 0 && !person:
		m := tokenClause.FindStringSubmatch(expr)
		if m == nil || !slices.Contains(c.Buckets, m[1]) || (bucket != "" && bucket != m[1]) {
			// Not a per-repository job's tokens/<slug>/: it may still be a
			// project job's, which is the project module's own widening and
			// is accepted only for an account that module creates.
			if c.projectJobTokenCondition(rc, createdSAIDs, heldSAIDs, title, expr) {
				return ""
			}
			return "grants objectViewer on the bucket to an account under a condition that is not one repository's tokens/ condition of this run's bucket, nor a project job's whole-tokens/ one"
		}
		// A per-repository job's read of its own repository's tokens/
		// (gcp.TokenBucketCondition): the expression alone is not that grant.
		// The title must be the one built from the account
		// (gcp.TokenBucketConditionTitle) and the member a Fugaro job account
		// of this run that the grant's own module holds or creates — the same
		// account rule a project job's tokens/ grant passes
		// (projectJobTokenCondition), without the project module. Without it,
		// any member holding this expression would be covered as the module's
		// grant, and a job account's binding retitled for another account
		// would be too.
		if _, ok := c.jobAccountGrant(rc, createdSAIDs, heldSAIDs, title, gcp.TokenBucketConditionTitlePrefix); !ok {
			return "grants objectViewer on the bucket under one repository's tokens/<slug>/ condition to a member that is not the job account its title names, which the modules never do"
		}
	case role == "roles/storage.objectAdmin" || role == "roles/storage.objectViewer":
		if len(conds) != 0 {
			return "grants " + role + " on the bucket under a condition, which the modules never do"
		}
	case role == "roles/storage.objectUser" && person:
		for _, b := range c.Buckets {
			// A configured bucket name is not charset-checked everywhere it is
			// read (localcfg only validates runs_bucket, not a bucket_url): skip
			// one gcp.LauncherBucketCondition would panic on, rather than crash
			// the plan review over a config problem this function does not own.
			if !gcp.ValidBucketName(b) {
				continue
			}
			if (bucket == "" || bucket == b) && title == gcp.LauncherBucketConditionTitle && expr == gcp.LauncherBucketCondition(b) {
				return ""
			}
		}
		return "grants objectUser on the bucket to a person under a condition that is not the launchers' runs/ and tokens/ condition"
	case role == "roles/storage.objectUser":
		if len(conds) == 0 {
			return "grants objectUser on the bucket to an account with no condition"
		}
		if c.projectJobCondition(rc, createdSAIDs, heldSAIDs, title, expr) {
			return ""
		}
		for _, clause := range strings.Split(expr, " || ") {
			m := accountClause.FindStringSubmatch(clause)
			if m == nil || !slices.Contains(c.Buckets, m[1]) || (bucket != "" && bucket != m[1]) {
				return "grants objectUser on the bucket to an account under a condition that is not a Fugaro prefix condition of this run's bucket"
			}
		}
	}
	return ""
}

// projectModule is the module path of a project job's resources: the
// installation root calls deploy/terraform/gcp/modules/project as the module
// "project", once per (profile, provider, account)
// (roots/installation/main.tf). The path is what says a grant belongs to that
// module — the account IDs cannot: a per-repository job account matches
// jobAccount's shape too (fugaro-<readable slug>-<workflow>-<8 hex>), so a
// repository whose slug begins with "p" gets an ID of the project job's own
// shape, and the widened condition would then be accepted for it.
var projectModule = regexp.MustCompile(`^module\.project\["[^"]+"\]$`)

// projectJobCondition reports whether a bucket grant is a project job's
// widened one (plan task 3.2 step 2, gcp.ProjectBucketCondition), for the
// account the grant is written for: the grant lives in the project module, the
// expression is that builder's bytes for one of this run's buckets — no slug,
// and no other expression can pass, since this compares the whole string — the
// bucket is that one, and the title is the condition title of the module's
// own account: the one the member names, which the same module must hold in
// this plan (an update plan lists it as a no-op), or, when the member is
// computed as the module writes it, one this plan creates in the same module.
//
// Both parts are what keep the widening bounded: the widened condition is the
// project module's own, for the accounts that module creates, so widening a
// per-repository job's grant — in its workflow module, where the account is
// created just the same — or granting it to an account of another module, is
// still a plan the review screen stops.
func (c Cover) projectJobCondition(rc ResourceChange, createdSAIDs, heldSAIDs map[string]bool, title, expr string) bool {
	if _, ok := c.projectJobGrantAccount(rc, createdSAIDs, heldSAIDs, title, gcp.BucketConditionTitlePrefix); !ok {
		return false
	}
	bucket := str(rc.Change.After["bucket"])
	for _, b := range c.Buckets {
		if !gcp.ValidBucketName(b) || (bucket != "" && bucket != b) {
			continue
		}
		if expr == gcp.ProjectBucketCondition(b, gcp.JobBucketPrefixes) {
			return true
		}
	}
	return false
}

// projectJobTokenCondition reports whether a bucket grant is a project job's
// read-only grant on the bucket's budget token objects
// (gcp.ProjectTokenBucketCondition, plan task 3.2 step 2), for the account
// the grant is written for. It is projectJobCondition's own account rule —
// the project module, an account that module creates or holds, the title
// built from that account — with the tokens/ builder's title and expression,
// so the same plan a per-repository job's token grant comes from cannot be
// read as this one.
func (c Cover) projectJobTokenCondition(rc ResourceChange, createdSAIDs, heldSAIDs map[string]bool, title, expr string) bool {
	if _, ok := c.projectJobGrantAccount(rc, createdSAIDs, heldSAIDs, title, gcp.ProjectTokenBucketConditionTitlePrefix); !ok {
		return false
	}
	bucket := str(rc.Change.After["bucket"])
	for _, b := range c.Buckets {
		if !gcp.ValidBucketName(b) || (bucket != "" && bucket != b) {
			continue
		}
		if expr == gcp.ProjectTokenBucketCondition(b) {
			return true
		}
	}
	return false
}

// projectJobGrantAccount is jobAccountGrant for the project module: the
// widened conditions are the project module's own, so a grant a repository's
// workflow module writes is never read as one.
//
// The module path is what says a grant belongs to the project module — the
// account IDs cannot (see projectModule).
func (c Cover) projectJobGrantAccount(rc ResourceChange, createdSAIDs, heldSAIDs map[string]bool, title, titlePrefix string) (string, bool) {
	if !projectModule.MatchString(modulePrefix(rc)) {
		return "", false
	}
	return c.jobAccountGrant(rc, createdSAIDs, heldSAIDs, title, titlePrefix)
}

// jobAccountGrant is the account rule the project module's two conditions and
// a repository job's tokens/ grant share: the condition's title is titlePrefix
// + the account's ID, and the account is one the grant's own module creates or
// holds in this plan. It returns the ID and whether the grant passed.
//
// An account named outright (an update plan, where the account already exists)
// must be a Fugaro job account of this run that the same module holds, not
// merely any job account of the project, which would let one module's grant
// widen another profile's or a repository's account; a computed member must be
// one the same module creates in this plan, since the title is built from it.
// The title is what says whose grant it is: an expression a job account's grant
// carries says nothing about the member, so without this check any member could
// hold the same expression and be covered.
func (c Cover) jobAccountGrant(rc ResourceChange, createdSAIDs, heldSAIDs map[string]bool, title, titlePrefix string) (string, bool) {
	var id string
	switch m := saMember.FindStringSubmatch(str(rc.Change.After["member"])); {
	case m != nil:
		if !slices.Contains(c.Projects, m[2]) || !jobAccountID(m[1]) || !heldSAIDs[modulePrefix(rc)+"|"+m[1]] {
			return "", false
		}
		id = m[1]
	case unknown(rc.Change, "member"):
		// The computed member the module writes: the title is built from the
		// account the same module creates, so the plan must create it.
		id = strings.TrimPrefix(title, titlePrefix)
		if id == "" || id == title || !createdSAIDs[modulePrefix(rc)+"|"+id] {
			return "", false
		}
	default:
		return "", false
	}
	if title != titlePrefix+id {
		return "", false
	}
	return id, true
}

func (c Cover) fugaroAccount(m string) bool {
	sm := saMember.FindStringSubmatch(m)
	if slices.Contains(c.Accounts, m) && strings.HasPrefix(m, "serviceAccount:") {
		return true
	}
	if sm == nil || !slices.Contains(c.Projects, sm[2]) {
		return false
	}
	return fixedAccountID(sm[1]) || jobAccountID(sm[1])
}

// fixedAccountID is whether a service account ID is one of the accounts the
// modules create once per installation.
func fixedAccountID(id string) bool { return slices.Contains(FugaroAccounts, id) }

// jobAccountID is whether a service account ID has the shape of a per-job
// account (fugaro-<name>-<8 hex>).
func jobAccountID(id string) bool { return jobAccount.MatchString(id) }

// roleKey is the grantRules key of a role: a predefined role by name, a
// custom role of this run's projects as customKey+ID. ok is false for any
// other role, including a custom role the modules do not create.
func (c Cover) roleKey(r string) (string, bool) {
	if strings.HasPrefix(r, "roles/") {
		return r, allowedRoles[r]
	}
	for _, p := range c.Projects {
		if id, ok := strings.CutPrefix(r, "projects/"+p+"/roles/"); ok && slices.Contains(FugaroRoles, id) {
			return customKey + id, true
		}
	}
	return "", false
}

// modulePrefix is the module path of a resource's address.
func modulePrefix(rc ResourceChange) string {
	if i := strings.LastIndex(rc.Address, "."+rc.Type+"."); i >= 0 {
		return rc.Address[:i]
	}
	return ""
}

// importOK: an import's ID names a resource of one of this run's projects.
func (c Cover) importOK(id string) bool {
	for _, p := range c.Projects {
		if strings.HasPrefix(id, p+"/") || strings.HasPrefix(id, "projects/"+p+"/") {
			return true
		}
	}
	return false
}

// at walks a plan value through blocks (lists of objects, of which the first is
// taken) and objects; a list of one object at the end reads as that object.
func at(v any, path ...string) any {
	for _, k := range path {
		if l, ok := v.([]any); ok {
			if len(l) == 0 {
				return nil
			}
			v = l[0]
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	if l, ok := v.([]any); ok && len(l) == 1 {
		if _, isMap := l[0].(map[string]any); isMap {
			return l[0]
		}
	}
	return v
}

func str(v any) string { s, _ := v.(string); return s }

func strList(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		out = append(out, str(x))
	}
	return out
}

// objects reads a block that may have collapsed to one object.
func objects(v any) []map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return []map[string]any{x}
	case []any:
		var out []map[string]any
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// attributes checks what a covered resource points at: images in this run's
// registry, schedulers calling this run's jobs as Fugaro's accounts, a sink to
// this project, a private bucket, this run's projects and buckets, the
// billing account the review showed, and the web key's two API targets. ""
// when all is as the modules write it.
func (c Cover) attributes(rc ResourceChange, creates map[string]bool) string {
	a := rc.Change.After
	if a == nil {
		a = map[string]any{}
	}
	proj := ""
	if len(c.Projects) > 0 {
		proj = c.Projects[0]
	}
	pre := modulePrefix(rc)
	if unknown(rc.Change, "project") {
		return "in a project that is not known"
	}
	if p := str(a["project"]); p != "" && !slices.Contains(c.Projects, p) {
		return "in project " + p + ", which is not this run's"
	}
	if rc.Type == "google_storage_bucket_object" || (strings.HasPrefix(rc.Type, "google_storage_bucket_") && strings.HasSuffix(rc.Type, "_iam_member")) {
		switch b := str(a["bucket"]); {
		case b != "":
			if !slices.Contains(c.Buckets, b) {
				return "on bucket " + b + ", which is not this run's"
			}
		case !unknown(rc.Change, "bucket") || !creates[pre+"|google_storage_bucket"]:
			return "on a bucket that is not known"
		}
	}
	if strings.HasPrefix(rc.Type, "google_artifact_registry_repository_iam") {
		if r := str(a["repository"]); r != "" && !repoID.MatchString(r) {
			return "on repository " + r + ", which is not a Fugaro registry"
		}
	}
	switch rc.Type {
	case "google_cloud_run_v2_job":
		cs := objects(at(a["template"], "template", "containers"))
		unk := rc.Change.AfterUnknown
		if unknownAt(unk, "template", 0, "template", 0, "containers") {
			return "containers that are not known"
		}
		if len(cs) == 0 {
			return "no container"
		}
		for i, m := range cs {
			cp := []any{"template", 0, "template", 0, "containers", i}
			for _, attr := range []string{"image", "command", "args"} {
				if unknownAt(unk, append(slices.Clone(cp), attr)...) {
					return "a container " + attr + " that is not known"
				}
			}
			if img := str(m["image"]); !c.baseImageOK(img, proj) {
				return fmt.Sprintf("runs the image %q, which is outside this run's registry %s/%s/ or not a base image of it in the shape this installation pins (%s/%s/%s/<owner>/fugaro-<kind>:<version>@sha256:<digest>)",
					img, c.Registry, proj, c.Registry, proj, c.Ghcr)
			}
			cmd, args := strList(m["command"]), strList(m["args"])
			known := false
			for _, k := range knownCommands {
				known = known || (slices.Equal(cmd, k[0]) && slices.Equal(args, k[1]))
			}
			if !known {
				return "runs a command the modules do not set"
			}
			if why := c.checkSpecs(m, unk, append(slices.Clone(cp), "env"), proj); why != "" {
				return why
			}
		}
	case "google_cloud_scheduler_job":
		ht := at(a["http_target"])
		if at(rc.Change.AfterUnknown, "http_target", "uri") == true || unknown(rc.Change, "http_target") {
			// The job's name and location are known only once the job this
			// module creates exists.
			if !creates[pre+"|google_cloud_run_v2_job"] {
				return "calls a job that is not known and none is created with it"
			}
		} else {
			uri := str(at(ht, "uri"))
			u, err := url.Parse(uri)
			if err != nil || u.Scheme != "https" || u.Host != "run.googleapis.com" || !strings.HasPrefix(u.Path, "/v2/projects/"+proj+"/locations/"+c.Region+"/jobs/") || !strings.HasSuffix(u.Path, ":run") {
				return fmt.Sprintf("calls %q, not a job of this run's project and region on run.googleapis.com", uri)
			}
		}
		if at(rc.Change.AfterUnknown, "http_target", "oauth_token", "service_account_email") == true {
			if !creates[pre+"|google_service_account"] {
				return "calls as an account that is not known and none is created with it"
			}
		} else if email := str(at(ht, "oauth_token", "service_account_email")); email == "" || !c.fugaroAccount("serviceAccount:"+email) {
			return "calls as " + email + ", not a Fugaro service account of this run"
		}
	case "google_logging_project_sink":
		if unknown(rc.Change, "destination") {
			if !creates[pre+"|google_logging_project_bucket_config"] {
				return "sends logs to a place that is not known and no log bucket is created with it"
			}
		} else if d := str(a["destination"]); !strings.HasPrefix(d, "logging.googleapis.com/projects/"+proj+"/") {
			return fmt.Sprintf("sends logs to %q, not a log bucket of this project", d)
		}
	case "google_storage_bucket":
		if a["public_access_prevention"] != "enforced" || a["uniform_bucket_level_access"] != true {
			return "a bucket without public access prevention enforced and uniform access"
		}
	case "google_billing_budget":
		acct := strings.TrimPrefix(str(a["billing_account"]), "billingAccounts/")
		if acct == "" || !slices.Contains(c.Billing, acct) {
			return "a budget on billing account " + acct + ", which the review did not show"
		}
	case "google_apikeys_key":
		var got []string
		for _, t := range objects(at(a["restrictions"], "api_targets")) {
			got = append(got, str(t["service"]))
		}
		slices.Sort(got)
		if !slices.Equal(got, APITargets()) {
			return "a key whose API targets are not the web key's two"
		}
	}
	return ""
}

// checkSpec is the part of the check job's FUGARO_CHECK_SPEC that points at
// registries; it decodes as fugaro image check --job does (infra.CheckJobSpec
// with the same tags: encoding/json's case-insensitive keys and last
// duplicate), so the two cannot read different values out of one string.
type checkSpec struct {
	Registry            string            `json:"registry"`
	BuildServiceAccount string            `json:"build_service_account"`
	BaseImages          map[string]string `json:"base_images"`
}

// refChars are the characters an image reference may hold here: the spec's
// refs reach a Dockerfile FROM line and a build's arguments.
var refChars = regexp.MustCompile(`^[A-Za-z0-9._:/@-]+$`)

// pinNameRE, pinVersionRE and pinDigestRE are the pin's own parts, as
// baseImageOK reads them: the image (fugaro-<kind>), the version tag it
// keeps as the release marker, and the digest it must carry (design §3.6).
//
// pinVersionRE is a RELEASE version, not any tag: every reader of a pin
// works the version marker out of the tag (managedVersion, baseIsCurrent,
// refreshVerdict's never-downgrade rule), so a path tagged "latest" or a
// dev sha is not a pin this tool wrote and must not be covered here — the
// same shape releaseRE and baseImageRE enforce on the CLI's side.
var (
	pinNameRE    = regexp.MustCompile(`^fugaro-[a-z0-9]+(?:-[a-z0-9]+)*$`)
	pinVersionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	pinDigestRE  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// buildAccount: the spec's build_service_account is the email of a Fugaro job
// account (fugaro-<readable>-<8 hex>) in one of this run's projects. The fixed
// accounts (scheduler, history, token signer) are not build accounts.
func (c Cover) buildAccount(email string) bool {
	sm := saMember.FindStringSubmatch("serviceAccount:" + email)
	return sm != nil && slices.Contains(c.Projects, sm[2]) && jobAccountID(sm[1])
}

// baseImageOK reports whether ref may appear as an image in a covered plan:
// under this run's registry, then either outside the remote (pull-through)
// repository's ID — the installation's own repositories, as before — or in
// the base pin's closed shape (design registry-remote-repository.md §3.6):
//
//	<host>/<project>/<ghcr>/<owner>/fugaro-<kind>:<version>@sha256:<64 hex>
//
// The proxy serves ALL of ghcr.io, so "under <registry>/<project>/" stops
// confining anything once that repository exists, and its upstream is not
// ours to vouch for: everything under its ID must therefore name the
// RECORDED owner (c.Owner), a fugaro-<kind> image, a version tag — the tag
// is the version marker every reader of a pin works from — and a digest.
// The owner and the repository ID are the caller's (c.Ghcr, c.Owner), and
// an empty owner refuses every remote path. refChars and the ".." guard are
// the ones this check has always had.
//
// What this closes is bounded, and the bound is design §10.7's: the owner
// and the entry it is checked against can be published in the same shared
// config, so against whoever writes that document this is a *consistency*
// check, not a provenance guarantee. What it does close is real: a foreign
// owner, a bare tag, a missing digest or any other shape in an entry whose
// config records our owner — a hand-edited config, a stale entry, an
// accidental paste.
func (c Cover) baseImageOK(ref, proj string) bool {
	prefix := c.Registry + "/" + proj + "/"
	if c.Registry == "" || !strings.HasPrefix(ref, prefix) || len(ref) == len(prefix) || !refChars.MatchString(ref) ||
		slices.Contains(strings.Split(ref, "/"), "..") {
		return false
	}
	if c.Ghcr == "" || !strings.HasPrefix(ref, prefix+c.Ghcr+"/") {
		return true
	}
	owner, rest, ok := strings.Cut(strings.TrimPrefix(ref, prefix+c.Ghcr+"/"), "/")
	if !ok || c.Owner == "" || owner != c.Owner {
		return false
	}
	name, tag, ok := strings.Cut(rest, ":")
	if !ok || !pinNameRE.MatchString(name) {
		return false
	}
	version, digest, ok := strings.Cut(tag, "@")
	return ok && pinVersionRE.MatchString(version) && pinDigestRE.MatchString(digest)
}

// checkSpecs checks every FUGARO_CHECK_SPEC entry of a container's
// environment (envPath is the env list's path in after_unknown): the spec's
// registry and each base image it builds FROM must be in this run's registry,
// so a job that the covered plan creates can never build from or push to
// anything else. An environment that is not fully known is not covered.
func (c Cover) checkSpecs(m map[string]any, unk any, envPath []any, proj string) string {
	if unknownAt(unk, envPath...) {
		return "an environment that is not known"
	}
	prefix := c.Registry + "/" + proj + "/"
	inRegistry := c.baseImageOK
	for j, e := range objects(m["env"]) {
		ep := append(slices.Clone(envPath), j)
		if unknownAt(unk, append(slices.Clone(ep), "name")...) {
			return "an environment variable whose name is not known"
		}
		if e["name"] != "FUGARO_CHECK_SPEC" {
			continue
		}
		if unknownAt(unk, append(slices.Clone(ep), "value")...) {
			return "a check spec that is not known"
		}
		var s checkSpec
		if err := json.Unmarshal([]byte(str(e["value"])), &s); err != nil {
			return "a check spec that is not the JSON the check job reads"
		}
		if !inRegistry(s.Registry, proj) {
			return fmt.Sprintf("a check spec whose registry %q is outside this run's %s", s.Registry, prefix)
		}
		// The account the check's builds run as: one of this run's Fugaro job
		// accounts (the repository's build account), never an arbitrary one.
		if !c.buildAccount(s.BuildServiceAccount) {
			return fmt.Sprintf("a check spec whose build service account is %q, not a Fugaro service account of this run", s.BuildServiceAccount)
		}
		for _, k := range slices.Sorted(maps.Keys(s.BaseImages)) {
			if ref := s.BaseImages[k]; !inRegistry(ref, proj) {
				return fmt.Sprintf("a check spec whose base image %s is %q, outside this run's %s", k, ref, prefix)
			}
		}
	}
	return ""
}
