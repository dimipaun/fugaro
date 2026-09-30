package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The fugaro init --repo tests run the fugaro binary against the gcpfake
// servers and a fake terraform, from a checkout of the live-test sandbox
// fixture. The fake terraform plans whatever the test scripts; what the
// tests check is what fugaro hands it: the tfvars and imports of each
// plan, the applies, and the calls to the fakes.

const (
	initRepoProject       = "proj-1234"
	initRepoProjectName   = "aurora"
	initRepoProjectNumber = 123456789012
	initRepoRegion        = "us-east5"
	initRepoRunsBucket    = "fugaro-runs-proj-1234"
	initRepoStateBucket   = "fugaro-tfstate-proj-1234"
	initRepoBaseImage     = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-0123abc"
)

var (
	fakeTerraformOnce sync.Once
	fakeTerraformBin  string
	fakeTerraformErr  error
)

// buildFakeTerraform builds internal/infra/tf/faketerraform once per test
// binary.
func buildFakeTerraform(t *testing.T) string {
	t.Helper()
	fakeTerraformOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fugaro-e2e-faketerraform-")
		if err != nil {
			fakeTerraformErr = err
			return
		}
		fakeTerraformBin = filepath.Join(dir, "faketerraform")
		out, err := exec.Command("go", "build", "-o", fakeTerraformBin, "github.com/dimipaun/fugaro/internal/infra/tf/faketerraform").CombinedOutput()
		if err != nil {
			fakeTerraformErr = fmt.Errorf("building the fake terraform: %v\n%s", err, out)
		}
	})
	if fakeTerraformErr != nil {
		t.Fatal(fakeTerraformErr)
	}
	return fakeTerraformBin
}

type initRepoRig struct {
	fugaro, dir, cfg, checkout string
	cfgText                    string

	gcs   *gcpfake.GCS
	ar    *gcpfake.ArtifactRegistry
	iam   *gcpfake.IAM
	crm   *gcpfake.CRM
	run   *gcpfake.Run
	sm    *gcpfake.Secrets
	build *gcpfake.Build
	logs  *gcpfake.Logging
	sched *gcpfake.Scheduler
	su    *gcpfake.ServiceUsage

	builds atomic.Int32
	runs   *blobx.Bucket // the runs bucket, on the GCS fake
	repo   string
	spec   infra.RepoSpec
}

// newInitRepoRig sets up an installation that fugaro init has applied:
// the runs and state buckets, the installation's state, and a local config
// with its outputs. checkoutYAML replaces the sandbox's fugaro.yaml when
// not empty; origin is the checkout's origin.
func newInitRepoRig(t *testing.T, repo, origin string, edit func(string) string) *initRepoRig {
	t.Helper()
	testutil.IsolateGit(t)
	r := &initRepoRig{
		fugaro: testutil.BuildFugaro(t), dir: t.TempDir(), repo: repo,
		gcs: gcpfake.NewGCS(t), ar: gcpfake.NewArtifactRegistry(t), iam: gcpfake.NewIAM(t), crm: gcpfake.NewCRM(t),
		run: gcpfake.NewRun(t), sm: gcpfake.NewSecrets(t), build: gcpfake.NewBuild(t),
		logs: gcpfake.NewLogging(t), sched: gcpfake.NewScheduler(t), su: gcpfake.NewServiceUsage(t),
	}
	r.crm.AddProject(initRepoProject, initRepoProjectNumber)
	r.gcs.AddProject(initRepoProject, initRepoProjectNumber)
	r.gcs.AddBucket(initRepoRunsBucket, initRepoProjectNumber, map[string]string{"fugaro": "managed"})
	r.gcs.AddBucket(initRepoStateBucket, initRepoProjectNumber, map[string]string{"fugaro": "tfstate"})
	r.runs = r.gcs.Bucket(t, initRepoRunsBucket)
	if err := r.gcs.Bucket(t, initRepoStateBucket).WriteAll(context.Background(), infra.StatePrefixInstallation+"/default.tfstate", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(k, filepath.Join(r.dir, strings.ToLower(k)))
	}
	for _, k := range []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "GODEBUG"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	// The local config fugaro init leaves: the bootstrap's, plus the
	// installation's outputs.
	// It is the project's config, found by the checkout's project:.
	r.cfg = filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initRepoProjectName+".yaml")
	if err := os.MkdirAll(filepath.Dir(r.cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	r.cfgText = "version: 1\nname: " + initRepoProjectName + "\ngcp_project: " + initRepoProject + "\nregion: " + initRepoRegion + "\nruns_bucket: " + initRepoRunsBucket + "\n" +
		"registry: " + initRepoRegion + "-docker.pkg.dev/" + initRepoProject + "/fugaro\n" +
		"registry_host: " + initRepoRegion + "-docker.pkg.dev/" + initRepoProject + "\n" +
		"base_image: " + initRepoBaseImage + "\n" +
		"scheduler_region: us-east4\n" +
		"terraform: { state_bucket: " + initRepoStateBucket + " }\n" +
		"user: test@example.com\n" +
		"endpoints: { run: " + r.run.URL + "/, secret_manager: " + r.sm.URL + "/, cloud_build: " + r.build.URL + "/, storage: " + r.gcs.URL +
		"/storage/v1/, iam: " + r.iam.URL + "/, artifact_registry: " + r.ar.URL + "/, resource_manager: " + r.crm.URL +
		"/, logging: " + r.logs.URL + "/, cloud_scheduler: " + r.sched.URL + "/, service_usage: " + r.su.URL + "/, no_auth: true }\n"
	if err := os.WriteFile(r.cfg, []byte(r.cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", "")
	t.Setenv("FUGARO_PROJECT", "")

	// The checkout: the sandbox fixture, committed, with its origin.
	r.checkout = filepath.Join(r.dir, "checkout")
	src := filepath.Join(testutil.ModuleRoot(), "deploy", "sandbox")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
	}
	if p, err := config.ProjectOf([]byte(files["fugaro.yaml"])); err != nil || p == "" {
		// The checkout names its project, which selects the project config.
		files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "version: 1\n", "version: 1\nproject: "+initRepoProjectName+"\n", 1)
	}
	if edit != nil {
		files["fugaro.yaml"] = edit(files["fugaro.yaml"])
	}
	if err := os.MkdirAll(r.checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, r.checkout, files)
	testutil.Git(t, r.checkout, "init", "-q")
	testutil.Git(t, r.checkout, "add", "-A")
	testutil.Git(t, r.checkout, "commit", "-qm", "fixture")
	testutil.Git(t, r.checkout, "remote", "add", "origin", origin)

	// The names, from Go, as fugaro init --repo computes them.
	lc, err := localcfg.Parse([]byte(r.cfgText))
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := config.Parse([]byte(files["fugaro.yaml"]))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	in := infra.Inputs{LC: lc, Repo: repo, Cfg: cfg, RepoURL: origin}
	if cfg.Git.Provider == "github" {
		in.GitHubAppID = "123456"
	}
	if r.spec, err = infra.Repo(in); err != nil {
		t.Fatal(err)
	}

	// The build: its registry exists (the first apply made it), and its
	// promote and record steps leave what a real build leaves: latest in
	// the registry, and the record in the runs bucket.
	r.build.AddRegistry(initRepoProject, initRepoRegion, r.spec.Registry.RepositoryID)
	r.build.Steps = map[string]func(string) error{
		"build": func(string) error { r.builds.Add(1); return nil },
		"promote": func(string) error {
			for _, ws := range r.spec.Workflows {
				r.ar.SetTag(initRepoProject, initRepoRegion, r.spec.Registry.RepositoryID, path.Base(strings.TrimSuffix(ws.Image, ":latest")), "latest")
			}
			return nil
		},
		"record": func(string) error {
			for name := range r.spec.Workflows {
				if err := r.runs.WriteAll(context.Background(), imagecheck.RecordKey(r.spec.Slug, name), []byte("{}"), nil); err != nil {
					return err
				}
			}
			return nil
		},
	}

	// terraform on PATH hands the fake its log and a script chosen by the
	// root it runs in and the number of applies so far, and keeps the
	// tfvars and imports of each plan.
	bin := filepath.Join(r.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := `#!/bin/sh
d='` + r.dir + `'
root=$(basename "$PWD")
n=$(grep -c '"args":\["apply"' "$d/calls.jsonl" 2>/dev/null); n=${n:-0}
s="$d/script-$root-$n.json"; [ -f "$s" ] || s="$d/script-$root.json"
if [ "$1" = plan ]; then cp terraform.tfvars.json "$d/vars-$n.json"; cp imports.tf.json "$d/imports-$n.json"; fi
FAKE_TERRAFORM_LOG="$d/calls.jsonl" FAKE_TERRAFORM_SCRIPT="$(cat "$s" 2>/dev/null)" exec '` + buildFakeTerraform(t) + `' "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "terraform"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r.script(t, "installation", -1, map[string]any{"output": map[string]any{"stdout": installationOutputs(t)}, "show": map[string]any{"stdout": installationStateManaged}})
	r.script(t, "repo", -1, map[string]any{
		"plan":   map[string]any{"exit": 2},
		"show":   map[string]any{"stdout": planJSON(t, planChange("module.repo.google_artifact_registry_repository.images", "create"))},
		"output": map[string]any{"stdout": `{"registry":{"value":"x","type":"string","sensitive":false}}`},
	})
	return r
}

func sandboxRig(t *testing.T) *initRepoRig {
	t.Helper()
	return newInitRepoRig(t, "acme/sandbox", "https://bitbucket.org/acme/sandbox.git", nil)
}

// script writes the fake's script for root after n applies (-1: any).
func (r *initRepoRig) script(t *testing.T, root string, n int, s map[string]any) {
	t.Helper()
	name := "script-" + root + ".json"
	if n >= 0 {
		name = fmt.Sprintf("script-%s-%d.json", root, n)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func installationOutputs(t *testing.T) string {
	t.Helper()
	vals := map[string]any{
		"runs_bucket":               initRepoRunsBucket,
		"registry_host":             initRepoRegion + "-docker.pkg.dev/" + initRepoProject,
		"base_registry":             infra.BaseRegistry,
		"legacy_registry":           infra.LegacyRegistry,
		"scheduler_service_account": infra.SchedulerServiceAccountID + "@" + initRepoProject + ".iam.gserviceaccount.com",
		"role_ids": map[string]string{
			"launcher":        "projects/" + initRepoProject + "/roles/" + infra.RoleLauncher,
			"job_runner":      "projects/" + initRepoProject + "/roles/" + infra.RoleJobRunner,
			"build_submitter": "projects/" + initRepoProject + "/roles/" + infra.RoleBuildSubmitter,
			"tag_mover":       "projects/" + initRepoProject + "/roles/" + infra.RoleTagMover,
		},
		"launchers":                []string{},
		"operators":                []string{},
		"log_view":                 "projects/" + initRepoProject + "/locations/global/buckets/fugaro/views/fugaro-runs",
		"registry_cleanup_dry_run": true,
	}
	out := map[string]any{}
	for k, v := range vals {
		out[k] = map[string]any{"value": v, "sensitive": false}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func planChange(addr string, actions ...string) map[string]any {
	parts := strings.Split(addr, ".")
	typ := ""
	for _, p := range parts {
		if strings.HasPrefix(p, "google_") {
			typ, _, _ = strings.Cut(p, "[")
		}
	}
	return map[string]any{"address": addr, "type": typ,
		"change": map[string]any{"actions": actions, "before": map[string]any{}, "after": map[string]any{}}}
}

func planJSON(t *testing.T, changes ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"format_version": "1.2", "resource_changes": changes})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type cliResult struct {
	stdout, stderr string
	code           int
}

func (c cliResult) String() string {
	return "exit " + fmt.Sprint(c.code) + "\nstdout:\n" + c.stdout + "\nstderr:\n" + c.stderr
}

// fugaroInit runs fugaro init with args from a directory outside the
// checkout, with no terminal on stdin.
func (r *initRepoRig) fugaroInit(t *testing.T, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.fugaro, append([]string{"init"}, args...)...)
	cmd.Dir = r.dir
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	var xe *exec.ExitError
	switch {
	case errors.As(err, &xe):
		res.code = xe.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return res
}

// calls are the terraform subcommands run so far, as "<root> <subcommand>".
func (r *initRepoRig) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, "calls.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var c struct {
			Args []string `json:"args"`
			Dir  string   `json:"dir"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		sub := c.Args[0]
		if sub == "state" {
			sub += " " + c.Args[1] + " " + strings.Join(c.Args[len(c.Args)-1:], " ")
		}
		out = append(out, filepath.Base(c.Dir)+" "+sub)
	}
	return out
}

func count(calls []string, call string) int {
	n := 0
	for _, c := range calls {
		if c == call {
			n++
		}
	}
	return n
}

// vars are the tfvars of the plan made after n applies.
func (r *initRepoRig) vars(t *testing.T, n int) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, fmt.Sprintf("vars-%d.json", n)))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// imports are the import addresses of the plan made after n applies,
// with their IDs.
func (r *initRepoRig) imports(t *testing.T, n int) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, fmt.Sprintf("imports-%d.json", n)))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Import []infra.Import `json:"import"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, i := range doc.Import {
		out[i.To] = i.ID
	}
	return out
}

func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

var managedLabels = map[string]string{gcp.LabelManaged: gcp.ManagedValue}

func withLabels(m map[string]string, kv ...string) map[string]string {
	out := maps.Clone(m)
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

// bootstrap seeds exactly what the M4 bootstrap made for the sandbox fixture:
// each job with the M4 labels and the legacy image, its account with the
// legacy display name, the labelled secrets (stored, so with a version),
// the account's conditional grant on the runs bucket, and the accessor
// grants of the job account and of the bootstrap's shared build account.
func (r *initRepoRig) bootstrap(t *testing.T) {
	t.Helper()
	spec := r.spec
	legacyBuild := "serviceAccount:fugaro-build@" + initRepoProject + ".iam.gserviceaccount.com"
	var bucket []gcpfake.Binding
	accessors := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(spec.Workflows)) {
		ws := spec.Workflows[name]
		if ws.LegacyImage == "" {
			t.Fatal("the fixture's local config has no legacy registry")
		}
		r.iam.AddServiceAccount(initRepoProject, ws.ServiceAccountEmail, gcp.LegacyJobSADisplayName(ws.Slug, name))
		r.run.SetJob(ws.Job, ws.Labels, ws.LegacyImage+":latest")
		bucket = append(bucket, gcpfake.Binding{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:" + ws.ServiceAccountEmail},
			Condition: &gcpfake.IAMCondition{Title: ws.BucketCondition.Title, Expression: ws.BucketCondition.Expression}})
		for _, logical := range ws.SecretEnv {
			accessors[logical] = append(accessors[logical], "serviceAccount:"+ws.ServiceAccountEmail)
		}
		for _, logical := range ws.BuildSecrets {
			accessors[logical] = append(accessors[logical], legacyBuild)
		}
	}
	r.gcs.SetBucketPolicy(initRepoRunsBucket, bucket)
	for logical, id := range spec.Secrets {
		r.sm.Seed(id, withLabels(managedLabels, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte(secretValue(logical)))
		m := slices.Compact(slices.Sorted(slices.Values(accessors[logical])))
		r.sm.SetPolicy(id, []gcpfake.Binding{{Role: "roles/secretmanager.secretAccessor", Members: m}})
	}
}

// storedSecrets seeds the repository's secrets as fugaro secrets set makes
// them, each with a version, and nothing else.
func (r *initRepoRig) storedSecrets() {
	for logical, id := range r.spec.Secrets {
		r.sm.Seed(id, withLabels(managedLabels, gcp.LabelRepo, r.spec.Label, gcp.LabelSecret, logical), []byte(secretValue(logical)))
	}
}

// requests counts the requests every fake has had.
func (r *initRepoRig) requests() int {
	n := 0
	for _, s := range []*gcpfake.Server{r.gcs.Server, r.crm.Server, r.ar.Server, r.iam.Server, r.run.Server, r.sm.Server, r.build.Server, r.logs.Server, r.sched.Server} {
		n += len(s.Requests())
	}
	return n
}

// noSecretValues checks that no seeded secret value reached the output,
// the local config, or any plan's tfvars and imports.
func (r *initRepoRig) noSecretValues(t *testing.T, res cliResult) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(r.dir, "*-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, r.cfg)
	texts := map[string]string{"stdout": res.stdout, "stderr": res.stderr}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		texts[f] = string(b)
	}
	if len(files) < 3 {
		t.Fatalf("no tfvars or imports to check: %q", files)
	}
	for logical := range r.spec.Secrets {
		for where, text := range texts {
			if strings.Contains(text, secretValue(logical)) {
				t.Errorf("the value of %s reached %s", logical, where)
			}
		}
	}
}

func secretValue(logical string) string { return "planted-value-" + logical }

func (r *initRepoRig) localConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

func TestInitRepoAdoptsBootstrapResources(t *testing.T) {
	r := sandboxRig(t)
	r.bootstrap(t)
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build")
	if res.code != 0 {
		t.Fatal(res)
	}
	spec, web := r.spec, r.spec.Workflows["web"]
	p := "projects/" + initRepoProject + "/"
	want := map[string]string{
		`module.repo.module.workflow["web"].google_cloud_run_v2_job.this[0]`:  p + "locations/" + initRepoRegion + "/jobs/" + web.Job,
		`module.repo.module.workflow["web"].google_service_account.job`:       p + "serviceAccounts/" + web.ServiceAccountEmail,
		`module.repo.google_secret_manager_secret.this["bitbucket-token"]`:    p + "secrets/" + spec.Secrets["bitbucket-token"],
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`: p + "secrets/" + spec.Secrets["claude-oauth-token"],
		`module.repo.google_secret_manager_secret.this["sandbox-probe"]`:      p + "secrets/" + spec.Secrets["sandbox-probe"],
	}
	if got := r.imports(t, 0); !maps.Equal(got, want) {
		t.Errorf("imports:\n%v\nwant:\n%v", got, want)
	}
	v := r.vars(t, 0)
	if got := dig(v, "repo", "workflows", "web", "service_account", "display_name"); got != gcp.LegacyJobSADisplayName(web.Slug, "web") {
		t.Errorf("display name = %v, want the legacy one", got)
	}
	if got := dig(v, "repo", "workflows", "web", "image"); got != web.LegacyImage+":latest" {
		t.Errorf("image = %v, want the legacy %s:latest", got, web.LegacyImage)
	}
	if got := dig(v, "repo", "workflows", "web", "deploy_job"); got != true {
		t.Errorf("deploy_job = %v, want true (the job exists)", got)
	}
	if got := dig(v, "repo", "check", "paused"); spec.Check != nil && got != true {
		t.Errorf("check paused = %v, want true (no build record yet)", got)
	}
	if v["allow_job_delete"] != false {
		t.Errorf("allow_job_delete = %v", v["allow_job_delete"])
	}
	r.noSecretValues(t, res)
	if !strings.Contains(res.stdout, "IAM: 4 bindings match the live ones (adopted), 0 new") {
		t.Errorf("the summary doesn't count the adopted bindings:\n%s", res)
	}
	// The guard passed, and the plan it passed was applied.
	calls := r.calls(t)
	if count(calls, "repo apply") != 1 {
		t.Errorf("applies = %q", calls)
	}
	if n := r.builds.Load(); n != 0 {
		t.Errorf("--no-build submitted %d build(s)", n)
	}
	if !strings.Contains(res.stdout, "fugaro image build --repo acme/sandbox --workflow web") {
		t.Errorf("the missing image isn't reported:\n%s", res)
	}
	// The repository joins the local config.
	lc := r.localConfig(t)
	if got, ok := lc.Repos["acme/sandbox"]; !ok || got.Provider != "bitbucket" || got.BaseBranch != "master" || !slices.Equal(got.Workflows, []string{"web"}) {
		t.Errorf("local config repos = %+v", lc.Repos)
	}
	// The state lives under the repository's own prefix.
	backend, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", initRepoProject, "repos", spec.Slug, "gcp", "roots", "repo", infra.BackendFile))
	if err != nil || !strings.Contains(string(backend), `prefix = "`+infra.RepoStatePrefix(spec.Slug)+`"`) {
		t.Errorf("backend.hcl = %s, %v", backend, err)
	}
}

func TestInitRepoFreshPrintsSecrets(t *testing.T) {
	r := sandboxRig(t)
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if res.code != 0 {
		t.Fatal(res)
	}
	if got := r.imports(t, 0); len(got) != 0 {
		t.Errorf("imports on a fresh project: %v", got)
	}
	if got := dig(r.vars(t, 0), "repo", "workflows", "web", "deploy_job"); got != false {
		t.Errorf("deploy_job = %v, want false (no secret stored, no image)", got)
	}
	if n := r.builds.Load(); n != 0 {
		t.Errorf("a build was submitted with no secret stored")
	}
	for logical, id := range r.spec.Secrets {
		line := "fugaro secrets set " + logical + " --repo acme/sandbox"
		if !strings.Contains(res.stdout, line) || !strings.Contains(res.stdout, id) {
			t.Errorf("no command for %s:\n%s", logical, res)
		}
	}
	if !strings.Contains(res.stdout, "(you, in your own terminal) claude setup-token, then: fugaro secrets set claude-oauth-token") {
		t.Errorf("claude setup-token isn't the user's step:\n%s", res)
	}
	if !strings.Contains(res.stdout, "rerun fugaro init --repo when they are stored") {
		t.Errorf("no rerun hint:\n%s", res)
	}
}

func TestInitRepoBuildsThenDeploys(t *testing.T) {
	r := sandboxRig(t)
	r.storedSecrets()
	r.script(t, "repo", 1, map[string]any{
		"plan": map[string]any{"exit": 2},
		"show": map[string]any{"stdout": planJSON(t,
			planChange(`module.repo.module.workflow["web"].google_cloud_run_v2_job.this[0]`, "create"),
			map[string]any{"address": "module.repo.google_cloud_scheduler_job.check[0]", "type": "google_cloud_scheduler_job",
				"change": map[string]any{"actions": []string{"update"}, "before": map[string]any{"paused": true}, "after": map[string]any{"paused": false}}})},
	})
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if res.code != 0 {
		t.Fatal(res)
	}
	web := r.spec.Workflows["web"]
	if got := dig(r.vars(t, 0), "repo", "workflows", "web", "deploy_job"); got != false {
		t.Errorf("first plan: deploy_job = %v, want false (no image yet)", got)
	}
	if n := r.builds.Load(); n != 1 {
		t.Fatalf("builds = %d, want 1\n%s", n, res)
	}
	if !strings.Contains(res.stdout, "submits a Cloud Build for acme/sandbox/web") {
		t.Errorf("the build wasn't confirmed:\n%s", res)
	}
	// The first apply, the build and the second apply each asked.
	if n := strings.Count(res.stdout, "⚠ CONFIRM"); n != 3 || !strings.Contains(res.stdout, "deploying the images just built") {
		t.Errorf("%d confirmations, want 3 with the second apply's own:\n%s", n, res)
	}
	r.noSecretValues(t, res)
	if sa, _ := r.build.Last()["serviceAccount"].(string); !strings.HasSuffix(sa, "/"+r.spec.BuildServiceAccountEmail) {
		t.Errorf("the build runs as %q, not the repository's build account", sa)
	}
	v := r.vars(t, 1)
	if got := dig(v, "repo", "workflows", "web", "deploy_job"); got != true {
		t.Errorf("second plan: deploy_job = %v, want true", got)
	}
	if got := dig(v, "repo", "workflows", "web", "image"); got != web.Image {
		t.Errorf("second plan: image = %v, want %s", got, web.Image)
	}
	if got := dig(v, "repo", "check", "paused"); got != false {
		t.Errorf("second plan: paused = %v, want false (the record exists)", got)
	}
	if n := count(r.calls(t), "repo apply"); n != 2 {
		t.Errorf("applies = %d, want 2: %q", n, r.calls(t))
	}
	if !strings.Contains(res.stdout, "⚠ update module.repo.google_cloud_scheduler_job.check[0]") {
		t.Errorf("the second summary doesn't highlight the unpaused schedule:\n%s", res)
	}
	if strings.Contains(res.stdout, "fugaro secrets set") {
		t.Errorf("secrets reported missing:\n%s", res)
	}
}

// A repository whose agent authenticates through Vertex AI is recorded as
// such, so fugaro init enables the Vertex AI API; the first one says to
// rerun fugaro init.
func TestInitRepoRecordsVertex(t *testing.T) {
	r := newInitRepoRig(t, "acme/sandbox", "https://bitbucket.org/acme/sandbox.git", func(y string) string {
		return strings.Replace(y, "auth: oauth", "auth: vertex", 1)
	})
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build")
	if res.code != 0 {
		t.Fatal(res)
	}
	if got := r.localConfig(t).Repos["acme/sandbox"]; !got.Vertex {
		t.Errorf("local config repo = %+v, want vertex", got)
	}
	if !strings.Contains(res.stdout, "warning: ") || !strings.Contains(res.stdout, "Vertex AI API") || !strings.Contains(res.stdout, "rerun fugaro init") {
		t.Errorf("no warning to enable the Vertex AI API:\n%s", res)
	}
	// Recorded, it isn't repeated.
	res = r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build")
	if res.code != 0 || strings.Contains(res.stdout, "Vertex AI API") {
		t.Errorf("the second run:\n%s", res)
	}
	// An oauth repository records nothing.
	r = sandboxRig(t)
	if res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build"); res.code != 0 || r.localConfig(t).Repos["acme/sandbox"].Vertex || strings.Contains(res.stdout, "Vertex AI API") {
		t.Errorf("an oauth repository:\n%s", res)
	}
}

// A base image outside the installation's base registry is a warning, not
// a refusal: the build account reads only that registry, so its builds
// would fail at the pull.
func TestInitRepoWarnsBaseImageOutsideBaseRegistry(t *testing.T) {
	r := sandboxRig(t)
	legacy := strings.Replace(initRepoBaseImage, "/fugaro-base/", "/fugaro/", 1)
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(r.cfgText, initRepoBaseImage, legacy, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build")
	if res.code != 0 {
		t.Fatal(res)
	}
	if !strings.Contains(res.stdout, "warning: ") || !strings.Contains(res.stdout, legacy) || !strings.Contains(res.stdout, infra.BaseRegistry) {
		t.Errorf("no warning about the base image:\n%s", res)
	}
	// The one in the base registry passes silently.
	r = sandboxRig(t)
	if res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build"); res.code != 0 || strings.Contains(res.stdout, "base registry") {
		t.Errorf("a base image in the base registry:\n%s", res)
	}
}

// init --repo reads the installation's outputs in a directory of its own,
// so the installation's workdir (a fugaro init running meanwhile, or
// terraform run there by hand) keeps its tfvars, imports and saved plan.
func TestInitRepoLeavesInstallationWorkdir(t *testing.T) {
	r := sandboxRig(t)
	dir, err := infra.InstallationWorkdir(os.Getenv, initRepoProject)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "gcp", "roots", "installation")
	files := map[string]string{infra.VarsFile: `{"project":"` + initRepoProject + `"}`, infra.ImportsFile: "{}", infra.PlanFile: "a plan"}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, root, files)
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build")
	if res.code != 0 {
		t.Fatal(res)
	}
	for name, want := range files {
		if got, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(got) != want {
			t.Errorf("the installation workdir's %s = %q, %v; want it untouched", name, got, err)
		}
	}
	if count(r.calls(t), "installation output") != 1 {
		t.Errorf("calls = %q", r.calls(t))
	}
}

// A first build that fails comes after the first apply, which already
// made the repository's resources: the repository still joins the local
// config, and what it still needs is still printed, before the exit 2.
func TestInitRepoFailedBuildStillRecordsRepo(t *testing.T) {
	r := sandboxRig(t)
	r.storedSecrets()
	r.build.Steps["build"] = func(string) error { return errors.New("the build broke") }
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if res.code != 2 {
		t.Fatalf("want exit 2:\n%s", res)
	}
	if n := count(r.calls(t), "repo apply"); n != 1 {
		t.Fatalf("applies = %d, want the first one only: %q", n, r.calls(t))
	}
	if !strings.Contains(res.stdout, "Still missing for acme/sandbox") || !strings.Contains(res.stdout, "fugaro image build --repo acme/sandbox --workflow web") {
		t.Errorf("what is missing isn't printed:\n%s", res)
	}
	lc := r.localConfig(t)
	if got, ok := lc.Repos["acme/sandbox"]; !ok || !slices.Equal(got.Workflows, []string{"web"}) {
		t.Errorf("local config repos = %+v", lc.Repos)
	}
}

func TestInitRepoAdoptedSwitchesImageAfterBuild(t *testing.T) {
	r := sandboxRig(t)
	r.bootstrap(t)
	r.script(t, "repo", 1, map[string]any{
		"plan": map[string]any{"exit": 2},
		"show": map[string]any{"stdout": planJSON(t, map[string]any{
			"address": `module.repo.module.workflow["web"].google_cloud_run_v2_job.this[0]`, "type": "google_cloud_run_v2_job",
			"change": map[string]any{"actions": []string{"update"},
				"before": map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{map[string]any{"image": "old"}}}}}}},
				"after":  map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{map[string]any{"image": "new"}}}}}}}},
		})},
	})
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if res.code != 0 {
		t.Fatal(res)
	}
	web := r.spec.Workflows["web"]
	if got := dig(r.vars(t, 0), "repo", "workflows", "web", "image"); got != web.LegacyImage+":latest" {
		t.Errorf("first plan: image = %v, want the legacy one", got)
	}
	if n := r.builds.Load(); n != 1 {
		t.Fatalf("builds = %d, want 1\n%s", n, res)
	}
	v := r.vars(t, 1)
	if got := dig(v, "repo", "workflows", "web", "image"); got != web.Image {
		t.Errorf("second plan: image = %v, want %s", got, web.Image)
	}
	if got := dig(v, "repo", "workflows", "web", "service_account", "display_name"); got != gcp.LegacyJobSADisplayName(web.Slug, "web") {
		t.Errorf("second plan: display name = %v, want the legacy one", got)
	}
	if got := dig(v, "repo", "check", "paused"); got != false {
		t.Errorf("second plan: paused = %v, want false", got)
	}
	if !strings.Contains(res.stdout, "template.template.containers[0].image") {
		t.Errorf("the second summary doesn't highlight the image change:\n%s", res)
	}
	if n := count(r.calls(t), "repo apply"); n != 2 {
		t.Errorf("applies = %d, want 2", n)
	}
}

func TestInitRepoRefusesForeign(t *testing.T) {
	r := sandboxRig(t)
	id := r.spec.Secrets["bitbucket-token"]
	r.sm.Seed(id, withLabels(managedLabels, gcp.LabelRepo, "someone-else", gcp.LabelSecret, "bitbucket-token"), []byte("v"))
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if res.code != 1 {
		t.Fatalf("want exit 1:\n%s", res)
	}
	if !strings.Contains(res.stderr, "secret "+id) || !strings.Contains(res.stderr, "refuses to adopt") {
		t.Errorf("the refusal doesn't name the secret:\n%s", res)
	}
	if calls := r.calls(t); count(calls, "repo plan") != 0 || count(calls, "repo apply") != 0 {
		t.Errorf("terraform planned after a refusal: %q", calls)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "imports-0.json")); err == nil {
		t.Errorf("imports were written after a refusal")
	}
}

func TestInitRepoIdempotent(t *testing.T) {
	r := sandboxRig(t)
	r.storedSecrets()
	// Everything an earlier run made: the job on the new image, the
	// registry, the build account, the check job, and the build records.
	spec, web := r.spec, r.spec.Workflows["web"]
	r.iam.AddServiceAccount(initRepoProject, web.ServiceAccountEmail, web.ServiceAccount.DisplayName)
	r.iam.AddServiceAccount(initRepoProject, spec.BuildServiceAccountEmail, spec.BuildServiceAccount.DisplayName)
	r.run.SetJob(web.Job, web.Labels, web.Image)
	r.ar.AddRepository(initRepoProject, initRepoRegion, spec.Registry.RepositoryID, withLabels(managedLabels, gcp.LabelRepo, spec.Label))
	r.ar.SetTag(initRepoProject, initRepoRegion, spec.Registry.RepositoryID, path.Base(strings.TrimSuffix(web.Image, ":latest")), "latest")
	if spec.Check != nil {
		r.run.SetJob(spec.Check.Job, withLabels(managedLabels, gcp.LabelRepo, spec.Label, gcp.LabelRole, gcp.RoleCheck), spec.Check.Image)
	}
	if err := r.runs.WriteAll(context.Background(), imagecheck.RecordKey(spec.Slug, "web"), []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}

	first := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if first.code != 0 {
		t.Fatal(first)
	}
	cfg, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing changed since: the plan has no changes.
	r.script(t, "repo", -1, map[string]any{"plan": map[string]any{"exit": 0}})
	second := r.fugaroInit(t, "--repo", r.checkout)
	if second.code != 0 {
		t.Fatalf("second run:\n%s", second)
	}
	if strings.Contains(second.stdout+second.stderr, "CONFIRM") {
		t.Errorf("the second run asked:\n%s", second)
	}
	if n := count(r.calls(t), "repo apply"); n != 1 {
		t.Errorf("applies = %d, want only the first run's", n)
	}
	if n := r.builds.Load(); n != 0 {
		t.Errorf("builds = %d with a record", n)
	}
	if after, _ := os.ReadFile(r.cfg); !bytes.Equal(after, cfg) {
		t.Errorf("the second run changed the local config")
	}
	if got := dig(r.vars(t, 1), "repo", "check", "paused"); got != false {
		t.Errorf("paused = %v with every record", got)
	}
}

func TestInitRepoGitHubNeedsAppID(t *testing.T) {
	r := newInitRepoRig(t, "acme/webapp", "https://github.com/acme/webapp.git", func(y string) string {
		return strings.Replace(y, "provider: bitbucket, base_branch: master", "provider: github, base_branch: main", 1)
	})
	before := r.requests()
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if res.code != 1 || !strings.Contains(res.stderr, "--github-app-id") {
		t.Fatalf("want exit 1 naming --github-app-id:\n%s", res)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("terraform ran: %q", calls)
	}
	if after := r.requests(); after != before {
		t.Errorf("%d cloud request(s) before the refusal", after-before)
	}

	res = r.fugaroInit(t, "--repo", r.checkout, "--github-app-id", "123456", "--plan-only")
	if res.code != 0 {
		t.Fatal(res)
	}
	v := r.vars(t, 0)
	if v["github_app_id"] != "123456" {
		t.Errorf("github_app_id = %v", v["github_app_id"])
	}
	if got := dig(v, "repo", "provider"); got != "github" {
		t.Errorf("provider = %v", got)
	}
	if count(r.calls(t), "repo apply") != 0 {
		t.Errorf("--plan-only applied")
	}
}

func TestInitRepoPrintVarsNoCalls(t *testing.T) {
	r := sandboxRig(t)
	before := r.requests()
	res := r.fugaroInit(t, "--repo", r.checkout, "--print-vars", "--allow-job-delete")
	if res.code != 0 {
		t.Fatal(res)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &v); err != nil {
		t.Fatalf("%v:\n%s", err, res)
	}
	if dig(v, "repo", "slug") != r.spec.Slug || v["allow_job_delete"] != true {
		t.Errorf("vars = %v", v)
	}
	// The values skip discovery and the readiness gates, which need cloud
	// calls, so a team applying them itself is told what that means.
	for _, want := range []string{"warning:", "ungated", "deploy_job", "image", "display name", "fugaro init --repo"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, res.stderr)
		}
	}
	if after := r.requests(); after != before {
		t.Errorf("--print-vars made %d cloud request(s)", after-before)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("--print-vars ran terraform: %q", calls)
	}
}

func TestInitRepoAllowJobDeleteHighlighted(t *testing.T) {
	r := sandboxRig(t)
	res := r.fugaroInit(t, "--repo", r.checkout, "--allow-job-delete", "--plan-only")
	if res.code != 0 {
		t.Fatal(res)
	}
	if r.vars(t, 0)["allow_job_delete"] != true {
		t.Errorf("allow_job_delete isn't set")
	}
	if !strings.Contains(res.stdout, "⚠ --allow-job-delete") {
		t.Errorf("the summary doesn't highlight --allow-job-delete:\n%s", res)
	}
}

// stateManaged and stateEmpty are terraform show -json of the
// repository's state before and after state rm (which leaves the outputs).
const (
	stateManaged = `{"format_version":"1.0","values":{"outputs":{"registry":{"value":"x"}},"root_module":{"child_modules":[{"address":"module.repo","resources":[{"address":"module.repo.google_service_account.build"}]}]}}}`
	stateEmpty   = `{"format_version":"1.0","values":{"outputs":{"registry":{"value":"x"}},"root_module":{}}}`
	// installationStateManaged is the installation's state while
	// Terraform manages it.
	installationStateManaged = `{"format_version":"1.0","values":{"outputs":{"runs_bucket":{"value":"x"}},"root_module":{"child_modules":[{"address":"module.installation","resources":[{"address":"module.installation.google_storage_bucket.runs"}]}]}}}`
)

// forgetRig is a sandbox rig whose repository has been applied: its state
// object exists in the (versioned) state bucket and manages resources.
func forgetRig(t *testing.T) (*initRepoRig, *blobx.Bucket, string) {
	t.Helper()
	r := sandboxRig(t)
	r.gcs.SetVersioning(initRepoStateBucket, true)
	state := r.gcs.Bucket(t, initRepoStateBucket)
	key := infra.RepoStatePrefix(r.spec.Slug) + "/default.tfstate"
	if err := state.WriteAll(context.Background(), key, []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	r.script(t, "repo", -1, map[string]any{"show": map[string]any{"stdout": stateManaged}})
	return r, state, key
}

func exists(t *testing.T, b *blobx.Bucket, key string) bool {
	t.Helper()
	ok, err := b.Exists(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// After the installation's --forget (state rm), its state keeps its
// outputs but manages nothing: init --repo refuses to plan against it,
// and says to run fugaro init first.
func TestInitRepoRefusesForgottenInstallation(t *testing.T) {
	r := sandboxRig(t)
	r.script(t, "installation", -1, map[string]any{"output": map[string]any{"stdout": installationOutputs(t)}, "show": map[string]any{"stdout": stateEmpty}})
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build")
	if res.code != 1 || !strings.Contains(res.stderr, "run fugaro init first") || !strings.Contains(res.stderr, "forgotten") {
		t.Fatalf("init --repo on a forgotten installation:\n%s", res)
	}
	if calls := r.calls(t); count(calls, "repo plan") != 0 || count(calls, "repo apply") != 0 {
		t.Errorf("calls = %q, want no plan or apply of the repository", calls)
	}
}

// An installation applied before the tag mover role existed outputs no
// role_ids.tag_mover: init --repo refuses to plan a grant of a role that
// isn't there, and says to run fugaro init first.
func TestInitRepoRefusesInstallationWithoutTagMover(t *testing.T) {
	r := sandboxRig(t)
	var outs map[string]map[string]any
	if err := json.Unmarshal([]byte(installationOutputs(t)), &outs); err != nil {
		t.Fatal(err)
	}
	delete(outs["role_ids"]["value"].(map[string]any), "tag_mover")
	b, err := json.Marshal(outs)
	if err != nil {
		t.Fatal(err)
	}
	r.script(t, "installation", -1, map[string]any{"output": map[string]any{"stdout": string(b)}, "show": map[string]any{"stdout": installationStateManaged}})
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build")
	if res.code != 1 || !strings.Contains(res.stderr, "run fugaro init first") || !strings.Contains(res.stderr, "tag_mover") {
		t.Fatalf("init --repo on an installation without the tag mover role:\n%s", res)
	}
	if calls := r.calls(t); count(calls, "repo plan") != 0 || count(calls, "repo apply") != 0 {
		t.Errorf("calls = %q, want no plan or apply of the repository", calls)
	}
}

func TestInitRepoForget(t *testing.T) {
	r, state, key := forgetRig(t)
	// Without a confirmation, nothing is forgotten.
	res := r.fugaroInit(t, "--repo", r.checkout, "--forget")
	if res.code != 1 || count(r.calls(t), "repo state rm module.repo") != 0 || !exists(t, state, key) {
		t.Fatalf("forgot without a confirmation:\n%s\n%q", res, r.calls(t))
	}
	res = r.fugaroInit(t, "--repo", r.checkout, "--forget", "--yes")
	if res.code != 0 {
		t.Fatal(res)
	}
	calls := r.calls(t)
	if count(calls, "repo state rm module.repo") != 1 || count(calls, "repo plan") != 0 || count(calls, "repo apply") != 0 {
		t.Errorf("calls = %q, want one state rm and no plan or apply", calls)
	}
	if exists(t, state, key) {
		t.Errorf("the repository's state object is still there")
	}
	if !exists(t, state, infra.StatePrefixInstallation+"/default.tfstate") {
		t.Errorf("the installation's state went too")
	}
}

// The rollback is recoverable: after init --repo --forget and init
// --forget (state rm, which destroys nothing), a retried fugaro init and
// init --repo import what state rm left behind instead of planning creates
// that fail with 409: the custom roles, the scheduler account, the log
// bucket (once undeleted) and the repository's Scheduler job, next to
// what they imported before.
func TestInitForgetThenReinitImports(t *testing.T) {
	r, _, _ := forgetRig(t)
	r.bootstrap(t)
	spec := r.spec
	if spec.Check == nil {
		t.Fatal("the fixture has no daily check")
	}
	// What the M5 applies made, besides the bootstrap's.
	r.ar.AddRepository(initRepoProject, initRepoRegion, infra.BaseRegistry, managedLabels)
	r.ar.AddRepository(initRepoProject, initRepoRegion, spec.Registry.RepositoryID, withLabels(managedLabels, gcp.LabelRepo, spec.Label))
	r.iam.AddServiceAccount(initRepoProject, spec.BuildServiceAccountEmail, spec.BuildServiceAccount.DisplayName)
	r.run.SetJob(spec.Check.Job, withLabels(managedLabels, gcp.LabelRepo, spec.Label, gcp.LabelRole, gcp.RoleCheck), spec.Check.Image)
	checkURI := "https://run.googleapis.com/v2/projects/" + initRepoProject + "/locations/" + initRepoRegion + "/jobs/" + spec.Check.Job + ":run"
	r.sched.SetJob(initRepoProject, spec.Check.SchedulerRegion, spec.Check.SchedulerJob, checkURI, spec.Installation.SchedulerServiceAccount)
	r.iam.AddRole(initRepoProject, infra.RoleLauncher, "Fugaro launcher", false)
	r.iam.AddRole(initRepoProject, infra.RoleJobRunner, "Fugaro job runner", false)
	r.iam.AddRole(initRepoProject, infra.RoleBuildSubmitter, "Fugaro build submitter", false)
	scheduler := infra.SchedulerServiceAccountID + "@" + initRepoProject + ".iam.gserviceaccount.com"
	r.iam.AddServiceAccount(initRepoProject, scheduler, "Fugaro scheduler")
	// The rollback's apply deleted the log bucket: it is pending deletion.
	r.logs.AddBucket(initRepoProject, "global", infra.LogBucket, "DELETE_REQUESTED", infra.LogBucketDescription)

	// The rollback: the repository, then the installation.
	if res := r.fugaroInit(t, "--repo", r.checkout, "--forget", "--yes"); res.code != 0 {
		t.Fatal(res)
	}
	if res := r.fugaroInit(t, "--forget", "--yes"); res.code != 0 {
		t.Fatal(res)
	}
	if calls := r.calls(t); count(calls, "repo state rm module.repo") != 1 || count(calls, "installation state rm module.installation") != 1 {
		t.Fatalf("calls = %q", calls)
	}

	// A retry before the undelete is refused, naming the undelete, and
	// plans nothing.
	plans := count(r.calls(t), "installation plan")
	res := r.fugaroInit(t, "--yes")
	if res.code != 1 || !strings.Contains(res.stderr, infra.LogBucketUndelete(initRepoProject)) {
		t.Fatalf("a retry with the log bucket pending deletion:\n%s", res)
	}
	if count(r.calls(t), "installation plan") != plans {
		t.Fatalf("planned with the log bucket pending deletion: %q", r.calls(t))
	}

	// Once undeleted, the retry imports every installation resource.
	r.logs.AddBucket(initRepoProject, "global", infra.LogBucket, "ACTIVE", infra.LogBucketDescription)
	if res := r.fugaroInit(t, "--yes"); res.code != 0 {
		t.Fatal(res)
	}
	p := "projects/" + initRepoProject + "/"
	want := map[string]string{
		"module.installation.google_storage_bucket.runs":                     initRepoProject + "/" + initRepoRunsBucket,
		"module.installation.google_artifact_registry_repository.base":       p + "locations/" + initRepoRegion + "/repositories/" + infra.BaseRegistry,
		"module.installation.google_project_iam_custom_role.launcher":        p + "roles/" + infra.RoleLauncher,
		"module.installation.google_project_iam_custom_role.job_runner":      p + "roles/" + infra.RoleJobRunner,
		"module.installation.google_project_iam_custom_role.build_submitter": p + "roles/" + infra.RoleBuildSubmitter,
		"module.installation.google_service_account.scheduler":               p + "serviceAccounts/" + scheduler,
		"module.installation.google_logging_project_bucket_config.fugaro[0]": p + "locations/global/buckets/" + infra.LogBucket,
	}
	if got := r.imports(t, 0); !maps.Equal(got, want) {
		t.Errorf("installation imports:\n%v\nwant:\n%v", got, want)
	}

	// And init --repo imports the Scheduler job, in the scheduler region,
	// with the rest of the repository.
	if res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build"); res.code != 0 {
		t.Fatal(res)
	}
	web := spec.Workflows["web"]
	want = map[string]string{
		`module.repo.google_cloud_scheduler_job.check[0]`:                     p + "locations/" + spec.Check.SchedulerRegion + "/jobs/" + spec.Check.SchedulerJob,
		`module.repo.google_cloud_run_v2_job.check[0]`:                        p + "locations/" + initRepoRegion + "/jobs/" + spec.Check.Job,
		`module.repo.google_artifact_registry_repository.images`:              p + "locations/" + initRepoRegion + "/repositories/" + spec.Registry.RepositoryID,
		`module.repo.google_service_account.build`:                            p + "serviceAccounts/" + spec.BuildServiceAccountEmail,
		`module.repo.module.workflow["web"].google_cloud_run_v2_job.this[0]`:  p + "locations/" + initRepoRegion + "/jobs/" + web.Job,
		`module.repo.module.workflow["web"].google_service_account.job`:       p + "serviceAccounts/" + web.ServiceAccountEmail,
		`module.repo.google_secret_manager_secret.this["bitbucket-token"]`:    p + "secrets/" + spec.Secrets["bitbucket-token"],
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`: p + "secrets/" + spec.Secrets["claude-oauth-token"],
		`module.repo.google_secret_manager_secret.this["sandbox-probe"]`:      p + "secrets/" + spec.Secrets["sandbox-probe"],
	}
	if got := r.imports(t, 0); !maps.Equal(got, want) {
		t.Errorf("repository imports:\n%v\nwant:\n%v", got, want)
	}
}

// A run that stops between state rm and the object's delete leaves a state
// with outputs but no resources; the next run skips state rm (which would
// fail on an address that is gone) and deletes the object.
func TestInitRepoForgetResumesAfterPartialFailure(t *testing.T) {
	r, state, key := forgetRig(t)
	r.gcs.FailObjectDeletes(1)
	res := r.fugaroInit(t, "--repo", r.checkout, "--forget", "--yes")
	if res.code != 2 || !strings.Contains(res.stderr, "rerun fugaro init --repo --forget") {
		t.Fatalf("want exit 2 asking for a rerun:\n%s", res)
	}
	if count(r.calls(t), "repo state rm module.repo") != 1 || !exists(t, state, key) {
		t.Fatalf("after the failed delete: calls %q, object there %v", r.calls(t), exists(t, state, key))
	}
	// What state rm left: outputs, no resources.
	r.script(t, "repo", -1, map[string]any{
		"show":     map[string]any{"stdout": stateEmpty},
		"state rm": map[string]any{"exit": 1, "stderr": "Invalid target address: No matching objects found"},
	})
	res = r.fugaroInit(t, "--repo", r.checkout, "--forget", "--yes")
	if res.code != 0 {
		t.Fatal(res)
	}
	if !strings.Contains(res.stdout, "manages nothing any more") {
		t.Errorf("the confirmation doesn't say only the object is left:\n%s", res)
	}
	if n := count(r.calls(t), "repo state rm module.repo"); n != 1 {
		t.Errorf("state rm ran %d times, want only the first run's", n)
	}
	if exists(t, state, key) {
		t.Errorf("the state object is still there")
	}
	// And then there is nothing left to forget.
	res = r.fugaroInit(t, "--repo", r.checkout, "--forget", "--yes")
	if res.code != 1 || !strings.Contains(res.stderr, "nothing to forget") {
		t.Errorf("a third run:\n%s", res)
	}
}

func TestInitRepoForgetStateRmFailsKeepsObject(t *testing.T) {
	r, state, key := forgetRig(t)
	r.script(t, "repo", -1, map[string]any{
		"show":     map[string]any{"stdout": stateManaged},
		"state rm": map[string]any{"exit": 1, "stderr": "Error acquiring the state lock"},
	})
	res := r.fugaroInit(t, "--repo", r.checkout, "--forget", "--yes")
	if res.code != 2 {
		t.Fatalf("want exit 2:\n%s", res)
	}
	if !exists(t, state, key) {
		t.Errorf("the state object was deleted though state rm failed")
	}
}

func TestInitRepoForgetRefusesUnversionedStateBucket(t *testing.T) {
	r, state, key := forgetRig(t)
	r.gcs.SetVersioning(initRepoStateBucket, false)
	res := r.fugaroInit(t, "--repo", r.checkout, "--forget", "--yes")
	if res.code != 1 || !strings.Contains(res.stderr, "no object versioning") {
		t.Fatalf("want exit 1 about versioning:\n%s", res)
	}
	if count(r.calls(t), "repo state rm module.repo") != 0 || !exists(t, state, key) {
		t.Errorf("forgot anyway: %q", r.calls(t))
	}
}

func TestInitRepoNeedsInstallation(t *testing.T) {
	r := sandboxRig(t)
	if err := r.gcs.Bucket(t, initRepoStateBucket).Delete(context.Background(), infra.StatePrefixInstallation+"/default.tfstate"); err != nil {
		t.Fatal(err)
	}
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if res.code != 1 || !strings.Contains(res.stderr, "run fugaro init first") {
		t.Fatalf("want exit 1 with run fugaro init first:\n%s", res)
	}
}

func TestInitRepoNeedsFugaroYAML(t *testing.T) {
	r := sandboxRig(t)
	testutil.Git(t, r.checkout, "rm", "-q", "fugaro.yaml")
	res := r.fugaroInit(t, "--repo", r.checkout, "--yes")
	if res.code != 1 || !strings.Contains(res.stderr, "/fugaro:onboard") || !strings.Contains(res.stderr, "fugaro config example") {
		t.Fatalf("want exit 1 pointing at /fugaro:onboard:\n%s", res)
	}
}

func TestInitRepoRefusesInstallationFlags(t *testing.T) {
	r := sandboxRig(t)
	for _, args := range [][]string{
		{"--repo", r.checkout, "--config-only"},
		{"--repo", r.checkout, "--launcher", "user:a@example.com"},
		{"--repo", r.checkout, "--registry-cleanup", "on"},
		{"--repo", r.checkout, "--forget", "--allow-job-delete"},
		{"--repo", r.checkout, "--plan-only", "--print-vars"},
		{"--github-app-id", "123456"},
		{"--no-build"},
		{r.checkout},
	} {
		res := r.fugaroInit(t, args...)
		if res.code != 1 {
			t.Errorf("%q: want exit 1:\n%s", args, res)
		}
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("terraform ran: %q", calls)
	}
}

// With no terminal and no --yes, the billable build is not started: the
// banner and a warning are shown, and the run still exits 0.
func TestInitRepoBuildNeedsConfirmation(t *testing.T) {
	r := sandboxRig(t)
	r.storedSecrets()
	// A first run records the repository without building.
	if res := r.fugaroInit(t, "--repo", r.checkout, "--yes", "--no-build"); res.code != 0 {
		t.Fatal(res)
	}
	r.script(t, "repo", -1, map[string]any{"plan": map[string]any{"exit": 0}})
	res := r.fugaroInit(t, "--repo", r.checkout)
	if res.code != 0 {
		t.Fatalf("want exit 0:\n%s", res)
	}
	if !strings.Contains(res.stdout, "⚠ CONFIRM (project "+initRepoProjectName+", GCP project "+initRepoProject+"): submits a Cloud Build for acme/sandbox/web") {
		t.Errorf("no build banner:\n%s", res)
	}
	if !strings.Contains(res.stdout, "warning: the first image build of acme/sandbox/web was not confirmed") {
		t.Errorf("no warning:\n%s", res)
	}
	if n := r.builds.Load(); n != 0 {
		t.Errorf("builds = %d without a confirmation", n)
	}
	if len(r.build.Requests()) != 0 {
		t.Errorf("Cloud Build was called: %d request(s)", len(r.build.Requests()))
	}
}

// init --repo on a project where Cloud Resource Manager is disabled
// enables it once confirmed, and a disabled Cloud Scheduler hides nothing
// to adopt: the check's Scheduler job is planned as a create.
func TestInitRepoEnablesResourceManager(t *testing.T) {
	r := sandboxRig(t)
	r.bootstrap(t)
	if r.spec.Check == nil {
		t.Fatal("the fixture needs a check")
	}
	r.sched.SetJob(initRepoProject, r.spec.Check.SchedulerRegion, r.spec.Check.SchedulerJob,
		"https://example.com/not-ours", r.spec.Installation.SchedulerServiceAccount)
	r.su.Disable(infra.ServiceResourceManager, r.crm.Server)
	r.su.Disable("cloudscheduler.googleapis.com", r.sched.Server)

	// No terminal, no --yes: exit 1 with the command, nothing enabled.
	res := r.fugaroInit(t, "--repo", r.checkout, "--plan-only")
	if res.code != 1 || !strings.Contains(res.stderr, "gcloud services enable cloudresourcemanager.googleapis.com --project "+initRepoProject) {
		t.Fatalf("want exit 1 with the gcloud command:\n%s", res)
	}
	if len(r.su.Enables()) != 0 || len(r.calls(t)) != 0 {
		t.Fatalf("enables %q, terraform calls %q", r.su.Enables(), r.calls(t))
	}

	res = r.fugaroInit(t, "--repo", r.checkout, "--plan-only", "--yes")
	if res.code != 0 {
		t.Fatal(res)
	}
	if got := r.su.Enables(); !slices.Equal(got, []string{infra.ServiceResourceManager}) {
		t.Fatalf("enables = %q, want only Resource Manager", got)
	}
	if !strings.Contains(res.stdout, "⚠ CONFIRM (project "+initRepoProjectName+", GCP project "+initRepoProject+"): enables the Cloud Resource Manager API") {
		t.Errorf("no confirmation:\n%s", res)
	}
	if _, ok := r.imports(t, 0)["module.repo.google_cloud_scheduler_job.check[0]"]; ok {
		t.Errorf("a Scheduler job was imported from a disabled API")
	}

	// A Scheduler API that refuses for want of permission still fails closed.
	r.sched.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
	if res := r.fugaroInit(t, "--repo", r.checkout, "--plan-only"); res.code != 2 || !strings.Contains(res.stderr, "Cloud Scheduler job") {
		t.Fatalf("want exit 2 on a permission 403:\n%s", res)
	}
}

// init --repo with the Storage API disabled fails as a remote error at
// the state bucket, not as "no installation: run fugaro init first".
func TestInitRepoStorageDisabledIsRemote(t *testing.T) {
	r := sandboxRig(t)
	r.su.Disable("storage.googleapis.com", r.gcs.Server)
	res := r.fugaroInit(t, "--repo", r.checkout, "--plan-only", "--yes")
	if res.code != 2 || !strings.Contains(res.stderr, "state bucket") || strings.Contains(res.stderr, "run fugaro init first") {
		t.Fatalf("want exit 2 at the state bucket:\n%s", res)
	}
	if len(r.calls(t)) != 0 {
		t.Fatalf("terraform calls %q", r.calls(t))
	}
}
