#!/usr/bin/env bash
# Check 422 — `fugaro update` on an installation at the previous version
# converges with no manual step, as ONE script YOU run.
#
# It settles the live half of MC4 (docs/model-crafts/implementation-plan.md,
# Check 422; reconciliation F17, decision D1621) on one sandbox installation:
# `fugaro update` on a machine whose local config still carries the accounts
# design's keys (`claude_accounts`, `default_claude_account` and `stages`)
# converts them in place into the craft vocabulary — a typed credential of type
# claude-oauth per named account, and a craft access per craft whose stages all
# run on one of those accounts — and the run finishes with NOTHING left for the
# person: exit 0, no `left_for_you`, no failed stage. The older keys stay, which
# is the same decision's other half: the migration only ADDS, because the
# launch, the runner and `fugaro models` still read the account keys until MC5
# moves them, and a migration that emptied them first would leave an
# installation whose runs resolve no model and no credential at all.
#
# Three readings here are only a real installation's. The converge itself: no
# test in this tree runs Terraform, the rules deploy and a policy publish
# against a project (`TestUpdateConvergesPreviousInstallation` is the hermetic
# half of the same claim, and it drives the migration on a config in a temp
# directory). The idempotence: the second converge of the same installation
# must leave the migrated keys byte for byte as the first left them
# (`TestUpdateIsIdempotent` is the hermetic half). And the published policy: the
# check reads the node OUTSIDE the CLI, with your own credentials, before and
# after the converge, because what a release does to the node every run reads is
# not something the CLI's own account of itself can settle.
#
# What the check is NOT: it does not ask the node to carry the migrated
# credentials. No command of this release writes the crafts members of the
# published policy — `fugaro project policy publish` keeps whatever the node
# holds, as its own comment says — so a migration is a change to the local
# config. The policy row asserts that, which is why it is a row: a release that
# started rewriting the operator's published crafts from a local file would be
# the silent loosening this check exists to catch, and MC5 is where the node's
# writer is decided.
#
# Usage:      bash scripts/live-checks/check422.sh [--installation <project>] \
#               [--config <file>] [--timeout <seconds>] [--dry-run]
#             Nothing is required but the script's own path: the installation is
#             the one `fugaro doctor --json` answers (run it from a checkout of
#             the sandbox project, or with $FUGARO_PROJECT or $FUGARO_CONFIG
#             naming it), and the config is that installation's own local config
#             as the CLI's own --config default documents it
#             ($XDG_CONFIG_HOME/fugaro/projects/<name>.yaml, else under
#             ~/.config), or $FUGARO_CONFIG when it is set. `--installation`
#             holds doctor's own answer to the GCP project you name, and
#             `--config` names the file instead of deriving it: both are
#             compared against doctor and against the config's own `name` and
#             `gcp_project`, and a disagreement is a STOP before anything runs —
#             this check never decides for itself which installation it
#             converges.
# Needs:      bash, mktemp, python3, gcloud, curl and fugaro on PATH. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it. No python3 module beyond the
#             standard library is used.
# Where:      SANDBOX installations only, and never `belong`: the name is
#             refused, whether you typed it or doctor answers it. The check runs
#             `fugaro update --yes` TWICE on the installation you name — the
#             converge itself, with the Terraform plan and apply, the rules
#             deploy, the policy publish and the local config write that command
#             already does. It launches no run and makes no model call. The
#             second converge is the idempotence reading and is the same command
#             again; on a converged installation it plans no change.
# When:       Run it on an installation you have NOT yet converged with this
#             CLI: the migration is the first half of what is measured, and it
#             happens in the local config's first write. On an installation
#             whose config already holds the typed credentials and the crafts,
#             the check says so and asserts the end state and the idempotence
#             alone.
# Costs:      two converges of a sandbox installation. Nothing here is billable
#             on its own; an image rebuild, a deploy or a first policy publish
#             that an unrelated stage decides it wants is the converge's own
#             asking and `fugaro update --yes` covers it, so run the check where
#             that is acceptable. The plugin stage writes your own Claude Code
#             plugin at user scope, outside every checkout. The check itself
#             makes two reads of the published policy node and no other call.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed.
#             Nothing else was created — the converge is the installation's own
#             next `fugaro update` — so the `restore` row says that, says that
#             the migration STAYS (it is the installation's own state, and
#             `fugaro update --forget` is your own rollback, which this check
#             never runs), and says whether the directory really went.
# Output:     progress on stderr, ONE block on stdout headed CHECK 422 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one FAILed, 2 when the run could not tell (a guard stopped
#             it, a tool is missing, a converge did not run, a row could not be
#             measured).
#
# The block carries no secret and no account value. The local config holds
# credential NAMES and never a value (spec §1: a credential's value lives in a
# secret under a name derived from it), so what the rows print is names, counts
# and the update's own stage names; the access token the node read needs is used
# and dropped, and a trace of this run would print the node document in full —
# which is why guard_xtrace REFUSES a shell that arrived with tracing on.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had. BASH_XTRACEFD is unset
# so a trace redirected to a file this script knows nothing about cannot be
# reopened, and the flag is looked for in each of the three places it can sit.
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
# block and cleans up, so a stop still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# refact KEY PASS|FAIL|INFO "detail": replace a row the run has already recorded,
# rather than adding a second one. The block prints the FIRST line of a key, so a
# second reading of the same key would be invisible: guard_belong passes before
# anything is known, and doctor's own answer can name belong afterwards, and the
# block has to carry that answer and not the earlier one.
refact() { # refact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  local kept="$FACTS_FILE.kept"
  grep -v "^$1|" "$FACTS_FILE" >"$kept" 2>/dev/null || true
  mv "$kept" "$FACTS_FILE"
  fact "$1" "$2" "$3"
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the check ends as one that could not measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

# The published policy's node, from internal/projectpolicy/paths.go
# (PathShared): the project-wide document, which is the one whose `crafts`,
# `craft_credentials` and `craft_pools` members a run reads.
POLICY_PATH="config/policy/shared"

# The GCP project the owner's own installation lives in. Refused as a sandbox
# before anything else, in every spelling it can arrive in.
SANDBOX_REFUSED="belong"

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 422 needs bash"
  local t
  for t in mktemp python3 gcloud curl fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 422 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk422-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
WORKDIR=$PWD
DRY_RAN=""
FUGARO_BIN=fugaro
NAME=""
PROJECT=""
REGION=""
CONFIG=""
CONFIG_SOURCE=""
RTDB=""
READ_TIMEOUT=60
UPDATE_TIMEOUT=3600
# The config before the first converge, then after each of the two: how many of
# the accounts design's keys it holds, what the craft vocabulary it gained looks
# like, and a digest of the two blocks a second converge must not move.
PRE_SHAPE=""
PRE_ACCOUNTS=0
PRE_DEFAULT=""
PRE_STAGES=0
PRE_CREDS=0
PRE_CRAFTS=0
PRE_ACCOUNT_LIST=""
AFTER_CREDS=0
AFTER_CRAFT_ACCESSES=""
AFTER_CRED_SHA=""
AFTER_CRAFT_SHA=""
AFTER_OAUTH_LIST=""
SECOND_CREDS=0
SECOND_CRAFT_ACCESSES=""
SECOND_CRED_SHA=""
SECOND_CRAFT_SHA=""
# What each converge said about itself: its exit status, what it left for the
# person, and the lines it printed about keys it could not convert.
U1_STATUS=""
U1_LEFT=0
U1_FAILED=""
U1_STAGES=0
U1_NEEDS_YOU=0
U1_NOTES=0
U1_APPLIED=no
U1_CHANGES=""
U1_CONFIG=""
U1_BACKUP=""
U2_NOTES=0
U2_CHANGES=""
UPDATE_UNFINISHED=""
# The published policy read outside the CLI, before and after: whether a node is
# there at all, its generation, and a digest of the three members no command of
# this release writes.
PB_PUBLISHED=""
PB_VERSION=""
PB_SIG=""
PB_READ=""
PA_PUBLISHED=""
PA_VERSION=""
PA_SIG=""
CRAFTS_PROBLEMS=""
CRAFT_TABLE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk422.XXXXXXXX") || die "Check 422 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard keeps the removal from ever reaching the owner's home,
  # and reading it unguarded would abort cleanup under `set -u` on a machine
  # where HOME is unset, leaving the scratch directory behind.
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
    say "CHECK422_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents and the local config, in one program: a live check on
# the owner's machine has python3 and no jq, and a row that read a document with
# grep would report a key the release did not write the day the document is
# re-wrapped. The config is read by a scanner rather than a YAML implementation:
# it is a flat document the CLI itself writes, and this reads the blocks this
# check is about — presence, counts, the names inside them and a digest of each
# block's own text.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import hashlib
import json
import re
import sys


def text(path):
    # A document is named by the shell only when it is a file it keeps (the
    # config, a command's captured output); nothing is read from standard input.
    if path:
        with open(path, encoding="utf-8", errors="replace") as fh:
            return fh.read()
    return sys.stdin.read()


def out(pairs):
    for k, v in pairs:
        print("%s=%s" % (k, v))


def flat(value):
    return " ".join(str(value or "").split())


def yes(value):
    return "true" if value else "false"


def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def unquote(value):
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
        return value[1:-1]
    return value


def scan(document):
    """The local config's top-level blocks: name -> [(indent, text, is_item, raw)].

    A top-level key opens a block and every deeper line belongs to it; a
    top-level `key: value` is one entry of its own block, so the same code reads
    both spellings. This is the whole of the grammar read here, and the config
    is a document the CLI writes with one setting per key.
    """
    blocks = {}
    cur = None
    for raw in document.splitlines():
        line = raw.rstrip()
        if not line.strip() or line.strip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip(" \t"))
        stripped = line.strip()
        if indent == 0:
            if ":" not in stripped:
                cur = None
                continue
            key, _, value = stripped.partition(":")
            cur = key.strip()
            blocks.setdefault(cur, [])
            if value.strip():
                blocks[cur].append((1, value.strip(), False, value.strip()))
            continue
        if cur is None:
            continue
        blocks[cur].append((indent, stripped, stripped.startswith("- "), stripped))
    return blocks


def scalar(blocks, key):
    lines = blocks.get(key) or []
    if not lines:
        return ""
    return unquote(lines[0][1])


def entries(blocks, key):
    """One string per entry of a block: a list item or a map key at its top level.

    The entry's own deeper lines are folded into its text, which is what lets one
    regular expression find a `name` beside its `type` whatever the writer's
    indentation is.
    """
    body = [line for line in (blocks.get(key) or []) if line[0] > 0]
    if not body:
        return []
    level = min(line[0] for line in body)
    found = []
    for indent, stripped, is_item, _raw in body:
        if indent == level:
            found.append(stripped[2:] if is_item else stripped)
        elif found:
            found[-1] = found[-1] + " " + stripped
    return found


def entry_field(entry, field):
    """The value of one `field: value` inside an entry, flow style included.

    Read for the two fields this check compares -- a credential's name and type,
    and a craft's access -- and never as a general YAML reader: a value ends at a
    comma, a brace or a space, which is what both spellings of these two members
    give it.
    """
    match = re.search(r"(?:^|[{,\s])" + re.escape(field) + r":\s*([^,}\s]+)", entry)
    return unquote(match.group(1)) if match else ""


def block_digest(blocks, key):
    """A digest of a block's own text, with trailing spaces dropped.

    This is what makes a second converge's write comparable with the first's:
    both were written by the CLI, so the same keys must come out with the same
    bytes, and a re-encoding that moved a member would show here even when the
    reading above it still found the same names.
    """
    lines = [raw for _indent, _stripped, _item, raw in (blocks.get(key) or [])]
    return hashlib.sha256("\n".join(lines).encode("utf-8")).hexdigest()[:16]


def doctor(path=""):
    doc = json.loads(text(path))
    project = doc.get("project") or {}
    out([("name", flat(project.get("name"))),
         ("project", flat(project.get("gcp_project"))),
         ("region", flat(project.get("region")))])


def config(path=""):
    document = text(path)
    blocks = scan(document)
    accounts = entries(blocks, "claude_accounts")
    stages = entries(blocks, "stages")
    pools = entries(blocks, "pools")
    creds = entries(blocks, "credentials")
    oauth = [entry_field(e, "name") for e in creds if entry_field(e, "type") == "claude-oauth"]
    types = [entry_field(e, "type") for e in creds]
    crafts = []
    for entry in entries(blocks, "crafts"):
        name = entry.split(":", 1)[0].strip().strip("\"'")
        if name in ("coder", "review", "senior-review", "security-review"):
            crafts.append((name, entry_field(entry, "access")))
    accesses = [name + "=" + (access or "none") for name, access in crafts]
    with_access = [name for name, access in crafts if access]
    rtdb = ""
    for _indent, stripped, _item, _raw in blocks.get("budget") or []:
        field = entry_field(stripped, "rtdb_url")
        if field:
            rtdb = field
            break
    try:
        version = int(scalar(blocks, "version") or "0")
    except ValueError:
        version = 0
    out([
        ("version", version),
        ("name", scalar(blocks, "name")),
        ("project", scalar(blocks, "gcp_project")),
        ("rtdb", rtdb),
        ("claude_accounts", len(accounts)),
        ("accounts", ",".join(sorted(entry.split(":", 1)[0].strip().strip("\"'") for entry in accounts))),
        ("default", scalar(blocks, "default_claude_account")),
        ("stages", len(stages)),
        ("credentials", len(creds)),
        ("credential_names", ",".join(sorted(n for n in (entry_field(e, "name") for e in creds) if n))),
        ("credential_oauth", ",".join(sorted(n for n in oauth if n))),
        ("credential_types", ",".join(sorted(t for t in types if t))),
        ("credentials_digest", block_digest(blocks, "credentials")),
        ("crafts", len(with_access)),
        ("craft_accesses", ",".join(sorted(accesses))),
        ("crafts_digest", block_digest(blocks, "crafts")),
        ("pools", len(pools)),
    ])


def update(path=""):
    """What a converge said about itself, out of its --json document."""
    try:
        doc = json.loads(text(path))
    except ValueError:
        out([("ran", "no")])
        return
    failed = doc.get("failed") or {}
    left = doc.get("left_for_you") or []
    stages = doc.get("stages") or []
    counts = doc.get("changes") or {}
    needs = [s for s in stages if (s.get("state") or "") in ("needs-you", "blocked", "failed")]
    out([
        ("ran", "yes"),
        ("failed", flat(failed.get("stage"))),
        ("left", len(left)),
        ("left_stages", ",".join(sorted(flat(entry.get("stage")) for entry in left))),
        ("stages", len(stages)),
        ("needs_you", len(needs)),
        ("applied", yes(doc.get("applied"))),
        ("changes", "%d/%d/%d/%d/%d" % (
            counts.get("imports") or 0, counts.get("creates") or 0, counts.get("updates") or 0,
            counts.get("deletes") or 0, counts.get("replaces") or 0)),
        # The run's own `error` field is printed as `refused`: a key literally
        # named `error` would collide with the marker this reader prints when it
        # cannot read a document at all, and every caller reads that marker.
        ("refused", "yes" if doc.get("error") else "no"),
        ("config", flat(doc.get("config"))),
        ("backup", flat(doc.get("backup"))),
    ])


def crafts(path=""):
    doc = json.loads(text(path))
    out([("problems", len(doc.get("problems") or []))])
    for problem in doc.get("problems") or []:
        out([("problem", flat(problem))])
    for row in doc.get("crafts") or []:
        print("craft\t%s\t%s\t%s\t%s" % (
            flat(row.get("craft")), flat(row.get("model")),
            flat(row.get("model_from")), flat(row.get("access"))))


def policy(path=""):
    """The published node as the database itself holds it.

    The three craft members are digested as data, in one canonical form, so a
    converge that re-spells a member is not read as a change and one that moves,
    adds or drops a member is. The generation is printed and never digested: a
    policy migration is this release's own work and may bump it.
    """
    raw = text(path).strip()
    if raw in ("", "null"):
        out([("published", "false"), ("version", ""), ("digest", ""),
             ("credentials", 0), ("pools", 0), ("crafts", 0), ("accesses", "")])
        return
    doc = json.loads(raw) or {}
    crafts = doc.get("crafts") or {}
    canonical = json.dumps({
        "crafts": crafts,
        "craft_credentials": doc.get("craft_credentials") or [],
        "craft_pools": doc.get("craft_pools") or [],
    }, sort_keys=True, separators=(",", ":"))
    accesses = []
    if isinstance(crafts, dict):
        for name in sorted(crafts):
            block = crafts.get(name) or {}
            accesses.append(name + "=" + (flat(block.get("access")) or "none"))
    out([
        ("published", "true"),
        ("version", doc.get("version") if doc.get("version") is not None else ""),
        ("digest", hashlib.sha256(canonical.encode("utf-8")).hexdigest()[:16]),
        ("credentials", len(doc.get("craft_credentials") or [])),
        ("pools", len(doc.get("craft_pools") or [])),
        ("crafts", len(accesses)),
        ("accesses", ",".join(accesses)),
    ])


if __name__ == "__main__":
    try:
        which, *rest = sys.argv[1:]
        {"doctor": doctor, "config": config, "update": update,
         "crafts": crafts, "policy": policy}[which](*rest)
    except Exception as exc:  # noqa: BLE001 — the check reads the failure, not a traceback
        print("error=%s" % type(exc).__name__)
PY
  chmod 700 "$SCRATCH/read.py"
}

reader() { python3 "$SCRATCH/read.py" "$@"; }

# field FILE KEY — the value of one `key=value` line, "" when the line is not
# there.
field() { # field FILE KEY
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# in_list LIST NAME — whether the comma-joined LIST holds NAME whole, rather
# than as the beginning of a longer name.
in_list() { # in_list LIST NAME
  case ",$1," in *",$2,"*) return 0 ;; esac
  return 1
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound, stdout in $SCRATCH/$KEY.out and stderr in
# $SCRATCH/$KEY.err. The child execs, so the pid the bound kills IS the command:
# a hung cloud call would otherwise outlive the check that gave up on it. The
# ERR trap is dropped in it too: this check runs commands whose failing exit
# status is one of its own readings, and a trap inherited here would turn each
# of them into "an internal error" before the step could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! ticks=0 max=$((secs * 10))
  while kill -0 "$pid" 2>/dev/null; do
    [ "$ticks" -lt "$max" ] || break
    sleep 0.1
    ticks=$((ticks + 1))
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

# node_read OUTFILE: the published policy node, straight from the Realtime
# Database's REST API with an access token of your own. It is the one read here
# that is NOT the CLI's, which is why the check can say what the converge did to
# the node without believing the converge: the rules' `.read: false` binds
# client tokens, and an IAM-authenticated read of the database (yours, on a
# sandbox) is answered. The token lives in this function and in no fact.
node_read() { # node_read OUTFILE
  local out=$1 url token code
  url="${RTDB%/}/$POLICY_PATH.json"
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
    owner "the database answered $code: your own account may not read $POLICY_PATH in this installation"
    return 1
    ;;
  *)
    owner "the database answered $code for $POLICY_PATH"
    return 1
    ;;
  esac
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check422.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would print the local config and the published policy node, which are read here in full"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the local config or the policy node it reads"
}

# guard_agent_session: the check is the owner's. It runs a converge, whose cloud
# writes a coding agent's session is refused, and it reads the database with
# your own token. Refusing here rather than letting the converge fail one stage
# at a time is what makes the block say why: a session that ran this far would
# report those refusals as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 422 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_belong: the typed names are refused before any command runs. What doctor
# answers is held to the same refusal in step_installation, because the name you
# type and the installation the config in force describes are two things.
guard_belong() {
  case "${INSTALLATION:-}${CONFIG:-}" in
  *"$SANDBOX_REFUSED"*)
    fact belong FAIL "the installation or config named names $SANDBOX_REFUSED, which is the owner's own: Check 422 converges an installation's local config and publishes nothing, so it is a sandbox check"
    stop "Check 422 never runs against '$SANDBOX_REFUSED'. Name a sandbox installation: bash scripts/live-checks/check422.sh --installation <project>"
    ;;
  esac
  fact belong PASS "nothing named here mentions $SANDBOX_REFUSED: the installation this check converges is ${INSTALLATION:-the one fugaro doctor answers} and the account it runs as is yours"
}

# step_features: the release gate. The model-crafts vocabulary is what the
# migration writes and what the check reads back, and a build without it would
# converge an installation and migrate nothing — so the check stops naming what
# it is missing rather than reporting a converged installation as unconverted.
step_features() {
  local missing="" help=""
  if ! "$FUGARO_BIN" craft ls --help >/dev/null 2>&1; then
    missing="fugaro craft ls"
  elif ! help=$("$FUGARO_BIN" update --help 2>&1); then
    missing="fugaro update --help"
  elif ! printf '%s' "$help" | grep -q -- '--json'; then
    missing="fugaro update --json"
  fi
  if [ -n "$missing" ]; then
    fact features FAIL "this build has no '${missing}': the model-crafts vocabulary (credentials, pools and crafts, docs/model-crafts/implementation-plan.md MC1–MC7) and the converge's own result document are what this check reads, so there is no migration here for it to measure. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro has no '${missing}': this check needs the model-crafts vocabulary and 'update --json'"
  fi
  fact features PASS "fugaro craft ls is there and fugaro update reads its flags from a document this check can read (--json): the build carries the crafts this check reads back"
}

# ------------------------------------------------------------------- reading
# read_config FILE: the local config's own answer into $SCRATCH/config-$1.txt,
# which the callers below read with field. The file's name is kept in the
# scratch directory rather than in a variable so a step that needs it twice
# reads the same answer.
read_config() { # read_config TAG
  local tag=$1 answer
  answer=$(reader config "$CONFIG") || return 1
  printf '%s\n' "$answer" >"$SCRATCH/config-$tag.txt"
  case "$answer" in *error=*) return 1 ;; esac
  return 0
}

# step_installation asks doctor which installation is in force and holds it to
# the name typed, to the GCP project named, and to the config the check will
# converge. Nothing below can be right if these three disagree.
step_installation() {
  run_bounded "$READ_TIMEOUT" doctor "$WORKDIR" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    fact installation FAIL "fugaro doctor --json did not answer within ${READ_TIMEOUT}s: the check cannot tell which installation it is looking at"
    stop "fugaro doctor --json timed out after ${READ_TIMEOUT}s. Run it yourself in ${WORKDIR} and fix what it reports"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    sed -n '1,5p' "$SCRATCH/doctor.err" >&4 || true
    fact installation FAIL "fugaro doctor --json exited $BOUNDED_STATUS, so this run cannot tell which installation it is looking at"
    stop "fugaro doctor --json did not answer (exit $BOUNDED_STATUS): the check needs your own credentials and a local config for the installation. Fix what it reports and run the check again"
  fi
  local answer
  answer=$(reader doctor "$SCRATCH/doctor.out") || stop "the answer to fugaro doctor --json is not the document this check reads (is this fugaro too old to print --json?)"
  case "$answer" in *error=*) stop "fugaro doctor --json printed something this check cannot read: run it yourself and see" ;; esac
  printf '%s\n' "$answer" >"$SCRATCH/doctor.txt"
  NAME=$(field "$SCRATCH/doctor.txt" name)
  PROJECT=$(field "$SCRATCH/doctor.txt" project)
  REGION=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$PROJECT" ] || [ -z "$NAME" ]; then
    fact installation FAIL "fugaro doctor --json answers no installation (project '${PROJECT:-none}', name '${NAME:-none}'): there is no local config for this check to converge"
    stop "fugaro doctor --json names no installation. Run it yourself (from a checkout of the sandbox project, or with \$FUGARO_PROJECT or \$FUGARO_CONFIG set) and fix what it reports"
  fi
  case "$PROJECT$NAME" in
  *"$SANDBOX_REFUSED"*)
    refact belong FAIL "the installation in force is $NAME ($PROJECT), which names $SANDBOX_REFUSED: Check 422 converges an installation's local config and is a sandbox check"
    stop "the config in force describes '$SANDBOX_REFUSED'. Set \$FUGARO_CONFIG or \$FUGARO_PROJECT to a sandbox installation: bash scripts/live-checks/check422.sh --installation <project>"
    ;;
  esac
  if [ -n "$INSTALLATION" ] && [ "$PROJECT" != "$INSTALLATION" ]; then
    fact installation FAIL "you named the installation $INSTALLATION and fugaro doctor --json answers $PROJECT: this check never decides for itself which installation it converges"
    stop "the installation named ($INSTALLATION) is not the one the config in force describes ($PROJECT). Set \$FUGARO_CONFIG to the sandbox's own config, or name $PROJECT"
  fi
  if [ -n "$CONFIG" ]; then
    CONFIG_SOURCE="named with --config (or by \$FUGARO_CONFIG)"
  else
    CONFIG="${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/$NAME.yaml"
    CONFIG_SOURCE="derived from the installation's own name, as the CLI's own --config default documents it"
  fi
  if [ ! -f "$CONFIG" ]; then
    fact installation FAIL "the local config $CONFIG is not a file, so there is nothing for this check to converge"
    stop "there is no local config at $CONFIG ($CONFIG_SOURCE). Name the sandbox installation's own config: bash scripts/live-checks/check422.sh --installation $PROJECT --config <file>"
  fi
  if ! read_config before; then
    fact installation FAIL "the config at $CONFIG is not a document this check can read"
    stop "the local config at $CONFIG is not the YAML this check reads: name the installation's own config with --config"
  fi
  local cname cproject
  cname=$(field "$SCRATCH/config-before.txt" name)
  cproject=$(field "$SCRATCH/config-before.txt" project)
  if [ "$cname" != "$NAME" ] || [ "$cproject" != "$PROJECT" ]; then
    fact installation FAIL "the config at $CONFIG is the ${cname:-?} installation's (project ${cproject:-?}) and fugaro doctor answers $NAME ($PROJECT): this check holds the file it converges to the installation doctor names"
    stop "the config at $CONFIG names project ${cproject:-?}, not $PROJECT"
  fi
  fact installation PASS "the installation in force is $NAME ($PROJECT, region ${REGION:-?}) and its local config is $CONFIG ($CONFIG_SOURCE): doctor's answer, the name typed and the file's own $cname all agree"
}

# step_config_before reads the shape the migration has to convert: the accounts
# design's keys still in the file, and the craft vocabulary it does not yet
# hold. Both are reported as they are — a converged installation says so and the
# rows below assert the end state alone.
step_config_before() {
  local version accounts default stages creds crafts pools shape
  version=$(field "$SCRATCH/config-before.txt" version)
  accounts=$(field "$SCRATCH/config-before.txt" claude_accounts)
  default=$(field "$SCRATCH/config-before.txt" default)
  stages=$(field "$SCRATCH/config-before.txt" stages)
  creds=$(field "$SCRATCH/config-before.txt" credentials)
  crafts=$(field "$SCRATCH/config-before.txt" crafts)
  pools=$(field "$SCRATCH/config-before.txt" pools)
  RTDB=$(field "$SCRATCH/config-before.txt" rtdb)
  PRE_ACCOUNTS=$accounts
  PRE_DEFAULT=$default
  PRE_STAGES=$stages
  PRE_CREDS=$creds
  PRE_CRAFTS=$crafts
  PRE_ACCOUNT_LIST=$(field "$SCRATCH/config-before.txt" accounts)
  if [ "$version" != 2 ]; then
    fact config-before FAIL "the config at $CONFIG states version ${version:-none}, and this release writes version 2: an older CLI refuses a newer file by name, and a converge on this one is not the migration this check measures"
    stop "the config at $CONFIG is version ${version:-none}, which is not the version this release writes"
  fi
  if [ "$accounts" != 0 ] || [ -n "$default" ] || [ "$stages" != 0 ]; then
    PRE_SHAPE=previous
    shape="the accounts design's keys are still here — $accounts named Claude account(s), $stages stage key(s) and default_claude_account ${default:-not set} — and the craft vocabulary is not: $creds typed credential(s), $crafts craft access(es). This is the shape this check exists to converge (MC4, F17, D1621)"
  else
    PRE_SHAPE=converged
    shape="none of the accounts design's keys are here (no claude_accounts, no default_claude_account, no stages): this installation converged already, so the converge below asserts the end state and the second one asserts the idempotence, instead of watching the migration run"
  fi
  fact config-before PASS "$CONFIG is version 2 and $shape"
}

# step_bound says the spend this check is about to commit, before it commits it.
# `--yes` is the owner's answer to it, passed to the converge itself; the check
# asks for no confirmation of its own, because the owner runs it by hand and its
# header says what it does.
step_bound() {
  local second="a second converge of the same installation, which must plan nothing"
  fact bound PASS "TWO converges of the $NAME installation ($PROJECT) — the whole of what fugaro update does: the Terraform plan and apply, the rules deploy, the published policy, the job images behind their own confirmation and the local config. Nothing here is billable on its own; an image rebuild or a deploy an unrelated stage decides it wants is the converge's own asking, and the --yes this check passes covers it. Then $second. No run is launched and no model call is made"
}

# step_update is the converge itself, read as data. It is run with --json so the
# check can read what the run says about itself — what it left for the person,
# which stage failed, which config file it wrote — and with the config named, so
# the file the check reads before and after is the file the run wrote.
step_update() {
  local secs=$UPDATE_TIMEOUT
  run_bounded "$secs" update1 "$WORKDIR" "$FUGARO_BIN" update --yes --json --config "$CONFIG"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured update "fugaro update did not finish within ${secs}s and was killed: nothing below is a verdict about the migration. Raise the bound with --timeout <seconds> and run the check again"
    stop "the converge did not finish within ${secs}s (the check killed it). Raise the bound with --timeout and run the check again — nothing was concluded about the migration"
  fi
  U1_STATUS=$BOUNDED_STATUS
  local answer
  answer=$(reader update "$SCRATCH/update1.out") || stop "the converge's --json document is not one this check can read"
  case "$answer" in *error=*) stop "the converge's --json document is not the shape this check reads: run 'fugaro update --yes --json' yourself and see" ;; esac
  printf '%s\n' "$answer" >"$SCRATCH/update1.txt"
  if [ "$(field "$SCRATCH/update1.txt" ran)" != yes ]; then
    unmeasured update "the converge printed no --json document this check can read, so what it left for the person is not measured: run 'fugaro update --yes --json' at your terminal and see"
    stop "the converge printed no --json document: nothing was concluded about the migration"
  fi
  U1_LEFT=$(field "$SCRATCH/update1.txt" left)
  U1_FAILED=$(field "$SCRATCH/update1.txt" failed)
  U1_STAGES=$(field "$SCRATCH/update1.txt" stages)
  U1_NEEDS_YOU=$(field "$SCRATCH/update1.txt" needs_you)
  U1_APPLIED=$(field "$SCRATCH/update1.txt" applied)
  U1_CHANGES=$(field "$SCRATCH/update1.txt" changes)
  U1_CONFIG=$(field "$SCRATCH/update1.txt" config)
  U1_BACKUP=$(field "$SCRATCH/update1.txt" backup)
  # The lines a run prints about the keys it could not convert go to the run's
  # own stream, which --json sends to stderr: the document carries the stages and
  # not these notes.
  U1_NOTES=$(grep -c '^note: ' "$SCRATCH/update1.err" 2>/dev/null || true)
  U1_NOTES=${U1_NOTES:-0}
  if [ "${U1_STATUS}" -ne 0 ]; then
    owner "the converge's own output, last lines (this is NOT in the block):"
    tail -n 12 "$SCRATCH/update1.err" >&4 || true
    fact update FAIL "fugaro update exited $U1_STATUS${U1_FAILED:+, at the stage $U1_FAILED}: its output is in the terminal above"
    if [ "$U1_LEFT" = 0 ]; then
      stop "the converge did not finish (exit $U1_STATUS): fix what it reported and run the check again — nothing was concluded about the migration"
    fi
    # A converge that exited non-zero WITH something left for the person is the
    # case the next row exists for, so it is left to that row to say and the run
    # stops under it rather than here.
    return 0
  fi
  local wrote="it wrote $U1_CONFIG"
  if [ -z "$U1_CONFIG" ]; then
    if [ -n "$U1_BACKUP" ]; then
      wrote="it wrote no config file and backed one up"
    else
      wrote="it wrote no config file (there was nothing to change)"
    fi
  elif [ "$U1_CONFIG" != "$CONFIG" ]; then
    fact update FAIL "the converge exited 0 and wrote the local config $U1_CONFIG, and this check named $CONFIG: the rows below read the file the run did not write"
    stop "the converge wrote $U1_CONFIG, not the $CONFIG this check named. Run it with --config $U1_CONFIG, or find out which installation that file is"
  fi
  fact update PASS "the converge exited 0 over $U1_STAGES stage(s) and $wrote, from $WORKDIR: $(pre_shape_sentence) Its Terraform plan was $U1_CHANGES (imports/creates/updates/deletes/replaces) and applied=$U1_APPLIED"
}

# pre_shape_sentence spells what there was to migrate, so the update row reads
# the same way on a converged installation and on one at the previous version.
pre_shape_sentence() {
  if [ "$PRE_SHAPE" = previous ]; then
    printf 'it converted an installation whose config still held the accounts design: %s named account(s), %s stage key(s), and %s typed credential(s) before it' \
      "$PRE_ACCOUNTS" "$PRE_STAGES" "$PRE_CREDS"
  else
    printf 'the config held none of the accounts design, so the run had no migration to make'
  fi
}

# step_no_step_left is THE reading of this check: the converge finished with
# nothing left for the person. A step in left_for_you is a manual step, and the
# hard rule is that `fugaro update` is the whole upgrade.
step_no_step_left() {
  local where
  if [ "$U1_LEFT" != 0 ]; then
    where=$(field "$SCRATCH/update1.txt" left_stages)
    UPDATE_UNFINISHED=yes
    fact no-step-left FAIL "the converge left $U1_LEFT step(s) for you${where:+, at the stage(s) $where}: 'fugaro update' is the whole upgrade and must leave nothing for a person (an update that needs another command is the release's own bug). Read the step(s) where the run printed them, finish them, and run the check again"
    return 0
  fi
  if [ "$(field "$SCRATCH/update1.txt" refused)" = yes ]; then
    fact no-step-left FAIL "the converge refused before any stage ran and printed no stage account: read the refusal where the run printed it"
    return 0
  fi
  if [ "$U1_NEEDS_YOU" != 0 ]; then
    fact no-step-left FAIL "$U1_NEEDS_YOU stage(s) of $U1_STAGES ended needs-you/blocked/failed while left_for_you is empty: the two halves of the run's own account disagree, so nothing is concluded about what it left"
    return 0
  fi
  fact no-step-left PASS "the converge exited 0 with left_for_you empty and no stage needing you ($U1_STAGES stage(s) ran): the installation is at this release with no manual step, which is the whole of what Check 422 measures"
}

# step_crafts reads the crafts back through the CLI: no craft may have a
# problem, and every access the migrated config states must be the one the CLI
# resolves. This is the half the config's own text cannot show — that the keys
# the migration wrote are keys this build's resolver reads.
step_crafts() {
  run_bounded "$READ_TIMEOUT" crafts "$WORKDIR" "$FUGARO_BIN" craft ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured crafts "fugaro craft ls --json did not answer (exit $BOUNDED_STATUS): whether the crafts the migration wrote resolve is not measured"
    return 0
  fi
  local answer
  answer=$(reader crafts "$SCRATCH/crafts.out") || {
    unmeasured crafts "the answer to fugaro craft ls --json is not a document this check can read"
    return 0
  }
  case "$answer" in
  *error=*)
    unmeasured crafts "fugaro craft ls --json printed something this check cannot read"
    return 0
    ;;
  esac
  printf '%s\n' "$answer" >"$SCRATCH/crafts.txt"
  CRAFTS_PROBLEMS=$(field "$SCRATCH/crafts.txt" problems)
  CRAFT_TABLE=$(awk -F'\t' '$1 == "craft" { printf "%s%s=%s", (n++ ? ", " : ""), $2, ($5 == "" ? "none" : $5) }' "$SCRATCH/crafts.txt")
  if [ "$CRAFTS_PROBLEMS" != 0 ]; then
    local first
    first=$(awk -F'\t' '$1 == "problem" { print $2; exit }' "$SCRATCH/crafts.txt")
    fact crafts FAIL "fugaro craft ls reports $CRAFTS_PROBLEMS problem(s) after the converge, the first being: ${first:-unnamed}. The keys the migration wrote are keys this build cannot resolve"
    return 0
  fi
  # Every access the config states must be the one the CLI resolves for that
  # craft, when the configuration names the craft itself. The craft ls table has
  # one row per craft with its effective access, so the comparison needs a lookup
  # by name.
  local mismatch="" pair name want got
  for pair in $(printf '%s' "$AFTER_CRAFT_ACCESSES" | tr ',' ' '); do
    name=${pair%%=*}
    want=${pair#*=}
    [ "$want" != none ] || continue
    got=$(awk -F'\t' -v n="$name" '$1 == "craft" && $2 == n { print $5; exit }' "$SCRATCH/crafts.txt")
    if [ "$got" != "$want" ]; then
      mismatch="$mismatch;$name"
    fi
  done
  if [ -n "$mismatch" ]; then
    fact crafts FAIL "the config's own crafts do not resolve as they are written: ${mismatch#;} resolves to another access in fugaro craft ls --json (the table is $CRAFT_TABLE), so the keys the migration wrote are not the ones a run would use"
    return 0
  fi
  local read_back=""
  if [ "$PRE_SHAPE" = previous ] && [ "$AFTER_CRAFTS" = 0 ]; then
    read_back=" The migration wrote no craft access, which is what a config whose crafts' stages name no account (or name two of them) gives, and the converge's own notes say which"
  fi
  fact crafts PASS "fugaro craft ls reports no problem and answers for every craft — $CRAFT_TABLE — and each of the $AFTER_CRAFTS craft access(es) the config states is the one the CLI resolves for that craft: the keys the migration wrote are keys this build reads$read_back"
}

# step_policy_before reads the published node outside the CLI. It records what
# it found and decides nothing: the row is the comparison, and it is written
# after the converge.
step_policy_before() {
  if [ -z "$RTDB" ]; then
    PB_READ="no backend"
    return 0
  fi
  if ! node_read "$SCRATCH/policy-before.json"; then
    PB_READ="unreadable"
    return 0
  fi
  local answer
  answer=$(reader policy "$SCRATCH/policy-before.json") || {
    PB_READ="unreadable"
    return 0
  }
  case "$answer" in
  *error=*)
    PB_READ="unreadable"
    return 0
    ;;
  esac
  printf '%s\n' "$answer" >"$SCRATCH/policy-before.txt"
  PB_READ="ok"
  PB_PUBLISHED=$(field "$SCRATCH/policy-before.txt" published)
  PB_VERSION=$(field "$SCRATCH/policy-before.txt" version)
  PB_SIG=$(field "$SCRATCH/policy-before.txt" digest)
}

# step_policy is what the converge did to the node every run reads: the migration
# writes the local config, and no command of this release writes the crafts
# members of the published policy (fugaro project policy publish keeps whatever
# the node holds). So the reading is that the node still holds what it held, and
# that a node that was published is still published — a converge that dropped it
# would leave every run in the installation reading no policy at all.
step_policy() {
  case "$PB_READ" in
  "no backend")
    fact policy INFO "this installation has no budget.rtdb_url, so it has no Realtime Database and no published policy node: there is no node for the converge to change"
    return 0
    ;;
  unreadable)
    unmeasured policy "the published policy node at ${RTDB%/}/$POLICY_PATH could not be read before the converge with your own credentials (gcloud auth print-access-token, then the database's REST API): whether the converge changed it is not measured"
    return 0
    ;;
  esac
  if ! node_read "$SCRATCH/policy-after.json"; then
    unmeasured policy "the published policy node at ${RTDB%/}/$POLICY_PATH could not be read after the converge: it was there before (generation ${PB_VERSION:-none}), so whether it survived is not measured"
    return 0
  fi
  local answer
  answer=$(reader policy "$SCRATCH/policy-after.json") || {
    unmeasured policy "the node document at $POLICY_PATH is not one this check can read"
    return 0
  }
  case "$answer" in
  *error=*)
    unmeasured policy "the node document at $POLICY_PATH is not the shape this check reads"
    return 0
    ;;
  esac
  printf '%s\n' "$answer" >"$SCRATCH/policy-after.txt"
  PA_PUBLISHED=$(field "$SCRATCH/policy-after.txt" published)
  PA_VERSION=$(field "$SCRATCH/policy-after.txt" version)
  PA_SIG=$(field "$SCRATCH/policy-after.txt" digest)
  if [ "$PB_PUBLISHED" = true ] && [ "$PA_PUBLISHED" != true ]; then
    fact policy FAIL "the published policy node at $POLICY_PATH was there before the converge (generation ${PB_VERSION:-none}) and is gone after it: every run in this installation reads its policy from that node, so a converge that removes it leaves them unbounded"
    return 0
  fi
  if [ "$PA_PUBLISHED" != true ]; then
    fact policy PASS "there is no published policy node at $POLICY_PATH before or after the converge, so the converge had none to change (this installation's policy is published by another command, and this check measures the migration and not that)"
    return 0
  fi
  if [ "$PA_SIG" != "$PB_SIG" ]; then
    fact policy FAIL "the published policy node's craft members (crafts, craft_credentials, craft_pools) changed across the converge: generation ${PB_VERSION:-none} → ${PA_VERSION:-none}. No command of this release writes them — a publish keeps the node's own — so the converge rewrote operator-written policy on a node every run reads. Read both with 'fugaro project policy show' before you converge again"
    return 0
  fi
  local moved=""
  if [ "$PA_VERSION" != "$PB_VERSION" ]; then
    moved=", and its generation moved ${PB_VERSION:-none} → ${PA_VERSION:-none}, which is the policy migration this release runs in place"
  fi
  fact policy PASS "the published policy node at $POLICY_PATH survived the converge with its craft members unchanged ($(field "$SCRATCH/policy-after.txt" crafts) craft(s), $(field "$SCRATCH/policy-after.txt" credentials) credential(s), $(field "$SCRATCH/policy-after.txt" pools) pool(s) — ${PA_SIG})$moved, read outside the CLI with your own credentials: the migration is a change to the local config and not to the node, which is what this release's publish does with the members it does not write"
}

# step_config_after holds the file to what the migration is: it ADDS the typed
# credentials and the craft accesses and removes nothing. Every account whose
# credential it could not write must be named by the run's own note, so an
# account that silently vanished is a finding rather than a note nobody read.
step_config_after() {
  if ! read_config after; then
    fact config-after FAIL "the config at $CONFIG could not be read after the converge, so nothing is concluded about what the migration wrote"
    return 0
  fi
  AFTER_CREDS=$(field "$SCRATCH/config-after.txt" credentials)
  AFTER_CRAFT_ACCESSES=$(field "$SCRATCH/config-after.txt" craft_accesses)
  AFTER_CRED_SHA=$(field "$SCRATCH/config-after.txt" credentials_digest)
  AFTER_CRAFT_SHA=$(field "$SCRATCH/config-after.txt" crafts_digest)
  AFTER_OAUTH_LIST=$(field "$SCRATCH/config-after.txt" credential_oauth)
  AFTER_CRAFTS=$(field "$SCRATCH/config-after.txt" crafts)
  if [ "$PRE_SHAPE" = converged ]; then
    fact config-after INFO "the config held the craft vocabulary before the converge and holds it after ($AFTER_CREDS typed credential(s), $AFTER_CRAFTS craft access(es)): there was no migration to watch, and the second converge below is what asserts this file does not move"
    return 0
  fi
  if [ "$AFTER_CREDS" = 0 ]; then
    fact config-after FAIL "the config at $CONFIG still holds no typed credential after the converge, although it held the accounts design's keys before it ($PRE_ACCOUNTS named account(s)): fugaro update is where that conversion happens (MC4, F17), so the migration did not run"
    return 0
  fi
  local account missing="" named_by_note=""
  for account in $(printf '%s' "$PRE_ACCOUNT_LIST" | tr ',' ' '); do
    [ -n "$account" ] || continue
    if in_list "$AFTER_OAUTH_LIST" "$account"; then
      continue
    fi
    if grep -qF "the account ${account}," "$SCRATCH/update1.err" 2>/dev/null; then
      named_by_note="${named_by_note}${named_by_note:+, }${account}"
      continue
    fi
    missing="${missing}${missing:+, }${account}"
  done
  if [ -n "$missing" ]; then
    fact config-after FAIL "the accounts $missing hold no credential of type claude-oauth after the converge and the converge printed no note about them: an account the migration cannot convert is left alone AND named on every write of the file (MC4), so an account that is neither converted nor named was dropped silently"
    return 0
  fi
  local notes_said=""
  if [ "$U1_NOTES" != 0 ]; then
    notes_said=" The converge printed $U1_NOTES note line(s) about keys it could not convert — read them at your terminal: they are the keys the migration leaves to you, and it names them on every write of this file"
  fi
  local crafts_said=""
  if [ "$AFTER_CRAFTS" = 0 ] && { [ -n "$PRE_DEFAULT" ] || [ "$PRE_STAGES" != 0 ]; }; then
    crafts_said=" No craft access was written, which this config's own stages explain (a craft whose stages name two accounts holds one access, so none is written)"
  fi
  fact config-after PASS "the config at $CONFIG now holds $AFTER_CREDS typed credential(s) — one claude-oauth credential per account of the accounts design${named_by_note:+ (the account(s) $named_by_note left unconverted and named by a note of the run itself)} — and $AFTER_CRAFTS craft access(es) ($AFTER_CRAFT_ACCESSES). The older keys are still there, which is the same decision's other half: the migration only ADDS (its readers move to the crafts in MC5), so nothing a run reads today changed meaning.$crafts_said$notes_said"
}

# step_second_update is the idempotence: the same command again must leave the
# migrated keys exactly as the first left them, say nothing new about what it
# cannot convert, and plan no change. A migration that rewrote its own output on
# the second pass would make every later converge a diff.
step_second_update() {
  local secs=$UPDATE_TIMEOUT
  run_bounded "$secs" update2 "$WORKDIR" "$FUGARO_BIN" update --yes --json --config "$CONFIG"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured second-update "the second fugaro update did not finish within ${secs}s and was killed: the idempotence of the migration is not measured"
    return 0
  fi
  local status=$BOUNDED_STATUS
  if [ "$status" -ne 0 ]; then
    local at=""
    local refused
    refused=$(reader update "$SCRATCH/update2.out") || true
    printf '%s\n' "$refused" >"$SCRATCH/update2.txt"
    at=$(field "$SCRATCH/update2.txt" failed)
    owner "the second converge's own output, last lines (this is NOT in the block):"
    tail -n 12 "$SCRATCH/update2.err" >&4 || true
    fact second-update FAIL "the second fugaro update exited $status${at:+, at the stage $at}: a converged installation converges again with the same command, so this is either a finding about the second run or an installation that was still moving"
    return 0
  fi
  local answer
  answer=$(reader update "$SCRATCH/update2.out") || {
    unmeasured second-update "the second converge's --json document is not one this check can read"
    return 0
  }
  printf '%s\n' "$answer" >"$SCRATCH/update2.txt"
  if [ "$(field "$SCRATCH/update2.txt" ran)" != yes ]; then
    unmeasured second-update "the second converge printed no --json document this check can read: the idempotence of the migration is not measured"
    return 0
  fi
  local left
  left=$(field "$SCRATCH/update2.txt" left)
  if [ "$left" != 0 ]; then
    fact second-update FAIL "the second converge left $left step(s) for you (stage(s) $(field "$SCRATCH/update2.txt" left_stages)): the first converge said it left none, so the installation was not converged when the check read it"
    return 0
  fi
  if ! read_config second; then
    fact second-update FAIL "the config at $CONFIG could not be read after the second converge"
    return 0
  fi
  SECOND_CREDS=$(field "$SCRATCH/config-second.txt" credentials)
  SECOND_CRAFT_ACCESSES=$(field "$SCRATCH/config-second.txt" craft_accesses)
  SECOND_CRED_SHA=$(field "$SCRATCH/config-second.txt" credentials_digest)
  SECOND_CRAFT_SHA=$(field "$SCRATCH/config-second.txt" crafts_digest)
  U2_NOTES=$(grep -c '^note: ' "$SCRATCH/update2.err" 2>/dev/null || true)
  U2_NOTES=${U2_NOTES:-0}
  U2_CHANGES=$(field "$SCRATCH/update2.txt" changes)
  if [ "$SECOND_CRED_SHA" != "$AFTER_CRED_SHA" ]; then
    fact second-update FAIL "the credentials block of $CONFIG is not the one the first converge wrote: the same keys came out of the second write with different text ($(field "$SCRATCH/config-second.txt" credential_names) vs $(field "$SCRATCH/config-after.txt" credential_names)), so the migration is not idempotent"
    return 0
  fi
  if [ "$SECOND_CRAFT_SHA" != "$AFTER_CRAFT_SHA" ]; then
    fact second-update FAIL "the crafts block of $CONFIG is not the one the first converge wrote: the second write moved it ($SECOND_CRAFT_ACCESSES vs $AFTER_CRAFT_ACCESSES), so the migration is not idempotent"
    return 0
  fi
  if [ "$U2_NOTES" != "$U1_NOTES" ]; then
    fact second-update FAIL "the second converge printed $U2_NOTES note line(s) about keys it cannot convert and the first printed $U1_NOTES: what a config leaves unconverted is a state of the file, and the same file says the same thing on every write (MC4)"
    return 0
  fi
  local planned="the second converge's planned change was $U2_CHANGES (imports/creates/updates/deletes/replaces)"
  if [ "$U2_CHANGES" = "0/0/0/0/0" ]; then
    planned="the second converge planned no change at all ($U2_CHANGES), which is what a converged installation gives"
  fi
  fact second-update PASS "the second converge exited 0 with left_for_you empty, and $CONFIG came out of it byte for byte as the first left it: $SECOND_CREDS typed credential(s) and $SECOND_CRAFT_ACCESSES craft access(es) under the same text, and the same $U2_NOTES note line(s). $planned"
}

# plan prints what a real run would do. It is written after doctor and the
# config read, which are reads: `--dry-run` makes no cloud call, converges
# nothing and reads no node.
plan() {
  printf '\nCHECK 422 DRY RUN — nothing was converged\n' >&3
  printf 'the installation:            %s (%s, region %s)\n' "$NAME" "$PROJECT" "${REGION:-?}" >&3
  printf 'the local config:            %s (%s)\n' "$CONFIG" "$CONFIG_SOURCE" >&3
  printf 'its shape before a converge: %s\n' "$PRE_SHAPE" >&3
  printf 'the migration it would run:  %s typed credential(s) and %s craft access(es) today, over the accounts design'"'"'s %s account(s) and %s stage key(s)\n' \
    "$PRE_CREDS" "$PRE_CRAFTS" "$PRE_ACCOUNTS" "$PRE_STAGES" >&3
  printf 'the converges it would run:  fugaro update --yes --json --config %s (twice: the second is the idempotence reading)\n' "$CONFIG" >&3
  printf 'the node it would read:      %s/%s.json, before and after, with your own credentials\n' "${RTDB%/}" "$POLICY_PATH" >&3
  printf 'the reads it makes first:    fugaro doctor --json, fugaro craft ls --json\n' >&3
  printf 'what it does not do:         launch a run, make a model call, write a credential value, publish a policy by itself\n' >&3
  printf 'what it leaves behind:       the converge itself — the migrated local config and whatever the installation'"'"'s own update applies — which is the point of the check and is not reverted\n' >&3
  printf 'CHECK 422 DRY RUN END (exit 0)\n' >&3
  local key
  for key in update no-step-left crafts policy config-after second-update; do
    fact "$key" INFO "planned: a real converge of $PROJECT measures this row; --dry-run did not"
  done
  fact dry-run PASS "the guards passed, doctor and the local config were read, and the plan of the run is printed above; no converge, no cloud write and no node read was made"
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  if [ -n "$DRY_RAN" ]; then
    RESTORE_NOTE="a dry run: nothing was converged and nothing was written, so there is nothing to undo. The scratch directory held the two reads' output"
    return 0
  fi
  local backup=""
  if [ -n "$U1_BACKUP" ]; then
    backup=" The CLI backed the config it replaced up to $U1_BACKUP, which is its own backup and not this check's."
  fi
  RESTORE_NOTE="this check wrote nothing of its own: it ran the installation's own 'fugaro update' twice, which is the converge the installation would have run anyway, and the migration it wrote into $CONFIG — the typed credentials and the craft accesses — STAYS. That is the installation's own state and not this check's, and reverting it would not be a restore: it would be the unconverted config this release no longer has to offer. 'fugaro update --forget' is your own rollback if the installation must go back, and this check never runs it.${backup} The scratch directory held the commands' output"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to read facts and
  # remove a directory, and a second Ctrl-C during it would kill this shell half
  # done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
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

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version features xtrace agent-session belong bound installation
  config-before update no-step-left crafts policy config-after second-update
  restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 422 RESULT\n' >&3
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
  printf 'CHECK 422 RESULT END (exit %s)\n' "$status" >&3
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
INSTALLATION_CLI=""
CONFIG_CLI=""
TIMEOUT_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --installation)
    [ $# -ge 2 ] || die "--installation needs the GCP project ID of the sandbox installation"
    INSTALLATION_CLI=$2
    shift 2
    ;;
  --config)
    [ $# -ge 2 ] || die "--config needs a path: the sandbox installation's own local config"
    CONFIG_CLI=$2
    shift 2
    ;;
  --timeout)
    [ $# -ge 2 ] || die "--timeout needs a whole number of seconds: how long one converge may take"
    TIMEOUT_CLI=$2
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

INSTALLATION=${CHECK422_INSTALLATION:-${INSTALLATION_CLI:-}}
CONFIG=${CHECK422_CONFIG:-${CONFIG_CLI:-}}
if [ -z "$CONFIG" ]; then
  CONFIG=${FUGARO_CONFIG:-}
fi
DRY_RUN=${CHECK422_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK422_KEEP_SCRATCH:-}
if [ -n "$TIMEOUT_CLI" ]; then
  UPDATE_TIMEOUT=$TIMEOUT_CLI
fi
UPDATE_TIMEOUT=${CHECK422_TIMEOUT:-$UPDATE_TIMEOUT}
case "$UPDATE_TIMEOUT" in
'' | *[!0-9]*) die "CHECK422_TIMEOUT/--timeout must be a whole number of seconds (got '$UPDATE_TIMEOUT')" ;;
esac

main() {
  need_tools
  scratch_up
  trap finish EXIT
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  fact fugaro-version INFO "$($FUGARO_BIN version 2>/dev/null | head -n 1 || true)"
  step_features
  step_installation
  step_config_before
  step_bound
  if [ -n "$DRY_RUN" ]; then
    DRY_RAN=yes
    plan
    return 0
  fi
  step_policy_before
  step_update
  step_no_step_left
  # A converge that left the person a step stopped the installation half way:
  # what the file holds after it is not the migration's outcome, so the rows
  # below are not measured and the run ends as one that could not tell.
  if [ -n "$UPDATE_UNFINISHED" ]; then
    stop "the converge left $U1_LEFT step(s) for you: finish them and run the check again — whether the migration landed was not measured"
  fi
  step_config_after
  step_crafts
  step_policy
  step_second_update
}

trap on_signal INT TERM HUP
main "$@"
