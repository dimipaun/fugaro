#!/usr/bin/env bash
# Check 791 — the two refusals the release adds under every budget mode, as ONE
# script YOU run.
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
# Neither statement is about a mode: both hold under observe and under enforce,
# so the mode in force is read as context and never as a demand — an
# installation in enforce is not a finding.
#
# The check BUILDS ITS OWN SUBJECTS in its scratch directory, so a real run needs
# nothing of the sandbox but its own committed file, and nothing of the
# installation but the policy it already published:
#
#   unpriced  a fugaro.yaml, a project layer and a project config the check
#             writes, the layer naming a model absent from the price table. The
#             price table is read first (`fugaro budget prices --json`, offline):
#             a table that carries the model leaves the row UNVERIFIED rather
#             than measuring nothing.
#   mode-off  a scratch checkout carrying the sandbox's own COMMITTED
#             `fugaro.yaml` (`git show HEAD:fugaro.yaml`) with `budget.mode: off`
#             written in, committed on its own. The launch is made there and
#             never in the sandbox, whose working tree, branches and HEAD are
#             left exactly as they were.
#
# Nothing is published and no cloud setting is written: no `budget set`, no
# `config publish`, no policy write. The check writes only inside its scratch
# directory.
#
# It reads three commands and no secret: `fugaro project policy show` (the mode
# in force, context), `fugaro budget prices --json` (offline), `fugaro validate`
# (spends nothing), and the ONE launch of the mode-off row — which exists to be
# REFUSED, and a config load is what refuses it, so nothing is spent. A launch
# the release does NOT refuse is the finding: it is a real, billable run then,
# and the row names it and the command that stops it.
#
# Usage:      bash scripts/live-checks/check791.sh --sandbox <a sandbox repository
#               checkout> [--installation <name>] [--dry-run]
#             --dry-run       prints the plan, lists every premise and says which
#                             of them it CANNOT settle, and calls NOTHING.
#             --sandbox       the checkout whose committed `fugaro.yaml` the
#                             check's own scratch checkout is built from (also
#                             CHECK791_SANDBOX). It is read only: no branch, no
#                             commit and no file of it is written.
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
#             command that stops it. No build, no write outside the scratch
#             directory.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start in
#             a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check791_test.sh) drives it against a fake
#             `fugaro` and a real local git repository, and touches no cloud.
# Cleanup:    its scratch directory — the subjects it wrote, the scratch checkout
#             included — is removed from the exit trap, and the `restore` row
#             says whether it really went. Nothing else was created.
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
# No secret is read, so none can be printed: the documents it reads are the
# policy it names, the price table, a repository's own committed configuration
# and a command's refusal.
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
Check 791 — the two refusals the release adds under every budget mode, read on
subjects the check builds itself. Run by YOU.

Usage:
  bash scripts/live-checks/check791.sh --sandbox <a sandbox repository checkout> \
    [--installation <name>] [--dry-run]
  bash scripts/live-checks/check791.sh --help

Row by row:
  premise   the mode in force is 'observe' or 'enforce' (context, not a demand:
            both rows below hold in every mode)
  unpriced  a model with no price is refused, and the refusal names 'model_prices'
            (D3020), measured under observe AND under enforce
  mode-off  a committed 'fugaro.yaml' that says off is refused with its fix (D3022)

The subjects are the check's own: a scratch fugaro.yaml, project layer and
project config for the first row, and a scratch checkout holding the sandbox's
committed file with 'budget.mode: off' for the second. The sandbox is read, never
written, and no cloud setting is changed.

The last line of the block is the verdict and the exit status: PASS (exit 0),
FAIL (exit 1), INCOMPLETE (exit 2, stopped early) or INCOMPLETE (exit 3, a row
reads UNVERIFIED). Only an all-PASS block is a PASS.
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

# The subjects: a project and a model this check invents, so a repository that
# commits a model of its own cannot change what the rows read. The model is
# spelled as a Claude model ID (`pricing.IsAlias`) and is not one: an alias is
# refused as an alias, with no `model_prices` in the sentence, which is a
# different refusal from D3020's.
SUBJECT_PROJECT=check791-scratch
SUBJECT_MODEL=claude-check791-unpriced

dry_run_plan() {
  # Quoted, so nothing in the plan is expanded and nothing in it can run: the
  # three values a person needs are substituted afterwards.
  local policy_args="" sandbox_txt="${SANDBOX:-<not given>}"
  [ -z "$INSTALLATION" ] || policy_args="--project $INSTALLATION "
  sed -e "s|@SANDBOX@|${sandbox_txt}|g" \
    -e "s|@INSTALLATION@|${INSTALLATION:-<not given>}|g" \
    -e "s|@POLICY_ARGS@|${policy_args}|g" \
    -e "s|@MODEL@|${SUBJECT_MODEL}|g" \
    -e "s|@PROJECT@|${SUBJECT_PROJECT}|g" <<'PLAN'
Check 791 plan (nothing is called: no git, no fugaro, nothing spent)

  sandbox:      @SANDBOX@
  installation: @INSTALLATION@

What it would do, in order:

  premise   fugaro project policy show @POLICY_ARGS@--json
            -> the mode in force is CONTEXT and not a demand: D3020 and D3022 hold
               under observe and under enforce alike, so either mode is a PASS.
               Only a policy that says 'off' (every run refused for the policy's
               own reason) or an unreadable one is UNVERIFIED.

  unpriced  the check writes its own subject in its scratch directory: a fugaro.yaml,
            a project layer naming @MODEL@, and the project config of
            project @PROJECT@; then, read in that environment:
              fugaro budget prices --json                      (offline, the price table)
              fugaro validate --offline --project-layer <layer> fugaro.yaml   (budget.mode unset = observe)
              fugaro validate --offline --project-layer <layer> <enforce>.yaml
            -> both must REFUSE, and both refusals must name 'model_prices' and
               @MODEL@ (D3020). It holds in every mode: one mode's refusal is a
               refusal about a mode, not this one.

  mode-off  the check writes its own scratch checkout, holding the sandbox's
            COMMITTED fugaro.yaml (git -C @SANDBOX@ show HEAD:fugaro.yaml)
            with 'budget.mode: off' written in, and launches from THERE
            (never from the sandbox):
              fugaro run --task-file <a task the check writes> --json
            -> it must be refused, at once, with the word 'off' named (D3022). A config
               load is what refuses it, so nothing is spent; a launch it does NOT
               refuse is a real run, and the row names it and the command that stops it.

Premises this dry run does NOT settle (nothing is read: no git, no fugaro, no policy):

  NOT settled  the installation's published budget mode is not 'off'   (read at run time)
  NOT settled  the price table can be read, and does not carry @MODEL@  (read at run time)
  NOT settled  the sandbox's committed fugaro.yaml can be read         (read at run time)
  NOT settled  the sandbox has an 'origin' remote                      (read at run time;
               a scratch checkout of it cannot be placed without one, and the row reads UNVERIFIED)
  NOT settled  its committed file names 'main' as its git.base_branch  (read at run time;
               the scratch checkout is built on 'main', so another branch is a repository this row does not cover)
  NOT settled  the launch is refused at once, naming 'off'             (read at run time; only a real run settles it)

  The two subjects themselves are settled by construction: the check writes them,
  and writes nothing anywhere but its scratch directory.

Costs: nothing, as long as the release holds. 'fugaro validate' and 'budget prices' spend
       nothing, and the one launch spends nothing when the refusal happens — which is what
       the row is for.
Cleanup: the check's scratch directory, the scratch checkout included.
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
[ -d "$SANDBOX/.git" ] || stop "$SANDBOX is not a git checkout: --sandbox is the repository whose COMMITTED fugaro.yaml the subject is built from"

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
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone, its subjects with it (the fugaro.yaml, the project layer, the project config, the scratch checkout, the price table, the refusals and the launch's own answer). The sandbox is untouched and nothing else was created"
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
# wrong one would matter. Nothing is written into the checkout.
committed_file() { # committed_file <outfile>
  git -C "$SANDBOX" show HEAD:fugaro.yaml >"$1" 2>"$1.err"
}

# mode_off_file rewrites $1 with its top-level `budget:` block replaced by one
# that says off. It reads the indentation rather than pulling in a YAML parser:
# every repository this tree writes spells the block two-space indented, and a
# file that carries none gets the block appended.
mode_off_file() { # mode_off_file <file>
  python3 - "$1" <<'PY'
import sys
path = sys.argv[1]
lines = open(path).read().splitlines()
out, skipping = [], False
for line in lines:
    top = line[:1] not in ("", " ", "\t", "#")
    if top:
        skipping = line.split(":", 1)[0].strip() == "budget"
        if skipping:
            continue
    if skipping:
        continue
    out.append(line)
out.append("budget:")
out.append("  mode: off")
open(path, "w").write("\n".join(out) + "\n")
PY
}

# ---------------------------------------------------------------- the rows

step_premise() {
  say "reading the budget mode in force (context, not a demand)"
  local mode
  if [ -n "$INSTALLATION" ]; then
    (cd "$SANDBOX" && fugaro project policy show --project "$INSTALLATION" --json) >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.json.err" || true
  else
    (cd "$SANDBOX" && fugaro project policy show --json) >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.json.err" || true
  fi
  mode=$(jget "$SCRATCH/policy.json" budget.mode)
  if [ -z "$mode" ]; then
    mode=$(jget "$SCRATCH/policy.json" policy.budget.mode)
  fi
  case "$mode" in
  observe | enforce)
    fact premise PASS "the published policy puts the repository in budget mode '${mode}'. Neither row below depends on the mode — an unpriced model is refused in every mode (D3020) and a file that says off is refused in every mode (D3022) — so this row is the context the refusals are read in, not a demand"
    ;;
  off)
    fact premise UNVERIFIED "the published policy still says 'off', which this release refuses for its own reason: a launch refused below would be the policy's refusing, not the repository file's, so the mode-off row would measure nothing. Republish the policy ('fugaro update') and run this check again"
    ;;
  "")
    fact premise UNVERIFIED "the policy read answered with no budget mode ('fugaro project policy show --json' printed: $(head -c 300 "$SCRATCH/policy.json.err" 2>/dev/null)). The two rows below hold in every mode, so this is context rather than a refusal — but a launch refused by a policy that says 'off' would not be the repository file's refusal, and this check cannot tell that apart. Read the policy yourself, or pass --installation <name>"
    ;;
  *)
    fact premise UNVERIFIED "the policy read answered budget mode '${mode}', which is not one of the two modes this release has: the refusals below are still D3020's and D3022's, but the installation is not in a state this check can read the mode of. Check it with 'fugaro doctor'"
    ;;
  esac
}

# step_unpriced is D3020 read under BOTH modes: the check writes its own
# fugaro.yaml, its own project layer naming a model with no price, and its own
# project config, and validates the file under observe and under enforce. The
# layer is where the model is named because a repository's own file may no
# longer name one (D3237): the project does, and a project layer file is the
# document this check can write without publishing anything.
step_unpriced() {
  local dir="$SCRATCH/unpriced" cfg="$SCRATCH/config" priced prices_status=0
  mkdir -p "$dir" "$cfg/fugaro/projects" "$SCRATCH/cache" "$SCRATCH/home"
  say "reading the price table, then validating the check's own subject under both modes"

  printf 'version: 1\nname: %s\ngcp_project: %s\nregion: us-central1\nruns_bucket: %s-runs\nbuild: { machine_type: E2_HIGHCPU_8 }\n' \
    "$SUBJECT_PROJECT" "$SUBJECT_PROJECT" "$SUBJECT_PROJECT" >"$cfg/fugaro/projects/$SUBJECT_PROJECT.yaml"
  (
    cd "$dir"
    env XDG_CONFIG_HOME="$cfg" XDG_CACHE_HOME="$SCRATCH/cache" HOME="$SCRATCH/home" \
      fugaro budget prices --json
  ) >"$dir/prices.json" 2>"$dir/prices.json.err" || prices_status=$?
  if [ "$prices_status" -ne 0 ]; then
    fact unpriced UNVERIFIED "the price table could not be read: 'fugaro budget prices --json' exited ${prices_status} with $(head -c 300 "$dir/prices.json.err" 2>/dev/null). Whether the subject is priced is the premise this row reads it for, and a table read that failed says nothing about it — a validation refused below could be refusing anything"
    return 0
  fi
  if grep -q "$SUBJECT_MODEL" "$dir/prices.json" 2>/dev/null; then
    fact unpriced UNVERIFIED "the price table this installation reads CARRIES '${SUBJECT_MODEL}', so this row's model IS priced here and there is nothing for D3020 to refuse. That is the subject's name being taken, not a reading: change SUBJECT_MODEL in this script to a model the price table does not carry"
    return 0
  fi
  priced=$(jget "$dir/prices.json" models.0.id)
  if [ -z "$priced" ]; then
    fact unpriced UNVERIFIED "the price table read answered with no models at all: $(head -c 300 "$dir/prices.json" 2>/dev/null). A table with no entry in it is not a table that does not carry the subject, so this row cannot say the model it wrote is unpriced — and a refusal read under it measures nothing. Check the read with 'fugaro budget prices --json'"
    return 0
  fi
  local pins="    models: { coder: ${SUBJECT_MODEL}${priced:+, reviewer: $priced, background: $priced} }"
  {
    printf 'version: 1\nproject: %s\ngcp_project: %s\ndefaults:\n  agent:\n%s\n' "$SUBJECT_PROJECT" "$SUBJECT_PROJECT" "$pins"
  } >"$dir/layer.yaml"
  local mode file observe_status="" enforce_status=""
  for mode in observe enforce; do
    # Per leg: a status left over from the leg before would read the second
    # leg's exit 0 as the first leg's refusal, and a validation that passes
    # under one mode would be reported as the refusal of every mode.
    local status=0
    file="$dir/fugaro-${mode}.yaml"
    {
      printf 'version: 1\nproject: %s\ngcp_project: %s\ngit: { provider: github, base_branch: main }\nagent: { auth: api-key }\n' "$SUBJECT_PROJECT" "$SUBJECT_PROJECT"
      [ "$mode" = observe ] || printf 'budget: { mode: %s }\n' "$mode"
      printf 'workflows:\n  app: { commands: { build: "true", test: "true" } }\n'
    } >"$file"
    (
      cd "$dir"
      env XDG_CONFIG_HOME="$cfg" XDG_CACHE_HOME="$SCRATCH/cache" HOME="$SCRATCH/home" \
        fugaro validate --offline --project-layer layer.yaml "$file"
    ) >"$dir/validate.$mode.out" 2>&1 || status=$?
    if [ "$status" -eq 0 ]; then
      fact unpriced FAIL "the validation of the check's own subject under '${mode}' exits 0: $(head -c 400 "$dir/validate.$mode.out" 2>/dev/null). A model with no price is REFUSED in every mode (D3020) — a validation that passes is a run that goes on to spend a model nobody priced"
      return 0
    fi
    if ! grep -qi 'model_prices' "$dir/validate.$mode.out"; then
      fact unpriced FAIL "the validation under '${mode}' refused (exit ${status}) without naming 'model_prices': $(head -c 400 "$dir/validate.$mode.out" 2>/dev/null). The person who meets this refusal has to be told which entry clears it (D3020), and a refusal that does not name it leaves them with nothing to do"
      return 0
    fi
    if ! grep -qF "$SUBJECT_MODEL" "$dir/validate.$mode.out"; then
      fact unpriced FAIL "the validation under '${mode}' refused naming 'model_prices' but never naming '${SUBJECT_MODEL}': $(head -c 400 "$dir/validate.$mode.out" 2>/dev/null). The refusal is about another pin, so it says nothing about the model this row wrote"
      return 0
    fi
    if [ "$mode" = observe ]; then observe_status=$status; else enforce_status=$status; fi
  done
  fact unpriced PASS "a model with no price ('${SUBJECT_MODEL}') is refused under observe AND under enforce (exit ${observe_status} and exit ${enforce_status}), each refusal naming the 'model_prices' entry that clears it: D3020 holds in every mode, on a subject this check wrote"
}

step_mode_off() {
  local subj="$SCRATCH/mode-off" origin
  say "building a scratch checkout whose committed fugaro.yaml says off"
  if ! committed_file "$SCRATCH/committed.yaml"; then
    fact mode-off UNVERIFIED "the sandbox's COMMITTED fugaro.yaml could not be read with 'git -C ${SANDBOX} show HEAD:fugaro.yaml': $(head -c 300 "$SCRATCH/committed.yaml.err" 2>/dev/null). The subject of this row is that document with 'budget.mode: off' written in, and inventing one would measure a repository nobody has. Commit a fugaro.yaml on a branch of a sandbox checkout and point --sandbox at it"
    return 0
  fi
  origin=$(git -C "$SANDBOX" remote get-url origin 2>/dev/null || true)
  if [ -z "$origin" ]; then
    fact mode-off UNVERIFIED "the sandbox has no 'origin' remote, so a launch from a scratch checkout of it cannot be placed: the repository a run belongs to is read off the checkout's origin. Point --sandbox at a checkout of a repository the installation serves"
    return 0
  fi
  mkdir -p "$subj"
  {
    git -C "$subj" init -q -b main
    git -C "$subj" remote add origin "$origin"
    cp "$SCRATCH/committed.yaml" "$subj/fugaro.yaml"
    mode_off_file "$subj/fugaro.yaml"
    git -C "$subj" add fugaro.yaml
    git -C "$subj" -c user.email=check791@example.invalid -c user.name=check791 commit -qm "check791: budget.mode: off"
  } >/dev/null 2>"$subj/build.err" || {
    fact mode-off FAIL "the scratch checkout could not be built: $(head -c 300 "$subj/build.err" 2>/dev/null). The row writes only inside its scratch directory and never into the sandbox"
    return 0
  }
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: check791 — a repository whose committed fugaro.yaml says off

Change nothing: this launch exists to be refused.
TASK
  say "launching a run from the scratch checkout (never from the sandbox)"
  local rstatus=0
  (cd "$subj" && fugaro run --task-file "$SCRATCH/task.md" --json) >"$SCRATCH/launch.out" 2>&1 || rstatus=$?
  if [ "$rstatus" -eq 0 ]; then
    local rid
    rid=$(jget "$SCRATCH/launch.out" run_id)
    [ -n "$rid" ] || rid=$(jget "$SCRATCH/launch.out" id)
    fact mode-off FAIL "a launch from a checkout whose committed fugaro.yaml says 'budget.mode: off' was NOT refused: it launched ${rid:-a run whose id this check could not read}, and off is accepted nowhere (AB2, D3022) — a repository's file that says it is rejected at once, so a launch that goes ahead is a run on a mode the release does not have. STOP IT: 'fugaro cancel --now ${rid:-<the run id>}'; the launcher printed $(head -c 400 "$SCRATCH/launch.out" 2>/dev/null)"
    return 0
  fi
  if ! grep -qi 'off' "$SCRATCH/launch.out"; then
    fact mode-off FAIL "the launch was refused (exit ${rstatus}) and the refusal never names the word 'off': $(head -c 400 "$SCRATCH/launch.out" 2>/dev/null). The person meeting it has to be told which key to remove, and a refusal that does not name it leaves them guessing"
    return 0
  fi
  fact mode-off PASS "a launch from a scratch checkout whose COMMITTED fugaro.yaml says 'budget.mode: off' is refused (exit ${rstatus}) and the refusal names the word 'off': a repository's file that says it is rejected at once (D3022), in every mode"
}

step_premise
step_unpriced
step_mode_off
REACHED_END=yes
