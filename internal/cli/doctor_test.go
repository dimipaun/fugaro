package cli

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/basepin"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// doctorRig is a local project config ("aurora", GCP proj-1234) pointing at
// fresh Resource Manager, Billing and Secret Manager fakes, with no
// installation other than that config.
type doctorRig struct {
	dir     string
	cfgPath string
	cfg     string
	crm     *gcpfake.CRM
	billing *gcpfake.Billing
	sm      *gcpfake.Secrets
	// run is the Cloud Run fake the project-secret scope check reads a
	// deployed job's own mounted secret references from. No test has to use
	// it: a rig with no job in it reports nothing.
	run *gcpfake.Run
}

func newDoctorRig(t *testing.T) *doctorRig {
	t.Helper()
	dir := t.TempDir()
	// The rig runs the whole command, catalog check included, on a clock
	// inside the shipped catalog's own window: a check that turns into a
	// warning as the file ages (the catalog's 30 days, catalog.StaleAfter)
	// must not decide whether these tests pass, and --strict fails on a
	// warning.
	pinCatalogClock(t, shippedCatalog(t), 24*time.Hour)
	r := &doctorRig{dir: dir, crm: gcpfake.NewCRM(t), billing: gcpfake.NewBilling(t), sm: gcpfake.NewSecrets(t), run: gcpfake.NewRun(t)}
	r.crm.AddProject("proj-1234", 123456789012)
	r.crm.SetPolicy("proj-1234", gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}})
	r.billing.SetBilling("proj-1234", true)
	for _, k := range []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "GODEBUG"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	// Hermetic: a stand-in terraform (CI has none), no real Claude Code
	// config, and a fixed binary version.
	withVersion(t, "0.2.0")
	doctorFakeTerraform(t, dir)
	path := isolateProjects(t, dir)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"user: someone@example.com\n" +
		"endpoints: { resource_manager: " + r.crm.URL + "/, cloud_billing: " + r.billing.URL + "/, secret_manager: " + r.sm.URL + "/, run: " + r.run.URL + "/, no_auth: true }\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfgPath, r.cfg = path, cfg
	return r
}

// doctorFakeTerraform makes doctor find a supported terraform, whatever the
// machine running the tests has installed.
func doctorFakeTerraform(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "fake-terraform")
	script := "#!/bin/sh\necho '{\"terraform_version\":\"1.9.0\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := doctorLookPath
	doctorLookPath = func(string) (string, error) { return bin, nil }
	t.Cleanup(func() { doctorLookPath = old })
}

// putSecret stores value for secret name of repository slug directly
// through the production Secrets client against the rig's fake, exactly as
// `fugaro secrets set` would.
func putSecret(t *testing.T, sm *gcpfake.Secrets, project, slug, name string, value []byte) {
	t.Helper()
	ctx := context.Background()
	s, err := gcp.NewSecrets(ctx, gcp.Options{GCPProject: project, Endpoints: gcp.Endpoints{SecretManager: sm.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.SecretID(slug, name)
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: name}
	if _, err := s.Set(ctx, id, value, labels); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorWithNoInstallationSaysRunInit: with no project config anywhere,
// doctor says so and names fugaro setup as the fix, rather than guessing at
// one or crashing.
func TestDoctorWithNoInstallationSaysRunInit(t *testing.T) {
	isolateProjects(t, t.TempDir())
	out, _, err := execute(t, "doctor", "--dir", t.TempDir())
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "fugaro setup") {
		t.Fatalf("exit %d, err %v, out %q", ExitCode(err), err, out)
	}

	out, _, err = execute(t, "doctor", "--json", "--dir", t.TempDir())
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "installation")
	if !ok || c.OK || !strings.Contains(c.Fix, "fugaro setup") {
		t.Fatalf("installation check = %+v", c)
	}
}

func doctorCheckByID(checks []doctorCheck, id string) (doctorCheck, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c, true
		}
	}
	return doctorCheck{}, false
}

// TestDoctorReadOnly: doctor's Google calls are reads only, whatever it
// finds (the same discipline preflight's own tests hold it to).
func TestDoctorReadOnly(t *testing.T) {
	r := newDoctorRig(t)
	_, _, _ = execute(t, "doctor", "--dir", t.TempDir())
	for _, req := range r.crm.Requests() {
		if req.Method != "GET" && !(req.Method == "POST" && strings.HasSuffix(req.Path, ":getIamPolicy")) {
			t.Errorf("doctor made a non-read call to the Resource Manager fake: %s %s", req.Method, req.Path)
		}
	}
	for _, req := range r.billing.Requests() {
		if req.Method != "GET" {
			t.Errorf("doctor made a non-read call to the Billing fake: %s %s", req.Method, req.Path)
		}
	}
}

// TestDoctorJSONShape: `doctor --json` is one JSON object with the checks,
// the project and the plugin report, whatever their outcome.
func TestDoctorJSONShape(t *testing.T) {
	newDoctorRig(t)
	out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if o.Project == nil || o.Project.Name != "aurora" || o.Project.GCPProject != "proj-1234" {
		t.Fatalf("project = %+v", o.Project)
	}
	if len(o.Checks) == 0 {
		t.Fatal("no checks reported")
	}
	for _, c := range o.Checks {
		if c.ID == "" {
			t.Fatalf("a check has no id: %+v", c)
		}
	}
}

// With no base_images.base, doctor names the one fix, as a warning: an
// installation mid-migration still works for everything else.
func TestDoctorSaysWhenTheBaseIsMissing(t *testing.T) {
	newDoctorRig(t)
	out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	for _, c := range o.Checks {
		if c.ID == "base-image" {
			if c.OK || c.Severity != "warning" || !strings.Contains(c.Fix, "fugaro image refresh") {
				t.Errorf("base-image = %+v", c)
			}
			return
		}
	}
	t.Errorf("no base-image check in %+v", o.Checks)
}

// TestDoctorNamesTheFixLine: every failing check, whatever it is, names a
// fix a person can run.
func TestDoctorNamesTheFixLine(t *testing.T) {
	r := newDoctorRig(t)
	r.billing.SetBilling("proj-1234", false)
	r.crm.SetPolicy("proj-1234", gcpfake.Binding{
		Role: "roles/editor", Members: []string{"serviceAccount:123456789012-compute@developer.gserviceaccount.com"},
	})
	out, _, err := execute(t, "doctor", "--json", "--dir", t.TempDir())
	if err == nil {
		t.Fatal("want doctor to fail: billing is not linked")
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	failed := 0
	for _, c := range o.Checks {
		if c.OK {
			continue
		}
		failed++
		if c.Fix == "" {
			t.Errorf("check %s has no fix line: %+v", c.ID, c)
		}
	}
	if failed == 0 {
		t.Fatal("expected at least one failing check")
	}
}

// TestDoctorRefusesCloudBatch: `backend: cloud-batch` is a name this build
// knows but must not run on yet -- the seam and the fake exist, the tasks
// that make a Batch run safe (the log sink, the cost of a VM, the reaper)
// come after the owner's Check 57a measurement (design compute-backend.md
// §8.1 Task 2, §9). doctor must say so rather than reporting the project
// healthy and leaving the refusal to a launch.
func TestDoctorRefusesCloudBatch(t *testing.T) {
	r := newDoctorRig(t)
	if err := os.WriteFile(r.cfgPath, []byte(r.cfg+"backend: cloud-batch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "doctor", "--json", "--dir", t.TempDir())
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v, out %q", ExitCode(err), err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	// doctor cannot check a project whose config it cannot read, so the
	// refusal is the run's error, and it names the backend that was refused
	// and the one this build runs.
	if !strings.Contains(o.Error, "cloud-batch") || !strings.Contains(o.Error, "cloud-run") {
		t.Fatalf("error = %q, want it to name both backends", o.Error)
	}
}

// TestDoctorStrictPlugin: a stale pin fails doctor only with --strict; a
// current one never does.
func TestDoctorStrictPlugin(t *testing.T) {
	withVersion(t, "0.2.0")
	for _, tc := range []struct {
		name            string
		ref             string
		failsWithStrict bool
	}{
		{"ok", "v0.2.0", false},
		{"outdated", "v0.1.0", true},
		{"newer", "v0.3.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := skillCheckout(t, wiredAt(tc.ref))
			if _, _, err := execute(t, "doctor", "--plugin", "--dir", root); err != nil {
				t.Fatalf("without --strict: %v", err)
			}
			_, _, err := execute(t, "doctor", "--plugin", "--strict", "--dir", root)
			if failed := err != nil; failed != tc.failsWithStrict {
				t.Fatalf("--strict: failed = %v, want %v (err %v)", failed, tc.failsWithStrict, err)
			}
		})
	}
}

// TestDoctorStrictFailsForeignAndUnpinned: foreign, unpinned and unwired
// plugin states are reported either way, but only fail doctor with
// --strict: the repository's CI is where this is meant to be enforced, a
// developer's machine is not.
func TestDoctorStrictFailsForeignAndUnpinned(t *testing.T) {
	withVersion(t, "0.2.0")
	foreign := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"someone/fork","ref":"v0.2.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
	for _, tc := range []struct{ name, content string }{
		{"foreign", foreign},
		{"unpinned", wiredAt("")},
		{"not-wired", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := skillCheckout(t, tc.content)
			if _, _, err := execute(t, "doctor", "--plugin", "--dir", root); err != nil {
				t.Fatalf("without --strict: %v", err)
			}
			if _, _, err := execute(t, "doctor", "--plugin", "--strict", "--dir", root); err == nil {
				t.Fatal("--strict: want a failure")
			}
		})
	}
}

// TestDoctorOpenProvidersIsInformational: doctor names every provider whose
// project config leaves it open to every repository (an absent
// allow_data_to, or ["*"] -- design §3a), as an informational finding: the
// owner ruled that state valid, so it informs and never fails doctor, with
// --strict or without.
func TestDoctorOpenProvidersIsInformational(t *testing.T) {
	r := newDoctorRig(t)
	doctorJSON := func(t *testing.T) doctorOutput {
		t.Helper()
		out, _, err := execute(t, "doctor", "--json", "--dir", t.TempDir())
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		var o doctorOutput
		if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
			t.Fatalf("json: %v\n%s", jerr, out)
		}
		return o
	}
	// The rig has failures of its own (no checkout for plugin-pin, no base
	// image), so strictness is compared rather than expected to pass: the
	// set of checks that fail under --strict must be the same one as before
	// the provider existed.
	failing := func(t *testing.T) map[string]string {
		t.Helper()
		out, _, _ := execute(t, "doctor", "--strict", "--json", "--dir", t.TempDir())
		var o doctorOutput
		if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
			t.Fatalf("json: %v\n%s", jerr, out)
		}
		m := map[string]string{}
		for _, c := range o.Checks {
			// "info" is what checksFail never fails on, so an informational
			// finding is not part of the verdict either way.
			if !c.OK && c.Severity != "info" {
				m[c.ID] = c.Severity
			}
		}
		return m
	}
	before := failing(t)
	if _, ok := before["provider-open-to-every-repository"]; ok {
		t.Fatal("reported with no providers configured")
	}
	addLocalLines(t, r, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n"+
		"    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n")
	o := doctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, "provider-open-to-every-repository")
	if !ok || c.OK || c.Severity != "info" {
		t.Fatalf("provider-open-to-every-repository = %+v", c)
	}
	if !strings.Contains(c.Problem, "provider openrouter has no allow_data_to") || !strings.Contains(c.Problem, "may mount its key") {
		t.Fatalf("problem = %q", c.Problem)
	}
	if !strings.Contains(c.Fix, "allow_data_to") || !strings.Contains(c.Fix, "update --repo") {
		t.Fatalf("fix = %q", c.Fix)
	}
	if !o.OK {
		t.Fatalf("an informational state failed doctor: %+v", o)
	}
	if after := failing(t); !maps.Equal(before, after) {
		t.Fatalf("--strict: the finding changed the verdict: before %v, after %v", before, after)
	}
}

// TestDoctorProviderSecretNameIsInformational: doctor names every provider
// whose key is not stored under the name the convention gives it
// (<provider>-api-key), as an informational finding, and says the two halves
// that make it information rather than a failure: nothing is renamed, and the
// conventional name is reserved for that provider either way. The owner ruled
// the off-convention state valid (2026-10-18), so it never fails doctor, with
// --strict or without, and an on-convention installation reports nothing at
// all.
func TestDoctorProviderSecretNameIsInformational(t *testing.T) {
	r := newDoctorRig(t)
	// The rig has failures of its own (no checkout for plugin-pin, no base
	// image), so strictness is compared rather than expected to pass: the set
	// of checks that fail under --strict must be the same one as before the
	// provider existed.
	failing := func(t *testing.T) map[string]string {
		t.Helper()
		out, _, _ := execute(t, "doctor", "--strict", "--json", "--dir", t.TempDir())
		var o doctorOutput
		if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
			t.Fatalf("json: %v\n%s", jerr, out)
		}
		m := map[string]string{}
		for _, c := range o.Checks {
			if !c.OK && c.Severity != "info" {
				m[c.ID] = c.Severity
			}
		}
		return m
	}
	before := failing(t)
	// On convention: openrouter's key IS openrouter-api-key, and deepseek's
	// entry spells the algorithm it means, so nothing is reported -- the
	// warning is about a name, not about a provider existing.
	addLocalLines(t, r, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n"+
		"    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n")
	if _, ok := doctorCheckByID(doctorJSON(t).Checks, "provider-secret-name"); ok {
		t.Fatal("an on-convention key was reported")
	}
	// Off convention: the finding names the entry, the name it uses and the
	// name the convention gives it.
	addLocalLines(t, r, "  deepseek:\n    kind: anthropic-compat\n    base_url: https://api.deepseek.com/anthropic\n"+
		"    auth: bearer\n    secret: ds-key\n    models: [\"deepseek-chat\"]\n")
	o := doctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, "provider-secret-name")
	if !ok || c.OK || c.Severity != "info" {
		t.Fatalf("provider-secret-name = %+v", c)
	}
	for _, want := range []string{"deepseek", "ds-key", "deepseek-api-key"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the problem does not say %q: %s", want, c.Problem)
		}
	}
	if strings.Contains(c.Problem, "openrouter") {
		t.Errorf("an on-convention provider is named: %s", c.Problem)
	}
	for _, want := range []string{"rename nothing", "reserved"} {
		if !strings.Contains(c.Fix, want) {
			t.Errorf("the fix does not say %q: %s", want, c.Fix)
		}
	}
	if !o.OK {
		t.Fatalf("an informational state failed doctor: %+v", o)
	}
	if after := failing(t); !maps.Equal(before, after) {
		t.Fatalf("--strict: the finding changed the verdict: before %v, after %v", before, after)
	}
}

// TestDoctorNotInstalledIsInformational: a correctly pinned plugin nobody
// has installed yet is informational, under --strict or not: a teammate who
// hasn't opened the folder in Claude Code is not a CI failure.
func TestDoctorNotInstalledIsInformational(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	out, _, err := execute(t, "doctor", "--plugin", "--json", "--dir", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "plugin-install")
	if !ok || c.OK || c.Severity != "info" {
		t.Fatalf("plugin-install = %+v", c)
	}
	if !strings.Contains(c.Fix, "/plugin install fugaro@fugaro") || strings.Contains(c.Fix, "by itself") {
		t.Fatalf("plugin-install fix = %q", c.Fix)
	}
	if !o.OK {
		t.Fatalf("an informational state failed doctor: %+v", o)
	}
	if _, _, err := execute(t, "doctor", "--plugin", "--strict", "--dir", root); err != nil {
		t.Fatalf("--strict: %v", err)
	}
}

// TestDoctorFugaroYAML: a checkout's fugaro.yaml validity is both its own
// check (fugaro-yaml, with a fix line) and the fugaro_yaml detail, and an
// invalid file fails doctor's overall result, not just that one field.
func TestDoctorFugaroYAML(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		newDoctorRig(t)
		checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
		out, _, err := execute(t, "doctor", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		var o doctorOutput
		if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
			t.Fatalf("json: %v\n%s", jerr, out)
		}
		c, ok := doctorCheckByID(o.Checks, "fugaro-yaml")
		if !ok || !c.OK || c.Problem != "" || c.Fix != "" {
			t.Fatalf("fugaro-yaml check = %+v", c)
		}
		if o.FugaroYAML == nil || !o.FugaroYAML.Valid || len(o.FugaroYAML.Problems) != 0 {
			t.Fatalf("fugaro_yaml = %+v", o.FugaroYAML)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		newDoctorRig(t)
		invalid := strings.Replace(cliMinimalYAML, "sh build.sh", "./build.sh", 1)
		checkoutWith(t, map[string]string{"fugaro.yaml": invalid})
		out, _, err := execute(t, "doctor", "--json")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d, err %v, out %s", ExitCode(err), err, out)
		}
		var o doctorOutput
		if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
			t.Fatalf("json: %v\n%s", jerr, out)
		}
		if o.OK {
			t.Fatalf("an invalid fugaro.yaml must fail doctor's overall result: %+v", o)
		}
		c, ok := doctorCheckByID(o.Checks, "fugaro-yaml")
		if !ok || c.OK {
			t.Fatalf("fugaro-yaml check = %+v", c)
		}
		if !strings.Contains(c.Problem, "1 problem") || c.Fix == "" {
			t.Fatalf("fugaro-yaml check didn't name the problem and a fix: %+v", c)
		}
		if o.FugaroYAML == nil || o.FugaroYAML.Valid || len(o.FugaroYAML.Problems) != 1 ||
			!strings.Contains(o.FugaroYAML.Problems[0].Message, "does not exist") {
			t.Fatalf("fugaro_yaml = %+v", o.FugaroYAML)
		}
	})
}

// TestDoctorNeverPrintsSecretValues: doctor lists a repository's secrets by
// name (design: "secrets by name from secrets ls"), and the value never
// reaches stdout, stderr or --json, in either outcome. The list is context and
// not work, so it prints under --all: the default view is the verdict and what
// to do, and a clean installation is one line.
func TestDoctorNeverPrintsSecretValues(t *testing.T) {
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	const value = "super-secret-token-value-ABC123"
	putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), "claude-oauth-token", []byte(value))

	out, errOut, err := execute(t, "doctor", "--all")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if strings.Contains(out, value) || strings.Contains(errOut, value) {
		t.Fatalf("the value leaked: out %q, err %q", out, errOut)
	}
	if !strings.Contains(out, "claude-oauth-token") {
		t.Fatalf("doctor didn't name the secret: %q", out)
	}

	outJSON, errJSON, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, errJSON)
	}
	if strings.Contains(outJSON, value) {
		t.Fatalf("the value leaked in --json: %q", outJSON)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(outJSON), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, outJSON)
	}
	if len(o.Secrets) != 1 || o.Secrets[0].Name != "claude-oauth-token" {
		t.Fatalf("secrets = %+v", o.Secrets)
	}
}

// TestDoctorPluginIsOffline: --plugin never touches the cloud: with a local
// installation pointing at fakes, not one request reaches them, and no
// installation or credentials are needed at all.
func TestDoctorPluginIsOffline(t *testing.T) {
	r := newDoctorRig(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent")
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	if out, _, err := execute(t, "doctor", "--plugin", "--strict", "--dir", root); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := len(r.crm.Requests()) + len(r.billing.Requests()) + len(r.sm.Requests()); n != 0 {
		t.Fatalf("--plugin made %d cloud calls", n)
	}

	isolateProjects(t, t.TempDir()) // and no installation configured
	if out, _, err := execute(t, "doctor", "--plugin", "--dir", root); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestDoctorStrictIgnoresInstalledState: a CI runner has no Claude Code
// install, so the installed state is not evaluated under --plugin --strict
// (design 4.5), though plain doctor --plugin still reports it.
func TestDoctorStrictIgnoresInstalledState(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	cfg := os.Getenv("CLAUDE_CONFIG_DIR")
	if err := os.MkdirAll(filepath.Join(cfg, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	rb, _ := json.Marshal(root)
	installed := `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"project","projectPath":` + string(rb) + `,"version":"0.1.0"}]}}`
	if err := os.WriteFile(filepath.Join(cfg, "plugins", "installed_plugins.json"), []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := execute(t, "doctor", "--plugin", "--json", "--dir", root)
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "plugin-install"); !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("plain doctor must report the installed state as a warning: %+v", c)
	}
	out, _, err := execute(t, "doctor", "--plugin", "--strict", "--json", "--dir", root)
	if err != nil {
		t.Fatalf("--strict must not evaluate the installed state: %v\n%s", err, out)
	}
	if strings.Contains(out, "plugin-install") {
		t.Fatalf("--strict evaluated the installed state: %s", out)
	}
}

// TestDoctorStrictOutsideCheckoutFails: no checkout means no wiring to
// vouch for: a warning, so CI mis-pointed at a bare directory fails
// rather than passing silently.
func TestDoctorStrictOutsideCheckoutFails(t *testing.T) {
	withVersion(t, "0.2.0")
	dir := t.TempDir()
	if _, _, err := execute(t, "doctor", "--plugin", "--dir", dir); err != nil {
		t.Fatalf("without --strict: %v", err)
	}
	if _, _, err := execute(t, "doctor", "--plugin", "--strict", "--dir", dir); err == nil {
		t.Fatal("--strict: want a failure outside a checkout")
	}
}

// TestDoctorIsDeterministic: the same state prints the same bytes, with
// several secrets stored, listed sorted by name.
func TestDoctorIsDeterministic(t *testing.T) {
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	for _, n := range []string{"zeta", "alpha", "mid"} {
		putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), n, []byte("v-"+n))
	}
	first, _, _ := execute(t, "doctor", "--json")
	for range 3 {
		if again, _, _ := execute(t, "doctor", "--json"); again != first {
			t.Fatalf("output differs between runs:\n%s\n---\n%s", first, again)
		}
	}
	var o doctorOutput
	if err := json.Unmarshal([]byte(first), &o); err != nil {
		t.Fatal(err)
	}
	if len(o.Secrets) != 3 || o.Secrets[0].Name != "alpha" || o.Secrets[2].Name != "zeta" {
		t.Fatalf("secrets not sorted by name: %+v", o.Secrets)
	}
}

// doctorApp is a doctor rig whose config records the GitHub repository
// acme/app with App 12345, its key stored in the fake Secret Manager, and a
// GitHub that answers the installation lookup.
func doctorApp(t *testing.T, status int, perms map[string]string) (*doctorRig, *[]string) {
	t.Helper()
	r := newDoctorRig(t)
	r.sm.AllowAccess()
	path, err := localcfg.ProjectPath(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, "repos:\n  acme/app: { provider: github, github_app_id: \"12345\", workflows: [app] }\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), "github-app-key", []byte(appKeyPEM(t)))
	auth := gitHubAppServer(t, status, perms)
	return r, auth
}

// doctorAppChecks is doctorJSON with the App check's own flag: doctor's
// checks, with each recorded GitHub App's installation looked up.
func doctorAppChecks(t *testing.T) []doctorCheck {
	t.Helper()
	return doctorJSON(t, "--check-github-app").Checks
}

// grantBuildAccount grants the project-wide secret's accessor role to repo's
// build service account, as the apply that resolved that name to the
// project copy did (grantedTo is true) -- or to nothing at all, the
// pre-first-apply state, when it is false.
func grantBuildAccount(t *testing.T, r *doctorRig, secretID, repo string, grantedTo bool) {
	t.Helper()
	member := buildAccountMember(gcp.BuildServiceAccountID(mustSlug("github", repo)) + "@" + initProject + ".iam.gserviceaccount.com")
	var bindings []gcpfake.Binding
	if grantedTo {
		bindings = []gcpfake.Binding{{Role: gcp.SecretAccessorRole, Members: []string{member}}}
	}
	r.sm.SetPolicy(secretID, bindings)
}

// The doctor GitHub App check reads the key live state resolves for
// github-app-key, not the repository's own ID unconditionally (small item
// 5): with the repository's own copy holding no version and the project
// copy holding one -- and the build account's grant already naming the
// project copy -- the check signs with the PROJECT secret's value and
// reports the App fine, where the old hard-coded repo ID would have
// reported "its private key is not stored yet".
func TestDoctorGitHubAppCheckResolvesLive(t *testing.T) {
	r := projectSecretsRig(t)
	r.sm.AllowAccess()
	slug := mustSlug("github", "acme/app")
	repoID := gcp.SecretID(slug, "github-app-key")
	r.sm.Seed(repoID, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "github-app-key"}, nil)
	projectID := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte(appKeyPEM(t)))
	grantBuildAccount(t, r, projectID, "acme/app", true)
	gitHubAppServer(t, 200, fullPerms)

	checks := doctorAppChecks(t)
	c, ok := doctorCheckByID(checks, "github-app:acme/app")
	if !ok || !c.OK {
		t.Fatalf("github-app:acme/app = %+v (%v)", c, ok)
	}
	if !slices.Contains(r.sm.Accessed(), projectID) {
		t.Fatalf("doctor read %v, want the project-wide copy %s (the repository's own %s has no version)", r.sm.Accessed(), projectID, repoID)
	}
}

// B1 at doctor's call site: the live resolution and the build account's own
// grant disagree (a repository override stored since the last apply), so
// the check is a warning naming both IDs and the apply that fixes it,
// rather than silently signing with whichever one resolved live.
func TestDoctorGitHubAppCheckRefusesOnGrantMismatch(t *testing.T) {
	r := projectSecretsRig(t)
	r.sm.AllowAccess()
	slug := mustSlug("github", "acme/app")
	repoID := putSecretID(t, r.sm, slug, "github-app-key", []byte(appKeyPEM(t))) // the override: versioned
	projectID := seedProjectSecret(t, r.sm, "aurora", "github-app-key")
	grantBuildAccount(t, r, projectID, "acme/app", true) // the grant still names the project copy
	gitHubAppServer(t, 200, fullPerms)

	checks := doctorAppChecks(t)
	c, ok := doctorCheckByID(checks, "github-app:acme/app")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("github-app:acme/app = %+v (%v)", c, ok)
	}
	if !strings.Contains(c.Problem, repoID) || !strings.Contains(c.Problem, projectID) || !strings.Contains(c.Fix, "update --repo") {
		t.Fatalf("the warning names neither ID nor the fix: %+v", c)
	}
}

// putSecretID stores a version of name for repo's slug directly in the
// fake, through the production client (as fugaro secrets set does), and
// returns the secret ID.
func putSecretID(t *testing.T, sm *gcpfake.Secrets, slug, name string, value []byte) string {
	t.Helper()
	putSecret(t, sm, initProject, slug, name, value)
	return gcp.SecretID(slug, name)
}

// gitHubAppServer points githubAPIURL at a GitHub answering the App's
// installation lookup with status and perms, and returns the Authorization
// headers it saw.
func gitHubAppServer(t *testing.T, status int, perms map[string]string) *[]string {
	t.Helper()
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth = append(auth, req.Header.Get("Authorization"))
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "permissions": perms, "message": "Not Found"})
	}))
	t.Cleanup(srv.Close)
	old := githubAPIURL
	githubAPIURL = srv.URL
	t.Cleanup(func() { githubAPIURL = old })
	return &auth
}

// doctor says whether each recorded GitHub App is installed with what runs
// ask for: failing with the fix, a warning for what it must not hold, and
// never a secret in its output.
func TestDoctorGitHubApp(t *testing.T) {
	doctorChecks := func(t *testing.T) (out string, checks []doctorCheck) {
		t.Helper()
		return "", doctorAppChecks(t)
	}
	t.Run("fine", func(t *testing.T) {
		_, auth := doctorApp(t, 200, fullPerms)
		out, checks := doctorChecks(t)
		if c, ok := doctorCheckByID(checks, "github-app:acme/app"); !ok || !c.OK || len(*auth) != 1 {
			t.Fatalf("%+v %v asked %d\n%s", c, ok, len(*auth), out)
		}
	})
	t.Run("not installed", func(t *testing.T) {
		doctorApp(t, 404, nil)
		_, checks := doctorChecks(t)
		c, _ := doctorCheckByID(checks, "github-app:acme/app")
		if c.OK || c.Severity != "" || !strings.Contains(c.Problem, "the GitHub App 12345 is not installed on acme/app") || !strings.Contains(c.Fix, "install it on this repository only") {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("missing permission and workflows write", func(t *testing.T) {
		doctorApp(t, 200, map[string]string{"contents": "write", "pull_requests": "write", "metadata": "read", "workflows": "write"})
		out, checks := doctorChecks(t)
		c, _ := doctorCheckByID(checks, "github-app:acme/app")
		if c.OK || !strings.Contains(c.Problem, "Issues: Read") || !strings.Contains(c.Fix, "add Issues: Read, then accept the new permission on the installation") {
			t.Fatalf("%+v", c)
		}
		w, ok := doctorCheckByID(checks, "github-app-permissions-1:acme/app")
		if !ok || w.OK || w.Severity != "warning" || !strings.Contains(w.Problem, "Workflows: Write") {
			t.Fatalf("%+v %v", w, ok)
		}
		if strings.Contains(out, "BEGIN RSA PRIVATE KEY") || strings.Contains(out, "eyJhbGciOi") {
			t.Fatalf("doctor printed key material:\n%s", out)
		}
	})
	t.Run("GitHub unreachable", func(t *testing.T) {
		r, _ := doctorApp(t, 200, fullPerms)
		githubAPIURL = "http://127.0.0.1:1"
		_ = r
		_, checks := doctorChecks(t)
		c, _ := doctorCheckByID(checks, "github-app:acme/app")
		if c.OK || c.Severity != "warning" || !strings.Contains(c.Problem, "does not say the App is fine") {
			t.Fatalf("%+v", c)
		}
	})
}

// addClaudeAccounts appends a claude_accounts: block to the rig's local
// config, as an owner's own hand edit would (claude_accounts is
// local-config-only, never written by any Fugaro command itself; design
// docs/design/named-oauth-tokens.md §3 T1).
func addClaudeAccounts(t *testing.T, r *doctorRig, yaml string) {
	t.Helper()
	data, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfgPath, append(data, []byte(yaml)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// claudeAccountsRig is a doctor rig inside a checkout of github.com/acme/app,
// whose local config allows acme/app to choose the named Claude account
// acme-personal.
func claudeAccountsRig(t *testing.T) *doctorRig {
	t.Helper()
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addClaudeAccounts(t, r, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n")
	return r
}

// TestDoctorClaudeAccountsStoredMissing: a repository allowed to choose a
// named Claude account, whose secret was never stored, fails doctor and
// names the exact commands to run — never the secret's own value.
func TestDoctorClaudeAccountsStoredMissing(t *testing.T) {
	r := claudeAccountsRig(t)
	out, _, err := execute(t, "doctor", "--json")
	if err == nil {
		t.Fatal("want doctor to fail: the allowed named account has no stored token")
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored")
	if !ok || c.OK || c.Severity != "" {
		t.Fatalf("claude-accounts-stored = %+v", c)
	}
	if !strings.Contains(c.Problem, "acme-personal") {
		t.Fatalf("problem doesn't name the account: %q", c.Problem)
	}
	// The fix stores the token where D2 (plan Task 2.1) puts this name: the
	// project copy, which is the one the job mounts and the reach the
	// account's own allow_data_to already decided. `--repo acme/app` would
	// store a repository copy that then wins over the project copy forever
	// (§5, the footgun the project-secret checks name).
	if !strings.Contains(c.Fix, "claude setup-token") || !strings.Contains(c.Fix, "secrets set claude-oauth-token-acme-personal") {
		t.Fatalf("fix doesn't name the exact commands: %q", c.Fix)
	}
	if strings.Contains(c.Fix, "--repo") {
		t.Fatalf("the fix asks for a repository copy that would override the project's: %q", c.Fix)
	}
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("doctor read a secret value: %v", a)
	}
}

// TestDoctorClaudeAccountsStoredFromTheProject: a named account's token
// stored once for the project is stored -- the job mounts the project copy --
// so doctor must not call it missing, and must not hand the owner the
// repository-copy command that would override it (code review, medium: the
// check read only the repository's own containers).
func TestDoctorClaudeAccountsStoredFromTheProject(t *testing.T) {
	r := claudeAccountsRig(t)
	const value = "fake-token-0123456789"
	putProjectSecret(t, r.sm, "aurora", "claude-oauth-token-acme-personal", []byte(value))
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored")
	if !ok || !c.OK {
		t.Fatalf("claude-accounts-stored = %+v", c)
	}
	if strings.Contains(out, value) {
		t.Fatalf("the value leaked: %s", out)
	}
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("doctor read a secret value: %v", a)
	}
}

// TestDoctorClaudeAccountsStoredPresent: once the secret is stored, the
// check passes and no value ever reaches doctor's output.
func TestDoctorClaudeAccountsStoredPresent(t *testing.T) {
	r := claudeAccountsRig(t)
	const value = "fake-token-0123456789"
	putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), "claude-oauth-token-acme-personal", []byte(value))
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored")
	if !ok || !c.OK {
		t.Fatalf("claude-accounts-stored = %+v", c)
	}
	if strings.Contains(out, value) {
		t.Fatalf("the value leaked: %s", out)
	}
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("doctor read a secret value: %v", a)
	}
}

// TestDoctorClaudeAccountsStoredExcludesDisallowedRepo: a named account this
// repository is not allowed to choose never asks for its token to be
// stored, mirroring config.AllowedClaudeAccounts's own exclusion.
func TestDoctorClaudeAccountsStoredExcludesDisallowedRepo(t *testing.T) {
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addClaudeAccounts(t, r, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/other]\n")
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored"); ok {
		t.Fatalf("a repository got a check for an account it isn't allowed: %+v", c)
	}
}

// TestDoctorClaudeAccountsStoredSilentWithNoAccounts: an installation that
// configures no claude_accounts at all gets no new check — byte-identical
// to doctor's output before this feature (design §5; carry-forward T8).
func TestDoctorClaudeAccountsStoredSilentWithNoAccounts(t *testing.T) {
	newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	// A real owner/name origin, so this actually exercises "no accounts
	// configured" inside claudeAccountsStoredCheck, not just "no origin to
	// resolve a repository from" (a different, weaker case).
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored"); ok {
		t.Fatalf("an installation with no claude_accounts got a new check: %+v", c)
	}
}

// TestDoctorClaudeAccountsStoredFailsClosedWhenUnreachable: when the
// repository and its allowed named account are both known, but the secrets
// list itself could not be read, the check must warn, not go silent (fail
// closed: carry-forward T8).
func TestDoctorClaudeAccountsStoredFailsClosedWhenUnreachable(t *testing.T) {
	r := claudeAccountsRig(t)
	data, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), r.sm.URL, "http://127.0.0.1:1", 1)
	if broken == string(data) {
		t.Fatal("secret_manager endpoint not found in the local config to break")
	}
	if err := os.WriteFile(r.cfgPath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("claude-accounts-stored = %+v", c)
	}
	if _, _, err := execute(t, "doctor", "--strict", "--json"); err == nil {
		t.Fatal("--strict: want a failure, since the check could not be answered")
	}
}

// TestDoctorClaudeAccountsStoredWarnsWhenRepoUnknown: claude_accounts is
// configured (for some repository), but this checkout's origin remote
// cannot be parsed as owner/name, so it is impossible to tell whether any
// named account applies here. Review finding: this used to fail open
// (silence); fixed to warn instead (fail closed, never silence).
func TestDoctorClaudeAccountsStoredWarnsWhenRepoUnknown(t *testing.T) {
	r := newDoctorRig(t)
	// No testutil.Git ... remote set-url: checkoutWith's default origin is a
	// local bare-repo path, which repoFromOrigin cannot parse as owner/name.
	checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	addClaudeAccounts(t, r, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n")
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("claude-accounts-stored = %+v", c)
	}
	if !strings.Contains(c.Problem, "no origin remote") {
		t.Fatalf("problem doesn't explain why: %q", c.Problem)
	}
	if _, _, err := execute(t, "doctor", "--strict", "--json"); err == nil {
		t.Fatal("--strict: want a failure, since the check could not be answered")
	}
}

// TestDoctorClaudeAccountsStoredSilentWhenRepoUnknownAndNoAccounts: with no
// claude_accounts configured at all, an unparseable origin still produces no
// new check — byte-identical output (design §5; carry-forward T8).
func TestDoctorClaudeAccountsStoredSilentWhenRepoUnknownAndNoAccounts(t *testing.T) {
	newDoctorRig(t)
	checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored"); ok {
		t.Fatalf("an installation with no claude_accounts got a new check: %+v", c)
	}
}

// TestDoctorNamedAccountCheckReadsNoValue is the plan's own named test
// (Task 8): doctor's named-account check reads secrets' metadata only
// (List, by label) and never a value (Access), proven here with a fake that
// fatals the test on any Access call (gcpfake.Secrets' default: Access is
// refused unless a test explicitly calls AllowAccess, which this test never
// does). Exercises every branch that touches o.Secrets: present, missing,
// and the repository-unknown path that makes no Secret Manager call at all.
func TestDoctorNamedAccountCheckReadsNoValue(t *testing.T) {
	r := claudeAccountsRig(t)
	putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), "claude-oauth-token-acme-personal", []byte("fake-token-0123456789"))
	if _, _, err := execute(t, "doctor", "--json"); err != nil {
		t.Fatal(err)
	}
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("doctor read a secret value: %v", a)
	}

	r2 := newDoctorRig(t)
	checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	addClaudeAccounts(t, r2, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n")
	if _, _, err := execute(t, "doctor", "--json"); err != nil {
		t.Fatal(err)
	}
	if a := r2.sm.Accessed(); len(a) != 0 {
		t.Fatalf("doctor read a secret value with no resolvable repository: %v", a)
	}
}

// TestDoctorClaudeAccountsStoredCheckEscapesHostileInput is a direct, function-level
// test of claudeAccountsStoredCheck (bypassing config.ValidateClaudeAccounts,
// which already refuses hostile account names and non-slug allow_data_to
// entries at parse time — this test is defense in depth, proving the
// function's own output is safe even if fed input no real parse path can
// produce today). A control character, a bidi override and an over-long
// name must all come out escaped and length-bounded in the human-readable
// Problem text. The Fix text is deliberately exempted: it carries the exact,
// literal secret ID and repo an operator must paste into a real command
// (the same convention githubapp.go's Fix lines already use), so escaping it
// would print a command that cannot actually be run.
func TestDoctorClaudeAccountsStoredCheckEscapesHostileInput(t *testing.T) {
	const bell = "\x07"
	bidi := "‮"
	long := strings.Repeat("x", 500)
	hostileRepo := "acme/app" + bell + bidi
	hostileAccount := "acme-personal-" + long
	lc := &localcfg.Config{ClaudeAccounts: map[string]config.ClaudeAccount{
		hostileAccount: {AllowDataTo: []string{hostileRepo}},
	}}
	c := claudeAccountsStoredCheck(lc, hostileRepo, []secretEntry{}, nil, true)
	if c == nil || c.OK {
		t.Fatalf("claudeAccountsStoredCheck = %+v", c)
	}
	for _, raw := range []string{bell, bidi} {
		if strings.Contains(c.Problem, raw) {
			t.Fatalf("a control/bidi character leaked raw into Problem: %q", c.Problem)
		}
	}
	if strings.Contains(c.Problem, long) {
		t.Fatalf("an over-long name was not bounded in Problem: %q", c.Problem)
	}
	if n := len([]rune(c.Problem)); n > 2*pluginwire.MaxPrintable+200 {
		t.Fatalf("Problem is not length-bounded: %d runes", n)
	}

	// The "could not list" warning branch (listed == nil) prints the repo and
	// the names too: it must escape and bound them just the same (replacing
	// printableRepo/printableNamed with the raw values there stayed green).
	w := claudeAccountsStoredCheck(lc, hostileRepo, nil, nil, false)
	if w == nil || w.OK {
		t.Fatalf("claudeAccountsStoredCheck(listed=nil) = %+v, want a warning", w)
	}
	for _, raw := range []string{bell, bidi} {
		if strings.Contains(w.Problem, raw) {
			t.Fatalf("a control/bidi character leaked raw into the could-not-list warning: %q", w.Problem)
		}
	}
	if strings.Contains(w.Problem, long) {
		t.Fatalf("an over-long name was not bounded in the could-not-list warning: %q", w.Problem)
	}
	if n := len([]rune(w.Problem)); n > 2*pluginwire.MaxPrintable+200 {
		t.Fatalf("the could-not-list warning is not length-bounded: %d runes", n)
	}
}

// oauthTokenYAML is cliMinimalYAML with agent.auth: oauth and
// agent.oauth_token set, for oauth-token-allowed's allow-list tests: auth:
// oauth so they exercise the allow-list check, not the (review finding D)
// auth-mismatch refusal TestDoctorFlagsOAuthTokenWhenAuthIsNotOAuth covers
// on its own, with a plain (non-oauth) auth.
const oauthTokenYAML = `version: 1
project: aurora
git: { provider: github }
agent: { auth: oauth, oauth_token: acme-overflow }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`

// TestDoctorFlagsOAuthTokenNotAllowed is the plan's own named test (Task 8):
// fugaro.yaml names agent.oauth_token: acme-overflow, but the local config's
// allow_data_to for acme-overflow does not list this repository: the
// offline check fails, naming both the token and the repository, with no
// GCP call needed to answer it (TestDoctorOAuthTokenAllowedMakesNoNetworkCall
// below proves that independently).
func TestDoctorFlagsOAuthTokenNotAllowed(t *testing.T) {
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": oauthTokenYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addClaudeAccounts(t, r, "claude_accounts:\n  acme-overflow:\n    allow_data_to: [acme/other]\n")
	out, _, err := execute(t, "doctor", "--json")
	if err == nil {
		t.Fatal("want doctor to fail: agent.oauth_token names an account acme/app isn't allowed")
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "oauth-token-allowed")
	if !ok || c.OK || c.Severity != "" {
		t.Fatalf("oauth-token-allowed = %+v", c)
	}
	if !strings.Contains(c.Problem, "acme-overflow") || !strings.Contains(c.Problem, "acme/app") {
		t.Fatalf("problem doesn't name both: %q", c.Problem)
	}
}

// TestDoctorFlagsOAuthTokenWhenAuthIsNotOAuth is review finding D (round 2):
// doctor used to report oauth-token-allowed OK for an agent.oauth_token the
// local config DOES allow, even when the workflow's agent.auth isn't oauth
// — disagreeing with fugaro run, which already refuses that combination
// outright (claudeAccountAllowed, internal/cli/run.go). The account is
// allowed here (allow_data_to lists acme/app) precisely to isolate the
// auth-mismatch rule: it must still fail, not fall through to the
// allow-list check that would otherwise pass.
func TestDoctorFlagsOAuthTokenWhenAuthIsNotOAuth(t *testing.T) {
	r := newDoctorRig(t)
	yaml := strings.Replace(oauthTokenYAML, "auth: oauth", "auth: api-key", 1)
	if yaml == oauthTokenYAML {
		t.Fatal("oauthTokenYAML's auth: oauth line not found to replace")
	}
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": yaml})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addClaudeAccounts(t, r, "claude_accounts:\n  acme-overflow:\n    allow_data_to: [acme/app]\n")
	out, _, err := execute(t, "doctor", "--json")
	if err == nil {
		t.Fatal("want doctor to fail: agent.oauth_token is set but agent.auth is not oauth")
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "oauth-token-allowed")
	if !ok || c.OK || c.Severity != "" {
		t.Fatalf("oauth-token-allowed = %+v", c)
	}
	if !strings.Contains(c.Problem, "acme-overflow") || !strings.Contains(c.Problem, "agent.auth is oauth") || !strings.Contains(c.Problem, "api-key") {
		t.Fatalf("problem doesn't explain the auth mismatch: %q", c.Problem)
	}
}

// TestDoctorOAuthTokenAllowedPresent: once the local config allows this
// repository to choose the named account fugaro.yaml's agent.oauth_token
// names, the check passes.
func TestDoctorOAuthTokenAllowedPresent(t *testing.T) {
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": oauthTokenYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addClaudeAccounts(t, r, "claude_accounts:\n  acme-overflow:\n    allow_data_to: [acme/app]\n")
	// Stored too, so the unrelated claude-accounts-stored check (same named
	// account, same repository) doesn't also fail doctor here: this test is
	// about oauth-token-allowed only.
	putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), "claude-oauth-token-acme-overflow", []byte("fake-token-0123456789"))
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "oauth-token-allowed")
	if !ok || !c.OK {
		t.Fatalf("oauth-token-allowed = %+v", c)
	}
}

// TestDoctorOAuthTokenAllowedSilentWhenUnset: a checkout with no
// agent.oauth_token at all gets no new check — byte-identical to doctor's
// output before this feature (design §5; same discipline as
// claude-accounts-stored's own "silent with no accounts" test).
func TestDoctorOAuthTokenAllowedSilentWhenUnset(t *testing.T) {
	newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "oauth-token-allowed"); ok {
		t.Fatalf("a checkout with no agent.oauth_token got a new check: %+v", c)
	}
}

// TestDoctorOAuthTokenAllowedWarnsWhenRepoUnknown: agent.oauth_token is set,
// but this checkout's origin remote cannot be parsed as owner/name, so it is
// impossible to tell whether the named account applies here. Fail closed
// (warn), never silence — the same rule claude-accounts-stored already
// follows for the same situation.
func TestDoctorOAuthTokenAllowedWarnsWhenRepoUnknown(t *testing.T) {
	newDoctorRig(t)
	// No testutil.Git ... remote set-url: checkoutWith's default origin is a
	// local bare-repo path, which repoFromOrigin cannot parse as owner/name.
	checkoutWith(t, map[string]string{"fugaro.yaml": oauthTokenYAML})
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "oauth-token-allowed")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("oauth-token-allowed = %+v", c)
	}
	if !strings.Contains(c.Problem, "no origin remote") {
		t.Fatalf("problem doesn't explain why: %q", c.Problem)
	}
	if _, _, err := execute(t, "doctor", "--strict", "--json"); err == nil {
		t.Fatal("--strict: want a failure, since the check could not be answered")
	}
}

// TestDoctorOAuthTokenAllowedMakesNoNetworkCall: unlike claude-accounts-stored
// (which needs a Secret Manager listing and degrades to a warning when it's
// unreachable), oauth-token-allowed is computed purely from the already-loaded
// local config and the already-resolved fugaro.yaml. With Secret Manager
// broken, claude-accounts-stored must warn (it can't be answered), while
// oauth-token-allowed must still give its real answer (OK here) — proving it
// needed no network call to do so.
func TestDoctorOAuthTokenAllowedMakesNoNetworkCall(t *testing.T) {
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": oauthTokenYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addClaudeAccounts(t, r, "claude_accounts:\n  acme-overflow:\n    allow_data_to: [acme/app]\n")
	data, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), r.sm.URL, "http://127.0.0.1:1", 1)
	if broken == string(data) {
		t.Fatal("secret_manager endpoint not found in the local config to break")
	}
	if err := os.WriteFile(r.cfgPath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning (claude-accounts-stored) must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "claude-accounts-stored"); !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("claude-accounts-stored should warn with Secret Manager unreachable: %+v", c)
	}
	c, ok := doctorCheckByID(o.Checks, "oauth-token-allowed")
	if !ok || !c.OK {
		t.Fatalf("oauth-token-allowed should still answer OK with no network call needed: %+v", c)
	}
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("doctor read a secret value: %v", a)
	}
}

// TestDoctorOAuthTokenAllowedCheckEscapesHostileInput is a direct,
// function-level test of oauthTokenAllowedCheck (bypassing config.Validate,
// which already refuses a hostile agent.oauth_token at parse time — this is
// defense in depth, the same posture
// TestDoctorClaudeAccountsStoredCheckEscapesHostileInput takes for its own
// sibling check). A control character, a bidi override and an over-long
// name must all come out escaped and length-bounded.
func TestDoctorOAuthTokenAllowedCheckEscapesHostileInput(t *testing.T) {
	const bell = "\x07"
	bidi := "‮"
	long := strings.Repeat("x", 500)
	hostileRepo := "acme/app" + bell + bidi
	hostileToken := "acme-overflow-" + long
	lc := &localcfg.Config{ClaudeAccounts: map[string]config.ClaudeAccount{
		"acme-personal": {AllowDataTo: []string{hostileRepo}},
	}}
	c := oauthTokenAllowedCheck(lc, hostileRepo, hostileToken, "oauth")
	if c == nil || c.OK {
		t.Fatalf("oauthTokenAllowedCheck = %+v", c)
	}
	for _, raw := range []string{bell, bidi} {
		if strings.Contains(c.Problem, raw) || strings.Contains(c.Fix, raw) {
			t.Fatalf("a control/bidi character leaked raw: Problem %q, Fix %q", c.Problem, c.Fix)
		}
	}
	if strings.Contains(c.Problem, long) || strings.Contains(c.Fix, long) {
		t.Fatalf("an over-long name was not bounded: Problem %q, Fix %q", c.Problem, c.Fix)
	}
	if n := len([]rune(c.Problem)); n > 4*pluginwire.MaxPrintable+200 {
		t.Fatalf("Problem is not length-bounded: %d runes", n)
	}
}

// doctor without --check-github-app (what the setup skill runs, in an agent's
// session) never reads the App's key and never asks GitHub: the fake refuses
// any access call.
func TestDoctorWithoutFlagNeverReadsTheKey(t *testing.T) {
	r, auth := doctorApp(t, 200, fullPerms)
	// doctorApp allows access for the flagged tests; here the fake must see none.
	t.Setenv("CLAUDECODE", "1")
	out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "github-app") {
			t.Errorf("a GitHub App check without the flag: %+v", c)
		}
	}
	if a := r.sm.Accessed(); len(a) != 0 || len(*auth) != 0 {
		t.Errorf("accessed %v, GitHub asked %d times", a, len(*auth))
	}
}

// TestDoctorPriceCatalogStaleIsWarning: the catalog check measures the
// catalog's own 30 days, not the Claude table's 90, and a stale catalog is a
// warning (it fails doctor only under --strict) whose fix is the command
// that regenerates the file (design §7, P7, Task 5 step 3).
func TestDoctorPriceCatalogStaleIsWarning(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	checked, err := time.Parse("2006-01-02", cat.CheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	fresh := doctorPriceCatalog(checked.Add(29 * 24 * time.Hour))
	if fresh.ID != "provider-price-catalog" || fresh.Severity == "warning" || !strings.Contains(fresh.Problem, cat.CheckedAt) {
		t.Errorf("29 days: %+v", fresh)
	}
	if !strings.Contains(fresh.Problem, cat.Sources[0]) {
		t.Errorf("the fresh check does not name the catalog's source: %+v", fresh)
	}
	if checksFail([]doctorCheck{fresh}, true) {
		t.Error("a fresh catalog failed doctor")
	}
	// The threshold is 30 days, not the Anthropic table's 90: 89 days is
	// already stale here.
	stale := doctorPriceCatalog(checked.Add(31 * 24 * time.Hour))
	if stale.Severity != "warning" || !strings.Contains(stale.Problem, "stale") {
		t.Errorf("31 days: %+v", stale)
	}
	if !strings.Contains(stale.Fix, "scripts/gen-price-catalog.sh") {
		t.Errorf("the stale check does not name the regeneration command: %+v", stale)
	}
	if checksFail([]doctorCheck{stale}, false) || !checksFail([]doctorCheck{stale}, true) {
		t.Error("a stale catalog is not a --strict-only warning")
	}
	if long := doctorPriceCatalog(checked.Add(89 * 24 * time.Hour)); long.Severity != "warning" {
		t.Errorf("89 days is not stale: %+v", long)
	}
}

// TestDoctorZDRCheckSaysUnverified: a route whose require_zdr is on (the
// default) is priced at the ZDR-eligible endpoints, and doctor says plainly
// that this is a price and not an enforcement Fugaro can make or read
// (design §8, P9, Task 5 step 3). Only the routes the built-in catalog prices
// are named: require_zdr on a vendor's own endpoint selects no price, so the
// sentence would be false there.
func TestDoctorZDRCheckSaysUnverified(t *testing.T) {
	if c := doctorProviderZDR(&localcfg.Config{}); c != nil {
		t.Errorf("no route, but %+v", c)
	}
	off := false
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"quiet": {BaseURL: "https://openrouter.ai/api", RequireZDR: &off},
		"zdr":   {BaseURL: "https://openrouter.ai/api"},
		// A vendor's own endpoint: require_zdr selects no price there, so
		// the note must not claim its models are priced from the catalog.
		"vendor": {BaseURL: "https://api.deepseek.com/anthropic"},
	}}
	c := doctorProviderZDR(lc)
	if c == nil {
		t.Fatal("a require_zdr route is not reported")
	}
	if c.Severity != "info" || c.OK {
		t.Errorf("the check is not informational: %+v", c)
	}
	for _, want := range []string{"zdr", "price", "privacy setting", "cannot read", "zdr"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the check lacks %q: %s", want, c.Problem)
		}
	}
	if strings.Contains(c.Problem, "quiet") || strings.Contains(c.Problem, "vendor") || !strings.Contains(c.Problem, "zdr") {
		t.Errorf("the check names the wrong routes: %s", c.Problem)
	}
	// A require_zdr route the catalog does not price is not this check's
	// business at all: with no catalog-eligible route there is nothing to
	// say, rather than a sentence that would be false.
	if c := doctorProviderZDR(&localcfg.Config{Providers: map[string]config.ModelProvider{
		"vendor": {BaseURL: "https://api.deepseek.com/anthropic"},
	}}); c != nil {
		t.Errorf("a route the catalog does not price is reported as catalog-priced: %+v", c)
	}
	if checksFail([]doctorCheck{*c}, true) {
		t.Error("the informational ZDR check failed doctor under --strict")
	}
}

// addLocalLines appends configuration to the rig's local config file, as an
// owner's own hand edit would (project_secrets and repos are local-config-only
// and never written by a Fugaro command other than update --repo).
func addLocalLines(t *testing.T, r *doctorRig, yaml string) {
	t.Helper()
	data, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfgPath, append(data, []byte(yaml)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// repoLabel is the fugaro_repo label of a repository slug, for seeding the
// fake Secret Manager with a container that looks like update --repo's own.
func repoLabel(t *testing.T, slug string) string {
	t.Helper()
	l, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// projectLabels are the labels of a project-wide container (design
// docs/design/project-secrets.md §4).
func projectLabels(fugaroProject, name string) map[string]string {
	return map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: fugaroProject, gcp.LabelSecret: name}
}

// seedProjectSecret creates the project-wide container of name with no
// version at all: update --repo created it (or a hand edit did), and nothing
// has ever been stored.
func seedProjectSecret(t *testing.T, sm *gcpfake.Secrets, fugaroProject, name string) string {
	t.Helper()
	id := gcp.ProjectSecretID(fugaroProject, name)
	sm.Seed(id, projectLabels(fugaroProject, name), nil)
	return id
}

// putProjectSecret stores value as a new version of a project-wide
// container, through the production Secrets client, exactly as
// `fugaro secrets set NAME --project-wide` would.
func putProjectSecret(t *testing.T, sm *gcpfake.Secrets, fugaroProject, name string, value []byte) string {
	t.Helper()
	ctx := context.Background()
	s, err := gcp.NewSecrets(ctx, gcp.Options{GCPProject: "proj-1234", Endpoints: gcp.Endpoints{SecretManager: sm.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.ProjectSecretID(fugaroProject, name)
	if _, err := s.Set(ctx, id, value, projectLabels(fugaroProject, name)); err != nil {
		t.Fatal(err)
	}
	return id
}

// projectSecretsConfig is the local config the project-secret rigs below
// add: github-app-key opted into project-wide storage for acme/app, and
// acme/app's own repos entry (infra.Repo requires a GitHub repository's App
// ID, which is not a secret).
const projectSecretsConfig = "project_secrets:\n  github-app-key:\n    allow_data_to: [acme/app]\n" +
	"repos:\n  acme/app: { provider: github, github_app_id: \"12345\", workflows: [app] }\n"

// projectSecretsRig is a doctor rig inside a checkout of github.com/acme/app,
// whose local config opts github-app-key into project-wide storage for
// acme/app.
func projectSecretsRig(t *testing.T) *doctorRig {
	t.Helper()
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addLocalLines(t, r, projectSecretsConfig)
	return r
}

// TestDoctorFlagsUnstoredProjectSecret: the one secret every run mounts is
// allow-listed for this repository project-wide, and neither copy holds an
// enabled version, so no run can start. Red, and the fix names both ways out.
func TestDoctorFlagsUnstoredProjectSecret(t *testing.T) {
	r := projectSecretsRig(t)
	slug := mustSlug("github", "acme/app")
	// Both containers exist (update --repo created them) and neither has an
	// enabled version: a container with no version can never be mounted.
	r.sm.Seed(gcp.SecretID(slug, "github-app-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "github-app-key"}, nil)
	seedProjectSecret(t, r.sm, "aurora", "github-app-key")

	out, _, err := execute(t, "doctor", "--json")
	if err == nil {
		t.Fatalf("want doctor to fail: the secret every run mounts has no version anywhere\n%s", out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-secret-stored")
	if !ok || c.OK || c.Severity != "" {
		t.Fatalf("project-secret-stored = %+v", c)
	}
	if !strings.Contains(c.Problem, "github-app-key") {
		t.Fatalf("problem doesn't name the secret: %q", c.Problem)
	}
	if !strings.Contains(c.Fix, "secrets set github-app-key") {
		t.Fatalf("fix doesn't name the command: %q", c.Fix)
	}
	if !strings.Contains(c.Fix, "secrets set github-app-key --repo acme/app") {
		t.Fatalf("fix doesn't name the repository command: %q", c.Fix)
	}
	// The bare command leads (code review, low): it is D2's default store, so
	// it is the command that puts the value where the repositories the owner
	// allow-listed it for mount it. --repo first offered a repository copy --
	// the one that wins over the project copy forever -- to a person whose
	// repository may already mount the shared one. The sibling
	// claude-accounts check leads the same way.
	if first, _, _ := strings.Cut(c.Fix, ", or "); !strings.HasSuffix(first, "secrets set github-app-key") {
		t.Fatalf("fix does not lead with the bare command: %q", c.Fix)
	}
}

// TestDoctorProjectSecretStoredPassesWithEitherCopy: a version on either
// side is enough: the repository's own copy wins, and the project copy is
// what a repository with no copy of its own mounts.
func TestDoctorProjectSecretStoredPassesWithEitherCopy(t *testing.T) {
	r := projectSecretsRig(t)
	slug := mustSlug("github", "acme/app")
	// Only the project copy: the repository has no container of its own.
	putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "project-secret-stored"); ok && !c.OK {
		t.Fatalf("project-secret-stored = %+v, want ok: the project copy has a version", c)
	}
	if out := string(r.sm.Latest(gcp.SecretID(slug, "github-app-key"))); out != "" {
		t.Fatalf("a repository copy exists: %q", out)
	}
}

// apiKeyRig is a doctor rig inside a checkout of github.com/acme/app whose
// config takes the api-key auth with lines (the agent and budget blocks to
// splice in) and whose local config offers one provider serving deepseek/*.
func apiKeyRig(t *testing.T, lines string) *doctorRig {
	t.Helper()
	r := newDoctorRig(t)
	yaml := strings.Replace(cliMinimalYAML, "workflows:", lines+"workflows:", 1)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": yaml})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addLocalLines(t, r, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n"+
		"    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n"+
		"repos:\n  acme/app: { provider: github, github_app_id: \"12345\", workflows: [app] }\n")
	// The two names this rig's runs really need, so the key under test is the
	// only thing left: the git credential, and the provider's own key.
	putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("pem"))
	putProjectSecret(t, r.sm, "aurora", "openrouter-api-key", []byte("provider-value"))
	return r
}

// TestDoctorDoesNotRequireTheOptionalAnthropicKey: D2's sweep makes
// anthropic-api-key project-wide-eligible for every repository (a reserved
// name with no per-name list), and the project-secret-stored check then
// demanded a stored value for it. The key is one a job can go without
// (infra.OptionalSecret): a repository whose models all route through a
// provider never needs one, and Readiness drops the mount and deploys rather
// than gate it -- so doctor failed outright, "no run of a workflow mounting
// one can start", for a repository whose runs are fine (code review, high).
func TestDoctorDoesNotRequireTheOptionalAnthropicKey(t *testing.T) {
	apiKeyRig(t, "agent: { auth: api-key, models: { coder: deepseek/coder, reviewer: deepseek/coder } }\n"+
		"budget: { mode: enforce, per_run_usd: 20 }\n")
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor failed a repository that needs no Anthropic key: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	for _, id := range []string{"project-secret-stored", "project-secret-optional"} {
		if c, ok := doctorCheckByID(o.Checks, id); ok && !c.OK {
			t.Fatalf("%s = %+v, want no check or an ok one: the job drops the key's mount and deploys without it", id, c)
		}
	}
}

// TestDoctorWarnsForTheOptionalAnthropicKeyWhenItIsNeeded: the key is needed
// when a pin no provider serves goes direct to Anthropic, and when the budget
// isn't enforcing every call goes there whatever the pins are
// (config.AnthropicKeyNeeded) -- so a keyless repository's runs are refused
// at agent start. Readiness says so as a note and never gates the deploy, so
// doctor warns and does not fail.
func TestDoctorWarnsForTheOptionalAnthropicKeyWhenItIsNeeded(t *testing.T) {
	for _, tc := range []struct{ name, lines string }{
		{"a pin no provider serves", "agent: { auth: api-key, models: { coder: claude-sonnet-5-5, reviewer: claude-sonnet-5-5 } }\n" +
			"budget: { mode: enforce, per_run_usd: 20 }\n"},
		{"nothing routes to a provider", "agent: { auth: api-key, models: { coder: claude-sonnet-5-5, reviewer: claude-sonnet-5-5 } }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doctorWarnsForTheOptionalAnthropicKey(t, tc.lines)
		})
	}
}

func doctorWarnsForTheOptionalAnthropicKey(t *testing.T, lines string) {
	t.Helper()
	apiKeyRig(t, lines)
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor failed on a warning: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-secret-optional")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("project-secret-optional = %+v", c)
	}
	if !strings.Contains(c.Problem, "anthropic-api-key") {
		t.Fatalf("problem doesn't name the key: %q", c.Problem)
	}
	if !strings.Contains(c.Fix, "secrets set anthropic-api-key") {
		t.Fatalf("fix doesn't name the command: %q", c.Fix)
	}
	if stored, ok := doctorCheckByID(o.Checks, "project-secret-stored"); ok && !stored.OK {
		t.Fatalf("project-secret-stored = %+v: the optional key is not the red gate", stored)
	}
}

// TestDoctorFlagsStaleScope: the deployed job still mounts the repository's
// own copy, and the copy Readiness resolves now is the project's (the
// repository's lost its last version since the last apply). A warning: the
// next apply fixes it, and doctor without --strict must not fail on it.
func TestDoctorFlagsStaleScope(t *testing.T) {
	// The fix is printed to be pasted, so it names the binary as the user
	// ran it: the literal text below is what a person sees.
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	r := projectSecretsRig(t)
	slug := mustSlug("github", "acme/app")
	repoID := gcp.SecretID(slug, "github-app-key")
	r.sm.Seed(repoID, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "github-app-key"}, nil)
	projID := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))

	// The deployed job's own mounted reference, as the module writes it.
	job := gcp.JobName(slug, "app")
	r.run.SetJobJSON(job, `{"name":"projects/proj-1234/locations/us-east5/jobs/`+job+`",
	  "template":{"template":{"containers":[{"image":"fake/app","env":[
	    {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueSource":{"secretKeyRef":{"secret":"`+repoID+`","version":"latest"}}}]}]}}}`)

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-secret-scope-stale")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("project-secret-scope-stale = %+v", c)
	}
	if !strings.Contains(c.Problem, "github-app-key") || !strings.Contains(c.Problem, projID) || !strings.Contains(c.Problem, repoID) {
		t.Fatalf("problem doesn't name the secret and both copies: %q", c.Problem)
	}
	if !strings.Contains(c.Fix, "fugaro update --repo") {
		t.Fatalf("fix doesn't name the command that re-applies it: %q", c.Fix)
	}
}

// TestDoctorNoStaleScopeWhenTheJobAlreadyMountsTheProjectCopy: the same
// repository, already re-applied. Nothing to say.
func TestDoctorNoStaleScopeWhenTheJobAlreadyMountsTheProjectCopy(t *testing.T) {
	r := projectSecretsRig(t)
	slug := mustSlug("github", "acme/app")
	repoID := gcp.SecretID(slug, "github-app-key")
	r.sm.Seed(repoID, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "github-app-key"}, nil)
	projID := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))

	job := gcp.JobName(slug, "app")
	r.run.SetJobJSON(job, `{"name":"projects/proj-1234/locations/us-east5/jobs/`+job+`",
	  "template":{"template":{"containers":[{"image":"fake/app","env":[
	    {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueSource":{"secretKeyRef":{"secret":"`+projID+`","version":"latest"}}}]}]}}}`)

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "project-secret-scope-stale"); ok && !c.OK {
		t.Fatalf("project-secret-scope-stale = %+v, want nothing: the job already mounts the resolved copy", c)
	}
}

// TestDoctorFlagsStaleScopeWhenTheRepoCopyComesBack: the same repository
// moving the other way. The deployed job was applied while the project-wide
// copy was the one Readiness resolved, and a repository copy has since been
// stored again (`fugaro secrets set github-app-key --repo acme/app`). The
// job still mounts the project's ID, while the copy Fugaro resolves for it
// now is the repository's own -- so the warning has to fire in this
// direction too, or a job keeps reading the project value after the
// operator moved the name back, with nothing said.
func TestDoctorFlagsStaleScopeWhenTheRepoCopyComesBack(t *testing.T) {
	r := projectSecretsRig(t)
	slug := mustSlug("github", "acme/app")
	repoID := gcp.SecretID(slug, "github-app-key")
	r.sm.Seed(repoID, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "github-app-key"}, []byte("repo-value"))
	projID := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))

	// The deployed job's own mounted reference: the project-wide copy, as
	// the apply that ran before this repository had one of its own wrote it.
	job := gcp.JobName(slug, "app")
	r.run.SetJobJSON(job, `{"name":"projects/proj-1234/locations/us-east5/jobs/`+job+`",
	  "template":{"template":{"containers":[{"image":"fake/app","env":[
	    {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueSource":{"secretKeyRef":{"secret":"`+projID+`","version":"latest"}}}]}]}}}`)

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-secret-scope-stale")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("project-secret-scope-stale = %+v, want a warning: the job mounts the project copy and the repository's own copy wins now", c)
	}
	if !strings.Contains(c.Problem, "github-app-key") || !strings.Contains(c.Problem, projID) || !strings.Contains(c.Problem, repoID) {
		t.Fatalf("problem doesn't name the secret and both copies: %q", c.Problem)
	}
}

// secretScopeRig is a doctor rig whose local config opts github-app-key into
// project-wide storage for acme/app (projectSecretsRig) and whose checkout
// carries a workflow secret of its own, so the scope listing has both a
// project-eligible name and a repository-only one to show.
func secretScopeRig(t *testing.T) *doctorRig {
	t.Helper()
	r, _ := secretScopeCheckout(t)
	addLocalLines(t, r, projectSecretsConfig)
	return r
}

// secretScopeSweepRig is the same checkout with the other half of the same
// rule: a local config with no project_secrets entry at all. github-app-key is
// still project-scope eligible there — config.ProjectScopeByDefault sweeps in
// the reserved names the platform itself mounts, "the same reach the CLI's
// default store announces" (infra.ProjectSecretCandidate) — so both doctor
// checks have to answer for that case too, not only for an owner's entry.
func secretScopeSweepRig(t *testing.T) *doctorRig {
	t.Helper()
	r, _ := secretScopeCheckout(t)
	addLocalLines(t, r, "repos:\n  acme/app: { provider: github, github_app_id: \"12345\", workflows: [app] }\n")
	return r
}

// secretScopeCheckout is the checkout both rigs above share: github.com/acme/app,
// one workflow mounting a secret of its own, and the local config's repos block
// left to the caller. The checkout's own directory is returned beside the rig,
// for a caller that has to write into it.
func secretScopeCheckout(t *testing.T) (*doctorRig, string) {
	t.Helper()
	r := newDoctorRig(t)
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh }, secrets: [{ name: db-password, env: DB_PASSWORD }] }\n"
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": yaml})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	return r, dir
}

// TestDoctorSecretScopeCheck: doctor lists each name this checkout's jobs
// mount with the copy Fugaro resolves for it — the project's, or the
// repository's own (plan Task 2.3 step 1) — as an info line, which never
// fails doctor and names no value.
func TestDoctorSecretScopeCheck(t *testing.T) {
	r := secretScopeRig(t)
	// The project's copy of the App key, and this repository's own copy of
	// the workflow secret: one name each way, with two values that must never
	// appear in doctor's output.
	putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	slug := mustSlug("github", "acme/app")
	r.sm.Seed(gcp.SecretID(slug, "db-password"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "db-password"}, []byte("repo-value"))

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "secret-scope")
	if !ok || c.OK || c.Severity != "info" {
		t.Fatalf("secret-scope = %+v", c)
	}
	if !strings.Contains(c.Problem, "github-app-key") || !strings.Contains(c.Problem, "project scope") {
		t.Fatalf("secret-scope does not name the project-scope name: %q", c.Problem)
	}
	if !strings.Contains(c.Problem, "db-password") || !strings.Contains(c.Problem, "repository copy") {
		t.Fatalf("secret-scope does not name the repository copy: %q", c.Problem)
	}
	if checksFail([]doctorCheck{c}, true) {
		t.Fatalf("the informational scope listing failed doctor under --strict: %+v", c)
	}
	for _, leak := range []string{"project-value", "repo-value"} {
		if strings.Contains(out, leak) {
			t.Fatalf("doctor printed a secret value %q:\n%s", leak, out)
		}
	}
	// The D2 sweep (plan Task 2.1 step 2): the same installation with no
	// project_secrets entry at all. github-app-key is project-scope eligible
	// through config.ProjectScopeByDefault, so the line has to say "project
	// scope" for it here too — and say "no enabled copy this repository can
	// resolve" for the workflow secret nothing has a version of, which is
	// the answer the docs promise
	// for every name (both copies really are read: the payloads are here and
	// the assertions below keep them out of the output).
	sweep := secretScopeSweepRig(t)
	putProjectSecret(t, sweep.sm, "aurora", "github-app-key", []byte("project-value"))
	sout, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor failed on the sweep rig: %v\n%s", err, sout)
	}
	sc := secretScopeOf(t, sout)
	if !strings.Contains(sc.Problem, "github-app-key") || !strings.Contains(sc.Problem, "project scope") {
		t.Fatalf("the D2 sweep is not listed as project scope: %q", sc.Problem)
	}
	if !strings.Contains(sc.Problem, "db-password") || !strings.Contains(sc.Problem, "no enabled copy this repository can resolve") {
		t.Fatalf("a name with no stored copy is missing from the listing: %q", sc.Problem)
	}
	for _, leak := range []string{"project-value", "repo-value"} {
		if strings.Contains(sout, leak) {
			t.Fatalf("doctor printed a secret value %q:\n%s", leak, sout)
		}
	}
}

// secretScopeOf is doctor --json's "secret-scope" check, which has to be
// there: the listing is the check these tests are about.
func secretScopeOf(t *testing.T, out string) doctorCheck {
	t.Helper()
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	c, ok := doctorCheckByID(o.Checks, "secret-scope")
	if !ok || c.OK || c.Severity != "info" {
		t.Fatalf("secret-scope = %+v", c)
	}
	return c
}

// TestDoctorDoesNotShadowADisabledRepositoryCopy: the repository's container
// exists but holds no enabled version (the state a disabled version leaves),
// so the project's copy is the one Fugaro resolves for the name and nothing
// is shadowed. Only an enabled version wins (design project-secrets.md §5),
// and warning here would name a footgun that is not there — and send the
// operator to a store command for a value that already reaches this
// repository.
func TestDoctorDoesNotShadowADisabledRepositoryCopy(t *testing.T) {
	r := projectSecretsRig(t)
	slug := mustSlug("github", "acme/app")
	// Seeded with no versions: latest is empty, which is what a disabled
	// version leaves behind.
	r.sm.Seed(gcp.SecretID(slug, "github-app-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "github-app-key"}, nil)
	putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if c, ok := doctorCheckByID(o.Checks, "secret-scope-shadowed"); ok {
		t.Fatalf("a disabled repository copy was reported as shadowing the project's: %+v", c)
	}
	if c := secretScopeOf(t, out); !strings.Contains(c.Problem, "github-app-key — project scope") {
		t.Fatalf("the name does not resolve to the project's copy: %q", c.Problem)
	}
	if strings.Contains(out, "project-value") {
		t.Fatalf("doctor printed a secret value:\n%s", out)
	}
}

// TestDoctorSecretScopeSilentWhenAListingIsUnreadable: neither check says
// anything when either listing could not be read. project-secret-stored
// already fails closed on exactly that state, and a second warning about the
// same unreadable listing would tell the operator nothing new — while a
// listing built from half the state could name the wrong copy as the one a
// job mounts.
func TestDoctorSecretScopeSilentWhenAListingIsUnreadable(t *testing.T) {
	facts := doctorRepoSecrets{repo: "acme/app",
		candidates: map[string]string{"github-app-key": gcp.ProjectSecretID("aurora", "github-app-key")},
		repoIDs:    map[string]string{"github-app-key": gcp.SecretID("slug", "github-app-key")}}
	for name, got := range map[string][]doctorCheck{
		"the repository's listing could not be read": doctorSecretScopeChecks(facts, nil, nil, true),
		"the project's listing could not be read":    doctorSecretScopeChecks(facts, []secretEntry{{Name: "github-app-key", Latest: "1"}}, nil, false),
	} {
		if got != nil {
			t.Errorf("%s: checks = %+v, want none", name, got)
		}
	}
}

// TestDoctorSecretScopeSaysNoEnabledCopyNotNoCopyStored: the line's last
// answer is about what a job resolves to, and the two states that reach it
// with a container in them are not "nothing is stored" (code review, minor:
// the wording said "no copy stored", which is false of both). A container
// that exists holding only disabled versions left something stored, and so
// did a project-wide copy that is enabled but not eligible for this
// repository — ProjectSecretCandidate is what makes a project copy
// reachable here, and a name outside it resolves to nothing however many
// versions the project's container holds. Neither state may be reported as
// having no copy at all.
func TestDoctorSecretScopeSaysNoEnabledCopyNotNoCopyStored(t *testing.T) {
	facts := doctorRepoSecrets{repo: "acme/app",
		repoIDs: map[string]string{"github-app-key": gcp.SecretID("slug", "github-app-key")}}
	cases := map[string]struct {
		listed  []secretEntry
		project []gcp.SecretInfo
	}{
		"the repository's container holds only a disabled version": {
			listed: []secretEntry{{Name: "github-app-key", Latest: ""}},
		},
		"the project's container holds an enabled version this repository cannot resolve": {
			listed:  []secretEntry{},
			project: []gcp.SecretInfo{{ID: gcp.ProjectSecretID("aurora", "github-app-key"), Latest: "1"}},
		},
	}
	for name, tc := range cases {
		checks := doctorSecretScopeChecks(facts, tc.listed, tc.project, true)
		if len(checks) == 0 {
			t.Fatalf("%s: no secret-scope line", name)
		}
		got := checks[0].Problem
		if strings.Contains(got, "no copy stored") {
			t.Errorf("%s: the line says a copy is not stored when one is: %q", name, got)
		}
		if !strings.Contains(got, "no enabled copy this repository can resolve") {
			t.Errorf("%s: the line does not say which copy resolves: %q", name, got)
		}
	}
}

// TestDoctorWarnsOnAShadowedProjectName: the same name is stored both
// project-wide and in this repository's own container. A repository copy
// with an enabled version wins over the project's forever (design
// project-secrets.md §5, the footgun `fugaro secrets set` warns about at
// store time), so the project's value never reaches this repository. A
// warning, not red: the next fugaro update --repo cannot change it, and the
// value the repository mounts is the one its own copy holds.
func TestDoctorWarnsOnAShadowedProjectName(t *testing.T) {
	r := projectSecretsRig(t)
	slug := mustSlug("github", "acme/app")
	r.sm.Seed(gcp.SecretID(slug, "github-app-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "github-app-key"}, []byte("repo-value"))
	putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "secret-scope-shadowed")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("secret-scope-shadowed = %+v", c)
	}
	if !strings.Contains(c.Problem, "github-app-key") || !strings.Contains(c.Problem, "wins") {
		t.Fatalf("the warning doesn't name the shadowed name and the rule: %q", c.Problem)
	}
	if !strings.Contains(c.Fix, "secrets set github-app-key --repo acme/app") {
		t.Fatalf("the fix doesn't name the command that stores the repository's own value: %q", c.Fix)
	}
	for _, leak := range []string{"project-value", "repo-value"} {
		if strings.Contains(out, leak) {
			t.Fatalf("doctor printed a secret value %q:\n%s", leak, out)
		}
	}
	// And the same footgun under the D2 sweep, with no project_secrets entry
	// anywhere: the project-wide copy is not the owner's entry's doing but
	// config.ProjectScopeByDefault's, which is the case most installations
	// are in.
	sweep := secretScopeSweepRig(t)
	sslug := mustSlug("github", "acme/app")
	sweep.sm.Seed(gcp.SecretID(sslug, "github-app-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, sslug), gcp.LabelSecret: "github-app-key"}, []byte("repo-value"))
	putProjectSecret(t, sweep.sm, "aurora", "github-app-key", []byte("project-value"))
	sout, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("the sweep's warning must not fail doctor either: %v\n%s", err, sout)
	}
	var so doctorOutput
	if jerr := json.Unmarshal([]byte(sout), &so); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, sout)
	}
	sc, ok := doctorCheckByID(so.Checks, "secret-scope-shadowed")
	if !ok || sc.OK || sc.Severity != "warning" {
		t.Fatalf("GitHub App key stored both ways with no project_secrets entry: secret-scope-shadowed = %+v", sc)
	}
	if !strings.Contains(sc.Problem, "github-app-key") || !strings.Contains(sc.Fix, "secrets set github-app-key --repo acme/app") {
		t.Fatalf("the sweep's warning does not name the name and the fix: %+v", sc)
	}
	if strings.Contains(sout, "project-value") || strings.Contains(sout, "repo-value") {
		t.Fatalf("doctor printed a secret value:\n%s", sout)
	}
}

// TestDoctorNamesEveryShadowedName: the shadowed warning's fix is printed to
// be pasted, so it carries one command per shadowed name rather than only the
// first one's (code review, minor).
func TestDoctorNamesEveryShadowedName(t *testing.T) {
	// agent.auth: api-key, so this checkout's job mounts two reserved names --
	// the App key and the Anthropic key -- and both can be stored both ways.
	r := newDoctorRig(t)
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": yaml})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	slug := mustSlug("github", "acme/app")
	addLocalLines(t, r, "project_secrets:\n  github-app-key:\n    allow_data_to: [acme/app]\n  anthropic-api-key:\n    allow_data_to: [acme/app]\n"+
		"repos:\n  acme/app: { provider: github, github_app_id: \"12345\", workflows: [app] }\n")
	for _, name := range []string{"github-app-key", "anthropic-api-key"} {
		r.sm.Seed(gcp.SecretID(slug, name), map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: name}, []byte("repo-value"))
		putProjectSecret(t, r.sm, "aurora", name, []byte("project-value"))
	}
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "secret-scope-shadowed")
	if !ok || c.OK {
		t.Fatalf("secret-scope-shadowed = %+v", c)
	}
	for _, name := range []string{"github-app-key", "anthropic-api-key"} {
		if !strings.Contains(c.Fix, "secrets set "+name+" --repo acme/app") {
			t.Errorf("the fix has no command for %s: %q", name, c.Fix)
		}
		if !strings.Contains(c.Problem, name) {
			t.Errorf("the problem does not name %s: %q", name, c.Problem)
		}
	}
}

// extraAccessorRig is a doctor rig whose local config opts github-app-key
// into project-wide storage for acme/app only, and records acme/old (which
// is not allow-listed any more) with one workflow, so its job's service
// account can be derived forward from the slug.
func extraAccessorRig(t *testing.T) *doctorRig {
	t.Helper()
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addLocalLines(t, r, projectSecretsConfig+
		"  acme/old: { provider: github, workflows: [app] }\n")
	return r
}

// TestDoctorFlagsExtraProjectSecretAccessor (S3, SF9): a project-wide
// container's live IAM policy still grants a repository the allow-list no
// longer names. That is revocation lag, expected until that repository's own
// apply runs again, so it is a warning -- and its fix is spelled out, so the
// operator needs no cross-referencing to act on it.
func TestDoctorFlagsExtraProjectSecretAccessor(t *testing.T) {
	// The fix is printed to be pasted, so it names the binary as the user
	// ran it: the literal text below is what a person sees.
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	r := extraAccessorRig(t)
	projID := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	oldSA := gcp.ServiceAccountID(mustSlug("github", "acme/old"), "app")
	r.sm.SetPolicy(projID, []gcpfake.Binding{{Role: "roles/secretmanager.secretAccessor",
		Members: []string{"serviceAccount:" + oldSA + "@proj-1234.iam.gserviceaccount.com"}}})

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-secret-extra-accessors")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("project-secret-extra-accessors = %+v", c)
	}
	if !strings.Contains(c.Problem, "acme/old") || !strings.Contains(c.Problem, "github-app-key") {
		t.Fatalf("problem doesn't name the repository and the secret: %q", c.Problem)
	}
	if !strings.Contains(c.Fix, "fugaro update --repo") || !strings.Contains(c.Fix, "--allow-delete") {
		t.Fatalf("fix doesn't name the follow-up command and its flag: %q", c.Fix)
	}
	if strings.Contains(out, "project-value") {
		t.Fatalf("a value leaked: %s", out)
	}
}

// TestDoctorNoExtraAccessorWhenTheRepoIsAllowed: the repository the policy
// grants is the one the allow-list names. Nothing to say -- and the
// installation's own build account, which the same project-wide container
// legitimately grants, is never mistaken for an extra one.
func TestDoctorNoExtraAccessorWhenTheRepoIsAllowed(t *testing.T) {
	r := extraAccessorRig(t)
	projID := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	slug := mustSlug("github", "acme/app")
	old := gcp.ServiceAccountID(mustSlug("github", "acme/old"), "app")
	r.sm.SetPolicy(projID, []gcpfake.Binding{
		{Role: "roles/secretmanager.secretAccessor", Members: []string{
			"serviceAccount:" + gcp.ServiceAccountID(slug, "app") + "@proj-1234.iam.gserviceaccount.com",
			"serviceAccount:" + gcp.BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com"}},
		{Role: "roles/secretmanager.secretAccessor", Members: []string{
			"serviceAccount:" + old + "@proj-1234.iam.gserviceaccount.com"}}})

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-secret-extra-accessors")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("project-secret-extra-accessors = %+v", c)
	}
	if strings.Contains(c.Problem, "acme/app") {
		t.Fatalf("an allow-listed repository was flagged as extra: %q", c.Problem)
	}
}

// TestDoctorFlagsExtraAccessorForNamedAccountVariant (MUST-FIX 4): a named
// Claude account's own job runs under its own, variant-keyed service
// account, so the live grant to be found is that account's, not the bare
// workflow's. A precomputed set built only from bare repository/workflow
// pairs would miss this finding entirely.
func TestDoctorFlagsExtraAccessorForNamedAccountVariant(t *testing.T) {
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	// A named account's own token is never an entry of project_secrets
	// (config.ValidateProjectSecrets accepts only config.ReservedSecrets
	// keys): its eligibility for project-wide storage comes from the
	// account's own allow_data_to, and ProjectSecretCandidate reads it from
	// there.
	addLocalLines(t, r, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n"+
		"repos:\n  acme/app: { provider: github, github_app_id: \"12345\", workflows: [app] }\n"+
		"  acme/other: { provider: github, workflows: [app] }\n")
	// claude-accounts-stored's own business: the account acme/app may choose
	// needs its token stored, or that check fails doctor outright and this
	// test could not tell a warning from a failure.
	putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), "claude-oauth-token-acme-personal", []byte("repo-value"))
	// D2's sweep makes github-app-key a project candidate for every
	// repository, so project-secret-stored now also asks whether a value is
	// stored anywhere for the key this checkout's workflow mounts: without
	// one its own check is red (no run can start), which is not what this
	// test is about.
	putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), "github-app-key", []byte("repo-value"))

	const name = "claude-oauth-token-acme-personal"
	projID := putProjectSecret(t, r.sm, "aurora", name, []byte("project-value"))
	slug := mustSlug("github", "acme/other")
	variant := gcp.ServiceAccountID(slug, gcp.WorkflowVariant("app", "acme-personal"))
	if variant == gcp.ServiceAccountID(slug, "app") {
		t.Fatal("the variant account id equals the bare workflow's: this test proves nothing")
	}
	r.sm.SetPolicy(projID, []gcpfake.Binding{{Role: "roles/secretmanager.secretAccessor",
		Members: []string{"serviceAccount:" + variant + "@proj-1234.iam.gserviceaccount.com"}}})

	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-secret-extra-accessors")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("project-secret-extra-accessors = %+v", c)
	}
	if !strings.Contains(c.Problem, "acme/other") || !strings.Contains(c.Problem, name) {
		t.Fatalf("problem doesn't name the repository and the secret: %q", c.Problem)
	}
}

// TestDoctorProjectSecretChecksReadNoValue: none of the three project-secret
// checks ever reads a value: a listing and a getIamPolicy are metadata, and
// the fake fails the test outright for an access call of any kind.
func TestDoctorProjectSecretChecksReadNoValue(t *testing.T) {
	r := extraAccessorRig(t)
	projID := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	old := gcp.ServiceAccountID(mustSlug("github", "acme/old"), "app")
	r.sm.SetPolicy(projID, []gcpfake.Binding{{Role: "roles/secretmanager.secretAccessor",
		Members: []string{"serviceAccount:" + old + "@proj-1234.iam.gserviceaccount.com"}}})
	// A deployed job still mounting the repository's own copy, so the
	// scope check has something to read too.
	slug := mustSlug("github", "acme/app")
	job := gcp.JobName(slug, "app")
	r.run.SetJobJSON(job, `{"name":"projects/proj-1234/locations/us-east5/jobs/`+job+`",
	  "template":{"template":{"containers":[{"image":"fake/app","env":[
	    {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueSource":{"secretKeyRef":{"secret":"`+gcp.SecretID(slug, "github-app-key")+`","version":"latest"}}}]}]}}}`)

	_, _, _ = execute(t, "doctor", "--json")
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("a project-secret check read a value: %v", a)
	}
}

// TestDoctorProjectSecretExtraAccessorsReadsNoValue is the project-wide
// check's own mutation-proof case: getIamPolicy and nothing else, whatever
// the policy says.
func TestDoctorProjectSecretExtraAccessorsReadsNoValue(t *testing.T) {
	r := extraAccessorRig(t)
	projID := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	old := gcp.ServiceAccountID(mustSlug("github", "acme/old"), "app")
	r.sm.SetPolicy(projID, []gcpfake.Binding{{Role: "roles/secretmanager.secretAccessor",
		Members: []string{"serviceAccount:" + old + "@proj-1234.iam.gserviceaccount.com"}}})

	// The seeding above writes (Set creates the container and adds its
	// version); only what doctor itself sends is doctor's.
	before := len(r.sm.Requests())
	_, _, _ = execute(t, "doctor", "--json")
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("the project-wide accessor check read a value: %v", a)
	}
	for _, req := range r.sm.Requests()[before:] {
		if req.Method != http.MethodGet {
			t.Errorf("the project-wide accessor check made a non-read call: %s %s", req.Method, req.Path)
		}
	}
}

// The base pin's verifier: `cosign` must be on PATH for any base pin
// (internal/basepin shells out to it, design registry-remote-repository.md
// §9 Decision 1's option (ii)), and a missing binary refuses the pin rather
// than warning past it — so doctor says so before the project's name is
// typed. It is a warning, not a failure: a repository that only copies
// history never needs it.
func TestDoctorChecksTheCosignVerifier(t *testing.T) {
	old := basepin.CosignBin
	t.Cleanup(func() { basepin.CosignBin = old })
	bin := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, o doctorOutput) doctorCheck {
		t.Helper()
		for _, c := range o.Checks {
			if c.ID == "cosign" {
				return c
			}
		}
		t.Fatalf("no cosign check in %+v", o.Checks)
		return doctorCheck{}
	}
	run := func(t *testing.T) doctorOutput {
		t.Helper()
		newDoctorRig(t)
		out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
		var o doctorOutput
		if err := json.Unmarshal([]byte(out), &o); err != nil {
			t.Fatalf("json: %v\n%s", err, out)
		}
		return o
	}

	basepin.CosignBin = bin
	if c := check(t, run(t)); !c.OK || c.Problem != "" || c.Severity != "" {
		t.Errorf("with cosign on PATH: %+v", c)
	}

	basepin.CosignBin = filepath.Join(t.TempDir(), "not-here")
	c := check(t, run(t))
	if c.OK || c.Severity != "info" || !strings.Contains(c.Problem, "cosign is not on PATH") || !strings.Contains(c.Fix, "install cosign") {
		t.Errorf("with no cosign: %+v", c)
	}
}

// projectAccountsConfig is the local config the project-account rigs add: a
// project-wide container for the named account acme-personal and one for the
// default account, and the standard profile serving both — the two jobs
// config.ProjectJobAccounts computes for that profile, each holding one
// account's token (config.ProjectJobTokenAccount: the named account on a job
// of its own, the default account on the profile's no-account job, D5).
const projectAccountsConfig = "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n" +
	"project_secrets:\n  claude-oauth-token:\n    allow_data_to: [acme/app]\n" +
	"profiles:\n  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [acme-personal, default] }\n"

// projectAccountRig is a doctor rig inside a checkout of github.com/acme/app
// whose project serves two Claude account jobs.
func projectAccountRig(t *testing.T) *doctorRig {
	t.Helper()
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	addClaudeAccounts(t, r, projectAccountsConfig)
	return r
}

// doctorProjectAccounts is doctor --json on the rig above, the checks by ID.
func doctorProjectAccounts(t *testing.T, r *doctorRig) (string, []doctorCheck) {
	t.Helper()
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("doctor read a secret value: %v", a)
	}
	return out, o.Checks
}

// TestDoctorProjectAccountStored: every shared job a profile serves for a
// Claude account mounts that account's token by a static secret_env entry, so
// the project-wide container the job names must have a stored version —
// otherwise every run of that job starts with a credential that is not
// there. doctor fails, names the job, the account and the container of each
// one, states the default account's reach, and hands over the commands that
// store them; no secret value is ever read.
//
// Mutation (run, restored): read the version out of the array instead of the
// label (treat a stored secret as missing) or skip the default account's
// entry (config.ProjectJobTokenAccount's no-account case): this test fails on
// the container it stops naming.
func TestDoctorProjectAccountStored(t *testing.T) {
	r := projectAccountRig(t)
	out, _, err := execute(t, "doctor", "--json")
	if err == nil {
		t.Fatal("want doctor to fail: no account job's token is stored")
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-account-stored")
	if !ok || c.OK || c.Severity != "" {
		t.Fatalf("project-account-stored = %+v", c)
	}
	accountJob, defaultJob := gcp.ProjectJobName("standard", "github", "acme-personal"), gcp.ProjectJobName("standard", "github", "")
	for _, want := range []string{accountJob, defaultJob, "acme-personal", "default",
		"claude-oauth-token-acme-personal", "claude-oauth-token"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the problem %q lacks %q", c.Problem, want)
		}
	}
	// The default account's reach is the sentence this check owes the owner:
	// its token rides the no-account job, so every run of that job holds it
	// (D5, SECURITY.md's residual), and doctor states it rather than gating it.
	if !strings.Contains(c.Problem, "every run of that job holds it") || !strings.Contains(c.Problem, "agent.auth is not oauth") {
		t.Errorf("the problem does not state the default account's reach: %q", c.Problem)
	}
	for _, want := range []string{"claude setup-token", "secrets set claude-oauth-token-acme-personal --project-wide", "secrets set claude-oauth-token --project-wide"} {
		if !strings.Contains(c.Fix, want) {
			t.Errorf("the fix %q lacks %q", c.Fix, want)
		}
	}
	if a := r.sm.Accessed(); len(a) != 0 {
		t.Fatalf("doctor read a secret value: %v", a)
	}
}

// TestDoctorProjectAccountStoredPresent: with both containers stored the
// check passes, and it reports each account job's reach — the job, the
// account, the container it mounts and the repositories that token may be
// used by — since that list is what the token's blast radius is (design
// §6.1). A repository's own copy does not satisfy it: a shared job mounts
// the project-wide container its secret_env names.
func TestDoctorProjectAccountStoredPresent(t *testing.T) {
	r := projectAccountRig(t)
	putProjectSecret(t, r.sm, "aurora", "claude-oauth-token-acme-personal", []byte("fake-token-0123456789"))
	putProjectSecret(t, r.sm, "aurora", "claude-oauth-token", []byte("fake-token-9876543210"))
	out, checks := doctorProjectAccounts(t, r)
	c, ok := doctorCheckByID(checks, "project-account-stored")
	if !ok || !c.OK || c.Severity != "info" {
		t.Fatalf("project-account-stored = %+v", c)
	}
	for _, want := range []string{
		gcp.ProjectJobName("standard", "github", "acme-personal"), "account acme-personal",
		"token claude-oauth-token-acme-personal", "repositories acme/app in claude_accounts.acme-personal.allow_data_to",
		gcp.ProjectJobName("standard", "github", ""), "account default",
		"token claude-oauth-token", "in project_secrets.claude-oauth-token.allow_data_to",
		"every run of that job holds it",
	} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the reach line %q lacks %q", c.Problem, want)
		}
	}
	if !strings.Contains(c.Fix, "launcher-jobs") {
		t.Errorf("the check does not point at the launcher's own reach: %q", c.Fix)
	}
	if strings.Contains(out, "fake-token") {
		t.Fatalf("a value leaked into doctor's output:\n%s", out)
	}
}

// TestDoctorProjectAccountStoredSilentWithNoAccounts: an installation whose
// profiles serve no Claude account gets no new check — byte-identical output
// to the release before this one, the same silence claude-accounts-stored
// keeps for an installation with no claude_accounts.
func TestDoctorProjectAccountStoredSilentWithNoAccounts(t *testing.T) {
	newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	_, checks := doctorProjectAccounts(t, newDoctorRig(t))
	if c, ok := doctorCheckByID(checks, "project-account-stored"); ok {
		t.Fatalf("an installation serving no account job got a new check: %+v", c)
	}
}

// TestDoctorProjectAccountStoredFailsClosedWhenUnreachable: the profile
// serves an account and the secrets list could not be read, so whether the
// container has a version is unknown, not no. doctor warns rather than going
// silent, the discipline claude-accounts-stored keeps (fail closed).
//
// Mutation (run, restored): return nil instead of a warning when the listing
// is unavailable. This test finds no check at all.
func TestDoctorProjectAccountStoredFailsClosedWhenUnreachable(t *testing.T) {
	r := projectAccountRig(t)
	data, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), r.sm.URL, "http://127.0.0.1:1", 1)
	if broken == string(data) {
		t.Fatal("secret_manager endpoint not found in the local config to break")
	}
	if err := os.WriteFile(r.cfgPath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("a listing that could not be read must warn, not fail: %v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "project-account-stored")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("project-account-stored = %+v", c)
	}
	if !strings.Contains(c.Problem, "could not list the project's secrets") ||
		!strings.Contains(c.Problem, gcp.ProjectJobName("standard", "github", "acme-personal")) {
		t.Fatalf("the warning does not say what could not be checked: %+v", c)
	}
}
