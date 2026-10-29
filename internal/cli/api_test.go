package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestCloudBudgetDBRefusesAnotherProjectsDatabase: the opened handle is the
// one thing a moved command gets, so the check the core's own paths make — the
// database at budget.rtdb_url is THIS project's — has to hold behind the
// handle too, or a command across the boundary could write another
// installation's budget.
func TestCloudBudgetDBRefusesAnotherProjectsDatabase(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	env := memEnv(t, f.cloudFixture)
	c := &Cloud{env: env}

	if _, err := c.BudgetDB(context.Background()); err != nil {
		t.Fatalf("the project's own budget database was refused: %v", err)
	}
	f.db.Set("fugaro/project", "their-project")
	_, err := c.BudgetDB(context.Background())
	if err == nil {
		t.Fatal("Cloud.BudgetDB opened another project's budget database")
	}
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "belongs to project") {
		t.Errorf("the refusal = %v (exit %d), want the user error that names the wrong project", err, ExitCode(err))
	}
}

// TestCloudFirestoreNeedsABudgetBlock: the core's own reader checks the
// project's Firebase fields before it reaches for a client, so the handle must
// too. A project config with no budget block is what a project that never ran
// `fugaro update --firebase-project` has, and it is a refusal rather than a
// panic.
func TestCloudFirestoreNeedsABudgetBlock(t *testing.T) {
	cases := []struct {
		name string
		env  func(t *testing.T) *cloudEnv
	}{
		{"no budget block", func(t *testing.T) *cloudEnv {
			env := memEnv(t, newCloudFixture(t))
			env.lc.Budget = nil
			return env
		}},
		{"a budget block with no Firebase project", func(t *testing.T) *cloudEnv {
			f := newLaunchBudget(t, "observe")
			env := memEnv(t, f.cloudFixture)
			env.lc.Budget.FirebaseProject = ""
			return env
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env(t)
			c := &Cloud{env: env}
			fs, err := c.Firestore(context.Background())
			if err == nil {
				t.Fatalf("Cloud.Firestore opened a client for a project with %s", tc.name)
			}
			if fs != nil {
				t.Error("Cloud.Firestore returned a client beside its error")
			}
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no spend history") {
				t.Errorf("the refusal = %v (exit %d), want the user error that says there is no history", err, ExitCode(err))
			}
		})
	}
}

// TestAPIRunObjectsAnswersTheCoresRead: the coordinator's sweep reads the
// runs of a repository through the core's own reader, so the published one
// has to be that reader and not a second one. It may hand back only what the
// sweep reads: the run's own objects and the warning about what could not be
// read, never the store behind them.
func TestAPIRunObjectsAnswersTheCoresRead(t *testing.T) {
	ctx := context.Background()
	env := memEnv(t, newCloudFixture(t))
	c := &Cloud{env: env}

	got, err := c.ReadRun(ctx, "owner/repo-a", "run-1")
	if err != nil {
		t.Fatalf("Cloud.ReadRun: %v", err)
	}
	want, err := readRun(ctx, env, "owner/repo-a", "run-1")
	if err != nil {
		t.Fatalf("readRun: %v", err)
	}
	if !reflect.DeepEqual(got.Input(), want.in) {
		t.Errorf("Cloud.ReadRun's input = %+v, want readRun's %+v", got.Input(), want.in)
	}
	if got.Warning() != want.warning {
		t.Errorf("Cloud.ReadRun's warning = %q, want readRun's %q", got.Warning(), want.warning)
	}
}

// TestAPICloudAnswersTheCoreHandles: RunsBucket, RepoProvider and Close are
// the handle's own fields and methods, reached through the opaque *Cloud
// because a moved command holds nothing else. Each has to answer exactly what
// the core answers for the same project.
func TestAPICloudAnswersTheCoreHandles(t *testing.T) {
	env := memEnv(t, newCloudFixture(t))
	c := &Cloud{env: env}
	if got := c.RunsBucket(); got != env.bucket {
		t.Errorf("Cloud.RunsBucket() = %v, want the cloud's own bucket %v", got, env.bucket)
	}
	co := &config.Config{}
	if got, want := c.RepoProvider("owner/repo-a", co), env.repoProvider("owner/repo-a", co); got != want {
		t.Errorf("Cloud.RepoProvider = %q, want %q", got, want)
	}
	c.Close()
	if _, err := c.ReadRun(context.Background(), "owner/repo-a", "run-1"); err == nil {
		t.Error("Cloud.Close left the runs bucket open")
	}
}

// TestAPIParseSinceAnswersTheCoresParse: --since is read on both sides of the
// boundary, and a window the core accepts but the moved command refuses (or
// the other way round) is two answers to one flag.
func TestAPIParseSinceAnswersTheCoresParse(t *testing.T) {
	for _, in := range []string{"0", "7d", "36h", "90m", "-1d", "0d", "nonsense", ""} {
		got, gotErr := ParseSince(in)
		want, wantErr := parseSince(in)
		if got != want || (gotErr == nil) != (wantErr == nil) {
			t.Errorf("ParseSince(%q) = %v, %v; parseSince = %v, %v", in, got, gotErr, want, wantErr)
		}
		if gotErr != nil && wantErr != nil && gotErr.Error() != wantErr.Error() {
			t.Errorf("ParseSince(%q) refused with %q, parseSince with %q", in, gotErr, wantErr)
		}
	}
}

// TestAPILsClockAnswersTheCoresClock: the coordinator's default window is
// ls's, and a fixed clock is what its tests pin it with, so the published
// reader has to read the same variable rather than a copy of its value.
func TestAPILsClockAnswersTheCoresClock(t *testing.T) {
	if got, want := LsDefaultSince(), lsDefaultSince; got != want {
		t.Errorf("LsDefaultSince() = %q, want %q", got, want)
	}
	fixed := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	old := lsNow
	lsNow = func() time.Time { return fixed }
	defer func() { lsNow = old }()
	if got := LsNow(); !got.Equal(fixed) {
		t.Errorf("LsNow() = %v, want the clock the core's tests installed, %v", got, fixed)
	}
}

// TestAPIParallelAnswersTheCoresParallel: the sweep reads one run per
// goroutine, so the published one has to be the core's bound and the core's
// call pattern, not a second implementation with its own width.
func TestAPIParallelAnswersTheCoresParallel(t *testing.T) {
	const n = 12
	want := make([]int, n)
	parallel(n, func(i int) { want[i] = i })
	got := make([]int, n)
	Parallel(n, func(i int) { got[i] = i })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parallel wrote %v, parallel wrote %v", got, want)
	}
}

// TestAPIOpenProviderAnswersTheCoresOpener: the sweep opens the project's
// code host with no --provider override, exactly as the core does, so the
// kind and the opener it answers with are the core's.
func TestAPIOpenProviderAnswersTheCoresOpener(t *testing.T) {
	warn := func(string) {}
	kind, opener, err := OpenProvider(nil, warn)
	wantKind, wantOpener, wantErr := providerOptions(execOptions{}, nil, warn)
	if kind != wantKind || (err == nil) != (wantErr == nil) || (opener == nil) != (wantOpener == nil) {
		t.Errorf("OpenProvider = %q, %v, %v; providerOptions = %q, %v, %v", kind, opener != nil, err, wantKind, wantOpener != nil, wantErr)
	}
}

// TestAPINewBudgetClientRefusesWhatTheCoreRefuses: the client the moved
// command opens is the core's own, with the caller's credentials, so the URL
// it validates and the refusal it words are the core's too.
func TestAPINewBudgetClientRefusesWhatTheCoreRefuses(t *testing.T) {
	ctx := context.Background()
	const bad = "not-a-database-url"
	if _, err := NewBudgetClient(ctx, bad, true); err == nil {
		t.Fatal("NewBudgetClient accepted a URL the core refuses")
	} else if _, wantErr := newBudgetClient(ctx, bad, true); wantErr == nil || err.Error() != wantErr.Error() {
		t.Errorf("NewBudgetClient refused with %q, newBudgetClient with %v", err, wantErr)
	}
	db, err := NewBudgetClient(ctx, "http://127.0.0.1:9000", true)
	if err != nil {
		t.Fatalf("NewBudgetClient refused a loopback database: %v", err)
	}
	if db == nil {
		t.Error("NewBudgetClient returned no client for a URL it accepted")
	}
}

// TestAPIBudgetScopesAreTheCoresScopesAndACopy: the scopes are what the
// database token is minted with, so a caller must not be able to widen them
// for the core by writing through the answer.
func TestAPIBudgetScopesAreTheCoresScopesAndACopy(t *testing.T) {
	got := BudgetScopes()
	if !reflect.DeepEqual(got, budgetScopes) {
		t.Errorf("BudgetScopes() = %v, want %v", got, budgetScopes)
	}
	if len(got) == 0 {
		t.Fatal("BudgetScopes() is empty")
	}
	got[0] = "https://www.googleapis.com/auth/cloud-platform"
	if budgetScopes[0] == got[0] {
		t.Error("BudgetScopes() handed out the core's own slice: a caller changed what the core mints with")
	}
}

// TestAPICheckDatabaseHostRefusesWhatTheCoreRefuses: the sweep writes only to
// this Fugaro project's own database, and that check is the core's.
func TestAPICheckDatabaseHostRefusesWhatTheCoreRefuses(t *testing.T) {
	loopback := "http://127.0.0.1:9000"
	if err := CheckDatabaseHost(loopback, "their-project"); err != nil {
		t.Errorf("CheckDatabaseHost refused a loopback database: %v", err)
	}
	other := "https://another-project-default-rtdb.firebaseio.com"
	err := CheckDatabaseHost(other, "this-project")
	if err == nil {
		t.Fatal("CheckDatabaseHost accepted another project's database")
	}
	if wantErr := checkDatabaseHost(other, "this-project"); wantErr == nil || err.Error() != wantErr.Error() {
		t.Errorf("CheckDatabaseHost refused with %q, checkDatabaseHost with %v", err, wantErr)
	}
}

// TestAPICheckoutOfAnswersTheCoresCheckout: the sweep reads each repository's
// fugaro.yaml through the checkout lookup, so the published one has to be that
// lookup. The comparison is made inside a checkout of the repository: outside
// one both lookups answer nil whatever they do, so the first assertion is what
// keeps this test from passing on two empty answers. That the looked-up config
// is memoized is the core's own business and is pinned there, where two calls
// can be counted (checkoutOf); counting them here would take a hook in the
// production path, which is a higher price than the claim is worth.
func TestAPICheckoutOfAnswersTheCoresCheckout(t *testing.T) {
	ctx := context.Background()
	dir := checkoutsAt(t, filepath.Join(t.TempDir(), "app"), checkoutsYAML, "https://github.com/acme/app.git")
	t.Chdir(dir)

	got := CheckoutOf(ctx, "acme/app")
	want := checkoutOf(ctx, "acme/app")
	if want() == nil {
		t.Fatal("the core's own lookup found no fugaro.yaml in a checkout of acme/app: this test would compare two empty answers")
	}
	if !reflect.DeepEqual(got(), want()) {
		t.Errorf("CheckoutOf answered %+v, checkoutOf %+v, in the same checkout", got(), want())
	}
}

// TestAPIEnvNamesAreTheCoresNames: the sweep job reads its settings out of
// the environment Terraform sets on it, under the names the core's own paths
// read, so a moved command must not spell them a second time.
func TestAPIEnvNamesAreTheCoresNames(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{EnvProject, envProject},
		{EnvFirebaseFP, envFirebaseFP},
		{EnvRTDBURL, envRTDBURL},
		{EnvRunsBucket, envRunsBucket},
		{ReportIntentTimeout.String(), reportIntentTimeout.String()},
	} {
		if tc.got != tc.want {
			t.Errorf("published %q, want the core's %q", tc.got, tc.want)
		}
	}
}

// TestAPICloudOverBucketReadsTheBucket: CloudOverBucket is the cloud a moved
// command builds with no config at all — the coordinator's sweep job, which
// Terraform gives environment variables and which opens the runs bucket itself.
// What it must be is a cloud over exactly that bucket: RunsBucket answers it,
// and a run the bucket holds reads back through it. Config answers nil by
// design, which CloudOverBucket's own doc says, so nothing here asks it for a
// project.
func TestAPICloudOverBucketReadsTheBucket(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raw, err := blob.OpenBucket(ctx, "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	b := blobx.Wrap(raw)

	const id = "20261024-120000-abcd"
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	if err := runstore.Open(b.Bucket, appSlug, id).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}

	c := CloudOverBucket(b)
	if c.RunsBucket() != b {
		t.Error("CloudOverBucket answered a cloud over another bucket")
	}
	if c.Config() != nil {
		t.Error("CloudOverBucket answered a project config it was never given")
	}
	o, err := c.ReadRun(ctx, appSlug, id)
	if err != nil {
		t.Fatalf("reading a run out of the cloud's own bucket: %v", err)
	}
	if got := o.Input().Task; got == nil || got.RunID != id {
		t.Errorf("the run read back = %+v, want the task the bucket holds", got)
	}
}

// TestAPILsSlugsAnswersTheCoresSlugs: the moved report command lists the
// repositories a window's runs live in through the core's own slug resolution,
// so the published one has to be that resolution and not a second one.
func TestAPILsSlugsAnswersTheCoresSlugs(t *testing.T) {
	ctx := context.Background()
	env := memEnv(t, newCloudFixture(t))
	c := &Cloud{env: env}
	seedAPIRun(t, env, appSlug, "20261024-120000-abcd")
	// A repository the local config does not list, so the bucket's slugs and
	// the config's are not the same answer.
	seedAPIRun(t, env, mustSlug("github", "owner/repo-b"), "20261024-130000-abcd")

	// all=true is the branch the by-mode path uses (it covers every
	// repository the bucket holds, not only the local config's), and it
	// answers from the bucket alone: a wrapper that dropped the flag would
	// still answer the config's slugs here, so this case is what pins it.
	for _, tc := range []struct {
		name      string
		repo      string
		all       bool
		wantSlugs *lsOptions
	}{
		{"a named repository", "acme/app", false, &lsOptions{repo: "acme/app"}},
		{"the project's own", "", false, &lsOptions{}},
		{"every slug in the bucket", "", true, &lsOptions{all: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.LsSlugs(ctx, tc.repo, tc.all, io.Discard)
			if err != nil {
				t.Fatalf("Cloud.LsSlugs: %v", err)
			}
			want, err := lsSlugs(ctx, env, tc.wantSlugs, io.Discard)
			if err != nil {
				t.Fatalf("lsSlugs: %v", err)
			}
			if len(want) == 0 {
				t.Fatal("the core's own slug resolution answered nothing: this test would compare two empty answers")
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Cloud.LsSlugs = %v, want lsSlugs's %v", got, want)
			}
		})
	}
}

// TestAPILoadRowsAnswersTheCoresLoader: the moved report command's degraded
// path lists the run records through ls's own loader, so the published one has
// to be that loader on the same window.
func TestAPILoadRowsAnswersTheCoresLoader(t *testing.T) {
	ctx := context.Background()
	env := memEnv(t, newCloudFixture(t))
	c := &Cloud{env: env}
	since := time.UnixMilli(1_700_000_000_000)
	now := time.UnixMilli(1_700_100_000_000)
	seedAPIRun(t, env, appSlug, "20261024-120000-abcd")

	got, err := c.LoadRows(ctx, []string{appSlug}, since, io.Discard, now)
	if err != nil {
		t.Fatalf("Cloud.LoadRows: %v", err)
	}
	want, err := loadRows(ctx, env, lsFilter{slugs: []string{appSlug}, since: since, warn: io.Discard}, now)
	if err != nil {
		t.Fatalf("loadRows: %v", err)
	}
	if len(want) == 0 {
		t.Fatal("the core's own loader answered no rows: this test would compare two empty answers")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Cloud.LoadRows = %+v, want loadRows's %+v", got, want)
	}
}

// TestAPIReadRunRecordsAnswersTheCoresRead: the moved report command's by-mode
// rows read the window's runs through the core's own bounded reader, and may
// see only the three facts those rows read.
func TestAPIReadRunRecordsAnswersTheCoresRead(t *testing.T) {
	ctx := context.Background()
	env := memEnv(t, newCloudFixture(t))
	c := &Cloud{env: env}
	seedAPIRun(t, env, appSlug, "20261024-120000-abcd")
	minted := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	// The bounds are distinct and non-zero, and each one is what decides
	// whether the run is in: the by-mode path relies on the until bound
	// dropping a run minted after a window that ends in the past, so a
	// wrapper that swapped since and until, or dropped one, has to fail here.
	for _, tc := range []struct {
		name     string
		since    time.Time
		until    time.Time
		wantRuns int
	}{
		{"the window that holds it", minted.Add(-time.Hour), minted.Add(time.Hour), 1},
		{"a window that ends when it was minted", minted.Add(-time.Hour), minted, 0},
		{"a window that starts after it was minted", minted.Add(time.Hour), time.Time{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, gotUn, gotTrunc, err := c.ReadRunRecords(ctx, []string{appSlug}, tc.since, tc.until, 10)
			if err != nil {
				t.Fatalf("Cloud.ReadRunRecords: %v", err)
			}
			want, wantUn, wantTrunc, err := readRunObjects(ctx, env, []string{appSlug}, tc.since, tc.until, 10)
			if err != nil {
				t.Fatalf("readRunObjects: %v", err)
			}
			if len(got) != tc.wantRuns || len(want) != tc.wantRuns {
				t.Fatalf("the read answered %d runs (core %d), want %d: the window's bounds decide what is in it",
					len(got), len(want), tc.wantRuns)
			}
			if gotUn != wantUn || gotTrunc != wantTrunc {
				t.Fatalf("Cloud.ReadRunRecords = unlaunched %d, truncated %v; readRunObjects = %d, %v",
					gotUn, gotTrunc, wantUn, wantTrunc)
			}
			for i := range want {
				if got[i].ID() != want[i].id {
					t.Errorf("run %d's ID = %q, want %q", i, got[i].ID(), want[i].id)
				}
				if got[i].RecordCorrupt() != want[i].recordCorrupt {
					t.Errorf("run %d's RecordCorrupt = %v, want %v", i, got[i].RecordCorrupt(), want[i].recordCorrupt)
				}
				if want[i].record == nil {
					t.Errorf("run %d's core record is nil: this test cannot tell the accessors apart", i)
					continue
				}
				if !reflect.DeepEqual(got[i].Record(), want[i].record) {
					t.Errorf("run %d's Record = %+v, want %+v", i, got[i].Record(), want[i].record)
				}
			}
		})
	}
}

// seedAPIRun writes one launch and one record for slug's run id into the
// fixture's runs bucket: a reader over the bucket then has a run to answer
// with, and a comparison against the core that ran on two empty answers would
// prove nothing.
func seedAPIRun(t *testing.T, env *cloudEnv, slug, id string) {
	t.Helper()
	ctx := context.Background()
	s := runstore.Open(env.bucket.Bucket, slug, id)
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Backend: "cloud-run", Job: "job", LaunchedAt: time.UnixMilli(1_700_000_000_000)}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: slug, Status: runstore.StatusSucceeded}); err != nil {
		t.Fatal(err)
	}
}

// TestAPIBudgetNowIsTheBudgetClock: the moved report command measures its own
// --since/--until window back from the budget commands' clock, so the
// published one has to be that clock and not a second one — a copy of the
// value, or a call to time.Now, would print a different report on the day the
// core's own tests pin.
func TestAPIBudgetNowIsTheBudgetClock(t *testing.T) {
	fixed := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	old := budgetNow
	budgetNow = func() time.Time { return fixed }
	defer func() { budgetNow = old }()
	if got := BudgetNow(); !got.Equal(fixed) {
		t.Errorf("BudgetNow() = %v, want the clock the core's tests installed, %v", got, fixed)
	}
}

// TestAPIRecordOfRunAnswersTheCoreRecord: the moved report command's degraded
// path turns each listed run into a one-run day record through the core's own
// conversion, so the two groupings cannot divide a run differently.
func TestAPIRecordOfRunAnswersTheCoreRecord(t *testing.T) {
	row := runview.Row{
		Repo: "acme/app", Created: time.UnixMilli(1_700_000_000_000),
		RequestedBy: "person-1@example.com",
		Cost:        runstore.NewCost(1.25, 0, runstore.BasisAPIList).WithNotional(0.25),
	}
	if !reflect.DeepEqual(RecordOfRun(row), recordOfRun(row)) {
		t.Errorf("RecordOfRun = %+v, want recordOfRun's %+v", RecordOfRun(row), recordOfRun(row))
	}
}

// TestAPIComputeSourcesAnswersTheCoreSources: the moved report command names
// each compute estimate's price table through the core's own collector, sorted
// and deduplicated the same way.
func TestAPIComputeSourcesAnswersTheCoreSources(t *testing.T) {
	rows := []runview.Row{
		{Cost: runstore.Cost{ComputeSource: "gcp-list"}},
		{Cost: runstore.Cost{ComputeSource: "vm-table"}},
		{Cost: runstore.Cost{ComputeSource: "gcp-list"}},
		{Cost: runstore.Cost{}},
	}
	got, want := ComputeSources(rows), computeSources(rows)
	if len(want) == 0 {
		t.Fatal("the core's own collector answered nothing: this test would compare two empty answers")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ComputeSources = %v, want computeSources's %v", got, want)
	}
}

// TestAPIApplyCostAnswersTheCoreCost: the moved report command writes a run's
// cost into a day record through the core's own split, so a mixed run's
// dollars are divided the same way on both sides of the boundary.
func TestAPIApplyCostAnswersTheCoreCost(t *testing.T) {
	c := runstore.NewCost(1.25, 0.10, runstore.BasisAPIList).WithNotional(0.25)
	var got, want budget.DayRecord
	ApplyCost(&got, c)
	applyCost(&want, c)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ApplyCost wrote %+v, want applyCost's %+v", got, want)
	}
}

// TestAPIPaidCommandNamesIsACopy: the list is a reader, not a seam, so a
// caller that mutates the slice it answers must not change what the next call
// sees. E1.7b's review found the copy untested.
func TestAPIPaidCommandNamesIsACopy(t *testing.T) {
	first := PaidCommandNames()
	if len(first) == 0 {
		t.Fatal("PaidCommandNames answered nothing: this test would pass on an empty list")
	}
	first[0] = "not-a-command"
	if again := PaidCommandNames(); again[0] == "not-a-command" {
		t.Error("PaidCommandNames answered the slice a caller had already changed")
	}
}

// TestAPISelectProjectAnswersTheCoresSelection: the moved command selects its
// project through this wrapper, so it has to be the core's own selection —
// same config, same refusal — and it has to open nothing eagerly, or a
// Firestore-only path pays for a runs bucket it never reads.
func TestAPISelectProjectAnswersTheCoresSelection(t *testing.T) {
	f := newCloudFixture(t)
	path := os.Getenv("FUGARO_CONFIG")
	stderr := func() io.Writer { return io.Discard }
	flags := &CloudFlags{o: cloudOptions{config: path, stderr: stderr}}

	got, err := SelectProject(context.Background(), flags)
	if err != nil {
		t.Fatalf("SelectProject: %v", err)
	}
	_, want, err := selectProject(context.Background(), cloudOptions{config: path, stderr: stderr})
	if err != nil {
		t.Fatalf("selectProject: %v", err)
	}
	if !reflect.DeepEqual(got.Config(), want) {
		t.Errorf("SelectProject's config = %+v, want the core's %+v", got.Config(), want)
	}
	if got.env.bucket != nil || got.env.be != nil {
		t.Error("SelectProject opened the runs bucket or the backend: a path that reads no run record pays for neither")
	}
	// The refusals are the core's too: a config file that is not there is the
	// user error the core's own selection raises, not a second wording.
	_, err = SelectProject(context.Background(), &CloudFlags{o: cloudOptions{config: filepath.Join(f.dir, "absent.yaml"), stderr: stderr}})
	if err == nil || ExitCode(err) != ExitUserError {
		t.Errorf("SelectProject over an absent config = %v (exit %d), want the core's user error", err, ExitCode(err))
	}
}

// TestAPIRefuseHTTP2DebugAnswersTheCoresRefusal: the refusal protects a
// credential from landing on stderr, so the published one has to judge the
// environment exactly as the core does, word for word.
func TestAPIRefuseHTTP2DebugAnswersTheCoresRefusal(t *testing.T) {
	for _, godebug := range []string{"", "http2debug=1", "http2debug=0", "http2client=0", "x=1,http2debug=2", "HTTP2DEBUG=1"} {
		getenv := func(key string) string {
			if key == "GODEBUG" {
				return godebug
			}
			return ""
		}
		got, want := RefuseHTTP2Debug(getenv), refuseHTTP2Debug(getenv)
		if (got == nil) != (want == nil) {
			t.Errorf("RefuseHTTP2Debug(GODEBUG=%q) = %v, refuseHTTP2Debug = %v", godebug, got, want)
			continue
		}
		if got != nil && got.Error() != want.Error() {
			t.Errorf("RefuseHTTP2Debug(GODEBUG=%q) refused with %q, refuseHTTP2Debug with %q", godebug, got, want)
		}
	}
}

// TestAPILiveFactsAnswersTheCoresReader: the days a project has not archived
// are computed from the bucket, and the moved command reads them through this
// handle. Both sides have to answer the same facts for the same project.
func TestAPILiveFactsAnswersTheCoresReader(t *testing.T) {
	f := newCloudFixture(t)
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got := NewLiveFacts(lc, func(string) {}, nil)
	defer got.Close()
	want := &lazyFacts{lc: lc, warn: func(string) {}, slugs: nil}
	defer want.close()

	from, to := budget.Day(time.Now().UTC()), budget.Day(time.Now().UTC())
	gotFacts, gotMissing, gotErr := got.Facts(ctx, from, to)
	wantFacts, wantMissing, wantErr := want.Facts(ctx, from, to)
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("LiveFacts.Facts = %v, lazyFacts.Facts = %v", gotErr, wantErr)
	}
	if !reflect.DeepEqual(gotFacts, wantFacts) || !reflect.DeepEqual(gotMissing, wantMissing) {
		t.Errorf("LiveFacts.Facts = %v, %v; lazyFacts.Facts = %v, %v", gotFacts, gotMissing, wantFacts, wantMissing)
	}
}
