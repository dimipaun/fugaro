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
