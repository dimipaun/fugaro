// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// abLaunchPairBlock is the ab: block every fixture but the ones about a skip
// publishes: the owner's challenger, at the highest rate a project layer may
// set. abLaunchDailyBlock is the same block with the day's cap on the pairs
// one repository is sampled for (D566) written down.
var (
	abLaunchPairBlock  = "ab: { rate: " + fmt.Sprintf("%g", config.ABMaxRate) + ", challengers: [" + abLaunchChallenger + "] }"
	abLaunchDailyBlock = "ab: { rate: " + fmt.Sprintf("%g", config.ABMaxRate) + ", challengers: [" + abLaunchChallenger + "], max_pairs_per_day: 1 }"
)

// abLaunchLayerText is the published layer with the ab: block it is given: the
// block is the one thing a fixture about a skip varies, and everything else —
// the champion's coder and the profile that resolves the layer — is what a
// launch of this project reads either way.
func abLaunchLayerText(block string) string {
	return "defaults:\n  agent: { auth: api-key, models: { coder: " + abLaunchChampion + " } }\n" +
		block + "\n" +
		"default_profile: web\nprofiles:\n  web: { commands: { build: make, test: make test } }\n"
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
	return newABLaunchLayer(t, abLaunchLayerText(abLaunchPairBlock))
}

// newABLaunchLayer is newABLaunch with the layer's ab: block of the caller's
// choosing: a launch reads the layer it is given before it decides anything.
func newABLaunchLayer(t *testing.T, layer string) *abLaunchFixture {
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
	f.publishLayer(t, layer)
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

// TestBothArmsSameCommit: the two arms are one run twice (D571, U5). Both
// tasks are the same bytes — the same commit, workflow, recipe, mode, tier and
// --path set — but for the arm's own run id and ab block, which the launcher
// copies onto the task the champion was started from.
func TestBothArmsSameCommit(t *testing.T) {
	f := newABLaunch(t)
	out, errOut, err := f.launch(t, "--recipe", "default", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	doc := abLaunchJSON(t, out)
	if doc.Pair == nil || doc.Pair.Challenger == "" {
		t.Fatalf("the launch sampled no pair: %q", out)
	}
	champion, arm := f.taskOf(t, doc.RunID), f.taskOf(t, doc.Pair.Challenger)
	if champion.Ref != arm.Ref {
		t.Errorf("the arms were launched at different refs: %q and %q", champion.Ref, arm.Ref)
	}
	// The arm is the champion's task as the *job* reads it, so the two are
	// compared with the fields D555 gives the arm alone cleared: everything
	// else has to match, or the pair compares two runs of different shapes.
	champion.RunID, arm.RunID = "", ""
	champion.AB, arm.AB = nil, nil
	want, err := json.Marshal(champion)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(arm)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("the arms' tasks differ but for the run id and the ab block:\nchampion %s\nchallenger %s", want, got)
	}
	if f.run.RunRequests()[0].Env["FUGARO_RUN"] == f.run.RunRequests()[1].Env["FUGARO_RUN"] {
		t.Errorf("both arms were started as one run: %v", f.run.RunRequests())
	}
}

// TestSkipReasonInJSON: a project that opens no challenger samples nothing,
// and says so in the closed reason the launcher prints on stderr and carries
// in the document (D556, D554).
func TestSkipReasonInJSON(t *testing.T) {
	f := newABLaunchLayer(t, abLaunchLayerText("ab: { rate: 0.1 }"))
	out, errOut, err := f.launch(t, "--recipe", "default", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	if got := len(f.run.RunRequests()); got != 1 {
		t.Fatalf("the launch started %d runs, want the champion alone", got)
	}
	doc := abLaunchJSON(t, out)
	if doc.Pair == nil || doc.Pair.Reason != ReasonNoChallenger {
		t.Fatalf("--json carried no %q reason: %q", ReasonNoChallenger, out)
	}
	if want := "ab: no pair sampled: " + ReasonNoChallenger + "; launching the champion alone"; !strings.Contains(errOut, want) {
		t.Errorf("stderr does not say %q:\n%s", want, errOut)
	}
}

// TestNoPrecheckNoSample: D573 never samples beside a skipped pre-check. The
// champion goes on with the check skipped, as --no-budget-check asks.
func TestNoPrecheckNoSample(t *testing.T) {
	f := newABLaunch(t)
	out, errOut, err := f.launch(t, "--recipe", "default", "--no-budget-check", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	if got := len(f.run.RunRequests()); got != 1 {
		t.Fatalf("the launch started %d runs, want the champion alone", got)
	}
	doc := abLaunchJSON(t, out)
	if doc.Pair == nil || !strings.Contains(doc.Pair.Reason, "--no-budget-check") {
		t.Fatalf("--json does not name the skipped pre-check: %q", out)
	}
}

// TestUncappedNoSample: a project whose database holds no per-run cap and no
// daily cap has no figure to measure D573's margin against, so nothing is
// sampled — and the champion still launches, because a pair is never the
// launch (D554).
func TestUncappedNoSample(t *testing.T) {
	f := newABLaunch(t)
	// A project whose database holds no caps at all and does not enforce them
	// yet (the mode the launch's own pre-check reads): the champion's launch
	// is nobody's refusal, and the margin has no figure to measure itself
	// against.
	f.db.Set("config/mode", "observe")
	f.db.Set("config/caps/global", nil)
	f.db.Set("config/caps/defaults", nil)
	out, errOut, err := f.launch(t, "--recipe", "default", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	if got := len(f.run.RunRequests()); got != 1 {
		t.Fatalf("the launch started %d runs, want the champion alone", got)
	}
	doc := abLaunchJSON(t, out)
	if doc.Pair == nil || !strings.Contains(doc.Pair.Reason, "no per-run cap") {
		t.Fatalf("--json does not name the missing cap: %q", out)
	}
}

// TestMarginRequired: five per-run caps of the day's headroom are what D573
// requires before a pair is sampled, and a day under that is not sampled.
func TestMarginRequired(t *testing.T) {
	f := newABLaunch(t)
	// A repository day with four per-run caps left: under the five the margin
	// asks for, and above the two runs the pair's own pre-check leases.
	f.db.Set("config/caps/defaults", map[string]any{
		"repoDailyMicros":  20 * abLaunchUSD,
		"repoPerRunMicros": 5 * abLaunchUSD,
	})
	out, errOut, err := f.launch(t, "--recipe", "default", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	if got := len(f.run.RunRequests()); got != 1 {
		t.Fatalf("the launch started %d runs, want the champion alone", got)
	}
	doc := abLaunchJSON(t, out)
	if doc.Pair == nil || !strings.Contains(doc.Pair.Reason, "five per-run caps") {
		t.Fatalf("--json does not name the margin: %q", out)
	}
}

// TestDailyCountStopsSampling: the project's own cap on the pairs one
// repository is sampled for per day (D566) is read off the day's challenger
// records, and a day that is full samples nothing.
func TestDailyCountStopsSampling(t *testing.T) {
	f := newABLaunchLayer(t, abLaunchLayerText(abLaunchDailyBlock))
	f.seedChallenger(t)
	out, errOut, err := f.launch(t, "--recipe", "default", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	if got := len(f.run.RunRequests()); got != 1 {
		t.Fatalf("the launch started %d runs, want the champion alone", got)
	}
	doc := abLaunchJSON(t, out)
	if doc.Pair == nil || doc.Pair.Reason != ReasonDailyCount {
		t.Fatalf("--json carried no %q reason: %q", ReasonDailyCount, out)
	}
}

// seedChallenger stores one challenger record of today, the way a launch that
// sampled a pair earlier in the day left one: a day's count is a read of the
// day's records, and nothing else reserves a pair slot.
func (f *abLaunchFixture) seedChallenger(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	id := time.Now().UTC().Format("20060102") + "-000000-0001"
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: "acme/app", Status: runstore.StatusRunning, Stage: "implement",
		AB: &task.AB{PairID: "pair-20260101-000000-0000", Arm: task.ABArmChallenger,
			Publish: task.ABPublishNone, Challenger: abLaunchChallenger, ChallengerSource: task.ABChallengerOwner},
	}
	if err := runstore.Open(b, f.slug, id).CreateRecord(ctx, rec); err != nil {
		t.Fatalf("seeding today's challenger: %v", err)
	}
}

// TestChallengerFailureNeverFailsChampion: the money path. The second arm's
// start is refused and the launch is still the developer's run: exit zero, the
// champion launched, and one line saying the arm did not start (D554).
func TestChallengerFailureNeverFailsChampion(t *testing.T) {
	f := newABLaunch(t)
	var calls atomic.Int32
	f.run.SetOnRun(func(gcpfake.RunCall) {
		// The champion's own start is the first call; every one after it is
		// the arm, which the fake is made to refuse. A launch starts its arms
		// one after the other, so the counter is the whole of the ordering.
		if calls.Add(1) == 1 {
			f.run.FailRunWith = http.StatusInternalServerError
		}
	})
	out, errOut, err := f.launch(t, "--recipe", "default", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("a challenger that did not start failed the champion's launch: %v\n%s%s", err, out, errOut)
	}
	doc := abLaunchJSON(t, out)
	if doc.RunID == "" {
		t.Fatalf("the champion did not launch: %q", out)
	}
	if doc.Pair == nil || doc.Pair.ID == "" || doc.Pair.Challenger != "" {
		t.Fatalf("--json does not show a pair whose arm never started: %q", out)
	}
	if want := "ab: the challenger did not start:"; !strings.Contains(errOut, want) {
		t.Errorf("stderr does not say %q:\n%s", want, errOut)
	}
}

// TestOldImageNotSampled: a job image older than the ab field is never
// sampled, because a pair written onto one costs the champion its run — the
// runner refuses the whole task at bootstrap. The champion goes on alone, on
// the task it was already launched with.
func TestOldImageNotSampled(t *testing.T) {
	f := newABLaunch(t)
	f.writeBuildRecord(t, "ghcr.io/dimipaun/fugaro-base:0.13.0")
	// The recipe is the one whose own constructs a 0.13.0 runner knows (solo
	// reads review and nothing newer), so the image the pair gate refuses is
	// one the launch itself still goes on with.
	out, errOut, err := f.launch(t, "--recipe", "solo", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	if got := len(f.run.RunRequests()); got != 1 {
		t.Fatalf("the launch started %d runs, want the champion alone", got)
	}
	doc := abLaunchJSON(t, out)
	if doc.Pair == nil || doc.Pair.Reason != "old-image" {
		t.Fatalf("--json carried no old-image reason: %q", out)
	}
	champion := f.taskOf(t, doc.RunID)
	if champion.AB != nil || champion.Batch != "" {
		t.Errorf("the champion was marked with a pair it did not start: %+v, batch %q", champion.AB, champion.Batch)
	}
}
