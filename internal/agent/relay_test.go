package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestRelay(t *testing.T) {
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), nil)
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-5-5"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Reading the code."},{"type":"tool_use","name":"Bash","input":{"command":"fugaro verify test"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","is_error":false,"content":"lots of output"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"exit 1\nmore"}]}}`,
		`not json`,
		`{"type":"result","subtype":"success","total_cost_usd":0.5,"is_error":false}`,
	}
	for _, l := range lines {
		_, _ = r.Write([]byte(l + "\n"))
	}
	r.Flush()
	var events []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		events = append(events, m)
	}
	want := []struct{ event, msg string }{
		{"init", "session s1, model claude-opus-5-5"},
		{"text", "Reading the code."},
		{"tool", "tool Bash: fugaro verify test"},
		{"tool_error", "tool error: exit 1"},
		{"result", "result success"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for i, w := range want {
		if events[i]["event"] != w.event || events[i]["msg"] != w.msg || events[i]["stream"] != "agent" {
			t.Errorf("event %d = %v, want %+v", i, events[i], w)
		}
	}
}

func TestRelayRedactsAfterDecoding(t *testing.T) {
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), []string{"hunter2-token"})
	// "\u0068unter2-token" is "hunter2-token" once decoded: no byte-level redactor sees it.
	_, _ = r.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"key \u0068unter2-token"}]}}` + "\n"))
	if strings.Contains(buf.String(), "hunter2-token") || !strings.Contains(buf.String(), "[REDACTED]") {
		t.Fatalf("logs = %s", buf.String())
	}
}

func TestRelayClipsLongText(t *testing.T) {
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), nil)
	line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 10000)}}}})
	_, _ = r.Write(append(line, '\n'))
	if buf.Len() > 3000 {
		t.Fatalf("relay logged %d bytes for one event", buf.Len())
	}
}

// unicodeEscape spells every byte of an ASCII s as a \u00XX JSON escape, so
// no form of s appears in the encoded line: only a decoder sees it.
func unicodeEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		fmt.Fprintf(&b, `\u%04x`, c)
	}
	return b.String()
}

// assertNoSecret fails if any logged byte, or any decoded string value of
// any logged entry, carries secret or one of its escaped spellings. Decoding
// matters: slog's JSON handler escapes <, > and &, so a raw byte search
// alone would miss a secret containing them.
func assertNoSecret(t *testing.T, out string, secret string) {
	t.Helper()
	// The collapsed form catches a summary that squeezed a secret's
	// whitespace before redacting it.
	forms := []string{secret, jsonEscape(secret), unicodeEscape(secret), strings.Join(strings.Fields(secret), " ")}
	for _, form := range forms {
		if strings.Contains(out, form) {
			t.Fatalf("log carries secret form %q:\n%s", form, out)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", l)
		}
		for k, v := range m {
			s := fmt.Sprint(v)
			for _, form := range forms {
				if strings.Contains(s, form) || strings.Contains(k, form) {
					t.Fatalf("log field %q carries secret form %q: %q", k, form, s)
				}
			}
		}
	}
}

// TestRelayNeverLogsSecretsInToolEvents plants known secrets in every place a
// tool_use or tool_result can carry them, spelled raw, JSON-escaped and fully
// \u-escaped, and checks that none reaches the log.
func TestRelayNeverLogsSecretsInToolEvents(t *testing.T) {
	// Contains HTML-sensitive characters and a quote, so the re-encoded
	// fallback summary and slog's own escaping are both exercised.
	secret := `sk-live-<A&b>"9f3QzX`
	spaced := "pass  phrase   word42" // collapsing whitespace must not unmask it
	pem := "-----BEGIN KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEF\nAASCBKcwggSjAgEAAoIBAQC7\n-----END KEY-----"
	// Single spaces: spelled with tabs, newlines or runs of spaces, no
	// form of it is in the input, but toolSummary's whitespace collapse
	// rebuilds it exactly. Only msg's final redaction catches that.
	single := "correct horse battery"
	secrets := []string{secret, spaced, pem, single}

	enc := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	uesc := func(s string) string { return `"` + unicodeEscape(s) + `"` }
	tool := func(input string) string {
		return `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":` + input + `}]}}`
	}
	result := func(isError bool, content string) string {
		return fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","is_error":%v,"content":%s}]}}`, isError, content)
	}
	var lines []string
	for _, s := range secrets {
		for _, v := range []string{enc(s), uesc(s)} {
			lines = append(lines,
				tool(`{"command":"curl -H 'Authorization: `+strings.Trim(v, `"`)+`'"}`),
				tool(`{"command":`+v+`}`),
				tool(`{"file_path":`+v+`}`),
				tool(`{"pattern":`+v+`}`),
				tool(`{"description":`+v+`}`),
				// No summary key: the fallback prints the input JSON.
				tool(`{"content":`+v+`,"nested":{"env":[`+v+`]}}`),
				tool(`{`+v+`:"as a key"}`),
				result(true, v),
				result(true, `[{"type":"text","text":`+v+`}]`),
				result(false, v), // successful results are never logged
				`{"type":"system","subtype":"init","session_id":`+v+`,"model":`+v+`}`,
				`{"type":"result","subtype":`+v+`,"total_cost_usd":1,"is_error":true}`,
			)
		}
	}
	for _, spelled := range []string{`correct\thorse\nbattery`, `correct  horse   battery`, ` correct\r\nhorse\t\tbattery `} {
		lines = append(lines,
			tool(`{"command":"login `+spelled+`"}`),
			tool(`{"description":"`+spelled+`"}`),
		)
	}
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), secrets)
	for _, l := range lines {
		_, _ = r.Write([]byte(l + "\n"))
	}
	r.Flush()
	out := buf.String()
	for _, s := range append(secrets, strings.Split(pem, "\n")[1:3]...) {
		assertNoSecret(t, out, s)
	}
	if !strings.Contains(out, "[REDACTED]") || !strings.Contains(out, `"event":"tool_error"`) {
		t.Fatalf("expected redacted tool events, got:\n%s", out)
	}
	// Successful tool results never reach the log, redacted or not.
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(l, `"event":"tool_result"`) {
			t.Fatalf("successful tool result logged: %s", l)
		}
	}
}

// TestRelayBoundsEveryEntry checks that no entry grows with its input, even
// for tool inputs and errors far past the clip limits.
func TestRelayBoundsEveryEntry(t *testing.T) {
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), nil)
	huge := strings.Repeat("y", 50000)
	for _, l := range []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"` + huge + `"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"` + huge + `","input":{"other":"` + huge + `"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"` + huge + `"}]}}`,
		`{"type":"system","subtype":"init","session_id":"` + huge + `","model":"m"}`,
		`{"type":"result","subtype":"` + huge + `","total_cost_usd":1,"is_error":false}`,
	} {
		_, _ = r.Write([]byte(l + "\n"))
	}
	entries := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(entries) != 5 {
		t.Fatalf("got %d entries:\n%s", len(entries), buf.String())
	}
	for _, e := range entries {
		if len(e) > 1000 {
			t.Fatalf("entry is %d bytes: %.200s…", len(e), e)
		}
	}
}

// TestRelayDropsOversizedLines checks that the relay's own buffer stays
// bounded for a line past relayMaxLine, and that it resynchronizes on the
// next newline.
func TestRelayDropsOversizedLines(t *testing.T) {
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), nil)
	head := `{"type":"assistant","message":{"content":[{"type":"text","text":"`
	_, _ = r.Write([]byte(head))
	chunk := bytes.Repeat([]byte("z"), 64<<10)
	for n := 0; n < relayMaxLine+len(chunk); n += len(chunk) {
		_, _ = r.Write(chunk)
		if len(r.buf) > relayMaxLine {
			t.Fatalf("relay buffers %d bytes, cap %d", len(r.buf), relayMaxLine)
		}
	}
	_, _ = r.Write([]byte(`"}]}}` + "\n" + head + `after"}]}}` + "\n"))
	r.Flush()
	if strings.Count(buf.String(), "\n") != 1 || !strings.Contains(buf.String(), `"msg":"after"`) {
		t.Fatalf("logs = %.500s", buf.String())
	}
}

// secretOnlyRuns returns every 8-character run of the base64 encoding of
// in whose characters encode only bytes of in[lo:hi]: the part of an
// encoded secret that reveals it, wherever it sits in the input.
func secretOnlyRuns(enc *base64.Encoding, in []byte, lo, hi int) []string {
	out := enc.EncodeToString(in)
	var runs []string
	for g := 0; 3*g+6 <= len(in); g++ {
		if 3*g >= lo && 3*g+6 <= hi && 4*g+8 <= len(out) {
			runs = append(runs, out[4*g:4*g+8])
		}
	}
	return runs
}

// TestRelayRedactsEncodedSecrets checks the encoded forms the shared
// redactor registers: a tool result carrying the output of
// `echo $TOKEN | base64` (a trailing newline shifts the final group),
// base64 of the secret inside a longer string at each byte offset, the
// URL-safe alphabet, and percent-encoding.
func TestRelayRedactsEncodedSecrets(t *testing.T) {
	secret := "ghs_T0ken+With/Sl=sh?&x~!"
	b64 := base64.StdEncoding
	type planted struct {
		enc    *base64.Encoding
		in     []byte
		lo, hi int
	}
	var cases []planted
	for _, prefix := range []string{"", "A", "AB", "Authorization: Bearer "} {
		in := []byte(prefix + secret + "\n")
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
			cases = append(cases, planted{enc, in, len(prefix), len(prefix) + len(secret)})
		}
	}
	errResult := func(content string) string {
		c, _ := json.Marshal(content)
		return `{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":` + string(c) + `}]}}`
	}
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo $TOKEN | base64"}}]}}`,
	}
	for _, c := range cases {
		lines = append(lines, errResult(c.enc.EncodeToString(c.in)+"\nexit 1"))
	}
	for _, e := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		lines = append(lines, errResult("decoded: "+e.EncodeToString([]byte(secret))))
	}
	lines = append(lines,
		errResult("curl: (22) 401 for https://api.invalid/?token="+url.QueryEscape(secret)),
		errResult("curl: (22) 401 for https://api.invalid/t/"+url.PathEscape(secret)+"/x"),
	)
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), []string{secret})
	for _, l := range lines {
		_, _ = r.Write([]byte(l + "\n"))
	}
	out := buf.String()
	if n := strings.Count(out, `"event":"tool_error"`); n != len(lines)-1 {
		t.Fatalf("got %d tool errors, want %d:\n%s", n, len(lines)-1, out)
	}
	if n := strings.Count(out, "[REDACTED]"); n < len(lines)-1 {
		t.Fatalf("only %d redactions:\n%s", n, out)
	}
	for _, c := range cases {
		for _, run := range secretOnlyRuns(c.enc, c.in, c.lo, c.hi) {
			if strings.Contains(out, run) {
				t.Fatalf("log carries base64 run %q of the secret:\n%s", run, out)
			}
		}
	}
	for _, form := range []string{b64.EncodeToString([]byte(secret)), url.QueryEscape(secret), url.PathEscape(secret), secret} {
		assertNoSecret(t, out, form)
	}
}
