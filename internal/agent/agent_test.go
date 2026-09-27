package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
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

func TestClaudeRunNoResult(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"no_result":true,"exit":1}]}`)
	_, err := Claude{Bin: bin}.Run(context.Background(), Request{SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}})
	if err == nil || !strings.Contains(err.Error(), "without a result event") {
		t.Fatalf("err = %v", err)
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
	if _, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=k"}, EnvSpec{Auth: "api-key", Secrets: []string{"MISSING"}}); err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("missing secret err = %v", err)
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

func TestNewSessionID(t *testing.T) {
	id := NewSessionID()
	if len(id) != 36 || id[14] != '4' || id == NewSessionID() {
		t.Fatalf("bad UUIDv4 %q", id)
	}
}
