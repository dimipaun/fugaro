// Command fakeclaude stands in for the claude CLI in tests. It replays the
// calls in script.json (next to the binary), one per invocation, and logs each
// invocation to calls.jsonl.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
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
	// ModelUsage is the result event's modelUsage, by model: an oauth run
	// reports its notional spend by model from it.
	ModelUsage map[string]modelUsage `json:"model_usage,omitempty"`
	// API are model calls made after the shell, through the gateway the
	// environment points at.
	API []apiCall `json:"api"`
}

type modelUsage struct {
	Input         int64 `json:"inputTokens"`
	Output        int64 `json:"outputTokens"`
	CacheRead     int64 `json:"cacheReadInputTokens"`
	CacheCreation int64 `json:"cacheCreationInputTokens"`
}

// apiCall is one model call (Parallel > 1: that many at once). It goes to
// $ANTHROPIC_BASE_URL/v1/messages with x-api-key $ANTHROPIC_API_KEY, or to
// $ANTHROPIC_VERTEX_BASE_URL's rawPredict path.
type apiCall struct {
	Model     string `json:"model"`
	MaxTokens int64  `json:"max_tokens"`
	BodyBytes int    `json:"body_bytes"` // padded prompt size
	CacheTTL  string `json:"cache_ttl"`  // "", "5m", "1h"
	Stream    bool   `json:"stream"`
	Parallel  int    `json:"parallel"`
}

type invocation struct {
	Args   []string `json:"args"`
	Prompt string   `json:"prompt"`
	Env    []string `json:"env"`
	Dir    string   `json:"dir"`
	// API is the status of each model call the invocation made, in order.
	API []int `json:"api,omitempty"`
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
	sid, resume := sessionID(os.Args[1:])
	session(sid, resume, wd, string(prompt))
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sid})
	if c.Shell != "" {
		cmd := exec.Command("sh", "-c", c.Shell)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "fakeclaude: shell: %v\n", err)
		}
	}
	if len(c.API) > 0 {
		statuses, refusal := makeCalls(c.API)
		recordStatuses(callsPath, statuses)
		if refusal != "" {
			// As Claude Code ends when the API refuses with x-should-retry:
			// false: an error result, exit 1.
			emit(map[string]any{
				"type": "result", "subtype": "error_during_execution", "is_error": true, "total_cost_usd": c.Cost,
				"session_id": sid, "result": refusal,
			})
			os.Exit(1)
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
		ev := map[string]any{
			"type": "result", "subtype": subtype, "is_error": c.IsError, "total_cost_usd": c.Cost,
			"session_id": sid, "result": os.ExpandEnv(c.Text), "structured_output": structured,
		}
		if len(c.ModelUsage) > 0 {
			ev["modelUsage"] = c.ModelUsage
		}
		emit(ev)
	}
	os.Exit(c.Exit)
}

// sessionID returns the session the call names, and whether it resumes it.
func sessionID(args []string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--session-id" || args[i] == "--resume" {
			return args[i+1], args[i] == "--resume"
		}
	}
	return "", false
}

// session keeps a session file as Claude Code does, under
// $HOME/.claude/projects/<escaped wd>/<sid>.jsonl, appending one line per
// invocation. A resume of a session with no file fails as Claude Code
// does: exit 1, the message on stderr, no result event. Without HOME the
// fake keeps no sessions, so harnesses that don't set it are unaffected.
func session(sid string, resume bool, wd, prompt string) {
	home := os.Getenv("HOME")
	if home == "" || sid == "" {
		return
	}
	if !agent.ValidSessionID(sid) {
		fail("session ID %q is not a lower-case UUID", sid)
	}
	dir := agent.SessionDir(home, wd)
	path := filepath.Join(dir, sid+".jsonl")
	if resume {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "No conversation found with session ID: %s\n", sid)
			os.Exit(1)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail("%v", err)
	}
	appendLine(path, map[string]string{"prompt": prompt})
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

// makeCalls sends each call (a parallel one, all its copies at once) and
// returns every status in order. The second result is the body of the first
// refusal the client may not retry (status >= 400 with x-should-retry:
// false), or "".
func makeCalls(calls []apiCall) ([]int, string) {
	var statuses []int
	refusal := ""
	for _, c := range calls {
		n := max(c.Parallel, 1)
		type result struct {
			status int
			body   string
			retry  bool
		}
		results := make([]result, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i].status, results[i].body, results[i].retry = send(c)
			}()
		}
		wg.Wait()
		for _, r := range results {
			statuses = append(statuses, r.status)
			if r.status >= 400 && !r.retry && refusal == "" {
				refusal = r.body
			}
		}
	}
	return statuses, refusal
}

// send makes one call and reads the whole response.
func send(c apiCall) (status int, body string, retry bool) {
	url, key, payload := request(c)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return 0, err.Error(), true
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if key != "" {
		req.Header.Set("x-api-key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error(), true
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("x-should-retry") != "false"
}

// request is where a call goes, with which key, and its body.
func request(c apiCall) (url, key, payload string) {
	pad := strings.Repeat("x", max(c.BodyBytes-200, 0))
	block := map[string]any{"type": "text", "text": "hello " + pad}
	switch c.CacheTTL {
	case "":
	case "5m", "1h":
		block["cache_control"] = map[string]any{"type": "ephemeral", "ttl": c.CacheTTL}
	default:
		block["cache_control"] = map[string]any{"type": "ephemeral"}
	}
	body := map[string]any{
		"max_tokens": c.MaxTokens,
		"messages":   []any{map[string]any{"role": "user", "content": []any{block}}},
	}
	if c.Stream {
		body["stream"] = true
	}
	if vb := os.Getenv("ANTHROPIC_VERTEX_BASE_URL"); vb != "" && os.Getenv("ANTHROPIC_BASE_URL") == "" {
		body["anthropic_version"] = "vertex-2023-10-16"
		method := "rawPredict"
		if c.Stream {
			method = "streamRawPredict"
		}
		url = strings.TrimRight(vb, "/") + "/projects/" + os.Getenv("ANTHROPIC_VERTEX_PROJECT_ID") + "/locations/" + os.Getenv("CLOUD_ML_REGION") +
			"/publishers/anthropic/models/" + c.Model + ":" + method
	} else {
		body["model"] = c.Model
		url = strings.TrimRight(os.Getenv("ANTHROPIC_BASE_URL"), "/") + "/v1/messages"
		key = os.Getenv("ANTHROPIC_API_KEY")
	}
	b, _ := json.Marshal(body)
	return url, key, string(b)
}

// recordStatuses adds the calls' statuses to the invocation's own line in
// calls.jsonl, which is the last.
func recordStatuses(path string, statuses []int) {
	data, err := os.ReadFile(path)
	if err != nil {
		fail("%v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var inv invocation
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &inv); err != nil {
		fail("%v", err)
	}
	inv.API = statuses
	b, _ := json.Marshal(inv)
	lines[len(lines)-1] = string(b)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		fail("%v", err)
	}
}
