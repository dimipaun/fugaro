package cli

import (
	"bufio"
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// stages_subset_test.go pins AP2 (docs/composite-commands, spec §2.5 rule 2,
// D1561): a command that changes one thing runs the steps that change needs,
// named, in place of the whole list `fugaro update` runs — and a named subset
// is a SLICE of an update and never a shortcut through one. Three things that
// means, and one test each:
//
//   - the answer is in initflow's order and not the caller's, so a stage is
//     never handed to the loop before one it needs that is in the same subset,
//     and a name no stage of this door answers is reported rather than dropped
//     (TestStagesSubsetKeepsOrderAndNeeds);
//   - `accounts add`, the first command to run a subset, runs the three names
//     spec §2.5 gives it and no others, out of the stages the door registers
//     (TestAccountsAddStillRunsItsThree);
//   - every stage a subset carries reaches the same verdict it reaches in the
//     full list — the door's own agent refusal, the managed project's gate and
//     its record, the stage's own loosening judgement — and nothing is applied
//     that an update would not apply (TestEachStageKeepsItsAgentRefusalAndAuditRecord).
//
// The sweeps drive initflow.Run over `stagesNamed(e, name)` one stage at a
// time, because that is the path a subset takes and the one this task adds: a
// door's own converge (initEngine.converge) runs the full list and no
// composite command calls it.

// TestStagesSubsetKeepsOrderAndNeeds is the ordering rule, over every door the
// CLI registers stages for: the subset comes back in initflow's canonical order
// whatever order it was asked for in, every name the door registers is
// answered, a name it registers no stage under — a stage of initflow's the door
// leaves out, or a name that is no stage at all — is reported and not dropped,
// and no subset ever puts a stage before one it needs.
//
// The red step is the second field of the pairs below: an implementation that
// answered the caller's own order — or that filtered initStages() without
// re-ordering the answer — passes everything else here and fails on the pairs,
// which is the whole reason this is a helper in this package rather than a loop
// at each call site.
func TestStagesSubsetKeepsOrderAndNeeds(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    initOptions
	}{
		{"update", initOptions{door: updateDoor.name}},
		{"update with the batch backend", initOptions{door: updateDoor.name, provisionBatch: true}},
		{"setup", initOptions{door: setupDoor.name}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := prodEngine(tc.o)
			// The stages this door registers, built once: identity against a
			// second initStages() call would be identity against a second set of
			// engines, and the canonical order comes from initflow.Validate
			// because initStages() appends the stages behind a flag last (the
			// project one, of a setup run) and the loop is what orders them.
			built := initStages(e)
			ordered, err := initflow.Validate(built)
			if err != nil {
				t.Fatal(err)
			}
			registered := namesOf(ordered)

			// Every name this door registers, asked for backwards: the answer is
			// the door's list in the door's own order, with nothing unanswered —
			// the full list is the subset that is not a subset.
			reversed := slices.Clone(registered)
			slices.Reverse(reversed)
			all, unanswered := stagesNamed(e, reversed...)
			if len(unanswered) != 0 {
				t.Errorf("names this door registers came back unanswered (%v)", unanswered)
			}
			if got := namesOf(all); !slices.Equal(got, registered) {
				t.Errorf("the subset of every name = %v, want the registered order %v", got, registered)
			}

			// Each registered name, alone and named twice: one stage, the very
			// stage initStages() registered (a copy would be a second
			// registration, which initflow.Validate refuses), once.
			for _, name := range registered {
				one, unanswered := stagesNamed(e, name, name)
				if len(unanswered) != 0 {
					t.Errorf("%s came back unanswered", name)
				}
				if len(one) != 1 {
					t.Fatalf("naming %s twice answered %d stage(s), want one: a subset is a set", name, len(one))
				}
				want, _ := stageNamed(built, name)
				if reflect.TypeOf(one[0]) != reflect.TypeOf(want) {
					t.Errorf("the subset carries %s as %T, want the stage this door builds (%T)", name, one[0], want)
				}
			}

			// Every ordered pair, asked for in both orders: the answer is the
			// canonical order, which is the one in which a stage's predecessors
			// precede it.
			for _, a := range registered {
				for _, b := range registered {
					if a == b {
						continue
					}
					got, _ := stagesNamed(e, b, a)
					ahead, behind := a, b
					if slices.Index(registered, a) > slices.Index(registered, b) {
						ahead, behind = b, a
					}
					if names := namesOf(got); !slices.Equal(names, []string{ahead, behind}) {
						t.Errorf("the subset {%s, %s} = %v, want [%s %s]: the caller's order is not the run's", a, b, names, ahead, behind)
					}
				}
			}

			// And the edges themselves, read off initflow and not written down
			// here: a subset that names a stage and the stages it needs runs the
			// needs first. A predecessor this door registers no stage under (the
			// services slot, which neither door fills) is not required —
			// initflow.Validate's own rule — so only the registered ones are asked
			// for.
			for _, name := range registered {
				var needs []string
				for _, need := range initflow.Needs(name) {
					if slices.Contains(registered, need) {
						needs = append(needs, need)
					}
				}
				if len(needs) == 0 {
					continue
				}
				got, unanswered := stagesNamed(e, append([]string{name}, needs...)...)
				if len(unanswered) != 0 {
					t.Errorf("the subset of %s and the stages it needs came back with %v unanswered", name, unanswered)
				}
				names := namesOf(got)
				if names[len(names)-1] != name {
					t.Errorf("the subset of %s and the stages it needs = %v, want %s last", name, names, name)
				}
			}

			// A name no stage of this door answers comes back in the second
			// answer and is not dropped: apply's one step that is not a stage at
			// all, and a name nothing registers.
			got, unanswered := stagesNamed(e, initflow.Policy, apply.CatalogBuild, "not-a-stage")
			if gotNames := namesOf(got); !slices.Equal(gotNames, []string{initflow.Policy}) {
				t.Errorf("the subset = %v, want the policy stage alone", gotNames)
			}
			if !slices.Equal(unanswered, []string{apply.CatalogBuild, "not-a-stage"}) {
				t.Errorf("unanswered = %v, want the two names no stage answers", unanswered)
			}
			for _, name := range registered {
				if slices.Contains(unanswered, name) {
					t.Errorf("%s is a stage this door registers and came back unanswered", name)
				}
			}

			// And it is this door's registration that decides, not initflow's
			// table: a stage of initflow's that this door registers no stage
			// under is unanswered too, and nothing runs for it. Naming the
			// services slot against the update door is the sharpest case — the
			// name is a stage of the canonical order and no door builds one —
			// and the project stage and the Batch backend's compute are the
			// other two, registered behind `setup`'s first run and behind their
			// own flag. A helper that answered initflow's name instead of
			// dropping it would run nothing and say nothing, which is the one
			// thing a subset may not do.
			unregistered := 0
			for _, name := range initflow.Names() {
				if slices.Contains(registered, name) {
					continue
				}
				unregistered++
				got, unanswered := stagesNamed(e, name)
				if len(got) != 0 || !slices.Equal(unanswered, []string{name}) {
					t.Errorf("the subset of %s, a stage of initflow's this door registers no stage for, = %v unanswered %v, want nothing run and the name reported", name, namesOf(got), unanswered)
				}
			}
			if unregistered == 0 {
				t.Fatal("this door registers every stage of initflow's, so the loop above proved nothing about the ones a door leaves out")
			}
		})
	}
}

// TestAccountsAddStillRunsItsThree is the regression pin on the first command
// that ran a subset by hand (accountsAddStages, spec §2.5): the three names are
// still exactly the three, they are the stages initStages() registered and not
// copies, and the list is one initflow.Run accepts — the loop's own checker,
// asked rather than assumed.
//
// What the three DO when they run is accounts_add_converge_test.go's
// (TestAccountsAddLeavesNothingToRun: the node, the job, the secret), and this
// test does not restate it: what a stage list can get wrong is which stages are
// in it, which is what a rewire onto stagesNamed can change.
//
// The zero door is the registered set the command's own door gets for these
// three names: what a door decides is the unit stage, whether the project
// stage is registered at all and whether the Batch backend's is, and none of
// the three is one of them (initStages).
func TestAccountsAddStillRunsItsThree(t *testing.T) {
	e := prodEngine(initOptions{})
	subset := accountsAddStages(e)
	want := []string{initflow.Installation, initflow.Repository, initflow.Policy}
	if got := namesOf(subset); !slices.Equal(got, want) {
		t.Fatalf("accounts add runs %v, want %v in this order", got, want)
	}
	registered := initStages(e)
	for _, st := range subset {
		mine, ok := stageNamed(registered, st.Name())
		if !ok {
			t.Fatalf("accounts add runs a stage (%s) this door does not register", st.Name())
		}
		if reflect.TypeOf(st) != reflect.TypeOf(mine) {
			t.Errorf("accounts add runs %s as %T, want the stage this door builds (%T)", st.Name(), st, mine)
		}
	}
	// The other half of an update's list is not this command's: a stage the
	// command does not name is not in its run, whatever the door registers.
	if len(subset) != len(want) {
		t.Errorf("accounts add runs %d stages, want %d", len(subset), len(want))
	}
	ordered, err := initflow.Validate(subset)
	if err != nil {
		t.Fatalf("the loop refuses the list accounts add runs: %v", err)
	}
	if got := namesOf(ordered); !slices.Equal(got, want) {
		t.Errorf("the loop orders accounts add's three as %v, want %v", got, want)
	}
}

// TestEachStageKeepsItsAgentRefusalAndAuditRecord is F5 through the subset
// path: taking a stage out of the full list and running it alone does not take
// its refusal, its gate or its record with it. It is per stage for the first
// arm, which is the one every registered stage is crossed with, and the rest of
// the arms are the policy stage's, because that is the stage whose own refusal
// the plan's mutation check removes.
func TestEachStageKeepsItsAgentRefusalAndAuditRecord(t *testing.T) {
	// The door's stages, one at a time, in a project that is not managed and a
	// coding agent's session: nothing is applied, nothing is recorded, and a
	// stage the guard holds back says so in the session's own sentence — never
	// in the gate's, which has no marks to judge here. The sentence is the
	// run's own line for the stage (the loop reports it as the stage's detail,
	// loop.go's agentApplied) and not the stage's Left, which is the command
	// that runs the step at the owner's own terminal.
	t.Run("nothing is applied and nothing is recorded", func(t *testing.T) {
		const marker = "CLAUDECODE"
		r, _ := updateBudgetRig(t)
		t.Setenv(marker, "1")
		updating(t, r)
		r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
		e := rigEngine(t, r, &initOptions{door: updateDoor.name})
		refused := 0
		for _, name := range namesOf(initStages(e)) {
			subset, unanswered := stagesNamed(e, name)
			if len(unanswered) != 0 || len(subset) != 1 {
				t.Fatalf("the subset of %s = %v, unanswered %v", name, namesOf(subset), unanswered)
			}
			res, err := initflow.Run(t.Context(), subset, e.options())
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			state, row := subsetStageRow(res, name)
			if state == initflow.Changed {
				t.Errorf("%s applied in a coding agent's session:\n%s", name, statesText(res))
			}
			if state != initflow.NeedsYou {
				continue
			}
			refused++
			if want := initflow.AgentRefusal(marker); row.Detail != want {
				t.Errorf("%s was refused with %q, want the session's own sentence %q", name, row.Detail, want)
			}
		}
		if refused == 0 {
			t.Fatal("no stage of the sweep was held back in a coding agent's session, so the refusal half of this test proved nothing")
		}
		if calls := r.ran(t, "apply"); len(calls) != 0 {
			t.Errorf("a run that only names stages applied terraform: %q", calls)
		}
		if recs := audits(t, r); len(recs) != 0 {
			t.Errorf("a project that is not managed recorded %d audit record(s): %+v", len(recs), recs)
		}
	})

	// The same door's stages, one at a time, on a managed project under the same
	// marker: the gate stands in for the confirmation, so a stage that applies
	// is one the gate judged — and every one of them leaves its record. This is
	// the "audit record" half, per stage: a subset that lost the record would be
	// a step no owner can read afterwards.
	t.Run("a stage that applies is recorded", func(t *testing.T) {
		r, db := updateBudgetRig(t)
		r.managedRuns(t, initProjectName, initProject)
		t.Setenv("CLAUDECODE", "1")
		isolateGateCredentials(t)
		updating(t, r)
		r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
		// The rules marker an installation that has converged carries, so the
		// policy stage is judgeable in its own run and not held back by a
		// precondition (project_cmd.go's requirePolicyRules).
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		e := rigEngine(t, r, &initOptions{door: updateDoor.name})
		applied := 0
		for _, name := range namesOf(initStages(e)) {
			subset, _ := stagesNamed(e, name)
			before := len(audits(t, r))
			res, err := initflow.Run(t.Context(), subset, e.options())
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			state, _ := subsetStageRow(res, name)
			if state != initflow.Changed {
				continue
			}
			applied++
			recs := audits(t, r)[before:]
			if len(recs) == 0 {
				t.Errorf("%s applied with no record of what was judged:\n%s", name, statesText(res))
				continue
			}
			for _, rec := range recs {
				if rec.Command != "fugaro update" {
					t.Errorf("%s's record names the command %q", name, rec.Command)
				}
			}
		}
		if applied == 0 {
			t.Fatal("no stage of the sweep applied anything in a managed project, so the record half of this test proved nothing")
		}
	})

	// The policy stage's own refusal, on its own, with the loop opening the
	// stage while this run holds no gate answer at all: Options.ManagedGate is
	// the caller's own statement about a gate the caller read (initflow.Options),
	// and a stage handed to its apply path on another caller's word has nothing
	// to stand in for the confirmation — so the session's refusal is the stage's
	// and the write does not happen. The mutation the plan names lands here: drop
	// the refusal from the stage and this run publishes the node instead.
	t.Run("the policy stage refuses on its own when no gate judged it", func(t *testing.T) {
		const marker = "CLAUDECODE"
		r, db := updateBudgetRig(t)
		t.Setenv(marker, "1")
		isolateGateCredentials(t)
		updating(t, r)
		r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		db.Set(projectpolicy.PathShared(), v3Node(nil))
		e := rigEngine(t, r, &initOptions{door: updateDoor.name})
		out := &syncBuf{}
		e.r.w = out
		subset, _ := stagesNamed(e, initflow.Policy)
		opts := e.options()
		opts.ManagedGate = func(string) bool { return true }
		res, err := initflow.Run(t.Context(), subset, opts)
		if err != nil {
			t.Fatalf("%v", err)
		}
		state, row := subsetStageRow(res, initflow.Policy)
		if state == initflow.Changed {
			t.Errorf("the policy stage published a node no gate had judged:\n%s", statesText(res))
		}
		if want := initflow.AgentRefusal(marker); row.Detail != want {
			t.Errorf("the run's refusal reads %q, want the session's own sentence %q:\n%s", row.Detail, want, out.String())
		}
		// And what the person is left is the stage's own command all the same:
		// the step is theirs, refused here because nobody at this end may run
		// it, and the route to it is not lost with the refusal.
		if left := subsetLeft(res, initflow.Policy); left == nil || left.Kind != initflow.LeftCommand {
			t.Errorf("the refused step's left-for-you is %+v, want the stage's own command", left)
		}
		if v := nodeVersionOn(t, db); v != 3 {
			t.Errorf("the published node is version %d, want the 3 it started at", v)
		}
		if recs := audits(t, r); len(recs) != 0 {
			t.Errorf("a step no gate judged was recorded: %+v", recs)
		}
	})

	// D1024's window, read by the subset path: the run read the marks and the
	// project IS managed, a coding agent's session is present, and the project
	// stops being managed between that read and the step's own second read — so
	// the admission the loop opened the stage for is gone by the time the step
	// is in hand. What the session reads is its own refusal and not the gate's
	// sentence, and nothing is written: the same sentence the full update gives
	// this window (TestUpdateRevokedMidRunKeepsTheOwnersTerminal is the owner's
	// terminal on the other side of it).
	t.Run("a lost admission is the session's refusal, not the gate's", func(t *testing.T) {
		const marker = "CLAUDECODE"
		r, db := updateBudgetRig(t)
		r.managedRuns(t, initProjectName, initProject)
		t.Setenv(marker, "1")
		isolateGateCredentials(t)
		updating(t, r)
		r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		db.Set(projectpolicy.PathShared(), v3Node(nil))
		// The run's own read, in a project that is managed at that moment — the
		// gate this run holds — and then the owner unflagging the project before
		// the step's write, which is the interval D1024 narrows.
		gate, err := readManagedGate(context.Background(), managedGateEnv{
			lc:  &localcfg.Config{Name: initProjectName, GCPProject: initProject, RunsBucket: initRunsBucket},
			s:   managedStorage(t, r.gcs),
			crm: managedCRM(t, r.crm),
		}, managedGateFacts{Subcommand: "update", Declined: "nothing was applied"})
		if err != nil {
			t.Fatal(err)
		}
		if !gate.managed {
			t.Fatal("the rig's project did not read as managed, so there is no revocation for this test to make")
		}
		r.crm.SetLabels(initProject, map[string]string{managed.ProjectLabelKey: "no"})

		e := rigEngine(t, r, &initOptions{door: updateDoor.name})
		out := &syncBuf{}
		e.r.w = out
		e.r.in = bufio.NewReader(strings.NewReader(""))
		e.r.cmd.SetIn(strings.NewReader(""))
		e.r.gate = &initGate{g: gate, managed: true}
		subset, _ := stagesNamed(e, initflow.Policy)
		res, err := initflow.Run(t.Context(), subset, e.options())
		if err != nil {
			t.Fatalf("%v", err)
		}
		state, row := subsetStageRow(res, initflow.Policy)
		if state == initflow.Changed {
			t.Errorf("the revoked step wrote the node anyway:\n%s", statesText(res))
		}
		if want := initflow.AgentRefusal(marker); row.Detail != want {
			t.Errorf("the revoked step's refusal reads %q, want the session's own sentence %q (and not the gate's)", row.Detail, want)
		}
		if left := subsetLeft(res, initflow.Policy); left == nil || left.Kind != initflow.LeftCommand {
			t.Errorf("the revoked step's left-for-you is %+v, want the stage's own command and not the gate's", left)
		}
		// Neither of the gate's two sentences is this: the run read the marks
		// and the project was managed when it did, so what the step met is its
		// own session, not a ceremony that judged it and said no.
		if strings.Contains(row.Detail, "managed gate") {
			t.Errorf("the revoked step was refused by the gate's own sentence: %q", row.Detail)
		}
		if v := nodeVersionOn(t, db); v != 3 {
			t.Errorf("the published node is version %d, want the 3 it started at:\n%s", v, out.String())
		}
		if recs := audits(t, r); len(recs) != 0 {
			t.Errorf("a step whose admission was revoked was recorded: %+v", recs)
		}
	})

	// And the same stage, alone, in a managed project whose marks stand: the
	// gate judges the node, the step applies, the session reads the sentence
	// that says the gate stood in, and the record is the plan's own digest — the
	// facts TestUpdateManagedPolicyStageUnderTheGate pins for the full update,
	// read here through the subset, which is what says a subset is a slice of an
	// update.
	t.Run("the admitted policy step is the one record", func(t *testing.T) {
		r, db := updateBudgetRig(t)
		r.managedRuns(t, initProjectName, initProject)
		t.Setenv("CLAUDECODE", "1")
		isolateGateCredentials(t)
		updating(t, r)
		r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		db.Set(projectpolicy.PathShared(), v3Node(nil))
		e := rigEngine(t, r, &initOptions{door: updateDoor.name})
		out := &syncBuf{}
		e.r.w = out
		subset, _ := stagesNamed(e, initflow.Policy)
		res, err := initflow.Run(t.Context(), subset, e.options())
		if err != nil {
			t.Fatalf("%v", err)
		}
		state, _ := subsetStageRow(res, initflow.Policy)
		if state != initflow.Changed {
			t.Fatalf("the admitted policy step did not rewrite the node: %s\n%s", state, out.String())
		}
		if !strings.Contains(out.String(), initflow.AgentProceeded("CLAUDECODE")) {
			t.Errorf("the admitted step did not say it proceeded:\n%s", out.String())
		}
		if v := nodeVersionOn(t, db); v != projectpolicy.Version {
			t.Errorf("the published policy is version %d, want %d", v, projectpolicy.Version)
		}
		recs := audits(t, r)
		if len(recs) != 1 {
			t.Fatalf("%d record(s) for one judged node, want one: %+v", len(recs), recs)
		}
		if rec := recs[0]; rec.Verdict != managedmode.AuditAdmitted || rec.Command != "fugaro update" || rec.PlanDigest == "" {
			t.Errorf("the record reads %+v, want an admission of this node by this command, with the plan's own digest", rec)
		}
	})
}

// subsetLeft is what a run left for the person under a stage's name, if
// anything. (update_managed_test.go's leftFor reads the printed block; this is
// the record itself.)
func subsetLeft(res *initflow.Result, name string) *initflow.Left {
	for i := range res.Left {
		if res.Left[i].Stage == name {
			return &res.Left[i]
		}
	}
	return nil
}

// stageRow is the stage's own record in a run: its state and, for a stage
// that did not run, the sentence the loop filed it under. The refusal a
// coding agent's session reads is that detail — AgentError.Error, put there by
// loop.go's agentApplied — while the stage's Left is the command the step is
// run with at the owner's terminal.
func subsetStageRow(res *initflow.Result, name string) (initflow.State, initflow.StageResult) {
	for _, st := range res.Stages {
		if st.Name == name {
			return st.State, st
		}
	}
	return "", initflow.StageResult{}
}

// statesText is a run's per-stage states, for a failure message: a sweep over
// every stage reports what each reached, not only the one that failed.
func statesText(res *initflow.Result) string {
	var b strings.Builder
	for _, st := range res.Stages {
		fmt.Fprintf(&b, "  %-20s %-9s %s\n", st.Name, st.State, st.Detail)
	}
	for _, l := range res.Left {
		fmt.Fprintf(&b, "  left for you (%s, %s): %s\n", l.Stage, l.Kind, l.Text)
	}
	return b.String()
}
