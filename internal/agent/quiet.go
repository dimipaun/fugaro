package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// QuietPoint tracks the main thread's tool calls in a stage's stream-json
// output and reports its quiet points: the moments when every tool_use the
// main thread issued has its tool_result. Stopping the agent there loses
// only the model's in-flight reply to the last tool result, never a running
// tool, which is what makes a --now yield safe (run-messages rule 2, D2945).
//
// Only the main thread counts. A sub-agent's events carry the tool_use id
// of the Task call that started it in parent_tool_use_id, and the calls the
// sub-agent makes are its own thread's: they neither hold a quiet point off
// nor reach one. The Task call itself is one main-thread tool_use like any
// other, so the quiet point still waits for its own tool_result.
//
// One assistant message may arrive as one event per content block (U3), so
// every tool_use block of every assistant event is counted, and every
// tool_result block answers one id by that id.
//
// Observe runs on the goroutine copying the stage's output; AtQuiet and
// LastUsage are read by the yield's watcher, so the state is behind a mutex.
type QuietPoint struct {
	mu      sync.Mutex
	pending map[string]struct{}
	seen    bool // a main-thread tool_use has been issued
	usage   Usage
	haveUse bool
}

// Observe decodes one stream-json line and reports whether the line leaves
// the main thread at a quiet point: a tool_use has been issued and every
// one of them is answered. A sub-agent's event (one with a
// parent_tool_use_id) is ignored whole, so what is reported is the main
// thread's own state; a line that does not decode reports false.
func (q *QuietPoint) Observe(line []byte) bool {
	var ev quietEvent
	if json.Unmarshal(bytes.TrimSpace(line), &ev) != nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if ev.ParentToolUseID == "" {
		switch ev.Type {
		case "assistant":
			for _, c := range ev.Message.Content {
				if c.Type == "tool_use" && c.ID != "" {
					if q.pending == nil {
						q.pending = map[string]struct{}{}
					}
					q.pending[c.ID] = struct{}{}
					q.seen = true
				}
			}
			// The assistant message's own usage is the last one a stage
			// that never reaches its result event reports: a yielded
			// segment's token figure comes from here.
			if ev.Message.Usage != nil {
				u := ev.Message.Usage
				q.usage = Usage{Input: u.Input, CacheCreation: u.CacheCreation, CacheRead: u.CacheRead, Output: u.Output}
				q.haveUse = true
			}
		case "user":
			for _, c := range ev.Message.Content {
				if c.Type == "tool_result" && c.ToolUseID != "" {
					delete(q.pending, c.ToolUseID)
				}
			}
		}
	}
	return q.quiet()
}

// AtQuiet reports the main thread's state now: a tool_use has been issued
// and none is unanswered.
func (q *QuietPoint) AtQuiet() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.quiet()
}

// LastUsage is the usage of the last main-thread assistant event seen, and
// false when the stream showed none.
func (q *QuietPoint) LastUsage() (Usage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.usage, q.haveUse
}

// quiet is the quiet-point predicate, with the lock held.
func (q *QuietPoint) quiet() bool { return q.seen && len(q.pending) == 0 }

// quietEvent is the part of a stream-json event a quiet point reads: the
// event's type and parent, and the message's content blocks and usage. The
// tool_result's content is deliberately not decoded, so a large tool output
// is skipped rather than held.
type quietEvent struct {
	Type            string `json:"type"`
	ParentToolUseID string `json:"parent_tool_use_id"`
	Message         struct {
		Content []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ToolUseID string `json:"tool_use_id"`
		} `json:"content"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			Output        int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// quietWriter copies the stage's output to w and feeds every complete line
// to q, signalling ch without blocking when a line leaves the main thread at
// a quiet point. It sits between the process and the pipe ParseStream reads,
// so a stage that asked for no yield keeps its own writer and its own path.
//
// A line longer than relayMaxLine is dropped whole, as the relay drops it.
// The tool call it carried then stays unanswered, and the yield waits for
// its bound rather than stopping mid-tool.
type quietWriter struct {
	w       io.Writer
	q       *QuietPoint
	ch      chan<- struct{}
	buf     []byte
	dropped bool
}

func (w *quietWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.feed(p)
	return n, err
}

func (w *quietWriter) feed(p []byte) {
	for {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.append(p)
			return
		}
		w.append(p[:i])
		if !w.dropped && w.q.Observe(w.buf) {
			select {
			case w.ch <- struct{}{}:
			default:
			}
		}
		w.buf, w.dropped = w.buf[:0], false
		p = p[i+1:]
	}
}

func (w *quietWriter) append(p []byte) {
	if w.dropped {
		return
	}
	if len(w.buf)+len(p) > relayMaxLine {
		w.buf, w.dropped = w.buf[:0], true
		return
	}
	w.buf = append(w.buf, p...)
}

// yieldOutcome is what a yield request's watcher recorded: whether it
// stopped the stage, and the reason. Run reads it once the process is gone.
type yieldOutcome struct {
	mu      sync.Mutex
	stopped bool
	reason  string
}

func (o *yieldOutcome) record(reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped, o.reason = true, reason
}

func (o *yieldOutcome) get() (bool, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stopped, o.reason
}

// watchYield stops a yielded stage: it waits for y's request, then for the
// stream's next quiet point, and stops the stage there -- or at once, with
// the bound as the reason, when y.Bound passes first. A stage already at a
// quiet point when the request arrives stops at once: the model's in-flight
// reply is the only thing a stop there loses, and the alternative is waiting
// for a tool call the agent may never make again.
//
// It never touches the caller's context: runCtx is cancelled here, and a
// cancel or a halt from the outside cancels it too, so Run sees the parent
// context ended and reports the error rather than a yield.
func watchYield(runCtx context.Context, y *YieldRequest, q *QuietPoint, quietCh <-chan struct{}, out *yieldOutcome, stop context.CancelFunc) {
	if y.Ask != nil {
		select {
		case <-y.Ask:
		case <-runCtx.Done():
			return
		}
	}
	// A quiet point seen before the request says nothing about now, and the
	// state itself does: a stage between tools when the request arrives
	// stops here.
	select {
	case <-quietCh:
	default:
	}
	if q.AtQuiet() {
		out.record(YieldAtQuietPoint)
		stop()
		return
	}
	var bound <-chan time.Time
	if y.Bound > 0 {
		t := time.NewTimer(y.Bound)
		defer t.Stop()
		bound = t.C
	}
	for {
		select {
		case <-quietCh:
			out.record(YieldAtQuietPoint)
			stop()
			return
		case <-bound:
			out.record(YieldAtBound)
			stop()
			return
		case <-runCtx.Done():
			return
		}
	}
}
