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

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestArgs(t *testing.T) {
	got := Args(Request{SessionID: "s1", AppendSystemPrompt: "rules", Model: "m", MaxBudgetUSD: 12.5, JSONSchema: `{}`})
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions",
		"--session-id", "s1", "--append-system-prompt", "rules", "--model", "m", "--max-budget-usd", "12.50", "--json-schema", "{}"}
	if !slices.Equal(got, want) {
		t.Fatalf("Args = %q\nwant   %q", got, want)
	}
	// A coding run's stage carries neither of the review stage's flags: its
	// argv is exactly what it was (D894(a)).
	for _, flag := range []string{"--setting-sources", "--strict-mcp-config"} {
		if slices.Contains(got, flag) {
			t.Errorf("a stage that asked for no posture carries %s: %q", flag, got)
		}
	}
	review := Args(Request{SessionID: "s1", SettingSources: ReviewSettingSources, StrictMCPConfig: true})
	wantReview := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions",
		"--session-id", "s1", "--setting-sources", "user", "--strict-mcp-config"}
	if !slices.Equal(review, wantReview) {
		t.Fatalf("review Args = %q\nwant         %q", review, wantReview)
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

// An OAuth run's agent environment is built from its own credential and
// nothing else. ANTHROPIC_API_KEY is in no allow-list for auth: oauth, so a
// key that is in the runner's own environment — a job that mounts one beside
// the token, a hand-started runner, a local run on a machine that has one —
// never reaches the agent, and the run cannot fall back to it. The token's
// absence is an error (TestBuildEnvAuthModes pins that text); this is the
// other half of the same rule, and it is the mechanism design
// project-named-oauth-tokens §4.4 row 10 names for "the run fails; it does
// not fall back".
//
// Mutation (run, restored): add ANTHROPIC_API_KEY to oauth's secretNames in
// BuildEnv. Both halves of this test fail: the agent authenticates as the
// key, and the run with no token is admitted.
func TestBuildEnvOAuthNeverFallsBackToTheAPIKey(t *testing.T) {
	const key = "placeholder-api-key"
	env, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=" + key, "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef"},
		EnvSpec{Auth: "oauth"})
	if err != nil {
		t.Fatalf("BuildEnv = %v", err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Errorf("an OAuth run's environment carries the Anthropic API key: %q", kv)
		}
	}
	if !slices.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef") {
		t.Errorf("the agent's environment lacks the token: %v", env)
	}
	// The other half: with the key in the environment and no token, the run
	// is refused rather than run as something its configuration did not
	// choose.
	if _, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=" + key}, EnvSpec{Auth: "oauth"}); err == nil ||
		!strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("a run with auth: oauth and no token was admitted with an API key in the environment: err = %v", err)
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
		// The VM backend's one addition to the allowlist since M9 (design
		// compute-backend.md §4.3), gated on the run's own ask and asserted
		// below rather than here: a run whose workflow asked for Docker
		// reaches the VM's daemon through this, and only such a run gets it.
	}
	parent := append([]string{"ANTHROPIC_API_KEY=key-123", "VERTEX_REGION_CLAUDE_4_5_SONNET=europe-west1",
		// The operator's own shell, or the job env of a Docker run: the
		// variable is in the runner's environment either way, and which run
		// this is is what decides whether it reaches the agent.
		"DOCKER_HOST=unix:///var/run/docker.sock"}, pass...)
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
	// DOCKER_HOST reaches the agent only for a run that asked for Docker,
	// and the ask is the one the runner passes in, not the presence of the
	// variable in its own environment: an operator with DOCKER_HOST exported
	// who hand-starts a runner, or a local run, must not hand the agent an
	// address to a daemon its container was never given a socket for (design
	// compute-backend.md §2.2, §4.3, §4.4 check 6).
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "DOCKER_HOST=") }) {
		t.Errorf("DOCKER_HOST passed to a run that did not ask for Docker: %v", env)
	}
	dockerEnv, _, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Docker: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(dockerEnv, "DOCKER_HOST=unix:///var/run/docker.sock") {
		t.Errorf("a run that asked for Docker lacks DOCKER_HOST: %v", dockerEnv)
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

func TestParseStreamUsage(t *testing.T) {
	f, err := os.Open("testdata/stream-success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, found, err := ParseStream(f, nil)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if want := (Usage{Input: 100, CacheCreation: 20, CacheRead: 300, Output: 40}); res.Usage != want {
		t.Fatalf("usage = %+v, want %+v", res.Usage, want)
	}
	if res.Usage.Total() != 460 {
		t.Fatalf("total = %d", res.Usage.Total())
	}
	if len(res.ModelUsage) != 2 {
		t.Fatalf("modelUsage = %+v", res.ModelUsage)
	}
	if got, want := res.ModelUsage["claude-haiku-x"], (Usage{Input: 10, Output: 10, CacheRead: 100}); got != want {
		t.Fatalf("haiku = %+v, want %+v", got, want)
	}
	if got, want := res.ModelUsage["claude-opus-x"], (Usage{Input: 100, Output: 30, CacheRead: 200, CacheCreation: 20}); got != want {
		t.Fatalf("opus = %+v, want %+v", got, want)
	}
}

func TestParseStreamNoUsage(t *testing.T) {
	res, found, err := ParseStream(strings.NewReader(`{"type":"result","subtype":"success","is_error":false}`+"\n"), nil)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if res.Usage != (Usage{}) || len(res.ModelUsage) != 0 || res.StageTokens() != 0 {
		t.Fatalf("usage = %+v / %+v", res.Usage, res.ModelUsage)
	}
}

func TestStageTokensTakesLarger(t *testing.T) {
	r := Result{Usage: Usage{Input: 10, Output: 5}}
	if got := r.StageTokens(); got != 15 {
		t.Fatalf("usage only = %d", got)
	}
	r.ModelUsage = map[string]Usage{"a": {Input: 10, Output: 5}, "b": {CacheRead: 100}}
	if got := r.StageTokens(); got != 115 {
		t.Fatalf("modelUsage larger = %d", got)
	}
	r.Usage = Usage{Input: 1000}
	if got := r.StageTokens(); got != 1000 {
		t.Fatalf("usage larger = %d", got)
	}
}

func TestBuildEnvGatewayDropsRealKey(t *testing.T) {
	const realKey = "placeholder-api-key"
	parent := []string{"PATH=/bin", "ANTHROPIC_API_KEY=" + realKey, "ANTHROPIC_BASE_URL=https://elsewhere.invalid", "NPM_TOKEN=npm-456"}
	gw := &Gateway{BaseURL: "http://127.0.0.1:4000", Token: "tok-abc"}
	env, secrets, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Gateway: gw, Secrets: []string{"NPM_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range env {
		if strings.Contains(kv, realKey) {
			t.Errorf("the real key is in the agent's env: %s", kv)
		}
	}
	for _, want := range []string{"ANTHROPIC_API_KEY=tok-abc", "ANTHROPIC_BASE_URL=http://127.0.0.1:4000", "NPM_TOKEN=npm-456"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %v", want, env)
		}
	}
	if !slices.Contains(secrets, realKey) {
		t.Errorf("the real key is not registered for redaction: %v", secrets)
	}
	// A declared secret of the same name must not bring the real key back.
	env, secrets, err = BuildEnv(parent, EnvSpec{Auth: "api-key", Gateway: gw, Secrets: []string{"ANTHROPIC_API_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.Contains(kv, realKey) }) || !slices.Contains(env, "ANTHROPIC_API_KEY=tok-abc") {
		t.Errorf("a declared ANTHROPIC_API_KEY put the real key back: %v", env)
	}
	if !slices.Contains(secrets, realKey) {
		t.Errorf("secrets = %v", secrets)
	}
	// No key at all is accepted: a run whose models all route through
	// providers has none, and the gateway refuses a call that needs
	// Anthropic (internal/gateway, missingAnthropicKey).
	env, secrets, err = BuildEnv([]string{"PATH=/bin"}, EnvSpec{Auth: "api-key", Gateway: gw})
	if err != nil {
		t.Fatalf("a run with no Anthropic key: %v", err)
	}
	if !slices.Contains(env, "ANTHROPIC_API_KEY=tok-abc") || len(secrets) != 0 {
		t.Errorf("env = %v, secrets = %v, want the gateway token and nothing to redact", env, secrets)
	}
	// A key that is there but too short is still refused: it cannot be
	// redacted safely.
	if _, _, err := BuildEnv([]string{"PATH=/bin", "ANTHROPIC_API_KEY=abc"}, EnvSpec{Auth: "api-key", Gateway: gw}); err == nil {
		t.Error("a 3-byte real key was accepted")
	}
	// Set and the parent can't override the routing.
	env, _, err = BuildEnv(parent, EnvSpec{Auth: "api-key", Gateway: gw, Set: map[string]string{"ANTHROPIC_BASE_URL": "https://set.invalid"}})
	if err != nil || !slices.Contains(env, "ANTHROPIC_BASE_URL=http://127.0.0.1:4000") {
		t.Errorf("env = %v, %v", env, err)
	}
}

func TestBuildEnvGatewayVertex(t *testing.T) {
	parent := []string{"CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=p", "ANTHROPIC_VERTEX_BASE_URL=https://elsewhere.invalid/v1"}
	env, _, err := BuildEnv(parent, EnvSpec{Auth: "vertex", Gateway: &Gateway{BaseURL: "http://127.0.0.1:4000/"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ANTHROPIC_VERTEX_BASE_URL=http://127.0.0.1:4000/v1", "CLAUDE_CODE_SKIP_VERTEX_AUTH=1", "CLAUDE_CODE_USE_VERTEX=1",
		"CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=p",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %v", want, env)
		}
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.Contains(kv, "elsewhere.invalid") }) {
		t.Errorf("the parent's base URL passed through: %v", env)
	}
}

func TestBuildEnvGatewayOAuthRefused(t *testing.T) {
	_, _, err := BuildEnv([]string{"CLAUDE_CODE_OAUTH_TOKEN=oauth-123"}, EnvSpec{Auth: "oauth", Gateway: &Gateway{BaseURL: "http://127.0.0.1:1", Token: "t"}})
	if err == nil || !strings.Contains(err.Error(), "oauth") {
		t.Fatalf("err = %v", err)
	}
	if _, err := GatewayVars("oauth", Gateway{BaseURL: "http://127.0.0.1:1", Token: "t"}, nil); err == nil {
		t.Fatal("GatewayVars accepted oauth")
	}
}

// TestBuildEnvUnchangedWithNoCompression pins the whole environment a
// gateway run gets today, byte for byte, against the parent and spec of
// TestBuildEnvGatewayDropsRealKey: a nil Compression adds no key and
// changes no value (P3's "nil: off, today's behaviour, unchanged").
func TestBuildEnvUnchangedWithNoCompression(t *testing.T) {
	parent := []string{"PATH=/bin", "ANTHROPIC_API_KEY=placeholder-api-key", "ANTHROPIC_BASE_URL=https://elsewhere.invalid", "NPM_TOKEN=npm-456"}
	env, secrets, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Gateway: &Gateway{BaseURL: "http://127.0.0.1:4000", Token: "tok-abc"}, Secrets: []string{"NPM_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ANTHROPIC_API_KEY=tok-abc", "ANTHROPIC_BASE_URL=http://127.0.0.1:4000",
		"NO_PROXY=127.0.0.1,localhost", "NPM_TOKEN=npm-456", "PATH=/bin", "no_proxy=127.0.0.1,localhost",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("env = %q\nwant   %q", env, want)
	}
	if !slices.Equal(secrets, []string{"placeholder-api-key", "npm-456"}) {
		t.Fatalf("secrets = %v", secrets)
	}
}

// Compression needs an auth that gives Claude Code a gateway token to present
// to the sidecar: oauth has none, and a vertex run routes by
// ANTHROPIC_VERTEX_BASE_URL -- a base-URL shape Compression never touches --
// so admitting it would silently skip the sidecar rather than refuse it, the
// opposite of failing closed (P3). Both specs carry a gateway, so the auth
// rule is the only refusal left: without it a vertex run would succeed
// outright and an oauth one would be refused with the gateway's own message.
func TestBuildEnvRefusesCompressionWithOAuth(t *testing.T) {
	parent := []string{"CLAUDE_CODE_OAUTH_TOKEN=oauth-token-for-tests-1234"}
	spec := EnvSpec{Auth: "oauth", Gateway: &Gateway{BaseURL: "http://127.0.0.1:9000", Token: "gateway-token-abcdef"}, Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:9100"}}
	if _, _, err := BuildEnv(parent, spec); err == nil || !strings.Contains(err.Error(), "agent.compression") || !strings.Contains(err.Error(), "auth: oauth") {
		t.Fatalf("oauth + compression: err = %v, want a refusal naming agent.compression and auth: oauth", err)
	}
}

func TestBuildEnvRefusesCompressionWithVertex(t *testing.T) {
	parent := []string{"CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=p"}
	spec := EnvSpec{Auth: "vertex", Gateway: &Gateway{BaseURL: "http://127.0.0.1:9000", Token: "gateway-token-abcdef"}, Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:9100"}}
	if _, _, err := BuildEnv(parent, spec); err == nil || !strings.Contains(err.Error(), "agent.compression") || !strings.Contains(err.Error(), "auth: vertex") {
		t.Fatalf("vertex + compression: err = %v, want a refusal naming agent.compression and auth: vertex", err)
	}
}

// The sidecar holds no credential of its own: the gateway's per-run token is
// what Claude Code presents, through the sidecar, to the gateway. A run with
// no gateway would have to send the real key to the sidecar instead, so it is
// refused here as it is by config.Validate and the runner's own bootstrap
// (P9, defense in depth at the point closest to the credential).
func TestBuildEnvRefusesCompressionWithoutGateway(t *testing.T) {
	parent := []string{"ANTHROPIC_API_KEY=key-123"}
	if _, _, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:9100"}}); err == nil ||
		!strings.Contains(err.Error(), "agent.compression") {
		t.Fatalf("compression without a gateway: err = %v, want a refusal naming agent.compression", err)
	}
}

// TestBuildEnvRoutesThroughCompressionNotGateway: with compression on, Claude
// Code talks to the sidecar and never to the gateway directly, and none of the
// gateway's credentials reach the sidecar's address. The sidecar's URL carries
// no credential (CompressionProxy holds a BaseURL and nothing else), the
// gateway's address appears in no variable at all, the only variable holding
// the gateway's token is the api key Claude Code presents through the sidecar,
// and the real key stays registered for redaction and out of the environment.
//
// Mutation (put the compression step before maps.Copy(out, vars), as an
// earlier draft of the plan had it): the gateway's write clobbers it and the
// ANTHROPIC_BASE_URL assertion fails.
func TestBuildEnvRoutesThroughCompressionNotGateway(t *testing.T) {
	const realKey = "placeholder-api-key"
	gw := &Gateway{BaseURL: "http://127.0.0.1:9000", Token: "gateway-token-abcdef"}
	sidecar := &CompressionProxy{BaseURL: "http://127.0.0.1:9100"}
	env, secrets, err := BuildEnv([]string{"PATH=/bin", "ANTHROPIC_API_KEY=" + realKey}, EnvSpec{Auth: "api-key", Gateway: gw, Compression: sidecar})
	if err != nil {
		t.Fatalf("BuildEnv = %v", err)
	}
	if !slices.Contains(env, "ANTHROPIC_BASE_URL="+sidecar.BaseURL) {
		t.Fatalf("ANTHROPIC_BASE_URL does not point at the sidecar: %v", env)
	}
	for _, kv := range env {
		if strings.Contains(kv, gw.BaseURL) {
			t.Errorf("the gateway's address is in the agent's environment: %s", kv)
		}
		if strings.Contains(kv, realKey) {
			t.Errorf("the gateway's real key is in the agent's environment: %s", kv)
		}
		if strings.Contains(kv, gw.Token) && kv != "ANTHROPIC_API_KEY="+gw.Token {
			t.Errorf("the gateway's token reached the sidecar in another variable: %s", kv)
		}
	}
	if !slices.Contains(env, "ANTHROPIC_API_KEY="+gw.Token) {
		t.Errorf("Claude Code has no token to present through the sidecar: %v", env)
	}
	if !slices.Contains(secrets, realKey) {
		t.Errorf("the gateway's real key is not registered for redaction: %v", secrets)
	}
}

func TestBuildEnvNoGatewayUnchanged(t *testing.T) {
	env, secrets, err := BuildEnv([]string{"PATH=/bin", "ANTHROPIC_API_KEY=key-123"}, EnvSpec{Auth: "api-key"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "ANTHROPIC_API_KEY=key-123") || !slices.Equal(secrets, []string{"key-123"}) {
		t.Fatalf("env = %v, secrets = %v", env, secrets)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_BASE_URL") || strings.HasPrefix(kv, "NO_PROXY") || strings.HasPrefix(kv, "no_proxy") {
			t.Errorf("unexpected variable without a gateway: %s", kv)
		}
	}
}

func TestNoProxyCoversLoopback(t *testing.T) {
	gw := &Gateway{BaseURL: "http://127.0.0.1:4000", Token: "tok-abc"}
	env, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=key-123", "NO_PROXY=example.invalid"}, EnvSpec{Auth: "api-key", Gateway: gw})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NO_PROXY=example.invalid,127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %v", want, env)
		}
	}
	// Already covered: no duplicates.
	env, _, _ = BuildEnv([]string{"ANTHROPIC_API_KEY=key-123", "NO_PROXY=localhost,corp.invalid"}, EnvSpec{Auth: "api-key", Gateway: gw})
	if !slices.Contains(env, "NO_PROXY=localhost,corp.invalid,127.0.0.1") {
		t.Errorf("env = %v", env)
	}
}

// TestAgentEnvHasNoProviderKey: a provider's key is in the runner's
// environment for the gateway, and in no agent environment: not with the
// gateway, not without it, not on any auth, and not by declaring it.
func TestAgentEnvHasNoProviderKey(t *testing.T) {
	const (
		orKey = "sk-or-v1-not-a-real-key"
		other = "other-provider-key-xyz"
	)
	parent := []string{
		"PATH=/bin", "ANTHROPIC_API_KEY=key-123", "CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=p",
		config.ProviderKeyEnv("openrouter-api-key") + "=" + orKey, config.ProviderKeyEnv("other-key") + "=" + other,
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-tok-1",
	}
	gw := &Gateway{BaseURL: "http://127.0.0.1:4000", Token: "tok-abc"}
	for name, spec := range map[string]EnvSpec{
		"api-key":         {Auth: "api-key"},
		"api-key gateway": {Auth: "api-key", Gateway: gw},
		"vertex":          {Auth: "vertex"},
		"vertex gateway":  {Auth: "vertex", Gateway: gw},
		"oauth":           {Auth: "oauth"},
	} {
		env, secrets, err := BuildEnv(parent, spec)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, kv := range env {
			if strings.Contains(kv, orKey) || strings.Contains(kv, other) || config.IsProviderKeyEnv(strings.SplitN(kv, "=", 2)[0]) {
				t.Errorf("%s: a provider key is in the agent's env: %s", name, kv)
			}
		}
		if !slices.Contains(secrets, orKey) || !slices.Contains(secrets, other) {
			t.Errorf("%s: the provider keys are not registered for redaction: %v", name, secrets)
		}
	}
	// A workflow that declares the variable gets an error, not the key.
	_, _, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Secrets: []string{config.ProviderKeyEnv("openrouter-api-key")}})
	if err == nil || strings.Contains(err.Error(), orKey) {
		t.Errorf("declaring a provider key: err = %v", err)
	}
}

// TestClaudeArgsNeverSideloadPlugins pins the launch contract of design
// section 9 and plan V3: the headless run never loads, installs or trusts
// anything the repository's settings name (the Fugaro plugin is for people's
// interactive sessions). No plugin, marketplace, trust or settings-source
// flag is ever passed, whatever the request. If the real container proves
// that a project settings file does load the plugin, the runner adds a
// disable and this test changes with it (plan V3 fallback).
func TestClaudeArgsNeverSideloadPlugins(t *testing.T) {
	for name, req := range map[string]Request{
		"minimal": {SessionID: "s1"},
		"full":    {SessionID: "s1", AppendSystemPrompt: "rules", Model: "m", MaxBudgetUSD: 5, JSONSchema: `{}`},
		"resume":  {SessionID: "s1", Resume: true, Model: "m"},
	} {
		for _, a := range Args(req) {
			l := strings.ToLower(a)
			for _, bad := range []string{"plugin", "marketplace", "trust", "setting", "fugaro@fugaro", "extraknownmarketplaces"} {
				// The prompt-like values (system prompt, schema) are ours and
				// carry none of these words in the cases above.
				if strings.Contains(l, bad) {
					t.Errorf("%s: argument %q mentions %q", name, a, bad)
				}
			}
		}
	}
}

// TestClaudeEnvHasNoPluginOrTrustVariables: the scrubbed environment never
// carries a plugin or trust variable from the runner's environment, on any
// auth, and no variable that points Claude Code's config or plugin cache
// elsewhere than the run's own home.
func TestClaudeEnvHasNoPluginOrTrustVariables(t *testing.T) {
	parent := []string{
		"PATH=/bin", "HOME=/home/agent", "ANTHROPIC_API_KEY=key-123", "CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=p",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-tok-1",
		"CLAUDE_CODE_PLUGIN_SEED_DIR=/elsewhere", "CLAUDE_CODE_PLUGIN_CACHE_DIR=/elsewhere", "CLAUDE_CODE_PLUGIN_URL=https://evil.invalid",
		"CLAUDE_CONFIG_DIR=/elsewhere", "CLAUDE_PLUGIN_ROOT=/elsewhere", "CLAUDE_CODE_TRUST_ALL=1",
	}
	for _, auth := range []string{"api-key", "vertex", "oauth"} {
		env, _, err := BuildEnv(parent, EnvSpec{Auth: auth})
		if err != nil {
			t.Fatalf("%s: %v", auth, err)
		}
		for _, kv := range env {
			k := strings.SplitN(kv, "=", 2)[0]
			if strings.Contains(k, "PLUGIN") || strings.Contains(k, "TRUST") || k == "CLAUDE_CONFIG_DIR" {
				t.Errorf("%s: the agent's env has %s", auth, kv)
			}
		}
	}
}

// TestBuildEnvNoGatekeeperNeedsTheKeyActionably (design project-centric §6.1,
// review Medium 1): with no gateway every call goes direct to Anthropic, so
// the key is not optional whatever the pins are -- and the refusal names the
// fix, the same text the gateway refuses such a call with, instead of the
// bare "is not set in the runner environment".
func TestBuildEnvNoGatekeeperNeedsTheKeyActionably(t *testing.T) {
	_, _, err := BuildEnv([]string{"PATH=/bin"}, EnvSpec{Auth: "api-key"})
	if err == nil {
		t.Fatal("api-key with no gateway and no key started")
	}
	for _, want := range []string{"ANTHROPIC_API_KEY", "fugaro secrets set anthropic-api-key", "fugaro update", "route the model through a provider"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// A declared secret that is missing keeps the plain message: only the
	// Anthropic key has an action worth naming.
	if _, _, err := BuildEnv([]string{"PATH=/bin", "ANTHROPIC_API_KEY=key-123"}, EnvSpec{Auth: "api-key", Secrets: []string{"MISSING"}}); err == nil || strings.Contains(err.Error(), "fugaro secrets set anthropic-api-key") {
		t.Errorf("a missing declared secret err = %v, want the plain message", err)
	}
}

// TestCascadeAgentEnvHoldsNoCredential (C1.19): a cascade run's agent holds no
// model credential at all. Nothing of the job's own reaches it — not the
// Anthropic key the runner's environment may carry, not a subscription OAuth
// token — and what Claude Code presents is the gateway's own per-run token,
// which the credentials service (a different process, with the credential) is
// behind. D143: there is no credential in the job to fall back to.
func TestCascadeAgentEnvHoldsNoCredential(t *testing.T) {
	// Every value is built here, from this test's own name: no fixture in this
	// repository is a literal that looks like a key (Convention 2).
	realKey := "key-" + strings.ReplaceAll(t.Name(), "/", "-")
	oauthToken := "oauth-" + strings.ReplaceAll(t.Name(), "/", "-")
	gw := &Gateway{BaseURL: "http://127.0.0.1:4000", Token: "tok-" + strings.ReplaceAll(t.Name(), "/", "-")}
	parent := []string{
		"PATH=/bin", "ANTHROPIC_API_KEY=" + realKey,
		"CLAUDE_CODE_OAUTH_TOKEN=" + oauthToken,
		"CLAUDE_CODE_OAUTH_TOKEN_WORK=" + oauthToken,
	}
	env, secrets, err := BuildEnv(parent, EnvSpec{Auth: config.AuthCascade, Gateway: gw})
	if err != nil {
		t.Fatalf("a cascade run's agent env: %v", err)
	}
	for _, kv := range env {
		for _, leak := range []string{realKey, oauthToken} {
			if strings.Contains(kv, leak) {
				t.Errorf("a credential is in the cascade agent's env: %s", kv)
			}
		}
	}
	for _, want := range []string{"ANTHROPIC_API_KEY=" + gw.Token, "ANTHROPIC_BASE_URL=" + gw.BaseURL} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %v", want, env)
		}
	}
	// The real key is registered for redaction (a transcript never carries
	// it) and the gateway's token never is: it is this run's own, dead with
	// the run.
	if !slices.Contains(secrets, realKey) {
		t.Errorf("the job's own key is not registered for redaction: %v", secrets)
	}
	if slices.Contains(secrets, gw.Token) {
		t.Errorf("the gateway's per-run token is registered as a secret: %v", secrets)
	}
	// A declared mount of a model credential is refused rather than mounted:
	// the service holds it, and this job has no reason to (D143).
	for _, logical := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if _, _, err := BuildEnv(parent, EnvSpec{Auth: config.AuthCascade, Gateway: gw, Secrets: []string{logical}}); err == nil {
			t.Errorf("a cascade run mounted %s", logical)
		}
	}
	// A cascade run with no gateway is refused: there is nothing to carry its
	// calls and no credential of its own.
	if _, _, err := BuildEnv(parent, EnvSpec{Auth: config.AuthCascade}); err == nil {
		t.Error("a cascade run's agent env was built with no gateway")
	}
	// GatewayVars is the same answer where the managed settings file is written.
	vars, err := GatewayVars(config.AuthCascade, *gw, nil)
	if err != nil {
		t.Fatalf("GatewayVars for cascade: %v", err)
	}
	if vars["ANTHROPIC_API_KEY"] != gw.Token || vars["ANTHROPIC_BASE_URL"] != gw.BaseURL {
		t.Errorf("the managed settings route cascade to %v", vars)
	}
}
