#!/usr/bin/env bash
# Check 885 — after release 2 a retired key is rejected with the fix. Run by
# YOU, after release 2 is installed.
#
# `model-credentials-routing` takes a repository's auth mode away: a model's
# source is a table entry (C1), not a key a repository writes. Release 2 retires
# the keys that mode was written with — `agent.auth` (in a repository's file and
# in a project layer's `defaults:`), `agent.oauth_token`, a craft's `access` and
# a grade's `access` in a repository's file, and `default_claude_account`,
# `stages.<stage>.claude_account` and `routing.claude` in the INSTALLATION's own
# config (X2, R3) — and rejects a document that still writes one, at `validate`,
# at `update` and at a default-branch launch, naming the key and the fix. Every
# key is measured in the document that really carries it, because a key bent
# into a document that has no such field is refused by the strict decoder for
# that reason and measures nothing. Nothing offline can settle what an
# installation does with a file that still writes one, so this check does its own
# setup and reads, row by row, one row per shape:
#
#   premise     the checkout it is pointed at is a git checkout whose committed
#               file writes NO retired key, names the installation as its
#               project, and which `fugaro validate` accepts: it is the release-2
#               state, and every row below bends a copy of ITS file. A premise
#               that does not hold is a STOP (exit 2), not a finding.
#   clean       a copy of that file, unchanged, in a checkout of its own, must
#               still validate: the control that a refusal below is about the
#               key and not about the copy.
#   agent-auth  a copy with `agent.auth` inserted must be REFUSED, naming
#               `agent.auth` and the fix. UNVERIFIED when it is refused without
#               naming the key.
#   oauth-token the same, with `agent.oauth_token` (CM29).
#   craft-access the same, with `agent.crafts.<craft>.access`.
#   grade-access the same, with `model_grades.<grade>.access`.
#   stage-account a copy of the INSTALLATION's OWN CONFIG with
#               `stages.<stage>.claude_account` inserted, resolved by `fugaro
#               validate --offline` in a checkout of the repository: the
#               per-stage account key lives in that document and nowhere else
#               (X2, R3 — a repository's file has no `stages:` block at all),
#               so that is the only document its gate can be read in.
#   default-account the same copy with `default_claude_account` inserted.
#   layer-keys  a project layer writing `defaults.agent.auth`, resolved with
#               `--project-layer`, must be REFUSED naming the key.
#   config      the installation's OWN config, the document `fugaro update`
#               writes, must carry none of the keys X2 retires: `routing.claude`,
#               `default_claude_account` or `stages.<stage>.claude_account`. A
#               scan, because for a config file there is nothing else to ask;
#               it is read-only and it prints the key names, never a value.
#   restore     the check's own scratch directory is gone.
#
# Every row is OFFLINE: `fugaro validate --offline` reads files and nothing else,
# this check reads one config file, and no row makes a cloud call, launches a run
# or mounts anything. It is read-only.
#
# Usage:      bash scripts/live-checks/check-885-retired-keys.sh \
#               --installation <name> --repo <checkout> [--config <file>] \
#               [--dry-run]
#             --dry-run       prints the plan and calls NOTHING: no fugaro, no
#                             validate, no file is read.
#             --installation  the installation's Fugaro project name, which is
#                             how the check finds the installation's own config.
#             --repo          a checkout of a repository of that installation on
#                             release 2: its committed `fugaro.yaml` is the base
#                             of every copy this check bends.
#             --config        the installation's own config file, when it is not
#                             $XDG_CONFIG_HOME/fugaro/projects/<name>.yaml.
# Needs:      bash, mktemp, python3 (every document is read by one python3
#             reader), git, fugaro. Nothing is installed, nothing is fetched and
#             nothing is called over a network.
# Where:      the checkout and the config file you name, and NOWHERE else: the
#             copies this check bends are written in its own scratch directory
#             and removed with it, so no file of yours is ever edited.
# Costs:      nothing. Nothing is launched and no cloud is called.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-885-retired-keys_test.sh) drives it
#             against a fake `fugaro` on PATH and real git checkouts, and
#             touches no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went. NOTHING ELSE IS
#             CREATED: no file of the checkout, of the layer or of the config is
#             written — every bent file is a copy inside the scratch directory.
# Output:     progress on stderr, ONE block on stdout headed CHECK 885 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 885 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 885 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 885 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 885 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS.
#
# NO secret is read at all, and none may be printed: the documents this check
# reads are a `fugaro.yaml`, a project layer and the installation's own config,
# all of them files that name models, routes and account NAMES. Every key this
# check writes into a copy is a word (`oauth`, `obsolete`, `personal`), never
# anything shaped like a credential, and its rows name keys and paths.
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
CONFIG=""
BASE=""

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

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 885: it reads your installation's own config and judges a release gate. Run it yourself: bash scripts/live-checks/check-885-retired-keys.sh --installation <name> --repo <checkout>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-885-retired-keys.sh"
  fi
}

usage() {
  cat <<'USAGE'
Check 885 — after release 2 a retired key is rejected with the fix. Run by YOU,
after release 2 is installed. Every row is offline and nothing is launched.

Usage:
  bash scripts/live-checks/check-885-retired-keys.sh \
    --installation <name> --repo <checkout> [--config <file>] [--dry-run]
  bash scripts/live-checks/check-885-retired-keys.sh --help

It copies your checkout's committed `fugaro.yaml` into a checkout of its own per
key shape, inserts one retired key into each copy, and runs `fugaro validate
--offline` on it: the key must be rejected with the fix. It does the same with a
copy of your INSTALLATION's own config, for the account keys release 2 retires
there, resolves a project layer that writes agent.auth, and finally reads your
installation's own config to say whether it still carries any of those keys at
all. Every row it prints is one criterion of the register's Check 885; the last
line of the block is the verdict and the exit status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

INSTALLATION=${CHECK885_INSTALLATION:-}
REPO=${CHECK885_REPO:-}
CONFIG=${CHECK885_CONFIG:-}
DRY_RUN=no
if [ "${CHECK885_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi

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
    REPO=$2
    shift 2
    ;;
  --config)
    [ $# -ge 2 ] || stop "--config needs the path of the installation's own config file"
    CONFIG=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  *)
    stop "unknown argument $1 (usage: check-885-retired-keys.sh --installation <name> --repo <checkout> [--config <file>], --help, or --dry-run)"
    ;;
  esac
done

dry_run_plan() {
  local where_config="$CONFIG"
  [ -n "$where_config" ] || where_config="the installation's own config, \$XDG_CONFIG_HOME/fugaro/projects/<name>.yaml"
  cat <<PLAN
Check 885 plan (nothing is called: no fugaro, no validate, no file is read)

  installation:  ${INSTALLATION:-<not given>}
  checkout:      ${REPO:-<not given>}
  config:        $where_config

What it would do, in order:

  premise      fugaro validate --offline ${REPO:-<checkout>}/fugaro.yaml
               -> the checkout must be a git checkout whose committed fugaro.yaml writes NO retired key,
               names the installation as its project, and which validate accepts: it is the release-2
               state, and every copy below is made from its file. A premise that does not hold STOPS
               the check (exit 2).
  clean        copy ${REPO:-<checkout>}/fugaro.yaml into a checkout of its own and validate it
               -> it must still validate: the control that a refusal below is about the key.
  agent-auth   the same copy with agent.auth: oauth inserted
               -> it must be REFUSED, naming agent.auth and the fix (fugaro update).
  oauth-token  the same copy with agent.oauth_token inserted        -> REFUSED, naming it.
  craft-access the same copy with agent.crafts.coder.access inserted -> REFUSED, naming it.
  grade-access the same copy with model_grades.strong.access        -> REFUSED, naming it.
  stage-account a copy of the config above with stages.implement.claude_account inserted, resolved in a
               scratch XDG_CONFIG_HOME as the installation's own config  -> REFUSED, naming it.
  default-account the same with default_claude_account inserted     -> REFUSED, naming it.
  layer-keys   a project layer writing defaults.agent.auth, resolved with
               fugaro validate --offline --project-layer <file> (in a copy's own checkout)
               -> it must be REFUSED, naming the key.
  config       read $where_config
               -> it must carry none of routing.claude, default_claude_account or
               stages.<stage>.claude_account: the keys release 2 retires in the installation's config.
  restore      the check's own scratch directory is removed.

Nothing is launched, no cloud is called, and no file of yours is written: every bent file is a copy
inside the check's scratch directory.
PLAN
}

if [ "$DRY_RUN" = no ]; then
  [ -n "$INSTALLATION" ] || stop "no --installation was given (or --help for the usage)"
  [ -n "$REPO" ] || stop "no --repo was given (or --help for the usage)"
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

if [ -z "$CONFIG" ]; then
  CONFIG=${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/${INSTALLATION}.yaml
fi

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk885.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk885-mine"
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
  printf '\nCHECK 885 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 885 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the release"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: the checkout's own file, the copies this check bent, the layer it resolved, each validation's own output, and the keys the installation's own config was read for). No file of yours was written: every bent file was a copy in there"
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

# retired_keys FILE prints the retired keys a `fugaro.yaml` writes, one per line:
# `agent.auth` and `agent.oauth_token` under the `agent` block,
# `agent.crafts.<craft>.access` under it, and `model_grades.<grade>.access` at
# the top level (X2's list; the fields are internal/config's — config.Agent.Auth,
# config.Agent.OAuthToken, config.Craft.Access and the grades' — and a
# `providers.<name>.auth`, which is NOT retired, is skipped by reading the
# section each key sits in rather than the key's name alone). It is a scan of
# the file's indentation, and it is only ever a PREMISE here: the rows that
# measure a refusal read `fugaro validate`'s own answer.
retired_keys() { # retired_keys <file>
  python3 - "$1" <<'PY'
import re, sys
try:
    lines = open(sys.argv[1]).read().splitlines()
except Exception:
    sys.exit(0)
sec = sub = subsub = ""
found = []
for line in lines:
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    indent = len(line) - len(line.lstrip())
    m = re.match(r"\s*([A-Za-z0-9_.-]+):\s*(.*?)\s*(?:#.*)?$", line)
    if not m:
        continue
    key, value = m.group(1), m.group(2).strip()
    if indent == 0:
        sec, sub, subsub = key, "", ""
        continue
    if key == "access" and value:
        if sec == "agent" and sub == "crafts" and subsub:
            found.append("agent.crafts.%s.access" % subsub)
        elif sec == "model_grades" and sub:
            found.append("model_grades.%s.access" % sub)
        continue
    if indent <= 2:
        sub, subsub = key, ""
        if sec == "agent" and key in ("auth", "oauth_token") and value:
            found.append("agent." + key)
        continue
    if indent <= 4:
        subsub = key
        continue
print("\n".join(found))
PY
}

# config_keys FILE prints the keys release 2 retires in the INSTALLATION's own
# config, one per line: `routing.claude`, `default_claude_account` and
# `stages.<stage>.claude_account` (X2: the per-stage account keys and the
# routing switch live in that document alone — a repository's file may not hold
# them — and `fugaro update` is what takes them out of it). It reads names, and
# every line it prints is a key's own name.
config_keys() { # config_keys <file>
  python3 - "$1" <<'PY'
import re, sys
try:
    lines = open(sys.argv[1]).read().splitlines()
except Exception:
    sys.exit(0)
sec = sub = ""
found = []
for line in lines:
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    indent = len(line) - len(line.lstrip())
    m = re.match(r"\s*([A-Za-z0-9_.-]+):\s*(.*?)\s*(?:#.*)?$", line)
    if not m:
        continue
    key, value = m.group(1), m.group(2).strip()
    if indent == 0:
        sec, sub = key, ""
        if key == "default_claude_account" and value:
            found.append("default_claude_account")
        continue
    if sec == "routing" and indent <= 2 and key == "claude":
        found.append("routing.claude")
        continue
    if sec == "stages" and indent <= 2:
        sub = key
        continue
    if sec == "stages" and sub and key == "claude_account" and value:
        found.append("stages.%s.claude_account" % sub)
        continue
print("\n".join(found))
PY
}

# project_of FILE prints the `project:` a `fugaro.yaml` names, or "" when it
# names none. It is what keeps the installation-config rows honest:
# `fugaro validate` selects the installation's own config by the project this
# checkout names, so a checkout of ANOTHER project is one whose bent config the
# command would never read, and a row about it would be a row about nothing.
project_of() { # project_of <file>
  python3 - "$1" <<'PY'
import re, sys
try:
    lines = open(sys.argv[1]).read().splitlines()
except Exception:
    sys.exit(0)
for line in lines:
    m = re.match(r"project:\s*(.*?)\s*(?:#.*)?$", line)
    if m:
        print(m.group(1).strip().strip("\"'"))
        break
PY
}

# insert_key BASE OUT PATH VALUE writes BASE with the key at the dotted PATH set
# to VALUE, as a copy. It walks the sections the path names and adds only the
# segments that are missing, so a file that already has `agent:` (or `agent:`
# and `crafts:`) gains the leaf and not a second block. It is how every bent
# copy below is written, and it never touches the file it reads.
insert_key() { # insert_key <base> <out> <dotted.path> <value>
  python3 - "$1" "$2" "$3" "$4" <<'PY'
import re, sys
base, out, path, value = sys.argv[1:5]
segs = path.split(".")
try:
    lines = open(base).read().splitlines()
except Exception:
    lines = []
# The deepest segment of the path that already exists as a block, and the line
# it is on: everything after it is added right there, at its own indentation.
# A missing section is appended at the end of the file instead.
anchor = -1
matched = 0
search_from = 0
for depth, seg in enumerate(segs[:-1]):
    want = depth * 2
    found = -1
    for i in range(search_from, len(lines)):
        line = lines[i]
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        if indent < want:
            break
        if indent == want and re.match(r"^%s\s*:" % re.escape(seg), line.strip()):
            found = i
            break
    if found < 0:
        break
    anchor = found
    matched = depth + 1
    search_from = found + 1
added = []
for depth in range(matched, len(segs)):
    seg = segs[depth]
    if depth == len(segs) - 1:
        added.append("%s%s: %s" % ("  " * depth, seg, value))
    else:
        added.append("%s%s:" % ("  " * depth, seg))
if anchor >= 0:
    lines[anchor + 1:anchor + 1] = added
else:
    if lines and lines[-1].strip():
        lines.append("")
    lines.extend(added)
open(out, "w").write("\n".join(lines) + "\n")
PY
}

# scratch_checkout NAME writes a real git checkout in the scratch directory
# with the checkout's own committed `fugaro.yaml` (or another file) in it, and
# prints its path. Each one is a checkout of its own so that `fugaro validate`
# reads it as it would read any checkout, and nothing of the owner's is touched.
scratch_checkout() { # scratch_checkout <name> [file]
  local dir="$SCRATCH/$1"
  mkdir -p "$dir"
  git -C "$dir" init -q 2>/dev/null || true
  git -C "$dir" config user.email check885@example.invalid 2>/dev/null || true
  git -C "$dir" config user.name check885 2>/dev/null || true
  if [ -n "${2:-}" ] && [ -f "${2:-}" ]; then
    cp "$2" "$dir/fugaro.yaml"
  else
    cp "$BASE" "$dir/fugaro.yaml"
  fi
  printf 'check885\n' >"$dir/README.md"
  git -C "$dir" add -A 2>/dev/null || true
  git -C "$dir" commit -qm check885 2>/dev/null || true
  printf '%s' "$dir"
}

# validate_run OUT DIR [extra args...] runs `fugaro validate --offline` in a
# scratch checkout and prints its exit status. --offline is what makes every row
# of this check offline: it reads the files and no node. The positional argument
# is the FILE and not the directory: `fugaro validate [path]` takes the
# fugaro.yaml to read (config.ReadRegular refuses a directory — "is not a
# regular file"), so a directory here would be refused for being a directory and
# every row below would read that refusal instead of the retired-key one.
validate_run() { # validate_run <out> <dir> [args...]
  local out=$1 dir=$2
  shift 2
  local rc=0
  (cd "$dir" && fugaro validate --offline "$@" "$dir/fugaro.yaml") >"$out" 2>&1 || rc=$?
  printf '%s' "$rc"
}

# validate_run_xdg is validate_run with the whole command run under another
# XDG_CONFIG_HOME, which is how a bent copy of the INSTALLATION's own config is
# put in front of `fugaro validate` without touching the owner's own: the
# command selects that config as $XDG_CONFIG_HOME/fugaro/projects/<project>.yaml,
# and the checkout it validates is what names the project.
validate_run_xdg() { # validate_run_xdg <out> <dir> <xdg> [args...]
  local out=$1 dir=$2 xdg=$3
  shift 3
  local rc=0
  (cd "$dir" && XDG_CONFIG_HOME="$xdg" fugaro validate --offline "$@" "$dir/fugaro.yaml") >"$out" 2>&1 || rc=$?
  printf '%s' "$rc"
}

# refusal_names FILE DOTTED prints `yes` when a refusal's own output names the
# key it refused — the whole dotted path or its last segment — and `no`
# otherwise. It is how a row tells "the key was refused" from "this file was
# refused for another reason".
refusal_names() { # refusal_names <file> <dotted>
  local tail_of=${2##*.}
  if grep -qF -- "$2" "$1" 2>/dev/null || grep -qF -- "$tail_of" "$1" 2>/dev/null; then
    printf 'yes'
  else
    printf 'no'
  fi
}

# ------------------------------------------------------------- the check's rows

# step_premise refuses to measure anything until the checkout is the release-2
# state: every copy below is made from ITS file, so a file that still writes a
# retired key, or a checkout validate does not accept, would make every row
# below a row about something else.
step_premise() {
  say "reading ${REPO}'s fugaro.yaml and validating the checkout (premise)"
  if [ ! -d "$REPO" ] || [ ! -d "$REPO/.git" ]; then
    fact premise FAIL "${REPO} is not a git checkout (it carries no .git directory): --repo is a checkout of a repository of this installation, and every copy below is a copy of ITS fugaro.yaml. Point --repo at one"
    stop "${REPO} is not a git checkout"
  fi
  local keys rc first
  BASE="$SCRATCH/base.yaml"
  if [ ! -s "$REPO/fugaro.yaml" ]; then
    fact premise FAIL "the checkout ${REPO} carries no fugaro.yaml at its root: every row below bends a copy of that file, so nothing is measured without it. Point --repo at a checkout that carries one"
    stop "the checkout carries no fugaro.yaml"
  fi
  cp "$REPO/fugaro.yaml" "$BASE"
  keys=$(retired_keys "$BASE" || true)
  if [ -n "$keys" ]; then
    fact premise FAIL "the fugaro.yaml of ${REPO} still writes a retired key (${keys//$'\n'/, }): this check's own copies are made from that file, and a file that already writes one is no control for a refusal. Commit the removal 'fugaro update' offers (release 2 rejects a file that still writes one), or point --repo at a checkout it has reached"
    stop "the checkout's fugaro.yaml still writes a retired key"
  fi
  local named
  named=$(project_of "$BASE" || true)
  if [ "$named" != "$INSTALLATION" ]; then
    fact premise FAIL "the checkout ${REPO} names no project, or names '${named}' where this check is about the installation '${INSTALLATION}': the two rows that bend YOUR OWN CONFIG are read by 'fugaro validate', which selects that config by the project this checkout names, so a bent copy under another project's name is never read and those rows would measure nothing. Point --repo at a checkout of a repository of ${INSTALLATION}, or give the installation that checkout belongs to"
    stop "the checkout does not name the installation as its project"
  fi
  rc=$(validate_run "$SCRATCH/premise.out" "$REPO")
  first=$(head -1 "$SCRATCH/premise.out" 2>/dev/null || true)
  if [ "$rc" -ne 0 ]; then
    fact premise FAIL "the fugaro.yaml of ${REPO} writes no retired key, but this Fugaro does not validate the checkout (exit ${rc}): ${first}. Every row below is about what validate says of a BENT copy of this file, and a checkout validate refuses for another reason is one whose refusals cannot be read as this release's retired-key gate. Fix what it names, or point --repo at a checkout it accepts"
    stop "the checkout does not validate"
  fi
  fact premise PASS "${REPO} is the release-2 state: its fugaro.yaml writes no retired key and this Fugaro validates the checkout (exit 0). Every copy below is bent from that file"
}

# step_clean validates an UNBENT copy of the file. It is the control: a validate
# that refused everything would pass every row below without measuring anything.
step_clean() {
  say "validating an unbent copy of the file (clean)"
  local dir rc first
  dir=$(scratch_checkout clean || true)
  rc=$(validate_run "$SCRATCH/clean.out" "$dir")
  first=$(head -1 "$SCRATCH/clean.out" 2>/dev/null || true)
  if [ -z "$dir" ] || [ ! -f "$dir/fugaro.yaml" ]; then
    fact clean UNVERIFIED "this check could not write its own copy of the checkout's file, so whether a refusal below is about the key rather than about the copy is NOT measured"
    return 0
  fi
  if [ "$rc" -ne 0 ]; then
    fact clean FAIL "this check's own unbent copy of the checkout's committed file was REFUSED (exit ${rc}): ${first}. The copy is byte for byte the file the checkout itself validates, so a refusal here is about the check's own checkout and not about a retired key, and every row below reads a bent copy of the same file"
    return 0
  fi
  fact clean PASS "an unbent copy of the checkout's committed file, in a checkout of its own, validates (exit 0): a refusal below is about the key this check inserted and not about the copy"
}

# step_shape runs one bent copy through `fugaro validate --offline` and reads
# what it says about the key. One row per shape X2 retires in a `fugaro.yaml`.
step_shape() { # step_shape <row> <scratch-name> <dotted> <value>
  local row=$1 name=$2 dotted=$3 value=$4 dir rc named first
  say "validating a copy that writes ${dotted} (${row})"
  dir=$(scratch_checkout "$name" || true)
  if [ -z "$dir" ]; then
    fact "$row" UNVERIFIED "this check could not write its own copy of the file for ${dotted}, so whether release 2 rejects it is NOT measured"
    return 0
  fi
  insert_key "$BASE" "$dir/fugaro.yaml" "$dotted" "$value"
  rc=$(validate_run "$SCRATCH/$name.out" "$dir")
  first=$(head -1 "$SCRATCH/$name.out" 2>/dev/null || true)
  if [ "$rc" -eq 0 ]; then
    fact "$row" FAIL "a file writing ${dotted} was not refused: 'fugaro validate --offline' ACCEPTED it (exit 0). Release 2 rejects the keys it retired, with the fix (X2, CM29), and a file that validates is a file a launch from the default branch will run on a key nothing reads any more. If this installation is on release 1, this is the release the check is waiting for and not a defect"
    return 0
  fi
  named=$(refusal_names "$SCRATCH/$name.out" "$dotted")
  if [ "$named" = no ]; then
    fact "$row" UNVERIFIED "the file writing ${dotted} was refused (exit ${rc}), but its own output names none of the retired keys ('${dotted}' or '${dotted##*.}'), so whether this refusal is the retired key's is NOT measured: ${first}. Read it yourself and record it in the register's section"
    return 0
  fi
  if ! grep -qi -- 'update' "$SCRATCH/$name.out"; then
    fact "$row" FAIL "the file writing ${dotted} is refused (exit ${rc}) but the refusal names no fix: release 2's refusal offers the removal 'fugaro update' writes as a diff (X2), and an owner who is not told how to have it removed is stopped with no way forward. The refusal was: ${first}"
    return 0
  fi
  fact "$row" PASS "'fugaro validate --offline' refused a file writing ${dotted} (exit ${rc}), naming the key and the command that fixes it ('fugaro update', whose removal the owner commits himself). Its first line: ${first}"
}

# step_layer resolves a project layer that writes a retired key. A project layer
# is the one document whose key is `defaults.agent.auth` (config.Scopes: agent.auth
# is retired in a repository's file AND in a project layer, and a layer writes it
# under `defaults:`), and it is the layer's own gate this row reads.
step_layer() {
  say "resolving a project layer that writes a retired key (layer-keys)"
  local dotted=defaults.agent.auth dir layer rc named first
  layer="$SCRATCH/layer.yaml"
  cat >"$layer" <<'YAML'
defaults:
  agent:
    auth: oauth
YAML
  dir=$(scratch_checkout layer || true)
  if [ -z "$dir" ]; then
    fact layer-keys UNVERIFIED "this check could not write its own project layer or the checkout it resolves in, so whether a layer writing ${dotted} is rejected is NOT measured"
    return 0
  fi
  rc=$(validate_run "$SCRATCH/layer.out" "$dir" --project-layer "$layer")
  first=$(head -1 "$SCRATCH/layer.out" 2>/dev/null || true)
  if [ "$rc" -eq 0 ]; then
    fact layer-keys FAIL "a project layer writing ${dotted} was not refused: 'fugaro validate --offline --project-layer' ACCEPTED it (exit 0). Release 2 rejects agent.auth in a project layer as it does in a repository's file (X2, R3: config.Scopes retires it in both), and a layer that resolves is one every repository of the project is resolved over. If this installation is on release 1, this is the release the check is waiting for and not a defect"
    return 0
  fi
  named=$(refusal_names "$SCRATCH/layer.out" "$dotted")
  if [ "$named" = no ]; then
    fact layer-keys UNVERIFIED "the layer writing ${dotted} was refused (exit ${rc}), but its own output names none of the retired keys ('${dotted}' or '${dotted##*.}'), so whether this refusal is the retired key's is NOT measured: ${first}. Read it yourself and record it in the register's section"
    return 0
  fi
  if ! grep -qi -- 'update' "$SCRATCH/layer.out"; then
    fact layer-keys FAIL "the layer writing ${dotted} is refused (exit ${rc}) but the refusal names no fix: release 2's refusal says what command removes it (X2). The refusal was: ${first}"
    return 0
  fi
  fact layer-keys PASS "a project layer writing ${dotted} is refused (exit ${rc}), naming the key and the fix. Its first line: ${first}"
}

# step_config_key measures the retirement of one key of the INSTALLATION's own
# config: a copy of that document, carrying the key, is put where `fugaro
# validate` selects it ($XDG_CONFIG_HOME/fugaro/projects/<project>.yaml) and the
# command is run in a checkout of the repository, which is what names the
# project. The per-stage account keys and default_claude_account live in that
# document and nowhere else (X2, R3 — a repository's file has no `stages:` block,
# and a project layer may not hold an account name at all), so a copy of it is
# the only way a refusal of one can be measured rather than scanned for. The
# value it writes is `default`, the account every stage uses when it names none
# (config.DefaultClaudeAccount): a value the key's own rule admits, so a refusal
# is about the KEY and not about what the copy says.
step_config_key() { # step_config_key <row> <dotted> <value>
  local row=$1 dotted=$2 value=$3 xdg dir rc named first mine already
  if [ ! -r "$CONFIG" ]; then
    fact "$row" UNVERIFIED "the installation's own config (${CONFIG}) could not be read, so whether release 2 rejects ${dotted} in it is NOT measured. Give it with --config and run the check again"
    return 0
  fi
  already=$(config_keys "$CONFIG" || true)
  if [ -n "$already" ]; then
    fact "$row" UNVERIFIED "the installation's own config (${CONFIG}) already carries ${already//$'\n'/, } (the 'config' row below reads it), so a copy of it is no control for whether release 2 rejects ${dotted}: nothing could tell the key this check writes from the one that was already there. Run 'fugaro update' first, or point --config at a config that carries none of them"
    return 0
  fi
  xdg="$SCRATCH/xdg-$row"
  mkdir -p "$xdg/fugaro/projects"
  insert_key "$CONFIG" "$xdg/fugaro/projects/${INSTALLATION}.yaml" "$dotted" "$value"
  mine=$(config_keys "$xdg/fugaro/projects/${INSTALLATION}.yaml" || true)
  if [ "$mine" != "$dotted" ]; then
    fact "$row" UNVERIFIED "this check could not write its own copy of ${CONFIG} carrying ${dotted} alone (the copy reads back as '${mine}'), so whether release 2 rejects that key is NOT measured"
    return 0
  fi
  dir=$(scratch_checkout "$row" || true)
  if [ -z "$dir" ]; then
    fact "$row" UNVERIFIED "this check could not write the checkout ${dotted} is resolved in, so whether release 2 rejects it is NOT measured"
    return 0
  fi
  rc=$(validate_run_xdg "$SCRATCH/$row.out" "$dir" "$xdg")
  first=$(head -1 "$SCRATCH/$row.out" 2>/dev/null || true)
  if [ "$rc" -eq 0 ]; then
    fact "$row" FAIL "an installation config writing ${dotted} was not refused: 'fugaro validate --offline' ACCEPTED it (exit 0). Release 2 rejects the account keys it retired in the installation's own config, with the fix (X2, R3), and a config that validates is one whose stage accounts a run still reads out of a document nothing honours. If this installation is on release 1, this is the release the check is waiting for and not a defect"
    return 0
  fi
  named=$(refusal_names "$SCRATCH/$row.out" "$dotted")
  if [ "$named" = no ]; then
    fact "$row" UNVERIFIED "the config writing ${dotted} was refused (exit ${rc}), but its own output names none of the retired keys ('${dotted}' or '${dotted##*.}'), so whether this refusal is the retired key's is NOT measured: ${first}. Read it yourself and record it in the register's section"
    return 0
  fi
  if ! grep -qi -- 'update' "$SCRATCH/$row.out"; then
    fact "$row" FAIL "the config writing ${dotted} is refused (exit ${rc}) but the refusal names no fix: release 2's refusal says what command removes it (X2), and 'fugaro update' is the command that migrates this document. The refusal was: ${first}"
    return 0
  fi
  fact "$row" PASS "'fugaro validate --offline' refused the installation's own config writing ${dotted} (exit ${rc}), naming the key and the fix. Its first line: ${first}"
}

# step_config reads the installation's OWN config for the keys release 2 retires
# in it. It is a scan of a file's own names — for a config file there is nothing
# else to ask — and it prints the key names and never a value.
step_config() {
  say "reading the installation's own config for the keys release 2 retires (config)"
  if [ ! -r "$CONFIG" ]; then
    fact config UNVERIFIED "the installation's own config (${CONFIG}) could not be read, so whether it still carries routing.claude, default_claude_account or a stage's account key is NOT measured. Give it with --config and run the check again"
    return 0
  fi
  local keys
  keys=$(config_keys "$CONFIG" || true)
  if [ -n "$keys" ]; then
    fact config FAIL "the installation's own config (${CONFIG}) still carries ${keys//$'\n'/, }: release 2 retires those keys and 'fugaro update' is what takes them out of that document (X2, X3), so an installation carrying one was not migrated by the update this check follows. Run 'fugaro update' in your own terminal and read this row again"
    return 0
  fi
  fact config PASS "the installation's own config (${CONFIG}) carries none of the keys release 2 retires there (routing.claude, default_claude_account, stages.<stage>.claude_account): the update that brought this installation to release 2 took them out"
}

step_premise
step_clean
step_shape agent-auth bent-agent-auth agent.auth oauth
step_shape oauth-token bent-oauth-token agent.oauth_token obsolete
step_shape craft-access bent-craft-access agent.crafts.coder.access oauth
step_shape grade-access bent-grade-access model_grades.strong.access oauth
step_config_key stage-account stages.implement.claude_account default
step_config_key default-account default_claude_account default
step_layer
step_config
REACHED_END=yes
