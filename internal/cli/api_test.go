package cli

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
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
// fugaro.yaml through the checkout lookup, memoized the way the core memoizes
// it, so the published one has to be that lookup.
func TestAPICheckoutOfAnswersTheCoresCheckout(t *testing.T) {
	ctx := context.Background()
	got := CheckoutOf(ctx, "owner/repo-a")
	want := checkoutOf(ctx, "owner/repo-a")
	if !reflect.DeepEqual(got(), want()) {
		t.Errorf("CheckoutOf answered %+v, checkoutOf %+v, for the same repository", got(), want())
	}
	if !reflect.DeepEqual(got(), got()) {
		t.Error("CheckoutOf's lookup is not memoized: two calls answered differently")
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
