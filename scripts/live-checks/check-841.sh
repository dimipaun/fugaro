#!/usr/bin/env bash
# Check 841 — the grant moves and nothing stops: one `fugaro update` on a live
# installation at the previous release, with a run in flight, and what a run,
# a second account, a copied ticket, an older CLI and a pairing each then do.
#
# Plan task LM17 of docs/launcher-does-not-mint/implementation-plan.md writes
# this check, with the whole plan behind it. What it settles is read from the
# outside and nowhere else:
#
#   * **the update with a run in flight** (docs/design/update-in-flight.md, the
#     owner's requirement of 2026-11-09) on the release that TAKES THE GRANT
#     AWAY: a run launched before the update finishes, its record whole, and the
#     update's own line says how many runs in flight it waits for — none — and
#     that it changes nothing they hold. The installation this check names is at
#     the previous release when it starts: its published policy node reads the
#     version before this release's, which is the state `brew upgrade` leaves
#     behind (R24, LM21);
#   * **a new launch gets a session** (LM1, LM2, LM6, R2): a run launched after
#     the update asks the credentials service for its identity — the service
#     writes an issuance record for it and the run claims the ticket it was
#     handed, which is what `claimed` on that record means — and the run's own
#     ledger node is there. A run that started with no issuance record is a
#     launcher that signed the run's identity itself: the bridge of R7, which
#     after this release's move is a grant that is still in place somewhere;
#   * **a second account's ask for that run is refused** (R5, F13): an account
#     on the installation's roster, impersonated, asks the ticket route for the
#     run the FIRST launcher started, and the answer must be the service's own
#     refusal of another caller's record — not a ticket, and not a refusal about
#     the account, which would be a reading about the roster and not about the
#     record;
#   * **a copied ticket loses** (R2, R3, D3122): the check asks for a ticket for
#     a run id it makes up, claims it once as the run's own job account — which
#     is what a run does — and then presents the SAME ticket again with another
#     nonce. A ticket is claimed once: the copy must be refused. The ticket and
#     the custom token the first claim answers are handles of this check's own
#     made-up run, they are never printed, and they die with the record the
#     check removes;
#   * **an older CLI prints the upgrade sentence** (LM21, LM23): a binary of the
#     release behind, named with --old-cli, reads the raised node and refuses it
#     with the sentence this tree's own constants spell — the node's version,
#     the version that binary understands, and the one command that fixes it;
#   * **`fugaro pair` pairs** (LM10, R7, R14): after the update the pairing token
#     can only come from the credentials service — the operator's own credential
#     no longer holds the role that mints one — so a pairing that reaches the
#     code is a pairing the service served. The check runs it with --json, reads
#     the code event, and lets it time out waiting for a phone that is not there.
#
# What it measures, row by row:
#
#   * tree: the constants the readings below are judged against, read out of
#     this checkout rather than remembered — the ticket's variable and the two
#     identity routes (internal/identitywire/wire.go), the refusal codes the
#     ticket and claim answers carry (the same file), the policy version this
#     release writes and the release that introduced it
#     (internal/projectpolicy/version.go), the sentence a reader older than the
#     node prints (the same file), the escaping the record's and the ledger's
#     two names go through (internal/budget/model.go), and the service and
#     database the run's own environment names
#     (internal/backend/batch/env.go, internal/runner/budget_session.go);
#   * installation: the installation this check is about. The project and the
#     region are literals of this script and no flag or variable can name
#     another, the local configuration at CHK841_CONFIG must be theirs, it must
#     state the Realtime Database it reads and the credentials service it asks,
#     and the checkout's own `fugaro doctor --json` must agree about the project
#     and the region. Anything else STOPS the check (exit 2);
#   * premise: the installation is at the PREVIOUS release — its published
#     policy node reads one below the version this release writes — and the
#     `fugaro` on PATH is this release or newer. A node already at this
#     release's version means the update has been run and there is nothing left
#     to observe: that STOPS the check, and so does a CLI of the release behind;
#   * run: ONE throwaway run of the checkout, launched BEFORE the update, under
#     a title and a task file this check writes. The task asks for a report and
#     changes no file. The row holds the run's own job, its account and its
#     branch, and waits until `fugaro runs ls` shows it running — which is what
#     makes the update below an update with a run in flight;
#   * update: the real `fugaro update`, with its stdin on /dev/null, its streams
#     captured and a deadline (TERM, then KILL). Exit 0 must come with the
#     update's own line about the runs in flight — the count, and that it waits
#     for none and changes nothing they hold — and with the policy node raised
#     to this release's version. Exit 1 with the CLI's "N step(s) left for you"
#     is UNVERIFIED and names the line to run; anything else is a finding;
#   * asked: every line the update printed, read for the shapes a question
#     leaves behind ([y/N] in any casing, (y/n), (yes/no), a `to confirm:` left
#     open, an enter-prompt, a question left waiting on a blank). One is FAIL;
#   * inflight: the run launched before the update, followed until its record is
#     terminal. It has to still be there, its status has to be one a run
#     reaches (`succeeded` above all), and its branch, pull request and outcome
#     have to be readable;
#   * session: ONE more throwaway run of the checkout, launched AFTER the
#     update, and what the credentials service holds about it: its issuance
#     record, which the launcher's ask wrote, reading `claimed` when the run
#     presented its ticket — and the run's ledger node, which is the session it
#     opened. The record is the reading this row exists for: a launcher that
#     signed the run's identity itself writes none;
#   * second: the second account named with --second-launcher asks the ticket
#     route for the run of the `session` row. It must be refused, the refusal
#     must be the service's own about another caller's record, and a refusal
#     about the account (the roster does not admit it) is UNVERIFIED rather than
#     a pass: that answer would be about the roster;
#   * copy: a ticket for a run id this check makes up, claimed once as the run's
#     own job account and then presented again with another nonce. The copy must
#     be refused. A ticket served twice is the finding, and the check removes
#     the record it made afterwards;
#   * old-cli: the binary named with --old-cli reads the raised node and prints
#     the upgrade sentence. Without that binary the row is UNVERIFIED, and it
#     names how to get one;
#   * pair: `fugaro pair --json` in the checkout, with its stdin on /dev/null:
#     the code event can only come from the credentials service now, so the
#     event is the reading, and the command then times out waiting for a phone
#     that is not there. Its output is a stream of events, and this check reads
#     the event NAMES: the payload, which is the pairing's own secret, is never
#     printed and its file is in the 0700 scratch directory;
#   * restore: the scratch directory, removed from the exit trap. What this
#     check created is said rather than assumed: TWO runs of the checkout
#     (**billable**: their model calls are real and the installation's budget
#     governs them), their branches and their pull requests, which stay on the
#     repository — this check holds no credential of its own to remove them —
#     and the issuance record the `copy` row made, which it removes.
#
# Usage:      bash scripts/live-checks/check-841.sh --second-launcher <account>
#             bash scripts/live-checks/check-841.sh --second-launcher <a> --old-cli <binary>
#             bash scripts/live-checks/check-841.sh --dry-run
#             bash scripts/live-checks/check-841.sh --help
# Needs:      bash, mktemp, python3, curl (the identity asks are HTTP POSTs to
#             the installation's own credentials service), gcloud (the identity
#             tokens those asks carry, the job's own account, and the reads of
#             the installation's Realtime Database) and fugaro (the installed
#             release — the update this check runs and the two runs it launches
#             are the ones you have). BEFORE it runs: `brew upgrade` (or
#             whatever installs fugaro) must have put THIS release on PATH,
#             while the installation is still at the previous one — the check
#             runs the new CLI against an installation the update has not yet
#             moved. It uses no credential of its own: every read is the
#             operator's own gcloud identity, and an account named with
#             --second-launcher is IMPERSONATED.
# Where:      the dogfood installation, fugaro-dev in us-east5 — the project and
#             the region are literals of this script, as they are in the other
#             live checks here, and the installation's own configuration must
#             agree (CHK841_CONFIG points at it when it is not the default). No
#             step of it names another project, and a coding agent's session is
#             refused: every command this check runs is one you could type
#             yourself.
# Costs:      wall clock, not a fixed fee: the update (Terraform's own applies,
#             often half an hour) plus TWO runs of the installation's agent
#             (**billable**, capped at --max-usd each) — the one in flight across
#             the update and the one the `session` row launches after it.
# Runs:       by YOU. No agent runs any step of it — CHK841_OWNER_RUN=yes is
#             required, as it is for the other checks that launch runs — and its
#             own test (check-841_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C, and the issuance record the `copy` row made
#             for its own made-up run. The TWO runs' branches and pull requests
#             are NOT removed: this check holds no credential of its own. The
#             update's own writes to the installation stay, because that is what
#             an update is.
# Output:     progress on stderr, ONE block on stdout headed CHECK 841 RESULT, and
#             nothing else. Exit 0: every criterion that was measured held. 1: a
#             measured criterion did not hold. 2: a row could not be measured for
#             a reason that is this check's own setup or the installation's
#             configuration rather than a break (the block says which, by name)
#             — a guard that stopped the check before it could run, the premise,
#             a read that did not answer. 3: a criterion was read and its answer
#             is UNVERIFIED — a step left for you, an account the roster does not
#             admit, no --old-cli, a run that was not in flight. ONLY an all-PASS
#             block is a PASS.
#
# A NOTE ON WHAT IS NEVER PRINTED: no identity token, no ticket, no custom token
# and no pairing payload. A token this check mints goes into a 0600 curl config
# file and reaches no command line, no fact and no log line; a ticket and the
# token a claim answers go into 0600 body files under the scratch directory and
# are read by the shell alone; the pairing stream is read for its event NAMES
# and never for its payload. Every fact below is a NAME, a status or a count.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHK841_CONFIG (the installation's local configuration; the default is the
# dogfood installation's own), CHK841_REPO (or --repo: the checkout to run in,
# default the working directory), CHK841_SECOND_LAUNCHER (or --second-launcher:
# the account the `second` row's ask is made as, REQUIRED), CHK841_OLD_CLI (or
# --old-cli: a fugaro binary of the release behind; the `old-cli` row is
# UNVERIFIED without one), CHK841_DEADLINE (or --deadline: how long `fugaro
# update` may take, in seconds; 1800), CHK841_START_SECONDS (or --start-wait: how
# long a launched run may take to appear `running`, in seconds; 900),
# CHK841_CLAIM_SECONDS (or --claim-wait: how long the `session` row's run may
# take to claim its ticket, in seconds; 900), CHK841_WAIT_SECONDS (or --wait: how
# long the run in flight may then take to reach its terminal status, in seconds;
# 3600), CHK841_POLL_SECONDS (20), CHK841_PAIR_SECONDS (or --pair-wait: how long
# `fugaro pair` waits for a phone before the row reads its stream, in seconds;
# 5), CHK841_MAX_USD (or --max-usd: the cap each launch passes as --max-usd; 1;
# empty passes none and lets the installation's own stand), CHK841_NO_RUN (or
# --no-run: launch no run at all — the installation, premise, update and asked
# rows are still measured, and the rows that need a run read UNVERIFIED),
# CHK841_KEEP_SCRATCH and CHK841_DRY_RUN.
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

# No xtrace, whatever the shell it was started from had: this check prints its
# own RESULT block, and a trace of a run that converges an installation, launches
# two runs and mints identity tokens would copy every one of them to the
# terminal.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

REPORT=yes
FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED|INFO "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

one_line() { printf '%s' "$1" | tr '\n\r\t' '   ' | tr -s ' ' | sed 's/^ *//; s/ *$//' | cut -c1-500; }

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check is built on it"
}

# field FILE KEY prints the value of one `key=value` line, the empty string when
# the key is absent.
field() { # field <file> <key>
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1 || true
}

# last_line FILE: the last non-blank line of a file, bounded, for a row a reader
# has to be able to scan.
last_line() {
  if [ -f "$1" ]; then
    grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | cut -c1-300 || true
  fi
}

# left_for_you FILE...: the CLI's own sentence that a converge stopped with steps
# only you can run — "N step(s) left for you" — or the empty string. It is what
# tells a refusal from a hand-off on exit 1 (internal/cli/init_stages.go), and
# Check 821 reads the same line for the same reason.
left_for_you() {
  grep -h -o -E '[0-9]+ step\(s\) left for you' "$@" 2>/dev/null | tail -1
}

# --------------------------------------------------------------- the guards
# The project and the region are literals, as they are in every live check here:
# nothing reads gcloud's ambient configuration, and there is no flag that could
# point either elsewhere.
P=fugaro-dev
L=us-east5
# The two runs this check launches are capped through --max-usd, which may only
# tighten the ceiling the launch resolves: a refusal of it is the launch row's
# to report. An empty value passes no cap and lets the installation's own stand.
MAX_USD=${CHK841_MAX_USD-1}
# How much of the run's life the `copy` row asks the service for. It must be a
# positive number (the ask is refused without one) and small: the ticket this row
# asks for belongs to a run that will never exist.
COPY_RUNTIME_SECONDS=${CHK841_COPY_RUNTIME:-600}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-841.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run — and none of the identity tokens and tickets it handles — is printed a second time into a log"
}

# guard_owner: the register's owner-run marker, the same one the other owner-run
# checks carry. It is not a security boundary — an agent's shell can export
# anything — but it is the line that stops a session from converging an
# installation, launching two runs and asking for tokens.
guard_owner() {
  if [ "${CHK841_OWNER_RUN:-}" != yes ]; then
    fact owner FAIL "this is an owner-run check and CHK841_OWNER_RUN is not exported as yes; no agent runs any step of it"
    stop "Check 841 is owner-run. Run it at your own terminal, after: export CHK841_OWNER_RUN=yes — no agent performs any step of it. It converges the installation with a run in flight and launches two real runs"
  fi
  fact owner PASS "CHK841_OWNER_RUN=yes: this run was started by you at your own terminal, and no agent performs any step of Check 841"
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 841: it converges a real installation, launches runs and asks for identity tokens. Run it yourself: bash scripts/live-checks/check-841.sh"
}

# read_const pulls one quoted Go constant out of a file: the tree is the
# authority for the ticket's variable, the identity routes and the sentence an
# older reader prints, and a copy of any of them in this script would be a second
# home for a fact the check exists to test.
read_const() { # read_const FILE CONSTNAME
  # The declaration may be its own `const Name = "value"` line or a line inside a
  # `const (...)` block, and the two shapes are both used by the tree this reads:
  # the prefix is allowed rather than the two files being read differently. An
  # anchored name is what keeps `Version` from reading `RulesVersion`.
  sed -n "s/^[[:space:]]*\(const[[:space:]][[:space:]]*\)\?$2[[:space:]]*=[[:space:]]*\"\([^\"]*\)\".*/\2/p" "$1" 2>/dev/null | head -n 1 || true
}

# read_int_const is read_const for a constant that is a number rather than a
# string: the policy model's version is the one this check reads that way.
read_int_const() { # read_int_const FILE CONSTNAME
  sed -n "s/^[[:space:]]*\(const[[:space:]][[:space:]]*\)\?$2[[:space:]]*=[[:space:]]*\([0-9][0-9]*\).*/\2/p" "$1" 2>/dev/null | head -n 1 || true
}

# version_at_least HAVE WANT: whether HAVE is WANT or a later release, both
# written X.Y.Z with an optional leading v. "dev" and anything else that is not a
# release is not compared here — the caller decides what a build with no release
# means — and this is python3's arithmetic rather than `sort -V`, which BSD sort
# does not have.
version_at_least() { # version_at_least HAVE WANT
  python3 -c 'import sys
def parts(v):
    v = v.lstrip("v")
    try:
        return tuple(int(x) for x in v.split(".")[:3])
    except ValueError:
        return None
have, want = parts(sys.argv[1]), parts(sys.argv[2])
sys.exit(0 if have is not None and have >= want else 1)' "$1" "$2"
}

# guard_tree: the checkout this script lives in, read for the things the live
# readings are judged against rather than remembering: the variable a launcher
# puts the ticket in, the two identity routes and the refusal codes
# (internal/identitywire/wire.go), the version this release writes, the release
# that introduced it and the sentence a reader older than the node prints
# (internal/projectpolicy/version.go), the escaping the record's and the ledger's
# two names go through (internal/budget/model.go), the service's own URL's
# variable (internal/backend/batch/env.go) and the database's
# (internal/runner/budget_session.go).
guard_tree() {
  TREE=$(cd "$(dirname "$0")/../.." 2>/dev/null && pwd) \
    || stop "Check 841 stopped: the script's own path ($0) does not lead to a checkout, so the ticket's variable, the identity routes and the sentence an older CLI prints cannot be read out of the tree"
  local wire="$TREE/internal/identitywire/wire.go"
  local versionfile="$TREE/internal/projectpolicy/version.go"
  local budget="$TREE/internal/budget/model.go"
  local envgo="$TREE/internal/backend/batch/env.go"
  local budgetenv="$TREE/internal/runner/budget_session.go"
  for f in "$wire" "$versionfile" "$budget" "$envgo" "$budgetenv"; do
    [ -f "$f" ] || stop "Check 841 stopped: $TREE does not carry ${f#"$TREE"/}, so this is not the Fugaro tree the script belongs to"
  done
  TICKET_ENV=$(read_const "$wire" TicketEnv)
  TICKET_PATH=$(read_const "$wire" TicketPath)
  CLAIM_PATH=$(read_const "$wire" ClaimPath)
  RECORDS_PATH=$(read_const "$wire" pathRecords)
  REFUSED_CODE=$(read_const "$wire" RefusalIdentityRefused)
  NOT_ADMITTED_CODE=$(read_const "$wire" RefusalCallerNotAdmitted)
  NOT_SERVED_CODE=$(read_const "$wire" RefusalNotServed)
  POLICY_VERSION=$(read_int_const "$versionfile" Version)
  SERVICE_ENV=$(read_const "$envgo" credentialsServiceEnv)
  RTDB_ENV=$(read_const "$budgetenv" RTDBURLEnv)
  # The sentence an older reader prints, in the three pieces the tree spells it
  # in: the shared sentence with both versions in it, the CLI's own remedy, and
  # the version's own arriving clause. SkewSentence joins the first two for a node
  # AHEAD of its reader — which is the direction this check is about — and appends
  # the third when the reader's own table knows the release, which is a fact of
  # the reader and not of the node.
  SKEW_FMT=$(read_const "$versionfile" Skew)
  SKEW_SUFFIX=$(sed -n 's/^[[:space:]]*\(const[[:space:]][[:space:]]*\)\?CLISkew[[:space:]]*=[[:space:]]*Skew[[:space:]]*+[[:space:]]*"\(.*\)"[[:space:]]*$/\2/p' "$versionfile" 2>/dev/null | head -n 1)
  ARRIVAL_FMT=$(grep -o -- '; policy version %d arrived in fugaro %s' "$versionfile" 2>/dev/null | head -n 1)
  [ -n "$TICKET_ENV" ] && [ -n "$TICKET_PATH" ] && [ -n "$CLAIM_PATH" ] \
    || stop "Check 841 stopped: ${wire#"$TREE"/} no longer carries TicketEnv, TicketPath and ClaimPath, so the ticket's variable and the two routes would have to be remembered here instead of read"
  [ -n "$RECORDS_PATH" ] && [ -n "$REFUSED_CODE" ] && [ -n "$NOT_ADMITTED_CODE" ] && [ -n "$NOT_SERVED_CODE" ] \
    || stop "Check 841 stopped: ${wire#"$TREE"/} no longer carries pathRecords, RefusalIdentityRefused, RefusalCallerNotAdmitted and RefusalNotServed, so the issuance record's path and the refusal codes this check reads would have to be remembered here"
  [ -n "$POLICY_VERSION" ] && [ -n "$SKEW_FMT" ] && [ -n "$SKEW_SUFFIX" ] \
    || stop "Check 841 stopped: ${versionfile#"$TREE"/} no longer carries Version, Skew and CLISkew, so the version this release writes and the sentence an older CLI prints would have to be remembered here"
  [ -n "$SERVICE_ENV" ] && [ -n "$RTDB_ENV" ] \
    || stop "Check 841 stopped: the credentials service's URL's variable (internal/backend/batch/env.go's credentialsServiceEnv) and the database's (internal/runner/budget_session.go's RTDBURLEnv) are gone from the tree"
  # The version below this release's is the one the installation publishes before
  # the update, and the release that reads it is the table's row for THIS
  # release's version. A table without that row leaves nothing to compare the CLI
  # on PATH against.
  PREV_VERSION=$((POLICY_VERSION - 1))
  THIS_RELEASE=$(sed -n "s/^[[:space:]]*$POLICY_VERSION:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$versionfile" 2>/dev/null | head -n 1)
  [ -n "$THIS_RELEASE" ] \
    || stop "Check 841 stopped: ${versionfile#"$TREE"/}'s VersionSince carries no row for version ${POLICY_VERSION}, so the release this check is about cannot be read out of the tree"
  # The escaping the record's two names go through: the check builds the record's
  # and the ledger's paths itself, so it refuses to run against a tree whose Key
  # escapes something else rather than reading a path nobody writes.
  grep -qF "c == '%' || c == '.' || c == '\$' || c == '#' || c == '[' || c == ']' || c == '/'" "$budget" \
    || stop "Check 841 stopped: ${budget#"$TREE"/}'s Key no longer escapes the set this script escapes when it builds the issuance record's and the ledger's paths"
  grep -qF 'pathRecords + "/" + budget.Key(slug) + "/" + budget.Key(run)' "$wire" \
    || stop "Check 841 stopped: ${wire#"$TREE"/}'s PathRecord no longer joins the record's path the way this script builds it, so the path it would read is not the service's"
  local sha
  sha=$(git -C "$TREE" rev-parse HEAD 2>/dev/null || echo unknown)
  TREE_SHA=$sha
  fact tree PASS "the tree at ${TREE} (commit ${sha}) was read, never remembered: the ticket's variable ${TICKET_ENV}, the ticket's route ${TICKET_PATH} and the claim's ${CLAIM_PATH}, the refusal codes ${REFUSED_CODE}, ${NOT_ADMITTED_CODE} and ${NOT_SERVED_CODE}, the record's path family ${RECORDS_PATH} (its two names escaped as budget.Key does), policy version ${POLICY_VERSION} — written by ${THIS_RELEASE}, the release this check is about, whose reader is one version above the node this installation publishes (${PREV_VERSION}) — the sentence an older reader prints (\"$(printf "$SKEW_FMT" "$POLICY_VERSION" "$PREV_VERSION")${SKEW_SUFFIX}\"), the service's URL's variable ${SERVICE_ENV} and the database's ${RTDB_ENV}"
}

# guard_belong: the owner's own installation and the one argument that would make
# the two attacks pointless. This check names its project itself and has no flag
# that could name another, so the guard is about what the environment could
# supply instead.
guard_belong() {
  local ambient=${CLOUDSDK_CORE_PROJECT:-}${GOOGLE_CLOUD_PROJECT:-}${GCLOUD_PROJECT:-}
  local low
  low=$(printf '%s' "$ambient" | tr 'A-Z' 'a-z')
  case "/${low}/" in
  *belong* | *edgeweb* | *edgeserver*)
    fact belong FAIL "the ambient gcloud configuration names a project matching '${ambient}', and the owner's own installation (belong, EdgeWeb, EdgeServer) is the one thing no step of this check may touch"
    stop "Check 841 never runs against 'belong', EdgeWeb or EdgeServer. Unset CLOUDSDK_CORE_PROJECT/GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT and run it again: every command here names --project=${P} itself"
    ;;
  esac
  if [ -z "$SECOND" ]; then
    fact belong FAIL "no second launcher was named, and the second account's ask is what the 'second' row is: without one the ask would be made by the account the check runs as, which is the launcher of the run and not a second one"
    stop "Check 841 needs --second-launcher <account>: an account on the installation's roster that may ask for a run's ticket, which this check impersonates for one ask. Run: CHK841_OWNER_RUN=yes bash scripts/live-checks/check-841.sh --second-launcher <account>"
  fi
  case "$SECOND" in
  *@*) ;;
  *)
    fact belong FAIL "the second launcher '${SECOND}' is not an account address, so nothing can be impersonated as it"
    stop "Check 841's --second-launcher must be the account's address, as gcloud --impersonate-service-account takes it"
    ;;
  esac
  local mine
  mine=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
  if [ -n "$mine" ] && [ "$(printf '%s' "$mine" | tr 'A-Z' 'a-z')" = "$(printf '%s' "$SECOND" | tr 'A-Z' 'a-z')" ]; then
    fact belong FAIL "the second launcher '${SECOND}' is the account this check is authenticated as (${mine}), so the ask would be made by the run's own launcher rather than by a second one, and a refusal could not be told from the launcher's own access"
    stop "Check 841's --second-launcher must be another account than the one gcloud is authenticated as (${mine}): the ask is about what a second account can and cannot obtain"
  fi
  MY_ACCOUNT=$mine
  if [ -n "$OLD_CLI" ] && [ ! -x "$OLD_CLI" ]; then
    fact belong FAIL "the --old-cli '${OLD_CLI}' is not an executable file, so the older CLI's own answer cannot be read: the row would be a reading of nothing"
    stop "Check 841's --old-cli must be a fugaro binary of the release behind (an executable file). Leave it out and the old-cli row reads UNVERIFIED instead"
  fi
  fact belong PASS "the project this check names is ${P} and the region ${L}, both hard-coded; neither 'belong' nor EdgeWeb nor EdgeServer is named by any step, the one attack is made as the second launcher ${SECOND} (impersonated), and no flag or variable of this check can name another project$( [ -n "$OLD_CLI" ] && echo ", and the older CLI's answers come from ${OLD_CLI}" || echo "" )"
}

# ------------------------------------------------------------------- readers
# The documents this check reads, in one program: the installation's own
# configuration, doctor's answer, a launch's own --json, a run's row in `runs ls
# --json`, the published policy's version, the update's transcript read for the
# shapes a question leaves behind, the pairing stream read for its event names,
# an issuance record, a run's ledger node, an identity route's refusal and one
# job's own account. A machine running a live check has python3 and no jq, and a
# row that read a transcript with grep would miss a question the day one is
# printed in another shape. Every query prints `key=value` lines, so the shell
# holds them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys

# The shapes a prompt leaves in a transcript, all of them in lower case: the
# answer a read waits for ([y/N] in any casing, (y/n), (yes/no)), the typed
# confirmation's own line left open, an enter-prompt, and a question mark
# followed by a blank -- a question left waiting for an answer. This CLI's own
# text has none of them: its refusals are one sentence ending in a full stop.
# Check 380 and Check 821 read the same list for the same reason.
NEEDLES = ("[y/n]", "(y/n)", "(yes/no)", "to confirm:", "press enter", "hit enter", "press return", "hit return")


def load(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except Exception:
        return None


def pick(row, key):
    value = row.get(key)
    if value is None:
        return ""
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (dict, list)):
        return json.dumps(value)
    return value


def kv(key, value):
    print("%s=%s" % (key, str(value).replace("\n", " ")))


def strip(value):
    return value.strip().strip('"').strip("'")


what = sys.argv[1]

if what == "cfg":
    # The installation's own configuration, read as the two things this check
    # must not remember: the project and region it is about (which must be this
    # script's own literals) and the two addresses a run's identity travels
    # between -- the Realtime Database and the credentials service. The file is
    # an ordinary YAML document, and this reads the KEYS by their indentation
    # rather than with a parser a live machine may not have: a key under a block
    # is `block.key`.
    block = ""
    out = {}
    try:
        with open(sys.argv[2], "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except OSError:
        text = ""
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#") or ":" not in line:
            continue
        indent = len(line) - len(line.lstrip(" "))
        key, _, value = line.strip().partition(":")
        key = strip(key)
        value = strip(value)
        if indent == 0:
            block = key
            if value:
                out.setdefault(key, value)
            continue
        out.setdefault("%s.%s" % (block, key), value)
    kv("path", sys.argv[2])
    for key in ("name", "gcp_project", "region", "rtdb_url", "budget.rtdb_url", "credentials_service.url"):
        kv("cfg_" + key.replace(".", "_"), out.get(key, ""))
elif what == "doctor":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    project = doc.get("project") if isinstance(doc.get("project"), dict) else {}
    checkout = doc.get("fugaro_yaml") if isinstance(doc.get("fugaro_yaml"), dict) else {}
    kv("name", project.get("name") or "")
    kv("gcp", project.get("gcp_project") or "")
    kv("region", project.get("region") or "")
    kv("yaml_path", checkout.get("path") or "")
    kv("yaml_valid", "true" if checkout.get("valid") else "false")
elif what == "launch":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    kv("run_id", doc.get("run_id") or "")
    kv("run", doc.get("run") or "")
    kv("repo", doc.get("repo") or "")
    kv("status", doc.get("status") or "")
    kv("branch", doc.get("branch") or "")
    kv("execution", doc.get("execution") or "")
    kv("project", doc.get("project") or "")
    kv("error", doc.get("error") or "")
elif what == "runs":
    doc = load(sys.argv[2])
    want = sys.argv[3]
    found = None
    if isinstance(doc, dict):
        for row in doc.get("runs") or []:
            if isinstance(row, dict) and want in (row.get("run_id"), row.get("run")):
                found = row
                break
    if found is None:
        kv("found", "no")
        sys.exit(0)
    kv("found", "yes")
    for key in ("status", "terminal", "outcome", "branch", "pr", "pr_url", "title", "repo", "created"):
        kv(key, pick(found, key))
elif what == "policy":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    policy = doc.get("policy") if isinstance(doc.get("policy"), dict) else {}
    kv("version", policy.get("version") if policy.get("version") is not None else "")
    kv("error", doc.get("error") or "")
elif what == "prompts":
    bad = []
    for path in sys.argv[2:]:
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as fh:
                text = fh.read()
        except OSError:
            continue
        for line in text.splitlines():
            if any(needle in line.lower() for needle in NEEDLES) or line.endswith("? "):
                bad.append(line)
    kv("count", len(bad))
    for line in bad[:5]:
        print("prompt=%s" % line.strip()[:240])
elif what == "events":
    # The pairing stream, read for the NAMES of its events and for nothing else:
    # the payload of a `qr` event is the pairing's own one-time secret, and it
    # never enters a fact, a log line or this check's output. Both the
    # newline-delimited --json stream and a plain transcript are read, because a
    # refusal after the stream stops is prose on the same stream.
    names = []
    prose = []
    code = ""
    try:
        with open(sys.argv[2], "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except OSError:
        text = ""
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        event = None
        if line.startswith("{"):
            try:
                row = json.loads(line)
            except Exception:
                row = None
            if isinstance(row, dict):
                event = row.get("event")
                # A `refused` event is the command's own answer, and the code it
                # carries is what a row switches on. Its message is the one
                # sentence a person reads.
                if event == "refused" and row.get("code"):
                    code = str(row.get("code"))
                    prose.append("%s: %s" % (code, row.get("message") or row.get("reason") or ""))
        if event is not None:
            names.append(str(event))
        elif not line.startswith("{"):
            prose.append(line)
    kv("count", len(names))
    for name in names[:12]:
        print("event=%s" % name)
    kv("code", code)
    if prose:
        kv("prose", prose[-1][:300])
elif what == "record":
    # An issuance record (identitywire.Record): whose record it is, whether the
    # ticket it was written with has been claimed, the account the run's identity
    # is bound to, and whether the ticket's digest and the claim are there at all.
    # The digest is a fact of the record's shape and is reported as present or
    # absent: it is never printed, and neither is anything else of the record
    # besides these names.
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    kv("caller", doc.get("caller") or "")
    kv("expected_account", doc.get("expected_account") or "")
    kv("claimed", "true" if doc.get("claimed") else "false")
    kv("has_ticket_hash", "true" if doc.get("ticket_hash") else "false")
    kv("attempt", doc.get("attempt") or "")
elif what == "ledger":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    for key in ("reserved", "spent", "tokens", "exp", "released"):
        value = doc.get(key)
        kv(key, "" if value is None else value)
elif what == "wire":
    # An identity route's answer: the contract's own code and the service's one
    # sentence about it. The code is what a row is judged by and the sentence is
    # what a person reads.
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(0)
    kv("error", doc.get("error") or "")
    kv("message", (doc.get("message") or "")[:300])
    kv("ticket", "yes" if doc.get("ticket") else "no")
    kv("token", "yes" if doc.get("token") else "no")
elif what == "job":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    spec = doc.get("spec") if isinstance(doc.get("spec"), dict) else {}
    template = spec.get("template") if isinstance(spec.get("template"), dict) else {}
    tspec = template.get("spec") if isinstance(template.get("spec"), dict) else {}
    kv("account", tspec.get("serviceAccountName") or "")
    want = sys.argv[3] if len(sys.argv) > 3 else ""
    value = ""
    for container in tspec.get("containers") or []:
        for env in (container or {}).get("env") or []:
            if isinstance(env, dict) and env.get("name") == want:
                value = env.get("value") or ""
    kv("env", value)
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

read_json() { # read_json DEST QUERY ARGS...
  local dest=$1 query=$2
  shift 2
  python3 "$SCRATCH/read.py" "$query" "$@" >"$dest" 2>"$dest.err" || true
}

# body.py writes one identity ask's body: a JSON object of the key=value lines it
# reads on stdin, with the two typed fields the contract has. The file is written
# 0600 in a subshell's umask, because a ticket travels in one and is a handle no
# other process on the machine has any business reading.
write_body_py() {
  cat >"$SCRATCH/body.py" <<'PY'
import json
import sys

out = sys.argv[1]
body = {}
for line in sys.stdin:
    key, _, value = line.rstrip("\n").partition("=")
    if not key:
        continue
    if key == "max_runtime_seconds":
        body[key] = int(value)
    elif key == "pull_requests":
        body[key] = [int(x) for x in value.split(",") if x]
    else:
        body[key] = value
with open(out, "w", encoding="utf-8") as fh:
    json.dump(body, fh)
PY
  chmod 700 "$SCRATCH/body.py"
}

write_body() { # write_body OUTFILE key=value ...
  local out=$1
  shift
  # The pairs go to python3 on a pipe and never as its arguments: a ticket this
  # check claims travels in one of them, and `ps` shows a process's argv to every
  # user of the machine. printf is a builtin, so the pairs reach no process's
  # command line on their way in.
  (umask 077; printf '%s\n' "$@" | python3 "$SCRATCH/body.py" "$out")
}

# key_of escapes one path segment the way the tree does (internal/budget/model.go's
# Key): RTDB forbids . $ # [ ] / and ASCII control characters, each becomes %XX in
# uppercase hex, and % itself is escaped. guard_tree refuses a tree whose Key
# escapes another set, so this is a re-spelling of a read fact, not a remembered
# one.
key_of() { # key_of SEGMENT
  python3 -c 'import sys
out = []
for ch in sys.argv[1].encode():
    if ch in b"%.$#[]/" or ch < 0x20 or ch == 0x7f:
        out.append("%%%02X" % ch)
    else:
        out.append(chr(ch))
sys.stdout.write("".join(out))
' "$1"
}

# record_path and ledger_path: the two paths this check reads, built the way the
# tree builds them (identitywire.PathRecord and budget.PathRun).
record_path() { # record_path SLUG RUN
  printf '%s/%s/%s' "$RECORDS_PATH" "$(key_of "$1")" "$(key_of "$2")"
}

ledger_path() { # ledger_path SLUG RUN
  printf 'runs/%s/%s' "$(key_of "$1")" "$(key_of "$2")"
}

# --------------------------------------------------------------- the cloud
# curl_conf: a 0600 curl configuration file carrying one bearer header, so no
# identity token this check mints is ever an argument of a command (where `ps`,
# a trace or a shell history would hold it). Every call below that presents a
# token uses one; the file lives in the scratch directory and dies with it.
curl_conf() { # curl_conf FILE TOKEN
  (umask 077; printf 'header = "Authorization: Bearer %s"\n' "$1" >"$2")
}

# mint_identity IMPERSONATE OUTFILE: one Google ID token for the credentials
# service's own URL, as the operator's own account or — when an account is named
# — impersonating it. The audience is the service's address because that is what
# every class but a person's own presents (internal/credsvc/auth.go's
# checkCallerAudience: the job and roster-service-account classes admit this
# service's URL alone).
mint_identity() { # mint_identity IMPERSONATE OUTFILE
  local args=(auth print-identity-token --audiences="${SERVICE:?}" --include-email --quiet)
  if [ -n "$1" ]; then args+=(--impersonate-service-account="$1"); fi
  (umask 077; gcloud "${args[@]}" >"$2" 2>"$2.err")
}

# ask_route PATH BODYFILE OUTFILE IMPERSONATE prints the HTTP status of one POST
# of the identity contract, the answer in OUTFILE. Nothing of the answer is
# echoed here: the caller reads the status and the reader above reads the names.
ask_route() { # ask_route PATH BODYFILE OUTFILE IMPERSONATE
  local path=$1 body=$2 out=$3 who=$4 tok code conf="$SCRATCH/curl.conf"
  if ! mint_identity "$who" "$SCRATCH/idtoken"; then
    : >"$out"
    printf '000'
    return 0
  fi
  tok=$(cat "$SCRATCH/idtoken")
  curl_conf "$tok" "$conf"
  code=$(curl -sS -o "$out" -w '%{http_code}' --max-time 20 --config "$conf" \
    -H 'Content-Type: application/json' --data-binary "@$body" "${SERVICE%/}${path}" 2>"$SCRATCH/curl.err") || code=000
  rm -f "$SCRATCH/idtoken" "$conf"
  printf '%s' "${code:-000}"
}

# rtdb_get PATH OUTFILE [IMPERSONATE] prints the HTTP status of one read of the
# installation's Realtime Database and writes the body to OUTFILE. The
# operator's own access token is what every read here uses — the owner's, whose
# project it is — and an account named explicitly is impersonated for it.
rtdb_get() { # rtdb_get PATH OUTFILE [IMPERSONATE]
  local tok code conf="$SCRATCH/rtdb.conf"
  if [ -n "${3:-}" ]; then
    tok=$(gcloud auth print-access-token --impersonate-service-account="$3" 2>/dev/null || true)
  else
    tok=$(gcloud auth print-access-token 2>/dev/null || true)
  fi
  if [ -z "$tok" ]; then
    : >"$2"
    printf '000'
    return 0
  fi
  curl_conf "$tok" "$conf"
  code=$(curl -sS -o "$2" -w '%{http_code}' --max-time 20 --config "$conf" \
    -H 'Accept: application/json' "${RTDB%/}/${1}.json" 2>/dev/null) || code=000
  rm -f "$conf"
  printf '%s' "${code:-000}"
}

# rtdb_delete PATH prints the HTTP status of one DELETE of the installation's
# Realtime Database. It is used once, at the end of the `copy` row, for the
# record that row made for a run id of its own: nothing else this check reads is
# anything it may remove.
rtdb_delete() { # rtdb_delete PATH
  local tok code conf="$SCRATCH/rtdb.conf"
  tok=$(gcloud auth print-access-token 2>/dev/null || true)
  if [ -z "$tok" ]; then
    printf '000'
    return 0
  fi
  curl_conf "$tok" "$conf"
  code=$(curl -sS -o "$SCRATCH/deleted.json" -w '%{http_code}' --max-time 20 --config "$conf" \
    -X DELETE "${RTDB%/}/${1}.json" 2>/dev/null) || code=000
  rm -f "$conf"
  printf '%s' "${code:-000}"
}

# job_read refreshes $SCRATCH/job.txt with one Cloud Run job's own account and the
# value of one variable of its environment. A job that cannot be read leaves the
# file empty, which is an answer the callers report rather than a value.
job_read() { # job_read NAME [ENVNAME]
  local rc=0
  gcloud run jobs describe "$1" --project="$P" --region="$L" --format=json \
    >"$SCRATCH/job.json" 2>"$SCRATCH/job.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    : >"$SCRATCH/job.txt"
    return 0
  fi
  read_json "$SCRATCH/job.txt" job "$SCRATCH/job.json" "${2:-}"
}

# doctor_read refreshes $SCRATCH/doctor.txt in the checkout and leaves the exit
# status in DOCTOR_STATUS.
DOCTOR_STATUS=0
doctor_read() {
  DOCTOR_STATUS=0
  (cd "$REPO" && fugaro doctor --json) >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || DOCTOR_STATUS=$?
  read_json "$SCRATCH/doctor.txt" doctor "$SCRATCH/doctor.json"
}

# runs_read refreshes $SCRATCH/runs.txt for one run id and leaves the read's exit
# status in RUNS_STATUS.
RUNS_STATUS=0
runs_read() { # runs_read <run-id>
  RUNS_STATUS=0
  (cd "$REPO" && fugaro runs ls --json --limit 200) >"$SCRATCH/runs.json" 2>"$SCRATCH/runs.err" || RUNS_STATUS=$?
  if [ "$RUNS_STATUS" -eq 0 ]; then
    read_json "$SCRATCH/runs.txt" runs "$SCRATCH/runs.json" "$1"
  else
    : >"$SCRATCH/runs.txt"
  fi
}

# policy_read refreshes $SCRATCH/policy.txt with the version the published node
# states, through the CLI's own reader, and leaves its exit status in
# POLICY_STATUS.
POLICY_STATUS=0
policy_read() {
  POLICY_STATUS=0
  (cd "$REPO" && fugaro project policy show --json) >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.err" || POLICY_STATUS=$?
  read_json "$SCRATCH/policy.txt" policy "$SCRATCH/policy.json"
}

# run_closed_stdin SECONDS KEY CMD...: run one command with its stdin on
# /dev/null and its two streams in the scratch directory, from the checkout, and
# end it at the deadline. The child execs, so the pid the deadline kills IS the
# command: a command waiting on a terminal would otherwise outlive the check that
# gave up on it, which is the one thing the deadline exists to prevent.
RUN_STATUS=0
RUN_TIMED_OUT=no
RUN_SECONDS=0
run_closed_stdin() { # run_closed_stdin SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  RUN_TIMED_OUT=no
  local started=$SECONDS
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  ( cd "$REPO" || exit 1; exec "$@" ) </dev/null >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      RUN_TIMED_OUT=yes
      say "the command is still going after ${secs}s: killing it (TERM, then KILL). A command waiting on an answer looks exactly like this"
      kill -TERM "$pid" 2>/dev/null || true
      sleep 2
      kill -KILL "$pid" 2>/dev/null || true
      break
    fi
    sleep 1
    waited=$((waited + 1))
    if [ $((waited % 60)) -eq 0 ]; then
      say "still running after ${waited}s of ${secs}s ..."
    fi
  done
  RUN_STATUS=0
  wait "$pid" 2>/dev/null || RUN_STATUS=$?
  RUN_SECONDS=$((SECONDS - started))
}

# ------------------------------------------------------------- the check's rows
# step_installation reads the installation this check is about and refuses to
# measure anything until it is the one this script names.
step_installation() {
  say "reading the installation's own configuration (${CFG})"
  if [ ! -f "$CFG" ]; then
    fact installation FAIL "no installation configuration is at ${CFG}, so nothing says which project and region this installation is, nor which credentials service and database it holds (fugaro writes that file at \`fugaro setup\` and \`fugaro update\`; point CHK841_CONFIG at it)"
    stop "Check 841 needs the installation's own configuration at ${CFG}: run it on the ${P} installation, or point CHK841_CONFIG at that installation's file"
  fi
  read_json "$SCRATCH/cfg.txt" cfg "$CFG"
  local gcp region rtdb service
  gcp=$(field "$SCRATCH/cfg.txt" cfg_gcp_project)
  region=$(field "$SCRATCH/cfg.txt" cfg_region)
  rtdb=$(field "$SCRATCH/cfg.txt" cfg_rtdb_url)
  [ -n "$rtdb" ] || rtdb=$(field "$SCRATCH/cfg.txt" cfg_budget_rtdb_url)
  service=$(field "$SCRATCH/cfg.txt" cfg_credentials_service_url)
  if [ "$gcp" != "$P" ] || [ "$region" != "$L" ]; then
    fact installation FAIL "the installation at ${CFG} is project '${gcp:-<unset>}' in region '${region:-<unset>}', and every cloud call of this check names ${P}/${L} itself: this is not the installation Check 841 is about, and no reading below could be made against it"
    stop "Check 841 runs on the ${P} installation (region ${L}) and on no other. Run it there, or point CHK841_CONFIG at that installation's own configuration file"
  fi
  if [ -z "$rtdb" ]; then
    fact installation FAIL "the installation at ${CFG} states no Realtime Database URL, so the issuance record and the ledger of a run cannot be read: its identity's own record is the reading the \`session\` and \`copy\` rows exist for"
    stop "Check 841 reads the installation's Realtime Database (its configuration's rtdb_url, written by \`fugaro setup\`/\`fugaro update\`). Set it in ${CFG}, or run the check on an installation that has one"
  fi
  if [ -z "$service" ]; then
    fact installation FAIL "the installation at ${CFG} holds no credentials_service block, so it serves no identity route at all: every run's identity is signed by its launcher, and there is nothing for this check's rows to be about"
    stop "Check 841 is about an installation whose runs get their identity from the credentials service (credentials_service.url in ${CFG}, which \`fugaro update\` writes). This installation has none: run \`fugaro update\` on it first, then run this check"
  fi
  RTDB=$rtdb
  SERVICE=$service
  if [ ! -d "$REPO" ]; then
    fact installation FAIL "--repo ${REPO} is not a directory: this check runs the update and launches the runs from a checkout"
    stop "--repo ${REPO} is not a directory"
  fi
  if [ ! -f "$REPO/fugaro.yaml" ]; then
    fact installation FAIL "${REPO} holds no fugaro.yaml, so it is not a Fugaro checkout: the runs this check launches would be refused by it"
    stop "${REPO} holds no fugaro.yaml: run this check from the checkout of the installation you are converging (--repo names another)"
  fi
  doctor_read
  local name dgcp dregion valid
  name=$(field "$SCRATCH/doctor.txt" name)
  dgcp=$(field "$SCRATCH/doctor.txt" gcp)
  dregion=$(field "$SCRATCH/doctor.txt" region)
  valid=$(field "$SCRATCH/doctor.txt" yaml_valid)
  if [ -z "$name" ]; then
    fact installation FAIL "'fugaro doctor --json' in ${REPO} named no installation (exit ${DOCTOR_STATUS}): $(last_line "$SCRATCH/doctor.err"). This check converges the installation the checkout belongs to, and one that cannot be named is not one to converge"
    stop "the checkout's own doctor named no installation"
  fi
  if [ "$valid" != "true" ]; then
    fact installation FAIL "${REPO}/fugaro.yaml is not valid (fugaro doctor --json, $(field "$SCRATCH/doctor.txt" yaml_path)): the runs this check launches would be refused by it"
    stop "the checkout's fugaro.yaml is not valid"
  fi
  if [ "$dgcp" != "$P" ] || [ "$dregion" != "$L" ]; then
    fact installation FAIL "the checkout in ${REPO} belongs to the installation '${name}' in ${dgcp:-<unread>}/${dregion:-<unread>}, and the configuration at ${CFG} says ${P}/${L}: two different installations, and this check will not pick one"
    stop "the checkout's installation is not the one the configuration at ${CFG} describes"
  fi
  NAME=$name
  CLI_VERSION=$(one_line "$(fugaro version 2>/dev/null | head -n 1 || true)")
  fact installation PASS "the installation ${NAME} is ${P}/${L}, its configuration at ${CFG} states its Realtime Database ${RTDB} and its credentials service ${SERVICE}, and the checkout ${REPO} agrees about the project and the region with a valid fugaro.yaml. \`fugaro version\` says: ${CLI_VERSION:-<unread>}"
}

# step_premise reads the state this check is about: an installation at the
# PREVIOUS release, and a CLI of THIS one on PATH. Either the other way round
# makes every row below a reading of the wrong thing, so both are stops.
step_premise() {
  say "reading the published policy node (premise)"
  policy_read
  local version
  version=$(field "$SCRATCH/policy.txt" version)
  if [ -z "$version" ]; then
    fact premise FAIL "'fugaro project policy show --json' (exit ${POLICY_STATUS}) named no published policy version: $(last_line "$SCRATCH/policy.err"). Whether this installation is at the previous release — the whole premise of this check — is NOT measured"
    stop "the published policy node could not be read"
  fi
  if [ "$version" = "$POLICY_VERSION" ]; then
    fact premise FAIL "the installation ${NAME} publishes policy version ${version}, which is THIS release's version (${THIS_RELEASE}): the update this check measures has already been run here, so there is no move left to observe — and the runs below would be runs of an installation that already issues their identity"
    stop "the installation is not at the previous release: it publishes this release's policy version already. This check is run ONCE per installation, before \`fugaro update\`"
  fi
  if [ "$version" != "$PREV_VERSION" ]; then
    fact premise FAIL "the installation ${NAME} publishes policy version ${version}, and this check measures the move from version ${PREV_VERSION} to ${POLICY_VERSION} (${THIS_RELEASE}): an installation at another release is measured by the check of ITS release, not by this one"
    stop "the installation publishes policy version ${version}, not ${PREV_VERSION}: run the update it needs first, at the release that needs it"
  fi
  if [ -z "$CLI_VERSION" ]; then
    fact premise FAIL "\`fugaro version\` answered nothing, so the release this check runs against the installation is unknown: a CLI of the release behind would be the one thing this check may not run the update with"
    stop "\`fugaro version\` answered nothing"
  fi
  if [ "$CLI_VERSION" != "dev" ] && ! version_at_least "$CLI_VERSION" "$THIS_RELEASE"; then
    fact premise FAIL "the \`fugaro\` on PATH is ${CLI_VERSION} and this check is about ${THIS_RELEASE}: the release behind does not know the identity routes at all and would publish the node back to version ${PREV_VERSION}, so nothing below would be about the move this check measures"
    stop "the fugaro on PATH is ${CLI_VERSION}, and this check needs ${THIS_RELEASE} or newer: upgrade first (brew upgrade dimipaun/tap/fugaro), then run this check"
  fi
  fact premise PASS "the installation ${NAME} publishes policy version ${version} — the previous release's, one below ${POLICY_VERSION} — and the \`fugaro\` on PATH is ${CLI_VERSION}, the release ${THIS_RELEASE} or newer that reads it: what the update below raises is exactly the node this check exists for"
}

# launch_run OUTFILE: one throwaway run of the checkout, capped by --max-usd when
# one is set, its answer in OUTFILE.
launch_run() { # launch_run OUTFILE
  local args=(run --repo "$REPO" --title "$TITLE" --task-file "$SCRATCH/task.md" --json)
  if [ -n "$MAX_USD" ]; then args+=(--max-usd "$MAX_USD"); fi
  (cd "$REPO" && fugaro "${args[@]}") >"$1" 2>"$SCRATCH/launch.err"
}

# launch_read OUTFILE PREFIX: the launch's own answer into the shell's variables,
# with the slug and the run's job held to the shapes the rows below need.
# LAUNCH_* is what the caller reads.
LAUNCH_RUN_ID=""
LAUNCH_SLUG=""
LAUNCH_JOB=""
LAUNCH_BRANCH=""
launch_read() { # launch_read JSONFILE
  read_json "$SCRATCH/launch.txt" launch "$1"
  LAUNCH_RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_BRANCH=$(field "$SCRATCH/launch.txt" branch)
  # The run's own name is "<slug>/<run id>", which is the value a job carries in
  # FUGARO_RUN and the two names the record's and the ledger's paths are built
  # from. A slug is one path segment: a value with a slash in it would make the
  # paths this check reads somebody else's.
  local run
  run=$(field "$SCRATCH/launch.txt" run)
  LAUNCH_SLUG=""
  case "$run" in
  */*) LAUNCH_SLUG=${run%%/*} ;;
  esac
  case "$LAUNCH_SLUG" in
  */*) LAUNCH_SLUG="" ;;
  esac
  # The job's own name, out of the execution the launch answered with: the
  # account it runs as is the account a ticket names and a claim is made by.
  local execution
  execution=$(field "$SCRATCH/launch.txt" execution)
  LAUNCH_JOB=""
  case "$execution" in
  */jobs/*)
    LAUNCH_JOB=${execution#*/jobs/}
    LAUNCH_JOB=${LAUNCH_JOB%%/*}
    ;;
  esac
}

# step_run launches ONE throwaway run of the checkout before the update and waits
# until its record reads running, which is what makes the update below an update
# with a run in flight.
RUN_ID=""
SLUG=""
RUN_ACCOUNT=""
RUN_SEEN_LIVE=no
step_run() {
  if [ "$NO_RUN" = yes ]; then
    fact run UNVERIFIED "no run was launched (--no-run), so nothing was in flight across the update below: the run-launched-before-the-update half of Check 841 is NOT measured, and the inflight, session, second and copy rows say the same"
    return 0
  fi
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: Check 841: a run in flight across the update

A live check launched this run to watch `fugaro update` move this installation
from the release behind to the release that issues a run's identity, while a run
is in flight. The check writes no file and opens no pull request on your behalf:
report only.

Read `docs/launcher-does-not-mint/spec.md` and report, in your final message, the
six things it asks for, each in one line. Change nothing, commit nothing, open no
pull request.
TASK
  say "launching ONE run of ${REPO} before the update (billable): ${TITLE}"
  local rc=0
  launch_run "$SCRATCH/launch.json" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact run FAIL "'fugaro run --repo ${REPO} --title ... --task-file ... --json' exited ${rc}, so no run was launched and nothing is in flight across the update: $(last_line "$SCRATCH/launch.err"). The run is the half of Check 841 that measures the update in flight"
    return 0
  fi
  launch_read "$SCRATCH/launch.json"
  RUN_ID=$LAUNCH_RUN_ID
  SLUG=$LAUNCH_SLUG
  if [ -z "$RUN_ID" ]; then
    fact run FAIL "the launch printed no run id (fugaro run --json): $(one_line "$(cat "$SCRATCH/launch.json" 2>/dev/null)"). Nothing below can follow a run that was not named"
    return 0
  fi
  if [ -z "$SLUG" ]; then
    fact run FAIL "the launch answered the run's own name as '$(field "$SCRATCH/launch.txt" run)', which is not the '<slug>/<run id>' this check builds the record's and the ledger's paths from: the reads below cannot be made"
    return 0
  fi
  # The job's account, read from the job the launch named. A job that cannot be
  # read leaves the account empty, and the `copy` row is then UNVERIFIED rather
  # than made as an account that may not be the run's.
  if [ -n "$LAUNCH_JOB" ]; then
    job_read "$LAUNCH_JOB"
    RUN_ACCOUNT=$(field "$SCRATCH/job.txt" account)
  fi
  say "launched ${RUN_ID} in ${SLUG}; waiting for its record to read running"
  local waited=0 status
  while [ "$waited" -lt "$START_SECONDS" ]; do
    runs_read "$RUN_ID"
    if [ "$RUNS_STATUS" -ne 0 ]; then
      fact run FAIL "the launched run ${RUN_ID} could not be read back: 'fugaro runs ls --json' exited ${RUNS_STATUS}: $(last_line "$SCRATCH/runs.err"). The record of the run this check launched is what the inflight row reads"
      return 0
    fi
    status=$(field "$SCRATCH/runs.txt" status)
    if [ "$status" = running ]; then
      RUN_SEEN_LIVE=yes
      break
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  local where="job ${LAUNCH_JOB:-<unnamed>}, account ${RUN_ACCOUNT:-<unread>}, branch ${LAUNCH_BRANCH:-<unread>}"
  if [ "$RUN_SEEN_LIVE" = yes ]; then
    fact run PASS "one throwaway run of ${REPO} was launched before the update (${RUN_ID} in ${SLUG}, ${where}) and its record read running after ${waited}s: the update below is an update with this run in flight, and the inflight row follows it to its end"
    return 0
  fi
  if [ "$(field "$SCRATCH/runs.txt" found)" = yes ]; then
    fact run UNVERIFIED "the launched run ${RUN_ID} (${where}) was readable after ${waited}s but never read running: its status is '$(one_line "$(field "$SCRATCH/runs.txt" status)")'. A run that reached a terminal status before the update started means nothing was in flight across it"
    return 0
  fi
  fact run UNVERIFIED "the launched run ${RUN_ID} (${where}) was not listed by 'fugaro runs ls --json' after ${waited}s (limit 200): the launch printed a run id, and its row is not there to read"
}

# step_update runs the real converge, with its stdin closed and its output read
# back. This row is the update's own statement about the runs in flight, and the
# node it was run to raise.
UPDATE_STATUS=0
UPDATE_TIMED_OUT=no
UPDATE_SECONDS=0
UPDATE_NOTE=""
UPDATE_COUNT=""
UPDATE_NODE=""
step_update() {
  say "running: fugaro update < /dev/null  (deadline ${DEADLINE}s)"
  run_closed_stdin "$DEADLINE" update fugaro update
  UPDATE_STATUS=$RUN_STATUS
  UPDATE_TIMED_OUT=$RUN_TIMED_OUT
  UPDATE_SECONDS=$RUN_SECONDS
  local out="$SCRATCH/update.out" err="$SCRATCH/update.err"
  local last from=stderr
  last=$(last_line "$err")
  if [ -z "$last" ]; then
    last=$(last_line "$out")
    from=stdout
  fi
  # The update's own line about the runs in flight (internal/cli/update_in_flight.go):
  # the count, that it waits for none of them, and that it changes nothing they
  # hold. It is the whole of the sentence this row measures, and the count in it
  # is read out of the number the update printed.
  UPDATE_NOTE=$(grep -h -o -E '[0-9]+ runs? in flight: update waits for none and changes nothing they hold' "$out" "$err" 2>/dev/null | tail -1 || true)
  UPDATE_COUNT=""
  if [ -n "$UPDATE_NOTE" ]; then
    UPDATE_COUNT=$(printf '%s' "$UPDATE_NOTE" | grep -o -E '^[0-9]+' | head -1)
  fi
  if [ "$UPDATE_TIMED_OUT" = yes ]; then
    fact update UNVERIFIED "'fugaro update' was still running after ${DEADLINE}s with its stdin on /dev/null and this check killed it (TERM, then KILL): a converge that needs longer and a command waiting on an answer look alike, and neither is an answer about the converge. Its last line (${from}): ${last:-<nothing>}. Raise --deadline and run the check again; the transcript is in ${out} and ${err} (CHK841_KEEP_SCRATCH=1 keeps the directory)"
    return 0
  fi
  local left
  left=$(left_for_you "$out" "$err" || true)
  case "$UPDATE_STATUS" in
  0)
    policy_read
    UPDATE_NODE=$(field "$SCRATCH/policy.txt" version)
    if [ -z "$UPDATE_NOTE" ]; then
      fact update FAIL "'fugaro update' ended after ${UPDATE_SECONDS}s with exit 0 and no line about the runs in flight, although this check launched one (${RUN_ID:-<none>}) before it started: an update that says nothing about the runs it ran past is the one thing docs/design/update-in-flight.md exists to forbid. Its last line (${from}): ${last:-<nothing>}"
      return 0
    fi
    if [ "${UPDATE_COUNT:-0}" -lt 1 ]; then
      fact update FAIL "'fugaro update' ended after ${UPDATE_SECONDS}s and its line about the runs in flight counts none ('${UPDATE_NOTE}'), although this check launched ${RUN_ID:-a run} before it started: a count that misses a live run is a wrong count, and the update's statement about what it left alone is worth nothing without it"
      return 0
    fi
    if [ "$UPDATE_NODE" != "$POLICY_VERSION" ]; then
      fact update FAIL "'fugaro update' ended after ${UPDATE_SECONDS}s with exit 0 and its line named ${UPDATE_COUNT} run(s) in flight, but the published node reads version '${UPDATE_NODE:-<unread>}' and not ${POLICY_VERSION}: the update this check is about is the one that raises the node and issues a run's identity, and this one did not. Its last line (${from}): ${last:-<nothing>}"
      return 0
    fi
    fact update PASS "'fugaro update' ended after ${UPDATE_SECONDS}s with exit 0, its own line named the runs in flight — ${UPDATE_NOTE} — and the published node now reads version ${UPDATE_NODE}: the installation moved to ${THIS_RELEASE} while ${UPDATE_COUNT} run(s) were in flight, and it asked none of them to finish first"
    ;;
  1)
    if [ -n "$left" ]; then
      fact update UNVERIFIED "'fugaro update' ended after ${UPDATE_SECONDS}s with exit 1 and its own sentence for a hand-off — ${left} — which is the CLI's answer for a step only you can run and not a failure: the converge did what it could without a terminal. Its last line (${from}): ${last:-<nothing>}. Run the step(s) at your own terminal and run the check again"
      return 0
    fi
    fact update FAIL "'fugaro update' ended after ${UPDATE_SECONDS}s with exit 1 and no 'step(s) left for you' sentence, so this is a refusal and the installation was NOT converged: its last line (${from}): ${last:-<nothing>}. The whole transcript is in ${out} and ${err} (CHK841_KEEP_SCRATCH=1 keeps the directory); every row below reads an installation this update did not move"
    ;;
  2)
    fact update FAIL "'fugaro update' ended after ${UPDATE_SECONDS}s with exit 2 — a cloud failure, so the converge did not finish: its last line (${from}): ${last:-<nothing>}. Fix what the line names and run the check again"
    ;;
  *)
    fact update FAIL "'fugaro update' ended after ${UPDATE_SECONDS}s with exit ${UPDATE_STATUS}, which is not one of update's own codes (0 done, 1 a refusal or a step left for you, 2 a cloud failure): its last line (${from}): ${last:-<nothing>}"
    ;;
  esac
}

# step_asked reads every line the update printed for the shapes a question leaves
# behind (D3090, the no-prompt policy). Check 821 reads the same transcript for
# the same reason.
step_asked() {
  local out="$SCRATCH/update.out" err="$SCRATCH/update.err"
  if [ ! -s "$out" ] && [ ! -s "$err" ]; then
    fact asked UNVERIFIED "the update printed nothing at all (exit ${UPDATE_STATUS} after ${UPDATE_SECONDS}s), so there is no transcript to read for a question: an empty transcript is neither evidence that it asked nothing nor evidence of a converge"
    return 0
  fi
  read_json "$SCRATCH/prompts.txt" prompts "$out" "$err"
  local count listed
  count=$(field "$SCRATCH/prompts.txt" count)
  listed=$(awk -F'=' '$1 == "prompt" { printf "%s | ", substr($0, 8) }' "$SCRATCH/prompts.txt" 2>/dev/null || true)
  if [ "${count:-0}" != 0 ]; then
    fact asked FAIL "${count} line(s) of the update's own output ask for an answer: ${listed}A command that puts a question to a run with no terminal is the one prompt the policy removes"
    return 0
  fi
  fact asked PASS "none of the update's output asks for an answer: no [y/N] in any casing, no (y/n) or (yes/no), no 'to confirm:' left open, no enter-prompt and no question left waiting on a blank. Its streams: stdin was /dev/null (a read meets EOF at once and never a person's answer), stdout and stderr were files in the scratch directory, and no pty was attached to either"
}

# step_inflight follows the run launched before the update to its terminal status
# and reads its record back (docs/design/update-in-flight.md: the update asks no
# run to finish first and holds nothing a run writes).
step_inflight() {
  if [ -z "$RUN_ID" ]; then
    fact inflight UNVERIFIED "no run was launched by this check (--no-run, or a launch that failed), so nothing was in flight across the update: the criterion 'a run launched before the update finishes' is NOT measured"
    return 0
  fi
  say "following the run launched before the update (${RUN_ID})"
  local waited=0 status found
  while :; do
    runs_read "$RUN_ID"
    if [ "$RUNS_STATUS" -ne 0 ]; then
      fact inflight FAIL "'fugaro runs ls --json' exited ${RUNS_STATUS} while following ${RUN_ID}: $(last_line "$SCRATCH/runs.err"). The record of the run this check launched cannot be read at all, which is the one thing this row exists to catch"
      return 0
    fi
    found=$(field "$SCRATCH/runs.txt" found)
    status=$(field "$SCRATCH/runs.txt" status)
    if [ "$found" != yes ] || [ "$status" != running ]; then
      break
    fi
    if [ "$waited" -ge "$WAIT_SECONDS" ]; then
      fact inflight UNVERIFIED "the run launched before the update (${RUN_ID}) is still running after ${WAIT_SECONDS}s, and this check stops waiting here: its record is whole and it was in flight across the update, but nothing here says it FINISHES — raise --wait and run the check again to read its terminal status"
      return 0
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
    if [ $((waited % 300)) -eq 0 ]; then say "still following ${RUN_ID} after ${waited}s of ${WAIT_SECONDS}s ..."; fi
  done
  if [ "$found" != yes ]; then
    fact inflight FAIL "the run launched before the update (${RUN_ID}) is NOT in 'fugaro runs ls --json' (limit 200) after the converge: a run's record is in the runs bucket, and one that is gone after an update is the finding this row exists for. Look for it yourself: fugaro runs show ${RUN_ID}"
    return 0
  fi
  local outcome pr prurl branch
  outcome=$(field "$SCRATCH/runs.txt" outcome)
  pr=$(field "$SCRATCH/runs.txt" pr)
  prurl=$(field "$SCRATCH/runs.txt" pr_url)
  branch=$(field "$SCRATCH/runs.txt" branch)
  if [ "$RUN_SEEN_LIVE" != yes ]; then
    fact inflight UNVERIFIED "the run launched before the update (${RUN_ID}) reached '${status}' and its record is whole (branch ${branch:-<none>}, pull request ${pr:-<none>}${prurl:+ at ${prurl}}, outcome ${outcome:-<none>}), but it was never seen running: it had already finished when the update started, so nothing was in flight across it"
    return 0
  fi
  case "$status" in
  succeeded)
    fact inflight PASS "the run launched before the update (${RUN_ID}) was in flight across it and finished: status succeeded, branch ${branch:-<none>}, pull request ${pr:-<none>}${prurl:+ at ${prurl}}, outcome ${outcome:-<none>}. Its record is whole after the converge"
    ;;
  failed | infra_error | cancelled | halted | unreviewed)
    fact inflight UNVERIFIED "the run launched before the update (${RUN_ID}) was in flight across it and ended '${status}' (branch ${branch:-<none>}, pull request ${pr:-<none>}, outcome ${outcome:-<none>}): its record is whole and it was not disturbed out of existence, but a run that did not finish succeeded is not the reading this row wants — read its own history (fugaro runs show ${RUN_ID}) before treating it as an answer about the update"
    ;;
  *)
    fact inflight UNVERIFIED "the run launched before the update (${RUN_ID}) read back as '${status}', which is not a status this check knows how to read: its record is in ${SCRATCH}/runs.json (CHK841_KEEP_SCRATCH=1 keeps the directory)"
    ;;
  esac
}

# step_session launches ONE more throwaway run of the checkout after the update
# and reads what the credentials service holds about it: the issuance record the
# launcher's ask wrote — whose claim is the run's own proof that it was handed a
# ticket — and the ledger node that is the session the run opened.
RUN2_ID=""
RUN2_SEEN=""
step_session() {
  if [ "$NO_RUN" = yes ]; then
    fact session UNVERIFIED "no run was launched (--no-run), so no launch after the update was made: 'a new launch gets a session' is NOT measured, and nothing here says an installation that no longer mints a run's identity can still start one"
    return 0
  fi
  say "launching ONE run of ${REPO} after the update (billable): ${TITLE}"
  local rc=0
  launch_run "$SCRATCH/launch2.json" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact session FAIL "'fugaro run --repo ${REPO} --title ... --task-file ... --json' exited ${rc} after the update, so no run was launched: $(last_line "$SCRATCH/launch.err"). A launch that cannot start on the release that takes the grant away is the finding this row exists for — read the line it printed"
    return 0
  fi
  launch_read "$SCRATCH/launch2.json"
  RUN2_ID=$LAUNCH_RUN_ID
  local slug2=$LAUNCH_SLUG
  if [ -z "$RUN2_ID" ] || [ -z "$slug2" ]; then
    fact session FAIL "the launch answered run id '${RUN2_ID:-<none>}' and run name '$(field "$SCRATCH/launch.txt" run)' (fugaro run --json), which is not the run id and the '<slug>/<run id>' this row reads the issuance record from"
    return 0
  fi
  if [ -z "$RUN_ACCOUNT" ] && [ -n "$LAUNCH_JOB" ]; then
    job_read "$LAUNCH_JOB"
    RUN_ACCOUNT=$(field "$SCRATCH/job.txt" account)
  fi
  if [ "$slug2" != "$SLUG" ]; then
    fact session FAIL "the run launched after the update is in the repository '${slug2}' and the one launched before it is in '${SLUG:-<none>}': this check launches the same checkout twice, and a launch that answered another repository's slug means the reads below are not about the installation this check named"
    return 0
  fi
  say "waiting for ${RUN2_ID} to claim its ticket (${RECORDS_PATH})"
  local rec="$(record_path "$slug2" "$RUN2_ID")" waited=0 code claimed=""
  while :; do
    code=$(rtdb_get "$rec" "$SCRATCH/record.json")
    claimed=""
    if [ "$code" = 200 ]; then
      read_json "$SCRATCH/record.txt" record "$SCRATCH/record.json"
      claimed=$(field "$SCRATCH/record.txt" claimed)
    fi
    if [ "$code" = 200 ] && [ "$claimed" = true ]; then
      RUN2_SEEN=claimed
      break
    fi
    if [ "$waited" -ge "$CLAIM_SECONDS" ]; then
      RUN2_SEEN="timeout:${code}"
      break
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
    if [ $((waited % 300)) -eq 0 ]; then say "still waiting for ${RUN2_ID} to claim after ${waited}s of ${CLAIM_SECONDS}s ..."; fi
  done
  if [ "$RUN2_SEEN" != claimed ]; then
    local lastcode="$code"
    if [ "$lastcode" = 404 ] || [ "$lastcode" = 200 ]; then
      fact session FAIL "the run launched after the update (${RUN2_ID}) has no issuance record at ${rec} (HTTP ${lastcode} after ${waited}s): the launcher of this run asked the credentials service for no ticket, which means it signed the run's identity ITSELF — the bridge of R7, which after this release's move is a grant that is still in place somewhere, or a service that does not answer the ticket route"
      return 0
    fi
    fact session UNVERIFIED "the issuance record of the run launched after the update (${RUN2_ID}) could not be read at ${rec} (HTTP ${lastcode}, exit of the read): the Realtime Database at ${RTDB} answers the owner's own token for every other read this check makes, so this is a read that failed and not a verdict. Run the check again, and read the node yourself: curl -H \"Authorization: Bearer \$(gcloud auth print-access-token)\" ${RTDB%/}/${rec}.json"
    return 0
  fi
  read_json "$SCRATCH/record.txt" record "$SCRATCH/record.json"
  local caller account
  caller=$(field "$SCRATCH/record.txt" caller)
  account=$(field "$SCRATCH/record.txt" expected_account)
  local lcode led
  lcode=$(rtdb_get "$(ledger_path "$slug2" "$RUN2_ID")" "$SCRATCH/ledger.json")
  led=""
  if [ "$lcode" = 200 ]; then
    read_json "$SCRATCH/ledger.txt" ledger "$SCRATCH/ledger.json"
    led="reserved=$(field "$SCRATCH/ledger.txt" reserved), exp=$(field "$SCRATCH/ledger.txt" exp), tokens=$(field "$SCRATCH/ledger.txt" tokens)"
  fi
  if [ "$lcode" != 200 ]; then
    fact session UNVERIFIED "the run launched after the update (${RUN2_ID}) claimed its ticket — the issuance record at ${rec} reads claimed, its caller ${caller:-<none>} and its account ${account:-<none>} — but its ledger node could not be read (HTTP ${lcode} at $(ledger_path "$slug2" "$RUN2_ID")): half of what this row measures (the session the run opened) is NOT read"
    return 0
  fi
  fact session PASS "the run launched after the update (${RUN2_ID} in ${slug2}) got its identity from the credentials service: the record at ${rec} was written by this launcher's ask (caller ${caller:-<none>}, account ${account:-<none>}) and reads claimed, so the run presented the ticket it was handed, and its ledger node is there with its session open (${led}). No launcher signed this run's identity itself"
}

# step_second makes the one attack this check is about: an account on the
# installation's roster, impersonated, asks the ticket route for the run the
# FIRST launcher started. The answer must be the service's own refusal of another
# caller's record.
SECOND_STATUS=""
step_second() {
  if [ -z "$RUN2_ID" ]; then
    fact second UNVERIFIED "no run was launched after the update (--no-run, or a launch that failed), so there is no run for a second account to ask about: the criterion 'a second account's ask for that run is refused' is NOT measured"
    return 0
  fi
  say "asking the ticket route as the second launcher (${SECOND})"
  write_body "$SCRATCH/second.body" "slug=${SLUG}" "run=${RUN2_ID}" "account=${RUN_ACCOUNT}" "launcher=${SECOND}" "max_runtime_seconds=${COPY_RUNTIME_SECONDS}"
  SECOND_STATUS=$(ask_route "$TICKET_PATH" "$SCRATCH/second.body" "$SCRATCH/second.json" "$SECOND")
  read_json "$SCRATCH/second.txt" wire "$SCRATCH/second.json"
  local code msg
  code=$(field "$SCRATCH/second.txt" error)
  msg=$(field "$SCRATCH/second.txt" message)
  case "$SECOND_STATUS" in
  2*)
    fact second FAIL "the second account ${SECOND} was SERVED a ticket for the run ${RUN2_ID} of ${SLUG}, which the FIRST launcher started (HTTP ${SECOND_STATUS}, the answer carries a ticket: $(field "$SCRATCH/second.txt" ticket)): a run's ticket is re-issued to the caller on the record alone (R5), and one another account can obtain is an identity another account can wear"
    ;;
  0*)
    fact second UNVERIFIED "the ticket route could not be reached as ${SECOND} (HTTP ${SECOND_STATUS}): nothing was asked, so nothing about a second account's ask is measured — check that ${SECOND} is an account this machine may impersonate (gcloud auth print-identity-token --impersonate-service-account=${SECOND}) and that ${SERVICE} answers"
    ;;
  *)
    case "$code" in
    "$REFUSED_CODE")
      fact second PASS "the second account ${SECOND} asked the ticket route for the run ${RUN2_ID} of ${SLUG} — the run another launcher started — and the service refused it with its own code ${code}: ${msg:-<no sentence>}. A run's ticket is re-issued to the caller on its record alone, and this caller is not on it"
      ;;
    "$NOT_ADMITTED_CODE")
      fact second UNVERIFIED "the second account ${SECOND} was refused with ${code}, which is the service's answer about the ACCOUNT and not about the record: ${msg:-<no sentence>}. The reading this row wants is a refusal of another caller's run, and an account the roster does not admit cannot test that — name an account the installation's roster holds (launcher_accounts), or a roster service account"
      ;;
    "$NOT_SERVED_CODE")
      fact second UNVERIFIED "the ticket route answered ${code}, which is the contract's 'this service serves no issuer': ${msg:-<no sentence>}. The installation's credentials service does not issue tickets at all, so a second account's ask is refused for a reason that is not about the record"
      ;;
    *)
      fact second UNVERIFIED "the second account ${SECOND} was refused (HTTP ${SECOND_STATUS}) with the code '${code:-<none>}': ${msg:-<no sentence>}. This check knows the contract's four refusals, and this one is not one it can read — the answer is in ${SCRATCH}/second.json (CHK841_KEEP_SCRATCH=1 keeps the directory)"
      ;;
    esac
    ;;
  esac
}

# step_copy measures the ticket itself: a ticket for a run id this check makes up,
# claimed once as the run's own job account — which is what a run does — and then
# presented again with another nonce. A ticket is claimed once (R2, R3), so the
# copy must be refused. The ticket and the token the first claim answers are
# handles of a run that does not exist: they live in 0600 files under the scratch
# directory, they are never printed, and the record this row made is removed at
# the end of it.
COPY_RUN=""
COPY_NOTE=""
step_copy() {
  if [ -z "$RUN_ACCOUNT" ]; then
    fact copy UNVERIFIED "no job account of a run could be read (the launch named no job, or the job answered no service account), and a ticket is bound to the account that claims it: without one this row could not tell a refused copy from a claim made as the wrong account — the answer would be about the account"
    return 0
  fi
  # A run id of this installation's shape (internal/task's ValidRunID: eight
  # digits, six digits, four hex), from the clock, so it collides with no run.
  COPY_RUN="$(date -u +%Y%m%d)-$(date -u +%H%M%S)-$(printf '%04x' $((RANDOM % 65536)))"
  say "asking for a ticket for a run this check made up (${COPY_RUN})"
  write_body "$SCRATCH/copy.body" "slug=${SLUG}" "run=${COPY_RUN}" "account=${RUN_ACCOUNT}" "launcher=${MY_ACCOUNT:-}" "max_runtime_seconds=${COPY_RUNTIME_SECONDS}"
  local status
  status=$(ask_route "$TICKET_PATH" "$SCRATCH/copy.body" "$SCRATCH/copy.json" "")
  read_json "$SCRATCH/copy.txt" wire "$SCRATCH/copy.json"
  if [ "${status#2}" = "$status" ]; then
    fact copy UNVERIFIED "the ticket route was not served for this check's own ask (HTTP ${status}, code '$(field "$SCRATCH/copy.txt" error)'): $(field "$SCRATCH/copy.txt" message). Nothing was issued, so there is no ticket whose copy could be measured — read the answer in ${SCRATCH}/copy.json (CHK841_KEEP_SCRATCH=1 keeps the directory)"
    return 0
  fi
  # The ticket, into a 0600 file of its own: the value itself is what a claim
  # presents, and nothing but the two body files below ever holds it.
  (umask 077; python3 -c 'import json,sys
doc = json.load(open(sys.argv[1]))
open(sys.argv[2], "w").write(doc.get("ticket") or "")
' "$SCRATCH/copy.json" "$SCRATCH/copy.ticket")
  if [ ! -s "$SCRATCH/copy.ticket" ]; then
    fact copy FAIL "the ticket route answered HTTP ${status} for this check's own ask and the answer carries no ticket: $(one_line "$(cat "$SCRATCH/copy.json" 2>/dev/null)"). A served ask that issues nothing is the contract's own reading of a wrong answer"
    return 0
  fi
  local ticket
  ticket=$(cat "$SCRATCH/copy.ticket")
  # The first claim: the run's own account, the ticket, one nonce. This is the
  # claim a run makes at its bootstrap, and the answer is the custom token a run
  # exchanges for its identity: this check keeps it in a 0600 file and prints
  # nothing of it.
  write_body "$SCRATCH/claim1.body" "ticket=${ticket}" "nonce=chk841-claim-1"
  local first
  first=$(ask_route "$CLAIM_PATH" "$SCRATCH/claim1.body" "$SCRATCH/claim1.json" "$RUN_ACCOUNT")
  read_json "$SCRATCH/claim1.txt" wire "$SCRATCH/claim1.json"
  case "$first" in
  2*) ;;
  *)
    fact copy UNVERIFIED "the ticket this check asked for could not be claimed as the run's own account ${RUN_ACCOUNT} (HTTP ${first}, code '$(field "$SCRATCH/claim1.txt" error)'): $(field "$SCRATCH/claim1.txt" message). Without one claim there is no claimed ticket for a copy to lose to, and this row measures nothing — the account must be one this machine may impersonate"
    rm -f "$SCRATCH/copy.ticket"
    return 0
    ;;
  esac
  # The copy: the SAME ticket, another hand's nonce. A ticket is claimed once,
  # and the same nonce again would be the first claim's own retry — which is why
  # this one is different.
  write_body "$SCRATCH/claim2.body" "ticket=${ticket}" "nonce=chk841-claim-2"
  local copy_status
  copy_status=$(ask_route "$CLAIM_PATH" "$SCRATCH/claim2.body" "$SCRATCH/claim2.json" "$RUN_ACCOUNT")
  read_json "$SCRATCH/claim2.txt" wire "$SCRATCH/claim2.json"
  local code msg
  code=$(field "$SCRATCH/claim2.txt" error)
  msg=$(field "$SCRATCH/claim2.txt" message)
  rm -f "$SCRATCH/copy.ticket" "$SCRATCH/claim1.json" "$SCRATCH/claim1.body"
  # The record this row made, removed: it names a run that will never exist, and
  # this check holds the database admin that wrote nothing else of it.
  local deleted
  deleted=$(rtdb_delete "$(record_path "$SLUG" "$COPY_RUN")")
  case "$deleted" in
  200 | 204) COPY_NOTE="was removed (HTTP ${deleted})" ;;
  *) COPY_NOTE="is still there (HTTP ${deleted}): remove $(record_path "$SLUG" "$COPY_RUN") yourself" ;;
  esac
  if [ "${copy_status#2}" != "$copy_status" ]; then
    fact copy FAIL "the ticket of the run ${COPY_RUN} was claimed TWICE as ${RUN_ACCOUNT} (HTTP ${copy_status}): a ticket is claimed once, its nonce and the account together are a run's identity for the ticket's life (R2), and one that can be claimed a second time is an identity two holders can wear. ${COPY_NOTE}"
    return 0
  fi
  case "$code" in
  "$REFUSED_CODE")
    fact copy PASS "the ticket of the run this check made up (${COPY_RUN}, slug ${SLUG}, account ${RUN_ACCOUNT}) was claimed once and the copy lost: the same ticket presented again with another nonce was refused with ${code} — ${msg:-<no sentence>}. The ticket and the token the first claim answered were this check's own handles of a run that does not exist; neither is printed anywhere, and ${COPY_NOTE}"
    ;;
  *)
    fact copy UNVERIFIED "the second claim of the ticket of ${COPY_RUN} was refused (HTTP ${copy_status}) with the code '${code:-<none>}': ${msg:-<no sentence>}. The copy did not win, and the refusal is not the one this row knows how to read — the answer is in ${SCRATCH}/claim2.json (CHK841_KEEP_SCRATCH=1 keeps the directory). ${COPY_NOTE}"
    ;;
  esac
}

# step_oldcli runs a binary of the release behind over the raised node: it must
# refuse it with the sentence this tree's own constants spell.
step_oldcli() {
  if [ -z "$OLD_CLI" ]; then
    fact old-cli UNVERIFIED "no binary of the release behind was named, so the criterion 'an older CLI prints the upgrade sentence' is NOT measured. Name one with --old-cli: the release before ${THIS_RELEASE} is published on the repository's releases page (a tarball per platform), and a binary of it must not be the \`fugaro\` on PATH — this check runs it against the installation the update has just raised"
    return 0
  fi
  say "running the older CLI (${OLD_CLI}) over the raised node"
  local rc=0
  (cd "$REPO" && "$OLD_CLI" doctor --json) >"$SCRATCH/old.json" 2>"$SCRATCH/old.err" || rc=$?
  local want
  want="$(printf "$SKEW_FMT" "$POLICY_VERSION" "$PREV_VERSION")${SKEW_SUFFIX}"
  if grep -qF -- "$want" "$SCRATCH/old.json" 2>/dev/null; then
    local arrives=""
    if [ -n "$ARRIVAL_FMT" ] && grep -qF -- "$THIS_RELEASE" "$SCRATCH/old.json" 2>/dev/null; then
      arrives="; it also names ${THIS_RELEASE}, the release the version arrived in, which is the row its own table carries"
    fi
    fact old-cli PASS "the binary ${OLD_CLI} of the release behind read the node of version ${POLICY_VERSION} and printed the upgrade sentence: '${want}'${arrives}. A CLI of the release behind refuses the raised node instead of launching against an installation that no longer mints what it would sign"
    return 0
  fi
  fact old-cli FAIL "the binary ${OLD_CLI} did not print the upgrade sentence '${want}'. What it printed (exit ${rc}): $(one_line "$(cat "$SCRATCH/old.json" "$SCRATCH/old.err" 2>/dev/null)"). A CLI of the release behind that reads a node of version ${POLICY_VERSION} without refusing it is a launcher that mints an identity this installation no longer accepts, which is the lockstep the version exists to keep (LM21)"
}

# step_pair reads the pairing route through the one client that uses it: after the
# update the pairing token can only come from the credentials service, so a code
# that reaches the terminal is a pairing the service served.
PAIR_STATUS=0
step_pair() {
  say "pairing with --json, ${PAIR_SECONDS}s of wait for a phone that is not there (pair)"
  run_closed_stdin "$((PAIR_SECONDS + 30))" pair fugaro pair --json --timeout "${PAIR_SECONDS}s"
  PAIR_STATUS=$RUN_STATUS
  read_json "$SCRATCH/pair.txt" events "$SCRATCH/pair.out"
  local count prose code names
  count=$(field "$SCRATCH/pair.txt" count)
  prose=$(field "$SCRATCH/pair.txt" prose)
  code=$(field "$SCRATCH/pair.txt" code)
  if grep -qx 'event=qr' "$SCRATCH/pair.txt" 2>/dev/null; then
    names=$(one_line "$(awk -F'=' '$1 == "event" { printf "%s ", $2 }' "$SCRATCH/pair.txt" 2>/dev/null || true)")
    fact pair PASS "'fugaro pair --json' reached the code after the update: its stream carried the events ${names:-(none)} (exit ${PAIR_STATUS} after ${RUN_SECONDS}s, the wait for a phone that is not there). The pairing token behind that code can only have come from the credentials service — the operator's own credential no longer holds the role that mints one — so the pairing route is served on this installation"
    return 0
  fi
  if [ "${count:-0}" = 0 ] && [ -z "$prose" ]; then
    fact pair FAIL "'fugaro pair --json' printed no event at all (exit ${PAIR_STATUS} after ${RUN_SECONDS}s): $(last_line "$SCRATCH/pair.err"). A pairing that says nothing is neither a pairing nor a refusal this row can read, and the command's output is the whole of what it has to say"
    return 0
  fi
  # minter_role_missing is the reading this row is here to catch: the command fell
  # back to the local mint — the R7 bridge — and the role that minted a pairing
  # token is gone, so nothing issued one. Every other refusal is about this
  # installation's roster or its operator, which is a different reading.
  case "$code" in
  minter_role_missing)
    fact pair FAIL "'fugaro pair --json' fell back to signing its own pairing token and refused with minter_role_missing (exit ${PAIR_STATUS} after ${RUN_SECONDS}s): ${prose:-<no sentence>}. The role that minted a pairing token is exactly what this release's move takes away, and the credentials service is what replaces it — a fallback that reaches the missing role means the fallback happened at all. Nothing was paired. Read the service's own log for the pairing route and run 'fugaro update' again"
    ;;
  '')
    fact pair UNVERIFIED "'fugaro pair --json' printed no code and no refusal this row knows how to read (exit ${PAIR_STATUS} after ${RUN_SECONDS}s): '${prose:-<no sentence>}'. The pairing is NOT measured — its events were ${count:-0} and none of them was the code. The stream is in ${SCRATCH}/pair.out (CHK841_KEEP_SCRATCH=1 keeps the directory)"
    ;;
  *)
    fact pair UNVERIFIED "'fugaro pair --json' was refused with ${code} (exit ${PAIR_STATUS} after ${RUN_SECONDS}s): ${prose:-<no sentence>}. The refusal is this installation's own answer about the operator or the roster rather than about the route the update installed, so the pairing is NOT measured — read the code's own remedy (docs/pairing/*.md) and run the check again"
    ;;
  esac
}

# ----------------------------------------------------------------- the result
# Everything above prints to stderr: the block below is the whole of what this
# check says on stdout, one row per thing it looked at, and a verdict. A row's
# detail says what was read and why it is what it is, so a break of the
# installation can be told from a break of the check by reading it.
VERDICT=""
verdict() {
  if [ -n "$ABORT" ]; then printf 'ABORTED'; return 0; fi
  if [ ! -s "$FACTS" ]; then printf 'UNVERIFIED'; return 0; fi
  if grep -q '|FAIL|' "$FACTS" 2>/dev/null; then printf 'FAIL'; return 0; fi
  if grep -q '|UNVERIFIED|' "$FACTS" 2>/dev/null; then printf 'UNVERIFIED'; return 0; fi
  printf 'PASS'
}

print_block() {
  printf '\n=== CHECK 841 RESULT ===\n'
  local key state detail
  while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '%-10s %-10s %s\n' "$key" "$state" "$detail"
  done <"$FACTS"
  printf '%-10s %-10s %s\n' verdict "$VERDICT" "exit 0 only when every measured row above is PASS"
  printf '=== END CHECK 841 RESULT ===\n'
}

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  if [ "$KEEP_SCRATCH" = 1 ]; then
    SCRATCH_REMOVED=no
    return 0
  fi
  # Only ever a directory this script made: the marker file is the proof, and the
  # path itself must be the scratch shape mktemp makes.
  case "$SCRATCH" in
  "${TMPDIR:-/tmp}"/chk841.*) ;;
  *) SCRATCH_REMOVED=no; return 0 ;;
  esac
  if [ -f "$SCRATCH/.chk841-mine" ]; then
    rm -rf "$SCRATCH"
    SCRATCH_REMOVED=yes
  fi
}

finish() {
  # --help and --dry-run print their own text and no block; the scratch directory
  # they never made is cleaned up and nothing else happens.
  if [ "$REPORT" != yes ]; then
    cleanup
    exit 0
  fi
  set +e
  local rc=0
  if [ "$REACHED_END" != yes ] && [ -z "$ABORT" ]; then
    fact aborted FAIL "this check ended before its last row without stopping itself, so a command it ran failed under \`set -e\` and the rows below are as far as it got: the transcript of that command is in the scratch directory (CHK841_KEEP_SCRATCH=1 keeps it)"
  fi
  if [ -n "$ABORT" ]; then
    fact aborted FAIL "stopped before the rows below could be measured: ${ABORT}"
  fi
  # Said before the removal below, which is what makes the row a statement about
  # what this check does rather than a report of what has happened: the block is
  # printed first, so a person reads what was left behind and where.
  local scratch_note
  case "$SCRATCH" in
  '') scratch_note="no scratch directory was made, so nothing was left in one" ;;
  *)
    if [ "$KEEP_SCRATCH" = 1 ]; then
      scratch_note="the scratch directory ${SCRATCH} is KEPT (CHK841_KEEP_SCRATCH=1) and holds the update's transcript"
    elif [ -f "$SCRATCH/.chk841-mine" ]; then
      scratch_note="the scratch directory ${SCRATCH} was removed from the exit trap"
    else
      scratch_note="the scratch directory ${SCRATCH} was NOT removed (it is not one this check made)"
    fi
    ;;
  esac
  if [ "$NO_RUN" = yes ]; then
    fact restore INFO "${scratch_note}; no run was launched (--no-run), so this check leaves no branch and no pull request behind, and what it wrote to the installation is what \`fugaro update\` wrote and nothing else"
  else
    fact restore INFO "${scratch_note}; the runs this check launched are NOT undone — their branches and pull requests stay on the repository (this check holds no credential to remove them, and their model calls are billed)${COPY_RUN:+; the issuance record it made for its own made-up run ${COPY_RUN} was ${COPY_NOTE:-not read back}}"
  fi
  # The verdict is read ONCE, before the scratch directory goes: the rows are the
  # file it reads, and a verdict taken after the removal would be a verdict about
  # nothing.
  VERDICT=$(verdict)
  print_block
  cleanup
  if [ -n "$ABORT" ]; then rc=2; else
    case "$VERDICT" in
    PASS) rc=0 ;;
    FAIL) rc=1 ;;
    *) rc=3 ;;
    esac
  fi
  exit "$rc"
}

# ------------------------------------------------------------------- the plan
usage() {
  REPORT=no
  cat <<'USAGE'
Check 841 — the grant moves and nothing stops: one `fugaro update` on a live
installation at the previous release, with a run in flight, and what a run, a
second account, a copied ticket, an older CLI and a pairing each then do.

Usage:
  bash scripts/live-checks/check-841.sh --second-launcher <account> [flags]
  bash scripts/live-checks/check-841.sh --dry-run
  bash scripts/live-checks/check-841.sh --help

Flags (each also has a CHK841_* variable; see the header of the script):
  --second-launcher <account>  the account the second account's ask is made as
                               (impersonated). REQUIRED: the ask is the point
  --old-cli <binary>           a fugaro binary of the release behind, for the row
                               that reads its upgrade sentence (UNVERIFIED without)
  --repo <dir>                 the checkout to run in (default: the working directory)
  --deadline <seconds>         how long `fugaro update` may take (1800)
  --start-wait <seconds>       how long a launched run may take to read running (900)
  --claim-wait <seconds>       how long a run may take to claim its ticket (900)
  --wait <seconds>             how long the run in flight may then take to end (3600)
  --pair-wait <seconds>        how long `fugaro pair` waits for a phone (5)
  --max-usd <amount>           the cap each launch passes (1); empty passes none
  --no-run                     launch no run: the update rows are still measured
  --dry-run                    print what it would do and call nothing

Owner-run: it needs CHK841_OWNER_RUN=yes and refuses a coding agent's session.
It runs on the fugaro-dev installation in us-east5 and on no other: the project
and the region are literals of the script.
USAGE
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    -h | --help)
      usage
      exit 0
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    --no-run)
      NO_RUN=yes
      shift
      ;;
    --repo)
      [ $# -ge 2 ] || stop "--repo needs a directory"
      REPO=$2
      shift 2
      ;;
    --repo=*)
      REPO=${1#--repo=}
      shift
      ;;
    --second-launcher)
      [ $# -ge 2 ] || stop "--second-launcher needs an account address"
      SECOND=$2
      shift 2
      ;;
    --second-launcher=*)
      SECOND=${1#--second-launcher=}
      shift
      ;;
    --old-cli)
      [ $# -ge 2 ] || stop "--old-cli needs a path to a fugaro binary"
      OLD_CLI=$2
      shift 2
      ;;
    --old-cli=*)
      OLD_CLI=${1#--old-cli=}
      shift
      ;;
    --deadline | --start-wait | --claim-wait | --wait | --pair-wait | --max-usd)
      [ $# -ge 2 ] || stop "$1 needs a value"
      case "$1" in
      --deadline) DEADLINE=$2 ;;
      --start-wait) START_SECONDS=$2 ;;
      --claim-wait) CLAIM_SECONDS=$2 ;;
      --wait) WAIT_SECONDS=$2 ;;
      --pair-wait) PAIR_SECONDS=$2 ;;
      --max-usd) MAX_USD=$2 ;;
      esac
      shift 2
      ;;
    --*)
      stop "unknown flag $1 (--help lists them)"
      ;;
    *)
      stop "unexpected argument $1 (--help lists the flags)"
      ;;
    esac
  done
  case "$DEADLINE" in
  '' | *[!0-9]*) stop "--deadline must be a whole number of seconds" ;;
  esac
  [ "$DEADLINE" -ge 60 ] || stop "--deadline must be at least 60 seconds (a converge that is given less is a converge this check would kill halfway)"
  [ "$START_SECONDS" -ge 1 ] || stop "--start-wait must be at least 1 second"
  [ "$CLAIM_SECONDS" -ge 1 ] || stop "--claim-wait must be at least 1 second"
  [ "$WAIT_SECONDS" -ge 1 ] || stop "--wait must be at least 1 second"
  [ "$PAIR_SECONDS" -ge 1 ] || stop "--pair-wait must be at least 1 second"
  [ "$POLL_SECONDS" -ge 1 ] || stop "CHK841_POLL_SECONDS must be at least 1"
  [ "$COPY_RUNTIME_SECONDS" -ge 1 ] 2>/dev/null || stop "CHK841_COPY_RUNTIME must be a whole number of seconds"
  case "$COPY_RUNTIME_SECONDS" in *[!0-9]*) stop "CHK841_COPY_RUNTIME must be a whole number of seconds" ;; esac
}

# dry_run_plan is the mode that calls NOTHING: no gcloud, no fugaro, no cloud
# read, no convergence, no launch. It prints the rows this check would measure
# and what each of them would run, which is what makes it usable while building
# the check and safe to run anywhere.
dry_run_plan() {
  REPORT=no
  cat <<PLAN
CHECK 841 DRY RUN — nothing below is called: no gcloud, no fugaro, no HTTP, no
launch, no convergence. The rows this check would measure on the installation
${P} in ${L}, from the checkout ${REPO} (configuration ${CFG}):
  xtrace   this shell is not tracing (-x would copy the tokens it handles)
  owner    CHK841_OWNER_RUN=yes, else the check stops
  belong   no ambient belong/EdgeWeb/EdgeServer project; --second-launcher ${SECOND:-<UNSET>} is another account than gcloud's own
  tree     the ticket's variable, the two identity routes and the refusal codes out of internal/identitywire/wire.go; the policy version, the release that introduced it and the sentence an older CLI prints out of internal/projectpolicy/version.go; the escaping of internal/budget/model.go; the service's and the database's variables out of internal/backend/batch/env.go and internal/runner/budget_session.go
  installation  ${CFG} exists, names ${P}/${L}, states a rtdb_url and a credentials_service.url; the checkout's own 'fugaro doctor --json' agrees about the project and the region
  premise  fugaro project policy show --json must read the version BELOW internal/projectpolicy/version.go's Version (the previous release's node); fugaro version must be this release or newer
  run      fugaro run --repo ${REPO} --title ... --task-file ... --json${MAX_USD:+ --max-usd ${MAX_USD}}  (BILLABLE), then follows it until its record reads running (--start-wait ${START_SECONDS}s)
  update   fugaro update  < /dev/null, streams captured, deadline ${DEADLINE}s (TERM then KILL); its own line about the runs in flight, and the published node raised to this release's version
  asked    every line of that transcript, for the shapes a question leaves behind
  inflight the launched run followed to its terminal status (--wait ${WAIT_SECONDS}s), and its record read back
  session  one MORE fugaro run (BILLABLE), then the issuance record under internal/identitywire/wire.go's pathRecords and the ledger at runs/<slug>/<run>, read over the installation's rtdb_url
  second   internal/identitywire/wire.go's TicketPath asked as ${SECOND:-<UNSET>} (impersonated) for a run of another launcher, which must be refused
  copy     a ticket asked for a run id this check makes up, claimed once as the run's own job account, then presented again with another nonce: the copy must be refused, and the record is removed afterwards
  old-cli  ${OLD_CLI:-<none>} doctor --json over the raised node, for the upgrade sentence${OLD_CLI:+}
  pair     fugaro pair --json --timeout ${PAIR_SECONDS}s < /dev/null, read for its event names (never its payload)
  restore  the scratch directory, from the exit trap
Knobs in force: CHK841_CONFIG=${CFG} CHK841_REPO=${REPO} CHK841_SECOND_LAUNCHER=${SECOND:-<unset>} CHK841_OLD_CLI=${OLD_CLI:-<unset>} CHK841_DEADLINE=${DEADLINE} CHK841_START_SECONDS=${START_SECONDS} CHK841_CLAIM_SECONDS=${CLAIM_SECONDS} CHK841_WAIT_SECONDS=${WAIT_SECONDS} CHK841_POLL_SECONDS=${POLL_SECONDS} CHK841_PAIR_SECONDS=${PAIR_SECONDS} CHK841_MAX_USD=${MAX_USD} CHK841_NO_RUN=${NO_RUN} CHK841_KEEP_SCRATCH=${KEEP_SCRATCH}
A real run needs, and this mode never does: CHK841_OWNER_RUN=yes at your own terminal, ${SECOND:-a second launcher}, gcloud authenticated for ${P}, and the \`fugaro\` on PATH at this release with the installation still at the previous one.
PLAN
  exit 0
}

# ------------------------------------------------------------------ the check
main() {
  DRY_RUN=${CHK841_DRY_RUN:-no}
  REPO=${CHK841_REPO:-$PWD}
  SECOND=${CHK841_SECOND_LAUNCHER:-}
  OLD_CLI=${CHK841_OLD_CLI:-}
  CFG=${CHK841_CONFIG:-$HOME/.config/fugaro/projects/fugaro.yaml}
  DEADLINE=${CHK841_DEADLINE:-1800}
  START_SECONDS=${CHK841_START_SECONDS:-900}
  CLAIM_SECONDS=${CHK841_CLAIM_SECONDS:-900}
  WAIT_SECONDS=${CHK841_WAIT_SECONDS:-3600}
  POLL_SECONDS=${CHK841_POLL_SECONDS:-20}
  PAIR_SECONDS=${CHK841_PAIR_SECONDS:-5}
  NO_RUN=no
  KEEP_SCRATCH=${CHK841_KEEP_SCRATCH:-0}
  TITLE="Check 841: a run in flight across the update"
  if [ -n "${CHK841_NO_RUN:-}" ]; then NO_RUN=yes; fi
  # The scratch directory is made BEFORE the flags are read: every row of the
  # block is written into it, and a flag this check refuses is a row a person
  # should still be able to read.
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk841.XXXXXXXX") \
    || stop "mktemp could not make a scratch directory, and this check keeps the transcript of everything it runs in one"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/.chk841-mine"
  FACTS="$SCRATCH/facts.txt"
  : >"$FACTS"
  write_reader
  write_body_py
  parse_args "$@"
  say "Check 841 — converges the installation ${P}/${L} with a run in flight (--help lists the flags)"
  if [ "$DRY_RUN" = yes ]; then
    dry_run_plan
  fi
  need bash
  need mktemp
  need python3
  need curl
  need gcloud
  need fugaro
  guard_xtrace
  guard_agent
  guard_owner
  guard_belong
  guard_tree
  step_installation
  step_premise
  step_run
  step_update
  step_asked
  step_inflight
  step_session
  step_second
  step_copy
  step_oldcli
  step_pair
  REACHED_END=yes
}

trap 'set +e; finish' EXIT
main "$@"
