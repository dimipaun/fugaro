package gcp

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
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
		{LauncherBucketConditionTitle, "fugaro-launchers-runs-tokens"},
		{LauncherBucketCondition("fugaro-runs-proj-1234"),
			`resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/runs/") || resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/")`},
		// The launchers' grant as every release before 0.9.0 wrote it: what a
		// reader must recognise as that grant's old form rather than as a
		// stranger's condition (internal/infra's RunsBucketFindings), and the
		// bytes no release writes any more.
		{LegacyLauncherBucketConditionTitle, "fugaro-launchers-runs"},
		{LegacyLauncherBucketCondition("fugaro-runs-proj-1234"),
			`resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/runs/")`},
		{TokenBucketConditionTitle("fugaro-x-12345678"), "fugaro-tokens-fugaro-x-12345678"},
		{TokenBucketCondition("b", "s"), `resource.name.startsWith("projects/_/buckets/b/objects/tokens/s/")`},
		// The project job's pair (plan task 3.2 step 2): the widened objectUser
		// grant on runs/, cache/ and locks/, and the read-only one on the whole
		// of tokens/. Both are slugless, and neither is the per-repository
		// builder called with an empty slug, which refuses
		// (TestProjectBucketConditionHasNoSlug).
		{ProjectBucketCondition("b", []string{"runs", "cache"}),
			`resource.name.startsWith("projects/_/buckets/b/objects/runs/") || resource.name.startsWith("projects/_/buckets/b/objects/cache/")`},
		{ProjectTokenBucketCondition("fugaro-runs-proj-1234"),
			`resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/")`},
		{ProjectTokenBucketConditionTitle("fugaro-x-12345678"), "fugaro-project-tokens-fugaro-x-12345678"},
		// The three title prefixes the plan reader matches on
		// (internal/infra/tf) are these, and a project job's title must not
		// read as a per-repository job's: the prefix of one is not a prefix of
		// the other's titles.
		{BucketConditionTitlePrefix, "fugaro-"},
		{TokenBucketConditionTitlePrefix, "fugaro-tokens-"},
		{ProjectTokenBucketConditionTitlePrefix, "fugaro-project-tokens-"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	// The plan reader takes a tokens/ expression for a repository's job grant
	// only under the title built from the member
	// (internal/infra/tf's jobAccountGrant), so the per-repository title and
	// the project job's must not be readable as each other: a title built with
	// one prefix never is a title built with the other.
	for _, id := range []string{"fugaro-x-12345678", "fugaro-p-standard-git-0ca96e64"} {
		if got, other := TokenBucketConditionTitle(id), ProjectTokenBucketConditionTitle(id); got == other ||
			strings.HasPrefix(got, ProjectTokenBucketConditionTitlePrefix) || strings.HasPrefix(other, TokenBucketConditionTitlePrefix) {
			t.Errorf("the per-repository and project tokens/ titles are not told apart: %q vs %q", got, other)
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
	// The old form embeds the bucket the same way, so it takes the same guard.
	for _, bad := range []string{`fugaro-runs-proj-1234"`, "fugaro runs proj", ""} {
		t.Run("legacy "+bad, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("LegacyLauncherBucketCondition(%q) did not panic", bad)
				}
			}()
			LegacyLauncherBucketCondition(bad)
		})
	}
	// The old form is not the new one (the new expression *starts* with the
	// old clause — it is that clause plus tokens/ — so the two are told apart
	// by the whole string and by the title, which is what every reader
	// compares): a reader that confused them would accept the runs/-only
	// write as the widened grant, the failure this pair exists to name.
	b := "fugaro-runs-proj-1234"
	if got, want := LegacyLauncherBucketCondition(b), LauncherBucketCondition(b); got == want || LegacyLauncherBucketConditionTitle == LauncherBucketConditionTitle {
		t.Errorf("the launchers' old form and its replacement are not told apart: %q vs %q, titles %q vs %q", got, want, LegacyLauncherBucketConditionTitle, LauncherBucketConditionTitle)
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
// WorkflowVariant accepts (would not panic on): the same checks
// WorkflowVariant itself makes (config.WorkflowNameRE directly, and the
// package's own validWorkflowVariantToken, itself a thin wrapper over
// config.ClaudeAccountNameRE/config.MaxClaudeAccountNameLen), so a test
// case can decide up front whether to expect a result or a panic.
func workflowVariantValid(workflow, token string) bool {
	return config.WorkflowNameRE.MatchString(workflow) && validWorkflowVariantToken(token)
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
	labelRE := readTerraformNameVariableRegex(t)
	for _, p := range workflowVariantTrickyPairs {
		if !workflowVariantValid(p[0][0], p[0][1]) || !workflowVariantValid(p[1][0], p[1][1]) {
			continue // covered by TestWorkflowVariantRejectsOutOfCharsetInput instead
		}
		a, b := WorkflowVariant(p[0][0], p[0][1]), WorkflowVariant(p[1][0], p[1][1])
		if a == b {
			t.Errorf("WorkflowVariant(%q,%q) == WorkflowVariant(%q,%q) == %q", p[0][0], p[0][1], p[1][0], p[1][1], a)
			continue
		}
		for _, v := range []string{a, b} {
			if !labelRE.MatchString(v) {
				t.Errorf("WorkflowVariant produced %q, which does not match the Terraform module's name/label regex %s", v, labelRE)
			}
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
	// Every variant produced here is also checked against the Terraform
	// module's own label regex, not just one hand-picked long input (a
	// review finding: a mutation that only mishandled long tokens survived
	// when only a single maximum-length case was checked elsewhere).
	seen := map[string][2]string{}
	parts := []string{"a", "b", "a-b", "ab", "a-b-c", "x"}
	for _, w := range parts {
		for _, tok := range parts {
			v := WorkflowVariant(w, tok)
			if prev, dup := seen[v]; dup && prev != ([2]string{w, tok}) {
				t.Errorf("WorkflowVariant(%q,%q) == WorkflowVariant(%q,%q) == %q", w, tok, prev[0], prev[1], v)
			}
			seen[v] = [2]string{w, tok}
			if !labelRE.MatchString(v) {
				t.Errorf("WorkflowVariant(%q,%q) = %q does not match the Terraform module's name/label regex %s", w, tok, v, labelRE)
			}
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
// which config.ClaudeAccountNameRE refuses but a looser, workflow-shaped
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

// TestWorkflowVariantTokenLengthCap is the review finding that
// config.ClaudeAccountNameRE's `{0,30}` quantifier counts repetition units,
// not characters, so `^[a-z](?:[a-z0-9]|-[a-z0-9]){0,30}$` alone actually
// accepts tokens up to 61 characters (each of the 30 repeated units can be
// 2 bytes, a hyphen plus an alnum), not the 31-character bound every
// comment, TestWorkflowVariantAtMaximumLengths and the former Task 4 TODO
// on JobSADisplayName assumed. validWorkflowVariantToken checks the length
// separately in Go, against config.MaxClaudeAccountNameLen: this test
// exercises exactly the boundary and the reviewer's own counterexample,
// not just an all-alphanumeric 31-character token.
func TestWorkflowVariantTokenLengthCap(t *testing.T) {
	maxLenAllAlnum := strings.Repeat("a", 31)
	maxLenHyphenated := "a" + strings.Repeat("-a", 15) // 1 + 15*2 = 31 characters
	if !validWorkflowVariantToken(maxLenAllAlnum) {
		t.Errorf("a 31-character all-alphanumeric token was rejected: %q", maxLenAllAlnum)
	}
	if !validWorkflowVariantToken(maxLenHyphenated) {
		t.Errorf("a 31-character hyphenated token was rejected: %q", maxLenHyphenated)
	}

	oneOver := strings.Repeat("a", 32)
	if validWorkflowVariantToken(oneOver) {
		t.Errorf("a 32-character token was accepted: %q", oneOver)
	}
	// The reviewer's own counterexample: structurally valid (no leading,
	// trailing or doubled hyphen) but 61 characters — the length a bare
	// `{0,30}` quantifier on the hyphen-adjacency group would have allowed.
	adversarial := "a" + strings.Repeat("-a", 30)
	if len(adversarial) != 61 {
		t.Fatalf("test fixture adversarial is %d characters, want 61", len(adversarial))
	}
	if !config.ClaudeAccountNameRE.MatchString(adversarial) {
		t.Fatalf("test fixture adversarial does not even match the hyphen-adjacency structure; fixture is wrong")
	}
	if validWorkflowVariantToken(adversarial) {
		t.Errorf("a 61-character structurally-valid token was accepted (the exact bound-escape the review found): %q", adversarial)
	}

	// WorkflowVariant itself must refuse the over-length token (panic), not
	// just the lower-level helper.
	defer func() {
		if recover() == nil {
			t.Error("WorkflowVariant(\"build\", 61-character adversarial token) did not panic")
		}
	}()
	WorkflowVariant("build", adversarial)
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
	maxToken := strings.Repeat("a", 31)    // config.MaxClaudeAccountNameLen's longest
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
// (20) and config.MaxClaudeAccountNameLen (31) actually allow, not
// just short illustrative names, since a truncation bug only shows up once
// the join is long enough to hit it.
func TestWorkflowVariantAtMaximumLengths(t *testing.T) {
	labelRE := readTerraformNameVariableRegex(t)
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
	// valid, unique derived names. Every variant in the grid (not just one
	// hand-picked maximum-length pair) is checked against the Terraform
	// label regex: a review finding showed a mutation that mishandled long
	// tokens specifically (e.g. falling back to a dotted separator past a
	// length threshold) can pass if only a single long case is ever
	// checked against it.
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

			if !labelRE.MatchString(v) {
				t.Errorf("WorkflowVariant(%q,%q) = %q does not match the Terraform module's name/label regex %s", w, tok, v, labelRE)
			}
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

// TestJobSADisplayNameFitsIAMLimit pins the "at most 95" bound for today's
// one real caller (internal/infra/spec.go, which always passes the plain
// fugaro.yaml workflow name, config.WorkflowNameRE, <= 20 characters) and
// today's longest possible slug (task.Slug's own 63-character bound): the
// natural concatenation, unchanged, exactly as it is today — this is the
// golden-compatibility case internal/infra's own golden tfvars tests also
// cover indirectly, byte-identical before and after JobSADisplayName
// gained its length-cap fallback.
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
		t.Fatalf("JobSADisplayName length = %d, want the documented %d (11+63+1+20), meaning the fallback incorrectly triggered for a plain workflow name", len(name), want)
	}
}

// TestJobSADisplayNameFitsIAMLimitForVariants is the fix for a review
// finding: JobSADisplayName's old, unconditional "Fugaro job "+slug+"
// "+workflow concatenation goes over IAM's 100-character display-name
// limit once workflow is a gcp.WorkflowVariant string instead of a plain
// fugaro.yaml name — worst case 11+63+1+52 = 127, and with a 63-character
// slug alone, truncation starts once the variant passes roughly 25
// characters. JobSADisplayName now falls back to a truncated, hashed form
// once the natural concatenation would exceed the limit; this test
// exercises that fallback at the true maximum variant length
// (config.WorkflowNameRE's 20 + "_" + config.MaxClaudeAccountNameLen's 31
// = 52 characters) and checks it is deterministic and, as a practical (not
// load-bearing) guard, distinct across different variants of the same
// repository.
func TestJobSADisplayNameFitsIAMLimitForVariants(t *testing.T) {
	maxSlug := strings.Repeat("a", 46) + "-0123456789abcdef" // task.Slug's own 63-char shape
	maxVariant := WorkflowVariant(strings.Repeat("a", 20), strings.Repeat("b", 31))
	if len(maxVariant) != 52 {
		t.Fatalf("test fixture maxVariant is %d characters, want 52", len(maxVariant))
	}

	name := JobSADisplayName(maxSlug, maxVariant)
	if len(name) > jobSADisplayNameMaxLen {
		t.Fatalf("JobSADisplayName(63-char slug, 52-char variant) = %d characters, want <= %d (IAM's display-name limit): %q", len(name), jobSADisplayNameMaxLen, name)
	}
	if name2 := JobSADisplayName(maxSlug, maxVariant); name2 != name {
		t.Fatalf("JobSADisplayName is not deterministic: %q then %q for the same inputs", name, name2)
	}

	// A practical collision guard, not a hard invariant (service accounts
	// carry no labels; a display name is never parsed back): distinct
	// variants of the same repository still get distinct display names.
	other := JobSADisplayName(maxSlug, WorkflowVariant(strings.Repeat("a", 20), strings.Repeat("c", 31)))
	if other == name {
		t.Errorf("two distinct variants produced the same display name: %q", name)
	}

	// Shorter, more realistic variant lengths also stay within the limit.
	for _, tok := range []string{"a", "acme-personal", strings.Repeat("a", 25)} {
		v := WorkflowVariant("build", tok)
		if n := JobSADisplayName(maxSlug, v); len(n) > jobSADisplayNameMaxLen {
			t.Errorf("JobSADisplayName(63-char slug, %q) = %d characters, want <= %d: %q", v, len(n), jobSADisplayNameMaxLen, n)
		}
	}
}

// TestProjectSADisplayNameFitsIAMLimit is the same review finding for the
// project jobs (plan task 3.2): ProjectSADisplayName's natural concatenation,
// "Fugaro project job "+profile+" "+provider+" "+account, reaches 19+40+1+9+1
// +31 = 101 characters at the bounds a valid local config may name — a
// 40-character profile (config.ProjectNameRE, which projectpolicy.CheckProfiles
// holds a profile name to), the longest provider name (bitbucket, 9) and a
// Claude account at config.MaxClaudeAccountNameLen (31) — one over IAM's 100.
// A config that validates would then plan cleanly and fail the apply, so the
// fallback must trigger and the name must stay within the limit.
func TestProjectSADisplayNameFitsIAMLimit(t *testing.T) {
	maxProfile := strings.Repeat("a", 40) // config.ProjectNameRE's longest
	if !config.ProjectNameRE.MatchString(maxProfile) {
		t.Fatalf("test fixture maxProfile %q is not a valid profile name", maxProfile)
	}
	maxAccount := strings.Repeat("b", config.MaxClaudeAccountNameLen)
	const maxProvider = "bitbucket" // the longest provider Fugaro names a job for
	if natural := len("Fugaro project job ") + len(maxProfile) + 1 + len(maxProvider) + 1 + len(maxAccount); natural != 101 {
		t.Fatalf("the natural display name is %d characters, want the 101 this test is about", natural)
	}

	name := ProjectSADisplayName(maxProfile, maxProvider, maxAccount)
	if len(name) > jobSADisplayNameMaxLen {
		t.Fatalf("ProjectSADisplayName(40-char profile, %s, 31-char account) = %d characters, want <= %d (IAM's display-name limit): %q", maxProvider, len(name), jobSADisplayNameMaxLen, name)
	}
	if name2 := ProjectSADisplayName(maxProfile, maxProvider, maxAccount); name2 != name {
		t.Fatalf("ProjectSADisplayName is not deterministic: %q then %q for the same inputs", name, name2)
	}
	// A practical collision guard, not a hard invariant (service accounts
	// carry no labels; a display name is never parsed back): distinct triples
	// still get distinct display names.
	if other := ProjectSADisplayName(maxProfile, maxProvider, strings.Repeat("c", config.MaxClaudeAccountNameLen)); other == name {
		t.Errorf("two distinct accounts produced the same display name: %q", name)
	}

	// The empty account (an api-key or Vertex profile, projectjobs.go) and the
	// shorter, realistic triples are within the limit too, and a short one is
	// the natural form rather than the fallback: the hash suffix is what says
	// the readable part was cut.
	if got := ProjectSADisplayName("standard", "bitbucket", "default"); got != "Fugaro project job standard bitbucket default" {
		t.Errorf("ProjectSADisplayName(standard, bitbucket, default) = %q, want the natural form", got)
	}
	for _, tc := range []struct{ profile, provider, account string }{
		{"standard", "bitbucket", ""},
		{"standard", "bitbucket", "default"},
		{strings.Repeat("a", 25), "bitbucket", strings.Repeat("b", 20)},
		{maxProfile, "github", ""},
	} {
		if n := ProjectSADisplayName(tc.profile, tc.provider, tc.account); len(n) > jobSADisplayNameMaxLen {
			t.Errorf("ProjectSADisplayName(%q, %q, %q) = %d characters, want <= %d: %q", tc.profile, tc.provider, tc.account, len(n), jobSADisplayNameMaxLen, n)
		}
	}
}

func TestDefaultRegistryHost(t *testing.T) {
	if got := DefaultRegistryHost("us-east5", "my-proj"); got != "us-east5-docker.pkg.dev/my-proj" || !registryHostRE.MatchString(got) {
		t.Errorf("DefaultRegistryHost = %q, want us-east5-docker.pkg.dev/my-proj", got)
	}
}

// TestJobSADisplayNameMaxLengthIsPinned pins the EXACT truncated display name
// for the longest slug and variant. internal/infra/discover.go compares a
// service account's display name exactly as its ownership mark and reports
// anything else as a ForeignError, so changing the truncation, the hash width
// or the hash input would make discovery refuse every existing named-account
// service account. If this test must change, the change needs a migration.
func TestJobSADisplayNameMaxLengthIsPinned(t *testing.T) {
	maxSlug := strings.Repeat("a", 46) + "-0123456789abcdef"
	maxVariant := WorkflowVariant(strings.Repeat("a", 20), strings.Repeat("b", 31))
	const want = "Fugaro job aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-0123456789abcdef aaaaaaaaaaaaaaaa d4964ee5"
	if got := JobSADisplayName(maxSlug, maxVariant); got != want {
		t.Fatalf("JobSADisplayName(max slug, max variant) = %q, want the pinned %q (discover.go compares it exactly)", got, want)
	}
}

func TestProjectSecretIDDistinctFromRepoSecretID(t *testing.T) {
	a := ProjectSecretID("acme", "anthropic-api-key")
	b := SecretID("some-repo-slug-0123456789abcdef", "anthropic-api-key")
	if a == b {
		t.Fatal("a project secret ID collided with a repo secret ID")
	}
	if ProjectSecretID("acme", "anthropic-api-key") != ProjectSecretID("acme", "anthropic-api-key") {
		t.Fatal("ProjectSecretID is not pure")
	}
	if ProjectSecretID("acme", "x") == ProjectSecretID("acme2", "x") {
		t.Fatal("two different projects collided")
	}
	// A Fugaro project name that happens to equal some repository's own
	// slug must not make the two functions collide on the same byte string
	// (the domain separation this function's own comment names).
	if ProjectSecretID("acme/app", "x") == SecretID("acme/app", "x") {
		t.Fatal("ProjectSecretID collided with SecretID for a slug-shaped project name")
	}
}

// The project job's name (plan task 3.2 step 1) has three properties, and it
// has no inverse: it is written from a (profile, provider, account) the
// policy holds and matched by computing it, never parsed (R2-5).
func TestProjectJobNameIsStableAndDistinct(t *testing.T) {
	// Stable: the same triple is always the same name, and the name is one
	// Cloud Run and IAM accept.
	triples := []struct{ profile, provider, account string }{
		{"standard", "github", "default"},
		{"standard", "bitbucket", "default"},
		{"heavy", "github", ""}, // an api-key job: no account segment
		{"a-hyphenated-profile", "bitbucket", "alice"},
		{strings.Repeat("p", 40), "bitbucket", "default"}, // truncated readable part
		{"standard", "bitbucket", strings.Repeat("a", 31)},
		{"p-standard", "bitbucket", "default"}, // confusable with the prefix
		{"standard", "bitbucket", "default-default"},
	}
	seen := map[string]string{}
	for _, tr := range triples {
		name := ProjectJobName(tr.profile, tr.provider, tr.account)
		if got := ProjectJobName(tr.profile, tr.provider, tr.account); got != name {
			t.Errorf("ProjectJobName(%q, %q, %q) is not stable: %q then %q", tr.profile, tr.provider, tr.account, name, got)
		}
		if !jobNameRE.MatchString(name) {
			t.Errorf("ProjectJobName(%q, %q, %q) = %q, which is not a Cloud Run job name", tr.profile, tr.provider, tr.account, name)
		}
		if !strings.HasPrefix(name, "fugaro-p-") {
			t.Errorf("ProjectJobName(%q, %q, %q) = %q, which does not carry the project prefix", tr.profile, tr.provider, tr.account, name)
		}
		// The readable part is the triple, or as much of it as fits: it is a
		// prefix of the whole thing (never a rewording), and a short triple
		// is not truncated at all.
		want := "fugaro-p-" + tr.profile + "-" + tr.provider
		if tr.account != "" {
			want += "-" + tr.account
		}
		stem := strings.TrimSuffix(name, name[len(name)-1-jobHashHex:])
		if !strings.HasPrefix(want, stem) || stem == "fugaro-p-" {
			t.Errorf("ProjectJobName(%q, %q, %q) = %q: the readable part %q is not a prefix of %q", tr.profile, tr.provider, tr.account, name, stem, want)
		}
		if len(want) <= maxJobName-1-jobHashHex && stem != want {
			t.Errorf("ProjectJobName(%q, %q, %q) = %q: %q fits, so nothing should be truncated", tr.profile, tr.provider, tr.account, name, want)
		}
		if strings.Contains(name, "--") {
			t.Errorf("ProjectJobName(%q, %q, %q) = %q, which has an empty segment", tr.profile, tr.provider, tr.account, name)
		}
		saID := ProjectServiceAccountID(tr.profile, tr.provider, tr.account)
		if !saIDRE.MatchString(saID) {
			t.Errorf("ProjectServiceAccountID(%q, %q, %q) = %q, which is not a service account ID", tr.profile, tr.provider, tr.account, saID)
		}
		for _, k := range []string{name, saID} {
			if prev, dup := seen[k]; dup {
				t.Errorf("%q is the name of two triples: %v", k, prev)
			}
			seen[k] = tr.profile + "/" + tr.provider + "/" + tr.account
		}
	}
	// A legacy per-repository name is never a project name for the fixture
	// policy: the two namespaces cannot collide, so a run that matched
	// CLOUD_RUN_JOB against a computed project name can never match a
	// repository's job by accident.
	slug := realSlug(t, "bitbucket", "acme/sandbox")
	for _, tr := range triples {
		for _, wf := range []string{"web", "api", "standard", "default"} {
			if legacy := JobName(slug, wf); legacy == ProjectJobName(tr.profile, tr.provider, tr.account) {
				t.Errorf("ProjectJobName(%q, %q, %q) equals the legacy job name %q", tr.profile, tr.provider, tr.account, legacy)
			}
		}
	}
}

// The widened condition (plan task 3.2 step 2): one clause per prefix, no
// slug, and it is not the per-repository builder called with an empty slug.
func TestProjectBucketConditionHasNoSlug(t *testing.T) {
	bucket := "fugaro-runs-proj-1234"
	got := ProjectBucketCondition(bucket, JobBucketPrefixes)
	want := `resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/runs/") || ` +
		`resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/cache/") || ` +
		`resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/locks/")`
	if got != want {
		t.Fatalf("ProjectBucketCondition = %q, want %q", got, want)
	}
	if clauses := strings.Split(got, " || "); len(clauses) != len(JobBucketPrefixes) {
		t.Errorf("%d clauses for %d prefixes", len(clauses), len(JobBucketPrefixes))
	}
	for _, clause := range strings.Split(got, " || ") {
		// Everything after objects/ must be the prefix and nothing else: a
		// fourth segment would be a slug.
		rest, ok := strings.CutPrefix(clause, `resource.name.startsWith("projects/_/buckets/`+bucket+`/objects/`)
		if !ok {
			t.Fatalf("clause %q is not a prefix clause of %s", clause, bucket)
		}
		rest = strings.TrimSuffix(rest, `")`)
		prefix, _, ok2 := strings.Cut(rest, "/")
		if !ok2 || !slices.Contains(JobBucketPrefixes, prefix) {
			t.Errorf("clause %q names more than the prefix", clause)
		}
	}
	// A slug reaches a repository's own prefix only; the widened condition
	// reaches every repository's, which is the regression D4 documents.
	if perSlug := BucketCondition(bucket, JobBucketPrefixes, "acme-sandbox-0123456789abcdef"); got == perSlug {
		t.Error("the project condition equals a per-slug one")
	}
	if strings.Contains(ProjectBucketCondition(bucket, JobBucketPrefixes), "acme-sandbox") {
		t.Error("the project condition names a repository")
	}
	// And the per-repository builder refuses to produce the widened one: an
	// empty slug has nothing to scope the grant to, and the clause it used to
	// build was this condition's, byte for byte — a grant over every
	// repository's objects under the prefix, from a call whose whole point is
	// to be narrower than that. A panic is the refusal: it is a programming
	// error (a slug comes from task.Slug and is never empty), and returning
	// the wider condition is the one thing a caller must not be handed.
	func() {
		defer func() {
			if recover() == nil {
				t.Errorf("BucketCondition(%q, prefixes, \"\") returned %q instead of refusing: the per-repository condition must never be the widened one", bucket, BucketCondition(bucket, JobBucketPrefixes, ""))
			}
		}()
		BucketCondition(bucket, JobBucketPrefixes, "")
	}()
}

// TestTheTokenPrefixIsTheTokenPackages: the grants name the prefix the token
// objects live under (token.ObjectKey), and the job's prefixes never include
// it: the job's objectUser on tokens/ would let a peer run delete a queued
// run's token (docs/design/budget-token-channel.md §7.2, D16). The prefix is
// one constant on both sides, so a move of the object without the grants (or
// the reverse) fails here.
func TestTheTokenPrefixIsTheTokenPackages(t *testing.T) {
	if TokenBucketPrefix != token.ObjectPrefix || !strings.HasPrefix(token.ObjectKey("s", "r"), TokenBucketPrefix+"/s/") {
		t.Fatalf("TokenBucketPrefix = %q, token.ObjectKey = %q", TokenBucketPrefix, token.ObjectKey("s", "r"))
	}
	for _, p := range append(append([]string{}, JobBucketPrefixes...), BuildBucketPrefixes...) {
		if p == TokenBucketPrefix {
			t.Fatalf("%q is a prefix an account may write", p)
		}
	}
}

// TestEveryNameInThisFileIsUsed: every exported declaration of names.go is
// named somewhere outside it. The file is the one definition of every name the
// deployer, the install guard, doctor and the readers compare against, so a
// second spelling of one of them is not dead weight but a trap: it is a
// constant or a builder that reads like the definition and is not the one
// anything compares byte for byte. This is what a merge leaves behind when two
// branches fixed the same thing — the pre-0.9.0 launcher condition was
// declared twice here, once as the legacy pair and once as a second copy of
// it, after the two lines met.
//
// The name of a declaration this test is about must not be written in this
// file: naming it here would count as a use of it (the walk reads every other
// Go file, tests included), which is the one way to defeat the check.
//
// Mutation (run, restore): re-adding any exported declaration of names.go that
// no other file refers to — the duplicate pair, or a copy of any builder under
// a second name — fails this test naming it.
func TestEveryNameInThisFileIsUsed(t *testing.T) {
	fset := gotoken.NewFileSet()
	f, err := parser.ParseFile(fset, "names.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if x.Name.IsExported() {
				declared = append(declared, x.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range x.Specs {
				switch sp := s.(type) {
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						if n.IsExported() {
							declared = append(declared, n.Name)
						}
					}
				case *ast.TypeSpec:
					if sp.Name.IsExported() {
						declared = append(declared, sp.Name.Name)
					}
				}
			}
		}
	}
	if len(declared) < 20 {
		t.Fatalf("names.go declares only %d exported names: this test is reading the wrong file", len(declared))
	}
	// Every other Go file of the repository, tests included: a name the
	// deployer, the guard, doctor, a reader or a test uses is used, and the
	// point is only that names.go declares nothing no other file has heard of
	// — which is what the pair the merge left behind was.
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(root, "internal", "backend", "gcp", "names.go")
	// Compiled once: the walk visits thousands of files, and a pattern built
	// per file per name is the whole cost of this test.
	refs := make([]struct {
		name string
		re   *regexp.Regexp
	}, 0, len(declared))
	for _, name := range declared {
		refs = append(refs, struct {
			name string
			re   *regexp.Regexp
		}{name, regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)})
	}
	used := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".terraform", "dist", "build":
				return fs.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go":
		default:
			return nil
		}
		if path == self {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, r := range refs {
			if !used[r.name] && r.re.Match(data) {
				used[r.name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range declared {
		if !used[name] {
			t.Errorf("names.go declares %s and no other file in the repository names it: a second spelling of a name this file is the definition of is what a merge leaves behind, and a reader compares against the wrong one", name)
		}
	}
}
