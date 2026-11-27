package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
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
	// What the run itself had done, read before the call and not by it: the
	// session this call starts has no tools and no checkout of its own, so the
	// only facts the answer can rest on are the ones the prompt carries
	// (checkpointPromptFor).
	commits, files := r.checkpointWork(stageCtx)
	req := agent.Request{
		Prompt:    checkpointPromptFor(r.spec.Task, stopped, *h, commits, files),
		SessionID: agent.NewSessionID(),
		Model:     r.stageModel(name),
		// The run's own agent.max_budget_usd as the run really reads it
		// (stageCapUSD, R20), the figure every stage's call is launched with
		// (runner.go). It is the agent's own accounting and not what bounds
		// this call: the checkpoint stage is admitted one call, at the small
		// output limit it is pinned to (checkpointMaxOutputTokens).
		MaxBudgetUSD: r.stageCapUSD(),
		// The posture is the triage judge's (F25, F29): this call writes a
		// report and touches nothing else. The one-call allowance bounds the
		// stage's spend and not what the session may do — a tool use in the
		// reply, a hook the checkout's settings declare and an MCP server its
		// .mcp.json names would all run after the halt, in the tree finalize is
		// about to commit and push. Both lists are empty and not nil, for the
		// reason agent.Request spells the two readings apart: a nil one is the
		// flag's absence, which is Claude Code's own default of every level and
		// every built-in tool.
		SettingSources:  []string{},
		Tools:           []string{},
		StrictMCPConfig: true,
		Dir:             r.d.WorkDir,
		Env:             env,
	}
	// The stage's own clock starts at its call, as every other stage's does
	// (stageOnce): the row below is a stage's timing and not this stage's setup.
	started := r.d.Now()
	res, err := r.d.Agent.Run(stageCtx, req)
	violations, _ := r.endGatewayStage(name)
	// The stage's spend is the run's like any other stage's, and the ledger is
	// what the run's own figure is read from (recordGatewayStageCost). Its row
	// in the record is appended as stageOnce appends one, before the outcome is
	// read: whatever became of the call, its dollars are in the run's figure
	// and in its split, and a call that reaches cost.model_usd and
	// cost.model_by with no stage of its own would leave the record's own
	// reconciliation short by exactly its cost (runstore.StageTiming).
	r.recordGatewayStageCost(ctx, name, res.CostUSD, true)
	r.rec.Stages = append(r.rec.Stages, r.stageTiming(name, started, res.CostUSD, res.SubAgents, res.Repeats))
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
// the stop: the task, the stage the run stopped in, why it is over, and what the
// run itself had done by then. A fresh session knows nothing of any of them — it
// was started for this one call — so all of them have to be here or the answer
// is a guess, and the guess is written into the record and the pull request a
// person reads to decide whether to continue the run.
//
// commits and files are that last part, read from the run's own branch
// (checkpointWork): the subjects of the commits it made and the paths it
// changed. They are what the answer's "what the work had finished" rests on. A
// nil list is a read the runner could not make and the prompt says nothing about
// it; an empty one is a run that had made no commit, or changed no path, when it
// stopped — a fact the answer may rest on, and the reason the two are not the
// same value.
//
// The task is the person's own text, as it is in every other prompt this runner
// builds, and the summary is the runner's own words for the halt
// (haltReasonText) with the tripwire's detail beside them.
func checkpointPromptFor(task, stage string, h runstore.Halt, commits, files []string) string {
	var b strings.Builder
	b.WriteString(checkpointPrompt)
	b.WriteString("\n\nYour task was:\n\n")
	b.WriteString(task)
	fmt.Fprintf(&b, "\n\nThe run stopped in stage %s: %s", stage, haltReasonText(h))
	if h.Detail != "" {
		fmt.Fprintf(&b, " (%s)", h.Detail)
	}
	b.WriteString(".\n")
	b.WriteString(checkpointWorkSection(commits, files))
	return b.String()
}

// checkpointCommitsShown, checkpointFilesShown and checkpointItemRunes bound
// what the checkpoint prompt carries of the run's own work. The three fields the
// answer fills are wanted in a prompt rather than in a file the call could page
// through, and a run that made two hundred commits or changed a thousand paths
// would spend the call's input bound on the list of them. The count is the
// honest half — a list that stopped silently would read as the whole of the
// work — so the section says how many it left out.
const (
	checkpointCommitsShown = 20
	checkpointFilesShown   = 40
	checkpointItemRunes    = 200
)

// checkpointWorkSection is what the run itself had done when it stopped, as its
// own branch has it: the subjects of its commits and the paths it changed. It is
// "" when neither list was read, and a list the runner has nothing for is named
// as such rather than left blank — the answer to "what the work had finished"
// rests on these facts, so a blank where a list belongs has to read as "none"
// and never as the runner having kept quiet.
func checkpointWorkSection(commits, files []string) string {
	if commits == nil && files == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nWhat this run itself had done when it stopped, read from its branch:")
	b.WriteString(checkpointItems("Commits it made, newest first", commits, checkpointCommitsShown, "commit"))
	b.WriteString(checkpointItems("Paths it changed", files, checkpointFilesShown, "path"))
	return b.String()
}

// checkpointItems is one list of checkpointWorkSection: its heading, one line
// per item, and a line naming what did not fit. A nil list is a read that could
// not be made and says nothing at all; a list that was read and is empty is
// answered "none". Each item is clipped and folded onto one line: a subject and
// a path are the run's own agent's words, and neither may pass for a line of
// this prompt's own.
func checkpointItems(heading string, items []string, shown int, noun string) string {
	if items == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n%s:", heading)
	if len(items) == 0 {
		return b.String() + " none.\n"
	}
	b.WriteString("\n")
	for i, item := range items {
		if i == shown {
			break
		}
		fmt.Fprintf(&b, "- %s\n", safetext.Clip(safetext.OneLine(item), checkpointItemRunes))
	}
	if n := len(items) - shown; n > 0 {
		fmt.Fprintf(&b, "%s not shown.\n", Plural(n, noun))
	}
	return b.String()
}

// checkpointWork reads what the run itself had done, for the checkpoint prompt:
// the subjects of the commits it made, newest first, and the paths it changed
// (gitops.CommitSubjects, gitops.ChangedFiles), both read from the revision its
// own work starts at (checkpointBase) and so both the run's own work rather than
// what the branch already carried when it started.
//
// Best-effort, like everything else about this stage: a read that fails leaves
// that list nil and warns, and the prompt then says nothing about it rather than
// reporting a run that had done nothing. That is the line agent.Request draws
// for the flags it passes, and the one the record's own changed-paths field
// draws between a run that recorded none and an empty set.
func (r *run) checkpointWork(ctx context.Context) (commits, files []string) {
	if r.repo == nil {
		return nil, nil
	}
	base := r.checkpointBase(ctx)
	if list, err := r.repo.CommitSubjects(ctx, base); err != nil {
		r.d.Log.Warn("the commits this run made could not be read; the checkpoint prompt goes without them", "err", r.redact(err.Error()))
	} else {
		commits = list
	}
	paths, err := r.repo.ChangedFiles(ctx, base)
	switch {
	case err != nil:
		r.d.Log.Warn("the paths this run changed could not be read; the checkpoint prompt goes without them", "err", r.redact(err.Error()))
	case paths == nil:
		// A run that changed nothing reads as none rather than as unread: only
		// the error above leaves the paths out of the prompt.
		files = []string{}
	default:
		files = paths
	}
	return commits, files
}

// checkpointBase is the revision the checkpoint prompt's own facts are read
// from: the commit the run's own range was narrowed to while it still leads to
// HEAD (narrowedLeading), and the commit the run was cut from otherwise
// (workBase). Both are the commit this run's own commits start after, so what
// the prompt carries is the run's own work and not a person's commits that were
// already on the branch — and the narrowed commit is the same one in every case
// but a run whose work was cut from a ref other than the one it was launched
// with.
func (r *run) checkpointBase(ctx context.Context) string {
	if nb := r.narrowedLeading(ctx); nb != "" {
		return nb
	}
	return r.workBase()
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
