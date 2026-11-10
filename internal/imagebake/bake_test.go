package imagebake

import (
	"strings"
	"testing"
)

// reqApp is the request a build of the "app" entry from acme/app's checkout
// makes, with the map and the switch each test overrides.
func reqApp() Request {
	return Request{
		Entry: "app",
		Repo: Repo{
			Repo: "acme/app", Slug: "app-1234abcd",
			URL: "https://github.com/acme/app.git", Branch: "main",
		},
		Map:  map[string]string{"acme/app": "app"},
		Bake: true,
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
	req.Bake = false
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

// TestDecideBakeSwitchOff: D1901's switch, off, keeps the checkout out of every
// image and says so.
func TestDecideBakeSwitchOff(t *testing.T) {
	req := reqApp()
	req.Bake = false
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
	// keeps an installation with no map converging to exactly what it had.
	off, err := Append(own, Decide(func() Request { r := reqApp(); r.Bake = false; return r }()))
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
