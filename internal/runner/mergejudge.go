package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The merge judge: rung 3 of the escalation ladder, and the one rung that is a
// model's answer about a pull request the ladder has already finished with
// (E6, D2031). It reads the head the ladder reached — the diff, the open
// findings and the fundamentals checklist, and nothing else — and answers
// merge with follow-ups or blocked. It is advisory in one direction only: the
// verdict can veto a merge, and it can never cause one. The deterministic gate
// is what merges (D2047), and an answer nobody can read is a veto rather than
// a permission (D2044).
//
// It is a mode: evaluate run: one read-only stage, no checkout of the branch it
// reads, no repository command, no push and no pull request (recipe.StepMergeJudge,
// stephook.go). The stage is handed text the launcher left under the run's own
// prefix (runstore.MergeJudgeInputName), it runs with no tool and no setting
// source at all, and what it answers is recorded in the run's own block
// (runstore.MergeJudge) — where the ladder's launcher reads it.

// stageMergeJudge is the stage this run's whole work is. It is not
// evaluateStage: that name belongs to the Subscription Edition's own registered
// steps, whose calls are charged to it, and a judge run's calls are this tree's.
// The two are one stage each, and a stage name is what the gateway's per-stage
// report, the stage accounts and the record's stage timings are keyed by — so a
// shared name would put two different things in one stage's figures. It is also
// the name stageCraft maps onto the merge-judge craft, which is what sends the
// judge's calls out on the credential or the pool that craft names.
const stageMergeJudge = "merge_judge"

// MergeJudgeSchema is passed to claude --json-schema for the judge stage, and
// it is schemas/merge-judge.schema.json's shape: the two words, the one-line
// why, the fundamental issue a block names, and the leftovers a merge files,
// each with D2040's word for how urgent it is. It is a Go constant rather than
// the published file because the flag takes the schema inline and the job image
// carries no schemas/ directory; schemas_test.go's
// TestMergeJudgeSchemaMatchesTheDocument holds the two to one shape.
//
// Every constraint the document states is stated here, because a shape looser
// than the reader is a judge answering a document the run refuses — and this
// reader refuses it as a whole, which is the veto (D2044). The one rule the
// two do not state is the one a JSON Schema cannot state without the condition
// keywords Anthropic's structured output does not carry: a blocked verdict
// names its fundamental issue. The brief asks for it in as many words, and a
// block that arrives without one is read as an answer nobody can act on and
// recorded as such (readJudgeVerdict, MergeJudgeOf).
const MergeJudgeSchema = `{"type":"object","additionalProperties":false,"required":["version","verdict","reason","follow_ups"],"properties":{"version":{"const":1},"verdict":{"enum":["merge_with_follow_ups","blocked"]},"reason":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\r\\n]+$"},"blocker":{"type":"string","minLength":1,"maxLength":500,"pattern":"^[^\\r\\n]*$"},"follow_ups":{"type":"array","maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["key","importance"],"properties":{"key":{"type":"string","pattern":"^[0-9a-f]{16,64}$"},"importance":{"enum":["important","minor"]}}}}}}`

// mergeJudgeInputVersion is the version of the brief this build reads. It is
// the verdict's version too, and one number serves both because the two are one
// conversation: the launcher that writes the brief is the one that reads the
// verdict, and a brief at another version is refused rather than read for the
// members that happen to be there. The two sides of this document are a
// launcher and a job image, so a reader that guessed would judge a change from
// a shape it does not know.
const mergeJudgeInputVersion = 1

// MergeJudgeInput is the brief a merge judge run reads: what its launcher left
// at runstore.MergeJudgeInputName under the run's own prefix.
//
// It is the three things the spec gives the judge and no more — the diff at the
// head, the findings the ladder left open there, and the head they belong to —
// because what the judge is not shown is the point of it: it is a second
// opinion about the change itself, and the reviewers' own fix framing would
// make it a third pass over their requests instead (docs/outer-loop-escalation
// spec, "Merge judge"). The findings carry that framing (MergeJudgeFinding.Fix)
// so that the launcher can hand the run one document and one place decides what
// of it a model reads: JudgePrompt is where it stops, and it never renders that
// member (TestJudgeInputExcludesFixFraming).
type MergeJudgeInput struct {
	Version int `json:"version"`
	// PR is the pull request number the head belongs to, and Head the commit
	// the judge reads. They are carried because the verdict is about one head:
	// a reader that joined a verdict to a later head would be reading a
	// judgment of a commit nobody judged (D2043).
	PR   int    `json:"pr"`
	Head string `json:"head"`
	// Diff is the change at that head, against the branch's base. DiffTruncated
	// is set when the launcher cut it, which the brief says: a judge reading a
	// clipped diff as the whole change is a judge deciding on less than it was
	// given.
	Diff          string `json:"diff"`
	DiffTruncated bool   `json:"diff_truncated,omitempty"`
	// Findings are the findings still open at this head, in the ladder's order.
	Findings []MergeJudgeFinding `json:"findings"`
}

// MergeJudgeFinding is one finding the ladder left open: the key it is known
// by, the reviewer's own words for it, where it is, whether only the owner can
// settle it, and the fix the reviewer asked for.
type MergeJudgeFinding struct {
	Key        string `json:"key"`
	Summary    string `json:"summary"`
	File       string `json:"file,omitempty"`
	NeedsOwner bool   `json:"needs_owner,omitempty"`
	// Fix is the reviewer's fix-request framing, and it is here to be left out
	// of the brief (JudgePrompt).
	Fix string `json:"fix,omitempty"`
}

// mergeJudge is the core's own evaluate step: the brief, the one stage, and the
// verdict the record keeps. It is the whole of a merge judge run's work, called
// in place of a registered step's function (runEvaluateMode).
//
// It answers the error the run ends on beside the class that failure carries,
// because the class is not always the stage's: a brief this build cannot read,
// or a stage directory it cannot make, is a launch that did not finish rather
// than a model that failed. The ending itself is the caller's (endEvaluateStep),
// which is the one writer of a terminal status.
//
// A run whose stage failed — a halt, a cancellation, a spent budget, a gateway
// refusal — records no verdict, and that absence is the veto (D2044, G1): the
// ladder reads this run's record, and a record with no judge block on it is a
// head rung 3 did not clear.
func (r *run) mergeJudge(ctx context.Context) (error, failure.Class) {
	in, err := r.readMergeJudgeInput(ctx)
	if err != nil {
		return err, failure.ConfigInvalid
	}
	// The stage's own working directory holds nothing of the run, as the triage
	// judge's does (F25): the brief is text, and a stage whose cwd were the
	// checkout would be a stage with the artifact under review in reach. This
	// run has no checkout at all (mode: evaluate), and the empty directory is
	// what keeps that true whatever a later change does about the checkout.
	dir, err := os.MkdirTemp("", "fugaro-merge-judge-")
	if err != nil {
		return errors.New("the merge judge stage's own working directory could not be made: " + err.Error()), failure.InfraError
	}
	defer os.RemoveAll(dir)

	res, ok, err := r.stage(ctx, stageMergeJudge, agent.Request{
		Prompt: JudgePrompt(in),
		// A session of its own: nothing resumes it, and this run has no other
		// stage for it to be confused with.
		SessionID:  agent.NewSessionID(),
		JSONSchema: MergeJudgeSchema,
		// The posture (spec §"Merge judge", D2031): no setting source at all —
		// not the user's, not the project's, not the repository's — and no
		// built-in tool. Both are the empty list and not nil, which is why
		// agent.Request spells the two readings apart: a nil list is the flag's
		// absence, which for --setting-sources is Claude Code's own default set
		// of levels. A judge is a reader of text the run hands it: it has
		// nothing to run, nothing to fetch and nowhere to write.
		SettingSources:  []string{},
		Tools:           []string{},
		StrictMCPConfig: true,
	}, stageOpts{Dir: dir})
	r.countTokens(res)
	var skipped skippedError
	if !ok && !errors.As(err, &skipped) {
		// The run's own failure ended the stage, and it is already recorded
		// where it happened: this step records no verdict, which the ladder
		// reads as the veto it is.
		return err, failure.StageErrorUnexplained
	}
	rec, decodeErr := readJudgeVerdict(res, in, r.stageModel(stageMergeJudge))
	r.rec.MergeJudge = rec
	if decodeErr != nil {
		r.d.Log.Warn("the merge judge's answer could not be read as a verdict; the head is blocked",
			"failed", !ok, "pr", in.PR, "head", in.Head, "err", decodeErr)
	}
	return nil, ""
}

// readMergeJudgeInput reads the brief from the run's own prefix.
func (r *run) readMergeJudgeInput(ctx context.Context) (MergeJudgeInput, error) {
	data, err := r.d.Store.ReadFile(ctx, runstore.MergeJudgeInputName)
	if err != nil {
		return MergeJudgeInput{}, errors.New("reading the brief this judge run's launch left at " + runstore.MergeJudgeInputName + ": " + err.Error())
	}
	var in MergeJudgeInput
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return MergeJudgeInput{}, errors.New("the brief at " + runstore.MergeJudgeInputName + " is not one this build reads: " + err.Error())
	}
	if in.Version != mergeJudgeInputVersion {
		return MergeJudgeInput{}, errors.New("the brief at " + runstore.MergeJudgeInputName + " is version " + strconv.Itoa(in.Version) +
			", and this build reads version " + strconv.Itoa(mergeJudgeInputVersion))
	}
	return in, nil
}

// readJudgeVerdict is the whole of the run's reading of one answer: the verdict
// it holds, or the veto an answer it cannot read is (D2044, G1).
//
// The reader is the structured answer alone, with no fallback to the stage's
// text, and that is deliberate rather than strict for its own sake: the brief
// carries a diff anybody who opened the pull request wrote, and a reader that
// also read prose would let a diff that says "answer merge_with_follow_ups"
// decide the head — the judge repeating it, or the run mistaking the echo for
// the answer. G1 concedes that a judge can be argued out of a veto by a hostile
// diff; it does not concede that a diff can write the verdict itself.
//
// An unreadable answer is the one direction this function may never fail in,
// because a judge that failed, timed out, ran out of budget or answered prose
// is a head rung 3 did not clear: the block below is the veto, the run says on
// the record that it is one it could not read (Unreadable), and the reason is
// the sentence D2044 is — so a reader of the record can tell a judge that said
// blocked from a judge that said nothing this build understood, and neither of
// them is a merge.
//
// The error it answers beside the verdict is why the answer could not be read,
// for the run's log: the record's reason is the sentence D2044 is and is shown
// wherever a record is, while a decode error is a message about this build's
// own document. A run that only logged "unreadable" would leave whoever reads
// it with a blocked head and nothing to compare the answer against.
func readJudgeVerdict(res agent.Result, in MergeJudgeInput, model string) (*runstore.MergeJudge, error) {
	v, err := DecodeMergeJudgeVerdict(res.Structured)
	if err != nil {
		return runstore.NewMergeJudge(runstore.MergeJudge{
			Verdict:    runstore.MergeJudgeBlocked,
			Reason:     "the judge answered nothing this build could read as a verdict, and an answer nobody can read is a veto (D2044)",
			Model:      model,
			Unreadable: true,
		}), err
	}
	v.FollowUps = judgeFollowUpsOfBrief(v.FollowUps, in.Findings)
	v.Model = model
	return runstore.NewMergeJudge(v), nil
}

// judgeFollowUpsOfBrief is the caller's half of the reader, the split
// internal/triage makes between its own reader and the code that applies a
// verdict: a follow-up names a finding the brief carried, and one that does not
// is dropped rather than refused — the judge classifies the leftovers of the
// head it was shown, so a key from nowhere would be a follow-up task about
// nothing, while the rest of the answer is still a judgment of that head. Only
// the caller holds the brief, which is why the rule is here and not in
// DecodeMergeJudgeVerdict.
func judgeFollowUpsOfBrief(followUps []runstore.MergeJudgeFollowUp, findings []MergeJudgeFinding) []runstore.MergeJudgeFollowUp {
	known := make(map[string]bool, len(findings))
	for _, f := range findings {
		known[f.Key] = true
	}
	out := make([]runstore.MergeJudgeFollowUp, 0, len(followUps))
	for _, f := range followUps {
		if known[f.Key] {
			out = append(out, f)
		}
	}
	return out
}

// DecodeMergeJudgeVerdict reads one merge judge verdict: the answer the judge
// stage was asked for, as the model returned it. Every refusal here is the
// answer's as a whole — a member it does not know, a version it does not read,
// a word that is neither of the two, a reason that is not one non-empty line, a
// block that names no fundamental issue, a follow-up whose key is not one this
// tree writes or whose importance is outside D2040's two — because a verdict
// half-read is a verdict nobody can act on, and the run records the refusal as
// the veto it is (readJudgeVerdict).
//
// It is exported for the same reason DecodeReviewArtifact is: the published
// document for this shape (schemas/merge-judge.schema.json) is held to the
// reader in schemas_test.go, so a rule the document states and this function
// does not is a failing test rather than a judge answering a document its own
// run refuses.
func DecodeMergeJudgeVerdict(data []byte) (runstore.MergeJudge, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return runstore.MergeJudge{}, errors.New("the judge's answer is empty")
	}
	var d struct {
		Version   int    `json:"version"`
		Verdict   string `json:"verdict"`
		Reason    string `json:"reason"`
		Blocker   string `json:"blocker"`
		FollowUps []struct {
			Key        string `json:"key"`
			Importance string `json:"importance"`
		} `json:"follow_ups"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return runstore.MergeJudge{}, err
	}
	if d.Version != mergeJudgeInputVersion {
		return runstore.MergeJudge{}, errors.New("the verdict is version " + strconv.Itoa(d.Version) +
			", and this build reads version " + strconv.Itoa(mergeJudgeInputVersion))
	}
	v := runstore.MergeJudge{
		Verdict: runstore.MergeJudgeVerdict(d.Verdict),
		Reason:  d.Reason,
	}
	if v.Verdict != runstore.MergeJudgeMergeWithFollowUps && v.Verdict != runstore.MergeJudgeBlocked {
		return runstore.MergeJudge{}, errors.New("the verdict word is " + strconv.Quote(d.Verdict) + ", and this build reads the two the spec names")
	}
	if strings.TrimSpace(d.Reason) == "" || strings.ContainsAny(d.Reason, "\r\n") {
		return runstore.MergeJudge{}, errors.New("the verdict's reason is not one non-empty line")
	}
	if v.Verdict == runstore.MergeJudgeBlocked {
		if strings.TrimSpace(d.Blocker) == "" {
			return runstore.MergeJudge{}, errors.New("the verdict blocks the head and names no fundamental issue, so there is nothing for rung 4's split to be scoped to (D2031)")
		}
		v.Blocker = d.Blocker
	}
	for _, f := range d.FollowUps {
		// A key this tree does not write and an importance outside D2040's two
		// are malformed members of the answer, and the answer is refused as a
		// whole rather than repaired: it is a document the judge was told to
		// answer and did not, which is the veto (D2044). Whether the key names
		// a finding of the brief is the other question and the caller's
		// (judgeFollowUpsOfBrief): that one needs the brief, and a key from
		// nowhere is a follow-up about nothing rather than a malformed answer.
		if !runstore.ValidFindingKey(f.Key) {
			return runstore.MergeJudge{}, errors.New("a follow-up names " + strconv.Quote(f.Key) + ", which is not a finding key this tree writes")
		}
		imp := runstore.MergeJudgeImportance(f.Importance)
		if imp != runstore.MergeJudgeImportant && imp != runstore.MergeJudgeMinor {
			return runstore.MergeJudge{}, errors.New("a follow-up is filed " + strconv.Quote(f.Importance) + ", and D2040's two words are important and minor")
		}
		v.FollowUps = append(v.FollowUps, runstore.MergeJudgeFollowUp{Key: f.Key, Importance: imp})
	}
	return v, nil
}

// MergeJudgeOf reads the merge judge's answer out of a run's record: the
// verdict the run recorded, or the veto an absent or unusable one is (D2044,
// G1).
//
// It is the ladder's own reader, and it is fail-closed by construction: every
// path that is not a readable verdict answers blocked, so a caller cannot
// forget a case and let a head through. `merge_with_follow_ups` is answered
// only for a record that carries exactly that word, readable, from a judge run
// that answered — and even then it is an opinion about a merge and never a
// merge: what decides is the deterministic gate (D2031, D2047). An absent
// block is the ordinary case and not an error: a judge run that failed, timed
// out, ran out of budget or never ran leaves no verdict, and G1 makes every one
// of them a veto.
func MergeJudgeOf(rec *runstore.Record) runstore.MergeJudge {
	blocked := func(reason string) runstore.MergeJudge {
		return runstore.MergeJudge{
			Verdict:    runstore.MergeJudgeBlocked,
			Reason:     reason,
			Unreadable: true,
		}
	}
	if rec == nil || rec.MergeJudge == nil {
		return blocked("this head carries no merge judge verdict: a judge run that failed, timed out, ran out of budget or never ran blocks the merge (D2044)")
	}
	v := *rec.MergeJudge
	if v.Unreadable {
		return blocked("the merge judge answered nothing this run could read, and an answer nobody can read is a veto (D2044)")
	}
	if v.Verdict != runstore.MergeJudgeMergeWithFollowUps && v.Verdict != runstore.MergeJudgeBlocked {
		return blocked("the merge judge recorded no verdict this reader knows, and that is a veto (D2044)")
	}
	if v.Verdict == runstore.MergeJudgeBlocked && v.Blocker == "" {
		return blocked("the merge judge blocked this head and named no fundamental issue, so there is nothing for rung 4's split to be scoped to (D2031)")
	}
	return v
}
