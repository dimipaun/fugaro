#!/usr/bin/env bash
# Check 582 — the migration: `fugaro update --repo` enrols a repository on its
# profile's shared job and retires the repository's whole per-repository root.
#
# Plan task M5 of docs/shared-jobs-default/implementation-plan.md writes this
# check; the number is that work's own (its README.md reserves 580-589). What it
# settles is M2's own behaviour, read from the outside and nowhere else: an
# update of ONE checkout leaves that repository running on the profile's SHARED
# job and takes the repository's per-repository Terraform root away with it — the
# jobs deleted, the state object forgotten, nothing of it left to apply. Every
# one of those is a fact about a live installation and a live project, so this
# check drives the real `fugaro update --repo` against a SANDBOX repository and
# reads back what the project holds before and after it.
#
# What it measures, one row per assertion:
#
#   * the PROFILE: the published policy admits the repository to exactly ONE
#     profile (`fugaro project policy show --json`), that profile serves provider
#     `bitbucket`, and it admits the repository by its own list or the project's
#     (projectpolicy.Admits, both halves). The profile's name is what the shared
#     job's name is built from, so a repository admitted to two profiles leaves
#     which one the launch selects unreadable and this check refuses rather than
#     guesses at it;
#   * the PREMISE: the repository's OWN per-repository jobs are deployed before
#     the update — `gcloud run jobs list`, the jobs carrying `fugaro_repo` equal
#     to the repository's label (gcp.RepoLabel of task.Slug) and no
#     `fugaro_profile` at all. A repository with none has nothing to retire and
#     this check says so instead of measuring an update that changes nothing;
#   * the IMAGE: the profile's shared jobs resolve to a container image —
#     `fugaro-p-<profile>-bitbucket…` by name or by label, EVERY one of them
#     carrying a non-empty `spec.template.spec.template.spec.containers[].image`
#     (S10/D2305: `infra.ProjectJobImage` is what the deployer, the launcher and
#     the retirement's own precondition resolve, and a job with an empty image is
#     a repository with no job at all);
#   * the PROXY: the published `bitbucket_token` node has `proxy: true` and
#     admits the repository, and every shared job of the profile carries
#     `FUGARO_CREDENTIALS_SERVICE_URL` (D493/G3). That pair is M2's own
#     precondition — a Bitbucket repository whose token is not brokered holds the
#     retirement back by name — so a proxy that is off is a FAIL here and an
#     update that would leave the root standing;
#   * the RETIREMENT: `fugaro update --repo <the checkout>` — the ONE documented
#     command (CLAUDE.md's HARD RULE) — exits 0, prints no hold note, and the
#     repository's per-repository jobs the PREMISE named are GONE from the job
#     list read after it. An update that prints the hold note (`…'s own root is
#     not retired here: …`) or leaves a step for you is NOT a failure of this
#     row: it is a retirement that did not happen yet, and the row is UNMEASURED
#     naming the note rather than FAIL;
#   * the RUN: ONE throwaway run of that repository, launched with `fugaro run`
#     after the retirement, waited for by polling `fugaro runs ls --json` until
#     its own row is terminal. It has to end `succeeded`, to say `pushed=yes`, to
#     name a pull request AND to have run on one of the profile's shared jobs —
#     the job is read off its execution path
#     (`projects/<project>/locations/<region>/jobs/<job>/executions/<name>`), the
#     only place a run's job is written down. A run on a per-repository job after
#     the retirement is a repository that was not enrolled, and the migration is
#     not done.
#
# What it creates: it RETIRES the sandbox repository's per-repository root — the
# jobs deleted, the state object forgotten, the repository enrolled on the
# profile's shared job — and that is NOT undone: it is what this check measures,
# and the repository runs on the shared job from then on. It also launches ONE
# throwaway run (**billable**: its model calls are real) whose branch and pull
# request stay on the repository. Both are yours to settle; the retirement is not
# a thing a later command puts back.
#
# Usage:      bash scripts/live-checks/check-582.sh --sandbox <a sandbox Bitbucket repository checkout>
#             bash scripts/live-checks/check-582.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check-582.sh --help
# Needs:      bash, mktemp, python3 (every `--json` document is read with it),
#             git (the one remote read that names the repository, and the clean
#             checkout the check requires), gcloud (authenticated as YOU: the job
#             list is read with your credentials) and fugaro (the installed
#             release — the update and the run this check drives are the ones you
#             have).
# Where:      your own machine, and the **sandbox project** only: the project the
#             checkout's own `fugaro doctor --json` names has to be the sandbox
#             (CHECK582_SANDBOX_PROJECT, default fugaro-dev), and a project, a
#             region, an account, a repository or a path naming belong is refused
#             by name. So is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Costs:      wall clock, not a fixed fee: the update (minutes, and it may ask its
#             own confirmation for a billable image build), then about 40 minutes
#             of the run at the terminal, and that run's model calls
#             (**billable** — it is an ordinary run of the installation's agent,
#             on the sandbox).
# Runs:       by YOU, at a terminal. The retirement's own confirmation is the
#             update's, and it is answerable only from a real terminal (T7): a
#             run with no terminal leaves the root standing and this check reads
#             that as UNMEASURED. No agent runs any step of it, and its own test
#             (check-582_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The retired root and the run's branch and
#             pull request are NOT undone — see "What it creates" above.
# Output:     progress on stderr, ONE block on stdout headed CHECK 582 RESULT,
#             then CHECK 582 RESULT END (exit N) — to copy back into the
#             register's Check 582 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold, or a guard stopped the check before it could run. 2: a row
#             could not be measured for a reason that is this check's own setup
#             or the installation's configuration rather than a break (the block
#             says which, by name). A key the run never reached reads `FAIL —
#             not reached`, which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# authorization header. Every fact below is a NAME — a profile's name, a job's
# name, a repository's name, an image reference, a status — and no line this
# check reads carries a credential value at all.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK582_SANDBOX (or --sandbox: the checkout of a SANDBOX Bitbucket repository
# — REQUIRED, and the repository this check migrates is read from it),
# CHECK582_SANDBOX_PROJECT (the GCP project this check may run in; fugaro-dev),
# CHECK582_DRY_RUN, CHECK582_KEEP_SCRATCH, CHECK582_RUN_ID (the base of this
# check's own run id, default `date -u +%Y%m%d-%H%M%S`; the four hex characters
# `-0582` are appended, task.ValidRunID's form), CHECK582_UPDATE_SECONDS (how
# long the update is given; 3600), CHECK582_RUN_SECONDS (how long the run is
# waited for; 2400), CHECK582_POLL_SECONDS (20),
# CHECK582_DOCTOR_TIMEOUT_SECONDS (180) and CHECK582_CLI_TIMEOUT_SECONDS (180).
# None of them can point this check at another installation: the project and the
# region are read from the checkout.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check prints its
# own RESULT block, and a trace of a run that drives an update and launches a run
# would copy their output to the terminal.
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

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2).
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

# run_bounded SECONDS KEY CMD…: run one command under a bound. The child execs,
# so the pid the bound kills IS the command — a hung cloud read would otherwise
# outlive the check that gave up on it. The ERR trap is dropped in it: this check
# runs commands whose failing exit status is one of its own readings, and a trap
# inherited there would turn each of them into "an internal error" before the
# step could report it. stdin is NOT redirected: the update's own retirement
# confirmation is asked on it (T7's canType reads the terminal, not the pipes).
run_bounded() { # run_bounded SECONDS KEY CMD... (the KEY names the .out/.err pair it is read back through)
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; if [ -d "${SANDBOX:-}" ]; then cd "$SANDBOX" || exit 1; fi; exec "$@" ) \
    >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$secs" ] || break
    sleep 1
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes
    kill -TERM "$pid" 2>/dev/null || true
    sleep 2
    kill -KILL "$pid" 2>/dev/null || true
  fi
  BOUNDED_STATUS=0
  wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 582 needs bash"
  local t
  for t in mktemp python3 git gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 582 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk582-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
PRINTED=""

INSTALL=""
PROJECT=""
REGION=""
ACCOUNT=""
REPO=""
REPO_PROVIDER=""
REPO_LABEL=""
PROFILE=""
PROVIDER=""
SHARED_PREFIX=""
SHARED_JOBS=""
SHARED_IMAGES=""
REPO_JOBS=""
RUN_ID=""
# LAUNCHED: "" when no launch was attempted, `yes` when `fugaro run` returned
# (it launched the run), `maybe` when it did not answer inside its bound — a
# launch that timed out may or may not have started a run, and the restore row
# says which of the three it is rather than assuming one.
LAUNCHED=""
# SANDBOX_STOP: the checkout could not be read far enough to name the
# installation — this check's own setup, or a doctor that did not answer — and
# every row below rests on that installation. PROFILE_STOP is the same for the
# published policy, JOBS_STOP for the job list read before the update, and
# RETIRED_STOP for the one read after it.
SANDBOX_STOP=""
PROFILE_STOP=""
JOBS_STOP=""
RETIRED_STOP=""
UPDATE_NOTE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk582.XXXXXXXX") || die "Check 582 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
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
    SCRATCH_KEPT=yes
    say "keeping $SCRATCH (CHECK582_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created and what it did, said rather than assumed. The
# retirement is the check's own subject and is NOT undone, and neither is the one
# run it launches.
restore() {
  if [ -n "$DRY_RUN" ]; then
    fact restore PASS "this was a dry run: no update was run, no run was launched, no command was given to fugaro or gcloud, and nothing was created or retired"
    return 0
  fi
  local retired_note="nothing was retired (the update did not run, or it held the retirement back — the row above says which)"
  if [ -n "$RETIRED_OK" ]; then
    retired_note="${REPO}'s whole per-repository root was RETIRED by this check's own update — its jobs deleted and its state object forgotten — and that is NOT undone: it is what the row above measures, and the repository runs on the profile '${PROFILE}'s shared job from now on"
  fi
  case "$LAUNCHED" in
  yes)
    fact restore PASS "${retired_note}. One throwaway run of ${REPO} was launched (${RUN_ID}) and is NOT undone either: its branch and its pull request stay on that repository, which is the owner's to settle — this check holds no Bitbucket credential of its own"
    ;;
  maybe)
    fact restore PASS "${retired_note}. 'fugaro run' did not answer inside ${RUN_SECONDS}s, so a throwaway run of ${REPO} (${RUN_ID}) may have been launched and is NOT undone either: read 'fugaro runs show ${RUN_ID} --repo ${REPO}', and its branch and its pull request, if any, stay on that repository and are the owner's to settle — this check holds no Bitbucket credential of its own"
    ;;
  *)
    fact restore PASS "${retired_note}. No run was launched, so nothing else was created"
    ;;
  esac
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory, so there is none to remove"
  elif [ "$SCRATCH_KEPT" = yes ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK582_KEEP_SCRATCH is set), at $SCRATCH"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  # A row that reads FAIL is one of two things, and the verdict says which: a
  # criterion that did not hold (exit 1), or a row this check could not measure
  # (exit 2). Every unmeasured row is recorded FAIL too, so that it is never read
  # as a pass, and telling them apart is what UNMEASURED_KEYS is for.
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

# redact: what a gcloud or fugaro message may carry back into the block, which is
# pasted into a register. A credential-shaped run of characters goes, whatever it
# is — the second line of that defence rather than the first.
redact() {
  sed -E \
    -e 's/([Aa]uthorization)[[:space:]]*:[^|]*/\1: <redacted> /g' \
    -e 's/(Bearer|Basic)[[:space:]]+[A-Za-z0-9._~+/=-]{12,}/<redacted>/g' \
    -e 's/[A-Za-z0-9_]*(token|Token|TOKEN|secret|Secret|SECRET)[A-Za-z0-9_]*=[^[:space:]]+/<redacted>/g' \
    -e 's/((oauth|api)[_-]?key["=: ]+)[A-Za-z0-9._~+/=-]{12,}/\1<redacted>/Ig'
}

# strip_credentials: every `scheme://user:token@` in the text becomes `scheme://`.
# A token-based clone — a CI checkout — carries its credential in the origin's
# userinfo, and that URL is printed in the refusals below, into a block the
# register quotes. The rule is Check 480's, character for character.
strip_credentials() {
  sed -E 's#([a-zA-Z][a-zA-Z0-9+.-]*://)[^@[:space:]]*@#\1#g'
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
# One file reads every document this check takes — the doctor document, the
# published policy, the job list and a run's row — so that no reading of a JSON
# shape is spelled twice and none of them is done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 582's reader: one file, one reading per subcommand, no credential in
any of them.

origin argv: a git remote URL. Prints the `owner/name` it names, ASCII-lowered
       the way task.CanonicalRepo folds it, and the host it is on, or `-` for a
       URL that is not one.
doctor argv: a `fugaro doctor --json` document. Prints the installation's name,
       its GCP project and its region.
label  argv: a provider and a repository. Prints the repository's slug
       (task.Slug: the readable canonical path, truncated, then "-" and 16 hex of
       sha256(provider NUL canonical)) and the GCP label derived from it
       (gcp.RepoLabel: every character outside [a-z0-9_-] becomes "_"). The label
       is what the repository's own per-repository jobs carry under `fugaro_repo`,
       and it is the only way to tell them apart in a job list.
policy argv: the `fugaro project policy show --json` document, a repository and
       its provider. Prints whether the PROJECT's allow_repos admits it, how many
       profiles admit it (both halves of projectpolicy.Admits: the project's list
       and the profile's own, the project's where the profile writes none) and
       which, that profile's provider, and the published `bitbucket_token` node:
       whether its `proxy` is on, whether its allow_repos admits the repository
       (projectpolicy.MatchRepo, segment wildcards included) and the two together,
       which is the fact M2's retirement precondition reads.
jobs   argv: the installation's job list, the shared-name prefix, the profile and
       the repository's label. One line per job that is the profile's shared job
       (by label, or by name prefix where the name was truncated) or this
       repository's own (label `fugaro_repo` equal to the label and no
       `fugaro_profile` at all) — `job=<name> kind=<shared|repo> image=<present|
       absent> url=<present|absent>` — and then the totals: how many shared jobs
       there are, their names, how many carry a non-empty image and which image,
       how many carry FUGARO_CREDENTIALS_SERVICE_URL, and the repository's own
       jobs by name.
run    argv: the listing's file, a run id. Prints that run's row field by field,
       `found=no` when the listing does not hold it, and the JOB its execution
       path names (`projects/<project>/locations/<region>/jobs/<job>/executions/
       <name>`, internal/backend's Execution.Name), which is the only place a
       run's job is written down.
"""
import hashlib
import json
import re
import sys


def word(v, cap=80):
    if v is None:
        return "-"
    s = str(v)
    s = re.sub(r"[\x00-\x1f\x7f]", " ", s).strip()
    return s[:cap] if s else "-"


def text(v, cap=300):
    if v is None:
        return ""
    s = str(v)
    s = re.sub(r"[\x00-\x1f\x7f]", " ", s).strip()
    return s[:cap]


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def rows(path):
    doc = load(path)
    if isinstance(doc, list):
        return doc
    if isinstance(doc, dict):
        for key in ("runs", "items", "rows"):
            if isinstance(doc.get(key), list):
                return doc[key]
    return []


def spec_of(doc):
    """The container spec of a job.

    A Job carries spec.template.spec.template.spec (the v1 shape `gcloud run
    jobs list --format=json` prints), and older documents carry the shorter
    spec.template.spec. Both are walked, because a job this check could not read
    the containers of is a job it would call image-less.
    """
    spec = doc.get("spec") if isinstance(doc, dict) else None
    if not isinstance(spec, dict):
        return {}
    tpl = spec.get("template")
    if not isinstance(tpl, dict):
        return spec
    inner = tpl.get("spec")
    if isinstance(inner, dict):
        t2 = inner.get("template")
        if isinstance(t2, dict) and isinstance(t2.get("spec"), dict):
            return t2["spec"]
        return inner
    return tpl


def containers_of(doc):
    spec = spec_of(doc)
    out = spec.get("containers")
    return [c for c in out if isinstance(c, dict)] if isinstance(out, list) else []


def env(spec):
    out = {}
    if isinstance(spec, dict):
        for e in spec.get("env") or []:
            if isinstance(e, dict) and e.get("name"):
                out[e["name"]] = e
    return out


def carries(containers, name):
    for c in containers:
        if name in env(c):
            return True
    return False


def ascii_lower(s):
    """task.ASCIILower: A-Z only, and nothing else."""
    return "".join(chr(ord(c) + 32) if "A" <= c <= "Z" else c for c in s)


REPO_RE = re.compile(r"^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$")


def canonical(repo):
    """task.CanonicalRepo: ASCII-lowered, ".git" trimmed, owner/name checked."""
    c = ascii_lower(repo)
    if c.endswith(".git"):
        c = c[:-4]
    if not REPO_RE.match(repo) or not REPO_RE.match(c):
        return ""
    if any(seg in (".", "..") for seg in c.split("/")):
        return ""
    return c


def slug_of(provider, repo):
    """task.Slug: the readable canonical path, then "-" and 16 hex of
    sha256(provider NUL canonical)."""
    c = canonical(repo)
    if not c:
        return ""
    digest = hashlib.sha256((provider + "\x00" + c).encode()).hexdigest()[:16]
    readable = re.sub(r"[^a-z0-9]+", "-", c).strip("-")
    if len(readable) > 46:
        readable = readable[:46].rstrip("-")
    if not readable:
        readable = "repo"
    return readable + "-" + digest


def label_of(provider, repo):
    """gcp.RepoLabel of task.Slug: outside [a-z0-9_-] becomes "_", and a value
    over 63 characters is an error rather than a shorter label."""
    s = slug_of(provider, repo)
    if not s:
        return ""
    v = re.sub(r"[^a-z0-9_-]", "_", s)
    return v if len(v) <= 63 else ""


def seg_match(pattern, s):
    """config.MatchSegment: "*" is the only wildcard, within one segment."""
    return re.fullmatch(re.escape(pattern).replace(r"\*", ".*"), s) is not None


def match_repo(pattern, canon):
    """projectpolicy.MatchRepo: segment against segment, "*" the only wildcard."""
    if not pattern or not canon:
        return False
    if pattern == "*":
        return True
    ps, cs = pattern.split("/"), canon.split("/")
    if len(ps) != len(cs):
        return False
    return all(seg_match(p, c) for p, c in zip(ps, cs))


def admit_repo(patterns, canon):
    if not isinstance(patterns, list):
        return False
    return any(match_repo(p, canon) for p in patterns if isinstance(p, str))


def job_of_execution(execution):
    """The job an execution resource path names (`…/jobs/<job>/executions/<e>`)."""
    if "/jobs/" not in execution:
        return ""
    rest = execution.split("/jobs/", 1)[1]
    return rest.split("/", 1)[0]


cmd = sys.argv[1] if len(sys.argv) > 1 else ""
if cmd == "origin":
    url = (sys.argv[2] if len(sys.argv) > 2 else "").strip()
    host = ""
    path = ""
    m = re.match(r"^[a-z+]+://(?:[^@/]+@)?([^/:]+)(?::\d+)?/(.+)$", url)
    if m:
        host, path = m.group(1), m.group(2)
    else:
        m = re.match(r"^(?:[^@]+@)?([^:]+):(.+)$", url)
        if m:
            host, path = m.group(1), m.group(2)
    path = re.sub(r"\.git$", "", path.strip("/"))
    parts = [p for p in path.split("/") if p]
    print("host=%s" % word(host, 80))
    if len(parts) >= 2 and host:
        print("repo=%s" % word(ascii_lower("/".join(parts[-2:])), 120))
    else:
        print("repo=-")
elif cmd == "doctor":
    doc = load(sys.argv[2]) or {}
    project = doc.get("project") or {}
    print("install=%s" % word(project.get("name"), 60))
    print("project=%s" % word(project.get("gcp_project"), 80))
    print("region=%s" % word(project.get("region"), 40))
elif cmd == "label":
    provider = (sys.argv[2] if len(sys.argv) > 2 else "").strip()
    repo = (sys.argv[3] if len(sys.argv) > 3 else "").strip()
    print("slug=%s" % word(slug_of(provider, repo), 80))
    print("label=%s" % word(label_of(provider, repo), 80))
elif cmd == "policy":
    repo = sys.argv[3]
    repo_provider = (sys.argv[4] if len(sys.argv) > 4 else "").strip()
    canon = ("%s/%s" % (repo_provider, repo)) if repo_provider else repo
    doc = load(sys.argv[2]) or {}
    policy = doc.get("policy") if isinstance(doc, dict) else None
    if not isinstance(policy, dict):
        policy = {}
    project = policy.get("allow_repos") or []
    profiles = policy.get("profiles") or {}
    if not isinstance(profiles, dict):
        profiles = {}
    names = []
    for name in sorted(profiles):
        pr = profiles.get(name) or {}
        if not isinstance(pr, dict):
            continue
        # A profile admits only repositories of ITS OWN provider, and both
        # halves must admit (projectpolicy.Admits): the project's list and the
        # profile's own, which falls back to the project's where it writes none.
        if repo_provider and pr.get("provider") != repo_provider:
            continue
        own = pr.get("allow_repos") or project
        if admit_repo(project, canon) and admit_repo(own, canon):
            names.append(name)
    print("proj_admits=%s" % ("yes" if admit_repo(project, canon) else "no"))
    print("profiles=%s" % len(names))
    print("profile=%s" % (word(names[0], 60) if names else "-"))
    provider = ""
    if names:
        pr = profiles.get(names[0]) or {}
        if isinstance(pr, dict):
            provider = pr.get("provider") or ""
    print("provider=%s" % word(provider, 40))
    node = policy.get("bitbucket_token")
    node = node if isinstance(node, dict) else {}
    allows = node.get("allow_repos") or []
    token_admits = admit_repo(allows, canon)
    proxy = bool(node.get("proxy"))
    print("proxy=%s" % ("on" if proxy else "off"))
    print("token_admits=%s" % ("yes" if token_admits else "no"))
    print("proxy_ok=%s" % ("yes" if proxy and token_admits else "no"))
elif cmd == "jobs":
    prefix = sys.argv[3]
    profile = sys.argv[4] if len(sys.argv) > 4 else ""
    repo_label = sys.argv[5] if len(sys.argv) > 5 else ""
    shared = shared_images = shared_urls = repo_jobs = 0
    shared_names, image_names, repo_names = [], [], []
    for doc in load(sys.argv[2]) or []:
        if not isinstance(doc, dict):
            continue
        meta = doc.get("metadata") or {}
        name = meta.get("name") or ""
        labels = meta.get("labels") or {}
        if not name:
            continue
        # The name is truncated before its hash when the parts are long
        # (fugaro-p-<profile>-bitbucke-<hash>), so the prefix alone misses a real
        # job; the labels the job carries are what name its profile.
        is_shared = bool(profile) and labels.get("fugaro_profile") == profile and labels.get("fugaro_provider") == "bitbucket"
        if not is_shared and prefix and name.startswith(prefix):
            is_shared = True
        is_repo = bool(repo_label) and labels.get("fugaro_repo") == repo_label and not labels.get("fugaro_profile")
        if not (is_shared or is_repo):
            continue
        containers = containers_of(doc)
        images = [str(c.get("image") or "").strip() for c in containers]
        image = next((i for i in images if i), "")
        has_url = carries(containers, "FUGARO_CREDENTIALS_SERVICE_URL")
        if is_shared:
            shared += 1
            shared_names.append(name)
            if image:
                shared_images += 1
                image_names.append(image)
            if has_url:
                shared_urls += 1
        if is_repo:
            repo_jobs += 1
            repo_names.append(name)
        print("job=%s kind=%s image=%s url=%s" % (
            word(name, 120),
            "shared" if is_shared else "repo",
            "present" if image else "absent",
            "present" if has_url else "absent"))
    print("shared=%s" % shared)
    print("shared_names=%s" % text(",".join(shared_names), 400))
    print("shared_images=%s" % shared_images)
    print("image_names=%s" % text(",".join(image_names), 300))
    print("shared_urls=%s" % shared_urls)
    print("repojobs=%s" % repo_jobs)
    print("repo_names=%s" % text(",".join(repo_names), 400))
elif cmd == "run":
    want = sys.argv[3]
    for row in rows(sys.argv[2]):
        if not isinstance(row, dict) or row.get("run_id") != want:
            continue
        print("found=yes")
        for key in ("repo", "status", "terminal", "outcome", "pushed", "pr", "pr_url"):
            if key in row:
                v = row[key]
                if isinstance(v, bool):
                    v = "yes" if v else "no"
                print("%s=%s" % (key, word(v, 200 if key == "pr_url" else 60)))
        execution = row.get("execution") or ""
        print("execution=%s" % text(execution, 200))
        print("job=%s" % word(job_of_execution(execution), 120))
        halt = row.get("halt")
        if isinstance(halt, dict) and halt.get("detail"):
            print("halt_detail=%s" % text(halt.get("detail"), 240))
        if row.get("reason"):
            print("reason=%s" % text(row.get("reason"), 240))
        break
    else:
        print("found=no")
else:
    sys.stderr.write("reader.py: unknown query %s\n" % cmd)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/reader.py"
}

reader() { python3 "$SCRATCH/reader.py" "$@"; }

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-582.sh --sandbox <a sandbox Bitbucket repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a command's output to the terminal"
}

# guard_agent_session: the check is the owner's and it retires a root and launches
# a billable run of the installation's agent. Refusing here rather than letting
# the update refuse one at a time is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 582 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_belong: this check retires a root and launches a run that commits to a
# real repository, so every value it was given is read for the installation it
# must never touch before anything else runs. The values read from the checkout
# are guarded again where they are read.
guard_belong() {
  local all="${ACCOUNT:-}${SANDBOX:-}${INSTALL:-}${PROJECT:-}${REGION:-}${REPO:-}"
  case "$all" in
  *belong*) stop "a value this check was given or read names belong: this check is never run there" ;;
  esac
  fact belong PASS "nothing this check reads or was given names belong"
}

version_row() {
  local v=""
  if [ -x "$FUGARO_BIN" ] || command -v "$FUGARO_BIN" >/dev/null 2>&1; then
    v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fi
  fact fugaro-version PASS "${v:-fugaro version printed nothing} (the build this check drives: the update and the run it drives are the ones this release makes)"
}

# ------------------------------------------------------------------ the steps
# step_sandbox: the premise. The repository, the installation, the project and the
# region are all read from the checkout itself, so the check measures the
# installation you work in rather than one it guessed — and the project is held to
# the SANDBOX one, because this check retires a root.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a sandbox Bitbucket repository)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the update this check runs is run for the repository the checkout belongs to"
  fi
  local dirty=""
  dirty=$(git -C "$SANDBOX" status --porcelain 2>"$SCRATCH/status.err" || true)
  if [ -n "$dirty" ]; then
    stop "the checkout at $SANDBOX has uncommitted changes, so it is not the clean sandbox this check migrates: the update would apply a retirement over work in progress and the diff the update offers (the plugin pin's removal) could not be told from yours. Commit or stash them and run this check again"
  fi
  local url="" rc=0
  url=$(git -C "$SANDBOX" remote get-url origin 2>"$SCRATCH/origin.err") || rc=$?
  if [ "$rc" -ne 0 ] || [ -z "$url" ]; then
    stop "the checkout at $SANDBOX names no origin this check could read (git exited ${rc}): $(last_lines "$SCRATCH/origin.err"). The repository this check migrates is the one the checkout's own origin names, and a checkout without one is a repository this check cannot name"
  fi
  # The credential goes before the URL is matched, recorded or printed.
  url=$(printf '%s\n' "$url" | strip_credentials)
  printf '%s\n' "$url" >"$SCRATCH/origin.txt"
  local got host
  got=$(reader origin "$url" 2>"$SCRATCH/origin.reader.err") || stop "the origin of $SANDBOX could not be read: $(last_lines "$SCRATCH/origin.reader.err")"
  host=$(printf '%s\n' "$got" | sed -n 's/^host=//p')
  REPO=$(printf '%s\n' "$got" | sed -n 's/^repo=//p')
  if [ -z "$REPO" ] || [ "$REPO" = - ]; then
    stop "the origin of $SANDBOX is '$url', which names no repository: the update this check runs is run for the repository the checkout belongs to, and a URL this check cannot read is one it will not guess at"
  fi
  if [ "$host" != bitbucket.org ]; then
    stop "the origin of $SANDBOX is $url, which is not a Bitbucket repository (its host is '${host:-none}'): the retirement's own precondition holds for a BITBUCKET repository whose token is brokered through the credentials service (M2, D2306), and the proxy row below is the Bitbucket token's — a GitHub or GitLab checkout has nothing here for it to measure"
  fi
  REPO_PROVIDER=bitbucket
  # The repository's own label, derived exactly as internal/infra derives it
  # (gcp.RepoLabel of task.Slug): the per-repository jobs carry it under
  # `fugaro_repo`, and it is the only way this check can tell them from another
  # repository's in a job list.
  got=$(reader label "$REPO_PROVIDER" "$REPO" 2>"$SCRATCH/label.reader.err") || stop "the label of $REPO could not be derived: $(last_lines "$SCRATCH/label.reader.err")"
  REPO_LABEL=$(printf '%s\n' "$got" | sed -n 's/^label=//p')
  if [ -z "$REPO_LABEL" ] || [ "$REPO_LABEL" = - ]; then
    stop "$REPO is not a repository this check can name (its label came back '${REPO_LABEL:-none}'): the repository's own jobs are read by that label"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$FUGARO_BIN" doctor --json
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the installation the sandbox belongs to, and every row below is about it. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    SANDBOX_STOP=yes
    return 0
  fi
  local got2
  if ! got2=$(reader doctor "$SCRATCH/doctor.out" 2>"$SCRATCH/doctor.reader.err"); then
    unmeasured sandbox "the doctor document in $SCRATCH/doctor.out could not be read: $(last_lines "$SCRATCH/doctor.reader.err")"
    SANDBOX_STOP=yes
    return 0
  fi
  INSTALL=$(printf '%s\n' "$got2" | sed -n 's/^install=//p')
  PROJECT=$(printf '%s\n' "$got2" | sed -n 's/^project=//p')
  REGION=$(printf '%s\n' "$got2" | sed -n 's/^region=//p')
  if [ "$INSTALL" = - ]; then INSTALL=""; fi
  if [ "$PROJECT" = - ]; then PROJECT=""; fi
  if [ "$REGION" = - ]; then REGION=""; fi
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox belongs to, and every row below is read in it. What it printed is in $SCRATCH/doctor.out"
    SANDBOX_STOP=yes
    return 0
  fi
  case "${INSTALL}${PROJECT}${REGION}${REPO}" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${INSTALL:-<no name>} (${PROJECT}, region ${REGION}) and ${REPO}: this check is never run there" ;;
  esac
  if [ "$PROJECT" != "$SANDBOX_PROJECT" ]; then
    stop "the checkout at $SANDBOX belongs to the GCP project ${PROJECT}, and this check retires a root: it runs on the sandbox project only (${SANDBOX_PROJECT}). Point it at a sandbox repository checkout, or set CHECK582_SANDBOX_PROJECT if the sandbox project is named otherwise"
  fi
  fact sandbox PASS "${SANDBOX} is a clean Fugaro repository's checkout of the BITBUCKET repository ${REPO} (label ${REPO_LABEL}), and it belongs to the ${INSTALL:-<no name>} installation (${PROJECT}, region ${REGION}) — the SANDBOX project, which is the only one this check may run in: the update it drives retires a root"
}

# step_policy: the ONE profile this check is about, and the published proxy node
# the retirement's own precondition reads. The profile is read from the published
# policy rather than from the repository's own fugaro.yaml, because the shared
# job's name is built from the policy's profile name — and a repository admitted
# to two profiles leaves which one its own file selected unreadable, so that is a
# row the check refuses rather than guesses at.
step_policy() {
  if [ -n "$SANDBOX_STOP" ] || [ -z "$INSTALL" ]; then
    unmeasured profile "there is no installation to read the published policy of: the row above says why"
    PROFILE_STOP=yes
    return 0
  fi
  say "reading the published policy: fugaro project policy show --project ${INSTALL} --json"
  run_bounded "$CLI_TIMEOUT" policy "$FUGARO_BIN" project policy show --project "$INSTALL" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured profile "fugaro project policy show --project ${INSTALL} --json did not answer (exit ${BOUNDED_STATUS}): the profile that admits ${REPO} is what the shared job's name is built from, and no update is run on a policy nobody read. Its output is in $SCRATCH/policy.out and $SCRATCH/policy.err"
    PROFILE_STOP=yes
    return 0
  fi
  if ! reader policy "$SCRATCH/policy.out" "$REPO" "$REPO_PROVIDER" >"$SCRATCH/policy.txt" 2>"$SCRATCH/policy.reader.err"; then
    unmeasured profile "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/policy.out"
    PROFILE_STOP=yes
    return 0
  fi
  local n
  if [ "$(field "$SCRATCH/policy.txt" proj_admits)" != yes ]; then
    unmeasured profile "the published policy of ${INSTALL} does not admit ${REPO} to the PROJECT (its own allow_repos does not cover it), so no profile may admit it either and the repository would never be enrolled on a shared job: add it with 'fugaro project repos add bitbucket/${REPO}' and publish ('fugaro project policy publish'), then run this check again"
    PROFILE_STOP=yes
    return 0
  fi
  n=$(field "$SCRATCH/policy.txt" profiles)
  case "${n:-0}" in
  0)
    fact profile FAIL "${REPO} is admitted to the project and to NO profile of ${INSTALL}'s published policy, so no shared job serves it and the update would retire nothing: add the repository to that profile's allow_repos and publish ('fugaro project policy publish'), then run this check again"
    PROFILE_STOP=yes
    return 0
    ;;
  1) ;;
  *)
    unmeasured profile "${n} profiles of ${INSTALL}'s published policy admit ${REPO}, and which one its own fugaro.yaml selects is not a fact of the policy: the shared job's name is built from the profile's name and this check reads the profile from the document alone, so a repository admitted to more than one leaves every row below unmeasured. Name a sandbox Bitbucket repository exactly one profile admits and run this check again"
    PROFILE_STOP=yes
    return 0
    ;;
  esac
  PROFILE=$(field "$SCRATCH/policy.txt" profile)
  PROVIDER=$(field "$SCRATCH/policy.txt" provider)
  SHARED_PREFIX="fugaro-p-${PROFILE}-bitbucket"
  if [ "$PROVIDER" != bitbucket ]; then
    fact profile FAIL "${REPO} is admitted to the profile '${PROFILE}', which serves provider '${PROVIDER:-<none>}': a shared job serves exactly ONE provider (the provider is part of the job's name), and this check is the Bitbucket one"
    PROFILE_STOP=yes
    return 0
  fi
  fact profile PASS "${REPO} is admitted to exactly ONE profile of ${INSTALL}'s published policy — '${PROFILE}', provider bitbucket — by both halves of the admission (the project's allow_repos and the profile's own). The shared job this check reads the repository on is named from that profile (gcp.ProjectJobName: fugaro-p-${PROFILE}-bitbucket…)"
}

# step_jobs: the repository's OWN jobs, and the profile's shared ones, read back
# before the update. The repository's jobs are what the update has to retire; the
# shared job's image and its credentials-service switch are M2's own precondition
# (S10/D2305, S6/D2306), and both are settled before the update runs.
step_jobs() {
  if [ -n "$PROFILE_STOP" ]; then
    unmeasured premise "there is no profile to read the repository's jobs against: the row above says why"
    unmeasured image "there is no profile to read a shared job of: the row above says why"
    unmeasured proxy "there is no profile to read a shared job of: the row above says why"
    JOBS_STOP=yes
    return 0
  fi
  say "reading the installation's jobs: gcloud run jobs list --project=${PROJECT} --region=${REGION}"
  run_bounded "$CLI_TIMEOUT" jobs gcloud run jobs list --project="$PROJECT" --region="$REGION" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured premise "gcloud run jobs list did not answer (exit ${BOUNDED_STATUS}), so the jobs of ${REPO} that the update has to retire are not read: a read that did not answer is not a repository with none. Its output is in $SCRATCH/jobs.out and $SCRATCH/jobs.err"
    unmeasured image "gcloud run jobs list did not answer (exit ${BOUNDED_STATUS}), so the profile '${PROFILE}''s shared jobs are not read: its output is in $SCRATCH/jobs.out and $SCRATCH/jobs.err"
    unmeasured proxy "gcloud run jobs list did not answer (exit ${BOUNDED_STATUS}), so the profile '${PROFILE}''s shared jobs are not read: its output is in $SCRATCH/jobs.out and $SCRATCH/jobs.err"
    JOBS_STOP=yes
    return 0
  fi
  if ! reader jobs "$SCRATCH/jobs.out" "$SHARED_PREFIX" "$PROFILE" "$REPO_LABEL" >"$SCRATCH/jobs.txt" 2>"$SCRATCH/jobs.reader.err"; then
    unmeasured premise "gcloud run jobs list printed a document this check could not read: what it printed is in $SCRATCH/jobs.out"
    unmeasured image "gcloud run jobs list printed a document this check could not read: what it printed is in $SCRATCH/jobs.out"
    unmeasured proxy "gcloud run jobs list printed a document this check could not read: what it printed is in $SCRATCH/jobs.out"
    JOBS_STOP=yes
    return 0
  fi
  local shared listed
  shared=$(field "$SCRATCH/jobs.txt" shared)
  SHARED_JOBS=$(field "$SCRATCH/jobs.txt" shared_names)
  SHARED_IMAGES=$(field "$SCRATCH/jobs.txt" image_names)
  REPO_JOBS=$(field "$SCRATCH/jobs.txt" repo_names)
  listed=$(field "$SCRATCH/jobs.txt" repojobs)
  # The premise: the repository has its own jobs. Nothing else here can be read
  # without one — an update that retires nothing is not this check's subject.
  if [ "${listed:-0}" -lt 1 ]; then
    unmeasured premise "${REPO} has NO per-repository job in 'gcloud run jobs list' — no job carries fugaro_repo=${REPO_LABEL} without a fugaro_profile label — so there is no root for the update to retire and nothing for the retirement row to read. Either the repository is already on the profile '${PROFILE}'s shared job (a previous migration, or a repository that never had a root of its own: this check measures the migration itself), or its root was never applied. Name a sandbox repository that still has its own jobs and run this check again"
    unmeasured image "the shared jobs of the profile '${PROFILE}' are read, but this check stopped at its premise: the row above says why"
    unmeasured proxy "the shared jobs of the profile '${PROFILE}' are read, but this check stopped at its premise: the row above says why"
    JOBS_STOP=yes
    return 0
  fi
  fact premise PASS "${REPO}'s OWN per-repository job(s) are deployed — $(printf '%s' "$REPO_JOBS" | tr ',' ' ') — read by the label fugaro_repo=${REPO_LABEL} (gcp.RepoLabel of task.Slug) and by carrying no fugaro_profile at all. That is the whole per-repository root the update this check runs has to retire, and it is read again after it"
  # The image: the profile's shared jobs have to resolve one, which is M2's first
  # precondition (S10/D2305) — a job with an empty image is a repository with no
  # job at all.
  if [ "${shared:-0}" -lt 1 ]; then
    unmeasured image "no job of the installation is the profile '${PROFILE}''s shared Bitbucket job — neither named '${SHARED_PREFIX}…' nor labelled fugaro_profile=${PROFILE} with fugaro_provider=bitbucket — so the image this repository's runs would use does not exist to be read. Deploy the profile's job ('fugaro update') and run this check again"
    unmeasured proxy "the profile '${PROFILE}' serves no shared Bitbucket job in this project, so the job's own switch is not read: the row above says why"
    JOBS_STOP=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/jobs.txt" shared_images)" -lt "$shared" ]; then
    fact image FAIL "$(field "$SCRATCH/jobs.txt" shared_images) of ${shared} shared Bitbucket job(s) of the profile '${PROFILE}' carry a container image and the rest do not ($(printf '%s' "$SHARED_JOBS" | tr ',' ' ')): a shared job with an empty image is a repository with no job at all, and this is the fact M2's retirement precondition resolves (infra.ProjectJobImage, S10/D2305) — while it does not resolve, an update holds the repository's root back by name. Re-apply with 'fugaro update' so the profile's jobs are rewritten with the project's image, then run this check again"
    JOBS_STOP=yes
    return 0
  fi
  fact image PASS "${shared} shared Bitbucket job(s) of the profile '${PROFILE}' are deployed — $(printf '%s' "$SHARED_JOBS" | tr ',' ' ') — and EVERY one of them carries a container image (${SHARED_IMAGES}): that is the project's image for its shared jobs, which the deployer, the launcher and the retirement's own precondition resolve (infra.ProjectJobImage, S10/D2305)"
  # The proxy: the published node AND the job's own switch. Both are M2's
  # precondition for a Bitbucket repository (S6/D2306).
  if [ "$(field "$SCRATCH/policy.txt" proxy_ok)" != yes ]; then
    fact proxy FAIL "the published policy's bitbucket_token node does not broker ${REPO}: its proxy is '$(field "$SCRATCH/policy.txt" proxy)' and it admits the repository: $(field "$SCRATCH/policy.txt" token_admits). A Bitbucket repository whose token is not brokered holds the retirement of its root back by name — the update would print the hold note and leave the per-repository root standing (M2, S6/D2306). Turn the token's brokering on for this repository in this machine's local config and publish it ('fugaro project policy publish'), then run this check again"
    JOBS_STOP=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/jobs.txt" shared_urls)" -lt "$shared" ]; then
    fact proxy FAIL "$(field "$SCRATCH/jobs.txt" shared_urls) of ${shared} shared Bitbucket job(s) of the profile '${PROFILE}' carry FUGARO_CREDENTIALS_SERVICE_URL and the rest do not: the job that names no service holds whatever credential it mounts and reaches nothing, and a Bitbucket shared job holds NO token at all (D2302), so its git traffic would go nowhere. This is the profile's own switch and it is turned on for every job of the profile at once ('fugaro update', from this machine's local config). Re-apply and run this check again"
    JOBS_STOP=yes
    return 0
  fi
  fact proxy PASS "the published bitbucket_token node brokers ${REPO} (proxy: true, and its allow_repos admits it) and every one of the ${shared} shared Bitbucket job(s) of the profile '${PROFILE}' carries FUGARO_CREDENTIALS_SERVICE_URL — so a run of this repository reaches bitbucket.org through the credentials service (D2302/D2306), which is the state M2's retirement precondition needs"
}

# step_update: the ONE documented command, on the checkout. What it does to the
# repository's per-repository jobs is the whole of this check's subject, so the
# row is read from the job list after it and not from the update's own words. A
# retirement that did not happen — the update's hold note, or a step it left for
# the owner — is UNMEASURED and not a FAIL: the release is not what kept it back.
step_update() {
  if [ -n "$JOBS_STOP" ]; then
    unmeasured retired "nothing is run before the jobs of ${REPO} and of the profile '${PROFILE}' are read: the row above says why"
    RETIRED_STOP=yes
    return 0
  fi
  say "running the update: fugaro update --repo --project ${INSTALL} ${SANDBOX} — it asks its own confirmations at this terminal"
  run_bounded "$UPDATE_SECONDS" update "$FUGARO_BIN" update --repo --project "$INSTALL" "$SANDBOX"
  local out=$SCRATCH/update.out
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured retired "fugaro update did not answer inside ${UPDATE_SECONDS}s and was ended: it may or may not have retired ${REPO}'s root, so nothing about it is read. Its output is in $out and $SCRATCH/update.err — read 'gcloud run jobs list' yourself before running this check again"
    RETIRED_STOP=yes
    return 0
  fi
  # A hold note is the update saying, by name, that it did NOT retire the root:
  # that is a retirement that did not happen, not a break (M2's rootRetireHolds).
  if grep -q "own root is not retired here" "$out" 2>/dev/null; then
    UPDATE_NOTE=$(grep -m1 "own root is not retired here" "$out" | redact | cut -c1-400)
    unmeasured retired "the update did not retire ${REPO}'s root and said so: '${UPDATE_NOTE}'. That is a retirement that did not happen yet rather than a break — a run is executing on one of its jobs, the read that would say so did not answer, or the session could not confirm — and this check holds no row for it. Read the note, settle what it names, and run this check again"
    RETIRED_STOP=yes
    return 0
  fi
  # An update that converged what asks nothing and stopped for a step only the
  # owner can take exits non-zero and says what is left. That is not a refusal
  # either: the migration is unfinished, not broken.
  if grep -qE '[0-9]+ step\(s\) left for you' "$out" 2>/dev/null; then
    unmeasured retired "the update left a step for you and did not finish (exit ${BOUNDED_STATUS}): '$(grep -m1 -E '[0-9]+ step\(s\) left for you' "$out" | redact | cut -c1-300)'. The retirement of ${REPO}'s root is not measured through an update that stopped: settle that step ('fugaro update --repo ${SANDBOX}' yourself, at a terminal) and run this check again"
    RETIRED_STOP=yes
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact retired FAIL "fugaro update --repo --project ${INSTALL} ${SANDBOX} exited ${BOUNDED_STATUS} and did not retire ${REPO}'s root: $(last_lines "$SCRATCH/update.err"). The whole upgrade is that one command (CLAUDE.md's HARD RULE), and this check measures the migration it makes — an update that fails is the finding. Its whole output is in $out and $SCRATCH/update.err"
    RETIRED_STOP=yes
    return 0
  fi
  # The reading that decides: the repository's own jobs, gone. A read that did
  # not answer is not a retirement.
  say "reading the installation's jobs again: gcloud run jobs list"
  run_bounded "$CLI_TIMEOUT" jobs-after gcloud run jobs list --project="$PROJECT" --region="$REGION" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured retired "the update exited 0, and 'gcloud run jobs list' did not answer afterwards (exit ${BOUNDED_STATUS}), so whether ${REPO}'s own jobs are gone is not read: a read that did not answer is not a retirement. Its output is in $SCRATCH/jobs-after.out and $SCRATCH/jobs-after.err — read the job list yourself before running this check again"
    RETIRED_STOP=yes
    return 0
  fi
  local after after_jobs left=""
  after=$(reader jobs "$SCRATCH/jobs-after.out" "$SHARED_PREFIX" "$PROFILE" "$REPO_LABEL" 2>"$SCRATCH/jobs-after.reader.err" || true)
  after_jobs=$(printf '%s\n' "$after" | sed -n 's/^repo_names=//p')
  if [ -n "$after_jobs" ]; then
    left=$(printf '%s' "$after_jobs" | tr ',' ' ')
    fact retired FAIL "the update exited 0 and ${REPO}'s own per-repository job(s) are STILL deployed — ${left} — while its root was supposed to be retired whole: every job carrying fugaro_repo=${REPO_LABEL} has to be gone from 'gcloud run jobs list' when the retirement is done (M2: the destroy plan, the state removal, the API deletes and the state object forgotten). A repository left with its own jobs is a repository that is not on the profile '${PROFILE}'s shared job — its runs would still land on a job the retirement claims to have taken away. Read 'fugaro update --repo ${SANDBOX}' and the update's own output in $SCRATCH/update.out"
    RETIRED_STOP=yes
    return 0
  fi
  RETIRED_OK=yes
  fact retired PASS "fugaro update --repo --project ${INSTALL} ${SANDBOX} exited 0 and ${REPO}'s whole per-repository root is RETIRED: its own job(s) — $(printf '%s' "$REPO_JOBS" | tr ',' ' ') — are gone from 'gcloud run jobs list' (read before and after the update, by the label fugaro_repo=${REPO_LABEL}), and the profile '${PROFILE}'s shared job(s) — $(printf '%s' "$SHARED_JOBS" | tr ',' ' ') — are what the repository has left to run on. The update is the one documented command and it made the whole migration"
}

# step_run: the ONE run, after the migration. It has to end `succeeded`, to push
# and to open a pull request AND to land on one of the profile's shared jobs: a
# run that landed on a per-repository job is a repository that was not enrolled,
# which is exactly what this check exists to catch.
step_run() {
  if [ -n "$RETIRED_STOP" ]; then
    unmeasured run "nothing is launched before the migration is read as done: the row above says why"
    return 0
  fi
  RUN_ID="${RUN_BASE}-0582"
  local row status pushed pr outcome job
  if ! row=$(launch_and_wait); then
    LAUNCHED=$(cat "$SCRATCH/launch-state" 2>/dev/null || true)
    unmeasured run "$(cat "$SCRATCH/launch-err" 2>/dev/null || true)"
    return 0
  fi
  LAUNCHED=$(cat "$SCRATCH/launch-state" 2>/dev/null || true)
  status=$(field "$SCRATCH/row.txt" status)
  outcome=$(field "$SCRATCH/row.txt" outcome)
  pushed=$(field "$SCRATCH/row.txt" pushed)
  pr=$(field "$SCRATCH/row.txt" pr)
  job=$(field "$SCRATCH/row.txt" job)
  if [ "$status" != succeeded ] || [ "$pushed" != yes ] || [ -z "$pr" ] || [ "$pr" = 0 ]; then
    fact run FAIL "the run (${RUN_ID}) of ${REPO}, launched after its root was retired, did not end with a push and a pull request: status=${status:-unread}, outcome=${outcome:-none}, pushed=${pushed:-none}, pr=${pr:-none (the row names no pull request)}$(printf '%s' "$(field "$SCRATCH/row.txt" halt_detail)" | sed 's/^/, halt: /')$(printf '%s' "$(field "$SCRATCH/row.txt" reason)" | sed 's/^/, reason: /'). The repository runs on the profile '${PROFILE}'s shared job now, and that job reaches every credential through the credentials service, so a run that could not push or open its pull request is the shared job, the profile's account or its own switch not serving it. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}' before anything else"
    return 0
  fi
  if [ -z "$job" ] || [ "$job" = - ]; then
    unmeasured run "the run (${RUN_ID}) ended '${status}' with pull request #${pr}, and its own row names no execution, so the job it ran on cannot be read: the execution path is the only place that job is written down, and without it this row cannot say whether the run landed on the SHARED job. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}'"
    return 0
  fi
  case ",$SHARED_JOBS," in
  *",$job,"*)
    fact run PASS "the run (${RUN_ID}) of ${REPO}, launched after its root was retired, ended '${status}' (outcome=${outcome:-none}), its own row says it pushed (pushed=yes) and names pull request #${pr}, and it ran on '${job}' — one of the profile '${PROFILE}''s SHARED Bitbucket jobs ($(printf '%s' "$SHARED_JOBS" | tr ',' ' ')), read off its execution path. That is the migration's positive half: the repository's own root is gone and its runs land on the shared job"
    ;;
  *)
    fact run FAIL "the run (${RUN_ID}) of ${REPO} ended '${status}' with pull request #${pr} and ran on '${job}', which is NOT one of the profile '${PROFILE}''s shared Bitbucket jobs ($(printf '%s' "${SHARED_JOBS:-<none>}" | tr ',' ' ')): the migration is not done — a run lands on a shared job only when the repository is enrolled on a profile that admits it, and a run on a per-repository job is one whose root the retirement did not take away. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}' and 'gcloud run jobs list'"
    ;;
  esac
}

# task_write FILE RID: the throwaway task, by the FILE argument and never by a
# here-document on a command line (CLAUDE.md: an unquoted heredoc runs any
# backticked text). Its first line is the `# Task:` line `fugaro watch` and the
# pull request title come from, and what it asks for is the smallest thing that
# makes a run commit, push and open a pull request.
task_write() { # task_write FILE RID
  {
    printf '# Task: Check 582 — the migration (%s)\n\n' "$2"
    printf 'Add one line to `CHK582.md` at the repository root:\n\n'
    printf '    check 582 %s\n\n' "$2"
    printf 'Commit it on your own branch and open a pull request for it, then stop.\n'
    printf 'Change nothing else in the repository.\n'
  } >"$1"
}

# launch_and_wait: one throwaway run, printed as its terminal row. `fugaro run`
# returns AT LAUNCH, so the row is polled for by run id until the listing's own
# `terminal` word is yes — a reading taken straight after a launch is an in-flight
# row, which is a FAIL no run earned. A failure to launch or to read the row is
# not printed as a row: the reason goes to the launch-err file.
launch_and_wait() {
  local task=$SCRATCH/task.md waited=0 row=""
  : >"$SCRATCH/launch-state"
  task_write "$task" "$RUN_ID"
  say "launching the run (${RUN_ID}) in ${REPO} — it goes on in the cloud, about 40 minutes"
  run_bounded "$RUN_SECONDS" run "$FUGARO_BIN" run --repo "$REPO" --project "$INSTALL" \
    --run-id "$RUN_ID" --title "Check 582: the migration" --task-file "$task" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    printf 'maybe' >"$SCRATCH/launch-state"
    printf '%s' "fugaro run did not answer inside ${RUN_SECONDS}s and was ended: it may or may not have launched the run (${RUN_ID}), so nothing about it is read. Read the listing yourself ('fugaro runs ls --repo ${REPO} --project ${INSTALL}') before launching another one" >"$SCRATCH/launch-err"
    return 1
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    printf '%s' "fugaro run for ${REPO} exited ${BOUNDED_STATUS} and never launched a run: $(last_lines "$SCRATCH/run.err"). A launch refused before Cloud Run — a repository the profile does not admit, the project's own admission — is the launcher's own sentence, and it is the reading this row carries" >"$SCRATCH/launch-err"
    return 1
  fi
  printf 'yes' >"$SCRATCH/launch-state"
  while :; do
    row=""
    if "$FUGARO_BIN" runs ls --repo "$REPO" --project "$INSTALL" --no-cache --json >"$SCRATCH/rows.json" 2>>"$SCRATCH/run.err"; then
      row=$(reader run "$SCRATCH/rows.json" "$RUN_ID" 2>>"$SCRATCH/run.err" || true)
    fi
    if [ "$(printf '%s\n' "$row" | sed -n 's/^found=//p')" = yes ] &&
      [ "$(printf '%s\n' "$row" | sed -n 's/^terminal=//p')" = yes ]; then break; fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  printf '%s\n' "$row" >"$SCRATCH/row.txt"
  if [ "$(printf '%s\n' "$row" | sed -n 's/^found=//p')" != yes ]; then
    printf '%s' "the run (${RUN_ID}) does not appear in 'fugaro runs ls --repo ${REPO}' after ${waited}s: it was launched but its row is not in the listing, so nothing about it is read. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}' yourself" >"$SCRATCH/launch-err"
    return 1
  fi
  if [ "$(printf '%s\n' "$row" | sed -n 's/^terminal=//p')" != yes ]; then
    printf '%s' "the run (${RUN_ID}) had not reached a final status after ${waited}s: status=$(printf '%s\n' "$row" | sed -n 's/^status=//p'). Raise CHECK582_RUN_SECONDS, or read that run's row yourself ('fugaro runs show ${RUN_ID} --repo ${REPO}')" >"$SCRATCH/launch-err"
    return 1
  fi
  printf '%s\n' "$row"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 582 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] git status and git remote get-url origin in ${SANDBOX} — the clean checkout and the repository it belongs to"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation and the project, which has to be the sandbox"
  say "  [read] fugaro project policy show --json — the ONE profile that admits the repository, and the published bitbucket_token node (proxy, allow_repos)"
  say "  [network, read-only] gcloud run jobs list — the repository's OWN per-repository jobs (fugaro_repo label), the profile's shared jobs, their image and their credentials-service switch"
  say "  [network, DESTRUCTIVE] fugaro update --repo --project <the installation> ${SANDBOX} — the ONE documented command: it enrols the repository on the profile's shared job and RETIRES the repository's whole per-repository root (jobs deleted, state object forgotten, not undone), and it asks its own confirmations at this terminal"
  say "  [network, read-only] gcloud run jobs list — the same jobs again: the repository's own have to be gone"
  say "  [network, BILLABLE] ONE run of the repository: fugaro run --repo <the repository> — one commit, a push and a pull request, about 40 minutes, its model calls billed"
  say "  [read] fugaro runs ls --json — the run's own row, polled until it is terminal: status, pushed, the pull request, and the SHARED job its execution names"
  fact sandbox INFO "planned: a real run measures this row; --dry-run did not"
  fact profile INFO "planned: a real run measures this row; --dry-run did not"
  fact premise INFO "planned: a real run measures this row; --dry-run did not"
  fact image INFO "planned: a real run measures this row; --dry-run did not"
  fact proxy INFO "planned: a real run measures this row; --dry-run did not"
  fact retired INFO "planned: a real run measures this row; --dry-run did not"
  fact run INFO "planned: a real run measures this row; --dry-run did not"
  fact dry-run PASS "the plan was printed and nothing was run: no update, no job list, no launch and no retirement"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong sandbox profile premise image proxy retired run restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 582 RESULT\n' >&3
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
  printf 'CHECK 582 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

main() {
  need_tools
  if [ -z "$FUGARO_BIN" ]; then
    die "Check 582 needs 'fugaro' on PATH: the check drives the update and the run you have installed, and it is not there"
  fi
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_sandbox
  step_policy
  step_jobs
  step_update
  step_run
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a sandbox Bitbucket repository, which is the repository this check migrates"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

SANDBOX=${CHECK582_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 582 needs --sandbox: the checkout of a sandbox Bitbucket repository. The repository this check migrates, and the installation it is read in, are both taken from that checkout. Usage: bash scripts/live-checks/check-582.sh --sandbox <a sandbox Bitbucket repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
DRY_RUN=${CHECK582_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK582_KEEP_SCRATCH:-}
SANDBOX_PROJECT=${CHECK582_SANDBOX_PROJECT:-fugaro-dev}
RUN_BASE=${CHECK582_RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}
UPDATE_SECONDS=${CHECK582_UPDATE_SECONDS:-3600}
RUN_SECONDS=${CHECK582_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK582_POLL_SECONDS:-20}
DOCTOR_TIMEOUT=${CHECK582_DOCTOR_TIMEOUT_SECONDS:-180}
CLI_TIMEOUT=${CHECK582_CLI_TIMEOUT_SECONDS:-180}
RETIRED_OK=""
# Resolved before anything runs, and by absolute path: this check drives a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there.
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
