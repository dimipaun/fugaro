#!/usr/bin/env bash
# Tests for scripts/live-checks/check96.sh, against a fake gcloud, a fake
# fugaro and a fake curl on PATH: nothing here reaches the network, there is no
# cloud project, no GitHub installation and no Firebase project, and no live
# step of the check is run by an agent.
#
# The three fakes are the check's whole outside world. The fake gcloud answers
# the reads the check makes (the service, the job, the two secret containers
# and their policies, the service's own log, the two identities it mints) from
# files this test writes; the fake fugaro answers `doctor --json` and `budget
# show --all --json`; and the fake curl answers the exchanges and the mint from
# its own routing rules, so a case says what the service would answer by
# choosing which repository a run identity is for. Every call of every fake is
# recorded, argv and all: that log is what the "no token in argv" test reads.
#
# Usage: bash scripts/live-checks/check96_test.sh
#        CHECK_TEST_ONLY=test_name bash ...   one test
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check96.sh
# The PHYSICAL path, so a path this test compares and a path a child reports
# are spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK96_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 800)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|installation|service|job|job-env|app-key|mint|pairing|outsider|survivors|restore|secret-scan):' | sed 's/^/ ... last run: /')"
}

# The installation the whole suite runs against: the sandbox one, and the
# project the check is pinned to.
PROJECT=fugaro-dev
PROJECT_NAME=fugaro
REGION=europe-west1
FUGARO_PROJECT=fugaro-sbx
SERVICE=credsvc
SERVICE_URL=https://credsvc-fixture.a.run.app
SIGNER=fugaro-token-signer@fugaro-sbx.iam.gserviceaccount.com
JOB=fugaro-acme-sandbox
JOBSA=run-acme-sandbox@fugaro-dev.iam.gserviceaccount.com
SVCSA=credsvc@fugaro-dev.iam.gserviceaccount.com
APPID=424242
SANDBOX_REPO=acme/sandbox
SANDBOX_SLUG=acme_sandbox
OTHER_REPO=acme/legacy
OTHER_SLUG=acme_legacy
BUNDLE=fugaro-credentials
APPKEY=github-app-key
# The token the fake service would mint. It is a fixture and not a credential:
# the check must never print it, and the block's own scan exists to make that
# true whatever a failure's detail says.
MINTED=ghs_fixture_check96_token
# The installation's restricted web API key, which the check reads out of the
# config and never prints. Shaped like Google's, and nobody's.
FIREKEY=AIzaSyFIXTURE-not-a-credential-000000

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation service job job-env app-key bundle front-end mint reach audit pairing outsider survivors xtrace restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check96.sh's test: every answer comes from $FAKE96_DIR and
# nothing is reached. The whole argv of every call is recorded.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE96_DIR/calls"
case "${1:-}" in
run)
  case "${2:-}" in
  services)
    case "${3:-}" in
    list) cat "$FAKE96_DIR/services.json" ;;
    describe) cat "$FAKE96_DIR/service.json" ;;
    *) printf 'fake gcloud: unknown services verb %s\n' "${3:-}" >&2; exit 64 ;;
    esac
    ;;
  jobs)
    case "${3:-}" in
    list) cat "$FAKE96_DIR/jobs.json" ;;
    describe) cat "$FAKE96_DIR/job.json" ;;
    *) printf 'fake gcloud: unknown jobs verb %s\n' "${3:-}" >&2; exit 64 ;;
    esac
    ;;
  *) printf 'fake gcloud: unknown run verb %s\n' "${2:-}" >&2; exit 64 ;;
  esac
  ;;
secrets)
  case "${2:-}" in
  list) cat "$FAKE96_DIR/secrets.json" ;;
  get-iam-policy)
    case "${3:-}" in
    "$FAKE96_APPKEY") cat "$FAKE96_DIR/policy-app.json" ;;
    "$FAKE96_BUNDLE") cat "$FAKE96_DIR/policy-bundle.json" ;;
    *) printf 'fake gcloud: no policy for %s\n' "${3:-}" >&2; exit 1 ;;
    esac
    ;;
  *) printf 'fake gcloud: unknown secrets verb %s\n' "${2:-}" >&2; exit 64 ;;
  esac
  ;;
logging)
  # The filter is the last argument. A run's own lines come from a template with
  # the run substituted, because the run id is made at run time; the count the
  # front-end row takes before and after its call comes from a file, and a case
  # that wants the count to move writes the second one.
  filter="${!#}"
  case "$filter" in
  *'jsonPayload.run="'*)
    run=$(printf '%s' "$filter" | sed -n 's/.*jsonPayload.run="\([^"]*\)".*/\1/p')
    case "$run" in
    *-c96a) tmpl=${FAKE96_AUDIT_OK:-$FAKE96_DIR/audit-ok.tmpl} ;;
    *) tmpl=${FAKE96_AUDIT_REFUSAL:-$FAKE96_DIR/audit-refusal.tmpl} ;;
    esac
    sed "s/@RUN@/$run/g" "$tmpl"
    ;;
  *)
    if [ -n "${FAKE96_COUNT_NOT_JSON:-}" ]; then
      # `gcloud logging read` exiting 0 with something this check cannot parse —
      # a notice on stdout, or a document that is not a list of entries. It is
      # the case a count is taken from a log that was never read.
      printf '%s\n' "$FAKE96_COUNT_NOT_JSON"
      exit 0
    fi
    n=0
    [ -f "$FAKE96_DIR/count-called" ] && n=$(cat "$FAKE96_DIR/count-called")
    n=$((n + 1))
    printf '%s\n' "$n" >"$FAKE96_DIR/count-called"
    if [ -s "$FAKE96_DIR/audit-purpose-after" ] && [ "$n" -ge 2 ]; then
      cat "$FAKE96_DIR/audit-purpose-after"
    else
      cat "$FAKE96_DIR/audit-purpose.json"
    fi
    ;;
  esac
  ;;
auth)
  case "${2:-}" in
  print-identity-token)
    # The account this token is for is in the argv, so a case can tell the job's
    # own account from one that is no job account.
    sa=""
    for a in "$@"; do case "$a" in --impersonate-service-account=*) sa=${a#*=} ;; esac; done
    printf 'IDTOKEN-CALLER-%s\n' "${sa##*@}"
    ;;
  print-access-token)
    printf 'ACCESS-TOKEN-FIXTURE\n'
    ;;
  *) printf 'fake gcloud: unknown auth verb %s\n' "${2:-}" >&2; exit 64 ;;
  esac
  ;;
iam)
  case "${2:-}" in
  service-accounts)
    # sign-jwt --iam-account=... --project=... IN OUT. The signed token carries
    # the uid of the claims it was given, so the fake service can tell which
    # repository's run identity it is being shown.
    out="${!#}"
    in="${@: -2:1}"
    uid=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["uid"])' "$in")
    printf 'FAKEJWT.%s.signature\n' "$uid" >"$out"
    ;;
  *) printf 'fake gcloud: unknown iam verb %s\n' "${2:-}" >&2; exit 64 ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check96.sh's test: doctor's answer and the stored budget
# come from $FAKE96_DIR.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE96_DIR/calls"
case "${1:-}" in
doctor) cat "$FAKE96_DIR/doctor.json" ;;
budget) cat "$FAKE96_DIR/budget.json" ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A fake curl for check96.sh's test. It reads the check's own `--config -` from
# stdin, which is where every credential the check holds travels, and routes on
# the URL. The whole argv is recorded and the CONFIG IS NOT: a check that put a
# token on its command line would show up in that log, and one that keeps it in
# the config shows up nowhere.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE96_DIR/calls"
outfile=""
writefmt=""
cfg=""
while [ $# -gt 0 ]; do
  case "$1" in
  --config)
    cfg=$(cat)
    shift
    ;;
  -o)
    outfile=$2
    shift 2
    ;;
  -w)
    writefmt=$2
    shift 2
    ;;
  -X)
    shift 2
    ;;
  *) shift ;;
  esac
done
url=$(printf '%s\n' "$cfg" | sed -n 's/^url *= *"\(.*\)"$/\1/p')
auth=$(printf '%s\n' "$cfg" | sed -n 's/^header *= *"Authorization: Bearer \(.*\)"$/\1/p')
rid=$(printf '%s\n' "$cfg" | sed -n 's/^header *= *"X-Fugaro-Run-Identity: \(.*\)"$/\1/p')
data=$(printf '%s\n' "$cfg" | sed -n 's/^data *= *"\(.*\)"$/\1/p')

body=""
code=200
case "$url" in
*accounts:signInWithCustomToken*)
  # The custom token the check signed carries the uid; the ID token answers with
  # it, so every mint call below can be told which repository's identity it is.
  uid=$(printf '%s' "$data" | sed -n 's/.*FAKEJWT\.\([^.]*\)\..*/\1/p')
  if [ -z "$uid" ]; then
    code=400
    body='{"error":{"message":"INVALID_CUSTOM_TOKEN"}}'
  else
    body=$(printf '{"idToken":"FAKEIDTOKEN.%s.signature","expiresIn":"3600"}' "$uid")
  fi
  ;;
"$FAKE96_MINT_URL"*)
  case "$auth" in
  "")
    # No identity at all: Cloud Run's own front end, before the container. The
    # body is Google's -- the platform's own HTML, so the check prints none of
    # it and parses none of it. FAKE96_NOIDENT_BODY is the case where the call
    # reached the container instead, which is a service deployed with IAM off:
    # the body is then the service's typed refusal, and that is what the
    # front-end row reads to tell the two 403s apart.
    code=403
    body="${FAKE96_NOIDENT_BODY:-}"
    ;;
  *"IDTOKEN-CALLER-${FAKE96_OUTSIDER_DOMAIN}"*)
    # A caller that is no job account of the installation: IAM let it through
    # (the case of an account somebody granted run.invoker) and the service's own
    # account table refuses it. A case that wants the finding instead sets
    # FAKE96_OUTSIDER_IS_JOB=yes, and then this caller is answered like a job.
    if [ "${FAKE96_OUTSIDER_IS_JOB:-no}" = yes ]; then
      code=200
      body=$(cat "$FAKE96_DIR/mint.json")
    else
      code=403
      body='{"error":"unverified_job_identity","message":"the caller is not a job of this installation"}'
    fi
    ;;
  *)
    case "$rid" in
    *"~$FAKE96_OTHER_SLUG~"*)
      code=403
      body='{"error":"unverified_run_identity","message":"the run identity is not a run of the calling job'"'"'s repository"}'
      ;;
    *)
      code=${FAKE96_MINT_CODE:-200}
      if [ -n "${FAKE96_MINT_BODY:-}" ]; then
        body=$FAKE96_MINT_BODY
      else
        body=$(cat "$FAKE96_DIR/mint.json")
      fi
      ;;
    esac
    ;;
  esac
  ;;
*api.github.com/installation/repositories*)
  body=$(cat "$FAKE96_DIR/repos.json")
  ;;
*api.github.com/repos/*/contents/*)
  code=200
  body='[]'
  ;;
*api.github.com/repos/*/actions/workflows/*/dispatches*)
  code=403
  body='{"message":"Resource not accessible by integration"}'
  ;;
*accounts:delete*)
  printf '%s\n' "$(printf '%s' "$data" | sed -n 's/.*\\"localId\\":\\"\([^\\]*\)\\".*/\1/p')" >>"$FAKE96_DIR/deleted"
  body='{}'
  ;;
*accounts:batchGet*)
  if [ -n "${FAKE96_BATCHGET_NOT_JSON:-}" ]; then
    # The user list read back as something this check cannot parse. The row's
    # whole assertion is the count out of this document, so a case wants it to
    # stop the run rather than to read back as an empty count.
    body=$FAKE96_BATCHGET_NOT_JSON
  elif [ "${FAKE96_SURVIVOR:-no}" = yes ]; then
    body=$(python3 - "$FAKE96_DIR/deleted" <<'PY'
import json, sys
try:
    uids = [l.strip() for l in open(sys.argv[1]) if l.strip()]
except FileNotFoundError:
    uids = []
print(json.dumps({"users": [{"localId": u} for u in uids]}))
PY
    )
  else
    body='{"users":[]}'
  fi
  ;;
*)
  printf 'fake curl: unexpected url %s\n' "$url" >&2
  exit 64
  ;;
esac

if [ -n "$outfile" ]; then
  printf '%s' "$body" >"$outfile"
else
  printf '%s' "$body"
fi
if [ -n "$writefmt" ]; then
  # curl's own -w formats are printf-like: this check uses %{http_code} alone
  # and once behind a \n, and both have to come out as curl writes them.
  fmt=${writefmt//\\n/$'\n'}
  printf '%s' "${fmt//\%\{http_code\}/$code}"
fi
CURL_EOF

  chmod +x "$1/bin/gcloud" "$1/bin/fugaro" "$1/bin/curl"
}

# refuse_scratch_removal dir — a fake `rm` for the one case about a scratch
# directory that cannot be removed: it refuses the scratch directory itself
# (mktemp -d's `chk96.XXXXXXXX` and nothing under it, so the check's own removals
# inside it still work) and is the real `rm` for everything else. It is written
# by the case that wants it and by no other: a `restore` row that FAILs has to
# make the run exit 1, and this is the only way to get one.
refuse_scratch_removal() { # dir
  cat >"$1/bin/rm" <<RM_EOF
#!/usr/bin/env bash
set -euo pipefail
for a in "\$@"; do
  case "\$a" in
  */chk96.????????) printf 'rm: %s: Operation not permitted\n' "\$a" >&2; exit 1 ;;
  esac
done
exec $(command -v rm) "\$@"
RM_EOF
  chmod +x "$1/bin/rm"
}

# ---------------------------------------------------------------- the fixtures
write_doctor() { # dir [GCP_PROJECT] [NAME]
  printf '{"ok": true, "checks": [], "project": {"name": "%s", "gcp_project": "%s", "region": "%s"}}\n' \
    "${3:-$PROJECT_NAME}" "${2:-$PROJECT}" "$REGION" >"$1/state/doctor.json"
}

# write_budget dir — the installation's repository state. BUDGET_REPOS=one makes
# the sandbox repository the only one with state, which is the one shape in which
# the check derives the repository itself instead of refusing to pick.
write_budget() { # dir
  if [ "${BUDGET_REPOS:-two}" = one ]; then
    printf '{"project": "%s", "gcp_project": "%s", "mode": "observe", "day": "2026-11-10", "repos": [{"slug": "%s", "repo": "%s"}], "running": []}\n' \
      "$FUGARO_PROJECT" "$PROJECT" "$SANDBOX_SLUG" "$SANDBOX_REPO" >"$1/state/budget.json"
    return 0
  fi
  cat >"$1/state/budget.json" <<JSON
{"project": "$FUGARO_PROJECT", "gcp_project": "$PROJECT", "mode": "observe", "day": "2026-11-10",
 "repos": [{"slug": "$SANDBOX_SLUG", "repo": "$SANDBOX_REPO"},
           {"slug": "$OTHER_SLUG", "repo": "$OTHER_REPO"}],
 "running": []}
JSON
}

# write_config dir [SERVICE_URL] — the installation's own config file. An empty
# argument leaves the credentials_service block out entirely, which is a
# repository whose root has not been applied with the service on.
write_config() { # dir
  local dir=$1 url=${2-$SERVICE_URL}
  {
    printf 'project: %s\n' "$PROJECT_NAME"
    if [ -n "$url" ]; then
      printf 'credentials_service:\n  url: %s\n' "$url"
    fi
    printf 'budget:\n  rtdb_url: https://%s-default-rtdb.firebaseio.com\n' "$FUGARO_PROJECT"
    printf '  firebase_project: %s\n  firebase_api_key: %s\n  token_signer: %s\n' \
      "$FUGARO_PROJECT" "$FIREKEY" "$SIGNER"
    # Four spaces, which is what fugaro itself writes: localcfg.Config.Marshal
    # is yaml.Marshal (yaml.v3's default indent), and a check that only finds a
    # repository at two spaces finds none of them in a real config.
    printf 'repos:\n    %s:\n        provider: github\n        workflows:\n            - default\n' "$SANDBOX_REPO"
    if [ "${READY:-yes}" = yes ]; then
      printf '        credentials_service_ready: true\n'
    fi
    printf '    %s:\n        provider: github\n        workflows:\n            - default\n' "$OTHER_REPO"
  } >"$dir/state/fugaro.yaml"
}

# write_job dir — the sandbox repository's own job. JOB_URL=no takes the service
# URL off it, JOB_APP_KEY=no takes the App key's environment entry and mount
# off it, JOB_PROFILE=yes makes it a shared project job, and JOB_SA overrides
# the account it runs as.
write_job() { # dir
  local dir=$1
  JOB_URL="${JOB_URL-yes}" JOB_APP_KEY="${JOB_APP_KEY-yes}" JOB_PROFILE="${JOB_PROFILE:-no}" \
    JOB_NAME="${JOB_NAME:-$JOB}" JOB_SA="${JOB_SA:-$JOBSA}" python3 - "$dir/state/job.json" "$dir/state/jobs.json" <<'PY'
import json, os, sys

env = [{"name": "FUGARO_PROJECT", "value": os.environ["FAKE96_PROJ"]},
       {"name": "FUGARO_GITHUB_APP_ID", "value": os.environ["FAKE96_APPID"]}]
container = {"env": env}
spec = {"serviceAccountName": os.environ["JOB_SA"]}
labels = {"fugaro_repo": os.environ["FAKE96_LABEL"], "fugaro_workflow": "default"}
if os.environ["JOB_URL"] == "yes":
    env.append({"name": "FUGARO_CREDENTIALS_SERVICE_URL", "value": os.environ["FAKE96_URL"]})
if os.environ["JOB_APP_KEY"] == "yes":
    env.append({"name": "FUGARO_GITHUB_APP_PRIVATE_KEY",
                "valueSource": {"secretKeyRef": {"secret": os.environ["FAKE96_APPKEY"], "key": "latest"}}})
    container["volumeMounts"] = [{"name": "app-key", "mountPath": "/secrets/app-key"}]
    spec["volumes"] = [{"name": "app-key", "secret": {"secretName": os.environ["FAKE96_APPKEY"]}}]
if os.environ["JOB_PROFILE"] == "yes":
    labels = {"fugaro_profile": "shared", "fugaro_role": "project"}
spec["containers"] = [container]
job = {"metadata": {"name": os.environ["JOB_NAME"], "labels": labels},
       "spec": {"template": {"spec": spec}}}
check = {"metadata": {"name": os.environ["JOB_NAME"] + "-check",
                      "labels": {"fugaro_repo": os.environ["FAKE96_LABEL"], "fugaro_role": "check"}},
         "spec": {"template": {"spec": {"serviceAccountName": os.environ["JOB_SA"],
                                        "containers": [{"env": []}]}}}}
open(sys.argv[1], "w", encoding="utf-8").write(json.dumps(job))
open(sys.argv[2], "w", encoding="utf-8").write(json.dumps([job, check]))
PY
}

write_service() { # dir
  cat >"$1/state/services.json" <<JSON
[{"metadata": {"name": "history", "labels": {"fugaro_role": "history"}}},
 {"metadata": {"name": "$SERVICE", "labels": {"fugaro_role": "credentials", "fugaro": "managed"}}}]
JSON
  cat >"$1/state/service.json" <<JSON
{"metadata": {"name": "$SERVICE", "labels": {"fugaro_role": "credentials"}},
 "spec": {"template": {"spec": {"serviceAccountName": "$SVCSA",
   "containers": [{"env": [{"name": "FUGARO_PROJECT", "value": "$FUGARO_PROJECT"}]}]}}},
 "status": {"url": "$SERVICE_URL"}}
JSON
}

# write_secrets dir [APPKEY_MEMBER] — the bundle's policy is Q6's one grant; the
# App key's carries a project-wide operator and, when APPKEY_MEMBER=job, the
# job's own account, which is what makes the job hold the key.
write_secrets() { # dir
  local dir=$1 member=${2:-job}
  cat >"$dir/state/secrets.json" <<JSON
[{"name": "$APPKEY", "labels": {"fugaro": "managed", "fugaro_project": "$FUGARO_PROJECT", "fugaro_secret": "$APPKEY"}},
 {"name": "$BUNDLE", "labels": {"fugaro": "managed", "fugaro_project": "$FUGARO_PROJECT"}}]
JSON
  local appkey_members="\"serviceAccount:owner@example.com\""
  if [ "$member" = job ]; then
    appkey_members="\"serviceAccount:$JOBSA\", \"serviceAccount:owner@example.com\""
  fi
  printf '{"bindings": [{"role": "roles/secretmanager.secretVersionManager", "members": [%s]}]}\n' \
    "$appkey_members" >"$dir/state/policy-app.json"
  printf '{"bindings": [{"role": "roles/secretmanager.secretAccessor", "members": ["serviceAccount:%s"]}]}\n' \
    "$SVCSA" >"$dir/state/policy-bundle.json"
}

# write_audit dir [CHECKED_SLUG OTHER_SLUG] — the service's log. The two
# templates are per run -- the mint's line names the repository the token was
# minted for and the refusal's names the run identity's own, which is the pair
# a case that names its repository has to swap. The purpose filter is the
# front-end row's count, and `audit-purpose-after` is what a case writes to
# make that count move.
write_audit() { # dir
  local dir=$1 checked=${2:-$SANDBOX_SLUG} other=${3:-$OTHER_SLUG}
  printf '[{"jsonPayload": {"run": "@RUN@", "repository": "%s", "job": "%s", "caller": "%s", "credential": "%s", "purpose": "mint", "status": "ok"}}]\n' \
    "$checked" "$JOB" "$JOBSA" "$APPKEY" >"$dir/state/audit-ok.tmpl"
  printf '[{"jsonPayload": {"run": "@RUN@", "repository": "%s", "job": "%s", "caller": "%s", "credential": "%s", "purpose": "mint", "status": "unverified_run_identity"}}]\n' \
    "$other" "$JOB" "$JOBSA" "$APPKEY" >"$dir/state/audit-refusal.tmpl"
  printf '[]\n' >"$dir/state/audit-purpose.json"
  : >"$dir/state/audit-purpose-after"
}

# The three answers the fake service gives: the mint's token, the two refusals
# and the installation's repository list. The repository they name is the one
# the check is about, which is $SANDBOX_REPO unless a case named another: the
# service mints for the calling repository alone, and the token's reach at
# GitHub is that repository's own. The mint's answer names it the way the real
# service does — the canonical "<provider>/<owner>/<name>"
# (internal/credsvc's MintResponse), not the bare "<owner>/<name>" the config
# and the budget document use — while GitHub's own `full_name` is the bare one.
write_answers() { # dir [CHECKED_REPO]
  local dir=$1 checked=${2:-$SANDBOX_REPO}
  printf '{"token": "%s", "expires_at": "%s", "repository": "github/%s"}\n' \
    "$MINTED" "$(date -u -d '+45 minutes' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+45M +%Y-%m-%dT%H:%M:%SZ)" \
    "$checked" >"$dir/state/mint.json"
  printf '{"total_count": 1, "repositories": [{"full_name": "%s"}]}\n' "$checked" >"$dir/state/repos.json"
}

# setup_case NAME — a fresh case: the fakes, a fugaro-dev installation whose
# config records the service, a repository job with the service on it and the
# App key still in its environment (the state this check is run against, its
# drop held), and a service whose answers are all the expected ones. A case
# changes what it is about.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  export FAKE96_DIR="$dir/state" FAKE96_PROJ="$FUGARO_PROJECT" FAKE96_APPID="$APPID"
  export FAKE96_LABEL="$SANDBOX_SLUG" FAKE96_URL="$SERVICE_URL"
  export FAKE96_APPKEY="$APPKEY" FAKE96_BUNDLE="$BUNDLE"
  export FAKE96_OTHER_SLUG="$OTHER_SLUG"
  export FAKE96_OUTSIDER_DOMAIN="${SIGNER##*@}"
  export FAKE96_MINT_URL="$SERVICE_URL/v1/github/installation-token"
  write_doctor "$dir"
  write_budget "$dir"
  write_config "$dir"
  write_service "$dir"
  write_job "$dir"
  write_secrets "$dir"
  write_audit "$dir"
  write_answers "$dir"
  : >"$dir/state/deleted"
}

# run96 [ARGS...] — runs the check on the case at hand. The sandbox repository is
# named for the case, because that is what the check asks of the owner: the
# default fixture has two repositories with state, and the check refuses to pick
# one of several itself (RUN96_UNNAMED=yes is for the cases about what it does
# then, and for the cases that name a repository of their own). RUN96_ARGS adds
# arguments, RUN96_PATH prefixes PATH (for a fake that has to come first), and
# RUN96_ENV names environment variables (NAME=value). Sets $status, $out and
# $dir/err.
run96() {
  status=0
  local named=("$SANDBOX_REPO")
  [ "${RUN96_UNNAMED:-no}" = yes ] && named=()
  out=$(env TMPDIR="$dir/tmp" FAKE96_DIR="$dir/state" FUGARO_CONFIG="$dir/state/fugaro.yaml" \
    PATH="${RUN96_PATH:+$RUN96_PATH:}$dir/bin:$PATH" \
    ${RUN96_ENV[@]+"${RUN96_ENV[@]}"} \
    bash "$script" ${RUN96_ARGS[@]+"${RUN96_ARGS[@]}"} ${named[@]+"${named[@]}"} "$@" 2>"$dir/err") || status=$?
  printf '%s' "$out" >"$dir/out"
  RUN96_ARGS=()
  RUN96_ENV=()
  RUN96_PATH=""
  RUN96_UNNAMED=no
  BUDGET_REPOS=two
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 96 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

expect_scratch_gone() { # dir what
  local scratch
  scratch=$(scratch_of "$1")
  if [ -z "$scratch" ]; then
    fail "$2: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$2: the scratch directory $scratch survived the run"
  [ -z "$(find "$1/tmp" -mindepth 1 2>/dev/null)" ] || fail "$2: files were left in TMPDIR: $(find "$1/tmp" -mindepth 1)"
  return 0
}

# expect_no_credential_anywhere dir what — no secret the run held is in what it
# printed, and none is on any command line any fake was given. The argv log is
# the point: a token passed with `-H` instead of `--config -` is visible to
# every user of the machine, and this is the assertion that says it is not.
expect_no_credential_anywhere() { # dir what
  local dir=$1 what=$2 printed
  printed=$(cat "$dir/out" "$dir/err" 2>/dev/null || true)
  local needle
  for needle in "$MINTED" "$FIREKEY" FAKEJWT FAKEIDTOKEN ACCESS-TOKEN-FIXTURE; do
    expect_absent "$printed" "$needle" "$what: '$needle' in what the run printed"
    expect_absent "$(cat "$dir/state/calls")" "$needle" "$what: '$needle' on a command line"
  done
  expect_absent "$printed" ghs_ "$what: the token prefix in what the run printed"
}

# ------------------------------------------------------------------- the tests
test_happy_path() {
  setup_case happy
  run96
  expect_status "$status" 0 "happy path"
  expect_block_keys "$out" "happy path"
  expect_contains "$out" "CHECK 96 RESULT END (exit 0)" "happy path: the end line"
  expect_contains "$out" "FACT installation: PASS — " "happy path: the installation"
  expect_contains "$out" "$SANDBOX_REPO" "happy path: the repository it derived"
  expect_contains "$out" "$OTHER_REPO" "happy path: the second repository the pairing case needs"
  expect_contains "$out" "FACT service: PASS — " "happy path: the service"
  expect_contains "$out" "$SERVICE_URL" "happy path: the URL it read"
  expect_contains "$out" "FACT job: PASS — " "happy path: the job"
  expect_contains "$out" "$JOB" "happy path: the job it found by its label"
  expect_contains "$out" "FACT job-env: PASS — " "happy path: the job's environment"
  expect_contains "$out" "FACT app-key: PASS — " "happy path: the first claim"
  expect_contains "$out" "C2.3's drop HELD" "happy path: the held drop is the expected answer here"
  expect_contains "$out" "FACT bundle: PASS — " "happy path: Q6's one grant"
  expect_contains "$out" "FACT front-end: PASS — " "happy path: the front end refused the anonymous call"
  expect_contains "$out" "FACT mint: PASS — " "happy path: the mint"
  expect_contains "$out" "$SANDBOX_REPO" "happy path: the repository the token is for"
  expect_contains "$out" "the request has no repository field to fill in" "happy path: the request cannot name another"
  expect_contains "$out" "FACT reach: PASS — " "happy path: the token's reach at GitHub"
  expect_contains "$out" "FACT audit: PASS — " "happy path: the service's own line"
  expect_contains "$out" "FACT pairing: PASS — " "happy path: X's run with Y's job"
  expect_contains "$out" "the run identity is not a run of the calling job's repository" "happy path: the refusal's own message"
  expect_contains "$out" "FACT outsider: PASS — " "happy path: a caller that is no job account"
  expect_contains "$out" "FACT survivors: PASS — " "happy path: nothing this check created survives"
  expect_contains "$out" "FACT xtrace: PASS — " "happy path: not tracing"
  expect_contains "$out" "FACT restore: PASS — " "happy path: the Restore"
  expect_absent "$out" "FACT secret-scan:" "happy path: nothing was dropped from the block"
  expect_no_credential_anywhere "$dir" "happy path"
  expect_scratch_gone "$dir" "happy path"
}

test_dry_run_runs_nothing_that_writes() {
  setup_case dry
  run96 --dry-run
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 96 RESULT" "dry run: the block"
  expect_contains "$out" "FACT dry-run: PASS — " "dry run: the row that says so"
  expect_contains "$out" "guards and every read only" "dry run: what it did"
  expect_contains "$(cat "$dir/err")" "NOTHING below was run" "dry run: the plan"
  expect_contains "$(cat "$dir/err")" "MINTS a real GitHub installation token" "dry run: the mint it did not make"
  expect_contains "$(cat "$dir/err")" "accounts:delete" "dry run: the deletions it did not make"
  expect_contains "$out" "INFO — not measured" "dry run: the rows it did not measure"
  # The reads happened, the calls did not.
  expect_contains "$(cat "$dir/state/calls")" "gcloud run services list" "dry run: the service read"
  expect_absent "$(cat "$dir/state/calls")" "curl " "dry run: a network call"
  expect_absent "$(cat "$dir/state/calls")" "sign-jwt" "dry run: a signed run identity"
  expect_absent "$(cat "$dir/state/calls")" "accounts:delete" "dry run: a deletion"
  expect_scratch_gone "$dir" "dry run"
}

test_a_named_repository_is_the_one_checked_and_a_lone_one_needs_no_name() {
  setup_case named
  # The check's roles swap when the repository is named: the sandbox is the one
  # named and the pairing case's run identity has to belong to the other one, so
  # the fake is told which repository this run's pairing case is about.
  RUN96_ENV=(FAKE96_OTHER_SLUG="$SANDBOX_SLUG")
  RUN96_UNNAMED=yes
  write_audit "$dir" "$OTHER_SLUG" "$SANDBOX_SLUG"
  write_answers "$dir" "$OTHER_REPO"
  run96 "$OTHER_REPO"
  expect_status "$status" 0 "named repository"
  expect_contains "$out" "the sandbox repository is $OTHER_REPO (slug $OTHER_SLUG), the one you named" \
    "named repository: the one named"
  expect_contains "$out" "the second repository the pairing case needs is $SANDBOX_REPO" \
    "named repository: the other one is the pairing case's"
  # And with no argument where the installation's state holds exactly one
  # repository: that one is the sandbox, so the run gets past the naming. It
  # stops one step later, at the pairing case, which needs a second repository —
  # which is why the command the owner runs names the repository: a derivation
  # this check can make is a derivation on an installation that cannot pass the
  # third claim anyway. What this case reads is that the run got as far as that
  # stop instead of the refusal to pick.
  BUDGET_REPOS=one
  setup_case derived
  RUN96_UNNAMED=yes
  run96
  expect_status "$status" 2 "the derived repository"
  expect_contains "$out" "the installation has one repository with state, and the pairing case needs a second one" \
    "the derived repository: the one repository with state was read as the sandbox"
  expect_absent "$out" "cannot tell which one is the sandbox" \
    "the derived repository: no refusal to pick a repository where there is only one"
}

test_a_derivation_among_several_repositories_is_refused() {
  setup_case ambiguous
  # Two repositories with state and no name given: the check must not pick one,
  # because it mints a real GitHub installation token for whatever it names. It
  # stops instead, and the message lists what it could have been.
  RUN96_UNNAMED=yes
  run96
  expect_status "$status" 2 "the ambiguous derivation"
  expect_contains "$out" "this installation has 2 repositories with state and none was named, so this check cannot tell which one is the sandbox: name it, one of $OTHER_REPO, $SANDBOX_REPO" \
    "the ambiguous derivation: the refusal names the candidates"
  expect_contains "$out" "FACT installation: FAIL — not reached" "the ambiguous derivation: nothing was claimed"
  expect_absent "$(cat "$dir/state/calls")" "curl " "the ambiguous derivation: a call before the refusal"
  expect_absent "$(cat "$dir/state/calls")" "installation-token" "the ambiguous derivation: a mint"
}

test_a_repository_that_is_not_one_with_state_is_refused() {
  setup_case notmine
  RUN96_UNNAMED=yes
  run96 acme/not-managed
  expect_status "$status" 2 "a repository with no state"
  expect_contains "$out" "acme/not-managed is not one of this installation's repositories with state" \
    "a repository with no state: the refusal says which"
  expect_absent "$(cat "$dir/state/calls")" "curl " "a repository with no state: a call before the refusal"
}

test_belong_is_refused() {
  setup_case belong
  RUN96_UNNAMED=yes
  run96 acme/belong-sandbox
  expect_status "$status" 2 "belong"
  expect_contains "$out" "aborted: FAIL" "belong: the abort is recorded"
  expect_contains "$out" "never belong" "belong: the guard says why"
  expect_absent "$(cat "$dir/state/calls")" "gcloud" "belong: a cloud call before the guard"
  expect_absent "$(cat "$dir/state/calls")" "fugaro" "belong: a fugaro call before the guard"
}

test_a_repository_argument_that_is_not_owner_name_is_refused() {
  setup_case shape
  RUN96_UNNAMED=yes
  run96 sandbox
  expect_status "$status" 2 "argument shape"
  expect_contains "$out" "it has to be <owner>/<name>" "argument shape: the message"
  expect_absent "$(cat "$dir/state/calls")" "curl " "argument shape: a call before the guard"
}

test_a_config_that_is_not_fugaro_dev_stops() {
  setup_case otherproject
  write_doctor "$dir" other-project
  run96
  expect_status "$status" 2 "another project"
  expect_contains "$out" "runs on $PROJECT only" "another project: the guard says why"
  expect_contains "$out" "FACT installation: FAIL — not reached" "another project: no claim was measured"
  expect_absent "$(cat "$dir/state/calls")" "curl " "another project: a call before the guard"
}

test_a_repository_with_one_job_too_few_stops() {
  setup_case nojob
  printf '[]\n' >"$dir/state/jobs.json"
  run96
  expect_status "$status" 2 "no job"
  expect_contains "$out" "no single job of its own" "no job: the message"
  expect_contains "$out" "fugaro setup --repo" "no job: the command that makes one"
}

test_the_shared_project_job_with_the_url_is_a_fail() {
  setup_case profilejob
  JOB_PROFILE=yes write_job "$dir"
  run96
  expect_status "$status" 1 "shared project job"
  expect_contains "$out" "FACT job-env: FAIL — " "shared project job: the row"
  expect_contains "$out" "SHARED PROJECT job" "shared project job: what it found"
  expect_contains "$out" "Global Constraint 6" "shared project job: why it is wrong"
  # A finding, not a reason to stop: the claims after it are still measured.
  expect_contains "$(cat "$dir/state/calls")" "curl " "shared project job: the run went on"
  expect_contains "$out" "FACT mint: PASS — " "shared project job: the mint is still measured"
}

test_a_job_that_has_no_service_url_while_the_config_records_one_is_a_fail() {
  setup_case urlmissing
  JOB_URL=no write_job "$dir"
  run96
  expect_status "$status" 1 "the URL is missing"
  expect_contains "$out" "FACT job-env: FAIL — " "the URL is missing: the row"
  expect_contains "$out" "carries no FUGARO_CREDENTIALS_SERVICE_URL" "the URL is missing: what it found"
  expect_contains "$out" "fugaro update --repo" "the URL is missing: the operator's next step"
  expect_contains "$out" "FACT mint: PASS — " "the URL is missing: the mint is still measured"
}

test_the_drop_landed_is_a_pass() {
  setup_case landed
  printf 'var ServiceHeldSecretNames = []string{"github-app-key"}\n' >"$dir/state/providers.go"
  JOB_APP_KEY=no write_job "$dir"
  write_secrets "$dir" operator
  RUN96_ENV=(CHECK96_PROVIDERS="$dir/state/providers.go")
  run96
  expect_status "$status" 0 "the drop landed"
  expect_contains "$out" "FACT app-key: PASS — " "the drop landed: the row"
  expect_contains "$out" "C2.3's drop LANDED" "the drop landed: what it found"
}

test_the_drop_without_the_code_is_a_fail() {
  setup_case nocode
  # Neither side names the App key: the deployed job holds none of it and the
  # code that decides the drop does not carry it either.
  printf 'var ServiceHeldSecretNames = []string{}\n' >"$dir/state/providers.go"
  JOB_APP_KEY=no write_job "$dir"
  # The job's account is not a member of the App key's container either: with
  # neither the mount nor the accessor, nothing about the job holds it.
  write_secrets "$dir" operator
  RUN96_ENV=(CHECK96_PROVIDERS="$dir/state/providers.go")
  run96
  expect_status "$status" 1 "the drop without the code"
  expect_contains "$out" "FACT app-key: FAIL — " "the drop without the code: the row"
  expect_contains "$out" "the two have moved apart" "the drop without the code: the pair it names"
}

test_the_switch_hook_may_only_name_the_file_it_reads() {
  setup_case hook
  RUN96_ENV=(CHECK96_PROVIDERS="$dir/state/elsewhere.go")
  run96
  expect_status "$status" 2 "the hook"
  expect_contains "$out" "may only name the file that declares the switch" "the hook: the refusal"
}

test_a_bundle_with_the_job_as_a_second_member_is_a_fail() {
  setup_case bundleleak
  printf '{"bindings": [{"role": "roles/secretmanager.secretAccessor", "members": ["serviceAccount:%s", "serviceAccount:%s"]}]}\n' \
    "$SVCSA" "$JOBSA" >"$dir/state/policy-bundle.json"
  run96
  expect_status "$status" 1 "the bundle's second member"
  expect_contains "$out" "FACT bundle: FAIL — " "the bundle's second member: the row"
  expect_contains "$out" "a job account with an accessor on the bundle" "the bundle's second member: why it is a finding"
}

test_a_mint_whose_answer_names_another_repository_is_a_fail() {
  setup_case otherrepo
  # The service mints for a repository this run is not of. Its answer names the
  # canonical spelling of the OTHER repository — "github/<owner>/<name>", which
  # is what the real service answers — so this is also the case that says the
  # row compares the two spellings of one repository rather than the strings.
  write_answers "$dir" "$OTHER_REPO"
  run96
  expect_status "$status" 1 "another repository"
  expect_contains "$out" "FACT mint: FAIL — " "another repository: the row"
  expect_contains "$out" "a token for github/$OTHER_REPO" "another repository: the repository the answer named"
  expect_contains "$out" "while the run identity is a run of $SANDBOX_REPO" "another repository: the run it was for"
}

test_a_mint_whose_answer_is_a_longer_name_ending_in_the_run_repository_is_a_fail() {
  setup_case doubled
  # The answer names a repository whose owner happens to be the run's owner
  # followed by the run's own "<owner>/<name>": "github/acme/acme/sandbox" is a
  # repository called "acme/sandbox" that belongs to an owner called "acme", and
  # it is not the run's repository. A row that matched the answer against $REPO
  # by its tail with a glob would take it; the row strips exactly one segment —
  # the provider's — and compares the rest, so it does not.
  python3 - "$dir/state/mint.json" "$SANDBOX_REPO" <<'PY'
import json, sys
p, repo = sys.argv[1], sys.argv[2]
d = json.load(open(p))
d["repository"] = "github/%s/%s" % (repo.split("/", 1)[0], repo)
json.dump(d, open(p, "w"))
PY
  run96
  expect_status "$status" 1 "an answer that only ends in the run's repository"
  expect_contains "$out" "FACT mint: FAIL — " "an answer that only ends in the run's repository: the row"
  expect_contains "$out" "a token for github/acme/acme/sandbox" "an answer that only ends in the run's repository: the repository the answer named"
}

test_a_mint_whose_expiry_is_outside_the_hour_is_a_fail() {
  setup_case badexpiry
  python3 - "$dir/state/mint.json" "$SANDBOX_REPO" <<'PY'
import datetime, json, sys
p, repo = sys.argv[1], sys.argv[2]
d = json.load(open(p))
d["repository"] = "github/" + repo
d["expires_at"] = (datetime.datetime.now(datetime.timezone.utc)
                   + datetime.timedelta(hours=3)).strftime("%Y-%m-%dT%H:%M:%SZ")
json.dump(d, open(p, "w"))
PY
  run96
  expect_status "$status" 1 "an expiry outside the hour"
  expect_contains "$out" "FACT mint: FAIL — " "an expiry outside the hour: the row"
  expect_contains "$out" "inside the hour: no" "an expiry outside the hour: what it read"
}

test_a_bundle_whose_one_member_is_not_the_service_account_is_a_fail() {
  setup_case bundlemember
  # One binding, one member, no condition, and the member is not the service's
  # own account: the count is Q6's and the account is not, which is the half a
  # row that reads only the count would report as held.
  printf '{"bindings": [{"role": "roles/secretmanager.secretAccessor", "members": ["serviceAccount:someone-else@%s.iam.gserviceaccount.com"]}]}\n' \
    "$PROJECT" >"$dir/state/policy-bundle.json"
  run96
  expect_status "$status" 1 "the bundle's one member"
  expect_contains "$out" "FACT bundle: FAIL — " "the bundle's one member: the row"
  expect_contains "$out" "the service's own account among them: no" "the bundle's one member: what it read"
}

test_a_service_reachable_without_iam_is_a_fail() {
  setup_case noiam
  # The call with no identity reached the container: the service's own header
  # parse refused it (a call with no Authorization header at all fails
  # RequestFromHTTP, which server.go answers through refuse with
  # unverified_run_identity) — a 403 with no audit line, the same status and the
  # same unmoved count the front end's own refusal leaves. The body is what tells
  # them apart, and a front-end row that read only the status would call this the
  # outer fence holding.
  RUN96_ENV=(FAKE96_NOIDENT_BODY='{"error": "unverified_run_identity", "message": "credsvc: the call carries no Authorization header"}')
  run96
  expect_status "$status" 1 "a service without IAM"
  expect_contains "$out" "FACT front-end: FAIL — " "a service without IAM: the row"
  expect_contains "$out" "BY THE SERVICE" "a service without IAM: who answered the call"
}

test_a_log_that_cannot_be_counted_is_not_a_passing_front_end_row() {
  setup_case uncountable
  # `gcloud logging read` exits 0 with something this check cannot parse. The
  # front-end row takes a count of the service's log before its call and one
  # after, and PASSes when the two are equal — and two counts that were never
  # read are both the empty string, which is equal to itself. That shape printed
  # `front-end: PASS` asserting "an unauthenticated request never reaches the
  # container" from a log the check had not read, which is the one thing this row
  # must never do: the fence it reads is the whole reason the account table is a
  # second gate rather than the only one. The read has to stop the run instead.
  RUN96_ENV=(FAKE96_COUNT_NOT_JSON='WARNING: a gcloud notice on stdout, and not a document')
  run96
  expect_status "$status" 2 "a log that cannot be counted"
  expect_absent "$out" "FACT front-end: PASS" "a log that cannot be counted: the row that would assert the fence from a log never read"
  expect_contains "$out" "FACT front-end: FAIL — not reached" "a log that cannot be counted: the row records that it was never measured"
  expect_contains "$out" "the service's own log could not be read" "a log that cannot be counted: what stopped the run"
}

test_a_scratch_directory_that_cannot_be_removed_is_a_fail() {
  setup_case restore
  # The fake `rm` refuses the scratch directory itself, so the check's own
  # cleanup cannot remove it. The row has to say so AND the run has to exit 1: a
  # block that printed `restore: FAIL` beside `END (exit 0)` would read as a
  # pass, and the exit code is what a wrapper around this check reads.
  refuse_scratch_removal "$dir"
  run96
  expect_status "$status" 1 "a scratch directory that stayed"
  expect_contains "$out" "FACT restore: FAIL — " "a scratch directory that stayed: the row"
  expect_contains "$out" "the scratch directory is STILL THERE" "a scratch directory that stayed: what it says"
  expect_contains "$out" "CHECK 96 RESULT END (exit 1)" "a scratch directory that stayed: the block's own end line says the run failed"
  scratch=$(scratch_of "$dir")
  [ -z "$scratch" ] || rm -rf "$scratch"
  return 0
}

test_a_mint_that_answers_other_than_200_is_a_fail() {
  setup_case mintrefused
  # The bundle has no document yet, or the service cannot read it: a refusal with
  # a cause, and no token anywhere in it.
  run96_env=(FAKE96_MINT_CODE=503)
  run96_env+=(FAKE96_MINT_BODY='{"error": "credentials_unavailable", "message": "the bundle has no github-app-key"}')
  RUN96_ENV=("${run96_env[@]}")
  run96
  expect_status "$status" 1 "the mint refused"
  expect_contains "$out" "FACT mint: FAIL — " "the mint refused: the row"
  expect_contains "$out" "credentials_unavailable" "the mint refused: the service's own cause"
  expect_contains "$out" "fugaro creds set github-app-key" "the mint refused: the operator's next step"
  # The rows after it are about identities and accounts and need no token, so
  # they still ran: one cause is not five rows of "not reached".
  expect_contains "$out" "FACT pairing: PASS — " "the mint refused: the pairing is still measured"
  expect_contains "$out" "FACT reach: INFO — not measured" "the mint refused: the reach says why it could not be read"
  expect_contains "$out" "FACT survivors: PASS — " "the mint refused: the users it did create are deleted all the same"
}

test_a_mint_that_answers_200_with_no_token_stops() {
  setup_case mintnotoken
  RUN96_ENV=(FAKE96_MINT_BODY='{"repository": "acme/sandbox"}')
  run96
  expect_status "$status" 2 "200 without a token"
  expect_contains "$out" "answered 200 with no token in it" "200 without a token: why it stopped"
}

test_a_pairing_that_is_answered_is_the_finding() {
  setup_case pairinganswered
  # The service answers the pairing call with a token — the sandbox job's account
  # minted for another repository, which is the one thing this case exists to
  # catch. The fake refuses a run identity of $FAKE96_OTHER_SLUG, so pointing
  # that at a repository no run identity here belongs to is what makes the
  # fake answer like a service that does not check the pair.
  RUN96_ENV=(FAKE96_OTHER_SLUG=acme-never)
  run96
  expect_status "$status" 1 "the pairing answered"
  expect_contains "$out" "FACT pairing: FAIL — " "the pairing answered: the row"
  expect_contains "$out" "A 200 is the finding this case exists to prevent" "the pairing answered: why it is a finding"
}

test_a_refusal_with_no_audit_line_is_the_finding() {
  setup_case noauditline
  printf '[]\n' >"$dir/state/audit-refusal.tmpl"
  run96
  expect_status "$status" 1 "the unrecorded refusal"
  expect_contains "$out" "FACT pairing: FAIL — " "the unrecorded refusal: the audit row"
  expect_contains "$out" "a 0 here would be the finding" "the unrecorded refusal: why it is a finding"
}

test_a_mint_that_answered_and_left_no_line_is_a_fail() {
  setup_case noauditmint
  printf '[]\n' >"$dir/state/audit-ok.tmpl"
  run96
  expect_status "$status" 1 "the unrecorded mint"
  expect_contains "$out" "FACT audit: FAIL — " "the unrecorded mint: the row"
}

test_an_outsider_that_is_answered_is_the_finding() {
  setup_case outsideranswered
  # A service that admits a caller which is no job account of the installation:
  # the fake answers the signer's token like a job's, which is the finding the
  # account table exists to prevent.
  RUN96_ENV=(FAKE96_OUTSIDER_IS_JOB=yes)
  run96
  expect_status "$status" 1 "the outsider answered"
  expect_contains "$out" "FACT outsider: FAIL — " "the outsider answered: the row"
  expect_contains "$out" "was ANSWERED 200 by the mint" "the outsider answered: what it found"
  expect_contains "$out" "$SIGNER" "the outsider answered: the account it names"
}

test_a_user_that_survived_the_deletions_is_a_fail() {
  setup_case survivors
  RUN96_ENV=(FAKE96_SURVIVOR=yes)
  run96
  expect_status "$status" 1 "a surviving user"
  expect_contains "$out" "FACT survivors: FAIL — " "a surviving user: the row"
  expect_contains "$out" "the read-back IS the assertion" "a surviving user: why it is a finding"
  expect_contains "$out" "FACT restore: PASS — " "a surviving user: the deletions did happen"
}

test_a_user_list_that_cannot_be_read_back_stops_the_run() {
  setup_case unreadable
  # The last step reads the project's user list back and its assertion is the
  # count out of that document. A list this check cannot parse used to read back
  # as an empty count — a document never read scored as a count, and the FAIL
  # that followed named no number at all. The read stops the run now: "could not
  # tell" is what this is, and it is exit 2 rather than a row that looks like a
  # measurement.
  RUN96_ENV=(FAKE96_BATCHGET_NOT_JSON='<html>the user list, unreadable</html>')
  run96
  expect_status "$status" 2 "a user list that cannot be read back"
  expect_absent "$out" "the read-back IS the assertion" "a user list that cannot be read back: the row that would report a count it never read"
  expect_contains "$out" "FACT survivors: FAIL — not reached" "a user list that cannot be read back: the row records that it was never measured"
  expect_contains "$out" "the project's user list is not the document this check reads" "a user list that cannot be read back: what stopped the run"
}

test_a_line_that_carries_a_token_is_dropped_and_reported() {
  setup_case scan
  # A detail that carries the token's prefix: the block must print the run
  # without that line and say so, rather than print it.
  python3 - "$dir/state/mint.json" "$SANDBOX_SLUG" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["repository"] = "ghs_fixture_smuggled_into_the_repository_field"
json.dump(d, open(p, "w"))
PY
  run96
  expect_status "$status" 1 "the secret scan"
  expect_contains "$out" "FACT secret-scan: FAIL — " "the secret scan: the row"
  expect_absent "$out" "ghs_fixture_smuggled_into_the_repository_field" "the secret scan: the value"
  expect_contains "$out" "CHECK 96 RESULT END (exit 1)" "the secret scan: the block says the run failed"
}

test_xtrace_is_refused() {
  setup_case xtrace
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE96_DIR="$dir/state" FUGARO_CONFIG="$dir/state/fugaro.yaml" \
    PATH="$dir/bin:$PATH" bash -x "$script" 2>"$dir/err") || status=$?
  printf '%s' "$out" >"$dir/out"
  expect_status "$status" 2 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL — " "xtrace: the row"
  expect_contains "$out" "this shell is tracing" "xtrace: the refusal"
  expect_absent "$(cat "$dir/state/calls")" "curl " "xtrace: any call at all"
  expect_no_credential_anywhere "$dir" "xtrace"
}

test_usage() {
  setup_case usage
  run96 --help
  expect_status "$status" 0 "usage"
  expect_contains "$out" "usage: check96.sh" "usage: the line"
  run96 --nonsense
  expect_status "$status" 2 "an unknown flag"
  expect_contains "$(cat "$dir/err")" "unknown argument" "an unknown flag: the refusal"
  RUN96_UNNAMED=yes
  run96 "$SANDBOX_REPO" "$OTHER_REPO"
  expect_status "$status" 2 "two repositories"
  expect_contains "$(cat "$dir/err")" "only one repository" "two repositories: the refusal"
}

# The detector's own self-test: the assertions above are only worth having if
# they fail on a run that leaks. A block that carries the token is assembled
# here and put through the same detectors.
test_the_credential_assertions_catch_a_leaking_run() {
  setup_case detect
  printf 'CHECK 96 RESULT\nFACT mint: PASS — the token is %s\nCHECK 96 RESULT END (exit 0)\n' "$MINTED" >"$dir/out"
  printf 'progress\n' >"$dir/err"
  printf 'curl -H "Authorization: Bearer %s" https://example.invalid\n' "$MINTED" >"$dir/state/calls"
  # The counter is a subshell's, not the suite's: the three FAIL lines this
  # provokes on purpose are counted rather than printed, because output that
  # reads FAIL beside a suite that passed is output nobody trusts.
  local caught
  caught=$(
    local caught_failures=0
    fail() { caught_failures=$((caught_failures + 1)); }
    expect_no_credential_anywhere "$dir" "the detector"
    printf '%s' "$caught_failures"
  )
  [ "${caught:-0}" -eq 3 ] \
    || fail "the detector: a leaking run raised $caught of the 3 credential assertions that must catch it (the token in the block, the token on a command line, the token prefix), so one of them is vacuous"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_happy_path
t test_dry_run_runs_nothing_that_writes
t test_a_named_repository_is_the_one_checked_and_a_lone_one_needs_no_name
t test_belong_is_refused
t test_a_repository_argument_that_is_not_owner_name_is_refused
t test_a_derivation_among_several_repositories_is_refused
t test_a_repository_that_is_not_one_with_state_is_refused
t test_a_config_that_is_not_fugaro_dev_stops
t test_a_repository_with_one_job_too_few_stops
t test_the_shared_project_job_with_the_url_is_a_fail
t test_a_job_that_has_no_service_url_while_the_config_records_one_is_a_fail
t test_the_drop_landed_is_a_pass
t test_the_drop_without_the_code_is_a_fail
t test_the_switch_hook_may_only_name_the_file_it_reads
t test_a_bundle_with_the_job_as_a_second_member_is_a_fail
t test_a_bundle_whose_one_member_is_not_the_service_account_is_a_fail
t test_a_mint_whose_answer_names_another_repository_is_a_fail
t test_a_mint_whose_answer_is_a_longer_name_ending_in_the_run_repository_is_a_fail
t test_a_mint_whose_expiry_is_outside_the_hour_is_a_fail
t test_a_service_reachable_without_iam_is_a_fail
t test_a_log_that_cannot_be_counted_is_not_a_passing_front_end_row
t test_a_mint_that_answers_other_than_200_is_a_fail
t test_a_mint_that_answers_200_with_no_token_stops
t test_a_pairing_that_is_answered_is_the_finding
t test_a_refusal_with_no_audit_line_is_the_finding
t test_a_mint_that_answered_and_left_no_line_is_a_fail
t test_an_outsider_that_is_answered_is_the_finding
t test_a_user_that_survived_the_deletions_is_a_fail
t test_a_user_list_that_cannot_be_read_back_stops_the_run
t test_a_scratch_directory_that_cannot_be_removed_is_a_fail
t test_a_line_that_carries_a_token_is_dropped_and_reported
t test_xtrace_is_refused
t test_usage
t test_the_credential_assertions_catch_a_leaking_run

if [ "$failures" -ne 0 ]; then
  echo "check96_test: $failures failure(s)" >&2
  exit 1
fi
echo "check96_test: all tests passed" >&2
