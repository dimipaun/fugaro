package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func publishedLayerAt(t *testing.T, r *addRig, text string) {
	t.Helper()
	p := filepath.Join(r.dir, "runs", filepath.FromSlash(config.LayerKey))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// starterHost records the one publish a starter makes.
type starterHost struct{ got []statePublish }

func (h *starterHost) Visibility(context.Context, string) (stateVisibility, error) {
	return stateVisibilityPrivate, nil
}

func (h *starterHost) Publish(_ context.Context, p statePublish) (string, error) {
	h.got = append(h.got, p)
	return "https://github.com/" + p.Repo + "/pull/7", nil
}

func useStarterHost(t *testing.T) *starterHost {
	t.Helper()
	h := &starterHost{}
	old := enrolStarterHosts
	enrolStarterHosts = func(string) (stateHost, error) { return h, nil }
	t.Cleanup(func() { enrolStarterHosts = old })
	return h
}

// A repository with no fugaro.yaml gets a pull request for the minimal one and
// the enrolment stops there, writing nothing; one that has a file is enrolled
// as before and no pull request is opened.
func TestReposAddOpensAPullRequestForTheStarterFugaroYAML(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		r := enrolRig(t)
		publishedLayerAt(t, r, testProjectLayer)
		host := useStarterHost(t)
		file := enrolCheckoutFile(t)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		before := readFileString(t, r.cfg)
		out, errOut, err := r.enrol(t, enrolRepo)
		if err != nil {
			t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
		}
		if len(host.got) != 1 {
			t.Fatalf("publishes = %d, want 1", len(host.got))
		}
		p := host.got[0]
		if p.Repo != enrolRepo || p.Path() != "fugaro.yaml" || p.Branch == "main" || !strings.HasPrefix(string(p.Bytes), minimalAnchored) || strings.Contains(string(p.Bytes), "agent") {
			t.Errorf("publish = %+v, want the minimal file on a branch of %s", p, enrolRepo)
		}
		for _, want := range []string{"pull request https://github.com/" + enrolRepo + "/pull/7", "merge it, then run `fugaro project repos add " + enrolRepo + "` again"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "⚠ CONFIRM") {
			t.Errorf("the run asked the confirmation:\n%s", out)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("the checkout was written to: %v", err)
		}
		if got := readFileString(t, r.cfg); got != before {
			t.Errorf("the local config changed:\n%s", got)
		}
	})

	t.Run("one", func(t *testing.T) {
		r := enrolRig(t)
		publishedLayerAt(t, r, testProjectLayer)
		host := useStarterHost(t)
		out, errOut, err := r.enrol(t, enrolRepo)
		if err != nil {
			t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
		}
		if len(host.got) != 0 {
			t.Errorf("a repository with a fugaro.yaml had a pull request opened: %+v", host.got)
		}
		if !strings.Contains(out, "⚠ CONFIRM") {
			t.Errorf("the enrolment did not run:\n%s", out)
		}
	})
}
