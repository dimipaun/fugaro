package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
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
	// Hermetic: a stand-in terraform (CI has none), no real Claude Code
	// config, and a fixed binary version.
	withVersion(t, "0.2.0")
	doctorFakeTerraform(t, dir)
	path := isolateProjects(t, dir)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"user: someone@example.com\n" +
		"endpoints: { resource_manager: " + r.crm.URL + "/, cloud_billing: " + r.billing.URL + "/, secret_manager: " + r.sm.URL + "/, no_auth: true }\n"
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
	return r, &auth
}

// doctor says whether each recorded GitHub App is installed with what runs
// ask for: failing with the fix, a warning for what it must not hold, and
// never a secret in its output.
func TestDoctorGitHubApp(t *testing.T) {
	doctorChecks := func(t *testing.T) (out string, checks []doctorCheck) {
		t.Helper()
		out, errOut, _ := execute(t, "doctor", "--check-github-app", "--json", "--dir", t.TempDir())
		var o doctorOutput
		if err := json.Unmarshal([]byte(out), &o); err != nil {
			t.Fatalf("%v\n%s\n%s", err, out, errOut)
		}
		return out + errOut, o.Checks
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
	if !strings.Contains(c.Fix, "claude setup-token") || !strings.Contains(c.Fix, "secrets set claude-oauth-token-acme-personal --repo acme/app") {
		t.Fatalf("fix doesn't name the exact commands: %q", c.Fix)
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
	c := claudeAccountsStoredCheck(lc, hostileRepo, []secretEntry{})
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
	w := claudeAccountsStoredCheck(lc, hostileRepo, nil)
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
  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }
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
