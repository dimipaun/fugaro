package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// runClients are the clients these reads go through, pointed at one fake:
// both readers ask Cloud Run and nothing else, so a client for it is the
// whole of what either needs.
func runClients(t *testing.T, f *gcpfake.Run) *Clients {
	t.Helper()
	svc, err := run.NewService(context.Background(), option.WithEndpoint(f.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return &Clients{Run: svc}
}

// --- ProjectJobRetirements (issue #3686)

const (
	// The retired coding job's instance: the job itself, the service account
	// it runs as, and one grant. The module creates exactly these three kinds
	// of resource for an instance, so the address set below is the whole of
	// what a retirement takes, whatever the state happens to hold.
	goneJobAddr = `module.project["fugaro-standard-github-team-a"].google_cloud_run_v2_job.this`
	goneSAAddr  = `module.project["fugaro-standard-github-team-a"].google_service_account.job`
	goneIAMAddr = `module.project["fugaro-standard-github-team-a"].google_project_iam_member.vertex`

	// The instance of a job the document still declares, which no retirement
	// may touch.
	keptJobAddr = `module.project["fugaro-standard-github-team-b"].google_cloud_run_v2_job.this`
	keptSAAddr  = `module.project["fugaro-standard-github-team-b"].google_service_account.job`

	// The retired coding job's review-only variant: the same axis under the
	// second module instance, its own name, and the same fate.
	goneReviewJobAddr = `module.project_review["fugaro-standard-github-team-a-review"].google_cloud_run_v2_job.this`

	// The for_each keys those two instances are held under, which is what the
	// document declares or does not.
	goneJobKey       = "fugaro-standard-github-team-a"
	goneReviewJobKey = "fugaro-standard-github-team-a-review"
	keptJobKey       = "fugaro-standard-github-team-b"
)

// quote renders s as a JSON string, for the documents a test composes.
func quote(s string) string { return fmt.Sprintf("%q", s) }

// projectJobsStateDoc is that installation root's state: the two instances of
// a job the document no longer declares (the coding one and its review
// variant) and one of a job it does, each with its job's own values — the
// name Cloud Run knows it by, its region, its labels and its deletion
// protection.
const projectJobsStateDoc = `{
  "format_version": "1.0",
  "values": {
    "root_module": {
      "child_modules": [
        {
          "address": "module.project[\"fugaro-standard-github-team-a\"]",
          "resources": [
            {"address": %q, "mode": "managed", "type": "google_cloud_run_v2_job", "name": "this",
             "values": {"name": "fugaro-standard-github-team-a", "location": "us-east5", "deletion_protection": true,
                        "labels": {"fugaro": "managed", "fugaro_profile": "standard", "fugaro_provider": "github", "fugaro_account": "team-a"}}},
            {"address": %q, "mode": "managed", "type": "google_service_account", "name": "job",
             "values": {"account_id": "fugaro-standard-github-team-a"}},
            {"address": %q, "mode": "managed", "type": "google_project_iam_member", "name": "vertex",
             "values": {"role": "roles/aiplatform.user"}}
          ]
        },
        {
          "address": "module.project[\"fugaro-standard-github-team-b\"]",
          "resources": [
            {"address": %q, "mode": "managed", "type": "google_cloud_run_v2_job", "name": "this",
             "values": {"name": "fugaro-standard-github-team-b", "location": "us-east5", "deletion_protection": true}},
            {"address": %q, "mode": "managed", "type": "google_service_account", "name": "job",
             "values": {"account_id": "fugaro-standard-github-team-b"}}
          ]
        },
        {
          "address": "module.project_review[\"fugaro-standard-github-team-a-review\"]",
          "resources": [
            {"address": %q, "mode": "managed", "type": "google_cloud_run_v2_job", "name": "this",
             "values": {"name": "fugaro-standard-github-team-a-review", "location": "us-east5", "deletion_protection": true}}
          ]
        }
      ]
    }
  }
}`

// projectJobsVars is the document the plan of that run reads: the job of
// (standard, github, team-b) alone, so the team-a instance of both module
// instances is what the plan would delete.
const projectJobsVars = `{
  "project_jobs": {"fugaro-standard-github-team-b": {"profile": "standard", "provider": "github", "account": "team-b", "job": "fugaro-standard-github-team-b"}},
  "project_review_jobs": {"fugaro-standard-github-team-b-review": {"profile": "standard", "provider": "github", "account": "team-b-review", "job": "fugaro-standard-github-team-b-review"}}
}`

func projectJobsStateOf(t *testing.T) *tf.State {
	t.Helper()
	doc := fmt.Sprintf(projectJobsStateDoc, goneJobAddr, goneSAAddr, goneIAMAddr, keptJobAddr, keptSAAddr, goneReviewJobAddr)
	var st tf.State
	if err := json.Unmarshal([]byte(doc), &st); err != nil {
		t.Fatal(err)
	}
	return &st
}

// TestProjectJobRetirementsNamesTheInstanceTheDocumentNoLongerDeclares: the
// retirement is the whole instance whose for_each key the document dropped —
// the job, its service account and its grants — and nothing of the instance
// the document still declares.
//
// The two halves of the answer are what the flow does with the rows: the job's
// address is PROTECTED (no apply can delete it while the module's own
// deletion_protection says true), so it is the one address that leaves
// Terraform's state and is deleted through the Cloud Run API; the service
// account and the grant are not, so they stay in the state and the apply
// removes them.
func TestProjectJobRetirementsNamesTheInstanceTheDocumentNoLongerDeclares(t *testing.T) {
	st := projectJobsStateOf(t)
	rets, err := ProjectJobRetirements([]byte(projectJobsVars), st)
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]Retirement{}
	for _, r := range rets {
		at[r.Address] = r
	}
	for _, addr := range []string{goneJobAddr, goneSAAddr, goneIAMAddr, goneReviewJobAddr} {
		if _, ok := at[addr]; !ok {
			t.Errorf("%s is not in the retirement: the document declares no job of its key", addr)
		}
	}
	for _, addr := range []string{keptJobAddr, keptSAAddr} {
		if r, ok := at[addr]; ok {
			t.Errorf("%s is in the retirement (%+v): the document declares its job", addr, r)
		}
	}
	job := at[goneJobAddr]
	if job.Job != "fugaro-standard-github-team-a" || job.Region != "us-east5" {
		t.Errorf("the retired job is %+v; want the state's own name and region", job)
	}
	if !job.Protected {
		t.Error("the retired job is not protected: no apply can delete it, which is why it leaves the state")
	}
	if got := job.Labels[gcp.LabelProfile]; got != "standard" {
		t.Errorf("the retired job's labels are %v; want the state's own set, which is what the API delete checks", job.Labels)
	}
	if sa := at[goneSAAddr]; sa.Protected {
		t.Error("the retired service account is protected: an apply deletes it, and taking it out of the state would leave it alive in GCP")
	}
}

// TestProjectJobRetirementsKeepsTheAxisApart: the two module instances are
// two for_each maps over the same kind of key, and a key declared under one
// says nothing about the instance of the same key under the other. A walk
// that read one set for both would either retire a job the document still
// declares on its own axis, or keep one it does not.
func TestProjectJobRetirementsKeepsTheAxisApart(t *testing.T) {
	// Each key declared on the OTHER axis: neither instance is declared on
	// its own, so both go — a name is not a declaration.
	crossed := `{"project_jobs": {` + quote(goneReviewJobKey) + `: {}}, "project_review_jobs": {` + quote(goneJobKey) + `: {}}}`
	rets, err := ProjectJobRetirements([]byte(crossed), projectJobsStateOf(t))
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]bool{}
	for _, r := range rets {
		at[r.Address] = true
	}
	for _, addr := range []string{goneJobAddr, goneReviewJobAddr} {
		if !at[addr] {
			t.Errorf("%s is not retired: its key is declared on the other axis and not on its own", addr)
		}
	}

	// Each key declared on its OWN axis: neither of the two goes.
	straight := `{"project_jobs": {` + quote(goneJobKey) + `: {}, ` + quote(keptJobKey) + `: {}}, "project_review_jobs": {` + quote(goneReviewJobKey) + `: {}}}`
	rets, err = ProjectJobRetirements([]byte(straight), projectJobsStateOf(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rets {
		if r.Address == goneJobAddr || r.Address == goneReviewJobAddr {
			t.Errorf("%s is retired: the document declares its own key on its own axis", r.Address)
		}
	}
}

// TestProjectJobRetireStopsRefusesATypeNobodyDecided: the allowlist is read
// per address, and the resource the project module grows tomorrow is the case
// it exists for. A project job is retirable — the job, its account and its
// grants are three of tf.RetirableTypes' rows — and a secret container under
// the same instance is not, so one such row stops the whole set: taking the
// rest of the instance apart around a container would be half a retirement.
func TestProjectJobRetireStopsRefusesATypeNobodyDecided(t *testing.T) {
	rets := []Retirement{
		{Address: goneJobAddr},
		{Address: `module.project["x"].google_secret_manager_secret.this`},
		{Address: goneSAAddr},
	}
	stops := ProjectJobRetireStops(rets)
	if len(stops) != 1 || stops[0].Address != `module.project["x"].google_secret_manager_secret.this` {
		t.Fatalf("stops = %+v; want the container alone", stops)
	}
	if !strings.Contains(stops[0].Why, "google_secret_manager_secret") {
		t.Errorf("the stop is %q; want the type's own words", stops[0].Why)
	}
	if got := ProjectJobRetireStops(rets[:1]); len(got) != 0 {
		t.Errorf("stops = %+v; want a job address retirable on its own", got)
	}
}

// TestProjectJobRetirementsRefusesADocumentItCannotRead: a tfvars nobody can
// parse is not a document that declares nothing, and reading it as one would
// delete every job of the installation. The refusal is the only direction.
func TestProjectJobRetirementsRefusesADocumentItCannotRead(t *testing.T) {
	if _, err := ProjectJobRetirements([]byte("not json"), projectJobsStateOf(t)); err == nil {
		t.Fatal("an unreadable document was read as one that declares no job")
	}
	if _, err := DeclaredProjectJobs([]byte("not json")); err == nil {
		t.Fatal("DeclaredProjectJobs read an unreadable document")
	}
}

// TestLiveProjectJobsReadsTheProjectModulesOwnLabels: the cloud is the record
// of what the installation still runs, and a project job is findable by the
// four labels modules/project/job.tf writes — which is what makes a job an
// update could not delete one the next update finds again.
//
// A repository's own job wears fugaro_workflow or fugaro_role and no
// fugaro_profile, so it is not one of these; a job the installation's config
// still declares is filtered by the caller against the document, not here.
func TestLiveProjectJobsReadsTheProjectModulesOwnLabels(t *testing.T) {
	fake := gcpfake.NewRun(t)
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProfile: "standard", "fugaro_provider": "github", "fugaro_account": "team-a"}
	fake.AddProjectJob("fugaro-standard-github-team-a", "standard", "github", "team-a", "2", "8Gi")
	fake.SetJob("fugaro-standard-github-team-a", labels, "image")
	fake.AddJob("fugaro-acme-app-app", "1", "512Mi")
	fake.SetJob("fugaro-acme-app-app", map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelWorkflow: "app"}, "image")
	c := runClients(t, fake)

	jobs, answered, warning := LiveProjectJobs(context.Background(), c, "proj-1234", "us-east5")
	if !answered {
		t.Fatalf("the listing answered nothing: %s", warning)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v; want the installation's own shared job alone", jobs)
	}
	j := jobs[0]
	if j.Name != "fugaro-standard-github-team-a" || j.Profile != "standard" || j.Provider != "github" || j.Account != "team-a" {
		t.Errorf("the job is %+v; want the triple its own labels name", j)
	}
	if j.Region != "us-east5" || j.Labels[gcp.LabelProfile] != "standard" {
		t.Errorf("the job is %+v; want the region asked about and the labels the API delete checks", j)
	}
	if _, answered, _ := LiveProjectJobs(context.Background(), nil, "proj-1234", "us-east5"); answered {
		t.Error("no client answered: a question never asked is not an empty list")
	}
}

// TestProjectJobStateLabelsReadsTheStatesOwnRow: the set the API delete of a
// retired job is checked against is the one the job's own state row carries,
// and never the one the listing that found the job matched — the listing
// filtered on those very pairs, so a delete judged by them is judged by
// nothing. A state row that carries none is nil, which the caller answers with
// the marks the listing matched by and nothing else.
func TestProjectJobStateLabelsReadsTheStatesOwnRow(t *testing.T) {
	rets, err := ProjectJobRetirements([]byte(projectJobsVars), projectJobsStateOf(t))
	if err != nil {
		t.Fatal(err)
	}
	got := ProjectJobStateLabels(rets, goneJobKey)
	if got[gcp.LabelProfile] != "standard" || got["fugaro_account"] != "team-a" {
		t.Errorf("the labels are %v; want the state's own set for the job", got)
	}
	// The review variant's row carries none in this fixture, which is the
	// state of an installation written before the module labelled its jobs.
	if got := ProjectJobStateLabels(rets, goneReviewJobKey); got != nil {
		t.Errorf("the labels are %v; want nil for a row that carries none", got)
	}
	// A job the retirement does not hold at all: a name nothing answers for.
	if got := ProjectJobStateLabels(rets, keptJobKey); got != nil {
		t.Errorf("the labels are %v; want nil for a job no retirement holds", got)
	}
}

// TestProjectJobRetiredRowsKeepsAKeptInstancesEveryRow: an instance this run
// leaves keeps ALL of its rows, never only the job's. A state removal is what
// makes the plan below stop producing the address, so an instance whose job row
// left while its service account and its grants stayed is an apply that
// destroys what a run in flight reads and writes through.
func TestProjectJobRetiredRowsKeepsAKeptInstancesEveryRow(t *testing.T) {
	rets, err := ProjectJobRetirements([]byte(projectJobsVars), projectJobsStateOf(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := ProjectJobRetiredRows(rets, nil); len(got) != len(rets) {
		t.Errorf("the rows are %+v; want every row of the retirement when nothing is kept", got)
	}
	got := map[string]bool{}
	for _, r := range ProjectJobRetiredRows(rets, []string{goneJobKey}) {
		got[r.Address] = true
	}
	for _, addr := range []string{goneJobAddr, goneSAAddr, goneIAMAddr} {
		if got[addr] {
			t.Errorf("%s is still a row of the retirement: every row of an instance this run keeps stays", addr)
		}
	}
	if !got[goneReviewJobAddr] {
		t.Errorf("the rows are %v; want the other instance's own row, which is not the one kept", got)
	}
}

// TestProjectJobInstanceNamesNamesEveryInstanceOnce: the names a listing that
// could not be read leaves whole, which is what a carry puts back into the
// document. The for_each key IS the job's Cloud Run name, and one name answers
// on both axes, so the two instances of one name are one entry.
func TestProjectJobInstanceNamesNamesEveryInstanceOnce(t *testing.T) {
	rets, err := ProjectJobRetirements([]byte(projectJobsVars), projectJobsStateOf(t))
	if err != nil {
		t.Fatal(err)
	}
	got := ProjectJobInstanceNames(rets)
	want := []string{goneJobKey, goneReviewJobKey}
	if !slices.Equal(got, want) {
		t.Errorf("the names are %v; want %v, once each and sorted: the rows of one instance name one job", got, want)
	}
	// A row that is not a module instance's names no job, and one name on both
	// axes is one job: the review variant of a coding job carries its name.
	same := []Retirement{{Address: goneJobAddr}, {Address: strings.Replace(goneJobAddr, "module.project[", "module.project_review[", 1)}}
	if got := ProjectJobInstanceNames(same); !slices.Equal(got, []string{goneJobKey}) {
		t.Errorf("the names are %v; want %s alone, once", got, goneJobKey)
	}
	if got := ProjectJobInstanceNames([]Retirement{{Address: `module.foo.google_cloud_run_v2_job.this`}}); len(got) != 0 {
		t.Errorf("the names are %v; want none from a row that is not a project module instance", got)
	}
}

// TestJobInFlightCountsEveryExecutionThatHasNotFinished: deleting a job
// cancels the executions on it, and the run behind one is a person's work
// going away with no report anywhere, so pending is as much a run as running
// is and only a completion time settles the question.
//
// The two answers this reader must never confuse are "nothing is running" and
// "nobody could look": a job whose executions cannot be listed answers
// nothing, and every caller of it deletes nothing that run.
func TestJobInFlightCountsEveryExecutionThatHasNotFinished(t *testing.T) {
	fake := gcpfake.NewRun(t)
	fake.AddProjectJob("fugaro-standard-github-team-a", "standard", "github", "team-a", "2", "8Gi")
	fake.AddProjectJob("fugaro-standard-github-team-b", "standard", "github", "team-b", "2", "8Gi")
	c := runClients(t, fake)
	ctx := context.Background()

	// A pending execution: what a launcher leaves behind, and no more visible
	// than a running one from the outside.
	fake.Start("fugaro-standard-github-team-a")

	// A finished one, which is no reason to keep a job nobody declares.
	done := fake.Start("fugaro-standard-github-team-b")
	fake.SetState(done, backend.StateSucceeded)

	cases := []struct {
		name string
		job  string
		want bool
	}{
		{"a job with a pending execution", "fugaro-standard-github-team-a", true},
		{"a job whose only execution finished", "fugaro-standard-github-team-b", false},
		{"a job the cloud does not have", "fugaro-standard-github-team-gone", false},
	}
	for _, tc := range cases {
		busy, answered, warning := JobInFlight(ctx, c, "proj-1234", "us-east5", tc.job)
		if !answered || busy != tc.want {
			t.Errorf("%s: busy=%v answered=%v (%s); want %v and an answer", tc.name, busy, answered, warning, tc.want)
		}
	}
	// A Cloud Run nobody can reach is the third answer, and it is not the
	// second: every caller of this reader deletes nothing on it.
	dead, err := run.NewService(ctx, option.WithEndpoint("http://127.0.0.1:1/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	if _, answered, warning := JobInFlight(ctx, &Clients{Run: dead}, "proj-1234", "us-east5", "fugaro-standard-github-team-a"); answered {
		t.Error("a listing that failed was answered as if nothing were running")
	} else if !strings.Contains(warning, "gcloud run jobs executions list") {
		t.Errorf("the warning is %q; want the command that answers it by hand", warning)
	}
}
