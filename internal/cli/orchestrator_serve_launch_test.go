package cli

// This file drives P5.1d's half of the resident service: the launch requests a
// node's own record already holds, which this pass reads and starts. The store,
// the route and the strictness of the node's decoder are P5.1c's
// (orchestrator_serve_test.go); what is here is one node that holds a request,
// and what the pass asks a launcher for, answers and logs about it.
//
// The launcher under these tests is a FAKE, deliberately: what is under test
// here is the pass's own wiring — which actions reach a launcher at all, what
// the pass reports about them, and what one refusal does to the rest — while the
// launcher itself is `fugaro run`'s own path, driven for real over a fixture in
// orchestrator_launcher_test.go (TestResidentLauncherStartsTheRecordedAttempt:
// the claim, the mint, the job execution, and the identity the run's own task
// decides).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
// internal/task mints, and the id a re-issue starts under — the same one, never
// a second one.
const residentLaunchRun = "20261117-050000-abcd"

// fakeResidentLauncher is the launcher the tick is served with in this file. It
// records every start it was asked for, in the order it was asked, and answers
// the run id it was handed — which is what the real launcher answers for a run
// whose launch record exists (the same id, never a second one) — unless the test
// gave it a refusal for that run id.
type fakeResidentLauncher struct {
	starts []residentStart
	refuse map[string]error
}

// residentStart is one launch the fake was asked for: the item the spec names
// and the run id the node recorded.
type residentStart struct {
	ItemID string
	RunID  string
}

func (l *fakeResidentLauncher) Launch(_ context.Context, spec orchestrator.Spec, runID string) (string, error) {
	l.starts = append(l.starts, residentStart{ItemID: spec.ItemID, RunID: runID})
	if err := l.refuse[runID]; err != nil {
		return "", err
	}
	return runID, nil
}

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
	return residentNode(t, at, [2]string{"p5-1d", residentLaunchRun})
}

// residentNode is one node holding one RUNNING item per (id, run id) pair, each
// with one attempt past its claim window: residentNodeDoc's own document, with
// as many items as the test needs — which is how a test gives a pass two
// re-issues and a launcher that refuses one of them.
func residentNode(t *testing.T, at time.Time, pairs ...[2]string) string {
	t.Helper()
	approvalAt := at.Add(-2 * time.Hour)
	items := make([]orchestrator.WorkItem, 0, len(pairs))
	intents := make([]orchestrator.Intent, 0, len(pairs))
	for _, p := range pairs {
		w := orchestrator.WorkItem{
			ID:        p[0],
			Title:     "the launcher half",
			Repo:      "example/repo",
			Recipe:    "default",
			RigorTier: "medium",
			Brief:     "Start the run the node already records.",
			Phase:     orchestrator.PhaseRunning,
			RunID:     p[1],
			Attempts:  1,
		}
		w.Approved = &orchestrator.Approval{
			By: "owner", At: approvalAt.UTC(), SpecDigest: orchestrator.SpecDigest(w),
			Attempts: orchestrator.ApprovalAttempts,
		}
		items = append(items, w)
		intents = append(intents, orchestrator.Intent{
			ID:         orchestrator.IntentID(w.ID, 1),
			RunID:      w.RunID,
			SpecDigest: orchestrator.SpecDigest(w),
			ApprovalAt: approvalAt.UTC(),
			At:         at.Add(-orchestrator.ClaimTTL - time.Minute).UTC(),
		})
	}
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           7,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		Goal:          "keep the installation's work moving",
		WorkItems:     items,
		Decisions:     []orchestrator.Decision{},
		Facts:         map[string]orchestrator.Fact{},
		Archived:      []orchestrator.Archived{},
		Intents:       intents,
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// tickWithNode serves one tick against the node the test names, with the
// launcher the test names, and fails the test if the pass reached the store's
// write half: this process reads a node and writes none.
func tickWithNode(t *testing.T, docs map[string]string, at time.Time, launch orchestrator.Launcher) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var log bytes.Buffer
	store := &fakeOrchestratorStore{docs: docs, at: at}
	srv, err := newResidentOrchestrator(orchestratorConfig{id: "aurora", db: store, launch: launch, log: runner.NewLogger(&log)})
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

// TestOrchestratorTickCarriesTheReissueItReads is P5.1d's half of the pass as
// this deployment serves it, and it is written around the fact the P5.1c review
// turned up: the fixture's item is in PhaseRunning with a run id — an attempt
// the node records, past its claim window — and this pass reads NO runs bucket,
// so a run that is simply longer than the ten-minute window reads here exactly
// as one that never went. The count is therefore an upper bound, named for what
// was read (`unreported`) and never for a launch the node is asking for.
//
// What the pass DOES with such a reading is start it, under the run id the node
// recorded and through the one launcher (the fake here): that is `carried`, and
// it is a second field rather than the same number under another name because
// the two differ exactly where a launcher refused. Nothing is counted per
// attempt and nothing is named: the run id never reaches this process's log (a
// project's plan does not belong in Cloud Logging), and a long run — which
// reads as one of these too — is not a warning, because there is nothing a
// person could do about it.
func TestOrchestratorTickCarriesTheReissueItReads(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	doc := residentNodeDoc(t, at)
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	launch := &fakeResidentLauncher{}

	rec, logged := tickWithNode(t, map[string]string{statePath: doc}, at, launch)
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s), and the node it read is this installation's: %s", rec.Code, rec.Body.String(), logged)
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatalf("the tick's answer is not a pass: %v (%s)", err, rec.Body.String())
	}
	if pass.Unreported != 1 || pass.Carried != 1 || pass.Left != 0 {
		t.Errorf("the pass reads unreported %d, carried %d, left %d, and the node holds one attempt past its claim window with nothing to refuse it", pass.Unreported, pass.Carried, pass.Left)
	}
	if !strings.Contains(rec.Body.String(), `"carried"`) {
		t.Errorf("the tick answered %s, and the field's own name is the reading: a caller has to be able to tell a launch that was carried from a step that was left", rec.Body.String())
	}
	want := []residentStart{{ItemID: "p5-1d", RunID: residentLaunchRun}}
	if len(launch.starts) != 1 || launch.starts[0] != want[0] {
		t.Errorf("the tick asked the launcher to start %v, and the node records one attempt of p5-1d under %s: a re-issue is started under the run id the node recorded and never under a second one", launch.starts, residentLaunchRun)
	}
	if strings.Contains(logged, "WARNING") {
		t.Errorf("the pass logged %q, and a carried attempt is not a warning: the pass's own line carries the count", logged)
	}
	if strings.Contains(logged, residentLaunchRun) {
		t.Errorf("the pass logged %q, and the node's items are counted and never quoted: a project's plan does not belong in Cloud Logging", logged)
	}
}

// TestOrchestratorTickCarriesTheRestWhenALauncherRefusesOne is the fence around
// the carrying: one attempt a launcher refuses — a task the runs bucket does not
// hold, a repository this deployment cannot start — is ONE warning line, and
// every other attempt the node records is still started. A pass that stopped at
// the first refusal would hold the whole node back for as long as that one
// attempt stands: this process has no abandon, no breaker and no pause (all of
// them writes), so the node would never make progress again.
//
// The refusal is logged once, with the launcher's own words and on one line, and
// the pass still answers 200 with the count of what it carried: what a person
// needs is both the number and the reason.
func TestOrchestratorTickCarriesTheRestWhenALauncherRefusesOne(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	const secondRun = "20261117-051500-ef01"
	doc := residentNode(t, at, [2]string{"p5-1d", residentLaunchRun}, [2]string{"p5-1d-b", secondRun})
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	// The launcher refuses the first attempt the plan reads and answers the
	// second: what the pass does with a plan whose first request fails is the
	// whole of this test.
	launch := &fakeResidentLauncher{refuse: map[string]error{residentLaunchRun: errors.New("the runs bucket holds no task for this run")}}

	rec, logged := tickWithNode(t, map[string]string{statePath: doc}, at, launch)
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s): one attempt a launcher refuses does not make the pass a failure", rec.Code, logged)
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatal(err)
	}
	if pass.Unreported != 2 || pass.Carried != 1 || pass.Left != 0 {
		t.Errorf("the pass reads unreported %d, carried %d, left %d, and the node holds two attempts past their window of which one was refused", pass.Unreported, pass.Carried, pass.Left)
	}
	if len(launch.starts) != 2 {
		t.Fatalf("the launcher was asked for %v, and both attempts were the pass's to carry: a refusal must not stop the rest", launch.starts)
	}
	if !strings.Contains(logged, "could not start an attempt the node records") || !strings.Contains(logged, "holds no task") {
		t.Errorf("the pass logged %q, and a refusal that is not written down is an attempt nobody knows is stuck", logged)
	}
	if strings.Contains(logged, residentLaunchRun) || strings.Contains(logged, secondRun) {
		t.Errorf("the pass logged %q, and the run ids are the node's, not this process's log's", logged)
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
			launch := &fakeResidentLauncher{}

			rec, logged := tickWithNode(t, map[string]string{statePath: doc, budget.PathKillGlobal: tc.node}, at, launch)
			if rec.Code != http.StatusOK {
				t.Fatalf("the tick answered %d (%s), and a stopped pass is still a pass", rec.Code, logged)
			}
			var pass orchestratorPass
			if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
				t.Fatal(err)
			}
			if pass.Unreported != 0 || pass.Carried != 0 || pass.Left != 0 {
				t.Errorf("the pass reads unreported %d, carried %d, left %d under the switch, and a switch that is on is read before any attempt is", pass.Unreported, pass.Carried, pass.Left)
			}
			if len(launch.starts) != 0 {
				t.Errorf("the pass asked the launcher for %v under a switch that is on: the kill switch is this pass's too, and the re-issues are not exempt", launch.starts)
			}
			if !strings.Contains(logged, "the budget switch is on") {
				t.Errorf("the pass logged %q, and the line has to say why: \"nothing was committed\" and \"the switch is on\" are the same numbers otherwise", logged)
			}
		})
	}
}

// TestOrchestratorTickReadsNothingWhenTheGuardrailsCannotBeRead is carry's
// fail-closed half, which is the half a change can silently turn permissive:
// both branches return before the plan is read, and a version that carried on
// instead would plan under a value nobody read — the zero Caps' kill switch,
// which is the OFF value Caps' own doc forbids, or a clock of this container's
// own, which D1230 forbids for every judgment under the node. Each is driven by
// the read that reaches it and by no other: the switch is made to fail at its
// own path, and the clock is a store that has answered nothing, which is what
// ServerNow's ok = false means.
//
// Both cases assert the same pair. The pass still ANSWERS — a guardrail this
// process could not read is read as ON, and a tick that refused would leave the
// scheduler retrying a read that will fail again — with nothing counted and
// nothing launched, because an attempt started off a guardrail nobody read is
// the permissive reading wearing the fail-closed branch's numbers; and the line
// names the read that failed, because "there was nothing to read" and "the
// guardrail is on" are the same numbers in the pass otherwise.
func TestOrchestratorTickReadsNothingWhenTheGuardrailsCannotBeRead(t *testing.T) {
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		errs map[string]error
		want string
	}{
		{
			name: "the budget switch could not be read",
			at:   at,
			errs: map[string]error{budget.PathKillGlobal: errors.New("the database refused the read")},
			want: "the budget switch could not be read",
		},
		{
			// The zero time is the store that has not answered: ServerNow is
			// (zero, false), which is the state carry reads as "no clock to
			// judge the claim window with".
			name: "the database's clock could not be read",
			want: "the database's clock could not be read",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeOrchestratorStore{
				docs: map[string]string{statePath: residentNodeDoc(t, at)},
				errs: tc.errs,
				at:   tc.at,
			}
			launch := &fakeResidentLauncher{}
			rec, logged := tickForTest(t, store, "aurora", launch)
			if rec.Code != http.StatusOK {
				t.Fatalf("the tick answered %d (%s), and a guardrail this process could not read is read as ON: the pass still answers", rec.Code, logged)
			}
			var pass orchestratorPass
			if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
				t.Fatal(err)
			}
			if pass.Unreported != 0 || pass.Carried != 0 || pass.Left != 0 {
				t.Errorf("the pass reads unreported %d, carried %d, left %d, and the node holds one attempt past its claim window: a count read off a guardrail nobody read is the permissive reading", pass.Unreported, pass.Carried, pass.Left)
			}
			if len(launch.starts) != 0 {
				t.Errorf("the pass asked the launcher for %v behind a guardrail it could not read", launch.starts)
			}
			if !strings.Contains(logged, tc.want) {
				t.Errorf("the pass logged %q, and the line has to name the read that failed (%q): \"there was nothing to read\" and \"the guardrail is on\" are the same numbers otherwise", logged, tc.want)
			}
		})
	}
}

// TestOrchestratorTickReadsNothingForARecentAttempt is the fence around the
// read: an attempt inside its claim window may be a launch that is still in
// flight, so no request is read for it — which is what keeps a pass that cannot
// read the run records from starting the same attempt twice.
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
	launch := &fakeResidentLauncher{}

	rec, logged := tickWithNode(t, map[string]string{statePath: doc}, at, launch)
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s)", rec.Code, logged)
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatal(err)
	}
	if pass.Unreported != 0 || pass.Carried != 0 {
		t.Errorf("the pass reads unreported %d, carried %d, and the attempt is inside its claim window: a launch beside it may still be in flight", pass.Unreported, pass.Carried)
	}
	if len(launch.starts) != 0 {
		t.Errorf("the pass asked the launcher for %v for an attempt inside its claim window", launch.starts)
	}
}

// TestOrchestratorServiceConfigBuildsALauncher is the wiring's own shape, and
// it replaces the review finding this revision answers: the service is built
// with the node, the log AND a launcher, because the plan's re-issues are
// carried only through one — a service with no launcher would read a node and
// refuse every attempt it found, and would be refused at start-up rather than
// serving (newResidentOrchestrator). The launcher the configuration builds is
// the resident one, which is `fugaro run`'s own path behind it
// (orchestrator_launcher.go).
func TestOrchestratorServiceConfigBuildsALauncher(t *testing.T) {
	orchestratorTest = &orchestratorWiring{tokens: func(context.Context) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{}), nil
	}}
	t.Cleanup(func() { orchestratorTest = nil })

	cfg, err := orchestratorServiceConfig(context.Background(), envFrom(orchestratorServeEnv), io.Discard)
	if err != nil {
		t.Fatalf("a complete environment was refused: %v", err)
	}
	if _, ok := cfg.launch.(*residentLauncher); !ok {
		t.Fatalf("the configuration carries %T, and the one launcher this tree has is the resident one over the command's own path", cfg.launch)
	}
	if _, err := newResidentOrchestrator(cfg); err != nil {
		t.Fatalf("the configuration does not build the service: %v", err)
	}
	if _, err := newResidentOrchestrator(orchestratorConfig{id: "aurora", db: cfg.db, log: cfg.log}); err == nil {
		t.Fatal("a service with no launcher was built: it would read a node and refuse every re-issue it found, which is a fault nobody would see in the log")
	}
}
