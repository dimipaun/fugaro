package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

// A plan that destroys, replaces or removes an IAM grant is not covered: the
// step shows why and asks its own typed confirmation, even after the run
// confirmation was taken; a plan that only adds is covered.
func TestPlanBeyondAdditiveAsksItsOwn(t *testing.T) {
	c := condition{"a terminal", initOptions{}, true, ""}
	destroy := &tf.Plan{ResourceChanges: []tf.ResourceChange{{Address: "module.x.google_y.z", Type: "google_y", Change: tf.Change{Actions: []string{"delete", "create"}}}}}
	additive := &tf.Plan{ResourceChanges: []tf.ResourceChange{{Address: "module.x.google_y.z", Type: "google_y", Change: tf.Change{Actions: []string{"create"}}}}}
	if planNotCovered(additive) != "" || planNotCovered(destroy) == "" {
		t.Fatalf("planNotCovered: additive %q, destroy %q", planNotCovered(additive), planNotCovered(destroy))
	}
	for name, tc := range map[string]struct {
		stdin string
		err   bool
	}{"its own answer": {initProjectName + "\n" + initProjectName + "\n", false}, "only the run's answer": {initProjectName + "\n", true}} {
		t.Run(name, func(t *testing.T) {
			r, _, out, _, _ := c.reviewRun(t, tc.stdin, true)
			if err := r.confirmOrdinary("applies the additive plan", "x", planNotCovered(additive)); err != nil {
				t.Fatal(err)
			}
			err := r.confirmOrdinary("applies the destroying plan", "nothing was applied", planNotCovered(destroy))
			if (err != nil) != tc.err {
				t.Fatalf("err %v\n%s", err, out)
			}
			if !strings.Contains(out.String(), "not covered by the run confirmation") || strings.Count(out.String(), "Type aurora to apply to GCP project") != 1 {
				t.Errorf("\n%s", out)
			}
		})
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
	e := &initEngine{r: r, lc: &localcfg.Config{Name: initProjectName}, spec: infra.InstallationSpec{RunsBucket: initRunsBucket, StateBucket: initStateBucket}}
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
	run := func(t *testing.T, marked bool, stdin string) (string, error, *imagesRig) {
		t.Helper()
		r := newImagesRig(t, "1.2.3")
		if marked {
			r.markedRuns(t, initProjectName, initProject)
		} else {
			r.markedRuns(t, "", "")
		}
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, stdin, "init", "--firebase", fpID, "--base", "go")
		return out + errOut, err, r
	}
	out, err, r := run(t, true, initProjectName+"\n"+infra.FirestoreLocation+"\n")
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
		t.Errorf("%d covered steps (want at least the viewers, three applies, the Firestore writes, the images and the config):\n%s", n, out)
	}
	if !strings.Contains(out, "images:         copy history latest, go 1.2.3") {
		t.Errorf("the review does not name the images:\n%s", out)
	}
	// The name does not give the location.
	if _, err, r2 := run(t, true, initProjectName+"\n"+initProjectName+"\n"); err == nil || len(r2.dst.tags) != 0 {
		t.Errorf("the project's name confirmed the Firestore location (err %v, copied %v)", err, r2.dst.tags)
	}
	// Unmarked: a name per step, and the count is what it was.
	out, err, _ = run(t, false, names(4)+infra.FirestoreLocation+"\n"+names(8))
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
			if run, _ := prompts(out); run != 1 || !strings.Contains(out, "not covered by the run confirmation: the plan destroys, replaces") {
				t.Fatalf("\n%s", out)
			}
		})
	}
}
