package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/triage"
)

// The triage stage: one judge call after each review round, whose verdict now
// decides where each finding goes (docs/post-review-triage, D1840, D1850).
//
// T-TR7a logged the verdict and acted on nothing; this slice acts on it, and
// three things follow from an outcome. The fixer is handed the round's blocking
// findings and never a deferred one (F4, F16, acceptance 2); a round whose
// findings are all deferred ends its level and buys no fix round, and the loop
// ends ready on it (F21, acceptance 1); and the deferrals that took effect are
// filed in the record's own block, which is what the report, the pull request's
// body (T-TR9) and `fugaro followups` read.
//
// What T-TR7a pinned still holds wherever the table alone decides: the mode's
// threshold table stays the authority and the fallback (D1840). A judge may
// only move the findings the table blocks and the floor does not name (D1857),
// a failed call blocks every finding the table blocks (D1846), and with the
// switch off, in POC or Critical, or for a repository that never opted in, the
// stage makes no call and the loop and the fixer are the ones they were before
// it existed (F32). T-TR8 reads the cap, and only T-TR9 writes the pull
// request's deferred section.

// TriageSchema is passed to claude --json-schema for the triage stage, and it
// is schemas/triage.schema.json's shape: one entry per finding the brief
// numbered, each naming its index, echoing its key, and answering one of the
// two words with a one-line why. It is a Go constant rather than the published
// file because the flag takes the schema inline and the job image carries no
// schemas/ directory; schemas_test.go's TestTriageSchemaMatchesTheDocument
// holds the two to one shape, member names and bounds included.
//
// Every constraint the document states is stated here, because a shape looser
// than the reader is a judge answering a document the run refuses as a whole:
// the round is recorded failed and every finding the mode's table blocks blocks
// (D1846). A reason the judge wrapped onto a second line is the case in point —
// the reader refuses it rather than folding it (checkLine) — so the one-line
// rule is stated where the model can read it, and not only where it is
// enforced.
//
// It is not internal/triage's reader (Decode), which is stricter in the five
// ways its own doc states: the model is constrained by this schema as it
// decodes, and what comes back is still untrusted data read by that reader, so
// the two agreeing on the shape is what keeps a well-formed answer from being
// refused for a spelling the model was never shown.
const TriageSchema = `{"type":"object","additionalProperties":false,"required":["version","findings"],"properties":{"version":{"const":1},"findings":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["index","key","verdict","reason"],"properties":{"index":{"type":"integer","minimum":0},"key":{"type":"string","pattern":"^[0-9a-f]{16,64}$"},"verdict":{"enum":["block","defer"]},"reason":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\r\\n]+$"},"group":{"type":"string","maxLength":200,"pattern":"^[^\\r\\n]*$"}}}}}}`

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

// triageOutcome is what one triage round settled, as the loops read it: the
// findings the round still blocks on, the ones a judge deferred, and whether a
// judge was asked at all.
type triageOutcome struct {
	// Judged is false where the stage did not run — the switch is off, the mode
	// is POC, Critical or Hotfix, or the repository never opted in to modes at
	// all — and every field below is empty then. The loops read it to tell "this
	// round's findings are all deferred" from "no judge was asked", which is
	// F32's whole content: the two leave the same empty blocking list, and only
	// the first ends the loop.
	Judged bool
	// Blocking is the round's findings that hold the merge, in the round's own
	// order and as the reviewer wrote them — Apply answers entries, which carry
	// an index and not the finding, so the two are paired back here. It is what
	// the fixer is handed and what the owner check reads.
	Blocking []Finding
	// Deferred is the scores of the findings a *judge* deferred, and not the
	// ones the mode's table tolerated: those are already in rigor.Split's
	// tolerated half of the same round, and a policy that read them here as well
	// would count one finding twice and tolerate no round at all
	// (FindingsPolicy.Deferred).
	Deferred []rigor.Score
	// AllDeferred is the round the loop ends on: it named findings and every one
	// of them is deferred. A round that named none is not one of them — a `ship`
	// verdict is the loop's own end, and a `changes` verdict naming nothing is a
	// review that failed to say what it wanted, which no tolerance covers (D4).
	AllDeferred bool
}

// fixable is what the fixer may be handed for this round: the blocking subset
// where a judge was asked, the round's own findings where none was. The second
// half is F32 as much as the first is F4 — with the stage off, in POC or
// Critical, or for a repository that never opted in, the fixer is handed
// exactly what it was handed before the stage existed.
func (o triageOutcome) fixable(v Verdict) []Finding {
	if !o.Judged {
		return v.Findings
	}
	return o.Blocking
}

// triageOutcomeOf pairs one round's verdict back with Apply's answer: the
// findings the round still blocks on, and the scores of the ones a judge's own
// verdict deferred. A placement whose index is outside the round cannot name a
// finding of it, so it is passed over rather than read as the zero finding —
// Apply answers only indexes the round it was given, and this is the guard that
// keeps that true if it ever stops being.
func triageOutcomeOf(v Verdict, out triage.Outcome) triageOutcome {
	o := triageOutcome{Judged: true}
	for _, p := range out.Blocking {
		if p.Index >= 0 && p.Index < len(v.Findings) {
			o.Blocking = append(o.Blocking, v.Findings[p.Index])
		}
	}
	for _, p := range out.Deferred {
		if p.Source != triage.SourceAgent || p.Index < 0 || p.Index >= len(v.Findings) {
			continue
		}
		o.Deferred = append(o.Deferred, scoreOf(v.Findings[p.Index]))
	}
	o.AllDeferred = len(v.Findings) > 0 && len(o.Blocking) == 0
	return o
}

// triageRound is the step itself: it judges the findings of one review round,
// records what it answered, and reports what the round's findings are now worth
// to the loop that asked. It reports whether the loop may go on at all beside
// that: false is a stage failure that already ended the run, and the caller
// returns into its own loop the way it does after any other stage that failed.
//
// A judge the run could not reach or could not read is not a failure of the
// round: the outcome is the fail-safe one — every finding the table blocks
// blocks (D1846) — and the loop goes on with it, exactly as it does with a
// verdict that was read.
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
func (r *run) triageRound(ctx context.Context, round int, level policy.Level, v Verdict) (triageOutcome, bool) {
	if !r.triageOn() {
		return triageOutcome{}, true
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
		out := triage.Apply(mode, in, triage.Verdict{}, false)
		r.recordTriage(ctx, round, level, v, out, false)
		return triageOutcomeOf(v, out), true
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
		return triageOutcome{}, false
	}
	verdict, parsed := readTriageVerdict(res)
	if !parsed {
		r.d.Log.Warn("the triage round produced no readable verdict; every finding the mode's table blocks blocks",
			"round", round, "failed", !ok)
	}
	out := triage.Apply(mode, in, verdict, parsed)
	r.recordTriage(ctx, round, level, v, out, parsed)
	return triageOutcomeOf(v, out), true
}

// recordTriage writes one round to the record's triage block and folds its
// deferrals into the run's own. The two are the record's two halves of the same
// decision: Triage is the log of what the judge was asked and answered, round
// by round, and Deferred is what took effect — the findings a follow-up task is
// filed for, and the block the report and the pull request's body read (T-TR9).
//
// v is the round the placements were made on, and it is read for the one thing
// an entry does not carry: the reviewer's own words for the finding. Apply
// answers entries — index, key, word, reason, group — and the summary a
// follow-up task opens with is the finding's, so the two are paired here, by
// the same index the brief numbered and Apply placed on (D1850).
//
// A round is recorded whether or not the call answered, which is what makes a
// failed one visible rather than absent (runstore.TriageRound.Failed): the
// outcome it carries is Apply's fail-safe one, every finding the table blocks
// blocking.
func (r *run) recordTriage(ctx context.Context, round int, level policy.Level, v Verdict, out triage.Outcome, read bool) {
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
	r.noteDeferred(v, out)
	r.rec.Deferred = runstore.NewDeferred(r.deferred)
	r.save(ctx)
}

// noteDeferred folds one round's placements into the run's deferral ledger:
// what took effect, across every round and level of the run so far.
//
// A finding is filed by its key and never by its position (D1850): the same
// finding a later round raises again is the same entry, so a second deferral
// replaces the first's text rather than filing it twice, and a round that
// blocked it takes it back out — the finding is this round's work again, and a
// follow-up saying it was filed would send a reader after code the pull request
// carries. The order is the order the findings were first deferred in.
//
// The text comes from the finding and from the placement, redacted like every
// other model-written string the record keeps (agent.Redact): a reviewer's
// summary and a judge's reason both reach a follow-up task's prompt and the
// pull request's body.
func (r *run) noteDeferred(v Verdict, out triage.Outcome) {
	for _, p := range out.Blocking {
		r.forgetDeferred(p.Key)
	}
	for _, p := range out.Deferred {
		if p.Index < 0 || p.Index >= len(v.Findings) {
			continue
		}
		f := v.Findings[p.Index]
		r.rememberDeferred(runstore.DeferredFinding{
			Key:     p.Key,
			Summary: r.redact(f.Summary),
			Reason:  r.redact(p.Reason),
			Group:   r.redact(p.Group),
			Source:  runstore.TriageSource(p.Source),
		})
	}
}

// rememberDeferred files one deferral, replacing the entry the same finding
// already has.
func (r *run) rememberDeferred(e runstore.DeferredFinding) {
	for i := range r.deferred {
		if r.deferred[i].Key == e.Key {
			r.deferred[i] = e
			return
		}
	}
	r.deferred = append(r.deferred, e)
}

// forgetDeferred takes a finding back out of the ledger: the round it is read
// in blocked it, so it is not a follow-up any more.
func (r *run) forgetDeferred(key string) {
	for i := range r.deferred {
		if r.deferred[i].Key == key {
			r.deferred = append(r.deferred[:i], r.deferred[i+1:]...)
			return
		}
	}
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
