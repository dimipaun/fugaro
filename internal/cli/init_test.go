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
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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

// labelled marks project as the project of a managed installation: the
// fugaro_managed label the run that created it writes on the project itself,
// beside the creation fact (Task 3.0). A world that puts the object in a
// bucket and means the read to believe it needs both halves — the read
// requires the label and answers "not managed" for a project without it, so
// a rig that forgot it would be testing the wrong refusal.
func labelled(f *gcpfake.CRM, project string) {
	f.SetLabels(project, map[string]string{managed.ProjectLabelKey: managed.ProjectLabelValue})
}

func newInitRig(t *testing.T) *initRig {
	t.Helper()
	// The converge runs the checkout stage, which runs fugaro upgrade's
	// plugin step: without this, a rig would install the plugin into the
	// developer's own Claude Code through whatever claude is on PATH.
	noClaudeOnPath(t)
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
	typ := strings.Split(strings.TrimPrefix(addr, "module.installation."), ".")[0]
	after := map[string]any{}
	if typ == "google_storage_bucket" { // as the module writes it
		after = map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}
	}
	return planChange{"address": addr, "type": typ,
		"change": map[string]any{"actions": actions, "before": map[string]any{}, "after": after}}
}

// setPlan makes the fake's show -json report changes, in every root.
func (r *initRig) setPlan(t *testing.T, changes ...planChange) {
	t.Helper()
	r.script["show"] = map[string]any{"stdout": planJSON(t, changes...)}
	r.save(t)
}

// setPlanAt makes ONE root's show -json report changes and leaves the other
// roots' plans empty: the fake tries "show@<root>" before the plain "show"
// (faketerraform's stepFor), so a test can aim a change at one stage. That
// matters wherever a test is about a particular stage's own handling of a
// plan: a change scripted for every root is seen by the FIRST stage that
// plans, which is rarely the stage under test.
func (r *initRig) setPlanAt(t *testing.T, root string, changes ...planChange) {
	t.Helper()
	r.script["show@"+root] = map[string]any{"stdout": planJSON(t, changes...)}
	r.save(t)
}

// planJSON is the `terraform show -json` answer for a set of changes.
func planJSON(t *testing.T, changes ...planChange) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"format_version": "1.2", "resource_changes": changes})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
	out, _, err := executeStdin(t, "", "setup")
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

// TestLegacyBaseKindRefusalsNameTheMigrationDoc: --base, --expect-digest
// and --base-image each refuse a kind the 0.7.0 cut removed (go,
// web-node, java-services) naming config.MigrationDoc, not just "is not
// a base kind": the one pointer to the fix, wherever the legacy kind was
// typed.
func TestLegacyBaseKindRefusalsNameTheMigrationDoc(t *testing.T) {
	newInitRig(t)
	for _, args := range [][]string{
		{"setup", "--yes", "--base", "go"},
		{"setup", "--yes", "--expect-digest", "go=sha256:" + strings.Repeat("a", 64)},
		{"setup", "--yes", "--base-image", "go=ghcr.io/me/fugaro-go:mine"}, // "go": removed in 0.7.0
	} {
		_, _, err := executeStdin(t, "", args...)
		if err == nil || !strings.Contains(err.Error(), config.MigrationDoc) {
			t.Errorf("%v: err = %v, want it to name %s", args, err, config.MigrationDoc)
		}
	}
}

// The apply takes the plan file that was shown and guarded, and nothing
// else; the fake's plan exits 2, which means changes, not failure.
func TestInitAppliesSavedPlan(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil {
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
	_, _, err := executeStdin(t, "", "setup", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "module.installation.google_storage_bucket.runs") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("a plan the guard refused was applied")
	}
	// A budget exists only through its flags: the refusal says to pass
	// them again.
	r.setPlan(t, change("module.installation.google_billing_budget.this[0]", "delete"))
	_, _, err = executeStdin(t, "", "setup", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--budget, --budget-currency and --billing-account") {
		t.Fatalf("budget delete: exit %d, err %v", ExitCode(err), err)
	}
	r.setPlan(t, change("module.installation.google_storage_bucket.runs", "delete", "create"))
	// Naming the address lets it through.
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--allow-delete", "module.installation.google_storage_bucket.runs"); err != nil {
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
	out, _, err := executeStdin(t, "", "setup")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if !strings.Contains(out, "⚠ CONFIRM (project aurora, GCP project proj-1234): creates gs://"+initStateBucket+" in us-east5 with versioning, for Terraform state (cents a month)") {
		t.Errorf("no state bucket banner:\n%s", out)
	}
	if r.gcs.Inserted(initStateBucket) != nil || len(r.ran(t, "init")) != 0 {
		t.Fatal("the state bucket was created, or terraform ran, without a confirmation")
	}
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil {
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
	out, _, err := executeStdin(t, "", "setup", "--yes")
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
	if out, _, err := executeStdin(t, "", "setup", "--yes"); err != nil || strings.Contains(out, "project Viewers") {
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
	out, _, err := executeStdin(t, "no\n"+initProjectName+"\n", "setup")
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

// noTerminal is the other half: a run with no terminal on stdin, whatever the
// test binary's own stdin is. A test that has to be sure of it says so rather
// than reading the default, which is green on a developer's terminal and red in
// CI (or the reverse).
func noTerminal(t *testing.T) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return false }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// typedThroughYes lets a test that is about something else answer a typed-only
// confirmation (the Firestore location, a billable build, replacing a tag) from
// its stdin while it passes --yes, which the real rule never allows (its own
// tests, TestTypedOnly..., use the real one). A coding agent's environment
// still confirms nothing.
func typedThroughYes(t *testing.T) {
	t.Helper()
	old := canConfirmTyped
	canConfirmTyped = func(_ initflow.ConfirmClass, c initflow.Conditions) bool { return c.Agent == "" }
	t.Cleanup(func() { canConfirmTyped = old })
}

// At a terminal, anything but the project name declines.
func TestInitTypedConfirmation(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	_, _, err := executeStdin(t, "yes\n", "setup")
	if ExitCode(err) != ExitUserError || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("exit %d, err %v, calls %q", ExitCode(err), err, r.calls(t))
	}
	if _, _, err := executeStdin(t, initProjectName+"\n", "setup"); err != nil || len(r.ran(t, "apply")) != 1 {
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
			_, _, err := executeStdin(t, "", "setup", "--yes")
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
			_, _, err := executeStdin(t, "", "setup", "--yes")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), k) {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if len(r.calls(t)) != 0 || len(r.gcs.Requests())+len(r.crm.Requests()) != 0 {
				t.Fatal("init went on with another project in the environment")
			}
			// The same project is fine.
			t.Setenv(k, initProject)
			if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInitRefusesImpersonation(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	t.Setenv("GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "someone@proj-1234.iam.gserviceaccount.com")
	_, _, err := executeStdin(t, "", "setup", "--yes")
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
	_, _, err := executeStdin(t, "", "setup", "--yes")
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
	_, _, err := executeStdin(t, "", "setup", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "1.6.6") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.gcs.Requests()) != 0 {
		t.Fatal("init called the cloud with an unsupported terraform")
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := executeStdin(t, "", "setup", "--yes"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "terraform") {
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
	base := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc"
	out, _, err := executeStdin(t, "", "setup", "--yes", "--base-image", base, "--launcher", "user:launcher@example.com", "--alert-email", "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lc.RunsBucket != initRunsBucket || lc.RegistryHost != "us-east5-docker.pkg.dev/proj-1234" || lc.LogView != initLogView ||
		lc.SchedulerRegion != "us-east4" || lc.Terraform.StateBucket != initStateBucket || lc.BaseImage("base") != base ||
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
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base-image", base); err != nil {
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
	out, _, err := executeStdin(t, "", "setup", "--plan-only")
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
	out, _, err = executeStdin(t, "", "setup", "--plan-only", "--yes")
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
	out, _, err := executeStdin(t, "", "setup")
	if ExitCode(err) != ExitUserError || len(policySets(r.gcs, initStateBucket)) != 0 || len(r.ran(t, "init")) != 0 {
		t.Fatalf("unconfirmed: exit %d, err %v, calls %q", ExitCode(err), err, r.calls(t))
	}
	if !strings.Contains(out, "⚠ CONFIRM (project aurora, GCP project proj-1234): removes project Viewers' read access to gs://"+initStateBucket) {
		t.Errorf("no banner:\n%s", out)
	}
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil {
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
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil {
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
	out, stderr, err := executeStdin(t, "", "setup", "--print-vars", "--no-log-isolation")
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
	for _, want := range []string{"warning:", "ungated", "adopt_legacy_registry", "fugaro setup"} {
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
	if _, _, err := executeStdin(t, "", "setup", "--config-only", "--yes"); err != nil {
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
	out, _, err := executeStdin(t, "", "setup", "--config-only", "--yes", "--scheduler-region", "us-central1")
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
	if _, _, err := executeStdin(t, "", "setup", "--config-only", "--yes"); err != nil {
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
	if _, _, err := executeStdin(t, "", "setup", "--config-only", "--yes"); ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "403") {
		t.Fatalf("an unreadable state: exit %d, err %v", ExitCode(err), err)
	}
	if after, _ := os.ReadFile(r.cfg); string(after) != string(before) {
		t.Error("the local config was written from the flags although the state exists")
	}
	// A foreign state bucket is refused.
	r = newInitRig(t)
	r.gcs.AddBucket(initStateBucket, 999, map[string]string{"fugaro": "tfstate"})
	if _, _, err := executeStdin(t, "", "setup", "--config-only", "--yes"); ExitCode(err) != ExitUserError {
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
	out, _, err := executeStdin(t, "", "update", "--forget", "--yes")
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
	_, _, err = executeStdin(t, "", "update", "--forget", "--yes")
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
	if _, _, err := executeStdin(t, "", "update", "--forget", "--yes"); ExitCode(err) != ExitUserError || len(r.ran(t, "apply")) != 0 {
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
	if _, _, err := executeStdin(t, "", "update", "--forget", "--yes"); err != nil {
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
	if _, _, err := executeStdin(t, "", "update", "--forget", "--yes"); ExitCode(err) != ExitUserError || len(r.ran(t, "plan")) != 0 {
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
	_, _, err := executeStdin(t, "", "update", "--forget", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "update --repo --forget") || !strings.Contains(err.Error(), "*.tfstate") {
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
	if _, _, err := executeStdin(t, "", "setup", "--plan-only"); err != nil {
		t.Fatal(err)
	}
	return r.tfvars(t)
}

// A stale lock under the repositories' prefix is not state: update --repo
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
	if _, _, err := executeStdin(t, "", "update", "--forget", "--yes"); err != nil {
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
	out, _, err := executeStdin(t, "", "update", "--forget", "--yes")
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
	out, _, err := executeStdin(t, "", "setup", "--yes", "--json")
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
	out, _, err := executeStdin(t, "", "setup", "--yes")
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
	out, _, err := executeStdin(t, "", "setup", "--plan-only", "--yes")
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
	for _, args := range [][]string{{"setup", "--yes"}, {"setup", "--yes", "--name", "borealis"}} {
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
	_, _, err = executeStdin(t, "", "setup", "--yes", "--name", "borealis")
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
	_, _, err := executeStdin(t, "", "setup", "--yes")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "no project name yet") || !strings.Contains(err.Error(), "--name aurora") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("an unnamed installation was planned: %q", r.calls(t))
	}
	// With --name the apply goes ahead, and it carries the name.
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--name", "aurora"); err != nil {
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
	_, _, err := executeStdin(t, "", "setup", "--config-only", "--yes")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "the installation has no project name yet") || !strings.Contains(err.Error(), "fugaro setup --name") {
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
	_, _, err := executeStdin(t, "", "setup", "--config-only", "--yes")
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
		!strings.Contains(err.Error(), "the installation has no project name yet; an operator runs fugaro setup --name <name>") {
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
	cmd := newSetupCmd()
	cmd.SetOut(io.Discard)
	ir := newInitRun(cmd, &initOptions{})
	err = ir.writeConfig(context.Background(), lc, spec, infra.InstallationOutputs{ProjectName: "borealis", RunsBucket: initRunsBucket}, r.cfg, nil, true)
	if err == nil || !strings.Contains(err.Error(), "borealis") {
		t.Fatalf("err = %v", err)
	}
}

// A Vertex workflow can't be enforced yet: update --repo refuses before any
// plan or cloud call, while observe and off pass.
func TestInitRepoRefusesVertexEnforce(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	cfgFile := func(auth string) *config.Config {
		t.Helper()
		c, err := config.Parse([]byte("version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: " + auth + " }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"))
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
	// A committed enforce is the effective mode under an off ceiling, and
	// under an observe one (the stricter wins).
	committed := func(mode string) *config.Config {
		t.Helper()
		c, err := config.Parse([]byte("version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: vertex }\nbudget: { mode: " + mode + ", per_run_usd: 5 }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for name, lc := range map[string]*localcfg.Config{"off ceiling": withBudget(""), "observe ceiling": withBudget("budget: { mode: observe }\n")} {
		err := checkVertexBudget(lc, committed("enforce"))
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "Vertex budgets are not supported yet") {
			t.Errorf("committed enforce under %s: exit %d, err %v", name, ExitCode(err), err)
		}
		if err := checkVertexBudget(lc, committed("observe")); err != nil {
			t.Errorf("committed observe under %s: %v", name, err)
		}
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
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: vertex }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	_, _, err = execute(t, "setup", "--repo", "--yes", "--onboard-repo", "acme/webapp", "--project", "aurora", root)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "Vertex budgets are not supported yet") {
		t.Fatalf("update --repo: exit %d, err %v", ExitCode(err), err)
	}
}

// Outputs that exist but can't be decoded are an error, not "no
// installation": the immutable-name check must not be skipped by them.
func TestInitRefusesUnreadableOutputs(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": 5})}
	r.save(t)
	_, _, err := executeStdin(t, "", "setup", "--yes")
	if err == nil || !strings.Contains(err.Error(), "project_name") {
		t.Fatalf("err = %v, want the unreadable output named", err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("unreadable outputs were planned over: %q", r.calls(t))
	}
}

// A brand-new installation's state is empty: `terraform output -json`
// succeeds with {}, which means "no prior installation", not unreadable
// outputs. init goes on to the plan.
func TestInitPlanOnlyOnEmptyState(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["output"] = map[string]any{"stdout": "{}"}
	r.save(t)
	out, _, err := executeStdin(t, "", "setup", "--plan-only")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ran(t, "plan")) != 1 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("calls = %q", r.calls(t))
	}
	if !strings.Contains(out, "create module.installation.google_storage_bucket.runs") {
		t.Errorf("no plan summary:\n%s", out)
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
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err == nil || !strings.Contains(err.Error(), "renaming isn't supported") {
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

// update --repo checks the repository's project against the installation's
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
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n")
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": "borealis"})}
	r.save(t)
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	_, _, err = executeStdin(t, "", "setup", "--repo", "--yes", "--onboard-repo", "acme/webapp", "--project", "aurora", "--github-app-id", "12345", root)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "fugaro.yaml names project aurora, but this installation is project borealis") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("a refused repository was planned: %q", r.calls(t))
	}
}

// The ceiling's new keys reach the job env that update --repo computes.
func TestInitRepoPassesPolicyEnv(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "budget: { max_run_tokens: 123456, allowed_models: [claude-sonnet-5-5] }\nbase_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "setup", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FUGARO_MAX_RUN_TOKENS", "123456", "FUGARO_ALLOWED_MODELS", "claude-sonnet-5-5"} {
		if !strings.Contains(out, want) {
			t.Errorf("print-vars output lacks %q:\n%s", want, out)
		}
	}
}

// TestInitRepoNotesProvidersOpenToEveryRepository: a provider the project
// config leaves open to every repository (no allow_data_to at all, or
// ["*"]) is named once, on stderr -- a repository's owner cannot see the
// project config, and under the old rule an absent list meant nobody, so a
// silently widened key has to be said out loud. stdout stays the tfvars
// alone, as every other note of update --repo's does.
func TestInitRepoNotesProvidersOpenToEveryRepository(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	providers := "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n" +
		"    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n"
	if err := os.WriteFile(path, append(data, []byte(providers+"base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	out, errOut, err := execute(t, "setup", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
	if err != nil {
		t.Fatal(err)
	}
	want := "note: provider openrouter has no allow_data_to, so every repository of the project may mount its key and send it code"
	if n := strings.Count(errOut, want); n != 1 {
		t.Errorf("the note appears %d times, want once:\n%s", n, errOut)
	}
	if strings.Contains(out, "note:") {
		t.Errorf("print-vars stdout must stay the tfvars alone:\n%s", out)
	}
}

// update --repo says what ceiling it is about to apply and which keys of the
// checkout's committed budget block it would clamp.
func TestInitRepoShowsCeilingAndClamps(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "budget: { mode: observe, per_run_usd: 5, max_run_tokens: 1000 }\nbase_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := func(block string) string {
		return "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\n" + block +
			"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	}
	run := func(block string) (string, string) {
		t.Helper()
		root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), yaml(block))
		testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
		t.Chdir(t.TempDir())
		out, errOut, err := execute(t, "setup", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
		if err != nil {
			t.Fatal(err)
		}
		return out, errOut
	}
	out, errOut := run("budget: { mode: off, per_run_usd: 20 }\n")
	want := "Budget ceiling for aurora from project aurora: per_run_usd 5, mode observe, max_run_tokens 1000"
	if !strings.Contains(errOut, want) {
		t.Errorf("stderr lacks %q:\n%s", want, errOut)
	}
	for _, w := range []string{"budget.per_run_usd: 20 is above the project's ceiling of 5", "budget.mode: off is looser than the project's observe"} {
		if !strings.Contains(errOut, w) {
			t.Errorf("stderr lacks %q:\n%s", w, errOut)
		}
	}
	if strings.Contains(out, "ceiling") {
		t.Errorf("print-vars stdout must stay the tfvars alone:\n%s", out)
	}
	// A tighter block clamps nothing.
	if _, errOut = run("budget: { per_run_usd: 2 }\n"); !strings.Contains(errOut, "Budget ceiling") || strings.Contains(errOut, "above") {
		t.Errorf("tight block: %s", errOut)
	}
}

// A project without a budget has no ceiling to announce.
func TestInitRepoSilentWithoutCeiling(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), yaml)
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	out, errOut, err := execute(t, "setup", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
	if err != nil || strings.Contains(out+errOut, "Budget ceiling") {
		t.Fatalf("%v\n%s\n%s", err, out, errOut)
	}
}

// update --repo reads a checkout's governance: block and, in a build that
// links no ee/ features, holds nothing against it: the ceiling a raised
// guardrail would be clamped by carries Fugaro's shipped figures, which
// ee/register registers and this build does not carry (D521, D522). The block
// is still decoded and accepted — refusing it would stop every repository of
// the project (G2) — and nothing here names it as looser than anything.
//
// The budget line stays silent for its own reason: a project with no budget
// has no ceiling to announce.
//
// The tagged build still clamps, and ee/governance's TestGovernanceTightenOnly
// is where that rule is pinned (E2.3).
func TestInitRepoDoesNotClampGovernanceInAnUntaggedBuild(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\n" +
		"governance: { review_ratio: 20 }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), yaml)
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	out, errOut, err := execute(t, "setup", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, errOut)
	}
	if strings.Contains(errOut, "is looser than") {
		t.Errorf("an untagged build clamped a governance value it does not enforce:\n%s", errOut)
	}
	if strings.Contains(errOut, "Budget ceiling") {
		t.Errorf("a project with no budget announced one:\n%s", errOut)
	}
	if strings.Contains(out, "governance") {
		t.Errorf("print-vars stdout must stay the tfvars alone:\n%s", out)
	}
}

// --base-image sets the base kind's entry of base_images, from the image's
// name or KIND=IMAGE, and a later run naming it again replaces it.
//
// TestInitBaseImagePerKind's "leaves the kinds it doesn't name alone" case
// is deleted: it needed a second real kind the 0.7.0 cut removed (java-services) to
// show one kind's --base-image left another's base_images entry untouched,
// which config.Bases (one kind, "base") can no longer have; --base-image
// itself now refuses any kind but "base" (ParseBaseImageFlag).
func TestInitBaseImagePerKind(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	base := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc"
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base-image", base); err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lc.BaseImage("base") != base || len(lc.BaseImages) != 1 {
		t.Fatalf("base_images = %v", lc.BaseImages)
	}
	// A later run replaces it.
	base2 := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-9999999"
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base-image", base2); err != nil {
		t.Fatal(err)
	}
	if lc, err = localcfg.Load(r.cfg); err != nil || lc.BaseImage("base") != base2 || len(lc.BaseImages) != 1 {
		t.Fatalf("base_images = %v, %v", lc.BaseImages, err)
	}
	// An image whose kind can't be told is refused before anything else.
	_, _, err = executeStdin(t, "", "setup", "--yes", "--base-image", "reg/some-image:1")
	if err == nil || !strings.Contains(err.Error(), "KIND=IMAGE") || ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, %v", ExitCode(err), err)
	}
}

func writeFileForTest(path, data string) error { return os.WriteFile(path, []byte(data), 0o644) }

// sandboxProjectSecretLC makes bitbucket-token a project-wide secret
// acme/sandbox may use, so Readiness can resolve that name to the project
// copy.
const sandboxProjectSecretLC = "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/sandbox] }\n"

// repoPlanYAML is acme/sandbox's fugaro.yaml for the planRepo tests: one
// workflow, and (the default) the daily image check job B2's gate judges.
const repoPlanYAML = "version: 1\nproject: aurora\ngit: { provider: bitbucket, base_branch: master }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// repoPlanRig is one repository-root run for the rig's acme/sandbox,
// prepared the way init's own repository stage prepares it: a fake
// terraform reporting changes, the installation's outputs, clients on the
// fakes. It stops just before planRepo, which the caller runs.
type repoPlanRig struct {
	ri   *initRig
	run  *initRun
	out  *strings.Builder
	wd   *infra.Workdir
	tfr  *tf.TF
	c    *infra.Clients
	lc   *localcfg.Config
	cfg  *config.Config
	spec infra.RepoSpec
}

// newRepoPlanRig prepares the rig. stdin answers the repository stage's own
// confirmations, one line each (the run confirmation is taken first, as an
// earlier step of the run would).
func newRepoPlanRig(t *testing.T, stdin, baseImage, extraLC string, changes func(infra.RepoSpec) []planChange) *repoPlanRig {
	t.Helper()
	fakeTerminal(t)
	ri := newInitRig(t)
	ri.appendConfig(t, "base_images: {base: "+baseImage+"}\n"+extraLC)
	lc, err := localcfg.Load(ri.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := config.Parse([]byte(repoPlanYAML))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	r, _, out, _, _ := condition{"a terminal", initOptions{}, true, ""}.reviewRun(t, stdin, true)
	if err := r.confirmOrdinary("applies an earlier step", "nothing was applied", ""); err != nil {
		t.Fatal(err)
	}
	wd, tfr, err := r.terraform("terraform", t.TempDir(), "repo")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tfr.Output(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := infra.Repo(infra.Inputs{LC: lc, Repo: "acme/sandbox", Cfg: cfg, RepoURL: "https://bitbucket.org/acme/sandbox.git", Installation: outs})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Check == nil {
		t.Fatal("the fixture has no check job")
	}
	ri.ar.AddRepository(initProject, "us-east5", spec.Registry.RepositoryID,
		map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label})
	ri.setPlan(t, changes(spec)...)
	c, err := newInitClients(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	return &repoPlanRig{ri: ri, run: r, out: out, wd: wd, tfr: tfr, c: c, lc: lc, cfg: cfg, spec: spec}
}

// plan runs the repository stage.
func (p *repoPlanRig) plan(t *testing.T) (infra.RepoSpec, []infra.Missing, error) {
	t.Helper()
	return p.run.planRepo(t.Context(), p.c, p.tfr, p.wd, p.spec, p.lc, infra.RepoRootOptions{}, true)
}

// seedSandboxProjectSecret seeds bitbucket-token's project-wide container
// (the ID Readiness resolves to for acme/sandbox) with an enabled version
// and the marks discovery checks before adopting it, and returns its ID.
func (p *repoPlanRig) seedProjectSecret(t *testing.T) string {
	t.Helper()
	id := gcp.ProjectSecretID(initProjectName, "bitbucket-token")
	p.ri.sm.Seed(id, projectLabels(initProjectName, "bitbucket-token"), []byte("project-token"))
	return id
}

// changedRepoBuild is the one plan change the repository stage needs to have
// something to apply.
func changedRepoBuild(infra.RepoSpec) []planChange {
	return []planChange{change("module.repo.google_service_account.build", "create")}
}

// sandboxBaseRelease is a release base image of the sandbox fixture's base
// kind, at or after gitSecretIDSince (B2's gate judges it).
const sandboxBaseRelease = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.7.5"

// B1 inside init's own flow: the spec planRepo returns is the one Readiness
// resolved (not the pre-Readiness one its callers used to keep), and the
// first build reads the apply-time choice back out of it rather than
// resolving live again -- the grants the apply just made were fixed for
// exactly that choice, so re-deriving it could only disagree with them.
//
// The proof that no live resolve happens is the drift itself: a version
// stored for the repository's own copy after planRepo returns would win any
// live resolution, and the build must still name the project copy.
func TestPlanRepoThreadsReadyToFirstBuild(t *testing.T) {
	fb := useFakeBuilder(t)
	p := newRepoPlanRig(t, names(3), sandboxBaseRelease, sandboxProjectSecretLC, changedRepoBuild)
	projectID := p.seedProjectSecret(t)
	ready, _, err := p.plan(t)
	if err != nil {
		t.Fatal(err)
	}
	if ready.ProjectSecrets["bitbucket-token"] != projectID {
		t.Fatalf("ready.ProjectSecrets = %v, want bitbucket-token resolved to %s", ready.ProjectSecrets, projectID)
	}
	// The drift, after the apply: a version for the repository's own copy.
	// A build that resolved live would now name it.
	p.ri.sm.Seed(gcp.SecretID(p.spec.Slug, "bitbucket-token"), nil, []byte("repo-token"))
	names := slices.Sorted(maps.Keys(ready.Workflows))
	if built, err := p.run.buildImages(t.Context(), p.lc, p.cfg, ready, names); err != nil || built != 1 || len(fb.specs) != 1 {
		t.Fatalf("built %d, err %v, specs %+v", built, err, fb.specs)
	}
	if fb.specs[0].GitSecretID != projectID {
		t.Fatalf("the first build names %q, want the apply's own %s (the repository's copy %s was stored after it, so this is not a live resolve)",
			fb.specs[0].GitSecretID, projectID, gcp.SecretID(p.spec.Slug, "bitbucket-token"))
	}
}

// L2 of the PR #379 review, the second mutation no test caught: update --repo
// must not pass on whether the project has published a policy. The CLI's side
// of the Firebase gate (D11, plan task 1.4 step 2) is planRepo — it reads the
// node with the operator's own credentials, which only this side holds, and
// hands the answer to Readiness — so a published policy must really stop a
// repository whose jobs cannot read one. A CLI that never filled
// ex.PolicyExists, or that read a failed read as "no policy", deploys the job
// here and this test fails.
func TestPlanRepoGatesOnAPublishedPolicy(t *testing.T) {
	db := gcpfake.NewRTDB(t)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), json.RawMessage(`{"version":4,"budget":{"mode":"enforce","per_run_usd":5}}`))
	// The local config holds the database (so the policy can be read) but no
	// web API key, so its jobs carry neither of what a run needs.
	p := newRepoPlanRig(t, names(3), sandboxBaseRelease, "budget: { mode: off, rtdb_url: "+db.URL+" }\n", changedRepoBuild)
	ready, missing, err := p.plan(t)
	if err != nil {
		t.Fatal(err)
	}
	gate := ""
	for _, m := range missing {
		if m.Kind == infra.MissingFirebase {
			gate = m.Reason
		}
	}
	if gate == "" {
		t.Fatalf("missing = %v, want the Firebase gate for a published policy", missing)
	}
	for _, want := range []string{projectpolicy.PathShared(), infra.RTDBURLEnv, infra.FirebaseAPIKeyEnv, "fugaro update --firebase-project", "fugaro update"} {
		if !strings.Contains(gate, want) {
			t.Errorf("the gate does not say %q: %s", want, gate)
		}
	}
	for name, ws := range ready.Workflows {
		if ws.DeployJob {
			t.Errorf("workflow %s is deployed though no run of it could read the published policy", name)
		}
	}
}

// B2: the check job's own base image is what its rebuild's fugaro comes
// from, so a resolved project-wide credential needs a base image whose
// fugaro reads CheckJobSpec.GitSecretID. When the repository's own copy has
// no version (resolution fell through to the project copy) and the check
// job's base image predates that field, the run is refused before tfvars are
// written or anything is applied, naming both ways out.
func TestPlanRepoRefusesProjectScopeOnOldBaseImage(t *testing.T) {
	p := newRepoPlanRig(t, names(1), "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.5.0", sandboxProjectSecretLC, changedRepoBuild)
	p.seedProjectSecret(t)
	_, _, err := p.plan(t)
	if ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "0.5.0") || !strings.Contains(err.Error(), gitSecretIDSince) ||
		!strings.Contains(err.Error(), "base_images") || !strings.Contains(err.Error(), "--project-wide") && !strings.Contains(err.Error(), "fugaro secrets set bitbucket-token") {
		t.Fatalf("exit %d, err %v: want a refusal naming the base image's release, %s, and both fixes", ExitCode(err), err, gitSecretIDSince)
	}
	if applies := p.ri.ran(t, "apply"); len(applies) != 0 {
		t.Errorf("the run applied anyway: %v", applies)
	}
	if _, err := os.Stat(filepath.Join(p.wd.Root, infra.VarsFile)); err == nil {
		t.Error("the refusal wrote the root's tfvars")
	}
}

// B2's other half: a check job whose base image is not a release image at
// all (a custom or development build) has an unknown fugaro version, so the
// run warns and goes on -- the identical distinction baseRefReason draws
// between an old release and a dev ref for its own constant.
func TestPlanRepoWarnsOnUnparseableBaseImage(t *testing.T) {
	p := newRepoPlanRig(t, names(3), "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc", sandboxProjectSecretLC, changedRepoBuild)
	p.seedProjectSecret(t)
	if _, _, err := p.plan(t); err != nil {
		t.Fatalf("%v", err)
	}
	if out := p.out.String(); !strings.Contains(out, "is not a release base image") || !strings.Contains(out, gitSecretIDSince) {
		t.Fatalf("no warning about the unknown base image release:\n%s", out)
	}
	if applies := p.ri.ran(t, "apply"); len(applies) != 1 {
		t.Fatalf("%d applies, want the run to go on: %v", len(applies), applies)
	}
}

// appPreCheckRig is init's GitHub App pre-check (init.go's appPreCheck) with
// --check-github-app on -- the one path that reads the App's key from Secret
// Manager, so the only one a resolved secret ID decides anything for.
type appPreCheckRig struct {
	ri     *initRig
	run    *initRun
	out    *strings.Builder
	lc     *localcfg.Config
	spec   infra.RepoSpec
	readID string // the secret ID the check read the key from
	auth   *[]string
}

// newAppPreCheckRig prepares it for the rig's acme/sandbox as a GitHub
// repository, with bitbucket-token's sibling github-app-key allow-listed
// project-wide for that repository.
func newAppPreCheckRig(t *testing.T, stdin string) *appPreCheckRig {
	t.Helper()
	ri := newInitRig(t)
	ri.appendConfig(t, "project_secrets:\n  github-app-key: { allow_data_to: [acme/sandbox] }\n")
	lc, err := localcfg.Load(ri.cfg)
	if err != nil {
		t.Fatal(err)
	}
	slug := mustSlug(gitprov.KindGitHub, "acme/sandbox")
	spec := infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Provider: gitprov.KindGitHub, GitHubAppID: "12345",
		GCPProject:               initProject,
		BuildServiceAccountEmail: gcp.BuildServiceAccountID(slug) + "@" + initProject + ".iam.gserviceaccount.com",
		Secrets:                  map[string]string{"github-app-key": gcp.SecretID(slug, "github-app-key")}}
	r, _, out, _, _ := condition{"a terminal", initOptions{checkApp: true}, true, ""}.reviewRun(t, stdin, true)
	rg := &appPreCheckRig{ri: ri, run: r, out: out, lc: lc, spec: spec}
	oldRead, oldAPI := readAppKey, githubAPIURL
	readAppKey = func(_ context.Context, _ *localcfg.Config, id string) ([]byte, error) {
		rg.readID = id
		return []byte(appKeyPEM(t)), nil
	}
	t.Cleanup(func() { readAppKey, githubAPIURL = oldRead, oldAPI })
	rg.auth = gitHubAppServer(t, 200, fullPerms)
	return rg
}

// check runs the pre-check and returns why the first build must wait ("" to
// go on).
func (g *appPreCheckRig) check(t *testing.T) string {
	t.Helper()
	return g.run.appPreCheck(t.Context(), g.lc, g.spec)
}

// Small item 5's fifth call site, and B1 at it: the key this pre-check signs
// with is the one live state resolves for github-app-key -- here the
// project-wide copy, since the repository's own has no version -- and only
// after the build account's own grant is found to name that same ID.
func TestInitAppPreCheckResolvesLiveNotAlwaysRepoID(t *testing.T) {
	g := newAppPreCheckRig(t, "")
	projectID := projectLabelsSeed(t, g.ri.sm, "github-app-key", []byte(appKeyPEM(t)))
	grantBuildAccountFor(t, g.ri.sm, projectID, g.spec.BuildServiceAccountEmail)
	if why := g.check(t); why != "" {
		t.Fatalf("the pre-check held the build: %s\n%s", why, g.out.String())
	}
	if g.readID != projectID {
		t.Fatalf("the key was read from %q, want the project-wide copy %s (the repository's own %s has no version)",
			g.readID, projectID, g.spec.Secrets["github-app-key"])
	}
}

// B1's mismatch case at init's own call site: a repository override stored
// with no apply in between, while the build account's grant still names the
// project copy. The build is held, with both IDs and the apply that fixes
// them, rather than offered on a key the build's clone would not read.
func TestInitAppPreCheckRefusesOnGrantMismatch(t *testing.T) {
	g := newAppPreCheckRig(t, "")
	repoID := g.spec.Secrets["github-app-key"]
	g.ri.sm.Seed(repoID, nil, []byte(appKeyPEM(t))) // the override, versioned
	projectID := projectLabelsSeed(t, g.ri.sm, "github-app-key", nil)
	grantBuildAccountFor(t, g.ri.sm, projectID, g.spec.BuildServiceAccountEmail)
	why := g.check(t)
	if !strings.Contains(why, repoID) || !strings.Contains(why, projectID) {
		t.Fatalf("the hold does not name both IDs: %q", why)
	}
	// The fix line names the door the run is at: the rig's options carry no
	// door, so the held build names the fallback doorName documents (W2.1).
	if out := g.out.String(); !strings.Contains(out, " "+g.run.o.doorName()+" --repo acme/sandbox") || !strings.Contains(out, "not offered") {
		t.Fatalf("the hold names no fix:\n%s", out)
	}
	if g.readID != "" || len(*g.auth) != 0 {
		t.Errorf("the check read %q and asked GitHub %d times, want neither", g.readID, len(*g.auth))
	}
}

// projectLabelsSeed creates (or updates) the project-wide container of
// logical in the rig's fake Secret Manager, marked as the installation
// module's own would be, and returns its ID.
func projectLabelsSeed(t *testing.T, sm *gcpfake.Secrets, logical string, value []byte) string {
	t.Helper()
	id := gcp.ProjectSecretID(initProjectName, logical)
	sm.Seed(id, projectLabels(initProjectName, logical), value)
	return id
}

// grantBuildAccountFor makes secretID's policy grant email the accessor
// role, the grant the apply that resolved that name makes.
func grantBuildAccountFor(t *testing.T, sm *gcpfake.Secrets, secretID, email string) {
	t.Helper()
	sm.SetPolicy(secretID, []gcpfake.Binding{{Role: gcp.SecretAccessorRole, Members: []string{"serviceAccount:" + email}}})
}

// TestInitPrintVarsNotesProvidersOpenToEveryRepository: the converge path
// (init without --repo) reaches init.go's own r.noteOpenProviders call, and
// it must say there which providers every repository of the project may
// use: their keys are mounted into every repository's job and every
// repository may send them code. An operator who never wrote allow_data_to
// would not learn that from the project config alone, so the note is the
// one place they meet it. The --repo path has its own call and its own test
// (TestInitRepoNotesProvidersOpenToEveryRepository); this pins the other.
//
// Mutation (run, restore): drop the r.noteOpenProviders(lc) call from
// init's own run (init.go, just after setProject) — this test finds no note.
func TestInitPrintVarsNotesProvidersOpenToEveryRepository(t *testing.T) {
	r := newInitRig(t)
	r.appendConfig(t, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n"+
		"    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n")
	r.save(t)
	_, errOut, err := executeStdin(t, "", "setup", "--print-vars", "--no-log-isolation")
	if err != nil {
		t.Fatal(err)
	}
	want := "note: provider openrouter has no allow_data_to, so every repository of the project may mount its key and send it code"
	if n := strings.Count(errOut, want); n != 1 {
		t.Errorf("the note appears %d times, want once:\n%s", n, errOut)
	}
}

// TestInitRefusesAProjectJobDeleteWhateverAllowDeleteNames is the stack
// review's init-level pin of infra.RefuseProjectJobDeletes: a plan that
// replaces a project job (a profile renamed, or its account changed) would
// remove the job's launcher grant and then fail on the job's deletion
// protection. The init stage refuses it even with --allow-delete naming both
// the job and the grant, and nothing is applied.
//
// Mutation (run, restore): guard passes RefuseProjectJobDeletes(nil) — the
// plan is applied.
func TestInitRefusesAProjectJobDeleteWhateverAllowDeleteNames(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	mod := `module.project["` + gcp.ProjectJobName("standard", "github", "") + `"]`
	job := mod + ".google_cloud_run_v2_job.this"
	grant := mod + `.google_cloud_run_v2_job_iam_member.launcher["user:launcher@example.com"]`
	r.setPlan(t, change(grant, "delete"), change(job, "delete", "create"))
	_, _, err := executeStdin(t, "", "setup", "--yes", "--allow-delete", job, "--allow-delete", grant)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "retiring a project job is not supported yet") {
		t.Fatalf("exit %d, err %v; want the project-job refusal", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("a plan deleting a project job and its launcher grant was applied")
	}
}

// TestPrintCeilingDoesNotCarryGovernanceInAnUntaggedBuild: `update --repo`
// states the ceiling the run will clamp against, and a build that links no
// ee/ features has no governance in that ceiling whatever the project layer
// writes: the merge records nothing dropped, so there is nothing for this
// command to warn about (D521, D522). The budget half is unchanged and still
// prints when the project has one.
//
// What the tagged arm pins — the layer's 200ms stage timeout against the
// file's 5m, warned about with both values named — is the merge
// ee/governance's TestTheGovernanceWarningsSourceIsTheMergesRecord covers
// (E2.3).
//
// The budget-only line stays the budget's either way: a layer carrying
// governance and no budget has no ceiling to state — "none (no cap, ...)"
// would read as one (TestInitRepoSilentWithoutCeiling).
func TestPrintCeilingDoesNotCarryGovernanceInAnUntaggedBuild(t *testing.T) {
	gov := func(d time.Duration) *config.Governance {
		return &config.Governance{StageTimeout: config.StageTimeout{Implement: config.Duration{Duration: d}}}
	}
	// lc is the project config the ceiling comes from; cfg the checkout's file.
	for _, tc := range []struct {
		name        string
		project     *localcfg.Budget
		file        *config.Budget
		wantCeiling bool
	}{
		{"governance alone", nil, nil, false},
		{"a budget in the file alone", nil, &config.Budget{Mode: config.BudgetEnforce, PerRunUSD: 5}, false},
		{"a budget in the project too", &localcfg.Budget{Mode: localcfg.BudgetEnforce, PerRunUSD: 5}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := &localcfg.Config{Name: "aurora", Budget: tc.project}
			cfg := &config.Config{
				Project:    "aurora",
				Budget:     tc.file,
				Governance: gov(5 * time.Minute),
				Layer:      &config.ProjectLayer{Governance: gov(200 * time.Millisecond)},
			}
			var b strings.Builder
			printCeiling(&b, lc, cfg)
			got := b.String()
			if strings.Contains(got, "governance.") {
				t.Fatalf("an untagged build warned about governance the run does not clamp:\n%s", got)
			}
			if has := strings.Contains(got, "Budget ceiling"); has != tc.wantCeiling {
				t.Fatalf("a budget ceiling line printed = %v, want %v:\n%s", has, tc.wantCeiling, got)
			}
		})
	}
}
