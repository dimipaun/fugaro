package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestCloudCheckReadsOnlyLocalFiles: --check's cloud state comes from
// fugaro.yaml and the local config: no build record, no Terraform, no
// registry, no credentials.
func TestCloudCheckReadsOnlyLocalFiles(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		state        cloudState
		has, lacks   string
	}{
		{"nothing recorded", "", cloudStale, "go: none recorded, this release's is " + managedRef("go", "0.5.1"), ""},
		{"one kind behind", "base_images: {web-node: " + managedRef("web-node", "0.5.1") + ", go: " + managedRef("go", "0.4.0") + "}\n",
			cloudStale, "go: " + managedRef("go", "0.4.0") + ", this release's is " + managedRef("go", "0.5.1"), "web-node:"},
		{"current", "base_images: {web-node: " + managedRef("web-node", "0.5.1") + ", go: " + managedRef("go", "0.5.1") + "}\n",
			cloudCurrent, "reads no build record", ""},
		{"a newer copy is kept", "base_images: {web-node: " + managedRef("web-node", "0.6.0") + ", go: " + managedRef("go", "0.5.1") + "}\n",
			cloudCurrent, "", ""},
		{"a custom base blocks", "base_images: {go: " + refreshHost + "/fugaro-base/fugaro-go:dev-abc}\n",
			cloudBlocked, "base_images.go is " + refreshHost + "/fugaro-base/fugaro-go:dev-abc", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useVersion(t, "0.5.1")
			useSelf(t)
			r := newAnchorModeRig(t, refreshYAML, true)
			if tc.config != "" {
				r.appendConfig(t, tc.config)
			}
			noRecordReads(t)
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "none.json"))
			v := cloudCheck(t.Context(), r.root, cloudOptions{}, "")
			if v.state != tc.state || !strings.Contains(v.reason, tc.has) || (tc.lacks != "" && strings.Contains(v.reason, tc.lacks)) {
				t.Fatalf("%+v", v)
			}
			if calls := r.calls(t); len(calls) != 0 || len(r.ar.Requests()) != 0 {
				t.Fatalf("terraform %q or the registry was touched", calls)
			}
		})
	}
}

// TestCloudCheckReadsOnlyLocalFilesWhenAnchored is
// TestCloudCheckReadsOnlyLocalFiles for a checkout that already has
// gcp_project: (an anchored repository, the common case once fugaro init
// --anchor has run): cloudCheck resolves the project layer too now (Task
// 10), and must still never read its bucket or need credentials.
//
// Mutation (run, restore): revert cloudCheck's loadCheckoutResolved call
// back to loadCheckoutConfigAt, and this test fails loudly (it opens a real
// gs:// project layer bucket; noLayerBucketReads catches it directly, and
// TestMain's own gs:// guard would too if it didn't).
func TestCloudCheckReadsOnlyLocalFilesWhenAnchored(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, withRigAnchor(refreshYAML), true)
	noRecordReads(t)
	noLayerBucketReads(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "none.json"))
	v := cloudCheck(t.Context(), r.root, cloudOptions{}, "")
	if v.state != cloudStale || !strings.Contains(v.reason, "go: none recorded, this release's is "+managedRef("go", "0.5.1")) {
		t.Fatalf("%+v", v)
	}
	if calls := r.calls(t); len(calls) != 0 || len(r.ar.Requests()) != 0 {
		t.Fatalf("terraform %q or the registry was touched", calls)
	}
}

func TestCloudCheckNotHere(t *testing.T) {
	t.Run("not onboarded", func(t *testing.T) {
		useVersion(t, "0.5.1")
		r := newAnchorModeRig(t, refreshYAML, false)
		if v := cloudCheck(t.Context(), r.root, cloudOptions{}, ""); v.state != cloudNotHere || !strings.Contains(v.reason, "acme/app is not onboarded to project aurora") {
			t.Fatalf("%+v", v)
		}
	})
	t.Run("no local config: a teammate", func(t *testing.T) {
		useVersion(t, "0.5.1")
		r := newAnchorModeRig(t, refreshYAML, true)
		v := cloudCheck(t.Context(), r.root, cloudOptions{config: filepath.Join(t.TempDir(), "none.yaml")}, "")
		if v.state != cloudNotHere || !strings.Contains(v.reason, "no local project config selects this checkout") {
			t.Fatalf("%+v", v)
		}
	})
	t.Run("development build", func(t *testing.T) {
		useVersion(t, "dev")
		r := newAnchorModeRig(t, refreshYAML, true)
		if v := cloudCheck(t.Context(), r.root, cloudOptions{}, ""); v.state != cloudNotHere || !strings.Contains(v.reason, "development build") {
			t.Fatalf("%+v", v)
		}
	})
	t.Run("no fugaro.yaml", func(t *testing.T) {
		useVersion(t, "0.5.1")
		root, _ := skillCheckout(t, wiredAt("v0.5.1"))
		if v := cloudCheck(t.Context(), root, cloudOptions{}, ""); v.state != cloudNotHere || !strings.Contains(v.reason, "no fugaro.yaml") {
			t.Fatalf("%+v", v)
		}
	})
}

// TestCloudCheckFailsOnRealErrors: everything outside U9's four skip states
// is a failure, never a skip: --check must not exit 0 where fugaro image
// refresh itself would refuse.
func TestCloudCheckFailsOnRealErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(t *testing.T, r *anchorModeRig, co *cloudOptions)
		has  string
	}{
		{"invalid fugaro.yaml", func(t *testing.T, r *anchorModeRig, _ *cloudOptions) {
			bad := strings.Replace(refreshYAML, "base: go", "base: no/such kind", 1) + "  broken: { base: go }\n"
			if err := os.WriteFile(r.path, []byte(bad), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "problem"},
		{"no origin", func(t *testing.T, r *anchorModeRig, _ *cloudOptions) {
			testutil.Git(t, r.root, "remote", "remove", "origin")
		}, "origin"},
		{"an origin that names no repository", func(t *testing.T, r *anchorModeRig, _ *cloudOptions) {
			testutil.Git(t, r.root, "remote", "set-url", "origin", "nonsense")
		}, "owner/name"},
		{"unreadable local config", func(t *testing.T, r *anchorModeRig, co *cloudOptions) {
			bad := filepath.Join(t.TempDir(), "bad.yaml")
			if err := os.WriteFile(bad, []byte("name: [unclosed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			co.config = bad
		}, ""},
		{"fugaro.yaml without a project", func(t *testing.T, r *anchorModeRig, _ *cloudOptions) {
			if err := os.WriteFile(r.path, []byte(strings.Replace(refreshYAML, "project: aurora\n", "", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useVersion(t, "0.5.1")
			r := newAnchorModeRig(t, refreshYAML, true)
			var co cloudOptions
			tc.set(t, r, &co)
			v := cloudCheck(t.Context(), r.root, co, "")
			if v.state != cloudFailed || v.reason == "" || !strings.Contains(v.reason, tc.has) || strings.Contains(v.reason, "\n") {
				t.Fatalf("%+v", v)
			}
		})
	}
}

// TestCloudCheckPrintsNothing: the caller prints the per-checkout line, so
// cloudCheck's own selection must not repeat the project header per PATH.
func TestCloudCheckPrintsNothing(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	var buf bytes.Buffer
	co := cloudOptions{stderr: func() io.Writer { return &buf }}
	if v := cloudCheck(t.Context(), r.root, co, ""); v.state != cloudStale {
		t.Fatalf("%+v", v)
	}
	if buf.Len() != 0 {
		t.Fatalf("cloudCheck wrote %q", buf.String())
	}
}

// TestCloudCheckBlockedRerunLine: the blocked reason tells the user to run
// the command they ran, PATH and flags included.
func TestCloudCheckBlockedRerunLine(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	r.appendConfig(t, "base_images: {go: "+refreshHost+"/fugaro-base/fugaro-go:dev-abc}\n")
	again := "fugaro upgrade --check " + quoteWord("/tmp/my repo")
	v := cloudCheck(t.Context(), r.root, cloudOptions{}, again)
	if v.state != cloudBlocked || !strings.Contains(v.reason, again) {
		t.Fatalf("%+v want %q", v, again)
	}
}

// TestLoadRepoConfigSelectsByTheCheckoutsOrigin: with several project
// configs and nothing else selecting one, the origin that selects is the
// given checkout's, whatever the working directory is (U16).
func TestLoadRepoConfigSelectsByTheCheckoutsOrigin(t *testing.T) {
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FUGARO_CONFIG", "")
	t.Setenv("FUGARO_PROJECT", "")
	dir := filepath.Join(xdg, "fugaro", "projects")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, repo := range map[string]string{"alpha": "acme/other", "beta": "acme/app"} {
		cfg := strings.Replace(initConfig, "name: aurora", "name: "+name, 1) + "repos:\n  " + repo + ": { provider: github, workflows: [app] }\n"
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	target := repoCheckout(t, githubOrigin, "") // no fugaro.yaml: nothing names a project
	elsewhere := repoCheckout(t, "https://github.com/acme/other.git", "")
	t.Chdir(elsewhere) // a checkout whose origin would select alpha
	lc, _, _, err := loadRepoConfig(t.Context(), &initOptions{cloud: cloudOptions{stderr: func() io.Writer { return io.Discard }}}, target)
	if err != nil || lc.Name != "beta" {
		t.Fatalf("%v %+v", err, lc)
	}
}
