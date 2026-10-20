package managed

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
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/tf"
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

// markerBudgetJSON is the version-2 object, byte for byte: the same fact,
// with the monthly amount and the currency a managed creation was required
// to choose (Task 4.2). Three fields, and the budget's own two inside it —
// still no token, no key, no email: the amount is a figure the owner chose,
// never a credential, and this is the constant anything added to it has to
// come through.
const markerBudgetJSON = `{"version":2,"gcp_project":"proj-1234","budget":{"amount":7,"currency":"USD"}}`

// The budget version's shape: the version follows the budget and nothing
// else does, so a marker whose version and budget disagree is refused rather
// than written. The two refusals are what make the version a fact the read
// can believe — the read takes version 2 to mean "this creation chose an
// amount", and a writer that set the version without one would record a
// managed project with no line to compare a spend against.
func TestMarkerBudgetVersionShape(t *testing.T) {
	m := Marker{Version: MarkerVersionBudget, GCPProject: testProject, Budget: &Budget{Amount: 7, Currency: "USD"}}
	b, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != markerBudgetJSON {
		t.Errorf("Bytes() = %q, want %q", b, markerBudgetJSON)
	}
	var back Marker
	if err := json.Unmarshal([]byte(markerBudgetJSON), &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, m) {
		t.Errorf("the fixture parses as %+v, want %+v", back, m)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(markerBudgetJSON), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Errorf("the object holds %d fields (%v): a fact and an amount, not a credential", len(fields), fields)
	}
	if budget, ok := fields["budget"].(map[string]any); !ok || len(budget) != 2 {
		t.Errorf("the budget is %v, want the amount and the currency and nothing else", fields["budget"])
	}

	for name, tc := range map[string]Marker{
		"version 1 with a budget":    {Version: MarkerVersion, GCPProject: testProject, Budget: &Budget{Amount: 7, Currency: "USD"}},
		"version 2 with no budget":   {Version: MarkerVersionBudget, GCPProject: testProject},
		"an amount of 0":             {Version: MarkerVersionBudget, GCPProject: testProject, Budget: &Budget{Currency: "USD"}},
		"a negative amount":          {Version: MarkerVersionBudget, GCPProject: testProject, Budget: &Budget{Amount: -1, Currency: "USD"}},
		"an amount with no currency": {Version: MarkerVersionBudget, GCPProject: testProject, Budget: &Budget{Amount: 7}},
		"a lower-case currency":      {Version: MarkerVersionBudget, GCPProject: testProject, Budget: &Budget{Amount: 7, Currency: "usd"}},
		"a currency with a newline":  {Version: MarkerVersionBudget, GCPProject: testProject, Budget: &Budget{Amount: 7, Currency: "XXXXXXXX\n"}},
		"an amount above the range":  {Version: MarkerVersionBudget, GCPProject: testProject, Budget: &Budget{Amount: MaxBudgetAmount + 1, Currency: "USD"}},
		"the largest int64 amount":   {Version: MarkerVersionBudget, GCPProject: testProject, Budget: &Budget{Amount: 9223372036854775807, Currency: "USD"}},
		"a version this build does not know": {
			Version: MarkerVersionBudget + 1, GCPProject: testProject,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if b, err := tc.Bytes(); err == nil {
				t.Fatalf("Bytes() = %q, want a refusal", b)
			}
		})
	}
}

// The write, read back through the API a person and the read of this
// package both use; and a second call, which is an error rather than an
// update: the fact is written once, from the one place that knows it.
func TestWriteMarkerRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	if err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber, nil); err != nil {
		t.Fatal(err)
	}
	b, ok := readMarker(t, s, testBucket)
	if !ok || string(b) != markerJSON {
		t.Fatalf("gs://%s/%s = %q (present %v), want %q", testBucket, MarkerObject, b, ok, markerJSON)
	}
	err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber, nil)
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second write: %v, want a refusal naming the object as already there", err)
	}
	if b, _ := readMarker(t, s, testBucket); string(b) != markerJSON {
		t.Errorf("the second call changed the object to %q", b)
	}
}

// The same write for a managed creation: WriteMarker with a budget writes
// the version that carries it, and a call with a budget that is not an
// amount — no currency, an amount of 0 — is refused with nothing written,
// which is the fail-closed direction of a fact the read believes.
func TestWriteMarkerRecordsTheBudgetItWasGiven(t *testing.T) {
	ctx := context.Background()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	budget := &Budget{Amount: 7, Currency: "USD"}
	if err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber, budget); err != nil {
		t.Fatal(err)
	}
	b, ok := readMarker(t, s, testBucket)
	if !ok || string(b) != markerBudgetJSON {
		t.Fatalf("gs://%s/%s = %q (present %v), want %q", testBucket, MarkerObject, b, ok, markerBudgetJSON)
	}
	// Once, like version 1: the amount is what the creation was required to
	// choose, and a later run's budget flags change the Terraform budget
	// rather than rewriting the record of what was chosen.
	err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber, budget)
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second write: %v, want a refusal naming the object as already there", err)
	}

	// What must not happen is a version-2 object with no amount in it, or
	// with an amount that is not one. (nil is not this table: it writes
	// version 1, the object TestWriteMarkerRoundTrip asserts.)
	for name, bad := range map[string]*Budget{
		"no amount":     {Currency: "USD"},
		"no currency":   {Amount: 7},
		"a zero amount": {Currency: "USD"},
	} {
		t.Run("a write with "+name+" is refused", func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			if err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber, bad); err == nil {
				t.Fatal("want a refusal: the amount and the currency are a pair, above 0")
			}
			if names := objects(t, s, testBucket); len(names) != 0 {
				t.Errorf("a refused call wrote %v", names)
			}
		})
	}
}

// A run that created no project records nothing, whatever else it did, and
// touches nothing at all: created is "" in every row here, the call returns
// before it reads the bucket, and the object, the bucket's labels and its
// number are never looked at. This is the property the task exists for: the
// marker is what turns the confirmations off, so a write that does not read
// the fact would mark every project init touches, belong and the owner's own
// included.
//
// The rows are therefore the arguments that would otherwise decide, each one
// wrong a different way, with the first row the call that would succeed if
// the fact were set: the project's own runs bucket, labelled and numbered as
// WriteMarker demands, the project and the created project the same value.
// That none of them writes anything is the point — with the fact set, the
// same arguments on the same bucket are the two tests below, where a bucket
// or a number that is not the project's own is a refusal.
func TestWriteMarkerWritesNothingWithoutTheFact(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		bucket, project string
		number          uint64
	}{
		"everything the write would need, but the fact": {testBucket, testProject, testProjectNumber},
		"a bucket that is not there":                    {"fugaro-runs-elsewhere", testProject, testProjectNumber},
		"the project's own bucket, numbered wrong":      {testBucket, testProject, 999},
		"another project named":                         {testBucket, "belong", testProjectNumber},
		"no bucket named and no number":                 {"", testProject, 0},
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			// created is "" here: the project this run installs into.
			if err := WriteMarker(ctx, s, tc.bucket, "", tc.project, tc.number, nil); err != nil {
				t.Fatalf("err = %v, want a silent no-op", err)
			}
			// Not one request, before the reads below make their own: a run
			// that created nothing does not read the bucket to find out
			// whether it may write to it.
			if reqs := f.Requests(); len(reqs) != 0 {
				t.Errorf("the call touched the API: %v", reqs)
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
			err := WriteMarker(ctx, s, testBucket, tc.created, tc.project, testProjectNumber, nil)
			if err == nil {
				t.Fatal("want a refusal")
			}
			// Both names, quoted, in every row including the one that names
			// nothing: the refusal is about the fact this run holds, so it
			// says what that was — and what it was asked to write instead.
			for _, want := range []string{tc.created, tc.project} {
				if !strings.Contains(err.Error(), fmt.Sprintf("%q", want)) {
					t.Errorf("err = %v, want it to name %q", err, want)
				}
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
			err := WriteMarker(ctx, s, tc.bucket, testProject, testProject, tc.number, nil)
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
	if err := WriteMarker(ctx, s, testBucket, testProject, testProject, testProjectNumber, nil); err != nil {
		t.Fatalf("the project's own runs bucket was refused: %v", err)
	}
	// And the client the rows above all have: a call with none is a refusal
	// rather than a panic, the way WriteDeleteList's own nil-client branch is
	// (deletelist_test.go, the same assertion for the other object).
	if err := WriteMarker(ctx, nil, testBucket, testProject, testProject, testProjectNumber, nil); err == nil {
		t.Error("the write was made with no storage client")
	}
}

// WriteManagedLabel is the other half of the same fact (Task 3.0), on the
// project itself rather than in its bucket: the gate is WriteMarker's, the
// write is idempotent in the one direction that is not dangerous (the label
// already says what this package writes), and a label someone else put there
// is refused rather than overwritten — `fugaro_managed=false` is a statement,
// and this package does not get to replace it. A Resource Manager v1 update
// replaces the whole label map, so the other labels the project carries are
// the third thing the rows below pin: a writer that sent only its own key
// would drop them.
func TestWriteManagedLabel(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		created, project string
		labels           map[string]string // the project's labels as the fake has them
		missing          bool              // the project is not in the fake at all
		refuse           int               // a status for every Resource Manager call
		noUpdate         bool              // the read is readable and the update is refused
		want             string            // a substring the refusal must carry, "" for no refusal
		wantLabels       map[string]string // what the project carries afterwards
		wantCalls        int               // Resource Manager calls the write may make
	}{
		"the project this run created": {
			created: testProject, project: testProject,
			labels:     map[string]string{"environment": "prod"},
			wantLabels: map[string]string{"environment": "prod", ProjectLabelKey: ProjectLabelValue},
			wantCalls:  2, // the read and the update
		},
		"a project with no labels at all": {
			created: testProject, project: testProject,
			wantLabels: map[string]string{ProjectLabelKey: ProjectLabelValue},
			wantCalls:  2,
		},
		"a project that already carries the label": {
			created: testProject, project: testProject,
			labels:     map[string]string{ProjectLabelKey: ProjectLabelValue},
			wantLabels: map[string]string{ProjectLabelKey: ProjectLabelValue},
			wantCalls:  1, // the read, and no update: the mark is written once
		},
		"a project carrying the key with another value": {
			created: testProject, project: testProject,
			labels:     map[string]string{ProjectLabelKey: "false"},
			want:       "already carries",
			wantLabels: map[string]string{ProjectLabelKey: "false"},
			wantCalls:  1,
		},
		"an empty value is not the label": {
			created: testProject, project: testProject,
			labels:     map[string]string{ProjectLabelKey: ""},
			wantLabels: map[string]string{ProjectLabelKey: ProjectLabelValue},
			wantCalls:  2,
		},
		"another project": {
			created: testProject, project: "belong",
			want:      "did not create",
			wantCalls: 0,
		},
		"nothing to mark": {
			created: testProject, project: "",
			want:      "did not create",
			wantCalls: 0,
		},
		"the project read is refused": {
			created: testProject, project: testProject, refuse: http.StatusForbidden,
			want:      "reading project",
			wantCalls: 1,
		},
		"the project is not there": {
			created: testProject, project: testProject, missing: true,
			want:      "reading project",
			wantCalls: 1,
		},
		// The shape every credential a run holds is in: the project can be
		// read and its label cannot be written (no
		// resourcemanager.projects.update). The object may already be in the
		// bucket by then — this is
		// the half-mark of Task 3.0's point 4, and the read answers "not
		// managed" for it.
		"the update is refused": {
			created: testProject, project: testProject,
			labels:     map[string]string{"environment": "prod"},
			noUpdate:   true,
			want:       "labelling project",
			wantLabels: map[string]string{"environment": "prod"},
			wantCalls:  2,
		},
		"the read fails": {
			created: testProject, project: testProject, refuse: http.StatusInternalServerError,
			want:      "reading project",
			wantCalls: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, cf := newCRM(t)
			if !tc.missing {
				cf.AddProject(testProject, testProjectNumber)
				if tc.labels != nil {
					cf.SetLabels(testProject, tc.labels)
				}
			}
			if tc.refuse != 0 {
				cf.Refuse(tc.refuse, "PERMISSION_DENIED", "", "refused")
			}
			if tc.noUpdate {
				cf.DenyUpdate(testProject)
			}
			err := WriteManagedLabel(ctx, c, tc.created, tc.project)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want the label written", err)
			case tc.want != "" && err == nil:
				t.Fatalf("want a refusal carrying %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("err = %v, want it to carry %q", err, tc.want)
			}
			if n := len(cf.Requests()); n != tc.wantCalls {
				t.Errorf("the write made %d Resource Manager calls, want %d: %v", n, tc.wantCalls, cf.Requests())
			}
			// Every call is a read or the one update, and the update is a
			// projects update: a writer reaching for anything else (a policy,
			// a create) is not this function.
			for _, r := range cf.Requests() {
				if r.Method != http.MethodGet && r.Method != http.MethodPut {
					t.Errorf("the write made a %s %s", r.Method, r.Path)
				}
			}
			if tc.wantLabels != nil {
				if got := cf.Labels(testProject); !maps.Equal(got, tc.wantLabels) {
					t.Errorf("the project carries %v, want %v", got, tc.wantLabels)
				}
			}
		})
	}

	// A run that created no project writes nothing at all — not a read, not a
	// label — exactly as WriteMarker does not: adding Firebase to an existing
	// project (D73) and every ordinary run of an existing installation come
	// through here with created == "", and the owner's own projects, belong
	// among them, must come out of it untouched.
	c, cf := newCRM(t)
	cf.AddProject(testProject, testProjectNumber)
	if err := WriteManagedLabel(ctx, c, "", testProject); err != nil {
		t.Fatalf("err = %v, want a silent no-op", err)
	}
	if reqs := cf.Requests(); len(reqs) != 0 {
		t.Errorf("the call touched the API: %v", reqs)
	}
	if got := cf.Labels(testProject); len(got) != 0 {
		t.Errorf("the project carries %v, want nothing", got)
	}

	// And the client the rows above all have: a call with none is a refusal
	// rather than a panic (WriteMarker's own nil-client branch, and
	// WriteDeleteList's).
	if err := WriteManagedLabel(ctx, nil, testProject, testProject); err == nil {
		t.Error("the write was made with no Resource Manager client")
	}
}

// TestWriteManagedLabelForFlag is the other gate on the same label: the one
// `fugaro managed flag` holds (D58's owner, their own terminal, the GCP
// project ID typed), for a project no run created. What it must do that
// WriteManagedLabel must not is exactly that — label a project this run did
// not create — and everything else is the same write: the same value, the
// same refusals, the same labels kept.
//
// The gate itself is not in this function and cannot be: it is the command's
// (an agent's session and a run's process are refused before it reads
// anything, and the typed ID is the confirmation), which is why the test that
// pins it is managed_flag_test.go's. What is pinned here is that this entry
// point asks for none of WriteManagedLabel's creation evidence.
func TestWriteManagedLabelForFlag(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		project    string
		labels     map[string]string // the project's labels as the fake has them
		missing    bool              // the project is not in the fake at all
		refuse     int               // a status for every Resource Manager call
		noUpdate   bool              // the read is readable and the update is refused
		want       string            // a substring the refusal must carry, "" for no refusal
		wantLabels map[string]string // what the project carries afterwards
		wantCalls  int               // Resource Manager calls the write may make
	}{
		// The row this entry point exists for: no run created this project —
		// it is the owner's, flagged by hand — and the label lands on it
		// anyway, with the label it already carried kept.
		"a project no run created": {
			project:    testProject,
			labels:     map[string]string{"environment": "prod"},
			wantLabels: map[string]string{"environment": "prod", ProjectLabelKey: ProjectLabelValue},
			wantCalls:  2, // the read and the update
		},
		"a project with no labels at all": {
			project:    testProject,
			wantLabels: map[string]string{ProjectLabelKey: ProjectLabelValue},
			wantCalls:  2,
		},
		"a project that already carries the label": {
			project:    testProject,
			labels:     map[string]string{ProjectLabelKey: ProjectLabelValue},
			wantLabels: map[string]string{ProjectLabelKey: ProjectLabelValue},
			wantCalls:  1, // the read, and no update: the mark is written once
		},
		// Refused, and the refusal says which of the two writes this label
		// is, because the flag's message for it is the operator's: the label
		// is not this command's to replace, whatever put it there.
		"a project carrying the key with another value": {
			project:    testProject,
			labels:     map[string]string{ProjectLabelKey: "false"},
			want:       "already carries",
			wantLabels: map[string]string{ProjectLabelKey: "false"},
			wantCalls:  1,
		},
		"an empty value is not the label": {
			project:    testProject,
			labels:     map[string]string{ProjectLabelKey: ""},
			wantLabels: map[string]string{ProjectLabelKey: ProjectLabelValue},
			wantCalls:  2,
		},
		"no project to label": {
			project:   "",
			want:      "no project name",
			wantCalls: 0,
		},
		"the project read is refused": {
			project: testProject, refuse: http.StatusForbidden,
			want:      "reading project",
			wantCalls: 1,
		},
		"the project is not there": {
			project: testProject, missing: true,
			want:      "reading project",
			wantCalls: 1,
		},
		"the update is refused": {
			project:    testProject,
			labels:     map[string]string{"environment": "prod"},
			noUpdate:   true,
			want:       "labelling project",
			wantLabels: map[string]string{"environment": "prod"},
			wantCalls:  2,
		},
		"the read fails": {
			project: testProject, refuse: http.StatusInternalServerError,
			want:      "reading project",
			wantCalls: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, cf := newCRM(t)
			if !tc.missing {
				cf.AddProject(testProject, testProjectNumber)
				if tc.labels != nil {
					cf.SetLabels(testProject, tc.labels)
				}
			}
			if tc.refuse != 0 {
				cf.Refuse(tc.refuse, "PERMISSION_DENIED", "", "refused")
			}
			if tc.noUpdate {
				cf.DenyUpdate(testProject)
			}
			err := WriteManagedLabelForFlag(ctx, c, tc.project)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want the label written", err)
			case tc.want != "" && err == nil:
				t.Fatalf("want a refusal carrying %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("err = %v, want it to carry %q", err, tc.want)
			}
			if n := len(cf.Requests()); n != tc.wantCalls {
				t.Errorf("the write made %d Resource Manager calls, want %d: %v", n, tc.wantCalls, cf.Requests())
			}
			// One read and one update, and the update is a projects update:
			// the same shape WriteManagedLabel's own test pins.
			for _, r := range cf.Requests() {
				if r.Method != http.MethodGet && r.Method != http.MethodPut {
					t.Errorf("the write made a %s %s", r.Method, r.Path)
				}
			}
			if tc.wantLabels != nil {
				if got := cf.Labels(testProject); !maps.Equal(got, tc.wantLabels) {
					t.Errorf("the project carries %v, want %v", got, tc.wantLabels)
				}
			}
		})
	}

	// The refusal that names who writes the label is this entry point's own
	// text: the creation path's would tell a flagged project's owner about a
	// run that created it, which is not what happened.
	c, cf := newCRM(t)
	cf.AddProject(testProject, testProjectNumber)
	cf.SetLabels(testProject, map[string]string{ProjectLabelKey: "false"})
	err := WriteManagedLabelForFlag(ctx, c, testProject)
	if err == nil || !strings.Contains(err.Error(), "the owner flagging the project") {
		t.Errorf("err = %v, want it to say who writes the label", err)
	}
	if err := WriteManagedLabel(ctx, c, testProject, testProject); err == nil || !strings.Contains(err.Error(), "the run that created the project") {
		t.Errorf("err = %v, want the creation path's own text", err)
	}

	// And the client the rows above all have: a call with none is a refusal
	// rather than a panic.
	if err := WriteManagedLabelForFlag(ctx, nil, testProject); err == nil {
		t.Error("the write was made with no Resource Manager client")
	}
}

// TestWriteManagedLabelKeepsTheProjectsDisplayName is the round-2 review's
// should-fix, and it is about the request and not about the label: Resource
// Manager v1 projects.update replaces the project's editable state with what
// the request carries — the display name and the whole label map — so a writer
// that sends a Project holding only the labels it computed takes the owner's
// display name off the project, silently, on every project it labels. gcloud
// and Terraform both read the project, change the field they mean to change
// and send the whole object back for exactly this reason.
//
// The fake models that replacement (gcpfake.CRM, and its own test pins it), so
// the assertion here is the API's: after each writer has run, the project
// still has the display name it had. It covers both writers, because both end
// in the same update and a fix in one would leave the other clearing names.
func TestWriteManagedLabelKeepsTheProjectsDisplayName(t *testing.T) {
	ctx := context.Background()
	const name = "Belong production"
	for what, write := range map[string]func(context.Context, *crm.Service) error{
		"the creating run's write": func(ctx context.Context, c *crm.Service) error {
			return WriteManagedLabel(ctx, c, testProject, testProject)
		},
		"the owner's flag": func(ctx context.Context, c *crm.Service) error {
			return WriteManagedLabelForFlag(ctx, c, testProject)
		},
	} {
		t.Run(what, func(t *testing.T) {
			c, cf := newCRM(t)
			cf.AddProject(testProject, testProjectNumber)
			cf.SetDisplayName(testProject, name)
			cf.SetLabels(testProject, map[string]string{"environment": "prod"})
			if err := write(ctx, c); err != nil {
				t.Fatalf("the write = %v, want the label written", err)
			}
			if got := cf.DisplayName(testProject); got != name {
				t.Errorf("the project's display name is %q after the label write, want %q: an update replaces the project's editable fields, so the writer has to send the project it read", got, name)
			}
			if got, want := cf.Labels(testProject), map[string]string{"environment": "prod", ProjectLabelKey: ProjectLabelValue}; !maps.Equal(got, want) {
				t.Errorf("the project carries %v, want %v", got, want)
			}
		})
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
// Terraform under deploy/terraform — the modules and the roots — where every
// role granted to anyone who is not a set of people must be on an explicit
// allowlist (runAccountGrants, the grants made today, pinned whole), every
// grant of a write role on the runs bucket to a run's own credential must be
// a conditioned one that this test can read, no member may hold a storage
// write role on the project, which the bucket's policy does not bound
// (SECURITY.md:56) — with one exception, read rather than assumed: Task 1.5's
// binding (2), the project-level grant of the scoped deployer role, whose
// condition is the managed prefix and is matched here clause for clause
// (managedPrefixFence, deployerConditionClauses) — and a block the scan
// cannot read is a finding. The live
// bindings are Check 68, owner-run, because Global Constraint 12 forbids a
// live IAM read from a test. A known residual Check 68 must look at: the
// build account may submit builds across the project, and a build that
// names no service account may run as the default build or compute
// account, which this scan cannot see.
func TestRunAccountsHaveNoWritePathToTheMarker(t *testing.T) {
	// The objects at the bucket's root that a run must never write: the
	// creation fact this task writes, and the delete list Task 1.4 will.
	root := []string{MarkerObject, "fugaro/allow-delete.json"}
	for name, expr := range map[string]string{
		"a job account's grant": gcp.BucketCondition(testBucket, gcp.JobBucketPrefixes, "web-a1b2c3"),
		// A project job's widened grant (phase 3, Task 3.2): no slug, but
		// still the three prefixes, so never the bucket's root.
		"a project job account's grant": gcp.ProjectBucketCondition(testBucket, gcp.JobBucketPrefixes),
		// A project job's read-only tokens/ grant (phase 3, Task 3.2): the
		// whole prefix, and still not the bucket's root.
		"a project job account's tokens grant": gcp.ProjectTokenBucketCondition(testBucket),
		"a job account's tokens grant":         gcp.TokenBucketCondition(testBucket, "web-a1b2c3"),
		"a build account's grant":              gcp.BucketCondition(testBucket, gcp.BuildBucketPrefixes, "web"),
		"a launcher's grant":                   gcp.LauncherBucketCondition(testBucket),
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
	// locks/, a build writes builds/, a launcher writes runs/ and, since the
	// sealed budget token moved there, tokens/ (budget-token-channel.md §7.2,
	// D16: the deliberate widening). tokens/ is not a run account's write
	// prefix — the job's grant there is objectViewer, checked by
	// runAccountGrants and by the scan's write-role rules.
	if !slices.Equal(gcp.JobBucketPrefixes, []string{"runs", "cache", "locks"}) {
		t.Errorf("gcp.JobBucketPrefixes = %v", gcp.JobBucketPrefixes)
	}
	if !slices.Equal(gcp.BuildBucketPrefixes, []string{"builds"}) {
		t.Errorf("gcp.BuildBucketPrefixes = %v", gcp.BuildBucketPrefixes)
	}
	if got := startsWithPrefixes(gcp.LauncherBucketCondition(testBucket), testBucket); !slices.Equal(got, []string{"runs/", "tokens/"}) {
		t.Errorf("the launcher condition grants %v", got)
	}

	scan := scanModuleIAM(moduleFiles(t))
	// The compute module's grants are refused, and that refusal is expected
	// for 0.8.5 (knownComputeRefusals): every other finding is a failure.
	for _, f := range otherThanComputeRefusals(t, scan.findings) {
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
	if scan.runAccountGrants != len(runAccountGrants) || scan.runAccountGrants < 20 {
		t.Errorf("the scan matched %d of the allowlist's %d grants, want every one (the modules' grants to run, history and scheduler accounts)", scan.runAccountGrants, len(runAccountGrants))
	}
	if scan.customRoles < 5 {
		t.Errorf("the scan read %d custom roles, want the modules'", scan.customRoles)
	}
	// Task 3.0's rule, and the count that keeps it from passing vacuously:
	// every grant to a member that is not a set of people, whose role this
	// scan could resolve, was read for the two permissions that set or remove
	// the project's own label — and none of them holds one. Without the
	// first count the second is an assertion about nothing, which is exactly
	// what a scan of the bucket's grants alone would give.
	if scan.labelChecked < 20 {
		t.Errorf("the scan read %d grants for a path to the %s label, want every grant the modules make to a member that is not a set of people (the run, history and scheduler accounts)", scan.labelChecked, ProjectLabelKey)
	}
	if scan.labelGrants != 0 {
		t.Errorf("the scan found %d of those grants reaching the %s label, want none: no run account may set or remove it (Task 3.0)", scan.labelGrants, ProjectLabelKey)
	}
	// The one project-level storage write this scan admits, and the reason it
	// admits it: binding (2)'s fence, which the scan read out of the module
	// and matched clause for clause. The runs bucket's own name is outside it
	// — that is what the fence is for — and the fence names the reserved
	// prefix in its storage clauses, so the admission is not vacuous.
	if scan.fencedWrites < 1 {
		t.Errorf("the scan admitted %d project-level write grants under the deployer fence, want the scoped deployer role's binding (2)", scan.fencedWrites)
	}
	if strings.HasPrefix(testBucket, tf.ManagedPrefix) {
		t.Errorf("the runs bucket %q carries the reserved prefix %q, so binding (2) reaches it", testBucket, tf.ManagedPrefix)
	}
	for _, c := range deployerConditionClauses {
		if strings.Contains(c, "storage.googleapis.com/") && !strings.Contains(c, "projects/_/buckets/"+tf.ManagedPrefix) {
			t.Errorf("storage clause %q does not name the reserved prefix %q", c, tf.ManagedPrefix)
		}
	}
	// Each of the three condition variables is the one the Go side builds
	// (internal/infra/spec.go:652 and :809 set the first two from
	// gcp.BucketCondition, installation/iam.tf's precondition pins the
	// launcher's): one that stops being used is a diff here, not a silent
	// narrowing of this assertion.
	for _, v := range []string{"var.workflow.bucket_condition.expression", "var.repo.build_bucket_condition.expression", "var.launcher_bucket_condition.expression", "var.job.bucket_condition.expression"} {
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
			f["modules/workflow/sa.tf"] += `
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
			f["modules/workflow/sa.tf"] += `
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
			f["modules/workflow/sa.tf"] += `
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
			f["modules/workflow/sa.tf"] += `
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
			f["modules/workflow/sa.tf"] += `
resource "google_storage_bucket_iam_policy" "job" {
  bucket      = var.runs_bucket
  policy_data = data.google_iam_policy.job.policy_data
}
`
		},
		// A bucket this test cannot identify used to be skipped instead of
		// reported: a literal name that is the runs bucket — or any name the
		// scan does not know — must be a finding, or the one shape it cannot
		// read is the one shape that passes.
		"a bucket named by a literal": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_storage_bucket_iam_member" "job_literal" {
  bucket = "fugaro-runs-proj-1234"
  role   = "roles/storage.objectAdmin"
  member = google_service_account.job.member
}
`
		},
		// A for_each that is a set of people and a literal service account:
		// each.value is whichever member the grant is for, and the literals
		// in the expression used to be ignored.
		"a for_each mixing people with a literal member": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_storage_bucket_iam_member" "job_mixed" {
  for_each = setunion(toset(var.operators), toset(["serviceAccount:job@example.iam.gserviceaccount.com"]))

  bucket = var.runs_bucket
  role   = "roles/storage.objectAdmin"
  member = each.value
}
`
		},
		// A condition that is not narrowing: "true" is a condition and
		// narrows nothing, and a variable this test does not recognise is no
		// better — the job grant's own two are the ones the Go side builds.
		"a condition that is true": func(f map[string]string) {
			f["modules/workflow/sa.tf"] = strings.Replace(f["modules/workflow/sa.tf"],
				"expression = var.workflow.bucket_condition.expression", `expression = "true"`, 1)
		},
		"a condition this test does not recognise": func(f map[string]string) {
			f["modules/workflow/sa.tf"] = strings.Replace(f["modules/workflow/sa.tf"],
				"expression = var.workflow.bucket_condition.expression", "expression = var.wider_bucket_condition.expression", 1)
		},

		// The second review's mutations: roles that are not storage roles
		// but give the job account a way to one. The scan used to read any
		// role it did not recognise as harmless; a run account's roles are
		// now an allowlist, and each of these is off it.
		"projectIamAdmin to the job account (grant itself storage.admin)": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += projectGrantToJob("job_iam_admin", `"roles/resourcemanager.projectIamAdmin"`)
		},
		"a custom role with setIamPolicy to the job account": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_project_iam_custom_role" "job_policy" {
  project     = var.project
  role_id     = "jobPolicy"
  title       = "job policy"
  permissions = ["resourcemanager.projects.setIamPolicy"]
}
` + projectGrantToJob("job_policy", "google_project_iam_custom_role.job_policy.name")
		},
		"serviceAccountTokenCreator at the project to the job account (impersonate the editor compute account)": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += projectGrantToJob("job_token_creator", `"roles/iam.serviceAccountTokenCreator"`)
		},
		"cloudbuild.builds.builder to the job account (holds storage.objects.create)": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += projectGrantToJob("job_builder", `"roles/cloudbuild.builds.builder"`)
		},
		"firebase.admin to the job account": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += projectGrantToJob("job_firebase", `"roles/firebase.admin"`)
		},
		"tokenCreator on the default compute account to the job account": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_service_account_iam_member" "job_compute" {
  service_account_id = "projects/${var.project}/serviceAccounts/123456789012-compute@developer.gserviceaccount.com"
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = google_service_account.job.member
}
`
		},
		// The build account already holds serviceAccountUser on itself:
		// the same role on the default compute account would let it submit
		// a build that runs as that account (roles/editor).
		"actAs on the default compute account to the build account": func(f map[string]string) {
			f["modules/repo/build.tf"] = strings.Replace(f["modules/repo/build.tf"],
				"  service_account_id = google_service_account.build.name\n  role               = \"roles/iam.serviceAccountUser\"\n  member             = google_service_account.build.member",
				"  service_account_id = \"projects/${var.project}/serviceAccounts/123456789012-compute@developer.gserviceaccount.com\"\n  role               = \"roles/iam.serviceAccountUser\"\n  member             = google_service_account.build.member", 1)
		},
		"a folder-level grant to the job account": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_folder_iam_member" "job" {
  folder = "folders/123"
  role   = "roles/storage.admin"
  member = google_service_account.job.member
}
`
		},
		"an organization-level grant to the job account": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_organization_iam_member" "job" {
  org_id = "123"
  role   = "roles/viewer"
  member = google_service_account.job.member
}
`
		},
		"a project binding whose members are references": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_project_iam_binding" "job" {
  project = var.project
  role    = "roles/iam.serviceAccountTokenCreator"
  members = [google_service_account.job.member]
}
`
		},
		"a service account policy": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_service_account_iam_policy" "job" {
  service_account_id = google_service_account.job.name
  policy_data        = data.google_iam_policy.job.policy_data
}
`
		},
		"an allowlisted custom role widened with a storage write": func(f map[string]string) {
			f["modules/installation/iam.tf"] = strings.Replace(f["modules/installation/iam.tf"],
				`permissions = ["cloudbuild.builds.create", "cloudbuild.builds.get"]`,
				`permissions = ["cloudbuild.builds.create", "cloudbuild.builds.get", "storage.objects.create"]`, 1)
		},
		"an allowlisted role to another member": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_project_iam_member" "vertex_build" {
  project = var.project
  role    = "roles/aiplatform.user"
  member  = "serviceAccount:123456789012-compute@developer.gserviceaccount.com"
}
`
		},
		"an ACL on the runs bucket": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
resource "google_storage_bucket_access_control" "job" {
  bucket = var.runs_bucket
  role   = "WRITER"
  entity = "user-job@example.iam.gserviceaccount.com"
}
`
		},

		// Resources the parser could not read used to be skipped: each of
		// these was a storage write on the runs bucket that passed.
		"a hyphenated resource name": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += projectGrantToJob("job-storage", `"roles/storage.admin"`)
		},
		"two spaces after resource": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += strings.Replace(projectGrantToJob("job_storage", `"roles/storage.admin"`), `resource "`, `resource  "`, 1)
		},
		"unquoted labels": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += strings.Replace(projectGrantToJob("job_storage", `"roles/storage.admin"`),
				`resource "google_project_iam_member" "job_storage"`, `resource google_project_iam_member job_storage`, 1)
		},
		"an indented resource": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += strings.ReplaceAll(projectGrantToJob("job_storage", `"roles/storage.admin"`), "\n", "\n  ")
		},
		"a new file whose first line is the grant": func(f map[string]string) {
			f["modules/workflow/zz.tf"] = `resource "google_storage_bucket_iam_member" "job_root" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectAdmin"
  member = google_service_account.job.member
}
`
		},
		"a grant in a root": func(f map[string]string) {
			f["roots/repo/main.tf"] += `
resource "google_storage_bucket_iam_member" "job_root" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:job@example.iam.gserviceaccount.com"
}
`
		},
		"a module from outside the modules": func(f map[string]string) {
			f["roots/repo/main.tf"] += `
module "grants" {
  source = "github.com/example/grants"
}
`
		},
		"a JSON-syntax Terraform file": func(f map[string]string) {
			f["modules/workflow/zz.tf.json"] = `{"resource": {"google_project_iam_member": {"job": {"role": "roles/storage.admin"}}}}`
		},
		// The parser's own cross-check: a resource keyword it did not read
		// as a top-level resource block (here, inside another block), and a
		// file it cannot lex at all.
		"a resource keyword the parser does not read as a block": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
terraform {
resource "google_project_iam_member" "job_nested" {
  role   = "roles/storage.admin"
  member = google_service_account.job.member
}
}
`
		},
		"a file the lexer cannot read": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += `
locals {
  open = "a string that does not end
}
` + projectGrantToJob("job_storage", `"roles/storage.admin"`)
		},
		// Task 1.5's fence is the only reason a storage write at the project
		// level is admitted: the three mutations below are what that
		// admission must not survive.
		"the scoped deployer grant without its condition": func(f map[string]string) {
			f["modules/installation/deployer.tf"] = strings.Replace(f["modules/installation/deployer.tf"], `  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_expression
  }

`, "", 1)
		},
		"the scoped deployer grant under a fence widened to every bucket": func(f map[string]string) {
			f["modules/installation/deployer.tf"] = strings.ReplaceAll(f["modules/installation/deployer.tf"], "projects/_/buckets/fmgd-", "projects/_/buckets/")
		},
		"a fence clause that names no resource type": func(f map[string]string) {
			f["modules/installation/deployer.tf"] = strings.ReplaceAll(f["modules/installation/deployer.tf"],
				`(resource.type == \"run.googleapis.com/Job\" && resource.name.contains(\"/jobs/fmgd-\"))`,
				`(resource.name.contains(\"/jobs/fmgd-\"))`)
		},
		"a storage write added to the unconditioned deployer role": func(f map[string]string) {
			f["modules/installation/deployer.tf"] = strings.Replace(f["modules/installation/deployer.tf"],
				`    "storage.buckets.create",`, `    "storage.buckets.create",
    "storage.objects.create",`, 1)
		},
		// Task 3.0's rule, in the three spellings a grant of the label
		// permission could come in: a predefined role that carries it, and a
		// custom role holding each of the two permissions (setIamPolicy
		// matters on its own — it is how a holder grants itself the update).
		// Each is a project-level grant to the job account, the credential a
		// run's agent holds: the one that must never be able to put a project
		// into managed mode, or take it out. None of them touches the runs
		// bucket, which is the point — the label is not in the bucket, so
		// neither the bucket's conditions nor the deployer's prefix fence
		// bounds this permission.
		"roles/editor to the job account": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += projectGrantToJob("job_editor", `"roles/editor"`)
		},
		"a custom role holding resourcemanager.projects.update, to the job account": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += customRole("job_labeller", "jobLabeller", `"resourcemanager.projects.update"`) +
				projectGrantToJob("job_label", `google_project_iam_custom_role.job_labeller.name`)
		},
		"a custom role holding resourcemanager.projects.setIamPolicy, to the job account": func(f map[string]string) {
			f["modules/workflow/sa.tf"] += customRole("job_policy_labeller", "jobPolicyLabeller", `"resourcemanager.projects.setIamPolicy"`) +
				projectGrantToJob("job_policy_label", `google_project_iam_custom_role.job_policy_labeller.name`)
		},
		"a grant inside a comment-looking string": func(f map[string]string) {
			// A block comment opened inside a string does not hide what
			// follows the string.
			f["modules/workflow/sa.tf"] += `
locals {
  note = "/*"
}
` + projectGrantToJob("job_storage", `"roles/storage.admin"`) + `
locals {
  note2 = "*/"
}
`
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
	// Task 3.0's rule fires by itself, and not only beside the allowlist.
	// Every mutation above is also a new grant to a non-people member, which
	// runAccountGrants refuses whatever it grants — so a mutation being
	// caught says nothing yet about the label rule. What says something is
	// the finding itself: each of these names the project's label, so the
	// scan refused it for the permission it carries and not only for being
	// new. That is the rule a future task needs when it adds an entry to the
	// allowlist without reading it.
	for name, role := range map[string]string{
		"roles/editor":                          `"roles/editor"`,
		"roles/owner":                           `"roles/owner"`,
		"roles/resourcemanager.projectIamAdmin": `"roles/resourcemanager.projectIamAdmin"`,
		"roles/iam.securityAdmin":               `"roles/iam.securityAdmin"`,
		"a custom role with projects.update":    `google_project_iam_custom_role.labeller.name`,
		"a custom role with projects.setIamPol": `google_project_iam_custom_role.policy_labeller.name`,
	} {
		t.Run("the label rule refuses "+name, func(t *testing.T) {
			files := moduleFiles(t)
			files["modules/workflow/sa.tf"] += labelRoleFor(role) + projectGrantToJob("job_label_rule", role)
			scan := scanModuleIAM(files)
			if scan.labelGrants == 0 {
				t.Errorf("the label rule counted no grant for %s: %v", role, scan.findings)
			}
			if !slices.ContainsFunc(scan.findings, func(f string) bool { return strings.Contains(f, ProjectLabelKey) }) {
				t.Errorf("%s: no finding names the %s label, so it was refused for another reason:\n%s", role, ProjectLabelKey, strings.Join(scan.findings, "\n"))
			}
		})
	}
	// The other half: the label rule reads the modules' own grants and finds
	// nothing there, which is only worth saying beside a count of what it
	// read (TestRunAccountsHaveNoWritePathToTheMarker asserts that count).
	if scan := scanModuleIAM(moduleFiles(t)); scan.labelGrants != 0 {
		t.Errorf("the modules as they are: %d grant(s) reach the %s label", scan.labelGrants, ProjectLabelKey)
	}

	// The modules as they are are the other half of the fixture: the scan
	// above must find nothing in them, or the mutations would be passing for
	// a reason that has nothing to do with them.
	// The compute module's grants are the one known exception: the scan
	// refuses them, on purpose for 0.8.5 (knownComputeRefusals), so they are
	// set aside by name and every other finding still fails this.
	if rest := otherThanComputeRefusals(t, scanModuleIAM(moduleFiles(t)).findings); len(rest) != 0 {
		t.Errorf("the modules as they are: %v", rest)
	}
}

// knownComputeRefusals are the compute module's grants (main's Cloud Batch
// backend, deploy/terraform/gcp/modules/compute/iam.tf and — Task 11's —
// reaper.tf) that this scan refuses: every one is a grant to a run, reaper,
// preload or scheduler-agent account (or to a Google service agent) that
// runAccountGrants does not list. For 0.8.5 the refusal stands (the owner's
// ruling of 2026-10-17/18 on the merge of main into next-0.8.5): nobody has
// reviewed these grants for a write path to fugaro/managed.json, so they are
// not allowlisted, and the managed cover keeps refusing the compute module.
// Reviewing them and moving them into runAccountGrants is a decision for a
// later release, and this list is what that decision deletes.
var knownComputeRefusals = []string{
	"run_agent_reporter", "run_log_writer", "run_registry_reader", "launcher_user",
	"reaper", "preload_builder", "preload_build_submitter", "preload_log_writer",
	"preload_scheduler_agent", "preload_cloud_build_agent", "preload_self_user", "preload_run_account_user",
	// The reaper's invocation (Task 11, reaper.tf): the job-scoped
	// roles/run.invoker held by the reaper account itself (the Scheduler job
	// starts the job with that account's OAuth token) and the actAs the
	// Scheduler's own agent needs to mint it. Both are refusals for
	// runAccountGrants's reason and not a judgement about them: the member is
	// an account, not a set of people, so nobody has reviewed it for a write
	// path to the creation fact. The operator actAs grant in the same file is
	// a for_each over people and is not refused.
	"reaper_runner", "reaper_scheduler_agent",
}

// computeRefusalFile is where every known compute refusal is found: the
// module's iam.tf, and the reaper's own file, which Task 11 added and which
// makes grants of its own (the job's invoker and the two actAs bindings the
// Scheduler token needs) — the same kind of grant, refused for the same
// reason, and so part of the same known limit.
var computeRefusalFiles = []string{
	"modules/compute/iam.tf: ",
	"modules/compute/reaper.tf: ",
}

// otherThanComputeRefusals returns the findings that are not one of the
// known compute refusals, and fails the test when the known refusals are not
// all there: a compute grant that stops being refused is a change to this
// known limit, made on purpose or not, and either way it must be looked at.
func otherThanComputeRefusals(t *testing.T, findings []string) []string {
	t.Helper()
	var rest []string
	seen := map[string]bool{}
	for _, f := range findings {
		name, ok := computeRefusalName(f)
		if !ok {
			rest = append(rest, f)
			continue
		}
		seen[name] = true
	}
	for _, name := range knownComputeRefusals {
		if !seen[name] {
			t.Errorf("the scan no longer refuses the compute module's %s grant: the known limit (knownComputeRefusals) changed, so review it", name)
		}
	}
	return rest
}

// computeRefusalName is the compute grant a finding refuses, when it is one
// of knownComputeRefusals, in one of the compute module's own files.
func computeRefusalName(f string) (string, bool) {
	rest, ok := "", false
	for _, file := range computeRefusalFiles {
		rest, ok = strings.CutPrefix(f, file)
		if ok {
			break
		}
	}
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(rest, " ")
	return name, slices.Contains(knownComputeRefusals, name)
}

// TestTheScanRefusesTheComputeModulesGrantsAsAKnownLimit names the known
// limit of 0.8.5: the run-account grant scan refuses every grant the compute
// module (main's Cloud Batch backend) makes to an account that is not a set
// of people, and nothing else in the modules. The refusal is the expected
// answer, not a failure to fix by allowlisting: the grants have not been
// reviewed for a write path to the creation fact.
func TestTheScanRefusesTheComputeModulesGrantsAsAKnownLimit(t *testing.T) {
	findings := scanModuleIAM(moduleFiles(t)).findings
	refused := map[string]bool{}
	for _, f := range findings {
		if name, ok := computeRefusalName(f); ok {
			if !strings.Contains(f, "not on the allowlist") && !strings.Contains(f, "cannot resolve") {
				t.Errorf("the compute grant %s is refused for another reason than the allowlist: %s", name, f)
			}
			refused[name] = true
		} else if strings.HasPrefix(f, "modules/compute/") {
			t.Errorf("a compute finding outside the known limit: %s", f)
		}
	}
	for _, name := range knownComputeRefusals {
		if !refused[name] {
			t.Errorf("the compute module's %s grant is not refused: the known limit says it is", name)
		}
	}
}

// labelRoleFor is the custom role a grant of role needs before the grant can
// be said to reach the project label, or nothing when role names one of
// Google's own — which carries the permission in its title, not in the file.
func labelRoleFor(role string) string {
	switch role {
	case `google_project_iam_custom_role.labeller.name`:
		return customRole("labeller", "jobLabeller", `"resourcemanager.projects.update"`)
	case `google_project_iam_custom_role.policy_labeller.name`:
		return customRole("policy_labeller", "jobPolicyLabeller", `"resourcemanager.projects.setIamPolicy"`)
	}
	return ""
}

// customRole is a project-level custom role holding one permission.
func customRole(name, roleID, perm string) string {
	return `
resource "google_project_iam_custom_role" "` + name + `" {
  project     = var.project
  role_id     = "` + roleID + `"
  title       = "` + roleID + `"
  permissions = [` + perm + `]
}
`
}

// projectGrantToJob is a project-level grant of role to the job account,
// named name: the shape of most of the mutations above.
func projectGrantToJob(name, role string) string {
	return `
resource "google_project_iam_member" "` + name + `" {
  project = var.project
  role    = ` + role + `
  member  = google_service_account.job.member
}
`
}

// moduleFiles is every Terraform source under deploy/terraform: the modules
// and the roots that call them, path (relative to deploy/terraform/gcp) →
// content. A .tf.json file is read too, so the scan can refuse it: it is a
// syntax this test does not parse.
func moduleFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "deploy", "terraform", "gcp")
	files := map[string]string{}
	err := filepath.WalkDir(filepath.Join(root, ".."), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".terraform" {
				return filepath.SkipDir // a local terraform init's provider cache
			}
			return nil
		}
		if !strings.HasSuffix(path, ".tf") && !strings.HasSuffix(path, ".tf.json") {
			return nil
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

	// fencedWrites is every project-level grant of a write role the scan
	// admitted under the deployer fence (binding (2)): one, today.
	fencedWrites int

	// runAccountGrants is how many of runAccountGrants the scan matched.
	runAccountGrants int

	// labelChecked is every grant to a member that is not a set of people
	// whose role this scan could resolve, and labelGrants the ones that give
	// that member a way to set or remove the project's own label (Task 3.0):
	// the first is what makes the second's zero an assertion rather than a
	// vacuous pass.
	labelChecked, labelGrants int

	conditions map[string]int
}

func (s *iamScan) failf(format string, a ...any) {
	s.findings = append(s.findings, fmt.Sprintf(format, a...))
}

// scanModuleIAM reads every Terraform source under deploy/terraform (path →
// content: the modules and the roots that call them) and reports what may
// give a run's credential a write path to the creation fact at the runs
// bucket's root.
//
// It reads the sources with a lexer of its own (strings, interpolations,
// heredocs and all three comment forms) rather than a pattern per block, and
// a block it cannot read is a finding, not a skip: so is a .tf.json file, a
// top-level block of a kind it does not know, a module whose source is not
// one of the modules here, and any difference between the resource and
// module keywords in a file and the blocks it handled.
//
// Two rules over what it reads. First, every grant to anyone who is not one
// of the people sets (the operators, launchers and admins, the variables a
// person is configured into) — a run's job or build account, any service
// account, any literal member — must be one of runAccountGrants below, the
// grants the modules make today pinned whole (resource type, role, the
// resource it is on, the member, its for_each, and a custom role's
// permissions): a role nobody has reviewed for a run account is a finding,
// whatever API it belongs to, because a role that is not a storage role can
// still reach the bucket (projectIamAdmin grants storage.admin; tokenCreator
// on the default compute account is roles/editor). This covers every
// _iam_member, _iam_binding and _iam_policy resource type, the folder's and
// the organization's included, and an ACL resource is refused outright.
// Second, the storage-specific checks the first rule does not replace: a
// write role on the runs bucket is accepted only when it is granted to a set
// of people (the operators' own access, the documented limit) or when the
// grant's condition is one this test recognises as keeping it off the
// bucket's root; and no member may hold a storage write role at the project
// level, which the bucket's policy does not bound (SECURITY.md:56).
func scanModuleIAM(files map[string]string) iamScan {
	s := iamScan{conditions: map[string]int{}}
	resources, locals := s.parseFiles(files)
	roles := s.customRolePermissions(resources)
	seen := map[string]bool{}
	for _, r := range resources {
		dir := path.Dir(r.path)
		switch {
		case r.typ == "google_project_iam_custom_role" || r.typ == "google_organization_iam_custom_role":
			s.customRoles++
		case iamGrantTypeRE.MatchString(r.typ):
			s.checkRunAccountGrant(r, roles, locals[dir], seen)
			s.checkLabelGrant(r, roles, locals[dir])
			switch strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(r.typ, "_member"), "_binding"), "_policy") {
			case "google_storage_bucket_iam":
				s.checkBucketGrant(r, roles, locals[dir])
			case "google_project_iam", "google_folder_iam", "google_organization_iam":
				s.checkProjectGrant(r, roles, locals[dir])
			}
		case accessTypeRE.MatchString(r.typ):
			s.failf("%s: %s is a %s, a grant this test does not read: a run's credential must have no write path to %s, so name what it grants in runAccountGrants's terms (an _iam_member) or extend this test",
				r.path, r.name, r.typ, MarkerObject)
		}
	}
	// The allowlist is the modules' grants, not a superset of them: an entry
	// nothing makes any more is dropped from it, so it cannot stand ready for
	// a grant to come back without a review.
	for _, k := range slices.Sorted(maps.Keys(runAccountGrants)) {
		if !seen[k] {
			s.failf("runAccountGrants has %q, which no module grants any more: drop it", k)
		}
	}
	s.runAccountGrants = len(seen)
	return s
}

// iamGrantTypeRE is every IAM grant resource type of the Google provider:
// the member, binding and policy forms of a project, folder, organization,
// bucket, service account, secret, repository, job, ...
var iamGrantTypeRE = regexp.MustCompile(`^google_[a-z0-9_]+_iam_(member|binding|policy)$`)

// accessTypeRE is any other resource type that may grant access: an IAM
// type of a form the scan does not read (an audit config, a type of the
// google_iam_ family) and the ACL resources, which a bucket without uniform
// access would honour.
var accessTypeRE = regexp.MustCompile(`_iam_|_iam$|^google_iam_|_acl$|_acl_|access_control`)

// runAccountGrants is every grant the modules make to a member that is not
// a set of people, pinned whole: resource type | role | the resource it is
// on | member | for_each | the custom role's permissions. These are the run
// accounts' roles (the job's, the build's), the history and scheduler
// accounts', and the operators' grants built from a for_each this test does
// not resolve to people. A new grant to any of them, a role changed, a
// target changed or a custom role widened is a key that is not here, which
// is a finding: add it only after reviewing that it gives none of them a
// way to write the runs bucket's root (directly, by granting itself a role,
// by acting as or minting tokens for another account, or by running a build
// as one).
var runAccountGrants = setOf(
	// The job account's (a run's own credential): the runs bucket under its
	// condition, its secrets one by one, Vertex AI.
	`google_project_iam_member | "roles/aiplatform.user" | project=var.project | google_service_account.job.member | for_each= | perms=`,
	`google_secret_manager_secret_iam_member | "roles/secretmanager.secretAccessor" | secret_id=var.secrets[each.value] | google_service_account.job.member | for_each=toset(values(var.workflow.secret_env)) | perms=`,
	`google_storage_bucket_iam_member | "roles/storage.objectUser" | bucket=var.runs_bucket | google_service_account.job.member | for_each= | perms=`,
	// The same account's read-only grant on the budget token objects, in the
	// workflow module (tokens/<slug>/, one repository) and in the project
	// module (the whole of tokens/, a shared job; phase 3, modules/project/
	// sa.tf). objectViewer carries no create, no delete and no update, so
	// neither can write, replace or remove a token object — and what the
	// shared job reads is sealed ciphertext (budget-token-channel.md §7.2).
	`google_storage_bucket_iam_member | "roles/storage.objectViewer" | bucket=var.runs_bucket | google_service_account.job.member | for_each= | perms=`,
	// A project job account's (phase 3, modules/project/sa.tf): the same
	// shape, its secrets keyed by the profile's mounts; its bucket grant is
	// the workflow job's key above, under the widened condition.
	`google_secret_manager_secret_iam_member | "roles/secretmanager.secretAccessor" | secret_id=var.secrets[each.value] | google_service_account.job.member | for_each=toset(values(var.job.secret_env)) | perms=`,

	// The build account's (a build's credential): its secrets, its own
	// registry, the shared ones read-only, builds/ under its condition, actAs
	// on itself alone, and log writing and build submission on the project
	// (the residual: a build that names no account may run as the default
	// one, Check 68).
	`google_artifact_registry_repository_iam_member | "roles/artifactregistry.reader" | repository=var.installation.base_registry | google_service_account.build.member | for_each= | perms=`,
	`google_artifact_registry_repository_iam_member | "roles/artifactregistry.reader" | repository=var.installation.ghcr_registry | google_service_account.build.member | for_each= | perms=`,
	`google_artifact_registry_repository_iam_member | "roles/artifactregistry.writer" | repository=google_artifact_registry_repository.images.repository_id | google_service_account.build.member | for_each= | perms=`,
	`google_artifact_registry_repository_iam_member | var.installation.role_ids.tag_mover | repository=google_artifact_registry_repository.images.repository_id | google_service_account.build.member | for_each= | perms=artifactregistry.tags.delete`,
	`google_project_iam_member | "roles/logging.logWriter" | project=var.project | google_service_account.build.member | for_each={ log_writer = "roles/logging.logWriter" build_submitter = var.installation.role_ids.build_submitter } | perms=`,
	`google_project_iam_member | var.installation.role_ids.build_submitter | project=var.project | google_service_account.build.member | for_each={ log_writer = "roles/logging.logWriter" build_submitter = var.installation.role_ids.build_submitter } | perms=cloudbuild.builds.create,cloudbuild.builds.get`,
	`google_secret_manager_secret_iam_member | "roles/secretmanager.secretAccessor" | secret_id=local.secrets[each.value] | google_service_account.build.member | for_each=toset(var.repo.build_secrets) | perms=`,
	`google_service_account_iam_member | "roles/iam.serviceAccountUser" | service_account_id=google_service_account.build.name | google_service_account.build.member | for_each= | perms=`,
	`google_storage_bucket_iam_member | "roles/storage.objectUser" | bucket=var.installation.runs_bucket | google_service_account.build.member | for_each= | perms=`,

	// The history account's: the Firebase databases, its run listing, and a
	// read of the runs bucket.
	`google_project_iam_member | "roles/datastore.user" | project=var.project | "serviceAccount:${var.history_account}" | for_each= | perms=`,
	`google_project_iam_member | "roles/firebaseauth.admin" | project=var.project | "serviceAccount:${var.history_account}" | for_each= | perms=`,
	`google_project_iam_member | "roles/firebasedatabase.admin" | project=var.project | "serviceAccount:${var.history_account}" | for_each= | perms=`,
	`google_project_iam_member | "roles/serviceusage.serviceUsageConsumer" | project=var.project | "serviceAccount:${var.history_account}" | for_each= | perms=`,
	// Task 1.5's step 5 writes this role as the literal projects/<project>
	// /roles/<id>, known at plan time, so a plan that creates a deployer role
	// can refuse every project grant of a role known only after apply.
	`google_project_iam_member | "projects/${var.project}/roles/fugaroHistory" | project=var.project | google_service_account.history[0].member | for_each= | perms=run.executions.get,run.executions.list,run.jobs.get,run.jobs.list,run.operations.get`,
	`google_storage_bucket_iam_member | "roles/storage.objectViewer" | bucket=google_storage_bucket.runs.name | google_service_account.history[0].member | for_each= | perms=`,

	// The scheduler account's: starting the history and check jobs.
	`google_cloud_run_v2_job_iam_member | "roles/run.invoker" | name=google_cloud_run_v2_job.check[0].name | "serviceAccount:${var.installation.scheduler_service_account}" | for_each= | perms=`,
	`google_cloud_run_v2_job_iam_member | google_project_iam_custom_role.job_runner.name | name=google_cloud_run_v2_job.history[0].name | google_service_account.scheduler.member | for_each= | perms=run.jobs.run,run.jobs.runWithOverrides`,

	// The deployer account's (D75, Task 1.5): the identity an agent-authored
	// root is applied as, and the only project-level grant of a storage write
	// role this test admits. The unconditioned role holds no storage
	// permission but storage.buckets.create (storageNeutralPerms: creating a
	// bucket cannot touch the runs bucket), and the conditioned role's storage
	// writes are fenced by binding (2), which the scan reads clause for clause
	// (managedPrefixFence) and which the runs bucket's own name is outside.
	`google_project_iam_member | "projects/${var.project}/roles/fugaroDeployer" | project=var.project | google_service_account.fugaro_deployer.member | for_each= | perms=apikeys.keys.create,apikeys.keys.get,apikeys.keys.list,artifactregistry.repositories.create,artifactregistry.repositories.get,artifactregistry.repositories.getIamPolicy,artifactregistry.repositories.list,cloudscheduler.jobs.create,cloudscheduler.jobs.get,cloudscheduler.jobs.list,iam.serviceAccounts.create,iam.serviceAccounts.get,iam.serviceAccounts.getIamPolicy,iam.serviceAccounts.list,run.jobs.create,run.jobs.get,run.jobs.getIamPolicy,run.jobs.list,secretmanager.secrets.create,secretmanager.secrets.get,secretmanager.secrets.getIamPolicy,secretmanager.secrets.list,serviceusage.services.enable,serviceusage.services.get,serviceusage.services.list,storage.buckets.create`,
	`google_project_iam_member | "projects/${var.project}/roles/fugaroDeployerScoped" | project=var.project | google_service_account.fugaro_deployer.member | for_each= | perms=apikeys.keys.delete,apikeys.keys.update,artifactregistry.repositories.delete,artifactregistry.repositories.setIamPolicy,artifactregistry.repositories.update,cloudscheduler.jobs.delete,cloudscheduler.jobs.enable,cloudscheduler.jobs.pause,cloudscheduler.jobs.run,cloudscheduler.jobs.update,iam.serviceAccounts.actAs,iam.serviceAccounts.delete,iam.serviceAccounts.setIamPolicy,iam.serviceAccounts.update,run.jobs.delete,run.jobs.run,run.jobs.runWithOverrides,run.jobs.setIamPolicy,run.jobs.update,secretmanager.secrets.delete,secretmanager.secrets.setIamPolicy,secretmanager.secrets.update,storage.buckets.delete,storage.buckets.get,storage.buckets.setIamPolicy,storage.buckets.update,storage.objects.create,storage.objects.delete,storage.objects.get,storage.objects.list,storage.objects.update`,

	// The operators' and launchers' grants whose for_each this test does not
	// resolve to a set of people (a conditional, a merge of pairs): pinned
	// whole, so a member slipped into one is a different key.
	`google_cloud_run_v2_job_iam_member | var.job_runner_role | name=google_cloud_run_v2_job.this[0].name | each.value | for_each=var.workflow.deploy_job ? toset(var.launchers) : toset([]) | perms=?`,
	`google_logging_log_view_iam_member | "roles/logging.viewAccessor" | name=google_logging_log_view.runs[0].name | each.value | for_each=var.log_isolation ? toset(concat(var.launchers, var.operators)) : toset([]) | perms=`,
	`google_secret_manager_secret_iam_member | each.value.role | secret_id=google_secret_manager_secret.project[each.value.secret].secret_id | each.value.member | for_each=merge( { for k, v in local.operator_project_secrets : "${k} secretVersionAdder" => merge(v, { role = "roles/secretmanager.secretVersionAdder" }) }, { for k, v in local.operator_project_secrets : "${k} viewer" => merge(v, { role = "roles/secretmanager.viewer" }) }, ) [local.operator_project_secrets={ for p in setproduct(keys(var.project_secrets), var.operators) : "${p[0]} ${p[1]}" => { secret = p[0], member = p[1] } }] | perms=?`,
	`google_secret_manager_secret_iam_member | each.value.role | secret_id=google_secret_manager_secret.this[each.value.secret].secret_id | each.value.member | for_each=merge( { for k, v in local.operator_secrets : "${k} secretVersionAdder" => merge(v, { role = "roles/secretmanager.secretVersionAdder" }) }, { for k, v in local.operator_secrets : "${k} viewer" => merge(v, { role = "roles/secretmanager.viewer" }) }, ) [local.operator_secrets={ for p in setproduct(keys(var.repo.secrets), var.installation.operators) : "${p[0]} ${p[1]}" => { secret = p[0], member = p[1] } }] | perms=?`,
)

// setOf is a set of strings.
func setOf(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// checkRunAccountGrant is the allowlist rule of scanModuleIAM, for one grant.
func (s *iamScan) checkRunAccountGrant(r tfResource, roles map[string][]string, locals map[string]string, seen map[string]bool) {
	members, ok := grantMembers(r)
	if !ok {
		s.failf("%s: %s is a %s whose members this test cannot read (a policy_data document, or a members list that is not a list literal): use the member form, or extend this test",
			r.path, r.name, r.typ)
		return
	}
	rolesOf := grantRoles(r, roles)
	if len(rolesOf) == 0 {
		s.failf("%s: %s is a grant with no role this test can read", r.path, r.name)
		return
	}
	forEach := field(r.body, "for_each")
	target := grantTarget(r.body)
	for _, role := range rolesOf {
		for _, m := range members {
			if isPersonSet(m, forEach, locals) {
				continue
			}
			key := grantKey(r.typ, role, target, m, forEach, roles, locals)
			if !runAccountGrants[key] {
				s.failf("%s: %s grants %s on %s to %s, which is not a set of people and not on the allowlist (runAccountGrants): a role nobody reviewed for this member may give it a write path to %s\n\tkey: %q",
					r.path, r.name, role, target, m, MarkerObject, key)
				continue
			}
			seen[key] = true
		}
	}
}

// labelWritePerms are the permissions that set, change or remove the
// project's own fugaro_managed label — the second half of the creation fact,
// written by the owner-run command that creates the project and required by
// the read (Task 3.0, design finding 14). Both are on the list because
// either one is enough: resourcemanager.projects.update writes the label
// directly, and resourcemanager.projects.setIamPolicy is how a holder grants
// itself the first (Project IAM Admin does not carry update; Owner and Editor
// carry both, which is why labelWriteRoles exists as well).
//
// A run's credential holding either is a finding however it is spelled, and
// that is what makes this rule different from the storage rules above: the
// label is not in the runs bucket, so no bucket condition and no prefix fence
// can bound it. The permission is the whole of what it takes to put a project
// into managed mode, or to take it out.
var labelWritePerms = map[string]bool{
	"resourcemanager.projects.update":       true,
	"resourcemanager.projects.setIamPolicy": true,
}

// labelWriteRoles are the predefined roles this test knows carry one of the
// permissions above, and **the list is not claimed to be exhaustive**. It is
// a list rather than a permission check because a test cannot ask Google what
// roles/xyzzy carries (Global Constraint 12: no live IAM read), and a role
// whose contents this test cannot resolve is not silently passed: it lands in
// runAccountGrants, which pins every grant to a non-people member whole, so a
// predefined role reaching a run account is a finding there whether or not it
// is named here. What naming one adds is the second, independent reason for
// the roles whose contents are not in doubt — the rule still refuses them if
// a future task widens runAccountGrants without reading what it admitted.
//
// The four are chosen by what they carry and nothing else: Owner and Editor
// carry resourcemanager.projects.update (and setIamPolicy with it), and
// Project IAM Admin and Security Admin carry
// resourcemanager.projects.setIamPolicy, which is how a holder grants itself
// the update. A role this test does not know is left to the allowlist rather
// than guessed at: a wrong guess here would be a rule that refuses nothing.
var labelWriteRoles = map[string]bool{
	"roles/owner":                           true,
	"roles/editor":                          true,
	"roles/resourcemanager.projectIamAdmin": true,
	"roles/iam.securityAdmin":               true,
}

// checkLabelGrant refuses a grant that would let a credential a run holds set
// or remove the project's fugaro_managed label. It is the third rule over the
// same grants checkRunAccountGrant and checkBucketGrant read, and it is the
// plan's Open item 2 answered by meeting it rather than by falling back to
// it: the fixture could be written, so the label is not refused for want of
// one (docs/plans/2026-10-18-managed-projects-task-3.0-decisions.md §6).
//
// Members that are a set of people are skipped, exactly as they are above:
// the operators' own accounts hold resourcemanager.projects.update by
// construction — the owner-run command that writes the label runs on one —
// and that is the documented limit rather than a hole (SECURITY.md).
func (s *iamScan) checkLabelGrant(r tfResource, roles map[string][]string, locals map[string]string) {
	members, ok := grantMembers(r)
	if !ok {
		return // checkRunAccountGrant reports a shape this test cannot read
	}
	forEach := field(r.body, "for_each")
	for _, role := range grantRoles(r, roles) {
		why, resolved := labelGrantWhy(role, roles)
		if !resolved {
			continue // not a role this test can resolve: checkRunAccountGrant's finding
		}
		for _, m := range members {
			if isPersonSet(m, forEach, locals) {
				continue
			}
			s.labelChecked++
			if why == "" {
				continue
			}
			s.labelGrants++
			s.failf("%s: %s grants %s on %s to %s, and %s: no credential a run holds may set or remove the project's %s label, which is what puts a project into managed mode (Task 3.0; the label is written by the run that creates the project or by the owner's own flag command, and by no credential a run holds)",
				r.path, r.name, role, grantTarget(r.body), m, why, ProjectLabelKey)
		}
	}
}

// labelGrantWhy says why a grant of role would let its holder set or remove
// the project's label, and whether the role could be resolved at all: a
// predefined role by the two names above, and a custom role by the
// permissions this scan read for it (customRolePermissions). A predefined
// role that carries the permission but is not named above is not resolved
// here — runAccountGrants is where it would be caught.
func labelGrantWhy(role string, roles map[string][]string) (why string, resolved bool) {
	ref := unquote(strings.TrimSpace(role))
	if labelWriteRoles[ref] {
		return ref + " carries resourcemanager.projects.update", true
	}
	if strings.HasPrefix(ref, "roles/") {
		return "", true
	}
	perms, ok := roles[customRoleRef(role)]
	if !ok {
		return "", false
	}
	for _, p := range perms {
		if labelWritePerms[p] {
			return fmt.Sprintf("the custom role %s holds %s", ref, p), true
		}
	}
	return "", true
}

// grantTarget is the resource a grant is on: the first of the attributes
// that name one, most specific first.
func grantTarget(body string) string {
	for _, k := range []string{"service_account_id", "secret_id", "repository", "name", "bucket", "project", "folder", "org_id"} {
		if v := field(body, k); v != "" {
			return k + "=" + v
		}
	}
	return "?"
}

// grantKey is a grant as runAccountGrants keys it. A local its for_each
// reads is part of it, so the set the grant iterates cannot change under the
// key.
func grantKey(typ, role, target, member, forEach string, roles map[string][]string, locals map[string]string) string {
	perms := ""
	if !strings.HasPrefix(unquote(role), "roles/") {
		if p, ok := roles[customRoleRef(role)]; ok {
			p = slices.Clone(p)
			slices.Sort(p)
			perms = strings.Join(p, ",")
		} else {
			perms = "?"
		}
	}
	fe := strings.Join(strings.Fields(forEach), " ")
	refs := tfRefRE.FindAllString(forEach, -1)
	slices.Sort(refs)
	for _, ref := range slices.Compact(refs) {
		if name, ok := strings.CutPrefix(ref, "local."); ok {
			fe += " [" + ref + "=" + strings.Join(strings.Fields(locals[name]), " ") + "]"
		}
	}
	return strings.Join([]string{typ, role, target, member, "for_each=" + fe, "perms=" + perms}, " | ")
}

// tfResource is one Terraform resource block; body is its text between the
// braces, comments blanked.
type tfResource struct{ path, typ, name, body string }

// parseFiles is every resource block in files, in path order, and every
// module directory's locals (directory → name → expression). What it cannot
// read is a finding.
func (s *iamScan) parseFiles(files map[string]string) ([]tfResource, map[string]map[string]string) {
	var out []tfResource
	locals := map[string]map[string]string{}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasSuffix(p, ".tf") {
			s.failf("%s: a Terraform file in a syntax this test does not read: write it as .tf, or extend this test", p)
			continue
		}
		clean, braces, err := lexHCL(files[p])
		if err != nil {
			s.failf("%s: %v: a file this test cannot read may hold a grant it cannot see", p, err)
			continue
		}
		blocks, err := topBlocks(clean, braces)
		if err != nil {
			s.failf("%s: %v: a file this test cannot read may hold a grant it cannot see", p, err)
			continue
		}
		dir := path.Dir(p)
		var resources, modules int
		for _, b := range blocks {
			switch b.typ {
			case "resource":
				if len(b.labels) != 2 {
					s.failf("%s: a resource block with labels %q", p, b.labels)
					continue
				}
				resources++
				out = append(out, tfResource{path: p, typ: b.labels[0], name: b.labels[1], body: b.body})
			case "module":
				modules++
				s.checkModule(p, b, locals)
			case "locals":
				if locals[dir] == nil {
					locals[dir] = map[string]string{}
				}
				for _, k := range attrNames(b.body) {
					locals[dir][k] = field(b.body, k)
				}
			case "data", "variable", "output", "terraform", "provider", "moved", "removed", "import", "check":
			default:
				s.failf("%s: a top-level %q block, a kind this test does not know", p, b.typ)
			}
		}
		// The cross-check of the parser itself: every resource and module
		// keyword in the file is a block it handled.
		if n := len(resourceKeywordRE.FindAllString(clean, -1)); n != resources {
			s.failf("%s: %d resource keyword(s) but %d resource block(s) read: a resource this test cannot read may hold a grant it cannot see", p, n, resources)
		}
		if n := len(moduleKeywordRE.FindAllString(clean, -1)); n != modules {
			s.failf("%s: %d module keyword(s) but %d module block(s) read", p, n, modules)
		}
	}
	return out, locals
}

var (
	resourceKeywordRE = regexp.MustCompile(`(?m)^\s*resource(?:\s+["A-Za-z_]|")`)
	moduleKeywordRE   = regexp.MustCompile(`(?m)^\s*module(?:\s+["A-Za-z_]|")`)
)

// checkModule is one module call: its source must be one of the modules
// this test scans (anything else could grant what it likes), and what it
// passes as a set of people must be one.
func (s *iamScan) checkModule(p string, b hclBlock, locals map[string]map[string]string) {
	src := unquote(field(b.body, "source"))
	target := path.Join(path.Dir(p), src)
	if !strings.HasPrefix(src, "../") || !strings.HasPrefix(target, "modules/") || strings.Contains(strings.TrimPrefix(target, "modules/"), "/") {
		s.failf("%s: module %q has source %q, which is not one of the modules this test scans: whatever it grants is unread", p, strings.Join(b.labels, " "), src)
	}
	for _, k := range []string{"operators", "launchers", "admins", "budget_admins"} {
		v := field(b.body, k)
		if v == "" {
			continue
		}
		if !peopleVars[v] && !peopleSet(v, locals[path.Dir(p)]) {
			s.failf("%s: module %q passes %s = %s, which is not a set of people: the module grants that set what it grants the people", p, strings.Join(b.labels, " "), k, v)
		}
	}
}

// hclBlock is one top-level block: its type, its labels and its body.
type hclBlock struct {
	typ    string
	labels []string
	body   string
}

// lexHCL is src with every comment (#, // and /* */) and every heredoc's
// text blanked, newlines kept so offsets and lines stay, and the offsets of
// the braces that are structure: not inside a string or an interpolation.
func lexHCL(src string) (string, []int, error) {
	b := []byte(src)
	blank := func(from, to int) {
		for k := from; k < to; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	type frame struct {
		str   bool // a quoted string, or else an interpolation's code
		depth int  // an interpolation's own open braces
	}
	var stack []frame
	var braces []int
	at := func(i int, s string) bool { return bytes.HasPrefix(b[i:], []byte(s)) }
	for i := 0; i < len(b); {
		c := b[i]
		if n := len(stack); n > 0 && stack[n-1].str {
			switch {
			case c == '\\':
				i += 2
			case c == '"':
				stack = stack[:n-1]
				i++
			case at(i, "$${") || at(i, "%%{"):
				i += 3
			case at(i, "${") || at(i, "%{"):
				stack = append(stack, frame{})
				i += 2
			case c == '\n':
				return "", nil, fmt.Errorf("line %d: a string that does not end on its line", 1+bytes.Count(b[:i], []byte("\n")))
			default:
				i++
			}
			continue
		}
		switch {
		case c == '#' || at(i, "//"):
			j := bytes.IndexByte(b[i:], '\n')
			if j < 0 {
				j = len(b) - i
			}
			blank(i, i+j)
			i += j
		case at(i, "/*"):
			j := bytes.Index(b[i+2:], []byte("*/"))
			if j < 0 {
				return "", nil, fmt.Errorf("a /* comment that does not end")
			}
			blank(i, i+2+j+2)
			i += 2 + j + 2
		case c == '"':
			stack = append(stack, frame{str: true})
			i++
		case at(i, "<<"):
			m := heredocRE.FindSubmatch(b[i:])
			if m == nil {
				return "", nil, fmt.Errorf("line %d: a << this test does not read", 1+bytes.Count(b[:i], []byte("\n")))
			}
			end := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(string(m[1])) + `[ \t]*$`).FindIndex(b[i+len(m[0]):])
			if end == nil {
				return "", nil, fmt.Errorf("a heredoc %s that does not end", m[1])
			}
			stop := i + len(m[0]) + end[1]
			blank(i, stop)
			i = stop
		case c == '{' || c == '}':
			if n := len(stack); n > 0 {
				switch {
				case c == '{':
					stack[n-1].depth++
				case stack[n-1].depth == 0:
					stack = stack[:n-1] // back into the string
				default:
					stack[n-1].depth--
				}
			} else {
				braces = append(braces, i)
			}
			i++
		default:
			i++
		}
	}
	if len(stack) != 0 {
		return "", nil, fmt.Errorf("a string or interpolation that does not end")
	}
	return string(b), braces, nil
}

var heredocRE = regexp.MustCompile(`^<<-?([A-Za-z_][A-Za-z0-9_-]*)\r?\n`)

// topBlocks splits lexed text into its top-level blocks. Anything at the
// top level that is not a block header followed by its body is an error:
// there is nowhere for a grant to hide in text this does not read.
func topBlocks(clean string, braces []int) ([]hclBlock, error) {
	var out []hclBlock
	depth, prev, open := 0, 0, 0
	var header string
	for _, p := range braces {
		if clean[p] == '{' {
			if depth == 0 {
				header, open = clean[prev:p], p
			}
			depth++
			continue
		}
		depth--
		if depth < 0 {
			return nil, fmt.Errorf("line %d: a } with no {", 1+strings.Count(clean[:p], "\n"))
		}
		if depth > 0 {
			continue
		}
		m := blockHeaderRE.FindStringSubmatch(header)
		if m == nil {
			return nil, fmt.Errorf("line %d: %q is not a block header this test reads", 1+strings.Count(clean[:open], "\n"), strings.TrimSpace(header))
		}
		var labels []string
		for _, l := range blockLabelRE.FindAllStringSubmatch(m[2], -1) {
			labels = append(labels, l[1]+l[2])
		}
		out = append(out, hclBlock{typ: m[1], labels: labels, body: clean[open+1 : p]})
		prev = p + 1
	}
	if depth != 0 {
		return nil, fmt.Errorf("a block that does not end")
	}
	if rest := strings.TrimSpace(clean[prev:]); rest != "" {
		return nil, fmt.Errorf("%q after the last block", rest)
	}
	return out, nil
}

var (
	blockHeaderRE = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_-]*)((?:[ \t]+(?:"[^"\n]*"|[A-Za-z_][A-Za-z0-9_-]*))*)[ \t]*$`)
	blockLabelRE  = regexp.MustCompile(`"([^"\n]*)"|([A-Za-z_][A-Za-z0-9_-]*)`)
)

// topLevel is a block body with its nested blocks' and literals' insides
// blanked, offsets kept: the text where its own attributes are.
func topLevel(body string) string {
	_, braces, err := lexHCL(body)
	if err != nil {
		return ""
	}
	b := []byte(body)
	depth, open := 0, 0
	for _, p := range braces {
		if b[p] == '{' {
			if depth == 0 {
				open = p
			}
			depth++
			continue
		}
		depth--
		if depth == 0 {
			for k := open + 1; k < p; k++ {
				if b[k] != '\n' {
					b[k] = ' '
				}
			}
		}
	}
	return string(b)
}

// nestedBlocks is the bodies of a block body's nested blocks named name.
func nestedBlocks(body, name string) []string {
	_, braces, err := lexHCL(body)
	if err != nil {
		return nil
	}
	var out []string
	depth, open := 0, 0
	for _, p := range braces {
		if body[p] == '{' {
			if depth == 0 {
				open = p
			}
			depth++
			continue
		}
		depth--
		if depth == 0 {
			line := body[strings.LastIndexByte(body[:open], '\n')+1 : open]
			if strings.TrimSpace(line) == name {
				out = append(out, body[open+1:p])
			}
		}
	}
	return out
}

// attrNames is the attributes a block body sets at its own level.
func attrNames(body string) []string {
	var out []string
	for _, m := range attrNameRE.FindAllStringSubmatch(topLevel(body), -1) {
		out = append(out, m[1])
	}
	return out
}

var attrNameRE = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_-]*)\s*=[^=]`)

// runsBucketSources are the expressions that are the runs bucket in the
// modules: the installation module's own resource, and the bucket the
// installation's outputs hand the other modules.
var runsBucketSources = map[string]bool{
	"google_storage_bucket.runs.name": true,
	"var.runs_bucket":                 true,
	"var.installation.runs_bucket":    true,
}

// otherBucketSources are the bucket-level grants' other buckets, named one by
// one so that the runs bucket is not the only expression this test knows
// what to do with: the installation's Terraform state bucket, which holds no
// object at a fugaro/ root (the creation fact and the delete list are in the
// runs bucket alone). Any other expression is a bucket this test cannot
// identify, which is a finding: a grant on the runs bucket under a name the
// scan does not know — a literal, a local, a new variable — would otherwise
// be the one shape that passes the assertion by being unreadable.
var otherBucketSources = map[string]bool{"var.state_bucket": true}

// customRolePermissions maps every custom role the modules define to its
// permissions, under two names: the resource it is (the modules refer to it
// as google_project_iam_custom_role.launcher.name, say) and the last segment
// of its role_id (var.installation.role_ids.launcher, say, which is how the
// repository module reaches the installation's roles). Two roles under one
// name is a finding: which of them a grant means would be a guess.
func (s *iamScan) customRolePermissions(resources []tfResource) map[string][]string {
	roles := map[string][]string{}
	add := func(r tfResource, k string, perms []string) {
		if old, ok := roles[k]; ok && !slices.Equal(old, perms) {
			s.failf("%s: %s: two custom roles answer to %q, so which one a grant names is a guess", r.path, r.name, k)
		}
		roles[k] = perms
	}
	for _, r := range resources {
		if r.typ != "google_project_iam_custom_role" && r.typ != "google_organization_iam_custom_role" {
			continue
		}
		perms, ok := stringList(r.body, "permissions")
		if !ok {
			s.failf("%s: %s is a custom role whose permissions this test cannot read", r.path, r.name)
		}
		key := r.typ + "." + r.name
		add(r, key, perms)
		id := lastSegment(unquote(field(r.body, "role_id")))
		if id != "" {
			add(r, id, perms)
		}
		for lit, ref := range literalRoleIDs {
			if ref != key {
				continue
			}
			// The mapping has to be the resource it names: the role_id this
			// resource carries must end in the literal id (fugaroLauncher
			// and var.names.role_ids.launcher's last segment, launcher).
			if id == "" || !strings.HasSuffix(roleIDKey(lit), roleIDKey(id)) {
				s.failf("%s: %s has role_id %q, but literalRoleIDs maps the id %q to it: the id a grant names and the role the module creates have to agree",
					r.path, r.name, id, lit)
				continue
			}
			add(r, lit, perms)
		}
	}
	return roles
}

// literalRoleIDs are the role ids a grant of Task 1.5's step 5 names as a
// literal, and the custom role resource that creates each, for the two roles
// whose role_id the module takes from var.names.role_ids.*: the value is a Go
// constant written into the root's tfvars (internal/infra's RoleLauncher and
// RoleBuildSubmitter), so the Terraform text carries only the variable. The
// mapping is stated here and checked against the resource's own role_id
// above, so mapping an id to the wrong resource is a finding rather than a
// grant read against another role's permissions. The roles whose role_id is a
// literal — fugaroHistory and Task 1.5's two deployer roles — need no entry:
// the scan reads their id from the resource itself.
var literalRoleIDs = map[string]string{
	"fugaroLauncher":       "google_project_iam_custom_role.launcher",
	"fugaroBuildSubmitter": "google_project_iam_custom_role.build_submitter",
}

// roleIDKey is a role id compared the way the two spellings differ: the
// module's variable is a last segment (build_submitter) and the id a grant
// names is the id itself (fugaroBuildSubmitter).
func roleIDKey(id string) string { return strings.ToLower(strings.ReplaceAll(id, "_", "")) }

// peopleVars are the variables a set of people is built from: the only
// member sets the modules may grant an unconditioned write on the runs
// bucket to (operators hold the bucket's root by design, SECURITY.md:56, and
// the launchers' own write is conditioned), as a module reads them and as
// the repository module reads the installation's. checkModule holds every
// module call to passing a set of people where a module expects one.
var peopleVars = map[string]bool{
	"var.operators": true, "var.launchers": true, "var.admins": true, "var.budget_admins": true,
	"var.installation.operators": true, "var.installation.launchers": true,
}

// peopleSet reports whether a for_each expression is a set of people:
// var.operators and var.launchers, directly or through a locals block. A
// reference to anything else — a service account, a literal, a variable this
// test does not know — is not one, so each.value over it is not a person.
func peopleSet(expr string, locals map[string]string) bool { return peopleSetDepth(expr, locals, 0) }

func peopleSetDepth(expr string, locals map[string]string, depth int) bool {
	expr = strings.TrimSpace(expr)
	// A quoted member anywhere in the expression makes it not a set of
	// people, whatever variables it also names: setunion(toset(var.operators),
	// toset(["serviceAccount:job@…"])) is the operators and a service
	// account, and each.value over it is whichever member the grant is for.
	// Every set of people in the modules is built from variables and locals
	// alone.
	if strings.Contains(expr, `"`) {
		return false
	}
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
		if storageNeutralPerms[p] {
			continue // neither a read nor a write of anything that exists
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

// storageNeutralPerms are storage permissions that are neither a read nor a
// write path to a bucket that exists: creating a bucket makes a new one, and
// the one name that matters (the runs bucket's) is taken, so a role holding
// this and nothing else in the storage API cannot write the bucket's root.
// Task 1.5's unconditioned deployer role holds exactly this one storage
// permission; adding another to it makes the role a write again, which this
// test then refuses unless the grant carries the fence below.
var storageNeutralPerms = map[string]bool{"storage.buckets.create": true}

// storageReadPerms are the only storage permissions a custom role may hold
// and stay a read: anything else in the storage API (create, delete, update,
// a wildcard) is a write.
var storageReadPerms = map[string]bool{
	"storage.objects.get": true, "storage.objects.list": true,
	"storage.buckets.get": true, "storage.buckets.list": true,
	"storage.objects.getIamPolicy": true, "storage.buckets.getIamPolicy": true,
}

// customRoleRef is a custom role reference as customRolePermissions keys it:
// google_project_iam_custom_role.history[0].name is the resource,
// var.installation.role_ids.build_submitter the role_id's last segment, and
// the literal projects/<project>/roles/<id> Task 1.5's step 5 writes the id —
// a grant whose role the plan knows, so that a plan which creates a deployer
// role can refuse every project grant of a role known only after apply
// (internal/infra/tf, checkGrant).
func customRoleRef(role string) string {
	role = strings.ReplaceAll(strings.ReplaceAll(unquote(role), "[0]", ""), ".name", "")
	if strings.HasPrefix(role, "google_project_iam_custom_role.") || strings.HasPrefix(role, "google_organization_iam_custom_role.") {
		return role
	}
	if _, id, ok := strings.Cut(role, "/roles/"); ok {
		return id
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
	bucket := field(r.body, "bucket")
	switch {
	case runsBucketSources[bucket]:
	case otherBucketSources[bucket]:
		return // another bucket, named above: it holds no fugaro/ root
	default:
		s.failf("%s: %s is a grant on %q, a bucket this test cannot identify: if it is not the runs bucket, name it in otherBucketSources, and if it is, a write grant here may reach %s",
			r.path, r.name, bucket, MarkerObject)
		return
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
func (s *iamScan) checkProjectGrant(r tfResource, roles map[string][]string, locals map[string]string) {
	rolesOf := grantRoles(r, roles)
	if len(rolesOf) == 0 {
		s.failf("%s: %s is a project-level grant this test cannot read: give it a role, or extend this test", r.path, r.name)
		return
	}
	s.projectGrants++
	for _, role := range rolesOf {
		switch classifyRole(role, roles) {
		case roleWrite:
			// One exception, and it is read rather than assumed: Task 1.5's
			// binding (2) is a project-level grant of a role holding storage
			// writes, and the condition is the whole fence that keeps it off
			// the runs bucket (the managed prefix, which fugaro-runs-<project>
			// does not carry). A write role under any other condition, or
			// under none, is still a finding.
			if why := s.managedPrefixFence(r, locals); why != "" {
				s.failf("%s: %s grants the storage write role %s at the project level, where the runs bucket's policy does not bound it (SECURITY.md:56): %s",
					r.path, r.name, role, why)
				continue
			}
			s.fencedWrites++
		case roleUnknown:
			s.failf("%s: %s grants %q at the project level, which this test cannot resolve to what it can do: resolve it here before it ships",
				r.path, r.name, role)
		}
	}
}

// deployerConditionClauses is binding (2)'s fence, clause for clause, as
// installation/deployer.tf writes it (its local.deployer_condition_expression)
// and as internal/infra/tf builds it (DeployerConditionClauses, which
// cover_test.go pins against the module). It is pinned here too, so a widening
// of the fence is a diff in this file and not only in the package that builds
// it: nine clauses, one per service, each carrying the resource type it fences
// and the reserved managed prefix (tf.ManagedPrefix, fmgd-).
var deployerConditionClauses = []string{
	`(resource.type == "storage.googleapis.com/Bucket" && resource.name.startsWith("projects/_/buckets/fmgd-"))`,
	`(resource.type == "storage.googleapis.com/Object" && resource.name.startsWith("projects/_/buckets/fmgd-"))`,
	`(resource.type == "secretmanager.googleapis.com/Secret" && resource.name.contains("/secrets/fmgd-"))`,
	`(resource.type == "secretmanager.googleapis.com/SecretVersion" && resource.name.contains("/secrets/fmgd-"))`,
	`(resource.type == "artifactregistry.googleapis.com/Repository" && resource.name.contains("/repositories/fmgd-"))`,
	`(resource.type == "run.googleapis.com/Job" && resource.name.contains("/jobs/fmgd-"))`,
	`(resource.type == "cloudscheduler.googleapis.com/Job" && resource.name.contains("/jobs/fmgd-"))`,
	`(resource.type == "iam.googleapis.com/ServiceAccount" && resource.name.contains("/serviceAccounts/fmgd-"))`,
	`(resource.type == "apikeys.googleapis.com/Key" && resource.name.contains("/keys/fmgd-"))`,
}

// managedPrefixFence is why a project-level grant of a write role is admitted,
// or "" when it is binding (2) exactly: the condition must be a module local
// this scan can resolve, that local must be a join(" || ", [...]) of exactly
// deployerConditionClauses, every clause must carry a resource.type term (a
// clause with none applies to every type the role reaches), and every storage
// clause must name a prefix under the reserved managed prefix. A fence that
// fails any of these is a sentence saying which, and the caller reports it.
func (s *iamScan) managedPrefixFence(r tfResource, locals map[string]string) string {
	if r.typ != "google_project_iam_member" {
		return "this scan reads the fence on the member form alone, and this is a " + r.typ
	}
	expr := conditionExpr(r.body)
	if expr == "" {
		return "it carries no condition"
	}
	local, ok := strings.CutPrefix(expr, "local.")
	if !ok {
		return fmt.Sprintf("its condition is %q, not a module local this scan can resolve", expr)
	}
	resolved := strings.Join(strings.Fields(locals[local]), " ")
	if resolved == "" {
		return fmt.Sprintf("its condition is local.%s, which this scan cannot resolve", local)
	}
	clauses, ok := joinClauses(resolved)
	if !ok {
		return fmt.Sprintf("its condition is %q, not a join of clause literals", resolved)
	}
	if !slices.Equal(clauses, deployerConditionClauses) {
		return "its condition is not the deployer fence this test knows (deployerConditionClauses), clause for clause"
	}
	for _, c := range clauses {
		if !strings.Contains(c, `resource.type == "`) {
			return fmt.Sprintf("clause %q carries no resource.type term, so it applies to every type the role reaches", c)
		}
		if strings.Contains(c, "storage.googleapis.com/") && !strings.Contains(c, "projects/_/buckets/"+tf.ManagedPrefix) {
			return fmt.Sprintf("storage clause %q does not name the reserved prefix %s, so it can reach the runs bucket", c, tf.ManagedPrefix)
		}
	}
	return ""
}

// joinRE is a join(" || ", [ ... ]) expression, its list's text captured;
// clauseLitRE a Terraform string literal inside it, escapes and all.
var (
	joinRE      = regexp.MustCompile(`^join\(" \|\| ", \[(.*)\]\)$`)
	clauseLitRE = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

// joinClauses is the elements of a join(" || ", [ ... ]) of string literals,
// unquoted: the shape the module writes its fence in. ok is false for
// anything else, which is a finding wherever it is used as a condition.
func joinClauses(expr string) ([]string, bool) {
	m := joinRE.FindStringSubmatch(strings.TrimSpace(expr))
	if m == nil {
		return nil, false
	}
	var out []string
	for _, lit := range clauseLitRE.FindAllString(m[1], -1) {
		c, err := strconv.Unquote(lit)
		if err != nil {
			return nil, false
		}
		out = append(out, c)
	}
	return out, len(out) > 0
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
	// gcp.ProjectBucketCondition, set by internal/infra/projectjobs.go.
	"var.job.bucket_condition.expression": true,
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
	return []string{role}
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

// stringList is a Terraform list literal's elements, a quoted one unquoted
// and a reference as written, and whether the value was a list literal at
// all: a members list of references is as much a list as one of strings.
func stringList(body, key string) ([]string, bool) {
	v := field(body, key)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil, false
	}
	var out []string
	for _, e := range splitTop(v[1 : len(v)-1]) {
		e = strings.TrimSpace(e)
		switch {
		case e == "":
		case len(e) >= 2 && e[0] == '"' && e[len(e)-1] == '"' && !strings.Contains(e[1:len(e)-1], `"`):
			out = append(out, e[1:len(e)-1])
		default:
			out = append(out, e)
		}
	}
	return out, true
}

// splitTop splits an expression at its own commas, not those inside a
// string, a call, a list or a map.
func splitTop(s string) []string {
	var out []string
	start := 0
	walkExpr(s, 0, func(i int, depth int) bool {
		if depth == 0 && s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
		return true
	})
	return append(out, s[start:])
}

// exprEnd is where the expression starting at from ends: the first newline
// outside a string, a call, a list or a map.
func exprEnd(s string, from int) int {
	end := len(s)
	walkExpr(s, from, func(i int, depth int) bool {
		if depth <= 0 && s[i] == '\n' {
			end = i
			return false
		}
		return true
	})
	return end
}

// walkExpr calls fn on each byte of s from from that is outside any string,
// with the bracket depth there, until fn returns false.
func walkExpr(s string, from int, fn func(i, depth int) bool) {
	type frame struct {
		str   bool
		depth int
	}
	var stack []frame
	depth := 0
	for i := from; i < len(s); i++ {
		c := s[i]
		if n := len(stack); n > 0 {
			top := &stack[n-1]
			switch {
			case top.str && c == '\\':
				i++
			case top.str && c == '"':
				stack = stack[:n-1]
			case top.str && (strings.HasPrefix(s[i:], "$${") || strings.HasPrefix(s[i:], "%%{")):
				i += 2
			case top.str && (strings.HasPrefix(s[i:], "${") || strings.HasPrefix(s[i:], "%{")):
				stack = append(stack, frame{})
				i++
			case !top.str && c == '"':
				stack = append(stack, frame{str: true})
			case !top.str && c == '{':
				top.depth++
			case !top.str && c == '}':
				if top.depth == 0 {
					stack = stack[:n-1]
				} else {
					top.depth--
				}
			}
			continue
		}
		switch c {
		case '"':
			stack = append(stack, frame{str: true})
			continue
		case '(', '[', '{':
			depth++
			continue
		case ')', ']', '}':
			depth--
			continue
		}
		if !fn(i, depth) {
			return
		}
	}
}

// conditionExpr is a grant's condition expression, "" when it has none or
// when its condition is not a plain condition block this test reads (a
// dynamic one, say): no condition read is the strict reading.
func conditionExpr(body string) string {
	blocks := nestedBlocks(body, "condition")
	if len(blocks) != 1 {
		return ""
	}
	return field(blocks[0], "expression")
}

// field is one attribute of a block body at the body's own level (not a
// nested block's), its whole expression however many lines it spans; ""
// when the body does not set it.
func field(body, key string) string {
	re, ok := fieldREs[key]
	if !ok {
		re = regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=[ \t]*`)
		fieldREs[key] = re
	}
	loc := re.FindStringIndex(topLevel(body))
	if loc == nil || (loc[1] < len(body) && body[loc[1]] == '=') {
		return ""
	}
	return strings.TrimSpace(body[loc[1]:exprEnd(body, loc[1])])
}

// unquote drops the quotes of a Terraform string attribute.
func unquote(s string) string { return strings.Trim(s, `"`) }

// fieldREs are field's patterns, one per attribute name. The tests that use
// them do not run in parallel.
var fieldREs = map[string]*regexp.Regexp{}

// Budget.Check is the one rule the write and the read share: an amount in
// [MinBudgetAmount, MaxBudgetAmount] whole units of its currency, and a
// currency of exactly three ASCII uppercase letters (the ISO 4217 code
// shape). The rows on each side of each bound are the ones a mutation of the
// bound moves; the probes are the values a review read back as a valid
// budget before the rule existed.
func TestBudgetCheck(t *testing.T) {
	for _, b := range []Budget{
		{Amount: MinBudgetAmount, Currency: "USD"},
		{Amount: MaxBudgetAmount, Currency: "EUR"},
		{Amount: 50, Currency: "JPY"},
		{Amount: 7, Currency: "AZZ"},
	} {
		if err := b.Check(); err != nil {
			t.Errorf("%+v.Check() = %v, want nil", b, err)
		}
	}
	for name, b := range map[string]Budget{
		"an amount of 0":         {Amount: 0, Currency: "USD"},
		"a negative amount":      {Amount: -1, Currency: "USD"},
		"one above the ceiling":  {Amount: MaxBudgetAmount + 1, Currency: "USD"},
		"the largest int64":      {Amount: 9223372036854775807, Currency: "USD"},
		"no currency":            {Amount: 7},
		"a lower-case currency":  {Amount: 7, Currency: "usd"},
		"a mixed-case currency":  {Amount: 7, Currency: "Usd"},
		"two letters":            {Amount: 7, Currency: "US"},
		"four letters":           {Amount: 7, Currency: "USDX"},
		"the probe XXXXXXXX\\n":  {Amount: 7, Currency: "XXXXXXXX\n"},
		"a newline inside":       {Amount: 7, Currency: "U\nS"},
		"a trailing newline":     {Amount: 7, Currency: "US\n"},
		"a digit":                {Amount: 7, Currency: "US1"},
		"a letter outside ASCII": {Amount: 7, Currency: "\u00dcS"},
		"a byte just before A":   {Amount: 7, Currency: "@SD"},
		"a byte just after Z":    {Amount: 7, Currency: "US["},
		"a space":                {Amount: 7, Currency: "US "},
	} {
		if err := b.Check(); err == nil {
			t.Errorf("%s: %+v.Check() = nil, want a refusal", name, b)
		}
	}
}

// --- WriteFlag: the owner's statement about an existing project (D58, the
// plan's Task 6.1) ---
//
// The flag is the second writer of MarkerObject and the only one that does
// not need a creation: the owner names a project Fugaro did not create, and
// the object it writes is the version that carries a budget, because a
// marked project whose marker records no amount is refused by the apply-time
// budget read (Task 4.4) — a flag that wrote version 1 would lower a guard
// and buy nothing.

// TestWriteFlagRoundTrip is the write half of the command: the object is
// MarkerObject, it is the version that carries the amount the owner chose,
// and a project that already records an amount is refused rather than
// rewritten — this command adds a budget, it does not edit one.
func TestWriteFlagRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	replaced, err := WriteFlag(ctx, s, testBucket, testProject, testProjectNumber, Budget{Amount: 7, Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if replaced {
		t.Error("the flag reported a replacement with nothing there to replace")
	}
	b, ok := readMarker(t, s, testBucket)
	if !ok || string(b) != markerBudgetJSON {
		t.Fatalf("gs://%s/%s = %q (present %v), want %q", testBucket, MarkerObject, b, ok, markerBudgetJSON)
	}
	// Once a budget is recorded: a project that already records one is not
	// flagged again — the amount is the owner's decision at the time it was
	// taken, and this command is not an editor of it — and the second call
	// says so rather than overwriting.
	replaced, err = WriteFlag(ctx, s, testBucket, testProject, testProjectNumber, Budget{Amount: 9, Currency: "EUR"})
	if err == nil || !errors.Is(err, ErrAlreadyMarked) {
		t.Fatalf("second flag: %v, want ErrAlreadyMarked", err)
	}
	if replaced {
		t.Error("the refused second call reported a replacement")
	}
	if b, _ := readMarker(t, s, testBucket); string(b) != markerBudgetJSON {
		t.Errorf("the second call changed the object to %q", b)
	}
}

// The flag's other half, and the reason it is not "create only": a project
// Fugaro created WITHOUT --managed records the version-1 creation fact and no
// amount (init passes no budget to WriteMarker), and the apply-time budget
// read refuses that marker — this release's own gate refusal names this
// command as the way such a project gets an amount
// (internal/managed/budget.go). So the flag REPLACES exactly that object,
// and exactly that one: the version-1 fact of this project. Anything else
// that is there is refused rather than replaced.
func TestWriteFlagReplacesTheCreationFactThatRecordsNoAmount(t *testing.T) {
	ctx := context.Background()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))
	replaced, err := WriteFlag(ctx, s, testBucket, testProject, testProjectNumber, Budget{Amount: 7, Currency: "USD"})
	if err != nil {
		t.Fatalf("flagging a version-1 project: %v", err)
	}
	if !replaced {
		t.Error("the flag replaced a version-1 fact and did not say so")
	}
	b, ok := readMarker(t, s, testBucket)
	if !ok {
		t.Fatalf("no %s in gs://%s after the flag", MarkerObject, testBucket)
	}
	if string(b) != markerBudgetJSON {
		t.Errorf("after the flag gs://%s/%s = %s, want %s", testBucket, MarkerObject, b, markerBudgetJSON)
	}
}

// What the flag does NOT replace, one row each: the objects a marker may be
// and are not this project's version-1 fact. None of them is touched, and
// each is refused with ErrAlreadyMarked — the object is there, and the write
// that refused to overwrite it is the answer.
func TestWriteFlagReplacesNothingButTheCreationFactOfItsOwnProject(t *testing.T) {
	ctx := context.Background()
	for name, existing := range map[string]string{
		"a marker that already records an amount":         markerBudgetJSON,
		"a version-1 marker about another project":        `{"version":1,"gcp_project":"another-proj"}`,
		"a version this build does not know":              `{"version":3,"gcp_project":"proj-1234"}`,
		"an object that is not JSON at all":               "not json",
		"two JSON values":                                 `{"version":1,"gcp_project":"proj-1234"}{"version":1,"gcp_project":"proj-1234"}`,
		"a key this build does not write":                 `{"version":1,"gcp_project":"proj-1234","budget":{"amount":7,"currency":"USD"}}`,
		"a version-1 marker whose project is not spelled": `{"version":1,"gcp_project":"PROJ-1234"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			f.Put(testBucket, MarkerObject, []byte(existing))
			replaced, err := WriteFlag(ctx, s, testBucket, testProject, testProjectNumber, Budget{Amount: 7, Currency: "USD"})
			if err == nil || !errors.Is(err, ErrAlreadyMarked) {
				t.Fatalf("flagging over %s: %v, want ErrAlreadyMarked", existing, err)
			}
			if replaced {
				t.Error("the refused call reported a replacement")
			}
			if b, _ := readMarker(t, s, testBucket); string(b) != existing {
				t.Errorf("a refused flag changed the object: %q → %q", existing, b)
			}
		})
	}
}

// The write and the read of the same object, together: what the flag writes
// is read back through the path the apply uses — IsManaged for "this project
// is managed" and ReadBudget for the amount that opens the gate (Task 4.4) —
// for a project with no marker at all and for one whose version-1 fact the
// flag replaced. The two tests above assert the bytes; this one asserts that
// the bytes are a fact this build believes, which is what the command is for.
func TestWriteFlagWritesSomethingTheManagedReadBelieves(t *testing.T) {
	ctx := context.Background()
	for name, existing := range map[string]string{
		"no marker at all":            "",
		"the version-1 creation fact": markerJSON,
	} {
		t.Run(name, func(t *testing.T) {
			s, f, c, _ := managedWorld(t)
			if existing != "" {
				f.Put(testBucket, MarkerObject, []byte(existing))
			}
			if _, err := WriteFlag(ctx, s, testBucket, testProject, testProjectNumber, Budget{Amount: 7, Currency: "USD"}); err != nil {
				t.Fatal(err)
			}
			ok, reason, err := IsManaged(ctx, s, c, testFugaroProject, testProject)
			if err != nil || !ok || reason != "" {
				t.Fatalf("IsManaged after the flag = %v, %q, %v; want true, \"\", nil", ok, reason, err)
			}
			budget, ok, reason, err := ReadBudget(ctx, s, c, testFugaroProject, testProject)
			if err != nil || !ok || reason != "" {
				t.Fatalf("ReadBudget after the flag = %+v, %v, %q, %v; want 7 USD, true, \"\", nil", budget, ok, reason, err)
			}
			if budget.Amount != 7 || budget.Currency != "USD" {
				t.Errorf("ReadBudget after the flag = %+v, want the amount the flag recorded (7 USD)", budget)
			}
		})
	}
}

// TestWriteFlagRefusesABudgetThatIsNotOne: the amount is required (D64, no
// silent default, and Task 4.4's read refuses a marker that records none),
// and the pair is the marker's own rule — the same Check the write of a
// managed creation applies and the read believes. A refused pair writes
// nothing at all.
func TestWriteFlagRefusesABudgetThatIsNotOne(t *testing.T) {
	ctx := context.Background()
	for name, b := range map[string]Budget{
		"no amount":                 {Currency: "USD"},
		"a zero amount":             {Currency: "USD"},
		"a negative amount":         {Amount: -1, Currency: "USD"},
		"no currency":               {Amount: 7},
		"one above the ceiling":     {Amount: MaxBudgetAmount + 1, Currency: "USD"},
		"a lower-case currency":     {Amount: 7, Currency: "usd"},
		"a four-letter currency":    {Amount: 7, Currency: "USDX"},
		"a currency with a newline": {Amount: 7, Currency: "US\n"},
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			if _, err := WriteFlag(ctx, s, testBucket, testProject, testProjectNumber, b); err == nil {
				t.Fatal("want a refusal: the amount and the currency are a pair, above 0 and in range")
			}
			// Before the read below makes a request of its own: a flag with
			// a budget that is not one is refused before the cloud is
			// touched at all — it does not even read the bucket it would
			// have written into.
			if reqs := f.Requests(); len(reqs) != 0 {
				t.Errorf("a flag with a budget that is not one touched the API: %v", reqs)
			}
			if names := objects(t, s, testBucket); len(names) != 0 {
				t.Errorf("a refused flag wrote %v", names)
			}
		})
	}
}

// TestWriteFlagNeedsTheProjectsOwnRunsBucket is WriteMarker's proof, on the
// flag's own writer: the object may only be written into a bucket that is
// the project's own, by number and by the installation's label. A bucket
// name is global, so a mark written into a stranger's bucket would read as a
// fact about this project (internal/infra/project.go's rule, and the read's).
func TestWriteFlagNeedsTheProjectsOwnRunsBucket(t *testing.T) {
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
			if _, err := WriteFlag(ctx, s, tc.bucket, testProject, tc.number, Budget{Amount: 7, Currency: "USD"}); err == nil {
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
	if _, err := WriteFlag(ctx, s, testBucket, testProject, testProjectNumber, Budget{Amount: 7, Currency: "USD"}); err != nil {
		t.Fatalf("the project's own runs bucket was refused: %v", err)
	}
	// A call with no storage client is a refusal rather than a panic.
	if _, err := WriteFlag(ctx, nil, testBucket, testProject, testProjectNumber, Budget{Amount: 7, Currency: "USD"}); err == nil {
		t.Error("the flag was written with no storage client")
	}
	// A call naming no project has nothing to write a fact about.
	if _, err := WriteFlag(ctx, s, testBucket, "", testProjectNumber, Budget{Amount: 7, Currency: "USD"}); err == nil {
		t.Error("the flag was written with no project named")
	}
}
