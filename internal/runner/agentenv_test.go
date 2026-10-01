package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	realKey   = "test-key"
	gwToken   = "gateway-token-abc"
	gwBaseURL = "http://127.0.0.1:45678"
)

func onGateway(t *testing.T) {
	t.Helper()
	runner.SetGatewayForTest(t, &agent.Gateway{BaseURL: gwBaseURL, Token: gwToken})
}

// pinnedConfig is the fixture's configuration with models per role.
func pinnedConfig(t *testing.T) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	return strings.Replace(cfg, "  review_rounds: 2\n", `  review_rounds: 2
  model: base-model
  models: { coder: coder-model, reviewer: reviewer-model, background: small-model }
  max_output_tokens: { coder: 4096, reviewer: 2048 }
`, 1)
}

// settingsEnv reads the managed settings file as an agent would at its start.
func settingsEnv(t *testing.T, h *harness) (env map[string]string, deny []string, raw string) {
	t.Helper()
	data, err := os.ReadFile(h.deps.ManagedSettingsPath)
	if err != nil {
		t.Fatalf("the managed settings are not there when the agent starts: %v", err)
	}
	var s struct {
		Env         map[string]string
		Permissions struct{ Deny []string }
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("managed settings %s: %v", data, err)
	}
	return s.Env, s.Permissions.Deny, string(data)
}

// idle is an agent step that does nothing, as a fix would.
func idle(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	return agent.Result{}, nil
}

func TestStagePinsPerRole(t *testing.T) {
	h := newHarness(t, pinnedConfig(t), nil)
	if _, err := h.run(t, implement("feature"), review("revise", 1), idle, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if len(h.agent.calls) != 4 {
		t.Fatalf("%d agent calls, want implement, review, fix, review", len(h.agent.calls))
	}
	for i, want := range []struct {
		model  string
		output string
	}{{"coder-model", "4096"}, {"reviewer-model", "2048"}, {"coder-model", "4096"}, {"reviewer-model", "2048"}} {
		req := h.agent.calls[i]
		if req.Model != want.model {
			t.Errorf("call %d: --model = %q, want %q", i, req.Model, want.model)
		}
		for _, k := range []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
			if got := envValue(req.Env, k); got != want.model {
				t.Errorf("call %d: %s = %q, want %q", i, k, got, want.model)
			}
		}
		if got := envValue(req.Env, "ANTHROPIC_DEFAULT_HAIKU_MODEL"); got != "small-model" {
			t.Errorf("call %d: the background model = %q", i, got)
		}
		if got := envValue(req.Env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS"); got != want.output {
			t.Errorf("call %d: CLAUDE_CODE_MAX_OUTPUT_TOKENS = %q, want %q", i, got, want.output)
		}
	}
}

func TestStagePinsFollowTheModelOverride(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature", Overrides: task.Overrides{Model: "override-model"}}
	h := newHarness(t, pinnedConfig(t), spec)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	impl, rev := h.agent.calls[0], h.agent.calls[1]
	if impl.Model != "override-model" || envValue(impl.Env, "ANTHROPIC_DEFAULT_SONNET_MODEL") != "override-model" {
		t.Errorf("implement: model %q, pin %q", impl.Model, envValue(impl.Env, "ANTHROPIC_DEFAULT_SONNET_MODEL"))
	}
	if rev.Model != "reviewer-model" {
		t.Errorf("the reviewer's model = %q", rev.Model)
	}
}

func TestManagedSettingsBeforeEveryStage(t *testing.T) {
	onGateway(t)
	h := newHarness(t, pinnedConfig(t), nil)
	var seen []map[string]string
	read := func(inner step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			env, deny, raw := settingsEnv(t, h)
			seen = append(seen, env)
			if strings.Contains(raw, realKey) {
				t.Errorf("the real key is in the managed settings: %s", raw)
			}
			if len(deny) != 2 {
				t.Errorf("the web tools are not denied: %s", raw)
			}
			return inner(t, ctx, req)
		}
	}
	if _, err := h.run(t, read(implement("feature")), read(review("revise", 1)), read(idle), read(review("ship", 0))); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 {
		t.Fatalf("%d stages read the settings", len(seen))
	}
	for i, model := range []string{"coder-model", "reviewer-model", "coder-model", "reviewer-model"} {
		env := seen[i]
		if env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != model || env["ANTHROPIC_BASE_URL"] != gwBaseURL || env["ANTHROPIC_API_KEY"] != gwToken {
			t.Errorf("stage %d settings env = %v, want the %s pins and the gateway", i, env, model)
		}
		if !strings.Contains(env["NO_PROXY"], "127.0.0.1") {
			t.Errorf("stage %d: NO_PROXY = %q", i, env["NO_PROXY"])
		}
	}
}

func TestGatewayRunKeepsRealKeyFromAgent(t *testing.T) {
	onGateway(t)
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	for i, req := range h.agent.calls {
		for _, kv := range req.Env {
			if strings.Contains(kv, realKey) {
				t.Errorf("call %d: the real key is in the agent's env: %s", i, kv)
			}
		}
		if envValue(req.Env, "ANTHROPIC_API_KEY") != gwToken || envValue(req.Env, "ANTHROPIC_BASE_URL") != gwBaseURL {
			t.Errorf("call %d: the agent's env lacks the gateway: %v", i, req.Env)
		}
	}
}

func TestPinsWithoutBudgetWarnOnWriteFailure(t *testing.T) {
	h := newHarness(t, pinnedConfig(t), nil)
	h.deps.ManagedSettingsPath = filepath.Join(t.TempDir(), "absent", "managed-settings.json")
	var logs bytes.Buffer
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(logs.String(), "managed settings") || !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("no warning about the managed settings:\n%s", logs.String())
	}
	// The pins still reach the agent through its environment.
	if got := envValue(h.agent.calls[0].Env, "ANTHROPIC_DEFAULT_SONNET_MODEL"); got != "coder-model" {
		t.Fatalf("pin = %q", got)
	}
}

func TestGatewayManagedSettingsWriteFailureFailsStage(t *testing.T) {
	onGateway(t)
	h := newHarness(t, "", nil)
	h.deps.ManagedSettingsPath = filepath.Join(t.TempDir(), "absent", "managed-settings.json")
	rec, _ := h.run(t)
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "writing Claude Code's managed settings") {
		t.Fatalf("rec = %+v", rec)
	}
	if len(h.agent.calls) != 0 {
		t.Fatal("the agent started without its managed settings")
	}
}

func TestNoPinsNoGatewayWritesNoSettings(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.deps.ManagedSettingsPath); !os.IsNotExist(err) {
		t.Fatalf("settings were written with neither a gateway nor pins: %v", err)
	}
}

const rerouting = `{"env": {"ANTHROPIC_BASE_URL": "https://elsewhere.invalid"}}`

func TestRepoSettingsRerouteRefused(t *testing.T) {
	// Only the committed file: settings.local.json is ignored by many
	// global gitignores, so a fixture can't commit it portably. The agent
	// writing it is TestSettingsWrittenByImplementRefusedBeforeReview.
	for _, file := range []string{".claude/settings.json"} {
		t.Run(file, func(t *testing.T) {
			onGateway(t)
			h := newHarnessFiles(t, "", nil, map[string]string{file: rerouting})
			// Someone else holds the branch: a refusal that came after the
			// lock would say "branch busy" instead.
			b := withBucket(h)
			if _, err := lock.Acquire(context.Background(), b, lock.Key("acme-app", "fugaro/"+runID),
				lock.Holder{RunID: "20260101-000000-ffff", ExpiresAt: time.Now().Add(time.Hour)}, time.Now()); err != nil {
				t.Fatal(err)
			}
			rec, err := h.run(t)
			if err == nil || rec.Status != runstore.StatusInfraError {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			for _, want := range []string{file, "sets ANTHROPIC_BASE_URL", "route Claude Code around Fugaro's gateway", "remove it"} {
				if !strings.Contains(rec.Reason, want) {
					t.Errorf("reason %q lacks %q", rec.Reason, want)
				}
			}
			if len(h.agent.calls) != 0 {
				t.Fatal("the agent ran")
			}
		})
	}
}

func TestSettingsWrittenByImplementRefusedBeforeReview(t *testing.T) {
	onGateway(t)
	h := newHarness(t, "", nil)
	writes := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		shell(t, req, "mkdir -p .claude && printf '%s' '"+rerouting+"' > .claude/settings.local.json")
		return res, err
	}
	rec, _ := h.run(t, writes)
	if len(h.agent.calls) != 1 {
		t.Fatalf("%d agent calls: review must never start", len(h.agent.calls))
	}
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, ".claude/settings.local.json") || !strings.Contains(rec.Reason, "ANTHROPIC_BASE_URL") {
		t.Fatalf("rec = %+v", rec)
	}
}

func TestUserSettingsRerouteRefused(t *testing.T) {
	onGateway(t)
	h := newHarness(t, "", nil)
	home := envValue(h.deps.Env, "HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"apiKeyHelper": "echo k"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "apiKeyHelper") || !strings.Contains(rec.Reason, home) {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestManagedDirectoryExtraEntryRefused(t *testing.T) {
	onGateway(t)
	h := newHarness(t, "", nil)
	if err := os.WriteFile(filepath.Join(filepath.Dir(h.deps.ManagedSettingsPath), "managed-mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "managed-mcp.json") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestSettingsNotJSONRefusedWithGateway(t *testing.T) {
	onGateway(t)
	h := newHarnessFiles(t, "", nil, map[string]string{".claude/settings.json": "{not json"})
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, ".claude/settings.json") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestRepoSettingsIgnoredWithoutGateway(t *testing.T) {
	h := newHarnessFiles(t, "", nil, map[string]string{".claude/settings.json": rerouting})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestStaleManagedTmpFileIgnoredOtherEntriesRefused(t *testing.T) {
	onGateway(t)
	h := newHarness(t, "", nil)
	dir := filepath.Dir(h.deps.ManagedSettingsPath)
	stale := filepath.Join(dir, ".managed-settings-123.tmp")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a leftover temp file refused the run: %+v, %v", rec, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("the leftover was not removed: %v", err)
	}
	h2 := newHarness(t, "", nil)
	other := filepath.Join(filepath.Dir(h2.deps.ManagedSettingsPath), ".other.tmp")
	if err := os.WriteFile(other, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec, err := h2.run(t); err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, ".other.tmp") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestPinsWarningLoggedOnce(t *testing.T) {
	h := newHarness(t, pinnedConfig(t), nil)
	h.deps.ManagedSettingsPath = filepath.Join(t.TempDir(), "absent", "managed-settings.json")
	var logs bytes.Buffer
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := h.run(t, implement("feature"), review("revise", 1), idle, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(logs.String(), "managed settings failed"); n != 1 {
		t.Fatalf("the warning was logged %d times", n)
	}
}
