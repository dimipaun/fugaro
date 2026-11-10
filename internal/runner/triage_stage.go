package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/triage"
)

// The triage stage: one judge call after each review round, its verdict
// recorded and nothing yet acted on (docs/post-review-triage, D1840, D1850).
//
// This slice is the shadow half. What the judge answered is written to the
// record's triage block and to nothing else: the fixer is handed the round's
// findings exactly as it is today, Decide reads the recorded verdict word as it
// always has, and the pull request's body carries no deferred section — so a
// bug in this path can mislabel a round's log and cannot merge anything
// (T-TR7b enforces, T-TR8 reads the cap, T-TR9 writes the section). The one
// thing the stage must not do is act, and TestShadowTriageDoesNotChangeTheFix
// Prompt and TestShadowTriageWritesNoDeferred pin that.

// TriageSchema is passed to claude --json-schema for the triage stage, and it
// is schemas/triage.schema.json's shape: one entry per finding the brief
// numbered, each naming its index, echoing its key, and answering one of the
// two words with a one-line why.
//
// It is not internal/triage's reader (Decode), which is stricter in the five
// ways its own doc states: the model is constrained by this schema as it
// decodes, and what comes back is still untrusted data read by that reader, so
// the two agreeing on the shape is what keeps a well-formed answer from being
// refused for a spelling the model was never shown.
const TriageSchema = `{"type":"object","properties":{"version":{"type":"integer","const":1},"findings":{"type":"array","items":{"type":"object","properties":{"index":{"type":"integer","minimum":0},"key":{"type":"string","pattern":"^[0-9a-f]{16,64}$"},"verdict":{"type":"string","enum":["block","defer"]},"reason":{"type":"string","minLength":1,"maxLength":200},"group":{"type":"string","maxLength":200}},"required":["index","key","verdict","reason"]}}},"required":["version","findings"]}`

// triageOn reports whether this run judges its review rounds at all. Three
// answers have to agree, and each of them is a gate the plan pins with a test:
//
//   - The owner's published node has not turned the stage off
//     (policy.Effective.TriageOff, D1854). A repository may turn it off for
//     itself and may not turn it back on over that node, which is the merge's
//     own rule.
//   - The repository has opted in to rigor modes at all: the switch is read
//     through the same base-branch path as the mode, and an unconfigured
//     repository — one whose file has no rigor: block — resolves Pro by default
//     (D19) and must not defer anything (D1852). Configured is exactly the
//     bound findingsPolicyOf makes for the same reason.
//   - The base branch's own agent.triage says so, under the mode the run has
//     reached (config.Agent.TriageOn, which is where D1851's Startup-and-Pro
//     rule lives). baseCfg is the base branch's resolved file and nil when that
//     file could not be read, and a run with no base file to read a switch from
//     does not triage: the strict direction, and never the checkout's own
//     answer, which is the artifact under review (D25, D1854).
//
// r.rigorCfg is the base's rigor half and r.baseCfg the whole file, both read
// once at bootstrap (baseRigor). The mode asked here is reachedRigor and not
// the mode the run started at: the mode never falls (D22), so a run that rose
// to Pro mid-run is judged as Pro for the rounds that follow (F14).
func (r *run) triageOn() bool {
	if r.policy.TriageOff() {
		return false
	}
	if !r.rigorCfg.Configured() {
		return false
	}
	if r.baseCfg == nil {
		return false
	}
	return r.baseCfg.Agent.TriageOn(r.reachedRigor())
}

// triageRound is the step itself: it judges the findings of one review round
// and records what it answered. It never changes what the run does with a
// verdict — a call that could not be made or could not be read is recorded as
// a failed round and the loop goes on (D1846) — and it reports whether the loop
// may go on at all: false is a stage failure that already ended the run, and
// the caller returns into its own loop the way it does after any other stage
// that failed.
//
// The distinction is the run's and not the stage's. A stage the run may do
// without (Skippable) is skipped and the round logged as failed; a stage the
// gateway refused — a model that is not pinned for it, a tool the policy
// forbids, a request shape it will not forward — a stage the run's own clock
// stopped, a cancel, a spent budget and a halt are all facts about the run,
// and the loop that called the judge is not the one that gets to ignore them:
// a run a shadow-only stage failed buys no fix round, no further review and no
// second judge, exactly as it buys none after a failed review round. The
// failure itself is recorded where the stage recorded it; nothing is logged
// for this round, which no judge answered.
//
// round is the review round this triage followed, as the round's own summary
// numbered it (ReviewSummary.Round), and level is the review level that ran it
// (D1847): a level 1 round and a level 2 round of the same run are two triage
// rounds, and which level's question the judge answered is what the record
// says beside each.
func (r *run) triageRound(ctx context.Context, round int, level policy.Level, v Verdict) bool {
	if !r.triageOn() {
		return true
	}
	mode := r.reachedRigor()
	in, findings := triageInputs(v.Findings)
	// The judge is asked from a directory that holds nothing of the run: the
	// brief carries the round's findings and the task's goal as text, and a
	// stage whose working directory were the checkout would be a stage with the
	// artifact under review in reach — the very thing the empty tool list and
	// the empty setting sources take away (F25). A failure to make it is not
	// worth a model call with the checkout as its cwd, so the round is recorded
	// as failed.
	dir, err := os.MkdirTemp("", "fugaro-triage-")
	if err != nil {
		r.d.Log.Warn("the triage stage's own working directory could not be made; the round is recorded as failed",
			"round", round, "err", r.redact(err.Error()))
		r.recordTriage(ctx, round, level, triage.Outcome{}, false)
		return true
	}
	defer os.RemoveAll(dir)

	req := agent.Request{
		Prompt: triage.Prompt(mode, r.spec.Task, findings),
		// A session of its own, like a review round's: nothing resumes it, and
		// giving it the implement session's ID would put the judge's question
		// into the conversation the fixer goes on with.
		SessionID:  agent.NewSessionID(),
		JSONSchema: TriageSchema,
		// The posture (F25, F29): no setting source at all — not the user's,
		// not the project's, not the checkout's — and no built-in tool. Both are
		// the empty list and not nil, which is why agent.Request spells the two
		// readings apart: a nil list is the flag's absence, which for
		// --setting-sources is Claude Code's own default set of levels, the
		// user's own included (agent.Request.SettingSources). A judge is a
		// reader of text the run hands it: it has nothing to run, nothing to
		// fetch and nowhere to write.
		SettingSources:  []string{},
		Tools:           []string{},
		StrictMCPConfig: true,
	}
	res, ok, err := r.stage(ctx, stageTriage, req, stageOpts{Skippable: true, Dir: dir})
	r.countTokens(res)
	var skipped skippedError
	if !ok && !errors.As(err, &skipped) {
		// A halt, a cancellation, a spent budget or a violation ended the run:
		// the run's own reason is already recorded and there is no round left
		// to log, exactly as the review loop itself returns here.
		return false
	}
	verdict, parsed := readTriageVerdict(res)
	if !parsed {
		r.d.Log.Warn("the triage round produced no readable verdict; every finding the mode's table blocks blocks",
			"round", round, "failed", !ok)
	}
	r.recordTriage(ctx, round, level, triage.Apply(mode, in, verdict, parsed), parsed)
	return true
}

// recordTriage writes one round to the record's triage block. The block is the
// run's log of what the judge decided, and the only place this stage writes
// anything: Record.Deferred — what took effect — stays empty until T-TR7b, so
// no shadow verdict can reach a follow-up task or the pull request's body.
//
// A round is recorded whether or not the call answered, which is what makes a
// failed one visible rather than absent (runstore.TriageRound.Failed): the
// outcome it carries is Apply's fail-safe one, every finding the table blocks
// blocking.
func (r *run) recordTriage(ctx context.Context, round int, level policy.Level, out triage.Outcome, read bool) {
	placed := make(map[int]triage.Placed, len(out.Blocking)+len(out.Deferred))
	for _, p := range out.Blocking {
		placed[p.Index] = p
	}
	for _, p := range out.Deferred {
		placed[p.Index] = p
	}
	entries := make([]runstore.TriageEntry, 0, len(placed))
	// The round's own order, which is the order the brief numbered and the
	// order the record's entries are read in (D1850): Apply answers two lists,
	// blocking and deferred, and neither of them is the round's order alone.
	for i := 0; i <= maxIndex(placed); i++ {
		p, ok := placed[i]
		if !ok {
			continue
		}
		entries = append(entries, runstore.TriageEntry{
			Index:   p.Index,
			Key:     p.Key,
			Verdict: runstore.TriageVerdict(p.Verdict),
			Reason:  p.Reason,
			Group:   p.Group,
			Source:  runstore.TriageSource(p.Source),
		})
	}
	r.triageRounds = append(r.triageRounds, runstore.TriageRound{
		Round:   round,
		Mode:    r.reachedRigor(),
		Level:   level,
		Account: r.rec.StageAccounts[stageTriage],
		Model:   r.stageModel(stageTriage),
		Failed:  !read,
		Entries: entries,
	})
	r.rec.Triage = runstore.NewTriage(r.triageRounds)
	r.save(ctx)
}

// maxIndex is the largest index placed, or -1 for a round nothing was placed
// on — an empty round, or one whose every finding the answer left out.
func maxIndex(placed map[int]triage.Placed) int {
	hi := -1
	for i := range placed {
		if i > hi {
			hi = i
		}
	}
	return hi
}

// triageInputs is one review round's findings as the judge reads them and as
// applying its verdict reads them: the brief's own list, and the scores, owner
// flags and keys Apply places entries by. The two are built together so an
// entry's index means the same finding in both — the brief numbers the list,
// and Apply's Input is indexed by it (D1850).
//
// Every finding of the round is in both, in the round's order. Nothing is
// filtered here: which findings a judge may reach is Apply's rule and not the
// brief's (D1857), and a brief that left the floor's findings out would be a
// judge asked a question about a round it was not shown.
func triageInputs(findings []Finding) ([]triage.Input, []triage.Finding) {
	in := make([]triage.Input, 0, len(findings))
	brief := make([]triage.Finding, 0, len(findings))
	for i, f := range findings {
		key := findingKey(f)
		in = append(in, triage.Input{Score: scoreOf(f), NeedsOwner: f.NeedsOwner, Key: key})
		brief = append(brief, triage.Finding{
			Index: i, Key: key, Score: scoreOf(f), NeedsOwner: f.NeedsOwner,
			File: f.File, Summary: f.Summary,
		})
	}
	return in, brief
}

// findingKey is a finding's identity across rounds (F11, D1850): a lowercase
// hex digest of its file and summary, which is the pair that says which finding
// this is where a re-review reordered or renumbered the round's list. The two
// are joined with a NUL — the separator internal/backend's own name hashes use
// — so that a file and a summary cannot be split into another finding's pair,
// and the whole digest is kept because nothing needs it shorter: the schema
// allows 16 to 64 characters and the record and the follow-up task carry the
// key as an opaque string.
//
// It is derived here rather than in internal/triage because the reader of a
// verdict holds the key to being non-empty and nothing more: the digest is the
// caller's, and only the caller can say what a finding is made of.
//
// A reviewer writes both halves, so two findings of one round that share a file
// and a summary share a key. That is the honest reading of the pair and not a
// collision to defend against (D1850): the two are one finding said twice, and
// Apply blocks both when a verdict cannot be placed on one of them.
func findingKey(f Finding) string {
	sum := sha256.Sum256([]byte(f.File + "\x00" + f.Summary))
	return hex.EncodeToString(sum[:])
}

// readTriageVerdict reads one triage verdict from the judge's answer, with the
// fallback the review stages use: the structured output first, then the last
// fenced JSON block of the text. Anything else is no answer, which blocks every
// finding the table blocks (Apply's ok == false).
func readTriageVerdict(res agent.Result) (triage.Verdict, bool) {
	if len(res.Structured) > 0 {
		if v, err := triage.Decode(res.Structured); err == nil {
			return v, true
		}
	}
	if m := fencedJSONRE.FindAllStringSubmatch(res.Text, -1); len(m) > 0 {
		if v, err := triage.Decode([]byte(m[len(m)-1][1])); err == nil {
			return v, true
		}
	}
	return triage.Verdict{}, false
}
