package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// doctorRig is a local project config ("aurora", GCP proj-1234) pointing at
// fresh Resource Manager, Billing and Secret Manager fakes, with no
// installation other than that config.
type doctorRig struct {
	dir     string
	crm     *gcpfake.CRM
	billing *gcpfake.Billing
	sm      *gcpfake.Secrets
}

func newDoctorRig(t *testing.T) *doctorRig {
	t.Helper()
	dir := t.TempDir()
	r := &doctorRig{dir: dir, crm: gcpfake.NewCRM(t), billing: gcpfake.NewBilling(t), sm: gcpfake.NewSecrets(t)}
	r.crm.AddProject("proj-1234", 123456789012)
	r.crm.SetPolicy("proj-1234", gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}})
	r.billing.SetBilling("proj-1234", true)
	for _, k := range []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "GODEBUG"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	path := isolateProjects(t, dir)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"user: someone@example.com\n" +
		"endpoints: { resource_manager: " + r.crm.URL + "/, cloud_billing: " + r.billing.URL + "/, secret_manager: " + r.sm.URL + "/, no_auth: true }\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return r
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
// doctor says so and names fugaro init as the fix, rather than guessing at
// one or crashing.
func TestDoctorWithNoInstallationSaysRunInit(t *testing.T) {
	isolateProjects(t, t.TempDir())
	out, _, err := execute(t, "doctor", "--dir", t.TempDir())
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "fugaro init") {
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
	if !ok || c.OK || !strings.Contains(c.Fix, "fugaro init") {
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

// TestDoctorNotInstalledIsInformational: a correctly pinned plugin nobody
// has installed yet is informational, under --strict or not: a teammate who
// hasn't opened the folder in Claude Code is not a CI failure.
func TestDoctorNotInstalledIsInformational(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	out, _, err := execute(t, "doctor", "--plugin", "--strict", "--json", "--dir", root)
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
	if !o.OK {
		t.Fatalf("an informational state failed doctor: %+v", o)
	}
}

// TestDoctorNeverPrintsSecretValues: doctor lists a repository's secrets by
// name (design: "secrets by name from secrets ls"), and the value never
// reaches stdout, stderr or --json, in either outcome.
func TestDoctorNeverPrintsSecretValues(t *testing.T) {
	r := newDoctorRig(t)
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	const value = "super-secret-token-value-ABC123"
	putSecret(t, r.sm, "proj-1234", mustSlug("github", "acme/app"), "claude-oauth-token", []byte(value))

	out, errOut, err := execute(t, "doctor")
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
