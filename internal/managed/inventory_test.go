package managed

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/deploy/terraform"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// fakeAssets is the whole-project read as a test sees it: what it answers, and
// the log of every call it was asked to make. The log is what proves the
// inventory reads and never writes: List is the only method the interface has,
// and the log shows it was called once, for the project asked about.
type fakeAssets struct {
	assets []Asset
	err    error
	calls  []string
}

func (f *fakeAssets) List(_ context.Context, gcpProject string) ([]Asset, error) {
	f.calls = append(f.calls, "List "+gcpProject)
	return f.assets, f.err
}

// listed is one asset a read returns: its full resource name and the type GCP
// files it under, in the shape the Cloud Asset API answers with.
func listed(name, typ string) Asset { return Asset{Name: name, Type: typ} }

// inventory is the check under test, for a project whose state accounts for
// accounted and whose whole-project read answers with assets.
func inventory(a Assets, accounted ...string) Inventory {
	return Inventory{Project: project, GCPProject: gcpProject, Assets: a, Accounted: accounted}
}

// A resource the state does not account for falls the run back to
// confirmations and is named: never a halt, never a delete (D59).
func TestInventoryForeignResourceFallsBack(t *testing.T) {
	runs := "//storage.googleapis.com/projects/_/buckets/fugaro-runs-" + gcpProject
	other := "//compute.googleapis.com/projects/" + gcpProject + "/zones/us-east5-a/instances/somebody-elses"
	a := &fakeAssets{assets: []Asset{
		listed(runs, "storage.googleapis.com/Bucket"),
		listed(other, "compute.googleapis.com/Instance"),
	}}
	d := inventory(a, "fugaro-runs-"+gcpProject).Check(context.Background())

	if !d.Fallback {
		t.Fatalf("a foreign resource did not fall the run back: %+v", d)
	}
	if d.Cause != CauseForeign {
		t.Errorf("Cause = %q, want %q", d.Cause, CauseForeign)
	}
	if len(d.Foreign) != 1 || d.Foreign[0].Name != other {
		t.Errorf("Foreign = %+v, want only %s", d.Foreign, other)
	}
	for _, want := range []string{other, "compute.googleapis.com/Instance", project, gcpProject} {
		if !strings.Contains(d.Why, want) {
			t.Errorf("the warning does not name %q: %s", want, d.Why)
		}
	}
	// The resource it could account for is not in the warning: the warning is
	// about what is foreign, not about the listing.
	if strings.Contains(d.Why, runs) {
		t.Errorf("the warning names the accounted bucket: %s", d.Why)
	}
}

// Every resource accounted for is a clean project: no fallback, no warning,
// and the run's own confirmations are not touched.
func TestInventoryEveryResourceAccountedIsClean(t *testing.T) {
	job := "projects/" + gcpProject + "/locations/us-east5/jobs/check"
	a := &fakeAssets{assets: []Asset{
		listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-"+gcpProject, "storage.googleapis.com/Bucket"),
		listed("//run.googleapis.com/"+job, "run.googleapis.com/Job"),
	}}
	d := inventory(a, "fugaro-runs-"+gcpProject, job).Check(context.Background())

	if d.Fallback || d.Cause != "" || len(d.Foreign) != 0 || d.Why != "" {
		t.Errorf("an accounted-for project decided %+v, want a clean decision", d)
	}
	if !slices.Equal(a.calls, []string{"List " + gcpProject}) {
		t.Errorf("the read was called %v, want one List of the project", a.calls)
	}
}

// The fail-closed direction, and the three causes it has to tell apart: an
// inventory that cannot be read is a fallback and says which of the three it
// was — the API disabled, the read role missing, or the read over quota
// (Global Constraint 3, D59's never-halt).
func TestInventoryReadThatFailsFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		cause Cause
		names []string
	}{
		{"api disabled", errorInfo(403, "SERVICE_DISABLED", ServiceCloudAsset, "Cloud Asset API has not been used in project"), CauseAPIDisabled,
			[]string{ServiceCloudAsset, "disabled"}},
		{"role missing", &googleapi.Error{Code: 403, Message: "Permission 'cloudasset.assets.searchAllResources' denied"}, CauseRoleMissing,
			[]string{RoleCloudAssetViewer}},
		{"over quota", &googleapi.Error{Code: 429, Message: "Quota exceeded for quota metric 'Requests'"}, CauseQuota,
			[]string{"quota"}},
		{"anything else", errors.New("connection reset"), CauseUnreadable, []string{"connection reset"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAssets{err: tc.err}
			d := inventory(a).Check(context.Background())

			if !d.Fallback {
				t.Fatalf("an inventory that could not be read did not fall back: %+v", d)
			}
			if d.Cause != tc.cause {
				t.Errorf("Cause = %q, want %q (%s)", d.Cause, tc.cause, d.Why)
			}
			if len(d.Foreign) != 0 {
				t.Errorf("Foreign = %+v, want none: nothing was read", d.Foreign)
			}
			for _, want := range append([]string{project, gcpProject, "confirmation"}, tc.names...) {
				if !strings.Contains(d.Why, want) {
					t.Errorf("Why does not name %q: %s", want, d.Why)
				}
			}
			if !slices.Equal(a.calls, []string{"List " + gcpProject}) {
				t.Errorf("the read was called %v, want one List of the project", a.calls)
			}
		})
	}
}

// No read at all is a fallback too, and it is not attempted: a check that
// cannot run means ask for confirmation, never "clean" (Global Constraint 3).
func TestInventoryNoReadFallsBack(t *testing.T) {
	// No reader at all.
	d := Inventory{Project: project, GCPProject: gcpProject}.Check(context.Background())
	if !d.Fallback || d.Cause != CauseNoReader {
		t.Fatalf("decided %+v, want a fallback with cause %q", d, CauseNoReader)
	}
	if !strings.Contains(d.Why, "confirmation") {
		t.Errorf("the warning does not say what happens next: %s", d.Why)
	}

	// A reader, but no project to read it for: the read is not made at all.
	a := &fakeAssets{}
	d = Inventory{Project: project, Assets: a}.Check(context.Background())
	if !d.Fallback || d.Cause != CauseNoReader {
		t.Fatalf("decided %+v, want a fallback with cause %q", d, CauseNoReader)
	}
	if len(a.calls) != 0 {
		t.Errorf("the read was called %v for no project", a.calls)
	}
}

// What "the state accounts for" matches: the resource's own name, which the
// state stores either as its API path or, for a resource GCP names that way,
// as its bare name — and never a name that merely contains it.
func TestInventoryAccountedMatching(t *testing.T) {
	const job = "projects/proj-1234/locations/us-east5/jobs/check"
	for _, tc := range []struct {
		name      string
		accounted []string
		listName  string
		want      bool
	}{
		{"a bare name, a bucket", []string{"fugaro-runs-proj-1234"}, "//storage.googleapis.com/projects/_/buckets/fugaro-runs-proj-1234", true},
		{"an API path", []string{job}, "//run.googleapis.com/" + job, true},
		{"a project-prefixed path", []string{"jobs/check"}, "//run.googleapis.com/" + job, true},
		{"empty list", nil, "//storage.googleapis.com/projects/_/buckets/fugaro-runs-proj-1234", false},
		{"a name merely contained", []string{"runs"}, "//storage.googleapis.com/projects/_/buckets/not-runs", false},
		{"a name that is a prefix", []string{"runs"}, "//storage.googleapis.com/projects/_/buckets/runs-2", false},
		{"a child of an accounted resource", []string{job}, "//run.googleapis.com/" + job + "/executions/e-1", false},
		{"another project's resource", []string{"fugaro-runs-proj-1234"}, "//storage.googleapis.com/projects/_/buckets/fugaro-runs-other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAssets{assets: []Asset{listed(tc.listName, "x.googleapis.com/Thing")}}
			d := inventory(a, tc.accounted...).Check(context.Background())
			if got := !d.Fallback; got != tc.want {
				t.Errorf("accounted = %v: %s decided %+v, want accounted %v", tc.accounted, tc.listName, d, tc.want)
			}
		})
	}
}

// A project with very many foreign resources warns without printing all of
// them, and says how many it did not print.
func TestInventoryWarnsWithoutPrintingEverything(t *testing.T) {
	var assets []Asset
	for i := range 7 {
		assets = append(assets, listed(fmt.Sprintf("//compute.googleapis.com/projects/%s/zones/z/disks/d-%d", gcpProject, i), "compute.googleapis.com/Disk"))
	}
	d := inventory(&fakeAssets{assets: assets}).Check(context.Background())
	if !d.Fallback || len(d.Foreign) != len(assets) {
		t.Fatalf("decided %+v, want all %d foreign resources counted", d, len(assets))
	}
	if !strings.Contains(d.Why, "d-4") || strings.Contains(d.Why, "d-5") {
		t.Errorf("the warning names the wrong number of resources: %s", d.Why)
	}
	if !strings.Contains(d.Why, "2 more") {
		t.Errorf("the warning does not say how many it left out: %s", d.Why)
	}
}

// The production read is a read: one GET per page of the RPC, for the project
// asked about, and it follows the next-page token.
func TestInventoryCloudAssetsListsTheProject(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+"?pageToken="+r.URL.Query().Get("pageToken"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pageToken") == "" {
			fmt.Fprintf(w, `{"results":[{"name":"//storage.googleapis.com/projects/_/buckets/a","assetType":"storage.googleapis.com/Bucket"}],"nextPageToken":"t2"}`)
			return
		}
		fmt.Fprintf(w, `{"results":[{"name":"//compute.googleapis.com/projects/%s/zones/z/instances/vm","assetType":"compute.googleapis.com/Instance"}]}`, gcpProject)
	}))
	defer srv.Close()

	c, err := NewCloudAssets(context.Background(), option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.List(context.Background(), gcpProject)
	if err != nil {
		t.Fatal(err)
	}
	want := []Asset{
		{Name: "//storage.googleapis.com/projects/_/buckets/a", Type: "storage.googleapis.com/Bucket"},
		{Name: "//compute.googleapis.com/projects/" + gcpProject + "/zones/z/instances/vm", Type: "compute.googleapis.com/Instance"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("List = %+v, want %+v", got, want)
	}
	wantCalls := []string{
		"GET /v1/projects/" + gcpProject + ":searchAllResources?pageToken=",
		"GET /v1/projects/" + gcpProject + ":searchAllResources?pageToken=t2",
	}
	if !slices.Equal(calls, wantCalls) {
		t.Errorf("the read made %v, want %v", calls, wantCalls)
	}
	// No project, no read: the read is never made for an empty scope.
	if _, err := c.List(context.Background(), ""); err == nil {
		t.Error("List read with no project")
	}
}

// A SERVICE_DISABLED for a service that is not the one this read goes through
// is not this API being disabled: the answer names the other service, and
// sending the operator to fugaro init for it would be a wrong sentence about
// their project. It is the read failing, with the answer's own words in the
// warning — the same reading internal/infra's serviceDisabled makes of the
// detail's service.
func TestInventoryOtherServiceDisabledIsNotThisAPI(t *testing.T) {
	const other = "logging.googleapis.com"
	err := errorInfo(403, "SERVICE_DISABLED", other, "Logging API has not been used in project "+gcpProject+" before or it is disabled")
	d := inventory(&fakeAssets{err: err}).Check(context.Background())

	if !d.Fallback {
		t.Fatalf("a read that failed did not fall back: %+v", d)
	}
	if d.Cause != CauseUnreadable {
		t.Errorf("Cause = %q, want %q (%s)", d.Cause, CauseUnreadable, d.Why)
	}
	if strings.Contains(d.Why, string(CauseAPIDisabled)) {
		t.Errorf("the warning says the Cloud Asset API is disabled: %s", d.Why)
	}
	if !strings.Contains(d.Why, other) {
		t.Errorf("the warning does not carry the answer's own words, which name %s: %s", other, d.Why)
	}
}

// The two halves of this task that are facts about the installation's own
// module rather than about this package: the API the read goes through is
// enabled, and the identity that makes the read holds the read role. Both are
// read out of the embedded module the installation applies, never out of a
// literal here, so a module that stops granting either fails this test
// (Global Constraint 12: no live cloud in a test).
func TestInventoryCloudAssetAPIAndGrantAreTheModules(t *testing.T) {
	apis, err := fs.ReadFile(terraform.FS, "gcp/modules/installation/apis.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apis), `"`+ServiceCloudAsset+`"`) {
		t.Errorf("the installation module does not enable %s: %s", ServiceCloudAsset, apis)
	}

	iam, err := fs.ReadFile(terraform.FS, "gcp/modules/installation/iam.tf")
	if err != nil {
		t.Fatal(err)
	}
	grant := grantBlock(t, string(iam), RoleCloudAssetViewer)
	for _, want := range []string{"toset(var.operators)", "each.value"} {
		if !strings.Contains(grant, want) {
			t.Errorf("the installation's grant of %s does not name %s:\n%s", RoleCloudAssetViewer, want, grant)
		}
	}

	// And the gate admits the grant the module makes, which is the coupling
	// that makes the grant real rather than decorative: internal/infra/tf's
	// grantRules is an allowlist, so a module grant the table does not name
	// makes fugaro init refuse the installation's own plan — and the identity
	// the read needs would then never exist.
	op := "user:operator@example.com"
	plan := &tf.Plan{ResourceChanges: []tf.ResourceChange{{
		Address: `module.installation.google_project_iam_member.operator_cloudasset_viewer["` + op + `"]`,
		Type:    "google_project_iam_member",
		Change: tf.Change{Actions: []string{"create"},
			After: map[string]any{"member": op, "role": RoleCloudAssetViewer, "project": gcpProject}},
	}}}
	cover := tf.Cover{Projects: []string{gcpProject}, Listed: []string{op}}
	if why := cover.NotCovered(plan, nil); len(why) != 0 {
		t.Errorf("the installation's own gate refuses the grant its module makes: %q", why)
	}
}

// grantBlock is the body of the module's *_iam_member resource that grants
// role, read the way internal/infra/tf's own module walk reads the modules:
// the blocks are the fixture, and a module that grants the role nowhere fails
// the test rather than passing it.
func grantBlock(t *testing.T, module, role string) string {
	t.Helper()
	for _, m := range iamMemberRE.FindAllStringSubmatch("\n"+module, -1) {
		if strings.Contains(m[2], `"`+role+`"`) {
			return m[2]
		}
	}
	t.Fatalf("no *_iam_member resource in the installation module grants %s", role)
	return ""
}

// iamMemberRE is one *_iam_member resource block of a module.
var iamMemberRE = regexp.MustCompile(`(?s)\nresource "([a-z0-9_]+_iam_member)" "[a-z0-9_]+" \{(.*?)\n\}`)

// errorInfo is a googleapi error carrying one ErrorInfo detail, as the client
// parses Google's answer to a call to an API that is not enabled (the shape
// internal/infra's own tests use).
func errorInfo(code int, reason, service, message string) error {
	return &googleapi.Error{Code: code, Message: message, Details: []any{
		map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason, "domain": "googleapis.com",
			"metadata": map[string]any{"service": service}},
	}}
}
