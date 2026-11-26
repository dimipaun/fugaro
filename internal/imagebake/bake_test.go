package imagebake

import (
	"regexp"
	"strings"
	"testing"
)

// userinfoURL matches what a generated definition must never spell: a URL
// carrying credentials, whose userinfo sits between the scheme and the host. The
// stage's own shell names the "@" it folds a submodule URL through, which is why
// this is a search for a URL rather than for the character.
var userinfoURL = regexp.MustCompile(`://[^/\s'"]*@`)

// bakeOn and bakeOff are D1901's switch answered; a Request that leaves Bake
// nil is the project that answered nothing, which is on exactly as bakeOn is
// (projectpolicy.Policy.BakesRepos).
func bakeOn() *bool  { v := true; return &v }
func bakeOff() *bool { v := false; return &v }

// repoSpec is one of the project's repositories as a build resolves it: the
// canonical name, the provider key the map reads it by (projectpolicy.Canonical,
// which a repository name alone does not carry), the slug its checkout is
// written at (task.Slug) and the https URL it clones from. GitHub is the
// provider a test names unless it is about the other one. The slugs are the
// shape task.Slug emits and are written out rather than computed here, because a
// test that derived them would be a second implementation of the thing it is
// checking.
func repoSpec(name, slug string) Repo {
	return Repo{Repo: name, Key: "github/" + name, Slug: slug, URL: "https://github.com/" + name + ".git", Branch: "main"}
}

// names is what a plan bakes, as a line names it.
func names(rs []Repo) string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Repo
	}
	return strings.Join(out, ", ")
}

// reqApp is the request a build of the "app" entry from acme/app's checkout
// makes, with the map, the project's repositories and the switch each test
// overrides.
func reqApp() Request {
	return Request{
		Entry: "app",
		Repo:  repoSpec("acme/app", "app-1234abcd"),
		Map:   map[string]string{"github/acme/app": "app"},
		Repos: []Repo{repoSpec("acme/app", "app-1234abcd")},
		Bake:  bakeOn(),
	}
}

// TestDecideBakesTheBuildsOwnRepo: the repository the build runs for is baked
// when the map puts it on the entry being built, at the per-slug path its own
// jobs open (IS8), and the plan is the one repository alone.
func TestDecideBakesTheBuildsOwnRepo(t *testing.T) {
	p := Decide(reqApp())
	if len(p.Baked) != 1 {
		t.Fatalf("Decide bakes %q (%s), want the one repository the map puts on the entry", names(p.Baked), p.Reason)
	}
	if p.Baked[0].Repo != "acme/app" {
		t.Errorf("the plan bakes %s, want acme/app", p.Baked[0].Repo)
	}
	if p.Baked[0].Slug != "app-1234abcd" {
		t.Errorf("the plan bakes the slug %q, want app-1234abcd", p.Baked[0].Slug)
	}
	dir, err := Path(p.Baked[0].Slug)
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/work/baked/app-1234abcd" {
		t.Errorf("Path(%q) = %q, want /work/baked/app-1234abcd", p.Baked[0].Slug, dir)
	}
	if p.Reason != "" {
		t.Errorf("a plan that bakes carries the reason %q, want none", p.Reason)
	}
	if !p.Notable {
		t.Error("the plan is quiet, so a build that baked a checkout says nothing about it")
	}
	if got := strings.Join(p.Repositories(), ","); got != "acme/app" {
		t.Errorf("the plan's repositories are %q, want acme/app: they are what the build's one credential has to cover", got)
	}
}

// TestDecideBakesSeveralRepos: D2301, which overturns O3's reading (b). One
// catalog build bakes EVERY repository the project's map puts on the entry being
// built — their jobs are the ones that start this image, so their checkouts are
// the ones its image carries — in one order with the build's own LAST, which is
// what BakedSlug, the smoke and the nightly rebuild read a definition by.
func TestDecideBakesSeveralRepos(t *testing.T) {
	req := reqApp()
	req.Repos = []Repo{
		repoSpec("acme/app", "app-1234abcd"),
		repoSpec("acme/api", "api-1111aaaa"),
		repoSpec("acme/web", "web-2222bbbb"),
		// The map puts this one on another entry, so its jobs never start this
		// image: a checkout baked here is one they would not read.
		repoSpec("acme/tool", "tool-3333cccc"),
		// The map names no entry for this one at all.
		repoSpec("acme/spare", "spare-4444dddd"),
	}
	req.Map = map[string]string{
		"github/acme/app": "app", "github/acme/api": "app", "github/acme/web": "app", "github/acme/tool": "other",
	}
	p := Decide(req)
	if len(p.Baked) != 3 {
		t.Fatalf("Decide bakes %q (%s), want the three repositories the map puts on the app entry", names(p.Baked), p.Reason)
	}
	if got := names(p.Baked); got != "acme/api, acme/web, acme/app" {
		t.Errorf("the plan bakes %s, want acme/api, acme/web, acme/app: the entry's repositories in one order, the build's own last", got)
	}
	if got := strings.Join(p.Repositories(), ","); got != "acme/api,acme/web,acme/app" {
		t.Errorf("the credential has to cover %q, want the baked repositories in the same order", got)
	}
	if !p.Notable || p.Reason != "" {
		t.Errorf("the plan is notable %v with the reason %q, want a bake and no reason", p.Notable, p.Reason)
	}
	// The definition carries one stage per repository, each at its own slug path,
	// and the last of them is the build's own — which is the stage a reader that
	// has to know which repository a definition bakes finds.
	def, err := Append([]byte("FROM scratch\n"), p)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Baked {
		dir, err := Path(r.Slug)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(def), markerPrefix+dir+" "+r.URL+" "+r.Branch+"\n") {
			t.Errorf("the composed definition has no stage for %s at %s:\n%s", r.Repo, dir, def)
		}
		if !strings.Contains(string(def), "COPY --from="+BakedImage+" --chown=1000:1000 /"+r.Slug+"/ "+dir+"/\n") {
			t.Errorf("the stage of %s does not copy its checkout out of %s:\n%s", r.Repo, BakedImage, def)
		}
	}
	if got := strings.Count(string(def), markerPrefix); got != len(p.Baked) {
		t.Errorf("the composed definition carries %d stages, want %d:\n%s", got, len(p.Baked), def)
	}
	if slug, ok := BakedSlug(def); !ok || slug != "app-1234abcd" {
		t.Errorf("BakedSlug(composed) = %q, %v; want app-1234abcd, true: the last stage is the build's own", slug, ok)
	}
	// And what the clone step clones the stages from: the plan's repositories,
	// in the order their stages run, read back out of the definition's own
	// marker lines.
	repos := BakedRepos(def)
	if len(repos) != len(p.Baked) {
		t.Fatalf("BakedRepos(composed) = %+v, want the plan's %d repositories", repos, len(p.Baked))
	}
	for i, r := range repos {
		// The Repo name and the URL are the checkout's, which is what a clone
		// step can act on; the number is the stage's place and not the plan's
		// order, which is the same one.
		if r.Slug != p.Baked[i].Slug || r.URL != p.Baked[i].URL || r.Branch != p.Baked[i].Branch {
			t.Errorf("BakedRepos(composed)[%d] = %+v, want %+v", i, r, p.Baked[i])
		}
	}
}

// TestDecideBakesOnlyTheMapEntrysRepositories: the bake set is the map's answer
// for the entry being built and not the project's list of repositories. A
// repository the project has and the map puts nowhere may start any image at
// all — the deploy resolves it by its own reference or by the tags it needs, and
// a build reads one repository's config — so a checkout baked for it would be a
// checkout nobody reads.
func TestDecideBakesOnlyTheMapEntrysRepositories(t *testing.T) {
	req := reqApp()
	req.Repos = []Repo{repoSpec("acme/app", "app-1234abcd"), repoSpec("acme/other", "other-9999eeee")}
	p := Decide(req)
	if len(p.Baked) != 1 || p.Baked[0].Repo != "acme/app" {
		t.Errorf("Decide bakes %q (%s), want acme/app alone: the map puts no other repository on the entry", names(p.Baked), p.Reason)
	}
}

// TestDecideRefusesARepositoryTheProjectDoesNotAdmit: the map is a config value
// and a published node, so the build reads it against the project's own list
// rather than trusting it whole. A repository the list does not answer for is
// one this build cannot clone — it holds no URL and no provider for it — and the
// bake is refused by name instead of composed from a guess (D2301).
func TestDecideRefusesARepositoryTheProjectDoesNotAdmit(t *testing.T) {
	req := reqApp()
	req.Map = map[string]string{"github/acme/app": "app", "github/acme/web": "app"}
	p := Decide(req)
	if len(p.Baked) != 0 {
		t.Fatalf("Decide baked %q into an entry that serves a repository this project does not admit", names(p.Baked))
	}
	if !strings.Contains(p.Reason, "acme/web") {
		t.Errorf("the reason %q does not name the repository, so a build's line does not say what refused it", p.Reason)
	}
	if !p.Notable {
		t.Error("the plan is quiet, so the operator who asked for a bake is left with nothing")
	}
	// The same map with the repository in the project's own list is a bake of
	// both: what was refused was the unknown repository, not the shared entry.
	req.Repos = append(req.Repos, repoSpec("acme/web", "web-2222bbbb"))
	if p := Decide(req); len(p.Baked) != 2 {
		t.Errorf("Decide bakes %q (%s), want both repositories: the project admits the one it records", names(p.Baked), p.Reason)
	}
	// A key that is not a repository name is refused too: it is a published
	// node's own text, and it reaches a line a person reads.
	req = reqApp()
	req.Map = map[string]string{"github/acme/app": "app", "not a repo": "app"}
	if p := Decide(req); len(p.Baked) != 0 || !strings.Contains(p.Reason, "which is not a repository") {
		t.Errorf("Decide baked %q with the reason %q, want a refusal naming the map key that is not a repository", names(p.Baked), p.Reason)
	}
}

// TestDecideByProviderKey: the map is read by provider/owner/name, so one name
// at two providers is two keys and two entries (D3370). A build of the Bitbucket
// acme/app reads the Bitbucket key: a lookup by the bare name would read the
// GitHub key and bake a checkout whose jobs run another image, and would refuse
// the bake over a repository this build never clones.
func TestDecideByProviderKey(t *testing.T) {
	req := reqApp()
	own := Repo{Repo: "acme/app", Key: "bitbucket/acme/app", Slug: "app-1234abcd", URL: "https://bitbucket.org/acme/app.git", Branch: "main"}
	req.Repo, req.Repos = own, []Repo{own}

	// The project serves acme/app at Bitbucket, and the map names it at both
	// providers: the build's own key bakes its checkout, the other provider's is
	// skipped (R9).
	req.Map = map[string]string{"bitbucket/acme/app": "app", "github/acme/app": "app"}
	p := Decide(req)
	if len(p.Baked) != 1 || p.Baked[0].Key != "bitbucket/acme/app" {
		t.Fatalf("Decide bakes %q (%s), want the one repository this build runs for, read at its own provider", names(p.Baked), p.Reason)
	}

	// The map puts only the other provider's key on the entry, and this
	// repository's own workflow names the entry: nothing is baked, and both the
	// reason and the fix name the key the map is read by — a key the person's own
	// command has to spell the same way.
	req.Reference = "app"
	req.Map = map[string]string{"github/acme/app": "app"}
	p = Decide(req)
	if len(p.Baked) != 0 {
		t.Fatalf("Decide baked %q from a key at a provider this build does not run at", names(p.Baked))
	}
	if !strings.Contains(p.Reason, "bitbucket/acme/app") || !strings.Contains(p.Fix, "fugaro image map set bitbucket/acme/app app") {
		t.Errorf("the reason %q and the fix %q do not name the key the map is read by", p.Reason, p.Fix)
	}
}

// TestEntryReposSkipsAKeyAtTheWrongProvider: R9, and F14's case — a key
// D3375's migration wrote for a provider the project turns out not to serve
// (bitbucket/acme/app where acme/app is only at GitHub). It names a repository
// no job of this image runs and that this build holds no credential for, so it
// is skipped: one such key must not refuse the whole bake.
func TestEntryReposSkipsAKeyAtTheWrongProvider(t *testing.T) {
	req := reqApp()
	req.Map = map[string]string{"github/acme/app": "app", "bitbucket/acme/app": "app"}
	baked, refused := entryRepos(req)
	if refused != "" {
		t.Fatalf("entryRepos refused the bake (%s): a key at a provider this build does not run at is skipped and not refused", refused)
	}
	if len(baked) != 1 || baked[0].Key != "github/acme/app" {
		t.Errorf("entryRepos baked %+v, want the build's own repository alone", baked)
	}
}

// TestEntryReposStillRefusesAnUnadmittedRepo: R9's other half. A key at the
// build's own provider for a repository the project does not admit is still
// refused, by name: the build holds no URL and no provider for it, and the
// entry's jobs may well run this image, so a skipped key here would bake
// nothing quietly.
func TestEntryReposStillRefusesAnUnadmittedRepo(t *testing.T) {
	req := reqApp()
	req.Map = map[string]string{"github/acme/app": "app", "github/acme/other": "app"}
	baked, refused := entryRepos(req)
	if refused == "" {
		t.Fatalf("entryRepos baked %+v, want a refusal: github/acme/other is not a repository this project admits", baked)
	}
	if !strings.Contains(refused, "github/acme/other") || !strings.Contains(refused, "not a repository this project admits") {
		t.Errorf("the refusal %q does not name the key and why it is refused", refused)
	}
}

// TestDecideReadsALegacyMap: R2. A map in the old form — bare names, matching
// every provider — is read as it always was, because a node or a local config
// `update` has not rewritten yet still holds it. repo_images wins where the
// project has one, per member and never per key.
func TestDecideReadsALegacyMap(t *testing.T) {
	req := reqApp()
	req.Map = nil
	req.LegacyMap = map[string]string{"acme/app": "app"}
	if p := Decide(req); len(p.Baked) != 1 {
		t.Fatalf("Decide baked nothing (%s): a legacy map puts this repository on the entry", p.Reason)
	}
	req.Map = map[string]string{"github/acme/app": "app"}
	req.LegacyMap = map[string]string{"acme/app": "other"}
	if p := Decide(req); len(p.Baked) != 1 {
		t.Fatalf("Decide baked nothing (%s): repo_images answers whatever the legacy map holds", p.Reason)
	}
}

// TestDecideRefusesARepositoryOnAnotherHost: one credential is for one host
// (rule 5 of contract-build-token.md), and a plan is one credential, so a
// repository the map puts on the entry whose clone URL is on another host than
// the build's own is refused by name. Composing its stage would be a clone the
// credential does not reach — or, where the repository is public, a clone made
// with no credential at all.
func TestDecideRefusesARepositoryOnAnotherHost(t *testing.T) {
	req := reqApp()
	req.Map = map[string]string{"github/acme/app": "app", "github/acme/web": "app"}
	// The key's provider is the build's own, so R9 does not skip it: what refuses
	// it is the host its clone URL sends the credential to (an enterprise GitHub,
	// which is a different host from github.com).
	other := repoSpec("acme/web", "web-2222bbbb")
	other.URL = "https://github.example.com/acme/web.git"
	req.Repos = append(req.Repos, other)
	p := Decide(req)
	if len(p.Baked) != 0 {
		t.Fatalf("Decide baked %q across two hosts, and its one credential reaches one of them", names(p.Baked))
	}
	if !strings.Contains(p.Reason, "acme/web") {
		t.Errorf("the reason %q does not name the repository on the other host", p.Reason)
	}
	// The same repository on the build's own host, spelled with a capital letter
	// and without ".git", is the same host: what is compared is not the string.
	same := repoSpec("acme/web", "web-2222bbbb")
	same.URL = "https://GitHub.com/acme/web"
	req.Repos = append(req.Repos[:1], same)
	if p := Decide(req); len(p.Baked) != 2 {
		t.Errorf("Decide bakes %q (%s), want both repositories: their clone URLs are on one host", names(p.Baked), p.Reason)
	}
}

// TestDecideBakesNothingForAnotherImage: the entry being built is not the one
// this repository runs, so its jobs never pull the image a bake would land in.
func TestDecideBakesNothingForAnotherImage(t *testing.T) {
	for name, req := range map[string]Request{
		"the map puts the repository on another entry": func() Request {
			r := reqApp()
			r.Map = map[string]string{"github/acme/app": "other"}
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
			if len(p.Baked) != 0 {
				t.Fatalf("Decide baked the checkout into the %s entry: a bake in an image these jobs never pull is work nobody reads", req.Entry)
			}
			if p.Reason == "" {
				t.Error("a plan that bakes nothing gives no reason, so a build has nothing to print")
			}
		})
	}
}

// TestDecideBakesNothingWhileTheMapDoesNotListIt: the entry is one this
// repository's own workflow names, so a bake belongs in it (T1 reading (b), IS1's
// resolver) — but a build reads one repository's config, so the map is all it
// holds about the repositories that would share the image. Where the map records
// no entry for this repository the build cannot say which they are, and it
// refuses the bake naming the command that would let it follow. What it must not
// do is guess: a checkout in an image whose other readers it cannot name is
// source those readers' jobs can read (F18).
func TestDecideBakesNothingWhileTheMapDoesNotListIt(t *testing.T) {
	req := reqApp()
	req.Reference = "app"
	req.Map = nil
	p := Decide(req)
	if len(p.Baked) != 0 {
		t.Fatalf("Decide baked %q into an entry the project's map says nothing about", names(p.Baked))
	}
	if !p.Notable {
		t.Error("the plan is quiet, so a build would not say why the project's bake did not happen")
	}
	if !strings.Contains(p.Reason, "acme/app") {
		t.Errorf("the reason %q does not name the repository the map leaves out", p.Reason)
	}
	if !strings.Contains(p.Fix, "fugaro image map set acme/app app") {
		t.Errorf("the fix %q does not name the command that would make the bake follow", p.Fix)
	}
	if !p.Asked {
		t.Error("the plan does not record that bake_repos was written true")
	}
	if line := p.Reason + ". " + p.Fix; len(line) >= 200 {
		t.Errorf("the note is %d characters, want under 200: %q", len(line), line)
	}
	// Recorded there, the bake lands: the map puts this repository on the entry
	// its own workflow names, and puts no other repository on it.
	req.Map = map[string]string{"github/acme/app": "app"}
	if p := Decide(req); len(p.Baked) != 1 {
		t.Fatalf("Decide baked nothing (%s): the map puts this repository on the entry its workflow names", p.Reason)
	}
}

// TestDecideBakeSwitchOff: D1901's switch, off, keeps the checkout out of every
// image and says so.
func TestDecideBakeSwitchOff(t *testing.T) {
	req := reqApp()
	req.Bake = bakeOff()
	p := Decide(req)
	if len(p.Baked) != 0 {
		t.Fatal("Decide baked a checkout with bake_repos off")
	}
	if p.Asked {
		t.Error("a switch written false was recorded as an ask for a bake")
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
		if len(p.Baked) != 0 {
			t.Errorf("Decide baked the slug %q, want a refusal: Path refuses it, so the stage would name a directory no reader opens", slug)
		}
		if _, err := Path(slug); err == nil {
			t.Errorf("Path(%q) accepted a slug that is not one", slug)
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

// TestAppendAndBakedDir: the composition is the entry's own bytes with a stage
// per baked repository on the end, a definition that bakes nothing is returned
// untouched, and the directory the last marker names is the one the build wrote
// — which is what the smoke spec of that build has to check.
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

// TestAppendNamesNoSecretAndCopiesTheCheckout: what a generated stage may never
// do is clone — the entry's own build holds no git credential (B8, F19), and a
// RUN of it that cloned would need one. A stage is therefore the copy of a
// checkout the clone step made, out of the image that step tagged: no RUN at
// all, no build argument, no secret mount, no USER line, and one COPY per
// repository.
func TestAppendNamesNoSecretAndCopiesTheCheckout(t *testing.T) {
	p := Decide(reqApp())
	def, err := Append([]byte("FROM scratch\nUSER 0\n"), p)
	if err != nil {
		t.Fatal(err)
	}
	generated := strings.TrimPrefix(string(def), "FROM scratch\nUSER 0\n")
	// The comments of the stages say that no stage names a secret, so this looks
	// for the argument that would mount one and not for the word.
	for _, banned := range []string{"RUN ", "--mount", "id=git-credentials", "ARG ", "ENV ", "USER ", "FROM "} {
		if strings.Contains(generated, banned) {
			t.Errorf("a generated stage carries %q, so a build of it may read the credential or run as somebody else:\n%s", banned, generated)
		}
	}
	// The entry's own bytes are the entry's own: whatever they say stands.
	if !strings.HasPrefix(string(def), "FROM scratch\nUSER 0\n") {
		t.Errorf("the composed definition does not begin with the entry's own bytes:\n%s", def)
	}
	if loc := userinfoURL.FindString(string(def)); loc != "" {
		t.Errorf("the composed definition spells the URL %q, which could carry a credential:\n%s", loc, def)
	}
	// Every stage copies out of the image the clone step built, and the last of
	// them is the build's own — the checkout a reader finds (BakedSlug).
	own := p.Baked[len(p.Baked)-1]
	dir, err := Path(own.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(generated, "COPY --from="+BakedImage+" --chown=1000:1000 /"+own.Slug+"/ "+dir+"/\n") {
		t.Errorf("the last generated stage is not the copy of the build's own checkout:\n%s", generated)
	}
}

// TestAppendRefusesWhatADefinitionMayNotSpell: the URL, the branch and the slug
// are config values, and a definition may not spell arbitrary text — it is copied
// into the project's bucket and read by every build and job of the entry. Each is
// refused rather than written, with one line, because a value that ended a line
// would spell a marker or a piece of shell of its own.
func TestAppendRefusesWhatADefinitionMayNotSpell(t *testing.T) {
	good := repoSpec("acme/web", "web-2222bbbb")
	for name, r := range map[string]Repo{
		"a branch a clone would read as a flag": {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: "--upload-pack=/bin/sh"},
		"an empty branch":                       {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: ""},
		"a branch with a newline":               {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: "main\nRUN echo hi"},
		"a branch with a quote":                 {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: "main'; RUN echo hi #"},
		"a URL with userinfo":                   {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://user:token@github.com/acme/web.git", Branch: "main"},
		"a URL that is not https":               {Repo: "acme/web", Slug: "web-2222bbbb", URL: "http://github.com/acme/web.git", Branch: "main"},
		"no host at all":                        {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https:///acme/web.git", Branch: "main"},
		"a shell-quoting hazard":                {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/it's.git", Branch: "main"},
		"a URL with a newline":                  {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git\nRUN echo hi", Branch: "main"},
		"a slug that is not one":                {Repo: "acme/web", Slug: "../escape", URL: "https://github.com/acme/web.git", Branch: "main"},
		"no slug at all":                        {Repo: "acme/web", Slug: "", URL: "https://github.com/acme/web.git", Branch: "main"},
	} {
		t.Run(name, func(t *testing.T) {
			def, err := Append([]byte("FROM scratch\n"), Plan{Baked: []Repo{good, r}})
			if err == nil {
				t.Fatalf("Append spelled %+v into a definition:\n%s", r, def)
			}
			if strings.ContainsAny(err.Error(), "\n\r") {
				t.Errorf("the refusal is not one line, so it does not belong in a build log: %q", err.Error())
			}
		})
	}
}

// TestBakedReposSkipAnEarlierReleasesMarker: a definition composed by an earlier
// release names its checkout and no clone URL — its stage cloned inside the
// entry's own build, with a credential that build is not handed any more — and no
// reader can clone what such a stage never named. Such a marker is skipped rather
// than read as a repository, and the marker a composed definition carries is
// read with its URL and branch. The reader that needs one of the old markers to
// be an answer is internal/cli's, which refuses such a copy by name.
func TestBakedReposSkipAnEarlierReleasesMarker(t *testing.T) {
	old := markerPrefix + "/work/baked/first-0000aaaa\n"
	def := old +
		"FROM scratch\n" +
		markerPrefix + "/work/baked/second-1111bbbb https://github.com/acme/app.git main\n" +
		markerPrefix + "/work/repo https://github.com/acme/elsewhere.git main\n"
	repos := BakedRepos([]byte(def))
	if len(repos) != 1 {
		t.Fatalf("BakedRepos = %+v, want the one marker carrying a clone URL", repos)
	}
	if r := repos[0]; r.Slug != "second-1111bbbb" || r.URL != "https://github.com/acme/app.git" || r.Branch != "main" {
		t.Errorf("BakedRepos = %+v, want the slug, URL and branch the marker names", r)
	}
	if _, baked := BakedSlug([]byte(def)); !baked {
		t.Error("BakedSlug does not read a marker that names no URL: that is what tells internal/cli a copy is one an earlier release composed")
	}
	// A marker whose URL or branch may not be spelled is not a repository this
	// package can clone: it is skipped, and the copy is refused by name rather
	// than built from a URL a stage may not carry.
	for _, line := range []string{
		markerPrefix + "/work/baked/x-0000aaaa https://user:tok@github.com/acme/app.git main\n",
		markerPrefix + "/work/baked/x-0000aaaa https://github.com/acme/app.git bad branch\n",
		markerPrefix + "/work/baked/x-0000aaaa https://github.com/acme/app.git\n",
		markerPrefix + "/work/baked/x-0000aaaa https://github.com/acme/app.git \n",
	} {
		if repos := BakedRepos([]byte(line + "FROM scratch\n")); len(repos) != 0 {
			t.Errorf("BakedRepos read %q as %+v, want no repository", line, repos)
		}
	}
}
