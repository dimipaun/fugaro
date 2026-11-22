#!/usr/bin/env bash
# Check 881 — a live chain falls through from an exhausted pool to the key,
# flagged and counted, as ONE script YOU run.
#
# It is CK1 of docs/model-credentials-routing/implementation-plan.md, run by YOU
# after release 1 is installed through the real `fugaro update` (the plan's
# "Checks the plan takes", row 881). What it measures is the one thing a chain
# does when its first source cannot serve: the next source serves the call, the
# step is FLAGGED as a fallback and the money it spent is COUNTED (D3180, R4,
# R16, CS8, F26, F27).
#
# A source is a NAME here and never a value: the pool's name, the source's kind,
# the model id and the dollars. No token, no key and no secret is read, and none
# may be printed — every document this check reads is a table, a state or a run
# record, and the readers below drop any field a document may carry a value in.
#
# What it reads, in this order:
#
#   agent-session  this shell is not a coding agent's session (no agent runs a
#                  paid run), it is not tracing, and nothing here names belong.
#   checkout       the checkout you point `--checkout` at is a git work tree
#                  whose committed file the run goes out on.
#   installation   `fugaro doctor --json` in that checkout: the project and the
#                  region every read below is scoped to. The project has to be
#                  the one you gave (or the only one named).
#   version        the build you have, recorded: this check is about what the
#                  release you installed does.
#   table          `fugaro routing ls --json`: the entry that wins for the
#                  model (`--model`, else the one entry whose chain starts with
#                  the launcher's pool), its first source, and the source after
#                  it. The winner is read by the plan's own rule — an exact
#                  pattern beats a prefix, a longer prefix beats a shorter
#                  (D3172) — and the row names every pattern that matched, so
#                  you can see the choice. A chain that does not start with the
#                  launcher's pool, or that names no source after it (D3180
#                  refuses such a chain), is a STOP: there is no fall-through
#                  to measure.
#   budget         `fugaro budget show --json`: the project's mode is `enforce`
#                  and the repository has an effective per-run cap. R17 requires
#                  both before a chain that names a paid source after a pool may
#                  run at all; a project that does not hold them is a STOP.
#   first-source   the pool the chain's first source resolves to cannot serve:
#                  every member of it is at its limit, or no pool of that name
#                  is designated at all (D3180's other half: the launcher's pool
#                  is none and the chain skips it). A pool that still has a
#                  member able to serve is a STOP — the run would be served by
#                  the pool and the rows below would measure nothing.
#   launch         ONE run is launched in the checkout (`fugaro run --task-file`,
#                  with the check's own task file). This SPENDS MONEY. A launch
#                  that is refused is a STOP, and the refusal's own message is
#                  quoted: it names the fix.
#   run            the run is followed to its end, bounded (`fugaro runs ls
#                  --json`), and its own record is read out of the runs bucket
#                  (`gcloud storage cat`). A run still going when the bound is
#                  over is UNVERIFIED rather than a finding.
#   served-by-the-paid-source
#                  every stage of the record that carries a `source` names a
#                  kind OTHER than the launcher's pool, and at least one names
#                  the paid source the chain lists after it: the chain really
#                  fell through. A record whose stages carry no `source` at all
#                  is the finding (CS8 adds the member).
#   flagged        at least one stage carries `fallback: true` (CS8, D3188).
#   counted        the record's `cost.fallback_usd` is more than zero: the real
#                  money the fallback spent (CS8, D140).
#   restore        what this check changed, said rather than assumed: it creates
#                  nothing, and the run it launched is left to end on its own.
#
# Usage:      bash scripts/live-checks/check-881-chain-falls-through.sh \
#               --checkout <the repository's checkout> \
#               [--pool <the pool the chain's first source resolves to>] \
#               [--model <the model id the chain is for>] \
#               [--installation <name>] [--gcp-project <project>] \
#               [--region <region>] [--bucket <the runs bucket>] [--dry-run]
#             --dry-run prints the plan and calls NOTHING: no gcloud, no fugaro,
#                       no run. It is the way to read what a real run would do
#                       before spending anything.
#             --checkout  the checkout of the repository whose runs this check
#                         is about; the run is launched in it.
#             --pool      the pool the chain's first source resolves to for you
#                         (the one `fugaro accounts pool designate` recorded, or
#                         the one `--pool` named on the launch). Without it the
#                         pool is read off the table's first source when that
#                         source names one; a chain whose first source names no
#                         pool needs this flag.
#             --model     the model the chain is read for. Without it the check
#                         takes the one entry whose chain starts with the
#                         launcher's pool, and stops if there are several.
#             --installation, --gcp-project, --region
#                         what `fugaro doctor --json` has to agree with. The
#                         project and the region are the checkout's OWN — read
#                         from doctor, never defaulted — because every document
#                         below is the installation's; a value given here that
#                         disagrees with doctor's stops the check. What doctor
#                         answers is held to the belong guard as well.
#             --bucket    the runs bucket (default fugaro-runs-<project>).
# Needs:      bash, mktemp, python3 (every document is read by one python3
#             reader), git, gcloud (authenticated as the project's owner: the
#             run's record out of the runs bucket), fugaro. Nothing is installed
#             and nothing is fetched.
# Where:      the installation, project and region you name, and NOWHERE else:
#             every call carries them explicitly, and a project, region or
#             installation naming belong is refused by name — both what you
#             typed and what the checkout's own `fugaro doctor --json` answers,
#             since with no --gcp-project the project used is doctor's. Run it
#             on an installation you may upgrade and on a repository you may
#             spend on.
# Costs:      ONE run's model spend, bounded by the repository's own per-run cap
#             — the money this check exists to see counted. Every other call is
#             a read.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-881-chain-falls-through_test.sh) drives
#             it against fake `fugaro` and fake `gcloud` on PATH, with no
#             network, no cloud project and no run at all.
# Cleanup:    the scratch directory is removed from the exit trap, on every way
#             out including a Ctrl-C, and the `restore` row says whether it
#             really went. NOTHING ELSE IS CREATED: the run it launches is left
#             to end on its own, which is what a real run does.
# Output:     progress on stderr, ONE block on stdout headed CHECK 881 RESULT,
#             whose last line is the verdict and the exit status:
#
#               CHECK 881 RESULT: PASS (exit 0)        every criterion held;
#               CHECK 881 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 881 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 881 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS. Paste the block into the
#             register with the date and `fugaro version`.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace would print the
# commands of a run this check launches, into the log an owner pastes back.
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
ABORT=""
CLEANUP_RUNNING=no

# Every global a step assigns is set to the empty string here, so the exit trap
# can read them on a run that stopped at its first step: an unbound variable
# under `set -u` would end the trap before it printed the block, which is the
# one thing an owner pastes back.
RUN_ID=""
RUN_REF=""
FIRST_KIND=""
FIRST_POOL=""
PAID_KIND=""
PAID_NAME=""

stop() {
  ABORT=$1
  say "stopped: $1"
  exit 2
}
die() { say "$*"; exit 2; }

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO|UNVERIFIED "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured KEY "detail": a row the check could NOT measure for a reason that
# is not a finding about the code. It reads FAIL in the block — it is not a pass
# — and the exit says which rows they were.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the chain, the fall-through or the money"
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 881 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 881 needs bash"
  local t
  for t in mktemp git python3 gcloud fugaro; do need "$t"; done
}

# ------------------------------------------------------------- scratch + traps
SCRATCH=""
SCRATCH_MARKER=".chk881-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
KEEP_SCRATCH=${CHECK881_KEEP_SCRATCH:-}
RESTORE_VERDICT=""
RESTORE_NOTE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk881.XXXXXXXX") || die "Check 881 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 881 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
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
    say "CHECK881_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check changed, said rather than assumed. It writes nothing
# and deletes nothing: its one lasting effect is the run it launched, which ends
# on its own.
restore() {
  RESTORE_VERDICT=PASS
  if [ -n "$RUN_ID" ]; then
    RESTORE_NOTE="nothing was created by this check and nothing was changed: it launched the run ${RUN_ID} of ${RUN_REF}, which is left to end on its own (its own pull request is the outcome a run leaves), and it published no policy, wrote no record and minted no credential. Its whole other footprint is the reads above and the scratch directory"
  else
    RESTORE_NOTE="nothing was created and nothing was changed: no run was launched, no policy was published, no record was written and no credential was minted. Its whole footprint is the reads above and the scratch directory"
  fi
}

cleanup() {
  CLEANUP_RUNNING=yes
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  facts="${facts}"$'\n'"belong|${BELONG_VERDICT:-FAIL}|${BELONG_DETAIL:-the belong guard did not run}"
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  local key verdict failed=no unverified=no
  while IFS='|' read -r key verdict _; do
    case "$verdict" in
    FAIL)
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      failed=yes
      ;;
    UNVERIFIED) unverified=yes ;;
    esac
  done <<<"$facts"
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then status=1; fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    [ "$status" = 0 ] && status=2
  fi
  if [ "$unverified" = yes ] && [ "$status" = 0 ]; then status=3; fi
  if [ -n "$ABORT" ] && [ "$status" = 0 ]; then status=2; fi
  print_block "$facts" "$status"
  exit "$status"
}

# ------------------------------------------------------------------- the tools
oneline() { tr '\n\t' '  ' | tr -s ' ' | sed -e 's/^ //' -e 's/ $//'; }

# jget FILE PATH prints the value at a dotted path of a JSON document, or the
# empty string. A field that is absent is an absence rather than an error: a row
# that has to tell "absent" from "unreadable" reads the exit status of the
# command that wrote the file, not this.
jget() { # jget <file> <dotted.path>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
cur = doc
for part in sys.argv[2].split("."):
    if part == "":
        continue
    if isinstance(cur, list):
        try:
            cur = cur[int(part)]
        except Exception:
            print("")
            sys.exit(0)
    elif isinstance(cur, dict):
        if part not in cur:
            print("")
            sys.exit(0)
        cur = cur[part]
    else:
        print("")
        sys.exit(0)
if isinstance(cur, (dict, list)):
    print(json.dumps(cur))
elif isinstance(cur, bool):
    print("true" if cur else "false")
else:
    print(cur)
PY
}

# table_winner FILE MODEL prints one line for the entry that wins for MODEL:
#
#   <pattern>|<kind>:<name>|<kind>:<name>|...
#
# and one `matched:<pattern>` line for every other pattern that matched it, so
# the row can say what the choice was between. The winner is read by the plan's
# own rule (D3172): an exact pattern beats a prefix, a longer prefix beats a
# shorter one, and two entries with the same pattern are refused. The reader
# takes `entries` or a bare list, `match` or `pattern`, `chain` or `sources`,
# because the JSON is the contract's (C1, contract-table.md) and this check is
# run by hand against the release that shipped it. It prints kinds and names and
# nothing else: a source carries no value on any of these documents.
table_winner() { # table_winner <file> <model>
  python3 - "$1" "$2" <<'PY'
import json, sys

def name_of(s):
    return s.get("pool") or s.get("provider") or s.get("name") or ""

try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
model = sys.argv[2]
entries = doc.get("entries") if isinstance(doc, dict) else doc
if not isinstance(entries, list):
    sys.exit(1)

def render(e):
    chain = e.get("chain") or e.get("sources") or []
    parts = []
    for s in chain:
        if isinstance(s, dict):
            parts.append("%s:%s" % (s.get("kind") or "", name_of(s)))
    return "|".join(parts)

matches = []
for e in entries:
    if not isinstance(e, dict):
        continue
    pat = e.get("match") or e.get("pattern") or ""
    if not pat:
        continue
    if pat.endswith("*"):
        if model.startswith(pat[:-1]):
            matches.append((1, len(pat) - 1, pat, e))
    elif pat == model:
        matches.append((2, len(pat), pat, e))
if not matches:
    sys.exit(3)
matches.sort(key=lambda m: (-m[0], -m[1]))
best = matches[0]
print("%s|%s" % (best[2], render(best[3])))
for m in matches[1:]:
    print("matched:%s" % m[2])
PY
}

# pool_report FILE POOL prints `absent` when no pool of that name is in the
# document, or `present|<members>` followed by one line per member:
#
#   <state>|<headroom>|<exhausted_until>
#
# The reader takes `pools` or a bare list and `name` or `pool`, and it prints no
# `value` field of anything: C2's contract says the document carries state, next
# reset, usage and headroom and never a value, and a member that carried one
# anyway would be printed by nobody here.
pool_report() { # pool_report <file> <pool>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
pools = doc.get("pools") if isinstance(doc, dict) else doc
if not isinstance(pools, list):
    sys.exit(1)
want = sys.argv[2]
for p in pools:
    if not isinstance(p, dict):
        continue
    if (p.get("name") or p.get("pool") or "") != want:
        continue
    members = p.get("members") or []
    print("present|%d" % len(members))
    for m in members:
        if not isinstance(m, dict):
            continue
        state = m.get("state") or ""
        headroom = m.get("headroom")
        headroom = "" if headroom is None else headroom
        until = m.get("exhausted_until") or m.get("reset") or ""
        print("%s|%s|%s" % (state, headroom, until))
    sys.exit(0)
print("absent")
PY
}

# member_can_serve STATE HEADROOM WINDOW: whether a pool member can serve a call,
# which is what the gate below reads. It cannot when its state word says the
# credential is out for good (a revoked, retired, disabled, invalid, dead or
# broken one waits for a person and never for a clock), when its headroom is known
# to be zero, or when it is exhausted and its window is still closed. Nothing else
# counts as unable, and every direction matters. An absent headroom figure is
# nobody having reported one (PL10) rather than zero. A window in the past is a
# window that has reopened: an exhausted mark whose hold has lapsed serves again
# (internal/creds Mark.Usable), so the state word alone does not say "out". An
# instant this check cannot read is not one it may call a closed window. And the
# instant is consulted only for an exhausted member: the listing prints a next
# reset for every member (PL10), so an available member's reset is when its window
# rolls over and not a closed one, and reading it as closed would call a pool that
# can serve unable — the costly mistake, because a member wrongly called unable
# passes the gate and spends a paid run on a pool that could have served it, which
# is the whole thing the gate prevents.
member_can_serve() { # member_can_serve <state> <headroom> <window>
  case "$1" in
  *retir* | *revok* | *disabl* | *invalid* | *dead* | *broken*) return 1 ;;
  esac
  python3 - "$1" "$2" "$3" <<'PY'
import datetime, sys
state, headroom, window = sys.argv[1], sys.argv[2], sys.argv[3]
if headroom not in ("", "null", "None"):
    try:
        if float(headroom) == 0.0:
            sys.exit(1)
    except ValueError:
        pass
if "exhaust" in state and window not in ("", "null", "None"):
    text = window[:-1] + "+00:00" if window.endswith("Z") else window
    try:
        when = datetime.datetime.fromisoformat(text)
    except ValueError:
        sys.exit(0)
    if when.tzinfo is None:
        when = when.replace(tzinfo=datetime.timezone.utc)
    if when > datetime.datetime.now(datetime.timezone.utc):
        sys.exit(1)
sys.exit(0)
PY
}

# record_stages FILE prints one line per stage of a run's own record:
#
#   <stage>|<model>|<model_usd>|<notional_usd>|<source kind>|<source name>|<fallback>
#
# The members are CS8's: `source` (a kind and a name, never a value) and
# `fallback` per stage, and `cost.fallback_usd` on the cost block.
record_stages() { # record_stages <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
stages = doc.get("stages")
if not isinstance(stages, list):
    sys.exit(0)
for s in stages:
    if not isinstance(s, dict):
        continue
    src = s.get("source") or {}
    if not isinstance(src, dict):
        src = {}
    print("|".join([
        str(s.get("stage") or s.get("name") or ""),
        str(s.get("model") or ""),
        str(s.get("model_usd") if s.get("model_usd") is not None else ""),
        str(s.get("model_notional_usd") if s.get("model_notional_usd") is not None else ""),
        str(src.get("kind") or ""),
        str(src.get("name") or ""),
        "true" if s.get("fallback") else "false",
    ]))
PY
}

# -------------------------------------------------------------------- guards
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 881: it launches a paid run against a real installation. Run it yourself: bash scripts/live-checks/check-881-chain-falls-through.sh --checkout <the repository's checkout>"
  fact agent-session PASS "this shell carries no coding agent's marker, so the run this check launches is launched by you"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs, including the launch. Run it without -x: bash scripts/live-checks/check-881-chain-falls-through.sh"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the commands it ran"
}

# is_belong NAME: the installation named `belong` is the owner's own. A substring
# test on purpose, so a name that merely contains it is refused too.
is_belong() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *belong*) return 0 ;;
  esac
  return 1
}

# guard_belong WHERE NAME...: called on what was typed AND on what the checkout's
# own `fugaro doctor --json` answers, because the check can be pointed at the
# owner's own installation by either — and with no --gcp-project the project this
# check goes on to use is doctor's, so judging only what was typed judges the
# empty string. The verdict is held in a variable rather than recorded here, so
# the row says the LAST word: a PASS from the typed values followed by a FAIL
# from doctor's would otherwise leave a PASS in the block.
guard_belong() { # guard_belong WHERE NAME...
  local where=$1 v
  shift
  for v in "$@"; do
    [ -n "$v" ] || continue
    if is_belong "$v"; then
      BELONG_VERDICT=FAIL
      BELONG_DETAIL="the installation this check would use names belong — '$v', from ${where} — and this check is never run there: it launches a paid run against the owner's own installation. Run it on an installation you may spend on"
      stop "the installation this check would use names belong (from ${where}), and this check is never run there: it launches a paid run against the owner's own installation"
    fi
  done
}

step_belong() {
  guard_belong "what you typed" "${INSTALLATION:-}" "${GCP_PROJECT:-}" "${REGION:-}"
  BELONG_VERDICT=PASS
  BELONG_DETAIL="what you typed (--installation ${INSTALLATION:-<unset>}, --gcp-project ${GCP_PROJECT:-<unset>}, --region ${REGION:-<unset>}) names no installation this check refuses, and neither will the checkout's own fugaro doctor --json answer: it is held to the same guard, and the project and region every read below carries are doctor's own"
}

# doctor_facts FILE prints `name|gcp_project|region` out of `fugaro doctor
# --json`, which answers under `project` (internal/cli/doctor.go's doctorProject).
# Exit 2 is a document with no project in it, which is a different thing from a
# document that cannot be read.
doctor_facts() { # doctor_facts <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
p = doc.get("project") if isinstance(doc, dict) else None
if not isinstance(p, dict):
    sys.exit(2)
print("%s|%s|%s" % (p.get("name") or "", p.get("gcp_project") or "", p.get("region") or ""))
PY
}

# repo_of URL prints `owner/name` out of an origin remote, in the shapes git
# prints them: https://github.com/owner/name.git, git@github.com:owner/name.git,
# ssh://git@github.com/owner/name, the same on bitbucket, and the same with a
# port. A remote that is a path on this machine names no repository, so it is a
# refusal (exit 1) rather than a made-up owner/name.
repo_of() { # repo_of <url>
  python3 - "$1" <<'PY'
import re, sys
u = sys.argv[1].strip()
u = re.sub(r"\.git$", "", u)
u = re.sub(r"^[a-zA-Z][a-zA-Z0-9+.-]*://", "", u)
if u.startswith(("/", "./", "../", "~")):
    sys.exit(1)
head = u.split("/", 1)[0]
if "@" in head:
    u = u.split("@", 1)[1]
u = re.sub(r"^[^/:]+:", "", u)
parts = [p for p in re.split(r"[/:]", u) if p]
if len(parts) < 2:
    sys.exit(1)
print("%s/%s" % (parts[-2], parts[-1]))
PY
}

# --------------------------------------------------------------------- steps
step_checkout() {
  [ -n "$CHECKOUT" ] || die "Check 881 needs --checkout: the checkout of the repository whose runs it is about (the run is launched in it). Usage: bash scripts/live-checks/check-881-chain-falls-through.sh --checkout <the repository's checkout>"
  [ -d "$CHECKOUT" ] || stop "--checkout names $CHECKOUT, and that is not a directory"
  local top
  top=$(git -C "$CHECKOUT" rev-parse --show-toplevel 2>/dev/null || true)
  [ -n "$top" ] || stop "--checkout names $CHECKOUT, and it is not a git work tree: this check launches a run in it, and a run goes out on a checkout"
  CHECKOUT=$(cd "$top" && pwd)
  [ -f "$CHECKOUT/fugaro.yaml" ] || stop "$CHECKOUT carries no fugaro.yaml, so it is not a repository a run goes out on: point --checkout at the checkout of the repository this check is about"
  # The repository this checkout belongs to is what the run is charged to and
  # whose per-run cap the money is settled against, and its origin remote is
  # where `fugaro run` reads it too. Without it the cap row below could read
  # another repository's cap and call it this one's.
  local url rc=0
  url=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null) || rc=$?
  [ "$rc" = 0 ] && [ -n "$url" ] ||
    stop "$CHECKOUT has no 'origin' remote, so this check cannot name the repository whose caps the run it launches is charged to (fugaro run resolves it from the same remote). Run it in the checkout of the repository this check is about"
  local rc2=0
  REPO_NAME=$(repo_of "$url") || rc2=$?
  [ "$rc2" = 0 ] && [ -n "$REPO_NAME" ] ||
    stop "the origin remote of $CHECKOUT ($url) does not name a repository this check can read as owner/name"
  fact checkout PASS "$CHECKOUT is a git work tree carrying the fugaro.yaml the run goes out on, and its origin remote names ${REPO_NAME}: the run below is charged to that repository and the cap row reads ITS per-run cap"
}

step_installation() {
  say "reading the checkout's installation: fugaro doctor --json (in $CHECKOUT)"
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" doctor --json) >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || rc=$?
  local facts rc2=0
  facts=$(doctor_facts "$SCRATCH/doctor.json") || rc2=$?
  if [ "$rc" != 0 ] || [ "$rc2" = 1 ]; then
    stop "fugaro doctor --json failed in $CHECKOUT (exit $rc): $(head -c 300 "$SCRATCH/doctor.err" | oneline) — every read below is scoped to the project and region it names, and a checkout whose installation cannot be read is not one this check can measure"
  fi
  if [ "$rc2" = 2 ]; then
    stop "fugaro doctor --json answered about no project for $CHECKOUT: the runs bucket and the run's record are the project's own, and a document with no project in it names none. Run it where a project config is in force"
  fi
  local rest
  DOCTOR_NAME=${facts%%|*}
  rest=${facts#*|}
  DOCTOR_GCP=${rest%%|*}
  DOCTOR_REGION=${rest#*|}
  # What doctor answers is guarded too, and BEFORE anything is run: with no
  # --gcp-project the project below is doctor's own, so a checkout whose config
  # names the owner's installation would otherwise launch its paid run there.
  guard_belong "the checkout's own fugaro doctor --json" "$DOCTOR_NAME" "$DOCTOR_GCP" "$DOCTOR_REGION"
  [ -n "$DOCTOR_GCP" ] || stop "fugaro doctor --json named no gcp_project for $CHECKOUT (it answered project '${DOCTOR_NAME:-none}'): the runs bucket and the run's record are named after the project the checkout's config carries"
  if [ -n "$GCP_PROJECT" ] && [ "$GCP_PROJECT" != "$DOCTOR_GCP" ]; then
    stop "--gcp-project names ${GCP_PROJECT} and the checkout's own project is ${DOCTOR_GCP}: this check reads the runs bucket of the project the checkout belongs to, so the two have to be the same"
  fi
  GCP_PROJECT=$DOCTOR_GCP
  # The region is the installation's own, held to --region the way the project
  # is: every read below carries it, so a region that is not the installation's
  # reads another installation's documents.
  if [ -n "$REGION" ] && [ -n "$DOCTOR_REGION" ] && [ "$REGION" != "$DOCTOR_REGION" ]; then
    stop "--region names ${REGION} and the checkout's own installation is in ${DOCTOR_REGION} (fugaro doctor --json): every read this check makes is the installation's own, so the two have to be the same"
  fi
  if [ -n "$DOCTOR_REGION" ]; then REGION=$DOCTOR_REGION; fi
  [ -n "$REGION" ] || stop "fugaro doctor --json named no region for $CHECKOUT and --region was not given: every read this check makes carries it"
  [ -n "$BUCKET" ] || BUCKET="fugaro-runs-${GCP_PROJECT}"
  fact installation PASS "the checkout belongs to ${GCP_PROJECT} (${REGION}), and its runs are read out of gs://${BUCKET}/runs/<slug>/<run>/ — the project and region are the checkout's own fugaro doctor --json answer, not a default"
}

step_version() {
  local v=""
  v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fact version PASS "${v:-fugaro version printed nothing} — the build this check drives: what the chain does and what the record holds are the ones you installed"
}

step_table() {
  say "reading the routing table: fugaro routing ls --json (in $CHECKOUT)"
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" routing ls --json) >"$SCRATCH/table.json" 2>"$SCRATCH/table.err" || rc=$?
  if [ "$rc" != 0 ]; then
    stop "fugaro routing ls --json failed in $CHECKOUT (exit $rc): $(head -c 300 "$SCRATCH/table.err" | oneline) — release 1's table is what this check reads the chain out of. Is the installed fugaro the release `fugaro update` brought?"
  fi
  local rows rc2=0
  rows=$(table_winner "$SCRATCH/table.json" "$MODEL") || rc2=$?
  case "$rc2" in
  0) ;;
  3)
    stop "no entry of the table matches ${MODEL} (fugaro routing ls --json listed the entries above): R1 refuses a model no entry matches, so there is no chain to fall through — point --model at a model the table routes"
    ;;
  *)
    stop "fugaro routing ls --json printed something this check cannot read as a table: $SCRATCH/table.json. The reader takes the contract's own spellings (C1: entries with a match and a chain of sources)"
    ;;
  esac
  local winner pattern kinds first second matched
  winner=$(printf '%s\n' "$rows" | grep -v '^matched:' | head -n 1)
  matched=$(printf '%s\n' "$rows" | sed -n 's/^matched://p' | tr '\n' ' ')
  pattern=${winner%%|*}
  kinds=${winner#*|}
  MODEL=${MODEL:-$pattern}
  first=${kinds%%|*}
  second=""
  case "$kinds" in
  *"|"*) second=${kinds#*|}; second=${second%%|*} ;;
  esac
  FIRST_KIND=${first%%:*}
  FIRST_POOL=${first#*:}
  PAID_KIND=${second%%:*}
  PAID_NAME=${second#*:}
  local between=""
  [ -n "$matched" ] && between=" (other patterns matched: ${matched% })"
  if [ "$FIRST_KIND" != "launcher-pool" ]; then
    fact table FAIL "the entry that wins for ${MODEL} is '${pattern}' and its chain starts at ${FIRST_KIND}, not at the launcher's pool${between}: Check 881 measures a chain falling through FROM the launcher's pool, and this chain never starts there. Write one with 'fugaro routing set <pattern> launcher-pool anthropic' and run the check again"
    stop "the chain for ${MODEL} does not start with the launcher's pool"
  fi
  if [ -z "$second" ]; then
    fact table FAIL "the entry that wins for ${MODEL} is '${pattern}' and its chain names no source after the launcher's pool${between}: D3180 refuses such a chain (a chain of only the launcher's pool is refused with the fix), so there is no key to fall through to and nothing this check could measure. Put a source after it: 'fugaro routing set <pattern> launcher-pool anthropic'"
    stop "the chain for ${MODEL} names no source after the launcher's pool"
  fi
  if [ -n "$FIRST_POOL" ]; then
    [ -n "$POOL" ] || POOL=$FIRST_POOL
  fi
  fact table PASS "the entry that wins for ${MODEL} is '${pattern}': the launcher's pool first, then ${PAID_KIND}${PAID_NAME:+ (${PAID_NAME})}${between}"
}

step_budget() {
  say "reading the caps: fugaro budget show --json --repo ${REPO_NAME} (in $CHECKOUT)"
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" budget show --json --repo "$REPO_NAME") >"$SCRATCH/budget.json" 2>"$SCRATCH/budget.err" || rc=$?
  if [ "$rc" != 0 ]; then
    stop "fugaro budget show --json --repo ${REPO_NAME} failed in $CHECKOUT (exit $rc): $(head -c 300 "$SCRATCH/budget.err" | oneline) — R17 requires an enforced project with a per-run cap before a chain that names a paid source after a pool may run at all"
  fi
  local mode
  mode=$(jget "$SCRATCH/budget.json" mode)
  if [ "$mode" != "enforce" ]; then
    fact budget FAIL "the project's budget mode is '${mode:-unset}' and R17 requires 'enforce' before a chain that names a paid source after a pool may run: a fallback is money against the caps (D140), and on a project that does not enforce them the paid source would be reached with nothing holding it. Set the mode with 'fugaro budget mode enforce' and run the check again"
    stop "the project's budget mode is not enforce"
  fi
  # --repo narrows the listing to this checkout's own repository, so the row read
  # here is ITS cap: without it a multi-repository installation's first capped row
  # would be reported as this repository's and the R17 gate would pass falsely.
  local cap rc2=0
  cap=$(python3 - "$SCRATCH/budget.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(3)
rows = [r for r in (doc.get("repos") or []) if isinstance(r, dict)]
if not rows:
    print("norow")
    raise SystemExit(0)
r = rows[0]
if r.get("per_run_cap_usd") is None:
    print("nocap")
    raise SystemExit(0)
print("%s|%s" % (r.get("per_run_cap_usd"), r.get("per_run_cap_source") or ""))
PY
) || rc2=$?
  if [ "$rc2" = 3 ]; then
    fact budget FAIL "fugaro budget show --json --repo ${REPO_NAME} printed something this check cannot read as a caps document: $SCRATCH/budget.json"
    stop "the caps listing could not be read"
  fi
  case "$cap" in
  norow)
    fact budget FAIL "fugaro budget show --json --repo ${REPO_NAME} listed no row for ${REPO_NAME}, the repository this checkout's origin remote names: the mode and the per-run cap the run is settled against could not be read for the repository it is charged to"
    stop "the caps listing carries no row for ${REPO_NAME}"
    ;;
  nocap)
    fact budget FAIL "the project enforces its caps and no per-run cap is in force for ${REPO_NAME} (the repository this checkout belongs to): R17 requires one before a chain that names a paid source after a pool may run, because the money a fallback spends is settled against the run's cap (D140). Set one with 'fugaro budget set --repo ${REPO_NAME} --run <usd>' and run the check again"
    stop "no effective per-run cap is set for ${REPO_NAME}"
    ;;
  "")
    fact budget FAIL "fugaro budget show --json --repo ${REPO_NAME} answered about ${REPO_NAME} with no per-run cap figure this check can read: $SCRATCH/budget.json"
    stop "the per-run cap of ${REPO_NAME} could not be read"
    ;;
  esac
  local source
  source=${cap#*|}
  cap=${cap%%|*}
  fact budget PASS "the project enforces its caps and ${REPO_NAME}'s own effective per-run cap is \$${cap}${source:+ (from ${source})}: R17 holds, so the paid source the chain falls through to is money against the run's cap"
}

step_first_source() {
  local pool=${POOL:-}
  if [ -z "$pool" ]; then
    fact first-source FAIL "the chain's first source is the launcher's pool and neither the table nor --pool names which pool that is for you: 'fugaro accounts pool ls --json' lists the pools and 'fugaro accounts pool designate <pool>' records yours. Name it with --pool and run the check again"
    stop "no pool is named for the launcher's pool"
  fi
  say "reading the pool's state: fugaro accounts pool ls --json (in $CHECKOUT)"
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" accounts pool ls --json) >"$SCRATCH/pools.json" 2>"$SCRATCH/pools.err" || rc=$?
  if [ "$rc" != 0 ]; then
    stop "fugaro accounts pool ls --json failed in $CHECKOUT (exit $rc): $(head -c 300 "$SCRATCH/pools.err" | oneline) — whether the chain's first source can serve is read off this document"
  fi
  local report rc2=0
  report=$(pool_report "$SCRATCH/pools.json" "$pool") || rc2=$?
  [ "$rc2" = 0 ] || stop "fugaro accounts pool ls --json printed something this check cannot read as the pools: $SCRATCH/pools.json (C2's shape: a list of pools, each with a name and its members)"
  if [ "$report" = "absent" ]; then
    fact first-source PASS "no pool of that name is designated in this project (the check looked for '${pool}'), so the launcher's pool is none and the chain skips it (D3180): the first source cannot serve, which is what this check needs"
    return 0
  fi
  local n=${report#present|}
  n=${n%%$'\n'*}
  local usable=0 line state headroom window
  while IFS= read -r line; do
    case "$line" in
    present\|*) continue ;;
    "") continue ;;
    esac
    state=${line%%|*}
    window=${line##*|}
    headroom=${line#*|}
    headroom=${headroom%%|*}
    member_can_serve "$state" "$headroom" "$window" || continue
    usable=$((usable + 1))
  done <<<"$report"
  if [ "$usable" -gt 0 ]; then
    fact first-source FAIL "${pool} still has a member that can serve (${usable} of ${n} members carries neither a known-zero headroom nor an exhausted mark with its window still closed), so a run would be served by the pool and the rows below would measure no fall-through at all: this check stops before spending anything. Make the first source unable to serve — wait for the members' window to run out, or point --pool at a pool whose members are at their limit — and run the check again"
    stop "the launcher's pool still has a member that can serve"
  fi
  fact first-source PASS "every member of ${pool} is at its limit (${n} members, each carrying a known-zero headroom or an exhausted mark with its window still closed), so the chain's first source cannot serve and the next source has to: this is the state the check measures"
}

step_launch() {
  say "launching ONE run in $CHECKOUT (this spends money, bounded by the repository's per-run cap)"
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: check881 — one run whose chain falls through to the paid source

Change nothing that matters: add one line to README.md naming this run as Check
881's fall-through run, and open a pull request.
TASK
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" run --task-file "$SCRATCH/task.md" --json) >"$SCRATCH/launch.json" 2>"$SCRATCH/launch.err" || rc=$?
  if [ "$rc" != 0 ]; then
    unmeasured launch "the run could not be launched (exit $rc): $(head -c 400 "$SCRATCH/launch.err" | oneline). Every row below reads THIS run, so none of them is measured without it. A launch REFUSED here names its own fix — a repository with no per-run cap, a model no entry matches, a job image the release cannot serve — so read the message and settle it"
    stop "the run could not be launched"
  fi
  RUN_ID=$(jget "$SCRATCH/launch.json" run_id)
  [ -n "$RUN_ID" ] || RUN_ID=$(jget "$SCRATCH/launch.json" id)
  RUN_REF=$(jget "$SCRATCH/launch.json" run)
  [ -n "$RUN_REF" ] || RUN_REF=$RUN_ID
  if [ -z "$RUN_ID" ] || [ -z "$RUN_REF" ]; then
    unmeasured launch "a run was launched but its own answer carries no run id or no ref this check can read: $(head -c 200 "$SCRATCH/launch.json" | oneline)"
    stop "the launch answer carries no run id"
  fi
  fact launch PASS "the run ${RUN_ID} of ${RUN_REF} was launched in ${CHECKOUT}: the rows below read its own record"
}

step_run() {
  say "following the run to its end: fugaro runs ls --json, up to ${WAIT_SECONDS}s"
  local waited=0 outcome="" terminal="" answered=no
  while :; do
    if (cd "$CHECKOUT" && "$FUGARO_BIN" runs ls --json) >"$SCRATCH/runs.json" 2>"$SCRATCH/runs.err"; then
      answered=yes
      local got
      got=$(python3 - "$SCRATCH/runs.json" "$RUN_ID" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
rows = doc if isinstance(doc, list) else (doc.get("runs") or [])
for r in rows:
    if not isinstance(r, dict):
        continue
    ref = r.get("run") or ""
    if r.get("run_id") == sys.argv[2] or ref.endswith("/" + sys.argv[2]):
        print("%s|%s" % ("true" if r.get("terminal") is True else "false", r.get("outcome") or ""))
        break
PY
)
      terminal=${got%%|*}
      outcome=${got#*|}
      # The ended signal is the row's `terminal` (runview.Row.Terminal), not its
      # outcome: a run in flight already answers an outcome — `none`, written at
      # bootstrap — so breaking on a non-empty outcome reads the record while the
      # run is still spending and prints a false FAIL. The outcome is only what
      # the rows below print.
      if [ "$terminal" = true ]; then break; fi
    fi
    if [ "$waited" -ge "$WAIT_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$terminal" != true ]; then
    if [ "$answered" = no ]; then
      unmeasured run "fugaro runs ls --json never answered in $CHECKOUT, so the run ${RUN_ID} was not followed to its end and its record was not read: $(head -c 300 "$SCRATCH/runs.err" | oneline)"
    else
      unmeasured run "the run ${RUN_ID} is still not terminal after this check's own wait of ${WAIT_SECONDS}s (fugaro runs ls --json answered, and the row it answered with reads terminal=${terminal:-unknown}, outcome=${outcome:-unknown}): whether the chain fell through is NOT measured. Read it yourself ('fugaro watch', then the run's own record) and record the outcome in the register's section"
    fi
    stop "the run did not reach a terminal outcome inside the check's wait"
  fi
  say "reading the run's own record: gcloud storage cat gs://${BUCKET}/runs/${RUN_REF}/result.json"
  local rc=0
  gcloud storage cat "gs://${BUCKET}/runs/${RUN_REF}/result.json" --project="$GCP_PROJECT" \
    >"$SCRATCH/record.json" 2>"$SCRATCH/record.err" || rc=$?
  if [ "$rc" != 0 ]; then
    unmeasured run "the run ${RUN_ID} ended as '${outcome:-unknown}' and its record could not be read from gs://${BUCKET}/runs/${RUN_REF}/result.json (exit $rc): $(head -c 300 "$SCRATCH/record.err" | oneline). Every row below is read off that record, so none of them is measured"
    stop "the run's record could not be read"
  fi
  fact run PASS "the run ${RUN_ID} ended as '${outcome:-unknown}' and its own record was read out of gs://${BUCKET}/runs/${RUN_REF}/result.json: the rows below are what it holds"
}

step_served_by_the_paid_source() {
  local rc=0 stages
  stages=$(record_stages "$SCRATCH/record.json") || rc=$?
  if [ "$rc" != 0 ]; then
    unmeasured served-by-the-paid-source "the run's record is not a JSON document this check can read as a run record: $SCRATCH/record.json"
    stop "the record could not be read as a record"
  fi
  if [ -z "$stages" ]; then
    fact served-by-the-paid-source FAIL "the run's record carries no stages at all, so whether the chain fell through cannot be read off it: $SCRATCH/record.json holds no stage timings, and CS8's source member is written per stage. The run's own output is in the run's log, and the record it wrote is the subject here"
    return 0
  fi
  local withsource=0 served_paid=0 served_first=0 kinds="" line kind
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    kind=$(printf '%s' "$line" | cut -d'|' -f5)
    [ -n "$kind" ] || continue
    withsource=$((withsource + 1))
    case " ${kinds} " in *" ${kind} "*) ;; *) kinds="${kinds}${kinds:+ }${kind}" ;; esac
    if [ "$kind" = "$FIRST_KIND" ]; then served_first=$((served_first + 1)); fi
    if [ "$kind" = "$PAID_KIND" ]; then served_paid=$((served_paid + 1)); fi
  done <<<"$stages"
  if [ "$withsource" = 0 ]; then
    fact served-by-the-paid-source FAIL "the run's record carries no source on any of its stages: CS8 writes, per stage, the kind and the name of what served it, and a record without the member is the finding this check exists for — nothing on it says whether the chain fell through or which source served. The record is $SCRATCH/record.json"
    return 0
  fi
  if [ "$served_first" -gt 0 ]; then
    fact served-by-the-paid-source FAIL "${served_first} of the ${withsource} stages that carry a source were served by ${FIRST_KIND}, the chain's FIRST source, so the chain did not fall through for them: this check measures a run whose first source cannot serve, and a stage it served means the state the check read was not the state the run met (the pool's window may have reset between the two reads). What served each stage is in $SCRATCH/record.json"
    return 0
  fi
  if [ "$served_paid" -eq 0 ]; then
    fact served-by-the-paid-source FAIL "no stage was served by ${PAID_KIND}, the source the chain names after the launcher's pool: the stages carry ${kinds:-no kind at all}, so the fall-through this check measures did not happen. The chain's own order is in $SCRATCH/table.json and what served each stage is in $SCRATCH/record.json"
    return 0
  fi
  fact served-by-the-paid-source PASS "${served_paid} of the ${withsource} stages that carry a source were served by ${PAID_KIND}${PAID_NAME:+ (${PAID_NAME})} and none by ${FIRST_KIND}, the chain's first source: the chain fell through from the launcher's pool to the source after it"
}

step_flagged() {
  local rc=0 stages
  stages=$(record_stages "$SCRATCH/record.json") || rc=$?
  if [ "$rc" != 0 ]; then
    unmeasured flagged "the run's record is not a JSON document this check can read as a run record: $SCRATCH/record.json"
    stop "the record could not be read as a record"
  fi
  local flagged=0 total=0 line flag
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    total=$((total + 1))
    flag=$(printf '%s' "$line" | cut -d'|' -f7)
    [ "$flag" = "true" ] && flagged=$((flagged + 1))
  done <<<"$stages"
  if [ "$flagged" -eq 0 ]; then
    fact flagged FAIL "none of the run's ${total} stages carries 'fallback: true': a step served by a source that is not the chain's first is FLAGGED (CS8, D3188), so the console can show the fallback badge and a person can see that their pool ran dry (the spec's §'Cost attribution'). The record is $SCRATCH/record.json"
    return 0
  fi
  fact flagged PASS "${flagged} of the run's ${total} stages carries 'fallback: true': the step the paid source served is flagged as a fallback"
}

step_counted() {
  local money
  money=$(jget "$SCRATCH/record.json" cost.fallback_usd)
  case "$money" in
  "" )
    fact counted FAIL "the run's record carries no cost.fallback_usd at all: CS8 adds the member, and the money a paid fallback spent is real money against the run's caps (D140), so a fallback nothing counted is the finding this row exists for. The record is $SCRATCH/record.json"
    return 0
    ;;
  esac
  local positive
  positive=$(python3 -c 'import sys
try:
    print("yes" if float(sys.argv[1]) > 0 else "no")
except Exception:
    print("unreadable")' "$money")
  case "$positive" in
  yes)
    fact counted PASS "the record's cost.fallback_usd is \$${money}: the real money the fallback spent is counted, which is what settles it against the run's cap"
    ;;
  no)
    fact counted FAIL "the record's cost.fallback_usd is ${money}: a fallback to a paid source is money against the caps (D140) and this one counted none of it. Either nothing was spent — in which case the stages above should not have been served by a paid source — or the money was not counted. The record is $SCRATCH/record.json"
    ;;
  *)
    fact counted FAIL "the record's cost.fallback_usd is '${money}', which this check cannot read as a figure: CS8 writes the real money spent on fallback sources there. The record is $SCRATCH/record.json"
    ;;
  esac
}

# --------------------------------------------------------------------- the plan
plan() {
  say "CHECK 881 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] git -C <checkout> rev-parse --show-toplevel, the checkout's fugaro.yaml, and its origin remote — the owner/name the run below is charged to and whose cap is read"
  say "  [network, read] fugaro doctor --json (in <checkout>) — the project and the region every read below is scoped to (they are the installation's own, and what it answers is held to the belong guard too)"
  say "  [network, read] fugaro routing ls --json — the entry that wins for the model, its first source and the source after it"
  say "  [network, read] fugaro budget show --json --repo <owner/name> — the mode (enforce, R17) and THAT repository's effective per-run cap"
  say "  [network, read] fugaro accounts pool ls --json — whether the chain's first source can serve"
  say "  [network, ONE PAID RUN] fugaro run --task-file <a task this check writes> --json (in <checkout>) — the run whose record the rows below read. It costs its model spend, bounded by the repository's per-run cap"
  say "  [network, reads] fugaro runs ls --json, polled until the run is terminal — and then gcloud storage cat gs://${BUCKET:-fugaro-runs-<project>}/runs/<slug>/<run>/result.json, the run's own record"
  say "  [nothing created] no policy is published, no record is written and no credential is minted; the run it launches is left to end on its own"
  local key
  for key in checkout installation version table budget first-source launch run \
    served-by-the-paid-source flagged counted; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  # The belong row is not planned: the guard really ran on what you typed. What a
  # real run adds is doctor's own answer, which the dry run never reads.
  BELONG_VERDICT=INFO
  BELONG_DETAIL="the guard ran on what you typed (--installation ${INSTALLATION:-<unset>}, --gcp-project ${GCP_PROJECT:-<unset>}, --region ${REGION:-<unset>}) and passed it; --dry-run read no fugaro doctor --json, so the checkout's own answer is the part a real run adds to this row"
  fact dry-run PASS "the plan was printed and nothing was run: no launch, no cloud call and no model call"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(agent-session xtrace belong checkout installation version table budget
  first-source launch run served-by-the-paid-source flagged counted restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 881 RESULT\n' >&3
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
  local verdict=PASS
  case "$status" in
  0) verdict=PASS ;;
  1) verdict=FAIL ;;
  *) verdict=INCOMPLETE ;;
  esac
  printf 'CHECK 881 RESULT: %s (exit %s)\n' "$verdict" "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

usage() {
  cat <<'USAGE'
Check 881 — a live chain falls through from an exhausted pool to the key,
flagged and counted. Run by YOU, after release 1 is installed through the real
`fugaro update`. It launches ONE paid run.

Usage:
  bash scripts/live-checks/check-881-chain-falls-through.sh \
    --checkout <the repository's checkout> \
    [--pool <the pool the chain's first source resolves to>] \
    [--model <the model id the chain is for>] \
    [--installation <name>] [--gcp-project <project>] [--region <region>] \
    [--bucket <the runs bucket>] [--dry-run]
  bash scripts/live-checks/check-881-chain-falls-through.sh --dry-run
  bash scripts/live-checks/check-881-chain-falls-through.sh --help

Every row it prints is one criterion of Check 881; the last line of the block is
the verdict and the exit status:

  PASS (exit 0)        every criterion held
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

# ---------------------------------------------------------------------- inputs
CHECKOUT=${CHECK881_CHECKOUT:-}
POOL=${CHECK881_POOL:-}
MODEL=${CHECK881_MODEL:-}
INSTALLATION=${CHECK881_INSTALLATION:-}
GCP_PROJECT=${CHECK881_GCP_PROJECT:-}
REGION=${CHECK881_REGION:-}
BUCKET=${CHECK881_BUCKET:-}
DRY_RUN=${CHECK881_DRY_RUN:-}
WAIT_SECONDS=${CHECK881_WAIT_SECONDS:-3600}
POLL_SECONDS=${CHECK881_POLL_SECONDS:-20}
# REPO_NAME and DOCTOR_* are resolved from the checkout itself: the repository
# its origin remote names, and what its own `fugaro doctor --json` answers.
REPO_NAME=
DOCTOR_NAME=
DOCTOR_GCP=
DOCTOR_REGION=
BELONG_VERDICT=
BELONG_DETAIL=

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --checkout) [ $# -ge 2 ] || die "--checkout needs the checkout's path"; CHECKOUT=$2; shift 2 ;;
  --pool) [ $# -ge 2 ] || die "--pool needs the pool's name"; POOL=$2; shift 2 ;;
  --model) [ $# -ge 2 ] || die "--model needs the model id"; MODEL=$2; shift 2 ;;
  --installation) [ $# -ge 2 ] || die "--installation needs the installation's name"; INSTALLATION=$2; shift 2 ;;
  --gcp-project) [ $# -ge 2 ] || die "--gcp-project needs the project id"; GCP_PROJECT=$2; shift 2 ;;
  --region) [ $# -ge 2 ] || die "--region needs the Cloud Run region"; REGION=$2; shift 2 ;;
  --bucket) [ $# -ge 2 ] || die "--bucket needs the runs bucket's name"; BUCKET=$2; shift 2 ;;
  --dry-run) DRY_RUN=1; shift ;;
  *) die "unknown argument: $1" ;;
  esac
done

# The model the chain is read for: the one the table routes to the launcher's
# pool first, when --model did not name one. Read after the table, in
# step_table, because which entry that is is the table's own answer.
FUGARO_BIN=$(command -v fugaro || true)
[ -n "$FUGARO_BIN" ] || die "Check 881 needs 'fugaro' on PATH: it drives the build you have installed, and it is not there"
# No region default here: the region is the installation's own, read from the
# checkout's `fugaro doctor --json` (--region only has to agree with it). A
# default would read another installation's documents.

step_table_or_default_model() {
  # Without --model, the model is the one the table routes to the launcher's
  # pool first. One such entry is taken; several is a STOP, because the check
  # may not choose which chain it measures; none is the table failing this
  # check's own premise, and the row says what each entry starts at instead.
  [ -z "$MODEL" ] || return 0
  local rc=0
  (cd "$CHECKOUT" && "$FUGARO_BIN" routing ls --json) >"$SCRATCH/table.json" 2>"$SCRATCH/table.err" || rc=$?
  [ "$rc" = 0 ] || stop "fugaro routing ls --json failed in $CHECKOUT (exit $rc): $(head -c 300 "$SCRATCH/table.err" | oneline)"
  local listed rc2=0
  listed=$(python3 - "$SCRATCH/table.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
entries = doc.get("entries") if isinstance(doc, dict) else doc
for e in entries or []:
    if not isinstance(e, dict):
        continue
    match = e.get("match") or e.get("pattern") or ""
    chain = e.get("chain") or e.get("sources") or []
    kind = ""
    if chain and isinstance(chain[0], dict):
        kind = chain[0].get("kind") or ""
    if kind == "launcher-pool":
        print("cand|%s" % match)
    else:
        print("other|%s|%s" % (match, kind))
PY
)
  [ "$rc2" = 0 ] || stop "fugaro routing ls --json printed something this check cannot read as a table: $SCRATCH/table.json (C1's shape: entries with a match and a chain of sources)"
  local cands others n
  cands=$(printf '%s\n' "$listed" | sed -n 's/^cand|//p')
  others=$(printf '%s\n' "$listed" | sed -n 's/^other|//p')
  n=$(printf '%s\n' "$cands" | grep -c . || true)
  case "$n" in
  1)
    MODEL=$cands
    say "no --model was given: the check reads the one entry that routes a model to the launcher's pool first (${MODEL})"
    ;;
  0)
    local each="" line
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      each="${each}${each:+, }the entry '${line%%|*}' starts at ${line##*|}"
    done <<<"$others"
    fact table FAIL "no entry of the table routes a model to the launcher's pool first, so no chain in it can fall through from there${each:+ — }${each}. Check 881 measures a chain falling through FROM the launcher's pool to the key named after it, and without --model it also takes the model from that entry, so --model alone does not settle this: write one with 'fugaro routing set <pattern> launcher-pool anthropic' and run the check again"
    stop "no entry's chain starts with the launcher's pool"
    ;;
  *)
    stop "$n entries route a model to the launcher's pool first ($(printf '%s' "$cands" | oneline)): name the one this check is about with --model"
    ;;
  esac
}

main() {
  need_tools
  scratch_up
  trap on_error ERR
  guard_agent
  guard_xtrace
  step_belong
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_checkout
  step_table_or_default_model
  step_installation
  step_version
  step_table
  step_budget
  step_first_source
  step_launch
  step_run
  step_served_by_the_paid_source
  step_flagged
  step_counted
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
