package task

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
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

// TestReviewPRsValidation covers review_prs' own shape rules (Task 12 of
// the generic-tool plan): 1 to 4 positive, distinct pull request numbers,
// and never together with a follow-up's branch/pr/previous_run. A task
// naming review_prs alone needs no task text either.
//
// Mutation (drop the anyReview case from the "task is required" check,
// i.e. change it back to `if !anyFollowUp`): the last case below (task-less
// review_prs) wrongly fails "task is required".
func TestReviewPRsValidation(t *testing.T) {
	base := func() *Spec {
		return &Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main"}
	}
	cases := []struct {
		name string
		edit func(*Spec)
		want string
	}{
		{"with a follow-up", func(s *Spec) {
			s.ReviewPRs = []int{7}
			s.Branch, s.PR, s.PreviousRun = "fugaro/20260926-221530-a1b2", 7, "20260926-221530-a1b2"
		},
			"review_prs and branch/pr/previous_run don't go together"},
		{"five", func(s *Spec) { s.ReviewPRs = []int{1, 2, 3, 4, 5} }, "1 to 4 pull requests"},
		{"empty but present", func(s *Spec) { s.ReviewPRs = []int{} }, "1 to 4 pull requests"},
		{"zero entry", func(s *Spec) { s.ReviewPRs = []int{0} }, "positive pull request numbers"},
		{"negative entry", func(s *Spec) { s.ReviewPRs = []int{-1} }, "positive pull request numbers"},
		{"duplicate", func(s *Spec) { s.ReviewPRs = []int{3, 3} }, "repeats #3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base()
			c.edit(s)
			if err := s.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %v, want it to mention %q", err, c.want)
			}
		})
	}
	s := base()
	s.ReviewPRs = []int{7, 9}
	if err := s.Validate(); err != nil {
		t.Fatalf("1 to 4 distinct positive review_prs and no task: %v", err)
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

func TestProjectLayerField(t *testing.T) {
	text := "version: 1\nproject: aurora\ngcp_project: proj-1234\n"
	ok := Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/app", Ref: "main", Task: "x",
		ProjectLayer: &ProjectLayer{SHA256: config.LayerSum([]byte(text)), Generation: 7, YAML: text}}
	data, err := ok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data)
	if err != nil || got.ProjectLayer == nil || *got.ProjectLayer != *ok.ProjectLayer {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	over := text + strings.Repeat("#", config.LayerMaxBytes+1-len(text)) // LayerMaxBytes+1 bytes, correct sha
	atCap := text + strings.Repeat("#", config.LayerMaxBytes-len(text))
	capped := ok
	capped.ProjectLayer = &ProjectLayer{SHA256: config.LayerSum([]byte(atCap)), YAML: atCap}
	if err := capped.Validate(); err != nil {
		t.Errorf("a layer of exactly LayerMaxBytes: %v", err)
	}
	for _, tc := range []struct {
		pl   ProjectLayer
		want string
	}{
		{ProjectLayer{SHA256: strings.Repeat("0", 64), YAML: text}, "project_layer.sha256 does not match"},
		{ProjectLayer{SHA256: config.LayerSum(nil)}, "project_layer.yaml must hold the project layer"},
		{ProjectLayer{SHA256: config.LayerSum([]byte(text)), YAML: text, Generation: -1}, "project_layer.generation"},
		{ProjectLayer{SHA256: config.LayerSum([]byte(over)), YAML: over}, "project_layer.yaml must hold the project layer"},
	} {
		bad := ok
		bad.ProjectLayer = &tc.pl
		if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.pl, err, tc.want)
		}
	}
}

// A spec without a layer must marshal exactly as before 0.6.0, so a runner
// that refuses unknown fields still accepts it.
func TestProjectLayerOmittedWhenAbsent(t *testing.T) {
	s := Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/app", Ref: "main", Task: "x"}
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "project_layer") {
		t.Errorf("no layer, yet the task has project_layer: %s", data)
	}
	s.ProjectLayer = &ProjectLayer{SHA256: "x", YAML: "y"}
	data, _ = s.Marshal()
	if !strings.Contains(string(data), `"project_layer"`) || strings.Contains(string(data), "generation") {
		t.Errorf("generation 0 must be omitted, layer present: %s", data)
	}
}

// TestOldRunnerRefusesOAuthTokenField simulates a pre-feature runner's
// strict decode: oldSpec is Spec's shape before this task, copied field for
// field (not re-derived), so this proves what an old binary actually did,
// not what this task wishes it did.
func TestOldRunnerRefusesOAuthTokenField(t *testing.T) {
	spec := Spec{Version: 1, RunID: "20261009-101010-abcd", Repo: "acme/app", Ref: "main", Task: "x", OAuthToken: "acme-personal"}
	data, err := spec.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	type oldSpec struct {
		Version      int           `json:"version"`
		RunID        string        `json:"run_id"`
		Repo         string        `json:"repo"`
		Ref          string        `json:"ref"`
		Workflow     string        `json:"workflow,omitempty"`
		Task         string        `json:"task,omitempty"`
		Branch       string        `json:"branch,omitempty"`
		PR           int           `json:"pr,omitempty"`
		PreviousRun  string        `json:"previous_run,omitempty"`
		Overrides    Overrides     `json:"overrides"`
		RequestedBy  string        `json:"requested_by,omitempty"`
		Batch        string        `json:"batch,omitempty"`
		Recipe       *Recipe       `json:"recipe,omitempty"`
		ProjectLayer *ProjectLayer `json:"project_layer,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var old oldSpec
	if err := dec.Decode(&old); err == nil {
		t.Fatal("an old-shaped decoder accepted oauth_token silently")
	}
}

// TestOAuthTokenOmittedWhenAbsent mirrors TestProjectLayerOmittedWhenAbsent:
// a spec with no oauth_token marshals exactly as before this task, so a
// runner that refuses unknown fields still accepts it.
func TestOAuthTokenOmittedWhenAbsent(t *testing.T) {
	s := Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/app", Ref: "main", Task: "x"}
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "oauth_token") {
		t.Errorf("no token, yet the task has oauth_token: %s", data)
	}
	s.OAuthToken = "acme-personal"
	data, err = s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"oauth_token": "acme-personal"`) {
		t.Errorf("oauth_token set but missing from marshal: %s", data)
	}
	got, err := Parse(data)
	if err != nil || got.OAuthToken != "acme-personal" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

// TestTaskSpecCompressionOffRoundTrips: task.Spec.CompressionOff (fugaro run
// --no-compression) survives a marshal/parse round trip, and a spec that does
// not carry it is byte-identical to what the previous release wrote.
func TestTaskSpecCompressionOffRoundTrips(t *testing.T) {
	s := Spec{Version: 1, RunID: "20261012-100000-abcd", Repo: "owner/repo-a", Ref: "main", CompressionOff: true}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got Spec
	if err := json.Unmarshal(data, &got); err != nil || !got.CompressionOff {
		t.Fatalf("round trip: got %+v, err %v", got, err)
	}
	plain, err := json.Marshal(Spec{Version: 1, RunID: "20261012-100000-abcd", Repo: "owner/repo-a", Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "compression_off") {
		t.Errorf("a false CompressionOff was not omitted: %s", plain)
	}
}

// TestOldTaskParserRefusesCompressionOffField simulates a pre-feature
// runner's strict decode: oldSpec is Spec's shape before this task, copied
// field for field (not re-derived), so this proves what an old binary
// actually did, not what this task wishes it did.
func TestOldTaskParserRefusesCompressionOffField(t *testing.T) {
	spec := Spec{Version: 1, RunID: "20261012-100000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "x", CompressionOff: true}
	data, err := spec.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	type oldSpec struct {
		Version      int           `json:"version"`
		RunID        string        `json:"run_id"`
		Repo         string        `json:"repo"`
		Ref          string        `json:"ref"`
		Workflow     string        `json:"workflow,omitempty"`
		Task         string        `json:"task,omitempty"`
		Title        string        `json:"title,omitempty"`
		PlanTask     string        `json:"plan_task,omitempty"`
		Branch       string        `json:"branch,omitempty"`
		PR           int           `json:"pr,omitempty"`
		PreviousRun  string        `json:"previous_run,omitempty"`
		Overrides    Overrides     `json:"overrides"`
		RequestedBy  string        `json:"requested_by,omitempty"`
		Batch        string        `json:"batch,omitempty"`
		Recipe       *Recipe       `json:"recipe,omitempty"`
		ReviewPRs    []int         `json:"review_prs,omitempty"`
		ProjectLayer *ProjectLayer `json:"project_layer,omitempty"`
		OAuthToken   string        `json:"oauth_token,omitempty"`
		Mode         string        `json:"mode,omitempty"`
		Rigor        rigor.Mode    `json:"rigor,omitempty"`
		Paths        []string      `json:"paths,omitempty"`
		Inputs       []InputFile   `json:"inputs,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var old oldSpec
	if err := dec.Decode(&old); err == nil {
		t.Fatal("a pre-feature Spec decoder accepted compression_off silently")
	}
}

// TestOAuthTokenValidate pins the same rule schemas/task.schema.json
// enforces (config.ClaudeAccountNameRE and config.MaxClaudeAccountNameLen,
// and "default" refused separately): the shared testdata/task/invalid
// corpus relies on Validate rejecting these exact shapes, not just the
// schema. The 61- and 32-character cases both match ClaudeAccountNameRE
// (its {0,30} repetitions may each consume one or two characters, so the
// pattern alone admits up to 61 characters) and are refused only by the
// explicit length check, not the regex — this is what a maxLength keyword
// missing from the schema, or a missing len() check here, would miss.
func TestOAuthTokenValidate(t *testing.T) {
	ok := Spec{Version: 1, RunID: "20261009-101010-abcd", Repo: "acme/app", Ref: "main", Task: "x"}
	for _, name := range []string{
		"", "a", "acme-personal", "a-b-c0",
		strings.Repeat("a", config.MaxClaudeAccountNameLen),              // 31 plain characters: the boundary
		"a" + strings.Repeat("-a", (config.MaxClaudeAccountNameLen-1)/2), // 31 characters, dash-heavy: the boundary
	} {
		s := ok
		s.OAuthToken = name
		if err := s.Validate(); err != nil {
			t.Errorf("oauth_token %q (%d chars): %v", name, len(name), err)
		}
	}
	for _, name := range []string{
		"default", "Bad_Name", "a-", "a--b",
		"a" + strings.Repeat("-a", 30),       // 61 characters, matches the pattern, over the length cap
		"a" + strings.Repeat("-a", 15) + "b", // 32 characters, matches the pattern, one over the cap
	} {
		s := ok
		s.OAuthToken = name
		if err := s.Validate(); err == nil {
			t.Errorf("oauth_token %q (%d chars): accepted, want refused", name, len(name))
		}
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

// TestSpecMode covers Spec.Mode (artifact-runs plan Task 2): the launcher
// writes the resolved recipe's mode, "artifact" today, and nothing at all
// for implement or review, so every existing spec stays byte-identical to
// what the previous release wrote. A value that is not a recipe mode is
// refused at parse, before the runner could select a run shape from a mode
// nothing implements.
//
// Mutation (drop the mode switch from Validate): "sizzle" parses and this
// test fails.
func TestSpecMode(t *testing.T) {
	for _, mode := range []string{"", "implement", "review", "artifact"} {
		in := `{"version":1,"run_id":"20261015-120000-a1b2","repo":"acme/app","ref":"main","task":"x","mode":"` + mode + `"}`
		spec, err := Parse([]byte(in))
		if err != nil {
			t.Errorf("mode %q: %v", mode, err)
			continue
		}
		if spec.Mode != mode {
			t.Errorf("mode %q parsed as %q", mode, spec.Mode)
		}
	}
	for _, mode := range []string{"sizzle", "ARTIFACT", "impl", " artifacts", "artifact "} {
		in := `{"version":1,"run_id":"20261015-120000-a1b2","repo":"acme/app","ref":"main","task":"x","mode":"` + mode + `"}`
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), "mode") {
			t.Errorf("mode %q: err = %v, want a refusal naming the mode", mode, err)
		}
	}
	// The empty mode is omitted, not written as "": an implement or review
	// spec is unchanged on the wire.
	data, err := json.Marshal(&Spec{Version: 1, RunID: "20261015-120000-a1b2", Repo: "acme/app", Ref: "main", Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"mode"`) {
		t.Fatalf("an empty mode is written: %s", data)
	}
}

// TestSpecInputsValidate is the decoder's own arm for the attached-files list
// (design artifact-runs.md §4, plan Task 6): the three fields are all
// required and structural — a name, a size that cannot be negative, and a
// sha256 in the one spelling the launcher writes. It is deliberately NOT the
// name's shape: "..", an absolute path, a NUL and a duplicate are refused
// where a name becomes a filesystem path (the launcher's attachPrepared, then
// the runner's downloadInputs), because a decoder that refused them first
// would make those checks unreachable. TestInputNameProblem below pins the
// list they use.
//
// Mutation (drop the inputs loop from Validate): every case below parses and
// this test fails.
func TestSpecInputsValidate(t *testing.T) {
	const head = `{"version":1,"run_id":"20261015-120000-a1b2","repo":"acme/app","ref":"main","task":"x","mode":"artifact"`
	sha := strings.Repeat("a", 64)
	for _, c := range []struct {
		name string
		body string
		want string
	}{
		{"one input", `,"inputs":[{"name":"shot.png","bytes":1024,"sha256":"` + sha + `"}]`, ""},
		{"none", `,"inputs":[]`, ""},
		{"absent", ``, ""},
		{"no name", `,"inputs":[{"bytes":1,"sha256":"` + sha + `"}]`, "name is required"},
		// A name of nothing but whitespace is no name HERE, and it must be no
		// name in InputNameProblem too (the case above): the launcher writes
		// this task and the runner reads it back, so a name this refuses is a
		// name the launcher must refuse before it uploads the file.
		{"a whitespace-only name", `,"inputs":[{"name":"   ","bytes":1,"sha256":"` + sha + `"}]`, "name is required"},
		{"negative bytes", `,"inputs":[{"name":"a.png","bytes":-1,"sha256":"` + sha + `"}]`, "negative"},
		{"no sha256", `,"inputs":[{"name":"a.png","bytes":1}]`, `sha256 "" must be`},
		{"short sha256", `,"inputs":[{"name":"a.png","bytes":1,"sha256":"abc"}]`, "sha256"},
		{"upper-case sha256", `,"inputs":[{"name":"a.png","bytes":1,"sha256":"` + strings.Repeat("A", 64) + `"}]`, "sha256"},
		{"a name that escapes", `,"inputs":[{"name":"../x.png","bytes":1,"sha256":"` + sha + `"}]`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(head + c.body + `}`))
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("err = %v, want the spec accepted", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// TestInputNameProblem is the shape list the launcher and the runner share
// (design §8.2): what a name may not be, as a filesystem path under inputs/.
// The empty name and the length limit are here with the rest because this is
// the one place the rule is written down; the decoder above refuses an absent
// or empty name first, so the runner sees this list only for the shapes
// Validate lets through.
//
// Mutation (drop a case from InputNameProblem): the corresponding entry below
// reports "" and this test fails.
func TestInputNameProblem(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"shot.png", ""},
		{"a/b/shot.png", ""},
		{"inputs/shot.png", ""},
		{"", "is empty"},
		{"   ", "is only whitespace"},
		{" \t ", "is only whitespace"},
		// Whitespace AROUND a name is a name: the rule is for a string that
		// holds nothing but whitespace, not for whitespace in one.
		{" shot.png", ""},
		{"shot .png", ""},
		{strings.Repeat("a", MaxInputNameBytes), ""},
		{strings.Repeat("a", MaxInputNameBytes+1), "is longer than 200 bytes"},
		{"/etc/passwd", "is an absolute path"},
		{"../escape.png", `has a ".." path element`},
		{"a/../../b.png", `has a ".." path element`},
		{"a/./b.png", `has a "." path element`},
		{"a//b.png", "has an empty path element"},
		{"trailing/", "has an empty path element"},
		{"a\x00b.png", "holds a NUL or a newline"},
		{"a\nb.png", "holds a NUL or a newline"},
	} {
		if got := InputNameProblem(c.name); got != c.want {
			t.Errorf("InputNameProblem(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// A plan task id (Spec.PlanTask, fugaro run --plan-task) is a display-only
// join hint: it says which task of which plan a run is working on, so the
// console can join the run to a node of that plan's graph. It is written by
// a launcher — job-writable text — so it is shape-validated on write and on
// read, never joined into a path and never an authorization input (console
// dependency graph plan, constraint C3).
//
// Mutation (drop the shape check from Validate): a hand-written task.json
// carrying "../etc/passwd" as its plan task is accepted, and the field that
// exists to be shown as text starts carrying a path.
func TestPlanTaskShape(t *testing.T) {
	for _, ok := range []string{
		"setup-wizard/W1.3",
		"setup-wizard/GV1.2",
		// A plan under docs/plans/ is a two-segment plan id, so its tasks
		// are three segments (the join rule GV1.6 names).
		"plans/2026-10-15-desktop-console-v2/GV1.2",
		"a/b",
		"acme_plan/task-1.2",
	} {
		if got := PlanTaskProblem(ok); got != "" {
			t.Errorf("PlanTaskProblem(%q) = %q, want it accepted", ok, got)
		}
	}
	for _, c := range []struct{ name, want string }{
		{"", "is empty"},
		{"setup-wizard", "must name a task in a plan"},
		{"setup-wizard/", "has an empty path element"},
		{"/setup-wizard/W1.3", "is an absolute path"},
		{"setup-wizard/../etc/passwd", `has a ".." path element`},
		{"setup-wizard/./W1.3", `has a "." path element`},
		{"setup-wizard/W1.3\x1b[31m", "holds a control character"},
		{"setup-wizard/W1.3\x00", "holds a NUL or a newline"},
		{"setup-wizard/W1.3\nx", "holds a NUL or a newline"},
		{"setup wizard/W1.3", "letters, digits, '.', '_' and '-'"},
		{"setup-wizard/W1.3;rm -rf /", "letters, digits, '.', '_' and '-'"},
		{strings.Repeat("a", MaxPlanTaskBytes+1) + "/b", "is longer than 128 bytes"},
	} {
		if got := PlanTaskProblem(c.name); !strings.Contains(got, c.want) {
			t.Errorf("PlanTaskProblem(%q) = %q, want it to mention %q", c.name, got, c.want)
		}
	}
	// The cap counts bytes, and a value exactly at it is accepted.
	if id := "a/" + strings.Repeat("b", MaxPlanTaskBytes-2); len(id) != MaxPlanTaskBytes || PlanTaskProblem(id) != "" {
		t.Errorf("PlanTaskProblem of a %d-byte id = %q, want it accepted", len(id), PlanTaskProblem(id))
	}
	// The same list is what Validate applies to a stored task.
	s := Spec{Version: 1, RunID: "20261024-120000-abcd", Repo: "acme/app", Ref: "main", Task: "x"}
	if err := s.Validate(); err != nil {
		t.Fatalf("no plan task: %v", err)
	}
	s.PlanTask = "setup-wizard/W1.3"
	if err := s.Validate(); err != nil {
		t.Fatalf("a shaped plan task: %v", err)
	}
	s.PlanTask = "setup-wizard/../escape"
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "task spec: plan_task") {
		t.Fatalf("Validate() = %v, want the plan_task refusal", err)
	}
	// And an absent one writes no field, so a spec from a launcher that
	// never heard of the flag is byte-identical to before (the runner's
	// decoder refuses unknown fields).
	s.PlanTask = ""
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "plan_task") {
		t.Fatalf("a spec with no plan task carries the field:\n%s", data)
	}
	s.PlanTask = "setup-wizard/W1.3"
	data, err = s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data)
	if err != nil || got.PlanTask != "setup-wizard/W1.3" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}
