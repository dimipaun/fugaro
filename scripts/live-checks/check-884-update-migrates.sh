#!/usr/bin/env bash
# Check 884 — `fugaro update` migrates the dev installation and no repository's
# source moves. Run by YOU, at release 1's install.
#
# The whole upgrade is one command (CLAUDE.md's HARD RULE): `fugaro update`
# converges the installation — Terraform, the Firebase rules, the published
# policy and the published layer, the job images and the local config — and a
# second command, a deletion or a hand repair is a broken release. The same
# release also reaches a repository: `fugaro update --repo <checkout>` converges
# the checkout (a workflow, a `fugaro.yaml` field, the plugin pin it offers to
# remove). Nothing offline can settle what that does to a real installation and
# to the repositories on it, so this check does its own setup and reads, row by
# row:
#
#   premise     every checkout it is pointed at is a git checkout with a clean
#               working tree, so "did the update move anything" is a question
#               that has an answer. A premise that does not hold is a STOP
#               (exit 2), not a finding.
#   update      `fugaro update --project <installation>` — the real one, the
#               command the owner would type — must exit 0. It is the migration
#               this check is about, and it is NOT read-only. A step the update
#               left FOR YOU (a secret's value, the confirmation of a billable
#               image build, a publish that is yours to make) is not that: the
#               command is unfinished, not refused, and the row reads UNVERIFIED
#               with the step counted, telling you to run it yourself.
#   policy      the published policy, read back through `fugaro project policy
#               show --json`, must decode on THIS CLI: a policy carrying a key
#               this Fugaro does not know is the pair that did not converge, and
#               the command refuses it by name. The row names which of the
#               plan's new top-level keys the published policy carries.
#   source      for each `--repo` checkout: its HEAD, its branch, its tree and
#               every reference are recorded, `fugaro update --repo <checkout>`
#               is run in it, and they are recorded again. Nothing may have
#               moved: an update that commits, tags or rewrites anything in a
#               repository moves a source the owner did not ask it to touch. A
#               change in the WORKING TREE is not that — the update offers the
#               plugin pin's removal as a diff the owner commits or does not —
#               so it is counted and named, and it is not a failure. A checkout
#               whose update was left for you is UNVERIFIED, for the reason the
#               update row gives.
#   restore     the check's own scratch directory is gone.
#
# It runs `fugaro update` once for the installation and once per checkout, and
# launches no run: the only money it can spend is what the update itself
# rebuilds, which is the update's own confirmation and not this check's. It is
# NOT read-only.
#
# Usage:      bash scripts/live-checks/check-884-update-migrates.sh \
#               --installation <name> --repo <checkout> [--repo <checkout>...] \
#               [--dry-run]
#             --dry-run       prints the plan and calls NOTHING: no fugaro, no
#                             update, no policy read.
#             --installation  the installation's Fugaro project name, the one
#                             `fugaro update --project` takes.
#             --repo          a checkout of a repository of that installation:
#                             the check runs `fugaro update --repo` in it and
#                             reads whether anything of its source moved. Give
#                             it once per repository. It may be repeated.
# Needs:      bash, mktemp, python3 (every document is read by one python3
#             reader), git, fugaro. Nothing is installed and nothing is
#             fetched. Run it from the directory where you would type `fugaro
#             update`: the check runs that command with your cwd unchanged, and
#             with its output captured so it never waits on you (Fugaro asks for
#             nothing). A step only you can take is therefore LEFT, counted in
#             the update row, and the row tells you to run that one command in
#             your own terminal and run the check again.
# Where:      the installation you name and the checkouts you point it at, and
#             NOWHERE else: no other project, no other repository, no other
#             machine.
# Costs:      nothing by itself. The update it runs is the update you would run
#             anyway, and its own rebuilds are its own confirmation.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-884-update-migrates_test.sh) drives it
#             against a fake `fugaro` on PATH and real git checkouts, and
#             touches no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went. Nothing else is
#             created: the updates are the ones the owner would run anyway, and
#             a checkout is left exactly as the update left it (a diff it
#             offers is the owner's to commit or drop).
# Output:     progress on stderr, ONE block on stdout headed CHECK 884 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 884 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 884 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 884 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 884 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS.
#
# NO secret is read at all, and none may be printed: the documents this check
# reads are the published policy (a table of models and routes) and a
# repository's own git metadata. It reads no secret value, mounts nothing, and
# its rows name keys, branches and paths.
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
REPOS=()

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
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads a real installation with it"
}

# jget FILE PATH prints the value at a dotted path of a JSON document, or the
# empty string.
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

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 884: it runs the real update against a real installation, which writes the installation's cloud side and the repositories on it. Run it yourself: bash scripts/live-checks/check-884-update-migrates.sh --installation <name> --repo <checkout>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-884-update-migrates.sh"
  fi
}

usage() {
  cat <<'USAGE'
Check 884 — `fugaro update` migrates the dev installation and no repository's
source moves. Run by YOU, at release 1's install, from the directory where you
would type `fugaro update`, with your own terminal.

Usage:
  bash scripts/live-checks/check-884-update-migrates.sh \
    --installation <name> --repo <checkout> [--repo <checkout>...] [--dry-run]
  bash scripts/live-checks/check-884-update-migrates.sh --help

It runs the real `fugaro update --project <name>`, reads the published policy
back, and runs `fugaro update --repo <checkout>` in each checkout, recording
HEAD, the branch, the tree and every reference before and after. Every row it
prints is one criterion of the register's Check 884; the last line of the block
is the verdict and the exit status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

INSTALLATION=${CHECK884_INSTALLATION:-}
DRY_RUN=no
if [ "${CHECK884_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi
if [ -n "${CHECK884_REPO:-}" ]; then REPOS+=("$CHECK884_REPO"); fi

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --installation)
    [ $# -ge 2 ] || stop "--installation needs the installation's name"
    INSTALLATION=$2
    shift 2
    ;;
  --repo)
    [ $# -ge 2 ] || stop "--repo needs the path of a repository's checkout"
    REPOS+=("$2")
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  *)
    stop "unknown argument $1 (usage: check-884-update-migrates.sh --installation <name> --repo <checkout> [--repo <checkout>...], --help, or --dry-run)"
    ;;
  esac
done

dry_run_plan() {
  cat <<PLAN
Check 884 plan (nothing is called: no fugaro, no update, no policy read)

  installation:  ${INSTALLATION:-<not given>}
  repositories:  ${REPOS[*]:-<none given>}

What it would do, in order:

  premise      git -C <checkout> status --porcelain   (one per checkout)
               -> every checkout must be a git checkout with a clean working tree, so that "did the update
               move anything" has an answer. A premise that does not hold STOPS the check (exit 2).
  update       fugaro update --project ${INSTALLATION:-<installation>}   (with your cwd unchanged, output captured)
               -> the real update: Terraform, the Firebase rules, the published policy and layer, the job
               images, the local config. It must exit 0. This is the migration the check is about, and it
               is NOT read-only. A step it left for YOU (its output says "N step(s) left for you") means the
               command is unfinished rather than refused: the row reads UNVERIFIED and tells you to run
               'fugaro update --project ${INSTALLATION:-<installation>}' yourself and run this check again.
  policy       fugaro project policy show --json --project ${INSTALLATION:-<installation>}
               -> the published policy must decode on this CLI (a key this Fugaro does not know is the
               pair that did not converge). The row names which of model_routing and pause it carries.
  source       for each of the ${#REPOS[@]} checkout(s): record HEAD, the branch, HEAD's tree and every
               reference; run
               fugaro update --repo --project ${INSTALLATION:-<installation>} <checkout>
               in it; record them again
               -> nothing may have moved (a commit, a tag, a rewritten ref). A change in the WORKING TREE
               is the diff the update offers the owner, not a source moving: it is counted and named. A
               checkout whose update was left for you is UNVERIFIED, and the row names it.
  restore      the check's own scratch directory is removed.

Costs: nothing by itself. The update it runs is the one you would run anyway.
Cleanup: the check's scratch directory. Nothing else is created: a checkout is left as the update left it.
PLAN
}

if [ "$DRY_RUN" = no ]; then
  [ -n "$INSTALLATION" ] || stop "no --installation was given (or --help for the usage)"
  [ "${#REPOS[@]}" -gt 0 ] || stop "no --repo was given (or --help for the usage)"
fi

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

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk884.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk884-mine"
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
  printf '\nCHECK 884 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 884 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
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
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the update"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: every checkout's HEAD, branch, tree, references and working tree before and after its update, the installation's own update output, and the published policy as this CLI printed it). Nothing else was created: the updates are the ones the owner would run anyway, and a checkout is left exactly as the update left it — a diff it offers is the owner's to commit or drop"
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

# ---------------------------------------------------------------- the readers

# canonical_of CHECKOUT prints the checkout's repository as `owner/name`, read
# from its own `origin` and folded the way a slug's readable part folds one
# (ASCII lower case, a trailing `.git` off, the scp form's host off). It is how a
# row names a checkout the owner can recognise.
canonical_of() { # canonical_of <checkout>
  local tmp="$SCRATCH/origin-$$.txt"
  git -C "$1" remote get-url origin >"$tmp" 2>/dev/null || : >"$tmp"
  python3 - "$tmp" <<'PY'
import re, sys
raw = open(sys.argv[1]).read().strip()
if not raw:
    sys.exit(0)
raw = raw.split("://")[-1]
raw = raw.split("@")[-1]
head = raw.split("/")[0]
if ":" in head:
    raw = raw.split(":", 1)[1]
parts = [p for p in raw.split("/") if p]
if len(parts) < 2:
    sys.exit(0)
repo = re.sub(r"\.git$", "", "/".join(parts[-2:]))
print("".join(c.lower() if "A" <= c <= "Z" else c for c in repo))
PY
}

# checkout_name CHECKOUT prints how a row names a checkout: its canonical
# `owner/name` where its origin gives one, and its path otherwise (a checkout
# with no origin is still a checkout whose source the update must not move).
checkout_name() { # checkout_name <checkout>
  local canon
  canon=$(canonical_of "$1" || true)
  if [ -n "$canon" ]; then printf '%s' "$canon"; else printf '%s' "$1"; fi
}

# safe_name NAME prints a name as a file name this check can write: a checkout
# is named `owner/name` or by its path, and neither one is a single path
# component, so a row's own name cannot be a scratch file's name as it stands.
safe_name() { # safe_name <name>
  printf '%s' "$1" | tr -c 'a-zA-Z0-9._-' '-'
}

# committed_state CHECKOUT OUT records everything of a checkout's git state that
# an update must not move: HEAD, the branch HEAD is on, HEAD's own tree, and
# every reference with the object it names. The working tree is recorded
# separately, because the update is allowed to leave a diff there.
committed_state() { # committed_state <checkout> <out>
  {
    printf 'head %s\n' "$(git -C "$1" rev-parse HEAD 2>/dev/null || printf '<none>')"
    printf 'branch %s\n' "$(git -C "$1" symbolic-ref -q --short HEAD 2>/dev/null || printf '<detached>')"
    printf 'tree %s\n' "$(git -C "$1" rev-parse 'HEAD^{tree}' 2>/dev/null || printf '<none>')"
    git -C "$1" for-each-ref --format='ref %(refname) %(objectname)' 2>/dev/null | LC_ALL=C sort
  } >"$2" 2>/dev/null || true
}

# changed_lines BEFORE AFTER prints the lines that differ between two recorded
# states, at most three of them, as one sentence a row can carry. It is what
# names what moved rather than printing two documents side by side.
changed_lines() { # changed_lines <before> <after>
  local lines
  lines=$(diff "$1" "$2" 2>/dev/null | grep -E '^[<>] ' | head -3 || true)
  if [ -z "$lines" ]; then
    printf 'the state changed in a way these two records do not spell out'
  else
    printf '%s' "$lines" | tr '\n' ';' | sed 's/; */; /g; s/; $//'
  fi
}

# working_changes CHECKOUT OUT writes the checkout's working-tree changes after
# the update and prints their number.
working_changes() { # working_changes <checkout> <out>
  git -C "$1" status --porcelain 2>/dev/null >"$2" || : >"$2"
  local n
  n=$(grep -c . "$2" 2>/dev/null || true)
  printf '%s' "${n:-0}"
}

# working_paths FILE prints up to three paths of a `git status --porcelain`
# answer, named as paths and never as contents.
working_paths() { # working_paths <file>
  awk '{ $1 = ""; sub(/^ +/, ""); print }' "$1" 2>/dev/null | head -3 | tr '\n' ' ' | sed 's/ *$//'
}

# policy_keys FILE prints one line per top-level key of the `policy` member of a
# `fugaro project policy show --json` answer. It is how the row reads what the
# published policy NAMES rather than guessing at its members: the plan's release
# 1 adds `model_routing` and `pause`, and this check was written before either
# was in the tree.
policy_keys() { # policy_keys <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
p = doc.get("policy") if isinstance(doc, dict) else None
if not isinstance(p, dict):
    sys.exit(0)
for key in sorted(p):
    print(key)
PY
}

# ------------------------------------------------------------- the check's rows

# step_premise refuses to measure anything until every checkout is one whose
# "did the update move anything" can be read. A checkout with uncommitted
# changes already carries a change nothing of this check made, and a directory
# that is not a git checkout has no refs to compare at all.
step_premise() {
  say "reading the state of every checkout (premise)"
  local dirty=() notgit=() names=() r name n g safe
  for r in "${REPOS[@]}"; do
    name=$(checkout_name "$r")
    safe=$(safe_name "$name")
    if [ ! -d "$r/.git" ]; then
      notgit+=("$r")
      continue
    fi
    g=$(working_changes "$r" "$SCRATCH/pre-$safe.txt" || true)
    if [ "${g:-0}" != 0 ]; then
      dirty+=("$name (${g} path(s): $(working_paths "$SCRATCH/pre-$safe.txt"))")
    fi
    names+=("$name")
  done
  if [ "${#notgit[@]}" -gt 0 ]; then
    fact premise FAIL "$(printf '%s, ' "${notgit[@]}" | sed 's/, $//') is not a git checkout: --repo is a checkout of a repository of this installation, and a directory that is no git checkout has no HEAD and no refs for this check to read before and after its update"
    stop "a repository the check was pointed at is not a git checkout"
  fi
  if [ "${#dirty[@]}" -gt 0 ]; then
    fact premise FAIL "$(printf '%s; ' "${dirty[@]}" | sed 's/; $//') has uncommitted changes before the check ran: a checkout that is dirty to begin with is one whose 'did the update move anything' has no answer, because the change was there first. Commit or stash it (or point --repo at a clean checkout) and run the check again"
    stop "a checkout the check was pointed at has uncommitted changes"
  fi
  n=${#names[@]}
  fact premise PASS "every one of the ${n} checkout(s) this check was pointed at is a git checkout with a clean working tree — $(printf '%s, ' "${names[@]}" | sed 's/, $//') — so what ${PROG}'s two updates do to their source is readable before and after them"
}

# left_for_you FILE prints the update's own count of the steps it left ("N
# step(s) left for you") when its output says the run stopped for steps only a
# person can take, and nothing otherwise. `fugaro update` exits 1 both for that
# and for a refusal, and the two are opposite things: a refusal is the migration
# failing, a step left is the migration waiting for the person who ran it. This
# check runs both doors with their output redirected, so a step that needs a
# terminal is left rather than asked (initsecrets' canPrompt wants stdout and
# stderr to be terminals, and --json never prompts), and the sentence is the
# only thing in the output that tells the two apart.
left_for_you() { # left_for_you <file>
  grep -o -E '[0-9]+ step\(s\) left for you' "$1" 2>/dev/null | tail -1
}

# step_update runs the real installation update. It is the check's own subject:
# the whole upgrade is that one command (CLAUDE.md's HARD RULE), and an update
# that does not finish is not the migration this check is about.
step_update() {
  say "running 'fugaro update --project ${INSTALLATION}' (the migration)"
  local rc=0 left=""
  fugaro update --project "$INSTALLATION" >"$SCRATCH/update.out" 2>&1 || rc=$?
  if [ "$rc" -eq 0 ]; then
    fact update PASS "'fugaro update --project ${INSTALLATION}' exited 0: the installation was converged by the one command that is the whole upgrade (CLAUDE.md's HARD RULE). Its own output was $(wc -l <"$SCRATCH/update.out" | tr -d ' ') line(s), left in the check's scratch directory until the check ends"
    return 0
  fi
  left=$(left_for_you "$SCRATCH/update.out" || true)
  if [ -n "$left" ]; then
    fact update UNVERIFIED "'fugaro update --project ${INSTALLATION}' converged what asks nothing and exited 1 with ${left}: the one command is not finished because a step only YOU can take is left — a secret's value, the confirmation of a billable image build, a publish that is yours to make. This check runs the door with its output redirected so it can never wait for an answer, and a step that needs a person is the whole of what that leaves behind, so the MIGRATION IS NOT MEASURED YET and this row does not pass: run 'fugaro update --project ${INSTALLATION}' yourself, in your own terminal, answer what it asks, and run this check again. The door's own last line: $(tail -n 1 "$SCRATCH/update.out" 2>/dev/null)"
    return 0
  fi
  fact update FAIL "'fugaro update --project ${INSTALLATION}' did not exit 0 (exit ${rc}) and left no step for you: $(head -c 600 "$SCRATCH/update.out" 2>/dev/null). The whole upgrade is that one command, and an update that refused or failed is not the migration this check is about. Every row below still reads the checkouts, but the installation is where it stopped"
}

# step_policy reads the published policy back. The criterion is that the two
# halves of the pair agree: a policy carrying a key this Fugaro does not know is
# refused by name, which is exactly the state a half-applied migration leaves.
step_policy() {
  say "reading the published policy of ${INSTALLATION} back through this CLI (policy)"
  local rc=0 keys=""
  fugaro project policy show --json --project "$INSTALLATION" >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    local first
    first=$(head -c 400 "$SCRATCH/policy.err" 2>/dev/null)
    if grep -qi -- 'does not know' "$SCRATCH/policy.err" 2>/dev/null; then
      fact policy FAIL "the published policy of ${INSTALLATION} carries a key this Fugaro does not know, and 'fugaro project policy show' refused it (exit ${rc}): ${first}. A published policy and the CLI that reads it are one pair, and a key the reader does not know is a policy that never takes effect — the release that wrote it and this binary did not converge, which is what this row is about"
      return 0
    fi
    fact policy UNVERIFIED "the published policy of ${INSTALLATION} could not be read ('fugaro project policy show --json' exited ${rc}): ${first}. A read that did not answer is NOT a policy that fails to decode, so whether the two halves of the pair agree is NOT measured here. Read it yourself in your own terminal and record it in the register's section"
    return 0
  fi
  keys=$(policy_keys "$SCRATCH/policy.json" || true)
  if [ -z "$keys" ]; then
    fact policy UNVERIFIED "'fugaro project policy show --json' exited 0, but its answer carries no policy member this check can read, so the published policy's own keys are NOT measured: $(head -c 200 "$SCRATCH/policy.json" 2>/dev/null)"
    return 0
  fi
  local named=""
  if printf '%s\n' "$keys" | grep -qx 'model_routing'; then named="model_routing"; fi
  if printf '%s\n' "$keys" | grep -qx 'pause'; then named="${named}${named:+, }pause"; fi
  if [ -z "$named" ]; then
    fact policy PASS "the published policy decoded on this Fugaro (its top-level keys: $(printf '%s' "$keys" | tr '\n' ' ' | sed 's/ $//')) and it carries neither model_routing nor pause, so the routing table and the pause settings are not published for this project — a project that routes nothing by table has none to publish, and this row is about the policy and this binary agreeing, not about a table that exists"
    return 0
  fi
  fact policy PASS "the published policy decoded on this Fugaro and carries ${named}: the release that wrote the policy and the binary that reads it are one pair, and the row names what else it carries ($(printf '%s' "$keys" | tr '\n' ' ' | sed 's/ $//'))"
}

# step_source runs `fugaro update --repo` in every checkout and reads whether
# anything of its source moved. A change in the working tree is NOT that: the
# update offers the plugin pin's removal as a diff the owner commits or does
# not, so it is counted and named and the row still passes.
step_source() {
  say "updating each checkout and reading whether anything of its source moved (source)"
  local r name safe rc=0 lf="" failed=() lefts=() moved=() lines=() diffs=() g
  for r in "${REPOS[@]}"; do
    name=$(checkout_name "$r")
    safe=$(safe_name "$name")
    committed_state "$r" "$SCRATCH/before-$safe.txt"
    rc=0
    fugaro update --repo --project "$INSTALLATION" "$r" >"$SCRATCH/repo-$safe.out" 2>&1 || rc=$?
    committed_state "$r" "$SCRATCH/after-$safe.txt"
    if [ "$rc" -ne 0 ]; then
      lf=$(left_for_you "$SCRATCH/repo-$safe.out" || true)
      if [ -n "$lf" ]; then
        lefts+=("$name (exit ${rc}, ${lf})")
        continue
      fi
      failed+=("$name (exit ${rc}: $(head -c 200 "$SCRATCH/repo-$safe.out" 2>/dev/null))")
      continue
    fi
    if ! cmp -s "$SCRATCH/before-$safe.txt" "$SCRATCH/after-$safe.txt"; then
      moved+=("$name")
      diffs+=("$(changed_lines "$SCRATCH/before-$safe.txt" "$SCRATCH/after-$safe.txt")")
      continue
    fi
    g=$(working_changes "$r" "$SCRATCH/post-$safe.txt" || true)
    if [ "${g:-0}" != 0 ]; then
      lines+=("left ${g} working-tree change of ${name} ($(working_paths "$SCRATCH/post-$safe.txt")) — the diff the update offers, which commits nothing")
    fi
  done
  if [ "${#failed[@]}" -gt 0 ]; then
    fact source FAIL "$(printf '%s; ' "${failed[@]}" | sed 's/; $//'): 'fugaro update --repo' did not exit 0 there, so whether the update moves a repository's source is NOT measured for it — an update that stopped is not an update that moved nothing"
    return 0
  fi
  if [ "${#moved[@]}" -gt 0 ]; then
    local i=0 detail=""
    while [ "$i" -lt "${#moved[@]}" ]; do
      detail="${detail}${moved[$i]}: ${diffs[$i]}; "
      i=$((i + 1))
    done
    fact source FAIL "'fugaro update --repo' moved a reference of $(printf '%s, ' "${moved[@]}" | sed 's/, $//') — ${detail%, }. A repository's source is the owner's: the update converges a workflow, a config field or the plugin pin it offers as a diff, and it commits nothing and rewrites no ref (CLAUDE.md's HARD RULE and the update's own design). A moved HEAD, branch or tag is a source that moved without the owner asking"
    return 0
  fi
  if [ "${#lefts[@]}" -gt 0 ]; then
    fact source UNVERIFIED "'fugaro update --repo' exited 1 with a step only YOU can take left in $(printf '%s; ' "${lefts[@]}" | sed 's/; $//'), so its update did not finish, and a checkout whose update did not finish is one whose before/after state this check cannot read as 'nothing moved': the MIGRATION IS NOT MEASURED there. Every checkout it did finish, it left alone. Run 'fugaro update --repo <checkout>' yourself, in your own terminal, for each of those named above, answer what it asks, and run this check again — this check redirects the door's output so it can never wait for an answer, which is exactly why a step that needs a person is left rather than asked"
    return 0
  fi
  local extra=""
  if [ "${#lines[@]}" -gt 0 ]; then
    extra=". In the working tree it $(printf '%s; ' "${lines[@]}" | sed 's/; $//')"
  fi
  fact source PASS "'fugaro update --repo' moved nothing in any of the ${#REPOS[@]} checkout(s): HEAD, the branch, HEAD's tree and every reference of each are identical before and after its update${extra}"
}

step_premise
step_update
step_policy
step_source
REACHED_END=yes
