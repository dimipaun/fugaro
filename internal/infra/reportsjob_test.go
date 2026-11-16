package infra

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/task"
)

// reportsLocal is the local config the reports job's tests are about: one
// repository per provider, one the config records no provider for, and one
// spelled twice in the two cases the fold makes equal (task.CanonicalRepo), so
// the document's repository list is asserted against a set the config does not
// hand over as a list.
const reportsLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
registry_host: us-east5-docker.pkg.dev/proj-1234
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
repos:
  acme/APP: { provider: bitbucket, workflows: [web] }
  acme/app: { provider: bitbucket, workflows: [web] }
  acme/none: { workflows: [web] }
  acme/other: { provider: github, github_app_id: "12345", workflows: [app] }
`

// reportsNode is the reports key of the installation tfvars the options render
// for yaml, decoded: the node is the contract between this program and the
// module, so what the tests read is the document Terraform is handed rather
// than the spec it was built from.
func reportsNode(t *testing.T, yaml string, o InstallOptions) map[string]json.RawMessage {
	t.Helper()
	spec, err := Installation(parseLC(t, yaml), o)
	if err != nil {
		t.Fatalf("Installation: %v", err)
	}
	if spec.Reports == nil {
		t.Fatal("the installation records a base image and plans no reports job: an installation at this release holds reports/ with nobody to write it")
	}
	vars, err := InstallationVars(spec)
	if err != nil {
		t.Fatalf("InstallationVars: %v", err)
	}
	var doc struct {
		Reports map[string]json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatalf("the installation tfvars: %v", err)
	}
	if len(doc.Reports) == 0 {
		t.Fatal("the installation tfvars carry no reports node: the module plans no job, no account and no schedule")
	}
	return doc.Reports
}

// rawOf is one member of a node, with the failure of a member that is not
// there named for the claim that needs it.
func rawOf(t *testing.T, node map[string]json.RawMessage, key string) json.RawMessage {
	t.Helper()
	raw, ok := node[key]
	if !ok {
		t.Fatalf("the reports node carries no %s key: %v", key, slices.Sorted(maps.Keys(node)))
	}
	return raw
}

// reportsEnv is the node's environment, decoded.
func reportsEnv(t *testing.T, node map[string]json.RawMessage) map[string]string {
	t.Helper()
	var env map[string]string
	if err := json.Unmarshal(rawOf(t, node, "env"), &env); err != nil {
		t.Fatalf("reports.env: %v", err)
	}
	return env
}

// reportsJobDoc is the assignment the node's environment carries, decoded
// through the same type the environment is rendered from.
func reportsJobDoc(t *testing.T, env map[string]string) ReportsJobDoc {
	t.Helper()
	raw := env[ReportsJobDocEnv]
	if raw == "" {
		t.Fatalf("the job's environment carries no %s: a job deployed without it can only fail, once a night, unnoticed until the alert fires", ReportsJobDocEnv)
	}
	var doc ReportsJobDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%s: %v", ReportsJobDocEnv, err)
	}
	return doc
}

// TestUpdatePlansTheReportsJobPaused: `fugaro update` renders the node for
// every installation that records a base image to run the job FROM, and the
// schedule it renders is stopped. The job ships paused (E4), and the one thing
// that starts it is the project layer's jobs.reports.paused (R13), which is a
// setting the owner writes and this run reads: a `gcloud scheduler jobs resume`
// would be reverted by the next update, because the module always applies
// paused.
func TestUpdatePlansTheReportsJobPaused(t *testing.T) {
	node := reportsNode(t, reportsLocal, InstallOptions{})
	var paused bool
	if err := json.Unmarshal(rawOf(t, node, "paused"), &paused); err != nil {
		t.Fatalf("reports.paused: %v", err)
	}
	if !paused {
		t.Error("an option set that names no layer started the reports schedule: the loosening direction is the operator's alone, and a caller that read no layer must leave the schedule stopped")
	}

	// The spec's own names and image, beside the node: the module validates
	// each of them, and a name or an account that drifted here would be a job
	// this program believes it deployed.
	spec, err := Installation(parseLC(t, reportsLocal), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Reports.AccountID != tf.ReportsAccountID {
		t.Errorf("account_id = %q, want %q: the plan review reads that ID as this job's own kind of principal", spec.Reports.AccountID, tf.ReportsAccountID)
	}
	if spec.Reports.Job != ReportsJobName || spec.Reports.SchedulerJob != ReportsSchedulerJobName {
		t.Errorf("the job is %q and its schedule %q, want %q and %q", spec.Reports.Job, spec.Reports.SchedulerJob, ReportsJobName, ReportsSchedulerJobName)
	}
	if want := parseLC(t, reportsLocal).BaseImage("base"); spec.Reports.Image != want {
		t.Errorf("image = %q, want the installation's recorded base image %q: the job runs FROM it, and a guessed image would write reports from whatever the guess names", spec.Reports.Image, want)
	}

	// The assignment itself: the job runs with no checkout and no local config
	// on its machine — it runs in the project, over the runs bucket — so the
	// project, the bucket, the repositories and the periods it closes have to
	// be in the document, and nothing else about the installation is handed to
	// it.
	doc := reportsJobDoc(t, reportsEnv(t, node))
	if doc.Version != ReportsJobDocVersion {
		t.Errorf("version = %d, want %d", doc.Version, ReportsJobDocVersion)
	}
	if doc.Project != "proj-1234" || doc.Bucket != "fugaro-runs-proj-1234" {
		t.Errorf("the document names the project %q and the bucket %q, want the installation's own", doc.Project, doc.Bucket)
	}
	slugs := func(pairs ...string) []string {
		t.Helper()
		out := make([]string, 0, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			slug, err := task.Slug(pairs[i], pairs[i+1])
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, slug)
		}
		slices.Sort(out)
		return out
	}
	// acme/none has no provider kind in the config, and the job cannot read
	// one off fugaro.yaml: it is a repository the job could not address (the
	// slug is the provider and the name together), so a run leaves it out
	// rather than writing a report it cannot name. The two spellings of
	// acme/app are one repository and derive one slug between them.
	want := slugs("bitbucket", "acme/app", "bitbucket", "acme/APP", "github", "acme/other")
	if !slices.Equal(doc.Repositories, slices.Compact(want)) {
		t.Errorf("repositories = %v, want %v", doc.Repositories, slices.Compact(want))
	}
	if want := []string{"day", "week", "month", "year"}; !slices.Equal(doc.Periods, want) {
		t.Errorf("periods = %v, want %v", doc.Periods, want)
	}

	// The layer's own answer, which is the only caller that writes the other
	// direction.
	no := false
	node = reportsNode(t, reportsLocal, InstallOptions{ReportsPaused: &no})
	if err := json.Unmarshal(rawOf(t, node, "paused"), &paused); err != nil {
		t.Fatalf("reports.paused: %v", err)
	}
	if paused {
		t.Error("the layer's jobs.reports.paused: false did not reach the node the module applies: starting the job would need the gcloud command the setting replaced")
	}
}

// TestBucketLifecycleCarriesReportsAtEighteenMonths: the stored reports are
// kept 18 months and then deleted (E3), and both halves of that rule are in
// the plan — this program's reports_days and the module's reports/ rule that
// reads it. The number is in whole days, as the block's other members are, and
// it is longer than a year on purpose: a yearly report rolls the twelve stored
// monthly ones up, so the reports have to outlive the records they summarise
// (runs/ keeps its 90, F24, untouched).
func TestBucketLifecycleCarriesReportsAtEighteenMonths(t *testing.T) {
	spec, err := Installation(parseLC(t, reportsLocal), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const months, days = 18, 548
	if spec.BucketLifecycle.ReportsDays != days {
		t.Errorf("reports_days = %d, want %d (%d months, in whole days as the block's members are)", spec.BucketLifecycle.ReportsDays, days, months)
	}
	if days <= 365 {
		t.Errorf("%d days is not more than a year: a yearly report would outlive the monthly ones it rolls up only by months", days)
	}
	if spec.BucketLifecycle.RunsDays != 90 {
		t.Errorf("runs_days = %d, want the unchanged 90: the reports' retention is E3's, and the records' is F24's", spec.BucketLifecycle.RunsDays)
	}

	spec.Reports = nil
	vars, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	var node struct {
		BucketLifecycle map[string]json.RawMessage `json:"bucket_lifecycle"`
	}
	if err := json.Unmarshal(vars, &node); err != nil {
		t.Fatal(err)
	}
	var got int
	if err := json.Unmarshal(rawOf(t, node.BucketLifecycle, "reports_days"), &got); err != nil {
		t.Fatalf("bucket_lifecycle.reports_days: %v", err)
	}
	if got != days {
		t.Errorf("the installation tfvars carry reports_days %d, want %d: the member is written for every installation rather than left to the module's default, so the value and the rule it is about are one thing in the plan a review reads", got, days)
	}

	// The other half, which is the module's: the rule deletes reports/ and
	// nothing else, and its age is the member above rather than a second
	// spelling of 548 in HCL.
	b, err := terraform.FS.ReadFile("gcp/modules/installation/bucket.tf")
	if err != nil {
		t.Fatal(err)
	}
	rule := regexp.MustCompile(`age\s*=\s*var\.bucket_lifecycle\.reports_days\s*\n\s*matches_prefix\s*=\s*\["reports/"\]`)
	if !rule.Match(b) {
		t.Error("the installation module has no rule deleting reports/ at var.bucket_lifecycle.reports_days: the Go half's 548 days nothing applies, and the stored reports would be kept for ever")
	}
}

// TestReportsJobAccountHoldsNoSecretAndNoProjectRole: the job's whole reach is
// the two conditioned bucket grants the module writes — runs/ read-only, so it
// can read every repository's records, and reports/ read-write, so it is the
// one writer of them (ruling 3). It holds no project role, mounts no secret
// and carries no git or model credential: a job that could reach a token could
// write a report about a run rather than about its records, and one that could
// reach the project could do it to anything else in it.
func TestReportsJobAccountHoldsNoSecretAndNoProjectRole(t *testing.T) {
	node := reportsNode(t, reportsLocal, InstallOptions{})

	// The node's own members: the module's variable declares exactly these,
	// and a member added here (a secret container, an extra grant) would be a
	// field no validation refuses — the variable's type is the schema.
	want := []string{"account_id", "env", "image", "job", "paused", "read_condition", "scheduler_job", "scheduler_region", "write_condition"}
	if got := slices.Sorted(maps.Keys(node)); !slices.Equal(got, want) {
		t.Errorf("the reports node carries %v, want %v", got, want)
	}

	// The environment: the platform variables every job has and the document,
	// and nothing else. The list is asserted by name rather than by scanning
	// for a word, so a credential mounted under a name nobody guessed fails
	// here too.
	env := reportsEnv(t, node)
	platform := []string{"FUGARO_BACKEND", "FUGARO_BUCKET", "FUGARO_GCP_PROJECT", "FUGARO_PROJECT", "FUGARO_REGION"}
	wantEnv := slices.Sorted(slices.Values(append(slices.Clone(platform), ReportsJobDocEnv)))
	if got := slices.Sorted(maps.Keys(env)); !slices.Equal(got, wantEnv) {
		t.Errorf("the job's environment is %v, want the platform variables %v beside %s", got, platform, ReportsJobDocEnv)
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		if strings.Contains(strings.ToUpper(k), "TOKEN") || strings.Contains(strings.ToUpper(k), "SECRET") {
			t.Errorf("the job's environment carries %s: the job holds no credential of the installation's", k)
		}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(env[ReportsJobDocEnv]), &doc); err != nil {
		t.Fatal(err)
	}
	if got, want := slices.Sorted(maps.Keys(doc)), []string{"bucket", "periods", "project", "repositories", "version"}; !slices.Equal(got, want) {
		t.Errorf("the document carries %v, want %v: it is the job's whole assignment, and a credential in it would be a secret in Terraform's state", got, want)
	}

	// The other half is the module's, and it is the half that decides what the
	// account may do: no project-level grant at all, no secret in the job's
	// template, and the two bucket members above as the only resources that
	// name the account.
	b, err := terraform.FS.ReadFile("gcp/modules/installation/reports.tf")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "google_project_iam") {
		t.Error("the module grants the reports job's account a project role: its reach is the two conditioned bucket grants, and a project role is everything else in the project")
	}
	if strings.Contains(src, "secret_key_ref") || strings.Contains(src, "value_source") {
		t.Error("the job's container mounts a secret: it holds no credential of the installation's, and its assignment is the document in its environment")
	}
	if !strings.Contains(src, `resource "google_storage_bucket_iam_member" "reports_read"`) ||
		!strings.Contains(src, `resource "google_storage_bucket_iam_member" "reports_write"`) {
		t.Error("the module has no conditioned bucket grant for reports_read and reports_write: the account would hold nothing, or the grants would be the wide ones a condition exists to avoid")
	}
	// Every resource whose body names the account as a member is one of the
	// two bucket grants: a project role, a token grant or an invoker grant
	// held by THIS account rather than by the installation's scheduler would
	// be read off the same plan.
	bodies := regexp.MustCompile(`(?ms)^resource "([a-z_]+)" "([a-z_]+)" \{.*?\n\}`).FindAllStringSubmatch(src, -1)
	if len(bodies) == 0 {
		t.Fatal("the module's reports.tf holds no resource: this test is reading the wrong file")
	}
	for _, block := range bodies {
		if !strings.Contains(block[0], "google_service_account.reports[0].member") {
			continue
		}
		if block[1] != "google_storage_bucket_iam_member" {
			t.Errorf("%s.%s grants the reports account something: its two conditioned bucket members are the whole of its reach", block[1], block[2])
		}
	}

	// The two conditions are the ones gcp builds, byte for byte: the module
	// refuses a binding whose condition changed (internal/infra/tf), and this
	// is the same comparison one step earlier.
	spec, err := Installation(parseLC(t, reportsLocal), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Reports.ReadCondition.Expression; got != gcp.ReportsReadCondition("fugaro-runs-proj-1234") {
		t.Errorf("the read condition is %q, want gcp.ReportsReadCondition's bytes", got)
	}
	if got := spec.Reports.WriteCondition.Expression; got != gcp.ReportsWriteCondition("fugaro-runs-proj-1234") {
		t.Errorf("the write condition is %q, want gcp.ReportsWriteCondition's bytes", got)
	}
}

// TestReportsJobNameIsNotAWorkflowJob: the job's name starts with fugarorpt-
// so that nothing counts it as one of the workflows' own (backend/gcp skips a
// job whose name does not start with fugaro-, which is the prefix every
// workflow job's name is built with), and so that the module's alert policy,
// which matches ^fugarorpt-, is the policy that fires on it.
func TestReportsJobNameIsNotAWorkflowJob(t *testing.T) {
	if !strings.HasPrefix(ReportsJobName, "fugarorpt-") {
		t.Errorf("the job is named %q, which the module's alert policy (^fugarorpt-) does not match: an execution that died before it could log its decision would be silent", ReportsJobName)
	}
	if strings.HasPrefix(ReportsJobName, "fugaro-") {
		t.Errorf("the job is named %q: a name with the workflow prefix is counted as one of the repositories' jobs by every listing that skips the others", ReportsJobName)
	}
	// The two vocabularies are disjoint, whatever a slug is: every workflow
	// job's name is fugaro-<slug>-<workflow>.
	slug, err := task.Slug("github", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{gcp.JobName(slug, "web"), gcp.CheckJobName(slug), gcp.SchedulerJobName(slug)} {
		if name == ReportsJobName {
			t.Fatalf("a job of %s is named %q, the reports job's own name", slug, name)
		}
	}
	if !strings.HasPrefix(gcp.JobName(slug, "web"), "fugaro-") {
		t.Fatalf("a workflow job of %s is named %q, which does not carry the prefix the reports job is kept out of", slug, gcp.JobName(slug, "web"))
	}
	// The name's two constraints are one thing in the module too: its
	// validation refuses a job that does not start with the prefix, and the
	// alert policy is written with it.
	b, err := terraform.FS.ReadFile("gcp/modules/installation/reports.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `job_name=~\"^fugarorpt-\"`) {
		t.Error("the module's alert policy does not match ^fugarorpt-: a renamed job would lose its alert rather than fail a test")
	}
	v, err := terraform.FS.ReadFile("gcp/modules/installation/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(v), `startswith(var.reports.job, "fugarorpt-")`) {
		t.Error("the module does not refuse a reports job whose name leaves the prefix")
	}
}

// TestReportsSchedulerJobIsInTheSchedulerRegion: Scheduler is not offered in
// every Cloud Run region, so the schedule runs where it is offered (the
// nearest location gcp knows of) and the job runs in the installation's own
// region — the http target names the job's URI, whichever region each is in.
// A project that names its own scheduler region keeps it.
func TestReportsSchedulerJobIsInTheSchedulerRegion(t *testing.T) {
	spec, err := Installation(parseLC(t, reportsLocal), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := gcp.SchedulerRegion("us-east5")
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Reports.SchedulerRegion; got != want {
		t.Errorf("scheduler_region = %q, want %q, the location gcp derives for the installation's us-east5", got, want)
	}
	if spec.Reports.SchedulerRegion == "us-east5" {
		t.Errorf("the schedule is planned in the job's own region %q: us-east5 is one of the regions Scheduler is not offered in", spec.Reports.SchedulerRegion)
	}

	// The config's own answer wins, which is what --scheduler-region and the
	// published layer write for an installation whose jobs are somewhere
	// Scheduler does not reach.
	const elsewhere = reportsLocal + "scheduler_region: us-central1\n"
	spec, err = Installation(parseLC(t, elsewhere), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Reports.SchedulerRegion; got != "us-central1" {
		t.Errorf("scheduler_region = %q, want the config's own us-central1", got)
	}
}
