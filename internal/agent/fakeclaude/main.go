// Command fakeclaude stands in for the claude CLI in tests. It replays the
// calls in script.json (next to the binary), one per invocation, and logs each
// invocation to calls.jsonl.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type call struct {
	Shell      string          `json:"shell"`
	Text       string          `json:"text"`
	Structured json.RawMessage `json:"structured,omitempty"`
	Cost       float64         `json:"cost"`
	IsError    bool            `json:"is_error"`
	Subtype    string          `json:"subtype"`
	Exit       int             `json:"exit"`
	SleepS     float64         `json:"sleep_s"`
	NoResult   bool            `json:"no_result"`
}

type invocation struct {
	Args   []string `json:"args"`
	Prompt string   `json:"prompt"`
	Env    []string `json:"env"`
	Dir    string   `json:"dir"`
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		fail("%v", err)
	}
	dir := filepath.Dir(exe)
	var script struct {
		Calls []call `json:"calls"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "script.json"))
	if err != nil {
		fail("reading script: %v", err)
	}
	if err := json.Unmarshal(data, &script); err != nil {
		fail("parsing script: %v", err)
	}
	prompt, _ := io.ReadAll(os.Stdin)
	wd, _ := os.Getwd()
	callsPath := filepath.Join(dir, "calls.jsonl")
	n := countLines(callsPath)
	appendLine(callsPath, invocation{Args: os.Args[1:], Prompt: string(prompt), Env: os.Environ(), Dir: wd})
	if n >= len(script.Calls) {
		fail("unexpected call #%d (script has %d)", n+1, len(script.Calls))
	}
	c := script.Calls[n]
	sid := sessionID(os.Args[1:])
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sid})
	if c.Shell != "" {
		cmd := exec.Command("sh", "-c", c.Shell)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "fakeclaude: shell: %v\n", err)
		}
	}
	if c.SleepS > 0 {
		time.Sleep(time.Duration(c.SleepS * float64(time.Second)))
	}
	if !c.NoResult {
		subtype := c.Subtype
		if subtype == "" {
			subtype = "success"
		}
		var structured any
		if len(c.Structured) > 0 {
			structured = c.Structured
		}
		emit(map[string]any{
			"type": "result", "subtype": subtype, "is_error": c.IsError, "total_cost_usd": c.Cost,
			"session_id": sid, "result": os.ExpandEnv(c.Text), "structured_output": structured,
		})
	}
	os.Exit(c.Exit)
}

func sessionID(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--session-id" || args[i] == "--resume" {
			return args[i+1]
		}
	}
	return ""
}

func emit(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		fail("%v", err)
	}
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<24)
	for s.Scan() {
		n++
	}
	return n
}

func appendLine(path string, v any) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		fail("%v", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(v); err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fakeclaude: "+format+"\n", args...)
	os.Exit(3)
}
