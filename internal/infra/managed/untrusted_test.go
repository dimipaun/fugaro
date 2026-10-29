package managed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The two boundaries this file is about are the reads (IsManaged,
// ReadBudgetAnswer) and the writes and the guard (WriteMarker, GuardAllowList)
// of this package: each answers with a sentence a caller prints and with an
// error, and both carry text that is not this program's — a project name out of
// a configuration, a bucket name, the content of the object, and an API's own
// words about a request that failed (a 5xx body, a proxy's page). Every one of
// them is bounded at the boundary (safetext.Field, safetext.FieldErr) so that
// no caller has to remember, and this file feeds each boundary the four classes
// of hostile text and reads the answer back.
//
// The values are the same four internal/managed's refusals are tested with: an
// escape sequence, a bidi override that rewrites what the line says, a newline
// that would forge a line of this program's output, and ten thousand characters
// that push the sentence off the screen.

func hostileValues() map[string]string {
	return map[string]string{
		"C1 CSI":        string(rune(0x9b)) + "31mred",
		"bidi override": "a" + string(rune(0x202e)) + "gnp.exe",
		"newline":       "backend error\n  and a forged line",
		"10k":           strings.Repeat("x", 10000),
	}
}

// bounded fails when s carries a C1 control, a bidi override, a C0 control but
// a newline, more than lines lines, or the 10k value unbounded.
func bounded(t *testing.T, what, s string, lines int) {
	t.Helper()
	for _, r := range s {
		if (r >= 0x80 && r <= 0x9f) || r == 0x202e || (r < 0x20 && r != '\n') {
			t.Errorf("%s carries %U:\n%q", what, r, s)
		}
	}
	if n := strings.Count(s, "\n") + 1; n > lines {
		t.Errorf("%s is %d lines, want at most %d:\n%q", what, n, lines, s)
	}
	if strings.Contains(s, strings.Repeat("x", 600)) {
		t.Errorf("%s carries the 10k value unbounded (%d bytes)", what, len(s))
	}
}

// hostileListRead is a managed world whose object read fails with a body that
// is not one line: the one place a control character really arrives here (a 5xx
// body, or a proxy's), and the row the bound is proved on.
func hostileListRead(t *testing.T, body string) (*storage.Service, *crm.Service) {
	t.Helper()
	s, f, c, _ := managedWorld(t)
	f.Put(testBucket, MarkerObject, []byte(markerJSON))
	f.RefusePath(MarkerObject, http.StatusInternalServerError, "INTERNAL", "", body)
	return s, c
}

// TestTheReadsReasonAndItsErrorAreBounded: the reason a caller prints and the
// error beside it, from both reads of the same fact.
func TestTheReadsReasonAndItsErrorAreBounded(t *testing.T) {
	ctx := context.Background()
	for name, v := range hostileValues() {
		t.Run(name, func(t *testing.T) {
			s, c := hostileListRead(t, "backend error"+v)
			ok, reason, err := IsManaged(ctx, s, c, testFugaroProject, testProject)
			if ok {
				t.Fatal("a marker that reads as managed turns the confirmations off")
			}
			if err == nil {
				t.Fatal("the API did not answer, and the caller was not told the read failed")
			}
			bounded(t, name+" in IsManaged's reason", reason, 1)
			bounded(t, name+" in IsManaged's error", err.Error(), 1)
			// The chain survives the bound: a caller that reads the API's own
			// error out of it (the status, the reason) still can.
			var ge *googleapi.Error
			if !errors.As(err, &ge) || ge.Code != http.StatusInternalServerError {
				t.Errorf("the API's error is no longer readable through the bound: %v", err)
			}

			s, c = hostileListRead(t, "backend error"+v)
			a, err := ReadBudgetAnswer(ctx, s, c, testFugaroProject, testProject)
			if err == nil {
				t.Fatal("the API did not answer, and the budget read was not told")
			}
			bounded(t, name+" in ReadBudgetAnswer's reason", a.Reason, 1)
			bounded(t, name+" in ReadBudgetAnswer's error", err.Error(), 1)
		})
	}
}

// TestTheWritesRefusalIsBounded: the marker write's own refusal, which names
// the project it was asked to mark and the project the run created — both out
// of the caller's configuration, and one of them is repeated.
func TestTheWritesRefusalIsBounded(t *testing.T) {
	for name, v := range hostileValues() {
		t.Run(name, func(t *testing.T) {
			s, _, _, _ := managedWorld(t)
			err := WriteMarker(context.Background(), s, testBucket, v, testFugaroProject, testProjectNumber, nil)
			if err == nil {
				t.Fatal("a marker for a project this run did not create was written")
			}
			bounded(t, name+" in WriteMarker's refusal", err.Error(), 1)
		})
	}
	// And the caller's own name in the bucket mismatch, which the write reads
	// out of the cloud before it refuses.
	s, f, _, _ := managedWorld(t)
	f.AddBucket(testBucket, testProjectNumber+1, testManaged)
	err := WriteMarker(context.Background(), s, testBucket, testFugaroProject, testFugaroProject, testProjectNumber, nil)
	if err == nil {
		t.Fatal("a bucket that is not the project's own was believed")
	}
	bounded(t, "the bucket mismatch's refusal", err.Error(), 1)
}

// TestTheWritersReadErrorIsBounded: the writer's read of the owner's list is
// strict — a read that fails is an error — and that error names the bucket, the
// project the run is for and an API's own words about a request that failed,
// all three of which the command prints. The state beside it is not bounded:
// the read hands the caller the owner's addresses exactly, so an approval is
// compared against the owner's own text and a cut copy never approves nothing.
func TestTheWritersReadErrorIsBounded(t *testing.T) {
	ctx := context.Background()
	for name, v := range hostileValues() {
		t.Run(name+" in the read's error", func(t *testing.T) {
			// The bucket is proved first, so the object read is the one that
			// fails: the API's 5xx body is what arrives in the error.
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			f.RefusePath(AllowDeleteObject, http.StatusInternalServerError, "INTERNAL", "", "backend error"+v)
			st, err := ReadDeleteListForWrite(ctx, s, testBucket, testProject, testProjectNumber)
			if err == nil {
				t.Fatalf("the API did not answer, and the writer's read was not told: %+v", st)
			}
			bounded(t, name+" in ReadDeleteListForWrite's error", err.Error(), 1)
		})
		t.Run(name+" in the bucket's name", func(t *testing.T) {
			// The same sentence names the bucket, out of the caller's
			// configuration, and the bucket read is what fails here.
			s, _ := newStorage(t)
			_, err := ReadDeleteListForWrite(ctx, s, v, testProject, testProjectNumber)
			if err == nil {
				t.Fatal("a bucket that is not the project's own was read")
			}
			bounded(t, name+" in ReadDeleteListForWrite's error", err.Error(), 1)
		})
		t.Run(name+" in the project", func(t *testing.T) {
			// And the project, which the read names before it reads anything.
			_, err := ReadDeleteListForWrite(ctx, nil, testBucket, v, testProjectNumber)
			if err == nil {
				t.Fatal("a read with no storage client was made")
			}
			bounded(t, name+" in ReadDeleteListForWrite's error", err.Error(), 1)
		})
	}
	// The state is the owner's own text: the bound is on the error alone.
	// Both halves of an entry are the owner's — the address and the identity —
	// and both are compared to a plan's by exact equality.
	list := `{"version":2,"gcp_project":"` + testProject + `","entries":[{"address":"google_storage_bucket.a\u001b[31m","before":"type=google_storage_bucket id=a\u001b[31m"}]}`
	s, bucket := seedDeleteList(t, list)
	st, err := ReadDeleteListForWrite(ctx, s, bucket, testProject, testProjectNumber)
	if err != nil {
		t.Fatalf("ReadDeleteListForWrite: %v", err)
	}
	want := tf.DeleteEntry{Address: "google_storage_bucket.a\u001b[31m", Before: "type=google_storage_bucket id=a\u001b[31m"}
	if len(st.Entries) != 1 || st.Entries[0] != want {
		t.Errorf("the writer's read = %q, want the owner's own entry %q", st.Entries, want)
	}
}

// TestTheGuardsRefusalIsBounded: the addresses an applying command's own
// command line named — the one argument a coding agent writes — are repeated
// back in the refusal, and the refusal is what its reader acts on.
func TestTheGuardsRefusalIsBounded(t *testing.T) {
	for name, v := range hostileValues() {
		t.Run(name, func(t *testing.T) {
			s, bucket := seedDeleteList(t, "")
			_, err := GuardAllowList(context.Background(), s, bucket, testProject, testProjectNumber, []string{v + "\n  and a forged line"})
			if err == nil {
				t.Fatal("an address the applying command passed was admitted")
			}
			bounded(t, name+" in the guard's refusal", err.Error(), 1)
			if want := safetext.Field(v + "\n  and a forged line"); !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not carry the address it refused, bounded (%q):\n%q", want, err.Error())
			}
		})
		t.Run(name+" in the bucket's name", func(t *testing.T) {
			// The bucket is named in the same sentence and read out of a
			// configuration (or the marker): it is bounded too, and the refusal
			// is reached before anything is read.
			_, err := GuardAllowList(context.Background(), nil, v, testProject, testProjectNumber, []string{"google_storage_bucket.a"})
			if err == nil {
				t.Fatal("an address the applying command passed was admitted")
			}
			bounded(t, name+" in the guard's refusal", err.Error(), 1)
		})
	}
}

// TestAllowDeleteReturnsTheOwnersEntriesUntouched: the bound is on what this
// package prints and never on what the gate compares — an entry is admitted or
// refused by the exact text of its two halves, and a sanitised copy would
// approve an object the owner never approved.
func TestAllowDeleteReturnsTheOwnersEntriesUntouched(t *testing.T) {
	list := `{"version":2,"gcp_project":"` + testProject + `","entries":[` +
		`{"address":"google_storage_bucket.a\u001b[31m","before":"type=google_storage_bucket id=a\u001b[31m"},` +
		`{"address":"module.m.google_storage_bucket.b","before":"type=google_storage_bucket id=b"}]}`
	s, bucket := seedDeleteList(t, list)
	got, err := AllowDelete(context.Background(), s, bucket, testProject, testProjectNumber)
	if err != nil {
		t.Fatalf("AllowDelete: %v", err)
	}
	want := []tf.DeleteEntry{
		{Address: "google_storage_bucket.a\u001b[31m", Before: "type=google_storage_bucket id=a\u001b[31m"},
		{Address: "module.m.google_storage_bucket.b", Before: "type=google_storage_bucket id=b"},
	}
	if len(got) != len(want) {
		t.Fatalf("AllowDelete = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("address %d = %q, want the owner's own text %q", i, got[i], want[i])
		}
	}
}

// TestTheFlagsWriteErrorIsBounded: WriteFlag is the owner-run flag's writer
// (Task 6.1), and like WriteMarker its error carries the bucket's name, the
// project and an API's own words about a read that failed — the marker read it
// makes before it writes — all of which `fugaro managed flag` prints. The bound
// is on the message alone: ErrRefused is still matched through it.
func TestTheFlagsWriteErrorIsBounded(t *testing.T) {
	ctx := context.Background()
	budget := Budget{Amount: 50, Currency: "USD"}
	for name, v := range hostileValues() {
		t.Run(name+" in the read's error", func(t *testing.T) {
			s, f := newStorage(t)
			f.AddBucket(testBucket, testProjectNumber, testManaged)
			f.RefusePath(MarkerObject, http.StatusInternalServerError, "INTERNAL", "", "backend error"+v)
			_, err := WriteFlag(ctx, s, testBucket, testProject, testProjectNumber, budget)
			if err == nil {
				t.Fatal("the marker read failed, and the flag was written anyway")
			}
			bounded(t, name+" in WriteFlag's error", err.Error(), 1)
		})
		t.Run(name+" in the project", func(t *testing.T) {
			_, err := WriteFlag(ctx, nil, testBucket, v, testProjectNumber, budget)
			if err == nil {
				t.Fatal("a flag with no storage client was written")
			}
			if !errors.Is(err, ErrRefused) {
				t.Errorf("the bound hid the refusal's identity: %v", err)
			}
			bounded(t, name+" in WriteFlag's refusal", err.Error(), 1)
		})
	}
}

// TestTheLabelsWritersErrorsAreBounded: WriteManagedLabel (the creating run,
// Task 3.0) and WriteManagedLabelForFlag (the owner's flag, Task 6.1) are the
// two hands a project's label is written by, and both end in the same write
// WriteMarker's does. Their errors carry text that is not this program's — the
// project name out of a configuration and the words of a Resource Manager
// answer about a request that failed — and the caller prints them: `fugaro
// update` puts the warning in its output and in res.Warnings on exactly the
// branch where the label write failed, and `fugaro managed flag` prints its
// own. So both are bounded like every sibling writer, and the bound is on the
// message alone.
func TestTheLabelsWritersErrorsAreBounded(t *testing.T) {
	ctx := context.Background()
	for name, v := range hostileValues() {
		t.Run(name+" in the creation write's refusal", func(t *testing.T) {
			// The refusal names the project it was asked to label and the one
			// the run created; the first is the caller's own value.
			err := WriteManagedLabel(ctx, nil, testFugaroProject, v+"\n  and a forged line")
			if err == nil {
				t.Fatal("a label for a project this run did not create was written")
			}
			bounded(t, name+" in WriteManagedLabel's refusal", err.Error(), 1)
		})
		t.Run(name+" in the flag's write error", func(t *testing.T) {
			// The flag's gate is the command's, so this is reached with the
			// operator's value as the project: the read names it and the API's
			// 5xx body is what arrives in the error.
			c, cf := newCRM(t)
			cf.AddProject(testProject, testProjectNumber)
			cf.FailGet(testProject, http.StatusInternalServerError, "INTERNAL", "backend error"+v)
			err := WriteManagedLabelForFlag(ctx, c, testProject)
			if err == nil {
				t.Fatal("the project read failed, and the label was written anyway")
			}
			bounded(t, name+" in WriteManagedLabelForFlag's error", err.Error(), 1)
		})
		t.Run(name+" in the label write's project", func(t *testing.T) {
			// The same write with no client at all: the sentence names the
			// project before anything is read, and that name is the caller's.
			err := WriteManagedLabel(ctx, nil, v, v)
			if err == nil {
				t.Fatal("a label with no Resource Manager client was written")
			}
			bounded(t, name+" in WriteManagedLabel's refusal", err.Error(), 1)
		})
	}
	// And the API's own words are what reaches the sentence, not the caller's:
	// the failing read above carries the hostile value in its body too.
	c, cf := newCRM(t)
	cf.AddProject(testProject, testProjectNumber)
	cf.FailGet(testProject, http.StatusInternalServerError, "INTERNAL", "backend error\n  and a forged line")
	err := WriteManagedLabel(ctx, c, testProject, testProject)
	if err == nil {
		t.Fatal("the project read failed, and the label was written anyway")
	}
	bounded(t, "the API's body in WriteManagedLabel's error", err.Error(), 1)
	// The bound is on the message: the typed error inside it is still reachable.
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		t.Errorf("the bound hid the API error: %v", err)
	}
}

// TestTheGuardsRefusalCapsItsListing: the addresses a command line named are
// as many as the session passed, so the refusal that repeats them (twice) is
// capped at safetext.FieldListMax and says how many it left out — and never
// leaves out the fact that it left some out.
func TestTheGuardsRefusalCapsItsListing(t *testing.T) {
	total := safetext.FieldListMax + 5
	applied := make([]string, 0, total)
	for i := 0; i < total; i++ {
		applied = append(applied, fmt.Sprintf("google_storage_bucket.a%02d", i))
	}
	_, err := GuardAllowList(context.Background(), nil, testBucket, testProject, testProjectNumber, applied)
	if err == nil {
		t.Fatal("addresses the applying command passed were admitted")
	}
	msg := err.Error()
	if got := strings.Count(msg, safetext.FieldListNote(5)); got != 2 {
		t.Errorf("the refusal says %d time(s) that it left 5 out, want 2 (once per listing):\n%s", got, msg)
	}
	if last := applied[total-1]; strings.Contains(msg, last) {
		t.Errorf("the refusal carries %q, past the cap", last)
	}
	if edge := applied[safetext.FieldListMax-1]; !strings.Contains(msg, edge) {
		t.Errorf("the refusal leaves out %q, the last entry inside the cap", edge)
	}
	if edge := applied[safetext.FieldListMax]; strings.Contains(msg, edge) {
		t.Errorf("the refusal carries %q, the first entry past the cap", edge)
	}
}
