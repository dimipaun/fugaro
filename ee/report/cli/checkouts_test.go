// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// The moved half of internal/cli/checkouts_test.go's
// TestConsoleAllowListedReadsWriteNothing (E2.2b, D444): the design's §5.1
// allow-list lets the console spawn `report --json` as a read, and a read the
// console spawns changes nothing, so the command does not remember the checkout
// it resolved on the way to its own work.
//
// The case left the core's loop because the command did: an untagged build
// answers the name with the D522 stub, which never resolves a checkout, so the
// core row could only pass vacuously. The core's assertion for the other
// commands is untouched.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// reportCheckoutYAML is a fugaro.yaml that names the project and nothing else:
// the selection it resolves is this file's project, which is the state a write
// would record.
const reportCheckoutYAML = "version: 1\nproject: aurora\n"

// TestReportReadWritesNoCheckout: `report --json` spawned from inside an
// unrecorded checkout, exactly as the design says the console spawns it (§3.4
// rule 5), leaves the checkouts state file absent.
func TestReportReadWritesNoCheckout(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "src", "app")
	testutil.IsolateGit(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(reportCheckoutYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	t.Chdir(dir)

	if _, _, err := execute(t, "report", "--json"); err != nil {
		t.Fatalf("report --json exited with %v", err)
	}
	statePath, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("report --json wrote %s: a console allow-listed read remembers the checkout it resolved", statePath)
	}
}
