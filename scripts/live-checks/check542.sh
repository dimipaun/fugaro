#!/usr/bin/env bash
# Check 542 — the App's merge permission, the base's bypass lists, and the
# gated merge mint the daemon asks the credentials service for.
#
# Plan task E15 (docs/outer-loop-escalation/implementation-plan.md) writes this
# check and E16 builds the route it measures (D2048, D2049). What no test of the
# service can say is whether YOUR installation is the one the mint works on: the
# App's merge permission is granted per repository by you and by nobody else
# (D2048), the base's bypass lists are GitHub's own state, and whether the
# daemon can present an identity the service names is a fact about a Terraform
# grant and a variable that either exist on your project or do not.
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to (`fugaro doctor --json`)
#     and the pull request the mint is about, read from the host with `gh pr
#     view`: it has to be OPEN, and it has to carry a head — the mint is bound to
#     one head, and it is the head the daemon's gate admitted;
#   * **the App's own installation on the sandbox repository**, read with
#     `gh api /user/installations` and that installation's repository list: the
#     App ID, its slug, and the permission set GitHub has granted it. Three
#     permissions are required and each has its own reason: contents write and
#     pull requests write are the set the mint hands out
#     (`internal/credsvc`'s mergeTokenPermissions), and administration read is
#     what the mint's gate reads the base's rulesets and its branch protection
#     under (`mergeGateReadPermissions`) — an App without it fails the gate
#     closed and mints nothing at all;
#   * **the App is on neither bypass list** (G14): the rulesets covering the
#     pull request's base, each listed ruleset read again by id because the
#     bypass list is only in the per-id answer, and the base's classic branch
#     protection, whose own list is the second place GitHub lets an App merge
#     past a protected branch. A page of rulesets that comes back full is a
#     finding, and so is a bypass list that could not be read: a list this check
#     cannot read is not one it may call absent;
#   * **the daemon's own identity**, which is the half that cannot be granted
#     from this terminal: the service's class list (`AuthConfig.
#     OrchestratorAccounts`), the environment variable that would carry it, the
#     daemon account's own existence and the invoker grant that lets it call the
#     service. On the release this check was written against all three are
#     absent, and the row says so by name rather than reporting the mint's
#     refusal as a mystery;
#   * **the mint itself**, as the identity the daemon holds:
#     `POST /v1/github/merge-token` with the one repository, the one pull
#     request, the head it must still be at and the App ID — a token back, with
#     its expiry, and the token itself never printed;
#   * **the two refusals the plan names**: the same call naming a head the pull
#     request is not at is refused, and the same call presenting a run identity
#     is refused by name. Beside them is the third refusal SECURITY.md states,
#     the one that holds on every installation whether the class is wired or
#     not: the token your own credentials mint — the one a coding-agent session
#     on this machine holds — is refused before any account list is read, because
#     it cannot carry this service's URL as its audience;
#   * **the service's audit log**: one `credsvc: audit` line for each call of
#     this run that an admitted caller answered, and none for a call refused
#     before the caller was verified (that refusal is answered ahead of any
#     identity, and a line for it would name a caller the service never had). A
#     line is this run's by its own instant — the row matches the line's
#     timestamp against the instant the call went out — so another caller's line
#     with the same status is never read as this run's.
#
# What it does NOT do: it grants nothing (the App's permission is your own
# per-repository step, D2048), it changes no ruleset and no branch protection, it
# merges nothing, it launches no run, it writes no setting and no policy, and it
# never uses the token a successful mint answers with.
#
# Usage:      bash scripts/live-checks/check542.sh --sandbox <checkout> --pr <number>
#             bash scripts/live-checks/check542.sh --sandbox <checkout> --pr <number> --app-id <id>
#             bash scripts/live-checks/check542.sh --sandbox <checkout> --pr <n> --dry-run
#             bash scripts/live-checks/check542.sh --help
# Needs:      bash, mktemp, date, python3 (every --json document is read with
#             it), gcloud (authenticated as YOU, with permission to impersonate
#             the daemon's account), gh (as YOU, with read access to the sandbox
#             repository), curl (the mint calls) and fugaro (the installed
#             release).
# Where:      your own terminal and the **sandbox project and repository** only.
#             A coding agent's session is refused by name: this check asks the
#             service for a merge credential, which is the one credential the
#             gate exists to keep out of a session. A project or an account
#             naming belong is refused too.
# Creates:    nothing on the installation — no grant, no permission, no setting,
#             no ruleset, no merge, no run. One scratch directory, removed from
#             the exit trap. Up to four merge-mint calls: each one an admitted
#             caller answers counts against the service's per-repository rate
#             (ten an hour, D2049) and writes one audit line. The installation
#             token a successful mint answers with is written into the scratch
#             directory, removed by this check before the scratch is kept or
#             removed, never printed and never used.
# Costs:      nothing but a handful of GitHub and Cloud Run API calls: no model
#             call and no run.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check542_test.sh) drives it against fake tools only.
# Output:     progress on stderr, ONE block on stdout headed CHECK 542 RESULT,
#             then CHECK 542 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, or a read it needed did not answer. A key the run never
#             reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches the block, a
# command line or a file this check keeps. The ID tokens gcloud mints go
# straight into mode-0600 curl config files, so they are never a shell variable
# and never an argument; the installation token a successful mint answers with is
# read field by field by the check's own reader and deleted before the scratch is
# dealt with, whatever way the run ended; and no App key is read at all — the
# permission rows read GitHub's own answer about the App, not the key.
#
# Knobs (all CHECK542_*): DRY_RUN, KEEP_SCRATCH, SANDBOX, PR, APP_ID, RUN_ID (the
# id the run-identity probe presents), SERVICE_NAME (fugaro-credentials),
# SERVICE_URL (read from Cloud Run when empty), ORCHESTRATOR_ACCOUNT (the
# daemon's own account; the installation's `fugaro-orch` when empty), REGION
# (us-east5), LOG_FRESHNESS (30m), LOG_SKEW_SECONDS (60, the clock slack the
# audit row allows between a call's instant here and the line's instant in the
# log), GCP_TIMEOUT_SECONDS (180),
# CLI_TIMEOUT_SECONDS (180) and TOKEN_TIMEOUT_SECONDS (60).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check mints ID
# tokens and asks the service for an installation token, and a trace of that is a
# second copy of the run — with the token in it.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

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
# finding about the code. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2, unless a row below
# is a finding, which then decides the exit).
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

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 542 needs bash"
  local t
  for t in mktemp date python3 gcloud gh curl fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 542 needs '$t' and it is not on PATH"
  done
}

# The route this check asks for a merge token. It is internal/credsvc's
# MergeMintPath and the same path internal/creds's client spells — the two are
# held to each other by that package's own test — and it is written out here
# because a live check goes over the wire, where the client's constant is not.
MERGE_MINT_PATH=/v1/github/merge-token

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk542-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REGION_USED=""
SLUG=""
PR=""
PR_STATE=""
HEAD=""
BASE=""
APP_ID=""
APP_SLUG=""
APP_INSTALLATION=""
APP_PERMS=""
SERVICE_URL=""
DAEMON=""
DAEMON_EXISTS=no
DAEMON_CLASS_ENV=""
DAEMON_INVOKER=""
DAEMON_STATUS=""
DAEMON_READ=""
IAM_STATUS=""
IAM_READ=""
MINT_CALLS=""       # one `key<TAB>status<TAB>admitted<TAB>instant` line per call this check made
MINT_ANSWERED=0
MINT_STARTED_AT=""  # the instant the call now being made went out, in seconds (post_mint)
scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk542.XXXXXXXX") || die "Check 542 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  MINT_CALLS="$SCRATCH/mint.calls"
  : >"$MINT_CALLS"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

scrub_credentials() {
  # The one file a credential ever reaches: the answer of a mint that passed its
  # gate. A run that ends with KEEP_SCRATCH would otherwise leave an installation
  # token on the disk for as long as the owner kept the directory, so it is
  # removed on every exit path rather than by the scratch's own removal.
  local removed=""
  if [ -s "$SCRATCH/mint.body" ]; then
    rm -f "$SCRATCH/mint.body" && removed="the installation token the mint answered with"
  fi
  local f
  for f in "$SCRATCH"/token-*.out "$SCRATCH"/token-*.curl "$SCRATCH"/mint-*.body; do
    [ -e "$f" ] || continue
    rm -f "$f"
  done
  if [ -n "$removed" ]; then
    say "${removed} was written into the scratch directory and is now removed; it was never read into a shell variable, never printed and never used"
  fi
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
    say "CHECK542_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with grep
# would report a permission the installation does not grant the day a document is
# re-wrapped. Every reader prints `key=value` lines or `\t`-separated rows, and
# none of them prints a value the document carried unless the field is a name: the
# service's environment is read as NAMES and a set-or-empty flag, because an
# environment entry's value is a value and a value is what this check never
# copies into its output.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import datetime
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def flat(value):
    return " ".join(str(value or "").split())


def yes(value):
    return "true" if value else "false"


def epoch_of(stamp):
    # A log line's own instant as seconds since the epoch, so the audit row can
    # compare it with the instant a call was made in arithmetic rather than in
    # string order (Cloud Logging's spelling is RFC3339 in UTC, to the
    # nanosecond, and a timestamp this reader cannot read is 0: an instant it
    # does not know can never be the instant of a call this check made).
    try:
        return int(
            datetime.datetime.strptime(str(stamp)[:19], "%Y-%m-%dT%H:%M:%S")
            .replace(tzinfo=datetime.timezone.utc)
            .timestamp()
        )
    except (TypeError, ValueError):
        return 0


def ref_covers(include, exclude, base):
    # Whether a ruleset's conditions reach one branch, decided only as far as
    # this reader can decide it: an include list with no pattern at all covers
    # every ref, a pattern this reader does not translate COVERS (the direction
    # that refuses — a ruleset read as not covering is one whose bypass list is
    # never seen), and everything else is compared as the literal ref GitHub
    # writes it as.
    ref = "refs/heads/" + base

    def decide(pattern):
        if pattern in (ref, "~ALL", "~DEFAULT_BRANCH"):
            return "covers"
        for ch in "*?[\\":
            if ch in pattern:
                return "covers"
        return "not-covers"

    for pattern in exclude:
        if decide(pattern) == "covers":
            return "not-covers"
    if not include:
        return "covers"
    for pattern in include:
        if decide(pattern) == "covers":
            return "covers"
    return "not-covers"


what = sys.argv[1]
rest = sys.argv[3:]

if what == "http":
    # The header block `gh api --include --silent` prints: the status line, and
    # nothing else. Read before the JSON load below, because this document is not
    # JSON and a reader that tried would answer a traceback where a status is.
    code = ""
    with open(sys.argv[2], "r", encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if line.startswith("HTTP/"):
                parts = line.split()
                if len(parts) > 1:
                    code = parts[1]
                break
    print("code=%s" % code)
    raise SystemExit(0)

doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    print("gcp_project=%s" % flat(project.get("gcp_project")))
    print("region=%s" % flat(project.get("region")))
elif what == "name":
    print("nameWithOwner=%s" % flat(doc.get("nameWithOwner")))
elif what == "pr":
    print("number=%s" % (doc.get("number") if doc.get("number") is not None else ""))
    print("state=%s" % flat(doc.get("state")))
    print("is_draft=%s" % yes(doc.get("isDraft")))
    print("head_sha=%s" % flat(doc.get("headRefOid")))
    print("base=%s" % flat(doc.get("baseRefName")))
    print("url=%s" % flat(doc.get("url")))
elif what == "installations":
    # The permission map is flattened to "name:level" pairs and sorted, so two
    # runs of the same installation print the same string.
    rows = doc.get("installations") or []
    print("count=%d" % len(rows))
    for row in rows:
        perms = row.get("permissions") or {}
        listed = ",".join("%s:%s" % (name, perms[name]) for name in sorted(perms))
        print("inst\t%s\t%s\t%s\t%s\t%s" % (
            row.get("id") or "",
            row.get("app_id") or "",
            flat(row.get("app_slug")),
            flat(row.get("repository_selection")),
            listed,
        ))
elif what == "repos":
    rows = doc.get("repositories") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("repo\t%s" % flat(row.get("full_name")))
elif what == "rulesets":
    rows = doc if isinstance(doc, list) else (doc.get("rulesets") or [])
    print("count=%d" % len(rows))
    for row in rows:
        print("rs\t%s" % (row.get("id") if row.get("id") is not None else ""))
elif what == "ruleset":
    base = rest[0] if rest else ""
    actors = []
    for actor in doc.get("bypass_actors") or []:
        actors.append("%s:%s" % (flat(actor.get("actor_type")), actor.get("actor_id")))
    print("name=%s" % flat(doc.get("name")))
    print("target=%s" % flat(doc.get("target")))
    print("enforcement=%s" % flat(doc.get("enforcement")))
    print("list=%s" % yes("bypass_actors" in doc))
    print("actors=%s" % ",".join(actors))
    # A covering list with NO bypass_actors key at all is a hidden list, and a
    # hidden list is not an empty one (G14): `has_list` is printed beside the
    # actors for that reason.
    includes = ((doc.get("conditions") or {}).get("ref_name") or {}).get("include") or []
    excludes = ((doc.get("conditions") or {}).get("ref_name") or {}).get("exclude") or []
    print("include=%s" % ",".join(flat(p) for p in includes))
    print("exclude=%s" % ",".join(flat(p) for p in excludes))
    print("coverage=%s" % ref_covers(includes, excludes, base))
    # GitHub answers current_user_can_bypass for the identity that ASKED. A read
    # with the owner's own token therefore says nothing about the App, and it is
    # printed as what it is rather than read as the App's answer.
    print("asking=%s" % flat(doc.get("current_user_can_bypass")))
elif what == "protection":
    reviews = doc.get("required_pull_request_reviews")
    allowances = (reviews or {}).get("bypass_pull_request_allowances") or {}
    apps = allowances.get("apps") or []
    print("required_reviews=%s" % yes(reviews is not None))
    print("allowances=%s" % yes("bypass_pull_request_allowances" in (reviews or {})))
    print("apps=%s" % ",".join(str(a.get("id")) for a in apps if a.get("id") is not None))
elif what == "service":
    print("url=%s" % flat((doc.get("status") or {}).get("url")))
    containers = (((doc.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or []
    count = 0
    for container in containers:
        for entry in container.get("env") or []:
            count += 1
            source = entry.get("valueSource") or {}
            ref = source.get("secretKeyRef") or {}
            if ref:
                print("env\t%s\t%s\tsecret" % (flat(entry.get("name")), "set"))
            else:
                value = entry.get("value")
                print("env\t%s\t%s\tvalue" % (flat(entry.get("name")), "empty" if not value else "set"))
    print("count=%d" % count)
elif what == "account":
    print("email=%s" % flat(doc.get("email")))
elif what == "policy":
    members = []
    roles = []
    for binding in doc.get("bindings") or []:
        role = flat(binding.get("role"))
        if role == "roles/run.invoker":
            roles.append(role)
            for member in binding.get("members") or []:
                members.append(flat(member))
    print("invoker_bindings=%d" % len(roles))
    for member in members:
        print("invoker\t%s" % member)
elif what == "mintanswer":
    # A merge mint's answer, read field by field: the token is reported as
    # PRESENT or absent and never printed, and the rest of the body is never
    # quoted either, because this is the one answer of the route that carries a
    # credential.
    token = doc.get("token")
    print("has_token=%s" % yes(bool(token)))
    print("expires_at=%s" % flat(doc.get("expires_at")))
    print("repository=%s" % flat(doc.get("repository")))
    print("error=%s" % flat(doc.get("error")))
    print("message=%s" % flat(doc.get("message")))
elif what == "audit":
    rows = doc if isinstance(doc, list) else (doc.get("entries") or [])
    print("count=%d" % len(rows))
    for row in rows:
        payload = row.get("jsonPayload") or {}
        print("line\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d" % (
            flat(payload.get("caller")),
            flat(payload.get("repository")),
            flat(payload.get("status")),
            flat(payload.get("credential")),
            flat(payload.get("run")),
            flat(payload.get("job")),
            flat(row.get("timestamp")),
            epoch_of(row.get("timestamp")),
        ))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST [ARGS...]: answer one query into a file, and fail the
# step when the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST [ARGS...]
  local query=$1 file=$2 dest=$3
  shift 3
  python3 "$SCRATCH/read.py" "$query" "$file" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

perm_level() { # perm_level "name:level,name:level" NAME
  printf '%s' "$1" | tr ',' '\n' | awk -F: -v n="$2" '$1 == n { print $2; exit }'
}

# err_text FILE: the first line a tool wrote to stderr, for a row that has to say
# what a refusal said. Every caller of it is a tool whose stderr carries no
# credential — a gcloud refusal, a gh error, a curl message — and the rows that
# quote an answer's body quote the service's own sentences and never a token.
err_text() { # err_text FILE
  head -n 1 "$1" 2>/dev/null | cut -c 1-400 || true
}

# ---------------------------------------------------------------- the bounds
# bound_loop SECONDS PID: watch one background job to its bound and end it when
# the bound is reached. A hung cloud call would otherwise outlive the check that
# gave up on it, which is the one thing the bound exists to prevent — so the
# child of both helpers below execs, and the pid watched here IS the command.
bound_loop() { # bound_loop SECONDS PID
  local secs=$1 pid=$2 waited=0
  BOUNDED_TIMED_OUT=no
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

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. The ERR trap is dropped in the child: this check
# runs commands whose failing exit status is one of its own readings (a 404 that
# means "no classic protection", an installation whose repositories cannot be
# read), and a trap inherited here would turn each of them into "an internal
# error" and a status of 2 before the step could report it. Each key's files are
# STARTED FRESH: the mint's answer and the status curl printed are two writers on
# two paths, and a body a second document was appended to is a document nothing
# can parse.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  bound_loop "$secs" "$!"
  # Whatever a token mint or a mint's answer left behind is readable by this
  # shell and by nobody else, whatever umask the owner's shell carries.
  chmod 600 "$SCRATCH/$key.out" "$SCRATCH/$key.err" 2>/dev/null || true
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check542.sh --sandbox <checkout> --pr <n> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the ID token it mints and the installation token the service answers with into the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a token it minted or a token it was answered"
}

# guard_agent_session: this check asks the credentials service for a merge
# credential. That token is exactly what the mint's gate exists to keep out of a
# coding agent's session, so no agent runs this — and the same fence refuses the
# daemon's identity to one anyway.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 542 asks the credentials service for a merge token and reads your installation's own GitHub App state, at your own terminal, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for — it asks the service for a merge credential, which is the credential the gate exists to keep out of a session"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}" in
  *belong*) stop "the account or project this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

version_row() {
  local v=""
  run_bounded "$CLI_TIMEOUT" version "$SANDBOX" "$FUGARO_BIN" version
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ]; then
    v=$(head -n 1 "$SCRATCH/version.out" 2>/dev/null || true)
  fi
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the build this check drives: the merge mint it reads is the one you have installed)"
}

# ---------------------------------------------------------------- the steps
# step_installation: the premise. The project and the region come from the
# checkout itself (`fugaro doctor --json`), so the gcloud reads below ask the
# installation you actually work in rather than one this check guessed, and the
# repository name comes from the host (`gh repo view`) rather than from a path.
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the installation whose service this check calls is that repository's project's"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured installation "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the installation this checkout belongs to, and every row below is about it. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured installation "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  REGION_USED=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$PROJECT" ]; then
    unmeasured installation "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox checkout belongs to, and the service, the daemon's account and the App's installation are all that installation's. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  if [ -z "$REGION_USED" ]; then
    REGION_USED="$REGION"
  fi
  say "reading the repository: gh repo view --json nameWithOwner, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" slug "$SANDBOX" gh repo view --json nameWithOwner
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured installation "gh repo view --json nameWithOwner did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the pull request, the App's installation and the bypass lists are the repository's, and the repository's name could not be read. What gh printed is in $SCRATCH/slug.err"
    return 0
  fi
  if ! read_json name "$SCRATCH/slug.out" "$SCRATCH/slug.txt"; then
    unmeasured installation "gh repo view printed a document this check could not read: what it printed is in $SCRATCH/slug.out"
    return 0
  fi
  SLUG=$(field "$SCRATCH/slug.txt" nameWithOwner)
  if [ -z "$SLUG" ]; then
    unmeasured installation "gh repo view answered without a repository name: this check cannot say which repository's App installation and pull request it is about. What it printed is in $SCRATCH/slug.out"
    return 0
  fi
  DAEMON="${ORCHESTRATOR_ACCOUNT:-fugaro-orch@${PROJECT}.iam.gserviceaccount.com}"
  fact installation PASS "${SANDBOX} is a Fugaro repository's checkout of the installation ${PROJECT} (region ${REGION_USED}), and its repository is ${SLUG}: the service this check calls, the daemon whose identity it presents and the App's installation all belong to that installation"
}

# step_pull_request: the pull request the mint is about. It has to be OPEN and it
# has to carry a head and a base: the mint names the head the pull request must
# still be at, and the bypass lists this check reads are the base's. Its draft
# state is read and reported and decides nothing here — a mint is a credential,
# not a merge, and what the gate holds a draft back for is the ladder's own row
# (Check 540's).
step_pull_request() {
  if [ -z "$SLUG" ]; then
    unmeasured pull-request "there is no repository to read a pull request from: the row above says why"
    return 0
  fi
  say "reading the pull request: gh pr view ${PR} --repo ${SLUG} --json state,isDraft,headRefOid,baseRefName,url"
  run_bounded "$CLI_TIMEOUT" pr "$SANDBOX" gh pr view "$PR" --repo "$SLUG" \
    --json state,isDraft,headRefOid,baseRefName,url,number
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pull-request "gh pr view ${PR} did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the pull request the mint is bound to. Its output is in $SCRATCH/pr.err"
    return 0
  fi
  if ! read_json pr "$SCRATCH/pr.out" "$SCRATCH/pr.txt"; then
    unmeasured pull-request "gh pr view ${PR} printed a document this check could not read: what it printed is in $SCRATCH/pr.out"
    return 0
  fi
  PR_STATE=$(field "$SCRATCH/pr.txt" state)
  HEAD=$(field "$SCRATCH/pr.txt" head_sha)
  BASE=$(field "$SCRATCH/pr.txt" base)
  case "$PR_STATE" in
  OPEN) ;;
  *)
    fact pull-request FAIL "the pull request ${SLUG}#${PR} is ${PR_STATE:-unreadable}: the mint's gate reads a pull request that is still open (a merged or closed one is refused before the head is compared), so there is nothing here for the mint to be asked about. Point --pr at an open pull request of the sandbox repository"
    # The head goes with the finding: four mint calls on a pull request the gate
    # refuses before it compares a head would measure nothing, count against the
    # service's own rate and write lines about a call this row has just said
    # there is no subject for. The mint rows read `not measured` and point here.
    HEAD=""
    return 0
    ;;
  esac
  if [ -z "$HEAD" ]; then
    unmeasured pull-request "gh pr view answered without a head: the mint's whole binding is the head the call names, and without one there is nothing to ask for. What it printed is in $SCRATCH/pr.out"
    return 0
  fi
  if [ -z "$BASE" ]; then
    unmeasured pull-request "gh pr view answered without a base branch: the bypass lists this check reads are the base's, and a base this check cannot name is one whose rules it cannot read. What it printed is in $SCRATCH/pr.out"
    return 0
  fi
  fact pull-request PASS "the pull request ${SLUG}#${PR} is ${PR_STATE} at head ${HEAD:0:8} onto base ${BASE} (read with gh pr view): the mint below names that head, and the bypass lists it reads are ${BASE}'s"
}

# step_app_installation: the owner's own per-repository step (D2048), read rather
# than granted. GitHub answers the App's installation and the permissions it has
# been granted on it, and the three this check requires are the mint's own set
# plus the read the gate makes — never the App's key, which this check does not
# read at all.
step_app_installation() {
  if [ -z "$SLUG" ]; then
    unmeasured app-installation "there is no repository whose App installation could be read: the row above says why"
    return 0
  fi
  say "reading the App's installations: gh api /user/installations?per_page=100"
  run_bounded "$CLI_TIMEOUT" installations "$SANDBOX" gh api "/user/installations?per_page=100"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured app-installation "gh api /user/installations did not answer (exit ${BOUNDED_STATUS}): the App's granted permissions are read from its own installation, and that read is what this row is about. What gh printed is in $SCRATCH/installations.err"
    return 0
  fi
  if ! read_json installations "$SCRATCH/installations.out" "$SCRATCH/installations.txt"; then
    unmeasured app-installation "gh api /user/installations printed a document this check could not read: what it printed is in $SCRATCH/installations.out"
    return 0
  fi
  local count
  count=$(field "$SCRATCH/installations.txt" count)
  if [ "${count:-0}" -ge 100 ]; then
    fact app-installation FAIL "the first page of /user/installations is full (${count} installations) and a page cannot be told from a truncated list, so this check will not pick the App's installation out of a list whose end it cannot see (the rule the mint's own gate reads a ruleset page by, G14). Run this as a token that sees fewer installations, or pass --app-id with the App's own id: the permission rows below read that installation by its repositories, so an App named here is still checked against the repository you named"
    return 0
  fi
  if [ "${count:-0}" -eq 0 ]; then
    fact app-installation FAIL "the token your gh carries sees no App installation at all: the App your installation's runs use is installed on the account, and D2048's grant is made on its own page. Check that gh is authenticated as the person who installed it (\`gh auth status\`)"
    return 0
  fi
  local tag id app slug sel perms unreadable="" full="" found=no
  while IFS=$'\t' read -r tag id app slug sel perms; do
    [ "$tag" = inst ] || continue
    if [ -n "$APP_ID" ] && [ "$app" != "$APP_ID" ]; then continue; fi
    say "reading installation ${id}'s repositories: gh api /user/installations/${id}/repositories"
    run_bounded "$CLI_TIMEOUT" "inst-${id}" "$SANDBOX" gh api "/user/installations/${id}/repositories?per_page=100"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      unreadable="${unreadable}${unreadable:+, }${app:-installation ${id}}"
      continue
    fi
    if ! read_json repos "$SCRATCH/inst-${id}.out" "$SCRATCH/inst-${id}.txt"; then
      unreadable="${unreadable}${unreadable:+, }${app:-installation ${id}}"
      continue
    fi
    if [ "$(field "$SCRATCH/inst-${id}.txt" count)" -ge 100 ]; then
      full="${full}${full:+, }${app:-installation ${id}}"
      continue
    fi
    if awk -F'\t' -v s="$SLUG" '$1 == "repo" && $2 == s { seen = 1 } END { exit(seen ? 0 : 1) }' "$SCRATCH/inst-${id}.txt"; then
      APP_INSTALLATION=$id
      APP_ID=$app
      APP_SLUG=$slug
      APP_PERMS=$perms
      found=yes
      break
    fi
  done <"$SCRATCH/installations.txt"
  if [ "$found" != yes ]; then
    if [ -n "$unreadable" ] || [ -n "$full" ]; then
      unmeasured app-installation "no installation the token sees lists ${SLUG} and the question is not settled: ${unreadable:+the repositories of ${unreadable} could not be read; }${full:+the repository page of ${full} came back full, and a full page cannot be told from a truncated one}. This check will not report an App as installed on a repository out of a list it did not finish reading"
      return 0
    fi
    fact app-installation FAIL "no App installation the token your gh carries sees covers ${SLUG} (it saw ${count}: $(awk -F'\t' '$1 == "inst" { printf "%s ", $3 }' "$SCRATCH/installations.txt")): the merge mint is signed as the App installed on this repository, so there is nothing to mint from. The owner installs it and selects the sandbox repository (github.com/settings/apps — the App's own page — then Install, or Configure on the account's installations page), and grants the permissions the row below names (D2048)"
    return 0
  fi
  local contents pullreq admin missing=""
  contents=$(perm_level "$APP_PERMS" contents)
  pullreq=$(perm_level "$APP_PERMS" pull_requests)
  admin=$(perm_level "$APP_PERMS" administration)
  case "$contents" in write | admin) ;; *) missing="${missing} contents=${contents:-none}" ;; esac
  case "$pullreq" in write | admin) ;; *) missing="${missing} pull_requests=${pullreq:-none}" ;; esac
  case "$admin" in read | write | admin) ;; *) missing="${missing} administration=${admin:-none}" ;; esac
  if [ -n "$missing" ]; then
    fact app-installation FAIL "the App (id ${APP_ID}, slug ${APP_SLUG:-unknown}) IS installed on ${SLUG} (installation ${APP_INSTALLATION}) and its granted permissions are missing${missing}. Three are required and each for its own reason: contents write and pull requests write are the set the mint hands out (internal/credsvc's mergeTokenPermissions, the narrowest set GitHub's merge endpoint accepts), and administration read is what the gate reads the base's rulesets and its branch protection under (mergeGateReadPermissions) — GitHub answers those two reads under its Administration permission, so an App without it fails the gate closed and mints nothing at all. The owner grants an App's repository permissions per repository (D2048): github.com/settings/apps/${APP_SLUG:-<app>}/permissions, then accept the change on that installation's own page for ${SLUG}. GitHub also refuses an installation token that asks for a permission the App does not hold, which is the other way this row shows up"
    return 0
  fi
  fact app-installation PASS "the App (id ${APP_ID}, slug ${APP_SLUG:-unknown}) is installed on ${SLUG} (installation ${APP_INSTALLATION}) and holds contents: ${contents}, pull_requests: ${pullreq}, administration: ${admin} — the merge set the mint hands out and the read its gate makes of the base's rules. Read with gh api /user/installations as YOU, from GitHub's own answer about the App: this check reads no App key at all"
}

# step_bypass: the App is on neither of the two lists GitHub lets it be a bypass
# actor on (G14) — the rulesets that cover the base, each read again by id, and
# the base's classic branch protection, whose list is a second one. Both reads
# must answer: an unreadable list is not an absent one, and a page of rulesets
# that comes back full cannot be told from a truncated list.
step_bypass() {
  if [ -z "$SLUG" ] || [ -z "$BASE" ]; then
    unmeasured bypass "there is no repository and base whose rules could be read: the rows above say why"
    return 0
  fi
  if [ -z "$APP_ID" ]; then
    unmeasured bypass "the App's own ID could not be read, so whether it is an actor on the base's bypass lists cannot be compared: the app-installation row says why. Pass --app-id with the App's own id to read the lists anyway"
    return 0
  fi
  say "reading the rulesets covering ${BASE}: gh api /repos/${SLUG}/rulesets"
  run_bounded "$CLI_TIMEOUT" rulesets "$SANDBOX" gh api "/repos/${SLUG}/rulesets?per_page=100"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured bypass "gh api /repos/${SLUG}/rulesets did not answer (exit ${BOUNDED_STATUS}): the mint's gate reads exactly this list and refuses when it cannot (G14), so this row cannot say the App is off a list it did not read. What gh printed is in $SCRATCH/rulesets.err"
    return 0
  fi
  if ! read_json rulesets "$SCRATCH/rulesets.out" "$SCRATCH/rulesets.txt"; then
    unmeasured bypass "gh api /repos/${SLUG}/rulesets printed a document this check could not read: what it printed is in $SCRATCH/rulesets.out"
    return 0
  fi
  local rs_count
  rs_count=$(field "$SCRATCH/rulesets.txt" count)
  if [ "${rs_count:-0}" -ge 100 ]; then
    fact bypass FAIL "the rulesets list of ${SLUG} came back full (${rs_count}) and a full page cannot be told from a truncated one: the ruleset that names the App may be the one past it, which is the read GitHub's own list cannot settle — the mint's own gate refuses for exactly this reason (G14), so no token is minted for this base until the list fits in one page"
    return 0
  fi
  local named="" ids="" id reads_rules="yes"
  while IFS=$'\t' read -r tag id; do
    [ "$tag" = rs ] || continue
    ids="${ids}${ids:+, }${id}"
    run_bounded "$CLI_TIMEOUT" "rs-${id}" "$SANDBOX" gh api "/repos/${SLUG}/rulesets/${id}"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      reads_rules=no
      continue
    fi
    if ! read_json ruleset "$SCRATCH/rs-${id}.out" "$SCRATCH/rs-${id}.txt" "$BASE"; then
      reads_rules=no
      continue
    fi
    local target enforcement list coverage actors
    target=$(field "$SCRATCH/rs-${id}.txt" target)
    enforcement=$(field "$SCRATCH/rs-${id}.txt" enforcement)
    list=$(field "$SCRATCH/rs-${id}.txt" list)
    coverage=$(field "$SCRATCH/rs-${id}.txt" coverage)
    actors=$(field "$SCRATCH/rs-${id}.txt" actors)
    [ "$target" = branch ] || continue
    [ "$enforcement" != disabled ] || continue
    [ "$coverage" = covers ] || continue
    # A covering ruleset that carries NO bypass list at all is a hidden list, and
    # a hidden list is not one that names nobody (G14): the App's own token is
    # what sees its actors, and this read cannot.
    if [ "$list" != true ]; then
      named="${named}${named:+; }ruleset ${id} (name $(field "$SCRATCH/rs-${id}.txt" name), $(field "$SCRATCH/rs-${id}.txt" enforcement)) covers ${BASE} and its answer carries NO bypass list — a list this check cannot read is not one that names nobody"
      continue
    fi
    if printf '%s' "$actors" | tr ',' '\n' | grep -qxF "Integration:${APP_ID}"; then
      named="${named}${named:+; }ruleset ${id} (name $(field "$SCRATCH/rs-${id}.txt" name), $(field "$SCRATCH/rs-${id}.txt" enforcement)) covers ${BASE} and names the App (Integration:${APP_ID}) among its bypass actors (${actors:-none})"
    fi
  done <"$SCRATCH/rulesets.txt"
  if [ "$reads_rules" != yes ]; then
    unmeasured bypass "a ruleset of ${SLUG} that the list named could not be read again by id (what GitHub's list answers is a summary, and the bypass list is in the per-id answer alone). The mint's gate refuses on exactly this read for exactly this reason (G14), and this row will not call a list the App may not be on one it did not read"
    return 0
  fi
  say "reading the base's classic branch protection: gh api /repos/${SLUG}/branches/${BASE}/protection"
  run_bounded "$CLI_TIMEOUT" protection-status "$SANDBOX" gh api --include --silent \
    "/repos/${SLUG}/branches/${BASE}/protection"
  local code=""
  if read_json http "$SCRATCH/protection-status.out" "$SCRATCH/protection-status.txt"; then
    code=$(field "$SCRATCH/protection-status.txt" code)
  fi
  local classic="the base carries no classic branch protection, which is the ordinary answer for a repository protected by rulesets alone (GH answered 404, and the mint's gate reads that 404 as 'no protection to be named on')"
  if [ "$code" = 200 ]; then
    run_bounded "$CLI_TIMEOUT" protection "$SANDBOX" gh api "/repos/${SLUG}/branches/${BASE}/protection"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] ||
      ! read_json protection "$SCRATCH/protection.out" "$SCRATCH/protection.txt"; then
      unmeasured bypass "the base's classic branch protection answered 200 and this check could not read the object (exit ${BOUNDED_STATUS}): the mint's gate refuses a bypass list it cannot read (G14), and this row will not call a list absent that it did not read. What gh printed is in $SCRATCH/protection.err"
      return 0
    fi
    local apps
    apps=$(field "$SCRATCH/protection.txt" apps)
    if printf '%s' "$apps" | tr ',' '\n' | grep -qxF "$APP_ID"; then
      named="${named}${named:+; }the classic branch protection of ${BASE} names the App (app id ${APP_ID}) among its bypass actors (${apps})"
    fi
    classic="the base's classic branch protection was read (required_pull_request_reviews $(field "$SCRATCH/protection.txt" required_reviews)): its bypass list is $(if [ -n "$apps" ]; then printf 'apps %s' "$apps"; else printf 'empty, so it names nobody'; fi)"
  elif [ "$code" = 404 ]; then
    : # the classic string above already says so
  elif [ -n "$code" ]; then
    fact bypass FAIL "the base's classic branch protection of ${BASE} answered HTTP ${code}: the mint's gate reads that object and refuses on anything but the object or the 404 that means this branch carries no classic protection (G14), so no merge token is minted for this base until the read answers. The App's token is what reads it (administration read), and a 403 here is the App's own permission row's finding: $(err_text "$SCRATCH/protection-status.err")"
    return 0
  else
    unmeasured bypass "gh api --include --silent did not answer a status for the base's classic branch protection (exit ${BOUNDED_STATUS}): the mint's gate refuses a bypass list it cannot read, and this row will not report one it did not read either. What gh printed is in $SCRATCH/protection-status.err"
    return 0
  fi
  if [ -n "$named" ]; then
    fact bypass FAIL "the App may merge past ${BASE} of ${SLUG}: ${named}. Both places GitHub names a bypass actor are read here — the rulesets that cover the branch and the classic branch protection of it — and the mint's gate reads both and refuses when the App is on either, or when either list cannot be read (G14). Nothing is minted for this base until the App is off that list: removing it is your own step (D2048) on the App's or the ruleset's own page, exactly as granting the permission is"
    return 0
  fi
  fact bypass PASS "App ${APP_ID} is on neither bypass list of ${BASE} in ${SLUG}: the ${rs_count:-0} ruleset(s) covering it (${ids:-none}) name it on none of their bypass lists, and ${classic}. What GitHub answers as current_user_can_bypass is deliberately NOT read as the App's answer here: it is answered for the identity that ASKED, which for this row is your own token, and the mint's own gate asks it with the App's token — the mint row below is where that answer is measured"
}

# step_service: the credentials service's own URL and the names of the
# environment variables its revision carries. The URL is read from Cloud Run
# rather than from a config, so the calls below go where this installation's
# service is, and the NAMES (never the values) are what the daemon row reads the
# class list out of.
step_service() {
  if [ -z "$PROJECT" ]; then
    unmeasured service "there is no installation whose service could be read: the row above says why"
    return 0
  fi
  if [ -n "$SERVICE_URL" ]; then
    fact service PASS "${SERVICE_URL} — the URL the merge mint answers on, given with CHECK542_SERVICE_URL"
    return 0
  fi
  say "reading the service: gcloud run services describe ${SERVICE_NAME} --region ${REGION_USED}"
  run_bounded "$GCP_TIMEOUT" service "$SANDBOX" gcloud run services describe "$SERVICE_NAME" \
    --project="$PROJECT" --region="$REGION_USED" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact service FAIL "gcloud could not read the credentials service ${SERVICE_NAME} in ${PROJECT}/${REGION_USED} (exit ${BOUNDED_STATUS}): fugaro update deploys that service for every installation and it is the holder of the App key the merge mint is signed with, so nothing below can be asked. What gcloud printed is in $SCRATCH/service.err"
    return 0
  fi
  if ! read_json service "$SCRATCH/service.out" "$SCRATCH/service.txt"; then
    unmeasured service "the service's describe is not a document this check could read: what gcloud printed is in $SCRATCH/service.out"
    return 0
  fi
  SERVICE_URL=$(field "$SCRATCH/service.txt" url)
  if [ -z "$SERVICE_URL" ]; then
    unmeasured service "the credentials service ${SERVICE_NAME} answered without a status.url: the mint's calls have no destination and the daemon's audience check has nothing to compare against. What gcloud printed is in $SCRATCH/service.out"
    return 0
  fi
  fact service PASS "${SERVICE_URL} — the credentials service ${SERVICE_NAME} of ${PROJECT}, read from Cloud Run rather than from a config, with $(field "$SCRATCH/service.txt" count) environment entr(ies) whose NAMES this check reads and whose values it never reads at all"
}

# step_daemon_identity: the half of this check that cannot be granted from this
# terminal. The merge mint's caller class is a list of service accounts the
# installation names (credsvc's AuthConfig.OrchestratorAccounts), it is read from
# an environment variable of the service, and the account on it needs the grant
# that lets it invoke the service. On the release this check was written against
# none of the three exists, and the row says so by name instead of leaving the
# mint's refusal unexplained.
step_daemon_identity() {
  if [ -z "$PROJECT" ]; then
    unmeasured daemon-identity "there is no installation whose daemon could be read: the row above says why"
    return 0
  fi
  say "reading the daemon's own account: gcloud iam service-accounts describe ${DAEMON}"
  run_bounded "$GCP_TIMEOUT" daemon "$SANDBOX" gcloud iam service-accounts describe "$DAEMON" \
    --project="$PROJECT" --format=json
  # The two reads below each keep their own exit status: the row is built after
  # both of them, and a bound's global would be the second call's answer read as
  # the first one's.
  DAEMON_STATUS=$BOUNDED_STATUS
  DAEMON_READ=$(err_text "$SCRATCH/daemon.err")
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ]; then
    DAEMON_EXISTS=yes
    DAEMON_READ=""
  fi
  # The class list is read as an environment variable's NAME: no value of any
  # entry is printed, read into a row or compared, because a value may be a
  # credential and this check reads none.
  if [ -s "$SCRATCH/service.txt" ]; then
    DAEMON_CLASS_ENV=$(awk -F'\t' '$1 == "env" && tolower($2) ~ /orchestrator/ && $3 == "set" { printf "%s ", $2 }' \
      "$SCRATCH/service.txt" 2>/dev/null || true)
  fi
  if [ -n "$SERVICE_URL" ]; then
    say "reading the service's invoker policy: gcloud run services get-iam-policy ${SERVICE_NAME}"
    run_bounded "$GCP_TIMEOUT" iam "$SANDBOX" gcloud run services get-iam-policy "$SERVICE_NAME" \
      --project="$PROJECT" --region="$REGION_USED" --format=json
    IAM_STATUS=$BOUNDED_STATUS
    if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] &&
      read_json policy "$SCRATCH/iam.out" "$SCRATCH/iam.txt"; then
      DAEMON_INVOKER=$(awk -F'\t' -v who="serviceAccount:${DAEMON}" '$1 == "invoker" && $2 == who { print $2; exit }' "$SCRATCH/iam.txt" 2>/dev/null || true)
      if [ -z "$DAEMON_INVOKER" ] && grep -qxF $'invoker\tallUsers' "$SCRATCH/iam.txt" 2>/dev/null; then
        DAEMON_INVOKER="every caller (allUsers)"
      fi
    else
      IAM_READ=$(err_text "$SCRATCH/iam.err")
    fi
  fi
  if [ "$DAEMON_EXISTS" = yes ] && [ -n "$DAEMON_CLASS_ENV" ] && [ -n "$DAEMON_INVOKER" ]; then
    fact daemon-identity PASS "the daemon's identity is wired on this installation: the account ${DAEMON} exists, the service's revision names a class by ${DAEMON_CLASS_ENV}, and that account may invoke ${SERVICE_NAME} (${DAEMON_INVOKER}): the merge mint is the caller class credsvc's VerifyOrchestrator admits, and the mint row below asks it as that account"
    return 0
  fi
  local missing=""
  if [ "$DAEMON_EXISTS" != yes ]; then
    missing="${missing}the account ${DAEMON} could not be read (gcloud iam service-accounts describe exited ${DAEMON_STATUS}: ${DAEMON_READ:-nothing}) — internal/infra's OrchestratorAccountID is 'fugaro-orch' and the installation module creates that account for an installation that opted into an orchestrator, so a NOT_FOUND here is that half missing and any other error is your own account's permission to read it; "
  fi
  if [ -z "$DAEMON_CLASS_ENV" ]; then
    missing="${missing}no environment entry of the service's revision names an orchestrator class at all (internal/credsvc's AuthConfig.OrchestratorAccounts is the list the merge mint's caller is matched against, and the environment set internal/cli/credentials_serve.go reads — FUGARO_PROJECT, FUGARO_JOB_ACCOUNTS and the rest — carries no name for it, nor does deploy/terraform/gcp/modules/installation/credentials.tf publish one); "
  fi
  if [ -z "$DAEMON_INVOKER" ]; then
    if [ -n "$IAM_READ" ]; then
      missing="${missing}the invoker policy of ${SERVICE_NAME} could not be read (gcloud run services get-iam-policy exited ${IAM_STATUS}: ${IAM_READ}), so whether ${DAEMON} may invoke the service is not settled either way; "
    else
      missing="${missing}${DAEMON} holds no roles/run.invoker on ${SERVICE_NAME} in ${PROJECT} (the caller's grant is Terraform, and it is not carried yet); "
    fi
  fi
  fact daemon-identity FAIL "the merge mint has no caller on this installation: ${missing}this is not a defect in the route — SECURITY.md says it in one line ('the route ships inert on this tree'): the grant that would let a daemon's account invoke the service, and the variable that names it, are Terraform and are not carried yet, and the daemon builds no label seams either while internal/cli/orchestrator_labels.go's orchestratorLabelIdentity is nil (F19, O2). The route exists in the binary and refuses every caller with the class's own refusal until the installation names its daemon. An installation converges by becoming that one; until it does, the mint rows below can only report that refusal, and the permission rows above are the half of D2048 you can settle today"
}

# mint_token ACCOUNT KEY: put an ID token for ACCOUNT, minted by impersonation,
# straight into a mode-0600 curl config file. It never enters a shell variable, a
# command line, the block or a log. An EMPTY account mints the owner's own
# ambient token instead, which is the credential a coding-agent session on this
# machine holds and the one the service must refuse.
mint_token() { # mint_token ACCOUNT KEY
  local account=$1 key=$2
  MINT_TOKEN_ERROR=""
  if [ -n "$account" ]; then
    run_bounded "$TOKEN_TIMEOUT" "token-${key}" "$SANDBOX" gcloud auth print-identity-token \
      --impersonate-service-account="$account" --audiences="$SERVICE_URL" --include-email
  else
    # No --audiences: gcloud refuses an audience for a user credential, and the
    # token a person's terminal mints is exactly the one without one.
    run_bounded "$TOKEN_TIMEOUT" "token-${key}" "$SANDBOX" gcloud auth print-identity-token
  fi
  local ok=yes
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    ok=no
  fi
  if [ "$ok" = yes ] && [ ! -s "$SCRATCH/token-${key}.out" ]; then
    ok=no
    MINT_TOKEN_ERROR="an empty token"
  fi
  if [ "$ok" = no ]; then
    MINT_TOKEN_ERROR=${MINT_TOKEN_ERROR:-$(err_text "$SCRATCH/token-${key}.err")}
    MINT_TOKEN_ERROR=${MINT_TOKEN_ERROR:-nothing}
  fi
  if [ "$ok" = yes ]; then
    {
      printf 'header = "Authorization: Bearer '
      tr -d '\r\n' <"$SCRATCH/token-${key}.out"
      printf '"\n'
    } >"$SCRATCH/token-${key}.curl"
    chmod 600 "$SCRATCH/token-${key}.curl"
  fi
  # Both files go, whatever happened: a gcloud that printed a token and then
  # failed, or a token this check did not use, would otherwise sit in the scratch
  # directory with the reason it was not used read out of it — the one file a
  # credential could be copied into by anything that reads the scratch later.
  rm -f "$SCRATCH/token-${key}.out" "$SCRATCH/token-${key}.err"
  [ "$ok" = yes ]
}

# post_mint KEY TOKENKEY [CURL-ARGS...]: one POST of the merge mint's own body to
# the route, with the Authorization header of the config file TOKENKEY names. The
# body carries the four fields the contract requires and no fifth: the route's
# decoder is strict (Convention 6), which is what makes "no caller can widen this
# mint" a property of the route.
post_mint() { # post_mint KEY TOKENKEY [CURL-ARGS...]
  local key=$1 tokenkey=$2
  shift 2
  local body="$SCRATCH/post-${key}.json"
  printf '{"repository":"%s","number":%s,"head_sha":"%s","app_id":"%s"}\n' \
    "$SLUG" "$PR" "$MINT_HEAD" "$APP_ID" >"$body"
  # The instant the call is made, taken before it: the audit row binds a line to
  # this run by the line's OWN instant, so it has to know when each call went
  # out. Taken here rather than after the call returns, because the service
  # writes the line while it is serving the call.
  MINT_STARTED_AT=$(date -u +%s 2>/dev/null || true)
  run_bounded "$TIMEOUT" "$key" "$SANDBOX" curl -sS -o "$SCRATCH/$key.body" -w '%{http_code}' \
    --config "$SCRATCH/token-${tokenkey}.curl" -H 'Content-Type: application/json' \
    --data @"$body" "$@" "${SERVICE_URL}${MERGE_MINT_PATH}"
}

# read_mint_answer KEY: read one mint answer. The body of this route's answer is
# the ONE answer of this check that carries a credential, so it is never printed,
# never quoted and never copied: the reader reports whether a token is present,
# when it expires and what the refusal said, and the row is built from those
# fields alone. MINT_KIND is `mint` (a token came back), `refusal` (the service
# refused the call), `silent` (no body at all) or `other` (a document this check
# does not know — reported as such, never echoed).
read_mint_answer() { # read_mint_answer KEY
  local key=$1
  MINT_KIND=other
  MINT_ERROR=""
  # curl's own `-w '%{http_code}'` writes the status and nothing else, and the
  # digits are taken out of it anyway: a status read out of a line a body shares
  # would be a status this check made up.
  MINT_STATUS=$(tr -dc '0-9' <"$SCRATCH/$key.out" 2>/dev/null | head -c 3 || true)
  MINT_EXPIRES=""
  MINT_REPO=""
  if [ ! -s "$SCRATCH/$key.body" ]; then
    MINT_KIND=silent
    return 0
  fi
  if ! read_json mintanswer "$SCRATCH/$key.body" "$SCRATCH/$key.txt"; then
    MINT_KIND=other
    return 0
  fi
  MINT_EXPIRES=$(field "$SCRATCH/$key.txt" expires_at)
  MINT_REPO=$(field "$SCRATCH/$key.txt" repository)
  MINT_ERROR=$(field "$SCRATCH/$key.txt" error)
  if [ "$(field "$SCRATCH/$key.txt" has_token)" = true ]; then
    MINT_KIND=mint
    return 0
  fi
  if [ -n "$MINT_ERROR" ]; then
    MINT_KIND=refusal
    return 0
  fi
  MINT_KIND=other
  return 0
}

# mint_answer_text KEY: the answer in one clause, for a row. It never carries a
# byte of the body: a mint reads as the token's presence and its expiry, a
# refusal as the service's own two fields.
mint_answer_text() { # mint_answer_text KEY
  local key=$1 text
  case "$MINT_KIND" in
  mint)
    text="a merge token came back (expires_at ${MINT_EXPIRES:-unknown}, repository ${MINT_REPO:-unnamed}, HTTP ${MINT_STATUS:-?})"
    ;;
  refusal)
    text="${MINT_ERROR}: $(field "$SCRATCH/$key.txt" message) (HTTP ${MINT_STATUS:-?})"
    ;;
  silent)
    text="no answer at all (curl exited ${BOUNDED_STATUS}, HTTP ${MINT_STATUS:-none})"
    ;;
  *)
    text="a document this check does not know (HTTP ${MINT_STATUS:-?}), NOT printed: a merge mint's answer is the one answer of this route that carries a credential"
    ;;
  esac
  printf '%s' "$text"
}

# record_mint KEY: one line per mint call this check made, for the audit row: the
# key, the STATUS the service's own line would carry for it, whether the CALLER
# was admitted at all, and the instant the call went out (post_mint's own, in
# seconds). The instant is what makes the row this run's: a line is looked for at
# or after the moment the call was made, so another caller's line with the same
# status — the daemon minting on its own, hours or minutes earlier — can never
# stand in for a line this run's call never got. The status is read off the
# answer rather than the HTTP code, because the audit line's status field is
# the contract's own error type (ok, or unverified_run_identity,
# request_refused, state_unavailable)
# and a refusal answered BEFORE the caller is verified — the class's own — is the
# one refusal with no line to look for: there is no identity for it to name.
#
# An answer that was not the route's at all (Cloud Run's front end, a body this
# check could not read) is recorded as unchecked: this row may not demand a line
# for a call that may never have reached the service.
record_mint() { # record_mint KEY
  local key=$1 status admitted
  case "$MINT_KIND" in
  mint) status=ok; admitted=yes ;;
  refusal)
    status=$MINT_ERROR
    case "$MINT_ERROR" in
    unverified_job_identity | "") admitted=no ;;
    *) admitted=yes ;;
    esac
    ;;
  *)
    status=-
    admitted=no
    ;;
  esac
  printf '%s\t%s\t%s\t%s\n' "$key" "$status" "$admitted" "$MINT_STARTED_AT" >>"$MINT_CALLS"
  MINT_ANSWERED=$((MINT_ANSWERED + 1))
}

# mint_premise: the four things the call's own body needs, or the row that says
# which of them is missing. Answers 0 when the call can be made.
mint_premise() { # mint_premise KEY
  local key=$1 missing=""
  [ -n "$SERVICE_URL" ] || missing="the service's URL"
  [ -n "$SLUG" ] || missing="${missing}${missing:+, }the repository"
  [ -n "$PR" ] || missing="${missing}${missing:+, }the pull request number"
  [ -n "$MINT_HEAD" ] || missing="${missing}${missing:+, }the head the call is bound to"
  [ -n "$APP_ID" ] || missing="${missing}${missing:+, }the App ID"
  if [ -n "$missing" ]; then
    unmeasured "$key" "this call needs ${missing} and it could not be read: the rows above say why. The mint's body is four required fields and a call that named fewer would be refused for its shape rather than for what this row measures"
    return 1
  fi
  return 0
}

# step_mint: the mint itself, asked as the identity the daemon holds.
step_mint() {
  MINT_HEAD="$HEAD"
  if ! mint_premise mint; then return 0; fi
  say "asking the service for a merge token at head ${HEAD:0:8}: POST ${SERVICE_URL}${MERGE_MINT_PATH} as ${DAEMON}"
  if ! mint_token "$DAEMON" daemon; then
    fact mint FAIL "gcloud could not mint an ID token for ${DAEMON} by impersonation (exit ${BOUNDED_STATUS}: ${MINT_TOKEN_ERROR}), so the mint could not be asked as the daemon at all. The class the service admits is that account, and the token has to carry ${SERVICE_URL} as its audience — a plain \`gcloud auth print-identity-token\` cannot mint for a custom audience, which is why the impersonation is the only way to present this caller. The daemon-identity row above says whether the account and its grant exist; if they do and this still fails, your own account needs roles/iam.serviceAccountTokenCreator on it"
    return 0
  fi
  post_mint mint daemon
  read_mint_answer mint
  record_mint mint
  case "$MINT_KIND" in
  mint)
    fact mint PASS "the service minted an installation token for ${SLUG}#${PR} at head ${HEAD:0:8} onto base ${BASE} (expires_at ${MINT_EXPIRES:-unknown}, repository ${MINT_REPO:-unnamed}), asked as ${DAEMON} with a Google ID token for the service's own URL: the gate behind it read the pull request's CURRENT head (G2) and both of the base's bypass lists (G14) before it minted. The token itself is in this run's scratch directory, was never printed, and is never used: this check merges nothing. Its permission set is not measured here — internal/credsvc's own test holds it (TestMergeMintNeverReturnsABroaderToken) and this check would have to use the token to read it"
    ;;
  refusal)
    if [ "$MINT_ERROR" = unverified_job_identity ]; then
      fact mint FAIL "the service refused the daemon's own token with ${MINT_ERROR} (HTTP ${MINT_STATUS}): the route admits one class and this installation names none of it, so the caller is refused before anything is read (no audit line is written for a refusal taken before an identity exists — correctly). This is the state SECURITY.md calls 'the route ships inert on this tree' and the daemon-identity row above names its three missing halves; the refusal is the route behaving as documented, and the row is a finding about this installation rather than about the code"
      return 0
    fi
    fact mint FAIL "the service refused the mint of ${SLUG}#${PR} at head ${HEAD:0:8}: $(mint_answer_text mint). The caller WAS admitted (${MINT_ERROR} is answered after the class check), so this is a refusal of the call or of the state behind it: a request_refused names the body or the repository it resolved, state_unavailable names the rate (ten mints per repository per hour, per instance), the bundle, the App key, the operator's stop switch, or the gate's own refusal — a gate refusal is where a moved head or a bypass list shows up as this same status, and it is logged (credsvc: the merge mint's gate refused) with the class of the failure"
    ;;
  *)
    fact mint FAIL "the merge mint answered $(mint_answer_text mint) for ${SLUG}#${PR} at head ${HEAD:0:8}: the contract's answer is either a token with its expiry or a typed refusal, and this is neither. In production, an empty or unparseable body here is Cloud Run's own front end answering before the container ran (${DAEMON} holding no roles/run.invoker on ${SERVICE_NAME} is the likeliest cause, and the daemon-identity row reads that grant)"
    ;;
  esac
}

# step_mint_moved_head: the binding that makes the mint worth having (G2). The
# call names a head the pull request is NOT at, and nothing may be minted.
step_mint_moved_head() {
  if [ -z "$HEAD" ]; then
    unmeasured mint-moved-head "there is no head to move: the pull-request row says why"
    return 0
  fi
  MINT_HEAD=$(moved_head "$HEAD")
  if ! mint_premise mint-moved-head; then return 0; fi
  if [ ! -s "$SCRATCH/token-daemon.curl" ]; then
    unmeasured mint-moved-head "no daemon token was minted (the mint row above says why), so the moved-head call cannot be made as the caller the answer has to be about"
    return 0
  fi
  say "asking the service for a merge token at a head the pull request is not at: ${MINT_HEAD:0:8}"
  post_mint mint-moved-head daemon
  read_mint_answer mint-moved-head
  record_mint mint-moved-head
  case "$MINT_KIND:$MINT_ERROR" in
  refusal:request_refused)
    fact mint-moved-head PASS "the mint was refused with request_refused at head ${MINT_HEAD:0:8} while the pull request is at ${HEAD:0:8} ($(field "$SCRATCH/mint-moved-head.txt" message)): the service read the pull request's CURRENT head and refused to mint for a head it is not at, which is the binding the whole route exists for (G2) — a token minted here would be a token for a merge nobody reviewed"
    ;;
  mint:*)
    fact mint-moved-head FAIL "the service MINTED A TOKEN for ${SLUG}#${PR} at head ${MINT_HEAD:0:8} while the pull request is at ${HEAD:0:8} (HTTP ${MINT_STATUS}): this is the one answer this row must never see, and it is a finding about the gate rather than about this installation. The token was not printed and was not used, and this check removes it with the scratch directory; revoke it at GitHub (the installation's own page) if you want it gone before its expiry"
    ;;
  refusal:unverified_job_identity)
    fact mint-moved-head FAIL "the route refused the moved-head call with ${MINT_ERROR} before it read anything (HTTP ${MINT_STATUS}): the class refusal comes before the head is compared, so the head binding cannot be measured on this installation at all — this is the state the daemon-identity row names, and the row is a finding about the installation rather than about the gate (the mint's own unit test, TestMergeMintBoundToHeadSHA, is where this binding is held until the daemon is named)"
    ;;
  *)
    fact mint-moved-head FAIL "the moved-head call answered $(mint_answer_text mint-moved-head) and not the contract's refusal: a head the pull request is not at is refused with request_refused (HTTP 400) and nothing else, and any other answer is a reading this row cannot call a pass"
    ;;
  esac
}

# step_mint_person_token: the credential a coding-agent session on this machine
# holds — the operator's own ambient ID token, and the shape a run's identity
# token has too: neither is a Google ID token for this service's URL, and the
# audience check refuses them before any account list is read. It is the one
# refusal of this check that holds on every installation, wired or not.
step_mint_person_token() {
  MINT_HEAD="$HEAD"
  if ! mint_premise mint-person-token; then return 0; fi
  say "asking the service with your own ambient credential (no impersonation)"
  if ! mint_token "" person; then
    unmeasured mint-person-token "gcloud could not mint your own ID token (exit ${BOUNDED_STATUS}: ${MINT_TOKEN_ERROR}): this row is about the credential a coding-agent session on this machine holds, and it cannot be measured without one. Run \`gcloud auth login\` if this account's credentials have expired"
    return 0
  fi
  post_mint mint-person-token person
  read_mint_answer mint-person-token
  record_mint mint-person-token
  case "$MINT_KIND:$MINT_ERROR" in
  refusal:unverified_job_identity)
    fact mint-person-token PASS "your own ambient ID token was refused with unverified_job_identity (HTTP ${MINT_STATUS}) and nothing was minted: the class is the token's, and a person's credential cannot carry ${SERVICE_URL} as its audience (a plain \`gcloud auth print-identity-token\` mints for gcloud's own client id), so the refusal is answered before any account list is read — which is why this row holds whatever the daemon's own identity is wired to. A coding-agent session on this machine holds exactly this credential, and it buys no merge from the service"
    ;;
  refusal:*)
    fact mint-person-token FAIL "your own ambient credential was refused with ${MINT_ERROR} (HTTP ${MINT_STATUS}), which is not the class's own refusal: $(mint_answer_text mint-person-token). The route answers a caller it will not verify with unverified_job_identity and nothing else, and any other typed refusal is a reading about a call that got further than a person's credential may"
    ;;
  mint:*)
    fact mint-person-token FAIL "the service MINTED A MERGE TOKEN for ${SLUG}#${PR} to your own ambient credential (HTTP ${MINT_STATUS}): this is the finding this row exists for — the credential a coding-agent session on this machine holds bought a merge credential from the service. The token was not printed and not used, and this check removes it with the scratch directory. Report this before anything else: the class admission (credsvc's VerifyOrchestrator and its audience check) is the whole of the barrier here"
    ;;
  # The subject carries the refusal's own name, so the pattern has to: a bare
  # `silent` never matches `silent:` and this whole branch — the one answer that
  # is a PASS — would fall through to the row below it.
  silent:*)
    if [ "$MINT_STATUS" = 401 ] || [ "$MINT_STATUS" = 403 ]; then
      fact mint-person-token PASS "the call was refused at Cloud Run's own front end (HTTP ${MINT_STATUS}, no body from the container): your account holds no roles/run.invoker on ${SERVICE_NAME}, so the call never reached the route and nothing was minted. That is one step before the refusal this row is about — the class's own answer could not be measured here — and it is a stronger one: a credential without the invoker role cannot even ask"
      return 0
    fi
    fact mint-person-token FAIL "the call with your own ambient credential answered no body at all (curl exited ${BOUNDED_STATUS}, HTTP ${MINT_STATUS:-none}): neither a mint, nor the class's refusal, nor the front end's. What curl said is in $SCRATCH/mint-person-token.err"
    ;;
  *)
    fact mint-person-token FAIL "the call with your own ambient credential answered $(mint_answer_text mint-person-token): the route answers a caller it will not verify with unverified_job_identity and nothing else, and this is not that answer"
    ;;
  esac
}

# step_mint_run_identity: the plan's third refusal — a call that presents a run.
# The route takes no run identity at all (it mints for a daemon that holds none),
# so a caller that is admitted as the daemon and presents one is refused BY NAME.
step_mint_run_identity() {
  MINT_HEAD="$HEAD"
  if ! mint_premise mint-run-identity; then return 0; fi
  if [ ! -s "$SCRATCH/token-daemon.curl" ]; then
    unmeasured mint-run-identity "no daemon token was minted (the mint row above says why), so a call presenting a run identity cannot be made as a caller the route would otherwise admit"
    return 0
  fi
  say "asking the service as the daemon, presenting a run identity (${RUN_ID})"
  post_mint mint-run-identity daemon -H "X-Fugaro-Run-Identity: ${RUN_ID}"
  read_mint_answer mint-run-identity
  record_mint mint-run-identity
  case "$MINT_KIND:$MINT_ERROR" in
  refusal:unverified_run_identity)
    fact mint-run-identity PASS "a call admitted as the daemon and presenting a run identity was refused with unverified_run_identity (HTTP ${MINT_STATUS}) and nothing was minted: the route mints for a caller that holds no run, and a run identity on it is refused by name — the refusal the plan's third measurement is about. The header carries the shape and not a named run (any non-empty value is refused: the route checks that the identity is absent, never what it says), so this row does not depend on a real run of yours"
    ;;
  refusal:unverified_job_identity)
    fact mint-run-identity FAIL "the call presenting a run identity was refused with ${MINT_ERROR} before the run identity was looked at (HTTP ${MINT_STATUS}): the class admission comes first, so the by-name refusal of a run is not reachable on this installation at all — the daemon-identity row names the missing half, and the route's own unit test (TestMergeMintRefusesRunCaller) is where this refusal is held until the daemon is named"
    ;;
  refusal:*)
    fact mint-run-identity FAIL "a call presenting a run identity was refused with ${MINT_ERROR} (HTTP ${MINT_STATUS}) and not with unverified_run_identity: $(mint_answer_text mint-run-identity). The route refuses a run identity by NAME (it mints for a caller that holds no run), and any other typed refusal means the call was refused for something else first"
    ;;
  mint:*)
    fact mint-run-identity FAIL "the service MINTED a merge token to a call that presented a run identity (HTTP ${MINT_STATUS}): a run's own token is the credential the merge mint exists to keep out of the merge, and a mint answered here is the finding this row is for. The token was not printed and not used, and this check removes it with the scratch directory"
    ;;
  *)
    fact mint-run-identity FAIL "the call presenting a run identity answered $(mint_answer_text mint-run-identity): the route refuses that shape with unverified_run_identity, and this is not that answer"
    ;;
  esac
}

# step_audit: the service's own record. Once a caller IS verified every mint and
# every refusal writes exactly one line (C1.13, D143), and a call refused before
# that writes none — there is no caller for a line to name. This row reads the
# lines of the call this run made rather than the log in general: another
# caller's mints (the daemon's own) are not this row's to judge.
step_audit() {
  if [ -z "$PROJECT" ] || [ -z "$SERVICE_URL" ]; then
    unmeasured audit "there is no service whose log could be read: the rows above say why"
    return 0
  fi
  if [ "$MINT_ANSWERED" -eq 0 ]; then
    fact audit INFO "no mint call was made (the rows above say why there was none), so there is no line of this run's to read: the service's log is exactly as this check found it"
    return 0
  fi
  say "reading the service's audit lines: gcloud logging read (purpose=merge_mint)"
  run_bounded "$GCP_TIMEOUT" audit "$SANDBOX" gcloud logging read \
    "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SERVICE_NAME}\" AND jsonPayload.message=\"credsvc: audit\" AND jsonPayload.purpose=\"merge_mint\"" \
    --project="$PROJECT" --format=json --limit=100 --freshness="$LOG_FRESHNESS" --order=desc
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured audit "gcloud logging read did not answer (exit ${BOUNDED_STATUS}): this row is about the lines the service writes for the calls this run made, and it cannot be read without the log. What gcloud printed is in $SCRATCH/audit.err"
    return 0
  fi
  if ! read_json audit "$SCRATCH/audit.out" "$SCRATCH/audit.txt"; then
    unmeasured audit "the log read is not a document this check could read: what gcloud printed is in $SCRATCH/audit.out"
    return 0
  fi
  local rows
  rows=$(field "$SCRATCH/audit.txt" count)
  if [ "${rows:-0}" -ge 100 ]; then
    unmeasured audit "the log read came back full (${rows} merge_mint line(s) in the last ${LOG_FRESHNESS}, the read's own limit) and a full page cannot be told from a truncated one: a line of this run's may be past it. Narrow the window (CHECK542_LOG_FRESHNESS=2m) and run the check again"
    return 0
  fi
  # A line for a caller the service never verified is the one thing the audit
  # rule forbids: the class refusal is answered ahead of any identity, and a line
  # naming it would be a line about a caller the service did not have.
  local wrong
  wrong=$(awk -F'\t' '$1 == "line" && $4 == "unverified_job_identity" { printf "%s ", $2 }' "$SCRATCH/audit.txt" 2>/dev/null || true)
  if [ -n "$wrong" ]; then
    fact audit FAIL "the service's log carries merge_mint line(s) with status unverified_job_identity (caller ${wrong:-unknown}) in the last ${LOG_FRESHNESS}: that status is the refusal answered BEFORE the caller is verified, so there is no identity for a line to name and none may be written — a line there means either the service audits a caller it refused (the rule this row exists to check) or its status vocabulary and its refusal order disagree. Read the lines with \`gcloud logging read\` on purpose=merge_mint and take it to the service's owner"
    return 0
  fi
  # Every call this run made that the service ADMITTED must have its one line —
  # and it has to be a line of THIS run's: the line's own instant ($9, the
  # seconds the reader made of the line's timestamp) has to be at or after the
  # instant the call went out. Matching the status alone over the whole window
  # would let another caller's line — the daemon minting on its own, half an hour
  # ago — stand in for a line this run's call never got, which is the one reading
  # this row exists to prevent.
  local key status admitted at want="" missing="" unchecked="" pre=0
  while IFS=$'\t' read -r key status admitted at; do
    [ -n "$key" ] || continue
    if [ "$status" = "-" ]; then
      unchecked="${unchecked}${unchecked:+, }${key}"
      continue
    fi
    if [ "$admitted" != yes ]; then
      pre=$((pre + 1))
      continue
    fi
    want="${want}${want:+, }${key}(${status})"
    if [ -z "$at" ]; then
      missing="${missing}${missing:+, }${key} (the instant its call went out could not be taken)"
      continue
    fi
    if ! awk -F'\t' -v s="$status" -v at="$at" -v slack="$LOG_SKEW_SECONDS" \
      '$1 == "line" && $4 == s && $9 + 0 >= at - slack { seen = 1 } END { exit(seen ? 0 : 1) }' "$SCRATCH/audit.txt"; then
      missing="${missing}${missing:+, }${status}"
    fi
  done <"$MINT_CALLS"
  # Built here rather than written into the rows as `${unchecked:+...}`: the word
  # of an expansion nested inside another is its own quoting context, where an
  # apostrophe opens a quote bash then looks for the end of.
  local unchecked_clause=""
  if [ -n "$unchecked" ]; then
    unchecked_clause=", and ${unchecked} answered something that was not the route's at all, so no line was looked for on its behalf"
  fi
  if [ -z "$want" ]; then
    if [ "$pre" -eq 0 ]; then
      fact audit INFO "not one of this run's ${MINT_ANSWERED} mint call(s) reached the route: each answered something that was not the service's own answer (${unchecked}), so there is no line of this run's to read and nothing of the rule to check here — the rows above say what each call answered, and the likeliest reading is that they were refused at Cloud Run's own front end before the container ran, which the daemon-identity row is about"
      return 0
    fi
    fact audit PASS "none of this run's ${MINT_ANSWERED} mint call(s) was admitted (each was refused with the class's own answer), so the rule this row checks holds in the direction that applies: a refusal taken before the caller is verified writes NO line, and the log carries ${rows:-0} merge_mint line(s) in the last ${LOG_FRESHNESS} — none of them with the pre-verification status${unchecked_clause}. The 'one line per mint' half is unmeasured here because no caller was admitted on this installation (the daemon-identity row names why)"
    return 0
  fi
  if [ -n "$missing" ]; then
    fact audit FAIL "the service wrote no merge_mint line of this run's with status ${missing}, at or after the instant the call went out (up to ${LOG_SKEW_SECONDS}s of clock slack), for the call(s) this run made as an ADMITTED caller (${want}): every mint and every refusal after the caller is verified writes exactly one line, and a call with no line is the one thing that rule forbids (C1.13, D143). A line of another caller's with the same status does not stand in for it: this row matches the line's own instant against the instant of the call. If the calls were made in the last minute or two, Cloud Logging's own lag is the other reading — wait and run this check again. The log carries ${rows:-0} merge_mint line(s) in the last ${LOG_FRESHNESS}, and what they say is in $SCRATCH/audit.txt"
    return 0
  fi
  fact audit PASS "the service's log carries one merge_mint line per call of this run that was admitted (${want}) and none with the pre-verification status${unchecked_clause}: each line carries its call's own status and its own instant at or after the instant that call went out (up to ${LOG_SKEW_SECONDS}s of clock slack), so it is this run's call the line is about and not another caller's line of the same status. The line names the repository, the daemon as its caller, the App key as its credential and no run and no job (the caller holds none — it is a daemon, and no run identity is accepted on this route at all), and it carries no value. Read from Cloud Logging for ${SERVICE_NAME} in ${PROJECT} over the last ${LOG_FRESHNESS}; the log holds ${rows:-0} merge_mint line(s) in that window, and the others are other callers'"
}

# moved_head SHA: the same commit shape, one character along — a head the pull
# request is not at, which is all the binding has to be asked about.
moved_head() { # moved_head SHA
  local sha=$1 last
  last=${sha: -1}
  case "$last" in
  0) printf '%s1' "${sha%?}" ;;
  *) printf '%s0' "${sha%?}" ;;
  esac
}

# restore: what this check changed, said rather than assumed. It changes nothing
# on the installation, and that is the reading worth printing.
restore() {
  RESTORE_VERDICT=PASS
  if [ -n "$DRY_RUN" ]; then
    RESTORE_NOTE="nothing was read and nothing was run: this was a dry run, so no GitHub call was made, no token was minted and no mint was asked for"
    return 0
  fi
  RESTORE_NOTE="nothing on the installation was changed, so nothing had to be put back: this check grants no permission, edits no ruleset and no branch protection, merges nothing, launches no run and writes no setting, no policy and no file outside its own scratch directory. The App's own permission and the base's bypass lists are READ (D2048's grant is your own step, and it is the one thing this check cannot do for you). ${MINT_ANSWERED} merge-mint call(s) were made: each one an admitted caller answered counts against the service's own per-repository rate (ten an hour, per instance, D2049) and writes one audit line. The installation token a successful mint answered with never left this run's scratch directory, was never printed and was never used, and this check removed it before the scratch was kept or removed"
}

cleanup() {
  restore
  scrub_credentials
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
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone, and the token a successful mint answered with was removed before it was" || echo "is still there, at $SCRATCH — and the token a successful mint answered with was removed from it before it was kept")"
  fi
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    [ "$verdict" = FAIL ] || continue
    case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
    failed=yes
  done <<<"$facts"
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then status=1; fi
  if [ -n "$UNMEASURED_KEYS" ] && [ "$status" = 0 ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    status=2
  fi
  print_block "$facts" "$status"
  exit "$status"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 542 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation this checkout belongs to, and gh repo view --json nameWithOwner for the repository"
  say "  [read, host] gh pr view ${PR:-<n>} --repo ${SLUG:-<owner/name>} --json state,headRefOid,baseRefName — OPEN, with the head the mint is bound to and the base whose rules are read"
  say "  [read, host] gh api /user/installations?per_page=100 and each installation's /repositories — the App's own installation on ${SLUG:-<owner/name>}, and its granted permissions (contents write, pull_requests write, administration read)"
  say "  [read, host] gh api /repos/${SLUG:-<owner/name>}/rulesets?per_page=100 and each ruleset by id, and gh api --include /repos/${SLUG:-<owner/name>}/branches/<base>/protection — the two places GitHub lets the App be a bypass actor (G14)"
  say "  [read, cloud] gcloud run services describe ${SERVICE_NAME} --region ${REGION} — the service's own URL, and the NAMES of its environment entries (never their values)"
  say "  [read, cloud] gcloud iam service-accounts describe <the daemon's account>, and gcloud run services get-iam-policy ${SERVICE_NAME} — whether the merge mint's caller class exists on this installation at all"
  say "  [network] POST ${SERVICE_URL:-<the service URL>}${MERGE_MINT_PATH} as the daemon (an ID token minted by impersonating its account, straight into a 0600 curl config), with the repository, the pull request, the head it must still be at and the App ID — the token that comes back is written into the scratch directory, never printed and never used"
  say "  [network] the same call at a head the pull request is NOT at (the refusal the binding exists for), the same call with your own ambient credential (the credential a coding-agent session holds: refused before any list is read), and the same call presenting a run identity (refused by name)"
  say "  [write, cloud] the service's own audit log gets one line per call an admitted caller answers (up to four calls; up to ten an hour per repository are allowed)"
  say "  [read, cloud] gcloud logging read (jsonPayload.purpose=merge_mint) — one line per admitted call, and none for a call refused before the caller was verified"
  say "  [write] nothing on the installation: no grant, no permission, no ruleset, no branch protection, no setting, no policy, no merge and no run"
  for key in installation pull-request app-installation bypass service daemon-identity mint mint-moved-head mint-person-token mint-run-identity audit; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no GitHub call, no gcloud call, no token minted, no mint asked for and no log read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation pull-request
  app-installation bypass service daemon-identity mint mint-moved-head
  mint-person-token mint-run-identity audit restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 542 RESULT\n' >&3
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
  printf 'CHECK 542 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
PR_CLI=""
APP_ID_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, whose installation this check reads"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --pr)
    [ $# -ge 2 ] || die "--pr needs the number of the open pull request the merge mint is asked about (a pull request of the sandbox repository, at a head the mint can be bound to)"
    PR_CLI=$2
    shift 2
    ;;
  --app-id)
    [ $# -ge 2 ] || die "--app-id needs the App's own numeric id, for the read of the base's bypass lists when /user/installations cannot be read"
    APP_ID_CLI=$2
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

SANDBOX=${CHECK542_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 542 needs --sandbox: the checkout of a repository of the sandbox project. Usage: bash scripts/live-checks/check542.sh --sandbox <checkout> --pr <number>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
PR=${CHECK542_PR:-${PR_CLI:-}}
if [ -z "$PR" ]; then
  die "Check 542 needs --pr: the number of the open pull request the merge mint is asked about, which is a pull request of the sandbox repository the App's installation covers (gh pr list prints the numbers). Usage: bash scripts/live-checks/check542.sh --sandbox <checkout> --pr <number>"
fi
case "$PR" in
*[!0-9]*) die "--pr takes a pull request number, and '$PR' is not one" ;;
esac
APP_ID=${CHECK542_APP_ID:-${APP_ID_CLI:-}}
case "$APP_ID" in
"") ;;
*[!0-9]*) die "--app-id takes the App's numeric id (1 to 20 digits), and '$APP_ID' is not one" ;;
esac
RUN_ID=${CHECK542_RUN_ID:-chk542-probe}
DRY_RUN=${CHECK542_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK542_KEEP_SCRATCH:-}
SERVICE_NAME=${CHECK542_SERVICE_NAME:-fugaro-credentials}
SERVICE_URL=${CHECK542_SERVICE_URL:-}
ORCHESTRATOR_ACCOUNT=${CHECK542_ORCHESTRATOR_ACCOUNT:-}
REGION=${CHECK542_REGION:-us-east5}
LOG_FRESHNESS=${CHECK542_LOG_FRESHNESS:-30m}
# The slack the audit row allows between the instant this machine took a call and
# the instant Cloud Logging stamps the line for it: the two clocks are not the
# same one, and a line written for the call can carry a timestamp a few seconds
# on either side of it. A minute is the slack; a narrower one would FAIL a
# correct installation for its clock, and the row names the instant it matched.
LOG_SKEW_SECONDS=${CHECK542_LOG_SKEW_SECONDS:-60}
GCP_TIMEOUT=${CHECK542_GCP_TIMEOUT_SECONDS:-180}
CLI_TIMEOUT=${CHECK542_CLI_TIMEOUT_SECONDS:-180}
TOKEN_TIMEOUT=${CHECK542_TOKEN_TIMEOUT_SECONDS:-60}
TIMEOUT=$((GCP_TIMEOUT > CLI_TIMEOUT ? GCP_TIMEOUT : CLI_TIMEOUT))
# Resolved before anything runs, and by absolute path: this check calls a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there.
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 542 needs 'fugaro' on PATH: the check reads the installation you have installed, and it is not there"
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
  step_installation
  step_pull_request
  step_app_installation
  step_bypass
  step_service
  step_daemon_identity
  step_mint
  step_mint_moved_head
  step_mint_person_token
  step_mint_run_identity
  step_audit
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
