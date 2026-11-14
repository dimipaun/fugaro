// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"context"
	"encoding/json"
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
// the route that serves it opened to this repository, and the budget mode a
// provider-served challenger needs (F18).
func (f *armFixture) plannedRun() fcli.PlannedRun {
	return fcli.PlannedRun{
		Cloud: f.cloud, Slug: f.slug, Repo: armRepo,
		Config: &config.Config{
			Git:   config.Git{Provider: gitprov.KindGitHub},
			Agent: config.Agent{Auth: "api-key", Models: config.ModelRoles{Coder: abLaunchChampion}},
			Layer: &config.ProjectLayer{AB: &config.AB{Challengers: []string{abLaunchChallenger}}},
		},
		Policy:    policy.Effective{Mode: policy.ModeEnforce},
		Providers: armProviders(),
		Prices:    armPrices(f.t),
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

// arm is the reviewer arm's answer for the fixture's champion.
func (f *armFixture) arm(slot task.ABSlot) (*task.Spec, string, error) {
	f.t.Helper()
	return ReviewerArm(context.Background(), f.plannedRun(), f.champion(), slot)
}

// championRecord is the champion's record as its job leaves one: finished, cut
// from armStartSHA, with the patch object it uploaded.
func championRecord(store *runstore.Store) runstore.Record {
	return runstore.Record{
		Version: 1, RunID: armChampion, Repo: armRepo,
		Status: runstore.StatusSucceeded, StartSHA: armStartSHA,
		AB:          &task.AB{PairID: armPairID, Arm: task.ABArmChampion},
		PatchObject: runstore.PatchKey(store.Prefix()),
	}
}

// armChampionTask is the task the champion ran, pair block and all: the arm is
// the same task with the arm's own two differences, and the branch it names is
// not the commit the arm is cut at.
func armChampionTask() *task.Spec {
	return &task.Spec{
		Version: 1, RunID: armChampion, Repo: armRepo, Ref: armChampionRef,
		Task: "Add a feature", Batch: "ab-" + armPairID,
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
	rec := championRecord(f.store())

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

// TestABReviewerArmUsesChampionStartSHA: the arm is the champion's own task cut
// at the commit the champion's record says it started from, not at the ref that
// task names — the champion's branch may have moved on in the hours between the
// champion's launch and its patch — and nothing else about the task moves with
// it (D555, D571's one-commit rule).
func TestABReviewerArmUsesChampionStartSHA(t *testing.T) {
	f := newArmFixture(t)
	f.seed(championRecord(f.store()))
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
