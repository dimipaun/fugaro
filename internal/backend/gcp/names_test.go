package gcp

import (
	"math/rand/v2"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/task"
)

var (
	// Cloud Run job: at most 49, lowercase letters, digits and hyphens,
	// starting with a letter and not ending with a hyphen.
	jobNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}[a-z0-9]$`)
	// Service account ID: 6 to 30, same alphabet.
	saIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	// Secret Manager ID: at most 255 of [A-Za-z0-9_-].
	secretIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)
	// An image repository path component.
	imageRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

func TestNames(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{JobName("acme-app", "web"), "fugaro-acme-app-web-2dc53d2bf3a6"},
		{ServiceAccountID("acme-app", "web"), "fugaro-acme-app-web-2dc53d2b"},
		{SecretID("acme-app", "bitbucket-token"), "fugaro-acme-app-bitbucket-token-0efe1f9a3085f9af"},
		{ImageName("us-east5-docker.pkg.dev/p/fugaro", "acme-my.app", "web"), "us-east5-docker.pkg.dev/p/fugaro/acme-my-app-web-1f52a58402ad0f07"},
		// Storage slugs may hold '.' and '_'; Cloud Run names may not.
		{JobName("acme-my.app_x", "web"), "fugaro-acme-my-app-x-web-63dbfb983390"},
		{ServiceAccountID("acmecorp-fugarosandbox", "web"), "fugaro-acmecorp-fugar-b8404e39"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// namers are every derived name, as functions of the (slug, second) pair.
var namers = map[string]func(slug, second string) string{
	"JobName":          JobName,
	"ServiceAccountID": ServiceAccountID,
	"SecretID":         SecretID,
	"ImageName":        func(s, w string) string { return ImageName("reg.example/p/fugaro", s, w) },
}

// Pairs whose readable forms coincide: the '-' join, sanitizing, case, and
// truncation all merge them.
var trickyPairs = [][2][2]string{
	{{"acme-app", "web-x"}, {"acme-app-web", "x"}},
	{{"acme-app", "web"}, {"acme", "app-web"}},
	{{"acme.app", "web"}, {"acme-app", "web"}},
	{{"acme_app", "web"}, {"acme-app", "web"}},
	{{"acme--app", "web"}, {"acme-app", "web"}},
	{{"acme-app-", "web"}, {"acme-app", "web"}},
	{{"Acme-App", "web"}, {"acme-app", "web"}},
	{{strings.Repeat("a", 80), "web"}, {strings.Repeat("a", 80), "api"}},
	{{strings.Repeat("a", 60) + "x", "web"}, {strings.Repeat("a", 60) + "y", "web"}},
	{{"acmecorp-fugarosandbox", "web"}, {"acmecorp-fugarosandbox", "api"}},
	// readableSlug strips a 16-hex tail, even a genuine one; the hash still differs.
	{{"acme-0123456789abcdef", "web"}, {"acme", "web"}},
}

func TestNamesAreInjective(t *testing.T) {
	for name, f := range namers {
		for _, p := range trickyPairs {
			a, b := f(p[0][0], p[0][1]), f(p[1][0], p[1][1])
			if a == b {
				t.Errorf("%s(%q,%q) == %s(%q,%q) == %q", name, p[0][0], p[0][1], name, p[1][0], p[1][1], a)
			}
		}
		// A dense grid of short slugs and workflows made of the characters
		// that merge in the readable form.
		seen := map[string][2]string{}
		parts := []string{"a", "b", "a-b", "a-", "-b", "a.b", "a_b", "ab", "A", "a--b"}
		for _, s := range parts {
			for _, w := range parts {
				n := f("x"+s, w)
				if prev, dup := seen[n]; dup {
					t.Errorf("%s: %q from both %q and %q", name, n, prev, [2]string{"x" + s, w})
				}
				seen[n] = [2]string{"x" + s, w}
			}
		}
		// Stable across calls.
		if f("acme-app", "web") != f("acme-app", "web") {
			t.Errorf("%s is not stable", name)
		}
	}
}

func FuzzNamesAreInjective(f *testing.F) {
	f.Add("acme-app", "web-x", "acme-app-web", "x")
	f.Add("acme.app", "web", "acme-app", "web")
	f.Fuzz(func(t *testing.T, s1, w1, s2, w2 string) {
		// NUL is the hash's separator; slugs and workflow names never hold it.
		if (s1 == s2 && w1 == w2) || strings.ContainsRune(s1+w1+s2+w2, 0) {
			return
		}
		for name, fn := range namers {
			if a, b := fn(s1, w1), fn(s2, w2); a == b {
				t.Errorf("%s(%q,%q) == %s(%q,%q) == %q", name, s1, w1, name, s2, w2, a)
			}
		}
	})
}

func TestNamesFitEachResourcesLimits(t *testing.T) {
	long := "someorganization-" + strings.Repeat("verylongrepositoryname", 12)
	inputs := [][2]string{
		{"acme-app", "web"}, {"a", "b"}, {long, "web"}, {long, long},
		{"acme-my.app_x", "web"}, {"UPPER_case.repo", "Web.Flow"}, {"acmecorp-fugarosandbox", "web"},
		{"a-" + strings.Repeat("-", 60), "b"}, // truncation lands on hyphens
	}
	for _, in := range inputs {
		s, w := in[0], in[1]
		if j := JobName(s, w); len(j) > 49 || !jobNameRE.MatchString(j) {
			t.Errorf("JobName(%q,%q) = %q (%d)", s, w, j, len(j))
		}
		if sa := ServiceAccountID(s, w); len(sa) < 6 || len(sa) > 30 || !saIDRE.MatchString(sa) {
			t.Errorf("ServiceAccountID(%q,%q) = %q (%d)", s, w, sa, len(sa))
		}
		if id := SecretID(s, w); !secretIDRE.MatchString(id) {
			t.Errorf("SecretID(%q,%q) = %q (%d)", s, w, id, len(id))
		}
		img := ImageName("reg.example/p/fugaro/", s, w)
		last, ok := strings.CutPrefix(img, "reg.example/p/fugaro/")
		if !ok || len(last) > maxImage || !imageRE.MatchString(last) {
			t.Errorf("ImageName(%q,%q) = %q", s, w, img)
		}
	}
	// Truncated names keep a readable prefix.
	if sa := ServiceAccountID("acmecorp-fugarosandbox", "web"); !strings.HasPrefix(sa, "fugaro-acmecorp-") {
		t.Errorf("ServiceAccountID lost its prefix: %q", sa)
	}
	if j := JobName(long, "web"); !strings.HasPrefix(j, "fugaro-someorganization-") {
		t.Errorf("JobName lost its prefix: %q", j)
	}
}

// Real slugs end in their own hash; names leave it out of the readable
// part, stay within limits, and still separate the repositories.
func TestNamesOfRealSlugs(t *testing.T) {
	// Pinned: these name live resources.
	bb := realSlug(t, "bitbucket", "acme/app")
	for _, c := range []struct{ got, want string }{
		{JobName(bb, "web"), "fugaro-acme-app-web-d23e1b1855b7"},
		{ServiceAccountID(bb, "web"), "fugaro-acme-app-web-d23e1b18"},
		{SecretID(bb, "bitbucket-token"), "fugaro-acme-app-bitbucket-token-56bdbf4c11445b53"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}

	a, b := realSlug(t, "github", "acme/app-web"), realSlug(t, "github", "acme-app/web")
	if j := JobName(a, "x"); !strings.HasPrefix(j, "fugaro-acme-app-web-x-") || len(j) > 49 || !jobNameRE.MatchString(j) {
		t.Errorf("JobName(%q) = %q", a, j)
	}
	for name, f := range namers {
		if f(a, "x") == f(b, "x") {
			t.Errorf("%s: acme/app-web and acme-app/web share %q", name, f(a, "x"))
		}
	}
	long := realSlug(t, "github", strings.Repeat("organization", 6)+"/"+strings.Repeat("repository", 6))
	if j := JobName(long, "web"); len(j) > 49 || !jobNameRE.MatchString(j) {
		t.Errorf("JobName(long slug) = %q (%d)", j, len(j))
	}
	if sa := ServiceAccountID(long, "web"); len(sa) > 30 || !saIDRE.MatchString(sa) {
		t.Errorf("ServiceAccountID(long slug) = %q", sa)
	}
}

func realSlug(t *testing.T, provider, repo string) string {
	t.Helper()
	s, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRepoLabel(t *testing.T) {
	if got, err := RepoLabel("acme-my.app"); err != nil || got != "acme-my_app" {
		t.Fatalf("RepoLabel = %q, %v", got, err)
	}
	// Never truncated: a long slug ends in its hash, which must survive.
	if got, err := RepoLabel(strings.Repeat("a", 64)); err == nil {
		t.Fatalf("64-character slug gave label %q", got)
	}
	if got, err := RepoLabel(strings.Repeat("a", 63)); err != nil || len(got) != 63 {
		t.Fatalf("63-character slug: %q, %v", got, err)
	}
}

// randomSlugs are n distinct real slugs of random repositories.
func randomSlugs(t *testing.T, n int) []string {
	t.Helper()
	r := rand.New(rand.NewPCG(1, 2))
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789-._"
	word := func() string {
		b := make([]byte, 1+r.IntN(30))
		b[0] = 'a' + byte(r.IntN(26))
		for i := 1; i < len(b); i++ {
			b[i] = alphabet[r.IntN(len(alphabet))]
		}
		return string(b)
	}
	seen := map[string]bool{}
	var out []string
	for len(out) < n {
		provider := []string{"github", "bitbucket"}[r.IntN(2)]
		s, err := task.Slug(provider, word()+"/"+word())
		if err != nil || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// A build account's ID must never be a job account's, even for a workflow
// named build, or one account would hold both sets of grants.
func TestBuildSAIDDisjoint(t *testing.T) {
	jobs := map[string]string{}
	slugs := randomSlugs(t, 2000)
	for _, s := range slugs {
		for _, wf := range []string{"build", "web", "b", "x"} {
			jobs[ServiceAccountID(s, wf)] = s + " " + wf
		}
	}
	builds := map[string]string{}
	for _, s := range slugs {
		id := BuildServiceAccountID(s)
		if len(id) < 6 || len(id) > 30 || !saIDRE.MatchString(id) {
			t.Errorf("BuildServiceAccountID(%q) = %q (%d)", s, id, len(id))
		}
		if other, dup := jobs[id]; dup {
			t.Errorf("BuildServiceAccountID(%q) = %q, the job account of %s", s, id, other)
		}
		if other, dup := builds[id]; dup {
			t.Errorf("BuildServiceAccountID(%q) = %q, as for %s", s, id, other)
		}
		builds[id] = s
	}
	if id := BuildServiceAccountID(slugs[0]); id != BuildServiceAccountID(slugs[0]) || !strings.HasPrefix(id, "fugaro-b-") {
		t.Errorf("BuildServiceAccountID = %q", id)
	}
}

var registryIDRE = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

func TestRegistryRepoID(t *testing.T) {
	seen := map[string]string{}
	long := "someorganization-" + strings.Repeat("verylongrepositoryname", 12)
	for _, s := range append(randomSlugs(t, 2000), long, "a", "acme-my.app_x") {
		id := RegistryRepoID(s)
		if !registryIDRE.MatchString(id) || id == "fugaro" || id == "fugaro-base" || !strings.HasPrefix(id, "fugaro-") {
			t.Errorf("RegistryRepoID(%q) = %q (%d)", s, id, len(id))
		}
		if other, dup := seen[id]; dup {
			t.Errorf("RegistryRepoID(%q) = %q, as for %q", s, id, other)
		}
		seen[id] = s
	}
}

// Check jobs must stay out of the fugaro- job listings (ls, max_parallel).
func TestCheckJobNameNotFugaroPrefix(t *testing.T) {
	long := "someorganization-" + strings.Repeat("verylongrepositoryname", 12)
	for _, s := range append(randomSlugs(t, 200), long, "a", "-", "acme-my.app_x") {
		for name, n := range map[string]string{"CheckJobName": CheckJobName(s), "SchedulerJobName": SchedulerJobName(s)} {
			if strings.HasPrefix(n, "fugaro-") || !strings.HasPrefix(n, "fugarochk-") || len(n) > 49 || !jobNameRE.MatchString(n) {
				t.Errorf("%s(%q) = %q", name, s, n)
			}
		}
		if CheckJobName(s) == SchedulerJobName(s) {
			t.Errorf("CheckJobName and SchedulerJobName of %q share a hash domain", s)
		}
	}
}

func TestSchedulerRegion(t *testing.T) {
	for in, want := range map[string]string{"us-east5": "us-east4", "us-east4": "us-east4", "europe-west1": "europe-west1", "us-central1": "us-central1"} {
		if got, err := SchedulerRegion(in); err != nil || got != want {
			t.Errorf("SchedulerRegion(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := SchedulerRegion("mars-north1"); err == nil || !strings.Contains(err.Error(), "--scheduler-region") {
		t.Errorf("SchedulerRegion(unknown) = %q, %v", got, err)
	}
	for _, r := range SchedulerRegions {
		if got, err := SchedulerRegion(r); err != nil || got != r {
			t.Errorf("listed region %q gives %q, %v", r, got, err)
		}
	}
}

func TestDisplayNamesAndConditions(t *testing.T) {
	slug := realSlug(t, "bitbucket", "acme/sandbox")
	for _, c := range []struct{ got, want string }{
		{JobSADisplayName(slug, "web"), "Fugaro job " + slug + " web"},
		{LegacyJobSADisplayName(slug, "web"), "Fugaro M4 job " + slug + " web"},
		{BuildSADisplayName(slug), "Fugaro build " + slug},
		{BucketConditionTitle("fugaro-x-12345678"), "fugaro-fugaro-x-12345678"},
		{BucketCondition("b", []string{"runs", "cache"}, "s"),
			`resource.name.startsWith("projects/_/buckets/b/objects/runs/s/") || resource.name.startsWith("projects/_/buckets/b/objects/cache/s/")`},
		{LauncherBucketConditionTitle, "fugaro-launchers-runs"},
		{LauncherBucketCondition("fugaro-runs-proj-1234"),
			`resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/runs/")`},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// LauncherBucketCondition embeds bucket in a CEL string literal unescaped: a
// name outside [a-z0-9._-] (a quote, in particular) could otherwise break out
// of it and widen the condition the installation module, the install guard
// and doctor all compare against byte for byte (docs/design/bucket-iam.md
// H5-H6). A caller passing such a name is a bug, so this panics rather than
// returning a condition that looks plausible but is not what it claims to be.
func TestLauncherBucketConditionRejectsBadBucketNames(t *testing.T) {
	for _, bad := range []string{
		`fugaro-runs-proj-1234"`,
		`fugaro-runs-proj") || resource.name.startsWith("projects/_/buckets/x/objects/`,
		"Fugaro-Runs-Proj-1234",
		"fugaro runs proj",
		"fugaro-runs-proj/x",
		"",
	} {
		t.Run(bad, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("LauncherBucketCondition(%q) did not panic", bad)
				}
			}()
			LauncherBucketCondition(bad)
		})
	}
	// A bucket name of only the allowed characters never panics.
	if got := LauncherBucketCondition("fugaro-runs-proj-1234"); got == "" {
		t.Error("a valid bucket name panicked")
	}
}

// Pinned: these name live resources once a repository is onboarded.
func TestNewNamesOfRealSlugs(t *testing.T) {
	s := realSlug(t, "bitbucket", "acme/sandbox")
	for _, c := range []struct{ got, want string }{
		{BuildServiceAccountID(s), "fugaro-b-acme-sandbox-78cbc6a5"},
		{RegistryRepoID(s), "fugaro-acme-sandbox-6a12f465054d"},
		{CheckJobName(s), "fugarochk-acme-sandbox-39759bd68a27"},
		{SchedulerJobName(s), "fugarochk-acme-sandbox-5d4307f21198"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestWorkflowVariantIsIdentityForDefault(t *testing.T) {
	for _, tok := range []string{"", "default"} {
		if got := WorkflowVariant("build", tok); got != "build" {
			t.Errorf("WorkflowVariant(build, %q) = %q, want build", tok, got)
		}
	}
	// The separator is "_", not "." or "--": "." would fail Terraform's
	// label validation (TestWorkflowVariantMatchesTerraformWorkflowLabel),
	// and a bare "--" is not injective (TestWorkflowVariantIsInjective).
	// This exact-match check also catches a join that silently reorders
	// its operands (token first instead of workflow first).
	if got := WorkflowVariant("build", "acme-personal"); got != "build_acme-personal" {
		t.Errorf("WorkflowVariant(build, acme-personal) = %q, want build_acme-personal", got)
	}
	// Every downstream name-deriving function must still produce a distinct,
	// valid name for a variant: JobName, ServiceAccountID and SecretID are
	// all exercised against the plain workflow's own name, not just one of
	// them (TestWorkflowVariantIsInjective below covers the harder case of
	// two distinct (workflow, token) pairs colliding with each other).
	variant := WorkflowVariant("build", "acme-personal")
	if JobName("s", variant) == JobName("s", "build") {
		t.Error("a variant collided with the plain workflow's job name")
	}
	if ServiceAccountID("s", variant) == ServiceAccountID("s", "build") {
		t.Error("a variant collided with the plain workflow's service account ID")
	}
	if SecretID("s", variant) == SecretID("s", "build") {
		t.Error("a variant collided with the plain workflow's secret ID")
	}
}

// workflowVariantValid reports whether (workflow, token) is a pair
// WorkflowVariant accepts (would not panic on): the same charset check
// WorkflowVariant itself makes, duplicated in the test so a test case can
// decide up front whether to expect a result or a panic.
func workflowVariantValid(workflow, token string) bool {
	return workflowVariantWorkflowRE.MatchString(workflow) && workflowVariantTokenRE.MatchString(token)
}

// workflowVariantTrickyPairs are (workflow, token) pairs a naive "--" join
// (or any join using a byte either charset already allows) would conflate:
// both components may hold hyphens, so where the hyphen "boundary" falls
// between the two fields is ambiguous unless the separator itself is a
// byte neither field can contain.
var workflowVariantTrickyPairs = [][2][2]string{
	{{"ab--cd", "ef"}, {"ab", "cd--ef"}},
	{{"build", "acme-personal"}, {"build-acme", "personal"}},
	{{"a-b-c", "d"}, {"a-b", "c-d"}},
	{{"a", "b-c-d"}, {"a-b", "c-d"}},
	{{"a-", "b"}, {"a", "-b"}}, // not charset-valid starts, still worth covering
}

// TestWorkflowVariantIsInjective is the property TestNamesAreInjective (and
// FuzzNamesAreInjective) already pin for every other name in this file,
// extended to WorkflowVariant: two distinct (workflow, token) pairs must
// never produce the same variant, and the collision must not reappear one
// layer down in JobName, ServiceAccountID or SecretID either (the finding
// this test was added for: a "--" join let two unrelated pairs share a job,
// a service account and a secret, defeating the one-secretAccessor-grant-
// per-named-account design, docs/design/named-oauth-tokens.md §4 option B).
func TestWorkflowVariantIsInjective(t *testing.T) {
	for _, p := range workflowVariantTrickyPairs {
		if !workflowVariantValid(p[0][0], p[0][1]) || !workflowVariantValid(p[1][0], p[1][1]) {
			continue // covered by TestWorkflowVariantRejectsOutOfCharsetInput instead
		}
		a, b := WorkflowVariant(p[0][0], p[0][1]), WorkflowVariant(p[1][0], p[1][1])
		if a == b {
			t.Errorf("WorkflowVariant(%q,%q) == WorkflowVariant(%q,%q) == %q", p[0][0], p[0][1], p[1][0], p[1][1], a)
			continue
		}
		if JobName("s", a) == JobName("s", b) {
			t.Errorf("JobName collided for distinct variants %q and %q", a, b)
		}
		if ServiceAccountID("s", a) == ServiceAccountID("s", b) {
			t.Errorf("ServiceAccountID collided for distinct variants %q and %q", a, b)
		}
		if SecretID("s", a) == SecretID("s", b) {
			t.Errorf("SecretID collided for distinct variants %q and %q", a, b)
		}
	}

	// A dense grid of short, charset-valid workflow/token names made of the
	// characters that merge in a naive join, the same style
	// TestNamesAreInjective uses for JobName/ServiceAccountID/SecretID/ImageName.
	seen := map[string][2]string{}
	parts := []string{"a", "b", "a-b", "ab", "a-b-c", "x"}
	for _, w := range parts {
		for _, tok := range parts {
			v := WorkflowVariant(w, tok)
			if prev, dup := seen[v]; dup && prev != ([2]string{w, tok}) {
				t.Errorf("WorkflowVariant(%q,%q) == WorkflowVariant(%q,%q) == %q", w, tok, prev[0], prev[1], v)
			}
			seen[v] = [2]string{w, tok}
		}
	}
}

func FuzzWorkflowVariantIsInjective(f *testing.F) {
	f.Add("ab--cd", "ef", "ab", "cd--ef")
	f.Add("build", "acme-personal", "build-acme", "personal")
	f.Fuzz(func(t *testing.T, w1, tok1, w2, tok2 string) {
		if !workflowVariantValid(w1, tok1) || !workflowVariantValid(w2, tok2) {
			return // only charset-valid inputs are WorkflowVariant's contract
		}
		if w1 == w2 && tok1 == tok2 {
			return
		}
		if a, b := WorkflowVariant(w1, tok1), WorkflowVariant(w2, tok2); a == b {
			t.Errorf("WorkflowVariant(%q,%q) == WorkflowVariant(%q,%q) == %q", w1, tok1, w2, tok2, a)
		}
	})
}

// TestWorkflowVariantRejectsOutOfCharsetInput proves the fail-closed guard
// is real: WorkflowVariant panics rather than silently return a variant
// string that looks plausible but is ambiguous, if ever called (a bug
// elsewhere, since every caller is expected to pass already-validated
// workflow and token names) with a workflow or token outside its own
// charset. The token cases include a trailing and a doubled hyphen,
// which workflowVariantTokenRE refuses but a looser, workflow-shaped
// charset would have silently accepted (the charset-tightening finding
// this test was extended for).
func TestWorkflowVariantRejectsOutOfCharsetInput(t *testing.T) {
	for _, c := range []struct{ workflow, token string }{
		{"build", "a.b"},
		{"build.x", "acme-personal"},
		{"Build", "acme-personal"},
		{"build", "Acme-Personal"},
		{"build", "acme-"},   // token: trailing hyphen, refused since Task 1's review
		{"build", "acme--x"}, // token: doubled hyphen, refused since Task 1's review
		{"build", "-acme"},   // token: leading hyphen
	} {
		t.Run(c.workflow+"/"+c.token, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("WorkflowVariant(%q, %q) did not panic", c.workflow, c.token)
				}
			}()
			WorkflowVariant(c.workflow, c.token)
		})
	}
	// A charset-valid pair never panics. The workflow side still allows a
	// trailing/doubled hyphen (config.WorkflowNameRE itself allows it; this
	// package only mirrors that rule, it does not tighten it).
	for _, c := range []struct{ workflow, token string }{
		{"build", "acme-personal"},
		{"build-", "acme"},
		{"build--x", "acme"},
	} {
		if got := WorkflowVariant(c.workflow, c.token); got == "" {
			t.Errorf("WorkflowVariant(%q, %q) panicked", c.workflow, c.token)
		}
	}
}

// readTerraformNameVariableRegex extracts the regular expression
// deploy/terraform/gcp/modules/workflow/variables.tf's "name" variable
// validates against, straight out of the .tf file's text (never edited by
// this package: decision T5 forbids a Terraform diff for this feature).
// Reading it live, rather than hard-coding a copy, means a future change to
// that validation breaks this test immediately, pointing at the one place
// gcp.WorkflowVariant's separator and charset choice must be reconciled
// with it, instead of silently drifting until a real `terraform apply`
// fails.
func readTerraformNameVariableRegex(t *testing.T) *regexp.Regexp {
	t.Helper()
	path := "../../../deploy/terraform/gcp/modules/workflow/variables.tf"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	block := regexp.MustCompile(`variable\s+"name"\s*\{[\s\S]*?\n\}`).FindString(string(data))
	if block == "" {
		t.Fatalf(`%s: no variable "name" { ... } block found; this test must be updated to match the current file`, path)
	}
	m := regexp.MustCompile(`regex\("(\^.*?\$)",\s*var\.name\)`).FindStringSubmatch(block)
	if m == nil {
		t.Fatalf(`%s: var.name's "name" block has no regex("...", var.name) validation; this test must be updated to match the current file:
%s`, path, block)
	}
	re, err := regexp.Compile(m[1])
	if err != nil {
		t.Fatalf("%s: var.name's regex %q does not compile: %v", path, m[1], err)
	}
	return re
}

// TestWorkflowVariantMatchesTerraformWorkflowLabel is decision T5's own
// guarantee, checked: WorkflowVariant's output becomes the Terraform
// `workflows` map's key (modules/repo/workflows.tf's `for_each`/`each.key`),
// which modules/workflow passes straight into the job's fugaro_workflow GCP
// label. A byte outside the label charset would make every named-account
// `terraform apply` fail validation with no code change on the Terraform
// side at all — exactly the kind of silent breakage T5 promises never
// happens. This also pins the module's regex to the value this package was
// written against, so an unrelated .tf edit that changes it is caught here.
func TestWorkflowVariantMatchesTerraformWorkflowLabel(t *testing.T) {
	labelRE := readTerraformNameVariableRegex(t)
	if labelRE.String() != `^[a-z0-9_-]{1,63}$` {
		t.Fatalf(`deploy/terraform/gcp/modules/workflow/variables.tf's var.name regex changed to %s (this test was written against ^[a-z0-9_-]{1,63}$); reconcile WorkflowVariant's separator and charset assumptions with the new rule before updating this constant`, labelRE)
	}

	maxWorkflow := strings.Repeat("a", 20) // config.WorkflowNameRE's longest
	maxToken := strings.Repeat("a", 31)    // the planned config.ClaudeAccountNameRE's longest
	for _, c := range []struct{ workflow, token string }{
		{"build", "default"},
		{"build", ""},
		{"build", "acme-personal"},
		{"a", "a"},
		{maxWorkflow, maxToken},
	} {
		v := WorkflowVariant(c.workflow, c.token)
		if !labelRE.MatchString(v) {
			t.Errorf("WorkflowVariant(%q, %q) = %q does not match the Terraform module's name/label regex %s (modules/workflow/variables.tf's \"name\" variable, the job's fugaro_workflow label); terraform apply would refuse it", c.workflow, c.token, v, labelRE)
		}
	}
}

// TestWorkflowVariantAtMaximumLengths is the property test for finding 2:
// a reviewer's mutation that truncated the joined string
// ("(workflow+"_"+token)[:20]") made WorkflowVariant of a 20-character
// workflow and any token equal the plain workflow name again, silently
// reintroducing the identity-for-default collision for named accounts.
// Every check here is run at the boundary lengths config.WorkflowNameRE
// (20) and the planned config.ClaudeAccountNameRE (31) actually allow, not
// just short illustrative names, since a truncation bug only shows up once
// the join is long enough to hit it.
func TestWorkflowVariantAtMaximumLengths(t *testing.T) {
	maxWorkflow := strings.Repeat("a", 20)
	maxToken := strings.Repeat("a", 31)
	// task.Slug's own shape: a readable part trimmed to 46, "-", 16 hex.
	longSlug := strings.Repeat("a", 46) + "-0123456789abcdef"

	variant := WorkflowVariant(maxWorkflow, maxToken)
	if variant == maxWorkflow {
		t.Fatalf("WorkflowVariant at maximum lengths equals the plain workflow name: %q", variant)
	}
	if want := len(maxWorkflow) + 1 + len(maxToken); len(variant) != want {
		t.Fatalf("len(WorkflowVariant(maxWorkflow, maxToken)) = %d, want %d (20+1+31)", len(variant), want)
	}

	// A grid of boundary-length (workflow, token) pairs: shortest and
	// longest of each, crossed, must stay injective and keep producing
	// valid, unique derived names.
	workflows := []string{"a", maxWorkflow, strings.Repeat("a", 19) + "z"}
	tokens := []string{"a", maxToken, strings.Repeat("a", 30) + "z"}
	variants := map[string][2]string{}
	for _, w := range workflows {
		for _, tok := range tokens {
			v := WorkflowVariant(w, tok)
			if prev, dup := variants[v]; dup && prev != ([2]string{w, tok}) {
				t.Errorf("WorkflowVariant(%q,%q) == WorkflowVariant(%q,%q) == %q", w, tok, prev[0], prev[1], v)
			}
			variants[v] = [2]string{w, tok}

			if j := JobName(longSlug, v); len(j) > maxJobName || !jobNameRE.MatchString(j) {
				t.Errorf("JobName(longSlug, %q) = %q (%d chars), want <= %d matching %s", v, j, len(j), maxJobName, jobNameRE)
			}
			if sa := ServiceAccountID(longSlug, v); len(sa) < 6 || len(sa) > maxSAID || !saIDRE.MatchString(sa) {
				t.Errorf("ServiceAccountID(longSlug, %q) = %q (%d chars)", v, sa, len(sa))
			}
			if id := SecretID(longSlug, v); !secretIDRE.MatchString(id) {
				t.Errorf("SecretID(longSlug, %q) = %q", v, id)
			}
		}
	}

	// Distinct variants must still give distinct derived names: format
	// validity alone (above) would not catch a collision.
	jobs, sas, secrets := map[string]string{}, map[string]string{}, map[string]string{}
	for v := range variants {
		if other, dup := jobs[JobName(longSlug, v)]; dup && other != v {
			t.Errorf("JobName collided for variants %q and %q", v, other)
		}
		jobs[JobName(longSlug, v)] = v
		if other, dup := sas[ServiceAccountID(longSlug, v)]; dup && other != v {
			t.Errorf("ServiceAccountID collided for variants %q and %q", v, other)
		}
		sas[ServiceAccountID(longSlug, v)] = v
		if other, dup := secrets[SecretID(longSlug, v)]; dup && other != v {
			t.Errorf("SecretID collided for variants %q and %q", v, other)
		}
		secrets[SecretID(longSlug, v)] = v
	}
}

// TestJobSADisplayNameFitsIAMLimit pins the "at most 95" bound
// JobSADisplayName's own comment claims, for today's one real caller
// (internal/infra/spec.go, which always passes the plain fugaro.yaml
// workflow name, config.WorkflowNameRE, <= 20 characters) and today's
// longest possible slug (task.Slug's own 63-character bound).
//
// TASK 4 TODO (see the comment on JobSADisplayName): once infra starts
// deriving this function's workflow argument from gcp.WorkflowVariant (up
// to 52 characters), the bound becomes 11+63+1+52 = 127, over IAM's
// 100-character display-name limit, and this test must gain a
// variant-length case alongside a fix to JobSADisplayName itself.
func TestJobSADisplayNameFitsIAMLimit(t *testing.T) {
	maxSlug := strings.Repeat("a", 46) + "-0123456789abcdef" // task.Slug's own 63-char shape
	if len(maxSlug) != 63 {
		t.Fatalf("test fixture maxSlug is %d characters, want 63", len(maxSlug))
	}
	maxWorkflow := strings.Repeat("a", 20) // config.WorkflowNameRE's longest
	name := JobSADisplayName(maxSlug, maxWorkflow)
	if len(name) > 100 {
		t.Fatalf("JobSADisplayName(63-char slug, 20-char workflow) = %d characters, want <= 100 (IAM's display-name limit): %q", len(name), name)
	}
	if want := len("Fugaro job ") + len(maxSlug) + 1 + len(maxWorkflow); len(name) != want {
		t.Fatalf("JobSADisplayName length = %d, want the documented %d (11+63+1+20)", len(name), want)
	}
}

func TestDefaultRegistryHost(t *testing.T) {
	if got := DefaultRegistryHost("us-east5", "my-proj"); got != "us-east5-docker.pkg.dev/my-proj" || !registryHostRE.MatchString(got) {
		t.Errorf("DefaultRegistryHost = %q, want us-east5-docker.pkg.dev/my-proj", got)
	}
}
