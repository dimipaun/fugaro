package localcfg

import (
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"testing"
)

type sharedStub struct {
	calls     int
	name, gcp string
	err       error
	note      string
}

func (s *sharedStub) hook(name, gcp string) (*Config, string, error) {
	s.calls++
	s.name, s.gcp = name, gcp
	if s.err != nil {
		return nil, "", s.err
	}
	return &Config{Name: name, GCPProject: gcp}, s.note, nil
}

func sharedEnv(t *testing.T, projects ...string) func(string) string {
	t.Helper()
	xdg := t.TempDir()
	for _, p := range projects {
		writeFile(t, filepath.Join(xdg, "fugaro", "projects", p+".yaml"), projectYAML(p, gcpOf[p]))
	}
	return func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
}

func TestSelectFallsBackToTheSharedConfig(t *testing.T) {
	t.Run("checkout with gcp_project", func(t *testing.T) {
		st := &sharedStub{note: "using the cached shared config, 3 days old"}
		sel, c, err := Select(SelectInput{Checkout: &Checkout{Project: "aurora", GCPProject: "aurora-gcp-1"}, Shared: st.hook, GCPProject: "aurora-gcp-1", Getenv: sharedEnv(t)})
		if err != nil || c == nil || c.Name != "aurora" {
			t.Fatalf("%+v %+v %v", sel, c, err)
		}
		if sel.From != "shared config" || sel.Path != "" || sel.Name != "aurora" {
			t.Fatalf("selection = %+v", sel)
		}
		if st.calls != 1 || st.name != "aurora" || st.gcp != "aurora-gcp-1" {
			t.Fatalf("hook = %+v", st)
		}
		if len(sel.Notes) != 1 || !strings.Contains(sel.Notes[0], "3 days old") {
			t.Fatalf("notes = %v", sel.Notes)
		}
	})
	t.Run("checkout without gcp_project", func(t *testing.T) {
		st := &sharedStub{}
		_, c, err := Select(SelectInput{Checkout: &Checkout{Project: "aurora"}, Shared: st.hook, Getenv: sharedEnv(t)})
		if err == nil || c != nil {
			t.Fatalf("err = %v", err)
		}
		for _, want := range []string{"there is no project config for aurora", "add `gcp_project: <id>` next to `project:` in fugaro.yaml", "run fugaro init in the checkout", "fugaro init --config-only --gcp-project <id>"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal lacks %q: %v", want, err)
			}
		}
		if st.calls != 0 {
			t.Fatalf("hook called %d times", st.calls)
		}
	})
	t.Run("--project with --gcp-project", func(t *testing.T) {
		st := &sharedStub{}
		sel, c, err := Select(SelectInput{Project: "aurora", GCPProject: "aurora-gcp-1", Shared: st.hook, Getenv: sharedEnv(t)})
		if err != nil || c == nil || sel.From != "shared config" || sel.Path != "" || st.calls != 1 {
			t.Fatalf("%+v %v %v calls=%d", sel, c, err, st.calls)
		}
	})
	t.Run("FUGARO_PROJECT with --gcp-project", func(t *testing.T) {
		st := &sharedStub{}
		sel, c, err := Select(SelectInput{EnvProject: "aurora", GCPProject: "aurora-gcp-1", Shared: st.hook, Getenv: sharedEnv(t)})
		if err != nil || c == nil || sel.From != "shared config" || st.calls != 1 {
			t.Fatalf("%+v %v %v calls=%d", sel, c, err, st.calls)
		}
	})
	t.Run("--project without any gcp project keeps the old refusal", func(t *testing.T) {
		st := &sharedStub{}
		_, _, err := Select(SelectInput{Project: "aurora", Shared: st.hook, Getenv: sharedEnv(t)})
		if err == nil || !strings.Contains(err.Error(), "has no project config") || st.calls != 0 {
			t.Fatalf("err = %v calls=%d", err, st.calls)
		}
	})
	t.Run("a fetch failure is the refusal", func(t *testing.T) {
		st := &sharedStub{err: errors.New("no access to gs://x")}
		_, _, err := Select(SelectInput{Checkout: &Checkout{Project: "aurora", GCPProject: "aurora-gcp-1"}, GCPProject: "aurora-gcp-1", Shared: st.hook, Getenv: sharedEnv(t)})
		if err == nil || !strings.Contains(err.Error(), "no access to gs://x") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a local config wins and the hook is never called", func(t *testing.T) {
		st := &sharedStub{}
		sel, c, err := Select(SelectInput{Checkout: &Checkout{Project: "aurora", GCPProject: "aurora-gcp-1"}, GCPProject: "aurora-gcp-1", Shared: st.hook, Getenv: sharedEnv(t, "aurora")})
		if err != nil || c == nil || sel.From != "checkout" || sel.Path == "" || st.calls != 0 {
			t.Fatalf("%+v %v %v calls=%d", sel, c, err, st.calls)
		}
	})
	t.Run("FUGARO_CONFIG of the project wins over the hook", func(t *testing.T) {
		st := &sharedStub{}
		env := sharedEnv(t)
		f := filepath.Join(t.TempDir(), "x.yaml")
		writeFile(t, f, projectYAML("aurora", "aurora-gcp-1"))
		sel, c, err := Select(SelectInput{Checkout: &Checkout{Project: "aurora", GCPProject: "aurora-gcp-1"}, GCPProject: "aurora-gcp-1", EnvConfig: f, Shared: st.hook, Getenv: env})
		if err != nil || c == nil || sel.Path != f || st.calls != 0 {
			t.Fatalf("%+v %v %v calls=%d", sel, c, err, st.calls)
		}
	})
	t.Run("no hook is today's behavior", func(t *testing.T) {
		_, _, err := Select(SelectInput{Checkout: &Checkout{Project: "aurora", GCPProject: "aurora-gcp-1"}, GCPProject: "aurora-gcp-1", Getenv: sharedEnv(t)})
		if err == nil || !strings.Contains(err.Error(), "fugaro init --config-only --gcp-project <id>") || strings.Contains(err.Error(), "add `gcp_project:") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("creating never fetches", func(t *testing.T) {
		st := &sharedStub{}
		sel, c, err := Select(SelectInput{Checkout: &Checkout{Project: "aurora", GCPProject: "aurora-gcp-1"}, GCPProject: "aurora-gcp-1", Creating: true, Shared: st.hook, Getenv: sharedEnv(t)})
		if err != nil || c != nil || st.calls != 0 || sel.From != "checkout" {
			t.Fatalf("%+v %v %v calls=%d", sel, c, err, st.calls)
		}
	})
}

func TestSharedDiff(t *testing.T) {
	local := &Config{Version: 1, Name: "aurora", GCPProject: "g1", Region: "us-east5", MaxParallel: 5, User: "me@example.com",
		Repos: map[string]Repo{"a/b": {Provider: "github", BaseBranch: "main"}}}
	same := local.Shared()
	if d := SharedDiff(same, local); len(d) != 0 {
		t.Fatalf("same = %v", d)
	}
	other := local.Shared()
	other.MaxParallel = 9
	other.Region = "europe-west1"
	if d := SharedDiff(other, local); strings.Join(d, ",") != "max_parallel,region" {
		t.Fatalf("diff = %v", d)
	}
}

// The published repos, base_images and prices are a union across machines,
// while a local config holds only its own: an entry only the published
// object has is not a difference; one present in both that differs is, and
// so is a local entry the published object lacks.
func TestSharedDiffIgnoresPublishedOnlyEntries(t *testing.T) {
	local := &Config{Version: 1, Name: "aurora", GCPProject: "g1", Region: "us-east5",
		Repos:      map[string]Repo{"a/b": {Provider: "github", BaseBranch: "main"}},
		BaseImages: map[string]string{"web": "x@sha256:1"}}
	pub := local.Shared()
	pub.Repos, pub.BaseImages = maps.Clone(pub.Repos), maps.Clone(pub.BaseImages) // Shared() aliases the maps
	pub.Repos["other/svc"] = Repo{Provider: "github"}
	pub.BaseImages["py"] = "y@sha256:2"
	if d := SharedDiff(pub, local); len(d) != 0 {
		t.Fatalf("published-only entries counted: %v", d)
	}
	pub.Repos["a/b"] = Repo{Provider: "bitbucket", BaseBranch: "dev"}
	if d := SharedDiff(pub, local); strings.Join(d, ",") != "repos" {
		t.Fatalf("a changed entry = %v", d)
	}
	local.BaseImages["extra"] = "z@sha256:3"
	if d := SharedDiff(pub, local); strings.Join(d, ",") != "base_images,repos" {
		t.Fatalf("a local-only entry = %v", d)
	}
}
