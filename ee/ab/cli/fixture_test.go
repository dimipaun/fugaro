// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/ee/ab"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
	"github.com/dimipaun/fugaro/internal/verify"
)

// This file is this package's fixture: a project whose runs bucket is a
// directory, with pairs written into it. It is a sibling of ee/report/cli's
// fixture rather than a shared helper, for the reason that one is itself a
// rebuild of internal/cli's: an ee/ package's tests cannot reach another
// package's _test.go, and the two fixtures seed different things (day records
// and a Firestore history there, pairs of run records here).

// abAppSlug is the storage slug of the fixture's repository, GitHub's acme/app.
var abAppSlug = mustSlug("github", "acme/app")

func mustSlug(provider, repo string) string {
	s, err := task.Slug(provider, repo)
	if err != nil {
		panic(err)
	}
	return s
}

// abDeps is what the fixture's tree is built with: the core's own cap, which a
// test lowers through withABMaxRuns. A moved command takes its seams through
// the constructor (§5.5: no production setter exists for a test to override).
var abDeps = Deps{MaxRuns: fcli.UserStatsMaxRuns}

// abFixture is a local config pointing at fakes and a file:// runs bucket.
type abFixture struct {
	dir    string
	runs   string // the runs bucket's directory, which is also the layer's
	bucket string // file:// URL
}

// newABFixture writes the project config, the runs bucket and the installation
// marker every cloud command checks.
func newABFixture(t *testing.T) *abFixture {
	t.Helper()
	f := &abFixture{dir: t.TempDir()}
	run := gcpfake.NewRun(t)
	logging := gcpfake.NewLogging(t)
	run.Project, run.Region = "proj-1234", "us-east5"
	f.runs = filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(f.runs, 0o755); err != nil {
		t.Fatal(err)
	}
	f.bucket = "file://" + f.runs
	// The runs bucket carries its installation's default name even though the
	// local config reads and writes it through a file:// URL: a project layer
	// read is keyed by that name, and a config naming any other one is left
	// with no layer at all (findLayer's own rule), which would make every rate
	// here the shipped figure whatever the fixture published.
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\nbucket_url: " + f.bucket +
		"\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + run.URL + "/, logging: " + logging.URL + "/, no_auth: true }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	path := f.writeConfigDir(t)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	run.AddJob(gcp.JobName(abAppSlug, "web"), "4", "8Gi")
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(f.runs, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, marker, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// writeConfigDir points the project configs at a directory of the fixture's
// own, clears the environment that could name another project, and answers
// where aurora's config goes.
func (f *abFixture) writeConfigDir(t *testing.T) string {
	t.Helper()
	xdg := filepath.Join(f.dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// The project-name and layer caches live here, never in the developer's.
	t.Setenv("XDG_CACHE_HOME", filepath.Join(f.dir, "cache"))
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_CONFIG", "")
	projects := filepath.Join(xdg, "fugaro", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(projects, "aurora.yaml")
}

// appendConfig adds top-level YAML (such as budget: …) to the project config.
func (f *abFixture) appendConfig(t *testing.T, yaml string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(yaml)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// seed writes one record into the fixture's runs bucket.
func (f *abFixture) seed(t *testing.T, slug, runID string, rec *runstore.Record) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := runstore.Open(b, slug, runID).WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// publishLayer writes the project layer an operator published, where the layer
// read the command makes finds it: the object in the runs bucket under the key
// every layer read of the project reads (config.LayerKey), anchored on the
// fixture's own project. An empty yaml is still a published layer (the
// version and anchor lines alone), which is how a project that set nothing in
// it is told from one that published none.
func (f *abFixture) publishLayer(t *testing.T, yaml string) {
	t.Helper()
	text := "version: 1\nproject: aurora\ngcp_project: proj-1234\n" + yaml
	path := filepath.Join(f.runs, filepath.FromSlash(config.LayerKey))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// judgeVerdictFixture is one pair's outcome in a verdicts object, as A5.2
// writes it: the pair it decided, the arm the judge took and the word beside it.
type judgeVerdictFixture struct {
	PairID string `json:"pair_id"`
	Winner string `json:"winner,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// judgeVerdictsFixture is a judge run's verdicts object (runstore.VerdictsName):
// the model that judged every pair in it, and the pairs themselves.
type judgeVerdictsFixture struct {
	Version    int                   `json:"version"`
	JudgeModel string                `json:"judge_model"`
	Pairs      []judgeVerdictFixture `json:"pairs"`
}

// seedJudge writes one judge run: the record the read tells a judge run by — the
// catalog recipe A5.2's step runs — and the verdicts object it left under its
// own prefix, which is the only place a verdict is written (D559).
func (f *abFixture) seedJudge(t *testing.T, runID, model string, verdicts ...judgeVerdictFixture) {
	t.Helper()
	body, err := json.Marshal(judgeVerdictsFixture{Version: 1, JudgeModel: model, Pairs: verdicts})
	if err != nil {
		t.Fatal(err)
	}
	f.seedJudgeRun(t, runID, body)
}

// seedJudgeRun is seedJudge with the object's own bytes, so a test can leave an
// object this read cannot open.
func (f *abFixture) seedJudgeRun(t *testing.T, runID string, body []byte) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	start, err := runstore.RunTime(runID)
	if err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{
		RunID: runID, Repo: "acme/app", Status: "succeeded", StartedAt: start,
		Recipe: &runstore.RecipeRecord{Name: ab.RecipeName, Source: "catalog", SHA256: strings.Repeat("cd", 32)},
	}
	store := runstore.Open(b, abAppSlug, runID)
	if err := store.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteAll(ctx, store.Prefix()+runstore.VerdictsName, body, nil); err != nil {
		t.Fatal(err)
	}
}

// seedCorrupt writes a result.json the bucket holds and this build cannot read.
func (f *abFixture) seedCorrupt(t *testing.T, slug, runID string) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.WriteAll(ctx, runstore.Open(b, slug, runID).Prefix()+"result.json", []byte(`{"version": 1, "run_id": `), nil); err != nil {
		t.Fatal(err)
	}
}

// seedPair writes one sampled launch's two arms under pairID.
func (f *abFixture) seedPair(t *testing.T, pairID, startID, mode, tr, championModel, challengerModel string) {
	t.Helper()
	start, err := runstore.RunTime(startID)
	if err != nil {
		t.Fatal(err)
	}
	armOf := func(id string, arm task.ABArm, model string, cost float64) *runstore.Record {
		fin := start.Add(time.Hour)
		rec := &runstore.Record{
			RunID: id, Repo: "acme/app", Status: "succeeded", StartedAt: start, FinishedAt: &fin,
			StartSHA: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f", HeadSHA: "1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e",
			AB:     &task.AB{PairID: pairID, Arm: arm, Challenger: challengerModel, ChallengerSource: task.ABChallengerOwner},
			Recipe: &runstore.RecipeRecord{Name: "default", Source: "catalog", SHA256: "abababababababababababababababababababababababababababababababab"},
			Rigor:  &runstore.RigorRecord{Mode: rigor.Mode(mode)},
			Tier:   &runstore.TierRecord{Tier: tier.Tier(tr)},
			Verify: []verify.Record{{Kind: verify.KindTest, HeadSHA: "1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e", CleanTree: true, Passed: true}},
			Stages: []runstore.StageTiming{{Name: "implement", Model: model}},
			Cost:   &runstore.Cost{ModelUSD: cost, TotalUSD: cost, Estimate: true},
		}
		return rec
	}
	// The two arms differ in their coder model alone (D555), and the champion
	// is the cheaper one here: the ratio a report reads is a real one.
	f.seed(t, abAppSlug, startID, armOf(startID, task.ABArmChampion, championModel, 1))
	challenger := start.Add(time.Minute).UTC().Format("20060102-150405") + "-cccc"
	f.seed(t, abAppSlug, challenger, armOf(challenger, task.ABArmChallenger, challengerModel, 0.5))
}

// withABMaxRuns lowers the read's cap for one test, so a truncated window can
// be driven with three runs rather than thousands.
func withABMaxRuns(t *testing.T, n int) {
	t.Helper()
	old := abDeps.MaxRuns
	abDeps.MaxRuns = n
	t.Cleanup(func() { abDeps.MaxRuns = old })
}

// abTestRoot is the tree the tests drive: the released tree with this package's
// command in place of the stub a build without it shows for the name. It
// assembles the tree itself rather than blank-importing ee/register, which
// would be an import cycle (ee/register imports this package); the registration
// itself is pinned in ee/register/ab_command_test.go.
func abTestRoot() *cobra.Command {
	root := fcli.NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "ab" {
			root.RemoveCommand(c)
			break
		}
	}
	root.AddCommand(newABCmd(abDeps))
	return root
}

// executeAB runs the tree with args and captures its output.
func executeAB(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := abTestRoot()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(fcli.NormalizeArgs(args))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}
