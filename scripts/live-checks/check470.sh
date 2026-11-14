#!/usr/bin/env bash
# Checks 470, 471 and 472 — `fugaro issue mine`'s shared lookup, its offline
# path and its listing at size — as ONE script YOU run, one mode each.
#
# The three numbers are the issue-mine work's (docs/issue-mine/
# implementation-plan.md §"Checks", reserved Checks 470–479), and the plan
# writes each as its own check: 470 the shared lookup across machines and
# people, 471 the offline path, 472 the listing at size. What the plan cannot
# say is that two of the three are one machine's work and one is not, so this is
# one script with three modes: the guards, the Restore, the block and the
# reading of the cache are the same for all three, and only the steps differ.
# The register's three rows and three sections (docs/gcp-live-checklist.md,
# "Check 470", "Check 471", "Check 472") each name the mode they run.
#
#   --mode 470 (the default) — the shared lookup across machines and people.
#     What it measures: whether this machine's `fugaro issue mine` really reads
#     the project's store end to end (the run's own `source: shared`), and
#     whether the signed-in login may list the two prefixes the store's own
#     reader lists — `issues/reporters/` and `issues/occurrences/` — and read a
#     record under `issues/records/` (the reconciliation's U1, F7). The store
#     lives in the project's runs bucket and is written by every launcher, so a
#     launcher's login that cannot list those prefixes is a `mine` that silently
#     shows local rows only.
#     What it does NOT measure, because no one machine can: the second machine
#     and the second person. Those stay numbered steps in the register's Check
#     470 section, the script prints them as a prompt and says it does not run
#     them, and nothing about them is faked into a row.
#
#   --mode 471 — offline. After one online `fugaro issue mine` (which writes the
#     snapshot the offline run stands on), the script asks you to turn the
#     network OFF, then runs `fugaro issue mine` again: it must print
#     `offline: shared state as of <time>` within a few seconds (the plan's
#     acceptance 3, and U3's question: how long the credential lookup takes with
#     no network). Then, with the cache cleared — the script moves the snapshot
#     directory aside itself — it runs once more and must print
#     `no snapshot of the project's issues is cached`. It then asks for the
#     network back. Turning the network off is the one thing here a script
#     cannot do for you, so it is a prompt and never faked: `--no-prompt` says
#     you have already done it (and the rows then read what they read).
#
#   --mode 472 — the listing at size. `fugaro issue mine --json` on the project
#     with the most issues, timed: the number of rows, the seconds the whole
#     command took, and whether `truncated` is true (U2's question, and whether
#     the 500-issue and 1000-name caps are right). The readings are the answer
#     and are printed as one line above the block to copy back.
#
# Every mode runs `fugaro issue mine` in the checkout you name, so the project
# and the store are the checkout's own; `--bucket` is the runs bucket of that
# project, and mode 470 is the only mode that reads it directly.
#
# `fugaro issue mine` is not read-only, and this check does not pretend it is:
# it uploads this machine's own pending issues to the project's store (the write
# the command makes whenever you run it) and it files two local caches — the
# identity cache and the offline snapshot. The Restore puts both caches back the
# way it found them; an upload is not undone, because the store has no delete
# and the upload is the command's own behaviour rather than this check's.
#
# Usage:      bash scripts/live-checks/check470.sh --checkout <dir> --project <project>
#                 --bucket <bucket> [--mode 470|471|472] [--dry-run] [--no-prompt]
#             --checkout is the sandbox repository's checkout: the directory
#             `fugaro issue mine` is run in, which is what decides the project
#             and the store it reads (its own fugaro.yaml and this machine's
#             config). --project and --bucket are the installation's GCP project
#             and the runs bucket that holds `issues/` — typed by you, never read
#             from a config that may name another. `--dry-run` prints every
#             command a real run would make and runs nothing at all: no fugaro
#             call, no gcloud call, no prompt.
# Needs:      bash, mktemp, sed, grep, cmp, diff and `fugaro` on PATH. Mode 470
#             also needs `gcloud`, which is what reads the store directly. Nothing
#             is installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it.
# Where:      a SANDBOX installation only, and NEVER an installation named
#             `belong`, `edge-devel-dimi` or `edgeappinc`: those three names are
#             refused in the project and in the bucket before anything is run
#             (the same three check96.sh refuses), and the refusal is what the
#             script's own test pins. This check writes nothing to the project's
#             store of its own and creates nothing in the cloud: what it writes
#             is the two local caches the command writes, and the Restore puts
#             them back.
# Costs:      nothing billable and no model call: one to three `fugaro issue
#             mine` runs (a listing, plus one upload of this machine's own
#             pending issues if it has any), two or three store reads, and one
#             record read. No run is launched and no image is built.
# Runs:       by YOU. No agent runs any step of it, and the test beside it drives
#             fake tools only.
# Cleanup:    always, including on a Ctrl-C or on a failed fact: the two cache
#             files are restored to the bytes this run found (or removed when
#             they were not there), the scratch directory is removed, and the
#             Restore reads both back and says what it found. An upload to the
#             store is not undone — see above.
# Output:     progress on stderr, ONE block on stdout headed `CHECK 470 RESULT`
#             (or 471/472, the mode), and for mode 472 one line above it with the
#             numbers to copy back. Exit 0 when every fact this mode is about
#             held AND the Restore confirmed the cache, 1 when one of them
#             failed, 2 when the run could not tell (a guard stopped it, a tool
#             is missing, a call that did not answer, an internal error).
#
# Nothing secret is handled anywhere in it as a value: no credential is read,
# printed or traced, and the only addresses that reach the block are the ones
# the store's own objects and the CLI's own rows carry.
#
# CHECK470_OFFLINE_MAX_SECONDS exists for this script's own test: it is how long
# an offline `fugaro issue mine` may take before the offline row fails, and it
# can point the check at no other project, bucket or checkout.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all.
set -E

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by step_verdict once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
# The line the block prints above its header: the numbers mode 472 exists to
# produce, and empty for the two modes that answer with their rows.
ANSWER=""
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
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as an answer about the store for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the issue store"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check $MODE needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check $MODE needs bash"
  need mktemp
  need sed "the --json document and the two cache files are read with sed"
  need grep
  need cmp "the Restore reads the identity cache back against the copy it saved"
  need diff "the Restore reads the snapshot directory back against the copy it saved"
  need fugaro "every reading this check takes is of the CLI's own command: fugaro issue mine, run in the checkout"
  if [ "$MODE" = 470 ]; then
    need gcloud "mode 470 reads the project's issue store with YOUR own identity: the two listings and the one record read of U1"
  fi
}

# How long an offline `fugaro issue mine` may take before its row fails. The
# plan's own words are "in a few seconds" and the CLI bounds its credential
# lookup at 5 s, so a run that spends the whole network timeout is the reading
# U3 is asking for rather than a pass.
OFFLINE_MAX_SECONDS=${CHECK470_OFFLINE_MAX_SECONDS:-30}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk470-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""
# The user's cache directory as the CLI itself resolves it (localcfg.UserDir
# with DirCache): $XDG_CACHE_HOME when it is absolute, else $HOME/.cache, and the
# `fugaro` directory under it. The two files this check may change live there:
# identity.json (the cached address) and mine/<digest>.json (the snapshot).
CACHE=""
CACHE_TOUCHED=no
CACHE_IDENTITY_SAVED=no
CACHE_MINE_SAVED=no
# This run's own readings, kept so the block's rows and the answer line can be
# written from what was measured rather than from what was expected.
MINE_STATUS=0
OFFLINE_SECONDS=0
SIZE_SECONDS=0
SIZE_ROWS=0
SIZE_TRUNCATED=""
HELPED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk470.XXXXXXXX") || die "Check $MODE cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check $MODE stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  cache_dir || die "Check $MODE cannot find the user's cache directory: set XDG_CACHE_HOME or HOME to an absolute path and run again"
  save_cache
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  say "the cache this run may touch: $CACHE — its identity.json and its mine/ directory are saved aside and put back by the Restore"
}

# cache_dir: the fugaro directory in the user's cache, resolved the way
# localcfg.UserDir resolves it, so the Restore is about the files the CLI
# itself wrote and not about a path this script guessed at.
cache_dir() {
  CACHE=""
  case "${XDG_CACHE_HOME:-}" in
  /*) CACHE=$XDG_CACHE_HOME ;;
  esac
  if [ -z "$CACHE" ]; then
    case "${HOME:-}" in
    /*) CACHE=$HOME/.cache ;;
    esac
  fi
  [ -n "$CACHE" ] || return 1
  CACHE="$CACHE/fugaro"
  return 0
}

# save_cache: copies the two files aside BEFORE the first fugaro call, so the
# Restore restores bytes rather than a shape it hopes for. A run that never
# reaches a fugaro call has nothing to restore, which is why the flag is set by
# the caller rather than here.
save_cache() {
  if [ -f "$CACHE/identity.json" ]; then
    cp "$CACHE/identity.json" "$SCRATCH/identity.json.saved" || die "Check $MODE cannot save the identity cache aside ($CACHE/identity.json)"
    CACHE_IDENTITY_SAVED=yes
  fi
  if [ -d "$CACHE/mine" ]; then
    cp -R "$CACHE/mine" "$SCRATCH/mine.saved" || die "Check $MODE cannot save the snapshot directory aside ($CACHE/mine)"
    CACHE_MINE_SAVED=yes
  fi
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  restore_run
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
  # exit trap, where a failing `rm` under `set -e` would end the shell before the
  # block is printed.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# --------------------------------------------------------------- the Restore
# The Restore puts the two cache files back the way this run found them and reads
# them back afterwards. It runs from the exit trap, so it has to tolerate a run
# that stopped anywhere: every command is guarded — a failed `cp` must not end
# the shell before the block is printed — and what it could not confirm is a
# FAILed row rather than a silent pass.
restore_run() {
  if [ "$CACHE_TOUCHED" != yes ]; then
    fact restore PASS "nothing of this check's was created or changed: no fugaro call was made, so the two cache files this check can touch ($CACHE/identity.json and $CACHE/mine/) are exactly as they were, and nothing was uploaded"
    return 0
  fi
  say "Restore: putting the cache back the way this run found it"
  local bad="" left
  # 1. The identity cache: the run rewrites it whenever the credential lookup
  #    answered, so it is removed and put back from the copy this run saved — or
  #    removed outright when it was not there before.
  rm -f "$CACHE/identity.json" 2>/dev/null || true
  if [ "$CACHE_IDENTITY_SAVED" = yes ]; then
    cp "$SCRATCH/identity.json.saved" "$CACHE/identity.json" 2>/dev/null || true
  fi
  # 2. The snapshots: one file per installation, written by `mine` alone, so the
  #    whole directory is this check's to put back.
  rm -rf "$CACHE/mine" 2>/dev/null || true
  if [ "$CACHE_MINE_SAVED" = yes ]; then
    cp -R "$SCRATCH/mine.saved" "$CACHE/mine" 2>/dev/null || true
  fi
  # 3. Read both back. Only a byte-for-byte match counts as restored: a file that
  #    is there but different is a cache this run changed and did not put back,
  #    which would make the NEXT `mine` print this check's rows as the machine's
  #    own state.
  if [ "$CACHE_IDENTITY_SAVED" = yes ]; then
    if [ ! -f "$CACHE/identity.json" ] || ! cmp -s "$SCRATCH/identity.json.saved" "$CACHE/identity.json"; then
      bad="$bad the identity cache ($CACHE/identity.json is not the file this run found; the copy it found is $SCRATCH/identity.json.saved)"
    fi
  elif [ -e "$CACHE/identity.json" ]; then
    bad="$bad the identity cache ($CACHE/identity.json did not hold a regular file when this run started, so this check could not save it aside, and something still holds that name)"
  fi
  if [ "$CACHE_MINE_SAVED" = yes ]; then
    if [ ! -d "$CACHE/mine" ] || ! diff -r "$SCRATCH/mine.saved" "$CACHE/mine" >/dev/null 2>&1; then
      bad="$bad the snapshot directory ($CACHE/mine is not what this run found; the copy it found is $SCRATCH/mine.saved)"
    fi
  elif [ -e "$CACHE/mine" ]; then
    left=$(find "$CACHE/mine" -type f 2>/dev/null | tr '\n' ' ')
    bad="$bad the snapshot directory ($CACHE/mine was not a directory this check could save aside when the run started, and it holds $left now)"
  fi
  if [ -n "$bad" ]; then
    fact restore FAIL "this check's Restore could not put the cache back the way it found it:$bad. Put it back by hand before running any other check: the identity cache is $CACHE/identity.json and the snapshots are the files under $CACHE/mine/, one per installation. A snapshot this check left behind is not harmless — the next offline \`mine\` would print this run's rows as the machine's own state"
    return 0
  fi
  # The PASS row says only what was READ, and it says the one thing the Restore
  # does NOT cover, because a check that read as "everything is as it was" while
  # having uploaded an issue would be the wrong sentence: `mine` uploads this
  # machine's own pending issues, and the store has no delete.
  local shape
  if [ "$CACHE_IDENTITY_SAVED" = yes ] && [ "$CACHE_MINE_SAVED" = yes ]; then
    shape="both cache files were there before this run and both are back byte for byte (read back with cmp and diff)"
  elif [ "$CACHE_IDENTITY_SAVED" = yes ]; then
    shape="the identity cache is back byte for byte (read back with cmp) and no snapshot directory was left behind"
  elif [ "$CACHE_MINE_SAVED" = yes ]; then
    shape="the snapshot directory is back as it was (read back with diff) and no identity cache was left behind"
  else
    shape="neither cache file existed before this run and neither exists now"
  fi
  fact restore PASS "the two cache files this check can touch are as this run found them — $shape. NOT undone, and not this check's to undo: \`fugaro issue mine\` uploads this machine's own pending issues to the project's store (the write the command makes whenever you run it, and the store has no delete), and the local issue files it marked uploaded in the state directory are the command's own effect"
}

# ------------------------------------------------------------------ the guards
# guard_names: the three installations this check must never touch are refused by
# NAME, in the project and in the bucket, before a single call. It is a substring
# test on purpose — a project or bucket that merely contains one of the names is
# refused too, and the message says which one matched — and the three are the
# ones check96.sh refuses: `belong` is the owner's own installation, and
# `edge-devel-dimi` and `edgeappinc` are the projects it runs in.
guard_names() {
  local name
  for name in "$P" "$B"; do
    case "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" in
    *belong* | *edge-devel-dimi* | *edgeappinc*)
      fact guard FAIL "'$name' names an installation this check never touches (never belong, edge-devel-dimi or edgeappinc): this check reads a project's issue store with your own identity and runs the CLI's own command against it, so it is a SANDBOX check"
      stop "the name '$name' matches an installation this check must never touch (belong, edge-devel-dimi or edgeappinc)"
      ;;
    esac
  done
  fact guard PASS "neither the project ($P) nor the bucket ($B) names an installation this check must never touch (nothing typed contains 'belong', 'edge-devel-dimi' or 'edgeappinc')"
}

# guard_checkout: the directory `fugaro issue mine` is run in has to be the
# checkout of a repository that carries a fugaro.yaml, because that file is what
# says which project the command reads — a directory without one makes `mine`
# answer "no project with a runs bucket is selected" and this check would then be
# measuring a machine with no config rather than a store.
guard_checkout() {
  if [ ! -d "$CO" ]; then
    fact checkout FAIL "'$CO' is not a directory: --checkout has to be the sandbox repository's checkout, because that is the directory \`fugaro issue mine\` is run in"
    stop "'$CO' is not a directory"
  fi
  if [ ! -f "$CO/fugaro.yaml" ]; then
    fact checkout FAIL "'$CO' carries no fugaro.yaml, so it is not the checkout of a repository: \`fugaro issue mine\` reads the project out of that file, and a run from here would answer 'no project with a runs bucket is selected' rather than reading a store"
    stop "'$CO' carries no fugaro.yaml, so it is not a repository checkout"
  fi
  fact checkout PASS "$CO is a directory carrying a fugaro.yaml, so \`fugaro issue mine\` is run there and reads the project that file names"
}

# ------------------------------------------------------------------- the reader
# The --json document is Go's encoding/json with a two-space indent, so every
# top-level member is one line at indent 2 and every row member is one at indent
# 6: matching the indent is what keeps a row's own "display" or "number" from
# being read as the document's. Nothing here parses JSON; it reads the five
# members this check is about, and a member it cannot find is the empty string —
# a document this build cannot read is a FAILed row, never a silent zero.
json_str() { # FILE KEY
  sed -n "s/^  \"$2\": \"\([^\"]*\)\".*/\1/p" "$1" 2>/dev/null | head -n 1
}
json_num() { # FILE KEY
  sed -n "s/^  \"$2\": \([0-9][0-9]*\).*/\1/p" "$1" 2>/dev/null | head -n 1
}
json_bool() { # FILE KEY
  sed -n "s/^  \"$2\": \(true\|false\).*/\1/p" "$1" 2>/dev/null | head -n 1
}
# json_rows: how many issue rows the document holds. `number` is a member of a
# row and of nothing else in this document (the counts are `hidden` and
# `others_hit`), so counting its occurrences counts rows.
json_rows() { # FILE
  local n
  n=$(grep -o '"number":' "$1" 2>/dev/null | grep -c . || true)
  printf '%s' "${n:-0}"
}
# first_number: the first row's own issue number, which is the issue this run's
# user is on in the store — the object mode 470 reads back under issues/records/.
first_number() { # FILE
  grep -o '"number": [0-9][0-9]*' "$1" 2>/dev/null | sed -n 's/.*: //p' | head -n 1 || true
}
# bounded: one line, capped, for a fact's detail. A failure a row quotes is the
# tool's own words, and a whole document on one row would push the rows that
# matter off the paste-back.
bounded() { # TEXT MAX
  local t
  t=$(printf '%s' "$1" | tr '\n' ' ' | sed 's/  */ /g')
  if [ "${#t}" -gt "$2" ]; then printf '%s…' "${t:0:$2}"; else printf '%s' "$t"; fi
}
fact_verdict() { # KEY
  sed -n "s/^$1|\([A-Z]*\)|.*/\1/p" "$FACTS_FILE" 2>/dev/null | head -n 1
}

# ------------------------------------------------------------------ the runner
# mine_json / mine_text: `fugaro issue mine` run in the checkout. The status is
# kept in MINE_STATUS rather than returned, because a non-zero status is a
# READING here (the CLI prints its own note and carries on) and a `$(...)` would
# swallow it into the fact's text.
mine_json() { # OUTFILE ERRFILE
  local st=0
  (cd "$CO" && fugaro issue mine --json) >"$1" 2>"$2" || st=$?
  MINE_STATUS=$st
}
mine_text() { # OUTFILE ERRFILE
  local st=0
  (cd "$CO" && fugaro issue mine) >"$1" 2>"$2" || st=$?
  MINE_STATUS=$st
}

# prompt TEXT: the one thing this script cannot do for you. It never fakes the
# step: it asks, waits for Enter, and with --no-prompt it says out loud that it
# is assuming the step was taken.
prompt() { # TEXT
  if [ -n "$NO_PROMPT" ]; then
    say "not asking (--no-prompt): assuming this was done — $1"
    return 0
  fi
  printf '%s: %s\n' "$PROG" "$1" >&2
  printf '%s: press Enter when it is done (Ctrl-C stops the check and restores the cache)\n' "$PROG" >&2
  read -r _ || true
}

# ------------------------------------------------- mode 470: the shared lookup
# step_mine_online KEY: the listing read the store end to end. `source: shared`
# is the one member that says so — a run that could not reach the store answers
# `snapshot` or `local` and prints why on stderr — and the row quotes those notes
# so the failure is readable rather than inferred.
step_mine_online() { # KEY
  local key=$1 asof rows source format
  mine_json "$SCRATCH/mine.json" "$SCRATCH/mine.err"
  source=$(json_str "$SCRATCH/mine.json" source)
  format=$(json_num "$SCRATCH/mine.json" format)
  asof=$(json_str "$SCRATCH/mine.json" as_of)
  rows=$(json_rows "$SCRATCH/mine.json")
  if [ "$MINE_STATUS" -ne 0 ]; then
    fact "$key" FAIL "\`fugaro issue mine --json\` exited $MINE_STATUS in $CO: the command itself failed, so nothing about the store was measured (its own words: $(bounded "$(cat "$SCRATCH/mine.err" 2>/dev/null || true)" 400))"
    return 0
  fi
  if [ "$format" != "1" ]; then
    fact "$key" FAIL "\`fugaro issue mine --json\` printed a document whose format is '${format:-absent}' rather than 1, so this build's --json shape is not the one this check reads (schemas/issue-mine.schema.json)"
    return 0
  fi
  if [ "$source" != "shared" ]; then
    fact "$key" FAIL "\`fugaro issue mine\` answered from '${source:-nothing}' rather than the project's store, so the shared lookup was not made at all and the rows are this machine's own (the notes the run printed are: $(bounded "$(cat "$SCRATCH/mine.err" 2>/dev/null || true)" 400))"
    return 0
  fi
  fact "$key" PASS "\`fugaro issue mine --json\` read the project's store ('source: shared') and printed $rows row(s) in format 1; the store's own as-of time is ${asof:-absent}"
}

# store_probe KEY PREFIX — one listing as the SIGNED-IN identity. U1 is whether a
# launcher's login may list these prefixes, so the row says what the identity
# was: a denial here is the reading the check exists for, while a call that did
# not answer at all (an expired session, a 5xx gcloud did not retry) says nothing
# about the permission and stops the run rather than being recorded as one.
store_probe() { # KEY PREFIX
  local key=$1 prefix=$2 out st=0 entries
  out=$(gcloud storage ls --project="$P" "gs://$B/$prefix" 2>&1) || st=$?
  if [ "$st" -eq 0 ]; then
    entries=$(printf '%s' "$out" | grep -c . || true)
    fact "$key" PASS "the signed-in login may list gs://$B/$prefix (${entries:-0} name(s) came back), which is what \`mine\`'s own reader lists to find this user's issues"
    return 0
  fi
  if denial_is_permission "$out"; then
    fact "$key" FAIL "the signed-in login may NOT list gs://$B/$prefix: gcloud refused it (gcloud's own words: $(bounded "$out" 400)). \`fugaro issue mine\` lists that prefix to find this user's issues, so a login refused here is a \`mine\` that shows local rows only — and the launcher grant the reconciliation reads (F17, troubleshoot-issue T-IS17) is what has to carry the listing"
    return 0
  fi
  if empty_listing "$out"; then
    fact "$key" PASS "the signed-in login may list gs://$B/$prefix, and the prefix holds no object yet: gcloud's own answer to the request was an empty list, which it reports as an error (gcloud's own words: $(bounded "$out" 200)). A project with no issue in the store reads this way, and it is not a refusal"
    return 0
  fi
  stop "the listing of gs://$B/$prefix did not answer, so nothing was read about the permission: $(bounded "$out" 300)"
}

# denial_is_permission: whether a failed gcloud call's own words are a refusal
# about a permission rather than a call that did not answer. A 403 or an
# IAM-style denial is the reading; a 5xx, an expired session or a killed request
# is not, and the caller stops on it.
denial_is_permission() {
  grep -qiE 'permission|denied|forbidden|403|does not have storage' <<<"$1"
}

# empty_listing: gcloud's own words for a prefix that holds nothing. It is not a
# refusal — the API answered, and the answer was an empty list — so a project
# with no issues yet must not read as a login that cannot list, which is the
# other reading a failed call can be.
empty_listing() {
  grep -qiE 'matched no objects|No URLs matched' <<<"$1"
}

# record_read KEY: one record under issues/records/, read with the signed-in
# identity. U1 asks whether a launcher can READ records there as well as list,
# and the object read is the only way to ask it. With no record in the store at
# all there is nothing to read and the row says so instead of failing a project
# that simply has no issues yet.
record_read() { # KEY
  local key=$1 number out st=0 names
  number=$(first_number "$SCRATCH/mine.json")
  if [ -z "$number" ]; then
    names=$(gcloud storage ls --project="$P" "gs://$B/issues/records/" 2>&1) || names=""
    number=$(printf '%s' "$names" | sed -n 's|.*/records/\([0-9][0-9]*\)$|\1|p' | head -n 1)
  fi
  if [ -z "$number" ]; then
    fact "$key" INFO "the store holds no record under issues/records/, so U1's read half was not measured: this project has no issue yet. Save one on a machine (\`fugaro issue sync\` uploads it) and run this mode again"
    return 0
  fi
  out=$(gcloud storage cat --project="$P" "gs://$B/issues/records/$number" 2>&1) || st=$?
  if [ "$st" -eq 0 ] && [ -n "$out" ]; then
    fact "$key" PASS "the signed-in login read gs://$B/issues/records/$number ($(printf '%s' "$out" | wc -c | tr -d ' ') byte(s)), which is what \`mine\` reads to build a row"
    return 0
  fi
  if [ "$st" -ne 0 ] && denial_is_permission "$out"; then
    fact "$key" FAIL "the signed-in login may NOT read gs://$B/issues/records/$number: gcloud refused it (gcloud's own words: $(bounded "$out" 400)). \`mine\` reads the record of every issue it lists, so a login refused the read shows rows it cannot fill"
    return 0
  fi
  stop "the read of gs://$B/issues/records/$number did not answer, so nothing was read about the permission: $(bounded "$out" 300)"
}

# step_human_470: the half no one machine can measure, printed and never faked.
step_human_470() {
  local prompt_text
  prompt_text="NOT RUN by this script and never faked into a row, because it needs a second machine and a second person: (1) on machine 2, with the SAME Google login, run \`fugaro issue mine\` in a checkout of the same repository and confirm this issue is listed with its number — the number is the same one machine 1 saw; (2) have a SECOND PERSON hit the same failure, upload it (\`fugaro issue sync\`), and run \`fugaro issue mine\`: both listings must show the same number and 'also hit by 1 other' on the row"
  fact human-steps INFO "$prompt_text"
  say "the two steps this script does not run:"
  say "  1. machine 2, same Google login: \`fugaro issue mine\` in a checkout of the same repository must list the issue with the same number."
  say "  2. a second person hits the same failure, uploads it, runs \`fugaro issue mine\`: both see the same number and 'also hit by 1 other'."
  say "Neither is measured here, and neither is faked: the register's Check 470 section carries them as numbered steps."
}

# ------------------------------------------------------- mode 471: the offline
# step_offline: the two readings the plan asks for, both of them of a run with no
# network — the snapshot standing in, and then no snapshot at all.
step_offline() {
  prompt "TURN THE NETWORK OFF now (wi-fi or ethernet off, or your machine's own offline switch), and leave it off until this script asks for it back"
  local st=0 start=$SECONDS out
  (cd "$CO" && fugaro issue mine) >"$SCRATCH/offline.out" 2>"$SCRATCH/offline.err" || st=$?
  OFFLINE_SECONDS=$((SECONDS - start))
  out=$(cat "$SCRATCH/offline.out" 2>/dev/null || true)
  if [ "$st" -ne 0 ]; then
    fact offline-snapshot FAIL "\`fugaro issue mine\` with no network exited $st rather than answering from this machine's own rows: an offline listing is supposed to be best effort and to say so (its own words: $(bounded "$(cat "$SCRATCH/offline.err" 2>/dev/null || true)" 400))"
  elif ! printf '%s' "$out" | grep -q 'offline: shared state as of '; then
    fact offline-snapshot FAIL "\`fugaro issue mine\` with no network printed no 'offline: shared state as of <time>' line, so either the network was NOT off (the run read the store and printed its rows as current) or the online run wrote no snapshot to stand in. Its own first lines were: $(bounded "$out" 400)"
  elif [ "$OFFLINE_SECONDS" -gt "$OFFLINE_MAX_SECONDS" ]; then
    fact offline-snapshot FAIL "\`fugaro issue mine\` with no network printed the offline line, but took ${OFFLINE_SECONDS}s — more than the ${OFFLINE_MAX_SECONDS}s this check allows for the credential lookup with no network (U3: the CLI bounds that lookup at 5 s, so a run this slow is the full network timeout rather than a fast failure)"
  else
    fact offline-snapshot PASS "\`fugaro issue mine\` with no network printed the snapshot's own line ('$(printf '%s' "$out" | grep -m1 'offline: shared state as of ' | sed 's/[[:space:]]*$//')') and answered in ${OFFLINE_SECONDS}s (U3: the credential lookup with no network is a fast failure here, not the 20 s the access package allows)"
  fi
  # The cache is cleared HERE, by this script, because "a cleared cache
  # directory" is a local file and not a network step: the snapshot directory is
  # moved aside, and the Restore puts the original back whatever happens next.
  if [ ! -d "$CACHE/mine" ]; then
    fact cache-cleared FAIL "the online run left no snapshot under $CACHE/mine/, so the offline listing that stands on it could not be measured with the cache cleared: \`mine\` writes that file on every successful read of the store, and a run that wrote none is a run that never read it"
    prompt "TURN THE NETWORK BACK ON now"
    return 0
  fi
  mv "$CACHE/mine" "$SCRATCH/mine.cleared" || stop "the snapshot directory $CACHE/mine could not be moved aside, so the cache could not be cleared"
  st=0
  (cd "$CO" && fugaro issue mine) >"$SCRATCH/cleared.out" 2>"$SCRATCH/cleared.err" || st=$?
  out=$(cat "$SCRATCH/cleared.out" 2>/dev/null || true)
  if [ "$st" -ne 0 ]; then
    fact cache-cleared FAIL "\`fugaro issue mine\` with the cache cleared and no network exited $st rather than printing this machine's own rows (its own words: $(bounded "$(cat "$SCRATCH/cleared.err" 2>/dev/null || true)" 400))"
  elif ! printf '%s' "$out" | grep -q 'no snapshot of the project.s issues is cached'; then
    fact cache-cleared FAIL "\`fugaro issue mine\` with the cache cleared printed no 'no snapshot of the project's issues is cached' line, so a reader is not told that the rows are this machine's own files alone. Its own first lines were: $(bounded "$out" 400)"
  else
    fact cache-cleared PASS "\`fugaro issue mine\` with the snapshot cleared and no network printed '$(printf '%s' "$out" | grep -m1 'no snapshot of the project.s issues is cached' | sed 's/[[:space:]]*$//')' — the local rows alone, said out loud"
  fi
  prompt "TURN THE NETWORK BACK ON now"
}

# ---------------------------------------------------- mode 472: the size of it
# step_size: the numbers U2 asks for, timed end to end, and the answer line above
# the block so the owner can copy it back without reading the rows.
step_size() {
  local st=0 start=$SECONDS source asof hidden
  (cd "$CO" && fugaro issue mine --json) >"$SCRATCH/size.json" 2>"$SCRATCH/size.err" || st=$?
  SIZE_SECONDS=$((SECONDS - start))
  source=$(json_str "$SCRATCH/size.json" source)
  asof=$(json_str "$SCRATCH/size.json" as_of)
  hidden=$(json_num "$SCRATCH/size.json" hidden)
  SIZE_ROWS=$(json_rows "$SCRATCH/size.json")
  SIZE_TRUNCATED=$(json_bool "$SCRATCH/size.json" truncated)
  if [ "$st" -ne 0 ]; then
    fact mine-json FAIL "\`fugaro issue mine --json\` exited $st in $CO, so nothing about the listing was measured (its own words: $(bounded "$(cat "$SCRATCH/size.err" 2>/dev/null || true)" 400))"
  elif [ "$(json_num "$SCRATCH/size.json" format)" != "1" ]; then
    fact mine-json FAIL "\`fugaro issue mine --json\` printed a document that is not format 1, so the shape this check counts is not the one the build prints (schemas/issue-mine.schema.json)"
  else
    fact mine-json PASS "\`fugaro issue mine --json\` answered in ${SIZE_SECONDS}s with a format-1 document from '${source:-nothing}' (as of ${asof:-absent}), which is the listing this mode measures"
  fi
  if [ "$st" -ne 0 ] || [ "$(json_num "$SCRATCH/size.json" format)" != "1" ]; then
    fact row-count FAIL "the document could not be read, so the row count was not taken: the count is what U2 is asked about (how many issues a listing returns, and whether the 500-issue cap is the right one)"
    fact truncated FAIL "the document could not be read, so whether the store's own listing was cut short was not taken"
    return 0
  fi
  fact row-count PASS "$SIZE_ROWS issue row(s) came back, and the whole command took ${SIZE_SECONDS}s (U2: the time a few thousand names take against the real bucket, and whether the 500-issue and 1000-name caps are right); ${hidden:-0} finished row(s) were hidden by the 30-day rule and --all shows them"
  if [ "$SIZE_TRUNCATED" = "true" ]; then
    fact truncated PASS "truncated: true — the store's own listing was cut short at the 500-issue cap, so some of this user's issues are missing from this listing and the cap is the one U2 has to answer"
  else
    fact truncated PASS "truncated: false — the store's listing was not cut short, so this project is under the 500-issue index cap (and under the 1000-name per-issue occurrence cap)"
  fi
  ANSWER="THE LISTING AT SIZE: $SIZE_ROWS issue row(s), ${SIZE_SECONDS}s, truncated: ${SIZE_TRUNCATED:-unread}, hidden: ${hidden:-0}, source: ${source:-unread}"
}

# ------------------------------------------------------------- the verdict
# step_verdict reads the facts rather than the exit path: a FAILed fact beside an
# exit 0 would read as a pass. The rows a mode is ABOUT are its criteria; the
# rows that are readings for the record (the human half, a store with no record
# in it) are INFO and never gate.
step_verdict() {
  REACHED_END=yes
  local key verdict failed=no unread="" criteria
  case "$MODE" in
  470) criteria=(mine-online listing-reporters listing-occurrences record-read) ;;
  471) criteria=(online offline-snapshot cache-cleared) ;;
  472) criteria=(mine-json row-count truncated) ;;
  esac
  for key in ${criteria[@]+"${criteria[@]}"}; do
    verdict=$(fact_verdict "$key")
    case "$verdict" in
    FAIL) failed=yes ;;
    "") unread="$unread $key" ;;
    esac
  done
  if [ "$failed" = yes ]; then
    owner "the block's rows say which fact failed and what it read; nothing is concluded about the store from this run"
    exit 1
  fi
  if [ -n "$unread" ]; then
    owner "these facts were never measured, so this run cannot tell:$unread — the block's 'not reached' rows say where it stopped"
    exit 2
  fi
  case "$MODE" in
  470) owner "every fact held: \`mine\` read the project's store, and the signed-in login may list and read the two prefixes U1 asks about. The second machine and the second person are still yours to run (the section's numbered steps)" ;;
  471) owner "every fact held: with no network the snapshot stood in and said so, and with the snapshot cleared the local rows alone were printed and said so" ;;
  472) owner "every fact held: the listing's numbers are in the row above the block — copy that line back to the register" ;;
  esac
  exit 0
}

# print_plan: every command a real run would make, in order, and its Restore's.
# Printed by --dry-run INSTEAD of running anything: this check's own test pins
# that a dry run makes no fugaro call and no gcloud call at all, so the plan
# names the shape of each command and never evaluates one.
print_plan() {
  say "dry run: NOTHING below was run. A real run of mode $MODE would run, in this order:"
  say "  [local, reads] the guards: the three refused names in --project and --bucket, and --checkout being a directory that carries a fugaro.yaml"
  say "  [local, reads+writes] the user's cache directory ($CACHE): identity.json and mine/ are copied into \$SCRATCH before the first fugaro call, so the Restore restores bytes rather than a shape it hopes for"
  case "$MODE" in
  470)
    say "  [network, reads] (cd $CO && fugaro issue mine --json) — the shared lookup itself: the run's own 'source: shared' is the one member that says the store was read end to end"
    say "  [network, reads] gcloud storage ls --project=$P gs://$B/issues/reporters/ — the prefix \`mine\`'s reader lists to find this user's issues, read with YOUR signed-in identity (U1: whether a launcher's login may list it; a denial is the reading, a call that does not answer stops the run)"
    say "  [network, reads] gcloud storage ls --project=$P gs://$B/issues/occurrences/ — the same question about the prefix the hit times and the other-reporter counts come from"
    say "  [network, reads] gcloud storage cat --project=$P gs://$B/issues/records/<NNNN> — one record, read with your identity (the number is the first row's own, else the first name under issues/records/; a store with no record at all records INFO and gates nothing)"
    say "  [nothing] the second machine and the second person: printed as a prompt, never run and never faked"
    ;;
  471)
    say "  [network, reads] (cd $CO && fugaro issue mine --json) — the online run that writes the snapshot the offline run stands on"
    say "  [you] TURN THE NETWORK OFF (prompted; --no-prompt assumes you did)"
    say "  [network, none] (cd $CO && fugaro issue mine) — timed, and its output must carry 'offline: shared state as of <time>' within ${OFFLINE_MAX_SECONDS}s (CHECK470_OFFLINE_MAX_SECONDS changes the bound for this script's own test)"
    say "  [local, writes] mv $CACHE/mine \$SCRATCH/mine.cleared — the cache cleared by this script, because it is a local file and not a network step"
    say "  [network, none] (cd $CO && fugaro issue mine) — again, and its output must carry 'no snapshot of the project's issues is cached'"
    say "  [you] TURN THE NETWORK BACK ON (prompted)"
    ;;
  472)
    say "  [network, reads] (cd $CO && fugaro issue mine --json) — timed end to end, and the document's row count, its 'truncated' member and its hidden count are the readings U2 asks for"
    ;;
  esac
  say "and its Restore (also from the exit trap) would run:"
  say "  [local, writes] rm -f $CACHE/identity.json, then the saved copy is put back when this run found one (and it is left absent when it did not)"
  say "  [local, writes] rm -rf $CACHE/mine, then the saved directory is put back the same way"
  say "  [local, reads] both are read back — cmp for the identity cache, diff -r for the snapshots — and only a byte-for-byte match is a PASSed restore"
  say "Nothing was run, uploaded, read from the cloud or written: this dry run made no fugaro call and no gcloud call at all."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=()
set_block_keys() {
  case "$MODE" in
  470) BLOCK_KEYS=(mode project bucket checkout guard mine-online listing-reporters listing-occurrences record-read human-steps restore) ;;
  471) BLOCK_KEYS=(mode project bucket checkout guard online offline-snapshot cache-cleared restore) ;;
  472) BLOCK_KEYS=(mode project bucket checkout guard mine-json row-count truncated restore) ;;
  esac
}

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK %s RESULT\n' "$MODE" >&3
  for key in ${BLOCK_KEYS[@]+"${BLOCK_KEYS[@]}"}; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
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
  printf 'CHECK %s RESULT END (exit %s)\n' "$MODE" "$status" >&3
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
  # bad substitution), and a block that then read as a finished run would offer
  # rows that were never measured. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the store"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cache file of this check's to put back"
  elif ! printf '%s' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|FAIL|the Restore recorded nothing, so the two cache files this check can touch were not put back: $CACHE/identity.json and the snapshots under $CACHE/mine/ may hold this run's own rows. Put them back by hand (the copies this run saved are gone with the scratch directory)"
  fi
  # The verdict is taken from the facts, not from the exit path: an exit 0 beside
  # a FAILed criterion row, or beside a Restore that did not finish, would read
  # as a pass.
  if [ "$status" -eq 0 ] && printf '%s\n' "$facts" | grep -qE '^(mine-online|listing-reporters|listing-occurrences|record-read|online|offline-snapshot|cache-cleared|mine-json|row-count|truncated|restore)\|FAIL\|'; then
    status=1
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    if [ -n "$ANSWER" ]; then
      printf '\n%s\n' "$ANSWER" >&3
    fi
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured and putting the cache back"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check470.sh --checkout <dir> --project <project> --bucket <bucket> [--mode 470|471|472] [--dry-run] [--no-prompt]"

parse_args() {
  P=""
  B=""
  CO=""
  MODE=470
  DRY_RUN=${CHECK470_DRY_RUN:-}
  NO_PROMPT=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --mode)
      [ $# -ge 2 ] || die "--mode needs a number. usage: $USAGE"
      MODE=$2
      shift 2
      ;;
    --checkout)
      [ $# -ge 2 ] || die "--checkout needs a directory. usage: $USAGE"
      CO=$2
      shift 2
      ;;
    --project)
      [ $# -ge 2 ] || die "--project needs a project ID. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --bucket)
      [ $# -ge 2 ] || die "--bucket needs a bucket name (with or without gs://). usage: $USAGE"
      B=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --no-prompt)
      NO_PROMPT=1
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
  case "$MODE" in
  470 | 471 | 472) ;;
  *) die "--mode has to be 470, 471 or 472 (the register's three checks), and '$MODE' is none of them. usage: $USAGE" ;;
  esac
  case "$B" in gs://*) B=${B#gs://} ;; esac
  case "$B" in */) B=${B%/} ;; esac
  [ -n "$CO" ] || die "--checkout is required, and it has to be the SANDBOX repository's checkout: \`fugaro issue mine\` is run there and reads the project that checkout's own fugaro.yaml names. usage: $USAGE"
  [ -n "$P" ] || die "--project is required, and it has to be the project the checkout's runs bucket lives in: it is typed by you and never read from a config that may name another. usage: $USAGE"
  [ -n "$B" ] || die "--bucket is required, and it has to be that project's runs bucket: mode 470 reads the issue store there with your own identity, and the other two record it. usage: $USAGE"
}

main() {
  parse_args "$@"
  need_tools
  set_block_keys
  scratch_up
  fact mode PASS "$MODE (the register's check this run takes: $(
    case "$MODE" in
    470) printf 'the shared lookup across machines and people' ;;
    471) printf 'offline: the snapshot stands in, then the local rows alone' ;;
    472) printf 'the listing at size' ;;
    esac
  ))"
  fact project PASS "$P (typed by the owner, and the project the checkout's issues belong to)"
  fact bucket PASS "$B (typed by the owner, and the runs bucket that holds the store's issues/ prefix)"
  guard_names
  guard_checkout
  if [ -n "$DRY_RUN" ]; then
    fact dry-run PASS "guards and the plan only: nothing was run, uploaded, read from the cloud or written, and no fugaro call and no gcloud call was made at all"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the three arguments, the two guards, and the dry-run row itself. It reached
    # its end -- the plan IS the end of a dry run -- so the exit trap must not
    # read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(mode project bucket checkout guard dry-run)
    return 0
  fi
  # From here on a fugaro call may rewrite the two cache files, so the Restore
  # has something to do even if the first call fails.
  CACHE_TOUCHED=yes
  case "$MODE" in
  470)
    step_mine_online mine-online
    store_probe listing-reporters "issues/reporters/"
    store_probe listing-occurrences "issues/occurrences/"
    record_read record-read
    step_human_470
    ;;
  471)
    step_mine_online online
    step_offline
    ;;
  472)
    step_size
    ;;
  esac
  step_verdict
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
