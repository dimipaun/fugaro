package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The checkpoint stage (plan T7.3, T6.1, D329, G7). A governance stop leaves a
// report of why the run is over and what a person deciding whether to continue
// it needs to know, and three of that report's fields are the stopping agent's
// own words: what the work had finished, what it would do next, and what it
// found hard to satisfy. They are asked for after the stop, from a fresh
// session, by the one call this stage makes.
//
// The stage is the core's because the halt it answers is the core's: what
// decides whether a stop gets a call at all is the stage hook's own decision
// (StageDecision.Checkpoint, governance_hook.go), and an untagged build never
// sets it — its hooks answer nothing, so this file runs no call in the MIT
// build whatever a run's layers asked for (D522, D527). The core owns the
// report, the record and the pull request; ee/governance owns the tripwire.

// checkpointStageName is the gateway stage the checkpoint call runs in. It is
// the name ee/governance's gateway half admits exactly one call for
// (gateway.CheckpointStage), and the two are one value written twice because
// neither side may import the other: the name is the contract between the
// core's stage and the ee/ hook that governs it.
const checkpointStageName = "checkpoint"

// checkpointMaxOutputTokens bounds the checkpoint call's answer, in tokens,
// and the stage's per-call limit in the gateway with it. The answer is three
// labelled lines, and a call admitted at the coder's own limit would let a
// stopped run spend a full stage's output on a report about the work it is no
// longer doing.
const checkpointMaxOutputTokens = 2000

// checkpointStage makes the one call that fills the stopping agent's three
// answers, and writes them into the report the halt left.
//
// It runs after the mode loop and before finalize, which is the only window it
// has: the call is a model call, so it must precede freezeHalts and
// closeGateway, and it must follow the stop — the agent process of the stage
// that tripped the tripwire is gone by then, which is what keeps the
// allowance the gateway admits (one call, in a stage of its own) from being
// spendable by the caller that was stopped.
//
// Everything about it is best-effort. A stop that is real whatever became of
// the call: a stage that cannot be set up, a gateway that refuses the call
// because the run's cap has no room, an agent that fails — each leaves the
// report the runner's own fields alone and warns. Nothing here may turn a
// governance halt into a failure, and nothing here may un-halt a run.
func (r *run) checkpointStage(ctx context.Context) {
	if r.gw == nil || !r.checkpointWanted() {
		return
	}
	h := r.haltValue()
	if h == nil {
		return
	}
	name := checkpointStageName
	log := r.d.Log.With("stage", name)
	pins, err := r.applyStageEffort(name, r.stagePins(name))
	if err != nil {
		log.Warn("the checkpoint stage was not set up; the run's checkpoint report keeps the runner's own fields", "err", r.redactErr(err))
		return
	}
	if err := r.writeManagedSettings(name, pins); err != nil {
		log.Warn("the checkpoint stage was not set up; the run's checkpoint report keeps the runner's own fields", "err", r.redactErr(err))
		return
	}
	env, err := r.stageEnv(name, pins)
	if err != nil {
		log.Warn("the checkpoint stage was not set up; the run's checkpoint report keeps the runner's own fields", "err", r.redactErr(err))
		return
	}
	// The stage's own bound, the same one every stage runs under: the run's
	// timeouts.stage, or the finalize reserve when that comes first. A run out
	// of time makes no call at all — the reserve is what finalize needs, and
	// spending it on a report would cost the run the pull request that carries
	// it.
	if r.budget.Exhausted() {
		log.Warn("the run is out of time; the checkpoint call is not made and the report keeps the runner's own fields")
		return
	}
	stageCtx, cancel := r.budget.StageContext(ctx)
	defer cancel()
	r.beginGatewayStage(name)
	r.mu.Lock()
	stopped := r.rec.Stage
	r.mu.Unlock()
	req := agent.Request{
		Prompt:    checkpointPromptFor(r.spec.Task, stopped, *h),
		SessionID: agent.NewSessionID(),
		Model:     r.stageModel(name),
		// The report is worth one call and no more: Claude Code's own budget
		// for it is the same small figure the gateway admits the call at.
		MaxBudgetUSD: r.cfg.Agent.MaxBudgetUSD,
		Dir:          r.d.WorkDir,
		Env:          env,
	}
	res, err := r.d.Agent.Run(stageCtx, req)
	violations, _ := r.endGatewayStage(name)
	// The stage's spend is the run's like any other stage's, and the ledger is
	// what the run's own figure is read from (recordGatewayStageCost).
	r.recordGatewayStageCost(ctx, name, res.CostUSD, true)
	if err != nil {
		log.Warn("the checkpoint call failed; the run's checkpoint report keeps the runner's own fields", "err", r.redactErr(err))
		return
	}
	if len(violations) > 0 {
		log.Warn("the gateway refused the checkpoint call; the run's checkpoint report keeps the runner's own fields", "violation", violations[0])
		return
	}
	done, next, hard := checkpointAnswer(res.Text)
	if done == "" && next == "" && hard == "" {
		log.Warn("the checkpoint call answered nothing the report could read; the run's checkpoint report keeps the runner's own fields")
		return
	}
	r.mu.Lock()
	if cp := r.rec.Checkpoint; cp != nil {
		filled := *cp
		filled.Done, filled.Next, filled.HardInstructions = done, next, hard
		r.rec.Checkpoint = runstore.NewCheckpoint(filled)
	}
	r.mu.Unlock()
	r.save(ctx)
	log.Info("the stopping agent answered the checkpoint prompt")
	// The draft the halt opened carries the runner's own fields until now: the
	// answers are what a person reading it came for, so the section is written
	// again with them. A failure only warns — finalize writes the same report
	// into the pull request's final section.
	r.checkpointPR(ctx, h.Reason)
}

// checkpointPromptFor is the checkpoint prompt plus the runner's own summary of
// the stop: the task, the stage the run stopped in, and why it is over. A fresh
// session knows nothing of any of them — it was started for this one call — so
// all three have to be here or the answer is a guess.
//
// The task is the person's own text, as it is in every other prompt this runner
// builds, and the summary is the runner's own words for the halt
// (haltReasonText) with the tripwire's detail beside them.
func checkpointPromptFor(task, stage string, h runstore.Halt) string {
	var b strings.Builder
	b.WriteString(checkpointPrompt)
	b.WriteString("\n\nYour task was:\n\n")
	b.WriteString(task)
	fmt.Fprintf(&b, "\n\nThe run stopped in stage %s: %s", stage, haltReasonText(h))
	if h.Detail != "" {
		fmt.Fprintf(&b, " (%s)", h.Detail)
	}
	b.WriteString(".\n")
	return b.String()
}

// checkpointAnswer reads the three labelled lines the checkpoint prompt asks
// for, and "" for a field the answer did not carry. It is deliberately
// forgiving: the answer is a model's prose and the report is worth having even
// when one label is missing or a line wraps, so a label starts a field wherever
// it appears in the answer, a continuation line extends the field it follows,
// and anything before the first label is dropped.
//
// Nothing here is trusted with markup: the fields are clipped by
// runstore.NewCheckpoint and rendered through inlineText, so an answer cannot
// forge a section of the pull request it lands on.
func checkpointAnswer(text string) (done, next, hard string) {
	fields := map[string]*string{
		"done":              &done,
		"next":              &next,
		"hard instructions": &hard,
	}
	current := ""
	for _, line := range strings.Split(text, "\n") {
		label, rest, found := strings.Cut(line, ":")
		if key := strings.ToLower(strings.TrimSpace(label)); found {
			if f, ok := fields[key]; ok {
				*f = strings.TrimSpace(rest)
				current = key
				continue
			}
		}
		if current == "" {
			continue
		}
		if s := strings.TrimSpace(line); s != "" {
			if f := fields[current]; *f != "" {
				*f += " " + s
			} else {
				*f = s
			}
		}
	}
	return done, next, hard
}
