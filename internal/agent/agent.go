// Package agent runs Claude Code headless (`claude -p`) for one stage.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/procgroup"
)

// Request is one agent invocation.
type Request struct {
	Prompt             string
	SessionID          string // new session ID, or the one to resume
	Resume             bool
	AppendSystemPrompt string
	Model              string
	MaxBudgetUSD       float64
	JSONSchema         string // optional structured-output schema
	// SettingSources, when non-empty, is passed as --setting-sources: the
	// settings levels Claude Code loads for this session. A review-mode
	// stage passes ReviewSettingSources, the user level alone, so the
	// reviewed checkout's own project and local settings never load. It is
	// the flag and not a settings key: setting-source selection has no
	// managed-settings form at all.
	SettingSources []string
	// StrictMCPConfig passes --strict-mcp-config: only servers named by
	// --mcp-config are used and every other MCP configuration is ignored. A
	// review-mode stage sets it and passes no --mcp-config at all, so the
	// reviewed checkout's .mcp.json (and any MCP server a settings file
	// declares) loads nothing.
	StrictMCPConfig bool
	Dir             string
	Env             []string
	Transcript      io.Writer // receives the raw stream-json lines
	Stderr          io.Writer
}

// Result is what the agent's final result event reported.
type Result struct {
	SessionID  string
	Text       string
	Structured json.RawMessage
	CostUSD    float64
	IsError    bool
	Subtype    string
	ExitCode   int
	// SubAgents is the stage's sub-agent tool calls, which the runner's
	// relay counts from the stage's own stream (task-governance T1.3). The
	// relay is the runner's, not the agent's, so the runner folds the count
	// in after Run; a caller that kept no relay leaves it 0.
	SubAgents int
	// Repeats is the most times one identical shell command ran in the
	// stage, which the runner's relay counts from the stage's own stream the
	// same way (task-governance T1.4): the digest reads it as the loop
	// tripwire. It is a count alone -- the command can carry a token or a
	// path -- and it is 0 for a stage that ran no shell command. A caller
	// that kept no relay leaves it 0.
	Repeats int
	// Usage is the result event's "usage" and ModelUsage its "modelUsage"
	// by model, which also covers subagent and background models.
	Usage      Usage
	ModelUsage map[string]Usage
}

// Usage counts tokens by kind.
type Usage struct{ Input, CacheCreation, CacheRead, Output int64 }

// Total is every kind of token added up.
func (u Usage) Total() int64 { return u.Input + u.CacheCreation + u.CacheRead + u.Output }

// StageTokens is the stage's token count from its result event: the larger
// of Usage and the sum over ModelUsage, since a subagent's or a background
// model's tokens may appear only in the latter.
func (r Result) StageTokens() int64 {
	n := r.Usage.Total()
	var sum int64
	for _, u := range r.ModelUsage {
		sum += u.Total()
	}
	return max(n, sum)
}

// Agent runs one stage of agent work.
type Agent interface {
	Run(ctx context.Context, req Request) (Result, error)
}

// Claude runs the claude CLI.
type Claude struct {
	Bin   string        // default "claude"
	Grace time.Duration // SIGTERM → SIGKILL grace; zero means procgroup's default
}

// Args returns the claude command-line arguments for req. The prompt goes on stdin.
func Args(req Request) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if req.Resume {
		args = append(args, "--resume", req.SessionID)
	} else {
		args = append(args, "--session-id", req.SessionID)
	}
	if req.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.AppendSystemPrompt)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(req.MaxBudgetUSD, 'f', 2, 64))
	}
	if req.JSONSchema != "" {
		args = append(args, "--json-schema", req.JSONSchema)
	}
	if len(req.SettingSources) > 0 {
		args = append(args, "--setting-sources", strings.Join(req.SettingSources, ","))
	}
	if req.StrictMCPConfig {
		args = append(args, "--strict-mcp-config")
	}
	return args
}

// Run invokes claude and returns its result event.
//
// req.Env must not be nil: procgroup treats a nil Env as "inherit the whole
// runner environment", which would hand the permission-bypassed agent every
// credential the runner holds. Callers build a scrubbed environment with
// BuildEnv (even an empty one is a non-nil, zero-length slice).
func (c Claude) Run(ctx context.Context, req Request) (Result, error) {
	if req.Env == nil {
		return Result{}, fmt.Errorf("agent: refusing to run claude with a nil Env, which would inherit the runner's full environment")
	}
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	pr, pw := io.Pipe()
	type parsed struct {
		res   Result
		found bool
		err   error
	}
	done := make(chan parsed, 1)
	go func() {
		res, found, err := ParseStream(pr, req.Transcript)
		_, _ = io.Copy(io.Discard, pr) // keep draining if parsing stopped early
		done <- parsed{res, found, err}
	}()

	// Stderr is routed through our own pipe, the same way stdout is, so
	// that closing our end after procgroup.Run returns cuts off any
	// straggling write from an abandoned internal copy goroutine (the
	// leaked-daemon case): such a write fails immediately against a closed
	// io.Pipe rather than ever reaching req.Stderr. That means no write to
	// req.Stderr can happen after Run returns, so a caller can safely
	// Flush a Redactor wrapped around it once Run is done.
	errR, errW := io.Pipe()
	stderrDone := make(chan struct{})
	var noSession stderrMatch // read only after stderrDone
	go func() {
		defer close(stderrDone)
		w := io.Writer(io.Discard)
		if req.Stderr != nil {
			w = req.Stderr
		}
		if req.Resume {
			w = io.MultiWriter(&noSession, w)
		}
		_, _ = io.Copy(w, errR)
	}()

	code, runErr := procgroup.Run(ctx, procgroup.Cmd{
		Name: bin, Args: Args(req), Dir: req.Dir, Env: req.Env,
		Stdin: strings.NewReader(req.Prompt), Stdout: pw, Stderr: errW, Grace: c.Grace,
	})
	pw.Close()
	errW.Close()
	p := <-done
	<-stderrDone
	p.res.ExitCode = code
	switch {
	case runErr != nil:
		return p.res, runErr
	case p.err != nil:
		// ParseStream already wraps this with context.
		return p.res, p.err
	case !p.found && noSession.found:
		return p.res, fmt.Errorf("%w: claude exited with code %d, saying it has no session %s", ErrNoSession, code, req.SessionID)
	case !p.found:
		return p.res, fmt.Errorf("claude exited with code %d without a result event", code)
	}
	return p.res, nil
}

// NewSessionID returns a random UUIDv4 for --session-id.
func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
