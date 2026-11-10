package imagebake

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// reqApp is the request a build of the "app" entry from acme/app's checkout
// makes, with the map and the switch each test overrides.
// bakeOn and bakeOff are D1901's switch answered; a Request that leaves Bake
// nil is the project that answered nothing, which is on exactly as bakeOn is
// (projectpolicy.Policy.BakesRepos).
func bakeOn() *bool  { v := true; return &v }
func bakeOff() *bool { v := false; return &v }

func reqApp() Request {
	return Request{
		Entry: "app",
		Repo: Repo{
			Repo: "acme/app", Slug: "app-1234abcd",
			URL: "https://github.com/acme/app.git", Branch: "main",
		},
		Map:  map[string]string{"acme/app": "app"},
		Bake: bakeOn(),
	}
}

// TestDecideBakesTheBuildsOwnRepo: the repository the build runs for, and only
// when the map puts it on the entry being built. It is O3's reading (b): the
// build's credential reaches this one repository, so this is the one an image
// a build submits can bake.
func TestDecideBakesTheBuildsOwnRepo(t *testing.T) {
	p := Decide(reqApp())
	if p.Baked == nil {
		t.Fatalf("Decide baked nothing (%s): the map puts acme/app on the app entry the build runs for, so its checkout is this build's own to bake", p.Reason)
	}
	if p.Baked.Slug != "app-1234abcd" {
		t.Errorf("the plan bakes the slug %q, want app-1234abcd", p.Baked.Slug)
	}
	dir, err := Path(p.Baked.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/work/baked/app-1234abcd" {
		t.Errorf("Path(%q) = %q, want /work/baked/app-1234abcd", p.Baked.Slug, dir)
	}
	if p.Reason != "" {
		t.Errorf("a plan that bakes carries the reason %q, want none", p.Reason)
	}
}

// TestDecideClonesTheOtherReposAtStart: the repositories the image serves that
// are not this build's own are named as the clone-at-start half, so the line a
// build prints does not read as though every repo of the project was baked.
func TestDecideClonesTheOtherReposAtStart(t *testing.T) {
	req := reqApp()
	req.Map = map[string]string{"acme/app": "app", "acme/api": "app", "acme/web": "app", "acme/tool": "other"}
	p := Decide(req)
	if p.Baked == nil {
		t.Fatalf("Decide baked nothing: %s", p.Reason)
	}
	want := []string{"acme/api", "acme/web"}
	if len(p.Cloned) != len(want) {
		t.Fatalf("the plan clones %v at start, want %v", p.Cloned, want)
	}
	for i, got := range p.Cloned {
		if got != want[i] {
			t.Fatalf("the plan clones %v at start, want %v", p.Cloned, want)
		}
	}
	// And the same list is what a plan that bakes nothing reports: the entry's
	// own jobs still clone at start, whichever way the decision went.
	req.Bake = bakeOff()
	if p := Decide(req); len(p.Cloned) != len(want) {
		t.Errorf("with baking off the plan clones %v at start, want %v: which repositories clone does not depend on the switch", p.Cloned, want)
	}
}

// TestDecideBakesNothingForAnotherImage: the entry being built is not the one
// this repository runs, so its jobs never pull the image a bake would land in.
func TestDecideBakesNothingForAnotherImage(t *testing.T) {
	for name, req := range map[string]Request{
		"the map puts the repository on another entry": func() Request {
			r := reqApp()
			r.Map = map[string]string{"acme/app": "other"}
			return r
		}(),
		"the map names no repository at all": func() Request {
			r := reqApp()
			r.Map = nil
			return r
		}(),
		"the repository's own workflow names another entry": func() Request {
			r := reqApp()
			r.Reference = "web"
			return r
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			p := Decide(req)
			if p.Baked != nil {
				t.Fatalf("Decide baked the checkout into the %s entry: a bake in an image these jobs never pull is work nobody reads", req.Entry)
			}
			if p.Reason == "" {
				t.Error("a plan that bakes nothing gives no reason, so a build has nothing to print")
			}
		})
	}
}

// TestDecideReferenceBeatsTheMap: T1 reading (b) — a repository that names an
// entry in its own workflow runs that entry's image whatever the map says, so
// the bake follows the reference. The reference equal to the entry being built
// is the case that still bakes.
func TestDecideReferenceBeatsTheMap(t *testing.T) {
	req := reqApp()
	req.Map = map[string]string{"acme/app": "other"}
	req.Reference = "app"
	if p := Decide(req); p.Baked == nil {
		t.Fatalf("Decide baked nothing (%s): the repository's own workflow names the entry being built, which is where its jobs run", p.Reason)
	}
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
	for name, req := range map[string]Request{
		"the map puts the repository on the entry": reqApp(),
		"the map names nothing":                    func() Request { r := reqApp(); r.Map = nil; return r }(),
		"the entry is shared with another repository": func() Request {
			r := reqApp()
			r.Map = map[string]string{"acme/app": "app", "acme/web": "app"}
			return r
		}(),
		"the checkout's own workflow names the entry": func() Request { r := reqApp(); r.Map = nil; r.Reference = "app"; return r }(),
	} {
		t.Run(name, func(t *testing.T) {
			req.Bake = nil
			unset := Decide(req)
			req.Bake = &on
			answered := Decide(req)
			if unset.Baked == nil != (answered.Baked == nil) {
				t.Fatalf("Decide baked %v with the switch unset and %v with it answered yes: one field, two meanings", unset.Baked, answered.Baked)
			}
			if unset.Baked != nil {
				return
			}
			if unset.Notable != answered.Notable || unset.Reason != answered.Reason {
				t.Errorf("Decide says %q (notable %v) with the switch unset and %q (notable %v) with it answered yes", unset.Reason, unset.Notable, answered.Reason, answered.Notable)
			}
		})
	}
}

// TestDecideBakesASharedImageAndNamesTheReaders: the entry serves another
// repository as well, which is F18's boundary — whatever is baked into the
// image is source that repository's jobs can read. The bake happens (the
// switch is on unless an operator turns it off), and the plan names the
// repositories that can read the checkout, which is what the line a build
// prints says out loud.
func TestDecideBakesASharedImageAndNamesTheReaders(t *testing.T) {
	req := reqApp()
	req.Bake = nil
	req.Map = map[string]string{"acme/app": "app", "acme/web": "app"}
	p := Decide(req)
	if p.Baked == nil {
		t.Fatalf("Decide baked nothing (%s): an unset switch is on", p.Reason)
	}
	if len(p.Cloned) != 1 || p.Cloned[0] != "acme/web" {
		t.Errorf("the plan clones %v at start, want [acme/web]: the note has to name who else reads the baked checkout", p.Cloned)
	}
	// Turning it off is the operator's own answer to F18, and it is the one
	// thing that keeps the checkout out of a shared image.
	req.Bake = bakeOff()
	if p := Decide(req); p.Baked != nil {
		t.Error("Decide baked the checkout into a shared image with bake_repos off")
	}
}

// TestDecideBakeSwitchOff: D1901's switch, off, keeps the checkout out of every
// image and says so.
func TestDecideBakeSwitchOff(t *testing.T) {
	req := reqApp()
	req.Bake = bakeOff()
	p := Decide(req)
	if p.Baked != nil {
		t.Fatal("Decide baked a checkout with bake_repos off")
	}
	if !strings.Contains(p.Reason, "bake_repos") {
		t.Errorf("the reason %q does not name the switch that turned the bake off, so an operator cannot tell it from a map that does not name the repository", p.Reason)
	}
}

// TestDecideRefusesASlugThatIsNotOne: the slug is spelled into a generated
// Dockerfile and compared against the path a reader opens, so a slug that is
// not a slug bakes nothing rather than writing a path out of Root.
func TestDecideRefusesASlugThatIsNotOne(t *testing.T) {
	for _, slug := range []string{"", "../escape", "a/b", ".hidden", "UPPER", strings.Repeat("a", 129)} {
		req := reqApp()
		req.Repo.Slug = slug
		p := Decide(req)
		if p.Baked != nil {
			t.Errorf("Decide baked the slug %q, want a refusal: Path refuses it, so the stage would name a directory no reader opens", slug)
		}
		if _, err := Path(slug); err == nil {
			t.Errorf("Path(%q) accepted a slug that is not one", slug)
		}
	}
}

// TestStageClonesWithTheBuildsOwnCredential: the stage is the generated
// Dockerfile clone. What it must never do is carry a secret into a layer: the
// credential is the BuildKit secret mount the catalog build already provides
// (D1150, F14), and no ARG, ENV or COPY names a token.
func TestStageClonesWithTheBuildsOwnCredential(t *testing.T) {
	stage, err := Stage(reqApp().Repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--mount=type=secret,id=git-credentials,uid=1000,required=false",
		"credential.helper='store --file=/run/secrets/git-credentials'",
		"case \"$repo\" in 'https://github.com/acme/app') ;;",
		"clone --quiet --branch \"$BASE_BRANCH\" --single-branch -- \"$REPO_URL\" /work/baked/app-1234abcd",
		"reset --quiet --hard \"$commit\"",
		"/usr/local/lib/fugaro/finalize-checkout /work/baked/app-1234abcd",
	} {
		if !strings.Contains(stage, want) {
			t.Errorf("the generated stage does not carry %q:\n%s", want, stage)
		}
	}
	// The token is never a build argument, an ENV or a COPY: those reach a
	// layer, and a layer is what everybody who pulls the image reads.
	for _, banned := range []string{"ENV ", "COPY ", "ARG GIT_TOKEN", "ARG TOKEN", "ARG GITHUB_TOKEN", "ARG SECRET"} {
		if strings.Contains(stage, banned) {
			t.Errorf("the generated stage carries %q:\n%s", banned, stage)
		}
	}
	// The clone URL is the credential-free one the catalog build passes, and
	// the stage never rewrites the remote to a URL of its own: an origin with
	// userinfo is what finalize-checkout refuses.
	if strings.Contains(stage, "@") {
		t.Errorf("the generated stage mentions a URL that could carry userinfo:\n%s", stage)
	}
	// It runs as the image's own user: a USER line here would change whom every
	// job on this image runs as.
	if strings.Contains(stage, "\nUSER ") {
		t.Errorf("the generated stage switches user:\n%s", stage)
	}
	if !strings.Contains(stage, "case \"$FUGARO_COMMIT\" in *[!0-9a-f]*)") {
		t.Errorf("the generated stage does not hold FUGARO_COMMIT to a commit ID:\n%s", stage)
	}
}

// TestStageRefusesAnotherRepositorysURL: the stage clones the URL it was
// composed for and no other. A definition is rebuilt by whichever repository the
// nightly check assigns its entry to (the repository its source names, or the
// project's first when it has none), which is not always the checkout that
// composed it: without the guard that rebuild would clone one repository into
// another's slug path, under the marker and the tag the composed definition
// gave it, and the smoke — which reads the directory out of the definition and
// the origin out of the job — would pass on the pairing.
func TestStageRefusesAnotherRepositorysURL(t *testing.T) {
	stage, err := Stage(reqApp().Repo)
	if err != nil {
		t.Fatal(err)
	}
	// The stage runs the guard the test below runs; what is pinned here is
	// that this stage carries it and not a second spelling of the rule.
	if !strings.Contains(stage, repoURLGuard(reqApp().Repo.URL)) {
		t.Fatalf("the stage does not hold REPO_URL to the repository it was composed for:\n%s", stage)
	}
	if !strings.Contains(stage, "a rebuild of the entry has to run as the repository whose checkout it bakes") {
		t.Errorf("the guard's refusal does not say what went wrong or what to do:\n%s", stage)
	}
	// And a URL that may not be spelled into a definition is refused rather
	// than written: the object is read by every build and job of the entry, so
	// a URL with a credential in it would be copied into the bucket.
	for name, bad := range map[string]string{
		"a URL with userinfo":     "https://user:token@github.com/acme/app.git",
		"a URL that is not https": "http://github.com/acme/app.git",
		"no host at all":          "https:///acme/app.git",
		"a shell-quoting hazard":  "https://github.com/acme/it's.git",
		"a newline":               "https://github.com/acme/app.git\nRUN echo hi",
	} {
		t.Run(name, func(t *testing.T) {
			r := reqApp().Repo
			r.URL = bad
			if stage, err := Stage(r); err == nil {
				t.Fatalf("Stage spelled %q into a definition:\n%s", bad, stage)
			}
		})
	}
}

// TestRepoURLGuardHoldsTheRepositoryNotTheSpelling: the guard is shell, and
// what it has to do is what the text of it cannot promise — accept every
// spelling git clones one repository from and refuse another repository. The
// build's REPO_URL is not the composed one: a catalog build passes
// repourl.CloneURL's canonical form (https://host/owner/name.git) while the
// definition was composed from the checkout's origin, which git spells with or
// without ".git", with or without a trailing slash, and in any case. A guard
// comparing the two strings refused the nightly rebuild of such a definition
// inside the build, every night, after it was submitted and paid for.
func TestRepoURLGuardHoldsTheRepositoryNotTheSpelling(t *testing.T) {
	// Composed from an origin with none of the three canonical marks.
	guard := repoURLGuard("https://Bitbucket.org/acme/Other")
	for name, tc := range map[string]struct {
		repoURL string
		held    bool
	}{
		"the canonical form the nightly check passes": {"https://bitbucket.org/acme/other.git", true},
		"the origin it was composed from":             {"https://Bitbucket.org/acme/Other", true},
		"a trailing slash":                            {"https://bitbucket.org/acme/other.git/", true},
		"another repository":                          {"https://bitbucket.org/acme/elsewhere.git", false},
		"another repository, spelt like the origin":   {"https://Bitbucket.org/Acme/Elsewhere", false},
		"another repository of another host":          {"https://github.com/acme/other.git", false},
		"a repository whose name starts the same":     {"https://bitbucket.org/acme/other-repo.git", false},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", guard)
			cmd.Env = append(os.Environ(), "REPO_URL="+tc.repoURL)
			out, err := cmd.CombinedOutput()
			switch {
			case tc.held && err != nil:
				t.Fatalf("the guard refused %s, which is the repository this definition bakes: %v\n%s", tc.repoURL, err, out)
			case !tc.held && err == nil:
				t.Fatalf("the guard accepted %s, and the rebuild would bake it into this definition's slug path", tc.repoURL)
			case !tc.held && !strings.Contains(string(out), "REPO_URL is not it"):
				t.Fatalf("the guard refused %s without saying why:\n%s", tc.repoURL, out)
			}
		})
	}
}

// TestBakedSlugIsTheMarkerRepository: a reader that has to know WHICH
// repository a definition bakes — the nightly rebuild, which runs as one
// repository and cannot reproduce another's definition — reads the slug, and
// gets nothing from a definition this package did not compose.
func TestBakedSlugIsTheMarkerRepository(t *testing.T) {
	composed, err := Append([]byte("FROM scratch\n"), Decide(reqApp()))
	if err != nil {
		t.Fatal(err)
	}
	slug, ok := BakedSlug(composed)
	if !ok || slug != "app-1234abcd" {
		t.Errorf("BakedSlug(composed) = %q, %v; want app-1234abcd, true", slug, ok)
	}
	for name, def := range map[string]string{
		"an entry's own definition":   "FROM scratch\nRUN echo hi\n",
		"a marker outside Root":       markerPrefix + "/work/repo\n",
		"a marker with no repository": markerPrefix + Root + "/\n",
	} {
		if slug, ok := BakedSlug([]byte(def)); ok {
			t.Errorf("BakedSlug(%s) = %q, true; want no repository", name, slug)
		}
	}
}

// TestBakedSlugIsTheLastMarker: a definition can carry more than one marker —
// composing a definition that already bakes composes a second stage onto it,
// and an entry's own bytes may spell the line by hand. The stage that runs is
// the last one, so the checkout a reader looks at is the last marker's; reading
// the first would open the directory of a stage an earlier one replaced.
func TestBakedSlugIsTheLastMarker(t *testing.T) {
	def := markerPrefix + "/work/baked/first-0000aaaa\n" +
		"FROM scratch\n" +
		markerPrefix + "/work/baked/second-1111bbbb\n"
	slug, ok := BakedSlug([]byte(def))
	if !ok || slug != "second-1111bbbb" {
		t.Errorf("BakedSlug(%q) = %q, %v; want second-1111bbbb, true: the last stage is the one that runs", def, slug, ok)
	}
	// A trailing marker that names nothing this package wrote is skipped, and
	// the one before it answers.
	def = markerPrefix + "/work/baked/first-0000aaaa\n" + markerPrefix + "/work/repo\n"
	if slug, ok := BakedSlug([]byte(def)); !ok || slug != "first-0000aaaa" {
		t.Errorf("BakedSlug(%q) = %q, %v; want first-0000aaaa, true", def, slug, ok)
	}
}

// TestAppendAndBakedDir: the composition is the entry's own bytes with the
// stage on the end, a definition that bakes nothing is returned untouched, and
// the directory the marker names is the one the build wrote — which is what the
// smoke spec of that build has to check.
func TestAppendAndBakedDir(t *testing.T) {
	own := []byte("FROM scratch\nRUN echo hi\n")
	p := Decide(reqApp())
	out, err := Append(own, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), string(own)) {
		t.Errorf("the composed definition does not begin with the entry's own bytes:\n%s", out)
	}
	if dir, ok := BakedDir(out); !ok || dir != "/work/baked/app-1234abcd" {
		t.Errorf("BakedDir(composed) = %q, %v; want /work/baked/app-1234abcd, true", dir, ok)
	}
	// An entry's own definition is not one of these: nothing is baked unless a
	// build composed a stage into it.
	if dir, ok := BakedDir(own); ok {
		t.Errorf("BakedDir(a definition with no generated stage) = %q, true; want no directory", dir)
	}
	// A marker that names a path out of Root is not one, so a definition that
	// happens to carry such a line cannot redirect the smoke.
	if _, ok := BakedDir([]byte(markerPrefix + "/work/repo\n")); ok {
		t.Error("BakedDir accepted a marker naming a path outside Root: a definition's own text can then move the smoke's checkout")
	}
	// A definition that bakes nothing comes back byte for byte, which is what
	// keeps an installation that names no entry and is mapped to none exactly
	// what it had.
	off, err := Append(own, Decide(func() Request { r := reqApp(); r.Bake = bakeOff(); return r }()))
	if err != nil {
		t.Fatal(err)
	}
	if string(off) != string(own) {
		t.Errorf("a build that bakes nothing wrote %q, want the entry's own bytes %q", off, own)
	}
	// A definition with no trailing newline still gets a stage of its own.
	nl, err := Append([]byte("FROM scratch"), p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nl), "FROM scratch\n"+markerPrefix) {
		t.Errorf("the stage was appended without a line of its own:\n%s", nl)
	}
}
