package runner_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// allModes is every string a job's FUGARO_BUDGET_MODE can carry into a run:
// the two the vocabulary keeps, unset, and a string no door accepts — the word
// that used to be a mode — which a hand edit may still leave. A test that
// varies the mode sweeps this.
var allModes = []string{"", "off", policy.ModeObserve, policy.ModeEnforce}

// TestTheGatewayNoLongerReadsTheMode (AB1b, D3009, F15): there is always a
// budget, so whether a run has a gateway is a fact about its credential and
// nothing else. I1 dropped GatewayOn's mode argument with the constant it read,
// and a run whose job names no mode at all — the state the suite's own harness
// is in — starts its gateway like any other.
//
// Red step: with an unset mode (and with the word off) GatewayOn answered
// false, so the run had no gateway and agent.BuildEnv's api-key arm mounted the
// job's real key in the agent's environment. Both halves below are about that:
// the predicate answers true for every credential the gateway can carry, and
// the stage's environment carries the gateway's per-run token where the key
// used to be.
//
// Hand mutation this fails on: restoring the mode as a condition
// (`auth == "api-key" && mode != "" && mode != "off"`), which leaves the
// harness's own runs — and every job of an installation whose mode is unset —
// keyless and gateway-less again.
func TestTheGatewayNoLongerReadsTheMode(t *testing.T) {
	for _, auth := range []string{"api-key", "vertex", config.AuthCascade} {
		if !runner.GatewayOn(auth) {
			t.Errorf("GatewayOn(%q) = false: the mode is not a fact of the gateway any more (D3009)", auth)
		}
	}
	// The credential's own arm is untouched: a credential the gateway cannot
	// carry has no gateway, which is what keeps an oauth run the one that
	// reaches Anthropic with a token of its own (D3026, R12).
	if runner.GatewayOn("oauth") {
		t.Error(`GatewayOn("oauth") = true: oauth is not a credential the gateway carries`)
	}
	for _, mode := range allModes {
		// An enforcing run without a cap halts at bootstrap (HaltNoCap), which
		// is a different rule than this one: the cap is what keeps that from
		// standing in for the gateway's own question.
		capUSD := ""
		if mode == policy.ModeEnforce {
			capUSD = "5"
		}
		g := newGW(t, gwConfig(t, ""), mode, capUSD, anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 1}))
		probed := false
		inspect := func(inner step) step {
			return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				probed = true
				base, key := gwEnv(req)
				if base == "" {
					t.Fatalf("mode %q: the stage's calls go nowhere: no gateway was started", mode)
				}
				if key == "" || key == plantedKey {
					t.Fatalf("mode %q: the stage presents %q, want the gateway's per-run token (ANTHROPIC_BASE_URL=%s)", mode, key, base)
				}
				return inner(t, ctx, req)
			}
		}
		if _, err := g.run(t, inspect(implement("feature")), review("ship", 0)); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if !probed {
			t.Fatalf("mode %q: no stage ran", mode)
		}
	}
}

// TestALocalRunNeedsNoManagedSettingsDirectory (AB1b, D149, the review finding
// on #3481): Claude Code's managed settings are the image's and so is the
// directory they live in. `fugaro exec --task-file` is the CLI's one local
// invocation (execPlatform, D149) and it runs on the developer's own machine,
// which may hold no /etc/claude-code at all and cannot point --managed-settings
// elsewhere while the budget is on (checkTestHooks). So the gateway's
// precondition and the file it writes are the cloud's: a local run goes on
// without either, its stages reach the gateway through the environment the
// runner hands them, and it says so once.
//
// Red step: with the precondition applied to a local run, this run stops at
// bootstrap — "the image can't hold Claude Code's managed settings (<dir>: no
// such file or directory); rebuild it on an M9a base image" — a remedy no host
// machine has.
//
// Hand mutation this fails on: dropping the Local arm from
// checkManagedDirWritable (the bootstrap refusal returns), or turning the
// tolerated write back into a refusal (the run ends at its first stage).
func TestALocalRunNeedsNoManagedSettingsDirectory(t *testing.T) {
	g := newGW(t, pinnedConfig(t), "", "", anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 1}))
	h := g.harness
	if !h.deps.Local {
		t.Fatal("the harness is not a local run: this test is about the local invocation")
	}
	// The developer's machine, which holds no managed settings directory.
	h.deps.ManagedSettingsPath = filepath.Join(t.TempDir(), "absent", "managed-settings.json")
	var logs bytes.Buffer
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	stages := 0
	inspect := func(inner step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			base, key := gwEnv(req)
			if !strings.HasPrefix(base, "http://127.0.0.1:") {
				t.Errorf("stage %d: the agent's calls go nowhere: ANTHROPIC_BASE_URL = %q", stages, base)
			}
			// The gateway's per-run token, never the job's own key: that the
			// key stays out of the agent's environment is the whole point.
			if key == "" || key == "test-key" {
				t.Errorf("stage %d: ANTHROPIC_API_KEY = %q, want the gateway's per-run token", stages, key)
			}
			stages++
			return inner(t, ctx, req)
		}
	}
	rec, err := h.run(t, inspect(implement("feature")), inspect(review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if stages != 2 {
		t.Fatalf("%d stages ran, want 2", stages)
	}
	if _, err := os.Stat(filepath.Dir(h.deps.ManagedSettingsPath)); !os.IsNotExist(err) {
		t.Errorf("the host's managed settings directory was touched: %v", err)
	}
	if n := strings.Count(logs.String(), "managed settings could not be written"); n != 1 {
		t.Errorf("%d lines about the managed settings, want 1:\n%s", n, logs.String())
	}
}

// TestALocalReviewRunNeedsTheManagedSettingsDirectory (AB1b, D894(a), the
// review finding on #3481): the write a local run tolerates a failure of is the
// gateway's own — its variables and the pins, which the stage's environment
// carries too — and never the posture a review stage is launched under. That
// posture lives in the managed settings file and nowhere else: no environment
// variable carries it, so a machine with no directory to hold it must stop the
// stage rather than let it read a pull request's own head with that head's
// hooks and project MCP server enabled (D894(a), the reasons in
// WriteReviewManagedSettings).
//
// Red step: with the tolerance left as `if r.d.Local`, the review stage runs
// with no posture written and the scripted agent is reached.
//
// Hand mutation this fails on: dropping `&& !posture` from the tolerated write
// in writeManagedSettings.
func TestALocalReviewRunNeedsTheManagedSettingsDirectory(t *testing.T) {
	h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	pushReviewBranch(t, h, "someone-elses-change", func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	addPR(h, 7, "someone-elses-change", "")
	if !h.deps.Local {
		t.Fatal("the harness is not a local run: this test is about the local invocation")
	}
	// The developer's machine, which holds no managed settings directory.
	h.deps.ManagedSettingsPath = filepath.Join(t.TempDir(), "absent", "managed-settings.json")
	rec, err := h.run(t, review("changes", 1))
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "writing Claude Code's managed settings") {
		t.Fatalf("record = %+v, err = %v: the review stage did not stop at the posture it could not write", rec, err)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("%d agent calls: a stage ran without the posture it must be launched under", len(h.agent.calls))
	}
}

// TestALocalRunStopsAtAManagedSettingsFileItCannotReplace (AB1b, D149, the
// review finding on #3481): the write a local run tolerates a failure of is one
// that found nothing to replace. A managed settings file already at the path is
// not this run's — an organisation's own policy, say — and the managed level
// outranks the stage's environment, so a stage left reading it would call
// wherever it says: this run's ledger would read nothing, the enforce cap would
// bind nothing and the pins would be ignored, while the warning said the stages
// still reached the gateway. Nothing the run hands a stage can outrank that
// file, so the run stops.
//
// Red step: with the tolerance unconditional, this run reaches its stages and
// ends succeeded with the organisation's file still in place.
//
// Hand mutation this fails on: dropping the Lstat check from the tolerated
// write in writeManagedSettings.
func TestALocalRunStopsAtAManagedSettingsFileItCannotReplace(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a root test user writes into a read-only directory")
	}
	g := newGW(t, pinnedConfig(t), "", "", anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 1}))
	h := g.harness
	dir := filepath.Join(t.TempDir(), "claude-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "managed-settings.json")
	corp := "{\"env\":{\"ANTHROPIC_BASE_URL\":\"https://corp-proxy.invalid\"}}\n"
	if err := os.WriteFile(path, []byte(corp), 0o644); err != nil {
		t.Fatal(err)
	}
	// The organisation's file, in a directory the developer may not write.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	h.deps.ManagedSettingsPath = path
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "writing Claude Code's managed settings") {
		t.Fatalf("record = %+v, err = %v: the run went on over a file that routes its stages away from the gateway", rec, err)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("%d agent calls: a stage ran on the file's routing, not the gateway's", len(h.agent.calls))
	}
	if b, rerr := os.ReadFile(path); rerr != nil || string(b) != corp {
		t.Errorf("the managed settings file that was already there was touched: %q, %v", b, rerr)
	}
}

// TestALocalRunStopsAtAWritableManagedSettingsFileItDidNotWrite (AB1b, D149,
// the review finding on #3481): what a local run may not do is take a file it
// did not write, and a writable directory is the case where it *can*: the same
// organisation's managed-settings.json is replaced by this run's copy and then
// deleted with it at the end of the run, while the stages read the gateway's
// variables either way — so the file was there, was somebody else's, and is
// gone, with nothing said. The refusal is therefore made before the write, on
// the file rather than on the failure, and the run stops with the file as it
// was.
//
// Red step: with the check only in the write's error branch (the shape this
// finding corrects), this run ends succeeded, no line is logged, and the path
// is empty at the end: replaced, then removed.
func TestALocalRunStopsAtAWritableManagedSettingsFileItDidNotWrite(t *testing.T) {
	g := newGW(t, pinnedConfig(t), "", "", anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 1}))
	h := g.harness
	// Writable on purpose: the run could take the file, and that is the case.
	path := filepath.Join(t.TempDir(), "managed-settings.json")
	corp := "{\"env\":{\"ANTHROPIC_BASE_URL\":\"https://corp-proxy.invalid\"}}\n"
	if err := os.WriteFile(path, []byte(corp), 0o644); err != nil {
		t.Fatal(err)
	}
	h.deps.ManagedSettingsPath = path
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "already there and this run did not write it") {
		t.Fatalf("record = %+v, err = %v: the run went on over a managed settings file it did not write", rec, err)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("%d agent calls: a stage ran on a file this run did not write", len(h.agent.calls))
	}
	if b, rerr := os.ReadFile(path); rerr != nil || string(b) != corp {
		t.Errorf("the managed settings file that was already there was replaced: %q, %v", b, rerr)
	}
}

// TestTheVertexRefusalNamesObserveOnly (AB1b, F11): the mode off is gone from
// the vocabulary (D3009), so the remedy a Vertex run is given names the one
// mode such a run can hold. The text is pinned here as well as in the CLI,
// whose validate and init print it: a reader who sees it is told the same
// thing wherever they see it.
func TestTheVertexRefusalNamesObserveOnly(t *testing.T) {
	if !strings.Contains(runner.VertexBudgetRefusal, "budget.mode observe") {
		t.Errorf("VertexBudgetRefusal does not name observe: %q", runner.VertexBudgetRefusal)
	}
	// The old word of the mode is not a remedy any more: a reader sent to it
	// would be sent to a value the configuration refuses.
	if strings.Contains(runner.VertexBudgetRefusal, "off") {
		t.Errorf("VertexBudgetRefusal still names the removed mode: %q", runner.VertexBudgetRefusal)
	}
}
