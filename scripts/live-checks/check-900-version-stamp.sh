#!/usr/bin/env bash
# Check 900 — a project's required-version stamp, read-only, as ONE script YOU
# run. It settles U1 of docs/required-version-stamp/reconciliation.md, and run
# as a member it settles U2 as well.
#
# The stamp is the node a project publishes at config/policy/required_cli
# (internal/versionstamp.Path) saying the oldest fugaro release that may act on
# it, and every later CLI reads it before it acts. Its shape is
# `{"schema":1,"min":"X.Y.Z","set_at":"<RFC3339>"}` (internal/versionstamp,
# schemas/required-cli.schema.json): `fugaro update`'s version-stamp stage is
# what writes it, nothing lowers it (D3262), and doctor reports a binary below
# it as the `cli-below-required` row (T5).
#
# It reads, and only reads:
#
#   - the node, over the Realtime Database REST API with the caller's own token
#     (one curl GET of <rtdb>/config/policy/required_cli.json). It prints the
#     node's `schema`, `min` and `set_at`, which are not secrets, and checks
#     them against the schema's rules: the three members and no others, a
#     `schema` of 1, a `min` that is a release version, a `set_at` that is a
#     date-time;
#   - `fugaro version`;
#   - `fugaro doctor --json`'s `cli-below-required` row, which must AGREE with
#     the release this binary is: at or above the stamp it is ok, below it it is
#     the warning whose fix is the brew command and then `fugaro update`;
#   - the time of `fugaro runs ls --json` with XDG_CACHE_HOME pointed at an
#     empty scratch directory.
#
# It prints PASS when the node is well formed, its `min` is the installed
# release (the state `fugaro update` leaves behind, which is why this check is
# run after one), the doctor row agrees, and the cold read is under 2 seconds
# (U1).
#
# THE FIGURE THE COLD READ IS JUDGED ON is the stamp read's own cost, and this
# is the one reading in this check that needs a word. Every project command
# reads the node behind versionstamp.ReadTimeout (2 s, D3271) through a
# 15-minute cache, and a read that does not fit fails OPEN: the command
# proceeds with nothing read, and the fence the node exists to be is silently
# absent. That is what U1 asks about, and the whole of a `fugaro runs ls` is not
# the place to see it: the listing is 15 s to 32 s on a real installation
# (Check 760's own baselines of 2026-11-19), so a criterion on the whole
# command's time would fail every healthy installation while measuring the
# listing. So the check takes the plan's own reading — `runs ls --json` with an
# empty XDG_CACHE_HOME — and then, in that same cache home, takes it apart:
#
#   cold     the first run: every cache cold, which is the plan's literal
#            reading, printed as a figure and judged by nothing;
#   stamp    the stamp's own cache directory removed (and nothing else), so the
#            stamp is the ONE cold read in the command;
#   warm     the same command again, with the stamp's cache back: the listing's
#            own cost, on this bucket, right now.
#
# A cold read writes the stamp cache when it succeeds (internal/versionstamp),
# so the cache's presence after the `stamp` run is the direct evidence that the
# read completed inside ReadTimeout at all — and that, beside `stamp - warm`,
# is the reading U1 was left to this check for. Each of the two is taken
# CHECK900_REPEATS times (3) and the smallest of each is used, because the
# listing's own variance is seconds and the figure wanted here is a fraction of
# one. The `cold` figure is printed beside them for the plan's record.
#
# Usage:      bash scripts/live-checks/check-900-version-stamp.sh <fugaro-project>
#             bash scripts/live-checks/check-900-version-stamp.sh --help
# Environment CHECK900_CONFIG     the project's local config, when it is not
#             $XDG_CONFIG_HOME/fugaro/projects/<fugaro-project>.yaml
#             CHECK900_FUGARO     the binary to read (default `fugaro`)
#             CHECK900_COLD_MS    the bound the cold read is judged by, in
#                                 milliseconds (default 2000,
#                                 versionstamp.ReadTimeout)
#             CHECK900_CALL_SECONDS  the deadline for one `fugaro` call
#                                 (default 600)
#             CHECK900_REPEATS    how many times the `stamp`/`warm` pair is
#                                 taken (default 3)
#             CHECK900_KEEP_SCRATCH=yes  keep the scratch directory and say so
# Needs:      bash, mktemp, python3 (the clock and the readers), perl (the
#             deadline), gcloud, curl, and a fugaro on PATH.
# Calls:      gcloud auth print-access-token --quiet; one curl GET of
#             <rtdb>/config/policy/required_cli.json; fugaro version; fugaro
#             doctor --json; fugaro runs ls --json (once, then twice per
#             repeat). Every call runs with stdin closed and nothing is written
#             outside this run's scratch directory.
# Runs:       by YOU, after the release and its `fugaro update`, and by a member
#             as well when one exists — a member's run is what settles U2. It
#             refuses to start in a coding agent's session and in a traced
#             shell. No agent runs it; its own test
#             (check-900-version-stamp_test.sh) drives it against fakes on PATH
#             and touches no cloud.
# Token:      the access token goes into a 0600 curl config file in a 0700
#             scratch directory that the exit trap removes; it is never printed
#             and never on an argv.
# Output:     progress on stderr, ONE block on stdout headed CHECK 900 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        the node is well formed, its min is the
#                                    installed release, doctor's row agrees and
#                                    the cold read fits ReadTimeout
#               FAIL (exit 1)        the node is missing or malformed, or its
#                                    min is not this release, or the doctor row
#                                    disagrees, or the cold read did not fit
#               INCOMPLETE (exit 2)  the run stopped before it could judge
#               INCOMPLETE (exit 3)  it judged, and a read it needed did not
#                                    answer (UNVERIFIED)
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
AUTH_CONF=""
PROJECT=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY STATE "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

usage() {
  cat <<'USAGE'
Check 900 — a project's required-version stamp, read-only. It asks nothing and
writes nothing outside its own scratch directory.

Usage:
  bash scripts/live-checks/check-900-version-stamp.sh <fugaro-project>
  bash scripts/live-checks/check-900-version-stamp.sh --help

It reads the node config/policy/required_cli with the caller's own token, prints
its schema, min and set_at and checks them against the schema's rules, reads
`fugaro version` and `fugaro doctor --json`'s cli-below-required row, and times a
`fugaro runs ls --json` whose stamp cache is cold. It PASSes when the node is
well formed, its min is this release, the doctor row agrees and the cold read
fits versionstamp.ReadTimeout (2 s).

  PASS (exit 0)  FAIL (exit 1)  INCOMPLETE (exit 2 stopped, exit 3 unverified)
USAGE
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs Check 900: a person runs it, and a member's own run is what settles U2. Run it yourself: bash scripts/live-checks/check-900-version-stamp.sh <fugaro-project>"
}

guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell is tracing (bash -x), and its trace would print the access token this check mints. Run it without -x"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads the project with it"
}

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  -*) stop "unknown argument $1 (usage: check-900-version-stamp.sh <fugaro-project>, or --help)" ;;
  *)
    if [ -z "$PROJECT" ]; then PROJECT=$1; else stop "one project is read at a time (got '$PROJECT' and '$1')"; fi
    shift
    ;;
  esac
done

guard_xtrace
guard_agent

[ -n "$PROJECT" ] || stop "no Fugaro project was named (usage: check-900-version-stamp.sh <fugaro-project>)"
printf '%s' "$PROJECT" | grep -Eq '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' ||
  stop "'$PROJECT' is not a Fugaro project name"

FUGARO=${CHECK900_FUGARO:-fugaro}
COLD_MS=${CHECK900_COLD_MS:-2000}
CALL_SECONDS=${CHECK900_CALL_SECONDS:-600}
REPEATS=${CHECK900_REPEATS:-3}
for pair in "CHECK900_COLD_MS:$COLD_MS" "CHECK900_CALL_SECONDS:$CALL_SECONDS" "CHECK900_REPEATS:$REPEATS"; do
  case "${pair#*:}" in '' | *[!0-9]*) stop "${pair%%:*} '${pair#*:}' is not a number of milliseconds, seconds or repeats" ;; esac
done
[ "$COLD_MS" -ge 1 ] || stop "CHECK900_COLD_MS must be at least 1"
[ "$CALL_SECONDS" -ge 1 ] || stop "CHECK900_CALL_SECONDS must be at least 1"
[ "$REPEATS" -ge 1 ] || stop "CHECK900_REPEATS must be at least 1"

need mktemp
need python3
need perl
need gcloud
need curl
command -v "$FUGARO" >/dev/null 2>&1 || stop "$FUGARO is not on PATH, and this check reads the installation with it"

CONFIG=${CHECK900_CONFIG:-${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/${PROJECT}.yaml}

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk900.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS=$SCRATCH/facts
: >"$FACTS"
AUTH_CONF=$SCRATCH/auth.conf
COLD_HOME=$SCRATCH/cache
mkdir -p "$COLD_HOME"
: >"$SCRATCH/timing"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  if [ -n "${CHECK900_KEEP_SCRATCH:-}" ]; then
    say "kept the scratch directory at $SCRATCH (CHECK900_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

verdict() { # verdict <facts>
  if printf '%s\n' "$1" | grep -q '^aborted|'; then
    printf 'INCOMPLETE'
  elif printf '%s\n' "$1" | grep -q '|FAIL|'; then
    printf 'FAIL'
  elif printf '%s\n' "$1" | grep -q '|UNVERIFIED|'; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

print_block() { # print_block <facts> <status>
  printf '\nCHECK 900 RESULT\n'
  printf 'ROWS\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-12s %-10s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 900 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT judged"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory, with the token's curl config file, is gone"
  elif [ -n "${CHECK900_KEEP_SCRATCH:-}" ]; then
    facts="${facts}"$'\n'"restore|INFO|the scratch directory is kept at ${SCRATCH}, by CHECK900_KEEP_SCRATCH, and it holds an access token: remove it yourself"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH}, and it holds an access token: remove it yourself"
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
trap finish EXIT

# read_config prints the config's needed members as JSON. yaml.v3 writes block
# style, so an indentation reader suffices and python3 needs no YAML module.
read_config() { # read_config <file>
  python3 - "$1" <<'PY'
import json, re, sys

def unquote(v):
    v = v.strip()
    if len(v) >= 2 and v[0] == v[-1] and v[0] in "'\"":
        return v[1:-1]
    return v

want = {"name", "budget.rtdb_url"}
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

first_line() { head -1 "$1" 2>/dev/null | cut -c1-300 || true; }

now_ms() { python3 -c 'import time; print(int(time.time() * 1000))' </dev/null; }

# run_call SECONDS CMD... — one command with a deadline of its own and stdin
# closed. perl's alarm rather than timeout(1), which macOS does not have; the
# alarm is set on the process that then execs the command, so it is the command
# itself that the deadline kills.
run_call() { # run_call SECONDS CMD...
  local secs=$1
  shift
  perl -e 'alarm shift; exec @ARGV or die "exec $ARGV[0]: $!\n"' "$secs" "$@" </dev/null
}

# ---------------------------------------------------------------- the config
[ -f "$CONFIG" ] || stop "no local config for $PROJECT at $CONFIG: name it with CHECK900_CONFIG"
read_config "$CONFIG" >"$SCRATCH/config.json" 2>"$SCRATCH/config.err" ||
  stop "the local config $CONFIG could not be read: $(first_line "$SCRATCH/config.err")"
NAME=$(cfg_get name)
RTDB=$(cfg_get budget.rtdb_url)
RTDB=${RTDB%/}
[ -z "$NAME" ] || [ "$NAME" = "$PROJECT" ] || stop "$CONFIG is the config of '$NAME', not '$PROJECT'"
printf '%s' "$RTDB" | grep -Eq '^https://[a-z0-9-]+(\.[a-z0-9-]+)*\.(firebaseio\.com|firebasedatabase\.app)$' ||
  stop "$CONFIG names no Realtime Database URL (budget.rtdb_url '$RTDB'), and the stamp lives there. Run 'fugaro update' to write it"
fact config PASS "read $CONFIG: the node is read from $RTDB/config/policy/required_cli.json, which is where fugaro update publishes it"

# ------------------------------------------------------------------- the node
say "minting an access token (gcloud auth print-access-token)"
mint_token || stop "gcloud auth print-access-token did not answer ($(first_line "$SCRATCH/token.err")): run 'gcloud auth login' and this check again"
fact token PASS "an access token was written to a 0600 file in this run's 0700 scratch directory, never printed or put on an argv"

say "reading the node config/policy/required_cli"
NODE_CODE=$(curl -q -sS --max-time 30 --config "$AUTH_CONF" -o "$SCRATCH/node.json" -w '%{http_code}' \
  "$RTDB/config/policy/required_cli.json" </dev/null 2>"$SCRATCH/node.err") || NODE_CODE=000

# --------------------------------------------------------------- the version
say "reading fugaro version"
VERSION_STATUS=0
run_call "$CALL_SECONDS" "$FUGARO" version >"$SCRATCH/version.txt" 2>"$SCRATCH/version.err" || VERSION_STATUS=$?
printf '%s\n' "$VERSION_STATUS" >"$SCRATCH/version.status"

say "reading fugaro doctor --json"
DOCTOR_STATUS=0
run_call "$CALL_SECONDS" "$FUGARO" doctor --json --project "$PROJECT" >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || DOCTOR_STATUS=$?
printf '%s\n' "$DOCTOR_STATUS" >"$SCRATCH/doctor.status"

# -------------------------------------------------------------- the cold read
# stamp_cache_files counts the files the stamp's own cache holds under the cold
# cache home: a successful read of the node writes one there
# (internal/versionstamp), so its presence after a run is the evidence that the
# read completed at all.
stamp_cache_files() {
  local n=0 f
  for f in "$COLD_HOME/fugaro/required-cli"/*.json; do
    [ -e "$f" ] || continue
    n=$((n + 1))
  done
  printf '%s' "$n"
}

time_runs() { # time_runs <tag>: one timed `fugaro runs ls --json` in the cold cache home
  local tag=$1 rc=0 started
  started=$(now_ms)
  run_call "$CALL_SECONDS" env XDG_CACHE_HOME="$COLD_HOME" "$FUGARO" runs ls --json --project "$PROJECT" \
    >"$SCRATCH/runs-$tag.json" 2>"$SCRATCH/runs-$tag.err" || rc=$?
  printf '%s %s %s %s\n' "$tag" "$(( $(now_ms) - started ))" "$rc" "$(stamp_cache_files)" >>"$SCRATCH/timing"
}

say "timing fugaro runs ls --json with an empty cache home (${COLD_HOME})"
time_runs cold
say "timing it again with only the stamp's cache removed, ${REPEATS} times"
i=1
while [ "$i" -le "$REPEATS" ]; do
  rm -rf "$COLD_HOME/fugaro/required-cli"
  time_runs "stamp-$i"
  time_runs "warm-$i"
  i=$((i + 1))
done

# ------------------------------------------------------------- the judgement
say "judging the node, the version, doctor's row and the cold read"
python3 - "$SCRATCH" "$NODE_CODE" "$COLD_MS" >"$SCRATCH/analysis" 2>"$SCRATCH/analysis.err" <<'PY' ||
import json, os, re, sys
from datetime import datetime, timezone

scratch, http_code, bound_ms = sys.argv[1], sys.argv[2], int(sys.argv[3])

def fact(key, state, detail):
    detail = " ".join(str(detail).split()).replace("|", "/")
    print("fact|%s|%s|%s" % (key, state, detail))

def read(name):
    try:
        with open(os.path.join(scratch, name), encoding="utf-8") as f:
            return f.read()
    except OSError:
        return ""

def first_line(name):
    lines = read(name).strip().splitlines()
    return lines[0][:300] if lines else ""

VER = re.compile(r"^v?(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$")
DT = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$")

def core(v):
    return v[1:] if v.startswith("v") else v

def trip(v):
    return tuple(int(x) for x in core(v).split("."))

def age(t):
    secs = (datetime.now(timezone.utc) - t).total_seconds()
    if secs < 0:
        return "dated %.1f h in the future" % (-secs / 3600.0)
    if secs < 90:
        return "written %d s ago" % secs
    if secs < 5400:
        return "written %.1f h ago" % (secs / 3600.0)
    return "written %.1f days ago" % (secs / 86400.0)

# ------------------------------------------------------------------ the node
# The members the schema allows, and only these: schemas/required-cli.schema.json
# is closed, and a member nobody reads is a decision nobody made.
MEMBERS = ("schema", "min", "set_at")

node_ok = False
min_v = None
body = read("node.json").strip()


def node_fail(detail):
    fact("node", "FAIL", detail)


if http_code == "000":
    fact("node", "UNVERIFIED", "the database did not answer, so no stamp was seen: %s"
         % (first_line("node.err") or "the read failed"))
elif http_code in ("401", "403"):
    node_fail("the node could not be read with the caller's own token (HTTP %s), and a principal that cannot read it cannot be fenced by it: this is U2 answered NO for this caller. Run this check as the owner, and as a member as well if one exists" % http_code)
elif http_code == "404":
    node_fail("the database answered 404 for the node's path, so the Realtime Database URL the local config names points at no database. Run 'fugaro update'")
elif http_code != "200":
    fact("node", "UNVERIFIED", "the node's read answered HTTP %s %s, which is neither a stamp nor an absent one"
         % (http_code, first_line("node.err")))
elif body in ("", "null"):
    node_fail("the database holds no stamp at config/policy/required_cli (the read answered null): `fugaro update`'s version-stamp stage publishes it and nothing else does. Run 'fugaro update'")
else:
    try:
        doc = json.loads(body)
    except ValueError:
        doc = None
        node_fail("the read answered 200 with a body that is not JSON: %s" % body[:200])
    if doc is not None and not isinstance(doc, dict):
        node_fail("the node holds %s, not the object the schema describes" % type(doc).__name__)
    elif isinstance(doc, dict):
        missing = [k for k in MEMBERS if k not in doc]
        extra = [k for k in sorted(doc) if k not in MEMBERS]
        when = None
        if missing or extra:
            why = []
            if missing:
                why.append("it is missing %s" % ", ".join(missing))
            if extra:
                why.append("it carries %s, which the schema does not allow" % ", ".join(extra))
            node_fail("the stamp's shape is closed at every level: " + "; ".join(why))
        elif isinstance(doc["schema"], bool) or not isinstance(doc["schema"], int):
            node_fail("the stamp's schema is %r, and the schema is the number 1" % (doc["schema"],))
        elif doc["schema"] != 1:
            node_fail("the stamp's schema is %s, and this CLI writes and reads schema 1: a schema a reader does not know means the reader is behind, so this binary is older than the stamp" % doc["schema"])
        elif not isinstance(doc["min"], str) or not VER.match(doc["min"]):
            node_fail("the stamp's min is %r, which is not a release version (X.Y.Z): a dev build or a pre-release tag is not one, and a hand edit is what leaves anything else" % (doc["min"],))
        elif not isinstance(doc["set_at"], str) or not DT.match(doc["set_at"]):
            node_fail("the stamp's set_at is %r, which is not an RFC3339 date-time: it is what a refusal reports as when the fence was raised" % (doc["set_at"],))
        else:
            try:
                when = datetime.fromisoformat(doc["set_at"].replace("Z", "+00:00"))
            except ValueError:
                when = None
            if when is None or when.tzinfo is None:
                node_fail("the stamp's set_at %r is not a date-time that carries its zone" % doc["set_at"])
            else:
                min_v = doc["min"]
                node_ok = True
                fact("node", "PASS", "schema=%s min=%s set_at=%s (%s), read over the REST API with the caller's own token"
                     % (doc["schema"], doc["min"], doc["set_at"], age(when)))

# ------------------------------------------------------------- the version
installed = read("version.txt").strip()
version_status = read("version.status").strip()
version_ok = version_status == "0" and bool(VER.match(installed))
if version_status != "0":
    fact("version", "UNVERIFIED", "`fugaro version` exited %s: %s" % (version_status, first_line("version.err")))
elif not version_ok:
    fact("version", "UNVERIFIED", "`fugaro version` prints %r, which is not a release version: this binary was built from a checkout (a dev build), so it is neither at nor below a stamp, and doctor skips this row for it too. Run this check with a released fugaro" % (installed or "nothing"))
else:
    fact("version", "PASS", "fugaro version prints %s" % installed)

# ----------------------------------------------------------- the stamp's min
below = None
if not node_ok:
    fact("stamp-min", "UNVERIFIED", "the node is not a well-formed stamp, so no min could be compared with this release")
elif not version_ok:
    fact("stamp-min", "UNVERIFIED", "the installed binary is not a release version, so the stamp's min cannot be compared with it")
else:
    below = trip(installed) < trip(min_v)
    if core(min_v) == core(installed):
        fact("stamp-min", "PASS", "the stamp's min (%s) is the installed release (%s): the state `fugaro update` leaves behind" % (min_v, installed))
    elif trip(min_v) < trip(installed):
        fact("stamp-min", "FAIL", "the stamp's min (%s) is BELOW the installed release (%s): the version-stamp stage of `fugaro update` writes the installed release and nothing lowers the node (D3262), so this installation has not been updated since it was upgraded. Run 'fugaro update' and this check again" % (min_v, installed))
    else:
        fact("stamp-min", "FAIL", "this CLI (%s) is BELOW the project's required release (%s): every project command is refused until it is not. Run 'brew upgrade fugaro', then 'fugaro update', then this check again" % (installed, min_v))

# ------------------------------------------------------------ doctor's row
dj = read("doctor.json")
doctor_status = read("doctor.status").strip()
docout = None
try:
    docout = json.loads(dj) if dj.strip() else None
except ValueError:
    docout = None

if docout is None:
    fact("doctor-row", "UNVERIFIED", "`fugaro doctor --json` (exit %s) answered nothing this check could read: %s"
         % (doctor_status, first_line("doctor.err") or first_line("doctor.json")))
elif not isinstance(docout, dict):
    fact("doctor-row", "UNVERIFIED", "`fugaro doctor --json` answered %s, not the document the console and this check read" % type(docout).__name__)
elif docout.get("error"):
    fact("doctor-row", "UNVERIFIED", "doctor could not run its checks: %s" % docout["error"])
else:
    row = None
    for c in docout.get("checks") or []:
        if isinstance(c, dict) and c.get("id") == "cli-below-required":
            row = c
    if row is None:
        fact("doctor-row", "FAIL", "doctor reports no cli-below-required row over %d checks (ok=%s): the release this check consumes is not installed. Run this check with the release that carries it" % (len(docout.get("checks") or []), docout.get("ok")))
    elif row.get("skipped"):
        fact("doctor-row", "UNVERIFIED", "doctor's cli-below-required row was skipped, so it agrees with nothing: %s%s"
             % (row.get("problem") or "no reason given", (" Fix: " + row["fix"]) if row.get("fix") else ""))
    elif below is None:
        fact("doctor-row", "UNVERIFIED", "the installed release is not known, so the row could not be compared with it: %s" % (row.get("problem") or "the row is ok=%s" % row.get("ok")))
    else:
        ok = bool(row.get("ok"))
        if ok == (not below):
            fact("doctor-row", "PASS", "doctor's cli-below-required row (ok=%s) agrees with the installed release %s beside the stamp's min %s" % (ok, installed, min_v))
        else:
            fact("doctor-row", "FAIL", "doctor's cli-below-required row says ok=%s while this binary (%s) is %s the stamp's min (%s): %s%s"
                 % (ok, installed, "BELOW" if below else "at or above", min_v,
                    row.get("problem") or "no problem text given", (" Fix: " + row["fix"]) if row.get("fix") else ""))

# ------------------------------------------------------------- the cold read
timing = {}
for line in read("timing").splitlines():
    parts = line.split()
    if len(parts) == 4:
        timing[parts[0]] = (int(parts[1]), int(parts[2]), int(parts[3]))

def one(tag):
    return timing.get(tag)

cold = one("cold")
stamps = [timing[k] for k in sorted(timing) if k.startswith("stamp-")]
warms = [timing[k] for k in sorted(timing) if k.startswith("warm-")]

def timed_out(entry):
    return entry is not None and entry[1] != 0

def names(tag_ms):
    return "%s ms" % tag_ms

if cold is None or not stamps or not warms:
    fact("cold-read", "UNVERIFIED", "the timed reads were not taken, so U1 was not settled")
else:
    broken = [(tag, e) for tag, e in [("cold", cold)] + [("stamp-%d" % (i + 1), e) for i, e in enumerate(stamps)] + \
              [("warm-%d" % (i + 1), e) for i, e in enumerate(warms)] if timed_out(e)]
    if broken:
        tag, e = broken[0]
        fact("cold-read", "UNVERIFIED", "`fugaro runs ls --json` never answered for the %s read (exit %s after %s ms, CHECK900_CALL_SECONDS bounds it): %s"
             % (tag, e[1], e[0], first_line("runs-%s.err" % tag)))
    elif stamps[0][2] == 0 and cold[2] == 0:
        fact("cold-read", "UNVERIFIED", "no stamp cache landed under %s in any of the runs: either this fugaro reads no stamp at all (the release that carries the fence is not installed) or every cold read failed and failed open. Run this check with the release that carries L1, and read node.json in the scratch directory (CHECK900_KEEP_SCRATCH=yes) for what the fence did" % os.path.join(scratch, "cache", "fugaro", "required-cli"))
    else:
        cost = min(e[0] for e in stamps) - min(e[0] for e in warms)
        if cost < 0:
            cost = 0
        figures = "cold cache home %s, only the stamp's cache removed %s, the same run warm %s" % (
            names(cold[0]), names(min(e[0] for e in stamps)), names(min(e[0] for e in warms)))
        if stamps[0][2] == 0:
            fact("cold-read", "FAIL", "the cold read of the stamp landed NO cache: it did not complete inside versionstamp.ReadTimeout, so the fence failed open, and that is U1 answered NO. The readings: %s" % figures)
        elif cost >= bound_ms:
            fact("cold-read", "FAIL", "the cold read of the stamp took %s ms, at or over the %s ms bound (versionstamp.ReadTimeout): the read does not fit the timeout a project command reads the node under, and the fence fails open when it does not. The readings: %s" % (cost, bound_ms, figures))
        else:
            fact("cold-read", "PASS", "the cold read of the stamp cost %s ms, under the %s ms bound (versionstamp.ReadTimeout), and it landed its cache. The readings: %s; the whole command with every cache cold, the plan's own figure, is %s ms, of which the listing is the whole (Check 760's baselines are 15 s to 32 s), which is why the criterion is the stamp read's own cost" % (cost, bound_ms, figures, cold[0]))
PY
  stop "the judgement could not be made: $(first_line "$SCRATCH/analysis.err")"

while IFS='|' read -r tag k state detail; do
  case "$tag" in
  fact) fact "$k" "$state" "$detail" ;;
  esac
done <"$SCRATCH/analysis"

REACHED_END=yes
