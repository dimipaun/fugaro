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
	"github.com/dimipaun/fugaro/internal/infra/managed"
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

	// The note is short — a count line and one short line per step, which is
	// the owner's feedback of 2026-11-15 — and it holds the facts whatever this
	// update turns out to do: the update asks no run to finish first and it
	// rewrites nothing a run holds. Not a question by default, not a refusal,
	// and never a request to wait. It does NOT claim the runs are unaffected —
	// the budget mode's raise can bind one at its next lease, and the steps that
	// can are named (the table test below).
	note := inFlightNoteOf(t, out)
	if !strings.Contains(note, "1 run(s) are in flight") {
		t.Errorf("the update's note does not say how many runs are running: %q", note)
	}
	for _, banned := range []string{"wait for them", "refus", "finish first, then"} {
		if strings.Contains(note, banned) {
			t.Errorf("the update's note asks the operator to act (%q): %q", banned, note)
		}
	}
	for _, want := range []string{"asks no run to finish first", "rewrites nothing a run holds"} {
		if !strings.Contains(note, want) {
			t.Errorf("the update's note does not say %q: %q", want, note)
		}
	}
	if strings.Contains(note, "unaffected") {
		t.Errorf("the update's note claims the runs are unaffected, which the budget mode's raise can make false: %q", note)
	}
	// The progress the owner asked for: the count is a cloud read, and the run
	// says what it is doing while it makes it.
	if !strings.Contains(out, "Checking for runs in flight") {
		t.Errorf("the update did not say what it was doing while it counted the runs:\n%s", out)
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

// TestRunInFlightSurvivesPolicyBump is CLAUDE.md's HARD RULE read against this
// release's own bump: the installation is one policy version behind (v6Node,
// the node 0.24.0 leaves), a run of its repository has bootstrapped under that
// release, and `fugaro update` raises the node to version 7 while the run keeps
// running. The node's version moves — the policy stage is what carries a
// version-6 node forward, and the migration is the shape-free step the banner
// above the policy stage promises — and every write the run makes afterwards
// still lands, because the bump asks no run to finish first and rewrites
// nothing a run holds.
//
// The difference from TestUpdateLeavesARunInFlightRunning is the version, and
// it is the whole point: that fixture's node is so old the update refuses the
// node as older than the chain's first step in one place and republishes it in
// another, while this one is the ordinary state of an installation the release
// before this one left behind — the window the bump's own migration exists for,
// and the one a person meets on the day they upgrade.
func TestRunInFlightSurvivesPolicyBump(t *testing.T) {
	r := newConvergeRig(t, v6Node())
	run := startRunInFlight(t, r)

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if got := convergeNode(t, r).Version; got != projectpolicy.Version {
		t.Errorf("after the update the published node is version %d, want this binary's %d: the policy stage did not carry a version-6 node forward, and a run that re-reads the policy mid-run meets a node the release that left it cannot explain", got, projectpolicy.Version)
	}

	// What the run holds is what it held before the update: its record is the
	// one it bootstrapped with, not one the update rewrote or removed.
	ctx := context.Background()
	rec, err := run.store.ReadRecord(ctx)
	if err != nil {
		t.Fatalf("the run's record could not be read after the update: %v", err)
	}
	if rec.Status != runstore.StatusRunning || rec.Stage != "implement" {
		t.Errorf("the run's record reads status %q stage %q after the update, want the running/implement it bootstrapped with: the update rewrote a record a run holds", rec.Status, rec.Stage)
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

// TestUpdateInFlightLineNamesEveryStepThatCanReachARun: the audit found five
// steps of the update that can still reach a run in flight — the budget mode's
// raise to enforce, which a session re-reads on every lease while the policy
// node is read once; the policy node itself, which a project routed through the
// credentials cascade re-reads on every model call; the window between that
// write and the credentials service's own replacement; the creation fact this
// release converges; and a retirement, which deletes an orphaned job and takes
// the executions running on it with it — and the door's note names every one
// this run can take, one short line each.
//
// The rows are the mutation check of that reading: each one turns exactly one
// step on and asserts its line is there, and the other steps' lines are not. The
// first row is the ordinary update — nothing this run does reaches a run in
// flight — where the note says so, and the alternative to every line here,
// silence over a step that then reaches a run, is the failure the requirement
// of 2026-11-09 is about. Every row is also held to the claim the note makes in
// both cases, and to the one it must never make: the budget mode's raise DOES
// reach a run in flight, so "the runs are unaffected" is not a sentence this
// note may print. And every row is held to the note's SHAPE — a count line and
// one short line per step, never the paragraph of reasons the owner's feedback
// of 2026-11-15 asked to be rid of.
func TestUpdateInFlightLineNamesEveryStepThatCanReachARun(t *testing.T) {
	// A published node that turns the cascade on: O3's switch, which is what
	// sends a run's model calls through the credentials service — and so through
	// a policy read per call rather than the runner's one memoized read.
	cascade := map[string]any{
		"version":     projectpolicy.Version,
		"allow_repos": []string{"*"},
		"routing":     map[string]any{"claude": map[string]any{"via": projectpolicy.RouteCascade}},
	}
	// The same node as the release before this one wrote it: the shape every
	// installation holds while the update that bumps Version converges, and the
	// one the door meets before its policy stage has run.
	behindCascade := map[string]any{
		"version":     projectpolicy.Version - 1,
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
			want:    []string{"1 run(s) are in flight", "no step of it can reach them"},
			notWant: []string{"budget mode", "policy:", "retirement", "credentials service", "creation fact"},
		},
		{
			// The raise is a managed project's alone, and so is the retirement
			// it brings with it (retireTakesExecutions): the two lines travel
			// together, which is why this row does not call the retirement
			// clause absent.
			name: "the budget mode's raise", mode: budget.ModeObserve, managed: true,
			want:    []string{"budget mode", "raise to enforce", "retirement", "orphaned job"},
			notWant: []string{"policy:", "credentials service", "creation fact"},
		},
		{
			name: "the policy a cascade project re-reads", node: cascade, mode: budget.ModeEnforce,
			want:    []string{"1 run(s) are in flight", "policy: runs in flight pick up the new policy"},
			notWant: []string{"budget mode", "retirement", "credentials service", "creation fact"},
		},
		{
			// The same project on the node this upgrade actually reads: the one
			// the release before this wrote, which the policy stage has not
			// migrated yet. The line has to be there — the service re-reads
			// the node per call, and the stage is about to rewrite it — and a
			// reader that decoded the node with the refusing Read would answer
			// "no cascade" and print "no step of it can reach them" over it.
			name: "the policy a cascade project re-reads, one version behind", node: behindCascade, mode: budget.ModeEnforce,
			want:    []string{"1 run(s) are in flight", "policy: runs in flight pick up the new policy"},
			notWant: []string{"budget mode", "retirement", "no step of it can reach them"},
		},
		{
			name: "the retirement a managed project runs", mode: budget.ModeEnforce, managed: true,
			want:    []string{"1 run(s) are in flight", "retirement: deletes an orphaned job"},
			notWant: []string{"budget mode", "policy:", "credentials service", "creation fact"},
		},
		{
			name: "the retirement --retire asks for", mode: budget.ModeEnforce, retire: true,
			want:    []string{"1 run(s) are in flight", "retirement: deletes an orphaned job"},
			notWant: []string{"budget mode", "policy:", "credentials service", "creation fact"},
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
			if _, err := h.s.e.announceRunsInFlight(context.Background()); err != nil {
				t.Fatalf("the door's note failed the run: %v\n%s", err, h.out)
			}

			out := strings.TrimSpace(h.out.String())
			lines := strings.Split(out, "\n")
			if len(lines) > 6 {
				t.Errorf("the door's note is %d lines, want the count and one line per step:\n%s", len(lines), out)
			}
			for i, l := range lines {
				if i == 0 {
					continue // the count line, which carries the two facts
				}
				if !strings.HasPrefix(l, "  ") {
					t.Errorf("line %d of the note is not one of its step lines: %q\n%s", i+1, l, out)
				}
				if words := len(strings.Fields(l)); words > 14 {
					t.Errorf("line %d of the note is %d words long, want a short line:\n%s", i+1, words, out)
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("the note does not say %q:\n%s", want, out)
				}
			}
			for _, bad := range tc.notWant {
				if strings.Contains(out, bad) {
					t.Errorf("the note names %q over a step this run cannot take:\n%s", bad, out)
				}
			}
			// The two facts the note states in every case, and the claim it
			// never makes: the runs are not "unaffected" — the budget mode's
			// raise reaches one at its next lease, which is the whole of this
			// reading.
			for _, want := range []string{"asks no run to finish first", "rewrites nothing a run holds"} {
				if !strings.Contains(out, want) {
					t.Errorf("the note does not state %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "unaffected") {
				t.Errorf("the note claims the runs are unaffected, which the budget mode's raise can make false:\n%s", out)
			}
		})
	}
}

// TestUpdateInFlightAskOffersToStopBeforeAnythingIsWritten is the owner's
// second ask of 2026-11-15: after the note, at a real terminal, the person who
// read it may stop the update there — and stopping means the installation is
// exactly as it was, before the first write of the run (the note is printed
// before syncAccess, whose grants-node write is that first write).
//
// The question is behind --ask (asksBack: the flag AND a gate with something to
// restore for), which is what keeps the no-prompt policy true — a managed
// project without the flag asks nothing, and a coding agent's session, a pipe
// and --json are answered without a prompt. The second case below is the door
// with an empty answer, which is the default a person gets by pressing Enter,
// and it GOES ON: an update must never refuse over a run in flight, so a
// courtesy whose Enter key stopped the upgrade would be that refusal wearing a
// prompt's clothes.
func TestUpdateInFlightAskOffersToStopBeforeAnythingIsWritten(t *testing.T) {
	note := func(t *testing.T, answer string) (string, bool, error) {
		t.Helper()
		f := newProjectFixture(t, "")
		f.db.Set(budget.PathMode, budget.ModeEnforce)
		f.db.Set("agents", map[string]any{"proj-1234": map[string]any{"run-1": map[string]any{"repo": "github/acme/app"}}})
		h := newMigrationRig(t, f, &initOptions{ask: true}).managedGate()
		// The rig's run holds the root command and the flag is the update
		// door's own: the real run holds the subcommand with its flags parsed,
		// which is the state this puts it in without executing anything.
		cmd, _, err := h.cmd.Find([]string{"update"})
		if err != nil {
			t.Fatal(err)
		}
		if cmd.Flags().Lookup("ask") == nil {
			t.Fatal("the update door has no --ask, so this test would read the flag's absence as an answer")
		}
		if err := cmd.Flags().Set("ask", "true"); err != nil {
			t.Fatal(err)
		}
		cmd.SetIn(strings.NewReader(answer))
		h.s.e.r.cmd = cmd
		fakeTerminal(t)
		noAgentSession(t)
		stop, err := h.s.e.announceRunsInFlight(context.Background())
		return h.out.String(), stop, err
	}
	t.Run("a yes stops before anything is written", func(t *testing.T) {
		out, stop, err := note(t, "y\n")
		if err != nil {
			t.Fatalf("the answer to the door's question failed the run: %v\n%s", err, out)
		}
		if !stop {
			t.Fatalf("a yes did not stop the update:\n%s", out)
		}
		if !strings.Contains(out, "1 run(s) are in flight") {
			t.Errorf("the question was put without the note it is about:\n%s", out)
		}
		if !strings.Contains(out, "stop here, before anything is written?") {
			t.Errorf("the door did not put its question:\n%s", out)
		}
		if !strings.Contains(out, "stopped at your answer: nothing was written") {
			t.Errorf("the run did not say it stopped and wrote nothing:\n%s", out)
		}
	})
	t.Run("an empty answer goes on", func(t *testing.T) {
		out, stop, err := note(t, "\n")
		if err != nil || stop {
			t.Fatalf("an empty answer stopped the update (stop=%v, err=%v), which is the refusal the HARD RULE forbids:\n%s", stop, err, out)
		}
		if strings.Contains(out, "stopped at your answer") {
			t.Errorf("an empty answer printed the stop line:\n%s", out)
		}
	})
	t.Run("a run that did not pass --ask is not asked", func(t *testing.T) {
		f := newProjectFixture(t, "")
		f.db.Set("agents", map[string]any{"proj-1234": map[string]any{"run-1": map[string]any{"repo": "github/acme/app"}}})
		h := newMigrationRig(t, f, &initOptions{}).managedGate()
		fakeTerminal(t)
		noAgentSession(t)
		h.cmd.SetIn(strings.NewReader("y\n"))
		stop, err := h.s.e.announceRunsInFlight(context.Background())
		if err != nil || stop {
			t.Fatalf("a run that did not pass --ask was asked, or stopped (stop=%v, err=%v):\n%s", stop, err, h.out)
		}
		if strings.Contains(h.out.String(), "stop here, before anything is written?") {
			t.Errorf("the door asked its question without the flag that puts questions back:\n%s", h.out)
		}
		if !strings.Contains(h.out.String(), "1 run(s) are in flight") {
			t.Errorf("the note was not printed at all, so the third case proves nothing:\n%s", h.out)
		}
	})
}

// TestUpdateInFlightLineNamesTheCredentialsServiceWindow is the fourth step the
// line names, and the one this release's reorder created: the update raises the
// published node's version in the policy stage and replaces the service that
// reads that node per call in the stage immediately after it, so a call landing
// between the two meets the previous release's service with a node its own
// binary does not understand and is refused. The service is read by a cascade
// project's model calls and by the Bitbucket-proxied calls alike — which is what
// the clause is about, and why it is named over a project whose routing is not
// the cascade's.
//
// The second case is the pair's negative half: the same stale service over a
// node this binary already publishes is moved with nothing raised, so no call
// meets a service that cannot read the node, and the line names no such step.
// Both halves are read off the line the door really printed, over a run really
// in flight, rather than off the question the clause is gated on.
func TestUpdateInFlightLineNamesTheCredentialsServiceWindow(t *testing.T) {
	line := func(t *testing.T, node map[string]any) string {
		t.Helper()
		r := newConvergeRig(t, node)
		// The service an earlier release left, and this release's image in the
		// fake source: the move runs, which is the window's second half. The
		// installation root's plan holds the revision moving (a plan that reports
		// no change is never applied), and the outputs name the service the
		// tfvars carry, as the module's own do.
		publishCredentialsImage(t, r.imagesRig, convergeRelease)
		seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
		r.script["show -json"] = r.script["show"]
		r.script["plan@installation"] = map[string]any{"exit": 2}
		r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
			"credentials_service": credentialsServiceOutput(),
			"intake":              intakeServiceOutput(),
		})}
		r.save(t)
		startRunInFlight(t, r)
		return inFlightNoteOf(t, convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"}))
	}
	t.Run("the node's version rises and the service moves", func(t *testing.T) {
		got := line(t, v6Node())
		for _, want := range []string{"credentials service:", "cannot read the node"} {
			if !strings.Contains(got, want) {
				t.Errorf("the note does not say %q over a run whose node is raised before the service that reads it is replaced:\n%s", want, got)
			}
		}
		if strings.Contains(got, "no step of it can reach them") {
			t.Errorf("the note claims no step can reach a run in flight, over the window this update opens:\n%s", got)
		}
	})
	t.Run("the service moves over a node that does not", func(t *testing.T) {
		got := line(t, v7Node())
		if strings.Contains(got, "credentials service") {
			t.Errorf("the note names the credentials service over a run that raises no node, so no call can meet a service that cannot read it:\n%s", got)
		}
		if !strings.Contains(got, "no step of it can reach them") {
			t.Errorf("the note names no step at all over a run this update leaves where it is:\n%s", got)
		}
	})
}

// TestUpdateInFlightLineNamesTheMarkerConvergence is the fifth step the line
// names, and the one D1543 adds: this update rewrites the installation's
// creation fact out of version 3 (update_marker_version.go, T10), and the run
// in flight is the release behind, whose reader REFUSES the version it is
// rewritten to rather than reading it as the shape it resembles. So the step is
// one that changes what a run in flight makes of the marks — a command of that
// run re-reads them per command (D1015) and will answer "not managed", keeping
// the typed confirmation it would have applied without — and a person who sees
// a run stop taking the gate is owed the step that did it.
//
// The second case is the negative half: the same run over a fact this build
// already writes, where the step leaves the object exactly where it is and the
// line names no such thing. The third is the other gate: a version-3 object on a
// project nothing has labelled is not a fact the read believes, so the step has
// nothing to converge and says nothing.
//
// All three are read off the line the door really printed, over a run really in
// flight and over the real marker read — the bucket's project number, the
// bucket's own label and the project's label — rather than off the question the
// clause is gated on.
func TestUpdateInFlightLineNamesTheMarkerConvergence(t *testing.T) {
	line := func(t *testing.T, fact string, isManaged bool) string {
		t.Helper()
		// The rig is the marker's own (convergeRigWithMarker), so this test reads
		// the line the door prints over the very installation
		// TestPreviousMarkerVersionConvergesOnUpdate converges.
		var r *convergeRig
		if isManaged {
			r = convergeRigWithMarker(t, fact)
		} else {
			r = newConvergeRig(t, v3Node(nil))
			r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
			r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(fact))
		}
		startRunInFlight(t, r)
		return inFlightNoteOf(t, convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "plugin-pin"}))
	}
	t.Run("a version-3 fact is rewritten", func(t *testing.T) {
		got := line(t, markerBuildsFact, true)
		for _, want := range []string{"creation fact:", "keeps its typed confirmation"} {
			if !strings.Contains(got, want) {
				t.Errorf("the note does not say %q over a run whose installation still carries a version-3 creation fact:\n%s", want, got)
			}
		}
		if strings.Contains(got, "no step of it can reach them") {
			t.Errorf("the note claims no step can reach a run in flight, over the fact this update rewrites:\n%s", got)
		}
	})
	t.Run("a fact this build already writes is left alone", func(t *testing.T) {
		got := line(t, markerNowFact, true)
		if strings.Contains(got, "creation fact") {
			t.Errorf("the note names the marker's convergence over a fact that is already this build's:\n%s", got)
		}
	})
	t.Run("a version-3 object the read does not believe", func(t *testing.T) {
		got := line(t, markerBuildsFact, false)
		if strings.Contains(got, "creation fact") {
			t.Errorf("the note names the marker's convergence over an object the managed read does not believe, so the step has nothing to converge:\n%s", got)
		}
		if !strings.Contains(got, "no step of it can reach them") {
			t.Errorf("the note names a step over a run this update leaves where it is:\n%s", got)
		}
	})
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

// inFlightNoteOf is the update's own note about the runs in flight: the line
// that names them and every short line under it, and the whole of what the door
// says about them.
//
// It is the note and not one line, because the note is one line per step
// (update_in_flight.go's inFlightNote, the owner's feedback of 2026-11-15): the
// count and the two facts that hold whatever this update does, then one short
// line per step that can still reach a run. The log is read beside the screen
// because the note is printed to both — a person reading the log of an update
// that failed has to find the runs it was about there too.
func inFlightNoteOf(t *testing.T, out string) string {
	t.Helper()
	note := func(s string) string {
		lines := strings.Split(s, "\n")
		start := -1
		for i, line := range lines {
			if strings.Contains(line, "run(s) are in flight") {
				start = i
				break
			}
		}
		if start < 0 {
			return ""
		}
		end := start + 1
		for end < len(lines) && strings.HasPrefix(lines[end], "  ") {
			end++
		}
		return strings.Join(lines[start:end], "\n")
	}
	screen := note(out)
	if screen == "" {
		t.Fatalf("the update printed nothing about the runs in flight:\n%s", out)
	}
	return screen + "\n" + note(newestLog(t, "update"))
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
