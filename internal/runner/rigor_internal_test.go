package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
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
	remote := testutil.NewRemote(t, remoteFiles)
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		cfg = &config.Config{}
	}
	cfg.Git.BaseBranch = "main"
	if spec == nil {
		spec = &task.Spec{}
	}
	r := &run{
		repo: repo, cfg: cfg, spec: spec, rec: &runstore.Record{},
		d: Deps{Log: slog.New(slog.DiscardHandler)},
	}
	return r, repo
}

// workflowsYAML is the one required block a fugaro.yaml must carry to resolve
// at all: its absence would make every base file in these tests unreadable, and
// the tests would pass for the wrong reason.
const workflowsYAML = "workflows:\n  server: { commands: { build: make, test: make test } }\n"

// rigorLayer is a project layer whose defaults.rigor sets Critical: the value
// a run falls back to when no branch can be read.
func rigorLayer(t *testing.T, mode rigor.Mode) *config.ProjectLayer {
	t.Helper()
	return &config.ProjectLayer{Defaults: config.LayerDefaults{Rigor: config.LayerRigor{Default: mode}}}
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
    - { name: payments, match: "internal/payments/**", mode: critical }
    - { name: docs, match: "docs/**", mode: poc }
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

// TestFinalizeRigorResolvesFromTheRunsOwnPathsAndNeverFalls: D22's moments at
// the runner. Bootstrap declares no path (T1.6 owns the flag), so the run
// starts at its repository's default; finalize resolves from the paths the
// run's own record measured, and the area both paths match decides the mode.
// A moment after that which resolves weaker leaves the block as it stands: a
// mode a run has reached never falls (D22).
func TestFinalizeRigorResolvesFromTheRunsOwnPathsAndNeverFalls(t *testing.T) {
	base := `version: 1
project: aurora
git: { provider: github }
rigor:
  default: startup
  areas:
    - { name: payments, match: "internal/payments/**", mode: critical }
    - { name: docs, match: "docs/**", mode: poc }
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
	// Both rules matched; the strictest is first (D23) and each carries the
	// moment it was applied at.
	want := []runstore.RigorArea{{Name: "payments", At: runstore.RigorAtFinalize}, {Name: "docs", At: runstore.RigorAtFinalize}}
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
