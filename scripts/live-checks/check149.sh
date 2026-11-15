#!/usr/bin/env bash
# Check 149 — the console shows the stored view on the owner's own machine, as
# ONE script YOU run.
#
# It settles the live half of the Bitbucket-credentials work's U9 end to end
# (D364): the console's project view is drawn from the view the sweep stored
# (coordinator/repos/<slug>/ in the project's database), read as the signed-in
# person through roles/firebasedatabase.viewer, with NO App key and NO Bitbucket
# credential. The register's row and section are docs/gcp-live-checklist.md,
# "Check 149".
#
# Usage:      bash scripts/live-checks/check149.sh --project <name> [--console-ok]
#             [--dry-run]
#             The name is a Fugaro project (an installation) on THIS machine,
#             and it has to be the one the config in force describes: the
#             machine's own `fugaro config list --json` is asked first, and a
#             name this machine's configs do not carry is a STOP before
#             anything is read. It also has to be the register's own live-test
#             sandbox: FUGARO_LIVE_GCP_PROJECT names that installation's GCP
#             project (the register's Preconditions), and the config has to
#             name the same one.
# Needs:      bash, mktemp, python3 and fugaro on PATH. Nothing is installed
#             and nothing is fetched; if one of those is missing the script
#             stops with one line naming it.
# Where:      SANDBOX installations only, and never `belong`: the name is
#             refused whether you typed it or the config answers it. It is
#             READ-ONLY: it creates nothing, writes nothing, launches no run
#             and mints no credential.
# Costs:      one read of the project's database as YOU (the console's own
#             read), one local listing of this machine's configs, and one
#             window you look at.
# Runs:       by YOU, at your own terminal. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed.
#             Nothing else was created, so the `restore` row says that and
#             whether the directory really went.
# Output:     progress on stderr, ONE block on stdout headed CHECK 149 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one FAILed, 2 when the run could not tell (a guard stopped
#             it, a tool is missing, the read did not answer).
#
# What the block never carries: a document. The read's answer is parsed by one
# python3 reader that prints a fixed vocabulary — counts, yes/no, the key path a
# shape problem is in, and the names of the repositories the config in force
# already lists — and the block is written from those words, so no pull request
# title, no branch, no URL and no stale reason reaches the log you paste back.
# And no trace: `bash -x` prints every document this check reads as it reads it,
# so guard_xtrace REFUSES a run whose shell arrived with tracing on.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had. The stored view this
# check reads carries pull request titles, branch names and stale reasons that
# other people wrote, and a trace would print them in full into the log the
# owner would then paste back. So the trace is turned off here, BASH_XTRACEFD is
# unset so a trace redirected to a file this script knows nothing about cannot
# be reopened, and guard_xtrace refuses a shell that arrived tracing (the flag
# sits in SHELLOPTS/BASHOPTS then, and every child would start tracing on its
# own). Check 145 spells the three parts out; this is the same guard.
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
# --help printed the usage and measured nothing, so it gets no block and no
# verdict. It is set here rather than in main because the exit trap reads it
# whatever ended the run, including a run that died before main's own body.
HELPED=no
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
# all, and the block would then read as "the console is fine" for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the console" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 149 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 149 needs bash"
  need mktemp
  need python3 "the reader that parses this machine's configs and the read's answer is one python3 file"
  need fugaro "the stored view is read with fugaro coordinator status --from-store, which is the read a console makes"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk149-mine"
SCRATCH_REMOVED=no
PRINTED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk149.XXXXXXXX") || die "Check 149 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 149 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
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
  if [ "$HELPED" = yes ]; then exit 0; fi
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it:
  # bash exits by itself for an error the ERR trap does not see (an unbound
  # variable, a bad substitution), and a block that then read as a finished run
  # would offer a row of FAILs as a verdict about the console when the steps
  # that would have filled them never ran. Any such ending is an abort, and 2
  # says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the console"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cloud state of this check's to undo"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    facts="${facts}"$'\n'"restore|$restore|this check created nothing and changed no cloud state: it listed the configs on this machine, ran ONE read of the project's database as you, and asked you about a window. The scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
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
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the stored view this check reads — pull request titles, branch names and stale reasons other people wrote — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check149.sh --project <name> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a document it read"
}

# guard_project: `belong` is the owner's own installation and this check is a
# sandbox check of the Bitbucket-credentials block, so the name is refused here
# — before this machine's configs are listed, before the read — and the same
# refusal is made again on the config's own answer a moment later, because the
# name you type and the config in force are two things and the check has to see
# the same one twice. The test is the lowercased substring, so `fugaro-belong`
# and `belong-dev` are refused with `belong` itself.
guard_project() {
  case "$(printf '%s' "$NAME" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    fact sandbox FAIL "the project named is $NAME, which names the owner's own installation: Check 149 reads a SANDBOX installation's stored view, and a name carrying 'belong' is refused however it is spelled"
    stop "Check 149 never runs against 'belong'. Name the sandbox installation: bash scripts/live-checks/check149.sh --project <sandbox>"
    ;;
  esac
}

# guard_live_target: the register's own rule for a sandbox (its Preconditions)
# is that FUGARO_LIVE_GCP_PROJECT names the live-test GCP project and the config
# in force names the same one. Without it there is no project the register
# names as a sandbox, and this check never decides for itself which project is
# one — so it stops and names the one line to export.
guard_live_target() {
  if [ -z "$LIVE_PROJECT" ]; then
    fact sandbox FAIL "FUGARO_LIVE_GCP_PROJECT is not set, so no project is named as this session's live-test sandbox: Check 149 reads a sandbox installation's stored view and refuses to decide for itself which project is one"
    stop "FUGARO_LIVE_GCP_PROJECT is not set. The register's Preconditions set it for the whole bring-up: export FUGARO_LIVE_GCP_PROJECT=<the sandbox's GCP project> and run again (the same shell you run the other checks from already has it)"
  fi
  case "$(printf '%s' "$LIVE_PROJECT" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    fact sandbox FAIL "FUGARO_LIVE_GCP_PROJECT is $LIVE_PROJECT, which names the owner's own GCP project: the register's live-test sandbox is never the owner's own installation"
    stop "FUGARO_LIVE_GCP_PROJECT names '$LIVE_PROJECT', which carries 'belong'. Point it at the sandbox's GCP project and run again"
    ;;
  esac
}

# --------------------------------------------------------------- the requests
# run_capture OUTFILE CMD... — runs a read, keeps stdout in OUTFILE, keeps the
# command's own message in the scratch directory, and leaves its exit status in
# $?. stderr is never parsed — it is where fugaro puts its progress — and it is
# shown to the owner only through report_stderr below.
run_capture() {
  local out=$1
  shift
  "$@" >"$out" 2>"$SCRATCH/cmd.err" || {
    local status=$?
    report_stderr
    return "$status"
  }
}

# report_stderr: the command's own message, shown to the owner ONLY when the
# reader finds nothing credential-shaped in it. The message is never parsed and
# never pasted into the block — a line of fugaro's own progress is not a fact
# about the read — and the one thing that must not happen is a credential in
# the log the owner copies back.
report_stderr() {
  local verdict kinds
  verdict=$(reader scan "$SCRATCH/cmd.err" 2>/dev/null || printf 'creds=unreadable\n')
  kinds=$(field_of creds "$verdict")
  case "$kinds" in
  none) sed -n '1,5p' "$SCRATCH/cmd.err" >&4 || true ;;
  *)
    owner "the command's own message is NOT printed here: it carries something credential-shaped (${kinds:-unreadable}), and this script never echoes one. Run the command yourself and read what it says"
    ;;
  esac
}

# reader SUBCOMMAND [ARGS] — the python3 reader that parses this machine's
# configs and the read's answer. It prints a fixed vocabulary and never a
# document: `installations=`, `found=`, `name=`, `project=`, `region=`,
# `repos=`, `repo=`, `stored=`, `unstored=`, `records=`, `runs=`, `prs=`,
# `collisions=`, `unread=`, `stale=`, `shape=`, `problem=`, `creds=`, `count=`.
reader() { python3 "$SCRATCH/reader.py" "$@"; }

# ------------------------------------------------------------------ the steps
# step_project asks THIS machine's own configs which installation the name is
# and holds it to the name that was typed and to the register's sandbox. It is
# a local read — `fugaro config list` opens no bucket and needs no project
# selected — so it makes no cloud call and reads no credential: the check's one
# read of the project is the step after this one.
step_project() {
  if ! run_capture "$SCRATCH/configs.json" fugaro config list --json; then
    stop "fugaro config list --json did not answer, so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs a local config for the sandbox installation"
  fi
  local answer
  answer=$(reader config "$NAME" <"$SCRATCH/configs.json") || stop "the answer to fugaro config list --json is not the document this check reads (is this fugaro too old to print --json?)"
  if [ "$(field_of found "$answer")" != yes ]; then
    stop "this machine has no installation named $NAME (it lists $(field_of installations "$answer") of them). Run 'fugaro config list' yourself and name the sandbox one"
  fi
  PROJECT=$(field_of project "$answer")
  REGION=$(field_of region "$answer")
  [ -n "$PROJECT" ] || stop "the config for $NAME names no gcp_project, so this run cannot tell whether it is the register's sandbox. Fix the config and run again"
  case "$(printf '%s' "$PROJECT" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    fact sandbox FAIL "the config in force describes the installation $NAME as the GCP project $PROJECT, which names the owner's own one: the name you typed and the config in force are two things, and this check refuses belong under either"
    stop "the config for $NAME names the GCP project '$PROJECT', which carries 'belong'. Point FUGARO_CONFIG at the sandbox's own config, or name the sandbox installation"
    ;;
  esac
  if [ "$PROJECT" != "$LIVE_PROJECT" ]; then
    fact sandbox FAIL "the installation $NAME is the GCP project $PROJECT and FUGARO_LIVE_GCP_PROJECT names $LIVE_PROJECT: the project you named is not the sandbox this session's register names, and Check 149 never decides for itself which project is one"
    stop "the installation $NAME lives in the GCP project '$PROJECT' and FUGARO_LIVE_GCP_PROJECT names '$LIVE_PROJECT'. Name the sandbox installation, or export the sandbox's own GCP project"
  fi
  fact sandbox PASS "the installation $NAME is the config in force and it is the register's sandbox: GCP project $PROJECT (the one FUGARO_LIVE_GCP_PROJECT names), region $REGION, and neither the name nor the GCP project carries 'belong'"
}

# step_read makes the read the console makes, word for word the register's argv:
# the stored view of every repository the config lists, as the signed-in person,
# with no key. Its stdout is the document §6.13 describes; the block never
# prints it.
step_read() {
  if ! run_capture "$SCRATCH/view.json" fugaro coordinator status --from-store --all --json --project "$NAME"; then
    stop "fugaro coordinator status --from-store --all --json --project $NAME did not answer, so this run has nothing to read: the check needs an account that may read the project's database (roles/firebasedatabase.viewer) and a config that lists at least one repository"
  fi
  READ_ANSWER=$(reader view <"$SCRATCH/view.json") || stop "the answer to fugaro coordinator status --from-store --all --json is not JSON, so it is not a document this check can read"
  REPOS=$(field_of repos "$READ_ANSWER")
  REPOS_STORED=$(field_of stored "$READ_ANSWER")
  REPOS_UNSTORED=$(field_of unstored "$READ_ANSWER")
  local line
  REPO_NAMES=()
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    REPO_NAMES+=("${line#repo=}")
  done <<<"$(printf '%s\n' "$READ_ANSWER" | grep '^repo=' || true)"
}

# step_stored_view: the fact this check turns on. What it reads is the read's OWN
# answer about where its view came from — `stored` is true for every repository
# — because "no project record changed" is not something a script can see from
# outside: the coordinator's node is in the project's database, and what says
# the command took the stored path rather than the runs-alone fallback is the
# document it printed (D364, §6.13).
step_stored_view() {
  case "$REPOS_STORED" in
  yes)
    fact stored-view PASS "the read answered the STORED view for all $REPOS repositories the config lists ($(printf '%s' "${REPO_NAMES[*]:-none}")): this is the view the sweep stored, read with no host and no key, which is what the console's project view draws"
    ;;
  no)
    fact stored-view FAIL "the read answered the RUNS ALONE for $REPOS_UNSTORED of the $REPOS repositories: the coordinator has never reconciled them, so there is no stored view for the console to show — this is not a run of Check 149 (a sweep has to have stored the view first: fugaro update --firebase-project <id> --coordinator, then wait for a tick)"
    stop "the coordinator has stored no view for $REPOS_UNSTORED of the $REPOS repositories the config lists. Turn the coordinator on and let a sweep store one, then run this check again"
    ;;
  *)
    fact stored-view FAIL "the read's answer does not carry the envelope §6.13 describes ($(field_of problem "$READ_ANSWER")), so which half of the view it printed could not be read from it"
    stop "the answer to fugaro coordinator status --from-store --all --json is not the document §6.13 describes: $(field_of problem "$READ_ANSWER")"
    ;;
  esac
}

# step_keys: the document carries the keys the console reads, with the types
# §6.13 gives them. This is the half of U9 that a script CAN settle: a key the
# console reads and the CLI does not print is a screen that cannot be built.
step_keys() {
  if [ "$(field_of shape "$READ_ANSWER")" = ok ]; then
    fact json-keys PASS "the document is the shape console/docs/json-contract.md §6.13 describes: the repos envelope, and on each repository the repo, provider, stored, view and stale keys with the types the console reads — $REPOS repositories, $(field_of records "$READ_ANSWER") records, $(field_of runs "$READ_ANSWER") runs, $(field_of prs "$READ_ANSWER") pull requests, $(field_of collisions "$READ_ANSWER") collisions, $(field_of unread "$READ_ANSWER") unread and $(field_of stale "$READ_ANSWER") stale markers"
    return 0
  fi
  fact json-keys FAIL "the document is not the shape console/docs/json-contract.md §6.13 describes: $(field_of problem "$READ_ANSWER"). The problem is named by the key path and the type this check read, never by a value out of the document"
  return 1
}

# step_credentials: nothing the read printed — its document or its own message —
# is credential-shaped. The check names the KINDS it found and never the text,
# so a run that trips this row still hands back a block with no secret in it.
step_credentials() {
  local verdict kinds
  verdict=$(reader scan "$SCRATCH/view.json" "$SCRATCH/cmd.err" 2>/dev/null || printf 'creds=unreadable\ncount=0\n')
  kinds=$(field_of creds "$verdict")
  case "$kinds" in
  none)
    fact no-credential PASS "neither the document the read printed nor the command's own message carries anything credential-shaped (a bearer header, a key or secret assignment, a PEM block, a Google access token or API key, a GitHub or Slack token, a JWT, or a long opaque run): the read holds no key, and what it printed holds none either"
    ;;
  unreadable)
    fact no-credential FAIL "the read's output could not be scanned for credential-shaped text, so this row is not a claim that there is none"
    return 1
    ;;
  *)
    fact no-credential FAIL "the read's output carries $kinds credential-shaped (found in $(field_of count "$verdict") place(s)). The KINDS are named here and the text is NOT: it never reaches this block. A pull request's title is text somebody else wrote, so a match can be a false alarm — look at the command's own output yourself before pasting this block back"
    stop "the read's output carries something credential-shaped ($kinds), so this run stops rather than asking you about the console: read the command's own output yourself, and treat a secret reaching a CLI's output as the finding"
    ;;
  esac
}

# step_console: the half no script can see. One explicit step, one answer, and
# the exact words to look for — the console's own labels, so the answer is about
# what the screen draws and not about what this check hopes it draws.
step_console() {
  local answer="" first
  first=${REPO_NAMES[0]:-the repository the read listed}
  owner ""
  owner "ONE step is yours, and it is the half no script can see: the console window."
  owner "  1. Open the console and pick the installation '$NAME' (its project picker lists it as '$PROJECT')."
  owner "  2. Open its project view and look for these three things:"
  owner "       - the source line   fugaro coordinator status --from-store --all --json"
  owner "       - the sentence      the stored view carries no time, so this console claims no age for it"
  owner "       - the repository $first labelled   the last stored view"
  owner "         (a label reading 'no stored view: the runs alone' means the console did NOT read the stored view)"
  owner "  3. Answer y when the view is drawn from the stored view, n when it is not. Ctrl-C stops the run."
  owner ""
  if [ "$CONSOLE_OK" = yes ]; then
    fact console-renders-stored-view PASS "--console-ok was given, which stands for your typed answer: the console's project view of $NAME renders the stored view the read above printed. NOTHING WAS LOOKED AT for this row — it is your word, and no run of this script can make it true"
    return 0
  fi
  printf 'type y or n: ' >&4
  read -r answer || true
  case "$answer" in
  y | Y | yes | YES)
    fact console-renders-stored-view PASS "you answered y: the console's project view of $NAME draws the stored view — the source line 'fugaro coordinator status --from-store --all --json', the sentence that the view carries no time, and the repository labelled 'the last stored view' and not 'no stored view: the runs alone'"
    ;;
  n | N | no | NO)
    fact console-renders-stored-view FAIL "you answered n: the console's project view of $NAME does not draw the stored view. Its own label says which failure it is — 'no stored view: the runs alone' is a console whose project view does not read the stored view at all (a console build older than this read, or a view opened on another installation), while a view that draws nothing for this project is the screen refusing the document the read printed"
    return 1
    ;;
  *)
    stop "the console step was not answered (the answer was '${answer:-nothing}'), so nothing is concluded about the window: answer y or n, or pass --console-ok when you are running this without a terminal to answer at"
    ;;
  esac
}

# field_of NAME TEXT — the value of a NAME= line the reader printed.
field_of() {
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# ------------------------------------------------------------------- reporting
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL — not reached. Any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project sandbox stored-view json-keys no-credential
  console-renders-stored-view xtrace restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 149 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 149 RESULT END (exit %s)\n' "$status" >&3
}

# print_plan: every command a real run would make, in order, and its Restore's.
# Printed by --dry-run INSTEAD of running anything: this check's own test pins
# that a dry run makes no fugaro call at all, so the plan names the shape of each
# command and never runs one.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [local, reads] fugaro config list --json — this machine's own installations, to hold the name you typed to the config in force and to the register's sandbox (FUGARO_LIVE_GCP_PROJECT=$LIVE_PROJECT). It opens no bucket, needs no project selected and reads no credential."
  say "  [network, reads, as YOUR ADC] fugaro coordinator status --from-store --all --json --project $NAME — the register's row, word for word (the console adds --config <path> to the same argv). It reads coordinator/repos/<slug>/ in the project's database through roles/firebasedatabase.viewer, opens no host and holds no App key and no Bitbucket credential. Its stdout is parsed for the keys the console reads (§6.13) and scanned for credential-shaped text; its stderr is never parsed, and it is echoed to you only when it carries nothing credential-shaped."
  say "  [your own window] you open the console's project view for $NAME and look for the source line 'fugaro coordinator status --from-store --all --json', the sentence 'the stored view carries no time, so this console claims no age for it', and the repository labelled 'the last stored view' and not 'no stored view: the runs alone'. You answer y or n; --console-ok stands for y."
  say "and its Restore would run: remove the scratch directory (0700, its own marker, outside every git work tree). Nothing else is created — this check writes nothing to the project, mints no credential, launches no run and changes no cloud state."
  say "Nothing was read from the project and nothing was written: this dry run ran no fugaro command at all."
}

# ----------------------------------------------------------------------- main
USAGE="check149.sh --project <name> [--console-ok] [--dry-run]"

parse_args() {
  NAME=""
  CONSOLE_OK=no
  DRY_RUN=${CHECK149_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a Fugaro project name. usage: $USAGE"
      NAME=$2
      shift 2
      ;;
    --console-ok)
      CONSOLE_OK=yes
      shift
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
  [ -n "$NAME" ] || die "--project is required: it is the sandbox installation whose stored view you read, and this check never picks one for itself. usage: $USAGE"
}

main() {
  need_tools
  parse_args "$@"
  LIVE_PROJECT=${FUGARO_LIVE_GCP_PROJECT:-}

  PROJECT=""
  REGION=""
  READ_ANSWER=""
  REPOS=0
  REPOS_STORED=unknown
  REPOS_UNSTORED=0
  REPO_NAMES=()

  scratch_up
  write_files
  guard_xtrace
  guard_project
  guard_live_target
  # The name is a fact of the command line, not of the config: it is recorded
  # before the config is read, so a dry run's block carries it too.
  fact project PASS "$NAME (typed by the owner: the Fugaro project whose stored view is read, and the installation the config in force has to describe)"

  if [ -n "$DRY_RUN" ]; then
    fact dry-run PASS "guards and the plan only: nothing was read from the project, nothing was written, and no fugaro command was run at all"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the name it was given, the trace guard, and the dry-run row itself. It
    # reached its end -- the plan IS the end of a dry run -- so the exit trap
    # must not read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(project xtrace dry-run)
    return 0
  fi

  step_project
  step_read
  step_stored_view
  # A step that has recorded a FAIL and returned has said everything it has to
  # say; `|| true` keeps the ERR trap for the steps that ended without saying
  # why, and the count below is what makes the run's exit code a FAIL's.
  step_keys || true
  step_credentials || true
  step_console || true

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

write_files() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check 149's reader: the two documents this check reads are parsed here.

It prints a fixed vocabulary and never a document. `fugaro config list --json`
becomes installations=, found=, name=, project= and region=; the stored view
becomes repos=, one repo= line per repository, stored=, unstored=, records=,
runs=, prs=, collisions=, unread=, stale= and shape= — with problem=<code> when
the document is not the shape console/docs/json-contract.md §6.13 describes,
where the code names a KEY PATH and a TYPE and never a value out of the
document; and `scan` reads the command's own output for credential-shaped text
and prints the KINDS it found and how many, never the text.

The one value it prints out of a document is a pull request number, and only to
say one is below 1: the console's decoder refuses that number, and a number is
not a secret. Repository names are printed as the config in force spells them,
which is the same text the check's own stop messages and the console's screen
carry.
"""
import json
import re
import sys


def fail(message):
    print("error=%s" % message, file=sys.stderr)
    sys.exit(1)


def load(path=None):
    raw = open(path, encoding="utf-8").read() if path else sys.stdin.read()
    try:
        return json.loads(raw)
    except ValueError as e:
        fail("not JSON: %s" % e)


def is_str(v):
    return isinstance(v, str)


def is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)


def is_bool(v):
    return isinstance(v, bool)


class Shape:
    """The first problem found, as a code of this reader's own: a key path and
    a type. A later problem is not looked for — one is what a caller needs to
    know the document is not the one the console reads."""

    def __init__(self):
        self.problem = None

    def bad(self, code):
        if self.problem is None:
            self.problem = code


def opt(obj, key, kind, code, shape):
    """A key that may be absent or null: when it is there it has to be `kind`."""
    if key not in obj or obj[key] is None:
        return None
    v = obj[key]
    if not kind(v):
        shape.bad(code + "-not-a-" + word(kind))
    return v


def req(obj, key, kind, code, shape):
    """A key §6.13 says is never absent and never null."""
    if key not in obj:
        shape.bad(code + "-missing")
        return None
    v = obj[key]
    if v is None:
        # Printed as a different code from an absent one: the two are a
        # different job for whoever reads the row back.
        shape.bad(code + "-null")
        return None
    if not kind(v):
        shape.bad(code + "-not-a-" + word(kind))
        return None
    return v


def word(kind):
    return {is_str: "string", is_int: "number", is_bool: "bool"}[kind]


def req_list(obj, key, code, shape):
    """A list §6.13 says is never absent: `[]` is a fact it prints."""
    if key not in obj:
        shape.bad(code + "-missing")
        return []
    v = obj[key]
    if v is None:
        shape.bad(code + "-null")
        return []
    if not isinstance(v, list):
        shape.bad(code + "-not-a-list")
        return []
    return v


def opt_list(obj, key, code, shape):
    """A list §6.13 says may be absent — absent means the empty list."""
    if key not in obj or obj[key] is None:
        return []
    v = obj[key]
    if not isinstance(v, list):
        shape.bad(code + "-not-a-list")
        return []
    return v


def check_stale(stale, shape):
    if stale is None:
        return
    if not isinstance(stale, dict):
        shape.bad("stale-not-an-object")
        return
    req(stale, "since", is_str, "stale-since", shape)
    req(stale, "reason", is_str, "stale-reason", shape)


def check_unread(unread, shape):
    for u in unread:
        if not isinstance(u, dict):
            shape.bad("unread-not-an-object")
            continue
        n = req(u, "number", is_int, "unread-number", shape)
        if n is not None and n < 1:
            shape.bad("unread-number-below-one")
        req(u, "reason", is_str, "unread-reason", shape)
        opt(u, "carried", is_bool, "unread-carried", shape)
        opt(u, "failures", is_int, "unread-failures", shape)
        opt(u, "retry_after", is_str, "unread-retry-after", shape)


def check_pr(pr, shape, where):
    if pr is None:
        return
    if not isinstance(pr, dict):
        shape.bad(where + "-pr-not-an-object")
        return
    n = req(pr, "number", is_int, where + "-pr-number", shape)
    if n is not None and n < 1:
        # The console's own refusal (§6.13): a pull request number below 1 is
        # not a number it will draw.
        shape.bad(where + "-pr-number-below-one")
    opt(pr, "url", is_str, where + "-pr-url", shape)
    opt(pr, "title", is_str, where + "-pr-title", shape)
    opt(pr, "branch", is_str, where + "-pr-branch", shape)
    opt(pr, "draft", is_bool, where + "-pr-draft", shape)
    opt(pr, "state", is_str, where + "-pr-state", shape)


def check_run(run, shape):
    if not isinstance(run, dict):
        shape.bad("run-not-an-object")
        return
    req(run, "id", is_str, "run-id", shape)
    n = opt(run, "pr", is_int, "run-pr", shape)
    if n is not None and n < 1:
        shape.bad("run-pr-below-one")
    opt(run, "status", is_str, "run-status", shape)
    opt(run, "outcome", is_str, "run-outcome", shape)
    opt(run, "terminal", is_bool, "run-terminal", shape)
    opt(run, "branch", is_str, "run-branch", shape)
    opt(run, "base_branch", is_str, "run-base-branch", shape)
    opt(run, "declared_by", is_str, "run-declared-by", shape)
    req_list(run, "files", "run-files", shape)
    opt(run, "files_truncated", is_bool, "run-files-truncated", shape)


def check_record(rec, shape, counts):
    if not isinstance(rec, dict):
        shape.bad("record-not-an-object")
        return
    if rec.get("pr") is not None:
        counts["prs"] += 1
    check_pr(rec.get("pr"), shape, "record")
    runs = req_list(rec, "runs", "record-runs", shape)
    counts["runs"] += len(runs)
    for run in runs:
        check_run(run, shape)
    opt(rec, "branch", is_str, "record-branch", shape)
    opt(rec, "release", is_str, "record-release", shape)
    req(rec, "in_flight", is_bool, "record-in-flight", shape)
    req_list(rec, "files", "record-files", shape)
    req_list(rec, "services", "record-services", shape)


def check_collision(c, shape):
    if not isinstance(c, dict):
        shape.bad("collision-not-an-object")
        return
    req(c, "a", is_str, "collision-a", shape)
    req(c, "b", is_str, "collision-b", shape)
    req_list(c, "files", "collision-files", shape)
    req_list(c, "services", "collision-services", shape)


def check_view(view, shape, counts):
    if not isinstance(view, dict):
        shape.bad("entry-view-not-an-object")
        return
    req(view, "repo", is_str, "view-repo", shape)
    records = req_list(view, "records", "view-records", shape)
    counts["records"] += len(records)
    for rec in records:
        check_record(rec, shape, counts)
    collisions = req_list(view, "collisions", "view-collisions", shape)
    counts["collisions"] += len(collisions)
    for c in collisions:
        check_collision(c, shape)
    unread = opt_list(view, "unread", "view-unread", shape)
    counts["unread"] += len(unread)
    check_unread(unread, shape)
    opt(view, "omitted", is_int, "view-omitted", shape)
    opt(view, "omitted_collisions", is_int, "view-omitted-collisions", shape)


def cmd_config(name):
    doc = load()
    if not isinstance(doc, dict):
        fail("the document is not an object")
    insts = doc.get("installations")
    if not isinstance(insts, list):
        fail("installations is not a list")
    print("installations=%d" % len(insts))
    for inst in insts:
        if not isinstance(inst, dict):
            fail("an installation is not an object")
        if inst.get("name") != name:
            continue
        print("found=yes")
        print("name=%s" % (inst.get("name") or ""))
        print("project=%s" % (inst.get("gcp_project") or ""))
        print("region=%s" % (inst.get("region") or ""))
        return
    print("found=no")


def cmd_view():
    doc = load()
    if not isinstance(doc, dict):
        fail("the document is not an object")
    counts = {"records": 0, "runs": 0, "prs": 0, "collisions": 0, "unread": 0}
    shape = Shape()
    # The --all form's envelope (§6.13). A document without it is the
    # one-repository read, which is not the argv the console passes: it is a
    # shape problem and not an error of this reader's, so the caller reports it
    # as a FAILed row rather than as an unreadable answer.
    repos = doc.get("repos")
    if repos is None:
        shape.bad("repos-missing")
    if not isinstance(repos, list):
        if repos is not None:
            shape.bad("repos-not-a-list")
        repos = []
    elif not repos:
        # §6.13: an empty array is a shape this read does not print -- --all on
        # a config that lists no repository is refused instead.
        shape.bad("repos-empty")
    stored = 0
    stored_readable = True
    stale = 0
    names = []
    for entry in repos:
        if not isinstance(entry, dict):
            shape.bad("entry-not-an-object")
            stored_readable = False
            continue
        repo = req(entry, "repo", is_str, "entry-repo", shape)
        if repo is not None:
            if not repo:
                shape.bad("entry-repo-empty")
            else:
                names.append(repo)
        opt(entry, "provider", is_str, "entry-provider", shape)
        if entry.get("stored") is True:
            stored += 1
        elif entry.get("stored") is not False:
            shape.bad("entry-stored-not-a-bool")
            stored_readable = False
        check_stale(entry.get("stale"), shape)
        if entry.get("stale") is not None:
            stale += 1
        check_view(entry.get("view"), shape, counts)
    for name in names:
        print("repo=%s" % name)
    print("repos=%d" % len(repos))
    if not stored_readable or not repos:
        print("stored=unknown")
    elif stored == len(repos):
        print("stored=yes")
    else:
        print("stored=no")
    print("unstored=%d" % (len(repos) - stored))
    print("records=%d" % counts["records"])
    print("runs=%d" % counts["runs"])
    print("prs=%d" % counts["prs"])
    print("collisions=%d" % counts["collisions"])
    print("unread=%d" % counts["unread"])
    print("stale=%d" % stale)
    if shape.problem is None:
        print("shape=ok")
    else:
        print("shape=bad")
        print("problem=%s" % shape.problem)


# The credential-shaped text this check refuses to see in the read's output.
# The kinds are named and the text is never printed: a match can come from a
# pull request's title, which somebody else wrote, so the row says what kind it
# was and leaves the reading to the owner.
CREDENTIALS = (
    ("bearer", re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._~+/-]{8,}")),
    ("pem-block", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("google-access-token", re.compile(r"\bya29\.[A-Za-z0-9._-]{10,}")),
    ("google-api-key", re.compile(r"\bAIza[0-9A-Za-z_-]{30,}")),
    ("github-token", re.compile(r"\b(ghp|gho|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,}")),
    ("slack-token", re.compile(r"\bxox[abprs]-[A-Za-z0-9-]{10,}")),
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}")),
    ("key-assignment", re.compile(
        r"(?i)\b(secret|token|password|passwd|api[_-]?key|private[_-]?key|client[_-]?secret)\b\s*[:=]\s*[\"']?[A-Za-z0-9._~+/-]{16,}")),
    ("opaque-run", re.compile(r"\b[A-Za-z0-9+/]{60,}={0,2}\b")),
    ("hex-run", re.compile(r"\b[0-9a-fA-F]{64,}\b")),
)


def cmd_scan(paths):
    text = ""
    for path in paths:
        try:
            text += open(path, encoding="utf-8", errors="replace").read()
        except OSError:
            fail("cannot read %s" % path)
    kinds = []
    count = 0
    for kind, rx in CREDENTIALS:
        found = rx.findall(text)
        if found:
            kinds.append(kind)
            count += len(found)
    print("creds=%s" % ("+".join(kinds) if kinds else "none"))
    print("count=%d" % count)


def main(argv):
    if not argv:
        fail("no subcommand")
    cmd = argv[0]
    if cmd == "config":
        cmd_config(argv[1])
    elif cmd == "view":
        cmd_view()
    elif cmd == "scan":
        cmd_scan(argv[1:])
    else:
        fail("unknown subcommand %s" % cmd)


main(sys.argv[1:])
READER_EOF
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}
trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
