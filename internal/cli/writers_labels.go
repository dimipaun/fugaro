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
// bookkeeping, `config` and `config init` are the keys a person states in a named
// file. The single place the probe moved a row off its token
// is the `endpoints` block: the registry calls it `setup`'s, and nothing
// outside a test assigns a localcfg.Endpoints field — `fugaro setup` carries
// the block through untouched — so every endpoint carries `fake-endpoint` and
// its reason says so (F18).
//
// The lines below were measured, not guessed: a row whose Owner names a
// command family (`models`, `accounts`, `rigor`, `secrets`, `project`,
// `workspace`, `budget`, the image map, the clone URLs) or whose key the
// generic path's allowlist admits belongs to those writers and is registered by
// them — unless the family does not write it, which its owner token cannot say:
// a token names the family a key belongs to, not that the family writes the
// key, and `secrets` owns `batch.secrets` while writing expiry and rotation and
// no key of the batch block. Those rows are named in settingsUnwrittenKeys
// beside the tokens, so the measurement reaches them (F21). A row another task
// of the plan has given a writer is not labelled here at all: its family
// registers the writer, and labelling it beside that would be a second
// registrant of one pattern, which settings.RegisterWriter panics on in every
// command (docs/contracts/config-ownership.md §4).

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
// states in the file the token names (`config`, `config init`). Every other token
// is a command family's, and a row it owns is that family's to register. The
// machine's own config had a token of its own (`config local`) until R1 moved its
// last two rows — the nudge thresholds — onto `settings set` (D2603).
var settingsUnwrittenOwners = map[string]bool{
	"setup":       true,
	"update":      true,
	"config":      true,
	"config init": true,
}

// settingsUnwrittenKeys are the rows no command writes although their Owner
// names a command family. An owner token is a family's claim on a key and not a
// statement that the family writes it, and the three rows where the two come
// apart are these:
//
//   - F21's `batch.secrets`: `secrets` owns the block naming the Secret Manager
//     secrets a batch job mounts, and the family writes secret expiry and
//     rotation, never a key of the batch block;
//   - R15's `rigor.default` and `rigor.areas` (D3237): `rigor` owns both keys,
//     and since the ruling moved them into the project no command writes a
//     repository's own — `fugaro rigor set --layer repo` is refused for them,
//     and `rigor set`'s own file registers the layer's rows alone. Their labels
//     name the writer of each key's new home.
var settingsUnwrittenKeys = map[string]bool{
	"batch.secrets": true,
	"rigor.areas":   true,
	"rigor.default": true,
}

// Two task-scoped exemptions have stood here and are gone, both because the
// command they named landed: R1's five rows left when `settings set` learned to
// write them, and M4's five — `security.patterns` and the layer's four
// `security.*` rows — when `fugaro rigor security` did. A row of this file is
// now one of the two maps above and nothing else.

// settingsRowNeedsALabel is the measurement the two maps above make: the row is
// one of a token no command answers for, or a key no family writes although one
// owns it. It is the scope of this file, read by the test and by nothing else.
func settingsRowNeedsALabel(r settings.Row) bool {
	return settingsUnwrittenOwners[r.Owner] || settingsUnwrittenKeys[r.Key]
}

const (
	// The endpoint overrides are one block with one answer (F18).
	reasonEndpoint = "an API-root override for a fake installation: `fugaro setup` carries the block through and writes no endpoint, so only a test sets one"

	// U2's three blocks: the registry calls them `setup`'s, and no assignment
	// to one of their localcfg fields exists outside a test, so the labelled
	// shape is the true one rather than `plumbing`, which claims a writer.
	reasonBatch = "a key of the local config's `batch` block: no command writes one, and a batch installation states it as the backend is provisioned"
	// F21: this one's registry owner names a family, and the family does not
	// write it, so the reason says which family and what it writes instead.
	reasonBatchSecrets = "a key of the local config's `batch` block naming the Secret Manager secrets a batch job mounts: the `secrets` family writes secret expiry and rotation and no key of this block, so a batch installation states it as the backend is provisioned"
	// R2 and R16: the opt-in fact is an operator's own, and the two commands
	// that write it are the selection of the backend and the explicit opt-out —
	// C1 lands the fact and BR18 and BR20 land the writers, which then register
	// them and are why this label is not a claim that no command ever will.
	reasonBatchOptIn = "a key of the local config's `batch` block stating whether the installation has opted in to Batch: an operator writes it by selecting the backend and by opting out (R2, R16), and no settings command writes it"
	// R13: the key fact is `fugaro update`'s alone, and the reason says why that
	// is a rule and not a column — a key fact a settings command could write is
	// a substituted public key, and a substituted one hands its holder every
	// seal key wrapped to it.
	reasonBatchSealKey = "a key of the local config's `batch` block: `fugaro update` writes it from the compute root's own output (R13), and no settings command writes it, because a substituted public key would open every seal key wrapped to it"
	reasonBuild        = "a key of the local config's `build` block: no command writes one, and a person states where the installation's images are built"
	reasonPrice        = "a key of the local config's `compute_prices` block: no command writes one, and it overrides a region's Compute Engine list prices"

	reasonTerraform = "`fugaro setup` writes it from its flags as it applies the installation's root: a Terraform input of the installation"

	// R15's three (D3237): a repository's file states neither of the two keys
	// any more, and the project states them. Each reason names the command that
	// writes the key in its new home, because the label is about the spelling
	// the row is and not about the question being open.
	reasonRigorDefault = "a repository's own rigor level, which no command writes any more: the project states it (D3237), and `fugaro rigor set <mode> --repo <owner/name>` writes the `defaults.rigor.repos` entry it resolves to"
	reasonRigorAreas   = "a repository's own rigor areas, which no command writes any more: the project states them (D3237), and `fugaro rigor set <mode> --area <glob> --name <name> --repo <owner/name>` writes the member area of its own"
	reasonRigorRepos   = "the project layer's per-repository rigor levels, which a repository's file may not state at all: `fugaro rigor set <mode> --repo <owner/name>` writes one entry, and a person writes the rest of the map in the layer file"

	// The locations record: `fugaro setup` and `fugaro update` write the
	// block from what the cloud holds, after the last apply (LP14 of
	// docs/location-preference/), and no flag states one — the location of a
	// thing that exists is read, never chosen (R5, D3088). So the row is the
	// converge's bookkeeping and not an editor's.
	reasonLocations = "`fugaro setup` and `fugaro update` write the record from what the cloud holds as they converge the installation: where one of the installation's located things is"

	// RT2's block (docs/model-credentials-routing/, D3182): the project's
	// routing table. `fugaro update`'s migration writes a project's first table
	// (MG2) and `fugaro routing set` edits one after that (RT5), which then
	// registers these patterns and is why this label is not a claim that no
	// command ever will.
	reasonModelRouting = "a key of the local config's `model_routing` block: `fugaro update`'s migration writes a project's first table (MG2) and `fugaro routing set` edits one after that (RT5), and no settings command writes an entry"
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
	{"batch.secrets", labelFile, reasonBatchSecrets},
	{"batch.socket_gid", labelFile, reasonBatch},
	{"batch.subnetwork", labelFile, reasonBatch},
	{"batch.task_timeout", labelFile, reasonBatch},
	{"batch.opt_in", labelFile, reasonBatchOptIn},
	{"batch.seal_key.public_key", labelFile, reasonBatchSealKey},
	{"batch.seal_key.version", labelFile, reasonBatchSealKey},
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
	{"locations.firestore", labelPlumbing, reasonLocations},
	{"locations.log_bucket", labelPlumbing, reasonLocations},
	{"locations.rtdb", labelPlumbing, reasonLocations},
	{"locations.secrets", labelPlumbing, reasonLocations},
	{"model_routing.*.chain.*.admit", labelPlumbing, reasonModelRouting},
	{"model_routing.*.chain.*.kind", labelPlumbing, reasonModelRouting},
	{"model_routing.*.chain.*.model", labelPlumbing, reasonModelRouting},
	{"model_routing.*.chain.*.pool", labelPlumbing, reasonModelRouting},
	{"model_routing.*.chain.*.provider", labelPlumbing, reasonModelRouting},
	{"model_routing.*.match", labelPlumbing, reasonModelRouting},
	{"name", labelPlumbing, "`fugaro setup` writes it when it creates the file: the installation's project name, which is never renamed"},
	{"region", labelPlumbing, "`fugaro setup` writes it when it creates the file: the installation's GCP region"},
	{"registry", labelFile, "the legacy shared registry's address, stated in the local config: no command writes it"},
	{"registry_host", labelPlumbing, "`fugaro setup` writes it from the apply's outputs as it installs: the registry's host"},
	{"rigor.areas", labelFile, reasonRigorAreas},
	{"rigor.default", labelFile, reasonRigorDefault},
	{"rigor.repos", labelFile, reasonRigorRepos},
	{"rigor_pending", labelPlumbing, "`fugaro setup` writes the wizard's rigor answer into it, and the layer stage clears it once the project layer carries the mode: the answer a run that stopped before its Layer stage left for the next one"},
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
