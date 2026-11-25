package imagebake_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// This file lives outside the package because of an import edge and not because
// of what it tests: internal/projectpolicy reads internal/backend/gcp, which reads
// this package, so an internal test file of this package may not name the policy
// at all (and this package's own code reads it through the value a caller hands
// it, imagebake.Request.Bake).

// bakeRepoSpec is one of the project's repositories as a build resolves it, and
// bakeRequest is the request a build of the "app" entry from acme/app's checkout
// makes: the shape the package's own test file builds, written again here because
// the two files are two packages.
func bakeRepoSpec(name, slug string) imagebake.Repo {
	return imagebake.Repo{Repo: name, Slug: slug, URL: "https://github.com/" + name + ".git", Branch: "main"}
}

func bakeRequest() imagebake.Request {
	return imagebake.Request{
		Entry: "app",
		Repo:  bakeRepoSpec("acme/app", "app-1234abcd"),
		Map:   map[string]string{"acme/app": "app"},
		Repos: []imagebake.Repo{bakeRepoSpec("acme/app", "app-1234abcd")},
		Bake:  bakeOn(),
	}
}

func bakeOn() *bool { v := true; return &v }

func bakeNames(rs []imagebake.Repo) string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Repo
	}
	return strings.Join(out, ", ")
}

// TestDecideAnUnsetSwitchIsOn: bake_repos has one meaning in this repository
// (projectpolicy.Policy.BakesRepos, localcfg.BakeReposEffective): absent is
// yes, and only a written false turns the bake off. A build that read the
// absent case any other way would be a second meaning for one field — the same
// published node telling a settings reader "on" and a build "nothing is
// baked".
func TestDecideAnUnsetSwitchIsOn(t *testing.T) {
	var absent projectpolicy.Policy
	on := absent.BakesRepos()
	if !on {
		t.Fatal("projectpolicy says an absent bake_repos is off: this test's premise is the opposite")
	}
	for name, req := range map[string]imagebake.Request{
		"the map puts the repository on the entry": bakeRequest(),
		"the map names nothing":                    func() imagebake.Request { r := bakeRequest(); r.Map = nil; return r }(),
		"the entry serves another repository too": func() imagebake.Request {
			r := bakeRequest()
			r.Map = map[string]string{"acme/app": "app", "acme/web": "app"}
			r.Repos = append(r.Repos, bakeRepoSpec("acme/web", "web-2222bbbb"))
			return r
		}(),
		"the checkout's own workflow names the entry": func() imagebake.Request { r := bakeRequest(); r.Map = nil; r.Reference = "app"; return r }(),
	} {
		t.Run(name, func(t *testing.T) {
			req.Bake = nil
			unset := imagebake.Decide(req)
			req.Bake = &on
			answered := imagebake.Decide(req)
			if (len(unset.Baked) == 0) != (len(answered.Baked) == 0) {
				t.Fatalf("Decide baked %q with the switch unset and %q with it answered yes: one field, two meanings", bakeNames(unset.Baked), bakeNames(answered.Baked))
			}
			if len(unset.Baked) > 0 {
				return
			}
			if unset.Notable != answered.Notable || unset.Reason != answered.Reason {
				t.Errorf("Decide says %q (notable %v) with the switch unset and %q (notable %v) with it answered yes", unset.Reason, unset.Notable, answered.Reason, answered.Notable)
			}
		})
	}
}
