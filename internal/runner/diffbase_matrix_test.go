package runner_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The safety net #840 left: the review stage and the diff the run's own
// writeback and readiness are read from must be measured against the SAME
// base — the run's base of work, never the configuration's base branch when
// they differ. Each of the ways a run has a base is a cell below, and every
// cell asks the same question of every diff the run takes: the review
// prompt's scope line, the input the runner builds for the reviewer, what
// the prefetch fetches, and the run's own work as the readiness guard reads
// it.
//
//	(a) a fresh run on main;
//	(b) `fugaro run --ref integration-0.9.6`, whose pull request still
//	    targets the configured base;
//	(c) `fugaro follow-up --pr N` on a pull request retargeted at the
//	    integration branch;
//	(d) a run whose base of work moved on while it worked, and whose branch
//	    merged that move in: the base's own commit is not this run's work,
//	    and the review and the guard must agree about that;
//	(e) a run cut from another ref whose *configuration's* base moves on,
//	    and whose branch merges that in: the guard must leave out both
//	    bases' own commits, or it refuses this run for a workflow file main
//	    added (the mirror of (d) — with only the diff base subtracted, this
//	    cell is the one that fails).
//
// Every cell names the base it must use (want) and a base a wrong answer
// would name instead (stale): a diff measured against the stale base shows
// up as `origin/<stale>` in a prompt or as a path the stale base owns.

// advanceBranch pushes one commit onto branch on the remote, cut from the
// tip the branch already has: the base a run is measured against moving on
// while the run works. addBaseBranch (diffbase_test.go) cuts a new branch
// from main instead.
func advanceBranch(t *testing.T, remote, branch string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "advance")
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", remote, dir)
	testutil.Git(t, dir, "checkout", "--quiet", branch)
	testutil.WriteFiles(t, dir, files)
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "a person's commit on "+branch)
	testutil.Git(t, dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return testutil.Git(t, dir, "rev-parse", "HEAD")
}

// baseRun is one cell's run, as the matrix reads it: what the review stage
// was told, what the writeback recorded of the run's own work, what the
// prefetch fetched, and how the run ended.
type baseRun struct {
	rec      *runstore.Record
	err      error
	prompt   string         // the review stage's prompt
	impl     string         // the follow-up's implement prompt, "" when not a follow-up
	fetched  map[string]int // the base of each prefetch fetch, counted
	provider *fake.Provider // the run's provider, for the pull request it opened
}

// ownFiles is the paths the writeback recorded, sorted by the runner itself.
func (b baseRun) ownFiles() []string {
	if b.rec == nil || b.rec.ChangedFiles == nil {
		return nil
	}
	return b.rec.ChangedFiles.Paths
}

// baseCell is one cell of the matrix: the run itself, the base every diff
// must be measured against, and the paths that tell a right answer from a
// wrong one.
type baseCell struct {
	name string
	// want is the base of work every diff must name; stale is the base a
	// wrong answer names instead. Neither may be empty.
	want, stale string
	// recordHolds are the paths the run record must hold, and recordOmits
	// paths it must not. The record is read from the commit the run was cut
	// from (workBase, refused.go) by design, so where a base moved on and the
	// run merged it in, that base's own file IS in it: such a path is listed
	// in recordHolds, and both cells of this shape say so. What neither list
	// may hold is the stale base's own files — a record measured against the
	// wrong branch blames the run for them (cells (a) to (e) all catch that).
	recordHolds, recordOmits []string
	// wantPRBase is the base the run's own pull request targets, when the
	// cell opens one: the configuration's base, which is a different
	// question from the base of the diff and must not move with it.
	wantPRBase string
	// fetches is how many prefetch fetches the cell must make. A first run
	// prefetches nothing (prefetch.go returns early without a clone job or a
	// follow-up/review run), so only cell (c) is above zero, and it is the
	// cell that proves the prefetch fetches the base of the diff.
	fetches int
	// run stages the cell's run and returns what the matrix reads.
	run func(t *testing.T) baseRun
}

// The cells. Order is the order of the report's own list.
var baseCells = []baseCell{
	{
		name:        "(a) a fresh run on main",
		want:        "main",
		stale:       integrationBranch,
		recordHolds: []string{"feature.txt"},
		recordOmits: []string{"person.txt", "integration.txt"},
		wantPRBase:  "main",
		run: func(t *testing.T) baseRun {
			h := newHarness(t, "", nil)
			// A person's commit is on main before the run is cut from it,
			// and another branch exists that the run must not use: the
			// record of a run measured against either would name their
			// files.
			commitOnBranch(t, h.remote, "main", map[string]string{"person.txt": "a person's file\n"})
			addBaseBranch(t, h, integrationBranch)
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			return baseRun{rec: rec, err: err, prompt: reviewPromptOf(t, h.agent.calls), provider: h.provider}
		},
	},
	{
		name:        "(b) a run cut from another ref",
		want:        integrationBranch,
		stale:       "main",
		recordHolds: []string{"feature.txt"},
		recordOmits: []string{"integration.txt"},
		wantPRBase:  "main",
		run: func(t *testing.T) baseRun {
			spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: integrationBranch, Workflow: "app", Task: "Add a feature"}
			h := newHarness(t, "", spec)
			addBaseBranch(t, h, integrationBranch)
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			return baseRun{rec: rec, err: err, prompt: reviewPromptOf(t, h.agent.calls), provider: h.provider}
		},
	},
	{
		name:        "(c) a follow-up whose pull request targets the integration branch",
		want:        integrationBranch,
		stale:       "main",
		recordHolds: []string{"more.txt"},
		recordOmits: []string{"integration.txt", "feature.txt"},
		fetches:     3,
		run: func(t *testing.T) baseRun {
			// cloneYAML and cloneJob: a clone job is the one that prefetches,
			// and the fetch is the half of this cell that proves the objects
			// the reviewer's own git reads are the ones its prompt names
			// (TestFollowUpPrefetchUsesThePRsCurrentBase is the same rig).
			h := followUpHarness(t, cloneYAML, func(h *harness) {
				addBaseBranch(t, h, integrationBranch)
				h.cloneJob(t)
			})
			h.editState(t, func(st *fake.State) { st.PRs[0].Spec.Base = integrationBranch })
			h.followUp(t, followID, runID, "Please continue.")
			var mu sync.Mutex
			fetched := map[string]int{}
			runner.SetPrefetchFetch(t, func(_ *gitops.Repo, _ context.Context, _, base, _ string) error {
				mu.Lock()
				fetched[base]++
				mu.Unlock()
				return nil
			})
			rec, err := h.run(t, implement("more"), review("ship", 0))
			mu.Lock()
			defer mu.Unlock()
			return baseRun{rec: rec, err: err, prompt: reviewPromptOf(t, h.agent.calls),
				impl: followUpPromptOf(t, h.agent.calls), fetched: fetched, provider: h.provider}
		},
	},
	{
		name:  "(d) a run whose base of work moved on while it worked",
		want:  integrationBranch,
		stale: "main",
		// The advance commit is the base of work's own, and the record is
		// read from the commit the run was cut from, so its workflow file is
		// in the record — the design's own choice (workBase), not a wrong
		// base. What must not be is main's.
		recordHolds: []string{"feature.txt", ".github/workflows/integration.yml"},
		recordOmits: []string{"integration.txt"},
		run: func(t *testing.T) baseRun {
			spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: integrationBranch, Workflow: "app", Task: "Add a feature"}
			h := newHarness(t, "", spec)
			addBaseBranch(t, h, integrationBranch)
			mergeTheMovedBase := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				// The base branch moves on while the run works — a person's
				// commit brings a workflow file, which only the base's own
				// commits may carry — and the run merges it, as one told to
				// keep up with its base does.
				advanceBranch(t, h.remote, integrationBranch, map[string]string{
					".github/workflows/integration.yml": "on: push\n",
				})
				shell(t, req, "git fetch -q origin "+integrationBranch+" && git merge -q --no-edit origin/"+integrationBranch)
				return implement("feature")(t, ctx, req)
			}
			rec, err := h.run(t, mergeTheMovedBase, review("ship", 0))
			return baseRun{rec: rec, err: err, prompt: reviewPromptOf(t, h.agent.calls), provider: h.provider}
		},
	},
	{
		name:  "(e) a run cut from another ref whose configured base moved on",
		want:  integrationBranch,
		stale: "main",
		// Same as (d), on the other base: main's own workflow file is in the
		// record (the run merged main in), and the guard is what must not
		// blame the run for it.
		recordHolds: []string{"feature.txt", ".github/workflows/person.yml"},
		recordOmits: []string{"integration.txt"},
		wantPRBase:  "main",
		run: func(t *testing.T) baseRun {
			spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: integrationBranch, Workflow: "app", Task: "Add a feature"}
			h := newHarness(t, "", spec)
			addBaseBranch(t, h, integrationBranch)
			mergeTheMovedBase := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				// The configuration's base branch — not the run's own base of
				// work, which is the integration branch — moves on with a
				// workflow file, and the run merges it in. Its commit is
				// main's: subtracting only the base of the diff leaves it in
				// the range the guard reads, and the run is refused for it.
				advanceBranch(t, h.remote, "main", map[string]string{
					".github/workflows/person.yml": "on: push\n",
				})
				shell(t, req, "git fetch -q origin main && git merge -q --no-edit origin/main")
				return implement("feature")(t, ctx, req)
			}
			rec, err := h.run(t, mergeTheMovedBase, review("ship", 0))
			return baseRun{rec: rec, err: err, prompt: reviewPromptOf(t, h.agent.calls), provider: h.provider}
		},
	},
}

// TestTheReviewStageAndTheWritebackUseTheSameBase is the matrix: every cell
// runs a whole run and then asks the same questions of it.
//
// Mutation (make any of them answer with another base): each check below
// fails for the cell it belongs to — the review prompt names the
// configuration's base (r.cfg.Git.BaseBranch in place of diffBase), or the
// writeback's own record does, or the readiness guard's exclusion bound does
// (refused.go's guardBounds reading cfg.Git.BaseBranch rather than the base
// the review diffs against — cell (d)), or it reads only one of the two
// bases the run works from (refused.go's guardBounds with a single bound —
// cell (e)).
func TestTheReviewStageAndTheWritebackUseTheSameBase(t *testing.T) {
	for _, c := range baseCells {
		t.Run(c.name, func(t *testing.T) {
			got := c.run(t)
			// The run reached its writeback: a push that was refused (the
			// guard blaming the run for the base's own commit) ends it as
			// infra_error, and a stage that failed ends it not ready.
			if got.err != nil || got.rec == nil {
				t.Fatalf("the run did not finish: rec = %+v, err = %v", got.rec, got.err)
			}
			if got.rec.Status != runstore.StatusSucceeded || got.rec.Outcome != runstore.OutcomeReady {
				t.Fatalf("the run is not ready: rec = %+v, err = %v (reason %q)", got.rec, got.err, got.rec.Reason)
			}
			if got.rec.PushedHead == "" || got.rec.PushedHead != got.rec.HeadSHA {
				t.Fatalf("the run's work was not pushed: %+v", got.rec)
			}

			// 1. The review stage's scope line names the run's base of work,
			//    and the reviewer is never handed the other base.
			if !strings.Contains(got.prompt, "Review the changes on this branch against origin/"+c.want) {
				t.Fatalf("the review prompt does not name origin/%s:\n%s", c.want, got.prompt)
			}
			if strings.Contains(got.prompt, "origin/"+c.stale) {
				t.Fatalf("the review prompt names the wrong base origin/%s:\n%s", c.stale, got.prompt)
			}
			// 2. The input the runner built for the reviewer — the diff it
			//    was handed — is the same range as the scope line.
			if !strings.Contains(got.prompt, "git diff origin/"+c.want+"...HEAD:") {
				t.Fatalf("the review input is not the diff against origin/%s:\n%s", c.want, got.prompt)
			}

			// 3. The writeback's record of the run's work holds what it must
			//    and not what the stale base owns. It is measured from the
			//    commit the run was cut from (workBase, refused.go) rather
			//    than from a base, which is deliberate — and that is why a
			//    base the run merged in while it worked is in it, in the two
			//    cells whose base moved (their recordHolds say so). What no
			//    cell may hold is the stale base's own files: a record
			//    measured against the wrong branch blames the run for them.
			record := got.ownFiles()
			for _, want := range c.recordHolds {
				if !contains(record, want) {
					t.Errorf("the writeback recorded %v, want %s among them", record, want)
				}
			}
			for _, absent := range c.recordOmits {
				if contains(record, absent) {
					t.Errorf("the writeback recorded %v and blames the run for %s, which another branch owns", record, absent)
				}
			}

			// 4. A pull request still targets the configuration's base: the
			//    base of the diff is another question, and this change must
			//    not have moved the one Fugaro opens against.
			if c.wantPRBase != "" {
				if pr := onlyPR(t, got.provider); pr.Spec.Base != c.wantPRBase {
					t.Errorf("the pull request targets %q, want the configured base %s", pr.Spec.Base, c.wantPRBase)
				}
			}
			// 5. Where the run prefetches at all, it fetches the base of the
			//    diff and nothing else, the expected number of times: the
			//    objects the reviewer's own git reads have to be the ones its
			//    prompt names. A first run prefetches nothing (fetches is 0),
			//    so this check is carried by cell (c) — and the count is
			//    asserted, so a seam that is never wired fails the cell rather
			//    than passing it vacuously.
			total := 0
			for base, n := range got.fetched {
				total += n
				if base != "origin/"+c.want {
					t.Errorf("the prefetch fetched %s %d time(s), want only origin/%s", base, n, c.want)
				}
			}
			if total != c.fetches {
				t.Errorf("the prefetch fetched %d time(s) (%v), want %d against origin/%s", total, got.fetched, c.fetches, c.want)
			}
			// 6. A follow-up's own prompt reads the same range: the base it
			//    says the pull request targets, and the stat below it.
			if got.impl != "" {
				if !strings.Contains(got.impl, "which targets "+c.want) {
					t.Errorf("the follow-up prompt names another base:\n%s", got.impl)
				}
				if !strings.Contains(got.impl, "git diff --stat origin/"+c.want+"...HEAD") {
					t.Errorf("the follow-up's stat is read against another base:\n%s", got.impl)
				}
			}
		})
	}
}

func contains(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
