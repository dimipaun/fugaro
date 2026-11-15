package imagebake

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
// canonical name, the slug its checkout is written at (task.Slug) and the https
// URL it clones from. The slugs are the shape task.Slug emits and are written
// out rather than computed here, because a test that derived them would be a
// second implementation of the thing it is checking.
func repoSpec(name, slug string) Repo {
	return Repo{Repo: name, Slug: slug, URL: "https://github.com/" + name + ".git", Branch: "main"}
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
		Map:   map[string]string{"acme/app": "app"},
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
		"acme/app": "app", "acme/api": "app", "acme/web": "app", "acme/tool": "other",
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
		if !strings.Contains(string(def), markerPrefix+dir+"\n") {
			t.Errorf("the composed definition has no stage for %s at %s:\n%s", r.Repo, dir, def)
		}
	}
	if got := strings.Count(string(def), markerPrefix); got != len(p.Baked) {
		t.Errorf("the composed definition carries %d stages, want %d:\n%s", got, len(p.Baked), def)
	}
	if slug, ok := BakedSlug(def); !ok || slug != "app-1234abcd" {
		t.Errorf("BakedSlug(composed) = %q, %v; want app-1234abcd, true: the last stage is the build's own", slug, ok)
	}
	// The build's own stage takes the build's own arguments; the entry's other
	// repositories are spelled into the definition, because the arguments a
	// catalog build passes name the repository it runs for.
	if !strings.Contains(string(def), `clone --quiet --branch "$BASE_BRANCH" --single-branch -- "$REPO_URL" /work/baked/app-1234abcd`) {
		t.Errorf("the build's own stage does not clone its own REPO_URL:\n%s", def)
	}
	if !strings.Contains(string(def), "clone --quiet --branch 'main' --single-branch -- 'https://github.com/acme/api.git' /work/baked/api-1111aaaa") {
		t.Errorf("the stage of acme/api does not spell its own URL and branch:\n%s", def)
	}
	if strings.Contains(string(def), "ARG REPO_URL\nARG BASE_BRANCH\nARG FUGARO_COMMIT") &&
		strings.Count(string(def), "ARG REPO_URL") != 1 {
		t.Errorf("more than the build's own stage declares the build's arguments:\n%s", def)
	}
	// The submodule guard of every stage is over the plan's own repositories, the
	// credential's reach, and not over the stage's repository alone.
	guard := submoduleGuard("/work/baked/api-1111aaaa", p.Baked)
	if !strings.Contains(string(def), guard) {
		t.Errorf("the stage of acme/api does not carry the guard over the plan's repositories:\n%s", def)
	}
	if !strings.Contains(guard, "github.com/acme/api github.com/acme/web github.com/acme/app") {
		t.Errorf("the guard does not cover every repository the credential covers:\n%s", guard)
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
	req.Map = map[string]string{"acme/app": "app", "acme/web": "app"}
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
	req.Map = map[string]string{"acme/app": "app", "not a repo": "app"}
	if p := Decide(req); len(p.Baked) != 0 || !strings.Contains(p.Reason, "which is not a repository") {
		t.Errorf("Decide baked %q with the reason %q, want a refusal naming the map key that is not a repository", names(p.Baked), p.Reason)
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
	req.Map = map[string]string{"acme/app": "app", "acme/web": "app"}
	other := repoSpec("acme/web", "web-2222bbbb")
	other.URL = "https://bitbucket.org/acme/web.git"
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
	req.Map = map[string]string{"acme/app": "app"}
	if p := Decide(req); len(p.Baked) != 1 {
		t.Fatalf("Decide baked nothing (%s): the map puts this repository on the entry its workflow names", p.Reason)
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
		"the entry serves another repository too": func() Request {
			r := reqApp()
			r.Map = map[string]string{"acme/app": "app", "acme/web": "app"}
			r.Repos = append(r.Repos, repoSpec("acme/web", "web-2222bbbb"))
			return r
		}(),
		"the checkout's own workflow names the entry": func() Request { r := reqApp(); r.Map = nil; r.Reference = "app"; return r }(),
	} {
		t.Run(name, func(t *testing.T) {
			req.Bake = nil
			unset := Decide(req)
			req.Bake = &on
			answered := Decide(req)
			if (len(unset.Baked) == 0) != (len(answered.Baked) == 0) {
				t.Fatalf("Decide baked %q with the switch unset and %q with it answered yes: one field, two meanings", names(unset.Baked), names(answered.Baked))
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

// TestStageClonesWithTheBuildsOwnCredential: the stage is the generated
// Dockerfile clone. What it must never do is carry a secret into a layer: the
// credential is the BuildKit secret mount the catalog build provides (D1150,
// F14), and no ARG, ENV or COPY names a token.
func TestStageClonesWithTheBuildsOwnCredential(t *testing.T) {
	p := Decide(reqApp())
	stage, err := Stage(p.Baked[len(p.Baked)-1], p.Baked)
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
		"case \" github.com/acme/app \" in",
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
	if loc := userinfoURL.FindString(stage); loc != "" {
		t.Errorf("the generated stage spells the URL %q, which could carry a credential:\n%s", loc, stage)
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
	p := Decide(reqApp())
	stage, err := Stage(p.Baked[0], p.Baked)
	if err != nil {
		t.Fatal(err)
	}
	// The stage runs the guard the test below runs; what is pinned here is
	// that this stage carries it and not a second spelling of the rule.
	if !strings.Contains(stage, repoURLGuard(p.Baked[0].URL)) {
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
			if stage, err := Stage(r, nil); err == nil {
				t.Fatalf("Stage spelled %q into a definition:\n%s", bad, stage)
			}
		})
	}
}

// TestOtherStageSpellsTheURLAndBranch: the stage of one of the entry's other
// repositories carries that repository's URL and branch in the definition
// itself, because the arguments a catalog build passes name the repository the
// build runs for — and it carries no argument of its own, no commit pin (the
// build holds no commit of a repository it does not run for) and the same
// submodule guard as every other stage.
func TestOtherStageSpellsTheURLAndBranch(t *testing.T) {
	covered := []Repo{repoSpec("acme/app", "app-1234abcd"), repoSpec("acme/web", "web-2222bbbb")}
	stage, err := OtherStage(covered[1], covered)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		markerPrefix + "/work/baked/web-2222bbbb\n",
		"# acme/web, one of the repositories",
		"--mount=type=secret,id=git-credentials,uid=1000,required=false",
		"clone --quiet --branch 'main' --single-branch -- 'https://github.com/acme/web.git' /work/baked/web-2222bbbb",
		submoduleGuard("/work/baked/web-2222bbbb", covered),
		"/usr/local/lib/fugaro/finalize-checkout /work/baked/web-2222bbbb",
	} {
		if !strings.Contains(stage, want) {
			t.Errorf("the other repository's stage does not carry %q:\n%s", want, stage)
		}
	}
	for _, banned := range []string{"$REPO_URL", "$BASE_BRANCH", "$FUGARO_COMMIT", "ARG ", "ENV ", "COPY "} {
		if strings.Contains(stage, banned) {
			t.Errorf("the other repository's stage carries %q, which names the build's own repository:\n%s", banned, stage)
		}
	}
	if loc := userinfoURL.FindString(stage); loc != "" {
		t.Errorf("the other repository's stage spells the URL %q, which could carry a credential:\n%s", loc, stage)
	}
	// The URL, the branch and the name are config values, and a definition may
	// not spell arbitrary text: each is refused rather than written. The
	// refusals are one line each — a value that ended a line would spell a
	// Dockerfile instruction of its own.
	for name, tc := range map[string]Repo{
		"a branch a clone would read as a flag": {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: "--upload-pack=/bin/sh"},
		"an empty branch":                       {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: ""},
		"a branch with a newline":               {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: "main\nRUN echo hi"},
		"a branch with a quote":                 {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: "main'; RUN echo hi #"},
		"a URL with userinfo":                   {Repo: "acme/web", Slug: "web-2222bbbb", URL: "https://user:token@github.com/acme/web.git", Branch: "main"},
		"a name that is not a repository":       {Repo: "acme/web\nRUN echo hi", Slug: "web-2222bbbb", URL: "https://github.com/acme/web.git", Branch: "main"},
		"a slug that is not one":                {Repo: "acme/web", Slug: "../escape", URL: "https://github.com/acme/web.git", Branch: "main"},
	} {
		t.Run(name, func(t *testing.T) {
			stage, err := OtherStage(tc, covered)
			if err == nil {
				t.Fatalf("OtherStage spelled %+v into a definition:\n%s", tc, stage)
			}
			if strings.ContainsAny(err.Error(), "\n\r") {
				t.Errorf("the refusal is not one line, so it does not belong in a build log: %q", err.Error())
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

// TestBakedSubmoduleOutsideTheCredentialNamesTheRepo: every stage ends with the
// guard, and what the guard has to do is what its text cannot promise. The
// checkout is the baked repository's own, so a submodule it declares is a fetch
// this build would make with the credential the entry's repositories were minted
// into — and a repository the credential does not cover is not one it fetches:
// the build fails, naming the repository, rather than cloning it.
//
// The declaration is read with git's own parser and what the refusal names is
// the repository the URL resolves to, folded to host/owner/name. That fold is
// what keeps a credential out of the build log — everything before the first "@"
// is off the text printed — and what refuses a repository of the entry's own
// owner and name that lives on another host.
func TestBakedSubmoduleOutsideTheCredentialNamesTheRepo(t *testing.T) {
	dir := t.TempDir()
	covered := []Repo{repoSpec("acme/app", "app-1234abcd"), repoSpec("acme/web", "web-2222bbbb")}
	guard := submoduleGuard(dir, covered)
	sub := func(name, url string) string {
		return "[submodule \"" + name + "\"]\n\tpath = " + name + "\n\turl = " + url + "\n"
	}
	// run writes a .gitmodules and runs the guard the stage carries, through the
	// shell a stage runs it with: the invariant is what the shell does with a
	// declaration, and a guard whose text alone is pinned can be relaxed to an
	// always-accepting arm with nothing failing.
	run := func(t *testing.T, body string) (string, error) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", guard)
		// The build's own environment, without the operator's: a system or a
		// global gitconfig must not change what the guard reads (B8's clone step
		// sets both of these).
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, tc := range []struct {
		name string
		body string
		// named is the repository the refusal has to name, "" when the guard
		// accepts the declaration.
		named string
	}{
		{"nothing declared", "", ""},
		{"an empty .gitmodules", "# nothing\n", ""},
		{"a submodule the credential covers", sub("lib", "https://github.com/acme/web.git"), ""},
		{"a submodule it covers, spelt another way", sub("lib", "https://GitHub.com/Acme/Web/"), ""},
		{"a submodule it does not cover", sub("lib", "https://github.com/acme/lib.git"), "github.com/acme/lib"},
		{"a submodule of another host", sub("lib", "https://gitlab.com/acme/web.git"), "gitlab.com/acme/web"},
		{"a relative submodule", sub("lib", "../lib"), "../lib"},
		{"a submodule in git's scp spelling", sub("lib", "git@github.com:acme/web.git"), "github.com:acme/web"},
		{"the second of two declared", sub("a", "https://github.com/acme/app.git") + sub("b", "https://github.com/acme/zzz.git"), "github.com/acme/zzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, tc.body)
			if tc.named == "" {
				if err != nil {
					t.Fatalf("the guard refused a checkout whose submodules the credential covers: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("the guard accepted %s, and the build clones a repository the credential does not cover", tc.named)
			}
			if !strings.Contains(out, tc.named) {
				t.Errorf("the guard refused without naming %s, so the build log does not say which repository it could not read:\n%s", tc.named, out)
			}
			if strings.Contains(out, "https://") {
				t.Errorf("the guard echoed a URL, and a submodule URL may carry a credential:\n%s", out)
			}
		})
	}
	// A URL that carries a credential is refused — the repository is not one the
	// credential covers — and the token never reaches the build log, which is what
	// the fold's dropping everything before the first "@" is for.
	out, err := run(t, sub("lib", "https://x-token:tok3n@github.com/acme/lib.git"))
	if err == nil {
		t.Fatal("the guard accepted a submodule URL carrying a credential")
	}
	if strings.Contains(out, "tok3n") {
		t.Errorf("the guard put a credential from a submodule URL into the build log:\n%s", out)
	}
	if !strings.Contains(out, "github.com/acme/lib") {
		t.Errorf("the guard refused without naming the repository:\n%s", out)
	}
	// A URL on the covered host and name, with a credential in it, is the covered
	// repository: git sends that credential to that host, and nothing is printed.
	out, err = run(t, sub("lib", "https://x-token:tok3n@github.com/acme/web.git"))
	if err != nil {
		t.Fatalf("the guard refused a submodule of the covered repository: %v\n%s", err, out)
	}
	if strings.Contains(out, "tok3n") {
		t.Errorf("the guard printed a URL that carried a credential:\n%s", out)
	}
	// A guard over no repositories — a stage composed with nothing the credential
	// covers — refuses every declaration rather than accepting what it cannot show
	// is covered.
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte(sub("lib", "https://github.com/acme/web.git")), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", submoduleGuard(dir, nil))
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("a guard over no repositories accepted a submodule declaration:\n%s", out)
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
