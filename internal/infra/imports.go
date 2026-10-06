package infra

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// Import adopts an existing resource into Terraform's state at the address
// To, as an import block of imports.tf.json.
type Import struct {
	To string `json:"to"`
	ID string `json:"id"`
}

// Imports are what discovery found: the resources to adopt, and what the
// plan needs to know about the rest.
type Imports struct {
	List []Import
	// AdoptLegacyRegistry is the installation's adopt_legacy_registry: the
	// legacy registry exists and carries our mark.
	AdoptLegacyRegistry bool
	// Bindings counts the job accounts' grants on the runs bucket and
	// their secrets against the live policies.
	Bindings BindingCount
	// Notes are things the user should know that don't stop the plan.
	Notes []string
}

// BindingCount counts the IAM grants a repository plans for its job
// accounts. IAM members are never imported: planning one that exists is
// idempotent, since the provider merges an identical role, condition and
// member into the policy. So an adopted grant is a planned create that
// changes nothing, which the live check makes safe to assume.
type BindingCount struct {
	Adopted, New int
}

func (b BindingCount) String() string {
	return fmt.Sprintf("IAM: %d bindings match the live ones (adopted), %d new", b.Adopted, b.New)
}

type importKind string

// The kinds of resource discovery adopts.
const (
	importRunsBucket     importKind = "runs bucket"
	importLegacyRegistry importKind = "legacy registry"
	importBaseRegistry   importKind = "base registry"
	importSecret         importKind = "secret"
	importRepoRegistry   importKind = "repository registry"
	importJobSA          importKind = "job account"
	importJob            importKind = "job"
	importBuildSA        importKind = "build account"
	importCheckJob       importKind = "check job"
	importSchedulerJob   importKind = "Scheduler job"
	// The installation's singletons, which the rollback's state rm leaves
	// in place, and whose create fails once they exist.
	importLauncherRole       importKind = "launcher role"
	importJobRunnerRole      importKind = "job runner role"
	importBuildSubmitterRole importKind = "build submitter role"
	importTagMoverRole       importKind = "tag mover role"
	importSchedulerSA        importKind = "scheduler account"
	importLogBucket          importKind = "log bucket"
	// The Firebase root's singletons (DiscoverFirebase), whose create fails
	// once they exist.
	importFirebaseDB importKind = "Realtime Database instance"
	importAPIKey     importKind = "web API key"
	importSignerSA   importKind = "token signer"
	importMinterRole importKind = "token minter role"
)

// importTable is each kind's address and import ID, with {project},
// {region}, {name} (the resource's own name or email) and {key} (the
// for_each key: a logical secret name or a workflow) to fill in.
var importTable = map[importKind]struct{ to, id string }{
	importRunsBucket:     {"module.installation.google_storage_bucket.runs", "{project}/{name}"},
	importLegacyRegistry: {"module.installation.google_artifact_registry_repository.legacy[0]", "projects/{project}/locations/{region}/repositories/{name}"},
	importBaseRegistry:   {"module.installation.google_artifact_registry_repository.base", "projects/{project}/locations/{region}/repositories/{name}"},
	importSecret:         {"module.repo.google_secret_manager_secret.this[{key}]", "projects/{project}/secrets/{name}"},
	importRepoRegistry:   {"module.repo.google_artifact_registry_repository.images", "projects/{project}/locations/{region}/repositories/{name}"},
	importJobSA:          {"module.repo.module.workflow[{key}].google_service_account.job", "projects/{project}/serviceAccounts/{name}"},
	importJob:            {"module.repo.module.workflow[{key}].google_cloud_run_v2_job.this[0]", "projects/{project}/locations/{region}/jobs/{name}"},
	importBuildSA:        {"module.repo.google_service_account.build", "projects/{project}/serviceAccounts/{name}"},
	importCheckJob:       {"module.repo.google_cloud_run_v2_job.check[0]", "projects/{project}/locations/{region}/jobs/{name}"},
	// {region} is the scheduler region here, not the repository's.
	importSchedulerJob:       {"module.repo.google_cloud_scheduler_job.check[0]", "projects/{project}/locations/{region}/jobs/{name}"},
	importLauncherRole:       {"module.installation.google_project_iam_custom_role.launcher", "projects/{project}/roles/{name}"},
	importJobRunnerRole:      {"module.installation.google_project_iam_custom_role.job_runner", "projects/{project}/roles/{name}"},
	importBuildSubmitterRole: {"module.installation.google_project_iam_custom_role.build_submitter", "projects/{project}/roles/{name}"},
	importTagMoverRole:       {TagMoverRoleAddress, "projects/{project}/roles/{name}"},
	importSchedulerSA:        {"module.installation.google_service_account.scheduler", "projects/{project}/serviceAccounts/{name}"},
	importLogBucket:          {LogBucketAddress, "projects/{project}/locations/global/buckets/{name}"},
	importFirebaseDB:         {"module.firebase.google_firebase_database_instance.this", "projects/{project}/locations/{region}/instances/{name}"},
	importAPIKey:             {"module.firebase.google_apikeys_key.web", "projects/{project}/locations/global/keys/{name}"},
	importSignerSA:           {"module.firebase.google_service_account.signer", "projects/{project}/serviceAccounts/{name}"},
	importMinterRole:         {"module.firebase.google_project_iam_custom_role.token_minter", "projects/{project}/roles/{name}"},
}

// newImport is the import of a resource of kind k.
//
// Several repository kinds (secrets, job accounts, jobs) are for_each rows
// whose {key} is quoted with strconv.Quote. Their keys are restricted by
// config.SecretNameRE and config.WorkflowNameRE to [a-z0-9-], where strconv.Quote and
// Terraform's HCL quoting give identical text, so the address matches the
// plan's and tf.Cover's (address, ID) allowlist accepts it. A looser key
// regex later would make Cover refuse such an import, and re-prompt on
// every run: extend this quoting to HCL's rules if that changes.
func newImport(k importKind, project, region, key, name string) Import {
	a, ok := importTable[k]
	if !ok {
		panic("infra: no import address for " + string(k))
	}
	r := strings.NewReplacer("{project}", project, "{region}", region, "{name}", name, "{key}", strconv.Quote(key))
	return Import{To: r.Replace(a.to), ID: r.Replace(a.id)}
}

// Keys are the imports as the address and ID pairs the plan's classifier
// compares against.
func (im Imports) Keys() []tf.ImportKey {
	out := make([]tf.ImportKey, 0, len(im.List))
	for _, i := range im.List {
		out = append(out, tf.ImportKey{Address: i.To, ID: i.ID})
	}
	return out
}

// ImportsFile is the file WriteImports writes into a root.
const ImportsFile = "imports.tf.json"

// WriteImports writes im's imports into dir's imports.tf.json, sorted by
// address, so equal discoveries give equal files. With none, the file is
// an empty configuration, which replaces the imports of an earlier run.
func WriteImports(dir string, im Imports) error {
	var doc any = struct{}{}
	if len(im.List) > 0 {
		list := slices.Clone(im.List)
		slices.SortFunc(list, func(a, b Import) int { return cmp.Compare(a.To, b.To) })
		doc = struct {
			Import []Import `json:"import"`
		}{list}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ImportsFile), append(b, '\n'), 0o600)
}
