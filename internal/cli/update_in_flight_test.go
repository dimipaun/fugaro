package cli

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_in_flight_test.go is the owner's requirement of 2026-11-09: `fugaro
// update` is safe while runs are in flight, always, and a test says so.
// docs/design/update-in-flight.md is the audit this file pins, step by step —
// what a run that bootstrapped under the PREVIOUS release can observe or lose
// while the update applies.
//
// The fixture is the update door's own (update_converge_test.go's convergeRig),
// an installation one release behind, plus a run of its repository running
// across the update: a real budget session — the registry entry the database
// counts as running, the ledger a lease writes, the counters a settled call
// writes, the outcome it ends with — and a real run record and review session
// in the runs bucket. Nothing here stands in for the run's own writes, because
// the claim is about them.

// The run: its repository is the fixture's, its id is one a launcher writes.
const (
	inFlightRunID  = "20261109-042800-d84f"
	inFlightSigner = "fugaro-token-signer@init-project.iam.gserviceaccount.com"
	inFlightAPIKey = "AIzaSyFakeWebApiKeyForTheUpdateTest000"
	inFlightOwner  = "owner@example.invalid"
)

// TestUpdateLeavesARunInFlightRunning: an installation one release behind,
// updated while a run of its own repository is in flight. The update converges
// — `fugaro update` stays the whole upgrade — the runs are named once and not
// asked about, and every write the run makes afterwards still lands: its lease,
// the ledger and the counters that lease settles, the outcome it ends with, and
// its record and review session in the runs bucket.
func TestUpdateLeavesARunInFlightRunning(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	// The deployed rules are the release behind's document and not a stand-in:
	// the add-only assertion below is a diff against what a run of that release
	// was granted by.
	r.db.SetRules(previousRulesDocument(t))
	before := rulePaths(t, r.db.Rules())
	run := startRunInFlight(t, r)

	out := convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "plugin-pin"})

	// One line, one fact: the runs are in flight and the update leaves them
	// running. Not a question, not a refusal, and never a request to wait — the
	// owner's ruling of 2026-11-09, and the reason the policy stage's refusal
	// is not a thing this door does any more.
	line := inFlightLine(t, out)
	if !strings.Contains(line, "1 run(s) are in flight") {
		t.Errorf("the update's line does not say how many runs are running: %q", line)
	}
	for _, banned := range []string{"wait for them", "--yes", "refus", "finish first, then"} {
		if strings.Contains(line, banned) {
			t.Errorf("the update's line asks the operator to act (%q): %q", banned, line)
		}
	}
	if !strings.Contains(line, "they are unaffected") {
		t.Errorf("the update's line does not say the runs are unaffected: %q", line)
	}

	// The document the update deployed is this binary's, and it only ADDED to
	// the one the previous release left: every rule path that was there is there
	// byte for byte, which is what makes a rule this release adds — a deny
	// included — unable to take anything away from a run of the older one. The
	// other half of that composition is the generator's own suite:
	// TestRunTokenWritesSurviveThePolicyEdit (internal/budget/rules) fails when
	// any location a run token writes loses its grant.
	if !infra.SameJSON(r.db.Rules(), generatedRules(t)) {
		t.Errorf("the update did not leave this binary's document deployed, so the run below is not under the rules this test reads it against:\n%s", r.db.Rules())
	}
	after := rulePaths(t, r.db.Rules())
	for path, want := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("/%s was in the document the previous release deployed and is gone: this update may only add", path)
			continue
		}
		if got != want {
			t.Errorf("/%s changed:\n  was %s\n  now %s", path, want, got)
		}
	}

	// The policy is still readable under the marker the update wrote, which is
	// what a run that re-reads it mid-run does: a cascade call reads the node
	// again through the credentials service (docs/design/update-in-flight.md).
	db, err := newBudgetClient(context.Background(), r.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.Read(context.Background(), db)
	if err != nil {
		t.Fatalf("the published policy could not be read after the update: %v", err)
	}
	if !found || p.Version != projectpolicy.Version {
		t.Errorf("the policy reads as found=%v version=%d after the update, want version %d", found, p.Version, projectpolicy.Version)
	}

	run.laterWrites(t, r)
}

// TestUpdateSaysNothingWhenNothingRuns: the line is about runs in flight, so an
// installation with none gets the update's ordinary transcript and nothing
// about runs. The converse of the test above, and the reason the line is not
// simply always printed.
func TestUpdateSaysNothingWhenNothingRuns(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))

	out := convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "plugin-pin"})

	if strings.Contains(out, "run(s) are in flight") {
		t.Errorf("the update said runs are in flight over an empty registry:\n%s", out)
	}
}

// TestUpdateInFlightLineNamesEveryStepThatCanReachARun: the audit found three
// steps of the update that can still reach a run in flight — the budget mode's
// raise to enforce, which a session re-reads on every lease while the policy
// node is read once; the policy node itself, which a project routed through the
// credentials cascade re-reads on every model call; and a retirement, which
// deletes an orphaned job and takes the executions running on it with it — and
// the door's line names every one this run can take, with why.
//
// The rows are the mutation check of that reading: each one turns exactly one
// step on and asserts the clause is there, and the other clauses are not. The
// first row is the ordinary update — nothing this run does reaches a run in
// flight — where the line says the runs are unaffected because they are, and
// the alternative to every clause here, silence over a step that then reaches a
// run, is the failure the requirement of 2026-11-09 is about.
func TestUpdateInFlightLineNamesEveryStepThatCanReachARun(t *testing.T) {
	// A published node that turns the cascade on: O3's switch, which is what
	// sends a run's model calls through the credentials service — and so through
	// a policy read per call rather than the runner's one memoized read.
	cascade := map[string]any{
		"version":     projectpolicy.Version,
		"allow_repos": []string{"*"},
		"routing":     map[string]any{"claude": map[string]any{"via": projectpolicy.RouteCascade}},
	}
	for _, tc := range []struct {
		name    string
		node    map[string]any
		mode    string
		managed bool
		retire  bool
		want    []string
		notWant []string
	}{
		{
			name:    "nothing this update does reaches them",
			mode:    budget.ModeEnforce,
			want:    []string{"1 run(s) are in flight", "they are unaffected"},
			notWant: []string{"budget mode", "policy this update publishes", "retirement"},
		},
		{
			// The raise is a managed project's alone, and so is the retirement
			// it brings with it (retireTakesExecutions): the two clauses travel
			// together, which is why this row does not call the retirement
			// clause absent.
			name: "the budget mode's raise", mode: budget.ModeObserve, managed: true,
			want:    []string{"budget mode", "caps taking effect", "the retirement"},
			notWant: []string{"policy this update publishes"},
		},
		{
			name: "the policy a cascade project re-reads", node: cascade, mode: budget.ModeEnforce,
			want:    []string{"1 run(s) are in flight", "the policy this update publishes", "routing.claude.via", projectpolicy.RouteCascade},
			notWant: []string{"budget mode", "retirement"},
		},
		{
			name: "the retirement a managed project runs", mode: budget.ModeEnforce, managed: true,
			want:    []string{"1 run(s) are in flight", "the retirement", "orphaned job"},
			notWant: []string{"budget mode", "policy this update publishes"},
		},
		{
			name: "the retirement --retire asks for", mode: budget.ModeEnforce, retire: true,
			want:    []string{"1 run(s) are in flight", "the retirement", "orphaned job"},
			notWant: []string{"budget mode", "policy this update publishes"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			f.db.Set(budget.PathMode, tc.mode)
			if tc.node != nil {
				f.db.Set(projectpolicy.PathShared(), tc.node)
			}
			f.db.Set("agents", map[string]any{"proj-1234": map[string]any{"run-1": map[string]any{"repo": "github/acme/app"}}})
			h := newMigrationRig(t, f, &initOptions{retire: tc.retire})
			if tc.managed {
				h.managedGate()
			}
			h.s.e.announceRunsInFlight(context.Background())

			out := strings.TrimSpace(h.out.String())
			if strings.Count(out, "\n") != 0 {
				t.Errorf("the door's line is one line:\n%s", out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("the line does not say %q:\n%s", want, out)
				}
			}
			for _, bad := range tc.notWant {
				if strings.Contains(out, bad) {
					t.Errorf("the line names %q over a step this run cannot take:\n%s", bad, out)
				}
			}
		})
	}
}

// startRunInFlight opens a run of the fixture's repository whose budget session
// is already started — the registry entry is written, so the database counts it
// as running — and plants its run record in the runs bucket. It is a run that
// bootstrapped under the previous release: everything it holds is written
// before the update runs.
func startRunInFlight(t *testing.T, r *convergeRig) *inFlightRun {
	t.Helper()
	ctx := context.Background()
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(inFlightSigner)
	itk := gcpfake.NewIdentityToolkit(t, iam, inFlightAPIKey, initProject)
	signer, err := token.NewIAMSigner(inFlightSigner,
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	custom, err := token.MintAt(ctx, signer, token.Claims{
		Slug: slug, Run: inFlightRunID, FX: token.ExpiryFX(now, 2*time.Hour), FP: initProjectName, RB: inFlightOwner,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	// The caps an enforcing project compares its leases against, and the
	// largest single lease. A run that bootstrapped before the update found them
	// where its own installation put them, and no step of the update writes
	// either. The limit is a CHILD of config/limits: the node holds the rules
	// generations beside it, and writing the parent would take them with it.
	r.db.Set(budget.PathLimits+"/maxReserveMicros", budget.Micros(5_000_000))
	r.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: microsPtr(150_000_000), PerRunMicros: microsPtr(20_000_000)})
	r.db.Set(budget.PathCapsDefaults, budget.DefaultCaps{RepoDailyMicros: microsPtr(60_000_000)})

	sess, err := budget.Open(ctx, budget.Config{
		RTDBURL: r.db.URL, APIKey: inFlightAPIKey, IdentityURL: itk.URL, SecureTokenURL: itk.URL,
		Slug: slug, Run: inFlightRunID, Now: time.Now, Grace: time.Minute, HeartbeatEvery: time.Hour,
		Register: func(string) {},
		Log:      slog.New(slog.DiscardHandler),
		OnHalt:   func(budget.Halt) {},
	}, custom)
	if err != nil {
		t.Fatalf("opening the run's budget session: %v", err)
	}
	t.Cleanup(func() { sess.Finish(context.Background(), "cancelled") })
	if h, err := sess.Admit(ctx, budget.AdmitOptions{Gateway: true}); err != nil || h != nil {
		t.Fatalf("admitting the run: halt %+v, err %v", h, err)
	}
	if err := sess.Start(ctx, budget.AgentEntry{Repo: "acme/app", Stage: "implement", Round: 1}); err != nil {
		t.Fatalf("starting the run: %v", err)
	}

	// Its record, as the runner writes the first one: running, in the stage it
	// bootstrapped into.
	store := runStoreIn(t, ctx, slug, inFlightRunID)
	if err := store.CreateRecord(ctx, &runstore.Record{
		Version: 1, RunID: inFlightRunID, Repo: "acme/app", Workflow: "app",
		Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone,
	}); err != nil {
		t.Fatalf("writing the run's record: %v", err)
	}
	return &inFlightRun{sess: sess, store: store, slug: slug}
}

// inFlightRun is the run across the update: its budget session and its run
// store, the two things its own later writes go through.
type inFlightRun struct {
	sess  *budget.Session
	store *runstore.Store
	slug  string
}

// laterWrites is everything the run does after the update has applied: the
// lease a stage asks for, the report a settled call makes, the outcome it ends
// with, and the record and the review session the finalize stage writes. Each
// one goes through the client the run really uses, so a step of the update that
// had taken a path or an object away fails here rather than in production.
func (r *inFlightRun) laterWrites(t *testing.T, rig *convergeRig) {
	t.Helper()
	ctx := context.Background()
	if _, err := r.sess.Lease().Grant(ctx, gatewayGrant); err != nil {
		t.Fatalf("the run in flight could not lease after the update: %v", err)
	}
	// What the gateway hands the session as the stage's real use. The session
	// settles the report against it, so a fixture that skipped this would write
	// no spent at all and the assertion below would hold over a ledger nothing
	// had written — the reading has to be the run's own accounting.
	r.sess.SetUsage(func() budget.Micros { return gatewayReport })
	if err := r.sess.Lease().Report(ctx, gateway.StageReport{Calls: 2, Used: gatewayReport}); err != nil {
		t.Fatalf("the run in flight could not report a settled stage after the update: %v", err)
	}
	r.sess.Finish(ctx, "succeeded")

	ledger := budget.PathRun(r.slug, inFlightRunID)
	if got := numAt(t, rig.db, ledger+"/spent"); got != float64(gatewayReport) {
		t.Errorf("the run's ledger reads spent=%v after its lease and its report, want %d (path %s)", got, gatewayReport, ledger+"/spent")
	}
	if got := numAt(t, rig.db, ledger+"/reserved"); got <= 0 {
		t.Errorf("the run's ledger reads reserved=%v, want the lease it is holding (path %s)", got, ledger+"/reserved")
	}
	if got := numAt(t, rig.db, budget.PathSpendRun(budget.Day(time.Now()), r.slug, inFlightRunID)+"/spent"); got != float64(gatewayReport) {
		t.Errorf("the day's counter for the run reads spent=%v, want %d", got, gatewayReport)
	}
	if got := rig.db.Value(budget.PathOutcome(budget.Day(time.Now()), r.slug, inFlightRunID)); got == nil {
		t.Errorf("the run's outcome was not written after it ended")
	}
	if got := rig.db.Value(budget.PathAgent(r.slug, inFlightRunID)); got != nil {
		t.Errorf("the run's registry entry is still there after it ended: %v", got)
	}

	if _, err := r.store.ReadRecord(ctx); err != nil {
		t.Fatalf("the run's record could not be read back after the update: %v", err)
	}
	if err := r.store.WriteRecord(ctx, &runstore.Record{
		Version: 1, RunID: inFlightRunID, Repo: "acme/app", Workflow: "app",
		Status: runstore.StatusSucceeded, Stage: "finalize", Outcome: runstore.OutcomeReady,
	}); err != nil {
		t.Fatalf("the run could not write its record after the update: %v", err)
	}
	if err := r.store.PutReviewSession(ctx, runstore.SessionMeta{
		Version: 1, ID: "3f1a0c92-5e4b-4a7d-9c2e-1b8f0d6a4e21", HeadSHA: "0123456789abcdef", Pushed: true,
		WorkDir: "/workspace", Bytes: 12,
	}, []byte("a review session")); err != nil {
		t.Fatalf("the run could not write its review session after the update: %v", err)
	}
	rec, err := r.store.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Errorf("the record reads status %q after the run's last write", rec.Status)
	}
}

// The two amounts the run's lease and its settled calls are for: a run's own
// figures, in micros of a dollar.
var (
	gatewayGrant  = budget.Micros(1_000_000)
	gatewayReport = budget.Micros(400_000)
)

// rulePaths flattens a rules document into path -> the rule at it, encoded, so
// two documents can be compared path by path: the reading
// TestRulesPreexistingPathsUnchanged makes of the generator's output, made here
// of what the update deployed.
func rulePaths(t *testing.T, doc []byte) map[string]string {
	t.Helper()
	var root struct {
		Rules map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(doc, &root); err != nil {
		t.Fatalf("the rules are not JSON: %v", err)
	}
	out := map[string]string{}
	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		attrs := map[string]any{}
		for k, v := range node {
			if strings.HasPrefix(k, ".") {
				attrs[k] = v
				continue
			}
			child, ok := v.(map[string]any)
			if !ok {
				continue
			}
			next := k
			if prefix != "" {
				next = prefix + "/" + k
			}
			walk(next, child)
		}
		if len(attrs) == 0 {
			return
		}
		enc, err := json.Marshal(attrs)
		if err != nil {
			t.Fatal(err)
		}
		out[prefix] = string(enc)
	}
	walk("", root.Rules)
	return out
}

// inFlightLine is the update's own line about the runs in flight: the one line
// of the transcript that names them, and the whole of what the door says about
// them.
func inFlightLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "run(s) are in flight") {
			return line
		}
	}
	t.Fatalf("the update printed nothing about the runs in flight:\n%s", out)
	return ""
}

// microsPtr is what the caps a fixture seeds are: pointers, an absent side being
// an absent node.
func microsPtr(m budget.Micros) *budget.Micros { return &m }

// numAt is the number at a path of the fixture's database — a leaf the run in
// flight wrote itself — and a failure when there is nothing there, which is the
// way a write of its own that the update had taken a path from would show.
func numAt(t *testing.T, db *gcpfake.RTDB, path string) float64 {
	t.Helper()
	switch v := db.Value(path).(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			t.Fatalf("%s holds %q, which is not a number", path, v)
		}
		return f
	case nil:
		t.Fatalf("nothing at %s: the run's own write did not land", path)
	default:
		t.Fatalf("%s holds %T, and this test reads a number", path, v)
	}
	return 0
}

// runStoreIn opens the runs bucket the CLI opens — the fixture's own opener, so
// this is the bucket the update under test reads and writes — and returns a
// store for one run of it.
func runStoreIn(t *testing.T, ctx context.Context, slug, runID string) *runstore.Store {
	t.Helper()
	b, err := openRecordBucket(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	return runstore.Open(b.Bucket, slug, runID)
}
