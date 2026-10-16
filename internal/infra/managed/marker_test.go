package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// The fakes' project and its runs bucket, named as the installation module
// names one (fugaro-runs-<gcp project>); the same figures internal/infra's
// tests use.
const (
	testProject       = "proj-1234"
	testBucket        = "fugaro-runs-proj-1234"
	testProjectNumber = 123456789012
)

var testManaged = map[string]string{gcp.LabelManaged: gcp.ManagedValue}

// markerJSON is the object's whole shape, byte for byte: the fixture a
// person reads and the read of this package parses. Two fields, a version
// and the project the fact is about — no token, no key, no email: the
// marker's value is never a credential (Task 0.1's rule), and anything
// added to it has to come through this constant.
const markerJSON = `{"version":1,"gcp_project":"proj-1234"}`

func newStorage(t *testing.T) (*storage.Service, *gcpfake.GCS) {
	t.Helper()
	f := gcpfake.NewGCS(t)
	s, err := storage.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/storage/v1/"))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

// readMarker is the object's bytes and whether it is there at all.
func readMarker(t *testing.T, s *storage.Service, bucket string) ([]byte, bool) {
	t.Helper()
	resp, err := s.Objects.Get(bucket, MarkerObject).Download()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return nil, false
		}
		t.Fatalf("reading gs://%s/%s: %v", bucket, MarkerObject, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b, true
}

// objects is every object in the bucket: what a refusal must leave untouched.
func objects(t *testing.T, s *storage.Service, bucket string) []string {
	t.Helper()
	l, err := s.Objects.List(bucket).Do()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range l.Items {
		names = append(names, o.Name)
	}
	return names
}

// The object's shape and its round trip, against the fixture above.
func TestMarkerJSONShape(t *testing.T) {
	m := Marker{Version: MarkerVersion, GCPProject: testProject}
	b, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != markerJSON {
		t.Errorf("Bytes() = %q, want %q", b, markerJSON)
	}
	var back Marker
	if err := json.Unmarshal([]byte(markerJSON), &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, m) {
		t.Errorf("the fixture parses as %+v, want %+v", back, m)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(markerJSON), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 {
		t.Errorf("the object holds %d fields (%v): a fact, not a credential", len(fields), fields)
	}
	if _, err := (Marker{Version: MarkerVersion}).Bytes(); err == nil {
		t.Error("a marker that names no project was encoded")
	}
	if _, err := (Marker{Version: MarkerVersion + 1, GCPProject: testProject}).Bytes(); err == nil {
		t.Error("a marker of another version was encoded")
	}
}

// The write, read back through the API a person and the read of this
// package both use; and a second call, which is an error rather than an
// update: the fact is written once, from the one place that knows it.
func TestWriteMarkerRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	if err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber); err != nil {
		t.Fatal(err)
	}
	b, ok := readMarker(t, s, testBucket)
	if !ok || string(b) != markerJSON {
		t.Fatalf("gs://%s/%s = %q (present %v), want %q", testBucket, MarkerObject, b, ok, markerJSON)
	}
	err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber)
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second write: %v, want a refusal naming the object as already there", err)
	}
	if b, _ := readMarker(t, s, testBucket); string(b) != markerJSON {
		t.Errorf("the second call changed the object to %q", b)
	}
}

// A run that created no project records nothing, whatever else it did, and
// writes nothing at all into the bucket — not the object, not anything
// else. This is the property the task exists for: the marker is what turns
// the confirmations off, so a write that does not read the fact would mark
// every project init touches, belong and the owner's own included.
func TestWriteMarkerWritesNothingWithoutTheFact(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		bucket, created, project string
		number                   uint64
	}{
		"the project this run did not create": {testBucket, "", testProject, testProjectNumber},
		"a bucket that is not there":          {"fugaro-runs-elsewhere", "", testProject, testProjectNumber},
		"another project named":               {testBucket, "", "belong", testProjectNumber},
		"no bucket named and no number":       {"", "", testProject, 0},
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			if err := WriteMarker(ctx, s, tc.bucket, tc.created, tc.project, tc.number); err != nil {
				t.Fatalf("err = %v, want a silent no-op", err)
			}
			if _, ok := readMarker(t, s, testBucket); ok {
				t.Error("the object was written")
			}
			if names := objects(t, s, testBucket); len(names) != 0 {
				t.Errorf("the bucket holds %v, want nothing", names)
			}
		})
	}
}

// The one thing a run must not be able to do with the fact it holds: mark a
// project it did not create. init passes the project this run created and
// the project it is installing into (r.res.Created and r.gcpProject), which
// are the same value in the one run that reaches here; a pair that differs
// is a refusal naming both, never a marker for either.
func TestWriteMarkerRefusesAProjectThisRunDidNotCreate(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct{ created, project string }{
		"another project": {testProject, "belong"},
		"nothing to mark": {testProject, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			err := WriteMarker(ctx, s, testBucket, tc.created, tc.project, testProjectNumber)
			if err == nil {
				t.Fatal("want a refusal")
			}
			if tc.created != "" && !strings.Contains(err.Error(), tc.project) {
				t.Errorf("err = %v, want it to name %s", err, tc.project)
			}
			if names := objects(t, s, testBucket); len(names) != 0 {
				t.Errorf("a refused call wrote %v", names)
			}
		})
	}
}

// The bucket the fact goes into must be the created project's own, proved
// the way ReadProjectMarker proves one (internal/infra/project.go): its own
// project number, and the label the installation sets. A bucket name is
// global, so neither alone is worth anything — and a marker written into
// somebody else's bucket is a fact about the wrong project.
func TestWriteMarkerNeedsTheProjectsOwnRunsBucket(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		bucket string
		number uint64
		labels map[string]string
	}{
		"no bucket number":                  {testBucket, 0, testManaged},
		"another project's":                 {testBucket, 999, testManaged},
		"an unlabelled bucket":              {testBucket, testProjectNumber, nil},
		"a bucket labelled by someone else": {testBucket, testProjectNumber, map[string]string{gcp.LabelManaged: "managed-by-someone"}},
		"no bucket named":                   {"", testProjectNumber, testManaged},
		"a bucket that is not there":        {"fugaro-runs-elsewhere", testProjectNumber, testManaged},
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, tc.labels)
			err := WriteMarker(ctx, s, tc.bucket, testProject, testProject, tc.number)
			if err == nil {
				t.Fatal("want a refusal")
			}
			if _, ok := readMarker(t, s, testBucket); ok {
				t.Error("a refused call wrote the object")
			}
		})
	}
	// The labelled bucket with the right number is the one case that works,
	// so the table above is not passing for the wrong reason.
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	if err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber); err != nil {
		t.Fatalf("the project's own runs bucket was refused: %v", err)
	}
}

// The write half of Global Constraint 4 (design D57): no credential a run
// holds may have a write path to the marker. The agent of a run holds a job
// account and a build holds a build account, and the grant each is given on
// the runs bucket is roles/storage.objectUser under a name-prefix
// condition; this is the assertion that no such condition reaches the
// bucket's root, where fugaro/managed.json sits beside fugaro/project.json.
//
// It covers the two sources of every grant: the Go that builds the
// conditions the modules are handed (gcp.BucketCondition and
// gcp.LauncherBucketCondition, whose prefixes are asserted here), and the
// modules themselves, where every grant of a write role on the runs bucket
// to a run's own credential must be a conditioned one that this test can
// read, and no member may hold a storage write role on the project, which
// the bucket's policy does not bound (SECURITY.md:56). The live bindings are
// Check 68, owner-run, because Global Constraint 12 forbids a live IAM read
// from a test.
func TestRunAccountsHaveNoWritePathToTheMarker(t *testing.T) {
	// The objects at the bucket's root that a run must never write: the
	// creation fact this task writes, and the delete list Task 1.4 will.
	root := []string{MarkerObject, "fugaro/allow-delete.json"}
	for name, expr := range map[string]string{
		"a job account's grant":   gcp.BucketCondition(testBucket, gcp.JobBucketPrefixes, "web-a1b2c3"),
		"a build account's grant": gcp.BucketCondition(testBucket, gcp.BuildBucketPrefixes, "web"),
		"a launcher's grant":      gcp.LauncherBucketCondition(testBucket),
	} {
		prefixes := startsWithPrefixes(expr, testBucket)
		if len(prefixes) == 0 {
			t.Fatalf("%s: %q names no prefix, so this assertion is vacuous", name, expr)
		}
		for _, object := range root {
			if slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(object, p) }) {
				t.Errorf("%s reaches %s: %s", name, object, expr)
			}
		}
	}
	// The prefixes the fakes and the modules agree on, named here so a
	// widening of one is a visible diff: a job writes runs/, cache/ and
	// locks/, a build writes builds/, a launcher writes runs/.
	if !slices.Equal(gcp.JobBucketPrefixes, []string{"runs", "cache", "locks"}) {
		t.Errorf("gcp.JobBucketPrefixes = %v", gcp.JobBucketPrefixes)
	}
	if !slices.Equal(gcp.BuildBucketPrefixes, []string{"builds"}) {
		t.Errorf("gcp.BuildBucketPrefixes = %v", gcp.BuildBucketPrefixes)
	}
	if got := startsWithPrefixes(gcp.LauncherBucketCondition(testBucket), testBucket); !slices.Equal(got, []string{"runs/"}) {
		t.Errorf("the launcher condition grants %v", got)
	}

	scan := scanModuleIAM(moduleFiles(t))
	for _, f := range scan.findings {
		t.Errorf("%s", f)
	}
	// Non-vacuity, one count per shape of grant the scan exists for. The
	// modules condition the job account's, the build account's and the
	// launchers' writes on the runs bucket; the operators' own unconditioned
	// grant is the documented limit; and the project-level and custom-role
	// shapes are the ones a scan of google_storage_bucket_iam_member alone
	// would miss.
	if scan.writes < 4 {
		t.Errorf("the scan found %d write grants on the runs bucket, want the job account's, the build account's, the launchers' and the operators'", scan.writes)
	}
	if scan.saWrites < 2 || scan.narrowed < 3 {
		t.Errorf("the scan found %d write grants to a run account and %d narrowing conditions, want 2 and 3 (the job account's, the build account's and the launchers')", scan.saWrites, scan.narrowed)
	}
	if scan.projectGrants < 10 {
		t.Errorf("the scan read %d project-level grants, want the modules' (the firebase, history, build and vertex bindings)", scan.projectGrants)
	}
	if scan.customRoles < 5 {
		t.Errorf("the scan read %d custom roles, want the modules'", scan.customRoles)
	}
	// Each of the three condition variables is the one the Go side builds
	// (internal/infra/spec.go:652 and :809 set the first two from
	// gcp.BucketCondition, installation/iam.tf's precondition pins the
	// launcher's): one that stops being used is a diff here, not a silent
	// narrowing of this assertion.
	for _, v := range []string{"var.workflow.bucket_condition.expression", "var.repo.build_bucket_condition.expression", "var.launcher_bucket_condition.expression"} {
		if scan.conditions[v] == 0 {
			t.Errorf("no write grant on the runs bucket carries %s: the scan accepted a condition variable nothing uses", v)
		}
	}
}

// TestTheModuleScanCatchesTheMutations is the red step of the scan below: an
// independent review made each of these edits to the modules and found the
// test above still passing. They are fixtures here, the modules as they are
// plus one edit each, and each must be a finding: a mutation that reaches
// the creation fact is exactly what this test exists to refuse.
func TestTheModuleScanCatchesTheMutations(t *testing.T) {
	for name, mutate := range map[string]func(files map[string]string){
		// A project-level storage role on a service account: only
		// google_storage_bucket_iam_member used to be scanned, and a bucket
		// policy does not bound the project's (SECURITY.md:56). The same
		// shape with a custom role is the fixture after it.
		"a project-level storage role to a run account": func(f map[string]string) {
			f["workflow/sa.tf"] += `
resource "google_project_iam_member" "job_storage" {
  project = var.project
  role    = "roles/storage.objectAdmin"
  member  = google_service_account.job.member
}
`
		},
		// A custom project role holding storage.objects permissions, granted
		// at the project level.
		"a custom role with storage permissions at the project level": func(f map[string]string) {
			f["workflow/sa.tf"] += `
resource "google_project_iam_custom_role" "job_storage" {
  project     = var.project
  role_id     = "jobStorageWriter"
  title       = "job storage writer"
  permissions = ["storage.objects.create", "storage.objects.delete"]
}

resource "google_project_iam_member" "job_custom_storage" {
  project = var.project
  role    = google_project_iam_custom_role.job_storage.name
  member  = google_service_account.job.member
}
`
		},
		// each.value used to be read as "a person": a for_each over a
		// service account's member makes each.value the service account.
		"a for_each over a run account": func(f map[string]string) {
			f["workflow/sa.tf"] += `
resource "google_storage_bucket_iam_member" "job_batch" {
  for_each = toset([google_service_account.job.member])

  bucket = var.runs_bucket
  role   = "roles/storage.objectAdmin"
  member = each.value
}
`
		},
		// The binding form, which takes members instead of a member.
		"a bucket binding to a run account with no condition": func(f map[string]string) {
			f["workflow/sa.tf"] += `
resource "google_storage_bucket_iam_binding" "job_batch" {
  bucket  = var.runs_bucket
  role    = "roles/storage.objectAdmin"
  members = ["serviceAccount:job@example.iam.gserviceaccount.com"]
}
`
		},
		// The policy form: its bindings live in a policy_data document this
		// scan does not read, and a shape it cannot read is a finding rather
		// than a pass.
		"a bucket policy to a run account": func(f map[string]string) {
			f["workflow/sa.tf"] += `
resource "google_storage_bucket_iam_policy" "job" {
  bucket      = var.runs_bucket
  policy_data = data.google_iam_policy.job.policy_data
}
`
		},
		// A condition that is not narrowing: "true" is a condition and
		// narrows nothing, and a variable this test does not recognise is no
		// better — the job grant's own two are the ones the Go side builds.
		"a condition that is true": func(f map[string]string) {
			f["workflow/sa.tf"] = strings.Replace(f["workflow/sa.tf"],
				"expression = var.workflow.bucket_condition.expression", `expression = "true"`, 1)
		},
		"a condition this test does not recognise": func(f map[string]string) {
			f["workflow/sa.tf"] = strings.Replace(f["workflow/sa.tf"],
				"expression = var.workflow.bucket_condition.expression", "expression = var.wider_bucket_condition.expression", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			files := moduleFiles(t)
			mutate(files)
			if scan := scanModuleIAM(files); len(scan.findings) == 0 {
				t.Error("the scan found nothing in this mutation, so it reaches the creation fact")
			}
		})
	}
	// The modules as they are are the other half of the fixture: the scan
	// above must find nothing in them, or the mutations would be passing for
	// a reason that has nothing to do with them.
	if scan := scanModuleIAM(moduleFiles(t)); len(scan.findings) != 0 {
		t.Errorf("the modules as they are: %v", scan.findings)
	}
}

// moduleFiles is the Terraform modules' sources: path (relative to the
// modules directory) → content.
func moduleFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules")
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("read %d module files, want the modules' own", len(files))
	}
	return files
}

// startsWithPrefixes is the object prefixes an IAM condition expression
// grants on bucket.
func startsWithPrefixes(expr, bucket string) []string {
	var out []string
	for _, m := range startsWithRE.FindAllStringSubmatch(expr, -1) {
		if m[1] != bucket {
			continue
		}
		out = append(out, m[2])
	}
	return out
}

// startsWithRE reads an object-path prefix condition: the bucket it names
// and what follows objects/ in it.
var startsWithRE = regexp.MustCompile(`startsWith\("projects/_/buckets/([^/]+)/objects/([^"]*)"\)`)

// iamScan is what a scan of the modules' IAM found: findings, empty being
// the property the test asserts, and the counts and names that keep the
// findings from being vacuous.
type iamScan struct {
	findings []string

	// writes is every grant of a write role on the runs bucket; saWrites the
	// ones to a run's own credential (a service account); narrowed the
	// grants whose condition this scan recognised as keeping the grant off
	// the bucket's root; projectGrants every project-level grant read;
	// customRoles every custom role read; conditions how often each accepted
	// condition variable was used.
	writes, saWrites, narrowed, projectGrants, customRoles int
	conditions                                             map[string]int
}

func (s *iamScan) failf(format string, a ...any) {
	s.findings = append(s.findings, fmt.Sprintf(format, a...))
}

// scanModuleIAM reads every IAM resource the Terraform modules define (path
// → content) and reports what may give a run's credential a write path to
// the creation fact at the runs bucket's root.
//
// It covers every shape a grant takes in Terraform — _iam_member,
// _iam_binding, _iam_policy and the custom roles' permissions — at both
// levels that reach the bucket: the bucket's own policy and the project's,
// which the bucket's policy does not bound (SECURITY.md:56). A write role on
// the runs bucket is accepted only when it is granted to a set of people
// (the operators' own access, the documented limit) or when the grant's
// condition is one this test recognises as keeping it off the bucket's root.
// The rest are findings, including a shape this scan cannot read: a
// policy_data grant, a role it cannot resolve, a for_each that is not a set
// of people, and a condition that narrows nothing.
func scanModuleIAM(files map[string]string) iamScan {
	s := iamScan{conditions: map[string]int{}}
	resources := moduleResources(files)
	roles := customRolePermissions(resources)
	locals := moduleLocals(files)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths) // findings in a stable order
	for _, path := range paths {
		for _, m := range tfResourceRE.FindAllStringSubmatch(files[path], -1) {
			r := tfResource{path: path, typ: m[1], name: m[2], body: m[3]}
			switch r.typ {
			case "google_project_iam_custom_role":
				s.customRoles++
			case "google_storage_bucket_iam_member", "google_storage_bucket_iam_binding", "google_storage_bucket_iam_policy":
				s.checkBucketGrant(r, roles, locals)
			case "google_project_iam_member", "google_project_iam_binding", "google_project_iam_policy":
				s.checkProjectGrant(r, roles)
			}
		}
	}
	return s
}

// tfResource is one Terraform resource block.
type tfResource struct{ path, typ, name, body string }

// moduleResources is every resource block in files, in path order.
func moduleResources(files map[string]string) []tfResource {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	var out []tfResource
	for _, path := range paths {
		for _, m := range tfResourceRE.FindAllStringSubmatch(files[path], -1) {
			out = append(out, tfResource{path: path, typ: m[1], name: m[2], body: m[3]})
		}
	}
	return out
}

// tfResourceRE is a resource block; the resource's own closing brace is the
// first at column zero, which is also why nested blocks (a condition, a
// lifecycle and its precondition) stay inside the body.
var tfResourceRE = regexp.MustCompile(`(?s)\nresource "([a-z0-9_]+)" "([a-z0-9_]+)" \{\n(.*?)\n\}`)

// runsBucketSources are the expressions that are the runs bucket in the
// modules: the installation module's own resource, and the bucket the
// installation's outputs hand the other modules.
var runsBucketSources = map[string]bool{
	"google_storage_bucket.runs.name": true,
	"var.runs_bucket":                 true,
	"var.installation.runs_bucket":    true,
}

// customRolePermissions maps every custom role the modules define to its
// permissions, under two names: the resource it is (the modules refer to it
// as google_project_iam_custom_role.launcher.name, say) and the last segment
// of its role_id (var.installation.role_ids.launcher, say, which is how the
// repository module reaches the installation's roles).
func customRolePermissions(resources []tfResource) map[string][]string {
	roles := map[string][]string{}
	for _, r := range resources {
		if r.typ != "google_project_iam_custom_role" {
			continue
		}
		perms, _ := stringList(r.body, "permissions")
		roles["google_project_iam_custom_role."+r.name] = perms
		if id := field(r.body, "role_id"); id != "" {
			roles[lastSegment(unquote(id))] = perms
		}
	}
	return roles
}

// moduleLocals is every locals block's values, name → expression: the for_each
// sets are built from them (firebase/iam.tf's local.people is the launchers
// and the operators).
func moduleLocals(files map[string]string) map[string]string {
	out := map[string]string{}
	for _, content := range files {
		for _, m := range tfLocalsRE.FindAllStringSubmatch(content, -1) {
			for _, l := range strings.Split(m[1], "\n") {
				if i := strings.Index(l, "#"); i >= 0 {
					l = l[:i]
				}
				if k, v, ok := strings.Cut(l, "="); ok {
					out[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
		}
	}
	return out
}

var tfLocalsRE = regexp.MustCompile(`(?s)\nlocals \{\n(.*?)\n\}`)

// peopleVars are the variables a set of people is built from: the only
// member sets the modules may grant an unconditioned write on the runs
// bucket to (operators hold the bucket's root by design, SECURITY.md:56, and
// the launchers' own write is conditioned).
var peopleVars = map[string]bool{"var.operators": true, "var.launchers": true, "var.admins": true, "var.budget_admins": true}

// peopleSet reports whether a for_each expression is a set of people:
// var.operators and var.launchers, directly or through a locals block. A
// reference to anything else — a service account, a literal, a variable this
// test does not know — is not one, so each.value over it is not a person.
func peopleSet(expr string, locals map[string]string) bool { return peopleSetDepth(expr, locals, 0) }

func peopleSetDepth(expr string, locals map[string]string, depth int) bool {
	expr = strings.TrimSpace(expr)
	refs := tfRefRE.FindAllString(expr, -1)
	if len(refs) == 0 || depth > 8 {
		return false
	}
	for _, ref := range refs {
		switch {
		case strings.HasPrefix(ref, "local."):
			v, ok := locals[strings.TrimPrefix(ref, "local.")]
			if !ok || !peopleSetDepth(v, locals, depth+1) {
				return false
			}
		case peopleVars[ref]:
		default:
			return false // a service account, or a variable nothing here knows
		}
	}
	return true // every reference is a set of people
}

// tfRefRE reads the references a Terraform expression makes: a variable, a
// local, a resource.
var tfRefRE = regexp.MustCompile(`\b(?:var|local|data|google_[a-z0-9_]+|each)\.[a-z0-9_.\[\]-]+`)

// classifyRole is what a role can do to the runs bucket: write it, read it,
// or nothing this test is about.
type roleKind int

const (
	roleOther   roleKind = iota // a role of another API: it cannot reach storage
	roleRead                    // storage reads, which take nothing from the bucket policy
	roleWrite                   // storage writes, which are the whole question
	roleUnknown                 // a role this test cannot resolve
)

var (
	storageWriteRoles = map[string]bool{
		"roles/storage.objectAdmin": true, "roles/storage.objectUser": true, "roles/storage.objectCreator": true,
		"roles/storage.admin": true, "roles/storage.legacyBucketOwner": true, "roles/storage.legacyBucketWriter": true,
		"roles/owner": true, "roles/editor": true,
	}
	storageReadRoles = map[string]bool{
		"roles/storage.objectViewer": true, "roles/storage.legacyBucketReader": true, "roles/storage.legacyObjectReader": true,
		"roles/storage.objectLister": true,
	}
)

// classifyRole resolves a role reference: a standard role by name, a custom
// role the modules define by its permissions (only a storage permission
// matters here — a custom role without one cannot write the bucket), and
// everything else by whether it names storage at all. A reference this test
// cannot resolve is roleUnknown, which is a finding wherever it is used.
func classifyRole(role string, roles map[string][]string) roleKind {
	role = strings.TrimSpace(role)
	switch {
	case role == "":
		return roleUnknown
	case strings.HasPrefix(role, `"`):
		return classifyRole(unquote(role), roles)
	case storageWriteRoles[role]:
		return roleWrite
	case storageReadRoles[role]:
		return roleRead
	case strings.HasPrefix(role, "roles/"):
		if strings.Contains(role, "storage") {
			return roleUnknown // a storage role this test has not classified
		}
		return roleOther
	}
	perms, ok := roles[customRoleRef(role)]
	if !ok {
		return roleUnknown
	}
	kind := roleOther
	for _, p := range perms {
		if !strings.HasPrefix(p, "storage.") {
			continue
		}
		if storageReadPerms[p] {
			if kind == roleOther {
				kind = roleRead
			}
			continue
		}
		return roleWrite // storage.objects.* or storage.buckets.*, minus the reads below
	}
	return kind
}

// storageReadPerms are the only storage permissions a custom role may hold
// and stay a read: anything else in the storage API (create, delete, update,
// a wildcard) is a write.
var storageReadPerms = map[string]bool{
	"storage.objects.get": true, "storage.objects.list": true,
	"storage.buckets.get": true, "storage.buckets.list": true,
	"storage.objects.getIamPolicy": true, "storage.buckets.getIamPolicy": true,
}

// customRoleRef is a custom role reference as customRolePermissions keys it:
// google_project_iam_custom_role.history[0].name is the resource, and
// var.installation.role_ids.build_submitter the role_id's last segment.
func customRoleRef(role string) string {
	role = strings.ReplaceAll(strings.ReplaceAll(role, "[0]", ""), ".name", "")
	if strings.HasPrefix(role, "google_project_iam_custom_role.") {
		return role
	}
	return lastSegment(role)
}

// lastSegment is the last dot-separated segment of a Terraform reference.
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// checkBucketGrant is one grant on the runs bucket's own policy: who it is
// to, and whether what they are given can reach the bucket's root.
func (s *iamScan) checkBucketGrant(r tfResource, roles map[string][]string, locals map[string]string) {
	if !runsBucketSources[field(r.body, "bucket")] {
		return // another bucket: the state bucket's and the registry's own grants
	}
	members, ok := grantMembers(r)
	if !ok {
		s.failf("%s: %s is a grant whose bindings live in a policy_data document this test does not read, so nothing here says who may write %s; use the member or the binding form, or extend this test",
			r.path, r.name, MarkerObject)
		return
	}
	cond := conditionExpr(r.body)
	forEach := field(r.body, "for_each")
	for _, role := range grantRoles(r, roles) {
		kind := classifyRole(role, roles)
		switch kind {
		case roleRead:
			// Nothing to say: the reads the bucket policy already gives.
		case roleWrite:
			s.writes++
			for _, member := range members {
				person := isPersonSet(member, forEach, locals)
				if member == "each.value" && !person {
					s.failf("%s: %s grants %s on the runs bucket over for_each = %s, so each.value is whoever that is, not a set of people: a run's own credential must have no write path to the bucket's root (%s)",
						r.path, r.name, role, forEach, MarkerObject)
				}
				if !person && cond == "" {
					s.failf("%s: %s grants %s on the runs bucket to %q with no condition: only the operators' own identities hold an unconditioned grant there (SECURITY.md:56)",
						r.path, r.name, role, member)
					continue
				}
				if cond != "" && !s.narrowing(cond) {
					s.failf("%s: %s grants %s on the runs bucket and conditions it with %q, which this test does not read as keeping the grant off the bucket's root (%s)",
						r.path, r.name, role, cond, MarkerObject)
					continue
				}
				if !person {
					s.saWrites++
				}
				if cond != "" {
					s.narrowed++
				}
			}
		default:
			s.failf("%s: %s grants %q on the runs bucket, which this test cannot classify as a read or a write grant: classify it here (or drop it) before it ships",
				r.path, r.name, role)
		}
	}
}

// checkProjectGrant is one project-level grant. The project's policy is not
// bounded by the runs bucket's (SECURITY.md:56): a storage write role there
// reaches the bucket's root whatever the bucket says, so no member may hold
// one.
func (s *iamScan) checkProjectGrant(r tfResource, roles map[string][]string) {
	rolesOf := grantRoles(r, roles)
	if len(rolesOf) == 0 {
		s.failf("%s: %s is a project-level grant this test cannot read: give it a role, or extend this test", r.path, r.name)
		return
	}
	s.projectGrants++
	for _, role := range rolesOf {
		switch classifyRole(role, roles) {
		case roleWrite:
			s.failf("%s: %s grants the storage write role %s at the project level, where the runs bucket's policy does not bound it (SECURITY.md:56): no member may hold one, whatever its condition",
				r.path, r.name, role)
		case roleUnknown:
			s.failf("%s: %s grants %q at the project level, which this test cannot resolve to what it can do: resolve it here before it ships",
				r.path, r.name, role)
		}
	}
}

// narrowing is whether a condition expression keeps a grant off the bucket's
// root: one of the condition variables the Go side builds — the input byte
// for byte to the modules (internal/infra/spec.go:652 and :809 build the
// job's and the build's from gcp.BucketCondition, installation/iam.tf's
// lifecycle precondition pins the launcher's to gcp.LauncherBucketCondition,
// and the prefixes all three hold are asserted at the top of the test
// above) — or a literal startsWith naming a prefix under objects/.
//
// "true" is a condition and narrows nothing; a variable nothing here knows
// could be anything. Neither is one.
func (s *iamScan) narrowing(expr string) bool {
	expr = strings.TrimSpace(expr)
	if narrowingConditions[expr] {
		s.conditions[expr]++
		return true
	}
	ms := startsWithRE.FindAllStringSubmatch(expr, -1)
	if len(ms) == 0 {
		return false
	}
	for _, m := range ms {
		if m[2] == "" {
			return false // the bucket's root itself, where the marker is
		}
	}
	return true
}

// narrowingConditions are the condition expressions the Go side builds: the
// job account's, the build account's and the launchers'. A condition that
// differs is a different binding, and one this test has not been taught to
// read.
var narrowingConditions = map[string]bool{
	"var.workflow.bucket_condition.expression":   true,
	"var.repo.build_bucket_condition.expression": true,
	"var.launcher_bucket_condition.expression":   true,
}

// grantMembers is who a grant is to: the member attribute, or the members of
// the binding form. ok is false for a grant whose bindings this test cannot
// read (the policy form's policy_data document).
func grantMembers(r tfResource) (members []string, ok bool) {
	if m := field(r.body, "member"); m != "" {
		return []string{m}, true
	}
	if m := field(r.body, "members"); m != "" {
		ms, ok := stringList(r.body, "members")
		if !ok {
			return nil, false
		}
		return ms, true
	}
	return nil, false
}

// grantRoles is a grant's roles: the role attribute, or every value of the
// for_each when the role is each.value over a map literal (the repository
// module's build grants two roles from one block: a log writer and the
// installation's build submitter).
func grantRoles(r tfResource, roles map[string][]string) []string {
	if rs := field(r.body, "roles"); rs != "" {
		if list, ok := stringList(r.body, "roles"); ok {
			return list
		}
		return []string{rs}
	}
	role := field(r.body, "role")
	if role == "" {
		return nil
	}
	if role != "each.value" {
		return []string{role}
	}
	if values, ok := mapValues(field(r.body, "for_each")); ok {
		return values
	}
	if values, ok := mapValues(mapLiteral(r.body, "for_each")); ok {
		return values
	}
	return []string{role}
}

// mapLiteral is a map literal attribute written over several lines: the
// text from its opening brace to its closing one. The repository module's
// build grant takes two roles from one such block.
func mapLiteral(body, key string) string {
	re := regexp.MustCompile(`(?s)(?:^|\n)\s*` + key + `\s*=\s*(\{.*?\n\s*\})`)
	if m := re.FindStringSubmatch(stripComments(body)); m != nil {
		return m[1]
	}
	return ""
}

// mapValues is the values of a Terraform map literal, one per key: the
// for_each a grant takes its roles from. ok is false for anything else.
func mapValues(expr string) ([]string, bool) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "{") || !strings.HasSuffix(expr, "}") {
		return nil, false
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(expr, "{"), "}"), "\n") {
		if i := strings.Index(l, "#"); i >= 0 {
			l = l[:i]
		}
		if _, v, ok := strings.Cut(l, "="); ok {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out, len(out) > 0
}

// isPersonSet is whether a member is one of the operators' or launchers' own
// identities: each.value over a set of people, or a variable that is one.
func isPersonSet(member, forEach string, locals map[string]string) bool {
	if member == "each.value" {
		return peopleSet(forEach, locals)
	}
	return peopleVars[member]
}

// stringList is a Terraform list literal's string elements and whether the
// value was a list literal at all.
func stringList(body, key string) ([]string, bool) {
	re := regexp.MustCompile(`(?s)(?:^|\n)\s*` + key + `\s*=\s*\[(.*?)\]`)
	m := re.FindStringSubmatch(stripComments(body))
	if m == nil {
		return nil, false
	}
	var out []string
	for _, q := range quotedRE.FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out, true
}

var quotedRE = regexp.MustCompile(`"([^"]*)"`)

// stripComments drops # comments, to the end of their line: the modules'
// comments hold quotes and words like "member".
func stripComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if j := strings.Index(l, "#"); j >= 0 {
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, "\n")
}

// conditionExpr is a grant's condition expression, "" when it has none.
func conditionExpr(body string) string {
	m := condBlockRE.FindStringSubmatch(stripComments(body))
	if m == nil {
		return ""
	}
	if e := exprRE.FindStringSubmatch(m[1]); e != nil {
		return strings.TrimSpace(e[1])
	}
	return ""
}

var (
	// The condition block, which is indented two spaces; the literal
	// expression inside a lifecycle precondition is deeper and is not this.
	condBlockRE = regexp.MustCompile(`(?s)\n  condition \{\n(.*?)\n  \}`)
	exprRE      = regexp.MustCompile(`(?m)^\s*expression\s*=\s*(.+)$`)
)

// field is one attribute of a resource block, comments dropped.
func field(body, key string) string {
	re := regexp.MustCompile(`(?m)^\s*` + key + `\s*=\s*(.+)$`)
	for _, l := range strings.Split(stripComments(body), "\n") {
		if m := re.FindStringSubmatch(l); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// unquote drops the quotes of a Terraform string attribute.
func unquote(s string) string { return strings.Trim(s, `"`) }
