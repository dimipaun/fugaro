package infra

import (
	"encoding/json"
	"io/fs"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// variableRE and outputRE find a root's top-level variable and output
// blocks, as the import checks find resource blocks.
var (
	variableRE = regexp.MustCompile(`(?s)variable "([a-z_]+)" \{(.*?)\n\}`)
	outputRE   = regexp.MustCompile(`(?m)^output "([a-z_]+)" \{`)
	defaultRE  = regexp.MustCompile(`(?m)^\s*default\s*=`)
)

// rootVariables are the installation root's variables, and whether each
// is required (has no default).
func rootVariables(t *testing.T) map[string]bool {
	t.Helper()
	b, err := fs.ReadFile(terraform.FS, "gcp/roots/installation/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]bool{}
	for _, m := range variableRE.FindAllStringSubmatch(string(b), -1) {
		vars[m[1]] = !defaultRE.MatchString(m[2])
	}
	if len(vars) == 0 {
		t.Fatal("no variable found in the installation root")
	}
	return vars
}

func jsonKeys(t *testing.T, data []byte) []string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(doc))
}

// The tfvars fugaro init writes are exactly the root's variables: a key
// the root lacks fails the plan, and a required variable without a key
// fails it too. The outputs fugaro init reads are exactly the root's.
func TestInstallationVarsMatchModule(t *testing.T) {
	vars := rootVariables(t)
	spec := installationSpec(t)
	spec.Budget = &Budget{BillingAccount: "000000-000000-000000", Amount: 50, CurrencyCode: "USD"}
	email := "ops@example.com"
	spec.AlertEmail = &email
	// The default leaves log_isolation out; the rollback's ForgetSpec
	// writes log_isolation=false, which must be a root variable too.
	for name, s := range map[string]InstallationSpec{"default": spec, "forget": ForgetSpec(spec)} {
		data, err := InstallationVars(s)
		if err != nil {
			t.Fatal(err)
		}
		keys := jsonKeys(t, data)
		if name == "forget" && !slices.Contains(keys, "log_isolation") {
			t.Errorf("forget: the tfvars have no log_isolation key")
		}
		for _, k := range keys {
			if _, ok := vars[k]; !ok {
				t.Errorf("%s: the tfvars key %s is not a variable of the installation root", name, k)
			}
		}
		for v, required := range vars {
			if required && !slices.Contains(keys, v) {
				t.Errorf("%s: the installation root's required variable %s has no tfvars key", name, v)
			}
		}
	}

	b, err := fs.ReadFile(terraform.FS, "gcp/roots/installation/outputs.tf")
	if err != nil {
		t.Fatal(err)
	}
	var outputs []string
	for _, m := range outputRE.FindAllStringSubmatch(string(b), -1) {
		outputs = append(outputs, m[1])
	}
	slices.Sort(outputs)
	var tags []string
	typ := reflect.TypeFor[InstallationOutputs]()
	for i := range typ.NumField() {
		tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	if len(outputs) == 0 || !slices.Equal(outputs, tags) {
		t.Errorf("the root's outputs %q are not InstallationOutputs' fields %q", outputs, tags)
	}
}

// The installation root passes each of its variables to the module
// unchanged, so a value the tfvars carry is never dropped on the way. A
// variable the root iterates instead (project_jobs, one module per project
// job) is handed over entry by entry, which is the same guarantee: the whole
// map reaches a module, and for_each = var.x is the only other shape a root
// variable may have.
func TestInstallationRootPassesEveryVariable(t *testing.T) {
	b, err := fs.ReadFile(terraform.FS, "gcp/roots/installation/main.tf")
	if err != nil {
		t.Fatal(err)
	}
	for v := range rootVariables(t) {
		passed := regexp.MustCompile(`(?m)^\s*` + v + `\s*=\s*var\.` + v + `\s*$`).Match(b)
		iterated := regexp.MustCompile(`(?m)^\s*for_each\s*=\s*var\.` + v + `\s*$`).Match(b)
		if !passed && !iterated {
			t.Errorf("the installation root neither passes var.%s to a module nor iterates it", v)
		}
	}
}

// log_isolation is left to the module's default (on) unless it is turned
// off, which only --no-log-isolation and the rollback do.
func TestInstallationLogIsolation(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	s, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := InstallationVars(s)
	if strings.Contains(string(data), "log_isolation") {
		t.Errorf("the default tfvars set log_isolation:\n%s", data)
	}
	s, err = Installation(lc, InstallOptions{NoLogIsolation: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = InstallationVars(s)
	if !strings.Contains(string(data), `"log_isolation": false`) {
		t.Errorf("--no-log-isolation tfvars:\n%s", data)
	}
}

// TestInstallationCredentialsServiceVars is the installation root's side of
// the opt-in (docs/credentials-service, D146, D147; C1.20): an installation
// that has not opted in writes no credentials_service key at all — its plan
// is the one it always was, which is the OFF-by-default half of D147 — and
// the one that has carries the four values the module reads, with the bundle
// container's ID derived the way the loader derives it (the project-wide
// secret of CredentialsBundleName).
//
// TestInstallationVarsMatchModule beside it is what holds the key to the
// root's own variable: this test is about the value.
func TestInstallationCredentialsServiceVars(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	s, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := InstallationVars(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "credentials_service") {
		t.Errorf("an installation that has not opted in writes credentials_service:\n%s", data)
	}
	// The service on: the digest-pinned image the stage verified, and the one
	// bundle container of Q6.
	image := "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.3@sha256:" + strings.Repeat("ab", 32)
	svc, err := Credentials(lc, image)
	if err != nil {
		t.Fatal(err)
	}
	s, err = Installation(lc, InstallOptions{CredentialsService: &svc})
	if err != nil {
		t.Fatal(err)
	}
	data, err = InstallationVars(s)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		CredentialsService *CredentialsServiceSpec `json:"credentials_service"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.CredentialsService == nil {
		t.Fatalf("the opt-in's tfvars have no credentials_service:\n%s", data)
	}
	if got := *doc.CredentialsService; got.Image != image || got.AccountID != CredentialsAccountID || got.Service != CredentialsServiceName {
		t.Errorf("credentials_service = %+v, want the pinned image beside the account and service names", got)
	}
	// The literal, not the constant: the loader derives this name itself
	// (C1.11's CredsBundleSecretID, "creds-bundle"), and the two must be the
	// one string (modules/installation/credentials.tf's own comment says so).
	if want := gcp.ProjectSecretID(lc.Name, "creds-bundle"); doc.CredentialsService.BundleSecret != want {
		t.Errorf("bundle_secret = %q, want %q: the module creates the container the loader derives by the same name", doc.CredentialsService.BundleSecret, want)
	}
}

// TestCredentialsRefusesAnImageThatIsNotADigest: the stage's own half of the
// module's image rule (variables.tf's validation, and why it is a security
// rule rather than a preference: every operator holds
// roles/artifactregistry.writer on the registry the image comes from, and
// this service runs as the account that reads every credential in the
// bundle). A tag is refused here, before a tfvars is written, so the plan
// never sees one; a digest with a tag BESIDE it is what the stage writes and
// is admitted (mirror.RemoteRef's form, tf.cover's own case).
func TestCredentialsRefusesAnImageThatIsNotADigest(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	digest := "@sha256:" + strings.Repeat("ab", 32)
	reg := "us-east5-docker.pkg.dev/proj-1234/"
	for image, ok := range map[string]bool{
		reg + "fugaro-ghcr/dimipaun/fugaro-credentials:1.2.3" + digest:            true,
		reg + "fugaro-base/fugaro-credentials" + digest:                           true,
		reg + "fugaro-base/fugaro-credentials:latest":                             false,
		reg + "fugaro-ghcr/dimipaun/fugaro-credentials:1.2.3":                     false,
		reg + "fugaro-base/fugaro-credentials@sha256:" + strings.Repeat("AB", 32): false, // upper case is not a digest
		"": false,
	} {
		svc, err := Credentials(lc, image)
		switch {
		case ok && err != nil:
			t.Errorf("Credentials(%q) = %v, want it admitted", image, err)
		case ok && !strings.Contains(svc.Image, "@sha256:"):
			t.Errorf("Credentials(%q) lost the digest: %q", image, svc.Image)
		case !ok && err == nil:
			t.Errorf("Credentials(%q) was admitted; it is not pinned by digest", image)
		case !ok && !strings.Contains(err.Error(), "@sha256:"):
			t.Errorf("the refusal of %q does not say what is missing: %v", image, err)
		}
	}
}

// The installation spec carries the launchers' condition exactly as
// gcp.LauncherBucketCondition spells it, for the spec's own runs bucket.
func TestInstallationSpecCarriesTheLauncherCondition(t *testing.T) {
	spec := installationSpec(t)
	want := Condition{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(spec.RunsBucket)}
	if spec.LauncherBucketCondition != want {
		t.Fatalf("launcher condition = %+v, want %+v", spec.LauncherBucketCondition, want)
	}
	data, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		C Condition `json:"launcher_bucket_condition"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.C != want {
		t.Fatalf("tfvars launcher_bucket_condition = %+v (%v), want %+v", doc.C, err, want)
	}
}

// The remote repository's name travels to the installation root with the
// other registry names: the module's own validation names it (variables.tf
// lists each registry ID one by one, so a new one is unvalidated until it
// is listed), and every reader of the pin reads it from the installation's
// outputs (design §3.1, §5.3).
func TestInstallationNamesCarryTheRemoteRepository(t *testing.T) {
	spec := installationSpec(t)
	if spec.Names.GhcrRegistry != GhcrRegistry {
		t.Fatalf("names.ghcr_registry = %q, want %q", spec.Names.GhcrRegistry, GhcrRegistry)
	}
	if GhcrRegistry != "fugaro-ghcr" {
		t.Errorf("GhcrRegistry = %q, want fugaro-ghcr: it must satisfy cover.go's repoID and be a registry ID the module validates", GhcrRegistry)
	}
	data, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Names struct {
			GhcrRegistry string `json:"ghcr_registry"`
		} `json:"names"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Names.GhcrRegistry != GhcrRegistry {
		t.Errorf("tfvars names.ghcr_registry = %q, want %q", doc.Names.GhcrRegistry, GhcrRegistry)
	}
}

func TestDecodeOutputs(t *testing.T) {
	raw := map[string]json.RawMessage{
		"runs_bucket":               json.RawMessage(`"fugaro-runs-proj-1234"`),
		"registry_host":             json.RawMessage(`"us-east5-docker.pkg.dev/proj-1234"`),
		"base_registry":             json.RawMessage(`"fugaro-base"`),
		"ghcr_registry":             json.RawMessage(`"fugaro-ghcr"`),
		"legacy_registry":           json.RawMessage(`null`),
		"scheduler_service_account": json.RawMessage(`"fugaro-scheduler@proj-1234.iam.gserviceaccount.com"`),
		"role_ids":                  json.RawMessage(`{"launcher":"projects/proj-1234/roles/fugaroLauncher","job_runner":"projects/proj-1234/roles/fugaroJobRunner","build_submitter":"projects/proj-1234/roles/fugaroBuildSubmitter","tag_mover":"projects/proj-1234/roles/fugaroTagMover"}`),
		"launchers":                 json.RawMessage(`["user:a@example.com"]`),
		"operators":                 json.RawMessage(`[]`),
		"log_view":                  json.RawMessage(`"projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs"`),
		"registry_cleanup_dry_run":  json.RawMessage(`true`),
	}
	o, err := DecodeOutputs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if o.RunsBucket != "fugaro-runs-proj-1234" || o.LegacyRegistry != "" || o.RoleIDs.JobRunner != "projects/proj-1234/roles/fugaroJobRunner" ||
		o.GhcrRegistry != GhcrRegistry ||
		o.RoleIDs.TagMover != "projects/proj-1234/roles/fugaroTagMover" ||
		!slices.Equal(o.Launchers, []string{"user:a@example.com"}) || o.LogView == "" || o.RegistryCleanupDryRun == nil || !*o.RegistryCleanupDryRun {
		t.Fatalf("outputs = %+v", o)
	}
	if _, err := DecodeOutputs(map[string]json.RawMessage{"runs_bucket": json.RawMessage(`42`)}); err == nil {
		t.Error("an output of the wrong type decoded")
	}
	if _, err := DecodeOutputs(map[string]json.RawMessage{}); err == nil {
		t.Error("no outputs (an empty state) decoded as an installation")
	}
}

func TestCountPlan(t *testing.T) {
	p := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		{Address: "a", Change: tf.Change{Actions: []string{"no-op"}, Importing: &tf.Importing{ID: "x"}}},
		{Address: "b", Change: tf.Change{Actions: []string{"update"}, Importing: &tf.Importing{ID: "y"}}},
		{Address: "c", Change: tf.Change{Actions: []string{"create"}}},
		{Address: "d", Change: tf.Change{Actions: []string{"update"}}},
		{Address: "e", Change: tf.Change{Actions: []string{"delete"}}},
		{Address: "f", Change: tf.Change{Actions: []string{"delete", "create"}}},
		{Address: "g", Change: tf.Change{Actions: []string{"no-op"}}},
	}}
	got := CountPlan(p)
	want := PlanCounts{Imports: 2, Creates: 1, Updates: 2, Deletes: 1, Replaces: 1}
	if got != want {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	if s := got.String(); s != "2 imports, 1 creates, 2 updates, 1 deletes, 1 replaces" {
		t.Errorf("String() = %q", s)
	}
	if s := (PlanCounts{Creates: 3}).String(); s != "0 imports, 3 creates, 0 updates" {
		t.Errorf("String() = %q", s)
	}
}

func TestAdoptsRunsBucket(t *testing.T) {
	im := Imports{List: []Import{newImport(importBaseRegistry, "proj-1234", "us-east5", "", BaseRegistry)}}
	if im.AdoptsRunsBucket() {
		t.Fatal("adopts the runs bucket without its import")
	}
	im.List = append(im.List, newImport(importRunsBucket, "proj-1234", "us-east5", "", "fugaro-runs-proj-1234"))
	if !im.AdoptsRunsBucket() {
		t.Fatal("the runs bucket's import is not seen")
	}
}

// A refused delete of something only flags declare says which flags keep it.
func TestDeleteHints(t *testing.T) {
	p := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		{Address: "module.installation.google_billing_budget.this[0]", Change: tf.Change{Actions: []string{"delete"}}},
		{Address: "module.installation.data.google_project.this[0]", Change: tf.Change{Actions: []string{"delete"}}},
		{Address: "module.installation.google_monitoring_alert_policy.image[\"check\"]", Change: tf.Change{Actions: []string{"delete"}}},
		{Address: "module.installation.google_monitoring_notification_channel.email[0]", Change: tf.Change{Actions: []string{"delete"}}},
		{Address: `module.installation.google_project_iam_member.launcher["user:a@example.com"]`, Change: tf.Change{Actions: []string{"delete"}}},
		{Address: "module.installation.google_storage_bucket.runs", Change: tf.Change{Actions: []string{"update"}}},
	}}
	hints := DeleteHints(p, nil)
	if len(hints) != 3 || !strings.Contains(hints[0], "--budget, --budget-currency and --billing-account") ||
		!strings.Contains(hints[1], "--alert-email") || !strings.Contains(hints[2], "--launcher") {
		t.Fatalf("hints = %q", hints)
	}
	if hints := DeleteHints(p, []string{"module.installation.google_billing_budget.this[0]"}); len(hints) != 2 {
		t.Fatalf("an allowed delete still hinted: %q", hints)
	}
}

// An API only the local config or the budget flags declare, planned away,
// says where each comes from.
func TestDeleteHintsAPIs(t *testing.T) {
	p := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		{Address: `module.installation.google_project_service.this["aiplatform.googleapis.com"]`, Change: tf.Change{Actions: []string{"delete"}}},
		{Address: `module.installation.google_project_service.this["billingbudgets.googleapis.com"]`, Change: tf.Change{Actions: []string{"delete"}}},
	}}
	hints := DeleteHints(p, nil)
	if len(hints) != 1 {
		t.Fatalf("hints = %q, want one about the APIs", hints)
	}
	for _, want := range []string{"Vertex AI", "repos.<repo>.vertex", "fugaro init --repo", "--budget"} {
		if !strings.Contains(hints[0], want) {
			t.Errorf("the APIs' hint lacks %q: %q", want, hints[0])
		}
	}
}

// managed_deployer_scoped is left out of the tfvars unless the installation
// opts in, so a default installation's tfvars are the same bytes as before the
// variable existed and its plan has no scoped deployer role or binding (0.8.6
// gates them off: deploy/terraform/gcp/modules/installation/deployer.tf). The
// opt-in writes the key, which must be a variable of the installation root.
func TestInstallationManagedDeployerScopedIsOptIn(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	s, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := InstallationVars(s)
	if strings.Contains(string(data), "managed_deployer_scoped") {
		t.Errorf("the default tfvars name managed_deployer_scoped:\n%s", data)
	}
	s, err = Installation(lc, InstallOptions{ManagedDeployerScoped: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = InstallationVars(s)
	if !slices.Contains(jsonKeys(t, data), "managed_deployer_scoped") || !strings.Contains(string(data), `"managed_deployer_scoped": true`) {
		t.Errorf("the opt-in's tfvars do not set managed_deployer_scoped to true:\n%s", data)
	}
	if _, ok := rootVariables(t)["managed_deployer_scoped"]; !ok {
		t.Error("managed_deployer_scoped is not a variable of the installation root")
	}
}
