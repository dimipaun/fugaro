// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
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
// What the launcher does with the answer — writing the task under the arm's
// run id and starting it, and asking the pair's own image gate before it
// writes a slot at all — is the core's, exactly as it is for a coder pair's
// challenger (launchChallenger, pairImageWhy). This file answers the arm's
// task, or the one closed word why there is none to launch, and every read
// behind that answer is a read of the champion's own objects in the runs
// bucket.
//
// The slot is asked for here and never drawn: Plan writes no slot on any task,
// so no launch of A3.3's can put a reviewer slot on the wire, and this is the
// only launch that writes ab.slot and ab.champion_run (B1.1).

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
// slot is the level the pair varies. The arm's model is deliberately not an
// argument: the owner names the challengers (D556) and Admit picks one exactly
// as it picks a coder pair's, judged for this repository by the same rules.
//
// An error is for a caller that asked for an arm no champion can carry (a
// coder slot, a run that is not a pair's champion, a patch recorded under a key
// that is not the run's own) or for a read whose absence leaves the decision
// unfounded. A reason is a champion that is simply not ready yet: the
// launcher prints it, and nothing about the champion's own run is affected,
// just as a coder pair's skip never is (D554).
func ReviewerArm(ctx context.Context, run fcli.PlannedRun, champion fcli.RunRecord, slot task.ABSlot) (*task.Spec, string, error) {
	if !slot.IsReviewer() {
		return nil, "", fmt.Errorf("a deferred arm varies a review slot, and %q is not one: a coder pair is launched as a pair at the champion's own moment (D571, A3.3)", slot)
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
	if want := runstore.PatchKey(store.Prefix()); rec.PatchObject != want {
		return nil, "", fmt.Errorf("the champion %s records its patch as %q, and its own patch object is %q", champion.ID(), rec.PatchObject, want)
	}
	if _, err := store.ReadFile(ctx, runstore.PatchName); err != nil {
		if errors.Is(err, runstore.ErrNotFound) {
			return nil, ReasonNoChampionPatch, nil
		}
		return nil, "", fcli.RemoteErr(fmt.Errorf("reading the champion %s's patch: %w", champion.ID(), err))
	}
	layer := abBlock(cfg)
	var pair config.AB
	if layer != nil {
		pair = *layer
	}
	judged := *cfg
	judged.Agent = launchAgent(*cfg)
	model, ok, why := Admit(judged, pair, run.Repo, run.Policy, run.Providers, run.Prices, run.ServiceHeldKey)
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
