#!/usr/bin/env bash
# Check 920 (and, with --catalog, Check 921) — the image-secrets closure read
# off a live installation.
#
# It settles the image-secrets plan's IS12, whose claims are the spec's §6
# acceptance criteria for the workflow-secret class plus the reconciliation's
# UNVERIFIED U1 and U4:
#
#   * no run job of the installation mounts a workflow secret (the flip, G6);
#   * a run of a VERTEX repository fetches one, with no mount anywhere on it
#     (the fetch of IS2, and U1: a vertex run holds a run identity);
#   * a `file: true` secret arrives as a `0400` file under the path its
#     variable names, and is absent in a review of the run's own pull request
#     (D3327, G3, R5);
#   * a repository the secret's allow-list does not admit is refused, with the
#     fix (G1, D3328, D3333);
#   * the service's audit log holds ONE line per fetch, naming one container
#     (the parent plan's audit rule, G18);
# and Check 921, the catalog half (IS8, D3330):
#
#   * a catalog build runs as the installation's build identity, succeeds, and
#     carries no available secret;
#   * and the build identity holds no Secret Manager accessor — on a container
#     of the project or at the project level.
#
# None of that is visible offline. Whether a repository the owner allow-listed
# is really served, whether a repository it did not is really refused, whether
# a value reaches a run whose job mounts nothing, and whether the file a run
# sees is one the review of its own pull request never gets, are answers of a
# live installation and of the credentials service's own lines. So this check
# LAUNCHES throwaway runs in the sandbox repositories you name and reads the
# service's own answers back; it writes nothing into the installation, and it
# deletes nothing.
#
# Usage:      bash scripts/live-checks/check920.sh [--dry-run]
#             bash scripts/live-checks/check920.sh --catalog [--dry-run]
#             bash scripts/live-checks/check920.sh --help
# Needs:      bash, mktemp, python3, gcloud, curl and fugaro on PATH, with
#             `gcloud` authenticated as you. It reads `gcloud run jobs list`,
#             one job, one execution, Cloud Logging (the service's own lines
#             and the run's own), each Secret Manager container's IAM policy,
#             the build list, and the runs bucket (`gcloud storage cat` of one
#             run's result.json). It mints one access token, for the read of
#             the installation's own Realtime Database record. Nothing is
#             installed and nothing is fetched.
# Where:      installation `fugaro` ONLY — project `fugaro-dev`, region
#             `us-east5`. Both are HARD-CODED in this file and never read from
#             `gcloud config get-value project`, and `fugaro doctor --project
#             fugaro --all` must print its `project: … (GCP fugaro-dev,` line:
#             a check that read the project from the environment could be
#             pointed at another installation by a stray config, and this one
#             must never be. Never belong.
# Creates:    throwaway runs in the SANDBOX repositories you name — one in the
#             repository under test, one in the repository the allow-list does
#             not admit, and, in the run mode, one REVIEW of the pull request
#             the first opened, which is a run of its own. Their branches and
#             pull requests stay on those sandbox repositories: this check
#             deletes no branch and closes no pull request, and their rows are
#             yours to settle. It creates no cloud resource and writes no
#             record.
# Costs:      wall clock, not money: about forty minutes per run at the
#             terminal, and the review is a run too. Every model call of those
#             runs is answered by the credentials service, which is the
#             subject.
# Runs:       by YOU. It refuses to start in a coding agent's session and in a
#             traced shell. No agent runs any step of it, and none has: what it
#             measures is a live installation. Its own test (check920_test.sh)
#             drives it against fake tools on PATH and touches no cloud.
# Cleanup:    the scratch directory, and nothing else — this check creates no
#             cloud resource and writes no record. It runs from the exit trap
#             too, so a Ctrl-C leaves nothing of the check's behind.
# Output:     progress on stderr, ONE block on stdout headed CHECK 920 RESULT
#             (or CHECK 921 RESULT with --catalog), then ... RESULT END
#             (exit N) — to copy back to the coordinator, and nothing else.
#             Exit 0: every criterion that was measured held. 1: a measured
#             criterion did not hold. 2: a guard stopped the check, or the run
#             broke, before its last step. 3: a row could not be measured for a
#             reason that is this check's own setup rather than the
#             installation (the block says which, by name). A key the run never
#             reached reads `FAIL — not reached`, which is a row that was NOT
#             measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value, no
# authorization header and no workflow secret's value. Every fact below is a
# NAME — a secret's logical name, a CONTAINER's id, an account's name, a job's
# name — and the service's own lines are reduced to a short plain-word shape
# before they are printed. The one identity in the path is the one Cloud Run
# gives the job this check launched.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK920_REPO (REQUIRED in the run mode: `<owner>/<name>` of the sandbox
#   repository whose own fugaro.yaml declares the two secrets, whose
#   agent.auth is `vertex`, and whose verify command fails unless the file
#   secret is a `0400` non-empty file at the path its variable names — the
#   steps file carries that snippet);
# CHECK920_CHECKOUT (REQUIRED in the run mode: a checkout of CHECK920_REPO.
#   The review half is `fugaro review --pr N`, which resolves the repository
#   from its own checkout's origin, so the check runs it there);
# CHECK920_UNLISTED_REPO (REQUIRED in the run mode: a sandbox repository that
#   declares the same secret and which the secret's allow-list does NOT admit);
# CHECK920_SECRET (REQUIRED in the run mode: the workflow secret declared as an
#   ENVIRONMENT variable, its logical name — `fugaro_p_<name>` is the
#   container the service reads);
# CHECK920_FILE_SECRET (REQUIRED in the run mode: the workflow secret declared
#   `file: true`, its logical name);
# CHECK920_CATALOG_BUILD (the catalog mode: the id of the build to read;
#   unset, the newest build that ran as the build identity);
# CHECK920_CONFIG (the installation's local config, when it is not
#   $XDG_CONFIG_HOME/fugaro/projects/fugaro.yaml), CHECK920_RUN_ID (the base of
#   this check's own run ids, default `date -u +%Y%m%d-%H%M%S`),
# CHECK920_RUN_SECONDS (how long a run is waited for; default 2400),
# CHECK920_POLL_SECONDS (20), CHECK920_LOG_WAIT_SECONDS (60),
# CHECK920_LOG_POLL_SECONDS (5), CHECK920_LOG_AGE (1h), CHECK920_LOG_LIMIT
#   (200), CHECK920_SECRET_LIMIT (how many Secret Manager containers the
#   catalog mode reads the IAM of; default 200), CHECK920_DRY_RUN and
#   CHECK920_KEEP_SCRATCH. None of them can point this check at another project
#   or another region: those two are constants in this file.
#
# -E (errtrace) as well as -e: without it a command that fails inside a command
# substitution ends the shell with a bare exit 1 and NO error trap — the block
# would print the rows below as `FAIL — not reached` and the owner would get no
# line saying where the run broke. With it, the failure reaches on_error and
# ends as one that could not measure (exit 2), which is what the contract says.
set -Eeuo pipefail

# No xtrace, whatever shell it was started from: the block this check prints is
# pasted into a pull request, and `bash -x` would print every command, including
# the access token's own. BASH_XTRACEFD is unset so a trace redirected to a file
# this script knows nothing about cannot be reopened, and guard_xtrace REFUSES
# the run when the shell arrived with tracing on.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block is written to
# the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
AUTH_CONF=""
MODE=run
DRY_RUN_CLI=""

stop() { # stop REASON
  ABORT=$1
  say "stopped: $1"
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED|INFO "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

# unmeasured KEY REASON: a row this check could NOT measure, for a reason that
# is this check's own setup — a knob pointing at the wrong thing, a record it
# could not read — rather than a criterion that did not hold. The row reads
# UNVERIFIED, which is not a pass: the verdict then says a row was not
# measured, and the reason is in the block, by name.
unmeasured() { fact "$1" UNVERIFIED "$2"; }

usage() {
  cat <<'USAGE'
Check 920 — image secrets off a live installation: no run job mounts a
workflow secret, a vertex repository's run fetches one, a `file: true` secret
arrives as a 0400 file and is absent in a review of the run's own pull
request, a repository the allow-list does not admit is refused, and the
service's audit log holds one line per fetch. With --catalog it is Check 921:
a catalog build succeeds as the installation's build identity, which holds no
Secret Manager accessor.

Usage:
  bash scripts/live-checks/check920.sh [--dry-run]
  bash scripts/live-checks/check920.sh --catalog [--dry-run]
  bash scripts/live-checks/check920.sh --help

Read-only on the installation (it creates no cloud resource and writes no
record); it launches throwaway runs and one review in the sandbox repositories
you name, whose branches and pull requests stay there. Run by the owner only.

  PASS (exit 0)  FAIL (exit 1)  stopped (exit 2)  unmeasured (exit 3)
USAGE
}

guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell is tracing (bash -x), and its trace would print the access token this check mints. Run it without -x"
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token this check mints"
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs Check 920: it launches live runs on an installation. Run it yourself: bash scripts/live-checks/check920.sh --dry-run, then without it"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads the installation with it"
}

first_line() { head -1 "$1" 2>/dev/null | cut -c1-300 || true; }
last_lines() { tail -3 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400 || true; }

# field_of KEY ROW: one field out of a `key=value` row this check's reader
# printed. A row of the reader's is newline separated, and a field it did not
# print is empty — which is the reading, not an error.
field_of() { # field_of KEY ROW
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

for arg in "$@"; do
  case "$arg" in
  --catalog) MODE=catalog ;;
  --dry-run) DRY_RUN_CLI=1 ;;
  -h | --help)
    # The header comment and nothing else: the range ends on the first line of
    # the script itself, which is filtered out with it.
    sed -n '2,/^set -Eeuo pipefail/{/^#/p}' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *) say "unknown argument: $arg"; exit 2 ;;
  esac
done
DRY_RUN=${CHECK920_DRY_RUN:-$DRY_RUN_CLI}

# Hard-coded, never `gcloud config get-value project`: a check that read the
# project from the environment can be pointed at another installation by a stray
# config, and this one must never be.
INSTALL=fugaro
P=fugaro-dev
L=us-east5
SVC=fugaro-credentials
IDENTITY_ID=fugaro-build-identity

REPO=${CHECK920_REPO:-}
CHECKOUT=${CHECK920_CHECKOUT:-}
UNLISTED=${CHECK920_UNLISTED_REPO:-}
SECRET=${CHECK920_SECRET:-}
FILE_SECRET=${CHECK920_FILE_SECRET:-}
CATALOG_BUILD=${CHECK920_CATALOG_BUILD:-}
CONFIG=${CHECK920_CONFIG:-${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/${INSTALL}.yaml}
RUN_BASE=${CHECK920_RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}
RUN_SECONDS=${CHECK920_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK920_POLL_SECONDS:-20}
LOG_WAIT_SECONDS=${CHECK920_LOG_WAIT_SECONDS:-60}
LOG_POLL_SECONDS=${CHECK920_LOG_POLL_SECONDS:-5}
LOG_AGE=${CHECK920_LOG_AGE:-1h}
LOG_LIMIT=${CHECK920_LOG_LIMIT:-200}
SECRET_LIMIT=${CHECK920_SECRET_LIMIT:-200}
KEEP_SCRATCH=${CHECK920_KEEP_SCRATCH:-}

NAME_RE='^[a-z0-9][a-z0-9-]{0,62}$'
REPO_RE='^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$'

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk920.XXXXXXXX") || stop "cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  FACTS=$SCRATCH/facts
  : >"$FACTS"
  AUTH_CONF=$SCRATCH/auth.conf
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  fact scratch PASS "$SCRATCH (mode 0700, made by this check, removed by its EXIT trap)"
  NAMES=""
  RECORD_OK=no
  IDENTITY_OK=no
  IDENTITY_EMAIL=""
  RTDB_CODE=""
  RUN_ONE_ID=""
  RUN_ONE_JOB=""
  RUN_ONE_EXEC=""
  RUN_ONE_PR=""
  RUN_ONE_SUCCEEDED=no
}

cleanup() {
  [ "$KEEP_SCRATCH" = 1 ] && return 0
  [ -n "$SCRATCH" ] || return 0
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

verdict() { # verdict <facts>
  if printf '%s\n' "$1" | grep -q '^aborted|'; then
    printf 'STOPPED'
  elif printf '%s\n' "$1" | grep -q '|FAIL|'; then
    printf 'FAIL'
  elif printf '%s\n' "$1" | grep -q '|UNVERIFIED|'; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows, in the order it was
# recorded. That is what makes a block safe to diff between two runs. The two
# modes print two DIFFERENT key sets — Check 921's rows are the catalog's — so a
# block from one is never read as a block from the other.
BLOCK_KEYS_RUN=(fugaro-version project-region xtrace scratch record jobs
  run-one run-mounts run-verify audited review unlisted restore scratch-removed)
BLOCK_KEYS_CATALOG=(fugaro-version project-region xtrace scratch
  build-identity identity-accessors catalog-build restore scratch-removed)

print_block() { # print_block <facts> <status>
  local facts=$1 status=$2 key line keys=()
  if [ "$MODE" = catalog ]; then keys=("${BLOCK_KEYS_CATALOG[@]}"); else keys=("${BLOCK_KEYS_RUN[@]}"); fi
  printf '\nCHECK %s RESULT\n' "$CHECK_NO" >&3
  printf 'MODE: %s\n' "$MODE" >&3
  for key in "${keys[@]}"; do
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    case $'\n'"$(printf '%s\n' "${keys[@]}")"$'\n' in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$state" "$detail" >&3
  done
  printf 'CHECK %s RESULT END (exit %s)\n' "$CHECK_NO" "$status" >&3
}
CHECK_NO=920

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT measured"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory, with the access token's curl config file, is gone"
    facts="${facts}"$'\n'"scratch-removed|PASS|the scratch directory is gone"
  elif [ "$KEEP_SCRATCH" = 1 ]; then
    facts="${facts}"$'\n'"restore|INFO|the scratch directory was KEPT, at ${SCRATCH}, because CHECK920_KEEP_SCRATCH is set; it holds the access token's curl config file"
    facts="${facts}"$'\n'"scratch-removed|INFO|kept on purpose (CHECK920_KEEP_SCRATCH)"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH}, and it holds an access token: remove it yourself"
    facts="${facts}"$'\n'"scratch-removed|FAIL|the scratch directory is still there"
  fi
  local v
  v=$(verdict "$facts")
  if [ -n "$ABORT" ] || [ "$REACHED_END" = no ]; then
    status=2
  elif [ "$v" = FAIL ]; then
    status=1
  elif [ "$v" = INCOMPLETE ]; then
    status=3
  else
    status=0
  fi
  print_block "$facts" "$status"
  exit "$status"
}

on_error() {
  local rc=$?
  stop "line ${1:-?}: '${BASH_COMMAND}' exited ${rc}"
}

on_signal() {
  owner "interrupted"
  exit 130
}

# ------------------------------------------------------------------- the reader
# One python program, written into the scratch directory: every JSON document
# this check reads is turned into `key=value` rows by it, so the bash below
# compares names and never parses JSON itself.
write_reader() {
  cat >"$SCRATCH/reader" <<'PY'
import datetime
import json
import re
import sys

WORD = re.compile(r"[^A-Za-z0-9._:@/+=-]")
ACCESSOR_ROLES = ("roles/secretmanager.secretAccessor", "roles/secretmanager.admin")
PROJECT_ACCESSOR_ROLES = ACCESSOR_ROLES + ("roles/owner", "roles/editor")


def word(v, cap=120):
    return WORD.sub("", str(v))[:cap] or "-"


def text(v, cap=300):
    return " ".join(str(v).split())[:cap].replace("|", "/") or "-"


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def rows(path):
    doc = load(path)
    if isinstance(doc, dict):
        for key in ("runs", "Runs"):
            if isinstance(doc.get(key), list):
                return doc[key]
        return []
    return doc if isinstance(doc, list) else []


def env(spec):
    out = {}
    for e in (spec.get("env") or []) if isinstance(spec, dict) else []:
        if isinstance(e, dict) and e.get("name"):
            out[e["name"]] = e
    return out


def spec_of(doc):
    """The container spec of a job or of one execution of it: the two live at
    different depths (spec.template.spec.template.spec and
    spec.template.spec), so the known paths are tried in order and the document
    is finally searched for the first containers list."""
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


def container_id(ref):
    """A Secret Manager container's own name, out of any of the shapes an id
    travels in: a bare id, a `name:version`, a resource path
    `projects/<p>/secrets/<id>/versions/<n>`, or one with the secret's own
    project prefix. Only the container's name comes back — a version, a project
    prefix and a version path are dropped, because the row that compares it is
    about WHICH container was read or mounted and never about which version."""
    s = str(ref or "")
    if "/secrets/" in s:
        s = s.split("/secrets/", 1)[1]
    s = s.split("@")[-1].split("/")[0].split(":")[0]
    return s


def container_of(entry):
    """The Secret Manager container an env entry is read from, as its name, or
    '' when the entry carries a plain value or nothing at all. A mount is
    valueSource.secretKeyRef.secret: the deployment mounts
    var.secrets[env.value], a DERIVED Secret Manager ID, and the API may spell
    the same container as a version reference or a resource path."""
    src = entry.get("valueSource") or {}
    ref = ((src.get("secretKeyRef") or {}).get("secret")
           or (src.get("secretKeyRef") or {}).get("name") or "")
    return container_id(ref)


def job_name(doc):
    return ((doc.get("metadata") or {}).get("name")) or ""


def execution_paths(execution):
    """(<job>, <execution name>) out of a run row's own execution reference,
    which is a resource path `projects/<p>/locations/<l>/jobs/<job>/executions/<name>`.
    A row that names it in some other shape prints neither."""
    m = re.search(r"/jobs/([^/]+)/executions/([^/]+)$", str(execution or ""))
    return (m.group(1), m.group(2)) if m else ("", "")


def parse_time(v):
    if not isinstance(v, str) or not v.strip():
        return None
    t = v.strip()
    if t.endswith("Z"):
        t = t[:-1] + "+00:00"
    try:
        d = datetime.datetime.fromisoformat(t)
    except Exception:
        return None
    return d if d.tzinfo else d.replace(tzinfo=datetime.timezone.utc)


cmd = sys.argv[1] if len(sys.argv) > 1 else ""

if cmd == "names":
    # argv: the project secret record (creds/projectsecrets). Its exact member
    # names are that record's own business and this check does not pin them:
    # the workflow-secret names are the keys of its `secrets` object when it has
    # one, and otherwise the keys of the document itself less the members the
    # record's own shape uses (`version`, the two allow-list members). Every
    # candidate has to be a legal secret name, so an allow-list member can never
    # be read as one.
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
        sys.exit(0)
    node = doc.get("secrets") if isinstance(doc.get("secrets"), dict) else doc
    skip = {"version", "all_repos", "repos", "secrets"}
    names = sorted(k for k in node if k not in skip and re.fullmatch(r"[a-z0-9][a-z0-9-]{0,62}", str(k)))
    print("found=yes")
    for n in names:
        print("name=%s" % word(n, 80))
    print("count=%d" % len(names))
elif cmd == "run":
    # argv: `fugaro runs ls --json`'s document, a run id. Prints that run's row
    # field by field, and the job and execution its own execution reference
    # names, which is what the two rows below read.
    want = sys.argv[3]
    for row in rows(sys.argv[2]):
        if not isinstance(row, dict) or row.get("run_id") != want:
            continue
        print("found=yes")
        for key in ("run_id", "status", "terminal", "outcome", "pr", "repo", "started_at", "execution", "mode"):
            if key in row:
                v = row[key]
                if isinstance(v, bool):
                    v = "yes" if v else "no"
                print("%s=%s" % (key, text(v) if key in ("started_at", "execution") else word(v, 80)))
        job, ex = execution_paths(row.get("execution"))
        if job:
            print("job=%s" % word(job, 120))
            print("exec=%s" % word(ex, 120))
        if row.get("reason"):
            print("reason=%s" % text(row.get("reason"), 240))
        break
    else:
        print("found=no")
elif cmd == "slug":
    # argv: `gcloud run jobs list --format=json`'s file, a repository
    # (owner/name). Prints the key the repository's own runs are stored under in
    # the runs bucket — the `fugaro_repo` label `fugaro update --repo` writes on
    # its jobs, which IS that slug (readable path, then "-" and 16 hex of
    # sha256(provider NUL canonical path)). The run's own record is read at
    # `runs/<slug>/<run id>/result.json`, and a path spelled out of the
    # repository's name is a prefix nobody writes.
    want = str(sys.argv[3]).lower().replace("/", "-")
    for job in load(sys.argv[2]) or []:
        if not isinstance(job, dict):
            continue
        label = ((job.get("metadata") or {}).get("labels") or {}).get("fugaro_repo") or ""
        if label.startswith(want + "-") and re.fullmatch(r"[0-9a-f]{16}", label[len(want) + 1:]):
            print("slug=%s" % word(label, 100))
            break
elif cmd == "mounts":
    # argv: `gcloud run jobs list --format=json`'s file. Prints one `job=` line
    # per deployed job and one `mount=<job>|<ENV>|<container>` line per variable
    # fed from a Secret Manager container, plus the counts. Nothing of it is a
    # value: a mount is a container's NAME.
    jobs = 0
    mounts = 0
    for job in load(sys.argv[2]) or []:
        if not isinstance(job, dict):
            continue
        name = job_name(job)
        if not name:
            continue
        jobs += 1
        print("job=%s" % word(name, 120))
        for c in containers_of(job):
            for var, entry in env(c).items():
                cid = container_of(entry)
                if cid:
                    mounts += 1
                    print("mount=%s|%s|%s" % (word(name, 120), word(var, 80), word(cid, 120)))
    print("jobs=%d" % jobs)
    print("mounts=%d" % mounts)
elif cmd == "pod":
    # argv: one job's or one execution's `--format=json` document. Prints the
    # account its containers run as and one `mount=<ENV>|<container>` line per
    # variable fed from a container — for EVERY container of the document, since
    # a job's env is a list of containers and a reading that stopped at the
    # first would answer `absent` for a mount the second one carries.
    doc = load(sys.argv[2])
    spec = spec_of(doc)
    print("name=%s" % word(job_name(doc), 120))
    print("account=%s" % word((spec.get("serviceAccountName") if isinstance(spec, dict) else "") or "-", 160))
    n = 0
    for c in containers_of(doc):
        for var, entry in env(c).items():
            cid = container_of(entry)
            if cid:
                n += 1
                print("mount=%s|%s" % (word(var, 80), word(cid, 120)))
    print("mounts=%d" % n)
elif cmd == "audit":
    # argv: `gcloud logging read --format=json`'s file, a run id. Prints one
    # `audit=` line per line the credentials service wrote for that run — its
    # own acts, naming the container it served from and the purpose — and one
    # `refused=` line per refusal, with the sentence the caller was sent. The
    # service's shapes are its own; what is read here is only what its lines
    # say, reduced to plain words.
    want = sys.argv[3]
    for entry in load(sys.argv[2]) or []:
        if not isinstance(entry, dict):
            continue
        p = entry.get("jsonPayload") or {}
        if p.get("run") != want:
            continue
        msg = entry.get("textPayload") or p.get("msg") or p.get("message") or ""
        if msg == "credsvc: audit":
            print("audit=%s|%s|%s|%s" % (
                word(p.get("status"), 40),
                word(container_id(p.get("credential")), 120),
                word(p.get("purpose"), 40),
                word(p.get("caller"), 120)))
        elif msg == "credsvc: refused":
            print("refused=%s|%s" % (word(p.get("status"), 40), text(p.get("message"), 300)))
elif cmd == "verify":
    # argv: one run's own result.json, out of the runs bucket. Prints the head
    # it was written for and one `verify=` line per verify record it holds
    # (whether it passed, on a clean tree, its exit code and its test counts).
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
        sys.exit(0)
    print("found=yes")
    print("head=%s" % word(doc.get("head_sha"), 60))
    for i, v in enumerate(doc.get("verify") or []):
        if not isinstance(v, dict):
            continue
        print("verify=%d|%s|%s|%s|%s|%s" % (
            v.get("n", i),
            "passed" if v.get("passed") else "failed",
            "clean" if v.get("clean_tree") else "dirty",
            v.get("exit_code", "-"),
            v.get("tests", "-"),
            v.get("failures", "-")))
elif cmd == "sa":
    # argv: `gcloud iam service-accounts list --format=json`'s file, an address.
    # Prints whether the account is there, with its display name.
    want = sys.argv[3]
    for a in load(sys.argv[2]) or []:
        if not isinstance(a, dict):
            continue
        if a.get("email") != want:
            continue
        print("found=yes")
        print("display=%s" % text(a.get("displayName"), 120))
        break
    else:
        print("found=no")
elif cmd == "iam":
    # argv: one IAM policy document, a member (a bare address). Prints one
    # `grant=<role>|<member>` line per binding that names it. Nothing of it is a
    # value: a binding is a role and a name.
    doc = load(sys.argv[2])
    want = sys.argv[3]
    found = 0
    for b in (doc or {}).get("bindings") or []:
        if not isinstance(b, dict):
            continue
        role = b.get("role") or ""
        for m in b.get("members") or []:
            bare = str(m)
            for prefix in ("serviceAccount:", "user:", "group:", "domain:"):
                if bare.startswith(prefix):
                    bare = bare[len(prefix):]
                    break
            if bare != want:
                continue
            found += 1
            print("grant=%s|%s" % (word(role, 80), word(str(m), 160)))
    print("grants=%d" % found)
elif cmd == "builds":
    # argv: `gcloud builds list --format=json`'s file, the build identity's
    # address. Prints one `build=<id>|<status>|<serviceAccount>|<createTime>`
    # line per build that ran as that identity, NEWEST FIRST.
    want = sys.argv[3]
    out = []
    for b in load(sys.argv[2]) or []:
        if not isinstance(b, dict):
            continue
        sa = str(b.get("serviceAccount") or "")
        bare = sa.split("/")[-1]
        if want not in (sa, bare):
            continue
        out.append(b)
    out.sort(key=lambda b: parse_time(b.get("createTime")) or datetime.datetime.min.replace(tzinfo=datetime.timezone.utc), reverse=True)
    for b in out:
        print("build=%s|%s|%s|%s" % (
            word(b.get("id"), 80), word(b.get("status"), 40), word(b.get("serviceAccount"), 160),
            word(b.get("createTime"), 40)))
    print("builds=%d" % len(out))
elif cmd == "build":
    # argv: one `gcloud builds describe --format=json` document. Prints what the
    # build REQUEST carried: its status, its available secrets count, and how
    # many steps read a secret into their environment or mount one as a volume.
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
        sys.exit(0)
    avail = doc.get("availableSecrets") or {}
    asecrets = avail.get("secretManager") if isinstance(avail, dict) else None
    steps_secret_env = 0
    volumes = 0
    for s in doc.get("steps") or []:
        if not isinstance(s, dict):
            continue
        steps_secret_env += len(s.get("secretEnv") or [])
        for v in s.get("volumes") or []:
            src = (v or {}).get("secret") if isinstance(v, dict) else None
            if src:
                volumes += 1
    print("found=yes")
    print("id=%s" % word(doc.get("id"), 80))
    print("status=%s" % word(doc.get("status"), 40))
    print("account=%s" % word(doc.get("serviceAccount"), 160))
    print("available=%d" % len(asecrets or []))
    print("secret-env=%d" % steps_secret_env)
    print("secret-volumes=%d" % volumes)
else:
    print("unknown reader command: %s" % cmd, file=sys.stderr)
    sys.exit(64)
PY
}

reader() { python3 "$SCRATCH/reader" "$@" </dev/null; }

# -------------------------------------------------------------- the installation
read_config() { # read_config <file>
  python3 - "$1" <<'PY'
import json, re, sys

def unquote(v):
    v = v.strip()
    if len(v) >= 2 and v[0] == v[-1] and v[0] in "'\"":
        return v[1:-1]
    return v

want = {"name", "gcp_project", "region", "runs_bucket", "bucket_url", "budget.rtdb_url"}
out = {k: "" for k in want}
stack = []
for raw in open(sys.argv[1], encoding="utf-8"):
    line = raw.rstrip("\n")
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    line = re.sub(r"\s+#.*$", "", line)
    ind = len(line) - len(line.lstrip(" "))
    body = line.strip()
    m = re.match(r"^([A-Za-z0-9_./-]+):(?:\s+(.*))?$", body)
    if not m:
        continue
    while stack and stack[-1][0] >= ind:
        stack.pop()
    key, val = m.group(1), (m.group(2) or "").strip()
    path = ".".join([k for _, k in stack] + [key])
    if val == "":
        stack.append((ind, key))
    elif path in want:
        out[path] = unquote(val)
json.dump(out, sys.stdout)
PY
}

cfg_get() { # cfg_get <key>
  python3 -c 'import json,sys; v=json.load(open(sys.argv[1])).get(sys.argv[2],""); print(v if isinstance(v,str) else "")' "$SCRATCH/config.json" "$1" </dev/null
}

mint_token() {
  local tok
  tok=$(gcloud auth print-access-token --quiet </dev/null 2>"$SCRATCH/token.err") || return 1
  [ -n "$tok" ] || return 1
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") || return 1
  chmod 600 "$AUTH_CONF" || return 1
  unset tok
}

guard_inputs() {
  if [ "$MODE" = catalog ]; then return 0
  fi
  [ -n "$REPO" ] || stop "CHECK920_REPO must name the sandbox repository under test (owner/name)"
  printf '%s' "$REPO" | grep -Eq "$REPO_RE" || stop "CHECK920_REPO='$REPO' is not owner/name"
  [ -n "$CHECKOUT" ] || stop "CHECK920_CHECKOUT must name a checkout of ${REPO}: the review half is 'fugaro review --pr N', which resolves the repository from its own checkout's origin"
  [ -d "$CHECKOUT" ] || stop "CHECK920_CHECKOUT='$CHECKOUT' is not a directory"
  [ -n "$UNLISTED" ] || stop "CHECK920_UNLISTED_REPO must name the sandbox repository the allow-list does NOT admit"
  printf '%s' "$UNLISTED" | grep -Eq "$REPO_RE" || stop "CHECK920_UNLISTED_REPO='$UNLISTED' is not owner/name"
  [ "$UNLISTED" != "$REPO" ] || stop "CHECK920_UNLISTED_REPO must not be CHECK920_REPO: the two rows are the served repository and the refused one"
  [ -n "$SECRET" ] || stop "CHECK920_SECRET must name the workflow secret the repository declares as an environment variable"
  printf '%s' "$SECRET" | grep -Eq "$NAME_RE" || stop "CHECK920_SECRET='$SECRET' is not a workflow secret name ([a-z0-9][a-z0-9-]{0,62})"
  [ -n "$FILE_SECRET" ] || stop "CHECK920_FILE_SECRET must name the workflow secret the repository declares as a file"
  printf '%s' "$FILE_SECRET" | grep -Eq "$NAME_RE" || stop "CHECK920_FILE_SECRET='$FILE_SECRET' is not a workflow secret name"
  [ "$FILE_SECRET" != "$SECRET" ] || stop "CHECK920_FILE_SECRET must not be CHECK920_SECRET: the two rows are the environment secret and the file one"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`:
  # this is a reading on ONE installation, and run anywhere else every line below
  # is about a service, a record and repositories of another project. So a
  # disagreement STOPS the check rather than warning about it.
  say "reading the installation's identity (fugaro doctor --project ${INSTALL} --all)"
  local out rc=0
  # --all, because the `project:` line is part of doctor's informational tail: the
  # default view prints its pointer lines and stops, so a guard reading the plain
  # view would find no project line and stop every run.
  out=$(fugaro doctor --project "$INSTALL" --all 2>"$SCRATCH/doctor.err" </dev/null) || rc=$?
  if ! printf '%s\n' "$out" | grep -q "project: .*(GCP ${P},"; then
    stop "this is not the ${P} installation: 'fugaro doctor --project ${INSTALL} --all' printed no 'project: ... (GCP ${P},' line (exit ${rc}: $(first_line "$SCRATCH/doctor.err")). Either this is not the ${P} installation, or --project ${INSTALL} disagrees with this checkout's project:, or doctor itself failed (doctor is not offline: it needs usable credentials). Run 'fugaro doctor --project ${INSTALL} --all' here yourself before going on — this check runs on the ${P} installation only, never belong"
  fi
  fact project-region PASS "${P}/${L} (installation ${INSTALL}, sandbox repositories only; never belong)"
  rc=0
  local ver
  ver=$(fugaro version 2>"$SCRATCH/version.err" </dev/null) || rc=$?
  if [ -z "$ver" ]; then
    fact fugaro-version FAIL "fugaro version printed nothing (exit ${rc}): $(first_line "$SCRATCH/version.err")"
  elif printf '%s' "$ver" | grep -qi "dev\|dirty"; then
    fact fugaro-version FAIL "$(printf '%s' "$ver" | head -1 | cut -c1-120) — a development build is not the release this check is about: a checkout build resolves no released credentials-service image to deploy, and this check is about what that image does. Install the release under test and run it again"
  else
    fact fugaro-version PASS "$(printf '%s' "$ver" | head -1 | cut -c1-120)"
  fi
  rc=0
  git -C "$CHECKOUT" remote get-url origin >"$SCRATCH/origin" 2>"$SCRATCH/origin.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured review "CHECK920_CHECKOUT='${CHECKOUT}' has no readable origin ($(first_line "$SCRATCH/origin.err")), so 'fugaro review --pr N' run there would not resolve ${REPO}"
  elif ! grep -q "$REPO" "$SCRATCH/origin"; then
    unmeasured review "CHECK920_CHECKOUT='${CHECKOUT}' names origin $(first_line "$SCRATCH/origin"), which is not ${REPO}: a review launched there would review another repository's pull request"
  fi
}

# ------------------------------------------------------------------- the record
# record_read OUT: the project secret record, read from the installation's own
# Realtime Database at `creds/projectsecrets` — the path IS3 writes. The names
# in it are the workflow-secret CLASS this check is about: the run jobs may
# mount none of them, and the containers the audit lines name are
# `fugaro_p_<name>` for the names in it.
record_read() { # record_read OUT
  RTDB_CODE=$(curl -q -sS --max-time 30 --config "$AUTH_CONF" -o "$1" -w '%{http_code}' \
    "${RTDB%/}/creds/projectsecrets.json" </dev/null 2>"$SCRATCH/rtdb.err") || RTDB_CODE=000
  [ "$RTDB_CODE" = 200 ]
}

# --------------------------------------------------------------- the cloud reads
jobs_read() { # jobs_read OUT
  gcloud run jobs list --project="$P" --region="$L" --format=json --quiet </dev/null >"$1" 2>"$SCRATCH/jobs.err"
}

job_describe() { # job_describe JOB OUT
  gcloud run jobs describe "$1" --project="$P" --region="$L" --format=json --quiet </dev/null >"$2" 2>"$SCRATCH/job-${1}.err"
}

exec_describe() { # exec_describe EXEC OUT
  gcloud run jobs executions describe "$1" --project="$P" --region="$L" --format=json --quiet </dev/null >"$2" 2>"$SCRATCH/exec-${1}.err"
}

# audit_read RID OUT: the credentials service's own lines for one run, retried
# while Cloud Logging catches up — ingestion lags the request by seconds, and a
# reading taken immediately is an empty answer about a service that did serve.
# Returns 0 only when the last read ANSWERED; a read that exited non-zero left
# OUT empty or half written, and a caller that took that for the service's
# silence would read the absent line as the answer it is looking for.
audit_read() { # audit_read RID OUT
  local rid=$1 out=$2 waited=0 rc=0 filter
  filter='resource.type="cloud_run_revision" resource.labels.service_name="'"$SVC"'" jsonPayload.run="'"$rid"'"'
  while :; do
    rc=0
    gcloud logging read "$filter" --project="$P" --freshness="$LOG_AGE" \
      --limit="$LOG_LIMIT" --format=json --quiet </dev/null >"$out" 2>"$SCRATCH/audit.err" || rc=$?
    if [ "$rc" -eq 0 ]; then return 0; fi
    if [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
      printf '%s' "'gcloud logging read' for the service's own lines of run ${rid} exited ${rc} and printed: $(last_lines "$SCRATCH/audit.err"). A read that did not answer is NOT the service having written no line: nothing here says whether that run was served. Read the log yourself and run this check again" >"$out.unread"
      return 1
    fi
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
}

# --------------------------------------------------------------------- the runs
run_id_for() { # run_id_for LABEL
  case "$1" in
  one) printf '%s-0920' "$RUN_BASE" ;;
  review) printf '%s-0921' "$RUN_BASE" ;;
  unlisted) printf '%s-0922' "$RUN_BASE" ;;
  *) die "Check 920 has no run id for the label '$1'" ;;
  esac
}

task_write() { # task_write FILE LABEL RID
  {
    printf '# Task: Check 920 — the %s run (%s)\n\n' "$2" "$3"
    printf 'Add one line to `CHK920.md` at the repository root:\n\n'
    printf '    check 920 %s %s\n\n' "$2" "$3"
    printf 'Commit it on your own branch and open a pull request for it, then stop.\n'
    printf 'Change nothing else in the repository.\n'
  } >"$1"
}

# poll_row LABEL RID REPO: the run's own row, polled until the listing's own
# `terminal` word is yes. `fugaro run` returns AT LAUNCH, so a row read
# straight after a launch is an in-flight row, which is a FAIL no run earned.
poll_row() { # poll_row LABEL RID REPO
  local label=$1 rid=$2 repo=$3 waited=0 row=""
  while :; do
    row=""
    if fugaro runs ls --repo "$repo" --project "$INSTALL" --no-cache --json >"$SCRATCH/rows-$label.json" 2>>"$SCRATCH/rows-$label.err"; then
      row=$(reader run "$SCRATCH/rows-$label.json" "$rid" || true)
    fi
    if [ "$(field_of found "$row")" = yes ] && [ "$(field_of terminal "$row")" = yes ]; then break; fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  printf '%s\n' "$row" >"$SCRATCH/row-$label"
  printf '%s' "$waited" >"$SCRATCH/row-$label.waited"
}

row_of() { cat "$SCRATCH/row-$1" 2>/dev/null || true; }
row_waited() { cat "$SCRATCH/row-$1.waited" 2>/dev/null || true; }

# launch_run LABEL RID REPO: one throwaway run, its row polled. Returns 1 with
# a reason in $SCRATCH/launch-err when it could not be launched or its row never
# became terminal — the callers print that as the row's own unmeasured reason
# rather than as a criterion that did not hold.
launch_run() { # launch_run LABEL RID REPO
  local label=$1 rid=$2 repo=$3 rc=0
  printf '%s' "" >"$SCRATCH/launch-err"
  task_write "$SCRATCH/task-$label" "$label" "$rid"
  say "launching the ${label} run (${rid}) in ${repo} — it goes on in the cloud"
  fugaro run --repo "$repo" --project "$INSTALL" --run-id "$rid" \
    --title "Check 920: ${label}" --task-file "$SCRATCH/task-$label" --json \
    >"$SCRATCH/run-$label.json" 2>"$SCRATCH/run-$label.err" </dev/null || rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '%s' "'fugaro run' for the ${label} run (${rid}) exited ${rc} and never launched one: $(last_lines "$SCRATCH/run-$label.err")" >"$SCRATCH/launch-err"
    return 1
  fi
  poll_row "$label" "$rid" "$repo"
  local row; row=$(row_of "$label")
  if [ "$(field_of found "$row")" != yes ]; then
    printf '%s' "the ${label} run (${rid}) does not appear in 'fugaro runs ls --repo ${repo}' after $(row_waited "$label")s: it was launched but its row is not in the listing, so nothing about it is read" >"$SCRATCH/launch-err"
    return 1
  fi
  if [ "$(field_of terminal "$row")" != yes ]; then
    printf '%s' "the ${label} run (${rid}) had not reached a final status after $(row_waited "$label")s: status=$(field_of status "$row"). A row still in flight is not an answer about the credentials service — raise CHECK920_RUN_SECONDS, or read that run's row yourself" >"$SCRATCH/launch-err"
    return 1
  fi
  return 0
}

launch_err() { cat "$SCRATCH/launch-err" 2>/dev/null || true; }

# ------------------------------------------------------------------ mode: the run
step_record() {
  if ! record_read "$SCRATCH/projectsecrets.json"; then
    unmeasured record "the project secret record could not be read from ${RTDB%/}/creds/projectsecrets.json (HTTP ${RTDB_CODE:-none}: $(first_line "$SCRATCH/rtdb.err")). It is what says which names are workflow secrets, and a check that guessed the class would call a mount of something else a failure of this one. Run 'fugaro update' as the project's Owner, or read the node yourself"
    RECORD_OK=no
    return 0
  fi
  RECORD_OK=yes
  reader names "$SCRATCH/projectsecrets.json" >"$SCRATCH/names" || true
  local n
  n=$(sed -n 's/^count=//p' "$SCRATCH/names" | head -n 1)
  NAMES=$(sed -n 's/^name=//p' "$SCRATCH/names" | tr '\n' ' ')
  if [ "${n:-0}" = 0 ]; then
    unmeasured record "${RTDB%/}/creds/projectsecrets.json holds no workflow-secret name: every row below is about a class the record does not name yet. Allow the secret ('fugaro secrets allow ${SECRET} ${REPO}') and run 'fugaro update' as the project's Owner, then this check again"
    return 0
  fi
  local missing=""
  for want in "$SECRET" "$FILE_SECRET"; do
    printf '%s\n' "$NAMES" | tr ' ' '\n' | grep -qx "$want" || missing="${missing}${missing:+, }$want"
  done
  if [ -n "$missing" ]; then
    unmeasured record "the record names ${n} workflow secret(s) and does NOT name ${missing}: those are not a class this installation serves yet, so nothing below measures them. Store the value, allow it ('fugaro secrets allow ${SECRET} ${REPO}') and run 'fugaro update' as the project's Owner, then this check again"
    return 0
  fi
  fact record PASS "${RTDB%/}/creds/projectsecrets.json read: ${n} workflow secret(s) — $(printf '%s' "$NAMES" | cut -c1-200) — including both secrets under test"
}

# is_workflow_secret NAME: whether the project record named NAME as a workflow
# secret. The `fugaro_p_` grammar is ALSO the dynamic-delivery grammar for an
# admin-shared project credential (reconciliation F10), and the two classes are
# told apart by the container's own marker label (IS4) — which is why the
# reading below is the record's list of names and never the prefix alone: a job
# mounting a shared credential is not a job mounting a workflow secret, and a
# row that failed on the prefix would report the flip broken on an installation
# where it is not.
is_workflow_secret() {
  [ "$RECORD_OK" = yes ] || return 1
  printf '%s\n' "$NAMES" | tr ' ' '\n' | grep -qx "$1"
}

step_jobs() {
  if ! jobs_read "$SCRATCH/jobs.json"; then
    unmeasured jobs "'gcloud run jobs list --project=${P} --region=${L} --format=json' exited non-zero and printed: $(last_lines "$SCRATCH/jobs.err"). A read that did not answer is not an installation with no job, and that is exactly the question this row asks"
    return 0
  fi
  reader mounts "$SCRATCH/jobs.json" >"$SCRATCH/mounts" || {
    unmeasured jobs "the job list could not be read"
    return 0
  }
  local jobs mounts bad="" others="" job var cid name
  jobs=$(sed -n 's/^jobs=//p' "$SCRATCH/mounts" | head -n 1)
  mounts=$(sed -n 's/^mounts=//p' "$SCRATCH/mounts" | head -n 1)
  while IFS='|' read -r job var cid; do
    [ -n "$cid" ] || continue
    [ "${cid#fugaro_p_}" != "$cid" ] || continue
    name=${cid#fugaro_p_}
    if is_workflow_secret "$name"; then
      bad="${bad}${bad:+, }${job} mounts ${cid} as ${var}"
    else
      others="${others}${others:+, }${job} mounts ${cid} as ${var}"
    fi
  done < <(sed -n 's/^mount=//p' "$SCRATCH/mounts")
  if [ -n "$bad" ]; then
    fact jobs FAIL "${jobs} deployed job(s), ${mounts} Secret Manager mount(s), and ${bad} — a workflow secret. After the flip no run job mounts one: the run fetches it from the credentials service at bootstrap. A mount left behind is the plan's G6 broken: run 'fugaro update --repo' for that repository, and 'fugaro doctor' lists any job that still mounts one"
  elif [ -n "$others" ] && [ "$RECORD_OK" != yes ]; then
    unmeasured jobs "${jobs} deployed job(s) and ${mounts} Secret Manager mount(s): ${others}. The delivery grammar is also the one an admin-shared project credential uses (F10), the record that tells the two apart could not be read (see the record row), so whether any of those is a workflow secret is NOT settled here — read the record and run this check again"
  elif [ -n "$others" ]; then
    fact jobs PASS "${jobs} deployed job(s) and ${mounts} Secret Manager mount(s): ${others} — and the record does NOT name any of them, so they are shared credentials of the delivery grammar and not the workflow-secret class this row is about"
  else
    fact jobs PASS "${jobs} deployed job(s), ${mounts} Secret Manager mount(s), and none of them is a fugaro_p_ container at all: no run job mounts a workflow secret"
  fi
}

step_run() {
  RUN_ONE_ID=$(run_id_for one)
  if ! launch_run one "$RUN_ONE_ID" "$REPO"; then
    unmeasured run-one "$(launch_err)"
    unmeasured run-mounts "$(launch_err)"
    unmeasured run-verify "$(launch_err)"
    unmeasured audited "$(launch_err)"
    return 0
  fi
  local row status pr
  row=$(row_of one)
  status=$(field_of status "$row")
  pr=$(field_of pr "$row")
  RUN_ONE_SUCCEEDED=no
  if [ "$status" != succeeded ]; then
    fact run-one FAIL "the run ${RUN_ONE_ID} of ${REPO} ended '${status}' (outcome $(field_of outcome "$row"), reason $(field_of reason "$row")): a vertex repository's run must fetch its workflow secrets at bootstrap and finish. A run that could not is the fetch failing — read its own lines and the service's"
  elif [ -z "$pr" ] || [ "$pr" = 0 ]; then
    fact run-one FAIL "the run ${RUN_ONE_ID} of ${REPO} ended succeeded with no pull request: the review half below is a review OF the run's pull request, so a run with none leaves that row unmeasured. Read the run's row yourself"
  else
    RUN_ONE_SUCCEEDED=yes
    fact run-one PASS "the run ${RUN_ONE_ID} of the vertex repository ${REPO} succeeded on pull request #${pr} (execution $(field_of exec "$row"))"
  fi
  RUN_ONE_JOB=$(field_of job "$row")
  RUN_ONE_EXEC=$(field_of exec "$row")
  RUN_ONE_PR=$pr
  step_run_mounts "$row"
  step_run_verify
  step_audited "$RUN_ONE_ID" "$SECRET $FILE_SECRET" 2
}

step_run_mounts() { # step_run_mounts <row>
  local row=$1 job
  job=$(field_of job "$row")
  if [ -z "$job" ] || [ -z "$RUN_ONE_EXEC" ]; then
    unmeasured run-mounts "the run's row names no execution (execution='$(field_of execution "$row")'), so the job and the execution it ran on cannot be read, and this row is that job's own spec read against the two containers"
    return 0
  fi
  if ! job_describe "$job" "$SCRATCH/job-one.json"; then
    unmeasured run-mounts "'gcloud run jobs describe ${job} --project=${P} --region=${L} --format=json' exited non-zero and printed: $(last_lines "$SCRATCH/job-${job}.err")"
    return 0
  fi
  if ! exec_describe "$RUN_ONE_EXEC" "$SCRATCH/exec-one.json"; then
    unmeasured run-mounts "'gcloud run jobs executions describe ${RUN_ONE_EXEC} --project=${P} --region=${L} --format=json' exited non-zero and printed: $(last_lines "$SCRATCH/exec-${RUN_ONE_EXEC}.err")"
    return 0
  fi
  local bad="" doc file account="" accounts="" cid name
  for file in job-one exec-one; do
    reader pod "$SCRATCH/$file.json" >"$SCRATCH/pod-$file" || true
    # BOTH documents' accounts: a job's own template and the execution of it can
    # name different accounts (Cloud Run overrides the execution's), and a row
    # that read only the first would miss the one the run really ran as.
    account=$(sed -n 's/^account=//p' "$SCRATCH/pod-$file" | head -n 1)
    accounts="${accounts}${accounts:+, }${file}=${account}"
    if [ "$account" = "${IDENTITY_ID}@${P}.iam.gserviceaccount.com" ]; then
      bad="${bad}${bad:+, }${file} runs as the BUILD identity ${account}"
    fi
    while IFS='|' read -r var cid; do
      [ -n "$cid" ] || continue
      case "$cid" in
      "fugaro_p_${SECRET}" | "fugaro_p_${FILE_SECRET}")
        bad="${bad}${bad:+, }${file} feeds ${var} from ${cid}"
        continue
        ;;
      esac
      [ "${cid#fugaro_p_}" != "$cid" ] || continue
      name=${cid#fugaro_p_}
      is_workflow_secret "$name" && bad="${bad}${bad:+, }${file} feeds ${var} from ${cid}"
    done < <(sed -n 's/^mount=//p' "$SCRATCH/pod-$file")
  done
  if [ -n "$bad" ]; then
    fact run-mounts FAIL "${bad}. The run is served at bootstrap and its job carries no container: a spec that still names one is the mount the flip removed (G6), and a run whose containers run as the build identity is a run reading the service's own store"
  else
    fact run-mounts PASS "neither the job ${job} nor the execution ${RUN_ONE_EXEC} the run ${RUN_ONE_ID} ran on is fed from fugaro_p_${SECRET} or fugaro_p_${FILE_SECRET} in any container, and neither runs as the build identity (${accounts}): the value reached the run from the service and from nowhere else"
  fi
}

step_run_verify() {
  if [ "$RUN_ONE_SUCCEEDED" != yes ]; then
    unmeasured run-verify "the run ${RUN_ONE_ID} did not succeed (see the run-one row), so whether its own commands passed is not the reading this row is after: the verify record is the file row's evidence only on a run that finished its own work"
    return 0
  fi
  local slug rc=0
  slug=$(reader slug "$SCRATCH/jobs.json" "$REPO" 2>/dev/null | sed -n 's/^slug=//p' || true)
  if [ -z "$slug" ]; then
    slug=$(printf '%s' "$REPO" | tr '/A-Z' '-a-z')
  fi
  if ! gcloud storage cat "gs://${BUCKET}/runs/${slug}/${RUN_ONE_ID}/result.json" --quiet </dev/null \
    >"$SCRATCH/result-one.json" 2>"$SCRATCH/result.err"; then
    unmeasured run-verify "the run's own record could not be read ('gcloud storage cat gs://${BUCKET}/runs/${slug}/${RUN_ONE_ID}/result.json' exited non-zero: $(last_lines "$SCRATCH/result.err")). It is where the run's verify records are, and a verify that is not read is not one that passed"
    return 0
  fi
  reader verify "$SCRATCH/result-one.json" >"$SCRATCH/verify-one" || { unmeasured run-verify "the run's own record could not be parsed"; return 0; }
  # `|| true` is not decoration: a `grep` with no match exits 1, and under
  # `set -e -o pipefail` a command substitution that exits 1 inside an
  # assignment trips the ERR trap — so without it a run whose record holds no
  # verify at all would stop the whole check instead of failing this one row.
  local last
  last=$(grep '^verify=' "$SCRATCH/verify-one" | tail -n 1 || true)
  if [ -z "$last" ]; then
    fact run-verify FAIL "the run ${RUN_ONE_ID}'s record holds no verify record at all: nothing shows its own commands ran. The file row is read through that verify — the steps file makes it fail unless the file secret is a 0400, non-empty file at the path its variable names"
    return 0
  fi
  last=${last#verify=}
  local n passed clean
  n=${last%%|*}; last=${last#*|}
  passed=${last%%|*}; last=${last#*|}
  clean=${last%%|*}
  if [ "$passed" != passed ]; then
    fact run-verify FAIL "the run ${RUN_ONE_ID}'s last verify record (#${n}) reads '${passed}': a run whose own commands failed is not a run whose file secret arrived. Read that run's transcript and its verify record"
  elif [ "$clean" != clean ]; then
    fact run-verify FAIL "the run ${RUN_ONE_ID}'s last verify record (#${n}) passed on a DIRTY tree: the verify the owner's steps write reads the file's mode, and one that ran over uncommitted work is not the reading this row is after"
  else
    fact run-verify PASS "the run ${RUN_ONE_ID}'s last verify (#${n}) passed on a clean tree; the steps file makes that verify fail unless the file secret arrived as a 0400, non-empty file whose path is the value of its variable (D3327, G3), so its passing IS that arrival"
  fi
}

# step_audited RID SECRETS WANT: the service's own lines for one run. WANT is
# the number of fetches the run declares, so the reading is exactly "one line
# per fetch": every workflow-secret act of the run is counted, each must name
# one of the two containers, and the count must be WANT.
step_audited() { # step_audited RID "SECRET FILE_SECRET" WANT
  local rid=$1 secrets=$2 want=$3 lines got
  lines="$SCRATCH/audit-$rid.json"
  if ! audit_read "$rid" "$lines"; then
    unmeasured audited "$(cat "$lines.unread" 2>/dev/null)"
    return 0
  fi
  got=$(reader audit "$lines" "$rid" 2>/dev/null | sed -n 's/^audit=[^|]*|\([^|]*\)|\([^|]*\)|.*/\1|\2/p' || true)
  local fetch="${got}"$'\n'
  fetch=$(printf '%s\n' "$got" | awk -F'|' '$2 == "workflow-secret" { print $1 }')
  local bad="" c n=0 one
  for one in $secrets; do
    c=$(printf '%s\n' "$fetch" | grep -cx "fugaro_p_${one}" || true)
    n=$((n + ${c:-0}))
    if [ "${c:-0}" -eq 0 ]; then
      bad="${bad}${bad:+, }no line for fugaro_p_${one}"
    elif [ "${c:-0}" -gt 1 ]; then
      bad="${bad}${bad:+, }${c} lines for fugaro_p_${one} (one fetch, one line)"
    fi
  done
  local other
  other=$(printf '%s\n' "$fetch" | grep -v -e "^fugaro_p_${SECRET}$" -e "^fugaro_p_${FILE_SECRET}$" | grep -c . || true)
  if [ -n "$bad" ]; then
    fact audited FAIL "the service's lines for the run ${rid} under purpose workflow-secret read: ${bad}. The run declares ${want} secret(s) and fetches each once at bootstrap, so the service writes one line per fetch, naming the container it read (G18: a line is a name, never a value). Read the log yourself with the run's id"
  elif [ "${other:-0}" -gt 0 ]; then
    fact audited FAIL "the service's lines for the run ${rid} hold ${other} workflow-secret act(s) naming a container that is neither fugaro_p_${SECRET} nor fugaro_p_${FILE_SECRET}: a line this check cannot account for is not a pass. Read the log yourself with the run's id"
  else
    fact audited PASS "the service's audit log holds exactly one workflow-secret line per fetch for the run ${rid} (${want} lines), each naming one container: fugaro_p_${SECRET} and fugaro_p_${FILE_SECRET}"
  fi
}

step_review() {
  if [ -z "$RUN_ONE_PR" ] || [ "$RUN_ONE_PR" = 0 ]; then
    unmeasured review "the run ${RUN_ONE_ID} opened no pull request, so there is nothing to review: this row is a review OF that pull request, which must be served neither file"
    return 0
  fi
  local rid rc=0
  rid=$(run_id_for review)
  say "launching the review of pull request #${RUN_ONE_PR} (${rid}) from ${CHECKOUT} — it goes on in the cloud"
  ( cd "$CHECKOUT" && fugaro review --pr "$RUN_ONE_PR" --project "$INSTALL" --run-id "$rid" --json \
      >"$SCRATCH/review.json" 2>"$SCRATCH/review.err" </dev/null ) || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured review "'fugaro review --pr ${RUN_ONE_PR}' exited ${rc} and launched no review: $(last_lines "$SCRATCH/review.err")"
    return 0
  fi
  poll_row review "$rid" "$REPO"
  local row; row=$(row_of review)
  if [ "$(field_of found "$row")" != yes ] || [ "$(field_of terminal "$row")" != yes ]; then
    unmeasured review "the review run (${rid}) has no terminal row after $(row_waited review)s: $(launch_err). A review that never finished is not one that was served no secret"
    return 0
  fi
  local status bad="" job ex name
  status=$(field_of status "$row")
  if [ "$status" != succeeded ]; then
    fact review FAIL "the review of pull request #${RUN_ONE_PR} (${rid}) ended '${status}' (reason $(field_of reason "$row")): the review must run and finish with no workflow secret on it. Read its row and the service's lines for it"
    return 0
  fi
  job=$(field_of job "$row"); ex=$(field_of exec "$row")
  if [ -n "$job" ] && [ -n "$ex" ]; then
    if job_describe "$job" "$SCRATCH/job-review.json" && exec_describe "$ex" "$SCRATCH/exec-review.json"; then
      local file cid
      for file in job-review exec-review; do
        reader pod "$SCRATCH/$file.json" >"$SCRATCH/pod-$file" || true
        while IFS='|' read -r var cid; do
          [ -n "$cid" ] || continue
          case "$cid" in
          "fugaro_p_${SECRET}" | "fugaro_p_${FILE_SECRET}")
            bad="${bad}${bad:+, }${file} feeds ${var} from ${cid}"
            continue
            ;;
          esac
          [ "${cid#fugaro_p_}" != "$cid" ] || continue
          name=${cid#fugaro_p_}
          is_workflow_secret "$name" && bad="${bad}${bad:+, }${file} feeds ${var} from ${cid}"
        done < <(sed -n 's/^mount=//p' "$SCRATCH/pod-$file")
      done
    else
      unmeasured review "the review run's job ${job} or execution ${ex} could not be described, so what it carried is not read"
      return 0
    fi
  fi
  # The reader prints `audit=<status>|<container>|<purpose>|<caller>`, so the
  # purpose is the THIRD field and the container the second: an awk keyed on a
  # bare `audit` in the first field would match no line at all, and this row
  # would pass a review the service had served.
  local lines served
  lines="$SCRATCH/audit-$rid.json"
  if ! audit_read "$rid" "$lines"; then
    unmeasured review "$(cat "$lines.unread" 2>/dev/null)"
    return 0
  fi
  served=$(reader audit "$lines" "$rid" 2>/dev/null | awk -F'|' '$1 ~ /^audit=/ && $3 == "workflow-secret" { print $2 }' | grep -c . || true)
  if [ -n "$bad" ]; then
    fact review FAIL "the review of pull request #${RUN_ONE_PR} (${rid}) carries a workflow secret: ${bad}. A review run reads an untrusted checkout and prompt, so it is served no secret and holds none — the file is absent in it (G3, R5)"
  elif [ "${served:-0}" -gt 0 ]; then
    fact review FAIL "the service served the review run (${rid}) ${served} workflow-secret act(s): a review run must be refused the name (G5), and a file written in one is a value an untrusted checkout can read. Read the log yourself with that run's id"
  else
    fact review PASS "the review of pull request #${RUN_ONE_PR} (${rid}) finished with neither container on its job or execution and no workflow-secret line in the service's log: the file secret was absent in it"
  fi
}

step_unlisted() {
  local rid status lines
  rid=$(run_id_for unlisted)
  if ! launch_run unlisted "$rid" "$UNLISTED"; then
    unmeasured unlisted "$(launch_err)"
    return 0
  fi
  local row; row=$(row_of unlisted)
  status=$(field_of status "$row")
  if [ "$status" = succeeded ]; then
    fact unlisted FAIL "the run ${rid} of ${UNLISTED}, which the secret's allow-list does not admit, SUCCEEDED: the repository was served a secret it is not listed for. Read the record ('gcloud auth print-access-token' and a GET of ${RTDB%/}/creds/projectsecrets.json) and the service's lines for that run"
    return 0
  fi
  lines="$SCRATCH/audit-$rid.json"
  if ! audit_read "$rid" "$lines"; then
    unmeasured unlisted "the run ${rid} of ${UNLISTED} did not succeed (status ${status}), which is the refusal, but the service's own lines for it could not be read: $(cat "$lines.unread" 2>/dev/null)"
    return 0
  fi
  local sentences
  sentences=$(reader audit "$lines" "$rid" 2>/dev/null | sed -n 's/^refused=[^|]*|//p' || true)
  if [ -z "$sentences" ]; then
    unmeasured unlisted "the run ${rid} of ${UNLISTED} ended '${status}' and the service's log holds no refusal for it: a run that failed without a refusal is not this row (its own lines say why it failed — read them). Nothing here says an unlisted repository was refused by the service"
    return 0
  fi
  if printf '%s\n' "$sentences" | grep -q "fugaro secrets allow"; then
    fact unlisted PASS "the run ${rid} of ${UNLISTED} ended '${status}' and the service refused it with the fix: $(printf '%s' "$sentences" | head -1 | cut -c1-200)"
  else
    fact unlisted FAIL "the run ${rid} of ${UNLISTED} was refused, but with no fix in the sentence: $(printf '%s' "$sentences" | head -1 | cut -c1-200). An unlisted repository is refused TYPED, naming 'fugaro secrets allow NAME owner/repo' (G1, D3328)"
  fi
}

run_920() {
  say "Check 920: the workflow-secret class off the installation ${INSTALL} (project ${P})"
  step_record
  step_jobs
  step_run
  step_unlisted
  step_review
}

# --------------------------------------------------------------- mode: catalog
step_identity() {
  local email="${IDENTITY_ID}@${P}.iam.gserviceaccount.com"
  IDENTITY_EMAIL=$email
  if ! gcloud iam service-accounts list --project="$P" --format=json --quiet </dev/null \
    >"$SCRATCH/sa.json" 2>"$SCRATCH/sa.err"; then
    unmeasured build-identity "'gcloud iam service-accounts list --project=${P}' exited non-zero and printed: $(last_lines "$SCRATCH/sa.err")"
    IDENTITY_OK=no
    return 0
  fi
  reader sa "$SCRATCH/sa.json" "$email" >"$SCRATCH/sa-one" || true
  if [ "$(sed -n 's/^found=//p' "$SCRATCH/sa-one" | head -n 1)" != yes ]; then
    unmeasured build-identity "${email} is not an account in ${P}: either the installation runs no credentials service (it is created with it), or it is named differently here. Nothing of this mode's is measured without it"
    IDENTITY_OK=no
    return 0
  fi
  IDENTITY_OK=yes
  fact build-identity PASS "${email} exists in ${P} ($(sed -n 's/^display=//p' "$SCRATCH/sa-one" | head -n 1)): it is the ONE account a catalog build runs as, so what it may read is what a catalog build may read"
}

step_accessors() {
  local email=$IDENTITY_EMAIL bad="" read_ok=yes n=0
  say "reading the project IAM policy of ${P}"
  if gcloud projects get-iam-policy "$P" --format=json --quiet </dev/null >"$SCRATCH/iam-project.json" 2>"$SCRATCH/iam-project.err"; then
    reader iam "$SCRATCH/iam-project.json" "$email" >"$SCRATCH/iam-project-one" || true
    while IFS='|' read -r role member; do
      [ -n "$role" ] || continue
      case "$role" in
      roles/secretmanager.secretAccessor | roles/secretmanager.admin | roles/owner | roles/editor)
        bad="${bad}${bad:+, }the project grants it ${role}"
        ;;
      esac
    done < <(sed -n 's/^grant=//p' "$SCRATCH/iam-project-one")
  else
    read_ok=no
    bad="${bad}${bad:+, }the project's IAM policy could not be read ($(first_line "$SCRATCH/iam-project.err"))"
  fi
  say "listing the Secret Manager containers of ${P}"
  if ! gcloud secrets list --project="$P" --format=json --quiet </dev/null >"$SCRATCH/secrets.json" 2>"$SCRATCH/secrets.err"; then
    unmeasured identity-accessors "the Secret Manager containers of ${P} could not be listed: $(last_lines "$SCRATCH/secrets.err"). A read that did not answer is not an identity with no accessor"
    return 0
  fi
  local names
  names=$(python3 -c 'import json,sys; print("\n".join(sorted(s.get("name","").split("/")[-1] for s in (json.load(open(sys.argv[1])) or []) if s.get("name"))))' "$SCRATCH/secrets.json" </dev/null || true)
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    n=$((n + 1))
    if [ "$n" -gt "$SECRET_LIMIT" ]; then
      unmeasured identity-accessors "the project holds more than ${SECRET_LIMIT} Secret Manager containers and only the first ${SECRET_LIMIT} were read (raise CHECK920_SECRET_LIMIT): an unread container is not one the identity cannot read"
      return 0
    fi
    if ! gcloud secrets get-iam-policy "$name" --project="$P" --format=json --quiet </dev/null \
      >"$SCRATCH/iam-$name.json" 2>"$SCRATCH/iam-$name.err"; then
      unmeasured identity-accessors "the IAM policy of the container ${name} could not be read: $(last_lines "$SCRATCH/iam-$name.err"). A read that did not answer is not a container the identity cannot read"
      return 0
    fi
    reader iam "$SCRATCH/iam-$name.json" "$email" >"$SCRATCH/iam-one" || true
    while IFS='|' read -r role member; do
      [ -n "$role" ] || continue
      case "$role" in
      roles/secretmanager.secretAccessor | roles/secretmanager.admin)
        bad="${bad}${bad:+, }the container ${name} grants it ${role}"
        ;;
      esac
    done < <(sed -n 's/^grant=//p' "$SCRATCH/iam-one")
  done <<<"$names"
  if [ -n "$bad" ]; then
    fact identity-accessors FAIL "${bad}. After IS8 the catalog build reads its git credential from the credentials service and the identity holds NO accessor: D3330's whole point is that nothing but the service reads Secret Manager (spec §6, G6)"
  elif [ "$read_ok" != yes ]; then
    unmeasured identity-accessors "${bad}"
  else
    fact identity-accessors PASS "read ${n} container policy(ies) and the project's own: the build identity ${email} holds no Secret Manager accessor anywhere"
  fi
}

step_catalog_build() {
  local email=$IDENTITY_EMAIL id=""
  say "listing the recent builds of ${P} (region ${L})"
  if ! gcloud builds list --project="$P" --region="$L" --limit=50 --format=json --quiet </dev/null \
    >"$SCRATCH/builds.json" 2>"$SCRATCH/builds.err"; then
    unmeasured catalog-build "'gcloud builds list --project=${P} --region=${L}' exited non-zero and printed: $(last_lines "$SCRATCH/builds.err")"
    return 0
  fi
  reader builds "$SCRATCH/builds.json" "$email" >"$SCRATCH/builds-one" || true
  if [ -n "$CATALOG_BUILD" ]; then
    id=$CATALOG_BUILD
  else
    id=$(sed -n 's/^build=\([^|]*\)|.*/\1/p' "$SCRATCH/builds-one" | head -n 1)
  fi
  if [ -z "$id" ]; then
    unmeasured catalog-build "none of the last 50 builds of ${P} ran as ${email}: nothing here shows a catalog build at all. Run one (the catalog's own entry build), or name one with CHECK920_CATALOG_BUILD"
    return 0
  fi
  if ! gcloud builds describe "$id" --project="$P" --region="$L" --format=json --quiet </dev/null \
    >"$SCRATCH/build.json" 2>"$SCRATCH/build.err"; then
    unmeasured catalog-build "the build ${id} could not be described: $(last_lines "$SCRATCH/build.err")"
    return 0
  fi
  reader build "$SCRATCH/build.json" >"$SCRATCH/build-one" || { unmeasured catalog-build "the build ${id}'s document could not be parsed"; return 0; }
  local status account avail senv svol
  status=$(sed -n 's/^status=//p' "$SCRATCH/build-one" | head -n 1)
  account=$(sed -n 's/^account=//p' "$SCRATCH/build-one" | head -n 1)
  avail=$(sed -n 's/^available=//p' "$SCRATCH/build-one" | head -n 1)
  senv=$(sed -n 's/^secret-env=//p' "$SCRATCH/build-one" | head -n 1)
  svol=$(sed -n 's/^secret-volumes=//p' "$SCRATCH/build-one" | head -n 1)
  local bad=""
  [ "$status" = SUCCESS ] || bad="${bad}${bad:+, }its status is ${status}"
  [ "$account" = "$email" ] || bad="${bad}${bad:+, }it runs as ${account} and not as ${email}"
  [ "${avail:-0}" = 0 ] || bad="${bad}${bad:+, }its request carries ${avail} available secret(s)"
  [ "${senv:-0}" = 0 ] || bad="${bad}${bad:+, }${senv} step(s) read a secret into their environment"
  [ "${svol:-0}" = 0 ] || bad="${bad}${bad:+, }${svol} step(s) mount a secret as a volume"
  if [ -n "$bad" ]; then
    fact catalog-build FAIL "the catalog build ${id}: ${bad}. A catalog build reads its git credential from the credentials service (IS8, D3330), so it succeeds with no availableSecret and no secret on a step"
  else
    fact catalog-build PASS "the catalog build ${id} ran as ${email} and reached SUCCESS with no available secret, no secret in a step's environment and no secret volume"
  fi
}

run_921() {
  CHECK_NO=921
  MODE=catalog
  say "Check 921: the catalog build and its identity, off the installation ${INSTALL} (project ${P})"
  step_identity
  [ "${IDENTITY_OK:-no}" = yes ] || {
    unmeasured identity-accessors "the build identity is not there (see the build-identity row), so nothing it holds was read"
    unmeasured catalog-build "the build identity is not there (see the build-identity row), so no build of its was read"
    return 0
  }
  step_accessors
  step_catalog_build
}

# ------------------------------------------------------------------ the plan
print_plan() {
  local empty="" n
  say "dry run: NOTHING below was run, and no tool was called at all — not gcloud, not fugaro, not curl. A real run would:"
  if [ "$MODE" = run ]; then
    for n in CHECK920_REPO CHECK920_CHECKOUT CHECK920_UNLISTED_REPO CHECK920_SECRET CHECK920_FILE_SECRET; do
      eval "[ -n \"\${$n:-}\" ]" || empty="${empty}${empty:+, }$n"
    done
    [ -z "$empty" ] || say "note: ${empty} — unset right now, and a real run stops before its first step when any of them is. An input is not a credential: reading it here, where nothing is called, costs nothing"
  fi
  if [ "$MODE" = catalog ]; then
    say "  [network] fugaro doctor --project ${INSTALL} --all and fugaro version  (this is the ${P} installation, and the binary is not a development build)"
    say "  [network] gcloud projects get-iam-policy ${P} --format=json  (the project-level grants, checked for a Secret Manager accessor held by the build identity)"
    say "  [network] gcloud iam service-accounts list --project=${P} --format=json  (the account ${IDENTITY_ID}@${P}.iam.gserviceaccount.com)"
    say "  [network] gcloud secrets list --project=${P} --format=json, then gcloud secrets get-iam-policy <each container> --format=json  (every container's own policy, checked the same way)"
    say "  [network] gcloud builds list --project=${P} --region=${L} --limit=50 --format=json, then gcloud builds describe <the build ${CHECK920_CATALOG_BUILD:-<newest one run as the identity>}> --format=json  (its status, its serviceAccount, and its availableSecrets, secretEnv and secret volumes)"
    say "and its cleanup would run: nothing. It creates no cloud resource and writes no record: the scratch directory is the only thing it makes, and its own EXIT trap removes it."
    return 0
  fi
  say "  [network] fugaro doctor --project ${INSTALL} --all and fugaro version  (this is the ${P} installation, and the binary is not a development build)"
  say "  [network] gcloud auth print-access-token  (one token, into a 0600 curl config file in the scratch directory, for the record read below; never printed and never on an argv)"
  say "  [network] curl GET <the installation's Realtime Database>/creds/projectsecrets.json  (the workflow-secret names — the class this check is about — and the two secrets under test must be in it)"
  say "  [network] gcloud run jobs list --project=${P} --region=${L} --format=json  (every deployed job: NONE of them may mount a fugaro_p_<name> container)"
  say "  [network, writes] fugaro run --repo ${REPO:-<the repository under test>} --project ${INSTALL} --run-id <today>-0920 --title 'Check 920: one' --task-file <task> --json  (one throwaway run in the VERTEX repository: it writes one line to CHK920.md, commits and opens a pull request)"
  say "  [network] fugaro runs ls --repo ${REPO:-<the repository under test>} --no-cache --json  (its row, polled until terminal: status, outcome and its pull request)"
  say "  [network] gcloud run jobs describe <the job it ran on>, gcloud run jobs executions describe <its execution>  (neither may feed any variable from fugaro_p_${SECRET:-<secret>} or fugaro_p_${FILE_SECRET:-<file secret>})"
  say "  [network] gcloud storage cat gs://<the runs bucket>/runs/<slug>/<run id>/result.json  (the run's own verify records: the steps file makes its verify fail unless the file secret is a 0400 non-empty file at the path its variable names)"
  say "  [network] gcloud logging read 'resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${SVC}\" jsonPayload.run=\"<run id>\"'  (the service's own lines: exactly one workflow-secret act per fetch, each naming a fugaro_p_ container)"
  say "  [network, writes] the same launch in ${UNLISTED:-<the repository the allow-list does not admit>} (<today>-0922), expected to FAIL its bootstrap: the service refuses it, and the sentence names the fix, 'fugaro secrets allow'"
  say "  [network] gcloud logging read for that run too  (its refusal sentence)"
  say "  [network, writes] cd ${CHECKOUT:-<a checkout of the repository under test>} && fugaro review --pr <the pull request> --project ${INSTALL} --run-id <today>-0921 --json  (a review run of its own, which must carry no workflow secret and have no workflow-secret line)"
  say "  [network] gcloud run jobs describe / executions describe for the review's job and execution, and gcloud logging read for its run id  (neither container on it, and no workflow-secret line)"
  say "and its cleanup would run: nothing. It creates no cloud resource and writes no record: the scratch directory is the only thing it makes, and its own EXIT trap removes it. The runs and the review open branches and pull requests on the SANDBOX repositories, which stay there and are yours to settle."
}

main() {
  scratch_up
  write_reader
  trap finish EXIT
  trap on_signal INT TERM HUP
  guard_xtrace
  guard_agent
  if [ "$MODE" = catalog ]; then CHECK_NO=921; else CHECK_NO=920; fi
  if [ -n "$DRY_RUN" ]; then
    need mktemp
    print_plan
    fact dry-run PASS "the plan above was printed and NOTHING was called: no gcloud, no fugaro, no curl. The guards that read the cloud (the installation's identity and the record) run only in a real run"
    REACHED_END=yes
    return 0
  fi
  guard_inputs
  need mktemp
  need python3
  need gcloud
  need curl
  need fugaro
  trap 'on_error $LINENO' ERR

  [ -f "$CONFIG" ] || stop "no local config for ${INSTALL} at ${CONFIG}: name it with CHECK920_CONFIG"
  read_config "$CONFIG" >"$SCRATCH/config.json" 2>"$SCRATCH/config.err" ||
    stop "the local config ${CONFIG} could not be read: $(first_line "$SCRATCH/config.err")"
  NAME=$(cfg_get name)
  GCP=$(cfg_get gcp_project)
  RTDB=$(cfg_get budget.rtdb_url)
  RTDB=${RTDB%/}
  BUCKET=$(cfg_get runs_bucket)
  if [ -z "$BUCKET" ]; then
    BUCKET=$(cfg_get bucket_url)
    BUCKET=${BUCKET#gs://}
    BUCKET=${BUCKET%%/*}
  fi
  [ -z "$NAME" ] || [ "$NAME" = "$INSTALL" ] || stop "$CONFIG is the config of '$NAME', not '$INSTALL'"
  [ "$GCP" = "$P" ] || stop "$CONFIG names gcp_project '$GCP', and this check is hard-coded to ${P}: never run it against another installation"
  printf '%s' "$RTDB" | grep -Eq '^https://[a-z0-9-]+(\.[a-z0-9-]+)*\.(firebaseio\.com|firebasedatabase\.app)$' ||
    stop "$CONFIG names no Realtime Database URL (budget.rtdb_url '$RTDB'), and the project record lives there. Run 'fugaro update'"
  printf '%s' "$BUCKET" | grep -Eq '^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$' ||
    stop "$CONFIG names no runs bucket (runs_bucket or a gs:// bucket_url)"
  BUCKET_OK=yes
  fact config PASS "read ${CONFIG}: GCP project ${GCP}, runs bucket gs://${BUCKET}, database ${RTDB}"

  # The installation guard runs BEFORE the token is minted: a shell pointed at
  # another project must not have read a credential for it on the way to saying
  # so.
  guard_project

  say "minting an access token (gcloud auth print-access-token)"
  mint_token || stop "gcloud auth print-access-token did not answer ($(first_line "$SCRATCH/token.err")): run 'gcloud auth login' and this check again"
  fact token PASS "an access token was written to a 0600 file in this run's 0700 scratch directory, never printed and never put on an argv"

  if [ "$MODE" = catalog ]; then run_921; else run_920; fi
  REACHED_END=yes
}

main "$@"
