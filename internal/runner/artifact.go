package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// renderTree is the tree a render runs in: the working checkout, uncommitted
// work included, which the plan stage shared. It is recorded in
// runstore.RenderRecord.Tree, whose only legal value it is today — the
// design's D8 is what would add a second one (plan P2).
const renderTree = "working"

// renderPlanPath is where a mode: artifact run's plan stage is asked to leave
// its plan: a file in the checkout, read by nobody but a person (the render
// the runner runs is commands.render, never a plan the model wrote).
const renderPlanPath = ".fugaro/render-plan.json"

// runArtifactMode runs artifactMode, recovering a panic so the run always
// ends with a record, the same guarantee runAgentLoop and runReviewMode
// give their own modes.
func (r *run) runArtifactMode(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			// The same priority every other end branch gives a halt: it
			// decides the run whatever else is true, a panic mid-stage
			// included.
			if h := r.haltValue(); h != nil {
				r.endArtifactHalted(ctx, h)
				return
			}
			r.failArtifactInfra(ctx, fmt.Errorf("stage %s panicked: %v", r.rec.Stage, p))
		}
	}()
	r.artifactMode(ctx)
}

// artifactMode runs a mode: artifact run (design artifact-runs.md §3): the
// optional plan stage, then the one render step. The run ends with outcome
// artifacts on success, or failed/none otherwise; it never pushes, never
// opens a pull request and sets r.skipPR, so finalize, which still runs
// after, does nothing more.
//
// An artifact run's agent gets no git credentials (the agent env block and
// refreshGitAuth both skip r.artifact, exactly as they skip r.review): the
// deliverable goes to the runs bucket, never to a branch, so a push
// credential in its environment would only be reachable by the plan stage's
// own prompt injection for nothing.
func (r *run) artifactMode(ctx context.Context) {
	if r.artifactPlan {
		r.rec.Stage = "plan"
		r.save(ctx)
		req := agent.Request{Prompt: r.artifactPlanPrompt(), SessionID: agent.NewSessionID()}
		res, ok, _ := r.stage(ctx, "plan", req, stageOpts{})
		r.countTokens(res)
		if !ok {
			// The same priority gate.go's own error branch follows: a halt
			// decides the run whatever else is true, before cancel is even
			// asked about.
			if h := r.haltValue(); h != nil {
				r.endArtifactHalted(ctx, h)
				return
			}
			if r.isCancelled() {
				r.endArtifactCancelled(ctx, "cancelled during the plan stage")
				return
			}
			r.endArtifactFailed(ctx)
			return
		}
		// r.stage checked haltValue() right before returning ok=true, but a
		// halt can land in the window between that check and here.
		if h := r.haltValue(); h != nil {
			r.endArtifactHalted(ctx, h)
			return
		}
	}
	rec, err := r.runRender(ctx)
	r.renderRec = renderRecordFor(r.wf.Commands.Render, rec)
	r.rec.Render = r.renderRec
	r.updateCost()
	switch {
	case err == nil:
		if h := r.haltValue(); h != nil {
			r.endArtifactHalted(ctx, h)
			return
		}
		r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeArtifacts
	case r.haltValue() != nil:
		// A halt that landed while the render ran: recorded by the stage's
		// own machinery (the budget backend polls independently), and a halt
		// decides the run whatever the render did. Both of these end the run
		// themselves — status, reason, skipPR and the report — so they return:
		// falling through would store the report a second time, as
		// reviewmode.go's equivalent branches do not.
		r.endArtifactHalted(ctx, r.haltValue())
		return
	case r.isCancelled():
		r.endArtifactCancelled(ctx, "cancelled during render")
		return
	default:
		r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, r.redact(err.Error())
		r.d.Log.Warn("the render step failed", "err", r.rec.Reason)
	}
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// renderRecordFor is the Record.Render to store for what runRender returned,
// or nil when no render ran. runRender's blank-command arm returns before it
// executed anything, and its record describes no render: a blank command,
// the sha256 of the empty string, exit code -1. runstore.Record.Render
// documents a blank command as none, and schemas/result.schema.json's render
// description says the same ("absent ... for an artifact run refused before
// its render step"), so that record is dropped here rather than stored as an
// artifact run's render. bootstrap's checkGateCommands refuses such a recipe
// first, so this is the fail-closed arm behind it, not a reachable launch.
func renderRecordFor(command string, rec runstore.RenderRecord) *runstore.RenderRecord {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	return &rec
}

// runRender runs commands.render once, in the checkout, under
// timeouts.render and the run's own finalize reserve, with the agent's own
// environment (the workflow's secrets included, design §8.6/D10). Its exit
// code decides the run.
//
// The command string and the timeout come from the runner's IN-MEMORY
// workflow config (r.wf.Commands.Render, r.wf.Timeouts.Render), which
// bootstrap set once at runner.go and nothing re-reads: this function is
// NOT a verify.LoadSettings caller. That is deliberate, not an omission —
// <StateDir>/workflow.json is 0644 inside a directory the plan-stage agent
// can write (it is the same user in the same container), and
// verify.LoadSettings re-reads it per invocation. A render that loaded its
// command from there could be made to run a string the reviewer never read,
// while render.command_sha256 attested to it (design §3, Review Focus 1).
//
// The record's Command is the redacted string and CommandSHA256 the hash of
// the string the runner RAN (the unredacted one), so the two are one hash
// pair exactly when the redactor did not fire (design D8).
func (r *run) runRender(ctx context.Context) (runstore.RenderRecord, error) {
	command := r.wf.Commands.Render
	started := r.d.Now()
	rec := runstore.RenderRecord{
		Command:       r.redact(command),
		CommandSHA256: sha256Hex(command),
		Tree:          renderTree,
		ExitCode:      -1,
		StartedAt:     started.UTC().Format(time.RFC3339),
	}
	if strings.TrimSpace(command) == "" {
		// Fail closed: bootstrap's checkGateCommands already refuses a
		// recipe whose render step has no commands.render, but a caller is
		// never trusted to have checked first, and an empty command would
		// otherwise run as a successful no-op.
		return rec, errors.New("commands.render is empty")
	}
	r.rec.Stage = "render"
	r.save(ctx)
	deadline, cutByReserve := r.budget.StepDeadline(r.wf.Timeouts.Render.Duration)
	stageCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	// The render's output is a repository's own command's, so it goes
	// through the same redactor the agent's streams do, whole lines at a
	// time, and only its tail is kept.
	tail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	out := agent.NewRedactor(tail, r.secretList())
	code, err := procgroup.Run(stageCtx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: r.d.WorkDir, Env: r.env, Stdout: out, Stderr: out,
	})
	// Flushed before the tail is read: the redactor holds a line until a
	// newline arrives, so a render whose last line has none — a failure
	// message, typically — would be missing from the tail exactly when it
	// explains the failure.
	out.Flush()
	rec.DurationS = r.d.Now().Sub(started).Seconds()
	rec.ExitCode = code
	rec.TimedOut = errors.Is(stageCtx.Err(), context.DeadlineExceeded)
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{Name: "render", StartedAt: started, DurationS: rec.DurationS})
	log := r.d.Log.With("stage", "render")
	switch {
	case rec.TimedOut && cutByReserve:
		// The run's own reserve ended the render, not timeouts.render: the
		// deadline above is whichever of the two comes first, and the
		// reason says which one it was, so the record never blames a
		// timeout that had time left to run (design §4.5).
		log.Warn("commands.render cut at the finalize reserve", "reserve", r.budget.Reserve, "total", r.budget.Total, "exit_code", code)
		r.noteRenderTail(tail)
		return rec, fmt.Errorf("commands.render was cut at the run's finalize reserve, not at its timeouts.render (%s): the %s run budget keeps %s for finalize",
			r.wf.Timeouts.Render.Duration, r.budget.Total, r.budget.Reserve)
	case rec.TimedOut:
		// procgroup.Run reports a command killed by ctx as ctx.Err().
		log.Warn("commands.render timed out", "timeout", r.wf.Timeouts.Render.Duration, "exit_code", code)
		r.noteRenderTail(tail)
		return rec, fmt.Errorf("commands.render timed out after %s", r.wf.Timeouts.Render.Duration)
	case err != nil:
		return rec, fmt.Errorf("running commands.render: %w", err)
	case code != 0:
		log.Warn("commands.render failed", "exit_code", code)
		r.noteRenderTail(tail)
		return rec, fmt.Errorf("commands.render exited %d", code)
	}
	log.Info("commands.render finished", "exit_code", code, "duration_s", rec.DurationS)
	for _, l := range tail.Lines() {
		log.Info("render output", "line", l)
	}
	return rec, nil
}

// noteRenderTail keeps the render's last lines as the report's log tail: a
// failed render's output is the one thing that explains it.
func (r *run) noteRenderTail(tail *logtail.Writer) {
	lines := tail.Lines()
	if len(lines) == 0 {
		return
	}
	r.tail = &LogTail{Source: "the render step", Lines: r.redactAll(lines)}
}

// artifactPlanPrompt is the plan stage's prompt (design §3): the task text,
// the recipe's own use_when, and what the run will render afterwards. It is
// the only place the run describes the deliverable to the agent, so it says
// plainly that the render is the runner's, not the agent's.
//
// When the task attached files it names the directory they were downloaded
// into, absolute and the same value FUGARO_INPUTS_DIR carries in this run's
// environment; a task that attached nothing gets no such paragraph, so a
// plan stage is never pointed at a directory that does not exist.
func (r *run) artifactPlanPrompt() string {
	var b strings.Builder
	b.WriteString("This is a Fugaro artifact run: the deliverable is a set of files uploaded to this run's bucket, not a pull request. There is no pull request to open, no branch is pushed, and the plan you write is not the deliverable.\n\n")
	if len(r.spec.Inputs) > 0 {
		fmt.Fprintf(&b, "The task attached %d file(s): they are in `%s` (also named by FUGARO_INPUTS_DIR), already verified against the hashes in the task, and read-only input to you — the run was given them, it did not produce them. They are excluded from git, so `git add -A` never commits one.\n\n",
			len(r.spec.Inputs), filepath.Join(r.d.WorkDir, inputsDir))
	}
	name := r.rec.Recipe.Name
	fmt.Fprintf(&b, "The runner renders the deliverable itself, once, after this stage: it runs the workflow's own render command (commands.render in fugaro.yaml at %s) in this checkout and uploads the files the recipe's outputs.globs name. Do not run that render command, and do not try to produce the artifacts yourself.\n\n", r.spec.Ref)
	if u := r.useWhen; u != "" {
		fmt.Fprintf(&b, "The recipe `%s` is for: %s\n\n", name, u)
	}
	if r.out != nil && len(r.out.Globs) > 0 {
		fmt.Fprintf(&b, "The artifacts it expects are the files matching: %s (at most %d files, %s in total).\n\n",
			strings.Join(r.out.Globs, ", "), r.out.FileCount(), fmtBytes(r.out.MaxSetBytes()))
	}
	fmt.Fprintf(&b, "Write your plan for the render to `%s` in this checkout: what the render will have to produce, exactly which files it should leave behind and where, what inputs in the repository it needs, and what is likely to go wrong. Keep it short and concrete.\n\n", renderPlanPath)
	if t := strings.TrimSpace(r.spec.Task); t != "" {
		b.WriteString("**Task:**\n\n" + t + "\n")
	}
	return b.String()
}

// fmtBytes is an artifact cap for a prompt: a whole number of bytes with a
// human unit, never a bare digit string a reader has to decode.
func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// storeArtifactEnded stores an artifact run's report.md — the ordinary run
// report, whose outcome arm and Artifacts section are this task's (report.go)
// — under every terminal state below.
//
// The write is detached from ctx's cancellation and bounded, as save's is:
// this is the only write of report.md for this mode (r.skipPR means finalize
// stores none), and one of the states below IS the cancelled run, whose ctx
// is already cancelled by the time it is reached — so a write on the live ctx
// would refuse exactly the report that explains the cancel.
func (r *run) storeArtifactEnded(ctx context.Context) {
	report := agent.Redact(followUpReport(r.rec, r.d.Store.Prefix(), r.tail, nil, nil), r.secretList())
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
	defer cancel()
	r.storeReport(wctx, report, nil)
}

// endArtifactCancelled ends an artifact run that was cancelled: status
// cancelled, outcome none, never infra_error. finalize, which still runs
// after, does nothing more (r.skipPR).
func (r *run) endArtifactCancelled(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusCancelled, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// endArtifactHalted ends an artifact run that a halt (a budget cap, a kill
// switch, the token cap) stopped. Its outcome is none — a halted artifact
// run has no artifacts, exactly as a halted review run posts no comment —
// which is why the halt advice in halt.go needs no arm of its own for this
// mode.
func (r *run) endArtifactHalted(ctx context.Context, h *runstore.Halt) {
	r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeNone, h
	r.rec.Reason = (&HaltError{*h}).Error()
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// failArtifactInfra ends an artifact run whose own operation (the checkout,
// the state directory, the render's process) failed outright: status
// infra_error, outcome none.
func (r *run) failArtifactInfra(ctx context.Context, err error) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, r.redact(err.Error())
	r.skipPR = true
	r.d.Log.Warn("the artifact run failed", "err", r.rec.Reason)
	r.storeArtifactEnded(ctx)
}

// endArtifactFailed ends an artifact run whose plan stage failed (the model
// timed out, crashed, or reported an error) or whose render ended non-zero:
// status failed, outcome none — no pull request to fall back to, and no
// artifacts to claim.
func (r *run) endArtifactFailed(ctx context.Context) {
	reason := r.failure()
	if reason == "" {
		reason = "the plan stage ended without a result"
	}
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, r.redact(reason)
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// artifactHasPlan reports whether rcp's steps lead with a plan stage: the
// optional agent stage of a mode: artifact recipe (a recipe's artifact
// shape is exactly [plan], render or [render]).
func artifactHasPlan(rcp *recipe.Recipe) bool {
	for _, s := range rcp.Steps {
		if s.Kind == recipe.StepPlan {
			return true
		}
	}
	return false
}

// sha256Hex is the render record's CommandSHA256: lowercase hex of the
// sha256 of the string the runner ran, which is the text a reviewer can
// hash from the repository at the recorded ref.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// inputsDir is the checkout directory a mode: artifact run downloads its
// attached files into, and the name excluded from git so the run can never
// commit one (design §4 step 5).
const inputsDir = runstore.InputsDir

// downloadInputs downloads every file the task spec attached into the
// checkout's inputs/, before the run's first step (design §4 step 4), and
// records them on the run. It is called only for a mode: artifact run whose
// spec carries inputs; bootstrap has already refused the other modes.
//
// The spec is text a launcher wrote, so nothing here trusts it. The caps are
// re-applied before the first GetFile — the count against
// workflows.<wf>.inputs.max_files, EACH entry's own bytes against
// inputs.max_bytes, and the SUM of the entries' bytes against
// inputs.max_total_bytes, the set cap and never one entry's size times the
// count — so an over-cap spec downloads nothing and leaves inputs/ empty.
// Each name is refused by task.InputNameProblem before it becomes a path, so
// a spec whose name is ../../.git/hooks/pre-commit fails the run rather than
// writing outside inputs/, and is never cleaned into something plausible.
//
// Each accepted name is then read from runs/<slug>/<run>/inputs/<name> with
// blobx.GetFile, passing the spec's own sha256 as the expected hash and
// strict false: the job's account cannot list that prefix, so a 403 is what
// a MISSING input looks like to it, and the run must say "the task names an
// input that is not in the bucket" rather than report an IAM problem. A
// mismatch — or an object bigger than the size the spec claims — leaves
// nothing at the destination: GetFile writes a temporary beside it and
// renames only after the hash matched. A file the bucket holds but the spec
// does not name is not downloaded at all; it cannot be named, which is the
// whole reason the list lives in the spec.
// The workflow's caps come in as an argument rather than from r.wf: this runs
// before bootstrap assigns r.wf (the assignment sits with the record's own
// fields, after the recipe checks), and reading the field here would silently
// judge every run by the zero config's defaults instead of by the workflow's
// own `inputs:` block — the caps would never bite.
func (r *run) downloadInputs(ctx context.Context, caps config.Inputs) error {
	if len(r.spec.Inputs) == 0 {
		return nil
	}
	if r.d.Bucket == nil {
		return errors.New("the task attached files, and this run has no runs bucket to download them from")
	}
	if err := checkInputCaps(r.spec.Inputs, caps); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, in := range r.spec.Inputs {
		if problem := task.InputNameProblem(in.Name); problem != "" {
			return fmt.Errorf("attached input %q %s", in.Name, problem)
		}
		if seen[in.Name] {
			return fmt.Errorf("attached input %q is named twice; one file cannot be two", in.Name)
		}
		seen[in.Name] = true
	}
	dir := filepath.Join(r.d.WorkDir, inputsDir)
	for _, in := range r.spec.Inputs {
		dest := filepath.Join(dir, filepath.FromSlash(in.Name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
		}
		key := runstore.InputKey(r.d.Store.Prefix(), in.Name)
		// The limit is the size the spec claims, not the workflow's
		// per-file cap: the cap was just checked, so this is the tighter of
		// the two, and an object bigger than the claim never reaches the
		// destination.
		size, _, err := r.d.Bucket.GetFile(ctx, key, dest, in.SHA256, in.Bytes, false)
		switch {
		case errors.Is(err, blobx.ErrNotExist):
			return fmt.Errorf("the task names an attached input %q that is not in this run's bucket (%s)", in.Name, key)
		case err != nil:
			return fmt.Errorf("downloading attached input %q: %w", in.Name, err)
		}
		if size != in.Bytes {
			return fmt.Errorf("attached input %q is %d bytes, and the task claims %d", in.Name, size, in.Bytes)
		}
	}
	if err := r.excludeInputsFromGit(ctx); err != nil {
		return err
	}
	// The record's inputs are the spec's list, echoed, and they are stored
	// once the files they name are actually in the checkout: a reader sees
	// exactly what the run was given, and never sees a list for a download
	// that failed.
	r.rec.Inputs = r.spec.Inputs
	r.save(ctx)
	return nil
}

// checkInputCaps applies the workflow's three inputs caps to the claims a
// task spec makes, before anything is downloaded. The three are independent
// and each means one thing (design §4 step 2): max_files counts ENTRIES,
// max_bytes bounds ONE ENTRY's own size, max_total_bytes bounds THE SET —
// never one entry's size, and never the count times the per-file cap. An
// entry's `bytes` is the launcher's own claim about what it uploaded, which
// is exactly why it is judged here and again against the object that comes
// down.
func checkInputCaps(inputs []task.InputFile, caps config.Inputs) error {
	if n := len(inputs); n > caps.MaxFileCount() {
		return fmt.Errorf("the task attached %d files, and workflows.inputs.max_files allows %d", n, caps.MaxFileCount())
	}
	var total int64
	for _, in := range inputs {
		if in.Bytes > caps.MaxFileBytes() {
			return fmt.Errorf("the task's attached input %q claims %s, and workflows.inputs.max_bytes allows %s per file", in.Name, fmtBytes(in.Bytes), fmtBytes(caps.MaxFileBytes()))
		}
		total += in.Bytes
	}
	if set := caps.MaxSetBytes(); total > set {
		return fmt.Errorf("the task's %d attached inputs claim %s in all, and workflows.inputs.max_total_bytes allows %s for the set", len(inputs), fmtBytes(total), fmtBytes(set))
	}
	return nil
}

// excludeInputsFromGit appends inputs/ to the checkout's own exclude file,
// <git dir>/info/exclude, once and idempotently (design §4 step 5): an
// artifact run must never commit a screenshot, and the agent's own `git add
// -A` is the way it otherwise would. It is the git directory's exclude and
// not .gitignore, because .gitignore is repository text the run would then
// be proposing as part of its work.
//
// It fails the run rather than warning: a run whose inputs/ were not
// excluded is a run whose plan stage can commit them.
func (r *run) excludeInputsFromGit(ctx context.Context) error {
	repo, err := gitops.Open(r.d.WorkDir, nil)
	if err != nil {
		return fmt.Errorf("excluding %s/ from git: %w", inputsDir, err)
	}
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return fmt.Errorf("finding the git directory: %w", err)
	}
	path := filepath.Join(gitDir, "info", "exclude")
	line := inputsDir + "/"
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	for _, have := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(have) == line {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	add := line + "\n"
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		add = "\n" + add
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(add); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
