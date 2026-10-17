package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The delete list's whole shape, byte for byte (Task 1.4, D74): a version,
// the project the list is about, and the addresses — nothing else. No note,
// no reason, no comment field: a free-text field in a file a run reads is
// text an agent would like to write, and the approval's provenance is the
// object's own version history rather than prose inside it.
const deleteListJSON = `{"version":1,"gcp_project":"proj-1234","addresses":["google_cloud_run_v2_job.web","module.svc.google_storage_bucket.data"]}`

// deleteListAddresses is what the fixture above holds, in order.
var deleteListAddresses = []string{"google_cloud_run_v2_job.web", "module.svc.google_storage_bucket.data"}

// deleteListAt is the delete list's bytes in bucket, and whether it is there
// at all.
func deleteListAt(t *testing.T, s *storage.Service, bucket string) (string, bool) {
	t.Helper()
	resp, err := s.Objects.Get(bucket, AllowDeleteObject).Download()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return "", false
		}
		t.Fatalf("reading gs://%s/%s: %v", bucket, AllowDeleteObject, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// deletePlan is a plan carrying one delete per address: the fixture the
// fail-closed tests assert against, so what they check is the gate — the
// list handed to tf.Guard — rather than the reader's return value alone.
func deletePlan(addresses ...string) *tf.Plan {
	p := &tf.Plan{FormatVersion: "1.2"}
	for _, a := range addresses {
		p.ResourceChanges = append(p.ResourceChanges, tf.ResourceChange{
			Address: a, Type: "google_storage_bucket", Change: tf.Change{Actions: []string{"delete"}},
		})
	}
	return p
}

// seedDeleteList is the fixture every test below starts from: the project's
// own runs bucket, labelled and numbered as the read proves it, with body at
// the object's key ("" for no object at all).
func seedDeleteList(t *testing.T, body string) (*storage.Service, string) {
	t.Helper()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	if body != "" {
		f.Put(testBucket, AllowDeleteObject, []byte(body))
	}
	return s, testBucket
}

// TestDeleteListJSONShape pins the object's shape against the fixture: three
// fields, exactly, and a version and a project that say what the list is.
func TestDeleteListJSONShape(t *testing.T) {
	d := DeleteList{Version: AllowDeleteVersion, GCPProject: testProject, Addresses: deleteListAddresses}
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != deleteListJSON {
		t.Errorf("Bytes() = %q, want %q", b, deleteListJSON)
	}
	var back DeleteList
	if err := json.Unmarshal([]byte(deleteListJSON), &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, d) {
		t.Errorf("the fixture parses as %+v, want %+v", back, d)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(deleteListJSON), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Errorf("the object holds %d fields (%v): a version, a project and the addresses, and nothing else", len(fields), fields)
	}
	for name, bad := range map[string]DeleteList{
		"no project":      {Version: AllowDeleteVersion, Addresses: deleteListAddresses},
		"another version": {Version: AllowDeleteVersion + 1, GCPProject: testProject, Addresses: deleteListAddresses},
		"no addresses":    {Version: AllowDeleteVersion, GCPProject: testProject},
		"a blank address": {Version: AllowDeleteVersion, GCPProject: testProject, Addresses: []string{"", "a.b"}},
		"a space address": {Version: AllowDeleteVersion, GCPProject: testProject, Addresses: []string{"a.b", " "}},
	} {
		if _, err := bad.Bytes(); err == nil {
			t.Errorf("a list with %s was encoded: %+v", name, bad)
		}
	}
}

// TestWriteDeleteListRoundTrip: written by the one writer, read back through
// the project-number check.
func TestWriteDeleteListRoundTrip(t *testing.T) {
	s, bucket := seedDeleteList(t, "")
	ctx := context.Background()
	if err := WriteDeleteList(ctx, s, bucket, testProject, testProjectNumber, deleteListAddresses, 0); err != nil {
		t.Fatal(err)
	}
	if got, ok := deleteListAt(t, s, bucket); !ok || got != deleteListJSON {
		t.Fatalf("gs://%s/%s = %q (there %v), want %q", bucket, AllowDeleteObject, got, ok, deleteListJSON)
	}
	list, err := AllowDelete(ctx, s, bucket, testProject, testProjectNumber)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(list, deleteListAddresses) {
		t.Errorf("AllowDelete() = %v, want %v", list, deleteListAddresses)
	}
}

// TestWriteDeleteListWritesTheWholeList: the writer is idempotent, and a
// shorter list only ever refuses more — the object holds the second call's
// addresses, not a merge of the two, so nothing an owner took off the list
// stays deletable.
func TestWriteDeleteListWritesTheWholeList(t *testing.T) {
	s, bucket := seedDeleteList(t, "")
	ctx := context.Background()
	if err := WriteDeleteList(ctx, s, bucket, testProject, testProjectNumber, deleteListAddresses, 0); err != nil {
		t.Fatal(err)
	}
	short := []string{deleteListAddresses[1]}
	state, err := ReadDeleteListForWrite(ctx, s, bucket, testProject, testProjectNumber)
	if err != nil || state.Generation == 0 || !slices.Equal(state.Addresses, deleteListAddresses) {
		t.Fatalf("ReadDeleteListForWrite() = %+v, %v; want the first list and its generation", state, err)
	}
	if err := WriteDeleteList(ctx, s, bucket, testProject, testProjectNumber, short, state.Generation); err != nil {
		t.Fatal(err)
	}
	list, err := AllowDelete(ctx, s, bucket, testProject, testProjectNumber)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(list, short) {
		t.Errorf("after the second write AllowDelete() = %v, want %v (the whole list, not an append)", list, short)
	}
	if err := tf.Guard(deletePlan(deleteListAddresses[0]), list); err == nil {
		t.Error("an address the second write dropped is still deletable")
	}
}

// TestWriteDeleteListNeedsTheProjectsOwnRunsBucket: the writer proves the
// bucket the way the read does, so a list can never be recorded somewhere
// nothing reads — a bucket of the same name in someone else's project, or
// one without the installation's own label.
func TestWriteDeleteListNeedsTheProjectsOwnRunsBucket(t *testing.T) {
	// bucketNumber is what the bucket says its project is numbered, number
	// what the call passes as the number it read for gcpProject: the write
	// believes a bucket only when the two agree, so the row about another
	// project's bucket is a row where they differ.
	for name, tc := range map[string]struct {
		bucket       string
		bucketNumber uint64
		number       uint64
		labels       map[string]string
		project      string
	}{
		"another project's bucket": {testBucket, testProjectNumber + 1, testProjectNumber, testManaged, testProject},
		"a bucket with no mark":    {testBucket, testProjectNumber, testProjectNumber, nil, testProject},
		"a mark of someone else":   {testBucket, testProjectNumber, testProjectNumber, map[string]string{gcp.LabelManaged: "managed-by-someone"}, testProject},
		"no project number":        {testBucket, testProjectNumber, 0, testManaged, testProject},
		"no project named":         {testBucket, testProjectNumber, testProjectNumber, testManaged, ""},
		"no bucket named":          {"", testProjectNumber, testProjectNumber, testManaged, testProject},
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStorage(t)
			if tc.bucket != "" {
				f.AddBucket(tc.bucket, tc.bucketNumber, tc.labels)
			}
			if tc.bucket != testBucket {
				// A bucket the refusal must leave empty, so the row that
				// names no bucket is checked as well as the ones that do.
				f.AddBucket(testBucket, testProjectNumber, testManaged)
			}
			err := WriteDeleteList(context.Background(), s, tc.bucket, tc.project, tc.number, deleteListAddresses, 0)
			if err == nil {
				t.Fatal("the list was written into a bucket that is not the project's own")
			}
			if got, ok := deleteListAt(t, s, testBucket); ok {
				t.Errorf("gs://%s/%s is there (%s): the write must refuse", testBucket, AllowDeleteObject, got)
			}
		})
	}
	// The bucket the tests above refuse is the one this one writes: the
	// project's own runs bucket, numbered and labelled as the read proves it.
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	if err := WriteDeleteList(context.Background(), s, testBucket, testProject, testProjectNumber, deleteListAddresses, 0); err != nil {
		t.Fatalf("the project's own runs bucket was refused: %v", err)
	}
	if err := WriteDeleteList(context.Background(), nil, testBucket, testProject, testProjectNumber, deleteListAddresses, 0); err == nil {
		t.Error("the write was made with no storage client")
	}
}

// TestWriteDeleteListRefusesNoAddresses: with no addresses the command
// prints the list rather than emptying it, and the writer refuses for the
// same reason — a write that emptied the list would be a refusal of every
// delete dressed as an approval, and the owner's way to refuse a delete is
// to write the list without it.
func TestWriteDeleteListRefusesNoAddresses(t *testing.T) {
	s, bucket := seedDeleteList(t, "")
	ctx := context.Background()
	for name, addresses := range map[string][]string{
		"no addresses":    nil,
		"blank addresses": {"", "  "},
		"an empty slice":  {},
	} {
		if err := WriteDeleteList(ctx, s, bucket, testProject, testProjectNumber, addresses, 0); err == nil {
			t.Errorf("%s: an empty list was written", name)
		}
	}
	if _, ok := deleteListAt(t, s, bucket); ok {
		t.Errorf("gs://%s/%s is there: a refused write must write nothing", bucket, AllowDeleteObject)
	}
}

// TestWriteDeleteListRefusesAListItsOwnReadWouldRefuse: the read applies
// allowDeleteMaxBytes, so the write does too. A list past the cap would
// otherwise be written and printed as an approval and then read back as no
// addresses — every delete the owner just approved silently refused, with
// the object looking like the approval it is not.
func TestWriteDeleteListRefusesAListItsOwnReadWouldRefuse(t *testing.T) {
	s, bucket := seedDeleteList(t, "")
	ctx := context.Background()
	// Long enough that the encoding passes the cap, which is what the write
	// checks rather than the count: an address is a Terraform address, and
	// the object's size is what the read is bounded by.
	addresses := make([]string, 0, allowDeleteMaxBytes/20)
	for i := range cap(addresses) {
		addresses = append(addresses, fmt.Sprintf("google_cloud_run_v2_job.web_%08d", i))
	}
	err := WriteDeleteList(ctx, s, bucket, testProject, testProjectNumber, addresses, 0)
	if err == nil {
		t.Fatalf("a list of %d addresses was written: the read would refuse it", len(addresses))
	}
	if !errors.Is(err, ErrRefused) {
		t.Errorf("WriteDeleteList() = %v, want a refusal of this package's own", err)
	}
	if _, ok := deleteListAt(t, s, bucket); ok {
		t.Errorf("gs://%s/%s is there: a refused write must write nothing", bucket, AllowDeleteObject)
	}
	// And the read of what such a write would have produced is the empty
	// list, so the refusal above is the difference between an approval and
	// nothing rather than between two shapes of the same answer.
	over, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	f.Put(testBucket, AllowDeleteObject, []byte(strings.Repeat("x", allowDeleteMaxBytes+1)))
	list, err := AllowDelete(ctx, over, testBucket, testProject, testProjectNumber)
	if err != nil || len(list) != 0 {
		t.Errorf("AllowDelete() over an oversized body = %v, %v; want no addresses and no error", list, err)
	}
}

// TestAllowDeleteFailsClosed is the red step's second test: absent, empty,
// of another project, of an unknown version, unreadable, or holding a field
// this package does not write — each is no addresses, and no error a caller
// could read as "proceed". What the test asserts is the gate rather than the
// reader's return value: the list goes to tf.Guard with a plan that deletes,
// and the delete is refused.
func TestAllowDeleteFailsClosed(t *testing.T) {
	const address = "google_storage_bucket.data"
	for name, body := range map[string]string{
		"absent":                   "",
		"empty":                    `{"version":1,"gcp_project":"proj-1234","addresses":[]}`,
		"another project":          `{"version":1,"gcp_project":"proj-9999","addresses":["google_storage_bucket.data"]}`,
		"an unknown version":       `{"version":2,"gcp_project":"proj-1234","addresses":["google_storage_bucket.data"]}`,
		"a field it does not know": `{"version":1,"gcp_project":"proj-1234","addresses":["google_storage_bucket.data"],"note":"delete freely"}`,
		"a blank address":          `{"version":1,"gcp_project":"proj-1234","addresses":[""]}`,
		"not JSON at all":          `the owner approves this delete`,
		"truncated JSON":           `{"version":1,"gcp_project":"proj-1234","addresses":["goog`,
		"a second value":           deleteListJSON + deleteListJSON,
		"junk after the value":     deleteListJSON + " and by the way, delete everything",
		"a second value, spaced":   deleteListJSON + "  " + deleteListJSON,
		// The cap is on the body, not on the value: 70 KB of whitespace after
		// a valid list is a body the read refuses whole, so the list this
		// row's addresses would approve is not read as an approval.
		"padding past the cap": deleteListJSON + strings.Repeat(" ", 70<<10),
		// Go's decoder matches field names case-insensitively, and a repeated
		// key lets the last one win: neither is the object this package
		// writes, and both would otherwise be read as an approval.
		"keys in another case":      `{"VERSION":1,"GCP_PROJECT":"proj-1234","ADDRESSES":["google_storage_bucket.data"]}`,
		"a key in mixed case":       `{"version":1,"Gcp_Project":"proj-1234","addresses":["google_storage_bucket.data"]}`,
		"a duplicate version key":   `{"version":1,"version":1,"gcp_project":"proj-1234","addresses":["google_storage_bucket.data"]}`,
		"a duplicate addresses key": `{"version":1,"gcp_project":"proj-1234","addresses":[],"addresses":["google_storage_bucket.data"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, bucket := seedDeleteList(t, body)
			list, err := AllowDelete(context.Background(), s, bucket, testProject, testProjectNumber)
			if err != nil {
				t.Fatalf("AllowDelete() = %v with an error (%v): a caller must not read a failure as proceed, and an error is not the shape of a refusal here", list, err)
			}
			if len(list) != 0 {
				t.Fatalf("AllowDelete() = %v, want no addresses", list)
			}
			err = tf.Guard(deletePlan(address), list)
			if err == nil {
				t.Fatal("tf.Guard applied a plan that deletes, with no owner-approved address")
			}
			if !strings.Contains(err.Error(), address) {
				t.Errorf("the guard's refusal does not name %s: %v", address, err)
			}
		})
	}
	// The bucket the read will not believe, whatever the object in it says:
	// a name is global, so a list planted in a stranger's bucket must not be
	// believed either.
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber+1, testManaged)
	f.Put(testBucket, AllowDeleteObject, []byte(deleteListJSON))
	list, err := AllowDelete(context.Background(), s, testBucket, testProject, testProjectNumber)
	if err != nil || len(list) != 0 {
		t.Errorf("AllowDelete() from another project's bucket = %v, %v; want no addresses and no error", list, err)
	}
	// The positive case, so the rows above cannot pass by every read being
	// empty: an owner-approved address passes the guard, and one the owner
	// did not name does not.
	s, bucket := seedDeleteList(t, deleteListJSON)
	list, err = AllowDelete(context.Background(), s, bucket, testProject, testProjectNumber)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(list, deleteListAddresses) {
		t.Fatalf("AllowDelete() = %v, want the owner's %v", list, deleteListAddresses)
	}
	if err := tf.Guard(deletePlan(deleteListAddresses...), list); err != nil {
		t.Errorf("a plan deleting the owner's own addresses was refused: %v", err)
	}
	if err := tf.Guard(deletePlan(deleteListAddresses[0], address), list); err == nil {
		t.Error("a plan deleting an address the owner did not name was applied")
	}
}

// TestAllowDeleteRefusesAnAddressTheApplyingCommandPasses is the red step's
// first test: an apply that feeds its own --allow-delete into the guard in a
// managed project must not have it honoured. The addresses come from the
// owner's object — written by the owner's own credential through
// fugaro allow-delete — and never from the arguments of the command that
// applies the plan (D72, D74).
func TestAllowDeleteRefusesAnAddressTheApplyingCommandPasses(t *testing.T) {
	const address = "google_storage_bucket.data"
	s, bucket := seedDeleteList(t, deleteListJSON)
	ctx := context.Background()
	pasted := []string{"google_storage_bucket.elsewhere"}
	list, err := GuardAllowList(ctx, s, bucket, testProject, testProjectNumber, pasted)
	if err == nil {
		t.Fatal("the applying command's own --allow-delete was accepted in a managed project")
	}
	if !strings.Contains(err.Error(), "google_storage_bucket.elsewhere") {
		t.Errorf("the refusal does not name the address it refuses: %v", err)
	}
	// And it names no flag: the session it refuses is the one that would read
	// a flag name here as the thing to pass next, so the managed refusal says
	// what the owner's list is instead of naming an argument.
	if strings.Contains(err.Error(), "--") {
		t.Errorf("the managed refusal names a flag: %v", err)
	}
	if !strings.Contains(err.Error(), AllowDeleteObject) {
		t.Errorf("the refusal does not name the object the owner's addresses come from: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("GuardAllowList() = %v, want no addresses", list)
	}
	// Even the address the owner did approve is refused when it is passed on
	// the command line: what is refused is the source, not the value.
	if _, err := GuardAllowList(ctx, s, bucket, testProject, testProjectNumber, []string{deleteListAddresses[0]}); err == nil {
		t.Error("an owner-approved address passed on the applying command line was accepted")
	}
	// With no flags of its own, the same call returns the owner's list: the
	// refusals above are about the source, not about the read failing.
	list, err = GuardAllowList(ctx, s, bucket, testProject, testProjectNumber, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(list, deleteListAddresses) {
		t.Errorf("GuardAllowList() = %v, want the owner's list %v", list, deleteListAddresses)
	}
	// The owner's list approves its own addresses whole: nothing they are a
	// prefix of, nothing under them, nothing differing in case.
	for _, planned := range []string{
		"module.svc.google_storage_bucket.data_old",
		"module.svc.google_storage_bucket.data[\"a\"]",
		"module.svc2.google_storage_bucket.data",
		"module.svc.google_storage_bucket.DATA",
		"google_cloud_run_v2_job.web_old",
	} {
		if err := tf.Guard(deletePlan(planned), list); err == nil {
			t.Errorf("the owner's list %v let %q be deleted", list, planned)
		}
	}
	// And a list it cannot read is no addresses, never an error a caller
	// could read as proceed.
	absent, empty := seedDeleteList(t, "")
	list, err = GuardAllowList(ctx, absent, empty, testProject, testProjectNumber, nil)
	if err != nil {
		t.Fatalf("GuardAllowList() over an absent list = %v, %v; want no addresses and no error", list, err)
	}
	if len(list) != 0 {
		t.Errorf("GuardAllowList() = %v, want no addresses", list)
	}
}

// TestTheDeleteListIsNotTerraformManaged is D74's other half, and the reason
// the list is a second object rather than a field of the marker: no
// Terraform-managed object writes it. fugaro init rewrites the
// installation's terraform.tfvars.json from its own flags on every run, so a
// variable-driven object would be planned to destroy on the next ordinary
// init — silently, with the apply that follows reading an empty list and
// refusing every delete the owner approved.
func TestTheDeleteListIsNotTerraformManaged(t *testing.T) {
	files := moduleFiles(t)
	for _, p := range slices.Sorted(maps.Keys(files)) {
		for _, needle := range []string{"allow-delete", "allow_delete", "AllowDelete"} {
			if strings.Contains(files[p], needle) {
				t.Errorf("%s names %q: the delete list is written by the owner-run command alone, never by the installation's Terraform", p, needle)
			}
		}
	}
	if len(files) < 10 {
		t.Fatalf("read %d module files: this assertion is vacuous", len(files))
	}
}

// TestRunAccountsHaveNoWritePathToTheDeleteList is Global Constraint 4 for
// this object: no credential a run holds may write it, which is what makes
// D72's "never from the applying command's own arguments" a property of the
// IAM rather than a promise — an agent that could write the list could
// approve its own deletes. The three grants below are the ones the Go side
// builds for a run's job account, a build account and a launcher
// (internal/infra/spec.go, installation/iam.tf); the object sits at the
// bucket's root, above every prefix they are conditioned to.
//
// The live bindings are Check 68, owner-run: Global Constraint 12 forbids a
// live IAM read from a test.
func TestRunAccountsHaveNoWritePathToTheDeleteList(t *testing.T) {
	if got := path.Dir(AllowDeleteObject); got != "fugaro" {
		t.Fatalf("%s is not at the runs bucket's fugaro/ root (%s)", AllowDeleteObject, got)
	}
	for name, expr := range map[string]string{
		"a job account's grant":   gcp.BucketCondition(testBucket, gcp.JobBucketPrefixes, "web-a1b2c3"),
		"a build account's grant": gcp.BucketCondition(testBucket, gcp.BuildBucketPrefixes, "web"),
		"a launcher's grant":      gcp.LauncherBucketCondition(testBucket),
	} {
		prefixes := startsWithPrefixes(expr, testBucket)
		if len(prefixes) == 0 {
			t.Fatalf("%s: %q names no prefix, so this assertion is vacuous", name, expr)
		}
		for _, p := range prefixes {
			if strings.HasPrefix(AllowDeleteObject, p) {
				t.Errorf("%s reaches %s: %s", name, AllowDeleteObject, expr)
			}
		}
	}
	// And the modules themselves, read by the same scan Task 0.1's test uses:
	// no grant on the runs bucket that this scan cannot account for, and no
	// finding at all.
	scan := scanModuleIAM(moduleFiles(t))
	for _, f := range scan.findings {
		t.Errorf("%s", f)
	}
	if scan.writes < 4 || scan.narrowed < 3 {
		t.Errorf("the scan found %d write grants on the runs bucket and %d narrowing conditions, want the job account's, the build account's and the launchers' at least", scan.writes, scan.narrowed)
	}
}

// TestWriteDeleteListRefusesAListThatChangedSinceItsRead: the write is
// conditioned on the generation the writer read, so a second writer's list is
// never overwritten by one computed from what was there before it — neither
// over an object that appeared (generation 0) nor over one that changed.
func TestWriteDeleteListRefusesAListThatChangedSinceItsRead(t *testing.T) {
	s, bucket := seedDeleteList(t, "")
	ctx := context.Background()
	if err := WriteDeleteList(ctx, s, bucket, testProject, testProjectNumber, deleteListAddresses, 0); err != nil {
		t.Fatal(err)
	}
	stale, err := ReadDeleteListForWrite(ctx, s, bucket, testProject, testProjectNumber)
	if err != nil {
		t.Fatal(err)
	}
	// Another writer, in between.
	if err := WriteDeleteList(ctx, s, bucket, testProject, testProjectNumber, deleteListAddresses[:1], stale.Generation); err != nil {
		t.Fatal(err)
	}
	for name, gen := range map[string]int64{"a stale generation": stale.Generation, "no object expected": 0} {
		err := WriteDeleteList(ctx, s, bucket, testProject, testProjectNumber, deleteListAddresses[1:], gen)
		if !errors.Is(err, ErrChanged) {
			t.Errorf("%s: WriteDeleteList() = %v, want ErrChanged", name, err)
		}
	}
	if list, _ := AllowDelete(ctx, s, bucket, testProject, testProjectNumber); !slices.Equal(list, deleteListAddresses[:1]) {
		t.Errorf("after the refused writes AllowDelete() = %v, want the other writer's %v", list, deleteListAddresses[:1])
	}
}

// TestReadDeleteListForWriteFailsClosed: the writer's read is strict where
// AllowDelete is lenient. A read that fails is an error rather than no
// addresses (which would let the next write drop every approval unasked);
// an absent object is no addresses and generation 0; an object it cannot
// believe is reported as such, with its generation.
func TestReadDeleteListForWriteFailsClosed(t *testing.T) {
	ctx := context.Background()
	s, f := newStorage(t)
	f.AddBucket(testBucket, testProjectNumber, testManaged)
	state, err := ReadDeleteListForWrite(ctx, s, testBucket, testProject, testProjectNumber)
	if err != nil || state.Generation != 0 || state.Addresses != nil || state.Unreadable {
		t.Fatalf("over no object: %+v, %v; want the zero state", state, err)
	}
	f.Put(testBucket, AllowDeleteObject, []byte(deleteListJSON))
	// The metadata read failing, then the media read after it.
	for _, skip := range []int{0, 1} {
		f.FailObjectGetsAfter(testBucket, AllowDeleteObject, skip)
		if state, err := ReadDeleteListForWrite(ctx, s, testBucket, testProject, testProjectNumber); err == nil {
			t.Errorf("a read failing after %d good ones returned %+v and no error", skip, state)
		}
		s, f = newStorage(t)
		f.AddBucket(testBucket, testProjectNumber, testManaged)
		f.Put(testBucket, AllowDeleteObject, []byte(deleteListJSON))
	}
	s2, f2 := newStorage(t)
	f2.AddBucket(testBucket, testProjectNumber, testManaged)
	f2.Put(testBucket, AllowDeleteObject, []byte(`{"version":1,"gcp_project":"`+testProject+`","addresses":["a"],"note":"x"}`))
	state, err = ReadDeleteListForWrite(ctx, s2, testBucket, testProject, testProjectNumber)
	if err != nil || !state.Unreadable || state.Generation == 0 || state.Addresses != nil {
		t.Errorf("over an object it cannot believe: %+v, %v; want Unreadable with a generation", state, err)
	}
	f2.AddBucket(testBucket, testProjectNumber+1, testManaged)
	if _, err := ReadDeleteListForWrite(ctx, s2, testBucket, testProject, testProjectNumber); !errors.Is(err, ErrRefused) {
		t.Errorf("over another project's bucket: %v, want ErrRefused", err)
	}
}
