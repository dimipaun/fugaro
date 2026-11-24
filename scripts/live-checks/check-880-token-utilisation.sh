#!/usr/bin/env bash
# Check 880 — whether an answer on a subscription token carries a utilisation
# figure. Run by YOU, after a run on a subscription token. It settles U1 of the
# model-credentials routing work (D3178), and it gates PL8: the threshold rule is
# not launched unless this check found a utilisation figure.
#
# What it reads, and why it calls nothing
# ---------------------------------------
# The question is what the provider returns on a subscription token, and this
# script cannot ask it: a model route admits a run's identity only, so the
# script makes no model call of its own. What it reads instead is the record the
# service keeps while it serves (creds.Observation, internal/creds/observed.go,
# written on the answer path in internal/credsvc): for every source it has
# served, the NAMES of the rate-limit headers that source's last answer carried,
# and never one of their values. A name that is a utilisation figure is the
# answer U1 asks for, and it is read from the name alone — the script never
# needs, sees or prints a figure.
#
# It reads, and only reads:
#
#   - the installation's local config (`name`, `budget.rtdb_url`);
#   - the Realtime Database node `creds/observed`, whose every value is a list
#     of header names: the record holds the names of rate-limit headers and
#     nothing else, and it is the service's own credential state, not a bundle.
#
# It prints, for each source under test, the names recorded for it and which of
# them is a utilisation figure, then one row per source and one verdict row.
#
# The question is EXISTENTIAL and the verdict is too: U1 asks whether a
# subscription token's answer reports a utilisation figure at all, so ONE source
# whose record names one answers it yes — the subscription token's own run is one
# answer among the installation's, and a key's record beside it that names rate
# limits without a figure changes nothing about it. That is also the direction
# that cannot lose the gate: a source naming no figure is recorded as INFO,
# never as a finding about the check.
#
# The verdict is read from the question's own row (`utilisation`) and from no
# other: a source whose answer named nothing, a source the service never served,
# a name you gave it does not know and a scratch directory that outlived the trap
# are reported as their own rows and move no verdict — read as the verdict they
# would answer "no" or "nothing to judge" on evidence that says yes. What makes
# that row FAIL is a figure's absence where names were read, or a record that is
# not the shape the service writes.
#
# Verdicts:
#   PASS (exit 0)        a source's record names a utilisation figure: U1 is
#                        answered yes on this evidence, and PL8 may be launched
#   FAIL (exit 1)        no source's record names a utilisation figure, and at
#                        least one names rate-limit headers: U1 is answered no on
#                        this evidence, and PL8 is withdrawn (the plan says so).
#                        A record that is not the shape the service writes is a
#                        FAIL too.
#   INCOMPLETE (exit 2)  the run stopped before it could read
#   INCOMPLETE (exit 3)  there is no name to judge at all (UNVERIFIED): the
#                        record holds no source, every source it holds recorded
#                        no name — nothing has been recorded since this release,
#                        or the last recorded answer carried no rate-limit header
#                        at all — or a name you gave is not one the service has
#                        served. Nothing is concluded and PL8 is not launched.
#
# Usage:      bash scripts/live-checks/check-880-token-utilisation.sh <fugaro-project> [source…]
#             source      one credential's name as the service serves it (the
#                         account's own name, `default` or a named account,
#                         which `fugaro accounts ls` lists — NOT the bundle
#                         document key `fugaro creds ls` prints). With none
#                         named, every source in the record is judged.
# Environment CHECK880_CONFIG  the installation's local config, when it is not
#                         $XDG_CONFIG_HOME/fugaro/projects/<fugaro-project>.yaml
# Needs:      bash, mktemp, python3, gcloud, curl.
# Calls:      gcloud auth print-access-token; one curl GET of
#             <rtdb>/creds/observed.json. Every call runs with stdin closed and the
#             gcloud call with --quiet. Nothing is written.
# Runs:       by YOU. It refuses to start in a coding agent's session and in a
#             traced shell. Its test (check-880-token-utilisation_test.sh) drives
#             it against fakes on PATH and touches no cloud.
# Token:      the access token goes into a 0600 curl config file in a 0700
#             scratch directory that the exit trap removes; it is never printed
#             and never on an argv.
# Output:     progress on stderr, ONE block on stdout headed CHECK 880 RESULT.
#             Its last line is the verdict, and the verdict is the exit status.
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
SOURCES_TABLE=""

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
Check 880 — whether an answer on a subscription token carries a utilisation
figure (U1, D3178). Read-only: it reads the rate-limit header names the
credentials service recorded for each source and makes no model call of its own.

Usage:
  bash scripts/live-checks/check-880-token-utilisation.sh <fugaro-project> [source…]
  bash scripts/live-checks/check-880-token-utilisation.sh --help

With no source named, every source in creds/observed is judged. For each it
prints the rate-limit header names its last answer carried and which of them is
a utilisation figure. No value of any header is recorded, printed or needed.

The question is existential, and so is the verdict: ONE source whose record names
a utilisation figure answers it yes, whatever the other sources' records hold.

  PASS (exit 0)  a source's record names a utilisation figure
  FAIL (exit 1)  no source's record names one, and at least one names rate-limit
                 headers (or the record is not the shape the service writes)
  INCOMPLETE (exit 2 stopped before it could read,
              exit 3 nothing to judge at all: UNVERIFIED)
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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs Check 880: it is the owner's reading of his own installation, and it gates PL8. Run it yourself: bash scripts/live-checks/check-880-token-utilisation.sh <fugaro-project> [source…]"
}

guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell is tracing (bash -x), and its trace would print the access token this check mints. Run it without -x"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads the installation's own record with it"
}

PROJECT=""
SOURCES=()
while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  -*) stop "unknown argument $1 (usage: check-880-token-utilisation.sh <fugaro-project> [source…], or --help)" ;;
  *)
    if [ -z "$PROJECT" ]; then PROJECT=$1; else SOURCES+=("$1"); fi
    shift
    ;;
  esac
done

guard_xtrace
guard_agent

[ -n "$PROJECT" ] || stop "no Fugaro project was named (usage: check-880-token-utilisation.sh <fugaro-project> [source…])"
printf '%s' "$PROJECT" | grep -Eq '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' ||
  stop "'$PROJECT' is not a Fugaro project name"
for s in ${SOURCES[@]+"${SOURCES[@]}"}; do
  printf '%s' "$s" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$' ||
    stop "'$s' is not a source name (a credential's name as the service serves it: the account's own name, which 'fugaro accounts ls' lists, and not the bundle key 'fugaro creds ls' prints)"
done

need mktemp
need python3
need gcloud
need curl

CONFIG=${CHECK880_CONFIG:-${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/${PROJECT}.yaml}

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk880.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS=$SCRATCH/facts
: >"$FACTS"
AUTH_CONF=$SCRATCH/auth.conf

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

# verdict reads the question's OWN row, and nothing else. Every fact this
# check writes is a row of the block, and the rows beside the question are not
# the question: a source whose last answer carried no rate-limit header, a
# source the service has never served and a scratch directory that outlived the
# trap are all rows a run writes beside a recorded utilisation figure, and
# reading any of them as the verdict answers U1 "no" or "nothing to judge" on
# evidence that says yes. The row that answers it is `utilisation`, which the
# analysis writes PASS, FAIL or UNVERIFIED — FAIL for a record that is not the
# shape the service writes, because the question needs a record to be read at
# all.
verdict() { # verdict <facts>
  local key state row=""
  while IFS='|' read -r key state _detail; do
    if [ "$key" = aborted ]; then
      printf 'INCOMPLETE'
      return 0
    fi
    if [ "$key" = utilisation ]; then row=$state; fi
  done <<<"$1"
  case "$row" in
  PASS|FAIL) printf '%s' "$row" ;;
  *) printf 'INCOMPLETE' ;;
  esac
}

print_block() { # print_block <facts> <status>
  printf '\nCHECK 880 RESULT\n'
  if [ -n "$SOURCES_TABLE" ]; then printf '%s\n' "$SOURCES_TABLE"; fi
  printf 'ROWS\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-10s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 880 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  if [ -f "$SCRATCH/sources" ]; then SOURCES_TABLE=$(cat "$SCRATCH/sources"); fi
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT judged"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory, with the token's curl config file, is gone"
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
# style, so an indentation reader suffices and python3 needs no YAML module. It
# is the same reader Check 892 uses, and it is deliberately narrow: this check
# reads two members of the file and nothing else in it.
read_config() { # read_config <file>
  python3 - "$1" <<'PY'
import json, re, sys

def unquote(v):
    v = v.strip()
    if len(v) >= 2 and v[0] == v[-1] and v[0] in "'\"":
        return v[1:-1]
    return v

want_scalars = {"name", "gcp_project", "budget.rtdb_url"}
out = {k: "" for k in want_scalars}
stack = []
for raw in open(sys.argv[1], encoding="utf-8"):
    line = raw.rstrip("\n")
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    line = re.sub(r"\s+#.*$", "", line)
    ind = len(line) - len(line.lstrip(" "))
    body = line.strip()
    if body == "-" or body.startswith("- "):
        continue
    m = re.match(r"^([A-Za-z0-9_./-]+):(?:\s+(.*))?$", body)
    if not m:
        continue
    while stack and stack[-1][0] >= ind:
        stack.pop()
    key, val = m.group(1), (m.group(2) or "").strip()
    path = ".".join([k for _, k in stack] + [key])
    if val == "":
        stack.append((ind, key))
    elif path in want_scalars:
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

# ------------------------------------------------------------------ the config
[ -f "$CONFIG" ] || stop "no local config for $PROJECT at $CONFIG: name it with CHECK880_CONFIG"
read_config "$CONFIG" >"$SCRATCH/config.json" 2>"$SCRATCH/config.err" ||
  stop "the local config $CONFIG could not be read: $(first_line "$SCRATCH/config.err")"
NAME=$(cfg_get name)
RTDB=$(cfg_get budget.rtdb_url)
RTDB=${RTDB%/}
[ -z "$NAME" ] || [ "$NAME" = "$PROJECT" ] || stop "$CONFIG is the config of '$NAME', not '$PROJECT'"
printf '%s' "$RTDB" | grep -Eq '^https://[a-z0-9-]+(\.[a-z0-9-]+)*\.(firebaseio\.com|firebasedatabase\.app)$' ||
  stop "$CONFIG names no Realtime Database URL (budget.rtdb_url '$RTDB'), and the record lives there. Run 'fugaro update' to write it"
fact config PASS "read $CONFIG: the record is in $RTDB"

# ------------------------------------------------------------------- the token
say "minting an access token (gcloud auth print-access-token)"
mint_token || stop "gcloud auth print-access-token did not answer ($(first_line "$SCRATCH/token.err")): run 'gcloud auth login' and this check again"
fact token PASS "an access token was written to a 0600 file in this run's 0700 scratch directory, never printed or put on an argv"

# ------------------------------------------------------------------ the record
say "reading the record of the rate-limit header names (creds/observed)"
code=$(curl -q -sS --max-time 30 --config "$AUTH_CONF" -o "$SCRATCH/observed.json" -w '%{http_code}' \
  "$RTDB/creds/observed.json" </dev/null 2>"$SCRATCH/observed.err") || code=000
[ "$code" = 200 ] ||
  stop "the record $RTDB/creds/observed could not be read (HTTP $code$([ -s "$SCRATCH/observed.err" ] && printf ': %s' "$(first_line "$SCRATCH/observed.err")")): it is the whole of what this check reads"

python3 - "$SCRATCH/observed.json" "$SCRATCH/sources" ${SOURCES[@]+"${SOURCES[@]}"} >"$SCRATCH/analysis" 2>"$SCRATCH/analysis.err" <<'PY' ||
import json, sys

path, table_path = sys.argv[1], sys.argv[2]
wanted = sys.argv[3:]

# Names reach a terminal, and a source name is text another writer made. A name
# of a header is a name too, so both are held to printable ASCII and a length
# before either is printed. No value is ever printed because none is ever read:
# the record is the names.
def san(s, n=120):
    out = "".join(c if 32 <= ord(c) < 127 else "?" for c in str(s))
    return out[:n] + ("…" if len(out) > n else "")

def is_utilisation(name):
    low = name.lower()
    return "utilization" in low or "utilisation" in low

def fact(k, state, detail):
    print("fact|%s|%s|%s" % (k, state, detail.replace("|", "/")))

try:
    with open(path) as f:
        doc = json.load(f)
except Exception as exc:  # a body that is not JSON is not a record
    doc = None
    parse_error = san(exc)
else:
    parse_error = ""

# Whether the record is the shape the service writes, which is the one thing
# beside a figure's absence that makes the question's row FAIL: a record that is
# not a shape this release's service writes is a fact about the service, and the
# check may not conclude "there is nothing to judge" on it.
shape_failed = False

if doc is None:
    doc = {}
    fact("record", "UNVERIFIED", "the record node is absent: no answer of any source has been recorded on it, so nothing can be judged (run a run on the subscription token and run this again)")
elif not isinstance(doc, dict):
    doc = {}
    shape_failed = True
    fact("record", "FAIL", "the record node is not an object of sources%s: it is not the shape the service writes" % (" (%s)" % parse_error if parse_error else ""))
elif not doc:
    # An empty object is the JSON a node with nothing under it answers with,
    # and it is NOT a pass: no source was judged, so the question U1 asks has
    # no answer in it.
    fact("record", "UNVERIFIED", "the record holds no source at all: nothing has been recorded since this release, so nothing can be judged (run a run on the subscription token and run this again)")

table = []
names = [san(k) for k in sorted(doc)]
# The record's own keys are looked up as they are and printed as names: a key
# another writer made can carry anything, and a display that sanitizes its own
# lookup would fail to find the source it just showed.
order = list(wanted) if wanted else sorted(doc)

if doc:
    fact("record", "PASS", "%d source%s recorded in creds/observed" % (len(names), "" if len(names) == 1 else "s"))

# The figures any source recorded, and the sources that recorded rate-limit names
# without one. The verdict below is read from these two lists and from nothing
# else, so a source with no figure of its own is never a finding about the check.
with_figure = []
without = []

for src in order:
    shown = san(src)
    if src not in doc:
        fact("source", "UNVERIFIED", "%s has no record: no answer of it has been recorded since this release, or the name is not one the service serves" % shown)
        table.append("  %s  names=-  utilisation=?" % shown)
        continue
    m = doc[src]
    if not isinstance(m, dict):
        shape_failed = True
        fact("source", "FAIL", "%s holds something that is not an observation: it is not the shape the service writes" % shown)
        table.append("  %s  names=-  utilisation=?" % shown)
        continue
    if "headers" not in m:
        fact("source", "UNVERIFIED", "%s holds a node with no names in it: it is not the shape this release's service writes, so nothing can be read from it" % shown)
        table.append("  %s  names=-  utilisation=?" % shown)
        continue
    raw = m["headers"]
    if not isinstance(raw, list):
        fact("source", "UNVERIFIED", "%s holds a record that is not a list of names: nothing can be read from it" % shown)
        table.append("  %s  names=?  utilisation=?" % shown)
        continue
    got = [san(h, 200) for h in raw]
    if not got:
        # An answer that carried no rate-limit header is still an answer that was
        # recorded: the node says so, and it names nothing to judge.
        fact("source", "UNVERIFIED", "%s has no names recorded: its last recorded answer carried no rate-limit header at all, so there is nothing to judge for it" % shown)
        table.append("  %s  names=-  utilisation=?" % shown)
        continue
    util = [h for h in got if is_utilisation(h)]
    if util:
        with_figure.append(shown)
        fact("source", "PASS", "%s records %s — a utilisation figure is carried (%s), so U1 is answered yes here" % (shown, ", ".join(got), ", ".join(util)))
    else:
        without.append(shown)
        fact("source", "INFO", "%s records %s — none of them is a utilisation figure. It answers nothing about a subscription token's own answer: this check asks whether ANY source names one" % (shown, ", ".join(got)))
    table.append("  %s  names=%s  utilisation=%s" % (shown, ", ".join(got), ", ".join(util) or "-"))

# The verdict row, and the ONLY row the verdict is read from (verdict, above).
# It is existential because the question is: one source naming a utilisation
# figure answers U1 yes, whatever the sources beside it hold, and a gate read the
# other way round would be withdrawn by a key's own rate-limit headers. When no
# source names one, the answer is FAIL only if the record really holds names to
# read or is not the shape the service writes — otherwise there is nothing to
# conclude (UNVERIFIED, exit 3) and PL8 is not launched.
if with_figure:
    fact("utilisation", "PASS", "%s: a utilisation figure is recorded%s, so U1 is answered yes and PL8 may be launched" % (
        ", ".join(with_figure), "" if not without else " (no figure on %s, which does not answer this question)" % ", ".join(without)))
elif without:
    fact("utilisation", "FAIL", "no source's record names a utilisation figure: %s recorded rate-limit names and none of them is one, so U1 is answered no on this evidence and PL8 is withdrawn" % ", ".join(without))
elif shape_failed:
    fact("utilisation", "FAIL", "the record is not the shape this release's service writes, so no source's names can be read from it: nothing is concluded about U1 and PL8 is not launched on it")
else:
    fact("utilisation", "UNVERIFIED", "no source recorded a rate-limit header name at all, so there is nothing to judge and PL8 is not launched on it")

with open(table_path, "w") as f:
    f.write("SOURCES\n")
    for line in table or ["  (none)"]:
        f.write(line + "\n")
PY
  stop "the record could not be judged: $(first_line "$SCRATCH/analysis.err")"

while IFS='|' read -r tag k state detail; do
  case "$tag" in
  fact) fact "$k" "$state" "$detail" ;;
  esac
done <"$SCRATCH/analysis"

REACHED_END=yes
