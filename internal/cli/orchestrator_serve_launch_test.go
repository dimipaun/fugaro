package cli

// This file drives P5.1d's half of the resident service: the launch requests a
// node's own record already holds, carried out through the launcher the
// deployment carries. The store, the route and the strictness of the node's
// decoder are P5.1c's (orchestrator_serve_test.go); what is here is a launcher,
// one node that holds a request, and what the pass answers and logs about it.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runner"
	"golang.org/x/oauth2"
)

// residentLaunchRun is the run id the node below records: one of the shapes
// internal/task mints, and the id a re-issue starts under — the same one, never
// a second one.
const residentLaunchRun = "20261117-050000-abcd"

// residentNodeDoc is a node whose one attempt was committed and never started:
// the record a person's patch leaves, which is the only thing a resident pass
// may start. The intent carries the item's own digest — recovery abandons an
// intent whose spec has moved under it — it is past its claim window against
// the store's clock, and an approval covers it.
//
// The document is written by the state's own writer, which is the opposite of
// what the literal beside it does, and deliberately: that literal is the strict
// decoder's test, and what is under test HERE is the launcher half. The item it
// hands back is the one the assertions compare the launch request against.
func residentNodeDoc(t *testing.T, at time.Time) (string, orchestrator.WorkItem) {
	t.Helper()
	approvalAt := at.Add(-2 * time.Hour)
	w := orchestrator.WorkItem{
		ID:        "p5-1d",
		Title:     "the launcher half",
		Repo:      "example/repo",
		Recipe:    "default",
		RigorTier: "medium",
		Brief:     "Start the run the node already records.",
		Phase:     orchestrator.PhaseRunning,
		RunID:     residentLaunchRun,
		Attempts:  1,
	}
	it := orchestrator.Intent{
		ID:         orchestrator.IntentID(w.ID, 1),
		RunID:      w.RunID,
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: approvalAt.UTC(),
		At:         at.Add(-orchestrator.ClaimTTL - time.Minute).UTC(),
	}
	w.Approved = &orchestrator.Approval{
		By: "owner", At: approvalAt.UTC(), SpecDigest: orchestrator.SpecDigest(w),
		Attempts: orchestrator.ApprovalAttempts,
	}
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           7,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		Goal:          "keep the installation's work moving",
		WorkItems:     []orchestrator.WorkItem{w},
		Decisions:     []orchestrator.Decision{},
		Facts:         map[string]orchestrator.Fact{},
		Archived:      []orchestrator.Archived{},
		Intents:       []orchestrator.Intent{it},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), w
}

// fakeOrchestratorLauncher is the launcher half as a test drives it: the specs
// and the run ids it was asked for, in order, and one answer a test can make a
// refusal.
type fakeOrchestratorLauncher struct {
	asked  []string
	specs  []orchestrator.Spec
	refuse error
}

func (l *fakeOrchestratorLauncher) Launch(_ context.Context, spec orchestrator.Spec, runID string) (string, error) {
	l.asked = append(l.asked, runID)
	l.specs = append(l.specs, spec)
	if l.refuse != nil {
		return "", l.refuse
	}
	return runID, nil
}

// tickWithLauncher serves one tick with the launcher half the test names, and
// fails the test if the pass reached the store's write half: this process
// starts runs and writes no node.
func tickWithLauncher(t *testing.T, store *fakeOrchestratorStore, launch orchestrator.Launcher) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var log bytes.Buffer
	srv, err := newResidentOrchestrator(orchestratorConfig{id: "aurora", db: store, launch: launch, log: runner.NewLogger(&log)})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, orchestratorTickPath, nil))
	if len(store.writes) != 0 {
		t.Fatalf("the tick wrote %v: the launcher half starts a run and this pass writes no node", store.writes)
	}
	return rec, log.String()
}

// TestOrchestratorTickStartsTheNodesOwnLaunchRequest is the launcher half
// end to end: the attempt the node records and never started is started through
// the launcher, under the run id the record holds and with the spec the state
// carries — the run id is the one that was committed, so a crash between the
// intent and the start of a previous pass spends nothing that was not spent —
// and the pass says what it did both in its answer and in its one line.
func TestOrchestratorTickStartsTheNodesOwnLaunchRequest(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	doc, w := residentNodeDoc(t, at)
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeOrchestratorStore{docs: map[string]string{statePath: doc}, at: at}
	launcher := &fakeOrchestratorLauncher{}

	rec, logged := tickWithLauncher(t, store, launcher)
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s), and the node it read is this installation's: %s", rec.Code, rec.Body.String(), logged)
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatalf("the tick's answer is not a pass: %v (%s)", err, rec.Body.String())
	}
	if pass.Requests != 1 || pass.Carried != 1 || pass.Refused != 0 || pass.Left != 0 {
		t.Errorf("the pass reads requests %d, carried %d, refused %d, left %d, and the node holds one attempt committed and never started", pass.Requests, pass.Carried, pass.Refused, pass.Left)
	}
	if !slices.Equal(launcher.asked, []string{residentLaunchRun}) {
		t.Fatalf("the launcher was asked for %v, and the node records the attempt under %q: a re-issue is the same launch and never a second one", launcher.asked, residentLaunchRun)
	}
	if launcher.specs[0] != orchestrator.SpecOf(w) {
		t.Errorf("the launcher was handed %+v, and the state carries %+v: what a launch starts from is the spec the node holds", launcher.specs[0], orchestrator.SpecOf(w))
	}
	if !strings.Contains(logged, "started the launch requests it holds") {
		t.Errorf("the pass logged %q, and a tick that started a run is not a tick that ran no step", logged)
	}
}

// TestOrchestratorTickReportsALaunchRequestItCannotCarryOut is the deployment
// this release can build: the launcher half is wired and carries no seam, so a
// request is read, refused by name and counted — and the tick is still a pass.
// That last part is the one that matters for what this service is for: the read
// is what check 660 measures, and a launch this revision cannot make must not
// take the service down with it. It is also the shape a deployment whose
// launcher reaches nothing meets, which is the fact this change reports rather
// than invents names for (this file's header).
func TestOrchestratorTickReportsALaunchRequestItCannotCarryOut(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		launch orchestrator.Launcher
		want   string
	}{
		{
			name:   "the deployment's launcher carries no seam",
			launch: orchestratorResidentLauncher(),
			want:   "the run's launch claim",
		},
		{
			name:   "the launcher's own start is refused",
			launch: &fakeOrchestratorLauncher{refuse: context.DeadlineExceeded},
			want:   context.DeadlineExceeded.Error(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, _ := residentNodeDoc(t, at)
			statePath, err := orchestrator.PathState("aurora")
			if err != nil {
				t.Fatal(err)
			}
			store := &fakeOrchestratorStore{docs: map[string]string{statePath: doc}, at: at}

			rec, logged := tickWithLauncher(t, store, tc.launch)
			if rec.Code != http.StatusOK {
				t.Fatalf("a launch that could not be carried out answered %d: the read is what this service is for", rec.Code)
			}
			var pass orchestratorPass
			if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
				t.Fatal(err)
			}
			if pass.Requests != 1 || pass.Carried != 0 || pass.Refused != 1 {
				t.Errorf("the pass reads requests %d, carried %d, refused %d, and the node holds one request this launcher could not make", pass.Requests, pass.Carried, pass.Refused)
			}
			if !strings.Contains(logged, "was not carried out") {
				t.Errorf("the pass logged %q, and a request it could not carry out has to be a line an operator finds", logged)
			}
			if !strings.Contains(logged, tc.want) {
				t.Errorf("the pass logged %q, want the refusal to name %q: the line is where an operator learns which step their deployment does not carry", logged, tc.want)
			}
			if strings.Contains(logged, "started the launch requests it holds") {
				t.Errorf("the pass logged %q, and it started nothing", logged)
			}
		})
	}
}

// TestOrchestratorTickStartsNothingForARecentAttempt is the fence around the
// launcher half: an attempt inside its claim window may be a launch that is
// still in flight, so the pass starts nothing and counts nothing — which is
// what keeps a pass that cannot read the run records from spending a run's
// launch twice.
func TestOrchestratorTickStartsNothingForARecentAttempt(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	doc, _ := residentNodeDoc(t, at)
	// The intent is inside the window: the record is five minutes old and the
	// window is ten. The stamp is the one the document was written with — a
	// whole minute, which is what makes the literal below the same string — and
	// a document this edit did not reach would leave the test asserting the
	// window over an attempt that is past it.
	stale, fresh := at.Add(-orchestrator.ClaimTTL-time.Minute).UTC().Format(time.RFC3339), at.Add(-time.Minute).UTC().Format(time.RFC3339)
	if !strings.Contains(doc, stale) {
		t.Fatalf("the node does not record the attempt at %s: %s", stale, doc)
	}
	doc = strings.Replace(doc, stale, fresh, 1)
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeOrchestratorStore{docs: map[string]string{statePath: doc}, at: at}
	launcher := &fakeOrchestratorLauncher{}

	rec, logged := tickWithLauncher(t, store, launcher)
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s)", rec.Code, logged)
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatal(err)
	}
	if pass.Requests != 0 || pass.Carried != 0 {
		t.Errorf("the pass reads requests %d, carried %d, and the attempt is inside its claim window", pass.Requests, pass.Carried)
	}
	if len(launcher.asked) != 0 {
		t.Errorf("the launcher was asked for %v inside the attempt's claim window", launcher.asked)
	}
}

// TestOrchestratorServiceConfigCarriesALauncherHalf is the wiring's own
// requirement: the builder hands the service a launcher, and a nil one here is
// a deployment whose every tick would read a launch request and neither start
// it nor refuse it. The launcher a deployment can build carries no seam today
// (orchestratorResidentLauncher, and this file's header for the three facts
// orchestrator.tf does not set) — a refusal that names the step, which is a
// different thing from no answer at all.
func TestOrchestratorServiceConfigCarriesALauncherHalf(t *testing.T) {
	orchestratorTest = &orchestratorWiring{tokens: func(context.Context) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{}), nil
	}}
	t.Cleanup(func() { orchestratorTest = nil })

	cfg, err := orchestratorServiceConfig(context.Background(), envFrom(orchestratorServeEnv), io.Discard)
	if err != nil {
		t.Fatalf("a complete environment was refused: %v", err)
	}
	if cfg.launch == nil {
		t.Fatal("the configuration carries no launcher half: every tick would read the node's launch requests and answer nothing about them")
	}
}
