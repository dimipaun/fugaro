package batch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// TestTheJobIsRegisteredAfterTheCreateWithItsId is BR1's second call at the
// backend (R1, D3108): the id the launcher writes on the run's issuance record
// through BR17's route is the one the PLATFORM gave the job the launch created,
// read back after the create returned -- not a name, not a derivation, and not
// something this package could invent.
//
// The fake assigns each create an id of its own, so the assertion is that the
// value the launcher would register is the job's own: change the derivation
// here (return the job's name, hash it, or answer a constant) and the two stop
// agreeing.
func TestTheJobIsRegisteredAfterTheCreateWithItsId(t *testing.T) {
	ctx := context.Background()
	b, _, _, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	full := identitywire.JobName(ref.Name)
	if !full.Valid() {
		t.Fatalf("the launch answered %q as the job's name, which is not a Cloud Batch job's full resource name: the ticket ask, the registration and the run's claim all name the job this way", ref.Name)
	}
	id, err := b.CreatedJobID(ctx, full)
	if err != nil {
		t.Fatalf("CreatedJobID: %v", err)
	}
	if id == "" {
		t.Fatal("CreatedJobID answered no id: a record written with none is a run that can never claim its identity (D3108)")
	}
	// The id is the platform's and not the name's: a registration that carried
	// the name where the id belongs would tie the claim to nothing.
	if id == string(full) || strings.Contains(string(full), id) {
		t.Errorf("CreatedJobID answered %q, which is the job's name and not its id: what ties a VM to an execution is the id Batch assigns", id)
	}
	// A second read of the same job answers the same id: the value is the
	// job's, and BR17's record is written once from it.
	again, err := b.CreatedJobID(ctx, full)
	if err != nil {
		t.Fatalf("CreatedJobID (second read): %v", err)
	}
	if again != id {
		t.Errorf("two reads of %s answered %q and %q: the id is the job's own", full, id, again)
	}

	// A job the platform does not hold is an error and never an answer: a
	// registration would write it, and the run would stop at its claim with
	// nothing an operator could read.
	missing, err := b.CreatedJobID(ctx, identitywire.JobName("projects/"+testProject+"/locations/"+testRegion+"/jobs/fugaro-nobody-asks-for-this"))
	if err == nil {
		t.Fatalf("CreatedJobID of a job Batch does not hold = %q, want an error", missing)
	}
	if errors.Is(err, backend.ErrRejected) {
		t.Errorf("a read of an absent job is not a refusal of the launch: %v", err)
	}

	// A name no Batch job can have is refused before an API call is made for
	// it: the launcher's own derivation is what would be wrong, and a request
	// for it would be the wrong error to read.
	for _, bad := range []identitywire.JobName{
		"",
		"fugaro-acme-app-web-2dc53d2b",
		"projects/" + testProject + "/jobs/fugaro-acme-app-web",
	} {
		if _, err := b.CreatedJobID(ctx, bad); err == nil {
			t.Errorf("CreatedJobID(%q) = nil error, want a refusal: it is not a job's full resource name", bad)
		}
	}
}
