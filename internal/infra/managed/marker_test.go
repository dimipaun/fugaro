package managed

import (
	"context"
	"encoding/json"
	"errors"
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
// modules themselves, where every write grant to a service account must be
// a conditioned one. The live bindings are Check 68, owner-run, because
// Global Constraint 12 forbids a live IAM read from a test.
func TestRunAccountsHaveNoWritePathToTheMarker(t *testing.T) {
	// The objects at the bucket's root that a run must never write: the
	// creation fact this task writes, and the delete list Task 1.4 will.
	root := []string{MarkerObject, "fugaro/allow-delete.json"}
	for name, expr := range map[string]string{
		"a job account's grant":   gcp.BucketCondition(testBucket, gcp.JobBucketPrefixes, "web-a1b2c3"),
		"a build account's grant": gcp.BucketCondition(testBucket, gcp.BuildBucketPrefixes, "web"),
		"a launcher's grant":      gcp.LauncherBucketCondition(testBucket),
	} {
		prefixes := startsWithPrefixes(t, expr, testBucket)
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
	if got := startsWithPrefixes(t, gcp.LauncherBucketCondition(testBucket), testBucket); !slices.Equal(got, []string{"runs/"}) {
		t.Errorf("the launcher condition grants %v", got)
	}

	checkModuleGrants(t)
}

// startsWithPrefixes is the object prefixes an IAM condition expression
// grants on bucket.
func startsWithPrefixes(t *testing.T, expr, bucket string) []string {
	t.Helper()
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

// checkModuleGrants reads every google_storage_bucket_iam_member block in
// the Terraform modules and asserts the shape of the ones that touch the
// runs bucket: a write grant to a service account carries a condition, and
// no grant — to anyone — reaches the bucket's root.
func checkModuleGrants(t *testing.T) {
	t.Helper()
	// The expressions that are the runs bucket in the modules: the module's
	// own resource (installation), and the bucket the installation's outputs
	// hand the other modules.
	runs := []string{"google_storage_bucket.runs.name", "var.runs_bucket", "var.installation.runs_bucket"}
	// Read grants, which take nothing away from the mirror of the reads the
	// bucket policy already gives: objectViewer on the whole bucket.
	reads := map[string]bool{"roles/storage.objectViewer": true, "roles/storage.legacyBucketReader": true}
	writes := map[string]bool{
		"roles/storage.objectAdmin": true, "roles/storage.objectUser": true,
		"roles/storage.objectCreator": true, "roles/storage.admin": true,
	}
	seenWrites, seenSA := 0, 0
	root := filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range bucketGrantRE.FindAllStringSubmatch(string(b), -1) {
			name, body := m[1], m[2]
			bucket, role, member := field(t, path, name, body, "bucket"), unquote(field(t, path, name, body, "role")), field(t, path, name, body, "member")
			if !slices.Contains(runs, bucket) {
				continue // another bucket (the state bucket's own grants)
			}
			cond := conditionExpr(body)
			switch {
			case reads[role]:
			case writes[role]:
				seenWrites++
				if strings.Contains(member, "google_service_account.") {
					seenSA++
					if cond == "" {
						t.Errorf("%s: %s grants %s on the runs bucket to a service account with no condition: a run's own credential must have no write path to the bucket's root (%s)",
							path, name, role, MarkerObject)
					}
				} else if cond == "" && !strings.Contains(member, "var.operators") && !strings.Contains(member, "var.launchers") && !strings.Contains(member, "each.value") {
					t.Errorf("%s: %s grants %s on the runs bucket to %q with no condition: only the operators' own identities hold an unconditioned grant there (SECURITY.md:56)",
						path, name, role, member)
				}
			default:
				t.Errorf("%s: %s grants %q on the runs bucket: classify it as a read or a write grant in this test before it ships",
					path, name, role)
			}
			// A named object path in a condition on this bucket must not be
			// the bucket's root, whatever the role: this is the one place a
			// new condition's prefixes are read.
			for _, p := range startsWithPrefixes(t, cond, testBucket) {
				if strings.HasPrefix(MarkerObject, p) || strings.HasPrefix("fugaro/allow-delete.json", p) {
					t.Errorf("%s: %s conditions its grant to %q, which holds %s", path, name, p, MarkerObject)
				}
			}
			if cond != "" && len(startsWithPrefixes(t, cond, testBucket)) == 0 && strings.Contains(cond, "startsWith") {
				t.Errorf("%s: %s has a condition this test cannot read: %s", path, name, cond)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Non-vacuity: the parse found the grants this assertion is about. The
	// modules grant a job account, a build account and the launchers, and
	// the launchers' is the one write grant to identities that are not
	// service accounts.
	if seenSA < 2 {
		t.Errorf("found %d conditioned service-account write grants on the runs bucket, want the job account's and the build account's", seenSA)
	}
	if seenWrites < 3 {
		t.Errorf("found %d write grants on the runs bucket, want at least the job account's, the build account's and the launchers'", seenWrites)
	}
}

var (
	// A google_storage_bucket_iam_member block; the resource's own closing
	// brace is the first at column zero, which is also why the nested
	// lifecycle/precondition blocks stay inside the body.
	bucketGrantRE = regexp.MustCompile(`(?s)resource "google_storage_bucket_iam_member" "([a-z_]+)" \{\n(.*?)\n\}`)
	// The condition block, which is indented two spaces; the literal
	// expression inside a lifecycle precondition is deeper and is not this.
	condBlockRE = regexp.MustCompile(`(?s)\n  condition \{\n(.*?)\n  \}`)
	exprRE      = regexp.MustCompile(`(?m)^\s*expression\s*=\s*(.+)$`)
)

// conditionExpr is a grant's condition expression, "" when it has none. The
// modules pass the conditions in as variables (the Go side builds them, and
// the assertion above reads their prefixes there), so this is the variable
// reference, not the prefix itself; the one literal in the modules is the
// launcher grant's lifecycle precondition, which pins that variable to
// runs/.
func conditionExpr(body string) string {
	m := condBlockRE.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	if e := exprRE.FindStringSubmatch(m[1]); e != nil {
		return strings.TrimSpace(e[1])
	}
	return ""
}

// field is one attribute of a grant block, comments dropped: the modules'
// comments hold quotes and words like "member" (installation/iam.tf's
// operators' grant).
func field(t *testing.T, path, name, body, key string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + key + `\s*=\s*(.+)$`)
	for _, l := range strings.Split(body, "\n") {
		if i := strings.Index(l, "#"); i >= 0 {
			l = l[:i]
		}
		if m := re.FindStringSubmatch(l); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	t.Errorf("%s: %s has no %s", path, name, key)
	return ""
}

// unquote drops the quotes of a Terraform string attribute.
func unquote(s string) string { return strings.Trim(s, `"`) }
