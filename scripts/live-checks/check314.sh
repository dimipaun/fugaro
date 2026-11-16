#!/usr/bin/env bash
# Check 314 — T2's viewer-role coverage, as ONE script YOU run.
#
# T2 (docs/cli-output-console-graph/implementation-plan-graph.md, its CG12 row
# 314 and its rulings of 2026-11-06) is the half of the orchestrator subtree's
# access that no rules test can settle: the rules pin who may NOT read
# `orchestrator/` (CG6, H7) and a rules test cannot read a project's IAM. Whether
# a PERSON's credential carrying `roles/firebasedatabase.viewer` CAN read that
# subtree is IAM's answer, and B4 (the scheduling row) puts the check in your
# hands: **T2 is UNVERIFIED until you run it.** CG4's `fugaro orchestrator ls` is
# the read of that node, so it is the reading this check takes.
#
# What it reads, in this order:
#
#   1. The credential the CLI reads the database with. The CLI's token source is
#      `google.DefaultTokenSource` (internal/cli/cloud.go), which is the
#      application-default credential: $GOOGLE_APPLICATION_CREDENTIALS where it
#      is set, else $HOME/.config/gcloud/application_default_credentials.json —
#      oauth2's own wellKnownFile(), which reads neither CLOUDSDK_CONFIG nor
#      XDG_CONFIG_HOME, while `gcloud` honours both: on a machine that sets
#      either, the file read here and the account `gcloud` names can be two
#      different people's. T2 is about a PERSON's credential, so a file whose
#      `type` is a service account's, an impersonation's or a workload-identity
#      federation's stops the run: it would answer a different question. One
#      field is read out of the file — its type, with its quota project — and
#      nothing of the file is printed, pasted or logged.
#   2. The installation: `fugaro doctor --json`'s `project.name` and
#      `project.gcp_project`, held to what you typed when you typed `--project`.
#   3. The database's OWN project: the installation's config file
#      ($XDG_CONFIG_HOME/fugaro/projects/<name>.yaml, or the `--config` inside
#      --fugaro-flags where you passed one) carries `gcp_project` and
#      `budget.firebase_project`, and the project the IAM policy below is read
#      ON is the Firebase one. The two are the same in the single-project layout
#      and may differ (doctor's own preflight distinguishes them): the Realtime
#      Database, and the `orchestrator/` subtree with it, belongs to the Firebase
#      project.
#   4. The person whose roles are the subject: `gcloud config get-value account`,
#      or `--account <address>` where you pass one. This is the premise YOU own,
#      and the check says so in the row: the database read is made by your own
#      fugaro with the application-default credential, which is a SEPARATE store
#      from gcloud's own account, so where the person who ran `gcloud auth
#      application-default login` is a different one, pass that address with
#      --account or the two facts are about two different people.
#   5. The project's own IAM policy: `gcloud projects get-iam-policy <the Firebase
#      project>`. The bindings whose members name the account (`user:<address>`)
#      or its domain (`domain:<domain>`) are read: `roles/firebasedatabase.viewer`
#      among them is T2's premise, and a role that covers the whole project
#      (roles/owner, roles/editor, roles/firebasedatabase.admin) makes the read
#      below unattributable to viewer, so T2 is then NOT MEASURED rather than
#      passed.
#   6. **The read itself, which is the fact T2 is**: `fugaro orchestrator ls
#      --json` as that person. CG4's listing is a shallow read of `orchestrator/`
#      — one key per orchestrator the database holds — plus one lease read per
#      id, and the budget database's project is the one step 5 read the policy on.
#      The command's own sentence is quoted when it refuses, and its Viewer-role
#      sentence is read as T2 answered NO.
#
# **What this check cannot settle, and says so in its rows.** The project's own
# policy is what it reads: a role granted at the folder or organization level,
# or through a GROUP the person belongs to, is not in that document and this
# check cannot resolve it — a group grant of viewer would make the read below
# pass for a reason the policy alone does not show, and the row names the two
# blind spots rather than pretending to have covered them. What it settles is
# what T2 asks: as a person whose own project grant is viewer (and whose domain
# carries nothing wider), the read either answers or it is refused, and the
# refusal is quoted. The rules half is CG6's pin
# (`TestOrchestratorNodeIsDeniedToRunAndTokenIdentities`), not this check's: the
# `rules` row says so.
#
# What it NEVER does. It writes nothing, anywhere but its own scratch directory:
# no node, no file of yours, no project change, no grant, no rule. It mints no
# token of its own and passes no credential on a command line — the database read
# is your own fugaro's, with your own application-default credential, exactly the
# way you would read the listing by hand. It creates no cloud resource, spends
# nothing, and makes no model call and no run. Running it twice reads the same
# things twice and leaves nothing behind.
#
# Usage:      bash scripts/live-checks/check314.sh [--project <fugaro project>]
#                                                 [--account <address>]
#                                                 [--fugaro-flags '<flags>'] [--dry-run]
#             --project is the Fugaro project to read. Leave it out where a
#             project config is in force: `fugaro doctor --json` answers with it
#             and this check holds the two to each other.
#             --account is the person the IAM policy is read for. Leave it out
#             where `gcloud config get-value account` is that person — which it
#             is not where `gcloud auth application-default login` was run as
#             somebody else.
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (e.g. `--config /path/to/project.yaml`, which is also the file this
#             check reads the database's project out of). It is split on
#             whitespace, so a value with a space in it cannot be passed.
#             --dry-run prints the plan and makes no call at all.
# Needs:      bash, mktemp, sed, grep, head, cut, tr, python3, and a `fugaro`
#             (with `orchestrator ls` and `doctor --json`) and a `gcloud` on PATH
#             — your own gcloud, logged in as the person T2 is about. Nothing is
#             installed and nothing is fetched; a missing tool stops the run with
#             one line naming it.
# Where:      your own machine, against an installation whose project is NOT
#             `belong`: that name is refused before anything is read, in what you
#             typed and in what doctor answers, because the owner's own
#             installation is never the subject of an owner-run check. Nothing is
#             changed wherever you run it, so a sandbox installation and a
#             throwaway project are the natural place.
# Costs:      four reads: your application-default credential's type (local), one
#             `fugaro doctor --json` (which reads the installation's billing, IAM
#             and secret metadata — read-only, nothing billable), one
#             `gcloud projects get-iam-policy`, and one `fugaro orchestrator ls
#             --json` (one shallow node read plus one lease read per
#             orchestrator). No model call, no run, no Cloud Build, nothing.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 314 RESULT.
#             Exit 0 when T2 held (the person's own grant is the viewer role, and
#             that credential read an `orchestrator/` node), 1 when it did not (a
#             missing viewer binding, a refused read, a listing that is not the
#             document CG4 prints — each row says which), and 2 when this run
#             could not tell (a guard stopped it, the credential is not a
#             person's, the project carries a role that covers everything, the
#             subtree holds no node, a tool is missing, an internal error ended
#             it).
#
# CHECK314_FUGARO and CHECK314_GCLOUD name the two binaries (defaults `fugaro`
# and `gcloud`), so this script's own test can run the same code against fakes.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had. This check opens the
# file that holds your application-default refresh token and reads one field of
# it; nothing prints the file, but a shell that arrived tracing prints every line
# of the run -- including what a reader of that file was handed -- into the log
# you would then paste back. So the trace is turned off here, BASH_XTRACEFD is
# unset so a trace redirected to a file this script knows nothing about cannot be
# reopened, and guard_xtrace refuses a shell that arrived tracing (the flag sits
# in SHELLOPTS/BASHOPTS then, and every child would start tracing on its own).
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptor rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by the_verdict once the reading has been judged, so the exit trap
# can tell a finished run from one that ended on an error of its own.
REACHED_END=no
# --help printed the usage itself: it is not a run, and it is read by the exit
# trap, which has to see both of these whatever path ended the run.
HELPED=no
DRY_RUN=""
USAGE="check314.sh [--project <fugaro project>] [--account <address>] [--fugaro-flags '<flags>'] [--dry-run]"
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory, so a stop still gives you something to
# paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as an answer about T2 for a run that measured
# nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about T2"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 314 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 314 needs bash"
  need mktemp
  need sed "the config file's fields and the block's rows are read back out of files with it"
  need grep
  need head
  need cut
  need tr
  need python3 "the credential file, the IAM policy and the listing are JSON"
  need "$GCLOUD_BIN" "the person's own roles are read with it, and it is your gcloud login that says who that person is"
  need "$FUGARO_BIN" "the installation is read with doctor, and T2 is the orchestrator ls read"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk314-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk314.XXXXXXXX") || die "Check 314 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 314 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# ------------------------------------------------------------------- the guards
# guard_xtrace: a shell that arrived tracing prints every line of this run into
# the log an owner pastes back, and this run opens the file that holds the
# application-default refresh token. Nothing prints that file — the reader prints
# its type — but a tracing shell is refused rather than trusted, the way every
# scripted check in this directory does it.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), and this check opens the application-default credential file and prints the block you would paste back. Run it without -x"
    stop "the shell this check was started from is tracing (-x). Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing: this check reads no credential of its own and mints no token, the one file it opens is the application-default credential whose type it reads and nothing else, and the database read is your own fugaro's, with your own credential"
}

# is_belong NAME: the installation named `belong` is the owner's own. A substring
# test on purpose, so a name that merely contains it is refused too.
is_belong() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *belong*) return 0 ;;
  esac
  return 1
}

# guard_belong NAME WHERE: called twice, on what was typed and on what doctor
# answers, because the check may be pointed at the owner's own installation by
# either. Nothing is read before the typed one is refused.
guard_belong() { # name where
  if is_belong "$1"; then
    fact belong FAIL "'$1' ($2) names the owner's own installation: the owner's own installation is never the subject of an owner-run check, and this one reads its IAM policy and its database"
    stop "the project '$1' matches the installation 'belong', which this check must never touch"
  fi
}

# ------------------------------------------------- where the credential lives
# adc_path mirrors google.DefaultTokenSource exactly: the file
# $GOOGLE_APPLICATION_CREDENTIALS names, else oauth2's wellKnownFile(),
# $HOME/.config/gcloud/application_default_credentials.json. Neither
# CLOUDSDK_CONFIG nor XDG_CONFIG_HOME appears in that module, so honouring them
# here would read the type of a credential the listing is not made with -- and
# `gcloud`, whose account the row below records, moves with CLOUDSDK_CONFIG, so
# this would also pair the policy's answer with another person.
adc_path() {
  if [ -n "${GOOGLE_APPLICATION_CREDENTIALS:-}" ]; then
    printf '%s\n' "$GOOGLE_APPLICATION_CREDENTIALS"
    return 0
  fi
  printf '%s\n' "$HOME/.config/gcloud/application_default_credentials.json"
}

write_adc_reader() {
  cat >"$SCRATCH/adc_reader.py" <<'PY'
import json, sys

# The application-default credential file, read for the ONE thing this check may
# look at: which kind of credential it holds. The refresh token, the client
# secret and the rest of the document are never printed, and a case that needs
# the file to be a person's reads `refresh=yes` rather than the value.
path = sys.argv[1]
try:
    with open(path, "rb") as fh:
        raw = fh.read()
except OSError as exc:
    print("read=no")
    print("error=%s" % (exc.strerror or exc.__class__.__name__))
    sys.exit(0)
try:
    doc = json.loads(raw)
except ValueError:
    print("read=no")
    print("error=the file is not JSON")
    sys.exit(0)
if not isinstance(doc, dict):
    print("read=no")
    print("error=the file is not a JSON object")
    sys.exit(0)
print("read=yes")
print("type=%s" % (doc.get("type") or ""))
print("quota=%s" % (doc.get("quota_project_id") or ""))
print("refresh=%s" % ("yes" if doc.get("refresh_token") else "no"))
PY
}

judge_credential() {
  write_adc_reader
  CRED_PATH=$(adc_path)
  local answer read type quota refresh
  answer=$(python3 "$SCRATCH/adc_reader.py" "$CRED_PATH") ||
    stop "the application-default credential file at $CRED_PATH cannot be read by python3, and this check does not guess at what it holds"
  read=$(printf '%s\n' "$answer" | sed -n 's/^read=//p' | head -1)
  type=$(printf '%s\n' "$answer" | sed -n 's/^type=//p' | head -1)
  quota=$(printf '%s\n' "$answer" | sed -n 's/^quota=//p' | head -1)
  refresh=$(printf '%s\n' "$answer" | sed -n 's/^refresh=//p' | head -1)
  if [ "$read" != yes ]; then
    fact credential FAIL "the credential your fugaro reads the database with is the application-default one, and there is none this check can read at $CRED_PATH ($(printf '%s\n' "$answer" | sed -n 's/^error=//p' | head -1)). Run 'gcloud auth application-default login' as the person whose viewer role T2 is about, then run this check again. A machine whose credential is the metadata service's has a service account's, which is not what T2 is about"
    stop "no application-default credential to read at $CRED_PATH: run 'gcloud auth application-default login' as the person T2 is about"
  fi
  case "$type" in
  authorized_user)
    fact credential PASS "the credential your fugaro reads the database with is the application-default file at $CRED_PATH, type authorized_user ($([ "$refresh" = yes ] && printf 'a refresh token' || printf 'NO refresh token, which is not a person login this check can rely on')$( [ -n "$quota" ] && printf ', quota project %s' "$quota" || printf ', no quota project')). It is a person's own credential, which is what T2 is about: no secret of it is read, printed or logged"
    ;;
  *)
    fact credential FAIL "the application-default credential at $CRED_PATH has type '${type:-empty}', and T2 is about a PERSON's credential (authorized_user). A service account's key, an impersonation or a workload-identity federation answers a different question: run 'gcloud auth application-default login' as the person whose viewer role T2 is about -- with no key file and no impersonation in the way -- and run this check again"
    stop "the application-default credential is not a person's (type '${type:-empty}'): T2 is about a person's credential"
    ;;
  esac
}

# ------------------------------------------------------------- the installation
write_doctor_reader() {
  cat >"$SCRATCH/doctor_reader.py" <<'PY'
import json, sys

# `fugaro doctor --json`'s project block: the project's name and its GCP project.
# doctor exits non-zero whenever a check fails, so the exit status is not what
# this reader goes by -- the document is.
try:
    doc = json.load(sys.stdin)
except ValueError:
    sys.exit(1)
project = doc.get("project") if isinstance(doc, dict) else None
if not isinstance(project, dict):
    sys.exit(1)
print("name=%s" % (project.get("name") or ""))
print("gcp=%s" % (project.get("gcp_project") or ""))
PY
}

judge_project() {
  write_doctor_reader
  local args=(doctor --json)
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  DOCTOR_STATUS=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || DOCTOR_STATUS=$?
  local answer
  answer=$(python3 "$SCRATCH/doctor_reader.py" <"$SCRATCH/doctor.json" 2>/dev/null) || answer=""
  PROJECT_NAME=$(printf '%s\n' "$answer" | sed -n 's/^name=//p' | head -1)
  PROJECT_GCP=$(printf '%s\n' "$answer" | sed -n 's/^gcp=//p' | head -1)
  if [ -z "$PROJECT_NAME" ]; then
    if [ -n "$PROJECT" ]; then
      PROJECT_NAME=$PROJECT
      fact project INFO "fugaro doctor --json did not answer about a project (exit $DOCTOR_STATUS: $(first_line "$SCRATCH/doctor.err")), so the project this run is about is the one you typed, $PROJECT. Every fugaro call below carries --project $PROJECT, so a config that is not that project's is refused by the CLI rather than read here"
      return 0
    fi
    fact project FAIL "fugaro doctor --json did not answer about a project (exit $DOCTOR_STATUS: $(first_line "$SCRATCH/doctor.err")), and no --project was typed, so this check cannot tell which installation it is reading. Run it where a project config is in force, or pass --project <name>"
    stop "fugaro doctor --json named no project and no --project was typed: this check cannot tell which installation T2 is about"
  fi
  if [ -n "$PROJECT" ] && [ "$PROJECT" != "$PROJECT_NAME" ]; then
    fact project FAIL "you typed --project $PROJECT and the local config in force answers $PROJECT_NAME (fugaro doctor --json): two different installations, and this check will not pick one"
    stop "the project you typed is $PROJECT and the config in force is $PROJECT_NAME: run with --project $PROJECT_NAME, or point FUGARO_PROJECT at the one you meant"
  fi
  fact project PASS "the local config in force is project $PROJECT_NAME on GCP project $PROJECT_GCP (fugaro doctor --json): the policy and the listing below are read for it"
}

# ------------------------------------------------------------- the config file
# config_file_path: the file the CLI would read for this project, unless
# --fugaro-flags carries a --config, which is then the file this check reads too
# (a check that read a different one would name a database nobody is reading).
config_file_path() {
  local i=0 n=${#FUGARO_FLAGS[@]} flag
  while [ "$i" -lt "$n" ]; do
    flag=${FUGARO_FLAGS[$i]}
    case "$flag" in
    --config)
      if [ $((i + 1)) -lt "$n" ]; then
        printf '%s\n' "${FUGARO_FLAGS[$((i + 1))]}"
        return 0
      fi
      ;;
    --config=*)
      printf '%s\n' "${flag#--config=}"
      return 0
      ;;
    esac
    i=$((i + 1))
  done
  printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/$PROJECT_NAME.yaml"
}

judge_database() {
  CONFIG_FILE=$(config_file_path)
  if [ ! -r "$CONFIG_FILE" ]; then
    fact database FAIL "no readable project config at $CONFIG_FILE: this check reads the installation's own file for the project its database lives in (gcp_project, budget.firebase_project), and a check that remembered those would be reading whichever project it remembered"
    stop "no project config at $CONFIG_FILE: run this where the installation's config is, or pass --fugaro-flags '--config <path>'"
  fi
  CFG_GCP=$(sed -n 's|^ *gcp_project: *||p' "$CONFIG_FILE" | tr -d "\"'" | head -1)
  local fp url
  fp=$(sed -n 's|^ *firebase_project: *||p' "$CONFIG_FILE" | tr -d "\"'" | head -1)
  url=$(sed -n 's|^ *rtdb_url: *||p' "$CONFIG_FILE" | tr -d "\"'" | head -1)
  if [ -z "$CFG_GCP" ]; then
    fact database FAIL "$CONFIG_FILE carries no gcp_project, so this check cannot tell which installation the database belongs to"
    stop "$CONFIG_FILE carries no gcp_project"
  fi
  if [ -n "$fp" ]; then
    DB_PROJECT=$fp
    DB_FROM="budget.firebase_project, the Firebase project the database itself belongs to"
  else
    DB_PROJECT=$CFG_GCP
    DB_FROM="gcp_project -- the file carries no budget.firebase_project, so the database is the installation's own project's, which is the single-project layout"
  fi
  fact database PASS "the installation's file at $CONFIG_FILE: gcp_project $CFG_GCP, database $( [ -n "$url" ] && printf '%s' "$url" || printf 'address not recorded') in Firebase project $DB_PROJECT, read out of $DB_FROM. The policy below is read on $DB_PROJECT: that is the project the Realtime Database, and the orchestrator/ subtree with it, belongs to"
}

# ------------------------------------------------------------------- the person
judge_account() {
  if [ -n "$ACCOUNT" ]; then
    ACCOUNT_SOURCE="--account"
  else
    local status=0 out
    out=$("$GCLOUD_BIN" config get-value account 2>"$SCRATCH/account.err") || status=$?
    ACCOUNT=$(printf '%s\n' "$out" | head -1 | tr -d '[:space:]')
    ACCOUNT_SOURCE="gcloud config get-value account"
    if [ "$status" -ne 0 ] || [ -z "$ACCOUNT" ] || [ "$ACCOUNT" = "(unset)" ]; then
      fact account FAIL "gcloud names no account (exit $status, answer '${ACCOUNT:-(empty)}': $(first_line "$SCRATCH/account.err")), so this check cannot tell whose roles it is reading. Run 'gcloud auth login' as the person whose viewer role T2 is about, or pass --account <address>"
      stop "gcloud names no account: log in as the person T2 is about, or pass --account <address>"
    fi
  fi
  case "$ACCOUNT" in
  *gserviceaccount.com*)
    fact account FAIL "$ACCOUNT is a service account's address, and T2 is about a person's credential: run 'gcloud auth login' as the person whose viewer role T2 is about, or pass --account <their address>"
    stop "the account $ACCOUNT is a service account's: T2 is about a person's credential"
    ;;
  esac
  MEMBER="user:$ACCOUNT"
  fact account PASS "the policy below is read for $MEMBER ($ACCOUNT_SOURCE). The database read is made by your own fugaro with the application-default credential, which is a separate store from gcloud's own account: where the person who ran 'gcloud auth application-default login' is a different one, pass --account <that address> or the two facts are about two different people"
}

# ------------------------------------------------------------------- the policy
write_policy_reader() {
  cat >"$SCRATCH/policy_reader.py" <<'PY'
import json, sys

# One project's own IAM policy, read for the roles bound to ONE member: the
# account itself, or the domain it belongs to (which covers it without naming
# it, and is the one such grant a policy can show). A role granted through a
# group, or at the folder or organization level, is not in this document at all
# and the check's rows say so rather than reading them as absent.
member = sys.argv[1].lower()
addr = member.split(":", 1)[1] if ":" in member else member
domain = "domain:" + addr.split("@")[-1] if "@" in addr else ""
roles, domain_roles, bindings = [], [], 0
try:
    doc = json.load(sys.stdin)
except ValueError:
    print("read=no")
    print("error=the answer is not JSON")
    sys.exit(0)
if not isinstance(doc, dict) or not isinstance(doc.get("bindings", []), list):
    print("read=no")
    print("error=the answer is not a project's IAM policy")
    sys.exit(0)
for binding in doc.get("bindings") or []:
    if not isinstance(binding, dict):
        continue
    bindings += 1
    role = binding.get("role") or ""
    members = [str(m).lower() for m in (binding.get("members") or []) if isinstance(m, str)]
    if member in members:
        roles.append(role)
    if domain and domain in members:
        domain_roles.append(role)
print("read=yes")
print("bindings=%d" % bindings)
print("roles=%s" % ",".join(sorted(set(roles))))
print("domain-roles=%s" % ",".join(sorted(set(domain_roles))))
print("domain=%s" % domain)
PY
}

judge_policy() {
  write_policy_reader
  local status=0 answer read
  "$GCLOUD_BIN" projects get-iam-policy "$DB_PROJECT" --format=json \
    >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.err" || status=$?
  if [ "$status" -ne 0 ]; then
    fact policy FAIL "gcloud projects get-iam-policy $DB_PROJECT did not answer (exit $status): $(first_line "$SCRATCH/policy.err"). Reading a project's own policy needs resourcemanager.projects.getIamPolicy, which the basic Viewer role carries -- so the person who can read this database can read it"
    stop "gcloud projects get-iam-policy $DB_PROJECT did not answer (exit $status): $(first_line "$SCRATCH/policy.err")"
  fi
  answer=$(python3 "$SCRATCH/policy_reader.py" "$MEMBER" <"$SCRATCH/policy.json") || answer=""
  read=$(printf '%s\n' "$answer" | sed -n 's/^read=//p' | head -1)
  if [ "$read" != yes ]; then
    fact policy FAIL "what gcloud printed for $DB_PROJECT is not a project's IAM policy ($(printf '%s\n' "$answer" | sed -n 's/^error=//p' | head -1)): this check reads the bindings out of that document and will not guess"
    stop "the answer to gcloud projects get-iam-policy is not the document this check reads"
  fi
  POLICY_BINDINGS=$(printf '%s\n' "$answer" | sed -n 's/^bindings=//p' | head -1)
  ROLES=$(printf '%s\n' "$answer" | sed -n 's/^roles=//p' | head -1)
  DOMAIN_ROLES=$(printf '%s\n' "$answer" | sed -n 's/^domain-roles=//p' | head -1)
  POLICY_DOMAIN=$(printf '%s\n' "$answer" | sed -n 's/^domain=//p' | head -1)
  fact policy PASS "the project's own policy for $DB_PROJECT answered: $POLICY_BINDINGS binding(s) read. What a policy cannot show is named with the rows below: a role granted through a group, or at the folder or organization level, is not in this document"
}

has_role() { # "comma,list" role
  case ",$1," in *",$2,"*) return 0 ;; esac
  return 1
}

judge_viewer() {
  if has_role "$ROLES,$DOMAIN_ROLES" roles/firebasedatabase.viewer; then
    fact viewer PASS "the project's own policy binds roles/firebasedatabase.viewer to $MEMBER on $DB_PROJECT (the roles it binds to that member: ${ROLES:-none}$( [ -n "$DOMAIN_ROLES" ] && printf '; and to the domain %s: %s' "$POLICY_DOMAIN" "$DOMAIN_ROLES" )). That is T2's premise"
    return 0
  fi
  fact viewer FAIL "the project's own policy binds roles/firebasedatabase.viewer to nothing that covers $MEMBER on $DB_PROJECT (the roles it binds to that member: ${ROLES:-none}$( [ -n "$DOMAIN_ROLES" ] && printf '; to the domain %s: %s' "$POLICY_DOMAIN" "$DOMAIN_ROLES" )). A role granted through a GROUP the person belongs to, or at the folder or organization level, is not in a project's own policy and this check cannot resolve it -- where such a grant is what lets the read below answer, the answer is not this role's. Otherwise: 'gcloud projects add-iam-policy-binding $DB_PROJECT --member=$MEMBER --role=roles/firebasedatabase.viewer' is what grants it"
}

# The roles that cover the whole project, so a read made with one of them says
# nothing about the viewer role: T2 asks whether VIEWER is enough.
COVERING_ROLES=(roles/owner roles/editor roles/firebasedatabase.admin)

judge_broader() {
  local r found=()
  for r in "${COVERING_ROLES[@]}"; do
    if has_role "$ROLES" "$r"; then
      found+=("$r (bound to $MEMBER)")
      continue
    fi
    if has_role "$DOMAIN_ROLES" "$r"; then found+=("$r (bound to $POLICY_DOMAIN)"); fi
  done
  if [ ${#found[@]} -eq 0 ]; then
    fact broader PASS "no role that covers the whole project (roles/owner, roles/editor, roles/firebasedatabase.admin) is bound to $MEMBER or to its domain in the project's own policy, so the read below is attributable to the viewer role T2 is about"
    return 0
  fi
  BROADER=$(printf '%s, ' "${found[@]}")
  BROADER=${BROADER%, }
  fact broader INFO "a role that covers the whole project is bound on $DB_PROJECT for this person: $BROADER. Such a role reads every node whatever else the person holds, so the read below cannot be attributed to roles/firebasedatabase.viewer and T2 is NOT MEASURED by this run. Run this check as a person whose ONLY grant on the project is the viewer role -- a second account, or a sandbox project of its own"
}

# -------------------------------------------------------------- the read itself
write_listing_reader() {
  cat >"$SCRATCH/listing_reader.py" <<'PY'
import json, sys

# `fugaro orchestrator ls --json`'s document (schemas/orchestrator-output.schema.json,
# version 1: version, kind "ls", and one entry per orchestrator id). The ids are
# the children the shallow read of `orchestrator/` returned, which is the node
# T2 is about.
try:
    doc = json.load(sys.stdin)
except ValueError:
    print("read=no")
    print("error=the answer is not JSON")
    sys.exit(0)
if not isinstance(doc, dict) or doc.get("kind") != "ls" or not isinstance(doc.get("orchestrators"), list):
    print("read=no")
    print("error=the answer is not the listing document")
    sys.exit(0)
entries = [e for e in doc["orchestrators"] if isinstance(e, dict)]
ids = [str(e.get("project") or "") for e in entries]
kinds = sorted({str(e.get("kind") or "") for e in entries})
print("read=yes")
print("version=%s" % (doc.get("version"), ))
print("count=%d" % len(entries))
print("ids=%s" % ",".join(ids))
print("kinds=%s" % ",".join(kinds))
PY
}

# run_listing takes THE read: the one T2 is. Its own sentence is kept, because a
# refusal is the answer T2 turns on.
run_listing() {
  write_listing_reader
  local args=(orchestrator ls --json)
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then args+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then args+=(--project "$PROJECT"); fi
  LISTING_STATUS=0
  "$FUGARO_BIN" "${args[@]}" >"$SCRATCH/listing.json" 2>"$SCRATCH/listing.err" || LISTING_STATUS=$?
}

judge_listing() {
  local sentence
  if [ "$LISTING_STATUS" -ne 0 ]; then
    sentence=$(first_line "$SCRATCH/listing.err")
    if [ -z "$sentence" ]; then sentence=$(first_line "$SCRATCH/listing.json"); fi
    case "$sentence" in
    *"refused the read"*)
      fact listing FAIL "the read T2 is was REFUSED: 'fugaro orchestrator ls --json' exited $LISTING_STATUS and the CLI's own sentence is '$sentence'. That sentence names the Viewer role, so it is a permissions refusal and not an outage -- and with the roles read above, T2 is answered NO on $DB_PROJECT: roles/firebasedatabase.viewer does not cover orchestrator/"
      ;;
    *)
      fact listing FAIL "the read T2 is did not answer: 'fugaro orchestrator ls --json' exited $LISTING_STATUS and its own sentence is '${sentence:-nothing: it wrote no line}'. Its read is a shallow read of orchestrator/ plus one lease read per id (CG4), so the sentence says which of the two failed or whether the command did not run at all"
      ;;
    esac
    return 0
  fi
  LISTING_ANSWER=$(python3 "$SCRATCH/listing_reader.py" <"$SCRATCH/listing.json") || LISTING_ANSWER=""
  local read
  read=$(printf '%s\n' "$LISTING_ANSWER" | sed -n 's/^read=//p' | head -1)
  if [ "$read" != yes ]; then
    LISTING_READ=no
    fact listing FAIL "'fugaro orchestrator ls --json' exited 0 but what it printed is not the listing document CG4 defines (version, kind 'ls', orchestrators): $(printf '%s\n' "$LISTING_ANSWER" | sed -n 's/^error=//p' | head -1). A build whose document is another shape cannot be read as T2's answer"
    return 0
  fi
  LISTING_READ=yes
  LISTING_VERSION=$(printf '%s\n' "$LISTING_ANSWER" | sed -n 's/^version=//p' | head -1)
  NODE_COUNT=$(printf '%s\n' "$LISTING_ANSWER" | sed -n 's/^count=//p' | head -1)
  NODE_IDS=$(printf '%s\n' "$LISTING_ANSWER" | sed -n 's/^ids=//p' | head -1)
  NODE_KINDS=$(printf '%s\n' "$LISTING_ANSWER" | sed -n 's/^kinds=//p' | head -1)
  fact listing PASS "the read T2 is answered: 'fugaro orchestrator ls --json' exited 0 with the listing document CG4 defines (version $LISTING_VERSION, kind 'ls'), as $MEMBER on $DB_PROJECT. The command's read is a shallow read of orchestrator/ plus one lease read per id, so the subtree's permission is what answered it"
}

judge_node() {
  # A listing that did not answer is a FAIL above, not an empty subtree: this row
  # never turns a refused read into "orchestrator/ holds nothing".
  if [ "$LISTING_READ" != yes ]; then
    fact node INFO "the listing did not answer, so this run has no reading of the subtree's contents: whether orchestrator/ holds a node is not measured here, and the row above says what the command answered"
    return 0
  fi
  if [ "${NODE_COUNT:-0}" -gt 0 ]; then
    fact node PASS "the shallow read of orchestrator/ returned $NODE_COUNT orchestrator(s) (${NODE_IDS:-}; kind $( [ -n "$NODE_KINDS" ] && printf '%s' "$NODE_KINDS" || printf 'unknown' )), so a node of that subtree was really read and not an empty path"
    return 0
  fi
  NODE_EMPTY=yes
  fact node INFO "the read was permitted, but orchestrator/ holds no id in $DB_PROJECT's database: the shallow read answered and returned nothing. That settles the permission and not the thing T2 is about, so this run does NOT hold T2. Start a daemon on this installation ('fugaro orchestrator run', which writes its node under orchestrator/<the project's name>) or point this check at the installation that drives one, and run it again"
}

# The other half of T2, said out loud so the block cannot be read as covering it.
report_rules() {
  fact rules INFO "the rules half of T2 -- that no run or token identity reads or writes orchestrator/ (H7) -- is CG6's pin and not this check's reading: TestOrchestratorNodeIsDeniedToRunAndTokenIdentities and TestNoClientWriteUnderOrchestrator, on the deployed document (Check 300's subtree row). What this check adds is the other half: the person's own credential and the viewer role that covers that subtree"
}

# ------------------------------------------------------------------- teardown
report_teardown() {
  fact teardown PASS "this check wrote nothing, installed nothing, granted nothing and spent nothing: it read your application-default credential's type, one project's IAM policy and one listing. The scratch directory (mode 0700, holding the readers it ran) is removed at the end, and nothing of yours was touched"
}

# print_plan: what a real run would do, printed by --dry-run INSTEAD of doing any
# of it. This check's own test pins that a dry run makes no call at all -- no
# gcloud, no fugaro, no read of the credential file -- so the plan names the calls
# and never makes one.
print_plan() {
  say "dry run: NOTHING below was done. A real run would do, in this order:"
  say "  [reads the filesystem] the application-default credential file your fugaro reads the database with (\$GOOGLE_APPLICATION_CREDENTIALS where it is set, else \$HOME/.config/gcloud/application_default_credentials.json, which is oauth2's own wellKnownFile -- not \$CLOUDSDK_CONFIG, which only gcloud honours): its type is read and nothing else of it -- a file that is not a person's (authorized_user) stops the run"
  say "  [reads] ${FUGARO_BIN} doctor --json$( [ ${#FUGARO_FLAGS[@]} -gt 0 ] && printf ' %s' "${FUGARO_FLAGS[*]}" )$( [ -n "$PROJECT" ] && printf ' --project %s' "$PROJECT" ): project.name and project.gcp_project, held to --project where one was typed"
  say "  [reads the filesystem] the installation's config file (\$XDG_CONFIG_HOME/fugaro/projects/<name>.yaml, or the --config inside --fugaro-flags): gcp_project and budget.firebase_project -- the second is the project the database, and orchestrator/ with it, belongs to"
  say "  [reads] ${GCLOUD_BIN} config get-value account$( [ -n "$ACCOUNT" ] && printf ' (not called: --account %s was typed)' "$ACCOUNT" ): the person the policy below is read for"
  say "  [reads] ${GCLOUD_BIN} projects get-iam-policy <the database's Firebase project> --format=json: the roles bound to user:<that account> and to its domain, among them roles/firebasedatabase.viewer (T2's premise) and any project-wide role that would make the read below unattributable to it"
  say "  [reads] ${FUGARO_BIN} orchestrator ls --json$( [ ${#FUGARO_FLAGS[@]} -gt 0 ] && printf ' %s' "${FUGARO_FLAGS[*]}" )$( [ -n "$PROJECT" ] && printf ' --project %s' "$PROJECT" ) -- THE READ T2 IS: CG4's shallow read of orchestrator/ plus one lease read per id, as the same person, against the database whose project the policy was read on"
  say "  [writes] nothing at all: no cloud resource, no node, no file outside the scratch directory, which is removed at the end"
  say "Nothing is created, changed or spent, and no agent runs any step of it."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# -- not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(xtrace credential project belong database account policy viewer
  broader listing node rules t2 teardown)

print_block() { # print_block FACTS STATUS -- always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=$'\n'
  printf '\nCHECK 314 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
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
  while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case "$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done <<<"$facts"
  printf 'CHECK 314 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and made nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable, a
  # bad substitution), and a block that then read as a finished run would offer the
  # rows that read "not reached" as an answer about T2 when the reads that would
  # have filled them never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about T2"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH -- remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# ------------------------------------------------------------------- the verdict
row_verdict() {
  sed -n "s/^$1|\([A-Z]*\)|.*/\1/p" "$FACTS_FILE" | head -1
}

the_verdict() {
  local key drifted=() list not_measured=()
  for key in credential project database account policy viewer listing; do
    if [ "$(row_verdict "$key")" = FAIL ]; then drifted+=("$key"); fi
  done
  if [ -n "$BROADER" ]; then
    not_measured+=("$MEMBER also carries $BROADER on $DB_PROJECT, so a read that answers cannot be attributed to roles/firebasedatabase.viewer")
  fi
  if [ "$NODE_EMPTY" = yes ]; then
    not_measured+=("orchestrator/ holds no id in $DB_PROJECT's database, so the read returned an empty subtree")
  fi
  if [ ${#not_measured[@]} -gt 0 ]; then
    list=$(printf '%s; ' "${not_measured[@]}")
    list=${list%; }
    if [ ${#drifted[@]} -gt 0 ]; then
      list="$list (and these rows FAILed: $(printf '%s, ' "${drifted[@]}" | sed 's/, $//'))"
    fi
    fact t2 INFO "T2 is NOT MEASURED by this run: $list. The rows above are what was read; nothing here is an answer about whether roles/firebasedatabase.viewer covers orchestrator/"
    REACHED_END=yes
    exit 2
  fi
  if [ ${#drifted[@]} -eq 0 ]; then
    fact t2 PASS "T2 HOLDS for this installation: the project's own policy binds roles/firebasedatabase.viewer to $MEMBER on $DB_PROJECT and nothing wider, and that person's credential read the orchestrator/ subtree -- $NODE_COUNT orchestrator(s) (${NODE_IDS:-}) out of a shallow read of the node CG4 defines, with the CLI's listing document (version $LISTING_VERSION, kind 'ls'). The rules half (that no run or token identity reads it) is CG6's pin: the rules row names it"
    REACHED_END=yes
    exit 0
  fi
  list=$(printf '%s, ' "${drifted[@]}")
  list=${list%, }
  fact t2 FAIL "T2 does NOT hold for this installation as read: $list. Each row above says which reading it is and what to do about it"
  REACHED_END=yes
  exit 1
}

# first_line FILE: a tool's own words, one line, for a detail a reader has to be
# able to scan.
first_line() {
  if [ -f "$1" ]; then
    head -1 "$1" 2>/dev/null | cut -c1-300 || true
  fi
}

# -------------------------------------------------------------------- main
parse_args() {
  PROJECT=${CHECK314_PROJECT:-}
  ACCOUNT=${CHECK314_ACCOUNT:-}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK314_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a Fugaro project name. usage: $USAGE"
      PROJECT=$2
      shift 2
      ;;
    --account)
      [ $# -ge 2 ] || die "--account needs the person's address. usage: $USAGE"
      ACCOUNT=$2
      shift 2
      ;;
    --fugaro-flags)
      [ $# -ge 2 ] || die "--fugaro-flags needs a string of flags. usage: $USAGE"
      read -r -a FUGARO_FLAGS <<<"$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_xtrace
  if [ -n "$DRY_RUN" ]; then
    # A dry run reads nothing: the project guard is judged on what was TYPED (a
    # string test, no filesystem and no call), and the reads are the plan's
    # business.
    if [ -n "$PROJECT" ]; then
      guard_belong "$PROJECT" "the project you typed"
      fact belong PASS "the project name you typed ($PROJECT) does not name the owner's own installation (what was typed contains no 'belong')"
      fact project INFO "a dry run reads nothing: a real run asks fugaro doctor --json which installation the config in force is, holds it to the --project $PROJECT you typed, and refuses an installation named belong in that answer too"
    else
      fact belong INFO "no --project was typed, so the guard had nothing to judge: a real run refuses an installation named belong in what doctor answers too, before the policy and the listing are read"
      fact project INFO "no --project was typed, and a dry run reads nothing: a real run asks fugaro doctor --json which installation the config in force is"
    fi
    fact dry-run PASS "guards and the plan only: no credential file was opened, no gcloud call and no fugaro call was made, and nothing was written outside the scratch directory"
    BLOCK_KEYS=(xtrace belong project dry-run)
    print_plan
    REACHED_END=yes
    return 0
  fi
  if [ -n "$PROJECT" ]; then guard_belong "$PROJECT" "the project you typed"; fi
  judge_credential
  judge_project
  guard_belong "$PROJECT_NAME" "the config in force"
  fact belong PASS "neither the project you typed${PROJECT:+ ($PROJECT)} nor the one the config in force answers ($PROJECT_NAME) names the owner's own installation (nothing read contains 'belong')"
  judge_database
  judge_account
  judge_policy
  judge_viewer
  judge_broader
  run_listing
  judge_listing
  judge_node
  report_rules
  report_teardown
  the_verdict
}

FUGARO_BIN=${CHECK314_FUGARO:-fugaro}
GCLOUD_BIN=${CHECK314_GCLOUD:-gcloud}
FUGARO_FLAGS=()
PROJECT=""
PROJECT_NAME=""
PROJECT_GCP=""
DOCTOR_STATUS=0
ACCOUNT=""
ACCOUNT_SOURCE=""
MEMBER=""
CRED_PATH=""
CONFIG_FILE=""
CFG_GCP=""
DB_PROJECT=""
DB_FROM=""
POLICY_BINDINGS=""
ROLES=""
DOMAIN_ROLES=""
POLICY_DOMAIN=""
BROADER=""
LISTING_STATUS=0
LISTING_ANSWER=""
LISTING_READ="no"
LISTING_VERSION=""
NODE_COUNT=""
NODE_IDS=""
NODE_KINDS=""
NODE_EMPTY="no"

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
