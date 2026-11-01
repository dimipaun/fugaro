// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/blobx"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// updateCoordinatorGolden rewrites testdata/coordinator_status.golden, the way
// the package's other goldens do — under a flag name of its own, because
// -update, -update-review, -update-retire and -update-cloudrun are already
// taken and a second flag.Bool of one name panics.
var updateCoordinatorGolden = flag.Bool("update-coordinator", false, "rewrite testdata/coordinator_status.golden")

// The command is the read the CLI already performs — the runs bucket plus a
// gitprov.Provider opened exactly as exec.go's providerOptions opens one — with
// P2's credential on it (the coordinator's own read-only installation token,
// minted per repository), and these tests drive it end to end through the
// fixture bucket and fake hosts. What each of them is for:
//
//   - TestCoordinatorStatusJSON: the joined view, read as data, over real run
//     objects and a real (fake) provider — the records, the collision rule
//     (one per pair, and a pair that shares only a service is one), the
//     declaration under a name that says it is one, and the one record whose
//     stored file set is short of what the run changed: the field that
//     qualifies the collisions, since a truncated set is what they are
//     computed over.
//   - TestCoordinatorStatusTextGolden: the rendered view, read rather than
//     accepted.
//   - TestCoordinatorStatusWritesNothing: the plan's sentence for P1, held by
//     a test — the runs bucket is unchanged and the host's write log is empty.
//   - TestCoordinatorStatusReadsOnlyThePullRequestsTheRunsName: the reader's
//     one documented limit (finding #10), pinned as the limitation it is so
//     that widening it is a change somebody makes on purpose.
//   - TestCoordinatorStatusRefusesAnUnnamedRepository: one repository's view
//     is asked for by name, and --all with --repo is refused rather than
//     resolved.
//   - TestCoordinatorStatusAllReadsEveryRepository: --all is the project-wide
//     read — one view per repository, each read through a token of its own
//     (D193), and nothing compared across repositories.
//   - TestCoordinatorStatusMintsAReadOnlyTokenPerRepository: the credential
//     itself, held to the mint's own request body — one repository per token,
//     and no permission above read (Q3 answered (A)).
//   - TestCoordinatorStatusRefusesABitbucketProject: the project this read
//     does not serve is refused, naming the reason and not the repository
//     (Q12 answered (A), finding #30).
//   - TestCoordinatorStatusNeverPrintsACredential: the one security rule this
//     path owns — the provider's own credential values never reach an error
//     this command prints, from opening the provider or from a read.
//   - TestCoordinatorStatusNeverPrintsTheAppKey: the same rule on the half of
//     this command that holds the App's private key.
//   - TestCoordinatorStatusWithNoRuns: an empty bucket is a view with nothing
//     in it, not a failure.
//   - TestCoordinatorStatusWindow: the window is runs ls's, measured back from
//     runs ls's clock.
//   - TestCoordinatorStatusFollowsTheRecordsPullRequest: a run is on the pull
//     request its record names, else its task's — runs.go's own order.
//   - TestCoordinatorStatusSanitisesWhatSomebodyElseWrote: a branch name or a
//     launcher's own string cannot redraw the table around itself.
//   - TestCoordinatorStatusJSONSanitisesTheDeclaration: the same rule for the
//     document, where the renderer's own cell is not what a reader parses.
//   - TestCoordinatorStatusSurvivesGarbledRunObjects: a run whose task.json
//     and result.json are half-written is still a run in flight, warned about
//     on stderr and listed anyway.

// coordinatorRunSeed is one run the test fixture's bucket holds, in the terms
// the view reads it: what its task says, what its launch record declared, and
// what its result record ended up as.
type coordinatorRunSeed struct {
	id     string
	repo   string // the repository the run is of; "" means the fixture's own
	pr     int    // the pull request the task names, 0 for none (a first run)
	prev   string // the run the task continues, set with pr
	branch string // the run's branch, on the task and on the record
	who    string // launch.json's launched_by
	status runstore.Status
	prRef  int      // the pull request the record names, 0 for none
	base   string   // the record's base_branch
	files  []string // the paths the record says it changed
	// total is what the record says the run changed, when that is more than
	// files holds: a truncated set (runstore.ChangedFiles). 0 for a whole one,
	// which is the ordinary record and the only kind the store writes.
	total int
}

// coordinatorFixture is the fixture the tests below drive: a cloud fixture
// whose runs bucket holds seeds, and the fake host the runs' pull requests are
// read from.
type coordinatorFixture struct {
	*cloudFixture
	host *fake.Provider
}

// newCoordinatorFixture seeds runs into the fixture's bucket and points the
// command's provider opener at a fake host holding prs.
func newCoordinatorFixture(t *testing.T, seeds []coordinatorRunSeed, prs ...fake.PRState) *coordinatorFixture {
	t.Helper()
	f := newCloudFixture(t)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, s := range seeds {
		repo, slug := "acme/app", appSlug
		if s.repo != "" {
			repo, slug = s.repo, mustSlug(gitprov.KindGitHub, s.repo)
		}
		store := runstore.Open(b, slug, s.id)
		spec := &task.Spec{Version: 1, RunID: s.id, Repo: repo, Ref: "main", Workflow: "web", Task: "x"}
		// A follow-up task names its pull request, its branch and the run it
		// continues — task.Validate's own rule ("branch, pr and previous_run
		// must be set together") — and a first run names none of the three.
		if s.pr != 0 {
			spec.Branch, spec.PR, spec.PreviousRun = s.branch, s.pr, s.prev
		}
		if err := store.CreateTask(ctx, spec); err != nil {
			t.Fatal(err)
		}
		if err := store.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: s.id, LaunchedBy: s.who}); err != nil {
			t.Fatal(err)
		}
		if s.status == "" {
			continue
		}
		rec := &runstore.Record{Version: 1, RunID: s.id, Repo: repo, Workflow: "web",
			Status: s.status, Branch: s.branch, BaseBranch: s.base}
		if s.prRef != 0 {
			rec.PR = &runstore.PRRef{Number: s.prRef, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, s.prRef)}
		}
		if len(s.files) > 0 {
			rec.ChangedFiles = runstore.NewChangedFiles(s.files)
			if s.total > 0 {
				// A record whose stored set is short of the one the run
				// changed: the caps in runstore.ChangedFiles fall on a run
				// that touched more paths than the record holds, and the
				// record says so (Total and Truncated) rather than passing
				// the short list off as the whole one.
				rec.ChangedFiles.Total = s.total
				rec.ChangedFiles.Truncated = true
			}
		}
		if err := store.WriteRecord(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	host := &fake.Provider{Repo: "acme/app", SelfID: "1"}
	host.State.PRs = prs
	setCoordinatorHost(t, func([]string, func(string)) (coordinator.ProviderOpener, error) {
		return func(context.Context, string, gitprov.TokenSource) (gitprov.Provider, []string, error) {
			return host, nil, nil
		}, nil
	})
	return &coordinatorFixture{cloudFixture: f, host: host}
}

// setCoordinatorHost replaces the command's provider opener for one test: the
// coordinator.ProviderOpener the reader is built with, so a test decides what
// a repository is opened as — and, because the opener is also what installs the
// coordinator's token source on a provider, what the read is authenticated
// with.
func setCoordinatorHost(t *testing.T, open func([]string, func(string)) (coordinator.ProviderOpener, error)) {
	t.Helper()
	prev := openCoordinatorProvider
	openCoordinatorProvider = open
	t.Cleanup(func() { openCoordinatorProvider = prev })
}

// setCoordinatorGitHubRoot points the coordinator's pinned GitHub API root at
// one test's hermetic host. The seam is what the command reads instead of the
// environment (D474), so this is the only way a test reaches a fake server:
// FUGARO_GITHUB_API_URL changes nothing.
func setCoordinatorGitHubRoot(t *testing.T, root string) {
	t.Helper()
	prev := coordinatorGitHubAPIURL
	coordinatorGitHubAPIURL = root
	t.Cleanup(func() { coordinatorGitHubAPIURL = prev })
}

// setCoordinatorProject is the fixture for a project-wide read: one fake host
// per repository, opened through the coordinator's own token source, which the
// opener asks for a token the way a provider's first call does — so the read
// mints, caches and reads exactly as a real one does.
//
// tokens records the token each repository was read with, whichever mint is in
// place: that is how a test says "one token per repository" without a network.
func setCoordinatorProject(t *testing.T, hosts map[string]*fake.Provider, tokens map[string]string) {
	t.Helper()
	setCoordinatorHost(t, func([]string, func(string)) (coordinator.ProviderOpener, error) {
		return func(ctx context.Context, repo string, src gitprov.TokenSource) (gitprov.Provider, []string, error) {
			host, ok := hosts[repo]
			if !ok {
				return nil, nil, fmt.Errorf("the fixture has no host for %s", repo)
			}
			token, _, err := src.Token(ctx, 2*time.Minute)
			if err != nil {
				return nil, nil, err
			}
			if tokens != nil {
				tokens[repo] = token
			}
			return host, nil, nil
		}, nil
	})
}

// setCoordinatorMint replaces the command's mint for one test, so that a read
// mints without reaching GitHub: the token is per repository, which is what the
// coordinator's own mint is keyed by (D193(A)), and a test then asserts on the
// tokens the reads were authenticated with. It returns the tokens the command
// revoked, in the order it revoked them.
//
// The revoke goes with the mint, always, and that is not a convenience: a token
// this fixture minted is not one GitHub knows, so a command that revoked it for
// real would send a request no fixture recorded — out of the test container, on
// a suite that must be hermetic. A test about the revoke replaces it again,
// after this.
func setCoordinatorMint(t *testing.T, tokens map[string]string) *[]string {
	t.Helper()
	prevMint, prevRevoke := coordinatorMint, coordinatorRevoke
	revoked := &[]string{}
	coordinatorMint = func(_ context.Context, repo string) (string, time.Time, error) {
		token := "ghs_fixture_" + strings.ReplaceAll(repo, "/", "_")
		if tokens != nil {
			tokens[repo] = token
		}
		return token, time.Now().Add(time.Hour), nil
	}
	coordinatorRevoke = func(_ context.Context, token string) error {
		*revoked = append(*revoked, token)
		return nil
	}
	t.Cleanup(func() { coordinatorMint, coordinatorRevoke = prevMint, prevRevoke })
	return revoked
}

// coordinatorPR is one pull request in the fake host's state.
func coordinatorPR(number int, branch string, state gitprov.PRState, draft bool) fake.PRState {
	return fake.PRState{
		PR:    gitprov.PR{Number: number, URL: fmt.Sprintf("https://github.com/acme/app/pull/%d", number), Draft: draft},
		Spec:  gitprov.PRSpec{Branch: branch},
		State: state,
	}
}

// coordinatorSeeds is the fixture every test below starts from: two open pull
// requests that touch one file between them, a third open one a run is still
// working on, a merged one with a run behind it, and a run that has not opened
// a pull request at all.
func coordinatorSeeds() []coordinatorRunSeed {
	return []coordinatorRunSeed{
		// The one record whose stored file set is short of what the run
		// changed: it also collides with #14 on internal/coordinator/view.go,
		// which is the point — a truncated set is what the collisions are
		// computed over, and the view says so rather than passing the short
		// list off as the whole one.
		{id: "20261013-100000-aaaa", pr: 12, prev: "20261012-090000-1111", branch: "fugaro/20261013-100000-aaaa",
			who: "someone@example.com", status: runstore.StatusSucceeded, prRef: 12, base: "main",
			files: []string{"internal/cli/exec.go", "internal/coordinator/view.go"}, total: 400},
		{id: "20261013-110000-bbbb", pr: 14, prev: "20261012-090000-2222", branch: "fugaro/20261013-110000-bbbb",
			who: "other@example.com", status: runstore.StatusSucceeded, prRef: 14, base: "main",
			files: []string{"internal/coordinator/view.go"}},
		// A run still going that has opened no pull request: a first run, so
		// its task names none.
		{id: "20261013-120000-cccc", branch: "release/v0.11.0", who: "release-bot",
			status: runstore.StatusRunning, base: "main", files: []string{"internal/cli/root.go"}},
		{id: "20261013-130000-dddd", pr: 16, prev: "20261012-090000-3333", branch: "fugaro/20261013-130000-dddd",
			who: "someone@example.com", status: runstore.StatusSucceeded, prRef: 16, base: "main",
			files: []string{"internal/coordinator/view.go"}},
		// A run that finished with no pull request: not work in flight, and not
		// in the view.
		{id: "20261013-140000-eeee", branch: "fugaro/20261013-140000-eeee", who: "someone@example.com",
			status: runstore.StatusFailed},
	}
}

func coordinatorPRs() []fake.PRState {
	return []fake.PRState{
		coordinatorPR(12, "fugaro/20261013-100000-aaaa", gitprov.PROpen, false),
		coordinatorPR(14, "fugaro/20261013-110000-bbbb", gitprov.PROpen, true),
		coordinatorPR(16, "fugaro/20261013-130000-dddd", gitprov.PRMerged, false),
		// A pull request no run names: P1's reader never asks for it, and the
		// test below says so.
		coordinatorPR(99, "topic/hand-written", gitprov.PROpen, false),
	}
}

// TestCoordinatorStatusJSON drives the whole path — the runs bucket, the host,
// the join — and reads the view as data.
func TestCoordinatorStatusJSON(t *testing.T) {
	newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	out, errOut, err := execute(t, "coordinator", "status", "--json", "--since", "0")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var v coordinatorView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if v.Repo != "acme/app" {
		t.Errorf("repo = %q, want acme/app", v.Repo)
	}
	// The in-flight running run, #12, #14, #16 (merged, so listed last).
	var labels []string
	for _, r := range v.Records {
		labels = append(labels, recordLabel(r))
	}
	want := []string{"20261013-120000-cccc", "#12", "#14", "#16"}
	if !slices.Equal(labels, want) {
		t.Errorf("records = %v, want %v", labels, want)
	}
	// The run that finished with no pull request is not work in flight.
	if slices.Contains(labels, "20261013-140000-eeee") {
		t.Error("the view lists a run that finished with no pull request")
	}
	// Two pairs, one per pair of in-flight records that touch something
	// common. The running run touched internal/cli/root.go and #12 touched
	// internal/cli/exec.go: they share the package internal/cli and no file,
	// and a pair that shares only a service is a collision — that is the rule.
	// #12 and #14 share one path. The service is the package
	// (internal/cli, internal/coordinator), never the trunk both sit under:
	// the running run and #14 share no file and no package — root.go is the
	// CLI's, view.go the coordinator's — and the view reports no collision
	// between them, which is the half of this fixture that a first-segment
	// rule gets wrong.
	var pairs []string
	for _, c := range v.Collisions {
		pairs = append(pairs, c.A+" and "+c.B)
	}
	wantPairs := []string{
		"20261013-120000-cccc and #12",
		"#12 and #14",
	}
	if !slices.Equal(pairs, wantPairs) {
		t.Errorf("collisions = %v, want %v", pairs, wantPairs)
	}
	for _, c := range v.Collisions {
		wantFiles, wantServices := []string{}, []string{"internal/cli"}
		if c.A == "#12" && c.B == "#14" {
			// The services are the ones the pair *shares*, not the union:
			// #14's one path is the coordinator's, so internal/coordinator is
			// the whole of it even though #12 also touched the CLI's.
			wantFiles = []string{"internal/coordinator/view.go"}
			wantServices = []string{"internal/coordinator"}
		}
		if !slices.Equal(c.Files, wantFiles) || !slices.Equal(c.Services, wantServices) {
			t.Errorf("collision %s and %s = files %q, services %q; want files %q, services %q",
				c.A, c.B, c.Files, c.Services, wantFiles, wantServices)
		}
	}
	// The running run is on a release branch, and the record says so.
	for _, r := range v.Records {
		if recordLabel(r) == "20261013-120000-cccc" && r.Release != "0.11.0" {
			t.Errorf("the release branch run declares release %q, want 0.11.0", r.Release)
		}
	}
	// The launcher is carried as a declaration, under a name that says so.
	if !strings.Contains(out, `"declared_by": "someone@example.com"`) {
		t.Errorf("the view does not carry the launcher as a declaration:\n%s", out)
	}
	for _, refused := range []string{`"launched_by"`, `"requested_by"`, `"identity"`} {
		if strings.Contains(out, refused) {
			t.Errorf("the view carries %s: finding #11's string is a declaration, never an identity", refused)
		}
	}
	// The merged pull request collides with nothing.
	for _, c := range v.Collisions {
		if c.A == "#16" || c.B == "#16" {
			t.Errorf("the merged #16 is in a collision: %+v", c)
		}
	}
	// The one field that qualifies everything above it: the run behind #12
	// stored a file set short of the one it changed, so the collisions the view
	// reports are the ones the paths it recorded produce, and not necessarily
	// all of them. The view says so rather than passing the short list off as
	// the whole one. Every other record's set is whole, and the key is then
	// absent rather than false — so the count is one, not four.
	truncated := 0
	for _, r := range v.Records {
		for _, run := range r.Runs {
			if !run.FilesTruncated {
				continue
			}
			truncated++
			if recordLabel(r) != "#12" {
				t.Errorf("the run %s behind %s is marked truncated, want only #12's", run.ID, recordLabel(r))
			}
		}
	}
	if truncated != 1 {
		t.Errorf("%d runs are marked truncated, want exactly 1 (#12's)", truncated)
	}
	if n := strings.Count(out, `"files_truncated": true`); n != 1 {
		t.Errorf("the view carries files_truncated %d times, want once:\n%s", n, out)
	}
}

// TestCoordinatorStatusTextGolden is the rendered view, read rather than
// accepted: it is what a person sees.
func TestCoordinatorStatusTextGolden(t *testing.T) {
	newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	out, _, err := execute(t, "coordinator", "status", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	const path = "testdata/coordinator_status.golden"
	if *updateCoordinatorGolden {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("no golden file: run go test ./internal/cli -run TestCoordinatorStatusTextGolden -update-coordinator")
	}
	if out != string(want) {
		t.Errorf("the rendered view changed (go test ./internal/cli -run TestCoordinatorStatusTextGolden -update-coordinator to accept):\ngot:\n%s\nwant:\n%s", out, want)
	}
}

// "It reads; it writes nothing, anywhere" (the plan's own sentence for P1).
// The two places this command could write are the runs bucket and the host,
// and both are checked: the bucket's objects are the same files, byte for byte
// and stamp for stamp, and the fake host's call log — which records the calls
// that write and nothing else — is empty.
func TestCoordinatorStatusWritesNothing(t *testing.T) {
	f := newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	runs := filepath.Join(f.dir, "runs")
	before := coordinatorTree(t, runs)
	if _, _, err := execute(t, "coordinator", "status", "--json", "--since", "0"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "coordinator", "status", "--since", "0"); err != nil {
		t.Fatal(err)
	}
	if after := coordinatorTree(t, runs); !slices.Equal(before, after) {
		t.Errorf("the runs bucket changed under a read:\nbefore: %v\nafter:  %v", before, after)
	}
	if calls := f.host.Snapshot().Calls; len(calls) != 0 {
		t.Errorf("the host was called to write: %+v", calls)
	}
}

// coordinatorTree is every file under dir as "path size modtime", sorted: what
// a write into the bucket would change.
func coordinatorTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d %s", filepath.ToSlash(rel), info.Size(), info.ModTime().UTC()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// The reader's pull requests are the runs bucket's own declarations: gitprov has
// no call that lists a repository's pull requests (reconciliation finding #10),
// so the numbers the runs name are the ones read, and a pull request no run
// names is not in the view. P2's project-wide credential widens what the read
// reaches and not what it asks for — it is still one number at a time, and this
// assertion is here so that changing that is a change somebody makes on purpose.
func TestCoordinatorStatusReadsOnlyThePullRequestsTheRunsName(t *testing.T) {
	f := newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	out, _, err := execute(t, "coordinator", "status", "--json", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"number": 99`) {
		t.Errorf("the view holds a pull request no run names:\n%s", out)
	}
	for i := range f.host.State.PRs {
		if f.host.State.PRs[i].Number == 99 {
			return
		}
	}
	t.Fatal("the fixture's host lost the pull request the test is about")
}

// The view is one repository's, asked for by name: a local config listing more
// than one is refused rather than guessed at, and --repo answers it.
func TestCoordinatorStatusRefusesAnUnnamedRepository(t *testing.T) {
	f := newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: [web] }\n")
	if _, _, err := execute(t, "coordinator", "status", "--since", "0"); err == nil || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("two repositories and no --repo: err = %v, want one naming --repo", err)
	}
	if _, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--json", "--since", "0"); err != nil {
		t.Fatalf("--repo acme/app: %v", err)
	}
	if _, _, err := execute(t, "coordinator", "status", "--all", "--repo", "acme/app", "--since", "0"); err == nil ||
		!strings.Contains(err.Error(), "--all") || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("--all with --repo: err = %v, want one naming both flags", err)
	}
}

// --all is the project-wide read: every repository the local config lists is
// read, one view each, each through its own credential — and the views are not
// compared across repositories, because a service is a path's shape in one tree
// and two repositories' paths say nothing about each other.
//
// The tokens the two repositories were read with are asserted to be two, which
// is D193's answer of 2026-10-21 held at the command's own level: one token per
// repository, minted for that repository.
func TestCoordinatorStatusAllReadsEveryRepository(t *testing.T) {
	f := newCoordinatorFixture(t, append(coordinatorSeeds(),
		coordinatorRunSeed{id: "20261013-150000-ffff", repo: "acme/other", pr: 21, prev: "20261012-090000-4444",
			branch: "fugaro/20261013-150000-ffff", who: "someone@example.com",
			status: runstore.StatusRunning, prRef: 21, base: "main", files: []string{"internal/api/handler.go"}},
	), coordinatorPRs()...)
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: [web] }\n")
	other := &fake.Provider{Repo: "acme/other", SelfID: "2"}
	other.State.PRs = []fake.PRState{coordinatorPR(21, "fugaro/20261013-150000-ffff", gitprov.PROpen, false)}
	tokens := map[string]string{}
	setCoordinatorProject(t, map[string]*fake.Provider{"acme/app": f.host, "acme/other": other}, tokens)
	setCoordinatorMint(t, nil)

	out, _, err := execute(t, "coordinator", "status", "--all", "--json", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	var project struct {
		Repos []coordinatorView `json:"repos"`
	}
	if err := json.Unmarshal([]byte(out), &project); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var repos []string
	for _, v := range project.Repos {
		repos = append(repos, v.Repo)
	}
	if !slices.Equal(repos, []string{"acme/app", "acme/other"}) {
		t.Fatalf("the project view holds %v, want one view per repository the config lists", repos)
	}
	var otherLabels []string
	for _, r := range project.Repos[1].Records {
		otherLabels = append(otherLabels, recordLabel(r))
	}
	if !slices.Equal(otherLabels, []string{"#21"}) {
		t.Errorf("acme/other holds %v, want the work its own runs name", otherLabels)
	}
	if project.Repos[1].Collisions == nil || len(project.Repos[1].Collisions) != 0 {
		t.Errorf("acme/other reports collisions %+v, want none: nothing it touches is in the other repository's tree", project.Repos[1].Collisions)
	}
	// One token per repository, and neither repository read with the other's.
	if tokens["acme/app"] == "" || tokens["acme/other"] == "" {
		t.Fatalf("the read held tokens %v, want one for each repository", tokens)
	}
	if tokens["acme/app"] == tokens["acme/other"] {
		t.Errorf("both repositories were read with %q, want one token each (D193)", tokens["acme/app"])
	}

	text, _, err := execute(t, "coordinator", "status", "--all", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"acme/app", "acme/other"} {
		if !strings.Contains(text, repo+": ") {
			t.Errorf("the project view does not print %s's own view:\n%s", repo, text)
		}
	}
}

// The App's credential is the strongest one this command holds — the JWT the
// mint signs and the installation tokens minted with it — and where it goes is
// pinned (D474): FUGARO_GITHUB_API_URL is the run's redirect and the
// coordinator does not read it, so nothing that can set a variable in this
// process can have the App's key spent at a host of its choosing.
//
// The environment here names a server that holds no exchange at all, so a
// single request to it fails this test; the seam names the fixture the read is
// meant to reach. The command's own wiring is used — the real opener, mint and
// revoke — so all three calls that carry the App credential are covered: the
// mint, the read those tokens authenticate, and the revoke.
func TestCoordinatorIgnoresGitHubAPIURLEnv(t *testing.T) {
	envSrv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_read_token.json"))
	realOpen := openCoordinatorProvider
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	t.Setenv(providers.EnvGitHubAPIURL, envSrv.URL)
	t.Setenv(providers.EnvCredentialsServiceURL, "")
	setCoordinatorGitHubRoot(t, srv.URL)

	newCoordinatorFixture(t, []coordinatorRunSeed{{
		id: "20261013-100000-aaaa", pr: 12, prev: "20261012-090000-1111",
		branch: "fugaro/20261013-100000-aaaa", who: "someone@example.com",
		status: runstore.StatusSucceeded, prRef: 12, base: "main",
		files: []string{"internal/cli/exec.go"},
	}})
	setCoordinatorHost(t, realOpen)

	out, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--json", "--since", "0")
	if err != nil {
		t.Fatalf("the read with a GitHub API root in the environment: %v", err)
	}
	if !strings.Contains(out, "Add search") {
		t.Errorf("the view does not carry the pull request the fixture answered with:\n%s", out)
	}
}

// The root is pinned (D474): the seam holds GitHub's own API root and the
// environment cannot move it, not even where the read is opened, because the
// provider is opened with the pinned root put in place of whatever
// FUGARO_GITHUB_API_URL the process carries.
//
// This is the half of the claim no hermetic test can watch on the wire — a
// request to api.github.com is a network call — so it is asserted where the
// value is decided, beside the test that watches the requests the environment
// cannot redirect.
func TestCoordinatorGitHubRootIsPinned(t *testing.T) {
	if coordinatorGitHubAPIURL != "https://api.github.com" {
		t.Fatalf("the coordinator's GitHub API root = %q, want %q", coordinatorGitHubAPIURL, "https://api.github.com")
	}

	env := coordinatorProviderEnv([]string{
		"PATH=/usr/bin",
		providers.EnvGitHubAPIURL + "=https://github.example",
	})
	roots := 0
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k != providers.EnvGitHubAPIURL {
			continue
		}
		roots++
		if v != coordinatorGitHubAPIURL {
			t.Errorf("the provider environment's %s = %q, want the pinned %q", k, v, coordinatorGitHubAPIURL)
		}
	}
	if roots != 1 {
		t.Errorf("the provider environment carries %s %d times, want exactly one: the pinned root", providers.EnvGitHubAPIURL, roots)
	}
	if !slices.Contains(env, "PATH=/usr/bin") {
		t.Errorf("the provider environment is %v, want the rest of it left alone", env)
	}
}

// The coordinator's credential is a named read-only set, minted per repository
// from the App the environment holds (Q3 answered (A), D193(A)): the fixture is
// a real mint's own exchanges, so what is asserted here is the request the
// command sends — one repository per token, and no permission above read.
func TestCoordinatorStatusMintsAReadOnlyTokenPerRepository(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_token.json"))
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	setCoordinatorGitHubRoot(t, srv.URL)

	f := newCoordinatorFixture(t, append(coordinatorSeeds(),
		coordinatorRunSeed{id: "20261013-150000-ffff", repo: "acme/other", pr: 21, prev: "20261012-090000-4444",
			branch: "fugaro/20261013-150000-ffff", who: "someone@example.com",
			status: runstore.StatusRunning, prRef: 21, base: "main", files: []string{"internal/api/handler.go"}},
	), coordinatorPRs()...)
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: [web] }\n")
	other := &fake.Provider{Repo: "acme/other", SelfID: "2"}
	other.State.PRs = []fake.PRState{coordinatorPR(21, "fugaro/20261013-150000-ffff", gitprov.PROpen, false)}
	tokens := map[string]string{}
	setCoordinatorProject(t, map[string]*fake.Provider{"acme/app": f.host, "acme/other": other}, tokens)

	if _, _, err := execute(t, "coordinator", "status", "--all", "--json", "--since", "0"); err != nil {
		t.Fatal(err)
	}
	if tokens["acme/app"] != "ghs_fixture/app" || tokens["acme/other"] != "ghs_fixture/other" {
		t.Errorf("the reads were authenticated with %v, want one installation token per repository, scoped to it", tokens)
	}

	// The App's ID and key are the mint's whole input, and a missing one is a
	// refusal naming the variable rather than a read that reaches GitHub. The
	// assertion is on the mint itself, because on the command's real wiring the
	// provider opener refuses a missing App ID first and this read's mint is
	// never reached (TestCoordinatorStatusReadsWithTheReadOnlyToken pins that
	// failure); only this fixture's replaced opener makes the mint the one that
	// reports.
	t.Setenv(providers.EnvGitHubAppID, "")
	if _, _, err := coordinatorMint(context.Background(), "acme/app"); fcli.ExitCode(err) != fcli.ExitUserError ||
		!strings.Contains(err.Error(), providers.EnvGitHubAppID) {
		t.Errorf("no App ID: exit %d, %v", fcli.ExitCode(err), err)
	}
}

// TestCoordinatorStatusReadsWithTheReadOnlyToken drives the command's own
// wiring — no opener replaced and no mint replaced — against a hermetic host,
// and holds the read to the credential P2 mints for it.
//
// The fixture is the whole assertion: the mint's request body is the two-read
// set, and the pull-request read that follows carries that token's own
// Authorization header. A read authenticated with the run's set — what the
// provider's own mint asks for, two writes among them — fails here on the
// fixture's Authorization check, so the install on the provider is verified by
// the token the provider actually used and not by the code path that installed
// it.
func TestCoordinatorStatusReadsWithTheReadOnlyToken(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_read_token.json"))
	realOpen := openCoordinatorProvider
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	setCoordinatorGitHubRoot(t, srv.URL)
	// Opened on the App's own credential rather than keyless through the
	// credentials service: what is under test here is what the coordinator
	// mints.
	t.Setenv(providers.EnvCredentialsServiceURL, "")

	newCoordinatorFixture(t, []coordinatorRunSeed{{
		id: "20261013-100000-aaaa", pr: 12, prev: "20261012-090000-1111",
		branch: "fugaro/20261013-100000-aaaa", who: "someone@example.com",
		status: runstore.StatusSucceeded, prRef: 12, base: "main",
		files: []string{"internal/cli/exec.go"},
	}})
	// The fixture above put a fake host in the opener's place; this test is
	// about the command's own opener, so it goes back.
	setCoordinatorHost(t, realOpen)

	out, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--json", "--since", "0")
	if err != nil {
		t.Fatalf("the read through the coordinator's own credential: %v", err)
	}
	if !strings.Contains(out, "Add search") {
		t.Errorf("the view does not carry the pull request the host answered with:\n%s", out)
	}

	// With no App credential the opener refuses before this read's own mint is
	// reached, and that refusal is the CALLER'S: an environment that does not
	// carry the App credential is the person's to fix, and it is exit 1 here
	// exactly as it is where the mint asks for the same credential
	// (coordinatorAppCredential, asserted in
	// TestCoordinatorStatusMintsAReadOnlyTokenPerRepository). The opener asks
	// for it itself (openCoordinatorProvider) rather than letting
	// providers.FromEnv's plain error travel as a failure through the host.
	t.Setenv(providers.EnvGitHubAppID, "")
	if _, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0"); err == nil ||
		!strings.Contains(err.Error(), providers.EnvGitHubAppID) {
		t.Errorf("no App ID: %v, want a failure naming %s", err, providers.EnvGitHubAppID)
	} else if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("no App ID: exit %d, want %d: the environment the caller holds the credential in is the caller's to fix",
			fcli.ExitCode(err), fcli.ExitUserError)
	}
	// The App's key is the same refusal, from the other side: the ID is there
	// and the key is not, and that too is the caller's own (exit 1).
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, "")
	t.Setenv(providers.EnvGitHubAppKeyFile, filepath.Join(t.TempDir(), "no-such-key.pem"))
	if _, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0"); err == nil ||
		!strings.Contains(err.Error(), providers.EnvGitHubAppKeyFile) {
		t.Errorf("no App key: %v, want a failure naming %s", err, providers.EnvGitHubAppKeyFile)
	} else if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("no App key: exit %d, want %d", fcli.ExitCode(err), fcli.ExitUserError)
	}
}

// A key that is there and is not a key is the caller's own failure, wherever
// this command reads it. The credential is asked for in two places -- the
// provider opener asks for it before it opens anything (so that a person who has
// not exported it is not told the host failed), and the mint reads it for the
// token -- and the same environment has to answer the same way from both: a
// parse left to providers.FromEnv made a malformed key exit 2 through the opener
// and 1 through the mint, so a person whose key is not a key would be told the
// host failed and would retry it.
//
// The fixture holds no exchange at all, which is the other half of the claim:
// neither path may reach GitHub to find this out, and a request that arrived
// would fail the test rather than the network being asked.
func TestCoordinatorStatusRefusesAMalformedAppKeyAsTheCallers(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	realOpen := openCoordinatorProvider
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, "this is not a PEM block")
	setCoordinatorGitHubRoot(t, srv.URL)

	newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	setCoordinatorHost(t, realOpen)
	_, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0")
	if err == nil {
		t.Fatalf("the App's key is not a key and the command succeeded")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("a malformed App key: exit %d, want %d: the environment the caller holds the key in is the caller's to fix, exactly as it is when the key is missing",
			fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), "private key") {
		t.Errorf("a malformed App key: %v, want a failure naming the key", err)
	}

	// The mint is the second place the same credential is read, and it answers
	// the same way. The opener's refusal above is what a person meets; this is
	// the assertion that the two halves of the command cannot disagree, because
	// it is the one this command's own wiring does not reach first.
	if _, _, err := coordinatorMint(context.Background(), "acme/app"); fcli.ExitCode(err) != fcli.ExitUserError ||
		!strings.Contains(err.Error(), "private key") {
		t.Errorf("the mint with a malformed App key: exit %d, %v, want exit %d naming the key", fcli.ExitCode(err), err, fcli.ExitUserError)
	}
}

// With the installation's credentials service on, the App's key is the
// service's (C2.2, D145) and this process holds none. This command mints its
// own read-only token from that key, so there is no credential here to work
// with -- and a key read anyway would be one the environment says is not this
// process's to hold, which is the rule providers.FromEnv keeps for the run path
// and the one the coordinator's own mint has to keep too. The service mints a
// RUN's token and this read is not a run, so there is nothing to ask it for
// either: the command is refused rather than served the wrong credential.
//
// The refusal is the caller's (exit 1), because the environment they are
// running in is theirs, and it happens before anything is opened or minted --
// the empty fixture is here to prove that: a key present in the environment
// beside the service URL changes nothing.
func TestCoordinatorStatusRefusesToMintWhenTheServiceHoldsTheKey(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	realOpen := openCoordinatorProvider
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	setCoordinatorGitHubRoot(t, srv.URL)
	t.Setenv(providers.EnvCredentialsServiceURL, "https://credentials.example")

	newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	setCoordinatorHost(t, realOpen)
	_, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0")
	if err == nil {
		t.Fatalf("the credentials service holds the App's key and the command read one anyway")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the service holds the key: exit %d, want %d: the environment the caller is running in is the caller's to fix",
			fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), providers.EnvCredentialsServiceURL) {
		t.Errorf("the service holds the key: %v, want a failure naming %s", err, providers.EnvCredentialsServiceURL)
	}

	// The mint is the second place the credential is read, and it answers the
	// same way: the opener's refusal above is what a person meets, and this is
	// the assertion that the two halves of the command cannot disagree.
	if _, _, err := coordinatorMint(context.Background(), "acme/app"); fcli.ExitCode(err) != fcli.ExitUserError ||
		!strings.Contains(err.Error(), providers.EnvCredentialsServiceURL) {
		t.Errorf("the mint with the service holding the key: exit %d, %v, want exit %d naming %s",
			fcli.ExitCode(err), err, fcli.ExitUserError, providers.EnvCredentialsServiceURL)
	}
}

// The credential lives for the read and no longer: every token this command
// minted is taken back when it is done, in the order it minted them, and the
// revoke happens on EVERY return — a command that failed on its second
// repository has still left the first one's token behind, and that is the one
// nothing else would ever take back.
//
// The mint and the revoke are both replaced here (setCoordinatorMint), so what
// this asserts is the command's own bookkeeping; the two fixture tests above
// are what hold the request itself — the method, the path and the token's own
// Authorization header — to GitHub's API.
func TestCoordinatorStatusRevokesEveryTokenItMinted(t *testing.T) {
	f := newCoordinatorFixture(t, append(coordinatorSeeds(),
		coordinatorRunSeed{id: "20261013-150000-ffff", repo: "acme/other", pr: 21, prev: "20261012-090000-4444",
			branch: "fugaro/20261013-150000-ffff", who: "someone@example.com",
			status: runstore.StatusRunning, prRef: 21, base: "main", files: []string{"internal/api/handler.go"}},
	), coordinatorPRs()...)
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: [web] }\n")
	other := &fake.Provider{Repo: "acme/other", SelfID: "2"}
	other.State.PRs = []fake.PRState{coordinatorPR(21, "fugaro/20261013-150000-ffff", gitprov.PROpen, false)}
	setCoordinatorProject(t, map[string]*fake.Provider{"acme/app": f.host, "acme/other": other}, nil)
	revoked := setCoordinatorMint(t, nil)

	if _, _, err := execute(t, "coordinator", "status", "--all", "--since", "0"); err != nil {
		t.Fatal(err)
	}
	want := []string{"ghs_fixture_acme_app", "ghs_fixture_acme_other"}
	if !slices.Equal(*revoked, want) {
		t.Errorf("the command revoked %v, want every token it minted, in the order it minted them (%v)", *revoked, want)
	}

	// A project with nothing in flight mints nothing — a repository whose runs
	// name no pull request is never opened, so the mint is never reached — and
	// the command then revokes nothing: a revoke is a token taken back, never a
	// call made for the look of it.
	*revoked = nil
	newCoordinatorFixture(t, nil)
	if _, _, err := execute(t, "coordinator", "status", "--all", "--since", "0"); err != nil {
		t.Fatal(err)
	}
	if len(*revoked) != 0 {
		t.Errorf("a read with nothing in flight revoked %v, want nothing: no token was minted to take back", *revoked)
	}
}

// A command that failed still revokes what it minted: the token of a repository
// it read before the failure is the one nothing else would take back.
func TestCoordinatorStatusRevokesAfterAFailedRead(t *testing.T) {
	newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	// A host with no state: the first pull request the runs name is refused
	// after the mint, so the read fails with the token already handed out.
	setCoordinatorProject(t, map[string]*fake.Provider{"acme/app": {Repo: "acme/app", SelfID: "1"}}, nil)
	revoked := setCoordinatorMint(t, nil)

	if _, _, err := execute(t, "coordinator", "status", "--since", "0"); err == nil {
		t.Fatal("the read failed and the command succeeded")
	}
	if !slices.Equal(*revoked, []string{"ghs_fixture_acme_app"}) {
		t.Errorf("the failed command revoked %v, want the token it minted before the failure", *revoked)
	}
}

// A revoke that fails is not a read that failed: the token is left to expire on
// its own within the hour, the command exits as it would have, and nothing
// about it is printed — neither the failure nor the token.
func TestCoordinatorStatusSurvivesARefusedRevoke(t *testing.T) {
	const refusal = "the host refused the revoke for ghs_0123456789abcdefghij"
	f := newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	setCoordinatorProject(t, map[string]*fake.Provider{"acme/app": f.host}, nil)
	setCoordinatorMint(t, nil)
	prev := coordinatorRevoke
	coordinatorRevoke = func(context.Context, string) error { return errors.New(refusal) }
	t.Cleanup(func() { coordinatorRevoke = prev })

	out, errOut, err := execute(t, "coordinator", "status", "--since", "0")
	if err != nil {
		t.Fatalf("the revoke was refused and the command failed: %v", err)
	}
	for _, got := range []string{out, errOut} {
		if strings.Contains(got, "refused") || strings.Contains(got, "ghs_0123456789abcdefghij") {
			t.Errorf("a refused revoke reached the output: %q", got)
		}
	}
}

// An interrupted read still takes its tokens back. The command's context is not
// the revoke's to obey: a person who stops the read has been handed a token per
// repository it got to, and those are the ones nobody would ever take back — the
// credential outliving its use is the whole of what this revoke exists against.
// The revoke runs on the command's context detached and bounded, so a cancel
// that has already happened is not a revoke that does not happen.
func TestCoordinatorTokensRevokeOutlivesTheCommand(t *testing.T) {
	revoked := setCoordinatorMint(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tokens := &coordinatorTokens{}
	if _, _, err := tokens.mint(ctx, "acme/app"); err != nil {
		t.Fatal(err)
	}
	tokens.revoke(ctx)
	if !slices.Equal(*revoked, []string{"ghs_fixture_acme_app"}) {
		t.Errorf("an interrupted read revoked %v, want the token it had already minted", *revoked)
	}
}

// coordinatorSource is a TokenSource whose answer a test decides.
type coordinatorSource struct{ token string }

func (s coordinatorSource) Token(context.Context, time.Duration) (string, time.Time, error) {
	return s.token, time.Now().Add(time.Hour), nil
}

// installCoordinatorSource is where P2's credential is enforced on a provider:
// a provider that can be handed the coordinator's read-only source is, and one
// that cannot is refused rather than left to mint the run's own set. The
// refusal is the half a hermetic read cannot reach — providers.FromEnv only
// ever opens a provider that takes a source — so it is asserted here.
func TestCoordinatorInstallsItsOwnSourceOnTheProvider(t *testing.T) {
	src := coordinatorSource{token: "ghs_fixture_read_only"}
	// The gitprov fake cannot be handed a source: a provider like it would mint
	// its own token on first use, and that token asks for the run's set (Q3's
	// answer, which this read may not reach).
	if err := installCoordinatorSource(&fake.Provider{}, src); err == nil ||
		!strings.Contains(err.Error(), "read-only token source") {
		t.Errorf("a provider that cannot be handed a source: err = %v, want a refusal", err)
	}
	p, err := github.New(github.Options{Owner: "acme", Repo: "app", AppID: "12345", ServiceHeldKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := installCoordinatorSource(p, src); err != nil {
		t.Fatalf("installing the coordinator's read-only source: %v", err)
	}
	a, err := p.GitAuth(context.Background(), 0)
	if err != nil {
		t.Fatalf("GitAuth with the source installed: %v", err)
	}
	if a.Token != src.token || a.Env["GH_TOKEN"] != src.token {
		t.Errorf("the provider authenticated with %+v, want the installed read-only token", a)
	}
}

// A project holding a repository this read does not serve is refused, and the
// refusal names the reason and not the repository (reconciliation Q12 (A),
// finding #30): a bitbucket credential is one repository's access token and
// there is nothing to widen it to.
func TestCoordinatorStatusRefusesABitbucketProject(t *testing.T) {
	f := newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	if _, _, err := execute(t, "coordinator", "status", "--all", "--since", "0"); fcli.ExitCode(err) != fcli.ExitUserError ||
		!strings.Contains(err.Error(), "cannot be widened to a project") {
		t.Fatalf("a bitbucket repository in the project: exit %d, %v", fcli.ExitCode(err), err)
	} else if strings.Contains(err.Error(), "legacy") {
		t.Errorf("the refusal names the repository rather than the reason: %v", err)
	}
}

// secretErrHost fails every read of a pull request with an error carrying the
// credential, which is what a real host's 401 does.
type secretErrHost struct {
	*fake.Provider
	secret string
}

func (h secretErrHost) PullRequest(context.Context, int) (gitprov.PRInfo, error) {
	return gitprov.PRInfo{}, fmt.Errorf("GET /repos/acme/app/pulls/12: 401: bad credentials (token %s)", h.secret)
}

// The one security rule on this path: the credential values the provider
// opener hands back are redacted out of anything this command prints, whether
// the failure came from opening the provider or from a read through it.
func TestCoordinatorStatusNeverPrintsACredential(t *testing.T) {
	const secret = "ghs_0123456789abcdefghij"
	for _, tc := range []struct {
		name string
		open func([]string, func(string)) (coordinator.ProviderOpener, error)
	}{
		{"opening the provider", func([]string, func(string)) (coordinator.ProviderOpener, error) {
			return func(context.Context, string, gitprov.TokenSource) (gitprov.Provider, []string, error) {
				return nil, []string{secret}, fmt.Errorf("github: the App credential %s was refused", secret)
			}, nil
		}},
		{"reading a pull request", func([]string, func(string)) (coordinator.ProviderOpener, error) {
			host := &fake.Provider{Repo: "acme/app", SelfID: "1"}
			return func(context.Context, string, gitprov.TokenSource) (gitprov.Provider, []string, error) {
				return secretErrHost{Provider: host, secret: secret}, []string{secret}, nil
			}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
			setCoordinatorHost(t, tc.open)
			out, errOut, err := execute(t, "coordinator", "status", "--since", "0")
			if err == nil {
				t.Fatalf("the read failed and the command succeeded: %s%s", out, errOut)
			}
			for _, got := range []string{err.Error(), out, errOut} {
				if strings.Contains(got, secret) {
					t.Errorf("the credential reached the output: %q", got)
				}
			}
			if !strings.Contains(err.Error(), "[REDACTED]") {
				t.Errorf("the failure is %q, want the credential redacted in it", err)
			}
		})
	}
}

// The coordinator's own mint is the half of this command that holds the App's
// private key, and a failure it reports must not carry one: the key is redacted
// out of whatever the mint says (agent.Redact). The host here answers the
// installation lookup with an error, so the read fails the way an unreachable
// or refusing API does and the key is read from the environment on the way.
func TestCoordinatorStatusNeverPrintsTheAppKey(t *testing.T) {
	const secret = "ghs_0123456789abcdefghij"
	pem := appKeyPEM(t)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_mint_refused.json"))
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, pem)
	setCoordinatorGitHubRoot(t, srv.URL)

	newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	// The real coordinatorMint, against a host that refuses: the key is read
	// from the environment on the way and must not reach what is printed.
	setCoordinatorProject(t, map[string]*fake.Provider{"acme/app": {Repo: "acme/app", SelfID: "1"}}, nil)
	out, errOut, err := execute(t, "coordinator", "status", "--since", "0")
	if err == nil {
		t.Fatalf("the mint was refused and the command succeeded: %s%s", out, errOut)
	}
	for _, got := range []string{err.Error(), out, errOut} {
		if strings.Contains(got, pem) || strings.Contains(got, secret) {
			t.Errorf("a credential reached the output: %q", got)
		}
	}
	if !strings.Contains(err.Error(), "github") {
		t.Errorf("the failure is %q, want the mint's own failure reported", err)
	}
}

// An empty bucket is an empty view, not a failure: nothing is in flight, and
// the command says so without asking the host anything.
func TestCoordinatorStatusWithNoRuns(t *testing.T) {
	newCoordinatorFixture(t, nil)
	out, _, err := execute(t, "coordinator", "status", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0 records, 0 in flight") || !strings.Contains(out, "declared by") {
		t.Errorf("the empty view is:\n%s", out)
	}
}

// status reads runs ls's window, measured back from runs ls's clock
// (coordinatorSince), so a run older than the window is not in the view unless
// the window is opened.
func TestCoordinatorStatusWindow(t *testing.T) {
	recent := recentRunID(t, time.Minute, "feed")
	old := "20200101-000000-dddd" // runid-guard:allow
	newCoordinatorFixture(t, []coordinatorRunSeed{
		{id: recent, branch: "topic/recent", who: "someone@example.com"},
		{id: old, branch: "topic/old", who: "someone@example.com"},
	})
	labels := func(args ...string) []string {
		t.Helper()
		out, _, err := execute(t, append([]string{"coordinator", "status", "--json"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var v coordinatorView
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		var got []string
		for _, r := range v.Records {
			got = append(got, recordLabel(r))
		}
		return got
	}
	if got := labels(); !slices.Equal(got, []string{recent}) {
		t.Errorf("the default window holds %v, want only the run inside it (%s)", got, recent)
	}
	if got := labels("--since", "0"); !slices.Equal(got, []string{old, recent}) {
		t.Errorf("--since 0 holds %v, want both runs", got)
	}
}

// A run is on the pull request its own record names, and on the one its task
// names only when the record names none — runs.go's own rule (onPR), so the
// two views of one run never disagree about which pull request it is on.
func TestCoordinatorStatusFollowsTheRecordsPullRequest(t *testing.T) {
	newCoordinatorFixture(t,
		[]coordinatorRunSeed{
			// The record's pull request wins over the task's.
			{id: "20261013-100000-aaaa", pr: 12, prev: "20261012-090000-1111", branch: "fugaro/20261013-100000-aaaa",
				who: "someone@example.com", status: runstore.StatusSucceeded, prRef: 13, base: "main"},
			// And a run whose record names no pull request takes the task's.
			{id: "20261013-110000-bbbb", pr: 12, prev: "20261012-090000-2222", branch: "fugaro/20261013-110000-bbbb",
				who: "other@example.com", status: runstore.StatusRunning, base: "main"},
		},
		coordinatorPR(12, "fugaro/20261013-100000-aaaa", gitprov.PROpen, false),
		coordinatorPR(13, "fugaro/20261013-100000-aaaa", gitprov.PROpen, false),
	)
	out, _, err := execute(t, "coordinator", "status", "--json", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	var v coordinatorView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var labels []string
	for _, r := range v.Records {
		labels = append(labels, recordLabel(r))
	}
	if !slices.Equal(labels, []string{"#12", "#13"}) {
		t.Fatalf("records = %v, want #12 and #13", labels)
	}
	for _, r := range v.Records {
		var ids []string
		for _, run := range r.Runs {
			ids = append(ids, run.ID)
		}
		want := []string{"20261013-100000-aaaa"}
		if recordLabel(r) == "#12" {
			want = []string{"20261013-110000-bbbb"}
		}
		if !slices.Equal(ids, want) {
			t.Errorf("%s holds runs %v, want %v: the record's pull request comes first, the task's second",
				recordLabel(r), ids, want)
		}
	}
}

// Everything a row carries is somebody else's text — a branch name, a
// launcher's own string — and none of it may reach the terminal raw: a
// launcher that writes an escape, a carriage return or a megabyte about
// itself must not be able to redraw the table around it.
func TestCoordinatorStatusSanitisesWhatSomebodyElseWrote(t *testing.T) {
	hostile := "bob@example.com\x1b[2J\x1b[31mnot really bob\x07\r" + strings.Repeat("x", 4096)
	newCoordinatorFixture(t,
		[]coordinatorRunSeed{{id: "20261013-100000-aaaa", pr: 12, branch: "topic/" + strings.Repeat("b", 4096),
			who: hostile, status: runstore.StatusRunning, prRef: 12, base: "main"}},
		coordinatorPR(12, "topic/"+strings.Repeat("b", 4096), gitprov.PROpen, false),
	)
	out, _, err := execute(t, "coordinator", "status", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.IndexAny(out, "\x1b\x07\r"); i >= 0 {
		t.Errorf("the table carries a control character at offset %d: %q", i, out[max(0, i-40):min(len(out), i+40)])
	}
	for _, line := range strings.Split(out, "\n") {
		if n := len([]rune(line)); n > 4*coordinatorMaxCellRunes {
			t.Errorf("a line of the table is %d runes wide, want a cell to be bounded at %d: %q", n, coordinatorMaxCellRunes, line)
		}
	}
	if !strings.Contains(out, "bob@example.com") {
		t.Errorf("the declared launcher is not in the table at all:\n%s", out)
	}
}

// The same rule reaches the document and not only the table. The table's own
// cell cleans whatever it is handed (coordinatorCell -> cellOneLine), so a view
// that carried the launcher's string raw would still render a clean table —
// while --json, which is a document somebody parses and not cells somebody
// looks at, handed the raw bytes on. The sanitisation is therefore the view's
// (coordinatorRun, safetext.Field) and this is the half that shows it.
func TestCoordinatorStatusJSONSanitisesTheDeclaration(t *testing.T) {
	hostile := "bob@example.com\x1b[2J\x1b[31mnot really bob\x07\r" + strings.Repeat("x", 8192)
	newCoordinatorFixture(t, []coordinatorRunSeed{
		{id: "20261013-100000-aaaa", branch: "topic/hostile", who: hostile, status: runstore.StatusRunning},
	})
	out, _, err := execute(t, "coordinator", "status", "--json", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	var v coordinatorView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	runs := 0
	for _, r := range v.Records {
		for _, run := range r.Runs {
			runs++
			if i := strings.IndexAny(run.DeclaredBy, "\x1b\x07\r\n\t"); i >= 0 {
				t.Errorf("declared_by carries a control character at offset %d: %q: the launcher's own string reached the document raw", i, run.DeclaredBy)
			}
			if n := utf8.RuneCountInString(run.DeclaredBy); n > safetext.FieldMax {
				t.Errorf("declared_by is %d runes, want it bounded at %d", n, safetext.FieldMax)
			}
			if !strings.HasPrefix(run.DeclaredBy, "bob@example.com") {
				t.Errorf("declared_by = %q, want the launcher's own text at the front of it: the field is bounded and made safe, not dropped", run.DeclaredBy)
			}
		}
	}
	if runs != 1 {
		t.Fatalf("the view holds %d runs, want the one the fixture seeded", runs)
	}
}

// A run whose own objects are garbled is still a run, and still work in
// flight: task.json and result.json are written by a runner that can die
// mid-write, and dropping the work because its record is unreadable is the one
// thing this view must not do. The run is listed, labelled by its ID (nothing
// readable named a pull request), in flight (nothing readable said it
// finished), and the read's own warning goes to stderr rather than into the
// view. The command succeeds: one run's corrupt object is one run's row and not
// the listing's failure.
func TestCoordinatorStatusSurvivesGarbledRunObjects(t *testing.T) {
	const id = "20261013-100000-aaaa"
	f := newCoordinatorFixture(t, []coordinatorRunSeed{
		{id: id, branch: "topic/garbled", who: "someone@example.com", status: runstore.StatusRunning, base: "main"},
	})
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, name := range []string{"task.json", "result.json"} {
		// A write that stopped half way through: valid JSON's opening and
		// nothing else.
		if err := b.WriteAll(ctx, "runs/"+appSlug+"/"+id+"/"+name, []byte(`{"version": 1, "task": `), nil); err != nil {
			t.Fatalf("garbling %s: %v", name, err)
		}
	}
	out, errOut, err := execute(t, "coordinator", "status", "--json", "--since", "0")
	if err != nil {
		t.Fatalf("the run's corrupt objects failed the command: %v (stderr: %s)", err, errOut)
	}
	var v coordinatorView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(v.Records) != 1 || recordLabel(v.Records[0]) != id {
		t.Fatalf("records = %+v, want the one record labelled %s: a run whose objects are unreadable is still work in flight", v.Records, id)
	}
	if !v.Records[0].InFlight {
		t.Error("the run with no readable record is not in flight: nothing readable said it finished, and the safe direction is to report it")
	}
	if !strings.Contains(errOut, id) || !strings.Contains(errOut, "unreadable") {
		t.Errorf("stderr = %q, want the read's own warning naming %s and the unreadable object", errOut, id)
	}
}

// coordinatorView is the JSON the command prints, for the tests' own reads:
// the same field names the view's types carry, spelled here so that a rename
// in one place breaks a test in the other.
type coordinatorView struct {
	Repo       string              `json:"repo"`
	Records    []coordinatorRecord `json:"records"`
	Collisions []coordinatorPair   `json:"collisions"`
}

// coordinatorRecord is one record of the printed view.
type coordinatorRecord struct {
	PR struct {
		Number int `json:"number"`
	} `json:"pr"`
	Runs []struct {
		ID             string `json:"id"`
		DeclaredBy     string `json:"declared_by"`
		FilesTruncated bool   `json:"files_truncated"`
	} `json:"runs"`
	Release  string   `json:"release"`
	InFlight bool     `json:"in_flight"`
	Files    []string `json:"files"`
}

// coordinatorPair is one collision of the printed view.
type coordinatorPair struct {
	A        string   `json:"a"`
	B        string   `json:"b"`
	Files    []string `json:"files"`
	Services []string `json:"services"`
}

// recordLabel is the view's own name for a record, recomputed from the JSON
// rather than read from a field the view does not print.
func recordLabel(r coordinatorRecord) string {
	if r.PR.Number > 0 {
		return fmt.Sprintf("#%d", r.PR.Number)
	}
	if len(r.Runs) > 0 {
		return r.Runs[0].ID
	}
	return "-"
}

// The report path, as P4 builds it: `fugaro coordinator report` writes one
// intent record under the repository's node, through the caller's own
// credentials and the coordinator's own store, and prints it. These tests
// drive it through a real (fake) database, and they are the plan's red steps
// at the command's own level:
//
//   - TestCoordinatorReportWritesTheIntent: the record, the path it is written
//     to and the document itself, read back out of the database.
//   - TestCoordinatorReportIsIdempotent: one declaration, reported twice, is
//     ONE document — the mutation being an ID with the wall clock in it.
//   - TestCoordinatorReportRefusesACallerTheProjectCannotName: the refusal,
//     and that it leaves no document behind.
//   - TestCoordinatorReportRefusesADeclarationThatDeclaresNothing: the other
//     refusal.
//   - TestCoordinatorReportPrintsNoAdvice: the part's third red step at the
//     command — the output is the record and nothing else. The half of it that
//     is about reading nothing back is the writer's own dependency list
//     (ee/coordinator's TestReportReadsNothingBack), because a command
//     cannot read back through a writer that has no read.
//   - TestCoordinatorReportRefusesAWriteTheRulesDeny: a database that refuses
//     the write is the caller's error and not an outage, and it says what the
//     write needs.
//   - TestCoordinatorReportNeedsADatabase: a project with none is refused
//     naming the command that deploys one.
//   - TestCoordinatorReportText is the same record as the text a person reads,
//     which is the record and nothing else.
//   - TestDeclareWritesTheLaunchsIntent: the writer the launch path reaches
//     through the build's declarer is the same one this command uses, so a
//     launch declares itself without a second command.
//   - TestDeclareWithNoBranchDeclaresTheRunsOwn: the branch a launch declares
//     is the one it is about to work on, and a spec that names none gets the
//     run's own.
//   - TestAFailedDeclareIsTheCallersError: the coordinator is advisory, so the
//     writer answers the failure and the launch decides what to do with it --
//     one warning line and never a failed run (internal/cli's declareLaunch).
//   - TestADeclareAgainstARefusingDatabaseAnswersTheRefusal: a write the rules
//     refuse, which is what a launcher who is not a budget admin gets, is the
//     refusal the launch is quiet about.
//   - TestADeclareThatSkipsTheDatabaseDeclaresNothing: the declaration follows
//     the launch's own database switch, so a launch told not to use the
//     budget's database is not the one call that reaches it.

// coordinatorReportFixture is a cloud fixture whose project has the
// coordinator's database: an RTDB fake, reached the way the budget commands
// reach theirs (budget.rtdb_url, and no credentials in a test).
type coordinatorReportFixture struct {
	*cloudFixture
	db *gcpfake.RTDB
}

func newCoordinatorReportFixture(t *testing.T) *coordinatorReportFixture {
	t.Helper()
	f := &coordinatorReportFixture{cloudFixture: newCloudFixture(t), db: gcpfake.NewRTDB(t)}
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+f.db.URL+" }\n")
	return f
}

// intents is the intent node's children of the fixture's repository, as the
// fake database holds them: the record count the idempotency test is about.
func (f *coordinatorReportFixture) intents() map[string]any {
	m, _ := f.db.Value(coordinator.PathIntents(appSlug)).(map[string]any)
	return m
}

// oneIntent is the single record the node holds, failing the test when there
// is not exactly one.
func (f *coordinatorReportFixture) oneIntent(t *testing.T) coordinator.Intent {
	t.Helper()
	m := f.intents()
	if len(m) != 1 {
		t.Fatalf("the intent node holds %d documents, want 1: %v", len(m), m)
	}
	var rec coordinator.Intent
	for _, v := range m {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
	}
	return rec
}

func TestCoordinatorReportWritesTheIntent(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	out, _, err := execute(t, "coordinator", "report", "--repo", "acme/app",
		"--branch", "release/v0.11.0",
		"--file", "internal/cli/run.go", "--file", "internal/coordinator/intent.go",
		"--service", "internal/cli", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rec coordinator.Intent
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("the document does not parse: %v (%s)", err, out)
	}
	// The caller is the local config's user:, and the release is the one the
	// branch names rather than a second flag.
	if rec.DeclaredBy != "someone@example.com" || rec.Branch != "release/v0.11.0" || rec.Release != "0.11.0" {
		t.Errorf("the record = %+v, want the caller and the branch's release", rec)
	}
	if rec.At.IsZero() {
		t.Error("the record carries no clock: when a declaration was made is part of it")
	}
	if !slices.Equal(rec.Files, []string{"internal/cli/run.go", "internal/coordinator/intent.go"}) ||
		!slices.Equal(rec.Services, []string{"internal/cli"}) {
		t.Errorf("the record's declaration = %v / %v", rec.Files, rec.Services)
	}
	// The document is the record, at the path D195 puts it under.
	if got := f.oneIntent(t); got.DeclaredBy != rec.DeclaredBy || !got.At.Equal(rec.At) {
		t.Errorf("the stored record = %+v, want the one printed (%+v)", got, rec)
	}
	if want := coordinator.PathIntent(appSlug, coordinator.IntentID(rec)); f.db.Value(want) == nil {
		t.Errorf("nothing was written at %q", want)
	}
}

func TestCoordinatorReportText(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	out, _, err := execute(t, "coordinator", "report", "--repo", "acme/app",
		"--branch", "release/v0.11.0", "--file", "internal/cli/run.go")
	if err != nil {
		t.Fatal(err)
	}
	rec := f.oneIntent(t)
	path := coordinator.PathIntent(appSlug, coordinator.IntentID(rec))
	for _, want := range []string{
		"reported /" + path,
		"declared by someone@example.com at " + rec.At.Format(time.RFC3339),
		"branch release/v0.11.0",
		"release 0.11.0",
		"files internal/cli/run.go",
		"services none",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
}

// TestCoordinatorReportIsIdempotent is the plan's idempotency red step at the
// command: the same declaration from the same caller twice writes one
// document. The two runs carry different clocks — that is the mutation this is
// written for (an ID with the wall clock in it writes two) — and the second
// spells its paths in another order, because the order is not part of what was
// declared.
func TestCoordinatorReportIsIdempotent(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	args := []string{"coordinator", "report", "--repo", "acme/app", "--branch", "release/v0.11.0",
		"--file", "internal/cli/run.go", "--file", "internal/coordinator/intent.go"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	first := f.oneIntent(t)
	args[7], args[9] = args[9], args[7]
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	if n := len(f.intents()); n != 1 {
		t.Fatalf("two reports of one declaration wrote %d documents, want 1", n)
	}
	second := f.oneIntent(t)
	if !second.At.After(first.At) && !second.At.Equal(first.At) {
		t.Errorf("the second report kept the first clock (%v then %v): the record was not rewritten", first.At, second.At)
	}
	// A different declaration is a second document: the ID is a function of
	// the declaration, not of the caller alone.
	if _, _, err := execute(t, "coordinator", "report", "--repo", "acme/app", "--branch", "release/v0.11.0"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.intents()); n != 2 {
		t.Fatalf("a second declaration wrote %d documents in total, want 2", n)
	}
}

// TestCoordinatorReportRefusesACallerTheProjectCannotName: the refusal, and
// that it is a refusal rather than a document nobody can be named for. The
// identity is neither the config's nor git's — the config's user: is gone and
// an empty GIT_DIR is not a repository — so the command cannot tell who is
// declaring.
func TestCoordinatorReportRefusesACallerTheProjectCannotName(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	userStatsPlace(t, f.cloudFixture, "", nil, nil)
	testutil.IsolateGit(t)
	t.Setenv("GIT_DIR", t.TempDir())
	_, _, err := execute(t, "coordinator", "report", "--repo", "acme/app", "--branch", "fugaro/x")
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Fatalf("a report from an unnamed caller = %v (exit %d), want a user error", err, fcli.ExitCode(err))
	}
	for _, want := range []string{"user in the local config", "git config user.email"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say what is missing (%q)", err, want)
		}
	}
	if n := len(f.intents()); n != 0 {
		t.Errorf("the refused report wrote %d documents", n)
	}
}

// TestCoordinatorReportRefusesADeclarationThatDeclaresNothing: the second
// refusal, and it names what the caller has to give it.
func TestCoordinatorReportRefusesADeclarationThatDeclaresNothing(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	_, _, err := execute(t, "coordinator", "report", "--repo", "acme/app")
	if fcli.ExitCode(err) != fcli.ExitUserError || !strings.Contains(err.Error(), "declare something") {
		t.Fatalf("an empty declaration = %v (exit %d)", err, fcli.ExitCode(err))
	}
	if n := len(f.intents()); n != 0 {
		t.Errorf("the refused report wrote %d documents", n)
	}
}

// TestCoordinatorReportPrintsNoAdvice: the plan's third red step at the
// command. The output is the record and nothing else — no collision, no
// suggestion, no summary of what is already in flight — because the report
// path takes nothing back and a command that advised from what it wrote would
// be advising from nothing. The reading half of the same rule is
// ee/coordinator's TestReportReadsNothingBack, asserted against the
// writer's own dependency list.
//
// Hand mutation this fails on: printing a collision summary under the record.
func TestCoordinatorReportPrintsNoAdvice(t *testing.T) {
	newCoordinatorReportFixture(t)
	out, errOut, err := execute(t, "coordinator", "report", "--repo", "acme/app",
		"--branch", "release/v0.11.0", "--file", "internal/coordinator/view.go")
	if err != nil {
		t.Fatal(err)
	}
	for what, text := range map[string]string{"stdout": out, "stderr": errOut} {
		for _, word := range []string{"advice", "advise", "suggest", "collision", "collides", "conflict", "should", "consider", "warning"} {
			if strings.Contains(strings.ToLower(text), word) {
				t.Errorf("%s carries %q, and the report path issues no advice:\n%s", what, word, text)
			}
		}
	}
}

// TestCoordinatorReportRefusesAWriteTheRulesDeny: /coordinator is readable by
// a signed-in reader and writable by nobody through the rules (Q15 A, P3), so
// what admits a report is the database role its caller holds. A database that
// refuses the write is the caller's error — naming the role, and not the
// host's outage — and no document is written.
func TestCoordinatorReportRefusesAWriteTheRulesDeny(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	f.db.Deny(coordpath.PathRoot)
	_, _, err := execute(t, "coordinator", "report", "--repo", "acme/app", "--branch", "release/v0.11.0")
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Fatalf("a refused write = %v (exit %d), want a user error", err, fcli.ExitCode(err))
	}
	for _, want := range []string{"roles/firebasedatabase.admin", "budget_admins"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name what the write needs (%q)", err, want)
		}
	}
}

// TestCoordinatorReportNeedsADatabase: a project that never deployed a
// database has nothing to report to, and the refusal names the command that
// deploys one rather than failing on an empty address.
func TestCoordinatorReportNeedsADatabase(t *testing.T) {
	newCloudFixture(t)
	_, _, err := execute(t, "coordinator", "report", "--repo", "acme/app", "--branch", "release/v0.11.0")
	if fcli.ExitCode(err) != fcli.ExitUserError || !strings.Contains(err.Error(), "update --firebase-project") {
		t.Fatalf("a project with no database = %v (exit %d)", err, fcli.ExitCode(err))
	}
}

// TestDeclareWritesTheLaunchsIntent: Declare is the writer the launch path
// reaches through the build's declarer (internal/cli's declareLaunch), so a
// launch declares itself without a second command. What a launch can declare
// before any code exists is who asked for it and the branch it is about to
// work on, and the release that branch names. That the launch calls this, and
// that a build carrying no declarer declares nothing, is internal/cli's half
// (D535).
func TestDeclareWritesTheLaunchsIntent(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	spec := &task.Spec{Version: 1, RunID: "20261024-120000-abcd", Repo: "acme/app",
		RequestedBy: "someone@example.com", Branch: "release/v0.11.0"}
	if err := Declare(context.Background(), openFromConfig(t, f.cloudFixture), appSlug, spec); err != nil {
		t.Fatal(err)
	}
	rec := f.oneIntent(t)
	if rec.DeclaredBy != "someone@example.com" || rec.Branch != "release/v0.11.0" || rec.Release != "0.11.0" {
		t.Errorf("the launch's declaration = %+v", rec)
	}
}

// TestDeclareWithNoBranchDeclaresTheRunsOwn: the branch a launch declares is
// the one it is about to work on, and a spec that names none gets the run's
// own branch, the same fallback the run's records use.
func TestDeclareWithNoBranchDeclaresTheRunsOwn(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	spec := &task.Spec{Version: 1, RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com"}
	if err := Declare(context.Background(), openFromConfig(t, f.cloudFixture), appSlug, spec); err != nil {
		t.Fatal(err)
	}
	if rec := f.oneIntent(t); rec.Branch != "fugaro/20261024-120000-abcd" {
		t.Errorf("a declaration with no branch = %q, want the run's own", rec.Branch)
	}
}

// TestAFailedDeclareIsTheCallersError: the coordinator is advisory (spec §3),
// so a declaration that cannot be written is the writer's answer and never a
// failed launch: the launch path turns it into one warning line on stderr and
// starts the run anyway (internal/cli's declareLaunch, which is where that half
// lives now). What is held here is the half this package owns — a store that
// cannot be opened is an error and not a silent success.
func TestAFailedDeclareIsTheCallersError(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	prev := openCoordinatorStore
	openCoordinatorStore = func(context.Context, *localcfg.Config) (*coordinator.Store, error) {
		return nil, errors.New("the database is unreachable")
	}
	t.Cleanup(func() { openCoordinatorStore = prev })

	err := Declare(context.Background(), openFromConfig(t, f.cloudFixture), appSlug,
		&task.Spec{RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com"})
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("a declaration whose store could not be opened = %v, want the store's error", err)
	}
	if m := f.intents(); len(m) != 0 {
		t.Errorf("a failed declaration wrote %d records", len(m))
	}
}

// TestADeclareAgainstARefusingDatabaseAnswersTheRefusal: a launcher who is not
// a budget admin cannot write the coordinator's record, and that is the
// expected case for most people who launch rather than a fault to tell them
// about on every run (owner ruling 2026-10-24). The writer answers the refusal
// and the launch is what stays quiet about it (declareLaunch checks
// rtdb.ErrPermission); `coordinator report`, which a person asks for, keeps its
// explicit refusal (TestCoordinatorReportRefusesAWriteTheRulesDeny).
func TestADeclareAgainstARefusingDatabaseAnswersTheRefusal(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	f.db.Deny(coordpath.PathRoot)

	err := Declare(context.Background(), openFromConfig(t, f.cloudFixture), appSlug,
		&task.Spec{RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com", Branch: "fugaro/x"})
	if !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a declaration the rules refused = %v, want the refusal", err)
	}
	if m := f.intents(); len(m) != 0 {
		t.Errorf("a refused write declared %d records", len(m))
	}
}

// TestADeclareThatSkipsTheDatabaseDeclaresNothing: the declaration follows the
// launch's own database switch rather than sitting beside it. The coordinator
// keeps its state in the same database the budget uses, and a launch whose
// budget is off — or whose project never deployed a database — declares
// nothing: neither reaches a store at all, which the opener below makes
// visible. Nothing is lost by it — the declaration is advisory, and the view
// reads the runs bucket, where the launch's own record lands either way.
//
// --no-budget-check is the third spelling of that switch, and it is the
// launch's own flag rather than part of the project config: it reaches Declare
// on the cloud the launch opens, which the core's launch path passes through
// (internal/cli's declareLaunch). No test here builds such a cloud -- the flag
// is set on a cloud the core opens, and no exported name of the core sets it
// (design section 10) -- so that branch is reached by a tagged `fugaro run
// --no-budget-check` and by nothing in this package.
func TestADeclareThatSkipsTheDatabaseDeclaresNothing(t *testing.T) {
	for _, tc := range []struct{ what, cfg string }{
		{"a project whose budget is off", "budget: { mode: off }\n"},
		{"a project with no database", ""},
	} {
		f := newCloudFixture(t)
		if tc.cfg != "" {
			f.appendConfig(t, tc.cfg)
		}
		prev := openCoordinatorStore
		opened := false
		openCoordinatorStore = func(context.Context, *localcfg.Config) (*coordinator.Store, error) {
			opened = true
			return nil, errors.New("the store was opened")
		}
		t.Cleanup(func() { openCoordinatorStore = prev })

		err := Declare(context.Background(), openFromConfig(t, f), appSlug,
			&task.Spec{RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com", Branch: "fugaro/x"})
		if err != nil {
			t.Errorf("%s: the declaration = %v, want nothing declared and no error", tc.what, err)
		}
		if opened {
			t.Errorf("%s: the declaration opened the database", tc.what)
		}
	}

	// A launch that names nobody has no one to put on the record, whatever its
	// project's database says.
	f := newCoordinatorReportFixture(t)
	if err := Declare(context.Background(), openFromConfig(t, f.cloudFixture), appSlug,
		&task.Spec{RunID: "20261024-120000-abcd", Branch: "fugaro/x"}); err != nil {
		t.Fatalf("a declaration with no caller = %v", err)
	}
	if m := f.intents(); len(m) != 0 {
		t.Errorf("a declaration with no caller wrote %d records", len(m))
	}
}

// The aggregation job's entry point (docs/coordinator/implementation-plan.md,
// part P5). These tests are the command's half — the environment it reads, the
// database it writes and the exit code it leaves — and not the sweep's own
// logic, which ee/coordinator's tests hold. What they are here for is the
// wiring: a tick that reaches the real database, the real runs bucket and P2's
// own read, driven end to end with fakes at the three seams.

// coordinatorSweepFixture is the fixture the tests below drive: the status
// fixture's runs bucket and fake host, plus the database the coordinator's
// state is written to and the environment Terraform gives the job.
type coordinatorSweepFixture struct {
	*coordinatorFixture
	db *gcpfake.RTDB
}

func newCoordinatorSweepFixture(t *testing.T, seeds []coordinatorRunSeed, prs ...fake.PRState) *coordinatorSweepFixture {
	t.Helper()
	f := &coordinatorSweepFixture{coordinatorFixture: newCoordinatorFixture(t, seeds, prs...), db: gcpfake.NewRTDB(t)}
	old := coordinatorSweepTest
	coordinatorSweepTest = &coordinatorSweepWiring{
		noAuth: true,
		bucket: func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, f.bucket) },
		now:    func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) },
	}
	t.Cleanup(func() { coordinatorSweepTest = old })
	setCoordinatorMint(t, nil)
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	for k, v := range map[string]string{
		"FUGARO_PROJECT": "aurora", "FUGARO_FIREBASE_PROJECT": "aurora-fp",
		"FUGARO_RTDB_URL": f.db.URL, "FUGARO_RUNS_BUCKET": "unused-bucket",
		"FUGARO_COORDINATOR_REPOS": `[{"provider":"github","repo":"acme/app"}]`,
	} {
		t.Setenv(k, v)
	}
	f.db.Set("fugaro/project", "aurora")
	return f
}

// view is the repository's view document as the fake database holds it, and
// stale its marker (nil when there is none).
func (f *coordinatorSweepFixture) view() (map[string]any, map[string]any) {
	v, _ := f.db.Value(coordinator.PathView(appSlug)).(map[string]any)
	s, _ := f.db.Value(coordinator.PathStale(appSlug)).(map[string]any)
	return v, s
}

func TestCoordinatorSweepWritesTheView(t *testing.T) {
	f := newCoordinatorSweepFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	out, _, err := execute(t, "coordinator", "sweep")
	if err != nil {
		t.Fatal(err)
	}
	view, stale := f.view()
	if view == nil {
		t.Fatalf("the sweep wrote no view at /%s; output %q", coordinator.PathView(appSlug), out)
	}
	if stale != nil {
		t.Errorf("a sweep that read everything wrote a stale marker: %v", stale)
	}
	// The document is the view status computes, byte for byte: the two read
	// the same bucket and the same host through the same reader, and a
	// difference between them would be a difference between what the
	// coordinator shows and what it holds.
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var got coordinator.View
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Repo != "acme/app" || got.InFlight() == 0 || len(got.Collisions) == 0 {
		t.Errorf("the view is %s with %d records (%d in flight) and %d collisions, want the fixture's own",
			got.Repo, len(got.Records), got.InFlight(), len(got.Collisions))
	}
	shown, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var statusView coordinator.View
	if err := json.Unmarshal([]byte(shown), &statusView); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, statusView) {
		t.Errorf("the swept view is not the one status computes:\n%s\n%s", raw, shown)
	}
	if !strings.Contains(out, "acme/app:") || !strings.Contains(out, "swept 1 repositories: 1 reconciled, 0 stale") {
		t.Errorf("the tick's report = %q", out)
	}
}

// TestCoordinatorSweepRecordsAPullRequestTheHostRefused: one pull request the
// host would not answer for is recorded in the view on its own, with the state
// the last sweep read, and the repository is still reconciled — the numbers a
// view reads are written by the runs, so one refused number must not be able
// to keep a repository stale. The tick still exits 0.
func TestCoordinatorSweepRecordsAPullRequestTheHostRefused(t *testing.T) {
	f := newCoordinatorSweepFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	if _, _, err := execute(t, "coordinator", "sweep"); err != nil {
		t.Fatal(err)
	}

	f.host.FailPullRequest = 1
	out, _, err := execute(t, "coordinator", "sweep")
	if err != nil {
		t.Fatalf("a read that failed failed the tick: %v", err)
	}
	view, stale := f.view()
	if stale != nil {
		t.Errorf("one refused pull request left the repository stale: %v", stale)
	}
	unread, _ := view["unread"].([]any)
	if len(unread) != 1 {
		t.Fatalf("the view records %d unread pull requests, want 1: %v", len(unread), view["unread"])
	}
	u, _ := unread[0].(map[string]any)
	if u["reason"] != coordinator.UnreadFailed || u["carried"] != true {
		t.Errorf("the unread entry = %v, want %q with the last sweep's state carried", u, coordinator.UnreadFailed)
	}
	if !strings.Contains(out, "1 unread") || !strings.Contains(out, "1 reconciled, 0 stale") {
		t.Errorf("the tick's report = %q", out)
	}
}

// TestCoordinatorSweepMarksAHostItCannotOpenStale: a host the coordinator's
// token cannot be opened against is the repository's failure as a whole, so
// the previous view stays and the marker says so — a view recomputed from the
// runs alone would read as "nothing is in flight" about a repository that has
// work in it. The marker is a fact about the repository, not a failure of the
// job, so the tick exits 0.
func TestCoordinatorSweepMarksAHostItCannotOpenStale(t *testing.T) {
	f := newCoordinatorSweepFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	if _, _, err := execute(t, "coordinator", "sweep"); err != nil {
		t.Fatal(err)
	}
	before, _ := f.view()
	setCoordinatorHost(t, func([]string, func(string)) (coordinator.ProviderOpener, error) {
		return func(context.Context, string, gitprov.TokenSource) (gitprov.Provider, []string, error) {
			return nil, nil, errors.New("the host is unreachable")
		}, nil
	})
	out, _, err := execute(t, "coordinator", "sweep")
	if err != nil {
		t.Fatalf("a host that could not be opened failed the tick: %v", err)
	}
	after, stale := f.view()
	if stale == nil || stale["reason"] != coordinator.StalePullRequests {
		t.Fatalf("the marker = %v, want %q", stale, coordinator.StalePullRequests)
	}
	if !strings.Contains(out, "stale") {
		t.Errorf("the tick's report = %q, want the repository reported stale", out)
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Errorf("the failed read moved the view:\n%s\n%s", b, a)
	}
}

// TestCoordinatorSweepRefusesAMissingEnvironment: the job's environment is
// Terraform's, and a command that guessed a default for a variable nobody set
// would write the coordinator's state somewhere nobody named.
func TestCoordinatorSweepRefusesAMissingEnvironment(t *testing.T) {
	newCoordinatorSweepFixture(t, nil)
	for _, k := range []string{"FUGARO_PROJECT", "FUGARO_FIREBASE_PROJECT", "FUGARO_RTDB_URL", "FUGARO_RUNS_BUCKET", "FUGARO_COORDINATOR_REPOS"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "")
			_, _, err := execute(t, "coordinator", "sweep")
			if err == nil || fcli.ExitCode(err) != fcli.ExitUserError || !strings.Contains(err.Error(), k) {
				t.Errorf("without %s: err = %v, want exit 1 naming it", k, err)
			}
		})
	}
}

// TestCoordinatorSweepRefusesAForeignDatabase: the account can reach any
// database it is granted, so the tick checks the database is this Fugaro
// project's before writing to it (plan R-9, the history sweep's own check).
func TestCoordinatorSweepRefusesAForeignDatabase(t *testing.T) {
	f := newCoordinatorSweepFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	for _, mark := range []any{"birch", nil, 7} {
		f.db.Set("fugaro/project", mark)
		_, _, err := execute(t, "coordinator", "sweep")
		if err == nil || fcli.ExitCode(err) != fcli.ExitUserError {
			t.Fatalf("a database marked %v: err = %v, want exit 1", mark, err)
		}
		if v, _ := f.view(); v != nil {
			t.Fatalf("a foreign database was written to: %v", v)
		}
	}
}

// TestCoordinatorSweepRefusesAURLAsTheBucket: the variable is a bucket name and
// Terraform writes one, so a value carrying a scheme or a path is refused
// rather than opened.
func TestCoordinatorSweepRefusesAURLAsTheBucket(t *testing.T) {
	newCoordinatorSweepFixture(t, nil)
	t.Setenv("FUGARO_RUNS_BUCKET", "gs://aurora-runs")
	_, _, err := execute(t, "coordinator", "sweep")
	if err == nil || fcli.ExitCode(err) != fcli.ExitUserError || !strings.Contains(err.Error(), "bucket name") {
		t.Errorf("a URL as the bucket: err = %v, want exit 1", err)
	}
}

// TestCoordinatorSweepNeverPrintsACredentialOrTheHostsText: the read is P2's,
// so the credential values the opener hands back are redacted out of whatever
// this command prints, and the failure it records is this package's own
// classification rather than the host's message (Q14 A) — a view carrying
// "401: bad credentials (token ...)" would be another writer's text kept in
// the coordinator's state, which is what the classification exists to prevent.
func TestCoordinatorSweepNeverPrintsACredentialOrTheHostsText(t *testing.T) {
	const secret = "ghs_0123456789abcdefghij"
	f := newCoordinatorSweepFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	host := &fake.Provider{Repo: "acme/app", SelfID: "1"}
	host.State.PRs = coordinatorPRs()
	setCoordinatorHost(t, func([]string, func(string)) (coordinator.ProviderOpener, error) {
		return func(context.Context, string, gitprov.TokenSource) (gitprov.Provider, []string, error) {
			return secretErrHost{Provider: host, secret: secret}, []string{secret}, nil
		}, nil
	})

	out, errOut, err := execute(t, "coordinator", "sweep")
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{out, errOut} {
		if strings.Contains(got, secret) || strings.Contains(got, "bad credentials") {
			t.Errorf("the credential or the host's text reached the output: %q", got)
		}
	}
	view, _ := f.view()
	unread, _ := view["unread"].([]any)
	if len(unread) == 0 {
		t.Fatalf("the view records no unread pull request: %v", view)
	}
	for _, e := range unread {
		if u, _ := e.(map[string]any); u["reason"] != coordinator.UnreadFailed {
			t.Errorf("the unread entry = %v, want %q", u, coordinator.UnreadFailed)
		}
	}
	if raw := fmt.Sprint(f.db.Value(coordpath.PathRepo(appSlug))); strings.Contains(raw, secret) || strings.Contains(raw, "401") {
		t.Errorf("the repository's node carries the host's own text: %s", raw)
	}
}

// TestCoordinatorSweepRevokesEveryTokenItMinted: the scheduled sweep holds a
// repository's read-only token for the tick and no longer, exactly as status
// does. It drives the command's own opener, mint and revoke against a hermetic
// host, and the fixture's last exchange is the revoke: a tick that left its
// token behind leaves that DELETE unused, which fails the test.
func TestCoordinatorSweepRevokesEveryTokenItMinted(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_read_token.json"))
	realOpen, realMint, realRevoke := openCoordinatorProvider, coordinatorMint, coordinatorRevoke
	newCoordinatorSweepFixture(t, []coordinatorRunSeed{{
		id: "20261013-100000-aaaa", pr: 12, prev: "20261012-090000-1111",
		branch: "fugaro/20261013-100000-aaaa", who: "someone@example.com",
		status: runstore.StatusSucceeded, prRef: 12, base: "main",
		files: []string{"internal/cli/exec.go"},
	}})
	setCoordinatorHost(t, realOpen)
	coordinatorMint, coordinatorRevoke = realMint, realRevoke
	setCoordinatorGitHubRoot(t, srv.URL)
	t.Setenv(providers.EnvCredentialsServiceURL, "")

	if _, _, err := execute(t, "coordinator", "sweep"); err != nil {
		t.Fatal(err)
	}
}

// TestCoordinatorSweepWithAnInjectedReader: a sweep handed its reader builds no
// token record of its own, and the revoke at the end of the tick has nothing
// to take back rather than a nil record to fail on.
func TestCoordinatorSweepWithAnInjectedReader(t *testing.T) {
	f := newCoordinatorSweepFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	reader, _, err := coordinatorReader(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	coordinatorSweepTest.reader = reader
	if _, _, err := execute(t, "coordinator", "sweep"); err != nil {
		t.Fatal(err)
	}
	if view, _ := f.view(); view == nil {
		t.Errorf("the sweep with an injected reader wrote no view")
	}
}

// TestCoordinatorSweepIsHidden: the sweep is the job's entry point and not a
// command of the coordinator's surface, so the generated CLI reference leaves
// it out — the same rule budget history follows.
func TestCoordinatorSweepIsHidden(t *testing.T) {
	for _, c := range coordinatorTestRoot().Commands() {
		if c.Name() != "coordinator" {
			continue
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "sweep" && !sub.Hidden {
				t.Errorf("fugaro coordinator sweep is not hidden")
			}
		}
	}
}

// TestCoordinatorShowPrintsNoEscapeFromTheHostsText: the description and the
// title are `coordinator show`'s two fields carrying a stranger's bytes, and
// the text output is the surface that draws them for a person. The fixture's
// title and body each carry a screen-clear (ESC [ 2J) and an OSC-8 hyperlink
// (ESC ] 8 ; ; … BEL) — a sequence a terminal acts on and a reader cannot see —
// so what this holds is the sanitiser's contract at the point it is reached: no
// ESC and no BEL anywhere in the output, and the host's readable words still
// there. The second half is why: a command that printed nothing would satisfy
// the first on its own, and a reader that sanitised the document instead of the
// line would satisfy it by inventing a field (§6.15 — the document is the
// host's own answer).
func TestCoordinatorShowPrintsNoEscapeFromTheHostsText(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show_hostile.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, bad := range []struct {
		name string
		r    rune
	}{{"ESC", 0x1b}, {"BEL", 0x07}} {
		if i := strings.IndexRune(out, bad.r); i >= 0 {
			t.Errorf("the text output carries %s at byte %d, so the host's own bytes reached the terminal:\n%q", bad.name, i, out)
		}
	}
	for _, want := range []string{"Add search", "Adds a search box to the header.", "Closes #1200."} {
		if !strings.Contains(out, want) {
			t.Errorf("the text output does not carry %q, so the sanitiser dropped the host's words and not only its escapes:\n%q", want, out)
		}
	}
	if strings.Contains(out, "ghs_fixture") {
		t.Errorf("the text output carries the installation token:\n%q", out)
	}

	// The document is the other half of the boundary: it is the host's answer
	// as data, and the escapes are still in it. A decoder at the far edge
	// sanitises for its own surface; the CLI's text renderer is the one that
	// does it here, and a rewrite of the field would be a field this read
	// invented.
	srv = httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show_hostile.json"))
	setCoordinatorHost(t, showFixtureEnv(t, srv.URL))
	docOut, _, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(docOut), &doc); err != nil {
		t.Fatalf("the document is not JSON (%v):\n%s", err, docOut)
	}
	for _, field := range []struct{ name, value string }{{"title", doc.Title}, {"body", doc.Body}} {
		if !strings.ContainsRune(field.value, 0x1b) || !strings.ContainsRune(field.value, 0x07) {
			t.Errorf("the document's %s = %q, want the host's own bytes: the read sanitised the data rather than the line", field.name, field.value)
		}
	}
}

// TestCoordinatorShowNeverPrintsTheTokenTheHostEchoed: the failure path of the
// single read, end to end. The fixture's host answers the pull-request read
// with a 500 whose body quotes the Authorization header back — the shape a
// proxy or a debugging host really produces — so the text the provider hands
// the reader carries the installation token. The reader holds that token in its
// own cache (Project.minted) and not in the opener's secrets, which is the half
// a redactor built from the opener alone would miss, so this asserts the
// command's whole output and the failure it reports: the token in neither, the
// marker in the failure, and the host's status as the exit code.
func TestCoordinatorShowNeverPrintsTheTokenTheHostEchoed(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show_echo.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err == nil {
		t.Fatalf("the host answered 500 and the command printed a document: %s", out)
	}
	for _, got := range []string{out, errOut, err.Error()} {
		if strings.Contains(got, "ghs_fixture") {
			t.Errorf("the installation token the host echoed reached the output: %q", got)
		}
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("the failure is %v, want the token redacted in it", err)
	}
	if fcli.ExitCode(err) != fcli.ExitRemoteError {
		t.Errorf("exit %d, want %d: the host refused the read, and that is the host's failure", fcli.ExitCode(err), fcli.ExitRemoteError)
	}
}
