package gcp

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/task"
)

// The generated clone step (B8): the one step of a catalog build that holds the
// git credential, and the fence F19 asks for
// (docs/shared-jobs-default/reconciliation.md, contract-build-token.md rules
// 3 to 7).
//
// It is a step of its own rather than a RUN of the entry's Dockerfile, which is
// where a catalog entry clones today, because a secret mounted anywhere in the
// entry's build is readable by every one of its `RUN` steps: BuildKit hands a
// build's secret to whatever asks for it by id, and the entry's Dockerfile is
// hand-written — internal/catalog/fetch.go tells its author to write that mount.
// The credential therefore reaches a build the entry has no part in: Fugaro
// writes this Dockerfile and this script, they are that build's whole context,
// it is built from the pinned base digest with GIT_CONFIG_NOSYSTEM=1, and what
// it exports is the checkouts, tagged as images the worker holds, which the
// entry's own build copies from in place of a secret (catalogEntryBuildStep).
//
// Every checkout a definition bakes comes from here and not from a stage of the
// entry's own build: the definition names the repositories and their clone URLs
// (imagebake's marker lines, read into CatalogBuildSpec.Baked), this step clones
// each of them, and the stages imagebake composed copy them out of the image it
// tagged (imagebake.BakedImage) — so no `RUN` of the entry's build is a clone,
// and none of them can read a credential (B8, F19, D2311).
//
// The step alone decides what the credential may be spent on, and it decides it
// before each fetch rather than trusting the provider: the store file it is
// given is per host, every URL the repository itself declares — a submodule's, an
// LFS endpoint's — is read and refused when it names another one, a repository
// of an entry that bakes is refused when the entry's own repositories do not
// cover it, and protocol.file and protocol.ext are set to never, so neither a
// path nor a program is a transport at all. Then the credential's own end:
// GitHub revokes the token at its own endpoint, and a Bitbucket build holds no
// token to revoke (rule 7, R6).
const (
	// catalogCloneStepID is the clone step's id in a catalog build's step list,
	// where the checkout an entry with a build context used to get one.
	catalogCloneStepID = "catalog-clone"
	// catalogCloneWork is the directory the step writes its build's context
	// into: the Dockerfile and the clone script Fugaro generates, and nothing
	// else, so the build that holds the credential reads no file an entry
	// wrote.
	catalogCloneWork = "/workspace/clone"
	// catalogCloneDockerfilePath is the generated Dockerfile in that directory.
	catalogCloneDockerfilePath = catalogCloneWork + "/Dockerfile"
	// catalogCloneScriptPath is the generated clone script in it.
	catalogCloneScriptPath = catalogCloneWork + "/clone.sh"
	// catalogCloneName is the script's name where the generated build copies it
	// to, inside the clone stage the entry never shares.
	catalogCloneName = "/usr/local/lib/fugaro-clone.sh"
	// catalogCloneDest is where the step puts the checkout on disk for an entry
	// that names a build context: catalogContextRoot, the directory the derived
	// build's own source step clones into, which a context entry's build step
	// already builds from. A build context is a directory docker tars, so the
	// checkout is exported there by the build that made it (catalogCloneStepScript)
	// and the entry that names no context is spared it.
	//
	// Exported and not unpacked out of the image below: the image's root is the
	// checkout, but a container made from it has a rootfs of its own — the
	// daemon's /.dockerenv, /etc/hosts, /etc/hostname, /etc/resolv.conf, /dev,
	// /proc and /sys — so a `docker create` and a `docker cp` of that container
	// would write files no repository has into the context, and a `COPY .` of
	// the entry's Dockerfile would bake them into the image as untracked files
	// an agent's `git add -A` then commits. The export writes the tree and
	// nothing else.
	catalogCloneDest = catalogContextRoot
	// catalogCloneImage is the name the checkout reaches the entry's build
	// under: the step tags what its build exports with it, and an entry's
	// Dockerfile copies the checkout with `COPY --from=fugaro-checkout` instead
	// of cloning with a credential of its own.
	//
	// An image and not a named build context: `--build-context` is a buildx
	// flag, and the step that builds an entry is the template's plain
	// `docker build` on a worker that ships the docker CLI and no buildx
	// (docs/base-image/spec.md) — a request that wrote one fails the whole
	// build at its first flag, which is what an update of the `go` image found
	// out. Docker resolves the name in `COPY --from=<name>` out of the local
	// image store before the registry — its default resolve mode is "prefer
	// local" — and neither build passes --pull, so what an entry copies here is
	// what this step just built.
	//
	// The name is namespaced rather than a plain `src` for the reason a named
	// context was: docker resolves an image or a stage by that one name wherever
	// a Dockerfile writes `FROM <name>` or `COPY --from=<name>`, so a short,
	// generic name is one an entry's own stage may already have, and what the
	// entry meant by it would then be read from somewhere else.
	catalogCloneImage = "fugaro-checkout"
	// catalogCloneCredential is the git credential-store line the credential
	// step wrote into the fugaro-creds volume: the clone step's own build is
	// handed it as a file secret and this step removes it when it is done.
	catalogCloneCredential = "/creds/git-credentials"
)

// catalogCloneDockerfileText is the Dockerfile the clone step builds, up to the
// scratch stage that carries its tree out — the stages after it are the
// checkout and, for a definition that bakes, the image its stages copy
// (catalogCloneDockerfile).
//
// It runs from the pinned base digest the step hands it and sets
// GIT_CONFIG_NOSYSTEM=1, so the git that spends the credential is the base
// image's own git, reading no system config of the worker's — which is also
// what keeps the base image's system-wide git-lfs filter out of the clone, so
// the checkout writes pointer files and the objects are fetched by the script
// itself, after the `.lfsconfig` that could point them at another host has been
// read (rule 6).
//
// The export is the checkouts and nothing else: the clone writes under /out, the
// stage the credential ran in leaves this build no other way than through the
// scratch stages below, and what those carry out is the images the step tags for
// the entry's build to copy — and, for an entry that names a build context, the
// same tree exported as the directory docker is handed.
//
// The frontend is pinned as every Dockerfile Fugaro writes pins it
// (images/derived/Dockerfile.tmpl and the four beside it), so the clone is
// built by the same frontend the entries are.
const catalogCloneDockerfileText = `# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
ARG FUGARO_BASE
FROM ${FUGARO_BASE} AS clone
ENV GIT_CONFIG_NOSYSTEM=1
# The values the clone needs cross into this build as build arguments, and this
# file never writes one of them into a command's text: the clone script reads
# them from its environment, so a URL or a name that holds a quote, a space or a
# newline is data and never shell or Dockerfile syntax.
ARG REPO_URL=""
ARG COMMIT=""
ARG REPO_HOST=""
ARG GITHUB_API=""
USER root
RUN install -d -m 0755 -o 1000 -g 1000 /out
USER fugaro
COPY clone.sh ` + catalogCloneName + `
RUN --mount=type=secret,id=git-credentials,uid=1000 sh ` + catalogCloneName + `
FROM scratch AS checkout
`

// catalogCloneDockerfile is that Dockerfile with the stages after it, which are
// what each kind of definition is handed:
//
//   - `checkout` is the repository the build runs for, at the root of an image
//     whose name every entry's Dockerfile has always copied it by
//     (catalogCloneImage). A definition that bakes nothing clones that one
//     repository into /out, and its own stages are the entry's own — a
//     hand-written Dockerfile that copies this image, or clones itself with a
//     credential of its own (D1208).
//   - `baked` is every repository the definition that bakes names, one directory
//     per repository slug (imagebake.Root's layout is the stages' business), and
//     the entry's build copies each out of it with the stage imagebake composed
//     (imagebake.BakedImage). It is built only when there is one: an entry that
//     bakes names no repository and no stage copies from it.
//
// The clone's own work, which the two share, runs once: docker builds the stage
// up to each of them out of the same cache.
func catalogCloneDockerfile(s CatalogBuildSpec) string {
	var b strings.Builder
	b.WriteString(catalogCloneDockerfileText)
	if len(s.Baked) == 0 {
		b.WriteString("COPY --from=clone --chown=1000:1000 /out/ /\n")
		return b.String()
	}
	fmt.Fprintf(&b, "COPY --from=clone --chown=1000:1000 /out/%s/ /\n", catalogBakedOwn(s).Slug)
	b.WriteString("FROM scratch AS baked\n")
	b.WriteString("COPY --from=clone --chown=1000:1000 /out/ /\n")
	return b.String()
}

// catalogBakedOwn is the repository a definition that bakes names last, and the
// one the build runs for: imagebake composes the build's own stage last, so the
// checkout the definition is known by is the one that repository's jobs open
// (imagebake.BakedSlug, imagebake.Append). It is what the generated Dockerfile
// exports as the checkout image and what the script holds REPO_URL to.
func catalogBakedOwn(s CatalogBuildSpec) imagebake.Repo {
	return s.Baked[len(s.Baked)-1]
}

// catalogCloneScriptHelpers is the part of the generated script every catalog
// build runs, up to the clone: every git it runs goes through gitx, so the
// credential file, the two transports that are never allowed and the prompt that
// is never waited for are stated once — a fetch that git makes for itself, a
// submodule or an LFS object, inherits them because it is the same process — and
// the values reach it through the environment (see the Dockerfile), each quoted
// where it is used: `--` before the URL of a clone or a fetch, `"$VAR"`
// everywhere, and no `eval`.
//
// What a repository names about itself — a submodule's URL, an LFS endpoint — is
// read from a file the clone just made and refused when it does not belong to
// this host, before the fetch that would use it. A definition that bakes gets a
// second refusal over the same URLs: the entry's own repositories are what the
// build's credential was minted for (COVERED, the plan's own repositories), and a
// declaration that resolves to a repository outside them is refused where it is
// read (imagebake's B2, the guard the composed stages used to carry).
const catalogCloneScriptHelpers = `
gitx() {
  GIT_TERMINAL_PROMPT=0 git \
    -c credential.helper= \
    -c "credential.helper=store --file=$CRED" \
    -c protocol.file.allow=never \
    -c protocol.ext.allow=never \
    "$@"
}

# foreign_url is true when a URL a repository declares may not be fetched with
# this build's credential. Only an https URL on the entry's own host is one it
# may be spent on, and only a path relative to the entry's own repository — the
# shape almost every submodule uses, which git combines with that repository's
# own remote URL, so the fetch stays on the same host. Everything else is
# refused before the fetch: another host, another scheme (http, ssh, ext, file),
# a value git would read as an option, an absolute path, an empty value, and the
# scp-like git@host:path syntax, which is a host of its own with no scheme to
# see. The store file holds one host's credential, so a fetch of any of these
# would be answered with nothing, and a URL may carry a credential of its own.
foreign_url() {
  case "$1" in
    -*|"") return 0 ;;
    https://*)
      host=${1#https://}
      host=${host%%/*}
      case "$host" in "$REPO_HOST") return 1 ;; esac
      return 0 ;;
    ./*|../*) return 1 ;;
    *:*|/*) return 0 ;;
  esac
  return 1
}

# The coverage refusal names the repository the URL resolves to, folded the way
# the covered set is: the host, the owner and the name, lower-cased, with a
# trailing "/" and ".git" off. Two things follow from that fold and both are the
# point of it. A URL that carries a credential cannot put it in the build log,
# because everything from a "?" or a "#" is off the text a refusal prints, and so
# is a userinfo — the text before the URL's first "/" is its host, and only there
# does the fold drop everything up to the last "@", so a token in a userinfo, a
# query or a fragment is off a build log line (the fold also drops control
# characters, so nothing in that URL can rewrite the log it appears in). And a
# repository the credential does not cover is not one this build fetches,
# whatever it is called.
#
# The coverage refusal comes first, so a declaration that resolves to a repository
# outside the entry's own is refused naming it and not merely as a URL of another
# host (B2, spec.md's "fail with a clear message naming the repo that isn't
# covered"). The host refusal is left for what the fold cannot name: a transport
# that is not http, a value git would read as an option, an empty one, and a host
# whose spelling is not the entry's own.
#
# Both are skipped for a definition that bakes nothing (COVERED empty), where
# nothing of the engine's is cloned and the host refusal alone is the guard.
check_urls() {
  # git answers 1 for a file with no such key and 128 for one it cannot read, and
  # the second is a refusal: a checkout whose declarations git never read is not
  # one this guard can show is covered.
  status=0
  urls=$(git config --file "$1" --get-regexp '^submodule\..*\.url$') || status=$?
  [ "$status" = 0 ] || [ "$status" = 1 ] || exit 1
  printf '%s\n' "$urls" | while read -r key url; do
    [ -n "$key" ] || continue
    if [ -n "$COVERED" ]; then
      # The loop splits on whitespace and must not glob: a declaration holding "*",
      # "?" or "[" would otherwise be expanded against the build's filesystem and
      # read as whatever it matched rather than as the URL it is. The value is
      # quoted inside the case pattern below for the same reason.
      repo=$(printf '%s' "$url" | tr 'A-Z' 'a-z' | tr -d '\000-\037\177')
      repo=${repo#*://}
      repo=${repo%%[?]*}
      repo=${repo%%[#]*}
      auth=${repo%%/*}
      rest=${repo#"$auth"}
      repo=${auth##*@}$rest
      repo=${repo%/}
      repo=${repo%%.git}
      [ -n "$repo" ] || repo="a repository this build cannot read"
      case " $COVERED " in *" $repo "*) ;; *)
        echo "a submodule of this checkout is $repo, which this build's credential does not cover: it covers the repositories of the entry being built and no others" >&2
        exit 1 ;;
      esac
    fi
    if foreign_url "$url"; then
      echo "the submodule $(printf '%s' "$key" | tr -d '[:cntrl:]') names a URL that is not $REPO_HOST: this build's credential is offered to $REPO_HOST alone" >&2
      exit 1
    fi
  done
}

# check_lfs_url refuses the LFS endpoints a checkout declares that are not this
# host's. Both shapes git-lfs reads out of an .lfsconfig are read here — lfs.url
# and remote.<name>.lfsurl — because an endpoint named under the second would
# otherwise be the one the fetch talks to while this check saw nothing.
#
# A file that includes another is refused rather than followed: git-lfs follows
# include.path, so the endpoint it would use is read from a file this check has
# not seen, and no .lfsconfig needs one — what a repository may name here is its
# own LFS endpoint.
check_lfs_url() {
  if git config --file "$1" --get-regexp '^(include|includeif)\.' >/dev/null; then
    echo "the .lfsconfig includes another file, so the endpoint it names is not one this build read: this build's credential is offered to $REPO_HOST alone" >&2
    return 1
  fi
  git config --file "$1" --get-regexp '^(lfs\.url|remote\..*\.lfsurl)$' | while read -r key url; do
    [ -n "$url" ] || continue
    foreign_url "$url" || continue
    echo "the .lfsconfig names an LFS URL that is not $REPO_HOST: this build's credential is offered to $REPO_HOST alone" >&2
    exit 1
  done
}

# fetch_tree fetches what a repository holds beside its own files: its LFS
# objects, then its submodules, and then the same for each of those. A level's
# URLs are read and refused before the level under it is fetched, so a nested
# .gitmodules naming another host — or, for an entry that bakes, a repository
# outside the entry's own — is refused before any credential is offered to it
# (rule 6).
fetch_tree() {
  if [ -f "$1/.lfsconfig" ]; then
    check_lfs_url "$1/.lfsconfig"
  fi
  gitx -C "$1" lfs pull
  [ -f "$1/.gitmodules" ] || return 0
  check_urls "$1/.gitmodules"
  # git's own flags are written quiet-first: this tree's census of the retired
  # verb reads a flag list for its spelling, and update's initialising flag in
  # front of another is that spelling (internal/cli/docs_live_init_census_test.go).
  gitx -C "$1" submodule update --quiet --init
  gitx -C "$1" submodule foreach --quiet 'printf "%s\n" "$sm_path"' | while IFS= read -r line; do
    fetch_tree "$1/$line"
  done
}

case "$REPO_URL" in
  -*) echo "REPO_URL begins with a dash" >&2; exit 1 ;;
  https://*) ;;
  *) echo "REPO_URL must be an https URL" >&2; exit 1 ;;
esac
case "$COMMIT" in
  ""|*[!0-9a-f]*) echo "COMMIT is not a commit ID" >&2; exit 1 ;;
esac
`

// catalogCloneScriptOwn is the clone of the repository the build runs for, for a
// definition that bakes nothing: it is the only repository the step clones, and
// it lands at /out — the root of the image an entry's Dockerfile copies
// (catalogCloneImage).
//
// The commit is the one the definition pinned and never the branch head: the
// branch head at build time is the tree the definition was copied from only by
// accident (D1394). The clone is shallow, so a commit that is not the tip is
// fetched by itself, and one the remote no longer has fails the build saying so
// rather than baking another tree — the shape the derived template's build uses.
const catalogCloneScriptOwn = `
gitx clone --quiet --depth 1 --no-checkout -- "$REPO_URL" /out
gitx -C /out cat-file -e "$COMMIT^{commit}" 2>/dev/null \
  || gitx -C /out fetch --quiet origin "$COMMIT" \
  || { echo "commit $COMMIT is no longer reachable: the branch was force-pushed; re-run the build" >&2; exit 1; }
gitx -C /out reset --quiet --hard "$COMMIT"

fetch_tree /out
`

// catalogCloneScript is the script the generated build runs, and the only thing
// in that build that reads the credential: the secret mount is its standard
// input's neighbour and never an argument, an environment value, a line of
// output or an error.
//
// What it clones is what the definition bakes: the repository the build runs for
// at the commit the build pinned, and — for a definition that bakes several —
// each of the entry's other repositories at the head of the branch the definition
// names for it. The step holds one commit and that is the build's own: the others
// are the branch's head at the moment the build runs, which is what an entry's
// own Dockerfile that clones by hand has always done, and the reason a repository
// that wants its image pinned pins it by naming the entry its own workflow runs.
//
// A definition that bakes is held to the checkout it was composed for before
// anything is cloned: the nightly check rebuilds an entry as whichever repository
// the spec assigns it to (infra's catalogCheckRepos), and without the guard such
// a rebuild would clone one repository into another's slug path, under the marker
// and the tag the composed definition gave it, and the smoke would pass on the
// pairing it read from the two. The refusal is about the repository and not the
// spelling — the composed origin, the origin's capricious marks and the canonical
// form the catalog build passes are one fold (imagebake.OriginID) — and every
// repository the definition names is refused with it.
//
// catalogCloneScriptText is the clone step's script. Its fetch-by-id failure
// line — "the branch was force-pushed; re-run the build" — stands for a commit
// that was never pushed as well, and the script cannot tell the two apart: it
// holds no branch name, and the shallow clone's own refs are the only ones it
// may read, so both are the same fact here, that this remote does not publish
// the commit. A commit that was never pushed is refused in the checkout, before
// anything is submitted (internal/cli's refuseUnpushedCommit), so what reaches
// this line through the CLI is a real force-push — and a template driven by
// hand, which has no checkout to judge.
func catalogCloneScript(s CatalogBuildSpec) (string, error) {
	for _, r := range s.Baked {
		if err := r.Spellable(); err != nil {
			return "", fmt.Errorf("the definition bakes the checkout %s: %w", r.Slug, err)
		}
	}
	var b strings.Builder
	b.WriteString("set -eu\nexport LC_ALL=C\nCRED=/run/secrets/git-credentials\n")
	covered := make([]string, 0, len(s.Baked))
	for _, r := range s.Baked {
		if id := coveredID(r.URL); id != "" {
			covered = append(covered, id)
		}
	}
	fmt.Fprintf(&b, "COVERED='%s'\n", strings.Join(covered, " "))
	b.WriteString(catalogCloneScriptHelpers)
	if len(s.Baked) == 0 {
		b.WriteString(catalogCloneScriptOwn)
	} else {
		b.WriteString(catalogCloneScriptBaked(s.Baked))
	}
	if s.GitProvider == gitprov.KindGitHub {
		b.WriteString(catalogCloneRevoke)
	}
	return b.String(), nil
}

// catalogCloneScriptBaked is the script above for a definition that bakes: the
// build's own repository into its slug's directory, then each of the entry's
// other repositories into its own, and then what a repository holds beside its
// files — its LFS objects and its submodules — for every one of them.
//
// The definition's own marker line is what the two are read from
// (imagebake.BakedRepos), and the build's own is the last of them, which is the
// one the guard holds REPO_URL to. The branch the others are cloned at is the
// one their marker names: the definition was composed from the project's own
// repository records, and a branch that is not a branch name imagebake refuses
// before this script is written.
func catalogCloneScriptBaked(baked []imagebake.Repo) string {
	own := baked[len(baked)-1]
	var b strings.Builder
	fmt.Fprintf(&b, `
# The repository the build runs for, held to the definition: a rebuild of an entry
# that bakes has to run as the repository whose checkout the definition bakes.
own=$(printf '%%s' "$REPO_URL" | tr 'A-Z' 'a-z')
own=${own%%/}
own=${own%%.git}
[ "$own" = '%s' ] || { echo "this definition bakes one repository's checkout, and REPO_URL is not it: a rebuild of the entry has to run as the repository whose checkout it bakes" >&2; exit 1; }
gitx clone --quiet --depth 1 --no-checkout -- "$REPO_URL" /out/%s
gitx -C /out/%s cat-file -e "$COMMIT^{commit}" 2>/dev/null \
  || gitx -C /out/%s fetch --quiet origin "$COMMIT" \
  || { echo "commit $COMMIT is no longer reachable: the branch was force-pushed; re-run the build" >&2; exit 1; }
gitx -C /out/%s reset --quiet --hard "$COMMIT"

fetch_tree /out/%s
`, imagebake.OriginID(own.URL), own.Slug, own.Slug, own.Slug, own.Slug, own.Slug)
	for _, r := range baked[:len(baked)-1] {
		fmt.Fprintf(&b, `
# One of the entry's other repositories, at the head of the branch the definition
# names for it: the build holds the commit of its own repository and no other.
gitx clone --quiet --branch '%s' --single-branch -- '%s' /out/%s

fetch_tree /out/%s
`, r.Branch, r.URL, r.Slug, r.Slug)
	}
	return b.String()
}

// coveredID is the repository a clone URL names, as the coverage check's own
// shell spells it: the host and the path, lower-cased, with a trailing "/" and
// ".git" off. Go computes the covered set here and the shell folds the
// submodule's own URL, so the two spellings have to be one — a set the shell
// could not match would refuse every submodule of a repository the entry
// covers.
//
// The path is the escaped one, which is the text a URL really carries: a
// repository spelled with an escape (repourl.CloneURL's note about
// https://bitbucket.org/acme/sandb%6fx.git) folds to the bytes the shell sees
// only this way. An empty answer drops the repository from the covered set
// rather than adding a name nothing matches: a URL without a host is not one
// imagebake composes a stage for (Repo.Spellable), and a covered set that lost a
// member refuses MORE submodules, which is the direction a guard may be wrong
// in.
func coveredID(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return ""
	}
	id := task.ASCIILower(parsed.Host + parsed.EscapedPath())
	return strings.TrimSuffix(strings.TrimSuffix(id, "/"), ".git")
}

// catalogCloneRevoke is what the step that reads the credential ends with where
// the provider can revoke: the credential ends with the clone step (rule 7), for
// every definition, because no step of the entry's own build is handed it.
//
// GitHub's endpoint revokes the token that carries it and needs no other
// identity, so the token itself is the call: it is read back out of the
// credential file with git's own reader — which returns it decoded, so no
// percent-decoding is written here — and it goes into a 0600 curl config, the
// shape scripts/live-checks/check190.sh uses for the same reason: the value is
// never an argument, an environment value or a line of output, and curl reads it
// from a file and prints nothing of it. A revoke that fails does not fail the
// build: the token expires inside the build's timeout either way, which is where
// a Bitbucket build starts — it is answered a proxy grant and no token at all
// (R6), so this is not appended for one.
//
// The token it cannot read back is said and not passed over: an empty read is a
// credential file this step cannot see (a git that is not on the worker, a file
// a step removed) and a build whose token is then left to expire, which is a
// thing an operator reading the log has to be able to see.
const catalogCloneRevoke = `
tok=$(printf 'protocol=https\nhost=%s\n\n' "$REPO_HOST" | git credential-store --file="$CRED" get | sed -n 's/^password=//p') || tok=
[ -n "$tok" ] || { echo "the installation token could not be read back for revocation; it expires inside this build's timeout" >&2; exit 0; }
conf=$(mktemp)
(umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" > "$conf")
curl --silent --show-error --fail --request DELETE \
  --header "Accept: application/vnd.github+json" \
  --header "X-Github-Api-Version: 2022-11-28" \
  --header "User-Agent: fugaro" \
  --config "$conf" "$GITHUB_API/installation/token" >/dev/null \
  || echo "the installation token could not be revoked; it expires inside this build's timeout" >&2
rm -f "$conf"
`

// catalogTemplateCredentialArg is the git credential the derived template's
// build step hands the Dockerfile it builds. A catalog build takes it out of
// every entry's build step: no stage of a catalog entry clones any more, so
// where the credential would reach the entry's build there is nothing to read it
// (B8, F19, D2311) — the checkouts arrive as the images the clone step built.
const catalogTemplateCredentialArg = `--secret "id=git-credentials,src=` + catalogCloneCredential + `"`

// catalogTemplateCredentialLine is that argument as the one line of the build
// step's script a catalog build takes out: the argument and the backslash that
// joins it to the line after it. The checkout reaches the entry's build as the
// image catalogCloneImage instead, which no flag carries — the name is in the
// worker's image store by the time that build runs — so the line goes and
// nothing stands in its place.
const catalogTemplateCredentialLine = catalogTemplateCredentialArg + " \\"

// errTemplateCredentialMoved is what a catalog request answers when the embedded
// template no longer hands the build step the credential in that one argument.
// It is not a caller's error: it says this binary's template and this code
// disagree, and the build must not run until they agree, because the alternative
// is building with the credential in the entry's own build, which is the hole
// this step exists to close.
var errTemplateCredentialMoved = errors.New("the embedded build template no longer hands the git credential to the build step: this binary's catalog build must be read again")

// catalogCloneStep is the step that clones the repositories the entry's build
// copies: the build request carries it, and neither the entry's Dockerfile nor
// the copied definition is part of the build it runs.
//
// The base is pinned the way the template's build step pins it — the digest of
// the base image this worker already holds, which the credential step's own
// Cloud Build pull put there — so the tools that spend the credential cannot be
// swapped between the steps, and the build step pins the same digest again for
// the image it builds.
//
// The entry's context, where it names one, is checked here rather than in the
// shape of the request alone: the context reaches docker as the directory its
// path resolves to — the CLI tars what a symlinked root points at, not the link
// — so a repository at the pinned commit whose context is a link to a directory
// outside the checkout would hand the build step a tree this step did not clone.
// The guard resolves it from buildCwd, the directory of the build step that
// hands docker the value, because that is where docker resolves it
// (catalogCloneGuard).
func catalogCloneStep(s CatalogBuildSpec, buildCwd string) (*cloudbuild.BuildStep, error) {
	u, err := url.Parse(s.RepoURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the repository URL %s does not parse", RedactURL(s.RepoURL))
	}
	script, err := catalogCloneScript(s)
	if err != nil {
		return nil, err
	}
	env := []string{
		"DOCKER_BUILDKIT=1",
		"FUGARO_BASE=${_FUGARO_BASE}",
		"REPO_URL=${_REPO_URL}",
		"COMMIT=" + s.cloneCommit(),
		"REPO_HOST=" + u.Host,
		"GITHUB_API=" + github.DefaultBaseURL,
	}
	if s.Context != "" {
		env = append(env, "CONTEXT_DIR=${_CONTEXT_DIR}", "BUILD_CWD="+buildCwd)
	}
	return &cloudbuild.BuildStep{
		Id:         catalogCloneStepID,
		Name:       "gcr.io/cloud-builders/docker",
		Entrypoint: "bash",
		Env:        env,
		Args:       []string{"-c", cloudBuildEscape(catalogCloneStepScript(s, script))},
		Volumes:    []*cloudbuild.Volume{{Name: "fugaro-creds", Path: "/creds"}},
	}, nil
}

// cloneCommit is the commit the clone step checks out: the context's commit,
// where the entry names a context — the commit its tree and the copied
// Dockerfile were both read at (D1395) — and otherwise the commit the copied
// definition was taken from, which the build step hands the entry's Dockerfile
// as FUGARO_COMMIT and the record writes as the source commit.
func (s CatalogBuildSpec) cloneCommit() string {
	if s.Context != "" {
		return s.ContextCommit
	}
	return s.Definition.SourceCommit
}

// catalogCloneStepScript is the step's shell, with the script the generated
// build runs written into its build context: the step composes the two files of
// that build and nothing else, so what the credential-holding build reads is
// what this function wrote (F19: a Dockerfile Fugaro generates entirely).
//
// The credential file is removed however this step ends, the build failing and
// the guard refusing included — an exit trap, the shape the template's own build
// step uses for the same file. This step is the only one that mounts the volume
// it lives in: the entry's build step is handed neither the file nor the volume
// (catalogEntryBuildStep), so nothing outlives it.
//
// The generated Dockerfile is built once per image the entry's build copies out
// of, and the builds differ in their last flags alone: once with the checkout
// stage tagged catalogCloneImage, which is the image every entry's Dockerfile
// copies, once more with the baked stage tagged imagebake.BakedImage, which the
// stages of a definition that bakes copy each checkout out of (Baked), and —
// for an entry that names a build context — once more with the `type=local`
// export catalogCloneDest is written by. Each build is the one before it cache,
// the same Dockerfile at the same arguments, so the clone itself runs once, and
// what each writes is the tree its own scratch stage carries out and nothing
// else.
func catalogCloneStepScript(s CatalogBuildSpec, script string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "set -eu\nexport LC_ALL=C\ninstall -d -m 0700 %s\ntrap 'rm -f %s' EXIT\n", catalogCloneWork, catalogCloneCredential)
	fmt.Fprintf(&b, "cat > %s <<'FUGARO_DOCKERFILE'\n%sFUGARO_DOCKERFILE\n", catalogCloneDockerfilePath, catalogCloneDockerfile(s))
	fmt.Fprintf(&b, "cat > %s <<'FUGARO_SCRIPT'\n%sFUGARO_SCRIPT\n", catalogCloneScriptPath, script)
	b.WriteString(`base=$(docker image inspect --format '{{index .RepoDigests 0}}' "$FUGARO_BASE") \
  || { echo "the base image $FUGARO_BASE is not on this worker" >&2; exit 1; }
docker build ` + catalogCloneBuildFlags + `  --target checkout --tag ` + catalogCloneImage + ` \
  ` + catalogCloneWork + "\n")
	if len(s.Baked) > 0 {
		b.WriteString("docker build " + catalogCloneBuildFlags + "  --target baked --tag " + imagebake.BakedImage + ` \
  ` + catalogCloneWork + "\n")
	}
	if s.Context != "" {
		b.WriteString("docker build " + catalogCloneBuildFlags +
			`  --target checkout --output "type=local,dest=` + catalogCloneDest + `" \
  ` + catalogCloneWork + "\n")
		b.WriteString(catalogCloneGuard)
	}
	return b.String()
}

// catalogCloneBuildFlags is the flag list every build of the generated
// Dockerfile takes, ending in the continuation that joins the line after it: the
// same build each time, with the target and the output the only difference
// between them.
const catalogCloneBuildFlags = `--progress plain \
  --file ` + catalogCloneDockerfilePath + ` \
  --build-arg "FUGARO_BASE=$base" \
  --build-arg "REPO_URL=$REPO_URL" \
  --build-arg "COMMIT=$COMMIT" \
  --build-arg "REPO_HOST=$REPO_HOST" \
  --build-arg "GITHUB_API=$GITHUB_API" \
  --secret "id=git-credentials,src=` + catalogCloneCredential + `" \
`

// catalogCloneGuard is the check the clone step ends with for an entry that
// names a build context: the directory the build step will hand docker as its
// context has to be one this step just wrote. The request refuses the text of a
// context that climbs out of the checkout (contextPathMessage); a link is
// visible only in the tree, so this is the one place it can be seen, and it is
// checked on the directory docker is handed rather than walked through the tree.
const catalogCloneGuard = `root=$(cd ` + catalogCloneDest + ` && pwd -P)
cd -- "$BUILD_CWD"
context=$(cd -- "$CONTEXT_DIR" 2>/dev/null && pwd -P) \
  || { echo "the context $CONTEXT_DIR is not a directory of the checkout" >&2; exit 1; }
case "$context" in
  "$root"|"$root"/*) ;;
  *) echo "the context $CONTEXT_DIR resolves to $context, outside the checkout: the build step would read a tree this step did not clone" >&2; exit 1 ;;
esac
`

// catalogEntryBuildStep makes the entry's own build step the one a catalog build
// runs: the checkouts the clone step tagged reach it as images the worker holds
// by the time this step runs, and no flag of this step names them. The git
// credential the derived template hands its build step is taken out of the
// request — with the volume that holds it, which no step after the clone mounts —
// for every definition, the ones that bake included (B8, F19): a definition that
// bakes copies the checkouts the clone step made (Baked) and mounts no secret of
// its own, so there is nothing in the entry's build to hand the credential to.
//
// Only the credential is taken out here, and no other secret is left to take
// out: the repository's workflow secrets, declared in its fugaro.yaml, reached a
// catalog build until B9 answered D2311, and the request now states an empty
// _SECRET_ENVS — the build step's loop over it mounts nothing, and the catalog
// refuses an entry whose own bytes ask for a secret when it reads them. A
// dependency behind a private registry is served by an `--image` entry instead,
// which the catalog runs and never builds.
func catalogEntryBuildStep(build *cloudbuild.BuildStep) error {
	for i, arg := range build.Args {
		if !strings.Contains(arg, catalogTemplateCredentialArg) {
			continue
		}
		script, err := dropCredentialLine(arg)
		if err != nil {
			return err
		}
		build.Args[i] = script
		build.Volumes = nil
		return nil
	}
	return errTemplateCredentialMoved
}

// dropCredentialLine takes the credential argument out of a build step's script
// with the whole line that holds it, continuation included: the line the
// template writes is the argument alone, so what is left of the docker build is
// every other argument it had and the same line joins them. A template that
// wrote the credential beside another argument is a template this code no longer
// knows, and the build is refused rather than left with a command whose flags
// this function guessed at.
func dropCredentialLine(script string) (string, error) {
	lines := strings.Split(script, "\n")
	found := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != catalogTemplateCredentialLine {
			continue
		}
		if found >= 0 {
			return "", errTemplateCredentialMoved
		}
		found = i
	}
	if found < 0 {
		return "", errTemplateCredentialMoved
	}
	return strings.Join(append(lines[:found], lines[found+1:]...), "\n"), nil
}

// cloudBuildEscape doubles every dollar of a script the request carries: Cloud
// Build substitutes a build config textually, so a shell variable has to reach
// the step as $$VAR and a dollar a shell writes itself as $$$$, which is the
// rule images/derived/cloudbuild.yaml states for its own steps. The generated
// files are written by a quoted heredoc, so what the step's shell puts in them
// is what this package wrote and not a substitution of the request's.
func cloudBuildEscape(s string) string { return strings.ReplaceAll(s, "$", "$$") }
