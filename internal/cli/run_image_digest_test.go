package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// recordDigest and liveDigest are the two digests C4's warning is about: the
// one a build recorded as the digest it promoted latest to, and the one the
// registry's latest resolves to now.
var (
	recordDigest = "sha256:" + strings.Repeat("3", 64)
	liveDigest   = "sha256:" + strings.Repeat("4", 64)
)

// digestImage is the untagged image a fixture's build record names: one in
// the installation's own Artifact Registry, as a derived image is.
const digestImage = "europe-docker.pkg.dev/proj-1234/fugaro/acme-app-web"

// digestRegistryName is the registry name digestImage's latest tag is read under
// (parseImageRef's host/name split).
const digestRegistryName = "proj-1234/fugaro/acme-app-web"

// hookDigestRegistry points the launcher's digest reads at a registry fake.
func hookDigestRegistry(t *testing.T) *gcpfake.Registry {
	t.Helper()
	reg := gcpfake.NewRegistry(t)
	old := checkRegistry
	t.Cleanup(func() { checkRegistry = old })
	checkRegistry = func() *imagecheck.Registry {
		return &imagecheck.Registry{HTTP: &http.Client{}, Google: &http.Client{}, Endpoint: func(string) string { return reg.URL }}
	}
	return reg
}

// writeDigestBuildRecord writes the build record of slug's workflow as the
// build's record step does, naming the image and the digest it promoted. The
// base ref is a current release, so the launch's other image gates judge the
// record as one they have nothing to say about.
func writeDigestBuildRecord(t *testing.T, f *cloudFixture, slug, workflow, image, digest string) {
	t.Helper()
	data, err := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: workflow, BaseRef: release0120, FugaroVersion: "0.12.0", Image: image, ImageDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, imagecheck.RecordKey(slug, workflow), string(data))
}

// TestRunDigestWarnsOnMismatch: a launch whose workflow's image is no longer
// at the digest its build record names warns once and starts anyway. Nothing
// is pinned and no job is updated (D619: Cloud Run cannot vary the image per
// execution), so the mismatch is told to the operator and nothing else.
func TestRunDigestWarnsOnMismatch(t *testing.T) {
	f := newCloudFixture(t)
	reg := hookDigestRegistry(t)
	writeDigestBuildRecord(t, f, appSlug, "web", digestImage, recordDigest)
	reg.SetManifest(digestRegistryName, "latest", liveDigest)
	if _, warn, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "d1a1"), "A task"); err != nil {
		t.Fatal(err)
	} else if want := "warning: the image of acme/app workflow web is at digest " + liveDigest + ", not the " + recordDigest + " its build record names\n"; !strings.Contains(warn, want) {
		t.Fatalf("warn = %q, want it to carry %q", warn, want)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1: a mismatch must never refuse the launch", n)
	}
}

// TestRunDigestSilentWhenMatching: the common launch, where the registry's
// latest is the digest the record names, says nothing.
func TestRunDigestSilentWhenMatching(t *testing.T) {
	f := newCloudFixture(t)
	reg := hookDigestRegistry(t)
	writeDigestBuildRecord(t, f, appSlug, "web", digestImage, recordDigest)
	reg.SetManifest(digestRegistryName, "latest", recordDigest)
	if _, warn, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "d2a1"), "A task"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(warn, "is at digest ") {
		t.Fatalf("warn = %q, want no digest mismatch warning", warn)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// TestRunDigestReadFailureIsAWarningOnly: neither side's reader can refuse the
// launch. A digest the registry will not give (here a tag it does not have)
// and an image reference the registry reader cannot parse are both warnings,
// and the launch starts.
func TestRunDigestReadFailureIsAWarningOnly(t *testing.T) {
	t.Run("the registry has no such tag", func(t *testing.T) {
		f := newCloudFixture(t)
		hookDigestRegistry(t)
		writeDigestBuildRecord(t, f, appSlug, "web", digestImage, recordDigest)
		_, warn, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "d3a1"), "A task")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(warn, "warning: the image digest of acme/app workflow web is not compared with its build record: "+digestImage+":latest: the registry has no such manifest\n") {
			t.Fatalf("warn = %q, want the unreadable digest named as a warning", warn)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want 1: an unreadable digest must never refuse the launch", n)
		}
	})
	t.Run("the record names no readable image", func(t *testing.T) {
		f := newCloudFixture(t)
		hookDigestRegistry(t)
		writeDigestBuildRecord(t, f, appSlug, "web", "not a reference", recordDigest)
		_, warn, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "d3a2"), "A task")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(warn, "is not compared with its build record: the image \"not a reference:latest\" is not a registry/name[:tag|@digest] reference") {
			t.Fatalf("warn = %q, want the unreadable image named as a warning", warn)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want 1", n)
		}
	})
}

// TestRunDigestNoRecordYet: a workflow with no build record is silent — there
// is nothing to compare, and a repository whose image has never been built is
// not a drift. So is a record the build's record step has not completed: it
// names no image, or no digest of its own.
func TestRunDigestNoRecordYet(t *testing.T) {
	silent := func(t *testing.T, warn string) {
		t.Helper()
		if strings.Contains(warn, "is at digest ") || strings.Contains(warn, "is not compared with its build record") {
			t.Fatalf("warn = %q, want nothing about the image's digest", warn)
		}
	}
	t.Run("no record", func(t *testing.T) {
		f := newCloudFixture(t)
		hookDigestRegistry(t)
		_, warn, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "d4a1"), "A task")
		if err != nil {
			t.Fatal(err)
		}
		silent(t, warn)
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want 1", n)
		}
	})
	t.Run("a record with no digest of its own", func(t *testing.T) {
		f := newCloudFixture(t)
		hookDigestRegistry(t)
		writeDigestBuildRecord(t, f, appSlug, "web", digestImage, "")
		_, warn, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "d4a2"), "A task")
		if err != nil {
			t.Fatal(err)
		}
		silent(t, warn)
	})
}
