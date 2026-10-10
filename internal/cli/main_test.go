package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// TestMain closes the ways a test could reach real Google through a
// gs:// bucket_url or an unfaked client: no application default
// credentials (a file that doesn't exist), no metadata server, and every
// https request sent to a proxy nothing listens on (Go sends loopback
// traffic, which is all the fakes use, around any proxy). A test that
// means to talk to GCS points its config's endpoints at a fake.
func TestMain(m *testing.M) {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/fugaro-tests-have-no-credentials.json")
	os.Setenv("GCE_METADATA_HOST", "127.0.0.1:1")
	os.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	os.Setenv("https_proxy", "http://127.0.0.1:1")
	os.Unsetenv("STORAGE_EMULATOR_HOST")
	// init refuses to apply anything in a coding agent's session, and the
	// tests are often run from one: a test that means to be in one sets a
	// marker itself.
	for _, k := range agentMarkers {
		os.Unsetenv(k)
	}
	// Commands read the project from the fugaro.yaml of the checkout they run
	// in, found through git from the working directory. The tests run inside
	// this repository, whose own fugaro.yaml names the project fugaro, so git
	// is told not to look in the repository root: a test that wants a
	// checkout makes its own (git doesn't enter a ceiling directory itself).
	if wd, err := os.Getwd(); err == nil {
		os.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(filepath.Dir(wd)))
	}
	// The shared config is published to the runs bucket after init writes
	// the local one: tests that don't look at it publish to memory, never
	// to a real bucket.
	sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		if strings.HasPrefix(url, "file://") {
			return blobx.Open(ctx, url)
		}
		return blobx.Open(ctx, "mem://")
	}
	// Production skips a gs:// bucket (publish, build-record read) when the
	// config's endpoints are fakes; the tests reach buckets through seams
	// while their configs carry fake endpoints, so they turn the skip off and
	// the tests of the skip itself turn it on.
	skipGSOnFakeEndpoints = false
	// findLayer's bucket has no seam override redirecting gs:// elsewhere
	// the way sharedBucketOpener does above: a rig whose local config
	// computes a gs:// runs bucket URL (most do; only bucket_url: file://
	// fixtures don't) would otherwise have it opened for real the first
	// time anything resolves a project layer. A test that means to
	// exercise a layer already uses a file:// bucket_url or overrides this
	// var itself; anything else panics loudly instead of quietly reaching
	// storage.googleapis.com.
	layerBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		if strings.HasPrefix(url, "gs://") {
			panic("fugaro: test tried to open a real gs:// project layer bucket: " + url + "; use a bucket_url: file:// fixture or override layerBucketOpener")
		}
		return blobx.Open(ctx, url)
	}
	os.Exit(m.Run())
}

// A test that reaches findLayer's bucket with a gs:// URL and no seam
// override of its own must crash loudly, not quietly cross the network
// (TestNoRealGCSFromTests would stop the credentials and the proxy, but a
// slow failure there is still the gax retry loop the production risk fix
// in layer_resolve.go's layerBucketTimeout exists for).
//
// Mutation (run, restore): change the `strings.HasPrefix(url, "gs://")`
// condition in TestMain to `false`, and this test fails because nothing
// panics.
func TestLayerBucketOpenerRefusesARealGSBucket(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("layerBucketOpener did not panic on a gs:// URL")
		}
		if !strings.Contains(fmt.Sprint(r), "gs://") {
			t.Fatalf("panic = %v", r)
		}
	}()
	_, _ = layerBucketOpener(context.Background(), "gs://fugaro-tests-should-never-open-this")
}

func TestNoRealGCSFromTests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, err := blobx.Open(ctx, "gs://fugaro-tests-never-this-bucket")
	if err != nil {
		return // refused at once: also fine
	}
	defer b.Close()
	// Whatever stops it (no credentials, the dead proxy), it must stop:
	// the bucket doesn't exist, and nothing real may answer.
	if _, err := b.Exists(ctx, "x"); err == nil {
		t.Fatal("a gs:// read of a bucket nobody made succeeded: a test can reach real Google")
	}
}
