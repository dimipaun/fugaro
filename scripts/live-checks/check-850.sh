#!/usr/bin/env bash
# Check 850 — a person's own token at a workspace, end to end: whether a roster
# person who holds NOTHING but an invoke grant on the workspace's two services
# can store a token there and be served it in the workspace's members, and what
# each refusal says.
#
# Plan task WC12 writes it (docs/workspace-credentials/implementation-plan.md §8);
# no task of that plan runs it. It settles the plan's U5, U6 and U10 and pins the
# refusals R1 to R3 promise, on a real workspace:
#
#   U6   a person who holds only an invoke grant on the two services in the
#        workspace's project (no role on the project itself) is admitted by the
#        platform with a gcloud identity token;
#   U10  a roster person who is no launcher of the workspace's project can load
#        that project's config by naming it (R13, D3148);
#   U5   a real workspace service reads a `fugaro_u_` container through a
#        prefix-conditioned accessor in the workspace's project;
#   R1   the person's token is served in the members whose switch is on, in each
#        member's own runs, and a second person's run is not served it;
#   R2   the switch off serves none of it (D2972);
#   R3   a deleted token refuses naming `fugaro secrets user set`, and a person
#        removed from the roster is refused within a minute, a run of theirs in
#        flight included (D3146).
#
# RUN BY THE PERSON UNDER TEST, AND WHY. U6 and U10 are about what THAT PERSON
# can do: a check run by the workspace's owner proves neither, because the owner
# holds the roles the person does not. So the check refuses unless the gcloud
# account in use IS the person named in CHECK850_PERSON, and the two rows it
# makes for itself are the person's own calls: `fugaro secrets user ls` against
# the workspace's intake (the platform admits them, or it does not) and
# `fugaro config layer` with the workspace's project selected (the project's
# config loads, or it does not).
#
# WHAT IT WRITES, AND WHAT IT PUTS BACK. One thing: the token itself, stored
# through the intake as the person (`fugaro secrets user set`, the value piped
# in from the file CHECK850_TOKEN_FILE names and never printed, never passed as
# an argument). That is the act U6 is about, so reading it out of a saved listing
# would measure something else. The restore removes the container it stored and
# reads the removal back, unless CHECK850_KEEP_TOKEN is set — then it says so and
# leaves it, so the runs below can be launched against it.
#
# THE REST OF THE EVIDENCE IS YOURS, AND THAT IS DELIBERATE. Four readings need
# an admin's credential (the workspace's IAM, the two services' IAM, the
# container's own IAM, and the service's log lines), and the person under test
# holds none of them — reading it as the owner would measure the owner. So you
# save those readings and this check reads what you saved, exactly as it reads
# the update logs' own words. The commands are one per file, under
# CHECK850_SAVE_DIR (see "Where" below); a file you did not save makes its rows
# read not-measured, with the command that makes it, and moves no other verdict.
#
# Usage:      bash scripts/live-checks/check-850.sh
#             bash scripts/live-checks/check-850.sh --dry-run
#             bash scripts/live-checks/check-850.sh --help
# Needs:      bash, mktemp, python3, gcloud and fugaro, and a gcloud account that
#             IS the person under test (CHECK850_PERSON).
# Where:      run from anywhere, as the person. CHECK850_SAVE_DIR holds the
#             admin-side readings, by these names:
#               iam-project.json    gcloud projects get-iam-policy <workspace>
#                                     --format=json
#               iam-intake.json     gcloud run services get-iam-policy <the
#                                     intake service> --project <workspace>
#                                     --region <region> --format=json
#               iam-creds.json      the same for the credentials service
#               secret-accessor.json  gcloud secrets get-iam-policy
#                                     <the person's container> --project
#                                     <workspace> --format=json  (the container
#                                     name is printed by `fugaro secrets user ls`)
#               accessor-read       the file that holds `ok` if this succeeded:
#                                     gcloud secrets versions access latest
#                                     --secret <that container> --project
#                                     <workspace> --impersonate-service-account
#                                     <the workspace's account> >/dev/null \
#                                     && echo ok > accessor-read
#                                     (the value is discarded, never saved)
#               audit.json          gcloud logging read
#                                     'jsonPayload.msg="credsvc: audit"' \
#                                     --project <workspace> --freshness=7d \
#                                     --limit=2000 --format=json
#             None of these holds a secret's value: they are policies, one word,
#             and the service's audit lines, whose credential field is a NAME.
# Creates:    one container (the token, under the person's own prefix), removed
#             again by the restore and the removal read back; and a scratch
#             directory (mode 0700) removed from the exit trap even on a failure
#             or Ctrl-C.
# Costs:      nothing billable by the check: it calls no model and launches no
#             run. The runs it reads are yours.
# Runs:       by YOU, after the staging below — and no agent runs any step of it.
#             Its own test (check-850_test.sh) drives it against fake tools only.
# Cleanup:    the container it stored, and the scratch directory.
# Output:     progress on stderr, ONE block on stdout headed CHECK 850 RESULT,
#             then CHECK 850 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure — a read that did not answer, a value it
#             was not given, or a save file you did not make.
#
# Knobs (all CHECK850_*): DRY_RUN, KEEP_SCRATCH, KEEP_TOKEN, PERSON (the roster
# person's address: the gcloud account in use has to be this one), WORKSPACE_PROJECT
# (the workspace's own Fugaro project), MEMBER_A and MEMBER_B (the two members
# whose switch is on), MEMBER_OFF (a member whose switch is off — optional; its
# row is not measured without it), TOKEN_FILE (the file holding the token's
# value, read on stdin), SAVE_DIR, TOKEN_NAME (default claude-oauth-token),
# WS_CONFIG (the file that names the workspace's project, if you select it with
# FUGARO_CONFIG rather than FUGARO_PROJECT), WORKSPACE_ACCOUNT (the workspace's
# own service account, for the U5 rows), RUN_A, RUN_B, OTHER_RUN, OFF_RUN,
# DELETED_RUN, REMOVED_RUN (run ids, or their last four characters) and
# REMOVED_AT (when the person was removed from the roster, epoch seconds or
# RFC 3339).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this run prints a
# person's container name, reads a token into a pipe, and its output may be
# pasted into a report. A trace would carry all of it.
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

# The switch a member's admin writes, read off the node the workspace's service
# reads and nowhere else (C1, R9): `settings set` publishes it, so the node is
# the fact.
SWITCH_KEY='personal_workspace_tokens'
# The one personal container this plan serves, under its owner's own prefix:
# secretname.User(sub, creds.PersonTokenName). Read as a SHAPE: what the rows
# below need is whether a container of that shape was named, and no line of this
# check's output carries a person's own id.
PERSONAL_NAME_RE='fugaro_u_[0-9]+_claude-oauth-token'
# The refusal a run meets when the token its slip names is gone (WC5's own
# sentence, internal/credsvc/dynamicsecrets.go). Read for its command: "a person
# sets one at the workspace with `fugaro secrets user set`" is the remedy the
# refusal has to carry.
ABSENT_PHRASE='fugaro secrets user set'
# The halt a removed person's run takes (WC8's detail, internal/gateway/service.go;
# D3146). It is in the halt's DETAIL, beside the run's own reason, so the row
# reads every field of the halt rather than the reason alone.
REMOVED_PHRASE='slip_owner_removed'
LOG_FRESHNESS='7d'

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
TOKEN_STORED=no
RUNS_A=""
RUNS_B=""

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

# restore undoes the ONE thing this check wrote: the container it stored. It
# reads the removal back through the same listing that saw it appear, so a
# removal that did not land is a FAIL rather than a claim.
restore() {
  RESTORE_VERDICT=PASS
  if [ "$TOKEN_STORED" != yes ]; then
    RESTORE_NOTE="nothing was created by this check: the token was not stored in this run (a row above says why), and no installation and no run was touched"
    return 0
  fi
  if [ -n "$KEEP_TOKEN" ]; then
    RESTORE_VERDICT=INFO
    RESTORE_NOTE="CHECK850_KEEP_TOKEN is set: the token this check stored at ${WORKSPACE_PROJECT} is LEFT THERE (name ${TOKEN_NAME}, the person's own prefix), which is what the runs below need. Remove it by hand when you are done: fugaro secrets user rm ${TOKEN_NAME} --project ${WORKSPACE_PROJECT}"
    return 0
  fi
  local out rc=0
  fugaro secrets user rm "$TOKEN_NAME" --project "$WORKSPACE_PROJECT" >"$SCRATCH/restore-rm.out" 2>&1 || rc=$?
  if [ "$rc" -ne 0 ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the token this check stored at ${WORKSPACE_PROJECT} could NOT be removed (fugaro secrets user rm exited ${rc}: $(last_line "$(cat "$SCRATCH/restore-rm.out" 2>/dev/null || true)")). It is still there, under the person's own prefix. Remove it by hand: fugaro secrets user rm ${TOKEN_NAME} --project ${WORKSPACE_PROJECT}"
    return 0
  fi
  out=$(list_containers 2>/dev/null || true)
  if printf '%s\n' "$out" | grep -q "^listed-id=.*_${TOKEN_NAME}$"; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="fugaro secrets user rm reported success and the listing still names ${TOKEN_NAME}: read it yourself — fugaro secrets user ls --project ${WORKSPACE_PROJECT} — and remove it by hand if it is there"
    return 0
  fi
  RESTORE_NOTE="the token this check stored at ${WORKSPACE_PROJECT} was removed and the removal read back through the listing that saw it appear (fugaro secrets user rm ${TOKEN_NAME} --project ${WORKSPACE_PROJECT}); nothing else was created, and no installation and no run was touched"
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
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}
# mask_user_names replaces every `fugaro_u_<id>` with `fugaro_u_…` in what this
# check prints: the id is a person's own Google `sub`, the rows below need the
# name's SHAPE and never whose it is, and no line of this output may carry one.
mask_user_names() { sed -E "s/fugaro_u_[0-9]+/fugaro_u_…/g"; }

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 850's reader. Policies, one listing, the service's own audit lines, a
member's published policy, a config layer and a run listing. None of these
carries a secret's value: an audit line's credential is a NAME, an IAM policy is
roles and members, and the listing has no version in it at all.

iam      argv: an IAM policy document, the address asked about, and the role.
         Prints `binding=<role>|<member>,<member>` per binding, `named=yes|no`
         — whether ANY binding names the address — and `holds=yes|no`: whether
         some binding of that role does.
accessor argv: a secret's IAM policy and the workspace's account. Prints
         `account=<role>|<condition>` per binding that names it, and
         `conditioned=` — whether any of them carries a condition whose
         expression limits the read to the fugaro_u_ prefix. A binding with no
         condition is `conditioned=no`, which is what U5 asks about.
read     argv: the file the impersonated read was saved to. Prints
         `said=<the single word it holds>`.
list     argv: `fugaro secrets user ls --json`'s document and the token name.
         Prints `listed-id=<id>` (masked: the id carries the person's own sub)
         and `listed-name=<name>` per container, plus `personal=<n>`.
layer    argv: `fugaro config layer --json`'s document. Prints `where=`,
         `sha256=`, `generation=`.
switch   argv: `fugaro project policy show --json`'s document. Prints
         `policy=yes|no` (whether a policy is published at all) and
         `value=true|false|absent` for the personal-token switch at its top
         level — the node the workspace's service reads (C1, R9).
runs     argv: `fugaro runs ls --json`'s document and the run id asked about
         (or its last characters). Prints the row's status, terminal, outcome
         and its halt's reason, moment, detail and workspace marker; `found=no`
         when no row matches.
audit    argv: the service's log lines' file, a run id, and the regex a personal
         container's name matches. Prints one `audit=<status>|<credential>|tier|
         <purpose>` line per `credsvc: audit` line of that run, and
         `personal=<n>`, how many of them named a container of that shape.
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


def text(v, cap=300):
    return re.sub(r"\s+", " ", str(v or "")).strip()[:cap]


def masked(name):
    return re.sub(r"fugaro_u_[0-9]+", "fugaro_u_…", str(name or ""))


def bindings(doc):
    out = doc.get("bindings") if isinstance(doc, dict) else None
    return out if isinstance(out, list) else []


def iam(path, who, role):
    with open(path) as fh:
        doc = json.load(fh)
    holds = False
    named = False
    for b in bindings(doc):
        if not isinstance(b, dict):
            continue
        members = [str(m) for m in (b.get("members") or [])]
        print("binding=%s|%s" % (str(b.get("role") or ""), ",".join(masked(m) for m in members)))
        mine = [m for m in members if m == who or m.endswith(":" + who)]
        if mine:
            named = True
        if str(b.get("role") or "") == role and mine:
            holds = True
    print("named=" + ("yes" if named else "no"))
    print("holds=" + ("yes" if holds else "no"))


def accessor(path, account):
    with open(path) as fh:
        doc = json.load(fh)
    conditioned = False
    for b in bindings(doc):
        if not isinstance(b, dict):
            continue
        members = [str(m) for m in (b.get("members") or [])]
        if not any(m.endswith(":" + account) or m == account for m in members):
            continue
        cond = b.get("condition") if isinstance(b.get("condition"), dict) else {}
        expr = str(cond.get("expression") or "")
        print("account=%s|%s" % (str(b.get("role") or ""), text(expr, 200)))
        if expr and "fugaro_u_" in expr:
            conditioned = True
    print("conditioned=" + ("yes" if conditioned else "no"))


def read(path):
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            body = fh.read()
    except OSError as err:
        print("said=could not be read: %s" % err)
        return
    print("said=" + text(body, 60))


def listing(path, name):
    with open(path) as fh:
        doc = json.load(fh)
    rows = dig(doc, "secrets")
    if not isinstance(rows, list):
        rows = []
    personal = 0
    for row in rows:
        if not isinstance(row, dict):
            continue
        ident = str(row.get("id") or "")
        print("listed-id=" + masked(ident))
        print("listed-name=" + str(row.get("name") or ""))
        if re.search(r"fugaro_u_[0-9]+_" + re.escape(name) + r"$", ident):
            personal += 1
    print("personal=%d" % personal)


def layer(path):
    with open(path) as fh:
        doc = json.load(fh)
    print("where=" + text(dig(doc, "where")))
    print("sha256=" + str(dig(doc, "sha256") or ""))
    print("generation=" + str(dig(doc, "generation") or ""))


def switch(path):
    with open(path) as fh:
        doc = json.load(fh)
    policy = doc.get("policy") if isinstance(doc, dict) else None
    if not isinstance(policy, dict):
        print("policy=no")
        print("value=absent")
        return
    print("policy=yes")
    value = policy.get("personal_workspace_tokens")
    print("value=" + ("absent" if value is None else ("true" if value else "false")))


def runs(path, asked):
    with open(path) as fh:
        doc = json.load(fh)
    rows = dig(doc, "runs")
    if not isinstance(rows, list):
        rows = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        run_id = str(row.get("run_id") or "")
        if not asked or not (run_id == asked or run_id.endswith(asked)):
            continue
        halt = row.get("halt") if isinstance(row.get("halt"), dict) else {}
        print("found=yes")
        print("run_id=" + run_id)
        print("status=" + str(row.get("status") or ""))
        print("terminal=" + ("yes" if row.get("terminal") else "no"))
        print("outcome=" + str(row.get("outcome") or ""))
        print("halt_reason=" + str(halt.get("reason") or ""))
        print("halt_at=" + str(halt.get("at") or ""))
        print("halt_detail=" + text(halt.get("detail"), 600))
        print("halt_workspace_refusal=" + str(halt.get("workspace_refusal") or ""))
        return
    print("found=no")


def audit(path, run, pattern):
    want = re.compile(pattern)
    with open(path) as fh:
        doc = json.load(fh)
    if not isinstance(doc, list):
        doc = []
    personal = 0
    for entry in doc:
        if not isinstance(entry, dict):
            continue
        payload = entry.get("jsonPayload") or {}
        if not isinstance(payload, dict):
            continue
        # The run is matched the way the run listing's rows are: a knob may be
        # the run's last four characters (the check's own --help says so), and
        # the saved log is the whole service's, so the id is read by its tail.
        entry_run = str(payload.get("run") or "")
        if not run or not (entry_run == run or entry_run.endswith(run)):
            continue
        msg = entry.get("textPayload") or payload.get("msg") or payload.get("message") or ""
        if msg != "credsvc: audit":
            continue
        credential = str(payload.get("credential") or "")
        if want.search(credential):
            personal += 1
        print("audit=%s|%s|%s|%s" % (
            text(payload.get("status"), 40), masked(credential),
            text(payload.get("tier"), 8), text(payload.get("purpose"), 40)))
    print("personal=%d" % personal)


def main(argv):
    if len(argv) < 3:
        print("reader: need a reading", file=sys.stderr)
        return 2
    what = argv[1]
    if what == "iam":
        iam(argv[2], argv[3], argv[4])
    elif what == "accessor":
        accessor(argv[2], argv[3])
    elif what == "read":
        read(argv[2])
    elif what == "list":
        listing(argv[2], argv[3])
    elif what == "layer":
        layer(argv[2])
    elif what == "switch":
        switch(argv[2])
    elif what == "runs":
        runs(argv[2], argv[3] if len(argv) > 3 else "")
    elif what == "audit":
        audit(argv[2], argv[3], argv[4])
    else:
        print("reader: unknown reading %s" % what, file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
  chmod 700 "$SCRATCH/reader.py"
}

# ------------------------------------------------------------------ the guards
# No agent runs any step of Check 850 (the plan says so). The token store below
# is `fugaro secrets user set`, which refuses an agent's session on its own; this
# refuses first, so no other read of the person's workspace happens either.
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 850. Run it yourself, as the person under test: bash scripts/live-checks/check-850.sh"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-850.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the token it pipes in or the container name it prints"
}

guard_project() {
  local doctor_out v
  doctor_out=$(fugaro doctor --project "$INSTALLATION" --all || true)
  if ! printf '%s\n' "$doctor_out" | grep -q '^project: .*(GCP fugaro-dev,'; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project ${INSTALLATION} --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check reads ${WORKSPACE_PROJECT}${MEMBER_A:+, ${MEMBER_A}} and never belong"
  fi
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  case "$v" in
  *belong*) stop "the version this check printed names belong: this check is never run there" ;;
  esac
  fact fugaro-version PASS "${v:-fugaro version printed nothing} — the CLI the person under test stores through and the rows are read with: R1 to R3 are about this release's promises, and a check run with another release reads another release's words"
}

# The person under test, before any reading about them. U6 and U10 are what THAT
# person can do, and a check run under an admin's account measures the admin.
guard_person() {
  [ -n "$PERSON" ] ||
    stop "CHECK850_PERSON is not set, so this run does not know whose token it is about. Log in as the person under test ('gcloud auth login <their address>', then 'gcloud config set account <their address>') and name them: CHECK850_PERSON=<their address> bash scripts/live-checks/check-850.sh"
  if [ -z "$ACCOUNT" ]; then
    stop "gcloud prints no account ('gcloud config get-value account'), so this run cannot tell whether it is the person under test. Log in as them first: gcloud auth login ${PERSON}"
  fi
  if [ "$ACCOUNT" != "$PERSON" ]; then
    stop "this shell's gcloud account is ${ACCOUNT}, and the person under test is ${PERSON}: U6 and U10 are about what that person can do with nothing but the two invoke grants, so a check run under another account measures the wrong person. Log in as them ('gcloud auth login ${PERSON}') and run the check again"
  fi
  fact person-identity PASS "the gcloud account in use is ${PERSON} itself: the two calls below (the intake listing and the config layer) are the person's own, and that is the only way U6's admission and U10's config load are measured rather than assumed"
}

# The saved readings an admin makes. A missing file is a row not measured, never
# a FAIL: the person under test cannot make these reads, and a check that read
# them as the owner would measure the owner.
saved() { # saved FILENAME — prints the path, or nothing when it is not there
  local name=$1 path=$SAVE_DIR/$1
  if [ -z "$SAVE_DIR" ]; then
    printf ''
    return 0
  fi
  if [ -s "$path" ]; then
    printf '%s' "$path"
  fi
}

saved_missing() { # saved_missing KEY FILENAME COMMAND
  unmeasured "$1" "the file ${SAVE_DIR:-<CHECK850_SAVE_DIR>}/$2 is not there, and this reading needs an admin's credential, which ${PERSON} does not hold — so it is not measured here at all. Save it and run the check again: $3 > ${SAVE_DIR:-<save dir>}/$2"
}

# ------------------------------------------------------------------- the steps
# step_person_grants: U6, both halves, off the two saved policies. The project's
# own policy must name the person in NO binding (they hold no role on the project
# itself), and each of the two services must name them as an invoker.
step_person_grants() {
  local path
  path=$(saved iam-project.json)
  if [ -z "$path" ]; then
    saved_missing no-project-role iam-project.json "gcloud projects get-iam-policy ${WORKSPACE_PROJECT} --format=json"
  else
    local out named
    out=$(reader iam "$path" "$PERSON" "roles/run.invoker" || true)
    named=$(field_of named "$out")
    if [ "$named" = yes ]; then
      fact no-project-role FAIL "the workspace's project policy names ${PERSON} in a project-level binding: $(printf '%s' "$(lines_of binding "$out" | grep "$PERSON" | head -n 3 | tr '\n' '; ')" | head -c 400). U6 is 'nothing but the two invoke grants, no role on the project itself', and a person with a project role is admitted for reasons other than the roster. Read it yourself: gcloud projects get-iam-policy ${WORKSPACE_PROJECT} --format=json"
    else
      fact no-project-role PASS "the workspace's project policy (iam-project.json) names ${PERSON} in NO binding at all: the person under test holds no role on ${WORKSPACE_PROJECT} itself, which is the half of U6 the two service grants below rest on"
    fi
  fi
  local svc key svcname
  for svc in intake creds; do
    if [ "$svc" = intake ]; then
      key=person-invoke-intake
      svcname="${INTAKE_SERVICE:-<the workspace intake service>}"
    else
      key=person-invoke-creds
      svcname="${CREDS_SERVICE:-<the workspace credentials service>}"
    fi
    path=$(saved "iam-$svc.json")
    if [ -z "$path" ]; then
      saved_missing "$key" "iam-$svc.json" "gcloud run services get-iam-policy ${svcname} --project ${WORKSPACE_PROJECT}${REGION:+ --region }${REGION} --format=json"
      continue
    fi
    local out2 holds2
    out2=$(reader iam "$path" "$PERSON" "roles/run.invoker" || true)
    holds2=$(field_of holds "$out2")
    if [ "$holds2" = yes ]; then
      fact "$key" PASS "the $svc service's policy (iam-$svc.json) names ${PERSON} as roles/run.invoker: the platform admits a caller who holds that role on the service and needs no role on the project — which is the whole of what U6 asks the platform to do, and the person's own call below is what they do with it"
    else
      fact "$key" FAIL "the $svc service's policy names ${PERSON} in no roles/run.invoker binding (bindings read: $(printf '%s' "$(lines_of binding "$out2" | head -n 3 | tr '\n' '; ')" | head -c 300)): the person under test is not admitted to it, so the rows below about that service are rows about somebody else's access. Read it yourself: gcloud run services get-iam-policy ${svcname} --project ${WORKSPACE_PROJECT}${REGION:+ --region }${REGION}"
    fi
  done
}

# step_admits: U6's admission, measured by the person's OWN call. This is the
# row the whole check exists for: the platform either admits them with a gcloud
# identity token or it does not, and nothing but their own call can say.
step_admits() {
  local out rc=0
  out=$(fugaro secrets user ls --project "$WORKSPACE_PROJECT" --json 2>"$SCRATCH/ls.err") || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact intake-admits FAIL "the person's own call to the workspace's intake was refused (fugaro secrets user ls --project ${WORKSPACE_PROJECT} --json exited ${rc}): $(last_line "$(cat "$SCRATCH/ls.err" 2>/dev/null || true)"). U6 asks whether a person with nothing but the two invoke grants is admitted by the platform with a gcloud identity token — this is that call, made as ${PERSON}, and it was not. The refusal above says which of the two it was: a platform refusal (the invoke grant is missing — the rows above read the two policies) or the intake's own (the roster: a workspace's intake admits the people on its roster, so a person refused here is either not on that roster yet, or was added since the workspace's owner last ran fugaro update)"
    return 0
  fi
  printf '%s' "$out" >"$SCRATCH/ls.json"
  local read_out personal
  read_out=$(reader list "$SCRATCH/ls.json" "$TOKEN_NAME" || true)
  personal=$(field_of personal "$read_out")
  fact intake-admits PASS "the person's own call to the workspace's intake answered (fugaro secrets user ls --project ${WORKSPACE_PROJECT} --json, as ${PERSON}, with only the two invoke grants the rows above read): U6 holds on this installation — the platform admits a roster person by their identity token, and the intake answers them. It named ${personal} container(s) of their own prefix"
}

# step_store: the act U6 is about. The value comes from the file the owner
# names, through stdin (`secrets user set` takes no argument and no flag that
# carries a value) and is never printed.
step_store() {
  if [ -z "$WORKSPACE_PROJECT" ]; then
    unmeasured token-stored "CHECK850_WORKSPACE_PROJECT was not set, so there is no workspace to store a token in. Name it: CHECK850_WORKSPACE_PROJECT=<the workspace's project>"
    return 0
  fi
  if [ -z "$TOKEN_FILE" ]; then
    unmeasured token-stored "CHECK850_TOKEN_FILE was not set, so this run holds no value to store — and it will not invent one. Name the file that holds the token's value (one line; a test token is enough for U6, a live one is what the runs below need): CHECK850_TOKEN_FILE=<file>"
    return 0
  fi
  if [ ! -r "$TOKEN_FILE" ]; then
    unmeasured token-stored "the file you named (${TOKEN_FILE}) cannot be read, so this run will not store anything. Check the path and its permissions"
    return 0
  fi
  if [ ! -s "$TOKEN_FILE" ]; then
    unmeasured token-stored "the file you named (${TOKEN_FILE}) is empty, and an empty value is not a token. Put the value in it and run the check again"
    return 0
  fi
  local rc=0
  fugaro secrets user set "$TOKEN_NAME" --project "$WORKSPACE_PROJECT" <"$TOKEN_FILE" >"$SCRATCH/set.out" 2>"$SCRATCH/set.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact token-stored FAIL "storing the token was refused (fugaro secrets user set ${TOKEN_NAME} --project ${WORKSPACE_PROJECT} exited ${rc}): $(last_line "$(cat "$SCRATCH/set.err" 2>/dev/null || true)"). This is U6's own act — the person, with nothing but the two invoke grants, storing a token at the workspace — and it did not happen. The refusal above is the finding: read it, and read the two policy rows"
    return 0
  fi
  TOKEN_STORED=yes
  local out personal
  out=$(list_containers || true)
  personal=$(field_of personal "$out")
  if [ "${personal:-0}" -ge 1 ]; then
    fact token-stored PASS "the person stored ${TOKEN_NAME} at ${WORKSPACE_PROJECT} through the intake (fugaro secrets user set, the value piped in from the file you named and never printed, never an argument), and the listing names it back under their own prefix (fugaro_u_…_${TOKEN_NAME}, the id masked here and never written down): U6 holds — the roster person's own identity token is what put it there, and no caller can write under another person's prefix (R3, R12). The restore removes it again"
    return 0
  fi
  fact token-stored FAIL "the store reported success and the listing does not name a ${TOKEN_NAME} under the person's own prefix (fugaro_u_<their id>_${TOKEN_NAME}): read it yourself — fugaro secrets user ls --project ${WORKSPACE_PROJECT} — because a store that answers and leaves nothing is worse than one that refuses"
}

# step_config: U10, on the person's own machine. The project's config has to load
# when the project is named, with the grants WC2 and WC3 give and nothing else,
# and the layer it loads is the published one — a read out of the workspace's own
# runs bucket, which is the read a person with no launcher role has to be able
# to make.
step_config() {
  local out rc=0
  if [ -n "$WS_CONFIG" ]; then
    out=$(env FUGARO_CONFIG="$WS_CONFIG" FUGARO_PROJECT= fugaro config layer --json 2>"$SCRATCH/layer.err") || rc=$?
  else
    out=$(env FUGARO_PROJECT="$WORKSPACE_PROJECT" fugaro config layer --json 2>"$SCRATCH/layer.err") || rc=$?
  fi
  if [ "$rc" -ne 0 ]; then
    fact config-named FAIL "the workspace's config did not load with the project named (fugaro config layer --json exited ${rc}): $(last_line "$(cat "$SCRATCH/layer.err" 2>/dev/null || true)"). U10 asks whether a roster person who is no launcher of the project can load that project's config by naming it (R13, D3148), and this is that call, made as ${PERSON}${WS_CONFIG:+ with FUGARO_CONFIG=${WS_CONFIG}}${WS_CONFIG:- with FUGARO_PROJECT=${WORKSPACE_PROJECT}}. If the refusal says no config is selected, the person's machine has no config file for that project: ask the workspace's owner for it and name it with CHECK850_WS_CONFIG"
    return 0
  fi
  printf '%s' "$out" >"$SCRATCH/layer.json"
  local parsed where sha
  parsed=$(reader layer "$SCRATCH/layer.json" || true)
  where=$(field_of where "$parsed")
  sha=$(field_of sha256 "$parsed")
  if [ -z "$where" ] || [ -z "$sha" ]; then
    fact config-named FAIL "the config loaded and names no project layer (where '${where:-}', sha256 '${sha:-}'): the person can read their own file and not the project's published layer, which is the read U10 is about. Read it yourself: FUGARO_PROJECT=${WORKSPACE_PROJECT} fugaro config layer --json"
    return 0
  fi
  fact config-named PASS "the workspace's project config loaded with the project named (fugaro config layer --json, as ${PERSON}): the published layer resolved from ${where} at generation $(field_of generation "$parsed"), sha256 ${sha:0:12}… — U10 holds, the person who is no launcher of ${WORKSPACE_PROJECT} loads its config by naming it (R13, D3148), and the same config file is what carries the intake's address"
}

# step_switch: the member's own switch, read off the node the workspace's service
# reads (C1, R9) and nowhere else. `settings set` is what publishes it, so a
# member whose admin wrote it reads it here as true.
step_switch() { # step_switch KEY MEMBER OFF|ON WHERE — OFF accepts a written false or none
  local key=$1 member=$2 want=$3 where=$4
  local out rc=0
  out=$(fugaro project policy show --project "$member" --json 2>"$SCRATCH/policy-$key.err") || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured "$key" "the published policy for ${member} could not be read (fugaro project policy show --project ${member} --json exited ${rc}: $(last_line "$(cat "$SCRATCH/policy-$key.err" 2>/dev/null || true)")). The node is the only place the switch is read from (C1, R9), so nothing about this member is measured"
    return 0
  fi
  printf '%s' "$out" >"$SCRATCH/policy-$key.json"
  local parsed value policy
  parsed=$(reader switch "$SCRATCH/policy-$key.json" || true)
  value=$(field_of value "$parsed")
  policy=$(field_of policy "$parsed")
  case "$want" in
  ON)
    if [ "$value" = true ]; then
      fact "$key" PASS "the member's published policy (config/policy/shared of ${member}) carries ${SWITCH_KEY}=true: that node is the one place the workspace's service reads the switch from (C1, R9) — settings set publishes it and a run meets it there — so this is the fact ${where} is served by, and not a file on anybody's disk"
      return 0
    fi
    fact "$key" FAIL "the member ${member} reads ${SWITCH_KEY}=${value} on its published policy (a policy ${policy} published there) and this row needs true: the workspace's service reads that node and nothing else (C1, R9), so a member whose admin turned the switch on reads it here, and a switch written on a disk and never published reaches no run. Read it yourself: fugaro project policy show --project ${member} --json"
    ;;
  OFF)
    if [ "$value" = false ] || [ "$value" = absent ]; then
      fact "$key" PASS "the member's published policy (config/policy/shared of ${member}) reads ${SWITCH_KEY}=${value}: a member whose admin never turned the switch on is off, and a written off is off too (D2972, absent reads as off) — so ${where} serves the shared entries exactly as it did before the personal half, and the row below reads its run for the same thing"
      return 0
    fi
    fact "$key" FAIL "the member ${member} reads ${SWITCH_KEY}=${value} on its published policy and this row needs it off: a member left alone (never turned on) has to serve the shared entries and nothing personal, which is D2972 and R2 — a published true on a member nobody turned on means the release published the switch for it"
    ;;
  esac
}

# step_served: whether a run of the person's was served a personal container,
# off the service's own audit lines. The credential field is a NAME, and the
# shape it is matched against is the one personal container this plan serves.
step_served() { # step_served KEY RUN ENVVAR WANT WHERE
  local key=$1 run=$2 envvar=$3 want=$4 where=$5
  if [ -z "$run" ]; then
    unmeasured "$key" "no run id was given for ${where} (${envvar} is unset). Launch that run, keep its id, and pass it as ${envvar} (or its last four characters)"
    return 0
  fi
  local path
  path=$(saved audit.json)
  if [ -z "$path" ]; then
    saved_missing "$key" audit.json "gcloud logging read 'jsonPayload.msg=\"credsvc: audit\"' --project ${WORKSPACE_PROJECT} --freshness=${LOG_FRESHNESS} --limit=2000 --format=json"
    return 0
  fi
  local out personal
  out=$(reader audit "$path" "$run" "$PERSONAL_NAME_RE" || true)
  personal=$(field_of personal "$out")
  local lines
  lines=$(printf '%s\n' "$out" | grep -c '^audit=' || true)
  case "$want" in
  served)
    if [ "${lines:-0}" -eq 0 ]; then
      unmeasured "$key" "the service's saved lines hold no 'credsvc: audit' entry for the run ${run}: nothing was served to it that the audit recorded, so whether it was served the personal token is not measured. Check the run id, and that the saved read is recent enough (--freshness=${LOG_FRESHNESS}): the file you saved is ${path}"
      return 0
    fi
    if [ "${personal:-0}" -ge 1 ]; then
      fact "$key" PASS "the run ${run} in ${where} was served the person's own container: ${personal} of its ${lines} 'credsvc: audit' line(s) name a credential of the one personal shape this plan serves (fugaro_u_…_${TOKEN_NAME}, tier $(personal_tier "$out")): $(printf '%s' "$(printf '%s\n' "$out" | grep '^audit=' | grep 'fugaro_u_' | head -n 2 | tr '\n' '; ')" | head -c 300). The workspace's service read that container for the run — which is U5's second half too: the read went through the service's own account"
      return 0
    fi
    fact "$key" FAIL "the run ${run} in ${where} was served NO personal container across ${lines} 'credsvc: audit' line(s): its credential names are $(printf '%s' "$(lines_of audit "$out" | head -n 3 | tr '\n' '; ')" | head -c 400). A member whose switch reads true, with the person's token stored, must serve the person's own container first (R1, R7) — so either the run's member is not the one whose switch was read, or the switch was published after the run started, or the personal route is not wired"
  ;;
  not-served)
    if [ "${lines:-0}" -eq 0 ]; then
      unmeasured "$key" "the service's saved lines hold no 'credsvc: audit' entry for the run ${run}, so 'this run was served no personal container' is not measured: an unread log is not an empty one. Check the run id and the saved file ${path}"
      return 0
    fi
    if [ "${personal:-0}" -eq 0 ]; then
      fact "$key" PASS "the run ${run} in ${where} was served no personal container across its ${lines} 'credsvc: audit' line(s): ${where} is served what it was served before the personal half existed, and a second person's run is served THEIR entries rather than this person's token (R1) — the credential names are $(printf '%s' "$(lines_of audit "$out" | head -n 2 | tr '\n' '; ')" | head -c 300)"
      return 0
    fi
    fact "$key" FAIL "the run ${run} in ${where} was served a personal container ${personal} time(s), and it must not be: $(printf '%s' "$(printf '%s\n' "$out" | grep '^audit=' | grep 'fugaro_u_' | head -n 3 | tr '\n' '; ')" | head -c 400). A member's switch decides whether ITS runs may be served a person's token, and a run that is not that person's is never served theirs"
  ;;
  esac
}

# step_run_halt: a run that had to be refused, read off its own record. Two
# subjects share the shape: the token that is gone (the refusal has to name
# `fugaro secrets user set`) and the person who is gone (the halt has to carry
# slip_owner_removed, and to fall within a minute of the removal).
step_run_halt() { # step_run_halt KEY RUN ENVVAR SUBJECT
  local key=$1 run=$2 envvar=$3 subject=$4
  if [ -z "$run" ]; then
    unmeasured "$key" "no run id was given for this row (${envvar} is unset). Launch the run the staging describes, keep its id, and pass it as ${envvar}"
    return 0
  fi
  local out
  out=$(runs_lookup "$run" || true)
  if [ "$(field_of found "$out")" != yes ]; then
    unmeasured "$key" "no run in the listings this run could read has an id ending in ${run}: read them yourself (fugaro runs ls --project ${MEMBER_A} --json, and the same for ${MEMBER_B:-the other member}) and pass the id again"
    return 0
  fi
  local rid detail reason at refusal
  rid=$(field_of run_id "$out")
  detail=$(field_of halt_detail "$out")
  reason=$(field_of halt_reason "$out")
  at=$(field_of halt_at "$out")
  refusal=$(field_of halt_workspace_refusal "$out")
  case "$subject" in
  deleted-token)
    case "$detail" in
    *"$ABSENT_PHRASE"*)
      fact "$key" PASS "the run ${rid} halted naming the remedy a deleted token has to name: '$(printf '%s' "$detail" | head -c 300)'. R3's refusal is 'the personal token this call's slip names is not there: a person sets one at the workspace with fugaro secrets user set', and a run that halts with that sentence is the refusal as written — the run is stopped rather than silently served the shared entries"
      ;;
    *)
      if [ -z "$detail" ]; then
        fact "$key" FAIL "the run ${rid} is $(field_of status "$out")${reason:+ with halt ${reason}} and its record carries no detail at all: the refusal R3 asks for is 'the personal token this call's slip names is not there: a person sets one at the workspace with ${ABSENT_PHRASE}', so a run stopped without that sentence leaves a person with a run that stopped and nothing to do. Read it yourself: fugaro runs show ${rid}"
      else
        fact "$key" FAIL "the run ${rid} halted '$(printf '%s' "$detail" | head -c 300)' and that sentence does not name ${ABSENT_PHRASE}: the refusal a deleted token meets has to name the command that puts one back, or the person is left with a stopped run and no remedy"
      fi
      ;;
    esac
    ;;
  removed-refusal)
    case "$detail$refusal" in
    *"$REMOVED_PHRASE"*)
      if [ -z "$REMOVED_AT" ]; then
        fact "$key" PASS "the run ${rid} halted carrying ${REMOVED_PHRASE} (reason ${reason}, workspace marker '${refusal}', at ${at}): D3146's halt — the workspace's service refuses every call under a slip whose owner has left the roster — is in the run's own record. Whether it fell WITHIN A MINUTE of the removal is not measured: pass CHECK850_REMOVED_AT (the moment you removed them) and this row says that too"
        return 0
      fi
      if in_window "$REMOVED_AT" "$at" "$REMOVAL_WINDOW_SECONDS"; then
        fact "$key" PASS "the run ${rid} halted ${REMOVED_PHRASE} at ${at}, $(gap_after "$REMOVED_AT" "$at" || echo "within") — inside the ${REMOVAL_WINDOW_SECONDS}s window that starts at the removal you named: D3146 and R3 hold on this installation, a person removed from the roster is refused within a minute, a run of theirs in flight included"
        return 0
      fi
      fact "$key" FAIL "the run ${rid} halted ${REMOVED_PHRASE} at ${at}, $(gap_after "$REMOVED_AT" "$at" || echo "outside") — later than the ${REMOVAL_WINDOW_SECONDS}s window that starts at the removal you named (${REMOVED_AT}): the refusal came, but not 'within a minute', which is what R3 and D3146 promise. Either the run's own interval is longer than the window, or the service re-reads the roster more slowly than it says — and a window you set wider with CHECK850_REMOVAL_WINDOW_SECONDS only records a wider reading, it is not a fix"
      ;;
    *)
      fact "$key" FAIL "the run ${rid} carries no ${REMOVED_PHRASE} in its record (status $(field_of status "$out"), halt reason '${reason}', detail '$(printf '%s' "$detail" | head -c 200)'). D3146: a run whose slip's owner has left the roster halts, and its record carries that reason so the person reading the run knows why it stopped. Read it yourself: fugaro runs show ${rid} — and if the run was launched after the removal, it is refused at the launch and never takes a slip at all, which is a different reading: launch the run BEFORE the removal"
      ;;
    esac
    ;;
  esac
}

# personal_tier READER_OUT: the tier of the first personal line the service
# wrote for a run, so the row can name it (a personal token is tier A: it is a
# container of the person's, not a shared entry).
personal_tier() {
  printf '%s\n' "$1" | grep '^audit=' | grep 'fugaro_u_' | head -n 1 | cut -d'|' -f3
}

# list_containers: the person's own containers, through the intake, as the
# JSON the reader parses. It is the one reading of the listing this check makes
# in two places — the store's row and the restore that removes it — so the
# removal is read back through the same eyes that saw it appear.
list_containers() {
  local out
  out=$(fugaro secrets user ls --project "$WORKSPACE_PROJECT" --json 2>/dev/null) || return 0
  [ -n "$out" ] || return 0
  printf '%s' "$out" >"$SCRATCH/list.json"
  reader list "$SCRATCH/list.json" "$TOKEN_NAME"
}

# runs_lookup RUN: the run's row out of the two members' listings, read as the
# person. A launcher reads the runs of the members they are a launcher of; the
# listings are read once per run of the check.
runs_lookup() { # runs_lookup RUN
  local run=$1 out
  for out in "$RUNS_A" "$RUNS_B"; do
    [ -n "$out" ] || continue
    local parsed
    parsed=$(reader runs "$out" "$run" || true)
    if [ "$(field_of found "$parsed")" = yes ]; then
      printf '%s\n' "$parsed"
      return 0
    fi
  done
  printf 'found=no\n'
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

# step_accessor: U5, both halves, off the two saved readings. The container's own
# policy has to carry the workspace's account as an accessor whose condition
# limits it to the fugaro_u_ prefix, and the read itself has to have succeeded.
step_accessor() {
  if [ -z "$WORKSPACE_ACCOUNT" ]; then
    unmeasured accessor-conditioned "CHECK850_WORKSPACE_ACCOUNT was not set, so this run does not know which account the accessor is for (it is the workspace's own service account, the one the workspace's credentials service runs as). Name it: CHECK850_WORKSPACE_ACCOUNT=<its address>"
    unmeasured accessor-reads "CHECK850_WORKSPACE_ACCOUNT was not set: the read U5 asks about is made AS that account, so there is no row to read"
    return 0
  fi
  local path
  path=$(saved secret-accessor.json)
  if [ -z "$path" ]; then
    saved_missing accessor-conditioned secret-accessor.json "gcloud secrets get-iam-policy <the person's container> --project ${WORKSPACE_PROJECT} --format=json"
  else
    local out conditioned role_expr
    out=$(reader accessor "$path" "$WORKSPACE_ACCOUNT" || true)
    conditioned=$(field_of conditioned "$out")
    role_expr=$(lines_of account "$out" | head -n 1)
    if [ "$conditioned" = yes ]; then
      fact accessor-conditioned PASS "the container's policy names ${WORKSPACE_ACCOUNT} as an accessor and its condition limits the read to the fugaro_u_ prefix: ${role_expr:-the binding is conditioned}. That is U5's first half — the workspace's project grants its own service account reads of a person's containers and of nothing else, which is what makes 'every workspace admin can read a token, the workspace's service account too' (F20) a bounded grant rather than a project-wide one"
    else
      fact accessor-conditioned FAIL "the container's policy names ${WORKSPACE_ACCOUNT} in no binding whose condition limits the read to the fugaro_u_ prefix (bindings for that account: ${role_expr:-none}). U5 asks whether a real workspace service reads a fugaro_u_ container through a PREFIX-CONDITIONED accessor: an unconditioned accessor over the project would let the workspace's account read every secret there, the shared entries among them, which is the boundary D3141 draws"
    fi
  fi
  path=$(saved accessor-read)
  if [ -z "$path" ]; then
    saved_missing accessor-reads accessor-read "gcloud secrets versions access latest --secret <the person's container> --project ${WORKSPACE_PROJECT} --impersonate-service-account ${WORKSPACE_ACCOUNT} >/dev/null && echo ok"
    return 0
  fi
  local said
  said=$(field_of said "$(reader read "$path" || true)")
  if [ "$said" = ok ]; then
    fact accessor-reads PASS "the workspace's own account READ the person's container (the saved read says 'ok'; the value was discarded, never saved): U5's second half holds — the conditioned accessor is not only written down, it works, and the workspace's service is the account that reads the personal token when a run is served"
    return 0
  fi
  fact accessor-reads FAIL "the read you saved says '${said:-nothing}' rather than 'ok': the workspace's account did not read the person's container. U5 is about exactly this read, so the row is the finding — check that the impersonation was allowed (iam.serviceAccountTokenCreator on ${WORKSPACE_ACCOUNT}) and that the accessor's condition covers this container's name (fugaro_u_…_${TOKEN_NAME})"
}

# step_prompt: what only the owner can do, said out loud, in the order it has to
# be done. The runs and the removal are the owner's: they need a repository, a
# brief and two members to launch in, and this check measures rather than spends.
step_prompt() {
  fact owner-prompt INFO "the staging, and no agent may run any of it: (1) as the workspace's owner, add the person to the roster ('fugaro workspace user add <address> --workspace <name>') and give them NOTHING else — no role on ${WORKSPACE_PROJECT}; (2) on the two members' projects, have their admins turn the switch on ('fugaro settings set ${SWITCH_KEY} true --project <member>' — the write publishes the node the service reads) — leave MEMBER_OFF alone; (3) as the person: run this check, which stores the token (CHECK850_TOKEN_FILE) and reads their own admission and config load; (4) as the person, launch a run in each switch-on member (RUN_A, RUN_B), one in MEMBER_OFF (OFF_RUN), one whose token you then delete ('fugaro secrets user rm ${TOKEN_NAME} --project ${WORKSPACE_PROJECT}', then DELETED_RUN), and one a SECOND person launches (OTHER_RUN); (5) as the owner, remove the person from the roster while one of their runs is in flight, and write the moment down (REMOVED_AT, then REMOVED_RUN); (6) save the six admin-side readings under CHECK850_SAVE_DIR (the commands are in --help) and run this check again with every knob named"
}

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 850 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] fugaro doctor --project ${INSTALLATION} --all and fugaro version"
  say "  [network, read] fugaro secrets user ls --project ${WORKSPACE_PROJECT:-<the workspace project>} --json — the person's own admission (U6)"
  say "  [network, WRITE] fugaro secrets user set ${TOKEN_NAME} --project ${WORKSPACE_PROJECT:-<the workspace>}, the value piped in from ${TOKEN_FILE:-<a file you name>} — the one thing this check writes, removed again by the restore"
  say "  [network, read] FUGARO_PROJECT=${WORKSPACE_PROJECT:-<the workspace>} fugaro config layer --json — the project's config by naming it (U10)"
  say "  [network, read] fugaro project policy show --project <member> --json — the switch each member's node carries"
  say "  [network, read] fugaro runs ls --project <member> --json — the runs' records"
  say "  [saved, read] ${SAVE_DIR:-<CHECK850_SAVE_DIR>}/{iam-project,iam-intake,iam-creds,secret-accessor}.json, accessor-read and audit.json"
  say "  [not run] the runs, the switches and the removal: yours, never a script's"
  say "  [removed] the token this check stored, read back through the listing"
  for key in person-identity no-project-role person-invoke-intake person-invoke-creds \
    intake-admits token-stored config-named switch-on-a switch-on-b served-a served-b \
    other-run switch-off off-run accessor-conditioned accessor-reads deleted-token \
    removed-refusal owner-prompt; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no container was stored, no cloud was read, and no saved file was opened"
}

BLOCK_KEYS=(fugaro-version xtrace dry-run person-identity no-project-role
  person-invoke-intake person-invoke-creds intake-admits token-stored config-named
  switch-on-a switch-on-b served-a served-b other-run switch-off off-run
  accessor-conditioned accessor-reads deleted-token removed-refusal owner-prompt
  restore scratch-removed)

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
        "$(printf '%s' "$line" | cut -d'|' -f3- | mask_user_names)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$(printf '%s' "$detail" | mask_user_names)" >&3
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
PERSON=${CHECK850_PERSON:-}
WORKSPACE_PROJECT=${CHECK850_WORKSPACE_PROJECT:-}
MEMBER_A=${CHECK850_MEMBER_A:-}
MEMBER_B=${CHECK850_MEMBER_B:-}
MEMBER_OFF=${CHECK850_MEMBER_OFF:-}
REGION=${CHECK850_REGION:-}
INTAKE_SERVICE=${CHECK850_INTAKE_SERVICE:-}
CREDS_SERVICE=${CHECK850_CREDS_SERVICE:-}
WORKSPACE_ACCOUNT=${CHECK850_WORKSPACE_ACCOUNT:-}
SAVE_DIR=${CHECK850_SAVE_DIR:-}
WS_CONFIG=${CHECK850_WS_CONFIG:-}
TOKEN_FILE=${CHECK850_TOKEN_FILE:-}
TOKEN_NAME=${CHECK850_TOKEN_NAME:-claude-oauth-token}
RUN_A=${CHECK850_RUN_A:-}
RUN_B=${CHECK850_RUN_B:-}
OTHER_RUN=${CHECK850_OTHER_RUN:-}
OFF_RUN=${CHECK850_OFF_RUN:-}
DELETED_RUN=${CHECK850_DELETED_RUN:-}
REMOVED_RUN=${CHECK850_REMOVED_RUN:-}
REMOVED_AT=${CHECK850_REMOVED_AT:-}
REMOVAL_WINDOW_SECONDS=${CHECK850_REMOVAL_WINDOW_SECONDS:-90}
DRY_RUN=${CHECK850_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK850_KEEP_SCRATCH:-}
KEEP_TOKEN=${CHECK850_KEEP_TOKEN:-}
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ "$REMOVAL_WINDOW_SECONDS" -gt 0 ] || die "CHECK850_REMOVAL_WINDOW_SECONDS is ${REMOVAL_WINDOW_SECONDS}: the window is seconds a removal may take to reach a run, so it has to be a positive number"
  scratch_up
  trap on_error ERR
  guard_agent
  guard_xtrace
  guard_person
  guard_project
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -z "$WORKSPACE_PROJECT" ]; then
    unmeasured intake-admits "CHECK850_WORKSPACE_PROJECT was not set, so this run does not know the workspace's Fugaro project. Name it: CHECK850_WORKSPACE_PROJECT=<the workspace's project>"
    unmeasured token-stored "CHECK850_WORKSPACE_PROJECT was not set, so there is no workspace to store a token in. Name it: CHECK850_WORKSPACE_PROJECT=<the workspace's project>"
    unmeasured config-named "CHECK850_WORKSPACE_PROJECT was not set, so there is no project to name. Name it: CHECK850_WORKSPACE_PROJECT=<the workspace's project>"
  else
    say "checking the person's own access to ${WORKSPACE_PROJECT}"
    step_person_grants
    step_admits
    step_store
    step_config
  fi
  say "checking the members' switches and the runs the owner launched"
  [ -n "$MEMBER_A" ] || unmeasured switch-on-a "CHECK850_MEMBER_A was not set, so this run does not know the first member whose switch is on. Name it: CHECK850_MEMBER_A=<its project>"
  [ -n "$MEMBER_B" ] || unmeasured switch-on-b "CHECK850_MEMBER_B was not set, so this run does not know the second member whose switch is on. Name it: CHECK850_MEMBER_B=<its project>"
  [ -n "$MEMBER_A" ] && step_switch switch-on-a "$MEMBER_A" ON "the first switch-on member"
  [ -n "$MEMBER_B" ] && step_switch switch-on-b "$MEMBER_B" ON "the second switch-on member"
  if [ -n "$MEMBER_OFF" ]; then
    step_switch switch-off "$MEMBER_OFF" OFF "the member left alone"
  else
    unmeasured switch-off "CHECK850_MEMBER_OFF was not set, so no member's switch was read off: this row is the switch OFF of a member you leave alone (D2972), and it is a member you have to name. Name it: CHECK850_MEMBER_OFF=<its project>"
  fi
  if [ -n "$MEMBER_A" ]; then
    fugaro runs ls --project "$MEMBER_A" --json --all >"$SCRATCH/runs-a.json" 2>"$SCRATCH/runs-a.err" || true
    [ -s "$SCRATCH/runs-a.json" ] && RUNS_A="$SCRATCH/runs-a.json" || say "the run listing for ${MEMBER_A} could not be read ($(last_line "$(cat "$SCRATCH/runs-a.err" 2>/dev/null || true)"))"
  fi
  if [ -n "$MEMBER_B" ]; then
    fugaro runs ls --project "$MEMBER_B" --json --all >"$SCRATCH/runs-b.json" 2>"$SCRATCH/runs-b.err" || true
    [ -s "$SCRATCH/runs-b.json" ] && RUNS_B="$SCRATCH/runs-b.json" || say "the run listing for ${MEMBER_B} could not be read ($(last_line "$(cat "$SCRATCH/runs-b.err" 2>/dev/null || true)"))"
  fi
  step_served served-a "$RUN_A" CHECK850_RUN_A served "the first switch-on member (${MEMBER_A:-unnamed})"
  step_served served-b "$RUN_B" CHECK850_RUN_B served "the second switch-on member (${MEMBER_B:-unnamed})"
  step_served other-run "$OTHER_RUN" CHECK850_OTHER_RUN not-served "the run of a second person"
  step_served off-run "$OFF_RUN" CHECK850_OFF_RUN not-served "the member whose switch is off (${MEMBER_OFF:-unnamed})"
  step_run_halt deleted-token "$DELETED_RUN" CHECK850_DELETED_RUN deleted-token
  step_run_halt removed-refusal "$REMOVED_RUN" CHECK850_REMOVED_RUN removed-refusal
  step_accessor
  step_prompt
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
