package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The reviewer replay (docs/review-levels-ab-jobmax, B1.2; D728): a pair at a
// review slot does not run the task a second time. Its challenger is a shadow
// arm (shadow.go) whose whole work is to read the champion's recorded patch and
// review it at one level with the challenger model, so both reviewers of the
// pair read one diff and a finding the challenger missed is the reviewer's and
// not a second coder's code.
//
// Three things about that patch are why this file exists rather than a call
// into the ordinary loop:
//
//   - It is untrusted. Any launcher, and the champion's own job agent, can write
//     that object (docs/bucket-iam/spec.md §10 L3), so the arm reads it
//     bounded at the size the runner stores one at, refuses the paths that would
//     make it configuration of this run — a hook, an instruction file, a tool
//     setting — and applies it with git apply, which runs no hook and no script.
//   - It is the only thing the arm reads of the champion. One object under the
//     champion's own prefix: never its record, its sessions or its transcripts,
//     and nothing of the champion's run is trusted to say what to review.
//   - It is all the arm runs. One review round at the slot's level, then
//     finalize: no implement, no fix, no test and no push — a challenger
//     publishes nothing at all (D554), and the pair's signals are its two
//     reviews (B1.3).
//
// A replay that cannot be run is refused as the arm's own boundary (refuseReplay
// below), never as infrastructure: the class is config_invalid, the way every
// other refusal of an arm's task is recorded (refuseArm, shadow.go).

// replayLevels is the review level each slot of the pair block varies (D728):
// the one place the task's own vocabulary (task.ABSlots) meets the runner's
// levels. A slot the table has no entry for is a pair this runner cannot run,
// and applyChallenger refuses it rather than reading it as a coder pair.
var replayLevels = map[task.ABSlot]policy.Level{
	task.ABSlotL1: policy.LevelL1,
	task.ABSlotL2: policy.LevelL2,
	task.ABSlotL3: policy.LevelL3,
}

// replayTier is the record tier a level's review is written under: the three
// words the ladder uses for the in-run levels (runstore's tiers), so a replay's
// round reads as the round of that level and not as a tier of its own.
func replayTier(level policy.Level) string {
	switch level {
	case policy.LevelL1:
		return runstore.TierFirst
	case policy.LevelL2:
		return runstore.TierSenior
	default:
		return runstore.TierAdversarial
	}
}

// replayStage is the stage a level's review runs under (reviewStages): the name
// the record carries and the transcript is stored under, so a replay's round is
// filed exactly as the champion's own round at that level is, which is what the
// judge matches the two finding sets by (B1.3). The level always has one — it
// came out of replayLevels — and a level that somehow had none is refused
// rather than filed under a stage the run never opened.
func replayStage(level policy.Level) (reviewStage, bool) {
	for _, s := range reviewStages {
		if s.level == level {
			return s, true
		}
	}
	return reviewStage{}, false
}

// replayPrompt is the level's review prompt: the brief the run built for its own
// review (ReviewPrompt, over the range the champion's patch is) under the
// heading that level reads it with — level 3's adversarial preamble, levels 1
// and 2 the ordinary brief with their own focus quoted (reviewPromptWithFocus,
// AdversarialPrompt). It is the loop's own rule, spelled here for the level the
// arm replays: the pair is only a comparison if the two reviewers were asked the
// same question about the same diff.
func (r *run) replayPrompt(reviewPrompt string, level policy.Level) string {
	if level == policy.LevelL3 {
		return AdversarialPrompt(reviewPrompt, r.levelFocus(level))
	}
	return reviewPromptWithFocus(reviewPrompt, r.levelFocus(level))
}

// applyReplayLevel writes the arm's challenger model into the slot level's own
// block, once the policy has resolved which levels run (bootstrap, after
// resolvePolicy and before checkBudget). The level must be one the run runs: a
// replay is that level's review, and a run whose mode, recipe or floor does not
// run the level has no stage to replay it in — a refusal rather than a forcing,
// because what the arm asks for is a comparison at a level the champion's own
// run did not run either, which is no comparison at all.
//
// The challenger is routed here (routeModel) and not merely written: this is
// the one pin of the run that lands after the switch has run, because the
// levels the switch rewrites are also what say whether there is a stage to
// replay in at all (bootstrap calls this after resolvePolicy). A challenger the
// project routes and that kept the canonical ID would leave the owner's route:
// the level's pin would name a model no route claims while every other pin
// names the route's own ID, and the arm's review calls would go direct — the
// same outcome, one write later, that TestRoutingRoutesALevelsOwnModel pins for
// a level's own model (TestABReplayRoutesTheChallenger).
func (r *run) applyReplayLevel() error {
	if !levelOn(r.levels, r.replay) {
		return r.refuseArm(fmt.Sprintf("this task replays review level %s, and the run it carries does not run that level: a reviewer arm's one review is the slot level's, and which levels run is the run's own mode, recipe, policy and task (resolveLevels). Launch the arm against a run whose levels include the slot's", r.replay))
	}
	model, err := r.routeModel(r.spec.AB.Challenger)
	if err != nil {
		return err
	}
	r.setLevelModel(r.replay, model)
	return nil
}

// runReviewReplay is the arm's whole run: read the champion's patch, fence it,
// apply it, review it at the slot's level, and let finalize record the arm.
func (r *run) runReviewReplay(ctx context.Context) {
	stage, ok := replayStage(r.replay)
	if !ok {
		r.refuseReplay(fmt.Sprintf("this task replays review level %s, which is not a level this runner runs a stage for: a pair at a review slot replays one of the three levels the loop itself runs (%s, %s, %s)", r.replay, policy.LevelL1, policy.LevelL2, policy.LevelL3))
		return
	}
	patch, err := r.readChampionPatch(ctx)
	var input replayInputError
	switch {
	case err == nil:
	case errors.As(err, &input):
		r.refuseReplay(input.reason)
		return
	default:
		r.failReplayRead(ctx, err)
		return
	}
	// The component is bytes the patch itself spelled -- a C-quoted header names
	// its path in escapes, newlines and all -- so it enters the reason the way a
	// stage's own error text does (stageErrorText: redacted, folded onto one
	// line, clipped), and not as the bytes were written: this reason reaches the
	// run record, the run log and the owner's watch output.
	if comp := replayPatchRefusal(patch); comp != "" {
		r.refuseReplay(r.replayFenceReason("touches " + r.stageErrorText(comp)))
		return
	}
	// The paths the patch names, read by git's own parser and before anything is
	// written: `git apply --numstat` reads the patch with the reader that will
	// apply it and names one path per entry, whatever spelling the patch's own
	// headers used (replayPatchPaths). It is what reaches a path no listing of
	// this checkout can name — one inside a submodule or a nested repository —
	// and it refuses the whole of one, because what a patch leaves under such a
	// path is written, is in the reviewer's reach for the whole round, and is
	// named by no listing git gives of this checkout at all.
	links, err := replayGitlinkPaths(ctx, r.repo)
	if err != nil {
		r.failReplayRead(ctx, fmt.Errorf("listing the submodules this checkout carries: %w", err))
		return
	}
	named, err := replayPatchPaths(ctx, r.repo, patch)
	if err != nil {
		// The reader that refuses it here is the one that would refuse it on the
		// apply, so this is the apply's own refusal: git's message quotes the
		// paths the patch carries, which are bytes out of it too (stageErrorText).
		r.refuseReplay(fmt.Sprintf("the champion run %s's patch does not apply to this checkout: %s. The arm applies the patch with git apply, which writes nothing when it refuses, so this is a patch written for another base: launch the arm at the commit the champion's run started from (B1.4)", r.spec.AB.ChampionRun, r.stageErrorText(err.Error())))
		return
	}
	if path := replayRefusedPath(named); path != "" {
		r.refuseReplay(r.replayFenceReason("touches " + r.stageErrorText(path)))
		return
	}
	if path := replayUnlistedPath(links, named); path != "" {
		r.refuseReplay(r.replayUnlistedReason("writes " + r.stageErrorText(path)))
		return
	}
	// What the checkout carried before the patch, read before anything is
	// applied: an image's baked checkout is not the base alone, and the fence
	// reads the listing after the apply against this one (replayWrittenPaths).
	before, err := replayCheckoutSnapshot(ctx, r.repo)
	if err != nil {
		r.failReplayRead(ctx, fmt.Errorf("listing the paths this checkout already carried: %w", err))
		return
	}
	if err := replayApply(ctx, r.repo, patch); err != nil {
		// git's own message quotes the paths the patch carries, so it is bytes
		// out of the patch too and gets the same treatment.
		r.refuseReplay(fmt.Sprintf("the champion run %s's patch does not apply to this checkout: %s. The arm applies the patch with git apply, which writes nothing when it refuses, so this is a patch written for another base: launch the arm at the commit the champion's run started from (B1.4)", r.spec.AB.ChampionRun, r.stageErrorText(err.Error())))
		return
	}
	// The fence's second reading, and the one that decides: the paths git just
	// wrote, listed from git itself rather than from the patch's own lines, and
	// the ones the checkout already carried read out of it. A patch that reaches
	// a run's own configuration is refused here even when no header the first
	// reading holds names it (replayWrittenPaths).
	written, err := replayWrittenPaths(ctx, r.repo, before)
	if err != nil {
		r.failReplayRead(ctx, fmt.Errorf("listing the paths the champion's patch wrote: %w", err))
		return
	}
	// The paths git wrote, and the ones it names as a submodule entry or as a
	// directory it will not descend into: what a patch wrote under either of
	// those is named by no listing of this checkout, which is the one thing the
	// parser reading above cannot judge on its own.
	if path, unlisted := replayRefusedPath(written), replayUnlistedPath(links, written); path != "" || unlisted != "" {
		// The change is discarded before the arm is refused. What it applied is
		// what finalize would otherwise commit, record as this arm's own changed
		// paths and upload as this arm's own patch object, and an arm that
		// refused a patch may not carry it. HEAD is the arm's own base: the
		// patch was applied and never committed, so nothing moved it.
		if err := r.repo.ResetTo(ctx, "HEAD"); err != nil {
			r.failReplayRead(ctx, fmt.Errorf("discarding the champion's refused patch: %w", err))
			return
		}
		if path != "" {
			r.refuseReplay(r.replayFenceReason("writes " + r.stageErrorText(path)))
		} else {
			r.refuseReplay(r.replayUnlistedReason("writes " + r.stageErrorText(unlisted)))
		}
		return
	}
	committed, err := r.repo.CommitAll(ctx, "fugaro: the champion's patch (run "+r.spec.AB.ChampionRun+"), replayed")
	if err != nil {
		r.failReplayRead(ctx, fmt.Errorf("committing the champion's patch: %w", err))
		return
	}
	if !committed {
		r.refuseReplay(fmt.Sprintf("the champion run %s's patch changes nothing in this checkout, so this arm's reviewer would have no diff to read: a reviewer pair compares two readings of one change, and the champion's own record says why its run left none (no_commits, or a patch object nobody wrote a change into)", r.spec.AB.ChampionRun))
		return
	}
	// The range the review reads is the champion's patch and nothing else: the
	// arm's own commit is the range's base (narrowReviewRange), exactly as the
	// champion's own loop narrows its range to its own commits, and the prompt
	// and every round's input are built over it (reviewRev, reviewPromptWithInput).
	r.narrowReviewRange(ctx)
	// The session runs with a home of its own, as every review stage's does and
	// for the same reason (reviewmode.go's useReviewHome, D894(a)): the image's
	// home is Claude Code's user level, which is the one level the posture below
	// keeps (agent.ReviewSettingSources), so a settings file baked into the
	// image would otherwise configure the session that reads the champion's
	// patch. Made before the round, which is what builds the stage's settings
	// and environment out of the run's own.
	if err := r.useReviewHome(); err != nil {
		r.failReplayRead(ctx, err)
		return
	}
	reviewPrompt := ReviewPrompt(r.cfg.Agent.Review, r.reviewFile, r.reviewRev(ctx), r.reviewDiffSize(ctx), r.baseUnreadable(), r.reviewerPersona())
	// The task the champion's pull request was written for, on the prompt the
	// challenger's reviewer reads (RA2, R15): the pair is a comparison of two
	// readings of one change, and the champion's own round was handed the task
	// its run carried. A challenger's task is a copy of the champion's
	// (run.go's pair launch), so resolving it here asks the challenger the same
	// question — an `unmet` finding the champion's reviewer could make is one
	// this reviewer can make too.
	if block := r.reviewTaskBlock(ctx); block != "" {
		reviewPrompt += "\n\n" + block
	}
	prompt := r.replayPrompt(reviewPrompt, r.replay)
	// Read before the round, as the loops read it: the summary keeps the commit
	// this round read (D727).
	head := r.reviewedHead(ctx)
	v, parsed, ok := r.replayRound(ctx, stage.stage, prompt)
	if !ok {
		return
	}
	model, effort := r.reviewFacts(stage.stage)
	sum := runstore.ReviewSummary{Round: 1, Tier: replayTier(r.replay), Verdict: "none", Head: head, Model: model, Effort: effort, Against: r.inRunAgainst}
	if parsed {
		sum.Verdict, sum.Findings = v.Verdict, len(v.Findings)
	} else {
		// The round the champion's own level would have recorded as none is
		// recorded as none here too, and the arm carries on to finalize: the
		// pair's judge reads the findings that were answered, and a reviewer
		// that answered nothing is one finding set the judge has nothing in.
		r.d.Log.Warn("the replay's review round returned no parseable verdict after a retry", "level", string(r.replay))
	}
	r.rec.Reviews = append(r.rec.Reviews, sum)
	r.save(ctx)
}

// replayRound runs the level's one review round: the stage the level runs, with
// the retry the loop gives a round that answered nothing (noVerdictRetries) —
// the champion's own round at this level had that retry, and a pair whose two
// rounds were asked a different number of times is not the comparison D728 buys.
// It reports the verdict, whether one parsed, and whether the arm may go on: a
// failed stage ends it, exactly as it ends a loop.
func (r *run) replayRound(ctx context.Context, stage, prompt string) (Verdict, bool, bool) {
	for attempt := 1; attempt <= noVerdictRetries+1; attempt++ {
		if attempt > 1 && r.capReached() {
			return Verdict{}, false, false
		}
		// The stage's own posture, which a coding run's review rounds do not
		// take (reviewartifact.go's reviewStageRequest is the other place it is
		// spelled): the user level alone, which the review home leaves empty,
		// and no MCP configuration but the managed file's. The checkout the
		// reviewer reads is another run's patch, so neither the repository's own
		// settings nor an .mcp.json in it may configure this session (B1.2).
		req := agent.Request{Prompt: r.reviewPromptWithInput(ctx, prompt), SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema,
			SettingSources: agent.ReviewSettingSources, StrictMCPConfig: true}
		res, ok, _ := r.stage(ctx, stage, req, stageOpts{})
		// The review's own session is saved beside the work session, as every
		// review stage's is (C1.17, D138): an arm has no work session, so this
		// is the only one it saves, and nothing resumes it.
		r.noteReviewSession(req, res)
		r.countTokens(res)
		if !ok {
			return Verdict{}, false, false
		}
		if v, parsed := TryVerdict(res); parsed {
			return v, true, true
		}
		r.d.Log.Warn("the replay's review round produced no parseable verdict", "stage", stage, "attempt", attempt)
	}
	return Verdict{}, false, true
}

// replayInputError is a refusal of the arm's own input, told from a read that
// failed for a reason of the machinery's: the first is the arm's boundary and
// ends the run the way every arm refusal does, the second is infrastructure and
// ends it as infra_error (failReplayRead).
type replayInputError struct{ reason string }

func (e replayInputError) Error() string { return e.reason }

// readChampionPatch reads the champion's recorded patch and nothing else: the
// one object under the champion's own prefix, bounded at the size the runner
// stores a patch at (MaxPatchBytes, shadow.go), so an object larger than the
// writer's own cap is refused rather than read.
//
// The run ID is re-checked here although the task decoder refuses one that is
// not a run ID (task.Validate): this is where the value becomes an object key,
// and a key built from a launcher's string is what may not escape the prefix
// (runstore.Sibling builds the prefix from it without validating it).
func (r *run) readChampionPatch(ctx context.Context) ([]byte, error) {
	champion := r.spec.AB.ChampionRun
	if !task.ValidRunID(champion) {
		return nil, replayInputError{fmt.Sprintf("the task's ab.champion_run %q is not a run ID, so the patch this arm replays cannot be named: the champion's patch is read under the champion's own prefix, runs/<slug>/<run-id>/, and nothing else of the champion is read at all (B1.2)", champion)}
	}
	if r.d.Bucket == nil {
		return nil, replayInputError{fmt.Sprintf("this run has no runs bucket to read the champion run %s's patch from, and a reviewer arm replays that patch or nothing (B1.2)", champion)}
	}
	key := runstore.PatchKey(r.d.Store.Sibling(champion).Prefix())
	data, _, err := r.d.Bucket.ReadMax(ctx, key, MaxPatchBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, replayInputError{fmt.Sprintf("the champion run %s's patch is not in the runs bucket at %s: a reviewer arm replays the patch the champion's run stored, so a champion that has not ended, or whose patch object is gone, leaves this arm nothing to review (B1.2). Launch the reviewer arm once the champion's run has ended", champion, key)}
	case errors.Is(err, blobx.ErrTooLarge):
		return nil, replayInputError{fmt.Sprintf("the champion run %s's patch at %s is larger than the %d bytes a run's patch is stored at, so this arm may not read it: the runner writes a patch truncated at that size (MaxPatchBytes), and an object past it is not a patch this runner stored (B1.2)", champion, key, MaxPatchBytes)}
	case err != nil:
		return nil, fmt.Errorf("reading the champion run %s's patch at %s: %w", champion, key, err)
	}
	return data, nil
}

// failReplayRead ends the arm for a failure of the machinery rather than of its
// input: the read of the runs bucket, the local git of the checkout, or the home
// its review stage's session runs with (useReviewHome), stopped for a reason
// that is nobody's configuration. It is the ending the check gate's own
// infrastructure failures take (gate.go, failInfra): status infra_error, class
// infra_error, and no finalize, since there is nothing to record of an arm that
// never reached its review.
func (r *run) failReplayRead(ctx context.Context, err error) {
	reason := r.redact(err.Error())
	r.recordEnd(runstore.StatusInfraError, runstore.OutcomeNone, failure.InfraError, reason)
	r.skipPR = true
	r.storeEnded(ctx, nil)
	r.d.Log.Error("the reviewer replay could not reach the champion's patch", "err", reason)
}

// refuseReplay refuses the arm the way refuseArm does — failed, outcome none,
// config_invalid — and through the run's own ending rather than by writing the
// record here: a replay refused after bootstrap still has a finalize to run, and
// the shadow finalize is what writes the arm's ending, its changed paths and its
// own patch object (an empty one: nothing was applied). The reason quotes the
// champion's patch and git's own message about it, so it is redacted first --
// and the bytes it quotes out of the patch are put on one line and clipped
// before they get here (stageErrorText), because the object is untrusted.
func (r *run) refuseReplay(reason string) {
	reason = r.redact(reason)
	r.failWithClass(failure.ConfigInvalid, reason)
	r.d.Log.Warn("the reviewer replay was refused", "reason", reason)
}

// replayFenceReason is the refusal the arm's fences write when a patch reaches
// this run's own configuration: what was read (the component the patch's own
// headers named, or the path git wrote or named, each already through
// stageErrorText, because every one of them is bytes out of an untrusted object)
// and why such a patch may not be replayed. One wording for every reading, so a
// pair's owner reads one boundary rather than one per reading.
func (r *run) replayFenceReason(what string) string {
	return fmt.Sprintf("the champion run %s's patch %s, and a patch that reaches a hook, an instruction file or a tool setting may not be replayed: the review this arm runs would load it as configuration of its own session (B1.2, D728). %s", r.spec.AB.ChampionRun, what, replayFenceFix)
}

// replayUnlistedReason is the refusal the fence writes for a path git lists
// nothing under: a submodule's own path (a gitlink) and a nested repository
// (which git names as the directory alone). A patch may write anything below
// such a path and no listing of this checkout will name it, so the arm cannot
// say what the patch left there — and what it left there is in the checkout the
// review stage reads, for the whole round. It is the same boundary as the rest
// of the fence, stated for the one case in which no path under it can be read.
func (r *run) replayUnlistedReason(what string) string {
	return fmt.Sprintf("the champion run %s's patch %s, and a patch that writes inside a submodule or a nested repository may not be replayed: git lists nothing under such a path, so the arm cannot read what the patch left there and the review it runs would read a checkout the patch chose (B1.2, D728). %s", r.spec.AB.ChampionRun, what, replayFenceFix)
}

// replayFenceFix is the sentence every refusal of the arm ends with: what the
// pair's owner does about it.
const replayFenceFix = "Launch the pair against a champion whose patch touches none of .claude, .claude-plugin, .fugaro, .github, .git*, fugaro.yaml, CLAUDE.md, CLAUDE.local.md or .mcp.json, at any depth"

// replayRefusedNames are the path components a champion's patch may not touch
// (B1.2, D728): the files a review stage of this run would load as its own
// configuration, or read as this run's. The list is the repository's own hygiene
// list for an untrusted checkout — reviewmode.go's stripClaudeConfig removes
// .claude, .claude-plugin, CLAUDE.md, CLAUDE.local.md and .mcp.json at every
// depth — plus the three the plan names beside them: .fugaro (a recipe file),
// .github (a workflow, which no run may write) and fugaro.yaml (this run's own
// configuration, read from the champion's base before the patch is applied).
// ".git" is matched as a prefix rather than as a name, which covers .git,
// .gitattributes and .gitignore alike: a filter or an ignore rule the patch wrote
// would be honoured by the *later* git of this run — the commit just below, the
// diff the reviewer reads, the checkout finalize makes (gitops' own note on the
// stripped credential is about the same door).
var replayRefusedNames = []string{
	".claude", ".claude-plugin", ".fugaro", ".github",
	"fugaro.yaml", "CLAUDE.md", "CLAUDE.local.md", ".mcp.json",
}

// replayRefusesComponent reports whether a path component is one no patch may
// write.
//
// The comparison folds case, because the checkout the fence judges is not
// guaranteed to be the one the review stage reads it in: on a filesystem that
// does not distinguish case — macOS's default, Windows — the component .Claude
// and the component .claude are one path, so a patch spelling a refused name in
// another case writes the file the stage then loads as its own configuration,
// hooks included, while a byte-for-byte comparison refuses it in neither
// reading (the review of 2026-11-17). Folding refuses a patch that writes, say,
// Claude.md beside CLAUDE.md, and that is the safe side, as everywhere else in
// this fence: the arm loses a sample rather than run a review over a checkout
// whose configuration a patch chose.
func replayRefusesComponent(name string) bool {
	if len(name) >= len(".git") && strings.EqualFold(name[:len(".git")], ".git") {
		return true
	}
	for _, refused := range replayRefusedNames {
		if strings.EqualFold(name, refused) {
			return true
		}
	}
	return false
}

// replayPatchPrefixes are the two header lines that name a path in a git diff,
// in both of git's spellings: the extended header of a rename, a copy or a mode
// change ("rename from x") and the ---/+++ pair of an ordinary change ("--- a/x").
// A path is read out of a line that starts with one of these.
var replayPatchPrefixes = []string{"rename from ", "rename to ", "copy from ", "copy to ", "--- ", "+++ ", "diff --git "}

// replayPatchRefusal is the path component of patch that no reviewer arm may
// replay, "" when the patch touches none of them.
//
// It is the fence's first reading, and not the one that decides: it reads the
// patch's own header lines, and a patch is free to spell a header git applies
// and this list does not hold. What decides is the listing of the paths git
// wrote, taken from git itself once the patch is applied (replayWrittenPaths).
// It is also the cheapest of the three readings — the patch's bytes and nothing
// else, no git call at all — which is why it is the one that comes first.
//
// The paths are read from the patch's own header lines rather than from git
// (git apply --numstat names a rename's new path alone, so a rename away from a
// refused path would escape the fence, and the parser reading beside this one
// names no other), and each is read as git's own reader
// reads it rather than as the line's bytes are written: git writes a path whose
// bytes need escaping — a space, a control character, a byte outside ASCII — as
// a C-quoted string, and decodes that string when it applies the patch, so the
// name git writes is the name the fence judges. "b/\056claude/settings.json" is
// applied as .claude/settings.json; read as bytes it holds no refused component
// at all. A hunk line can never be read as a header — its first byte is a
// space, a "+", a "-" or a backslash — and the one line that can look like one
// (a removed line whose own text starts with "--- ") can only add a refusal,
// never hide one.
func replayPatchRefusal(patch []byte) string {
	for line := range strings.Lines(string(patch)) {
		for _, prefix := range replayPatchPrefixes {
			rest, ok := strings.CutPrefix(line, prefix)
			if !ok {
				continue
			}
			for _, name := range replayHeaderNames(rest) {
				for _, comp := range strings.Split(name, "/") {
					if replayRefusesComponent(comp) {
						return comp
					}
				}
			}
			break
		}
	}
	return ""
}

// replayPatchPaths are the paths the patch names, read by git's own parser
// before anything is written: `git apply --numstat -z` reads the patch with the
// reader that will apply it, one record per entry, and -z leaves each path's own
// bytes alone. --numstat applies nothing and runs nothing of the patch's, which
// is what makes it the reading the fence can take before the apply.
//
// It is the fence's reading of git's parser, and the one neither of the other
// two can stand in for. The header reading is a reading of the patch's lines and
// misses a spelling git applies (the review of 2026-11-16), and the listing of
// the paths git wrote stops at a submodule or a nested repository: a file a
// patch puts below either is written, is in the reviewer's checkout for the
// whole round, and is named by no listing git gives of it (the review of
// 2026-11-17).
//
// A patch this reader cannot parse is a patch git apply refuses, so its failure
// is the apply's own refusal rather than one of the fence's, and the caller
// reads it as that.
func replayPatchPaths(ctx context.Context, repo *gitops.Repo, patch []byte) ([]string, error) {
	out, err := replayGit(ctx, repo, bytes.NewReader(patch), "apply", "--numstat", "-z")
	if err != nil {
		return nil, err
	}
	return replayNumstatPaths(out), nil
}

// replayNumstatPaths are the paths of a `git apply --numstat -z` listing: one
// record per NUL, each the added and the deleted line count and then the path,
// tab-separated and never quoted (-z), so a path holding a newline or a quote is
// read as itself. A rename names its new path alone — the path git writes, and
// the one the fence judges — while the path it renames away from is a deletion
// of this checkout, which the patch's own headers name (replayPatchRefusal).
func replayNumstatPaths(out string) []string {
	var paths []string
	for _, rec := range strings.Split(out, "\x00") {
		if _, rest, ok := strings.Cut(rec, "\t"); ok {
			if _, path, ok := strings.Cut(rest, "\t"); ok && path != "" {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

// replayWrittenPaths lists every path the patch put in the checkout: the ones a
// commit would now carry, and the ones no commit carries at all, each of them
// read against the state the checkout had before the patch.
//
// The first half stages the checkout and lists what that commit would hold: a
// rename read as the delete and the add it is (--no-renames), NUL-separated so a
// path holding a newline survives the listing. Staging is `git add -A`, the call
// the arm's own commit makes, so the listing is exactly what that commit would
// carry and nothing beside it.
//
// The second half is why this is not that listing alone. `git add -A` honours
// the checkout's ignore rules, so a path the base's own .gitignore already
// excludes — CLAUDE.local.md and .claude/settings.local.json are the two names
// git's own advice keeps out of a repository, and a champion's patch can write
// either (the review of 2026-11-16) — is written by the patch, is in the
// reviewer's reach for the whole round, and appears in no commit at all: the
// commit's listing names it never, and the arm goes on to run the review with it
// in place. `git status` names it, ignored and untracked alike, which is the
// listing taken here.
//
// The ignored half of that listing is taken in git's traditional mode, never its
// matching one: matching reports an ignored directory as the one entry `!! dir/`
// and says nothing about what is inside it, so a patch writing
// build/.claude/settings.json — with build/ an ignore rule of the base, which is
// what a build directory is — is a refused component one level below everything
// that listing names, and reaches the reviewer the same way (the review of
// 2026-11-17). Traditional names each ignored file, at every depth, which is the
// depth the fence refuses a component at (replayRefusesComponent) and the depth
// B1.2 asks for.
//
// Neither listing is the patch's alone, which is what before is for. The arm's
// checkout is not always the champion's base cloned and bootstrapped: an image
// bakes a checkout that carries the dependency warm-up and the image.setup
// output (images/derived/Dockerfile.tmpl), untracked and ignored — a node_modules
// where many packages ship a .github/FUNDING.yml is the one every such repository
// has — and the run's own checkout of its branch leaves them where they are:
// CheckoutNewBranch's clean is git clean -fd, never -x, so what git already
// ignores survives it. Every one of those is a refused component the patch never
// touched, so a fence that read the listing alone would refuse every arm in such
// a repository and B1.3 would get no samples at all (the review of 2026-11-17).
// What is the patch's is what before does not hold, or holds with another size or
// modification time (replaySnapshot.wrote), and only that is listed.
//
// It is the fence's second reading, and it is the one that decides, because it
// is not a reading of the patch at all. The header fence reads the patch's own
// lines the way git's reader reads them, and that reader has corners this one
// cannot be trusted to have: `rename old`/`rename new` is a header git applies
// and the fence's prefix list does not hold, and a name git cannot parse — a
// quote never closed — is read by git from the line's own bytes with the first
// path part stripped, which is not the name the fence's reader takes out of it.
// The paths git wrote admit no such spelling: they are what git wrote
// (TestABReplayRefusesPatchTouchingAgentConfig's last cases).
//
// What this listing does not reach is a path inside a submodule or a nested
// repository: git names a gitlink entry as itself (` M sub`) and a directory it
// will not descend into as the directory alone (`?? vendor/x/`, a record whose
// path ends in "/"), and names nothing below either, so a path the patch wrote
// there is missing from this listing while it is in the reviewer's checkout for
// the whole round. The caller refuses those two records whole
// (replayUnlistedPath), and the reading of git's own parser, taken before the
// patch is applied, is what names a refused path below them (replayPatchPaths).
func replayWrittenPaths(ctx context.Context, repo *gitops.Repo, before replaySnapshot) ([]string, error) {
	// The status listing is taken before the checkout is staged, because staging
	// changes what git names a nested repository by: `git add -A` adds the
	// embedded repository as a gitlink entry, and `git status` then names the
	// entry (`A  vendor/x`) where it named the directory it would not descend
	// into before (`?? vendor/x/`). That trailing slash is the fence's only sign
	// of a nested repository, so the listing that carries it is this one
	// (replayUnlistedPath).
	status, err := replayCheckoutStatus(ctx, repo)
	if err != nil {
		return nil, err
	}
	if _, err := replayGit(ctx, repo, nil, "add", "-A"); err != nil {
		return nil, err
	}
	staged, err := replayGit(ctx, repo, nil, "--no-replace-objects", "diff", "--cached", "--no-renames", "--name-only", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(staged, "\x00") {
		if p != "" && before.wrote(repo, p) {
			paths = append(paths, p)
		}
	}
	for _, p := range status {
		if before.wrote(repo, p) {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// replaySnapshot is what the checkout carried before the patch was applied:
// every path git status names, with the size and modification time it had then.
// It is a map and not a slice because the listing after the patch is read
// against it path by path.
//
// The stamp is the size and the modification time rather than a content hash:
// both change when git apply writes the file and neither changes when it does
// not, and a hash of every file in a warmed-up node_modules is work this fence
// would do on every arm. What the two can miss is a patch that writes a path to
// the very size and the very timestamp it already had, and that needs the write
// to land inside the resolution of the clock the file was last written at.
type replaySnapshot map[string]replayStamp

// replayStamp is one path's size and modification time.
type replayStamp struct {
	size int64
	mod  time.Time
}

// replayCheckoutSnapshot reads the checkout's untracked, ignored and changed
// paths, with the stamp of each: the state replayWrittenPaths reads the listing
// after the patch against. A path git names and the filesystem does not have is
// left out of it, so the later reading counts it as the patch's — the safe side,
// and the only one left when the two disagree.
func replayCheckoutSnapshot(ctx context.Context, repo *gitops.Repo) (replaySnapshot, error) {
	paths, err := replayCheckoutStatus(ctx, repo)
	if err != nil {
		return nil, err
	}
	snap := make(replaySnapshot, len(paths))
	for _, p := range paths {
		if stamp, ok := replayStampOf(repo, p); ok {
			snap[p] = stamp
		}
	}
	return snap, nil
}

// wrote reports whether p is a path the patch wrote: one the snapshot does not
// hold, or one whose size or modification time the patch changed.
func (s replaySnapshot) wrote(repo *gitops.Repo, p string) bool {
	was, ok := s[p]
	if !ok {
		return true
	}
	now, ok := replayStampOf(repo, p)
	return !ok || now != was
}

// replayStampOf is the stamp of p in the checkout's working tree, false when
// there is no such file to stamp.
func replayStampOf(repo *gitops.Repo, p string) (replayStamp, bool) {
	info, err := os.Lstat(filepath.Join(repo.Dir, filepath.FromSlash(p)))
	if err != nil {
		return replayStamp{}, false
	}
	return replayStamp{size: info.Size(), mod: info.ModTime()}, true
}

// replayCheckoutStatus is the paths `git status` names in the checkout, ignored
// and untracked alike.
func replayCheckoutStatus(ctx context.Context, repo *gitops.Repo) ([]string, error) {
	out, err := replayGit(ctx, repo, nil, "--no-replace-objects", "status", "--porcelain=v1", "-z",
		"--no-renames", "--untracked-files=all", "--ignored=traditional")
	if err != nil {
		return nil, err
	}
	return replayStatusPaths(out), nil
}

// replayStatusPaths are the paths of a `git status --porcelain=v1 -z` listing:
// one record per NUL, each "XY PATH" — the two status letters and the space
// between them ahead of the path's own bytes, unquoted and never escaped
// (-z), so a path holding a newline or a quote is read as itself.
// --no-renames keeps a rename to one record, which is the shape parsed here: a
// rename record's origin path rides in a record of its own.
func replayStatusPaths(out string) []string {
	var paths []string
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) <= len("XY ") {
			continue
		}
		paths = append(paths, rec[len("XY "):])
	}
	return paths
}

// replayRefusedPath is the first path among paths that no reviewer arm may have
// in its checkout — one holding a refused component at any depth
// (replayRefusesComponent) — "" when there is none. It is applied to every
// reading that names paths: the parser's and the checkout's alike.
func replayRefusedPath(paths []string) string {
	for _, p := range paths {
		for _, c := range strings.Split(p, "/") {
			if replayRefusesComponent(c) {
				return p
			}
		}
	}
	return ""
}

// replayGitlinkPaths are the submodule entries of the checkout's index: the
// gitlink paths (mode 160000), whose content git lists nowhere — `git status`
// reports a gitlink entry as itself and never a path below it, and `git add -A`
// stages nothing inside one. The index is the base's own, since the apply writes
// the working tree and not the index, so the same reading serves both sides of
// it.
func replayGitlinkPaths(ctx context.Context, repo *gitops.Repo) ([]string, error) {
	out, err := replayGit(ctx, repo, nil, "--no-replace-objects", "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	var links []string
	for _, rec := range strings.Split(out, "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok || !strings.HasPrefix(meta, "160000 ") {
			continue
		}
		links = append(links, path)
	}
	return links, nil
}

// replayUnlistedPath is the first path among paths that the fence refuses
// because git lists nothing under it: a submodule's own path (a gitlink of the
// index) and a directory git names with a trailing slash, which is how git
// writes a nested repository it will not descend into. "" when there is none.
//
// A patch can write anything below such a path while every listing of this
// checkout says nothing about what it wrote — a submodule's content is not this
// repository's, and a nested repository's is not either — so the fence refuses
// the whole of it rather than judge it. Both readings that name paths apply this
// one: the parser's, before anything is written, and the listing after the
// apply, which is where the trailing slash comes from. The snapshot is what
// keeps the arm of a repository that already carried a nested repository: a path
// the patch left as it was is not one it wrote (the review of 2026-11-17).
func replayUnlistedPath(links, paths []string) string {
	for _, p := range paths {
		if strings.HasSuffix(p, "/") {
			return p
		}
		for _, link := range links {
			if p == link || strings.HasPrefix(p, link+"/") {
				return p
			}
		}
	}
	return ""
}

// replayHeaderNames are the path names that rest, a header line's remainder,
// names: the diff --git line names two (the old path and the new one), every
// other header one. Reading every name on the line is a superset of what git
// reads from it, and a superset can only add a refusal.
func replayHeaderNames(rest string) []string {
	var names []string
	for {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		if rest == "" {
			return names
		}
		name, tail := replayHeaderName(rest)
		names = append(names, name)
		rest = tail
	}
}

// replayHeaderName reads the name at the head of rest the way git's own reader
// does, and returns it with what follows it.
//
// A name that opens with a double quote is C-quoted and is decoded: \" and \\
// are the quote and the backslash, \a \b \f \n \r \t \v the control characters
// those letters name, and up to three octal digits a byte, which is how git
// writes a path that needs escaping — \056 is "." and \057 is "/", so a patch
// can spell a refused path without holding its bytes. Any other name runs to the
// next whitespace and is taken as it stands: an unquoted \056 is a backslash in
// the file's own name and not a dot.
//
// An escape git's writer never writes, and a quote never closed, are decoded to
// the bytes they hold here, and that is not what git does with them: git gives
// up on a name it cannot parse and reads the line's own bytes instead, dropping
// the first path part, so `+++ "b/CLAUDE.md` writes CLAUDE.md while this reader
// takes `b/CLAUDE.md\n` out of the line — a name no component of the fence
// matches. A name this reader takes and git does not is one the fence may miss,
// so this reading is not the fence's last word on a patch: the paths git wrote
// are listed from git itself once the patch is applied (replayWrittenPaths), and
// that listing is what decides.
func replayHeaderName(rest string) (name, tail string) {
	if rest[0] != '"' {
		if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
			return rest[:i], rest[i:]
		}
		return rest, ""
	}
	var b strings.Builder
	for i := 1; i < len(rest); {
		switch c := rest[i]; {
		case c == '"':
			return b.String(), rest[i+1:]
		case c != '\\' || i+1 == len(rest):
			b.WriteByte(c)
			i++
		case rest[i+1] >= '0' && rest[i+1] <= '7':
			v := 0
			for n := 0; n < 3 && i+1 < len(rest) && rest[i+1] >= '0' && rest[i+1] <= '7'; n++ {
				v = v*8 + int(rest[i+1]-'0')
				i++
			}
			b.WriteByte(byte(v))
			i++
		default:
			b.WriteByte(replayEscape(rest[i+1]))
			i += 2
		}
	}
	return b.String(), ""
}

// replayEscape is the byte a C-quote escape letter stands for: the control
// character it names, and the byte itself for the two escapes that spell
// themselves. Any other letter is not a quote git's writer writes, and reads as
// the letter it escapes.
func replayEscape(e byte) byte {
	switch e {
	case 'a':
		return '\a'
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'v':
		return '\v'
	}
	return e
}

// replayApply applies the patch to the checkout.
//
// git apply runs with the patch on stdin and no path argument, so it writes
// exactly what the patch names and nothing beside it, and it runs no hook, no
// filter and no script of the patch's own: the hooks an applypatch-style
// command would run belong to git am, which would also commit the patch's own
// message and author, and this applies the champion's change and no metadata of
// the champion's. It is given the runner's own standing for its git calls —
// the process environment without the provider credentials (Repo.Environ), which
// is what an agent-written filter or fsmonitor hook would need.
//
// A patch it refuses leaves the checkout untouched: git checks the whole patch
// before it writes any of it, so a truncated or corrupt patch applies nothing
// rather than half of itself. A binary entry cannot be applied from a stored
// patch at all (the object carries no binary bytes) and a symlink a later entry
// writes through is refused by git itself, so both are refusals rather than
// partial trees.
func replayApply(ctx context.Context, repo *gitops.Repo, patch []byte) error {
	_, err := replayGit(ctx, repo, bytes.NewReader(patch), "apply")
	return err
}

// replayGit runs one git command in the checkout with the runner's own standing
// for its git calls (Repo.Environ: the process environment without the provider
// credentials, which is what an agent-written filter or fsmonitor hook would
// need), in on stdin, and returns its stdout. A nil in leaves the command with
// no stdin to read, so nothing here can wait on one.
func replayGit(ctx context.Context, repo *gitops.Repo, in io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo.Dir
	cmd.Env = repo.Environ()
	cmd.Stdin = in
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
