package task

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

func TestNewRunID(t *testing.T) {
	now := time.Date(2026, 9, 26, 22, 15, 30, 0, time.FixedZone("x", 3*3600))
	id, err := NewRunID(now, bytes.NewReader([]byte{0xab, 0xcd}))
	if err != nil {
		t.Fatal(err)
	}
	if id != "20260926-191530-abcd" {
		t.Fatalf("id = %q (must be UTC)", id)
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("Acme/Server"); got != "acme-server-ce7d8f35" {
		t.Fatalf("Slug = %q", got)
	}
	if got := Slug("acme/app"); got != "acme-app-5f89da04" {
		t.Fatalf("Slug = %q", got)
	}
}

var slugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?-[0-9a-f]{8}$`)

// Every spelling of one repository gives one slug.
func TestSlugIsCanonical(t *testing.T) {
	spellings := []string{
		"acme/app", "Acme/App", "ACME/APP", "acme/app.git", "Acme/App.git", "/acme/app/", "acme/app/",
		"https://bitbucket.org/acme/app", "https://bitbucket.org/acme/app.git", "https://Bitbucket.org/Acme/App.git/",
		"https://user@bitbucket.org/acme/app.git", "http://bitbucket.org/acme/app",
		"ssh://git@bitbucket.org/acme/app.git", "ssh://git@bitbucket.org:7999/acme/app.git",
		"git@bitbucket.org:acme/app.git", "git@bitbucket.org:Acme/App", "bitbucket.org:acme/app.git",
		" acme/app ",
	}
	want := Slug("acme/app")
	for _, s := range spellings {
		if CanonicalRepo(s) != "acme/app" {
			t.Errorf("CanonicalRepo(%q) = %q", s, CanonicalRepo(s))
		}
		if got := Slug(s); got != want {
			t.Errorf("Slug(%q) = %q, want %q", s, got, want)
		}
	}
	if got := CanonicalRepo("https://gitlab.example/Group/Sub/Repo.git"); got != "group/sub/repo" {
		t.Errorf("nested group: %q", got)
	}
}

// Distinct repositories never share a slug: the bucket prefixes it names
// are the IAM boundary between them.
func TestSlugIsInjective(t *testing.T) {
	long := strings.Repeat("x", 60)
	pairs := [][2]string{
		{"acme/app-web", "acme-app/web"},
		{"acme/app.web", "acme/app-web"},
		{"acme/app_web", "acme/app-web"},
		{"acme.app/web", "acme-app/web"},
		{"acme_app/web", "acme-app/web"},
		{"acme/app--web", "acme/app-web"},
		{"acme/app", "acme/app.git.git"},
		{"acme/app.v2", "acme/app-v2"},
		{"org/sub/repo", "org-sub/repo"},
		{"org/sub/repo", "org/sub-repo"},
		{"org/sub/repo", "org/sub/repo/x"},
		{long + "/a", long + "/b"},
		{"acme/" + long + "1", "acme/" + long + "2"},
	}
	for _, p := range pairs {
		if a, b := Slug(p[0]), Slug(p[1]); a == b {
			t.Errorf("Slug(%q) == Slug(%q) == %q", p[0], p[1], a)
		}
	}
	seen := map[string]string{}
	parts := []string{"a", "b", "a-b", "a.b", "a_b", "ab", "a--b", "a-", "a.git"}
	for _, o := range parts {
		for _, n := range parts {
			r := "x" + o + "/" + n
			c := CanonicalRepo(r)
			s := Slug(r)
			if prev, dup := seen[s]; dup && prev != c {
				t.Errorf("%q from both %q and %q", s, prev, c)
			}
			seen[s] = c
		}
	}
}

func TestSlugShape(t *testing.T) {
	for _, r := range []string{"acme/app", "Acme/Server", "acme/my_service", "acme/app.v2", "org/sub/repo-x",
		strings.Repeat("verylong", 20) + "/" + strings.Repeat("name", 20), "-/-", "a/b"} {
		s := Slug(r)
		if len(s) > maxSlugReadable+9 || !slugRE.MatchString(s) {
			t.Errorf("Slug(%q) = %q (%d)", r, s, len(s))
		}
	}
}

func FuzzSlugIsInjective(f *testing.F) {
	f.Add("acme/app-web", "acme-app/web")
	f.Add("acme/app", "Acme/App.git")
	f.Fuzz(func(t *testing.T, a, b string) {
		if CanonicalRepo(a) == CanonicalRepo(b) {
			if Slug(a) != Slug(b) {
				t.Errorf("same repository %q, %q: slugs %q, %q", a, b, Slug(a), Slug(b))
			}
			return
		}
		if Slug(a) == Slug(b) {
			t.Errorf("Slug(%q) == Slug(%q) == %q", a, b, Slug(a))
		}
	})
}

func TestCorpus(t *testing.T) {
	valid, _ := filepath.Glob("../../testdata/task/valid/*.json")
	invalid, _ := filepath.Glob("../../testdata/task/invalid/*.json")
	if len(valid) == 0 || len(invalid) == 0 {
		t.Fatal("missing corpus")
	}
	for _, f := range valid {
		data, _ := os.ReadFile(f)
		if _, err := Parse(data); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	for _, f := range invalid {
		data, _ := os.ReadFile(f)
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: parsed, want error", f)
		}
	}
}

func TestParseErrorMessages(t *testing.T) {
	cases := map[string]string{
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"acme/server","ref":"main"}`:                                      "task is required",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"acme","ref":"main","task":"x"}`:                                  "repo",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"a/b","ref":"main","task":"x","pr":3}`:                            "must be set together",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"a/b","ref":"","task":"x"}`:                                       "ref is required",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"a/b","ref":"m","task":"x","overrides":{"total_timeout":"soon"}}`: "total_timeout",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"a/b","ref":"m","task":"x","overrides":{"max_budget_usd":0}}`:     "max_budget_usd must be greater than 0",
	}
	for in, want := range cases {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%s) err = %v, want it to mention %q", in, err, want)
		}
	}
}

func TestApply(t *testing.T) {
	rounds, budget := 4, 10.0
	s := &Spec{Overrides: Overrides{ReviewRounds: &rounds, MaxBudgetUSD: &budget, Model: "m", TotalTimeout: "45m"}}
	cfg := &config.Config{}
	w := &config.Workflow{Timeouts: config.Timeouts{FinalizeReserve: config.Duration{Duration: 5 * time.Minute}}}
	if err := s.Apply(cfg, w); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.ReviewRounds != 4 || cfg.Agent.MaxBudgetUSD != 10 || cfg.Agent.Model != "m" || w.Timeouts.Total.Duration != 45*time.Minute {
		t.Fatalf("not applied: %+v %+v", cfg.Agent, w.Timeouts)
	}
	s.Overrides = Overrides{TotalTimeout: "4m"}
	if err := s.Apply(cfg, w); err == nil || !strings.Contains(err.Error(), "finalize_reserve") {
		t.Fatalf("Apply with total below reserve err = %v", err)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	data, _ := os.ReadFile("../../testdata/task/valid/new.json")
	s, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if *s2.Overrides.ReviewRounds != 3 || s2.Task != s.Task || s2.IsFollowUp() {
		t.Fatalf("round trip lost data: %+v", s2)
	}
}

func TestBatch(t *testing.T) {
	s := &Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main", Task: "x", Batch: "tuesday-cleanup.2"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"Tuesday", "-x", "a b", strings.Repeat("a", 64)} {
		s.Batch = bad
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "batch") {
			t.Errorf("batch %q: %v", bad, err)
		}
	}
}
