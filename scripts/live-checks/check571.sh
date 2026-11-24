#!/usr/bin/env bash
# Check 571 — every identity that reaches signing in the project (the census).
#
# docs/portal/implementation-plan.md's Checks table, row 571, and
# docs/portal/reconciliation.md's F18/D2194: the portal lives in ONE project, not
# a dedicated one. Round 1 of that review found what that costs — Firebase
# Hosting's IAM is project-wide, so the website's CI deployer can replace the
# portal's JavaScript and with it take a fresh operations sign-in and mint, and
# a project-level `datastore.user`, `run.services.update` on the minter or
# `iam.serviceAccounts.actAs` on the minter's account reach signing too. The
# owner re-asked on 2026-11-12 with those facts and KEPT the one project,
# accepting the risk. This check is the half that remains: it prints the list, so
# the risk is one the owner reads rather than one they were told about once.
#
# It reads, and every one of these is a NAME:
#   - the project's own IAM policy: every binding whose role carries a
#     permission that reaches signing (`useToSign`, `signBlob`, `actAs`,
#     `getAccessToken`, `run.services.update`, the store's entities, a Hosting
#     site's update, `resourcemanager.projects.setIamPolicy`, and so on). The
#     list is the census and is printed one line per binding, beside a row that
#     counts it. Nothing here is a failure: it is D2194's accepted exposure;
#   - the minter's invokers: exactly one, the API's account (D2194's own fence,
#     and the fence D450(a) rests on), and never `allUsers`;
#   - the signing ring's policy: the signer role on it holds exactly the minter's
#     account, and no other member on a role that can sign;
#   - the handover ring's policy: the sealer role on it holds exactly the API's
#     account, and no other member: a second identity there opens every sealed
#     reveal in the store (D2182);
#   - the two accounts' own policies: empty. `actAs` on the minter's account is
#     impersonating the one identity that signs, and `actAs` on the API's is
#     wearing the identity the minter trusts.
#
# Usage:      bash scripts/live-checks/check571.sh --project <project>
#             [--region <region>] [--environment staging|production]
#             [--minter-service s] [--minter-account a] [--api-service s]
#             [--api-account a] [--ring r] [--signer-role r]
#             [--handover-ring r] [--handover-role r] [--production] [--dry-run]
#             --project is the GCP project the portal is deployed in (D2194's one
#             project: `fugaro-prod`, or the staging one). --environment picks
#             which key id this deployment signed for — it names the ring, whose
#             policy is read — and defaults to production for fugaro-prod and to
#             staging otherwise, because the register's rule is that this check's
#             siblings run on staging first. Everything else defaults to
#             ee/keyservice/terraform/services/variables.tf's own names.
# Needs:      bash, mktemp, python3 (the reader that parses the documents),
#             gcloud, authenticated to an account that can read IAM policies in
#             the project. Nothing is installed and nothing is fetched.
# Where:      any project you operate, staging or production — it writes nothing.
#             `fugaro-prod` is refused unless `--production` is given: these rows
#             are about the deployment that mints for real licences.
# Creates:    NOTHING. Every call is a get-iam-policy, a describe or a list; the
#             Restore row says so.
# Output:     the census, one line per binding that reaches signing, above the
#             block, then one FACT row per reading. Exit 0 when every reading
#             held, 1 when one did not (a second invoker, a second member where
#             the plan states one), 2 when this check could not measure.
#
# A NOTE ON WHAT IS NEVER PRINTED: a policy read here is a list of NAMES —
# addresses, service accounts and roles. No permission is asked for, no secret is
# read, no key material is touched and no token is minted: this check is the
# list, and a list of names is what the owner pastes back.
#
# Knobs: CHECK571_PROJECT, CHECK571_REGION, CHECK571_ENVIRONMENT,
#        CHECK571_MINTER_SERVICE, CHECK571_MINTER_ACCOUNT, CHECK571_API_SERVICE,
#        CHECK571_API_ACCOUNT, CHECK571_RING, CHECK571_SIGNER_ROLE,
#        CHECK571_HANDOVER_RING, CHECK571_HANDOVER_ROLE, CHECK571_PRODUCTION,
#        CHECK571_DRY_RUN, CHECK571_KEEP_SCRATCH.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the block is what you
# paste back, and a trace would be pasted with it.
XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and one pattern
# over the three concatenated would match that 'x' and refuse every run.
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block is written to
# the saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. A stop right after an
# `unmeasured` row ends the run as one that could not measure (exit 2).
stop() {
  local was=$LAST_ROW
  fact aborted FAIL "$1"
  owner "stopped: $1"
  if [ "$was" = unmeasured ]; then exit 2; fi
  exit 1
}

FACTS_FILE=""
LAST_ROW=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
  case "$2" in
  FAIL) LAST_ROW=fail ;;
  *) LAST_ROW="" ;;
  esac
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the deployment. Exit 2.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  LAST_ROW=unmeasured
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
  for t in bash mktemp python3 gcloud; do
    command -v "$t" >/dev/null 2>&1 || die "Check 571 needs '$t' and it is not on PATH"
  done
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }
slug() { printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '_'; }

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""

PROJECT=""
REGION=""
ENVIRONMENT=""
MINTER_SERVICE=""
MINTER_ACCOUNT=""
API_SERVICE=""
API_ACCOUNT=""
RING=""
SIGNER_ROLE=""
HANDOVER_RING=""
HANDOVER_ROLE=""
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check571.XXXXXX") || die "Check 571 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK571_KEEP_SCRATCH is set)"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
}

scratch_down() {
  [ -n "$SCRATCH" ] || return 0
  if [ "$KEEP_SCRATCH" = 1 ]; then
    SCRATCH_KEPT=yes
    return 0
  fi
  rm -rf "$SCRATCH"
  SCRATCH_REMOVED=yes
}

finish() {
  local status=$?
  CLEANUP_RUNNING=yes
  trap - ERR
  local facts=""
  if [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ]; then facts=$(cat "$FACTS_FILE"); fi
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK571_KEEP_SCRATCH is set)"
    else
      facts="$facts"$'\n'"scratch|PASS|the scratch directory was removed from the exit trap"
    fi
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    if [ "$status" -eq 0 ]; then status=2; fi
  fi
  # A run that reached the end with a FAILed row is a run with a finding: exit 1
  # says so without anyone having to read the block for it. An unmeasured row is
  # the exception — a FAIL in the block and a 2 above.
  if [ "$status" -eq 0 ]; then
    local failed=no key verdict
    while IFS='|' read -r key verdict _; do
      [ "$verdict" = FAIL ] || continue
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      failed=yes
    done <<<"$facts"
    if [ "$failed" = yes ]; then status=1; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(gcloud xtrace agent-session project belong census invokers signer handover actas restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 571 RESULT\n' >&3
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
  printf 'CHECK 571 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell is tracing (bash -x or set -x): a traced run prints every command AND every policy document this check reads, and a paste-back of it would be a paste-back of the deployment's whole access list. Run it again without -x"
    stop "this shell is tracing, so the run's own output is not the block alone"
  fi
  fact xtrace PASS "this shell is not tracing, so this run's output is this check's own words"
}

# The markers scripts/agent-session.sh reads: the same five, because a check whose
# census the owner reads is not one an agent should run and report.
guard_agent_session() {
  local marker
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!marker:-}" ]; then
      fact agent-session FAIL "$marker is set, so this is a coding agent's session. Check 571 is an owner's reading of their own project's IAM policy: run it yourself"
      stop "this is a coding agent's session ($marker is set), and Check 571 is the owner's own reading of the project's IAM policy"
    fi
  done
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_project() {
  case "$PROJECT" in
  "" ) die "Check 571 needs --project: the GCP project the portal is deployed in. Usage: $USAGE" ;;
  *belong*)
    fact belong FAIL "the project name '$PROJECT' names belong, and nothing in this check is ever run against belong"
    stop "the project '$PROJECT' names belong, which this check refuses by name"
    ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "'$PROJECT' is fugaro-prod, which is refused unless --production is given: the register's rule is that Checks 570 to 575 run on fugaro-staging first, and this check reads the production project's whole access list. If this is the run you mean, pass --production"
  fi
  if [ "$PRODUCTION" = 1 ]; then
    fact project PASS "$PROJECT — production, named with --production: read this census as a production run by choice"
  else
    fact project PASS "$PROJECT is the GCP project this check was given, and it is not fugaro-prod, which is refused without --production"
  fi
}

# The roles a binding can reach signing through, by PERMISSION rather than by
# name: a predefined role, a custom role and a role a future release adds are
# all read the same way, and a role that carries `useToSign` under a name nobody
# expected is exactly what this census exists to catch.
#   reach_perms: the permissions that are signing, the store, or a redeploy.
REACH_EXACT="$(printf '%s\n' \
  iam.serviceAccounts.signBlob \
  iam.serviceAccounts.signJwt \
  iam.serviceAccounts.implicitDelegation \
  iam.serviceAccounts.getAccessToken \
  iam.serviceAccounts.actAs \
  iam.serviceAccounts.setIamPolicy \
  iam.serviceAccountKeys.create \
  cloudkms.cryptoKeyVersions.useToSign \
  cloudkms.cryptoKeyVersions.useToDecrypt \
  cloudkms.cryptoKeys.setIamPolicy \
  resourcemanager.projects.setIamPolicy \
  run.services.update \
  run.services.create \
  run.services.delete \
  run.services.setIamPolicy \
  firebasehosting.sites.update \
  firebasehosting.sites.create \
  firebasehosting.sites.delete \
  firebasehosting.releases.create)"
# The store's own entities: whoever can write the portal's documents can write
# the entitlement a mint is checked against (D2194's own sentence).
REACH_PREFIX="datastore.entities."

# role_read ROLE: the role's permissions, one per line, into the scratch
# directory. A role that cannot be read is NOT treated as harmless — the census
# would then be a list with a hole in it, and the hole is exactly where an
# unexpected grant would sit.
role_read() { # role_read ROLE — 0 when the permissions were read
  local role=$1 file="$SCRATCH/role-$(slug "$role").perms"
  if [ -f "$file" ]; then return 0; fi
  local args=(iam roles describe "$role" --format=json)
  case "$role" in
  projects/*/roles/* | organizations/*/roles/*) args+=(--project "$PROJECT") ;;
  esac
  if ! gcloud "${args[@]}" >"$SCRATCH/role-$(slug "$role").json" 2>"$SCRATCH/role-$(slug "$role").err"; then
    return 1
  fi
  python3 - "$SCRATCH/role-$(slug "$role").json" "$file" <<'PY' || return 1
import json, sys
doc = json.load(open(sys.argv[1]))
perms = doc.get("includedPermissions")
if not isinstance(perms, list):
    sys.exit(1)
with open(sys.argv[2], "w") as fh:
    for p in perms:
        fh.write(p + "\n")
PY
}

# role_reaches ROLE: prints up to three of the role's permissions that reach
# signing, one per line, and prints nothing when the role reaches none. The list
# is read as PERMISSIONS, so a role named something nobody expected is caught
# exactly when a role named `useToSign` would be.
role_reaches() { # role_reaches ROLE
  local role=$1 file
  role_read "$role" || return 1
  file="$SCRATCH/role-$(slug "$role").perms"
  python3 - "$file" "$REACH_EXACT" "$REACH_PREFIX" <<'PYEOF'
import sys
perms = [p for p in open(sys.argv[1]).read().split() if p]
exact = set(p for p in sys.argv[2].split("\n") if p)
prefix = sys.argv[3]
hits = [p for p in perms if p in exact or p.startswith(prefix)]
print("\n".join(hits[:3]))
PYEOF
}

# --------------------------------------------------------------- gcloud reads
# gcloud_read KEY LABEL CMD...: one read into $SCRATCH/KEY.json. Its failure is a
# row this check could not measure, and the caller stops.
gcloud_read() { # gcloud_read KEY LABEL CMD... — 0 when the document was read
  local key=$1 label=$2
  shift 2
  if ! gcloud "$@" >"$SCRATCH/$key.json" 2>"$SCRATCH/$key.err"; then
    unmeasured "$key" "$label did not answer: $(last_lines "$SCRATCH/$key.err"). Every row here is a reading of the project's own IAM policy, so this check stops rather than report a census it could not make"
    return 1
  fi
  if [ ! -s "$SCRATCH/$key.json" ]; then
    unmeasured "$key" "$label answered nothing, so the $label could not be read"
    return 1
  fi
  return 0
}

# members_of FILE ROLE: the members a policy binds to one role, one per line.
members_of() { # members_of POLICY_JSON ROLE
  python3 - "$1" "$2" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for b in doc.get("bindings", []) or []:
    if b.get("role") == sys.argv[2]:
        for m in b.get("members", []) or []:
            print(m)
PY
}

# roles_of FILE: every distinct role a policy binds, one per line.
roles_of() { # roles_of POLICY_JSON
  python3 - "$1" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
seen = []
for b in doc.get("bindings", []) or []:
    r = b.get("role")
    if r and r not in seen:
        seen.append(r)
        print(r)
PY
}

# bindings_of FILE: `role<TAB>member` for every binding, one per line.
bindings_of() { # bindings_of POLICY_JSON
  python3 - "$1" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for b in doc.get("bindings", []) or []:
    for m in b.get("members", []) or []:
        print("%s\t%s" % (b.get("role"), m))
PY
}

# ------------------------------------------------------------------- the steps
step_gcloud() {
  local account version
  if ! gcloud --version --format=json >"$SCRATCH/gcloud-version.json" 2>"$SCRATCH/gcloud-version.err"; then
    unmeasured gcloud "gcloud did not answer --version: $(last_lines "$SCRATCH/gcloud-version.err"). This check reads the project's IAM policy through gcloud and nothing else"
    stop "gcloud is not answering, so no policy could be read"
  fi
  version=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d.get("Google Cloud SDK","unknown"))' "$SCRATCH/gcloud-version.json" 2>/dev/null || echo unknown)
  account=$(gcloud config get-value account 2>"$SCRATCH/account.err" | tr -d '\n' || true)
  case "$account" in
  "" | "(unset)")
    unmeasured gcloud "gcloud has no authenticated account (config get-value account answered '${account:-nothing}'), so the policy reads would be made as nobody and every row would be a refusal this check cannot tell from a finding. Run 'gcloud auth login' first"
    stop "gcloud is not authenticated, so the project's IAM policy cannot be read"
    ;;
  esac
  if ! gcloud projects describe "$PROJECT" --format=json >"$SCRATCH/project.json" 2>"$SCRATCH/project.err"; then
    unmeasured gcloud "gcloud projects describe $PROJECT did not answer: $(last_lines "$SCRATCH/project.err"). This check reads the project's IAM policy through gcloud with the account above, and a project it cannot describe is one whose policy it must not report rows about"
    stop "the project $PROJECT could not be described, so this check cannot say which project its census is about"
  fi
  local number state
  number=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("projectNumber",""))' "$SCRATCH/project.json" 2>/dev/null || true)
  state=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("lifecycleState",""))' "$SCRATCH/project.json" 2>/dev/null || true)
  fact gcloud PASS "gcloud ${version:-unknown} as ${account}, and project $PROJECT (number ${number:-unknown}, ${state:-unknown}) answers: the census below is this project's own IAM policy, read with that account"
}

# step_census: **D2194's accepted risk, made readable.** Every project-level
# binding whose role carries a permission that reaches signing is printed, one
# line per binding, above the block — and the row counts them. This is not a
# pass/fail row about the deployment: it is the list the owner asked to see.
step_census() {
  gcloud_read census "gcloud projects get-iam-policy $PROJECT" projects get-iam-policy "$PROJECT" --format=json || stop "the project's IAM policy could not be read, so this check has no census to print"
  bindings_of "$SCRATCH/census.json" >"$SCRATCH/bindings.txt"
  : >"$SCRATCH/reaching.txt"
  local role reason
  while IFS= read -r role; do
    [ -n "$role" ] || continue
    local perms
    if ! perms=$(role_reaches "$role"); then
      unmeasured census "the role '$role' is bound in $PROJECT's policy and its permissions could not be read, so the census has a hole in it at exactly the place an unexpected grant would sit: read it with 'gcloud iam roles describe $role' and run the check again"
      stop "the role '$role' could not be described, so the census is incomplete"
    fi
    [ -n "$perms" ] || continue
    reason=$(printf '%s' "$perms" | tr '\n' ',' | sed 's/,$//')
    while IFS=$'\t' read -r r m; do
      [ "$r" = "$role" ] || continue
      printf '%s\t%s\t%s\n' "$role" "$m" "$reason" >>"$SCRATCH/reaching.txt"
    done <"$SCRATCH/bindings.txt"
  done < <(roles_of "$SCRATCH/census.json")
  local count identities
  count=$(grep -c . "$SCRATCH/reaching.txt" || true)
  identities=$(cut -f2 "$SCRATCH/reaching.txt" | sort -u | grep -c . || true)
  local line
  while IFS=$'\t' read -r r m why; do
    [ -n "$r" ] || continue
    say "  reaches signing: $r -> $m  ($why)"
  done <"$SCRATCH/reaching.txt"
  if [ "${count:-0}" -eq 0 ]; then
    fact census INFO "no project-level binding in $PROJECT carries a permission that reaches signing: no member of this policy can sign, decrypt a handover, redeploy the minter or write the store. (Every member of a project's policy still holds whatever its roles carry outside that list, which is the part this census does not read)"
    return 0
  fi
  fact census INFO "$count project-level binding(s) in $PROJECT reach signing, held by $identities identity/identities: $(awk -F'\t' '{printf "%s>%s (%s);", $1, $2, $3}' "$SCRATCH/reaching.txt" | cut -c1-700). This is D2194's accepted risk — the owner kept the one project on 2026-11-12 knowing that Hosting deployers, the store's readers and writers, and anyone who can redeploy the minter reach signing — and this list is what makes it a risk the owner reads rather than one they were told about once. The list is printed above this block once per binding, with the permission that reaches signing; reads that never reach signing (a project viewer, a billing viewer, a log reader) are not in it"
}

# step_invokers: the two Cloud Run policies, and only one of them is a fence. The
# minter may be called by the API's account ALONE (D2194's own sentence, and the
# half D450(a)'s ID-token check rests on); the API is the internet-facing half, so
# it is public by design and its own checks are what stands in front of it — that
# half is read and named, and an extra invoker there is not a finding.
step_invokers() {
  local service=$MINTER_SERVICE
  gcloud_read invokers "gcloud run services get-iam-policy $service --region $REGION" \
    run services get-iam-policy "$service" --region "$REGION" --format=json \
    || stop "the minter's own Cloud Run policy could not be read, so this check cannot say who may call the signer"
  local api_member="serviceAccount:${API_ACCOUNT}@${PROJECT}.iam.gserviceaccount.com"
  local have
  have=$(members_of "$SCRATCH/invokers.json" roles/run.invoker | sort | tr '\n' ' ' | sed 's/ $//')
  local others
  others=$(bindings_of "$SCRATCH/invokers.json" | grep -v "^roles/run.invoker	$api_member\$" | tr '	' '>' || true)
  if ! gcloud run services get-iam-policy "$API_SERVICE" --region "$REGION" --format=json \
    >"$SCRATCH/api-invokers.json" 2>"$SCRATCH/api-invokers.err"; then
    unmeasured invokers "gcloud run services get-iam-policy $API_SERVICE --region $REGION did not answer: $(last_lines "$SCRATCH/api-invokers.err"), so the internet-facing half of the pair was not read"
    stop "the API's own Cloud Run policy could not be read"
  fi
  local api_have
  api_have=$(members_of "$SCRATCH/api-invokers.json" roles/run.invoker | sort | tr '\n' ' ' | sed 's/ $//')
  if [ "$have" = "$api_member" ] && [ -z "$others" ]; then
    fact invokers PASS "$service (the minter) can be called by $api_member alone, and holds no other binding: D2194's own fence, and the half D450(a)'s ID-token check rests on. $API_SERVICE (the API) is the internet-facing half and is invokable by '${api_have:-nobody}', which is the design rather than a finding: its own sign-in, role and rate checks are what stand in front of it (P1.3, F33)"
    return 0
  fi
  fact invokers FAIL "$service (the minter) is not callable by the API's account alone. Its policy reads: $(printf '%s' "$others" | tr '\n' ';' | cut -c1-400)${others:+ }and roles/run.invoker is held by '${have:-nobody}'. Anyone listed beside $api_member can ask the minter to sign, and 'allUsers' or 'allAuthenticatedUsers' there is the internet at the signer"
  stop "the minter's invokers are not the API's account alone, which is the fence the whole design rests on"
}

# ring_policy KEY RING LABEL: the ring's policy, read into the scratch
# directory. A key ring that does not exist is a finding about the deployment —
# the ring is keyring/'s, applied before this root — not a check error.
ring_read() { # ring_read KEY RING — 0 when the ring's policy was read
  local key=$1 ring=$2
  if ! gcloud kms keyrings get-iam-policy "$ring" --location "$REGION" --format=json \
    >"$SCRATCH/$key.json" 2>"$SCRATCH/$key.err"; then
    unmeasured "$key" "gcloud kms keyrings get-iam-policy $ring --location $REGION did not answer: $(last_lines "$SCRATCH/$key.err"). Is this the ring (--ring, --environment) and the region (--region) this deployment was applied with?"
    return 1
  fi
  return 0
}

# signing_members FILE: `role<TAB>member` for every binding on a policy whose
# role reaches signing — the ring's own half of the census.
signing_members() { # signing_members POLICY_JSON
  local file=$1 role
  while IFS= read -r role; do
    [ -n "$role" ] || continue
    local perms
    if ! perms=$(role_reaches "$role"); then
      return 1
    fi
    [ -n "$perms" ] || continue
    while IFS=$'\t' read -r r m; do
      [ "$r" = "$role" ] || continue
      printf '%s\t%s\n' "$r" "$m"
    done < <(bindings_of "$file")
  done < <(roles_of "$file")
}

step_signer() {
  ring_read signer "$RING" || stop "the signing ring $RING could not be read, so this check cannot say who may sign with it"
  local minter_member="serviceAccount:${MINTER_ACCOUNT}@${PROJECT}.iam.gserviceaccount.com"
  local signer_role="projects/${PROJECT}/roles/${SIGNER_ROLE}"
  local have others
  have=$(members_of "$SCRATCH/signer.json" "$signer_role" | sort | tr '\n' ' ' | sed 's/ $//')
  if ! others=$(signing_members "$SCRATCH/signer.json"); then
    unmeasured signer "a role bound on the signing ring $RING could not be described, so this check cannot say whether it can sign"
    stop "a role on the signing ring could not be read, so the signer census is incomplete"
  fi
  others=$(printf '%s' "$others" | grep -v "^${signer_role}	$minter_member\$" | tr '	' '>' || true)
  if [ "$have" = "$minter_member" ] && [ -z "$others" ]; then
    fact signer PASS "$RING's policy holds the signer role $signer_role for $minter_member alone, and no other role that can sign: one identity in this project can produce a licence key, and it is the minter's (D2194, §3.3)"
    return 0
  fi
  fact signer FAIL "$RING is not signable by the minter alone. The signer role $signer_role is held by '${have:-nobody}' and the plan's is $minter_member${others:+, and these other bindings on this ring reach signing: $(printf '%s' "$others" | tr '\n' ';' | cut -c1-400)}. A second member on that role is a second identity that can mint a licence for any workspace, and a role bound here without the signer grant is one nothing can sign through"
  stop "the signing ring's policy is not the signer role for the minter alone"
}

step_handover() {
  ring_read handover "$HANDOVER_RING" || stop "the handover ring $HANDOVER_RING could not be read, so this check cannot say who may open a sealed reveal"
  local api_member="serviceAccount:${API_ACCOUNT}@${PROJECT}.iam.gserviceaccount.com"
  local sealer_role="projects/${PROJECT}/roles/${HANDOVER_ROLE}"
  local have others
  have=$(members_of "$SCRATCH/handover.json" "$sealer_role" | sort | tr '\n' ' ' | sed 's/ $//')
  if ! others=$(signing_members "$SCRATCH/handover.json"); then
    unmeasured handover "a role bound on the handover ring $HANDOVER_RING could not be described, so this check cannot say whether it opens a sealed reveal"
    stop "a role on the handover ring could not be read, so the handover census is incomplete"
  fi
  others=$(printf '%s' "$others" | grep -v "^${sealer_role}	$api_member\$" | tr '	' '>' || true)
  if [ "$have" = "$api_member" ] && [ -z "$others" ]; then
    fact handover PASS "$HANDOVER_RING is sealed and opened by $api_member alone (the sealer role $sealer_role), and no other role there can decrypt: a second member would be a second identity that can read every stored reveal (D2182)"
    return 0
  fi
  fact handover FAIL "$HANDOVER_RING is not the API's alone. The sealer role $sealer_role is held by '${have:-nobody}' and the plan's is $api_member${others:+, and these other bindings on this ring reach a sealed document: $(printf '%s' "$others" | tr '\n' ';' | cut -c1-400)}. Whoever is beside the API's account can open every stored reveal (D2182)"
  stop "the handover ring's policy is not the API's account alone"
}

# step_actas: the two accounts' OWN policies. actAs on the minter's account is
# wearing the one identity that signs; actAs on the API's is wearing the identity
# the minter trusts. Both are empty in the plan, and a member here is a finding
# whatever their project-level roles say.
step_actas() {
  local minter_email="${MINTER_ACCOUNT}@${PROJECT}.iam.gserviceaccount.com"
  local api_email="${API_ACCOUNT}@${PROJECT}.iam.gserviceaccount.com"
  local who email
  for who in minter api; do
    case "$who" in
    minter) email=$minter_email ;;
    api) email=$api_email ;;
    esac
    if ! gcloud iam service-accounts get-iam-policy "$email" --format=json \
      >"$SCRATCH/actas-$who.json" 2>"$SCRATCH/actas-$who.err"; then
      unmeasured actas "gcloud iam service-accounts get-iam-policy $email did not answer: $(last_lines "$SCRATCH/actas-$who.err"). 'actAs' on the minter's account is impersonating the one identity that signs, so this row is read and not guessed"
      stop "the $who account's own policy could not be read, so this check cannot say who may impersonate it"
    fi
  done
  local minter_bindings api_bindings
  minter_bindings=$(bindings_of "$SCRATCH/actas-minter.json" | tr '\t' '>' | tr '\n' ';' | cut -c1-300)
  api_bindings=$(bindings_of "$SCRATCH/actas-api.json" | tr '\t' '>' | tr '\n' ';' | cut -c1-300)
  if [ -n "$minter_bindings" ] || [ -n "$api_bindings" ]; then
    fact actas FAIL "an account can be impersonated: the minter's account ($minter_email) reads '${minter_bindings:-nothing}' and the API's ($api_email) reads '${api_bindings:-nothing}'. 'roles/iam.serviceAccountUser' or a token-creator binding on the minter's account is a second identity that signs with the minter's own key, and on the API's account it is one the minter would accept the call from"
    stop "an identity can impersonate the minter's or the API's account, which is the design's own trust boundary"
  fi
  fact actas PASS "$minter_email and $api_email hold no member on their own policies: nobody may act as the signer, and nobody may act as the identity the signer trusts. (A project-level actAs is not read here — it is in the census row above, where D2194's risk belongs)"
}

step_restore() {
  fact restore PASS "nothing of this check's own is left behind: it made no write — every call was a get-iam-policy, a describe or a list — so there is nothing to undo. The census above is a reading of $PROJECT's IAM policy as it stands"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 571 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read, never a write] gcloud --version, gcloud config get-value account"
  say "  [read] gcloud projects describe $PROJECT — the project this census is about"
  say "  [read] gcloud projects get-iam-policy $PROJECT — the census: every binding whose role carries a permission that reaches signing"
  say "  [read] gcloud iam roles describe <role> for each role that policy binds — the permissions, so 'reaches signing' is a permission and not a name"
  say "  [read] gcloud run services get-iam-policy $MINTER_SERVICE --region $REGION — the minter's invokers, which must be the API's account alone"
  say "  [read] gcloud run services get-iam-policy $API_SERVICE --region $REGION — the API's own invokers, which are the design's (it is the internet-facing half)"
  say "  [read] gcloud kms keyrings get-iam-policy $RING --location $REGION — the signing ring: ${SIGNER_ROLE} for the minter alone"
  say "  [read] gcloud kms keyrings get-iam-policy $HANDOVER_RING --location $REGION — the handover ring: ${HANDOVER_ROLE} for the API alone"
  say "  [read] gcloud iam service-accounts get-iam-policy ${MINTER_ACCOUNT}@$PROJECT… and ${API_ACCOUNT}@$PROJECT… — both must be empty"
  say "  nothing is written: no policy, no member, no key, no token. $PROJECT is the project this check was given, and it is not belong"
  fact dry-run PASS "the plan was printed and nothing was run: no gcloud call, no row measured"
}

main() {
  need_tools
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_project
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_gcloud
  step_census
  step_invokers
  step_signer
  step_handover
  step_actas
  step_restore
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
PRODUCTION_CLI=""
USAGE="check571.sh --project <project> [--region <region>] [--environment staging|production] [--minter-service s] [--minter-account a] [--api-service s] [--api-account a] [--ring r] [--signer-role r] [--handover-ring r] [--handover-role r] [--production] [--dry-run]"
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --project)
    [ $# -ge 2 ] || die "--project needs a GCP project id. usage: $USAGE"
    PROJECT=$2
    shift 2
    ;;
  --region)
    [ $# -ge 2 ] || die "--region needs a region. usage: $USAGE"
    REGION=$2
    shift 2
    ;;
  --environment)
    [ $# -ge 2 ] || die "--environment needs staging or production. usage: $USAGE"
    ENVIRONMENT=$2
    shift 2
    ;;
  --minter-service | --minter-account | --api-service | --api-account | --ring | --signer-role | --handover-ring | --handover-role)
    [ $# -ge 2 ] || die "$1 needs a name. usage: $USAGE"
    case "$1" in
    --minter-service) MINTER_SERVICE_CLI=$2 ;;
    --minter-account) MINTER_ACCOUNT_CLI=$2 ;;
    --api-service) API_SERVICE_CLI=$2 ;;
    --api-account) API_ACCOUNT_CLI=$2 ;;
    --ring) RING_CLI=$2 ;;
    --signer-role) SIGNER_ROLE_CLI=$2 ;;
    --handover-ring) HANDOVER_RING_CLI=$2 ;;
    --handover-role) HANDOVER_ROLE_CLI=$2 ;;
    esac
    shift 2
    ;;
  --production)
    PRODUCTION_CLI=1
    shift
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    die "unknown argument '$1'. usage: $USAGE"
    ;;
  esac
done

PROJECT=${CHECK571_PROJECT:-${PROJECT:-}}
REGION=${CHECK571_REGION:-${REGION:-us-east5}}
ENVIRONMENT=${CHECK571_ENVIRONMENT:-${ENVIRONMENT:-}}
MINTER_SERVICE=${CHECK571_MINTER_SERVICE:-${MINTER_SERVICE_CLI:-fugaro-key-minter}}
MINTER_ACCOUNT=${CHECK571_MINTER_ACCOUNT:-${MINTER_ACCOUNT_CLI:-fugaro-key-minter}}
API_SERVICE=${CHECK571_API_SERVICE:-${API_SERVICE_CLI:-fugaro-portal-api}}
API_ACCOUNT=${CHECK571_API_ACCOUNT:-${API_ACCOUNT_CLI:-fugaro-portal-api}}
SIGNER_ROLE=${CHECK571_SIGNER_ROLE:-${SIGNER_ROLE_CLI:-fugaroKeySigner}}
HANDOVER_ROLE=${CHECK571_HANDOVER_ROLE:-${HANDOVER_ROLE_CLI:-fugaroHandoverSealer}}
HANDOVER_RING=${CHECK571_HANDOVER_RING:-${HANDOVER_RING_CLI:-fugaro-portal-handover}}
PRODUCTION=${CHECK571_PRODUCTION:-${PRODUCTION_CLI:-}}
DRY_RUN=${CHECK571_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK571_KEEP_SCRATCH:-}
# The register's rule is that Checks 570 to 575 run on staging first, and the two
# environments have one key id each inside one ring of their own (D459): the ring
# this check reads the signer grant from is named by the environment the project
# signs for, which is production for fugaro-prod and staging for every other
# project this check is pointed at.
if [ -z "$ENVIRONMENT" ]; then
  if [ "$PROJECT" = fugaro-prod ]; then ENVIRONMENT=production; else ENVIRONMENT=staging; fi
fi
case "$ENVIRONMENT" in
staging | production) ;;
*) die "--environment '$ENVIRONMENT' is neither staging nor production. usage: $USAGE" ;;
esac
RING=${CHECK571_RING:-${RING_CLI:-fugaro-licence-$ENVIRONMENT}}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
