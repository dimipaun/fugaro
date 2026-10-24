package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/coordinator"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
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
// tokens the reads were authenticated with.
func setCoordinatorMint(t *testing.T, tokens map[string]string) {
	t.Helper()
	prev := coordinatorMint
	coordinatorMint = func(_ context.Context, repo string) (string, time.Time, error) {
		token := "ghs_fixture_" + strings.ReplaceAll(repo, "/", "_")
		if tokens != nil {
			tokens[repo] = token
		}
		return token, time.Now().Add(time.Hour), nil
	}
	t.Cleanup(func() { coordinatorMint = prev })
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

// The coordinator's credential is a named read-only set, minted per repository
// from the App the environment holds (Q3 answered (A), D193(A)): the fixture is
// a real mint's own exchanges, so what is asserted here is the request the
// command sends — one repository per token, and no permission above read.
func TestCoordinatorStatusMintsAReadOnlyTokenPerRepository(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_token.json"))
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	t.Setenv(providers.EnvGitHubAPIURL, srv.URL)

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
	if _, _, err := coordinatorMint(context.Background(), "acme/app"); ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), providers.EnvGitHubAppID) {
		t.Errorf("no App ID: exit %d, %v", ExitCode(err), err)
	}
}

// TestCoordinatorStatusReadsWithTheReadOnlyToken drives the command's own
// wiring — no opener replaced and no mint replaced — against a hermetic host,
// and holds the read to the credential P2 mints for it.
//
// The fixture is the whole assertion: the mint's request body is the four-read
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
	t.Setenv(providers.EnvGitHubAPIURL, srv.URL)
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

	// With no App credential at all the provider opener refuses before this
	// read's own mint is reached, so what a person sees is the opener's line —
	// which names the variable — reported as a remote failure. That class is
	// the command's own (coordinatorViewOf; providers.FromEnv reports a missing
	// variable as a plain error and this command reads a failure through the
	// host as remote), and it is pinned here because it is what really happens;
	// the reachable mint refusal (exit 1) is asserted in
	// TestCoordinatorStatusMintsAReadOnlyTokenPerRepository.
	t.Setenv(providers.EnvGitHubAppID, "")
	if _, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0"); err == nil ||
		!strings.Contains(err.Error(), providers.EnvGitHubAppID) {
		t.Errorf("no App ID: %v, want a failure naming %s", err, providers.EnvGitHubAppID)
	} else if ExitCode(err) != ExitRemoteError {
		t.Errorf("no App ID: exit %d, want %d: the opener refuses first and the command reports that failure as a remote one",
			ExitCode(err), ExitRemoteError)
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
	if _, _, err := execute(t, "coordinator", "status", "--all", "--since", "0"); ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "cannot be widened to a project") {
		t.Fatalf("a bitbucket repository in the project: exit %d, %v", ExitCode(err), err)
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
	t.Setenv(providers.EnvGitHubAPIURL, srv.URL)

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
