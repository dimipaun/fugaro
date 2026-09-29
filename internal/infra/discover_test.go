package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
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
	c     *Clients
}

func newCloud(t *testing.T) *cloud {
	t.Helper()
	f := &cloud{
		iam: gcpfake.NewIAM(t), ar: gcpfake.NewArtifactRegistry(t), run: gcpfake.NewRun(t),
		sm: gcpfake.NewSecrets(t), gcs: gcpfake.NewGCS(t), crm: gcpfake.NewCRM(t),
		logs: gcpfake.NewLogging(t), sched: gcpfake.NewScheduler(t),
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

// options are the Google API options of the fakes, with hc (when set) as
// the HTTP client.
func (f *cloud) options(hc *http.Client) gcp.Options {
	return gcp.Options{Project: "proj-1234", Region: "us-east5", HTTPClient: hc, Endpoints: gcp.Endpoints{
		Run: f.run.URL + "/", SecretManager: f.sm.URL + "/", Logging: f.logs.URL + "/", NoAuth: true}}
}

func (f *cloud) endpoints() Endpoints {
	return Endpoints{IAM: f.iam.URL + "/", ArtifactRegistry: f.ar.URL + "/", Storage: f.gcs.URL + "/storage/v1/",
		ResourceManager: f.crm.URL + "/", Scheduler: f.sched.URL + "/"}
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
		f.iam.AddServiceAccount(spec.Project, ws.ServiceAccountEmail, display(ws))
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
	f.ar.AddRepository(spec.Project, spec.Region, spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	f.iam.AddServiceAccount(spec.Project, spec.BuildServiceAccountEmail, spec.BuildServiceAccount.DisplayName)
	if spec.Check != nil {
		f.run.SetJob(spec.Check.Job, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelRole, gcp.RoleCheck), spec.Check.Image)
		f.sched.SetJob(spec.Project, spec.Check.SchedulerRegion, spec.Check.SchedulerJob, checkRunURI(spec), spec.Installation.SchedulerServiceAccount)
	}
}

// m5Installation adds what an earlier M5 installation apply made, which
// the rollback's state rm leaves in place: the runs bucket, the base
// registry, the custom roles, the scheduler account and the log bucket.
func (f *cloud) m5Installation(inst InstallationSpec) {
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, managed)
	f.ar.AddRepository(inst.Project, inst.Region, BaseRegistry, managed)
	f.iam.AddRole(inst.Project, RoleLauncher, "Fugaro launcher", false)
	f.iam.AddRole(inst.Project, RoleJobRunner, "Fugaro job runner", false)
	f.iam.AddRole(inst.Project, RoleBuildSubmitter, "Fugaro build submitter", false)
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
	f.iam.AddServiceAccount(spec.Project, spec.BuildServiceAccountEmail, gcp.JobSADisplayName(spec.Slug, "web"))
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
	got, _, err := Readiness(context.Background(), f.c, spec, ex)
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

// After a rollback (state rm), every installation resource is still
// there; a retried fugaro init imports the custom roles, the scheduler
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
		"module.installation.google_project_iam_custom_role.launcher":        p + "roles/" + RoleLauncher,
		"module.installation.google_project_iam_custom_role.job_runner":      p + "roles/" + RoleJobRunner,
		"module.installation.google_project_iam_custom_role.build_submitter": p + "roles/" + RoleBuildSubmitter,
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

// After init --repo --forget, the repository's Scheduler job is still
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
			f.sched.SetJob(spec.Project, spec.Check.SchedulerRegion, spec.Check.SchedulerJob, "https://example.com/", spec.Installation.SchedulerServiceAccount)
		},
		"account": func(f *cloud) {
			f.sched.SetJob(spec.Project, spec.Check.SchedulerRegion, spec.Check.SchedulerJob, checkRunURI(spec), "x@proj-1234.iam.gserviceaccount.com")
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
	got, missing, err := Readiness(ctx, f.c, spec, ex)
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
	got, _, err := Readiness(ctx, f.c, spec, ex)
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
	got, missing, err := Readiness(ctx, f.c, spec, ex)
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
		got, missing, err := Readiness(ctx, f.c, spec, ex)
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

// The check job mounts the provider credential at latest, which Cloud Run
// checks when it creates the job, so like a workflow job it waits until
// the credential has a version; the invoker grant and the Scheduler job
// wait with it. A check job that exists stays, whatever its secrets.
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
		got, missing, err := Readiness(ctx, f.c, spec, ex)
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

	// A fresh repository: init --repo has just made the secrets, empty.
	f := newCloud(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	f.seedVersions(spec)
	got, missing := gate(f)
	if got.Check.DeployJob || len(missing) != 1 || missing[0].Kind != MissingSecret || missing[0].Deployed {
		t.Fatalf("an empty credential: deploy %v, missing %+v; want no check job, and the credential missing", got.Check.DeployJob, missing)
	}
	want := "secret " + creds[0] + " has no version: fugaro secrets set " + creds[0] + " --repo acme/sandbox"
	if s := missing[0].String(); !strings.Contains(s, "daily image check") || !strings.Contains(s, "not deployed yet") || !strings.Contains(s, want) {
		t.Errorf("missing = %q", s)
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
	got, _, err := Readiness(ctx, f.c, spec, ex)
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
	got, _, err = Readiness(ctx, f.c, spec, ex)
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
				_, _, err = Readiness(ctx, c, spec, ex)
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
	got, _, err := Readiness(ctx, f.c, spec, ex)
	if err == nil {
		t.Fatalf("readiness pointed a job with no image at %q", got.Workflows["web"].Image)
	}
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), spec.Workflows["web"].Job) {
		t.Errorf("err = %v; want a user error naming the job", err)
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
	got, _, err := Readiness(ctx, f.c, spec, ex)
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
	got, _, err := Readiness(ctx, f.c, spec, ex)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Check.Paused {
		t.Error("a check covering no workflow must stay paused")
	}
}
