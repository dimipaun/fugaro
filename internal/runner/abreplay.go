package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
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
//     that object (docs/design/bucket-iam.md §10 L3), so the arm reads it
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
func (r *run) applyReplayLevel() error {
	if !levelOn(r.levels, r.replay) {
		return r.refuseArm(fmt.Sprintf("this task replays review level %s, and the run it carries does not run that level: a reviewer arm's one review is the slot level's, and which levels run is the run's own mode, recipe, policy and task (resolveLevels). Launch the arm against a run whose levels include the slot's", r.replay))
	}
	r.setLevelModel(r.replay, r.spec.AB.Challenger)
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
	if comp := replayPatchRefusal(patch); comp != "" {
		r.refuseReplay(fmt.Sprintf("the champion run %s's patch touches %s, and a patch that reaches a hook, an instruction file or a tool setting may not be replayed: the review this arm runs would load it as configuration of its own session (B1.2, D728). Launch the pair against a champion whose patch touches none of .claude, .claude-plugin, .fugaro, .github, .git*, fugaro.yaml, CLAUDE.md, CLAUDE.local.md or .mcp.json, at any depth", r.spec.AB.ChampionRun, comp))
		return
	}
	if err := replayApply(ctx, r.repo, patch); err != nil {
		r.refuseReplay(fmt.Sprintf("the champion run %s's patch does not apply to this checkout: %v. The arm applies the patch with git apply, which writes nothing when it refuses, so this is a patch written for another base: launch the arm at the commit the champion's run started from (B1.4)", r.spec.AB.ChampionRun, err))
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
	reviewPrompt := ReviewPrompt(r.cfg.Agent.Review, r.reviewFile, r.reviewRev(ctx), r.reviewDiffSize(ctx), r.baseUnreadable())
	prompt := r.replayPrompt(reviewPrompt, r.replay)
	// Read before the round, as the loops read it: the summary keeps the commit
	// this round read (D727).
	head := r.reviewedHead(ctx)
	v, parsed, ok := r.replayRound(ctx, stage.stage, prompt)
	if !ok {
		return
	}
	model, effort := r.reviewFacts(stage.stage)
	sum := runstore.ReviewSummary{Round: 1, Tier: replayTier(r.replay), Verdict: "none", Head: head, Model: model, Effort: effort}
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
		req := agent.Request{Prompt: r.reviewPromptWithInput(ctx, prompt), SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema}
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
// input: the read of the runs bucket, or the local git of the checkout, stopped
// for a reason that is nobody's configuration. It is the ending the check gate's
// own infrastructure failures take (gate.go, failInfra): status infra_error,
// class infra_error, and no finalize, since there is nothing to record of an arm
// that never reached its review.
func (r *run) failReplayRead(ctx context.Context, err error) {
	reason := r.redact(err.Error())
	r.recordEnd(runstore.StatusInfraError, runstore.OutcomeNone, failure.InfraError, reason)
	r.skipPR = true
	r.storeEnded(ctx, nil)
	r.d.Log.Error("the reviewer replay could not read the champion's patch", "err", reason)
}

// refuseReplay refuses the arm the way refuseArm does — failed, outcome none,
// config_invalid — and through the run's own ending rather than by writing the
// record here: a replay refused after bootstrap still has a finalize to run, and
// the shadow finalize is what writes the arm's ending, its changed paths and its
// own patch object (an empty one: nothing was applied). The reason quotes the
// champion's patch and git's own message about it, so it is redacted first.
func (r *run) refuseReplay(reason string) {
	reason = r.redact(reason)
	r.failWithClass(failure.ConfigInvalid, reason)
	r.d.Log.Warn("the reviewer replay was refused", "reason", reason)
}

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
func replayRefusesComponent(name string) bool {
	return strings.HasPrefix(name, ".git") || slices.Contains(replayRefusedNames, name)
}

// replayPatchPrefixes are the two header lines that name a path in a git diff,
// in both of git's spellings: the extended header of a rename, a copy or a mode
// change ("rename from x") and the ---/+++ pair of an ordinary change ("--- a/x").
// A path is read out of a line that starts with one of these.
var replayPatchPrefixes = []string{"rename from ", "rename to ", "copy from ", "copy to ", "--- ", "+++ ", "diff --git "}

// replayPatchRefusal is the path component of patch that no reviewer arm may
// replay, "" when the patch touches none of them.
//
// The paths are read from the patch's own header lines rather than from git
// (git apply --numstat names a rename's new path alone, so a rename away from a
// refused path would escape the fence), and each is read as a set of path
// components: git quotes a header path that holds a space or a control
// character, and C-quoting escapes neither "." nor "/", so a refused component
// survives in the line's own bytes wherever in the path it sits. A hunk line can
// never be read as a header — its first byte is a space, a "+", a "-" or a
// backslash — and the one line that can look like one (a removed line whose own
// text starts with "--- ") can only add a refusal, never hide one.
func replayPatchRefusal(patch []byte) string {
	for line := range strings.Lines(string(patch)) {
		for _, prefix := range replayPatchPrefixes {
			rest, ok := strings.CutPrefix(line, prefix)
			if !ok {
				continue
			}
			for _, comp := range strings.FieldsFunc(rest, replayPathSep) {
				if replayRefusesComponent(comp) {
					return comp
				}
			}
			break
		}
	}
	return ""
}

// replayPathSep splits a header line's path into components: the separators of a
// path, the quote and the escape of git's C-quoted spelling, and whitespace,
// which is what ends the path in every one of the header shapes above.
func replayPathSep(r rune) bool {
	return r == '/' || r == '"' || r == '\\' || unicode.IsSpace(r)
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
	cmd := exec.CommandContext(ctx, "git", "apply")
	cmd.Dir = repo.Dir
	cmd.Env = repo.Environ()
	cmd.Stdin = bytes.NewReader(patch)
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
