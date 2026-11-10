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

// TestDecideBakesNothingIntoASharedImage: the entry serves other repositories
// as well, which is F18's boundary — whatever is baked into the image is source
// their jobs can read. O3's reading (b) bakes "only the repos that share the
// image's own repository grant — typically none for a shared image", so nothing
// is baked there, and the plan names the repositories that made it shared so
// the line a build prints says who would have read it.
//
// The switch is not what decides this: bake_repos: true asks for a bake where
// one may land, and no answer to the switch puts a checkout into an image other
// repositories' jobs start.
func TestDecideBakesNothingIntoASharedImage(t *testing.T) {
	want := []string{"acme/api", "acme/web"}
	for name, bake := range map[string]*bool{"the project answered nothing": nil, "the project asked for the bake": bakeOn()} {
		t.Run(name, func(t *testing.T) {
			req := reqApp()
			req.Bake = bake
			req.Map = map[string]string{"acme/app": "app", "acme/api": "app", "acme/web": "app", "acme/tool": "other"}
			p := Decide(req)
			if p.Baked != nil {
				t.Fatalf("Decide baked the checkout into an image acme/api and acme/web run: their jobs can read it (F18), and O3 (b) bakes nothing into a shared image")
			}
			if got := strings.Join(p.Cloned, ", "); got != strings.Join(want, ", ") {
				t.Fatalf("the plan names %q as the other repositories this image serves, want %q: they are who the refusal is about", got, strings.Join(want, ", "))
			}
			if !p.Notable {
				t.Error("the plan is quiet, so a build printing it would leave the operator who asked for a bake with nothing")
			}
			for _, r := range want {
				if !strings.Contains(p.Reason, r) {
					t.Errorf("the reason %q does not name %s, so a build's line does not say who else runs this image", p.Reason, r)
				}
			}
		})
	}
	// Which repositories the entry serves is read the same way with the bake
	// off: it is a fact about the map, not about the switch.
	req := reqApp()
	req.Bake = bakeOff()
	req.Map = map[string]string{"acme/app": "app", "acme/api": "app", "acme/web": "app"}
	if p := Decide(req); strings.Join(p.Cloned, ", ") != strings.Join(want, ", ") {
		t.Errorf("with baking off the plan names %v as the other repositories this image serves, want %v: which repositories run the image does not depend on the switch", p.Cloned, want)
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

// TestDecideBakesNothingWhenTheReadersCannotBeKnown: two repositories pinning
// one entry by their own workflows, with no map. The build runs in one of them
// and reads the project's map, which is its only evidence about any repository
// but its own (O3 (b): one credential, one checkout, no second repository's
// config) — so it cannot name acme/web, whose jobs run the same image and could
// read the checkout. The bake is refused rather than made on a guess, and the
// refusal names the map entry that makes the readers knowable.
func TestDecideBakesNothingWhenTheReadersCannotBeKnown(t *testing.T) {
	pinned := func() Request {
		req := reqApp()
		req.Entry = "tools"
		req.Reference = "tools"
		req.Map = nil
		req.Bake = bakeOn()
		return req
	}
	p := Decide(pinned())
	if p.Baked != nil {
		t.Fatal("Decide baked a checkout into an entry the map says nothing about: another repository may pin the same entry, and its jobs would read this checkout")
	}
	if !p.Notable {
		t.Error("the plan is quiet, so a build would not say why the project's bake did not happen")
	}
	if !strings.Contains(p.Reason, "image map set") {
		t.Errorf("the reason %q does not name the command that makes the readers knowable", p.Reason)
	}
	// The map is what makes them knowable: recorded there, and alone on the
	// entry, the bake lands — the same repository, the same pin.
	req := pinned()
	req.Map = map[string]string{"acme/app": "tools"}
	if p := Decide(req); p.Baked == nil {
		t.Fatalf("Decide baked nothing (%s): the map puts this repository on the entry and puts no other repository there", p.Reason)
	}
	// A second repository recorded on the entry is the shared case again.
	req.Map = map[string]string{"acme/app": "tools", "acme/web": "tools"}
	if p := Decide(req); p.Baked != nil {
		t.Fatal("Decide baked the checkout into an image the map puts acme/web on as well")
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

// TestDecideBakesAnEntryOnlyThisRepositoryRuns: the map puts this repository
// alone on the entry — the one shape O3 (b) leaves for a bake — and the plan
// says nothing about other readers because there are none to name.
func TestDecideBakesAnEntryOnlyThisRepositoryRuns(t *testing.T) {
	req := reqApp()
	req.Map = map[string]string{"acme/app": "app"}
	p := Decide(req)
	if p.Baked == nil {
		t.Fatalf("Decide baked nothing (%s): the map puts this repository alone on the entry its jobs run", p.Reason)
	}
	if len(p.Cloned) != 0 {
		t.Errorf("the plan names %v as other repositories this image serves, want none: the map puts no other repository on it", p.Cloned)
	}
	if p.Notable != true {
		t.Error("the plan is quiet, so a build that baked a checkout says nothing about it")
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
		// ".GIT" is the same suffix as ".git": the guard's shell lowercases
		// before it trims, so a trailing uppercase suffix on the build's own
		// REPO_URL reaches the same repository rather than a spelling nothing
		// matches.
		"the canonical form, uppercased suffix": {"https://bitbucket.org/acme/other.GIT", true},
		"the origin, uppercased suffix":         {"https://Bitbucket.org/acme/Other.GIT/", true},
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
	// The composed origin may carry the uppercase suffix itself, and then the
	// fold's order is the whole test: trimming ".git" before lowercasing leaves
	// "…/other.GIT" as "…/other.git", a pattern no REPO_URL matches — not the
	// canonical one, not the origin — so the composing build and every nightly
	// rebuild of it would fail inside the build, after it was paid for.
	upper := repoURLGuard("https://bitbucket.org/acme/other.GIT")
	for _, repoURL := range []string{"https://bitbucket.org/acme/other.git", "https://bitbucket.org/acme/other.GIT", "https://bitbucket.org/acme/other"} {
		cmd := exec.Command("sh", "-c", upper)
		cmd.Env = append(os.Environ(), "REPO_URL="+repoURL)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("a definition composed from an origin ending .GIT refused %s, the same repository: %v\n%s", repoURL, err, out)
		}
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
