#!/usr/bin/env bash
# Check 401 — the old member lists become grants, and a pure rename is an empty
# plan, as ONE script YOU run.
#
# It settles the live half of RS3 (docs/roles-scopes/implementation-plan.md,
# Check 401) on one sandbox installation: `fugaro update` on an installation
# whose local config still carries the version-1 `terraform.launchers` and
# `terraform.operators` writes the grants node under `config/admin/access` from
# the installation's own outputs (each `user:` member becoming operator@project
# or admin@project), retires those keys — the file is written as version 2, with
# `terraform.operators` and `terraform.admins` derived from the node — and the
# plan of the next converge is EMPTY, because the Terraform variable and
# resource names did not change (D1589) and a pure rename is nothing to apply.
# That pair is what only a real installation can show: no hermetic test in this
# tree reads an installation's outputs or runs Terraform, and
# `TestUpdateMigratesListsToAccessNode` is the other half of the same claims.
#
# Usage:      bash scripts/live-checks/check401.sh --installation <project> \
#               --config <file> [--dry-run]
#             The installation is named as the GCP project ID it lives in, and
#             it has to be the project of the config you name AND the one
#             `fugaro doctor --json` answers: the two are compared and a
#             disagreement is a STOP before anything runs — this check never
#             decides for itself which installation it is looking at.
#             `--config` is the local config of THAT installation (default:
#             $FUGARO_CONFIG when it is set). It is named rather than looked up
#             because the check reads that file's own version and keys, and the
#             config the CLI would pick by itself is not a document this check
#             can name.
# Needs:      bash, mktemp, python3, gcloud, curl and fugaro on PATH. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it. No python3 module beyond the
#             standard library is used.
# Where:      SANDBOX installations only, and never `belong`: the name is
#             refused, whether you typed it as the installation or the config
#             answers it. The check runs `fugaro update` — a converge, with the
#             plan and the apply that command already does — and one `fugaro
#             update --plan-only` after it. It launches no run, makes no model
#             call and creates no resource of its own; the migration it settles
#             is the installation's, and it stays.
# When:       Run it on an installation you have NOT yet converged with this
#             CLI: the migration is the first half of what is measured and it
#             happens once, when the node is absent. On an installation that
#             already migrated, the check says so and asserts the end state and
#             the empty plan alone.
# Costs:      one converge and one plan of a sandbox installation. Nothing here
#             is billable on its own; an image rebuild or a deploy that an
#             unrelated stage decides it wants is the converge's own asking, and
#             `--yes` covers it — run the check where that is acceptable.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed.
#             Nothing else was created, so the `restore` row says that and
#             whether the directory really went.
# Output:     progress on stderr, ONE block on stdout headed CHECK 401 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one FAILed, 2 when the run could not tell (a guard stopped
#             it, a tool is missing, the converge did not run).
#
# The block never carries a member address. The two lists are this
# installation's people and the place the block is pasted into is not the place
# for them: every roster fact here is a COUNT and a role word, and the
# comparisons that need the addresses are made inside the script. A trace would
# print the config and the node document in full, so guard_xtrace REFUSES a
# shell that arrived with tracing on: `bash -x` prints every command and every
# document this check reads as it reads it.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had. BASH_XTRACEFD is unset
# so a trace redirected to a file this script knows nothing about cannot be
# reopened, and the flag is looked for in each of the three places it can sit: a
# shell that arrived tracing would print the member lists on its own. Check 145
# spells the three parts out; this is the same guard.
XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and a single
# pattern over the three concatenated would match that 'x' and refuse every run.
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
ABORT_REASON=""
# Set to yes by main once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
# Set to yes by main's --dry-run branch, so the exit trap prints the rows the run
# really recorded and not a wall of "not reached".
DRY_RAN=""
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# The node the migration writes, and the two words its roles are spelled with:
# internal/access's PathNode, RoleOperator and RoleAdmin. They are written here
# rather than asked of the CLI because this check is the one that reads the node
# OUTSIDE the CLI — that is the point of it — and it holds the document to the
# words this release writes.
NODE_PATH="config/admin/access"
ROLE_OPERATOR="operator"
ROLE_ADMIN="admin"

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
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
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as "the migration is fine" for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the migration" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 401 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 401 needs bash"
  need mktemp
  need python3 "the reader that parses the config, doctor's answer and the node document is one python3 file"
  need gcloud "the check reads the grants node over the Realtime Database's REST API with an access token of your own"
  need curl
  need fugaro
}

# ------------------------------------------------------------------ arguments
USAGE="$PROG --installation <project> --config <file> [--dry-run]"
INSTALLATION=""
CONFIG=""
DRY_RUN=${CHECK401_DRY_RUN:-}
usage() {
  cat <<EOF
usage: $USAGE

  --installation <project>  the sandbox installation to converge, as its GCP
                            project ID (never belong)
  --config <file>           the local config of THAT installation; the default
                            is \$FUGARO_CONFIG
  --dry-run                 the local guards and the plan of what this run
                            would do, with nothing run: no converge, no
                            plan-only run and no read of the node
EOF
}
while [ $# -gt 0 ]; do
  case "$1" in
  --installation)
    [ $# -ge 2 ] || die "usage: $USAGE"
    INSTALLATION=$2
    shift 2
    ;;
  --config)
    [ $# -ge 2 ] || die "usage: $USAGE"
    CONFIG=$2
    shift 2
    ;;
  --dry-run) DRY_RUN=1; shift ;;
  -h | --help)
    usage
    exit 0
    ;;
  *) die "usage: $USAGE" ;;
  esac
done
[ -n "$INSTALLATION" ] || die "usage: $USAGE"
if [ -z "$CONFIG" ]; then
  CONFIG=${FUGARO_CONFIG:-}
fi
[ -n "$CONFIG" ] || die "usage: $USAGE (or set FUGARO_CONFIG to the sandbox installation's config)"

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the local config and the grants node — this installation's member addresses — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check401.sh --installation <project> --config <file> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a member address"
}

# guard_sandbox: `belong` is the owner's own installation and this check
# converges whatever it is pointed at, so the name is refused here — before
# doctor is asked, before a config is read — and doctor's own answer is refused
# the same way a moment later, because the name you type and the config in force
# are two things and the check has to see the same one twice.
guard_sandbox() {
  case "$INSTALLATION" in
  belong | belong-*)
    fact installation FAIL "the installation named is $INSTALLATION, which is the owner's own: Check 401 converges an installation's roles, and it is a sandbox check"
    stop "Check 401 never runs against 'belong'. Name a sandbox installation: bash scripts/live-checks/check401.sh --installation <project> --config <file>"
    ;;
  esac
  if [ ! -f "$CONFIG" ]; then
    fact installation FAIL "the config named is $CONFIG, and there is no such file"
    stop "the config named ($CONFIG) is not a file. Name the sandbox installation's own local config: bash scripts/live-checks/check401.sh --installation <project> --config <file>"
  fi
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk401-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk401.XXXXXXXX") || die "Check 401 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 401 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
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
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before
  # print_block and lose the whole block.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it:
  # bash exits by itself for an error the ERR trap does not see (an unbound
  # variable, a bad substitution), and a block that then read as a finished run
  # would offer a row of FAILs as a verdict about the migration when the steps
  # that would have filled them never ran. Any such ending is an abort, and 2
  # says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the migration"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cloud state of this check's to undo"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    facts="${facts}"$'\n'"restore|$restore|this check created nothing and changed no state of its own: the converge it runs is the installation's own next fugaro update, and the migration that writes the grants node is that run's outcome and STAYS (reverting it would not be a restore, it would be the bug this check exists to catch). The scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
  fi
  # The verdict is the facts themselves: a run is FAILed when one of them is.
  local verdict=$status
  case "$facts" in
  *"|FAIL|"*) [ "$verdict" -eq 0 ] && verdict=1 ;;
  esac
  if [ -n "$DRY_RAN" ]; then
    print_block "$facts" "$verdict" recorded
  else
    print_block "$facts" "$verdict"
  fi
  exit "$verdict"
}

BLOCK_KEYS=(xtrace installation config node-before update node-after config-after legacy plan)

print_block() { # print_block FACTS STATUS [recorded] — always on the owner's own
  # stdout. The third argument is for a dry run, which measures no step: only
  # what the run recorded is printed, because a wall of "not reached" rows would
  # read as a migration that failed when nothing was attempted (Check 111's
  # print_block spells the same rule out).
  local facts=$1 status=$2 mode=${3:-}
  local key printed=""
  printf '\nCHECK 401 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    elif [ -z "$mode" ]; then
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 401 RESULT END (exit %s)\n' "$status" >&3
}

# --------------------------------------------------------------- the readers
# reader SUBCOMMAND [ARGS] — one python3 file that parses what this check reads
# and prints a FIXED VOCABULARY: `key=value` lines, counts and yes/no. No
# address, no member form and no document text leaves it, which is what keeps
# the block free of this installation's people.
reader() { python3 "$SCRATCH/reader.py" "$@"; }

# field_of NAME ANSWER — one value out of a reader's answer.
field_of() {
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
import json
import re
import sys

# The three documents this check reads -- doctor's answer, the local config, the
# grants node -- reduced here to counts, yes/no and the two role words. The
# addresses are carried in roles_* so the shell can compare them, and printed by
# nothing else: the block the owner pastes carries counts alone.

LISTS = ("launchers", "operators", "admins", "budget_admins")


def out(pairs):
    for k, v in pairs:
        print(f"{k}={v}")


def text(path):
    # A document is named by the shell only when it is a file it keeps (the
    # config); the answers of the commands it runs arrive on standard input,
    # which is where `reader doctor <doctor.json` and `reader node <node.json`
    # put them.
    if path:
        with open(path, encoding="utf-8", errors="replace") as f:
            return f.read()
    return sys.stdin.read()


def member_kind(member):
    return member.split(":", 1)[0] if ":" in member else "other"


def unquote(value):
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
        return value[1:-1]
    return value


def doctor(path=""):
    doc = json.loads(text(path))
    project = doc.get("project") or {}
    out([("name", project.get("name", "")), ("project", project.get("gcp_project", "")),
         ("region", project.get("region", ""))])


def config(path=""):
    # A minimal reader for the two blocks this check is about, not a YAML
    # implementation. The local config is written by the CLI and is flat: a
    # top-level key per setting, `budget:` and `terraform:` blocks under it, and
    # one member per `- ` line in a list. A block ends at the next line that
    # starts in column zero, which is the whole of the grammar read here.
    top = {}
    lists = {name: [] for name in LISTS}
    seen = set()
    block = None
    current = None
    lines = text(path).splitlines()
    for raw in lines:
        stripped = raw.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if not raw.startswith((" ", "\t")):
            current = None
            if stripped.endswith(":"):
                block = stripped[:-1].strip()
                continue
            block = None
            key, _, value = stripped.partition(":")
            top[key.strip()] = unquote(value)
            continue
        if block == "terraform" and stripped.startswith("- "):
            if current is not None:
                lists[current].append(unquote(stripped[2:]))
            continue
        key, _, value = stripped.partition(":")
        key, value = key.strip(), value.strip()
        if block == "terraform" and key in lists:
            current = key
            # Presence is recorded before the value: a key written as an empty
            # list is still a key the file carries, and the check is about
            # whether a retired one is still there.
            seen.add(key)
            if value in ("", "[]"):
                continue
            if value.startswith("[") and value.endswith("]"):
                body = value[1:-1].strip()
                lists[key] = [unquote(part) for part in body.split(",")] if body else []
                continue
            lists[key] = [unquote(value)]
        elif block == "budget" and key == "rtdb_url":
            top["rtdb_url"] = unquote(value)

    try:
        version = int(top.get("version", "0") or "0")
    except ValueError:
        version = 0
    # The version is what tells the two shapes apart and it is read before the
    # keys, the rule localcfg.Parse follows: in version 1 `operators` names the
    # ADMIN list and there is no `admins` key; from version 2 on it names the
    # OPERATOR list.
    if version >= 2:
        op, adm = lists["operators"], lists["admins"]
    else:
        op, adm = lists["launchers"], lists["operators"]
    out([
        ("version", version),
        ("name", top.get("name", "")),
        ("project", top.get("gcp_project", "")),
        ("region", top.get("region", "")),
        ("rtdb", top.get("rtdb_url", "")),
        ("op", len(op)),
        ("adm", len(adm)),
        ("has_launchers", "yes" if "launchers" in seen else "no"),
        ("has_operators", "yes" if "operators" in seen else "no"),
        ("has_admins", "yes" if "admins" in seen else "no"),
        ("legacy", sum(1 for m in op + adm if member_kind(m) != "user")),
        ("roles_op", ",".join(sorted(m for m in op if member_kind(m) == "user"))),
        ("roles_adm", ",".join(sorted(m for m in adm if member_kind(m) == "user"))),
    ])


def node(path=""):
    raw = text(path).strip()
    if raw in ("", "null"):
        doc, grants = "absent", []
    else:
        doc, grants = "present", ((json.loads(raw) or {}).get("grants") or [])
    op = [g for g in grants if g.get("role") == "operator"]
    adm = [g for g in grants if g.get("role") == "admin"]
    out([
        ("doc", doc),
        ("grants", len(grants)),
        ("op", len(op)),
        ("adm", len(adm)),
        ("other_roles", len(grants) - len(op) - len(adm)),
        ("project", sum(1 for g in grants if g.get("scope") == "project")),
        ("other_scope", sum(1 for g in grants if g.get("scope") != "project")),
        ("nonuser", sum(1 for g in grants if member_kind(g.get("user", "")) != "user")),
        ("roles_op", ",".join(sorted(g.get("user", "") for g in op))),
        ("roles_adm", ",".join(sorted(g.get("user", "") for g in adm))),
    ])


def update(path=""):
    # The migration's own roster line, and the "nothing to apply" line of a
    # converge with an empty plan. Both are looked for by their fixed words and
    # never pasted: the roster behind the first one is member addresses.
    text_in = text(path)
    roster = []
    migrated = "no"
    m = re.search(
        r"migrated the installation's member lists into the grants node: (.*?); "
        r"the local config's", text_in, re.S)
    if m:
        migrated = "yes"
        roster = re.findall(r"(\S+?) \((operator|admin)\)", m.group(1))
    out([
        ("migrated", migrated),
        ("migrated_grants", len(roster)),
        ("migrated_op", sum(1 for _, role in roster if role == "operator")),
        ("migrated_adm", sum(1 for _, role in roster if role == "admin")),
        ("migrated_roles_op", ",".join(sorted(u for u, role in roster if role == "operator"))),
        ("migrated_roles_adm", ",".join(sorted(u for u, role in roster if role == "admin"))),
        ("drift", "yes" if "is not what the installation's own outputs list" in text_in else "no"),
        ("no_changes", "yes" if "No changes: the installation matches the plan." in text_in else "no"),
    ])


if __name__ == "__main__":
    try:
        which, *rest = sys.argv[1:]
        {"doctor": doctor, "config": config, "node": node, "update": update}[which](*rest)
    except Exception as exc:  # noqa: BLE001 — the check reads the failure, not a traceback
        print(f"error={type(exc).__name__}")
PY
}

# --------------------------------------------------------------- the requests
# run_capture OUTFILE CMD... — runs a read, keeps stdout in OUTFILE, and leaves
# its exit status in $?. stderr is shown to the owner and never parsed: it is
# where gcloud and fugaro put their progress, and a warning on it is not a
# failed read.
run_capture() {
  local out=$1
  shift
  "$@" >"$out" 2>"$SCRATCH/cmd.err" || {
    local status=$?
    sed -n '1,5p' "$SCRATCH/cmd.err" >&4 || true
    return "$status"
  }
}

# node_read FILE — the node document, straight from the Realtime Database's REST
# API with an access token of your own. It is the one read of the node that is
# NOT the CLI's, which is why the check can say what the migration wrote without
# believing the migration: the rules' `.read: false` binds client tokens, and an
# IAM-authenticated read of the database (yours, on a sandbox) is answered.
node_read() {
  local out=$1 url token code
  url="${RTDB%/}/$NODE_PATH.json"
  token=$(gcloud auth print-access-token 2>"$SCRATCH/token.err") || {
    sed -n '1,3p' "$SCRATCH/token.err" >&4 || true
    return 1
  }
  [ -n "$token" ] || return 1
  code=$(curl -sS -o "$out" -w '%{http_code}' -H "Authorization: Bearer $token" "$url" 2>"$SCRATCH/curl.err") || {
    sed -n '1,3p' "$SCRATCH/curl.err" >&4 || true
    return 1
  }
  case "$code" in
  200) return 0 ;;
  401 | 403)
    owner "the database answered $code: your own account may not read $NODE_PATH in this installation"
    return 1
    ;;
  *)
    owner "the database answered $code for $NODE_PATH"
    return 1
    ;;
  esac
}

# ------------------------------------------------------------------ the steps
step_installation() {
  if ! run_capture "$SCRATCH/doctor.json" fugaro doctor --json; then
    stop "fugaro doctor --json did not answer, so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs your own credentials and a local config for the installation"
  fi
  local answer name project region
  answer=$(reader doctor <"$SCRATCH/doctor.json") || stop "the answer to fugaro doctor --json is not the document this check reads (is this fugaro too old to print --json?)"
  case "$answer" in *error=*) stop "fugaro doctor --json printed something this check cannot read: run it yourself and see" ;; esac
  name=$(field_of name "$answer")
  project=$(field_of project "$answer")
  region=$(field_of region "$answer")
  PROJECT=$project
  # The name typed is held to doctor's own answer: the two are the same value or
  # this check would read one installation's node and compare another's config,
  # which is the one thing it must never do. guard_sandbox's name guard above is
  # not enough on its own — a config answering `belong` is the same refusal as
  # typing it.
  if [ "$PROJECT" != "$INSTALLATION" ]; then
    fact installation FAIL "you named $INSTALLATION and fugaro doctor --json answers the $PROJECT installation: set FUGARO_CONFIG to the sandbox's own config, or name $PROJECT — this check never decides for itself which installation it converges"
    stop "the installation named ($INSTALLATION) is not the one the config in force describes ($PROJECT)"
  fi
  fact installation PASS "the config in force is the $name installation: project $PROJECT, region $region"
}

# step_config_before reads the local config as it stands BEFORE the converge:
# which version it is, whether the version-1 keys are still there, and how many
# members each list holds under the role the version's meaning gives them.
step_config_before() {
  local answer
  answer=$(reader config "$CONFIG") || stop "the config at $CONFIG is not a document this check can read"
  case "$answer" in *error=*) stop "the config at $CONFIG is not the YAML this check reads ($(field_of error "$answer")): name the installation's own local config" ;; esac
  local version cproject
  version=$(field_of version "$answer")
  cproject=$(field_of project "$answer")
  RTDB=$(field_of rtdb "$answer")
  CONFIG_VERSION=$version
  if [ "$cproject" != "$INSTALLATION" ]; then
    fact config FAIL "the config named is the $cproject installation's and you named $INSTALLATION: this check holds the file it migrates to the installation it converges"
    stop "the config at $CONFIG names project $cproject, not $INSTALLATION"
  fi
  case "$version" in
  1 | 2) ;;
  *)
    fact config FAIL "the config at $CONFIG states version ${version:-none}, and this check reads the version-1 and version-2 shapes: no other shape is one the migration writes"
    stop "the config at $CONFIG is version ${version:-none}, which this check does not know"
    ;;
  esac
  if [ -z "$RTDB" ]; then
    fact config FAIL "the config at $CONFIG carries no budget.rtdb_url: this installation has no Realtime Database, so it has no grants node to migrate into and the check's whole subject is absent"
    stop "the config at $CONFIG has no budget.rtdb_url — the grants node lives in the Realtime Database, and Check 401 needs an installation that has one"
  fi
  PRE_OP=$(field_of op "$answer")
  PRE_ADM=$(field_of adm "$answer")
  PRE_LEGACY=$(field_of legacy "$answer")
  PRE_ROLES_OP=$(field_of roles_op "$answer")
  PRE_ROLES_ADM=$(field_of roles_adm "$answer")
  local shape
  case "$version" in
  1) shape="version 1, so terraform.launchers names the operators ($PRE_OP) and terraform.operators the admins ($PRE_ADM): the pre-migration shape this check is about" ;;
  2) shape="version 2, so terraform.operators names the operators ($PRE_OP) and terraform.admins the admins ($PRE_ADM): the migration has already run on this file" ;;
  esac
  fact config PASS "$CONFIG is $shape"
}

# step_node_before reads the node as it stands before the converge: absent is
# the shape an installation that never migrated has, and present is an
# installation whose roles the node already is.
step_node_before() {
  if ! node_read "$SCRATCH/node-before.json"; then
    fact node-before FAIL "the grants node could not be read at ${RTDB%/}/$NODE_PATH: without the before, the check cannot say what the migration changed, so nothing below it is a verdict"
    stop "the grants node at ${RTDB%/}/$NODE_PATH could not be read with your own credentials. Fix that read (it is your ADC and the database's IAM) and run the check again: without it this check measures nothing"
  fi
  local answer doc
  answer=$(reader node <"$SCRATCH/node-before.json") || stop "the node document is not the shape this check reads"
  case "$answer" in *error=*) stop "the node at $NODE_PATH is not the document this check reads ($(field_of error "$answer")): is something else written at that path?" ;; esac
  doc=$(field_of doc "$answer")
  BEFORE_GRANTS=$(field_of grants "$answer")
  if [ "$doc" = absent ]; then
    fact node-before INFO "$NODE_PATH is absent: this installation has no grants node yet, so the next converge migrates the lists into it"
  else
    fact node-before INFO "$NODE_PATH holds $BEFORE_GRANTS grants as this run starts: $(field_of op "$answer") $ROLE_OPERATOR and $(field_of adm "$answer") $ROLE_ADMIN, so this installation migrated already and this run asserts the end state"
  fi
}

# step_update is the converge itself: the one run that migrates the lists. Its
# output is kept in a file and read for the migration's own roster line — the
# check believes that line about which grants were written and then holds the
# node to it, which is the whole method here.
step_update() {
  local status=0
  fugaro update --yes --config "$CONFIG" >"$SCRATCH/update.log" 2>&1 || status=$?
  if [ "$status" -ne 0 ]; then
    owner "the converge's own output, last lines (this is NOT in the block):"
    tail -n 12 "$SCRATCH/update.log" >&4 || true
    fact update FAIL "fugaro update exited $status; its output is in the terminal above and in the installation's own log. Nothing below it is a verdict about the migration"
    stop "the converge did not finish (exit $status): fix what it reported and run the check again — nothing was concluded about the migration"
  fi
  local answer
  answer=$(reader update <"$SCRATCH/update.log") || stop "the converge's output is not the text this check reads"
  MIGRATED=$(field_of migrated "$answer")
  DRIFT=$(field_of drift "$answer")
  MIG_OP=$(field_of migrated_op "$answer")
  MIG_ADM=$(field_of migrated_adm "$answer")
  MIG_GRANTS=$(field_of migrated_grants "$answer")
  MIG_ROLES_OP=$(field_of migrated_roles_op "$answer")
  MIG_ROLES_ADM=$(field_of migrated_roles_adm "$answer")
  if [ "$MIGRATED" = yes ]; then
    fact update PASS "the converge exited 0 and printed the migration's own line: $MIG_GRANTS grants written, $MIG_OP $ROLE_OPERATOR and $MIG_ADM $ROLE_ADMIN (the roster itself is not in this block)"
  elif [ "$CONFIG_VERSION" = 2 ]; then
    fact update INFO "the converge exited 0 and printed no migration line, which is what an installation whose node already exists says: the step derives the two lists from the node instead of writing it"
  elif [ "$DRIFT" = yes ]; then
    fact update PASS "the converge printed no migration line and said out loud that this machine's copy is not the installation's own outputs (accessStale): the installation's lists hold no user: member to write a grant from, so the node is theirs and this file's roster is not"
  elif [ "$PRE_ROLES_OP" = "" ] && [ "$PRE_ROLES_ADM" = "" ]; then
    fact update INFO "the converge exited 0 and printed no migration line: this file's two lists hold no user: member, so there is no grant to write and a node of no grants is no node"
  else
    fact update FAIL "the converge exited 0 but printed no migration line, on a version-1 config whose lists hold user: members: the step that reads the installation's lists and writes $NODE_PATH did not run, so the installation's roles are still the file's"
  fi
}

# step_node_after re-reads the node and holds it to three things: the roster the
# migration itself named, the rule that a grant is a `user:` member at project
# scope (F25), and — in the ordinary case of a machine whose file agrees with
# the installation — the file's own lists.
step_node_after() {
  if ! node_read "$SCRATCH/node-after.json"; then
    fact node-after FAIL "the grants node could not be read after the converge, so nothing is concluded about what it holds"
    stop "the grants node could not be read after the converge: run the check again (this is a read of $NODE_PATH with your own credentials)"
  fi
  local answer doc nonuser oscope other_roles
  answer=$(reader node <"$SCRATCH/node-after.json") || stop "the node document is not the shape this check reads"
  case "$answer" in *error=*) stop "the node at $NODE_PATH is not the document this check reads ($(field_of error "$answer"))" ;; esac
  doc=$(field_of doc "$answer")
  AFTER_GRANTS=$(field_of grants "$answer")
  AFTER_OP=$(field_of op "$answer")
  AFTER_ADM=$(field_of adm "$answer")
  nonuser=$(field_of nonuser "$answer")
  oscope=$(field_of other_scope "$answer")
  other_roles=$(field_of other_roles "$answer")
  AFTER_ROLES_OP=$(field_of roles_op "$answer")
  AFTER_ROLES_ADM=$(field_of roles_adm "$answer")
  if [ "$MIGRATED" = yes ]; then
    if [ "$AFTER_OP|$AFTER_ADM" != "$MIG_OP|$MIG_ADM" ]; then
      fact node-after FAIL "$NODE_PATH holds $AFTER_OP $ROLE_OPERATOR and $AFTER_ADM $ROLE_ADMIN grants, and the migration's own line named $MIG_OP and $MIG_ADM: the node is not what this run says it wrote"
    elif [ "$AFTER_ROLES_OP" != "$MIG_ROLES_OP" ] || [ "$AFTER_ROLES_ADM" != "$MIG_ROLES_ADM" ]; then
      fact node-after FAIL "$NODE_PATH holds $AFTER_GRANTS grants with the counts the migration printed and not its members: the run's own line and the node disagree about who the $MIG_GRANTS grants are"
    elif [ "$DRIFT" = yes ]; then
      fact node-after PASS "$NODE_PATH holds the $MIG_GRANTS grants the migration printed, and the converge said out loud that this machine's copy is not the installation's own lists (accessStale): the installation's roster won, which is the rule the step is for"
    elif [ "$AFTER_ROLES_OP" = "$PRE_ROLES_OP" ] && [ "$AFTER_ROLES_ADM" = "$PRE_ROLES_ADM" ]; then
      fact node-after PASS "$NODE_PATH holds the $MIG_GRANTS grants the migration printed, and they are this file's own lists member for member: $AFTER_OP $ROLE_OPERATOR and $AFTER_ADM $ROLE_ADMIN"
    else
      fact node-after FAIL "$NODE_PATH holds $AFTER_OP $ROLE_OPERATOR and $AFTER_ADM $ROLE_ADMIN grants and the migration printed the same counts, but the members are not this file's ($PRE_OP/$PRE_ADM) and the converge printed no drift line: the node and the file disagree with nothing said about it"
    fi
  elif [ "$doc" = absent ] && [ "$BEFORE_GRANTS" -eq 0 ]; then
    fact node-after PASS "$NODE_PATH is absent and it was absent before, on a config that already migrated: there is nothing to hold (a node of no grants is no node), and this run asserts the empty plan below"
  else
    fact node-after PASS "$NODE_PATH holds $AFTER_GRANTS grants ($AFTER_OP $ROLE_OPERATOR, $AFTER_ADM $ROLE_ADMIN), as it did before this run: the converge derived the two lists from them rather than writing them"
  fi
  LEGACY_SAID=no
  if [ "$nonuser" != 0 ]; then
    LEGACY_SAID=yes
    fact legacy FAIL "$NODE_PATH holds $nonuser grant(s) whose member is not a user: address: a grant is a person the CLI can match against a caller, so a group:, domain: or serviceAccount: member is a legacy IAM-only member (F25) and never a grant"
  fi
  if [ "$oscope" != 0 ]; then
    LEGACY_SAID=yes
    fact legacy FAIL "$NODE_PATH holds $oscope grant(s) at a scope other than project: the migration writes project scope, and a narrower one is another task's door, not this step's"
  fi
  if [ "$other_roles" != 0 ]; then
    LEGACY_SAID=yes
    fact legacy FAIL "$NODE_PATH holds $other_roles grant(s) whose role is neither $ROLE_OPERATOR nor $ROLE_ADMIN: the migration writes the two project roles alone"
  fi
}

# step_config_after reads the file again: the retired keys are gone, the two
# lists are the node's, and the version-1 spellings are never written again.
step_config_after() {
  local answer version
  answer=$(reader config "$CONFIG") || stop "the config at $CONFIG could not be read after the converge"
  case "$answer" in *error=*) stop "the config at $CONFIG is not the YAML this check reads after the converge" ;; esac
  version=$(field_of version "$answer")
  AFTER_FILE_OP=$(field_of roles_op "$answer")
  AFTER_FILE_ADM=$(field_of roles_adm "$answer")
  AFTER_FILE_LEGACY=$(field_of legacy "$answer")
  if [ "$version" != 2 ]; then
    fact config-after FAIL "the config at $CONFIG states version $version after the converge: the migration writes the file as version 2 (ConfigVersion), which is also the floor an older CLI refuses by name, pointing at brew upgrade and fugaro update"
    return 0
  fi
  if [ "$(field_of has_launchers "$answer")" = yes ]; then
    fact config-after FAIL "the config at $CONFIG still carries terraform.launchers: the version-1 key is retired by the migration and never written again — an older CLI reading it would read a roster with another meaning"
    return 0
  fi
  if [ "$AFTER_FILE_OP" != "$AFTER_ROLES_OP" ] || [ "$AFTER_FILE_ADM" != "$AFTER_ROLES_ADM" ]; then
    fact config-after FAIL "the config's lists ($(field_of op "$answer") $ROLE_OPERATOR, $(field_of adm "$answer") $ROLE_ADMIN) are not the node's ($AFTER_OP, $AFTER_ADM): the file is derived from the node, and a file that disagrees is what the next converge would apply"
    return 0
  fi
  local also=""
  if [ "$AFTER_FILE_LEGACY" != 0 ]; then
    also=" (and the $AFTER_FILE_LEGACY member(s) of the two lists that no grant can hold, which F25 keeps there)"
  fi
  fact config-after PASS "the config at $CONFIG is version 2, and both lists hold exactly the node's own members among the grants: terraform.operators $(field_of op "$answer") and terraform.admins $(field_of adm "$answer")$also. terraform.launchers is gone"
}

# step_legacy says why a member the node cannot hold is not a finding here: a
# group:, domain: or serviceAccount: member is not a grant, and it keeps its
# cloud grant by STAYING in the list it was written in, which is F25 — so the
# file must still carry it after the converge, and one that fell out is the
# revocation this rename must never cause.
step_legacy() {
  [ "${LEGACY_SAID:-no}" = no ] || return 0
  local after=${AFTER_FILE_LEGACY:-0}
  if [ "${PRE_LEGACY:-0}" != "$after" ]; then
    fact legacy FAIL "the two lists held ${PRE_LEGACY:-0} member(s) no grant can hold before the converge and hold $after after it: a group:, domain: or serviceAccount: member is a LEGACY IAM-ONLY member (F25) whose cloud grant survives by staying in the list it was written in, so one that is gone from the file is a revocation this rename must not cause"
    return 0
  fi
  case "$after" in
  0) fact legacy PASS "neither list carries a member no grant can hold: every member of this installation is a user: address, so the node is the whole of its roles" ;;
  *)
    fact legacy PASS "$after member(s) of the two lists are group:, domain: or serviceAccount: forms: they stay LEGACY IAM-ONLY members (F25) — the node holds none of them ($AFTER_GRANTS grants, all user:) and they keep their cloud grants by staying in the lists the derive writes"
    ;;
  esac
}

# step_plan is the second half of the check: the pure rename is nothing to
# apply. The Terraform variable and resource names did not change (D1589), so a
# converge whose lists moved from a file to the node must plan no change at all
# — and this is the only place that can say so, because it needs a real
# installation's state.
step_plan() {
  local status=0
  fugaro update --plan-only --config "$CONFIG" >"$SCRATCH/plan.log" 2>&1 || status=$?
  if [ "$status" -ne 0 ]; then
    owner "the plan-only run's own output, last lines (this is NOT in the block):"
    tail -n 12 "$SCRATCH/plan.log" >&4 || true
    fact plan FAIL "fugaro update --plan-only exited $status: the check cannot say whether the rename is a pure one"
    return 0
  fi
  local answer
  answer=$(reader update <"$SCRATCH/plan.log") || stop "the plan-only run's output is not the text this check reads"
  if [ "$(field_of no_changes "$answer")" = yes ]; then
    fact plan PASS "the converge after the migration printed \"No changes: the installation matches the plan.\": the rename is pure — the roles moved from the file to the node and Terraform has nothing to apply (D1589)"
  else
    owner "the plan-only run's own output, last lines (this is NOT in the block):"
    tail -n 12 "$SCRATCH/plan.log" >&4 || true
    fact plan FAIL "the converge after the migration planned a change: a pure rename must plan nothing (D1589 keeps the Terraform variable and resource names), so either the installation was not at the previous shape when the check started or the migration wrote something the apply can see"
  fi
}

# plan_of_the_run prints what a real run would do, for --dry-run: the guards
# above have run, and nothing below them runs at all.
plan_of_the_run() {
  printf '\nCHECK 401 DRY RUN — nothing was run\n' >&3
  printf 'the installation named:        %s\n' "$INSTALLATION" >&3
  printf 'the config named:              %s\n' "$CONFIG" >&3
  printf 'the node it reads:             %s/%s.json\n' "${RTDB%/}" "$NODE_PATH" >&3
  printf 'the converge it would run:     fugaro update --yes --config %s\n' "$CONFIG" >&3
  printf 'the plan it would then read:   fugaro update --plan-only --config %s\n' "$CONFIG" >&3
  printf 'the reads it makes first:      fugaro doctor --json, gcloud auth print-access-token, curl the node\n' >&3
  printf 'what it does not do:           launch a run, create a resource, write the node by itself, publish anything\n' >&3
  printf 'what it leaves behind:         the migration itself (the converge writes the grants node and the version-2 config), which is the point of the check and is not reverted\n' >&3
  printf 'CHECK 401 DRY RUN END (exit 0)\n' >&3
}

main() {
  need_tools
  scratch_up
  trap finish EXIT
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_sandbox
  step_installation
  step_config_before
  if [ -n "$DRY_RUN" ]; then
    REACHED_END=yes
    DRY_RAN=yes
    fact dry-run PASS "the guards passed and the plan of the run is printed above; no converge, no plan-only run and no read of the node was made"
    plan_of_the_run
    return 0
  fi
  step_node_before
  step_update
  step_node_after
  step_config_after
  step_legacy
  step_plan
  REACHED_END=yes
}

main "$@"
