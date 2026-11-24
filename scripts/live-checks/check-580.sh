#!/usr/bin/env bash
# Check 580 — the Bitbucket shared job: one run's clone, fetch, push and pull
# request, and its first model call through the profile's claude_accounts.
#
# Plan task S8 of docs/shared-jobs-default/implementation-plan.md writes this
# check; the number is that work's own (its README.md reserves 580-589). What it
# settles is read from the outside and nowhere else: **I1** — a shared project
# job really serves a BITBUCKET repository: the run lands on the profile's
# shared job, it clones, fetches and pushes through the installation's
# credentials service (D2302: the job mounts no Bitbucket token at all, so the
# service is the only route to bitbucket.org) and it opens its pull request — and
# **F17** — a shared job holds no Claude OAuth token, so the model access of a
# Bitbucket shared job's run is the profile's `claude_accounts` through the
# cascade, which the first live check has to prove. Neither is answerable
# offline: whether the launch lands the run on that job, whether the service
# serves its git traffic, and which credential answered its first model call, are
# answers of a live installation. So this check launches ONE throwaway run in a
# SANDBOX Bitbucket repository and reads back what the installation and the
# service wrote about it.
#
# What it measures:
#
#   * the sandbox checkout: it is a Fugaro repository's checkout, its own origin
#     is a BITBUCKET repository (a GitHub one is refused by name: this check is
#     the Bitbucket half and there is nothing here for it to measure), and the
#     project, region and installation it belongs to are read from the checkout
#     itself (`fugaro doctor --json`), so the check measures the installation you
#     work in rather than one it guessed;
#   * the published policy (`fugaro project policy show --json`): exactly ONE
#     profile admits the repository (a repository admitted to two leaves which
#     one its own `fugaro.yaml` selected unreadable, and the shared job's name is
#     built from the profile's), that profile serves provider `bitbucket`, it
#     lists `claude_accounts` — the model access this check's last row is about —
#     and the published budget mode is one the gateway exists in (`observe` or
#     `enforce` — off is not a mode any more, D3009 — since no gateway is no
#     path to the service);
#   * the profile's shared jobs, read back with `gcloud run jobs list`: at least
#     one is named `fugaro-p-<profile>-bitbucket…` (one job per (profile,
#     provider, account), gcp.ProjectJobName), EVERY one of them carries
#     `FUGARO_CREDENTIALS_SERVICE_URL` (the switch, which nothing but a launcher
#     writes) and NONE of them carries `FUGARO_BITBUCKET_TOKEN` (D2302: a
#     Bitbucket shared job holds no token at all — its git traffic reaches
#     bitbucket.org through the service or not at all);
#   * ONE run of that repository, launched with `fugaro run` under an id this
#     check mints, waited for by polling `fugaro runs ls --json` until its own
#     row is terminal. What the row is held to: it ended `succeeded`, its own
#     `pushed` is yes and it names a pull request number — a run that pushed its
#     branch and opened its pull request is the positive half of this check, and
#     it is only reachable if its model calls were answered;
#   * the job that run LANDED on, read off the execution path in its own row
#     (`projects/<project>/locations/<region>/jobs/<job>/executions/<name>`,
#     internal/backend's Execution.Name — the only place a run's job is written
#     down). It has to be one of the profile's shared Bitbucket jobs: a run on a
#     per-repository job, or on another profile's, is a launch that did not
#     select this profile's shared job, and nothing below would be about one;
#   * the run's GIT traffic, out of the credentials service's OWN lines
#     (`gcloud logging read` over `credsvc: audit`, filtered by the run id): at
#     least one line of purpose `git` with status `ok` under the run's own id,
#     which is the clone and the fetch (the receive-pack advertisement and the
#     push are the same purpose) arriving at the service rather than at a token
#     the job holds. The line's purpose does not tell a fetch from a push
#     (credsvc's PurposeGit is one request of a run's git traffic), so the push
#     is read off the run's own row and this row says so;
#   * the run's FIRST MODEL CALL, out of those same lines: the earliest line of
#     purpose `forward` with status `ok`, whose CREDENTIAL NAME has to be one of
#     the profile's `claude_accounts`. That is F17: a shared job holds no Claude
#     OAuth token, so the credential that answered the run's model call is the
#     profile's account through the cascade — a line naming any other credential
#     (the project's paid key above all) is the finding this row exists for.
#
# What it creates: ONE throwaway run of the sandbox repository (**billable**: its
# model calls are real), its branch and its pull request, which **stay on that
# repository** — this check holds no Bitbucket credential of its own and cannot
# delete them, so their rows are yours to settle. It writes nothing into the
# installation and nothing into any repository: no cloud resource, no record, no
# object and no node.
#
# Usage:      bash scripts/live-checks/check-580.sh --sandbox <a sandbox Bitbucket repository checkout>
#             bash scripts/live-checks/check-580.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check-580.sh --help
# Needs:      bash, mktemp, python3 (every `--json` document is read with it),
#             git (the one remote read that names the repository), gcloud
#             (authenticated as YOU: the job list and the service's own lines
#             are read with your credentials) and fugaro (the installed release
#             — the run this check launches is the one you have).
# Where:      your own machine, and the **sandbox** only. A project, a region, an
#             account, a repository or a path naming belong is refused by name,
#             and so is a coding agent's session: every command this check runs
#             is one you could type yourself.
# Costs:      wall clock, not a fixed fee: about 40 minutes of the run at the
#             terminal, and that run's model calls (**billable** — it is an
#             ordinary run of the installation's agent, on the sandbox).
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-580_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's branch and pull request are NOT
#             removed: this check holds no Bitbucket credential of its own.
# Output:     progress on stderr, ONE block on stdout headed CHECK 580 RESULT,
#             then CHECK 580 RESULT END (exit N) — to copy back into the
#             register's Check 580 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold, or a guard stopped the check before it could run. 2: a row
#             could not be measured for a reason that is this check's own setup
#             or the installation's configuration rather than a break (the block
#             says which, by name). A key the run never reached reads `FAIL —
#             not reached`, which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# authorization header. Every fact below is a NAME — a profile's name, a
# credential's name, a job's name, a repository's name, a status — and every line
# the service writes and this check reads is reduced to a short plain-word shape
# before it is printed. This check holds no credential at all: the only identity
# in the path is the one Cloud Run gives the job it launched.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK580_SANDBOX (or --sandbox: the checkout of a SANDBOX Bitbucket
# repository — REQUIRED, and the repository this check runs for is read from
# it), CHECK580_DRY_RUN, CHECK580_KEEP_SCRATCH, CHECK580_RUN_ID (the base of this
# check's own run id, default `date -u +%Y%m%d-%H%M%S`; the four hex characters
# `-0580` are appended, task.ValidRunID's form), CHECK580_RUN_SECONDS (how long
# the run is waited for; 2400), CHECK580_POLL_SECONDS (20), CHECK580_LOG_AGE
# (1h: the least the read of the service's lines looks back — a run that
# outlasted it is read from its own launch, because the row that proves F17 is
# its FIRST model call), CHECK580_LOG_LIMIT (200), CHECK580_LOG_WAIT_SECONDS (120),
# CHECK580_LOG_POLL_SECONDS (5), CHECK580_DOCTOR_TIMEOUT_SECONDS (180) and
# CHECK580_CLI_TIMEOUT_SECONDS (180). None of them can point this check at
# another installation: the project and the region are read from the checkout.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check prints its
# own RESULT block, and a trace of a run that launches a run would copy that
# run's own output to the terminal.
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
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the check ends as one that could not measure (exit 2).
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

# need SECONDS: run one command under a bound. The child execs, so the pid the
# bound kills IS the command — a hung cloud read would otherwise outlive the
# check that gave up on it. The ERR trap is dropped in it: this check runs
# commands whose failing exit status is one of its own readings, and a trap
# inherited there would turn each of them into "an internal error" before the
# step could report it.
run_bounded() { # run_bounded SECONDS KEY CMD... (run from $SANDBOX, and the KEY names the .out/.err pair it is read back through)
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
  command -v bash >/dev/null 2>&1 || die "Check 580 needs bash"
  local t
  for t in mktemp python3 git gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 580 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk580-mine"
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
PROFILE=""
PROVIDER=""
PROFILE_ACCOUNTS=""
SHARED_PREFIX=""
SHARED_JOBS=""
RUN_ID=""
# SANDBOX_STOP: the checkout could not be read far enough to name the
# installation — this check's own setup, or a doctor that did not answer — and
# every row below rests on that installation. RUN_ROW is yes only when a TERMINAL
# row of this check's own run was read: the rows read off the run, its job and
# the service's lines for it are rows about that row, and one that was never read
# leaves them unmeasured rather than guessed at.
SANDBOX_STOP=""
PROFILE_STOP=""
JOB_STOP=""
RUN_ROW="no"
# LAUNCH_EPOCH is the second the run was launched in, and empty until then: it is
# what log_freshness measures the read of the service's lines from.
LAUNCH_EPOCH=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk580.XXXXXXXX") || die "Check 580 cannot make a scratch directory"
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
    say "keeping $SCRATCH (CHECK580_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, said rather than assumed. The one run it
# launches is not undone — it lives on the sandbox repository, its branch and its
# pull request with it, and this check holds no Bitbucket credential to remove
# them with.
restore() {
  if [ -n "$DRY_RUN" ]; then
    fact restore PASS "this was a dry run: no run was launched, no command was given to fugaro or gcloud, and nothing was created"
    return 0
  fi
  if [ -n "$RUN_ID" ]; then
    fact restore PASS "one throwaway run of ${REPO} was launched (${RUN_ID}) and is NOT undone: its branch and its pull request stay on that repository, which is the owner's to settle — this check holds no Bitbucket credential of its own. It created no cloud resource, wrote no record and touched no repository otherwise"
  else
    fact restore PASS "no run was launched, so nothing was created: it stopped before the launch. This check creates no cloud resource, writes no record and touches no repository"
  fi
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
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK580_KEEP_SCRATCH is set), at $SCRATCH"
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
# A token-based clone — a CI checkout, or the GitHub checkout the header tells the
# owner not to point this at — carries its credential in the origin's userinfo,
# and that URL is printed in the two refusals below, into a block the register
# quotes. redact's rules do not reach it: nothing there says `token=`, and a value
# the size of a personal access token is not a shape it knows. The rule is Check
# 480's, character for character: the userinfo ends at the last @ before the host,
# the way git's own URL parser reads it, so a pattern that cannot cross a `/`
# leaves a credential holding one in place verbatim
# (https://x-access-token:sec/ret@github.com/o/r.git); the class stops at
# whitespace, so the match cannot run past the URL into an @ later in the line.
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
# published policy, the job list, a run's row and the service's own lines — so
# that no reading of a JSON shape is spelled twice and none of them is done in
# bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 580's reader: one file, one reading per subcommand, no credential in
any of them.

origin argv: a git remote URL. Prints the `owner/name` it names and the host it
       is on, or `-` for a URL that is not one: the repository this check runs
       for is the one the sandbox checkout's own origin names.
doctor argv: a `fugaro doctor --json` document. Prints the installation's name,
       its GCP project and its region.
policy argv: the `fugaro project policy show --json` document, a repository.
       Prints whether the PROJECT's allow_repos admits it, how many of the
       document's profiles admit it and which, that profile's provider and its
       `claude_accounts`, and the published budget mode. The profile's own list
       is the narrower of the two and an absent one falls back to the project's
       (projectpolicy.ProfileAllowRepos), which is what makes a repository
       admitted to a profile whose list was never written.
jobs   argv: the installation's job list, a name prefix. One line per job whose
       name starts with the prefix — `job=<name> url=<present|absent>
       token=<present|absent>` — and then the totals. A shared job's name is
       `fugaro-p-<profile>-<provider>[-<account>]` plus a hash (gcp.ProjectJobName),
       so the prefix is how the jobs of one profile are read.
run    argv: the listing's file, a run id. Prints that run's row field by field,
       `found=no` when the listing does not hold it, and the JOB its execution
       path names (`projects/<project>/locations/<region>/jobs/<job>/executions/
       <name>`, internal/backend's Execution.Name), which is the only place a
       run's job is written down.
audit  argv: the service's lines' file, a run id. `audit=<status>|<credential>|
       <tier>|<purpose>|<job>|<caller>` for each `credsvc: audit` line of that
       run — the status is `ok` for a call the service served, the credential and
       tier are the NAME and TIER of the credential it used (never a value), the
       job is the one the RUN REPORTED and the caller is the account the service
       VERIFIED (audit.go's AuditEvent) — and `first_forward=<credential>`, the
       credential of the EARLIEST line of purpose `forward` with status ok, which
       is the run's first model call.
"""
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


def env(spec):
    out = {}
    if isinstance(spec, dict):
        for e in spec.get("env") or []:
            if isinstance(e, dict) and e.get("name"):
                out[e["name"]] = e
    return out


def spec_of(doc):
    """The container spec of a job.

    A Job carries spec.template.spec.template.spec (the v1 shape `gcloud run
    jobs list` answers), and the known paths are tried in order before the
    document is searched for the first containers list."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec"),):
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


def carries(containers, name):
    """Whether any of these containers names that variable at all.

    The presence of the NAME is the reading and never its value: a token a job
    mounts is a valueSource reference and one it was handed plainly is a value,
    and both are the job holding it — a reading that only looked at one of the
    two would answer "no token" for a job that had one."""
    for c in containers:
        if name in env(c):
            return True
    return False


def inside(entry, canonical):
    """Whether a published repository entry admits a canonical repository.

    Both sides are canonical — `bitbucket/acme/web`, `github/acme/app` and the
    wildcards `*` and `bitbucket/acme/*` (projectpolicy.Canonical, MatchRepo) —
    so they are compared segment against segment, exactly as the runner does:
    the provider segment is a segment like any other, so an entry naming another
    provider admits nothing here, and `*` alone admits every repository."""
    if entry == "*":
        return True
    e, c = entry.split("/"), canonical.split("/")
    if canonical == "" or len(e) != len(c):
        return False
    for a, b in zip(e, c):
        if a == b:
            continue
        if "*" in a and re.match("^" + re.escape(a).replace(r"\*", "[^/]*") + "$", b):
            continue
        return False
    return True


def any_admits(entries, canonical):
    return any(inside(e, canonical) for e in entries if isinstance(e, str))


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
        print("repo=%s" % word("/".join(parts[-2:]), 120))
    else:
        print("repo=-")
elif cmd == "doctor":
    doc = load(sys.argv[2]) or {}
    project = doc.get("project") or {}
    print("install=%s" % word(project.get("name"), 60))
    print("project=%s" % word(project.get("gcp_project"), 80))
    print("region=%s" % word(project.get("region"), 40))
elif cmd == "policy":
    repo = sys.argv[3]
    # The repository's own provider, and never the profile's below: `canonical`
    # is the repository in the policy's spelling, and a profile is counted only
    # when it serves that provider.
    repo_provider = (sys.argv[4] if len(sys.argv) > 4 else "").strip()
    canonical = ("%s/%s" % (repo_provider, repo)) if repo_provider else repo
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
        # A profile admits only repositories of ITS OWN provider
        # (projectpolicy.Admits, which compares the canonical's provider against
        # the profile's): a github profile's list says nothing about a bitbucket
        # repository, and one that writes no list of its own falls back to the
        # project's, which is nowhere near enough to count it.
        if repo_provider and pr.get("provider") != repo_provider:
            continue
        eff = pr.get("allow_repos") or project
        if any_admits(eff, canonical):
            names.append(name)
    print("proj_admits=%s" % ("yes" if any_admits(project, canonical) else "no"))
    print("profiles=%s" % len(names))
    print("profile=%s" % (word(names[0], 60) if names else "-"))
    provider = ""
    accounts = []
    if names:
        pr = profiles.get(names[0]) or {}
        if isinstance(pr, dict):
            provider = pr.get("provider") or ""
            accounts = [a for a in (pr.get("claude_accounts") or []) if isinstance(a, str)]
    print("provider=%s" % word(provider, 40))
    print("accounts=%s" % len(accounts))
    print("account_names=%s" % text(",".join(accounts), 300))
    budget = policy.get("budget")
    budget = budget if isinstance(budget, dict) else {}
    print("mode=%s" % word(budget.get("mode"), 20))
elif cmd == "jobs":
    prefix = sys.argv[3]
    profile = sys.argv[4] if len(sys.argv) > 4 else ""
    n = urls = tokens = 0
    names = []
    for doc in load(sys.argv[2]) or []:
        if not isinstance(doc, dict):
            continue
        name = ((doc.get("metadata") or {}).get("name")) or ""
        labels = ((doc.get("metadata") or {}).get("labels")) or {}
        # The name is truncated before its hash when the parts are long
        # (fugaro-p-<profile>-bitbucke-<hash>), so the prefix alone misses a
        # real job; the labels the job carries are what name its profile.
        by_label = bool(profile) and labels.get("fugaro_profile") == profile and labels.get("fugaro_provider") == "bitbucket"
        if not name or not (name.startswith(prefix) or by_label):
            continue
        containers = containers_of(doc)
        n += 1
        names.append(name)
        if carries(containers, "FUGARO_CREDENTIALS_SERVICE_URL"):
            urls += 1
        if carries(containers, "FUGARO_BITBUCKET_TOKEN"):
            tokens += 1
        print("job=%s url=%s token=%s" % (
            word(name, 120),
            "present" if carries(containers, "FUGARO_CREDENTIALS_SERVICE_URL") else "absent",
            "present" if carries(containers, "FUGARO_BITBUCKET_TOKEN") else "absent"))
    print("jobs=%s" % n)
    print("urls=%s" % urls)
    print("tokens=%s" % tokens)
    print("names=%s" % text(",".join(names), 400))
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
                # A pull request URL is the one field here that is long by
                # nature and is quoted whole in the register: a cut one is a URL
                # the owner cannot open.
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
elif cmd == "audit":
    want = sys.argv[3]
    forwards = []
    for i, entry in enumerate(load(sys.argv[2]) or []):
        if not isinstance(entry, dict):
            continue
        p = entry.get("jsonPayload") or {}
        if p.get("run") != want:
            continue
        msg = entry.get("textPayload") or p.get("msg") or p.get("message") or ""
        if msg != "credsvc: audit":
            continue
        status = p.get("status")
        credential = p.get("credential")
        purpose = p.get("purpose")
        print("audit=%s|%s|%s|%s|%s|%s" % (
            word(status, 40),
            word(credential, 80),
            word(p.get("tier"), 8),
            word(purpose, 40),
            word(p.get("job"), 120),
            word(p.get("caller"), 200)))
        if purpose == "forward" and status == "ok" and credential:
            # The earliest line by its own timestamp, and by the document's
            # order where a line carries none: the run's FIRST model call is
            # what F17's row reads, and a later switch must not be read as it.
            forwards.append((str(entry.get("timestamp") or ""), i, word(credential, 80)))
    if forwards:
        forwards.sort(key=lambda t: (t[0] == "", t[0], t[1]))
        print("first_forward=%s" % forwards[0][2])
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-580.sh --sandbox <a sandbox Bitbucket repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a command's output to the terminal"
}

# guard_agent_session: the check is the owner's and it launches a billable run of
# the installation's agent. Refusing here rather than letting the launch refuse
# one at a time is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 580 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_belong: this check launches a run that commits to a real repository, so
# every value it was given is read for the installation it must never touch
# before anything else runs. The values read from the checkout are guarded again
# where they are read.
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
  fact fugaro-version PASS "${v:-fugaro version printed nothing} (the build this check drives: the run it launches is the one this release makes)"
}

# ------------------------------------------------------------------ the steps
# step_sandbox: the premise. The repository, the installation, the project and
# the region are all read from the checkout itself, so the check measures the
# installation you work in rather than one it guessed.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a sandbox Bitbucket repository)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the run this check launches is launched on the repository the checkout belongs to"
  fi
  local url="" rc=0
  url=$(git -C "$SANDBOX" remote get-url origin 2>"$SCRATCH/origin.err") || rc=$?
  if [ "$rc" -ne 0 ] || [ -z "$url" ]; then
    stop "the checkout at $SANDBOX names no origin this check could read (git exited ${rc}): $(last_lines "$SCRATCH/origin.err"). The repository this check runs for is the one the checkout's own origin names, and a checkout without one is a repository this check cannot name"
  fi
  # The credential goes before the URL is matched, recorded or printed: a
  # credentialled origin reaches the refusals below, the scratch file and the
  # reader alike, and the reader's patterns cannot cross a `/` inside a userinfo,
  # so it reads the host of one as `https` — a refusal naming a host that is not
  # there, which is what the header's never-print rule is the smaller half of.
  url=$(printf '%s\n' "$url" | strip_credentials)
  printf '%s\n' "$url" >"$SCRATCH/origin.txt"
  local got host
  got=$(reader origin "$url" 2>"$SCRATCH/origin.reader.err") || stop "the origin of $SANDBOX could not be read: $(last_lines "$SCRATCH/origin.reader.err")"
  host=$(printf '%s\n' "$got" | sed -n 's/^host=//p')
  REPO=$(printf '%s\n' "$got" | sed -n 's/^repo=//p')
  if [ -z "$REPO" ] || [ "$REPO" = - ]; then
    stop "the origin of $SANDBOX is '$url', which names no repository: the run this check launches is named by the repository, and a URL this check cannot read is one it will not guess at"
  fi
  if [ "$host" != bitbucket.org ]; then
    stop "the origin of $SANDBOX is $url, which is not a Bitbucket repository (its host is '${host:-none}'): this check is the BITBUCKET shared-job half — a shared job mounts no Bitbucket token and reaches bitbucket.org through the credentials service — and a GitHub or GitLab checkout has nothing here for it to measure"
  fi
  # The repository's provider, in the policy's own canonical spelling
  # (projectpolicy.Canonical: "<provider>/<owner>/<name>"). This check is the
  # Bitbucket one — the host above is bitbucket.org or the check has stopped — and
  # the profile count below is provider-gated on it, so a profile serving another
  # provider is never counted as admitting this repository.
  REPO_PROVIDER=bitbucket
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
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout of the BITBUCKET repository ${REPO}, and it belongs to the ${INSTALL:-<no name>} installation (${PROJECT}, region ${REGION}): the run this check launches is that repository's, read in that installation"
}

# step_policy: the ONE profile this check is about, and the model access its last
# row is held to. The profile is read from the published policy rather than from
# the repository's own fugaro.yaml, because the shared job's name is built from
# the policy's profile name — and a repository admitted to two profiles leaves
# which one its own file selected unreadable, so that is a row the check refuses
# rather than guesses at.
step_policy() {
  if [ -n "$SANDBOX_STOP" ] || [ -z "$INSTALL" ]; then
    unmeasured policy "there is no installation to read the published policy of: the row above says why"
    PROFILE_STOP=yes
    return 0
  fi
  say "reading the published policy: fugaro project policy show --project ${INSTALL} --json"
  run_bounded "$CLI_TIMEOUT" policy "$FUGARO_BIN" project policy show --project "$INSTALL" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured policy "fugaro project policy show --project ${INSTALL} --json did not answer (exit ${BOUNDED_STATUS}): the profile that admits ${REPO} is what the shared job's name is built from, and nothing is launched on a policy nobody read. Its output is in $SCRATCH/policy.out and $SCRATCH/policy.err"
    PROFILE_STOP=yes
    return 0
  fi
  if ! reader policy "$SCRATCH/policy.out" "$REPO" "$REPO_PROVIDER" >"$SCRATCH/policy.txt" 2>"$SCRATCH/policy.reader.err"; then
    unmeasured policy "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/policy.out"
    PROFILE_STOP=yes
    return 0
  fi
  local n
  if [ "$(field "$SCRATCH/policy.txt" proj_admits)" != yes ]; then
    unmeasured policy "the published policy of ${INSTALL} does not admit ${REPO} to the PROJECT (its own allow_repos does not cover it), so no profile may admit it either and a launch would be refused before any execution exists: add it with 'fugaro project repos add <the repository's canonical name>' and publish ('fugaro project policy publish'), then run this check again"
    PROFILE_STOP=yes
    return 0
  fi
  n=$(field "$SCRATCH/policy.txt" profiles)
  case "${n:-0}" in
  0)
    fact policy FAIL "${REPO} is admitted to the project and to NO profile of ${INSTALL}'s published policy, so this launch would be refused before any execution exists and there is no shared job for this check to read: add the repository to that profile's allow_repos and publish ('fugaro project policy publish'), then run this check again"
    PROFILE_STOP=yes
    return 0
    ;;
  1) ;;
  *)
    unmeasured policy "${n} profiles of ${INSTALL}'s published policy admit ${REPO}, and which one its own fugaro.yaml selects is not a fact of the policy: the shared job's name is built from the profile's name and this check reads the profile from the document alone, so a repository admitted to more than one leaves every row below unmeasured. Name a sandbox Bitbucket repository exactly one profile admits and run this check again"
    PROFILE_STOP=yes
    return 0
    ;;
  esac
  PROFILE=$(field "$SCRATCH/policy.txt" profile)
  PROVIDER=$(field "$SCRATCH/policy.txt" provider)
  PROFILE_ACCOUNTS=$(field "$SCRATCH/policy.txt" account_names)
  SHARED_PREFIX="fugaro-p-${PROFILE}-bitbucket"
  if [ "$PROVIDER" != bitbucket ]; then
    fact policy FAIL "${REPO} is admitted to the profile '${PROFILE}', which serves provider '${PROVIDER:-<none>}': a shared job serves exactly ONE provider (the provider is part of the job's name and of the git credential it holds), and this check is the Bitbucket one. Name a sandbox BITBUCKET repository of a Bitbucket profile"
    PROFILE_STOP=yes
    return 0
  fi
  if [ -z "$PROFILE_ACCOUNTS" ]; then
    fact policy FAIL "the profile '${PROFILE}' admits ${REPO} and lists no claude_accounts, so its shared jobs hold NO model credential of their own: a shared job holds no Claude OAuth token (F17), and the model access of a run of this repository is the profile's account through the cascade — a profile with no account is a run whose model calls can only fail. Add the account to the profile (its claude_accounts, published from this machine's config: 'fugaro project policy publish') and re-apply with 'fugaro update', then run this check again"
    PROFILE_STOP=yes
    return 0
  fi
  local mode
  mode=$(field "$SCRATCH/policy.txt" mode)
  case "$mode" in
  - | '')
    unmeasured policy "${REPO} is on the profile '${PROFILE}' and the policy document carries no budget mode (it reads '${mode:-<none>}'), so whether the gateway exists for a run of this repository is NOT measured: the gateway exists in observe and enforce (D2071, GatewayOn), and a mode the document does not carry is neither. Publish the project policy so it carries one ('fugaro project policy publish', observe or enforce) and run this check again"
    PROFILE_STOP=yes
    return 0
    ;;
  esac
  fact policy PASS "${REPO} is admitted to the profile '${PROFILE}' (provider bitbucket), which lists the model account(s) ${PROFILE_ACCOUNTS} — the credential the model row below holds the run's first call to — and the published budget mode is '${mode}' (the mode is the gateway's; the credentials-service switch is the job's own environment, read next)"
}

# step_job: the profile's own shared Bitbucket jobs, read back before anything is
# launched. Every one of them has to carry the credentials-service switch — a job
# that names no service holds whatever credential it mounts and reaches nothing —
# and NONE of them may carry FUGARO_BITBUCKET_TOKEN: D2302's whole point is that a
# Bitbucket shared job holds no token at all and its git traffic reaches
# bitbucket.org through the service. Both are settled before the run is launched,
# because that run commits to a real repository and opens a pull request.
step_job() {
  local listed
  if [ -n "$PROFILE_STOP" ]; then
    unmeasured job "there is no profile to read a shared job of: the row above says why"
    JOB_STOP=yes
    return 0
  fi
  say "reading the installation's jobs: gcloud run jobs list --project=${PROJECT} --region=${REGION}"
  run_bounded "$CLI_TIMEOUT" jobs gcloud run jobs list --project="$PROJECT" --region="$REGION" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured job "gcloud run jobs list did not answer (exit ${BOUNDED_STATUS}): the jobs of the profile '${PROFILE}' are what the run has to land on, and a read that did not answer is not an installation with none. Its output is in $SCRATCH/jobs.out and $SCRATCH/jobs.err"
    JOB_STOP=yes
    return 0
  fi
  if ! reader jobs "$SCRATCH/jobs.out" "$SHARED_PREFIX" "$PROFILE" >"$SCRATCH/jobs.txt" 2>"$SCRATCH/jobs.reader.err"; then
    unmeasured job "gcloud run jobs list printed a document this check could not read: what it printed is in $SCRATCH/jobs.out"
    JOB_STOP=yes
    return 0
  fi
  listed=$(field "$SCRATCH/jobs.txt" jobs)
  SHARED_JOBS=$(field "$SCRATCH/jobs.txt" names)
  if [ "${listed:-0}" -lt 1 ]; then
    unmeasured job "no job of the installation is named '${SHARED_PREFIX}…' in 'gcloud run jobs list', so the profile '${PROFILE}' serves no shared Bitbucket job: the profile's jobs are the ones a run of ${REPO} would land on, and this check reads a run of a SHARED job (gcp.ProjectJobName: fugaro-p-<profile>-<provider>[-<account>]). Deploy the profile's job ('fugaro update', which deploys one per (profile, provider, account) the config's profiles list) and run this check again"
    JOB_STOP=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/jobs.txt" urls)" -lt "$listed" ]; then
    fact job FAIL "$(field "$SCRATCH/jobs.txt" urls) of ${listed} shared Bitbucket job(s) of the profile '${PROFILE}' carry FUGARO_CREDENTIALS_SERVICE_URL and the rest do not: the job that names no service holds whatever credential it mounts and reaches nothing, and a Bitbucket shared job holds NO token, so its git and model traffic would go nowhere. This is the profile's own switch and it is turned on for every job of the profile at once ('fugaro update', from this machine's local config). Re-apply and run this check again"
    JOB_STOP=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/jobs.txt" tokens)" -gt 0 ]; then
    fact job FAIL "$(field "$SCRATCH/jobs.txt" tokens) of the ${listed} shared Bitbucket job(s) of the profile '${PROFILE}' carry FUGARO_BITBUCKET_TOKEN: a Bitbucket shared job mounts no token at all (D2302) — its clone, fetch, push and REST traffic reach bitbucket.org through the credentials service, paired to the run's own repository — so a job holding one is a job whose run's git traffic would not be the service's. The jobs this check read: $(printf '%s' "$SHARED_JOBS" | tr ',' ' '). Re-apply with 'fugaro update' so the profile's jobs are rewritten without it, then run this check again"
    JOB_STOP=yes
    return 0
  fi
  fact job PASS "${listed} shared Bitbucket job(s) of the profile '${PROFILE}' are deployed — $(printf '%s' "$SHARED_JOBS" | tr ',' ' ') — every one of them carries FUGARO_CREDENTIALS_SERVICE_URL (the switch, which nothing but a launcher writes) and none of them carries FUGARO_BITBUCKET_TOKEN (D2302: the job holds no token at all)"
}

# task_write FILE RID: the throwaway task, by the FILE argument and never by a
# here-document on a command line (CLAUDE.md: an unquoted heredoc runs any
# backticked text). Its first line is the `# Task:` line `fugaro watch` and the
# pull request title come from, and what it asks for is the smallest thing that
# makes a run commit, push and open a pull request — which is what puts its git
# traffic and its model calls through the credentials service, the subject of
# this check.
task_write() { # task_write FILE RID
  {
    printf '# Task: Check 580 — the Bitbucket shared job (%s)\n\n' "$2"
    printf 'Add one line to `CHK580.md` at the repository root:\n\n'
    printf '    check 580 %s\n\n' "$2"
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
  task_write "$task" "$RUN_ID"
  say "launching the run (${RUN_ID}) in ${REPO} — it goes on in the cloud, about 40 minutes"
  run_bounded "$RUN_SECONDS" run "$FUGARO_BIN" run --repo "$REPO" --project "$INSTALL" \
    --run-id "$RUN_ID" --title "Check 580: the Bitbucket shared job" --task-file "$task" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    printf '%s' "fugaro run did not answer inside ${RUN_SECONDS}s and was ended: it may or may not have launched the run (${RUN_ID}), so nothing about it is read. Read the listing yourself ('fugaro runs ls --repo ${REPO} --project ${INSTALL}') before launching another one" >"$SCRATCH/launch-err"
    return 1
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    printf '%s' "fugaro run for ${REPO} exited ${BOUNDED_STATUS} and never launched a run: $(last_lines "$SCRATCH/run.err"). A launch refused before Cloud Run — a repository outside the profile, the project's own admission — is the launcher's own sentence, and it is the reading this row carries" >"$SCRATCH/launch-err"
    return 1
  fi
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
    printf '%s' "the run (${RUN_ID}) had not reached a final status after ${waited}s: status=$(printf '%s\n' "$row" | sed -n 's/^status=//p'). A row still in flight is not an answer about the credentials service — raise CHECK580_RUN_SECONDS, or read that run's row yourself ('fugaro runs show ${RUN_ID} --repo ${REPO}')" >"$SCRATCH/launch-err"
    return 1
  fi
  printf '%s\n' "$row"
}

# step_run: the ONE run. Three things are one fact here: it ended `succeeded`, its
# own row says it pushed, and it names a pull request — a run that pushed its
# branch and opened its pull request is only reachable if its git traffic and its
# model calls were answered.
step_run() {
  if [ -n "$JOB_STOP" ]; then
    unmeasured run "nothing is launched without the profile's own shared job: the row above says why"
    return 0
  fi
  RUN_ID="${RUN_BASE}-0580"
  # The second the run is launched in, recorded HERE and not inside the launch:
  # that call is read through a command substitution, which is a subshell, and a
  # value set there is the subshell's alone. log_freshness measures the read of
  # the service's lines from it, so a run that outlasted CHECK580_LOG_AGE still
  # has its FIRST model call in the answer.
  LAUNCH_EPOCH=$(date -u +%s)
  local row status pushed pr outcome
  if ! row=$(launch_and_wait); then
    unmeasured run "$(cat "$SCRATCH/launch-err" 2>/dev/null || true)"
    return 0
  fi
  # The row was read in a subshell and this is where its arrival is recorded: the
  # rows below are rows about that row, and one that was never read leaves them
  # unmeasured (RUN_ROW, set in the parent).
  RUN_ROW=yes
  status=$(field "$SCRATCH/row.txt" status)
  outcome=$(field "$SCRATCH/row.txt" outcome)
  pushed=$(field "$SCRATCH/row.txt" pushed)
  pr=$(field "$SCRATCH/row.txt" pr)
  if [ "$status" != succeeded ] || [ "$pushed" != yes ] || [ -z "$pr" ] || [ "$pr" = 0 ]; then
    fact run FAIL "the run (${RUN_ID}) of ${REPO} did not end with a push and a pull request: status=${status:-unread}, outcome=${outcome:-none}, pushed=${pushed:-none}, pr=${pr:-none (the row names no pull request)}$(printf '%s' "$(field "$SCRATCH/row.txt" halt_detail)" | sed 's/^/, halt: /')$(printf '%s' "$(field "$SCRATCH/row.txt" reason)" | sed 's/^/, reason: /'). A run on a Bitbucket shared job reaches every credential through the credentials service — its git traffic and its model calls both — so a run that could not push or open its pull request is the service, the profile's account or the job's own switch not serving it, and the rows below read the service's own lines for the calls it DID serve. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}' before anything else"
    return 0
  fi
  fact run PASS "the run (${RUN_ID}) of ${REPO} ended '${status}' (outcome=${outcome:-none}) and its own row says it pushed (pushed=yes) and names pull request #${pr}$(printf '%s' "$(field "$SCRATCH/row.txt" pr_url)" | sed 's/^/ (/; s/$/)/'): the task asks for one commit, a push and a pull request, and that is the positive half of this check. The branch and the pull request stay on ${REPO}: this check holds no Bitbucket credential of its own"
}

# step_landed: the job that run landed on, read off its own execution path. It
# has to be one of the profile's shared Bitbucket jobs: a run on a per-repository
# job (or on another profile's) was launched by a repository whose fugaro.yaml
# selects no profile, and every row below would be about a job this check is not
# about.
step_landed() {
  if [ "$RUN_ROW" != yes ]; then
    unmeasured landed "no terminal row of this check's own run was read, so there is no job to read: the row above says why"
    return 0
  fi
  local job
  job=$(field "$SCRATCH/row.txt" job)
  if [ -z "$job" ] || [ "$job" = - ]; then
    unmeasured landed "the row of the run (${RUN_ID}) names no execution, so the job it ran on cannot be read: the execution path is the only place that job is written down. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}' — a run whose own record carries no execution is one whose job this row cannot read"
    return 0
  fi
  case ",$SHARED_JOBS," in
  *",$job,"*)
    fact landed PASS "the run (${RUN_ID}) ran on '${job}', one of the profile '${PROFILE}''s shared Bitbucket jobs (the installation's are: $(printf '%s' "$SHARED_JOBS" | tr ',' ' ')) — so the launch landed on a SHARED job and the rows below are about that job's run"
    ;;
  *)
    fact landed FAIL "the run (${RUN_ID}) ran on '${job}', which is not one of the profile '${PROFILE}''s shared Bitbucket jobs (the installation's are: $(printf '%s' "${SHARED_JOBS:-<none>}" | tr ',' ' ')): a run lands on a shared job only when the repository's own fugaro.yaml SELECTS the profile — 'job_profile:' in ${REPO}'s fugaro.yaml — and this installation deploys that profile's job. Make the repository select the profile (and re-apply: 'fugaro update'), then run this check again"
    ;;
  esac
}

# log_freshness: how far back the service's lines are read. The run's FIRST model
# call is the oldest forward line the service wrote for it, so the window has to
# reach back to the run's own start: CHECK580_LOG_AGE is the floor and this widens
# it by the time since the launch, which is what a run that outlasted the knob —
# CHECK580_RUN_SECONDS waits 2400s for one — would otherwise lose the head of.
log_freshness() {
  local since=$LOG_AGE mins
  if [ -n "$LAUNCH_EPOCH" ] && [ -n "$LOG_AGE_MINUTES" ]; then
    mins=$(( ($(date -u +%s) - LAUNCH_EPOCH) / 60 + 5 ))
    [ "$mins" -gt "$LOG_AGE_MINUTES" ] && since="${mins}m"
  fi
  printf '%s' "$since"
}

# audit_read RID OUT: the run's service lines, retried while Cloud Logging
# catches up — ingestion lags the request by seconds. It returns 0 only when the
# last read ANSWERED: `gcloud logging read` exiting non-zero (no permission on
# the log, an expired session, a network answer) is NOT the service having
# written no line, and a row that took a failed read for silence would FAIL this
# check on nothing. The reason is written beside the file it is about (OUT.unread)
# and the two rows below read it with audit_unread.
audit_read() {
  local rid=$1 out=$2 waited=0 filter rc=0
  filter='resource.type="cloud_run_revision" resource.labels.service_name="fugaro-credentials" jsonPayload.run="'"$rid"'"'
  while :; do
    rc=0
    # --order=asc, because gcloud answers the NEWEST --limit lines by default and
    # a run of this repository writes far more than that: the run's first model
    # call — the row that proves F17 — is the OLDEST forward line, and read
    # newest-first it is not in the answer at all, so the row would be decided on
    # a later call's credential. The git lines the row above counts are the run's
    # earliest ones too (the clone and the fetch), so the old end is the end both
    # rows are read from.
    gcloud logging read "$filter" --project="$PROJECT" --freshness="$(log_freshness)" \
      --limit="$LOG_LIMIT" --order=asc --format=json >"$out" 2>"$SCRATCH/audit.err" || rc=$?
    if [ "$rc" -eq 0 ] && reader audit "$out" "$rid" 2>/dev/null | grep -qE '^audit='; then return 0; fi
    if [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
      [ "$rc" -eq 0 ] || {
        printf '%s' "the service's own lines for the run (${rid}) could not be read: 'gcloud logging read' exited ${rc} and printed: $(last_lines "$SCRATCH/audit.err"). Every row read off those lines is read off what the service wrote for that run, and a read that did not answer is NOT the service having written no line: nothing here says whether that run was served or not served. Read the log yourself ('gcloud logging read' with that run's id, and 'gcloud logging read --help' for the filter above) and run this check again" >"$out.unread"
        return 1
      }
      return 0
    fi
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
}

audit_unread() { cat "$1.unread" 2>/dev/null || true; }

# step_served: the service's own lines for the run, and the two rows they carry.
# The GIT row is the clone and the fetch (the receive-pack advertisement and the
# push are the same purpose, and the run's own row is what says it pushed); the
# MODEL row is the run's first model call, whose credential name has to be one of
# the profile's claude_accounts (F17).
step_served() {
  if [ "$RUN_ROW" != yes ]; then
    unmeasured git "no terminal row of this check's own run was read, so the service's lines for it are not read: the row above says why"
    unmeasured model "no terminal row of this check's own run was read, so the service's lines for it are not read: the row above says why"
    return 0
  fi
  local lines=$SCRATCH/audit.json got
  say "reading the service's own lines for ${RUN_ID}: gcloud logging read (credsvc: audit)"
  if ! audit_read "$RUN_ID" "$lines"; then
    unmeasured git "$(audit_unread "$lines")"
    unmeasured model "$(audit_unread "$lines")"
    return 0
  fi
  got=$(reader audit "$lines" "$RUN_ID" 2>"$SCRATCH/audit.reader.err" || true)
  local git_lines git_creds first_forward purposes triples
  git_lines=$(printf '%s\n' "$got" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|git|.*/\1/p' | grep -c . || true)
  git_creds=$(printf '%s\n' "$got" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|git|.*/\1/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  first_forward=$(printf '%s\n' "$got" | sed -n 's/^first_forward=//p')
  # What the service's lines DID carry, on one sorted line each: a row that found
  # nothing has to say what it read instead, and "nothing at all" is written out
  # rather than left as an empty gap.
  purposes=$(printf '%s\n' "$got" | sed -n 's/^audit=[^|]*|[^|]*|[^|]*|\([^|]*\)|.*/\1/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  triples=$(printf '%s\n' "$got" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|\([^|]*\)|.*/\1|\2|\3/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  if [ "${git_lines:-0}" -lt 1 ]; then
    fact git FAIL "the service wrote NO served git line (purpose 'git', status ok) for the run (${RUN_ID}) of ${REPO}, while the run pushed and opened a pull request: a Bitbucket shared job holds no token at all (D2302), so its clone, fetch and push reach bitbucket.org through this service or not at all — a run whose git traffic was served by something else is a job that still holds a Bitbucket token, or a route that did not go through the service. The purposes the service's own lines for this run carry: ${purposes:-<none>}. Check the job's own environment ('gcloud run jobs describe <the job of the row above>') and the service's lines for this run"
    return 0
  fi
  fact git PASS "${git_lines} served git line(s) of the credentials service's own audit for the run (${RUN_ID}) — purpose 'git', status ok — naming the credential(s) ${git_creds:-<none>}: that is the git proxy's own credential (the seeded 'bitbucket-token', or the document key of a named token once one is chosen, T8/R9, and no tier — a git line names a credential and never one of the cascade's), a NAME and never a value, and it is not the model account. The run's clone and fetch reached the service and were forwarded by it. The purpose does not tell a fetch from a push (credsvc's PurposeGit is one request of a run's git traffic), so the push itself is read off the run's own row above (pushed=yes, pull request #$(field "$SCRATCH/row.txt" pr))"
  if [ -z "$first_forward" ]; then
    fact model FAIL "the service wrote no served model line (purpose 'forward', status ok) for the run (${RUN_ID}) of ${REPO}, so nothing shows WHICH credential answered its model calls: a shared job holds no Claude OAuth token (F17) and its model access is the profile '${PROFILE}''s claude_accounts (${PROFILE_ACCOUNTS}) through the cascade, so a run whose first model call is not one of the service's own lines was answered somewhere else — the job's own mounts, or a credential this service does not serve. The credential|tier|purpose the service's lines for this run name: ${triples:-<none>}. Check the job's own environment ('gcloud run jobs describe', the mounts and the service URL) and the service's bundle ('fugaro creds set')"
    return 0
  fi
  case ",${PROFILE_ACCOUNTS}," in
  *",$first_forward,"*)
    fact model PASS "the run's first model call (${RUN_ID}) was served with the credential '${first_forward}', one of the profile '${PROFILE}''s claude_accounts (${PROFILE_ACCOUNTS}) — the earliest served line of purpose 'forward' the service wrote for that run, read by name and never by value. That is F17: a Bitbucket shared job holds no Claude OAuth token, and its model access is the profile's account through the cascade"
    ;;
  *)
    fact model FAIL "the run's first model call (${RUN_ID}) was served with the credential '${first_forward}', which is NOT one of the profile '${PROFILE}''s claude_accounts (${PROFILE_ACCOUNTS}): a shared job holds no Claude OAuth token (F17), so its model access is the profile's account through the cascade, and a line naming another credential is a call answered by something this profile does not serve — the project's paid key above all. The credential|tier|purpose the service's lines for this run name: ${triples:-<none>}. Check the policy's own accounts block for this profile ('fugaro project policy show --json') and the service's bundle ('fugaro creds set')"
    ;;
  esac
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 580 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation and the project the checkout belongs to"
  say "  [read] fugaro project policy show --json — the one profile that admits the repository, its provider, its claude_accounts and the gateway's mode"
  say "  [network, read-only] gcloud run jobs list — the profile's shared Bitbucket jobs: the switch every one carries and the token none may hold"
  say "  [network, BILLABLE] ONE run of the repository: fugaro run --repo <the repository> — one commit, a push and a pull request, about 40 minutes, its model calls billed"
  say "  [read] fugaro runs ls --json — the run's own row, polled until it is terminal: status, pushed, the pull request, and the job its execution names"
  say "  [network, read] gcloud logging read — the credentials service's own lines for that run: purpose 'git' (the clone and the fetch) and the first purpose 'forward' line (the run's first model call, and which credential answered it)"
  fact sandbox INFO "planned: a real run measures this row; --dry-run did not"
  fact policy INFO "planned: a real run measures this row; --dry-run did not"
  fact job INFO "planned: a real run measures this row; --dry-run did not"
  fact run INFO "planned: a real run measures this row; --dry-run did not"
  fact landed INFO "planned: a real run measures this row; --dry-run did not"
  fact git INFO "planned: a real run measures this row; --dry-run did not"
  fact model INFO "planned: a real run measures this row; --dry-run did not"
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no policy read, no job list, no launch and no log read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong sandbox policy job run landed git model restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 580 RESULT\n' >&3
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
  printf 'CHECK 580 RESULT END (exit %s)\n' "$status" >&3
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
    die "Check 580 needs 'fugaro' on PATH: the check drives the run you have installed, and it is not there"
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
  step_job
  step_run
  step_landed
  step_served
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
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a sandbox Bitbucket repository, which is what this check runs for"
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

SANDBOX=${CHECK580_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 580 needs --sandbox: the checkout of a sandbox Bitbucket repository. The repository this check launches the run on, and the installation it reads it in, are both taken from that checkout. Usage: bash scripts/live-checks/check-580.sh --sandbox <a sandbox Bitbucket repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
DRY_RUN=${CHECK580_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK580_KEEP_SCRATCH:-}
RUN_BASE=${CHECK580_RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}
RUN_SECONDS=${CHECK580_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK580_POLL_SECONDS:-20}
LOG_AGE=${CHECK580_LOG_AGE:-1h}
# The knob in minutes, for the comparison log_freshness makes against the time
# since the launch. A spelling this does not convert — anything but <digits>m,
# <digits>h or <digits>d — leaves it empty, and the knob is then used exactly as
# written and never widened: a unit this check cannot read is not one it may
# quietly shorten.
LOG_AGE_MINUTES=""
LOG_AGE_NUM=${LOG_AGE%?}
case "$LOG_AGE" in
*m | *h | *d)
  case "$LOG_AGE_NUM" in
  "" | *[!0-9]*) ;;
  *)
    case "${LOG_AGE#"$LOG_AGE_NUM"}" in
    m) LOG_AGE_MINUTES=$LOG_AGE_NUM ;;
    h) LOG_AGE_MINUTES=$(( LOG_AGE_NUM * 60 )) ;;
    d) LOG_AGE_MINUTES=$(( LOG_AGE_NUM * 1440 )) ;;
    esac
    ;;
  esac
  ;;
esac
LOG_LIMIT=${CHECK580_LOG_LIMIT:-200}
LOG_WAIT_SECONDS=${CHECK580_LOG_WAIT_SECONDS:-120}
LOG_POLL_SECONDS=${CHECK580_LOG_POLL_SECONDS:-5}
DOCTOR_TIMEOUT=${CHECK580_DOCTOR_TIMEOUT_SECONDS:-180}
CLI_TIMEOUT=${CHECK580_CLI_TIMEOUT_SECONDS:-180}
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
