// Package anthropicfake is a scripted stand-in for the Anthropic Messages
// API (and Vertex's rawPredict paths, which answer the same bodies) for the
// budget gateway's tests: each request gets the next scripted reply, and
// every request is kept, body included, for byte-equality checks.
package anthropicfake

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// Event is one server-sent event, sent as written: "event: <Name>" (when
// Name is set) and "data: <Data>", then a blank line.
type Event struct {
	Name string
	Data string
}

// Reply is how the fake answers one request.
type Reply struct {
	Status     int           // 0: 200
	Header     http.Header   // sent as is
	Body       string        // a non-streaming body
	Events     []Event       // a streaming body (text/event-stream unless Header says otherwise)
	CutAfter   int           // > 0: drop the connection after this many events
	EventDelay time.Duration // between events, for the no-buffering test
	// Encoding compresses the whole body whatever the request asked:
	// "gzip", "deflate" (zlib) and "zstd" really compress; "br" sends the
	// plain bytes labelled br (the module has no brotli encoder), which is
	// enough for a gateway that must not try to read it.
	Encoding string
	// DropBeforeHeaders reads the whole request body, then closes the
	// connection without a status line.
	DropBeforeHeaders bool
	// Hold reads the whole request body, then answers nothing until the
	// request's context ends (the caller went away), which Ended reports.
	Hold bool
}

// Fake is the scripted upstream.
type Fake struct {
	// Script is the reply to each request, in order. A request past its
	// end fails the test and gets a 500.
	Script []Reply
	// Func, when set, answers every request instead of Script, from the
	// request and its body (for concurrent tests, where order is random).
	Func func(r *http.Request, body []byte) Reply

	t      testing.TB
	mu     sync.Mutex
	seen   []*http.Request
	bodies [][]byte
	ended  []chan struct{}

	authName, authValue string // RequireAuth
}

// New starts a fake upstream answering with script; it is closed when the
// test ends.
func New(t testing.TB, script ...Reply) (*Fake, *httptest.Server) {
	t.Helper()
	f := &Fake{Script: script, t: t}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

// Seen returns the requests received so far, in order, each with a fresh
// reader over its body.
func (f *Fake) Seen() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*http.Request, len(f.seen))
	for i, r := range f.seen {
		c := r.Clone(r.Context())
		c.Body = io.NopCloser(bytes.NewReader(f.bodies[i]))
		out[i] = c
	}
	return out
}

// Body returns request i's body as received.
func (f *Fake) Body(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.bodies[i]...)
}

// Count is the number of requests received so far.
func (f *Fake) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// Ended is closed once the fake saw request i's context end while it was
// still answering (holding, or between events): the caller cancelled it.
func (f *Fake) Ended(i int) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended[i]
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	i := len(f.seen)
	f.seen = append(f.seen, r.Clone(r.Context()))
	f.bodies = append(f.bodies, body)
	ended := make(chan struct{})
	f.ended = append(f.ended, ended)
	var rep Reply
	switch {
	case f.authName != "" && f.authFailsLocked(r):
		f.mu.Unlock()
		rep = Error(http.StatusUnauthorized, "authentication_error", "invalid x-api-key")
	case f.Func != nil:
		f.mu.Unlock()
		rep = f.Func(r, body)
	case i < len(f.Script):
		rep = f.Script[i]
		f.mu.Unlock()
	default:
		f.mu.Unlock()
		f.t.Errorf("anthropicfake: unexpected request %d: %s %s", i, r.Method, r.URL)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
		return
	}
	markEnded := sync.OnceFunc(func() { close(ended) })

	if rep.DropBeforeHeaders {
		panic(http.ErrAbortHandler) // closes the connection, nothing written
	}
	if rep.Hold {
		select {
		case <-r.Context().Done():
			markEnded()
		case <-time.After(30 * time.Second):
			http.Error(w, "held too long", http.StatusGatewayTimeout)
		}
		return
	}

	for k, vs := range rep.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status := rep.Status
	if status == 0 {
		status = http.StatusOK
	}
	streaming := len(rep.Events) > 0
	if w.Header().Get("Content-Type") == "" {
		if streaming {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
	}
	rc := http.NewResponseController(w)

	if rep.Encoding != "" {
		plain := []byte(rep.Body)
		if streaming {
			var b bytes.Buffer
			for _, ev := range rep.Events {
				b.WriteString(format(ev))
			}
			plain = b.Bytes()
		}
		w.Header().Set("Content-Encoding", rep.Encoding)
		w.WriteHeader(status)
		_, _ = w.Write(compress(f.t, rep.Encoding, plain))
		return
	}

	w.WriteHeader(status)
	_ = rc.Flush()
	if !streaming {
		_, _ = io.WriteString(w, rep.Body)
		return
	}
	for j, ev := range rep.Events {
		if j > 0 && rep.EventDelay > 0 {
			select {
			case <-time.After(rep.EventDelay):
			case <-r.Context().Done():
				markEnded()
				return
			}
		}
		if _, err := io.WriteString(w, format(ev)); err != nil {
			markEnded()
			return
		}
		if err := rc.Flush(); err != nil {
			markEnded()
			return
		}
		if rep.CutAfter > 0 && j+1 == rep.CutAfter {
			panic(http.ErrAbortHandler) // drops the connection mid-body
		}
	}
}

func format(ev Event) string {
	if ev.Name == "" {
		return "data: " + ev.Data + "\n\n"
	}
	return "event: " + ev.Name + "\ndata: " + ev.Data + "\n\n"
}

func compress(t testing.TB, enc string, plain []byte) []byte {
	var b bytes.Buffer
	switch enc {
	case "gzip":
		zw := gzip.NewWriter(&b)
		_, _ = zw.Write(plain)
		_ = zw.Close()
	case "deflate":
		zw := zlib.NewWriter(&b)
		_, _ = zw.Write(plain)
		_ = zw.Close()
	case "zstd":
		zw, err := zstd.NewWriter(&b)
		if err != nil {
			t.Errorf("anthropicfake: zstd: %v", err)
			return plain
		}
		_, _ = zw.Write(plain)
		_ = zw.Close()
	default: // "br" and anything else: the plain bytes, labelled
		return plain
	}
	return b.Bytes()
}

// usageJSON is usage as the API reports it: input_tokens excludes cache
// writes and reads, which are counted apart (and split by TTL).
func usageJSON(u pricing.Usage, output int64) map[string]any {
	m := map[string]any{
		"input_tokens":                u.Input,
		"cache_creation_input_tokens": u.CacheWrite5m + u.CacheWrite1h,
		"cache_read_input_tokens":     u.CacheRead,
		"cache_creation": map[string]any{
			"ephemeral_5m_input_tokens": u.CacheWrite5m,
			"ephemeral_1h_input_tokens": u.CacheWrite1h,
		},
		"output_tokens": output,
	}
	if u.WebSearches > 0 {
		m["server_tool_use"] = map[string]any{"web_search_requests": u.WebSearches}
	}
	return m
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// StartEvent is a message_start event served by model with usage u (its
// output_tokens is 1, as the API reports at the start).
func StartEvent(model string, u pricing.Usage) Event {
	usage := usageJSON(u, 1)
	usage["service_tier"] = "standard"
	return Event{Name: "message_start", Data: mustJSON(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_fake", "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": usage,
		},
	})}
}

// DeltaEvent is a message_delta event whose (cumulative) usage is u.
func DeltaEvent(u pricing.Usage) Event {
	return Event{Name: "message_delta", Data: mustJSON(map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": usageJSON(u, u.Output),
	})}
}

// Ping is a keep-alive event.
var Ping = Event{Name: "ping", Data: `{"type": "ping"}`}

// Stop is the message_stop event that ends a complete stream.
var Stop = Event{Name: "message_stop", Data: `{"type":"message_stop"}`}

// TextEvents are a text content block's events.
func TextEvents(text string) []Event {
	return []Event{
		{Name: "content_block_start", Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{Name: "content_block_delta", Data: mustJSON(map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": text},
		})},
		{Name: "content_block_stop", Data: `{"type":"content_block_stop","index":0}`},
	}
}

// StreamOK is a complete stream served by model with usage u:
// message_start, a ping, a text block, message_delta and message_stop.
func StreamOK(model string, u pricing.Usage) Reply {
	ev := []Event{StartEvent(model, u), Ping}
	ev = append(ev, TextEvents("ok")...)
	ev = append(ev, DeltaEvent(u), Stop)
	return Reply{Status: http.StatusOK, Events: ev}
}

// MessageOK is a complete non-streaming reply served by model with usage u.
func MessageOK(model string, u pricing.Usage) Reply {
	usage := usageJSON(u, u.Output)
	usage["service_tier"] = "standard"
	return Reply{Status: http.StatusOK, Body: mustJSON(map[string]any{
		"id": "msg_fake", "type": "message", "role": "assistant", "model": model,
		"content":     []any{map[string]any{"type": "text", "text": "ok"}},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": usage,
	})}
}

// Error is an API error reply: status, and a body naming the error type.
func Error(status int, typ, message string) Reply {
	return Reply{Status: status, Body: fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, typ, message)}
}
