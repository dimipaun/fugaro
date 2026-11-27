package cli

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// This file is `fugaro image prune`'s tests (design
// docs/forge-authorship-images/shared-images.md §9, §10): the two conditions
// of eligibility, the delete order, --dry-run, and the per-repository image
// the migration leaves behind.

// keyA and keyB are two dependency sets' keys: the full hex a lock entry
// carries, whose first 12 characters are the image's own name (§2.6).
var (
	keyA = strings.Repeat("a", 64)
	keyB = strings.Repeat("b", 64)
)

// sharedImageRef is the reference a key's shared image has: the
// installation's Artifact Registry, whose host is what a delete requires.
func sharedImageRef(key string) string {
	return "us-east5-docker.pkg.dev/proj-1234/fugaro-shared-0123456789ab/img-" + shortHash(key)
}

// lockedImage is one hand-written lock entry: the state a refresh leaves
// behind, written by hand so prune's two conditions are judged without one.
// lastUsed is both the entry's age and, an hour earlier, its build.
func lockedImage(key string, lastUsed time.Time, jobs map[string]string) imagecheck.LockedImage {
	return imagecheck.LockedImage{
		Key: key, Image: sharedImageRef(key), Digest: "sha256:" + strings.Repeat("ab", 32),
		Repositories: slices.Sorted(maps.Keys(jobs)), Jobs: jobs,
		BuiltAt: lastUsed.Add(-time.Hour), LastUsedAt: lastUsed,
	}
}

// writeSharedLock writes the lock the fixture's installation holds.
func writeSharedLock(t *testing.T, f *cloudFixture, images map[string]imagecheck.LockedImage) {
	t.Helper()
	data, err := json.Marshal(imagecheck.Lock{Version: imagecheck.LockVersion, Images: images})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, imagecheck.LockKey, string(data))
}

// imageRegistryFake starts a registry fake holding each entry's image at the
// digest the entry names, and returns it with the client that reads it: the
// command's own seam (pruneRegistry, doctorRegistry) is what a caller installs
// it into, since prune's delete and doctor's read share the fake.
func imageRegistryFake(t *testing.T, images ...imagecheck.LockedImage) (*gcpfake.Registry, *imagecheck.Registry) {
	t.Helper()
	reg := gcpfake.NewRegistry(t)
	for _, img := range images {
		name, ok := strings.CutPrefix(img.Image, "us-east5-docker.pkg.dev/")
		if !ok {
			t.Fatalf("the image %q is not on the fake's host", img.Image)
		}
		reg.SetManifest(name, img.Digest, img.Digest)
	}
	client := &imagecheck.Registry{Google: reg.Client(), Endpoint: func(string) string { return reg.URL }}
	return reg, client
}

// pruneRig points `image prune`'s registry at a fake and returns it, with
// each image of the lock registered as a manifest the registry holds, so a
// delete of one is a delete the fake answers 202 to.
func pruneRig(t *testing.T, images ...imagecheck.LockedImage) *gcpfake.Registry {
	t.Helper()
	reg, client := imageRegistryFake(t, images...)
	old := pruneRegistry
	pruneRegistry = func() *imagecheck.Registry { return client }
	t.Cleanup(func() { pruneRegistry = old })
	return reg
}

// TestPruneKeepsAnImageAJobPointsAt: an entry a job points at is never
// eligible, whatever its age, while an entry nothing points at and nothing
// has used for images.retention_days is removed (§9's two conditions). The
// registry image goes first and the lock entry second.
func TestPruneKeepsAnImageAJobPointsAt(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now().UTC()
	pinned := lockedImage(keyA, now.AddDate(0, 0, -400), map[string]string{"acme/app": "web"})
	unused := lockedImage(keyB, now.AddDate(0, 0, -40), nil)
	writeSharedLock(t, f, map[string]imagecheck.LockedImage{keyA: pinned, keyB: unused})
	reg := pruneRig(t, pinned, unused)

	out, _, err := execute(t, "image", "prune")
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Deleted(); len(got) != 1 || !strings.Contains(got[0], "img-"+shortHash(keyB)) {
		t.Errorf("the registry deletes = %v, want only the image no job points at (%s)", got, sharedImageRef(keyB))
	}
	lock := readLockOf(t, f)
	if _, ok := lock.Images[keyA]; !ok {
		t.Error("the pinned image's lock entry is gone: a job still points at it")
	}
	if _, ok := lock.Images[keyB]; ok {
		t.Error("the unused image's lock entry is still there")
	}
	if !strings.Contains(out, sharedImageRef(keyB)) {
		t.Errorf("the output does not name the image it removed:\n%s", out)
	}
	if strings.Contains(out, sharedImageRef(keyA)) {
		t.Errorf("the output names the pinned image:\n%s", out)
	}
}

// TestPruneRemovesImagesUnusedForRetentionDays: the age is
// images.retention_days, so an entry inside it is kept even with no job on
// it, and --dry-run prints the list and removes nothing.
func TestPruneRemovesImagesUnusedForRetentionDays(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now().UTC()
	old := lockedImage(keyA, now.AddDate(0, 0, -31), nil)
	fresh := lockedImage(keyB, now.AddDate(0, 0, -29), nil)
	writeSharedLock(t, f, map[string]imagecheck.LockedImage{keyA: old, keyB: fresh})
	reg := pruneRig(t, old, fresh)

	out, _, err := execute(t, "image", "prune", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Deleted(); len(got) != 0 {
		t.Errorf("--dry-run deleted %v", got)
	}
	if lock := readLockOf(t, f); len(lock.Images) != 2 {
		t.Errorf("--dry-run left %d entries, want both", len(lock.Images))
	}
	if !strings.Contains(out, sharedImageRef(keyA)) || strings.Contains(out, sharedImageRef(keyB)) {
		t.Errorf("--dry-run's list is not the one entry older than the retention:\n%s", out)
	}
	if !strings.Contains(out, "nothing was removed") {
		t.Errorf("--dry-run does not say nothing was removed:\n%s", out)
	}

	out, _, err = execute(t, "image", "prune")
	if err != nil {
		t.Fatal(err)
	}
	lock := readLockOf(t, f)
	if _, ok := lock.Images[keyA]; ok {
		t.Error("the entry older than the retention is still in the lock")
	}
	if _, ok := lock.Images[keyB]; !ok {
		t.Error("the entry inside the retention was removed")
	}
	if !strings.Contains(out, "1 image removed") {
		t.Errorf("the removal line = %q", out)
	}
}

// TestPruneNothingToPrune: an installation with nothing eligible removes
// nothing, calls no registry and says so.
func TestPruneNothingToPrune(t *testing.T) {
	f := newCloudFixture(t)
	pinned := lockedImage(keyA, time.Now().UTC().AddDate(0, 0, -400), map[string]string{"acme/app": "web"})
	writeSharedLock(t, f, map[string]imagecheck.LockedImage{keyA: pinned})
	reg := pruneRig(t, pinned)

	out, _, err := execute(t, "image", "prune")
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Deleted(); len(got) != 0 {
		t.Errorf("the registry deletes = %v, want none", got)
	}
	if !strings.Contains(out, "nothing to prune") {
		t.Errorf("output = %q, want the nothing-to-prune line", out)
	}
}

// TestPruneRemovesTheImageAMigratedRepositoryLeftBehind: §10's second clause.
// A repository whose job is on a shared image stops building its own, and the
// image its build record still names is removed once the build is older than
// the retention — the registry manifest first, the record second.
func TestPruneRemovesTheImageAMigratedRepositoryLeftBehind(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now().UTC()
	own := "us-east5-docker.pkg.dev/proj-1234/fugaro-acme-app/web"
	rec := imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "web", Image: own,
		ImageDigest: "sha256:" + strings.Repeat("cd", 32), BuiltAt: now.AddDate(0, 0, -40)}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, imagecheck.RecordKey(appSlug, "web"), string(data))
	shared := lockedImage(keyA, now.AddDate(0, 0, -40), map[string]string{"acme/app": "web"})
	writeSharedLock(t, f, map[string]imagecheck.LockedImage{keyA: shared})
	reg := pruneRig(t, shared)
	reg.SetManifest("proj-1234/fugaro-acme-app/web", rec.ImageDigest, rec.ImageDigest)

	if _, _, err := execute(t, "image", "prune"); err != nil {
		t.Fatal(err)
	}
	if got := reg.Deleted(); len(got) != 1 || !strings.Contains(got[0], "fugaro-acme-app/web") {
		t.Errorf("the registry deletes = %v, want the repository's own old image %s", got, own)
	}
	if rec := readRecordOf(t, f); rec != nil {
		t.Errorf("the build record is still there: %+v", rec)
	}
}

// recordBucketOf is the fixture's runs bucket, opened as the record bucket
// the commands read and write it through.
func recordBucketOf(t *testing.T, f *cloudFixture) *blobx.Bucket {
	t.Helper()
	b, err := blobx.Open(t.Context(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// readLockOf reads the fixture's lock as the command left it.
func readLockOf(t *testing.T, f *cloudFixture) *imagecheck.Lock {
	t.Helper()
	lock, err := imagecheck.ReadLock(t.Context(), recordBucketOf(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if lock == nil {
		t.Fatal("the lock is gone")
	}
	return lock
}

// readRecordOf reads one build record of the fixture, nil when it is gone.
func readRecordOf(t *testing.T, f *cloudFixture) *imagecheck.Record {
	t.Helper()
	rec, err := readStoredBuildRecord(t.Context(), recordBucketOf(t, f), imagecheck.RecordKey(appSlug, "web"))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}
