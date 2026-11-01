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

// TestSpecRefusesReservedAccountName: oauth_token may not name the account
// the credentials bundle reserves for the platform's own git credential
// (config.ReservedClaudeAccountName), the same rule config.Validate applies
// to fugaro.yaml's agent.oauth_token and the schemas to both. The name passes
// the shape rules beside it, so the reservation is the only thing refusing it,
// and the refusal is worded with the one sentence the config surfaces answer
// that name with — never the name itself, which is untrusted text printed to
// a terminal.
func TestSpecRefusesReservedAccountName(t *testing.T) {
	ok := Spec{Version: 1, RunID: "20261009-101010-abcd", Repo: "acme/app", Ref: "main", Task: "x"}
	ok.OAuthToken = "acme-personal"
	if err := ok.Validate(); err != nil {
		t.Fatalf("a named account was refused: %v", err)
	}
	if !config.ClaudeAccountNameRE.MatchString(config.BitbucketSecret) || len(config.BitbucketSecret) > config.MaxClaudeAccountNameLen {
		t.Fatalf("%s is refused by a shape check, so this test proves nothing about the reservation", config.BitbucketSecret)
	}
	s := ok
	s.OAuthToken = config.BitbucketSecret
	err := s.Validate()
	if err == nil {
		t.Fatalf("oauth_token: %s was accepted: the reserved name is one the run must refuse", config.BitbucketSecret)
	}
	if want := config.ValidClaudeAccountRef(config.BitbucketSecret); !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not carry the one reserved-name sentence (%q): %v", want, err)
	}
	if strings.Contains(err.Error(), config.BitbucketSecret) {
		t.Errorf("the refusal echoes the name it refuses: %v", err)
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

// TestBaseBranchValidated: a task's base_branch is the base the launcher
// resolved from the local config and the project's published policy, which
// the runner uses only when the fugaro.yaml at the ref sets no
// git.base_branch. It reaches git, Docker build arguments and Cloud Build,
// so it is held to the same rule fugaro.yaml's own key is
// (config.ValidBranchName) on read and on the launcher's own write.
func TestBaseBranchValidated(t *testing.T) {
	const tmpl = `{"version":1,"run_id":"20260926-221530-a1b2","repo":"acme/app","ref":"main","task":"x"`
	s, err := Parse([]byte(tmpl + `,"base_branch":"master"}`))
	if err != nil || s.BaseBranch != "master" {
		t.Fatalf("Parse = %+v, %v", s, err)
	}
	s, err = Parse([]byte(tmpl + "}"))
	if err != nil || s.BaseBranch != "" {
		t.Fatalf("no base_branch: %+v, %v", s, err)
	}
	for _, ok := range []string{"master", "release/1.2", "dev_x"} {
		if _, err := Parse([]byte(tmpl + `,"base_branch":"` + ok + `"}`)); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	// An empty value is indistinguishable from an absent key (omitempty), and
	// means the same to the runner: the file's own resolution, then main.
	for _, bad := range []string{"--upload-pack=x", "a..b", "$(id)", "x y", "/x"} {
		if _, err := Parse([]byte(tmpl + `,"base_branch":"` + bad + `"}`)); err == nil || !strings.Contains(err.Error(), "base_branch") {
			t.Errorf("%q: err = %v, want it to mention base_branch", bad, err)
		}
	}
}

// TestDigestRunsRefusedWithoutDigestRecipe covers the decoder's own arm for a
// report run's manifest (task-governance T3.6, Spec.DigestRuns): the run IDs
// the launching CLI listed, which the digest step reads one record at a time.
// A manifest is accepted only under a recipe whose first step is digest — the
// only step that reads it — and every ID is shape-checked, because each one
// names an object the job will read.
//
// The repository-sourced case is the one this package cannot judge: such a
// task carries its recipe's name and no text, so the launcher judges the file
// it resolved (internal/cli/run_digest_manifest.go) and the runner resolves
// the same name at the task's ref. Refusing it here would break the one
// launcher that writes this field.
//
// Mutation (drop the recipe switch from Validate): a manifest under
// recipe solo parses and this test fails.
func TestDigestRunsRefusedWithoutDigestRecipe(t *testing.T) {
	digestText, ok := recipe.CatalogText("run-digest")
	if !ok {
		t.Fatal("run-digest is not in the catalog")
	}
	const soloText = "version: 1\nname: solo\nsteps:\n  - review: {}\n"
	digest := &Recipe{Name: "run-digest", Source: "catalog", SHA256: recipe.Sum(digestText), YAML: string(digestText)}
	solo := &Recipe{Name: "solo", Source: "catalog", SHA256: recipe.Sum([]byte(soloText)), YAML: soloText}
	base := func(r *Recipe, runs ...string) *Spec {
		return &Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main", Task: "x",
			Recipe: r, DigestRuns: runs}
	}
	if err := base(digest, "20260927-100000-abcd", "20260926-090000-1234").Validate(); err != nil {
		t.Errorf("a report run's manifest: %v", err)
	}
	// A repository recipe carries no text: accepted, judged where it is read.
	if err := base(&Recipe{Name: "reports", Source: "repo"}, "20260927-100000-abcd").Validate(); err != nil {
		t.Errorf("a repository recipe: %v", err)
	}
	for _, tc := range []struct {
		name string
		spec *Spec
		want string
	}{
		{"no recipe at all", base(nil, "20260927-100000-abcd"), "needs a recipe whose first step is digest"},
		{"a review recipe", base(solo, "20260927-100000-abcd"), "needs a recipe whose first step is digest"},
		{"recipe text that is not a recipe", base(&Recipe{Name: "solo", Source: "catalog", SHA256: recipe.Sum([]byte("x")), YAML: "x"}, "20260927-100000-abcd"), "needs a recipe whose first step is digest"},
		{"not a run ID", base(digest, "yesterday"), "must look like 20260926-221530-a1b2"},
		{"an empty ID", base(digest, ""), "must look like 20260926-221530-a1b2"},
		{"too many", base(digest, manyIDs(MaxDigestRuns+1)...), "at most 5000 runs"}} {
		err := tc.spec.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	// The field is omitted when there is no manifest, so a spec that is not a
	// report run is byte-identical to what the previous release wrote, and a
	// stored one parses back.
	data, err := json.Marshal(base(nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "digest_runs") {
		t.Fatalf("an absent manifest is written: %s", data)
	}
	raw, err := json.Marshal(base(digest, "20260927-100000-abcd"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(raw)
	if err != nil || len(out.DigestRuns) != 1 || out.DigestRuns[0] != "20260927-100000-abcd" {
		t.Fatalf("round trip: %+v, %v", out, err)
	}
}

// manyIDs is n run IDs of the one shape, for the bound's own case.
func manyIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "20260927-100000-abcd"
	}
	return ids
}

// abChallenger is a sampled challenger's block: the shape A3.3's launcher
// writes, with an invented model ID (no fixture in this repository names a
// real one).
func abChallenger(pairID string) *AB {
	return &AB{PairID: pairID, Arm: ABArmChallenger, Publish: ABPublishNone,
		Challenger: "invented-challenger-1", ChallengerSource: ABChallengerOwner}
}

// TestABRoundTrip: the pair block survives a marshal/parse round trip, both
// arms, and the challenger's own values are the ones that come back.
func TestABRoundTrip(t *testing.T) {
	pairID := "20261029-120000-abcd"
	for _, ab := range []*AB{
		abChallenger(pairID),
		{PairID: pairID, Arm: ABArmChampion},
	} {
		s := Spec{Version: 1, RunID: pairID, Repo: "owner/repo-a", Ref: "main", Task: "do it", AB: ab}
		data, err := s.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"ab"`) {
			t.Fatalf("AB set but missing from marshal: %s", data)
		}
		got, err := Parse(data)
		if err != nil {
			t.Fatalf("parse of %s: %v", data, err)
		}
		if got.AB == nil || *got.AB != *ab {
			t.Fatalf("round trip = %+v, want %+v", got.AB, ab)
		}
	}
}

// TestABChallengerRequiresPublishNone: a challenger that would publish is
// refused. D554 makes the shadow arm the whole reason a pair is safe to
// launch, so a block saying otherwise is a launcher's bug, not a run's
// opinion.
func TestABChallengerRequiresPublishNone(t *testing.T) {
	ab := abChallenger("20261029-120000-abcd")
	ab.Publish = ""
	s := Spec{Version: 1, RunID: "20261029-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", AB: ab}
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("a challenger with no publish = %v, want a refusal naming publish", err)
	}
}

// TestABChallengerRequiresModel: a challenger naming no model is refused —
// the pair's one difference (D555) is the coder model, so a challenger
// without one is a champion's task launched twice.
func TestABChallengerRequiresModel(t *testing.T) {
	ab := abChallenger("20261029-120000-abcd")
	ab.Challenger = ""
	s := Spec{Version: 1, RunID: "20261029-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", AB: ab}
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "challenger") {
		t.Fatalf("a challenger naming no model = %v, want a refusal naming challenger", err)
	}
}

// TestABChampionRefusesPublishNone: the champion is the run the developer
// gets: it publishes exactly as an unsampled run and runs no challenger, so
// the three fields that describe a challenger are refused on it rather than
// ignored. Ignoring them is what would break the record: the runner echoes
// Spec.AB onto result.json (A2.1), and both published schemas refuse a
// champion carrying any of the three.
func TestABChampionRefusesPublishNone(t *testing.T) {
	const pairID = "20261029-120000-abcd"
	champion := func(mutate func(*AB)) *AB {
		ab := &AB{PairID: pairID, Arm: ABArmChampion}
		mutate(ab)
		return ab
	}
	for _, tc := range []struct {
		name string
		ab   *AB
	}{
		{"a publish", champion(func(ab *AB) { ab.Publish = ABPublishNone })},
		{"a challenger model", champion(func(ab *AB) { ab.Challenger = "invented-challenger-1" })},
		{"a challenger source", champion(func(ab *AB) { ab.ChallengerSource = ABChallengerOwner })},
	} {
		s := Spec{Version: 1, RunID: pairID, Repo: "owner/repo-a", Ref: "main", Task: "do it", AB: tc.ab}
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), "champion") {
			t.Errorf("a champion carrying %s = %v, want a refusal naming champion", tc.name, err)
		}
	}
	// The bare champion — the shape A3.3 launches — is accepted.
	s := Spec{Version: 1, RunID: pairID, Repo: "owner/repo-a", Ref: "main", Task: "do it",
		AB: &AB{PairID: pairID, Arm: ABArmChampion}}
	if err := s.Validate(); err != nil {
		t.Fatalf("a champion carrying nothing but its pair and arm: %v", err)
	}
}

// TestABRefusesOverrideModel: the challenger never travels as an override
// (D555). Overrides.Model with no per-role models reaches every stage
// (Spec.Apply), so a task carrying both would change the reviewer the two
// arms share and the review signals would stop being comparable.
func TestABRefusesOverrideModel(t *testing.T) {
	s := Spec{Version: 1, RunID: "20261029-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it",
		AB: abChallenger("20261029-120000-abcd"), Overrides: Overrides{Model: "invented-champion-1"}}
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "overrides.model") {
		t.Fatalf("ab with overrides.model = %v, want a refusal naming overrides.model", err)
	}
	// The same override without a pair block is the flag it always was.
	s.AB = nil
	if err := s.Validate(); err != nil {
		t.Fatalf("overrides.model without an ab block: %v", err)
	}
}

// TestABUnknownArmRefused: the arm's vocabulary is closed, like rigor's and
// tier's: a near miss is a launcher's bug rather than a run's opinion about
// which arm it is.
func TestABUnknownArmRefused(t *testing.T) {
	for _, arm := range []ABArm{"", "Champion", "champion ", "shadow"} {
		s := Spec{Version: 1, RunID: "20261029-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it",
			AB: &AB{PairID: "20261029-120000-abcd", Arm: arm}}
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), "arm") {
			t.Errorf("arm %q = %v, want a refusal naming arm", arm, err)
		}
	}
	// The pair's own id, and the challenger's source, are the same kind of
	// closed value: an empty pair id names no pair, and a source that is not
	// the owner's is not one D572 reserves.
	for _, tc := range []struct {
		name string
		ab   *AB
		want string
	}{
		{"empty pair_id", &AB{Arm: ABArmChampion}, "pair_id"},
		{"unknown source", &AB{PairID: "20261029-120000-abcd", Arm: ABArmChallenger, Publish: ABPublishNone, Challenger: "invented-challenger-1", ChallengerSource: "repo"}, "challenger_source"},
		{"unknown publish", &AB{PairID: "20261029-120000-abcd", Arm: ABArmChallenger, Publish: "all", Challenger: "invented-challenger-1", ChallengerSource: ABChallengerOwner}, "publish"},
	} {
		s := Spec{Version: 1, RunID: "20261029-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", AB: tc.ab}
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want a refusal naming %s", tc.name, err, tc.want)
		}
	}
}

// TestSpecWithoutABIsByteIdentical: a launch that sampled nothing writes no
// ab key at all, so its task.json is byte-for-byte what the release before
// this field wrote. That is what keeps every unsampled launch readable by a
// runner older than the field, whose Parse refuses the whole spec over an
// unknown key — the same claim Spec.BaseBranch, CompressionOff and
// DigestRuns rest on.
func TestSpecWithoutABIsByteIdentical(t *testing.T) {
	s := Spec{Version: 1, RunID: "20261029-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it"}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"version":1,"run_id":"20261029-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it","overrides":{}}`
	if string(data) != want {
		t.Fatalf("a spec with no ab block is not byte-identical to the previous release's:\n got %s\nwant %s", data, want)
	}
}

// TestSpecAuthorRoundTrips: the launcher's author survives a marshal and a
// parse with both of its fields intact. It is the field A2b fills and A3's
// stamp environment reads back out of the spec it parsed, so a value that
// did not survive the round trip would silently become "the run's own
// identity" at the one moment the author is decided.
func TestSpecAuthorRoundTrips(t *testing.T) {
	want := &Person{Name: "person-1", Email: "person-1@example.invalid"}
	s := Spec{Version: 1, RunID: "20261030-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", Author: want}
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"author"`) {
		t.Fatalf("Author set but missing from marshal: %s", data)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("parse of %s: %v", data, err)
	}
	if got.Author == nil || *got.Author != *want {
		t.Fatalf("round trip author = %+v, want %+v", got.Author, want)
	}
}

// TestSpecWithoutAuthorIsByteIdentical: a launch that carries no author
// writes neither the key nor a byte the release before this field did not.
// That is what keeps every launch readable by a runner older than the field,
// whose Parse refuses the whole spec over an unknown key — the claim
// Spec.BaseBranch, CompressionOff, DigestRuns and AB rest on — and it is why
// the field is a pointer with omitempty rather than a struct the launcher
// always writes.
func TestSpecWithoutAuthorIsByteIdentical(t *testing.T) {
	s := Spec{Version: 1, RunID: "20261030-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it"}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"version":1,"run_id":"20261030-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it","overrides":{}}`
	if string(data) != want {
		t.Fatalf("a spec with no author is not byte-identical to the previous release's:\n got %s\nwant %s", data, want)
	}
}

// TestSpecAuthorRefusesControlCharacters: both fields reach git as
// GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL on every commit the run makes (A3's
// StampEnv), so a name carrying a line break or a control character could
// forge the header block of a commit, and the angle brackets are the
// `<name> <email>` form git itself prints from those two values. The email
// is also the address a forge maps to an account, so it has to be one
// address: exactly one '@' with a local part and a domain around it.
func TestSpecAuthorRefusesControlCharacters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		author Person
		want   string
	}{
		{"a newline in the name", Person{Name: "person-1\nOther: x", Email: "person-1@example.invalid"}, "one line"},
		{"a carriage return in the email", Person{Name: "person-1", Email: "person-1@example.invalid\r"}, "one line"},
		{"a control character in the name", Person{Name: "person\x07-1", Email: "person-1@example.invalid"}, "control character"},
		{"a control character in the email", Person{Name: "person-1", Email: "person\x1b-1@example.invalid"}, "control character"},
		{"an angle bracket in the name", Person{Name: "person-1 <person-2>", Email: "person-1@example.invalid"}, "angle bracket"},
		{"an angle bracket in the email", Person{Name: "person-1", Email: "person-1<@example.invalid"}, "angle bracket"},
		{"an email with no @", Person{Name: "person-1", Email: "person-1.example.invalid"}, "exactly one"},
		{"an email with two @", Person{Name: "person-1", Email: "person-1@example.invalid@x"}, "exactly one"},
		{"an email with an empty local part", Person{Name: "person-1", Email: "@example.invalid"}, "exactly one"},
		{"an email with an empty domain", Person{Name: "person-1", Email: "person-1@"}, "exactly one"},
		{"a blank name", Person{Name: "   ", Email: "person-1@example.invalid"}, "name"},
		{"an empty email", Person{Name: "person-1"}, "email"},
		{"a name over the cap", Person{Name: strings.Repeat("a", MaxAuthorNameLen+1), Email: "person-1@example.invalid"}, "longer than"},
		{"an email over the cap", Person{Name: "person-1", Email: strings.Repeat("a", MaxAuthorEmailLen) + "@example.invalid"}, "longer than"},
	} {
		s := Spec{Version: 1, RunID: "20261030-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", Author: &tc.author}
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Validate() = %v, want a refusal mentioning %q", tc.name, err, tc.want)
		}
	}
	// A person at both caps, and the two facts Validate's rule alone would
	// not catch: a name of spaces is no name, and an unclosed pair of angle
	// brackets is still an address form.
	ok := Person{Name: strings.Repeat("a", MaxAuthorNameLen), Email: strings.Repeat("a", MaxAuthorEmailLen-6) + "@b.co"}
	if err := (&Spec{Version: 1, RunID: "20261030-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", Author: &ok}).Validate(); err != nil {
		t.Errorf("an author at both length caps was refused: %v", err)
	}
}

// sameLevels reports whether two level blocks carry the same settings,
// pointer for pointer.
func sameLevels(a, b *config.ReviewLevels) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	same := func(x, y *config.LevelSetting) bool {
		if x == nil || y == nil {
			return x == y
		}
		return *x == *y
	}
	return same(a.L1, b.L1) && same(a.L2, b.L2) && same(a.L3, b.L3)
}

// TestReviewLevelsRoundTrip: the per-level block a launcher writes into a
// task is read back by both readers — the JSON a launcher writes and the
// struct this package marshals — and a spec that carries none writes
// neither the key nor a byte the release before this field did, which is
// what keeps every launch readable by a runner older than it (Parse decodes
// with DisallowUnknownFields).
func TestReviewLevelsRoundTrip(t *testing.T) {
	const doc = `{"version":1,"run_id":"20261031-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it",
	"review_levels":{"l1":{"model":"claude-haiku-4-5"},"l2":{"none":true},
	"l3":{"model":"claude-opus-5-5","effort":"xhigh","focus":"the migration and its rollback"}}}`
	want := &config.ReviewLevels{
		L1: &config.LevelSetting{Model: "claude-haiku-4-5"},
		L2: &config.LevelSetting{Off: true},
		L3: &config.LevelSetting{Model: "claude-opus-5-5", Effort: "xhigh", Focus: "the migration and its rollback"},
	}
	got, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse refused a task carrying review_levels: %v", err)
	}
	if !sameLevels(got.ReviewLevels, want) {
		t.Fatalf("parsed review_levels = %+v, want %+v", got.ReviewLevels, want)
	}
	s := Spec{Version: 1, RunID: "20261031-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", ReviewLevels: want}
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("parse of %s: %v", data, err)
	}
	if !sameLevels(back.ReviewLevels, want) {
		t.Fatalf("round trip review_levels = %+v, want %+v", back.ReviewLevels, want)
	}
	plain, err := json.Marshal(Spec{Version: 1, RunID: "20261031-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "review_levels") {
		t.Errorf("a spec with no review levels was not omitted: %s", plain)
	}
}

// TestReviewLevelsRefusesUnknownKey: the task's copy of the level block is
// written by the launcher, and every key inside it decides which review
// runs, on which model, at which effort — so an unknown key is refused at
// every level of the object (the block, a level, and a level's own keys)
// rather than dropped. The decode does it (DisallowUnknownFields reaches
// every nested object), and the block's own rules reach a hand-written
// task.json through Validate: a spec written straight into the bucket
// refuses the same documents a launcher would have refused before writing.
func TestReviewLevelsRefusesUnknownKey(t *testing.T) {
	head := `"version":1,"run_id":"20261031-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it"`
	for _, tc := range []struct {
		name string
		doc  string
		want string
	}{
		{"an unknown key inside the block", `{"l4":{"none":true}}`, "l4"},
		{"an unknown key inside a level", `{"l1":{"efforts":"high"}}`, "efforts"},
		{"an unknown key inside a level beside a real one", `{"l2":{"effort":"high","model_name":"claude-opus-5-5"}}`, "model_name"},
		{"an unknown key in the last level of three", `{"l1":{"none":true},"l2":{"none":true},"l3":{"off":true}}`, "off"},
		{"an unknown block", `{"reviewLevels":{"l1":{"none":true}}}`, "reviewLevels"},
		{"an effort outside the closed set", `{"l3":{"effort":"auto"}}`, "auto"},
		{"an effort the binary would drop in silence", `{"l3":{"effort":"xhigh "}}`, "xhigh"},
		{"an upper-case effort", `{"l2":{"effort":"HIGH"}}`, "HIGH"},
		{"none: true beside another key", `{"l2":{"none":true,"model":"claude-opus-5-5"}}`, "none: true"},
		{"a focus over the bound", `{"l3":{"focus":"` + strings.Repeat("a", config.MaxLevelFocusBytes+1) + `"}}`, "longer than"},
		{"a focus with a bidi override", `{"l3":{"focus":"the ‮migration"}}`, "U+202E"},
		{"a focus with a control character", `{"l1":{"focus":"the\u0007migration"}}`, "U+0007"},
	} {
		_, err := Parse([]byte("{" + head + `,"review_levels":` + tc.doc + "}"))
		if err == nil {
			t.Errorf("%s: Parse accepted the spec", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refusal %q does not mention %q", tc.name, err, tc.want)
		}
	}
	for _, doc := range []string{
		`{"l1":{"model":"claude-haiku-4-5","effort":"low"},"l2":{"none":true}}`,
		`{"l3":{"focus":"the migration and its rollback"}}`,
		`{"l2":{}}`,
	} {
		if _, err := Parse([]byte("{" + head + `,"review_levels":` + doc + "}")); err != nil {
			t.Errorf("Parse refused %s: %v", doc, err)
		}
	}
}
