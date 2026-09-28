package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/dimipaun/fugaro/internal/logtail"
)

const (
	relayTextBytes = 2000
	relayToolBytes = 300
	// relayMaxLine bounds the relay's own buffer. A longer stream-json
	// line (in practice a successful tool result carrying a large file,
	// which the relay would not log anyway) is dropped whole; it is still
	// in the transcript.
	relayMaxLine = 4 << 20
)

// Relay logs a concise, live view of claude's stream-json output (design
// §10: stream "agent"). It sits behind the transcript's Redactor, and it
// redacts again after decoding, because JSON escapes (\u0074oken) hide a
// secret from a byte-level redactor. Every decoded string is redacted
// before it is trimmed, cut or re-encoded, and every message once more
// before it is clipped and logged, so no transformation can reassemble or
// split a secret past the redactor.
//
// A Relay is not safe for concurrent use; it takes one stream.
type Relay struct {
	log     *slog.Logger
	forms   []string
	buf     []byte
	discard bool // dropping the rest of an oversized line
}

// NewRelay returns a Relay logging to log, redacting secrets.
func NewRelay(log *slog.Logger, secrets []string) *Relay {
	return &Relay{log: log.With("stream", "agent"), forms: redactForms(secrets)}
}

func (r *Relay) redact(s string) string { return replaceAll(s, r.forms) }

// msg redacts a message, then clips it to n bytes. Redacting first means a
// secret straddling the cut is never half-published.
func (r *Relay) msg(s string, n int) string { return logtail.Clip(r.redact(s), n) }

// Write buffers p and logs each complete line. It never fails.
func (r *Relay) Write(p []byte) (int, error) {
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		chunk := rest
		if i >= 0 {
			chunk = rest[:i]
		}
		if !r.discard {
			if len(r.buf)+len(chunk) > relayMaxLine {
				r.buf, r.discard = r.buf[:0], true
			} else {
				r.buf = append(r.buf, chunk...)
			}
		}
		if i < 0 {
			break
		}
		if !r.discard {
			r.line(r.buf)
		}
		r.buf, r.discard = r.buf[:0], false
		rest = rest[i+1:]
	}
	return len(p), nil
}

// Flush logs a trailing line without a newline.
func (r *Relay) Flush() {
	if len(r.buf) > 0 && !r.discard {
		r.line(r.buf)
	}
	r.buf, r.discard = nil, false
}

type relayEvent struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	SessionID string  `json:"session_id"`
	Model     string  `json:"model"`
	IsError   bool    `json:"is_error"`
	Cost      float64 `json:"total_cost_usd"`
	Message   struct {
		Content []struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			IsError bool            `json:"is_error"`
			Content json.RawMessage `json:"content"`
		} `json:"content"`
	} `json:"message"`
}

func (r *Relay) line(raw []byte) {
	var ev relayEvent
	if json.Unmarshal(bytes.TrimSpace(raw), &ev) != nil {
		return
	}
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			r.log.Info(r.msg("session "+r.redact(ev.SessionID)+", model "+r.redact(ev.Model), relayToolBytes), "event", "init")
		}
	case "assistant":
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(r.redact(c.Text)); t != "" {
					r.log.Info(r.msg(t, relayTextBytes), "event", "text")
				}
			case "tool_use":
				r.log.Info(r.msg("tool "+r.redact(c.Name)+": "+r.toolSummary(c.Input), relayToolBytes), "event", "tool")
			}
		}
	case "user":
		for _, c := range ev.Message.Content {
			if c.Type == "tool_result" && c.IsError {
				first, _, _ := strings.Cut(r.contentText(c.Content), "\n")
				r.log.Warn(r.msg("tool error: "+first, relayToolBytes), "event", "tool_error")
			}
		}
	case "result":
		r.log.Info(r.msg("result "+r.redact(ev.Subtype), relayToolBytes), "event", "result", "cost_usd", ev.Cost, "is_error", ev.IsError)
	}
}

// toolSummary returns the first of the input's command, file_path, pattern
// or description, whitespace collapsed, or else the whole input re-encoded
// as JSON. It is built from the decoded, redacted input, never from the
// raw bytes, which may spell a secret with \u escapes.
func (r *Relay) toolSummary(input json.RawMessage) string {
	var v any
	if json.Unmarshal(input, &v) != nil {
		return ""
	}
	v = r.redactValue(v)
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"command", "file_path", "pattern", "description"} {
			if s, ok := m[k].(string); ok && s != "" {
				return strings.Join(strings.Fields(s), " ")
			}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // as jsonEscape, so the escaped forms match
	if enc.Encode(v) != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// contentText flattens a tool_result's content, a string or a list of
// {type: text, text} blocks, redacting it before it is cut into lines.
func (r *Relay) contentText(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	switch v := r.redactValue(v).(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, b := range v {
			if m, ok := b.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// redactValue returns a decoded JSON value with every string in it, map
// keys included, redacted.
func (r *Relay) redactValue(v any) any {
	switch v := v.(type) {
	case string:
		return r.redact(v)
	case []any:
		for i := range v {
			v[i] = r.redactValue(v[i])
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[r.redact(k)] = r.redactValue(e)
		}
		return out
	}
	return v
}
