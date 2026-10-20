package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The remembered checkouts, from the CLI's side: the state file is written by
// the commands that resolve a checkout's project, read by `fugaro config
// list --json`, and written by nothing else. These are the tests for the
// recording path and for the read-only property the console's allow-listed
// reads turn on; config_list_test.go holds the tests for the document itself.

// checkoutsYAML is a fugaro.yaml that validates cleanly, so the recording
// tests run a command that succeeds and can only fail by what it does beside
// it.
const checkoutsYAML = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
	"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// checkoutsFixture points XDG_CONFIG_HOME at a directory of the test's own
// and writes two installations into it: the sandbox one (aurora) and a second
// (bramble), so a listing that returned "the one config" fails here.
func checkoutsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	isolateProjects(t, dir)
	writeProject(t, "aurora", "aurora-gcp")
	writeProject(t, "bramble", "bramble-gcp")
	return dir
}

// checkoutsProjectYAML is a fugaro.yaml that names project and nothing else.
func checkoutsProjectYAML(project string) string {
	return "version: 1\nproject: " + project + "\n"
}

// checkoutsAt makes a git checkout at dir whose fugaro.yaml is yaml and whose
// origin is origin, and returns the checkout's toplevel as git resolves it
// (the path a command in it would record, which is what the fixture has to
// compare against).
func checkoutsAt(t *testing.T, dir, yaml, origin string) string {
	t.Helper()
	gitCheckout(t, dir, yaml)
	testutil.Git(t, dir, "remote", "add", "origin", origin)
	return testutil.Git(t, dir, "rev-parse", "--show-toplevel")
}

// checkoutsRemember records a checkout exactly as a command running in it
// does, and returns the identity it was recorded under.
func checkoutsRemember(t *testing.T, project, path, origin string) localcfg.CheckoutIdentity {
	t.Helper()
	id := localcfg.CheckoutIdentity{Provider: "github", Repo: strings.TrimSuffix(strings.TrimPrefix(origin, "https://github.com/"), ".git")}
	if err := localcfg.RecordCheckout(os.Getenv, project, id, path, time.Now()); err != nil {
		t.Fatal(err)
	}
	return id
}

// checkoutsState is the remembered checkouts as they are on disk.
func checkoutsState(t *testing.T) string {
	t.Helper()
	path, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// captureStderr runs f with the process's own stderr redirected and returns
// what f wrote there. The commands' own output goes to the command's writer
// (execute's buffer), so what lands here is what a side effect printed behind
// the command's back.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestACommandInACheckoutRecordsIt: the remembered checkouts are written by
// the commands that already resolve a checkout's project — the one common
// code path, not one per command — so `fugaro validate` in a checkout is what
// makes the console's first run find it.
func TestACommandInACheckoutRecordsIt(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsYAML, "https://github.com/acme/app.git")
	t.Chdir(co)
	if out, _, err := execute(t, "validate", "--offline"); err != nil {
		t.Fatalf("fugaro validate: %v\n%s", err, out)
	}
	known, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := known.Projects["aurora"]["github/acme/app"]
	if !ok || len(r.Paths) != 1 || r.Paths[0].Path != co {
		t.Fatalf("aurora's checkouts = %+v, want the checkout validate ran in", known.Projects)
	}
}

// TestConsoleAllowListedReadsWriteNothing: every command the design's §5.1
// exact-argv allow-list lets the console spawn is a READ, and a read the
// console spawns changes nothing — so none of them remembers the checkout it
// resolved on the way to its own work.
//
// The commands are spawned exactly as the design says the console spawns
// them: from inside the installation's checkout directory (§3.4 rule 5), so
// each one resolves a checkout that this machine has not recorded, which is
// the only state in which a write would show.
//
// Mutation (run, restore): drop the consoleRead guard from recordCheckout in
// project.go and this test fails on the first row that resolves the checkout,
// with checkouts.json created under the fixture's config home.
func TestConsoleAllowListedReadsWriteNothing(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsYAML, "https://github.com/acme/app.git")
	t.Chdir(co)
	statePath, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	// The cloud reads fail on this machine (no credentials, no bucket) after
	// the selection they resolve the checkout for; their verdict is their own
	// and what is asserted here is what they wrote. `watch` is not run: it is
	// a stream, and a stream that never returns is not this test's subject —
	// consoleReadPaths carries it, and the design table test pins that.
	for _, args := range [][]string{
		{"accounts", "ls", "--json", "--repo", "acme/app"},
		{"doctor", "--json", "--dir", co},
		{"runs", "ls", "--json"},
		{"budget", "show", "--json"},
		{"report", "--json"},
		{"secrets", "ls", "--json", "--repo", "acme/app"},
	} {
		out, _, err := execute(t, args...)
		if err != nil {
			t.Logf("%v exited with %v:\n%s", args, err, out)
		}
		if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%v remembered the checkout it looked at (stat %s: %v)", args, statePath, err)
		}
	}
}

// TestAFailedRecordNeverFailsTheCommand: remembering a checkout is best
// effort. A state file that cannot be written (here: a directory where the
// file goes) leaves the resolver — and so the command that called it —
// exactly as it was: the checkout is returned, the error is nil, nothing is
// printed about it and the command exits 0.
func TestAFailedRecordNeverFailsTheCommand(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsYAML, "https://github.com/acme/app.git")
	statePath, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	// The resolver the commands share, called where the write it makes at
	// the end cannot succeed: the checkout is still the answer.
	var got *localcfg.Checkout
	printed := captureStderr(t, func() { got, err = checkoutProject(context.Background(), co) })
	if err != nil || got == nil || got.Project != "aurora" {
		t.Fatalf("checkoutProject = %+v, %v, want the checkout and no error", got, err)
	}
	t.Chdir(co)
	var stdout, stderr string
	printed += captureStderr(t, func() { stdout, stderr, err = execute(t, "validate", "--offline") })
	if err != nil {
		t.Fatalf("fugaro validate failed because it could not remember a checkout: %v\n%s", err, stdout)
	}
	// Nothing was said about it anywhere: not on the command's stderr, not
	// on the process's, and the path is not in either.
	for _, out := range []string{stderr, printed} {
		if strings.Contains(out, "checkout") || strings.Contains(out, dir) {
			t.Errorf("the failure to remember the checkout was printed: %q", out)
		}
	}
}
