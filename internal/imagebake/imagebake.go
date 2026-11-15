// Package imagebake is the bake step a catalog build generates: the stages it
// appends to an entry's Dockerfile so the image carries the checkouts of the
// repositories that entry serves, and the rule that says which they are.
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
// # The owner's ruling (D2301, 2026-11-13: several repositories, one credential)
//
// One catalog build bakes every repository its entry serves, not the one it
// runs for alone: D2301 overturns O3's reading (b) ("the bake set is the
// repositories that share the image's own repository grant — typically none for
// a shared image"), which held while a build's credential reached exactly the
// repository it ran for.
//
// What replaced it is a build credential over exactly those repositories: one
// request to the credentials service's build route, for the entry being built,
// and one narrowed answer covering the entry's repositories and no others
// (internal/buildtoken, docs/shared-jobs-default/contract-build-token.md; the
// build's own step asks for it — internal/cli's `fugaro image git-credential
// --entry`). It is what makes a second repository's checkout bakeable at all,
// and it is F19's single point of compromise: the clone runs in a build step
// Fugaro generates entirely, whose Dockerfile the entry cannot alter, so no
// entry RUN can read the credential (B8 — the stages this package composes into
// the entry's own definition carry the credential until B8 moves the clone out
// of it).
//
// What that leaves is F23: a job whose image carries several checkouts can read
// the others' source, which is the exposure F18 named. Its answer is not a
// refusal here but the runner's: it removes every baked directory but the run's
// own before the agent starts (D2309, task S9).
//
// # The generated stages
//
// One stage per repository the entry serves. Each bakes at Path(slug) — a
// per-slug directory under Root, so an image can hold several repositories'
// checkouts without them colliding — and never at /work/repo: the work
// directory an old image baked at belongs to the repository that authored its
// Dockerfile, and an engine stage that wrote there would hand one repository's
// checkout to every job the image serves. A job finds its own checkout at its
// slug's path (IS8).
//
// The stage for the repository the build runs for is the one a nightly rebuild
// composes again, so it takes REPO_URL, BASE_BRANCH and FUGARO_COMMIT as build
// arguments and holds REPO_URL to the repository it was composed for (Stage).
// The stages for the entry's other repositories spell their clone URL and
// branch into the definition, because the build's own arguments name another
// repository and the build holds no commit of a repository it does not run for
// (OtherStage).
//
// Nothing of a credential reaches a layer: the clone mounts the git-credentials
// BuildKit secret (docs/project-image-catalog, D1150) and no ARG, ENV or COPY
// names a token, and the one secret mount of a step is what B8 replaces with the
// build route's narrowing.
//
// Every stage ends with the submodule guard: a repository a checkout declares as
// a submodule, and that the build's credential does not cover, fails the build
// naming the repository rather than being fetched (submoduleGuard). The
// credential reaches one host, so the guard holds a submodule's own host to the
// plan's: a repository of the entry's owner and name on some other host is not
// one this build's credential covers.
//
// # When nothing is baked
//
// Two things have to hold, and neither is the switch's to override.
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
// The repositories the entry serves are the project's own, and the build can
// say so: the map is the one thing a build holds that speaks about a repository
// other than its own (it reads one repository's config), so the entry's
// repositories are read off it, keyed against the project's own list. An entry
// the map serves a repository the project does not admit is refused, naming the
// repository rather than baking a checkout the build cannot clone: Request.Repos
// is the project's own answer for the repositories it has.
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

// Repo is one repository a build bakes.
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
	// by canonical repository name. It is what says which repositories the entry
	// being built serves, and it is the only evidence a build has about any
	// repository but the build's own — a build reads one repository's config —
	// so the bake hangs off it: see Decide.
	Map map[string]string
	// Repos are the project's repositories, each resolved to where it clones
	// from — its canonical name, its storage slug, its https URL and its base
	// branch. A repository the project lists but cannot place (no provider
	// recorded for it, or no URL that names it) is not one of these, and the
	// build bakes none of a repository it cannot clone: see Decide.
	Repos []Repo
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
	// Baked are the repositories the stages clone — every repository the
	// project's map puts on the entry being built (D2301), in one order, the
	// build's own LAST; empty when this build bakes nothing.
	//
	// The last element's being the build's own is a fact a reader relies on and
	// not a decoration: BakedSlug answers one repository per definition, and the
	// stage that runs last is the one the smoke and the nightly rebuild read, so
	// the checkout a definition is known by is the one the build baked as its
	// own (Append).
	Baked []Repo
	// Reason is why nothing is baked, as a short clause a build prints inside
	// parentheses. It is empty when Baked is set.
	Reason string
	// Fix is the command that would let the bake follow, printed after the
	// reason; empty when no command of the operator's changes the answer.
	Fix string
	// Asked is whether bake_repos was written true in the config the build
	// reads. The switch itself still treats absent and true as one statement;
	// Asked only decides whether a build owes the operator an explanation, since
	// one that never asked for a bake has nothing to explain.
	Asked bool
	// Notable is whether a build should say this plan out loud: a bake, and a
	// project that asked for one and did not get it because its own switch is
	// off, are both worth a line — and a plan that is quiet because this entry
	// is not the image the repository runs is not: a build of another entry
	// says nothing about it, and printing a reason for each of the entries a
	// project has would read as a failure to the operator who asked for none.
	Notable bool
}

// Repositories is the canonical names of the repositories a plan bakes, in
// Baked's order: the repositories the build's one credential has to cover, and
// the set the submodule guard holds a checkout to.
func (p Plan) Repositories() []string {
	out := make([]string, 0, len(p.Baked))
	for _, r := range p.Baked {
		out = append(out, r.Repo)
	}
	return out
}

// Decide is the rule: a build bakes every repository the project's map puts on
// the entry being built, and only when that entry is one the build's own
// repository runs, the entry's repositories are the project's own, and the
// project asked for the bake.
//
// The entry a repository runs is the one its own workflow names, then the map's
// answer for it, in the resolver's own order (T1 (b); IS1's one resolver), so
// the bake lands in the image the repository's jobs actually start — a bake in
// an image they never pull is work nobody reads. A repository that runs this
// entry neither way bakes nothing, and says nothing.
//
// # Which repositories an entry bakes
//
// The repositories the map puts on the entry being built, the build's own
// included: they are the ones its jobs start, so they are the ones whose
// checkouts belong in its image (D2301). The build's own is Baked's last, which
// is what BakedSlug and the smoke read a definition by.
//
// The set is not a proof of who runs the entry — a repository the map puts on
// another entry reaches this one by its own workflow's reference or by the tags
// its jobs need, and a build reads the config of no repository but its own — but
// it is the project's own account of its repositories, and it is what the
// credential is minted from: the request names the entry, and the service reads
// the repositories a build may be given from the published map for that entry
// (contract-build-token.md rule 8). A repository that reaches the entry without
// the map saying so is a clone at job start, which is what it was before the
// bake existed.
//
// # The map, and the project's own list
//
// The map is the only thing a build holds that speaks about any repository but
// its own, and it is read against Request.Repos — the project's own answer for
// the repositories it has, each resolved to where it clones from. A repository
// the map puts on this entry and the project does not admit is refused, naming
// it: the build bakes no repository it cannot clone, and a key the project's own
// list does not answer for is either a map the project does not own or a
// repository it records no provider for. A repository of the entry's on another
// host is refused the same way, for the credential's own reason: one credential
// is for one host.
//
// # The bake switch
//
// bake_repos: false bakes nothing. Anything else — true, or the project
// answering nothing, which is every installation predating the field — bakes
// the entry's repositories into its image. Absent and true decide the same way,
// because that is what the switch means everywhere else it is read
// (projectpolicy.Policy.BakesRepos): one published node does not tell a settings
// reader "on" and a build "nothing is baked".
//
// A plan that bakes is Plan.Notable; one that bakes nothing is quiet unless the
// operator asked for a bake or the reason is one they can act on.
func Decide(req Request) Plan {
	asked := req.Bake != nil && *req.Bake
	// The slug is validated here rather than at the stage: a slug that is not
	// one is a bug this package's caller cannot fix by deciding differently,
	// and baking nothing is the direction that cannot write a path a reader
	// would not look at.
	if _, err := Path(req.Repo.Slug); err != nil {
		return Plan{Notable: true, Asked: asked, Reason: fmt.Sprintf("slug %s is not bakeable: %v", req.Repo.Slug, err)}
	}
	switch {
	case req.Reference != "" && req.Reference != req.Entry:
		return Plan{Asked: asked, Reason: fmt.Sprintf("this repo runs %s", req.Reference)}
	case req.Reference == "" && req.Map[req.Repo.Repo] != req.Entry:
		return Plan{Asked: asked, Reason: fmt.Sprintf("the image map doesn't put %s on it", req.Repo.Repo)}
	}
	// Only a written false turns the bake off: bake_repos absent and true are
	// one statement everywhere else that reads the switch
	// (projectpolicy.Policy.BakesRepos, localcfg.BakeReposEffective), and a
	// build that read the absent case as anything else would be a second
	// meaning for one field.
	if req.Bake != nil && !*req.Bake {
		return Plan{Notable: true, Asked: asked, Reason: "bake_repos is false"}
	}
	// The map is the build's only evidence about any repository but its own, so
	// where it does not put this repository on this entry there is no bake to
	// compose: the stage a rebuild of this definition could reproduce is the
	// build's own checkout, and its jobs never pull this image.
	if req.Map[req.Repo.Repo] != req.Entry {
		return Plan{Notable: true, Asked: asked, Reason: fmt.Sprintf("the image map doesn't list %s", req.Repo.Repo), Fix: fmt.Sprintf("To bake: fugaro image map set %s %s", req.Repo.Repo, req.Entry)}
	}
	baked, refused := entryRepos(req)
	if refused != "" {
		return Plan{Notable: true, Asked: asked, Reason: refused}
	}
	return Plan{Baked: baked, Notable: true, Asked: asked}
}

// entryRepos is the entry's repositories: every repository the project's map
// puts on the entry being built, in one order, the build's own last, or the
// reason no bake can be composed.
//
// The map is read against the project's own repositories rather than trusted
// whole: a repository the project does not admit — a key the project's list does
// not answer for, or one it lists without a provider to clone it from — is one
// this build cannot bake, and the reason names it. A slug that is not one is
// refused for Path's reason: it is spelled into a generated definition. And so is
// a repository that clones from another host than the build's own: a plan is one
// credential, a credential is for one host (rule 5 of contract-build-token.md),
// and a stage composed for the other one would be a clone the credential does
// not reach.
//
// The build's own repository is the exception, and it is not an exemption: it is
// the one repository whose clone URL and branch this build holds itself
// (Request.Repo), so it is admitted by construction, and the project's list
// answers for the others alone.
func entryRepos(req Request) ([]Repo, string) {
	byName := make(map[string]Repo, len(req.Repos))
	for _, r := range req.Repos {
		byName[r.Repo] = r
	}
	var names []string
	for name, entry := range req.Map {
		if entry == req.Entry {
			names = append(names, name)
		}
	}
	// The build's own repository is the last stage, whichever order the map
	// hands the others in (Plan.Baked).
	slices.SortFunc(names, func(a, b string) int {
		if (a == req.Repo.Repo) != (b == req.Repo.Repo) {
			if a == req.Repo.Repo {
				return 1
			}
			return -1
		}
		return strings.Compare(a, b)
	})
	out := make([]Repo, 0, len(names))
	for _, name := range names {
		// The build's own repository is the checkout this build holds: its URL,
		// its branch and its slug are this command's own and not what the
		// project's list records, and the project's list need not hold it at all
		// — a build composes the definition of an entry its own checkout may be
		// missing from the local config of. It is Baked's last, and this is the
		// element that makes that a fact rather than a sort's side effect.
		if name == req.Repo.Repo {
			out = append(out, req.Repo)
			continue
		}
		// The map is a config value and a published node, so a key is held to
		// the shape of a repository name before it reaches a line a person reads
		// or a command they run.
		if _, err := task.CanonicalRepo(name); err != nil {
			return nil, fmt.Sprintf("the map names %q, which is not a repository", name)
		}
		r, admitted := byName[name]
		if !admitted {
			return nil, fmt.Sprintf("the map puts %s on it, which is not a repository this project admits", name)
		}
		if _, err := Path(r.Slug); err != nil {
			return nil, fmt.Sprintf("slug %s is not bakeable: %v", r.Slug, err)
		}
		// One credential is for one host (rule 5 of contract-build-token.md), so
		// a plan that mixed two hosts could not be served by the one credential
		// this build asks for: a repository of the entry's that clones from
		// another host is refused by name rather than composed into a stage whose
		// clone that credential would not reach.
		if own, host := urlHost(req.Repo.URL), urlHost(r.URL); own == "" || host != own {
			return nil, fmt.Sprintf("the map puts %s on it, which does not clone from the host this build's one credential is for", name)
		}
		out = append(out, r)
	}
	return out, ""
}

// urlHost is the host a clone URL sends its credential to, lower-cased, or ""
// when the URL is not one — the comparison the one-credential-per-host rule is
// about. An empty answer leaves the repository's host equal to nothing, so the
// refusal is what an unparseable URL gets: the direction in which this is wrong
// is a bake that does not happen, never a credential offered to a host it was
// not minted for.
func urlHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return task.ASCIILower(parsed.Host)
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
//
// covered is the credential's own reach — Plan.Baked of the plan this stage was
// composed for — and the stage ends with the submodule guard over it
// (submoduleGuard).
func Stage(r Repo, covered []Repo) (string, error) {
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
	b.WriteString("# own jobs. The entry's other repositories are baked by the stages beside this one.\n")
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
		" && %s \\\n"+
		" && /usr/local/lib/fugaro/finalize-checkout %s\n",
		repoURLGuard(r.URL), Root, dir, dir, dir, dir, dir, submoduleGuard(dir, covered), dir)
	return b.String(), nil
}

// OtherStage is the stage that bakes one of the entry's other repositories: the
// same clone as Stage, with r's own URL and branch spelled into the definition
// instead of the build's REPO_URL and BASE_BRANCH arguments, which name another
// repository.
//
// It pins no commit, and that is the one thing it cannot do: the build holds the
// commit of the repository it runs for and no other, so the clone takes the
// branch's head at the moment the build runs. A definition composed once and
// rebuilt later therefore bakes a newer checkout of those repositories than of
// the build's own, which is what an entry's own Dockerfile that clones by hand
// has always done — and the reason a repository that wants its image pinned
// pins it by naming the entry its own workflow runs.
//
// The branch and the URL are config values: the branch is held to the shape of a
// branch name (a definition may not spell arbitrary text) and the URL to the
// same https-without-credentials rule the build's own is held to, with the
// repository named in the refusal, since a definition is composed for several of
// them. The repository's name is held to its canonical form for the same reason
// a slug is: it is a config value too, and the refusal and the definition's own
// comment both name it.
func OtherStage(r Repo, covered []Repo) (string, error) {
	if _, err := task.CanonicalRepo(r.Repo); err != nil {
		return "", fmt.Errorf("the repository name %q is not a repository", r.Repo)
	}
	dir, err := Path(r.Slug)
	if err != nil {
		return "", err
	}
	if err := httpsOrigin(r.URL); err != nil {
		return "", fmt.Errorf("%s: %w", r.Repo, err)
	}
	if !branchRE.MatchString(r.Branch) {
		return "", fmt.Errorf("the branch %q of %s is not a branch name a generated definition may spell", r.Branch, r.Repo)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n", markerPrefix, dir)
	fmt.Fprintf(&b, "# Engine-generated by fugaro image build (the image setup skill's bake step): the checkout of\n")
	fmt.Fprintf(&b, "# %s, one of the repositories the catalog entry's image serves, at the head of %s when this\n",
		r.Repo, r.Branch)
	b.WriteString("# build ran. The repository this build runs for is baked by the last stage of this definition.\n")
	fmt.Fprintf(&b, "RUN --mount=type=secret,id=git-credentials,uid=1000,required=false \\\n"+
		"    install -d -m 0755 %s \\\n"+
		" && GIT_TERMINAL_PROMPT=0 git -c credential.helper= \\\n"+
		"      -c credential.helper='store --file=/run/secrets/git-credentials' \\\n"+
		"      clone --quiet --branch '%s' --single-branch -- '%s' %s \\\n"+
		" && %s \\\n"+
		" && /usr/local/lib/fugaro/finalize-checkout %s\n",
		Root, r.Branch, r.URL, dir, submoduleGuard(dir, covered), dir)
	return b.String(), nil
}

// branchRE is what a branch a generated definition may spell looks like: the
// characters git's own refname rules allow a branch, starting with one a shell
// word cannot read as an option. The stage quotes it, so what this closes is not
// a quoting break but a branch a `git clone --branch` would refuse at build time
// — an empty one, or one starting with "-".
var branchRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// submoduleGuard is the shell a stage runs after its clone: every submodule the
// checkout declares must be a repository the build's credential covers (covered,
// the plan's own repositories), and one that is not fails the build naming the
// repository rather than being fetched.
//
// It reads the checkout's .gitmodules with git's own parser — the file is the
// baked repository's, so a line-oriented read of it here would be a second
// parser for a format that has one — and what a refusal names is the repository
// that declaration resolves to, folded the way the covered set is: the host, the
// owner and the name, lower-cased, with a trailing "/" and ".git" off. Two
// things follow from that fold and both are the point of it. A URL that carries
// a credential cannot put it in the build log, because everything before the
// first "@" is off the text a refusal prints. And a repository on another host
// is not this build's to fetch whatever its owner and name are: the covered set
// holds the host, because the credential is for one host (rule 5 of
// contract-build-token.md) and this stage's clone is the one place a submodule
// URL would be followed with it.
//
// A URL it cannot read as a repository — a relative one, an ssh one in git's
// scp spelling, one whose path is not owner/name — is refused: a repository the
// credential does not demonstrably cover is not one this build fetches. The
// refusal is fail-closed in the direction that matters, since a fold that
// matched more than the URL really names would fetch source the credential does
// not cover.
func submoduleGuard(dir string, covered []Repo) string {
	folded := make([]string, 0, len(covered))
	for _, r := range covered {
		if id := submoduleID(r.URL); id != "" {
			folded = append(folded, id)
		}
	}
	return "submodules=$(git -C " + dir + " config --file " + dir + "/.gitmodules --get-regexp '^submodule\\..*\\.url$' 2>/dev/null | cut -d' ' -f2- || true)" +
		" && for url in $submodules; do" +
		" repo=$(printf '%s' \"$url\" | tr 'A-Z' 'a-z');" +
		" repo=${repo%/}; repo=${repo%%.git}; repo=${repo#*://}; repo=${repo#*@};" +
		" case \" " + strings.Join(folded, " ") + " \" in *\" $repo \"*) [ -n \"$repo\" ] && continue ;; esac;" +
		" echo \"a submodule of this checkout is ${repo:-a repository this build cannot read}, which this build's credential does not cover: it covers the repositories of the entry being built and no others\" >&2; exit 1;" +
		" done"
}

// submoduleID is the repository a clone URL names, as submoduleGuard's own
// shell spells it: the host and the path, lower-cased, with a trailing "/" and
// ".git" off. Go computes the covered set here and the shell folds the
// submodule's own URL, so the two spellings have to be one — a stage whose
// covered set the shell could not match would refuse every submodule of a
// repository the credential does cover.
//
// The path is the escaped one, which is the text a URL really carries: a
// repository spelled with an escape (repourl.CloneURL's note about
// https://bitbucket.org/acme/sandb%6fx.git) folds to the bytes the shell sees
// only this way. An empty answer drops the repository from the covered set
// rather than adding a name nothing matches: a URL without a host is one this
// package refused a stage for already (httpsOrigin), and a covered set that lost
// a member refuses MORE submodules, which is the direction a guard may be wrong
// in.
func submoduleID(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return ""
	}
	id := task.ASCIILower(parsed.Host + parsed.EscapedPath())
	return strings.TrimSuffix(strings.TrimSuffix(id, "/"), ".git")
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

// Append is the composed definition: the entry's own bytes with one stage per
// repository p bakes on the end, and the bytes unchanged when p bakes nothing. A
// definition is appended to, never rewritten: the entry's own Dockerfile is
// what it is, and the stages at the end of it run in the image's last stage —
// the one every job starts from.
//
// The stages are composed in Baked's order, which puts the build's own last: the
// last stage is the one BakedSlug and the smoke read a definition by, so the
// checkout a definition is known by is the one the build baked as its own, and a
// rebuild that appends again lands its own stage where the reader looks (Stage,
// OtherStage).
//
// An entry whose own Dockerfile already clones (the hand-written definitions
// of D1208, e.g. images/catalog/*/Dockerfile) gets these stages as well, which
// pay a second clone for a repository the image already holds. Reading a
// Dockerfile to find out whether it clones is not a thing this package can do
// honestly, and the two checkouts are at different paths, so the entry's own
// bytes are left alone and the duplicate is a cost the person who wrote them
// can drop — the skill's generated definitions carry no clone of their own,
// which is what makes the engine's stages the only ones.
func Append(definition []byte, p Plan) ([]byte, error) {
	if len(p.Baked) == 0 {
		return definition, nil
	}
	covered := p.Baked
	var stages strings.Builder
	for i, r := range p.Baked {
		var (
			stage string
			err   error
		)
		// The build's own is the last, and the one stage that reads the build's
		// arguments: every other repository's URL and branch are spelled into the
		// definition, because the build's arguments name the build's repository.
		if i == len(p.Baked)-1 {
			stage, err = Stage(r, covered)
		} else {
			stage, err = OtherStage(r, covered)
		}
		if err != nil {
			return nil, err
		}
		stages.WriteString(stage)
	}
	body := stages.String()
	out := make([]byte, 0, len(definition)+1+len(body))
	out = append(out, definition...)
	if n := len(out); n > 0 && out[n-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, body...), nil
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
