package cli

import (
	"context"
	"io"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// Review Focus 2: one repository file and one layer give one resolved
// config, whichever consumer resolves it. The runner leg calls
// runner.ResolveConfig, the exported core of (*run).resolveConfig
// (internal/runner/runner.go), not a hand-rolled config.Resolve call: a
// runner-side divergence (TestRunnerRecordsTheLayer, Task 7, pins the
// same function from inside the package) would otherwise go unnoticed
// here.
func TestEveryConsumerResolvesTheSameBytes(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	repoYAML := minimalAnchored + "agent:\n  review_rounds: 3\n"
	dir := layerCheckout(t, f, repoYAML)
	env := fileEnv(t, f)
	ctx := context.Background()
	slug := mustSlug("github", "acme/other")
	if err := writeLayerCopy(ctx, env.bucket, slug, []byte(testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	sums := map[string]string{}

	rf, err := resolveFugaroYAML(ctx, []byte(repoYAML), env.lc, layerOptions{})
	if err != nil || len(rf.Problems) > 0 {
		t.Fatal(err, rf.Problems)
	}
	sums["cli (validate, run, config show)"] = rf.Res.ConfigSHA256

	spec := &task.Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/other", Ref: "main", Task: "x"}
	if _, err := embedProjectLayer(ctx, env, spec, io.Discard); err != nil || spec.ProjectLayer == nil {
		t.Fatal(err)
	}
	rc := runner.ResolveConfig([]byte(repoYAML), spec.ProjectLayer, env.lc.Name)
	if rc.LayerErr != nil || len(rc.Problems) > 0 || !rc.Applied {
		t.Fatal(rc.LayerErr, rc.Problems, rc.Applied)
	}
	sums["runner (the task's embedded text)"] = rc.Res.ConfigSHA256

	data, err := readLayerCopy(ctx, f.bucket, slug, config.LayerSum([]byte(testProjectLayer)))
	if err != nil {
		t.Fatal(err)
	}
	_, rr, err := loadCheckoutResolved(ctx, dir, nil, layerOptions{Data: data, Where: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	sums["cloud build render (the copy)"] = rr.Res.ConfigSHA256

	head, err := jobHeadConfig(ctx, env.bucket, slug, cloneOf(t, map[string]string{"fugaro.yaml": repoYAML}), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	sums["check job (the copy, over head)"] = head.SHA256()

	want := sums["cli (validate, run, config show)"]
	for who, got := range sums {
		if got != want {
			t.Errorf("%s resolved %s, the CLI %s", who, got, want)
		}
	}
}
