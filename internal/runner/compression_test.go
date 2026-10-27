package runner_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// compressionHarness is a gateway-on harness with agent.compression on. The
// fixture's fugaro.yaml never carries a compression: key -- config.Validate
// refuses one while the key is reserved (P11) -- so Deps.CompressionForTest
// is what turns it on, in memory, after Validate has accepted the file. The
// sidecar is the harness's fake, so no test spawns a real process.
func compressionHarness(t *testing.T) *harness {
	t.Helper()
	h := newGW(t, gwConfig(t, ""), "observe", "", anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 1})).harness
	h.deps.CompressionForTest = "headroom"
	return h
}

func TestRunnerStartsNoSidecarByDefault(t *testing.T) {
	h := newGW(t, gwConfig(t, ""), "observe", "").harness
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if h.compressionStarts != 0 {
		t.Fatalf("a sidecar was started %d times with agent.compression unset", h.compressionStarts)
	}
}

// TestStartCompressionRefusesWithoutGateway is P9's runtime half: the
// gateway requirement can live in no config layer, because the effective
// budget mode is the tightest of the ceiling, the default branch's file and
// the run's own, and only the runner's bootstrap sees all three. The
// harness is the gateway-less one -- no ceiling, no default-branch budget --
// so the run stops before any stage.
func TestStartCompressionRefusesWithoutGateway(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.CompressionForTest = "headroom"
	rec, err := h.run(t, implement("feature"))
	if err == nil {
		t.Fatal("agent.compression without the budget gateway did not fail the run")
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone || !strings.Contains(rec.Reason, "budget gateway") {
		t.Fatalf("record = %+v, want infra_error naming the budget gateway", rec)
	}
	if h.compressionStarts != 0 {
		t.Fatal("a sidecar was started despite the gateway being off")
	}
	if len(h.agent.calls) != 0 {
		t.Fatal("a stage ran")
	}
}

// TestRunnerBuildsANarrowSidecarEnv is this task's headline security test:
// the sidecar's environment is built from the gateway's loopback URL and
// nothing else (P7). What it must not carry is the run's credentials -- the
// gateway's own per-run token, the real provider key, the git token -- and
// it must not inherit the runner's environment, which holds all three.
//
// The upstream entry is proved to be the gateway's URL by calling through
// it: only the gateway forwards to the fake Anthropic upstream, and the
// gateway's token is the one Claude Code itself presents.
//
// Hand mutation this fails on: building the sidecar's Env from r.d.Env
// (append(append([]string{}, r.d.Env...), upstream)), the inheritance P7
// exists to refuse -- the planted key and the runner's ANTHROPIC_API_KEY
// entry are then in the sidecar's environment. Mutation (dropping the
// upstream entry): nothing answers the call below.
func TestRunnerBuildsANarrowSidecarEnv(t *testing.T) {
	h := compressionHarness(t)
	probed := false
	inspect := func(inner step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			gwToken := envValue(req.Env, "ANTHROPIC_API_KEY")
			if gwToken == "" {
				t.Fatal("the run has no gateway token, so this test proves nothing about leaking it")
			}
			if got := envValue(req.Env, "ANTHROPIC_BASE_URL"); got != sidecarURL {
				t.Fatalf("Claude Code is pointed at %q, want the sidecar %q", got, sidecarURL)
			}
			if len(h.lastCompressionEnv) != 1 {
				t.Fatalf("the sidecar's env = %q, want the upstream variable alone", h.lastCompressionEnv)
			}
			entry := h.lastCompressionEnv[0]
			for _, leaked := range []string{gwToken, plantedKey, "GH_TOKEN", "ANTHROPIC_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS", "FIXTURE_FAILS_FILE"} {
				if strings.Contains(entry, leaked) {
					t.Fatalf("the sidecar's env carries %q: %s", leaked, entry)
				}
			}
			name, upstream, ok := strings.Cut(entry, "=")
			if !ok || name == "" || upstream == "" {
				t.Fatalf("the sidecar's env entry is not a variable: %q", entry)
			}
			if !isTheGateway(t, upstream, gwToken) {
				t.Fatalf("the sidecar's upstream %q (%s) does not answer as the run's gateway", upstream, name)
			}
			probed = true
			return inner(t, ctx, req)
		}
	}
	if _, err := h.run(t, inspect(implement("feature")), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !probed {
		t.Fatal("no stage ran")
	}
}

// isTheGateway posts one Messages call to url with key, as Claude Code
// would, and reports whether the gateway forwarded it: the fake upstream's
// own answer comes back, which nothing but the gateway can produce.
func isTheGateway(t *testing.T, url, key string) bool {
	t.Helper()
	status, body := postBody(t, url+"/v1/messages", key,
		fmt.Sprintf(`{"model":%q,"max_tokens":4096,"messages":[{"role":"user","content":"hello"}]}`, sonnet))
	return status == http.StatusOK && strings.Contains(body, "msg_fake")
}

// TestRunnerFailsStageOnSidecarDeath: a sidecar that dies under a stage
// fails that stage, the way an ordinary stage failure does -- Run returns a
// nil error and the record is failed/draft (TestStageTimeoutOpensDraft's own
// shape) -- and never a silent fallthrough to the gateway or the API. The
// step blocks, as that test's does, so the death lands while the agent is
// still running rather than after it returned.
func TestRunnerFailsStageOnSidecarDeath(t *testing.T) {
	h := compressionHarness(t)
	h.killSidecarDuring("implement")
	rec, err := h.run(t, blockUntilDone)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft || !strings.Contains(rec.Reason, "Headroom sidecar") {
		t.Fatalf("a sidecar death mid-stage = %s/%s (%q), want failed/draft naming the sidecar", rec.Status, rec.Outcome, rec.Reason)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

// TestRunnerFailsStageOnSidecarHang is the death test's twin: the fake's
// Dead() channel closes because the sidecar stopped answering rather than
// because it exited, and the runner must treat the two identically (P8,
// P10) -- there is no second code path, only a second way of causing the
// one the runner watches.
func TestRunnerFailsStageOnSidecarHang(t *testing.T) {
	h := compressionHarness(t)
	h.hangSidecarDuring("implement")
	rec, err := h.run(t, blockUntilDone)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft || !strings.Contains(rec.Reason, "Headroom sidecar") {
		t.Fatalf("a sidecar hang mid-stage = %s/%s (%q), want failed/draft naming the sidecar", rec.Status, rec.Outcome, rec.Reason)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

// TestRunnerStopsSidecarOnRunEnd: the sidecar is stopped exactly once, on
// the same path the gateway is closed by, whatever became of the run.
func TestRunnerStopsSidecarOnRunEnd(t *testing.T) {
	h := compressionHarness(t)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if h.compressionStops != 1 {
		t.Fatalf("the sidecar was stopped %d times, want exactly 1", h.compressionStops)
	}
}
