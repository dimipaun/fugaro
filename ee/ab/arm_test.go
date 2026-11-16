// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is the deferred arm's fixture: a runs bucket in memory holding one
// finished champion, and the launch's own facts. It drives ReviewerArm
// directly rather than through the released launcher, because the arm is the
// one piece of a launch a person does not start: the champion's run started
// hours ago, and what a test needs is the champion's own prefix, its record and
// its patch.

const (
	armRepo        = "acme/app"
	armChampion    = "20261110-100000-aaaa"
	armPairID      = "pair-20261110-100000-aaaa"
	armChampionRef = "release/1.4"
	// armStartSHA is the commit the champion's branch was cut from: a full SHA,
	// which is what a record carries and what the arm is cut at, while the
	// champion's own task names a branch that has moved on since.
	armStartSHA = "9f2c1b0a4d3e5f60718293a4b5c6d7e8f9012345"
	armDiff     = "diff --git a/one.txt b/one.txt\n--- a/one.txt\n+++ b/one.txt\n@@ -1 +1 @@\n-alpha\n+beta\n"
	// armWorkflow is the workflow the champion ran, whose own ab: block is
	// where a launch asks for a reviewer arm (O12).
	armWorkflow = "web"
	// armLevelModel is the model the fixture's reviewed level runs: named in the
	// configuration so the level is on — a pair at a level the run does not run
	// varies a review nobody made, which the arm refuses — and so the challenger
	// is judged against a model that is not its own.
	armLevelModel = "claude-opus-5-5"
)

// armFixture is one champion's own prefix in a runs bucket no cloud is needed
// for, plus the launch a reviewer arm is asked about.
type armFixture struct {
	t      *testing.T
	bucket *blobx.Bucket
	cloud  *fcli.Cloud
	slug   string
}

func newArmFixture(t *testing.T) *armFixture {
	t.Helper()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	t.Cleanup(func() { b.Close() })
	slug, err := task.Slug("github", armRepo)
	if err != nil {
		t.Fatal(err)
	}
	return &armFixture{t: t, bucket: b, cloud: fcli.CloudOverBucket(b), slug: slug}
}

// store is the champion's prefix, the one its task, its record and its patch
// live under.
func (f *armFixture) store() *runstore.Store {
	return runstore.Open(f.bucket.Bucket, f.slug, armChampion)
}

// seed writes the champion's task and record. The patch object is written
// separately: whether it exists yet is the whole of what the first test is
// about.
func (f *armFixture) seed(rec runstore.Record) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.store().WriteTask(ctx, armChampionTask()); err != nil {
		f.t.Fatal(err)
	}
	if err := f.store().WriteRecord(ctx, &rec); err != nil {
		f.t.Fatal(err)
	}
}

// putPatch writes the champion's own patch object, which the champion's job
// does at its finalize (uploadPatch).
func (f *armFixture) putPatch(diff string) {
	f.t.Helper()
	if err := f.store().PutFile(context.Background(), runstore.PatchName, []byte(diff), "text/x-diff"); err != nil {
		f.t.Fatal(err)
	}
}

// champion is the record a launch reads, through the same read every other
// reader of a run makes: the arm is answered about a record this package did
// not construct.
func (f *armFixture) champion() fcli.RunRecord {
	f.t.Helper()
	recs, _, _, err := f.cloud.ReadRunRecords(context.Background(), []string{f.slug}, time.Time{}, time.Time{}, 10)
	if err != nil {
		t := f.t
		t.Fatalf("reading %s's runs: %v", f.slug, err)
	}
	for _, rec := range recs {
		if rec.ID() == armChampion {
			return rec
		}
	}
	f.t.Fatalf("the bucket holds no run %s", armChampion)
	return fcli.RunRecord{}
}

// plannedRun is the launch's own facts: the project layer's challenger list,
// the route that serves it opened to this repository, the budget mode a
// provider-served challenger needs (F18), and the workflow's own ask for a
// reviewer arm at level — which is the only way a launch asks for one, so a
// test that wants another level writes it there rather than passing it in.
func (f *armFixture) plannedRun(level task.ABSlot) fcli.PlannedRun {
	return fcli.PlannedRun{
		Cloud: f.cloud, Slug: f.slug, Repo: armRepo, Spec: armChampionTask(),
		Config: &config.Config{
			Git: config.Git{Provider: gitprov.KindGitHub},
			Agent: config.Agent{
				Auth:   "api-key",
				Models: config.ModelRoles{Coder: abLaunchChampion},
				Levels: armLevels(level, armLevelModel),
			},
			Layer: &config.ProjectLayer{AB: &config.AB{Challengers: []string{abLaunchChallenger}}},
			Workflows: map[string]config.Workflow{
				armWorkflow: {AB: &config.WorkflowAB{ReviewerArm: string(level)}},
			},
		},
		Policy:    policy.Effective{Mode: policy.ModeEnforce},
		Providers: armProviders(),
		Prices:    armPrices(f.t),
	}
}

// armLevels is the configuration's own levels block with the reviewed level
// turned on and named model — the shape a file has when it asks for an arm at a
// level a mode's row leaves out (level 1 and level 3 are off outside critical).
func armLevels(level task.ABSlot, model string) config.ReviewLevels {
	block := &config.LevelSetting{Model: model}
	switch level {
	case task.ABSlotL1:
		return config.ReviewLevels{L1: block}
	case task.ABSlotL3:
		return config.ReviewLevels{L3: block}
	default:
		return config.ReviewLevels{L2: block}
	}
}

// armProviders is the owner's route for the challenger, opened to this
// repository's code: the same shape the launcher resolves out of a project
// config (F18).
func armProviders() map[string]config.ModelProvider {
	return map[string]config.ModelProvider{
		"openrouter": {
			Kind: "anthropic-compat", BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{armRepo},
		},
	}
}

// armPrices is the launch's price table, built the way the launcher builds one
// (priceTable): the shipped catalog priced over the launch's own routes, which
// is what makes the challenger an admitted model rather than an unpriced one.
func armPrices(t *testing.T) *pricing.Table {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	table, err := config.PriceTable(armProviders(), nil, cat, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return table
}

// arm is the reviewer arm's answer for the fixture's champion, with the
// workflow's own ask set to level.
func (f *armFixture) arm(level task.ABSlot) (*task.Spec, string, error) {
	f.t.Helper()
	return ReviewerArm(context.Background(), f.plannedRun(level), f.champion())
}

// championRecord is the champion's record as its job leaves one: finished, cut
// from armStartSHA, with the patch object it uploaded, and carrying the slot
// its pair varies — the launcher writes it on both arms (B1.1), and the empty
// slot is the coder pair A3.3 launches.
func championRecord(store *runstore.Store, slot task.ABSlot) runstore.Record {
	return runstore.Record{
		Version: 1, RunID: armChampion, Repo: armRepo,
		Status: runstore.StatusSucceeded, StartSHA: armStartSHA,
		AB:          &task.AB{PairID: armPairID, Arm: task.ABArmChampion, Slot: slot},
		PatchObject: runstore.PatchKey(store.Prefix()),
	}
}

// otherPatchKey is the patch key of a run that is not the champion: the key a
// record would name if it had been written against another run's prefix.
func (f *armFixture) otherPatchKey(runID string) string {
	return runstore.PatchKey(runstore.Open(f.bucket.Bucket, f.slug, runID).Prefix())
}

// armChampionTask is the task the champion ran, pair block and all: the arm is
// the same task with the arm's own two differences, and the branch it names is
// not the commit the arm is cut at.
func armChampionTask() *task.Spec {
	return &task.Spec{
		Version: 1, RunID: armChampion, Repo: armRepo, Ref: armChampionRef,
		Workflow: armWorkflow, Task: "Add a feature", Batch: "ab-" + armPairID,
		AB: &task.AB{PairID: armPairID, Arm: task.ABArmChampion},
	}
}

// TestABReviewerArmLaunchesAfterChampionPatch: a reviewer arm exists only once
// the champion's patch does. A champion that is still running, has written no
// patch, or recorded one that was cut at the object cap is answered with the
// closed reason and no task at all — and the same launch, asked again over the
// champion's patch, answers the arm: one review of the champion's own diff on
// the layer's challenger, publishing nothing.
func TestABReviewerArmLaunchesAfterChampionPatch(t *testing.T) {
	f := newArmFixture(t)
	rec := championRecord(f.store(), task.ABSlotL2)

	t.Run("the champion is still running", func(t *testing.T) {
		running := rec
		running.Status = runstore.StatusRunning
		running.PatchObject = ""
		f.seed(running)
		arm, why, err := f.arm(task.ABSlotL2)
		if err != nil || why != ReasonChampionRunning || arm != nil {
			t.Fatalf("the arm of a champion that has not finished: spec = %v, why = %q, err = %v, want %q and no task", arm, why, err, ReasonChampionRunning)
		}
	})

	t.Run("the champion recorded no patch", func(t *testing.T) {
		none := rec
		none.PatchObject = ""
		f.seed(none)
		arm, why, err := f.arm(task.ABSlotL2)
		if err != nil || why != ReasonNoChampionPatch || arm != nil {
			t.Fatalf("the arm of a champion with no patch: spec = %v, why = %q, err = %v, want %q and no task", arm, why, err, ReasonNoChampionPatch)
		}
	})

	t.Run("the champion names a patch the bucket does not hold", func(t *testing.T) {
		f.seed(rec)
		arm, why, err := f.arm(task.ABSlotL2)
		if err != nil || why != ReasonNoChampionPatch || arm != nil {
			t.Fatalf("the arm of a champion whose patch object is absent: spec = %v, why = %q, err = %v, want %q and no task", arm, why, err, ReasonNoChampionPatch)
		}
	})

	// The arm's read is pinned to the champion's own key, which is the key the
	// record has to name (runstore.PatchKey): the champion's patch object is
	// written here as well, so this case is the record's spelling and not a
	// missing object, and a guard that let it through would replay a diff
	// another run's prefix holds.
	t.Run("the champion names another run's patch key", func(t *testing.T) {
		foreign := rec
		foreign.PatchObject = f.otherPatchKey("20261109-090000-bbbb")
		f.seed(foreign)
		f.putPatch(armDiff)
		arm, why, err := f.arm(task.ABSlotL2)
		if err == nil || arm != nil || why != "" {
			t.Fatalf("the arm of a champion whose record names %q: spec = %v, why = %q, err = %v, want a refusal", foreign.PatchObject, arm, why, err)
		}
		if !strings.Contains(err.Error(), armChampion) {
			t.Errorf("the refusal %q does not name the champion whose record it judged", err)
		}
	})

	t.Run("the champion's patch was cut at the cap", func(t *testing.T) {
		cut := rec
		cut.PatchTruncated = true
		f.seed(cut)
		f.putPatch(armDiff)
		arm, why, err := f.arm(task.ABSlotL2)
		if err != nil || why != ReasonPatchTruncated || arm != nil {
			t.Fatalf("the arm of a champion whose patch is half stored: spec = %v, why = %q, err = %v, want %q and no task", arm, why, err, ReasonPatchTruncated)
		}
	})

	t.Run("the champion's patch is there", func(t *testing.T) {
		f.seed(rec)
		f.putPatch(armDiff)
		arm, why, err := f.arm(task.ABSlotL2)
		if err != nil || why != "" {
			t.Fatalf("the arm of a finished champion: why = %q, err = %v", why, err)
		}
		if arm == nil {
			t.Fatal("a finished champion with a patch answered no arm")
		}
		if arm.AB == nil {
			t.Fatalf("the arm's task carries no pair block: %+v", arm)
		}
		if arm.AB.Arm != task.ABArmChallenger || arm.AB.Publish != task.ABPublishNone {
			t.Errorf("the arm's block is %+v, want a challenger that publishes nothing", arm.AB)
		}
		if arm.AB.Slot != task.ABSlotL2 || arm.AB.ChampionRun != armChampion {
			t.Errorf("the arm's block is %+v, want slot %s over %s", arm.AB, task.ABSlotL2, armChampion)
		}
		if arm.AB.Challenger != abLaunchChallenger || arm.AB.ChallengerSource != task.ABChallengerOwner {
			t.Errorf("the arm's challenger is %q from %q, want the layer's %q from the owner", arm.AB.Challenger, arm.AB.ChallengerSource, abLaunchChallenger)
		}
		if arm.AB.PairID != armPairID {
			t.Errorf("the arm joined pair %q, want the champion's %q", arm.AB.PairID, armPairID)
		}
		if arm.RunID == armChampion || !task.ValidRunID(arm.RunID) {
			t.Errorf("the arm's run id is %q, want one of its own", arm.RunID)
		}
		if err := arm.Validate(); err != nil {
			t.Errorf("the arm's task is not one the runner would read: %v", err)
		}
	})

	t.Run("a coder slot is not a deferred arm", func(t *testing.T) {
		f.seed(rec)
		f.putPatch(armDiff)
		for _, slot := range []task.ABSlot{"", task.ABSlotCoder} {
			arm, why, err := f.arm(slot)
			if err == nil || arm != nil || why != "" {
				t.Fatalf("an arm asked for slot %q: spec = %v, why = %q, err = %v, want a refusal", slot, arm, why, err)
			}
		}
	})
}

// patchOfSize is a diff of exactly n bytes. The arm reads the object and never
// its content, so the bytes only have to be that many — what the size is for is
// the cap the read is made under.
func patchOfSize(n int) string {
	head := "diff --git a/one.txt b/one.txt\n"
	if n <= len(head) {
		return head[:n]
	}
	rest := n - len(head)
	return head + strings.Repeat("+x\n", rest/3) + strings.Repeat("-", rest%3)
}

// TestABReviewerArmReadsAPatchTheRunnerStoredWhole: the read is capped at the
// size the runner stores a patch under (runner.MaxPatchBytes, 2 MiB), not at
// runstore.MaxRecordBytes (1 MiB, the cap for a run's JSON objects), because the
// champion's record is what says whether the patch is complete: a patch the
// runner stored whole in the band between the two caps is one the pair is owed,
// and refusing it for being read would drop a pair the runner considers
// comparable. An object past the runner's own cap is refused, since nothing the
// runner stores is that large and its record would be claiming a completeness
// the object contradicts.
func TestABReviewerArmReadsAPatchTheRunnerStoredWhole(t *testing.T) {
	f := newArmFixture(t)
	for _, tc := range []struct {
		name string
		size int
		want bool
	}{
		{"a patch just over the record cap", 1<<20 + 1, true},
		{"a patch at the runner's own cap", 2 << 20, true},
		{"a patch past the runner's own cap", 2<<20 + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.seed(championRecord(f.store(), task.ABSlotL2))
			f.putPatch(patchOfSize(tc.size))
			arm, why, err := f.arm(task.ABSlotL2)
			if tc.want {
				if err != nil || why != "" || arm == nil {
					t.Fatalf("an arm over a %d-byte patch: spec = %v, why = %q, err = %v, want the arm", tc.size, arm, why, err)
				}
				return
			}
			if err == nil || arm != nil || why != "" {
				t.Fatalf("an arm over a %d-byte patch: spec = %v, why = %q, err = %v, want a refusal", tc.size, arm, why, err)
			}
		})
	}
}

// TestABReviewerArmRefusesAnotherLevelsChampion: the arm joins the champion's
// pair, and a pair varies one slot (B1.1). A champion whose own block names
// another level, and one whose block names none — the coder pair A3.3
// launches — are refused rather than joined, because two levels' arms under one
// pair id are what B1.3 would read as a single pair and score together; the
// same champion answers an arm at the level it does vary, which is what makes
// this a mismatch and not a refusal of review slots.
func TestABReviewerArmRefusesAnotherLevelsChampion(t *testing.T) {
	f := newArmFixture(t)
	f.putPatch(armDiff)
	for _, tc := range []struct {
		name      string
		varied    task.ABSlot
		asked     task.ABSlot
		championN string
	}{
		{"the champion's pair varies another level", task.ABSlotL1, task.ABSlotL2, "l1"},
		{"the champion's pair varies no slot", "", task.ABSlotL2, "coder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.seed(championRecord(f.store(), tc.varied))
			arm, why, err := f.arm(tc.asked)
			if err == nil || arm != nil || why != "" {
				t.Fatalf("an arm at %s over a champion that varies %q: spec = %v, why = %q, err = %v, want a refusal", tc.asked, tc.varied, arm, why, err)
			}
			if !strings.Contains(err.Error(), tc.championN) {
				t.Errorf("the refusal %q does not name the %s slot the champion's own block carries", err, tc.championN)
			}
		})
	}

	t.Run("the same champion answers its own level", func(t *testing.T) {
		f.seed(championRecord(f.store(), task.ABSlotL1))
		arm, why, err := f.arm(task.ABSlotL1)
		if err != nil || why != "" || arm == nil {
			t.Fatalf("an arm at l1 over a champion that varies l1: spec = %v, why = %q, err = %v", arm, why, err)
		}
		if arm.AB.Slot != task.ABSlotL1 {
			t.Errorf("the arm's slot is %q, want %q", arm.AB.Slot, task.ABSlotL1)
		}
	})
}

// TestABReviewerArmUsesChampionStartSHA: the arm is the champion's own task cut
// at the commit the champion's record says it started from, not at the ref that
// task names — the champion's branch may have moved on in the hours between the
// champion's launch and its patch — and nothing else about the task moves with
// it (D555, D571's one-commit rule).
func TestABReviewerArmUsesChampionStartSHA(t *testing.T) {
	f := newArmFixture(t)
	f.seed(championRecord(f.store(), task.ABSlotL3))
	f.putPatch(armDiff)
	arm, why, err := f.arm(task.ABSlotL3)
	if err != nil || why != "" || arm == nil {
		t.Fatalf("the arm: spec = %v, why = %q, err = %v", arm, why, err)
	}
	champion := armChampionTask()
	if arm.Ref != armStartSHA {
		t.Errorf("the arm was cut at %q, want the champion's recorded start %q", arm.Ref, armStartSHA)
	}
	if arm.Ref == champion.Ref {
		t.Errorf("the arm was cut at the champion's ref %q, which is the branch the record's start commit replaces", champion.Ref)
	}
	if arm.AB.Slot != task.ABSlotL3 {
		t.Errorf("the arm's slot is %q, want %q", arm.AB.Slot, task.ABSlotL3)
	}
	// Everything else has to match the champion's task, or the pair compares a
	// review of one commit with a review of another: the two are compared with
	// the fields the arm is allowed to differ in cleared.
	champion.RunID, arm.RunID = "", ""
	champion.Ref, arm.Ref = "", ""
	champion.AB, arm.AB = nil, nil
	want, err := json.Marshal(champion)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(arm)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("the arm's task differs from the champion's but for its run id, its ref and its block:\nchampion %s\narm      %s", want, got)
	}
}

// armWith is arm with the launch's own facts edited: the refusal paths of a
// launch whose configuration, task or champion is not what a reviewer arm needs
// are all one field away from the fixture's, and each is a caller's mistake
// answered with an error rather than a reason.
func (f *armFixture) armWith(level task.ABSlot, edit func(run *fcli.PlannedRun)) (*task.Spec, string, error) {
	f.t.Helper()
	run := f.plannedRun(level)
	edit(&run)
	return ReviewerArm(context.Background(), run, f.champion())
}

// TestABReviewerArmAsksThroughTheWorkflowsOwnKey: the ask is the workflow's key
// and never a caller's argument (the owner's ruling of 2026-11-16, O12), so a
// launch whose workflow asks for no arm — no block at all, or a key that names
// the coder — is refused rather than answered with an arm nobody asked for, and
// a launch whose task names a workflow the configuration does not define is
// refused too: there is no key to read the ask off.
func TestABReviewerArmAsksThroughTheWorkflowsOwnKey(t *testing.T) {
	f := newArmFixture(t)
	f.seed(championRecord(f.store(), task.ABSlotL2))
	f.putPatch(armDiff)
	for _, tc := range []struct {
		name string
		edit func(run *fcli.PlannedRun)
		want string
	}{
		{"a workflow with no ab block", func(run *fcli.PlannedRun) {
			run.Config.Workflows[armWorkflow] = config.Workflow{}
		}, "asks for no A/B reviewer arm"},
		{"a workflow asking for the coder", func(run *fcli.PlannedRun) {
			run.Config.Workflows[armWorkflow] = config.Workflow{AB: &config.WorkflowAB{ReviewerArm: "coder"}}
		}, "not a review level"},
		{"a task naming no workflow the file defines", func(run *fcli.PlannedRun) {
			run.Spec.Workflow = "api"
		}, "names no workflow this configuration defines"},
		{"a launch with no task at all", func(run *fcli.PlannedRun) {
			run.Spec = nil
		}, "no task"},
		{"a launch with no configuration", func(run *fcli.PlannedRun) {
			run.Config = nil
		}, "no configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arm, why, err := f.armWith(task.ABSlotL2, tc.edit)
			if err == nil || arm != nil || why != "" {
				t.Fatalf("spec = %v, why = %q, err = %v, want an error and no arm", arm, why, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal %q does not say %q", err, tc.want)
			}
		})
	}
}

// TestABReviewerArmRefusesARunThatIsNoPairsChampion: the arm replays the
// champion's own patch, so a run whose record names no champion arm — no pair
// block at all, or the challenger's own arm — is refused rather than replayed:
// cutting an arm over it would file a pair under a champion that never ran one
// (B1.1, B1.3).
func TestABReviewerArmRefusesARunThatIsNoPairsChampion(t *testing.T) {
	f := newArmFixture(t)
	f.putPatch(armDiff)
	for _, tc := range []struct {
		name  string
		block *task.AB
	}{
		{"a run that is no pair at all", nil},
		{"a challenger of a pair", &task.AB{PairID: armPairID, Arm: task.ABArmChallenger, Slot: task.ABSlotL2, ChampionRun: armChampion}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := championRecord(f.store(), task.ABSlotL2)
			rec.AB = tc.block
			f.seed(rec)
			arm, why, err := f.arm(task.ABSlotL2)
			if err == nil || arm != nil || why != "" {
				t.Fatalf("spec = %v, why = %q, err = %v, want an error and no arm", arm, why, err)
			}
			if !strings.Contains(err.Error(), "not the champion of a pair") || !strings.Contains(err.Error(), armChampion) {
				t.Errorf("the refusal %q does not name the run whose record it judged", err)
			}
		})
	}
}

// TestABReviewerArmRefusesACorruptChampionRecord: a result.json the bucket
// holds and this build cannot read is never read as "this run wrote none"
// (fcli.RunRecord.RecordCorrupt). The champion's readiness is the whole
// question the arm asks, and a record nobody can read is no answer to it: the
// launch is refused, not answered with ReasonNoChampionPatch, which would be
// this build reporting a champion's silence it cannot see.
func TestABReviewerArmRefusesACorruptChampionRecord(t *testing.T) {
	f := newArmFixture(t)
	f.seed(championRecord(f.store(), task.ABSlotL2))
	f.putPatch(armDiff)
	if err := f.store().PutFile(context.Background(), "result.json", []byte("{not json"), "application/json"); err != nil {
		t.Fatal(err)
	}
	champion := f.champion()
	if !champion.RecordCorrupt() || champion.Record() != nil {
		t.Fatalf("the fixture's champion reads as record = %+v, corrupt = %v, want an unreadable record", champion.Record(), champion.RecordCorrupt())
	}
	arm, why, err := f.arm(task.ABSlotL2)
	if err == nil || arm != nil || why != "" {
		t.Fatalf("spec = %v, why = %q, err = %v, want an error and no arm", arm, why, err)
	}
	if !strings.Contains(err.Error(), "cannot read") || !strings.Contains(err.Error(), armChampion) {
		t.Errorf("the refusal %q does not name the record it could not read", err)
	}
}

// TestABReviewerArmRefusesAChampionWithNoStartCommit: the arm is cut at the
// commit the champion's record names, and the patch was made against that
// commit (B1.2). A record that names no full SHA — an ending old enough to
// predate the field — answers the closed reason and never an arm cut at the
// task's own ref, which is the branch the champion's cut may have moved on from
// and which the diff was not written against.
func TestABReviewerArmRefusesAChampionWithNoStartCommit(t *testing.T) {
	f := newArmFixture(t)
	f.putPatch(armDiff)
	for _, start := range []string{"", armChampionRef, "9f2c1b0"} {
		t.Run("start "+start, func(t *testing.T) {
			rec := championRecord(f.store(), task.ABSlotL2)
			rec.StartSHA = start
			f.seed(rec)
			arm, why, err := f.arm(task.ABSlotL2)
			if err != nil || why != ReasonNoChampionStart || arm != nil {
				t.Fatalf("start %q: spec = %v, why = %q, err = %v, want %q and no task", start, arm, why, err, ReasonNoChampionStart)
			}
		})
	}
}

// TestABReviewerArmSkipsWhenNoChallengerIsAdmitted: a reviewer arm is still one
// of the owner's challengers and nobody else's (D556), so a launch nothing is
// admitted for answers a reason and no arm — the same answer a coder pair's
// launch gives, printed by the launcher's skip line.
func TestABReviewerArmSkipsWhenNoChallengerIsAdmitted(t *testing.T) {
	f := newArmFixture(t)
	f.seed(championRecord(f.store(), task.ABSlotL2))
	f.putPatch(armDiff)
	arm, why, err := f.armWith(task.ABSlotL2, func(run *fcli.PlannedRun) {
		run.Config.Layer = &config.ProjectLayer{AB: &config.AB{}}
	})
	if err != nil || why != ReasonNoChallenger || arm != nil {
		t.Fatalf("spec = %v, why = %q, err = %v, want %q and no task", arm, why, err, ReasonNoChallenger)
	}
}

// TestABReviewerArmJudgesTheSlotItVaries: a pair is two runs that differ in the
// one thing the slot varies, and for a review slot that is the model the level
// runs (the owner's ruling of 2026-11-16, O12). A challenger that is the
// level's own model varies nothing and is refused as champion-model, while a
// challenger that is the coder's model is admitted: the coder's model is what a
// coder pair varies, and judging this one against it would refuse the arm that
// varies the review — the whole point of the pair — and admit the one that
// repeats the champion's own review.
func TestABReviewerArmJudgesTheSlotItVaries(t *testing.T) {
	f := newArmFixture(t)
	f.seed(championRecord(f.store(), task.ABSlotL2))
	f.putPatch(armDiff)

	t.Run("a challenger that is the level's own model", func(t *testing.T) {
		arm, why, err := f.armWith(task.ABSlotL2, func(run *fcli.PlannedRun) {
			run.Config.Agent.Levels.L2 = &config.LevelSetting{Model: abLaunchChallenger}
		})
		if err != nil || why != ReasonChampionModel || arm != nil {
			t.Fatalf("spec = %v, why = %q, err = %v, want %q and no task", arm, why, err, ReasonChampionModel)
		}
	})

	t.Run("a challenger that is the coder's model", func(t *testing.T) {
		arm, why, err := f.armWith(task.ABSlotL2, func(run *fcli.PlannedRun) {
			run.Config.Agent.Models.Coder = abLaunchChallenger
		})
		if err != nil || why != "" || arm == nil {
			t.Fatalf("spec = %v, why = %q, err = %v, want the arm", arm, why, err)
		}
		if arm.AB.Challenger != abLaunchChallenger {
			t.Errorf("the arm's challenger is %q, want %q", arm.AB.Challenger, abLaunchChallenger)
		}
		if arm.AB.Slot != task.ABSlotL2 {
			t.Errorf("the arm's slot is %q, want %q", arm.AB.Slot, task.ABSlotL2)
		}
	})

	t.Run("a level the run does not run", func(t *testing.T) {
		// Level 3 is off in every mode but critical, and the champion's own
		// pair has to vary the level the workflow asks for, so the level off
		// is the one the pair is at.
		f.seed(championRecord(f.store(), task.ABSlotL3))
		arm, why, err := f.armWith(task.ABSlotL3, func(run *fcli.PlannedRun) {
			run.Config.Agent.Levels = config.ReviewLevels{}
		})
		if err == nil || arm != nil || why != "" {
			t.Fatalf("spec = %v, why = %q, err = %v, want an error and no arm", arm, why, err)
		}
		if !strings.Contains(err.Error(), string(task.ABSlotL3)+" level does not run") {
			t.Errorf("the refusal %q does not name the level that does not run", err)
		}
	})
}
