#!/usr/bin/env bash
# Check 196 — a repository's image build and its daily image check, served by the
# credentials service, and the flip that takes the build account's accessor away.
#
# It settles S4's reading (plan B4.7, its "What it measures"; the
# reconciliation's **D2076** (a) and **D2077** (a), **R5**, **R6**, **G18**,
# **G19**, **G20**, **F28**, **F29** and **U9**; the build route is B4.3, the
# grant is B4.4 and the fetch is B4.5). An offline reading settles nothing:
# whether the image the owner's repositories really bake is submitted and
# finishes, under which account, with which credential, what the daily check
# runs as, and whether the service's GUEST list admits one caller and not
# another, are answers of a live installation. So this check reads what real
# builds did: the request each build carried, the job the daily check runs as
# (`gcloud run jobs`), the account that may invoke the service
# (`gcloud run services get-iam-policy`) and the service's OWN lines
# (`credsvc: audit`, purpose `build`) — and it never calls the service itself.
#
# WHAT THE INSTALLATION BUILDS NOW (changed 2026-11-24). A repository's own
# derived image is disabled (D1900): `fugaro image build --workflow` and
# `fugaro image refresh` print a notice and build nothing, and what the owner's
# repositories build is the CATALOG ENTRY their `image_map` names. That build is
# not a repository's build at all. `fugaro image build <entry>` (and the same
# step inside `fugaro apply`, apply.CatalogBuild) submits it as
# gcp.CatalogBuildSpec: it runs as the PROJECT'S BUILD IDENTITY
# (fugaro-build-identity@<project>.iam.gserviceaccount.com, B6/D2301) and never
# as the repository's own `fugaro-b-…` build account; its request names the
# entry in `_IMAGE` (`<region>-docker.pkg.dev/<project>/<shared-registry>/catalog-<entry>`)
# and the checkout it was submitted from in `_REPO_URL`, and carries NO `_SLUG`,
# no `_WORKFLOW` and no `_SECRET_ENVS`; and it carries `_CREDENTIALS_SERVICE_URL`
# EMPTY, because a catalog build clones with the provider git credential its
# request MOUNTS as `availableSecrets` (gcp.CatalogBuildSpec.AvailableSecrets).
# B4.9's flip does not reach it — the build-credentials route serves one
# repository to the account bound to that repository, and the project's build
# identity is not that account (docs/secrets-brokering's build classes note) —
# so a catalog build keeps its mounts before and after the flip. This check
# reads what a build really carries and says so, rather than requiring a shape
# no build of this installation has.
# A FAIL in the switch mode stops S4 and returns to the owner as a decision
# (B4.8 is the gate, B4.10 the flip's).
#
# Usage:      bash scripts/live-checks/check196.sh
#             bash scripts/live-checks/check196.sh --dry-run      (prints the
#             plan and calls NOTHING: no gcloud, no fugaro)
#             bash scripts/live-checks/check196.sh --after-flip   (Check 196's
#             second half: the accessor and availableSecrets gone, a build that
#             still succeeds)
# Needs:      bash, mktemp, python3, gcloud (authenticated as you: list the
#             REGION's own builds and read each repository's own catalog builds
#             out of them, read the credentials service itself and ONE of its
#             executions, read Cloud Run jobs, read the service's own IAM
#             policy, and read the service's own lines out of Cloud Logging) and
#             fugaro (doctor, version, and `project policy show --json`, which
#             is where the project's published `image_map` is read). The
#             project config under $XDG_CONFIG_HOME/fugaro/projects is read as
#             a file when it states a map of its own. Nothing is installed and
#             nothing is fetched.
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             Both are HARD-CODED in this file and never read from `gcloud
#             config get-value project`, and `fugaro doctor --project fugaro
#             --all` must print its `project: … (GCP fugaro-dev,` line: a check
#             that read the project from the environment could be pointed at
#             another installation by a stray config, and this one must never
#             be. Never belong.
# Creates:    nothing in the installation, the project or any repository. This
#             check WRITES nothing and DELETES nothing: it reads Cloud Build's
#             own records, Cloud Run's jobs and the service's own lines, and the
#             other repository's builds. The builds it reads are the catalog
#             entry's own, which `fugaro image build <entry>` (or `fugaro
#             apply`) submitted, and they are the project's own log rather than
#             this check's.
# Costs:      wall clock, not money: minutes at the terminal. No build is
#             submitted by the check itself.
# Runs:       by YOU. No agent runs any step of it, and no agent has: what it
#             measures is a live installation. Its own test (check196_test.sh)
#             drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else — this check creates no
#             cloud resource. It runs from the exit trap too, so a Ctrl-C leaves
#             nothing of the check's behind.
# Output:     progress on stderr, ONE block on stdout headed CHECK 196 RESULT,
#             then ... RESULT END (exit N) — to copy back to the coordinator, and
#             nothing else. Exit 0: every criterion that was measured held. 1: a
#             measured criterion did not hold, or a guard stopped the check
#             before it could run. 2: a row could not be measured for a reason
#             that is this check's own setup rather than the installation (the
#             block says which, by name). In the block, a key the run never
#             reached reads `FAIL — not reached`, which is a row that was NOT
#             measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# authorization header. The build's `availableSecrets` entries this check reads
# are Secret Manager CONTAINER IDs (a name, never a value, G18), the audit line
# names the CREDENTIAL and never its value, and the check holds no credential of
# its own beyond the gcloud access token it mints to read the project's own
# records.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK196_REPO (REQUIRED: the GitHub sandbox repository under test, as
# <owner>/<repository>); CHECK196_BITBUCKET (REQUIRED: the Bitbucket sandbox
# repository, which the same installation serves); CHECK196_BUILDS (how many of
# the project's recent builds to read; default 50), CHECK196_LOG_AGE,
# CHECK196_LOG_LIMIT, CHECK196_LOG_WAIT_SECONDS, CHECK196_LOG_POLL_SECONDS,
# CHECK196_DRY_RUN, CHECK196_KEEP_SCRATCH. None of them can point this check at
# another project or another region: those two are constants in this file.
#
# WHICH BUILDS ARE A REPOSITORY'S (changed 2026-11-24). The entry is the one the
# project's EFFECTIVE `image_map` names for the repository (D1906): this
# machine's project config when it states a map, the published policy node's
# otherwise — read with `fugaro project policy show --json`, the one read of the
# node this check makes — and the repository's builds are the newest ones whose
# `_IMAGE` names `catalog-<that entry>`. The build's own `_REPO_URL` is the
# ENTRY's source repository — the Dockerfile's repository, which one shared
# entry serves to every repository mapped to it — so matching a repository's
# builds by it missed every repository that is not the entry's source. A
# repository the map does not mention keeps the `_REPO_URL` match.
# The listing itself is the installation's REGION: `gcloud builds list
# --region=${L}` (a catalog build is a REGIONAL build, and the global listing
# holds none of them). With no region to pass there is no listing: the row
# stops rather than reading the global one, because a global listing is a
# reading of another region's builds and would report every catalog build as
# missing.
# The daily CHECK job is a repository's own and is unchanged by any of this: a
# repository-level image check is disabled with the repository-level images
# (D1900, internal/infra's IS2), so a repository the installation deploys no
# `fugarochk-` job for is read INFO rather than as a row that could not be
# measured — and a job an earlier release made, which `fugaro update` keeps, is
# read exactly as before.
#
# -E (errtrace) as well as -e: without it a command that fails inside a command
# substitution ends the shell with a bare exit 1 and NO error trap — the block
# would print later rows as `FAIL — not reached` and the owner would get no line
# saying where the run broke. With it, every such failure reaches on_error and
# ends as one that could not measure (exit 2), which is what its own contract
# says of that.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the block this check
# prints is pasted into a pull request, and `bash -x` would print every command
# of the run into the log it is read from. BASH_XTRACEFD is unset so a trace
# redirected to a file this script knows nothing about cannot be reopened, and
# guard_xtrace REFUSES the run when the shell arrived with tracing on.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory.
stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row the check could NOT measure, for a reason that is the
# check's own setup or the installation's configuration rather than a break. The
# row still reads FAIL in the block — it is not a pass and must not be read as
# one — but it does not by itself make the verdict "a criterion did not hold":
# the check ends as one that could not measure (exit 2), which is what its own
# contract says of a row that needed a state it did not find.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 196 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  need bash
  need mktemp
  need python3 "every gcloud document below is read by the reader in the scratch directory"
  need gcloud
  need fugaro
}

# ------------------------------------------------------------------- constants
# The project and the region are HARD-CODED, never read from `gcloud config`:
# see the header. The service's Cloud Run name is a literal in internal/infra
# (CredentialsServiceName) rather than a per-installation value, which is what
# makes the log filter below the same on every installation.
P=fugaro-dev
L=us-east5
# The Fugaro project's own name, which every fugaro command below is pinned to
# (`--project`): the installation is the one under test and nothing may select
# another.
NAME=fugaro
SVC=fugaro-credentials
# A repository's build service account is `fugaro-b-…@<project>.iam.gserviceaccount.com`
# (gcp.BuildServiceAccountID), so a caller is recognised as a repository's build
# by this prefix and the installation's own domain. The daily check's job name
# starts `fugarochk-` (gcp.CheckJobName). The CATALOG build's account is not a
# repository's at all: it is the installation's ONE project-wide build identity,
# `fugaro-build-identity@<project>.iam.gserviceaccount.com`
# (gcp.BuildIdentityAccountID, B6/D2301), and the shared registry every catalog
# entry's image lives in names the entry after this segment
# (`catalog-<entry>`, gcp.SharedCatalogImage).
BUILD_ACCOUNT_PREFIX=fugaro-b-
BUILD_IDENTITY_ID=fugaro-build-identity
CATALOG_IMAGE_PREFIX=catalog-
CHECK_JOB_PREFIX=fugarochk-

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk196-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
PRINTED=""
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
AT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk196.XXXXXXXX") || die "Check 196 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  fact scratch PASS "$SCRATCH (mode 0700, made by this check, removed by its EXIT trap)"
}

remove_scratch() {
  case "$SCRATCH" in
  "")
    # A guard stop leaves this empty and there is nothing to refuse: saying
    # "refusing to remove a scratch directory of ''" for a run that never made
    # one reads like a cleanup that went wrong. The guard stays for the two
    # paths that do mean trouble: a bare "/" and the owner's home directory.
    return 0
    ;;
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  if [ -n "$KEEP_SCRATCH" ]; then
    # The knob docs/secrets-brokering/check196-steps.md tells the owner to set
    # to read what the check wrote is not a directory that could not be removed:
    # the row below says which of the two it is, and only the second is a FAIL.
    SCRATCH_KEPT=yes
    say "keeping $SCRATCH (CHECK196_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, undone. There is nothing of this check's in
# the installation, the project or any repository — it writes nothing and
# deletes nothing, and the builds it reads are the project's own log rather than
# resources it made — so this row is about the one thing that COULD have been
# left: the scratch directory, which is removed by remove_scratch and read back
# there.
restore() {
  fact restore PASS "this check created no cloud resource, submitted no build and touched no repository: the only thing it made is the scratch directory, whose row is below"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to run, and a second
  # Ctrl-C during it would kill this shell with the reading half done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  # The scratch directory's row is added before the verdict is taken, because
  # the verdict is taken from the facts: a directory this check could not remove
  # is a FAIL, and a block that printed `scratch-removed: FAIL` beside `END
  # (exit 0)` would read as a pass.
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory, so there is none to remove"
  elif [ "$SCRATCH_KEPT" = yes ]; then
    # A directory the owner asked to keep is not one this check failed to remove:
    # the row says it was kept, and the verdict stays whatever the reading found.
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK196_KEEP_SCRATCH is set), at $SCRATCH"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  # A row that reads FAIL is one of two things, and the verdict says which: a
  # criterion that did not hold (exit 1), or a row this check could not measure
  # — every unmeasured row is recorded FAIL as well, so that it is never read as
  # a pass, and its own contract names exit 2 ("could not measure"). Telling
  # them apart by the keys UNMEASURED_KEYS names is the only way to reach the
  # second: a block with any other FAILed row in it is the first, and it wins.
  local key hard=no
  for key in $(printf '%s\n' "$facts" | sed -n 's/^\([^|]*\)|FAIL|.*/\1/p'); do
    case " ${UNMEASURED_KEYS} " in
    *" $key "*) ;;
    *) hard=yes ;;
    esac
  done
  if [ "$hard" = yes ]; then
    if [ "$status" = 0 ]; then status=1; fi
  elif [ -n "$UNMEASURED_KEYS" ]; then
    if [ "$status" = 0 ]; then status=2; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# redact: what a gcloud message may carry back into the block, which is pasted
# into a pull request. A credential-shaped run of characters goes, whatever it
# is. No token of this check's ever reaches a message it prints, and this is the
# second line of that defence rather than the first.
redact() {
  sed -E \
    -e 's/([Aa]uthorization)[[:space:]]*:[^|]*/\1: <redacted> /g' \
    -e 's/(Bearer|Basic)[[:space:]]+[A-Za-z0-9._~+/=-]{12,}/<redacted>/g' \
    -e 's/[A-Za-z0-9_]*(token|Token|TOKEN|secret|Secret|SECRET)[A-Za-z0-9_]*=[^[:space:]]+/<redacted>/g' \
    -e 's/((oauth|api)[_-]?key["=: ]+)[A-Za-z0-9._+=-]{12,}/\1<redacted>/Ig'
}

# last_lines FILE [N]: the last N lines of a file, on one redacted line, cut
# short. The one shape every failure row reads a command's own stderr with.
last_lines() {
  local f=$1 n=${2:-2}
  [ -f "$f" ] || {
    printf 'no output'
    return 0
  }
  tail -n "$n" "$f" | tr -d '\r' | tr '\n' ' ' | tr -s ' ' | redact | cut -c1-400
}

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# write_reader installs the one file that reads every document this check takes
# — the project's recent builds, the repository's own Cloud Run jobs, one
# execution, the service's own IAM policy and the service's own log lines — so
# that no reading of a JSON shape is spelled twice in this script and none of
# them is done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 196's reader: one file, one reading per subcommand, no credential in
any of them.

builds  argv: `gcloud builds list --region=<region> --format=json`'s file, a
        repository and the catalog entry the project's `image_map` names for it
        (empty when the map does not mention the repository). Prints the
        repository's OWN newest CATALOG-ENTRY build field by field: its id, its
        status, the account it ran as (a resource name, reduced to the address
        it ends in), the catalog ENTRY and image its `_IMAGE` names, the SOURCE
        repository its own `_REPO_URL` names (the entry's checkout, which is not
        the served repository when one entry serves several), which of the two
        selected the build (`match=map` or `match=repourl`), the substitutions
        this check reads (`_SLUG`, `_WORKFLOW`, `_SECRET_ENVS`,
        `_CREDENTIALS_SERVICE_URL`), the Secret Manager CONTAINER ids its
        `availableSecrets` names (a container is a name and never a value, G18)
        and the env names its build step carries as `secretEnv`. With an entry
        the builds are the ones whose `_IMAGE` names `catalog-<entry>`, because
        the build's `_REPO_URL` is the entry's SOURCE repository and not the
        repository the entry serves; without one (a repository the map does not
        mention) they are the ones whose `_REPO_URL` is the repository's own
        clone URL. A build that is not a catalog entry's is not this
        repository's image build at all (D1900: repository-level images are
        disabled) and is skipped. `found=no` when the region's last builds hold
        none of the repository's.
imagemap argv: a project config's path (or `-`), and `fugaro project policy
        show --json`'s file (or `-`). Prints the project's EFFECTIVE
        repo-to-image map (D1906) as `map=<repo>|<entry>` lines, and
        `map-read=local|published|none` naming which layer answered: the config
        when it states an `image_map` block of its own (an empty block is a
        stated empty map, which takes a published one away), the published
        node's otherwise.
job     argv: `gcloud run jobs list --format=json`'s file, a repository
        (`<workspace>/<repository>`). Prints the repository's own daily CHECK job
        (named `fugarochk-…`, gcp.CheckJobName), found by the clone URL its own
        FUGARO_REMOTE carries or by its `fugaro_repo` label — and the label it
        prints back as `slug=`, because that is the key the repository's record
        and the service's own lines are written under: its name, the account its
        containers run as, whether it carries FUGARO_CREDENTIALS_SERVICE_URL at
        all, and each Secret Manager container its env is fed from
        (`mount=<ENV>|<id>`), which is the mount the flip takes away. `found=no`
        when this installation deploys no such job for the repository.
svc     argv: `gcloud run services describe fugaro-credentials --format=json`'s
        file. Prints the service's own name and the URL Cloud Run serves it on
        (`status.url`), which is the address every caller mints its ID token
        for — read off the SERVICE and never off a build, so this row is a
        measurement even when the project holds no build at all.
exec    argv: `gcloud run jobs executions list --format=json`'s file. Prints the
        newest execution's name and status, and how many the listing holds.
iam     argv: one `gcloud secrets get-iam-policy --format=json` document. Prints
        one `accessor=<member>` line per member of a binding whose role can read
        a version, and the counts. Nothing of it is a value.
svciam  argv: `gcloud run services get-iam-policy --format=json`'s file, as JSON.
        Prints every binding's role and members, so the row can say who may
        invoke the credentials service — which is B4.4's grant and the front
        door a build account that holds it nowhere else is refused at.
audit   argv: the service's lines' file. Prints one `audit=<status>|<credential>|
        <purpose>|<caller>|<repository>` line per `credsvc: audit` line of
        purpose `build`, and `refused=<status>|<sentence>` per `credsvc: refused`
        line: the credential is a NAME and never a value.
"""
import json
import re
import sys

WORD = re.compile(r"[^A-Za-z0-9._:@/+-]")
ACCESSOR_ROLES = ("roles/secretmanager.secretAccessor",)
BUILD_ACCOUNT_PREFIX = "fugaro-b-"
CHECK_JOB_PREFIX = "fugarochk-"


def word(v, cap=80):
    """One field of a document, for the block's own `key=value` shape. A field
    the document does not carry is `-` and never the word `None`: the callers
    read `-` as "nothing here", and a `None` printed as a word is an answer
    they would take for a value — a build with no `_SLUG` substitution, which
    is every catalog build, would be looked up as the repository named `None`."""
    if v is None:
        return "-"
    return WORD.sub("", str(v))[:cap] or "-"


def text(v, cap=300):
    """Free text, for what the check PRINTS: a sentence keeps its spaces, and
    only what the block's own shape cannot carry is taken out — the field
    separator, a newline and any other control character."""
    return " ".join(str(v).split())[:cap].replace("|", "/") or "-"


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def bare_member(m):
    """An IAM member with its type prefix dropped (`serviceAccount:`, `user:`,
    `group:` or `domain:`) — and with nothing else touched. A prefix outside that
    set (`deleted:`, a `principalSet:` URL) is left alone: none of them is an
    account, and a member that is not one must not become equal to one by a
    rewrite."""
    s = str(m)
    for prefix in ("serviceAccount:", "user:", "group:", "domain:"):
        if s.startswith(prefix):
            return s[len(prefix):]
    return s


def env(spec):
    out = {}
    for e in (spec.get("env") or []) if isinstance(spec, dict) else []:
        if isinstance(e, dict) and e.get("name"):
            out[e["name"]] = e
    return out


def spec_of(doc):
    """The container spec of a job (`spec.template.spec.template.spec`), or one
    execution's (`spec.template.spec`). The known paths are tried in order and
    the document is finally searched for the first containers list, which is the
    one fact both shapes carry."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec")):
        node = doc
        for key in path:
            node = node.get(key) if isinstance(node, dict) else None
            if node is None:
                break
        if isinstance(node, dict) and isinstance(node.get("containers"), list):
            return node
    return None


def containers_of(doc):
    spec = spec_of(doc)
    return (spec.get("containers") or []) if isinstance(spec, dict) else []


def source_of(entry):
    """What feeds one environment variable: the Secret Manager CONTAINER it is
    read from, a plain value, or nothing at all. A mount is
    `valueSource.secretKeyRef.secret` (a DERIVED id), and its short name is the
    last path segment with any version suffix dropped."""
    src = entry.get("valueSource") or {}
    ref = ((src.get("secretKeyRef") or {}).get("secret")
           or (src.get("secretKeyRef") or {}).get("name") or "")
    if ref:
        return "secret", ref.split("@")[-1].split(":")[-1].split("/")[-1]
    if entry.get("value") not in (None, ""):
        return "value", ""
    return "absent", ""


def commit_short_name(v):
    """A Secret Manager version reference (`projects/<p>/secrets/<id>/versions/<v>`)
    reduced to the container's short id, the last readable segment before
    `/versions`."""
    s = str(v or "")
    i = s.find("/versions/")
    if i >= 0:
        s = s[:i]
    return s.split("/")[-1]


def catalog_entry_of(b):
    """The catalog ENTRY a build bakes, or "" for a build that is not a catalog
    entry's. `_IMAGE` is the whole answer: the catalog build's image is
    `<region>-docker.pkg.dev/<project>/<shared-registry>/catalog-<entry>`
    (gcp.SharedCatalogImage/SharedRegistryRepoID), while a derived build's names
    the repository and the workflow. An entry that names a context of its own is
    the same shape; a PREBUILT entry's image is the author's own reference and
    is therefore not a catalog build of this project, which is what the segment
    count and the prefix together reject."""
    image = str(((b.get("substitutions") or {}).get("_IMAGE")) or "")
    parts = image.split("/")
    if len(parts) < 4:
        return ""
    name = parts[-1]
    if not name.startswith("catalog-"):
        return ""
    return name[len("catalog-"):]


def repourl_of(b):
    """The clone URL a build's request names as `_REPO_URL`: the checkout it was
    submitted from. For a catalog build that is the ENTRY's source repository —
    the Dockerfile's repository — and not the repository the entry serves, which
    is why the map's entry and not this is what matches a repository's builds."""
    return str(((b.get("substitutions") or {}).get("_REPO_URL")) or "")


def builds_of(path, repo, entry=""):
    """Every CATALOG-ENTRY build of one repository, newest first, and which of
    the two answers selected them.

    With an ENTRY (the catalog entry the project's `image_map` names for the
    repository) the builds are the ones whose `_IMAGE` names `catalog-<entry>`:
    that is the image the repository runs, and one entry may serve several
    repositories, so the build's own `_REPO_URL` — the entry's SOURCE repository
    — cannot be the match. Without one (a repository the map does not mention)
    they are the ones whose `_REPO_URL` is the repository's own clone URL, which
    is what this check read before the map existed.

    Either way the build must be a catalog entry's: a repository-level derived
    image is disabled (D1900), so a derived build in this list is history from
    before that and says nothing about the image the repository builds today."""
    out = []
    for b in load(path) or []:
        if not isinstance(b, dict):
            continue
        if not catalog_entry_of(b):
            continue
        if entry:
            if catalog_entry_of(b) != entry:
                continue
        else:
            url = repourl_of(b)
            if not (url == "https://" + repo + ".git" or url.endswith("/" + repo + ".git")):
                continue
        out.append(b)
    out.sort(key=lambda b: str(b.get("startTime") or b.get("createTime") or ""), reverse=True)
    return out, ("map" if entry else "repourl")


def yaml_scalar(s):
    s = s.strip()
    if len(s) >= 2 and s[0] == s[-1] and s[0] in "'\"":
        return s[1:-1]
    return s


def local_image_map(path):
    """The `image_map` block of a project config, or None when the file states
    no such block. None is not {}: a file that says nothing keeps the published
    node's map, while a written block with no entry in it is how a published one
    is taken away on purpose (localcfg's ImageMapEffective). The block is read
    from the file's own lines rather than through a YAML library, because the
    one shape this file writes for a `map[string]string` is `key: value` lines
    indented under the key; a value the writer quoted is unquoted here."""
    if not path or path == "-":
        return None
    try:
        with open(path) as fh:
            lines = fh.read().splitlines()
    except Exception:
        return None
    out, saw, inblock = {}, False, False
    for raw in lines:
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        if raw[:1] not in (" ", "\t"):
            key, sep, rest = raw.partition(":")
            inblock = bool(sep) and key.strip() == "image_map"
            if inblock:
                saw = True
                inner = rest.strip()
                if inner.startswith("{") and inner.endswith("}"):
                    for part in inner[1:-1].split(","):
                        k, _, v = part.partition(":")
                        if k.strip():
                            out[yaml_scalar(k)] = yaml_scalar(v)
                    inblock = False
            continue
        if not inblock:
            continue
        key, sep, rest = raw.strip().partition(":")
        if sep and key.strip():
            out[yaml_scalar(key)] = yaml_scalar(rest)
    return out if saw else None


def published_image_map(path):
    """The project's published `image_map` out of `fugaro project policy
    show --json`, or None when nothing is published and when the node states no
    map at all (both are "no map of the node's", which the file's own answer
    then stands in for or leaves absent)."""
    doc = load(path)
    if not isinstance(doc, dict):
        return None
    policy = doc.get("policy")
    if not isinstance(policy, dict):
        return None
    m = policy.get("image_map")
    if not isinstance(m, dict) or not m:
        return None
    return {str(k): str(v) for k, v in m.items()}


def containers_of_build(b):
    """The Secret Manager containers a build's `availableSecrets` names, one per
    env, in the order the request writes them. Empty for a served repository
    after the flip, where the request carries none (B4.9)."""
    out = []
    av = b.get("availableSecrets") or {}
    for s in av.get("secretManager") or []:
        if isinstance(s, dict):
            out.append((s.get("env") or "-", commit_short_name(s.get("versionName"))))
    return out


def secret_envs_of_build(b):
    """The env names any build step carries from a Secret Manager container
    (`secretEnv`), which is the second half of what the flip takes away."""
    out = []
    for st in b.get("steps") or []:
        if isinstance(st, dict):
            for name in st.get("secretEnv") or []:
                out.append(str(name))
    out.sort()
    return out


def account_address(v):
    """A Cloud Build `serviceAccount` field, reduced to the address it ends in.
    The API reports a resource name (`projects/<p>/serviceAccounts/<a>`), and
    `<a>` is the bare address where it kept one and the numeric uniqueId where
    it resolved one; this reading keeps whatever it found so a reader sees it,
    and the caller judges the prefix."""
    return str(v or "").split("/")[-1]


cmd = sys.argv[1] if len(sys.argv) > 1 else ""
if cmd == "builds":
    repo = sys.argv[3]
    entry = sys.argv[4] if len(sys.argv) > 4 else ""
    bs, match = builds_of(sys.argv[2], repo, entry)
    if not bs:
        print("found=no")
    else:
        b = bs[0]
        print("found=yes")
        print("count=%s" % len(bs))
        subs = b.get("substitutions") or {}
        print("match=%s" % match)
        print("source=%s" % word(repourl_of(b), 200))
        print("id=%s" % word(b.get("id"), 60))
        print("status=%s" % word(b.get("status"), 40))
        print("account=%s" % word(account_address(b.get("serviceAccount")), 160))
        print("entry=%s" % word(catalog_entry_of(b), 100))
        print("image=%s" % text(subs.get("_IMAGE"), 200))
        print("slug=%s" % word(subs.get("_SLUG"), 100))
        print("workflow=%s" % word(subs.get("_WORKFLOW"), 80))
        print("secret_envs=%s" % word(subs.get("_SECRET_ENVS"), 200))
        url = subs.get("_CREDENTIALS_SERVICE_URL") or ""
        print("url=%s" % ("present" if url else "absent"))
        # The URL is the service's own address, which is a name and not a
        # credential: it is printed whole so a reader can compare it with the
        # installation's service.
        print("url_value=%s" % text(url, 200) if url else "url_value=-")
        print("started=%s" % word(b.get("startTime"), 40))
        print("finished=%s" % word(b.get("finishTime"), 40))
        cons = containers_of_build(b)
        print("containers=%s" % len(cons))
        for name, ident in cons:
            print("container=%s|%s" % (word(name, 80), word(ident, 200)))
        envs = secret_envs_of_build(b)
        print("secretenv=%s" % len(envs))
        for name in envs:
            print("secretenv_name=%s" % word(name, 80))
elif cmd == "imagemap":
    # The EFFECTIVE map (localcfg's ImageMapEffective): this machine's project
    # config when it states an `image_map` block — an empty block included,
    # which is a published map taken away on purpose — and the published policy
    # node's otherwise. The repository keys are printed as written: they are
    # `owner/name` and the caller looks one up by the name it was given.
    local = local_image_map(sys.argv[2] if len(sys.argv) > 2 else "")
    if local is not None:
        print("map-read=local")
        for repo in sorted(local):
            print("map=%s|%s" % (repo, local[repo]))
    else:
        published = published_image_map(sys.argv[3] if len(sys.argv) > 3 else "")
        if published is not None:
            print("map-read=published")
            for repo in sorted(published):
                print("map=%s|%s" % (repo, published[repo]))
        else:
            print("map-read=none")
elif cmd == "job":
    # The repository is named as a person writes it (`<workspace>/<repository>`),
    # never by its slug: a CATALOG build carries no `_SLUG` substitution, so the
    # build's own record is not where a repository's slug can be read — the job
    # is, and the label it is found by is the slug this reading prints back.
    repo = sys.argv[3]
    owner, _, short = repo.partition("/")
    doc = load(sys.argv[2]) or []
    found = None
    for job in doc:
        if not isinstance(job, dict):
            continue
        name = ((job.get("metadata") or {}).get("name")) or ""
        if not name.startswith(CHECK_JOB_PREFIX):
            continue
        # The check job of ONE repository: matched by a container's own
        # FUGARO_REMOTE, which is the clone URL the job was applied for — the
        # repository's own and no other's — or, for a job whose containers name
        # no remote this reading can see, by the `fugaro_repo` label the
        # installation writes (the repository's canonical slug).
        label = ((job.get("metadata") or {}).get("labels") or {}).get("fugaro_repo") or ""
        remote = ""
        for c in containers_of(job):
            remote = (env(c).get("FUGARO_REMOTE") or {}).get("value") or remote
        candidates = [repo, short, owner + "-" + short]
        owns = any(remote.endswith("/" + c + ".git") for c in candidates if c) \
            or (bool(label) and any(label == c or label.startswith(c + "-")
                                    for c in candidates if "/" not in c))
        if not owns:
            continue
        found = job
        break
    if found is None:
        print("found=no")
    else:
        print("found=yes")
        spec = spec_of(found)
        print("name=%s" % word((found.get("metadata") or {}).get("name"), 80))
        print("slug=%s" % word(((found.get("metadata") or {}).get("labels") or {}).get("fugaro_repo"), 100))
        sa = (spec.get("serviceAccountName") if isinstance(spec, dict) else "") or ""
        print("account=%s" % word(sa, 160) if sa else "account=-")
        url, mounts = "absent", []
        for c in containers_of(found):
            e = env(c)
            if "FUGARO_CREDENTIALS_SERVICE_URL" in e:
                url = "present"
            for name, entry in e.items():
                kind, ident = source_of(entry)
                if kind == "secret":
                    mounts.append((name, ident))
        print("url=%s" % url)
        print("mounts=%s" % len(mounts))
        for name, ident in mounts:
            print("mount=%s|%s" % (word(name, 80), word(ident, 200)))
elif cmd == "exec":
    doc = load(sys.argv[2])
    rows = doc if isinstance(doc, list) else []
    rows = [r for r in rows if isinstance(r, dict)]
    rows.sort(key=lambda r: str((r.get("metadata") or {}).get("creationTimestamp") or ""), reverse=True)
    print("count=%s" % len(rows))
    if rows:
        r = rows[0]
        print("name=%s" % word((r.get("metadata") or {}).get("name"), 120))
        status = ((r.get("status") or {}).get("conditions") or [{}])
        state = "-"
        for c in status:
            if isinstance(c, dict) and c.get("type") == "Completed":
                state = str(c.get("status") or "-")
        print("state=%s" % word(state, 40))
    else:
        print("name=-")
        print("state=-")
elif cmd == "iam":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        accessors = 0
        for b in doc.get("bindings") or []:
            if not isinstance(b, dict):
                continue
            role = b.get("role") or ""
            for m in b.get("members") or []:
                if role in ACCESSOR_ROLES:
                    accessors += 1
                    print("accessor=%s" % word(bare_member(m), 200))
        print("accessors=%s" % accessors)
elif cmd == "svciam":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        n = 0
        for b in doc.get("bindings") or []:
            if not isinstance(b, dict):
                continue
            role = b.get("role") or ""
            for m in b.get("members") or []:
                n += 1
                print("binding=%s|%s" % (word(role, 120), word(bare_member(m), 200)))
        print("bindings=%s" % n)
elif cmd == "svc":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        meta = doc.get("metadata") or {}
        status = doc.get("status") or {}
        print("name=%s" % word(meta.get("name"), 80))
        # status.url is the address Cloud Run serves the revision on, which is
        # the audience every caller mints its Google ID token for
        # (internal/cli/credentials_serve.go's credentialsServiceAudience): a
        # name, never a credential.
        print("url=%s" % (text(status.get("url") or "", 200) or "-"))
elif cmd == "audit":
    for entry in load(sys.argv[2]) or []:
        if not isinstance(entry, dict):
            continue
        p = entry.get("jsonPayload") or {}
        msg = entry.get("textPayload") or p.get("msg") or p.get("message") or ""
        if msg == "credsvc: audit":
            print("audit=%s|%s|%s|%s|%s" % (
                word(p.get("status"), 40),
                word(p.get("credential"), 120),
                word(p.get("purpose"), 40),
                word(p.get("caller"), 160),
                word(p.get("repository"), 100)))
        elif str(msg).startswith("credsvc:"):
            # A refusal is any other line this service writes, and its sentence
            # is the service's own message plus the scalar fields it carried
            # (`credsvc: the audit event's <field> is <reason>` is one of them).
            # Nothing here is a credential's value: a value has nowhere in the
            # service's lines to be, and the block redacts what it prints anyway.
            extra = " ".join("%s=%s" % (k, v) for k, v in sorted(p.items())
                             if isinstance(v, (str, int)) and k != "msg")
            print("refused=%s|%s" % (word(p.get("status"), 40) or "-",
                                     text(msg + (" " + extra if extra else ""), 300)))
PY
  chmod 700 "$SCRATCH/reader.py"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    say "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print every command this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check196.sh -- the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token or a command's environment"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`.
  # Check 196 is a reading on ONE installation: run anywhere else and every line
  # below is about builds, jobs and a service of another project, which is why
  # this stops rather than warns.
  local doctor_out v
  # --all, because the `project:` line is part of doctor's informational tail:
  # the default view prints its pointer lines and stops, so a guard reading the
  # plain view finds no project line at all and stops every run with "this is
  # not the fugaro-dev installation".
  doctor_out=$(fugaro doctor --project fugaro --all || true)
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP fugaro-dev,' >/dev/null; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project fugaro --all printed no 'project: ... (GCP fugaro-dev,' line. Either this is not the fugaro-dev installation, or --project fugaro disagrees with this checkout's project:, or doctor itself failed (doctor is not offline: it needs usable credentials). Run 'fugaro doctor --project fugaro --all' here yourself and read its output before going on — this check runs on the sandbox installation only, never belong"
  fi
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  case "$v" in
  dev | *-dev* | *dirty*)
    fact "fugaro-version" FAIL "$v — a development build is not the release this check is about: B4.7's check runs on the release that carries B4.3–B4.7, and a checkout build resolves no released credentials-service image to deploy (internal/cli's resolveCredentialsImage). Install the release under test and run it again"
    ;;
  *)
    fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
    ;;
  esac
  fact "project-region" PASS "${P}/${L} (installation fugaro, sandbox repositories only; never belong)"
}

guard_one_repo() { # guard_one_repo REPO KNOB
  local repo=$1 knob=$2 owner
  case "$repo" in
  */*/* | /* | */ | "") stop "$knob must be <workspace>/<repository> and it is '$repo'" ;;
  esac
  owner=${repo%%/*}
  case "$owner" in
  belong) stop "$repo names the owner's own workspace ($owner): this check runs against sandbox repositories of the fugaro-dev installation and nothing else, never belong" ;;
  esac
}

guard_inputs() {
  [ -n "$REPO" ] || stop "CHECK196_REPO must name the GitHub sandbox repository under test, as <workspace>/<repository>"
  [ -n "$BB_REPO" ] || stop "CHECK196_BITBUCKET must name the Bitbucket sandbox repository this installation serves: the check reads one repository of each provider, and G20's boundary — a build of one repository is served its own provider's credential and never the other's — needs both"
  guard_one_repo "$REPO" CHECK196_REPO
  guard_one_repo "$BB_REPO" CHECK196_BITBUCKET
  [ "$REPO" != "$BB_REPO" ] || stop "CHECK196_REPO and CHECK196_BITBUCKET name the same repository ($REPO): the two halves are a GitHub repository and a Bitbucket one, and one repository cannot be both"
  fact inputs PASS "GitHub ${REPO}, Bitbucket ${BB_REPO} (sandbox repositories of the fugaro-dev installation; never belong)"
}

# ---------------------------------------------------------------- the reading
BUILDS_FILE=""
JOBS_FILE=""
SERVICE_URL=""
SERVICE_DOC=""
SERVICE_ERR=""

# read_service: the credentials service ITSELF, once. The row this feeds is the
# service's own address, and reading it off a build — which is what this check
# did until 2026-11-24 — made the row conditional on a build existing: with no
# catalog build among the project's last builds the row was never reached and
# printed `FAIL — not reached`, on an installation whose service was serving
# runs perfectly well. The service is a resource of the installation and is read
# as one.
read_service() { # (SERVICE_DOC)
  SERVICE_DOC=$SCRATCH/service.json
  if ! gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" --format=json >"$SERVICE_DOC" 2>"$SCRATCH/service.err"; then
    SERVICE_ERR="the credentials service ${SVC} itself cannot be read: 'gcloud run services describe ${SVC} --project=${P} --region=${L} --format=json' failed with $(last_lines "$SCRATCH/service.err"). YOUR account needs run.services.get, which a project Owner has"
    return 1
  fi
  return 0
}

# read_builds: the REGION's recent builds, once, for both repositories. The
# region is not optional and there is no fallback to a global listing: a catalog
# build is a regional build (`gcloud builds submit --region`), so a global
# `gcloud builds list` holds none of them and would report every repository as
# having no build at all — the misreading this row is here to stop. `L` is a
# constant in this file, so "the region cannot be read" is a guard against the
# constant going missing rather than a state of the installation, and it stops
# the run instead of listing another region's builds.
read_builds() {
  BUILDS_FILE=$SCRATCH/builds.json
  [ -n "$L" ] || stop "the installation's region is empty, so the project's builds cannot be listed: 'gcloud builds list' with no --region reads the GLOBAL builds, which hold no catalog build at all (every catalog build is a regional one), and the check would report every repository as having none. This is a defect in the check itself: the region is a constant in this file (L)"
  if ! gcloud builds list --project="${P:?}" --region="$L" --limit="${BUILDS:?}" --format=json >"$BUILDS_FILE" 2>"$SCRATCH/builds.err"; then
    stop "the project's recent builds cannot be read: 'gcloud builds list --project=${P} --region=${L} --limit=${BUILDS} --format=json' failed with $(last_lines "$SCRATCH/builds.err"). YOUR account needs cloudbuild.builds.list in ${L}, which a project Owner has"
  fi
  case "$(head -c 1 "$BUILDS_FILE" 2>/dev/null || true)" in
  "[") ;;
  *) stop "the region's recent builds are not the JSON array this check reads (is this gcloud too old for --format=json?): $(last_lines "$SCRATCH/builds.err")" ;;
  esac
}

# project_config_file — this machine's own project config for the installation,
# the file whose `image_map` is the map's local half (localcfg's ImageMapEffective):
# $XDG_CONFIG_HOME/fugaro/projects/<name>.yaml, else ~/.config/fugaro/projects/<name>.yaml,
# and $FUGARO_CONFIG when the canonical path holds nothing (the same order
# localcfg's selector takes). A path that is not there is not a failure: the
# reader answers "the file states no map" and the published node's map stands.
project_config_file() {
  local dir cfg
  dir=${XDG_CONFIG_HOME:-}
  if [ -z "$dir" ]; then dir="${HOME:-}/.config"; fi
  cfg="$dir/fugaro/projects/${NAME}.yaml"
  if [ ! -f "$cfg" ] && [ -n "${FUGARO_CONFIG:-}" ]; then cfg=$FUGARO_CONFIG; fi
  printf '%s\n' "$cfg"
}

# read_image_map: the project's EFFECTIVE repo-to-image map (D1906), once, for
# both repositories. The local config's block wins when it states one — an empty
# block is a stated empty map and takes a published one away — and the published
# policy node's map stands otherwise, which is what a job reads. Both are read
# here and the keep rule is the reader's: a node that cannot be read is not a
# failure of the check, it is the local half answering alone (and no map at all
# when the file states none either, which the build rows then say).
IMAGE_MAP_FILE=""
IMAGE_MAP_READ=""
IMAGE_MAP_ERR=""
read_image_map() {
  IMAGE_MAP_FILE=$SCRATCH/imagemap
  local cfg policy=$SCRATCH/policy.json
  cfg=$(project_config_file)
  IMAGE_MAP_ERR=""
  if ! fugaro project policy show --json --project "${NAME:?}" >"$policy" 2>"$SCRATCH/policy.err"; then
    IMAGE_MAP_ERR="fugaro project policy show --json failed with $(last_lines "$SCRATCH/policy.err")"
    : >"$policy"
  fi
  if ! reader imagemap "$cfg" "$policy" >"$IMAGE_MAP_FILE" 2>"$SCRATCH/imagemap.err"; then
    stop "the project's image_map could not be read (the reader failed): $(last_lines "$SCRATCH/imagemap.err")"
  fi
  IMAGE_MAP_READ=$(field_of map-read "$(cat "$IMAGE_MAP_FILE")")
}

# map_entry_for REPO — the catalog entry the project's effective image_map names
# for a repository, or nothing when the map does not mention it (which is not a
# failure: a repository no map names keeps the `_REPO_URL` match, and a map the
# check could not read at all names nothing).
map_entry_for() { # map_entry_for REPO
  [ -f "$IMAGE_MAP_FILE" ] || return 0
  awk -F'|' -v r="$1" '$1 == "map=" r { print $2; exit }' "$IMAGE_MAP_FILE"
}

# map_phrase REPO — how a row says which answer found the repository's builds,
# in one clause: the entry its image_map names, or the `_REPO_URL` match and why
# the map did not answer.
map_phrase() { # map_phrase REPO ENTRY MATCH
  local repo=$1 entry=$2 match=$3
  if [ "$match" = map ]; then
    printf 'the catalog entry %s its image_map names' "$entry"
    return 0
  fi
  case "$IMAGE_MAP_READ" in
  none)
    printf 'the builds whose _REPO_URL is its own clone URL (the project image_map names no entry for %s, so the map did not select them%s)' \
      "$repo" "${IMAGE_MAP_ERR:+; $IMAGE_MAP_ERR}"
    ;;
  *)
    printf 'the builds whose _REPO_URL is its own clone URL (the project image_map does not name an entry for %s)' "$repo"
    ;;
  esac
}

# no_build_remedy REPO — why no build was found for a repository and the one
# command that submits it. A repository the map names has an entry to build and
# a command that names it; one the map does not name has to be mapped first, and
# the sentence says so rather than naming an entry the project never chose.
no_build_remedy() { # no_build_remedy REPO
  local repo=$1 entry
  entry=$(map_entry_for "$repo")
  if [ -n "$entry" ]; then
    printf "A repository-level derived image is disabled (D1900) and 'fugaro image refresh' is refused, so what a repository runs is the CATALOG ENTRY its image_map names — %s for %s — and this repository's builds are the newest whose _IMAGE names catalog-%s. Submit it with 'fugaro image build %s' in a checkout of the entry's source repository (or with 'fugaro apply', whose owed catalog-build step submits the same build) and run this check again" \
      "$entry" "$repo" "$entry" "$entry"
    return 0
  fi
  printf "The project's effective image_map names no entry for %s (read %s%s), so the check looked for the builds whose _REPO_URL is its own clone URL. A repository-level derived image is disabled (D1900) and 'fugaro image refresh' is refused, so what a repository runs is the CATALOG ENTRY its image_map names: map %s to one with 'fugaro image map set %s <entry>', submit it with 'fugaro image build <entry>' (or 'fugaro apply'), and run this check again" \
    "$repo" "${IMAGE_MAP_READ:-none}" "${IMAGE_MAP_ERR:+; $IMAGE_MAP_ERR}" "$repo" "$repo"
}

# read_jobs: the installation's Cloud Run jobs, once, for both repositories.
read_jobs() {
  JOBS_FILE=$SCRATCH/jobs.json
  if ! gcloud run jobs list --project="${P:?}" --region="${L:?}" --format=json >"$JOBS_FILE" 2>"$SCRATCH/jobs.err"; then
    stop "the installation's Cloud Run jobs cannot be read: 'gcloud run jobs list --project=${P} --region=${L} --format=json' failed with $(last_lines "$SCRATCH/jobs.err"). YOUR account needs run.jobs.list, which a project Owner has"
  fi
}

# read_build REPO KEY — read the repository's newest build into the scratch
# directory, and print the reader's answer. Cached so the two rows about one
# repository read it once. The catalog entry comes from the project's effective
# image_map (map_entry_for) and is what selects the builds; empty means the map
# names no entry for the repository and the `_REPO_URL` match stands.
read_build() { # read_build REPO KEY
  local key=$2 f
  f="$SCRATCH/builds-$key"
  if [ ! -f "$f" ]; then
    # A reader that failed returns non-zero and the CALLER stops the run: a
    # `stop` in here would run inside the command substitution its callers read
    # it through, which would end that subshell rather than this check and reach
    # the owner as "an internal error ended this check" instead of as the one
    # reason it could not measure.
    reader builds "$BUILDS_FILE" "$1" "$(map_entry_for "$1")" >"$f" 2>"$SCRATCH/builds-$key.err" || return 1
  fi
  cat "$f"
}

# build_read REPO KEY — read_build, with a reader that failed stopping the run.
build_read() { # build_read REPO KEY
  local ans
  if ! ans=$(read_build "$1" "$2"); then
    stop "the region's own builds could not be read (the reader failed on ${1}): $(last_lines "$SCRATCH/builds-$2.err")"
  fi
  printf '%s\n' "$ans"
}

# read_job REPO KEY — read the reader's answer for one repository's daily check
# job, cached so the rows about one repository read it once. The job is found by
# the repository itself (`<workspace>/<repository>`) and not by a slug out of a
# build's substitution: a catalog build carries no `_SLUG`, and the job's own
# `fugaro_repo` label is where a repository's slug is read from.
read_job() {
  local key=$2 f
  f="$SCRATCH/job-$key"
  if [ ! -f "$f" ]; then
    reader job "$JOBS_FILE" "$1" >"$f" 2>"$SCRATCH/job-$key.err" || return 1
  fi
  cat "$f"
}

# job_read REPO KEY — read_job, with a reader that failed stopping the run.
job_read() {
  local ans
  if ! ans=$(read_job "$1" "$2"); then
    stop "the installation's Cloud Run jobs could not be read (the reader failed on ${1}): $(last_lines "$SCRATCH/job-$2.err")"
  fi
  printf '%s\n' "$ans"
}

# repo_slug BUILD_SLUG LABEL REPO — the key a repository's record and the
# service's own lines are written under (G20). A derived build carries it as its
# own `_SLUG` substitution; a CATALOG build carries none, and the same slug is
# written on the repository's daily check job as its `fugaro_repo` label, which
# is where this reading takes it from then. `-` when neither answers.
repo_slug() { # repo_slug BUILD_SLUG LABEL REPO
  local slug=$1 ans
  case "$slug" in "" | "-") ;; *) printf '%s\n' "$slug"; return 0 ;; esac
  ans=$(job_read "$3" "$2")
  if [ "$(field_of found "$ans")" = yes ]; then
    slug=$(field_of slug "$ans")
    case "$slug" in "" | "-") ;; *) printf '%s\n' "$slug"; return 0 ;; esac
  fi
  printf '%s\n' -
}

# is_build_account ACCOUNT — whether a Cloud Build serviceAccount field names a
# repository's build account of THIS installation. The field is reduced to the
# address it ends in by the reader; the account is a repository's build
# (gcp.BuildServiceAccountID) when it carries the `fugaro-b-` prefix and the
# installation's own domain.
is_build_account() {
  case "$1" in
  "${BUILD_ACCOUNT_PREFIX}"*"@${P}.iam.gserviceaccount.com") return 0 ;;
  esac
  return 1
}

# is_build_identity ACCOUNT — whether the field names the installation's OWN
# build identity, `fugaro-build-identity@<project>.iam.gserviceaccount.com`
# (gcp.BuildIdentityAccountID, B6/D2301). It is the ONE account a CATALOG build
# runs as — gcp.CatalogBuildSpec.check refuses any other, so neither a
# repository's build account nor a retired root's account is ever the one a
# catalog build runs as — and it is project-wide rather than one repository's.
is_build_identity() {
  case "$1" in
  "${BUILD_IDENTITY_ID}@${P}.iam.gserviceaccount.com") return 0 ;;
  esac
  return 1
}

# git_env_name PROVIDER — the variable the daily check job's OWN mount of the
# repository's git credential lands in. A repository the service does not yet
# serve for its BUILDS keeps that mount: the job runs as the build account, and
# the accessor the mount needs is that account's, so the mount leaves only with
# the flip (check.tf's check_mount_served, B4.9). The variable is the platform's
# reserved name for the provider's git secret (config.ReservedSecrets), and
# whether it is among the job's Secret Manager mounts is what says the job still
# holds the credential itself.
git_env_name() { # git_env_name PROVIDER
  case "$1" in
  github) printf '%s\n' FUGARO_GITHUB_APP_PRIVATE_KEY ;;
  bitbucket) printf '%s\n' FUGARO_BITBUCKET_TOKEN ;;
  *) return 1 ;;
  esac
}

# mint_env_name PROVIDER — the env a CATALOG build's request feeds its own
# provider git credential into, which is what a catalog build clones with: the
# request mounts the credential the BUILD IDENTITY holds the accessor for
# (gcp.CatalogBuildSpec.AvailableSecrets, `credentialSecretEnv` in
# internal/backend/gcp/build.go). It is NOT the job's reserved name above: the
# job's mount lands in FUGARO_GITHUB_APP_PRIVATE_KEY / FUGARO_BITBUCKET_TOKEN,
# while a build asks Cloud Build for the secret under GITHUB_APP_KEY / GIT_TOKEN.
mint_env_name() { # mint_env_name PROVIDER
  case "$1" in
  github) printf '%s\n' GITHUB_APP_KEY ;;
  bitbucket) printf '%s\n' GIT_TOKEN ;;
  *) return 1 ;;
  esac
}

# audit_read SLUG — the service's own `credsvc: audit` and `credsvc: refused`
# lines for ONE repository, retried while Cloud Logging catches up. Ingestion
# lags the call by seconds, and a reading taken immediately is an empty answer
# about a build that did report.
audit_read() { # audit_read SLUG
  local slug=$1 waited=0 ans=""
  local filter="resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${SVC}\" jsonPayload.purpose=\"build\" jsonPayload.repository=\"${slug}\""
  while :; do
    if gcloud logging read "$filter" --project="${P:?}" --freshness="${LOG_AGE:?}" \
      --limit="${LOG_LIMIT:?}" --format=json >"$SCRATCH/audit-$slug.json" 2>"$SCRATCH/audit-$slug.err"; then
      ans=$(reader audit "$SCRATCH/audit-$slug.json" 2>/dev/null || true)
      # Ingestion lags the call by seconds: an empty answer right after a build
      # is an answer about Cloud Logging rather than about the service, so the
      # read is repeated while there is time left.
      if [ -n "$ans" ] || [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
        printf '%s\n' "$ans"
        return 0
      fi
    elif [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
      # A read that FAILED is not the answer "there is no line": the caller
      # tells them apart by this status and the sentence beside it.
      return 1
    fi
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
}

# ------------------------------------------------------------------- the steps
# step_service: the service ITSELF — that the installation's credentials service
# is deployed and that Cloud Run serves it at an address. It is read off the
# service and off nothing else, deliberately: this row is the one part of the
# reading that is independent of every build, so a project whose last builds
# hold no catalog build still measures it (and a build that names no service —
# which is every catalog build, B4.5 — cannot make it FAIL).
step_service() {
  local ans name url
  if ! read_service; then
    unmeasured service "$SERVICE_ERR"
    return 0
  fi
  ans=$(reader svc "$SERVICE_DOC" 2>"$SCRATCH/service-read.err") \
    || { unmeasured service "the credentials service's own document cannot be read: $(last_lines "$SCRATCH/service-read.err")"; return 0; }
  if [ "$(field_of found "$ans")" != yes ]; then
    fact service FAIL "the credentials service ${SVC} is not deployed in ${P} (Cloud Run has no service of that name in ${L}): every caller's address is empty, so no build, job or run of a switched repository has a service to fetch a credential from. Deploy it with 'fugaro update' at the installation's own checkout"
    return 0
  fi
  name=$(field_of name "$ans")
  url=$(field_of url "$ans")
  if [ -z "$url" ] || [ "$url" = "-" ]; then
    fact service FAIL "the credentials service ${name:-$SVC} is deployed in ${P} but Cloud Run serves it no URL (status.url is empty), so no caller can mint an ID token for it: every credential a switched repository's build, job or run fetches would be refused before the service answers. Read it with 'gcloud run services describe ${SVC} --project=${P} --region=${L}'"
    return 0
  fi
  SERVICE_URL=$url
  fact service PASS "the credentials service is at ${url} (Cloud Run service ${name:-$SVC} in ${P}/${L}, read off the service itself and not off any build): it is deployed and reachable, which is the address every caller mints its Google ID token for. Whether a build names it is a separate row: a catalog entry's build names none by design (_CREDENTIALS_SERVICE_URL is empty for every catalog build, B4.5) and clones with the credential its request mounts"
}

# step_repo LABEL REPO PROVIDER — the image one sandbox repository builds (the
# catalog entry its image_map names), the credential that build mounts, and the
# service's own lines for the repository.
step_repo() { # step_repo LABEL REPO PROVIDER
  local label=$1 repo=$2 provider=$3 ans
  ans=$(build_read "$repo" "$label")
  local found id status account entry image src_repo match containers url slug cenv
  found=$(field_of found "$ans")
  if [ "$found" != yes ]; then
    unmeasured "build-$label" "no CATALOG-ENTRY build of ${repo} is among the ${L} region's last ${BUILDS} builds, so the image this repository runs could not be read. $(no_build_remedy "$repo")"
    unmeasured "mounts-$label" "no catalog-entry build of ${repo} was read, so the credential its request mounts could not be measured"
    unmeasured "served-$label" "no catalog-entry build of ${repo} was read, so the service's own lines for it could not be measured"
    return 0
  fi
  id=$(field_of id "$ans")
  status=$(field_of status "$ans")
  account=$(field_of account "$ans")
  entry=$(field_of entry "$ans")
  image=$(field_of image "$ans")
  src_repo=$(field_of source "$ans")
  match=$(field_of match "$ans")
  containers=$(field_of containers "$ans")
  url=$(field_of url "$ans")
  slug=$(repo_slug "$(field_of slug "$ans")" "$label" "$repo")
  cenv=$(mint_env_name "$provider")

  # build-LABEL: the image this repository really runs — the CATALOG ENTRY its
  # image_map names, baked from the ENTRY's source repository (the checkout the
  # build was submitted from, which is not the served repository when one entry
  # serves several) — ended SUCCESS and ran as the installation's build identity,
  # which is the account a catalog build runs as and the only one
  # gcp.CatalogBuildSpec.check admits (B6/D2301). It names no credentials
  # service, and that is not a failure: the substitution is empty for every
  # catalog build (B4.5), and the credential is the one the request mounts (the
  # row below reads it).
  if [ "$status" != SUCCESS ]; then
    fact "build-$label" FAIL "the newest catalog build of ${repo} (${id}, the entry ${entry:-unknown}'s image, found by $(map_phrase "$repo" "$entry" "$match")) ended ${status}, not SUCCESS: $(read_audit_said "$slug") . Read it with 'gcloud builds log ${id} --project=${P} --region=${L}'. A catalog entry's build clones with the credential its request mounts as availableSecrets and bakes from the entry's source repository (${src_repo:-unknown}), so a build that cannot finish is that mount, the entry's definition or that checkout"
  elif ! is_build_identity "$account"; then
    fact "build-$label" FAIL "the newest catalog build of ${repo} (${id}, entry ${entry}) ended SUCCESS but its own record says it ran as ${account:-nobody this reading can name}, not as the installation's build identity (${BUILD_IDENTITY_ID}@${P}.iam.gserviceaccount.com): a catalog entry's build runs as the project's build identity and never as a repository's build account, and a build that ran as another identity is not a statement about this installation's entry builds"
  else
    fact "build-$label" PASS "the newest build of ${repo} is the CATALOG ENTRY ${entry}'s image (${image}, build ${id}, found by $(map_phrase "$repo" "$entry" "$match")): it ended SUCCESS and ran as ${account}, the installation's build identity — so the image this repository runs really is the entry its image_map names, baked from that entry's source repository (${src_repo:-unknown}), and the account is the one gcp.CatalogBuildSpec.check admits. Its request names NO credentials service: _CREDENTIALS_SERVICE_URL is empty for every catalog build (B4.5), because the build's identity is not one the build-credentials route admits and it clones with the credential its request mounts instead"
  fi

  # mounts-LABEL: what a catalog build clones with. The request mounts exactly
  # one Secret Manager container — the provider's git credential — and feeds it
  # to the build step as GITHUB_APP_KEY (GitHub) or GIT_TOKEN (Bitbucket)
  # (gcp.CatalogBuildSpec.AvailableSecrets). It is the one credential this build
  # has, and B4.9's flip does not take it away: the build runs as the project's
  # build identity, which the build-credentials route does not admit, so its
  # accessor and its mount stay (docs/secrets-brokering's build classes note).
  case "$containers" in
  "" | *[!0-9]*) unmeasured "mounts-$label" "the reader did not answer how many containers the build's availableSecrets names: $(last_lines "$SCRATCH/builds-$label.err")" ;;
  0)
    fact "mounts-$label" FAIL "the newest catalog build of ${repo} (${id}) carries no availableSecrets and names no credentials service: it holds no credential at all, so its clone can only fail — a catalog build clones with the provider credential its request MOUNTS, and a request with neither a mount nor a service is one no submitter of this installation writes. Submit the entry again with 'fugaro image build ${entry}' (or 'fugaro apply')"
    ;;
  *)
    if printf '%s\n' "$ans" | grep -q "^container=${cenv}|"; then
      fact "mounts-$label" PASS "the newest catalog build of ${repo} (${id}, entry ${entry}) carries ${containers} availableSecrets entry(ies) and one of them is the ${provider} clone credential, fed to the build step as ${cenv} ($(printf '%s\n' "$ans" | sed -n "s/^container=${cenv}|//p" | head -n 1)): the image is baked with the credential the request mounts — the one path a catalog build has, since its identity is not one the build-credentials route admits and B4.9's flip therefore leaves this mount in place"
    else
      fact "mounts-$label" FAIL "the newest catalog build of ${repo} (${id}) carries ${containers} availableSecrets entry(ies) and none of them is the ${provider} clone credential the build clones with (${cenv}): the mounts it does carry read $(printf '%s\n' "$ans" | sed -n 's/^container=/&/p' | tr '\n' ' '). A catalog entry's build mounts exactly the provider's git credential (gcp.CatalogBuildSpec.AvailableSecrets)"
    fi
    ;;
  esac

  # served-LABEL: the service's own lines for this repository. A build of a
  # SERVED repository asks the build route once, and the line names the caller
  # the service VERIFIED (the account Cloud Run minted the build's ID token for,
  # G20 — the one field a call cannot claim for itself), the credential it was
  # served and the purpose. A catalog build is not served and asks nothing, so
  # the row reads what the service wrote and says which of the two it is rather
  # than calling the absence a failure.
  if [ -z "$slug" ] || [ "$slug" = "-" ]; then
    unmeasured "served-$label" "the repository's slug could not be read — neither from the build's _SLUG substitution (a catalog build carries none) nor from the fugaro_repo label of its daily check job — so the repository the service's own lines name cannot be looked up"
  else
    step_served "served-$label" "$repo" "$slug" "$account" "$provider" "$url"
  fi
}

# credential_matches PROVIDER NAME — whether the credential a line names is the
# one this provider's build is served. A GitHub build is served the App key by
# its own name; a Bitbucket build the bundle's token, whose name carries which
# token of the family it is (`bitbucket-token`, or `bitbucket-token--<name>`).
credential_matches() {
  case "$1" in
  github) [ "$2" = github-app-key ] ;;
  bitbucket) case "$2" in bitbucket-token | bitbucket-token--*) return 0 ;; esac; return 1 ;;
  *) return 1 ;;
  esac
}

# read_audit_said SLUG — the service's own refusal sentence for a repository, on
# one line, redacted, or nothing.
read_audit_said() { # read_audit_said SLUG
  [ -f "$SCRATCH/audit-$1.json" ] || return 0
  reader audit "$SCRATCH/audit-$1.json" 2>/dev/null | sed -n 's/^refused=[^|]*|//p' | head -n 1 | redact | cut -c1-300
}

# step_served KEY REPO SLUG ACCOUNT PROVIDER URL — the service's own lines. The
# credential a line must name is the provider's GIT credential and nothing else
# (PurposeBuild): `github-app-key`, the App key a GitHub build signs with, or a
# `bitbucket-token…` — the installation's bundle token, whose name carries which
# token of the family it is (bbtokens.Name), so the reading takes the family
# rather than one spelling of it and a typed token is not read as a wrong one.
# URL is the build's own `_CREDENTIALS_SERVICE_URL` reading, which decides how
# an empty answer is read: a build that names no service is expected to have no
# line (INFO), and one that names the service and still has none is a failure.
step_served() {
  local key=$1 repo=$2 slug=$3 account=$4 provider=$5 url=$6 ans lines
  local cred
  if [ "$provider" = github ]; then cred=github-app-key; else cred="bitbucket-token…"; fi
  if ! ans=$(audit_read "$slug"); then
    unmeasured "$key" "the service's own lines for ${repo} could not be read (the log read failed): yours needs roles/logging.viewer on ${P}. Read them by hand with 'gcloud logging read \"resource.labels.service_name=${SVC} jsonPayload.repository=${slug}\" --project=${P}'"
    return 0
  fi
  lines=$(printf '%s\n' "$ans" | sed -n 's/^audit=ok|//p' | wc -l | tr -d ' ')
  if [ "${lines:-0}" -eq 0 ]; then
    local refused
    refused=$(printf '%s\n' "$ans" | sed -n 's/^refused=[^|]*|//p' | head -n 1 | redact | cut -c1-300)
    if [ "$url" != present ] && [ -z "$refused" ]; then
      # The build the row is about is a CATALOG entry's: it names no credentials
      # service (B4.5) and asks the service for nothing, so no line is the
      # answer this installation's shape predicts, and a FAIL here would end the
      # switch mode on a working installation. What the row still settles is
      # that nothing was refused — a `credsvc: refused` line under this
      # repository's slug is the service's own statement and is a FAIL above —
      # and that no call of this repository's was answered.
      fact "$key" INFO "the newest build of ${repo} is a catalog entry's, and the service wrote no 'credsvc: audit' line of purpose build for it (slug ${slug}) — which is what this installation's shape predicts: a catalog build names NO credentials service (_CREDENTIALS_SERVICE_URL is empty, B4.5) and clones with the credential its request mounts, so it never asks the build route at all, and the route serves one repository to the account bound to that repository — never the project's build identity a catalog build runs as (D2301).${refused:+ A refused line of this repository does read: ${refused}.} Read the lines with 'gcloud logging read \"resource.labels.service_name=${SVC} jsonPayload.repository=${slug}\" --project=${P}'"
      return 0
    fi
    fact "$key" FAIL "the service wrote no 'credsvc: audit' line of purpose build for ${repo} (slug ${slug}) although the build's own request names the service: the build's credential was answered by something else, or the build's calls were refused.${refused:+ Its own refusal line reads: ${refused}.} Read the service's lines with 'gcloud logging read \"resource.labels.service_name=${SVC} jsonPayload.repository=${slug}\" --project=${P}'"
    return 0
  fi
  local got
  got=$(printf '%s\n' "$ans" | sed -n 's/^audit=ok|//p' | head -n 1)
  local acred acaller arepo
  acred=$(printf '%s' "$got" | cut -d'|' -f1)
  acaller=$(printf '%s' "$got" | cut -d'|' -f3)
  arepo=$(printf '%s' "$got" | cut -d'|' -f4)
  if [ "$acaller" != "$account" ]; then
    fact "$key" FAIL "the service's own line for ${repo} names the caller ${acaller:-nobody}, not this repository's build account ${account}: the credential was served to another identity, which is the one field a call cannot claim for itself (G20). Read the lines by hand"
  elif ! credential_matches "$provider" "$acred"; then
    fact "$key" FAIL "the service's own line for ${repo} names the credential ${acred:-none}, not the ${provider} build's own git credential (${cred}): a build got a credential that is not its own repository's"
  elif [ -n "$arepo" ] && [ "$arepo" != "$slug" ]; then
    fact "$key" FAIL "the service's own line for ${repo} names the repository ${arepo}, not this repository's slug ${slug}: a build was served another repository's record"
  else
    fact "$key" PASS "the service wrote ${lines} 'credsvc: audit' line(s) of purpose build for ${repo} (slug ${slug}), served to ${account} as ${acred}: the build's credential came from the service and the caller the service verified is this repository's own build account"
  fi
}

# step_check_repo LABEL REPO PROVIDER — the repository's daily check job, found
# by the repository ITSELF: by the clone URL its own containers carry as
# FUGARO_REMOTE, and by the `fugaro_repo` label the installation writes on it —
# which is where this row reads the repository's SLUG from, since a catalog
# build carries no `_SLUG` substitution to read one out of. The row therefore
# does not depend on the build rows at all: a project whose last builds hold no
# catalog build still measures its daily check.
step_check_repo() { # step_check_repo LABEL REPO PROVIDER
  step_check "$1" "$2" "$3"
}

# step_check LABEL REPO PROVIDER — the daily image check in the switch mode,
# which is the shape the installation plans before the flip: the repository's
# own check job runs as a repository build account and still mounts the
# repository's git credential, and it carries NO service address — the address
# arrives with the mount's removal, because a job that mounted nothing and named
# no service would hold no credential at all (internal/infra/spec.go's
# checkMountServed, which both this environment and the job's own mount read).
# Its newest execution succeeded.
step_check() { # step_check LABEL REPO PROVIDER
  local label=$1 repo=$2 provider=$3 slug ans
  ans=$(job_read "$repo" "$label")
  if [ "$(field_of found "$ans")" != yes ]; then
    # Absent, and that is not a failure a catalog repository can be held to: a
    # repository-level image check is disabled with the repository-level images
    # (D1900, internal/infra's IS2), so the installation emits no `fugarochk-`
    # job for a repository that has none — the image a repository runs is its
    # catalog entry's, and the entry's build is the build-* row above. A
    # repository that already had a job keeps it (IS2 retires nothing), and the
    # rows below read that one: this INFO is only the absence.
    fact "check-$label" INFO "this installation deploys no daily check job (a job named ${CHECK_JOB_PREFIX}…) for ${repo}, and that is the shape D1900 (internal/infra's IS2) gives a repository that has none: a repository-level image check is disabled with the repository-level images, so the image this repository runs is its catalog entry's and the build-* row above is what reads it. Nothing here is measured, and nothing is claimed about a job there is not: if this repository should have one — it had one before and it is gone, since IS2 retires nothing — re-apply the root with 'fugaro update --repo' in a checkout of ${repo}"
    return 0
  fi
  slug=$(field_of slug "$ans")
  local name sa url mounts m gitenv
  name=$(field_of name "$ans")
  sa=$(field_of account "$ans")
  url=$(field_of url "$ans")
  mounts=$(field_of mounts "$ans")
  gitenv=$(git_env_name "$provider")
  # Each count is held to being a number on its own: a `case` over "$url:$mounts"
  # would read the separator as a non-digit and call every answer unmeasured.
  case "$mounts" in "" | *[!0-9]*) m=unreadable ;; *) m=$mounts ;; esac
  case "$url:$m" in
  present:0)
    # The post-flip job read in the switch mode: the mount is gone and the
    # address is there. Not a failure — the mode that reads this shape is
    # --after-flip, exactly as mounts-LABEL reads a post-flip BUILD.
    fact "check-$label" INFO "the daily check job ${name} of ${repo} carries FUGARO_CREDENTIALS_SERVICE_URL and mounts no Secret Manager container: the S4 flip (B4.9) has already landed for it, so this mode is reading a post-flip installation. The --after-flip mode is the one to run"
    return 0
    ;;
  present:*)
    if [ "$m" = unreadable ]; then
      unmeasured "check-$label" "how many Secret Manager containers the check job ${name} mounts could not be read, so its shape — a post-flip job, or a mount beside an address — could not be told"
    else
      fact "check-$label" FAIL "the daily check job ${name} of ${repo} carries FUGARO_CREDENTIALS_SERVICE_URL AND mounts ${m} Secret Manager container(s): the address is written only where the mount has been taken away (internal/infra/spec.go's checkMountServed, B4.9), so this job's shape is one the installation never plans. Re-apply the root ('fugaro update --repo')"
    fi
    return 0
    ;;
  absent:0)
    fact "check-$label" FAIL "the daily check job ${name} of ${repo} mounts no Secret Manager container and carries no FUGARO_CREDENTIALS_SERVICE_URL: a job with neither a mount nor a service holds no credential at all, so the image check can only fail. Re-apply the root ('fugaro update --repo')"
    return 0
    ;;
  absent:unreadable)
    unmeasured "check-$label" "how many Secret Manager containers the check job ${name} mounts could not be read, so whether it holds a credential of its own at all is unmeasured"
    return 0
    ;;
  esac
  # absent:N (N > 0): the shape this mode reads. The job must run as a
  # repository's build account of THIS installation — the mount's accessor is
  # that account's — and the repository's own git credential must be among what
  # it mounts. The account is read off the JOB and held to the `fugaro-b-`
  # prefix, never to the account a build ran as: a repository's newest build is
  # now its CATALOG entry's and runs as the project's build identity
  # (fugaro-build-identity@…, D2301), which is not the account the daily check
  # job runs as.
  if ! is_build_account "$sa"; then
    fact "check-$label" FAIL "the daily check job ${name} of ${repo} runs as ${sa:-nobody this reading can name}, not as a repository's build account of ${P} (${BUILD_ACCOUNT_PREFIX}…@${P}.iam.gserviceaccount.com): the mount the check reads its git credential from is one that account's accessor alone opens"
    return 0
  fi
  if ! printf '%s\n' "$ans" | grep -q "^mount=${gitenv}|"; then
    fact "check-$label" FAIL "the daily check job ${name} of ${repo} mounts ${m} Secret Manager container(s) and none of them is the repository's git credential (${gitenv}): the credential the check clones with is not the one its own job mounts. Re-apply the root ('fugaro update --repo')"
    return 0
  fi
  # The newest execution: the daily check succeeded with the credential its own
  # job mounts, before the flip takes the mount away.
  local ex
  if ex=$(gcloud run jobs executions list --project="${P:?}" --region="${L:?}" --job="$name" --limit=1 --format=json 2>"$SCRATCH/exec-$label.err"); then
    ex=$(printf '%s' "$ex" | reader exec /dev/stdin)
    local est
    est=$(field_of state "$ex")
    if [ "$est" = True ] || [ "$est" = true ]; then
      fact "check-$label" PASS "the daily check job ${name} of ${repo} (slug ${slug}) runs as the repository's build account ${sa}, mounts the repository's git credential (${gitenv}) and carries no FUGARO_CREDENTIALS_SERVICE_URL — the pre-flip shape, and the one the flip replaces with the service's address — and its newest execution ended completed: the image check still succeeds with the mount, so the flip is the only thing left to read"
    else
      fact "check-$label" FAIL "the daily check job ${name} of ${repo} runs as ${sa} and mounts the repository's git credential, but its newest execution's Completed condition reads ${est:-nothing}: the image check did not succeed. Read it with 'gcloud run jobs executions list --job=${name} --region=${L} --project=${P}'"
    fi
  else
    unmeasured "check-$label" "the check job ${name} of ${repo} mounts the repository's git credential (${gitenv}) and runs as ${sa}, but its executions could not be read: $(last_lines "$SCRATCH/exec-$label.err")"
  fi
}

# step_boundary: the service's own IAM policy — B4.4's grant is the front door,
# and a build account that is not a repository's build of this installation (an
# identity OTHER than a repository's build) is refused there before the route's
# own check. This is the plan's "another repository's build account is refused"
# read where it can be read from the outside: the grant names repository build
# accounts and no public member.
step_boundary() {
  local doc="$SCRATCH/serviceiam.json"
  if ! gcloud run services get-iam-policy "${SVC:?}" --project="${P:?}" --region="${L:?}" --format=json >"$doc" 2>"$SCRATCH/serviceiam.err"; then
    unmeasured boundary "the credentials service's own IAM policy could not be read: $(last_lines "$SCRATCH/serviceiam.err"). YOUR account needs run.services.getIamPolicy, which a project Owner has"
    return 0
  fi
  local ans ok=yes bad="" members=0
  ans=$(reader svciam "$doc")
  local line
  while IFS= read -r line; do
    case "$line" in
    binding=*) ;;
    *) continue ;;
    esac
    local member
    member=$(printf '%s' "$line" | cut -d'|' -f2)
    members=$((members + 1))
    case "$member" in
    allUsers | allAuthenticatedUsers)
      ok=no
      bad="${bad}${bad:+, }${member} (public)"
      ;;
    "${BUILD_ACCOUNT_PREFIX}"*"@${P}.iam.gserviceaccount.com") ;;
    *)
      # Every other member is reported, and only the PUBLIC ones are a FAIL: a
      # person or the coordinator holding the invoker role is not a build
      # account and not a public member, and this row is about the front door a
      # build account reaches — not about who else may invoke the service.
      bad="${bad}${bad:+, }${member}"
      ;;
    esac
  done <<<"$ans"
  if [ "$ok" = yes ]; then
    fact boundary PASS "the credentials service's own IAM policy admits repository build accounts of ${P} (${BUILD_ACCOUNT_PREFIX}…@${P}.iam.gserviceaccount.com) and names no allUsers and no allAuthenticatedUsers member: an identity that is not a repository's build account — another repository's build that holds no invoker here, or any caller that is not a build at all — is refused at Cloud Run's front door before the service's own check (B4.4). Members read: ${bad:-none outside the build accounts}${members:+ (${members} binding member(s))}"
  else
    fact boundary FAIL "the credentials service's own IAM policy names a public member (allUsers or allAuthenticatedUsers), so an identity that is not a repository's build account is not refused at the front door: ${bad}. Read it with 'gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L}'"
  fi
}

# ------------------------------------------------------------ the flip's steps
# step_flip_repo LABEL REPO PROVIDER — the flip: the accessor and the mounts are
# gone from the repository's own JOB, and a build still bakes. For a repository
# whose image is a CATALOG ENTRY the build half of the flip does not apply, and
# this mode reads that too rather than failing an installation that is right:
# B4.9's class list takes the mounts off a repository's own build, and a catalog
# build is not one — it runs as the project's build identity, which the
# build-credentials route does not admit (D2301) — so it keeps the one mount it
# clones with and names no service before and after. What the flip really
# changes for such a repository is its daily check job, which is the
# noaccessor-LABEL row below.
step_flip_repo() { # step_flip_repo LABEL REPO PROVIDER
  local label=$1 repo=$2 provider=$3 ans
  ans=$(build_read "$repo" "$label")
  if [ "$(field_of found "$ans")" != yes ]; then
    unmeasured "noavailable-$label" "no CATALOG-ENTRY build of ${repo} is among the ${L} region's last ${BUILDS} builds, so what the flip left on this repository's build could not be read. $(no_build_remedy "$repo")"
    unmeasured "build-flip-$label" "no catalog-entry build of ${repo} was read"
    unmeasured "served-$label-flip" "no catalog-entry build of ${repo} was read"
    unmeasured "noaccessor-$label" "no catalog-entry build of ${repo} was read"
    return 0
  fi
  local id status account entry src_repo match slug url containers envcount
  id=$(field_of id "$ans")
  status=$(field_of status "$ans")
  account=$(field_of account "$ans")
  entry=$(field_of entry "$ans")
  src_repo=$(field_of source "$ans")
  match=$(field_of match "$ans")
  url=$(field_of url "$ans")
  containers=$(field_of containers "$ans")
  envcount=$(field_of secretenv "$ans")
  slug=$(repo_slug "$(field_of slug "$ans")" "$label" "$repo")

  # noavailable-LABEL: what the flip left on this repository's BUILD. A
  # repository whose image is a catalog entry keeps its one mount: the flip's
  # class list reaches a repository's OWN build, and this build is not one. A
  # request that carries NEITHER a mount NOR a service is the shape no submitter
  # of this installation writes (a build with no credential could only fail),
  # and that is the FAIL. Each count is held to being a number on its own: a
  # `case` over "$a:$b" would read the separator as a non-digit and call every
  # answer unmeasured.
  case "$containers" in "" | *[!0-9]*) containers=unreadable ;; esac
  case "$envcount" in "" | *[!0-9]*) envcount=unreadable ;; esac
  case "$containers:$envcount" in
  *unreadable*) unmeasured "noavailable-$label" "the reader did not answer how many containers and secretEnv entries the build carries: $(last_lines "$SCRATCH/builds-$label.err")" ;;
  0:0)
    if [ "$url" != present ]; then
      fact "noavailable-$label" FAIL "the newest catalog build of ${repo} (${id}) carries no availableSecrets and names no credentials service either: a build with neither a mount nor a service has no credential at all and could only fail. A catalog entry's build clones with the credential its request mounts (gcp.CatalogBuildSpec.AvailableSecrets) — submit the entry again with 'fugaro image build ${entry}' (or 'fugaro apply')"
    else
      fact "noavailable-$label" PASS "the newest build of ${repo} (${id}) names the credentials service and carries 0 availableSecrets and 0 secretEnv entries: the request asks Cloud Build for no secret, whose accessor the build account has given up (B4.9, D2077)"
    fi
    ;;
  *)
    if [ "$url" = present ]; then
      fact "noavailable-$label" FAIL "the newest build of ${repo} (${id}) names the credentials service and still carries ${containers} availableSecrets entries and ${envcount} secretEnv entries, so the request names the secrets the flip was to take away. The flip has not reached this repository's builds — check the class list (config.BuildServedSecretClasses) and re-apply the root"
    else
      fact "noavailable-$label" PASS "the newest build of ${repo} is the CATALOG ENTRY ${entry}'s image (${id}) and still carries ${containers} availableSecrets entry(ies) (${envcount} secretEnv) while naming no service: B4.9's flip does not reach a catalog entry's build — it runs as the project's build identity, which the build-credentials route does not admit (D2301) — so the one mount it clones with stays, before and after the flip, and this repository's own half of the flip is its daily check job (the noaccessor row below)"
    fi
    ;;
  esac

  # build-flip-LABEL: a build after the flip still succeeds and still runs as
  # the account a catalog build runs as.
  if [ "$status" = SUCCESS ] && is_build_identity "$account"; then
    fact "build-flip-$label" PASS "the newest build of ${repo} is the CATALOG ENTRY ${entry}'s image (${id}, found by $(map_phrase "$repo" "$entry" "$match")): it ended SUCCESS and ran as ${account}, the installation's build identity, with the credential its request mounts — so the image this repository runs still bakes, and nothing the flip took was a path this build used"
  else
    fact "build-flip-$label" FAIL "the newest catalog build of ${repo} (${id}, entry ${entry:-unknown}) did not read as a success of a catalog build: status ${status}, account ${account:-none}, service ${url}. A catalog entry's build runs as the installation's build identity (${BUILD_IDENTITY_ID}@${P}.iam.gserviceaccount.com) and clones with the credential its request mounts, so a build that could not finish is that mount, the entry's definition or the entry's source repository (${src_repo:-unknown}) having broken"
  fi

  # served-flip-LABEL: the service's own lines for that build.
  if [ -z "$slug" ] || [ "$slug" = "-" ]; then
    unmeasured "served-$label-flip" "the repository's slug could not be read (a catalog build carries no _SLUG, and no check job label answered), so the service's own lines for it cannot be looked up"
  else
    step_served "served-$label-flip" "$repo" "$slug" "$account" "$provider" "$url"
  fi

  # noaccessor-LABEL: the daily check job mounts nothing AND is handed the
  # service's address, which are the flip's other half (B4.9 drops the check
  # job's secret_key_ref mounts, and internal/infra/spec.go writes the address in
  # the same breath). The two go together: with the build account's accessor gone
  # a job that mounted nothing and named no service would hold no credential at
  # all, so both are read here.
  local jans
  if jans=$(job_read "$repo" "$label"); then
    if [ "$(field_of found "$jans")" != yes ]; then
      # Absent, and the flip had nothing to reach: a repository-level image
      # check is disabled with the repository-level images (D1900, IS2), so a
      # repository that has no `fugarochk-` job is one the installation emits
      # none for, and there is no mount of its to read away. The same INFO the
      # switch mode's check-* row reads, for the same reason.
      fact "noaccessor-$label" INFO "this installation deploys no daily check job of ${repo} (a job named ${CHECK_JOB_PREFIX}…), so there is no mount of this repository's for the flip to have taken away — the shape D1900 (internal/infra's IS2) gives a repository that has none, since a repository-level image check is disabled with the repository-level images. Nothing here is measured: if this repository should have one — it had one before and it is gone, since IS2 retires nothing — re-apply the root with 'fugaro update --repo'"
    else
      local jm ju jname
      jname=$(field_of name "$jans")
      jm=$(field_of mounts "$jans")
      ju=$(field_of url "$jans")
      case "$jm" in
      "" | *[!0-9]*) unmeasured "noaccessor-$label" "how many secret mounts the check job ${jname} of ${repo} carries could not be read" ;;
      0)
        if [ "$ju" != present ]; then
          fact "noaccessor-$label" FAIL "the daily check job ${jname} of ${repo} mounts no Secret Manager container but carries no FUGARO_CREDENTIALS_SERVICE_URL either: with the build account's accessor gone (B4.9) the job holds no credential at all, so its check can only fail. The mount and the address go together — re-apply the root ('fugaro update --repo')"
        else
          fact "noaccessor-$label" PASS "the daily check job ${jname} of ${repo} (slug ${slug}) carries 0 Secret Manager mounts and names the credentials service (${ju}): with the build account's accessor gone (B4.9) the check's own clone and the catalog entries it re-bakes read their credentials from the service — for a repository whose image is a catalog entry this is the flip's whole effect, since the entry's own build is not reached by it"
        fi
        ;;
      *) fact "noaccessor-$label" FAIL "the daily check job ${jname} of ${repo} still mounts ${jm} Secret Manager container(s): the flip has not dropped the check job's secret_key_ref mounts (B4.9, check.tf), so the build account still needs an accessor on them. Re-apply the root" ;;
      esac
    fi
  else
    unmeasured "noaccessor-$label" "the installation's Cloud Run jobs could not be read: $(last_lines "$SCRATCH/job-$label.err")"
  fi
}

# print_plan: what a real run would read, in order, and what its Restore would
# delete. Printed by --dry-run, which runs NOTHING: no gcloud and no fugaro is
# called, so this mode is safe on a machine with no credentials at all.
print_plan() {
  say "dry run: NOTHING below was run, and no tool was called at all — not gcloud, not fugaro. A real run would first check that this is the fugaro-dev installation (fugaro doctor --project fugaro --all) and that fugaro version is not a development build, then read the project's effective image_map — this machine's project config for ${NAME} when it states one, the published policy node otherwise — and then, in this order:"
  say "  [network] gcloud builds list --project=${P} --region=${L} --limit=${BUILDS} --format=json  (the INSTALLATION'S REGION's recent builds — a listing with no --region reads the global region and would find none of them; each repository's own are the newest builds whose _IMAGE names catalog-<the entry its image_map names>, and a repository the map does not mention is matched by its own _REPO_URL clone URL instead — a repository-level derived image is disabled, D1900)"
  say "  [network] fugaro project policy show --json --project ${NAME}  (the published policy node, read for its image_map when this machine's project config states none — the map's local half wins where it writes one, D1906; a node that cannot be read is not a failure, the local half answers alone)"
  say "  [network] gcloud run services describe ${SVC} --project=${P} --region=${L} --format=json  (the credentials service ITSELF: this row is read off the service and not off any build, so it is measured even when the project holds no build at all)"
  say "  [network] gcloud run jobs list --project=${P} --region=${L} --format=json  (the installation's Cloud Run jobs; each repository's daily check job is the fugarochk-… one, found by its FUGARO_REMOTE clone URL or its fugaro_repo label)"
  say "  [network] gcloud logging read 'resource.labels.service_name=${SVC} jsonPayload.purpose=build' --project=${P}  (the service's OWN credsvc: audit lines for each repository, retried while Cloud Logging catches up)"
  say "  [network] gcloud run jobs executions list --job=<the check job> --project=${P} --region=${L} --format=json  (the daily check's newest execution)"
  say "  [network] gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L} --format=json  (who may invoke the service: B4.4's grant to the build accounts, and no public member)"
  if [ "$MODE" = flip ]; then
    say "and, for each repository, it would read its NEWEST catalog-entry build (that it still ended SUCCESS and still ran as the installation's build identity, and that it still carries the one mount it clones with — B4.9's flip does not reach a catalog entry's build, D2301), its check job (that it now mounts NOTHING and names the service's address instead — which is the flip's whole effect on such a repository) and the service's own lines for it."
  else
    say "and, for each repository, it would read its newest CATALOG-ENTRY build (status, the entry and image its _IMAGE names, the account it ran as — the installation's build identity, never a repository build account — and the availableSecrets it carries, which is the one provider git credential it clones with), the service's own lines for it, and its daily check job (found by its own clone URL and fugaro_repo label: that it runs as a repository build account, mounts the repository's git credential and carries NO service address — the pre-flip shape) with that job's newest execution."
  fi
  say "This check creates nothing, submits no build and deletes nothing: its Restore removes the scratch directory and nothing else. The builds it reads are the catalog entries' own, which 'fugaro image build <entry>' (or 'fugaro apply') submitted; 'fugaro image refresh' is refused (D1900) and builds nothing."
}

main() {
  need_tools
  scratch_up
  write_reader
  guard_xtrace
  guard_inputs

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "the plan above was printed and NOTHING was called: no gcloud, no fugaro. The guards that read the cloud (the installation's project, its version and its builds) run only in a real run"
    return 0
  fi

  guard_project
  read_image_map
  read_builds
  read_jobs

  if [ "$MODE" = flip ]; then
    step_flip_repo github "$REPO" github
    step_flip_repo bitbucket "$BB_REPO" bitbucket
  else
    step_service
    step_repo github "$REPO" github
    step_repo bitbucket "$BB_REPO" bitbucket
    step_check_repo github "$REPO" github
    step_check_repo bitbucket "$BB_REPO" bitbucket
    step_boundary
  fi
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS_SWITCH=(fugaro-version project-region xtrace inputs service
  build-github mounts-github served-github check-github
  build-bitbucket mounts-bitbucket served-bitbucket check-bitbucket
  boundary restore scratch-removed)
BLOCK_KEYS_FLIP=(fugaro-version project-region xtrace inputs
  noavailable-github noaccessor-github build-flip-github served-github-flip
  noavailable-bitbucket noaccessor-bitbucket build-flip-bitbucket served-bitbucket-flip
  restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local keys=("${BLOCK_KEYS_SWITCH[@]}")
  if [ "$MODE" = flip ]; then keys=("${BLOCK_KEYS_FLIP[@]}"); fi
  local key printed=""
  printf '\nCHECK 196 RESULT\n' >&3
  for key in "${keys[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 196 RESULT END (exit %s)\n' "$status" >&3
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed.
# This check has no cleanup that writes, so a second Ctrl-C during it is safe;
# the handler is here only so the RESULT block is printed on an interrupt.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

MODE=switch
case "${1:-}" in
--dry-run) DRY_RUN=1 ;;
--after-flip)
  MODE=flip
  case "${2:-}" in
  --dry-run) DRY_RUN=1 ;;
  "") ;;
  *) die "usage: $PROG [--dry-run|--after-flip [--dry-run]]" ;;
  esac
  ;;
"") ;;
*) die "usage: $PROG [--dry-run|--after-flip [--dry-run]]" ;;
esac

REPO=${CHECK196_REPO:-}
BB_REPO=${CHECK196_BITBUCKET:-}
BUILDS=${CHECK196_BUILDS:-50}
LOG_AGE=${CHECK196_LOG_AGE:-90m}
LOG_LIMIT=${CHECK196_LOG_LIMIT:-200}
LOG_WAIT_SECONDS=${CHECK196_LOG_WAIT_SECONDS:-60}
LOG_POLL_SECONDS=${CHECK196_LOG_POLL_SECONDS:-10}
DRY_RUN=${CHECK196_DRY_RUN:-${DRY_RUN:-}}
KEEP_SCRATCH=${CHECK196_KEEP_SCRATCH:-}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
