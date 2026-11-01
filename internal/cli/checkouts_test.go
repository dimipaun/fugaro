package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

// The temporary-directory rule is off for this package's tests, and it has to
// be: every checkout a test can make is under the machine's temporary
// directory (t.TempDir()), so with the real list (checkoutTempRoots) nothing
// here would be recorded at all and the tests below would pass for a reason
// that has nothing to do with what they mean — the console guard's own test
// would stop failing when the guard is dropped, which is what makes a mutation
// test worth having. The rule itself is tested where the list is a fact of the
// test (internal/localcfg/checkouts_test.go), and this package's own half of it
// — that a checkout is remembered at all — is TestACommandInACheckoutRecordsIt.
func init() { checkoutTempRoots = func() []string { return nil } }

// prodCheckoutTempRoots is the list a real binary's recording path passes,
// captured here because the init above replaces the variable for the whole
// package: a package-level variable is initialised before any init function
// runs, so this holds the CLI's own list (tempxdg.go's tempRoots) rather than
// the test's empty one. Without it the production list — the thing the
// temporary-directory rule actually stands on — would be unpinned in this
// package, where every test either passes its own list or none.
var prodCheckoutTempRoots = checkoutTempRoots

// checkoutsYAML is a fugaro.yaml that validates cleanly, so the recording
// tests run a command that succeeds and can only fail by what it does beside
// it.
const checkoutsYAML = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
	"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// checkoutsFixture points XDG_CONFIG_HOME at a directory of the test's own
// and writes two installations into it: the sandbox one (aurora) and a second
// (bramble), so a listing that returned "the one config" fails here. aurora
// enrolls the two repositories these tests work in, because only a repository
// the project enrolls is recorded or validated (localcfg.Config.Enrolls).
func checkoutsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	isolateProjects(t, dir)
	writeProject(t, "aurora", "aurora-gcp")
	writeProject(t, "bramble", "bramble-gcp")
	enrollRepo(t, "aurora", "acme/app")
	enrollRepo(t, "aurora", "acme/other")
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
// does, and returns the identity it was recorded under. The temporary-root
// list is the one the recording path passes (checkoutTempRoots, empty in this
// binary), so a test that records through this helper and a command that
// records on its own are the same write.
func checkoutsRemember(t *testing.T, project, path, origin string) localcfg.CheckoutIdentity {
	t.Helper()
	id := localcfg.CheckoutIdentity{Provider: "github", Repo: strings.TrimSuffix(strings.TrimPrefix(origin, "https://github.com/"), ".git")}
	if err := localcfg.RecordCheckout(os.Getenv, project, id, path, time.Now(), checkoutTempRoots()); err != nil {
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

// TestTheProductionTempRootsCoverTheMachinesTempDirs: the list a real binary
// passes names the temporary directories a checkout can be made in on this
// platform, so a scratchpad is refused wherever the machine puts it. The fixed
// /tmp and /private/tmp are in it on purpose: TMPDIR can point somewhere else
// entirely, and a list that read only $TMPDIR would remember a checkout under
// /tmp. An empty list would leave every scratchpad remembered, and nothing
// else in this package would notice — the tests here pass their own list.
func TestTheProductionTempRootsCoverTheMachinesTempDirs(t *testing.T) {
	roots := prodCheckoutTempRoots()
	if len(roots) == 0 {
		t.Fatal("the CLI's temporary roots are empty: a scratchpad would be remembered")
	}
	paths := []string{filepath.Join(os.TempDir(), "scratch", "app")}
	if runtime.GOOS != "windows" {
		// /tmp and /private/tmp are one directory on macOS, and neither is a
		// temporary directory on Windows.
		paths = append(paths, "/tmp/scratch/app", "/private/tmp/scratch/app")
	}
	for _, p := range paths {
		if !localcfg.InTempRoots(p, roots) {
			t.Errorf("%s is not under %v, want the list a real binary passes to cover it", p, roots)
		}
	}
}

// TestACommandInATempDirRecordsNothing: the whole recording path, with the
// list a real binary passes rather than a test's own. Every checkout a test
// can make is under the machine's temporary directory, so this is the
// scratchpad case as it happens in production: a command runs in a checkout of
// an enrolled repository, and the state file is not even created.
func TestACommandInATempDirRecordsNothing(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	statePath, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	restore := checkoutTempRoots
	checkoutTempRoots = prodCheckoutTempRoots
	t.Cleanup(func() { checkoutTempRoots = restore })
	if _, err := checkoutProject(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a command in a temporary directory wrote %s (stat: %v)", statePath, err)
	}
}

// TestACheckoutIsRememberedUnderItsOriginsProvider: the provider a repository
// is remembered by is the one whose host its origin is on, never the one its
// own fugaro.yaml claims. A file that says bitbucket over a github.com origin
// is a copied config, or a checkout re-pointed at another forge: remembering
// it as bitbucket/acme/app would put a name in the state file that no
// provider serves, and one the console rebuilds provider-host links from
// (design §5.1 rule 2), so the link would point at the wrong host. It is the
// same rule repoKnown already holds a repository to.
//
// **And a host no provider lives on records nothing, whatever the file
// says.** A GitHub Enterprise or self-hosted Bitbucket box is a host no
// provider's host is, so the pair (provider, owner/name) the file's
// git.provider would give is not one a provider serves — and the pair is
// exactly what the console builds a provider-host link from (design §5.1 rule
// 2). The file's word is not evidence: a hostile checkout on an arbitrary
// host (`https://git.evil.example/acme/app.git`, `git.provider: github`,
// `project: aurora`) would otherwise be recorded, and re-validated, as the
// enrolled github/acme/app — the enrolment gate this branch adds, bypassed by
// a line the checkout itself controls. It is repoKnown's rule: a provider
// knows a repository only when that provider's host is its origin's host.
//
// Mutation (run, restore): put the `co.Provider` fallback back into the
// provider lookup (as the code did before this review) and the second subtest
// fails with github/acme/app in the state file.
func TestACheckoutIsRememberedUnderItsOriginsProvider(t *testing.T) {
	t.Run("the origin's host wins over the file", func(t *testing.T) {
		dir := checkoutsFixture(t)
		co := checkoutsAt(t, filepath.Join(dir, "src", "app"),
			"version: 1\nproject: aurora\ngit: { provider: bitbucket, base_branch: main }\n",
			"https://github.com/acme/app.git")
		if _, err := checkoutProject(context.Background(), co); err != nil {
			t.Fatal(err)
		}
		known, err := localcfg.LoadCheckouts(os.Getenv)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := known.Projects["aurora"]["github/acme/app"]; !ok {
			t.Errorf("aurora's checkouts = %v, want the origin's identity github/acme/app", known.Projects)
		}
		if _, ok := known.Projects["aurora"]["bitbucket/acme/app"]; ok {
			t.Errorf("aurora's checkouts = %v, want nothing under the file's provider", known.Projects)
		}
	})
	t.Run("a host no provider lives on records nothing", func(t *testing.T) {
		// The file names a provider the project does enroll, for a host that
		// provider does not serve: the pair the fallback would record is one
		// a provider really has (github/acme/app), which is what makes this
		// the case a link built from it would follow.
		dir := checkoutsFixture(t)
		co := checkoutsAt(t, filepath.Join(dir, "src", "app"),
			"version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n",
			"https://git.evil.example/acme/app.git")
		if _, err := checkoutProject(context.Background(), co); err != nil {
			t.Fatal(err)
		}
		known, err := localcfg.LoadCheckouts(os.Getenv)
		if err != nil {
			t.Fatal(err)
		}
		if len(known.Projects["aurora"]) != 0 {
			t.Errorf("aurora's checkouts = %v, want none: the file's provider is not evidence for a host no provider lives on", known.Projects)
		}
	})
	t.Run("a host and a file that name no provider record nothing", func(t *testing.T) {
		dir := checkoutsFixture(t)
		co := checkoutsAt(t, filepath.Join(dir, "src", "app"),
			checkoutsProjectYAML("aurora"),
			"https://git.acme.example/acme/app.git")
		if _, err := checkoutProject(context.Background(), co); err != nil {
			t.Fatal(err)
		}
		known, err := localcfg.LoadCheckouts(os.Getenv)
		if err != nil {
			t.Fatal(err)
		}
		if len(known.Projects["aurora"]) != 0 {
			t.Errorf("aurora's checkouts = %v, want none: nothing names a provider for this origin", known.Projects)
		}
	})
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
		{"runs", "stats", "--json"},
		{"budget", "show", "--json"},
		// `report` is not here: the command moved to ee/ (E2.2b, D444), so an
		// untagged build answers the name with the D522 stub and never reaches
		// the checkout. The tagged tree's read of this checkout is asserted in
		// ee/report/cli/checkouts_test.go, against the same fixture rule.
		{"secrets", "ls", "--json", "--repo", "acme/app"},
		// The graph screen's read (GV1.6): a cloud read of this checkout's
		// own repository, so it resolves the checkout like runs ls does.
		{"plan", "status", "--json"},
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

// TestTheUpdateCheckLookupRemembersNothing: NewRootCmd's PersistentPreRun
// resolves the selected project config on EVERY command, before the command
// has done anything, to answer "does this project say update_check: false"
// (updateCheckDisabled). That lookup must not remember the checkout it looked
// at: the command it rides on may resolve no checkout for its own work at all,
// and the directory the operator happens to be standing in is not something
// the command did.
//
// The two commands below are exactly that — bare `fugaro backends` prints its
// own help, and `fugaro upgrade --check` is documented as writing nothing —
// and the console spawns neither, so neither is in consoleReadPaths. The
// read-only resolver is the only thing between them and a write, which is why
// this is the test that pins selectedProjectConfigReadOnly:
// TestConsoleV2ReadsWriteNothing's commands are protected twice over, by
// consoleReadPaths' context mark as well, and it passes with the resolver
// reverted.
//
// Mutation (run, restore): revert updateCheckDisabled (updatewarn.go) to
// selectedProjectConfig and this fails on the first row, with the state file
// holding the checkout the command merely started in.
func TestTheUpdateCheckLookupRemembersNothing(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsYAML, "https://github.com/acme/app.git")
	t.Chdir(co)
	statePath, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"backends"}, {"upgrade", "--check"}} {
		if out, _, err := execute(t, args...); err != nil {
			t.Logf("%v exited with %v:\n%s", args, err, out)
		}
		if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%v remembered the checkout it started in (stat %s: %v)", args, statePath, err)
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

// TestAnUnenrolledCheckoutIsNeverRecorded: the recorder writes a checkout
// down only when the project's own config enrolls the repository its origin
// names. A directory whose fugaro.yaml claims the project — a fork, a
// third-party repository, a hostile pull request — is a claim the checkout
// makes and not an enrolment the operator gave, and recording it would spend
// the 20-repository bound on it and put its path in front of the console.
//
// Mutation (run, restore): drop the LoadProject/Enrolls gate from
// recordCheckout (project.go) and this fails with the un-enrolled checkout in
// the state file.
func TestAnUnenrolledCheckoutIsNeverRecorded(t *testing.T) {
	dir := checkoutsFixture(t)
	// aurora enrolls acme/app; this checkout's origin is another repository
	// of the same owner, with the project: line copied in.
	co := checkoutsAt(t, filepath.Join(dir, "src", "evil"), checkoutsProjectYAML("aurora"), "https://github.com/acme/evil.git")
	got, err := checkoutProject(context.Background(), co)
	if err != nil || got == nil {
		t.Fatalf("checkoutProject = %+v, %v", got, err)
	}
	known, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(known.Projects["aurora"]) != 0 {
		t.Errorf("aurora's checkouts = %+v, want none: the project does not enroll github/acme/evil", known.Projects)
	}
	// The control, in the same fixture: the enrolled repository is recorded
	// by the same call, so this is the gate and not a broken fixture.
	app := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	if _, err := checkoutProject(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	known, err = localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := known.Projects["aurora"]["github/acme/app"]; !ok {
		t.Errorf("aurora's checkouts = %+v, want the enrolled repository recorded", known.Projects)
	}
}

// TestARewrittenOriginIsNeverRecorded: a checkout whose raw .git/config names
// a different host or repository than git resolves for "origin"
// (url.<base>.insteadOf) is not recorded at all. The rewrite is a local
// config line that can point anywhere, so the pair (provider, repository) it
// would produce is not a fact about the directory — and the state file is
// what the console offers paths from.
//
// Mutation (run, restore): change `if !ok || oi.Rewritten` to `if !ok` in
// recordCheckout (project.go) and this fails with the checkout recorded.
func TestARewrittenOriginIsNeverRecorded(t *testing.T) {
	dir := checkoutsFixture(t)
	// The raw origin is the gh: shorthand and the rewrite resolves it to the
	// enrolled github repository: with the guard gone the checkout would be
	// recorded under a name it is enrolled for, so this isolates the guard.
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "gh:acme/app.git")
	testutil.Git(t, co, "config", "--local", "--add",
		"url.https://github.com/acme/app.git.insteadOf", "gh:acme/app.git")
	if oi, ok := readOrigin(context.Background(), co); !ok || !oi.Rewritten {
		t.Fatalf("the fixture did not produce a rewritten origin: %+v ok=%v", oi, ok)
	}
	if _, err := checkoutProject(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	known, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(known.Projects["aurora"]) != 0 {
		t.Errorf("aurora's checkouts = %+v, want none: the origin is rewritten", known.Projects)
	}
}

// TestRecordCheckoutSkipsLinkedWorktree: a linked git worktree — `git worktree
// add`, which is the scratchpad a coding agent makes — is not remembered,
// while the checkout it was made from is. The two are the same repository, on
// the same project, enrolled the same way, and the worktree's own fugaro.yaml
// is the committed one: nothing but the worktree relation tells them apart.
//
// Both live under the machine's temporary directory, which is why this
// package's tests turn the temporary-directory rule off (see init): with it on
// the worktree would be skipped for the wrong reason and the control below —
// the main checkout, recorded by the same call — would fail. The worktree
// relation is the only thing that can skip a path here.
//
// Mutation (run, restore): drop the linkedWorktree call from recordCheckout
// (project.go) and the first assertion fails with the worktree's path in the
// state file, beside the main checkout the control records.
func TestRecordCheckoutSkipsLinkedWorktree(t *testing.T) {
	dir := checkoutsFixture(t)
	main := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	testutil.Git(t, main, "add", "-A")
	testutil.Git(t, main, "commit", "--quiet", "-m", "the repository")
	linked := filepath.Join(dir, "src", "scratch")
	testutil.Git(t, main, "worktree", "add", "--quiet", linked)
	if got, known := linkedWorktree(context.Background(), linked); !known || !got {
		t.Fatalf("the fixture is not a linked worktree: linked=%v known=%v", got, known)
	}
	if got, known := linkedWorktree(context.Background(), main); !known || got {
		t.Fatalf("the fixture's main checkout reads as a linked worktree: linked=%v known=%v", got, known)
	}

	if _, err := checkoutProject(context.Background(), linked); err != nil {
		t.Fatal(err)
	}
	known, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(known.Projects["aurora"]) != 0 {
		t.Errorf("aurora's checkouts = %+v, want none: a linked worktree is not a checkout this machine works in", known.Projects)
	}

	// The control, in the same fixture and by the same call: the checkout the
	// worktree was made from is remembered as it always was.
	if _, err := checkoutProject(context.Background(), main); err != nil {
		t.Fatal(err)
	}
	known, err = localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := known.Projects["aurora"]["github/acme/app"]
	if !ok || len(r.Paths) != 1 || r.Paths[0].Path != main {
		t.Errorf("aurora's checkouts = %+v, want the main checkout %q recorded", known.Projects, main)
	}
}

// TestConfigListRefusesARowUnderATempDir: the read refuses a row whose path is
// under the machine's temporary directory, with the reason the contract
// decides from the row's own strings and before anything touches the disk
// (not_representable, console/docs/json-contract.md §6.8). No stat and no git
// runs for such a row: the first path below is not there at all, so a read
// that looked would call it missing, and the second is a live checkout of the
// enrolled repository, so a read that looked would call it valid. Both are
// refused for the one reason, and neither is dropped — the row stays for the
// next write to prune.
//
// The list is the production one (prodCheckoutTempRoots), which this package's
// tests otherwise turn off: a row under a temporary directory is what an older
// fugaro recorded, before the writer refused them, and this is the read
// catching it. It is what the console is offered as an installation's working
// directory, and a live scratchpad an agent made is exactly what must not be.
func TestConfigListRefusesARowUnderATempDir(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	restore := checkoutTempRoots
	checkoutTempRoots = prodCheckoutTempRoots
	t.Cleanup(func() { checkoutTempRoots = restore })

	gone := filepath.Join(os.TempDir(), "scratch-not-there", "app")
	stamp := time.Now().UTC().Format(time.RFC3339)
	configListWriteState(t, `{"version": 1, "projects": {"aurora": {"github/acme/app": {`+
		`"provider": "github", "repo": "acme/app", "recorded_at": "`+stamp+`", "paths": [`+
		`{"path": "`+gone+`", "recorded_at": "`+stamp+`"},`+
		`{"path": "`+co+`", "recorded_at": "`+stamp+`"}]}}}}`+"\n")
	before := configListState(t)

	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	byPath := map[string]configListCheckout{}
	for _, c := range got {
		byPath[c.Path] = c
	}
	if c, ok := byPath[gone]; !ok || c.Valid || c.Reason != checkoutNotRepresentable {
		t.Errorf("the row for a path under a temporary directory that is not there = %+v, want valid false and reason %q: the read never looks at it", c, checkoutNotRepresentable)
	}
	if c, ok := byPath[co]; !ok || c.Valid || c.Reason != checkoutNotRepresentable {
		t.Errorf("the row for a live checkout under a temporary directory = %+v, want valid false and reason %q", c, checkoutNotRepresentable)
	}
	if after := configListState(t); after != before {
		t.Errorf("config list rewrote the remembered checkouts:\nbefore %s\nafter  %s", before, after)
	}
}

// TestConfigListNeverWrites: the pruning is the writer's, and the read is
// where the console sees what this machine remembers. A read re-validates
// every path and reports what it finds — a stale path is still there for the
// operator to see, and still there if the directory comes back — and it drops
// nothing and writes nothing, not even the rows the next write is going to
// prune. A state file that does not exist stays that way.
//
// The scratchpad row is reported as it stands here because this package's
// tests turn the temporary-directory rule off (see init), so this row is
// looked at like any other: what this test shows about it is that the read
// does not drop it, which is the subject here. What the read says about one is
// TestConfigListRefusesARowUnderATempDir, where the rule is on.
func TestConfigListNeverWrites(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	statePath, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(co)
	if _, _, err := execute(t, "config", "list", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config list created the remembered checkouts (stat %s: %v)", statePath, err)
	}

	// Hand-written state holding exactly the two rows the next write would
	// prune: a live checkout under the machine's temporary directory (a
	// scratchpad), and a path gone for longer than the grace.
	scratch := checkoutsAt(t, filepath.Join(dir, "src", "scratch"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	stamp := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	configListWriteState(t, `{"version": 1, "projects": {"aurora": {"github/acme/app": {`+
		`"provider": "github", "repo": "acme/app", "recorded_at": "`+stamp+`", "paths": [`+
		`{"path": "`+scratch+`", "recorded_at": "`+stamp+`"},`+
		`{"path": "/src/gone/app", "recorded_at": "`+stamp+`", "missing_since": "`+old+`"}]}}}}`+"\n")
	before := configListState(t)

	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 2 {
		t.Fatalf("aurora's checkouts = %+v, want both rows: the read reports what it finds and drops nothing", got)
	}
	byPath := map[string]configListCheckout{}
	for _, c := range got {
		byPath[c.Path] = c
	}
	if c := byPath[scratch]; !c.Valid || c.Reason != "" {
		t.Errorf("the row under a temporary directory = %+v, want it reported as it stands: this read drops nothing, whatever the next write would prune", c)
	}
	if c := byPath["/src/gone/app"]; c.Valid || c.Reason != checkoutMissing {
		t.Errorf("the long-missing row = %+v, want valid false and reason %q", c, checkoutMissing)
	}
	if after := configListState(t); after != before {
		t.Errorf("config list rewrote the remembered checkouts:\nbefore %s\nafter  %s", before, after)
	}
}

// TestCheckoutsJSONShapeUnchanged: the document the console reads is the same
// document it read before any of this. The state file grew one field (the
// clock a missing path is dropped by, localcfg.CheckoutPath.MissingSince) and
// nothing of it reaches the contract: the keys at every level are the ones the
// console's §6.8 table names, read off the JSON rather than off the Go types,
// so a field added to the document is caught here however it got there.
func TestCheckoutsJSONShapeUnchanged(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")
	// And a row the next write would prune, so the shape is checked on a
	// document that holds one: the field that decides it is the state file's.
	stamp := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	configListWriteState(t, `{"version": 1, "projects": {"aurora": {"github/acme/app": {`+
		`"provider": "github", "repo": "acme/app", "recorded_at": "`+stamp+`", "paths": [`+
		`{"path": "`+co+`", "recorded_at": "`+stamp+`"},`+
		`{"path": "/src/gone/app", "recorded_at": "`+stamp+`", "missing_since": "`+old+`"}]}}}}`+"\n")

	_, out := configListJSON(t)
	var top map[string]any
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if k := keysOf(t, top); !reflect.DeepEqual(k, goldenKeys(t, "config_list_keys.golden")) {
		t.Errorf("config list --json top-level keys = %v", k)
	}
	var inst map[string]any
	for _, v := range top["installations"].([]any) {
		if m, _ := v.(map[string]any); m["name"] == "aurora" {
			inst = m
		}
	}
	if inst == nil {
		t.Fatalf("aurora is not in the document:\n%s", out)
	}
	if k := keysOf(t, inst); !reflect.DeepEqual(k, goldenKeys(t, "config_list_installation_keys.golden")) {
		t.Errorf("config list --json installations[] keys = %v", k)
	}
	rows, _ := inst["checkouts"].([]any)
	if len(rows) != 2 {
		t.Fatalf("aurora's rows = %v, want the two", rows)
	}
	full := goldenKeys(t, "config_list_checkout_keys.golden")
	for _, v := range rows {
		row, _ := v.(map[string]any)
		want := full
		if row["valid"] == true {
			// reason is omitted when there is none, which is the one key the
			// contract lets a row carry fewer of.
			want = []string{"base_branch", "enrolled", "path", "repository", "valid"}
		}
		if k := keysOf(t, row); !reflect.DeepEqual(k, want) {
			t.Errorf("a checkout row's keys = %v, want %v", k, want)
		}
	}
	if strings.Contains(out, "missing_since") {
		t.Errorf("config list --json carries the state file's own clock:\n%s", out)
	}
}
