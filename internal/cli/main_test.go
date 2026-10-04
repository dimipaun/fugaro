package cli

import (
	"context"
	"os"
	"path/filepath"
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
	os.Exit(m.Run())
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
