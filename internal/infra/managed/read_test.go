package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
// installation labels it, the Resource Manager record that says that number
// is that project's own, and — since Task 3.0 — the project's own
// fugaro_managed label, which the run that created it writes beside the
// object. The object itself is put by the caller, because whether it is there
// at all is one of the mismatches.
//
// Every row that means to be about something *else* starts here, which is the
// point: a world that is managed in every other way is the only one in which
// the reason a test reads is the reason it arranged.
func managedWorld(t *testing.T) (*storage.Service, *gcpfake.GCS, *crm.Service, *gcpfake.CRM) {
	t.Helper()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	c, cf := newCRM(t)
	cf.AddProject(testProject, testProjectNumber)
	cf.SetLabels(testProject, map[string]string{ProjectLabelKey: ProjectLabelValue})
	return s, f, c, cf
}

// markedWorld is managedWorld with the object in it: the fact as the creating
// run leaves it, both halves written. It is what a row that has to take one
// half away starts from.
func markedWorld(t *testing.T) (*storage.Service, *crm.Service) {
	t.Helper()
	s, f, c, _ := managedWorld(t)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))
	return s, c
}

// labelledCRM is the Resource Manager side of a managed installation on its
// own, for the rows that build their storage by hand (a bucket in another
// project, a bucket without the label, a transport that dies mid-read). The
// project carries the label unless the row says otherwise, because a row that
// means to be about the storage side has to get past the project's own half
// of the fact to reach it.
func labelledCRM(t *testing.T, labels map[string]string) (*crm.Service, *gcpfake.CRM) {
	t.Helper()
	c, cf := newCRM(t)
	cf.AddProject(testProject, testProjectNumber)
	if labels == nil {
		labels = map[string]string{ProjectLabelKey: ProjectLabelValue}
	}
	cf.SetLabels(testProject, labels)
	return c, cf
}

// roundTripFunc answers the calls this package makes without a server behind
// them, which is how the two failures no fake serves are arranged: a body
// that dies mid-read, and a request the context ends.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// storageWithTransport is a storage service whose every call is answered by
// transport.
func storageWithTransport(t *testing.T, transport roundTripFunc) *storage.Service {
	t.Helper()
	s, err := storage.NewService(context.Background(), option.WithoutAuthentication(),
		option.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// bucketResponse is the bucket read answered as the JSON API answers it — the
// number is a string there — so that a row can fail the object read alone.
func bucketResponse(req *http.Request, number uint64, labels map[string]string) *http.Response {
	body, err := json.Marshal(map[string]any{"projectNumber": strconv.FormatUint(number, 10), "labels": labels})
	if err != nil {
		panic(err)
	}
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK",
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(bytes.NewReader(body)), Request: req}
}

// shortBody serves data and then fails, as a connection that dies
// mid-download does: the request itself succeeded, so the failure lands where
// the caller reads the body (read.go's io.ReadAll) and not at the request.
type shortBody struct {
	data []byte
	err  error
}

func (b *shortBody) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, b.err
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *shortBody) Close() error { return nil }

// cancelledCtx is a context that is already done, as a caller's own timeout
// leaves one.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
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
	// (initEngine.markCreated in internal/cli/init.go, which warns rather
	// than writes anywhere else for this reason).
	if got := runsBucketName(testProject); got != testBucket {
		t.Errorf("runsBucketName(%q) = %q, want %q", testProject, got, testBucket)
	}
	// One Resource Manager call and one only (Task 3.0). The project's label
	// is a field of the project object this read already fetches for its
	// number check, so the second half of the fact costs no new call and no
	// new permission: a second call here would be a second permission the
	// reading identity needs at run time, and a managed apply runs as the
	// deployer, whose grants are pinned whole for exactly this reason.
	if n := len(cf.Requests()); n != 1 {
		t.Errorf("the read made %d Resource Manager calls, want 1 — the number and the labels come from the one project get: %v", n, cf.Requests())
	}
}

// The label can only narrow, never widen (Task 3.0; docs/plans/2026-10-18-
// managed-projects-task-3.0-decisions.md §4). A project that carries
// fugaro_managed and has no creation fact is not managed, and the reason is
// about the fact: a label is a thing anything holding
// resourcemanager.projects.update can put on a project, which is a
// permission the owner's own account has and no credential a run holds may
// (design finding 14), so a label believed on its own would be the boundary
// written by whoever holds that permission rather than by the run that
// created the project. The other direction — the label taken away, with the
// fact left in place — is
// TestIsManagedRefusesEveryMismatch/"a project with no fugaro_managed label".
func TestTheProjectLabelAloneIsNotManaged(t *testing.T) {
	ctx := context.Background()

	// The label, the project, the runs bucket, everything but the fact.
	s, _, c, _ := managedWorld(t)
	ok, reason, err := IsManaged(ctx, s, c, testFugaroProject, testProject)
	if err != nil || ok {
		t.Fatalf("IsManaged = %v, %q, %v; want false, a reason, nil", ok, reason, err)
	}
	if !strings.Contains(reason, MarkerObject) {
		t.Errorf("reason = %q, want it to be about the creation fact that is not there", reason)
	}

	// And a value that is not the one this package writes is not the label,
	// however close it is: label values are case-sensitive, so "TRUE" is
	// another value and not the mark — with the fact there and perfect, so
	// the label is the only thing that can refuse this project.
	s, f, c, cf := managedWorld(t)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))
	cf.SetLabels(testProject, map[string]string{ProjectLabelKey: "TRUE"})
	ok, reason, err = IsManaged(ctx, s, c, testFugaroProject, testProject)
	if err != nil || ok {
		t.Fatalf("IsManaged = %v, %q, %v; want false, a reason, nil", ok, reason, err)
	}
	if !strings.Contains(reason, ProjectLabelKey) {
		t.Errorf("reason = %q, want it to be about the %s label", reason, ProjectLabelKey)
	}
}

// The version a managed creation writes (Task 4.2): the fact and the monthly
// amount it was required to choose, read as managed, with the amount and the
// currency readable from the same object — and still a read that writes
// nothing and asks for nothing but the project's number, the bucket and the
// object.
func TestIsManagedReadsTheBudgetVersion(t *testing.T) {
	ctx := context.Background()
	s, f, c, cf := managedWorld(t)
	f.Put(testBucket, MarkerObject, []byte(markerBudgetJSON))

	ok, reason, err := IsManaged(ctx, s, c, testFugaroProject, testProject)
	if err != nil || !ok || reason != "" {
		t.Fatalf("IsManaged = %v, %q, %v; want true, \"\", nil", ok, reason, err)
	}
	budget, ok, reason, err := ReadBudget(ctx, s, c, testFugaroProject, testProject)
	if err != nil || !ok || reason != "" {
		t.Fatalf("ReadBudget = %+v, %v, %q, %v; want 7 USD, true, \"\", nil", budget, ok, reason, err)
	}
	if budget.Amount != 7 || budget.Currency != "USD" {
		t.Errorf("ReadBudget = %+v, want the amount and the currency the fact records (7 USD)", budget)
	}
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet {
			t.Errorf("the budget read made a %s %s: a marker read writes nothing", r.Method, r.Path)
		}
	}
	for _, r := range cf.Requests() {
		if r.Method != http.MethodGet {
			t.Errorf("the budget read made a %s %s: a marker read writes nothing", r.Method, r.Path)
		}
	}
}

// ReadBudget's other answers, which are the reason it exists as a read of its
// own rather than a field IsManaged happens to return: an amount a caller can
// act on, or a refusal a caller can print. A managed project under version 1
// records none — it was created before this task, or it was not a managed
// creation — and that is ok=false with a reason and **no error**: it is an
// answer, and the fail-closed one for a caller that needs a line to compare a
// spend against, because an absent amount is not an unlimited one. A read
// that could not be made at all (an API that did not answer) is the error,
// exactly as IsManaged's is.
func TestReadBudgetAnswersWhatTheFactRecords(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		world  func(t *testing.T) (*storage.Service, *crm.Service)
		fails  bool
		reason string
	}{
		"the fact is version 1, which records no amount": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				return s, c
			},
			reason: "records no budget amount",
		},
		"the project is not managed at all": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, _, c, _ := managedWorld(t)
				return s, c
			},
			reason: MarkerObject,
		},
		"the project's number cannot be read": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, cf := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerBudgetJSON))
				cf.DenyGet(testProject)
				return s, c
			},
			reason: "cannot be read",
		},
		"the project's number read fails": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, cf := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerBudgetJSON))
				cf.Refuse(http.StatusInternalServerError, "INTERNAL", "", "backend error")
				return s, c
			},
			reason: "reading project " + testProject,
			fails:  true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, c := tc.world(t)
			budget, ok, reason, err := ReadBudget(ctx, s, c, testFugaroProject, testProject)
			if ok {
				t.Fatalf("ReadBudget = %+v, true: an amount came back from %s", budget, name)
			}
			if reason == "" || strings.Contains(reason, "\n") {
				t.Errorf("reason = %q, want one line a caller can print", reason)
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

// Every way of not being managed, each answered with false and a one-line
// reason a person can act on. The table is the whole failure surface the
// task names — no bucket, a bucket in another project, an unlabelled
// bucket, an absent object, an unreadable object, one naming another
// project, one of a version this build does not know — plus the ones the
// fail-closed rule adds: a project whose number cannot be read or is zero, an
// object this build cannot parse (an unknown field, a key spelled
// differently, a key that appears twice, a second value after it), and the
// object read itself failing every way it can — a 403, a 404, a 5xx, a body
// that dies mid-read, a request the context ends — because a fail-open
// mutation of that one branch is the mutation a table that only ever failed
// the bucket read would not catch. A row whose read fails outright (a 5xx, or
// a dead context: not an answer) is marked, and is still not managed.
func TestIsManagedRefusesEveryMismatch(t *testing.T) {
	for name, tc := range map[string]struct {
		// world is the installation as this row has it, the object
		// included: the mismatch is whatever the row leaves out or changes.
		world func(t *testing.T) (*storage.Service, *crm.Service)
		// ctx is the context the row's read is made with; nil is a live
		// one.
		ctx context.Context
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
				c, _ := labelledCRM(t, nil)
				return s, c
			},
			reason: "is not there",
		},
		// The state every project marked before Task 3.0 is in: the fact is
		// there and perfect, and nothing has ever written the label. It is
		// the regression the change carries (decisions §5) and it is the
		// fail-closed direction, so it is a row rather than a note: the
		// owner-run backfill of docs/design/managed-projects.md is what turns it back into
		// a managed project, and this is the reason a person reads first.
		"a project with no fugaro_managed label": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, testProjectNumber, testManaged)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				// Other labels of its own, so the row is about this key and
				// not about a project that carries no labels at all.
				c, _ := labelledCRM(t, map[string]string{"environment": "prod"})
				return s, c
			},
			// The condition, whole: both marks, this one missing, and the
			// guidance left to docs/design/managed-projects.md — no command is named here
			// (decisions §5, and TestTheNoLabelReasonNamesNoCommand pins
			// that half). It attributes the label to neither writer alone,
			// which is what the round-2 review found in the sentence this
			// replaced.
			reason: "carries no fugaro_managed label, so " + testFugaroProject + " is not managed: a managed project carries both marks, the creation fact in its runs bucket and the label on the project itself, and a project that has the fact without the label is marked again by hand, on the account that owns it (docs/design/managed-projects.md)",
		},
		// The label is missing AND the object read fails: the label alone
		// decides this project (read.go's last return), and the read's error
		// is dropped rather than returned — a caller failing a command for a
		// project the label has already refused would be reporting the wrong
		// thing. So this row's verdict is the one above, and `fails` is
		// false: the API did not answer and the answer is still the
		// label's.
		"no label, and the object read fails too": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, testProjectNumber, testManaged)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				f.RefusePath(MarkerObject, http.StatusInternalServerError, "INTERNAL", "", "backend error")
				c, _ := labelledCRM(t, map[string]string{"environment": "prod"})
				return s, c
			},
			reason: "carries no fugaro_managed label, so " + testFugaroProject + " is not managed: a managed project carries both marks, the creation fact in its runs bucket and the label on the project itself, and a project that has the fact without the label is marked again by hand, on the account that owns it (docs/design/managed-projects.md)",
		},
		"a project whose fugaro_managed label says something else": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, testProjectNumber, testManaged)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, _ := labelledCRM(t, map[string]string{ProjectLabelKey: "false"})
				return s, c
			},
			reason: "carries fugaro_managed=false, not true",
		},
		"a bucket in another project": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, 999, testManaged)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, _ := labelledCRM(t, nil)
				return s, c
			},
			reason: "belongs to project number 999",
		},
		"a bucket with no label": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, testProjectNumber, nil)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, _ := labelledCRM(t, nil)
				return s, c
			},
			reason: "fugaro=managed",
		},
		"a bucket labelled by someone else": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, testProjectNumber, map[string]string{"fugaro": "managed-by-someone"})
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, _ := labelledCRM(t, nil)
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
		"an object the read is not allowed (403)": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				f.RefusePath(MarkerObject, http.StatusForbidden, "PERMISSION_DENIED", "", "no")
				return s, c
			},
			reason: MarkerObject,
		},
		"an object the read is told is not there (404)": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				f.RefusePath(MarkerObject, http.StatusNotFound, "NOT_FOUND", "", "no such object")
				return s, c
			},
			reason: MarkerObject,
		},
		"the object read fails (500)": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				f.RefusePath(MarkerObject, http.StatusInternalServerError, "INTERNAL", "", "backend error")
				return s, c
			},
			reason: "reading gs://" + testBucket + "/" + MarkerObject,
			fails:  true,
		},
		// The API's own error text is untrusted like any other text this
		// package prints, and it is the one place a control character really
		// arrives (a 5xx body, or a proxy's): this row's message carries a
		// newline and an escape sequence, so the assertion below is about
		// safetext.Field (read.go's one boundary for every return) rather
		// than about the fixtures holding no control character — deleting
		// that boundary fails here and nowhere else.
		"the object read fails with a body that is not one line": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				f.RefusePath(MarkerObject, http.StatusInternalServerError, "INTERNAL", "", "backend error\n\x1b[31mred")
				return s, c
			},
			reason: "reading gs://" + testBucket + "/" + MarkerObject,
			fails:  true,
		},
		"the object's body dies mid-read": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				c, _ := labelledCRM(t, nil)
				s := storageWithTransport(t, func(r *http.Request) (*http.Response, error) {
					if strings.Contains(r.URL.Path, "/o/") {
						return &http.Response{StatusCode: http.StatusOK, Status: "200 OK",
							Header: http.Header{"Content-Type": []string{"application/json"}},
							Body:   &shortBody{data: []byte(markerJSON), err: io.ErrUnexpectedEOF}, Request: r}, nil
					}
					return bucketResponse(r, testProjectNumber, testManaged), nil
				})
				return s, c
			},
			reason: "reading gs://" + testBucket + "/" + MarkerObject,
			fails:  true,
		},
		"a context that is already done": {
			ctx: cancelledCtx(),
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				return s, c
			},
			reason: "reading project " + testProject,
			fails:  true,
		},
		"a context that ends while the object is read": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				c, _ := labelledCRM(t, nil)
				s := storageWithTransport(t, func(r *http.Request) (*http.Response, error) {
					if strings.Contains(r.URL.Path, "/o/") {
						return nil, context.Canceled
					}
					return bucketResponse(r, testProjectNumber, testManaged), nil
				})
				return s, c
			},
			reason: "reading gs://" + testBucket + "/" + MarkerObject,
			fails:  true,
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
				f.Put(testBucket, MarkerObject, []byte(`{"version":`+strconv.Itoa(MarkerVersionBudget+1)+`,"gcp_project":"`+testProject+`"}`))
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
		// The budget version (Task 4.2). Version 2 means "this creation was
		// required to choose an amount and did", so an object of that version
		// without one — or with an amount that is not one — is not a fact
		// this build wrote, and believing it would be a managed project with
		// no line to compare a spend against. Version 2 read as a version-1
		// object, or the amount taken from a field someone else chose, is the
		// mutation these rows exist for.
		"version 2 with no budget recorded": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`"}`))
				return s, c
			},
			reason: "records no budget amount",
		},
		"version 2 with an amount of 0": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"amount":0,"currency":"USD"}}`))
				return s, c
			},
			reason: "records no budget amount",
		},
		"version 2 with a budget and no currency": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"amount":50}}`))
				return s, c
			},
			reason: "records no budget amount",
		},
		// The amount is a monthly line above 0, so a negative one is no more
		// an amount than 0 is.
		"version 2 with a lower-case currency": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"amount":7,"currency":"usd"}}`))
				return s, c
			},
			reason: "records no budget amount",
		},
		"version 2 with a newline inside the currency": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"amount":7,"currency":"XXXXXXXX\n"}}`))
				return s, c
			},
			reason: "records no budget amount",
		},
		"version 2 with the largest int64 amount": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"amount":9223372036854775807,"currency":"USD"}}`))
				return s, c
			},
			reason: "records no budget amount",
		},
		"version 2 with an amount one above the ceiling": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"amount":10000001,"currency":"USD"}}`))
				return s, c
			},
			reason: "records no budget amount",
		},
		"version 2 with a negative amount": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"amount":-5,"currency":"USD"}}`))
				return s, c
			},
			reason: "records no budget amount",
		},
		// A version-1 object carries no budget: version 1 is a creation that
		// was never required to choose one, so a budget in it is a field
		// someone else wrote. The shape check refuses it first ("not a
		// marker"); readFact's version-1 case refuses it again, with its own
		// reason, so the rule does not rest on the shape check alone — and a
		// shape check that let the budget through for version 1 changes the
		// reason this row sees, which is what fails it.
		"version 1 with a budget": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":1,"gcp_project":"`+testProject+`","budget":{"amount":50,"currency":"USD"}}`))
				return s, c
			},
			reason: "not a marker",
		},
		"version 2 with a budget field spelled differently": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"AMOUNT":50,"currency":"USD"}}`))
				return s, c
			},
			reason: "not a marker",
		},
		"version 2 with a budget field this build does not write": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":{"amount":50,"currency":"USD","thresholds":[0.5]}}`))
				return s, c
			},
			reason: "not a marker",
		},
		"a budget that is not an object": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":2,"gcp_project":"`+testProject+`","budget":50}`))
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
		"a project and a bucket with no number": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f := newStorage(t)
				f.AddBucket(testBucket, 0, testManaged)
				f.Put(testBucket, MarkerObject, []byte(markerJSON))
				c, cf := newCRM(t)
				cf.AddProject(testProject, 0)
				return s, c
			},
			reason: "no number",
		},
		"keys spelled differently": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"VERSION":1,"GCP_PROJECT":"`+testProject+`"}`))
				return s, c
			},
			reason: "not a marker",
		},
		"a key that appears twice": {
			world: func(t *testing.T) (*storage.Service, *crm.Service) {
				s, f, c, _ := managedWorld(t)
				f.Put(testBucket, MarkerObject, []byte(`{"version":1,"gcp_project":"proj-9999","gcp_project":"`+testProject+`"}`))
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
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			s, c := tc.world(t)
			ok, reason, err := IsManaged(ctx, s, c, testFugaroProject, testProject)
			if ok {
				t.Fatalf("IsManaged = true: a marker that reads as managed with %s turns the confirmations off", name)
			}
			if reason == "" {
				t.Fatal("no reason: a refusal a person cannot act on is the same as no refusal")
			}
			// One line, and one line because safetext.OneLine made it one: the
			// row above hands the reason an API message with a newline and an
			// escape sequence in it, so this fails when that boundary is
			// removed rather than passing on fixtures that never carry one.
			if strings.ContainsAny(reason, "\n\r\x1b") {
				t.Errorf("the reason is not one line: %q", reason)
			}
			if !strings.Contains(reason, tc.reason) {
				t.Errorf("reason = %q, want it to say %q", reason, tc.reason)
			}
			// The Fugaro project name alone, because the GCP project's is no
			// evidence at all: the runs bucket is fugaro-runs-<gcp project>, so
			// nearly every reason holds the GCP project's name as a substring
			// of the bucket's, and an assertion that accepted either would pass
			// without the reason being about the project at all — which is what
			// it is documented to be (read.go: "project ... appears in every
			// reason").
			if !strings.Contains(reason, testFugaroProject) {
				t.Errorf("reason = %q: it does not name the Fugaro project %q it is about", reason, testFugaroProject)
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

// The no-label reason states the CONDITION and not a command, which is this
// PR's own settled decision (§5 of
// docs/plans/2026-10-18-managed-projects-task-3.0-decisions.md) and the one
// point in it the owner may veto: the reason is the one piece of the label
// that reaches a coding agent's session, and a sentence naming the write
// (gcloud projects update, or the owner's flag) invites the boundary write
// from the side that must never make it. The guidance stays in
// docs/design/managed-projects.md, which the reason names — so this test asserts both
// halves: the condition is stated, and no command is.
func TestTheNoLabelReasonNamesNoCommand(t *testing.T) {
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))
	c, _ := labelledCRM(t, map[string]string{"environment": "prod"})

	ok, reason, err := IsManaged(context.Background(), s, c, testFugaroProject, testProject)
	if err != nil || ok {
		t.Fatalf("IsManaged = %v, %q, %v; want false, a reason, and no failure", ok, reason, err)
	}
	if !strings.Contains(reason, ProjectLabelKey) || !strings.Contains(reason, "docs/design/managed-projects.md") {
		t.Errorf("reason = %q, want the condition (the missing %s label) and where a person reads the guidance", reason, ProjectLabelKey)
	}
	// The commands that write the label, and the one that removes it, by the
	// names a session would copy out of the string: none of them is this
	// reason's to give.
	for _, cmd := range []string{"gcloud", "managed flag", "managed apply", "projects update", "--update-labels", "--remove-labels"} {
		if strings.Contains(reason, cmd) {
			t.Errorf("reason = %q names %q: the refusal states the condition and not a command (decisions §5)", reason, cmd)
		}
	}
}

// The fact is about the GCP project, and the Fugaro project name is the name
// the answer is reported about: an installation is one Fugaro project in one
// GCP project, whose own bucket carries the fact, so the same GCP project
// answers the same way under any name — and a refusal names the project the
// caller asked about rather than a name read out of the object.
func TestIsManagedIsAboutTheGCPProject(t *testing.T) {
	ctx := context.Background()
	s, f, c, _ := managedWorld(t)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))

	ok, reason, err := IsManaged(ctx, s, c, "another-name", testProject)
	if err != nil || !ok || reason != "" {
		t.Fatalf("IsManaged under another name = %v, %q, %v; want true, \"\", nil", ok, reason, err)
	}

	// The other half: the refusal is about the project asked about. No
	// marker is the one mismatch every name shares.
	s2, _, c2, _ := managedWorld(t)
	ok, reason, err = IsManaged(ctx, s2, c2, "another-name", testProject)
	if err != nil || ok {
		t.Fatalf("no marker = %v, %q, %v; want false, a reason, nil", ok, reason, err)
	}
	if !strings.Contains(reason, "another-name") {
		t.Errorf("reason = %q, want it to name the project asked about", reason)
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

// The read's bounds are the write's (Budget.Check), so the largest amount a
// creation may record and the smallest are read back as they were written:
// the ceiling is a bound, not an off-by-one that refuses the value the write
// accepted.
func TestReadBudgetAcceptsTheBounds(t *testing.T) {
	ctx := context.Background()
	for _, amount := range []int64{MinBudgetAmount, MaxBudgetAmount} {
		s, f, c, _ := managedWorld(t)
		f.Put(testBucket, MarkerObject, []byte(fmt.Sprintf(`{"version":2,"gcp_project":"%s","budget":{"amount":%d,"currency":"EUR"}}`, testProject, amount)))
		budget, ok, reason, err := ReadBudget(ctx, s, c, testFugaroProject, testProject)
		if err != nil || !ok || budget.Amount != amount || budget.Currency != "EUR" {
			t.Errorf("amount %d: ReadBudget = %+v, %v, %q, %v; want it read back", amount, budget, ok, reason, err)
		}
	}
}

// A version-1 creation fact — every --create-project run without --managed
// writes one, and every project created before Task 4.2 carries one — is
// managed (IsManaged true: the compatibility the merged code and its tests
// keep) and has no budget (ReadBudget ok=false). The two answers differ on
// purpose, and this pins both so Task 4.4 cannot forget it: the apply gate
// and `fugaro managed apply` must ask ReadBudget, not IsManaged, or a v1
// project — managed without a budget — reaches an apply with no line to
// compare a spend against (docs/plans/2026-10-16-managed-projects.md, Task
// 4.4; docs/design/managed-projects.md).
func TestVersion1IsManagedWithoutABudget(t *testing.T) {
	ctx := context.Background()
	s, f, c, _ := managedWorld(t)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))
	ok, reason, err := IsManaged(ctx, s, c, testFugaroProject, testProject)
	if err != nil || !ok {
		t.Fatalf("IsManaged = %v, %q, %v; want true: a version-1 fact stays managed", ok, reason, err)
	}
	budget, ok, reason, err := ReadBudget(ctx, s, c, testFugaroProject, testProject)
	if err != nil || ok {
		t.Fatalf("ReadBudget = %+v, %v, %q, %v; want ok=false and no error: a version-1 fact records no budget", budget, ok, reason, err)
	}
	if !strings.Contains(reason, "records no budget amount") {
		t.Errorf("reason = %q, want it to say the fact records no budget amount", reason)
	}
}

// A version-2 marker whose recorded figure fails Budget.Check is refused —
// IsManaged false, ReadBudget ok=false — and ReadBudget hands the refused
// figure back beside the refusal, so a caller can say the recorded budget is
// invalid rather than that the project is not managed. Only that case carries
// a figure; a valid one naming another project carries none.
func TestReadBudgetHandsBackAFigureThatFailsCheck(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		marker string
		want   Budget
	}{
		{`{"version":2,"gcp_project":"` + testProject + `","budget":{"amount":5000,"currency":"usd"}}`, Budget{Amount: 5000, Currency: "usd"}},
		{`{"version":2,"gcp_project":"` + testProject + `","budget":{"amount":10000001,"currency":"USD"}}`, Budget{Amount: 10000001, Currency: "USD"}},
		{`{"version":2,"gcp_project":"proj-9999","budget":{"amount":5000,"currency":"USD"}}`, Budget{}},
	} {
		s, f, c, _ := managedWorld(t)
		f.Put(testBucket, MarkerObject, []byte(tc.marker))
		b, ok, reason, err := ReadBudget(ctx, s, c, testFugaroProject, testProject)
		if ok || err != nil {
			t.Fatalf("%s: ReadBudget = %+v, %v, %v: want a refusal answered, not failed", tc.marker, b, ok, err)
		}
		if b != tc.want {
			t.Errorf("%s: the figure handed back is %+v, want %+v", tc.marker, b, tc.want)
		}
		if reason == "" {
			t.Errorf("%s: no reason", tc.marker)
		}
		if managed, _, _ := IsManaged(ctx, s, c, testFugaroProject, testProject); managed {
			t.Errorf("%s: IsManaged believed it", tc.marker)
		}
	}
}

// L3: ReadBudgetAnswer says Invalid for a v2 figure that fails Check —
// including the empty "budget":{} — and the project name is judged first, so
// an invalid figure in a marker about another project is "another project",
// never this project's invalid budget.
func TestReadBudgetAnswerInvalidAndProjectFirst(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		marker  string
		invalid bool
		reason  string
	}{
		{`{"version":2,"gcp_project":"` + testProject + `","budget":{}}`, true, "records no budget amount a managed creation could have chosen"},
		{`{"version":2,"gcp_project":"` + testProject + `","budget":{"amount":5000,"currency":"usd"}}`, true, "could have chosen"},
		{`{"version":2,"gcp_project":"proj-9999","budget":{"amount":5000,"currency":"usd"}}`, false, "names project proj-9999"},
		{`{"version":2,"gcp_project":"proj-9999","budget":{}}`, false, "names project proj-9999"},
	} {
		s, f, c, _ := managedWorld(t)
		f.Put(testBucket, MarkerObject, []byte(tc.marker))
		a, err := ReadBudgetAnswer(ctx, s, c, testFugaroProject, testProject)
		if err != nil || a.OK {
			t.Fatalf("%s: %+v, %v", tc.marker, a, err)
		}
		if a.Invalid != tc.invalid {
			t.Errorf("%s: Invalid = %v, want %v (%s)", tc.marker, a.Invalid, tc.invalid, a.Reason)
		}
		if !strings.Contains(a.Reason, tc.reason) {
			t.Errorf("%s: reason %q, want %q", tc.marker, a.Reason, tc.reason)
		}
	}
}

// L3, and the precedence between the two reasons a project can be refused for
// at once: an unlabelled project whose runs bucket holds a version-2 marker
// about it carrying a figure that fails Budget.Check is refused for the
// FIGURE — Invalid, with the refused figure handed back — and not for the
// missing label. Both halves hold, and the figure's reason is the more
// specific one, which is the one to win: the label's sentence sends the owner
// to the backfill in docs/design/managed-projects.md, and a backfill would leave them with a
// managed project whose recorded amount nothing can be compared against.
//
// Every other state of the object keeps the label's reason. It is the verdict
// — a project without that label is not managed whatever the object says, and
// IsManaged has to stay false for all of these — and it is the sentence the
// owner of a project marked before Task 3.0 reads first (decisions §5).
func TestReadBudgetAnswerTheInvalidFigureOutranksTheMissingLabel(t *testing.T) {
	ctx := context.Background()
	const noLabel = "carries no fugaro_managed label"
	for name, tc := range map[string]struct {
		marker  string // "" for a bucket with no object in it
		invalid bool
		reason  string
	}{
		"a figure that fails Check": {
			marker:  `{"version":2,"gcp_project":"` + testProject + `","budget":{"amount":5000,"currency":"usd"}}`,
			invalid: true,
			reason:  "a managed creation could have chosen",
		},
		"an empty figure": {
			marker:  `{"version":2,"gcp_project":"` + testProject + `","budget":{}}`,
			invalid: true,
			reason:  "a managed creation could have chosen",
		},
		// Not Invalid, and the label's reason: the project is not managed, and
		// a valid figure in an object it may not be believed from changes
		// nothing about that.
		"a valid figure": {
			marker: `{"version":2,"gcp_project":"` + testProject + `","budget":{"amount":50,"currency":"USD"}}`,
			reason: noLabel,
		},
		"a version-1 fact, which carries no figure": {
			marker: `{"version":1,"gcp_project":"` + testProject + `"}`,
			reason: noLabel,
		},
		// The project's name is judged before its figure (readFact), so this
		// one would carry no figure back even with the label written; without
		// it the label's reason stands.
		"another project's marker, with a figure that fails Check": {
			marker: `{"version":2,"gcp_project":"proj-9999","budget":{"amount":5000,"currency":"usd"}}`,
			reason: noLabel,
		},
		"no object at all": {reason: noLabel},
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			if tc.marker != "" {
				f.Put(testBucket, MarkerObject, []byte(tc.marker))
			}
			// The state the regression of decisions §5 is: everything else as
			// a managed project has it, and this key never written. Other
			// labels of its own, so the row is about this key.
			c, _ := labelledCRM(t, map[string]string{"environment": "prod"})
			a, err := ReadBudgetAnswer(ctx, s, c, testFugaroProject, testProject)
			if err != nil || a.OK {
				t.Fatalf("%+v, %v: want a refusal answered, not a failure", a, err)
			}
			if a.Invalid != tc.invalid {
				t.Errorf("Invalid = %v, want %v (%s)", a.Invalid, tc.invalid, a.Reason)
			}
			if !strings.Contains(a.Reason, tc.reason) {
				t.Errorf("reason %q, want %q", a.Reason, tc.reason)
			}
			if tc.invalid && strings.Contains(a.Reason, noLabel) {
				t.Errorf("the label's reason was answered for a figure no creation could have chosen: %q", a.Reason)
			}
			// The verdict is the same either way: the read of the object buys
			// the better reason, never a managed project.
			if managed, _, _ := IsManaged(ctx, s, c, testFugaroProject, testProject); managed {
				t.Error("IsManaged believed a project with no fugaro_managed label")
			}
		})
	}
}
