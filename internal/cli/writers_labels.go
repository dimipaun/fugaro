package cli

import (
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file is item 8's half of the config-ownership work: every registry row
// no command writes, registered with one of the three labels D2611 settled and
// a one-line reason (T5). The other half is T1's ratchet, which runs the rows
// that do have a command line; a labelled row is not run, because the label and
// its reason are the fact that stands in for one.
//
// The label follows the row's own Owner token, which G12 already made the
// read's table (F11): `setup` and `update` are the installation's own
// bookkeeping, `config`, `config init` and `config local` are the keys a person
// states in a named file. The single place the probe moved a row off its token
// is the `endpoints` block: the registry calls it `setup`'s, and nothing
// outside a test assigns a localcfg.Endpoints field — `fugaro setup` carries
// the block through untouched — so every endpoint carries `fake-endpoint` and
// its reason says so (F18).
//
// The lines below were measured, not guessed: a row whose Owner names a
// command family (`models`, `accounts`, `rigor`, `secrets`, `project`,
// `workspace`, `budget`, the image map, the clone URLs) or whose key the
// generic path's allowlist admits belongs to those writers and is registered by
// them, and the rows another task of the plan already gives a writer are named
// in settingsLabelledElsewhere rather than labelled here — a label registered
// for one of those would be a second registrant of one pattern, and
// settings.RegisterWriter panics on that in every command
// (docs/contracts/config-ownership.md §4).

const (
	labelPlumbing     = settings.LabelPlumbing
	labelFakeEndpoint = settings.LabelFakeEndpoint
	labelFile         = settings.LabelFile
)

// settingsLabelRow is one registration: the registry row a label answers for,
// the label, and the one line saying what the label means for this row.
type settingsLabelRow struct {
	pattern string
	label   settings.Label
	reason  string
}

// settingsUnwrittenOwners are the registry Owner tokens that name no command:
// the installation's own bookkeeping (`setup`, `update`), and the keys a person
// states in the file the token names (`config`, `config init`, `config local`).
// Every other token is a command family's, and a row it owns is that family's
// to register.
var settingsUnwrittenOwners = map[string]bool{
	"setup":        true,
	"update":       true,
	"config":       true,
	"config init":  true,
	"config local": true,
}

// settingsLabelledElsewhere names the rows of those tokens another task of the
// plan gives a writer, with the task. They stay T1's golden gap until it lands,
// and a label here would be the same row registered twice.
var settingsLabelledElsewhere = map[string]string{
	"layer.jobs.catalog_check.paused":    "R1 hands the row to `settings set` (`update` is not a writer)",
	"layer.jobs.reports.paused":          "R1 hands the row to `settings set` (`update` is not a writer)",
	"planning.decoupling":                "R1 hands the row to `settings set`",
	"issues.nudge_after_days":            "R1 hands the row to `settings set`",
	"issues.nudge_recurrence":            "R1 hands the row to `settings set`",
	"security.patterns":                  "M4 registers `fugaro rigor security` for it",
	"layer.security.always_level_3":      "M4 registers `fugaro rigor security` for it",
	"layer.security.patterns.*.name":     "M4 registers `fugaro rigor security` for it",
	"layer.security.patterns.*.match":    "M4 registers `fugaro rigor security` for it",
	"layer.security.patterns.*.category": "M4 registers `fugaro rigor security` for it",
}

// settingsRowNeedsALabel is the measurement the two maps above make: the row is
// one of a token no command answers for, and no other task has claimed it. It
// is the scope of this file, read by the test and by nothing else.
func settingsRowNeedsALabel(r settings.Row) bool {
	if !settingsUnwrittenOwners[r.Owner] {
		return false
	}
	_, elsewhere := settingsLabelledElsewhere[r.Key]
	return !elsewhere
}

const (
	// The endpoint overrides are one block with one answer (F18).
	reasonEndpoint = "an API-root override for a fake installation: `fugaro setup` carries the block through and writes no endpoint, so only a test sets one"

	// U2's three blocks: the registry calls them `setup`'s, and no assignment
	// to one of their localcfg fields exists outside a test, so the labelled
	// shape is the true one rather than `plumbing`, which claims a writer.
	reasonBatch = "a key of the local config's `batch` block: no command writes one, and a batch installation states it as the backend is provisioned"
	reasonBuild = "a key of the local config's `build` block: no command writes one, and a person states where the installation's images are built"
	reasonPrice = "a key of the local config's `compute_prices` block: no command writes one, and it overrides a region's Compute Engine list prices"

	reasonTerraform = "`fugaro setup` writes it from its flags as it applies the installation's root: a Terraform input of the installation"
)

// settingsLabelRows is the table: every row this file answers for. It is
// ordered as the registry is, so the two read side by side.
var settingsLabelRows = []settingsLabelRow{
	{"backend", labelFile, "the compute backend the installation launches runs on, stated in the local config: no command writes it, and cloud-run is the default (docs/backends.md)"},
	{"base_images", labelPlumbing, "`fugaro setup` writes it from --base-image as it installs: the base image each workflow kind builds FROM"},
	{"batch.boot_image", labelFile, reasonBatch},
	{"batch.image", labelFile, reasonBatch},
	{"batch.machine_type", labelFile, reasonBatch},
	{"batch.network", labelFile, reasonBatch},
	{"batch.service_account", labelFile, reasonBatch},
	{"batch.socket_gid", labelFile, reasonBatch},
	{"batch.subnetwork", labelFile, reasonBatch},
	{"batch.task_timeout", labelFile, reasonBatch},
	{"bucket_url", labelFile, "the bucket address a run's task is written to, stated in the local config: `fugaro setup` writes runs_bucket and no command writes this one"},
	{"build.machine_type", labelFile, reasonBuild},
	{"build.region", labelFile, reasonBuild},
	{"build.service_account", labelPlumbing, "`fugaro setup` clears it as it converges, so a build runs as the installation's own account: no settings command writes it"},
	{"compute_prices.*.gib_second_usd", labelFile, reasonPrice},
	{"compute_prices.*.vcpu_second_usd", labelFile, reasonPrice},
	{"credentials_service.url", labelPlumbing, "`fugaro setup` writes it from the apply's outputs, since only the deployment knows the project number: the credentials service's Cloud Run URL"},
	{"endpoints.api_keys", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.artifact_registry", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.batch", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.cloud_asset", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.cloud_billing", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.cloud_build", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.cloud_scheduler", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.compute", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.firebase_database", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.firebase_management", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.firebase_rules", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.firestore", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.iam", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.iam_credentials", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.identity_toolkit", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.logging", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.no_auth", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.resource_manager", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.run", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.secret_manager", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.service_usage", labelFakeEndpoint, reasonEndpoint},
	{"endpoints.storage", labelFakeEndpoint, reasonEndpoint},
	{"image_source", labelPlumbing, "`fugaro setup` writes it as it installs: where the installation's images are built from"},
	{"local.gcp_project", labelPlumbing, "`fugaro setup` writes it when it creates the file: the local config's own GCP project"},
	{"local.version", labelPlumbing, "`fugaro setup` writes it when it creates the file: the local config's own format version"},
	{"log_view", labelPlumbing, "`fugaro setup` writes it from the apply's outputs as it installs: the installation's Cloud Logging view"},
	{"name", labelPlumbing, "`fugaro setup` writes it when it creates the file: the installation's project name, which is never renamed"},
	{"region", labelPlumbing, "`fugaro setup` writes it when it creates the file: the installation's GCP region"},
	{"registry", labelFile, "the legacy shared registry's address, stated in the local config: no command writes it"},
	{"registry_host", labelPlumbing, "`fugaro setup` writes it from the apply's outputs as it installs: the registry's host"},
	{"runs_bucket", labelPlumbing, "`fugaro setup` writes it from the apply's outputs as it installs: the bucket a run's task is written to"},
	{"scheduler_region", labelPlumbing, "`fugaro setup` writes it as it installs: the region the installation's Cloud Scheduler job runs in"},
	{"setup_pending", labelPlumbing, "`fugaro setup` writes and clears it as it installs: the marker an installation carries while one of its steps is unfinished"},
	{"terraform.admins", labelPlumbing, reasonTerraform},
	{"terraform.alert_email", labelPlumbing, reasonTerraform},
	{"terraform.budget_admins", labelPlumbing, reasonTerraform},
	{"terraform.budget_backend", labelPlumbing, reasonTerraform},
	{"terraform.coordinator", labelPlumbing, reasonTerraform},
	{"terraform.no_log_isolation", labelPlumbing, reasonTerraform},
	{"terraform.operators", labelPlumbing, reasonTerraform},
	{"terraform.registry_cleanup", labelPlumbing, reasonTerraform},
	{"terraform.state_bucket", labelPlumbing, reasonTerraform},
	{"user", labelFile, "the person the installation belongs to, stated in the local config: no command writes it"},
	{"gcp_project", labelPlumbing, "`fugaro update --anchor` writes the line into an existing fugaro.yaml as it converges the installation: the repository file's own GCP project"},
	{"profile", labelFile, "the profile the repository file selects: `fugaro config init --profile` writes it when it creates the file, and no command sets it afterwards"},
	{"project", labelFile, "the repository file's project name: `fugaro config init` writes it when it creates the file, and no command sets it afterwards"},
	{"version", labelFile, "the repository file's own schema version: `fugaro config init` writes it when it creates the file, and no command sets it afterwards"},
	{"layer.gcp_project", labelFile, "the project layer file's GCP project: the layer is published from a file a person writes, and no command sets it"},
	{"layer.project", labelFile, "the project layer file's project name: the layer is published from a file a person writes, and no command sets it"},
	{"layer.version", labelFile, "the project layer file's own version: the layer is published from a file a person writes, and no command sets it"},
	{"layer_generation", labelPlumbing, "the layer stage writes it as it publishes: how many times the project layer file has been published (`fugaro update`, `fugaro config publish`)"},
	{"policy_generation", labelPlumbing, "the policy stage writes it as it publishes: the generation of the policy node the installation has published (`fugaro update`)"},
}

func init() {
	for _, r := range settingsLabelRows {
		w := settings.Writer{Store: settings.StoreNone, Label: r.label, Reason: r.reason}
		if err := settings.RegisterWriter(r.pattern, w); err != nil {
			panic(err)
		}
	}
}
