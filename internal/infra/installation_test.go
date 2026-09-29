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
	data, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	keys := jsonKeys(t, data)
	for _, k := range keys {
		if _, ok := vars[k]; !ok {
			t.Errorf("the tfvars key %s is not a variable of the installation root", k)
		}
	}
	for v, required := range vars {
		if required && !slices.Contains(keys, v) {
			t.Errorf("the installation root's required variable %s has no tfvars key", v)
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

func TestDecodeOutputs(t *testing.T) {
	raw := map[string]json.RawMessage{
		"runs_bucket":               json.RawMessage(`"fugaro-runs-proj-1234"`),
		"registry_host":             json.RawMessage(`"us-east5-docker.pkg.dev/proj-1234"`),
		"base_registry":             json.RawMessage(`"fugaro-base"`),
		"legacy_registry":           json.RawMessage(`null`),
		"scheduler_service_account": json.RawMessage(`"fugaro-scheduler@proj-1234.iam.gserviceaccount.com"`),
		"role_ids":                  json.RawMessage(`{"launcher":"projects/proj-1234/roles/fugaroLauncher","job_runner":"projects/proj-1234/roles/fugaroJobRunner","build_submitter":"projects/proj-1234/roles/fugaroBuildSubmitter"}`),
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
		{Address: "module.installation.google_monitoring_alert_policy.image[0]", Change: tf.Change{Actions: []string{"delete"}}},
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
