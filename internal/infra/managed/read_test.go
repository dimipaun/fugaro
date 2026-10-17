package managed

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// The Fugaro project the reads below are asked about. It is deliberately not
// a substring of the bucket's name and not the GCP project's, so a reason
// that names the project is saying something rather than matching
// fugaro-runs-<gcp project> by accident.
const testFugaroProject = "sandbox"

// newCRM is a Resource Manager fake answering the one call this package's
// read makes: the project's own number.
func newCRM(t *testing.T) (*crm.Service, *gcpfake.CRM) {
	t.Helper()
	f := gcpfake.NewCRM(t)
	s, err := crm.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

// managedWorld is what a managed installation looks like to this read: the
// runs bucket, numbered as the GCP project is numbered and labelled as the
// installation labels it, and the Resource Manager record that says that
// number is that project's own. The object itself is put by the caller,
// because whether it is there at all is one of the mismatches.
func managedWorld(t *testing.T) (*storage.Service, *gcpfake.GCS, *crm.Service, *gcpfake.CRM) {
	t.Helper()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	c, cf := newCRM(t)
	cf.AddProject(testProject, testProjectNumber)
	return s, f, c, cf
}

// The happy path: the fact is there, the bucket it is in is the project's
// own, and it names the project asked about. The read is a read — it writes
// nothing and asks for nothing but the project's number, the bucket and the
// object.
func TestIsManagedReadsTheCreationFact(t *testing.T) {
	ctx := context.Background()
	s, f, c, cf := managedWorld(t)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))

	ok, reason, err := IsManaged(ctx, s, c, testFugaroProject, testProject)
	if err != nil || !ok || reason != "" {
		t.Fatalf("IsManaged = %v, %q, %v; want true, \"\", nil", ok, reason, err)
	}
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet {
			t.Errorf("the read made a %s %s: a marker read writes nothing", r.Method, r.Path)
		}
	}
	for _, r := range cf.Requests() {
		if r.Method != http.MethodGet {
			t.Errorf("the read made a %s %s: a marker read writes nothing", r.Method, r.Path)
		}
	}
	// The bucket is located from the GCP project alone, under the name the
	// installation module gives it and the name init's marker write uses
	// (init.go:672, which warns rather than writes anywhere else for this
	// reason).
	if got := runsBucketName(testProject); got != testBucket {
		t.Errorf("runsBucketName(%q) = %q, want %q", testProject, got, testBucket)
	}
}

// Every way of not being managed, each answered with false and a one-line
// reason a person can act on. The table is the whole failure surface the
// task names — no bucket, a bucket in another project, an unlabelled
// bucket, an absent object, an unreadable object, one naming another
// project, one of a version this build does not know — plus the ones the
// fail-closed rule adds: a project whose number cannot be read, an object
// this build cannot parse, and an object that parses but is not the whole
// document. A row whose read fails outright (a 5xx, which is not an answer)
// is marked, and is still not managed.
func TestIsManagedRefusesEveryMismatch(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		// world is the installation as this row has it, the object
		// included: the mismatch is whatever the row leaves out or changes.
		world func(t *testing.T) (*storage.Service, *crm.Service)
		// reason is what the refusal must say, a substring: a refusal a
		// person cannot act on is the same as no refusal.
		reason string
		// fails is whether the answer is a failure rather than an answer
		// (an API that did not answer, not an API that said no).
		fails bool
	}{
		"no runs bucket": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, _ := newStorage(t)
				c, cf := newCRM(t)
				cf.AddProject(testProject, testProjectNumber)
				return s, c
			},
			reason: "is not there",
		},
		"a bucket in another project": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, 999, testManaged)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, cf := newCRM(t)
				cf.AddProject(testProject, testProjectNumber)
				return s, c
			},
			reason: "belongs to project number 999",
		},
		"a bucket with no label": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, testProjectNumber, nil)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, cf := newCRM(t)
				cf.AddProject(testProject, testProjectNumber)
				return s, c
			},
			reason: "fugaro=managed",
		},
		"a bucket labelled by someone else": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, testProjectNumber, map[string]string{"fugaro": "managed-by-someone"})
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, cf := newCRM(t)
				cf.AddProject(testProject, testProjectNumber)
				return s, c
			},
			reason: "fugaro=managed",
		},
		"no object at all": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, _, c, _ := managedWorld(t)
				return s, c
			},
			reason: MarkerObject,
		},
		"an object that cannot be read": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				f.RefusePath(MarkerObject, http.StatusForbidden, "PERMISSION_DENIED", "", "no")
				return s, c
			},
			reason: MarkerObject,
		},
		"an object that is not there when read": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				f.FailObjectGetsAfter(testBucket, MarkerObject, 0)
				return s, c
			},
			reason: MarkerObject,
		},
		"an empty object": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, nil)
				return s, c
			},
			reason: "not a marker",
		},
		"an object that is not JSON": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte("this is not the marker"))
				return s, c
			},
			reason: "not a marker",
		},
		"an object bigger than any marker": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":1,"gcp_project":"`+testProject+`","pad":"`+strings.Repeat("a", 5000)+`"}`))
				return s, c
			},
			reason: "bytes",
		},
		"an object naming another project": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":1,"gcp_project":"proj-9999"}`))
				return s, c
			},
			reason: "names project proj-9999",
		},
		"a version this build does not know": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":`+strconv.Itoa(MarkerVersion+1)+`,"gcp_project":"`+testProject+`"}`))
				return s, c
			},
			reason: "version",
		},
		"a field this build does not know": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":1,"gcp_project":"`+testProject+`","budget_amount":50}`))
				return s, c
			},
			reason: "not a marker",
		},
		"an object with a second value after it": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON+`{"version":1,"gcp_project":"proj-9999"}`))
				return s, c
			},
			reason: "not a marker",
		},
		"a project whose number is denied": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, cf := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				cf.DenyGet(testProject)
				return s, c
			},
			reason: "cannot be read",
		},
		"a project that is not there": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, testProjectNumber, testManaged)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, _ := newCRM(t)
				return s, c
			},
			reason: "cannot be read",
		},
		"a bucket read that fails": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				f.Refuse(http.StatusInternalServerError, "INTERNAL", "", "backend error")
				return s, c
			},
			reason: "reading gs://" + testBucket,
			fails:  true,
		},
		"a project read that fails": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, cf := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				cf.Refuse(http.StatusInternalServerError, "INTERNAL", "", "backend error")
				return s, c
			},
			reason: "reading project " + testProject,
			fails:  true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, c := tc.world(t)
			ok, reason, err := IsManaged(ctx, s, c, testFugaroProject, testProject)
			if ok {
				t.Fatalf("IsManaged = true: a marker that reads as managed with %s turns the confirmations off", name)
			}
			if reason == "" {
				t.Fatal("no reason: a refusal a person cannot act on is the same as no refusal")
			}
			if strings.Contains(reason, "\n") {
				t.Errorf("the reason is not one line: %q", reason)
			}
			if !strings.Contains(reason, tc.reason) {
				t.Errorf("reason = %q, want it to say %q", reason, tc.reason)
			}
			if !strings.Contains(reason, testFugaroProject) && !strings.Contains(reason, testProject) {
				t.Errorf("reason = %q: it names neither the project nor the GCP project it is about", reason)
			}
			if tc.fails && err == nil {
				t.Error("want an error: the API did not answer, so the caller should be told the read failed")
			}
			if !tc.fails && err != nil {
				t.Errorf("err = %v, want the refusal to be an answer rather than a failure", err)
			}
		})
	}
}

// The error is never the signal. Three results and not two, so the
// difference is in the type rather than in a convention: a caller that
// treats a nil error as "managed" is wrong, because "there is no marker" is
// also a nil error; a caller that treats an error as "proceed" is wrong,
// because a failed read answers false. The assignment below is the
// compile-time half — it stops building if the signature ever loses a
// result — and the cases are the other half: managed is true only with a
// nil error, and false with one, and a reason either way.
func TestIsManagedNeverTreatsAnErrorAsManaged(t *testing.T) {
	ctx := context.Background()
	var read func(context.Context, *storage.Service, *crm.Service, string, string) (bool, string, error) = IsManaged

	s, f, c, _ := managedWorld(t)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))
	if ok, reason, err := read(ctx, s, c, testFugaroProject, testProject); err != nil || !ok || reason != "" {
		t.Fatalf("managed: IsManaged = %v, %q, %v; want true, \"\", nil", ok, reason, err)
	}

	// A nil error and no marker: the case a caller checking only the error
	// gets wrong, and the reason the bool is the signal.
	s2, _, c2, _ := managedWorld(t)
	ok, reason, err := read(ctx, s2, c2, testFugaroProject, testProject)
	if err != nil {
		t.Fatalf("no marker: err = %v, want the refusal to be an answer", err)
	}
	if ok {
		t.Error("no marker: IsManaged = true, and a nil error is not a fact")
	}
	if reason == "" {
		t.Error("no marker: no reason")
	}

	// An error and no answer: still not managed, and still something to
	// print, so neither result of the pair can be dropped.
	s3, f3, c3, _ := managedWorld(t)
	f3.Put(testBucket, MarkerObject, []byte(markerJSON))
	f3.Refuse(http.StatusInternalServerError, "INTERNAL", "", "backend error")
	ok, reason, err = read(ctx, s3, c3, testFugaroProject, testProject)
	if err == nil {
		t.Error("failed read: err = nil, want the failure reported")
	}
	if ok {
		t.Error("failed read: IsManaged = true, and a failed read is not a fact")
	}
	if reason == "" {
		t.Error("failed read: no reason, so a caller that prints the reason says nothing")
	}

	// Nothing to read at all: a caller's own mistake, answered with an
	// error rather than a guess about a project nobody named.
	for _, tc := range []struct {
		name       string
		s          *storage.Service
		c          *crm.Service
		project    string
		gcpProject string
	}{
		{name: "no storage client", c: c, project: testFugaroProject, gcpProject: testProject},
		{name: "no crm client", s: s, project: testFugaroProject, gcpProject: testProject},
		{name: "no fugaro project", s: s, c: c, gcpProject: testProject},
		{name: "no GCP project", s: s, c: c, project: testFugaroProject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason, err := read(ctx, tc.s, tc.c, tc.project, tc.gcpProject)
			if ok {
				t.Fatal("IsManaged = true with nothing to read")
			}
			if err == nil {
				t.Error("want an error: the read could not be made at all")
			}
			if reason == "" {
				t.Error("no reason")
			}
		})
	}
}
