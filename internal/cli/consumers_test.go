package cli

import (
	"context"
	"io"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// Review Focus 2: one repository file and one layer give one resolved
// config, whichever consumer resolves it. The runner is pinned to the
// task's path by TestRunnerRecordsTheLayer (Task 7), which compares its
// record with config.Resolve over the same embedded text.
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
	l, ps := config.ParseProjectLayer([]byte(spec.ProjectLayer.YAML), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	c, _, ps := config.Resolve([]byte(repoYAML), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	sums["runner (the task's embedded text)"] = c.SHA256()

	data, err := readLayerCopy(ctx, f.bucket, slug, config.LayerSum([]byte(testProjectLayer)))
	if err != nil {
		t.Fatal(err)
	}
	_, rr, err := loadCheckoutResolved(ctx, dir, nil, layerOptions{Data: data, Where: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	sums["cloud build render (the copy)"] = rr.Res.ConfigSHA256

	lo, err := jobLayerOptions(ctx, env.bucket, slug)
	if err != nil {
		t.Fatal(err)
	}
	head, err := readHeadConfig(ctx, cloneOf(t, map[string]string{"fugaro.yaml": repoYAML}), nil, lo)
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
