// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain closes the ways a test could reach the developer's own machine or
// Google for real: no application default credentials (a file that doesn't
// exist), no metadata server, and every https request sent to a proxy nothing
// listens on (Go sends loopback traffic, which is all the fakes use, around any
// proxy). It is internal/cli's TestMain, which this package's tests need for
// the same reason: they drive the same command tree, and without it a read
// would go to the ambient credentials and a real runs bucket.
//
// It also keeps the two directories a command writes out of the developer's own
// home — $XDG_CACHE_HOME (the update-check cache, and the project-name and
// layer caches) and $XDG_CONFIG_HOME (checkouts.json, which the console's first
// run reads) — and tells git not to find this repository's own fugaro.yaml, so
// a command that resolves the checkout's project resolves the fixture's and
// never the project this repository belongs to.
func TestMain(m *testing.M) {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/fugaro-tests-have-no-credentials.json")
	os.Setenv("GCE_METADATA_HOST", "127.0.0.1:1")
	os.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	os.Setenv("https_proxy", "http://127.0.0.1:1")
	os.Unsetenv("STORAGE_EMULATOR_HOST")
	for _, k := range []string{"XDG_CACHE_HOME", "XDG_CONFIG_HOME"} {
		if dir, err := os.MkdirTemp("", "fugaro-test-ee-"); err == nil {
			os.Setenv(k, dir)
		}
	}
	// git stops before it reaches the repository root, the way
	// internal/cli's TestMain stops it: Dir(Dir(wd)) is above this package's
	// own directory and below the root that holds .git.
	if wd, err := os.Getwd(); err == nil {
		os.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(filepath.Dir(wd)))
	}
	os.Exit(m.Run())
}
