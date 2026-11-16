// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/blobx"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is the reviewer arm's launch (plan task B1.4, D728): the arm of a
// pair whose slot is a review level. A3.3's Plan answers a coder pair's two
// arms at one moment, from one launched task (D571), and that same-moment
// launch is the whole of what a coder pair needs. A review slot cannot be
// launched that way: its challenger is a shadow replay of that level over the
// champion's own recorded patch (B1.2), and that object is written by the
// champion's job at its finalize (uploadPatch), so the arm is asked for
// afterwards — and, since the branch it was cut from may have moved on by
// then, cut at the commit the champion's record says the champion started
// from rather than at a ref that resolves today.
//
// The workflow asks for the arm and this function reads the ask (O12): a file
// that writes workflows.<name>.ab.reviewer_arm asks for a second run at that
// level on every launch of that workflow, and the champion's own launch reads
// the same key at the job image's slot gate (internal/cli's slotAsk, the gate
// slotSince declares), so the ask is gated before any money is spent and this
// function cannot be asked for a level the file does not ask for.
//
// Nothing in this tree calls ReviewerArm yet. What a launcher would do with the
// answer — writing the task under the arm's run id and starting the run — is
// `fugaro run`'s own work (freshLaunchGates, createTask, launchRun) and no seam
// this package can reach exports them: launchRun is unexported, fcli.Cloud
// reads the runs bucket and writes nothing, a Pair is always two runs written
// and started at one moment, and the runner's step seam hands a step no
// launcher. So the deferred launch is core work that does not exist yet, and
// until it does nothing reads a finished champion's patch or writes a slot:
// what this file carries is the decision the launch asks for, and every read
// behind it is a read of the champion's own objects in the runs bucket.
//
// The level is asked for here and never drawn: Plan writes no slot on any task,
// so no launch of A3.3's can put a reviewer slot on the wire, and the launch
// that asks this one is the only one that writes ab.slot and ab.champion_run
// (B1.1).

// The closed reasons ReviewerArm answers with: one word per way a champion
// that is not ready to be replayed produces no arm, in Admit's and Plan's own
// vocabulary because the launcher prints the word and `fugaro run --json`
// carries it, and nobody should have to parse a sentence built from a run id.
const (
	// ReasonChampionRunning is a champion whose own run has not finished: the
	// patch object is written at finalize, so an arm asked for now would be an
	// arm asked for an object that is not there yet, and the champion's launch
	// must not be read as the pair being over.
	ReasonChampionRunning = "champion-running"
	// ReasonNoChampionPatch is a champion that finished with no patch under its
	// own prefix: the replay would review nothing, and the pair would be a
	// champion's findings beside an arm that read no code (D559).
	ReasonNoChampionPatch = "no-champion-patch"
	// ReasonPatchTruncated is a champion whose stored patch was cut at
	// MaxPatchBytes (A2.1): half a change cannot be replayed, and a judge run
	// comparing two reviews of a diff nobody has in full would score a pair
	// that was never one.
	ReasonPatchTruncated = "champion-patch-truncated"
	// ReasonNoChampionStart is a champion whose record names no start commit —
	// an ending old enough to predate the field: the arm is cut at that commit
	// and the patch was made against it, so a launch that fell back to a ref
	// would replay the champion's diff onto a tree the diff was not written
	// against, which git either refuses or applies to the wrong code.
	ReasonNoChampionStart = "no-champion-start"
)

// ReviewerArm answers the challenger arm of a pair whose slot is a review
// level: the arm's task, cut at the champion's recorded start commit and
// carrying ab.slot and ab.champion_run, or — with a nil task — the one closed
// reason this launch samples no arm.
//
// champion is the champion's record as every other reader of a run reads one
// (fcli.RunRecord): the arm's pair, run id and start commit are the record's
// own, so a launch cannot replay a patch of a run other than the one it names.
// The level is not an argument either: a launch asks for an arm through its
// workflow's own key (workflows.<name>.ab.reviewer_arm, config.WorkflowAB —
// the owner's ruling of 2026-11-16, O12) and never through a flag, so the ask
// and the image gate that judges whether a job's runner can carry the arm
// (internal/cli's slotAsk) read one value, and a caller cannot ask this
// function for a level its own file does not ask for. The level it does ask
// for is judged against the champion's own block (B1.1: the launcher writes
// the pair's slot on both arms, and an absent one means coder), so an arm is
// never cut at a level the champion's pair does not vary.
//
// An error is for a caller that asked for an arm no champion can carry (a
// workflow whose file asks for none or names no review level, a run that is
// not a pair's champion, a slot the champion's own block does not name, a
// patch recorded under a key that is not the run's own) or for a read whose
// absence leaves the decision unfounded. A reason is a champion that is simply
// not ready yet: the launcher prints it, and nothing about the champion's own
// run is affected, just as a coder pair's skip never is (D554).
func ReviewerArm(ctx context.Context, run fcli.PlannedRun, champion fcli.RunRecord) (*task.Spec, string, error) {
	slot, err := reviewerArmSlot(run)
	if err != nil {
		return nil, "", err
	}
	if run.Cloud == nil || run.Cloud.RunsBucket() == nil {
		return nil, "", errors.New("the reviewer arm was asked about a launch with no runs bucket to read the champion's patch in")
	}
	cfg := run.Config
	if cfg == nil {
		return nil, "", errors.New("the reviewer arm was asked about a launch with no configuration to judge the challenger on")
	}
	rec := champion.Record()
	switch {
	case rec == nil && champion.RecordCorrupt():
		// A result.json that is there and unreadable is never read as "this run
		// has not written one": the champion's readiness is the whole question
		// here, and a bucket nobody can read is no answer to it.
		return nil, "", fmt.Errorf("the champion %s has a record this build cannot read, so whether its patch exists is not known", champion.ID())
	case rec == nil:
		return nil, "", fmt.Errorf("the champion %s wrote no record, and the arm of a pair is launched over one that has", champion.ID())
	}
	if rec.AB == nil || rec.AB.Arm != task.ABArmChampion {
		return nil, "", fmt.Errorf("run %s is not the champion of a pair (its record names no champion arm), and a reviewer arm replays its own champion's patch", champion.ID())
	}
	// A pair varies one slot and the launcher writes it on both arms (B1.1,
	// TestABSlotVocabulary): a champion whose own block names another level — or
	// none, which is the coder pair A3.3 launches — cannot carry this arm, and
	// joining it anyway would file two levels' arms under one pair id for B1.3
	// to read as a single pair.
	if varied := rec.AB.EffectiveSlot(); varied != slot {
		return nil, "", fmt.Errorf("the champion %s is the champion of a pair that varies the %s slot, and a reviewer arm at %s cannot join it", champion.ID(), varied, slot)
	}
	switch {
	case rec.Status == runstore.StatusRunning:
		return nil, ReasonChampionRunning, nil
	case rec.PatchTruncated:
		return nil, ReasonPatchTruncated, nil
	case rec.PatchObject == "":
		return nil, ReasonNoChampionPatch, nil
	case !gitops.IsFullSHA(rec.StartSHA):
		return nil, ReasonNoChampionStart, nil
	}
	store := runstore.Open(run.Cloud.RunsBucket().Bucket, run.Slug, champion.ID())
	// The patch is read under the champion's own prefix and nowhere else, which
	// is the key the record itself names (runstore.PatchKey): an arm that
	// replayed a diff another run's prefix holds would be an arm of a pair
	// nobody sampled.
	want := runstore.PatchKey(store.Prefix())
	if rec.PatchObject != want {
		return nil, "", fmt.Errorf("the champion %s records its patch as %q, and its own patch object is %q", champion.ID(), rec.PatchObject, want)
	}
	// The read is capped at what the runner stores a patch under
	// (runner.MaxPatchBytes, 2 MiB) and takes the bucket directly rather than
	// Store.ReadFile: that reader's cap for a run's object is
	// runstore.MaxRecordBytes (blobx's 1 MiB), written for the run's JSON, so a
	// patch the runner stored whole in the band between the two caps would be
	// refused for being read — and its record, patch_truncated unset, is the
	// champion's own word that the object is complete, which is the arm a pair
	// is owed. The runner's own patch test and the coordinator's get_result
	// read the object the same way.
	if _, _, err := run.Cloud.RunsBucket().ReadMax(ctx, want, runner.MaxPatchBytes); err != nil {
		if errors.Is(err, blobx.ErrNotExist) {
			return nil, ReasonNoChampionPatch, nil
		}
		return nil, "", fcli.RemoteErr(fmt.Errorf("reading the champion %s's patch: %w", champion.ID(), err))
	}
	layer := abBlock(cfg)
	var pair config.AB
	if layer != nil {
		pair = *layer
	}
	// What this arm's challenger must not be is the model the slot it varies
	// runs, and that model is read off the same resolution the launch itself
	// read: the owner's list is still the owner's (D556) and every other check
	// is a coder pair's, but a reviewer pair varies the review's model and not
	// the coder's (O12).
	varied, err := reviewSlotModel(run, slot)
	if err != nil {
		return nil, "", err
	}
	judged := *cfg
	judged.Agent = launchAgent(*cfg)
	model, ok, why := admitAgainst(judged, pair, run.Repo, run.Policy, run.Providers, run.Prices, run.ServiceHeldKey, varied)
	if !ok {
		return nil, why, nil
	}
	championSpec, err := store.ReadTask(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil, "", fmt.Errorf("the champion %s wrote no task, and the arm of a pair is launched from the task its champion ran", champion.ID())
	case err != nil:
		return nil, "", fcli.RemoteErr(fmt.Errorf("reading the champion %s's task: %w", champion.ID(), err))
	}
	// The arm's block names the run the patch was read and judged under, which
	// is the prefix the record was read from and not the id the record spells:
	// the prefix is what the bucket holds, and it is the one B1.2's replay
	// looks the patch up by.
	block := &task.AB{
		PairID:           rec.AB.PairID,
		Arm:              task.ABArmChallenger,
		Publish:          task.ABPublishNone,
		Challenger:       model,
		ChallengerSource: task.ABChallengerOwner,
		Slot:             slot,
		ChampionRun:      champion.ID(),
	}
	arm, err := reviewerArmSpec(championSpec, rec.StartSHA, block)
	if err != nil {
		return nil, "", err
	}
	return arm, "", nil
}

// reviewerArmSlot is the level this launch asks a reviewer arm for: the
// workflow's own key (workflows.<name>.ab.reviewer_arm, config.WorkflowAB),
// read off the configuration the launch resolved and the task it would launch.
// A launch whose workflow asks for no arm has no slot to replay, and cutting
// one anyway would be this package sampling a pair the owner's file never
// asked for — the arm is a second paid run, so it is asked for in the file and
// never inferred.
func reviewerArmSlot(run fcli.PlannedRun) (task.ABSlot, error) {
	spec, cfg := run.Spec, run.Config
	switch {
	case cfg == nil:
		return "", errors.New("the reviewer arm was asked about a launch with no configuration to read its workflow's ask off")
	case spec == nil:
		return "", errors.New("the reviewer arm was asked about a launch with no task, and the ask for an arm is its workflow's own key")
	}
	name, wf, err := cfg.SelectWorkflow(spec.Workflow)
	if err != nil {
		return "", fmt.Errorf("the reviewer arm's launch names no workflow this configuration defines, so no key of it asks for an arm: %w", err)
	}
	lv := task.ABSlot(wf.ReviewerArm())
	switch {
	case lv == "":
		return "", fmt.Errorf("the workflow %s asks for no A/B reviewer arm: a launch asks for one with fugaro.yaml's workflows.%s.ab.reviewer_arm, and this launch is not one", name, name)
	case !lv.IsReviewer():
		return "", fmt.Errorf("the workflow %s asks for a reviewer arm at %q, which is not a review level: a reviewer arm replays l1, l2 or l3 over the champion's patch, and a coder pair is launched as a pair at the champion's own moment (D571, A3.3)", name, lv)
	}
	return lv, nil
}

// reviewSlotModel is the model the slot this arm varies runs: what the arm's
// challenger must not be, since a pair is two runs that differ in the one thing
// the slot varies (D555) and for a review slot that one thing is the review's
// model. The model is the launch's own resolution of its levels, with the same
// call and the same inputs the launcher's own level gates make: the merged
// configuration, the launch's rigor mode, the owner's floor for that mode, the
// task's own review_levels block and the recipe.
//
// A recipe that travels as a name alone (a repository recipe, which the task
// carries no text of) resolves here as no recipe at all, exactly as
// recipeMode's own read of it comes out empty: the recipe's role mapping is
// then not applied to the levels' default models, so a file that maps the
// reviewer role onto the coder's could admit a challenger that is the model
// the level runs. That is a wrong pair and never a wrong arm: the challenger
// is still a model the owner allows, priced and routed for this repository,
// and the runner refuses the arm at bootstrap if the level it replays is one
// its own resolution does not have.
func reviewSlotModel(run fcli.PlannedRun, slot task.ABSlot) (string, error) {
	spec, cfg := run.Spec, run.Config
	mode := launchedMode(cfg, spec)
	levels, _, err := config.ResolveLevels(config.LevelInputs{
		Config: cfg, Mode: mode, Floor: run.Policy.ReviewFloor(mode), Task: spec.ReviewLevels,
		Recipe: embeddedRecipe(spec), Providers: run.Providers,
	})
	if err != nil {
		return "", fmt.Errorf("the levels of the run this arm would join do not resolve, so the model the %s slot runs is not known: %w", slot, err)
	}
	var lv config.ResolvedLevel
	switch slot {
	case task.ABSlotL2:
		lv = levels.L2
	case task.ABSlotL3:
		lv = levels.L3
	default:
		lv = levels.L1
	}
	if !lv.On {
		return "", fmt.Errorf("the %s level does not run for this launch, so a pair at it would vary a review the champion's own run never made", slot)
	}
	return lv.Model, nil
}

// reviewerArmSpec is the arm's task: the champion's own stored task with the
// two differences D555 and B1.2 give a challenger arm — a run id of its own
// and the arm's ab block — and one field a deferred arm cannot inherit, the
// ref, which is the commit the champion's record says it started from.
//
// The champion's task is read from the bucket rather than rebuilt from the
// launch, because it is what the champion's own job read: the arm inherits the
// repository, the workflow, the recipe, the mode, the tier, the --path set and
// the pair's batch label with it, so the two arms of a review pair differ in
// the one thing the slot varies, exactly as a coder pair's do (D555,
// TestBothArmsSameCommit).
func reviewerArmSpec(champion *task.Spec, startSHA string, block *task.AB) (*task.Spec, error) {
	data, err := json.Marshal(champion)
	if err != nil {
		return nil, fmt.Errorf("copying the champion's task: %w", err)
	}
	arm := &task.Spec{}
	if err := json.Unmarshal(data, arm); err != nil {
		return nil, fmt.Errorf("copying the champion's task: %w", err)
	}
	id, err := freshRunID(champion.RunID)
	if err != nil {
		return nil, err
	}
	arm.RunID = id
	arm.Ref = startSHA
	arm.AB = block
	// The arm's task is judged by the task codec before it is answered, the way
	// a coder pair's two are (sampledPair): a task the runner would refuse at
	// bootstrap must cost this launch nothing but a refusal here.
	if err := arm.Validate(); err != nil {
		return nil, fmt.Errorf("the reviewer arm's task is not one: %w", err)
	}
	return arm, nil
}
