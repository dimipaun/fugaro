// Package decide is the orchestrator's decision agent: the one place the loop
// asks a model to judge, and every bound around it (O12).
//
// A decision is a fresh `claude -p` with no memory of anything (D1228). It is
// given a brief — one work item, the facts the loop has learned, and the
// excerpt of what went wrong — and answers with a reasoning and a list of
// typed ops. The ops are not a way to write state: they are an
// orchestrator.Patch, applied by code through orchestrator.Apply under the
// DecisionAgent class, so an op a person alone may make is refused however the
// answer is worded (G1, F20).
//
// Everything that bounds one call lives here rather than in the caller: the
// model comes from O1's class and O4's key and never from the answer, the
// counts come from the state (six an hour, forty a day), the call is capped at
// a few turns and a pinned output size, the child runs with the deny rules a
// coding agent's session has, and a call that answered nothing is a cycle that
// achieved nothing (G10). The reasoning is written to the audit log, so what
// the model said is readable after the fact without asking it again.
package decide

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/safetext"
)

//go:embed prompts/system.md
var systemPrompt string

// ActorName is the name a decision agent's patch is applied under. It is not a
// person: the class is what decides which ops may land, and the name is what
// the record says about who asked.
const ActorName = "decision-agent"

// The refusals a runner meets before anything is spent.
var (
	// ErrNoAgent is a Runner with no way to run a call.
	ErrNoAgent = errors.New("decide: the runner has no agent")
	// ErrNoModel is a class of decision with no model: O4's key carries none
	// for it. The runner invents no model of its own, because a model chosen
	// here would be a second place the class-to-model rule lives.
	ErrNoModel = errors.New("decide: no model is configured for this class of decision")
	// ErrFailedCall is a call that did not answer: the child exited non-zero,
	// or reported an error result.
	ErrFailedCall = errors.New("decide: the decision call failed")
)

// Request is one decision to make.
type Request struct {
	State  orchestrator.State
	ItemID string
	// Paths are the changed paths the decision is about. O1's classifier
	// reads them and nothing else (D1234): the class that picks the model is
	// decided by code over paths, never by the model and never by how the
	// change or the brief describes itself.
	Paths []string
	// Failure is the excerpt of what went wrong — the CI output a run
	// reported, the review finding that did not resolve — and may be empty
	// for a decision that is not about a failure.
	Failure string
}

// Decision is what one call produced: which class and model it ran as, the
// reasoning the model gave, the patch it asked for and the state that patch
// makes.
//
// Next is the state to commit, not a state to write: the runner applied the
// patch to the state it was given, and a caller commits Next under the same
// compare-and-swap every other writer uses (G9). A refused decision carries no
// Next at all.
type Decision struct {
	Class     sensitivity.Class
	Model     string
	ItemID    string
	Reasoning string
	Patch     orchestrator.Patch
	Next      orchestrator.State
	// Outcome is how this cycle ended, in O6's vocabulary: progress for a
	// decision that landed, empty or failed for one that did not. It is the
	// tick's own summary of itself, which is what the breaker counts (G10).
	Outcome orchestrator.CycleOutcome
	// At is the server time the call was made at, and the stamp the patch's
	// actor carries: every stamp in the document comes from the database's
	// clock and never from this machine's (D1230).
	At time.Time
	// AuditErr is the audit log's own failure, reported without failing the
	// decision: the log is evidence, and what must not be lost is the
	// decision (orchestrator/audit.go's own rule).
	AuditErr error
}

// Reply is what one decision call replied: the structured output the model
// answered with, and how the child ended. It is not an Answer: the answer is
// what this package can read out of the structured output, and a reply may
// carry none.
type Reply struct {
	Structured json.RawMessage
	IsError    bool
	Subtype    string
}

// Agent runs one decision call. It is an interface so that the runner's
// bounds, its model choice and its class fence are testable without a process,
// and Claude is the implementation that runs the person's own `claude`.
type Agent interface {
	Run(ctx context.Context, call Call) (Reply, error)
}

// Call is one decision call: the argv, the environment, the working directory
// and the prompt. The prompt is passed on the child's stdin, which is how
// `claude -p` takes it.
type Call struct {
	Args   []string
	Env    []string
	Dir    string
	Prompt string
}

// Auditor is the audit log a decision is written to, which
// *orchestrator.Audit implements. It is an interface because the log is a
// database node and a decision's own behaviour must be testable without one.
type Auditor interface {
	Append(ctx context.Context, e orchestrator.Entry) (orchestrator.Entry, error)
}

// Runner makes one decision at a time. Every field is the caller's: the agent
// to run, the bounds, the merged policy's models, the audit log and the server
// clock. A Runner with no Now cannot decide anything — a decision carries the
// stamp its patch is applied at, and a stamp from this machine's clock is not
// one (D1230).
type Runner struct {
	Agent  Agent
	Bounds Bounds
	Models map[policy.DecisionClass]string
	Audit  Auditor
	Now    orchestrator.ServerNow
	// Dir is the child's working directory, and Env its environment; a nil
	// Env inherits this process's, as procgroup does.
	Dir string
	Env []string
}

// Decide makes one decision. The order of the steps is the order of the
// bounds: the model is settled first, then the caps — which cost nothing and
// must refuse before a child is started — then the brief, and only then the
// call.
func (r *Runner) Decide(ctx context.Context, req Request) (Decision, error) {
	if r.Agent == nil {
		return Decision{}, ErrNoAgent
	}
	if r.Now == nil {
		return Decision{}, orchestrator.ErrNoServerTime
	}
	now, ok := r.Now()
	if !ok || now.IsZero() {
		return Decision{}, orchestrator.ErrNoServerTime
	}
	class := sensitivity.Classify(req.Paths)
	model, err := Model(class, r.Models)
	if err != nil {
		return Decision{}, err
	}
	bounds := r.Bounds.WithDefaults()
	d := Decision{Class: class, Model: model, ItemID: req.ItemID, At: now}

	if err := bounds.Allow(req.State, now); err != nil {
		d.Outcome = orchestrator.CycleEmpty
		return d, err
	}
	brief, err := NewBrief(req.State, req.ItemID, req.Failure)
	if err != nil {
		d.Outcome = orchestrator.CycleEmpty
		return d, err
	}
	call, err := r.call(bounds, model, brief)
	if err != nil {
		d.Outcome = orchestrator.CycleEmpty
		return d, err
	}

	reply, err := r.Agent.Run(ctx, call)
	if err != nil {
		// A call the daemon's own shutdown cut short is not a cycle that
		// achieved nothing: it leaves the outcome unset, which the breaker
		// reads as neither a failure nor progress (orchestrator.Breaker).
		if ctx.Err() != nil {
			return d, err
		}
		d.Outcome = orchestrator.CycleFailed
		return d, err
	}
	if reply.IsError {
		d.Outcome = orchestrator.CycleFailed
		return d, fmt.Errorf("%w: %s", ErrFailedCall, safetext.OneLine(reply.Subtype))
	}

	parsed, err := ParseAnswer(reply.Structured)
	if err != nil {
		// An answer that asks for nothing is a cycle that achieved nothing,
		// which is what the breaker counts; one this release cannot read at
		// all is a call that failed. Both are the same to a person watching
		// (G10), and they are not the same in the record.
		d.Outcome = orchestrator.CycleFailed
		if errors.Is(err, ErrNoDecision) {
			d.Outcome = orchestrator.CycleEmpty
		}
		return d, err
	}
	d.Reasoning = parsed.Reasoning
	d.Patch = parsed.Patch(req.State.Rev)
	next, err := orchestrator.Apply(req.State, d.Patch, orchestrator.Actor{
		Class: orchestrator.DecisionAgent, Name: ActorName, Now: now,
	})
	if err != nil {
		// An op the agent's class may not make is a refusal of the whole
		// answer, and the answer is recorded with the reason it was refused:
		// a model that keeps reaching for a person's op is exactly what a
		// person reading the log needs to see.
		d.Outcome = orchestrator.CycleFailed
		r.audit(ctx, &d, safetext.OneLine(err.Error()))
		return d, err
	}
	d.Next = next
	d.Outcome = orchestrator.CycleProgress
	r.audit(ctx, &d, d.Reasoning)
	return d, nil
}

// audit appends this decision to the audit log. A decision the log could not
// take is still a decision: the failure is reported on the Decision rather
// than raised, because the caller's next move — commit the patch — is the same
// either way.
func (r *Runner) audit(ctx context.Context, d *Decision, text string) {
	if r.Audit == nil {
		return
	}
	// The transition carries the stamp, because the log's id is the subject
	// and the transition joined: one fixed transition would let an item have
	// one decision in the life of the node and never a second. The stamp is
	// spelled in the log's own alphabet — a transition is a name, lowercase
	// (auditTransitionRE) — so it is not RFC 3339's own punctuation, which a
	// name may not carry.
	_, d.AuditErr = r.Audit.Append(ctx, orchestrator.Entry{
		Subject:    d.ItemID,
		Transition: "decision-" + d.At.UTC().Format("20060102-150405"),
		Text:       text,
	})
}

// Model is the model one decision runs on: O4's key, looked up by O1's class.
// A class the map does not carry is refused rather than defaulted — the map is
// the merged policy's, which WithDefaults fills to each class's floor, and a
// floor spelled a second time here is a floor that can drift from O4's.
func Model(class sensitivity.Class, models map[policy.DecisionClass]string) (string, error) {
	key := policy.DecisionClass(class)
	if !slices.Contains(policy.DecisionClasses, key) {
		return "", fmt.Errorf("%w: %q is not a decision class", ErrNoModel, safetext.OneLine(string(class)))
	}
	name := models[key]
	if name == "" {
		return "", fmt.Errorf("%w: %s", ErrNoModel, policy.DecisionModelKey(key))
	}
	return name, nil
}

// call is the argv, the environment and the prompt of one decision call.
func (r *Runner) call(bounds Bounds, model string, brief Brief) (Call, error) {
	settings, err := settingsJSON(agent.DenyRules(true, false))
	if err != nil {
		return Call{}, err
	}
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		// The person's own machine is where this runs, so the default setting
		// sources would load their user level — their hooks, their plugins,
		// their MCP servers, their permission rules — into a process
		// answering a question nobody asked them, and the repository's own
		// level into one that may be sitting in a checkout it does not own.
		// --strict-mcp-config with no --mcp-config beside it says the same
		// for MCP servers.
		"--setting-sources", "",
		"--strict-mcp-config",
		// A decision is one question asked once: nothing about it belongs in
		// a session file under the person's home, and nothing resumes it.
		"--no-session-persistence",
		// The brief is the whole of what a decision may know, and the child
		// holds the person's own Claude login: a tool call is a process
		// `claude` starts with that credential in its environment. An empty
		// list removes the built-in set (check90.sh removes it for the same
		// reason), and the deny rules below stay beside it (G8).
		"--tools", "",
		"--settings", settings,
		"--append-system-prompt", systemPrompt,
		"--model", model,
		"--max-turns", strconv.Itoa(bounds.MaxTurns),
		"--json-schema", OutputSchema,
		// No --dangerously-skip-permissions: the container's justification for
		// it (the job is the boundary, the checkout is a throwaway) does not
		// hold on the person's own machine, where the child shares their home,
		// their login and their files (voice.md D54 excludes the flag by name
		// there). A decision is a judgement over a brief, so a tool call that
		// would need the flag is a call that has gone wrong.
	}
	return Call{Args: args, Env: r.env(bounds), Dir: r.Dir, Prompt: brief.Render()}, nil
}

// env is the child's environment: the caller's, plus the output pin. The pin
// is the agent package's own key (agent.PinVars) rather than a name spelled
// again here, so the daemon and the runs set the same variable.
//
// A pinned name the environment already carries is replaced rather than
// appended to: the child reads the first entry of a name and the parent's
// value is the one that would win, which is the opposite of a pin.
func (r *Runner) env(bounds Bounds) []string {
	env := slices.Clone(r.Env)
	if env == nil {
		env = os.Environ()
	}
	pins := agent.PinVars("", "", bounds.MaxOutputTokens)
	names := make([]string, 0, len(pins))
	for name := range pins {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		env = slices.DeleteFunc(env, func(e string) bool {
			return strings.HasPrefix(e, name+"=")
		})
		env = append(env, name+"="+pins[name])
	}
	return env
}

// settingsJSON is the inline --settings payload one decision call is launched
// with: the deny rules, and the hooks off.
//
// It is a payload and not the managed settings file the runs write
// (agent.WriteManagedSettings): that file is the container's, at a path this
// process may not own on a person's machine, and --settings is the seam the
// tree already uses for a local child (internal/diagnoseagent). The rules are
// the same ones a coding agent's session carries (G8), and the web tools are
// denied for the same reason the runs deny them: their use is billed as input
// nobody bounded, and a decision reads its brief and nothing else — a page
// fetched into a decision is text nobody in this loop vetted.
//
// The hooks are disabled because the child may be launched from a checkout
// the person did not vet for this: a hook is a shell command Claude Code
// itself starts, with the daemon's environment, which no rule on the agent's
// own tools reaches.
func settingsJSON(deny []string) (string, error) {
	payload := map[string]any{
		"disableAllHooks": true,
		"permissions":     map[string]any{"deny": deny},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("decide: building the settings payload: %w", err)
	}
	return string(b), nil
}

// Claude runs the person's own `claude` as one child process, reading the
// stream-json it prints with the agent package's parser so a decision and a
// run read the same event stream the same way.
type Claude struct {
	// Bin is the binary; empty means the `claude` on PATH.
	Bin string
}

// Run implements Agent.
func (c Claude) Run(ctx context.Context, call Call) (Reply, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	pr, pw := io.Pipe()
	type parsed struct {
		result agent.Result
		found  bool
		err    error
	}
	done := make(chan parsed, 1)
	go func() {
		result, found, err := agent.ParseStream(pr, nil)
		_, _ = io.Copy(io.Discard, pr)
		done <- parsed{result, found, err}
	}()
	code, runErr := procgroup.Run(ctx, procgroup.Cmd{
		Name:   bin,
		Args:   call.Args,
		Dir:    call.Dir,
		Env:    call.Env,
		Stdin:  strings.NewReader(call.Prompt),
		Stdout: pw,
		Stderr: io.Discard,
	})
	_ = pw.Close()
	p := <-done
	if runErr != nil {
		return Reply{}, runErr
	}
	if p.err != nil {
		return Reply{}, fmt.Errorf("decide: reading the call's stream: %w", p.err)
	}
	if code != 0 {
		return Reply{}, fmt.Errorf("%w: claude exited %d", ErrFailedCall, code)
	}
	if !p.found {
		return Reply{}, fmt.Errorf("%w: claude printed no result event", ErrNoAnswer)
	}
	return Reply{IsError: p.result.IsError, Subtype: p.result.Subtype, Structured: p.result.Structured}, nil
}
