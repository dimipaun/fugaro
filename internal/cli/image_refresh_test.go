package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
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
// agent marker is ignored and preflight's own refusal, that this directory
// is not a checkout, is returned instead).
func TestRefreshRefusedInAgentSession(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Chdir(t.TempDir()) // not even a checkout: preflight would refuse differently
	noRecordReads(t)
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

// D12: the order is base -> reload -> check job -> builds; a failure or a
// decline at a step stops everything after it, and the stop message names
// the finished steps, the failed one, the exact rerun command (every flag
// kept) and keeps the step's exit code (1 a decline, 2 a remote failure).
// Mutations (run, restore): in runImageRefresh, call s.checkJob before
// s.base; or change `return refreshStopped(2, ...)` to `_ = err` (a declined
// base copy goes on to the builds); or the same for step 4 (a failed build
// prints "is done" and exits 0): each fails a row below.
func TestRefreshStopsSayWhatFinished(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	steps := []struct {
		fail     string
		step     int
		name     string
		finished string
		ran      string
	}{
		{"base", 2, "base", "preflight", "base"},
		{"check job", 3, "check job", "preflight, base", "base,check job"},
		{"builds", 4, "builds", "preflight, base, check job", "base,check job,builds"},
	}
	causes := []struct {
		name string
		err  error
		exit int
	}{
		{"declined", userErr("not confirmed (the project's name was not typed); nothing was changed"), ExitUserError},
		{"remote failure", remote(errors.New("503 from the cloud")), ExitRemoteError},
	}
	for _, st := range steps {
		for _, c := range causes {
			t.Run(st.fail+" "+c.name, func(t *testing.T) {
				r := newAnchorModeRig(t, refreshYAML, true)
				fakeTerminal(t)
				ran := fakeSteps(t, st.fail, c.err)
				out, _, err := executeStdin(t, "", "image", "refresh", "--workflow", "app")
				if ExitCode(err) != c.exit {
					t.Fatalf("exit %d, want %d, err %v", ExitCode(err), c.exit, err)
				}
				for _, want := range []string{c.err.Error(), fmt.Sprintf("stopped at step %d (%s)", st.step, st.name), "steps finished: " + st.finished + ";",
					"rerun fugaro image refresh --workflow app in this checkout"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error lacks %q: %v", want, err)
					}
				}
				if got := strings.Join(*ran, ","); got != st.ran {
					t.Errorf("ran %s, want %s: a step after the stop ran, or one before it did not", got, st.ran)
				}
				if strings.Contains(out, "is done") || strings.Contains(out, "next: run fugaro init --anchor") {
					t.Errorf("a stopped run ended like a finished one:\n%s", out)
				}
				if !strings.Contains(out, "4. builds:") || strings.Index(out, "fugaro image refresh of acme/app") > strings.Index(out, "step 2, base:") {
					t.Errorf("the plan is not printed first:\n%s", out)
				}
				if calls := r.calls(t); len(calls) != 0 {
					t.Errorf("fugaro image refresh ran terraform: %q", calls)
				}
			})
		}
	}
}

// D1 and D4 are printed as they are: no stop message around them (the
// custom-base refusal already ends with its own rerun line, and neither is a
// step that finished anything). Outside a checkout the rerun line says to
// run it in a checkout of the repository, not "this checkout".
// Mutation (run, restore): drop the refusedAsIs wrapping of either refusal
// in refreshPreflight, or the noCheckout one, and a row fails.
func TestRefreshPreflightStopTexts(t *testing.T) {
	useSelf(t)
	fakeTerminal(t)
	t.Run("development build", func(t *testing.T) {
		useVersion(t, "dev")
		newAnchorModeRig(t, refreshYAML, true)
		_, _, err := executeStdin(t, "", "image", "refresh")
		want := "fugaro image refresh copies this release's base images, and this is a development build (dev) with none published: use a release build, or build a base from a checkout and point at it with fugaro init --base-image KIND=<tag>"
		if ExitCode(err) != ExitUserError || err.Error() != want {
			t.Fatalf("exit %d, err %q, want %q", ExitCode(err), err, want)
		}
	})
	t.Run("custom base", func(t *testing.T) {
		useVersion(t, "0.5.1")
		r := newAnchorModeRig(t, refreshYAML, true)
		r.appendConfig(t, "base_images: {go: "+refreshHost+"/fugaro-base/fugaro-go:dev-abc}\n")
		_, _, err := executeStdin(t, "", "image", "refresh", "--workflow", "api")
		_, direct := refreshPreflight(t.Context(), refreshOptions{workflows: []string{"api"}}, &initOptions{})
		if ExitCode(err) != ExitUserError || direct == nil || err.Error() != direct.Error() {
			t.Fatalf("exit %d, err %q, preflight's own %q", ExitCode(err), err, direct)
		}
		if n := strings.Count(err.Error(), "rerun fugaro image refresh"); n != 1 || strings.Contains(err.Error(), "stopped at step") ||
			!strings.Contains(err.Error(), "then rerun fugaro image refresh --workflow api. To keep your own base image") {
			t.Fatalf("the refusal is wrapped or doubled (%d rerun lines): %v", n, err)
		}
	})
	t.Run("outside a checkout", func(t *testing.T) {
		useVersion(t, "0.5.1")
		newAnchorModeRig(t, refreshYAML, true)
		t.Chdir(t.TempDir())
		_, _, err := executeStdin(t, "", "image", "refresh", "--repo", "acme/app")
		want := "fugaro image refresh reads fugaro.yaml and the origin from the repository's checkout, and this directory is not in one: run it in a checkout of acme/app" +
			"; fugaro image refresh stopped at step 1 (preflight), steps finished: none; once that is fixed, rerun fugaro image refresh --repo acme/app in a checkout of acme/app: finished steps say No changes"
		if ExitCode(err) != ExitUserError || err.Error() != want {
			t.Fatalf("exit %d, err %q, want %q", ExitCode(err), err, want)
		}
	})
}

// D2, the other half of "no --yes": the options the command builds for its
// steps carry yes, nonInteractive and asJSON all false, so r.ask (the base
// copy's and the check job's confirmations) cannot auto-confirm and a wrong
// or missing typed name changes nothing. The real base step and the real
// check-job step run through runImageRefresh.
// Mutation (run, restore): `iopts := &initOptions{yes: true, ...}` in
// runImageRefresh: both subtests fail (the options, and a patch / a copy
// that went through "confirmed by --yes").
func TestRefreshConfirmationsNeedTheTypedName(t *testing.T) {
	useSelf(t)
	checkOpts := func(t *testing.T, o *initOptions) {
		t.Helper()
		if o == nil {
			t.Fatal("the steps were never built")
		}
		if o.yes || o.nonInteractive || o.asJSON || o.planOnly {
			t.Errorf("the command builds options that can bypass the typed name: yes=%v nonInteractive=%v asJSON=%v planOnly=%v", o.yes, o.nonInteractive, o.asJSON, o.planOnly)
		}
	}
	for _, typed := range []string{"wrong\n", ""} {
		t.Run("check job "+strings.TrimSpace(typed), func(t *testing.T) {
			useVersion(t, "0.5.1")
			r := newAnchorModeRig(t, refreshYAML, true)
			fakeTerminal(t)
			slug, label := checkJobOwner(t, "github", "acme/app")
			oldRef, newRef := managedRef("web-node", "0.4.0"), managedRef("web-node", "0.5.1")
			spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/app", BaseImages: map[string]string{"web-node": oldRef}})
			r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
			r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
			var seen *initOptions
			old := newRefreshSteps
			newRefreshSteps = func(rr *initRun, e *initEngine, p *refreshPlan) refreshSteps {
				seen = rr.o
				return refreshSteps{
					base: func(context.Context) error { return nil },
					reload: func(context.Context) (*refreshTarget, error) {
						lc := *p.lc
						lc.BaseImages = map[string]string{"web-node": newRef}
						return &refreshTarget{lc: &lc, cfg: p.cfg, kinds: []string{"web-node"},
							spec: infra.RepoSpec{Name: "acme/app", Slug: slug, Label: label}}, nil
					},
					checkJob: func(ctx context.Context, tg *refreshTarget) error { return rr.refreshCheckJob(ctx, tg) },
					builds:   func(context.Context, *refreshTarget) error { return nil },
				}
			}
			t.Cleanup(func() { newRefreshSteps = old })
			out, _, err := executeStdin(t, typed, "image", "refresh", "--workflow", "app")
			checkOpts(t, seen)
			if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "stopped at step 3 (check job)") {
				t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
			}
			if n := len(r.run.Patches()); n != 0 || strings.Contains(out, "confirmed by --yes") || strings.Contains(out, "updated the daily image check job") {
				t.Fatalf("the check job was updated without the typed name (%d patches):\n%s", n, out)
			}
			if !strings.Contains(out, "Type "+initProjectName+" to apply") {
				t.Errorf("the typed name was never asked for:\n%s", out)
			}
		})
	}
	t.Run("base copy", func(t *testing.T) {
		useSelf(t)
		r := newImagesRig(t, "1.2.3")
		t.Chdir(repoCheckout(t, githubOrigin, refreshYAML))
		r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
		records = map[string]string{}
		prevOpen := openRecordBucket
		openRecordBucket = openFakeRecords
		t.Cleanup(func() { openRecordBucket = prevOpen })
		fakeTerminal(t)
		var seen *initOptions
		old := newRefreshSteps
		newRefreshSteps = func(rr *initRun, e *initEngine, p *refreshPlan) refreshSteps {
			seen = rr.o
			return refreshSteps{
				base: func(ctx context.Context) error { return rr.refreshBase(ctx, e, p.kinds) },
				reload: func(context.Context) (*refreshTarget, error) {
					t.Error("the run went on after a declined base copy")
					return nil, errors.New("unreachable")
				},
				checkJob: func(context.Context, *refreshTarget) error { return nil },
				builds:   func(context.Context, *refreshTarget) error { return nil },
			}
		}
		t.Cleanup(func() { newRefreshSteps = old })
		out, _, err := executeStdin(t, "wrong\n", "image", "refresh")
		checkOpts(t, seen)
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "stopped at step 2 (base)") {
			t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
		}
		if r.dst.puts != 0 || strings.Contains(out, "confirmed by --yes") {
			t.Fatalf("the base was copied without the typed name (%d writes):\n%s", r.dst.puts, out)
		}
		if got := r.localConfig(t).BaseImages["go"]; got != "" {
			t.Errorf("base_images.go was recorded: %s", got)
		}
	})
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
