package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/testutil"
	"gocloud.dev/blob/gcsblob"
	"google.golang.org/api/option"
)

// refreshYAML is acme/app with two workflows of two kinds.
const refreshYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { base: go, commands: { build: sh build.sh, test: sh test.sh } }\n"

func useVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func useSelf(t *testing.T) {
	t.Helper()
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
}

func noRecordReads(t *testing.T) {
	t.Helper()
	prev := openRecordBucket
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) {
		t.Fatal("a build record was read")
		return nil, nil
	}
	t.Cleanup(func() { openRecordBucket = prev })
}

func TestRefreshPreflightPlans(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	r.appendConfig(t, "base_images: {web-node: "+managedRef("web-node", "0.4.0")+"}\n")
	putWorkflowRecordFrom("app", "0.4.0", managedRef("web-node", "0.4.0"))
	p, err := refreshPreflight(t.Context(), refreshOptions{repo: "acme/app"}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.workflows, ",") != "api,app" || strings.Join(p.kinds, ",") != "go,web-node" ||
		p.want["web-node"] != managedRef("web-node", "0.5.1") || p.want["go"] != managedRef("go", "0.5.1") ||
		strings.Join(p.builds, ",") != "api,app" {
		t.Fatalf("plan %+v", p)
	}
	var out bytes.Buffer
	p.print(&out)
	for _, want := range []string{"fugaro image refresh of acme/app (project aurora, GCP project proj-1234)", "2. base go: " + managedRef("go", "0.5.1"),
		"3. the daily image check job", "4. builds: 2 of 2 workflow(s)", "app: built from " + managedRef("web-node", "0.4.0"), "5. then"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
	if calls := r.calls(t); len(calls) != 0 || len(r.ar.Requests()) != 0 {
		t.Errorf("preflight ran terraform or touched the registry: %q", calls)
	}
	// One workflow, already current.
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	p, err = refreshPreflight(t.Context(), refreshOptions{workflows: []string{"app"}}, &initOptions{})
	if err != nil || strings.Join(p.kinds, ",") != "web-node" || len(p.builds) != 0 || !strings.HasPrefix(p.why["app"], "current") {
		t.Fatalf("current: %v %+v", err, p)
	}
}

func TestRefreshRefusesCustomBase(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	r.appendConfig(t, "base_images: {go: "+refreshHost+"/fugaro-base/fugaro-go:dev-abc, web-node: "+refreshHost+"/fugaro-base/fugaro-web-node:dev-xyz}\n")
	noRecordReads(t)
	_, err := refreshPreflight(t.Context(), refreshOptions{workflows: []string{"api"}}, &initOptions{})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "base_images.go is "+refreshHost+"/fugaro-base/fugaro-go:dev-abc") ||
		strings.Contains(err.Error(), "base_images.web-node") || !strings.Contains(err.Error(), "then rerun fugaro image refresh --workflow api") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	r.check(t, refreshYAML)
}

func TestRefreshPreflightRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		onboarded     bool
		outside       bool
		o             refreshOptions
		want          string
	}{
		{"dev build", "dev", true, false, refreshOptions{}, "a development build"},
		{"not onboarded", "0.5.1", false, false, refreshOptions{}, "never onboards a repository"},
		{"another repo", "0.5.1", true, false, refreshOptions{repo: "acme/other"}, "--repo acme/other is not this checkout's origin"},
		{"unknown workflow", "0.5.1", true, false, refreshOptions{workflows: []string{"nope"}}, `fugaro.yaml has no workflow "nope" (it has: api, app)`},
		{"outside a checkout", "0.5.1", true, true, refreshOptions{repo: "acme/app"}, "run it in a checkout of acme/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useVersion(t, tc.version)
			newAnchorModeRig(t, refreshYAML, tc.onboarded)
			if tc.outside {
				t.Chdir(t.TempDir())
			}
			noRecordReads(t)
			_, err := refreshPreflight(t.Context(), tc.o, &initOptions{})
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
		})
	}
}

func TestRefreshAgainKeepsEveryFlag(t *testing.T) {
	useSelf(t)
	o := refreshOptions{
		cloud: cloudOptions{config: "/x/my p.yaml", project: "aurora", gcpProject: "proj-1234", region: "us-east5"},
		repo:  "acme/app", workflows: []string{"app", "api"}, imageSource: "ghcr.io/acme",
		expectDigests: []string{"go=sha256:" + strings.Repeat("a", 64), "web-node=sha256:" + strings.Repeat("b", 64)},
	}
	want := "fugaro image refresh --repo acme/app --workflow app --workflow api --config '/x/my p.yaml' --project aurora --gcp-project proj-1234 --region us-east5 --image-source ghcr.io/acme" +
		" --expect-digest go=sha256:" + strings.Repeat("a", 64) + " --expect-digest web-node=sha256:" + strings.Repeat("b", 64)
	if got := o.again(); got != want {
		t.Errorf("again:\n%s\nwant:\n%s", got, want)
	}
	if got := (refreshOptions{}).again(); got != "fugaro image refresh" {
		t.Errorf("no flags: %q", got)
	}
}

func TestRefreshRefusalRerunKeepsConfig(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	r.appendConfig(t, "base_images: {go: "+refreshHost+"/fugaro-base/fugaro-go:dev-abc}\n")
	noRecordReads(t)
	o := refreshOptions{workflows: []string{"api"}, expectDigests: []string{"go=sha256:" + strings.Repeat("c", 64)}}
	_, err := refreshPreflight(t.Context(), o, &initOptions{})
	if err == nil || !strings.Contains(err.Error(), "then rerun fugaro image refresh --workflow api --expect-digest go=sha256:"+strings.Repeat("c", 64)) {
		t.Fatalf("%v", err)
	}
}

// forbiddenRecords opens a GCS bucket whose every read is an HTTP 403.
func forbiddenRecords(t *testing.T) func(context.Context, string) (*blobx.Bucket, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":403,"message":"denied"}}`)
	}))
	t.Cleanup(srv.Close)
	return func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		client, err := storage.NewClient(ctx, option.WithEndpoint(srv.URL+"/storage/v1/"), option.WithoutAuthentication(), storage.WithJSONReads())
		if err != nil {
			return nil, err
		}
		client.SetRetry(storage.WithPolicy(storage.RetryNever))
		gb, err := gcsblob.OpenBucket(ctx, nil, "b", &gcsblob.Options{Client: client})
		if err != nil {
			return nil, err
		}
		return &blobx.Bucket{Bucket: gb, GCSName: "b"}, nil
	}
}

// A record read that fails is exit 2, never "current" and never a plan.
func TestRefreshRecordReadErrorsAreRemote(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(t *testing.T) func(context.Context, string) (*blobx.Bucket, error)
		want string
	}{
		{"open fails", func(*testing.T) func(context.Context, string) (*blobx.Bucket, error) {
			return func(context.Context, string) (*blobx.Bucket, error) { return nil, errors.New("boom") }
		}, "boom"},
		{"403", forbiddenRecords, "access denied (HTTP 403)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useVersion(t, "0.5.1")
			useSelf(t)
			newAnchorModeRig(t, refreshYAML, true)
			prev := openRecordBucket
			openRecordBucket = tc.open(t)
			t.Cleanup(func() { openRecordBucket = prev })
			p, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
			if ExitCode(err) != ExitRemoteError || p != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("exit %d, plan %+v, err %v", ExitCode(err), p, err)
			}
			if tc.name == "403" && strings.Contains(err.Error(), "no build record") && !strings.Contains(err.Error(), "is not \"no build record\"") {
				t.Errorf("a 403 reads as no record: %v", err)
			}
		})
	}
}

// The origin's path is percent-decoded, so a hostile origin can carry control
// or bidi characters into the repository name: none reaches the terminal.
// (A raw ESC makes the decoded URL unparseable and is refused even earlier.)
func TestRefreshRepoMismatchPrintsSafely(t *testing.T) {
	for _, esc := range []string{"%E2%80%AE", "%1b"} {
		t.Run(esc, func(t *testing.T) {
			useVersion(t, "0.5.1")
			r := newAnchorModeRig(t, refreshYAML, true)
			testutil.Git(t, r.dir, "remote", "set-url", "origin", "https://github.com/acme/ap"+esc+"p.git")
			noRecordReads(t)
			_, err := refreshPreflight(t.Context(), refreshOptions{repo: "acme/other"}, &initOptions{})
			if ExitCode(err) != ExitUserError || strings.ContainsAny(err.Error(), "\x1b\u202e") {
				t.Fatalf("exit %d, err %q", ExitCode(err), err)
			}
			if esc != "%1b" && !strings.Contains(err.Error(), "is not this checkout's origin (acme/ap\\u202ep)") {
				t.Fatalf("err %q", err)
			}
		})
	}
}

func TestRefreshEnvChecks(t *testing.T) {
	for _, tc := range []struct{ k, v, want string }{
		{"GOOGLE_CLOUD_PROJECT", "other-proj", "GOOGLE_CLOUD_PROJECT is set to other-proj"},
		{"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "sa@x.iam.gserviceaccount.com", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT is set"},
	} {
		t.Run(tc.k, func(t *testing.T) {
			useVersion(t, "0.5.1")
			newAnchorModeRig(t, refreshYAML, true)
			t.Setenv(tc.k, tc.v)
			noRecordReads(t)
			_, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
		})
	}
}

func TestRefreshLookalikeHostIsNotKnown(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	testutil.Git(t, r.dir, "remote", "set-url", "origin", "https://git.example.com/acme/app.git")
	noRecordReads(t)
	_, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "never onboards a repository") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestRefreshDuplicateWorkflowsAreOne(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	newAnchorModeRig(t, refreshYAML, true)
	putWorkflowRecordFrom("app", "0.4.0", managedRef("web-node", "0.4.0"))
	p, err := refreshPreflight(t.Context(), refreshOptions{workflows: []string{"app", "app"}}, &initOptions{})
	if err != nil || strings.Join(p.workflows, ",") != "app" || len(p.builds) != 1 {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestRefreshPlanSaysKeptForANewerCopy(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	r.appendConfig(t, "base_images: {web-node: "+managedRef("web-node", "0.6.0")+"}\n")
	putWorkflowRecordFrom("app", "0.6.0", managedRef("web-node", "0.6.0"))
	p, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	p.print(&out)
	got := out.String()
	if !strings.Contains(got, "base web-node: "+managedRef("web-node", "0.6.0")+" (a newer copy than this release's, kept") ||
		!strings.Contains(got, "base go: "+managedRef("go", "0.5.1")+" (copied into your registry if it lacks it") {
		t.Errorf("plan:\n%s", got)
	}
}

// fakeSteps replaces steps 2 to 4; fail names the step that fails, with
// err. It records the steps that ran, in order (requirement: order is base
// -> reload -> check job -> builds; a failure stops everything after it).
func fakeSteps(t *testing.T, fail string, err error) *[]string {
	t.Helper()
	ran := &[]string{}
	old := newRefreshSteps
	newRefreshSteps = func(r *initRun, e *initEngine, p *refreshPlan) refreshSteps {
		step := func(name string) error {
			*ran = append(*ran, name)
			if name == fail {
				return err
			}
			return nil
		}
		return refreshSteps{
			base: func(context.Context) error { return step("base") },
			reload: func(context.Context) (*refreshTarget, error) {
				return &refreshTarget{lc: p.lc, cfg: p.cfg}, nil
			},
			checkJob: func(context.Context, *refreshTarget) error { return step("check job") },
			builds:   func(context.Context, *refreshTarget) error { return step("builds") },
		}
	}
	t.Cleanup(func() { newRefreshSteps = old })
	return ran
}

// D2: a coding agent's session is refused before refreshPreflight runs, and
// before any credential, network or cloud use; there is no bypass flag.
// Mutation (run, restore): move the refuseRefreshHere call after
// refreshPreflight in runImageRefresh, or drop it, and this test fails (the
// agent marker is ignored and newMirror is reached, or a different error is
// returned from preflight's own git check).
func TestRefreshRefusedInAgentSession(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Chdir(t.TempDir()) // not even a checkout: preflight would refuse differently
	noRecordReads(t)
	prev := newMirror
	newMirror = func(context.Context, *localcfg.Config, []string) (*mirror.Mirror, error) {
		t.Fatal("an agent session reached the registry")
		return nil, nil
	}
	t.Cleanup(func() { newMirror = prev })
	fakeTerminal(t)
	_, _, err := executeStdin(t, "", "image", "refresh", "--repo", "acme/app")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro image refresh applies cloud changes: "+initflow.AgentRefusal("CLAUDECODE")) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// D2: anything but a real terminal is refused before refreshPreflight, with
// no --yes, --json or --non-interactive to get past it (the command defines
// none of them).
func TestRefreshNeedsATerminal(t *testing.T) {
	t.Chdir(t.TempDir())
	noRecordReads(t)
	_, _, err := executeStdin(t, "", "image", "refresh")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), initflow.NoTerminalAdvice) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// D2: fugaro image refresh defines no --yes, so r.confirm (step 3) and
// r.ask can never be auto-confirmed under it; cobra refuses the flag before
// RunE is even reached. Mutation (run, restore): add
// f.BoolVar(&o.yes, "yes", false, "...") to newImageRefreshCmd, and this
// test fails (the flag is accepted).
func TestRefreshHasNoYesFlag(t *testing.T) {
	if f := newImageRefreshCmd().Flags().Lookup("yes"); f != nil {
		t.Fatalf("fugaro image refresh defines --yes, which would bypass D2's typed confirmations: %+v", f)
	}
	_, _, err := executeStdin(t, "", "image", "refresh", "--yes")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --yes") {
		t.Fatalf("err %v", err)
	}
}

// Requirement: the engine's local-config path (where step 2, the images
// stage, records the new base) is the SAME path refreshReload reads in step
// 3. Real base and reload run here (only step 3 and 4 are stubbed), through
// runImageRefresh's own wiring. Mutation (run, restore): change
// runImageRefresh's initEngine literal from path: p.lcPath to a different
// path (or refreshReload to load a different one), and this test fails
// (reload does not see the base step's write).
func TestRefreshBaseAndReloadShareTheLocalConfigPath(t *testing.T) {
	useSelf(t)
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML))
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
	records = map[string]string{}
	prevOpen := openRecordBucket
	openRecordBucket = openFakeRecords
	t.Cleanup(func() { openRecordBucket = prevOpen })
	fakeTerminal(t)

	var reloaded *refreshTarget
	old := newRefreshSteps
	newRefreshSteps = func(rr *initRun, e *initEngine, p *refreshPlan) refreshSteps {
		return refreshSteps{
			base: func(ctx context.Context) error { return rr.refreshBase(ctx, e, p.kinds) },
			reload: func(ctx context.Context) (*refreshTarget, error) {
				tg, err := refreshReload(ctx, p)
				reloaded = tg
				return tg, err
			},
			checkJob: func(context.Context, *refreshTarget) error { return nil },
			builds:   func(context.Context, *refreshTarget) error { return nil },
		}
	}
	t.Cleanup(func() { newRefreshSteps = old })

	out, _, err := executeStdin(t, initProjectName+"\n", "image", "refresh")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wantGo := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-go:1.2.3"
	wantWeb := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"
	if reloaded == nil || reloaded.lc.BaseImages["go"] != wantGo || reloaded.lc.BaseImages["web-node"] != wantWeb {
		t.Fatalf("refreshReload did not see the base step's write through the same local-config path: %+v", reloaded)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("fugaro image refresh ran terraform: %q", calls)
	}
}

// D12: the order is base -> reload -> check job -> builds; a failure at a
// step stops everything after it, and the stop message names the finished
// steps, the failed one, and the exact rerun command (every flag kept).
// Mutation (run, restore): in runImageRefresh, call s.checkJob before
// s.base (reordering the two step invocations), and this test fails (ran
// becomes "check job" first, or the base step is reported as finished when
// it did not run).
func TestRefreshStopsSayWhatFinished(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	fakeTerminal(t)
	ran := fakeSteps(t, "check job", remote(errors.New("updating Cloud Run job x: 503")))
	out, _, err := executeStdin(t, "", "image", "refresh", "--workflow", "app")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	for _, want := range []string{"updating Cloud Run job x: 503", "stopped at step 3 (check job)", "steps finished: preflight, base",
		"rerun fugaro image refresh --workflow app in this checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Join(*ran, ",") != "base,check job" {
		t.Errorf("ran %v: the builds ran after a failed step", *ran)
	}
	if !strings.Contains(out, "4. builds:") || strings.Index(out, "fugaro image refresh of acme/app") > strings.Index(out, "step 2, base:") {
		t.Errorf("the plan is not printed first:\n%s", out)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("fugaro image refresh ran terraform: %q", calls)
	}
}

// D10: the end note names fugaro init --anchor only when the checkout
// lacks gcp_project:, and fugaro.yaml is never written either way (item 7:
// no Terraform, no live cloud, through the whole command).
func TestRefreshEndsWithTheAnchorNote(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	for _, tc := range []struct {
		yaml string
		note bool
	}{{refreshYAML, true}, {withRigAnchor(refreshYAML), false}} {
		r := newAnchorModeRig(t, tc.yaml, true)
		fakeTerminal(t)
		ran := fakeSteps(t, "", nil)
		out, _, err := executeStdin(t, "", "image", "refresh")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if strings.Join(*ran, ",") != "base,check job,builds" || !strings.Contains(out, "fugaro image refresh of acme/app is done") {
			t.Fatalf("ran %v\n%s", *ran, out)
		}
		if got := strings.Contains(out, "next: run fugaro init --anchor in this checkout"); got != tc.note {
			t.Errorf("anchor note %v, want %v:\n%s", got, tc.note, out)
		}
		r.check(t, tc.yaml) // fugaro.yaml is never written; no terraform, no Artifact Registry call
	}
}

// Requirement: when step 3 finds the daily image check job missing for a
// repository whose spec has one, that fact is repeated in the final
// summary so it is not just a line that scrolled past steps 3 and 4.
// Mutation (run, restore): remove the t.checkJobMissing block in
// runImageRefresh after the builds step, and this test fails (the note
// appears only once instead of twice).
func TestRefreshFinalSummaryRepeatsMissingCheckJob(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	newAnchorModeRig(t, refreshYAML, true)
	fakeTerminal(t)
	old := newRefreshSteps
	newRefreshSteps = func(r *initRun, e *initEngine, p *refreshPlan) refreshSteps {
		return refreshSteps{
			base: func(context.Context) error { return nil },
			reload: func(context.Context) (*refreshTarget, error) {
				return &refreshTarget{lc: p.lc, cfg: p.cfg}, nil
			},
			checkJob: func(_ context.Context, t *refreshTarget) error {
				fmt.Fprintln(r.w, "  the daily image check job was not found, though this repository's spec has one: run fugaro init --repo in the checkout to deploy it (nothing was updated)")
				t.checkJobMissing = true
				return nil
			},
			builds: func(context.Context, *refreshTarget) error { return nil },
		}
	}
	t.Cleanup(func() { newRefreshSteps = old })
	out, _, err := executeStdin(t, "", "image", "refresh")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := strings.Count(out, "was not found"); n < 2 {
		t.Fatalf("the missing check job is mentioned %d time(s), want at least twice (step 3, and the final summary):\n%s", n, out)
	}
	if i, j := strings.Index(out, "is done"), strings.LastIndex(out, "was not found"); i < 0 || j < 0 || j > i {
		t.Errorf("the repeated note does not come before the done line:\n%s", out)
	}
}
