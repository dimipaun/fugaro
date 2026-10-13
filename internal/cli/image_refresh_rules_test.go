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
		"alpha": managedRef("alpha", "0.4.0"), "beta": managedRef("beta", "0.6.0"), "ten": managedRef("ten", "0.10.0")}}
	for kind, want := range map[string]string{
		"alpha": managedRef("alpha", "0.5.1"), // an older copy moves on
		"beta":  managedRef("beta", "0.6.0"),  // a newer copy is kept
		"gamma": managedRef("gamma", "0.5.1"), // none yet: this release's
		"ten":   managedRef("ten", "0.10.0"),  // 0.10.0 is newer than 0.9.0 (numeric compare)
	} {
		if got, err := refreshBase(lc, kind, refreshVer(kind)); err != nil || got != want {
			t.Errorf("%s: %s, %v; want %s", kind, got, err, want)
		}
	}
}

// refreshVer is the CLI version a TestRefreshBase case runs under.
func refreshVer(kind string) string {
	if kind == "ten" {
		return "0.9.0"
	}
	return "0.5.1"
}

func TestRefreshBaseGuards(t *testing.T) {
	mk := func(kind, cur string) *localcfg.Config {
		return &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5", BaseImages: map[string]string{kind: cur}}
	}
	for _, cur := range []string{refreshHost + "/fugaro-base/fugaro-alpha:dev-abc", "ghcr.io/me/fugaro-alpha:mine", "evil.example/fugaro-base/fugaro-alpha:0.6.0", refreshHost + "/fugaro-base/fugaro-alpha:0.6.0@sha256:" + strings.Repeat("a", 64)} {
		got, err := refreshBase(mk("alpha", cur), "alpha", "0.5.1")
		if ExitCode(err) != ExitUserError || got != "" || !strings.Contains(err.Error(), "never replaces") {
			t.Errorf("%s: %q, %v", cur, got, err)
		}
	}
	for _, ver := range []string{"dev", "", "0.5.1-rc1", "v0.5.1"} {
		got, err := refreshBase(mk("alpha", managedRef("alpha", "0.4.0")), "alpha", ver)
		if ExitCode(err) != ExitUserError || got != "" {
			t.Errorf("version %q: %q, %v", ver, got, err)
		}
	}
}

func TestRefreshVerdict(t *testing.T) {
	want := managedRef("beta", "0.5.1")
	want9 := managedRef("beta", "0.9.0")
	if build, why := refreshVerdict(&imagecheck.Record{BaseRef: managedRef("beta", "0.10.0")}, want9, refreshHost, "beta"); build || !strings.Contains(why, "does not downgrade it") {
		t.Errorf("0.10.0 vs 0.9.0: build %v, %q", build, why)
	}
	for _, tc := range []struct {
		name  string
		rec   *imagecheck.Record
		build bool
		why   string
	}{
		{"no record", nil, true, "no build record"},
		{"current", &imagecheck.Record{BaseRef: want}, false, "current: built from " + want},
		{"older copy", &imagecheck.Record{BaseRef: managedRef("beta", "0.4.0")}, true, "built from " + managedRef("beta", "0.4.0")},
		{"ghcr release", &imagecheck.Record{BaseRef: "ghcr.io/dimipaun/fugaro-beta:0.5.1"}, true, "rebuilt from " + want},
		{"no base_ref", &imagecheck.Record{}, true, "does not say which base image"},
		{"newer copy", &imagecheck.Record{BaseRef: managedRef("beta", "0.6.0")}, false, "does not downgrade it"},
		{"lookalike newer", &imagecheck.Record{BaseRef: "evil.example/fugaro-base/fugaro-beta:0.6.0"}, true, "rebuilt from"},
		{"ghcr newer", &imagecheck.Record{BaseRef: "ghcr.io/dimipaun/fugaro-beta:0.6.0"}, true, "rebuilt from"},
		{"digest suffix", &imagecheck.Record{BaseRef: managedRef("beta", "0.6.0") + "@sha256:" + strings.Repeat("a", 64)}, true, "rebuilt from"},
		{"v prefix", &imagecheck.Record{BaseRef: managedRef("beta", "v0.6.0")}, true, "rebuilt from"},
		{"rc tag", &imagecheck.Record{BaseRef: managedRef("beta", "0.6.0-rc1")}, true, "rebuilt from"},
		{"newer, other kind", &imagecheck.Record{BaseRef: managedRef("gamma", "0.6.0")}, true, "rebuilt from"},
	} {
		build, why := refreshVerdict(tc.rec, want, refreshHost, "beta")
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
	putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.5.1"))
	if rec, err := readRefreshRecord(t.Context(), lc, slug, "app"); err != nil || rec.BaseRef != managedRef("base", "0.5.1") {
		t.Fatalf("record: %+v %v", rec, err)
	}
	putRecordData(t, "not json")
	if rec, err := readRefreshRecord(t.Context(), lc, slug, "app"); err != nil || rec == nil || rec.BaseRef != "" {
		t.Fatalf("unreadable: %+v %v", rec, err)
	}
}

func TestCustomBaseRefusal(t *testing.T) {
	lc := &localcfg.Config{BaseImages: map[string]string{"alpha": refreshHost + "/fugaro-base/fugaro-alpha:dev-abc", "beta": "ghcr.io/me/fugaro-beta:mine"}}
	err := customBaseRefusal(lc, "/home/me/my config.yaml", []string{"alpha", "beta"}, "fugaro image refresh --workflow app")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d", ExitCode(err))
	}
	for _, want := range []string{"base_images.alpha is " + refreshHost + "/fugaro-base/fugaro-alpha:dev-abc", "base_images.beta is ghcr.io/me/fugaro-beta:mine",
		"never replaces", "remove base_images.alpha, base_images.beta from '/home/me/my config.yaml' (keep a backup), then rerun fugaro image refresh --workflow app",
		"fugaro image build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("lacks %q: %v", want, err)
		}
	}
}

func TestCustomBaseRefusalNoKinds(t *testing.T) {
	err := customBaseRefusal(&localcfg.Config{}, "/home/me/c.yaml", nil, "fugaro image refresh")
	if ExitCode(err) != ExitUserError || strings.Contains(err.Error(), "remove  from") || !strings.Contains(err.Error(), "no custom base image") {
		t.Fatalf("%v", err)
	}
}
