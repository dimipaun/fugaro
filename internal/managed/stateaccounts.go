package managed

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// The accounted side of the whole-project inventory (Task 4.5, design D59).
//
// Inventory.Check compares the whole project against what the installation's
// own state accounts for, and the entries that comparison takes are
// StateResource values: a Cloud Asset type and a name. A Terraform state is
// written in Terraform's terms instead — a provider's resource type
// ("google_storage_bucket") and the id the provider gave the object — so
// something has to translate between the two, and this file is that
// translation, and nothing else.
//
// Why the translation is a closed table rather than a rule: a Cloud Asset type
// is a string GCP assigns per service and no property of the Terraform type
// derives it (google_storage_bucket is "storage.googleapis.com/Bucket", and
// google_cloud_run_v2_job is "run.googleapis.com/Job"), and an id's shape is
// the provider's ("projects/<p>/locations/<l>/jobs/<name>", a bare bucket
// name, "<bucket>/<object>"). Both are read off the resources this
// installation's own modules create — the installation root, the Firebase
// root, a repository root, the compute root the --provision-batch stage
// applies and the agent-authored root — because those are the states a
// managed project holds and D59's premise is that such a project holds only
// what Fugaro made (the owner's ruling of 2026-10-18). The compute root is
// listed here although no flag reaches it in this release: two of its three
// resources are in the table (they are the last two before the empty ones) and
// the third is in stateAssetTypesUndecided below, because a root whose types
// are absent would leave a project that ran it foreign for good
// (Inventory.foreign never empties, so Check's fallback never clears).
//
// The table is CLOSED, and the direction of everything it does not name is the
// fallback, never a clean project: a Terraform type this build cannot place in
// the Cloud Asset listing contributes no entry, so its resources stay
// unaccounted for and the run keeps its confirmations (D59: never halt, never
// delete, and a false positive must not stop the operator's own work). The
// other direction — an entry whose type or name is wrong — costs the same,
// because a name no asset answers to matches nothing. Neither direction can
// account for a resource that is not in the state: the id and the type both
// come from the state entry itself, so a match means the installation's own
// state really holds that object.
//
// What is NOT here, stated rather than left to a reader: the resources the
// creating run makes outside Terraform (the two state buckets of
// internal/infra's statebucket steps, the Firestore database and the Firebase
// project of infra.AddFirebase, and the creation fact and allow-delete list
// internal/infra/managed writes into the runs bucket), and the records a job
// writes into the runs bucket at run time. None is in any state, so none is
// accounted for here; each of the named ones is claimed by the inventory's own
// rule instead (Inventory.installationCreated — this function never claims
// what no state entry names), and what remains falls a managed apply back to
// its confirmations. That is the safe direction, and Check 67a is where the
// owner reads what the live listing of a real managed project answers with.

// AccountsOfState decodes one Terraform state document — the JSON a state
// bucket holds, format version 4 — and returns the resources it manages in the
// terms the whole-project read answers in.
//
// A document this build cannot read is an error and never an empty list of
// accounts: an empty answer means "the state accounts for nothing", which is
// the answer that makes every listed resource foreign, and reading a state
// that could not be parsed that way would blame the project for this
// program's failure (Global Constraint 3, fail closed).
func AccountsOfState(data []byte) ([]StateResource, error) {
	var st stateDocument
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("reading a Terraform state document: %w", safetext.FieldErr(err))
	}
	if st.Version != stateFormatVersion {
		return nil, fmt.Errorf("the state document is format version %d, and this build reads only %d", st.Version, stateFormatVersion)
	}
	var out []StateResource
	for _, r := range st.Resources {
		// A data source is not a managed resource, and a state's "data" entries
		// have no id of their own to account for anything with.
		if r.Mode != "" && r.Mode != "managed" {
			continue
		}
		// Three answers, one place (the table's own comment has the why): a
		// type this build cannot place in the listing is absent and stays
		// foreign, a type GCP keeps inside another resource is here as "" and
		// has nothing to account for, and the rest name an asset and are
		// looked up by name.
		typ, ok := stateAssetTypes[r.Type]
		if !ok || typ == "" {
			continue
		}
		for _, inst := range r.Instances {
			name, err := stateAssetName(r.Type, inst.Attributes)
			if err != nil {
				// The type and the name are the state's own, and the error under
				// them quotes the state's content: both are bounded, because this
				// sentence is what a person reads when the accounting fails.
				return nil, fmt.Errorf("the state's %s.%s: %w", safetext.Field(r.Type), safetext.Field(r.Name), safetext.FieldErr(err))
			}
			out = append(out, StateResource{Type: typ, Name: name})
		}
	}
	return out, nil
}

// stateFormatVersion is the Terraform state format this build reads. Terraform
// 1.x writes 4; a document of another version could have moved the fields read
// below, and reading it anyway would be a guess about what the state accounts
// for.
const stateFormatVersion = 4

// stateDocument is as much of a Terraform state as the accounting needs: the
// format version and, per resource, the mode, the provider's type and the
// instances' attributes.
type stateDocument struct {
	Version   int `json:"version"`
	Resources []struct {
		Mode      string `json:"mode"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		Instances []struct {
			Attributes map[string]json.RawMessage `json:"attributes"`
		} `json:"instances"`
	} `json:"resources"`
}

// stateAssetTypes maps a Terraform resource type to the Cloud Asset type GCP
// files its resources under, for the types this installation's own states can
// hold. Every entry was read off the resource the modules create, and every
// type the modules create that is an asset of a project is here.
//
// An entry is one of two answers, and both are deliberate:
//
//   - the Cloud Asset type, for a resource GCP inventories; and
//   - "" for a type whose resources are not Cloud Asset assets at all — a
//     binding or a policy GCP keeps inside the resource it grants on (an IAM
//     member, an audit config), a log exclusion, or a budget, which belongs to
//     a billing account and not to the project. Its absence from the listing
//     is what the empty string records, and an entry that "accounts for
//     nothing" is exactly right for it.
//
// A type that is neither is absent from the map, which is the third answer and
// the fallback: this build does not know how GCP files it, so its resources
// stay unaccounted for.
var stateAssetTypes = map[string]string{
	"google_storage_bucket":                  "storage.googleapis.com/Bucket",
	"google_storage_bucket_object":           "storage.googleapis.com/Object",
	"google_service_account":                 "iam.googleapis.com/ServiceAccount",
	"google_project_iam_custom_role":         "iam.googleapis.com/Role",
	"google_cloud_run_v2_job":                "run.googleapis.com/Job",
	"google_cloud_run_v2_service":            "run.googleapis.com/Service",
	"google_cloud_scheduler_job":             "cloudscheduler.googleapis.com/Job",
	"google_secret_manager_secret":           "secretmanager.googleapis.com/Secret",
	"google_artifact_registry_repository":    "artifactregistry.googleapis.com/Repository",
	"google_apikeys_key":                     "apikeys.googleapis.com/Key",
	"google_monitoring_alert_policy":         "monitoring.googleapis.com/AlertPolicy",
	"google_monitoring_notification_channel": "monitoring.googleapis.com/NotificationChannel",
	"google_logging_project_sink":            "logging.googleapis.com/LogSink",
	"google_logging_project_bucket_config":   "logging.googleapis.com/LogBucket",
	"google_logging_log_view":                "logging.googleapis.com/LogView",
	"google_project_service":                 "serviceusage.googleapis.com/Service",
	"google_firebase_project":                "firebase.googleapis.com/FirebaseProject",
	"google_firebase_database_instance":      "firebasedatabase.googleapis.com/Instance",
	"google_firestore_database":              "firestore.googleapis.com/Database",
	// The compute module the Batch backend's own root applies
	// (deploy/terraform/gcp/modules/compute/network.tf:58,75,84, each with
	// project = var.project): the deny-all ingress rule on the run-class
	// accounts and the router the NAT hangs off. The root writes its state
	// into the installation's own bucket, which managedAccounted reads with no
	// prefix, so a project that ran --provision-batch holds both. Their names
	// are the provider's ids and come from the state like every other entry's.
	// The module's third resource, google_compute_router_nat, is the one type
	// of the three this build could not place: see stateAssetTypesUndecided.
	"google_compute_firewall":                        "compute.googleapis.com/Firewall",
	"google_compute_router":                          "compute.googleapis.com/Router",
	"google_project_iam_member":                      "",
	"google_project_iam_binding":                     "",
	"google_project_iam_policy":                      "",
	"google_project_iam_audit_config":                "",
	"google_service_account_iam_member":              "",
	"google_service_account_iam_binding":             "",
	"google_service_account_iam_policy":              "",
	"google_storage_bucket_iam_member":               "",
	"google_storage_bucket_iam_binding":              "",
	"google_storage_bucket_iam_policy":               "",
	"google_secret_manager_secret_iam_member":        "",
	"google_secret_manager_secret_iam_binding":       "",
	"google_artifact_registry_repository_iam_member": "",
	"google_cloud_run_v2_job_iam_member":             "",
	"google_logging_log_view_iam_member":             "",
	"google_logging_project_exclusion":               "",
	"google_billing_budget":                          "",
}

// stateAssetTypesUndecided names the module-declared Terraform types this build
// could not place in the Cloud Asset listing, with the reason, so that leaving
// one out of stateAssetTypes is a recorded answer rather than an oversight. The
// value is the open question and how to settle it; it is documentation with a
// test behind it (TestStateAssetTypesUndecidedAreDeclaredAndReasoned), not a
// second answer the code reads.
//
// The behaviour is the fallback's, exactly as for a type stateAssetTypes does
// not name at all: AccountsOfState skips the resource, so it stays Foreign and
// the project's fallback never clears. That is fail-closed, and it is the right
// direction for a type this build cannot verify — an inventory type that does
// not exist would leave such a project foreign just as permanently, while
// claiming to have accounted for it.
var stateAssetTypesUndecided = map[string]string{
	"google_compute_router_nat": "the Compute API carries a RouterNat schema type, but a NAT lives inside its router's `nats[]` there, and nothing in this tree or the module cache names a Cloud Asset Inventory type for it — compute.googleapis.com/Router was easy to confirm and compute.googleapis.com/RouterNat could not be confirmed at all. The inventory's own listing settles it and no offline test can: read the Compute asset types a live provisioned project's whole-project listing answers with (Check 67a, internal/managed's inventory), or the Cloud Asset Inventory documentation's compute types. Unreachable in this release (--provision-batch is refused), so it must be settled before the task that ships provisioning; until then a NAT asset stays foreign, which is the safe direction.",
}

// stateAssetName is a state resource's name in the terms the whole-project
// read answers in: one of the shapes StateResource names — the provider's own
// id, which is the GCP path of the resource for these types ("projects/<p>/
// secrets/<s>"), or its bare name where GCP names the resource that way (a
// bucket), or, for the one type whose id is not either, the full path the
// listing holds.
//
// Why the id is trusted for a name: the id is what the provider wrote when it
// created the resource and what it reads the resource back by, so it names the
// object the state manages and no other. A state entry with no id, or an
// object with no bucket or name, is an error rather than an entry: an entry
// built out of nothing would account for nothing, silently, which is a
// fallback reported with the wrong cause.
func stateAssetName(terraformType string, attrs map[string]json.RawMessage) (string, error) {
	if terraformType == "google_storage_bucket_object" {
		// The provider's id of an object is "<bucket>/<object>", which is
		// neither of the shapes above: the listing files it under
		// "//storage.googleapis.com/projects/_/buckets/<b>/objects/<o>"
		// whatever the bucket's project is.
		bucket, err := stateString(attrs, "bucket")
		if err != nil {
			return "", err
		}
		object, err := stateString(attrs, "name")
		if err != nil {
			return "", err
		}
		return "projects/_/buckets/" + bucket + "/objects/" + object, nil
	}
	id, err := stateString(attrs, "id")
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(id, "\n\r") {
		return "", fmt.Errorf("its id %q is not one line", id)
	}
	return id, nil
}

// stateString is one attribute of a state instance as a string: an attribute
// that is absent, null or not a string is an error, because every name here is
// required and a "" would account for whatever a nameless asset matched.
func stateString(attrs map[string]json.RawMessage, key string) (string, error) {
	raw, ok := attrs[key]
	if !ok {
		return "", fmt.Errorf("its state carries no %s", key)
	}
	if string(raw) == "null" {
		// encoding/json would leave the string empty here without complaint,
		// and "empty" is the wrong sentence for an attribute the state holds
		// as null: it names the wrong failure to whoever reads it.
		return "", fmt.Errorf("its %s is null", key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("its %s is not a string", key)
	}
	if s == "" {
		return "", fmt.Errorf("its %s is empty", key)
	}
	return s, nil
}
