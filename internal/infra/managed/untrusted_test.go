package managed

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

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

// TestAllowDeleteReturnsTheOwnersAddressesUntouched: the bound is on what this
// package prints and never on what the gate compares — an address is admitted
// or refused by its exact text, and a sanitised copy would admit an address the
// owner never wrote.
func TestAllowDeleteReturnsTheOwnersAddressesUntouched(t *testing.T) {
	list := `{"version":1,"gcp_project":"` + testProject + `","addresses":["google_storage_bucket.a\u001b[31m","module.m.google_storage_bucket.b"]}`
	s, bucket := seedDeleteList(t, list)
	got, err := AllowDelete(context.Background(), s, bucket, testProject, testProjectNumber)
	if err != nil {
		t.Fatalf("AllowDelete: %v", err)
	}
	want := []string{"google_storage_bucket.a\u001b[31m", "module.m.google_storage_bucket.b"}
	if len(got) != len(want) {
		t.Fatalf("AllowDelete = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("address %d = %q, want the owner's own text %q", i, got[i], want[i])
		}
	}
}
