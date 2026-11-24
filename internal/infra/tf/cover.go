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

	"github.com/dimipaun/fugaro/internal/access"
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
	// The credentials service (D146, C1.18): the service itself and each job
	// account's roles/run.invoker on it. Every `fugaro update` deploys the
	// service now (DON1, the owner's ruling of 2026-11-05), so the
	// installation's own plan carries both — which is why the two are covered
	// by the step that plans it. An agent's session never writes the block
	// (init_stages.go's ensureCredentialsService returns early under a marker), and
	// an installation that cannot carry the service gets none.
	"google_cloud_run_v2_service":            true,
	"google_cloud_run_v2_service_iam_member": true,
	"google_cloud_scheduler_job":             true,
	// The Batch backend's own root (design compute-backend §8.1 task 6) is a
	// root of its own, applied by its own stage behind --provision-batch and
	// its own typed confirmation: these three types are the NAT, its router
	// and the deny-all ingress rule on the run-class accounts, and they are
	// listed here because every module the tree embeds has to be decided on.
	// The installation root does not include that module, so no plan this one
	// confirmation covers can carry them. What the compute stage does run
	// over its own plan is GrantsNotCovered: the grant rules below, the
	// launchers' Batch Job Editor condition among them.
	"google_compute_firewall":           true,
	"google_compute_router":             true,
	"google_compute_router_nat":         true,
	"google_firebase_database_instance": true,
	"google_firebase_project":           true,
	// The run index's composite indexes (RI2, contract-index.md): the plan is
	// the owner-run Firebase root's, because no job may change an index.
	"google_firestore_index": true,
	// The consumer's signing key's three types (plan task J6; O17).
	"google_kms_crypto_key":                   true,
	"google_kms_crypto_key_iam_member":        true,
	"google_kms_key_ring":                     true,
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
// Fugaro service accounts of this run, Deployer the deployer account of D75
// alone — its own kind, so that its eight roles name it and nothing else, and
// no row meant for Fugaro's accounts accepts it — and Credentials the
// credentials service's account, which is its own kind for the same reason.
//
// Why Credentials is not simply one of Accounts (the ledger finding this kind
// answers, #903). A toAccounts row is the intersection of what the
// installation's jobs need, so an account merely being Fugaro's own said
// nothing about which of those roles it may hold: with fugaro-credentials in
// FugaroAccounts, a plan granting it the history account's datastore.user, a
// job runner's run.jobs.run or a launcher's role was covered by init's one
// confirmation — a grant nobody wrote in a module, accepted because the
// account's NAME was. Its own kind makes every row state, one by one, whether
// it accepts this account: today exactly two do, the bundle's accessor and its
// own database role, and a third means editing this table on purpose.
// TestCredentialsAccountIsItsOwnPrincipalKind is what holds that.
//
// Coordinator is the coordinator's writer account (coordinator plan P3), its
// own kind for the same reason: it holds the FP's database admin and nothing
// else, and as one of FugaroAccounts every toAccounts row would have accepted
// it. TestCoordinatorAccountIsItsOwnPrincipalKind holds that.
//
// Verifier is the verifier service account (CSR14, D1068), the installation's
// own caller of the credentials service's verify route, and it is its own kind
// for the coordinator's reason and one more of its own: the one grant it holds
// is the invoker bit on that service, and the ONE grant it RECEIVES is the
// operators' impersonation (deployerTokenCreator accepts this account beside
// the deployer's). As one of FugaroAccounts a plan could have added any
// toAccounts row's role to an identity an operator can become — and the whole
// point of the account is that it reaches the one route and nothing else
// (TestVerifierTokenCreatorIsCoveredByOneUpdate reads both halves).
//
// CatalogCheck is the project's catalog check job account (D1148 task (ii)),
// its own kind for the same reason and one more: its grants are things no job
// account holds — actAs on EVERY repository's build account, a read of every
// repository's build records and check state, the copied catalog definitions
// project-wide — so as one of FugaroAccounts a plan could have added any
// toAccounts row's role to it, and as its own kind every one of its grants is
// refused until a row names that exact (type, role, condition) on purpose. The
// rules that accept a job account's grants would otherwise accept the project
// job's too: a build account's read of the copied definitions carries the
// expression byte for byte (catalogReadGrant), and the records grant carries
// the shape of a per-repository prefix condition, so only the account differs
// (TestCatalogCheckAccountIsItsOwnPrincipalKind, TestCatalogCheckRecordsGrant).
//
// MCP is the front door's own account (D920, D935), its own kind for the
// catalog check job's reason: as one of FugaroAccounts every toAccounts row
// would have accepted it, and the account's whole point is that it reaches the
// runs bucket's read (D932), the store its records live in (D935) and — since
// O16b — the orchestrator's node, read-only (D2020 (a)), and nothing else — a
// public endpoint whose identity could ride a job's datastore.user or a
// launcher's role would be the "there is no credential to reach" of spec.md §8
// one grant away from false. Exactly two rows carry the bit, and each is
// narrowed further: the store's by the bucket it is on (mcpauthStoreGrant) and
// the state group's by its role and the project it is in
// (mcpauthStateReadGrant, TestMCPAccountIsItsOwnPrincipalKind).
//
// Intake is the secrets intake's own account (D1931, DS2.2), its own kind for
// the catalog check job's reason: as one of FugaroAccounts every toAccounts row
// would have accepted it, and this is the one account that may CREATE, add a
// version to and DELETE the installation's dynamic secret containers — a write
// reach no job account has. Exactly two rows carry the bit
// (fugaroIntakeSecrets, which is conditioned by name, and the shared
// fugaroSecretLister), each narrowed further by the condition it must carry
// (dynamicSecretsGrant) and, for the runs bucket's one object, by the bucket
// and the object (intakeAdminListGrant).
type who uint16

const (
	toPeople who = 1 << iota
	toAccounts
	toDeployer
	toCredentials
	toCoordinator
	toVerifier
	toCatalogCheck
	toMCP
	toIntake
	// The reports job's own kind (slice P, ruling E4), appended rather than
	// inserted: the bits above are the ones a test's mask and every row in
	// grantRules are written with, and a shift would rewrite all of them.
	// ReportsAccountID says why the account is its own kind.
	toReports
	// The licence renewal job's own kind (plan K4.2), appended for the same
	// reason: LicenceAccountID says why the account is its own kind.
	toLicence
	// The resident orchestrator's own kind (mobile plan task P5.1b;
	// docs/mobile/design/always-on-orchestrator.md §4.3), appended rather than
	// inserted like the reports job's bit above and for the same reason.
	// OrchestratorAccountID says why the account is its own kind and not one of
	// FugaroAccounts.
	toOrchestrator
	// Voice is the voice hop's own account (P5.3.3), its own kind for the
	// reports job's reason: as one of FugaroAccounts every toAccounts row would
	// have accepted it, and this is the account a PHONE's request runs as — an
	// endpoint anyone on the internet can reach (voice.tf's allUsers binding),
	// reading the installation's whole Realtime Database instance through
	// fugaroStateRead. Two rows carry the bit: the custom read role, which is
	// also the front door's, narrowed further by voiceStateReadGrant to that
	// role, on the project the module declares it in, under no condition; and
	// — since P5.3.5's forwarding half — the per-container
	// roles/secretmanager.secretAccessor on the provider-key container, whose
	// target voiceProviderKeyGrant reads for the renewal job's reason.
	// Appended rather than inserted, for toReports' reason.
	//
	// The hop's OTHER reach — the allUsers member on its own invoker binding —
	// is deliberately NOT a kind here, which is the front door's own state and
	// the front door's own reason (coordinator.tf): no member of this tree is
	// everybody, so a plan that grants to `allUsers` is a plan with a principal
	// nobody classified, and the update that carries it shows that finding and
	// applies it rather than passing it as covered. What closes the reach is
	// the Terraform suite's TestAllUsersIsRefusedOnEveryOtherResource (by path
	// and by block) and internal/infra/managed's refusal, not a row here.
	toVoice
	// The build identity's own kind (B5, D2301), appended for the reports
	// job's reason. It is not a job account of a repository and not a person:
	// BuildIdentityAccountID says why it is a kind of its own.
	toBuildIdentity
	// The public mobile service's own kind (mobile plan task P5; D2782 as
	// amended by F31, O8), appended rather than inserted for the reports job's
	// reason. MobileAccountID says why it is a kind of its own.
	//
	// It carries exactly two rows, and both are narrower than the bit: the
	// unchanged database role fugaroCredentialsDatabase — on the FIREBASE
	// project, under no condition (O1, whole instance, rules bypassed) — and
	// iam.serviceAccounts.signJwt on the device-only signer of that project,
	// which mobileMinterGrant holds to this run's own signer account. The
	// account's remaining reach is its PUBLIC invoker binding, whose member is
	// allUsers: that is deliberately not a kind here, for the hop's own reason
	// (toVoice) — a plan that grants to everybody is a plan with a principal
	// nobody classified and the step that carries it shows the finding, while
	// the Terraform suite's TestAllUsersIsRefusedOnEveryOtherResource and
	// internal/infra/managed are what refuse a second one.
	toMobile
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
		// toOrchestrator is the resident orchestrator's bit (mobile plan task
		// P5.1b, docs/mobile/design/always-on-orchestrator.md §4.3): the row is
		// the launcher's own, and the design's table says the resident
		// orchestrator holds "the launcher's grants assembled from resources
		// that already exist" — so the same role is granted to the account one
		// binding at a time rather than by adding it to var.launchers, which is
		// what the eleventh never-rule forbids and what would widen this row's
		// meaning to "a list" instead of "a role".
		customKey + "fugaroLauncher":       toPeople | toOrchestrator,
		customKey + ViewerRole:             toPeople,
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
		customKey + CloudAssetRole: toPeople,
		// The credentials service's own database role: the credentials account
		// and, since mobile plan task P5, the public mobile service's account,
		// each in a binding of its own in credentials.tf (O1: the role is
		// unchanged — whole instance, rules bypassed — and the mobile binding is
		// the one project-level binding the task adds). Granting it to a job
		// account or to a person is not a grant the installation makes.
		customKey + CredentialsDatabaseRole: toCredentials | toMobile,
		// The service's Vertex role (CS11, D3174): the installation project's
		// own role, holding aiplatform.endpoints.predict and nothing else,
		// bound to the service's own account in credentials.tf and to nothing
		// else. A chain whose models are served on Vertex calls it as this
		// service, so the reach has to be this account's and no job's; the
		// jobs' own roles/aiplatform.user is the release-1 path CS11 keeps
		// only under budget.mode: observe (X8 retires it).
		customKey + CredentialsVertexRole: toCredentials,
		// The dynamic-delivery class (D1931, DS2.2). fugaroIntakeSecrets is
		// create, version-add and delete of the grammar's containers, held by
		// the intake's own account and by nothing else, and only under the
		// name-prefix fence (dynamicSecretsGrant reads the condition, which
		// grantRules cannot see); fugaroSecretLister is a name-and-metadata
		// read the two services share, because secretmanager.secrets.list is
		// refused under that same condition (Check 510) — so it is
		// unconditioned on purpose and it is the one row of this class that
		// takes two principals.
		customKey + IntakeSecretsRole: toIntake,
		customKey + SecretListerRole:  toIntake | toCredentials,
		// The front door's read of the orchestrator's node (O16b, D2020 (a)):
		// a PROJECT grant on the coordinator's own Firebase project, where the
		// database lives (D6), and the account's second role. Like the store's
		// bit, this one is not the row's to decide: mcpauthStateReadGrant
		// accepts exactly this account, exactly this role, on exactly the
		// project the role was declared in and under no condition, so a plan
		// that moved the read to another project, another role to this
		// account, or a write permission into the role (which RolePermissions
		// pins) is refused rather than covered.
		customKey + MCPStateReadRole: toMCP | toVoice,
		// The service's project-level accessor over the same fence: a PROJECT
		// grant, unlike the bundle's (google_secret_manager_secret_iam_member),
		// so the row is here rather than in that type's, and it names the
		// credentials account alone — a job account holding a project-wide
		// secretAccessor is precisely what DS2.2 exists to stop.
		"roles/secretmanager.secretAccessor": toCredentials,
		"roles/aiplatform.user":              toAccounts,
		"roles/batch.agentReporter":          toAccounts,
		"roles/batch.jobsEditor":             toPeople, // only under the Fugaro job-name condition (jobsCondition); never an account
		"roles/datastore.user":               toAccounts,
		"roles/datastore.viewer":             toPeople,
		"roles/firebaseauth.admin":           toAccounts,
		"roles/firebasedatabase.admin":       toPeople | toAccounts | toCoordinator,
		"roles/firebasedatabase.viewer":      toPeople,
		// logging.logWriter's toBuildIdentity is what a catalog build's own
		// step logs need (B6): a build that names a user-specified service
		// account is written to Cloud Logging as that account, and the role
		// is project-wide because Cloud Logging offers the write no narrower
		// — appending entries, and nothing else. The per-repository build
		// account the identity replaces held the same role at the same level
		// (modules/repo/build.tf's `build` row).
		"roles/logging.logWriter":                 toAccounts | toBuildIdentity,
		"roles/serviceusage.serviceUsageConsumer": toPeople | toAccounts,
	},
	"google_service_account_iam_member": {
		// toOrchestrator is the design's token-minter row (§4.3): "may mint is
		// exactly may launch", and the resident orchestrator launches runs, so
		// it holds the same role on the same signer the people on var.launchers
		// and var.operators hold. It is the widest grant in that table and the
		// design says so rather than pretending otherwise (E4), which is why
		// the bit is here on purpose: an account that may mint any claim set
		// for any uid is one the review has to see classified, not one a
		// widened toAccounts row would quietly accept. Since mobile plan task P5
		// the row carries toMobile too: the public mobile service's own account
		// holds the same role on the DEVICE-ONLY signer of the Firebase project
		// (a second account, because Firebase accepts a custom token only from
		// an account of the Firebase project), and it is the row's kind for the
		// orchestrator's reason — what narrows it further is mobileMinterGrant,
		// which reads the target account and reads it in this run's own Firebase
		// project, so the grant the module writes on the devices' signer is the
		// only one this cell admits.
		//
		// toCredentials is the issuer's own row (launcher-does-not-mint plan
		// task LM13; D3121, F7): the credentials service signs a run's identity
		// as the runs' signer once the people's row goes, and that row is the
		// ONE grant this design adds. It is here on the row's own terms and not
		// as a widening: the cell says the account may hold fugaroTokenMinter,
		// and issuerMinterGrant — the rule beside mobileMinterGrant — is what
		// holds it to this run's signer and to nothing else, the way the
		// orchestrator's and the mobile service's bits are held to theirs.
		customKey + "fugaroTokenMinter": toPeople | toOrchestrator | toMobile | toCredentials,
		// The operators' actAs on the accounts a build runs as. Two module
		// sites hold it now: the per-repository build account
		// (modules/repo/build.tf's operator_build, on google_service_account
		// .build) and — since B6 moved the catalog build onto the project's
		// build identity — that identity (modules/installation/
		// build_identity.tf's operator_build_identity, on the literal account
		// path). Both are people acting as an account of the installation's
		// own, which is this row's own kind; the account the role is on is
		// what the module's literal spelling pins, since this table sees only
		// the member.
		"roles/iam.serviceAccountUser": toPeople | toAccounts,
		// D75's one-time impersonation setup: the operators, and nobody else,
		// may assume the deployer account. On an account, for people — the
		// grant that makes the managed apply possible and the only grant of
		// this role anywhere (deployer.tf). Which account it is on is what
		// deployerGrant checks, since this table sees only the member.
		DeployerTokenCreatorRole: toPeople,
	},
	"google_storage_bucket_iam_member": {
		// objectAdmin is the operators' role on the runs bucket (iam.tf), and
		// — since M4's store half — the front door's account's one role on the
		// store D935 gives it (modules/installation/mcpauth_store.tf). The
		// account is its own kind (toMCP) and not a job account, and what
		// makes its grant the module's own is not the row but the BUCKET:
		// mcpauthStoreGrant accepts exactly this account, exactly this role, on
		// exactly `fugaro-mcpauth-<the installation's project>` and under no
		// condition, so a plan that moved the role to another bucket, or this
		// bucket's role to another member, is refused —
		// TestMCPAccountIsItsOwnPrincipalKind's subject.
		"roles/storage.objectAdmin": toPeople | toMCP,
		// objectUser is people only under the launchers' condition
		// (bucketCondition). objectViewer's toCoordinator is the aggregation
		// job's reader on the runs bucket (coordinator plan P5): the account is
		// its own kind, so the row says on purpose that the writer may read the
		// run records its view is computed from — and nothing else of the
		// bucket, which is what makes this the row's only coordinator bit.
		//
		// objectUser's toCatalogCheck is the project's catalog check job
		// holding ONE repository's build records and check state
		// (modules/installation/catalogcheck.tf's catalog_check_records,
		// D1148). The bit is here rather than among the account rows because
		// that account is a kind of its own, and what makes this grant the
		// modules' own is not the member but the CONDITION: builds/<slug>/
		// catalog/ of one of this run's buckets, byte for byte
		// gcp.CatalogRecordsCondition, under the title built from the same
		// slug — bucketCondition's catalogRecordsGrant checks exactly that,
		// and refuses the account under any other condition. Without that
		// check the bit would let a plan hand this account every repository's
		// records and check state (the account's own read is
		// TestCatalogCheckRecordsGrant's subject).
		//
		// objectUser's toReports is the reports job writing reports/ — the one
		// writer that prefix has (ruling 3), which is why no launcher's write
		// condition grows it and no job account holds it. Like the catalog
		// check's row, what makes the grant the modules' own is the condition
		// and not the member: reports/<the prefix> of one of this run's
		// buckets, byte for byte gcp.ReportsWriteCondition, under the title
		// built from that account (reportsWriteGrant). Without that check the
		// bit would let a plan hand this account objectUser on runs/ — the
		// launchers' own write area — or on another bucket.
		// objectUser's toLicence is the licence renewal job writing the one
		// object its whole reason to exist is (plan K4.2): the licence, at its
		// own name in the runs bucket and nowhere else. Like the reports row
		// above, what makes the grant the modules' own is the condition and not
		// the member — gcp.LicenceWriteCondition's one name, byte for byte — so
		// without licenceWriteGrant the bit would let a plan hand this account
		// objectUser on runs/ or on another of this run's buckets.
		//
		// objectUser's toBuildIdentity is the record step of a catalog build
		// (B6): the build runs as the project's identity, and the record it
		// writes is one repository's, so the grant is one condition per
		// repository — the same bytes the catalog check's own grant carries
		// (gcp.CatalogRecordsCondition) — and catalogRecordsGrant is what
		// pins it to the identity and to those bytes. Without the check the
		// bit would let a plan hand this account a project-wide builds/
		// prefix, reaching every repository's check state and the copied
		// definitions beside them.
		"roles/storage.objectUser": toPeople | toAccounts | toCatalogCheck | toReports | toLicence | toOrchestrator | toBuildIdentity,
		// The intake's bit is on the reader row and not on the writer's: what
		// the class reads is the derived admin list, one object of the runs
		// bucket (intakeAdminListGrant checks the bucket AND the object), and
		// its own writes go to Secret Manager, never to the runs bucket.
		//
		// objectViewer's toReports is the reports job reading runs/: every
		// repository's run records, which is what its reports summarise, under
		// the same title rule and reportsReadGrant's own bytes. The two bits
		// are on the two rows and not one: the read is objectViewer, and a
		// grant of the write role under the read condition (or the reverse) is
		// refused rather than read as either.
		//
		// objectViewer's toLicence is the renewal job's read of THREE named
		// objects (plan K4.2): the project marker, the published config and the
		// licence — the two the job finds its installation by, and the one it
		// renews — under licenceReadGrant's bytes and no other condition. The
		// bit is on the reader row and not on the writer's for the reports
		// rows' reason: the read is objectViewer and the write objectUser, and
		// a grant of one role under the other's condition is refused rather
		// than read as either.
		//
		// objectViewer's toBuildIdentity is the copied catalog definitions a
		// catalog build's pin step reads (B6): objectViewer and never
		// objectUser, under the input's own expression
		// (gcp.CatalogReadCondition) and this account's title, which is what
		// catalogReadGrant reads. A repository's build could not rewrite a
		// definition before this bit existed, and the identity cannot either.
		"roles/storage.objectViewer": toPeople | toAccounts | toCoordinator | toIntake | toReports | toLicence | toOrchestrator | toBuildIdentity,
	},
	"google_artifact_registry_repository_iam_member": {
		// toBuildIdentity carries the three roles the per-repository build
		// account held for the same reasons before B6: the writer and the tag
		// mover on the ONE shared registry, where the project's build identity
		// pushes every catalog image and moves its tags (its write already
		// reads what it pushes), and the reader a build's own pulls need —
		// fugaro-base and the ghcr remote, because a Cloud Build step pulls
		// its image as the build's account: the credential step's own image is
		// the installation's base image, the derived build's step image and
		// its FROM come through the ghcr remote, and a D2305 entry's smoke
		// stage pulls the base it pinned. That is modules/repo/build.tf's
		// build_base and build_ghcr, and B6 moved them with the account. No
		// other registry row widens for this principal: the identity reads a
		// repository's own registry through no grant, which is B6's "nothing
		// wider".
		customKey + "fugaroTagMover":    toAccounts | toBuildIdentity,
		"roles/artifactregistry.reader": toPeople | toAccounts | toBuildIdentity,
		"roles/artifactregistry.writer": toPeople | toAccounts | toBuildIdentity,
	},
	"google_secret_manager_secret_iam_member": {
		// The credentials service's own account reads Secret Manager in two
		// places, and this one row accepts both: its bundle, in credentials.tf
		// (the Q6 ruling), and — D2074 — one secretAccessor per workflow secret
		// a repository's job mounts, in modules/workflow/sa.tf, which is how
		// the service reads the container a run asks it for (B3.4). So this
		// row accepts the service account beside the jobs. The coordinator's
		// writer is here for the same shape (coordinator plan P5): the GitHub
		// App's key, on the one secret its job mounts, is the reader credential
		// P2 defines and nothing more.
		//
		// What this row decides is the principal KIND and the role, and not
		// which container: the cover reads no secret_id for this type, so a
		// hand-edited plan that pointed one of these principals at another of
		// the run's containers would be covered. The container is the module's
		// own to get right and its own plan tests to hold (the installation
		// root asserts the coordinator's one grant names the App key's secret,
		// and deploy/terraform's TestServiceWorkflowSecretGrantsMatchTheJobsMounts
		// holds D2074's set to the containers the job's own env mounts).
		//
		// LicenceRenewalRole's toLicence is the renewal job's four calls on the
		// installation's renewal container (plan K4.2), and it is the ONE grant
		// this file reads a container for: licenceSecretGrant compares the
		// binding's secret_id with the container the run's own licence spec
		// derives (Cover.LicenceRenewalSecret), because four Secret Manager
		// calls are a read of a value where the row's other grants are reads of
		// a key the job already holds. A plan that moved the grant to another
		// container of the project asks its owner.
		customKey + LicenceRenewalRole: toLicence,
		//
		// toBuildIdentity is a catalog build's own provider credential (B6):
		// Cloud Build resolves AvailableSecrets as the account the build runs
		// as, which is the project's identity now, and the containers it reads
		// are the repositories' own — one grant per repository whose entries
		// this project's catalog builds, exactly the secrets the repository's
		// own build account read (modules/repo/build.tf's build grant).
		//
		// toOrchestrator is P5.1e's row and the only one the resident
		// orchestrator has here: a cloud consumer verifies the phone's tag, the
		// tag is a symmetric HMAC, so it holds the key — one container per
		// installation, `fugaro-orch-pairing`, written and destroyed by `fugaro
		// pair` and `fugaro pair revoke` (the design's section 4.2 and its
		// section 4.7 step 2b). It is the per-secret row and never the
		// project-wide one above, and it is a reader in section 4.3's table
		// rather than the only one: a project Owner or Editor reads any
		// container's value past its own policy.
		// toVoice is P5.3.5's row and the hop's second reach: the provider-key
		// container secrets.tf creates and binds to its own account, which is
		// the one credential the forwarding half holds (voice.tf's header,
		// SECURITY.md). Like the renewal job's row above it is a binding whose
		// CONTAINER is what makes it the modules', so voiceProviderKeyGrant
		// reads the target — the literal ID internal/backend/gcp's
		// VoiceProviderKeysSecret carries and secrets.tf repeats — and refuses
		// a binding of this account on any other container of the project. The
		// bit alone would admit the hop as a reader of every container the
		// other kinds here are bound on, which is why the grant is checked
		// rather than accepted by the row.
		"roles/secretmanager.secretAccessor":     toAccounts | toCredentials | toCoordinator | toBuildIdentity | toOrchestrator | toVoice,
		"roles/secretmanager.secretVersionAdder": toPeople,
		"roles/secretmanager.viewer":             toPeople,
	},
	"google_cloud_run_v2_job_iam_member": {
		// toOrchestrator is the design's run.jobs.run / run.jobs.runWithOverrides
		// row (§4.3): the resident orchestrator starts this installation's jobs,
		// which is what a launch is. It is the one row of that table whose grant
		// the installation root writes rather than the module, because the jobs
		// are the root's project jobs.
		customKey + "fugaroJobRunner": toPeople | toAccounts | toOrchestrator,
		"roles/run.invoker":           toAccounts,
	},
	// The credentials service (C1.18): each job account may invoke it, and —
	// since BB4 — the installation's operators, whose person caller class the
	// read mint serves, and — since BB5 — the installation's own coordinator
	// account, whose coordinator class the same mint serves for the scheduled
	// sweep. So this is the one row where a person's invoker grant is the point
	// rather than a mistake, and the row naming toPeople is what makes it a
	// grant the review screen has to list: the person is on that screen by
	// name, and a member kind this row did not accept would be refused
	// outright. Every other kind is still out — the service's own ID-token
	// check (Q6) is what decides who is served, not an IAM condition, and the
	// grant stays unconditional.
	//
	// toCoordinator is BB5's deliberate widening and it is the only kind on
	// this row that is neither a job account nor a listed person: it is the
	// account THIS installation creates (google_service_account.coordinator,
	// whose email the module also hands the service as
	// FUGARO_COORDINATOR_ACCOUNT), so it is Fugaro's own and not a stranger's,
	// and the role is the front end's invoker bit on the one service the sweep
	// reads Bitbucket through. Nothing wider comes with it: no actAs, no
	// project role, and no second resource type.
	//
	// toVerifier is CSR14's widening of the same row for the same reason: the
	// installation's verifier service account (google_service_account.
	// fugaro_verifier, whose address the module hands the service as
	// FUGARO_VERIFIER_ACCOUNTS) is the verify route's one caller class, and
	// without the invoker bit the front end refuses it before the container
	// runs. It is Fugaro's own account, on the same switch as the service, and
	// it comes with nothing wider.
	//
	// toBuildIdentity is B5's (D2301), the verifier's fact one class over: the
	// project's build identity (google_service_account.build_identity, whose
	// address the module hands the service as FUGARO_BUILD_ACCOUNTS) is the
	// build route's one caller class, and the class is asked for the credential
	// a catalog build runs on, so without the invoker bit the front end refuses
	// the build before the container can decide its class. It is Fugaro's own
	// account, created by the same apply that turns the service on, and it
	// comes with nothing wider — no actAs, no project role, and no second
	// resource type: the registry roles a catalog build needs, to push and to
	// pull, are in the artifact registry row below, added there deliberately
	// by the task that granted them (B6), and they are the whole of what this
	// principal holds outside this bit.
	//
	// B4.4 puts one more account on this row: a repository's build account,
	// which B4.3's build route serves and which without the invoker bit has no
	// way to reach the credential the service mints for a build. It is the one
	// paragraph of this row that names no kind of its own, and it is admitted
	// as toAccounts, because no kind could name it: a build account's ID is
	// fugaro-b-<readable>-<8 hex>, the shape a job account's is under the same
	// regex (fugaroAccount accepts both through jobAccountID), so a plan's
	// member string does not say which of the two it is. A toBuildAccount kind
	// would have to be carried by every row the build account's own grants use
	// — the secret accessor, the registry roles, the two bucket reaches — and
	// it would then accept exactly what toAccounts accepts: a second name for
	// a membership and not a classification. The build account's cell here is
	// pinned in TestGrantRulesPinnedCells; the grant itself is inert until
	// B4.5 reads from the service.
	"google_cloud_run_v2_service_iam_member": {
		// toOrchestrator is Q13's cell, reversed on the owner's approval of
		// 2026-11-17 (F19 and O2 of docs/forge-labels; E16 of
		// docs/outer-loop-escalation): Q13's answer of 2026-10-19 kept it out
		// on the premise that the resident orchestrator never calls the
		// credentials service at all, and the service's merge mint and label
		// route are its own caller class (credsvc.OrchestratorAccounts,
		// D2049) — so the daemon's token is minted, and the front end refuses
		// it before the container runs without this binding. The grant is on
		// the ONE service the module deploys for this installation, and what
		// answers the call after the front end is the class: the account is
		// named on FUGARO_ORCHESTRATOR_ACCOUNTS and on no other list.
		"roles/run.invoker": toAccounts | toPeople | toCoordinator | toVerifier | toBuildIdentity | toOrchestrator,
	},
	"google_logging_log_view_iam_member": {
		"roles/logging.viewAccessor": toPeople,
	},
	"google_kms_crypto_key_iam_member": {
		// The consumer's signing key (mobile-identity-pairing plan task J6;
		// O17): the ONE grant of this class in the tree, and it is the mailbox
		// consumer's account on the installation's one key with the role that
		// holds cloudkms.cryptoKeyVersions.useToSign alone (MailboxSignerRole).
		// The bit is toOrchestrator and not toAccounts because the consumer IS
		// the resident orchestrator (P5.1; the design's §4.3 row the
		// reconciliation pins as "signs its replies through J6's KMS signer"),
		// and the account is a kind of its own for the reason
		// OrchestratorAccountID gives: as one of FugaroAccounts every
		// toAccounts row would have accepted it.
		//
		// The row decides the principal KIND and the role and nothing else, so
		// it is not the whole of O17's "and no other": a plan that pointed this
		// account's signing at another key of the project — or this key's
		// signing at another account — would be covered by the bit alone, which
		// is why the cell is narrowed further by mailboxSignerGrant. That reads
		// the target key, the role's project and the absence of a condition,
		// and refuses anything but this installation's one key.
		customKey + MailboxSignerRole: toOrchestrator,
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
	// LicenceRenewalSecret is the ID of the installation's renewal secret
	// container (internal/infra.LicenceRenewalSecretID): the one container
	// licenceSecretGrant accepts a binding on, and empty where the caller has
	// none to name. The caller names the container of the block the plan under
	// review is WRITTEN FROM — internal/cli records it on the run as its
	// licenceJob completes the spec, the completion happening on installRoot's
	// copy of that spec and so never on the engine's own — rather than deriving
	// it again at the review: an installation whose licence job is not deployed
	// has no block in the plan, so a binding to this account in that plan is one
	// the module never wrote, whatever container it names.
	//
	// It is carried rather than derived in this package because the derivation
	// that produces it is internal/infra's (LicenceRenewalSecretID), which
	// imports this package and so cannot be called from it. An empty one refuses
	// every binding — a caller that forgets to fill it cannot open the hole —
	// which is the fail-closed direction, since the container of a plan with no
	// licence block is one no grant of this class should name.
	LicenceRenewalSecret string
}

// CloudAssetRole is the installation's whole-project inventory read role
// (Task 4.1a of docs/managed-projects/implementation-plan.md, the owner's ruling
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

// ViewerRole is the installation's read-only job role (RS4 of
// docs/roles-scopes/implementation-plan.md, roles.md §4): the fugaroLauncher
// role minus run.executions.cancel, so its holder reads a run, its execution
// and its operations — and the project's secret list's metadata, which is the
// launcher's too — and cannot cancel one. Its list is the one grant of the
// viewer's that is a role of its own: everything else a viewer holds is a
// predefined role on a resource the modules already grant to someone.
//
// The ID is a literal in the module and here, like the Cloud Asset reader's
// and the history role's, and not a names.role_ids entry: nothing outside
// installation/iam.tf names it, the pin in this package is by the same literal
// (TestViewerRoleHasNoCancelPermission), and an installation with an empty
// viewer list must plan exactly what it planned before this role existed —
// which the tfvars plumbing of the four older roles would break, since role_ids
// is a required key of the root's tfvars (TestInstallationVarsMatchModule).
const ViewerRole = "fugaroViewer"

// CredentialsAccountID is the credentials service's fixed service account ID
// (internal/infra/tfvars.go's CredentialsAccountID, which
// var.credentials_service.account_id carries), CredentialsDatabaseRole the
// custom role credentials.tf creates for it in the run's Firebase project, and
// CredentialsVertexRole the second it creates for it in the installation's own
// project where the installation names Vertex (CS11).
//
// The account is deliberately NOT among FugaroAccounts, for the deployer
// account's reason and the ledger finding above: it holds its own roles and the
// bundle's accessor, and nothing a job holds, so every row that would otherwise
// accept it is refused (toCredentials). TestCoverNamesAreTheCodes and
// TestCredentialsAccountIsItsOwnPrincipalKind read both halves.
//
// The Vertex role is a role of its own rather than a permission of the
// database's — the two live in different projects and are read by different
// callers — and it exists only where the installation names Vertex (D3174: the
// service calls Vertex on its own installation identity, so a chain whose
// models are served there needs this account to hold endpoints.predict).
const (
	CredentialsAccountID    = "fugaro-credentials"
	CredentialsDatabaseRole = "fugaroCredentialsDatabase"
	CredentialsVertexRole   = "fugaroCredentialsVertex"
)

// CoordinatorAccountID is the coordinator's writer account (internal/infra's
// CoordinatorAccountID; coordinator plan P3, D194), created in the
// installation's project. It is NOT among FugaroAccounts, for the credentials
// account's reason: it holds roles/firebasedatabase.admin on the Firebase
// project and nothing else, so it is its own kind (toCoordinator).
const CoordinatorAccountID = "fugaro-coordinator"

// VerifierAccountID is the verifier service account (internal/infra's
// VerifierAccountID; CSR14, D1068), the one
// caller of the credentials service's verify route, created in the
// installation's project by the same module and the same apply that turns the
// service on. It is NOT among FugaroAccounts, for the credentials and
// coordinator accounts' reason: its whole reach is the invoker bit on that one
// service, so it is its own kind (toVerifier) and no toAccounts row accepts it.
//
// The ID is a LITERAL in the module (verifier.tf's account_id) and a constant
// here (internal/infra.VerifierAccountID), which is what makes the address a
// fact of the configuration rather than of the apply: the same run can both
// create the account and write its address into the owner's published policy.
const VerifierAccountID = "fugaro-verifier"

// BuildIdentityAccountID is the project's build identity (internal/infra's
// BuildIdentityAccountID; B5, D2301): the ONE account a project's catalog
// builds run as, created in the installation's project by the same apply that
// turns the credentials service on, and the ONE caller class of its build
// route (toBuildIdentity). It is NOT among FugaroAccounts, for the verifier
// account's reason: a catalog build may invoke that one service and do nothing
// else of this installation's, so a grant this table does not carry for a
// build identity is a widening nobody announced.
//
// It is a build identity and not a repository's build account
// (gcp.BuildServiceAccountID, derived per repository): the per-repository one
// dies with the repository's root while a catalog entry may name repositories
// whose root is gone (M2), which is the whole of why this account exists.
// The two are different names on purpose — `fugaro-build-identity` against the
// derived `fugaro-b-<slug>-<hash>` — so a plan can never confuse one for the
// other, and this constant is pinned to internal/infra's by
// TestCoverNamesAreTheCodes.
//
// The ID is a LITERAL in the module (build_identity.tf's account_id) and a
// constant here, which is the verifier's own arrangement: the address is a fact
// of the configuration and can be named before the apply, so a later task can
// hand it to CatalogBuildSpec and the module can render it into the service's
// account sets (FUGARO_BUILD_ACCOUNTS) as one value read twice.
const BuildIdentityAccountID = "fugaro-build-identity"

// CatalogCheckAccountID is the project's catalog check job account (D1148
// task (ii)), the one modules/installation/catalogcheck.tf creates in the
// installation's project; the module's variable validation pins the account ID
// it is given to this constant, so a plan cannot hand the account a name this
// package reads differently. It is NOT among FugaroAccounts, for the
// credentials and coordinator accounts' reason: its grants are a project-wide
// reach that no `fugaro update` confirmation announced, so it is read as a
// kind of its own (toCatalogCheck), and a row accepts it only where the row
// carries that bit — which ONE row does, the bucket rows' objectUser, and only
// through bucketCondition's catalogRecordsGrant, the check that pins the grant
// to one repository's records condition (D1148).
//
// Every other grant to it is one the tables do not cover, and what a managed
// apply then does with it is D1545's answer and not the pre-D1545 one: the
// grant table is one of that decision's three CONTENT sites
// (managed_cover.go's NotCoveredSplit names them), so the grant is a finding
// the gate records and warns about — the run prints it, files a warned record
// in the audit family and `doctor` reports that record as a finding — while
// the verdict's OK stays true and the apply goes ahead, a coding agent's
// session included, with nothing typed and no terminal. The grant of
// CatalogLeaseRole below is one of these, and deliberately not a row: a row
// would be the quieter outcome of the two, since the grant would be covered
// and announced by the gate's own line, and what the tables' silence buys is
// that the reach is never granted without the run naming it in its own words.
// Read the paragraph above at that size: the fence is a warning the owner can
// find again in `doctor`, not a question asked before the grant is made.
const CatalogCheckAccountID = "fugaro-catalog-check"

// ReportsAccountID is the project's reports job account (slice P): the account
// the installation module's reports resource creates, one per project, and the
// one the module's variable validation pins the ID to, so a plan cannot hand
// the account a name this package reads differently. It is NOT among
// FugaroAccounts, for the catalog check job's reason: it reads every
// repository's run records and it is the one writer of reports/, a reach no
// `fugaro update` confirmation announced on a job account's behalf, so it is a
// kind of its own (toReports).
//
// Its two grants are the two rows that carry that bit (the bucket's
// objectViewer and objectUser), each further narrowed by reportsReadGrant and
// reportsWriteGrant: one prefix of one of this run's buckets, under the
// condition title built from this account, byte for byte
// gcp.ReportsReadCondition or gcp.ReportsWriteCondition. Everything else the
// account is given is refused and the update that carries it asks its owner —
// which for this account includes its own two prefixes on another bucket, the
// other prefix under the other role, and any condition the two builders do not
// write.
const ReportsAccountID = "fugaro-reports"

// LicenceAccountID is the licence renewal job's account (plan K4.2 of
// docs/licence-key-v2): the account modules/installation/licence.tf creates in
// the installation's project and the job runs as, internal/infra.
// LicenceAccountID being the same name on the writer's side and the module's
// variable validation pinning the ID it is given to this literal.
//
// It is NOT among FugaroAccounts, for the reports job's reason: it reads three
// named objects of the runs bucket and writes one of them, and it holds four
// Secret Manager calls on the installation's renewal container — a reach no job
// account holds — so it is a kind of its own (toLicence) and every role it is
// given is refused unless a row carries that bit AND the narrowing check
// accepts it: the two bucket rows through licenceReadGrant and licenceWriteGrant
// (one exact object condition each, of one of this run's buckets) and the
// custom role through licenceSecretGrant (the literal role, this account, this
// run's container and no condition).
const LicenceAccountID = "fugaro-licence"

// LicenceRenewalRole is the custom role the renewal job's account holds on the
// installation's renewal secret container: the four Secret Manager calls K4.1's
// job makes — the metadata read (secrets.get), the list of the container's
// versions (versions.list), the value of the newest one (versions.access) and
// the write of the new one (versions.add) — and nothing else (LicenceRenewalPermissions).
//
// A custom role rather than a predefined set (plan amendment of 2026-11-14,
// and this repository's rule that a role is listed permission by permission
// unless a predefined one is exactly the surface needed): the closest
// predefined role, roles/secretmanager.secretVersionManager, adds
// versions.destroy and versions.enable and disable — the job could destroy the
// secret it renews with — and roles/secretmanager.secretAccessor carries
// versions.access alone, so covering the four calls would take two roles
// (secretAccessor + the version adder, whose predefined home
// roles/secretmanager.secretVersionAdder also carries versions.destroy). One
// custom role holding exactly these four is the narrowest of the three, and
// RolePermissions pins it so no later revision of the module can widen it.
//
// It is bound at the SECRET level (licence.tf's
// google_secret_manager_secret_iam_member), which is the whole of what fences
// it to one container: the same role granted on the project would be those four
// calls on every secret the installation holds.
const LicenceRenewalRole = "fugaroLicenceRenewal"

// LicenceRenewalPermissions are that role's permissions, in one var so a test
// can hold the map, the module's own list and this one to each other
// (MCPStateReadPermissions' shape). The four are K4.1's own calls and nothing
// else: no secrets.list (the job knows its container's ID — the spec carries
// it), no secrets.create and no secrets.delete (the container is Terraform's,
// and an installation that turns the job off gets it back from the same plan),
// and no versions.destroy, which would let the job destroy the secret it renews
// with.
var LicenceRenewalPermissions = []string{
	"secretmanager.secrets.get",
	"secretmanager.versions.list",
	"secretmanager.versions.access",
	"secretmanager.versions.add",
}

// CatalogLeaseRole is the custom role the catalog check job's account holds in
// the installation's Firebase project: the two Realtime Database DATA
// permissions its build leases are written with (catalogcheck.tf, D1554). It
// is a SECOND role rather than a second grant of CredentialsDatabaseRole, even
// though the two pin the same two permissions today, because the two accounts'
// reach must not be able to grow by the other's needs: a permission the
// credentials service needs next is not one the unattended nightly job gets,
// and RolePermissions pins each list on its own.
//
// It is created in the Firebase project, which is why roleKey resolves it
// through that project and not through the installation's alone (Cover.Projects
// carries a verified Firebase project for the reason).
const CatalogLeaseRole = "fugaroCatalogLease"

// MCPServerAccountID is the front door's own service account (D920), the one
// modules/installation/coordinator.tf creates in the installation's project
// and the service runs as; internal/infra.MCPServerAccountID is the same name
// on the writer's side, and TestCoverMCPNamesAreTheWritersNames pins the two
// together. It is NOT among FugaroAccounts, for the catalog check job's
// reason: its two roles are the store's (D935) and the database's read (O16b),
// so it is read as a kind of its own (toMCP) and every role it is given is
// refused unless a row carries that bit — which the objectAdmin row and the
// state-read row alone do, each narrowed further by mcpauthStoreGrant and
// mcpauthStateReadGrant, the checks that pin the two grants to D935's store and
// to the coordinator's own project.
const MCPServerAccountID = "fugaro-mcp"

// MCPStateReadRole is the custom role modules/installation/state_read.tf
// creates in the coordinator's Firebase project for the front door's own
// account, holding MCPStateReadPermissions and nothing else (O16b, D2020 (a)).
//
// It is the front door's reach into the orchestrator's node, and it is a READ:
// the state group's two tools answer get_state and get_events, apply_patch is
// served by nothing (D2021), and the state group's writes stay with `fugaro
// orchestrator patch` and the person's own credential (D1223, D1241). Realtime
// Database IAM has no path-level scope, so the role's permission list is the
// only place that direction can be decided: MCPStateReadPermissions is the
// instance's read alone, and the write (instances.update, the only one — a node
// write is an update) is not in it, so no later revision of the front door can
// reach for it. TestCoverAdmitsTheStateReadGrantOnPurpose and
// TestFrontDoorStateRoleHoldsNoDatabaseWrite (the module's own file) are the
// pins, and TestRolePinsAreTheModules holds the list to the module's.
const MCPStateReadRole = "fugaroStateRead"

// MCPStateReadPermissions are that role's permissions, the instance's read and
// no second one. A var rather than a literal inside RolePermissions so a test
// can hold the map, the module's own source and this list to each other — the
// shape WorkspaceReadPermissions has for the member's workspace read.
var MCPStateReadPermissions = []string{"firebasedatabase.instances.get"}

// MCPServerStorePrefix begins the name of D935's store: the installation
// module's own local is `fugaro-mcpauth-${var.project}`
// (modules/installation/mcpauth_store.tf), a function of the project alone and
// deliberately outside the managed `fmgd-` prefix the deployer's conditioned
// storage role is bound on. A literal here for MCPServerAccountID's reason
// (this package cannot import internal/infra, which imports it), pinned to the
// module by TestCoverMCPNamesAreTheWritersNames.
const MCPServerStorePrefix = "fugaro-mcpauth-"

// IntakeAccountID is the secrets intake's own service account (D1931, DS2.2),
// the one modules/installation/intake.tf creates in the installation's project
// and the service runs as. It is NOT among FugaroAccounts, for the front door's
// account's reason: as one of them every toAccounts row would have accepted it,
// and this is the one account that may CREATE, add a version to and DELETE the
// installation's dynamic secret containers — a write reach no job account has
// and no `fugaro update` confirmation announced on its behalf. It is a kind of
// its own (toIntake), and every grant it is given is refused unless a row
// carries that bit AND the narrowing check accepts it: the two project grants
// of the dynamic class, under the name-prefix fence (dynamicSecretsGrant), and
// the runs bucket's one admin-list object (intakeAdminListGrant).
//
// There is no writer-side constant to pin it to yet — the stage that deploys
// the intake is DS4.1's — so the module's own validation is what holds the ID
// today, and the pin lands with that stage's tfvars.
const IntakeAccountID = "fugaro-intake"

// OrchestratorAccountID is the resident orchestrator's own service account
// (mobile plan task P5.1b; docs/mobile/design/always-on-orchestrator.md §4.3),
// the one modules/installation/orchestrator.tf creates in the installation's
// project and the resident service runs as. It is NOT among FugaroAccounts, for
// the front door's and the intake's reason: as one of them every toAccounts row
// would have accepted it, and this is the account that holds
// iam.serviceAccounts.signJwt on the budget signer — "may mint is exactly may
// launch", and signJwt signs any claim set for any uid, so as a job account it
// could be handed every reach a job has on top of it.
//
// It is a kind of its own (toOrchestrator) whose rows are the design's table
// and nothing else: the launcher's project role, the fugaroJobRunner role on
// the installation's jobs, the two runs-bucket grants the launchers hold, and
// the token-minter role on the signer account. The design's eleventh
// never-rule — never a member of var.launchers — is what the bit makes
// checkable: the list is a member set, and a plan that put this account on it
// would grant it roles/firebasedatabase.viewer, roles/datastore.viewer and
// roles/serviceusage.serviceUsageConsumer, none of which any toOrchestrator row
// carries.
//
// The literal is pinned to internal/infra's OrchestratorAccountID by
// internal/infra's own test (TestCoverNamesAreTheCodes), the way
// tf.CoordinatorAccountID is pinned to infra.CoordinatorAccountID, because this
// package cannot import internal/infra.
const OrchestratorAccountID = "fugaro-orch"

// VoiceAccountID is the voice hop's own service account (P5.3.3), the one
// modules/installation/voice.tf creates in the installation's project and the
// service runs as. It is NOT among FugaroAccounts, for the front door's
// account's reason and a stronger one: as one of them every toAccounts row
// would have accepted it, and this account runs the ONE service of the tree
// that anyone on the internet may call — so a row's worth of extra reach, a
// job's datastore.user or a launcher's role, is a public endpoint one plan away
// from it. It is a kind of its own (toVoice), and the one grant it is given is
// refused unless the row carries that bit AND voiceStateReadGrant accepts the
// role, the project and the absence of a condition: the shared database read,
// which is a read of every node of the instance (state_read.tf).
//
// internal/infra's own VoiceAccountID is the same literal on the writer's side
// (tfvars.go), which is what lets this table hold the account the plan creates
// for it.
const VoiceAccountID = "fugaro-voice"

// MobileAccountID is the public mobile service's own account (mobile plan task
// P5; D2782 as amended by F31, O8), the one
// modules/installation/mobile_service.tf creates in the installation's project
// and the service runs as. It is NOT among FugaroAccounts, for the voice hop's
// reason and the same one twice over: as one of them every toAccounts row would
// have accepted it, and this account runs the tree's other service anyone on
// the internet may call — holding, unlike the hop, iam.serviceAccounts.signJwt
// on a signer account, which mints an identity for any uid. It is a kind of its
// own (toMobile), and its two grants are the row's own role and the signJwt
// mobileMinterGrant narrows: the custom database role bound to it in
// credentials.tf, and iam.serviceAccounts.signJwt on the Firebase project's
// device-only signer.
//
// The ID is a LITERAL in the module (mobile_service.tf's account_id) — the
// service's name and the account's are the same word there, so that a reader of
// a plan knows which public endpoint they are looking at — and
// deploy/terraform's TestMobileServiceNamesAreTheGoSpellings holds the module's
// two literals to this constant and to MobileServiceName.
const MobileAccountID = "fugaro-mobile"

// DeviceSignerAccountID is the devices' own signer in the FIREBASE project
// (mobile plan task P5; O1, O8), the second google_service_account
// modules/firebase/iam.tf creates beside the runs' own signer (FugaroAccounts'
// "fugaro-token-signer"). There are two because Firebase accepts a custom token
// only from an account OF the Firebase project and an installation can span
// two, and this one is the account the public mobile service holds signJwt on —
// iam.serviceAccounts.signJwt and not roles/iam.serviceAccountTokenCreator,
// whose holder could also mint access tokens as it (the role is the Firebase
// root's fugaroTokenMinter, the same role the runs' signer is minted through).
//
// What the second account buys is a key that can be disabled alone and an audit
// trail of its own, and NO narrowing of what may be signed: signJwt signs any
// claim set for any uid, IAM cannot express "device claims only", and what
// holds the service to device tokens is the route allowlist its mobile mode
// mounts and the tests over it. SECURITY.md states the residual risk.
//
// The literal is the module's own (firebase/iam.tf's account_id), pinned to
// this constant by deploy/terraform's TestDeviceSignerIsOneSpelling, which also
// reads the mobile service's grant of it back — the two sides of a
// cross-project grant whose account no variable carries.
const DeviceSignerAccountID = "fugaro-device-signer"

// MobileServiceName is the public mobile service's Cloud Run name, a literal of
// modules/installation/mobile_service.tf rather than a member of
// var.credentials_service: the service is the installation's one public
// endpoint of this kind, and a name an operator's hand-edited tfvars could move
// is a name a review screen cannot read. TestMobileServiceNamesAreTheGoSpellings
// holds the module's literal to this one.
const MobileServiceName = "fugaro-mobile"

// MailboxSignerRole is the consumer's signing role (mobile-identity-pairing
// plan task J6; O17): the custom role mailbox_key.tf creates in the
// installation's project, holding cloudkms.cryptoKeyVersions.useToSign alone
// and nothing else — no verify, no metadata read, none of the instance's
// lifecycle. It is a role of its own rather than a predefined one for the
// reason ee/keyservice/terraform/keyring's signer role gives: the nearest
// predefined role (roles/cloudkms.signerVerifier) carries the verify half and
// the reads beside the signing one, and the mailbox consumer signs and does
// nothing else with this key.
//
// The ID is a literal in the module and here, like the Cloud Asset reader's
// and the history role's, and not a names.role_ids entry: nothing outside
// installation/mailbox_key.tf names it, the pin in this package is by the same
// literal (RolePermissions, and TestRolePinsAreTheModules holds the module's
// list to it), and the ID in internal/mailbox's own constant is held to this
// one by internal/infra's TestMailboxKeyNamesAreTheGoSpellings.
const MailboxSignerRole = "fugaroMailboxSigner"

// MailboxKeyRingName and MailboxCryptoKeyName are the key ring and the key
// the installation's consumer signs with (mobile-identity-pairing plan task
// J6; O17), both literals of mailbox_key.tf. They are named here because the
// plan's signing reach is expressed over them: mailboxSignerGrant accepts the
// consumer's account on exactly one crypto key, and the key it names is
// projects/<this installation>/locations/<region>/keyRings/<ring>/cryptoKeys/
// <key>. A plan that moved the grant to another key of the project — a second
// key an operator created, or a key of another installation in the same
// project — is refused rather than covered.
//
// internal/mailbox's ConsumerKeyRingName and ConsumerCryptoKeyName are the
// same two words on the signer's side (KMSSigner holds a key version's NAME
// and nothing else), and internal/infra's TestMailboxKeyNamesAreTheGoSpellings
// holds the module's literals, the mailbox constants and these to each other:
// a key whose Go spelling drifted would be a signer pointed at a key the
// installation does not have, and the update that published its public half
// would publish the wrong one.
const (
	MailboxKeyRingName   = "fugaro-mailbox"
	MailboxCryptoKeyName = "fugaro-mailbox-consumer"
)

// IntakeSecretsRole and SecretListerRole are the dynamic class's two custom
// roles (intake.tf, D1931, DS2.2). They are two and not one because an IAM
// binding's condition cannot be lifted for a single permission of its role:
// the write half (create, delete and versions.add) is granted to the intake
// alone and only under the name-prefix fence, while secretmanager.secrets.list
// must stay unconditioned — Check 510 (2026-11-12) refused that call under the
// condition — so it is a role of its own, holding that permission and nothing
// else. The lister is the one role of the class two principals share: the
// intake and the credentials service each find containers by listing.
// RolePermissions pins both permission lists and TestRolePinsAreTheModules
// holds them to the module's, so no plan can widen either role.
const (
	IntakeSecretsRole = "fugaroIntakeSecrets"
	SecretListerRole  = "fugaroSecretLister"
)

// FugaroRoles are the custom role IDs the modules create; FugaroAccounts the
// fixed service account IDs a toAccounts row accepts. Job and build accounts
// are fugaro-<readable>-<8 hex>. The deployer account (DeployerAccountID) and
// the credentials service's (CredentialsAccountID) are deliberately NOT among
// FugaroAccounts: each holds roles of its own and nothing a job holds, so each
// is its own kind of principal (toDeployer, toCredentials). The coordinator's
// writer (CoordinatorAccountID) is not among them either, and neither is the
// project's catalog check job (CatalogCheckAccountID), nor the front door's
// own account (MCPServerAccountID), for the same reason (toCoordinator,
// toCatalogCheck, toMCP). The reports job's account (ReportsAccountID) is not
// among them either: its read of runs/ and its write of reports/ are a reach
// no job holds, so it is a kind of its own too (toReports). The project's
// build identity (BuildIdentityAccountID) is not among them for the same
// reason (toBuildIdentity): its reach is the credentials service's invoker bit
// and nothing a job of any repository holds.
var (
	FugaroRoles    = []string{"fugaroLauncher", ViewerRole, "fugaroJobRunner", "fugaroBuildSubmitter", "fugaroTagMover", "fugaroTokenMinter", "fugaroHistory", "fugaroReaper", "fugaroPreloadBuilder", CloudAssetRole, CredentialsDatabaseRole, CredentialsVertexRole, CatalogLeaseRole, WorkspaceReadRole, IntakeSecretsRole, SecretListerRole, DeployerRole, DeployerStorageRole, DeployerSecretRole, DeployerAccountRole, DeployerAPIKeyRole, DeployerRepositoryRole, DeployerRunJobRole, DeployerSchedulerRole, MCPStateReadRole, LicenceRenewalRole, MailboxSignerRole}
	FugaroAccounts = []string{"fugaro-scheduler", "fugaro-history", "fugaro-token-signer", "fugaro-reaper", "fugaro-preload"}
)

// The deployer account of D75, and the condition that fences the half of it
// that may touch an existing resource (Task 1.5 of
// docs/managed-projects/implementation-plan.md).
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
	// once under its own condition (deployerServices below). Until 0.10.0 this
	// was ONE role, fugaroDeployerScoped, under one condition; that condition
	// could not be applied (17 logical operators where IAM allows 12, a
	// resource.name.contains() which is not a supported function, and
	// resource.name unsupported for five of the nine types it named), so the
	// fence was gated off and redesigned — docs/managed-deployer-fence/spec.md.
	// The module creates them, and their bindings, only with
	// managed_deployer_scoped, which fugaro writes for an installation that
	// uses managed projects (0.10.0). A plan without them has
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
//   - "extract" tests the resource's own name, pulled out of the whole resource
//     name: Value is the extraction template the design's C6 records — literal
//     text, ONE
//     {placeholder}, and nothing else — and Starts is what the extracted text
//     must begin with. It is what the remaining five services need, because the
//     segment before the resource's own name carries a value the module does
//     not have at plan time: Secret Manager names a secret with the project
//     NUMBER rather than the id, and Artifact Registry, Cloud Run and Cloud
//     Scheduler carry the location, which a managed plan chooses and which is
//     not even constant inside one installation (the installation's own
//     scheduler runs in another region from its jobs). The template anchors at
//     the collection segment the resource's own name follows ("/secrets/",
//     "/jobs/", …) and the extracted text runs to the end of the name, which for
//     a resource of the clause's own type IS its own name — the reading C6
//     records defines the extraction by OCCURRENCE (the text after the FIRST occurrence
//     of the literal prefix, and, with a suffix too, up to the first subsequent
//     occurrence of it), which is what internal/infra/tf's cover_test.go models
//     in extractSegment.
//
// Value is never ManagedPrefix alone: a value that admitted any name merely
// containing "fmgd-" would admit a fugaro-* resource in a container whose name
// contains the prefix.
//
// The template carries exactly one placeholder, and that is not a style choice:
// the fence reads resource.name.extract as taking a single {placeholder} with
// literal text around it, and the design's C6 records that grammar as UNVERIFIED
// — no source in this tree quotes the reference's text for it. The first cut of
// this fence wrote a multi-placeholder template — extract("projects/{project}/
// secrets/{secret}") — which is not that form, and the failure it invites is the
// 0.8.6 outage again: setIamPolicy refusing the expression and stopping the
// owner-run apply that creates the whole installation. TestDeployerExtractTemplatesAreThe
// RecordedForm refuses a template that grows a second placeholder.
//
// A name that does not carry the template's literal text extracts nothing (the
// empty string), and the empty string does not start with the prefix, so a
// non-match denies under that same reading — C7, which the design records as
// UNVERIFIED and models with extractSegment rather than by a live probe
// (docs/managed-deployer-fence/spec.md). What remains UNVERIFIED,
// and what Check 68 measures, is whether these services honour resource.name at
// all (C3).
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
// docs/managed-deployer-fence/spec.md): one condition covering all nine
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
		// not carry the clause's type, so no clause admits one, and no
		// permission of this role acts on one — the old shape's
		// secretmanager.googleapis.com/SecretVersion clause was defensive, and
		// under extract it would be dead weight rather than a second fence.
		// secretmanager.versions.* is nowhere in the deployer, and the role pin
		// refuses it, so a plan that added one would find it unusable rather
		// than unfenced.
		//
		// The template is the collection segment alone, not
		// "projects/{project}/secrets/{secret}": the form C6 records has
		// extract take ONE placeholder, and the project in a secret's name is
		// its NUMBER, which the module does not know at plan time.
		{Role: DeployerSecretRole, Clauses: []DeployerClause{
			{Type: "secretmanager.googleapis.com/Secret", Op: "extract", Value: "/secrets/{secret}", Starts: ManagedPrefix},
		}},
		{Role: DeployerAccountRole, Clauses: []DeployerClause{
			{Type: "iam.googleapis.com/ServiceAccount", Op: "startsWith", Value: saPrefix},
		}},
		{Role: DeployerAPIKeyRole, Clauses: []DeployerClause{
			{Type: "apikeys.googleapis.com/Key", Op: "extract", Value: "/keys/{key}", Starts: ManagedPrefix},
		}},
		{Role: DeployerRepositoryRole, Clauses: []DeployerClause{
			{Type: "artifactregistry.googleapis.com/Repository", Op: "extract", Value: "/repositories/{repository}", Starts: ManagedPrefix},
		}},
		{Role: DeployerRunJobRole, Clauses: []DeployerClause{
			{Type: "run.googleapis.com/Job", Op: "extract", Value: "/jobs/{job}", Starts: ManagedPrefix},
		}},
		{Role: DeployerSchedulerRole, Clauses: []DeployerClause{
			{Type: "cloudscheduler.googleapis.com/Job", Op: "extract", Value: "/jobs/{job}", Starts: ManagedPrefix},
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

// installationProject is the run's installation project (Projects[0]): the one
// project the installation module — and so deployer.tf and credentials.tf — is
// applied to, whatever projects the roles it binds live in. "" when the cover
// has none.
func (c Cover) installationProject() string {
	if len(c.Projects) == 0 || c.Projects[0] == "" {
		return ""
	}
	return c.Projects[0]
}

// deployerProject is the project whose deployer account this run's plans may
// name: the installation's, since deployer.tf is that module's. "" when the
// cover has none.
func (c Cover) deployerProject() string { return c.installationProject() }

// isCredentialsMember is whether m is exactly the credentials service's
// account as an IAM member, in the installation's project: the account
// credentials.tf creates (var.project), whatever project the roles it binds
// live in — its database role is the Firebase project's, and that does not
// move the account.
func (c Cover) isCredentialsMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+CredentialsAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isCoordinatorMember is whether m is exactly the coordinator's writer
// account as an IAM member, in the installation's project: its one grant is
// on the Firebase project, which does not move the account.
func (c Cover) isCoordinatorMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+CoordinatorAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isVerifierMember is whether m is exactly the verifier account as an IAM
// member, in the installation's project: the account verifier.tf creates
// (var.project). It is a class of its own and not a spelling of the
// deployer's: the operators hold the token-creator grant on it, and that
// grant is the one place a plan is refused unless the table knows the
// principal (deployerTokenCreator).
func (c Cover) isVerifierMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+VerifierAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isCatalogCheckMember is whether m is exactly the project's catalog check job
// account as an IAM member, in the installation's project: the account
// catalogcheck.tf creates (var.project).
func (c Cover) isCatalogCheckMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+CatalogCheckAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isReportsMember is whether m is exactly the project's reports job account as
// an IAM member, in the installation's project: the account the installation
// module's reports resource creates (var.project, ReportsAccountID).
func (c Cover) isReportsMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+ReportsAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isLicenceMember is whether m is exactly the licence renewal job's account as
// an IAM member, in the installation's project: the account the installation
// module's licence resource creates (var.project, LicenceAccountID).
func (c Cover) isLicenceMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+LicenceAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isBuildIdentityMember is whether m is exactly the project's build identity as
// an IAM member, in the installation's project: the account build_identity.tf
// creates (var.project, BuildIdentityAccountID).
func (c Cover) isBuildIdentityMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+BuildIdentityAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isMCPMember is whether m is exactly the front door's own account as an IAM
// member, in the installation's project: the account coordinator.tf creates
// and the service runs as (MCPServerAccountID).
func (c Cover) isMCPMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+MCPServerAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isIntakeMember is whether m is the secrets intake's own account as an IAM
// member, in the installation's project: the account intake.tf creates and the
// service runs as (IntakeAccountID).
func (c Cover) isIntakeMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+IntakeAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isOrchestratorMember is whether m is the resident orchestrator's own account
// as an IAM member, in the installation's project: the account
// orchestrator.tf creates and the resident service runs as
// (OrchestratorAccountID).
//
// It is the installation's project and not the Firebase one, although one of
// the account's rows is bound on the Firebase root's signer: the account itself
// is this module's (var.project), and the grant's own project is what the
// binding names, not what the principal is.
func (c Cover) isOrchestratorMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+OrchestratorAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isVoiceMember is whether m is the voice hop's own account as an IAM member,
// in the installation's project: the account voice.tf creates and the service
// runs as (VoiceAccountID). It classifies the account's two grants — the
// project read role voiceStateReadGrant narrows and, since P5.3.5, the
// provider-key binding voiceProviderKeyGrant narrows — and nothing else: the
// hop's remaining reach is its PUBLIC invoker binding, whose member is
// allUsers, which no kind here carries (toVoice).
func (c Cover) isVoiceMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+VoiceAccountID+"@"+p+".iam.gserviceaccount.com"
}

// isMobileMember is whether m is the public mobile service's own account as an
// IAM member, in the installation's project: the account mobile_service.tf
// creates and the service runs as (MobileAccountID). Like isOrchestratorMember
// it is the INSTALLATION's project even though one of the account's rows is
// bound on the Firebase root's device signer: the account itself is this
// module's (var.project), and the project a binding names is not the principal.
//
// It classifies the account's two grants, and the two are held to different
// rules on purpose. The database role is the row's own, checked here exactly as
// far as the credentials account's own binding is (the row's role and kind,
// with the source pinned by deploy/terraform's
// TestCredentialsDatabaseRoleIsTheTwoDataPermissions, which holds both
// bindings to the Firebase project). The signJwt is not: it is
// mobileMinterGrant's, because what makes that grant the devices' is the
// account it is on. The service's remaining reach is its PUBLIC invoker
// binding, whose member is allUsers, which no kind here carries (toMobile).
func (c Cover) isMobileMember(m string) bool {
	p := c.installationProject()
	return p != "" && m == "serviceAccount:"+MobileAccountID+"@"+p+".iam.gserviceaccount.com"
}

// voiceStateReadGrant is mcpauthStateReadGrant's rule for the hop's own
// account (P5.3.3): the same role, on the project the role was declared in,
// under no condition, and the same refusal of every other role.
//
// It is a function of its own rather than a call with the front door's message
// because the account is a different one and the sentence has to name it: a
// reader of the refusal is deciding whether the update may proceed, and "the
// front door's account" would send them to the wrong file. The reasoning is
// mcpauthStateReadGrant's whole: RTDB IAM has no path-level scope, so this is a
// read of the WHOLE database instance — every run's state, the policy, the kill
// switches, creds/ and the mailbox — and what narrows each reader to its own
// nodes is its own code and not the grant (internal/voicesvc's paths for the
// hop). That is why the grant is checked rather than accepted by the row.
func (c Cover) voiceStateReadGrant(rc ResourceChange) string {
	ch := rc.Change
	project := str(ch.After["project"])
	if !slices.Contains(c.Projects, project) {
		return "grants the voice hop's account its database read in a project that is not this run's, where the role the module declares cannot be read"
	}
	want := "projects/" + project + "/roles/" + MCPStateReadRole
	if role := str(ch.After["role"]); role != want {
		return "grants the voice hop's account " + role + " on a project, and its one role is " + MCPStateReadRole + " on the project the module declares it in"
	}
	conds, why := grantConditions(ch, "a project grant")
	switch {
	case why != "":
		return why
	case len(conds) != 0:
		return "grants the voice hop's account its database read under a condition, which the module never sets: RTDB IAM cannot express a path prefix, so a condition here would fence nothing"
	}
	return ""
}

// voiceProviderKeyGrant is licenceSecretGrant's rule for the hop's
// provider-key binding (P5.3.5): the role is the row's own
// (roles/secretmanager.secretAccessor on google_secret_manager_secret_iam_member,
// checked before this is called) and what is left is WHICH container it is on —
// the module-owned literal secrets.tf writes and internal/backend/gcp's
// VoiceProviderKeysSecret repeats, holding the document of register names to
// provider keys the forwarding half reads as this account.
//
// Why the container is checked rather than the row trusted: the bit admits
// this account to the per-secret accessor cell, and a cell is every container
// the other kinds of that row are bound on — the installation's credentials
// bundle, a repository's own secrets, the front door's App key, the renewal
// container. A plan that pointed the hop's binding at one of those would hand
// a service anyone on the internet may call the credentials of another part of
// the installation, and the target is the only thing left that says otherwise.
//
// The container may be unknown, and then the grant is refused: the ID is a
// literal in the module (Terraform cannot compute a gcp.ProjectSecretID — the
// suffix is a hash), never a computed attribute of a resource in this plan, so
// "unknown" means the plan did not take it from the module's own text. The
// comparison is against the Go constant and not against a value the caller
// carries (licenceSecretGrant's Cover.LicenceRenewalSecret), because this
// container's name is the same literal for every installation: secrets.tf
// writes it, deploy/terraform's TestVoiceProviderKeysContainerIsTheGoSpelling
// holds the two spellings together, and a cover with a different one to name
// would be a second place the name could drift.
func (c Cover) voiceProviderKeyGrant(rc ResourceChange) string {
	if unknown(rc.Change, "secret_id") {
		return "grants the voice hop's account a secret whose secret_id is not known: it is the provider-key container, which the module writes as the literal " + safetext.Field(gcp.VoiceProviderKeysSecret) + " and never as a computed attribute"
	}
	if id := str(rc.Change.After["secret_id"]); id != gcp.VoiceProviderKeysSecret {
		return "grants the voice hop's account roles/secretmanager.secretAccessor on the secret " + safetext.Field(id) + ", which is not its provider-key container (" + safetext.Field(gcp.VoiceProviderKeysSecret) + "): those keys are the one credential the forwarding half holds, and every other container of this project belongs to another part of the installation (P5.3.5)"
	}
	conds, why := grantConditions(rc.Change, "a secret grant")
	switch {
	case why != "":
		return why
	case len(conds) != 0:
		return "grants the voice hop's account its provider keys under a condition: the container is the fence, and the module never adds one (secrets.tf)"
	}
	return ""
}

// mcpauthStoreBucket is the name of D935's store in this cover's installation
// project — MCPServerStorePrefix + the project — and "" when the cover has
// none. It is the same name for every installation of that project, which is
// what lets a plan review recognise the one bucket the front door's account
// may reach without being told its name.
func (c Cover) mcpauthStoreBucket() string {
	if p := c.installationProject(); p != "" {
		return MCPServerStorePrefix + p
	}
	return ""
}

// mcpauthStoreGrant says why a bucket grant to the front door's own account is
// not D935's store grant, "" when it is: the account's one role is
// roles/storage.objectAdmin, on the store's bucket, under no condition — the
// single binding modules/installation/mcpauth_store.tf makes. It is what
// narrows the objectAdmin row's toMCP bit, the way catalogRecordsGrant narrows
// the catalog check job's objectUser one, so the bit cannot be moved to
// another role, another bucket or a condition: the account serves the front
// door's records and holds nothing else, and a plan that gave it the runs
// bucket's objects (or the store to anyone else) would be a widening no
// `fugaro update` confirmation announced.
func (c Cover) mcpauthStoreGrant(rc ResourceChange) string {
	ch := rc.Change
	store := c.mcpauthStoreBucket()
	if role := str(ch.After["role"]); role != "roles/storage.objectAdmin" {
		return "grants the front door's account " + role + " on a bucket, and its one role is roles/storage.objectAdmin on the store D935 gives it"
	}
	if b := str(ch.After["bucket"]); store == "" || b != store {
		return "grants the front door's account a role on bucket " + b + ", which is not the store D935 gives it"
	}
	if m := str(ch.After["member"]); !c.isMCPMember(m) {
		return "grants D935's store role to " + m + ", which is not the front door's own account"
	}
	conds, why := grantConditions(ch, "a bucket grant")
	switch {
	case why != "":
		return why
	case len(conds) != 0:
		return "grants the front door's account its store role under a condition, which the module never sets"
	}
	return ""
}

// mcpauthStateReadGrant says why a PROJECT grant to the front door's own
// account is not O16b's read of the orchestrator's node, "" when it is: the
// account's second role, on the project the role was declared in, under no
// condition — the single binding modules/installation/state_read.tf makes.
//
// What it holds is the instance's READ and nothing else (RolePermissions pins
// the permission; the module's own file is the other half), and Realtime
// Database IAM has no path-level scope, so a project binding of this role is a
// read of the WHOLE database of that project — every run's state, the policy,
// the kill switches and the credentials' nodes included. That reach is what
// D2020 (a) answered for, and it is why the grant is checked rather than
// accepted by the row: a plan that moved the read to another role, put another
// role on this account, declared the role in a project the grant is not in, or
// carried a condition (which RTDB cannot express as a path prefix, so one would
// look like a fence and not be one) is refused here and the update that carries
// it asks its owner.
//
// The member is read before this runs: the caller checks it with isMCPMember,
// so what is left to check is the role, the project and the condition.
func (c Cover) mcpauthStateReadGrant(rc ResourceChange) string {
	ch := rc.Change
	project := str(ch.After["project"])
	if !slices.Contains(c.Projects, project) {
		return "grants the front door's account its database read in a project that is not this run's, where the role the module declares cannot be read"
	}
	want := "projects/" + project + "/roles/" + MCPStateReadRole
	if role := str(ch.After["role"]); role != want {
		return "grants the front door's account " + role + " on a project, and its second role is " + MCPStateReadRole + " on the project the module declares it in"
	}
	conds, why := grantConditions(ch, "a project grant")
	switch {
	case why != "":
		return why
	case len(conds) != 0:
		return "grants the front door's account its database read under a condition, which the module never sets: RTDB IAM cannot express a path prefix, so a condition here would fence nothing"
	}
	return ""
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
	// The viewer's read-only job role (RS4, roles.md §4): the launcher's list
	// minus run.executions.cancel and nothing else, so every read a launcher
	// makes a viewer makes — the run's own state and operations, and the
	// project's secret list's metadata, which is what `fugaro secrets ls`
	// needs — and the one thing the list withholds is the cancel.
	// TestViewerRoleHasNoCancelPermission reads this against the launcher's.
	ViewerRole: {"run.jobs.get", "run.jobs.list", "run.executions.get", "run.executions.list", "run.operations.get",
		"secretmanager.secrets.list", "secretmanager.versions.list"},
	"fugaroJobRunner":      {"run.jobs.run", "run.jobs.runWithOverrides"},
	"fugaroBuildSubmitter": {"cloudbuild.builds.create", "cloudbuild.builds.get"},
	"fugaroTagMover":       {"artifactregistry.tags.delete"},
	"fugaroTokenMinter":    {"iam.serviceAccounts.signJwt"},
	"fugaroHistory":        {"run.executions.list", "run.executions.get", "run.jobs.get", "run.jobs.list", "run.operations.get"},
	// The reaper's five, and no others (design compute-backend §3.7): it is
	// never roles/batch.jobsEditor and never roles/compute.instanceAdmin.v1,
	// both of which carry the create permissions a reaper must not have. It
	// named six until 0.10.0, the sixth being batch.jobs.cancel -- a permission
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
	// The credentials service's database role (0.10.1, ledger #903): the
	// service reads the kill switches, the project policy, a run's outcome node
	// and the credential state another revision wrote, and writes the creds/
	// nodes it owns. Realtime Database IAM has no path-level scope, so the
	// narrowest role that can do that is the instance's two DATA permissions —
	// get (the only read) and update (the only write). Both predefined roles
	// are wrong in opposite directions: viewer is get+list and cannot write at
	// all, admin adds the instance LIFECYCLE (create, delete, disable,
	// reenable, undelete) plus list, so the service could delete the
	// installation's database. credentials.tf lists both permissions with their
	// reasons; TestCredentialsDatabaseRoleIsTheTwoDataPermissions is the pin.
	CredentialsDatabaseRole: {"firebasedatabase.instances.get", "firebasedatabase.instances.update"},
	// The service's Vertex role (CS11, D3174): the one permission a :rawPredict
	// on one of the installation project's Claude endpoints needs, and nothing
	// else. roles/aiplatform.user — the release-1 grant a job holds under
	// budget.mode: observe — carries endpoints.predict plus the platform's
	// reads, model deployments, tuning and pipelines, which is exactly the
	// surface a chain-serving credential holder must not have.
	// deploy/terraform's TestTheServiceVertexRoleIsItsOwn is the pin that reads
	// this entry against the module.
	CredentialsVertexRole: {"aiplatform.endpoints.predict"},
	// The catalog check job's database role (D1554): the two permissions
	// catalogcheck.tf lists, and the same two the credentials role above pins.
	// They are a second entry and not a shared one on purpose — the two
	// accounts hold the same surface today, and neither may grow it because the
	// other needed a permission. The job reads the leases it left behind, the
	// day's counters and the database's mark, and writes the lease it takes and
	// settles (internal/budget/lease_build.go); it never touches the instance's
	// lifecycle, which is what roles/firebasedatabase.admin would hand it.
	// TestCatalogLeaseRoleIsTheTwoDataPermissions is the pin.
	CatalogLeaseRole: {"firebasedatabase.instances.get", "firebasedatabase.instances.update"},
	// The dynamic class's write half (intake.tf, D1931, DS2.2): what the
	// intake may do to a container, and no read at all — it creates one, adds a
	// version to it and deletes it, and it must not be able to read a value
	// back (R3), which is why neither secretmanager.secrets.get nor
	// secretmanager.versions.access is here. All three are conditioned by name
	// in the binding below (dynamicSecretsGrant), so the role's own reach is
	// the grammar's containers and no other resource of the project.
	IntakeSecretsRole: {"secretmanager.secrets.create", "secretmanager.secrets.delete", "secretmanager.versions.add"},
	// ... and the list half, which the intake and the credentials service
	// share: the names, labels and annotations of the project's containers, and
	// never a version and never a payload.
	SecretListerRole: {"secretmanager.secrets.list"},
	// The workspace's read of the MEMBER's database (docs/workspaces/
	// spec.md §5, W7; workspace_access.go): the ONE permission a member's own
	// Fugaro installation binds, so the workspace's credentials service can
	// read the member's ledger for the reconciliation alarm. It is the read
	// half of the credentials role above and deliberately not that role:
	// firebasedatabase.instances.update is not bound at all, so no later
	// revision of the service can write a member's database.
	WorkspaceReadRole: {"firebasedatabase.instances.get"},
	// The front door's read of the installation's database (O16b, D2020 (a),
	// state_read.tf): the instance's read and no second permission — the write
	// is instances.update, the only one RTDB has, and it is deliberately not
	// here, so the state group's writes stay with `fugaro orchestrator patch`
	// and the person's own credential (D1223, D1241) and a later revision of
	// the front door has none to reach for. The module's own file names the
	// same single permission and
	// TestFrontDoorStateRoleHoldsNoDatabaseWrite refuses a second one there;
	// TestCoverAdmitsTheStateReadGrantOnPurpose holds this pin to
	// MCPStateReadPermissions, and TestRolePinsAreTheModules holds both to the
	// module.
	MCPStateReadRole: {"firebasedatabase.instances.get"},
	// The consumer's signing role (mailbox_key.tf, plan task J6; O17): the ONE
	// permission Cloud KMS offers for making a signature with an asymmetric key
	// version — cloudkms.cryptoKeyVersions.useToSign — and nothing beside it.
	// The verify half (useToVerify) is deliberately not here: the phone holds
	// the public half and verifies for itself, so the installation's own
	// account has nothing to verify with, and a role that carried it would be a
	// permission granted for a call nobody makes. The metadata reads
	// (cryptoKeys.get, cryptoKeyVersions.get/list) are not here either: the
	// update that publishes the public half reads the key's own attributes with
	// the owner's credentials (internal/infra), not with this account.
	// TestRolePinsAreTheModules holds this list to the module's own, so a later
	// revision that widened the signer's reach would widen both in one change.
	MailboxSignerRole: {"cloudkms.cryptoKeyVersions.useToSign"},
	// The renewal job's four calls on the installation's renewal container
	// (licence.tf, plan K4.2, the plan amendment of 2026-11-14). A custom role
	// rather than a predefined one, and the comment on LicenceRenewalRole says
	// which and why: the nearest predefined roles add a destroy of the secret
	// this job renews with, and no single predefined role carries exactly these
	// four. LicenceRenewalPermissions is the list the module's own file names
	// and TestRolePinsAreTheModules holds the two together, so a later revision
	// that widened the job's Secret Manager reach would have to widen this pin
	// in the same change.
	LicenceRenewalRole: LicenceRenewalPermissions,
	// The deployer's eight roles (D75, Task 1.5, redesigned for 0.10.0). They
	// are not installation roles: nothing a run does needs them, and their
	// bound is the split between them — creation, reads and
	// serviceusage.services.enable in fugaroDeployer, and every update, delete,
	// run, pause, *.setIamPolicy and actAs split one role per service across
	// the seven below, each bound exactly once and only under its own
	// DeployerConditionExpression (when the installation uses managed
	// projects, which is what managed_deployer_scoped records; off by
	// default). The seven lists
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

// knownCommands are the container command and args the modules set: none for a
// workflow's job and for the credentials service (whose image's own entrypoint
// is the service, credentials.tf), the history sweep, the coordinator's
// aggregation sweep, the image check, the project catalog's check
// (catalogcheck.tf), the reports job (reports.tf) and the licence renewal job
// (licence.tf).
var knownCommands = [][2][]string{
	{nil, nil},
	{{"/usr/local/bin/fugaro"}, {"budget", "history", "--sweep"}},
	{{"/usr/local/bin/fugaro"}, {"coordinator", "sweep"}},
	{{"fugaro"}, {"image", "check", "--job"}},
	{{"fugaro"}, {"image", "check", "--job", "--catalog"}},
	{{"fugaro"}, {"runs", "reports", "--job"}},
	{{"/usr/local/bin/fugaro"}, {"licence", "renew", "--job"}},
}

// credentialsServiceEnv are the only environment variables the credentials
// service's container sets (installation/credentials.tf): the project it
// serves, the project it reads the bundle in, the region, the database and
// Firebase project it reads runs from, its own URL (the audience its callers'
// ID tokens are minted for), the installation's per-repository jobs as a
// JSON array of {provider, repo, workflow} triples, and — only on an
// installation that names a verifier — the addresses that may ask the verify
// route, — only on one that names an operator — the addresses the read
// mint admits (BB4), — only on one whose coordinator is on — the
// coordinator's own account, which is the read mint's second class (BB5), and
// — only on one that deploys review-only jobs — those jobs' {profile,
// provider, account} triples, which are the class the service admits a review
// caller as (C3.9a), — only on one whose roster names a launcher — the
// addresses the launcher class admits (D1938, F25), which is the class the
// grant route and the intake answer to, — only on an installation that
// runs the service at all — the address of the project's build identity (B5,
// D2301), which is the class the build route admits and the only list that
// class is on, and — only on one that runs a resident orchestrator — the
// daemon's own account (F19, O2), which is the class the label route and the
// gated merge mint are served to, and the identity half's three (LM22): the
// account the runs' identities are signed as (D3121), the roster's service
// accounts (D3123) and the implicit admins the CLI's own gate reads (F29, R16),
// which credentials.tf renders since LM12. The identity three went on this list
// AHEAD of the module, which is the order F28 states, and left
// CredentialsServiceEnvAwaitingTheModule in the change that rendered them. No
// secret ID and no secret value,
// because the loader
// derives the container from the project name and reads it as the service's own
// account, and both job sets are derived from their triples rather than
// carried. The case below is an allowlist of these, so an environment that
// hands this process anything else is not covered;
// TestAllowedTypesCoverTheModules keeps the two lists in step.
//
// This list is what the cover ADMITS, which is not always what a module renders
// yet: CredentialsServiceEnvAwaitingTheModule names the entries that are on it
// ahead of the module.
var credentialsServiceEnv = []string{
	"FUGARO_PROJECT", "FUGARO_GCP_PROJECT", "FUGARO_REGION",
	"FUGARO_RTDB_URL", "FUGARO_FIREBASE_PROJECT",
	"FUGARO_CREDENTIALS_SERVICE_URL", "FUGARO_JOB_ACCOUNTS",
	"FUGARO_VERIFIER_ACCOUNTS", "FUGARO_PERSON_ACCOUNTS",
	"FUGARO_COORDINATOR_ACCOUNT", "FUGARO_REVIEW_JOB_ACCOUNTS",
	"FUGARO_LAUNCHER_ACCOUNTS", "FUGARO_SHARED_JOB_ACCOUNTS",
	"FUGARO_BUILD_ACCOUNTS",
	// ... and — only on an installation that runs a resident orchestrator —
	// the account that class admits (F19, O2): the daemon's own identity, as
	// the JSON array credentials.tf renders from the account its own module
	// creates. It is the one name this deployment carries that is not read by
	// the run path at all, and it reaches no model credential: what the
	// process does with it is match a caller's email claim.
	"FUGARO_ORCHESTRATOR_ACCOUNTS",
	// ... and the three names of the identity half (LM22): the account a run's
	// identity is signed as (D3121), the roster's serviceAccount: entries
	// (D3123) and the implicit admins the CLI's own gate reads out of the
	// machine's configuration (F29, R16). None of them is a credential — the
	// signer is an address, the other two are lists of addresses, and the
	// service holds the minter row on the signer rather than a key of it — and
	// all three are read by `credentials serve` into values nothing uses yet
	// (internal/cli's credentialsServiceConfig). They were the cover's before
	// the module rendered them and credentials.tf sets each one since LM12,
	// only where it has something to carry.
	"FUGARO_SIGNER_ACCOUNT",
	"FUGARO_ROSTER_SERVICE_ACCOUNTS",
	"FUGARO_IMPLICIT_ADMINS",
}

// intakeServiceArgs and intakeServiceEnv are the argv and the environment
// intake.tf writes for the secrets intake's container, and nothing else is
// admitted on it: the released image's `intake serve` subcommand (the image is
// the credentials service's own, whose default command serves the other
// service), and the five names the process's configuration is made of, which
// internal/cli/intake_serve.go reads: the project, the service's own URL (the
// audience a caller's ID token is minted for), the runs bucket holding the
// derived admin list, the launcher class the process admits, and the region a
// container it creates is replicated in, which the intake is told from its
// deployment because it runs in the cloud with no project config and no
// installation record it may read (D3082).
var (
	intakeServiceArgs = []string{"intake", "serve"}
	intakeServiceEnv  = []string{"FUGARO_GCP_PROJECT", "FUGARO_REGION", "FUGARO_INTAKE_URL", "FUGARO_RUNS_BUCKET", "FUGARO_LAUNCHER_ACCOUNTS"}
)

// voiceServiceArgs and voiceServiceEnv are the argv and the environment
// voice.tf writes for the voice hop's container (P5.3.3, extended by P5.3.5),
// and nothing else is admitted on it: the released image's `voice serve`
// subcommand — the same image and the same pin the credentials service and the
// intake run, whose default command serves the credentials service — and the
// THREE names internal/cli/voice_serve.go reads: the Firebase project whose ID
// tokens the process verifies, the Realtime Database it reads the device node
// and the policy from, and the installation's GCP project, which is where the
// provider-key container the forwarding half reads lives.
//
// Three names and not the credentials service's twelve, because the hop holds
// no bundle, no job table and no address of its own: its callers are phones
// whose tokens are minted for the Firebase project, so there is no service URL
// in its environment (and no second apply to fill one). A name added here is a
// value the process would read out of the module rather than the deployment,
// which is the one path a credential could reach a container without a Secret
// Manager read — the reason the credentials service's own allow-list is
// enumerated, and the reason the provider-key container's ID is NOT one of
// these three: FUGARO_GCP_PROJECT plus the constant in
// internal/backend/gcp names that container, so no secret ID is a value of this
// environment at all.
var (
	voiceServiceArgs = []string{"voice", "serve"}
	voiceServiceEnv  = []string{"FUGARO_FIREBASE_PROJECT", "FUGARO_RTDB_URL", "FUGARO_GCP_PROJECT"}
)

// mobileServiceArgs and mobileServiceEnv are the argv and the environment
// mobile_service.tf writes for the public mobile service's container (mobile
// plan task P5; D2782 as amended by F31): the credentials service's OWN image,
// started in its mobile mode, which mounts the allowlisted mobile routes and
// none of the existing ones (I1 owns that mode and its cross-class test).
//
// The argv is THREE words and not the hop's two, because this service is the
// same COMMAND the private service's default runs, told apart by a mode: the
// image's entrypoint is the binary, `credentials serve` is what the private
// service runs by default, and the flag is what a reader of a plan needs to
// see to know that this URL does not serve the credentials routes. The pair is
// P5's inference of I1's mode — the mode's own spelling is that task's
// (internal/cli/credentials_serve.go) — and the two move together: this
// constant and the module's args are one rule, and a plan naming anything else
// is refused here rather than applied as a public service running whatever
// code the module did not write.
//
// The environment is the SAME SEVEN names the private service carries, and
// deliberately a second literal list rather than a slice of
// credentialsServiceEnv: the two services run the same binary and the seven
// are what it refuses to bind a port without, whatever mode it is in, but a
// name added to that list for the private service must not silently become a
// name a PUBLIC container may carry — the widening would be one edit away and
// invisible in the diff that made it. Values are literals and no value_source,
// for the credentials service's reason: an environment is the one path a
// credential could reach a container without a Secret Manager read, and this
// container is reachable by anyone.
var (
	mobileServiceArgs = []string{"credentials", "serve", "--mobile"}
	mobileServiceEnv  = []string{
		"FUGARO_PROJECT", "FUGARO_GCP_PROJECT", "FUGARO_REGION",
		"FUGARO_RTDB_URL", "FUGARO_FIREBASE_PROJECT",
		"FUGARO_CREDENTIALS_SERVICE_URL", "FUGARO_JOB_ACCOUNTS",
	}
)

// MobileServiceArgs is mobileServiceArgs for a caller outside this package, and
// the coupling it exists for is one this package cannot test: the argv the
// module writes has to be a command the CLI CAN RUN. The `--mobile` mode is
// I1's (internal/cli/credentials_serve.go defines none until the mobile routes
// land), and cobra answers a flag it does not know with `unknown flag: --mobile`
// — so a revision started with these args never becomes ready and the apply
// that carried it fails, on an installation whose runs are doing nothing wrong.
// Until that flag exists the module starts nothing with them (its own switch is
// OFF, var.enable_mobile_service); internal/cli's
// TestMobileServiceIsOffUntilThisBinaryRunsIt runs this slice through the real
// command tree and fails while the switch and the binary disagree.
//
// A copy rather than the slice itself: the caller is a test, and the slice it
// would otherwise hold is what every plan of this tree is checked against.
func MobileServiceArgs() []string { return slices.Clone(mobileServiceArgs) }

// orchestratorServiceResource, orchestratorServiceArgs, orchestratorServiceEnv
// and orchestratorTickPath are the same quartet for the resident orchestrator's
// own service (mobile plan tasks P5.1c and P5.1d;
// modules/installation/orchestrator.tf): the resource name the service case
// dispatches on, the argv of the released image's `orchestrator serve`
// subcommand, the six names internal/cli/orchestrator_serve.go reads and nothing
// else — the project that names this installation's node, the Firebase project
// it belongs to and the database URL it is read from, plus the launch's own
// three (the GCP project the jobs are in, the region they were deployed to and
// the runs bucket's URL, which P5.1d's launcher builds a launch from) — and the
// one route that process answers, which is the only path a tick of this
// installation may be pointed at.
//
// A scheduling job of this service is the tree's ONE tick whose target is a
// run.app URL rather than the run API's job path, and whose token is therefore
// an OIDC ID token rather than an OAuth access token: orchestratorTickChange is
// what those two facts are held by.
const (
	orchestratorServiceResource = "orchestrator"
	orchestratorServiceName     = "fugaro-orchestrator"
	orchestratorTickPath        = "/tick"
)

var (
	orchestratorServiceArgs = []string{"orchestrator", "serve"}
	orchestratorServiceEnv  = []string{"FUGARO_PROJECT", "FUGARO_FIREBASE_PROJECT", "FUGARO_RTDB_URL", "FUGARO_GCP_PROJECT", "FUGARO_REGION", "FUGARO_BUCKET"}
)

// CredentialsServiceEnvAwaitingTheModule are the names credentialsServiceEnv
// admits that the installation module does not render yet: the cover is what
// one `fugaro update` confirmation covers, so a name is added to the allow-list
// FIRST and the module that renders it second (the module and this table ship
// in the same binary — deploy/terraform is embedded — so the order is the
// cover's and not a release's).
//
// It is EMPTY now, and FUGARO_SHARED_JOB_ACCOUNTS is the name that left it
// last: the service reads it as an optional value (internal/cli's
// envSharedJobAccounts) and the module that renders it from the shared jobs an
// installation deploys is installation/credentials.tf, which sets it from
// var.credentials_service.shared_job_accounts on the same switch every other
// entry of that container is on (B5.1). It is kept as a function with a body
// that can hold a name rather than deleted, because the next name the service
// reads ahead of the module that renders it belongs here. Before it,
// FUGARO_LAUNCHER_ACCOUNTS (the launcher class's list, D1938, F25) left this
// list in the change that made installation/credentials.tf render it (DS2.2),
// and FUGARO_REVIEW_JOB_ACCOUNTS left it the same way before that.
//
// The identity half's three (LM22, D3121, D3123, R16) went on it at the same
// time: FUGARO_SIGNER_ACCOUNT, FUGARO_ROSTER_SERVICE_ACCOUNTS and
// FUGARO_IMPLICIT_ADMINS, which `credentials serve` reads into values nothing
// uses yet, and which left this list in the change that made
// installation/credentials.tf render them (LM12) — the same way
// FUGARO_LAUNCHER_ACCOUNTS left it before them. They are not classes the
// service admits a caller by — nothing is served from them until the issuer is
// wired in (I1) — and the service must keep starting wherever they are absent,
// which is why each is read as an OPTIONAL value on internal/cli's side and the
// module renders each one only where it has something to carry.
//
// The function is the tripwire and not a carve-out: two tests hold it —
// the golden test below fails on a name that is both here and on the plan, and
// internal/cli's TestCredentialsServiceEnvIsTheDeploymentsOwn fails on one that
// is here and in the module's own source — so a name added ahead of its module
// cannot be left behind once the module renders it.
func CredentialsServiceEnvAwaitingTheModule() []string {
	return nil
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
	all, _, _ := c.notCoveredSplit(p, imports)
	return all
}

// NotCoveredSplit is NotCovered's answer in the two halves D1545 needs: the
// reasons that REFUSE a plan (integrity, D1549) and the reasons that are
// recorded and warned instead (content). The two together are NotCovered's
// list, in its order and with its wording, so a reader that wants the whole
// answer keeps calling NotCovered and one that judges a plan asks for the
// halves.
//
// It exists on this cover and not only on tf.ManagedCover because this is the
// cover `fugaro update`'s gate is handed (internal/cli's stepCover wraps it,
// and it holds no delete list): a split the managed cover kept to itself left
// every CONTENT refusal of the installation's own review looking like an
// integrity one, which would have made D1545's whole point — record the two
// cover tables and warn — inert for the command that applies the most.
//
// Which reasons are which is decided in notCoveredSplit below, on exactly
// tf.ManagedCover's rule (managed_cover.go's coverFinding): the two tables that
// judge WHAT a plan contains — the type table and the grant table — are content
// while nothing is being removed, because D1545 leaves that judgement to
// record-and-warn, and every other rule of the chain is D1549's and refuses.
// The type table's content arm does not end the chain: the import rules and
// attributes' own rules below it are read for a type the modules do not carry
// as well, so an off-table resource in a project that is not this run's — or
// one importing an object this run's discovery never found — is refused as well
// as warned (notCoveredIntegrity).
func (c Cover) NotCoveredSplit(p *Plan, imports []ImportKey) (integrity, content []string) {
	_, integrity, content = c.notCoveredSplit(p, imports)
	return integrity, content
}

// notCoveredSplit is the one walk both answers come from, so a plan cannot be
// told two different things about one change depending on which half a caller
// asked for.
func (c Cover) notCoveredSplit(p *Plan, imports []ImportKey) (all, integrity, content []string) {
	if p == nil {
		return []string{"no plan"}, []string{"no plan"}, nil
	}
	add := func(rc ResourceChange, why string) {
		item := safetext.Field(rc.Address) + " (" + why + ")"
		all = append(all, item)
		integrity = append(integrity, item)
	}
	addContent := func(rc ResourceChange, why string) {
		item := safetext.Field(rc.Address) + " (" + why + ")"
		all = append(all, item)
		content = append(content, item)
	}
	addFinding := func(rc ResourceChange, f coverFinding) {
		if f.content {
			addContent(rc, f.why)
			return
		}
		add(rc, f.why)
	}
	creates := creates(p)
	createdSAIDs, heldSAIDs := planAccounts(p)
	creds := planServiceAccounts(p)
	dep := planDeployer(p)
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		switch {
		case len(acts) == 0:
			add(rc, "no actions")
			continue
		case !allOf(acts, "create", "update", "no-op", "read"):
			if !removalContent(rc) {
				add(rc, "actions "+strings.Join(acts, ", "))
				continue
			}
			// A removal of a classified, non-data-bearing type: what D1545
			// records and warns about. Its warning is not the whole answer
			// either — the walk carries on to judge the resource underneath it,
			// because a warning that ended the walk would make a removal a way
			// past every rule below that D1549 keeps refusing: a replace of a
			// scheduler pointed at another host, a custom role widened, an image
			// from somewhere else, a resource moved to another project. A REPLACE
			// is judged on what it creates; a plain DELETE creates nothing, so
			// terraform writes the destroyed resource in before and null in
			// after, and the same rules are handed the removed view instead
			// (removed, managed_cover.go) — otherwise a name outside the
			// projects, a resource of a type the modules dropped or an object
			// whose removal is refused for good would be waved through on the
			// removal's own warning.
			addContent(rc, "actions "+strings.Join(acts, ", "))
			if slices.Equal(acts, []string{"delete"}) {
				rc = removed(rc)
			}
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
			f := coverFinding{why: "a resource type outside the installation's modules", content: !removalRefuses(rc)}
			addFinding(rc, f)
			// The type table is CONTENT — D1545 records it and warns — and it
			// must not take the rules below it down with it: the imports and
			// attributes' own rules judge the resource itself, where it lands
			// and what it takes over, and they are D1549's whatever the type is.
			// A type the modules do not carry is therefore warned about AND
			// still refused for the deeper reason when there is one
			// (notCoveredIntegrity), which is the shape tf.ManagedCover's own
			// chain has.
			if f.content {
				if why := c.notCoveredIntegrity(rc, imports, creates, createdSAIDs, heldSAIDs, creds); why != "" {
					add(rc, why)
				}
			}
		case t == "google_project_iam_custom_role" && c.customRole(rc) != "":
			add(rc, c.customRole(rc))
		case rc.Change.Importing != nil && !c.importOK(rc.Change.Importing.ID):
			add(rc, "imports "+rc.Change.Importing.ID+", which is not in this run's projects")
		case rc.Change.Importing != nil && !slices.Contains(imports, ImportKey{rc.Address, rc.Change.Importing.ID}):
			add(rc, "imports "+rc.Change.Importing.ID+", which this run's discovery did not find")
		case c.attributes(rc, creates, createdSAIDs, heldSAIDs, creds) != "":
			add(rc, c.attributes(rc, creates, createdSAIDs, heldSAIDs, creds))
		case strings.HasSuffix(t, "_iam_member"):
			if why := c.grant(rc, creates, createdSAIDs, heldSAIDs, dep); why != "" {
				addFinding(rc, coverFinding{why: why, content: !removalRefuses(rc)})
			}
		}
	}
	return all, integrity, content
}

// notCoveredIntegrity is the part of the installation cover's chain below its
// type table — the two import rules and attributes' own rules — in the order
// the chain reads them. It is a function of its own for the reason
// tf.ManagedCover's helper of the name is (managed_cover.go): the type table is
// CONTENT for a type the modules do not carry, and its warning must not stop
// the rules that judge the resource itself from answering. "" when the change
// passes all of them.
func (c Cover) notCoveredIntegrity(rc ResourceChange, imports []ImportKey, creates, createdSAIDs, heldSAIDs map[string]bool, creds serviceAccountsPlan) string {
	switch {
	case rc.Change.Importing != nil && !c.importOK(rc.Change.Importing.ID):
		return "imports " + rc.Change.Importing.ID + ", which is not in this run's projects"
	case rc.Change.Importing != nil && !slices.Contains(imports, ImportKey{rc.Address, rc.Change.Importing.ID}):
		return "imports " + rc.Change.Importing.ID + ", which this run's discovery did not find"
	case c.attributes(rc, creates, createdSAIDs, heldSAIDs, creds) != "":
		return c.attributes(rc, creates, createdSAIDs, heldSAIDs, creds)
	}
	return ""
}

// removalContent is whether the arm this cover refuses a removal with — the one
// it has instead of a delete list — is a CONTENT refusal D1545 records and
// warns rather than an integrity one D1549 keeps. D1551's rule alone decides
// it: a delete, or a replace in either of terraform's two orders, of a
// data-bearing or unclassified type refuses for good (RefusesRemovalType), and
// one of any other classified type is a removal the managed gate now takes with
// a warning.
//
// A set terraform does not write, and forget, are not removals: nothing is
// destroyed by them, and this cover reads them fail closed — integrity — as
// tf.ManagedCover's managedActions reads the same sets (managed_cover.go).
func removalContent(rc ResourceChange) bool {
	return allOf(rc.Change.Actions, "create", "delete") && !RefusesRemovalType(rc.Type)
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

// credentialsAccountResource and intakeAccountResource are the names of the
// installation module's own two service accounts for its two Cloud Run
// services: resource "google_service_account" "credentials" in
// modules/installation/credentials.tf and "intake" in intake.tf. Both are
// counted, so a plan's address for either ends in
// google_service_account.<name>[0].
const (
	credentialsAccountResource = "credentials"
	intakeAccountResource      = "intake"
	voiceAccountResource       = "voice"
	// orchestratorAccountResource is the resident orchestrator's own account,
	// resource "google_service_account" "orchestrator" in
	// modules/installation/orchestrator.tf (mobile plan task P5.1b). It is the
	// account the module's Cloud Run service runs as (P5.1c), and the service
	// case reads it back through accountFrom, so it is counted the same way:
	// the plan's address for it ends in google_service_account.orchestrator[0].
	orchestratorAccountResource = "orchestrator"
	// intakeServiceResource is the Cloud Run service's own resource name:
	// intake.tf names the service and the account that runs it the same word,
	// and the service case dispatches on this one (cloudRunServiceName).
	intakeServiceResource = "intake"
	// voiceServiceResource is the same word for the hop (P5.3.3): voice.tf
	// names its service and its account "voice" too, and the dispatch below
	// reads this one.
	voiceServiceResource = "voice"
	// mobileAccountResource and mobileServiceResource are the same word a third
	// time (mobile plan task P5): mobile_service.tf names the public mobile
	// service and the account it runs as "mobile", and the two reads below —
	// accountOfResource for the account, cloudRunServiceName for the service —
	// dispatch on it.
	mobileAccountResource = "mobile"
	mobileServiceResource = "mobile"
)

// accountPlan is one of those accounts as a plan carries it, which the two
// service cases read: the account ID the module's own resource holds, by
// module, and whether this plan creates it or leaves it in place.
//
// It is deliberately not planAccounts' wider pair. modules/installation also
// creates the deployer, the history and the scheduler accounts, so a check
// made against every account of the module admits a plan whose Cloud Run
// template names one of those — the same image, the same bundle read, a
// different identity — and a plan that creates any of them satisfies the
// unknown-email arm vacuously (the review round that found this: the check was
// weaker than the case comment, the plan paragraph and the commit message all
// claimed).
type accountPlan struct {
	// id is the account ID of the module's resource this plan was read for, by
	// module: the local part the service's template.service_account must carry.
	id map[string]string
	// created is the modules whose account this plan creates, held the modules
	// it leaves in place (created, updated or unchanged, never deleted —
	// planAccounts' rule). A replace is in both.
	created map[string]bool
	held    map[string]bool
}

// serviceAccountsPlan is the installation module's five service accounts as a
// plan carries them, which the google_cloud_run_v2_service case reads: the
// credentials service's (D146), the secrets intake's (D1931, DS2.2), the voice
// hop's (P5.3.3), the resident orchestrator's (mobile plan task P5.1c) and the
// public mobile service's (P5).
//
// They are five and not one because the five services run as five different
// identities: a check that admitted either of the first two for the others
// would let a plan run the intake's container as the account that reads every
// model credential in the bundle — the account the intake's own roles
// deliberately keep free of every read (R3) — or the credentials service's as
// the account that only writes dynamic containers. The hop's is the third for
// the same reason and a sharper one: its service is reachable by anyone
// (voice.tf), so running another service's container as its account — or its
// container as the credentials service's — is a public endpoint one plan away
// from the bundle. The orchestrator's is the fourth: it holds
// iam.serviceAccounts.signJwt on the budget signer, so admitting it anywhere
// else would run a service as the identity that may mint a token for any uid
// (section 4.5 E4). The mobile service's is the fifth, and it is the sharpest
// of the five for the two reasons at once: it is public, and it signs custom
// tokens — a plan that ran any other container as this account would put the
// signJwt reach behind whatever routes that container serves.
type serviceAccountsPlan struct {
	credentials  accountPlan
	intake       accountPlan
	voice        accountPlan
	orchestrator accountPlan
	mobile       accountPlan
}

// planServiceAccounts reads the five accounts out of a plan. Every change
// counts, a no-op included: an unchanged account is one the plan holds and the
// service it runs keeps naming, which is the shape of every plan after the one
// that created the service.
func planServiceAccounts(p *Plan) serviceAccountsPlan {
	c := serviceAccountsPlan{
		credentials:  accountPlan{id: map[string]string{}, created: map[string]bool{}, held: map[string]bool{}},
		intake:       accountPlan{id: map[string]string{}, created: map[string]bool{}, held: map[string]bool{}},
		voice:        accountPlan{id: map[string]string{}, created: map[string]bool{}, held: map[string]bool{}},
		orchestrator: accountPlan{id: map[string]string{}, created: map[string]bool{}, held: map[string]bool{}},
		mobile:       accountPlan{id: map[string]string{}, created: map[string]bool{}, held: map[string]bool{}},
	}
	for _, rc := range p.ResourceChanges {
		for _, a := range []struct {
			resource string
			plan     *accountPlan
		}{
			{credentialsAccountResource, &c.credentials},
			{intakeAccountResource, &c.intake},
			{voiceAccountResource, &c.voice},
			{orchestratorAccountResource, &c.orchestrator},
			{mobileAccountResource, &c.mobile},
		} {
			id, ok := accountOfResource(rc, a.resource)
			if !ok || id == "" {
				continue
			}
			pre := modulePrefix(rc)
			a.plan.id[pre] = id
			if len(rc.Change.Actions) > 0 && !slices.Contains(rc.Change.Actions, "delete") {
				a.plan.held[pre] = true
			}
			if slices.Contains(rc.Change.Actions, "create") {
				a.plan.created[pre] = true
			}
		}
	}
	return c
}

// accountOfResource reads rc as the module's account resource named resource
// and returns the account ID it carries. ok is false for every other resource,
// the module's own siblings included: the resource name after the module
// prefix must be exactly that name, counted or not.
func accountOfResource(rc ResourceChange, resource string) (id string, ok bool) {
	if rc.Type != "google_service_account" {
		return "", false
	}
	pre := modulePrefix(rc)
	if pre == "" {
		return "", false
	}
	rest, found := strings.CutPrefix(rc.Address, pre+"."+rc.Type+".")
	if !found {
		return "", false
	}
	// "intake" or "intake[0]": the name, then the count index when the address
	// carries one. A different resource of the same module — the deployer's
	// address ends in ...google_service_account.fugaro_deployer, and the
	// fixtures' siblings in ...google_service_account.x — is not this one.
	if name, _, _ := strings.Cut(rest, "["); name != resource {
		return "", false
	}
	return str(rc.Change.After["account_id"]), true
}

// cloudRunServiceName is the installation module's Cloud Run service rc names,
// and "" for everything else: the resource name after the module prefix,
// counted or not. It is the same read accountOfResource makes one level over,
// and the two service cases dispatch on it — the credentials service and the
// intake are one module's two services, and which rules a change is held to
// may not be a hand-edited label or a container's own args.
func cloudRunServiceName(rc ResourceChange) string {
	if rc.Type != "google_cloud_run_v2_service" {
		return ""
	}
	pre := modulePrefix(rc)
	if pre == "" {
		return ""
	}
	rest, found := strings.CutPrefix(rc.Address, pre+"."+rc.Type+".")
	if !found {
		return ""
	}
	name, _, _ := strings.Cut(rest, "[")
	return name
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
	// The front door's account's row is one grant and not a role: the bit it
	// carries in grantRules opens the objectAdmin cell, and mcpauthStoreGrant
	// is what closes it again to D935's store — the account, that role, that
	// bucket, no condition. A plan that moved any of the four is refused here
	// even though the row would otherwise accept it: objectAdmin on the runs
	// bucket to the front door's account, say, which no module grants, or the
	// store's objectAdmin to an account that is not this one.
	if why == "" && t == "google_storage_bucket_iam_member" && c.isMCPMember(str(rc.Change.After["member"])) {
		why = c.mcpauthStoreGrant(rc)
	}
	// ... and the account's second grant is one row on the project too: the
	// state-read bit opens the custom-role cell, and mcpauthStateReadGrant
	// closes it again to that role, on the project it was declared in, under no
	// condition. A plan that moved the read to another role or project, or
	// another role to the account, is refused here even though the row would
	// otherwise accept it.
	if why == "" && t == "google_project_iam_member" && c.isMCPMember(str(rc.Change.After["member"])) {
		why = c.mcpauthStateReadGrant(rc)
	}
	// ... and the hop's account's one row, on the same role and the same
	// rule: voiceStateReadGrant closes the custom-role cell to that role, on
	// the project it was declared in, under no condition. The two accounts
	// share the role because RTDB IAM has it as the only read there is
	// (state_read.tf), so what tells the two grants apart is whose account the
	// member names.
	if why == "" && t == "google_project_iam_member" && c.isVoiceMember(str(rc.Change.After["member"])) {
		why = c.voiceStateReadGrant(rc)
	}
	// The hop's public invoker binding (voice.tf) is deliberately NOT checked
	// here, and its absence is the check: `allUsers` is a member no kind of
	// this table carries, so the classification above refuses it as a principal
	// nobody named and the step that carries it shows the finding — which is
	// D920's front door binding's own state (coordinator.tf) and D1545's rule
	// (a new kind of principal is one nobody refuses for me). What holds the
	// binding to the one service, role and region voice.tf writes is
	// deploy/terraform's TestAllUsersIsRefusedOnEveryOtherResource and
	// internal/infra/managed, both of which refuse rather than warn.
	if why == "" && t == "google_project_iam_member" {
		why = c.dynamicSecretsGrant(rc)
	}
	if why == "" && t == "google_storage_bucket_iam_member" {
		// The member says WHICH bucket check this is: the intake's own read is
		// one object under its own condition (intakeAdminListGrant), and
		// bucketCondition is what judges every other grant — a launcher's, a
		// job's, an account's — under the bucket conditions the modules write.
		// The two are alternatives and not two checks in a row: bucketCondition
		// refuses every conditioned objectViewer that is not a repository's or
		// a job's, so an intake grant reaching it would be refused for being
		// the very thing this check accepts.
		if c.isIntakeMember(str(rc.Change.After["member"])) {
			why = c.intakeAdminListGrant(rc)
		} else {
			why = c.bucketCondition(rc, createdSAIDs, heldSAIDs)
		}
	}
	// The renewal job's one secret binding is a row on a container:
	// licenceSecretGrant reads the target, and it is one of the two secret
	// grants in this file whose container is checked (plan K4.2).
	if why == "" && t == "google_secret_manager_secret_iam_member" && c.isLicenceMember(str(rc.Change.After["member"])) {
		why = c.licenceSecretGrant(rc)
	}
	// ... and the hop's provider-key binding is the other (P5.3.5): the same
	// shape and the same reason, over the container secrets.tf writes for the
	// forwarding half. The account's OTHER grant — the database read — is the
	// project row checked above (voiceStateReadGrant), and the two are read
	// here rather than in one function because their rows and their targets
	// are different.
	if why == "" && t == "google_secret_manager_secret_iam_member" && c.isVoiceMember(str(rc.Change.After["member"])) {
		why = c.voiceProviderKeyGrant(rc)
	}
	if why == "" && t == "google_project_iam_member" && !unknown(rc.Change, "role") && str(rc.Change.After["role"]) == "roles/batch.jobsEditor" {
		why = c.jobsCondition(rc.Change)
	}
	// The resident orchestrator's token-minter row is the one row of its table
	// whose TARGET is part of what makes it section 4.3's: the bit opens the
	// cell by member and by role, and this closes it again to the signer
	// account of this run. A plan that moved the role to another account would
	// otherwise hand the account that may mint a token for any uid the reach of
	// every job account of the installation.
	if why == "" && t == "google_service_account_iam_member" && c.isOrchestratorMember(str(rc.Change.After["member"])) {
		why = c.orchestratorMinterGrant(rc.Change)
	}
	// ... and the public mobile service's same row, one account over and on the
	// devices' signer rather than the runs': the bit opens the minter cell by
	// member and by role, and this closes it again to the account of THIS run's
	// Firebase project (mobileMinterGrant). Its other grant — the database role
	// — is the project row's, covered by the row itself.
	if why == "" && t == "google_service_account_iam_member" && c.isMobileMember(str(rc.Change.After["member"])) {
		why = c.mobileMinterGrant(rc.Change)
	}
	// ... and the credentials service's own row on that same signer (LM13):
	// the design's one NEW grant, and the only one of the three whose member is
	// a literal of a DIFFERENT root's apply — isCredentialsMember has already
	// pinned the member to the installation's credentials account, so what is
	// left to hold is the account the row is ON.
	if why == "" && t == "google_service_account_iam_member" && c.isCredentialsMember(str(rc.Change.After["member"])) {
		why = c.issuerMinterGrant(rc.Change)
	}
	// The consumer's signing grant (mobile-identity-pairing plan task J6; O17)
	// is the one row of that class in the tree, and its TARGET is the whole of
	// "the mailbox consumer's account only": the bit opens the cell to the
	// resident orchestrator with MailboxSignerRole, and mailboxSignerGrant
	// closes it again to this installation's one key, spelled out, with no
	// condition. A plan that pointed the account's signing at another key of
	// the project would otherwise be covered by the row alone.
	if why == "" && t == "google_kms_crypto_key_iam_member" {
		why = c.mailboxSignerGrant(rc.Change)
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
	creds := planServiceAccounts(p)
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
			if why := c.attributes(rc, made, createdSAIDs, heldSAIDs, creds); why != "" {
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
		case c.isCredentialsMember(m):
			// ... and neither is the credentials service's account, for the
			// same reason: its two rows are what it may hold, and a plan that
			// listed its address on the review screen would otherwise be able
			// to grant it a launcher's or an operator's role.
			kind = toCredentials
		case c.isCoordinatorMember(m):
			// The coordinator's writer likewise: its one row is the FP's
			// database admin.
			kind = toCoordinator
		case c.isVerifierMember(m):
			// The verifier account likewise (CSR14): its own kind, whose one
			// row is the credentials service's invoker, and whose
			// token-creator grant on it is checked by account in
			// deployerTokenCreator. Read here, before the review screen's
			// list, for the deployer's reason: a plan that listed its address
			// would otherwise be able to grant it a launcher's or an
			// operator's role.
			kind = toVerifier
		case c.isCatalogCheckMember(m):
			// ... and the project's catalog check job (D1148): its own kind,
			// which one row accepts — objectUser on a bucket, and only under
			// the records condition of one repository (catalogRecordsGrant) —
			// so every other grant it is given, and every other role a plan
			// might reach for, is refused and the update that carries them
			// asks its owner. Read here, before the review screen's list, for
			// the deployer's reason: a plan that listed its address would
			// otherwise be able to grant it a launcher's or an operator's
			// role.
			kind = toCatalogCheck
		case c.isReportsMember(m):
			// ... and the project's reports job (slice P): its own kind, which
			// two rows accept — objectViewer on runs/ and objectUser on
			// reports/, and only under the condition of the one prefix each
			// holds (reportsReadGrant, reportsWriteGrant) — so every other
			// grant it is given, and every other role a plan might reach for,
			// is refused and the update that carries them asks its owner. Read
			// here, before the review screen's list, for the deployer's
			// reason: a plan that listed its address would otherwise be able
			// to grant it a launcher's or an operator's role.
			kind = toReports
		case c.isLicenceMember(m):
			// ... and the installation's licence renewal job (plan K4.2): its
			// own kind, which three rows accept — objectViewer on the three
			// objects it finds its installation by, objectUser on the licence
			// object alone (licenceReadGrant, licenceWriteGrant) and its four
			// Secret Manager calls on the renewal container and no other
			// (licenceSecretGrant) — so every other grant it is given, and
			// every other role a plan might reach for, is refused and the
			// update that carries them asks its owner. Read here, before the
			// review screen's list, for the deployer's reason: a plan that
			// listed its address would otherwise be able to grant it a
			// launcher's or an operator's role.
			kind = toLicence
		case c.isMCPMember(m):
			// ... and the front door's own account (D920, D935, O16b): its own
			// kind, which two rows accept — objectAdmin on D935's store and the
			// state group's database read on the coordinator's project, each
			// narrowed further by mcpauthStoreGrant and mcpauthStateReadGrant,
			// which read the role, the target and the condition as well as the
			// member — so every other grant it is given is refused and the
			// update that carries one asks its owner.
			// Read here, before the review screen's list, for the deployer's
			// reason: a plan that listed the front door's address would
			// otherwise be able to grant it any toAccounts row's role.
			kind = toMCP
		case c.isIntakeMember(m):
			// ... and the secrets intake's own account (D1931, DS2.2): its own
			// kind, whose three rows — the conditioned write role, the shared
			// lister and objectViewer on the runs bucket — are narrowed further
			// by dynamicSecretsGrant and intakeAdminListGrant, so the condition
			// and the object are what make each grant the modules' own. Every
			// other grant it is given, an account's role or a person's alike,
			// is refused and the update that carries one asks its owner. Read
			// here, before the review screen's list, for the deployer's reason:
			// a plan that listed the intake's address would otherwise be able
			// to grant it a launcher's or an operator's role.
			kind = toIntake
		case c.isOrchestratorMember(m):
			// ... and the resident orchestrator's own account (mobile plan task
			// P5.1b): its own kind, whose four rows are the design's §4.3 table
			// and nothing else — the launcher's project role, the job runner's
			// role on the installation's jobs, the runs bucket's conditional
			// write and its unconditional read, and the token-minter role on
			// the signer account — so every other grant it is given is refused
			// and the update that carries one asks its owner. Read here, before
			// the review screen's list, for the deployer's reason: a plan that
			// listed this account's address would otherwise be able to grant it
			// any toAccounts row's role, and the design's eleventh never-rule
			// is precisely that it is never a launcher.
			kind = toOrchestrator
		case c.isVoiceMember(m):
			// ... and the voice hop's own account (P5.3.3): its own kind, whose
			// one row is the shared database read role, narrowed further by
			// voiceStateReadGrant — the account, that role, that project, no
			// condition. Every other grant it is given is refused and the update
			// that carries one asks its owner. Read here, before the review
			// screen's list, for the deployer's reason: this account runs the one
			// service anyone on the internet may call, so a plan that listed its
			// address would otherwise be able to grant it any toAccounts row's
			// role.
			kind = toVoice
		case c.isMobileMember(m):
			// ... and the public mobile service's own account (mobile plan
			// task P5): its own kind, whose two grants are the custom database
			// role — the row's, checked here exactly as the private service's
			// own binding is — and the signJwt on the Firebase project's
			// device-only signer, which mobileMinterGrant narrows to that
			// account. Every other grant it is given is refused and the update
			// that carries one asks its owner. Read here, before the review
			// screen's list, for the deployer's reason, and with this account
			// most of all: it runs the tree's other service anyone on the
			// internet may call, and it mints an identity for any uid, so a
			// plan that listed its address would otherwise be able to grant it
			// any toAccounts row's role on top of that.
			kind = toMobile
		case c.isBuildIdentityMember(m):
			// ... and the project's build identity (B5, D2301): its own kind,
			// which the cells B6 added accept — the invoker bit on the
			// credentials service, the registry pair a push and a pull need,
			// and the runs bucket and provider-credential reaches a catalog
			// build's own steps make — so every other grant it is given, and
			// every other role a plan might reach for, is refused and the
			// update that carries them asks its owner. Read here, before the
			// review screen's list, for the deployer's reason: a plan that
			// listed its address would otherwise be able to grant it a
			// launcher's or an operator's role, and the whole point of the
			// account is that a catalog build reaches the one service and
			// nothing else of this installation's.
			kind = toBuildIdentity
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
		// The dynamic class's two accounts (DS2.2) are refused a computed role
		// as flatly as the deployer is, and for the same reason: what this
		// file checks about their grants is the condition, and
		// dynamicSecretsGrant reads that condition off a role it can name. The
		// role at stake is this module's own custom role --
		// google_project_iam_custom_role.intake_secrets -- so a plan that
		// wrote it as a reference instead of the literal would hand the intake
		// create, delete and version-add for every container of the project
		// with no condition to read, under the ordinary confirmation. Both
		// roles are therefore written as literals in the modules (intake.tf,
		// credentials.tf), and a plan that computes one asks its owner.
		if kind == toIntake {
			return "a computed role to the intake's account: it holds its own two roles, named, and the name-prefix condition on the write role is the whole of what fences it to the delivery grammar (D1931, F30)"
		}
		if kind == toLicence {
			// The renewal job's secret role is this module's own custom role
			// (fugaroLicenceRenewal), written as a literal in licence.tf, and
			// licenceSecretGrant's whole check is the container the literal is
			// bound on. A computed role here could be any role of the project,
			// bound on the container the plan names, under the ordinary
			// confirmation.
			return "a computed role to the installation's licence renewal job: it holds its own custom role, named (projects/<project>/roles/" + LicenceRenewalRole + "), on the one renewal container, and licenceSecretGrant compares that binding's target (plan K4.2)"
		}
		if kind == toCredentials {
			return "a computed role to the credentials service's account: it holds the bundle's accessor, the dynamic class's prefix accessor and the shared lister, named, and the prefix condition is what narrows the second (D1931, Q6)"
		}
		// The resident orchestrator is refused a computed role as flatly as
		// those two, and for their reason: the row below this one would accept
		// a computed role whenever the plan creates ANY custom role of this
		// module — a plan that created the intake's secret role beside the
		// grant would then hand the orchestrator a project role nobody can
		// name, and a project role of this tree is a project-wide reach (the
		// intake's is create, delete and version-add for every container of the
		// project, with the name-prefix condition this write has no room to
		// check). The account's every row is written as a literal in
		// orchestrator.tf for this reason, and a plan that computes one asks
		// its owner.
		if kind == toOrchestrator {
			return "a computed role to the resident orchestrator's account: section 4.3's table is the whole of what it may hold, every row of it is written as a literal in orchestrator.tf, and a role nobody can name is one no reader of this plan can hold to that table"
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
		return "grants " + svc.Role + " under a condition that is not " + DeployerConditionTitle + " on the managed prefix: " + svc.Expression() + " is the whole fence around that role, byte for byte (D75 (2), one binding per service since 0.10.0)"
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
// every other grant on that type. The role is granted on the deployer account,
// since CSR14 on the installation's own verifier account, and on the resident
// orchestrator's account (orchestrator.tf: the daemon on an operator's machine
// mints its ID token as that account, and the grant lets an operator act as an
// account holding signJwt on the budget signer), and on nothing else; grantRules has already checked that the member is a person on the
// review screen, so what is left is which account.
//
// The account has to be named, and named as one of those two, in this run's
// installation project (Projects[0]), never in a verified Firebase project the
// installation module is not applied to: a plan that says "an account, decided
// during apply" is a plan whose account nobody can check, and the account this
// grant is on is the difference between an operator who can assume the deployer
// and one who can mint a token for the scheduler (which can reach the budget
// backend) or for any other identity.
//
// The verifier account is the second account deliberately, in CSR14: it is a
// literal ID in the module (verifier.tf) and its whole reach is the
// credentials service's verify route, so the token an operator mints for it is
// the one caller that route admits. Without the second site here the plan that
// creates the account — the same `fugaro update` that turns the service on —
// would be refused, which is the non-converging update CLAUDE.md's HARD RULE
// is about.
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
	if m != nil && dp != "" && m[1] == dp && m[3] == dp && (m[2] == DeployerAccountID || m[2] == VerifierAccountID || m[2] == OrchestratorAccountID) {
		return ""
	}
	want := "the deployer account of this run"
	if dp != "" {
		want = DeployerAccount(dp) + ", the verifier account " + VerifierAccountID + "@" + dp + ".iam.gserviceaccount.com or the resident orchestrator's account " + OrchestratorAccountID + "@" + dp + ".iam.gserviceaccount.com"
	}
	return fmt.Sprintf("grants %s on %s, which is not %s: the operators' impersonation is on those three accounts alone", DeployerTokenCreatorRole, id, want)
}

// orchestratorMinterGrant says why a token-minter grant to the resident
// orchestrator is not section 4.3's row, "" when it is or when the grant is not
// this row at all. It exists for the deployer's reason one role over:
// grantRules decides a grant by its MEMBER and its ROLE, and on
// google_service_account_iam_member neither of them says WHICH account the role
// is on — so without this rule a plan granting the resident orchestrator
// fugaroTokenMinter on a job account, or on any other account of this run,
// rides the ordinary confirmation, and iam.serviceAccounts.signJwt on an
// account IS that account: the self-signed JWT it signs is an access token for
// the issuer, so the resident orchestrator could be the job whose secrets are
// the model credentials (section 6 rule 5) or any other identity of the
// installation. The row is the budget signer account of this run and nothing
// else, in the project the role itself is declared in.
//
// Which of this run's projects the signer lives in is not the cover's to
// derive — the design's Firebase backend may be the installation's own project
// or one of its own, and the module takes the project from
// var.orchestrator.firebase_project — so the row is held to that ACCOUNT of
// this run, in one of this run's projects. The account has to be named: the
// module writes it out (orchestrator.tf), and a plan that leaves it unknown is
// a plan whose account nobody can check.
func (c Cover) orchestratorMinterGrant(ch Change) string {
	key, ok := c.roleKey(str(ch.After["role"]))
	if !ok || key != customKey+"fugaroTokenMinter" {
		return ""
	}
	// The account ID the Firebase root's signer carries (modules/firebase/iam.tf,
	// FugaroAccounts' own spelling of it).
	const signerID = "fugaro-token-signer"
	if unknown(ch, "service_account_id") {
		return "a fugaroTokenMinter grant to the resident orchestrator whose account is not known: section 4.3's row is the budget signer account of this run, written out, and a plan that computes the account is one whose signer nobody can name"
	}
	id := str(ch.After["service_account_id"])
	if m := saName.FindStringSubmatch(id); m != nil {
		for _, p := range c.Projects {
			if m[1] == p && m[3] == p && m[2] == signerID {
				return ""
			}
		}
	}
	return fmt.Sprintf("grants fugaroTokenMinter on %s to the resident orchestrator: the design's row is the budget signer account of this run (%s), and signJwt on any other account makes the resident orchestrator that account — the job accounts holding the model credentials among them (section 4.3, section 6 rule 5)", id, signerID+"@<one of this run's projects>.iam.gserviceaccount.com")
}

// mobileMinterGrant is orchestratorMinterGrant's rule for the public mobile
// service's own account (mobile plan task P5, O1, O8), and it is the function
// that makes P5's acceptance sentence a property of a plan rather than of the
// module's text: the account's signJwt is on the DEVICE-ONLY signer of the
// installation's Firebase project and on nothing else.
//
// Why the target is the whole of the check here. The role's row already says
// the account may hold fugaroTokenMinter, and iam.serviceAccounts.signJwt is
// the widest grant of this tree: its holder signs ANY claim set for ANY uid as
// the account it is on (orchestrator.tf's own sentence, section 4.3), and IAM
// cannot limit which claims a signer signs. So what holds this service to
// device tokens is not this grant but the route allowlist its mobile mode
// mounts and the tests over it (I1) — and what holds the grant itself to the
// devices' key, and away from the runs' signer and from every other account of
// the installation, is this comparison. A plan that pointed it at
// fugaro-token-signer would make the public endpoint the identity that mints
// every run's and every launcher's token; one that pointed it at a job account
// would put the model credentials' identity behind a URL anyone may call.
//
// The account has to be named: the row is written in the root that CREATES the
// signer (modules/firebase/iam.tf's mobile_token_minter), and it spells the
// account and the role out as literals rather than referencing the two
// resources beside it, because a reference reaches a plan as UNKNOWN — a
// principal and a role nobody can read off the review screen. A plan that
// computes either is refused, as a computed role is.
//
// The project is this run's and not a fixed one, for orchestratorMinterGrant's
// reason: the Firebase project may be the installation's own or one of its own
// (D6, D2781), and which of them it is is a fact of the tfvars and not of this
// package. What the grant must not do is name a project the review was not
// shown.
func (c Cover) mobileMinterGrant(ch Change) string {
	key, ok := c.roleKey(str(ch.After["role"]))
	if !ok || key != customKey+"fugaroTokenMinter" {
		return ""
	}
	if unknown(ch, "service_account_id") {
		return "a fugaroTokenMinter grant to the public mobile service whose account is not known: the account is the device-only signer of the Firebase project, written out as a literal in the row that binds it (firebase/iam.tf's mobile_token_minter), and a plan that computes it is one whose signer nobody can name"
	}
	id := str(ch.After["service_account_id"])
	if m := saName.FindStringSubmatch(id); m != nil {
		for _, p := range c.Projects {
			if m[1] == p && m[3] == p && m[2] == DeviceSignerAccountID {
				return ""
			}
		}
	}
	return fmt.Sprintf("grants fugaroTokenMinter on %s to the public mobile service: its one signJwt is on the devices' signer account of this run's Firebase project (%s), never on the runs' own signer and never on a job account — signJwt signs any claim set for any uid, so the account it is on IS the identity this public service may mint (P5, O1, O8)", id, DeviceSignerAccountID+"@<one of this run's projects>.iam.gserviceaccount.com")
}

// mailboxSignerGrant is voiceProviderKeyGrant's shape one row over, and it is
// the function that makes O17's "with signing granted to the mailbox consumer's
// account only" a property of a plan rather than of the module's text: the
// consumer's one grant is on THIS installation's one key, in this
// installation's own project, under no condition, and on nothing else.
//
// Why the key is the check that matters here. The row already says the
// consumer's account may hold the signer's role, and the role is the whole of
// what that account may do with a key: make a signature with it. It does not
// say which role of which project — roleKey resolves a custom role by its ID in
// any project of this run — so the grant is held to this installation's own
// role as well. What the row cannot say at all is WHICH key, and the difference
// matters more than it looks — a
// grant on another key of the same project is a signature the phones of this
// installation do not verify (they pinned the public half of this key, O16),
// made with material no step of this tree published or can revoke, and a grant
// on a key an operator created by hand is a second consumer identity the
// update's own review screen never showed. So the cell is closed here to the
// name mailbox_key.tf composes: projects/<the installation>/locations/<region>/
// keyRings/fugaro-mailbox/cryptoKeys/fugaro-mailbox-consumer.
//
// The key has to be SPELLED OUT, and the reason is the ordering one
// orchestrator.tf's launcher row gives: a provider-computed name reaches a
// plan as UNKNOWN — the module writes the literal for exactly that reason, and
// depends_on is what orders the binding behind the key the literal names — so
// an unknown target is a plan that did not take it from the module's own text
// and is refused. The project is this run's installation project
// (Cover.installationProject) rather than any project of Projects: O17 puts the
// key in the installation's project, and a grant in the Firebase project beside
// it would be a key the installation root does not create.
func (c Cover) mailboxSignerGrant(ch Change) string {
	project := c.installationProject()
	if project == "" {
		return "grants the consumer's signing role in no project of this run"
	}
	// The role's OWN project, which roleKey does not decide: that lookup admits
	// a custom role of any project of this run (Cover.Projects — the
	// installation's own and a verified Firebase project beside it), so a plan
	// could bind an identically named role of the Firebase project to this
	// account on this installation's key. The name is not the role: the other
	// project's is a different role with that project's own permission list,
	// and O17's row is the role the installation root creates.
	if got, want := str(ch.After["role"]), "projects/"+project+"/roles/"+MailboxSignerRole; got != want {
		return fmt.Sprintf("grants %s, and the installation's own signer role is %s: a role of the same ID in another project of this run is another project's rule with its own permission list, and the row O17 names is the one the installation creates", got, want)
	}
	if unknown(ch, "crypto_key_id") {
		return fmt.Sprintf("grants the consumer's signing role on a key that is not known: the design's row is the installation's one consumer key (%s), written out in mailbox_key.tf, and a plan that computes the key is one whose key nobody can name", mailboxCryptoKeyPath(project, c.Region))
	}
	want := mailboxCryptoKeyPath(project, c.Region)
	if got := str(ch.After["crypto_key_id"]); got != want {
		return fmt.Sprintf("grants the consumer's signing role on %s, and the installation's consumer key is %s: a signature made under another key verifies nowhere in this installation — every paired phone pinned the public half of this one (O16) — so a second key is a second consumer identity, which no module writes", got, want)
	}
	conds, why := grantConditions(ch, "a key grant")
	switch {
	case why != "":
		return why
	case len(conds) != 0:
		return "grants the consumer's signing role under a condition, which the module never sets: Cloud KMS expresses no useful condition here (the account's single permission is already the whole of what it may do with the key), so one would fence nothing and hide the key the grant is on from the review screen"
	}
	return ""
}

// mailboxCryptoKeyPath is the resource name of the installation's consumer
// signing key, composed the way mailbox_key.tf's own literal is: the key ring
// and the key are the module's two names (MailboxKeyRingName,
// MailboxCryptoKeyName) and the project and location are the installation's.
// internal/mailbox's ConsumerCryptoKeyPath is the same composition on the
// signer's side, and internal/infra's TestMailboxKeyNamesAreTheGoSpellings
// holds the two to the module's literals.
func mailboxCryptoKeyPath(project, region string) string {
	return "projects/" + project + "/locations/" + region + "/keyRings/" + MailboxKeyRingName + "/cryptoKeys/" + MailboxCryptoKeyName
}

// issuerMinterGrant is orchestratorMinterGrant's rule for the credentials
// service's own account (launcher-does-not-mint plan task LM13; D3121, F7), and
// it is the rule that makes the design's one new row a property of a plan
// rather than of the module's text: the account's signJwt is on the runs'
// signer of this run's Firebase project and on nothing else.
//
// Why the target is the whole of the check here. The role's row already says
// the account may hold fugaroTokenMinter, and iam.serviceAccounts.signJwt is
// the widest grant of this tree: its holder signs ANY claim set for ANY uid as
// the account it is on (orchestrator.tf's own sentence, section 4.3). That is
// the grant the design moves a run's identity onto — the service becomes the
// one identity that mints every run's and every launcher's token — so where
// the orchestrator holds it as the budget backend's own writer, this account
// holds it as the issuer, and the account it is on has to be the signer the
// runs are minted through. A plan that pointed it at the devices' signer would
// leave the runs' identity to the mobile route; one that pointed it at a job
// account would put every run's identity behind the model credentials'
// account.
//
// The account has to be named: the row is written in modules/firebase/iam.tf
// with the signer's name and the role spelled out as literals rather than read
// off the two resources beside it, because a reference reaches a plan as
// UNKNOWN — a principal and a role nobody can read off the review screen, and
// the row is one whose whole content is the account it is on. A plan that
// computes either is refused, as a computed role is. The project is this run's
// and not a fixed one, for mobileMinterGrant's reason: the Firebase project
// may be the installation's own or one of its own (D6, D2781).
//
// The MEMBER is this account and no other — the LITERAL the root writes
// (var.issuer_account) rather than a computed one. That half is already settled
// by the time this function runs: grant() reaches it through
// isCredentialsMember, which matches the member string byte for byte against
// the installation's credentials account, so a plan whose member is computed
// never arrives here: the minter cell carries no plain-account kind, and the
// grant is refused before this function is reached. What is left is the
// account the row is on, which is this function's whole subject.
func (c Cover) issuerMinterGrant(ch Change) string {
	key, ok := c.roleKey(str(ch.After["role"]))
	if !ok || key != customKey+"fugaroTokenMinter" {
		return ""
	}
	// The account ID the Firebase root's signer carries (modules/firebase/iam.tf,
	// FugaroAccounts' own spelling of it).
	const signerID = "fugaro-token-signer"
	if unknown(ch, "service_account_id") {
		return "a fugaroTokenMinter grant to the credentials service whose account is not known: the issuer's row is the budget signer account of this run, written out in the root that creates it (firebase/iam.tf's issuer_minter), and a plan that computes the account is one whose signer nobody can name"
	}
	id := str(ch.After["service_account_id"])
	if m := saName.FindStringSubmatch(id); m != nil {
		for _, p := range c.Projects {
			if m[1] == p && m[3] == p && m[2] == signerID {
				return ""
			}
		}
	}
	return fmt.Sprintf("grants fugaroTokenMinter on %s to the credentials service: the issuer's one row is the budget signer account of this run (%s), and signJwt on any other account makes the credentials service that account — the devices' signer, which would leave every run's identity to the mobile route, and the job accounts holding the model credentials among them (D3121, F7, section 4.3)", id, signerID+"@<one of this run's projects>.iam.gserviceaccount.com")
}

// accountClause is one clause of a job or build account's bucket condition,
// as gcp.BucketCondition writes it.
var accountClause = regexp.MustCompile(`^resource\.name\.startsWith\("projects/_/buckets/([a-z0-9][a-z0-9._-]*)/objects/(runs|cache|locks|builds)/([a-z0-9][a-z0-9_-]*)/"\)$`)

// tokenClause is the one clause of a job account's read-only grant on its
// repository's budget token objects, as gcp.TokenBucketCondition writes it:
// tokens/<slug>/ of a bucket, nothing before it and nothing after it.
var tokenClause = regexp.MustCompile(`^resource\.name\.startsWith\("projects/_/buckets/([a-z0-9][a-z0-9._-]*)/objects/` + gcp.TokenBucketPrefix + `/([a-z0-9][a-z0-9_-]*)/"\)$`)

// conditionSlug is the shape of the repository slug a condition names — the
// third group of accountClause and tokenClause above, which is what the modules
// key a per-repository grant by (var.catalog_check.repositories' r.slug is a
// task.Slug). Two of those clauses read the slug out of a whole clause and
// cannot see a slug the plan spelled differently, but a caller that BUILDS an
// expression from a slug the plan wrote (catalogRecordsGrant, over
// gcp.CatalogRecordsCondition) has to check it first: the slug lands in a CEL
// string literal, so one carrying a quote and the rest of the condition builds
// whatever expression its own title asks for — and the plan writes the title
// and the expression both, so the comparison below would pass on a grant this
// account's row exists to refuse.
var conditionSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// dynamicSecretsGrant says why a project grant of the dynamic secret class is
// not the modules', "" when it is — and "" for every project grant that is not
// of the class, which is the whole of what this function reads.
//
// The class's whole reach in a project is three bindings (intake.tf and
// credentials.tf, D1931, DS2.2), and each is the one thing a row of grantRules
// cannot see:
//
//   - the intake's write role (IntakeSecretsRole) to the intake's own account,
//     under the name-prefix fence;
//   - the credentials service's prefix accessor — a PROJECT grant of
//     roles/secretmanager.secretAccessor, deliberately not the per-secret
//     bundle's — to the service's own account, under the same fence byte for
//     byte: the reader half of the names the intake writes;
//   - the lister role (secretmanager.secrets.list alone), to either of those
//     two accounts, under NO condition, because Check 510 (2026-11-12) refused
//     that call under the fence.
//
// What makes this check the fence rather than a formality: without it a plan
// could grant the write role UNCONDITIONED — create and delete for every secret
// container of the project — or hand the prefix accessor to an account of its
// choosing, and both would ride the same one confirmation, since grantRules
// reads a member kind and a role and says nothing about a condition. The
// expression is compared against gcp.DynamicSecretNamesCondition() byte for
// byte, so a narrower or a wider spelling is refused rather than read as nearly
// right, and a condition known only after apply is refused the way the rest of
// this file refuses one.
func (c Cover) dynamicSecretsGrant(rc ResourceChange) string {
	ch := rc.Change
	key, ok := c.roleKey(str(ch.After["role"]))
	if !ok {
		return ""
	}
	write := key == customKey+IntakeSecretsRole
	lister := key == customKey+SecretListerRole
	accessor := key == "roles/secretmanager.secretAccessor"
	if !write && !lister && !accessor {
		return ""
	}
	member := str(ch.After["member"])
	switch {
	case write && !c.isIntakeMember(member):
		return "grants the intake's write role to " + member + ", which is not the intake's own account: create, delete and version-add of the dynamic containers is one account's authority (D1931)"
	case accessor && !c.isCredentialsMember(member):
		return "grants a project-wide roles/secretmanager.secretAccessor to " + member + ", which is not the credentials service's own account: the dynamic class's reader is that account and the per-secret bundle stays the only other accessor (D1931, Q6)"
	case lister && !c.isIntakeMember(member) && !c.isCredentialsMember(member):
		return "grants the dynamic secrets lister to " + member + ", which is neither the intake's account nor the credentials service's: those two are the only principals that find the dynamic containers by listing (D1931)"
	}
	if p := str(ch.After["project"]); !slices.Contains(c.Projects, p) {
		return "grants " + str(ch.After["role"]) + " for the dynamic secrets in project " + p + ", which is not one of this installation's projects"
	}
	conds, why := grantConditions(ch, "a dynamic secrets grant")
	if why != "" {
		return why
	}
	if lister {
		if len(conds) != 0 {
			return "grants the dynamic secrets lister under a condition, which the modules never do: secretmanager.secrets.list is REFUSED under the name-prefix condition (Check 510), so the list is the one half of the fence that carries none"
		}
		return ""
	}
	if len(conds) == 0 {
		return "grants " + str(ch.After["role"]) + " for the dynamic secrets with no condition, which is every secret container of the project: the name-prefix fence is the whole of what narrows it to the grammar's names (D1931, F30)"
	}
	if str(conds[0]["description"]) != "" {
		return "a dynamic secrets grant whose condition has a description, which the modules never set"
	}
	if title, expr := str(conds[0]["title"]), str(conds[0]["expression"]); title != gcp.DynamicSecretNamesConditionTitle || expr != gcp.DynamicSecretNamesCondition() {
		return "grants " + str(ch.After["role"]) + " for the dynamic secrets under a condition that is not " + gcp.DynamicSecretNamesConditionTitle + ": " + gcp.DynamicSecretNamesCondition() + " is the fence, byte for byte, and the same one the intake's writes carry (D1931, F30)"
	}
	return ""
}

// intakeAdminListGrant says why a bucket grant to the intake is not the
// modules', "" when it is (and "" for every bucket grant that is not the
// intake's): objectViewer on the runs bucket under a condition naming ONE
// object, the derived admin list `fugaro update` publishes (DS2.0's
// access.PathAdminList), for the intake's own account. The object is read by
// name and never by listing — the intake holds no storage.objects.list — and
// the condition is what makes that true: an objectViewer on the bucket without
// one reaches every run record, prompt and transcript the installation keeps.
//
// The bucket is read from the cover rather than from the grant where it can be
// (a bucket this run knows), and the expression is built here from that name
// and the object's fixed key, so a plan that moved the read to another object —
// the admin list's own neighbours are the run records — is refused.
func (c Cover) intakeAdminListGrant(rc ResourceChange) string {
	ch := rc.Change
	if !c.isIntakeMember(str(ch.After["member"])) {
		return ""
	}
	if role := str(ch.After["role"]); role != "roles/storage.objectViewer" {
		return "grants the intake's account " + role + " on a bucket, and its one bucket role is objectViewer on the admin list's object (DS2.0)"
	}
	conds, why := grantConditions(ch, "a bucket grant")
	if why != "" {
		return why
	}
	if len(conds) == 0 {
		return "grants the intake's account objectViewer on the bucket with no condition, which is every run record, prompt and transcript the installation keeps: the derived admin list is ONE object"
	}
	if str(conds[0]["description"]) != "" {
		return "a bucket grant whose condition has a description, which the modules never set"
	}
	bucket := str(ch.After["bucket"])
	if bucket == "" || !slices.Contains(c.Buckets, bucket) {
		return "grants the intake's account objectViewer on bucket " + bucket + ", which is not the runs bucket of this installation"
	}
	want := IntakeAdminListCondition(bucket)
	if str(conds[0]["title"]) != gcp.IntakeAdminListConditionTitle || str(conds[0]["expression"]) != want {
		return "grants the intake's account objectViewer on the runs bucket under a condition that is not the derived admin list's one object: " + want + " is that read, byte for byte"
	}
	return ""
}

// IntakeAdminListCondition is the one expression the intake's read of the
// derived admin list is bound under (intake.tf, DS2.0's access.PathAdminList):
// ONE object of that bucket and nothing else in it. It is here rather than
// built twice — in the module and in this check — for the reason every other
// condition of the modules has its builder beside its check: a test that
// spells it out again could agree with the module while the check refused it.
func IntakeAdminListCondition(bucket string) string {
	return `resource.name == "projects/_/buckets/` + bucket + `/objects/` + access.PathAdminList + `"`
}

// bucketCondition says why a bucket grant's condition is not the modules'
// (docs/bucket-iam/spec.md H6), "" when it is: objectAdmin carries none; an
// objectViewer carries none, except an account's, which may carry exactly
// one tokens/<slug>/ clause on one of this run's buckets (the job's read of
// its own budget token objects, budget-token-channel.md §7.2) — and then only
// for a Fugaro job account of this run that the grant's own module holds or
// creates, under the condition title built from that account
// (jobAccountGrant) — or exactly one whole-builds/catalog/ clause
// (gcp.CatalogReadCondition, a build account's read of the copied catalog
// definitions, PIC4), under the same account rule and that condition's own
// title (catalogReadGrant) — which since B6 is also the project's build
// identity's read of the same copy, under the title built from that account
// and that account alone (buildIdentityGrantAccount) — or, in the
// project module and for an account that module creates, exactly one
// whole-tokens/ clause (gcp.ProjectTokenBucketCondition, the shared job's
// read of every repository's sealed token, plan task 3.2 step 2); a person's
// objectUser carries exactly the launchers' condition on one of this run's
// buckets; an account's objectUser carries either a project job's widened
// condition (gcp.ProjectBucketCondition, byte for byte, in the project
// module, with the title of an account that module creates) or clauses of
// gcp.BucketCondition's shape on runs/, cache/, locks/ or builds/ — never
// tokens/ — each on one of this run's buckets; and the catalog check job's
// account carries exactly one repository's builds/<slug>/catalog/ clause
// (gcp.CatalogRecordsCondition, the job's read-write reach into the records
// and check state of the repository an entry belongs to, D1148), under that
// condition's own title and that account alone (catalogRecordsGrant) — which
// since B6 is also the project's build identity's grant of the same condition,
// one per repository, the record a catalog build writes being the one the
// check reads back — and the reports job's account carries exactly the
// two-clause condition of ONE prefix
// of one of this run's buckets: the runs/ one under objectViewer
// (gcp.ReportsReadCondition, the records its reports summarise) and the
// reports/ one under objectUser (gcp.ReportsWriteCondition, the one write that
// prefix has), each under its own condition title and that account alone
// (reportsReadGrant, reportsWriteGrant). The modules never set a description,
// and a condition known only after apply is not covered.
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
			// project job's, or a build account's read of the copied catalog
			// definitions, each of which is a widening of its own and is
			// accepted only for the accounts its own builder's title names.
			if c.projectJobTokenCondition(rc, createdSAIDs, heldSAIDs, title, expr) {
				return ""
			}
			if c.catalogReadGrant(rc, createdSAIDs, heldSAIDs, title, expr) {
				return ""
			}
			if ok, why := c.reportsReadGrant(rc, createdSAIDs, title, expr); ok {
				return ""
			} else if why != "" {
				return why
			}
			if ok, why := c.licenceReadGrant(rc, createdSAIDs, title, expr); ok {
				return ""
			} else if why != "" {
				return why
			}
			// The title says which grant this claims to be: when it is the
			// catalog builder's spelling, the refusal names that grant rather
			// than the tokens/ ones the expression alone would suggest.
			if strings.HasPrefix(title, gcp.CatalogReadConditionTitlePrefix) {
				return "grants objectViewer on the bucket under a build account's builds/catalog/ condition to a member that is not the build account its title names, which the modules never do"
			}
			// The reports job's account is admitted by this branch's row for
			// its runs/ read and for nothing else, so its name under any other
			// condition is refused HERE, with the grant named: the account
			// reads its source records and holds no other read of the bucket
			// (ruling E4).
			if c.isReportsMember(str(ch.After["member"])) {
				return "grants objectViewer on the bucket to the project's reports job under a condition that is not its runs/ condition of this run's bucket (gcp.ReportsReadCondition), which is the only objectViewer the modules give it (ruling E4)"
			}
			// ... and the renewal job's account has the same rule for its
			// three-name read (plan K4.2): its name under any other condition
			// is refused here rather than read as some other job's grant.
			if c.isLicenceMember(str(ch.After["member"])) {
				return "grants objectViewer on the bucket to the installation's licence renewal job under a condition that is not its three-object read of this run's bucket (gcp.LicenceReadCondition), which is the only objectViewer the module gives it (plan K4.2)"
			}
			return "grants objectViewer on the bucket to an account under a condition that is not one repository's tokens/ condition of this run's bucket, a project job's whole-tokens/ one, nor a build account's builds/catalog/ one"
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
		// An unconditioned grant to the reports job is the bucket-wide one:
		// every other account's objectViewer here is a job reading the whole
		// bucket, which is the modules' own choice for them, but this account
		// holds objectViewer under its runs/ condition alone — a whole-bucket
		// read of it would reach tokens/, cache/, locks/ and the issue store,
		// none of which the job has any business reading (ruling E4).
		if c.isReportsMember(str(ch.After["member"])) {
			return "grants " + role + " on the bucket, unconditioned, to the project's reports job: the modules give it objectViewer under its runs/ condition and objectUser under its reports/ one, and nothing else (ruling E4)"
		}
		// ... and the renewal job's account for the same reason and with the
		// same two conditioned rows (plan K4.2): objectViewer under its
		// three-object read and objectUser under the licence object's own
		// condition are the module's whole reach on the bucket, so an
		// unconditioned grant of either role is the bucket-wide one — every
		// run record, prompt, transcript, token, cache, lock and issue, none
		// of which the job reads (D524).
		if c.isLicenceMember(str(ch.After["member"])) {
			return "grants " + role + " on the bucket, unconditioned, to the installation's licence renewal job: the module gives it objectViewer under its three-object read and objectUser under the licence object's own condition, and nothing else (plan K4.2)"
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
		return "grants objectUser on the bucket to a person under a condition that is not the launchers' runs/, tokens/ and issues/ condition"
	case role == "roles/storage.objectUser" && c.isOrchestratorMember(str(ch.After["member"])):
		// The resident orchestrator's write (mobile plan task P5.1b) is the
		// launchers' own, byte for byte: runs/, tokens/ and issues/ of this
		// run's bucket, under the same title and the same expression, so the
		// account's reach is the list's rather than a second spelling of it that
		// happens to look similar. It is a case of its own because the `person`
		// case above is keyed on the review screen's list, and this account is
		// deliberately not on it (the design's eleventh never-rule): falling
		// through to the account case below would refuse the grant, which is
		// the right answer for every other condition and the wrong one here.
		for _, b := range c.Buckets {
			if !gcp.ValidBucketName(b) {
				continue
			}
			if (bucket == "" || bucket == b) && title == gcp.LauncherBucketConditionTitle && expr == gcp.LauncherBucketCondition(b) {
				return ""
			}
		}
		return "grants objectUser on the bucket to the resident orchestrator under a condition that is not the launchers' runs/, tokens/ and issues/ condition: the account's write area is the launchers' and the bucket root stays out of it, because the managed marker is written there (docs/mobile/design/always-on-orchestrator.md §4.3, §6 rule 2)"
	case role == "roles/storage.objectUser":
		if len(conds) == 0 {
			return "grants objectUser on the bucket to an account with no condition"
		}
		if c.projectJobCondition(rc, createdSAIDs, heldSAIDs, title, expr) {
			return ""
		}
		if c.catalogRecordsGrant(rc, createdSAIDs, title, expr) {
			return ""
		}
		if ok, why := c.reportsWriteGrant(rc, createdSAIDs, title, expr); ok {
			return ""
		} else if why != "" {
			return why
		}
		if ok, why := c.licenceWriteGrant(rc, createdSAIDs, title, expr); ok {
			return ""
		} else if why != "" {
			return why
		}
		// The catalog check job's account is admitted by this branch's row for
		// its one records grant and for nothing else, so its name under any
		// other condition is refused HERE, before the clause loop below: that
		// loop reads the condition alone, and every clause of a per-repository
		// job's condition passes it, so a grant to this account under one
		// would otherwise be covered as some repository's own.
		if c.isCatalogCheckMember(str(ch.After["member"])) {
			return "grants objectUser on the bucket to the project's catalog check job under a condition that is not one repository's builds/<slug>/catalog/ records condition of this run's bucket (gcp.CatalogRecordsCondition), which is the only objectUser the modules give it (D1148)"
		}
		// The reports job's account is admitted by the same row and for the
		// same reason: objectUser
		// under reports/ is the ONE write the modules give it, so its name
		// under a per-repository clause — runs/ included, which is the
		// launchers' own area — is refused here rather than read as some
		// repository's job grant by the clause loop below.
		if c.isReportsMember(str(ch.After["member"])) {
			return "grants objectUser on the bucket to the project's reports job under a condition that is not its reports/ condition of this run's bucket (gcp.ReportsWriteCondition), which is the only objectUser the modules give it (ruling E4)"
		}
		// The renewal job's account likewise: objectUser on the licence object
		// ALONE is the one write the module gives it (plan K4.2), so its name
		// under a per-repository clause — runs/ included — is refused before the
		// clause loop below reads it as some repository's job grant.
		if c.isLicenceMember(str(ch.After["member"])) {
			return "grants objectUser on the bucket to the installation's licence renewal job under a condition that is not the licence object's own of this run's bucket (gcp.LicenceWriteCondition), which is the only objectUser the module gives it (plan K4.2)"
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
//
// The review-only variants (design docs/cloud-senior-review, CSR7) are a
// second instance of the same module, "project_review"
// (roots/installation/main.tf), and their grants are these same two widened
// conditions for the account their own instance creates: reading them here is
// what lets the one plan fugaro update writes be covered at all, and it widens
// nothing — the second instance is the same module source, with its own
// accounts, so the account rule below still binds every grant to the account
// its own instance holds or creates. What the variant is NOT covered for is
// anything a job account may not have: it holds no secret accessor, because
// the root hands that instance no secret container.
var projectModule = regexp.MustCompile(`^module\.project(?:_review)?\["[^"]+"\]$`)

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

// catalogReadGrant reports whether a bucket grant is a build account reading
// the copied catalog definitions (gcp.CatalogReadCondition, PIC4):
// objectViewer, which the branch this is called from has already established,
// on exactly builds/catalog/ of one of this run's buckets, with the condition
// title built from the account the grant's own module holds or creates
// (gcp.CatalogReadConditionTitle) — the account rule the tokens/ grants pass
// (jobAccountGrant), or, since B6, the project's build identity
// (buildIdentityGrantAccount), which reads the same copy as the account a
// catalog build now runs as.
//
// The clause has no slug, so the expression is one builder's bytes for the
// whole string: the grant the repository module writes is accepted, and any
// other clause under objects/ — or the same clause on no run bucket — is not.
func (c Cover) catalogReadGrant(rc ResourceChange, createdSAIDs, heldSAIDs map[string]bool, title, expr string) bool {
	if _, ok := c.jobAccountGrant(rc, createdSAIDs, heldSAIDs, title, gcp.CatalogReadConditionTitlePrefix); !ok &&
		!c.buildIdentityGrantAccount(rc, createdSAIDs, heldSAIDs, title, gcp.CatalogReadConditionTitlePrefix) {
		return false
	}
	bucket := str(rc.Change.After["bucket"])
	for _, b := range c.Buckets {
		if !gcp.ValidBucketName(b) || (bucket != "" && bucket != b) {
			continue
		}
		if expr == gcp.CatalogReadCondition(b) {
			return true
		}
	}
	return false
}

// reportsGrant is the account rule the reports job's two conditions share: the
// condition's title is titlePrefix + the account's ID, the account is the
// reports account itself, and the expression is the builder's bytes for one of
// this run's buckets. It returns whether the grant passed and, when the title
// claims to be one of the reports ones and the grant is not, why — a sentence
// the caller uses instead of the generic refusal, since the title says which
// grant the plan meant to write.
//
// The member rule is catalogRecordsGrant's: the account named outright on the
// plan that carries it after a first deploy, or a computed member that the
// grant's own module creates in this plan — and it has to be THIS account,
// since the title is built from it. The account's ID is fixed (the module's
// variable validation pins it), so unlike a job account's there is nothing to
// derive from the member. The title is what says whose grant it is: the two
// expressions name no prefix of their own beyond the objects path, so without
// it any member could hold one of these two conditions and be covered.
//
// The bucket loop is the other half: the same bytes on another bucket are a
// grant the modules never write, and a grant that carries no bucket of its own
// yet (a computed one) is accepted only for a bucket of this run.
func (c Cover) reportsGrant(rc ResourceChange, createdSAIDs map[string]bool, title, expr, titlePrefix string, build func(bucket string) string) (bool, string) {
	id, ok := strings.CutPrefix(title, titlePrefix)
	if !ok {
		return false, ""
	}
	if id != ReportsAccountID {
		return false, "grants a bucket role under the reports job's condition to the account " + id + ", which is not the project's reports account (" + ReportsAccountID + ")"
	}
	if !c.isReportsMember(str(rc.Change.After["member"])) &&
		(!unknown(rc.Change, "member") || !createdSAIDs[modulePrefix(rc)+"|"+ReportsAccountID]) {
		return false, "grants a bucket role under the reports job's condition to a member that is not the reports account its title names, which the modules never do"
	}
	bucket := str(rc.Change.After["bucket"])
	for _, b := range c.Buckets {
		if !gcp.ValidBucketName(b) || (bucket != "" && bucket != b) {
			continue
		}
		if expr == build(b) {
			return true, ""
		}
	}
	return false, "grants a bucket role under the reports job's condition with an expression that is not the reports condition of this run's bucket (" + titlePrefix + ReportsAccountID + ")"
}

// reportsReadGrant reports whether a bucket grant is the reports job reading
// runs/ (gcp.ReportsReadCondition, slice P): objectViewer, which the branch
// this is called from has already established, on exactly runs/ of one of this
// run's buckets, with the condition title built from the reports account.
func (c Cover) reportsReadGrant(rc ResourceChange, createdSAIDs map[string]bool, title, expr string) (bool, string) {
	return c.reportsGrant(rc, createdSAIDs, title, expr, gcp.ReportsReadConditionTitlePrefix, gcp.ReportsReadCondition)
}

// reportsWriteGrant is reportsReadGrant for the job's other binding: objectUser
// on exactly reports/ (gcp.ReportsWriteCondition) — the write the job alone
// holds, and the one row this account may reach beyond the reader's.
func (c Cover) reportsWriteGrant(rc ResourceChange, createdSAIDs map[string]bool, title, expr string) (bool, string) {
	return c.reportsGrant(rc, createdSAIDs, title, expr, gcp.ReportsWriteConditionTitlePrefix, gcp.ReportsWriteCondition)
}

// licenceGrant is the account rule the licence renewal job's two bucket
// conditions share (plan K4.2): the condition's title is titlePrefix + the
// account's ID, the account is the installation's licence account itself, and
// the expression is the builder's bytes for one of this run's buckets. It
// returns whether the grant passed and, when the title claims to be one of the
// licence ones and the grant is not, why — the sentence the caller uses
// instead of the generic refusal, since the title says which grant the plan
// meant to write.
//
// The member rule and the bucket loop are reportsGrant's, for the same two
// reasons: the account has a fixed ID (the module's variable validation pins
// it, LicenceAccountID), so nothing is derived from the member and the TITLE
// is what says whose grant it is; and the same bytes on another bucket are a
// grant no module writes.
func (c Cover) licenceGrant(rc ResourceChange, createdSAIDs map[string]bool, title, expr, titlePrefix string, build func(bucket string) string) (bool, string) {
	id, ok := strings.CutPrefix(title, titlePrefix)
	if !ok {
		return false, ""
	}
	if id != LicenceAccountID {
		return false, "grants a bucket role under the licence renewal job's condition to the account " + id + ", which is not the installation's licence account (" + LicenceAccountID + ")"
	}
	if !c.isLicenceMember(str(rc.Change.After["member"])) &&
		(!unknown(rc.Change, "member") || !createdSAIDs[modulePrefix(rc)+"|"+LicenceAccountID]) {
		return false, "grants a bucket role under the licence renewal job's condition to a member that is not the licence account its title names, which the modules never do"
	}
	bucket := str(rc.Change.After["bucket"])
	for _, b := range c.Buckets {
		if !gcp.ValidBucketName(b) || (bucket != "" && bucket != b) {
			continue
		}
		if expr == build(b) {
			return true, ""
		}
	}
	return false, "grants a bucket role under the licence renewal job's condition with an expression that is not the licence condition of this run's bucket (" + titlePrefix + LicenceAccountID + ")"
}

// licenceReadGrant reports whether a bucket grant is the renewal job reading
// the three objects it finds its installation and its key by
// (gcp.LicenceReadCondition, plan K4.2): objectViewer, which the branch this is
// called from has already established, on exactly the project marker, the
// published config and the licence object of one of this run's buckets, with
// the condition title built from the licence account.
//
// It is THREE names and not one, and that is the grant's whole design: the job
// runs in a container with no checkout and no config file, so the marker and
// the published config are how it learns which installation it renews for
// (internal/cli's selectWith). The read carries no listing half: a List
// request names no object, so no clause of a name condition matches one, and
// the account stays out of the run records, the prompts and the transcripts
// the bucket holds.
func (c Cover) licenceReadGrant(rc ResourceChange, createdSAIDs map[string]bool, title, expr string) (bool, string) {
	return c.licenceGrant(rc, createdSAIDs, title, expr, gcp.LicenceReadConditionTitlePrefix, gcp.LicenceReadCondition)
}

// licenceWriteGrant is licenceReadGrant for the job's other binding: objectUser
// on the licence object ALONE (gcp.LicenceWriteCondition) — the one write the
// account holds, and the whole reason it is objectUser rather than
// objectCreator (the write replaces the object the job read, and the bucket is
// versioned).
func (c Cover) licenceWriteGrant(rc ResourceChange, createdSAIDs map[string]bool, title, expr string) (bool, string) {
	return c.licenceGrant(rc, createdSAIDs, title, expr, gcp.LicenceWriteConditionTitlePrefix, gcp.LicenceWriteCondition)
}

// licenceSecretGrant is the narrowing the renewal job's secret binding has and
// no other secret grant in this file does: WHICH container it is on. The role
// is the row's own (customKey + LicenceRenewalRole, checked before this is
// called), and what is left is the target — the container the run's own
// licence spec derived from the Fugaro project's name
// (infra.LicenceRenewalSecretID, carried into the review as
// Cover.LicenceRenewalSecret).
//
// Why this one and not the row's others: four Secret Manager calls are a read
// of a VALUE — `fugaro licence set` writes the per-key renewal secret into it —
// where the row's other grants are reads of a key the job already holds. A
// hand-edited plan that pointed this binding at another container of the
// project would hand the renewal job the credentials of some other part of the
// installation, and the target is the only thing left that says otherwise.
//
// The container may be unknown, and then the grant is refused: the ID is a
// derived literal (gcp.ProjectSecretID), never a computed attribute of a
// resource in this plan, so "unknown" means the plan did not take it from the
// spec. A cover with no container in it (LicenceRenewalSecret empty — a caller
// that had none to name) has nothing to compare with, and every binding to this
// account is refused.
func (c Cover) licenceSecretGrant(rc ResourceChange) string {
	if unknown(rc.Change, "secret_id") {
		return "grants the renewal role on a secret whose secret_id is not known: it is the container the installation's own licence spec derives (internal/infra.LicenceRenewalSecretID), which the plan carries as a literal"
	}
	if id := str(rc.Change.After["secret_id"]); c.LicenceRenewalSecret == "" || id != c.LicenceRenewalSecret {
		return "grants the renewal role on the secret " + safetext.Field(id) + ", which is not this installation's renewal container (" + safetext.Field(c.LicenceRenewalSecret) + "): the four calls are the renewal job's whole Secret Manager reach (plan K4.2)"
	}
	conds, why := grantConditions(rc.Change, "a secret grant")
	switch {
	case why != "":
		return why
	case len(conds) != 0:
		return "grants the renewal role on the renewal container under a condition: the container is the fence, and the module never adds one (plan K4.2)"
	}
	return ""
}

// catalogRecordsGrant reports whether a bucket grant is the project's catalog
// check job holding ONE repository's build records and check state
// (gcp.CatalogRecordsCondition, D1148): objectUser, which the branch this is
// called from has already established, on exactly builds/<slug>/catalog/ of one
// of this run's buckets, under the condition title built from the same slug
// (gcp.CatalogRecordsConditionTitle) — the one per-repository condition the
// module takes from its input because it is a repository's.
//
// The slug comes from the TITLE and the expression is then compared with the
// builder's own bytes for that slug, which is what makes the pair the module's:
// the clause shape carries no slug of its own (it is a whole-prefix clause),
// and a grant retitled for another repository, or carrying those bytes on a
// bucket of another run, is refused rather than read as this one. The account
// rule is jobAccountGrant's — the catalog check account, named outright on the
// plan that ADDS a repository to an installation that already has the job, or
// computed from the account the grant's own module creates on the plan that
// first deploys it. What differs from jobAccountGrant is what the title
// carries: a repository's slug there, an account ID in the tokens/ and
// catalog-read grants, so a grant cannot be read as the other grant's by
// retitling it.
func (c Cover) catalogRecordsGrant(rc ResourceChange, createdSAIDs map[string]bool, title, expr string) bool {
	slug, ok := strings.CutPrefix(title, gcp.CatalogRecordsConditionTitlePrefix)
	if !ok || !conditionSlug.MatchString(slug) {
		return false
	}
	// The member: the catalog check account itself, named outright on the plan
	// that adds a repository to an installation that already has the job, or
	// computed from an account the grant's own module creates in this plan —
	// which has to be this account, or the plan says nothing about whose grant
	// this is. A named member needs no further rule than the account's own
	// name: the ID and the project name it, and no other account of a run
	// carries either.
	//
	// Since B6 the project's build identity is the second account this grant is
	// written for: a catalog build runs as the identity and writes the record
	// the check reads back, so the same condition is bound to it, one grant per
	// repository (modules/installation/build_identity.tf) — the account rule is
	// the check's own, keyed on the identity's literal ID.
	if !c.isCatalogCheckMember(str(rc.Change.After["member"])) && !c.isBuildIdentityMember(str(rc.Change.After["member"])) {
		if !unknown(rc.Change, "member") {
			return false
		}
		mod := modulePrefix(rc)
		if !createdSAIDs[mod+"|"+CatalogCheckAccountID] && !createdSAIDs[mod+"|"+BuildIdentityAccountID] {
			return false
		}
	}
	bucket := str(rc.Change.After["bucket"])
	for _, b := range c.Buckets {
		if !gcp.ValidBucketName(b) || (bucket != "" && bucket != b) {
			continue
		}
		if expr == gcp.CatalogRecordsCondition(b, slug) {
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

// buildIdentityGrantAccount is jobAccountGrant's account rule for the
// conditioned grants written for the project's build identity (B6, D2301)
// instead of a job or build account: the condition's title is the prefix its
// own builder writes plus that account's ID, and the member is that account,
// held or created by the grant's own module. Its ID is a literal rather than
// derived, so unlike a job account's there is nothing to read out of the
// member: the title is the whole of what says whose grant this is, and the
// member then has to be the account the title names.
func (c Cover) buildIdentityGrantAccount(rc ResourceChange, createdSAIDs, heldSAIDs map[string]bool, title, titlePrefix string) bool {
	if title != titlePrefix+BuildIdentityAccountID {
		return false
	}
	mod := modulePrefix(rc) + "|" + BuildIdentityAccountID
	if c.isBuildIdentityMember(str(rc.Change.After["member"])) {
		return heldSAIDs[mod] || createdSAIDs[mod]
	}
	// The computed member the module writes
	// (google_service_account.build_identity[0].member): the plan has to be
	// the one that creates the account the title is built from.
	return unknown(rc.Change, "member") && createdSAIDs[mod]
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
func (c Cover) attributes(rc ResourceChange, creates, createdSAIDs, heldSAIDs map[string]bool, creds serviceAccountsPlan) string {
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
			// The store D935 gives the front door is this run's own bucket
			// too, but it is not in c.Buckets — the review screen lists the
			// buckets the installation's people and jobs already use, and the
			// store is the front door's alone — so it is admitted here for the
			// one grant that names it (mcpauthStoreGrant, which reads the
			// member and the role as well) and for nothing else.
			if !slices.Contains(c.Buckets, b) && c.mcpauthStoreGrant(rc) != "" {
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
	case "google_cloud_run_v2_service":
		// The installation module's five services are five shapes, and which
		// rules a change is held to is the resource the plan names and nothing
		// an editor of the variables can reach: intake.tf hardcodes the
		// intake's account, its args and its environment names, so the change
		// at that address is the intake's, orchestrator.tf does the same for
		// the resident orchestrator's (P5.1c), mobile_service.tf for the public
		// mobile service's (P5), and everything else falls to the credentials
		// service's case below — which is what refuses a Cloud Run service this
		// module does not create.
		switch cloudRunServiceName(rc) {
		case intakeServiceResource:
			return c.intakeServiceChange(rc, a, rc.Change.AfterUnknown, pre, creds, proj)
		case orchestratorServiceResource:
			return c.orchestratorServiceChange(rc, a, rc.Change.AfterUnknown, pre, creds, proj)
		case mobileServiceResource:
			return c.mobileServiceChange(rc, a, rc.Change.AfterUnknown, pre, creds, proj)
		}
		// ... and the voice hop (P5.3.3), the third subcommand of the same
		// image: voice.tf hardcodes the hop's account, its args, its two
		// environment names and its zero-minimum scaling, so the change at that
		// address is the hop's.
		if cloudRunServiceName(rc) == voiceServiceResource {
			return c.voiceServiceChange(rc, a, rc.Change.AfterUnknown, pre, creds, proj)
		}
		// The credentials service (D146, C1.18): the one container image in
		// the tree that is not a job's. Its container sits one `template`
		// shorter than a job's (template[0].containers[0]), and it is held to
		// the released pin — without this case init's one typed confirmation
		// would cover a plan whose service runs whatever
		// var.credentials_service.image names, so a hand-edited or stale
		// terraform.tfvars.json could point it at any registry, owner and
		// digest, and the apply would run that image as the account that reads
		// every model credential in the bundle (independent security review of
		// this file, which found the case missing).
		//
		// This case is STRICTER than a job's in four ways, because of what that
		// account is, and every one of them is a rule the modules already write
		// (so no plan they produce is refused):
		//
		//   - the image must be the base pin's closed shape through the
		//     pull-through repository, with the recorded owner
		//     (credentialsImageOK), and not merely a reference under this run's
		//     registry as a job's may be: modules/installation/iam.tf grants
		//     roles/artifactregistry.writer on the run's own repositories to
		//     EVERY operator, so bytes an operator can push and read a digest
		//     back from are bytes an operator can name here — an @sha256 rule
		//     alone forbids rewriting a pin without forbidding choosing one
		//     (independent security review of this file, which found the gap:
		//     baseImageOK admits <registry>/<project>/fugaro-base/…, which is
		//     exactly the operator-writable repository, and D145/M1 is lost
		//     with it);
		//   - no command and no args: credentials.tf sets neither, so the
		//     image's own entrypoint is the service (C1.14), and a hand edit
		//     adding `command = ["/bin/sh","-c",…]` would run something the
		//     module does not write under init's one confirmation (the same
		//     check the job case makes, with the module's pair rather than a
		//     table of them);
		//   - the environment is the module's own names (credentialsServiceEnv),
		//     with literal values
		//     and no value_source: an environment is the one path a credential
		//     could reach this process without a Secret Manager read, and a
		//     value_source on an allow-listed name is that read (independent
		//     security review of this file, which found both unchecked). The
		//     rule is the value_source block's CONTENTS and not the key's
		//     presence, because Terraform renders an unset nested block of list
		//     nesting mode as an empty list rather than omitting it: every env
		//     entry of a plan the module writes carries `"value_source": []`
		//     (testdata/golden/*.plan.json), so a test on the key alone refuses
		//     the module's own literals; the check below reads the contents,
		//     and the row pinned to that shape fails if it does not;
		//   - the account it runs as is the plan's own
		//     google_service_account.credentials[0] — that resource, and not
		//     any other account of the module: credentials.tf writes that
		//     resource's email there, and an unchecked
		//     template.service_account is a hand edit that runs the service's
		//     image as somebody else's identity (independent security review of
		//     this file; the review round found that the wider check, "an
		//     account of this module", is satisfied by the siblings the module
		//     also creates — serviceAccount, below).
		if unknown(rc.Change, "invoker_iam_disabled") {
			return "an IAM setting that is not known"
		}
		if a["invoker_iam_disabled"] != false {
			return "a service that does not require IAM, which would let anyone invoke the account that reads every model credential in the bundle"
		}
		cs := objects(at(a["template"], "containers"))
		unk := rc.Change.AfterUnknown
		if unknownAt(unk, "template", 0, "containers") {
			return "containers that are not known"
		}
		if len(cs) == 0 {
			return "no container"
		}
		if why := c.serviceAccount(a, unk, pre, creds); why != "" {
			return why
		}
		for i, m := range cs {
			cp := []any{"template", 0, "containers", i}
			for _, attr := range []string{"image", "command", "args"} {
				if unknownAt(unk, append(slices.Clone(cp), attr)...) {
					return "a container " + attr + " that is not known"
				}
			}
			img := str(m["image"])
			if !c.credentialsImageOK(img, proj) {
				return fmt.Sprintf("runs the image %q, which is not this installation's released pin read back through the pull-through repository (%s/%s/%s/%s/fugaro-<kind>:<version>@sha256:<digest>): this account reads every model credential in the bundle, and an image under this run's own repositories is one every operator may push to (iam.tf), so only the recorded owner's pin is covered",
					img, c.Registry, proj, c.Ghcr, c.Owner)
			}
			// The module sets neither, and they are held the same way a job's
			// are (credentials.tf: the image's own entrypoint is the service,
			// C1.14) — so an override that reached this layer would be a
			// hand-edited or stale tfvars running something else as the
			// account that reads every credential. A job's container may name
			// one of knownCommands; this one may name nothing at all.
			if cmd, args := strList(m["command"]), strList(m["args"]); len(cmd) != 0 || len(args) != 0 {
				return "a container that sets a command or args, which the module does not: the image's own entrypoint is the service"
			}
			if unknownAt(unk, append(slices.Clone(cp), "env")...) {
				return "an environment that is not known"
			}
			for j, e := range objects(m["env"]) {
				ep := append(slices.Clone(cp), "env", j)
				if unknownAt(unk, append(slices.Clone(ep), "name")...) || unknownAt(unk, append(slices.Clone(ep), "value")...) ||
					unknownAt(unk, append(slices.Clone(ep), "value_source")...) {
					return "an environment variable that is not known"
				}
				if n := str(e["name"]); !slices.Contains(credentialsServiceEnv, n) {
					return fmt.Sprintf("an environment variable %q, which the credentials service does not read: its container sets only %s",
						n, strings.Join(credentialsServiceEnv, ", "))
				}
				// A name off the allowlist is not the whole of it: an entry
				// that takes its value from a source (a secret_key_ref or the
				// like) carries the module's name and a value the module never
				// set, which is exactly the path a credential reaches this
				// process without a Secret Manager read. The module sets a
				// literal for each of the seven and nothing else.
				//
				// The test is the block's contents, not the key's presence.
				// `value_source` is a nested block of list nesting mode, and
				// Terraform renders an unset one as an empty list rather than
				// omitting it: every env entry of a plan the module writes
				// carries `"value_source": []` (testdata/golden/*.plan.json),
				// so a nil test on the interface refuses the module's own
				// literals. objects([]any{}) is nil, so this admits that
				// shape and still refuses a real secret_key_ref.
				if refs := objects(e["value_source"]); len(refs) > 0 {
					return "an environment variable whose value comes from a source rather than the module: this service's environment is the module's own names and nothing else"
				}
			}
		}
	case "google_cloud_scheduler_job":
		ht := at(a["http_target"])
		// The token block says which kind of target this is, and the two are
		// not interchangeable: a run API job URI is called with an OAuth access
		// token and a run.app service URL with an OIDC ID token, so a job
		// carrying the wrong one authenticates against nothing. The resident
		// orchestrator's tick is the tree's one job of the second kind
		// (P5.1c), and it is checked by rules of its own rather than by the
		// run API's.
		if len(objects(at(ht, "oidc_token"))) > 0 {
			return c.orchestratorTickChange(rc, a, rc.Change.AfterUnknown, pre, creates)
		}
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

// The two environment variables a runs bucket reaches a job as, each in the
// form its writer gives it: FUGARO_BUCKET, which infra's BucketEnv names and
// platformEnvFor writes for the check jobs (repo and catalog) and the workflow
// jobs alike, and FUGARO_RUNS_BUCKET, which the installation's history and
// coordinator sweeps read the runs' result.json objects through. This package
// cannot import either writer (infra and cli both import it), so the names are
// literals here as FUGARO_CHECK_SPEC's is, and cover_test.go pins each to its
// writer's own declaration.
const (
	bucketEnv     = "FUGARO_BUCKET"
	runsBucketEnv = "FUGARO_RUNS_BUCKET"
)

// bucketValue is what the variable env carries for one of this run's buckets:
// the whole object URL for FUGARO_BUCKET's writer, and the bare name for
// FUGARO_RUNS_BUCKET's, which history.tf and coordinator.tf pass
// var.runs_bucket in.
func bucketValue(env, bucket string) string {
	if env == runsBucketEnv {
		return bucket
	}
	return "gs://" + bucket
}

// bucketEnvOK reports whether v is one of this run's buckets in the form env
// carries it: the literal bucketValue(env, bucket). A value that is not known is
// refused by the caller, as checkSpecs refuses the check specs' unknown values.
func (c Cover) bucketEnvOK(env, v string) bool {
	for _, b := range c.Buckets {
		if v == bucketValue(env, b) {
			return true
		}
	}
	return false
}

// bucketValues is this run's buckets in that same form, joined, for a refusal
// that says what it wanted.
func (c Cover) bucketValues(env string) string {
	out := make([]string, 0, len(c.Buckets))
	for _, b := range c.Buckets {
		out = append(out, bucketValue(env, b))
	}
	return strings.Join(out, ", ")
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

// catalogCheckSpec is the part of the catalog check job's
// FUGARO_CATALOG_CHECK_SPEC that points at registries and accounts: the base
// images its builds start FROM and the account each repository's build runs
// as. It decodes as the job does (internal/cli's catalogCheckSpec reader; infra.CatalogCheckJobSpec is the writer, with the same tags).
type catalogCheckSpec struct {
	BaseImages   map[string]string `json:"base_images"`
	Repositories []struct {
		BuildServiceAccount string `json:"build_service_account"`
	} `json:"repositories"`
}

// checkCatalogSpec holds the catalog check job's document to what
// checkSpecs holds a repository check's: every base image in this run's
// registry and every build account one of this run's Fugaro accounts.
func (c Cover) checkCatalogSpec(value, proj, prefix string) string {
	var s catalogCheckSpec
	if err := json.Unmarshal([]byte(value), &s); err != nil {
		return "a catalog check spec that is not the JSON the catalog check job reads"
	}
	for _, k := range slices.Sorted(maps.Keys(s.BaseImages)) {
		if ref := s.BaseImages[k]; !c.baseImageOK(ref, proj) {
			return fmt.Sprintf("a catalog check spec whose base image %s is %q, outside this run's %s", k, ref, prefix)
		}
	}
	for _, r := range s.Repositories {
		if !c.buildAccount(r.BuildServiceAccount) {
			return fmt.Sprintf("a catalog check spec whose build service account is %q, not a Fugaro service account of this run", r.BuildServiceAccount)
		}
	}
	return ""
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
// A job's image is the loose half on purpose: a repository under this run's
// registry is what the run's own build pushed there. The credentials
// service's image is not — credentialsImageOK below is the strict half, and
// it is the one that account is held to.
//
// What this closes is bounded, and the bound is design §10.7's: the owner
// and the entry it is checked against can be published in the same shared
// config, so against whoever writes that document this is a *consistency*
// check, not a provenance guarantee. What it does close is real: a foreign
// owner, a bare tag, a missing digest or any other shape in an entry whose
// config records our owner — a hand-edited config, a stale entry, an
// accidental paste.
func (c Cover) baseImageOK(ref, proj string) bool {
	if !c.refOK(ref, proj) {
		return false
	}
	if c.Ghcr == "" || !strings.HasPrefix(ref, c.registryPrefix(proj)+c.Ghcr+"/") {
		return true
	}
	return c.pinOK(ref, proj)
}

// credentialsImageOK reports whether ref may be the credentials service's
// image (D146, C1.18): the base pin's closed shape through the pull-through
// repository and NOTHING else — the recorded owner, a fugaro-<kind> image, a
// release version tag and a digest:
//
//	<host>/<project>/<ghcr>/<owner>/fugaro-<kind>:<version>@sha256:<64 hex>
//
// baseImageOK, which a job's image is held to, is deliberately looser: it
// also admits any reference under the run's own repositories. That half is
// the hole for THIS image. modules/installation/iam.tf grants
// roles/artifactregistry.writer on the run's base repository to every
// operator, so an operator can push bytes there, read the digest back and
// name it in var.credentials_service.image — and the apply then runs their
// code as the account that reads every model credential in the bundle,
// which is the boundary D145 and credentials.tf's M1 comment exist to keep.
// The @sha256 rule forbids REWRITING a pin; it does not forbid CHOOSING one,
// so the pin's owner is what has to be checked (independent security review
// of this file, which found the gap; baseImageOK is unchanged for jobs,
// whose image is the run's own build). An empty repository ID, owner or
// registry refuses every reference.
//
// The residual, stated rather than implied: a deployment that mirrors the
// released image into its own repository is refused here rather than
// covered, and init asks for its own typed confirmation instead. That is the
// fail-closed direction, and it is the only shape the module produces — C1.20
// fills this variable with the released image read back through the proxy.
func (c Cover) credentialsImageOK(ref, proj string) bool {
	if c.Ghcr == "" || !c.refOK(ref, proj) || !strings.HasPrefix(ref, c.registryPrefix(proj)+c.Ghcr+"/") {
		return false
	}
	return c.pinOK(ref, proj)
}

// registryPrefix is this run's registry and project: the prefix both image
// checks read a reference against.
func (c Cover) registryPrefix(proj string) string { return c.Registry + "/" + proj + "/" }

// refOK is the part both image checks share: a reference under this run's
// registry, with the characters a reference may hold here and no ".."
// segment. It says nothing about the repository or the owner.
func (c Cover) refOK(ref, proj string) bool {
	prefix := c.registryPrefix(proj)
	return c.Registry != "" && strings.HasPrefix(ref, prefix) && len(ref) > len(prefix) &&
		refChars.MatchString(ref) && !slices.Contains(strings.Split(ref, "/"), "..")
}

// pinOK reports whether ref is the base pin's closed shape under the
// pull-through repository. The caller has already established that ref sits
// there (refOK and the repository's ID); this reads the rest: the recorded
// owner, a fugaro-<kind> image, a release version tag and a digest.
func (c Cover) pinOK(ref, proj string) bool {
	owner, rest, ok := strings.Cut(strings.TrimPrefix(ref, c.registryPrefix(proj)+c.Ghcr+"/"), "/")
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

// serviceAccount checks the account a covered credentials-service template
// names: the plan's own google_service_account.credentials[0], in this run's
// project and in the service's module. credentials.tf writes that resource's
// email there, and the email is computed while the account is being created,
// so an unknown value is the module's own only when this plan creates that
// resource; a known value must be that resource's account ID — the account the
// plan creates or holds under it. Anything else is a hand edit that would run
// the service's image — the one account that reads every model credential in
// the bundle — as an identity of the editor's choosing (independent security
// review of this file).
//
// The account is named by the resource and not by "an account of this module"
// (the review round that found this): the module also creates the deployer,
// the history and the scheduler accounts, and a template naming one of those
// is not the module's plan — it runs the bundle-reading image as somebody
// else's identity — while the unknown-email arm, which only required that the
// module create *some* account, was satisfied by all three of them.
func (c Cover) serviceAccount(a map[string]any, unk any, pre string, plans serviceAccountsPlan) string {
	return c.accountFrom(a, unk, pre, plans.credentials, "the credentials service", credentialsAccountResource)
}

// intakeAccount is serviceAccount's check on the other service of the same
// module (DS2.2, DS4.1): the plan's own google_service_account.intake[0],
// which intake.tf writes into the template and which the same rules hold.
//
// It is a check of its own rather than the credentials service's with another
// message, because the two accounts are two: admitting the credentials
// account here would run the intake's container as the identity that reads
// every model credential in the bundle, and admitting the intake's there would
// run the credentials service as an account whose whole reach is two
// conditioned secret roles and one object read.
func (c Cover) intakeAccount(a map[string]any, unk any, pre string, plans serviceAccountsPlan) string {
	return c.accountFrom(a, unk, pre, plans.intake, "the secrets intake", intakeAccountResource)
}

// voiceAccount is serviceAccount's check on the third service of the same
// module (P5.3.3): the plan's own google_service_account.voice[0], which
// voice.tf writes into the template and which the same rules hold.
//
// It is a check of its own for the same reason the intake's is, and its stakes
// are its own: the hop's service is the one anyone on the
// internet may call (voice.tf's allUsers binding), so a plan that ran its
// container as the credentials service's account would put every model
// credential of the bundle behind a public endpoint whose handler verifies a
// phone's token and nothing else.
func (c Cover) voiceAccount(a map[string]any, unk any, pre string, plans serviceAccountsPlan) string {
	return c.accountFrom(a, unk, pre, plans.voice, "the voice hop", voiceAccountResource)
}

// mobileAccount is the same check on the fifth service of the module (mobile
// plan task P5): the plan's own google_service_account.mobile[0], which
// mobile_service.tf writes into the template and which the same rules hold.
//
// It is a check of its own for intakeAccount's reason, and its stakes are the
// hop's and one more: this identity holds iam.serviceAccounts.signJwt on the
// devices' signer, so a plan that ran the credentials service's container as
// it would put the reach of minting an identity for any uid behind the private
// service's own routes, and one that ran this container as the credentials
// account would put the bundle's read behind a URL anyone may call. The two
// services of the same image therefore may not swap identities, which is the
// whole of what this check is.
func (c Cover) mobileAccount(a map[string]any, unk any, pre string, plans serviceAccountsPlan) string {
	return c.accountFrom(a, unk, pre, plans.mobile, "the public mobile service", mobileAccountResource)
}

// orchestratorAccount is the same check on the fourth service of the module
// (mobile plan task P5.1c): the plan's own google_service_account.
// orchestrator[0], which orchestrator.tf writes into the template and which the
// same rules hold.
//
// It is a check of its own for intakeAccount's reason one account over: the
// orchestrator's identity is the one that holds iam.serviceAccounts.signJwt on
// the installation's budget signer (section 4.3's first row, section 4.5 E4 —
// "may mint" is also "may be the phone"), so admitting either of the module's
// other two service accounts here would run this process as an identity the
// design's table never priced, and admitting this account at the credentials
// service's address would give the process that reads every model credential in
// the bundle the signJwt reach too.
func (c Cover) orchestratorAccount(a map[string]any, unk any, pre string, plans serviceAccountsPlan) string {
	return c.accountFrom(a, unk, pre, plans.orchestrator, "the resident orchestrator", orchestratorAccountResource)
}

// accountFrom is the check both of the above are: the account a covered
// service's template names must be the plan's own resource named resource, in
// this run's project and in the service's module, and the plan must create or
// hold it (the whole of the reasoning is serviceAccount's comment above).
func (c Cover) accountFrom(a map[string]any, unk any, pre string, plan accountPlan, what, resource string) string {
	if unknownAt(unk, "template", 0, "service_account") {
		if !plan.created[pre] {
			return "runs as an account that is not known and this plan creates none for it"
		}
		return ""
	}
	email := str(at(a["template"], "service_account"))
	sm := saMember.FindStringSubmatch("serviceAccount:" + email)
	id, known := plan.id[pre]
	if sm == nil || !slices.Contains(c.Projects, sm[2]) || !known || id != sm[1] || !(plan.created[pre] || plan.held[pre]) {
		return "runs as " + email + ", not the account this module creates for " + what + " (google_service_account." + resource + "[0])"
	}
	return ""
}

// intakeServiceChange holds the secrets intake's own service (DS2.2, DS4.1) to
// the same four rules the credentials service's change is held to, and the
// whole of the reasoning is that case's comment. Two of the four are the
// intake's own, and they are why the branch exists:
//
//   - the argv. The intake runs the credentials service's own image — the
//     released binary, one pin — and that image's default command is
//     `credentials serve`, so the module tells the two services apart by
//     `args = ["intake", "serve"]` (intake.tf). A change whose args are not
//     exactly that pair is refused in both directions: none would serve the
//     credentials service's routes from the intake's own URL, as the account
//     that writes this installation's dynamic secrets, and anything else would
//     run code the module does not write. The command is still empty, as the
//     credentials service's is: the image's entrypoint is the binary.
//   - the account, which is the intake's own google_service_account.intake[0]
//     (intakeAccount) and never its sibling's: the two services may not swap
//     identities, or the intake would run as the account that reads every model
//     credential in the bundle — the read its own two roles deliberately do not
//     hold (R3, F30).
//
// The environment is the five names the process reads (intakeServiceEnv), held
// to literal values and no value_source for the credentials service's reason:
// an environment is the one path a value could reach this process without a
// Secret Manager read, and this process writes containers it never reads.
func (c Cover) intakeServiceChange(rc ResourceChange, a map[string]any, unk any, pre string, plans serviceAccountsPlan, proj string) string {
	if unknown(rc.Change, "invoker_iam_disabled") {
		return "an IAM setting that is not known"
	}
	if a["invoker_iam_disabled"] != false {
		return "a service that does not require IAM, which would let anyone invoke the account that writes this installation's dynamic secrets"
	}
	cs := objects(at(a["template"], "containers"))
	if unknownAt(unk, "template", 0, "containers") {
		return "containers that are not known"
	}
	if len(cs) == 0 {
		return "no container"
	}
	if why := c.intakeAccount(a, unk, pre, plans); why != "" {
		return why
	}
	for i, m := range cs {
		cp := []any{"template", 0, "containers", i}
		for _, attr := range []string{"image", "command", "args"} {
			if unknownAt(unk, append(slices.Clone(cp), attr)...) {
				return "a container " + attr + " that is not known"
			}
		}
		img := str(m["image"])
		if !c.credentialsImageOK(img, proj) {
			return fmt.Sprintf("runs the image %q, which is not this installation's released pin read back through the pull-through repository (%s/%s/%s/%s/fugaro-<kind>:<version>@sha256:<digest>): the intake is a subcommand of the released binary, so it runs the credentials service's own pin and never an image of its own",
				img, c.Registry, proj, c.Ghcr, c.Owner)
		}
		cmd, args := strList(m["command"]), strList(m["args"])
		if len(cmd) != 0 || !slices.Equal(args, intakeServiceArgs) {
			return "a container whose command or args are not the module's: the intake runs the released image with no command of its own and args " +
				strings.Join(intakeServiceArgs, " ") + ", and a change that named anything else would either serve the credentials service's routes here or run code intake.tf does not write"
		}
		if unknownAt(unk, append(slices.Clone(cp), "env")...) {
			return "an environment that is not known"
		}
		for j, e := range objects(m["env"]) {
			ep := append(slices.Clone(cp), "env", j)
			if unknownAt(unk, append(slices.Clone(ep), "name")...) || unknownAt(unk, append(slices.Clone(ep), "value")...) ||
				unknownAt(unk, append(slices.Clone(ep), "value_source")...) {
				return "an environment variable that is not known"
			}
			if n := str(e["name"]); !slices.Contains(intakeServiceEnv, n) {
				return fmt.Sprintf("an environment variable %q, which the secrets intake does not read: its container sets only %s",
					n, strings.Join(intakeServiceEnv, ", "))
			}
			if refs := objects(e["value_source"]); len(refs) > 0 {
				return "an environment variable whose value comes from a source rather than the module: this service's environment is the module's own names and nothing else"
			}
		}
	}
	return ""
}

// voiceServiceChange holds the voice hop's own service (P5.3.3) to the same
// four rules the credentials service's and the intake's changes are held to,
// and the whole of the reasoning is that case's comment. Three of its rules are
// the hop's own, and they are why the branch exists:
//
//   - the argv. The hop runs the credentials service's own image — the released
//     binary, one pin — and that image's default command is `credentials
//     serve`, so voice.tf tells the four services apart by `args = ["voice",
//     "serve"]`. A change whose args are not exactly that pair is refused in
//     both directions: none would serve the credentials service's routes from
//     the hop's own ADDRESS, as the account that reads the whole database
//     instance and the bundle beside it, and anything else would run code the
//     module does not write. The command is still empty, as the other two
//     services': the image's entrypoint is the binary.
//   - the account, which is the hop's own google_service_account.voice[0]
//     (voiceAccount) and never a sibling's, for the reason voiceAccount
//     states: this is the one service of the module's four anyone may call.
//   - the two scaling blocks. The hop is the one service of the tree that must
//     stay at ZERO minimum instances: it has no warm path worth paying for (a
//     phone calls it when it speaks, and a cold start is a second), and a
//     public endpoint with a warm instance is a public endpoint with a monthly
//     bill attached whether or not anyone calls it. voice.tf writes the count
//     at BOTH levels because the provider computes one of them otherwise (the
//     service-level block's), which is why both are read here.
//
// The environment is the three names the process reads (voiceServiceEnv), held
// to literal values and no value_source for the credentials service's reason:
// the environment is the one path a value could reach this process without a
// Secret Manager read — and this process is reachable by anyone, so an entry
// the module did not write is one an unauthenticated caller's request would run
// with.
func (c Cover) voiceServiceChange(rc ResourceChange, a map[string]any, unk any, pre string, plans serviceAccountsPlan, proj string) string {
	if unknown(rc.Change, "invoker_iam_disabled") {
		return "an IAM setting that is not known"
	}
	// The flag is checked rather than merely required to be literal: with the
	// IAM check off the service would be reachable without the plan's own
	// allUsers member — that is, with no resource a reviewer reads — which is
	// the shape this tree refuses everywhere (voice.tf states it).
	if a["invoker_iam_disabled"] != false {
		return "a service that does not require IAM, which would make the hop public through a flag the plan's review never reads instead of through the binding voice.tf writes"
	}
	if why := c.publicServiceMinInstances(a, unk, rc.Address, "the voice hop", "voice.tf", "it is a public endpoint whose work arrives with a phone's call and whose idle cost is the installation's to pay for nothing"); why != "" {
		return why
	}
	cs := objects(at(a["template"], "containers"))
	if unknownAt(unk, "template", 0, "containers") {
		return "containers that are not known"
	}
	if len(cs) == 0 {
		return "no container"
	}
	if why := c.voiceAccount(a, unk, pre, plans); why != "" {
		return why
	}
	for i, m := range cs {
		cp := []any{"template", 0, "containers", i}
		for _, attr := range []string{"image", "command", "args"} {
			if unknownAt(unk, append(slices.Clone(cp), attr)...) {
				return "a container " + attr + " that is not known"
			}
		}
		img := str(m["image"])
		if !c.credentialsImageOK(img, proj) {
			return fmt.Sprintf("runs the image %q, which is not this installation's released pin read back through the pull-through repository (%s/%s/%s/%s/fugaro-<kind>:<version>@sha256:<digest>): the hop is a subcommand of the released binary, so it runs the credentials service's own pin and never an image of its own",
				img, c.Registry, proj, c.Ghcr, c.Owner)
		}
		cmd, args := strList(m["command"]), strList(m["args"])
		if len(cmd) != 0 || !slices.Equal(args, voiceServiceArgs) {
			return "a container whose command or args are not the module's: the hop runs the released image with no command of its own and args " +
				strings.Join(voiceServiceArgs, " ") + ", and a change that named anything else would either serve the credentials service's routes at the hop's own public address or run code voice.tf does not write"
		}
		if unknownAt(unk, append(slices.Clone(cp), "env")...) {
			return "an environment that is not known"
		}
		for j, e := range objects(m["env"]) {
			ep := append(slices.Clone(cp), "env", j)
			if unknownAt(unk, append(slices.Clone(ep), "name")...) || unknownAt(unk, append(slices.Clone(ep), "value")...) ||
				unknownAt(unk, append(slices.Clone(ep), "value_source")...) {
				return "an environment variable that is not known"
			}
			if n := str(e["name"]); !slices.Contains(voiceServiceEnv, n) {
				return fmt.Sprintf("an environment variable %q, which the voice hop does not read: its container sets only %s",
					n, strings.Join(voiceServiceEnv, ", "))
			}
			if refs := objects(e["value_source"]); len(refs) > 0 {
				return "an environment variable whose value comes from a source rather than the module: this service's environment is the module's own names and nothing else"
			}
		}
	}
	return ""
}

// mobileServiceChange holds the public mobile service to the same rules the hop
// is held to, and the running of it is the same four checks; what is this
// service's own is the argv and the account.
//
//   - the argv. This process is the credentials service's OWN image started in
//     its mobile mode (mobile_service.tf), so the module writes
//     `args = ["credentials", "serve", "--mobile"]` — the private service's own
//     command plus the flag that mounts the mobile routes — and the flag is the
//     one thing that tells this service from the private one running the same
//     pin. A change whose args are not exactly that is refused in both
//     directions, and the direction that matters here is the empty one: a
//     container with no args at all serves the credentials service's routes at a
//     public URL, as the account that may mint a device identity, which is
//     precisely the outcome F31 exists to prevent. The command stays empty, as
//     every service of this image's does: the image's entrypoint is the binary.
//   - the account, which is this service's own google_service_account.mobile[0]
//     (mobileAccount) and never a sibling's, for mobileAccount's reason: it
//     holds signJwt on the devices' signer, so running the credentials service
//     as it — or this container as the account that reads every model
//     credential in the bundle — is a pairing nothing announced.
//
// The zero-minimum scaling is the third, and it is the reason this service
// shares publicServiceMinInstances with the hop: a public endpoint kept warm is
// a monthly bill whether or not anybody calls it.
//
// The environment is the seven names the process refuses to start without
// (mobileServiceEnv), held to literal values and no value_source for the
// credentials service's reason — and with more force here, since an entry the
// module did not write is one an unauthenticated caller's request would run
// with.
func (c Cover) mobileServiceChange(rc ResourceChange, a map[string]any, unk any, pre string, plans serviceAccountsPlan, proj string) string {
	if unknown(rc.Change, "invoker_iam_disabled") {
		return "an IAM setting that is not known"
	}
	// The flag is checked rather than merely required to be literal: with the
	// IAM check off the service would be reachable without the plan's own
	// allUsers member — that is, with no resource a reviewer reads — which is
	// the shape this tree refuses everywhere (mobile_service.tf states it).
	if a["invoker_iam_disabled"] != false {
		return "a service that does not require IAM, which would make the mobile service public through a flag the plan's review never reads instead of through the binding mobile_service.tf writes"
	}
	if why := c.publicServiceMinInstances(a, unk, rc.Address, "the public mobile service", "mobile_service.tf", "a device pairs, refreshes or restores on a call of its own and an instance kept warm is a bill the installation pays for nobody's call"); why != "" {
		return why
	}
	cs := objects(at(a["template"], "containers"))
	if unknownAt(unk, "template", 0, "containers") {
		return "containers that are not known"
	}
	if len(cs) == 0 {
		return "no container"
	}
	if why := c.mobileAccount(a, unk, pre, plans); why != "" {
		return why
	}
	for i, m := range cs {
		cp := []any{"template", 0, "containers", i}
		for _, attr := range []string{"image", "command", "args"} {
			if unknownAt(unk, append(slices.Clone(cp), attr)...) {
				return "a container " + attr + " that is not known"
			}
		}
		img := str(m["image"])
		if !c.credentialsImageOK(img, proj) {
			return fmt.Sprintf("runs the image %q, which is not this installation's released pin read back through the pull-through repository (%s/%s/%s/%s/fugaro-<kind>:<version>@sha256:<digest>): the mobile service is the credentials service's own binary in another mode, so it runs that pin and never an image of its own",
				img, c.Registry, proj, c.Ghcr, c.Owner)
		}
		cmd, args := strList(m["command"]), strList(m["args"])
		if len(cmd) != 0 || !slices.Equal(args, mobileServiceArgs) {
			return "a container whose command or args are not the module's: the mobile service runs the released image with no command of its own and args " +
				strings.Join(mobileServiceArgs, " ") + ", and a container that named none of them would serve the credentials service's routes at this public URL as the account that signs device tokens"
		}
		if unknownAt(unk, append(slices.Clone(cp), "env")...) {
			return "an environment that is not known"
		}
		for j, e := range objects(m["env"]) {
			ep := append(slices.Clone(cp), "env", j)
			if unknownAt(unk, append(slices.Clone(ep), "name")...) || unknownAt(unk, append(slices.Clone(ep), "value")...) ||
				unknownAt(unk, append(slices.Clone(ep), "value_source")...) {
				return "an environment variable that is not known"
			}
			if n := str(e["name"]); !slices.Contains(mobileServiceEnv, n) {
				return fmt.Sprintf("an environment variable %q, which the mobile service does not read: its container sets only %s",
					n, strings.Join(mobileServiceEnv, ", "))
			}
			if refs := objects(e["value_source"]); len(refs) > 0 {
				return "an environment variable whose value comes from a source rather than the module: this service's environment is the module's own names and nothing else"
			}
		}
	}
	return ""
}

// orchestratorServiceChange holds the resident orchestrator's own service
// (mobile plan tasks P5.1c and P5.1d, modules/installation/orchestrator.tf) to
// the same four rules intakeServiceChange holds the intake's to, and the
// reasoning for the shape is that function's and credentials.tf's case above.
// It is a case of its own and not an arm of the intake's, because two of the
// four are this service's own:
//
//   - the argv. This process is the released image's `orchestrator serve`
//     subcommand (internal/cli/orchestrator_serve.go), and the image's own
//     default command serves the credentials service, so the module writes
//     `args = ["orchestrator", "serve"]`. A change whose args are anything else
//     is refused in both directions: none would serve the credentials service's
//     or the intake's routes from this URL as an account that may mint a token
//     for any uid, and anything else would run code orchestrator.tf does not
//     write. The command is empty, as the two other services' is.
//   - the environment, which is SIX names and not seven
//     (orchestratorServiceEnv): P5.1c's three — the project that names this
//     installation's node, the Firebase project it belongs to and the database
//     URL it is read from — beside P5.1d's own three, the GCP project the jobs
//     are in, the region they were deployed to and the runs bucket's URL (the
//     same three names a run's own container carries). Those six are what
//     internal/cli/orchestrator_serve.go reads, and an environment is the one
//     path a credential could reach this process without a Secret Manager read
//     — so an entry off the list, or one whose value comes from a source, is
//     refused here rather than under the one confirmation that covered the
//     plan. The database URL is on the list as a NAME; the check that the value
//     is THIS installation's database is the
//     process's own (it compares the host to the Firebase project it was
//     given), because only the project's own services know which database is
//     theirs.
//
// The account is the orchestrator's own google_service_account.orchestrator[0]
// (orchestratorAccount) and never one of the module's other accounts: this
// identity holds iam.serviceAccounts.signJwt on the installation's budget
// signer, so running either of the other two services' images as it — or this
// image as another account — is a pairing section 4.3's table never priced.
func (c Cover) orchestratorServiceChange(rc ResourceChange, a map[string]any, unk any, pre string, plans serviceAccountsPlan, proj string) string {
	if unknown(rc.Change, "invoker_iam_disabled") {
		return "an IAM setting that is not known"
	}
	if a["invoker_iam_disabled"] != false {
		return "a service that does not require IAM, which would let anyone on the internet wake the resident orchestrator and act on this installation's own state"
	}
	cs := objects(at(a["template"], "containers"))
	if unknownAt(unk, "template", 0, "containers") {
		return "containers that are not known"
	}
	if len(cs) == 0 {
		return "no container"
	}
	if why := c.orchestratorAccount(a, unk, pre, plans); why != "" {
		return why
	}
	for i, m := range cs {
		cp := []any{"template", 0, "containers", i}
		for _, attr := range []string{"image", "command", "args"} {
			if unknownAt(unk, append(slices.Clone(cp), attr)...) {
				return "a container " + attr + " that is not known"
			}
		}
		img := str(m["image"])
		if !c.credentialsImageOK(img, proj) {
			return fmt.Sprintf("runs the image %q, which is not this installation's released pin read back through the pull-through repository (%s/%s/%s/%s/fugaro-<kind>:<version>@sha256:<digest>): the resident orchestrator is a subcommand of the released binary, so it runs that pin and never an image of its own",
				img, c.Registry, proj, c.Ghcr, c.Owner)
		}
		cmd, args := strList(m["command"]), strList(m["args"])
		if len(cmd) != 0 || !slices.Equal(args, orchestratorServiceArgs) {
			return "a container whose command or args are not the module's: the resident orchestrator runs the released image with no command of its own and args " +
				strings.Join(orchestratorServiceArgs, " ") + ", and a change that named anything else would run one of this installation's other services here, or code orchestrator.tf does not write"
		}
		if unknownAt(unk, append(slices.Clone(cp), "env")...) {
			return "an environment that is not known"
		}
		for j, e := range objects(m["env"]) {
			ep := append(slices.Clone(cp), "env", j)
			if unknownAt(unk, append(slices.Clone(ep), "name")...) || unknownAt(unk, append(slices.Clone(ep), "value")...) ||
				unknownAt(unk, append(slices.Clone(ep), "value_source")...) {
				return "an environment variable that is not known"
			}
			if n := str(e["name"]); !slices.Contains(orchestratorServiceEnv, n) {
				return fmt.Sprintf("an environment variable %q, which the resident orchestrator does not read: its container sets only %s",
					n, strings.Join(orchestratorServiceEnv, ", "))
			}
			if refs := objects(e["value_source"]); len(refs) > 0 {
				return "an environment variable whose value comes from a source rather than the module: this service's environment is the module's own names and nothing else"
			}
		}
	}
	return ""
}

// publicServiceMinInstances holds a PUBLIC service's two scaling blocks to a
// literal zero, and it is called by both of the tree's public services: the
// voice hop's (voiceServiceChange's third rule) and the mobile service's
// (mobileServiceChange, mobile plan task P5). address is the change's own and
// what names the service, for the messages: a reader of the refusal has to know
// which of the two blocks moved.
func (c Cover) publicServiceMinInstances(a map[string]any, unk any, address, what, source, reason string) string {
	// The service-level block and the template's: the first is what the managed
	// gate prices and the second is what Cloud Run keeps warm, and both modules
	// write both because the provider computes one of them otherwise (the
	// service-level block's). An unknown count is a no here for the service-level
	// reason: the gate cannot price a service whose floor it cannot read.
	for _, b := range []struct {
		// read is the path at() takes to the block: at() unwraps a one-element
		// block list, so what comes back for a module-written plan is the block
		// itself, and a plan that wrote two of them is refused by the count
		// below.
		read []string
		// unknown is the same block's path in after_unknown, where the index
		// is explicit: unknownAt does not unwrap.
		min []any
	}{
		{read: []string{"scaling"}, min: []any{"scaling", 0, "min_instance_count"}},
		{read: []string{"template", "scaling"}, min: []any{"template", 0, "scaling", 0, "min_instance_count"}},
	} {
		if unknownAt(unk, b.min...) {
			return "a minimum instance count that is not known"
		}
		blocks := objects(at(a, b.read...))
		if len(blocks) != 1 {
			return fmt.Sprintf("%s states %d scaling blocks, want one: %s keeps zero minimum instances and the count is written at both the service and the template level", address, len(blocks), what)
		}
		if n := blocks[0]["min_instance_count"]; n != float64(0) {
			return fmt.Sprintf("%s keeps %v instance(s) warm: %s keeps no instance warm (%s), because %s", address, n, what, source, reason)
		}
	}
	return ""
}

// orchestratorTickChange holds the tick that wakes that service to the two
// facts that make it this service's, and it is the tree's only scheduling job
// of either shape:
//
//   - the URI. Every other tick in the tree calls the Cloud Run API's
//     `/v2/projects/<project>/locations/<region>/jobs/<job>:run`, because what
//     it starts is a JOB. This one calls a run.app URL, because what it wakes is
//     a SERVICE, and the path is the process's single route
//     (orchestratorTickPath). The URI is a computed attribute of the service
//     resource (`${google_cloud_run_v2_service.orchestrator[0].uri}/tick`), so
//     it is UNKNOWN on the apply that creates the service and KNOWN on every
//     later one, when terraform reads it back out of state — both arms are
//     checked, and neither is vacuous: unknown requires a service created in
//     this same module (the shape the module writes), and known holds the URL
//     to this run's region, the run.app domain, the project number of the
//     host's own label and the tick's path (orchestratorTickURI states the
//     bound, and what it does not read). A tick pointed anywhere else runs,
//     fails and logs every minute forever, which is a service nobody measures.
//   - the token. It is an OIDC ID token, not the OAuth access token every other
//     tick carries: Cloud Run's front end authenticates a run.app caller by the
//     ID token whose audience is that service's own URL, and an access token
//     there is refused before the container runs. The audience is therefore the
//     URI's origin, the service's URL without the tick's route (the audience
//     names a service, not a route on it), so a token minted for another
//     service or another URL cannot be described by this file — and the account is this
//     run's scheduler account, held to the same two arms the OAuth path's is
//     (an unknown email is the module's own only when the plan creates an
//     account with it; a known one must be a Fugaro account of this run).
//
// What it does NOT check, because the other case does: the service the URI
// names is checked where it is declared (orchestratorServiceChange), so this
// function never has to read a container.
func (c Cover) orchestratorTickChange(rc ResourceChange, a map[string]any, unk any, pre string, creates map[string]bool) string {
	ht := at(a["http_target"])
	// The two blocks are read through `at` rather than `unknownAt`, as the run
	// API arm below reads them: `http_target` and `oidc_token` are blocks of
	// list nesting mode, and `at` collapses a single-element list, so the same
	// call reaches the block's own attribute in both the after and the
	// after_unknown document.
	if at(unk, "http_target", "uri") == true {
		if !creates[pre+"|google_cloud_run_v2_service"] {
			return "calls a service that is not known and none is created with it"
		}
	} else if why := c.orchestratorTickURI(str(at(ht, "uri"))); why != "" {
		return why
	}
	oidc := objects(at(ht, "oidc_token"))
	if len(oidc) == 0 {
		return "calls a run.app URL with no ID token: Cloud Run's front end authenticates a caller of this service by an OIDC ID token, and an OAuth access token there is refused before the container runs"
	}
	if at(unk, "http_target", "oidc_token", "audience") == true {
		// The module's own shape: the audience is the service's own URL, which
		// is unknown exactly while the service is being created.
		if at(unk, "http_target", "uri") != true {
			return "an ID token whose audience is not known and a URL that is: the audience of a Cloud Run ID token is the service's own URL, and the two are one expression"
		}
	} else if got, want := str(oidc[0]["audience"]), orchestratorTickOriginOf(str(at(ht, "uri"))); got != want {
		return fmt.Sprintf("an ID token for the audience %q, not for the service URL it calls (%q): Cloud Run refuses an ID token whose audience is not the service's own URL (the URL without the route), so this job would never be admitted",
			got, want)
	}
	if at(unk, "http_target", "oidc_token", "service_account_email") == true {
		if !creates[pre+"|google_service_account"] {
			return "calls as an account that is not known and none is created with it"
		}
	} else if email := str(oidc[0]["service_account_email"]); email == "" || !c.fugaroAccount("serviceAccount:"+email) {
		return "calls as " + email + ", not a Fugaro service account of this run"
	}
	return ""
}

// orchestratorTickURI is the known-URI arm of the tick's URL rule: the URL has
// to be a Cloud Run service URL of THIS run's region on the tick's path —
// https, the run.app domain, the project number Cloud Run appends to the
// service's name, and the path with nothing after it.
//
// What it does not tie, and why: the service's own name. A run.app host is
// <service>-<project number>, and the number is a fact only the deployment has
// — this cover knows the project's ID and this run's region, not the number —
// so which service the host names is held where the expression is written:
// orchestrator.tf's `uri` is the service's own computed attribute, pinned by
// the module's TestOrchestratorServiceIsTheDesignsShape and by internal/infra's
// plan test, which reads the tick's uri and audience off the very service the
// same plan creates. The run API arm above draws the same bound one URL shape
// over: it checks this run's project and region, not the job the URI names.
func (c Cover) orchestratorTickURI(uri string) string {
	bad := func() string {
		return fmt.Sprintf("calls %q, not a Cloud Run service URL of this run's region on the tick's path: the job must be https://<the service this module creates>-<project number>.%s.run.app%s, which is the URL the service answers on and the audience its ID token must carry",
			uri, c.Region, orchestratorTickPath)
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "https" || u.RawQuery != "" || u.Fragment != "" || u.Path != orchestratorTickPath || c.Region == "" {
		return bad()
	}
	// The dot in front of the region is what makes it a label of its own: a
	// host that merely ends in the same words (evil-us-east5.run.app) is not
	// this region's service URL.
	service, ok := strings.CutSuffix(u.Host, "."+c.Region+".run.app")
	if !ok {
		return bad()
	}
	// <service>-<project number>: a label, a hyphen and digits. A URL that
	// names no project at all is not a Cloud Run service URL, whoever hosts it.
	number, ok := strings.CutPrefix(service, orchestratorServiceName+"-")
	if !ok || number == "" || strings.Trim(number, "0123456789") != "" {
		return bad()
	}
	return ""
}

// orchestratorTickOriginOf is the URL without its path: the audience the tick's
// ID token carries.
func orchestratorTickOriginOf(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// checkSpecs checks every FUGARO_CHECK_SPEC entry of a container's
// environment (envPath is the env list's path in after_unknown): the spec's
// registry and each base image it builds FROM must be in this run's registry,
// so a job that the covered plan creates can never build from or push to
// anything else. An environment that is not fully known is not covered.
//
// It holds the job's FUGARO_BUCKET there too, and on EVERY job rather than on
// the check jobs alone: infra's platformEnvFor writes the one runs bucket for
// the check jobs and the workflow jobs alike, so a rule that fired on the
// check job's copy would leave the same variable unchecked on the workflow
// job's, which a hand-edited plan turns into a run whose records land in
// another bucket — the same hole one job over, and under init's one
// confirmation. The value must be the module's own literal, gs://<one of this
// run's buckets>: a computed one is refused as the specs' unknown values are,
// and so is an entry that takes its value from a secret, which carries the
// variable's name and a value the module never set.
//
// FUGARO_RUNS_BUCKET is held with it, in the bare form its writers give it.
// The installation's history and coordinator sweeps are covered jobs (their
// commands are in knownCommands) and carry this run's runs bucket under that
// name: the history job rolls each run's result.json into the Firestore
// history and the coordinator reconciles the registry from the same objects,
// so a plan that pointed either at another bucket would feed forged records
// into both — and, like the workflow job's copy, under the one confirmation
// that covered the plan. The rule is the same one, on the form the variable's
// own writer uses.
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
		name := str(e["name"])
		if name == bucketEnv || name == runsBucketEnv {
			// A value that comes from a source (a secret_key_ref) is not the
			// module's literal even when the entry carries one too: the
			// credentials service's case refuses the same shape on its
			// container for the same reason, a value the module never set, and
			// a plan that kept this run's bucket here and added a source would
			// point the job's records wherever the secret says while this check
			// read the literal. The source block is read as the literal is: one
			// the plan leaves to apply is a value this run cannot show either,
			// and after carries an empty list for it, so it is the unknown path
			// and not the non-empty one that refuses it.
			if refs := objects(e["value_source"]); len(refs) > 0 {
				return "a " + name + " whose value comes from a secret rather than the module"
			}
			if unknownAt(unk, append(slices.Clone(ep), "value")...) || unknownAt(unk, append(slices.Clone(ep), "value_source")...) {
				return "a " + name + " that is not known"
			}
			if v := str(e["value"]); !c.bucketEnvOK(name, v) {
				return fmt.Sprintf("a %s of %q, which is not this run's runs bucket (%s)", name, v, c.bucketValues(name))
			}
			continue
		}
		if name != "FUGARO_CHECK_SPEC" && name != "FUGARO_CATALOG_CHECK_SPEC" {
			continue
		}
		if unknownAt(unk, append(slices.Clone(ep), "value")...) {
			return "a check spec that is not known"
		}
		if name == "FUGARO_CATALOG_CHECK_SPEC" {
			if why := c.checkCatalogSpec(str(e["value"]), proj, prefix); why != "" {
				return why
			}
			continue
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

// dataBearingTypes and nonDataBearingTypes classify every entry of allowedTypes
// under D1551 (docs/no-prompt-policy/reconciliation.md): whether the objects the
// type holds are data a delete or a replace would destroy, and — by the type's
// presence in one of the two — whether anybody has decided the question at all.
//
// D1545 makes the managed gate audit-only for CONTENT: a refusal about what a
// plan contains becomes record-and-warn. D1551 is the exception it leaves, and
// it is scoped to TYPES: a delete or a replace of a resource whose objects hold
// data is a loss the record-and-warn rule was never meant to take, so its type
// still refuses. Data-bearing here is exactly D1551's list of nine.
//
// The two tables are stated separately and neither is derived from the other by
// subtraction, deliberately: a type added to allowedTypes by a later module is
// in NEITHER table and is therefore UNCLASSIFIED, so DataBearingType answers
// (false, false) for it and RefusesRemovalType refuses its removal — a type
// refuses until somebody classifies it on purpose, which is D1551's fail-closed
// direction (an unclassified type behaves like a data-bearing one rather than a
// non-data-bearing one). TestEveryAllowedTypeIsClassifiedDataBearingOrNot holds
// the split: every allowedTypes entry is in exactly one table, the two do not
// overlap, and the nine data-bearing names are D1551's own.
var dataBearingTypes = map[string]bool{
	// The budget ledger's instance: D1551 names it by what it holds (the kill
	// switches, the caps and the mode the whole installation is bounded by).
	"google_firebase_database_instance": true,
	// Artifact Registry holds the images a managed project's jobs run; the
	// repository is the container the image lives in.
	"google_artifact_registry_repository": true,
	// The log bucket and the view over it are where a managed project's logs
	// are kept — deleting the bucket destroys the records in it.
	"google_logging_project_bucket_config": true,
	"google_logging_log_view":              true,
	// D1551 names the key's container: a secret's values live in its versions,
	// and deleting the secret destroys them.
	"google_secret_manager_secret": true,
	// An identity whose keys and grants are lost with it (D1551).
	"google_service_account": true,
	// The bucket and the objects in it: the plan's own storage.
	"google_storage_bucket":        true,
	"google_storage_bucket_object": true,
	// A key's own value is the credential an API call carries.
	"google_apikeys_key": true,
	// The consumer's signing key (mobile-identity-pairing plan task J6; O17):
	// the one resource of this tree whose loss is not repairable, and D1551's
	// question is whether a delete or a replace destroys data its objects hold.
	// It does. What the key holds is the private half every paired phone's
	// pinned public key corresponds to: destroy or replace it and every phone
	// verifies nothing the installation signs again — silently, at the phone,
	// with no error anywhere in the installation — until each one is paired
	// again. That is why the module carries prevent_destroy beside this entry
	// (deploy/terraform's TestPreventDestroy) and why the classification is
	// data-bearing rather than a type nobody decided.
	"google_kms_crypto_key": true,
}

// nonDataBearingTypes is the other half of D1551's classification: every entry
// of allowedTypes whose removal destroys no data its objects hold. D1551 groups
// them — the *_iam_member grant types, the Cloud Run job and service, the
// scheduler, the compute network, the monitoring and logging plumbing, the
// billing budget, a custom role, an enabled service and the Firebase project —
// and each is named here one by one so that the classification of every type is
// a decision rather than a default.
var nonDataBearingTypes = map[string]bool{
	"google_artifact_registry_repository_iam_member": true,
	"google_billing_budget":                          true,
	"google_cloud_run_v2_job":                        true,
	"google_cloud_run_v2_job_iam_member":             true,
	"google_cloud_run_v2_service":                    true,
	"google_cloud_run_v2_service_iam_member":         true,
	"google_cloud_scheduler_job":                     true,
	"google_compute_firewall":                        true,
	"google_compute_router":                          true,
	"google_compute_router_nat":                      true,
	"google_firebase_project":                        true,
	// An index holds no documents: the runs it orders live in the collection,
	// and deleting the index loses an answer, never a row.
	"google_firestore_index": true,
	// The key's ring and the grant on the key (plan task J6): a ring holds no
	// material of its own — the key inside it does, and it is the data-bearing
	// entry above — and a grant holds none. Removing the ring while the key is
	// in it is not something Terraform plans anyway (Cloud KMS refuses a ring
	// that is not empty), and removing the grant revokes the signing reach
	// rather than destroying anything, which is the state an installation that
	// turns the resident orchestrator off is left in: the key and its published
	// public half stand, and nothing signs with them.
	"google_kms_crypto_key_iam_member":        true,
	"google_kms_key_ring":                     true,
	"google_logging_log_view_iam_member":      true,
	"google_logging_project_exclusion":        true,
	"google_logging_project_sink":             true,
	"google_monitoring_alert_policy":          true,
	"google_monitoring_notification_channel":  true,
	"google_project_iam_custom_role":          true,
	"google_project_iam_member":               true,
	"google_project_service":                  true,
	"google_secret_manager_secret_iam_member": true,
	"google_service_account_iam_member":       true,
	"google_storage_bucket_iam_member":        true,
}

// DataBearingType classifies a resource type for D1551: whether its objects
// hold data a delete or a replace would destroy, and whether the type is
// classified at all. A type in neither table — one added to allowedTypes by a
// later module and not yet decided — is (false, false): not data-bearing as far
// as anyone has said, and unclassified, which the caller reads as the
// fail-closed direction (RefusesRemovalType refuses it).
//
// The question is asked of the TYPE and not of the change, because D1551's
// ruling is about types: it is what makes a type's removal refuse wherever it
// appears, whether or not the plan says anything else about it.
func DataBearingType(t string) (dataBearing, classified bool) {
	if dataBearingTypes[t] {
		return true, true
	}
	if nonDataBearingTypes[t] {
		return false, true
	}
	return false, false
}

// RefusesRemovalType is D1551's rule as the delete and replace rule reads it: a
// removal of a data-bearing type refuses, and so does one of an unclassified
// type — a type added to allowedTypes later refuses until somebody classifies
// it on purpose. Every other classified, non-data-bearing type is one the
// managed gate now takes with a warning (D1545).
func RefusesRemovalType(t string) bool {
	bearing, classified := DataBearingType(t)
	return !classified || bearing
}
