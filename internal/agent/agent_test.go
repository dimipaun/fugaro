package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestArgs(t *testing.T) {
	got := Args(Request{SessionID: "s1", AppendSystemPrompt: "rules", Model: "m", MaxBudgetUSD: 12.5, JSONSchema: `{}`})
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions",
		"--session-id", "s1", "--append-system-prompt", "rules", "--model", "m", "--max-budget-usd", "12.50", "--json-schema", "{}"}
	if !slices.Equal(got, want) {
		t.Fatalf("Args = %q\nwant   %q", got, want)
	}
	resumed := Args(Request{SessionID: "s1", Resume: true})
	if !slices.Contains(resumed, "--resume") || slices.Contains(resumed, "--session-id") {
		t.Fatalf("resume args = %q", resumed)
	}
}

func TestParseStream(t *testing.T) {
	f, err := os.Open("testdata/stream-success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var transcript bytes.Buffer
	res, found, err := ParseStream(f, &transcript)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if res.SessionID != "3f2a9c1e-0000-4000-8000-000000000001" || res.CostUSD != 0.049912 || res.IsError || res.Subtype != "success" {
		t.Fatalf("result = %+v", res)
	}
	if string(res.Structured) != `{"verdict":"ship"}` {
		t.Fatalf("structured = %s", res.Structured)
	}
	if strings.Count(transcript.String(), `"session_id"`) != 3 {
		t.Fatal("transcript did not receive every line")
	}
}

func TestParseStreamNoResult(t *testing.T) {
	_, found, err := ParseStream(strings.NewReader(`{"type":"system"}`+"\n"), nil)
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestParseStreamInitSessionID(t *testing.T) {
	const initID, resultID = "3f2a9c1e-0000-4000-8000-000000000001", "3f2a9c1e-0000-4000-8000-000000000002"
	init := `{"type":"system","subtype":"init","session_id":"` + initID + `"}` + "\n"
	// A process killed before its result event: the init event's ID.
	res, found, err := ParseStream(strings.NewReader(init), nil)
	if err != nil || found || res.SessionID != initID {
		t.Fatalf("init only: res = %+v, found = %v, err = %v", res, found, err)
	}
	// The result event is the final authority when the two differ.
	result := `{"type":"result","subtype":"success","session_id":"` + resultID + `"}` + "\n"
	if res, found, err := ParseStream(strings.NewReader(init+result), nil); err != nil || !found || res.SessionID != resultID {
		t.Fatalf("init and result: res = %+v, found = %v, err = %v", res, found, err)
	}
	// A result event without an ID keeps the init event's.
	if res, _, _ := ParseStream(strings.NewReader(init+`{"type":"result","subtype":"success"}`+"\n"), nil); res.SessionID != initID {
		t.Fatalf("result without an ID: res = %+v", res)
	}
}

func TestClaudeRun(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"text":"hello","cost":0.5}]}`)
	var transcript bytes.Buffer
	res, err := Claude{Bin: bin}.Run(context.Background(), Request{
		Prompt: "do the thing", SessionID: "s-1", Dir: t.TempDir(), Env: []string{"PATH=" + os.Getenv("PATH")}, Transcript: &transcript,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello" || res.CostUSD != 0.5 || res.SessionID != "s-1" || res.ExitCode != 0 {
		t.Fatalf("result = %+v", res)
	}
	calls := testutil.FakeClaudeCalls(t, bin)
	if len(calls) != 1 || calls[0].Prompt != "do the thing" || !slices.Contains(calls[0].Args, "--session-id") {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestClaudeRunTimeout(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"sleep_s":30}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(ctx, Request{SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}

// cancelOnInit cancels once the init event has passed through.
type cancelOnInit struct {
	cancel context.CancelFunc
	buf    bytes.Buffer
}

func (c *cancelOnInit) Write(p []byte) (int, error) {
	c.buf.Write(p)
	if strings.Contains(c.buf.String(), `"init"`) {
		c.cancel()
	}
	return len(p), nil
}

func TestClaudeRunKilledKeepsInitSessionID(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"sleep_s":30}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(ctx, Request{
		SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}, Transcript: &cancelOnInit{cancel: cancel},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	// Killed before its result event, the run still names its session.
	if res.SessionID != "s" {
		t.Fatalf("SessionID = %q, want the init event's", res.SessionID)
	}
}

func TestClaudeRunNoResult(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"no_result":true,"exit":1}]}`)
	_, err := Claude{Bin: bin}.Run(context.Background(), Request{SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}})
	if err == nil || !strings.Contains(err.Error(), "without a result event") {
		t.Fatalf("err = %v", err)
	}
}

func TestClaudeRunNilEnvRefused(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"text":"hello"}]}`)
	_, err := Claude{Bin: bin}.Run(context.Background(), Request{SessionID: "s"})
	if err == nil || !strings.Contains(err.Error(), "nil Env") {
		t.Fatalf("err = %v, want a nil-Env refusal", err)
	}
	if calls := testutil.FakeClaudeCalls(t, bin); len(calls) != 0 {
		t.Fatalf("fake claude was started: %+v", calls)
	}
}

func TestClaudeRunNonZeroExitWithResult(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"text":"done anyway","exit":2}]}`)
	res, err := Claude{Bin: bin}.Run(context.Background(), Request{SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}})
	if err != nil {
		t.Fatalf("err = %v, want nil (a result event was still emitted)", err)
	}
	if res.ExitCode != 2 || res.Text != "done anyway" {
		t.Fatalf("result = %+v", res)
	}
}

func TestClaudeRunStructuredPassesThrough(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"text":"ok","structured":{"verdict":"ship","score":3}}]}`)
	res, err := Claude{Bin: bin}.Run(context.Background(), Request{SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Verdict string `json:"verdict"`
		Score   int    `json:"score"`
	}
	if err := json.Unmarshal(res.Structured, &got); err != nil {
		t.Fatalf("Structured = %s: %v", res.Structured, err)
	}
	if got.Verdict != "ship" || got.Score != 3 {
		t.Fatalf("Structured decoded to %+v", got)
	}
}

func TestClaudeRunCapturesStderr(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"shell":"echo agent-stderr-marker 1>&2","text":"ok"}]}`)
	var stderr bytes.Buffer
	_, err := Claude{Bin: bin}.Run(context.Background(), Request{
		SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}, Stderr: &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "agent-stderr-marker") {
		t.Fatalf("stderr = %q, want the shell's output", stderr.String())
	}
}

// realDir returns dir with symlinks resolved, as the fake (like Claude
// Code) sees its working directory.
func realDir(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestFakeClaudeWritesSession(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"text":"one"},{"text":"two"}]}`)
	home, dir := t.TempDir(), t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	id := NewSessionID()
	res, err := Claude{Bin: bin}.Run(context.Background(), Request{Prompt: "first", SessionID: id, Dir: dir, Env: env})
	if err != nil || res.SessionID != id {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	res, err = Claude{Bin: bin}.Run(context.Background(), Request{Prompt: "second", SessionID: id, Resume: true, Dir: dir, Env: env})
	if err != nil || res.SessionID != id {
		t.Fatalf("resumed run = %+v, %v", res, err)
	}
	data, err := os.ReadFile(filepath.Join(SessionDir(home, realDir(t, dir)), id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"first"`) || !strings.Contains(lines[1], `"second"`) {
		t.Fatalf("session file = %q", data)
	}
}

func TestFakeClaudeNoHomeNoSession(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"text":"one"},{"text":"two"}]}`)
	dir := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH")}
	if _, err := (Claude{Bin: bin}).Run(context.Background(), Request{SessionID: "s-1", Dir: dir, Env: env}); err != nil {
		t.Fatal(err)
	}
	// Without HOME the fake keeps no sessions, so a resume isn't checked.
	if _, err := (Claude{Bin: bin}).Run(context.Background(), Request{SessionID: "s-2", Resume: true, Dir: dir, Env: env}); err != nil {
		t.Fatal(err)
	}
}

func TestFakeClaudeResumeMissing(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"text":"never"}]}`)
	var stderr bytes.Buffer
	id := NewSessionID()
	res, err := Claude{Bin: bin}.Run(context.Background(), Request{
		SessionID: id, Resume: true, Dir: t.TempDir(), Stderr: &stderr,
		Env: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
	})
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
	if res.ExitCode != 1 || !strings.Contains(stderr.String(), "No conversation found with session ID: "+id) {
		t.Fatalf("result = %+v, stderr = %q", res, stderr.String())
	}
}

func TestClaudeRunOtherFailureIsNotNoSession(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"shell":"echo No conversation found with session ID: x 1>&2","no_result":true,"exit":1}]}`)
	// Not a resume: the same text doesn't mean a missing session.
	_, err := Claude{Bin: bin}.Run(context.Background(), Request{SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}})
	if err == nil || errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
}

func TestBuildEnvScrubs(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_API_KEY=key-123", "NPM_TOKEN=npm-456", "AWS_SECRET_ACCESS_KEY=nope", "GOOGLE_APPLICATION_CREDENTIALS=/sa.json"}
	env, secrets, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Secrets: []string{"NPM_TOKEN"}, Set: map[string]string{"FUGARO_STATE_DIR": "/state"}, PathPrepend: "/fugaro/bin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PATH=/fugaro/bin:/bin", "HOME=/h", "ANTHROPIC_API_KEY=key-123", "NPM_TOKEN=npm-456", "FUGARO_STATE_DIR=/state"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %v", want, env)
		}
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "AWS_") || strings.HasPrefix(kv, "GOOGLE_APPLICATION_CREDENTIALS") {
			t.Errorf("undeclared variable leaked: %s", kv)
		}
	}
	if !slices.Equal(secrets, []string{"key-123", "npm-456"}) {
		t.Fatalf("secrets = %v", secrets)
	}
}

func TestBuildEnvAuthModes(t *testing.T) {
	env, _, err := BuildEnv([]string{"CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=p"}, EnvSpec{Auth: "vertex"})
	if err != nil || !slices.Contains(env, "CLAUDE_CODE_USE_VERTEX=1") || !slices.Contains(env, "CLOUD_ML_REGION=us-east5") {
		t.Fatalf("vertex env = %v, %v", env, err)
	}
	if _, _, err := BuildEnv(nil, EnvSpec{Auth: "vertex"}); err == nil {
		t.Fatal("vertex without region/project should fail")
	}
	if _, _, err := BuildEnv(nil, EnvSpec{Auth: "oauth"}); err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("oauth without token err = %v", err)
	}
	if _, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=key-123"}, EnvSpec{Auth: "api-key", Secrets: []string{"MISSING"}}); err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("missing secret err = %v", err)
	}
}

func TestBuildEnvShortSecret(t *testing.T) {
	if _, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=abc"}, EnvSpec{Auth: "api-key"}); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("short auth secret err = %v", err)
	}
	if _, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=key-123", "SHORT=abc"}, EnvSpec{Auth: "api-key", Secrets: []string{"SHORT"}}); err == nil || !strings.Contains(err.Error(), "SHORT") {
		t.Fatalf("short declared secret err = %v", err)
	}
}

func TestBuildEnvNoPathTrailingSeparator(t *testing.T) {
	env, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=key-123"}, EnvSpec{Auth: "api-key", PathPrepend: "/fugaro/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "PATH=/fugaro/bin") {
		t.Fatalf("env = %v, want PATH=/fugaro/bin with no trailing separator", env)
	}
}

func TestRedactor(t *testing.T) {
	var out bytes.Buffer
	r := NewRedactor(&out, []string{"s3cret-value", "ab"}) // secrets shorter than 4 bytes are ignored
	for _, chunk := range []string{"token=s3cr", "et-value ok\nab ", "tail s3cret-value"} {
		if _, err := r.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "token=[REDACTED] ok\nab tail [REDACTED]"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestRedactorJSONEscaped(t *testing.T) {
	secret := `p"ss-1234` // contains a quote, so its JSON-escaped form differs from its raw form
	line, err := json.Marshal(map[string]string{"key": secret})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := NewRedactor(&out, []string{secret})
	if _, err := r.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), secret) {
		t.Fatalf("secret leaked: %s", out.String())
	}
	if !strings.Contains(out.String(), "[REDACTED]") {
		t.Fatalf("secret was not redacted: %s", out.String())
	}
}

func TestRedactorMultilineSecret(t *testing.T) {
	secret := "-----BEGIN KEY-----\nsome-key-material-here\n-----END KEY-----"
	var out bytes.Buffer
	r := NewRedactor(&out, []string{secret})

	// Case 1: JSON-escaped onto a single line, as it would appear inside a
	// stream-json transcript line.
	escapedLine, err := json.Marshal(map[string]string{"key": secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write(append(escapedLine, '\n')); err != nil {
		t.Fatal(err)
	}

	// Case 2: arrives raw, split across lines (e.g. a shell echoing a PEM
	// key straight to stderr).
	if _, err := r.Write([]byte(secret + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(secret, "\n") {
		if strings.Contains(out.String(), line) {
			t.Fatalf("secret line %q leaked: %s", line, out.String())
		}
	}
}

func TestRedact(t *testing.T) {
	pem := "-----BEGIN KEY-----\nsome-key-material-here\n-----END KEY-----"
	quoted := `p"ss-1234`
	in := "key s3cret-value, pem line some-key-material-here, escaped p\\\"ss-1234, short ab"
	got := Redact(in, []string{"s3cret-value", pem, quoted, "ab"})
	want := "key [REDACTED], pem line [REDACTED], escaped [REDACTED], short ab"
	if got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}

func TestRedactEncodedForms(t *testing.T) {
	secret := "tok-9f3Q/zX+abc=?&"
	for _, in := range []string{
		base64.StdEncoding.EncodeToString([]byte(secret)),
		base64.RawStdEncoding.EncodeToString([]byte(secret)),
		base64.URLEncoding.EncodeToString([]byte(secret)),
		base64.StdEncoding.EncodeToString([]byte(secret + "\n")),
		base64.StdEncoding.EncodeToString([]byte("x" + secret)),
		base64.StdEncoding.EncodeToString([]byte("xy" + secret)),
		url.QueryEscape(secret),
		url.PathEscape(secret),
	} {
		got := Redact("value "+in+" end", []string{secret})
		if !strings.Contains(got, "[REDACTED]") || strings.Contains(got, in) {
			t.Errorf("Redact(%q) = %q", in, got)
		}
	}
	// Below encodedMin only the raw forms count: the base64 of a short
	// secret is too short a pattern to redact safely.
	short := "pw-12345"[:7]
	enc := base64.StdEncoding.EncodeToString([]byte(short))
	if got := Redact(enc, []string{short}); got != enc {
		t.Errorf("short secret's base64 redacted: %q", got)
	}
}

// TestRedactWrappedBase64 checks a long secret's base64 as GNU `base64`
// (76 columns) and `openssl base64` (64) wrap it, through Redact and
// through the line-by-line Redactor fed in small chunks.
func TestRedactWrappedBase64(t *testing.T) {
	for _, secret := range longSecrets() {
		for _, w := range wrapWidths {
			for _, in := range []string{secret, secret + "\n", secret + ":user@host\n"} {
				text := "$ echo $TOKEN | base64\n" + wrapLines(base64.StdEncoding.EncodeToString([]byte(in)), w) + "\n$ echo done\n"
				got := Redact(text, []string{secret})
				if !strings.Contains(got, "[REDACTED]") {
					t.Fatalf("len %d width %d: nothing redacted:\n%s", len(secret), w, got)
				}
				assertNoSecretRun(t, got, secret)

				var out bytes.Buffer
				r := NewRedactor(&out, []string{secret})
				for b := []byte(text); len(b) > 0; {
					n := min(7, len(b))
					if _, err := r.Write(b[:n]); err != nil {
						t.Fatal(err)
					}
					b = b[n:]
				}
				if err := r.Flush(); err != nil {
					t.Fatal(err)
				}
				assertNoSecretRun(t, out.String(), secret)
			}
		}
	}
}

func TestBuildEnvPassesNetworkAndImageVariables(t *testing.T) {
	pass := []string{
		"HTTPS_PROXY=http://proxy.invalid:3128", "https_proxy=http://proxy.invalid:3128", "NO_PROXY=localhost",
		"NODE_EXTRA_CA_CERTS=/etc/ssl/extra.pem", "SSL_CERT_FILE=/etc/ssl/extra.pem", "GIT_SSL_CAINFO=/etc/ssl/extra.pem",
		"DISABLE_AUTOUPDATER=1", "COREPACK_ENABLE_DOWNLOAD_PROMPT=0", "PLAYWRIGHT_BROWSERS_PATH=/ms-playwright",
	}
	parent := append([]string{"ANTHROPIC_API_KEY=key-123", "VERTEX_REGION_CLAUDE_4_5_SONNET=europe-west1"}, pass...)
	env, _, err := BuildEnv(parent, EnvSpec{Auth: "api-key"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range pass {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %v", want, env)
		}
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "VERTEX_REGION_") }) {
		t.Errorf("a Vertex region override passed without auth: vertex: %v", env)
	}
}

func TestBuildEnvVertexExtras(t *testing.T) {
	parent := []string{
		"CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=p",
		"ANTHROPIC_VERTEX_BASE_URL=https://gateway.invalid/v1", "VERTEX_REGION_CLAUDE_4_5_SONNET=europe-west1",
		"VERTEXISH=no",
	}
	env, _, err := BuildEnv(parent, EnvSpec{Auth: "vertex"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range parent[:4] {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %v", want, env)
		}
	}
	if slices.Contains(env, "VERTEXISH=no") {
		t.Errorf("an unrelated variable passed: %v", env)
	}
}

func TestNewSessionID(t *testing.T) {
	id := NewSessionID()
	if len(id) != 36 || id[14] != '4' || id == NewSessionID() {
		t.Fatalf("bad UUIDv4 %q", id)
	}
}
