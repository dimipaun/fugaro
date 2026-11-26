package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/lift"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_lift_test.go holds M2's tests (docs/repo-override-rulings/
// implementation-plan.md): the lift stage says first and writes once, records a
// repository only after the layer stage's publish of its edits has landed,
// migrates a repository once, names what it could not read or expand, and
// writes nothing at all under --plan-only.
//
// The registered planners are tested in internal/lift,
// so most tests here replace the stage's two seams: the planners it runs and the repositories it reads them
// over. That is what the seams are for — the stage's own promise is the order
// of saying and writing, the records, and the gates, and none of it depends on
// which item produced a change.

// liftFixture is the world one test's run sees: the repositories the reader
// answers, the admits it could not expand, and the planners, with the calls
// each of them got recorded.
type liftFixture struct {
	repos     []lift.Repo
	patterns  []string
	plans     map[string]lift.Planner
	order     []string
	calls     map[string]int
	reposSeen []lift.Repo
	migrated  []map[string]bool
}

func newLiftFixture(t *testing.T, repos []lift.Repo, patterns ...string) *liftFixture {
	t.Helper()
	f := &liftFixture{
		repos: repos, patterns: patterns,
		plans: map[string]lift.Planner{}, calls: map[string]int{},
	}
	oldPlanners, oldRepos := liftPlanners, liftRepositories
	// The name order the registry promises: the fixture's planners come back
	// the way lift.Planners() answers them, so a line's position is the
	// registry's and not the order a test named them in.
	liftPlanners = func() []lift.NamedPlanner {
		out := make([]lift.NamedPlanner, 0, len(f.order))
		for _, name := range f.order {
			out = append(out, lift.NamedPlanner{Name: name, Plan: f.wrap(name)})
		}
		slices.SortFunc(out, func(a, b lift.NamedPlanner) int { return strings.Compare(a.Name, b.Name) })
		return out
	}
	liftRepositories = func(context.Context, *initEngine, *localcfg.Config, []string) ([]lift.Repo, []string, error) {
		return f.repos, f.patterns, nil
	}
	t.Cleanup(func() { liftPlanners, liftRepositories = oldPlanners, oldRepos })
	return f
}

// plan registers one item's planner.
func (f *liftFixture) plan(name string, p lift.Planner) {
	f.plans[name] = p
	f.order = append(f.order, name)
}

// wrap is the call the fixture records on the way to the planner: how often it
// ran, over which repositories, and what the stage told it about the
// repositories already migrated (D3305).
func (f *liftFixture) wrap(name string) lift.Planner {
	return func(proj lift.Project, repos []lift.Repo) (lift.Change, error) {
		f.calls[name]++
		f.reposSeen = repos
		f.migrated = append(f.migrated, proj.Migrated)
		return f.plans[name](proj, repos)
	}
}

// slug is the storage slug of a repository of the fixture.
func liftSlug(t *testing.T, repo string) string {
	t.Helper()
	slug, err := task.Slug("github", repo)
	if err != nil {
		t.Fatal(err)
	}
	return slug
}

// bucketFile reads one object of the rig's bucket, or answers false for an
// object that is not there.
func liftBucketFile(t *testing.T, dir, key string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

// liftRecordFile is the repository's migration record in the rig's bucket,
// which is D3305's whole state.
func liftRecordFile(t *testing.T, dir, slug string) (string, bool) {
	t.Helper()
	return liftBucketFile(t, dir, lift.RecordPath(slug))
}

// localLayerText reads the local layer file, or answers false for a file that
// is not there.
func localLayerText(t *testing.T) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(localLayerPath(t))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

// noTerminalHere undoes a fakeTerminal for the rest of one test: the run's own
// confirmations then have nobody to answer them.
func noTerminalHere(t *testing.T) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return false }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// liftJSON is a value's JSON spelling, for reading a node as the value it is
// rather than as the bytes it happens to be written with.
func liftJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// before is the one ordering assertion this file needs: want must be on screen
// before than is.
func liftBefore(t *testing.T, all, want, than string) {
	t.Helper()
	i, j := strings.Index(all, want), strings.Index(all, than)
	switch {
	case i < 0:
		t.Errorf("%q is not in the run's output:\n%s", want, all)
	case j < 0:
		t.Errorf("%q is not in the run's output:\n%s", than, all)
	case i > j:
		t.Errorf("%q is printed after %q:\n%s", want, than, all)
	}
}

// liftedModel is the value the fixture planners lift, and liftSaid is the line
// one of them prints about the repository whose runs change.
const (
	liftedModel = "startup"
	liftSaid    = "lift: acme/one ran on its own model and now runs on the project's"
)

// liftOneChange is the ordinary change: one line, one scalar edit into the
// layer file, one recipe into the store, one record.
func liftOneChange(t *testing.T) lift.Change {
	t.Helper()
	return lift.Change{
		Lines: []string{liftSaid},
		LayerEdits: []lift.LayerEdit{{
			Path:  []string{"defaults", "rigor", "default"},
			Value: liftedModel,
		}},
		StoreWrites: []lift.StoreWrite{{
			Key:  recipe.ObjectKey("standard"),
			Data: []byte("version: 1\nname: standard\n"),
		}},
		Records: []string{liftSlug(t, "acme/one")},
	}
}

// TestLiftStageSaysFirst is the plan's red step: before this stage exists
// nothing prints what a repository's own file would move, and the update
// publishes a layer without it.
//
// What it pins is the order the stage is named for — every line of every change
// on screen before the first write — and the gates the writes pass: each of the
// three is behind the confirmation every other stage's write goes through, so
// a session that can answer none of the three is refused rather than asked
// (T7: nothing is typed, and no step of the update waits on a question).
func TestLiftStageSaysFirst(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	f := newLiftFixture(t, []lift.Repo{{Repo: "acme/one", Provider: "github"}})
	f.plan(lift.PlannerModels, func(lift.Project, []lift.Repo) (lift.Change, error) {
		return liftOneChange(t), nil
	})
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update")
	all := out + errOut
	if err != nil {
		t.Fatalf("fugaro update did not migrate: %v\n%s", err, all)
	}
	if f.calls[lift.PlannerModels] == 0 {
		t.Fatal("the stage ran no planner")
	}
	// Said once, though the loop reads this stage twice (the preview's pass and
	// the execution pass): the plan is one read and the lines are printed from
	// it.
	if got := strings.Count(all, liftSaid); got != 1 {
		t.Errorf("the change's line is printed %d times, want once:\n%s", got, all)
	}
	// Every gate, one per write, in the words the operator reads above them.
	for _, what := range []string{liftLayerWhat, liftStoreWhat} {
		if !strings.Contains(all, what) {
			t.Errorf("the write is not confirmed in the gate's own words (%q):\n%s", what, all)
		}
	}
	// And the saying comes first: the line is above each write's confirmation,
	// and above the sentence the write itself prints.
	liftBefore(t, all, liftSaid, liftLayerWhat)
	liftBefore(t, all, liftSaid, "the lifted values")
	liftBefore(t, all, liftSaid, "published the lifted recipe")

	text, ok := localLayerText(t)
	if !ok || !strings.Contains(text, liftedModel) {
		t.Errorf("the lifted value is not in the local layer file:\n%s", text)
	}
	data, ok := liftBucketFile(t, r.runs, recipe.ObjectKey("standard"))
	if !ok || !strings.Contains(data, "name: standard") {
		t.Errorf("the lifted recipe is not in the project's store: %q", data)
	}

	// The same run with nobody at the terminal: the migration is THIS run's own
	// write, so a session that cannot answer the gate is refused rather than
	// asked — and nothing of the migration is written.
	t.Run("a session that cannot answer is refused", func(t *testing.T) {
		r := newLayerRig(t, testProjectLayer)
		f := newLiftFixture(t, []lift.Repo{{Repo: "acme/one", Provider: "github"}})
		f.plan(lift.PlannerModels, func(lift.Project, []lift.Repo) (lift.Change, error) {
			return liftOneChange(t), nil
		})
		noAgentSession(t)
		noTerminalHere(t)
		out, errOut, err := executeStdin(t, "", "update")
		if err == nil {
			t.Fatalf("the run migrated with nobody to confirm it:\n%s%s", out, errOut)
		}
		if got := out + errOut; !strings.Contains(got, "the confirmation of this step") {
			t.Errorf("the refusal does not name the confirmation:\n%s", got)
		}
		if _, ok := localLayerText(t); ok {
			t.Error("the layer file was written with nobody to confirm the write")
		}
		if _, ok := liftRecordFile(t, r.runs, liftSlug(t, "acme/one")); ok {
			t.Error("a repository was recorded as migrated although nothing was written")
		}
	})
}

// TestLiftStageRecordsOnlyAfterThePublish: a record claims that a repository's
// migration has landed, and what lands the layer half of it is the layer
// stage's publish of the object every repository of the project resolves
// against. The record is written after the loop for that reason, and a run
// whose publish did not land writes none: the next update migrates the
// repository again.
func TestLiftStageRecordsOnlyAfterThePublish(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	f := newLiftFixture(t, []lift.Repo{{Repo: "acme/one", Provider: "github"}})
	f.plan(lift.PlannerModels, func(lift.Project, []lift.Repo) (lift.Change, error) {
		return liftOneChange(t), nil
	})
	fakeTerminal(t)
	slug := liftSlug(t, "acme/one")

	out, errOut, err := executeStdin(t, "", "update")
	all := out + errOut
	if err != nil {
		t.Fatalf("fugaro update did not migrate: %v\n%s", err, all)
	}
	record, ok := liftRecordFile(t, r.runs, slug)
	if !ok {
		t.Fatalf("the migration landed and no record was written:\n%s", all)
	}
	if !strings.Contains(record, "at") {
		t.Errorf("the record does not say when the repository was migrated: %s", record)
	}
	// It is written after the publish, and the run says so in that order.
	liftBefore(t, all, "published the project layer", "recorded the migration of")
	published, ok := liftBucketFile(t, r.runs, config.LayerKey)
	if !ok || !strings.Contains(published, liftedModel) {
		t.Errorf("the published project layer does not carry the lifted value:\n%s", published)
	}

	// The other direction: the layer stage runs, does NOT publish (this
	// machine's file was written from a generation the bucket no longer holds,
	// so D854's remedy is the re-seed), and the run records nothing — the
	// migration is not in the object every repository resolves against.
	t.Run("a publish that did not land records nothing", func(t *testing.T) {
		r := newLayerRig(t, testProjectLayer)
		// A local file that differs from the published object, with a recorded
		// generation the bucket does not answer: the layer stage's own D854
		// state, whose remedy is the re-seed and not the publish.
		noTerminalHere(t)
		recordLayer(t, testProjectLayer+"jobs:\n  reports: { paused: false }\n", "1")
		f := newLiftFixture(t, []lift.Repo{{Repo: "acme/one", Provider: "github"}})
		f.plan(lift.PlannerModels, func(lift.Project, []lift.Repo) (lift.Change, error) {
			return liftOneChange(t), nil
		})
		fakeTerminal(t)
		out, errOut, _ := executeStdin(t, "", "update")
		all := out + errOut
		if !strings.Contains(all, liftSaid) {
			t.Fatalf("the stage did not plan the migration at all:\n%s", all)
		}
		if _, ok := liftRecordFile(t, r.runs, slug); ok {
			t.Errorf("a repository was recorded as migrated although the project layer was never published:\n%s", all)
		}
		if strings.Contains(all, "published the project layer") {
			t.Fatalf("this run published the project layer after all:\n%s", all)
		}
	})
}

// TestLiftStageMigratesOnce: the migration happens once per repository (D3305).
// The stage reads which repositories already have a record and hands that to
// every planner, so the second run moves nothing; what a planner still prints
// on such a repository is the name of a value it did NOT move — the one a
// later release's rule drops — and the run writes nothing.
func TestLiftStageMigratesOnce(t *testing.T) {
	newLayerRig(t, testProjectLayer)
	slug := liftSlug(t, "acme/one")
	const later = "lift: acme/one sets max_budget_usd, which a later release's rule drops"
	f := newLiftFixture(t, []lift.Repo{{Repo: "acme/one", Provider: "github"}})
	f.plan(lift.PlannerModels, func(proj lift.Project, _ []lift.Repo) (lift.Change, error) {
		if proj.Migrated[slug] {
			return lift.Change{Lines: []string{later}}, nil
		}
		return liftOneChange(t), nil
	})
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("the first update did not migrate: %v\n%s%s", err, out, errOut)
	}
	path := localLayerPath(t)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the first run wrote no layer file: %v", err)
	}

	// The second run: the record is in the bucket, so the planner is told the
	// repository is migrated and moves nothing.
	out, errOut, err = executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("the second update failed: %v\n%s%s", err, out, errOut)
	}
	all := out + errOut
	if len(f.migrated) < 2 || !f.migrated[1][slug] {
		t.Errorf("the second run's planners were not told acme/one is migrated: %v", f.migrated)
	}
	if !strings.Contains(all, later) {
		t.Errorf("the second run does not print the name of the value it did not move:\n%s", all)
	}
	if strings.Contains(all, liftSaid) {
		t.Errorf("the second run planned the migration again:\n%s", all)
	}
	if strings.Contains(all, "the lifted values") || strings.Contains(all, "published the lifted recipe") {
		t.Errorf("the second run wrote something:\n%s", all)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("the second run changed the layer file:\n%s\nwant\n%s", second, first)
	}
}

// TestLiftStageNamesUnlistedPatterns: D3306's real list is the local config's
// repositories plus the concrete names the published admits carry, and the two
// things it cannot reach are named rather than dropped in silence: an admit
// pattern it cannot expand, and a repository it could not read — whose lifted
// values are then dropped and never moved, so what stands for it is the
// project's values alone.
func TestLiftStageNamesUnlistedPatterns(t *testing.T) {
	newLayerRig(t, testProjectLayer)
	f := newLiftFixture(t,
		[]lift.Repo{
			{Repo: "acme/one", Provider: "github"},
			{Repo: "acme/two", Provider: "github", Unreadable: "the github provider refused the read (403)"},
		},
		"acme/*",
	)
	f.plan(lift.PlannerModels, func(lift.Project, []lift.Repo) (lift.Change, error) {
		return liftOneChange(t), nil
	})
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update")
	all := out + errOut
	if err != nil {
		t.Fatalf("fugaro update failed: %v\n%s", err, all)
	}
	if !strings.Contains(all, "acme/*") || !strings.Contains(all, "pattern") {
		t.Errorf("the admit pattern that could not be expanded is not named:\n%s", all)
	}
	if !strings.Contains(all, "acme/two") || !strings.Contains(all, "403") {
		t.Errorf("the repository that could not be read is not named, with why:\n%s", all)
	}
	for _, line := range strings.Split(all, "\n") {
		if !strings.Contains(line, "acme/two") && !strings.Contains(line, "acme/*") {
			continue
		}
		if !strings.Contains(line, "dropped") {
			t.Errorf("the line does not say the repository's own values are dropped: %s", line)
		}
	}
	// Named before any write, like every other line of the stage.
	liftBefore(t, all, "acme/two", "the lifted values")
	liftBefore(t, all, "acme/*", "the lifted values")
}

// TestLiftStagePlanOnlyWritesNothing: --plan-only runs the planners, prints the
// same lines and writes nothing — no layer file, no object in the store, no
// record. The lines are the whole of what a plan-only run is for, and a
// migration is exactly the kind of write a person reads before it happens.
func TestLiftStagePlanOnlyWritesNothing(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	f := newLiftFixture(t, []lift.Repo{{Repo: "acme/one", Provider: "github"}})
	f.plan(lift.PlannerModels, func(lift.Project, []lift.Repo) (lift.Change, error) {
		return liftOneChange(t), nil
	})
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update", "--plan-only")
	all := out + errOut
	if err != nil {
		t.Fatalf("fugaro update --plan-only failed: %v\n%s", err, all)
	}
	if f.calls[lift.PlannerModels] == 0 {
		t.Fatal("--plan-only ran no planner: the lines a person reads come from them")
	}
	if !strings.Contains(all, liftSaid) {
		t.Errorf("--plan-only does not print the change's line:\n%s", all)
	}
	if _, ok := localLayerText(t); ok {
		t.Error("--plan-only wrote a local layer file")
	}
	if _, ok := liftBucketFile(t, r.runs, recipe.ObjectKey("standard")); ok {
		t.Error("--plan-only wrote the recipe into the project's store")
	}
	if _, ok := liftRecordFile(t, r.runs, liftSlug(t, "acme/one")); ok {
		t.Error("--plan-only recorded a repository as migrated")
	}
	published, ok := liftBucketFile(t, r.runs, config.LayerKey)
	if !ok || published != testProjectLayer {
		t.Errorf("--plan-only changed the published project layer:\n%s", published)
	}
}

// TestLiftStageLowersTheCapBehindTheGate: the third family is the budget
// database's nodes — a repository cap standing above the project's ceiling is
// lowered in place (B18) — and it passes the same confirmation as the other
// two. The node keeps its other figure and its name: what the migration writes
// is the two figures this release lifts and nothing else of the node.
func TestLiftStageLowersTheCapBehindTheGate(t *testing.T) {
	noAgentSession(t)
	noRunSession(t)
	r := newLayerRig(t, testProjectLayer)
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	r.save(t)
	db.Set(budget.PathMark, auroraMark())
	db.Set(budget.PathProject, initProjectName)
	slug := liftSlug(t, "acme/one")
	over := budget.RepoCaps{DailyMicros: microsPtr(200_000), PerRunMicros: microsPtr(50_000), Repo: "acme/one"}
	db.Set(budget.PathCapsRepo(slug), over)
	lowered := budget.RepoCaps{DailyMicros: microsPtr(200_000), PerRunMicros: microsPtr(3_000), Repo: "acme/one"}
	loweredJSON := liftJSON(t, lowered)

	f := newLiftFixture(t, []lift.Repo{{Repo: "acme/one", Provider: "github"}})
	f.plan(lift.PlannerCaps, func(lift.Project, []lift.Repo) (lift.Change, error) {
		return lift.Change{
			Lines:    []string{"lift: acme/one's per-run cap is above the project's ceiling and is lowered to it"},
			DBWrites: []lift.DBWrite{{Path: budget.PathCapsRepo(slug), Value: lowered}},
			Records:  []string{slug},
		}, nil
	})
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update")
	all := out + errOut
	if err != nil {
		t.Fatalf("fugaro update failed: %v\n%s", err, all)
	}
	if !strings.Contains(all, liftDBWhat) {
		t.Errorf("the cap write is not confirmed in the gate's own words:\n%s", all)
	}
	got := db.Value(budget.PathCapsRepo(slug))
	if !sameNode(liftJSON(t, got), loweredJSON) {
		t.Errorf("the cap node is %v, want the lowered cap %s", got, loweredJSON)
	}

	// And --plan-only leaves it standing: the whole migration writes nothing,
	// the database included.
	db.Set(budget.PathCapsRepo(slug), over)
	out, errOut, err = executeStdin(t, "", "update", "--plan-only")
	if err != nil {
		t.Fatalf("fugaro update --plan-only failed: %v\n%s%s", err, out, errOut)
	}
	if !sameNode(liftJSON(t, db.Value(budget.PathCapsRepo(slug))), liftJSON(t, over)) {
		t.Errorf("--plan-only lowered the cap: %v", db.Value(budget.PathCapsRepo(slug)))
	}
}

// TestUpdateLiftsEveryKey runs the registered planners, not fixtures: an
// installation whose repositories write lifted keys converges in one update,
// with the models and agent.auth in the layer file and a record per repository,
// and a second update changes nothing.
func TestUpdateLiftsEveryKey(t *testing.T) {
	// Block style: yamledit cannot add a key inside a flow map (defaults.agent: { auth: x }).
	r := newLayerRig(t, strings.Replace(testProjectLayer, "  agent: { auth: api-key }\n", "", 1))
	old := liftRepositories
	liftRepositories = func(context.Context, *initEngine, *localcfg.Config, []string) ([]lift.Repo, []string, error) {
		file := []byte("agent:\n  auth: cascade\n  models:\n    background: claude-haiku-4-5\n")
		return []lift.Repo{
			{Repo: "acme/one", Provider: "github", Config: file},
			{Repo: "acme/two", Provider: "github", Config: file},
		}, nil, nil
	}
	t.Cleanup(func() { liftRepositories = old })
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("fugaro update did not migrate: %v\n%s%s", err, out, errOut)
	}
	if all := out + errOut; !strings.Contains(all, "project: agent.auth -> cascade") {
		t.Errorf("update says nothing about agent.auth:\n%s", all)
	}
	text, _ := localLayerText(t)
	for _, want := range []string{"claude-haiku-4-5", "auth: cascade"} {
		if !strings.Contains(text, want) {
			t.Errorf("the layer file lacks %q:\n%s", want, text)
		}
	}
	for _, repo := range []string{"acme/one", "acme/two"} {
		if _, ok := liftRecordFile(t, r.runs, liftSlug(t, repo)); !ok {
			t.Errorf("%s is not recorded as migrated", repo)
		}
	}

	if _, _, err = executeStdin(t, "", "update"); err != nil {
		t.Fatalf("the second update failed: %v", err)
	}
	if again, _ := localLayerText(t); again != text {
		t.Errorf("the second update changed the layer file:\n%s", again)
	}
}
