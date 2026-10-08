package infra

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

const (
	oldBase = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-0123abc"
	newBase = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:0.5.1"
)

// The rewrite is byte for byte what Repo renders for the new base: the next
// init --repo from that local config plans no change to the job.
func TestRewriteCheckSpecEqualsTerraformRendering(t *testing.T) {
	before, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	in := sandboxInputs(t, m5Additions)
	in.LC.BaseImages = map[string]string{"web-node": newBase} // the fixture's local config already sets base_images
	after, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if before.Check.Image != oldBase || after.Check.Image != newBase {
		t.Fatalf("fixture: %s, %s", before.Check.Image, after.Check.Image)
	}
	spec, image, err := RewriteCheckSpec(before.Check.Env[CheckSpecEnv], before.Check.Image, map[string]string{"web-node": newBase, "go": "ignored: the spec names no go"})
	if err != nil {
		t.Fatal(err)
	}
	if spec != after.Check.Env[CheckSpecEnv] || image != after.Check.Image {
		t.Fatalf("rewritten:\n%s %s\nrendered:\n%s %s", spec, image, after.Check.Env[CheckSpecEnv], after.Check.Image)
	}
	// A kind bases lacks keeps its base.
	if spec, image, err = RewriteCheckSpec(before.Check.Env[CheckSpecEnv], before.Check.Image, nil); err != nil ||
		spec != before.Check.Env[CheckSpecEnv] || image != oldBase {
		t.Fatalf("no bases: %v %s %s", err, spec, image)
	}
}

func TestRewriteCheckSpecRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ raw, image string }{
		"not json":            {"{", oldBase},
		"image names no kind": {`{"base_images":{"web-node":"` + oldBase + `"}}`, "elsewhere/img:1"},
	} {
		if _, _, err := RewriteCheckSpec(tc.raw, tc.image, map[string]string{"web-node": newBase}); !errors.Is(err, ErrCheckJobShape) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// checkJobCloud is the fake with acme/sandbox's check job as init --repo
// made it from oldBase, plus an env entry and labels that must survive.
func checkJobCloud(t *testing.T) (*cloud, RepoSpec) {
	t.Helper()
	f := newCloud(t)
	rs, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	f.run.SetJob(rs.Check.Job, with(managed, gcp.LabelRepo, rs.Label, gcp.LabelRole, gcp.RoleCheck), rs.Check.Image)
	f.run.SetJobEnv(rs.Check.Job, map[string]string{CheckSpecEnv: rs.Check.Env[CheckSpecEnv], "FUGARO_PROJECT": "aurora"})
	old := checkJobPoll
	checkJobPoll = 0
	t.Cleanup(func() { checkJobPoll = old })
	return f, rs
}

func TestApplyCheckJobChangesOnlyImageAndSpec(t *testing.T) {
	f, rs := checkJobCloud(t)
	ctx := context.Background()
	u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, map[string]string{"web-node": newBase})
	if err != nil || u == nil || !u.Changes() || u.OldImage != oldBase || u.NewImage != newBase || !strings.Contains(u.NewSpec, newBase) {
		t.Fatalf("plan: %v %+v", err, u)
	}
	if len(f.run.Patches()) != 0 {
		t.Fatal("planning wrote")
	}
	if err := ApplyCheckJob(ctx, f.c, u); err != nil {
		t.Fatal(err)
	}
	p := f.run.Patches()
	if len(p) != 1 {
		t.Fatalf("%d patches", len(p))
	}
	data, _ := json.Marshal(p[0])
	for _, want := range []string{`"fugaro_role":"check"`, `"FUGARO_PROJECT"`, `"aurora"`, `"memory":"512Mi"`, `"image":"` + newBase + `"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the patch lacks %s: %s", want, data)
		}
	}
	// Now current: nothing to do, nothing written.
	u, err = PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, map[string]string{"web-node": newBase})
	if err != nil || u.Changes() {
		t.Fatalf("replan: %v %+v", err, u)
	}
	if err := ApplyCheckJob(ctx, f.c, u); err != nil || len(f.run.Patches()) != 1 {
		t.Fatalf("a no-op apply wrote: %v", err)
	}
	// No check job at all.
	if u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", "fugarochk-none", nil); u != nil || err != nil {
		t.Fatalf("missing job: %+v %v", u, err)
	}
}

func TestApplyCheckJobRefusesAConcurrentChange(t *testing.T) {
	f, rs := checkJobCloud(t)
	ctx := context.Background()
	u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, map[string]string{"web-node": newBase})
	if err != nil {
		t.Fatal(err)
	}
	// Someone else updates the job between the read and the write.
	other, _ := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, map[string]string{"web-node": newBase + "-x"})
	if err := ApplyCheckJob(ctx, f.c, other); err != nil {
		t.Fatal(err)
	}
	err = ApplyCheckJob(ctx, f.c, u)
	if err == nil || !strings.Contains(err.Error(), "changed since it was read") {
		t.Fatalf("stale write: %v", err)
	}
	if len(f.run.Patches()) != 1 {
		t.Fatal("the stale write landed")
	}
}
