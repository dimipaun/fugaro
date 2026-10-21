package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/runner"
)

// testProjectNumber is proj-1234's number in the fakes.
const testProjectNumber = 123456789012

// cloud is every fake discovery reads, and clients pointed at them.
type cloud struct {
	iam   *gcpfake.IAM
	ar    *gcpfake.ArtifactRegistry
	run   *gcpfake.Run
	sm    *gcpfake.Secrets
	gcs   *gcpfake.GCS
	crm   *gcpfake.CRM
	logs  *gcpfake.Logging
	sched *gcpfake.Scheduler
	su    *gcpfake.ServiceUsage
	c     *Clients
}

func newCloud(t *testing.T) *cloud {
	t.Helper()
	f := &cloud{
		iam: gcpfake.NewIAM(t), ar: gcpfake.NewArtifactRegistry(t), run: gcpfake.NewRun(t),
		sm: gcpfake.NewSecrets(t), gcs: gcpfake.NewGCS(t), crm: gcpfake.NewCRM(t),
		logs: gcpfake.NewLogging(t), sched: gcpfake.NewScheduler(t), su: gcpfake.NewServiceUsage(t),
	}
	c, err := NewClients(context.Background(), f.options(nil), f.endpoints())
	if err != nil {
		t.Fatal(err)
	}
	f.c = c
	f.crm.AddProject("proj-1234", testProjectNumber)
	return f
}

// Without credentials every endpoint must be a fake's: one left empty
// would reach Google itself.
func TestNewClientsNoAuthNeedsEveryEndpoint(t *testing.T) {
	f := newCloud(t)
	e := f.endpoints()
	e.Scheduler = ""
	if _, err := NewClients(context.Background(), f.options(nil), e); err == nil || !strings.Contains(err.Error(), "cloud_scheduler") {
		t.Fatalf("err = %v", err)
	}
	o := f.options(nil)
	o.Endpoints.Logging = ""
	if _, err := NewClients(context.Background(), o, f.endpoints()); err == nil || !strings.Contains(err.Error(), "logging") {
		t.Fatalf("err = %v", err)
	}
}

// The API Keys client is optional like FirebaseDB's: without credentials it
// exists only when its endpoint is set, and a missing one is not an error.
func TestNewClientsAPIKeysIsOptionalUnderNoAuth(t *testing.T) {
	f := newCloud(t)
	if f.c.APIKeys != nil {
		t.Fatal("APIKeys built without an endpoint under NoAuth")
	}
	e := f.endpoints()
	e.APIKeys = gcpfake.NewAPIKeys(t).URL + "/"
	c, err := NewClients(context.Background(), f.options(nil), e)
	if err != nil || c.APIKeys == nil {
		t.Fatalf("%v %v", err, c)
	}
}

// options are the Google API options of the fakes, with hc (when set) as
// the HTTP client.
func (f *cloud) options(hc *http.Client) gcp.Options {
	return gcp.Options{GCPProject: "proj-1234", Region: "us-east5", HTTPClient: hc, Endpoints: gcp.Endpoints{
		Run: f.run.URL + "/", SecretManager: f.sm.URL + "/", Logging: f.logs.URL + "/", NoAuth: true}}
}

func (f *cloud) endpoints() Endpoints {
	return Endpoints{IAM: f.iam.URL + "/", ArtifactRegistry: f.ar.URL + "/", Storage: f.gcs.URL + "/storage/v1/",
		ResourceManager: f.crm.URL + "/", Scheduler: f.sched.URL + "/", ServiceUsage: f.su.URL + "/"}
}

var managed = map[string]string{gcp.LabelManaged: gcp.ManagedValue}

func with(m map[string]string, kv ...string) map[string]string {
	out := maps.Clone(m)
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func saMember(email string) string { return "serviceAccount:" + email }

// bootstrap seeds what the M4 bootstrap made for spec: the runs bucket with
// each job account's conditional grant, the secrets (with a version each)
// and their accessor grants, the job accounts under display (a function
// of the workflow's spec), and each job, running the M4 image.
func (f *cloud) bootstrap(t *testing.T, spec RepoSpec, display func(WorkflowSpec) string) {
	t.Helper()
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	var bucket []gcpfake.Binding
	accessors := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(spec.Workflows)) {
		ws := spec.Workflows[name]
		f.iam.AddServiceAccount(spec.GCPProject, ws.ServiceAccountEmail, display(ws))
		f.run.SetJob(ws.Job, ws.Labels, ws.LegacyImage+":latest")
		c := ws.BucketCondition
		bucket = append(bucket, gcpfake.Binding{Role: "roles/storage.objectUser", Members: []string{saMember(ws.ServiceAccountEmail)},
			Condition: &gcpfake.IAMCondition{Title: c.Title, Expression: c.Expression}})
		for _, logical := range ws.SecretEnv {
			accessors[logical] = append(accessors[logical], saMember(ws.ServiceAccountEmail))
		}
	}
	f.gcs.SetBucketPolicy(spec.Installation.RunsBucket, bucket)
	for logical, id := range spec.Secrets {
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("value-"+logical))
		if m := accessors[logical]; m != nil {
			f.sm.SetPolicy(id, []gcpfake.Binding{{Role: "roles/secretmanager.secretAccessor", Members: m}})
		}
	}
}

func legacyDisplay(ws WorkflowSpec) string { return gcp.LegacyJobSADisplayName(ws.Slug, ws.Name) }
func newDisplay(ws WorkflowSpec) string    { return ws.ServiceAccount.DisplayName }

// m5 adds what an earlier M5 apply made for spec: the repository's
// registry, its build account and its check job.
func (f *cloud) m5(t *testing.T, spec RepoSpec) {
	t.Helper()
	f.ar.AddRepository(spec.GCPProject, spec.Region, spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.iam.AddServiceAccount(spec.GCPProject, spec.BuildServiceAccountEmail, spec.BuildServiceAccount.DisplayName)
	if spec.Check != nil {
		f.run.SetJob(spec.Check.Job, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelRole, gcp.RoleCheck), spec.Check.Image)
		f.sched.SetJob(spec.GCPProject, spec.Check.SchedulerRegion, spec.Check.SchedulerJob, checkRunURI(spec), spec.Installation.SchedulerServiceAccount)
	}
}

// m5Installation adds what an earlier M5 installation apply made, which
// the rollback's state rm leaves in place: the runs bucket, the base
// registry, the remote repository, the custom roles, the scheduler
// account and the log bucket.
func (f *cloud) m5Installation(inst InstallationSpec) {
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, managed)
	f.ar.AddRepository(inst.Project, inst.Region, BaseRegistry, managed)
	f.ar.AddRepository(inst.Project, inst.Region, GhcrRegistry, managed)
	f.iam.AddRole(inst.Project, RoleLauncher, "Fugaro launcher", false)
	f.iam.AddRole(inst.Project, RoleJobRunner, "Fugaro job runner", false)
	f.iam.AddRole(inst.Project, RoleBuildSubmitter, "Fugaro build submitter", false)
	f.iam.AddRole(inst.Project, RoleTagMover, "Fugaro tag mover", false)
	f.iam.AddServiceAccount(inst.Project, SchedulerServiceAccountID+"@"+inst.Project+".iam.gserviceaccount.com", "Fugaro scheduler")
	f.logs.AddBucket(inst.Project, "global", LogBucket, "ACTIVE", LogBucketDescription)
}

func sandboxSpec(t *testing.T) RepoSpec {
	t.Helper()
	rs, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func importMap(im Imports) map[string]string {
	out := map[string]string{}
	for _, i := range im.List {
		out[i.To] = i.ID
	}
	return out
}

func TestDiscoverImportsOwned(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, newDisplay)
	f.m5(t, spec)
	im, ex, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	web := spec.Workflows["web"]
	p := "projects/proj-1234/"
	want := map[string]string{
		`module.repo.google_secret_manager_secret.this["bitbucket-token"]`:    p + "secrets/" + spec.Secrets["bitbucket-token"],
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`: p + "secrets/" + spec.Secrets["claude-oauth-token"],
		`module.repo.google_secret_manager_secret.this["sandbox-probe"]`:      p + "secrets/" + spec.Secrets["sandbox-probe"],
		`module.repo.google_artifact_registry_repository.images`:              p + "locations/us-east5/repositories/" + spec.Registry.RepositoryID,
		`module.repo.google_service_account.build`:                            p + "serviceAccounts/" + spec.BuildServiceAccountEmail,
		`module.repo.google_cloud_run_v2_job.check[0]`:                        p + "locations/us-east5/jobs/" + spec.Check.Job,
		`module.repo.google_cloud_scheduler_job.check[0]`:                     p + "locations/" + spec.Check.SchedulerRegion + "/jobs/" + spec.Check.SchedulerJob,
		`module.repo.module.workflow["web"].google_service_account.job`:       p + "serviceAccounts/" + web.ServiceAccountEmail,
		`module.repo.module.workflow["web"].google_cloud_run_v2_job.this[0]`:  p + "locations/us-east5/jobs/" + web.Job,
	}
	if got := importMap(im); !maps.Equal(got, want) {
		t.Errorf("imports:\n got %v\nwant %v", got, want)
	}
	if ex.Jobs["web"] != web.LegacyImage+":latest" {
		t.Errorf("existing image = %q", ex.Jobs["web"])
	}
	if len(ex.DisplayNames) != 0 {
		t.Errorf("display names kept = %v, want none", ex.DisplayNames)
	}
	// Discovery only reads.
	for _, fk := range []*gcpfake.Server{f.iam.Server, f.ar.Server, f.run.Server, f.sm.Server, f.gcs.Server, f.crm.Server} {
		for _, r := range fk.Requests() {
			if r.Method != "GET" {
				t.Errorf("discovery sent %s %s", r.Method, r.Path)
			}
		}
	}
}

func TestDiscoverCreatesWhatIsMissing(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	im, ex, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(im.List) != 0 || len(ex.Jobs) != 0 {
		t.Errorf("imports %v, existing %+v; want nothing on an empty project", im.List, ex)
	}
	if im.Bindings.Adopted != 0 || im.Bindings.New != 4 {
		t.Errorf("bindings = %+v, want 0 adopted and 4 new (a bucket grant and three accessors)", im.Bindings)
	}
}

// foreign asserts that err refuses resource, naming what it carries.
func foreign(t *testing.T, err error, resource string, marks ...string) {
	t.Helper()
	var fe *ForeignError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want a *ForeignError", err)
	}
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Errorf("a refusal must be a user error (exit 1): %T", err)
	}
	msg := err.Error()
	for _, s := range append([]string{resource}, marks...) {
		if !strings.Contains(msg, s) {
			t.Errorf("refusal %q does not name %q", msg, s)
		}
	}
}

func TestDiscoverRefusesForeignSecret(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, newDisplay)
	id := spec.Secrets["sandbox-probe"]
	f.sm.Seed(id, with(managed, gcp.LabelRepo, "bitbucket_acme_other", gcp.LabelSecret, "sandbox-probe"), []byte("theirs"))
	im, _, err := DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, id, "fugaro_repo=bitbucket_acme_other")
	if len(im.List) != 0 {
		t.Errorf("a refusal must generate no imports: %v", im.List)
	}
}

func TestDiscoverRefusesSecretOtherLogicalName(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, newDisplay)
	id := spec.Secrets["sandbox-probe"]
	f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "npm-token"), []byte("x"))
	_, _, err := DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, id, "fugaro_secret=npm-token")
}

func TestDiscoverRefusesOtherRepoJob(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, newDisplay)
	web := spec.Workflows["web"]
	f.run.SetJob(web.Job, with(web.Labels, gcp.LabelRepo, "bitbucket_acme_other"), "img")
	_, _, err := DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, web.Job, "fugaro_repo=bitbucket_acme_other")

	// An unlabelled job, and one of another workflow, are refused too.
	f.run.SetJob(web.Job, nil, "img")
	_, _, err = DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, web.Job, "fugaro=none")
	f.run.SetJob(web.Job, with(web.Labels, gcp.LabelWorkflow, "api"), "img")
	_, _, err = DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, web.Job, "fugaro_workflow=api")

	// The check job must carry the check role.
	f.run.SetJob(web.Job, web.Labels, "img")
	f.run.SetJob(spec.Check.Job, with(managed, gcp.LabelRepo, spec.Label), spec.Check.Image)
	_, _, err = DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, spec.Check.Job, "fugaro_role=none")
}

func TestDiscoverRefusesSADisplayName(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, func(WorkflowSpec) string { return "Someone else's account" })
	web := spec.Workflows["web"]
	_, _, err := DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, web.ServiceAccountEmail, "Someone else's account")

	// A build account must carry the build display name.
	f = newCloud(t)
	f.bootstrap(t, spec, newDisplay)
	f.iam.AddServiceAccount(spec.GCPProject, spec.BuildServiceAccountEmail, gcp.JobSADisplayName(spec.Slug, "web"))
	_, _, err = DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, spec.BuildServiceAccountEmail)
}

func TestDiscoverAcceptsLegacySADisplayName(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, legacyDisplay)
	im, ex, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	web := spec.Workflows["web"]
	if _, ok := importMap(im)[`module.repo.module.workflow["web"].google_service_account.job`]; !ok {
		t.Errorf("legacy account not imported: %v", im.List)
	}
	legacy := gcp.LegacyJobSADisplayName(spec.Slug, "web")
	if ex.DisplayNames["web"] != legacy {
		t.Errorf("kept display name = %q, want %q", ex.DisplayNames["web"], legacy)
	}
	got, _, err := Readiness(context.Background(), f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if dn := got.Workflows["web"].ServiceAccount.DisplayName; dn != legacy {
		t.Errorf("the spec renames the account to %q; it must keep %q", dn, legacy)
	}
	if spec.Workflows["web"].ServiceAccount.DisplayName != web.ServiceAccount.DisplayName {
		t.Error("Readiness changed its input spec")
	}
	vars, err := RepoVars(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(vars), `"display_name": "`+legacy+`"`) {
		t.Errorf("the tfvars don't carry the legacy display name:\n%s", vars)
	}
}

func TestDiscoverRefusesBucketInOtherProject(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, 999, managed)
	_, _, err := DiscoverRepo(context.Background(), f.c, spec)
	foreign(t, err, spec.Installation.RunsBucket, "999")

	inst := installationSpec(t)
	_, err = DiscoverInstallation(context.Background(), f.c, inst)
	foreign(t, err, inst.RunsBucket, "999")

	// In the project but unmarked is refused too.
	f = newCloud(t)
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, nil)
	_, err = DiscoverInstallation(context.Background(), f.c, inst)
	foreign(t, err, inst.RunsBucket, "fugaro=none")
}

func installationSpec(t *testing.T) InstallationSpec {
	t.Helper()
	s, err := Installation(parseLC(t, m4LocalConfig+m5Additions), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDiscoverInstallation(t *testing.T) {
	f := newCloud(t)
	inst := installationSpec(t)
	ctx := context.Background()
	im, err := DiscoverInstallation(ctx, f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	if len(im.List) != 0 || im.AdoptLegacyRegistry {
		t.Errorf("a fresh project: %+v", im)
	}

	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, managed)
	f.ar.AddRepository("proj-1234", "us-east5", LegacyRegistry, managed)
	f.ar.AddRepository("proj-1234", "us-east5", BaseRegistry, managed)
	im, err = DiscoverInstallation(ctx, f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"module.installation.google_storage_bucket.runs":                    "proj-1234/" + inst.RunsBucket,
		"module.installation.google_artifact_registry_repository.legacy[0]": "projects/proj-1234/locations/us-east5/repositories/fugaro",
		"module.installation.google_artifact_registry_repository.base":      "projects/proj-1234/locations/us-east5/repositories/fugaro-base",
	}
	if got := importMap(im); !maps.Equal(got, want) {
		t.Errorf("imports:\n got %v\nwant %v", got, want)
	}
	if !im.AdoptLegacyRegistry {
		t.Error("a marked legacy registry must set adopt_legacy_registry")
	}
}

// TestDiscoverInstallationAdoptsTheGhcrRegistry: the remote repository is
// an installation singleton like the base registry (design §5.3). Check
// 43a creates fugaro-ghcr by hand before the implementation exists, so the
// first init after it must adopt a repository that carries fugaro=managed
// and refuse, by name, one that does not — never the bare Terraform
// "already exists" an ordinary create would give (design §5.4).
func TestDiscoverInstallationAdoptsTheGhcrRegistry(t *testing.T) {
	ctx := context.Background()
	inst := installationSpec(t)
	const address = "module.installation.google_artifact_registry_repository.ghcr"

	f := newCloud(t)
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, managed)
	f.ar.AddRepository("proj-1234", "us-east5", GhcrRegistry, managed)
	im, err := DiscoverInstallation(ctx, f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	if got := importMap(im)[address]; got != "projects/proj-1234/locations/us-east5/repositories/"+GhcrRegistry {
		t.Errorf("imports %v, want the remote repository adopted", importMap(im))
	}

	// Unmarked: refused by name, at discovery time, and nothing is adopted.
	f = newCloud(t)
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, managed)
	f.ar.AddRepository("proj-1234", "us-east5", GhcrRegistry, nil)
	_, err = DiscoverInstallation(ctx, f.c, inst)
	foreign(t, err, GhcrRegistry, "fugaro=none")

	// Absent: nothing planned and nothing noted, the ordinary create.
	f = newCloud(t)
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, managed)
	im, err = DiscoverInstallation(ctx, f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := importMap(im)[address]; ok || len(im.Notes) != 0 {
		t.Errorf("a project without the remote repository: %+v", im)
	}
}

// After a rollback (state rm), every installation resource is still
// there; a retried fugaro update imports the custom roles, the scheduler
// account and the log bucket too, since a create of any of them fails.
func TestDiscoverInstallationAfterForget(t *testing.T) {
	f := newCloud(t)
	inst := installationSpec(t)
	f.m5Installation(inst)
	im, err := DiscoverInstallation(context.Background(), f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	p := "projects/proj-1234/"
	want := map[string]string{
		"module.installation.google_storage_bucket.runs":                     "proj-1234/" + inst.RunsBucket,
		"module.installation.google_artifact_registry_repository.base":       p + "locations/us-east5/repositories/fugaro-base",
		"module.installation.google_artifact_registry_repository.ghcr":       p + "locations/us-east5/repositories/" + GhcrRegistry,
		"module.installation.google_project_iam_custom_role.launcher":        p + "roles/" + RoleLauncher,
		"module.installation.google_project_iam_custom_role.job_runner":      p + "roles/" + RoleJobRunner,
		"module.installation.google_project_iam_custom_role.build_submitter": p + "roles/" + RoleBuildSubmitter,
		"module.installation.google_project_iam_custom_role.tag_mover":       p + "roles/" + RoleTagMover,
		"module.installation.google_service_account.scheduler":               p + "serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com",
		"module.installation.google_logging_project_bucket_config.fugaro[0]": p + "locations/global/buckets/" + LogBucket,
	}
	if got := importMap(im); !maps.Equal(got, want) {
		t.Errorf("imports:\n got %v\nwant %v", got, want)
	}

	// With log isolation off nothing plans the log bucket, so it isn't read.
	off := inst
	off.LogIsolation = new(false)
	before := len(f.logs.Requests())
	im, err = DiscoverInstallation(context.Background(), f.c, off)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := importMap(im)[LogBucketAddress]; ok || len(f.logs.Requests()) != before {
		t.Errorf("log isolation off: imports %v, %d log requests", im.List, len(f.logs.Requests())-before)
	}
}

// A soft-deleted custom role is left to the plan's create, which the
// provider turns into an undelete; an import of a deleted role would fail.
func TestDiscoverLeavesDeletedRole(t *testing.T) {
	f := newCloud(t)
	inst := installationSpec(t)
	f.iam.AddRole("proj-1234", RoleLauncher, "Fugaro launcher", true)
	im, err := DiscoverInstallation(context.Background(), f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	if len(im.List) != 0 {
		t.Errorf("imports = %v", im.List)
	}
}

func TestDiscoverRefusesForeignSingletons(t *testing.T) {
	ctx := context.Background()
	inst := installationSpec(t)

	f := newCloud(t)
	f.iam.AddRole("proj-1234", RoleJobRunner, "Someone's runner", false)
	_, err := DiscoverInstallation(ctx, f.c, inst)
	foreign(t, err, "custom role projects/proj-1234/roles/"+RoleJobRunner, `"Someone's runner"`, `"Fugaro job runner"`)

	f = newCloud(t)
	f.iam.AddRole("proj-1234", RoleTagMover, "Someone's tagger", false)
	_, err = DiscoverInstallation(ctx, f.c, inst)
	foreign(t, err, "custom role projects/proj-1234/roles/"+RoleTagMover, `"Someone's tagger"`, `"Fugaro tag mover"`)

	f = newCloud(t)
	f.iam.AddServiceAccount("proj-1234", "fugaro-scheduler@proj-1234.iam.gserviceaccount.com", "Cron")
	_, err = DiscoverInstallation(ctx, f.c, inst)
	foreign(t, err, "service account fugaro-scheduler@proj-1234.iam.gserviceaccount.com", `"Cron"`, `"Fugaro scheduler"`)

	// Our log bucket pending deletion can't be imported or created: the
	// retry needs the undelete first, which the refusal names.
	f = newCloud(t)
	f.logs.AddBucket("proj-1234", "global", LogBucket, "DELETE_REQUESTED", LogBucketDescription)
	_, err = DiscoverInstallation(ctx, f.c, inst)
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), LogBucketUndelete("proj-1234")) {
		t.Fatalf("a log bucket pending deletion: %v", err)
	}

	// A log bucket carries no labels, so its description is the mark: one
	// without it is someone else's, whose retention the plan would cut and
	// a rollback would delete. Pending deletion or not, it is refused, and
	// the refusal says how to mark it or keep out of its way, never to
	// undelete it.
	for _, state := range []string{"ACTIVE", "DELETE_REQUESTED"} {
		for _, desc := range []string{"", "Team logs, kept a year"} {
			f = newCloud(t)
			f.logs.AddBucket("proj-1234", "global", LogBucket, state, desc)
			im, err := DiscoverInstallation(ctx, f.c, inst)
			if !errors.As(err, &ue) || len(im.List) != 0 {
				t.Fatalf("%s log bucket with description %q: imports %v, err %v", state, desc, im.List, err)
			}
			msg := err.Error()
			for _, want := range []string{"log bucket projects/proj-1234/locations/global/buckets/" + LogBucket, strconv.Quote(desc), strconv.Quote(LogBucketDescription),
				"gcloud logging buckets update " + LogBucket + " --location=global --project proj-1234 --description=", "--no-log-isolation"} {
				if !strings.Contains(msg, want) {
					t.Errorf("%s log bucket with description %q: the refusal lacks %q:\n%s", state, desc, want, msg)
				}
			}
			if strings.Contains(msg, "undelete") {
				t.Errorf("%s log bucket with description %q: the refusal suggests an undelete:\n%s", state, desc, msg)
			}
		}
	}
}

// After fugaro update --forget, the repository's Scheduler job is still
// there, in the scheduler region, and is imported by its name and target.
func TestDiscoverImportsSchedulerJob(t *testing.T) {
	ctx := context.Background()
	spec := sandboxSpec(t)
	if spec.Check == nil || spec.Check.SchedulerRegion == spec.Region {
		t.Fatalf("the fixture needs a check in another region than the job's: %+v", spec.Check)
	}
	f := newCloud(t)
	f.bootstrap(t, spec, newDisplay)
	f.m5(t, spec)
	im, _, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	want := "projects/proj-1234/locations/" + spec.Check.SchedulerRegion + "/jobs/" + spec.Check.SchedulerJob
	if got := importMap(im)["module.repo.google_cloud_scheduler_job.check[0]"]; got != want {
		t.Errorf("scheduler job import = %q, want %q", got, want)
	}

	// One under our name that starts another job, or as another account,
	// isn't ours.
	for name, set := range map[string]func(*cloud){
		"target": func(f *cloud) {
			f.sched.SetJob(spec.GCPProject, spec.Check.SchedulerRegion, spec.Check.SchedulerJob, "https://example.com/", spec.Installation.SchedulerServiceAccount)
		},
		"account": func(f *cloud) {
			f.sched.SetJob(spec.GCPProject, spec.Check.SchedulerRegion, spec.Check.SchedulerJob, checkRunURI(spec), "x@proj-1234.iam.gserviceaccount.com")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCloud(t)
			f.bootstrap(t, spec, newDisplay)
			set(f)
			_, _, err := DiscoverRepo(ctx, f.c, spec)
			foreign(t, err, "Cloud Scheduler job "+spec.Check.SchedulerJob)
		})
	}
}

// The singletons' lookups fail closed too: a 403 or a 500 is a remote
// error, never "doesn't exist".
func TestSingletonLookupsFailClosed(t *testing.T) {
	ctx := context.Background()
	spec := sandboxSpec(t)
	inst := installationSpec(t)
	for _, tc := range []struct{ name, match string }{
		{"role", "/roles/" + RoleBuildSubmitter},
		{"scheduler account", "/serviceAccounts/fugaro-scheduler@"},
		{"log bucket", "/buckets/" + LogBucket},
		{"scheduler job", "/jobs/" + spec.Check.SchedulerJob},
	} {
		for _, code := range []int{http.StatusForbidden, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s %d", tc.name, code), func(t *testing.T) {
				f := newCloud(t)
				ft := &failingTransport{match: tc.match, code: code}
				c, err := NewClients(ctx, f.options(&http.Client{Transport: ft}), f.endpoints())
				if err != nil {
					t.Fatal(err)
				}
				f.bootstrap(t, spec, newDisplay)
				f.m5(t, spec)
				f.m5Installation(inst)
				var im Imports
				if tc.name == "scheduler job" {
					im, _, err = DiscoverRepo(ctx, c, spec)
				} else {
					im, err = DiscoverInstallation(ctx, c, inst)
				}
				var ue *UserError
				if err == nil || errors.As(err, &ue) || len(im.List) != 0 {
					t.Errorf("err = %v, imports %v; want a remote error and no imports", err, im.List)
				}
			})
		}
	}
}

func TestDiscoverRefusesUnlabelledRegistry(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, newDisplay)
	f.ar.AddRepository("proj-1234", "us-east5", spec.Registry.RepositoryID, managed)
	_, _, err := DiscoverRepo(ctx, f.c, spec)
	foreign(t, err, spec.Registry.RepositoryID, "fugaro_repo=none")

	inst := installationSpec(t)
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, managed)
	f.ar.AddRepository("proj-1234", "us-east5", BaseRegistry, nil)
	_, err = DiscoverInstallation(ctx, f.c, inst)
	foreign(t, err, BaseRegistry, "fugaro=none")

	// An unmarked legacy registry is not ours to adopt, and nothing plans
	// it, so it is left alone.
	f = newCloud(t)
	f.ar.AddRepository("proj-1234", "us-east5", LegacyRegistry, nil)
	im, err := DiscoverInstallation(ctx, f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	if im.AdoptLegacyRegistry || len(im.List) != 0 || len(im.Notes) != 1 {
		t.Errorf("an unmarked legacy registry: %+v", im)
	}
	if want := "registry us-east5-docker.pkg.dev/proj-1234/" + LegacyRegistry + " has no fugaro=managed label"; len(im.Notes) == 1 &&
		(!strings.HasPrefix(im.Notes[0], want) || !strings.HasSuffix(im.Notes[0], "left alone, not managed by Terraform")) {
		t.Errorf("note = %q", im.Notes[0])
	}
}

func TestDiscoverRefusesMismatchedBinding(t *testing.T) {
	ctx := context.Background()
	spec := sandboxSpec(t)
	web := spec.Workflows["web"]
	c := web.BucketCondition
	member := saMember(web.ServiceAccountEmail)
	for _, tc := range []struct {
		name     string
		bindings []gcpfake.Binding
		found    string
	}{
		{"one space", []gcpfake.Binding{{Role: "roles/storage.objectUser", Members: []string{member},
			Condition: &gcpfake.IAMCondition{Title: c.Title, Expression: strings.Replace(c.Expression, " || ", " ||  ", 1)}}},
			strconv.Quote(strings.Replace(c.Expression, " || ", " ||  ", 1))},
		{"other title", []gcpfake.Binding{{Role: "roles/storage.objectUser", Members: []string{member},
			Condition: &gcpfake.IAMCondition{Title: "mine", Expression: c.Expression}}}, `title="mine"`},
		{"a description", []gcpfake.Binding{{Role: "roles/storage.objectUser", Members: []string{member},
			Condition: &gcpfake.IAMCondition{Title: c.Title, Expression: c.Expression, Description: "hand-made"}}}, "hand-made"},
		{"unconditional", []gcpfake.Binding{{Role: "roles/storage.objectUser", Members: []string{member}}}, "no condition"},
		{"two", []gcpfake.Binding{
			{Role: "roles/storage.objectUser", Members: []string{member}, Condition: &gcpfake.IAMCondition{Title: c.Title, Expression: c.Expression}},
			{Role: "roles/storage.objectUser", Members: []string{member}, Condition: &gcpfake.IAMCondition{Title: "old", Expression: "true"}},
		}, `title="old"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloud(t)
			f.bootstrap(t, spec, legacyDisplay)
			f.gcs.SetBucketPolicy(spec.Installation.RunsBucket, tc.bindings)
			im, _, err := DiscoverRepo(ctx, f.c, spec)
			var be *BindingError
			if !errors.As(err, &be) {
				t.Fatalf("err = %v, want a *BindingError", err)
			}
			var ue *UserError
			if !errors.As(err, &ue) {
				t.Error("a mismatched binding must be a user error (exit 1)")
			}
			msg := err.Error()
			for _, s := range []string{"- want", "+ found", strconv.Quote(c.Expression), tc.found, member, spec.Installation.RunsBucket} {
				if !strings.Contains(msg, s) {
					t.Errorf("refusal lacks %q:\n%s", s, msg)
				}
			}
			if len(im.List) != 0 {
				t.Errorf("a refusal must generate no imports: %v", im.List)
			}
		})
	}
	// A conditional accessor grant on a secret is refused too: the planned
	// grant would sit next to it.
	f := newCloud(t)
	f.bootstrap(t, spec, legacyDisplay)
	id := spec.Secrets["sandbox-probe"]
	f.sm.SetPolicy(id, []gcpfake.Binding{{Role: "roles/secretmanager.secretAccessor", Members: []string{member},
		Condition: &gcpfake.IAMCondition{Title: "t", Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"}}})
	_, _, err := DiscoverRepo(ctx, f.c, spec)
	var be *BindingError
	if !errors.As(err, &be) || !strings.Contains(err.Error(), id) {
		t.Errorf("conditional accessor: err = %v", err)
	}
}

func TestDiscoverCountsAdoptedBindings(t *testing.T) {
	f := newCloud(t)
	in := webappInputs(t)
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	// The bootstrap onboarded web; api is new.
	web := spec
	web.Workflows = map[string]WorkflowSpec{"web": spec.Workflows["web"]}
	f.bootstrap(t, web, legacyDisplay)
	for logical, id := range spec.Secrets {
		if _, ok := web.Secrets[logical]; !ok {
			f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), nil)
		}
	}
	// The legacy build account also reads the secrets: not ours to plan,
	// so it is noted.
	legacyBuild := "serviceAccount:fugaro-build@proj-1234.iam.gserviceaccount.com"
	npm := spec.Secrets["npm-token"]
	f.sm.SetPolicy(npm, []gcpfake.Binding{{Role: "roles/secretmanager.secretAccessor",
		Members: []string{saMember(spec.Workflows["web"].ServiceAccountEmail), legacyBuild}}})

	im, _, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	// web: its bucket grant and its three secrets (github-app-key,
	// npm-token and nothing for the agent on Vertex) match; api's bucket
	// grant and its one secret are new.
	webN := 1 + len(uniqueSecrets(spec.Workflows["web"]))
	apiN := 1 + len(uniqueSecrets(spec.Workflows["api"]))
	if im.Bindings.Adopted != webN || im.Bindings.New != apiN {
		t.Errorf("bindings = %+v, want %d adopted and %d new", im.Bindings, webN, apiN)
	}
	if got, want := im.Bindings.String(), "IAM: 3 bindings match the live ones (adopted), 2 new"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if !slices.ContainsFunc(im.Notes, func(n string) bool { return strings.Contains(n, legacyBuild) && strings.Contains(n, npm) }) {
		t.Errorf("notes = %q, want one about %s on %s", im.Notes, legacyBuild, npm)
	}
}

func TestReadinessKeepsExistingJob(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, legacyDisplay)
	probe := spec.Secrets["sandbox-probe"]
	f.sm.Seed(probe, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "sandbox-probe"), nil)
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	got, missing, err := Readiness(ctx, f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Workflows["web"].DeployJob {
		t.Error("an existing job must stay deployed")
	}
	if len(missing) != 2 {
		t.Fatalf("missing = %v, want the secret and the image", missing)
	}
	for _, m := range missing {
		if !m.Deployed || m.Workflow != "web" {
			t.Errorf("missing %+v: want a warning for web's deployed job", m)
		}
	}
	if s := missing[0].String() + missing[1].String(); !strings.Contains(s, "secret sandbox-probe has no version: fugaro secrets set sandbox-probe --repo acme/sandbox") {
		t.Errorf("missing = %q", s)
	}
}

func TestReadinessKeepsImageUntilBuilt(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, legacyDisplay)
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	web := spec.Workflows["web"]
	got, _, err := Readiness(ctx, f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if img := got.Workflows["web"].Image; img != web.LegacyImage+":latest" {
		t.Errorf("before the build, image = %q, want the job's current %q", img, web.LegacyImage+":latest")
	}
	if spec.Workflows["web"].Image != web.Image {
		t.Error("Readiness changed its input spec")
	}
	f.ar.AddRepository("proj-1234", "us-east5", spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.ar.SetTag("proj-1234", "us-east5", spec.Registry.RepositoryID, strings.TrimSuffix(strings.TrimPrefix(web.Image, spec.RegistryPath+"/"), ":latest"), "latest")
	got, missing, err := Readiness(ctx, f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if img := got.Workflows["web"].Image; img != web.Image {
		t.Errorf("after the build, image = %q, want %q", img, web.Image)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v", missing)
	}
}

func TestReadinessWaitsForImageAndSecrets(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	for logical, id := range spec.Secrets {
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), nil)
	}
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	ready := func() (bool, []string) {
		t.Helper()
		got, missing, err := Readiness(ctx, f.c, spec, ex, false)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range missing {
			if m.Deployed {
				t.Errorf("%+v: no job exists", m)
			}
			out = append(out, m.String())
		}
		return got.Workflows["web"].DeployJob, out
	}
	ok, missing := ready()
	if ok || len(missing) != 5 {
		t.Errorf("deploy %v, missing %q; want no job, three secrets, the image and the check's credential", ok, missing)
	}
	if !slices.ContainsFunc(missing, func(s string) bool { return strings.Contains(s, "image not built yet") }) {
		t.Errorf("missing = %q", missing)
	}
	web := spec.Workflows["web"]
	f.ar.AddRepository("proj-1234", "us-east5", spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.ar.SetTag("proj-1234", "us-east5", spec.Registry.RepositoryID, strings.TrimSuffix(strings.TrimPrefix(web.Image, spec.RegistryPath+"/"), ":latest"), "latest")
	if ok, missing := ready(); ok || len(missing) != 4 {
		t.Errorf("with the image: deploy %v, missing %q", ok, missing)
	}
	for logical, id := range spec.Secrets {
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	if ok, missing := ready(); !ok || len(missing) != 0 {
		t.Errorf("with the image and secrets: deploy %v, missing %q", ok, missing)
	}
}

// TestReadinessCloneDeploysWithoutAnImageBuild (design generic-tool.md §2):
// a checkout: clone workflow's job is ready as soon as its secrets have
// versions, with no Artifact Registry "latest" tag to wait for — its image
// is the installation's base, never a tag in the repository's own registry.
// Without one (an empty base_images.base), it is reported missing instead
// of making Readiness fail outright.
//
// Mutation (run, restore): in Readiness, change the checkout: clone branch's
// `built = ws.Image != ""` to instead call hasLatest unconditionally (as the
// non-clone branch does) — this test then fails with a *UserError ("is not
// <registry>/<package>:latest in the repository's registry"), because
// base_images.base is never a tag in the repository's own registry.
func TestReadinessCloneDeploysWithoutAnImageBuild(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	rs, err := Repo(cloneInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	f.gcs.AddBucket(rs.Installation.RunsBucket, testProjectNumber, managed)
	for logical, id := range rs.Secrets {
		f.sm.Seed(id, with(managed, gcp.LabelRepo, rs.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	_, ex, err := DiscoverRepo(ctx, f.c, rs)
	if err != nil {
		t.Fatal(err)
	}
	got, missing, err := Readiness(ctx, f.c, rs, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Workflows["web"].DeployJob {
		t.Errorf("web (checkout: clone) not ready: missing = %v", missing)
	}
	for _, m := range missing {
		if m.Workflow != "web" {
			continue
		}
		// web waits for no build of its own: its image is the installation's
		// base. A note would not hold it back either (MissingNote gates
		// nothing, readiness.go), and the one web could carry is D11's budget
		// note -- its job's budget is on and it carries no Firebase backend,
		// so a run could not read a project policy -- which is why it is
		// named by its own constants here rather than tolerated as "any note"
		// (code review, low). Every other kind, or a note about anything
		// else, is a reason web is not deployable.
		//
		// This fixture's local config names no budget, so that note does not
		// appear for web here today; the check is what keeps a note from
		// passing as a reason if one ever does.
		if m.Kind != MissingNote || !strings.Contains(m.Reason, BudgetModeEnv) || !strings.Contains(m.Reason, RTDBURLEnv) {
			t.Errorf("web (checkout: clone) has a missing reason: %v", m)
		}
	}
	// Without a base image, web is reported missing, not a hard failure.
	rs.Workflows = maps.Clone(rs.Workflows)
	w := rs.Workflows["web"]
	w.Image = ""
	rs.Workflows["web"] = w
	_, ex, err = DiscoverRepo(ctx, f.c, rs)
	if err != nil {
		t.Fatal(err)
	}
	_, missing, err = Readiness(ctx, f.c, rs, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(missing, func(m Missing) bool { return m.Workflow == "web" && m.Kind == MissingImage }) {
		t.Errorf("no MissingImage for web without base_images.base: %v", missing)
	}
}

// A named account's job variant (gcp.WorkflowVariant, design
// docs/design/named-oauth-tokens.md §4) shares its logical workflow's one
// image, built once: its own "image not built yet" hint must still tell the
// owner to run fugaro image build --workflow <the fugaro.yaml name>, never
// --workflow <the variant key>, which --workflow does not accept.
func TestReadinessImageHintNamesTheLogicalWorkflow(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	in := sandboxInputs(t, m5Additions+claudeAccountsLC)
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	_, missing, err := Readiness(ctx, f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range missing {
		if m.Workflow != "web_acme-personal" || m.Kind != MissingImage {
			continue
		}
		found = true
		if strings.Contains(m.Reason, "--workflow web_acme-personal") || !strings.Contains(m.Reason, "--workflow web") {
			t.Errorf("the named variant's missing reason is %q; want it to say --workflow web, never the variant key", m.Reason)
		}
	}
	if !found {
		t.Fatal("no MissingImage entry for the named account's job variant")
	}
}

// TestReadinessGatesCheckJob: the check job's gate and the fix line the
// operator is handed for its unversioned credential. The check job mounts the
// provider credential at latest, which Cloud Run checks when it creates the
// job, so like a workflow job it waits until the credential has a version;
// the invoker grant and the Scheduler job wait with it. A check job that
// exists stays, whatever its secrets.
//
// The assertion pins the whole command, not a prefix of it: the check job's
// credential is the sandbox's git token, which is the one reserved name D2's
// sweep does not take in (config.PerRepositorySecret, the owner's ruling of
// 2026-10-15), so the fix is the repository-copy form — and the bare
// project-scope spelling is a prefix of it, which is what a Contains test that
// stopped at the prefix would accept while the sweep regressed.
//
// Mutation (verified, run and restored): make config.PerRepositorySecret
// answer false for every name (the sweep taking bitbucket-token in, which is
// the regression this test exists for): this test fails, reading
// "fugaro secrets set bitbucket-token" with no --repo.
func TestReadinessGatesCheckJob(t *testing.T) {
	ctx := context.Background()
	spec := sandboxSpec(t)
	if !spec.Check.DeployJob {
		t.Fatal("the ungated spec must deploy the check job")
	}
	creds := slices.Sorted(maps.Values(spec.Check.SecretEnv))
	if len(creds) != 1 {
		t.Fatalf("check secrets = %v, want the provider credential alone", creds)
	}
	gate := func(f *cloud) (RepoSpec, []Missing) {
		t.Helper()
		_, ex, err := DiscoverRepo(ctx, f.c, spec)
		if err != nil {
			t.Fatal(err)
		}
		got, missing, err := Readiness(ctx, f.c, spec, ex, false)
		if err != nil {
			t.Fatal(err)
		}
		var check []Missing
		for _, m := range missing {
			if m.Check {
				check = append(check, m)
			}
		}
		return got, check
	}

	// A fresh repository: fugaro update has just made the secrets, empty.
	f := newCloud(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	f.seedVersions(spec)
	got, missing := gate(f)
	if got.Check.DeployJob || len(missing) != 1 || missing[0].Kind != MissingSecret || missing[0].Deployed {
		t.Fatalf("an empty credential: deploy %v, missing %+v; want no check job, and the credential missing", got.Check.DeployJob, missing)
	}
	// The credential is the sandbox's git token, and it is the one reserved
	// name D2's sweep does not take in: a Bitbucket repository access token is
	// issued for one repository (config.PerRepositorySecret, the owner's
	// ruling of 2026-10-15), so the fix names the repository-copy command and
	// not the bare one. The assertion is the whole command for that reason —
	// "fugaro secrets set bitbucket-token" is a prefix of both spellings, so a
	// sweep that regressed to include this name would print the project-scope
	// command and pass a Contains test that stopped there (code review, low).
	want := "secret " + creds[0] + " has no version: fugaro secrets set " + creds[0] + " --repo " + spec.Name
	if s := missing[0].String(); !strings.Contains(s, "daily image check") || !strings.Contains(s, "not deployed yet") || !strings.Contains(s, want) {
		t.Errorf("missing = %q\nwant it to contain %q", s, want)
	}
	if _, ok := spec.ProjectSecretCandidates[creds[0]]; ok {
		t.Fatalf("the fixture's check credential %s has a project-wide candidate: this test asserts the repository-copy command", creds[0])
	}
	if !got.Check.Paused {
		t.Error("the schedule must stay paused")
	}

	// Once the credential has a version, the check job is deployed; the
	// other secrets don't matter to it.
	f.seedVersions(spec, creds[0])
	if got, missing := gate(f); !got.Check.DeployJob || len(missing) != 0 {
		t.Errorf("with the credential: deploy %v, missing %+v", got.Check.DeployJob, missing)
	}

	// An existing check job is never removed by the gate: a missing
	// credential version is only a warning.
	f = newCloud(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	f.seedVersions(spec)
	f.m5(t, spec)
	got, missing = gate(f)
	if !got.Check.DeployJob || len(missing) != 1 || !missing[0].Deployed || !strings.Contains(missing[0].String(), "stays deployed") {
		t.Errorf("an existing check job: deploy %v, missing %+v", got.Check.DeployJob, missing)
	}
}

func TestScheduleOnlyWithRecord(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, legacyDisplay)
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := Readiness(ctx, f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Check.Paused {
		t.Error("without a build record the schedule must stay paused")
	}
	b := f.gcs.Bucket(t, spec.Installation.RunsBucket)
	if err := b.WriteAll(ctx, imagecheck.RecordKey(spec.Slug, "web"), []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	got, _, err = Readiness(ctx, f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.Paused {
		t.Error("with every checked workflow's record the schedule must run")
	}
	if !spec.Check.Paused {
		t.Error("Readiness changed its input spec's check")
	}
}

// failingTransport answers every request whose path contains match with
// code, in Google's error shape, and passes the rest through.
type failingTransport struct {
	match string
	code  int
}

func (f *failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.match != "" && strings.Contains(r.URL.Path, f.match) {
		body := fmt.Sprintf(`{"error":{"code":%d,"message":"injected","status":"X"}}`, f.code)
		return &http.Response{StatusCode: f.code, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	return http.DefaultTransport.RoundTrip(r)
}

// TestLookupsFailClosed makes each lookup answer 403 and 500. Neither may
// read as "doesn't exist": each must fail as a remote error (not a user
// error, whose refusal would be exit 1) with no imports, and never lead to
// a create or an import.
func TestLookupsFailClosed(t *testing.T) {
	ctx := context.Background()
	spec := sandboxSpec(t)
	web := spec.Workflows["web"]
	for _, tc := range []struct {
		name, match string
		readiness   bool
	}{
		{"project", "/v1/projects/proj-1234", false},
		{"bucket", "/b/" + spec.Installation.RunsBucket, false},
		{"bucket policy", "/b/" + spec.Installation.RunsBucket + "/iam", false},
		{"secret", "/secrets/" + spec.Secrets["sandbox-probe"], false},
		{"secret policy", ":getIamPolicy", false},
		{"registry", "/repositories/" + spec.Registry.RepositoryID, false},
		{"account", "/serviceAccounts/" + web.ServiceAccountEmail, false},
		{"job", "/jobs/" + web.Job, false},
		{"tag", "/tags/latest", true},
		{"versions", "/versions", true},
		{"record", "/o/", true},
	} {
		for _, code := range []int{http.StatusForbidden, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s %d", tc.name, code), func(t *testing.T) {
				f := newCloud(t)
				ft := &failingTransport{}
				c, err := NewClients(ctx, f.options(&http.Client{Transport: ft}), f.endpoints())
				if err != nil {
					t.Fatal(err)
				}
				f.bootstrap(t, spec, legacyDisplay)
				check := func(err error) {
					t.Helper()
					var ue *UserError
					if err == nil || errors.As(err, &ue) {
						t.Errorf("err = %v; want a remote error", err)
					}
				}
				if !tc.readiness {
					ft.match, ft.code = tc.match, code
					im, _, err := DiscoverRepo(ctx, c, spec)
					check(err)
					if len(im.List) != 0 {
						t.Errorf("imports on a failed lookup: %v", im.List)
					}
					if tc.name == "project" || tc.name == "bucket" {
						im, err := DiscoverInstallation(ctx, c, installationSpec(t))
						check(err)
						if len(im.List) != 0 {
							t.Errorf("installation imports on a failed lookup: %v", im.List)
						}
					}
					return
				}
				_, ex, err := DiscoverRepo(ctx, c, spec)
				if err != nil {
					t.Fatal(err)
				}
				ft.match, ft.code = tc.match, code
				_, _, err = Readiness(ctx, c, spec, ex, false)
				check(err)
			})
		}
	}
}

func TestReadinessRefusesJobWithoutImage(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, legacyDisplay)
	f.run.SetJob(spec.Workflows["web"].Job, spec.Workflows["web"].Labels, "")
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	// The spec's image isn't built, and the job has none to keep: switching
	// to the spec's would point the job at a missing image.
	got, _, err := Readiness(ctx, f.c, spec, ex, false)
	if err == nil {
		t.Fatalf("readiness pointed a job with no image at %q", got.Workflows["web"].Image)
	}
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), spec.Workflows["web"].Job) {
		t.Errorf("err = %v; want a user error naming the job", err)
	}
}

// TestReadinessCloneJobWithoutImageNamesTheConfig (review fix): a checkout:
// clone workflow's job existing with no container image, and the local
// config lacking base_images.base too, must not interpolate an empty
// ws.Image into the hard-failure message — unlike a baked workflow's
// (TestReadinessRefusesJobWithoutImage), ws.Image can itself be empty here
// (there is no base_images.base to name), so the message must say what is
// actually missing instead of leaving a blank.
//
// Mutation (run, restore): revert the `what`/`fix` branch in Readiness back
// to the single `userErr(..., ws.Image, fix)` call it replaced — this test
// then fails: the message reads "...and  is not built yet..." (a double
// space, naming nothing).
func TestReadinessCloneJobWithoutImageNamesTheConfig(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	in := cloneInputs(t)
	in.LC.BaseImages = maps.Clone(in.LC.BaseImages)
	delete(in.LC.BaseImages, config.BaseKind)
	// A clone-only repository: since the 0.7.0 cut the baked api would
	// need base_images.base too (its daily check's base), so without it
	// Repo itself refuses before Readiness is reached. Only a clone-only
	// repository can still have no base_images.base at Readiness time.
	in.Cfg.Workflows = maps.Clone(in.Cfg.Workflows)
	delete(in.Cfg.Workflows, "api")
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Workflows["web"].Image != "" {
		t.Fatalf("fixture bug: web's image = %q, want empty (no base_images.%s)", rs.Workflows["web"].Image, config.BaseKind)
	}
	f.gcs.AddBucket(rs.Installation.RunsBucket, testProjectNumber, managed)
	for logical, id := range rs.Secrets {
		f.sm.Seed(id, with(managed, gcp.LabelRepo, rs.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	f.run.SetJob(rs.Workflows["web"].Job, rs.Workflows["web"].Labels, "")
	_, ex, err := DiscoverRepo(ctx, f.c, rs)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Readiness(ctx, f.c, rs, ex, false)
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a user error", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "and  is") || strings.Contains(msg, "  ") {
		t.Fatalf("malformed message (an empty %%s left a double space): %q", msg)
	}
	if !strings.Contains(msg, "base_images."+config.BaseKind) {
		t.Errorf("message does not name the missing config key: %q", msg)
	}
}

func TestReadinessOutputSharesNoMaps(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, legacyDisplay)
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	before := mustJSON(t, spec)
	got, _, err := Readiness(ctx, f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	w := got.Workflows["web"]
	w.Env["X"], w.SecretEnv["X"], w.Labels["x"], w.SecretIDs["x"] = "1", "1", "1", "1"
	w.BuildSecrets[0] = "changed"
	got.Secrets["x"] = "1"
	got.BuildSecrets[0] = "changed"
	got.Check.Env["X"], got.Check.SecretEnv["X"] = "1", "1"
	got.Check.Workflows[0] = "changed"
	if after := mustJSON(t, spec); string(after) != string(before) ||
		spec.Workflows["web"].Labels["x"] != "" || spec.Workflows["web"].SecretIDs["x"] != "" ||
		spec.Workflows["web"].BuildSecrets[0] == "changed" || spec.Check.Workflows[0] == "changed" {
		t.Error("editing Readiness's output changed its input spec")
	}
}

func TestScheduleStaysPausedWithoutWorkflows(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, legacyDisplay)
	_, ex, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	check := *spec.Check
	check.Workflows = nil
	spec.Check = &check
	got, _, err := Readiness(ctx, f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Check.Paused {
		t.Error("a check covering no workflow must stay paused")
	}
}

// --- Task 3: Readiness's repo-wins-else-project precedence ---------------

// projectSecretLC makes bitbucket-token (a reserved name, design §3) a
// project-wide secret acme/sandbox may use, so infra.Repo() computes a
// ProjectSecretCandidates entry for it.
const projectSecretLC = `project_secrets:
  bitbucket-token: { allow_data_to: [acme/sandbox] }
`

// noListProviderLC is a model provider with no allow_data_to at all: under
// §3a's no-list rule its key is mountable by every repository.
const noListProviderLC = `providers:
  no-list:
    kind: anthropic-compat
    base_url: https://nolist.example
    auth: bearer
    secret: no-list-key
    models: ["x/*"]
`

func projectSpec(t *testing.T, extraLC string) RepoSpec {
	t.Helper()
	rs, err := Repo(sandboxInputs(t, m5Additions+extraLC))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// seedProjectSecret stores the project copy of logical, correctly labelled,
// with one enabled version, and returns its ID.
func (f *cloud) seedProjectSecret(spec RepoSpec, logical string) string {
	id := spec.ProjectSecretCandidates[logical]
	f.sm.Seed(id, with(managed, gcp.LabelProject, spec.Installation.ProjectName, gcp.LabelSecret, logical), []byte("project-"+logical))
	return id
}

// resolve runs DiscoverRepo then Readiness against the same spec.
func readinessOf(t *testing.T, f *cloud, spec RepoSpec) (RepoSpec, []Missing, Existing) {
	t.Helper()
	_, ex, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	got, missing, err := Readiness(context.Background(), f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	return got, missing, ex
}

// Review focus 2: a repo copy with an enabled version wins, and the only
// trace of that is the ABSENCE of an out.ProjectSecrets entry (the field
// that reaches tfvars). An earlier, inverted draft populated it for every
// eligible name regardless of the repo's own version, and Terraform's
// merge() prefers the project map's value for a key in both -- so this
// asserts the map, never some other field that "looks like" the repo won.
func TestReadinessRepoVersionMeansNoProjectSecretsEntry(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.bootstrap(t, spec, legacyDisplay)
	f.seedProjectSecret(spec, "bitbucket-token")
	got, missing, _ := readinessOf(t, f, spec)
	if id, ok := got.ProjectSecrets["bitbucket-token"]; ok {
		t.Errorf("ProjectSecrets[bitbucket-token] = %q, want no entry: the repo copy has a version", id)
	}
	for _, m := range missing {
		if strings.Contains(m.Reason, "bitbucket-token") {
			t.Errorf("missing = %v, want none for a name the repo copy resolves", m)
		}
	}
}

func TestReadinessFallsBackToProjectWhenRepoHasNoVersion(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	// The repo copy exists but has no enabled version.
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	want := f.seedProjectSecret(spec, "bitbucket-token")
	got, missing, _ := readinessOf(t, f, spec)
	if id := got.ProjectSecrets["bitbucket-token"]; id != want {
		t.Errorf("ProjectSecrets[bitbucket-token] = %q, want the project copy %q", id, want)
	}
	for _, m := range missing {
		if strings.Contains(m.Reason, "bitbucket-token") {
			t.Errorf("missing = %v, want none: the project copy resolves", m)
		}
	}
}

func TestReadinessOmitsProjectSecretForUnlistedRepo(t *testing.T) {
	f := newCloud(t)
	// The allow-list names a different repository.
	spec := projectSpec(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/other] }\n")
	if _, ok := spec.ProjectSecretCandidates["bitbucket-token"]; ok {
		t.Fatal("an unlisted repository got a ProjectSecretCandidates entry")
	}
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	f.sm.Seed(gcp.ProjectSecretID(spec.Installation.ProjectName, "bitbucket-token"),
		with(managed, gcp.LabelProject, spec.Installation.ProjectName, gcp.LabelSecret, "bitbucket-token"), []byte("v"))
	got, missing, _ := readinessOf(t, f, spec)
	if id, ok := got.ProjectSecrets["bitbucket-token"]; ok {
		t.Errorf("ProjectSecrets[bitbucket-token] = %q, want no entry for an unlisted repository", id)
	}
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return strings.Contains(m.Reason, "fugaro secrets set bitbucket-token --repo acme/sandbox")
	}) {
		t.Errorf("missing = %v, want the fix to name --repo, never --project-wide", missing)
	}
}

// D2 (plan Task 2.1 step 3): the fix for a name the plan would resolve to
// the project copy is `fugaro secrets set <name>` -- the form that stores
// project-wide by default -- and never the repository-copy form, which would
// store a copy that then wins over the project one forever, nor the
// --project-wide synonym a person no longer has to know.
func TestReadinessMissingNamesProjectWideFix(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	// Neither copy has a version, and the repository IS a candidate.
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	got, missing, _ := readinessOf(t, f, spec)
	if id, ok := got.ProjectSecrets["bitbucket-token"]; ok {
		t.Errorf("ProjectSecrets[bitbucket-token] = %q, want no entry: nothing resolves yet", id)
	}
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return strings.Contains(m.Reason, "fugaro secrets set bitbucket-token") && !strings.Contains(m.Reason, "--repo")
	}) {
		t.Errorf("missing = %v, want the project-scope fix (secrets set, no --repo)", missing)
	}
}

// design §5/P3: the fallback happens on the NEXT apply, never within one.
// The fake cannot disable a version in place, so the second call uses a
// fresh fake holding the same spec with the repo copy unversioned.
func TestReadinessFallsBackOnceRepoVersionGone(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), []byte("v"))
	want := f.seedProjectSecret(spec, "bitbucket-token")
	if got, _, _ := readinessOf(t, f, spec); got.ProjectSecrets["bitbucket-token"] != "" {
		t.Errorf("first call: ProjectSecrets = %v, want no entry while the repo copy is live", got.ProjectSecrets)
	}
	g := newCloud(t)
	g.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	g.seedProjectSecret(spec, "bitbucket-token")
	if got, _, _ := readinessOf(t, g, spec); got.ProjectSecrets["bitbucket-token"] != want {
		t.Errorf("second call: ProjectSecrets[bitbucket-token] = %q, want %q once the repo version is gone", got.ProjectSecrets["bitbucket-token"], want)
	}
}

// P7: a provider with no allow_data_to is a candidate for a repository its
// config names nowhere, and resolves to the project copy.
func TestReadinessProviderNoListEligibleForEveryRepo(t *testing.T) {
	in := sandboxInputs(t, m5Additions+noListProviderLC)
	in.Cfg.Agent.Auth = "api-key"
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	f := newCloud(t)
	f.sm.Seed(spec.Secrets["no-list-key"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "no-list-key"), nil)
	want := f.seedProjectSecret(spec, "no-list-key")
	got, _, _ := readinessOf(t, f, spec)
	if id := got.ProjectSecrets["no-list-key"]; id != want {
		t.Errorf("ProjectSecrets[no-list-key] = %q, want the project copy %q (no list means every repository)", id, want)
	}
}

// P7, the non-empty-list case, routed through ProviderBySecret +
// ModelProvider.AllowsData (not AllowedForProjectSecret, which reads an
// entirely different map): no candidate entry at all.
func TestReadinessProviderListedExcludesUnlistedRepo(t *testing.T) {
	in := sandboxInputs(t, m5Additions+providerLC)
	in.Cfg.Agent.Auth = "api-key"
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.ProjectSecretCandidates["other-key"]; ok {
		t.Error("a provider listing another repository got a candidate entry for acme/sandbox")
	}
	if _, ok := spec.ProjectSecretCandidates["openrouter-api-key"]; !ok {
		t.Error("a provider listing acme/sandbox got no candidate entry")
	}
}

// ProjectSecretCandidate is exported and called directly by the CLI's
// secrets stage and doctor, not only through Repo's candidate map, so its
// provider (and named-account) gate has to hold by itself. The map-based
// tests above would still pass if this function answered true for
// everything: SecretMounts never asks about a name the repository cannot
// mount. A mutation to `eligible = lc.Providers[name].Secret != ""` is what
// this test exists to kill.
// D2 (plan Task 2.1 step 3b, finding 13): a reserved name is a project-wide
// candidate with no project_secrets entry at all, which is the case this
// function refused before -- only an entry reached it, and that is exactly
// the set D2 sweeps in. The bare claude-oauth-token (the default account's
// own, phase 5's) and a repository's own application secret are not swept in,
// and an entry the owner wrote still decides.
// bitbucket-token is the reserved name the sweep deliberately leaves out: its
// credential is issued for one repository, so it needs the owner's entry
// (config.PerRepositorySecret, owner ruling of 2026-10-15).
func TestProjectSecretCandidateAcceptsAReservedNameWithNoList(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions) // no project_secrets, no providers, no claude_accounts
	for _, name := range []string{"anthropic-api-key", "github-app-key"} {
		id, ok := ProjectSecretCandidate(lc, name, "acme/sandbox")
		if !ok || id != gcp.ProjectSecretID(lc.Name, name) {
			t.Errorf("ProjectSecretCandidate(%q) = %q, %v; want %q, true", name, id, ok, gcp.ProjectSecretID(lc.Name, name))
		}
	}
	for _, name := range []string{"bitbucket-token", "claude-oauth-token", "sandbox-probe"} {
		if id, ok := ProjectSecretCandidate(lc, name, "acme/sandbox"); ok {
			t.Errorf("ProjectSecretCandidate(%q) = %q, true; want not a candidate", name, id)
		}
	}
	// An entry the owner wrote is still the owner's answer: a reserved name
	// allow-listed for another repository is not a candidate for this one.
	listed := parseLC(t, m4LocalConfig+m5Additions+"project_secrets:\n  bitbucket-token: { allow_data_to: [acme/other] }\n")
	if id, ok := ProjectSecretCandidate(listed, "bitbucket-token", "acme/sandbox"); ok {
		t.Errorf("ProjectSecretCandidate = %q, true; want not a candidate: the entry names acme/other", id)
	}
	if _, ok := ProjectSecretCandidate(listed, "bitbucket-token", "acme/other"); !ok {
		t.Error("the entry's own repository is not a candidate")
	}
}

func TestProjectSecretCandidateGate(t *testing.T) {
	const accounts = `claude_accounts:
  acme-personal:
    allow_data_to: [acme/sandbox]
  acme-other:
    allow_data_to: [acme/other]
`
	const wildcard = `providers:
  any-repo:
    kind: anthropic-compat
    base_url: https://any.example
    auth: bearer
    secret: any-key
    models: ["x/*"]
    allow_data_to: ["*"]
`
	tests := []struct {
		name    string
		lc      string
		logical string
		repo    string
		want    bool
	}{
		{"provider lists this repository", providerLC, "openrouter-api-key", "acme/sandbox", true},
		{"provider lists another repository", providerLC, "openrouter-api-key", "acme/other", false},
		{"provider lists another repository, asked by it", providerLC, "other-key", "acme/other", true},
		{"provider with no list", noListProviderLC, "no-list-key", "acme/other", true},
		{"provider with the wildcard", wildcard, "any-key", "acme/other", true},
		{"named account allows this repository", accounts, "claude-oauth-token-acme-personal", "acme/sandbox", true},
		{"named account excludes this repository", accounts, "claude-oauth-token-acme-personal", "acme/other", false},
		{"project_secrets entry names this repository", projectSecretLC, "bitbucket-token", "acme/sandbox", true},
		{"project_secrets entry names another repository", projectSecretLC, "bitbucket-token", "acme/other", false},
		{"a name nothing configures", providerLC, "sandbox-probe", "acme/sandbox", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := parseLC(t, m4LocalConfig+m5Additions+tt.lc)
			id, ok := ProjectSecretCandidate(lc, tt.logical, tt.repo)
			if ok != tt.want {
				t.Fatalf("ProjectSecretCandidate(%q, %q) ok = %v, want %v", tt.logical, tt.repo, ok, tt.want)
			}
			if !tt.want {
				if id != "" {
					t.Errorf("ProjectSecretCandidate(%q, %q) id = %q, want %q with ok false", tt.logical, tt.repo, id, "")
				}
				return
			}
			if want := gcp.ProjectSecretID(lc.Name, tt.logical); id != want {
				t.Errorf("ProjectSecretCandidate(%q, %q) id = %q, want %q", tt.logical, tt.repo, id, want)
			}
		})
	}
}

// B4: the check job's own secret_env goes through the identical resolution.
func TestReadinessCheckJobResolvesProjectSecretsToo(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	if spec.Check == nil {
		t.Fatal("the sandbox fixture has no check job")
	}
	for _, logical := range spec.Check.SecretEnv {
		if logical == "bitbucket-token" {
			f.sm.Seed(spec.Secrets[logical], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), nil)
			continue
		}
		f.sm.Seed(spec.Secrets[logical], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	want := f.seedProjectSecret(spec, "bitbucket-token")
	got, missing, _ := readinessOf(t, f, spec)
	if id := got.ProjectSecrets["bitbucket-token"]; id != want {
		t.Errorf("ProjectSecrets[bitbucket-token] = %q, want %q", id, want)
	}
	if !got.Check.DeployJob {
		t.Errorf("the check job is not deployed: %v", missing)
	}
}

// P7a/B5(a): an existing job never carries an unresolvable secret_env
// entry, and the runner's redaction list is recomputed from the
// post-omission set. This fails if the mount is left dangling (a
// secret_key_ref at a version-less secret) or if the omission is made but
// FUGARO_SECRET_ENVS still names the variable.
func TestReadinessOmitsUnresolvableMountFromExistingJob(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.bootstrap(t, spec, legacyDisplay)
	// No version anywhere: the repo copy is unversioned and no project
	// copy exists.
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	env := config.ReservedSecrets["bitbucket-token"]
	got, missing, _ := readinessOf(t, f, spec)
	ws := got.Workflows["web"]
	if _, ok := ws.SecretEnv[env]; ok {
		t.Errorf("the existing job still mounts %s: %v", env, ws.SecretEnv)
	}
	if _, ok := ws.SecretIDs["bitbucket-token"]; ok {
		t.Errorf("the existing job still names the unresolvable secret: %v", ws.SecretIDs)
	}
	if !ws.DeployJob {
		t.Error("DeployJob must stay true: job.tf's count would destroy the job outright")
	}
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return m.Kind == MissingSecret && m.Deployed && strings.Contains(m.Reason, "bitbucket-token")
	}) {
		t.Errorf("missing = %v, want a Deployed MissingSecret for bitbucket-token", missing)
	}
	if strings.Contains(ws.Env[runner.SecretEnvsVar], env) {
		t.Errorf("%s = %q, still names %s: the redaction list must be recomputed", runner.SecretEnvsVar, ws.Env[runner.SecretEnvsVar], env)
	}
}

// P7a for the check job (readiness.go's own branch, review L3): the check
// job runs as the build account from the base image, so an unresolvable entry
// left in its secret_env points secret_key_ref at a secret with zero
// versions, and its daily rebuild fails with no way to see why. The workflow
// jobs' half of P7a is the test above; this one fails if the check job's
// branch is disabled, which no test caught before.
func TestReadinessOmitsUnresolvableMountFromExistingCheckJob(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.bootstrap(t, spec, legacyDisplay)
	f.m5(t, spec) // the check job exists
	var gitEnv string
	for env, logical := range spec.Check.SecretEnv {
		if logical == "bitbucket-token" {
			gitEnv = env
		}
	}
	if gitEnv == "" {
		t.Fatalf("the sandbox fixture's check job mounts no bitbucket-token: %v", spec.Check.SecretEnv)
	}
	// No version anywhere: the repo copy is unversioned and no project copy
	// exists.
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	got, missing, _ := readinessOf(t, f, spec)
	if _, ok := got.Check.SecretEnv[gitEnv]; ok {
		t.Errorf("the existing check job still mounts %s: %v", gitEnv, got.Check.SecretEnv)
	}
	if env := config.ReservedSecrets["bitbucket-token"]; env != gitEnv {
		t.Errorf("the git credential's env name is %s, not %s", env, gitEnv)
	}
	if !got.Check.DeployJob {
		t.Error("the check job must stay deployed: check.tf's count would destroy it outright")
	}
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return m.Check && m.Kind == MissingSecret && m.Deployed && strings.Contains(m.Reason, "bitbucket-token")
	}) {
		t.Errorf("missing = %v, want a Deployed MissingSecret for the check job", missing)
	}
	if strings.Contains(got.Check.Env[runner.SecretEnvsVar], gitEnv) {
		t.Errorf("%s = %q, still names %s: the redaction list must be recomputed", runner.SecretEnvsVar, got.Check.Env[runner.SecretEnvsVar], gitEnv)
	}
}

func TestReadinessNewJobStillRefusesOnUnresolvableMount(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	got, _, _ := readinessOf(t, f, spec)
	if got.Workflows["web"].DeployJob {
		t.Error("a brand-new job must not deploy while a mount is unresolved")
	}
}

// N4: a correctly-labelled secret the current config no longer computes is
// unioned back into the tfvars-reaching Secrets map, so Terraform's
// for_each never shrinks (prevent_destroy would fail the plan outright).
func TestReadinessUnionsExtraSecretsNeverShrinksRsSecrets(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, "")
	if _, ok := spec.Secrets["openrouter-api-key"]; ok {
		t.Fatal("the fixture already computes openrouter-api-key")
	}
	id := gcp.SecretID(spec.Slug, "openrouter-api-key")
	f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "openrouter-api-key"), []byte("v"))
	got, _, ex := readinessOf(t, f, spec)
	if ex.ExtraSecrets["openrouter-api-key"] != id {
		t.Errorf("ExtraSecrets = %v, want openrouter-api-key → %s", ex.ExtraSecrets, id)
	}
	if got.Secrets["openrouter-api-key"] != id {
		t.Errorf("out.Secrets = %v, want the live container unioned back", got.Secrets)
	}
	// The mount is still governed by SecretMounts's own, config-driven
	// output: the container stays, the mount does not come back.
	if _, ok := got.Workflows["web"].SecretEnv[config.ProviderKeyEnv("openrouter-api-key")]; ok {
		t.Errorf("the union re-mounted a name the config no longer computes: %v", got.Workflows["web"].SecretEnv)
	}
}

// Plan follow-up 3 (review M1): an extra secret is not only unioned into the
// plan's Secrets (the test above) but imported and its live policy read,
// exactly as the secrets the config still computes are. Without the import
// the next apply tries to create a container that already exists: the 409
// that re-running init after `fugaro forget` (or with any missing repo
// state) would hit. Without the policy, the extra-accessors note sees
// nothing for the name, so a grant that outlives the config that made it
// goes unsaid.
func TestDiscoverRepoImportsExtraSecretsAndNotesTheirGrant(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, "")
	const logical = "openrouter-api-key"
	if _, ok := spec.Secrets[logical]; ok {
		t.Fatalf("the fixture already computes %s", logical)
	}
	id := gcp.SecretID(spec.Slug, logical)
	f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	member := saMember(spec.Workflows["web"].ServiceAccountEmail)
	f.sm.SetPolicy(id, []gcpfake.Binding{{Role: gcp.SecretAccessorRole, Members: []string{member}}})
	im, ex, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	if ex.ExtraSecrets[logical] != id {
		t.Fatalf("ExtraSecrets = %v, want %s → %s", ex.ExtraSecrets, logical, id)
	}
	want := fmt.Sprintf("module.repo.google_secret_manager_secret.this[%q]", logical)
	if !slices.ContainsFunc(im.List, func(i Import) bool {
		return i.To == want && i.ID == "projects/"+spec.GCPProject+"/secrets/"+id
	}) {
		t.Errorf("imports = %+v, want %s at the live ID %s", im.List, want, id)
	}
	if !slices.ContainsFunc(im.Notes, func(n string) bool { return strings.Contains(n, id) && strings.Contains(n, member) }) {
		t.Errorf("notes = %q, want one naming %s and its accessor %s", im.Notes, id, member)
	}
}

// SF7: a secret at the candidate's derived ID that is not ours is refused,
// the same path an unmarked repo secret already takes.
func TestReadinessRefusesAccidentalLabelMismatch(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	id := gcp.ProjectSecretID(spec.Installation.ProjectName, "bitbucket-token")
	f.sm.Seed(id, with(managed, gcp.LabelSecret, "bitbucket-token"), []byte("v")) // no fugaro_project
	_, _, err := DiscoverRepo(context.Background(), f.c, spec)
	if err == nil || !strings.Contains(err.Error(), "project secret") {
		t.Fatalf("DiscoverRepo = %v, want a refusal naming the project secret", err)
	}
}

func TestReadinessResolvesCorrectlyLabelledProjectSecret(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	f.seedProjectSecret(spec, "bitbucket-token")
	_, ex, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !ex.ProjectSecretsOK["bitbucket-token"] {
		t.Errorf("ProjectSecretsOK = %v, want bitbucket-token true", ex.ProjectSecretsOK)
	}
}

// installationSpecWith is installationSpec for a local config with extra
// blocks (project_secrets, providers, claude_accounts).
func installationSpecWith(t *testing.T, extraLC string) InstallationSpec {
	t.Helper()
	s, err := Installation(parseLC(t, m4LocalConfig+m5Additions+extraLC), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// projectSecretLCBlock makes bitbucket-token a project-wide secret the
// sandbox repository may use, so Installation() computes it.
const projectSecretLCBlock = `project_secrets:
  bitbucket-token: { allow_data_to: [acme/sandbox] }
`

// TestDiscoverInstallationImportsPreExistingProjectSecret (S2): a project
// secret stored with --project-wide before the installation root's first
// apply is adopted, not conflicted with, and reaches the map the root plans.
func TestDiscoverInstallationImportsPreExistingProjectSecret(t *testing.T) {
	f := newCloud(t)
	inst := installationSpecWith(t, projectSecretLCBlock)
	id := gcp.ProjectSecretID(inst.FugaroProject, "bitbucket-token")
	f.sm.Seed(id, with(managed, gcp.LabelProject, inst.FugaroProject, gcp.LabelSecret, "bitbucket-token"), []byte("project-value"))
	im, err := DiscoverInstallation(context.Background(), f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	addr := `module.installation.google_secret_manager_secret.project["bitbucket-token"]`
	if got := importMap(im)[addr]; got != "projects/proj-1234/secrets/"+id {
		t.Errorf("imports = %v, want an import of %s at %s", importMap(im), id, addr)
	}
	if im.ProjectSecrets["bitbucket-token"] != id {
		t.Errorf("ProjectSecrets = %v, want bitbucket-token -> %s", im.ProjectSecrets, id)
	}
}

// TestDiscoverInstallationRefusesForeignProjectSecret (S2): a secret exists
// at the computed project ID but its labels aren't ours -- refused, never
// silently adopted or overwritten.
func TestDiscoverInstallationRefusesForeignProjectSecret(t *testing.T) {
	f := newCloud(t)
	inst := installationSpecWith(t, projectSecretLCBlock)
	id := gcp.ProjectSecretID(inst.FugaroProject, "bitbucket-token")
	f.sm.Seed(id, with(managed, gcp.LabelProject, "some-other-project", gcp.LabelSecret, "bitbucket-token"), []byte("someone-elses"))
	_, err := DiscoverInstallation(context.Background(), f.c, inst)
	if err == nil || !strings.Contains(err.Error(), "project secret "+id) {
		t.Fatalf("err = %v, want a foreign-project-secret refusal", err)
	}
}

// TestDiscoverInstallationNeverShrinksProjectSecrets (N3 BLOCKER): a name
// eligible on the first call, discovered live, must still be in the map on a
// second call whose config no longer makes it eligible at all -- the exact
// property P2 gives the repo module, extended here. Otherwise
// google_secret_manager_secret.project's for_each shrinks and
// prevent_destroy fails the plan itself.
func TestDiscoverInstallationNeverShrinksProjectSecrets(t *testing.T) {
	f := newCloud(t)
	first := installationSpecWith(t, projectSecretLCBlock)
	id := gcp.ProjectSecretID(first.FugaroProject, "bitbucket-token")
	f.sm.Seed(id, with(managed, gcp.LabelProject, first.FugaroProject, gcp.LabelSecret, "bitbucket-token"), []byte("project-value"))
	im, err := DiscoverInstallation(context.Background(), f.c, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := im.ProjectSecrets["bitbucket-token"]; !ok {
		t.Fatalf("first call: ProjectSecrets = %v, want bitbucket-token", im.ProjectSecrets)
	}
	// The second operator's config: the same installation, no
	// project_secrets entry at all (the list was emptied, or the entry
	// removed).
	second := installationSpec(t)
	if len(second.ProjectSecrets) != 0 {
		t.Fatalf("the second spec computes %v, want nothing", second.ProjectSecrets)
	}
	im2, err := DiscoverInstallation(context.Background(), f.c, second)
	if err != nil {
		t.Fatal(err)
	}
	for name := range im.ProjectSecrets {
		if _, ok := im2.ProjectSecrets[name]; !ok {
			t.Errorf("the second call dropped %s: %v is not a superset of %v", name, im2.ProjectSecrets, im.ProjectSecrets)
		}
	}
}

// TestDiscoverInstallationSecondOperatorMissingEntryDoesNotShrink (S5): a
// local config with no project_secrets/providers/claude_accounts entries at
// all, run against an installation where another operator's config already
// created project secrets, still plans every one the live discovery finds.
func TestDiscoverInstallationSecondOperatorMissingEntryDoesNotShrink(t *testing.T) {
	f := newCloud(t)
	inst := installationSpec(t)
	// Two project secrets a first operator's config created, live.
	ids := map[string]string{}
	for _, logical := range []string{"bitbucket-token", "claude-oauth-token"} {
		id := gcp.ProjectSecretID(inst.FugaroProject, logical)
		f.sm.Seed(id, with(managed, gcp.LabelProject, inst.FugaroProject, gcp.LabelSecret, logical), []byte("v"))
		ids[logical] = id
	}
	im, err := DiscoverInstallation(context.Background(), f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	for logical, id := range ids {
		if im.ProjectSecrets[logical] != id {
			t.Errorf("ProjectSecrets[%s] = %q, want %s (map %v)", logical, im.ProjectSecrets[logical], id, im.ProjectSecrets)
		}
	}
}

// A live secret carrying this installation's labels but sitting at an ID the
// plan does not derive for its logical name is neither adopted nor deleted
// (liveProjectSecrets's own check, plan follow-up 2): adopting it cannot work
// -- the resource the plan manages is the derived ID -- and deleting it is
// not ours to do. Without the check, it would be imported under
// prevent_destroy and managed by the installation, so a mutation to `if
// false` there used to pass every test.
func TestDiscoverInstallationLeavesAMisnamedProjectSecretAlone(t *testing.T) {
	f := newCloud(t)
	inst := installationSpec(t)
	want := gcp.ProjectSecretID(inst.FugaroProject, "bitbucket-token")
	f.sm.Seed(want+"9", with(managed, gcp.LabelProject, inst.FugaroProject, gcp.LabelSecret, "bitbucket-token"), []byte("v"))
	im, err := DiscoverInstallation(context.Background(), f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := im.ProjectSecrets["bitbucket-token"]; ok {
		t.Errorf("ProjectSecrets[bitbucket-token] = %q, want no entry: the live secret is not at the derived %s", id, want)
	}
	addr := `module.installation.google_secret_manager_secret.project["bitbucket-token"]`
	if got := importMap(im)[addr]; got != "" {
		t.Errorf("imports[%s] = %q, want no import of a secret that is not the derived %s", addr, got, want)
	}
	if joined := strings.Join(im.Notes, "\n"); !strings.Contains(joined, "not the derived") || !strings.Contains(joined, want) {
		t.Errorf("Notes = %v, want one naming the derived ID %s", im.Notes, want)
	}
}

// TestReadinessVarsIncludeProjectSecrets (SF2's gap): a RepoSpec field with a
// correct json tag that nothing threads through RepoVars is invisible from Go
// unless this asserts the SERIALIZED output. A project copy with an enabled
// version and a repo copy without one resolves to the project container, and
// that has to reach the tfvars the repo root plans.
func TestReadinessVarsIncludeProjectSecrets(t *testing.T) {
	f := newCloud(t)
	spec := projectSpec(t, projectSecretLC)
	// The repo copy exists but has no enabled version, so the project copy wins.
	f.sm.Seed(spec.Secrets["bitbucket-token"], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, "bitbucket-token"), nil)
	want := f.seedProjectSecret(spec, "bitbucket-token")
	got, _, _ := readinessOf(t, f, spec)
	data, err := RepoVars(got)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Repo struct {
			ProjectSecrets map[string]string `json:"project_secrets"`
		} `json:"repo"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Repo.ProjectSecrets["bitbucket-token"] != want {
		t.Errorf("RepoVars project_secrets = %v, want bitbucket-token -> %s\n%s", doc.Repo.ProjectSecrets, want, data)
	}
}

// TestProjectSecretsFixtureIsTheGoldenPlusOneKey pins the hand-maintained
// Terraform fixture (deploy/terraform/gcp/roots/repo/tests/testdata/
// project-secrets.tfvars.json, see its README): it must stay exactly the
// golden tfvars plus one repo.project_secrets entry, so regenerating the
// golden for an unrelated reason cannot let the fixture drift silently.
func TestProjectSecretsFixtureIsTheGoldenPlusOneKey(t *testing.T) {
	dir := "../../deploy/terraform/gcp/roots/repo/tests/testdata/"
	golden, err := os.ReadFile(dir + "bitbucket-oauth.tfvars.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(dir + "project-secrets.tfvars.json")
	if err != nil {
		t.Fatal(err)
	}
	decode := func(b []byte) map[string]any {
		t.Helper()
		var doc map[string]any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	want, got := decode(golden), decode(fixture)
	repo, ok := want["repo"].(map[string]any)
	if !ok {
		t.Fatal("the golden has no repo object")
	}
	repo["project_secrets"] = map[string]any{"bitbucket-token": gcp.ProjectSecretID("aurora", "bitbucket-token")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("project-secrets.tfvars.json is not bitbucket-oauth.tfvars.json plus the one project_secrets key:\n got %v\nwant %v", got, want)
	}
}
