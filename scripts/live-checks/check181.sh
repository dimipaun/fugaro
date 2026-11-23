#!/usr/bin/env bash
# Check 181 — a Bitbucket shared project job end to end, and the allow-list
# refusal at the publish, at the deploy and at the proxy, as ONE script YOU run.
#
# It answers what no offline test can: that ONE Cloud Run job serves every
# repository of one Bitbucket profile, that the job holds NO Bitbucket token at
# all (D2302 — its git traffic reaches bitbucket.org only through the
# installation's credentials service), that a run of each allow-listed
# repository lands on that one job and opens its pull request, and that a
# repository the token's own block does not name gets NO git operation: it is
# refused at the publish, at the deploy and — launched by hand against the same
# job — by the run itself, before any git credential exists. The register's row
# and section are docs/gcp-live-checklist.md, "Check 181"; the plan is
# docs/bitbucket-workspace-token/implementation-plan.md, its task T2.7 (which is
# this check) over T2.6's section, and the shared job's own design is
# docs/shared-jobs-default/spec.md.
#
# Usage:      bash scripts/live-checks/check181.sh \
#               --installation <sandbox> \
#               --token-file <path> \
#               --repo <owner/name> \
#               --repo <owner/name> \
#               --third-repo <owner/name> \
#               [--workspace <workspace>] [--profile <name>]
#             --dry-run  prints the plan and touches nothing.
#             --workspace is the sandbox Bitbucket workspace the three
#             repositories live in, and it DEFAULTS TO `dimi_bmb`, the owner's
#             Premium sandbox workspace: the workspace every repository this
#             check names must belong to, which is checked rather than trusted.
#             Pass it only if the sandbox workspace has moved.
#             --profile is the Bitbucket profile the shared job is deployed for
#             and DEFAULTS TO `bitbucket-shared`. The check defines it if the
#             config has no such profile, and leaves it behind (the register's
#             Restore says so): the profile and its `fugaro-p-…` job stay until
#             you remove them. A config with no `standard` profile cannot take a
#             second one — the publisher refuses a profiles: block without it —
#             so this check stops and names that command if the add is refused.
#             --installation names the installation whose OWN config the
#             allow-list and the profile are written into. `fugaro project
#             profile` and `fugaro secrets allow` edit the project config this
#             machine selects and take no `--project`, so this check sets
#             FUGARO_PROJECT to the name you gave and reads the file back before
#             it writes anything, refusing a run from a checkout whose
#             fugaro.yaml names another project.
#             --token-file holds the project access token and nothing else — one
#             line, no quotes, no JSON — at mode 0600. It is the ONE place the
#             value lives: this script never holds it in a variable, never puts
#             it on a command line and never prints it. It is handed to
#             `fugaro secrets set` on stdin, and that is the only command that
#             ever sees it.
#             The two --repo values are the repositories the token's block is
#             allow-listed for, given as owner/name: one run of each is what
#             makes the shared job a reading of two repositories rather than of
#             one. No checkout is needed — every command this check runs takes
#             the repository by name.
#             --third-repo is the sandbox repository of the SAME workspace the
#             block does NOT name: added to the profile it is refused at the
#             publish and at the deploy, and launched by hand against the shared
#             job it is refused by the run before any git credential exists.
# Needs:      bash, mktemp, python3 (every `--json` document is read with it),
#             gcloud (the job the deploy leaves is read with your credentials)
#             and fugaro. Nothing is installed and nothing is fetched.
# Where:      a SANDBOX installation and a sandbox Bitbucket workspace only, and
#             never `belong`: this check refuses `belong` by name, as the
#             installation, as a repository's workspace and as the project
#             `fugaro doctor` selects. It also refuses a coding agent's session
#             (CLAUDECODE and the like): every command it runs is one you could
#             type yourself.
# Costs:      THREE sandbox runs (two allow-listed repositories and the third,
#             which halts at once), their model spend, two pull requests, and
#             the converges this check's publishes and updates apply. Every one
#             of them is billed to the sandbox installation, which is what it is
#             for.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (scripts/live-checks/check181_test.sh) drives it against fakes and
#             never touches gcloud or a Fugaro installation.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed
#             and the block's `restore` row says whether it really went. What a
#             person has to undo by hand is what the check exists to make: the
#             profile it defined and the `fugaro-p-…` job `fugaro update`
#             deployed for it, the runs' branches and their two pull requests,
#             and the project-wide secret (`fugaro secrets rm bitbucket-token`
#             once no repository needs it).
# Output:     progress on stderr, ONE block on stdout headed CHECK 181 RESULT,
#             to copy back to the coordinator. Its last line is the verdict, and
#             the verdict is the exit status:
#
#               CHECK 181 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 181 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 181 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything (a
#                                                      guard stopped it, a tool is
#                                                      missing, a command was
#                                                      refused, an error ended it):
#                                                      the rows it never reached
#                                                      read FAIL — not reached and
#                                                      `aborted` says what stopped
#                                                      it;
#               CHECK 181 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one criterion was
#                                                      NOT settled: a row reading
#                                                      UNVERIFIED, a step that
#                                                      could not verify its
#                                                      criterion (the third
#                                                      repository's refusal
#                                                      without a halt this check
#                                                      can read, the job without
#                                                      a listing it can read).
#
#             ONLY an all-PASS block is a PASS: a check that measured most of its
#             criteria and left one unmeasured is INCOMPLETE, not a pass, and the
#             register's section is read against that word.
#
# NO SECRET IN ANY OUTPUT is a property of the design, not a promise kept by
# care. The token's value is read from the file by the reader, which prints only
# its mode and its byte count; the value goes to `fugaro secrets set` on stdin
# and to nowhere else. Every other fact is a NAME — a profile's, a job's, a
# repository's, a credential's — and every line a command printed is reduced to
# a bounded, redacted last line before it reaches the block. No trace: `bash -x`
# prints every command as it runs, so guard_xtrace REFUSES a run whose shell
# arrived with tracing on.
#
# What this check deliberately does NOT do: it does not force the subset rule's
# own sentence. The owner's ruling of 2026-11-14 put the shared-job rule at the
# publish door too, and that rule is asked BEFORE a node is built — so a profile
# that both mounts `bitbucket-token` and names a repository the block does not
# admit is refused with the MOUNT sentence, not with the subset rule's. The
# subset rule is therefore read at the door that owns it and asks it first:
# `fugaro project profile set`, which holds the edited profile to
# projectpolicy.CheckBitbucketProfileRepos (internal/cli's projectProfileCheck).
# The block's `subset-refusal` row says which sentence came back either way.
#
# CHECK181_WORKSPACE, CHECK181_PROFILE, CHECK181_DRY_RUN, CHECK181_KEEP_SCRATCH,
# CHECK181_CLI_SECONDS, CHECK181_RUN_SECONDS, CHECK181_RUN_POLL_SECONDS and
# CHECK181_RUN_POLL_MAX exist for this script's own test: they shorten the caps,
# the intervals a run is polled at and the number of polls, and plan the check
# without touching anything. None of them can point this check at another
# installation: the project and the region are read from the installation the
# owner named.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the commands below hand
# a token file to a child on stdin, and a trace would print the command lines.
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
ABORT_REASON=""
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the shared job (no value is printed here)" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 181 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 181 needs bash"
  need mktemp
  need python3 "every --json document this check reads is one python3 file"
  need gcloud
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk181-mine"
SCRATCH_REMOVED=no
PRINTED=""
CLEANUP_RUNNING=no
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk181.XXXXXXXX") || die "Check 181 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 181 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  stop_call
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard is what keeps the removal from ever reaching the
  # owner's home, and reading it unguarded would abort cleanup under `set -u` on
  # a machine where HOME is unset, leaving the scratch directory behind.
  / | "${HOME:-}" | "${HOME:-}"/)
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

finish() {
  local status=$?
  trap - EXIT ERR
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the shared job"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    facts="${facts}"$'\n'"restore|$restore|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself"); what stays on the installation is what the register's Restore names (the profile, its fugaro-p-… job, the two pull requests, the project-wide secret)"
  fi
  # The status the block's last line carries, decided here because the restore
  # row above is a criterion like any other and exists only now. A finished run
  # whose restore row FAILed exits 1 (main decided 0 before that row existed);
  # a finished run with a criterion that is not a PASS row — a row reading
  # UNVERIFIED, a key nobody wrote — exits 3, because a block that measured most
  # of its criteria and left one unmeasured is not a pass; and a run that stopped
  # before it could measure everything keeps 2 and reads INCOMPLETE, because a
  # guard, a missing tool or an error is not a criterion that failed.
  if [ -z "$PRINTED" ] && [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    if printf '%s\n' "$facts" | grep -q '|FAIL|'; then
      status=1
    elif [ -n "$(criteria_unmet "$facts")" ]; then
      status=3
    fi
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check181.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry a command line or the token file this check hands to one child on stdin"
}

# guard_agent_session: this check launches billable runs and writes the
# installation's policy, and every command it runs is one a person types at
# their own terminal. A coding agent's session is refused rather than left to
# the CLI to refuse one command at a time, which is what makes the block say
# why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 181 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

# guard_belong: `belong` is the installation and the workspace this check must
# never touch, and a script that can be pointed at it by a typo has no business
# launching runs on it.
guard_belong() {
  local what value
  for what in installation project workspace repo-a repo-b third-repo; do
    case "$what" in
    installation) value=$INSTALLATION ;;
    project) value=$PROJECT_NAME ;;
    workspace) value=$WORKSPACE ;;
    repo-a) value=$REPO_A ;;
    repo-b) value=$REPO_B ;;
    third-repo) value=$THIRD_REPO ;;
    esac
    [ -n "$value" ] || continue
    case "$(lower "$value")" in
    belong | belong/* | */belong)
      die "Check 181 refused: '$value' (the $what) names 'belong', the installation and workspace this check must never touch. Nothing was sent anywhere"
      ;;
    esac
  done
  fact belong PASS "neither the installation '${INSTALLATION}', the workspace '${WORKSPACE}', nor any of the three repositories (${REPO_A}, ${REPO_B}, ${THIRD_REPO}) names 'belong': this check runs on a sandbox installation and a sandbox workspace, and the guard that says so runs before the first command — so a refused run has sent nothing anywhere"
}

# guard_workspace: every repository this check names has to belong to the one
# sandbox workspace, because the token's block and the profile are one
# workspace's. A repository whose owner is another workspace is refused rather
# than measured: it would be a run of a repository this check was never pointed
# at, and the reading would be about a different workspace's job.
guard_workspace() {
  local what repo o
  for what in repo-a repo-b third-repo; do
    case "$what" in
    repo-a) repo=$REPO_A ;;
    repo-b) repo=$REPO_B ;;
    third-repo) repo=$THIRD_REPO ;;
    esac
    o=${repo%%/*}
    if [ "$(lower "$o")" != "$(lower "$WORKSPACE")" ]; then
      die "Check 181 refused: $what '$repo' is not in the workspace '$WORKSPACE' (its owner is '$o'). All three repositories of this check are sandbox repositories of ONE Bitbucket workspace — the one the token's block, the profile and the shared job are all about — and the workspace defaults to 'dimi_bmb'; pass --workspace only if the sandbox workspace has moved. Nothing was sent anywhere"
    fi
  done
  fact workspace PASS "all three repositories (${REPO_A}, ${REPO_B}, ${THIRD_REPO}) are in the sandbox Bitbucket workspace '${WORKSPACE}', which is where the token's block, the profile and the shared job all live"
}

# --------------------------------------------------------------- the calls
# last_line FILE: one bounded, redacted line of a command's own output, for a
# block that has to say why a step failed without pasting a log.
redact_secrets() {
  printf '%s' "$1" | sed -E \
    -e 's/(([Pp]roxy-)?[Aa]uthorization[[:space:]]*:?[[:space:]]*)([Bb]asic|[Bb]earer|[Dd]igest)?[[:space:]]*[A-Za-z0-9._~+\/=-]{6,}/\1<redacted>/g' \
    -e 's/([Bb]earer[[:space:]]+)[A-Za-z0-9._~+\/=-]{6,}/\1<redacted>/g' \
    -e 's/([Aa]ccess[_-]?[Tt]oken|[Cc]lient[_-]?[Ss]ecret|[Rr]efresh[_-]?[Tt]oken|[Aa]pi[_-]?[Kk]ey|[Tt]oken)[[:space:]]*[:=][[:space:]]*[A-Za-z0-9._~+\/=-]{4,}/\1=<redacted>/g'
}

last_line() {
  local text
  text=$(grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | tr -d '\r' | tr '\n' ' ' | head -c 300 || true)
  [ -n "$text" ] || text="the command printed nothing"
  printf '%s' "$(redact_secrets "$text")"
}

# first_error FILE: the first line of a refusal, which is where the CLI puts its
# own sentence, bounded and redacted.
first_error() {
  local text
  text=$(grep -v '^[[:space:]]*$' "$1" 2>/dev/null | head -n 1 | tr -d '\r' | tr '\n' ' ' | head -c 400 || true)
  [ -n "$text" ] || text="the command printed nothing"
  printf '%s' "$(redact_secrets "$text")"
}

# run_call SECONDS OUT LOG STDIN cmd... — one long command with a deadline of
# its own, in a process group of its own so the deadline reaches whatever the
# call started. `timeout(1)` is not used: it is not on macOS, where this check is
# also run, and a run left behind after a failure is a job someone else pays
# for. STDIN is where the child's own standard input comes from: /dev/null for
# every command that has no question of its own, and a file for the one command
# that is handed the token. It is a parameter rather than a redirection at the
# call site because a redirection there would be undone by the child's own —
# which is how the store would read an empty token and store nothing.
CALL_PID=""
run_call() { # run_call SECONDS OUT LOG STDIN cmd...
  local secs=$1 out=$2 log=$3 in=$4
  shift 4
  set -m
  case "$in" in
  -) "$@" >"$out" 2>"$log" & ;;
  *) "$@" <"$in" >"$out" 2>"$log" & ;;
  esac
  CALL_PID=$!
  local waited=0
  while kill -0 "$CALL_PID" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      local pid=$CALL_PID
      stop_call
      wait "$pid" 2>/dev/null || true
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
  local status=0
  wait "$CALL_PID" || status=$?
  CALL_PID=""
  return "$status"
}

# call_q SECONDS OUT LOG cmd... — the same call with stdin at /dev/null, for
# every command that has no confirmation of its own: a prompt nobody can answer
# is a hang, and a hang inside this check is a run someone pays for while it
# waits. `fugaro update` is one of them: on an installation that is already
# applied its ordinary steps are covered by the converge's own review, so it
# asks nothing, and a step that would need a person is left to the owner with
# the CLI's own sentence rather than waited on.
call_q() { # call_q SECONDS OUT LOG cmd...
  local secs=$1 out=$2 log=$3
  shift 3
  run_call "$secs" "$out" "$log" /dev/null "$@"
}

# call_in SECONDS OUT LOG FILE cmd... — the same call with its stdin from a
# file: the ONE command this check is handed the token for, `fugaro secrets
# set`. Nothing else reads the token's value, and nothing prints it.
call_in() { # call_in SECONDS OUT LOG FILE cmd...
  local secs=$1 out=$2 log=$3 in=$4
  shift 4
  run_call "$secs" "$out" "$log" "$in" "$@"
}

stop_call() {
  if [ -n "${CALL_PID:-}" ] && kill -0 "$CALL_PID" 2>/dev/null; then
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    kill -KILL -- "-$CALL_PID" 2>/dev/null || true
  fi
  CALL_PID=""
}

# aimed SECONDS OUT LOG cmd... — the same call, on the INSTALLATION's own config.
# `fugaro project profile` and `fugaro secrets allow` edit the project config the
# machine selects and take no `--project` (the CLI refuses it by name), so the
# selector this check can set is the environment: $FUGARO_PROJECT is one of the
# ways localcfg.Select names, and it is the one the CLI's own refusal prints.
# What this does NOT settle is a checkout outranking it — a fugaro.yaml naming
# another project is a higher step of that same selection — which is why
# step_config reads back the file these commands act on before anything is
# written.
aimed() { # aimed SECONDS OUT LOG cmd...
  local secs=$1 out=$2 log=$3
  shift 3
  run_call "$secs" "$out" "$log" /dev/null env "FUGARO_PROJECT=$INSTALLATION" "$@"
}

# profile_allow REPO... — an allow_repos VALUE: the publisher holds that list to
# patterns that name a PROVIDER, an owner and a repository, so the two-segment
# owner/name this check is given is qualified with the provider here rather than
# at every call site. The token's own allow_data_to is the other shape
# (owner/name), which is why `fugaro secrets allow` below is given the bare one.
profile_allow() {
  local out="" r
  for r in "$@"; do
    out="${out:+$out,}bitbucket/$r"
  done
  printf '%s' "$out"
}

# ------------------------------------------------------------- the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check 181's reader: one file, one reading per subcommand, no credential in
any of them.

tokenfile argv: the token file. Prints its mode and its byte count and NOTHING
       of the value — the value itself is never read into this program, and the
       file is handed to `fugaro secrets set` by the shell.
doctor   argv: a `fugaro doctor --json` document. Prints the installation's own
       project: the name the local config carries, its GCP project and region.
profile  argv: a `fugaro project profile ls --json` document, a profile name.
       Prints the config FILE the document was read from, whether that profile
       is defined, its provider, the NAMES of the secrets it mounts and the
       repositories its allow_repos names.
runid    argv: a `fugaro run --json` document. Prints the run id it named.
run      argv: a runs listing, a run id. Prints that run's row field by field,
       `found=no` when the listing does not hold it, and the JOB its execution
       path names (`projects/<project>/locations/<region>/jobs/<job>/executions/
       <name>`, internal/backend's Execution.Name), which is the only place a
       run's job is written down. A halt's own words come with it.
jobs     argv: a `gcloud run jobs list --format=json` document, a name prefix,
       a profile. One line per job of that profile — `job=<name> url=<present|
       absent> token=<present|absent>` — and then the totals.
job      argv: a `gcloud run jobs describe --format=json` document. Prints the
       job's name, whether it names the credentials service, whether it names a
       Bitbucket token at all, and the NAMES of the secrets it mounts.
"""
import json
import os
import re
import sys

# A value this reader is willing to print: one line of printable ASCII. A
# repository's owner/name, a profile's name and a secret's name are all this
# shape, and a value that is not is dropped rather than copied into the block.
VALUE = re.compile(r"[\x21-\x7e]+")
# A profile's allow_repos entry names a PROVIDER, an owner and a repository
# (`bitbucket/myworkspace/api`), which is the shape the publisher holds the list
# to; the token's own allow_data_to is the two-segment owner/name beside it.
REPO = re.compile(r"[A-Za-z0-9._-]+/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+")
RUNID = re.compile(r"[A-Za-z0-9._-]{1,80}")


def word(v, cap=120):
    if v is None:
        return ""
    s = str(v)
    s = re.sub(r"[\x00-\x1f\x7f]", " ", s).strip()
    return s[:cap]


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def rows(path):
    doc = load(path)
    if isinstance(doc, list):
        return doc
    if isinstance(doc, dict):
        for key in ("runs", "items", "rows"):
            if isinstance(doc.get(key), list):
                return doc[key]
    return []


def env(spec):
    out = {}
    if isinstance(spec, dict):
        for e in spec.get("env") or []:
            if isinstance(e, dict) and e.get("name"):
                out[e["name"]] = e
    return out


def spec_of(doc):
    """The container spec of a job: the v1 shape `gcloud run jobs` answers is
    tried first, then the searched-for containers list."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec")):
        node = doc
        for key in path:
            node = node.get(key) if isinstance(node, dict) else None
            if node is None:
                break
        if isinstance(node, dict) and isinstance(node.get("containers"), list):
            return node
    return None


def containers_of(doc):
    spec = spec_of(doc)
    return (spec.get("containers") or []) if isinstance(spec, dict) else []


def carries(containers, name):
    """Whether any container names that variable at all. The presence of the
    NAME is the reading and never its value: a token a job mounts is a
    valueSource reference and one it was handed plainly is a value, and both are
    the job holding it."""
    for c in containers:
        if name in env(c):
            return True
    return False


def secret_names(containers):
    """The NAMES of the secret-backed environment entries: a reference's secret
    is a name, never a value."""
    out = []
    for c in containers:
        for name, e in env(c).items():
            src = e.get("valueSource") or {}
            ref = src.get("secretKeyRef") or {}
            secret = ref.get("name")
            if isinstance(secret, str) and secret:
                out.append("%s=%s" % (name, secret))
    return sorted(set(out))


def job_of_execution(execution):
    """The job an execution resource path names (`…/jobs/<job>/executions/<e>`)."""
    if "/jobs/" not in execution:
        return ""
    rest = execution.split("/jobs/", 1)[1]
    return rest.split("/", 1)[0]


cmd = sys.argv[1] if len(sys.argv) > 1 else ""
if cmd == "tokenfile":
    path = sys.argv[2]
    try:
        st = os.stat(path)
        size = os.path.getsize(path)
    except OSError:
        sys.stderr.write("the token file could not be read\n")
        sys.exit(1)
    print("mode=%o" % (st.st_mode & 0o777))
    print("bytes=%d" % size)
elif cmd == "doctor":
    doc = load(sys.argv[2])
    project = doc.get("project") if isinstance(doc, dict) else None
    if not isinstance(project, dict):
        print("project=")
        print("gcp_project=")
        print("region=")
    else:
        print("project=%s" % word(project.get("name")))
        print("gcp_project=%s" % word(project.get("gcp_project")))
        print("region=%s" % word(project.get("region")))
elif cmd == "profile":
    doc = load(sys.argv[2]) or {}
    want = sys.argv[3] if len(sys.argv) > 3 else ""
    print("path=%s" % word(doc.get("path"), 400))
    found = None
    for pr in doc.get("profiles") or []:
        if isinstance(pr, dict) and pr.get("name") == want:
            found = pr
            break
    if found is None:
        print("defined=no")
        print("provider=")
        print("secrets=")
        print("allow_repos=")
    else:
        print("defined=yes")
        print("provider=%s" % word(found.get("provider"), 40))
        print("secrets=%s" % word(",".join(sorted(
            s for s in (found.get("secrets") or []) if isinstance(s, str) and VALUE.fullmatch(s))), 400))
        print("allow_repos=%s" % word(",".join(sorted(
            r for r in (found.get("allow_repos") or []) if isinstance(r, str) and REPO.fullmatch(r))), 400))
elif cmd == "runid":
    doc = load(sys.argv[2])
    run_id = doc.get("run_id") if isinstance(doc, dict) else None
    print("run_id=%s" % (run_id if isinstance(run_id, str) and RUNID.fullmatch(run_id) else ""))
elif cmd == "run":
    want = sys.argv[3]
    for row in rows(sys.argv[2]):
        if not isinstance(row, dict) or row.get("run_id") != want:
            continue
        print("found=yes")
        print("status=%s" % word(row.get("status"), 40))
        print("terminal=%s" % ("yes" if row.get("terminal") is True else "no"))
        print("settled=%s" % ("yes" if row.get("settled") is True else "no"))
        print("pushed=%s" % ("yes" if row.get("pushed") is True else "no"))
        pr = row.get("pr")
        print("pr=%d" % pr if isinstance(pr, int) and not isinstance(pr, bool) and pr > 0 else "pr=0")
        print("job=%s" % word(job_of_execution(row.get("execution") or ""), 120))
        halt = row.get("halt")
        if isinstance(halt, dict):
            print("halted=yes")
            print("halt_reason=%s" % word(halt.get("reason"), 60))
            print("halt_detail=%s" % word(halt.get("detail"), 400))
        else:
            print("halted=no")
            print("halt_reason=")
            print("halt_detail=")
        break
    else:
        print("found=no")
elif cmd == "jobs":
    prefix = sys.argv[3]
    profile = sys.argv[4] if len(sys.argv) > 4 else ""
    n = urls = tokens = 0
    names = []
    for doc in load(sys.argv[2]) or []:
        if not isinstance(doc, dict):
            continue
        meta = doc.get("metadata") or {}
        name = meta.get("name") or ""
        labels = meta.get("labels") or {}
        # The name is truncated before its hash when the parts are long, so the
        # prefix alone misses a real job; the labels the job carries are what
        # name its profile (gcp.ProjectJobName).
        by_label = bool(profile) and labels.get("fugaro_profile") == profile and labels.get("fugaro_provider") == "bitbucket"
        if not name or not (name.startswith(prefix) or by_label):
            continue
        containers = containers_of(doc)
        n += 1
        names.append(name)
        url = carries(containers, "FUGARO_CREDENTIALS_SERVICE_URL")
        token = carries(containers, "FUGARO_BITBUCKET_TOKEN")
        urls += 1 if url else 0
        tokens += 1 if token else 0
        print("job=%s url=%s token=%s" % (word(name, 120),
                                          "present" if url else "absent",
                                          "present" if token else "absent"))
    print("jobs=%s" % n)
    print("urls=%s" % urls)
    print("tokens=%s" % tokens)
    print("names=%s" % word(",".join(names), 400))
elif cmd == "job":
    doc = load(sys.argv[2]) or {}
    name = ((doc.get("metadata") or {}).get("name")) or ""
    containers = containers_of(doc)
    print("name=%s" % word(name, 120))
    print("service=%s" % ("present" if carries(containers, "FUGARO_CREDENTIALS_SERVICE_URL") else "absent"))
    print("token=%s" % ("present" if carries(containers, "FUGARO_BITBUCKET_TOKEN") else "absent"))
    print("secretrefs=%s" % word(",".join(secret_names(containers)), 400))
else:
    sys.stderr.write("reader.py: unknown query %s\n" % cmd)
    sys.exit(2)
READER_EOF
  chmod 700 "$SCRATCH/reader.py"
}

# ------------------------------------------------------------------ the steps
step_installation() {
  local rc=0
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  call_q "$CLI_SECONDS" "$SCRATCH/doctor.json" "$SCRATCH/doctor.err" \
    fugaro doctor --project "$INSTALLATION" --json || rc=$?
  if [ "$rc" != 0 ] && [ "$rc" != 1 ]; then
    stop "fugaro doctor --project ${INSTALLATION} failed (exit ${rc}): $(last_line "$SCRATCH/doctor.err"). This check reads the installation's own project before it touches anything, and it needs an installation named on this machine with usable credentials"
  fi
  local facts
  facts=$(reader doctor "$SCRATCH/doctor.json" 2>/dev/null || true)
  PROJECT_NAME=$(field_of project "$facts")
  GCP_PROJECT=$(field_of gcp_project "$facts")
  REGION=$(field_of region "$facts")
  [ -n "$GCP_PROJECT" ] || stop "fugaro doctor --project ${INSTALLATION} named no gcp_project, so this check cannot read the job the deploy leaves and stops before it touches anything"
  guard_belong
  if [ "$(lower "$PROJECT_NAME")" != "$(lower "$INSTALLATION")" ]; then
    stop "the local config selected by --project ${INSTALLATION} is the installation '${PROJECT_NAME}': this check runs on the installation you named and no other, so it stops rather than measuring a project you did not mean"
  fi
  fact installation PASS "installation '${PROJECT_NAME}' in GCP project ${GCP_PROJECT}, region ${REGION:-unset}, read from fugaro doctor --project ${INSTALLATION} at $(date -u +%Y-%m-%dT%H:%M:%SZ). It is a sandbox installation: this check refuses 'belong' as an installation, as a workspace and as the project doctor selects"
}

# step_config: WHICH config this check is about to edit, read before the first
# write. The three commands that carry the allow-list and the profile —
# `fugaro secrets allow`, `fugaro project profile add/set` — edit the project
# config the machine selects and take no `--project` at all, so this check aims
# them with FUGARO_PROJECT (see `aimed`) and then proves the aim landed: the
# document names the file it was read from, and every edit below is an edit of
# that same file. A checkout whose fugaro.yaml names another project outranks
# the variable, and this is what catches it before a write rather than after.
step_config() {
  local rc=0
  aimed "$CLI_SECONDS" "$SCRATCH/profiles.json" "$SCRATCH/profiles.err" \
    fugaro project profile ls --json || rc=$?
  if [ "$rc" -ne 0 ]; then
    stop "fugaro project profile ls was REFUSED (exit ${rc}): $(first_error "$SCRATCH/profiles.err"). This check reads the installation's own project config before it writes anything into it — the allow-list and the profile a shared job is deployed for — and it needs that config to be the one FUGARO_PROJECT=${INSTALLATION} selects"
  fi
  local facts cfgpath
  facts=$(reader profile "$SCRATCH/profiles.json" "$PROFILE" 2>/dev/null || true)
  cfgpath=$(field_of path "$facts")
  case "$cfgpath" in
  */"$INSTALLATION".yaml) ;;
  *) stop "fugaro project profile acts on the project config this machine selects, and it read ${cfgpath:-no file}, not the installation's own ${INSTALLATION}.yaml: this check edits the config of the installation it names and of nothing else. It sets FUGARO_PROJECT=${INSTALLATION} for these commands, and a checkout whose fugaro.yaml names another project outranks that name (localcfg.Select). Run this check from a directory that is not a checkout of another project. Nothing was written" ;;
  esac
  fact config PASS "the config this check edits is ${cfgpath} — the installation '${INSTALLATION}'s own, selected with FUGARO_PROJECT=${INSTALLATION} (fugaro project profile and fugaro secrets allow edit the selected config and take no --project). Every write below is a write to this file, and the profile it carries stays on it after this check (the register's Restore)"
}

# step_token: the token's allow-list, the switch that hands it to the service,
# and the store. The value never leaves the file the owner named: it is handed
# to the one command that stores it, on stdin.
step_token() {
  local facts
  facts=$(reader tokenfile "$TOKEN_FILE" 2>"$SCRATCH/reader.err" || true)
  [ -n "$facts" ] || stop "the token file $TOKEN_FILE could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). It holds the project access token, one line and nothing else, and no value from it is printed. Nothing was sent anywhere"
  local mode bytes
  mode=$(field_of mode "$facts")
  bytes=$(field_of bytes "$facts")
  case "$mode" in
  600) ;;
  *) stop "the token file $TOKEN_FILE is mode ${mode:-?} and this check wants 0600: a token any other user of this machine can read is not one to store project-wide. Type 'chmod 600 $TOKEN_FILE' and run again. Nothing was sent anywhere" ;;
  esac
  if [ "${bytes:-0}" -lt 1 ]; then
    stop "the token file $TOKEN_FILE is empty (0 bytes): there is no token in it to store, and a store that ran anyway would put an empty value where every run's git credential comes from. Write the project access token into it, one line and nothing else, and run again. Nothing was sent anywhere"
  fi
  fact token PASS "the token file ${TOKEN_FILE} is mode ${mode} and ${bytes} byte(s), read for its shape and nothing else: no value from it is printed, and the only command that ever sees it is the store below, which reads it from the file on stdin"

  local rc=0
  aimed "$CLI_SECONDS" "$SCRATCH/allow.out" "$SCRATCH/allow.err" \
    fugaro secrets allow bitbucket-token "$REPO_A" "$REPO_B" --proxy || rc=$?
  if [ "$rc" -eq 0 ]; then
    fact allow-list PASS "fugaro secrets allow bitbucket-token ${REPO_A} ${REPO_B} --proxy accepted (exit 0), so the local config's project_secrets.bitbucket-token.allow_data_to names exactly these two repositories and the token's proxy switch is on — which is what makes the block this check's allow-list, and the job's git traffic the credentials service's rather than the job's own (D2302). No value is read or printed by this command"
  else
    stop "fugaro secrets allow bitbucket-token ${REPO_A} ${REPO_B} --proxy was REFUSED (exit ${rc}): $(first_error "$SCRATCH/allow.err"). The allow-list and the proxy switch are the state this whole check reads, and a session without them measures a job that holds no credential AND no service to reach git through. Run it yourself at your own terminal, then run this check again. Nothing was written and no value is printed"
  fi

  rc=0
  call_in "$CLI_SECONDS" "$SCRATCH/store.out" "$SCRATCH/store.err" "$TOKEN_FILE" \
    fugaro secrets set bitbucket-token --project-wide --project "$INSTALLATION" || rc=$?
  if [ "$rc" -eq 0 ]; then
    fact store PASS "fugaro secrets set bitbucket-token --project-wide accepted the token for installation ${INSTALLATION} (exit 0), reading the value from ${TOKEN_FILE} on stdin — the one place the value goes, and it is never on a command line or in this block. This is step 1 of the register's section: the token the credentials service will pair to each run's identity"
  else
    stop "fugaro secrets set bitbucket-token --project-wide was REFUSED (exit ${rc}): $(first_error "$SCRATCH/store.err"). The store is the first half of the path this check measures, and the likely reasons are that the allow-list entry above could not be written or that --project ${INSTALLATION} is not the installation you meant. Nothing was written and no value is printed"
  fi
}

# step_profile: the profile a shared job is deployed for — provider bitbucket,
# the two allow-listed repositories, and NOTHING of Bitbucket's among its
# secrets (D2302, projectpolicy.CheckSharedJobProfile).
step_profile() {
  local rc=0
  aimed "$CLI_SECONDS" "$SCRATCH/profiles.json" "$SCRATCH/profiles.err" \
    fugaro project profile ls --json || rc=$?
  if [ "$rc" -ne 0 ]; then
    stop "fugaro project profile ls was REFUSED (exit ${rc}): $(first_error "$SCRATCH/profiles.err"). This check reads the profile it deploys the shared job for before it publishes anything, and it needs the installation's own local config"
  fi
  local facts defined
  facts=$(reader profile "$SCRATCH/profiles.json" "$PROFILE" 2>/dev/null || true)
  defined=$(field_of defined "$facts")
  if [ "$defined" != yes ]; then
    say "the profile '${PROFILE}' is not defined in this installation's config; defining it as a bitbucket profile"
    rc=0
    aimed "$CLI_SECONDS" "$SCRATCH/add.out" "$SCRATCH/add.err" \
      fugaro project profile add "$PROFILE" --provider bitbucket --cpu 2 --memory 4Gi --timeout-class 6h || rc=$?
    if [ "$rc" -ne 0 ]; then
      stop "fugaro project profile add ${PROFILE} --provider bitbucket was REFUSED (exit ${rc}): $(first_error "$SCRATCH/add.err"). The profile a shared job is deployed from is this check's whole subject. A config that carries a profiles: block must define 'standard' too — it is the profile every workflow that names none runs on, and the publisher refuses the block without it, which is what refuses this add when the config has no standard yet. Define one yourself (fugaro project profile add standard --provider github --cpu 2 --memory 4Gi --timeout-class 6h), then run this check again"
    fi
    rc=0
    aimed "$CLI_SECONDS" "$SCRATCH/profiles.json" "$SCRATCH/profiles.err" \
      fugaro project profile ls --json || rc=$?
    facts=$(reader profile "$SCRATCH/profiles.json" "$PROFILE" 2>/dev/null || true)
    defined=$(field_of defined "$facts")
  fi
  [ "$defined" = yes ] || stop "the profile '${PROFILE}' is still not defined after fugaro project profile add said it wrote it: this check will not publish a profile it cannot read back"
  local provider secrets allow
  provider=$(field_of provider "$facts")
  secrets=$(field_of secrets "$facts")
  allow=$(field_of allow_repos "$facts")
  if [ "$provider" != bitbucket ]; then
    fact profile FAIL "the profile '${PROFILE}' serves provider '${provider:-unset}' and this check needs a BITBUCKET profile: the shared job it deploys is named from the profile and the provider, and a job of another provider is not the one a Bitbucket repository's run resolves. Define a bitbucket profile (fugaro project profile add <name> --provider bitbucket …) or pass --profile <the bitbucket one>"
    return 0
  fi
  # The tokenless shape is what step 2 of the register's section writes. A
  # profile an earlier shape left MOUNTING the credential has the entry taken
  # off it rather than refused: the entry is what the two refusal readings below
  # put back on purpose, and a profile left carrying it is the state this
  # reading is about.
  case ",$secrets," in
  *,bitbucket-token,*)
    rc=0
    aimed "$CLI_SECONDS" "$SCRATCH/set.out" "$SCRATCH/set.err" \
      fugaro project profile set "$PROFILE" secrets "" || rc=$?
    if [ "$rc" -ne 0 ]; then
      fact profile FAIL "the profile '${PROFILE}' mounts ${secrets} and taking the entry off was REFUSED (exit ${rc}): $(first_error "$SCRATCH/set.err"). A profile a shared job is deployed for holds no credential of the provider it serves (D2302, projectpolicy.CheckSharedJobProfile)"
      return 0
    fi
    secrets=""
    ;;
  esac
  rc=0
  aimed "$CLI_SECONDS" "$SCRATCH/set.out" "$SCRATCH/set.err" \
    fugaro project profile set "$PROFILE" allow_repos "$(profile_allow "$REPO_A" "$REPO_B")" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact profile FAIL "the profile '${PROFILE}' could not be given the two allow-listed repositories as its allow_repos (exit ${rc}): $(first_error "$SCRATCH/set.err"). The profile's list is which repositories this one shared job is deployed for, and without it a run of either repository resolves no shared job"
    return 0
  fi
  fact profile PASS "the profile '${PROFILE}' serves provider bitbucket, names $(profile_allow "$REPO_A" "$REPO_B") in its allow_repos, and mounts ${secrets:-nothing} — no Bitbucket credential (D2302: the job is shared by every repository the profile admits, and this token reaches a per-repository list such a job cannot be bounded by, so the job holds no token and its git reaches bitbucket.org through the credentials service). Read back with fugaro project profile ls, and the profile stays on the installation after this check (the register's Restore)"
}

# step_subset_refusal: the subset rule, at the door that owns it. A profile that
# mounts `bitbucket-token` may admit only what the token's block admits
# (projectpolicy.CheckBitbucketProfileRepos), and `fugaro project profile set`
# holds the edited profile to it before it writes — which is where the third
# repository's first refusal is read.
step_subset_refusal() {
  local rc=0
  aimed "$CLI_SECONDS" "$SCRATCH/set-mount.out" "$SCRATCH/set-mount.err" \
    fugaro project profile set "$PROFILE" secrets bitbucket-token || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact subset-refusal UNVERIFIED "the profile '${PROFILE}' could not be given the bitbucket-token entry at the profile door (exit ${rc}): $(first_error "$SCRATCH/set-mount.err"). This check reads the subset rule by putting the entry on a profile and then naming a repository the block does not admit, and without the first half the rule was NOT asked. The register's section says where to record what you see when you edit the profile by hand"
    return 0
  fi
  rc=0
  aimed "$CLI_SECONDS" "$SCRATCH/set-narrow.out" "$SCRATCH/set-narrow.err" \
    fugaro project profile set "$PROFILE" allow_repos "$(profile_allow "$REPO_A" "$REPO_B" "$THIRD_REPO")" || rc=$?
  local said
  said=$(first_error "$SCRATCH/set-narrow.err")
  if [ "$rc" -eq 0 ]; then
    fact subset-refusal FAIL "the profile edit adding ${THIRD_REPO} to a profile that MOUNTS bitbucket-token was ACCEPTED (exit 0), so the subset rule did not hold: a profile mounting the token may admit only what the token's block admits (projectpolicy.CheckBitbucketProfileRepos, asked by internal/cli's projectProfileCheck before it writes). ${THIRD_REPO} is not in project_secrets.bitbucket-token.allow_data_to, so this is the finding the check exists for"
    # Put the list back, so the runs below resolve one shared job over the two
    # repositories the block names.
    aimed "$CLI_SECONDS" "$SCRATCH/set.out" "$SCRATCH/set.err" \
      fugaro project profile set "$PROFILE" allow_repos "$(profile_allow "$REPO_A" "$REPO_B")" || true
  elif printf '%s' "$said" | grep -Fq -- "$THIRD_REPO"; then
    fact subset-refusal PASS "fugaro project profile set REFUSED adding ${THIRD_REPO} to a profile that mounts bitbucket-token (exit ${rc}), and its own sentence names the repository: $(printf '%s' "$said" | cut -c1-220). The token's block is the profile's ceiling and the profile's list can only narrow it (projectpolicy.CheckBitbucketProfileRepos), which is the first of the third repository's two refusals. The edit wrote nothing"
  else
    fact subset-refusal FAIL "the profile edit adding ${THIRD_REPO} was refused (exit ${rc}) but the refusal does not name the repository, so what refused it cannot be read as the subset rule: $said. This check records the reading rather than a refusal of any shape"
  fi
  # The entry comes off again: the readings below put it back on purpose, and
  # leaving it on would have the deploy refuse the profile before the job this
  # check reads is built.
  aimed "$CLI_SECONDS" "$SCRATCH/set.out" "$SCRATCH/set.err" \
    fugaro project profile set "$PROFILE" secrets "" || true
}

# step_mount_refusal: the shared-job rule at the two doors that own it — the
# publish (internal/cli's refuseAProfileMountingTheBitbucketCredential) and the
# deployer (internal/infra's ProjectJobs), both asking
# projectpolicy.CheckSharedJobProfile.
step_mount_refusal() {
  local rc=0
  aimed "$CLI_SECONDS" "$SCRATCH/set-mount.out" "$SCRATCH/set-mount.err" \
    fugaro project profile set "$PROFILE" secrets bitbucket-token || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact mount-publish UNVERIFIED "the bitbucket-token entry could not be put on the profile '${PROFILE}' at the profile door (exit ${rc}): $(first_error "$SCRATCH/set-mount.err"), so the publish that refuses it was NOT reached"
    fact mount-deploy UNVERIFIED "the same: without the entry on the profile there is no mount for the deployer to refuse"
    return 0
  fi
  rc=0
  call_q "$CLI_SECONDS" "$SCRATCH/publish-mount.out" "$SCRATCH/publish-mount.err" \
    fugaro project policy publish --project "$INSTALLATION" || rc=$?
  local said
  said=$(first_error "$SCRATCH/publish-mount.err")
  if [ "$rc" -eq 0 ]; then
    fact mount-publish FAIL "fugaro project policy publish ACCEPTED a profile that mounts bitbucket-token (exit 0): a shared project job holds no credential of the provider it serves (D2302), the rule is projectpolicy.CheckSharedJobProfile, and the publish is one of the two doors that ask it. The node it wrote is the shape this check exists to refuse"
  elif printf '%s' "$said" | grep -Fq -- "bitbucket-token"; then
    fact mount-publish PASS "fugaro project policy publish REFUSED the profile '${PROFILE}' mounting bitbucket-token (exit ${rc}), naming the credential: $(printf '%s' "$said" | cut -c1-220). The publish is the operator's door on the shared-job rule (the owner's ruling of 2026-11-14), so the entry is refused at the file that owns it rather than one step later at the deploy"
  else
    fact mount-publish FAIL "the publish was refused (exit ${rc}) but its sentence does not name the credential, so the refusal cannot be read as the shared-job rule: $said"
  fi
  rc=0
  call_q "$UPDATE_SECONDS" "$SCRATCH/update-mount.out" "$SCRATCH/update-mount.err" \
    fugaro update --project "$INSTALLATION" || rc=$?
  said=$(first_error "$SCRATCH/update-mount.err")
  if [ "$rc" -eq 0 ]; then
    fact mount-deploy FAIL "fugaro update ACCEPTED the profile that mounts bitbucket-token (exit 0): the deployer asks the same rule (internal/infra's ProjectJobs over projectpolicy.CheckSharedJobProfile) and a job for this profile is exactly what it must not build. The update's policy stage writes the node the config states, so the deployer is the only door left"
  elif printf '%s' "$said" | grep -Fq -- "bitbucket-token"; then
    fact mount-deploy PASS "fugaro update REFUSED the profile '${PROFILE}' mounting bitbucket-token (exit ${rc}), naming the credential: $(printf '%s' "$said" | cut -c1-220). The update's policy stage writes the node the config states (the HARD RULE: an update converges) and the DEPLOYER is what refuses the job, which is the second of the third repository's two refusals"
  else
    fact mount-deploy FAIL "the update was refused (exit ${rc}) but its sentence does not name the credential, so the refusal cannot be read as the deployer's: $said"
  fi
  # Off again: everything below needs a profile a shared job may be deployed for.
  aimed "$CLI_SECONDS" "$SCRATCH/set.out" "$SCRATCH/set.err" \
    fugaro project profile set "$PROFILE" secrets "" || true
}

# step_open_publish: the third repository added to the SHARED profile's
# allow_repos is refused at NO door — the block is not the profile's to mount,
# and the token's reach is the credentials service's to hold a call to. This is
# the difference the D2302 rule made, and it is also the state the last run of
# this check needs: the third repository has to be on the published node for its
# run to resolve the shared job at all.
step_open_publish() {
  local rc=0
  aimed "$CLI_SECONDS" "$SCRATCH/set-open.out" "$SCRATCH/set-open.err" \
    fugaro project profile set "$PROFILE" allow_repos "$(profile_allow "$REPO_A" "$REPO_B" "$THIRD_REPO")" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact open-publish FAIL "adding ${THIRD_REPO} to the tokenless profile '${PROFILE}' was REFUSED (exit ${rc}): $(first_error "$SCRATCH/set-open.err"). On a profile that mounts nothing there is no token for the subset rule to narrow, so this edit is one of the two the D2302 shape allows — and the third repository's run cannot resolve the shared job without it"
    return 0
  fi
  rc=0
  call_q "$CLI_SECONDS" "$SCRATCH/publish-open.out" "$SCRATCH/publish-open.err" \
    fugaro project policy publish --project "$INSTALLATION" || rc=$?
  if [ "$rc" -eq 0 ]; then
    fact open-publish PASS "with the profile tokenless, ${THIRD_REPO} added to its allow_repos was accepted AND fugaro project policy publish accepted the node (exit 0): a shared profile's list is not the token's ceiling, and nothing at this door refuses the third repository. That is the D2302 rule read from the other side, and the node now admits ${THIRD_REPO} to the profile, which is what makes its run resolve the shared job"
  else
    fact open-publish FAIL "fugaro project policy publish was REFUSED (exit ${rc}) with the tokenless profile naming ${THIRD_REPO}: $(first_error "$SCRATCH/publish-open.err"). Nothing is meant to refuse the third repository at this door — the block is not the profile's to mount — so this is a refusal the D2302 shape does not account for"
  fi
}

# step_deploy: the deploy, and the job it leaves — ONE `fugaro-p-…` job for the
# profile, with the credentials service's address on it and no Bitbucket token
# anywhere in it.
step_deploy() {
  local rc=0
  call_q "$UPDATE_SECONDS" "$SCRATCH/update.out" "$SCRATCH/update.err" \
    fugaro update --project "$INSTALLATION" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact deploy FAIL "fugaro update did not converge (exit ${rc}): $(last_line "$SCRATCH/update.err"). The deploy is what puts the one shared job in the deployer's hands, and without it nothing below has a job to read. A step the run left for you at your own terminal is the likely reason: run fugaro update --project ${INSTALLATION} yourself, then run this check again"
    fact job FAIL "the deploy did not finish, so no job of the profile '${PROFILE}' was read"
    return 0
  fi
  fact deploy PASS "fugaro update converged on installation ${INSTALLATION} (exit 0) with the tokenless bitbucket profile '${PROFILE}' published, so the deployer built the job that profile names"
  local rc2=0
  call_q "$CLI_SECONDS" "$SCRATCH/jobs.json" "$SCRATCH/jobs.err" \
    gcloud run jobs list --project "$GCP_PROJECT" --region "$REGION" --format=json || rc2=$?
  if [ "$rc2" -ne 0 ]; then
    fact job UNVERIFIED "gcloud run jobs list did not answer (exit ${rc2}): $(last_line "$SCRATCH/jobs.err"), so the job the deploy left was NOT read. This check needs an account that may read Cloud Run jobs on ${GCP_PROJECT} (roles/run.viewer or better). Read it yourself: gcloud run jobs list --project ${GCP_PROJECT} --region ${REGION}"
    return 0
  fi
  local facts count names
  facts=$(reader jobs "$SCRATCH/jobs.json" "fugaro-p-${PROFILE}-bitbucket" "$PROFILE" 2>/dev/null || true)
  count=$(field_of jobs "$facts")
  names=$(field_of names "$facts")
  if [ "${count:-0}" -eq 0 ]; then
    fact job FAIL "no job of the profile '${PROFILE}' is on ${GCP_PROJECT}/${REGION} after the update (the listing names ${count:-0} of the prefix fugaro-p-${PROFILE}-bitbucket and of that profile's labels): $(last_line "$SCRATCH/jobs.err"). One fugaro-p-… job per (profile, provider, account) is what the deploy leaves, and a run of an allow-listed repository resolves it or nothing"
    return 0
  fi
  # The reader prints one line per job of the profile, `<name> url=… token=…`:
  # the name is the first word, and it is the whole of what is read from it.
  JOB=$(printf '%s\n' "$facts" | sed -n 's/^job=\([^ ]*\).*/\1/p' | head -n 1)
  local rc3=0
  call_q "$CLI_SECONDS" "$SCRATCH/job.json" "$SCRATCH/job.err" \
    gcloud run jobs describe "$JOB" --project "$GCP_PROJECT" --region "$REGION" --format=json || rc3=$?
  if [ "$rc3" -ne 0 ]; then
    fact job UNVERIFIED "gcloud run jobs describe ${JOB} did not answer (exit ${rc3}): $(last_line "$SCRATCH/job.err"), so what the job carries was NOT read. Read it yourself: gcloud run jobs describe ${JOB} --project ${GCP_PROJECT} --region ${REGION} --format=json"
    return 0
  fi
  local jf service token refs
  jf=$(reader job "$SCRATCH/job.json" 2>/dev/null || true)
  service=$(field_of service "$jf")
  token=$(field_of token "$jf")
  refs=$(field_of secretrefs "$jf")
  case "$service:$token" in
  present:absent)
    fact job PASS "the deploy left ${count} job(s) of the profile '${PROFILE}' (${names}), and ${JOB} names the credentials service (FUGARO_CREDENTIALS_SERVICE_URL present) and holds NO Bitbucket token (FUGARO_BITBUCKET_TOKEN absent; the secret-backed entries it carries are ${refs:-none}). That is D2302 read off the job itself: a job that holds no git credential cannot clone without the service, and a job that carried bitbucket-token was built by something other than this path"
    ;;
  present:present)
    fact job FAIL "${JOB} names the credentials service AND holds a Bitbucket token (FUGARO_BITBUCKET_TOKEN present; secret-backed entries: ${refs:-none}): a shared Bitbucket job holds no token at all (D2302), and a job carrying one is exactly the state the shared-job rule exists to refuse. The deploy that built it is a finding"
    ;;
  *)
    fact job FAIL "${JOB} does not name the credentials service (FUGARO_CREDENTIALS_SERVICE_URL ${service:-unread}; Bitbucket token ${token:-unread}): a job that holds no git credential and has no service to reach git through cannot clone anything, so this is not the job a run of an allow-listed repository can execute on"
    ;;
  esac
}

# step_run LABEL REPO — one run of an allow-listed repository, waited for. The
# run has to reach its clone, its push and its pull request, and the job its
# execution path names has to be the ONE job the deploy left: a run on a
# per-repository job, or on another profile's, is a launch that did not select
# this profile's shared job, and nothing about the shared job would be read.
step_run() { # step_run LABEL REPO
  local label=$1 repo=$2 rc=0
  local task="$SCRATCH/task-${label}.md" json="$SCRATCH/run-${label}.json" log="$SCRATCH/run-${label}.log"
  cat >"$task" <<TASK_EOF
Add a line to the repository's README.md that says the repository was used for Fugaro's Check 181
($(date -u +%Y-%m-%d)), and open the pull request. Change nothing else: this task exists so a run
clones, pushes and opens a pull request on the shared job that holds no Bitbucket token, and Check
181 reads what that run recorded.
TASK_EOF
  say "launching one run in ${repo} on the shared job (this is the long step: a run clones, implements, reviews and opens its pull request)"
  call_q "$RUN_SECONDS" "$json" "$log" \
    fugaro run --repo "$repo" --project "$INSTALLATION" \
    --title "Check 181: one shared job, no token (${repo})" \
    --task-file "$task" --json || rc=$?
  local run_id row
  run_id=$(field_of run_id "$(reader runid "$json" 2>/dev/null || true)")
  say "the run in ${repo} is launched and now goes on in the cloud; waiting up to ${RUN_SECONDS}s for that run to finish"
  row=$(wait_for_run "$label" "$repo" "$run_id" "$log")
  set_run_result "$label" "$run_id" "$row" "$rc" "$repo" "$log"
}

# wait_for_run LABEL REPO RUNID LOG — the final row of one run, as the reader
# prints it, with a `poll=` word saying how the wait ended.
#
# `fugaro run` returns AT LAUNCH: the listing read straight after a launch holds
# an in-flight row for that run, and a reading taken from it is a FAIL the run
# never earned. So the check waits for the row of THIS run_id to be TERMINAL,
# the listing's own word for a run in a final status, and reads the push and the
# pull request only then. The row is picked out by run_id and never by position:
# the listing is newest-first and holds other runs.
wait_for_run() { # wait_for_run LABEL REPO RUNID LOG
  local label=$1 repo=$2 run_id=$3 log=$4 waited=0 polls=0 row=""
  [ -n "$run_id" ] || { printf 'poll=norunid\n'; return 0; }
  while :; do
    row=""
    if call_q "$CLI_SECONDS" "$SCRATCH/rows-${label}.json" "$log" \
      fugaro runs ls --repo "$repo" --project "$INSTALLATION" --json; then
      row=$(reader run "$SCRATCH/rows-${label}.json" "$run_id" 2>>"$log" || true)
    fi
    if [ "$(field_of found "$row")" = yes ] && [ "$(field_of terminal "$row")" = yes ]; then
      printf 'poll=terminal\n%s\n' "$row"
      return 0
    fi
    polls=$((polls + 1))
    if [ "$polls" -ge "$RUN_POLL_MAX" ]; then
      printf 'poll=timeout\n%s\n' "$row"
      return 0
    fi
    sleep "$RUN_POLL_SECONDS"
    waited=$((waited + RUN_POLL_SECONDS))
    if [ "$waited" -ge "$RUN_SECONDS" ]; then
      printf 'poll=timeout\n%s\n' "$row"
      return 0
    fi
  done
}

# set_run_result LABEL RUNID ROW RC REPO LOG — the fact for one run of an
# allow-listed repository, from the row wait_for_run settled on.
set_run_result() { # set_run_result LABEL RUNID ROW RC REPO LOG
  local label=$1 run_id=$2 row=$3 rc=$4 repo=$5 log=$6
  local status terminal pushed pr poll job
  status=$(field_of status "$row")
  terminal=$(field_of terminal "$row")
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  poll=$(field_of poll "$row")
  job=$(field_of job "$row")
  if [ "$pushed" = yes ] && [ -n "$pr" ] && [ "$pr" != 0 ]; then
    if [ -n "$JOB" ] && [ "$job" != "$JOB" ]; then
      fact "$label" FAIL "the run in ${repo}${run_id:+ (${run_id})} pushed and opened pull request #${pr}, but it executed on the job '${job:-unread}' and not on the one shared job the deploy left ('${JOB}'): a run on another job is a launch that did not resolve this profile, and nothing about the shared job is read from it. The execution path in the run's own row is where its job is written down"
      return 0
    fi
    fact "$label" PASS "the run in ${repo}${run_id:+ (${run_id})} pushed its branch and opened pull request #${pr}, executing on the shared job '${job:-unread}' (the row's pushed=yes, pr=${pr}, terminal=${terminal:-no}), status=${status:-unread}. It cloned, pushed and opened its pull request on a job that holds NO Bitbucket token, so its git credential was the credentials service's, paired to the repository the profile admits (D2302)"
    return 0
  fi
  if [ "$rc" -ne 0 ]; then
    fact "$label" FAIL "the run in ${repo} was never launched: fugaro run exited ${rc}${run_id:+, and named run ${run_id}} before the launch was confirmed, so there is no run of this check's to read: $(last_line "$log")"
    return 0
  fi
  if [ "$poll" = norunid ]; then
    fact "$label" FAIL "fugaro run --json named NO run id for the run in ${repo}, so which row of the listing belongs to this run cannot be read — and the row is the reading, not the launch"
    return 0
  fi
  if [ "$poll" != terminal ]; then
    fact "$label" FAIL "no row in a FINAL status for the run in ${repo}${run_id:+ (${run_id})} appeared within the wait (poll=${poll:-nolisting}; the last row read: found=$(field_of found "$row" || true), status=${status:-unread}, pushed=${pushed:-unread}, pr=${pr:-unread}). A run still going at the cap is a row for the register's section, not a pass: $(last_line "$log")"
    return 0
  fi
  if [ "$pushed" = yes ]; then
    fact "$label" FAIL "the run in ${repo} pushed its branch but its row names no pull request (pr=0, status=${status:-unread}): the push is what says the service served its git, and the pull request is the other half of what a shared job's run must reach"
    return 0
  fi
  fact "$label" FAIL "the run in ${repo} did not push (its final row reads pushed=no, status=${status:-unread}): $(last_line "$log"). A run that never reached its clone has skipped the reading rather than passed it — and the case to tell apart is a run halted before the allow-list was asked at all (an empty shared-job class table, or a bare Cloud Run 403), which is not this check's refusal"
}

# step_third_run: the third repository, launched by hand against the same shared
# job. Its run must be REFUSED before any git credential exists, and the
# refusal's own words must name the repository — that is what tells the
# service's allow-list refusal apart from the two non-readings the register's
# section names (the service's own authenticator refusing an empty class table,
# and a bare 403 from Cloud Run's front end), neither of which names a
# repository.
step_third_run() {
  local rc=0 repo=$THIRD_REPO
  local task="$SCRATCH/task-third.md" json="$SCRATCH/run-third.json" log="$SCRATCH/run-third.log"
  cat >"$task" <<TASK_EOF
Add a line to the repository's README.md. This task exists so Fugaro's Check 181 launches a run of a
repository the token's block does NOT name, against the same shared job: the run must be refused
before any git credential exists, and the refusal must name this repository.
TASK_EOF
  say "launching one run in ${repo}, which the token's block does not name, against the same shared job (it must be refused before any git credential)"
  call_q "$RUN_SECONDS" "$json" "$log" \
    fugaro run --repo "$repo" --project "$INSTALLATION" \
    --title "Check 181: the third repository, refused" \
    --task-file "$task" --json || rc=$?
  local run_id row
  run_id=$(field_of run_id "$(reader runid "$json" 2>/dev/null || true)")
  row=$(wait_for_run third "$repo" "$run_id" "$log")
  local found terminal pushed pr job halted detail reason
  found=$(field_of found "$row")
  terminal=$(field_of terminal "$row")
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  job=$(field_of job "$row")
  halted=$(field_of halted "$row")
  detail=$(field_of halt_detail "$row")
  reason=$(field_of halt_reason "$row")
  if [ "$pushed" = yes ]; then
    fact run-third FAIL "the run of ${THIRD_REPO}${run_id:+ (${run_id})} CLONED AND PUSHED on the shared job (pushed=yes, pr=${pr:-0}, job=${job:-unread}): the token's block does not name this repository, so the allow-list is NOT the boundary the design says it is. This is the finding no other check of the block would show"
    return 0
  fi
  if [ "$found" != yes ] || [ "$terminal" != yes ]; then
    fact run-third UNVERIFIED "no terminal row for the run of ${THIRD_REPO}${run_id:+ (${run_id})} was read within the wait (found=${found:-no}, terminal=${terminal:-no}; fugaro run exited ${rc}): $(last_line "$log"). Whether the run was refused cannot be read from a row this check does not have, so this criterion is NOT settled. Read it yourself: fugaro runs ls --repo ${THIRD_REPO} --project ${INSTALLATION} --json"
    return 0
  fi
  if [ "$halted" != yes ] || [ -z "$detail" ]; then
    fact run-third UNVERIFIED "the run of ${THIRD_REPO}${run_id:+ (${run_id})} ended without a halt this check can read (halted=${halted:-no}, reason=${reason:-none}), so the refusal's own words are NOT in its row: a run that did not push and says nothing about why is not a reading of the allow-list, and recording it as one would claim the allow-list held when nothing of the allow-list was asked. Read the run yourself and record what it said"
    return 0
  fi
  if printf '%s' "$detail" | grep -Fq -- "$THIRD_REPO"; then
    fact run-third PASS "the run of ${THIRD_REPO}${run_id:+ (${run_id})} was REFUSED before any git credential (pushed=no, pr=0, halt reason ${reason:-unread}) and the refusal names the repository: $(printf '%s' "$detail" | cut -c1-260). The published block does not admit it, the profile admits it, and the run stopped rather than finding a way around — the allow-list doing its job on a job that holds no credential to fall back on"
  else
    fact run-third FAIL "the run of ${THIRD_REPO} halted (reason ${reason:-unread}) but its own words do NOT name the repository, so this is one of the two refusals that is NOT this step's (the service's authenticator refusing an empty shared-job class table, or a bare Cloud Run 403 from the front end): $(printf '%s' "$detail" | cut -c1-260). Neither says anything about this profile, and a check recorded on it would claim the allow-list held when nothing of the allow-list was asked"
  fi
}

# ------------------------------------------------------------------ the block
# BLOCK_KEYS is the criteria, in the order the owner reads them: one row each,
# whatever the run measured.
BLOCK_KEYS=(installation agent-session belong workspace xtrace config token allow-list store
  profile subset-refusal mount-publish mount-deploy open-publish deploy job
  run-a run-b run-third restore)

# criteria_unmet FACTS — the keys that are not a PASS row, one per line. A
# criterion is one of BLOCK_KEYS: a row reading UNVERIFIED (the step could not
# verify its criterion) and a key no step wrote at all (the block prints those
# as "not reached"). Both are criteria that were NOT settled, and they are what
# makes a run INCOMPLETE rather than a pass.
criteria_unmet() {
  local facts=$1 key line
  for key in "${BLOCK_KEYS[@]}"; do
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    case "$line" in
    "$key|PASS|"*) ;;
    *) printf '%s\n' "$key" ;;
    esac
  done
}

verdict_of() { # verdict_of FACTS STATUS
  case "$2" in
  0) printf 'PASS' ;;
  1) printf 'FAIL' ;;
  *) printf 'INCOMPLETE' ;;
  esac
}

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 181 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached: nothing measured this criterion (the step that writes this row did not run, or it returned early)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 181 RESULT: %s (exit %s)\n' "$(verdict_of "$facts" "$status")" "$status" >&3
}

# ------------------------------------------------------------------- the plan
print_plan() {
  cat <<PLAN_EOF
Check 181 — a Bitbucket shared project job end to end, and the allow-list refusal at the publish,
at the deploy and at the proxy.
This is the PLAN. --dry-run touches nothing: no installation is read, no policy is published, no
secret is stored, no run is launched and no job is read.

  installation   ${INSTALLATION}
  workspace      ${WORKSPACE}   (the sandbox Bitbucket workspace; every repository below is checked against it)
  profile        ${PROFILE}   (the Bitbucket profile the shared job is deployed for)
  repositories   ${REPO_A}
                 ${REPO_B}
  third repo     ${THIRD_REPO}   (NOT in the token's block: refused at the publish, the deploy and the run)
  token file     ${TOKEN_FILE}
  run cap        ${RUN_SECONDS}s per run, polled every ${RUN_POLL_SECONDS}s up to ${RUN_POLL_MAX} polls
  cli cap        ${CLI_SECONDS}s per command, ${UPDATE_SECONDS}s for a converge

What the run will do, in this order:
  1. read the installation with 'fugaro doctor --project ${INSTALLATION} --json', and refuse 'belong'
     and a coding agent's session;
  2. read the config it is about to edit with 'fugaro project profile ls --json', aimed at the
     installation with FUGARO_PROJECT=${INSTALLATION} — the commands that edit a project's own config
     ('fugaro project profile', 'fugaro secrets allow') take no --project, and this proves the aim
     landed on ${INSTALLATION}.yaml before anything is written;
  3. 'fugaro secrets allow bitbucket-token ${REPO_A} ${REPO_B} --proxy' (the allow-list and the
     service switch), then 'fugaro secrets set bitbucket-token --project-wide' with the token file
     on stdin — the only command that ever sees the value;
  4. read the profile '${PROFILE}'; define it as a bitbucket profile if it is absent, give it the two
     allow-listed repositories as 'bitbucket/${REPO_A}' and 'bitbucket/${REPO_B}' (an allow_repos
     entry names a provider, an owner and a repository), and take any Bitbucket credential off it;
  5. put bitbucket-token ON the profile and add ${THIRD_REPO} to its allow_repos: 'fugaro project
     profile set' must REFUSE, naming the repository (projectpolicy.CheckBitbucketProfileRepos);
  6. put bitbucket-token ON the profile: 'fugaro project policy publish' and 'fugaro update' must
     each REFUSE (projectpolicy.CheckSharedJobProfile);
  7. take the entry off, add ${THIRD_REPO} to the tokenless profile's allow_repos, and publish:
     NOTHING must refuse — that is the D2302 rule read from the other side;
  8. 'fugaro update': one fugaro-p-… job for the profile, with FUGARO_CREDENTIALS_SERVICE_URL on it
     and no FUGARO_BITBUCKET_TOKEN anywhere (read with 'gcloud run jobs list/describe');
  9. one run of ${REPO_A} and one of ${REPO_B}: each must land on THAT job, push and open its pull
     request (polled until its row is terminal, then read by run id);
 10. one run of ${THIRD_REPO}: it must be REFUSED before any git credential, and the refusal's own
     words must name the repository.
It removes its scratch directory on the way out, however it ends, and says so in the block's restore
row. It leaves behind what the register's Restore names: the profile, its job, the two pull requests
and the project-wide secret.
PLAN_EOF
}

# ----------------------------------------------------------------------- main
USAGE="check181.sh --installation <sandbox> --token-file <path> --repo <owner/name> --repo <owner/name> --third-repo <owner/name> [--workspace <workspace>] [--profile <name>] [--dry-run]"

parse_args() {
  INSTALLATION=""
  TOKEN_FILE=""
  WORKSPACE=${CHECK181_WORKSPACE:-dimi_bmb}
  PROFILE=${CHECK181_PROFILE:-bitbucket-shared}
  REPO_A=""
  REPO_B=""
  THIRD_REPO=""
  DRY_RUN=no
  PROJECT_NAME=""
  GCP_PROJECT=""
  REGION=""
  JOB=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --token-file)
      [ $# -ge 2 ] || die "--token-file needs a path. usage: $USAGE"
      TOKEN_FILE=$2
      shift 2
      ;;
    --workspace)
      [ $# -ge 2 ] || die "--workspace needs a workspace. usage: $USAGE"
      WORKSPACE=$2
      shift 2
      ;;
    --profile)
      [ $# -ge 2 ] || die "--profile needs a name. usage: $USAGE"
      PROFILE=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs owner/name. usage: $USAGE"
      local name=$2
      case "$name" in
      */*/*) die "--repo's repository '$name' is not owner/name. usage: $USAGE" ;;
      */?*) ;;
      *) die "--repo's repository '$name' is not owner/name. usage: $USAGE" ;;
      esac
      if [ -z "$REPO_A" ]; then
        REPO_A=$name
      elif [ -z "$REPO_B" ]; then
        REPO_B=$name
      else
        die "--repo was given three times: this check runs for TWO allow-listed repositories. usage: $USAGE"
      fi
      shift 2
      ;;
    --third-repo)
      [ $# -ge 2 ] || die "--third-repo needs owner/name. usage: $USAGE"
      THIRD_REPO=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      # An intentional ending, not an abort: REACHED_END says so, and PRINTED
      # keeps the EXIT trap from printing a block of "not reached" rows for a run
      # that never started.
      printf 'usage: %s\n' "$USAGE"
      REACHED_END=yes
      PRINTED=1
      trap - ERR
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$INSTALLATION" ] || die "--installation is required. usage: $USAGE"
  [ -n "$TOKEN_FILE" ] || die "--token-file is required. usage: $USAGE"
  [ -n "$THIRD_REPO" ] || die "--third-repo is required (the repository the token's block does not name). usage: $USAGE"
  [ -n "$REPO_A" ] && [ -n "$REPO_B" ] || die "two --repo pairs are required. usage: $USAGE"
  [ "$REPO_A" != "$REPO_B" ] \
    || die "the two --repo pairs name the same repository: one run per repository is what makes the shared job a reading of two repositories, and one name for both measures it once. usage: $USAGE"
  case "$THIRD_REPO" in
  "$REPO_A" | "$REPO_B") die "--third-repo names one of the allow-listed repositories: it has to be one the token's block does NOT name. usage: $USAGE" ;;
  esac
}

main() {
  parse_args "$@"
  CLI_SECONDS=${CHECK181_CLI_SECONDS:-120}
  UPDATE_SECONDS=${CHECK181_UPDATE_SECONDS:-1800}
  RUN_SECONDS=${CHECK181_RUN_SECONDS:-5400}
  RUN_POLL_SECONDS=${CHECK181_RUN_POLL_SECONDS:-20}
  RUN_POLL_MAX=${CHECK181_RUN_POLL_MAX:-270}
  if [ "${CHECK181_DRY_RUN:-}" = yes ]; then DRY_RUN=yes; fi
  guard_belong
  # Asked in both endings: the plan is refused for a repository outside the
  # sandbox workspace exactly as the run is. Only the run's call writes the row,
  # because a run that only prints the plan has made no scratch directory for it
  # to land in.
  guard_workspace

  if [ "$DRY_RUN" = yes ]; then
    print_plan
    REACHED_END=yes
    PRINTED=1
    trap - ERR
    exit 0
  fi

  # After the two endings above rather than before the arguments are read: a run
  # that only prints the plan, or only the usage, calls none of these tools.
  need_tools
  scratch_up
  write_reader
  guard_workspace
  guard_xtrace
  guard_agent_session

  step_installation
  step_config
  step_token
  step_profile
  step_subset_refusal
  step_mount_refusal
  step_open_publish
  step_deploy
  step_run run-a "$REPO_A"
  step_run run-b "$REPO_B"
  step_third_run

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed
# -- including through the cleanup. A Ctrl-C while the scratch directory is being
# removed would otherwise kill this shell with it still on disk.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
