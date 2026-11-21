#!/usr/bin/env bash
# Check 771 — a member's job account, on a real deployment: admitted, reading
# the member's database with `fugaroWorkspaceRead` alone, refused at the
# workspace's ceiling, and refused within a minute of the member being removed.
#
# Plan task WS20 writes it (docs/workspace-service/implementation-plan.md §8);
# no task of that plan runs it. What it settles is U3 of
# docs/workspace-service/reconciliation.md: whether a database read through the
# `fugaroWorkspaceRead` custom role — `firebasedatabase.instances.get` and
# nothing else — is enough for the workspace's service to read a member's policy
# and its run state on a real deployment. It is the workspaces design's own U1,
# and only a real deployment can answer it: the fake in the tests admits what a
# fake is told to admit.
#
# WHAT THIS SCRIPT IS, AND WHAT IT IS NOT. It creates and deletes NOTHING: every
# read is a `describe`, a role read, an IAM read and a Cloud Logging listing,
# and the calls this check is about are the owner's own — a member's run against
# the workspace's service, one call made at the ceiling, and one call made from
# a member's job account after that member was removed. What the script
# measures is the evidence those calls left:
#
#   * the member-side role: `fugaroWorkspaceRead` carries exactly
#     `firebasedatabase.instances.get` and nothing else, which is the "alone"
#     half of U3, and it is bound to the workspace service's own account in this
#     project (deploy/terraform/gcp/modules/firebase/workspace_access.tf);
#   * the service's own lines: an admission (`credsvc: audit`) naming a member's
#     job account, and any refusal (`credsvc: refused`) a member call met. A
#     refusal whose read label is the member's policy or state is U3 answered NO
#     — the get-only role was not enough to read what the service reads;
#   * the refusal at the ceiling, and the refusal within a minute of the member
#     being removed. Both are calls the owner makes; the check reads the log at
#     the moment they leave behind (CHECK771_CEILING_AT, CHECK771_REMOVED_AT).
#
# Usage:      bash scripts/live-checks/check771.sh
#             bash scripts/live-checks/check771.sh --dry-run
#             bash scripts/live-checks/check771.sh --help
# Needs:      bash, mktemp, python3, gcloud (authenticated as an OWNER of BOTH
#             projects: the member's, where the role and the IAM policy are read,
#             and the workspace's own, where its service and its log are) and
#             fugaro (the guards).
# Where:      TWO projects, because the rows read two: the workspace's Cloud Run
#             service and its log live in the WORKSPACE's own project (the
#             project its own root deploys, deploy/terraform/gcp/roots/
#             workspace/main.tf), which is named with CHECK771_WORKSPACE_PROJECT
#             — there is no default, because the member's project carries no
#             such service — and the member-side role and the binding that holds
#             it live in the MEMBER's project, which for the dogfood installation
#             is fugaro-dev. The region is us-east5, hard-coded in this file. A
#             project, region, account, service or role naming belong is refused
#             by name.
# Creates:    nothing. The copies of the role, the IAM policy, the service
#             document and the log entries this run reads sit in a scratch
#             directory (mode 0700) removed from the exit trap even on a
#             failure or Ctrl-C.
# Costs:      nothing billable. The calls this check reads are the owner's own.
# Runs:       by YOU, after those calls — and no agent runs any step of it. Its
#             own test (check771_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else.
# Output:     progress on stderr, ONE block on stdout headed CHECK 771 RESULT,
#             then CHECK 771 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure. In the block, a key the run never reached
#             reads `FAIL — not reached`.
#
# Knobs (all CHECK771_*): DRY_RUN, KEEP_SCRATCH, WORKSPACE_PROJECT (the
# workspace's own GCP project, the one its service and its log live in;
# REQUIRED, there is no default), MEMBER_PROJECT (the member's GCP project, where
# the role and its binding are read; fugaro-dev, the installation the guard
# checks), SERVICE, INSTALLATION, WINDOW_MINUTES (how far back the log is read,
# 60 by default), ROLE (the member-side custom role, `fugaroWorkspaceRead`),
# JOB_ACCOUNT (the member's job service account the admission row looks for;
# unset, any job account will do), CEILING_AT and REMOVED_AT (the moment you made
# the call at the ceiling, and the moment you removed the member — epoch seconds
# or RFC 3339; unset, the two rows are not measured), GRACE_SECONDS (how long a
# refusal may take after that moment, 60 by default).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this run reads an IAM
# policy, and the shell it is started from may be a login shell whose trace ends
# up in a log.
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
die() { say "$*"; exit 2; }

stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 771 needs bash"
  local t
  for t in mktemp python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 771 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk771-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
SERVICE_URL=""
REVISION=""
SERVICE_ACCOUNT=""
ADMIN_ACCOUNT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk771.XXXXXXXX") || die "Check 771 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  write_reader
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard keeps the removal from ever reaching the owner's home,
  # and reading it unguarded would abort cleanup under `set -u` on a machine
  # where HOME is unset, leaving the scratch directory behind.
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  if [ -n "$KEEP_SCRATCH" ]; then
    say "CHECK771_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created and nothing was deleted by this check: no member, no job, no binding, no grant, no revision. The member you removed, the cap you raised to its ceiling and the credential you unshared stay exactly as you left them, and this check changes none of them"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    [ "$verdict" = FAIL ] || continue
    case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
    failed=yes
  done <<<"$facts"
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then status=1; fi
  if [ -n "$UNMEASURED_KEYS" ] && [ "$status" = 0 ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    status=2
  fi
  print_block "$facts" "$status"
  exit "$status"
}

# ------------------------------------------------------------------- the tools
# Two projects, two helpers: the workspace's project carries the service and its
# log, the member's carries the role and the binding. A read that went to the
# wrong one would be a reading of something else, so each call names its own.
gcloud_member() { gcloud --project="$P" --quiet "$@"; }
gcloud_workspace() { gcloud --project="$WP" --quiet "$@"; }

ADMITTED_LINE_MARK='credsvc: audit'
REFUSED_LINE_MARK='credsvc: refused'

# read_log FILTER DEST: the service's own lines, over the window.
read_log() { # read_log FILTER DEST — the workspace's service's log
  gcloud_workspace logging read "$1" --freshness="${WINDOW_MINUTES}m" --limit=2000 --format=json \
    >"$2" 2>"$SCRATCH/logs.err" || true
}

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1; }
lines_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p"; }
last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 771's reader: a role definition, an IAM policy, the service document
and the service's own log lines. None of them carries a credential.

role     argv: `gcloud iam roles describe --format=json`'s file. Prints the
         role's name, its stage, and one `permission=` line per permission,
         sorted. A role that states no permission prints none, which is a
         reading a caller has to be able to tell from "the read failed".
policy   argv: `gcloud projects get-iam-policy --format=json`'s file, the role's
         short id. Prints one `binding=` line per member bound to that role, the
         full role name before the member: `<role> <member>`.
service  argv: `gcloud run services describe --format=json`'s file. Prints the
         url, the latest ready revision and the service account the revision
         runs as — the account the member-side role is bound to.
log      argv: a log-entry listing's file. Prints, for the entries whose text
         carries one of the two marks: `line=<mark> <timestamp> <text>`, plus
         `status=<code> <timestamp>` for every HTTP request entry, and a count
         of every entry. The two marks are the service's own (internal/credsvc:
         audit.go, refusallog.go) and are passed in, so this file carries no
         package's spelling of them.
"""

import json
import re
import sys


def dig(doc, *path):
    cur = doc
    for step in path:
        if not isinstance(cur, dict) or step not in cur:
            return None
        cur = cur[step]
    return cur


def role(path):
    with open(path) as fh:
        doc = json.load(fh)
    if not isinstance(doc, dict):
        doc = {}
    print("name=" + str(doc.get("name") or ""))
    print("stage=" + str(doc.get("stage") or ""))
    permissions = doc.get("includedPermissions")
    if not isinstance(permissions, list):
        permissions = []
    for one in sorted(str(p) for p in permissions):
        print("permission=" + one)


def policy(path, role_id):
    with open(path) as fh:
        doc = json.load(fh)
    bindings = dig(doc, "bindings")
    if not isinstance(bindings, list):
        bindings = []
    for binding in bindings:
        if not isinstance(binding, dict):
            continue
        full = str(binding.get("role") or "")
        if not full.endswith("/" + role_id) and full != role_id:
            continue
        members = binding.get("members")
        if not isinstance(members, list):
            members = []
        for member in sorted(str(m) for m in members):
            print("binding=%s %s" % (full, member))


def service(path):
    with open(path) as fh:
        doc = json.load(fh)
    print("url=" + (dig(doc, "status", "url") or ""))
    print("revision=" + (dig(doc, "status", "latestReadyRevisionName") or ""))
    account = None
    containers = dig(doc, "spec", "template", "spec", "containers") or []
    if isinstance(containers, list) and containers and isinstance(containers[0], dict):
        account = containers[0].get("serviceAccountName")
    if not account:
        account = dig(doc, "spec", "template", "spec", "serviceAccountName")
    print("service_account=" + str(account or ""))


def timestamp_of(entry):
    for key in ("timestamp", "receiveTimestamp"):
        found = entry.get(key)
        if isinstance(found, str) and found:
            return found
    return ""


def text_of(entry):
    found = entry.get("textPayload")
    if isinstance(found, str):
        return found
    payload = entry.get("jsonPayload")
    if isinstance(payload, dict):
        message = payload.get("message")
        if isinstance(message, str):
            rest = []
            for key in sorted(payload):
                if key == "message":
                    continue
                rest.append("%s=%s" % (key, payload[key]))
            return message + (" " + " ".join(rest) if rest else "")
    proto = entry.get("protoPayload")
    if isinstance(proto, dict):
        return str(proto.get("status") or "") + " " + str(proto.get("methodName") or "")
    return ""


def log(path, marks):
    with open(path) as fh:
        entries = json.load(fh)
    if not isinstance(entries, list):
        entries = []
    kept = 0
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        text = re.sub(r"\s+", " ", text_of(entry)).strip()
        at = timestamp_of(entry)
        request = entry.get("httpRequest") if isinstance(entry.get("httpRequest"), dict) else {}
        status = str(request.get("status") or "")
        if status:
            print("status=%s %s" % (at, status))
        if not text:
            continue
        for mark in marks:
            if mark and mark in text:
                kept += 1
                print("line=%s %s %s" % (at, mark, text[:400]))
                break
    print("entries=%d" % len(entries))
    print("marked=%d" % kept)


def main(argv):
    if len(argv) < 3:
        print("reader: need a reading", file=sys.stderr)
        return 2
    if argv[1] == "role":
        role(argv[2])
        return 0
    if argv[1] == "policy":
        policy(argv[2], argv[3])
        return 0
    if argv[1] == "service":
        service(argv[2])
        return 0
    if argv[1] == "log":
        log(argv[2], argv[3:])
        return 0
    print("reader: unknown reading %s" % argv[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
}

# ------------------------------------------------------------------ the guards
# No agent runs any step of Check 771 (the plan says so): a coding agent's
# session is refused by the markers it carries, before anything is read.
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 771. Run it yourself: bash scripts/live-checks/check771.sh"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check771.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would carry the IAM policy it reads, member by member"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the IAM policy it reads member by member"
}

guard_project() {
  local doctor_out v
  doctor_out=$(fugaro doctor --project "$INSTALLATION" --all || true)
  if ! printf '%s\n' "$doctor_out" | grep -q '^project: .*(GCP fugaro-dev,'; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project ${INSTALLATION} --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check reads the member-side role and its binding in ${P} and the workspace's service and log in ${WP}, and never belong"
  fi
  case "${P}${WP}${L}${ACCOUNT:-}${SERVICE}${ROLE:-}" in
  *belong*) stop "the project, region, account, service or role this check names is belong: this check is never run there" ;;
  esac
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (this check reads the platform's role, policy and log, not a run: any build will do)"
  fact "project-region" PASS "${P}/${L} for the member's role and binding, ${WP}/${L} for the workspace's service and log (installation ${INSTALLATION}, sandbox only; never belong)"
}

# ------------------------------------------------------------------- the steps
step_service() {
  gcloud_workspace run services describe "$SERVICE" --region="$L" --format=json \
    >"$SCRATCH/service.json" 2>"$SCRATCH/service.err" || true
  if [ ! -s "$SCRATCH/service.json" ]; then
    die "the Cloud Run service ${SERVICE} could not be described in the workspace's own project ${WP}/${L} ($(last_line "$(cat "$SCRATCH/service.err" 2>/dev/null || true)")): this check is about what a REAL deployment admits, and there is none to read. The service is the workspace's, deployed by its own root (deploy/terraform/gcp/roots/workspace/main.tf) — not one in the member's ${P}. Run 'gcloud run services describe $SERVICE --project $WP --region $L' here yourself to read the error, name the workspace's project with CHECK771_WORKSPACE_PROJECT, or name the service with CHECK771_SERVICE"
  fi
  local out
  out=$(reader service "$SCRATCH/service.json") || die "the service document could not be read ($(last_line "$out"))"
  SERVICE_URL=$(field_of url "$out")
  REVISION=$(field_of revision "$out")
  SERVICE_ACCOUNT=$(field_of service_account "$out")
  fact service PASS "${SERVICE} in the workspace's project ${WP}/${L} at ${SERVICE_URL:-<no url>}, latest ready revision ${REVISION:-unknown}, running as ${SERVICE_ACCOUNT:-a platform-assigned account}: this is the deployment the rows below read, and every call this check is about was answered by it"
}

# step_role: U3's "alone" half. A role that carries a second permission is the
# answer U3 asks for already given, and the wrong one.
step_role() {
  gcloud_member iam roles describe "$ROLE" --format=json >"$SCRATCH/role.json" 2>"$SCRATCH/role.err" || true
  if [ ! -s "$SCRATCH/role.json" ]; then
    unmeasured role "the custom role ${ROLE} does not exist in ${P} ($(last_line "$(cat "$SCRATCH/role.err" 2>/dev/null || true)")): it is created with the member's installation (deploy/terraform/gcp/modules/firebase/workspace_access.tf) and applied by 'fugaro update' on that member. Read it yourself: gcloud iam roles describe $ROLE --project $P"
    return 0
  fi
  local out permissions
  out=$(reader role "$SCRATCH/role.json") || die "the role could not be parsed ($(last_line "$out"))"
  permissions=$(lines_of permission "$out" | tr '\n' ' ')
  if [ "${permissions% }" = "firebasedatabase.instances.get" ]; then
    fact role PASS "${ROLE} in ${P} carries exactly firebasedatabase.instances.get and nothing else ($(field_of stage "$out")): a read of a member's database through this role cannot write, cannot list and cannot reach another project's database, which is the 'alone' half of U3. Whether it is ENOUGH is the reads row below"
    return 0
  fi
  if [ -z "${permissions% }" ]; then
    unmeasured role "${ROLE} in ${P} states no permission at all, which is not the role the member's installation creates: the role is written in deploy/terraform/gcp/modules/firebase/workspace_access.tf with firebasedatabase.instances.get. Read it yourself: gcloud iam roles describe $ROLE --project $P --format=json"
    return 0
  fi
  fact role FAIL "${ROLE} in ${P} carries ${permissions% }: the member-side role is written with firebasedatabase.instances.get and NOTHING else, because a role that carries more is a grant the workspace's owner never agreed to hold on a member's database. Either the role was widened by hand — narrow it back in the Terraform and apply it — or this is another role and CHECK771_ROLE should name the right one"
}

# step_binding: the read is this check's subject, so the account it is bound to
# has to be the one the deployment runs as.
step_binding() {
  gcloud_member projects get-iam-policy "$P" --format=json >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.err" || true
  if [ ! -s "$SCRATCH/policy.json" ]; then
    unmeasured binding "the project's IAM policy could not be read ($(last_line "$(cat "$SCRATCH/policy.err" 2>/dev/null || true)")): reading it needs roles/resourcemanager.projectIamGet on ${P}, which its owner holds. Read it yourself: gcloud projects get-iam-policy $P --format=json | grep -A3 $ROLE"
    return 0
  fi
  local out bindings
  out=$(reader policy "$SCRATCH/policy.json" "$ROLE") || die "the IAM policy could not be parsed ($(last_line "$out"))"
  bindings=$(lines_of binding "$out")
  if [ -z "$bindings" ]; then
    fact binding FAIL "no member of ${P} is bound to ${ROLE}: the workspace's service reads a member's database with this role and no other grant, so with the binding gone every read it makes is refused — which is U3 answered 'not enough' for a reason that is a missing binding rather than a narrow role. Bind the service account (deploy/terraform/gcp/modules/firebase/workspace_access.tf does it when the member applies its installation)"
    return 0
  fi
  if [ -n "$SERVICE_ACCOUNT" ] && ! printf '%s\n' "$bindings" | grep -qF "serviceAccount:${SERVICE_ACCOUNT}"; then
    fact binding FAIL "${ROLE} is bound in ${P} to $(printf '%s' "$bindings" | tr '\n' '; ') and the deployment ${REVISION:-$SERVICE} runs as ${SERVICE_ACCOUNT}: the account that serves a member's call does not hold the member-side role, so the read U3 asks about is not the read this deployment makes — the binding names another account, and the one it should name is the workspace service's own"
    return 0
  fi
  fact binding PASS "${ROLE} is bound in ${P} to ${SERVICE_ACCOUNT:-$(printf '%s' "$bindings" | tr '\n' '; ')}, which is the account the deployment runs as: the read of a member's database is this one role and no other grant"
}

# step_reads: U3 answered by the service's own lines. An audit line for a member
# call with no refusal beside it is the get-only role having been enough.
step_reads() {
  read_log "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SERVICE}\"" "$SCRATCH/logs.json"
  if [ ! -s "$SCRATCH/logs.json" ]; then
    unmeasured reads "the service's log could not be read ($(last_line "$(cat "$SCRATCH/logs.err" 2>/dev/null || true)")): it is the workspace's own service's log, in ${WP}, and reading it needs roles/logging.viewer there. Read it yourself: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $WP --freshness=${WINDOW_MINUTES}m --format=json | head -50"
    return 0
  fi
  LOG_OUT=$(reader log "$SCRATCH/logs.json" "$ADMITTED_LINE_MARK" "$REFUSED_LINE_MARK") || die "the log entries could not be parsed ($(last_line "$LOG_OUT"))"
  local admitted refusals
  admitted=$(lines_of line "$LOG_OUT" | grep -F "$ADMITTED_LINE_MARK" || true)
  refusals=$(lines_of line "$LOG_OUT" | grep -F "$REFUSED_LINE_MARK" || true)
  local policy_refusals
  policy_refusals=$(printf '%s\n' "$refusals" | grep -E 'read=(policy_shared|policy_prices|selector_current|selector_marks|selector_leases)' || true)
  if [ -n "$policy_refusals" ]; then
    fact reads FAIL "the service refused a member call on a read the member-side role should have carried: $(printf '%s' "$policy_refusals" | head -n 1 | head -c 300). U3 asks exactly this and this is the answer NO — an instances.get-only role did NOT let the service read a member's policy or run state, so the member's database grants have to widen before a member's credentials can be served from the workspace. Record it in docs/workspace-service/reconciliation.md (U3) with the read that failed"
    return 0
  fi
  if [ -z "$admitted" ]; then
    unmeasured reads "the last ${WINDOW_MINUTES}m of the service's log hold no ${ADMITTED_LINE_MARK} line, so this run cannot say whether the get-only role sufficed: the evidence is a member's own call on the workspace's credentials. Run one — a 'fugaro run' on a repository whose kind the route sends to the workspace, launched as the member — and run this check inside CHECK771_WINDOW_MINUTES=${WINDOW_MINUTES}"
    return 0
  fi
  fact reads PASS "the last ${WINDOW_MINUTES}m hold $(printf '%s\n' "$admitted" | grep -c . ) admitted call(s) and no refusal on a member's policy or state: a database read through ${ROLE} (instances.get alone) was enough for the service to read what it reads. U3 is settled YES on this deployment — record it in docs/workspace-service/reconciliation.md"
}

# step_admitted: the member's job account, by name. The service looks the
# caller's account up in the workspace's own table, so an admission naming that
# account is a real deployment having admitted it.
step_admitted() {
  if [ -z "${LOG_OUT:-}" ]; then
    unmeasured admitted "not read: the log was not read (see the reads row). The evidence is one ${ADMITTED_LINE_MARK} line naming the member's job account: run a member's call and read gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $WP --freshness=${WINDOW_MINUTES}m --format=json"
    return 0
  fi
  local admitted candidates="${LOG_OUT:-}"
  admitted=$(lines_of line "$candidates" | grep -F "$ADMITTED_LINE_MARK" || true)
  if [ -n "$JOB_ACCOUNT" ]; then
    admitted=$(printf '%s\n' "$admitted" | grep -F "$JOB_ACCOUNT" || true)
  else
    admitted=$(printf '%s\n' "$admitted" | grep -Eo '[A-Za-z0-9._-]+@[A-Za-z0-9._-]+\.iam\.gserviceaccount\.com' || true)
  fi
  if [ -z "$admitted" ]; then
    unmeasured admitted "the last ${WINDOW_MINUTES}m hold no admitted call naming ${JOB_ACCOUNT:-a job service account}: the admission row is about the account the workspace's table carries for a member, and no member's job account appears in the window. Declare the member (fugaro workspace member add ... ) and run one of its runs, then run this check again"
    return 0
  fi
  fact admitted PASS "the service admitted a member's job account over the last ${WINDOW_MINUTES}m: $(printf '%s' "$admitted" | head -n 1 | head -c 300). The account is the member's own job service account, verified by the member's pins and looked up in the workspace's table — which is what 'admitted by a real deployment' means, and what no fake can settle"
}

# step_ceiling: one call at the ceiling, refused. Either the moment is named and
# a refusal is read inside the grace, or the window holds a refusal whose own
# words name the ceiling.
step_ceiling() {
  if [ -z "${LOG_OUT:-}" ]; then
    unmeasured ceiling "not read: the log was not read (see the reads row). Put the workspace's cap at its ceiling, make ONE call through the workspace's credentials, and read the log: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $WP --freshness=5m --format=json"
    return 0
  fi
  local refusals named in_grace
  refusals=$(lines_of line "$LOG_OUT" | grep -F "$REFUSED_LINE_MARK" || true)
  if [ -n "$CEILING_AT" ]; then
    # A refusal at the moment is not enough on its own: a member whose call was
    # refused for any other reason writes the same line, so the refusal has to
    # be the ceiling's own — its words name the ceiling.
    in_grace=$(refusals_in_grace "$refusals" "$CEILING_AT" "$GRACE_SECONDS")
    named=$(printf '%s\n' "$in_grace" | grep -i 'ceiling' || true)
    if [ -n "$named" ]; then
      fact ceiling PASS "the call made at the workspace's ceiling was refused $(grace_phrase "$CEILING_AT" "$GRACE_SECONDS"): $(printf '%s' "$named" | head -n 1 | head -c 300). A workspace's ceiling is what its owner set (fugaro workspace cap set --monthly ...), and a call that would pass it is refused rather than served — the member sees a refusal, and the workspace's own spend does not pass the ceiling"
      return 0
    fi
    if [ -n "$in_grace" ]; then
      unmeasured ceiling "a refusal was written $(grace_phrase "$CEILING_AT" "$GRACE_SECONDS"), but it does not name the ceiling: $(printf '%s' "$in_grace" | head -n 1 | head -c 300). A refusal from the service for ANY other reason — an unverified identity, a removed member, a read the role did not carry — is written the same way, so it is not the ceiling having been checked: this row reads only a refusal whose own words name the ceiling. Read the line yourself: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $WP --freshness=${WINDOW_MINUTES}m --format=json | grep -i 'ceiling\\|refused'. If the call really was at the ceiling and the refusal says something else, that is the finding this row exists to make — record it in docs/workspace-service/reconciliation.md (O2/WS8) with the line's own words"
      return 0
    fi
    unmeasured ceiling "no refusal follows the moment you named (CHECK771_CEILING_AT=${CEILING_AT}) within ${GRACE_SECONDS}s in the last ${WINDOW_MINUTES}m. Either the call was served — which is the finding, and O2/WS8's own question about where the ceiling is checked — or the moment is the wrong one, or the call never happened. Look for it: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $WP --freshness=${WINDOW_MINUTES}m --format=json | grep -i ceiling"
    return 0
  fi
  named=$(printf '%s\n' "$refusals" | grep -i 'ceiling' || true)
  if [ -n "$named" ]; then
    fact ceiling PASS "the last ${WINDOW_MINUTES}m hold a refusal that names the ceiling: $(printf '%s' "$named" | head -n 1 | head -c 300). A call that would pass the workspace's own ceiling is refused rather than served"
    return 0
  fi
  unmeasured ceiling "the last ${WINDOW_MINUTES}m hold no refusal naming the ceiling (${refusals:+$(printf '%s\n' "$refusals" | grep -c .) other refusal(s) were written}) and CHECK771_CEILING_AT was not set. Record the moment you make the call — 'date -u +%s' right before it, then CHECK771_CEILING_AT=<that> — with the workspace's cap at its ceiling (fugaro workspace cap set --workspace <name> --monthly <the month's own figure>), and run this check again"
}

# step_removed: a member removed is refused within a minute (R13: the service
# re-reads its node when it is older than 60 seconds, so no new revision is
# needed for the removal to take effect). A refusal taken before the identity is
# verified writes neither the audit line nor the refusal line — there is no
# verified run to name — so the reading is the request's own status.
step_removed() {
  if [ -z "${LOG_OUT:-}" ]; then
    unmeasured removed "not read: the log was not read (see the reads row). Remove the member (fugaro workspace member rm <workspace> <project>), make one call from that member's job account, and read the log: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $WP --freshness=5m --format=json"
    return 0
  fi
  if [ -z "$REMOVED_AT" ]; then
    unmeasured removed "CHECK771_REMOVED_AT was not set, so this run does not know when the member was removed and cannot measure how long the refusal took. Record the moment — 'date -u +%s' right before 'fugaro workspace member rm <workspace> <project>' — and run this check again as CHECK771_REMOVED_AT=<that> bash scripts/live-checks/check771.sh"
    return 0
  fi
  local codes named skip in_grace
  codes=$(lines_of status "$LOG_OUT")
  # The ceiling's own refusal is a 4xx at the service like any other, and the
  # ceiling would have refused the removed member's call whether the member was
  # still on the workspace or not — so the moments of the refusals that name the
  # ceiling are excluded: this row reads a 4xx that is not already the ceiling's.
  skip=$(ceiling_moments "$LOG_OUT")
  in_grace=$(statuses_in_grace "$codes" "$REMOVED_AT" "$GRACE_SECONDS" '^4[0-9][0-9]$')
  named=$(exclude_moments "$in_grace" "$skip")
  if [ -z "$named" ]; then
    if [ -n "$in_grace" ]; then
      unmeasured removed "the only 4xx(es) after the member was removed arrived $(grace_phrase "$REMOVED_AT" "$GRACE_SECONDS"), and every one of them is the CEILING's own refusal (its words name the ceiling), which the ceiling row already reads: the ceiling refuses a call that would pass it whether or not the member is still on the workspace, so those statuses say nothing about the removal having taken effect. That is not a pass — the removed member's call is either SERVED, which is the finding R13 forbids, or it was never made or the moment is the wrong one. Call once from that member's job account and read the log by hand: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $WP --freshness=5m --format=json"
      return 0
    fi
    unmeasured removed "no 4xx request follows the moment you named (CHECK771_REMOVED_AT=${REMOVED_AT}) within ${GRACE_SECONDS}s: either the removed member's call was SERVED — the finding, because R13 says the service re-reads its node within a minute — or the call was never made, or the moment is the wrong one. Remove the member, call once from that member's job account, and read the log by hand: gcloud logging read 'resource.labels.service_name=\"${SERVICE}\"' --project $WP --freshness=5m --format=json"
    return 0
  fi
  fact removed PASS "the first 4xx after the member was removed that is NOT the ceiling's own refusal arrived $(grace_phrase "$REMOVED_AT" "$GRACE_SECONDS"): status $(printf '%s' "$named" | head -n 1 | cut -d' ' -f2) at $(printf '%s' "$named" | head -n 1 | cut -d' ' -f1). The service re-reads its own node when it is older than 60 seconds (R13), so the removal took effect with no new revision and no update — and a call refused before its identity is verified writes neither the audit line nor the refusal line by design, which is why the status is the whole of this reading"
}

# ceiling_moments LINES: the moments, comma-joined, of the refusals whose own
# words name the ceiling. They are the statuses the removal's reading has to
# leave out, because a call the ceiling refused says nothing about a removal.
ceiling_moments() {
  lines_of line "$1" | grep -F "$REFUSED_LINE_MARK" | grep -i 'ceiling' \
    | cut -d' ' -f1 | tr '\n' ',' || true
}

# exclude_moments LINES MOMENTS: the lines whose first field is not one of the
# comma-joined MOMENTS.
exclude_moments() {
  local lines=$1 skip=$2
  [ -n "${lines//[[:space:]]/}" ] || return 0
  printf '%s\n' "$lines" | awk -v skip="$skip" '
    { n = split(skip, s, ","); for (i = 1; i <= n; i++) if (s[i] != "" && $1 == s[i]) next; print }'
}

# refusals_in_grace LINES MOMENT SECONDS: the lines whose timestamp falls in
# [MOMENT, MOMENT+SECONDS).
refusals_in_grace() {
  local lines=$1 moment=$2 seconds=$3
  local epoch
  epoch=$(moment_epoch "$moment") || return 0
  printf '%s\n' "$lines" | grace_filter "$epoch" "$seconds"
}

statuses_in_grace() {
  local lines=$1 moment=$2 seconds=$3 want=$4
  local epoch
  epoch=$(moment_epoch "$moment") || return 0
  # The status is the line's SECOND field: the first is the moment, so that a
  # line's own text may carry spaces (the marks this check reads do).
  printf '%s\n' "$lines" | grace_filter "$epoch" "$seconds" | awk -v want="$want" '$2 ~ want' || true
}

grace_filter() { # grace_filter EPOCH SECONDS — stdin: `<timestamp> <rest...>`
  # Prints the lines whose own moment falls in [EPOCH, EPOCH+SECONDS). The
  # timestamp is the FIRST field of every line the reader prints, so a line whose
  # text carries spaces — both marks this check greps for do — stays readable.
  python3 -c '
import sys, datetime
epoch = int(sys.argv[1]); seconds = int(sys.argv[2])
for raw in sys.stdin:
    head = raw.strip().split(" ", 1)
    if not head or not head[0]:
        continue
    try:
        at = datetime.datetime.fromisoformat(head[0].replace("Z", "+00:00")).timestamp()
    except ValueError:
        continue
    if epoch <= at < epoch + seconds:
        sys.stdout.write(raw if raw.endswith("\n") else raw + "\n")
' "$1" "$2"
}

moment_epoch() { # moment_epoch MOMENT — epoch seconds or RFC 3339
  local moment=$1
  case "$moment" in
  *[!0-9]*) python3 -c '
import sys, datetime
stamp = sys.argv[1].replace("Z", "+00:00")
try:
    print(int(datetime.datetime.fromisoformat(stamp).timestamp()))
except ValueError:
    sys.exit(1)
' "$moment" ;;
  *) printf '%s' "$moment" ;;
  esac
}

grace_phrase() {
  printf 'within %ss of %s' "$2" "$1"
}

# step_calls: what only the owner can do, said out loud rather than inferred.
step_calls() {
  fact calls-prompt INFO "not run by this check, and no agent may run any of them: (1) one member's run on the workspace's credentials; (2) one call with the workspace's cap at its ceiling; (3) 'fugaro workspace member rm <workspace> <project>' and one call from that member's job account right after. Note the moment before (2) and (3) — 'date -u +%s' — and pass them as CHECK771_CEILING_AT and CHECK771_REMOVED_AT. Removing a member and raising its cap change the workspace the owner declared: put both back afterwards"
}

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 771 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guards: fugaro doctor --project ${INSTALLATION} --all and fugaro version"
  say "  [network, read] gcloud run services describe ${SERVICE} --project ${WP} --region ${L} --format=json — the workspace's OWN project: the revision and the account it runs as"
  say "  [network, read] gcloud iam roles describe ${ROLE} --project ${P} — the member-side permissions, which U3 says are instances.get alone"
  say "  [network, read] gcloud projects get-iam-policy ${P} — which member holds that role"
  say "  [network, read] gcloud logging read --project ${WP} — the workspace's service's log: the window's ${ADMITTED_LINE_MARK} and ${REFUSED_LINE_MARK} lines and request statuses"
  say "  [not run] the three calls the rows read: a member's run, a call at the ceiling, and a call after the removal"
  say "  [nothing created] no member, no job, no binding, no grant, no revision; the scratch directory is removed at the end"
  for key in fugaro-version project-region xtrace service role binding reads admitted ceiling removed calls-prompt; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no service was described, no role was read and no log was listed"
}

BLOCK_KEYS=(fugaro-version project-region xtrace dry-run service role binding reads
  admitted ceiling removed calls-prompt restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 771 RESULT\n' >&3
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
  printf 'CHECK 771 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------------- inputs
# P is the MEMBER's GCP project, where the role and the binding are read; WP is
# the WORKSPACE's own GCP project, where its service and its log live. They are
# two projects because the feature is two-sided: the service runs in the
# workspace's own project (deploy/terraform/gcp/roots/workspace/main.tf) and the
# role that lets it read a member's database is created in the member's
# (deploy/terraform/gcp/modules/firebase/workspace_access.tf).
P=fugaro-dev
L=us-east5
DRY_RUN_CLI=""
for arg in "$@"; do
  case "$arg" in
  --dry-run) DRY_RUN_CLI=1 ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *) say "unknown argument: $arg"; exit 2 ;;
  esac
done

WORKSPACE_PROJECT=${CHECK771_WORKSPACE_PROJECT:-}
WP=$WORKSPACE_PROJECT
MEMBER_PROJECT=${CHECK771_MEMBER_PROJECT:-$P}
P=$MEMBER_PROJECT
SERVICE=${CHECK771_SERVICE:-fugaro-workspace-credentials}
INSTALLATION=${CHECK771_INSTALLATION:-fugaro}
WINDOW_MINUTES=${CHECK771_WINDOW_MINUTES:-60}
ROLE=${CHECK771_ROLE:-fugaroWorkspaceRead}
JOB_ACCOUNT=${CHECK771_JOB_ACCOUNT:-}
CEILING_AT=${CHECK771_CEILING_AT:-}
REMOVED_AT=${CHECK771_REMOVED_AT:-}
GRACE_SECONDS=${CHECK771_GRACE_SECONDS:-60}
DRY_RUN=${CHECK771_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK771_KEEP_SCRATCH:-}
LOG_OUT=""
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$ACCOUNT" ] || die "gcloud prints no account ('gcloud config get-value account'): the check reads the role, the IAM policy and the log with that account's own roles. Run 'gcloud auth login' first"
  [ "$WINDOW_MINUTES" -gt 0 ] || die "CHECK771_WINDOW_MINUTES is ${WINDOW_MINUTES}: the window is minutes of the service's log, so it has to be a positive number"
  [ "$GRACE_SECONDS" -gt 0 ] || die "CHECK771_GRACE_SECONDS is ${GRACE_SECONDS}: the grace is how long a refusal may take, so it has to be a positive number"
  scratch_up
  trap on_error ERR
  guard_agent
  guard_xtrace
  guard_project
  # The workspace's service and its log are in the workspace's OWN project, and
  # there is no default: the member's project carries the role and the binding
  # but no such service, so guessing it would describe the wrong deployment.
  [ -n "$WP" ] || die "CHECK771_WORKSPACE_PROJECT was not set: the workspace's Cloud Run service and its log live in the WORKSPACE's own project (the one its own root deploys, deploy/terraform/gcp/roots/workspace/main.tf), which is not the member's ${P} where the role and its binding are read. Reading the service from ${P} would describe a deployment that is not this check's subject, so name the workspace's project: CHECK771_WORKSPACE_PROJECT=<the workspace's GCP project> bash scripts/live-checks/check771.sh"
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  say "checking a member's admission and reads on the workspace's service"
  step_service
  step_role
  step_binding
  step_reads
  step_admitted
  step_ceiling
  step_removed
  step_calls
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
