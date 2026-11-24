package gcp

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
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
// it exports is the checkout, tagged as an image the worker holds, which the
// entry's own build copies from in place of a secret (catalogEntryBuildStep).
//
// The step alone decides what the credential may be spent on, and it decides it
// before each fetch rather than trusting the provider: the store file it is
// given is per host, every URL the repository itself declares — a submodule's, an
// LFS endpoint's — is read and refused when it names another one, and
// protocol.file and protocol.ext are set to never, so neither a path nor a
// program is a transport at all. Then the credential's own end: GitHub revokes
// the token at its own endpoint, and a Bitbucket build holds no token to revoke
// (rule 7, R6).
const (
	// catalogCloneStepID is the clone step's id in a catalog build's step list,
	// where the checkout an entry with a build context used to get one.
	catalogCloneStepID = "catalog-clone"
	// catalogCloneWork is the directory the step writes its build's context
	// into: the Dockerfile and the clone script Fugaro generates, and nothing
	// else, so the build that holds the credential reads no file an entry
	// wrote.
	catalogCloneWork = "/workspace/clone"
	// catalogCloneDockerfile is the generated Dockerfile in that directory.
	catalogCloneDockerfile = catalogCloneWork + "/Dockerfile"
	// catalogCloneScript is the generated clone script in it.
	catalogCloneScript = catalogCloneWork + "/clone.sh"
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

// catalogCloneDockerfileText is the Dockerfile the clone step builds, in full:
// the engine's own, and no part of it the entry's.
//
// It runs from the pinned base digest the step hands it and sets
// GIT_CONFIG_NOSYSTEM=1, so the git that spends the credential is the base
// image's own git, reading no system config of the worker's — which is also
// what keeps the base image's system-wide git-lfs filter out of the clone, so
// the checkout writes pointer files and the objects are fetched by the script
// itself, after the `.lfsconfig` that could point them at another host has been
// read (rule 6).
//
// The export is the checkout and nothing else: the clone writes under /out, the
// stage the credential ran in leaves this build no other way than through the
// scratch stage below, and what that stage carries out is the image the step
// tags for the entry's build to copy — and, for an entry that names a build
// context, the same tree exported as the directory docker is handed.
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
COPY --from=clone --chown=1000:1000 /out/ /
`

// catalogCloneScriptText is the script the generated build runs, and the only
// thing in that build that reads the credential: the secret mount is its
// standard input's neighbour and never an argument, an environment value, a
// line of output or an error.
//
// Every git it runs goes through gitx, so the credential file, the two
// transports that are never allowed and the prompt that is never waited for are
// stated once: a fetch that git makes for itself — a submodule, an LFS object —
// inherits them because it is the same process.
//
// The values reach it through the environment (see the Dockerfile), and each is
// quoted where it is used: `--` before the URL of a clone or a fetch, `"$VAR"`
// everywhere, and no `eval`. What a repository names about itself — a
// submodule's URL, an LFS endpoint — is read from a file the clone just made and
// refused when it does not belong to this host, before the fetch that would use
// it.
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
const catalogCloneScriptText = `set -eu
export LC_ALL=C
CRED=/run/secrets/git-credentials

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

# check_urls reads the URLs a checkout declares for its submodules and refuses
# one that is not this host's, naming the submodule and never the URL: a URL
# that does not belong here is the one value that may carry a credential of its
# own, and this script prints no credential.
check_urls() {
  git config --file "$1" --get-regexp '^submodule\..*\.url$' | while read -r key url; do
    foreign_url "$url" || continue
    echo "the submodule $(printf '%s' "$key" | tr -d '[:cntrl:]') names a URL that is not $REPO_HOST: this build's credential is offered to $REPO_HOST alone" >&2
    exit 1
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
# .gitmodules naming another host is refused before any credential is offered to
# it (rule 6).
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
  gitx -C "$1" submodule foreach --quiet 'printf "%s\n" "$sm_path"' | while IFS= read -r path; do
    fetch_tree "$1/$path"
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

# The commit is the one the definition pinned and never the branch head: the
# branch head at build time is the tree the definition was copied from only by
# accident (D1394). The clone is shallow, so a commit that is not the tip is
# fetched by itself, and one the remote no longer has fails the build saying so
# rather than baking another tree — the shape the derived template's build uses.
gitx clone --quiet --depth 1 --no-checkout -- "$REPO_URL" /out
gitx -C /out cat-file -e "$COMMIT^{commit}" 2>/dev/null \
  || gitx -C /out fetch --quiet origin "$COMMIT" \
  || { echo "commit $COMMIT is no longer reachable: the branch was force-pushed; re-run the build" >&2; exit 1; }
gitx -C /out reset --quiet --hard "$COMMIT"

fetch_tree /out
`

// catalogCloneRevoke is what the step that last reads the credential ends with
// where the provider can revoke: the credential ends with that step (rule 7).
// Which step that is depends on the definition — the clone step for one that
// bakes nothing, the entry's own build step for one that bakes a checkout
// (Baked), which is the last of them to read the file.
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
// build step hands the Dockerfile it builds. A catalog build keeps it for an
// entry whose definition bakes a checkout — the bake stage is a RUN of that
// definition and clones with this credential, which no other step can hand it
// (Baked) — and takes it out for every other one: the entry's own RUN steps may
// ask for that secret by id (F19, review round 2), so where nothing in the
// definition mounts it the credential reaches the clone step's build and no
// build of the entry's own.
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

// catalogCloneStep is the step that clones the entry's repository for the
// entry's build to copy: the build request carries it, and neither the entry's
// Dockerfile nor the copied definition is part of the build it runs.
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
	script := catalogCloneScriptText
	if s.GitProvider == gitprov.KindGitHub && !s.Baked {
		script += catalogCloneRevoke
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
		Args:       []string{"-c", cloudBuildEscape(catalogCloneStepScript(script, s.Context != "", s.Baked))},
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
// step uses for the same file. The step is the last that mounts the volume it
// lives in, and for an entry whose build mounts no secret the entry's build step
// no longer mounts it at all. A definition that bakes is the exception
// (keepCredential): its own build step mounts the file and is the last to read
// it, so the file has to outlive this step, and the template's build step
// removes it at its own exit instead.
//
// The generated Dockerfile is built twice, and the two builds differ in their
// last flag alone: once tagged catalogCloneImage, which is the image the entry's
// Dockerfile copies the checkout out of, and — for an entry that names a build
// context — once more with the `type=local` export catalogCloneDest is written
// by. The second build is the first one's own cache — the same Dockerfile at the
// same arguments — so the clone itself runs once, and what the export writes is
// the tree the scratch stage carries out and nothing else.
func catalogCloneStepScript(script string, context, keepCredential bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "set -eu\nexport LC_ALL=C\ninstall -d -m 0700 %s\n", catalogCloneWork)
	if !keepCredential {
		fmt.Fprintf(&b, "trap 'rm -f %s' EXIT\n", catalogCloneCredential)
	}
	fmt.Fprintf(&b, "cat > %s <<'FUGARO_DOCKERFILE'\n%sFUGARO_DOCKERFILE\n", catalogCloneDockerfile, catalogCloneDockerfileText)
	fmt.Fprintf(&b, "cat > %s <<'FUGARO_SCRIPT'\n%sFUGARO_SCRIPT\n", catalogCloneScript, script)
	b.WriteString(`base=$(docker image inspect --format '{{index .RepoDigests 0}}' "$FUGARO_BASE") \
  || { echo "the base image $FUGARO_BASE is not on this worker" >&2; exit 1; }
docker build ` + catalogCloneBuildFlags + `  --tag ` + catalogCloneImage + ` \
  ` + catalogCloneWork + "\n")
	if context {
		b.WriteString("docker build " + catalogCloneBuildFlags +
			`  --output "type=local,dest=` + catalogCloneDest + `" \
  ` + catalogCloneWork + "\n")
		b.WriteString(catalogCloneGuard)
	}
	return b.String()
}

// catalogCloneBuildFlags is the flag list both builds of the generated
// Dockerfile take, ending in the continuation that joins the line after it: the
// same build twice, with the output the only difference between them.
const catalogCloneBuildFlags = `--progress plain \
  --file ` + catalogCloneDockerfile + ` \
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
// runs: the checkout the clone step tagged reaches it as the image
// catalogCloneImage, which the worker holds by the time this step runs and no
// flag of this step names. The git credential the derived template hands its
// build step is taken out of the request — with the volume that holds it, which
// no step after the clone mounts — for every definition whose own text mounts no
// secret (B9, D2311).
//
// A definition that bakes keeps both (Baked): the bake stage this package
// appends is a RUN of that definition and clones the entry's repository itself,
// with the secret mount this same argument satisfies, and the clone step's own
// checkout cannot stand in for it — the stage clones at the definition's
// FUGARO_COMMIT into the path the entry's Dockerfile copies from, and the
// marker, the guards and the submodules are that stage's own text. It is the
// last step of the build to read the credential, so the revoke moves here with
// the file (catalogCloneRevoke), and the template's own exit trap removes it.
//
// Only the credential is taken out here, and no other secret is left to take
// out: the repository's workflow secrets, declared in its fugaro.yaml, reached a
// catalog build until B9 answered D2311, and the request now states an empty
// _SECRET_ENVS — the build step's loop over it mounts nothing, and the catalog
// refuses an entry whose own bytes ask for a secret when it reads them. A
// dependency behind a private registry is served by an `--image` entry instead,
// which the catalog runs and never builds.
func catalogEntryBuildStep(build *cloudbuild.BuildStep, s CatalogBuildSpec) error {
	for i, arg := range build.Args {
		if !strings.Contains(arg, catalogTemplateCredentialArg) {
			continue
		}
		if !s.Baked {
			script, err := dropCredentialLine(arg)
			if err != nil {
				return err
			}
			build.Args[i] = script
			build.Volumes = nil
			return nil
		}
		revoke := ""
		if s.GitProvider == gitprov.KindGitHub {
			u, err := url.Parse(s.RepoURL)
			if err != nil || u.Host == "" {
				return fmt.Errorf("the repository URL %s does not parse", RedactURL(s.RepoURL))
			}
			build.Env = append(build.Env,
				"CRED="+catalogCloneCredential,
				"REPO_HOST="+u.Host,
				"GITHUB_API="+github.DefaultBaseURL,
			)
			revoke = cloudBuildEscape(catalogCloneRevoke)
		}
		build.Args[i] = arg + revoke
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
