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
// Nothing is baked unless the entry being built is the image the repository
// runs — a catalog reference in its own workflow beats the project's map (T1
// reading (b)), and the map answers for a repository that names none — and the
// project's answer to D1901 says so.
//
// The default (bake_repos absent, which is every installation predating the
// field) is narrower than "on": it bakes a repository the project's map puts on
// this entry, and only into an entry the project puts nothing else on. The map
// is the only thing a build can read that says which repositories an entry
// serves, so an entry another repository runs is left alone until an operator
// writes bake_repos: true — that repository's jobs could read whatever was
// baked into a shared image (F18), which is the boundary O3 (b) leaves to the
// operator's own answer. Every refusal says so out loud and names the command
// that asks for the bake.
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
)

// Root is the directory a baked checkout lives under, one per repository slug.
// It is a constant of the images the engine builds, not of an installation:
// every job image built from a generated definition puts its checkout here, so
// a reader looks in one place.
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
	// by canonical repository name.
	Map map[string]string
	// Bake is the project's effective bake switch (bake_repos, D1901): nil when
	// the project answered nothing, which is what an installation predating the
	// field has, and true or false when an operator answered it. The three
	// states decide differently — see Decide.
	Bake *bool
}

// Plan is the bake decision, and the whole of what a build tells a person
// about it.
type Plan struct {
	// Baked is the repository the stage clones, nil when this build bakes
	// nothing.
	Baked *Repo
	// Cloned are the other repositories this build's image serves — they run on
	// it and are cloned by their own jobs at start, because the build's
	// credential reaches this build's repository alone (O3 (b)).
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
// that repository runs this entry and the project asked for the bake.
//
// The entry a repository runs is the one its own workflow names, then the map's
// answer for it, in the resolver's own order (T1 (b); IS1's one resolver), so
// the bake lands in the image the repository's jobs actually start — a bake in
// an image they never pull is work nobody reads. A repository that runs this
// entry neither way bakes nothing, and says nothing.
//
// # The bake switch, in its three states
//
// bake_repos: false bakes nothing. bake_repos: true bakes the repository's own
// checkout into the entry's image wherever the project says the repository runs
// it, a shared image among them: that is the operator's own answer to F18, and
// the build names the other repositories that can then read the checkout.
//
// The default — the project answered nothing, which is every installation
// predating the field — is narrower, and it is D1901's own words: the switch
// says the generated Dockerfiles clone each MAPPED repository. So the default
// bakes a repository the project's map puts on this entry, and only into an
// entry the map puts nothing else on. The map is the only thing a build can
// read that says who else runs an entry, and its absence is the honest answer
// to O3's "typically none for a shared image": nothing is baked into an image
// another repository runs, and nothing is baked for a repository the project
// never said runs this image, until an operator says so. Both refusals are
// Notable, and each names the one command that asks.
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
	// An answered switch decides on its own: false bakes nothing anywhere, and
	// true bakes the repository's own checkout into an entry other
	// repositories run as well (Plan.Cloned names them) — that is the
	// operator's answer to F18.
	if req.Bake != nil {
		if !*req.Bake {
			return Plan{Cloned: cloned, Notable: true, Reason: "the project turned baking off (bake_repos is false in the published policy), so this image carries no checkout and its jobs clone at start"}
		}
		baked := req.Repo
		return Plan{Baked: &baked, Cloned: cloned, Notable: true}
	}
	switch {
	case req.Map[req.Repo.Repo] != req.Entry:
		// The default is the map's, so a repository the map does not put on
		// this entry is not baked for by it — including one that runs the
		// entry by its own workflow's reference, which the map then has to
		// agree with before anything is written into an image.
		return Plan{Cloned: cloned, Notable: true, Reason: fmt.Sprintf("the project's image map (image_map) does not put %s on the catalog entry %s, so nothing is baked by default: `fugaro image map set %s %s` says it runs this image, and bake_repos: true asks for the bake without a map", req.Repo.Repo, req.Entry, req.Repo.Repo, req.Entry)}
	case len(cloned) > 0:
		// The entry serves another repository: whatever is baked into it is
		// source that repository's jobs can read (F18), so the default writes
		// nothing there.
		return Plan{Cloned: cloned, Notable: true, Reason: fmt.Sprintf("this image serves the other repositories the project maps to it (%s), whose jobs can read whatever is baked into it, so nothing is baked unless the project asks for it: bake_repos: true bakes %s's checkout here", strings.Join(cloned, ", "), req.Repo.Repo)}
	}
	baked := req.Repo
	return Plan{Baked: &baked, Cloned: cloned, Notable: true}
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
// or to the project's first when it has none (infra's catalogCheckRepos), which
// is not always the checkout that composed the definition. Without the check,
// that rebuild would clone one repository into another's slug path, under the
// marker and the tag the composed definition gave it, and the smoke would pass
// on the pairing it read from the two. So the stage refuses a REPO_URL that is
// not the one it was composed for, and the rebuild fails saying so instead of
// baking the wrong source.
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
		" && case \"$REPO_URL\" in '%s') ;; \\\n"+
		"      *) echo \"this definition bakes one repository's checkout, and REPO_URL is not it: a rebuild of the entry has to run as the repository whose checkout it bakes\" >&2; exit 1 ;; esac \\\n"+
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
		r.URL, Root, dir, dir, dir, dir, dir)
	return b.String(), nil
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
func BakedSlug(definition []byte) (string, bool) {
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
			return slug, true
		}
	}
	return "", false
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
