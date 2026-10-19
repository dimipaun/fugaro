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
	"sync/atomic"
	"testing"
	"time"

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
func inventory(a Assets, accounted ...StateResource) Inventory {
	return Inventory{Project: project, GCPProject: gcpProject, ProjectNumber: testProjectNumber, Assets: a, Accounted: accounted}
}

// testProjectNumber is gcpProject's number in these tests.
const testProjectNumber = 123456789012

// A resource the state does not account for falls the run back to
// confirmations and is named: never a halt, never a delete (D59).
func TestInventoryForeignResourceFallsBack(t *testing.T) {
	runs := "//storage.googleapis.com/projects/_/buckets/fugaro-runs-" + gcpProject
	other := "//compute.googleapis.com/projects/" + gcpProject + "/zones/us-east5-a/instances/somebody-elses"
	a := &fakeAssets{assets: []Asset{
		listed(runs, "storage.googleapis.com/Bucket"),
		listed(other, "compute.googleapis.com/Instance"),
	}}
	d := inventory(a, StateResource{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject}).Check(context.Background())

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
	d := inventory(a,
		StateResource{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject},
		StateResource{Type: "run.googleapis.com/Job", Name: job}).Check(context.Background())

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
			[]string{RoleCloudAssetReader}},
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

// What "the state accounts for" matches: the resource's own type and its own
// whole path — the full resource name, the API path, or for a resource GCP
// names that way a bare name of a top-level resource in this project — and
// never a name that merely contains it, ends in it, or is the same name filed
// under another type, another project or another location.
func TestInventoryAccountedMatching(t *testing.T) {
	const (
		job     = "projects/proj-1234/locations/us-east5/jobs/check"
		jobT    = "run.googleapis.com/Job"
		bucketT = "storage.googleapis.com/Bucket"
		saT     = "iam.googleapis.com/ServiceAccount"
		vmT     = "compute.googleapis.com/Instance"
		runs    = "fugaro-runs-proj-1234"
	)
	bucket := StateResource{Type: bucketT, Name: runs}
	for _, tc := range []struct {
		name      string
		accounted []StateResource
		listName  string
		listType  string
		want      bool
	}{
		{"a bare name, a bucket (Cloud Asset form)", []StateResource{bucket}, "//storage.googleapis.com/" + runs, bucketT, true},
		{"a bare name, a bucket (project form)", []StateResource{bucket}, "//storage.googleapis.com/projects/_/buckets/" + runs, bucketT, true},
		{"a bare name, a service account of this project", []StateResource{{Type: saT, Name: "fugaro@proj-1234.iam.gserviceaccount.com"}},
			"//iam.googleapis.com/projects/proj-1234/serviceAccounts/fugaro@proj-1234.iam.gserviceaccount.com", saT, true},
		{"an API path", []StateResource{{Type: jobT, Name: job}}, "//run.googleapis.com/" + job, jobT, true},
		{"a full resource name", []StateResource{{Type: jobT, Name: "//run.googleapis.com/" + job}}, "//run.googleapis.com/" + job, jobT, true},
		{"empty list", nil, "//storage.googleapis.com/" + runs, bucketT, false},
		{"an entry with no type", []StateResource{{Name: runs}}, "//storage.googleapis.com/" + runs, bucketT, false},
		{"a name merely contained", []StateResource{{Type: bucketT, Name: "runs"}}, "//storage.googleapis.com/projects/_/buckets/not-runs", bucketT, false},
		{"a name that is a prefix", []StateResource{{Type: bucketT, Name: "runs"}}, "//storage.googleapis.com/projects/_/buckets/runs-2", bucketT, false},
		{"a child of an accounted resource", []StateResource{{Type: jobT, Name: job}}, "//run.googleapis.com/" + job + "/executions/e-1", jobT, false},
		{"another bucket", []StateResource{bucket}, "//storage.googleapis.com/projects/_/buckets/fugaro-runs-other", bucketT, false},

		// The collisions a bare suffix let through.
		{"same name, different type", []StateResource{bucket},
			"//compute.googleapis.com/projects/proj-1234/zones/us-east5-a/instances/" + runs, vmT, false},
		{"same name, different type, same shape", []StateResource{bucket},
			"//iam.googleapis.com/projects/proj-1234/serviceAccounts/" + runs, saT, false},
		{"same path, different type", []StateResource{{Type: jobT, Name: job}}, "//run.googleapis.com/" + job, "run.googleapis.com/Service", false},
		{"a bare name nested under a location", []StateResource{{Type: jobT, Name: "check"}}, "//run.googleapis.com/" + job, jobT, false},
		{"a bare location name nested in the job", []StateResource{{Type: jobT, Name: "us-east5"}}, "//run.googleapis.com/" + job, jobT, false},
		{"a bare project name nested in the job", []StateResource{{Type: jobT, Name: "proj-1234"}}, "//run.googleapis.com/" + job, jobT, false},
		{"an API path that only ends the asset's path", []StateResource{{Type: jobT, Name: job}},
			"//run.googleapis.com/x/" + job, jobT, false},
		{"an untyped asset and an untyped entry", []StateResource{{Name: runs}}, "//storage.googleapis.com/" + runs, "", false},
		{"an entry with no name", []StateResource{{Type: bucketT}}, "//storage.googleapis.com/projects/proj-1234/buckets/", bucketT, false},
		{"a partial path names no project or location", []StateResource{{Type: jobT, Name: "jobs/check"}}, "//run.googleapis.com/" + job, jobT, false},
		{"same job, different project", []StateResource{{Type: jobT, Name: job}},
			"//run.googleapis.com/projects/other-5678/locations/us-east5/jobs/check", jobT, false},
		{"same job, different location", []StateResource{{Type: jobT, Name: job}},
			"//run.googleapis.com/projects/proj-1234/locations/europe-west1/jobs/check", jobT, false},
		{"a bare name of another project's resource", []StateResource{{Type: saT, Name: "fugaro"}},
			"//iam.googleapis.com/projects/other-5678/serviceAccounts/fugaro", saT, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The listing carries a resource the state always accounts for, so
			// the case under test is never an empty listing.
			a := &fakeAssets{assets: []Asset{listed(tc.listName, tc.listType)}}
			in := Inventory{Project: project, GCPProject: "proj-1234", Assets: a, Accounted: tc.accounted}
			d := in.Check(context.Background())
			if got := !d.Fallback; got != tc.want {
				t.Errorf("accounted = %+v: %s (%s) decided %+v, want accounted %v", tc.accounted, tc.listName, tc.listType, d, tc.want)
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
		// The read is SearchAllResources and nothing else, and this server
		// refuses anything else the way the API refuses a call the caller's
		// permissions do not cover: the wider predefined role Task 4.1 first
		// granted carries cloudasset.assets.listResource, and a fake that
		// answered a ListAssets call would hide a read this inventory must
		// never make (Task 4.1a).
		if !strings.HasSuffix(r.URL.Path, ":searchAllResources") {
			t.Errorf("the whole-project read called %s %s, want SearchAllResources alone", r.Method, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Permission 'cloudasset.assets.listResource' denied on resource"}}`)
			return
		}
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
	grant := grantBlock(t, string(iam), RoleCloudAssetReader)
	for _, want := range []string{"toset(var.operators)", "each.value"} {
		if !strings.Contains(grant, want) {
			t.Errorf("the installation's grant of %s does not name %s:\n%s", RoleCloudAssetReader, want, grant)
		}
	}

	// The role the grant is of is the installation's own custom role, created
	// in the same module with exactly the one permission the read needs
	// (Task 4.1a, the owner's ruling of 2026-10-17): the wider predefined role
	// Task 4.1 first granted also carries cloudasset.assets.listResource, which
	// no call of this read makes.
	role := roleBlock(t, string(iam), RoleCloudAssetReader)
	for _, want := range []string{`role_id     = "` + RoleCloudAssetReader + `"`, `permissions = ["cloudasset.assets.searchAllResources"]`} {
		if !strings.Contains(role, want) {
			t.Errorf("the installation's %s role does not hold %s:\n%s", RoleCloudAssetReader, want, role)
		}
	}
	if strings.Contains(string(iam), "roles/cloudasset.viewer") {
		t.Error("the installation module still names the wider predefined Cloud Asset role")
	}

	// And the gate admits the grant the module makes, which is the coupling
	// that makes the grant real rather than decorative: internal/infra/tf's
	// grantRules is an allowlist, so a module grant the table does not name
	// makes fugaro init refuse the installation's own plan — and the identity
	// the read needs would then never exist.
	op := "user:operator@example.com"
	plan := &tf.Plan{ResourceChanges: []tf.ResourceChange{{
		Address: `module.installation.google_project_iam_member.operator_cloud_asset_reader["` + op + `"]`,
		Type:    "google_project_iam_member",
		Change: tf.Change{Actions: []string{"create"},
			After: map[string]any{"member": op, "role": "projects/" + gcpProject + "/roles/" + RoleCloudAssetReader, "project": gcpProject}},
	}}}
	cover := tf.Cover{Projects: []string{gcpProject}, Listed: []string{op}}
	if why := cover.NotCovered(plan, nil); len(why) != 0 {
		t.Errorf("the installation's own gate refuses the grant its module makes: %q", why)
	}
	// The role the ruling replaced is refused: a plan that grants the wider
	// predefined Cloud Asset role to the same operator is not covered, so the
	// narrowing cannot be undone by a plan the gate would admit.
	wider := &tf.Plan{ResourceChanges: []tf.ResourceChange{{
		Address: `module.installation.google_project_iam_member.operator_cloud_asset_reader["` + op + `"]`,
		Type:    "google_project_iam_member",
		Change: tf.Change{Actions: []string{"create"},
			After: map[string]any{"member": op, "role": "roles/cloudasset.viewer", "project": gcpProject}},
	}}}
	if why := cover.NotCovered(wider, nil); len(why) == 0 {
		t.Error("the gate admits a grant of the wider predefined Cloud Asset role")
	}

	// The other half of "the identity that makes the read holds the role":
	// the impersonated deployer does not, and cannot be given it. The apply's
	// credential is the deployer account (D75), so a caller that makes this
	// read under it has to know it is the fail-closed case and not a clean
	// one — and the two facts below are what make that so. First, the
	// deployer's own roles grant no Cloud Asset permission: granting one is a
	// decision about D75's account, not a detail, and this test is where it
	// has to be made deliberately (D75's roles are enumerated one permission
	// per line in deployer.tf).
	deployer, err := fs.ReadFile(terraform.FS, "gcp/modules/installation/deployer.tf")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(deployer), "cloudasset") {
		t.Errorf("the deployer's roles name a Cloud Asset permission: the account applies as the impersonated deployer (D75), and %s on it is a decision, not a detail — read internal/managed/inventory.go's %s comment before making it", RoleCloudAssetReader, "RoleCloudAssetReader")
	}
	// Second, a plan cannot grant it to an account either: the same
	// allowlist refuses the role to a service-account member, so no admitted
	// managed plan can widen the deployer with it.
	sa := "serviceAccount:fugaro-deployer@" + gcpProject + ".iam.gserviceaccount.com"
	saPlan := &tf.Plan{ResourceChanges: []tf.ResourceChange{{
		Address: `google_project_iam_member.deployer_cloud_asset_reader`,
		Type:    "google_project_iam_member",
		Change: tf.Change{Actions: []string{"create"},
			After: map[string]any{"member": sa, "role": "projects/" + gcpProject + "/roles/" + RoleCloudAssetReader, "project": gcpProject}},
	}}}
	// The account is listed, so the refusal below is the role's own rule and
	// not "a member nobody listed".
	cover.Listed = []string{op, sa}
	if why := cover.NotCovered(saPlan, nil); len(why) != 1 || !strings.Contains(why[0], "a kind of principal the installation's modules do not grant it to") {
		t.Errorf("the gate's answer to %s on the deployer account %s is %q, want it refused for the principal kind: if it is admitted, the read runs under the deployer and the role-missing fallback above is wrong", RoleCloudAssetReader, sa, why)
	}
}

// roleBlock is the body of the module's google_project_iam_custom_role
// resource whose role ID is role, read the way grantBlock reads a grant: the
// blocks are the fixture, and a module that declares no such role fails the
// test rather than passing it.
func roleBlock(t *testing.T, module, role string) string {
	t.Helper()
	for _, m := range customRoleRE.FindAllStringSubmatch("\n"+module, -1) {
		if strings.Contains(m[2], `"`+role+`"`) {
			return m[2]
		}
	}
	t.Fatalf("no google_project_iam_custom_role resource in the installation module declares %s", role)
	return ""
}

// customRoleRE is one google_project_iam_custom_role resource block of a module.
var customRoleRE = regexp.MustCompile(`(?s)\nresource "google_project_iam_custom_role" "([a-z0-9_]+)" \{(.*?)\n\}`)

// grantBlock is the body of the module's *_iam_member resource that grants
// role, read the way internal/infra/tf's own module walk reads the modules:
// the blocks are the fixture, and a module that grants the role nowhere fails
// the test rather than passing it.
func grantBlock(t *testing.T, module, role string) string {
	t.Helper()
	for _, m := range iamMemberRE.FindAllStringSubmatch("\n"+module, -1) {
		// A predefined role is spelled as the literal "roles/<name>"; one of the
		// installation's own custom roles as the literal path
		// "projects/${var.project}/roles/<id>" (Task 1.5 step 5: a computed
		// project role cannot be told from the deployer's scoped one at plan
		// time), so both spellings name the role.
		if strings.Contains(m[2], `"`+role+`"`) || strings.Contains(m[2], `"projects/${var.project}/roles/`+role+`"`) {
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

// An empty listing is a read that went wrong, never a clean project: a real
// project always lists at least itself and the services it has enabled, so a
// read that answers with nothing is not evidence of anything (Global
// Constraint 3).
func TestInventoryEmptyListingFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name      string
		accounted []StateResource
	}{
		{"nothing accounted", nil},
		{"something accounted", []StateResource{{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAssets{}
			d := Inventory{Project: project, GCPProject: gcpProject, Assets: a, Accounted: tc.accounted}.Check(context.Background())
			if !d.Fallback || d.Cause != CauseEmpty {
				t.Fatalf("an empty listing decided %+v, want a fallback with cause %q", d, CauseEmpty)
			}
			if len(d.Foreign) != 0 {
				t.Errorf("Foreign = %+v, want none: nothing was listed", d.Foreign)
			}
			for _, want := range []string{project, gcpProject, "confirmation"} {
				if !strings.Contains(d.Why, want) {
					t.Errorf("Why does not name %q: %s", want, d.Why)
				}
			}
		})
	}
}

// pagingServer is a Cloud Asset API that answers every page with results
// results and the next-page token next(n) for its n-th request (1-based), and
// counts the requests it was asked. It never ends on its own unless next
// returns "".
func pagingServer(t *testing.T, results int, next func(n int) string) (*CloudAssets, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1))
		w.Header().Set("Content-Type", "application/json")
		var b strings.Builder
		b.WriteString(`{"results":[`)
		for j := range results {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"name":"//compute.googleapis.com/projects/%s/zones/z/disks/d-%d-%d","assetType":"compute.googleapis.com/Disk"}`, gcpProject, i, j)
		}
		fmt.Fprintf(&b, `],"nextPageToken":%q}`, next(i))
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)
	c, err := NewCloudAssets(context.Background(), option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return c, &n
}

// The read is bounded: a server that repeats a page token forever, one that
// hands out new tokens forever, and one whose listing is larger than the read
// will hold all end in an error after a bounded number of requests — never a
// loop, never unbounded memory, never an apply that hangs until its context
// ends (D59's never-halt) — and the error falls the run back. The tests give
// the read a generous context of their own, so a read that only stops at the
// deadline fails on its error and on its request count, not by hanging.
func TestInventoryCloudAssetsReadIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name     string
		results  int
		next     func(n int) string
		maxCalls int64
		says     string
	}{
		{"the same page token forever", 1, func(int) string { return "same" }, 2, "repeated"},
		{"new page tokens forever", 1, func(n int) string { return fmt.Sprintf("t%d", n) }, inventoryMaxPages, "pages"},
		{"more assets than the read holds", inventoryMaxAssets/4 + 1, func(n int) string { return fmt.Sprintf("t%d", n) }, 4, "assets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, calls := pagingServer(t, tc.results, tc.next)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			_, err := c.List(ctx, gcpProject)
			if err == nil {
				t.Fatal("an unbounded listing read as complete")
			}
			if ctx.Err() != nil {
				t.Fatalf("the read stopped only at the context's deadline (%d requests): %v", calls.Load(), err)
			}
			if got := calls.Load(); got > tc.maxCalls {
				t.Errorf("the read made %d requests, want at most %d", got, tc.maxCalls)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the error does not say what bound it hit (%q): %v", tc.says, err)
			}

			d := inventory(c).Check(ctx)
			if !d.Fallback || d.Cause != CauseUnreadable || len(d.Foreign) != 0 {
				t.Errorf("an unbounded listing decided %+v, want a fallback with cause %q and nothing foreign", d, CauseUnreadable)
			}
		})
	}
}

// deadlineAssets records the deadline of the context each read was given.
type deadlineAssets struct {
	deadline time.Time
	ok       bool
}

func (f *deadlineAssets) List(ctx context.Context, _ string) ([]Asset, error) {
	f.deadline, f.ok = ctx.Deadline()
	return []Asset{listed("//cloudresourcemanager.googleapis.com/projects/"+gcpProject, "cloudresourcemanager.googleapis.com/Project")}, nil
}

// A caller that passes a context with no deadline still gets a read that ends:
// the check applies inventoryTimeout. A caller's own, nearer deadline is kept.
func TestInventoryReadHasADeadline(t *testing.T) {
	f := &deadlineAssets{}
	start := time.Now()
	inventory(f).Check(context.Background())
	if !f.ok {
		t.Fatal("the read was given a context with no deadline")
	}
	if f.deadline.Before(start.Add(inventoryTimeout)) || f.deadline.After(time.Now().Add(inventoryTimeout)) {
		t.Errorf("the read's deadline is %v away, want %v", f.deadline.Sub(start), inventoryTimeout)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	inventory(f).Check(ctx)
	if !f.ok || !f.deadline.Equal(want) {
		t.Errorf("the read's deadline is %v, want the caller's own %v", f.deadline, want)
	}
}

// A null element in the listing decodes to a nil resource; reading its name
// would panic and crash the apply. It is a read error, and the run falls back.
func TestInventoryNullListingElementFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"name":"//storage.googleapis.com/a","assetType":"storage.googleapis.com/Bucket"},null]}`)
	}))
	defer srv.Close()
	c, err := NewCloudAssets(context.Background(), option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	d := inventory(c).Check(context.Background())
	if !d.Fallback || d.Cause != CauseUnreadable || len(d.Foreign) != 0 {
		t.Errorf("a null element decided %+v, want a fallback with cause %q and nothing foreign", d, CauseUnreadable)
	}
}

// An error on the second page is an error, not a short listing: reading the
// first page alone as the whole project would account for the rest unseen.
func TestInventoryErrorOnLaterPageFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pageToken") == "" {
			fmt.Fprint(w, `{"results":[{"name":"//storage.googleapis.com/a","assetType":"storage.googleapis.com/Bucket"}],"nextPageToken":"t2"}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"code":500,"message":"backend error"}}`)
	}))
	defer srv.Close()
	c, err := NewCloudAssets(context.Background(), option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.List(context.Background(), gcpProject); err == nil {
		t.Errorf("List = %+v, nil; want an error from page 2", got)
	}
	d := inventory(c).Check(context.Background())
	if !d.Fallback || d.Cause != CauseUnreadable || len(d.Foreign) != 0 {
		t.Errorf("a page-2 error decided %+v, want a fallback with cause %q and nothing foreign", d, CauseUnreadable)
	}
}

// The assets GCP itself creates, and the list the whole-project read ignores
// (Task 4.4's REQUIRED precursor, recorded on #489's review of 2026-10-17).
//
// A real managed project always holds resources GCP created and no Terraform
// state accounts for: the project itself, the services it has enabled, the
// _Default and _Required log buckets and sinks, the default and Google-managed
// service accounts, Cloud Run's revisions and executions, and the images a
// build pushes into Artifact Registry — several of them named by the project
// NUMBER, which the state's ids never carry. Without this list every read of a
// real project falls back, so the apply the gate opens would ask for a
// confirmation every time and 67a's "no fallback" step could not pass.
//
// Each entry is asserted on its own, so a type that stops being ignored fails
// the subtest named for it rather than a listing of thirteen.
func TestInventoryIgnoresTheAssetsGCPItselfCreates(t *testing.T) {
	const number = "123456789012"
	runs := StateResource{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject}
	runsName := "//storage.googleapis.com/projects/_/buckets/fugaro-runs-" + gcpProject

	for _, tc := range []struct {
		what  string
		asset Asset
	}{
		{"the project itself",
			listed("//cloudresourcemanager.googleapis.com/projects/"+gcpProject, "cloudresourcemanager.googleapis.com/Project")},
		{"an enabled service",
			listed("//serviceusage.googleapis.com/projects/"+number+"/services/run.googleapis.com", "serviceusage.googleapis.com/Service")},
		{"the _Default log bucket",
			listed("//logging.googleapis.com/projects/"+gcpProject+"/locations/global/buckets/_Default", "logging.googleapis.com/LogBucket")},
		{"the _Required log bucket",
			listed("//logging.googleapis.com/projects/"+gcpProject+"/locations/global/buckets/_Required", "logging.googleapis.com/LogBucket")},
		{"the _Default log sink",
			listed("//logging.googleapis.com/projects/"+gcpProject+"/sinks/_Default", "logging.googleapis.com/LogSink")},
		{"the _Required log sink",
			listed("//logging.googleapis.com/projects/"+gcpProject+"/sinks/_Required", "logging.googleapis.com/LogSink")},
		{"the Compute default service account",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/"+number+"-compute@developer.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"the Google APIs service agent",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/"+number+"@cloudservices.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"a Google-managed service agent",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/service-"+number+"@gcp-sa-firebase.iam.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"Cloud Run's service agent (a legacy agent domain)",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/service-"+number+"@serverless-robot-prod.iam.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"the App Engine default service account",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/"+gcpProject+"@appspot.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"a Cloud Run revision",
			listed("//run.googleapis.com/projects/"+gcpProject+"/locations/us-east5/revisions/check-00001-abc", "run.googleapis.com/Revision")},
		{"a Cloud Run execution",
			listed("//run.googleapis.com/projects/"+gcpProject+"/locations/us-east5/executions/check-abc", "run.googleapis.com/Execution")},
		{"an image a build pushed",
			listed("//artifactregistry.googleapis.com/projects/"+gcpProject+"/locations/us-east5/repositories/fugaro/dockerImages/sha256:abc", "artifactregistry.googleapis.com/DockerImage")},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a := &fakeAssets{assets: []Asset{tc.asset, listed(runsName, "storage.googleapis.com/Bucket")}}
			d := inventory(a, runs).Check(context.Background())
			if d.Fallback {
				t.Fatalf("a project holding only %s and its accounted bucket fell the run back: %s", tc.what, d.Why)
			}
		})
	}
}

// The other direction, and the one that matters more: a type the list does not
// name is still foreign, and a resource of a SHARED type that GCP did not name
// is still foreign. The list is closed on purpose — a type GCP adds later is
// foreign until it is named here — and it ignores the shared types by the
// name GCP gives them, never by type alone, because a service account or a log
// sink an operator created is a resource the state does not account for.
func TestInventoryStillForeignWhatGCPDidNotCreate(t *testing.T) {
	runs := StateResource{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject}
	runsName := "//storage.googleapis.com/projects/_/buckets/fugaro-runs-" + gcpProject

	for _, tc := range []struct {
		what  string
		asset Asset
	}{
		{"a type the list does not name",
			listed("//compute.googleapis.com/projects/"+gcpProject+"/zones/us-east5-a/instances/somebody-elses", "compute.googleapis.com/Instance")},
		{"a service account an operator created",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/fugaro-build@"+gcpProject+".iam.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"a service account that merely borrows the prefix",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/deployer@fugaro.example.com", "iam.googleapis.com/ServiceAccount")},
		{"a log sink an operator created",
			listed("//logging.googleapis.com/projects/"+gcpProject+"/sinks/audit-export", "logging.googleapis.com/LogSink")},
		{"a log bucket an operator created",
			listed("//logging.googleapis.com/projects/"+gcpProject+"/locations/us-east5/buckets/archive", "logging.googleapis.com/LogBucket")},
		// M1: the service-account forms are matched against THIS project's id
		// and number, never against any run of digits or any domain.
		{"a user-created account named like a service agent, in this project",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/service-1@"+gcpProject+".iam.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"a user-created account carrying this project's number, in this project",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/service-123456789012@"+gcpProject+".iam.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"an account of a foreign project named like a service agent",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/service-123456789012@other-proj-9.iam.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"a service agent whose digits are not this project's number",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/service-999999999999@gcp-sa-firebase.iam.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"another project's Compute default",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/999999999999-compute@developer.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"another project's Google APIs service agent",
			listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/999999999999@cloudservices.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
		{"another project's App Engine default",
			listed("//iam.googleapis.com/projects/other-proj-9/serviceAccounts/other-proj-9@appspot.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a := &fakeAssets{assets: []Asset{tc.asset, listed(runsName, "storage.googleapis.com/Bucket")}}
			d := inventory(a, runs).Check(context.Background())
			if !d.Fallback || d.Cause != CauseForeign {
				t.Fatalf("%s decided %+v, want a fallback with cause %q", tc.what, d, CauseForeign)
			}
			if len(d.Foreign) != 1 || d.Foreign[0].Name != tc.asset.Name {
				t.Errorf("Foreign = %+v, want only %s", d.Foreign, tc.asset.Name)
			}
		})
	}
}

// M1: with no project number the number-named accounts are not this
// project's, so they are foreign and the run falls back — the safe direction
// for a caller that did not read the number.
func TestInventoryWithoutANumberIgnoresNoNumberNamedAccount(t *testing.T) {
	runs := StateResource{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject}
	runsName := "//storage.googleapis.com/projects/_/buckets/fugaro-runs-" + gcpProject
	sa := listed("//iam.googleapis.com/projects/"+gcpProject+"/serviceAccounts/123456789012-compute@developer.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")
	in := inventory(&fakeAssets{assets: []Asset{sa, listed(runsName, "storage.googleapis.com/Bucket")}}, runs)
	in.ProjectNumber = 0
	if d := in.Check(context.Background()); !d.Fallback || d.Cause != CauseForeign {
		t.Fatalf("a number-named account with no project number decided %+v, want a foreign fallback", d)
	}
}

// M1: an account under the project's own domain is one somebody created in
// it, even when the project id itself has the agents' gcp-sa- shape: the
// domain check is on this project's id, not only on the prefix.
func TestInventoryNeverIgnoresAnAccountUnderTheProjectsOwnDomain(t *testing.T) {
	const pid = "gcp-sa-lookalike1"
	runsName := "//storage.googleapis.com/projects/_/buckets/fugaro-runs-" + pid
	sa := listed("//iam.googleapis.com/projects/"+pid+"/serviceAccounts/service-123456789012@"+pid+".iam.gserviceaccount.com", "iam.googleapis.com/ServiceAccount")
	in := Inventory{Project: project, GCPProject: pid, ProjectNumber: testProjectNumber,
		Assets:    &fakeAssets{assets: []Asset{sa, listed(runsName, "storage.googleapis.com/Bucket")}},
		Accounted: []StateResource{{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + pid}}}
	if d := in.Check(context.Background()); !d.Fallback || d.Cause != CauseForeign {
		t.Fatalf("an account under %s's own domain decided %+v, want a foreign fallback", pid, d)
	}
}

// The third thing that is not foreign: the resources Fugaro's own owner-run
// commands created and no state accounts for — the installation's state bucket
// (infra.CreateStateBucket), D77's managed state bucket, and the default
// Firestore database Firebase provisions when the project is created
// (infra.AddFirebase). The whole-project read lists every one of them, and
// without this the accounted side could never cover them: a real managed
// project would fall back on every apply and the agent apply the budget gate
// opens would ask for a confirmation every time.
//
// Each is asserted on its own, with the project's accounted runs bucket beside
// it, so the listing is otherwise clean.
func TestInventoryIgnoresWhatTheInstallationItselfCreated(t *testing.T) {
	runs := StateResource{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject}
	runsName := "//storage.googleapis.com/projects/_/buckets/fugaro-runs-" + gcpProject

	for _, tc := range []struct {
		what  string
		asset Asset
	}{
		{"the installation's own state bucket",
			listed("//storage.googleapis.com/"+InstallationsStateBucket(gcpProject), "storage.googleapis.com/Bucket")},
		{"D77's managed state bucket",
			listed("//storage.googleapis.com/"+ManagedStateBucketName(gcpProject), "storage.googleapis.com/Bucket")},
		{"the default Firestore database Firebase provisions",
			listed("//firestore.googleapis.com/projects/"+gcpProject+"/databases/(default)", "firestore.googleapis.com/Database")},
		{"the Firebase project infra.AddFirebase adds to every created project",
			listed("//firebase.googleapis.com/projects/"+gcpProject, "firebase.googleapis.com/FirebaseProject")},
		{"the creation fact the owner's run writes into the runs bucket",
			listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-"+gcpProject+"/objects/fugaro/managed.json",
				"storage.googleapis.com/Object")},
		{"the owner's allow-delete list, beside the creation fact",
			listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-"+gcpProject+"/objects/fugaro/allow-delete.json",
				"storage.googleapis.com/Object")},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a := &fakeAssets{assets: []Asset{tc.asset, listed(runsName, "storage.googleapis.com/Bucket")}}
			d := inventory(a, runs).Check(context.Background())
			if d.Fallback {
				t.Fatalf("a project holding only %s and its accounted bucket fell the run back: %s", tc.what, d.Why)
			}
		})
	}
	// The names are this project's, so with no project id nothing is claimed:
	// the direction a caller that did not read the id must take (Check refuses
	// such an inventory before it lists anything, so this is asserted on the
	// rule itself).
	none := Inventory{Project: project}
	for _, asset := range []Asset{
		listed("//storage.googleapis.com/"+InstallationsStateBucket(gcpProject), "storage.googleapis.com/Bucket"),
		listed("//storage.googleapis.com/"+ManagedStateBucketName(gcpProject), "storage.googleapis.com/Bucket"),
		listed("//firestore.googleapis.com/projects/"+gcpProject+"/databases/(default)", "firestore.googleapis.com/Database"),
		listed("//firebase.googleapis.com/projects/"+gcpProject, "firebase.googleapis.com/FirebaseProject"),
		listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-"+gcpProject+"/objects/fugaro/managed.json",
			"storage.googleapis.com/Object"),
		listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-"+gcpProject+"/objects/fugaro/allow-delete.json",
			"storage.googleapis.com/Object"),
	} {
		if none.installationCreated(asset) {
			t.Errorf("with no project id %s was claimed as the installation's own", asset.Name)
		}
	}
}

// And the other direction, which is the one that matters: only the exact names
// are claimed. A bucket or database that merely borrows their shape is one
// somebody created in the project, and the project is single-purpose (D59)
// whatever else it holds.
func TestInventoryStillForeignWhatTheInstallationDidNotCreate(t *testing.T) {
	runs := StateResource{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject}
	runsName := "//storage.googleapis.com/projects/_/buckets/fugaro-runs-" + gcpProject

	for _, tc := range []struct {
		what  string
		asset Asset
	}{
		{"another project's state bucket",
			listed("//storage.googleapis.com/"+InstallationsStateBucket("other-proj"), "storage.googleapis.com/Bucket")},
		{"another project's managed state bucket",
			listed("//storage.googleapis.com/"+ManagedStateBucketName("other-proj"), "storage.googleapis.com/Bucket")},
		{"a bucket named like this project's state bucket with a suffix",
			listed("//storage.googleapis.com/"+InstallationsStateBucket(gcpProject)+"-backup", "storage.googleapis.com/Bucket")},
		{"a bucket named like this project's managed state bucket with a suffix",
			listed("//storage.googleapis.com/"+ManagedStateBucketName(gcpProject)+"-backup", "storage.googleapis.com/Bucket")},
		{"a Firestore database somebody created",
			listed("//firestore.googleapis.com/projects/"+gcpProject+"/databases/agent-db", "firestore.googleapis.com/Database")},
		{"another project's default Firestore database",
			listed("//firestore.googleapis.com/projects/other-proj/databases/(default)", "firestore.googleapis.com/Database")},
		{"a database of another type in this project",
			listed("//firebasedatabase.googleapis.com/projects/"+gcpProject+"/locations/us-east5/instances/rtdb", "firebasedatabase.googleapis.com/Instance")},
		{"another project's Firebase project",
			listed("//firebase.googleapis.com/projects/other-proj", "firebase.googleapis.com/FirebaseProject")},
		{"a Firebase project nested under this one's path",
			listed("//firebase.googleapis.com/projects/"+gcpProject+"/extra/"+gcpProject, "firebase.googleapis.com/FirebaseProject")},
		{"a creation fact in another project's runs bucket",
			listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-other-proj/objects/fugaro/managed.json",
				"storage.googleapis.com/Object")},
		{"an allow-delete list in another project's runs bucket",
			listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-other-proj/objects/fugaro/allow-delete.json",
				"storage.googleapis.com/Object")},
		{"another object under fugaro/ in this project's runs bucket",
			listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-"+gcpProject+"/objects/fugaro/notes.json",
				"storage.googleapis.com/Object")},
		{"a run's own record in this project's runs bucket",
			listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-"+gcpProject+"/objects/runs/2026-10-19/abc.json",
				"storage.googleapis.com/Object")},
		{"a creation fact in a bucket of this project that is not the runs bucket",
			listed("//storage.googleapis.com/projects/_/buckets/"+InstallationsStateBucket(gcpProject)+"/objects/fugaro/managed.json",
				"storage.googleapis.com/Object")},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a := &fakeAssets{assets: []Asset{tc.asset, listed(runsName, "storage.googleapis.com/Bucket")}}
			d := inventory(a, runs).Check(context.Background())
			if !d.Fallback || d.Cause != CauseForeign {
				t.Fatalf("%s decided %+v, want a fallback with cause %q", tc.what, d, CauseForeign)
			}
			if len(d.Foreign) != 1 || d.Foreign[0].Name != tc.asset.Name {
				t.Errorf("Foreign = %+v, want only %s", d.Foreign, tc.asset.Name)
			}
		})
	}
}
