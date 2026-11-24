#!/usr/bin/env bash
# Check 791 — the two refusals the release adds under the budget mode that
# refuses nothing, as ONE script YOU run.
#
# `always-a-budget` lands every installation on `observe` (D3010) and states two
# things it now refuses that a mode of off used to let through:
#
#   D3020  a model with no price is refused in EVERY mode, with the `model_prices`
#          entry named as the fix. Prices come from the embedded table
#          (internal/pricing/table.go), the provider catalog and the project's
#          own `model_prices`; mode off skipped the check, and observe no longer
#          does.
#   D3022  a repository's committed `fugaro.yaml` that says `budget.mode: off` is
#          rejected AT ONCE, with the removal named as the fix, rather than read
#          as unset for one release. Off is accepted nowhere (AB2), and the
#          repository schema lists two modes.
#
# Both are refusals a person meets, and both name a fix a person has to perform:
# an entry to add to `model_prices`, and a key to remove from a repository's
# file. Nothing offline can settle that the refusal a REAL installation prints is
# the one the plan wrote, that the fix it names is the fix that clears it, or that
# the refusal really happens under a mode that refuses nothing — so this check
# reads them on a sandbox repository, row by row:
#
#   premise       the mode in force for the repository is `observe`: the
#                 published policy is read and a mode that is not observe makes
#                 the two refusals below rows about another mode. A mode that
#                 cannot be read is UNVERIFIED, never a PASS.
#   unpriced      `fugaro validate fugaro.yaml` on the checkout must REFUSE, and
#                 its refusal must name `model_prices` (D3020). The model it
#                 names is the one the repository's own file carries.
#   mode-off      the repository's COMMITTED `fugaro.yaml` is read with `git show
#                 HEAD:fugaro.yaml`: when it says `budget.mode: off`, a launch
#                 from that checkout must be refused, at once, with the word
#                 `off` named as the fix (D3022). A repository whose committed
#                 file does not say off leaves this row UNVERIFIED — the check
#                 names what to commit instead of inventing a reading.
#
# It writes nothing and changes nothing. The ONE launch it makes exists to be
# REFUSED, and a config load is what refuses it: nothing is spent when the
# refusal happens, which is the reading the row is for. `fugaro validate` is the
# only other command it runs, and that spends nothing. A launch the release does
# NOT refuse is the finding (a run on a mode the release does not have): it is a
# real run then, and the row names it and the command that stops it.
#
# Usage:      bash scripts/live-checks/check791.sh --sandbox <a sandbox repository
#               checkout> [--installation <name>] [--dry-run]
#             --dry-run       prints the plan and calls NOTHING.
#             --sandbox       the checkout whose committed `fugaro.yaml` is read
#                             and whose configuration is validated (also
#                             CHECK791_SANDBOX). Its working tree must match its
#                             HEAD, or the row that reads the COMMITTED file says
#                             so rather than reading the wrong one.
#             --installation  the installation whose published policy is read for
#                             the mode in force (also CHECK791_INSTALLATION).
# Needs:      bash, mktemp, python3, git, fugaro. Nothing is installed and
#             nothing is fetched.
# Where:      the checkout you name and the installation you name, and nowhere
#             else. It reads the published policy, which names repositories; run
#             it on a sandbox you are happy to see named.
# Costs:      nothing, as long as the release holds: `fugaro validate` spends
#             nothing, and the one launch spends nothing when the refusal happens
#             — which is what the row is for. A launch the release does NOT refuse
#             is a real, billable run, and the `mode-off` row names it and the
#             command that stops it. No build, no write.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start in
#             a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check791_test.sh) drives it against a fake
#             `fugaro` and a real local git repository, and touches no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went. Nothing else was created.
# Output:     progress on stderr, ONE block on stdout headed CHECK 791 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 791 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 791 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 791 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 791 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS.
#
# No secret is read, so none can be printed: the documents it reads are a
# repository's own configuration, a published policy and a command's refusal.
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

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads a real checkout with it"
}

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
print("" if cur is None else cur)
PY
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 791: the refusals it reads are meant to be read by a person, and the fix it names is a person's to make. Run it yourself: bash scripts/live-checks/check791.sh --sandbox <checkout>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check791.sh"
  fi
}

usage() {
  cat <<'USAGE'
Check 791 — the two refusals the release adds under observe, read on a sandbox
repository. Run by YOU.

Usage:
  bash scripts/live-checks/check791.sh --sandbox <a sandbox repository checkout> \
    [--installation <name>] [--dry-run]
  bash scripts/live-checks/check791.sh --help

Row by row:
  premise   the mode in force for the repository is 'observe'
  unpriced  a model with no price is refused, and the refusal names 'model_prices' (D3020)
  mode-off  a committed 'fugaro.yaml' that says off is refused with its fix (D3022)

The last line of the block is the verdict and the exit status: PASS (exit 0),
FAIL (exit 1), INCOMPLETE (exit 2, stopped early) or INCOMPLETE (exit 3, a row
reads UNVERIFIED). Nothing is written and no run is launched.
USAGE
}

SANDBOX=${CHECK791_SANDBOX:-}
INSTALLATION=${CHECK791_INSTALLATION:-}
DRY_RUN=no
if [ "${CHECK791_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --sandbox)
    [ $# -ge 2 ] || stop "--sandbox needs the path of a sandbox repository checkout"
    SANDBOX=$2
    shift 2
    ;;
  --installation)
    [ $# -ge 2 ] || stop "--installation needs the installation's name"
    INSTALLATION=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  *)
    stop "unknown argument $1 (usage: check791.sh --sandbox <checkout>, --help, or --dry-run)"
    ;;
  esac
done

dry_run_plan() {
  cat <<PLAN
Check 791 plan (nothing is called: no git, no fugaro, nothing spent)

  sandbox:      ${SANDBOX:-<not given>}
  installation: ${INSTALLATION:-<not given>}

What it would do, in order:

  premise   git -C ${SANDBOX:-<sandbox>} show HEAD:fugaro.yaml        (the COMMITTED file)
            fugaro project policy show --json                        (the mode in force)
            -> the mode must read 'observe'; anything else is UNVERIFIED, never a PASS.
  unpriced  fugaro validate ${SANDBOX:-<sandbox>}
            -> it must REFUSE, and its refusal must name 'model_prices' (D3020). The model it
               names is read off the committed file above.
  mode-off  when HEAD:fugaro.yaml carries 'budget.mode: off',
            fugaro run --task-file <a task the check writes> --json   (in ${SANDBOX:-<sandbox>})
            -> it must be refused, at once, with the word 'off' named (D3022). A config load is
               what refuses it, so nothing is spent; a launch it does NOT refuse is a real run,
               and the row names it and the command that stops it.
               A committed file that does not say off leaves this row UNVERIFIED.

Costs: nothing, as long as the release holds. 'fugaro validate' spends nothing, and the one
       launch spends nothing when the refusal happens — which is what the row is for.
Cleanup: the check's scratch directory.
PLAN
}

[ -n "$SANDBOX" ] || { [ "$DRY_RUN" = yes ] || stop "no --sandbox was given (or --help for the usage)"; }

guard_xtrace
guard_agent

if [ "$DRY_RUN" = yes ]; then
  dry_run_plan
  exit 0
fi

need bash
need mktemp
need python3
need git
need fugaro

[ -d "$SANDBOX" ] || stop "$SANDBOX is not a directory: --sandbox is a checkout of a sandbox repository"
[ -d "$SANDBOX/.git" ] || stop "$SANDBOX is not a git checkout: --sandbox is the repository whose COMMITTED fugaro.yaml is read"

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk791.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk791-mine"
: >"$MARKER"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  [ -f "$MARKER" ] || { say "refusing to remove $SCRATCH: it carries no marker from this check"; return 0; }
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
  local facts=$1 status=$2
  printf '\nCHECK 791 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-11s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 791 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

finish() {
  local status=$?
  trap - EXIT
  # The facts are read BEFORE the scratch directory holding them is removed.
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: the committed fugaro.yaml, the published policy, the validate refusal and the launch's own answer). Nothing else was created and nothing was changed"
  else
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH} — remove it yourself"
  fi
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    case "$(verdict "$facts")" in
    PASS) status=0 ;;
    FAIL) status=1 ;;
    *) status=3 ;;
    esac
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# committed_file writes the repository's COMMITTED fugaro.yaml to $1. The file a
# run reads is the one on the branch, not the one in the working tree, and the
# two differ exactly when a task is half-written — which is when a reading of the
# wrong one would matter.
committed_file() { # committed_file <outfile>
  git -C "$SANDBOX" show HEAD:fugaro.yaml >"$1" 2>"$1.err"
}

# yaml_get FILE KEY prints the value of a top-level `budget:` sub-key of the
# repository's own file. It reads the indentation rather than pulling in a YAML
# parser: the file's `budget:` block is two-space indented in every repository
# this tree writes it for, and a file that does not carry the key answers "".
yaml_get() { # yaml_get <file> <subkey>
  python3 - "$1" "$2" <<'PY'
import re, sys
try:
    text = open(sys.argv[1]).read()
except Exception:
    sys.exit(0)
cur = None
for line in text.splitlines():
    m = re.match(r"^([A-Za-z0-9_.-]+):\s*(.*)$", line)
    if m:
        cur = m.group(1)
        continue
    m = re.match(r"^\s+([A-Za-z0-9_.-]+):\s*(.*)$", line)
    if m and cur == "budget" and m.group(1) == sys.argv[2]:
        v = m.group(2).strip()
        if v[:1] in ("'", '"') and v[-1:] == v[:1]:
            v = v[1:-1]
        print(v)
        sys.exit(0)
print("")
PY
}

# model_of prints the model the repository's file names for the agent: the first
# `model:` key under `agent:`, or the top-level one a file may carry. It is what
# the unpriced row is about — the model the run would use.
model_of() { # model_of <file>
  python3 - "$1" <<'PY'
import re, sys
try:
    text = open(sys.argv[1]).read()
except Exception:
    sys.exit(0)
cur = None
agent_model = ""
for line in text.splitlines():
    m = re.match(r"^([A-Za-z0-9_.-]+):\s*(.*)$", line)
    if m:
        cur = m.group(1)
        if cur == "model" and m.group(2).strip():
            print(m.group(2).strip())
            sys.exit(0)
        continue
    m = re.match(r"^\s+([A-Za-z0-9_.-]+):\s*(.*)$", line)
    if m and m.group(1) == "model" and m.group(2).strip():
        if cur == "agent":
            print(m.group(2).strip())
            sys.exit(0)
        if not agent_model:
            agent_model = m.group(2).strip()
print(agent_model)
PY
}

# ---------------------------------------------------------------- the rows

step_premise() {
  say "reading the module in force and the repository's committed file"
  (cd "$SANDBOX" && fugaro project policy show --project "$INSTALLATION" --json) >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.json.err" || true
  local mode
  mode=$(jget "$SCRATCH/policy.json" budget.mode)
  if [ -z "$mode" ]; then
    mode=$(jget "$SCRATCH/policy.json" policy.budget.mode)
  fi
  case "$mode" in
  observe)
    fact premise PASS "the published policy puts the repository in budget mode 'observe', so both refusals below are read under a mode that refuses nothing — which is what D3020 and D3022 are about"
    ;;
  "")
    fact premise UNVERIFIED "the policy read did not answer with a budget mode ('fugaro project policy show --project ${INSTALLATION} --json' printed: $(head -c 300 "$SCRATCH/policy.json.err" 2>/dev/null)), so the mode the two refusals below happen under is NOT measured: a refusal read under an unknown mode is a refusal about nothing. Read the policy yourself and record the mode in the register's section"
    return 0
    ;;
  *)
    fact premise FAIL "the repository is in budget mode '${mode}', not 'observe': D3020 and D3022 are statements about what happens under observe, and a refusal read under another mode measures a different thing. Point --sandbox at a repository the policy puts in observe"
    return 0
    ;;
  esac
  committed_file "$SCRATCH/committed.yaml" || {
    fact premise FAIL "the repository's committed fugaro.yaml could not be read with 'git show HEAD:fugaro.yaml': $(head -c 300 "$SCRATCH/committed.yaml.err" 2>/dev/null). The file a RUN reads is the committed one, so this check reads that rather than the working tree"
    return 0
  }
  if ! git -C "$SANDBOX" diff --quiet HEAD -- fugaro.yaml 2>/dev/null; then
    fact premise UNVERIFIED "the checkout's fugaro.yaml differs from its HEAD, so the COMMITTED file and the file 'fugaro validate' reads are not the same document: commit or stash the change and run this check again, or the rows below read one file and name the other"
    return 0
  fi
}

step_unpriced() {
  local model vstatus=0
  model=$(model_of "$SCRATCH/committed.yaml")
  say "validating the checkout: a model with no price must be refused, naming model_prices"
  (cd "$SANDBOX" && fugaro validate fugaro.yaml) >"$SCRATCH/validate.out" 2>&1 || vstatus=$?
  # The exit status is what says whether the validation REFUSED: a warning that
  # names the fix and exits 0 is exactly the reading this row exists to tell
  # apart from the refusal D3020 writes.
  if [ "$vstatus" -eq 0 ]; then
    if grep -qi 'model_prices' "$SCRATCH/validate.out"; then
      fact unpriced FAIL "the validation NAMES 'model_prices' and exits 0: $(head -c 400 "$SCRATCH/validate.out" 2>/dev/null). A model with no price is REFUSED in every mode (D3020) — a warning that names the fix is a run that goes on to spend a model nobody priced"
      return 0
    fi
    fact unpriced UNVERIFIED "the checkout validates cleanly, so this repository's model ('${model:-<not read from the committed file>}') IS priced here: nothing is refused and the 'model_prices' fix has no subject. Point --sandbox at a repository whose model has no price (or name one the table does not carry) and run this check again"
    return 0
  fi
  if ! grep -qi 'model_prices' "$SCRATCH/validate.out"; then
    fact unpriced FAIL "the validation refused (exit ${vstatus}) without naming 'model_prices': $(head -c 400 "$SCRATCH/validate.out" 2>/dev/null). The person who meets this refusal has to be told which entry clears it (D3020), and a refusal that does not name it leaves them with nothing to do"
    return 0
  fi
  fact unpriced PASS "the model '${model:-<not read>}' has no price here and the validation is refused (exit ${vstatus}) with 'model_prices' named as the fix, under a published mode of observe: D3020 holds on a real checkout"
}

step_mode_off() {
  local committed_mode
  committed_mode=$(yaml_get "$SCRATCH/committed.yaml" mode)
  if [ "$committed_mode" != "off" ]; then
    fact mode-off UNVERIFIED "the repository's COMMITTED fugaro.yaml carries budget.mode '${committed_mode:-<none>}', not 'off', so this row has no subject: D3022 is about a repository whose own file says off. Commit 'budget.mode: off' on a sandbox branch (the state the release rejects) and run this check again"
    return 0
  fi
  say "launching a run from a repository whose committed fugaro.yaml says off"
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: check791 — a repository whose committed fugaro.yaml says off

Change nothing: this launch exists to be refused.
TASK
  local rstatus=0
  (cd "$SANDBOX" && fugaro run --task-file "$SCRATCH/task.md" --json) >"$SCRATCH/launch.out" 2>&1 || rstatus=$?
  if [ "$rstatus" -eq 0 ]; then
    local rid
    rid=$(jget "$SCRATCH/launch.out" run_id)
    [ -n "$rid" ] || rid=$(jget "$SCRATCH/launch.out" id)
    fact mode-off FAIL "a launch from a checkout whose committed fugaro.yaml says 'budget.mode: off' was NOT refused: it launched ${rid:-a run whose id this check could not read}, and off is accepted nowhere (AB2, D3022) — a repository's file that says it is rejected at once, so a launch that goes ahead is a run on a mode the release does not have. STOP IT: 'fugaro cancel --now ${rid:-<the run id>}'; the launcher printed $(head -c 400 "$SCRATCH/launch.out" 2>/dev/null)"
    return 0
  fi
  if ! grep -qi 'off' "$SCRATCH/launch.out"; then
    fact mode-off FAIL "the launch was refused (exit ${rstatus}) and the refusal never names 'off': $(head -c 400 "$SCRATCH/launch.out" 2>/dev/null). The person meeting it has to be told which key to remove, and a refusal that does not name it leaves them guessing"
    return 0
  fi
  fact mode-off PASS "a launch from a checkout whose committed fugaro.yaml says 'budget.mode: off' is refused (exit ${rstatus}) and the refusal names the word 'off': a repository's file that says it is rejected at once (D3022)"
}

step_premise
step_unpriced
step_mode_off
REACHED_END=yes
