#!/usr/bin/env bash
# Check 130 — the licence detectors after the open-core skeleton, as ONE script
# YOU run.
#
# It records the one thing no hermetic test in this tree can take: what GitHub's
# licence detectors make of the pair `LICENSE` and `LICENSING.md` side by side,
# and whether `LICENSING.md` is a page a contributor can read on GitHub with
# every link working. The choice behind that pair is the owner's **O2** answer
# of 2026-10-24 (decision **D301** as amended; docs/licensing/, task **L1.10**):
# the root `LICENSE` is not touched, so GitHub keeps reporting plain MIT, and
# the statement that `ee/` is excluded and reserved goes in a new root
# `LICENSING.md` beside it. A reading other than MIT is a finding against O2's
# premise and goes back to the owner — **no step here edits `LICENSE`**, and the
# script will not either. The register's row and section are
# docs/gcp-live-checklist.md, "Check 130"; the run is task **L1.11**'s, after
# task L1.3's `LICENSING.md` merged.
#
# Usage:      bash scripts/live-checks/check130.sh [--dry-run]
#             Run it from the checkout whose repository is to be read. The
#             repository is NOT typed in and no variable can name another one:
#             it is this checkout's origin, cross-checked against `go.mod`'s
#             module path, so the tree read here and the page read on GitHub are
#             the same repository.
#             --dry-run runs the local guards and the link list, prints the
#             commands a real run would run and what it would fetch — and
#             fetches NOTHING, so no request leaves the machine.
# Needs:      bash, mktemp, git, curl and python3 on PATH. Nothing is installed
#             and nothing is fetched but the three anonymous GETs below; if one
#             of those is missing the script stops with one line naming it.
# Where:      the public repository this checkout's origin names, read-only, and
#             nothing else: no cloud project, no installation, no credential,
#             no write of any kind, and never `belong`, EdgeWeb or EdgeServer —
#             a checkout whose origin names one of those three is refused by
#             name. Nothing belongs to this script but its own scratch
#             directory.
# Costs:      three anonymous `GET`s to api.github.com (part of the anonymous
#             rate limit and nothing else) and two page reads in your browser.
#             No money, no project, no model call and no credential.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory holding the
#             API's answers is removed, and the `restore` row says whether it
#             really went. Nothing else is created, so there is nothing else to
#             undo — nothing is enabled, disabled, granted or revoked anywhere.
# Output:     progress on stderr, ONE block on stdout headed `CHECK 130 RESULT`,
#             whose last line is `CHECK 130 RESULT: PASS|FAIL`. Exit 0 when
#             every row this script measures passed, 1 when one FAILed, 2 when
#             the run could not tell (a guard stopped it, a tool is missing, the
#             API did not answer).
#
# The two readings no script can take are the browser's, and they are printed as
# ONE `MANUAL:` line carrying the exact text to paste back: step 1, the
# repository page's own licence label and the file it opens beside a file list
# that carries both files; and step 3, `LICENSING.md` rendered as a page, with
# every relative link the `links` row lists followed to the file or heading it
# names. What this script takes is the machine-readable half of the same
# question — the licence API's own object — which is what makes it more than a
# reminder.
#
# The check's own criteria are quoted from its section (docs/gcp-live-checklist.md,
# "Check 130") and are what the assertions below assert. **Passes when:** "the
# licence API answers **200** with `spdx_id: "MIT"`, `key: "mit"` and **`path:
# "LICENSE"`** — the detector classified the untouched file and not the new
# one". **Fails when:** "the API's `spdx_id` is `other` or `NOASSERTION`, its
# `path` names `LICENSING.md` rather than `LICENSE`, or the endpoint answers
# 404, so that the licence GitHub reports is no longer the one `LICENSE`
# carries". A failing row says "record it and stop there — do not fix it by
# editing `LICENSE`", because the fix is a decision about the statement file's
# name or placement and it is the owner's, not this check's.
#
# There is no `CHECK130_*` variable of any kind: the repository comes from the
# checkout, the host is api.github.com and nothing here can be pointed
# elsewhere. The script's own test (scripts/live-checks/check130_test.sh) drives
# it entirely through fakes on PATH, with no network at all.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had. `bash -x check130.sh`
# is what someone reaches for when a run failed and they want a log to paste,
# and this script's commands carry the API's answers and the owner's own
# repository name: a trace would print them a second time, into the log. So the
# trace is turned off here, BASH_XTRACEFD is unset so a trace redirected to a
# file this script knows nothing about cannot be reopened, and guard_xtrace
# refuses a shell that arrived tracing (the flag sits in SHELLOPTS/BASHOPTS
# then, and every child would start tracing on its own). Check 90, Check 145 and
# Check 146 spell the three parts out; this is the same guard.
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
# finished run from one that ended on an error of its own. HELP is set when the
# argument was -h/--help, which asks nothing of the check and gets no block.
REACHED_END=no
HELP=""
# Set while the exit trap is running, so a second Ctrl-C is refused rather than
# killing this shell mid-cleanup: see on_signal.
CLEANUP_RUNNING=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

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
# all, and the block would then read as "the detector is fine" for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the detectors" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 130 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 130 needs bash"
  need mktemp
  # git reads the repository out of the checkout rather than out of memory, and
  # that is the guard the whole check rests on.
  need git "the repository this check reads is the checkout's own origin, not a name typed in"
  need curl "the three anonymous GETs to api.github.com are the check's machine-readable half"
  need python3 "the JSON answers are parsed by one python3 program, which prints the fields the block carries rather than the document: the object's path, its license block and its message"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk130-mine"
SCRATCH_REMOVED=no
PRINTED=""
MANUAL_LINE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk130.XXXXXXXX") || die "Check 130 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 130 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
  CLEANUP_RUNNING=yes
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
  # --help measured nothing and asked nothing: it printed the usage and it gets
  # no block and no verdict, only its own exit status.
  [ -n "$HELP" ] && exit 0
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it:
  # bash exits by itself for an error the ERR trap does not see (an unbound
  # variable, a bad substitution), and a block that then read as a finished run
  # would offer a row of FAILs as a verdict about the detectors when the steps
  # that would have filled them never ran. Any such ending is an abort, and 2
  # says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the detectors"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and nothing anywhere was enabled, disabled, granted or revoked"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    local note="this check created nothing and changed nothing anywhere — no project, no installation, no credential and no file of yours — and its scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
    facts="${facts}"$'\n'"restore|$restore|${note}"
  fi
  # The restore row is a row this run measured, and main decided its status from
  # the facts file before the row existed: a block whose last line is
  # `CHECK 130 RESULT: FAIL` next to an exit status of 0 is the check telling two
  # stories, and the section promises the status the block gives. So a finished
  # run whose verdict is FAIL exits 1 — and only that: 2 (the run could not tell)
  # and 130 (interrupted) keep their meanings.
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ] && [ "$(verdict_of "$facts")" = FAIL ]; then
    status=1
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check130.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run is printed a second time into a log"
}

# guard_checkout: the repository, from this checkout's origin rather than from
# memory, and from nowhere else — there is no argument and no variable that
# could name a repository other than the one you are standing in.
guard_checkout() {
  ROOT=$(git rev-parse --show-toplevel 2>/dev/null) \
    || stop "Check 130 stopped: this is not a git checkout (git rev-parse --show-toplevel failed), and the repository to read is that checkout's own origin. Run it from the Fugaro checkout: cd <checkout> && bash scripts/live-checks/check130.sh"
  ORIGIN=$(git -C "$ROOT" remote get-url origin 2>/dev/null) \
    || stop "Check 130 stopped: this checkout has no origin remote, so which repository the detectors are read about is unknown. Run it from a checkout whose origin is the repository to read"
  case "$ORIGIN" in
  *github.com[:/]*/*) : ;;
  *) stop "Check 130 stopped: this checkout's origin ('${ORIGIN}') names no github.com repository, so which repository to read is unknown" ;;
  esac
  REPO=$(printf '%s' "$ORIGIN" | sed -E 's#^.*github\.com[:/]##; s#\.git$##')
  [ -n "$REPO" ] || stop "Check 130 stopped: this checkout's origin names no github.com repository, so which repository to read is unknown"
  say "repository: github.com/${REPO} (origin ${ORIGIN})"
}

# guard_belong: `belong` is the owner's own installation and EdgeWeb and
# EdgeServer are its repositories. This check reads a public repository as an
# anonymous visitor and has no command that could touch a project — but a
# checkout is where it gets its subject from, so a checkout whose origin names
# one of the three is refused by name rather than read, exactly as Check 145
# refuses the workspace `belong` and Check 146 refuses the installation. This
# guard comes before the module cross-check rather than after it: the refusal is
# a standing rule and has to be the reason a run ended, not a stop that another
# guard's message could hide behind.
guard_belong() {
  local owner=${REPO%%/*} name=${REPO##*/} low
  low=$(printf '%s' "$owner/$name" | tr 'A-Z' 'a-z')
  case "/${low}/" in
  */belong/* | */edgeweb/* | */edgeserver/*)
    fact belong FAIL "this checkout's origin names github.com/${REPO}, which is the owner's own installation's (`belong`, EdgeWeb or EdgeServer): Check 130 reads a public repository and no step of it may touch them"
    stop "Check 130 never runs against 'belong', EdgeWeb or EdgeServer. Run it in the checkout of the repository whose detectors you mean to read: cd <checkout> && bash scripts/live-checks/check130.sh"
    ;;
  esac
  fact belong PASS "the checkout's repository is github.com/${REPO}, which is not 'belong', EdgeWeb or EdgeServer; this check names no cloud project, no installation and no credential"
}

# guard_module: the cross-check. The detectors answer about the repository the
# module lives in, and a tree paired with another repository's answer is the one
# mistake this guard exists to stop — so the repository row is recorded here,
# where the origin and `go.mod` have been shown to agree.
guard_module() {
  MOD=$(sed -n 's|^module[[:space:]]*||p' "$ROOT/go.mod" 2>/dev/null | head -n 1)
  [ "$MOD" = "github.com/${REPO}" ] \
    || stop "Check 130 stopped: go.mod's module path is ${MOD:-none} and this checkout's origin is ${REPO}: the two disagree, so which repository the detectors are read about is ambiguous"
  fact repository PASS "github.com/${REPO} — read from this checkout's origin (${ORIGIN}) and cross-checked against go.mod's module path (${MOD}), so the tree read here and the page read on GitHub are the same repository"
}

# guard_licensing_md: task L1.3's statement file, and step 3's subject. A tree
# without it has nothing for the browser reading to open, and the honest answer
# is the stop rather than a run that reports half a check.
guard_licensing_md() {
  LINK_FILE="$ROOT/LICENSING.md"
  [ -f "$LINK_FILE" ] \
    || stop "Check 130 stopped: this tree has no LICENSING.md (task L1.3 has not merged), so the reading of the rendered statement has nothing to open; run this check after L1.3 merges (task L1.11)"
  fact licensing-md PASS "LICENSING.md is in the tree at ${LINK_FILE} ($(wc -c <"$LINK_FILE" | tr -d ' ') bytes) — the statement task L1.3 writes, whose links the MANUAL line's second reading follows"
}

# guard_links: the links step 3 follows, read OUT of LICENSING.md rather than
# remembered — they are the ones task L1.3's statement pins, and a list kept
# here instead would be a second copy that could drift from the file. The
# targets are returned verbatim (a fragment included), because what the browser
# reading checks is where each one LANDS.
guard_links() {
  local raw stripped
  raw=$(grep -oE '\]\([^)]+\)' "$LINK_FILE" || true)
  [ -n "$raw" ] \
    || stop "Check 130 stopped: LICENSING.md carries no link at all, so the rendered page would have none to follow and step 3 would measure nothing"
  LINKS=()
  while IFS= read -r stripped; do
    [ -n "$stripped" ] && LINKS+=("$stripped")
  done < <(printf '%s\n' "$raw" | sed -E 's/^\]\((.*)\)$/\1/')
  say "links LICENSING.md carries, to follow in the browser:"
  local l
  for l in "${LINKS[@]}"; do say "  ${l}"; done
  fact links PASS "${#LINKS[@]} relative link(s) read out of LICENSING.md, to follow in the browser one at a time: $(printf '%s, ' "${LINKS[@]}" | sed 's/, $//')"
}

# ------------------------------------------------------------- the API reads
# read_licence_api FILE: the fields step 2 records, out of the endpoint's
# answer. The program prints one `key|value` line per field rather than echoing
# the document: what reaches the block is this fixed vocabulary — the path, the
# licence's `spdx_id`, `key` and `name`, the `message` — plus the compact object
# itself, and nothing else that arrived.
read_licence_api() { # read_licence_api JSON-FILE
  python3 - "$1" >"$SCRATCH/licence.fields" 2>"$SCRATCH/python.err" <<'PY'
import json, sys

try:
    d = json.load(open(sys.argv[1]))
except Exception as exc:  # the endpoint answered something that is not JSON
    print("error|%s" % exc)
    sys.exit(0)
if not isinstance(d, dict):
    print("error|the answer is not a JSON object")
    sys.exit(0)

lic = d.get("license") or {}
print("object|" + json.dumps(
    {"path": d.get("path"), "license": d.get("license"), "message": d.get("message")},
    sort_keys=True, separators=(",", ":")))
print("path|" + str(d.get("path") or ""))
print("spdx_id|" + str(lic.get("spdx_id") or ""))
print("key|" + str(lic.get("key") or ""))
print("name|" + str(lic.get("name") or ""))
print("message|" + str(d.get("message") or ""))
PY
  [ -s "$SCRATCH/licence.fields" ] || stop "Check 130 stopped: the licence endpoint's answer could not be parsed, so the reading step 2 records cannot be taken"
}

# field_of FILE KEY: the one line of a `key|value` file, without the key.
field_of() { # field_of FILE KEY
  sed -n "s/^$2|//p" "$1" | head -n 1
}

# ------------------------------------------------------------ step 2 and setup
# The three anonymous GETs the section's blocks run, in the order they run them:
# the repository's own metadata (the default branch step 3's URL is built on),
# the status of the licence endpoint (whether GitHub classifies a licence file
# at all), and the licence object itself. The bodies are fetched FIRST and
# parsed SECOND: a pipe into python3 would print a traceback when the endpoint
# answers nothing, which reads like a broken check rather than the stop it is.
step_detector_machine_readable() {
  local url="https://api.github.com/repos/${REPO}"
  local err="$SCRATCH/curl.err"

  curl -sS -f "$url" >"$SCRATCH/meta.json" 2>"$err" \
    || stop "Check 130 stopped: api.github.com did not answer for ${REPO} — no network, or the repository is not public (curl's own line above says which)"
  BRANCH=$(python3 -c '
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(3)
print(d.get("default_branch", "") if isinstance(d, dict) else "")
' "$SCRATCH/meta.json" 2>/dev/null) \
    || stop "Check 130 stopped: api.github.com answered for ${REPO} with something that is not the repository JSON object, so the default branch step 3's URL is built on cannot be read"
  [ -n "$BRANCH" ] || stop "Check 130 stopped: the API answered for ${REPO} with no default_branch, so step 3's URL cannot be built"

  # Whether GitHub classifies a licence file at all. A 404 here is the finding
  # step 2 records and is NOT a reason to stop; 403 and 429 are the anonymous
  # rate limit, which is, and so is anything else.
  CODE=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Accept: application/vnd.github+json' "$url/license" 2>"$err") \
    || stop "Check 130 stopped: api.github.com did not answer for ${REPO}'s licence — no network, or the repository is not public (curl's own line above says which)"
  case "$CODE" in
  200) : ;;
  404) say "the licence endpoint answers 404 for ${REPO}: GitHub classifies no file in it as a licence at all — read in full below" ;;
  403 | 429) stop "Check 130 stopped: the licence endpoint answered ${CODE} — the anonymous rate limit is the usual cause. Wait for it to turn and re-run: this check holds no credential and needs none" ;;
  *) stop "Check 130 stopped: the licence endpoint answered ${CODE} for ${REPO}" ;;
  esac

  curl -sS -H 'Accept: application/vnd.github+json' "$url/license" >"$SCRATCH/licence.json" 2>"$err" \
    || stop "Check 130 stopped: the licence endpoint's answer could not be read (curl's own line above says why)"
  read_licence_api "$SCRATCH/licence.json"

  local error object path spdx key message
  error=$(field_of "$SCRATCH/licence.fields" error)
  [ -z "$error" ] || stop "Check 130 stopped: the licence endpoint answered ${REPO} with something that is not the JSON object it answers with (${error}), so step 2's reading cannot be taken"
  object=$(field_of "$SCRATCH/licence.fields" object)
  path=$(field_of "$SCRATCH/licence.fields" path)
  spdx=$(field_of "$SCRATCH/licence.fields" spdx_id)
  key=$(field_of "$SCRATCH/licence.fields" key)
  message=$(field_of "$SCRATCH/licence.fields" message)

  # The check's own setup line, on the block rather than only on the terminal:
  # it is where the repository and the branch every reading below is about are
  # on the record, and the paste-back asks for it by name.
  fact setup PASS "CHECK 130 SETUP: repository ${REPO}, default branch ${BRANCH}, licence API HTTP ${CODE}"

  if [ "$CODE" != 200 ]; then
    fact licence-api FAIL "the licence endpoint answered HTTP ${CODE} for ${REPO}${message:+, saying '${message}'}: GitHub classifies no file in it as a licence at all, so the licence it reports is no longer the one LICENSE carries (the section's fails-when clause). The object verbatim: ${object}. Record it and stop there — do not fix it by editing LICENSE (O2): the finding goes back to the owner, whose choice is between the statement file's name and its placement"
  elif [ "$spdx" = MIT ] && [ "$key" = mit ] && [ "$path" = LICENSE ]; then
    fact licence-api PASS "${object} — HTTP 200 with spdx_id MIT, key mit and path LICENSE: the detector classified the untouched LICENSE and not the new LICENSING.md"
  else
    fact licence-api FAIL "${path:-<no path>} / spdx_id ${spdx:-<none>} / key ${key:-<none>} — the object verbatim: ${object}. The reading that passes is spdx_id MIT, key mit and path LICENSE, and the path is the half a reader would otherwise skip: a path of LICENSING.md, or an spdx_id of other or NOASSERTION, means the detector read the new statement file rather than the untouched LICENSE, and O2's premise has failed. Record it and stop there — do not fix it by editing LICENSE (O2; the plan's Conventions): this goes back to the owner, whose choice is then between the statement file's name and its placement"
  fi
}

# ------------------------------------------------------------- the manual line
# step_manual: the ONE line for the readings no script can take. It names the
# two pages, says what to read on each, and carries the exact text to paste
# back, because the register's row is written from what you saw and not from
# what this script measured.
step_manual() {
  MANUAL_LINE="open https://github.com/${REPO} and read the About box's licence label VERBATIM and the file it opens, and that the file list beside it carries BOTH LICENSE and LICENSING.md — then open https://github.com/${REPO}/blob/${BRANCH}/LICENSING.md and follow the ${#LINKS[@]} link(s) the links row lists, one at a time (each must land on the file it names; README.md#trademarks must land on the trademark paragraph, not the top of the page). Paste back, with this block: the label verbatim; the file it opened; both file names; that LICENSING.md rendered as a page; per link its rendered href and where it landed; the paragraph the README anchor opened; your browser and its version, and the date"
}

# ------------------------------------------------------------------- the plan
# print_plan: what a real run would run and fetch, in order, and what its
# Restore would remove. Printed by --dry-run; nothing here is run.
print_plan() {
  say "dry run: NOTHING below was run, and no request left this machine. A real run would run, in this order:"
  say "  [network] curl -sS -f https://api.github.com/repos/${REPO} (the repository's metadata: the default branch step 3's URL is built on)"
  say "  [network] curl -sS -o /dev/null -w '%{http_code}' -H 'Accept: application/vnd.github+json' https://api.github.com/repos/${REPO}/license (whether GitHub classifies a licence file at all: 200 and 404 are read, 403 and 429 are the anonymous rate limit and stop the run)"
  say "  [network] curl -sS -H 'Accept: application/vnd.github+json' https://api.github.com/repos/${REPO}/license (the object step 2 records verbatim)"
  say "  [local] python3 parses the object into the fixed vocabulary the block prints: path, spdx_id, key, name, message"
  say "  [local] the assertion on it: HTTP 200 with spdx_id MIT, key mit and path LICENSE — the detector classified the untouched file and not the new one"
  say "  [your browser] step 1: https://github.com/${REPO} — the licence label, the file it opens, and both LICENSE and LICENSING.md in the file list"
  say "  [your browser] step 3: https://github.com/${REPO}/blob/<default branch>/LICENSING.md — rendered, and each of the ${#LINKS[@]} link(s) the guard read out of it followed: $(printf '%s, ' "${LINKS[@]}" | sed 's/, $//')"
  say "and its Restore (also from the exit trap) would remove: the scratch directory ${SCRATCH} and nothing else — this check creates nothing, enables nothing, disables nothing and grants nothing, so no API is left off and no role is left removed by a failed step"
}

# ------------------------------------------------------------------- reporting
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL — not reached. Any other recorded key follows in the order the run
# recorded it. The last line is the verdict the task asks for, and the MANUAL
# line sits just above it.
BLOCK_KEYS=(setup repository belong licensing-md links licence-api restore)

# verdict_of FACTS: PASS unless one of the rows FAILed. A row that was never
# reached printed as FAIL by print_block is written into the facts by finish()
# as an abort, so a stopped run does not read as a pass.
verdict_of() { # verdict_of FACTS
  if printf '%s\n' "$1" | grep -qE '^[a-z0-9-]+\|FAIL\|'; then
    printf 'FAIL\n'
  else
    printf 'PASS\n'
  fi
}

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 130 RESULT\n' >&3
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
  if [ -n "$MANUAL_LINE" ]; then
    printf 'MANUAL: %s\n' "$MANUAL_LINE" >&3
  else
    printf 'MANUAL: not reached — this run stopped before the check’s two browser readings were printed, so steps 1 and 3 of Check 130 are NOT measured by it: re-run it, and read them from the line a finished run prints\n' >&3
  fi
  printf 'CHECK 130 RESULT: %s\n' "$(verdict_of "$facts")" >&3
}

# interrupted: exit 130 and let the EXIT trap print the block and clean up.
interrupted() {
  owner "interrupted"
  exit 130
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed
# — including through the cleanup, which is where its second answer matters. A
# second Ctrl-C during the Restore would otherwise kill this shell mid-cleanup
# and the block that says what was left behind would never be printed.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  interrupted
}

# ----------------------------------------------------------------------- main
USAGE="check130.sh [--dry-run]"

parse_args() {
  DRY_RUN=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --dry-run) DRY_RUN=1 ;;
    -h | --help) HELP=1 ;;
    *) die "unknown argument '$1'. usage: $USAGE" ;;
    esac
    shift
  done
}

main() {
  need_tools
  parse_args "$@"
  if [ -n "$HELP" ]; then
    printf 'usage: %s\n' "$USAGE"
    return 0
  fi
  scratch_up
  guard_xtrace
  guard_checkout
  guard_belong
  guard_module
  guard_licensing_md
  guard_links

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "guards and the link list only; the commands a real run would run — the three anonymous GETs and the two browser readings — were printed, not run, and no request left this machine"
    fact setup INFO "not taken: this is a dry run, so nothing was fetched and the default branch and the licence endpoint's HTTP code are what a real run would read"
    # Both rows a real run fills from the API say they were not taken rather
    # than reading `FAIL — not reached`: a dry run did not stop, it was asked
    # for nothing, and a block of FAILs would read as a finding about the
    # detectors.
    fact licence-api INFO "not taken: this is a dry run, so the licence object was not fetched — a real run asserts HTTP 200 with spdx_id MIT, key mit and path LICENSE on it"
    # The manual line is a string, not a step: printing it in a dry run shows
    # exactly what a real run asks you to paste back — with the branch the API
    # would report left as the placeholder a dry run cannot fill in.
    BRANCH='<the default branch a real run reads>'
    step_manual
  else
    step_detector_machine_readable
    step_manual
  fi

  REACHED_END=yes
  if printf '%s\n' "$(cat "$FACTS_FILE")" | grep -qE '^[a-z0-9-]+\|FAIL\|'; then
    exit 1
  fi
  return 0
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
