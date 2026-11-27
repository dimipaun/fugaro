package runner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/runmsg"
)

// This file is the runner's side of a message that stops a coder stage where
// it stands: `--now`, which asks for the stage to be stopped at its next quiet
// point and resumed with the message (rule 2), and `--wrap-up`, which asks the
// same and then has the coder stage end (rule 3).
//
// Both are built on the agent's own yield (RM3, internal/agent/quiet.go): the
// stage's segment runs with a yield request carrying an ask this file closes,
// and the agent stops itself between two tool calls when that ask fires. The
// stop is not a failure — the session is intact and the stage is resumed in it
// (D2945) — so everything about it is the stage's own in-stage resume, under
// r.stage's accounting and deadlines rather than a second stage's.
//
// One message channel, one read: a stop is armed off the same pending queue
// the stage boundaries drain (messages.go), and what a stop delivers is taken
// out of that queue here. A flagged message that no stage is running waits,
// like any other, for the next coder stage (rule 2, one during a review).

const (
	// nowQuietWait is how long a `now` message may wait for a quiet point in
	// the segment it reached before the runner stops the segment where it
	// stands (rule 2). The bound is the owner's ten minutes; reaching it is
	// not a failure either, and the record says why the stop was not at a
	// quiet point.
	nowQuietWait = 10 * time.Minute
	// wrapUpBound is how long a wrap-up turn may run before the coder stage
	// ends and the run carries on (rule 3, D2946). It is a bound on the turn
	// and not on the run: a wrap-up cannot hold the run's end open.
	wrapUpBound = 15 * time.Minute
	// yieldStopAtOnce is the quiet-point bound the agent is given on a
	// wrap-up turn: the shortest wait there is, which the agent reads as
	// "stop here" (RM3, internal/agent/quiet.go). Zero would mean waiting for
	// a quiet point however long that takes, and the turn's own bound is the
	// whole of its clock — ten minutes on top of fifteen would let a coder
	// stage run to twenty-five (rule 3).
	yieldStopAtOnce = time.Nanosecond
	// yieldBudgetFloor is the smallest stage remainder a message may resume a
	// coder stage on: a cent. The budget reaches the agent as
	// --max-budget-usd, which carries two decimals and is left off the
	// command altogether when it is zero (internal/agent/agent.go), so a
	// segment resumed on what is left of a spent budget would run with no
	// per-stage cap at all — which is worse for the run than not resuming it
	// (rule 2).
	yieldBudgetFloor = 0.01
)

// The two reasons a stop carries in the record beside the message it delivered
// (rules 2 and 3). Both are written as the thing that happened and not as a
// failure: a stop is not one.
const (
	// yieldNoQuietPoint is a `now` whose bound was reached with no quiet
	// point found. The agent is stopped where it stands, which is what makes
	// its session possibly unresumable (rule 2's fallback below).
	yieldNoQuietPoint = "no quiet point in 10m"
	// yieldSessionLost is a message that reached a stage only after the
	// session the stop left could not be resumed: it is the run's second
	// attempt, in a fresh session.
	yieldSessionLost = "session lost at forced stop"
)

// The three paragraphs a resumed turn's prompt carries around the messages.
//
// The first is what F23 requires: the agent's stop is a stop of its whole
// process group, so every background task it had started is gone with the
// segment that started it, and a turn resumed without being told would wait on
// a shell that no longer exists.
const yieldResumedNote = "This turn resumes the session that was stopped so a message sent to this run could reach you, " +
	"at a point between your tool calls rather than at the end of your turn. Everything you had running in the background " +
	"was stopped with it: start again whatever you still need, and check what it had already done before you rely on it."

// The second is the same for a session that could not be resumed at all: the
// stop reached its bound, the session was left on an unanswered tool call, and
// the run starts the work again from the repository rather than from a
// conversation it no longer has.
const yieldFreshNote = "This turn starts a new session: the one that was stopped could not be resumed, " +
	"so nothing of that conversation is available here. Read `git log` and the working tree first — " +
	"what you had already committed is still there — and carry on from that, repeating nothing that is already done."

// The third is rule 3's instruction, which is the whole of what a wrap-up asks
// for by itself: the owner wants the run to stop with what it has and open the
// pull request for a person to look at.
const yieldWrapUpNote = "Wrap up now: commit what you have and write `pr.md`, then stop. " +
	"Do not start anything new, and do not leave work half-done in a way a person cannot pick up."

// yieldOpts is how a coder stage's turn is prompted after a message stopped
// it. It is carried by stageOpts (runner.go) and it is what makes a stage a
// coder stage for the message channel: a stage without it is never run with a
// yield request at all, so rule 1's deny-by-default holds for every other
// stage by construction.
type yieldOpts struct {
	// Resume builds the prompt of the turn that carries the block: the
	// stage's own prompt for the work — a first run's brief, a follow-up's
	// implement prompt, a fix's findings — with the block after it. A fix
	// builds it through FixPrompt, which is what carries rule 4's widening
	// line with the message (D2943).
	Resume func(block string) string
}

// stageBounds is a stage's own clock, kept from its first attempt to the ones
// that resume it: a stop and its resume are one stage, and twenty of them must
// not stretch a stage's deadline (rule 2). It is filled by stageOnce on the
// first attempt and read by every attempt after it.
type stageBounds struct {
	// deadline is what the run's own bound and the finalize reserve gave this
	// stage (Budget.StageContext).
	deadline time.Time
	// own is the stage's own governance.stage_timeout, when it applied.
	own time.Time
}

// yieldStage is one coder stage's messages across its segments: what the stop
// so far cost it, and what the segment now running is.
type yieldStage struct {
	// msgs are the messages the last stop was for: the turn now running is
	// carrying them, and the record says why if its own resume fails.
	msgs []runmsg.Message
	// wrapUp says the segment now running is the turn a wrap-up resumed:
	// when that turn ends, or at its own bound, the coder stage ends as
	// succeeded (rule 3, D2946).
	wrapUp bool
	// forced says the last stop reached its bound with no quiet point, so the
	// session it left may not be resumable (rule 2).
	forced bool
	// before is the run's model spend when the stage started: what the stage
	// has spent is what it is now less this, which is what the segment now
	// running gets the rest of (rule 2).
	before float64
	// remaining is what is left of agent.max_budget_usd for the segment now
	// running: a message must not buy the stage a second full budget
	// (rule 2). It is nil until the first stop, so a stage that is never
	// stopped runs under the configured value exactly as it did before.
	remaining *float64
}

// beginYield starts the watch that stops the segments of one coder stage. The
// caller stops it when the stage ends (defer).
func (r *run) beginYield(name string) *coderYield {
	y := &coderYield{r: r, name: name, done: make(chan struct{})}
	go y.watch()
	return y
}

// coderYield is one coder stage's asking side: the ask the segment running now
// has (if any), and the watch that closes it.
type coderYield struct {
	r    *run
	name string
	// done ends the watch and the wrap-up timer with the stage.
	done     chan struct{}
	stopOnce sync.Once
	// mu guards ask and asked. It is never held with the run's other locks.
	mu sync.Mutex
	// ask is the ask of the segment running now, or nil between segments.
	ask chan struct{}
	// asked is whether that ask has already been closed: closing it twice
	// would panic, and the watch and the wrap-up timer can both want to.
	asked bool
}

// watch closes the segment's ask when a message carrying a flag reaches the
// run's queue (rule 2). It is the cancel watch's cadence and for the same
// reason: a message is an object a launcher wrote, and a poll is the run's one
// window onto it. The read itself is the poller's job (messages.go) — this
// only asks whether something is already pending.
func (y *coderYield) watch() {
	t := time.NewTicker(y.r.d.CancelPoll)
	defer t.Stop()
	for {
		select {
		case <-y.done:
			return
		case <-t.C:
			y.askIfAsked()
		}
	}
}

// askIfAsked closes the current ask when a flagged message is pending for it.
// A flag that arrives while no segment is armed is not lost: the message stays
// in the queue and the next segment's arming sees it, which is what makes a
// `now` during a review wait for the next coder stage (rule 2).
func (y *coderYield) askIfAsked() {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.ask == nil || y.asked {
		return
	}
	m, ok := y.r.flaggedPending()
	if !ok {
		return
	}
	y.r.d.Log.Info("a message stops this coder stage at its next quiet point", "stage", y.name,
		"seq", m.Seq, "flags", strings.Join(m.Flags, ","))
	y.asked = true
	close(y.ask)
}

// segment arms one segment of the stage and returns the ask it runs under and
// the bound the agent waits for a quiet point under once that ask closes: rule
// 2's ten minutes for the segment a `now` stopped, and at once for a wrap-up
// turn, whose own clock is rule 3's. A wrap-up turn is armed with that bound as
// well: rule 3 ends the coder stage fifteen minutes into the turn whatever the
// agent does.
func (y *coderYield) segment(wrapUp bool) (<-chan struct{}, time.Duration) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.ask, y.asked = make(chan struct{}), false
	ask := y.ask
	wait := nowQuietWait
	if wrapUp {
		wait = yieldStopAtOnce
		go y.bound(ask)
	}
	return ask, wait
}

// endSegment disarms the segment that just ended, so a later ask cannot close
// an ask nobody is running under.
func (y *coderYield) endSegment() {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.ask = nil
}

// bound closes a wrap-up turn's ask when the bound passes (rule 3).
func (y *coderYield) bound(ask chan struct{}) {
	t := time.NewTimer(y.r.wrapUpLimit())
	defer t.Stop()
	select {
	case <-t.C:
		y.mu.Lock()
		defer y.mu.Unlock()
		if y.ask != ask || y.asked {
			return
		}
		y.asked = true
		close(ask)
		y.r.d.Log.Info("the wrap-up turn reached its bound; the coder stage ends", "stage", y.name)
	case <-y.done:
	}
}

// stop ends the watch with the stage it belongs to.
func (y *coderYield) stop() {
	y.stopOnce.Do(func() { close(y.done) })
}

// wrapUpLimit is how long one wrap-up turn may run before the coder stage ends
// (rule 3): the owner's fifteen minutes, or the shorter one a test injects.
func (r *run) wrapUpLimit() time.Duration {
	if r.d.WrapUpBound > 0 {
		return r.d.WrapUpBound
	}
	return wrapUpBound
}

// flaggedPending reports the first message waiting for a coder stage that asks
// to stop one, and whether there is one.
func (r *run) flaggedPending() (runmsg.Message, bool) {
	if !r.msgs.on {
		return runmsg.Message{}, false
	}
	r.msgs.mu.Lock()
	defer r.msgs.mu.Unlock()
	for _, m := range r.msgs.pending {
		if yieldFlag(m) != "" {
			return m, true
		}
	}
	return runmsg.Message{}, false
}

// yieldFlag is the flag of a message that asks to stop a coder stage: wrap_up
// when it carries one — that message ends the stage, so it is the stronger of
// the two readings — else now, else "".
func yieldFlag(m runmsg.Message) string {
	switch {
	case slices.Contains(m.Flags, runmsg.FlagWrapUp):
		return runmsg.FlagWrapUp
	case slices.Contains(m.Flags, runmsg.FlagNow):
		return runmsg.FlagNow
	default:
		return ""
	}
}

// claimYield takes the messages waiting for a coder stage out of the queue and
// records each delivered into stage, with reason when the stop did not find a
// quiet point (rules 2 and 3). Every pending message goes, not just the flagged
// one the stop was for: the turn this resumes is one of rule 1's delivery
// points, so a message already waiting when the stop fired rides into it too,
// in number order, rather than waiting for a boundary the stage may never
// reach. It returns what it took, and nothing when nothing is waiting — a
// boundary that drained the queue first has delivered those already, and a
// stop with no message to carry is the stage's last word.
func (r *run) claimYield(stage, reason string) []runmsg.Message {
	r.msgs.mu.Lock()
	defer r.msgs.mu.Unlock()
	if len(r.msgs.pending) == 0 {
		return nil
	}
	out := r.msgs.pending
	r.msgs.pending = nil
	at := r.d.Now().UTC()
	for _, m := range out {
		r.deliverReason(m.Seq, stage, reason, at)
	}
	r.publishMessages()
	return out
}

// deliverReason is deliver (messages.go) with the reason a stop carries when it
// was not at a quiet point; msgs.mu is held.
func (r *run) deliverReason(seq int, stage, reason string, at time.Time) {
	for i := range r.msgs.dels {
		d := r.msgs.dels[i]
		if d.Seq != seq {
			continue
		}
		stamp := at
		r.msgs.dels[i] = runmsg.Delivery{
			Seq: seq, SentAt: d.SentAt, SentBy: d.SentBy, Flags: d.Flags,
			State: runmsg.StateDelivered, Stage: stage, DeliveredAt: &stamp, Reason: reason,
		}
		return
	}
}

// retagYield rewrites what the record says about messages already delivered by
// a stop whose resume then failed (rule 2's fallback): they reached the stage
// after all, in a fresh session, and the record says so.
//
// Each entry is rebuilt as a fresh value, the way deliver and deliverReason
// write one: publishMessages hands out a shallow copy of the block, so a field
// set on an entry a reader already holds would change a record under it, and a
// value built whole is the same shape every other write of an entry has.
func (r *run) retagYield(msgs []runmsg.Message, reason string) {
	r.msgs.mu.Lock()
	defer r.msgs.mu.Unlock()
	next := make([]runmsg.Delivery, len(r.msgs.dels))
	copy(next, r.msgs.dels)
	for _, m := range msgs {
		for i := range next {
			d := next[i]
			if d.Seq != m.Seq {
				continue
			}
			next[i] = runmsg.Delivery{Seq: d.Seq, SentAt: d.SentAt, SentBy: d.SentBy, Flags: d.Flags,
				State: d.State, Stage: d.Stage, DeliveredAt: d.DeliveredAt, Reason: reason}
		}
	}
	r.msgs.dels = next
	r.publishMessages()
}

// resumeBlock is what the turn a message resumed carries: the messages that
// stopped the stage, in RM2's fenced block, after the paragraph that says what
// the stop did to the session and, for a wrap-up, after rule 3's instruction.
// The block goes last so the messages are the closest thing to the work.
func (r *run) resumeBlock(msgs []runmsg.Message, wrapUp, fresh bool) string {
	var b strings.Builder
	if fresh {
		b.WriteString(yieldFreshNote)
	} else {
		b.WriteString(yieldResumedNote)
	}
	b.WriteString("\n\n")
	if wrapUp {
		b.WriteString(yieldWrapUpNote)
		b.WriteString("\n\n")
	}
	b.WriteString(MessageBlock(msgs, followup.NewNonce(), r.redact))
	return b.String()
}

// stageSpend is the run's model spend so far in US dollars. The stop's own
// accounting is read off the difference across the stage (rule 2).
func (r *run) stageSpend() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rec.CostUSD
}

// stageRemaining is what is left of the stage's model budget for the segment
// now running: agent.max_budget_usd less what the stage has spent since it
// started. It can be negative, because a stage that went past its budget is
// below zero rather than at it, and a stop refuses to resume on anything under
// yieldBudgetFloor (yieldStopped).
func (r *run) stageRemaining(ys *yieldStage) float64 {
	return r.stageCapUSD() - (r.stageSpend() - ys.before)
}

// stageCapUSD is agent.max_budget_usd as this run's stages really spend it
// (R20, RT2): the repository's figure where it tightens Fugaro's own per-stage
// cap, and the cap where the project layer says nothing about the key and the
// figure is larger, so a silent project's runs stop at the baseline. The
// launcher's preview takes the same judgement (internal/cli's budget preview),
// so the figure quoted and the figure a stage runs under agree.
//
// The task's own override is not a repository's figure and is kept: it is this
// run's word, taken ahead of every layer exactly as the plan takes
// Overrides.ReviewRounds (planOf), and Spec.Apply wrote it into the field this
// reads.
func (r *run) stageCapUSD() float64 {
	if o := r.spec.Overrides.MaxBudgetUSD; o != nil && *o > 0 {
		return *o
	}
	return config.TightenUSD(r.cfg, r.cfg.Agent.MaxBudgetUSD)
}

// stageBudget is the --max-budget-usd an attempt runs under: the run's own
// agent.max_budget_usd as the run really reads it (stageCapUSD), or, for a
// segment resumed after a stop, the stage's remainder — which a stop only ever
// resumes on above yieldBudgetFloor, so a resumed segment is never run without
// a cap.
func (r *run) stageBudget(o stageOpts) float64 {
	if o.BudgetUSD == nil {
		return r.stageCapUSD()
	}
	return *o.BudgetUSD
}

// stageDeadline is the deadline an attempt runs under: the run's own bound and
// the finalize reserve (Budget.StageContext), or, on an attempt that resumes a
// stage a message stopped, the deadline that stage's first attempt computed. A
// stop does not reset the stage's clock (rule 2).
func (o stageOpts) stageDeadline(ctx context.Context, b Budget) (context.Context, context.CancelFunc) {
	if o.Bounds != nil && !o.Bounds.deadline.IsZero() {
		return context.WithDeadline(ctx, o.Bounds.deadline)
	}
	return b.StageContext(ctx)
}

// yieldRecoverable is the recoverable predicate a segment resumed after a
// forced stop runs with: the agent not finding the session that stop left is
// rule 2's fallback and not a failed stage, while everything the caller
// recovers stays its own.
func yieldRecoverable(prev func(error) bool) func(error) bool {
	return func(err error) bool {
		if errors.Is(err, agent.ErrNoSession) {
			return true
		}
		return prev != nil && prev(err)
	}
}

// yieldStopped is what a stop did, in the stage loop's terms: it hands back the
// request the stage is resumed with, or reports that the coder stage ends here
// — rule 3's wrap-up turn, whose ending is the stage's own success (D2946), or
// a stop the stage has no budget left to resume under (yieldBudgetFloor).
func (r *run) yieldStopped(name string, res agent.Result, req *agent.Request, opts stageOpts, ys *yieldStage) (agent.Request, bool) {
	if ys.wrapUp {
		return *req, true
	}
	reason := ""
	if res.YieldReason == agent.YieldAtBound {
		reason = yieldNoQuietPoint
	}
	// A stop resumes the stage only under a cap the stage can still be given:
	// what is left of its budget has to be a figure `--max-budget-usd` can
	// state, or the resumed segment would run with no per-stage cap at all
	// (yieldBudgetFloor). The coder stage ends here instead, and the message
	// is left in the queue — the run's last read records it as one no coder
	// stage was left to carry, rather than as delivered into a turn that
	// never ran.
	remaining := r.stageRemaining(ys)
	if remaining < yieldBudgetFloor {
		r.d.Log.Info("the coder stage's budget is spent; a message cannot resume it", "stage", name,
			"remaining", remaining)
		return *req, true
	}
	ys.msgs = r.claimYield(name, reason)
	if len(ys.msgs) == 0 {
		// No message asked for this stop — one was delivered at a boundary
		// while the segment ran, or the sender withdrew nothing the runner
		// can see — so there is nothing to resume with.
		return *req, true
	}
	ys.wrapUp = wrapUpAsked(ys.msgs)
	ys.forced = reason != ""
	next := *req
	next.Prompt = opts.Yield.Resume(r.resumeBlock(ys.msgs, ys.wrapUp, false))
	// The session the stopped segment ran in: what its result reported, or
	// the one it was asked for when it reported none.
	session := res.SessionID
	if !agent.ValidSessionID(session) {
		session = req.SessionID
	}
	next.SessionID, next.Resume = session, true
	// The stopped segment is charged here, and only where the stage goes on
	// in another one: the caller charges the segment that ends the stage, and
	// charging this one in both places would count one segment's tokens
	// twice against the run's ceiling (countTokens). Every ending above
	// returns before this, so the caller's own count is the only one they
	// get.
	r.countTokens(res)
	ys.remaining = &remaining
	return next, false
}

// wrapUpAsked reports whether any of the messages that stopped a stage asked
// the run to finish (rule 3).
func wrapUpAsked(msgs []runmsg.Message) bool {
	for _, m := range msgs {
		if slices.Contains(m.Flags, runmsg.FlagWrapUp) {
			return true
		}
	}
	return false
}

// reportMessages is the part of the report's Messages section that only the
// runner's memory holds: each message's text, and the pull request an
// undelivered one would be sent on (F19). A message's text is never read back
// from the record — which carries none — nor from the object under tokens/,
// which a run's end may well be the last thing to see.
func (r *run) reportMessages(pr int) *ReportMessages {
	r.msgs.mu.Lock()
	defer r.msgs.mu.Unlock()
	if !r.msgs.on {
		return nil
	}
	texts := r.msgs.texts
	if len(texts) == 0 && len(r.msgs.dels) == 0 {
		return nil
	}
	out := make(map[int]string, len(texts))
	for seq, text := range texts {
		out[seq] = text
	}
	return &ReportMessages{Texts: out, Repo: r.spec.Repo, PR: pr}
}

// messageFlags are the flags this runner honours, as the record lists them
// (F25): what `fugaro tell` reads before it sends a flag. A run that reads no
// messages lists none, and every flag is refused for it.
func (r *run) messageFlags() []string {
	if !r.msgs.on {
		return nil
	}
	return []string{runmsg.FlagNow, runmsg.FlagWrapUp}
}
