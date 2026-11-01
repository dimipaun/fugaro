// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// `fugaro coordinator status --from-store` (D364): the read of the view the
// sweep already stored, which holds no App ID, no key and no Bitbucket
// credential, and reaches no host. It is the console's read and a person's
// read on a machine that has neither, so what these tests hold is that the
// command is the database's and nothing else's:
//
//   - TestCoordinatorStatusFromStoreNeedsNoKey: no App credential is in the
//     environment and no provider is ever opened, and the view printed is the
//     stored one — pull requests and all.
//   - TestCoordinatorStatusFromStoreWithService: the same read with the
//     installation's credentials service in the environment, where the
//     project-wide read refuses (coordinatorAppCredential). The fixture's runs
//     name pull requests, which is what makes a read that went through
//     coordinatorReader reach that refusal rather than skip it.
//   - TestCoordinatorStatusFromStoreFallsBackToRuns: nothing stored — the
//     coordinator is off, or this project never turned it on — is the runs
//     half with "stored": false, so a console needs one argv either way.
//   - TestCoordinatorStatusFromStoreWithSharedConfigOnly: the read does not
//     consult the local config for whether the coordinator is on. The config a
//     teammate's machine holds is Config.Shared's subset, which drops
//     Terraform, and Terraform is where that switch lives.
//   - TestCoordinatorStatusFromStoreAll: --all reads one view per repository,
//     the way the command's other selection does.
//   - TestCoordinatorStatusFromStorePrintsAStaleMarker: the marker is printed
//     as stored, with no age computed from it.
//   - TestCoordinatorFromStoreWarnsInRepositoryOrder: what a read warns about
//     reaches stderr one repository at a time, in the selection's own order,
//     and a repository the read failed on ends the warnings rather than
//     leaving the ones after it to be read on their own.

// coordinatorStoreFixture is the fixture this read is driven with: the status
// fixture's runs bucket and fake host, plus the database the coordinator's
// state is written to and read from.
type coordinatorStoreFixture struct {
	*coordinatorFixture
	db *gcpfake.RTDB
}

func newCoordinatorStoreFixture(t *testing.T, seeds []coordinatorRunSeed) *coordinatorStoreFixture {
	t.Helper()
	f := &coordinatorStoreFixture{
		coordinatorFixture: newCoordinatorFixture(t, seeds, coordinatorPRs()...),
		db:                 gcpfake.NewRTDB(t),
	}
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+f.db.URL+" }\n")
	return f
}

// store is the coordinator's store over the fixture's database, opened the way
// the command's own read opens one: the same client and the same address, with
// no credentials in a test.
func (f *coordinatorStoreFixture) store(t *testing.T) *coordinator.Store {
	t.Helper()
	db, err := rtdb.New(f.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator.NewStore(db)
}

// writeStoredView leaves v as the repository's stored view, and mark as its
// stale marker when it is not nil: what a sweep that could (or could not)
// reconcile the repository left behind.
func (f *coordinatorStoreFixture) writeStoredView(t *testing.T, v coordinator.View, mark *coordinator.Stale) {
	t.Helper()
	ctx := context.Background()
	store := f.store(t)
	if err := store.Write(ctx, coordinator.PathView(appSlug), v); err != nil {
		t.Fatal(err)
	}
	if mark != nil {
		if err := store.Write(ctx, coordinator.PathStale(appSlug), *mark); err != nil {
			t.Fatal(err)
		}
	}
}

// storedView is what a sweep left for the fixture's repository: one record in
// flight, on a pull request the runs name, carrying the host's own state. It
// is deliberately not what a live read of the fixture computes — one record
// against the runs' four — so a test can tell the stored view from a
// recomputed one.
func storedView() coordinator.View {
	return coordinator.View{
		Repo: "acme/app",
		Records: []coordinator.Record{{
			PR: &coordinator.PullRequest{
				Number: 12, URL: "https://github.com/acme/app/pull/12",
				Title: "the stored title", Branch: "fugaro/20261013-100000-aaaa", State: coordinator.StateOpen,
			},
			Runs: []coordinator.Run{{
				ID: "20261013-100000-aaaa", PR: 12, Status: "succeeded",
				Branch: "fugaro/20261013-100000-aaaa", DeclaredBy: "someone@example.com", Files: []string{},
			}},
			Branch: "fugaro/20261013-100000-aaaa", InFlight: true,
			Files: []string{"internal/cli/exec.go"}, Services: []string{"internal/cli"},
		}},
		Collisions: []coordinator.Collision{},
	}
}

// noStoreOpens fails the test if the command opens a provider at all: the
// stored-view read holds no App credential, so there is nothing to open a host
// with, and an opener that was reached is the read going somewhere it must not.
func noStoreOpens(t *testing.T) {
	t.Helper()
	setCoordinatorHost(t, func([]string, func(string)) (coordinator.ProviderOpener, error) {
		t.Error("--from-store opened a provider: this read holds no App credential and reads no host")
		return nil, errors.New("no provider")
	})
}

// storedJSON is the command's document for the fixture's repository, read.
func storedJSON(t *testing.T, args ...string) coordinator.RepoView {
	t.Helper()
	out, errOut, err := execute(t, args...)
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var got coordinator.RepoView
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got
}

func TestCoordinatorStatusFromStoreNeedsNoKey(t *testing.T) {
	f := newCoordinatorStoreFixture(t, coordinatorSeeds())
	f.writeStoredView(t, storedView(), nil)
	noStoreOpens(t)
	// A machine with no App credential anywhere in it: this is the read a
	// person and a console child both make, and neither has one.
	t.Setenv(providers.EnvGitHubAppID, "")
	t.Setenv(providers.EnvGitHubAppKey, "")
	t.Setenv(providers.EnvCredentialsServiceURL, "")

	got := storedJSON(t, "coordinator", "status", "--from-store", "--repo", "acme/app", "--since", "0", "--json")
	if got.Repo != "acme/app" {
		t.Errorf("repo = %q, want acme/app", got.Repo)
	}
	if !got.Stored {
		t.Error(`"stored" is false: the database held a view and this read is the one that shows it`)
	}
	if want := storedView(); !reflect.DeepEqual(got.View, want) {
		t.Errorf("the view printed is not the stored one:\n%+v\nwant\n%+v", got.View, want)
	}
	if got.Stale != nil {
		t.Errorf("a repository with no marker printed %+v", got.Stale)
	}
}

func TestCoordinatorStatusFromStoreWithService(t *testing.T) {
	// The opener a machine with the credentials service in its environment
	// has: the fixture replaces it, and this test puts it back, because the
	// refusal it makes is the thing this read must not meet.
	real := openCoordinatorProvider
	f := newCoordinatorStoreFixture(t, coordinatorSeeds())
	openCoordinatorProvider = real
	f.writeStoredView(t, storedView(), nil)
	// The installation's credentials service holds the App's key (C2.2,
	// D145), so the project-wide read refuses here rather than reaching for a
	// key the platform withheld — and the fixture's runs name pull requests,
	// so a read that delegated to coordinatorReader would reach that refusal
	// rather than return before it.
	t.Setenv(providers.EnvCredentialsServiceURL, "https://credentials.example.com/")

	// --since 0 so that the whole fixture is in the window: a read that
	// delegated to coordinatorReader reaches the refusal only for a run that
	// names a pull request, and a run outside the window is not one.
	got := storedJSON(t, "coordinator", "status", "--from-store", "--repo", "acme/app", "--since", "0", "--json")
	if !got.Stored {
		t.Error(`"stored" is false: the database held a view and this read is the one that shows it`)
	}
	if want := storedView(); !reflect.DeepEqual(got.View, want) {
		t.Errorf("the view printed is not the stored one:\n%+v\nwant\n%+v", got.View, want)
	}
	// The other half of the same rule: without --from-store the command is
	// still the one that refuses, so this is the flag that decides and not
	// the environment.
	if _, _, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0", "--json"); fcli.ExitCode(err) != fcli.ExitUserError ||
		err == nil || !strings.Contains(err.Error(), providers.EnvCredentialsServiceURL) {
		t.Errorf("status without --from-store: exit %d, err %v; want the credentials-service refusal", fcli.ExitCode(err), err)
	}
}

func TestCoordinatorStatusFromStoreFallsBackToRuns(t *testing.T) {
	f := newCoordinatorStoreFixture(t, coordinatorSeeds())
	noStoreOpens(t)
	if f.db.Value(coordinator.PathView(appSlug)) != nil {
		t.Fatal("the fixture stored a view: this test is about a repository that has none")
	}

	got := storedJSON(t, "coordinator", "status", "--from-store", "--repo", "acme/app", "--since", "0", "--json")
	if got.Stored {
		t.Error(`"stored" is true: the database holds no view for this repository`)
	}
	if got.View.Repo != "acme/app" || got.View.InFlight() == 0 {
		t.Errorf("the runs half is %s with %d records (%d in flight), want the fixture's own",
			got.View.Repo, len(got.View.Records), got.View.InFlight())
	}
	// The runs half is the join of the bucket alone: every run the bucket
	// holds is in it, and the pull requests they name are there with the
	// host's state unknown rather than fetched.
	var labels []string
	for _, r := range got.View.Records {
		labels = append(labels, r.Label())
	}
	want := []string{"20261013-120000-cccc", "#12", "#14", "#16"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("records = %v, want %v", labels, want)
	}
	if got.Stale != nil {
		t.Errorf("a repository with no marker printed %+v", got.Stale)
	}
}

func TestCoordinatorStatusFromStoreWithSharedConfigOnly(t *testing.T) {
	f := newCoordinatorStoreFixture(t, coordinatorSeeds())
	f.writeStoredView(t, storedView(), nil)
	noStoreOpens(t)

	path := os.Getenv("FUGARO_CONFIG")
	base, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The owner's own machine: the config carries terraform:, which is where
	// the coordinator switch lives.
	if err := os.WriteFile(path, append(base, []byte("terraform: { coordinator: true }\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	withSwitch := storedJSON(t, "coordinator", "status", "--from-store", "--repo", "acme/app", "--since", "0", "--json")
	// A teammate's machine: what it holds is the published shared config,
	// which is Config.Shared's subset — and Shared drops Terraform, so
	// nothing in it says whether the coordinator is on.
	if err := os.WriteFile(path, base, 0o600); err != nil {
		t.Fatal(err)
	}
	withoutSwitch := storedJSON(t, "coordinator", "status", "--from-store", "--repo", "acme/app", "--since", "0", "--json")

	if !withSwitch.Stored || !withoutSwitch.Stored {
		t.Errorf("stored = %v / %v, want the stored view either way: the read does not consult the local config for whether the coordinator is on",
			withSwitch.Stored, withoutSwitch.Stored)
	}
	if !reflect.DeepEqual(withSwitch, withoutSwitch) {
		t.Errorf("the read is not the same without the switch:\n%+v\n%+v", withSwitch, withoutSwitch)
	}
}

func TestCoordinatorStatusFromStoreAll(t *testing.T) {
	f := newCoordinatorStoreFixture(t, coordinatorSeeds())
	f.writeStoredView(t, storedView(), nil)
	noStoreOpens(t)

	out, errOut, err := execute(t, "coordinator", "status", "--from-store", "--all", "--since", "0", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var got coordinator.StoredProjectView
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got.Repos) != 1 || got.Repos[0].Repo != "acme/app" || !got.Repos[0].Stored {
		t.Fatalf("--all read %+v, want the fixture's one stored view", got.Repos)
	}
	if want := storedView(); !reflect.DeepEqual(got.Repos[0].View, want) {
		t.Errorf("the view printed is not the stored one:\n%+v\nwant\n%+v", got.Repos[0].View, want)
	}
}

func TestCoordinatorStatusFromStorePrintsAStaleMarker(t *testing.T) {
	f := newCoordinatorStoreFixture(t, coordinatorSeeds())
	since, err := time.Parse(time.RFC3339, "2026-10-24T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	mark := coordinator.Stale{Since: since, Reason: coordinator.StaleUnsupported}
	f.writeStoredView(t, storedView(), &mark)
	noStoreOpens(t)

	got := storedJSON(t, "coordinator", "status", "--from-store", "--repo", "acme/app", "--since", "0", "--json")
	if got.Stale == nil || !got.Stale.Since.Equal(mark.Since) || got.Stale.Reason != mark.Reason {
		t.Fatalf("the marker printed is %+v, want %+v exactly as stored", got.Stale, mark)
	}
	out, errOut, err := execute(t, "coordinator", "status", "--from-store", "--repo", "acme/app", "--since", "0")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	// Printed as stored: the marker's own words and no age computed from
	// them, which this read has no clock to place them against.
	if !strings.Contains(out, mark.Since.Format("2006-01-02T15:04:05Z07:00")) || !strings.Contains(out, coordinator.StaleUnsupported) {
		t.Errorf("the text output lacks the marker:\n%s", out)
	}
	if strings.Contains(out, "ago") {
		t.Errorf("the text output claims an age:\n%s", out)
	}
}

// fixtureRepos rewrites the fixture's repositories: --all reads what the local
// config lists, and the coordinator's own selection sorts them.
func fixtureRepos(t *testing.T, f *cloudFixture, repos ...string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var block strings.Builder
	block.WriteString("repos:\n")
	for _, r := range repos {
		block.WriteString("  " + r + ": { provider: github, base_branch: main, workflows: [web] }\n")
	}
	out := regexp.MustCompile(`(?m)^repos:\n(?:  .*\n)*`).ReplaceAllString(string(data), block.String())
	if out == string(data) {
		t.Fatalf("the fixture's config has no repositories to replace:\n%s", data)
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCoordinatorReposInParallelKeepOrder: --all reads every repository's view
// at once, and prints them in the order the selection names — the config's
// repositories, sorted — whatever order the reads finish in. The config lists
// them in another order, so a document in the config's order fails here.
func TestCoordinatorReposInParallelKeepOrder(t *testing.T) {
	repos := []string{"acme/third", "acme/app", "acme/other"}
	var seeds []coordinatorRunSeed
	run := map[string]string{}
	for i, repo := range repos {
		id := fmt.Sprintf("20261013-1%d0000-%04x", i, i)
		run[repo] = id
		seeds = append(seeds, coordinatorRunSeed{
			id: id, repo: repo,
			who: "someone@example.com", status: runstore.StatusRunning, base: "main",
		})
	}
	f := newCoordinatorStoreFixture(t, seeds)
	fixtureRepos(t, f.cloudFixture, repos...)
	noStoreOpens(t)

	out, errOut, err := execute(t, "coordinator", "status", "--from-store", "--all", "--since", "0", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var got coordinator.StoredProjectView
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got.Repos) != len(repos) {
		t.Fatalf("--all read %d repositories, want %d", len(got.Repos), len(repos))
	}
	want := slices.Sorted(slices.Values(repos))
	have := make([]string, 0, len(got.Repos))
	for _, v := range got.Repos {
		if v.Stored {
			t.Errorf("%s is stored: the database holds no view for any repository", v.Repo)
		}
		have = append(have, v.Repo)
	}
	if !slices.Equal(have, want) {
		t.Fatalf("repositories = %v, want %v", have, want)
	}
	// Each view is the one repository's own runs and not another's: the reads
	// are placed, so a view that landed in the wrong slot is visible here.
	for _, v := range got.Repos {
		var labels []string
		for _, r := range v.View.Records {
			labels = append(labels, r.Label())
		}
		if !slices.Equal(labels, []string{run[v.Repo]}) {
			t.Errorf("%s's view = %v, want the run seeded for it, %s", v.Repo, labels, run[v.Repo])
		}
	}
}

// peakRequestTransport is the database a width test reads through: it counts
// the requests a pass has in flight at once, and holds each open until want of
// them are open at the same time — one past the width under test — or hold
// passes, so a pass that widens past its bound is measured widening and a pass
// at the bound is measured sitting at it rather than at whatever the scheduler
// happened to overlap.
type peakRequestTransport struct {
	inner http.RoundTripper
	want  int
	hold  time.Duration

	mu      sync.Mutex
	open    int
	peak    int
	calls   int
	reached bool
	gate    chan struct{}
}

// peakOpen is the most requests that were ever in flight at once, and calls is
// how many the pass made in all.
func (p *peakRequestTransport) peakOpen() (calls, peak int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.peak
}

func (p *peakRequestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	p.open++
	p.calls++
	if p.open > p.peak {
		p.peak = p.open
	}
	// One request widened it, so one closes it: the flag is set in the critical
	// section the reading of it is in.
	widened := p.open >= p.want && !p.reached
	p.reached = p.reached || widened
	waiting := !p.reached && p.hold > 0
	p.mu.Unlock()
	switch {
	case widened:
		close(p.gate)
	case waiting:
		select {
		case <-p.gate:
		case <-time.After(p.hold):
		}
	}
	p.mu.Lock()
	p.open--
	p.mu.Unlock()
	return p.inner.RoundTrip(r)
}

// TestCoordinatorStatusFromStoreAllWithinCoordinatorViewRepos: --all reads the
// repositories' stored views a few at a time, and no more than
// coordinatorViewRepos of them at once. That width is the one multiplied by
// what a single repository's read costs — a repository with nothing stored
// falls back to its runs, read at lsWorkers — so it is the number that decides
// what a project with twenty repositories asks of the bucket and the database
// at any moment. A width is also the half a peak of one cannot show: a pass
// with no bound reads every repository at once.
//
// Measured at the database, where the read of a stored view is: the requests
// the pass has in flight are the repositories it is reading.
// Mutations caught: reading the repositories one at a time (a peak of 1), and
// dropping the bound (a peak that reaches coordinatorViewRepos+1, which opens
// the gate and lets the rest through).
func TestCoordinatorStatusFromStoreAllWithinCoordinatorViewRepos(t *testing.T) {
	repos := []string{"acme/a", "acme/b", "acme/c", "acme/d", "acme/e", "acme/f", "acme/g", "acme/h"}
	if len(repos) != coordinatorViewRepos*2 {
		t.Fatalf("this test seeds %d repositories, want twice the width under test (%d)", len(repos), coordinatorViewRepos)
	}
	var seeds []coordinatorRunSeed
	for i, repo := range repos {
		seeds = append(seeds, coordinatorRunSeed{
			id: fmt.Sprintf("20261013-1%04d-%04x", i, i), repo: repo,
			who: "someone@example.com", status: runstore.StatusRunning, base: "main",
		})
	}
	f := newCoordinatorStoreFixture(t, seeds)
	fixtureRepos(t, f.cloudFixture, repos...)
	noStoreOpens(t)
	peek := &peakRequestTransport{inner: http.DefaultTransport, want: coordinatorViewRepos + 1, hold: 200 * time.Millisecond, gate: make(chan struct{})}
	prev := openCoordinatorStore
	openCoordinatorStore = func(ctx context.Context, lc *localcfg.Config) (*coordinator.Store, error) {
		db, err := rtdb.New(lc.Budget.RTDBURL, rtdb.Auth{IDToken: func() string { return "" }}, rtdb.WithHTTPClient(&http.Client{Transport: peek}))
		if err != nil {
			return nil, err
		}
		return coordinator.NewStore(db), nil
	}
	t.Cleanup(func() { openCoordinatorStore = prev })

	out, errOut, err := execute(t, "coordinator", "status", "--from-store", "--all", "--since", "0", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var got coordinator.StoredProjectView
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got.Repos) != len(repos) {
		t.Fatalf("--all read %d repositories, want %d: the width test measured a pass that read something else", len(got.Repos), len(repos))
	}
	calls, peak := peek.peakOpen()
	if calls < len(repos) {
		t.Fatalf("the pass made %d database reads for %d repositories: the width test measured a pass that read something else", calls, len(repos))
	}
	if peak != coordinatorViewRepos {
		t.Errorf("--all read %d repositories' views at once, want exactly coordinatorViewRepos (%d): the pass fans out over the project's repositories, within its bound", peak, coordinatorViewRepos)
	}
}

// TestCoordinatorFromStoreWarnsInRepositoryOrder: every repository a read
// cannot make sense of warns about it, and the warnings reach stderr one
// repository at a time, in the order the selection names the repositories —
// never interleaved in whatever order the reads happened to finish. A
// repository the read failed on ends them: neither its own warnings nor any
// later repository's are about a pass that finished, and the failure is what
// the command reports instead.
//
// What this holds against: the warnings used to be written as each read made
// them, through one lock, so twenty repositories' lines met on stderr in
// completion order, and every repository's warnings were printed even when one
// of them had already failed the command.
func TestCoordinatorFromStoreWarnsInRepositoryOrder(t *testing.T) {
	repos := []string{"acme/app", "acme/keep", "acme/mid", "acme/zzz"}
	ids := map[string][]string{}
	var seeds []coordinatorRunSeed
	for i, repo := range repos {
		for j := range 2 {
			id := fmt.Sprintf("20261013-1%d0000-%04d", i, j)
			ids[repo] = append(ids[repo], id)
			seeds = append(seeds, coordinatorRunSeed{
				id: id, repo: repo, who: "someone@example.com", status: runstore.StatusRunning, base: "main",
			})
		}
	}
	f := newCoordinatorStoreFixture(t, seeds)
	fixtureRepos(t, f.cloudFixture, repos...)
	noStoreOpens(t)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, repo := range repos {
		for _, id := range ids[repo] {
			// A write that stopped half way through: valid JSON's opening and
			// nothing else, so the run is one the read warns about.
			if err := b.WriteAll(ctx, "runs/"+mustSlug(gitprov.KindGitHub, repo)+"/"+id+"/result.json", []byte(`{"version": 1, `), nil); err != nil {
				t.Fatalf("garbling %s's result.json: %v", id, err)
			}
		}
	}
	// One repository's stored view is one this reader is no longer let read,
	// which fails the command rather than falling back to its runs.
	f.db.DenyRead(coordinator.PathView(mustSlug(gitprov.KindGitHub, "acme/mid")))

	_, errOut, err := execute(t, "coordinator", "status", "--from-store", "--all", "--since", "0", "--json")
	if err == nil {
		t.Fatalf("the read succeeded with a repository the database refuses (stderr: %s)", errOut)
	}
	var lines []string
	for _, line := range strings.Split(errOut, "\n") {
		if strings.HasPrefix(line, "warning: ") {
			lines = append(lines, line)
		}
	}
	// The two repositories the selection names before the one that failed,
	// each warning named by the repository it is about and no other, with the
	// two runs of one repository read together.
	var want []string
	for _, repo := range []string{"acme/app", "acme/keep"} {
		want = append(want, repo, repo)
	}
	if len(lines) != len(want) {
		t.Fatalf("stderr =\n%s\nwant %d warnings, the two repositories before the one that failed, and nothing after them", errOut, len(want))
	}
	for at, repo := range want {
		wantPrefix := "warning: run " + mustSlug(gitprov.KindGitHub, repo) + "/"
		if !strings.HasPrefix(lines[at], wantPrefix) {
			t.Errorf("stderr warning %d = %q, want %s's own: one repository's warnings are read together and the selection's order decides which repository comes first", at, lines[at], repo)
		}
	}
}
