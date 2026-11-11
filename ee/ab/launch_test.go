// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is the pair launch's own fixture: a project whose runs bucket is a
// directory, whose budget backend is the fake an installation runs against,
// and whose published project layer carries the ab: block. It is a sibling of
// ee/ab/cli's fixture rather than a shared helper, for the reason that one
// gives: an ee/ package's tests cannot reach another package's _test.go. What
// it seeds is different too — that one writes pairs of records for the report
// to read, and this one is driven through the released launcher, which samples
// nothing until a planner answers.

const (
	// abLaunchBase is the release base image the fixture's job image was built
	// from: a release at or past every *Since gate this launcher applies, so a
	// launch here is judged on the thing its own test is about.
	abLaunchBase = "ghcr.io/dimipaun/fugaro-base:0.23.0"
	// abLaunchChampion is the fixture repository's own coder: a Claude model,
	// which goes direct and needs no route.
	abLaunchChampion = "claude-sonnet-5-5"
	// abLaunchChallenger is the model the fixture's project layer names first
	// as a challenger: a route's own model, priced from the built-in catalog,
	// which is the shape a real installation's list carries (a project layer
	// may not name a Claude model: config.CheckModelPattern refuses one).
	abLaunchChallenger = "deepseek/deepseek-v4.1-flash"
	// abLaunchSigner is the service account the budget mint signs as.
	abLaunchSigner = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
	// abLaunchUSD is one dollar in the micros the budget database counts.
	abLaunchUSD = int64(1_000_000)
)

// abLaunchPlannerOnce registers the fixture's planner, which the core seam
// refuses twice and every test in this binary launches through.
var abLaunchPlannerOnce sync.Once

// abSampledID is a run id whose draw lands under the highest rate a project
// layer may set. A launch's own id is generated (task.NewRunID) and a test
// cannot choose it — a chosen --run-id is never eligible (D558) — so the
// draw the sampler makes would be a coin no test can load. The planner below
// is handed a copy of the task (PlannedRun.Spec), so rewriting its id there
// changes the draw and nothing about the run the launch starts.
var abSampledID = func() string {
	for i := 0; ; i++ {
		id := fmt.Sprintf("20260101-120000-%04x", i)
		if Sampled(id, config.ABMaxRate) {
			return id
		}
	}
}()

// abLaunchPlanner is the test's planner: Plan with the drawn id standing in
// for the launch's own.
func abLaunchPlanner(ctx context.Context, run fcli.PlannedRun) (*fcli.Pair, error) {
	if run.Spec != nil {
		run.Spec.RunID = abSampledID
	}
	return Plan(ctx, run)
}

// abLaunchFixture is a project whose fakes the launcher can drive end to end.
type abLaunchFixture struct {
	dir    string
	runs   string
	bucket string
	slug   string
	run    *gcpfake.Run
	db     *gcpfake.RTDB
}

func newABLaunch(t *testing.T) *abLaunchFixture {
	t.Helper()
	run := gcpfake.NewRun(t)
	logging := gcpfake.NewLogging(t)
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(abLaunchSigner)
	db := gcpfake.NewRTDB(t)
	run.Project, run.Region = "proj-1234", "us-east5"
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	f := &abLaunchFixture{dir: dir, runs: runs, bucket: "file://" + runs, slug: slug, run: run, db: db}
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\n" +
		"runs_bucket: fugaro-runs-proj-1234\nbucket_url: " + f.bucket + "\n" +
		"user: someone@example.com\nmax_parallel: 2\n" +
		"base_images: { base: " + abLaunchBase + " }\n" +
		"endpoints: { run: " + run.URL + "/, logging: " + logging.URL + "/, iam_credentials: " + iam.URL + "/, no_auth: true }\n" +
		"budget: { mode: enforce, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + db.URL + ", token_signer: " + abLaunchSigner + " }\n" +
		// The route that serves the challenger: the owner's, in the project
		// config, opened to this repository's code (F18).
		"providers:\n  openrouter: { kind: anthropic-compat, base_url: https://openrouter.ai/api, auth: bearer, secret: openrouter-api-key, models: [deepseek/*], allow_data_to: [acme/app] }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	f.writeConfigDir(t, cfg)
	run.AddJob(gcp.JobName(slug, "web"), "4", "8Gi")
	f.writeMarker(t)
	// The database a real installation runs against carries Fugaro's own mark,
	// which every writer into it asks for before it writes (init wrote it).
	db.Set("fugaro/project", "aurora")
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": "aurora", "gcp_project": "proj-1234"})
	db.Set("config/mode", "enforce")
	db.Set("config/caps/global", map[string]any{"dailyMicros": 100 * abLaunchUSD, "perRunMicros": 10 * abLaunchUSD})
	db.Set("config/caps/defaults", map[string]any{"repoDailyMicros": 50 * abLaunchUSD, "repoPerRunMicros": 5 * abLaunchUSD})
	db.Set("config/limits", map[string]any{"maxReserveMicros": 5 * abLaunchUSD})
	f.writeBuildRecord(t, abLaunchBase)
	// A launch from outside the repository's checkout resolves the layer the
	// task carries (launchLayerConfig), so the agent the launch judges — and
	// the ab: block it samples on — are this layer's.
	f.publishLayer(t,
		"defaults:\n  agent: { auth: api-key, models: { coder: "+abLaunchChampion+" } }\n"+
			"ab: { rate: "+fmt.Sprintf("%g", config.ABMaxRate)+", challengers: ["+abLaunchChallenger+"] }\n"+
			"default_profile: web\nprofiles:\n  web: { commands: { build: make, test: make test } }\n")
	abLaunchPlannerOnce.Do(func() {
		if err := fcli.RegisterLaunchPlanner(abLaunchPlanner); err != nil {
			t.Fatal(err)
		}
	})
	return f
}

// writeConfigDir points the project configs at a directory of the fixture's
// own and writes aurora's config there, with the environment that could name
// another project cleared: the layer cache and the project name live in it.
func (f *abLaunchFixture) writeConfigDir(t *testing.T, cfg string) {
	t.Helper()
	// The launcher reads the checkout it stands in for the repository's own
	// fugaro.yaml (the walk from the working directory), and this repository is
	// Fugaro's own: without a working directory of the fixture's own, the
	// launch would take this checkout's project (fugaro-dev) for the one it
	// launches for.
	cwd := filepath.Join(f.dir, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	xdg := filepath.Join(f.dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(f.dir, "cache"))
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_CONFIG", "")
	projects := filepath.Join(xdg, "fugaro", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projects, "aurora.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
}

// writeMarker names the fixture's project in its runs bucket, which every
// cloud command checks before it calls anything.
func (f *abLaunchFixture) writeMarker(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.runs, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeBuildRecord stores the build record of acme/app's web workflow: the
// image the launch's own gates judge, with the base image it was built from.
func (f *abLaunchFixture) writeBuildRecord(t *testing.T, base string) {
	t.Helper()
	data, err := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "web", BaseRef: base})
	if err != nil {
		t.Fatal(err)
	}
	f.writeBucket(t, imagecheck.RecordKey(f.slug, "web"), data)
}

// publishLayer writes the project layer the launches read, where they find it:
// the object in the runs bucket under the key every layer read of the project
// reads (config.LayerKey), anchored on the fixture's own project.
func (f *abLaunchFixture) publishLayer(t *testing.T, yaml string) {
	t.Helper()
	f.writeBucket(t, config.LayerKey, []byte("version: 1\nproject: aurora\ngcp_project: proj-1234\n"+yaml))
}

func (f *abLaunchFixture) writeBucket(t *testing.T, key string, data []byte) {
	t.Helper()
	path := filepath.Join(f.runs, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// launch runs the released launcher's own tree, as a developer's terminal
// does: the A/B hook is the seam's, not a command of this package's.
func (f *abLaunchFixture) launch(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := fcli.NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(fcli.NormalizeArgs(append([]string{"run", "--repo", "acme/app"}, args...)))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// abLaunchDoc is the part of `fugaro run --json` a pair's test reads.
type abLaunchDoc struct {
	RunID string `json:"run_id"`
	Pair  *struct {
		ID         string `json:"id"`
		Batch      string `json:"batch"`
		Challenger string `json:"challenger"`
		Reason     string `json:"reason"`
	} `json:"pair"`
}

func abLaunchJSON(t *testing.T, out string) abLaunchDoc {
	t.Helper()
	var doc abLaunchDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the launch's --json document: %v (%q)", err, out)
	}
	return doc
}

// taskOf is the task a launch stored for run, read the way its job reads it.
func (f *abLaunchFixture) taskOf(t *testing.T, runID string) *task.Spec {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, f.slug, runID).ReadTask(ctx)
	if err != nil {
		t.Fatalf("the task of run %s: %v", runID, err)
	}
	return spec
}

// TestSampledLaunchStartsTwoRuns: a launch whose draw lands under the rate
// starts the champion and, beside it, the shadow challenger — the same task at
// the same commit on the layer's challenger model, with the pair's two ab
// blocks and one batch label.
func TestSampledLaunchStartsTwoRuns(t *testing.T) {
	f := newABLaunch(t)
	out, errOut, err := f.launch(t, "--recipe", "default", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	if got := len(f.run.RunRequests()); got != 2 {
		t.Fatalf("the launch started %d runs, want two (the champion and its shadow challenger):\n%s%s", got, out, errOut)
	}
	doc := abLaunchJSON(t, out)
	if doc.Pair == nil || doc.Pair.ID == "" {
		t.Fatalf("--json carries no pair: %q", out)
	}
	if doc.Pair.Challenger == "" {
		t.Fatalf("--json names no challenger run: %q", out)
	}
}
