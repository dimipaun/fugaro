package gcp

import (
	"regexp"
	"strings"
	"testing"
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
		{JobName("acme-app", "web"), "fugaro-acme-app-web-2dc53d2b"},
		{ServiceAccountID("acme-app", "web"), "fugaro-acme-app-web-2dc53d2b"},
		{SecretID("acme-app", "bitbucket-token"), "fugaro-acme-app-bitbucket-token-0efe1f9a"},
		{ImageName("us-east5-docker.pkg.dev/p/fugaro", "acme-my.app", "web"), "us-east5-docker.pkg.dev/p/fugaro/acme-my-app-web-1f52a584"},
		// Storage slugs may hold '.' and '_'; Cloud Run names may not.
		{JobName("acme-my.app_x", "web"), "fugaro-acme-my-app-x-web-63dbfb98"},
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
