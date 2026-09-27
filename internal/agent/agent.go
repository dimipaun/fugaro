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
	Dir                string
	Env                []string
	Transcript         io.Writer // receives the raw stream-json lines
	Stderr             io.Writer
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
	return args
}

// Run invokes claude and returns its result event.
func (c Claude) Run(ctx context.Context, req Request) (Result, error) {
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
	code, runErr := procgroup.Run(ctx, procgroup.Cmd{
		Name: bin, Args: Args(req), Dir: req.Dir, Env: req.Env,
		Stdin: strings.NewReader(req.Prompt), Stdout: pw, Stderr: req.Stderr, Grace: c.Grace,
	})
	pw.Close()
	p := <-done
	p.res.ExitCode = code
	switch {
	case runErr != nil:
		return p.res, runErr
	case p.err != nil:
		return p.res, fmt.Errorf("reading claude output: %w", p.err)
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
