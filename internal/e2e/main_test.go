//go:build !live

package e2e

import (
	"os"
	"testing"
)

// TestMain gives every e2e test the hermetic environment the cli tests
// have: no application default credentials (a file that doesn't exist), no
// metadata server, and every https request sent to a proxy nothing listens
// on (Go sends loopback traffic, which is all the fakes use, around any
// proxy). A test can then neither read the developer's credentials nor
// reach real Google through a gs:// bucket. The live tests (build tag live)
// need the real ones and are excluded.
func TestMain(m *testing.M) {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/fugaro-tests-have-no-credentials.json")
	os.Setenv("GCE_METADATA_HOST", "127.0.0.1:1")
	os.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	os.Setenv("https_proxy", "http://127.0.0.1:1")
	os.Unsetenv("STORAGE_EMULATOR_HOST")
	os.Exit(m.Run())
}
