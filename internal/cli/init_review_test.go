package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	gcp "github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

var updateReviewGolden = flag.Bool("update-review", false, "rewrite testdata/init_review.golden")

// reviewRun is condition.run with the run's one confirmation set up the way
// the converge does, over a fake owner check that is counted.
func (c condition) reviewRun(t *testing.T, stdin string, owned bool) (r *initRun, spy *readSpy, out *strings.Builder, ownerCalls, screens *int) {
	t.Helper()
	r, spy, out = c.run(t, stdin)
	ownerCalls, screens = new(int), new(int)
	r.review = &runReview{
		owner:  func(context.Context) (string, bool) { *ownerCalls++; return "test", owned },
		screen: func(context.Context, string) { *screens++; fmt.Fprintln(r.w, "REVIEW SCREEN") },
		cover: func() tf.Cover {
			return tf.Cover{Projects: []string{initProject}, Listed: []string{"user:me@example.com"}}
		},
	}
	return
}

func nextLine(t *testing.T, r *initRun) string {
	t.Helper()
	l, _ := r.in.ReadString('\n')
	return strings.TrimSpace(l)
}

// Every mode, fugaro-owned or not: who takes the one confirmation, who is
// asked per step as before, and who is refused. Two ordinary steps follow
// each other; the line after the first answer is never read by the second.
func TestRunConfirmationMatrix(t *testing.T) {
	for _, c := range conditions() {
		for _, owned := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/owned=%v", c.name, owned), func(t *testing.T) {
				r, spy, out, owner, screens := c.reviewRun(t, initProjectName+"\n"+initProjectName+"\nNEXT\n", owned)
				err1 := r.confirmOrdinary("applies A", "nothing was applied", "")
				err2 := r.confirmOrdinary("applies B", "nothing was applied", "")
				typedAtTerminal := c.terminal && c.agent == "" && !c.opts.yes && !c.opts.nonInteractive && !c.opts.asJSON
				switch {
				case c.agent != "":
					var ag *initflow.AgentError
					if !asAgent(err1, &ag) || !asAgent(err2, &ag) || spy.read || *owner != 0 || *screens != 0 {
						t.Fatalf("agent: %v / %v, read %v, owner %d, screens %d", err1, err2, spy.read, *owner, *screens)
					}
				case c.opts.yes:
					// --yes covers an ordinary step as it always did, with no review.
					if err1 != nil || err2 != nil || spy.read || *owner != 0 || *screens != 0 || !strings.Contains(out.String(), "confirmed by --yes") {
						t.Fatalf("--yes: %v / %v, read %v, owner %d, screens %d\n%s", err1, err2, spy.read, *owner, *screens, out)
					}
				case !c.terminal || c.opts.nonInteractive:
					if err1 == nil || err2 == nil || spy.read || *owner != 0 || *screens != 0 || !strings.Contains(err1.Error(), "real terminal") {
						t.Fatalf("no confirmation possible: %v / %v, read %v, owner %d", err1, err2, spy.read, *owner)
					}
				case !typedAtTerminal:
					// --json at a terminal: the per-step typed confirmation, as before.
					if err1 != nil || err2 != nil || *owner != 0 || *screens != 0 || nextLine(t, r) != "NEXT" || strings.Count(out.String(), "Type aurora to apply to GCP project") != 2 {
						t.Fatalf("--json: %v / %v, owner %d\n%s", err1, err2, *owner, out)
					}
				case owned:
					if err1 != nil || err2 != nil || *screens != 1 || *owner != 1 {
						t.Fatalf("owned: %v / %v, screens %d, owner %d\n%s", err1, err2, *screens, *owner, out)
					}
					// One prompt for both steps, and the second answer unread.
					if got := nextLine(t, r); got != initProjectName {
						t.Fatalf("the second step read its own line (%q)", got)
					}
					if n := strings.Count(out.String(), "Type aurora to"); n != 1 || strings.Count(out.String(), "covered by the run confirmation") != 2 {
						t.Fatalf("prompts %d:\n%s", n, out)
					}
				default:
					// Not Fugaro's: today's per-step prompts, no review, the owner asked once.
					if err1 != nil || err2 != nil || *screens != 0 || *owner != 1 || nextLine(t, r) != "NEXT" ||
						strings.Count(out.String(), "Type aurora to apply to GCP project") != 2 || strings.Contains(out.String(), "covered by the run") {
						t.Fatalf("unowned: %v / %v, screens %d, owner %d\n%s", err1, err2, *screens, *owner, out)
					}
				}
			})
		}
	}
}

// --plan-only never takes the run confirmation, with a terminal or not: a
// step it asks about is asked on its own, as before.
func TestRunConfirmationNeverTakenUnderPlanOnly(t *testing.T) {
	c := condition{"a terminal, plan-only", initOptions{planOnly: true}, true, ""}
	r, spy, _, owner, screens := c.reviewRun(t, initProjectName+"\n", true)
	if err := r.confirmOrdinary("applies A", "nothing was applied", ""); err != nil {
		t.Fatal(err)
	}
	if *owner != 0 || *screens != 0 || !spy.read {
		t.Fatalf("owner %d, screens %d, read %v: plan-only took the run confirmation", *owner, *screens, spy.read)
	}
	c = condition{"plan-only --yes", initOptions{planOnly: true, yes: true}, false, ""}
	r, spy, _, owner, _ = c.reviewRun(t, "", true)
	if err := r.confirmOrdinary("applies A", "nothing was applied", ""); err == nil || *owner != 0 || spy.read {
		t.Fatalf("--plan-only --yes confirmed a step: %v", err)
	}
}

// A name that is not the project's at the review applies nothing: this step
// is needs-you, and so is every step after it, which never reads stdin.
func TestWrongNameAtTheRunPromptAppliesNothing(t *testing.T) {
	c := condition{"a terminal", initOptions{}, true, ""}
	r, _, out, _, screens := c.reviewRun(t, "nope\n"+initProjectName+"\n", true)
	var ny *initflow.NeedsYouError
	err := r.confirmOrdinary("applies A", "nothing was applied", "")
	if !asNeedsYou(err, &ny) || *screens != 1 {
		t.Fatalf("err %v, screens %d", err, *screens)
	}
	if !strings.Contains(strings.Join(ny.Left.Commands, " "), "init") || !strings.Contains(ny.Left.Text, "type the project's name") || strings.Contains(ny.Left.Text+out.String(), "--yes") {
		t.Errorf("left %+v\n%s", ny.Left, out)
	}
	if ok, err := r.askOrdinary("applies B", ""); ok || !asNeedsYou(err, &ny) {
		t.Fatalf("a later step: %v, %v", ok, err)
	}
	if nextLine(t, r) != initProjectName {
		t.Error("a later step read stdin after the run was declined")
	}
	if *screens != 1 {
		t.Errorf("the review was shown %d times", *screens)
	}
}

// What the run confirmation never covers: each of these still asks for its own
// typed answer after it was taken (the line left in stdin is the next step's,
// and an empty stdin is a no).
func TestRunConfirmationNeverCoversTheSeparateSteps(t *testing.T) {
	c := condition{"a terminal", initOptions{}, true, ""}
	taken := func(t *testing.T, rest string) (*initRun, *strings.Builder) {
		t.Helper()
		r, _, out, _, _ := c.reviewRun(t, initProjectName+"\n"+rest, true)
		if err := r.confirmOrdinary("applies A", "nothing was applied", ""); err != nil {
			t.Fatal(err)
		}
		return r, out
	}
	t.Run("a typed-only step (askTyped)", func(t *testing.T) {
		r, _ := taken(t, "")
		if ok, reachable, err := r.askTyped("REPLACES a tag"); ok || !reachable || err != nil {
			t.Fatalf("confirmed %v, reachable %v, err %v: the run confirmation covered a typed step", ok, reachable, err)
		}
	})
	t.Run("the Firestore location", func(t *testing.T) {
		r, _ := taken(t, "")
		if err := r.confirmLocation(); err == nil {
			t.Fatal("the permanent location was confirmed by the run confirmation")
		}
		r, _ = taken(t, infra.FirestoreLocation+"\n")
		if err := r.confirmLocation(); err != nil {
			t.Fatalf("typing the location itself is the confirmation: %v", err)
		}
		r, _ = taken(t, initProjectName+"\n")
		if err := r.confirmLocation(); err == nil {
			t.Fatal("the project's name confirmed the location")
		}
	})
	t.Run("a Cloud Build", func(t *testing.T) {
		fb := gcpfake.NewBuild(t)
		r, _ := taken(t, "")
		lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			BaseImages: map[string]string{"web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"},
			Endpoints:  localcfg.Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}
		cfgs := &config.Config{Workflows: map[string]config.Workflow{"app": {Base: "web-node"}}}
		spec := infra.RepoSpec{Name: initProjectName, BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r"}
		built, err := r.buildImages(t.Context(), lc, cfgs, spec, []string{"app"})
		for _, rq := range fb.Requests() {
			if rq.Method == "POST" {
				t.Fatalf("a build was submitted on the run confirmation: %s", rq.Path)
			}
		}
		if built != 0 || err != nil && !strings.Contains(err.Error(), "not confirmed") {
			t.Fatalf("built %d, err %v", built, err)
		}
	})
	t.Run("an unlisted repository", func(t *testing.T) {
		r, _ := taken(t, "")
		e := &initEngine{r: r, lc: &localcfg.Config{Name: initProjectName}}
		ok, err := e.confirmRepo("/tmp/x", originInfo{Repo: "acme/web", Host: "github.com"})
		if ok || err != nil || e.authorized["acme/web"] {
			t.Fatalf("ok %v err %v: the run confirmation opted a repository in", ok, err)
		}
		r, _ = taken(t, initProjectName+"\n")
		e = &initEngine{r: r, lc: &localcfg.Config{Name: initProjectName}}
		if ok, _ := e.confirmRepo("/tmp/x", originInfo{Repo: "acme/web", Host: "github.com"}); ok {
			t.Fatal("the project's name opted a repository in: only owner/name does")
		}
	})
}

// A plan outside the allowlist (a destroy, a replace, an IAM binding, a
// grant to someone the review did not list, an import the root's discovery did
// not write) is not covered: the step shows why
// and asks its own typed confirmation, even after the run confirmation was
// taken; a known additive plan is covered.
func TestPlanOutsideTheAllowlistAsksItsOwn(t *testing.T) {
	c := condition{"a terminal", initOptions{}, true, ""}
	mk := func(typ string, after map[string]any, acts ...string) *tf.Plan {
		return &tf.Plan{ResourceChanges: []tf.ResourceChange{{Address: "module.x." + typ + ".z", Type: typ, Change: tf.Change{Actions: acts, After: after, AfterUnknown: map[string]any{}}}}}
	}
	additive := mk("google_storage_bucket", map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}, "create")
	for name, bad := range map[string]*tf.Plan{
		"replace":       mk("google_storage_bucket", nil, "delete", "create"),
		"iam binding":   mk("google_project_iam_binding", nil, "create"),
		"unlisted user": mk("google_project_iam_member", map[string]any{"member": "user:eve@example.com", "role": "roles/storage.objectUser"}, "create"),
		"owner":         mk("google_project_iam_member", map[string]any{"member": "user:me@example.com", "role": "roles/owner"}, "create"),
		"nil":           nil,
		"an import discovery did not write": {ResourceChanges: []tf.ResourceChange{{Address: "module.x.google_storage_bucket.z", Type: "google_storage_bucket",
			Change: tf.Change{Actions: []string{"no-op"}, Importing: &tf.Importing{ID: initProject + "/elsewhere"}, After: map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}, AfterUnknown: map[string]any{}}}}},
	} {
		for sname, tc := range map[string]struct {
			stdin string
			err   bool
		}{"its own answer": {initProjectName + "\n" + initProjectName + "\n", false}, "only the run's answer": {initProjectName + "\n", true}} {
			t.Run(name+"/"+sname, func(t *testing.T) {
				r, _, out, _, _ := c.reviewRun(t, tc.stdin, true)
				if r.notCovered(additive, nil) != "" || r.notCovered(bad, nil) == "" {
					t.Fatalf("notCovered: additive %q, bad %q", r.notCovered(additive, nil), r.notCovered(bad, nil))
				}
				if err := r.confirmOrdinary("applies the additive plan", "x", r.notCovered(additive, nil)); err != nil {
					t.Fatal(err)
				}
				err := r.confirmOrdinary("applies the other plan", "nothing was applied", r.notCovered(bad, nil))
				if (err != nil) != tc.err {
					t.Fatalf("err %v\n%s", err, out)
				}
				if !strings.Contains(out.String(), "not covered by the run confirmation") || strings.Count(out.String(), "Type aurora to apply to GCP project") != 1 {
					t.Errorf("\n%s", out)
				}
			})
		}
	}
}

// Only ordinary steps may ask the run's one confirmation. These are the
// places that do, and no other file may (the project's creation and billing,
// the Firestore location, a Cloud Build, replacing a tag, an unlisted
// repository, a secret and the plugin wiring each keep their own).
func TestRunConfirmationCallSites(t *testing.T) {
	want := map[string]int{"init.go": 7, "init_firebase.go": 2, "init_images.go": 1}
	re := regexp.MustCompile(`\br\.(confirmOrdinary|askOrdinary|runCovers)\(`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "init_review.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(re.FindAll(b, -1)); got != want[f] {
			t.Errorf("%s has %d calls of the run confirmation, want %d", f, got, want[f])
		}
	}
	// And the typed-only steps' own source never mentions it.
	for _, f := range []string{"init_project.go", "initsecrets.go", "init_repo_gate.go", "init_plugin_stage.go"} {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "Ordinary") || strings.Contains(string(b), "runCovers") {
			t.Errorf("%s uses the run confirmation", f)
		}
	}
}

// --- through the command, over the fakes ---

// markedRig is a rig whose runs bucket exists, carries the Fugaro mark for
// the project (or for another), and still gives project Viewers read access
// (a removal the review announces).
func (r *initRig) markedRuns(t *testing.T, name, gcp string) {
	t.Helper()
	r.stateBucket()
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{"fugaro": "managed"})
	r.gcs.SetBucketPolicy(initRunsBucket, gcpfake.ConvenienceBindings(initProject))
	if name != "" {
		body := fmt.Sprintf(`{"version":1,"name":%q,"gcp_project":%q}`, name, gcp)
		if err := r.gcs.Bucket(t, initRunsBucket).Bucket.WriteAll(context.Background(), infra.ProjectMarkerObject, []byte(body), nil); err != nil {
			t.Fatal(err)
		}
	}
}

func prompts(out string) (run, step int) {
	return strings.Count(out, "Type aurora to apply all of the above"), strings.Count(out, "Type aurora to apply to GCP project")
}

// A marked project asks the project's name once for the Viewer-read removal
// and the apply and the local config; each step still prints its banner.
func TestMarkedProjectTakesOnePrompt(t *testing.T) {
	r := newInitRig(t)
	r.markedRuns(t, initProjectName, initProject)
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "init")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if run, step := prompts(out); run != 1 || step != 0 {
		t.Fatalf("%d run prompt(s), %d per-step prompt(s):\n%s", run, step, out)
	}
	if len(r.ran(t, "apply")) != 1 || len(policySets(r.gcs, initRunsBucket)) != 1 || hasViewer(r.gcs.BucketPolicy(initRunsBucket)) {
		t.Fatalf("apply %d, policy sets %d", len(r.ran(t, "apply")), len(policySets(r.gcs, initRunsBucket)))
	}
	if n := strings.Count(out, "covered by the run confirmation"); n < 2 {
		t.Errorf("%d covered steps:\n%s", n, out)
	}
	for _, want := range []string{"Review: project aurora (GCP proj-1234) is Fugaro's own", "  installation:", "IAM changes announced: removes project Viewers' read access", "Still asks separately"} {
		if !strings.Contains(out, want) {
			t.Errorf("the review lacks %q:\n%s", want, out)
		}
	}
	// The plan of the apply is printed before it, as always.
	if strings.Index(out, "Review:") > strings.Index(out, "applies 0 imports") {
		t.Errorf("the review does not come before the apply:\n%s", out)
	}
	// A rerun has nothing to do, and asks nothing.
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	out, _, err = executeStdin(t, "", "init")
	if err != nil || strings.Contains(out, "Type aurora") {
		t.Fatalf("rerun: %v\n%s", err, out)
	}
}

// An unmarked project (adopted, first run, a mark for another project) keeps
// the per-step prompts exactly as before, and a typed name for one step
// confirms only that step.
func TestUnmarkedProjectKeepsPerStepPrompts(t *testing.T) {
	for name, mark := range map[string][2]string{"no mark": {"", ""}, "another project's mark": {"other", initProject}, "another GCP project": {initProjectName, "elsewhere-1"}} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			r.markedRuns(t, mark[0], mark[1])
			fakeTerminal(t)
			out, _, err := executeStdin(t, initProjectName+"\n"+initProjectName+"\n"+initProjectName+"\n", "init")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if run, step := prompts(out); run != 0 || step < 2 || strings.Contains(out, "Review:") || strings.Contains(out, "covered by the run") {
				t.Fatalf("%d run prompt(s), %d per-step:\n%s", run, step, out)
			}
			// One answer is one step: the next one is declined.
			r2 := newInitRig(t)
			r2.markedRuns(t, mark[0], mark[1])
			_, _, err = executeStdin(t, initProjectName+"\n", "init")
			if err == nil || len(r2.ran(t, "apply")) != 0 {
				t.Fatalf("one answer confirmed more than one step: %v", err)
			}
		})
	}
}

// A wrong name at the review applies nothing, the Viewer removal included,
// and the run says what to do.
func TestMarkedProjectWrongNameAppliesNothing(t *testing.T) {
	r := newInitRig(t)
	r.markedRuns(t, initProjectName, initProject)
	fakeTerminal(t)
	out, _, err := executeStdin(t, "nope\n"+initProjectName+"\n", "init")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if len(r.ran(t, "apply")) != 0 || len(policySets(r.gcs, initRunsBucket)) != 0 {
		t.Fatalf("something was applied: %q", r.calls(t))
	}
	if !strings.Contains(out, "type the project's name") || strings.Contains(out, "--yes") {
		t.Errorf("\n%s", out)
	}
}

// An agent's environment takes no confirmation and applies nothing, marked or
// not, with a name waiting on stdin.
func TestMarkedProjectInAnAgentSession(t *testing.T) {
	for _, args := range [][]string{{"init"}, {"init", "--yes"}} {
		r := newInitRig(t)
		r.markedRuns(t, initProjectName, initProject)
		fakeTerminal(t)
		t.Setenv("CLAUDECODE", "1")
		out, _, err := executeStdin(t, initProjectName+"\n", args...)
		if err == nil || len(r.ran(t, "apply")) != 0 || len(policySets(r.gcs, initRunsBucket)) != 0 {
			t.Fatalf("%v: %v, calls %q", args, err, r.calls(t))
		}
		if strings.Contains(out, "Review:") || strings.Contains(out, "Type aurora") || strings.Contains(out+err.Error(), "pass --yes") {
			t.Errorf("%v:\n%s", args, out)
		}
	}
}

// --yes covers the ordinary steps as before, with no review and no prompt;
// the typed-only ones are not reached through it.
func TestMarkedProjectYesAsBefore(t *testing.T) {
	r := newInitRig(t)
	r.markedRuns(t, initProjectName, initProject)
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "init", "--yes")
	if err != nil || strings.Contains(out, "Review:") || strings.Contains(out, "Type aurora") || len(r.ran(t, "apply")) != 1 {
		t.Fatalf("%v, calls %q\n%s", err, r.calls(t), out)
	}
	if !strings.Contains(out, "confirmed by --yes") {
		t.Errorf("\n%s", out)
	}
}

// --plan-only shows what a real run asks once, applies nothing and never
// takes --yes.
func TestMarkedProjectPlanOnlyShowsTheReview(t *testing.T) {
	r := newInitRig(t)
	r.markedRuns(t, initProjectName, initProject)
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "init", "--plan-only", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"Review: project aurora", "never takes --yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if len(r.ran(t, "apply")) != 0 || len(policySets(r.gcs, initRunsBucket)) != 0 || strings.Contains(out, "covered by the run confirmation") {
		t.Fatalf("plan-only applied: %q", r.calls(t))
	}
	// An unmarked project has no review to show.
	r2 := newInitRig(t)
	r2.markedRuns(t, "", "")
	out, _, err = executeStdin(t, "", "init", "--plan-only")
	if err != nil || strings.Contains(out, "Review:") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// What the review screen says, for a run with every kind of separate step.
func TestReviewScreenGolden(t *testing.T) {
	var b strings.Builder
	r := &initRun{cmd: &cobra.Command{}, o: &initOptions{firebase: "aurora-fp", replaceImages: []string{"history"}}, w: &b, projectName: initProjectName, gcpProject: initProject}
	e := &initEngine{r: r, lc: &localcfg.Config{Name: initProjectName}, spec: infra.InstallationSpec{RunsBucket: initRunsBucket, StateBucket: initStateBucket,
		Launchers: []string{"user:me@example.com"}, Operators: []string{"group:ops@example.com"}}, adminsRead: true, admins: []string{"user:owner@example.com"}}
	r.o.budgetAdmins = []string{"user:ba@example.com"}
	r.o.budgetAdminsChanged = true
	r.o.launchersChanged = true
	e.defaulted = "user:me@example.com"
	e.previewed = []initflow.StageResult{
		{Name: initflow.Preflight, State: initflow.Done},
		{Name: initflow.Installation, State: initflow.Skipped},
		{Name: initflow.Firebase, State: initflow.Todo},
		{Name: initflow.Installation2, State: initflow.Todo},
		{Name: initflow.Secrets, State: initflow.Todo},
		{Name: initflow.Plugin, State: initflow.Todo},
		{Name: initflow.Repository, State: initflow.Todo},
	}
	e.repo = &repositoryStage{tg: &repoTarget{repo: "acme/web", origin: originInfo{Repo: "acme/web", Host: "github.com"}}}
	e.reviewScreen(context.Background(), "created by this run")
	path := filepath.Join("testdata", "init_review.golden")
	if *updateReviewGolden {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatal("no golden file: go test ./internal/cli -run TestReviewScreenGolden -update-review")
		}
		t.Fatal(err)
	}
	if string(want) != b.String() {
		t.Fatalf("the review screen changed (go test -update-review to accept):\n%s\nwant:\n%s", b.String(), want)
	}
}

// The whole first-run shape of init --firebase with the images: in a project
// marked as Fugaro's, one typed project name covers the installation apply,
// the Viewer-read removal, the Firebase apply, the Firestore writes, the image
// copy, the history job's apply and the local config; the Firestore database's
// permanent location is typed on its own. The same run in an unmarked project
// asks for the name at each of those steps.
func TestFirebaseRunTakesOnePromptPlusTheLocation(t *testing.T) {
	run := func(t *testing.T, fp string, marked bool, stdin string) (string, error, *imagesRig) {
		t.Helper()
		r := newImagesRigFor(t, "1.2.3", fp)
		if marked {
			r.markedRuns(t, initProjectName, initProject)
		} else {
			r.markedRuns(t, "", "")
		}
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, stdin, "init", "--firebase", fp, "--base", "go")
		return out + errOut, err, r
	}
	// One project for everything (the Firebase project is the installation's).
	out, err, r := run(t, initProject, true, initProjectName+"\n"+infra.FirestoreLocation+"\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.Join(r.applies(t), ","); got != "installation,firebase,installation" {
		t.Errorf("applies %s", got)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] == "" || r.dst.tags[initProject+"/fugaro-base/history:latest"] == "" {
		t.Errorf("images not copied: %v", r.dst.tags)
	}
	runP, stepP := prompts(out)
	if runP != 1 || stepP != 0 || strings.Count(out, "Type "+infra.FirestoreLocation+" to create it there") != 1 {
		t.Fatalf("%d run prompt(s), %d per-step, location prompts %d\n%s", runP, stepP, strings.Count(out, "Type "+infra.FirestoreLocation), out)
	}
	if n := strings.Count(out, "covered by the run confirmation"); n < 5 {
		t.Errorf("%d covered steps:\n%s", n, out)
	}
	if !strings.Contains(out, "images:         copy history latest, go 1.2.3") || !strings.Contains(out, "NOT pinned with --expect-digest: history, go") {
		t.Errorf("the review does not name the images and their pinning before the typed name:\n%s", out)
	}
	if i, j := strings.Index(out, "NOT pinned"), strings.Index(out, "Type aurora to apply all of the above"); i < 0 || j < i {
		t.Errorf("the pinning is not shown before the typed name:\n%s", out)
	}
	if !strings.Contains(out, "the installation's own project") {
		t.Errorf("the screen does not say which project the Firebase root applies to:\n%s", out)
	}
	if i, j := strings.Index(out, "this does NOT create the Firestore database"), strings.Index(out, "Type "+infra.FirestoreLocation+" to create it there"); i < 0 || j < i {
		t.Errorf("the covered write does not say the database creation is asked next:\n%s", out)
	}
	// The name does not give the location.
	if _, err, r2 := run(t, initProject, true, initProjectName+"\n"+initProjectName+"\n"); err == nil || len(r2.dst.tags) != 0 {
		t.Errorf("the project's name confirmed the Firestore location (err %v, copied %v)", err, r2.dst.tags)
	}
	// Another project for Firebase, not verified as Fugaro's: its root and its
	// database writes each take their own typed name; the installation's steps
	// stay under the one confirmation.
	out, err, r = run(t, fpID, true, initProjectName+"\n"+names(1)+names(1)+infra.FirestoreLocation+"\n")
	if err != nil {
		t.Fatalf("separate Firebase project: %v\n%s", err, out)
	}
	runP, stepP = prompts(out)
	if runP != 1 || stepP != 2 || !strings.Contains(out, "NOT verified as Fugaro's") || !strings.Contains(out, "the Firebase project is NOT verified") {
		t.Fatalf("%d run prompt(s), %d per-step\n%s", runP, stepP, out)
	}
	// Unmarked: a name per step, and the count is what it was.
	out, err, _ = run(t, initProject, false, names(4)+infra.FirestoreLocation+"\n"+names(8))
	if err != nil {
		t.Fatalf("unmarked: %v\n%s", err, out)
	}
	if runP, stepP := prompts(out); runP != 0 || stepP < 6 {
		t.Fatalf("unmarked: %d run prompt(s), %d per-step\n%s", runP, stepP, out)
	}
}

// A project this run created is Fugaro's own without a mark (there is no
// bucket yet), and only then.
func TestProjectCreatedByThisRunIsOwned(t *testing.T) {
	r := &initRun{o: &initOptions{}, projectName: initProjectName, gcpProject: initProject}
	e := &initEngine{r: r}
	r.res.Created = initProject
	if why, ok := e.owner(t.Context()); !ok || why != "created by this run" {
		t.Fatalf("%q, %v", why, ok)
	}
}

// A plan that replaces something stops being covered: the apply asks its own
// typed confirmation even in a marked project after the review was taken.
func TestMarkedProjectReplacePlanAsksItsOwn(t *testing.T) {
	for name, tc := range map[string]struct {
		stdin string
		apply int
	}{"its own answer": {names(3), 1}, "only the review's": {names(1), 0}} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			r.markedRuns(t, initProjectName, initProject)
			addr := "module.installation.google_storage_bucket.runs"
			r.setPlan(t, change(addr, "delete", "create"))
			fakeTerminal(t)
			out, _, err := executeStdin(t, tc.stdin, "init", "--allow-delete", addr)
			if got := len(r.ran(t, "apply")); got != tc.apply {
				t.Fatalf("%d applies, want %d (err %v)\n%s", got, tc.apply, err, out)
			}
			if run, _ := prompts(out); run != 1 || !strings.Contains(out, "not covered by the run confirmation: the plan is outside what the review announced") {
				t.Fatalf("\n%s", out)
			}
		})
	}
}

// Only a bucket that is this project's, with the default name and the mark,
// makes the project Fugaro's own.
func TestOwnershipNeedsTheBucketToBelongToTheProject(t *testing.T) {
	mark := fmt.Sprintf(`{"version":1,"name":%q,"gcp_project":%q}`, initProjectName, initProject)
	write := func(t *testing.T, r *initRig, bucket string) {
		t.Helper()
		if err := r.gcs.Bucket(t, bucket).Bucket.WriteAll(context.Background(), infra.ProjectMarkerObject, []byte(mark), nil); err != nil {
			t.Fatal(err)
		}
	}
	managedLabels := map[string]string{"fugaro": "managed"}
	for name, tc := range map[string]struct {
		seed  func(t *testing.T, r *initRig)
		setup func(e *initEngine)
		owned bool
	}{
		"the project's own marked bucket": {func(t *testing.T, r *initRig) {
			r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedLabels)
			write(t, r, initRunsBucket)
		}, nil, true},
		"a perfect mark in another project's bucket": {func(t *testing.T, r *initRig) {
			r.gcs.AddBucket(initRunsBucket, 999, managedLabels)
			write(t, r, initRunsBucket)
		}, nil, false},
		"an unlabelled bucket with a perfect mark": {func(t *testing.T, r *initRig) {
			r.gcs.AddBucket(initRunsBucket, initProjectNumber, nil)
			write(t, r, initRunsBucket)
		}, nil, false},
		"a custom runs bucket, marked and the project's": {func(t *testing.T, r *initRig) {
			r.gcs.AddBucket("my-own-runs", initProjectNumber, managedLabels)
			write(t, r, "my-own-runs")
		}, func(e *initEngine) { e.spec.RunsBucket = "my-own-runs" }, false},
		"no bucket": {func(t *testing.T, r *initRig) {}, nil, false},
		"the project's number cannot be read": {func(t *testing.T, r *initRig) {
			r.gcs.AddBucket("fugaro-runs-nope", initProjectNumber, managedLabels)
			write(t, r, "fugaro-runs-nope")
		}, func(e *initEngine) { e.r.gcpProject = "nope"; e.spec.RunsBucket = "fugaro-runs-nope" }, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			tc.seed(t, r)
			e := rigEngine(t, r, &initOptions{})
			if tc.setup != nil {
				tc.setup(e)
			}
			if why, ok := e.owner(t.Context()); ok != tc.owned {
				t.Fatalf("owned %v (%q), want %v", ok, why, tc.owned)
			}
		})
	}
}

// The review's typed name is the project's name exactly: another case, a
// longer or a shorter name, or a name with something after it, is not it.
func TestReviewNameMustBeExact(t *testing.T) {
	for _, typed := range []string{"AURORA", "Aurora", "auroraa", "aurora2", "aur", "sandboxx", "aurora .", "x aurora", ""} {
		t.Run(typed, func(t *testing.T) {
			c := condition{"a terminal", initOptions{}, true, ""}
			r, _, _, _, _ := c.reviewRun(t, typed+"\n", true)
			var ny *initflow.NeedsYouError
			if err := r.confirmOrdinary("applies A", "x", ""); !asNeedsYou(err, &ny) {
				t.Fatalf("%q was accepted: %v", typed, err)
			}
		})
	}
}

// The review lists who gets access, with where each list came from, before the
// typed name: from flags, from the config, and the default.
func TestReviewScreenListsEffectiveAccess(t *testing.T) {
	screen := func(t *testing.T, o *initOptions, lc *localcfg.Config, spec infra.InstallationSpec, defaulted string) string {
		var b strings.Builder
		r := &initRun{cmd: &cobra.Command{}, o: o, w: &b, projectName: initProjectName, gcpProject: initProject}
		spec.RunsBucket, spec.StateBucket = initRunsBucket, initStateBucket
		e := &initEngine{r: r, lc: lc, spec: spec, defaulted: defaulted, adminsRead: true}
		e.reviewScreen(t.Context(), "test")
		return b.String()
	}
	out := screen(t, &initOptions{launchersChanged: true, operatorsChanged: true, alertEmail: "ops@example.com"}, &localcfg.Config{},
		infra.InstallationSpec{Launchers: []string{"user:a@example.com"}, Operators: []string{"user:b@example.com"}}, "")
	for _, want := range []string{"launchers:      user:a@example.com (from a flag)", "operators:      user:b@example.com (from a flag)", "alert email:    ops@example.com (from a flag)"} {
		if !strings.Contains(out, want) {
			t.Errorf("flags: lacks %q:\n%s", want, out)
		}
	}
	cfg := &localcfg.Config{}
	cfg.Terraform.AlertEmail = "cfg@example.com"
	out = screen(t, &initOptions{}, cfg, infra.InstallationSpec{Launchers: []string{"group:eng@example.com"}}, "")
	for _, want := range []string{"launchers:      group:eng@example.com (from the local config)", "operators:      nobody (from the local config)", "alert email:    cfg@example.com (from the local config)"} {
		if !strings.Contains(out, want) {
			t.Errorf("config: lacks %q:\n%s", want, out)
		}
	}
	out = screen(t, &initOptions{launchersChanged: true, operatorsChanged: true}, &localcfg.Config{},
		infra.InstallationSpec{Launchers: []string{"user:me@example.com"}, Operators: []string{"user:me@example.com"}}, "user:me@example.com")
	if !strings.Contains(out, "launchers:      user:me@example.com (default: you)") || !strings.Contains(out, "alert email:    none (not set)") {
		t.Errorf("default:\n%s", out)
	}
	if strings.Index(out, "launchers:") > strings.Index(out, "Every Terraform plan is shown as it runs") || !strings.Contains(out, "Fixed, announced and free") {
		t.Errorf("order or wording:\n%s", out)
	}
}

// Through the command: a plan that grants a role to someone the review did not
// list stops that step, which asks its own typed name.
func TestMarkedProjectUnlistedGrantAsksItsOwn(t *testing.T) {
	r := newInitRig(t)
	r.markedRuns(t, initProjectName, initProject)
	r.setPlan(t, planChange{"address": "module.installation.google_project_iam_member.x", "type": "google_project_iam_member",
		"change": map[string]any{"actions": []string{"create"}, "before": nil, "after": map[string]any{"member": "user:eve@example.com", "role": "roles/storage.objectUser"}, "after_unknown": map[string]any{}}})
	fakeTerminal(t)
	out, _, err := executeStdin(t, names(1), "init")
	if err == nil || len(r.ran(t, "apply")) != 0 || !strings.Contains(out, "not covered by the run confirmation") {
		t.Fatalf("err %v, calls %q\n%s", err, r.calls(t), out)
	}
}

// Everything the screen prints from flags or the config is printable text: an
// escape sequence from a hostile config reaches no terminal. A domain member
// is warned about, base images are listed and flagged when outside the
// project's registry, and the billing account is named.
func TestReviewScreenPrintsOnlySafeText(t *testing.T) {
	var b strings.Builder
	r := &initRun{cmd: &cobra.Command{}, o: &initOptions{launchersChanged: true, alertEmail: "a\x1b[2Jb@example.com", billingAccount: "AAAA\x1b[31m-BBBB", imageSource: "ghcr.io/ev\x1bil"},
		w: &b, projectName: initProjectName, gcpProject: initProject}
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5", RegistryHost: "us-east5-docker.pkg.dev/" + initProject,
		BaseImages: map[string]string{"go": "evil.example/\x1b[1mx:1", "web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"}}
	e := &initEngine{r: r, lc: lc, adminsRead: true, images: &imagesStage{}, spec: infra.InstallationSpec{RunsBucket: initRunsBucket, StateBucket: initStateBucket,
		Launchers: []string{"user:a\x1b[31m@example.com", "domain:example.com"}}}
	e.images.e = e
	e.previewed = []initflow.StageResult{{Name: initflow.Images, State: initflow.Todo}}
	e.reviewScreen(t.Context(), "test")
	out := b.String()
	if strings.Contains(out, "\x1b") {
		t.Fatalf("an escape sequence reached the screen:\n%q", out)
	}
	for _, want := range []string{"WARNING: launchers lists domain:example.com", "base image go: ", "OUTSIDE this project's registry", "billing account:", "image source: ghcr.io/ev"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "base image web-node: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3 (OUTSIDE") {
		t.Errorf("the project's own base image is flagged:\n%s", out)
	}
}

// The first Cloud Build's confirmation is never on the covered path: the
// function that offers it uses askTyped and never the run confirmation.
func TestCloudBuildConfirmationStaysOutsideTheCoveredPath(t *testing.T) {
	b, err := os.ReadFile("init.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	funcBody := func(sig string) string {
		i := strings.Index(src, sig)
		if i < 0 {
			t.Fatalf("%s not found", sig)
		}
		body := src[i:]
		if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
			body = body[:j+1]
		}
		return body
	}
	body := funcBody("func (r *initRun) buildImages(")
	if !strings.Contains(body, "r.askTyped(") {
		t.Error("buildImages no longer asks the typed confirmation")
	}
	if a, s := strings.Index(body, "r.askTyped("), strings.Index(body, "r.submitAndWait("); a < 0 || s < 0 || a > s {
		t.Errorf("buildImages must ask the typed confirmation (at %d) before r.submitAndWait( (at %d)", a, s)
	}
	for _, bad := range []string{"confirmOrdinary", "askOrdinary", "runCovers", "r.confirm(", "r.ask("} {
		if strings.Contains(body, bad) {
			t.Errorf("buildImages uses %s", bad)
		}
	}
	sw := funcBody("func (r *initRun) submitAndWait(")
	for _, bad := range []string{"r.confirm(", "runCovers", "askTyped"} {
		if strings.Contains(sw, bad) {
			t.Errorf("submitAndWait uses %s", bad)
		}
	}
	img, _ := os.ReadFile("image.go")
	for _, bad := range []string{"confirmOrdinary", "askOrdinary", "runCovers"} {
		if strings.Contains(string(img), bad) {
			t.Errorf("image.go uses %s", bad)
		}
	}
}

// fakeBuilder is a Cloud Build that records the specs it is given and
// succeeds.
type fakeBuilder struct {
	specs     []gcp.BuildSpec
	submitErr error // Submit returns it, after recording the spec
	waitErr   error // Wait returns it
}

func (f *fakeBuilder) Submit(_ context.Context, s gcp.BuildSpec) (gcp.BuildResult, error) {
	f.specs = append(f.specs, s)
	if f.submitErr != nil {
		return gcp.BuildResult{}, f.submitErr
	}
	return gcp.BuildResult{ID: fmt.Sprintf("b%04d", len(f.specs)), Image: s.Image + ":latest", LogURL: "https://log/" + s.Workflow}, nil
}

func (f *fakeBuilder) Wait(_ context.Context, id string, _ time.Duration) (gcp.BuildResult, error) {
	if f.waitErr != nil {
		return gcp.BuildResult{}, f.waitErr
	}
	return gcp.BuildResult{ID: id, Status: "SUCCESS", Digest: "sha256:" + strings.Repeat("d", 64)}, nil
}

func useFakeBuilder(t *testing.T) *fakeBuilder {
	t.Helper()
	f := &fakeBuilder{}
	old := newCloudBuilder
	newCloudBuilder = func(context.Context, *localcfg.Config) (cloudBuilder, error) { return f, nil }
	t.Cleanup(func() { newCloudBuilder = old })
	return f
}

// The first builds go through the seam and submitAndWait, after the typed
// confirmation, from the local config's base.
func TestFirstBuildsUseSubmitAndWait(t *testing.T) {
	fb := useFakeBuilder(t)
	fakeTerminal(t)
	e, out := stageEngine(t, initProjectName+"\n", nil)
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		BaseImages: map[string]string{"web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"}}
	e.r.setProject(lc)
	cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {Base: "web-node"}}}
	spec := infra.RepoSpec{Name: "acme/app", Slug: "acme-app", BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r",
		Workflows: map[string]infra.WorkflowSpec{"app": {}}}
	built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
	if err != nil || built != 1 || len(fb.specs) != 1 || fb.specs[0].Base != lc.BaseImages["web-node"] {
		t.Fatalf("built %d, err %v, specs %+v", built, err, fb.specs)
	}
	if !strings.Contains(out.String(), "built acme/app/app (Cloud Build build b0001)") || len(e.r.res.Builds) != 1 {
		t.Fatalf("output %s, builds %v", out.String(), e.r.res.Builds)
	}
}

// twoWorkflowBuild is a project with two workflows to build, the typed
// confirmation for each on stdin.
func twoWorkflowBuild(t *testing.T) (*initEngine, *syncBuf, *localcfg.Config, *config.Config, infra.RepoSpec) {
	t.Helper()
	fakeTerminal(t)
	e, out := stageEngine(t, initProjectName+"\n"+initProjectName+"\n", nil)
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5", RunsBucket: "proj-runs",
		Bucket: "gs://other-read", Build: localcfg.Build{MachineType: "E2_HIGHCPU_32"},
		BaseImages: map[string]string{"web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"}}
	e.r.setProject(lc)
	cfg := &config.Config{Workflows: map[string]config.Workflow{"a": {Base: "web-node"}, "b": {Base: "web-node"}}}
	spec := infra.RepoSpec{Name: "acme/app", Slug: "acme-app", BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r",
		Workflows: map[string]infra.WorkflowSpec{"a": {}, "b": {}}}
	return e, out, lc, cfg, spec
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("not an ExitError: %v", err)
	}
	return ee.Code
}

// A bad build spec is the user's to fix, and the loop stops at the first
// workflow: the second is never submitted.
func TestSubmitAndWaitBadSpecIsUserErrorAndStopsLoop(t *testing.T) {
	fb := useFakeBuilder(t)
	fb.submitErr = fmt.Errorf("wrapped: %w", gcp.ErrBadBuildSpec)
	e, _, lc, cfg, spec := twoWorkflowBuild(t)
	built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"a", "b"})
	if err == nil || exitCode(t, err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	if built != 0 || len(fb.specs) != 1 || len(e.r.res.Builds) != 0 {
		t.Fatalf("built %d, specs %d, builds %v", built, len(fb.specs), e.r.res.Builds)
	}
}

// Any other Submit error is remote, and also stops the loop.
func TestSubmitAndWaitSubmitErrorIsRemoteAndStopsLoop(t *testing.T) {
	fb := useFakeBuilder(t)
	fb.submitErr = errors.New("boom")
	e, _, lc, cfg, spec := twoWorkflowBuild(t)
	built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"a", "b"})
	if err == nil || exitCode(t, err) != ExitRemoteError {
		t.Fatalf("err = %v, want a remote error", err)
	}
	if built != 0 || len(fb.specs) != 1 {
		t.Fatalf("built %d, specs %d", built, len(fb.specs))
	}
}

// A failed Wait counts nothing and records no build.
func TestSubmitAndWaitWaitErrorIsRemoteAndRecordsNothing(t *testing.T) {
	fb := useFakeBuilder(t)
	fb.waitErr = errors.New("build failed")
	e, out, lc, cfg, spec := twoWorkflowBuild(t)
	built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"a", "b"})
	if err == nil || exitCode(t, err) != ExitRemoteError {
		t.Fatalf("err = %v, want a remote error", err)
	}
	if built != 0 || len(e.r.res.Builds) != 0 || len(fb.specs) != 1 || strings.Contains(out.String(), "built acme/app") {
		t.Fatalf("built %d, builds %v, specs %d, out %s", built, e.r.res.Builds, len(fb.specs), out.String())
	}
}

// The spec given to Submit carries the machine type, the record bucket and
// the base.
func TestSubmitAndWaitSpecCarriesMachineBucketBase(t *testing.T) {
	fb := useFakeBuilder(t)
	e, _, lc, cfg, spec := twoWorkflowBuild(t)
	if _, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"a"}); err != nil || len(fb.specs) != 1 {
		t.Fatalf("err %v, specs %d", err, len(fb.specs))
	}
	got := fb.specs[0]
	want, err := cloudBuildSpec(spec, cfg, "a", lc.BaseImages["web-node"], "E2_HIGHCPU_32", "gs://proj-runs")
	if err != nil {
		t.Fatal(err)
	}
	if got.MachineType != "E2_HIGHCPU_32" || got.Bucket != "gs://proj-runs" || got.Base != lc.BaseImages["web-node"] {
		t.Fatalf("machine %q, bucket %q, base %q", got.MachineType, got.Bucket, got.Base)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("spec = %+v, want %+v", got, want)
	}
}
