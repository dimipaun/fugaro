package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

const refreshHost = "us-east5-docker.pkg.dev/proj-1234"

func managedRef(kind, v string) string { return refreshHost + "/fugaro-base/fugaro-" + kind + ":" + v }

func TestRefreshBase(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5", BaseImages: map[string]string{
		"go": managedRef("go", "0.4.0"), "web-node": managedRef("web-node", "0.6.0")}}
	for kind, want := range map[string]string{
		"go":            managedRef("go", "0.5.1"),            // an older copy moves on
		"web-node":      managedRef("web-node", "0.6.0"),      // a newer copy is kept
		"java-services": managedRef("java-services", "0.5.1"), // none yet: this release's
	} {
		if got, err := refreshBase(lc, kind, "0.5.1"); err != nil || got != want {
			t.Errorf("%s: %s, %v; want %s", kind, got, err, want)
		}
	}
}

func TestRefreshVerdict(t *testing.T) {
	want := managedRef("web-node", "0.5.1")
	for _, tc := range []struct {
		name  string
		rec   *imagecheck.Record
		build bool
		why   string
	}{
		{"no record", nil, true, "no build record"},
		{"current", &imagecheck.Record{BaseRef: want}, false, "current: built from " + want},
		{"older copy", &imagecheck.Record{BaseRef: managedRef("web-node", "0.4.0")}, true, "built from " + managedRef("web-node", "0.4.0")},
		{"ghcr release", &imagecheck.Record{BaseRef: "ghcr.io/dimipaun/fugaro-web-node:0.5.1"}, true, "rebuilt from " + want},
		{"no base_ref", &imagecheck.Record{}, true, "does not say which base image"},
		{"newer copy", &imagecheck.Record{BaseRef: managedRef("web-node", "0.6.0")}, false, "does not downgrade it"},
		{"newer, other kind", &imagecheck.Record{BaseRef: managedRef("go", "0.6.0")}, true, "rebuilt from"},
	} {
		build, why := refreshVerdict(tc.rec, want, refreshHost, "web-node")
		if build != tc.build || !strings.Contains(why, tc.why) {
			t.Errorf("%s: build %v, %q", tc.name, build, why)
		}
	}
}

func TestReadRefreshRecord(t *testing.T) {
	newAnchorModeRig(t, anchorModeYAML(), true)
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"}
	slug := mustSlug("github", "acme/app")
	if rec, err := readRefreshRecord(t.Context(), lc, slug, "app"); rec != nil || err != nil {
		t.Fatalf("none: %+v %v", rec, err)
	}
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	if rec, err := readRefreshRecord(t.Context(), lc, slug, "app"); err != nil || rec.BaseRef != managedRef("web-node", "0.5.1") {
		t.Fatalf("record: %+v %v", rec, err)
	}
	putRecordData(t, "not json")
	if rec, err := readRefreshRecord(t.Context(), lc, slug, "app"); err != nil || rec == nil || rec.BaseRef != "" {
		t.Fatalf("unreadable: %+v %v", rec, err)
	}
}

func TestCustomBaseRefusal(t *testing.T) {
	lc := &localcfg.Config{BaseImages: map[string]string{"go": refreshHost + "/fugaro-base/fugaro-go:dev-abc", "web-node": "ghcr.io/me/fugaro-web-node:mine"}}
	err := customBaseRefusal(lc, "/home/me/my config.yaml", []string{"go", "web-node"}, "fugaro image refresh --workflow app")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d", ExitCode(err))
	}
	for _, want := range []string{"base_images.go is " + refreshHost + "/fugaro-base/fugaro-go:dev-abc", "base_images.web-node is ghcr.io/me/fugaro-web-node:mine",
		"never replaces", "remove base_images.go, base_images.web-node from '/home/me/my config.yaml' (keep a backup), then rerun fugaro image refresh --workflow app",
		"fugaro image build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("lacks %q: %v", want, err)
		}
	}
}
