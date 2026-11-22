#!/usr/bin/env bash
# Check 640's queue step — ONE approved task for the installation's daemon to
# launch, queued by ONE command YOU run.
#
# Check 640 (scripts/live-checks/check640.sh) reads the labels the daemon writes
# on the pull request of a run it launched, so it needs the daemon to have
# launched one APPROVED task of its own. This script queues that task: a docs-only
# probe item of a few words' work (one true typo, or one clarifying sentence, in
# docs/forge-labels/implementation-plan.md), on the repository the daemon already
# serves. It does the three things that otherwise take three commands:
#
#   1. reads the state's revision (`fugaro orchestrator state --json`);
#   2. upserts the item with `fugaro orchestrator patch -f` (a session may send
#      that op, so it needs no confirmation);
#   3. approves it with a second `fugaro orchestrator patch -f`, carrying the
#      item's spec digest (scripts/orchdigest computes it through the state's own
#      Apply, so it is the digest the daemon computes).
#
# THE APPROVAL IS YOURS. The approve op is a person's op: `fugaro orchestrator
# patch` stops at your terminal and asks you to type the project's name, and it
# refuses a pipe, --json and a coding agent's session. This script never types
# that answer and never goes around it: its second `patch` inherits your
# terminal, and what you type there is the approval. Read the item the script
# prints before you type.
#
# Usage:      bash scripts/live-checks/check640-queue-probe.sh \
#               [--project <name>] [--dry-run]
#             --project  the project whose orchestrator is written (default
#                        `fugaro`); `belong` is refused by name.
#             --dry-run  reads the state and prints the item, its digest and the
#                        two patches' shape; it writes nothing.
#             Run it from a checkout of Fugaro's own repository (it runs
#             `go run ./scripts/orchdigest`).
# Needs:      bash, mktemp, python3, go, fugaro.
# Costs:      nothing from this script. The task it queues is one trivial run
#             once the daemon launches it; the gateway enforces the run's cap
#             and the brief names none.
# Runs:       by YOU, at your own terminal. An agent session (CLAUDECODE and the
#             like) is refused, and so is a shell with tracing on. Its test is
#             scripts/live-checks/check640-queue-probe_test.sh, against fakes.
# Cleanup:    its scratch directory (mode 0700, patch files mode 0600) is
#             removed on exit, Ctrl-C included. A re-run is safe: the item id is
#             one per day, and an upsert that changes nothing changes nothing.
# Then:       run `bash scripts/live-checks/check640.sh --installation fugaro`
#             once the daemon has launched the item (a few minutes).
#
# No secret is read or printed: every value here is a project name, an item id,
# a revision or a digest.
set -Eeuo pipefail

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
die() { say "$*"; exit 2; }

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$here/../.." && pwd)

PROJECT=${CHECK640_PROBE_PROJECT:-fugaro}
DRY_RUN=${CHECK640_PROBE_DRY_RUN:-no}
while [ $# -gt 0 ]; do
  case "$1" in
  --project)
    [ $# -ge 2 ] || die "--project needs a name"
    PROJECT=$2
    shift 2
    ;;
  --project=*)
    PROJECT=${1#--project=}
    shift
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed '$d' >&2
    exit 0
    ;;
  *) die "unknown argument '$1' (see --help)" ;;
  esac
done

[ "$XTRACE_AT_START" = no ] || die "this shell has tracing on (set -x); it would print every command, so run it again without it"

for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
  [ -z "${!name:-}" ] || die "${name} is set, so this is a coding agent's session: approving a task is a person's act at a real terminal, and this script is yours to run"
done

case "$(printf '%s' "$PROJECT" | tr '[:upper:]' '[:lower:]')" in
belong) die "the project 'belong' is refused by name: this probe is for the dogfood installation" ;;
esac
case "$PROJECT" in
"" | *[!a-zA-Z0-9._-]*) die "the project name '$PROJECT' is not a name" ;;
esac

for tool in mktemp python3 go fugaro; do
  command -v "$tool" >/dev/null 2>&1 || die "this script needs '$tool' on PATH"
done

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk640q.XXXXXXXX") || die "cannot make a scratch directory"
chmod 700 "$SCRATCH"
trap 'rm -rf "$SCRATCH"' EXIT
trap 'exit 130' INT TERM
umask 077

DAY=${CHECK640_PROBE_DATE:-$(date +%Y%m%d)}
ITEM_ID="chk640-probe-$DAY"
TITLE="Check 640 probe: one tiny docs fix"
REPO="dimipaun/fugaro"
RECIPE="default"
TIER="trivial"
SPEC="forge-labels"
BRIEF="Fix one true typo, or add one clarifying sentence, in docs/forge-labels/implementation-plan.md. Keep it tiny: one line of the file, no other file, no reformatting. If you find no true typo, add one sentence that states a fact the file already implies. This task exists only so that a pull request is opened and labelled."

read_rev() { # read_rev FILE: the state's revision, read from the live node
  local out=$SCRATCH/state.json
  fugaro orchestrator state --project "$PROJECT" --json >"$out" || die "'fugaro orchestrator state --project $PROJECT --json' failed, so nothing was written"
  python3 - "$out" <<'PY' || die "the state document carries no revision: it is not the live node's, so a patch cannot be built from it"
import json, sys
d = json.load(open(sys.argv[1]))
rev = d.get("rev")
if not isinstance(rev, int) or isinstance(rev, bool) or d.get("source", "node") != "node":
    sys.exit(1)
print(rev)
PY
}

write_upsert() { # write_upsert REV FILE
  python3 - "$1" "$2" "$ITEM_ID" "$TITLE" "$REPO" "$RECIPE" "$TIER" "$SPEC" "$BRIEF" <<'PY'
import json, sys
rev, path, item, title, repo, recipe, tier, spec, brief = sys.argv[1:]
doc = {"base_rev": int(rev), "ops": [{"op": "work_item_upsert", "work_item": {
    "id": item, "title": title, "repo": repo, "recipe": recipe, "rigor_tier": tier,
    "spec": spec, "brief": brief, "depends_on": []}}]}
json.dump(doc, open(path, "w"), indent=2)
PY
  chmod 600 "$2"
}

write_approve() { # write_approve REV DIGEST FILE
  python3 - "$1" "$2" "$3" "$ITEM_ID" <<'PY'
import json, sys
rev, digest, path, item = sys.argv[1:]
doc = {"base_rev": int(rev), "ops": [{"op": "work_item_approve", "approve": {"id": item, "spec_digest": digest}}]}
json.dump(doc, open(path, "w"), indent=2)
PY
  chmod 600 "$3"
}

REV=$(read_rev)
say "project $PROJECT, state revision $REV"
UPSERT=$SCRATCH/upsert.json
write_upsert "$REV" "$UPSERT"
DIGEST=$(cd "$repo_root" && go run ./scripts/orchdigest <"$UPSERT") || die "the digest tool failed, so nothing was written"
case "$DIGEST" in
sha256:????????????????????????????????????????????????????????????????) ;;
*) die "the digest tool printed something that is not a digest, so nothing was written" ;;
esac

cat >&2 <<TXT

The item this queues:
  id      $ITEM_ID
  title   $TITLE
  repo    $REPO   recipe $RECIPE   rigor $TIER   spec $SPEC
  brief   $BRIEF
  digest  $DIGEST
TXT

if [ "$DRY_RUN" = yes ]; then
  say "--dry-run: nothing was written. A real run would (1) upsert the item at revision $REV, then (2) approve digest $DIGEST at your terminal, where you type the project name."
  exit 0
fi

say "step 1 of 2: upserting the item (a session may send this op)"
fugaro orchestrator patch --project "$PROJECT" -f "$UPSERT" || die "the upsert was refused (its reason is printed above); nothing is approved. Run this script again"

REV=$(read_rev)
APPROVE=$SCRATCH/approve.json
write_approve "$REV" "$DIGEST" "$APPROVE"
say "step 2 of 2: approving the item. Read it above; fugaro will ask you to type the project name ($PROJECT), and what you type is the approval"
fugaro orchestrator patch --project "$PROJECT" -f "$APPROVE" || die "the approval did not land (its reason is printed above). The item is queued but not approved; run this script again"

cat <<TXT
Queued and approved: $ITEM_ID on $PROJECT.
The daemon launches it within a few minutes (its tick is 30s; the run itself takes longer).
Next, once 'fugaro orchestrator state --project $PROJECT' shows the item running:
  bash scripts/live-checks/check640.sh --installation fugaro
TXT
