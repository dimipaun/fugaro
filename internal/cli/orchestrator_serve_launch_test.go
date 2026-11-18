package cli

// This file drives P5.1d's half of the resident service: the launch requests a
// node's own record already holds, which this pass reads and starts none of. The
// store, the route and the strictness of the node's decoder are P5.1c's
// (orchestrator_serve_test.go); what is here is one node that holds a request,
// and what the pass answers and logs about it.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runner"
	"golang.org/x/oauth2"
)

// residentLaunchRun is the run id the node below records: one of the shapes
// internal/task mints, and the id a re-issue would start under — the same one,
// never a second one.
const residentLaunchRun = "20261117-050000-abcd"

// residentNodeDoc is a node whose one attempt is past its claim window, in an
// item that is still running: the record a person's patch leaves, and the same
// reading this pass gets for a run that is simply longer than the window, since
// it reads no runs bucket. The intent carries the item's own digest — recovery
// abandons an intent whose spec has moved under it — it is past its claim window
// against the store's clock, and an approval covers it.
//
// The document is written by the state's own writer, which is the opposite of
// what the literal beside it does, and deliberately: that literal is the strict
// decoder's test, and what is under test HERE is the read of the plan.
func residentNodeDoc(t *testing.T, at time.Time) string {
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
	return string(raw)
}

// tickWithNode serves one tick against the node the test names, and fails the
// test if the pass reached the store's write half: this process reads a node and
// writes none.
func tickWithNode(t *testing.T, docs map[string]string, at time.Time) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var log bytes.Buffer
	store := &fakeOrchestratorStore{docs: docs, at: at}
	srv, err := newResidentOrchestrator(orchestratorConfig{id: "aurora", db: store, log: runner.NewLogger(&log)})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, orchestratorTickPath, nil))
	if len(store.writes) != 0 {
		t.Fatalf("the tick wrote %v: this pass reads the node and writes no node", store.writes)
	}
	return rec, log.String()
}

// TestOrchestratorTickReportsTheAttemptsItCannotSeeStarted is P5.1d's half of
// the pass as this deployment serves it, and it is written around the fact the
// review turned up: the fixture's item is in PhaseRunning with a run id — an
// attempt the node records, past its claim window — and this pass reads NO runs
// bucket, so a run that is simply longer than the ten-minute window reads here
// exactly as one that never went. The count is therefore an upper bound, named
// for what was read (`unreported`) and never for a launch the node is asking
// for, and it is the whole of what is said about it: no per-attempt line, and
// nothing at WARNING, because a warning once per tick per long run would fire
// for every normal run this installation has going.
func TestOrchestratorTickReportsTheAttemptsItCannotSeeStarted(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	doc := residentNodeDoc(t, at)
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}

	rec, logged := tickWithNode(t, map[string]string{statePath: doc}, at)
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s), and the node it read is this installation's: %s", rec.Code, rec.Body.String(), logged)
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatalf("the tick's answer is not a pass: %v (%s)", err, rec.Body.String())
	}
	if pass.Unreported != 1 || pass.Left != 0 {
		t.Errorf("the pass reads unreported %d, left %d, and the node holds one attempt past its claim window", pass.Unreported, pass.Left)
	}
	if !strings.Contains(rec.Body.String(), `"unreported"`) {
		t.Errorf("the tick answered %s, and the field's own name is the reading: a caller has to be able to tell it from a launch the node asked for", rec.Body.String())
	}
	if strings.Contains(logged, "WARNING") {
		t.Errorf("the pass logged %q, and a count that is an upper bound for every run longer than the window is not a warning: one per tick per long run is a line nobody can act on", logged)
	}
	if strings.Contains(logged, residentLaunchRun) {
		t.Errorf("the pass logged %q, and the node's items are counted and never quoted: a project's plan does not belong in Cloud Logging", logged)
	}
	if strings.Contains(logged, "started") {
		t.Errorf("the pass logged %q, and it started nothing: every line this revision writes says so", logged)
	}
}

// TestOrchestratorTickStartsNothingUnderTheBudgetSwitch is the guardrail this
// pass has to READ rather than assume. The switch is a node of the same
// database as the state node, so `fugaro budget kill` stops this pass exactly
// as it stops a run — the re-issues included, which is the rung no ceiling
// reaches — and the field's zero value is the PERMISSIVE one, so a pass that
// planned without reading would act under a switch nobody looked at. The second
// case is the decode's own fail-closed rule, which the pass inherits by reading
// through internal/budget: a node that is not a switch at all counts as on,
// because the intent to stop is not lost with the reason it was written.
func TestOrchestratorTickStartsNothingUnderTheBudgetSwitch(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		node string
	}{
		{name: "the owner turned it on", node: `{"on": true, "by": "owner"}`},
		{name: "the node is not a switch", node: `"stop"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := residentNodeDoc(t, at)
			statePath, err := orchestrator.PathState("aurora")
			if err != nil {
				t.Fatal(err)
			}

			rec, logged := tickWithNode(t, map[string]string{statePath: doc, budget.PathKillGlobal: tc.node}, at)
			if rec.Code != http.StatusOK {
				t.Fatalf("the tick answered %d (%s), and a stopped pass is still a pass", rec.Code, logged)
			}
			var pass orchestratorPass
			if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
				t.Fatal(err)
			}
			if pass.Unreported != 0 || pass.Left != 0 {
				t.Errorf("the pass reads unreported %d, left %d under the switch, and a switch that is on is read before any attempt is", pass.Unreported, pass.Left)
			}
			if !strings.Contains(logged, "the budget switch is on") {
				t.Errorf("the pass logged %q, and the line has to say why: \"nothing was committed\" and \"the switch is on\" are the same numbers otherwise", logged)
			}
		})
	}
}

// TestOrchestratorTickReadsNothingForARecentAttempt is the fence around the
// read: an attempt inside its claim window may be a launch that is still in
// flight, so no request is read for it — which is what keeps a pass that cannot
// read the run records from reporting the same attempt twice.
func TestOrchestratorTickReadsNothingForARecentAttempt(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	doc := residentNodeDoc(t, at)
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

	rec, logged := tickWithNode(t, map[string]string{statePath: doc}, at)
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s)", rec.Code, logged)
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatal(err)
	}
	if pass.Unreported != 0 {
		t.Errorf("the pass reads unreported %d, and the attempt is inside its claim window", pass.Unreported)
	}
}

// TestOrchestratorServiceConfigCarriesNoLauncher is the wiring's own shape, and
// it is the review finding this revision answers: the service is built with the
// node and the log and NOTHING else, because the one launcher is
// `fugaro run`'s own path (orchestrator_launcher.go) and this deployment cannot
// give it the runs bucket, the GCP project and the region it needs. A launcher
// built here would be a second launch path — and an empty one made every request
// look refused while nothing could start.
func TestOrchestratorServiceConfigCarriesNoLauncher(t *testing.T) {
	orchestratorTest = &orchestratorWiring{tokens: func(context.Context) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{}), nil
	}}
	t.Cleanup(func() { orchestratorTest = nil })

	cfg, err := orchestratorServiceConfig(context.Background(), envFrom(orchestratorServeEnv), io.Discard)
	if err != nil {
		t.Fatalf("a complete environment was refused: %v", err)
	}
	if _, err := newResidentOrchestrator(cfg); err != nil {
		t.Fatalf("the configuration does not build the service: %v", err)
	}
}
