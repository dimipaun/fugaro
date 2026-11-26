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
// entry RUN can read the credential (B8). That step is the only one of an
// entry's build that holds it: the stages this package composes into the entry's
// own definition copy the checkouts out of the image it tagged (BakedImage) and
// name no secret at all, and an entry that mounts the credential itself is
// refused by name (B9, D2311).
//
// What that leaves is F23: a job whose image carries several checkouts can read
// the others' source, which is the exposure F18 named. Its answer is not a
// refusal here but the runner's: it removes every baked directory but the run's
// own before the agent starts (D2309, task S9).
//
// # The generated stages
//
// One stage per repository the entry serves, and every one of them is a COPY:
// the checkout arrives from the image the clone step built (BakedImage), which
// holds each of them at its repository's slug, and the stage copies it to
// Path(slug) — a per-slug directory under Root, so an image can hold several
// repositories' checkouts without them colliding — and never to /work/repo: the
// work directory an old image baked at belongs to the repository that authored
// its Dockerfile, and a stage that wrote there would hand one repository's
// checkout to every job the image serves. A job finds its own checkout at its
// slug's path (IS8).
//
// Nothing the definition carries names a build argument or a secret: the marker
// line each stage opens with holds the repository's slug, its clone URL and its
// branch, and it is what the clone step reads the definition for (BakedRepos) —
// which is how a rebuild of an entry that bakes produces the same checkouts
// without the entry's own build seeing a credential. A definition an earlier
// release composed, whose stages cloned inside the entry's own build, carries
// markers with no URL: it is refused rather than built (internal/cli's
// catalogBakedRepos), because no build of it could be.
//
// What clones, and with what, is the clone step's own business
// (internal/backend/gcp's catalog_clone.go): the pinned base, the system git
// config off, the credential out of the container's own secret mount, and the
// revoke at the end. A repository a checkout declares as a submodule, and that
// the entry's repositories do not cover, fails that build naming the repository
// rather than being fetched.
//
// The marker line is what a reader holding nothing but the bytes finds the
// checkout by as well (BakedSlug, BakedDir), and the build's own repository is
// the last of the stages: the last stage is the one that runs, so the checkout a
// definition is known by is the one the build baked as its own (Append).
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
	// what a person reads and what the build's one credential names.
	Repo string
	// Key is the repository as the project's repository-to-image map
	// (`repo_images`) keys it: its provider and its canonical name together
	// (projectpolicy.Canonical, "github/acme/app"). A repository name carries no
	// provider of its own, so the key is the only spelling of one repository that
	// tells a GitHub acme/app from a Bitbucket one — and the map is read by it.
	// It is "" for a caller that records no provider, which matches no mapping.
	Key string
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
	// Map is the project's effective repository-to-image map (`repo_images`,
	// D1906), keyed by provider/owner/name (projectpolicy.Canonical): one key per
	// repository and provider, so a GitHub and a Bitbucket acme/app are two. It
	// is what says which repositories the entry being built serves, and it is the
	// only evidence a build has about any repository but the build's own — a
	// build reads one repository's config — so the bake hangs off it: see Decide.
	Map map[string]string
	// LegacyMap is the map in the old form the member had (image_map, D1906),
	// keyed by bare canonical repository name and matching every provider. It is
	// read only while Map is empty (R2), which is a node or a local config
	// `update` has not rewritten yet: repo_images wins over image_map, per member
	// and never per key. A caller fills exactly one of the two, from
	// localcfg.Config.RepoImagesEffective.
	LegacyMap map[string]string
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
	own, ownMapped := req.mappedEntry()
	switch {
	case req.Reference != "" && req.Reference != req.Entry:
		return Plan{Asked: asked, Reason: fmt.Sprintf("this repo runs %s", req.Reference)}
	case req.Reference == "" && (!ownMapped || own != req.Entry):
		return Plan{Asked: asked, Reason: fmt.Sprintf("the image map doesn't put %s on it", req.ownName())}
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
	// build's own checkout, and its jobs never pull this image. The fix names the
	// map's own key, and under repo_images that is the repository's provider key:
	// a bare name is a repository the project may run at either provider.
	if !ownMapped || own != req.Entry {
		return Plan{Notable: true, Asked: asked, Reason: fmt.Sprintf("the image map doesn't list %s", req.ownName()), Fix: fmt.Sprintf("To bake: fugaro image map set %s %s", req.ownName(), req.Entry)}
	}
	baked, refused := entryRepos(req)
	if refused != "" {
		return Plan{Notable: true, Asked: asked, Reason: refused}
	}
	return Plan{Baked: baked, Notable: true, Asked: asked}
}

// ownName is the build's own repository as the map in hand keys it: the
// provider key under repo_images, the bare name under the legacy map. The two
// keyspaces are told apart by which field is set and never by the shape of a
// key (R2), so a message that named the other arm's spelling would name a key
// the map does not hold and a fix that would not find it.
func (req Request) ownName() string {
	if len(req.Map) > 0 {
		return req.Repo.Key
	}
	return req.Repo.Repo
}

// mappedEntry is the entry the map puts on the build's own repository, and
// whether the map names it at all. repo_images wins over the legacy map, per
// member and never per key (R2): a caller fills exactly one of the two, so one
// that filled Map is answered by it whatever LegacyMap holds.
func (req Request) mappedEntry() (string, bool) {
	if len(req.Map) > 0 {
		name, ok := req.Map[req.Repo.Key]
		return name, ok && name != ""
	}
	name, ok := req.LegacyMap[req.Repo.Repo]
	return name, ok && name != ""
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
//
// Under repo_images a key carries a provider. A key a provider this build's
// enrolled set serves no repository at is skipped rather than refused (R9): a key
// a migration wrote for a provider the project turns out not to serve (F14,
// D3375) must not refuse the bake the build's own key asks for, and no job of the
// other provider reads this image. That is also the skip an old nested key gets
// (R10): "acme/team/app" reads as the provider "acme", which serves nothing. A key
// at a provider this build does serve is held to the project's repositories as a
// bare name was: a repository the project does not admit, and one that clones
// from another host, are both still refused by name.
func entryRepos(req Request) ([]Repo, string) {
	byName := make(map[string]Repo, len(req.Repos))
	byKey := make(map[string]Repo, len(req.Repos))
	serves := map[string]bool{}
	for _, r := range req.Repos {
		byName[r.Repo] = r
		if p, _, ok := strings.Cut(r.Key, "/"); ok {
			byKey[r.Key], serves[p] = r, true
		}
	}
	// The build's own repository is the one its enrolled set need not list, so
	// its provider is served by construction.
	if p, _, ok := strings.Cut(req.Repo.Key, "/"); ok {
		serves[p] = true
	}
	legacy := len(req.Map) == 0
	mapped := req.Map
	if legacy {
		mapped = req.LegacyMap
	}
	var names []string
	for name, entry := range mapped {
		if entry == req.Entry {
			names = append(names, name)
		}
	}
	own := req.ownName()
	// The build's own repository is the last stage, whichever order the map
	// hands the others in (Plan.Baked).
	slices.SortFunc(names, func(a, b string) int {
		if (a == own) != (b == own) {
			if a == own {
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
		if name == own {
			out = append(out, req.Repo)
			continue
		}
		// The map is a config value and a published node, so a key is held to the
		// shape of a repository name before it reaches a line a person reads or a
		// command they run — and under repo_images that shape is a provider and a
		// canonical name together, with the provider held to its shape alone
		// (R10), so a key a newer release wrote for a provider this one does not
		// know is skipped rather than refused.
		repo, prov := name, ""
		if legacy {
			if _, err := task.CanonicalRepo(repo); err != nil {
				return nil, fmt.Sprintf("the map names %q, which is not a repository", name)
			}
		} else {
			var ok bool
			if prov, repo, ok = strings.Cut(name, "/"); !ok || !task.ValidProvider(prov) {
				return nil, fmt.Sprintf("the map names %q, which is not a repository", name)
			}
		}
		r, admitted := byName[repo]
		if !legacy {
			r, admitted = byKey[name]
		}
		if !admitted {
			if !legacy && !serves[prov] {
				continue
			}
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
		if ownHost, host := urlHost(req.Repo.URL), urlHost(r.URL); ownHost == "" || host != ownHost {
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

// BakedImage is the name the clone step tags the image its generated stages copy
// out of: one directory per repository the entry serves, named by that
// repository's slug, and nothing else. internal/backend/gcp's catalog_clone.go
// is the other half of this name — it is the step that builds and tags it — and
// the copy is what keeps every stage of an entry free of a secret: the build
// that holds the git credential is the clone step's and no build of the entry's
// own (B8, F19).
const BakedImage = "fugaro-baked"

// Spellable refuses a repository a generated definition, and the script the
// clone step generates from it, may not spell: a slug, a clone URL and a branch,
// each of them one word of text that no shell and no Dockerfile reads as syntax
// of its own. A slug that is not one would name a path no reader opens, a URL
// with a credential in it would be copied into the project's bucket, and a URL or
// a branch holding a quote, a space or a newline would end the word it is written
// in — the clone step writes the same values into the shell it runs, where they
// are the words of a clone.
//
// The refusals are one line each: they reach an operator through a build log.
func (r Repo) Spellable() error {
	if _, err := Path(r.Slug); err != nil {
		return err
	}
	if err := httpsOrigin(r.URL); err != nil {
		return err
	}
	if !branchRE.MatchString(r.Branch) {
		return fmt.Errorf("the branch %q is not a branch name a generated definition may spell", r.Branch)
	}
	return nil
}

// branchRE is what a branch a generated definition may spell looks like: the
// characters git's own refname rules allow a branch, starting with one a shell
// word cannot read as an option. The clone step quotes it, so what this closes is
// not a quoting break (httpsOrigin) but a branch a `git clone --branch` would
// refuse at build time — an empty one, or one starting with "-".
var branchRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// copyStage is the one generated stage a baked repository gets: the marker line
// the clone step reads the repository out of, a comment, and the copy of that
// repository's checkout out of the image the clone step built.
//
// It carries no RUN at all, and that is the point of it: the entry's own build
// holds no git credential (B8, F19), so a stage of it cannot clone, and what
// would have been a clone is the copy of a checkout already made by the one
// build that may spend the credential. It also carries no USER line, so the
// stage cannot change whom a job's image runs as.
//
// own marks the repository the build runs for, which is the last of the stages
// (Append): its checkout is the one the build pinned to FUGARO_COMMIT, and the
// one a reader of the definition finds (BakedSlug).
func copyStage(r Repo, own bool) (string, error) {
	if err := r.Spellable(); err != nil {
		return "", err
	}
	dir, err := Path(r.Slug)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s %s %s\n", markerPrefix, dir, r.URL, r.Branch)
	b.WriteString("# Engine-generated by fugaro image build (the image setup skill's bake step): ")
	if own {
		b.WriteString("the checkout of\n# the repository this build runs for, at the commit the build recorded, for that repository's\n# own jobs.\n")
	} else {
		fmt.Fprintf(&b, "one of the\n# repositories this entry's image serves, at the head of %s when the build ran.\n", r.Branch)
	}
	fmt.Fprintf(&b, "# It was cloned by the clone step — the one build of this entry that holds a git credential\n"+
		"# (B8, F19) — and this stage copies it out of the image that build tagged. No stage of this\n"+
		"# definition names a secret.\n"+
		"COPY --from=%s --chown=1000:1000 /%s/ %s/\n", BakedImage, r.Slug, dir)
	return b.String(), nil
}

// markerSlug reads the directory a marker line names, and whether the line is one
// of this package's at all. The marker names Root/slug, and only a path under
// Root is one: a definition that spells anything else was not composed by this
// package and is not read as though it had been.
func markerSlug(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, markerPrefix)
	if !ok {
		return "", false
	}
	field, _, _ := strings.Cut(rest, " ")
	slug, ok := strings.CutPrefix(field, Root+"/")
	if !ok {
		return "", false
	}
	if _, err := Path(slug); err != nil {
		return "", false
	}
	return slug, true
}

// markerRepo reads a marker line as the repository it bakes: the slug, and the
// clone URL and branch the clone step clones it from.
//
// A marker that carries no URL is a line an earlier release of this package wrote
// — its stages cloned inside the entry's own build, with a credential that build
// is no longer handed — and no reader can clone what such a stage never named.
func markerRepo(line string) (Repo, bool) {
	slug, ok := markerSlug(line)
	if !ok {
		return Repo{}, false
	}
	rest, _ := strings.CutPrefix(line, markerPrefix)
	fields := strings.SplitN(rest, " ", 3)
	if len(fields) != 3 {
		return Repo{}, false
	}
	r := Repo{Slug: slug, URL: fields[1], Branch: fields[2]}
	if err := r.Spellable(); err != nil {
		return Repo{}, false
	}
	return r, true
}

// OriginID is a clone URL as the repository it names, spelled one way: the fold
// task.CanonicalRepo puts a repository name through — ASCII case down and a
// trailing ".git" off — applied to the whole URL, with a trailing "/" off first
// because a URL may carry one and a repository name may not.
//
// The case fold comes first, as it does in the clone step's own shell guard and
// in CanonicalRepo: ".GIT" is the same suffix as ".git" to whoever wrote the
// remote, and a fold that trimmed before lowercasing would leave "…/app.GIT" as
// "…/app.git" — a spelling the guard then refuses against every clone URL, its
// own included.
//
// It is the identity the clone step's guard compares and the spelling it writes
// into its script, so a definition composed from any of git's spellings of one
// repository is rebuilt by the checkout the nightly check assigns it. It is not a
// second parse of the URL: nothing here decides where the host ends, because two
// URLs git clones the same repository from are already the same string once this
// fold is applied, and a URL that is not one is refused before a stage is written
// (Repo.Spellable).
func OriginID(u string) string {
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
// definition is appended to, never rewritten: the entry's own Dockerfile is what
// it is, and the stages at the end of it run in the image's last stage — the one
// every job starts from.
//
// The stages are composed in Baked's order, which puts the build's own last: the
// last stage is the one BakedSlug and the smoke read a definition by, so the
// checkout a definition is known by is the one the build baked as its own, and a
// rebuild that appends again lands its own stage where the reader looks.
//
// An entry whose own Dockerfile already clones (the hand-written definitions of
// D1208, e.g. images/catalog/*/Dockerfile) gets these stages as well, which copy a
// checkout the image already holds. Reading a Dockerfile to find out whether it
// clones is not a thing this package can do honestly, and the two checkouts are at
// different paths, so the entry's own bytes are left alone and the duplicate is a
// cost the person who wrote them can drop — the skill's generated definitions
// carry no clone of their own, which is what makes the engine's stages the only
// ones.
func Append(definition []byte, p Plan) ([]byte, error) {
	if len(p.Baked) == 0 {
		return definition, nil
	}
	var stages strings.Builder
	for i, r := range p.Baked {
		stage, err := copyStage(r, i == len(p.Baked)-1)
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

// BakedSlug is the repository slug a composed definition bakes its checkout for,
// and whether it bakes one at all. A reader that has to know WHICH repository a
// definition bakes — the nightly rebuild, which runs as one repository and cannot
// reproduce another's definition — reads this; BakedDir is the path derived from
// it.
//
// It answers with the LAST marker: a definition that already bakes is composed
// again by appending, so it can carry several stages and the one that runs is the
// last. Reading the first would hand a reader the directory of a stage an earlier
// one replaced — and the repository of a build that is not the one the definition
// now bakes.
func BakedSlug(definition []byte) (string, bool) {
	var found string
	for line := range strings.Lines(string(definition)) {
		if slug, ok := markerSlug(strings.TrimSuffix(line, "\n")); ok {
			found = slug
		}
	}
	return found, found != ""
}

// BakedRepos are the repositories a composed definition bakes its checkouts of,
// in the order its stages run — the plan the clone step clones from
// (internal/backend/gcp's CatalogBuildSpec.Baked).
//
// It answers from the marker lines alone: a definition is the one object both the
// build that wrote it and a reader holding nothing but the bytes can agree on.
// A marker that carries no clone URL is skipped, because it is a stage an earlier
// release generated and no build can clone what it never named — the reader that
// needs one of those to be an answer is internal/cli's, which refuses such a copy
// by name.
func BakedRepos(definition []byte) []Repo {
	var out []Repo
	for line := range strings.Lines(string(definition)) {
		if r, ok := markerRepo(strings.TrimSuffix(line, "\n")); ok {
			out = append(out, r)
		}
	}
	return out
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
