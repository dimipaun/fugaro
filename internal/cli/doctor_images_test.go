package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// This file is doctor's read of the shared-image lock's digests (design
// docs/forge-authorship-images/shared-images.md §9): an entry whose digest the
// registry no longer has, which a crash between prune's two deletes leaves
// behind, and the read that could not answer at all.

// rigDoctorRegistry points doctor's registry read at a fake holding every
// digest the given entries name, and returns it: a test about another lock
// question writes its entries here so the digest read finds what the lock
// says, and a test about the digest read itself registers nothing, so every
// entry is one the registry has lost.
func rigDoctorRegistry(t *testing.T, images ...imagecheck.LockedImage) *gcpfake.Registry {
	t.Helper()
	reg, client := imageRegistryFake(t, images...)
	old := doctorRegistry
	doctorRegistry = func() *imagecheck.Registry { return client }
	t.Cleanup(func() { doctorRegistry = old })
	return reg
}

// TestDoctorReportsALockEntryTheRegistryLost: §9's crash — the registry image
// is gone and the lock entry is not — is one warning naming the entry, whose
// remedy is the prune that finishes the removal. It is a warning and not a
// measurement: a job on that digest cannot start.
//
// The line is short by the same rule the owner's ruling states: a reader gets
// the entry's short form, which is the HASH column `fugaro image ls` prints,
// and nowhere near a paragraph.
func TestDoctorReportsALockEntryTheRegistryLost(t *testing.T) {
	r := newDoctorRig(t)
	noProjectLayerBucket(t)
	runs := rigSharedBucket(t, r)
	key := strings.Repeat("a", 64)
	lost := lockedImage(key, time.Date(2026, 11, 2, 1, 5, 0, 0, time.UTC), nil)
	writeRigLock(t, runs, map[string]imagecheck.LockedImage{key: lost})
	// The fake holds nothing: the digest the lock names is the one a crash
	// left behind, and the read answers "no such manifest" for it.
	rigDoctorRegistry(t)

	o := doctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, "shared-image-missing-"+shortHash(key))
	if !ok {
		t.Fatalf("no check for the lock entry the registry lost: %+v", o.Checks)
	}
	if c.OK || c.Severity != "warning" {
		t.Errorf("the check = %+v, want a warning that fails --strict", c)
	}
	if !strings.Contains(c.Problem, shortHash(key)) {
		t.Errorf("the line does not name the entry: %q", c.Problem)
	}
	if !strings.Contains(c.Problem, shortDigest(lost.Digest)) {
		t.Errorf("the line does not name the digest that is gone: %q", c.Problem)
	}
	if n := len(c.Problem); n > 140 {
		t.Errorf("the line is %d characters, and the ruling asks for under about 140: %q", n, c.Problem)
	}
	if !strings.Contains(c.Fix, "fugaro image prune") {
		t.Errorf("the fix does not name the prune that finishes the removal: %q", c.Fix)
	}
	if got := doctorClassOf(c); got != doctorClassActionable {
		t.Errorf("the check is classed %q, want the default view to print it", got)
	}
	// The default view prints the warning, which is the whole point of the
	// ruling: a person sees the entry without --all.
	human, _, err := execute(t, "doctor", "--dir", t.TempDir())
	if err != nil && ExitCode(err) != ExitUserError {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(human, shortHash(key)) {
		t.Errorf("the default view does not print the warning:\n%s", human)
	}
}

// TestDoctorIsQuietWhenTheRegistryStillHasTheDigest: the ordinary
// installation — every lock entry's digest is still there — gets no line at
// all, and the read is made once per entry rather than once per run.
func TestDoctorIsQuietWhenTheRegistryStillHasTheDigest(t *testing.T) {
	r := newDoctorRig(t)
	noProjectLayerBucket(t)
	runs := rigSharedBucket(t, r)
	key := strings.Repeat("a", 64)
	held := lockedImage(key, time.Date(2026, 11, 2, 1, 5, 0, 0, time.UTC), nil)
	writeRigLock(t, runs, map[string]imagecheck.LockedImage{key: held})
	rigDoctorRegistry(t, held)

	for _, c := range doctorJSON(t).Checks {
		if strings.HasPrefix(c.ID, "shared-image-missing-") || c.ID == "shared-image-unread" {
			t.Errorf("doctor reported %+v about an installation whose digests are all there", c)
		}
	}
}

// TestDoctorNeverPassesARegistryItCouldNotRead: a registry read that failed is
// not a digest that is gone. It is one line naming the entries nobody could
// read, marked unread so doctorClassOf never files it as a measurement, and it
// is an info line: the registry answering 500 is not a fact about this
// installation, and --strict fails on findings, not on a read that did not
// answer.
func TestDoctorNeverPassesARegistryItCouldNotRead(t *testing.T) {
	r := newDoctorRig(t)
	noProjectLayerBucket(t)
	runs := rigSharedBucket(t, r)
	key := strings.Repeat("a", 64)
	held := lockedImage(key, time.Date(2026, 11, 2, 1, 5, 0, 0, time.UTC), nil)
	writeRigLock(t, runs, map[string]imagecheck.LockedImage{key: held})
	reg := rigDoctorRegistry(t, held)
	reg.Refuse(500, "INTERNAL", "internalError", "the registry is having a bad day")

	o := doctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, "shared-image-unread")
	if !ok {
		t.Fatalf("no check for the registry read that failed: %+v", o.Checks)
	}
	if c.OK {
		t.Error("the check reports ok: a read that did not answer is not a pass")
	}
	if c.Severity != "info" {
		t.Errorf("the check's severity = %q, want info: a read that failed is not a finding about this installation", c.Severity)
	}
	if !strings.Contains(c.Problem, shortHash(key)) {
		t.Errorf("the line does not name the entry it could not read: %q", c.Problem)
	}
	if got := doctorClassOf(c); got != doctorClassAudit {
		t.Errorf("the check is classed %q, want %q: the marker that says this read answered nothing is what keeps an unknown out of the measurement class", got, doctorClassAudit)
	}
	if checksFail([]doctorCheck{c}, true) {
		t.Error("a registry that did not answer fails doctor --strict: the fact is unknown, and unknown is not wrong")
	}
}
