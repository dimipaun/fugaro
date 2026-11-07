package tf

// This file is one decision, stated once, about the retirement a managed
// project's plain `fugaro update` makes by itself (issue #1794, the owner's
// ruling of 2026-11-06): of the resource types the modules declare, which are
// DERIVED — a grant, an identity, the job itself, a schedule — and may
// therefore be brought down with no prompt at all, and which are data, or the
// installation's rather than one repository's, and stop that retirement with
// the confirmations it has always had.
//
// Why a table of its own rather than a rule written where the retirement runs:
// it is the same kind of decision cover.go's allowedTypes makes for the other
// direction — what may change without a person — and a rule written at its one
// call site is a rule nobody re-reads when a module grows. The two tables are
// independent on purpose: allowedTypes says what one confirmation covers, and
// it refuses EVERY delete (NotCovered), so a retirement's own plan can never be
// covered by it; this table is narrower still, and TestRetirableTypesDecide
// TheModules fails the moment a module declares a type neither half decided.
//
// Why an allowlist and never a denylist: the question a managed project asks
// before it deletes something nobody confirmed is "is this one of the few
// things I know hold nothing?" — and the only safe answer to an address whose
// type nobody decided, or whose type did not exist when this file was written,
// is no. A denylist of data types would retire every resource some future
// module adds that nobody thought about, which is the one outcome the ruling
// forbids: a bucket, a container, a registry, a database and the state itself
// stop the run and say so.
//
// Nothing here widens cover.go's allowedTypes or its grant rules: a type is
// listed here only to say the retirement's own flow — state removal, plan,
// guard, apply, API delete — may take it down by itself.

// RetirableTypes are the module resource types a managed project's plain
// `fugaro update` may retire by itself, each with the reason the run prints
// when it says why an address is one of them: every grant (a binding between a
// principal and a role, which holds nothing), the service accounts the jobs run
// as, the Cloud Run jobs, and the scheduler entries that trigger them. What
// they have in common is not "fugaro made it" but the narrower fact the ruling
// turns on: destroying one loses no value anybody stored, and recreating it
// from the configuration is a plan away.
var RetirableTypes = map[string]string{
	// The grants. A *_iam_member resource is a binding, and the thing it binds
	// — a container, a bucket, a registry, a job, a service — is a different
	// address with a different decision below.
	"google_project_iam_member":                      "a project grant: a binding between a principal and a role, holding nothing of its own",
	"google_service_account_iam_member":              "an account grant: a binding between a principal and a role, holding nothing of its own",
	"google_secret_manager_secret_iam_member":        "a grant on a container: the value lives in the container, and the binding holds nothing",
	"google_storage_bucket_iam_member":               "a grant on a bucket: the objects live in the bucket, and the binding holds nothing",
	"google_cloud_run_v2_job_iam_member":             "a grant on a job: the binding holds nothing",
	"google_cloud_run_v2_service_iam_member":         "a grant on a service: the binding holds nothing",
	"google_artifact_registry_repository_iam_member": "a grant on a registry: the images live in the registry, and the binding holds nothing",
	"google_logging_log_view_iam_member":             "a grant on a log view: the binding holds nothing",

	// The identities and the jobs. A removed workflow variant's account, job
	// and schedule are the case the issue names.
	"google_service_account":     "an identity this repository's jobs run as: it holds no value, and what it could reach is a grant with its own decision",
	"google_cloud_run_v2_job":    "the job itself: its configuration, its image reference and its execution history are fugaro's, and a variant that left fugaro.yaml has no job",
	"google_cloud_scheduler_job": "a trigger: a schedule and a target, holding nothing of its own",
}

// RetainedTypes are every other type the modules declare, each with the reason
// it stops a managed project's own retirement. They fall in three groups, and
// the third is the one that matters most: what holds data (a bucket and its
// objects, a secret container, an image registry, a database), what belongs to
// the installation rather than to one repository (the network, the logs, the
// alerts, the roles, the API enablements), and a credential that is neither.
// The last one is the conservative direction: an API key is not data, and
// nothing about a repository's key says a person can lose it without reading
// what happens first.
var RetainedTypes = map[string]string{
	// Data. The owner's ruling names every one of these.
	"google_storage_bucket":                "a bucket holds objects, and an object is data",
	"google_storage_bucket_object":         "an object is data",
	"google_secret_manager_secret":         "a secret container holds its value, and fugaro never deletes a secret or a secret version",
	"google_artifact_registry_repository":  "a registry holds images, and nothing in a plan deletes one",
	"google_firebase_database_instance":    "a database holds what was written to it",
	"google_logging_project_bucket_config": "a log bucket holds the logs written to it",
	"google_billing_budget":                "the installation's, and a budget is money",

	// The installation's, not one repository's: a managed update retires the
	// derived resources of the repository it runs for, and these are shared
	// with every other repository and with the project itself.
	"google_project_service":                 "enabling an API is the installation's, and disabling one is not one repository's to do",
	"google_project_iam_custom_role":         "a custom role is shared by the installation's principals and by its other repositories",
	"google_firebase_project":                "the project's Firebase, not one repository's",
	"google_logging_project_sink":            "the installation's log routing",
	"google_logging_project_exclusion":       "the installation's log exclusions",
	"google_logging_log_view":                "the installation's log views",
	"google_monitoring_alert_policy":         "the installation's alerts",
	"google_monitoring_notification_channel": "the installation's notification channels",
	"google_compute_firewall":                "the installation's network",
	"google_compute_router":                  "the installation's network",
	"google_compute_router_nat":              "the installation's network",
	"google_cloud_run_v2_service":            "a long-lived service, and the installation's own: retiring one is a change nobody confirmed",

	// Neither data nor derived.
	"google_apikeys_key": "a key is a credential: losing it breaks whoever holds it, and that is a person's decision",
}

// RetirableType is this file's decision for one module resource type:
// whether a managed project's plain `fugaro update` may retire an address of
// it by itself, and the reason in the words the run prints. A type in neither
// table is refused with its own sentence rather than by silence, because a type
// a module declares and this file never decided is exactly the case the
// allowlist exists for.
func RetirableType(t string) (retirable bool, why string) {
	if why, ok := RetirableTypes[t]; ok {
		return true, why
	}
	if why, ok := RetainedTypes[t]; ok {
		return false, why
	}
	return false, "it is not a type this program has decided on, and a retirement nobody decided on is one no managed update makes by itself"
}
