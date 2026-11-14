#!/usr/bin/env bash
# Check 530 — the review class reads one repository, signs one verdict, and
# reaches nothing else.
#
# The check is §8 of docs/c39-review-identity/spec.md, written by that work's
# task C3.9g2; its steps are docs/c39-review-identity/live-check.md. What it
# settles is the review half of the credentials service read from the outside:
# a review-only job with no credential of its own, a verdict signed by the
# service and nothing else, and a caller class whose refusals are the ones the
# route was built to answer.
#
# WHAT THIS SCRIPT IS, AND WHAT IT IS NOT. §8's eleven steps are the check; this
# script measures the ones a machine can read and says so, and it prints a
# prompt for each one it does not run — never a fake:
#
#   * steps 1, 2, 6 and 11 are measured here: `fugaro doctor`'s two facts, the
#     review job's own describe and its account's IAM, the signature the run
#     left in the runs bucket and the service's audit line for the signing, and
#     the caller class the routes refuse;
#   * steps 3, 4, 5, 7, 8, 9 and 10 stay yours, because each needs something
#     this script cannot be: a real `fugaro review` run and its log read by eye
#     (3), a throwaway execution of the variant minting a token whose push
#     GitHub refuses (4), the gateway's log and a hand-made model call (5), a
#     tampered COPY of the artifact and a signature pasted under another run
#     (7 — the service's own answer to those needs a verifier, and which account
#     that is is **Open item 5**, still open), the verify route's caller class
#     with a published verifier account (8, the same open item), a mutant
#     subprocess inside the container (9, U7's own observation) and the second
#     sign with the run's own identity (10). Their prompts are printed, and the
#     block carries one INFO row per step saying it is not this script's.
#
# It creates NOTHING and deletes NOTHING: every call is a read of the project,
# the runs bucket, the run's own record and the service's log, plus two POSTs to
# the verify route that the service refuses — a refusal is the answer those rows
# measure, and the one line each writes in the service's log is the service's
# own record of it.
#
# Usage:      bash scripts/live-checks/check-c39.sh --sandbox <checkout> --run <the review run id>
#             bash scripts/live-checks/check-c39.sh --sandbox <checkout> --dry-run
#             bash scripts/live-checks/check-c39.sh --help
# Needs:      bash, mktemp, date, python3, gcloud (authenticated as YOU),
#             fugaro (the installed release: the review class has to be in it)
#             and curl (the two refusal rows).
# Where:      a sandbox checkout of a repository of the sandbox project, with
#             the sandbox's own review run behind it. `--sandbox` defaults to
#             $PWD, and it must carry a fugaro.yaml. Never `belong`, never
#             `edge-devel-dimi`, never `edgeappinc`: those names are refused in
#             the checkout, the project, the region, the bucket and the job.
# Creates:    nothing in the cloud. On disk: one scratch directory (mode 0700),
#             removed from the exit trap even on a failed assertion or Ctrl-C,
#             holding the ID tokens this check mints while it is running.
# Costs:      nothing billable and no model call. It reads the review run the
#             owner already paid for; the POSTs are refused before any work.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-c39_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory and nothing else.
# Output:     progress on stderr, ONE block on stdout headed CHECK 530 RESULT,
#             then CHECK 530 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure — a tool is missing, doctor answered
#             without a project, the service's URL or an ID token could not be
#             had. A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token value reaches this script's stdout,
# stderr, the block or any file it keeps. The ID token an impersonated mint
# answers with is written straight into a mode-0600 curl config file, so it
# never enters a shell variable, a command line or the block, and the check
# refuses a shell that was started with tracing on.
#
# Knobs (all CHECK530_*): DRY_RUN, KEEP_SCRATCH, SANDBOX, RUN, PR, CODING_JOB,
# BUCKET, SERVICE_URL, TIMEOUT_SECONDS (120), TOKEN_TIMEOUT_SECONDS (60),
# LOG_FRESHNESS (30d).
set -Eeuo pipefail

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

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the code. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 1
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 530 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro curl; do
    command -v "$t" >/dev/null 2>&1 || die "Check 530 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk530-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk530.XXXXXXXX") || die "Check 530 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
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
    say "CHECK530_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check changed, said rather than assumed. It is a reading of
# the owner's own sandbox run, and the only thing it leaves behind is the one
# audit line the service writes for each of the two refusals it asked for.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created, changed or deleted: every row reads the project, the review job's own describe, its account's IAM, the artifact the review run signed, that run's record and the service's audit lines. The two refusal rows POST to the verify route and the service refuses both before any work, so the only thing they leave is one audit line each in the service's log — the service's own record of a refusal, and not this check's to delete. The review run's own spend was already paid, and its artifact is kept in the bucket; the ID tokens this check minted died with the run and never left the scratch directory"
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
# run_bounded SECONDS KEY CMD...: run one command with its output in
# $SCRATCH/KEY.out and .err, and end it after SECONDS. `timeout` is not used: it
# is GNU coreutils, and this check runs on the owner's machine, where the tool is
# absent on macOS. Sets BOUNDED_STATUS and BOUNDED_TIMED_OUT.
run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  "$@" >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$secs" ] || break
    sleep 1
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes
    kill -TERM "$pid" 2>/dev/null || true
    sleep 2
    kill -KILL "$pid" 2>/dev/null || true
  fi
  BOUNDED_STATUS=0
  wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
  # The output of a token mint is a credential: it is readable by this shell and
  # by nobody else, whatever the umask the owner's shell carries.
  chmod 600 "$SCRATCH/$key.out" "$SCRATCH/$key.err" 2>/dev/null || true
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with grep
# would report a field the release did not write the day the document is
# re-wrapped. Each reader prints `key=value` lines or tab-separated rows, so a
# shell can hold them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import re
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def flat(value):
    return " ".join(str(value or "").split())


def walk(value, out):
    if isinstance(value, dict):
        for item in value.values():
            walk(item, out)
    elif isinstance(value, list):
        for item in value:
            walk(item, out)
    elif isinstance(value, str):
        out.append(value)


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    print("name=%s" % flat(project.get("name")))
    print("gcp_project=%s" % flat(project.get("gcp_project")))
    print("region=%s" % flat(project.get("region")))
    wanted = {"credentials-service-deployed", "project-account-stored"}
    for check in doc.get("checks") or []:
        cid = flat(check.get("id"))
        if cid not in wanted:
            continue
        print("check\t%s\t%s\t%s\t%s\t%s" % (
            cid,
            yes(check.get("ok")),
            flat(check.get("severity")),
            flat(check.get("problem")),
            flat(check.get("fix")),
        ))
        if cid == "project-account-stored":
            for name in re.findall(r"review job (\S+) mounts no secret", flat(check.get("problem"))):
                print("review_job\t%s" % name)
elif what == "job":
    spec = ((doc.get("spec") or {}).get("template") or {}).get("spec") or {}
    print("service_account=%s" % flat(spec.get("serviceAccountName")))
    found = []
    for container in spec.get("containers") or []:
        for env in container.get("env") or []:
            ref = (env.get("valueFrom") or {}).get("secretKeyRef") or {}
            if ref:
                found.append("%s -> %s" % (flat(env.get("name")), flat(ref.get("name"))))
    for volume in spec.get("volumes") or []:
        secret = volume.get("secret") or {}
        if secret:
            found.append("%s -> %s" % (flat(volume.get("name")), flat(secret.get("secretName"))))
    print("secret_count=%d" % len(found))
    for item in found:
        print("secret\t%s" % item)
elif what == "policy":
    wanted = sys.argv[3:]
    hits = []
    for binding in doc.get("bindings") or []:
        role = flat(binding.get("role"))
        if "secretaccessor" not in role.lower():
            continue
        for member in binding.get("members") or []:
            short = flat(member).split(":")[-1]
            for account in wanted:
                if account and short == account:
                    hits.append("%s holds %s" % (short, role))
    print("accessor_count=%d" % len(hits))
    for hit in hits:
        print("accessor\t%s" % hit)
elif what == "record":
    cost = doc.get("cost") or {}
    print("run_id=%s" % flat(doc.get("run_id")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
    print("outcome=%s" % flat(doc.get("outcome")))
    print("cost_usd=%s" % (cost.get("model_usd") or 0))
elif what == "audit":
    purpose = flat(sys.argv[3])
    want = flat(sys.argv[4]) if len(sys.argv) > 4 else ""
    known = ["run", "repository", "job", "caller", "credential", "purpose", "tier", "status", "slip", "owner"]
    total = 0
    runs = []
    chosen = None
    for entry in doc if isinstance(doc, list) else []:
        payload = (entry.get("jsonPayload") or {})
        if flat(payload.get("msg")) != "credsvc: audit":
            continue
        if flat(payload.get("purpose")) != purpose:
            continue
        total += 1
        runs.append(flat(payload.get("run")))
        if chosen is None and (not want or flat(payload.get("run")) == want):
            chosen = payload
    print("rows=%d" % total)
    print("found=%s" % yes(chosen is not None))
    print("runs=%s" % " ".join(sorted(set(runs))))
    if chosen is not None:
        for key in known:
            print("audit_%s=%s" % (key, flat(chosen.get(key))))
        extra = sorted(k for k in chosen if k not in known and k != "msg")
        words = []
        walk(chosen, words)
        print("audit_extra=%s" % ",".join(extra))
        print("audit_text=%s" % flat(" ".join(words))[:400])
elif what == "answer":
    valid = doc.get("valid")
    print("has_valid=%s" % yes("valid" in doc))
    print("valid=%s" % yes(valid))
    print("error=%s" % flat(doc.get("error") or doc.get("error_class") or doc.get("code")))
    print("keys=%s" % ",".join(sorted(doc.keys())))
PY
}

# read_json WHAT FILE OUT: run the reader and turn its output into `key=value`
# lines in OUT, one per key (a key the reader printed twice keeps its last
# value). Returns 1 when the reader could not read the document at all.
read_json() { # read_json WHAT FILE OUT [ARGS...]
  local what=$1 file=$2 out=$3
  shift 3
  python3 "$SCRATCH/read.py" "$what" "$file" "$@" >"$out.tmp" 2>"$out.err" || return 1
  : >"$out"
  local line key value
  while IFS= read -r line; do
    case "$line" in
    *=*) ;;
    *) continue ;;
    esac
    key=${line%%=*}
    value=${line#*=}
    grep -v "^${key}=" "$out" >"$out.next" 2>/dev/null || true
    mv "$out.next" "$out"
    printf '%s=%s\n' "$key" "$value" >>"$out"
  done <"$out.tmp"
  return 0
}

field() { # field FILE KEY
  grep -m1 "^$2=" "$1" 2>/dev/null | cut -d= -f2- || true
}

row() { # row FILE PREFIX — every tab-separated row with that prefix, fields after it
  awk -F'\t' -v p="$2" '$1 == p { s = ""; for (i = 2; i <= NF; i++) s = s (i > 2 ? "\t" : "") $i; print s }' "$1" 2>/dev/null || true
}

check_row() { # check_row FILE ID — the doctor `check` row for that id, tab-separated from its id
  awk -F'\t' -v id="$2" '$1 == "check" && $2 == id { print; exit }' "$1" 2>/dev/null || true
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x): this check mints ID tokens, and a trace of the run would copy one into the log you would then paste. Run it without -x — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a token it minted"
}

# guard_agent_session: the check is the owner's. It mints ID tokens by
# impersonation and reads the service's log, both of which a coding agent's
# session is refused; refusing here rather than letting the refusal happen
# inside `gcloud` is what keeps the finding about the check and not about the
# agent.
guard_agent_session() {
  local name marker=""
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker=$name; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 530 is yours to run at your own terminal, on the sandbox review run only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# refused_installation NAME: prints the installation NAME names, or nothing at
# all when this check may read it. `belong` is the owner's own installation and
# `edge-devel-dimi` and `edgeappinc` are the two other names check340.sh refuses;
# a check that read one of them would be measuring the thing it is checking.
refused_installation() {
  case "$1" in
  *belong*) printf '%s\n' belong ;;
  *edge-devel-dimi*) printf '%s\n' edge-devel-dimi ;;
  *edgeappinc*) printf '%s\n' edgeappinc ;;
  *) printf '\n' ;;
  esac
}

# guard_name NAME: the `belong` row and the stop for one name.
guard_name() { # guard_name NAME
  local hit
  hit=$(refused_installation "$1")
  [ -n "$hit" ] || return 0
  fact belong FAIL "'$1' names the installation '$hit', which this check never reads: Check 530 reads a sandbox review run, and an installation you work in is never the subject of it"
  stop "the name '$1' matches the installation '$hit', which this check must never touch"
}

guard_belong() {
  local name
  for name in "${SANDBOX:-}" "${PROJECT:-}" "${REGION:-}" "${BUCKET:-}" "${CODING_JOB:-}" "${ACCOUNT:-}"; do
    guard_name "$name"
  done
  fact belong PASS "the checkout (${SANDBOX:-$PWD}), the project, the region, the bucket and the job this check reads name neither belong nor edge-devel-dimi nor edgeappinc: it is the sandbox installation's own review run"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 530 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version — the build this check drives"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the installation's project and region, the credentials-service-deployed row and the census line naming the review job"
  say "  [network, read] gcloud run jobs describe <the review job> --project ${PROJECT:-<the doctor project>} --region ${REGION:-<the doctor region>} — whether it mounts a secret at all"
  say "  [network, read] gcloud projects get-iam-policy ${PROJECT:-<the doctor project>} — whether the review account holds any secretAccessor"
  say "  [network, read] gcloud storage ls gs://${BUCKET:-<runs bucket>}/runs/*/${RUN:-<the review run>}/artifacts/review.json.sig, then cat — the signature the run left, one line of lowercase hex"
  say "  [network, read] gcloud storage cat gs://…/result.json — the run's own record: its repository and its spend"
  say "  [network, read] gcloud logging read (purpose=review_sign) — the service's audit line for that signing: the account, the slug, the run, and no pull requests"
  say "  [network, read] gcloud run services describe ${SERVICE_NAME} --format=value(status.url) — the URL both routes answer on"
  say "  [network] POST ${SERVICE_URL:-<the service URL>}/v1/review/verify with NO token — a typed refusal is the answer this row measures"
  say "  [network] the same call with an ID token minted by impersonating the review job's own account — refused too, because a verifier is not a run"
  if [ -n "$CODING_JOB" ]; then
    say "  [network] the same two calls with the coding job's account (${CODING_JOB}) — refused on every route"
  else
    say "  [not asked for] the coding job's account: pass --coding-job <name> to measure that arm; without it the row says so"
  fi
  say "  [nothing created] no job, no build, no container, no binding, no account and no model call: every call is a read, and the two POSTs are refused before any work"
  say "  [yours, printed and not run] steps 3, 4, 5, 7, 8, 9 and 10 of the check — a real review run, a throwaway mint whose push GitHub refuses, the gateway's log and a hand-made model call, a tampered copy, the verifier class, a mutant subprocess, and the second sign"
  say "the block a real run prints carries one row per fact: $(printf '%s ' "${BLOCK_KEYS[@]}")"
  say "A dry run measured none of them, so its block carries only what it did settle — the guards and this row."
  fact dry-run PASS "guards and the plan only: no gcloud call, no token minted, no POST, no model call, no 'fugaro doctor' of the installation and no reading of the project, the bucket or the service"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong sandbox project region bucket
  repo pr run doctor-service review-jobs job-no-secret variant-sa-accessor
  verdict-sig sign-line verify-unauthenticated verify-review-refused gate-coding
  cost step-3 step-4 step-5 step-7 step-8 step-9 step-10 restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 530 RESULT\n' >&3
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
  printf 'CHECK 530 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------- the steps
step_version() {
  local v
  v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  # Not a release gate: the review class is what this check reads, and the
  # rows below say themselves when the build does not carry it (a doctor with
  # no credentials-service row, no census line naming a review job).
  fact fugaro-version PASS "${v:-fugaro version printed nothing} (the build this check drives: the review job, the routes and the audit line it reads are the ones you have installed)"
}

# step_sandbox: the premise. The project and the region come from the checkout
# itself (`fugaro doctor --json`), so the rows below are about the installation
# the owner works in rather than one this check guessed.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox installation)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the project this check reads is the one that checkout belongs to"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$TIMEOUT" doctor "$FUGARO_BIN" doctor --json
  cp "$SCRATCH/doctor.out" "$SCRATCH/doctor.json" 2>/dev/null || true
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the installation this checkout belongs to, and every row below is about that installation. Its output is in $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.json" "$SCRATCH/doctor.txt"; then
    unmeasured sandbox "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  PROJECT=${PROJECT:-$(field "$SCRATCH/doctor.txt" gcp_project)}
  REGION=${REGION:-$(field "$SCRATCH/doctor.txt" region)}
  BUCKET=${BUCKET:-fugaro-runs-${PROJECT}}
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox installation is in, and the rows below read that project's jobs, its runs bucket and its service. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  if [ -z "$REGION" ]; then
    unmeasured sandbox "fugaro doctor --json named project ${PROJECT} and no region: every gcloud call below needs the region of the installation's jobs and its service, and this check will not guess one"
    return 0
  fi
  # The names doctor answered with are checked here too: the guard above ran
  # before any tool call, when the project was not known yet.
  local hit
  for name in "$PROJECT" "$REGION" "$BUCKET"; do
    hit=$(refused_installation "$name")
    [ -n "$hit" ] || continue
    stop "the installation the checkout at $SANDBOX belongs to is '$hit' ('$name'): this check is never run there"
  done
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout and it belongs to ${PROJECT} (region ${REGION}): the rows below read this installation's review job, its runs bucket and its service"
  fact project PASS "${PROJECT} — the installation the sandbox checkout belongs to, read from doctor rather than guessed"
  fact region PASS "${REGION} — the region its jobs and its credentials service are in"
  fact bucket PASS "gs://${BUCKET} — the runs bucket the rows below read the review run's artefact and record from"
  fact pr INFO "the sandbox pull request this review run reviewed: ${PR:-not given (pass --pr <N>; the prompts for steps 3 to 5 name it)}"
  fact run INFO "the review run this check reads: ${RUN:-not given}"
}

# step_doctor: §8's step 1 — the two facts `fugaro doctor` reports. A warning
# line instead is the finding, and the check stops there: every row below reads
# a service or a variant doctor has just said is not there.
step_doctor() {
  if [ ! -s "$SCRATCH/doctor.txt" ]; then
    unmeasured doctor-service "there is no doctor document to read the two facts from: the row above says why"
    unmeasured review-jobs "there is no doctor document to read the census line from: the row above says why"
    return 0
  fi
  local service
  service=$(check_row "$SCRATCH/doctor.txt" credentials-service-deployed)
  if [ -z "$service" ]; then
    fact doctor-service FAIL "fugaro doctor carries no credentials-service-deployed row: this build predates the credentials service's own doctor check, so the two facts §8's step 1 reads are not in it. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you. What doctor printed is in $SCRATCH/doctor.out"
    unmeasured review-jobs "no doctor row names the installation's job set: the row above says why"
    return 0
  fi
  local ok severity problem fix
  ok=$(printf '%s' "$service" | cut -f2)
  severity=$(printf '%s' "$service" | cut -f3)
  problem=$(printf '%s' "$service" | cut -f4)
  fix=$(printf '%s' "$service" | cut -f5)
  case "$severity" in
  warning | error)
    fact doctor-service FAIL "doctor reports the credentials service as ${severity}: ${problem} — its remedy reads: ${fix}. §8's step 1 is this row, and a ${severity} line is the finding; every row below reads a service that is not serving, so the check stops here"
    stop "doctor's credentials-service-deployed row is ${severity}: ${problem}"
    ;;
  esac
  fact doctor-service PASS "doctor's credentials-service-deployed row is ${ok:-not answered} (severity ${severity:-none}): ${problem:-the service is deployed for this installation}"

  local census review_jobs=""
  census=$(check_row "$SCRATCH/doctor.txt" project-account-stored)
  if [ -z "$census" ]; then
    unmeasured review-jobs "fugaro doctor carries no project-account-stored row, which is the census line that names the installation's review-only job: without it this check cannot say which job §8's step 2 describes. Run 'fugaro doctor --all' yourself and name the job with --coding-job-style reads, or run this check on a build whose doctor carries the row"
    return 0
  fi
  REVIEW_JOBS=$(row "$SCRATCH/doctor.txt" review_job | tr '\n' ' ')
  if [ -z "${REVIEW_JOBS// /}" ]; then
    fact review-jobs FAIL "doctor's census line names no review job: it reads '$(printf '%s' "$census" | cut -f4)'. The census names each account's job and its review-only variant beside it ('review job <name> mounts no secret'), so an installation whose deployed job set has no variant is the finding §8's step 2 rests on — the variant is the job that holds no credential. Check the installation's profiles and its credential bundle, then run this check again"
    return 0
  fi
  fact review-jobs PASS "doctor's census names $(printf '%s' "$REVIEW_JOBS" | wc -w | tr -d ' ') review-only job(s) — $(printf '%s' "$REVIEW_JOBS" | sed 's/ $//') — each of them 'mounts no secret' in doctor's own words, which is CSR6's second fact and the job §8's step 2 describes"
}

# step_job: §8's step 2, first half — the variant holds no credential at all
# (ruling O1). The job's own describe is what says it: no secret in an env var,
# none in a volume. The account it runs as is read in the same call, because the
# rows below are about that identity.
step_job() {
  if [ -z "${REVIEW_JOBS// /}" ]; then
    unmeasured job-no-secret "there is no review job to describe: the row above says why"
    return 0
  fi
  : >"$SCRATCH/job-sa"
  local job described=0 offenders="" seen="" sa
  for job in $REVIEW_JOBS; do
    say "describing the review job: gcloud run jobs describe ${job} --region ${REGION}"
    run_bounded "$TIMEOUT" "job-${job}" gcloud run jobs describe "$job" \
      --project="$PROJECT" --region="$REGION" --format=json
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      offenders="${offenders:+$offenders; }${job} could not be described (exit ${BOUNDED_STATUS}): $(head -c 200 "$SCRATCH/job-${job}.err" 2>/dev/null | tr '\n' ' ')"
      continue
    fi
    described=$((described + 1))
    if ! read_json job "$SCRATCH/job-${job}.out" "$SCRATCH/job-${job}.txt"; then
      offenders="${offenders:+$offenders; }${job}'s describe is not a document this check could read"
      continue
    fi
    sa=$(field "$SCRATCH/job-${job}.txt" service_account)
    printf '%s|%s\n' "$job" "$sa" >>"$SCRATCH/job-sa"
    seen="${seen:+$seen, }${job} (as ${sa:-no account named})"
    local count
    count=$(field "$SCRATCH/job-${job}.txt" secret_count)
    if [ "${count:-0}" != 0 ]; then
      offenders="${offenders:+$offenders; }${job} mounts $(field "$SCRATCH/job-${job}.txt" secret_count) secret(s): $(row "$SCRATCH/job-${job}.txt" secret | tr '\n' ' ')"
    fi
  done
  REVIEW_SA=$(sed -n '1s/^[^|]*|//p' "$SCRATCH/job-sa")
  if [ "$described" = 0 ]; then
    unmeasured job-no-secret "no review job could be described, so this check cannot say what it mounts: ${offenders}"
    return 0
  fi
  if [ -n "$offenders" ]; then
    fact job-no-secret FAIL "${offenders}: §8's step 2 and ruling O1 are that the review-only variant holds NO credential at all — not the account's Claude token, which it no longer needs because its model calls come to the service, and never the App key. A secret mounted on it is the finding this row exists for"
    return 0
  fi
  fact job-no-secret PASS "${seen}: every review-only job this installation deploys mounts no secret at all — no env reference, no volume, nothing — which is ruling O1 read from the platform rather than from the module"
}

# step_variant_sa: §8's step 2, second half — the variant's service account
# holds no `secretAccessor`, on the App key, on the bundle, or on anything else.
step_variant_sa() {
  if [ ! -s "$SCRATCH/job-sa" ]; then
    unmeasured variant-sa-accessor "there is no review account to read a policy for: the row above says why"
    return 0
  fi
  local accounts
  accounts=$(cut -d'|' -f2 "$SCRATCH/job-sa" | sed '/^$/d' | sort -u | tr '\n' ' ')
  if [ -z "${accounts// /}" ]; then
    fact variant-sa-accessor FAIL "no review job names a service account: $(cut -d'|' -f1 "$SCRATCH/job-sa" | tr '\n' ' '). A job that runs as the project's default account is a job whose reach nobody named, and §8's step 2 reads this account's IAM"
    return 0
  fi
  say "reading the project's IAM policy: gcloud projects get-iam-policy ${PROJECT}"
  run_bounded "$TIMEOUT" policy gcloud projects get-iam-policy "$PROJECT" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured variant-sa-accessor "gcloud could not read ${PROJECT}'s IAM policy (exit ${BOUNDED_STATUS}): this row is about what the review account holds, and it cannot be answered without it. What gcloud printed is in $SCRATCH/policy.err"
    return 0
  fi
  if ! read_json policy "$SCRATCH/policy.out" "$SCRATCH/policy.txt" $accounts; then
    unmeasured variant-sa-accessor "the project's IAM policy is not a document this check could read: what gcloud printed is in $SCRATCH/policy.out"
    return 0
  fi
  local count hits
  count=$(field "$SCRATCH/policy.txt" accessor_count)
  hits=$(row "$SCRATCH/policy.txt" accessor | tr '\n' ';')
  if [ "${count:-0}" != 0 ]; then
    fact variant-sa-accessor FAIL "the review account holds a secret-reading role: ${hits} — §8's step 2 is that it holds no secretAccessor on the App key, on the bundle or on anything else, and a secretAccessor is a binding that reads a VALUE. Remove the binding, re-apply the root, and run this check again"
    return 0
  fi
  fact variant-sa-accessor PASS "${accounts}holds no secretAccessor in ${PROJECT}'s IAM policy — not on the App key, not on the bundle, not on anything: the review account is an identity that cannot read a secret value, which is ruling O1's second half"
}

# step_artifact: §8's step 6, first half — `runs/<slug>/<review-run>/artifacts/
# review.json.sig` exists and is one line of lowercase hex. The run's own record
# is read in the same step, because it is what names the repository the slug
# stands for and the spend §8's step 11 reports.
step_artifact() {
  if [ -z "$RUN" ]; then
    unmeasured verdict-sig "no review run was named: pass --run <the review run id> (the id 'fugaro review --pr <N>' printed when it launched)"
    unmeasured repo "no review run was named, so there is no record to read the repository from"
    unmeasured cost "no review run was named, so there is no record to read the spend from"
    return 0
  fi
  say "looking for the signature: gcloud storage ls gs://${BUCKET}/runs/*/${RUN}/artifacts/${SIG_NAME}"
  run_bounded "$TIMEOUT" sig-ls gcloud storage ls "gs://${BUCKET}/runs/*/${RUN}/artifacts/${SIG_NAME}" --project="$PROJECT"
  local object
  object=$(grep -m1 "review.json.sig" "$SCRATCH/sig-ls.out" 2>/dev/null | tr -d '\r' || true)
  if [ -z "$object" ]; then
    local dir
    run_bounded "$TIMEOUT" sig-dir gcloud storage ls "gs://${BUCKET}/runs/*/${RUN}/artifacts/" --project="$PROJECT"
    dir=$(head -c 400 "$SCRATCH/sig-dir.out" 2>/dev/null | tr '\n' ' ' || true)
    fact verdict-sig FAIL "no ${SIG_NAME} object is under gs://${BUCKET}/runs/*/${RUN}/artifacts/, and §8's step 6 is that the review run leaves the signature of its verdict there. Under that prefix this check found: ${dir:-nothing at all, so even the prefix itself is absent}. A run that reached neither is not one this check can read a signature from; a run whose artifacts are there without the sidecar is the finding — unsigned is not a verdict to act on"
    unmeasured repo "there is no artifact path, so no record of this run's was read"
    unmeasured cost "there is no artifact path, so no record of this run's was read"
    return 0
  fi
  local slug
  slug=$(printf '%s' "$object" | sed -n 's#^gs://[^/]*/runs/\(.*\)/'"$RUN"'/artifacts/.*#\1#p')
  if [ -z "$slug" ]; then
    unmeasured verdict-sig "gcloud storage ls answered with a path this check cannot take the repository's slug from: ${object}"
    unmeasured repo "the slug could not be read from the artifact path"
    unmeasured cost "the slug could not be read from the artifact path"
    return 0
  fi
  RUN_REF="runs/${slug}/${RUN}"
  say "reading the signature: gcloud storage cat ${object}"
  run_bounded "$TIMEOUT" sig gcloud storage cat "$object" --project="$PROJECT"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact verdict-sig FAIL "${object} could not be read (exit ${BOUNDED_STATUS}): the signature is the whole of §8's step 6, and a sidecar nothing can read is not one the coordinator can check either. What gcloud printed is in $SCRATCH/sig.err"
    return 0
  fi
  local content lines bad=""
  content=$(tr -d '\r' <"$SCRATCH/sig.out" | sed -e '$ { /^$/d; }')
  lines=$(printf '%s\n' "$content" | wc -l | tr -d ' ')
  [ -n "$content" ] || bad="it is empty"
  [ "$lines" = 1 ] || bad="${bad:+$bad; }it is ${lines} lines rather than one"
  case "$content" in
  *[!0-9a-f]* | "") bad="${bad:+$bad; }it is not lowercase hex alone" ;;
  esac
  [ "${#content}" = 64 ] || bad="${bad:+$bad; }it is ${#content} characters, and the service's own signature is 64"
  if [ -n "$bad" ]; then
    fact verdict-sig FAIL "${object} is not one line of lowercase hex: ${bad}. What it holds (first 32 characters) reads '$(printf '%s' "$content" | head -c 32)'. §8's step 6 measures this object's shape, and a signature that is not the service's own is not one a coordinator can verify"
    return 0
  fi
  fact verdict-sig PASS "${object} holds one line of 64 lowercase hex characters (${content:0:16}…), at the path §8 names: runs/<slug>/<review-run>/artifacts/${SIG_NAME}, with ${slug} the repository's slug"
  fact repo PASS "${slug} — the slug the signature sits under, and the repository the run's record names below"

  say "reading the run's record: gcloud storage cat gs://${BUCKET}/${RUN_REF}/result.json"
  run_bounded "$TIMEOUT" record gcloud storage cat "gs://${BUCKET}/${RUN_REF}/result.json" --project="$PROJECT"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact cost INFO "the run's record could not be read (exit ${BOUNDED_STATUS}), so the spend §8's step 11 reports is not in this block: what gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact cost INFO "the run's record is not a document this check could read, so the spend §8's step 11 reports is not in this block: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  RECORDED_REPO=$(field "$SCRATCH/record.txt" repo)
  fact cost INFO "the review run's own record reads status=$(field "$SCRATCH/record.txt" status), outcome=$(field "$SCRATCH/record.txt" outcome), cost.model_usd=$(field "$SCRATCH/record.txt" cost_usd) on ${RECORDED_REPO:-a repository it does not name}: that spend is what this check costs, the signature above is the artifact it kept, and nothing else of the check's was created"
}

# step_sign_line: §8's step 6, second half — the sign line names the account,
# the repository slug and the run, and carries no pull requests: the field set
# has nowhere to put them. The line is the service's audit line for the signing
# (purpose review_sign), read from Cloud Logging.
step_sign_line() {
  if [ -z "$RUN" ] || [ -z "$RUN_REF" ]; then
    unmeasured sign-line "there is no review run whose signing could be read: the row above says why"
    return 0
  fi
  say "reading the service's audit line: gcloud logging read (purpose=review_sign)"
  run_bounded "$TIMEOUT" audit gcloud logging read \
    "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SERVICE_NAME}\" AND jsonPayload.msg=\"credsvc: audit\" AND jsonPayload.purpose=\"review_sign\"" \
    --project="$PROJECT" --format=json --limit=100 --freshness="$LOG_FRESHNESS" --order=desc
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sign-line "gcloud logging read did not answer (exit ${BOUNDED_STATUS}): this row is about the line the service writes when it signs, and it cannot be read without the log. What gcloud printed is in $SCRATCH/audit.err"
    return 0
  fi
  if ! read_json audit "$SCRATCH/audit.out" "$SCRATCH/audit.txt" review_sign "$RUN"; then
    unmeasured sign-line "the log read is not a document this check could read: what gcloud printed is in $SCRATCH/audit.out"
    return 0
  fi
  local rows found
  rows=$(field "$SCRATCH/audit.txt" rows)
  found=$(field "$SCRATCH/audit.txt" found)
  if [ "${rows:-0}" = 0 ]; then
    fact sign-line FAIL "the service wrote no review_sign audit line in the last ${LOG_FRESHNESS} (of the ${SERVICE_NAME} service in ${PROJECT}): every act of the service that touches a credential writes exactly one line, and a signing with no line is the one thing that rule forbids. If this run signed minutes ago, Cloud Logging's own lag is the other reading — wait and run this check again"
    return 0
  fi
  if [ "$found" != true ]; then
    fact sign-line FAIL "the service wrote ${rows} review_sign line(s) in the last ${LOG_FRESHNESS} and none of them names run ${RUN} (they name: $(field "$SCRATCH/audit.txt" runs)): §8's step 6 reads the line for THIS run's signing, and a review run whose own signing left no line is the finding. The other lines belong to other runs, and 'fugaro runs ls' is where you can see whether ${RUN} is this repository's run at all"
    return 0
  fi
  local repo job caller credential tier status extra text bad=""
  repo=$(field "$SCRATCH/audit.txt" audit_repository)
  job=$(field "$SCRATCH/audit.txt" audit_job)
  caller=$(field "$SCRATCH/audit.txt" audit_caller)
  credential=$(field "$SCRATCH/audit.txt" audit_credential)
  tier=$(field "$SCRATCH/audit.txt" audit_tier)
  status=$(field "$SCRATCH/audit.txt" audit_status)
  extra=$(field "$SCRATCH/audit.txt" audit_extra)
  text=$(field "$SCRATCH/audit.txt" audit_text)
  if [ -z "$repo" ]; then
    bad="it names no repository"
  elif [ "$repo" != "$(printf '%s' "$RUN_REF" | sed 's#^runs/##; s#/'"$RUN"'$##')" ]; then
    bad="it names repository ${repo} and the signature sits under $(printf '%s' "$RUN_REF" | sed 's#^runs/##; s#/'"$RUN"'$##')"
  fi
  if [ -z "$credential" ]; then
    bad="${bad:+$bad; }it names no credential"
  else
    case "$credential" in
    *key* | *sign*) ;;
    *) bad="${bad:+$bad; }its credential is '${credential}', which does not name a signing key" ;;
    esac
  fi
  [ -n "$extra" ] && bad="${bad:+$bad; }it carries fields this check does not know (${extra})"
  case "${text}${job}" in
  *pull* | *PR*) bad="${bad:+$bad; }it carries a pull request, and the field set has nowhere to put one" ;;
  esac
  if [ -n "$bad" ]; then
    fact sign-line FAIL "the review_sign line for ${RUN} is not the one §8's step 6 describes: ${bad}. It reads: run=${RUN} repository=${repo:-none} job=${job:-none} caller=${caller:-none} credential=${credential:-none} tier=${tier:-none} status=${status:-none}"
    return 0
  fi
  local expected_sa
  expected_sa=$(awk -F'|' -v j="$job" '$1 == j { print $2; exit }' "$SCRATCH/job-sa" 2>/dev/null || true)
  if [ -n "$expected_sa" ] && [ -n "$caller" ] && [ "$caller" != "$expected_sa" ]; then
    fact sign-line FAIL "the review_sign line for ${RUN} names caller ${caller} and the review job ${job} runs as ${expected_sa}: the account that called is the identity the service verified, and a line whose caller is not the job's own account is a signing from somewhere else. The line reads: run=${RUN} repository=${repo} job=${job} caller=${caller} credential=${credential} tier=${tier} status=${status}"
    return 0
  fi
  fact sign-line PASS "the service's audit line for ${RUN}'s signing names the run, the repository ${repo} (the slug the signature sits under), the job ${job} calling as ${caller}, and the signing key '${credential}' as the credential, with tier '${tier:-none}' and status ${status:-none} — and it carries no pull request and no field §8 does not name. It reads: ${text}"
}

# ------------------------------------------------------- the route's callers
# step_service_url: the URL both routes answer on, read from the service rather
# than from a config, so the calls below go where this installation's service is.
step_service_url() {
  if [ -n "$SERVICE_URL" ]; then
    fact service-url PASS "${SERVICE_URL} — the URL both review routes answer on, given with --service-url"
    return 0
  fi
  say "reading the service's URL: gcloud run services describe ${SERVICE_NAME} --region ${REGION}"
  run_bounded "$TIMEOUT" service gcloud run services describe "$SERVICE_NAME" \
    --project="$PROJECT" --region="$REGION" --format='value(status.url)'
  SERVICE_URL=$(head -n 1 "$SCRATCH/service.out" 2>/dev/null | tr -d '\r' || true)
  if [ -z "$SERVICE_URL" ]; then
    unmeasured service-url "gcloud could not read ${SERVICE_NAME}'s URL in ${PROJECT}/${REGION} (exit ${BOUNDED_STATUS}): the two refusal rows below POST to it, and without it this check cannot ask the question they measure. What gcloud printed is in $SCRATCH/service.err"
    return 0
  fi
  fact service-url PASS "${SERVICE_URL} — the service's own URL, read from Cloud Run rather than from a config"
}

# mint_token ACCOUNT KEY: put an ID token for ACCOUNT, minted by impersonation,
# straight into a mode-0600 curl config file. It never enters a shell variable,
# a command line, the block or a log.
mint_token() { # mint_token ACCOUNT KEY
  local account=$1 key=$2
  run_bounded "$TOKEN_TIMEOUT" "token-${key}" gcloud auth print-identity-token \
    --impersonate-service-account="$account" --audiences="$SERVICE_URL" --include-email
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    return 1
  fi
  if [ ! -s "$SCRATCH/token-${key}.out" ]; then
    return 1
  fi
  {
    printf 'header = "Authorization: Bearer '
    tr -d '\r\n' <"$SCRATCH/token-${key}.out"
    printf '"\n'
  } >"$SCRATCH/token-${key}.curl"
  chmod 600 "$SCRATCH/token-${key}.curl"
  rm -f "$SCRATCH/token-${key}.out" "$SCRATCH/token-${key}.err"
  return 0
}

# post_verify KEY CONFIG: POST the framed pair's own fields to the verify route,
# with CONFIG's Authorization header (or none when CONFIG is empty). Prints the
# body to $SCRATCH/KEY.out and sets POST_STATUS. The body names the real run and
# the real digest of what was signed, so a service that ADMITS the call answers
# with a verdict about this run rather than about a made-up one.
post_verify() { # post_verify KEY [CONFIG]
  local key=$1 config=${2:-}
  local body="$SCRATCH/post-${key}.json"
  cat >"$body" <<JSON
{"account":"${VERIFY_ACCOUNT}","digest":"${VERIFY_DIGEST}","signature":"${VERIFY_SIGNATURE}","run":"${RUN}","repository":"${VERIFY_REPO}"}
JSON
  if [ -n "$config" ]; then
    run_bounded "$TIMEOUT" "$key" curl --config "$config" -sS -o "$SCRATCH/$key.out" \
      -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
      --data @"$body" "${SERVICE_URL}${VERIFY_PATH}"
  else
    run_bounded "$TIMEOUT" "$key" curl -sS -o "$SCRATCH/$key.out" \
      -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
      --data @"$body" "${SERVICE_URL}${VERIFY_PATH}"
  fi
  chmod 600 "$SCRATCH/$key.out" 2>/dev/null || true
}

# read_answer KEY DETAIL: read one POST's answer. Sets ANSWER_KIND to `verdict`
# (the service answered whether the pair is valid) or `refusal` (it refused the
# call), and ANSWER_TEXT to the sentence it printed.
read_answer() { # read_answer KEY
  local key=$1
  ANSWER_KIND=unreadable
  ANSWER_TEXT=$(tr -d '\r\n' <"$SCRATCH/$key.out" 2>/dev/null | head -c 400 || true)
  if [ ! -s "$SCRATCH/$key.out" ]; then
    ANSWER_TEXT="nothing at all (curl exited ${BOUNDED_STATUS})"
    return 0
  fi
  if ! read_json answer "$SCRATCH/$key.out" "$SCRATCH/$key.txt"; then
    return 0
  fi
  if [ "$(field "$SCRATCH/$key.txt" has_valid)" = true ]; then
    ANSWER_KIND=verdict
    ANSWER_TEXT="valid=$(field "$SCRATCH/$key.txt" valid)"
  else
    ANSWER_KIND=refusal
    ANSWER_TEXT=$(field "$SCRATCH/$key.txt" error)
    [ -n "$ANSWER_TEXT" ] || ANSWER_TEXT="a body with no valid field and no error: $(printf '%s' "$ANSWER_TEXT")"
  fi
}

# step_verify_class: §8's step 11, the half a script can ask — the verify route
# refuses a caller with no token at all, and it refuses the review job's own
# account. Step 8's verifier class is NOT read here: which account is in the
# verifier list is Open item 5, still open, so the prompt for that step is
# printed and this check claims nothing about it.
step_verify_class() {
  if [ -z "$SERVICE_URL" ]; then
    unmeasured verify-unauthenticated "there is no service URL to call: the row above says why"
    unmeasured verify-review-refused "there is no service URL to call: the row above says why"
    return 0
  fi
  if [ -z "$RUN" ]; then
    unmeasured verify-unauthenticated "no review run was named, so there is no pair to ask the service about"
    unmeasured verify-review-refused "no review run was named, so there is no pair to ask the service about"
    return 0
  fi
  if [ -z "$VERIFY_DIGEST" ] || [ -z "$VERIFY_SIGNATURE" ]; then
    unmeasured verify-unauthenticated "the artifact's digest could not be read (or the run left no signature), so this check has no pair to ask the route about: 'a typed refusal' would then be the refusal of a call that was malformed, which is not the reading §8's step 8 is after"
    unmeasured verify-review-refused "the artifact's digest could not be read (or the run left no signature), so this check has no pair to ask the route about"
    return 0
  fi
  say "asking the verify route with no token at all: POST ${SERVICE_URL}${VERIFY_PATH}"
  post_verify no-token
  read_answer no-token
  case "$ANSWER_KIND" in
  refusal)
    fact verify-unauthenticated PASS "the route refused a call with no token, and the sentence it printed is: ${ANSWER_TEXT}. §8's step 8 reads the same thing from the other end: the class is the token's, so a call without one is refused before the key is ever reached"
    ;;
  verdict)
    fact verify-unauthenticated FAIL "the verify route answered ${ANSWER_TEXT} to a call that carried NO token: the class that route admits is the caller's token, and a call without one is refused rather than answered. This is a finding about the route itself, and it is the one row of §8's step 8 that does not wait on Open item 5"
    ;;
  *)
    unmeasured verify-unauthenticated "the verify route's answer could not be read (curl exit ${BOUNDED_STATUS}): it printed ${ANSWER_TEXT}, which is neither a refusal this check knows nor a verdict. What it printed is in $SCRATCH/no-token.out"
    ;;
  esac

  if [ -z "$REVIEW_SA" ]; then
    unmeasured verify-review-refused "no review job's account was read, so this check cannot call the route as the review class: the rows above say why"
    return 0
  fi
  say "minting an ID token for the review job's own account: gcloud auth print-identity-token --impersonate-service-account=${REVIEW_SA}"
  if ! mint_token "$REVIEW_SA" review; then
    unmeasured verify-review-refused "an ID token for ${REVIEW_SA} could not be minted (exit ${BOUNDED_STATUS}): minting one by impersonation is your own IAM's business, not the code's, and this check will not call the route as an identity it could not become. What gcloud printed is in $SCRATCH/token-review.err"
    return 0
  fi
  say "asking the verify route as the review job: POST ${SERVICE_URL}${VERIFY_PATH}"
  post_verify review "$SCRATCH/token-review.curl"
  read_answer review
  rm -f "$SCRATCH/token-review.curl"
  case "$ANSWER_KIND" in
  refusal)
    fact verify-review-refused PASS "the verify route refused ${REVIEW_SA} — the account the review job itself runs as — and its sentence reads: ${ANSWER_TEXT}. A verifier is not a run, so the review class is refused the verify route, which is §8's step 11 read from the route's own answer"
    ;;
  verdict)
    fact verify-review-refused FAIL "the verify route answered ${ANSWER_TEXT} to a call made as ${REVIEW_SA}, the account the review job runs as: §8's step 11 is that the review class is refused the verify route, and a review account that can verify its own verdict is the widening this row exists to catch. The answer is in $SCRATCH/review.out"
    ;;
  *)
    unmeasured verify-review-refused "the verify route's answer could not be read (curl exit ${BOUNDED_STATUS}): it printed ${ANSWER_TEXT}, which is neither a refusal this check knows nor a verdict. What it printed is in $SCRATCH/review.out"
    ;;
  esac
}

# step_gate_coding: §8's step 11, first half — a CODING shared project job's
# account is refused on every route. It needs the coding job's name, which this
# check will not guess: the installation's job set is doctor's census to name,
# and the owner passes it.
step_gate_coding() {
  if [ -z "$CODING_JOB" ]; then
    fact gate-coding INFO "not asked for: pass --coding-job <the coding shared project job of this installation> and this row calls both routes as that job's account and reads the refusals. Without it this check makes no claim about the coding class, and §8's step 11's first half is yours to read by hand ('gcloud run jobs list' names the job set doctor's census describes)"
    return 0
  fi
  if [ -z "$SERVICE_URL" ]; then
    unmeasured gate-coding "there is no service URL to call: the row above says why"
    return 0
  fi
  say "describing the coding job: gcloud run jobs describe ${CODING_JOB} --region ${REGION}"
  run_bounded "$TIMEOUT" coding-job gcloud run jobs describe "$CODING_JOB" \
    --project="$PROJECT" --region="$REGION" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured gate-coding "${CODING_JOB} could not be described (exit ${BOUNDED_STATUS}): this row is about the account that job runs as, and it cannot name it without the describe. What gcloud printed is in $SCRATCH/coding-job.err"
    return 0
  fi
  if ! read_json job "$SCRATCH/coding-job.out" "$SCRATCH/coding-job.txt"; then
    unmeasured gate-coding "${CODING_JOB}'s describe is not a document this check could read: what gcloud printed is in $SCRATCH/coding-job.out"
    return 0
  fi
  local coding_sa
  coding_sa=$(field "$SCRATCH/coding-job.txt" service_account)
  if [ -z "$coding_sa" ]; then
    fact gate-coding FAIL "${CODING_JOB} names no service account: a coding job that runs as the project's default account is a job whose reach nobody named, which is the shape this row's refusal is supposed to be about"
    return 0
  fi
  say "minting an ID token for the coding job's account: gcloud auth print-identity-token --impersonate-service-account=${coding_sa}"
  if ! mint_token "$coding_sa" coding; then
    unmeasured gate-coding "an ID token for ${coding_sa} could not be minted (exit ${BOUNDED_STATUS}): minting one by impersonation is your own IAM's business, not the code's. What gcloud printed is in $SCRATCH/token-coding.err"
    return 0
  fi
  say "asking the verify route as the coding job: POST ${SERVICE_URL}${VERIFY_PATH}"
  post_verify coding "$SCRATCH/token-coding.curl"
  read_answer coding
  local verify_said="$ANSWER_TEXT" refused=""
  case "$ANSWER_KIND" in
  refusal) refused="verify" ;;
  verdict)
    rm -f "$SCRATCH/token-coding.curl"
    fact gate-coding FAIL "the verify route answered ${ANSWER_TEXT} to ${coding_sa}, the coding job's own account: §8's step 11 is that a coding shared project job is refused on every route, and a coding account that can verify a verdict is the gate failing where it was written. The answer is in $SCRATCH/coding.out"
    return 0
    ;;
  *)
    rm -f "$SCRATCH/token-coding.curl"
    unmeasured gate-coding "the verify route's answer to ${coding_sa} could not be read (curl exit ${BOUNDED_STATUS}): it printed ${ANSWER_TEXT}. What it printed is in $SCRATCH/coding.out"
    return 0
    ;;
  esac
  say "asking the sign route as the coding job: POST ${SERVICE_URL}${SIGN_PATH}"
  run_bounded "$TIMEOUT" coding-sign curl --config "$SCRATCH/token-coding.curl" -sS -o "$SCRATCH/coding-sign.out" \
    -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    --data-binary '{"account":"","digest":"","signature":"","run":"","repository":""}' "${SERVICE_URL}${SIGN_PATH}"
  read_answer coding-sign
  rm -f "$SCRATCH/token-coding.curl"
  if [ "$ANSWER_KIND" = refusal ]; then
    fact gate-coding PASS "${coding_sa} — the coding shared project job's own account — is refused the verify route ('${verify_said}') and the sign route ('$(field "$SCRATCH/coding-sign.txt" error)') alike: the gate still holds where it was written, and both refusals' own sentences are what this row carries"
  else
    fact gate-coding FAIL "the verify route refused ${coding_sa} ('${verify_said}'), and the sign route answered ${ANSWER_TEXT} to it: §8's step 11 is that a coding shared project job is refused on EVERY route, and this one was not. What it printed is in $SCRATCH/coding-sign.out"
  fi
}

# ------------------------------------------------------- the steps that are yours
# unscripted: §8's steps that need a person, a real run or an open answer. Each
# gets one INFO row here and a printed prompt above it — the check says which of
# its eleven steps it did not run rather than leaving them to be assumed.
unscripted() {
  local n=$1 needs=$2
  fact "step-${n}" INFO "not this script's: ${needs} — the prompt for it is printed above, and this row says so rather than faking it"
}

# step_prompts: the printed prompts. They are the owner's steps in the order §8
# numbers them, with the command each one types where §8 gives one.
step_prompts() {
  say ""
  say "THE STEPS THAT ARE YOURS — §8's steps 3, 4, 5, 7, 8, 9 and 10, which this script does not run:"
  say ""
  say "  STEP 3 — a review launches and runs. At your terminal, in the sandbox checkout:"
  say "      fugaro review --pr ${PR:-<N>}"
  say "    It has to complete, and its log must name NO token value: read the run's own evidence"
  say "    by eye ('fugaro runs show <the review run>' and the execution's log). The launch is"
  say "    the plain one — while the service admitted no review caller this step needed an"
  say "    environment switch (Open item 1, answered 2026-11-05), and the flip that made the"
  say "    service's admission the tree's own state deleted that switch: a launch that starts"
  say "    and is refused there has shown gap 1 rather than passed this step."
  say ""
  say "  STEP 4 — the token is read-only, and it is one repository's. From a throwaway"
  say "    execution of the review variant, mint a token for the reviewed repository and push"
  say "    with it: GitHub must refuse it (403). Then make the same hand-made call naming a"
  say "    DIFFERENT repository the account is admitted for: the SERVICE must refuse it — the"
  say "    repository is not the caller's to name."
  say ""
  say "  STEP 5 — the model call goes to the service, for the account's own tier only. The"
  say "    gateway's log names the service (the variant mounts no token, which is step 2), the"
  say "    service's audit line for that call names the account's own TIER A credential and no"
  say "    other, and a hand-made call as the review account naming a model only another tier"
  say "    serves is NOT served by another account's credential."
  say ""
  say "  STEP 7 — a tampered artifact does not verify. One byte changed in a COPY of"
  say "    review.json (never the stored object) and the signature no longer verifies; a copy of"
  say "    a valid signature pasted under a DIFFERENT review run answers {\"valid\":false} from the"
  say "    SERVICE itself (the framed message is not the one that was signed), and an artifact"
  say "    with no sidecar is refused. This script does not run it: every arm needs a caller the"
  say "    verify route admits, and that account is step 8's open item."
  say ""
  say "  STEP 8 — the verify route's caller class, and what its line carries. OPEN ITEM 5 IS"
  say "    STILL OPEN: which account is in credentials_service.verifier_accounts, and who may"
  say "    impersonate it, is yours to settle, and until a name is published nothing admits this"
  say "    call and the step cannot pass as it stands. Once you have settled it, the call is:"
  say "      gcloud auth print-identity-token --impersonate-service-account=<the verifier account> \\"
  say "        --audiences=${SERVICE_URL:-<the service URL>} --include-email"
  say "      curl -X POST -H \"Authorization: Bearer <that token>\" -H 'Content-Type: application/json' \\"
  say "        --data @verify.json ${SERVICE_URL:-<the service URL>}${VERIFY_PATH}"
  say "    It answers {\"valid\":true} for the real pair and {\"valid\":false} for the tampered one;"
  say "    the same call WITHOUT a token is a typed refusal; and the service's log shows the"
  say "    VERIFIER'S EMAIL as its caller field and the SIGNING KEY'S ENTRY NAME as its credential,"
  say "    with no run and no job on it, because a verifier is not a run."
  say ""
  say "  STEP 9 — a subprocess inside the container reaches nothing (U7, D1074). From inside a"
  say "    mutant subprocess of the mutation check, both a POST /v1/review/sign and a request to"
  say "    the instance metadata server must fail. That is the property the sign route was built"
  say "    on, re-observed against the shipped image rather than trusted from the pre-build one."
  say ""
  say "  STEP 10 — the second sign is refused, and it is the security event. With the run's own"
  say "    identity (minted as the launcher would), a second POST /v1/review/sign for the run that"
  say "    already signed must answer request_refused, NO second signature may exist, and the"
  say "    warning the service wrote is the line an operator's alerting would select."
  say ""
  say "  the block below carries one INFO row per step (step-3 … step-10) saying each is yours."
  say ""
  unscripted 3 "a real 'fugaro review --pr ${PR:-<N>}' run, its completion and its log read by eye"
  unscripted 4 "a throwaway mint whose push GitHub refuses (403), and a call naming another repository that the service refuses"
  unscripted 5 "the gateway's log naming the service, the service's tier-A audit line, and a hand-made call naming another tier's model"
  unscripted 7 "one byte changed in a COPY of review.json, a signature pasted under another run, and an artifact with no sidecar"
  unscripted 8 "the verify route's caller class with a published verifier account — Open item 5 is open, so the step cannot pass as it stands"
  unscripted 9 "a mutant subprocess inside the container: no egress to the sign route and none to the instance metadata server (U7)"
  unscripted 10 "the second POST /v1/review/sign with the run's own identity: request_refused, no second signature, and the service's warning line"
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs the checkout of a sandbox repository"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --run)
    [ $# -ge 2 ] || die "--run needs the review run's id"
    RUN=$2
    shift 2
    ;;
  --pr)
    [ $# -ge 2 ] || die "--pr needs the sandbox pull request's number"
    PR=$2
    shift 2
    ;;
  --coding-job)
    [ $# -ge 2 ] || die "--coding-job needs a job name"
    CODING_JOB=$2
    shift 2
    ;;
  --bucket)
    [ $# -ge 2 ] || die "--bucket needs a bucket name"
    BUCKET=$2
    shift 2
    ;;
  --service-url)
    [ $# -ge 2 ] || die "--service-url needs a URL"
    SERVICE_URL=$2
    shift 2
    ;;
  --dry-run) DRY_RUN_CLI=1; shift ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

SANDBOX=${SANDBOX_CLI:-${CHECK530_SANDBOX:-$PWD}}
RUN=${RUN:-${CHECK530_RUN:-}}
PR=${PR:-${CHECK530_PR:-}}
CODING_JOB=${CODING_JOB:-${CHECK530_CODING_JOB:-}}
BUCKET=${BUCKET:-${CHECK530_BUCKET:-}}
PROJECT=${CHECK530_PROJECT:-}
REGION=${CHECK530_REGION:-}
SERVICE_URL=${SERVICE_URL:-${CHECK530_SERVICE_URL:-}}
DRY_RUN=${CHECK530_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK530_KEEP_SCRATCH:-}
TIMEOUT=${CHECK530_TIMEOUT_SECONDS:-120}
TOKEN_TIMEOUT=${CHECK530_TOKEN_TIMEOUT_SECONDS:-60}
LOG_FRESHNESS=${CHECK530_LOG_FRESHNESS:-30d}
DRY_RUN=${DRY_RUN:-}
# Resolved before anything runs, and by absolute path: this check drives the
# installed release, so it must be the one on PATH when the check started rather
# than one a later step could put there.
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

# The constants §8 names: the service, its two review routes, the signing key's
# own name and the object the signature is written to. They are what the service
# serves today (`internal/credsvc/server.go`), and a check that read them from a
# config would be reading the thing it is checking.
SERVICE_NAME=fugaro-credentials
SIGN_PATH=/v1/review/sign
VERIFY_PATH=/v1/review/verify
SIG_NAME=review.json.sig
REVIEW_JOBS=""
REVIEW_SA=""
RUN_REF=""
RECORDED_REPO=""
VERIFY_ACCOUNT=""
VERIFY_DIGEST=""
VERIFY_SIGNATURE=""
VERIFY_REPO=""
ANSWER_KIND=""
ANSWER_TEXT=""

# the_frame_digest: the digest the service signs is the SHA-256 of the artifact's
# exact bytes (runner/reviewartifact.go), and the framed message carries the
# slug, the run and that digest. The verify rows send the digest of the artifact
# this check just read, so a service that admitted the call would be answering
# about the very pair the run signed.
step_verify_fields() {
  if [ -z "$RUN_REF" ] || [ ! -s "$SCRATCH/sig.out" ]; then
    VERIFY_DIGEST=""
    return 0
  fi
  say "hashing the artifact as the runner does: ${RUN_REF}/artifacts/review.json"
  run_bounded "$TIMEOUT" artifact gcloud storage cat "gs://${BUCKET}/${RUN_REF}/artifacts/review.json" --project="$PROJECT"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    VERIFY_DIGEST=""
    return 0
  fi
  VERIFY_DIGEST=$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$SCRATCH/artifact.out" 2>/dev/null || true)
  VERIFY_SIGNATURE=$(tr -d '\r\n' <"$SCRATCH/sig.out" | head -c 200)
  VERIFY_REPO=$(printf '%s' "$RUN_REF" | sed -n 's#^runs/\(.*\)/'"$RUN"'$#\1#p')
  [ -n "$VERIFY_REPO" ] || VERIFY_REPO=${RECORDED_REPO:-}
  VERIFY_ACCOUNT=${REVIEW_SA:-}
  rm -f "$SCRATCH/artifact.out"
}

# parse_run: a run ID is a UTC timestamp and four hex digits (task.ValidRunID's
# form), and a name that is not one is refused rather than looked up: a check
# that searched for a run by a loose name would read whatever it found.
check_run_id() {
  [ -z "$RUN" ] && return 0
  case "$RUN" in
  [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]-[0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
  *)
    die "--run '${RUN}' has no run ID's form (a UTC timestamp and four hex digits, 20261114-130104-a104): pass the id 'fugaro review --pr <N>' printed"
    ;;
  esac
}

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 530 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  check_run_id
  scratch_up
  write_reader
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_belong
  if [ -n "$DRY_RUN" ]; then
    # `fugaro version` is a call to the installed build, and a dry run makes
    # none: the row says which binary a real run would ask instead of asking it.
    fact fugaro-version INFO "not run: this dry run calls nothing, and 'fugaro version' is a call to the installed build. The one a real run drives is ${FUGARO_BIN:-none on PATH}"
    # The plan names the project and the region doctor would read, so a dry run
    # says which installation the real run would be about without asking it.
    PROJECT=${PROJECT:-<the doctor project>}
    REGION=${REGION:-<the doctor region>}
    plan
    # A dry run measured nothing about the review class, so its block carries
    # only what it did settle: the guards and its own row. Printing the check's
    # own keys here would offer a reader a block whose every fact is `not
    # reached`, which is a verdict about a check nobody ran.
    BLOCK_KEYS=(fugaro-version xtrace agent-session belong dry-run)
    return 0
  fi
  step_version
  step_sandbox
  step_doctor
  step_job
  step_variant_sa
  step_artifact
  step_verify_fields
  step_sign_line
  step_service_url
  step_verify_class
  step_gate_coding
  step_prompts
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
