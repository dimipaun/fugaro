#!/usr/bin/env bash
# Check 940 — the repository-to-image map keyed by provider, on a live
# installation.
#
# Plan task K1 of docs/image-map-by-provider/implementation-plan.md; the spec's
# acceptance 2, 6 and 7 are what it reads, and D3371 fixes the member's name
# (`repo_images`). Offline nothing is settled: whether the published node really
# carries `repo_images` and no `image_map`, whether `fugaro image survey --json`
# really prints version 2, and whether the job of a repository at each provider
# really runs the image its own `provider/owner/name` key names, are answers of
# an installation. So this check reads the project's own node (through `fugaro
# project policy show --json`, the readable spelling of what the node holds),
# the survey document and the project's Cloud Run jobs — and it WRITES nothing.
#
# Usage:      bash scripts/live-checks/check940.sh [--project <name>]
#             bash scripts/live-checks/check940.sh [--project <name>] --dry-run
# Needs:      bash, mktemp, python3, gcloud (read the project's Cloud Run jobs,
#             which is where a repository's job image is) and fugaro (`doctor
#             --json` for the installation's project and region, `project policy
#             show --json` for the node and `image survey --json` for the survey
#             document). Nothing is installed and nothing is fetched.
# Where:      any installation you name with --project (default `fugaro`). The
#             installation's project and region are read from `fugaro doctor
#             --json`, never from `gcloud config get-value project`, so a stray
#             config cannot point this check at another project. Never belong.
# Creates:    nothing in the installation, the project or any repository. This
#             check reads the node, the survey document and Cloud Run's job
#             list, and it writes nothing anywhere but its own scratch
#             directory.
# Costs:      wall clock, not money: a minute at the terminal. No build is
#             submitted and no run is launched.
# Runs:       by YOU. No agent runs any step of it, and no agent has: what it
#             measures is a live installation. Its own test (check940_test.sh)
#             drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else — this check creates no
#             cloud resource. It runs from the exit trap too, so a Ctrl-C leaves
#             nothing of the check's behind.
# Output:     progress on stderr, ONE block on stdout headed CHECK 940 RESULT,
#             then CHECK 940 RESULT END (exit N) — to copy back to the
#             coordinator, and nothing else. Exit 0: every criterion that was
#             measured held. 1: a measured criterion did not hold, or a guard
#             stopped the check before it could run. 2: a row could not be
#             measured for a reason that is this check's own setup rather than
#             the installation (the block says which, by name). In the block, a
#             key the run never reached reads `FAIL — not reached`, which is a
#             row that was NOT measured.
#
# THE THIRD STEP IS SKIPPED WHERE THE PROJECT HAS NO SUCH REPOSITORY (V3 of the
# plan's reconciliation). The known Bitbucket sandbox is the workspace
# `dimi_bmb`, and whether the project under test holds a Bitbucket repository is
# the owner's to settle: with no `bitbucket/…` key in the map the row reads INFO
# and says which provider was skipped, rather than FAILing an installation whose
# map is right.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# authorization header. The map's keys are repository names, its values are
# catalog entry names, and the job's image is an image reference — all names.
# This check holds no credential of its own beyond the gcloud access token it
# mints to read the project's own job list.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK940_PROJECT (or --project: the installation's name; `fugaro` by default),
# CHECK940_DRY_RUN, CHECK940_KEEP_SCRATCH.
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
need() { command -v "$1" >/dev/null 2>&1 || die "Check 940 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  need bash
  need mktemp
  need python3 "every document below is read by the reader in the scratch directory"
  need gcloud
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk940-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
PRINTED=""
FACTS_CAPTURED=""
CLEANUP_RUNNING=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk940.XXXXXXXX") || die "Check 940 cannot make a scratch directory"
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
    # The knob's own row says which of the two it is: a directory the owner
    # asked to keep is not one this check failed to remove.
    SCRATCH_KEPT=yes
    say "keeping $SCRATCH (CHECK940_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, undone. There is nothing of this check's in
# the installation, the project or any repository — it writes nothing — so this
# row is about the one thing that COULD have been left: the scratch directory.
restore() {
  fact restore PASS "this check created no cloud resource, submitted no build and launched no run: the only thing it made is the scratch directory, whose row is below"
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
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK940_KEEP_SCRATCH is set), at $SCRATCH"
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

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

rows_of() { # rows_of PREFIX TEXT — every `PREFIX=a|b` row's tail, in order
  printf '%s\n' "$2" | sed -n "s/^$1=//p"
}

# write_reader installs the one file that reads every document this check takes
# — the published policy node, the survey document and the project's Cloud Run
# jobs — so that no reading of a JSON shape is spelled twice in this script and
# none of them is done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 940's reader: one file, one reading per subcommand, no credential in
any of them.

policy  argv: `fugaro project policy show --json`'s file. Prints how many keys
        the node's repository-to-image map (`repo_images`) holds and how many
        its legacy `image_map` holds, and then one `map=<key>|<entry>` line per
        `repo_images` key, sorted. The keys are printed as the document spells
        them: `provider/owner/name`, which is what the caller looks a repository
        up by.
survey  argv: `fugaro image survey --json`'s file. Prints the document's
        version, how many keys `setup.repo_images` holds and whether
        `setup.legacy_image_map` is present at all (R4: it holds the bare-name
        map only while the project's effective map is legacy).
jobs    argv: `gcloud run jobs list --format=json`'s file, a repository
        (`<owner>/<name>`) and the provider its key named. Prints one
        `job=<name>|<image>|<entry>` line per Cloud Run job that is that
        repository's, sorted by name, followed by `found=yes` and `count=<n>`.
        `entry` is the catalog entry the job's image names (`catalog-<entry>`)
        or `-` for an image that is not a catalog entry's. The daily check jobs
        (`fugarochk-…`) are not a repository's job image and are skipped: this
        row is about the image a run of the repository would land on.
"""
import json
import re
import sys

WORD = re.compile(r"[^A-Za-z0-9._:@/+-]")
CHECK_JOB_PREFIX = "fugarochk-"


def word(v, cap=200):
    """One field of a document, for the block's own `key=value` shape. A field
    the document does not carry is `-` and never the word `None`: the callers
    read `-` as "nothing here", and a `None` printed as a word is an answer they
    would take for a value."""
    if v is None:
        return "-"
    return WORD.sub("", str(v))[:cap] or "-"


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def mapping(doc, key):
    """One member of a document, when it is a map. Anything else — an absent
    member, a list, a sentence — is an empty map: the caller reads a count of 0
    as "the document does not hold it", which is the answer it is looking for,
    and a member of another shape is not a map this check can count."""
    if not isinstance(doc, dict):
        return {}
    m = doc.get(key)
    if not isinstance(m, dict):
        return {}
    return {str(k): str(v) for k, v in m.items()}


def policy_of(path):
    doc = load(path)
    if not isinstance(doc, dict):
        return None
    p = doc.get("policy")
    return p if isinstance(p, dict) else None


def catalog_entry_of(image):
    """The catalog ENTRY an image reference names, or "" when it names none. A
    catalog entry's image is
    `<region>-docker.pkg.dev/<project>/<shared-registry>/catalog-<entry>`
    (gcp.SharedCatalogImage), and the tag is dropped before the last path
    segment is compared: a repository's job runs that entry's image, and an
    image of any other repository or workflow is not one."""
    ref = str(image or "").split("@")[0]
    parts = ref.split("/")
    if len(parts) < 2:
        return ""
    name = parts[-1].split(":")[0]
    if not name.startswith("catalog-"):
        return ""
    return name[len("catalog-"):]


def clone_url(repo, provider):
    host = "bitbucket.org" if provider == "bitbucket" else "github.com"
    return "https://%s/%s.git" % (host, repo)


def env(spec):
    out = {}
    for e in (spec.get("env") or []) if isinstance(spec, dict) else []:
        if isinstance(e, dict) and e.get("name"):
            out[e["name"]] = e
    return out


def spec_of(doc):
    node = doc
    for key in ("spec", "template", "spec", "template", "spec"):
        node = node.get(key) if isinstance(node, dict) else None
        if node is None:
            break
    if isinstance(node, dict) and isinstance(node.get("containers"), list):
        return node
    return None


def containers_of(doc):
    spec = spec_of(doc)
    return (spec.get("containers") or []) if isinstance(spec, dict) else []


def is_the_repository(job, repo, provider):
    """Whether a Cloud Run job is this repository's OWN: its containers name the
    repository's own clone URL as FUGARO_REMOTE — the remote the job was applied
    for — or the installation wrote the repository's slug on it as the
    `fugaro_repo` label. The daily check job is skipped by name: its image is
    not the image a run of the repository lands on."""
    name = ((job.get("metadata") or {}).get("name")) or ""
    if name.startswith(CHECK_JOB_PREFIX):
        return False
    label = ((job.get("metadata") or {}).get("labels") or {}).get("fugaro_repo") or ""
    remote = ""
    for c in containers_of(job):
        remote = (env(c).get("FUGARO_REMOTE") or {}).get("value") or remote
    owner, _, short = repo.partition("/")
    candidates = [repo, short, owner + "-" + short]
    if any(remote.endswith("/" + c + ".git") for c in candidates if c):
        return True
    return bool(label) and any(label == c or label.startswith(c + "-")
                              for c in candidates if "/" not in c)


cmd = sys.argv[1] if len(sys.argv) > 1 else ""
if cmd == "policy":
    p = policy_of(sys.argv[2])
    if p is None:
        print("read=no")
    else:
        print("read=yes")
        images = mapping(p, "repo_images")
        legacy = mapping(p, "image_map")
        print("repo_images=%s" % len(images))
        print("image_map=%s" % len(legacy))
        for key in sorted(images):
            print("map=%s|%s" % (word(key), word(images[key])))
elif cmd == "survey":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("read=no")
    else:
        print("read=yes")
        setup = doc.get("setup") if isinstance(doc.get("setup"), dict) else {}
        images = mapping(setup, "repo_images")
        legacy = mapping(setup, "legacy_image_map")
        print("version=%s" % word(doc.get("version"), 20))
        print("repo_images=%s" % len(images))
        print("legacy_image_map=%s" % len(legacy))
        for key in sorted(images):
            print("map=%s|%s" % (word(key), word(images[key])))
elif cmd == "jobs":
    repo = sys.argv[3]
    provider = sys.argv[4] if len(sys.argv) > 4 else "github"
    doc = load(sys.argv[2]) or []
    found = []
    for job in doc:
        if not isinstance(job, dict) or not is_the_repository(job, repo, provider):
            continue
        image = ""
        for c in containers_of(job):
            image = c.get("image") or image
        found.append((((job.get("metadata") or {}).get("name")) or "",
                      image, catalog_entry_of(image)))
    found.sort()
    print("found=%s" % ("yes" if found else "no"))
    print("count=%s" % len(found))
    for name, image, entry in found:
        print("job=%s|%s|%s" % (word(name, 120), word(image, 250), word(entry, 100)))
else:
    sys.stderr.write("reader.py: unknown query %s\n" % cmd)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/reader.py"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    say "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print every command this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check940.sh -- the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token or a command's environment"
}

# guard_project: the installation is the one --project names, and its project and
# region are read from `fugaro doctor --json` rather than from `gcloud config`, so
# a stray config cannot point this check at another project. A project naming
# belong stops the run rather than warning: this check reads one installation's
# own node and job list, and another project's answers nothing about it.
guard_project() {
  local doc=$SCRATCH/doctor.json
  if ! fugaro doctor --json --project "${NAME:?}" >"$doc" 2>"$SCRATCH/doctor.err"; then
    # doctor exits 1 when a check of its own is a warning and still prints its
    # document, so the document — not the status — is what this guard reads; a
    # run with no document at all could not read the installation.
    if [ ! -s "$doc" ]; then
      stop "fugaro doctor --json --project ${NAME} printed no document: this check cannot read the installation's project and region, which is where every gcloud read below is pinned. Run 'fugaro doctor --json --project ${NAME}' yourself and read its answer"
    fi
  fi
  GCP_PROJECT=$(reader_field "$doc" gcp_project)
  REGION=$(reader_field "$doc" region)
  if [ -z "$GCP_PROJECT" ]; then
    stop "fugaro doctor --json --project ${NAME} named no GCP project: this check cannot read the installation's Cloud Run jobs, which is where a repository's job image is. Run 'fugaro doctor --json --project ${NAME}' yourself and read its answer"
  fi
  case "${NAME}${GCP_PROJECT}${REGION}" in
  *belong*) stop "the installation '${NAME}' (GCP ${GCP_PROJECT}${REGION:+, $REGION}) names belong: this check is never run there" ;;
  esac
  fact project PASS "installation ${NAME}, GCP project ${GCP_PROJECT}${REGION:+, region $REGION} (read from fugaro doctor --json and not from gcloud config; never belong)"
}

reader_field() { # reader_field FILE KEY — one field of doctor's project block
  python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("project") or {}).get(sys.argv[2]) or "")' "$1" "$2" 2>/dev/null || true
}

# ---------------------------------------------------------------- the reading
POLICY_FILE=""
SURVEY_FILE=""
JOBS_FILE=""
POLICY_ANS=""
SURVEY_ANS=""
JOBS_ANS=""

# read_policy: the project's published policy node, once. `project policy show
# --json` is the node in the reader's own spelling: the member names are the ones
# the node holds, so this is a reading of the node and not of a copy of it.
read_policy() {
  POLICY_FILE=$SCRATCH/policy.json
  if ! fugaro project policy show --json --project "${NAME:?}" >"$POLICY_FILE" 2>"$SCRATCH/policy.err"; then
    stop "the project's published policy node cannot be read: 'fugaro project policy show --json --project ${NAME}' failed with $(last_lines "$SCRATCH/policy.err"). Run it yourself and read its answer before going on"
  fi
  if ! POLICY_ANS=$(reader policy "$POLICY_FILE" 2>"$SCRATCH/policy-read.err"); then
    stop "the project's published policy node could not be read (the reader failed): $(last_lines "$SCRATCH/policy-read.err")"
  fi
  if [ "$(field_of read "$POLICY_ANS")" != yes ]; then
    stop "fugaro project policy show --json --project ${NAME} printed no policy: no node is published in this installation, so there is no repository-to-image map (repo_images) to read. Publish one with 'fugaro project policy publish'"
  fi
}

# read_survey: `image survey --json`, once. It is read-only (the survey reads
# each repository through the code host's file API) and it is where the document's
# version is settled.
read_survey() {
  SURVEY_FILE=$SCRATCH/survey.json
  if ! fugaro image survey --json --project "${NAME:?}" >"$SURVEY_FILE" 2>"$SCRATCH/survey.err"; then
    stop "the image survey cannot be read: 'fugaro image survey --json --project ${NAME}' failed with $(last_lines "$SCRATCH/survey.err"). Run it yourself and read its answer before going on"
  fi
  if ! SURVEY_ANS=$(reader survey "$SURVEY_FILE" 2>"$SCRATCH/survey-read.err"); then
    stop "the image survey's document could not be read (the reader failed): $(last_lines "$SCRATCH/survey-read.err")"
  fi
  if [ "$(field_of read "$SURVEY_ANS")" != yes ]; then
    stop "fugaro image survey --json --project ${NAME} printed no document"
  fi
}

# read_jobs: the installation's Cloud Run jobs, once, for both providers. The
# region is the one doctor reported and it is not optional: a listing with no
# --region reads a region the installation does not deploy to.
read_jobs() {
  JOBS_FILE=$SCRATCH/jobs.json
  if [ -z "$REGION" ]; then
    JOBS_ANS=""
    return 0
  fi
  if ! gcloud run jobs list --project="${GCP_PROJECT:?}" --region="${REGION:?}" --format=json >"$JOBS_FILE" 2>"$SCRATCH/jobs.err"; then
    JOBS_ANS=""
    return 0
  fi
  JOBS_ANS=$(cat "$JOBS_FILE")
}

last_lines() {
  local f=$1 n=${2:-2}
  [ -f "$f" ] || {
    printf 'no output'
    return 0
  }
  tail -n "$n" "$f" | tr -d '\r' | tr '\n' ' ' | tr -s ' ' | cut -c1-400
}

# --------------------------------------------------------------- the steps
# step_node: the project's own node. The member's name is D3371's: `repo_images`
# present, `image_map` absent — the release this check is about writes the new
# member and, after the step that follows the credentials service, removes the
# old one. Both members on the node is an `update` that stopped between the two
# (harmless, and the next `update` removes the old one), and neither is an
# installation the release has not reached.
step_node() {
  local images legacy first
  images=$(field_of repo_images "$POLICY_ANS")
  legacy=$(field_of image_map "$POLICY_ANS")
  # A count the reader did not give at all is a document this check cannot read,
  # which is not the same statement as a node whose map is empty: it says which
  # of the two it is rather than reading an absent count as a zero.
  if [ -z "$images" ] || [ -z "$legacy" ]; then
    fact node FAIL "the project's published policy node could not be read for its repository-to-image map (repo_images): the reader gave no count for one of its two members. Read it yourself with 'fugaro project policy show --json --project ${NAME}' and look for repo_images and image_map"
    return 0
  fi
  if [ "$images" -eq 0 ] && [ "$legacy" -eq 0 ]; then
    fact node FAIL "the project's published policy node holds no repository-to-image map at all: neither repo_images nor a legacy image_map. Publish one with 'fugaro image map set <provider>/<owner>/<name> <entry>' in a checkout, then 'fugaro project policy publish'"
    return 0
  fi
  if [ "$images" -gt 0 ] && [ "$legacy" -eq 0 ]; then
    first=$(rows_of map "$POLICY_ANS" | head -n 1)
    fact node PASS "the project's published policy node carries the repository-to-image map (repo_images) and no legacy image_map: ${images} key(s), the first being ${first%%|*} — so the release's own member is on the node and the old one is gone"
    return 0
  fi
  if [ "$images" -gt 0 ]; then
    fact node FAIL "the project's published policy node carries the repository-to-image map (repo_images) (${images} key(s)) AND a legacy image_map (${legacy} key(s)): repo_images wins wherever both are read, so nothing is mis-mapped, but the old member is still there. Run 'fugaro update' — its step after the credentials service removes image_map"
    return 0
  fi
  fact node FAIL "the project's published policy node carries no repository-to-image map (repo_images) and still holds a legacy image_map (${legacy} key(s)): the map is not yet keyed by provider, so two providers of one name still share one entry. Run 'fugaro update' at the installation's own checkout"
}

# step_survey: `image survey --json` at version 2 (R4). The version is the
# criterion; the two members are read beside it, because a version-2 document
# that carried no repo_images would be a document whose version says one thing
# and whose body another.
step_survey() {
  local v images legacy
  v=$(field_of version "$SURVEY_ANS")
  images=$(field_of repo_images "$SURVEY_ANS")
  legacy=$(field_of legacy_image_map "$SURVEY_ANS")
  if [ "$v" != 2 ]; then
    fact survey FAIL "fugaro image survey --json prints version ${v:-nothing}, not version 2: the document's setup carries repo_images (provider/owner/name keys) and legacy_image_map at version 2, and a version-1 document carries the bare-name image_map instead. Install the release this check is about, and run 'fugaro image survey --json --project ${NAME}' to read it yourself"
    return 0
  fi
  fact survey PASS "fugaro image survey --json prints version 2, with ${images} key(s) in setup.repo_images (provider/owner/name) and $([ "$legacy" -gt 0 ] && printf 'a legacy setup.legacy_image_map of %s key(s), which the effective map being legacy explains' "$legacy" || printf 'no setup.legacy_image_map, so the project'"'"'s effective map is not legacy')"
}

# step_job PROVIDER: one job image for a provider the project has. The
# repository is the first key of that provider in the node's repo_images (sorted,
# so the same repository is read on every run of the same project), and its job's
# image must be the catalog image of the entry its own key names. With no key at
# that provider the row reads INFO and names the provider it skipped (V3): whether
# the project holds a repository of the other provider is the owner's to settle,
# and FAILing an installation whose map is right would end the release on a
# question the check cannot answer.
step_job() { # step_job PROVIDER
  local provider=$1 key repo entry ans found count name image imageentry
  key=$(rows_of map "$POLICY_ANS" | sed -n "s#^${provider}/##p" | head -n 1)
  if [ -z "$key" ]; then
    fact "job-$provider" INFO "the project's repository-to-image map (repo_images) names no ${provider} repository, so this step is skipped: there is no job image of that provider to read. Read the map with 'fugaro project policy show --json --project ${NAME}'"
    return 0
  fi
  repo=${key%%|*}
  entry=${key#*|}
  if [ -z "$JOBS_ANS" ]; then
    unmeasured "job-$provider" "the installation's Cloud Run jobs could not be read, so the image the job of ${repo} runs could not be measured: $(last_lines "$SCRATCH/jobs.err")"
    return 0
  fi
  ans=$(printf '%s' "$JOBS_ANS" | reader jobs /dev/stdin "$repo" "$provider" 2>"$SCRATCH/jobs-$provider.err") \
    || { unmeasured "job-$provider" "the Cloud Run job list could not be read (the reader failed): $(last_lines "$SCRATCH/jobs-$provider.err")"; return 0; }
  found=$(field_of found "$ans")
  count=$(field_of count "$ans")
  if [ "$found" != yes ]; then
    fact "job-$provider" FAIL "this installation deploys no Cloud Run job for ${repo}, the repository its ${provider} key names in the repository-to-image map (repo_images), so the image a run of it would land on could not be read: 'gcloud run jobs list --project=${GCP_PROJECT} --region=${REGION}' holds no job of this repository. Re-apply the repository's root with 'fugaro update --project ${NAME} --repo' in a checkout of ${repo}"
    return 0
  fi
  name=$(rows_of job "$ans" | head -n 1 | cut -d'|' -f1)
  image=$(rows_of job "$ans" | head -n 1 | cut -d'|' -f2)
  imageentry=$(rows_of job "$ans" | head -n 1 | cut -d'|' -f3)
  if [ "$imageentry" = "$entry" ]; then
    fact "job-$provider" PASS "the job of ${repo} (${name}, of ${count} the installation deploys for it) runs ${image}, the catalog entry ${entry}'s image — which is what the repository's own ${provider}/${repo} key in the repository-to-image map (repo_images) names, so a repository's image is read by its provider"
  else
    fact "job-$provider" FAIL "the job of ${repo} (${name}) runs ${image:-no image this reading can name}, and that is not the catalog entry ${entry}'s image (${imageentry:+-${imageentry} instead}): the repository's own ${provider}/${repo} key in the repository-to-image map (repo_images) names ${entry}. Submit the entry with 'fugaro image build ${entry}' (or 'fugaro apply') and re-apply the repository's root, or map the repository to the entry it really runs with 'fugaro image map set ${provider}/${repo} <entry>'"
  fi
}

# print_plan: what a real run would read, in order, and what its Restore would
# remove. Printed by --dry-run, which runs NOTHING: no gcloud and no fugaro is
# called, so this mode is safe on a machine with no credentials at all.
print_plan() {
  say "dry run: NOTHING below was run, and no tool was called at all — not gcloud, not fugaro. A real run would first refuse a shell started with tracing on, and then, in this order:"
  say "  [network] fugaro doctor --json --project ${NAME}  (the installation's project and region, which is how every gcloud read below is pinned)"
  say "  [network] fugaro project policy show --json --project ${NAME}  (the project's published policy node, read for its repo_images and its legacy image_map — the readable spelling of what the node holds)"
  say "  [network] fugaro image survey --json --project ${NAME}  (the survey document, held to version 2)"
  say "  [network] gcloud run jobs list --project=<the project doctor named> --region=<the region doctor named> --format=json  (the installation's Cloud Run jobs, once, for both providers)"
  say "and then it would read, and print PASS or FAIL for each: the node's members (repo_images present, image_map absent); the survey's version (2), beside its two map members; and, for each provider the map names a repository at — github, bitbucket — the image the job of that provider's first repository runs, held to the catalog image of the entry its own provider/owner/name key names. A provider the map names no repository at is skipped with a line, and never failed."
  say "This check creates nothing, submits no build and launches no run: its Restore removes the scratch directory and nothing else."
}

main() {
  need_tools
  scratch_up
  write_reader
  guard_xtrace
  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "the plan above was printed and NOTHING was called: no gcloud, no fugaro"
    return 0
  fi
  guard_project
  read_policy
  read_survey
  read_jobs
  step_node
  step_survey
  step_job github
  step_job bitbucket
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(xtrace project node survey job-github job-bitbucket restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 940 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
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
  printf 'CHECK 940 RESULT END (exit %s)\n' "$status" >&3
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

NAME=${CHECK940_PROJECT:-fugaro}
DRY_RUN=${CHECK940_DRY_RUN:-}
KEEP_SCRATCH=${CHECK940_KEEP_SCRATCH:-}

while [ $# -gt 0 ]; do
  case "$1" in
  --project)
    [ $# -ge 2 ] || die "--project needs the installation's name: usage: $PROG [--project <name>] [--dry-run]"
    NAME=$2
    shift 2
    ;;
  --project=*)
    NAME=${1#--project=}
    shift
    ;;
  --dry-run)
    DRY_RUN=1
    shift
    ;;
  --help | -h)
    printf 'usage: %s [--project <name>] [--dry-run]\n' "$PROG" >&2
    printf 'Check 940: the repository-to-image map (repo_images) keyed by provider, read on a live installation.\n' >&2
    exit 0
    ;;
  *)
    die "usage: $PROG [--project <name>] [--dry-run]"
    ;;
  esac
done
[ -n "$NAME" ] || die "--project is empty: name the installation, or leave it out for 'fugaro'"

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
