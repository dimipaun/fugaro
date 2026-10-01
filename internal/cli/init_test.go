package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	initProjectName   = "aurora"
	initProject       = "proj-1234"
	initProjectNumber = 123456789012
	initRunsBucket    = "fugaro-runs-proj-1234"
	initStateBucket   = "fugaro-tfstate-proj-1234"
)

var (
	fakeTFOnce sync.Once
	fakeTFBin  string
	fakeTFErr  error
)

// fakeTerraform builds the fake terraform once per test binary.
func fakeTerraform(t *testing.T) string {
	t.Helper()
	fakeTFOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fugaro-cli-faketerraform-")
		if err != nil {
			fakeTFErr = err
			return
		}
		fakeTFBin = filepath.Join(dir, "faketerraform")
		out, err := exec.Command("go", "build", "-o", fakeTFBin, "github.com/dimipaun/fugaro/internal/infra/tf/faketerraform").CombinedOutput()
		if err != nil {
			fakeTFErr = fmt.Errorf("building the fake terraform: %v\n%s", err, out)
		}
	})
	if fakeTFErr != nil {
		t.Fatal(fakeTFErr)
	}
	return fakeTFBin
}

// initRig is a local config pointing at fakes, a fake terraform on PATH,
// and private XDG directories.
type initRig struct {
	dir, cfg, log, scriptPath string
	script                    map[string]any
	gcs                       *gcpfake.GCS
	ar                        *gcpfake.ArtifactRegistry
	iam                       *gcpfake.IAM
	crm                       *gcpfake.CRM
	run                       *gcpfake.Run
	sm                        *gcpfake.Secrets
	logs                      *gcpfake.Logging
	sched                     *gcpfake.Scheduler
	su                        *gcpfake.ServiceUsage
}

const initConfig = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
`

func newInitRig(t *testing.T) *initRig {
	t.Helper()
	r := &initRig{
		dir: t.TempDir(), gcs: gcpfake.NewGCS(t), ar: gcpfake.NewArtifactRegistry(t), iam: gcpfake.NewIAM(t),
		crm: gcpfake.NewCRM(t), run: gcpfake.NewRun(t), sm: gcpfake.NewSecrets(t),
		logs: gcpfake.NewLogging(t), sched: gcpfake.NewScheduler(t), su: gcpfake.NewServiceUsage(t),
		script: map[string]any{},
	}
	r.crm.AddProject(initProject, initProjectNumber)
	r.gcs.AddProject(initProject, initProjectNumber)
	for _, k := range []string{"XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(k, filepath.Join(r.dir, strings.ToLower(k)))
	}
	for _, k := range []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "GODEBUG"} {
		t.Setenv(k, "") // restored when the test ends
		os.Unsetenv(k)
	}
	r.cfg = filepath.Join(r.dir, "config.yaml")
	cfg := initConfig + "endpoints: { run: " + r.run.URL + "/, secret_manager: " + r.sm.URL + "/, storage: " + r.gcs.URL + "/storage/v1/, iam: " +
		r.iam.URL + "/, artifact_registry: " + r.ar.URL + "/, resource_manager: " + r.crm.URL + "/, logging: " + r.logs.URL +
		"/, cloud_scheduler: " + r.sched.URL + "/, service_usage: " + r.su.URL + "/, no_auth: true }\n" +
		"repos:\n  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }\n"
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", r.cfg)

	// terraform on PATH is a wrapper that hands the fake its log and
	// script, which the allowlisted environment would drop.
	bin := filepath.Join(r.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	r.log = filepath.Join(r.dir, "terraform-calls.jsonl")
	r.scriptPath = filepath.Join(r.dir, "terraform-script.json")
	wrapper := "#!/bin/sh\nFAKE_TERRAFORM_LOG='" + r.log + "' FAKE_TERRAFORM_SCRIPT=\"$(cat '" + r.scriptPath + "')\" exec '" + fakeTerraform(t) + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "terraform"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r.setPlan(t, change("module.installation.google_storage_bucket.runs", "create"))
	r.script["plan"] = map[string]any{"exit": 2}
	r.script["output"] = map[string]any{"stdout": outputsJSON(t)}
	r.save(t)
	return r
}

func (r *initRig) appendConfig(t *testing.T, yaml string) {
	t.Helper()
	f, err := os.OpenFile(r.cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(yaml); err != nil {
		t.Fatal(err)
	}
}

func (r *initRig) save(t *testing.T) {
	t.Helper()
	b, err := json.Marshal(r.script)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.scriptPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

type planChange = map[string]any

func change(addr string, actions ...string) planChange {
	return planChange{"address": addr, "type": strings.Split(strings.TrimPrefix(addr, "module.installation."), ".")[0],
		"change": map[string]any{"actions": actions, "before": map[string]any{}, "after": map[string]any{}}}
}

// setPlan makes the fake's show -json report changes.
func (r *initRig) setPlan(t *testing.T, changes ...planChange) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"format_version": "1.2", "resource_changes": changes})
	if err != nil {
		t.Fatal(err)
	}
	r.script["show"] = map[string]any{"stdout": string(b)}
	r.save(t)
}

// stateBucket makes the state bucket exist, marked, in the project.
func (r *initRig) stateBucket() {
	r.gcs.AddBucket(initStateBucket, initProjectNumber, map[string]string{"fugaro": "tfstate"})
}

const initLogView = "projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs"

func outputsJSON(t *testing.T) string {
	t.Helper()
	return outputsJSONWith(t, nil)
}

// outputsJSONWith is outputsJSON with some outputs' values replaced.
func outputsJSONWith(t *testing.T, over map[string]any) string {
	t.Helper()
	vals := map[string]any{
		"project_name":              initProjectName,
		"runs_bucket":               initRunsBucket,
		"registry_host":             "us-east5-docker.pkg.dev/proj-1234",
		"base_registry":             infra.BaseRegistry,
		"legacy_registry":           nil,
		"scheduler_service_account": "fugaro-scheduler@proj-1234.iam.gserviceaccount.com",
		"role_ids": map[string]string{
			"launcher": "projects/proj-1234/roles/fugaroLauncher", "job_runner": "projects/proj-1234/roles/fugaroJobRunner",
			"build_submitter": "projects/proj-1234/roles/fugaroBuildSubmitter",
			"tag_mover":       "projects/proj-1234/roles/fugaroTagMover",
		},
		"launchers":                []string{},
		"operators":                []string{},
		"log_view":                 initLogView,
		"registry_cleanup_dry_run": true,
	}
	maps.Copy(vals, over)
	out := map[string]any{}
	for k, v := range vals {
		out[k] = map[string]any{"value": v, "type": "string", "sensitive": false}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// calls are the fake terraform's subcommands, in order ("state rm" as one).
func (r *initRig) calls(t *testing.T) [][]string {
	t.Helper()
	f, err := os.Open(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<24)
	for s.Scan() {
		var c struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c.Args)
	}
	return out
}

func (r *initRig) ran(t *testing.T, sub string) [][]string {
	t.Helper()
	var out [][]string
	for _, c := range r.calls(t) {
		name := c[0]
		if name == "state" && len(c) > 1 {
			name += " " + c[1]
		}
		if name == sub {
			out = append(out, c)
		}
	}
	return out
}

func (r *initRig) workdir() string {
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", initProject, "installation")
}

func (r *initRig) root() string { return filepath.Join(r.workdir(), "gcp", "roots", "installation") }

func (r *initRig) tfvars(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.root(), infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func policySets(g *gcpfake.GCS, bucket string) []gcpfake.Request {
	var out []gcpfake.Request
	for _, r := range g.Requests() {
		if r.Method == "PUT" && r.Path == "/storage/v1/b/"+bucket+"/iam" {
			out = append(out, r)
		}
	}
	return out
}

func hasViewer(bs []gcpfake.Binding) bool {
	for _, b := range bs {
		for _, m := range b.Members {
			if strings.HasPrefix(m, "projectViewer:") {
				return true
			}
		}
	}
	return false
}

// Without a terminal and without --yes nothing is applied.
func TestInitRefusesWithoutConfirmation(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	out, _, err := executeStdin(t, "", "init")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 1 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("calls = %q", r.calls(t))
	}
	if !strings.Contains(out, "⚠ CONFIRM (project aurora, GCP project proj-1234): applies 0 imports, 1 creates, 0 updates") {
		t.Errorf("no confirmation banner:\n%s", out)
	}
}

// The apply takes the plan file that was shown and guarded, and nothing
// else; the fake's plan exits 2, which means changes, not failure.
func TestInitAppliesSavedPlan(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
		t.Fatal(err)
	}
	var subs []string
	for _, c := range r.calls(t) {
		subs = append(subs, c[0])
	}
	if want := []string{"version", "init", "output", "plan", "show", "apply", "output"}; !slices.Equal(subs, want) {
		t.Fatalf("terraform calls = %q, want %q", subs, want)
	}
	plan := r.ran(t, "plan")[0]
	show := r.ran(t, "show")[0]
	apply := r.ran(t, "apply")[0]
	if !slices.Contains(plan, "-out="+infra.PlanFile) || show[len(show)-1] != infra.PlanFile || apply[len(apply)-1] != infra.PlanFile {
		t.Fatalf("plan %q, show %q, apply %q", plan, show, apply)
	}
	init := r.ran(t, "init")[0]
	if !slices.Contains(init, "-backend-config=bucket="+initStateBucket) || !slices.Contains(init, "-backend-config=prefix="+infra.StatePrefixInstallation) {
		t.Fatalf("init = %q", init)
	}
	for _, f := range []string{infra.VarsFile, infra.BackendFile, infra.ImportsFile, "main.tf"} {
		if _, err := os.Stat(filepath.Join(r.root(), f)); err != nil {
			t.Errorf("the workdir lacks %s: %v", f, err)
		}
	}
	if st, err := os.Stat(r.workdir()); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("workdir: %v, %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(r.workdir(), "terraformrc")); err != nil {
		t.Errorf("no terraformrc: %v", err)
	}
	if v := r.tfvars(t); v["project"] != initProject || v["runs_bucket"] != initRunsBucket || v["state_bucket"] != initStateBucket {
		t.Errorf("tfvars = %v", v)
	}
}

// A plan that replaces the runs bucket is refused by the guard, even with
// --yes: nothing is applied.
func TestInitRefusesDeletePlan(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, change("module.installation.google_storage_bucket.runs", "delete", "create"))
	_, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "module.installation.google_storage_bucket.runs") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("a plan the guard refused was applied")
	}
	// A budget exists only through its flags: the refusal says to pass
	// them again.
	r.setPlan(t, change("module.installation.google_billing_budget.this[0]", "delete"))
	_, _, err = executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--budget, --budget-currency and --billing-account") {
		t.Fatalf("budget delete: exit %d, err %v", ExitCode(err), err)
	}
	r.setPlan(t, change("module.installation.google_storage_bucket.runs", "delete", "create"))
	// Naming the address lets it through.
	if _, _, err := executeStdin(t, "", "init", "--yes", "--allow-delete", "module.installation.google_storage_bucket.runs"); err != nil {
		t.Fatal(err)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Fatal("the allowed delete was not applied")
	}
}

// The state bucket is created only after its own confirmation, and project
// Viewers can't read it.
func TestInitCreatesStateBucketAfterConfirm(t *testing.T) {
	r := newInitRig(t)
	out, _, err := executeStdin(t, "", "init")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if !strings.Contains(out, "⚠ CONFIRM (project aurora, GCP project proj-1234): creates gs://"+initStateBucket+" in us-east5 with versioning, for Terraform state (cents a month)") {
		t.Errorf("no state bucket banner:\n%s", out)
	}
	if r.gcs.Inserted(initStateBucket) != nil || len(r.ran(t, "init")) != 0 {
		t.Fatal("the state bucket was created, or terraform ran, without a confirmation")
	}
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
		t.Fatal(err)
	}
	if r.gcs.Inserted(initStateBucket) == nil {
		t.Fatal("no state bucket was created")
	}
	if p := r.gcs.BucketPolicy(initStateBucket); hasViewer(p) || len(p) == 0 {
		t.Fatalf("the state bucket's policy = %+v", p)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Fatal("nothing was applied")
	}
}

// Only the two projectViewer grants go from the runs bucket, in one set
// that carries the etag of the policy read.
func TestInitRemovesRunsBucketViewers(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{"fugaro": "managed"})
	job := gcpfake.Binding{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:a@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Title: "t", Expression: "e"}}
	conv := gcpfake.ConvenienceBindings(initProject)
	r.gcs.SetBucketPolicy(initRunsBucket, append(slices.Clone(conv), job))
	etag := r.gcs.BucketPolicyEtag(initRunsBucket)
	out, _, err := executeStdin(t, "", "init", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "⚠ CONFIRM (project aurora, GCP project proj-1234): removes project Viewers' read access to gs://"+initRunsBucket+", which holds transcripts and caches") {
		t.Errorf("no viewers banner:\n%s", out)
	}
	sets := policySets(r.gcs, initRunsBucket)
	if len(sets) != 1 {
		t.Fatalf("%d policy sets, want 1", len(sets))
	}
	var body struct {
		Etag string `json:"etag"`
	}
	if err := json.Unmarshal(sets[0].Body, &body); err != nil || body.Etag != etag {
		t.Fatalf("the set's etag = %q, want %q (%v)", body.Etag, etag, err)
	}
	want := []gcpfake.Binding{conv[0], conv[2], job}
	if got := r.gcs.BucketPolicy(initRunsBucket); !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %+v\nwant %+v", got, want)
	}
	// The bucket is adopted, not created.
	b, _ := os.ReadFile(filepath.Join(r.root(), infra.ImportsFile))
	if !strings.Contains(string(b), "module.installation.google_storage_bucket.runs") {
		t.Errorf("imports = %s", b)
	}
	// A second run finds nothing to remove and asks nothing.
	if out, _, err := executeStdin(t, "", "init", "--yes"); err != nil || strings.Contains(out, "project Viewers") {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if len(policySets(r.gcs, initRunsBucket)) != 1 {
		t.Fatal("a second run set the policy again")
	}
}

// Declining the viewers' removal is allowed; the run goes on and says so.
func TestInitRunsBucketViewersDeclined(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{"fugaro": "managed"})
	r.gcs.SetBucketPolicy(initRunsBucket, gcpfake.ConvenienceBindings(initProject))
	fakeTerminal(t)
	out, _, err := executeStdin(t, "no\n"+initProjectName+"\n", "init")
	if err != nil {
		t.Fatal(err)
	}
	if len(policySets(r.gcs, initRunsBucket)) != 0 || !hasViewer(r.gcs.BucketPolicy(initRunsBucket)) {
		t.Fatal("the viewers were removed although the user declined")
	}
	if !strings.Contains(out, "project Viewers can still read gs://"+initRunsBucket) {
		t.Errorf("the summary doesn't note the declined removal:\n%s", out)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Fatal("the typed project name did not confirm the apply")
	}
}

// fakeTerminal makes init treat stdin as a terminal, where the project name
// is typed to confirm.
func fakeTerminal(t *testing.T) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// At a terminal, anything but the project name declines.
func TestInitTypedConfirmation(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	_, _, err := executeStdin(t, "yes\n", "init")
	if ExitCode(err) != ExitUserError || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("exit %d, err %v, calls %q", ExitCode(err), err, r.calls(t))
	}
	if _, _, err := executeStdin(t, initProjectName+"\n", "init"); err != nil || len(r.ran(t, "apply")) != 1 {
		t.Fatalf("typed project name: %v, calls %q", err, r.calls(t))
	}
}

func TestInitRefusesForeignStateBucket(t *testing.T) {
	for name, labels := range map[string]map[string]string{"other project": {"fugaro": "tfstate"}, "unmarked": nil} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			num := uint64(initProjectNumber)
			if labels != nil {
				num = 999
			}
			r.gcs.AddBucket(initStateBucket, num, labels)
			_, _, err := executeStdin(t, "", "init", "--yes")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), initStateBucket) {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if len(r.ran(t, "init")) != 0 {
				t.Fatal("terraform ran against a foreign state bucket")
			}
		})
	}
}

func TestInitRefusesOtherProjectEnv(t *testing.T) {
	for _, k := range []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"} {
		t.Run(k, func(t *testing.T) {
			r := newInitRig(t)
			r.stateBucket()
			t.Setenv(k, "other-project")
			_, _, err := executeStdin(t, "", "init", "--yes")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), k) {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if len(r.calls(t)) != 0 || len(r.gcs.Requests())+len(r.crm.Requests()) != 0 {
				t.Fatal("init went on with another project in the environment")
			}
			// The same project is fine.
			t.Setenv(k, initProject)
			if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInitRefusesImpersonation(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	t.Setenv("GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "someone@proj-1234.iam.gserviceaccount.com")
	_, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.calls(t)) != 0 || len(r.gcs.Requests()) != 0 {
		t.Fatal("init went on while impersonating")
	}
}

// GODEBUG=http2debug would print the bearer token of every call.
func TestInitRefusesHTTP2Debug(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	t.Setenv("GODEBUG", "http2debug=1")
	_, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.calls(t)) != 0 || len(r.gcs.Requests()) != 0 {
		t.Fatal("init went on with http2debug set")
	}
}

func TestInitRefusesUnsupportedTerraform(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["version"] = map[string]any{"stdout": `{"terraform_version":"1.6.6"}`}
	r.save(t)
	_, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "1.6.6") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.gcs.Requests()) != 0 {
		t.Fatal("init called the cloud with an unsupported terraform")
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := executeStdin(t, "", "init", "--yes"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "terraform") {
		t.Fatalf("no terraform: exit %d, err %v", ExitCode(err), err)
	}
}

func TestInitWritesLocalConfigAndBackup(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-0123abc"
	out, _, err := executeStdin(t, "", "init", "--yes", "--base-image", base, "--launcher", "user:launcher@example.com", "--alert-email", "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lc.RunsBucket != initRunsBucket || lc.RegistryHost != "us-east5-docker.pkg.dev/proj-1234" || lc.LogView != initLogView ||
		lc.SchedulerRegion != "us-east4" || lc.Terraform.StateBucket != initStateBucket || lc.BaseImage != base ||
		!slices.Equal(lc.Terraform.Launchers, []string{"user:launcher@example.com"}) || lc.Terraform.AlertEmail != "ops@example.com" {
		t.Fatalf("local config = %+v", lc)
	}
	if lc.Build.ServiceAccount != "" {
		t.Error("build.service_account was kept")
	}
	if lc.Registry != "us-east5-docker.pkg.dev/proj-1234/fugaro" || len(lc.Repos) != 1 || lc.Endpoints.Storage == "" {
		t.Errorf("the legacy registry, repos or endpoints were lost: %+v", lc)
	}
	backups, _ := filepath.Glob(r.cfg + ".bak-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %q", backups)
	}
	st, err := os.Stat(backups[0])
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("backup: %v, %v", st, err)
	}
	if b, _ := os.ReadFile(backups[0]); string(b) != string(before) {
		t.Error("the backup is not the old config")
	}
	if !strings.Contains(out, "+ log_view: "+initLogView) || !strings.Contains(out, "- build:") && !strings.Contains(out, "-   service_account:") {
		t.Errorf("the diff is not shown:\n%s", out)
	}
	// The deprecation warning was printed, and the launcher reached Terraform.
	if !strings.Contains(out, "build.service_account is deprecated") {
		t.Errorf("no deprecation warning:\n%s", out)
	}
	if v := r.tfvars(t); fmt.Sprint(v["launchers"]) != "[user:launcher@example.com]" || v["alert_email"] != "ops@example.com" {
		t.Errorf("tfvars = %v", v)
	}
	// Nothing changes on a second run: no second backup.
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base-image", base); err != nil {
		t.Fatal(err)
	}
	if backups, _ := filepath.Glob(r.cfg + ".bak-*"); len(backups) != 1 {
		t.Fatalf("an unchanged config was backed up again: %q", backups)
	}
}

func TestInitPlanOnly(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	before, _ := os.ReadFile(r.cfg)
	out, _, err := executeStdin(t, "", "init", "--plan-only")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ran(t, "show")) != 1 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("calls = %q", r.calls(t))
	}
	if !strings.Contains(out, "create module.installation.google_storage_bucket.runs") {
		t.Errorf("no summary:\n%s", out)
	}
	if after, _ := os.ReadFile(r.cfg); string(after) != string(before) {
		t.Error("--plan-only changed the local config")
	}

	// It changes no IAM either, even with --yes: the viewers' removal is
	// only listed.
	r = newInitRig(t)
	r.stateBucket()
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{"fugaro": "managed"})
	r.gcs.SetBucketPolicy(initRunsBucket, gcpfake.ConvenienceBindings(initProject))
	out, _, err = executeStdin(t, "", "init", "--plan-only", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if len(policySets(r.gcs, initRunsBucket)) != 0 || !strings.Contains(out, "project Viewers' read access to gs://"+initRunsBucket) {
		t.Fatalf("--plan-only and the runs bucket's viewers:\n%s", out)
	}
}

// A state bucket whose viewers' removal failed after it was created gets
// the removal again, after a confirmation.
func TestInitRetriesStateBucketViewers(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.gcs.SetBucketPolicy(initStateBucket, gcpfake.ConvenienceBindings(initProject))
	out, _, err := executeStdin(t, "", "init")
	if ExitCode(err) != ExitUserError || len(policySets(r.gcs, initStateBucket)) != 0 || len(r.ran(t, "init")) != 0 {
		t.Fatalf("unconfirmed: exit %d, err %v, calls %q", ExitCode(err), err, r.calls(t))
	}
	if !strings.Contains(out, "⚠ CONFIRM (project aurora, GCP project proj-1234): removes project Viewers' read access to gs://"+initStateBucket) {
		t.Errorf("no banner:\n%s", out)
	}
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
		t.Fatal(err)
	}
	if hasViewer(r.gcs.BucketPolicy(initStateBucket)) || len(r.gcs.BucketPolicy(initStateBucket)) == 0 {
		t.Fatalf("state bucket policy = %+v", r.gcs.BucketPolicy(initStateBucket))
	}
}

// A symlinked local config is written through the link, which stays.
func TestInitWritesThroughSymlink(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	target := filepath.Join(r.dir, "dotfiles", "fugaro.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(r.cfg, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, r.cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(r.cfg); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", st, err)
	}
	if lc, err := localcfg.Load(target); err != nil || lc.LogView != initLogView {
		t.Fatalf("the link's target = %+v, %v", lc, err)
	}
}

func TestInitPrintVarsNoCalls(t *testing.T) {
	r := newInitRig(t)
	out, stderr, err := executeStdin(t, "", "init", "--print-vars", "--no-log-isolation")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if v["project"] != initProject || v["runs_bucket"] != initRunsBucket || v["log_isolation"] != false {
		t.Fatalf("vars = %v", v)
	}
	// Discovery didn't run, so adopt_legacy_registry is only a default: the
	// warning, on stderr, says so, and stdout stays the tfvars alone.
	for _, want := range []string{"warning:", "ungated", "adopt_legacy_registry", "fugaro init"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	for _, s := range []*gcpfake.Server{r.gcs.Server, r.crm.Server, r.ar.Server, r.iam.Server, r.run.Server, r.sm.Server} {
		if n := len(s.Requests()); n != 0 {
			t.Errorf("--print-vars made %d cloud request(s)", n)
		}
	}
	if len(r.calls(t)) != 0 {
		t.Errorf("--print-vars ran terraform: %q", r.calls(t))
	}
}

func TestInitConfigOnly(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	if _, _, err := executeStdin(t, "", "init", "--config-only", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("--config-only planned: %q", r.calls(t))
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil || lc.LogView != initLogView || lc.Terraform.StateBucket != initStateBucket {
		t.Fatalf("local config = %+v, %v", lc, err)
	}

	// Without a reachable state, the flags write it; nothing is created.
	r = newInitRig(t)
	out, _, err := executeStdin(t, "", "init", "--config-only", "--yes", "--scheduler-region", "us-central1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "written from the flags") || r.gcs.Inserted(initStateBucket) != nil || len(r.ran(t, "init")) != 0 {
		t.Fatalf("calls %q, output:\n%s", r.calls(t), out)
	}
	if lc, err := localcfg.Load(r.cfg); err != nil || lc.RegistryHost != "us-east5-docker.pkg.dev/proj-1234" || lc.SchedulerRegion != "us-central1" || lc.LogView != "" {
		t.Fatalf("local config = %+v, %v", lc, err)
	}
	// The registry host is the local config's when it has one.
	r = newInitRig(t)
	r.appendConfig(t, "registry_host: us-central1-docker.pkg.dev/proj-1234\n")
	if _, _, err := executeStdin(t, "", "init", "--config-only", "--yes"); err != nil {
		t.Fatal(err)
	}
	if lc, err := localcfg.Load(r.cfg); err != nil || lc.RegistryHost != "us-central1-docker.pkg.dev/proj-1234" {
		t.Fatalf("local config = %+v, %v", lc, err)
	}
	// A state that exists but can't be read is a failure, not a fallback.
	r = newInitRig(t)
	r.stateBucket()
	r.script["output"] = map[string]any{"exit": 1, "stderr": "Error: 403 Forbidden"}
	r.save(t)
	before, _ := os.ReadFile(r.cfg)
	if _, _, err := executeStdin(t, "", "init", "--config-only", "--yes"); ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "403") {
		t.Fatalf("an unreadable state: exit %d, err %v", ExitCode(err), err)
	}
	if after, _ := os.ReadFile(r.cfg); string(after) != string(before) {
		t.Error("the local config was written from the flags although the state exists")
	}
	// A foreign state bucket is refused.
	r = newInitRig(t)
	r.gcs.AddBucket(initStateBucket, 999, map[string]string{"fugaro": "tfstate"})
	if _, _, err := executeStdin(t, "", "init", "--config-only", "--yes"); ExitCode(err) != ExitUserError {
		t.Fatalf("a foreign state bucket: exit %d, err %v", ExitCode(err), err)
	}
}

// forgetPlan is the rollback's first plan: the log isolation goes, cleanup
// is switched off, everything else stays.
func forgetPlan() []planChange {
	var out []planChange
	for _, a := range infra.ForgetDeletes {
		out = append(out, change(a, "delete"))
	}
	return append(out,
		change(`module.installation.google_logging_log_view_iam_member.runs["user:launcher@example.com"]`, "delete"),
		change("module.installation.google_artifact_registry_repository.base", "update"),
		change("module.installation.google_storage_bucket.runs", "no-op"))
}

func TestInitForgetUndoesExclusionThenStateRm(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, forgetPlan()...)
	out, _, err := executeStdin(t, "", "init", "--forget", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	var subs []string
	for _, c := range r.calls(t) {
		subs = append(subs, strings.Join(c[:min(2, len(c))], " "))
	}
	if i, j := slices.IndexFunc(subs, func(s string) bool { return strings.HasPrefix(s, "apply") }),
		slices.IndexFunc(subs, func(s string) bool { return s == "state rm" }); i < 0 || j < i {
		t.Fatalf("terraform calls = %q: want an apply, then state rm", subs)
	}
	rm := r.ran(t, "state rm")[0]
	if rm[len(rm)-1] != "module.installation" {
		t.Fatalf("state rm = %q", rm)
	}
	v := r.tfvars(t)
	cleanup, _ := v["registry_cleanup"].(map[string]any)
	if v["log_isolation"] != false || cleanup["enabled"] != false {
		t.Fatalf("the rollback's tfvars = %v", v)
	}
	if b, _ := os.ReadFile(filepath.Join(r.root(), infra.ImportsFile)); strings.TrimSpace(string(b)) != "{}" {
		t.Errorf("the rollback imports: %s", b)
	}
	if strings.Count(out, "⚠ CONFIRM (project aurora, GCP project proj-1234)") != 2 {
		t.Errorf("want two confirmations:\n%s", out)
	}

	// Any other delete is refused, and nothing is applied or removed.
	r = newInitRig(t)
	r.stateBucket()
	r.setPlan(t, append(forgetPlan(), change("module.installation.google_service_account.scheduler", "delete"))...)
	_, _, err = executeStdin(t, "", "init", "--forget", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "google_service_account.scheduler") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply"))+len(r.ran(t, "state rm")) != 0 {
		t.Fatalf("calls = %q", r.calls(t))
	}
	// So is a plan that creates anything.
	r = newInitRig(t)
	r.stateBucket()
	r.setPlan(t, change("module.installation.google_storage_bucket.runs", "create"))
	if _, _, err := executeStdin(t, "", "init", "--forget", "--yes"); ExitCode(err) != ExitUserError || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("a create: exit %d, err %v", ExitCode(err), err)
	}
}

// An installation that adopted the legacy registry keeps it declared in
// the rollback's plan, which would otherwise delete it.
func TestInitForgetKeepsAdoptedLegacyRegistry(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, forgetPlan()...)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"legacy_registry": infra.LegacyRegistry})}
	r.save(t)
	if _, _, err := executeStdin(t, "", "init", "--forget", "--yes"); err != nil {
		t.Fatal(err)
	}
	if v := r.tfvars(t); v["adopt_legacy_registry"] != true {
		t.Fatalf("the rollback's tfvars = %v", v)
	}
	// With no outputs, the state holds no installation: refused.
	r = newInitRig(t)
	r.stateBucket()
	r.setPlan(t, forgetPlan()...)
	r.script["output"] = map[string]any{"stdout": "{}"}
	r.save(t)
	if _, _, err := executeStdin(t, "", "init", "--forget", "--yes"); ExitCode(err) != ExitUserError || len(r.ran(t, "plan")) != 0 {
		t.Fatalf("an empty state: exit %d, err %v, calls %q", ExitCode(err), err, r.calls(t))
	}
}

func TestInitForgetRefusesWithRepoStates(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	b := r.gcs.Bucket(t, initStateBucket)
	if err := b.WriteAll(t.Context(), infra.StatePrefixRepos+"bitbucket-acme-sandbox/default.tfstate", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeStdin(t, "", "init", "--forget", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "init --repo --forget") || !strings.Contains(err.Error(), "*.tfstate") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan"))+len(r.ran(t, "apply"))+len(r.ran(t, "state rm")) != 0 {
		t.Fatalf("calls = %q", r.calls(t))
	}
}

// A repository the local config records as using Vertex AI enables the
// Vertex AI API in the installation.
func TestInitEnablesVertexForVertexRepo(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	cfg, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v := r.runInitTfvars(t); v["enable_vertex"] != false {
		t.Fatalf("no vertex repository: enable_vertex = %v", v["enable_vertex"])
	}
	vertex := strings.Replace(string(cfg), "workflows: [web] }", "vertex: true, workflows: [web] }", 1)
	if err := os.WriteFile(r.cfg, []byte(vertex), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := r.runInitTfvars(t); v["enable_vertex"] != true {
		t.Fatalf("a vertex repository: enable_vertex = %v", v["enable_vertex"])
	}
}

func (r *initRig) runInitTfvars(t *testing.T) map[string]any {
	t.Helper()
	if _, _, err := executeStdin(t, "", "init", "--plan-only"); err != nil {
		t.Fatal(err)
	}
	return r.tfvars(t)
}

// A stale lock under the repositories' prefix is not state: init --repo
// --forget deletes only *.tfstate objects, so the installation's rollback
// counts only those too, and a leftover lock doesn't block it.
func TestInitForgetIgnoresStaleRepoLock(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, forgetPlan()...)
	b := r.gcs.Bucket(t, initStateBucket)
	if err := b.WriteAll(t.Context(), infra.StatePrefixRepos+"bitbucket-acme-sandbox/default.tflock", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeStdin(t, "", "init", "--forget", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(r.ran(t, "state rm")) != 1 {
		t.Fatalf("calls = %q", r.calls(t))
	}
}

func TestInitForgetWarnsLogBucketUndelete(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, forgetPlan()...)
	out, _, err := executeStdin(t, "", "init", "--forget", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pending deletion for 7 days") || !strings.Contains(out, infra.LogBucketUndelete(initProject)) {
		t.Fatalf("no undelete warning:\n%s", out)
	}
}

// --json prints one result document on stdout.
func TestInitJSON(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	out, _, err := executeStdin(t, "", "init", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Project    string           `json:"project"`
		GCPProject string           `json:"gcp_project"`
		Applied    bool             `json:"applied"`
		Changes    infra.PlanCounts `json:"changes"`
		Outputs    map[string]any   `json:"outputs"`
		Config     string           `json:"config"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.Project != initProjectName || res.GCPProject != initProject || !res.Applied || res.Changes.Creates != 1 || res.Outputs["log_view"] != initLogView || res.Config != r.cfg {
		t.Fatalf("result = %+v", res)
	}
}

// createOnApply makes the fake terraform's apply create the runs bucket
// in the GCS fake, as the real apply does on a fresh project: with GCS's
// convenience bindings, the project Viewers' included.
func (r *initRig) createOnApply(t *testing.T) {
	t.Helper()
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("needs curl to play the apply's bucket create")
	}
	path := filepath.Join(r.dir, "bin", "terraform")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + initRunsBucket + `","labels":{"fugaro":"managed"}}`
	create := `if [ "$1" = apply ]; then '` + curl + `' -sf -o /dev/null -X POST -H 'Content-Type: application/json' -d '` + body + `' '` +
		r.gcs.URL + `/storage/v1/b?project=` + initProject + `' || exit 9; fi` + "\n"
	lines := strings.SplitN(string(old), "\n", 2)
	if err := os.WriteFile(path, []byte(lines[0]+"\n"+create+lines[1]), 0o755); err != nil {
		t.Fatal(err)
	}
}

// On a fresh project the apply creates the runs bucket, with project
// Viewers' read access; init offers to remove it right after the apply.
func TestInitRemovesViewersOfCreatedRunsBucket(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.createOnApply(t)
	out, _, err := executeStdin(t, "", "init", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Fatal("no apply")
	}
	if !strings.Contains(out, "⚠ CONFIRM (project aurora, GCP project proj-1234): removes project Viewers' read access to gs://"+initRunsBucket+", which holds transcripts and caches") {
		t.Errorf("no viewers banner after the apply:\n%s", out)
	}
	if hasViewer(r.gcs.BucketPolicy(initRunsBucket)) {
		t.Errorf("project Viewers can still read the new runs bucket: %+v", r.gcs.BucketPolicy(initRunsBucket))
	}
	if len(policySets(r.gcs, initRunsBucket)) != 1 {
		t.Errorf("policy sets = %d, want 1", len(policySets(r.gcs, initRunsBucket)))
	}
}

// Without a terminal and without --yes nothing is applied, so there is no
// new bucket to offer; with --plan-only, likewise.
func TestInitPlanOnlyLeavesCreatedBucketAlone(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.createOnApply(t)
	out, _, err := executeStdin(t, "", "init", "--plan-only", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "gs://"+initRunsBucket+", which holds") || len(policySets(r.gcs, initRunsBucket)) != 0 {
		t.Errorf("--plan-only offered or changed the runs bucket's IAM:\n%s", out)
	}
}

// The project's name never changes once the installation has one: the
// state's project_name output wins over a project config and --name that
// say otherwise, and the refusal comes before any plan.
func TestInitNameImmutable(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	// A second project config for the same GCP project: same file, another name.
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(string(data), "name: aurora", "name: borealis", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--yes"}, {"init", "--yes", "--name", "borealis"}} {
		_, _, err := executeStdin(t, "", args...)
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "the installation's project name is aurora; renaming isn't supported") {
			t.Fatalf("%v: exit %d, err %v", args, ExitCode(err), err)
		}
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("a refused rename planned: %q", r.calls(t))
	}
	// --name against the config that selected aurora is refused as well.
	r = newInitRig(t)
	r.stateBucket()
	_, _, err = executeStdin(t, "", "init", "--yes", "--name", "borealis")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "renaming") {
		t.Fatalf("--name against the config: exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 0 {
		t.Fatalf("planned: %q", r.calls(t))
	}
}

// An installation applied before M9a has no name in its state. Naming it
// is permanent, so init asks for --name rather than taking the config's.
func TestInitNameRequiredWhenUnnamed(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": nil})}
	r.save(t)
	_, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "no project name yet") || !strings.Contains(err.Error(), "--name aurora") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("an unnamed installation was planned: %q", r.calls(t))
	}
	// With --name the apply goes ahead, and it carries the name.
	if _, _, err := executeStdin(t, "", "init", "--yes", "--name", "aurora"); err != nil {
		t.Fatal(err)
	}
	if got := r.tfvars(t)["fugaro_project"]; got != "aurora" {
		t.Errorf("fugaro_project = %v", got)
	}
	if len(r.ran(t, "apply")) == 0 {
		t.Errorf("the named apply didn't run: %q", r.calls(t))
	}
}

func TestCheckInstallationName(t *testing.T) {
	named := infra.InstallationOutputs{ProjectName: "aurora"}
	unnamed := infra.InstallationOutputs{}
	for name, c := range map[string]struct {
		flag, config string
		outs         infra.InstallationOutputs
		have         bool
		want         string // "" is accepted
	}{
		// With no installation in the state yet there is nothing to
		// disagree with: the project config's name is the installation's.
		"no installation yet":     {config: "aurora"},
		"no installation, --name": {flag: "aurora", config: "aurora"},
		"named, agreeing":         {config: "aurora", outs: named, have: true},
		"named, --name agrees":    {flag: "aurora", config: "aurora", outs: named, have: true},
		"named, config differs":   {config: "borealis", outs: named, have: true, want: "the installation's project name is aurora; renaming isn't supported"},
		"named, --name differs":   {flag: "borealis", config: "aurora", outs: named, have: true, want: "renaming isn't supported"},
		"unnamed, no --name":      {config: "aurora", outs: unnamed, have: true, want: "--name aurora"},
		"unnamed, --name":         {flag: "aurora", config: "aurora", outs: unnamed, have: true},
	} {
		err := checkInstallationName(c.flag, c.config, c.outs, c.have)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		case err != nil && ExitCode(err) != ExitUserError:
			t.Errorf("%s: exit %d", name, ExitCode(err))
		}
	}
}

func TestInitConfigOnlyWithoutProjectName(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": nil})}
	r.save(t)
	before, _ := os.ReadFile(r.cfg)
	_, _, err := executeStdin(t, "", "init", "--config-only", "--yes")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "the installation has no project name yet") || !strings.Contains(err.Error(), "fugaro init --name") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if after, _ := os.ReadFile(r.cfg); string(after) != string(before) {
		t.Error("the config was written for an unnamed installation")
	}
}

func TestInitConfigOnlyRefusesOtherName(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": "borealis"})}
	r.save(t)
	_, _, err := executeStdin(t, "", "init", "--config-only", "--yes")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "the installation's project name is borealis") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// --config-only with no project config yet (only --gcp-project and
// --region) learns the name from the installation's outputs and writes
// projects/<project_name>.yaml. It is exercised below the command because
// a config that doesn't exist yet has no fake endpoints to reach.
func TestInitConfigOnlyTakesNameFromOutputs(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("FUGARO_CONFIG", "")
	t.Setenv("FUGARO_PROJECT", "")
	provisional := func() *localcfg.Config {
		lc, err := localcfg.Parse([]byte("version: 1\nname: pending\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n"))
		if err != nil {
			t.Fatal(err)
		}
		lc.Name = ""
		return lc
	}
	lc, path, old, err := nameFromOutputs(provisional(), infra.InstallationOutputs{ProjectName: "aurora"})
	if err != nil || lc.Name != "aurora" || old != nil || filepath.Base(path) != "aurora.yaml" || filepath.Base(filepath.Dir(path)) != "projects" {
		t.Fatalf("lc %+v, path %q, old %q, err %v", lc, path, old, err)
	}
	// No name in the outputs: nothing to call the file.
	if _, _, _, err := nameFromOutputs(provisional(), infra.InstallationOutputs{}); err == nil ||
		!strings.Contains(err.Error(), "the installation has no project name yet; an operator runs fugaro init --name <name>") {
		t.Fatalf("no project_name: %v", err)
	}
	if _, _, _, err := nameFromOutputs(provisional(), infra.InstallationOutputs{ProjectName: "Not A Name"}); err == nil {
		t.Fatal("a bad name was accepted")
	}
	// A file for that name that belongs to another GCP project is refused;
	// one for the same project is kept and extended.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	other := "version: 1\nname: aurora\ngcp_project: other-proj\nregion: us-east5\nruns_bucket: fugaro-runs-other-proj\n"
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := nameFromOutputs(provisional(), infra.InstallationOutputs{ProjectName: "aurora"}); err == nil ||
		!strings.Contains(err.Error(), "other-proj") || !strings.Contains(err.Error(), "proj-1234") {
		t.Fatalf("a file for another GCP project: %v", err)
	}
	same := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\nuser: me@example.com\n"
	if err := os.WriteFile(path, []byte(same), 0o600); err != nil {
		t.Fatal(err)
	}
	lc, _, old, err = nameFromOutputs(provisional(), infra.InstallationOutputs{ProjectName: "aurora"})
	if err != nil || lc.User != "me@example.com" || string(old) != same {
		t.Fatalf("an existing file for the same project: %+v, %q, %v", lc, old, err)
	}
}

// What init writes can't drift from the installation's own name.
func TestInitWriteConfigChecksName(t *testing.T) {
	r := newInitRig(t)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := infra.Installation(lc, infra.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cmd := newInitCmd()
	cmd.SetOut(io.Discard)
	ir := newInitRun(cmd, &initOptions{})
	err = ir.writeConfig(lc, spec, infra.InstallationOutputs{ProjectName: "borealis", RunsBucket: initRunsBucket}, r.cfg, nil, true)
	if err == nil || !strings.Contains(err.Error(), "borealis") {
		t.Fatalf("err = %v", err)
	}
}

// A Vertex workflow can't be enforced yet: init --repo refuses before any
// plan or cloud call, while observe and off pass.
func TestInitRepoRefusesVertexEnforce(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	cfgFile := func(auth string) *config.Config {
		t.Helper()
		c, err := config.Parse([]byte("version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: " + auth + " }\nworkflows:\n  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	withBudget := func(block string) *localcfg.Config {
		t.Helper()
		lc, err := localcfg.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := lc.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		lc, err = localcfg.Parse(append(data, block...))
		if err != nil {
			t.Fatal(err)
		}
		return lc
	}
	vertex, apiKey := cfgFile("vertex"), cfgFile("api-key")
	enforce := withBudget("budget: { mode: enforce, per_run_usd: 5 }\n")
	err := checkVertexBudget(enforce, vertex)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "Vertex budgets are not supported yet: use budget.mode observe or off") {
		t.Fatalf("vertex + enforce: exit %d, err %v", ExitCode(err), err)
	}
	for name, tc := range map[string]struct {
		lc  *localcfg.Config
		cfg *config.Config
	}{
		"vertex + observe":  {withBudget("budget: { mode: observe }\n"), vertex},
		"vertex + off":      {withBudget("budget: { mode: off }\n"), vertex},
		"vertex + none":     {withBudget(""), vertex},
		"api-key + enforce": {enforce, apiKey},
	} {
		if err := checkVertexBudget(tc.lc, tc.cfg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Through the command, before any cloud call (there is no rig here, so
	// a call would fail differently).
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "budget: { mode: enforce, per_run_usd: 5 }\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: vertex }\nworkflows:\n  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	_, _, err = execute(t, "init", "--repo", "--project", "aurora", root)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "Vertex budgets are not supported yet") {
		t.Fatalf("init --repo: exit %d, err %v", ExitCode(err), err)
	}
}

// Outputs that exist but can't be decoded are an error, not "no
// installation": the immutable-name check must not be skipped by them.
func TestInitRefusesUnreadableOutputs(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": 5})}
	r.save(t)
	_, _, err := executeStdin(t, "", "init", "--yes")
	if err == nil || !strings.Contains(err.Error(), "project_name") {
		t.Fatalf("err = %v, want the unreadable output named", err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("unreadable outputs were planned over: %q", r.calls(t))
	}
}

// A refused rename changes no IAM: the runs bucket's viewers are only
// touched once the name is known to be the installation's.
func TestInitNameCheckBeforeViewersRemoval(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{"fugaro": "managed"})
	r.gcs.SetBucketPolicy(initRunsBucket, gcpfake.ConvenienceBindings(initProject))
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(string(data), "name: aurora", "name: borealis", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeStdin(t, "", "init", "--yes"); err == nil || !strings.Contains(err.Error(), "renaming isn't supported") {
		t.Fatalf("err = %v", err)
	}
	if n := len(policySets(r.gcs, initRunsBucket)); n != 0 {
		t.Fatalf("a refused rename changed the runs bucket's IAM %d times", n)
	}
}

// A flag's value can't become config syntax: the first config is built as
// a value and validated like a file.
func TestNewProjectConfigIsNotYAMLConcatenation(t *testing.T) {
	lc, err := newProjectConfig("aurora", "proj-1234", "us-east5")
	if err != nil || lc.Name != "aurora" || lc.GCPProject != "proj-1234" || lc.Region != "us-east5" || lc.RunsBucket != "fugaro-runs-proj-1234" {
		t.Fatalf("lc = %+v, err = %v", lc, err)
	}
	for _, bad := range []string{"proj-1234\nrepos: {x: {}}", "proj: 1234 # x", "Proj"} {
		if lc, err := newProjectConfig("aurora", bad, "us-east5"); err == nil {
			t.Errorf("gcp project %q accepted: %+v", bad, lc)
		}
	}
	if _, err := newProjectConfig("aurora", "proj-1234", "us-east5\nuser: x"); err == nil {
		t.Error("a region with a newline was accepted")
	}
}

// init --repo checks the repository's project against the installation's
// own name, from its outputs, before it plans anything.
func TestInitRepoRefusesAnInstallationOfAnotherProject(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	w, err := r.gcs.Bucket(t, initStateBucket).NewWriter(context.Background(), infra.StatePrefixInstallation+"/default.tfstate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r.appendConfig(t, "base_image: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc\n")
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": "borealis"})}
	r.save(t)
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	_, _, err = executeStdin(t, "", "init", "--repo", "--yes", "--project", "aurora", "--github-app-id", "12345", root)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "fugaro.yaml names project aurora, but this installation is project borealis") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("a refused repository was planned: %q", r.calls(t))
	}
}

// The ceiling's new keys reach the job env that init --repo computes.
func TestInitRepoPassesPolicyEnv(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "budget: { max_run_tokens: 123456, allowed_models: [claude-sonnet-5-5] }\nbase_image: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\nworkflows:\n  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "init", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FUGARO_MAX_RUN_TOKENS", "123456", "FUGARO_ALLOWED_MODELS", "claude-sonnet-5-5"} {
		if !strings.Contains(out, want) {
			t.Errorf("print-vars output lacks %q:\n%s", want, out)
		}
	}
}
