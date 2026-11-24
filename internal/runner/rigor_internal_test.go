package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// rigorRun builds the least a run needs to resolve its rigor: a real checkout
// cloned from a real remote (whose main is the base branch), a config naming
// that base, and a record to write into.
//
// cfg is the configuration the run resolved from the *checked-out ref* — for a
// first run that is the branch under review's own fugaro.yaml, which is
// exactly what the mode must not come from (D25) — and spec carries the
// project layer the task brought, if any.
func rigorRun(t *testing.T, remoteFiles map[string]string, cfg *config.Config, spec *task.Spec) (*run, *gitops.Repo) {
	t.Helper()
	testutil.IsolateGit(t)
	r, repo := rigorRunOn(t, testutil.NewRemote(t, remoteFiles), cfg, spec)
	r.cfg.Git.BaseBranch = "main"
	return r, repo
}

// rigorRunOn is rigorRun on a remote the test built itself, for the tests that
// need a branch beside the default one.
func rigorRunOn(t *testing.T, remote string, cfg *config.Config, spec *task.Spec) (*run, *gitops.Repo) {
	t.Helper()
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		cfg = &config.Config{}
	}
	if spec == nil {
		spec = &task.Spec{}
	}
	// A project, because that is what a job has and what these tests are about:
	// with none, checkProject and resolvePolicy skip the default-branch read and
	// so does the mode's (readBaseRigor), which is the local run's path. A test
	// that wants that path clears r.d.Project.
	r := &run{
		repo: repo, cfg: cfg, spec: spec, rec: &runstore.Record{},
		d: Deps{Log: slog.New(slog.DiscardHandler), Project: "aurora"},
	}
	return r, repo
}

// workflowsYAML is the one required block a fugaro.yaml must carry to resolve
// at all: its absence would make every base file in these tests unreadable, and
// the tests would pass for the wrong reason.
const workflowsYAML = "workflows:\n  server: { commands: { build: make, test: make test } }\n"

// rigorLayer is a project layer whose defaults.rigor sets Critical: the value
// a run falls back to when no branch can be read.
func rigorLayer(t *testing.T, mode rigor.Rigor) *config.ProjectLayer {
	t.Helper()
	return &config.ProjectLayer{Defaults: config.LayerDefaults{Rigor: config.LayerRigor{Default: mode}}}
}

// openRound is the review round the section cases below leave open: the last
// senior review, a `changes` verdict, three findings, which are the ones the
// mode's own table splits.
var openRound = runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 3}

// modeBase is a repository whose base branch pins one mode and declares no
// area rule, so the run's own paths match nothing and the mode it resolves at
// finalize is exactly the one the file names (D22's third moment reads the
// run's diff, and a matching rule would outrank the default). An empty block
// is a repository that configures no rigor at all, which is D19's case.
func modeBase(t *testing.T, block string) string {
	t.Helper()
	testutil.IsolateGit(t)
	const head = "version: 1\nproject: aurora\ngit: { provider: github }\n"
	const tail = "workflows:\n  server: { commands: { build: make, test: make test } }\n"
	return testutil.NewRemote(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": head + block + tail})
}

// modeRun is one run at finalize, cloned from a remote of its own whose base
// pins mode, and resolved from its own work. It returns the run and the path
// of the remote it pushes to.
func modeRun(t *testing.T, mode string, reviews ...runstore.ReviewSummary) (*run, string) {
	t.Helper()
	block := ""
	if mode != "" {
		block = "rigor: { default: " + mode + " }\n"
	}
	remote := modeBase(t, block)
	return runAtFinalize(t, remote, "fugaro/x", "20261024-120000-abcd", reviews...), remote
}

// runAtFinalize is a run at finalize in a checkout cloned from remote, on
// branch, with the run's own work committed and the mode resolved from it: a
// run whose record carries the reviews given, which is what the status
// section under test reads. runID is the run's own id, which the work is
// named after so that the diff the mode is resolved from is this run's.
func runAtFinalize(t *testing.T, remote, branch, runID string, reviews ...runstore.ReviewSummary) *run {
	t.Helper()
	testutil.IsolateGit(t)
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.CheckoutNewBranch(ctx, "main", branch); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{filepath.Join("internal/payments", runID+".go"): "package payments\n"})
	if _, err := repo.CommitAll(ctx, "the run's work"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Git.BaseBranch = "main"
	r := &run{
		repo: repo, cfg: cfg, spec: &task.Spec{RequestedBy: "octocat"},
		rec: &runstore.Record{
			Version: 1, RunID: runID, Branch: branch, BaseBranch: "main",
			Status: runstore.StatusRunning, Outcome: runstore.OutcomeNone, Reviews: reviews,
		},
		d: Deps{Log: slog.New(slog.DiscardHandler)},
	}
	// The clock is injected rather than read, so the section a case pins
	// carries the same time on every machine.
	r.d.Now = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }
	r.bootstrapRigor(ctx)
	r.recordChangedFiles(ctx)
	r.finalizeRigor()
	return r
}

// TestBootstrapRigorReadsTheBaseBranchNotTheBranchUnderReview is D25, and it is
// the reason this field exists at all: a run that could take its mode from its
// own tree could set itself to POC in the same pull request it wants merged.
// The config here is the branch under review's — a first run's r.cfg is read
// from the checked-out ref, so it is what the branch asked for — and the mode
// must be the base branch's instead, named with the revision it was read from.
func TestBootstrapRigorReadsTheBaseBranchNotTheBranchUnderReview(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor:
  default: critical
  areas:
    - { name: payments, match: "internal/payments/**", level: critical }
    - { name: docs, match: "docs/**", level: poc }
workflows:
  server: { commands: { build: make, test: make test } }
`
	cfg := &config.Config{RepoRigor: &config.Rigor{Default: rigor.POC}}
	r, _ := rigorRun(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base}, cfg, nil)

	r.bootstrapRigor(context.Background())

	if r.rigorRev != "origin/main" {
		t.Errorf("the revision the mode was read from = %q", r.rigorRev)
	}
	if r.rigorCfg.Repo != rigor.Critical {
		t.Errorf("the base branch's default = %q, want critical", r.rigorCfg.Repo)
	}
	// Both of the base's area rules, and neither of the branch's: the
	// resolution at a later moment reads this configuration, not r.cfg's.
	if names := areaNamesOf(r.rigorCfg.RepoAreas); !reflect.DeepEqual(names, []string{"payments", "docs"}) {
		t.Errorf("the base branch's areas = %v", names)
	}
	rg := r.rec.Rigor
	if rg == nil {
		t.Fatal("bootstrap recorded no rigor block")
	}
	if rg.Mode != rigor.Critical || rg.From != rigor.FromRepo || rg.At != runstore.RigorAtBootstrap {
		t.Errorf("the block = %+v, want critical from the repository's default at bootstrap", rg)
	}
	if rg.Revision != "origin/main" {
		t.Errorf("the block names the revision it read: %+v", rg)
	}
	if len(rg.Areas) != 0 {
		t.Errorf("a run that declared no path matched an area: %+v", rg.Areas)
	}
}

// TestBootstrapRigorReadsTheDefaultBranchNotTheCheckoutsBase is D25 where it
// bites, and the reason a first run cannot read "origin/" + cfg.Git.BaseBranch:
// the checkout is cut from the ref the run will push, so that name is the
// branch under review's own to write. This branch names *itself* as its
// git.base_branch and asks for POC; the repository's default branch says
// critical, and the default branch is what the mode comes from — the same file
// the project check and the policy are read from, which no file in the checkout
// can choose. The test above hard-codes a base that agrees with the default
// branch, so it never sees this; this one disagrees on purpose.
func TestBootstrapRigorReadsTheDefaultBranchNotTheCheckoutsBase(t *testing.T) {
	remote := selfNamingBranchRemote(t)
	// r.cfg is the file in the checkout: the branch under review's own, naming
	// itself as its base, exactly as bootstrap resolves it for a first run.
	cfg := &config.Config{Git: config.Git{BaseBranch: "feature-x"}, RepoRigor: &config.Rigor{Default: rigor.POC}}
	r, repo := rigorRunOn(t, remote, cfg, nil)
	if err := repo.FetchBase(context.Background(), "feature-x"); err != nil {
		t.Fatal(err)
	}

	r.bootstrapRigor(context.Background())

	if r.rigorCfg.Repo != rigor.Critical {
		t.Errorf("the mode = %q, want the default branch's %q: a branch must not choose its own mode (D25)", r.rigorCfg.Repo, rigor.Critical)
	}
	if r.rigorRev != "origin/main" {
		t.Errorf("the revision the mode was read from = %q, want the repository's default branch", r.rigorRev)
	}
	if rg := r.rec.Rigor; rg == nil || rg.Mode != rigor.Critical || rg.From != rigor.FromRepo || rg.Revision != "origin/main" {
		t.Errorf("the block = %+v, want critical read from origin/main", rg)
	}
}

// TestBootstrapRigorLocalRunReadsTheCheckoutsBaseRef: a local run has no
// project, so checkProject and resolvePolicy both skip the default-branch read
// there, and the mode's read skips it the same way rather than reaching the
// network for a branch it does not need (readBaseRigor). Nothing is at stake
// the way it is in a job: the checkout is the developer's own branch, not one
// under review, so it keeps reading the ref its own config names.
func TestBootstrapRigorLocalRunReadsTheCheckoutsBaseRef(t *testing.T) {
	remote := selfNamingBranchRemote(t)
	cfg := &config.Config{Git: config.Git{BaseBranch: "feature-x"}, RepoRigor: &config.Rigor{Default: rigor.POC}}
	r, repo := rigorRunOn(t, remote, cfg, nil)
	r.d.Project = "" // FUGARO_PROJECT unset: a local run
	if err := repo.FetchBase(context.Background(), "feature-x"); err != nil {
		t.Fatal(err)
	}

	r.bootstrapRigor(context.Background())

	if r.rigorCfg.Repo != rigor.POC {
		t.Errorf("the mode = %q, want the ref the local config names", r.rigorCfg.Repo)
	}
	if r.rigorRev != "origin/feature-x" {
		t.Errorf("the revision = %q, want the ref the local config names", r.rigorRev)
	}
}

// selfNamingBranchRemote is a remote whose default branch main asks for
// critical and whose feature-x asks for poc *and* names itself as its
// git.base_branch: the shape a branch under review is in when it writes its own
// base, which is what makes "the base branch" and "the checkout" two different
// answers.
func selfNamingBranchRemote(t *testing.T) string {
	t.Helper()
	testutil.IsolateGit(t)
	base := `version: 1
project: aurora
git: { provider: github, base_branch: main }
rigor: { default: critical }
` + workflowsYAML
	own := strings.NewReplacer("base_branch: main", "base_branch: feature-x", "default: critical", "default: poc").Replace(base)
	if own == base {
		t.Fatal("the fixture does not carry the strings this test rewrites")
	}
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base})
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", remote, other)
	testutil.Git(t, other, "checkout", "--quiet", "-b", "feature-x")
	testutil.WriteFiles(t, other, map[string]string{"fugaro.yaml": own})
	testutil.Git(t, other, "commit", "--quiet", "-am", "the branch under review asks for poc")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/feature-x")
	return remote
}

// TestBootstrapRigorForAFollowUpReadsNoBranch: a follow-up and a review run
// have already resolved fugaro.yaml from origin/<ref> (readBaseConfig), so the
// configuration the run is running on *is* the base's and no second read is
// made. The remote has no fugaro.yaml at all, which a first run would have to
// fall back over: resolving the config's own value and naming origin/main is
// only possible on the path that read the base already.
func TestBootstrapRigorForAFollowUpReadsNoBranch(t *testing.T) {
	for _, kind := range []struct {
		name  string
		setup func(*run)
	}{
		{"a follow-up", func(r *run) { r.follow = &followState{} }},
		{"a review run", func(r *run) { r.review = true }},
	} {
		t.Run(kind.name, func(t *testing.T) {
			cfg := &config.Config{Layer: rigorLayer(t, rigor.Pro), RepoRigor: &config.Rigor{Default: rigor.Critical}}
			r, _ := rigorRun(t, map[string]string{"README.md": "hello\n"}, cfg, nil)
			kind.setup(r)

			r.bootstrapRigor(context.Background())

			if r.rigorRev != "origin/main" {
				t.Errorf("the revision = %q", r.rigorRev)
			}
			if !reflect.DeepEqual(r.rigorCfg, cfg.RigorConfig()) {
				t.Errorf("the configuration = %+v, want the run's own", r.rigorCfg)
			}
			rg := r.rec.Rigor
			if rg == nil || rg.Mode != rigor.Critical || rg.From != rigor.FromRepo || rg.Revision != "origin/main" {
				t.Errorf("the block = %+v", rg)
			}
		})
	}
}

// TestBootstrapRigorTakesTheRunsOwnRequest: the run's own request —
// `fugaro run --mode`, carried in the task (T1.6) — is the narrowest rank of
// the order (LD3), so it decides whether it asks for more or for less than the
// layers set, and the block names the request as the layer that did. Asking
// for the mode the layers already resolved leaves their own name standing,
// since that is the narrower reason for the same answer.
//
// Hotfix is the mode the request takes whole rather than by rank
// (rigor.Rigor.PerRun): the owner ruled on 2026-11-13 that `--mode hotfix`
// needs no opt-in (docs/rigor-command/implementation-plan.md R7), so it decides
// even from below every on-axis mode.
func TestBootstrapRigorTakesTheRunsOwnRequest(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor:
  default: startup
  areas:
    - { name: payments, match: "internal/payments/**", level: critical }
` + workflowsYAML
	for _, tc := range []struct {
		name string
		req  string
		mode rigor.Rigor
		from rigor.Layer
	}{
		{"a request above the repository's default", "critical", rigor.Critical, rigor.FromRequest},
		{"a request below it", "poc", rigor.POC, rigor.FromRequest},
		{"a request for the mode the layers set", "startup", rigor.Startup, rigor.FromRepo},
		{"hotfix, which the request takes whole", "hotfix", rigor.Hotfix, rigor.FromRequest},
		{"no request at all", "", rigor.Startup, rigor.FromRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := rigorRun(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base}, nil, &task.Spec{Rigor: rigor.Rigor(tc.req)})

			r.bootstrapRigor(context.Background())

			rg := r.rec.Rigor
			if rg == nil || rg.Mode != tc.mode || rg.From != tc.from {
				t.Fatalf("the block = %+v, want %q from %q", rg, tc.mode, tc.from)
			}
			if len(rg.Areas) != 0 {
				t.Errorf("a request that declares no path matched an area: %+v", rg.Areas)
			}
		})
	}
}

// TestACarriedModeNeverLowersTheSettings: a mode a launch carried from a
// record rather than one its caller typed on this run — the one a --pr
// follow-up keeps from its pull request, and the parent's own answer a task
// filed from a run carries (task.Spec.RigorCarried) — is raise-only against
// the layers (T5.2, LF5 §3a, D1844). A repository whose settings have risen
// since the parent ran therefore holds the run at its own level, and the mode
// kept beside the request stays empty, because a carried request never takes
// the run below the layers and so leaves no floor row behind it. The other
// half is a carried mode that does outrank them: it is the answer, and the
// record names the request as the layer that decided it.
//
// This is the launcher's rule too: internal/cli resolves the mode it prices,
// gates and asks the levels of through the same Request (Spec.RigorRequest),
// so a follow-up cannot be launched as one mode and run as another.
func TestACarriedModeNeverLowersTheSettings(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor: { default: critical }
` + workflowsYAML
	for _, tc := range []struct {
		name  string
		spec  *task.Spec
		files map[string]string
		cfg   *config.Config
		setup func(*run)
	}{
		{
			name:  "a task filed from a parent run",
			spec:  &task.Spec{Rigor: rigor.Startup, FollowUpOf: &task.FollowUpOf{Run: "20261024-120000-abcd", Keys: []string{"fix"}}},
			files: map[string]string{"README.md": "hello\n", "fugaro.yaml": base},
		},
		{
			name:  "a --pr follow-up",
			spec:  &task.Spec{Rigor: rigor.Startup, Branch: "fugaro/20261024-120000-abcd", PR: 7, PreviousRun: "20261024-120000-abcd"},
			files: map[string]string{"README.md": "hello\n"},
			cfg:   &config.Config{RepoRigor: &config.Rigor{Default: rigor.Critical}},
			setup: func(r *run) { r.follow = &followState{} },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := rigorRun(t, tc.files, tc.cfg, tc.spec)
			if tc.setup != nil {
				tc.setup(r)
			}

			r.bootstrapRigor(context.Background())

			rg := r.rec.Rigor
			if rg == nil || rg.LevelOf() != rigor.Critical || rg.From != rigor.FromRepo {
				t.Fatalf("the block = %+v; want critical from the repository: a carried startup may not lower the settings", rg)
			}
			if got := r.layersRigor; got != "" {
				t.Errorf("the mode kept beside the request = %q; want none: a carried request never goes below the layers", got)
			}
		})
	}

	raiseBase := `version: 1
project: aurora
git: { provider: github }
rigor: { default: startup }
` + workflowsYAML
	raised, _ := rigorRun(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": raiseBase},
		nil, &task.Spec{Rigor: rigor.Critical, FollowUpOf: &task.FollowUpOf{Run: "20261024-120000-abcd", Keys: []string{"fix"}}})

	raised.bootstrapRigor(context.Background())

	if rg := raised.rec.Rigor; rg == nil || rg.LevelOf() != rigor.Critical || rg.From != rigor.FromRequest {
		t.Errorf("a carried critical over a startup repository resolved %+v; want critical from the request: a carry may still raise", rg)
	}
}

// TestAHotfixRequestIsTheRunsOwnAndLastsOneRun: the owner's ruling of
// 2026-11-20 (point 4) puts no gate, no second approval and no question over
// `--mode hotfix` — who may ask is the door that already exists — and it writes
// the cost down the other way: the lowering is recorded on the run and belongs
// to it. The record names `request` as the layer that decided the mode, and a
// later run of the same repository that asks for nothing resolves its own
// layers again, because the request is a field of one task (task.Spec.Rigor)
// and never a value written where a repository keeps one (D12, D834).
func TestAHotfixRequestIsTheRunsOwnAndLastsOneRun(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor: { default: pro }
` + workflowsYAML
	files := map[string]string{"README.md": "hello\n", "fugaro.yaml": base}

	asked, _ := rigorRun(t, files, nil, &task.Spec{Rigor: rigor.Hotfix})
	asked.bootstrapRigor(context.Background())
	if rg := asked.rec.Rigor; rg == nil || rg.LevelOf() != rigor.Hotfix || rg.From != rigor.FromRequest {
		t.Fatalf("the asking run's block = %+v, want hotfix from %q: the lowering is recorded on the run", rg, rigor.FromRequest)
	}

	next, _ := rigorRun(t, files, nil, &task.Spec{})
	next.bootstrapRigor(context.Background())
	if got := next.reachedRigor(); got != rigor.Pro {
		t.Errorf("the next run resolved %q, want pro from the repository's own block: a request lasts one run", got)
	}
}

// TestRigorFloorHoldsTheRowAHotfixRequestReads: a request lowers a mode and
// never a floor (D8, LF5), and the owner's ruling of 2026-11-20 says which row
// that leaves a run the flag took off the axis. The floor is read off the mode
// the run is at, and the mode the layers resolved is kept beside it only when
// the request took the run *below* them (layersRigor, rigor.LayersRigor): a
// hotfix run reads hotfix's own row alone, so a floor the owner wrote for pro
// does not reach it, and the lever for a floored hotfix run is the row written
// for hotfix.
func TestRigorFloorHoldsTheRowAHotfixRequestReads(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor: { default: pro }
` + workflowsYAML
	r, _ := rigorRun(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base}, nil, &task.Spec{Rigor: rigor.Hotfix})

	r.bootstrapRigor(context.Background())

	if got := r.reachedRigor(); got != rigor.Hotfix {
		t.Fatalf("the run resolved to %q; want hotfix from the request", got)
	}
	if got := r.layersRigor; got != "" {
		t.Fatalf("the mode the layers resolved beside the request = %q; want none: a hotfix run reads its own floor row alone", got)
	}
	r.policy = policy.Effective{Layer: policy.Layer{
		ReviewFloor: map[rigor.Rigor][]policy.Level{rigor.Pro: {policy.LevelL2, policy.LevelL3}},
	}}
	r.rcp = parseRecipe(t, "version: 1\nname: loop\nsteps:\n  - review: {}\n")

	if err := r.resolveLevels(); err != nil {
		t.Fatalf("resolving the levels of a hotfix run under a floor of l2,l3 on pro: %v", err)
	}
	if r.levels.L3.On {
		t.Error("l3 is on for a hotfix run under the owner's floor of l2,l3 on pro; want it off: a floor written for pro does not hold a hotfix run (the owner's ruling of 2026-11-20)")
	}

	// The owner's lever: the same run, with the floor written for hotfix.
	r.policy = policy.Effective{Layer: policy.Layer{
		ReviewFloor: map[rigor.Rigor][]policy.Level{rigor.Hotfix: {policy.LevelL2, policy.LevelL3}},
	}}
	if err := r.resolveLevels(); err != nil {
		t.Fatalf("resolving the levels of a hotfix run under a floor of l2,l3 on hotfix: %v", err)
	}
	if !r.levels.L3.On {
		t.Error("l3 is off for a hotfix run under a floor of l2,l3 written for hotfix; want it on: hotfix's own row is the owner's lever")
	}
}

// TestLayersRigorIsKeptOnlyForARequestBelowThem is the other half: the mode
// the layers resolved is kept beside the request exactly when the request took
// the run below them, which is where a floor could otherwise be left behind. A
// request that raised the run, or that asked for the mode the layers set,
// leaves that mode in force and layersRigor empty, which is what makes
// ReviewFloorFor's answer the one ReviewFloor would have given.
func TestLayersRigorIsKeptOnlyForARequestBelowThem(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor: { default: pro }
` + workflowsYAML
	for _, tc := range []struct {
		name   string
		req    rigor.Rigor
		want   rigor.Rigor
		layers rigor.Rigor
	}{
		{"a request below the layers", rigor.POC, rigor.POC, rigor.Pro},
		{"a request for the mode they set", rigor.Pro, rigor.Pro, ""},
		{"a request above them", rigor.Critical, rigor.Critical, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := rigorRun(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base}, nil, &task.Spec{Rigor: tc.req})

			r.bootstrapRigor(context.Background())

			if got := r.reachedRigor(); got != tc.want {
				t.Fatalf("the run resolved to %q; want %q from the request", got, tc.want)
			}
			if got := r.layersRigor; got != tc.layers {
				t.Errorf("the mode kept beside the request = %q; want %q", got, tc.layers)
			}
		})
	}
}

// TestBootstrapRigorResolvesTheRunsDeclaredPaths: D22's first moment reads the
// paths the run said it would touch (`fugaro run --path`, in the task's own
// list), so the area rule a declared path matches sets the mode before any of
// the work is done. Without the declared paths bootstrap could only resolve the
// repository's default, and a Critical area would not arrive until finalize.
func TestBootstrapRigorResolvesTheRunsDeclaredPaths(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor:
  default: startup
  areas:
    - { name: payments, match: "internal/payments/**", level: critical }
    - { name: docs, match: "docs/**", level: poc }
` + workflowsYAML
	r, _ := rigorRun(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base}, nil,
		&task.Spec{Paths: []string{"internal/payments/charge.go"}})

	r.bootstrapRigor(context.Background())

	rg := r.rec.Rigor
	if rg == nil || rg.Mode != rigor.Critical || rg.From != rigor.FromArea || rg.At != runstore.RigorAtBootstrap {
		t.Fatalf("the block = %+v, want critical from the area the declared path matched", rg)
	}
	want := []runstore.RigorArea{{Name: "payments", Mode: rigor.Critical, Level: rigor.Critical, At: runstore.RigorAtBootstrap}}
	if !reflect.DeepEqual(rg.Areas, want) {
		t.Errorf("the areas = %+v, want %+v", rg.Areas, want)
	}
}

// TestFinalSectionNamesTheModeAndWhereItCameFrom: D18's one-value rule at the
// pull request — the mode the run used is in the body, in the same words the
// record carries, so the two can be diffed — in D24's shape: the mode, the
// layer that won, and the strictest area rule that matched, with a "+N" for the
// rest. A run whose record carries no block shows no line at all: an absent
// answer is not the built-in default.
func TestFinalSectionNamesTheModeAndWhereItCameFrom(t *testing.T) {
	const twoAreas = `rigor:
  default: startup
  areas:
    - { name: payments, match: "internal/payments/**", level: critical }
    - { name: ledgers, match: "internal/**", level: startup }
`
	for _, tc := range []struct {
		name  string
		block string
		want  string
	}{
		{"the repository's own default", "rigor: { default: critical }\n", "rigor: critical (repo)"},
		{"the area a path the run touched matched",
			"rigor:\n  default: startup\n  areas:\n    - { name: payments, match: \"internal/payments/**\", level: critical }\n",
			"rigor: critical (area payments)"},
		{"two matching rules, the strictest named", twoAreas, "rigor: critical (area payments +1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runAtFinalize(t, modeBase(t, tc.block), "fugaro/x", "20261024-120000-abcd", openRound)

			section := r.finalSection(false, "review round 2 still has 3 findings", nil)

			if !strings.Contains(section, tc.want) {
				t.Fatalf("the status section does not carry %q:\n%s", tc.want, section)
			}
		})
	}

	r := &run{rec: &runstore.Record{Version: 1, RunID: "20261024-120000-abcd"},
		spec: &task.Spec{}, // every run has a task: the repository it is for is what the opt-in is read by
		d:    Deps{Now: func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }}}
	if section := r.finalSection(true, "", nil); strings.Contains(section, "rigor") {
		t.Errorf("a run that resolved no mode shows a rigor line:\n%s", section)
	}
}

// toleratedFindings are the three findings the review round below leaves open,
// scored so that Startup tolerates all three and Critical blocks all three:
// the split is the mode's, and these cases read it through the mode's own
// table rather than by asserting the table again.
var toleratedFindings = []Finding{
	{Likelihood: 2, Impact: 2, FixCost: 1, Class: "correctness"},
	{Likelihood: 1, Impact: 2, FixCost: 1, Class: "cosmetic"},
	{Likelihood: 1, Impact: 1, FixCost: 1, Class: "cosmetic"},
}

// blockingFindings are the same three findings scored so that every mode with a
// floor blocks them, Startup included: the round a mode did not let through.
var blockingFindings = []Finding{
	{Likelihood: 3, Impact: 3, FixCost: 1, Class: "correctness"},
	{Likelihood: 4, Impact: 4, FixCost: 1, Class: "maintainability"},
	{Likelihood: 3, Impact: 4, FixCost: 1, Class: "performance"},
}

// seniorFindings is the last senior review round as the loop leaves it
// (runner.go seniorLoop): the findings the record's summary beside them only
// counts, which is what the mode's table reads (outcome.go, T3.1).
func seniorFindings(r *run, findings []Finding) {
	last := r.rec.Reviews[len(r.rec.Reviews)-1]
	r.lastSenior = seniorRound{Round: last.Round, Findings: findings}
}

// TestFinalSectionNamesWhatTheModeTolerated: T3.3. A run whose last senior
// round the mode let through names each finding it tolerated with the word its
// score reads as, in the reviewer's own order, and the mode that tolerated it
// — the answer the record's own reason carries too (D18). A round the mode did
// not let through is the section it was before this task, byte for byte, and
// the mode line T1.6 added is in the body either way: D18's audit requirement
// does not depend on there being something to tolerate.
func TestFinalSectionNamesWhatTheModeTolerated(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		findings []Finding
		ready    bool
		reason   string
		want     string
	}{
		{
			"the mode let the round through", "startup", toleratedFindings, true,
			"review round 2 left 3 findings, tolerated at startup",
			"review round 2: 3 findings, tolerated at startup (low, nit, nit)",
		},
		{
			"the mode blocked the round", "startup", blockingFindings, false,
			"review round 2 still has 3 findings",
			"review round 2: 3 findings",
		},
		{
			"a mode that tolerates nothing", "critical", toleratedFindings, false,
			"review round 2 still has 3 findings",
			"review round 2: 3 findings",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := modeRun(t, tc.mode, openRound)
			seniorFindings(r, tc.findings)

			section := r.finalSection(tc.ready, tc.reason, nil)

			if !strings.Contains(section, tc.want) {
				t.Fatalf("the status section does not carry %q:\n%s", tc.want, section)
			}
			if !strings.Contains(section, "rigor: "+tc.mode+" (repo)") {
				t.Errorf("the status section lost the mode line (D18):\n%s", section)
			}
			if strings.Contains(section, "tolerated") != strings.Contains(tc.want, "tolerated") {
				t.Errorf("the status section's account of what was tolerated is not the mode's own:\n%s", section)
			}
		})
	}
}

// TestFinalSectionWithoutAFindingToTolerateIsUnchanged is T3.3's other half:
// a mode-aware run with nothing to tolerate carries the section it carried
// before this task — the mode line T1.6 added, and nothing else new — so the
// feature is invisible to every run that did not leave a round open.
func TestFinalSectionWithoutAFindingToTolerateIsUnchanged(t *testing.T) {
	r, _ := modeRun(t, "startup", runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "ship"})

	section := r.finalSection(true, "", nil)

	const want = "**Ready for review** · rigor: startup (repo) · no test run yet · senior review: ship in round 2 · model cost $0.00 · finished 2026-10-24 12:00Z\n" +
		"Run `abcd` (a stale time here means the run died: `fugaro diagnose 20261024-120000-abcd`)"
	if section != want {
		t.Errorf("the status section of a run with nothing to tolerate:\n got %q\nwant %q", section, want)
	}
}

// TestFinalizeRigorResolvesFromTheRunsOwnPathsAndNeverFalls: D22's moments at
// the runner. Bootstrap declares no path here, so the run starts at its
// repository's default; finalize resolves from the paths the run's own record
// measured, and the area both paths match decides the mode.
// A moment after that which resolves weaker leaves the block as it stands: a
// mode a run has reached never falls (D22).
func TestFinalizeRigorResolvesFromTheRunsOwnPathsAndNeverFalls(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor:
  default: startup
  areas:
    - { name: payments, match: "internal/payments/**", level: critical }
    - { name: docs, match: "docs/**", level: poc }
workflows:
  server: { commands: { build: make, test: make test } }
`
	r, repo := rigorRun(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base}, nil, nil)
	ctx := context.Background()
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"internal/payments/charge.go": "package payments\n", "docs/x.md": "x\n"})
	if _, err := repo.CommitAll(ctx, "the run's work"); err != nil {
		t.Fatal(err)
	}

	r.bootstrapRigor(ctx)
	if rg := r.rec.Rigor; rg == nil || rg.Mode != rigor.Startup || rg.From != rigor.FromRepo || rg.At != runstore.RigorAtBootstrap {
		t.Fatalf("bootstrap = %+v, want the repository's default", rg)
	}

	r.recordChangedFiles(ctx)
	r.finalizeRigor()
	rg := r.rec.Rigor
	if rg == nil || rg.Mode != rigor.Critical || rg.From != rigor.FromArea || rg.At != runstore.RigorAtFinalize {
		t.Fatalf("finalize = %+v, want critical from the area that matched", rg)
	}
	if rg.Revision != "origin/main" {
		t.Errorf("the block names the revision it read: %+v", rg)
	}
	// Both rules matched; the strictest is first (D23) and each carries its
	// own mode and the moment it was applied at.
	want := []runstore.RigorArea{
		{Name: "payments", Mode: rigor.Critical, Level: rigor.Critical, At: runstore.RigorAtFinalize},
		{Name: "docs", Mode: rigor.POC, Level: rigor.POC, At: runstore.RigorAtFinalize},
	}
	if !reflect.DeepEqual(rg.Areas, want) {
		t.Errorf("the areas = %+v, want %+v", rg.Areas, want)
	}

	// D22's never-falls rule, at the runner: the bootstrap moment again,
	// which with no declared path resolves the repository's weaker default.
	before, err := json.Marshal(rg)
	if err != nil {
		t.Fatal(err)
	}
	r.recordRigor(runstore.RigorAtBootstrap, nil)
	after, err := json.Marshal(r.rec.Rigor)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("a weaker later moment moved the block:\n before %s\n after  %s", before, after)
	}
}

// TestRigorFallsBackToTheProjectLayerWhenTheBaseCannotBeRead: the mode acts on
// nothing yet, so a base that cannot be read is a warning and the project
// layer's own value — the layer came with the task and is no branch's — never
// the checked-out ref's. The block names no revision, which is what that
// field's absence is for: the run could not read one.
func TestRigorFallsBackToTheProjectLayerWhenTheBaseCannotBeRead(t *testing.T) {
	valid := "version: 1\nproject: aurora\ngit: { provider: github }\nrigor: { default: startup }\n" + workflowsYAML
	invalid := "version: 1\nproject: aurora\ngit: { provider: github }\nrigor: { default: turbo }\n" + workflowsYAML
	for _, tc := range []struct {
		name   string
		files  map[string]string
		spec   *task.Spec
		reason string
	}{
		{"the base has no fugaro.yaml", map[string]string{"README.md": "hello\n"}, nil, "no file"},
		{"the base's fugaro.yaml does not resolve", map[string]string{"README.md": "hello\n", "fugaro.yaml": invalid}, nil, "invalid file"},
		{"the task's project layer is invalid", map[string]string{"README.md": "hello\n", "fugaro.yaml": valid},
			&task.Spec{ProjectLayer: &task.ProjectLayer{SHA256: "deadbeef", YAML: "version: 1\nproject: [\n"}}, "invalid layer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Layer: rigorLayer(t, rigor.Pro), RepoRigor: &config.Rigor{Default: rigor.POC}}
			r, _ := rigorRun(t, tc.files, cfg, tc.spec)

			// It reports and does not fail: nothing reads the mode yet.
			r.bootstrapRigor(context.Background())

			if r.rigorRev != "" {
				t.Errorf("%s: the block names a revision it never read: %q", tc.reason, r.rigorRev)
			}
			if !reflect.DeepEqual(r.rigorCfg, cfg.ProjectRigor()) {
				t.Errorf("%s: the configuration = %+v, want the project layer's", tc.reason, r.rigorCfg)
			}
			if r.rigorCfg.Repo != "" {
				t.Errorf("%s: the fallback took the branch under review's own value: %+v", tc.reason, r.rigorCfg)
			}
			rg := r.rec.Rigor
			if rg == nil || rg.Mode != rigor.Pro || rg.From != rigor.FromProject || rg.At != runstore.RigorAtBootstrap {
				t.Errorf("%s: the block = %+v, want pro from the project layer", tc.reason, rg)
			}
			if rg != nil && rg.Revision != "" {
				t.Errorf("%s: the block names a revision: %+v", tc.reason, rg)
			}
		})
	}
}

// areaNamesOf is the area names in order, for a configuration's own list.
func areaNamesOf(areas []rigor.Area) []string {
	out := make([]string, 0, len(areas))
	for _, a := range areas {
		out = append(out, a.Name)
	}
	return out
}
