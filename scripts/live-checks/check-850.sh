#!/usr/bin/env bash
# Check 850 — a person's token at the workspace, end to end: stored once with
# nothing but the two invoke grants, served to their runs in two members, and
# refused the four ways it has to be.
#
# Plan task WC12 writes it (docs/workspace-credentials/implementation-plan.md
# §8); no task of that plan runs it. It settles the reconciliation's U5, U6 and
# U10 of docs/workspace-credentials/reconciliation.md — whether a workspace's
# service can read a person's container through a prefix-conditioned accessor,
# whether a person holding only an invoke grant on the workspace's two services
# is admitted with a gcloud identity token, and whether a roster person who is
# no launcher of the workspace's project loads that project's config and reaches
# its intake by naming the project — and it reads D2960, D3148, R9/R11, R13 and
# D3146 as they were built by WC3, WC4, WC7 and WC9.
#
# WHAT THIS SCRIPT IS, AND WHAT IT IS NOT. It creates and deletes NOTHING and
# launches no run: the token, the launches, the switch and the removals are the
# owner's own acts, and what the script measures is what they left. It reads the
# workspace's own project (its two Cloud Run services' IAM policies, the
# person's container, the workspace's own account's accessor read off that
# project's IAM policy, and the intake's own audit lines), the workspace's
# credentials-service log for the runs, and the runs themselves.
#
#   * U6 and "nothing but the two invoke grants": each of the workspace's two
#     services binds `roles/run.invoker` to the person's own principal, and the
#     workspace's own project IAM names that person in no binding at all.
#   * U5 and R6: the workspace's own account holds `secretAccessor` on the
#     workspace's PROJECT under a condition naming BOTH halves of the one name
#     served — the `fugaro_u_` prefix AND the `claude-oauth-token` it ends in,
#     which is what the workspace's own root writes (a project-level conditional
#     member, not a grant on the container) — and the intake's own line says the
#     person wrote the container (op create, scope user, status ok). A grant
#     with no condition, or one that names the prefix alone, reaches every other
#     person's container: both are FAILs.
#   * R7: a run launched in each of two members, on a member whose switch is on,
#     is served that person's container — the service's own `credsvc: audit`
#     line for the run names it — and a second person's run in a member is served
#     something else, with no line naming the first person's container.
#   * R9/R11: the switch off refuses the call as the service writes it, and a
#     token deleted underneath a slip refuses as written too (D2974).
#   * D3146: a person removed from the roster is refused within a minute, a run
#     of theirs in flight included — the refusal names the removal, and the run
#     halts rather than moving to another credential.
#
# Usage:      bash scripts/live-checks/check-850.sh
#             bash scripts/live-checks/check-850.sh --dry-run
#             bash scripts/live-checks/check-850.sh --help
# Needs:      bash, mktemp, python3, gcloud (authenticated as an owner of the
#             workspace's project, which is where every read below lands) and
#             fugaro (the guards and the two members' run listings).
# Where:      run from anywhere. The workspace's own GCP project is
#             ${CHECK850_WORKSPACE_PROJECT} — there is no default, because the
#             member's project carries none of what this check reads — and the
#             workspace's two services are read in `${CHECK850_REGION}`
#             (us-east5 unless you name another): a Cloud Run call carries a
#             region, and one that is left out makes gcloud ask for it. A
#             project, service, account or container naming belong is refused by
#             name.
# Creates:    nothing. The copies of the policies, the listing and the log
#             entries this run reads sit in a scratch directory (mode 0700)
#             removed from the exit trap even on a failure or Ctrl-C.
# Costs:      nothing billable by the check: it launches no run and calls no
#             model. The runs it reads are the owner's own.
# Runs:       by YOU, after those acts — and no agent runs any step of it. Its
#             own test (check-850_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else.
# Output:     progress on stderr, ONE block on stdout headed CHECK 850 RESULT,
#             then CHECK 850 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure. In the block, a key the run never reached
#             reads `FAIL — not reached`.
#
# Knobs (all CHECK850_*): DRY_RUN, KEEP_SCRATCH, WORKSPACE_PROJECT (REQUIRED),
# REGION (the region the workspace's two Cloud Run services run in; us-east5),
# WORKSPACE_ACCOUNT (the workspace's own service account, the one R6's accessor
# is held by; `fugaro-workspace@<the workspace's project>.iam.gserviceaccount.com`
# when unset), INSTALLATION (the installation the guard reads; fugaro), PERSON
# (the roster person's email, the `user:` principal the two grants are read for;
# REQUIRED),
# PERSON_SUB (their `sub`, the container name's middle field; read off the
# intake's own line when unset), CONTAINER (their container's id in the
# workspace's project; read off the listing when unset), MEMBER_ONE_PROJECT and
# MEMBER_ONE_RUN, MEMBER_TWO_PROJECT and MEMBER_TWO_RUN (the two members and the
# run of the person's launched in each), OTHER_PERSON, OTHER_PROJECT and
# OTHER_RUN (a second person and a run of theirs in a member), OFF_PROJECT and
# OFF_RUN (the run launched in a member whose switch is off), DELETED_PROJECT and
# DELETED_RUN (the run launched after the token was deleted), REMOVED_PROJECT and
# REMOVED_RUN (the run of the person's in flight when they were removed),
# REMOVED_AT (when the removal was made, epoch seconds or RFC 3339), LOG_AGE
# (how far back the log is read, 24h), LOG_LIMIT (200), GRACE_SECONDS (how long
# a refusal may take after REMOVED_AT, 60). Any run left unset leaves its row
# unmeasured rather than guessed.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this run reads IAM
# policies and an installation's log, and the shell it is started from may be a
# login shell whose trace ends up in a log.
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

# The two services of the workspace's own project, as the installation names
# them (internal/infra's CredentialsServiceName and IntakeServiceName): the
# first label of a Cloud Run host, which is what a log filter and a get-iam-policy
# call carry.
CREDENTIALS_SERVICE=fugaro-credentials
INTAKE_SERVICE=fugaro-intake
# The prefix every per-user container of a workspace carries (secretname's
# grammar), and the one name this half serves: a grant that names neither is not
# the accessor U5 and R6 are about.
PERSON_PREFIX=fugaro_u_
PERSON_TOKEN_NAME=claude-oauth-token
# R6's accessor: the workspace's own root writes it as a PROJECT-level
# conditional member and not as a grant on the container
# (deploy/terraform/gcp/roots/workspace/main.tf's
# google_project_iam_member.workspace_person_token), because a container's name
# is the verified token's own `sub` and no plan knows it at apply time. The role
# is internal/infra/tf's WorkspaceBundleAccessorRole.
ACCESSOR_ROLE=roles/secretmanager.secretAccessor
# The service's own sentences, grepped and never paraphrased. Each is a
# constant of internal/credsvc (personal_workspace.go's personalSwitchOff,
# dynamicsecrets.go's per-user half and wire.go's ErrorSlipOwnerRemoved): a
# check that read a differently worded refusal as the same one would read a
# release that refuses something else as this one.
SWITCH_OFF_PHRASE='has the personal workspace token switch off'
TOKEN_ABSENT_PHRASE="the personal token this call's slip names is not there"
REMOVED_CODE='slip_owner_removed'
INTAKE_AUDIT_MSG='intake: audit'

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 850 needs bash"
  local t
  for t in mktemp python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 850 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk850-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk850.XXXXXXXX") || die "Check 850 cannot make a scratch directory"
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
    say "CHECK850_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created and nothing was deleted by this check: no secret was written, no grant was made, no switch was moved and no run was touched. Every read was a describe, a get-iam-policy, a listing or a log read"
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

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }
field_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1; }
lines_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p"; }
last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 300 || true
}
# members_of BODY ROLE — the member list of every binding with ROLE, one per
# line. The reader prints `binding=<role>|<members>|<condition>`, so the list is
# the field between the two first bars.
members_of() { printf '%s\n' "$1" | sed -n "s#^binding=$2|\([^|]*\)|.*#\1#p"; }
has_member() { # has_member LIST PRINCIPAL
  local list=$1 who=$2
  case ",$list," in
  *",$who,"*) return 0 ;;
  esac
  return 1
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 850's reader: two IAM policies, a service listing, a container listing,
an intake's own lines, a credentials service's own lines, a run listing and a
doctor document. None of them carries a credential's value.

policy  argv: `gcloud … get-iam-policy … --format=json`'s file. Prints
        `principal=<member>` for every member of every binding, and
        `binding=<role>|<members,joined>|<condition title or ->|<condition
        expression or ->` per binding, so a case can say WHO an accessor is and
        under WHAT condition — the title AND the expression, which is what R6's
        accessor is read on. A policy with no bindings prints `bindings=0`.
list    argv: a `--format=json` listing. Prints `name=<name>` per resource, and
        the name is the LAST path segment (`fugaro-credentials`, not the whole
        `projects/p/locations/r/services/fugaro-credentials`), so a case can
        compare it to the name the installation gives the service.
intake  argv: the intake's lines' file and the person's sub (or `any`). Prints
        `act=<op>|<scope>|<status>|<caller>|<sub>|<id>` for every `intake: audit`
        line whose sub matches, plus `acts=` with the count of every audit line
        so a read that answered nothing is told from one that answered lines.
audit   argv: the service's lines' file and a run id. Prints
        `audit=<status>|<credential>|<tier>|<purpose>|<run>|<slip>|<owner>` for
        each `credsvc: audit` line for that run, `refused=<status>|<message>`
        for each `credsvc: refused` line, `audits=`/`refusals=` counts, and
        `phrase=<msg>|<message>` for every line of ANY kind whose text carries
        the phrase the caller asked about — the service's own sentence for a
        refusal is what says which refusal it was.
runs    argv: `fugaro runs ls --json`'s file and the run id asked about (or its
        last characters). Prints the row's status, terminal, outcome and its
        halt's reason, moment and detail; `found=no` when no row matches.
doctor  argv: `fugaro doctor --all --json`'s file. Prints `ok=`, `checks=` and
        one `warning=<id>: <problem>` per warning.
"""

import json
import re
import sys


def load(path):
    with open(path) as fh:
        return json.load(fh)


def text(v):
    return re.sub(r"\s+", " ", str(v if v is not None else "")).strip()


def entries(path):
    doc = load(path)
    return doc if isinstance(doc, list) else []


def payload(entry):
    if not isinstance(entry, dict):
        return {}
    p = entry.get("jsonPayload")
    return p if isinstance(p, dict) else {}


def msg_of(entry):
    p = payload(entry)
    return entry.get("textPayload") or p.get("msg") or p.get("message") or ""


def policy(path):
    doc = load(path)
    bindings = doc.get("bindings") if isinstance(doc, dict) else None
    if not isinstance(bindings, list):
        bindings = []
    print("bindings=%d" % len(bindings))
    for b in bindings:
        if not isinstance(b, dict):
            continue
        role = text(b.get("role"))
        members = b.get("members") if isinstance(b.get("members"), list) else []
        cond = b.get("condition") if isinstance(b.get("condition"), dict) else {}
        title = text(cond.get("title")) if cond else "-"
        expr = text(cond.get("expression")) if cond else "-"
        print("binding=%s|%s|%s|%s" % (role, ",".join(text(m) for m in members),
                                       title or "-", expr or "-"))
        for m in members:
            print("principal=" + text(m))


def listing(path):
    doc = load(path)
    rows = doc if isinstance(doc, list) else []
    print("count=%d" % len(rows))
    for row in rows:
        if isinstance(row, dict):
            print("name=" + text(row.get("name")).rsplit("/", 1)[-1])


def intake(path, sub):
    rows = entries(path)
    total = 0
    for entry in rows:
        if msg_of(entry) != "intake: audit":
            continue
        total += 1
        p = payload(entry)
        if sub != "any" and text(p.get("sub")) != sub:
            continue
        print("act=%s|%s|%s|%s|%s|%s" % (
            text(p.get("op")), text(p.get("scope")), text(p.get("status")),
            text(p.get("caller")), text(p.get("sub")), text(p.get("id"))))
    print("acts=%d" % total)


def audit(path, rid, phrase):
    rows = entries(path)
    audits = refusals = 0
    for entry in rows:
        if not isinstance(entry, dict):
            continue
        p = payload(entry)
        line = json.dumps(entry)
        if phrase and phrase in line:
            print("phrase=%s|%s" % (text(msg_of(entry)), text(p.get("message"))[:300]))
        if p.get("run") != rid:
            continue
        m = msg_of(entry)
        if m == "credsvc: audit":
            audits += 1
            print("audit=%s|%s|%s|%s|%s|%s|%s" % (
                text(p.get("status")), text(p.get("credential")), text(p.get("tier")),
                text(p.get("purpose")), text(p.get("run")), text(p.get("slip")),
                text(p.get("owner"))))
        elif m == "credsvc: refused":
            refusals += 1
            print("refused=%s|%s" % (text(p.get("status")), text(p.get("message"))[:300]))
    print("audits=%d" % audits)
    print("refusals=%d" % refusals)


def runs(path, asked):
    doc = load(path)
    rows = doc.get("runs") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        rows = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        run_id = text(row.get("run_id"))
        if not asked or not (run_id == asked or run_id.endswith(asked)):
            continue
        halt = row.get("halt") if isinstance(row.get("halt"), dict) else {}
        print("found=yes")
        print("run_id=" + run_id)
        print("status=" + text(row.get("status")))
        print("terminal=" + ("yes" if row.get("terminal") else "no"))
        print("outcome=" + text(row.get("outcome")))
        print("halt_reason=" + text(halt.get("reason")))
        print("halt_at=" + text(halt.get("at")))
        print("halt_detail=" + text(halt.get("detail"))[:300])
        return
    print("found=no")


def doctor(path):
    doc = load(path)
    checks = doc.get("checks") if isinstance(doc, dict) else None
    if not isinstance(checks, list):
        checks = []
    warnings = 0
    for one in checks:
        if not isinstance(one, dict):
            continue
        if str(one.get("severity") or "") != "warning":
            continue
        warnings += 1
        print("warning=%s: %s" % (text(one.get("id")), text(one.get("problem"))[:300]))
    print("ok=" + ("yes" if doc.get("ok") else "no"))
    print("checks=%d" % len(checks))
    print("warnings=%d" % warnings)


def main(argv):
    if len(argv) < 3:
        print("reader: need a reading", file=sys.stderr)
        return 2
    what = argv[1]
    if what == "policy":
        policy(argv[2])
    elif what == "list":
        listing(argv[2])
    elif what == "intake":
        intake(argv[2], argv[3] if len(argv) > 3 else "any")
    elif what == "audit":
        audit(argv[2], argv[3] if len(argv) > 3 else "", argv[4] if len(argv) > 4 else "")
    elif what == "runs":
        runs(argv[2], argv[3] if len(argv) > 3 else "")
    elif what == "doctor":
        doctor(argv[2])
    else:
        print("reader: unknown reading %s" % what, file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
}

# ------------------------------------------------------------------ the guards
# No agent runs any step of Check 850 (the plan says so): the person's token,
# the launches and the removal are the owner's, and a coding agent's session is
# refused by the markers it carries, before anything is read.
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 850. Run it yourself: bash scripts/live-checks/check-850.sh"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-850.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry a policy or a log line it reads"
}

guard_installation() {
  local doctor_out v
  doctor_out=$(fugaro doctor --project "$INSTALLATION" --all || true)
  if ! printf '%s\n' "$doctor_out" | grep -q '^project: .*(GCP fugaro-dev,'; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project ${INSTALLATION} --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check's reads land on ${WORKSPACE_PROJECT} and never belong"
  fi
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  [[ "$v" == *belong* ]] && stop "the version this check printed names belong: this check is never run there"
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} — this is the CLI the rows are read against: the container's name, the intake's address and the refusal the switch writes are this release's, and a check run with an older CLI reads another release's promises"
  case "$INSTALLATION $WORKSPACE_PROJECT $PERSON ${PERSON_SUB:-} ${CONTAINER:-}" in
  *belong*) stop "one of the names this check was given carries belong: Check 850 never reads that installation" ;;
  esac
}

# ------------------------------------------------------------------- the reads
# log_read OUT PROJECT SERVICE FILTER — the service's own lines, nothing else.
# The filter is the service's name and one jsonPayload field; `--format=json`
# answers `[]` when nothing matched, which is a read that answered, and exits
# non-zero when it did not.
log_read() { # log_read OUT PROJECT SERVICE FILTER
  local out=$1 project=$2 service=$3 extra=$4
  local filter="resource.type=\"cloud_run_revision\" resource.labels.service_name=\"$service\""
  [ -z "$extra" ] || filter="$filter $extra"
  local rc=0
  gcloud logging read "$filter" --project="$project" --freshness="$LOG_AGE" \
    --limit="$LOG_LIMIT" --format=json >"$out" 2>"$out.err" || rc=$?
  if [ "$rc" -ne 0 ] || [ ! -s "$out" ]; then
    printf '%s' "the service's own lines could not be read: 'gcloud logging read' exited ${rc} and printed: $(last_line "$(cat "$out.err" 2>/dev/null || true)"). A read that did not answer is NOT the service having written no line. Read the log yourself (the filter is in this check's --help) and run the check again" >"$out.unread"
    return 1
  fi
  return 0
}

unread_text() { cat "$1.unread" 2>/dev/null || true; }

# ---------------------------------------------------------------- the policies
# step_grants: U6, and the "nothing but the two invoke grants" half. The two
# services bind the person's own principal as an invoker, and the project's own
# IAM names that person nowhere.
step_grants() {
  local key='invoke-grants'
  if [ -z "$PERSON" ]; then
    unmeasured "$key" "no person was named (CHECK850_PERSON is unset), so there is no principal to read the two grants for. Name the roster person whose token this check is about and run it again"
    return 0
  fi
  gcloud projects get-iam-policy "$WORKSPACE_PROJECT" --format=json >"$SCRATCH/project.json" 2>"$SCRATCH/project.err" || true
  gcloud run services list --project "$WORKSPACE_PROJECT" --region "$REGION" --platform=managed \
    --format=json >"$SCRATCH/services.json" 2>"$SCRATCH/services.err" || true
  if [ ! -s "$SCRATCH/project.json" ] || [ ! -s "$SCRATCH/services.json" ]; then
    unmeasured "$key" "the workspace's project (${WORKSPACE_PROJECT}) could not be read: 'gcloud projects get-iam-policy' or 'gcloud run services list --region ${REGION} --platform=managed' answered nothing ($(last_line "$(cat "$SCRATCH/services.err" 2>/dev/null || true)")). Run both here yourself — the check needs an owner of that project, and the region its two services run in (CHECK850_REGION, ${REGION} now) — and run it again"
    return 0
  fi
  local list svc granted="" missing="" body
  list=$(reader list "$SCRATCH/services.json")
  local bindings
  bindings=$(reader policy "$SCRATCH/project.json")
  if printf '%s\n' "$bindings" | grep -qx "principal=user:${PERSON}"; then
    unmeasured "$key" "the workspace's project IAM names ${PERSON} directly: $(printf '%s' "$(printf '%s\n' "$bindings" | grep "^binding=" | head -n 3 | tr '\n' '; ')" | head -c 400). The check cannot read U6 off a person who holds a role on the project itself — 'nothing but the two invoke grants' is the premise — so either this is another person's account, or the grant was made wider than WC2 asks"
    return 0
  fi
  for svc in $CREDENTIALS_SERVICE $INTAKE_SERVICE; do
    local name
    name=$(printf '%s\n' "$list" | sed -n "s|^name=${svc}$|${svc}|p" | head -n 1)
    if [ -z "$name" ]; then
      missing="${missing}${missing:+, }${svc} (the workspace's project serves no service whose name starts with it in ${REGION}; if its two services run in another region, name it with CHECK850_REGION)"
      continue
    fi
    gcloud run services get-iam-policy "$name" --project "$WORKSPACE_PROJECT" --region "$REGION" --platform=managed \
      --format=json >"$SCRATCH/policy-$svc.json" 2>"$SCRATCH/policy-$svc.err" || true
    if [ ! -s "$SCRATCH/policy-$svc.json" ]; then
      unmeasured "$key" "the invoke grant on ${name} could not be read in ${REGION} ($(last_line "$(cat "$SCRATCH/policy-$svc.err" 2>/dev/null || true)")): run 'gcloud run services get-iam-policy ${name} --project ${WORKSPACE_PROJECT} --region ${REGION} --format=json' here yourself and run the check again"
      return 0
    fi
    body=$(reader policy "$SCRATCH/policy-$svc.json")
    local invoker=no m
    while IFS= read -r m; do
      has_member "$m" "user:${PERSON}" && invoker=yes
    done <<<"$(members_of "$body" roles/run.invoker)"
    if [ "$invoker" = yes ]; then
      granted="${granted}${granted:+, }${name}"
    else
      missing="${missing}${missing:+, }${name} (no roles/run.invoker binding names user:${PERSON}; the bindings it has are $(printf '%s' "$(printf '%s\n' "$body" | grep '^binding=' | head -n 2 | tr '\n' '; ')" | head -c 300))"
    fi
  done
  if [ -n "$missing" ]; then
    fact "$key" FAIL "the two invoke grants are not both there: ${missing}. WC2's whole point is that a roster person holds the invoke grant on the workspace's credentials service and on its intake and NOTHING else — a person refused here is a person whose runs and whose token have nowhere to go. Re-apply the workspace's own root ('fugaro update' on the workspace's project) and run the check again"
    return 0
  fi
  fact "$key" PASS "both of the workspace's services bind roles/run.invoker to user:${PERSON} — ${granted} — and the project's own IAM names that person in no binding: the roster person holds the two invoke grants and nothing else on the workspace's project, which is the premise U6 (does the platform admit them with a gcloud identity token) is read on. Read it yourself: gcloud run services get-iam-policy <service> --project ${WORKSPACE_PROJECT} --region ${REGION}"
}

# step_container: U5 and D2960's "stored once". The container exists once in the
# workspace's project, the workspace's account reads it only under a prefix
# condition (R6), and the intake's own line says the person wrote it.
step_container() {
  local key='person-container'
  gcloud secrets list --project "$WORKSPACE_PROJECT" --filter="name:${PERSON_PREFIX}" \
    --format=json >"$SCRATCH/secrets.json" 2>"$SCRATCH/secrets.err" || true
  if [ ! -s "$SCRATCH/secrets.json" ]; then
    unmeasured "$key" "the workspace's containers could not be listed ($(last_line "$(cat "$SCRATCH/secrets.err" 2>/dev/null || true)")): run 'gcloud secrets list --project ${WORKSPACE_PROJECT} --filter=name:${PERSON_PREFIX} --format=json' here yourself and run the check again"
    return 0
  fi
  local list names count
  list=$(reader list "$SCRATCH/secrets.json")
  count=$(field_of count "$list")
  names=$(lines_of name "$list")
  if [ -z "$CONTAINER" ]; then
    if [ "${count:-0}" -ne 1 ]; then
      unmeasured "$key" "the workspace's project holds ${count:-0} containers under ${PERSON_PREFIX} ($(printf '%s' "$(printf '%s\n' "$names" | head -n 5 | tr '\n' ' ')" | head -c 300)): the check needs exactly one to read the accessor's condition for, and a person's token is stored once (D2960). Name the one this check is about with CHECK850_CONTAINER and run it again"
      return 0
    fi
    CONTAINER=$(printf '%s\n' "$names" | head -n 1)
  fi
  # R6's accessor is a PROJECT-level conditional member and not a grant on the
  # container (roots/workspace/main.tf's
  # google_project_iam_member.workspace_person_token): a container's name is the
  # verified token's own `sub`, which no plan knows at apply time, so the grant
  # is written on the workspace's project and narrowed by its condition.
  gcloud projects get-iam-policy "$WORKSPACE_PROJECT" --format=json >"$SCRATCH/project.json" 2>"$SCRATCH/project.err" || true
  if [ ! -s "$SCRATCH/project.json" ]; then
    unmeasured "$key" "the workspace's project policy could not be read, so the accessor ${WORKSPACE_ACCOUNT} holds on it could not be read either ($(last_line "$(cat "$SCRATCH/project.err" 2>/dev/null || true)")): run 'gcloud projects get-iam-policy ${WORKSPACE_PROJECT} --format=json' here yourself and run the check again"
    return 0
  fi
  local body role members title expr
  local conditioned="" unconditional="" prefix_only="" other=""
  body=$(reader policy "$SCRATCH/project.json")
  while IFS='|' read -r role members title expr; do
    [ -n "$role" ] || continue
    [ "$role" = "$ACCESSOR_ROLE" ] || continue
    case ",$members," in
    *",serviceAccount:${WORKSPACE_ACCOUNT},"*) ;;
    *) continue ;;
    esac
    if [ "$expr" = "-" ]; then
      unconditional="${unconditional}${unconditional:+, }${ACCESSOR_ROLE}"
      continue
    fi
    local has_prefix=no has_name=no
    case "$expr" in *"$PERSON_PREFIX"*) has_prefix=yes ;; esac
    case "$expr" in *"$PERSON_TOKEN_NAME"*) has_name=yes ;; esac
    if [ "$has_prefix" = yes ] && [ "$has_name" = yes ]; then
      conditioned="${conditioned}${conditioned:+, }${ACCESSOR_ROLE} under '${title}' (${expr})"
    elif [ "$has_prefix" = yes ]; then
      prefix_only="${prefix_only}${prefix_only:+, }${ACCESSOR_ROLE} under '${title}' (${expr})"
    else
      other="${other}${other:+, }${ACCESSOR_ROLE} under '${title}' (${expr})"
    fi
  done <<<"$(lines_of binding "$body")"
  if [ -n "$unconditional" ]; then
    fact "$key" FAIL "the workspace's own account (${WORKSPACE_ACCOUNT}) holds ${unconditional} on ${WORKSPACE_PROJECT}'s own IAM with NO condition: R6's accessor is conditioned to the ${PERSON_PREFIX} prefix AND the ${PERSON_TOKEN_NAME} name its grant serves, so an unconditional one reads EVERY secret of the workspace's project — every other person's token among them. Re-apply the workspace's own root ('fugaro update' on the workspace's project) and run the check again"
    return 0
  fi
  if [ -n "$prefix_only" ]; then
    fact "$key" FAIL "the workspace's own account holds ${prefix_only} — the ${PERSON_PREFIX} prefix ALONE. R6's condition names BOTH halves (${PERSON_PREFIX} and ${PERSON_TOKEN_NAME}), and a prefix-only one admits every personal secret a person keeps in the workspace's project, every other person's container among them: that widening is what U5 reads and what internal/infra/tf's cover refuses. Re-apply the workspace's own root and run the check again"
    return 0
  fi
  if [ -z "$conditioned" ]; then
    fact "$key" FAIL "no binding on ${WORKSPACE_PROJECT}'s own IAM gives ${WORKSPACE_ACCOUNT} ${ACCESSOR_ROLE} under a condition naming the ${PERSON_PREFIX} prefix and the ${PERSON_TOKEN_NAME} it ends in${other:+, and one binding carries ${other}}: the bindings of that role are $(printf '%s' "$(printf '%s\n' "$body" | grep "^binding=${ACCESSOR_ROLE}|" | head -n 3 | tr '\n' '; ' || true)" | head -c 400). U5 is exactly this read — whether a real workspace's service can read a person's container through a prefix-conditioned accessor — so a policy without one is the answer NO. Re-apply the workspace's own root and run the check again"
    return 0
  fi
  # The intake's own lines: the person wrote it, with the two grants and no role.
  log_read "$SCRATCH/intake.json" "$WORKSPACE_PROJECT" "$INTAKE_SERVICE" "" || {
    unmeasured "$key" "$(unread_text "$SCRATCH/intake.json")"
    return 0
  }
  local acts act sub
  sub=${PERSON_SUB:-any}
  acts=$(reader intake "$SCRATCH/intake.json" "$sub")
  act=$(printf '%s\n' "$acts" | grep '^act=create|user|ok|' | head -n 1 || true)
  if [ -z "$act" ]; then
    fact "$key" FAIL "the intake's own lines carry no write of a user-scope container (op create, scope user, status ok) for ${PERSON_SUB:+sub ${PERSON_SUB}}${PERSON_SUB:-any person} (checked $(field_of acts "$acts") audit line(s) in the last ${LOG_AGE}): the acts it wrote are $(printf '%s' "$(lines_of act "$acts" | head -n 5 | tr '\n' '; ')" | head -c 400). The container ${CONTAINER} is there and no line says the person's own identity wrote it — which is the half of U6/U10 that says the write came from a person holding only the two invoke grants. Write it once more at your own terminal ('fugaro secrets user set --project ${WORKSPACE_PROJECT}'), then run the check again"
    return 0
  fi
  PERSON_SUB=$(printf '%s' "$act" | cut -d'|' -f5)
  fact "$key" PASS "${CONTAINER} is the one container under ${PERSON_PREFIX} in ${WORKSPACE_PROJECT} (D2960: stored once, at the workspace), the workspace's own account (${WORKSPACE_ACCOUNT}) holds ${conditioned} on the project's own IAM — the prefix AND the ${PERSON_TOKEN_NAME} name, so it reaches this container and no other personal secret of the project — and the intake's own line says it was written by sub ${PERSON_SUB} with '$(printf '%s' "$act" | cut -d'|' -f1-3 | tr '|' ' ')' — a person who holds nothing but the two invoke grants. U5 answered YES: a workspace's service reads a person's container through a prefix-conditioned accessor"
}

# step_served KEY RUN WHERE — the run was served the person's own container. The
# service's own audit line for the run names the credential it served, and it is
# the person's container and not a shared entry.
step_served() { # step_served KEY RUN WHERE
  local key=$1 run=$2 where=$3
  if [ -z "$run" ]; then
    unmeasured "$key" "no run was named for ${where}: launch it BEFORE turning the switch on the member turned on, keep its run id, and pass it as the knob the --help names"
    return 0
  fi
  if [ -z "$CONTAINER" ]; then
    unmeasured "$key" "the person's container was not read, so nothing tells whether this run was served it: settle the person-container row first"
    return 0
  fi
  log_read "$SCRATCH/log-$key.json" "$WORKSPACE_PROJECT" "$CREDENTIALS_SERVICE" "jsonPayload.run=\"${run}\"" || {
    unmeasured "$key" "$(unread_text "$SCRATCH/log-$key.json")"
    return 0
  }
  local out served served_list
  out=$(reader audit "$SCRATCH/log-$key.json" "$run")
  served=$(printf '%s\n' "$out" | grep "^audit=ok|${CONTAINER}|" || true)
  if [ -n "$served" ]; then
    fact "$key" PASS "the run ${run} in ${where} was served the person's own container: the service's own line for it reads '$(printf '%s' "$served" | head -n 1 | sed 's/|/ /g' | head -c 300)' — the credential is ${CONTAINER}, the person's token at the workspace, and the slip and owner fields are the grant's (R7, and R8's 'ahead of the shared entries')"
    return 0
  fi
  served_list=$(lines_of audit "$out")
  fact "$key" FAIL "the run ${run} in ${where} was NOT served ${CONTAINER}: the service wrote $(field_of audits "$out") audit line(s) for it, and they name $(printf '%s' "$(printf '%s\n' "$served_list" | cut -d'|' -f2 | grep . | head -n 5 | tr '\n' ' ')" | head -c 300)${served_list:+}; its refusals read $(printf '%s' "$(lines_of refused "$out" | head -n 2 | tr '\n' '; ')" | head -c 300)}. R7 places the person's token first where the switch is on, so a run of theirs served another entry is either a member whose switch is off, a launch that asked for none (--no-personal-token), or a slip that named no personal token. Read the run's own lines ('gcloud logging read' with its id) and the launch's output, then run the check again"
}

# step_other: a second person's run in a member is served something else, and no
# line of it names the first person's container.
step_other() {
  local key='other-person'
  if [ -z "$OTHER_RUN" ]; then
    unmeasured "$key" "no second person's run was named (CHECK850_OTHER_RUN is unset): launch one in the same member, keep its run id, and pass it — this row is what says one person's token is not another's"
    return 0
  fi
  if [ -z "$CONTAINER" ]; then
    unmeasured "$key" "the person's container was not read, so no line can be told to name it: settle the person-container row first"
    return 0
  fi
  log_read "$SCRATCH/log-other.json" "$WORKSPACE_PROJECT" "$CREDENTIALS_SERVICE" "jsonPayload.run=\"${OTHER_RUN}\"" || {
    unmeasured "$key" "$(unread_text "$SCRATCH/log-other.json")"
    return 0
  }
  local out leaks
  out=$(reader audit "$SCRATCH/log-other.json" "$OTHER_RUN")
  if [ "$(field_of audits "$out")" = 0 ] && [ "$(field_of refusals "$out")" = 0 ]; then
    unmeasured "$key" "the service wrote no line at all for ${OTHER_RUN} in the last ${LOG_AGE}: an id nobody called under measures nothing. Check the id, and that the run really went through the workspace's service"
    return 0
  fi
  leaks=$(lines_of audit "$out" | grep "${CONTAINER}" || true)
  if [ -n "$leaks" ]; then
    fact "$key" FAIL "the run ${OTHER_RUN}${OTHER_PERSON:+ of ${OTHER_PERSON}} was served ${CONTAINER}, which is another person's token: $(printf '%s' "$(printf '%s\n' "$leaks" | head -n 2 | sed 's/|/ /g')" | head -c 300). A slip entitles its OWNER's token and no other (D2962), so this is the one finding this row exists for — read the run's slip and the grant that answered it"
    return 0
  fi
  fact "$key" PASS "the run ${OTHER_RUN}${OTHER_PERSON:+ of ${OTHER_PERSON}} in ${OTHER_PROJECT:-a member} was served no personal token of the first person's: the service's lines for it name $(printf '%s' "$(lines_of audit "$out" | cut -d'|' -f2 | grep . | sort -u | head -n 5 | tr '\n' ' ')" | head -c 300) and never ${CONTAINER}. A person's token is served to their own runs alone (D2962)"
}

# step_refused KEY RUN AT PHRASE WHERE — a refusal the release writes, read off
# the service's own line for that run, within GRACE_SECONDS of the moment the
# owner made the act that caused it.
step_refused() { # step_refused KEY RUN PHRASE WHERE
  local key=$1 run=$2 phrase=$3 where=$4
  if [ -z "$run" ]; then
    unmeasured "$key" "no run was named for ${where}: make the call, keep the run id, and pass it — this row reads what the service answered it"
    return 0
  fi
  log_read "$SCRATCH/log-$key.json" "$WORKSPACE_PROJECT" "$CREDENTIALS_SERVICE" "jsonPayload.run=\"${run}\"" || {
    unmeasured "$key" "$(unread_text "$SCRATCH/log-$key.json")"
    return 0
  }
  local out hits
  out=$(reader audit "$SCRATCH/log-$key.json" "$run" "$phrase")
  hits=$(lines_of phrase "$out" | grep . || true)
  if [ -z "$hits" ]; then
    fact "$key" FAIL "the service wrote no line carrying its own sentence for ${where} on the run ${run}: the lines it wrote for that run are $(printf '%s' "$(printf '%s\n' "$(lines_of audit "$out"; lines_of refused "$out")" | head -n 4 | tr '\n' '; ')" | head -c 400). The sentence the check looked for is '${phrase}' (internal/credsvc). A run that was served instead, or that was refused with something else, is a finding about the release — read the run's own lines and 'fugaro runs show ${run}'"
    return 0
  fi
  fact "$key" PASS "${where}: the service's own line for the run ${run} carries its sentence — '$(printf '%s' "$hits" | head -n 1 | cut -d'|' -f2- | head -c 300)'. The refusal is the one the release writes, and it is what a run of the person's meets"
}

# step_removed: D3146. The refusal names the removal, it arrives within a minute
# of the owner's act, and the run halts rather than moving to another credential.
step_removed() {
  local key='removed'
  if [ -z "$REMOVED_RUN" ] || [ -z "$REMOVED_AT" ]; then
    unmeasured "$key" "the removal was not given both halves (CHECK850_REMOVED_RUN and CHECK850_REMOVED_AT): remove the person from the roster with a run of theirs in flight, write down the moment, keep the run id, and pass all three — this row reads the refusal D3146 is"
    return 0
  fi
  log_read "$SCRATCH/log-removed.json" "$WORKSPACE_PROJECT" "$CREDENTIALS_SERVICE" "jsonPayload.run=\"${REMOVED_RUN}\"" || {
    unmeasured "$key" "$(unread_text "$SCRATCH/log-removed.json")"
    return 0
  }
  local out in_grace=""
  out=$(reader audit "$SCRATCH/log-removed.json" "$REMOVED_RUN" "$REMOVED_CODE")
  if [ -z "$(lines_of phrase "$out")" ]; then
    fact "$key" FAIL "no line of the service's for the run ${REMOVED_RUN} carries ${REMOVED_CODE}: the lines it wrote for that run are $(printf '%s' "$(printf '%s\n' "$(lines_of audit "$out"; lines_of refused "$out")" | head -n 4 | tr '\n' '; ')" | head -c 400). D3146's refusal is what a person's removal is answered with on their next call, and a run of theirs served anything at all after the removal is the finding this row exists for. Read the run's own lines and the roster's state, then run the check again"
    return 0
  fi
  local entry_at
  entry_at=$(phrase_at "$SCRATCH/log-removed.json" "$REMOVED_CODE" || true)
  if [ -n "$entry_at" ] && ! in_window "$REMOVED_AT" "$entry_at" "$GRACE_SECONDS"; then
    fact "$key" FAIL "the refusal (${REMOVED_CODE}) is in the service's lines for the run ${REMOVED_RUN}, and its moment ${entry_at} is NOT within ${GRACE_SECONDS}s of the removal (${REMOVED_AT}): $(gap_after "$REMOVED_AT" "$entry_at" || true). 'Within a minute' is the bound the plan's row states and the one the owner's own experience reads — a call that only stops being served later is a bound that has slipped"
    return 0
  fi
  [ -z "$entry_at" ] && in_grace=" (the line carries no readable timestamp, so the bound could not be placed)"
  # The run itself: halted, and the halt names the removal.
  fugaro runs ls --json ${REMOVED_PROJECT:+--project "$REMOVED_PROJECT"} >"$SCRATCH/removed-runs.json" 2>"$SCRATCH/removed-runs.err" || true
  if [ ! -s "$SCRATCH/removed-runs.json" ]; then
    unmeasured "$key" "the refusal is there, and the run's own record could not be read ($(last_line "$(cat "$SCRATCH/removed-runs.err" 2>/dev/null || true)")): run 'fugaro runs ls --json${REMOVED_PROJECT:+ --project ${REMOVED_PROJECT}}' here yourself, and treat the run's halt as this row's other half"
    return 0
  fi
  local run_out
  run_out=$(reader runs "$SCRATCH/removed-runs.json" "$REMOVED_RUN")
  if [ "$(field_of found "$run_out")" != yes ]; then
    unmeasured "$key" "no run in the listing has an id ending in ${REMOVED_RUN}. List them: fugaro runs ls --json${REMOVED_PROJECT:+ --project ${REMOVED_PROJECT}} | grep run_id"
    return 0
  fi
  local detail
  detail=$(field_of halt_detail "$run_out")
  if [ "$(field_of terminal "$run_out")" != yes ]; then
    fact "$key" FAIL "the run $(field_of run_id "$run_out") of the removed person is not terminal (status $(field_of status "$run_out")): D3146 halts a run whose slip's owner left the roster — it moves to no other credential — so a run still going is a run the halt did not reach, and 'the run of theirs in flight is refused within a minute' is not what it did. Read it: fugaro runs show $(field_of run_id "$run_out")"
    return 0
  fi
  if [ -z "$(field_of halt_reason "$run_out")" ]; then
    fact "$key" FAIL "the run $(field_of run_id "$run_out") finished while its launcher was removed from the roster, with no halt: outcome $(field_of outcome "$run_out"). A run served another credential after its owner left is exactly what D3146 forbids — the refusal has to reach it and end it"
    return 0
  fi
  case "$detail" in
  *"$REMOVED_CODE"*) ;;
  *)
    fact "$key" FAIL "the run $(field_of run_id "$run_out") halted ($(field_of halt_reason "$run_out")) with a detail that does not name the removal: '$(printf '%s' "$detail" | head -c 200)'. D3146's halt is the one that names the removal, so an operator reading this run learns why it stopped"
    return 0
    ;;
  esac
  fact "$key" PASS "the person was removed and the run ${REMOVED_RUN} in flight was refused with ${REMOVED_CODE}${in_grace}, then halted ($(field_of halt_reason "$run_out")) with the removal named in its detail — '$(printf '%s' "$detail" | head -c 200)'. D3146 answered: within a minute, no other credential, and the run's own record explains it"
}

# phrase_at FILE PHRASE: the timestamp of the first log entry whose text carries
# PHRASE, as the entry's own `timestamp` field.
phrase_at() {
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    with open(sys.argv[1]) as fh:
        rows = json.load(fh)
except (OSError, ValueError):
    sys.exit(1)
if not isinstance(rows, list):
    sys.exit(1)
for entry in rows:
    if not isinstance(entry, dict):
        continue
    if sys.argv[2] not in json.dumps(entry):
        continue
    stamp = entry.get("timestamp") or ""
    if stamp:
        print(stamp)
        sys.exit(0)
sys.exit(1)
PY
}

# in_window FROM TO SECONDS: whether TO falls in [FROM, FROM+SECONDS).
in_window() {
  python3 -c '
import sys, datetime
def at(one):
    if one.isdigit():
        return datetime.datetime.fromtimestamp(int(one), datetime.timezone.utc)
    return datetime.datetime.fromisoformat(one.replace("Z", "+00:00"))
try:
    start, end = at(sys.argv[1]), at(sys.argv[2])
except ValueError:
    sys.exit(1)
sys.exit(0 if 0 <= (end - start).total_seconds() < int(sys.argv[3]) else 1)
' "$1" "$2" "$3"
}

gap_after() {
  python3 -c '
import sys, datetime
def at(one):
    if one.isdigit():
        return datetime.datetime.fromtimestamp(int(one), datetime.timezone.utc)
    return datetime.datetime.fromisoformat(one.replace("Z", "+00:00"))
try:
    seconds = (at(sys.argv[2]) - at(sys.argv[1])).total_seconds()
except ValueError:
    sys.exit(1)
print("%dm%02ds after the removal" % (seconds // 60, seconds % 60))
' "$1" "$2"
}

# step_owner: what only the owner can do, said out loud.
step_owner() {
  fact owner-steps INFO "not run by this check, and no agent may run any of them: (1) on the workspace's own project, 'fugaro update' (binds the roster's people their two invoke grants, WC2) and 'fugaro secrets user set --project <the workspace's project>' as the person, with a value from stdin; (2) in a member, turn the switch on ('fugaro project policy …' + 'fugaro update') and launch a run there — twice, in two members — and once as a second person; (3) turn the switch off in a member and launch a run there; (4) delete the container ('fugaro secrets user rm --project <the workspace's project>') and launch a run; (5) remove the person with a run of theirs in flight, and write the moment down; (6) run this check with the twelve knobs. Nothing here launches a run or writes a secret"
}

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 850 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guard: fugaro doctor --project ${INSTALLATION} --all and fugaro version"
  say "  [network, read] gcloud projects get-iam-policy ${WORKSPACE_PROJECT:-<workspace project>} and gcloud run services list/get-iam-policy --region ${REGION} --platform=managed — the two invoke grants (U6)"
  say "  [network, read] gcloud secrets list --filter=name:${PERSON_PREFIX} — one container — and the project's own policy, where ${WORKSPACE_ACCOUNT} holds ${ACCESSOR_ROLE} under a condition naming ${PERSON_PREFIX} AND ${PERSON_TOKEN_NAME} (U5, R6)"
  say "  [network, read] gcloud logging read on ${INTAKE_SERVICE} in ${WORKSPACE_PROJECT:-<workspace project>} — the intake's own line for the person's write"
  say "  [network, read] gcloud logging read on ${CREDENTIALS_SERVICE} in ${WORKSPACE_PROJECT:-<workspace project>} — what the service served or refused each run"
  say "  [network, read] fugaro runs ls --json${REMOVED_PROJECT:+ --project ${REMOVED_PROJECT}} — the removed person's run, halted"
  say "  [not run] every launch, the switch, the deletion and the removal: the owner's, never a script's"
  say "  [nothing created] no grant, no secret, no switch and no run; the scratch directory is removed at the end"
  for key in fugaro-version xtrace invoke-grants person-container served-one served-two other-person switch-off token-deleted removed owner-steps; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no project was read and no log was opened"
}

BLOCK_KEYS=(fugaro-version xtrace dry-run invoke-grants person-container served-one
  served-two other-person switch-off token-deleted removed owner-steps restore
  scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 850 RESULT\n' >&3
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
  printf 'CHECK 850 RESULT END (exit %s)\n' "$status" >&3
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

INSTALLATION=${CHECK850_INSTALLATION:-fugaro}
WORKSPACE_PROJECT=${CHECK850_WORKSPACE_PROJECT:-}
PERSON=${CHECK850_PERSON:-}
PERSON_SUB=${CHECK850_PERSON_SUB:-}
CONTAINER=${CHECK850_CONTAINER:-}
MEMBER_ONE_PROJECT=${CHECK850_MEMBER_ONE_PROJECT:-}
MEMBER_ONE_RUN=${CHECK850_MEMBER_ONE_RUN:-}
MEMBER_TWO_PROJECT=${CHECK850_MEMBER_TWO_PROJECT:-}
MEMBER_TWO_RUN=${CHECK850_MEMBER_TWO_RUN:-}
OTHER_PERSON=${CHECK850_OTHER_PERSON:-}
OTHER_PROJECT=${CHECK850_OTHER_PROJECT:-}
OTHER_RUN=${CHECK850_OTHER_RUN:-}
OFF_PROJECT=${CHECK850_OFF_PROJECT:-}
OFF_RUN=${CHECK850_OFF_RUN:-}
DELETED_PROJECT=${CHECK850_DELETED_PROJECT:-}
DELETED_RUN=${CHECK850_DELETED_RUN:-}
REMOVED_PROJECT=${CHECK850_REMOVED_PROJECT:-}
REMOVED_RUN=${CHECK850_REMOVED_RUN:-}
REMOVED_AT=${CHECK850_REMOVED_AT:-}
LOG_AGE=${CHECK850_LOG_AGE:-24h}
LOG_LIMIT=${CHECK850_LOG_LIMIT:-200}
GRACE_SECONDS=${CHECK850_GRACE_SECONDS:-60}
REGION=${CHECK850_REGION:-us-east5}
WORKSPACE_ACCOUNT=${CHECK850_WORKSPACE_ACCOUNT:-}
WORKSPACE_ACCOUNT=${WORKSPACE_ACCOUNT#serviceAccount:}
[ -n "$WORKSPACE_ACCOUNT" ] ||
  WORKSPACE_ACCOUNT="fugaro-workspace@${WORKSPACE_PROJECT}.iam.gserviceaccount.com"
DRY_RUN=${CHECK850_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK850_KEEP_SCRATCH:-}
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$ACCOUNT" ] || die "gcloud prints no account ('gcloud config get-value account'): this check reads the workspace's own project with that account's roles. Run 'gcloud auth login' first"
  [ "$GRACE_SECONDS" -gt 0 ] || die "CHECK850_GRACE_SECONDS is ${GRACE_SECONDS}: the bound a refusal may take after the removal, so it has to be a positive number"
  scratch_up
  trap on_error ERR
  [ -n "$WORKSPACE_PROJECT" ] || unmeasured invoke-grants "CHECK850_WORKSPACE_PROJECT was not set, so this run does not know the workspace's own project to read anything off. Name it and run the check again: CHECK850_WORKSPACE_PROJECT=<project> bash scripts/live-checks/check-850.sh"
  guard_agent
  guard_xtrace
  guard_installation
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -z "$WORKSPACE_PROJECT" ]; then
    for k in person-container served-one served-two other-person switch-off token-deleted removed; do
      unmeasured "$k" "not read: CHECK850_WORKSPACE_PROJECT was not set, so the workspace's own project — where every container, policy and line of this check lives — was never named"
    done
    step_owner
    return 0
  fi
  say "reading the person's token at ${WORKSPACE_PROJECT}: the two grants, the container, and what the service served or refused"
  step_grants
  step_container
  step_served served-one "$MEMBER_ONE_RUN" "the member ${MEMBER_ONE_PROJECT:-one}"
  step_served served-two "$MEMBER_TWO_RUN" "the member ${MEMBER_TWO_PROJECT:-two}"
  step_other
  step_refused switch-off "$OFF_RUN" "$SWITCH_OFF_PHRASE" "the switch off in ${OFF_PROJECT:-a member}"
  step_refused token-deleted "$DELETED_RUN" "$TOKEN_ABSENT_PHRASE" "the token deleted in ${DELETED_PROJECT:-a member}"
  step_removed
  step_owner
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
