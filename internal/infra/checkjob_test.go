package infra

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
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

// A spec that is not exactly Repo's rendering cannot be rewritten through
// CheckJobSpec without loss, so it is refused when a base must move; when
// none moves it is not a change at all, whatever its form.
func TestRewriteCheckSpecRefusals(t *testing.T) {
	rs, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	canon := rs.Check.Env[CheckSpecEnv]
	var m map[string]any
	if err := json.Unmarshal([]byte(canon), &m); err != nil {
		t.Fatal(err)
	}
	m["future_key"] = "kept by a newer fugaro"
	unknown, _ := json.Marshal(m)
	indented, _ := json.MarshalIndent(m, "", "  ")
	delete(m, "future_key")
	reformatted, _ := json.MarshalIndent(m, "", "  ")
	for name, tc := range map[string]struct{ raw, image, why string }{
		"not json":            {"{", oldBase, "not a check spec"},
		"image names no kind": {`{"base_images":{"web-node":"` + oldBase + `"}}`, "elsewhere/img:1", "none of its spec's base images"},
		"unknown key":         {string(unknown), oldBase, "keys this fugaro does not know"},
		"unknown, indented":   {string(indented), oldBase, "keys this fugaro does not know"},
		"reformatted":         {string(reformatted), oldBase, "not in the form fugaro init --repo writes"},
	} {
		_, _, err := RewriteCheckSpec(tc.raw, tc.image, map[string]string{"web-node": newBase})
		if !errors.Is(err, ErrCheckJobShape) || !strings.Contains(err.Error(), tc.why) || !strings.Contains(err.Error(), "fugaro init --repo") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Nothing to move: the spec is kept as it is, unknown keys and form too.
	for _, raw := range []string{string(unknown), string(reformatted)} {
		if spec, image, err := RewriteCheckSpec(raw, oldBase, map[string]string{"web-node": oldBase}); err != nil || spec != raw || image != oldBase {
			t.Errorf("no move: %v %s %s", err, spec, image)
		}
	}
}

// checkJobJSON is acme/sandbox's check job as Cloud Run's jobs get returns
// it after init --repo made it from oldBase with check.tf (labels on the
// job and its template, max_retries = 0, timeout, service account, args,
// the env with a secret reference), plus output-only fields and fields this
// Go client does not model, which must all survive. edit changes it first.
func checkJobJSON(t *testing.T, rs RepoSpec, edit func(j map[string]any)) string {
	t.Helper()
	labels := map[string]any{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: rs.Label, gcp.LabelRole: gcp.RoleCheck}
	var env []any
	for _, k := range slices.Sorted(maps.Keys(rs.Check.Env)) {
		env = append(env, map[string]any{"name": k, "value": rs.Check.Env[k]})
	}
	env = append(env, map[string]any{"name": "FUGARO_GIT_CREDENTIAL", "valueSource": map[string]any{
		"secretKeyRef": map[string]any{"secret": "fugaro-sandbox-git", "version": "latest"}}})
	j := map[string]any{
		"name":               "projects/proj-1234/locations/us-east5/jobs/" + rs.Check.Job,
		"uid":                "0f4e1c2a-0000-4000-8000-000000000001",
		"generation":         "3",
		"observedGeneration": "3",
		"createTime":         "2026-10-01T10:00:00.123456Z",
		"updateTime":         "2026-10-05T10:00:00.123456Z",
		"creator":            "operator@example.com",
		"lastModifier":       "operator@example.com",
		"launchStage":        "GA",
		"labels":             labels,
		"executionCount":     json.Number("7"),
		"reconciling":        false,
		"terminalCondition":  map[string]any{"type": "Ready", "state": "CONDITION_SUCCEEDED"},
		"futureJobField":     map[string]any{"enabled": false, "count": json.Number("0"), "big": json.Number("12345678901234567890")},
		"template": map[string]any{
			"labels":      maps.Clone(labels),
			"taskCount":   json.Number("1"),
			"parallelism": json.Number("0"),
			"template": map[string]any{
				"maxRetries":           json.Number("0"),
				"timeout":              "900s",
				"serviceAccount":       "fugaro-build@proj-1234.iam.gserviceaccount.com",
				"executionEnvironment": "EXECUTION_ENVIRONMENT_GEN2",
				"futureTaskField":      map[string]any{"off": false},
				"containers": []any{map[string]any{
					"image":     rs.Check.Image,
					"command":   []any{"fugaro"},
					"args":      []any{"image", "check", "--job"},
					"resources": map[string]any{"limits": map[string]any{"cpu": "1", "memory": "2Gi"}, "cpuIdle": false},
					"env":       env,
				}},
			},
		},
	}
	if edit != nil {
		edit(j)
	}
	data, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// container is the job's only container in j.
func container(j map[string]any) map[string]any {
	return j["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)
}

// specEntry is the FUGARO_CHECK_SPEC env entry of j's container.
func specEntry(j map[string]any) map[string]any {
	for _, e := range container(j)["env"].([]any) {
		if e := e.(map[string]any); e["name"] == CheckSpecEnv {
			return e
		}
	}
	return nil
}

// checkJobCloud is the fake with acme/sandbox's check job (checkJobJSON,
// edited by edit) and polls that do not wait.
func checkJobCloud(t *testing.T, edit func(j map[string]any)) (*cloud, RepoSpec, CheckJobOwner) {
	t.Helper()
	f := newCloud(t)
	rs, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	f.run.SetJobJSON(rs.Check.Job, checkJobJSON(t, rs, edit))
	old := checkJobPoll
	checkJobPoll = 0
	t.Cleanup(func() { checkJobPoll = old })
	return f, rs, CheckJobOwner{Repo: rs.Name, Label: rs.Label}
}

var toNew = map[string]string{"web-node": newBase}

// The job after the update is the job before it, every field and zero
// value included (maxRetries 0, unknown fields, the secret env), except
// exactly the container's image and FUGARO_CHECK_SPEC's value, which are
// what Repo renders for the new base.
func TestApplyCheckJobChangesOnlyImageAndSpec(t *testing.T) {
	f, rs, owner := checkJobCloud(t, nil)
	ctx := context.Background()
	before := f.run.JobJSON("proj-1234", "us-east5", rs.Check.Job)
	u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
	if err != nil || u == nil || !u.Changes() || u.OldImage() != oldBase || u.NewImage() != newBase || !strings.Contains(u.NewSpec(), newBase) ||
		u.Job() != "projects/proj-1234/locations/us-east5/jobs/"+rs.Check.Job || u.OldSpec() != rs.Check.Env[CheckSpecEnv] {
		t.Fatalf("plan: %v %+v", err, u)
	}
	if len(f.run.Patches()) != 0 {
		t.Fatal("planning wrote")
	}
	if err := ApplyCheckJob(ctx, f.c, u); err != nil {
		t.Fatal(err)
	}
	after := f.run.JobJSON("proj-1234", "us-east5", rs.Check.Job)
	in := sandboxInputs(t, m5Additions)
	in.LC.BaseImages = toNew
	rendered, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if container(after)["image"] != rendered.Check.Image || specEntry(after)["value"] != rendered.Check.Env[CheckSpecEnv] {
		t.Fatalf("after: %v %v", container(after)["image"], specEntry(after)["value"])
	}
	if mr := after["template"].(map[string]any)["template"].(map[string]any)["maxRetries"]; mr != json.Number("0") {
		t.Fatalf("maxRetries = %v, want 0 sent back", mr)
	}
	for _, j := range []map[string]any{before, after} {
		container(j)["image"] = "IMAGE"
		specEntry(j)["value"] = "SPEC"
		delete(j, "etag")
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatalf("the job changed beyond its image and spec:\nbefore %s\nafter  %s", b, a)
	}
	// Now current: nothing to do, nothing written.
	u, err = PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
	if err != nil || u.Changes() {
		t.Fatalf("replan: %v %+v", err, u)
	}
	if err := ApplyCheckJob(ctx, f.c, u); err != nil || len(f.run.Patches()) != 1 {
		t.Fatalf("a no-op apply wrote: %v", err)
	}
	// No check job at all.
	if u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", "fugarochk-none", owner, nil); u != nil || err != nil {
		t.Fatalf("missing job: %+v %v", u, err)
	}
}

// A spec that differs from Repo's rendering only in form, with no base to
// move, is no change: nothing is written.
func TestPlanCheckJobIgnoresASpecsFormWhenNothingMoves(t *testing.T) {
	f, rs, owner := checkJobCloud(t, func(j map[string]any) {
		var m map[string]any
		_ = json.Unmarshal([]byte(specEntry(j)["value"].(string)), &m)
		out, _ := json.MarshalIndent(m, "", "  ")
		specEntry(j)["value"] = string(out)
	})
	u, err := PlanCheckJob(context.Background(), f.c, "proj-1234", "us-east5", rs.Check.Job, owner, map[string]string{"web-node": oldBase})
	if err != nil || u.Changes() {
		t.Fatalf("%v %+v", err, u)
	}
	if _, err := PlanCheckJob(context.Background(), f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew); !errors.Is(err, ErrCheckJobShape) {
		t.Fatalf("a move of a reformatted spec: %v", err)
	}
}

// A job that is not this repository's check job as init --repo made it is
// refused before anything is written.
func TestPlanCheckJobRefusesAForeignOrMisshapenJob(t *testing.T) {
	setSpecRepo := func(j map[string]any, repo string) {
		var m map[string]any
		_ = json.Unmarshal([]byte(specEntry(j)["value"].(string)), &m)
		m["repo"] = repo
		out, _ := json.Marshal(m)
		specEntry(j)["value"] = string(out)
	}
	for name, tc := range map[string]struct {
		edit func(j map[string]any)
		why  string
	}{
		"no labels":       {func(j map[string]any) { delete(j, "labels") }, "by its labels"},
		"another repo":    {func(j map[string]any) { j["labels"].(map[string]any)[gcp.LabelRepo] = "other_repo" }, "by its labels"},
		"no role":         {func(j map[string]any) { delete(j["labels"].(map[string]any), gcp.LabelRole) }, "by its labels"},
		"not managed":     {func(j map[string]any) { delete(j["labels"].(map[string]any), gcp.LabelManaged) }, "by its labels"},
		"a workflow role": {func(j map[string]any) { j["labels"].(map[string]any)[gcp.LabelRole] = "workflow" }, "by its labels"},
		"no container": {func(j map[string]any) {
			j["template"].(map[string]any)["template"].(map[string]any)["containers"] = []any{}
		}, "0 containers"},
		"two containers": {func(j map[string]any) {
			task := j["template"].(map[string]any)["template"].(map[string]any)
			task["containers"] = append(task["containers"].([]any), map[string]any{"image": "sidecar:1"})
		}, "2 containers"},
		"no spec": {func(j map[string]any) {
			c := container(j)
			c["env"] = slices.DeleteFunc(c["env"].([]any), func(e any) bool { return e.(map[string]any)["name"] == CheckSpecEnv })
		}, "has no " + CheckSpecEnv},
		"spec twice": {func(j map[string]any) {
			c := container(j)
			c["env"] = append(c["env"].([]any), maps.Clone(specEntry(j)))
		}, "twice"},
		"spec from a secret": {func(j map[string]any) {
			e := specEntry(j)
			delete(e, "value")
			e["valueSource"] = map[string]any{"secretKeyRef": map[string]any{"secret": "s", "version": "latest"}}
		}, "not a plain value"},
		"spec with a value and a valueSource": {func(j map[string]any) {
			specEntry(j)["valueSource"] = map[string]any{"secretKeyRef": map[string]any{"secret": "s", "version": "latest"}}
		}, "not a plain value"},
		"another repo's spec": {func(j map[string]any) { setSpecRepo(j, "acme/other") }, `checks repository "acme/other"`},
	} {
		t.Run(name, func(t *testing.T) {
			f, rs, owner := checkJobCloud(t, tc.edit)
			u, err := PlanCheckJob(context.Background(), f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
			if !errors.Is(err, ErrCheckJobShape) || !strings.Contains(err.Error(), tc.why) || u != nil {
				t.Fatalf("%v %+v", err, u)
			}
		})
	}
	f, rs, _ := checkJobCloud(t, nil)
	if _, err := PlanCheckJob(context.Background(), f.c, "proj-1234", "us-east5", rs.Check.Job, CheckJobOwner{}, toNew); err == nil {
		t.Fatal("planned with no owner")
	}
}

// A read that fails other than with 404 is an error, not "no job".
func TestPlanCheckJobReportsAFailedRead(t *testing.T) {
	f, rs, owner := checkJobCloud(t, nil)
	f.run.SetJobFaults(gcpfake.JobFaults{GetStatus: http.StatusForbidden})
	u, err := PlanCheckJob(context.Background(), f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
	var ae *googleapi.Error
	if u != nil || !errors.As(err, &ae) || ae.Code != http.StatusForbidden || errors.Is(err, ErrCheckJobShape) {
		t.Fatalf("%v %+v", err, u)
	}
}

func TestApplyCheckJobRefusesAConcurrentChange(t *testing.T) {
	f, rs, owner := checkJobCloud(t, nil)
	ctx := context.Background()
	u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
	if err != nil {
		t.Fatal(err)
	}
	// Someone else updates the job between the read and the write.
	other, _ := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, owner, map[string]string{"web-node": newBase + "-x"})
	if err := ApplyCheckJob(ctx, f.c, other); err != nil {
		t.Fatal(err)
	}
	err = ApplyCheckJob(ctx, f.c, u)
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != http.StatusConflict || !strings.Contains(err.Error(), "changed since it was read") {
		t.Fatalf("stale write: %v", err)
	}
	if len(f.run.Patches()) != 1 {
		t.Fatal("the stale write landed")
	}
}

// Only a conflict says the job changed since it was read; another refusal
// keeps its own error.
func TestApplyCheckJobKeepsOtherRefusalsPlain(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusBadRequest} {
		f, rs, owner := checkJobCloud(t, nil)
		ctx := context.Background()
		u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
		if err != nil {
			t.Fatal(err)
		}
		f.run.SetJobFaults(gcpfake.JobFaults{PatchStatus: code})
		err = ApplyCheckJob(ctx, f.c, u)
		var ae *googleapi.Error
		if !errors.As(err, &ae) || ae.Code != code || strings.Contains(err.Error(), "changed since") {
			t.Fatalf("%d: %v", code, err)
		}
	}
}

// The update waits for its operation: through several polls, to its error,
// up to the poll limit, and no longer than its context.
func TestApplyCheckJobWaitsForTheOperation(t *testing.T) {
	ctx := context.Background()
	plan := func(t *testing.T, faults gcpfake.JobFaults) (*cloud, RepoSpec, *CheckJobUpdate) {
		f, rs, owner := checkJobCloud(t, nil)
		u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
		if err != nil {
			t.Fatal(err)
		}
		f.run.SetJobFaults(faults)
		return f, rs, u
	}
	t.Run("several polls", func(t *testing.T) {
		f, rs, u := plan(t, gcpfake.JobFaults{Polls: 3})
		if err := ApplyCheckJob(ctx, f.c, u); err != nil {
			t.Fatal(err)
		}
		// The fake changes the job only when the operation is done.
		if got := container(f.run.JobJSON("proj-1234", "us-east5", rs.Check.Job))["image"]; got != newBase {
			t.Fatalf("returned before the operation was done: image %v", got)
		}
	})
	t.Run("operation error", func(t *testing.T) {
		f, _, u := plan(t, gcpfake.JobFaults{Polls: 1, OpError: "the job's secret is gone"})
		if err := ApplyCheckJob(ctx, f.c, u); err == nil || !strings.Contains(err.Error(), "the job's secret is gone") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("poll limit", func(t *testing.T) {
		f, _, u := plan(t, gcpfake.JobFaults{Polls: checkJobPolls + 5})
		if err := ApplyCheckJob(ctx, f.c, u); err == nil || !strings.Contains(err.Error(), "not done after") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		f, _, u := plan(t, gcpfake.JobFaults{Polls: 5})
		checkJobPoll = time.Hour
		cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- ApplyCheckJob(cctx, f.c, u) }()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the wait outlived its context")
		}
	})
}

// A plan not made by PlanCheckJob, or applied already, is refused, never a
// panic or a second write.
func TestApplyCheckJobRefusesAForeignOrSpentPlan(t *testing.T) {
	f, rs, owner := checkJobCloud(t, nil)
	ctx := context.Background()
	if err := ApplyCheckJob(ctx, f.c, nil); err == nil {
		t.Fatal("applied nil")
	}
	if err := ApplyCheckJob(ctx, f.c, &CheckJobUpdate{}); err == nil {
		t.Fatal("applied a zero plan")
	}
	var none *CheckJobUpdate
	if none.Changes() {
		t.Fatal("a nil plan changes")
	}
	u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyCheckJob(ctx, f.c, u); err != nil {
		t.Fatal(err)
	}
	if err := ApplyCheckJob(ctx, f.c, u); err == nil || !strings.Contains(err.Error(), "already applied") || len(f.run.Patches()) != 1 {
		t.Fatalf("a second apply: %v, %d patches", err, len(f.run.Patches()))
	}
}

// Building Run on its own HTTP client keeps run.NewService's endpoint:
// Google's, unless overridden.
func TestNewRunServiceKeepsTheEndpoint(t *testing.T) {
	s, hc, err := newRunService(context.Background(), []option.ClientOption{option.WithoutAuthentication()})
	if err != nil || hc == nil || s.BasePath != "https://run.googleapis.com/" {
		t.Fatalf("%v %v %s", err, hc, s.BasePath)
	}
	s, _, err = newRunService(context.Background(), []option.ClientOption{option.WithoutAuthentication(), option.WithEndpoint("http://127.0.0.1:1/")})
	if err != nil || s.BasePath != "http://127.0.0.1:1/" {
		t.Fatalf("%v %s", err, s.BasePath)
	}
}

// The job's execution tokens are never sent back (one could start an
// execution): the patch body has neither, and the stored job is otherwise
// what it was, except the image and the spec.
func TestApplyCheckJobDropsExecutionTokens(t *testing.T) {
	f, rs, owner := checkJobCloud(t, func(j map[string]any) {
		j["startExecutionToken"] = "start-tok"
		j["runExecutionToken"] = "run-tok"
	})
	ctx := context.Background()
	before := f.run.JobJSON("proj-1234", "us-east5", rs.Check.Job)
	if before["startExecutionToken"] == nil || before["runExecutionToken"] == nil {
		t.Fatalf("fixture: %v", before)
	}
	u, err := PlanCheckJob(ctx, f.c, "proj-1234", "us-east5", rs.Check.Job, owner, toNew)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyCheckJob(ctx, f.c, u); err != nil {
		t.Fatal(err)
	}
	p := f.run.Patches()
	if len(p) != 1 {
		t.Fatalf("%d patches", len(p))
	}
	for _, k := range []string{"startExecutionToken", "runExecutionToken"} {
		if _, ok := p[0][k]; ok {
			t.Errorf("the patch body carries %s", k)
		}
	}
	after := f.run.JobJSON("proj-1234", "us-east5", rs.Check.Job)
	for _, j := range []map[string]any{before, after} {
		container(j)["image"] = "IMAGE"
		specEntry(j)["value"] = "SPEC"
		delete(j, "etag")
		delete(j, "startExecutionToken")
		delete(j, "runExecutionToken")
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatalf("the job changed beyond its image and spec:\nbefore %s\nafter  %s", b, a)
	}
}

type recordingTransport struct {
	paths []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.paths = append(r.paths, req.Method+" "+req.URL.Path)
	return http.DefaultTransport.RoundTrip(req)
}

// The typed Run service and the raw calls (RunHTTP) go through one
// transport: the HTTP client given in the options, so one set of
// credentials, quota project and proxy settings serves both.
func TestRunServiceAndRunHTTPShareOneTransport(t *testing.T) {
	rt := &recordingTransport{}
	hc := &http.Client{Transport: rt}
	f := newCloud(t)
	c, err := NewClients(context.Background(), f.options(hc), f.endpoints())
	if err != nil {
		t.Fatal(err)
	}
	const typed = "projects/proj-1234/locations/us-east5/jobs/typed-probe"
	const rawJob = "raw-probe"
	if _, err := c.Run.Projects.Locations.Jobs.Get(typed).Context(context.Background()).Do(); !notFound(err) {
		t.Fatalf("typed get: %v", err)
	}
	owner := CheckJobOwner{Repo: "acme/sandbox", Label: "acme_sandbox"}
	if u, err := PlanCheckJob(context.Background(), c, "proj-1234", "us-east5", rawJob, owner, nil); u != nil || err != nil {
		t.Fatalf("raw get: %+v %v", u, err)
	}
	seen := strings.Join(rt.paths, "\n")
	for _, want := range []string{"GET /v2/" + typed, "GET /v2/projects/proj-1234/locations/us-east5/jobs/" + rawJob} {
		if !strings.Contains(seen, want) {
			t.Errorf("%q did not go through the given transport; saw:\n%s", want, seen)
		}
	}
}
