package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
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
	r.appendConfig(t, "base_images: {go: "+refreshHost+"/fugaro-base/fugaro-go:dev-abc, web-node: "+managedRef("web-node", "0.4.0")+"}\n")
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
