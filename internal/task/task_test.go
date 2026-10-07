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
	"github.com/dimipaun/fugaro/internal/recipe"
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

func mustSlug(t testing.TB, provider, repo string) string {
	t.Helper()
	s, err := Slug(provider, repo)
	if err != nil {
		t.Fatalf("Slug(%q, %q): %v", provider, repo, err)
	}
	return s
}

// Pinned: a slug names existing bucket data, so it must never drift.
func TestSlug(t *testing.T) {
	for _, c := range []struct{ provider, repo, want string }{
		{"bitbucket", "acme/app", "acme-app-e5c4c0c8a3d698ee"},
		{"github", "acme/app", "acme-app-dc4d4e59e884fc44"},
		{"bitbucket", "acme/fugaro-sandbox", "acme-fugaro-sandbox-aaaac508c040817e"},
		{"github", "Acme/Server", "acme-server-b198379bdcda586d"},
	} {
		if got := mustSlug(t, c.provider, c.repo); got != c.want {
			t.Errorf("Slug(%q, %q) = %q, want %q", c.provider, c.repo, got, c.want)
		}
	}
}

var slugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?-[0-9a-f]{16}$`)

// Every spelling of one repository gives one slug.
func TestSlugIsCanonical(t *testing.T) {
	want := mustSlug(t, "bitbucket", "acme/app")
	for _, s := range []string{"acme/app", "Acme/App", "ACME/APP", "acme/app.git", "Acme/App.git", "acme/APP.git"} {
		if c, err := CanonicalRepo(s); err != nil || c != "acme/app" {
			t.Errorf("CanonicalRepo(%q) = %q, %v", s, c, err)
		}
		if got := mustSlug(t, "bitbucket", s); got != want {
			t.Errorf("Slug(%q) = %q, want %q", s, got, want)
		}
	}
	if c, err := CanonicalRepo("Group/Sub/Repo.git"); err != nil || c != "group/sub/repo" {
		t.Errorf("nested group: %q, %v", c, err)
	}
}

func TestCanonicalRepoRejectsNonPaths(t *testing.T) {
	for _, bad := range []string{
		"", "acme", "acme/", "/acme/app", "acme/app/", "acme//app", " acme/app",
		"acme/.git", "acme/app/.git", "acme/..", "../app", "acme/./app",
		"https://bitbucket.org/acme/app.git", "git@bitbucket.org:acme/app.git", "ssh://git@h/acme/app",
		"acme/app%2Fx", "acme/K\u212aapp", // KELVIN SIGN must not fold to k
	} {
		if c, err := CanonicalRepo(bad); err == nil {
			t.Errorf("CanonicalRepo(%q) = %q, want an error", bad, c)
		}
		if s, err := Slug("github", bad); err == nil {
			t.Errorf("Slug(%q) = %q, want an error", bad, s)
		}
	}
	for _, bad := range []string{"", "GitHub", "git hub", "-x"} {
		if _, err := Slug(bad, "acme/app"); err == nil {
			t.Errorf("Slug accepted provider %q", bad)
		}
	}
}

// Distinct repositories never share a slug by accident: the bucket
// prefixes it names are the IAM boundary between them.
func TestSlugIsInjective(t *testing.T) {
	long := strings.Repeat("x", 60)
	pairs := [][2][2]string{
		{{"github", "acme/app-web"}, {"github", "acme-app/web"}},
		{{"github", "acme/app.web"}, {"github", "acme/app-web"}},
		{{"github", "acme/app_web"}, {"github", "acme/app-web"}},
		{{"github", "acme.app/web"}, {"github", "acme-app/web"}},
		{{"github", "acme_app/web"}, {"github", "acme-app/web"}},
		{{"github", "acme/app--web"}, {"github", "acme/app-web"}},
		{{"github", "acme/app"}, {"github", "acme/app.git.git"}},
		{{"github", "acme/app.v2"}, {"github", "acme/app-v2"}},
		{{"github", "org/sub/repo"}, {"github", "org-sub/repo"}},
		{{"github", "org/sub/repo"}, {"github", "org/sub-repo"}},
		{{"github", "org/sub/repo"}, {"github", "org/sub/repo/x"}},
		{{"github", long + "/a"}, {"github", long + "/b"}},
		{{"github", "acme/" + long + "1"}, {"github", "acme/" + long + "2"}},
		{{"github", "acme/app"}, {"bitbucket", "acme/app"}},
		{{"github", "acme/app"}, {"fake", "acme/app"}},
	}
	for _, p := range pairs {
		if a, b := mustSlug(t, p[0][0], p[0][1]), mustSlug(t, p[1][0], p[1][1]); a == b {
			t.Errorf("Slug%q == Slug%q == %q", p[0], p[1], a)
		}
	}
	seen := map[string]string{}
	parts := []string{"a", "b", "a-b", "a.b", "a_b", "ab", "a--b", "a-", "a.git"}
	for _, o := range parts {
		for _, n := range parts {
			r := "x" + o + "/" + n
			c, err := CanonicalRepo(r)
			if err != nil {
				t.Fatalf("%s: %v", r, err)
			}
			s := mustSlug(t, "github", r)
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
		s := mustSlug(t, "bitbucket", r)
		if len(s) > 63 || !slugRE.MatchString(s) {
			t.Errorf("Slug(%q) = %q (%d)", r, s, len(s))
		}
	}
}

func FuzzSlugIsInjective(f *testing.F) {
	f.Add("github", "acme/app-web", "github", "acme-app/web")
	f.Add("github", "acme/app", "bitbucket", "Acme/App.git")
	f.Fuzz(func(t *testing.T, p1, a, p2, b string) {
		s1, err1 := Slug(p1, a)
		s2, err2 := Slug(p2, b)
		if err1 != nil || err2 != nil {
			return
		}
		c1, _ := CanonicalRepo(a)
		c2, _ := CanonicalRepo(b)
		if (p1 == p2 && c1 == c2) != (s1 == s2) {
			t.Errorf("Slug(%q,%q) = %q, Slug(%q,%q) = %q", p1, a, s1, p2, b, s2)
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
	if cfg.Agent.ReviewRounds != 4 || cfg.Agent.MaxBudgetUSD != 10 || cfg.Agent.ModelFor(config.RoleCoder) != "m" || cfg.Agent.ModelFor(config.RoleReviewer) != "m" || w.Timeouts.Total.Duration != 45*time.Minute {
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

func TestFollowUpBranchMustBeRunBranch(t *testing.T) {
	s := &Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main", PR: 3, PreviousRun: "20260925-000000-0000"}
	s.Branch = "fugaro/20260925-000000-0000"
	if err := s.Validate(); err != nil {
		t.Fatalf("a run branch: %v", err)
	}
	for _, bad := range []string{"main", "fugaro/x", "fugaro/20260925-000000-0000/x", "fugaro/20260925-000000-000G", "x/fugaro/20260925-000000-0000"} {
		s.Branch = bad
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), "must be fugaro/<run-id>") {
			t.Errorf("branch %q: err = %v", bad, err)
		}
	}
}

func TestBranchRunID(t *testing.T) {
	if id, ok := BranchRunID("fugaro/20260925-000000-0a1b"); !ok || id != "20260925-000000-0a1b" {
		t.Fatalf("BranchRunID = %q, %v", id, ok)
	}
	for _, bad := range []string{"", "main", "fugaro/x", "fugaro/20260925-000000-0a1b/x", "20260925-000000-0a1b"} {
		if id, ok := BranchRunID(bad); ok || id != "" {
			t.Errorf("BranchRunID(%q) = %q, %v", bad, id, ok)
		}
	}
}

// overrides.model is the coder's model; the reviewer keeps its own.
func TestTaskOverrideModelIsCoder(t *testing.T) {
	s := &Spec{Overrides: Overrides{Model: "claude-opus-5-5"}}
	cfg := &config.Config{Agent: config.Agent{Model: "claude-sonnet-5-5", Models: config.ModelRoles{Reviewer: "claude-haiku-4-5"}}}
	if err := s.Apply(cfg, &config.Workflow{}); err != nil {
		t.Fatal(err)
	}
	a := cfg.Agent
	if a.ModelFor(config.RoleCoder) != "claude-opus-5-5" || a.ModelFor(config.RoleReviewer) != "claude-haiku-4-5" || a.Model != "claude-sonnet-5-5" {
		t.Fatalf("agent = %+v", a)
	}
}

// With no per-role models the override reaches every stage, as it always
// has; the budget off must not change which model reviews.
func TestTaskOverrideModelReachesAllStagesWithoutRoleModels(t *testing.T) {
	s := &Spec{Overrides: Overrides{Model: "claude-opus-5-5"}}
	cfg := &config.Config{Agent: config.Agent{Model: "claude-sonnet-5-5"}}
	if err := s.Apply(cfg, &config.Workflow{}); err != nil {
		t.Fatal(err)
	}
	a := cfg.Agent
	if a.ModelFor(config.RoleCoder) != "claude-opus-5-5" || a.ModelFor(config.RoleReviewer) != "claude-opus-5-5" {
		t.Fatalf("agent = %+v", a)
	}
}

func TestRecipeValidate(t *testing.T) {
	big16K := strings.Repeat("#", recipe.MaxBytes)
	const text = "version: 1\nname: solo\nsteps:\n  - review: {}\n"
	const sum = "8a9f3fd7432aabb347069c3108bdeddd8b834a55ebbeb3ba4af8ff8dd54754e0"
	base := func(r *Recipe) *Spec {
		return &Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main", Task: "x", Recipe: r}
	}
	for _, r := range []*Recipe{
		nil,
		{Name: "solo", Source: "catalog", SHA256: sum, YAML: text},
		{Name: "solo", Source: "project", SHA256: sum, YAML: text},
		{Name: "solo", Source: "repo"},
		{Name: "solo", Source: "catalog", SHA256: recipe.Sum([]byte(big16K)), YAML: big16K},
	} {
		if err := base(r).Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	for _, tc := range []struct {
		want string
		r    *Recipe
	}{
		{"is not a recipe name", &Recipe{Name: "Solo", Source: "catalog", SHA256: sum, YAML: text}},
		{"must be repo, project", &Recipe{Name: "solo", Source: "bucket", SHA256: sum, YAML: text}},
		{"does not match recipe.yaml", &Recipe{Name: "solo", Source: "project", SHA256: strings.Repeat("0", 64), YAML: text}},
		{"must hold the recipe", &Recipe{Name: "solo", Source: "catalog"}},
		{"must be empty", &Recipe{Name: "solo", Source: "repo", YAML: text}},
		{"must be empty", &Recipe{Name: "solo", Source: "repo", SHA256: sum}},
		{"is not a recipe name", &Recipe{Name: "", Source: "catalog", SHA256: sum, YAML: text}},
		{"does not match", &Recipe{Name: "solo", Source: "project", SHA256: strings.ToUpper(sum), YAML: text}},
		{"does not match recipe", &Recipe{Name: "solo", Source: "project", SHA256: sum[:63], YAML: text}},
		{"at most 16384 bytes", &Recipe{Name: "solo", Source: "catalog", SHA256: recipe.Sum([]byte(big16K + "x")), YAML: big16K + "x"}},
	} {
		if err := base(tc.r).Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.r, err, tc.want)
		}
	}
	// A spec carrying a recipe survives Marshal and Parse.
	in := base(&Recipe{Name: "solo", Source: "project", SHA256: sum, YAML: text})
	raw, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(raw)
	if err != nil || out.Recipe == nil || *out.Recipe != *in.Recipe {
		t.Fatalf("round trip: %+v, %v", out, err)
	}
	// A task.json written before recipes still parses (no recipe: default).
	s, err := Parse([]byte(`{"version":1,"run_id":"20260926-221530-a1b2","repo":"acme/app","ref":"main","task":"x","overrides":{}}`))
	if err != nil || s.Recipe != nil {
		t.Fatalf("old spec: %+v, %v", s, err)
	}
}
