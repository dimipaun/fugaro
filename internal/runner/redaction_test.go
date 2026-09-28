package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
)

// objectsContaining lists the bucket objects under the run's prefix whose
// content contains needle.
func objectsContaining(t *testing.T, h *harness, needle string) []string {
	t.Helper()
	ctx := context.Background()
	it := h.bucket.List(nil)
	var out []string
	for obj, err := it.Next(ctx); err == nil; obj, err = it.Next(ctx) {
		data, err := h.bucket.ReadAll(ctx, obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), needle) {
			out = append(out, obj.Key)
		}
	}
	return out
}

// TestVerifyRecordsInResultAreRedacted pins S-M3: failed test names come
// from reports the agent can write, so a secret in one is redacted in
// result.json and verify/<n>.json.
func TestVerifyRecordsInResultAreRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	// The model key happens to be part of a failing test's name.
	const secret = "Suite.beta"
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+secret)
	h.fails(t, "beta")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Verify) == 0 || len(rec.Verify[0].Failed) == 0 {
		t.Fatalf("no failed tests recorded: %+v", rec.Verify)
	}
	if keys := objectsContaining(t, h, secret); len(keys) > 0 {
		t.Fatalf("the secret is stored unredacted in %v", keys)
	}
}

// TestMountedSecretsAreRedactedFromTheStart pins S-M2: every secret the
// job mounts is named in FUGARO_SECRET_ENVS and redacted, whether or not
// the fugaro.yaml at the task's ref declares it.
func TestMountedSecretsAreRedactedFromTheStart(t *testing.T) {
	h := newHarness(t, "", nil)
	const mounted = "mounted-but-undeclared-value"
	h.deps.Env = append(h.deps.Env, "RENAMED_TOKEN="+mounted, runner.SecretEnvsVar+"=RENAMED_TOKEN,,UNSET_TOKEN")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		// The agent reads it from the runner's /proc/<pid>/environ.
		_, _ = req.Transcript.Write([]byte(`{"type":"assistant","text":"` + mounted + `"}` + "\n"))
		_, _ = req.Stderr.Write([]byte(mounted + "\n"))
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if keys := objectsContaining(t, h, mounted); len(keys) > 0 {
		t.Fatalf("the mounted secret is stored unredacted in %v", keys)
	}
}
