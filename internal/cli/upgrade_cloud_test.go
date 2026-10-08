package cli

import (
	"path/filepath"
	"strings"
	"testing"
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
			v := cloudCheck(t.Context(), r.root, cloudOptions{})
			if v.state != tc.state || !strings.Contains(v.reason, tc.has) || (tc.lacks != "" && strings.Contains(v.reason, tc.lacks)) {
				t.Fatalf("%+v", v)
			}
			if calls := r.calls(t); len(calls) != 0 || len(r.ar.Requests()) != 0 {
				t.Fatalf("terraform %q or the registry was touched", calls)
			}
		})
	}
}

func TestCloudCheckNotHere(t *testing.T) {
	t.Run("not onboarded", func(t *testing.T) {
		useVersion(t, "0.5.1")
		r := newAnchorModeRig(t, refreshYAML, false)
		if v := cloudCheck(t.Context(), r.root, cloudOptions{}); v.state != cloudNotHere || !strings.Contains(v.reason, "acme/app is not onboarded to project aurora") {
			t.Fatalf("%+v", v)
		}
	})
	t.Run("no local config: a teammate", func(t *testing.T) {
		useVersion(t, "0.5.1")
		r := newAnchorModeRig(t, refreshYAML, true)
		v := cloudCheck(t.Context(), r.root, cloudOptions{config: filepath.Join(t.TempDir(), "none.yaml")})
		if v.state != cloudNotHere || !strings.Contains(v.reason, "no local project config selects this checkout") {
			t.Fatalf("%+v", v)
		}
	})
	t.Run("development build", func(t *testing.T) {
		useVersion(t, "dev")
		r := newAnchorModeRig(t, refreshYAML, true)
		if v := cloudCheck(t.Context(), r.root, cloudOptions{}); v.state != cloudNotHere || !strings.Contains(v.reason, "development build") {
			t.Fatalf("%+v", v)
		}
	})
	t.Run("no fugaro.yaml", func(t *testing.T) {
		useVersion(t, "0.5.1")
		root, _ := skillCheckout(t, wiredAt("v0.5.1"))
		if v := cloudCheck(t.Context(), root, cloudOptions{}); v.state != cloudNotHere || !strings.Contains(v.reason, "no fugaro.yaml") {
			t.Fatalf("%+v", v)
		}
	})
}
