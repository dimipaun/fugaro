package gcp

import (
	"math/rand/v2"
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
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
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
