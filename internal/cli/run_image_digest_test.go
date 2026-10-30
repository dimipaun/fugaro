package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/task"
)

// recordDigest and liveDigest are the two digests C4's warning is about: the
// one a build recorded as the digest it promoted latest to, and the one the
// registry's latest resolves to now.
var (
	recordDigest = "sha256:" + strings.Repeat("3", 64)
	liveDigest   = "sha256:" + strings.Repeat("4", 64)
)

// digestHost, digestImage and digestRegistryName are the installation's own
// derived image for acme/app's web workflow, derived as the job spec derives
// it (infra's registryPath and gcp.ImageName) from newCloudFixture's project
// and region. The launcher must read *this* image, never one a build record
// names, so every test below serves its manifest under this name alone.
var (
	digestHost  = gcp.DefaultRegistryHost("us-east5", "proj-1234")
	digestImage = gcp.ImageName(digestHost+"/"+gcp.RegistryRepoID(appSlug), appSlug, "web")
	// digestRegistryName is the name the registry serves that image under:
	// the reference without its host, as parseImageRef splits the two. An
	// Artifact Registry name carries its project, so this is not the image
	// name alone.
	digestRegistryName = digestImage[strings.Index(digestImage, "/")+1:]
)

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
//
// The record names an image on a host of its own: the record comes from the
// runs bucket, so a launcher that built the read's host out of it would
// connect where a hand-edited or forged record says, and would miss this
// installation's own image. Only the derived name is served, so the warning
// below is reachable only by deriving the reference.
func TestRunDigestWarnsOnMismatch(t *testing.T) {
	f := newCloudFixture(t)
	reg := hookDigestRegistry(t)
	writeDigestBuildRecord(t, f, appSlug, "web", "registry.example.com/other/image", recordDigest)
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

// TestRunDigestMissingLatestTagIsNamed: the tag the build promoted the
// recorded digest to is gone. That is the daily check's own latest-drift case
// (LatestMissing, the registry has no such manifest) rather than a reader
// that failed, and the launch names it as such, so an operator reads what to
// fix instead of a bare "not compared". The launch starts either way.
func TestRunDigestMissingLatestTagIsNamed(t *testing.T) {
	f := newCloudFixture(t)
	hookDigestRegistry(t)
	writeDigestBuildRecord(t, f, appSlug, "web", digestImage, recordDigest)
	_, warn, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "d3a1"), "A task")
	if err != nil {
		t.Fatal(err)
	}
	want := "warning: the registry has no " + digestImage + ":latest, so the digest the build record of acme/app workflow web names (" + recordDigest + ") is not the one that tag resolves to\n"
	if !strings.Contains(warn, want) {
		t.Fatalf("warn = %q, want %q", warn, want)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1: a missing tag must never refuse the launch", n)
	}
}

// TestRunDigestNoRecordYet: a workflow with no build record is silent — there
// is nothing to compare, and a repository whose image has never been built is
// not a drift. So is a record the build's record step has not completed: one
// that names no digest of its own is nothing to compare either, and the
// registry is not even asked.
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
		reg := hookDigestRegistry(t)
		writeDigestBuildRecord(t, f, appSlug, "web", digestImage, "")
		_, warn, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "d4a2"), "A task")
		if err != nil {
			t.Fatal(err)
		}
		silent(t, warn)
		if got := len(reg.Accepts()); got != 0 {
			t.Fatalf("%d registry reads, want none: a record with no digest of its own is nothing to compare", got)
		}
	})
}

// TestWarnImageDigestDriftSkips pins the branches a launch-level test cannot
// reach: each is a case where the two digests cannot be compared at all, and
// each must stay a warning or a silence — never a refusal — and none may
// prescribe anything the reader could not do.
func TestWarnImageDigestDriftSkips(t *testing.T) {
	spec := &task.Spec{Repo: "acme/app", Workflow: "web"}
	cases := []struct {
		name  string
		setup func(t *testing.T, f *cloudFixture, env *cloudEnv, reg *gcpfake.Registry)
		// want is a substring of the warning; empty means the launcher must
		// say nothing at all.
		want string
		// reads is how many manifest requests the registry must have seen,
		// or -1 when the case does not care.
		reads int
	}{{
		name: "the records bucket will not open",
		setup: func(t *testing.T, _ *cloudFixture, env *cloudEnv, _ *gcpfake.Registry) {
			// A cloudEnv whose bucket_url is a GCS bucket other than the
			// runs bucket reads records from recordReadURL, opened through
			// openRecordBucket: here an installation whose records bucket it
			// cannot reach, which is a read failure and not the absence of a
			// record.
			env.lc.Bucket = "gs://another-bucket"
			old := openRecordBucket
			t.Cleanup(func() { openRecordBucket = old })
			openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) {
				return nil, errors.New("the records bucket is unreachable")
			}
		},
		want:  "warning: the image digest of acme/app workflow web is not compared with its build record: the records bucket is unreachable\n",
		reads: 0,
	}, {
		name: "the record cannot be read",
		setup: func(t *testing.T, f *cloudFixture, _ *cloudEnv, _ *gcpfake.Registry) {
			// Over MaxReadBytes: a read failure that is not ErrNotExist, so
			// what is tested is the failure rather than the record's absence.
			writeBucketFile(t, f, imagecheck.RecordKey(appSlug, "web"), strings.Repeat("x", blobx.MaxReadBytes+1))
		},
		want:  "is not compared with its build record: " + imagecheck.RecordKey(appSlug, "web") + ": object is too large",
		reads: 0,
	}, {
		name: "the record is unreadable",
		setup: func(t *testing.T, f *cloudFixture, _ *cloudEnv, _ *gcpfake.Registry) {
			writeBucketFile(t, f, imagecheck.RecordKey(appSlug, "web"), `{"version":`)
		},
		want:  "is not compared with its build record: the record is unreadable (the image record: ",
		reads: 0,
	}, {
		name: "the registry refuses the launcher's identity",
		setup: func(t *testing.T, _ *cloudFixture, _ *cloudEnv, reg *gcpfake.Registry) {
			// Artifact Registry read is the operators' grant, not the
			// launcher's (deploy/terraform/gcp/modules/repo/registry.tf): a
			// launchers-only identity is refused here on every launch, which
			// is not drift and nothing its reader can repair. It must be
			// silent rather than warn about a permission the launch does not
			// need.
			reg.Bearer = "a-token-this-identity-never-had"
		},
		reads: 1,
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCloudFixture(t)
			reg := hookDigestRegistry(t)
			env := fileEnv(t, f)
			writeDigestBuildRecord(t, f, appSlug, "web", digestImage, recordDigest)
			c.setup(t, f, env, reg)
			var warn strings.Builder
			warnImageDigestDrift(context.Background(), env, appSlug, spec, &warn)
			if got := warn.String(); c.want == "" {
				if got != "" {
					t.Fatalf("warn = %q, want nothing at all", got)
				}
			} else if !strings.Contains(got, c.want) {
				t.Fatalf("warn = %q, want it to carry %q", got, c.want)
			}
			if c.reads >= 0 {
				if n := len(reg.Accepts()); n != c.reads {
					t.Fatalf("%d registry reads, want %d", n, c.reads)
				}
			}
		})
	}
}
