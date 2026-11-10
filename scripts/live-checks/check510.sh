#!/usr/bin/env bash
# Check 510 — the name-prefix IAM condition, and the two things IAM cannot do.
#
# Plan task DS0.2 writes it and DS0.3 runs it (its steps are
# docs/secrets-brokering/dynamic-delivery/check510-steps.md). What it settles is
# the reconciliation's U3 and F30, which DS2.2's Terraform is written against:
#
#   * whether an IAM condition on resource.name applies to
#     secretmanager.versions.add and secretmanager.secrets.delete at all — if it
#     does not, the intake's and the service's prefix-conditioned grants are not
#     the fence R3 and D1931 say they are, and the plan returns to the owner;
#   * whether one condition naming BOTH prefixes (fugaro_u_ and fugaro_p_)
#     admits each of them and refuses a name outside them — F6's collision class
#     is a repository's own mounted secret, whose name is fugaro-<slug>-..., so
#     the refusal that matters is measured against that shape and not against a
#     name this check invented;
#   * that secretmanager.secrets.create cannot be gained under the same
#     condition — F30 says the check is made on the project, so a conditioned
#     create admits nothing, not even the prefix it names, which is why the
#     intake holds create unconditioned (and why an unconditioned create of a
#     name outside the prefixes then SUCCEEDS: the permission is project-wide
#     and no name fence exists);
#   * that secretmanager.secrets.list is the same case, so the intake's list is
#     granted unconditioned too;
#   * the maximum secret ID length (R1): a name over the limit is refused, with
#     a sentence that names the limit, and the limit is a number this check
#     reads rather than one the plan assumes.
#
# Usage:      bash scripts/live-checks/check510.sh
#             bash scripts/live-checks/check510.sh --dry-run   # print the plan
#             bash scripts/live-checks/check510.sh --help
# Needs:      bash, mktemp, curl, gcloud (authenticated as an OWNER of the
#             sandbox project: the check makes a throwaway service account,
#             grants it project IAM, and creates and deletes the containers it
#             measures without a condition of their own) and fugaro (the doctor
#             guard below, which is how the check knows it is pointed at the
#             sandbox installation).
# Where:      the fugaro-dev installation ONLY, and the project and region are
#             hard-coded in this file (never `gcloud config get-value project`).
#             A project, region or account naming belong is refused by name.
# Creates:    ONE throwaway service account (chk510-<stamp>), the project IAM
#             bindings its rows need — the two the condition is measured under,
#             and ONE written with no condition (roles/secretmanager.admin — the
#             same role, because Secret Manager has no create-only role), which
#             is what the unconditioned create row is measured on — and the probe
#             containers the rest of its rows are read on: five under the fugaro_u_,
#             fugaro_p_ and fugaro-<slug>-... shapes, plus the containers the
#             length bisection creates (about a dozen). Every one of them is
#             deleted again by the restore, which reads its own writing back:
#             the owner's own credentials do the deleting, never the conditioned
#             account's.
# Costs:      nothing but wall clock and a few API calls: no job, no build and
#             no model call. The IAM waits are the slow part (a fresh binding
#             can take a minute to be seen).
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check510_test.sh) drives it against fake tools only.
# Cleanup:    always, including on a Ctrl-C: the bindings, the service account
#             and every container this check made, each read back rather than
#             assumed. A restore row that FAILs names what to delete by hand.
# Output:     progress on stderr, ONE block on stdout headed CHECK 510 RESULT,
#             then CHECK 510 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure — a tool is missing, a probe of its own
#             could not be set up, or the fresh binding never became visible. In
#             the block, a key the run never reached reads `FAIL — not reached`,
#             which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value and no access token
# reaches this script's stdout, its stderr, the block or any file it writes. The
# probe payload is the plain filler below, sent to the API as a base64 argument
# curl never echoes; the impersonated access token lives in a shell variable and
# is passed to curl in a header curl never prints.
#
# Knobs (all CHECK510_*): DRY_RUN, KEEP_SCRATCH, STAMP (the suffix every name
# this run makes carries; the clock by default), IAM_WAIT_SECONDS and
# IAM_POLL_SECONDS (how long a fresh binding is waited for before its rows are
# read), LENGTH_MAX_PROBE (the highest secret ID length the bisection will try).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the commands below
# carry an access token, and the shell it is started from may be a login shell
# whose trace ends up in a log.
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

# unmeasured: a row the check could NOT measure, for a reason that is the
# check's own setup rather than a finding about IAM. The row still reads FAIL in
# the block — it is not a pass and must not be read as one — and the check ends
# as one that could not measure (exit 2).
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
  local t
  for t in bash mktemp curl gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 510 needs '$t' on PATH and it is not there"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk510-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""

# Every name this check creates is recorded the moment it exists, and restore
# deletes exactly this list: a container a row's own call created (the
# conditioned create, if it were ever admitted) is in it too.
MADE_SECRETS=()
BINDINGS_MADE=()
SA_EMAIL=""
SA_NAME=""
SA_EMAIL_WANTED=""
TOKEN=""
API_STATUS=""
API_BODY_FILE=""
CONDITION_LIVE=no
LENGTH_STATUS=""
LENGTH_DETAIL=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk510.XXXXXXXX") || die "Check 510 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  API_BODY_FILE="$SCRATCH/api.body"
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
    say "CHECK510_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# each_binding: every binding this run recorded, one "member|role|title" per
# line, and nothing when it recorded none (an unquoted ${arr[@]} under `set -u`
# is the trap this exists to avoid).
each_binding() {
  local b
  for b in ${BINDINGS_MADE[@]+"${BINDINGS_MADE[@]}"}; do printf '%s\n' "$b"; done
}

each_secret() {
  local s
  for s in ${MADE_SECRETS[@]+"${MADE_SECRETS[@]}"}; do printf '%s\n' "$s"; done
}

# restore: everything this check made, deleted and read back. It runs in the
# EXIT trap, so it runs however the check ended: the bindings are the part that
# matters — a conditioned secretmanager.admin left on a live account is a real
# grant in the owner's project — and the containers are the part with the
# owner's own project's name on them.
restore() {
  local leftover="" name member role title conditioned
  local args=()
  while IFS='|' read -r member role title conditioned; do
    [ -n "$title" ] || continue
    # The removal names the condition EXACTLY as the add wrote it — gcloud
    # matches the binding it removes on the whole condition, so a title alone
    # matches nothing (and gcloud refuses a condition that carries no
    # expression), which would leave the grant on the owner's project and report
    # it as removed. An unconditioned binding is named with --condition=None for
    # the same reason the add states it: the policy carries conditions, so
    # gcloud refuses a removal that does not say which one it means.
    args=(--member="$member" --role="$role")
    if [ "$conditioned" = conditioned ]; then
      args+=(--condition="title=$title,expression=$(condition_expression)")
    else
      args+=(--condition=None)
    fi
    gcloud_admin projects remove-iam-policy-binding "$P" "${args[@]}" >/dev/null 2>&1 || true
    if [ "$conditioned" = conditioned ]; then
      if title_present "$title"; then leftover="$leftover binding($member,$role,$title)"; fi
    else
      # An unconditioned binding carries no condition, so there is no title in
      # the policy to look for and the read-back is the member itself: the
      # project naming it at all means a binding this run wrote is still there.
      # Every other binding recorded for this member was written earlier (in
      # step_bindings, before step_create's own) and is removed above before this
      # one is read.
      if member_present "$member"; then leftover="$leftover binding($member,$role,no condition)"; fi
    fi
  done < <(each_binding)
  while read -r name; do
    [ -n "$name" ] || continue
    gcloud_admin secrets delete "$name" >/dev/null 2>&1 || true
    if secret_exists "$name"; then leftover="$leftover secret($name)"; fi
  done < <(each_secret)
  if [ -n "$SA_EMAIL" ]; then
    gcloud_admin iam service-accounts delete "$SA_EMAIL" >/dev/null 2>&1 || true
    if service_account_exists "$SA_EMAIL"; then leftover="$leftover service-account($SA_EMAIL)"; fi
    if member_present "serviceAccount:$SA_EMAIL"; then leftover="$leftover member(serviceAccount:$SA_EMAIL)"; fi
  fi
  if [ -n "$leftover" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="this check could not confirm what it deleted:$leftover — delete them yourself: gcloud secrets delete <name> --project $P; gcloud iam service-accounts delete <email> --project $P; gcloud projects remove-iam-policy-binding $P --member=<member> --role=<role> (with --condition=None for the one this run wrote with no condition, and with the whole condition — --condition=\"title=<title>,expression=$(condition_expression)\" — for one it wrote under a condition: gcloud matches on both halves, and a title alone removes nothing)"
    return 0
  fi
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="every binding this run wrote, the service account ${SA_EMAIL:-none made} and every container it created ($(each_secret | grep -c . || true)) were deleted and read back: nothing of this check's is left in ${P}"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to delete bindings
  # and containers, and a second Ctrl-C during it would kill this shell half
  # done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  # Both rows are added before the verdict is taken, because the verdict is
  # taken from the facts: a binding or a directory this check could not remove
  # is a FAIL, and a block that printed `restore: FAIL` beside `END (exit 0)`
  # would read as a pass.
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

# ------------------------------------------------------------------ the tools
gcloud_admin() { gcloud --project="$P" --quiet "$@"; }

# token_for_sa: an access token minted FOR the throwaway account. That the rows
# are made as that account and not as the owner is the whole point of the check:
# the owner's own project Owner role carries every permission unconditioned and
# would answer every row yes.
token_for_sa() {
  gcloud auth print-access-token --impersonate-service-account="$SA_EMAIL" 2>/dev/null
}

# api METHOD URL [BODY]: one call, as the throwaway account. The status lands in
# API_STATUS and the body in a file the block never prints; a transport failure
# is a status of 000 rather than a `set -e` exit, because every row below has to
# report what it saw rather than end the run.
api() {
  local method=$1 url=$2 body=${3:-}
  : >"$API_BODY_FILE"
  local args=(-sS -o "$API_BODY_FILE" -w '%{http_code}' -X "$method" "$url"
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json')
  if [ -n "$body" ]; then args+=(--data-binary "$body"); fi
  API_STATUS=$(curl "${args[@]}" 2>/dev/null || true)
  [ -n "$API_STATUS" ] || API_STATUS=000
}

api_url() { printf 'https://secretmanager.googleapis.com/v1/projects/%s' "$P"; }

# api_detail: the message the API returned, cut short. A message from Secret
# Manager names a permission or a limit and never a value.
api_detail() {
  local msg
  msg=$(tr ',' '\n' <"$API_BODY_FILE" 2>/dev/null | sed -n 's/.*"message": *"\([^"]*\)".*/\1/p' | tail -n 1 || true)
  [ -n "$msg" ] || msg=$(head -c 200 "$API_BODY_FILE" 2>/dev/null || true)
  printf '%s' "$msg" | head -c 240
}

secret_exists() { gcloud_admin secrets describe "$1" >/dev/null 2>&1; }
service_account_exists() { gcloud_admin iam service-accounts describe "$1" >/dev/null 2>&1; }

# title_present / member_present: the project policy, read back. Every condition
# this check writes carries a title of its own (the stamp is in it), so a title
# that is still there IS a binding this run left behind, and a member still
# named by the policy is a binding nobody removed whatever its title was. The
# whitespace goes first because gcloud's own JSON puts a space after each colon
# in one view and not in another, and a read-back that depended on that would be
# a read-back nobody could trust.
title_present() {
  gcloud_admin projects get-iam-policy "$P" --format=json 2>/dev/null |
    tr -d ' \t\n' | grep -F "\"title\":\"$1\"" >/dev/null 2>&1
}
member_present() {
  gcloud_admin projects get-iam-policy "$P" --format=json 2>/dev/null |
    tr -d ' \t\n' | grep -F "\"$1\"" >/dev/null 2>&1
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check510.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would carry the impersonated access token"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token this check mints for the throwaway account"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`.
  # Check 510 writes IAM and containers into a project: run anywhere else and it
  # writes them into another one, which is why this stops rather than warns.
  local doctor_out v
  doctor_out=$(fugaro doctor --project fugaro --all || true)
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP fugaro-dev,' >/dev/null; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project fugaro --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check writes IAM and containers into ${P} and into no other project, and never into belong"
  fi
  case "${P}${L}${ACCOUNT:-}" in
  *belong*) stop "the project, region or account this check names is belong: this check is never run there" ;;
  esac
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  # Not a release gate, unlike the checks that measure a run: what it reads is
  # Secret Manager's own IAM, so any build of fugaro does here.
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (this check reads Secret Manager's IAM, not a run: any build will do)"
  fact "project-region" PASS "${P}/${L} (installation fugaro, sandbox only; never belong)"
}

guard_names_free() {
  # This check never adopts: a container or an account already there under one of
  # its own names is a hard stop before it writes anything.
  local name
  for name in "$PROBE_U" "$PROBE_P" "$PROBE_N" "$PROBE_U2" "$PROBE_N2"; do
    if secret_exists "$name"; then
      stop "the secret $name already exists in ${P}: this check never adopts or overwrites a container it did not create. Delete it if it is an earlier attempt's (gcloud secrets delete $name --project $P) and run again"
    fi
  done
  if service_account_exists "$SA_EMAIL_WANTED"; then
    stop "the service account $SA_EMAIL_WANTED already exists in ${P}: this check never adopts one it did not create. Delete it if it is an earlier attempt's (gcloud iam service-accounts delete $SA_EMAIL_WANTED --project $P) and run again"
  fi
}

# ------------------------------------------------------------------ the steps
# The condition DS2.2 will write, in the shape this check measures: one
# expression, and both prefixes named.
#
# It anchors with resource.name.extract("/secrets/{secret}") and NOT with
# resource.name.startsWith("projects/<project>/secrets/…"): a secret's
# resource.name carries the project NUMBER, never the ID a person types, so an
# ID-form condition matches nothing at all — every row would come back unmeasured
# and U3 would read as a no when the condition was simply written against the
# wrong string. The extraction form is the one this repository already writes
# (deploy/terraform/gcp/modules/installation/deployer.tf, whose comment records
# the same trap), and it is also what makes ONE clause reach a SecretVersion
# (projects/<number>/secrets/<id>/versions/<n>), the resource
# secretmanager.versions.add is evaluated on: a clause carrying resource.type
# would have to name both Secret and SecretVersion to do the same, so this one
# names no type at all.
condition_expression() {
  printf 'resource.name.extract("/secrets/{secret}").startsWith("%s") || resource.name.extract("/secrets/{secret}").startsWith("%s")' \
    "$PREFIX_U" "$PREFIX_P"
}

add_binding() { # add_binding ROLE TITLE conditioned|unconditioned
  local role=$1 title=$2 conditioned=${3:-conditioned}
  local how=" unconditioned" args=(--member="serviceAccount:$SA_EMAIL" --role="$role")
  if [ "$conditioned" = conditioned ]; then
    how=" under the condition '${title}'"
    args+=(--condition="title=$title,expression=$(condition_expression)")
  else
    # --condition=None and not "no --condition at all": once the policy carries a
    # condition, gcloud refuses to add a binding without one in a
    # non-interactive shell — its own safety check, and the reason the message
    # tells you to run the command again with --condition=None. step_bindings has
    # already written the two conditioned grants by the time this runs, so
    # leaving it out would stop the check here. check91.sh writes its
    # unconditional project grants the same way.
    args+=(--condition=None)
  fi
  if ! gcloud_admin projects add-iam-policy-binding "$P" "${args[@]}" >/dev/null 2>&1; then
    die "could not grant ${role} to ${SA_EMAIL}${how} on ${P}: adding a project binding needs resourcemanager.projects.setIamPolicy, which a project Owner has, and the role has to be one this project can bind — a name that is not a role of the project is refused here the same way. Run 'gcloud projects add-iam-policy-binding $P --member=serviceAccount:$SA_EMAIL --role=$role' here yourself to read the error. Nothing else was written"
  fi
  BINDINGS_MADE+=("serviceAccount:$SA_EMAIL|$role|$title|$conditioned")
}

step_account() {
  if ! gcloud_admin iam service-accounts create "$SA_NAME" \
    --display-name="Check 510 probe (delete me)" >/dev/null 2>&1; then
    die "the throwaway service account ${SA_NAME} could not be created in ${P}: creating one needs iam.serviceAccounts.create, which a project Owner has. Run 'gcloud iam service-accounts create $SA_NAME --project $P' here yourself to read the error"
  fi
  SA_EMAIL="${SA_NAME}@${P}.iam.gserviceaccount.com"
  fact "test-account" PASS "${SA_EMAIL} (a throwaway account, deleted by the restore)"
  # The owner impersonates it, so the owner needs the token-creator role ON the
  # account. That binding goes away with the account it is written on.
  if ! gcloud_admin iam service-accounts add-iam-policy-binding "$SA_EMAIL" \
    --member="user:$ACCOUNT" --role=roles/iam.serviceAccountTokenCreator >/dev/null 2>&1; then
    die "could not grant $ACCOUNT roles/iam.serviceAccountTokenCreator on ${SA_EMAIL}: the check has to act AS the throwaway account (the owner's own role answers every row yes), and impersonation needs that grant. Nothing else was written"
  fi
  TOKEN=$(token_for_sa || true)
  [ -n "$TOKEN" ] || wait_for_token
  say "acting as ${SA_EMAIL} from here on (no token is printed)"
}

# wait_for_token: the impersonated access token, retried until it can be minted.
# The roles/iam.serviceAccountTokenCreator binding was written a moment ago, and
# a fresh binding is not always visible yet: minting is the FIRST thing that needs
# it, so without this wait a project whose IAM is a minute behind ends the run
# (exit 2) before a single row is read — while every other IAM-dependent read here
# retries. The token is never printed, by this or any other path.
wait_for_token() {
  local waited=0
  while :; do
    [ "$waited" -lt "$IAM_WAIT_SECONDS" ] || break
    sleep "$IAM_POLL_SECONDS"
    waited=$((waited + IAM_POLL_SECONDS))
    TOKEN=$(token_for_sa || true)
    if [ -n "$TOKEN" ]; then
      say "the impersonation grant became visible after ${waited}s of waiting (CHECK510_IAM_POLL_SECONDS=${IAM_POLL_SECONDS} between attempts)"
      return 0
    fi
  done
  die "gcloud could not mint an access token for ${SA_EMAIL} within ${IAM_WAIT_SECONDS}s, with the roles/iam.serviceAccountTokenCreator binding for ${ACCOUNT} written on it: a fresh binding can take a minute to be seen (rerun with a longer CHECK510_IAM_WAIT_SECONDS), and the grant has to be one ${ACCOUNT} really holds — run 'gcloud auth print-access-token --impersonate-service-account=$SA_EMAIL' here yourself to read the error. Nothing else was written"
}

step_make_probes() {
  local name
  for name in "$PROBE_U" "$PROBE_P" "$PROBE_N" "$PROBE_U2" "$PROBE_N2"; do
    if ! gcloud_admin secrets create "$name" --replication-policy=automatic --data-file=- >/dev/null 2>&1 <<<"$PROBE_PAYLOAD"; then
      die "the probe container ${name} could not be created in ${P}: creating secrets is what this check's rows are measured on, and the owner's own account must be able to (Owner, or roles/secretmanager.admin). Run 'gcloud secrets create $name --project $P' here yourself to read the error"
    fi
    MADE_SECRETS+=("$name")
  done
  say "five probe containers created (a fugaro_u_ name, a fugaro_p_ one, the fugaro-<slug>-... shape, and one more of each for the delete rows)"
}

step_bindings() {
  # The conditioned grants: secretmanager.admin carries versions.add AND
  # secrets.delete — the two permissions R3's fence is about — and viewer
  # carries secrets.list, whose row says why it is granted unconditioned. Each
  # binding carries a title of its own, so the restore can tell the two apart
  # and read each one back by name. Both are written BEFORE the wait below, so
  # the wait covers the propagation of both.
  add_binding roles/secretmanager.admin "chk510-admin-${STAMP}"
  add_binding roles/secretmanager.viewer "chk510-viewer-${STAMP}"
  fact bindings PASS "roles/secretmanager.admin and roles/secretmanager.viewer granted to ${SA_EMAIL} under the condition: $(condition_expression)"
  wait_for_admission
}

# wait_for_admission: the allow row is retried until it is admitted, because a
# binding written a moment ago is not always visible yet. It is the SAME call
# the allow row then measures, and a success here is what makes the deny rows
# meaningful: the condition is live, and a refusal beside it is the condition's
# rather than the absence of the grant.
wait_for_admission() {
  local waited=0
  while :; do
    add_version "$PROBE_U"
    if [ "$API_STATUS" = 200 ]; then
      CONDITION_LIVE=yes
      fact "condition-version-add-allow" PASS "secretmanager.versions.add was ADMITTED on ${PROBE_U} (a fugaro_u_ name) under the condition, after ${waited}s of waiting for the fresh binding: the condition applies to versions.add, which is U3's first half"
      return 0
    fi
    [ "$waited" -lt "$IAM_WAIT_SECONDS" ] || break
    sleep "$IAM_POLL_SECONDS"
    waited=$((waited + IAM_POLL_SECONDS))
    TOKEN=$(token_for_sa || true)
  done
  unmeasured condition-version-add-allow "the conditioned grant never admitted versions.add on ${PROBE_U} within ${IAM_WAIT_SECONDS}s (last status ${API_STATUS}: $(api_detail)). Three things look like this and only one of them is U3's finding: the binding had not propagated (rerun with a longer CHECK510_IAM_WAIT_SECONDS and read this row again); the condition names the secret in a form that cannot match — a secret's resource.name carries the project NUMBER, so a condition written with the project ID matches nothing, and the expression this run wrote is $(condition_expression); or the condition does not apply to versions.add at all, which IS the finding U3 asks for and would make DS2.2's fence something else. The rows that follow are reported as not measured, because a refusal beside a grant nobody has shown to be live is not the condition refusing"
}

# not_live: the rows that only measure something once the conditioned grant has
# been seen working. Without that sighting a 403 is as consistent with no grant
# at all as with the condition refusing, so the row is not measured.
not_live() { # not_live KEY "what it would have measured"
  unmeasured "$1" "not measured: the conditioned allow row never showed the grant live, so a refusal here would be the grant being absent rather than the condition refusing — $2"
}

step_version_add() {
  if [ "$CONDITION_LIVE" != yes ]; then
    not_live condition-prefix-allow "whether the one condition's second clause admits the fugaro_p_ prefix"
    not_live condition-version-add-deny "whether the condition refuses a fugaro-<slug>-... name"
    return 0
  fi
  add_version "$PROBE_P"
  if [ "$API_STATUS" = 200 ]; then
    fact condition-prefix-allow PASS "secretmanager.versions.add was ADMITTED on ${PROBE_P} (a fugaro_p_ name): the one condition's second clause admits the shared prefix as well, so DS2.2 needs one expression and not two bindings"
  else
    fact condition-prefix-allow FAIL "secretmanager.versions.add on ${PROBE_P} (a fugaro_p_ name) answered ${API_STATUS}: $(api_detail). The condition admits the per-user prefix and not the shared one, so DS2.2 needs a second binding rather than the one expression this check measured"
  fi
  add_version "$PROBE_N"
  if [ "$API_STATUS" = 200 ]; then
    fact condition-version-add-deny FAIL "secretmanager.versions.add was ADMITTED on ${PROBE_N} — a fugaro-<slug>-... name, the shape F6 collides with — under a condition that does not name it: THE CONDITION DID NOT REFUSE, so a name-prefix IAM condition is not the fence R3 and D1931 say it is, and this returns to the owner before DS2.2 is written"
  elif [ "$API_STATUS" = 403 ]; then
    fact condition-version-add-deny PASS "secretmanager.versions.add on ${PROBE_N} (the fugaro-<slug>-... shape a repository's own mounted secret has) was REFUSED with 403: $(api_detail) — the condition fences versions.add by name, which is R3's premise"
  else
    fact condition-version-add-deny FAIL "secretmanager.versions.add on ${PROBE_N} answered ${API_STATUS} and not 403: $(api_detail). Only a 403 is the condition refusing; any other status is this check's request being wrong, and the row did NOT measure the condition"
  fi
}

step_delete() {
  local state
  if [ "$CONDITION_LIVE" != yes ]; then
    not_live condition-delete-allow "whether the condition admits secrets.delete on a fugaro_u_ name"
    not_live condition-delete-deny "whether the condition refuses secrets.delete on a name outside both prefixes"
    return 0
  fi
  delete_secret "$PROBE_U2"
  if [ "$API_STATUS" = 204 ] || [ "$API_STATUS" = 200 ]; then
    if secret_exists "$PROBE_U2"; then
      fact condition-delete-allow FAIL "the delete of ${PROBE_U2} answered ${API_STATUS} and the container is STILL THERE: the status and the reading disagree, which is a finding about this check rather than about IAM"
    else
      fact condition-delete-allow PASS "secretmanager.secrets.delete DELETED ${PROBE_U2} (a fugaro_u_ name) under the same condition and the container reads back gone: the condition applies to secrets.delete, U3's second half"
    fi
  else
    fact condition-delete-allow FAIL "secretmanager.secrets.delete on ${PROBE_U2} (a fugaro_u_ name) answered ${API_STATUS}: $(api_detail). If the condition applies to versions.add and not to secrets.delete, DS2.2's delete grant is not fenced and the offboarding power R3 names is wider than the plan says"
  fi
  delete_secret "$PROBE_N2"
  if [ "$API_STATUS" = 204 ] || [ "$API_STATUS" = 200 ]; then
    fact condition-delete-deny FAIL "secretmanager.secrets.delete DELETED ${PROBE_N2} — a name outside both prefixes — under a condition that does not name it: THE CONDITION DID NOT REFUSE the delete, so an offboarding delete is not fenced by the name prefix"
  elif [ "$API_STATUS" = 403 ]; then
    state=$(if secret_exists "$PROBE_N2"; then echo "still there"; else echo "GONE"; fi)
    if [ "$state" = "still there" ]; then
      fact condition-delete-deny PASS "secretmanager.secrets.delete on ${PROBE_N2} (a name outside both prefixes) was REFUSED with 403 and the container is still there: $(api_detail)"
    else
      fact condition-delete-deny FAIL "the delete of ${PROBE_N2} answered 403 and the container is GONE: the reading and the status disagree, which is a finding about this check rather than about IAM"
    fi
  else
    fact condition-delete-deny FAIL "secretmanager.secrets.delete on ${PROBE_N2} answered ${API_STATUS} and not 403: $(api_detail) — the row did NOT measure the condition"
  fi
}

step_create() {
  local name="${PROBE_U_PREFIX}maked-${STAMP}"
  # The conditioned create, aimed at the prefix the condition itself names: were
  # IAM to evaluate the condition against the new secret, this would be
  # admitted, and F30 says it cannot (the check is made on the project, where
  # the secret does not exist yet). That is why the row is aimed at the prefix
  # rather than at a name outside it — a refusal of a name the condition does
  # not name would say nothing.
  if [ "$CONDITION_LIVE" != yes ]; then
    not_live create-conditioned "whether a conditioned grant admits a create of the prefix its condition names"
  else
    create_secret "$name"
    if [ "$API_STATUS" = 200 ]; then
      record_secret "$name"
      fact create-conditioned FAIL "secretmanager.secrets.create WAS ADMITTED for ${name} — a fugaro_u_ name, the prefix the condition names — under the conditioned grant: create IS fenceable by name here, so DS2.2's unconditioned create is unnecessary and the intake could be fenced after all. Report this reading to the owner before DS2.2"
    elif [ "$API_STATUS" = 403 ]; then
      fact create-conditioned PASS "secretmanager.secrets.create was REFUSED with 403 even for a name the condition names ($(api_detail)): IAM checks create on the project, where the secret does not exist yet — F30's reading, and why the intake's create is gained UNCONDITIONED"
    else
      fact create-conditioned FAIL "secretmanager.secrets.create on a fugaro_u_ name answered ${API_STATUS} and not 200 or 403: $(api_detail) — the row did NOT measure what F30 claims"
    fi
  fi

  # Then the unconditioned grant, which is what the intake will hold: F30 says
  # the create check is made on the project, so there is no name to condition
  # against and the intake holds the permission project-wide. It is added AFTER
  # the row above, so that row measured the conditioned grant alone — and the
  # SAME role is used, which is what makes the pair a reading: the one call
  # answered 403 under the condition and 200 without it, so the condition is the
  # only thing that differs between the two rows.
  #
  # roles/secretmanager.admin rather than a role of its own because it has to be
  # a role that exists — Secret Manager has six predefined roles and none of them
  # is a create-only one, so create cannot be granted without also granting list
  # (see step_list's own note, which is why that step runs before this one).
  add_binding roles/secretmanager.admin "chk510-admin-unconditioned-${STAMP}" unconditioned
  name="fugaro-chk510-${STAMP}-made"
  wait_for_unconditioned_create "$name"
}

# wait_for_unconditioned_create NAME: the unconditioned create, retried until it
# is seen. The same call is the row, so there is nothing to read twice: a 200 is
# an unconditioned create of a name OUTSIDE both prefixes being admitted, which
# is F30's other half and the power DS2.2 grants the intake.
wait_for_unconditioned_create() {
  local name=$1 waited=0
  while :; do
    create_secret "$name"
    if [ "$API_STATUS" = 200 ]; then
      record_secret "$name"
      fact create-unconditioned PASS "with roles/secretmanager.admin and NO condition — the same role the create-conditioned row was REFUSED under — secretmanager.secrets.create was ADMITTED for ${name} — a name outside both prefixes — after ${waited}s: an unconditioned create is project-wide, which is the power the intake holds and the reason its fence is the code that derives the prefix from the verified sub (R3)"
      return 0
    fi
    [ "$waited" -lt "$IAM_WAIT_SECONDS" ] || break
    sleep "$IAM_POLL_SECONDS"
    waited=$((waited + IAM_POLL_SECONDS))
    TOKEN=$(token_for_sa || true)
  done
  unmeasured create-unconditioned "secretmanager.secrets.create answered ${API_STATUS} on every attempt for ${IAM_WAIT_SECONDS}s under an UNCONDITIONED roles/secretmanager.admin grant: $(api_detail). Either the grant had not propagated — rerun with a longer CHECK510_IAM_WAIT_SECONDS — or create is not admitted unconditioned, which would leave the intake unable to create anything and returns to the owner before DS2.2"
}

record_secret() { if secret_exists "$1"; then MADE_SECRETS+=("$1"); fi; }

# step_list runs BEFORE step_create, and that order is the row: this measures
# what the CONDITIONED grants admit, and the grants it has to measure against are
# the two step_bindings wrote. It cannot run after step_create, because no
# predefined Secret Manager role carries secrets.create without secrets.list —
# roles/secretmanager.admin and roles/secretmanager.editor both carry both — so
# the unconditioned create grant this check writes (F30's other half, a role that
# exists) would also admit this listing, which would then read this check's own
# containers and fail the row on an artifact of the check's own grants rather
# than on anything about IAM.
step_list() {
  local seen_owner seen_conditioned
  # F30's list half: secrets.list is checked on the project, so the same
  # condition admits nothing at all. The owner's own listing is the control — a
  # conditioned listing that reads empty while the unconditioned one sees this
  # check's own containers is what says the condition admits no list.
  #
  # Both listings are counted by this run's OWN probe names, never by a substring
  # of them: only two of the five carry `chk510-` (the fugaro-<slug>-... pair),
  # while the two fugaro_u_ probes and the fugaro_p_ one carry `chk510a-`,
  # `chk510b-` and `chk510shared-`. A substring count would therefore read 0 for
  # a listing IAM had fenced to exactly the two prefixes — and PASS on the very
  # reading this row exists to catch.
  seen_owner=$(gcloud_admin secrets list --format='value(name)' 2>/dev/null | sed 's|.*/secrets/||' | probes_in || true)
  api GET "$(api_url)/secrets?pageSize=200"
  if [ "$CONDITION_LIVE" != yes ]; then
    not_live list-condition-admits-nothing "whether a conditioned grant admits secrets.list at all"
    return 0
  fi
  case "$API_STATUS" in
  403)
    fact list-condition-admits-nothing PASS "secrets.list under the conditioned grant was REFUSED with 403: $(api_detail). A listing is made on the project, where this run's grant is proven live and names no project — so the condition admits no listing at all, which is why the intake's list is granted UNCONDITIONED (F30) and the service's list sits beside its prefix accessor"
    return 0
    ;;
  200) ;;
  *)
    fact list-condition-admits-nothing FAIL "secrets.list under the conditioned grant answered ${API_STATUS} and not 200 or 403: $(api_detail) — the row did NOT measure whether a name-prefix condition admits a listing"
    return 0
    ;;
  esac
  seen_conditioned=$(listing_ids "$API_BODY_FILE" | probes_in || true)
  if [ "$seen_conditioned" -eq 0 ] && [ "$seen_owner" -gt 0 ]; then
    fact list-condition-admits-nothing PASS "the conditioned grant listed none of the ${seen_owner} probe containers this run made that the owner's own listing sees: the condition admits no secrets.list at all, which is why the intake's list is granted UNCONDITIONED (F30) and the service's list sits beside its prefix accessor"
  else
    fact list-condition-admits-nothing FAIL "the conditioned grant listed ${seen_conditioned} of the ${seen_owner} probe containers this run made while the owner's own listing sees them: the condition did NOT admit nothing, so secrets.list IS fenced by name here and F30's reading — and the unconditioned list DS2.2 writes — returns to the owner"
  fi
}

# probes_in: how many of this run's five probe containers the secret IDs on
# stdin name, one per line. Each name is matched WHOLE: the second delete-row
# probe's id is the first one's with a `2` on the end, so a substring test would
# count a container that is not there. `seen_owner`'s and `seen_conditioned`'s
# listings are read through this one function, so the two counts are the same
# measurement.
probes_in() {
  local ids seen=0 name
  ids=$(cat)
  for name in "$PROBE_U" "$PROBE_P" "$PROBE_N" "$PROBE_U2" "$PROBE_N2"; do
    if printf '%s\n' "$ids" | grep -qxF "$name"; then seen=$((seen + 1)); fi
  done
  printf '%d' "$seen"
}

# listing_ids FILE: the secret IDs a Secret Manager listing body names.
listing_ids() { # listing_ids FILE
  grep -o '"name": *"[^"]*"' "$1" 2>/dev/null | sed 's|.*/secrets/||; s|"$||'
}

# step_id_length: the largest secret ID Secret Manager admits, measured rather
# than assumed (R1's limit sentence names it). A create is what measures it: the
# ID is the caller's, its length is the only variable, and only a refusal the API
# calls INVALID_ARGUMENT is the limit's. Every admitted name is recorded for the
# restore, because each one is a real container.
step_id_length() {
  local lo=${#LENGTH_PREFIX} hi=$LENGTH_MAX_PROBE mid best refused refused_detail="" answer
  # The low end of the bisection is a probe the check can NAME: the stamped
  # prefix itself, which the probe below shows to be admitted. Starting at one
  # character would write a one-character container into the owner's project, and
  # a name that short can only be a name somebody else already holds.
  if [ "$hi" -le "$lo" ]; then
    unmeasured secret-id-length "CHECK510_LENGTH_MAX_PROBE=${hi} is not above the ${lo}-character stamped probe this bisection starts from: raise it and run again"
    return 0
  fi
  answer=0
  length_probe "$lo" || answer=$?
  case "$answer" in
  0) record_secret "$(length_name "$lo")" ;;
  1)
    unmeasured secret-id-length "the ${lo}-character stamped probe was REFUSED as INVALID_ARGUMENT (${LENGTH_DETAIL:-the refusal carried no message in its body}): the ID limit is below the length of the check's own prefix, so there is nothing here for a bisection to start from"
    return 0
    ;;
  *)
    unmeasured secret-id-length "the ${lo}-character stamped probe answered ${LENGTH_STATUS} and not 200 or 400: ${LENGTH_DETAIL:-no message in its body}. Only a 400 INVALID_ARGUMENT is the length limit refusing, so this row did not measure the limit"
    return 0
    ;;
  esac
  answer=0
  length_probe "$hi" || answer=$?
  case "$answer" in
  0)
    record_secret "$(length_name "$hi")"
    unmeasured secret-id-length "a ${hi}-character secret ID was ADMITTED: the limit is above CHECK510_LENGTH_MAX_PROBE=${hi}. Raise it and run again"
    return 0
    ;;
  1)
    refused=$hi
    refused_detail=$LENGTH_DETAIL
    ;;
  *)
    unmeasured secret-id-length "the ${hi}-character create answered ${LENGTH_STATUS} and not 200 or 400: ${LENGTH_DETAIL:-no message in its body}. Only a 400 is the limit refusing — a 409 is a container of that name already in the project, which is a leftover and not a limit — so the bisection was not started"
    return 0
    ;;
  esac
  while [ $((hi - lo)) -gt 1 ]; do
    mid=$(((lo + hi) / 2))
    answer=0
    length_probe "$mid" || answer=$?
    case "$answer" in
    0)
      record_secret "$(length_name "$mid")"
      lo=$mid
      ;;
    1)
      refused=$mid
      # Read here, beside the call that was refused: api_detail reads the body
      # of the LAST call, and the loop's last call is usually an admitted one,
      # which would put a success body under a sentence saying "was refused".
      refused_detail=$LENGTH_DETAIL
      hi=$mid
      ;;
    *)
      unmeasured secret-id-length "the ${mid}-character create answered ${LENGTH_STATUS} and not 200 or 400: ${LENGTH_DETAIL:-no message in its body}. A refusal that is not INVALID_ARGUMENT is not the length limit, so the bisection cannot go on: reading one as the limit would report a limit nobody measured"
      return 0
      ;;
    esac
  done
  best=$lo
  fact secret-id-length PASS "the longest admitted secret ID is ${best} characters and ${refused} was refused: ${refused_detail:-the refusal carried no message in its body}. R1's limit sentence and DS0.1's length test name ${best}; a fugaro_u_<id>_<name> name spends 10 characters of prefix and 16 of Google account ID before the caller's own name"
}

# length_probe L: creates the L-character probe and classifies the answer, which
# is the whole reading this row turns on. 0 admitted, 1 the length limit's own
# INVALID_ARGUMENT, 2 a refusal this row cannot read as a limit — with the status
# and the body left in LENGTH_STATUS and LENGTH_DETAIL. Called as
# `answer=0; length_probe L || answer=$?`, because a non-zero return would end
# the script under `set -e` if it stood alone.
length_probe() { # length_probe L
  create_secret "$(length_name "$1")"
  case "$API_STATUS" in
  200) return 0 ;;
  400)
    LENGTH_DETAIL=$(api_detail)
    return 1
    ;;
  *)
    LENGTH_STATUS=$API_STATUS
    LENGTH_DETAIL=$(api_detail)
    return 2
    ;;
  esac
}

# length_name L: a legal secret ID of exactly L characters, growing out of the
# stamped prefix — so the bisection never writes a name the project could already
# hold (it asks for L >= the prefix's own length) — and the prefix's own leading
# characters for an L below that, which nothing asks for today and which keeps the
# function total rather than accidentally right only where it is called.
length_name() {
  local l=$1 pad
  if [ "${#LENGTH_PREFIX}" -ge "$l" ]; then printf '%s' "${LENGTH_PREFIX:0:l}"; return 0; fi
  pad=$(printf '%*s' "$((l - ${#LENGTH_PREFIX}))" '' | tr ' ' 'a')
  printf '%s%s' "$LENGTH_PREFIX" "$pad"
}

create_secret() { api POST "$(api_url)/secrets?secretId=$1" '{"replication":{"automatic":{}}}'; }
add_version() { api POST "$(api_url)/secrets/$1:addVersion" "{\"payload\":{\"data\":\"$PAYLOAD_B64\"}}"; }
delete_secret() { api DELETE "$(api_url)/secrets/$1"; }

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 510 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guards: fugaro doctor --project fugaro --all (it reads the installation's config, policy and billing, and the update check, so this is the dry run's one network read) and fugaro version"
  say "  [network, write] gcloud iam service-accounts create ${SA_NAME} (throwaway), then roles/iam.serviceAccountTokenCreator for $ACCOUNT on it"
  say "  [network, write] gcloud secrets create for five probes: ${PROBE_U}, ${PROBE_P}, ${PROBE_N}, ${PROBE_U2}, ${PROBE_N2}"
  say "  [network, write] gcloud projects add-iam-policy-binding ${P} --role=roles/secretmanager.admin|viewer under the condition: $(condition_expression), then roles/secretmanager.admin again with --condition=None (the create row's grant: Secret Manager has no create-only role, and gcloud refuses an unconditional add on a policy that already carries a condition unless None is said out loud)"
  say "  [network, read] POST .../secrets/<name>:addVersion, DELETE .../secrets/<name>, POST .../secrets?secretId=<name>, GET .../secrets — as ${SA_NAME}, one call per row"
  say "  [network, write] the length bisection's creates (up to ${LENGTH_MAX_PROBE} characters, about 11 calls)"
  say "  [network, write] the restore: the bindings, the account and every container this check made, deleted and read back"
  for key in fugaro-version project-region xtrace test-account bindings \
    condition-version-add-allow condition-prefix-allow condition-version-add-deny \
    condition-delete-allow condition-delete-deny create-conditioned create-unconditioned \
    list-condition-admits-nothing secret-id-length; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no binding was written, no account was made and no container was created"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region xtrace dry-run test-account bindings
  condition-version-add-allow condition-prefix-allow condition-version-add-deny
  condition-delete-allow condition-delete-deny
  create-conditioned create-unconditioned list-condition-admits-nothing
  secret-id-length restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 510 RESULT\n' >&3
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
  printf 'CHECK 510 RESULT END (exit %s)\n' "$status" >&3
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
# Hard-coded, never `gcloud config get-value project`: a check that reads the
# project from the environment can be pointed at another installation by a stray
# config, and this one writes IAM and containers.
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

# The two prefixes R1 defines, and the shape the existing scheme emits, which is
# what the deny rows are aimed at: a repository owned by `p`, `u` or `p-labs`
# makes fugaro-p-labs-app-..., inside the prefix a fence keyed on fugaro_p_ has
# to stay out of (F6).
PREFIX_U=fugaro_u_
PREFIX_P=fugaro_p_
STAMP=${CHECK510_STAMP:-$(date -u +%Y%m%d%H%M%S)}
PROBE_U="${PREFIX_U}9900000000000001_chk510a-${STAMP}"
PROBE_U_PREFIX="${PREFIX_U}9900000000000001_"
PROBE_P="${PREFIX_P}chk510shared-${STAMP}"
PROBE_N="fugaro-chk510-${STAMP}-probe"
PROBE_U2="${PREFIX_U}9900000000000001_chk510b-${STAMP}"
PROBE_N2="fugaro-chk510-${STAMP}-probe2"
SA_NAME="chk510-${STAMP}"
SA_EMAIL_WANTED="${SA_NAME}@${P}.iam.gserviceaccount.com"
# A plain filler, never a value anyone could mistake for a credential: what the
# rows measure is IAM, and the payload only has to be legal base64.
PROBE_PAYLOAD="chk510-probe-payload"
PAYLOAD_B64=$(printf '%s' "$PROBE_PAYLOAD" | base64 | tr -d '\n')
IAM_WAIT_SECONDS=${CHECK510_IAM_WAIT_SECONDS:-90}
IAM_POLL_SECONDS=${CHECK510_IAM_POLL_SECONDS:-10}
LENGTH_MAX_PROBE=${CHECK510_LENGTH_MAX_PROBE:-1024}
# The bisection's names: a legal ID is [A-Za-z0-9_-]+, and every probe carries
# the stamp. `guard_names_free` cannot list them — their lengths are computed from
# the limit the row is measuring, and from CHECK510_LENGTH_MAX_PROBE — so the
# stamp is what keeps a probe from colliding with anything the project already
# holds, and a name that IS already taken is answered with a 409, which the row
# reads as unmeasured rather than as the limit (see length_probe).
LENGTH_PREFIX="chk510l-${STAMP}-"
DRY_RUN=${CHECK510_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK510_KEEP_SCRATCH:-}
# The impersonation grant is written for the account gcloud itself is using,
# because that IS the identity the calls run as. A value that does not look like
# one is refused rather than guessed at.
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$ACCOUNT" ] || die "gcloud prints no account ('gcloud config get-value account'): the check has to name the person it grants impersonation to. Run 'gcloud auth login' first"
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_project
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  guard_names_free
  step_account
  step_make_probes
  step_bindings
  step_version_add
  step_delete
  # step_list before step_create: see step_list's own note — the unconditioned
  # create grant carries secrets.list too, so the list row has to be measured
  # while the conditioned grants are still the only ones written.
  step_list
  step_create
  step_id_length
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
