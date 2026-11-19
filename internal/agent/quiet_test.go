package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// testSession is the session id the crafted stream-json lines name.
const testSession = "3f2a9c1e-0000-4000-8000-000000000001"

// jsonLine is one stream-json line.
func jsonLine(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// parentField is an event's parent_tool_use_id: null on the main thread's
// own events, as the real stream spells it.
func parentField(parent string) any {
	if parent == "" {
		return nil
	}
	return parent
}

// assistantLine is one assistant event carrying blocks, as the real stream
// may split one message into one event per content block (U3).
func assistantLine(parent string, blocks ...map[string]any) []byte {
	return jsonLine(map[string]any{
		"type": "assistant", "session_id": testSession, "parent_tool_use_id": parentField(parent),
		"message": map[string]any{"role": "assistant", "content": blocks},
	})
}

// userLine is one user event: a tool_result, or a sub-agent's own event.
func userLine(parent string, blocks ...map[string]any) []byte {
	return jsonLine(map[string]any{
		"type": "user", "session_id": testSession, "parent_tool_use_id": parentField(parent),
		"message": map[string]any{"role": "user", "content": blocks},
	})
}

func toolUse(id, name string) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name}
}

func toolResult(id string) map[string]any {
	return map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok"}
}

// TestQuietPointAfterToolResult: the tool_result that answers the main
// thread's only tool_use is the stage's quiet point -- and nothing before it
// is, so a stage that has issued no tool call yet is never stopped.
func TestQuietPointAfterToolResult(t *testing.T) {
	var q QuietPoint
	if q.Observe(jsonLine(map[string]any{"type": "system", "subtype": "init", "session_id": testSession})) {
		t.Fatal("a stage that has issued no tool call is not at a quiet point")
	}
	if q.Observe(assistantLine("", toolUse("t1", "Bash"))) {
		t.Fatal("a tool_use leaves a tool in flight")
	}
	if q.AtQuiet() {
		t.Fatal("AtQuiet disagrees with Observe while a tool runs")
	}
	if !q.Observe(userLine("", toolResult("t1"))) {
		t.Fatal("the tool_result answering the last tool_use is a quiet point")
	}
	if !q.AtQuiet() {
		t.Fatal("AtQuiet disagrees with Observe at a quiet point")
	}
}

// TestNoQuietPointMidTool: a running tool holds the stage off a quiet point,
// and a tool_result for an id no tool_use issued answers nothing.
func TestNoQuietPointMidTool(t *testing.T) {
	var q QuietPoint
	q.Observe(assistantLine("", toolUse("t1", "Bash")))
	if q.Observe(userLine("", toolResult("other"))) {
		t.Fatal("a tool_result for an id no tool_use issued reached a quiet point")
	}
	if q.Observe(assistantLine("", map[string]any{"type": "text", "text": "still working"})) {
		t.Fatal("an assistant event while a tool runs is not a quiet point")
	}
	if !q.Observe(userLine("", toolResult("t1"))) {
		t.Fatal("the tool_result that answers t1 is the quiet point")
	}
}

// TestParallelToolsCountedByID: two tool_use events are two tools in flight,
// and the quiet point is the last of them answered, whatever the order.
func TestParallelToolsCountedByID(t *testing.T) {
	var q QuietPoint
	q.Observe(assistantLine("", toolUse("t1", "Bash")))
	q.Observe(assistantLine("", toolUse("t2", "Read")))
	if q.Observe(userLine("", toolResult("t2"))) {
		t.Fatal("one of two parallel tools answered is not a quiet point")
	}
	if !q.Observe(userLine("", toolResult("t1"))) {
		t.Fatal("the last of two parallel tools answered is the quiet point")
	}
}

// TestSubAgentEventsIgnored: a sub-agent's events carry the Task call's id
// as their parent, and they are not the main thread's. Its tool_use neither
// counts as the main thread's nor holds a quiet point off, and its
// tool_result answers nothing of the main thread's.
func TestSubAgentEventsIgnored(t *testing.T) {
	var q QuietPoint
	if q.Observe(assistantLine("t1", toolUse("s1", "Bash"))) {
		t.Fatal("a sub-agent's tool_use was counted as the main thread's")
	}
	q.Observe(assistantLine("", toolUse("t1", "Task")))
	if q.Observe(assistantLine("t1", toolUse("s2", "Read"))) {
		t.Fatal("a sub-agent's tool_use reached a quiet point while Task ran")
	}
	if q.Observe(userLine("t1", toolResult("s2"))) {
		t.Fatal("a sub-agent's tool_result answered the main thread's Task call")
	}
	if !q.Observe(userLine("", toolResult("t1"))) {
		t.Fatal("the Task call's own tool_result is the quiet point")
	}
}

// TestQuietAfterEveryToolAnswered: every tool answered brings the stage back
// to a quiet point, so a long stage has one per tool call and a later --now
// is not left waiting for the first.
func TestQuietAfterEveryToolAnswered(t *testing.T) {
	var q QuietPoint
	for _, id := range []string{"t1", "t2", "t3"} {
		if q.Observe(assistantLine("", toolUse(id, "Bash"))) {
			t.Fatalf("%s: a tool_use is not a quiet point", id)
		}
		if !q.Observe(userLine("", toolResult(id))) {
			t.Fatalf("%s: its tool_result is a quiet point", id)
		}
	}
}

// TestPerBlockEventsHandled: the real CLI may split one assistant message
// into one event per content block (U3). A text block of its own is no quiet
// point, and each of the message's tool_use blocks is a tool in flight.
func TestPerBlockEventsHandled(t *testing.T) {
	var q QuietPoint
	if q.Observe(assistantLine("", map[string]any{"type": "text", "text": "let me look"})) {
		t.Fatal("a text block before any tool_use is not a quiet point")
	}
	if q.Observe(assistantLine("", toolUse("t1", "Read"))) {
		t.Fatal("the message's tool_use block left a tool in flight")
	}
	if q.Observe(assistantLine("", toolUse("t2", "Grep"))) {
		t.Fatal("a second block of one message is a second tool in flight")
	}
	if q.Observe(userLine("", toolResult("t1"))) {
		t.Fatal("one of the message's two tools answered is not a quiet point")
	}
	if !q.Observe(userLine("", toolResult("t2"))) {
		t.Fatal("both of the message's tools answered is a quiet point")
	}
}

// TestQuietPointAcrossWrites: the stage's output arrives in whatever chunks
// the pipe hands over, so a line split across writes is one line to the
// quiet point, and the bytes reach the caller's writer whole.
func TestQuietPointAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	var q QuietPoint
	ch := make(chan struct{}, 1)
	w := &quietWriter{w: &out, q: &q, ch: ch}
	stream := append(assistantLine("", toolUse("t1", "Bash")), '\n')
	stream = append(stream, userLine("", toolResult("t1"))...)
	stream = append(stream, '\n')
	for len(stream) > 0 {
		n := min(7, len(stream))
		if _, err := w.Write(stream[:n]); err != nil {
			t.Fatal(err)
		}
		stream = stream[n:]
	}
	select {
	case <-ch:
	default:
		t.Fatal("the tool_result that answered t1 signalled no quiet point")
	}
	if !q.AtQuiet() {
		t.Fatal("the split lines were not fed to the quiet point")
	}
	if !strings.Contains(out.String(), `"tool_use"`) || !strings.Contains(out.String(), `"tool_result"`) {
		t.Fatalf("the writer did not pass the stream through: %q", out.String())
	}
}

// TestYieldWaitsForQuietPoint: a yield request stops the stage at the
// tool_result that answers the running tool and not before it, so the
// transcript carries the tool_result and the run is back long before its
// bound.
func TestYieldWaitsForQuietPoint(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"tools":[{"id":"t1","name":"Bash","sleep_s":2}],"sleep_s":30,"text":"never"}]}`)
	var transcript bytes.Buffer
	start := time.Now()
	res, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(context.Background(), Request{
		Prompt: "work", SessionID: NewSessionID(), Dir: t.TempDir(),
		Env: []string{"PATH=" + os.Getenv("PATH")}, Transcript: &transcript,
		Yield: &YieldRequest{Bound: 20 * time.Second},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil: a yield is not a failure", err)
	}
	if !res.Yielded || res.YieldReason != YieldAtQuietPoint {
		t.Fatalf("result = %+v, want a yield at the quiet point", res)
	}
	if !strings.Contains(transcript.String(), `"tool_result"`) {
		t.Fatal("the stage was stopped before the running tool was answered")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("stopped after %s, which is the bound and not the quiet point", elapsed)
	}
}

// TestYieldParallelToolsAndSubAgent: the fake's parallel tools and its
// sub-agent, end to end. Two main-thread tools are in flight together, the
// sub-agent's events carry the Task call's id as their parent, and the
// quiet point is the second main-thread tool answered -- the sub-agent's
// events, arriving in between, neither reach one nor hold it off.
func TestYieldParallelToolsAndSubAgent(t *testing.T) {
	script := `{"calls":[{"tools":[` +
		`{"id":"t1","name":"Task","sleep_s":2},` +
		`{"id":"t2","name":"Bash","sleep_s":0},` +
		`{"id":"s1","name":"Read","parent":"t1","sleep_s":0}],` +
		`"sleep_s":30}]}`
	bin := testutil.FakeClaude(t, script)
	var transcript bytes.Buffer
	start := time.Now()
	res, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(context.Background(), Request{
		Prompt: "work", SessionID: NewSessionID(), Dir: t.TempDir(),
		Env: []string{"PATH=" + os.Getenv("PATH")}, Transcript: &transcript,
		Yield: &YieldRequest{Bound: 20 * time.Second},
	})
	if err != nil || !res.Yielded || res.YieldReason != YieldAtQuietPoint {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	got := transcript.String()
	if !strings.Contains(got, `"parent_tool_use_id":"t1"`) {
		t.Fatal("the fake emitted no sub-agent event")
	}
	if !strings.Contains(got, `"tool_use_id":"t2"`) {
		t.Fatal("the stage was stopped before the second main-thread tool was answered")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("stopped after %s, which is the bound and not the quiet point", elapsed)
	}
}

// TestYieldWaitsForTheRequest: with an Ask channel, Run stops at the quiet
// point the request is made in and not at one that came before it: the
// runner asks for the yield when a `now` message arrives mid-stage, and a
// stage already between tools then stops at once.
func TestYieldWaitsForTheRequest(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"tools":[{"id":"t1","name":"Bash","sleep_s":1}],"sleep_s":30}]}`)
	ask := make(chan struct{})
	time.AfterFunc(3*time.Second, func() { close(ask) })
	start := time.Now()
	res, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(context.Background(), Request{
		Prompt: "work", SessionID: NewSessionID(), Dir: t.TempDir(),
		Env:   []string{"PATH=" + os.Getenv("PATH")},
		Yield: &YieldRequest{Ask: ask, Bound: 30 * time.Second},
	})
	if err != nil || !res.Yielded || res.YieldReason != YieldAtQuietPoint {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("stopped after %s, at a quiet point that was already past when the request came", elapsed)
	}
}

// TestYieldBoundStopsAtOnce: with no quiet point inside the bound, the stage
// is stopped at once and says so, mid-tool and all.
func TestYieldBoundStopsAtOnce(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"tools":[{"id":"t1","name":"Bash","sleep_s":30}]}]}`)
	var transcript bytes.Buffer
	start := time.Now()
	res, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(context.Background(), Request{
		Prompt: "work", SessionID: NewSessionID(), Dir: t.TempDir(),
		Env: []string{"PATH=" + os.Getenv("PATH")}, Transcript: &transcript,
		Yield: &YieldRequest{Bound: 300 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil: a bound stop is not a failure either", err)
	}
	if !res.Yielded || res.YieldReason != YieldAtBound {
		t.Fatalf("result = %+v, want a yield at the bound", res)
	}
	if !strings.Contains(transcript.String(), `"tool_use"`) {
		t.Fatal("the stage was not mid-tool: nothing to force a stop over")
	}
	if strings.Contains(transcript.String(), `"tool_result"`) {
		t.Fatal("the tool was answered after all, so the bound was not what stopped it")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("stopped after %s, not at once", elapsed)
	}
}

// TestYieldedIsNotAnError: a stop at a quiet point reports no error, names
// the session the resume needs, and carries the last usage the stream
// showed, since a yielded segment emits no result event of its own. The
// session survives it, so the stage's --resume finds it.
func TestYieldedIsNotAnError(t *testing.T) {
	script := `{"calls":[` +
		`{"tools":[{"id":"t1","name":"Bash","sleep_s":1,"usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":300,"output_tokens":40}}],"sleep_s":30},` +
		`{"text":"resumed"}]}`
	bin := testutil.FakeClaude(t, script)
	home, dir := t.TempDir(), t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	id := NewSessionID()
	res, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(context.Background(), Request{
		Prompt: "work", SessionID: id, Dir: dir, Env: env,
		Yield: &YieldRequest{Bound: 20 * time.Second},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil: a yielded stage is not a failed one", err)
	}
	if !res.Yielded || res.YieldReason != YieldAtQuietPoint {
		t.Fatalf("result = %+v, want a yield at the quiet point", res)
	}
	if res.SessionID != id {
		t.Fatalf("SessionID = %q, want %q: the resume needs it", res.SessionID, id)
	}
	if want := (Usage{Input: 100, CacheCreation: 20, CacheRead: 300, Output: 40}); res.Usage != want {
		t.Fatalf("Usage = %+v, want the last usage the stream showed (%+v)", res.Usage, want)
	}
	if _, err := os.Stat(filepath.Join(SessionDir(home, realDir(t, dir)), id+".jsonl")); err != nil {
		t.Fatalf("the yielded stage left no session to resume: %v", err)
	}
	// The stage's own resume: the same session, the message as its prompt.
	res, err = Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(context.Background(), Request{
		Prompt: "the message", SessionID: id, Resume: true, Dir: dir, Env: env,
	})
	if err != nil || res.Text != "resumed" || res.SessionID != id || res.Yielded {
		t.Fatalf("resumed run = %+v, %v", res, err)
	}
}

// TestYieldParentCancelWins: a cancel during the wait is the run's and not a
// yield: Run reports the cancellation, so a stage a person cancelled is
// never recorded as a stage that yielded (rule 2).
func TestYieldParentCancelWins(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"tools":[{"id":"t1","name":"Bash","sleep_s":30}]}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(ctx, Request{
		Prompt: "work", SessionID: NewSessionID(), Dir: t.TempDir(),
		Env:   []string{"PATH=" + os.Getenv("PATH")},
		Yield: &YieldRequest{Bound: 30 * time.Second},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if res.Yielded {
		t.Fatal("a cancelled stage was reported as yielded")
	}
}
