// Package imagebake is the bake step a catalog build generates: the stage it
// appends to an entry's Dockerfile so the image carries the checkout of a
// repository, and the rule that says which repository that is.
//
// A job whose image carries its repository's checkout skips the clone it would
// otherwise pay for (internal/runner's openCheckout opens a checkout already in
// the work directory), which is what makes a catalog image start faster than
// one that has to clone. The definition of a catalog entry is the entry's own
// (D1208, docs/repo-image-removal): a hand-written Dockerfile that clones is
// written by whoever wrote it, and this package is the engine's own answer for
// the entries the image setup skill generates — the bake step the plan splits
// out as IS8b, which waited on the owner's O3.
//
// # The owner's ruling (O3, 2026-11-09: reading (b))
//
// A catalog build runs as ONE repository's build account with that
// repository's credential (docs/project-image-catalog; internal/backend/gcp's
// CatalogBuildSpec.check refuses any other account). The grant reaches exactly
// one repository — the one the build runs for — so a build cannot read a second
// repository and there is no cross-repo credential here: the repositories an
// entry serves that are NOT the one this build runs for are cloned by their own
// jobs at start, and this package only ever bakes the build's own. The build
// says so out loud (Plan.Cloned lists them), because "bake your repos" reads
// like every repo of the project and what it means is this one.
//
// # The generated stage
//
// It bakes at Path(slug) — a per-slug directory under Root, so one image can
// hold several repositories' checkouts without them colliding — and never at
// /work/repo: the work directory an old image baked at belongs to the
// repository that authored its Dockerfile, and an engine stage that wrote there
// would hand one repository's checkout to every job the image serves. A job
// finds its own checkout at its slug's path (IS8).
//
// The clone uses the build's own credential, the git-credentials BuildKit
// secret the catalog build already mounts (docs/project-image-catalog, D1150):
// the same secret file, for the same account, with the same REPO_URL,
// BASE_BRANCH and FUGARO_COMMIT build arguments the catalog build already
// passes. No new credential, no new build argument, and nothing of it reaches
// a layer: the mount is a secret mount, and no ARG or COPY carries a token.
//
// # When nothing is baked
//
// Three things have to hold, and none of them is the switch's to override.
//
// The entry being built is the image the repository runs (a catalog reference
// in its own workflow beats the project's map, T1 reading (b); the map answers
// for a repository that names none), and the project's switch (bake_repos,
// D1901) is on. Only a written false turns the switch off: absent and true are
// one statement everywhere it is read (projectpolicy.Policy.BakesRepos,
// localcfg.BakeReposEffective), so a build reads the absent case — every
// installation predating the field — the same way the settings, the skill and
// the publisher do.
//
// The entry serves no other repository, and the build can say so: a checkout
// baked into an image another repository's jobs run is source they can read
// (F18), and O3's reading (b) bakes "only the repos that share the image's own
// repository grant — typically none for a shared image". So nothing is baked
// into a shared image whatever the switch says: bake_repos: true asks for a
// bake where one may land, and the switch is not an answer to F18.
//
// The map is the evidence for both of those, because it is the only thing a
// build holds that speaks about any repository but its own (a build's
// credential reaches one, O3 (b), so it reads no other repository's config).
// A repository the map does not put on the entry — one that runs it by its own
// workflow's reference — is therefore refused rather than baked: the build
// cannot name the repositories that run the entry as well, and Plan.Cloned
// would be a guess.
//
// An installation that predates both fields is byte-for-byte what it was: it
// maps nothing, so no entry is its own, and no composed stage is ever quiet
// enough to be mistaken for a bake nobody asked for. Nothing here is written
// by `fugaro update`, which copies and builds no entry (O1): a bake arrives at
// a build an operator runs.
package imagebake

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/task"
)

// Root is the directory a baked checkout lives under, one per repository slug.
// It is a constant of the images the engine builds, not of an installation:
// every job image built from a generated definition puts its checkout here, so
// a reader looks in one place.
//
// The reader is internal/runner, and it derives the same path from the job's
// own work directory (runner.BakedDir, whose BakedRepoRoot is the last segment
// of this one) rather than reading this constant: the two agree only while a
// job's work directory is the base image's WORKDIR, which no package can check
// for another. internal/cli's TestBakedRootIsTheJobWorkDirectorysSibling holds
// the three ends together.
const Root = "/work/baked"

// sortedClone lists the names a plan reports as the repositories that clone at
// job start, in one order, so the same map always produces the same sentence.
// Nothing of a plan is hashed, so this is for the person reading the line and
// not for a reader comparing bytes.
func sortedClone(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for slug := range m {
		out = append(out, slug)
	}
	slices.Sort(out)
	return out
}

// Repo is the repository one build bakes.
type Repo struct {
	// Repo is the repository's canonical name (task.CanonicalRepo), which is
	// what image_map keys and what a person reads.
	Repo string
	// Slug is the repository's storage slug (task.Slug): the directory name
	// under Root. It is the same slug a job's own storage paths are keyed by,
	// so the path a bake writes and the path IS8's reader opens are one
	// derivation.
	Slug string
	// URL is the repository's https clone URL without credentials, and Branch
	// the branch the clone starts from — the REPO_URL and BASE_BRANCH the
	// catalog build passes. The build's checkout is reset to FUGARO_COMMIT, so
	// a push after the definition was copied cannot change what the image
	// bakes.
	URL    string
	Branch string
}

// Request is what a build decides its bake from.
type Request struct {
	// Entry is the catalog entry being built.
	Entry string
	// Repo is the repository this build runs for (the checkout's origin).
	Repo Repo
	// Reference is the catalog entry the repository's own workflow names
	// (config.Workflow.ImageName), "" when it names none. A reference beats the
	// map (T1 reading (b)), so a repository that pins an entry runs that entry's
	// image whatever the map says, and baking it here would bake a checkout no
	// job of it would read.
	Reference string
	// Map is the project's effective repo-to-image map (image_map, D1906), keyed
	// by canonical repository name. It is the only evidence a build has about
	// who else runs the entry — a build reads one repository's config — so the
	// bake hangs off it: see Decide.
	Map map[string]string
	// Repos are the project's repositories, as canonical names (task.
	// CanonicalRepo), which is the map's own key space. The map is read here
	// against this list rather than trusted whole: a repository the map does not
	// name is one whose image the build cannot know, because the deploy resolves
	// it by that repository's own reference or by the tag-cover rule and a build
	// reads neither — so it may be a reader of this entry (see Decide).
	Repos []string
	// Default is the name of the entry the project's catalog marks as its
	// default, "" when it marks none (config.Entry.Default). A repository that
	// names no entry of its own runs it, whatever the map says and whether or
	// not the map names that repository at all, which makes it an entry this
	// build cannot call its own: see Decide.
	Default string
	// Bake is the project's effective bake switch (bake_repos, D1901): nil when
	// the project answered nothing, which is what an installation predating the
	// field has, and true or false when an operator answered it. Nil and true
	// are one statement — see Decide — which is what the switch means wherever
	// else it is read (projectpolicy.Policy.BakesRepos).
	Bake *bool
}

// Plan is the bake decision, and the whole of what a build tells a person
// about it.
type Plan struct {
	// Baked is the repository the stage clones, nil when this build bakes
	// nothing.
	Baked *Repo
	// Cloned are the other repositories the project's map puts on this build's
	// image — they run on it and are cloned by their own jobs at start, because
	// the build's credential reaches this build's repository alone (O3 (b)). It
	// is what the build can say about who else reads the image, and it is why a
	// non-empty list refuses the bake: see Decide.
	Cloned []string
	// Reason is why nothing is baked, as a sentence a build can print. It is
	// empty when Baked is set.
	Reason string
	// Notable is whether a build should say this plan out loud: a bake, and a
	// project that asked for one and did not get it because its own switch is
	// off, are both worth a line — and a plan that is quiet because this entry
	// is not the image the repository runs is not: a build of another entry
	// says nothing about it, and printing a reason for each of the entries a
	// project has would read as a failure to the operator who asked for none.
	Notable bool
}

// Decide is the rule: a build bakes the repository it runs for, and only when
// that repository runs this entry, when the project's map leaves the entry to
// that repository alone, and when the project asked for the bake.
//
// The entry a repository runs is the one its own workflow names, then the map's
// answer for it, in the resolver's own order (T1 (b); IS1's one resolver), so
// the bake lands in the image the repository's jobs actually start — a bake in
// an image they never pull is work nobody reads. A repository that runs this
// entry neither way bakes nothing, and says nothing.
//
// # The bake switch, and what it does not decide
//
// bake_repos: false bakes nothing. Anything else — true, or the project
// answering nothing, which is every installation predating the field — bakes
// the repository's own checkout into the entry's image where a bake may land.
// Absent and true decide the same way, because that is what the switch means
// everywhere else it is read (projectpolicy.Policy.BakesRepos): one published
// node does not tell a settings reader "on" and a build "nothing is baked".
//
// A shared image is not somewhere a bake may land, whichever way the switch is
// set (O3 reading (b): the bake set is the repositories that share the image's
// own repository grant, which for a shared image is typically none). Whatever
// is baked into an image the entry's other repositories run is source their
// jobs can read (F18), and bake_repos: true is an operator asking for a bake —
// not the answer to a boundary the owner ruled on. So a non-empty Plan.Cloned
// refuses the bake, and the line a build prints names those repositories as the
// reason rather than as a warning.
//
// # What the map has to say, and what it does not
//
// Both of those are read off the project's map, the only thing a build holds
// that speaks about a repository other than its own: it reads one repository's
// config and its credential reaches one repository (O3 (b)), so a build cannot
// ask what the project's other repositories run. That makes the map a
// requirement and not a hint — a repository the map does not put on the entry,
// even one that runs it by its own workflow's reference, is refused rather than
// baked, because the build cannot name the repositories that run the entry as
// well and Plan.Cloned would be a guess.
//
// The map is not, on its own, evidence that the entry is this repository's: a
// job resolves its image with catalog.Choose, which reads a repository's own
// reference, then the tag-cover rule over the tags it needs, and then the
// catalog's default — and never the map, until IS1 makes the map that resolver.
// So two further things refuse the bake, each of them a way the entry reaches a
// job the map says nothing about. The entry the catalog marks as its default is
// run by every repository of the project that names no entry of its own. And a
// repository the map does not name at all is one whose image the build cannot
// know at all, since the deploy resolves it by that repository's own config
// (Request.Repos). What is left is the case the map is evidence for: every
// repository of the project is accounted for by it, this repository is on this
// entry, and no other repository is.
//
// That account is the project's own and not a proof of who runs the entry: a
// repository the map puts on another entry reaches this one by its own
// workflow's reference or by the tags its jobs need, and a build reads the
// config of no repository but its own (O3 (b)), so it cannot rule that out.
// What it can refuse is the entry the project's account says nothing about, and
// that is what the refusals above do.
func Decide(req Request) Plan {
	// The other repositories this image serves: they are the clone-at-start
	// half of O3 (b) and are named whether or not this build bakes.
	cloned := []string{}
	for _, name := range sortedClone(req.Map) {
		if req.Map[name] == req.Entry && name != req.Repo.Repo {
			cloned = append(cloned, name)
		}
	}
	// The slug is validated here rather than at the stage: a slug that is not
	// one is a bug this package's caller cannot fix by deciding differently,
	// and baking nothing is the direction that cannot write a path a reader
	// would not look at.
	if _, err := Path(req.Repo.Slug); err != nil {
		return Plan{Cloned: cloned, Notable: true, Reason: fmt.Sprintf("this repository's slug %s is not one this engine can bake: %v", req.Repo.Slug, err)}
	}
	switch {
	case req.Reference != "" && req.Reference != req.Entry:
		return Plan{Cloned: cloned, Reason: fmt.Sprintf("the repository names the catalog entry %s in its own workflow, so it runs that image and not this one (a catalog reference beats the map)", req.Reference)}
	case req.Reference == "" && req.Map[req.Repo.Repo] != req.Entry:
		return Plan{Cloned: cloned, Reason: fmt.Sprintf("the project's image map does not put %s on the catalog entry %s, so its jobs do not run this image", req.Repo.Repo, req.Entry)}
	}
	// Only a written false turns the bake off: bake_repos absent and true are
	// one statement everywhere else that reads the switch
	// (projectpolicy.Policy.BakesRepos, localcfg.BakeReposEffective), and a
	// build that read the absent case as anything else would be a second
	// meaning for one field.
	if req.Bake != nil && !*req.Bake {
		return Plan{Cloned: cloned, Notable: true, Reason: "the project turned baking off (bake_repos is false in the published policy), so this image carries no checkout and its jobs clone at start"}
	}
	// The map is the build's only evidence about any repository but its own (O3
	// (b): one credential, one checkout, no second repository's config read), so
	// where it does not put this repository on this entry the build cannot say
	// who else runs the entry. A bake there is a checkout read by repositories
	// the build cannot name — the reference is a fact about this repository's
	// jobs and not about the project's.
	if req.Map[req.Repo.Repo] != req.Entry {
		return Plan{Cloned: cloned, Notable: true, Reason: fmt.Sprintf("this repository runs the catalog entry %s by its own workflow's reference and the project's image map does not put it there, so a build cannot name the other repositories that run the entry and could read a checkout baked into it: `fugaro image map set %s %s` records it in the map a build reads, and the bake follows", req.Entry, req.Repo.Repo, req.Entry)}
	}
	// The catalog's default is the entry every repository that names nothing
	// runs, and the deploy resolves that without reading the map (catalog.Choose
	// step 3). So the map saying nothing about a repository is not the map saying
	// the repository runs something else: a project whose map puts this
	// repository alone on the default entry still hands that entry's image to
	// every other repository of the project, and the bake would be a checkout in
	// their jobs (F18).
	if req.Default != "" && req.Default == req.Entry {
		return Plan{Cloned: cloned, Notable: true, Reason: fmt.Sprintf("the catalog marks the entry %s as the project's default, so every repository of the project that names no entry of its own runs this image, and their jobs could read whatever is baked into it: the bake follows only where the entry is not the default", req.Entry)}
	}
	// A repository the map does not name at all is one whose image this build
	// cannot know: the deploy resolves it by that repository's own reference or
	// by the tag-cover rule, and a build reads neither — one repository's config
	// and one credential (O3 (b)). It is therefore a possible reader of this
	// entry, and the map is only whole evidence when it names every repository
	// the project has.
	if unknown := unmapped(req.Repos, req.Map, req.Repo.Repo); len(unknown) > 0 {
		return Plan{Cloned: cloned, Notable: true, Reason: fmt.Sprintf("the project's image map puts none of the project's other repositories (%s) on an entry, so this build cannot say which image their jobs run or whether they could read a checkout baked into this one: `fugaro image map set <repo> <entry>` records each of them, and the bake follows", strings.Join(unknown, ", "))}
	}
	// A shared image is O3 (b)'s own boundary and not the switch's: bake_repos:
	// true asks for a bake, and it does not decide who may read the result.
	if len(cloned) > 0 {
		return Plan{Cloned: cloned, Notable: true, Reason: fmt.Sprintf("the other repositories the project maps to this entry (%s) run this image too, so their jobs can read whatever is baked into it, and O3's reading (b) bakes nothing there — the bake follows only where an image serves this repository alone", strings.Join(cloned, ", "))}
	}
	baked := req.Repo
	return Plan{Baked: &baked, Cloned: cloned, Notable: true}

}

// unmapped lists the project's repositories the map names no entry for, in one
// order, other than repo itself: the repositories whose image a build cannot
// know, and therefore the ones whose jobs it cannot rule out as readers of an
// entry it bakes into. A repository the map names, whatever it names for it, is
// left out — the map is a statement about it, and Decide refuses the bake where
// that statement puts it on this entry.
func unmapped(repos []string, m map[string]string, self string) []string {
	var out []string
	for _, name := range repos {
		if name == self {
			continue
		}
		if _, known := m[name]; !known {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// markerPrefix begins the marker line the generated stage carries, so the
// definition's own reader can tell a composed definition from the entry's own
// bytes and find the directory the checkout was baked at without parsing the
// run below it (BakedDir).
const markerPrefix = "# fugaro bake: "

// slugRE is what a slug may be: the characters task.Slug emits, with no slash
// — Root/slug is one directory, and a slug that walked out of Root would put a
// checkout somewhere a reader does not look, or somewhere it must not.
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Path is where a baked checkout of slug lives. It refuses a slug that is not
// one rather than joining it: the path is spelled into a generated Dockerfile
// and compared against what a reader opens, so a slug that is not a slug is a
// refusal and never a path.
func Path(slug string) (string, error) {
	if len(slug) > 128 || !slugRE.MatchString(slug) {
		return "", fmt.Errorf("%q is not a repository slug", slug)
	}
	return Root + "/" + slug, nil
}

// Stage is the Dockerfile stage a build appends to bake r: the marker, the
// three build arguments the catalog build already passes, and one RUN that
// clones, pins the commit, and hands the checkout to the rest of the image
// through finalize-checkout (the derived build's own last step, which strips
// credential configuration and holds the origin to an https URL).
//
// It is written to run as the image's own user, which the base image sets to
// fugaro and which owns /work: no USER line, so the stage cannot change whom a
// job's image runs as.
//
// The stage clones r.URL and no other URL: the build arguments a catalog build
// passes are the building repository's, and the ones a REBUILD passes are its
// own — the nightly check assigns an entry to the repository its source names,
// or, for an inline definition, to the repository the project's map puts it on,
// falling back to the project's first (infra's catalogCheckRepos), which is not
// always the checkout that composed the definition. Without the check,
// that rebuild would clone one repository into another's slug path, under the
// marker and the tag the composed definition gave it, and the smoke would pass
// on the pairing it read from the two. So the stage refuses a REPO_URL that is
// not the one it was composed for, and the rebuild fails saying so instead of
// baking the wrong source.
//
// The refusal is about the repository, not about the spelling: a definition is
// composed from a checkout's origin, which is the operator's remote, while the
// catalog build itself passes repourl.CloneURL's canonical form — so a guard
// holding the two to one string would refuse the nightly rebuild of every
// definition composed from an origin without ".git", or with a trailing slash,
// or with a capital letter. The guard and the composed spelling are one fold
// (originID), which is task.CanonicalRepo's: what git and every other reader
// here call one repository is one answer to this REFUSAL too.
func Stage(r Repo) (string, error) {
	dir, err := Path(r.Slug)
	if err != nil {
		return "", err
	}
	if err := httpsOrigin(r.URL); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n", markerPrefix, dir)
	b.WriteString("# Engine-generated by fugaro image build (the image setup skill's bake step): the checkout of\n")
	b.WriteString("# the repository this build runs for, at the commit the build recorded, for that repository's\n")
	b.WriteString("# own jobs. Every other repository this image serves clones at job start: the build's\n")
	b.WriteString("# credential reaches this repository alone.\n")
	b.WriteString("ARG REPO_URL\nARG BASE_BRANCH\nARG FUGARO_COMMIT=\"\"\n")
	fmt.Fprintf(&b, "RUN --mount=type=secret,id=git-credentials,uid=1000,required=false \\\n"+
		"    case \"$FUGARO_COMMIT\" in *[!0-9a-f]*) echo \"FUGARO_COMMIT is not a commit ID\" >&2; exit 1 ;; esac \\\n"+
		" && %s \\\n"+
		" && install -d -m 0755 %s \\\n"+
		" && GIT_TERMINAL_PROMPT=0 git -c credential.helper= \\\n"+
		"      -c credential.helper='store --file=/run/secrets/git-credentials' \\\n"+
		"      clone --quiet --branch \"$BASE_BRANCH\" --single-branch -- \"$REPO_URL\" %s \\\n"+
		" && commit=\"${FUGARO_COMMIT:-HEAD}\" \\\n"+
		" && { git -C %s cat-file -e \"$commit^{commit}\" 2>/dev/null \\\n"+
		"      || GIT_TERMINAL_PROMPT=0 git -C %s -c credential.helper= \\\n"+
		"           -c credential.helper='store --file=/run/secrets/git-credentials' \\\n"+
		"           fetch --quiet origin \"$commit\" \\\n"+
		"      || { echo \"commit $commit is no longer reachable: the branch was force-pushed; re-run the build\" >&2; exit 1; }; } \\\n"+
		" && git -C %s reset --quiet --hard \"$commit\" \\\n"+
		" && /usr/local/lib/fugaro/finalize-checkout %s\n",
		repoURLGuard(r.URL), Root, dir, dir, dir, dir, dir)
	return b.String(), nil
}

// repoURLGuardFormat is the shell the generated stage runs to hold a build's
// REPO_URL to the repository the definition was composed for, with the one
// substitution it takes: the composed origin as originID spells it. The two
// folds the shell does are the two originID does — ASCII case down, then a
// trailing "/" and a trailing ".git" off — because the build's REPO_URL is not
// the composed spelling: a catalog build passes repourl.CloneURL's canonical
// form while this definition was composed from the checkout's origin, and the
// two differ by exactly those.
//
// It is a constant, and separate from the stage, so a test can run it through
// sh -c with a REPO_URL of each spelling: the invariant is what the shell does
// with two URLs and not the words of its refusal, and a guard whose text alone
// is pinned can be relaxed to an always-matching arm with nothing failing.
const repoURLGuardFormat = `repo=$(printf '%%s' "$REPO_URL" | tr 'A-Z' 'a-z') \
 && repo=${repo%%/} && repo=${repo%%.git} \
 && case "$repo" in '%s') ;; \
      *) echo "this definition bakes one repository's checkout, and REPO_URL is not it: a rebuild of the entry has to run as the repository whose checkout it bakes" >&2; exit 1 ;; esac`

// repoURLGuard is that shell for one composed URL, byte for byte what the
// stage carries.
func repoURLGuard(url string) string { return fmt.Sprintf(repoURLGuardFormat, originID(url)) }

// originID is a clone URL as the repository it names, spelled one way: the
// fold task.CanonicalRepo puts a repository name through — ASCII case down and
// a trailing ".git" off — applied to the whole URL, with a trailing "/" off
// first because a URL may carry one and a repository name may not.
//
// The case fold comes first, as it does in the shell guard and in CanonicalRepo:
// ".GIT" is the same suffix as ".git" to whoever wrote the remote, and a fold
// that trimmed before lowercasing would leave "…/app.GIT" as "…/app.git" — a
// spelling the guard then refuses against every REPO_URL, its own included.
//
// It is the identity the guard compares and the spelling the stage writes into
// it, so a definition composed from any of git's spellings of one repository
// is rebuilt by the nightly check, which passes the canonical one. It is not a
// second parse of the URL: nothing here decides where the host ends, because
// two URLs git clones the same repository from are already the same string
// once this fold is applied, and a URL that is not one is refused before a
// stage is written (httpsOrigin).
func originID(u string) string {
	return strings.TrimSuffix(strings.TrimSuffix(task.ASCIILower(u), "/"), ".git")
}

// httpsOrigin refuses a URL a stage may not spell into a definition: the URL is
// written into the generated Dockerfile as a quoted shell word and compared
// against the build's own REPO_URL, so it has to be one word and it must not
// carry a credential — a definition is copied into the project's bucket and
// read by every build and job of the entry, and finalize-checkout would refuse
// the checkout it produced anyway (a userinfo origin fails that contract).
func httpsOrigin(u string) error {
	// Nothing here prints the URL: a URL that reached this function may be one
	// that carries a credential, which is the reason it is refused.
	parsed, err := url.Parse(u)
	if err != nil {
		return errors.New("this repository's clone URL is not a URL")
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("this repository's clone URL is not an https URL without credentials, which is what a generated definition may spell")
	}
	if strings.ContainsAny(u, "'\"` \t\r\n\\$") {
		return errors.New("this repository's clone URL holds a character a generated definition cannot quote")
	}
	return nil
}

// Append is the composed definition: the entry's own bytes with the stage that
// bakes p.Baked on the end, and the bytes unchanged when p bakes nothing. A
// definition is appended to, never rewritten: the entry's own Dockerfile is
// what it is, and one RUN at the end of it runs in the image's last stage —
// the one every job starts from.
//
// An entry whose own Dockerfile already clones (the hand-written definitions
// of D1208, e.g. images/catalog/*/Dockerfile) gets this stage as well, which
// pays a second clone for a repository the image already holds. Reading a
// Dockerfile to find out whether it clones is not a thing this package can do
// honestly, and the two checkouts are at different paths, so the entry's own
// bytes are left alone and the duplicate is a cost the person who wrote them
// can drop — the skill's generated definitions carry no clone of their own,
// which is what makes the engine's stage the only one.
func Append(definition []byte, p Plan) ([]byte, error) {
	if p.Baked == nil {
		return definition, nil
	}
	stage, err := Stage(*p.Baked)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(definition)+1+len(stage))
	out = append(out, definition...)
	if n := len(out); n > 0 && out[n-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, stage...), nil
}

// BakedSlug is the repository slug a composed definition bakes its checkout
// for, and whether it bakes one at all. A reader that has to know WHICH
// repository a definition bakes — the nightly rebuild, which runs as one
// repository and cannot reproduce another's definition — reads this; BakedDir
// is the path derived from it.
//
// It answers with the LAST marker: a definition that already bakes is composed
// again by appending, so it can carry several stages and the one that runs is
// the last. Reading the first would hand a reader the directory of a stage an
// earlier one replaced — and the repository of a build that is not the one the
// definition now bakes.
func BakedSlug(definition []byte) (string, bool) {
	var found string
	for line := range strings.Lines(string(definition)) {
		line = strings.TrimSuffix(line, "\n")
		rest, ok := strings.CutPrefix(line, markerPrefix)
		if !ok {
			continue
		}
		// The marker names Root/slug, and only a path under Root is one: a
		// definition that spells anything else was not composed by this package
		// and is not read as though it had been.
		slug, ok := strings.CutPrefix(rest, Root+"/")
		if !ok {
			continue
		}
		if _, err := Path(slug); err == nil {
			found = slug
		}
	}
	return found, found != ""
}

// BakedDir is the directory a composed definition bakes its checkout at, and
// whether it bakes one at all. It reads the marker line the generated stage
// starts with, which is the one thing about a definition both the build that
// wrote it and a reader holding nothing but the bytes can agree on — the smoke
// spec a build submits (image.SpecForCloud) has to check the checkout where the
// image actually put it, and the nightly check reads the same object the build
// wrote.
func BakedDir(definition []byte) (string, bool) {
	slug, ok := BakedSlug(definition)
	if !ok {
		return "", false
	}
	dir, err := Path(slug)
	if err != nil {
		return "", false
	}
	return dir, true
}
