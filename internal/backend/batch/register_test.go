package batch

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/backendtest"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// TestTheTicketNamesTheJob is BR1's first half in this package: the name the
// launcher puts in its ticket ask IS the job the create makes. A launcher that
// named another job would be issued an identity for a job it does not run, and
// the run's claim would be served to no VM at all -- so the two sides are held
// together here, one derivation against the platform's own record of what it
// created.
//
// Mutation (build the name from another run id, another workflow, or the
// project's own display name): the name below is not the job the fake holds,
// which is the one inconsistency no later check can repair -- the ticket is
// already issued when the create returns.
func TestTheTicketNamesTheJob(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	s := spec()
	name := b.LaunchJobName(s.Repo.Slug, s.Workflow, s.RunID)
	if want := b.location() + "/jobs/" + JobID(s.Repo.Slug, s.Workflow, s.RunID); string(name) != want {
		t.Fatalf("LaunchJobName = %q, want %q", string(name), want)
	}
	if !name.Valid() {
		t.Fatalf("LaunchJobName = %q, which is not a name this contract carries (identitywire.JobName.Valid): a ticket ask naming it would be refused by the issuer", string(name))
	}
	ref, err := b.Launch(ctx, s)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	ids := bf.IDs()
	if len(ids) != 1 {
		t.Fatalf("the fake created %v, want the one job this launch asked about", ids)
	}
	if got := b.location() + "/jobs/" + ids[0]; string(name) != got {
		t.Fatalf("the ticket ask would name %q and the launch created %q: the run's identity would be issued for a job it does not run (R1)", string(name), got)
	}
	if ref.Job != ids[0] {
		t.Fatalf("the launch answers the job %q and created %q", ref.Job, ids[0])
	}
	// And a second derivation of the same run names the same job, which is what
	// makes the name safe to put in an ask that is made before the create.
	if again := b.LaunchJobName(s.Repo.Slug, s.Workflow, s.RunID); again != name {
		t.Fatalf("two derivations of one run name two jobs: %q and %q", string(name), string(again))
	}
}

// TestTheJobIsRegisteredWithItsOwnID: the second half of BR1's tie in this
// package. The id a launcher registers is the platform's own for the job it
// created -- the fake's uid -- and never the job's name, which a job deleted
// and created again under the same name reproduces (F30): a registration
// carrying the name would admit the second job's VM to the first run's
// identity.
//
// Mutation (answer the job's name, a digest of it, or a field of the job other
// than its uid): the assertion below holds the answer to the fake's own uid.
func TestTheJobIsRegisteredWithItsOwnID(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	s := spec()
	name := b.LaunchJobName(s.Repo.Slug, s.Workflow, s.RunID)
	if _, err := b.Launch(ctx, s); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	id, err := b.LaunchJobID(ctx, name)
	if err != nil {
		t.Fatalf("LaunchJobID of the job this launch just created: %v", err)
	}
	want, _ := bf.JobJSON(testProject, testRegion, JobID(s.Repo.Slug, s.Workflow, s.RunID))["uid"].(string)
	if want == "" {
		t.Fatal("the fake answers no uid for the job it created: this test cannot tell an id from a name")
	}
	if id != want {
		t.Fatalf("LaunchJobID = %q, want the platform's own id for the job (%q)", id, want)
	}
	if id == string(name) || strings.HasSuffix(id, JobID(s.Repo.Slug, s.Workflow, s.RunID)) {
		t.Fatalf("LaunchJobID = %q, which is the job's name: a job re-created under the same name reproduces a name and never an id (F30)", id)
	}

	// A job that does not exist, and a name this contract does not carry: both
	// are answered rather than guessed, because an id this launcher invented
	// would be an id no VM's attestation is checked against.
	if _, err := b.LaunchJobID(ctx, identitywire.JobName(b.location()+"/jobs/fugaro-nobody")); err == nil {
		t.Error("LaunchJobID answered an id for a job that does not exist")
	}
	if _, err := b.LaunchJobID(ctx, identitywire.JobName("acme/app")); err == nil {
		t.Error("LaunchJobID answered an id for something that is not a Cloud Batch job's name")
	}
}

// TestTheRegisteredIDFollowsAReCreatedJob is F30 read the other way round: the
// id a launcher reads back is the id of the job that is there NOW, which is
// what makes the registration a tie to an execution rather than to a name. A
// job deleted and created again under the same name is another job, and the
// read answers the other id.
func TestTheRegisteredIDFollowsAReCreatedJob(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	s := spec()
	name := b.LaunchJobName(s.Repo.Slug, s.Workflow, s.RunID)
	if _, err := b.Launch(ctx, s); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	first, err := b.LaunchJobID(ctx, name)
	if err != nil {
		t.Fatalf("LaunchJobID: %v", err)
	}
	bf.SetUID(JobID(s.Repo.Slug, s.Workflow, s.RunID), "uid-second-launcher")
	second, err := b.LaunchJobID(ctx, name)
	if err != nil {
		t.Fatalf("LaunchJobID after the job was replaced: %v", err)
	}
	if second == first {
		t.Fatalf("the read back answers %q for the job that replaced the first one: a re-created job's VM would be admitted to the first run's identity (D3108, F30)", second)
	}
	if second != "uid-second-launcher" {
		t.Fatalf("LaunchJobID = %q, want the id the job carries now", second)
	}
}

// TestTheJobNameIsThePlatformsShape: identitywire.JobName.Valid is a spelling
// check the issuer holds the ask to, so a backend that derived a name of
// another shape would have every Batch launch refused at its first call. The
// backend's own parts (the project, the region, the job id) are what it is
// built from, and this test is where a change to any of them meets the
// contract's rule rather than an operator's refusal.
func TestTheJobNameIsThePlatformsShape(t *testing.T) {
	b, _, _, _ := newTestBackend(t, nil)
	// The variant a review launch runs under is part of the job's own name
	// (JobID), so the shape holds for it too.
	for _, workflow := range []string{backendtest.Workflow, "web-review", "web-review-2"} {
		name := b.LaunchJobName(backendtest.Slug, workflow, "20260927-100000-abcd")
		if !name.Valid() {
			t.Errorf("LaunchJobName(%q) = %q, which is not a name identitywire.JobName carries", workflow, string(name))
		}
	}
}
