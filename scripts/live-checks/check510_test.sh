#!/usr/bin/env bash
# Tests for scripts/live-checks/check510.sh, with fake `gcloud`, `fugaro` and
# `curl` on PATH: NOTHING here reaches a cloud, a project or the network, no
# agent runs a live command, and the fakes record every call they are given so a
# case can assert what the check did — and did not — do.
#
# The fakes are a small model rather than a switchboard, because Check 510's
# rows are about IAM DECIDING something: the fake `gcloud` keeps a project
# policy (member, role, condition title and expression), the containers, and the
# throwaway service account; the fake `curl` reads that policy and answers each
# Secret Manager call the way IAM would — the caller is the member the token was
# minted for, a conditioned grant is evaluated against the resource's REAL name
# (project NUMBER and all, so a condition anchored at the project ID never
# matches, which is the trap finding (a) is about), secrets.create is granted
# only without a condition (F30), and secrets.list only without one. The modes
# below bend exactly one of those rules at a time, which is what lets a case show
# that the check notices.
#
# The role table is part of that model, and it is the reason the ORDER the check
# asks its rows in is covered here too: no predefined Secret Manager role carries
# secrets.create without secrets.list, so the unconditioned create grant the
# check writes also admits a listing — an admitted list whose body was empty
# would hide that, so the fake answers an admitted list with the names that
# exist, exactly as a real one does.
#
# Usage: bash scripts/live-checks/check510_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check510.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK510_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK510_TEST_HEAD:-900}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_empty() { # value what
  [ -z "$1" ] || fail "$2: expected nothing, got: $(printf '%s' "$1" | head -c 300)"
}

# The stamp every name this run makes carries. Pinned, so a case can name the
# container it pre-creates and read back the one a row made.
stamp=test510
prefix_u=fugaro_u_
probe_u="${prefix_u}9900000000000001_chk510a-${stamp}"
probe_p="fugaro_p_chk510shared-${stamp}"
probe_n="fugaro-chk510-${stamp}-probe"

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check510.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
version) echo "fugaro 0.21.0 (test)" ;;
doctor)
  # Faithful to internal/cli/doctor.go's --all view: the `project:` line naming
  # the installation and its GCP project, which is the whole of this check's
  # guard. The project is hard-coded in the check, so an installation that
  # doctor reports differently is refused by name.
  case "$mode" in
  other-project) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
  *) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
  esac
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check510.sh's test only. It
# keeps a project's policy, its containers and its service accounts in
# $FAKE_GCLOUD_STATE, and nothing here reaches a real project.
set -euo pipefail
state=$FAKE_GCLOUD_STATE
mode=${FAKE_MODE:-default}
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/gcloud.calls"

project=""; format=""; impersonate=""
member=""; role=""; condition=""; flatten=""; verb=""

# has_conditions: whether the policy this fake keeps carries a condition at all,
# which is what decides whether gcloud insists on --condition=None.
has_conditions() {
  awk -F'\t' 'NF >= 3 && $3 != "" { found = 1 } END { exit !found }' \
    "$state/bindings.tsv" 2>/dev/null
}

positional=()
while [ $# -gt 0 ]; do
  case "$1" in
  --project) shift; project=${1:-} ;;
  --project=*) project=${1#--project=} ;;
  --format) shift; format=${1:-} ;;
  --format=*) format=${1#--format=} ;;
  --impersonate-service-account) shift; impersonate=${1:-} ;;
  --impersonate-service-account=*) impersonate=${1#--impersonate-service-account=} ;;
  --member) shift; member=${1:-} ;;
  --member=*) member=${1#--member=} ;;
  --role) shift; role=${1:-} ;;
  --role=*) role=${1#--role=} ;;
  --condition) shift; condition=${1:-} ;;
  --condition=*) condition=${1#--condition=} ;;
  --flatten) shift; flatten=${1:-} ;;
  --flatten=*) flatten=${1#--flatten=} ;;
  --quiet | --all | --replication-policy=* | --display-name=* | --data-file=*) ;;
  --replication-policy | --display-name | --data-file) shift ;;
  -*) ;;
  *) positional+=("$1") ;;
  esac
  shift
done

sub=${positional[0]:-}
sub2=${positional[1]:-}
sub3=${positional[2]:-}

case "$sub" in
config)
  # `gcloud config get-value account`. No file: the value is unset, which is
  # what a machine nobody logged in on prints.
  if [ "$sub2" = "get-value" ] && [ "$sub3" = "account" ]; then
    case "$mode" in
    no-account) : ;;
    *) cat "$state/account" 2>/dev/null || true ;;
    esac
  fi
  ;;
auth)
  if [ "$sub2" = "print-access-token" ]; then
    [ "$mode" = token-fails ] && exit 1
    # A stand-in token, minted FOR whoever the caller asked to impersonate: the
    # fake `curl` reads the member out of it, so the check really acts as the
    # throwaway account and not as the owner.
    printf 'fake-token-for-%s\n' "${impersonate:-$project}"
  fi
  ;;
secrets)
  case "$sub2" in
  create)
    [ "$mode" = probe-create-fails ] && {
      echo "ERROR: (gcloud.secrets.create) the request failed" >&2
      exit 1
    }
    cat >/dev/null
    if [ -e "$state/secrets/$sub3" ]; then
      echo "ERROR: (gcloud.secrets.create) ALREADY_EXISTS: $sub3" >&2
      exit 1
    fi
    : >"$state/secrets/$sub3"
    ;;
  describe)
    [ -e "$state/secrets/$sub3" ] || {
      echo "ERROR: (gcloud.secrets.describe) NOT_FOUND: $sub3" >&2
      exit 1
    }
    ;;
  delete)
    case "$mode" in
    delete-does-nothing) exit 0 ;;
    esac
    rm -f "$state/secrets/$sub3"
    ;;
  list)
    local_names=$(cd "$state/secrets" && ls -1 2>/dev/null || true)
    case "$format" in
    value* | *"value("*) printf '%s\n' ${local_names:-} ;;
    *) printf '[]\n' ;;
    esac
    ;;
  esac
  ;;
iam)
  # `iam service-accounts ...` has its own word shapes, so it is read off the
  # positional list rather than guessed at above: positional[2] is the verb and
  # positional[3] the account.
  case "$sub2" in
  service-accounts)
    verb=$sub3
    sa=${positional[3]:-}
    case "$verb" in
    create)
      [ "$mode" = sa-create-fails ] && {
        echo "ERROR: (gcloud.iam.service-accounts.create) the request failed" >&2
        exit 1
      }
      if [ -e "$state/sa/$sa" ]; then
        echo "ERROR: (gcloud.iam.service-accounts.create) ALREADY_EXISTS: $sa" >&2
        exit 1
      fi
      : >"$state/sa/$sa"
      ;;
    describe)
      [ -e "$state/sa/${sa%%@*}" ] || {
        echo "ERROR: (gcloud.iam.service-accounts.describe) NOT_FOUND: $sa" >&2
        exit 1
      }
      ;;
    delete)
      case "$mode" in
      sa-delete-does-nothing) exit 0 ;;
      esac
      # gcloud takes either the email or the bare name, and this one is deleted
      # by the email the check recorded.
      rm -f "$state/sa/${sa%%@*}" "$state/sa-bindings/${sa%%@*}"
      ;;
    add-iam-policy-binding)
      printf '%s\t%s\n' "$sa" "$member" >>"$state/sa-bindings/$sa"
      ;;
    esac
    ;;
  esac
  ;;

# `projects <verb> <project-id>`: the verb is the second positional word.
projects)
  case "$sub2" in
  add-iam-policy-binding | remove-iam-policy-binding)
    verb=$sub2
    [ "$mode" = binding-add-fails ] && [ "$verb" = add-iam-policy-binding ] && {
      echo "ERROR: (gcloud.projects.add-iam-policy-binding) the request failed" >&2
      exit 1
    }
    [ "$mode" = binding-remove-fails ] && [ "$verb" = remove-iam-policy-binding ] && {
      echo "ERROR: (gcloud.projects.remove-iam-policy-binding) the request failed" >&2
      exit 1
    }
    # gcloud's own two refusals around conditions, which are what a check has to
    # get right rather than be lucky about:
    #   * a policy that carries a condition refuses an add OR a remove that does
    #     not say which one it means, unless the caller says --condition=None out
    #     loud — "Run the command again with --condition=None". Saying nothing
    #     and saying None are therefore not the same thing, and the fake tells
    #     them apart the way the real one does.
    #   * a condition has to carry BOTH a title and an expression, so a removal
    #     that names only the title is refused and removes nothing.
    if [ -z "$condition" ] && has_conditions; then
      echo "ERROR: (gcloud.projects.$verb) The policy contains bindings with conditions, so it must be specified. Run the command again with --condition=None to specify an unconditional binding." >&2
      exit 1
    fi
    if [ -z "$condition" ] || [ "$condition" = None ]; then
      title=""
      expr=""
    else
      case "$condition" in
      *,expression=*) ;;
      *)
        echo "ERROR: (gcloud.projects.$verb) argument --condition: a condition must specify both a title and an expression (got '$condition')" >&2
        exit 1
        ;;
      esac
      title=${condition#title=}
      title=${title%%,*}
      expr=${condition#*,expression=}
    fi
    if [ "$verb" = add-iam-policy-binding ]; then
      printf '%s\t%s\t%s\t%s\n' "$member" "$role" "$title" "$expr" >>"$state/bindings.tsv"
      exit 0
    fi
    # Faithful to gcloud's own matching: a conditioned binding is removed by
    # naming the WHOLE condition — the title alone matches nothing — and one
    # carrying none is removed by naming None. A --condition for a binding that
    # carries none removes nothing, which is the mistake a restore must not make.
    if [ -n "$title" ]; then
      awk -F'\t' -v m="$member" -v r="$role" -v t="$title" -v e="$expr" \
        '!($1 == m && $2 == r && $3 == t && $4 == e)' "$state/bindings.tsv" >"$state/bindings.tsv.new"
    else
      awk -F'\t' -v m="$member" -v r="$role" \
        '!($1 == m && $2 == r && $4 == "")' "$state/bindings.tsv" >"$state/bindings.tsv.new"
    fi
    mv "$state/bindings.tsv.new" "$state/bindings.tsv"
    ;;
  get-iam-policy)
    # The policy as JSON, rendered from the same table the check's own reads are
    # answered from: `title` and the member string are the two things the
    # restore looks for, so they are rendered the way the real one carries them.
    printf '{"bindings":['
    first=yes
    while IFS=$'\t' read -r bmember brole btitle bexpr; do
      [ -n "$bmember" ] || continue
      [ "$first" = yes ] || printf ','
      first=no
      printf '{"role": "%s", "members": ["%s"]' "$brole" "$bmember"
      if [ -n "$btitle" ]; then printf ', "condition": {"title": "%s", "expression": "%s"}' "$btitle" "$bexpr"; fi
      printf '}'
    done <"$state/bindings.tsv"
    printf ']}\n'
    ;;
  esac
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check510.sh's test only: it
# answers ONE Secret Manager REST call the way IAM would, from the policy the
# fake `gcloud` keeps. It prints only the status (the check asks for the body
# with -o, which is where the body goes), and it logs the method and the URL of
# every call and never a header, so nothing this fake sees can reach a log.
set -euo pipefail
state=$FAKE_GCLOUD_STATE
mode=${FAKE_MODE:-default}
method=GET
url=""
out=""
auth=""
body=""
while [ $# -gt 0 ]; do
  case "$1" in
  -X) shift; method=${1:-} ;;
  -o) shift; out=${1:-} ;;
  -w) shift ;;
  -sS | -sSf | -s | -S | -f) ;;
  -H)
    shift
    case "${1:-}" in Authorization:*) auth=${1#Authorization: } ;; esac
    ;;
  --data-binary | --data | -d) shift; body=${1:-} ;;
  --header) shift ;;
  http://* | https://*) url=$1 ;;
  esac
  shift
done
printf 'curl %s %s\n' "$method" "$url" >>"$FAKE_LOG_DIR/curl.calls"

rest=${url#*/v1/projects/}
project=${rest%%/*}
rest=${rest#*/}
name=""
perm=""
case "$rest" in
secrets\?secretId=*)
  perm=secrets.create
  name=${rest#secrets?secretId=}
  ;;
secrets\?*) perm=secrets.list ;;
secrets/*:addVersion)
  perm=secretmanager.versions.add
  name=${rest#secrets/}
  name=${name%:addVersion}
  ;;
secrets/*)
  perm=secretmanager.secrets.delete
  name=${rest#secrets/}
  ;;
esac

# The project the API answers with, and the one a condition is matched against:
# a Secret Manager resource name carries the project NUMBER, never the ID that
# stands in the URL, so the two are deliberately different here. Matching a
# condition against `projects/<ID>/secrets/…` — the mistake finding (a) is about,
# and the shape this check itself wrote before it was fixed — then matches
# nothing, exactly as it does in the sandbox.
number=$(cat "$state/project-number" 2>/dev/null || printf '%s' "$project")
resource="projects/${number}/secrets/${name}"

caller=""
case "$auth" in
"Bearer fake-token-for-"*) caller=${auth#Bearer fake-token-for-} ;;
esac

emit() { # emit STATUS BODY
  if [ -n "$out" ]; then printf '%s' "$2" >"$out"; fi
  printf '%s' "$1"
}

denied() { # denied STATUS DETAIL
  emit "$1" "{\"error\":{\"code\":$1,\"message\":\"$2\",\"status\":\"PERMISSION_DENIED\"}}"
  exit 0
}

# role_grants ROLE PERMISSION: the project's own predefined roles, as the
# permissions this check's rows are about sit in them. The table is faithful to
# the two things the check's own reads depend on: every role that carries
# secrets.create also carries secrets.list (there is no create-only Secret
# Manager role — roles/secretmanager.admin and roles/secretmanager.editor are the
# two that carry create, and both carry list), and secretmanager.versions.add and
# secrets.delete sit in admin and in secretVersionAdder, the two roles the check
# conditions on.
role_grants() {
  case "$1/$2" in
  roles/secretmanager.admin/secretmanager.versions.add | \
    roles/secretmanager.admin/secretmanager.secrets.delete | \
    roles/secretmanager.admin/secrets.create | \
    roles/secretmanager.admin/secrets.list | \
    roles/secretmanager.secrets.versionAdder/secretmanager.versions.add | \
    roles/secretmanager.secrets.versionAdder/secretmanager.secrets.delete | \
    roles/secretmanager.viewer/secrets.list) return 0 ;;
  esac
  return 1
}

# condition_allows_name EXPR RESOURCE: whether a grant's condition admits the
# resource IAM evaluates the permission on — the REAL resource name, project
# number and all, never the ID the URL carries. This is the reading U3 is about,
# and the one DS2.2's expression has to survive.
#
# The two clause shapes a name-prefix condition comes in are read apart, because
# they do NOT mean the same thing:
#   * resource.name.extract("/secrets/{secret}").startsWith("p") matches on the
#     secret's own segment, wherever the name comes from;
#   * resource.name.startsWith("projects/<what>/secrets/p") matches the whole
#     string, so it matches only if <what> is the project NUMBER — a clause
#     written with the project ID is a clause that names nothing.
condition_allows_name() {
  local expr=$1 resource=$2 clause rest p
  local segment=${resource#*/secrets/}
  segment=${segment%%/*}
  rest=$expr
  while :; do
    case "$rest" in
    *' || '*) clause=${rest%% || *}; rest=${rest#* || } ;;
    *) clause=$rest; rest="" ;;
    esac
    p=$(printf '%s' "$clause" | sed -n 's/.*startsWith("\([^"]*\)").*/\1/p')
    if [ -n "$p" ]; then
      case "$clause" in
      *'extract("/secrets/{secret}")'*)
        case "$segment" in "$p"*) return 0 ;; esac
        ;;
      *)
        case "$resource" in "$p"*) return 0 ;; esac
        ;;
      esac
    fi
    [ -n "$rest" ] || break
  done
  return 1
}

if [ -z "$caller" ]; then
  emit 401 '{"error":{"code":401,"message":"Request had invalid authentication credentials","status":"UNAUTHENTICATED"}}'
  exit 0
fi
if [ -z "$perm" ]; then
  emit 400 '{"error":{"code":400,"message":"unknown request","status":"INVALID_ARGUMENT"}}'
  exit 0
fi
# The mode that makes the allow row never visible: nothing this fake is told
# grants versions.add, so the check has to report that it could not measure.
if [ "$mode" = no-propagation ] && [ "$perm" = secretmanager.versions.add ]; then
  denied 403 "Permission 'secretmanager.versions.add' denied on resource (or it may not exist)"
fi

allowed=no
conditional_seen=no
if [ -f "$state/bindings.tsv" ]; then
  while IFS=$'\t' read -r bmember brole btitle bexpr; do
    [ -n "$bmember" ] || continue
    if [ "$bmember" != "serviceAccount:$caller" ]; then continue; fi
    if ! role_grants "$brole" "$perm"; then continue; fi
    if [ -z "$bexpr" ]; then
      allowed=yes
      break
    fi
    conditional_seen=yes
    case "$perm" in
    secrets.create)
      # F30's reading: the check is made where the secret does not exist yet, so
      # a condition on its name cannot admit a create. Only the mode that bends
      # that rule admits it, and the check has to say so.
      if [ "$mode" = create-conditioned-admitted ] && condition_allows_name "$bexpr" "$name"; then
        allowed=yes
        break
      fi
      ;;
    secrets.list)
      if [ "$mode" = list-admits-some ]; then
        allowed=yes
        break
      fi
      ;;
    secretmanager.versions.add)
      # versions.add is evaluated on the SecretVersion, so the resource the
      # condition is matched against carries the version segment too.
      if [ "$mode" = condition-ignored-on-add ] || condition_allows_name "$bexpr" "$resource/versions/1"; then
        allowed=yes
        break
      fi
      ;;
    secretmanager.secrets.delete)
      if [ "$mode" = condition-ignored-on-delete ] || condition_allows_name "$bexpr" "$resource"; then
        allowed=yes
        break
      fi
      ;;
    esac
  done <"$state/bindings.tsv"
fi
# A create under an UNCONDITIONED create-bearing grant that the mode refuses: the row for it
# then reads a refusal where an admission is expected, which is a not-measured.
if [ "$perm" = secrets.create ] && [ "$mode" = create-not-unconditioned ]; then
  allowed=no
fi
if [ "$allowed" != yes ]; then
  if [ "$perm" = secretmanager.secrets.delete ] && [ "$mode" = deny-delete-500 ]; then
    emit 500 '{"error":{"code":500,"message":"internal error while checking permission","status":"INTERNAL"}}'
    exit 0
  fi
  denied 403 "Permission '$perm' denied on resource '$resource' (or it may not exist)"
fi

case "$perm" in
secretmanager.versions.add)
  if [ ! -e "$state/secrets/$name" ]; then
    emit 404 "{\"error\":{\"code\":404,\"message\":\"Secret [$name] not found\",\"status\":\"NOT_FOUND\"}}"
    exit 0
  fi
  emit 200 "{\"name\":\"$resource/versions/1\"}"
  ;;
secretmanager.secrets.delete)
  if [ ! -e "$state/secrets/$name" ]; then
    emit 404 "{\"error\":{\"code\":404,\"message\":\"Secret [$name] not found\",\"status\":\"NOT_FOUND\"}}"
    exit 0
  fi
  # The mode that says the status and the reading disagree: the call answers a
  # delete and the container is still there, which the check has to report as a
  # finding about itself rather than as a pass.
  if [ "$mode" != "delete-leaves-the-name" ]; then rm -f "$state/secrets/$name"; fi
  emit 200 '{}'
  ;;
secrets.create)
  max=${FAKE_MAX_ID_LENGTH:-255}
  # The ID-length limit is applied to the bisection's own names, and to nothing
  # else: every one of them grows out of one root, so a name that starts with it
  # or is one of its own leading truncations belongs to that family. A limit that
  # applied to every name would refuse the check's own probes, which are long
  # because they carry the prefixes; the sandbox's real limit is 255 and they fit
  # inside it.
  length_family=chk510l-
  in_family=no
  case "$name" in
  "$length_family"*) in_family=yes ;;
  *)
    case "$length_family" in "$name"*) in_family=yes ;; esac
    ;;
  esac
  if [ "$in_family" = yes ] && [ "${#name}" -gt "$max" ]; then
    emit 400 "{\"error\":{\"code\":400,\"message\":\"Secret ID must be no more than $max characters long\",\"status\":\"INVALID_ARGUMENT\"}}"
    exit 0
  fi
  if [ -e "$state/secrets/$name" ]; then
    emit 409 "{\"error\":{\"code\":409,\"message\":\"Secret [$name] already exists\",\"status\":\"ALREADY_EXISTS\"}}"
    exit 0
  fi
  : >"$state/secrets/$name"
  emit 200 "{\"name\":\"$resource\"}"
  ;;
secrets.list)
  # The listing is the containers that exist, and it is written for every caller
  # this fake has ADMITTED: a real service returns the names to whoever the
  # policy admits, and the row's reading depends on that. It is what makes the
  # check's own ordering observable — every role carrying secrets.create also
  # carries secrets.list, so a list row asked AFTER the unconditioned create
  # grant is admitted and reads these names.
  names=$(cd "$state/secrets" && ls -1 2>/dev/null || true)
  first=yes
  {
    printf '{"secrets":['
    for n in ${names:-}; do
      [ "$first" = yes ] || printf ','
      first=no
      # The project NUMBER is what a real listing carries, too.
      printf '{"name":"projects/%s/secrets/%s"}' "$number" "$n"
    done
    printf ']}'
  } >"$state/list.json"
  if [ -n "$out" ]; then cp "$state/list.json" "$out"; fi
  printf '200'
  ;;
esac
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------- the runs
run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_MAX_ID_LENGTH="${FAKE_MAX_ID_LENGTH:-255}" \
    CHECK510_STAMP="$stamp" \
    CHECK510_IAM_WAIT_SECONDS=0 CHECK510_IAM_POLL_SECONDS=0 \
    CHECK510_LENGTH_MAX_PROBE="${CHECK510_LENGTH_MAX_PROBE:-256}" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state/secrets" "$1/state/sa" "$1/state/sa-bindings"
  : >"$1/state/bindings.tsv"
  printf 'check510-owner@example.com\n' >"$1/state/account"
  # The project's NUMBER, which is what a Secret Manager resource name carries
  # while the REST URL carries the ID: a condition anchored at the ID matches
  # nothing at all. Deliberately unlike the ID, so a fake that matched them up
  # would let an ID-form condition look like a working fence.
  printf '100000000001\n' >"$1/state/project-number"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
}

# what_is_left DIR: everything the check made that is still there, so a case can
# say that its own cleanup (and not the test's) removed it.
what_is_left() { # dir
  local dir=$1 out=""
  out+=$(cd "$dir/state/secrets" && ls -1 2>/dev/null | tr '\n' ' ' || true)
  out+=$(grep -c . "$dir/state/bindings.tsv" 2>/dev/null | tr -d '\n' || true)
  out+=" binding line(s) "
  out+=$(cd "$dir/state/sa" && ls -1 2>/dev/null | tr '\n' ' ' || true)
  printf '%s' "$out"
}

assert_clean() { # dir what
  local left
  left=$(what_is_left "$1")
  case "$left" in
  "0 binding line(s) ") ;;
  *) fail "$2: the check left things behind: $left" ;;
  esac
}

assert_no_writes() { # dir what
  local calls=$1/logs/gcloud.calls
  expect_absent "$(cat "$calls")" "add-iam-policy-binding" "$2: no binding is written"
  expect_absent "$(cat "$calls")" " secrets create " "$2: no container is created"
  expect_absent "$(cat "$calls")" " service-accounts create " "$2: no account is made"
  expect_absent "$(cat "$1/logs/curl.calls")" ":addVersion" "$2: no Secret Manager call is made"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check510.sh --dry-run" "--help: the dry run is offered"
  # --help is not a run: it prints its own usage and no block. What the header
  # text it prints MENTIONS includes the block's own end line, so what must be
  # absent is a fact line, which only a run writes.
  expect_absent "$out" "FACT " "--help: no block is printed"
  assert_no_writes "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK510_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 510 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "dry run: the hard-coded sandbox target"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore"
  expect_contains "$out" "nothing of this check's is left" "dry run: nothing to delete"
  expect_contains "$out" "CHECK 510 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT condition-version-add-deny: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT secret-id-length: INFO — planned" "dry run: a planned measurement is INFO"
  expect_contains "$err" "the length bisection's creates" "dry run: the plan says what it would create"
  assert_no_writes "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT condition-version-add-allow: PASS" "happy path: the conditioned allow row"
  expect_contains "$out" "FACT condition-prefix-allow: PASS" "happy path: the second prefix"
  expect_contains "$out" "FACT condition-version-add-deny: PASS" "happy path: the refusal"
  expect_contains "$out" "FACT condition-delete-allow: PASS" "happy path: the conditioned delete"
  expect_contains "$out" "FACT condition-delete-deny: PASS" "happy path: the refused delete"
  expect_contains "$out" "FACT create-conditioned: PASS" "happy path: the conditioned create is refused"
  expect_contains "$out" "FACT create-unconditioned: PASS" "happy path: the unconditioned create is admitted"
  expect_contains "$out" "FACT list-condition-admits-nothing: PASS" "happy path: the conditioned list admits nothing"
  expect_contains "$out" "the longest admitted secret ID is 255 characters and 256 was refused" "happy path: the length bisection"
  expect_contains "$out" "FACT restore: PASS" "happy path: the restore"
  expect_contains "$out" "CHECK 510 RESULT END (exit 0)" "happy path: the end line"
  # The rows really acted as the throwaway account, and really through the API:
  # the requests come from the token minted for the account, one call per row.
  local calls
  calls=$(cat "$dir/logs/curl.calls")
  expect_contains "$calls" "curl POST https://secretmanager.googleapis.com/v1/projects/fugaro-dev/secrets/${probe_u}:addVersion" "happy path: the allow row's call"
  expect_contains "$calls" "curl POST https://secretmanager.googleapis.com/v1/projects/fugaro-dev/secrets/${probe_p}:addVersion" "happy path: the shared prefix's call"
  expect_contains "$calls" "curl DELETE https://secretmanager.googleapis.com/v1/projects/fugaro-dev/secrets/fugaro-chk510-${stamp}-probe2" "happy path: the refused delete's call"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gcloud --project=fugaro-dev --quiet iam service-accounts create chk510-${stamp}" "happy path: the throwaway account"
  # Everything the check made is gone, and the check's own restore is what says
  # so: a case that cleaned up itself would hide a restore that did not.
  assert_clean "$dir" "happy path"
  [ ! -e "$scratch" ] || fail "happy path: the scratch directory $scratch is still there"
  # The block carries no credential: the impersonated token is never printed.
  expect_absent "$out" "fake-token-for-" "happy path: no token in the block"
  expect_absent "$err" "fake-token-for-" "happy path: no token on stderr"
}

test_the_roles_it_grants_are_a_closed_set_of_predefined_roles() {
  # A fake cannot tell a real role from a fictional one: gcloud is the only
  # oracle for whether a role exists, and this is hermetic. What this pins is
  # that the roles the check grants are a closed set, named here, so a fourth one
  # is an edit in two places rather than a name the fake quietly accepts — as
  # `roles/secretmanager.creator` was. Secret Manager has six predefined roles
  # and none of them carries secrets.create on its own.
  local dir=$work/roles
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the roles it grants"
  local roles distinct
  roles=$(grep -o 'role=[^ ]*' "$dir/logs/gcloud.calls" | sort -u | tr '\n' ' ')
  expect_contains "$roles" "role=roles/secretmanager.admin" "the roles it grants: the conditioned grant and the unconditioned create grant"
  expect_contains "$roles" "role=roles/secretmanager.viewer" "the roles it grants: the listing control"
  expect_contains "$roles" "role=roles/iam.serviceAccountTokenCreator" "the roles it grants: the impersonation grant"
  distinct=$(grep -o 'role=[^ ]*' "$dir/logs/gcloud.calls" | sort -u | grep -c . || true)
  [ "$distinct" = 3 ] || fail "the roles it grants: expected exactly three roles, got (${distinct}): ${roles}"
  # F30's unconditioned half: exactly one binding, roles/secretmanager.admin,
  # and it says --condition=None — the policy already carries the two conditioned
  # grants by then, so an add that stayed silent about the condition would be
  # refused by gcloud and the check would stop before its create row.
  local unconditioned
  unconditioned=$(grep 'projects add-iam-policy-binding' "$dir/logs/gcloud.calls" | grep -- '--condition=None' || true)
  expect_contains "$unconditioned" "--role=roles/secretmanager.admin" "the unconditioned grant: roles/secretmanager.admin"
  [ "$(printf '%s\n' "$unconditioned" | grep -c .)" = 1 ] || fail "the unconditioned grant: expected exactly one binding added with --condition=None, got: ${unconditioned}"
}

test_the_bindings_are_removed_with_the_whole_condition() {
  # The removal form, pinned because getting it wrong is silent: gcloud matches a
  # removal on the WHOLE condition, refuses a condition that carries no
  # expression, and refuses a removal from a policy that has conditions unless
  # --condition=None names the unconditional binding. The check's `|| true`
  # swallows whatever gcloud says, so a wrong form shows up only as a grant left
  # on the owner's project and a `restore: FAIL` — which the cases above catch,
  # and which this one makes legible as the form rather than the outcome.
  local dir=$work/removal removes
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the removal form"
  removes=$(grep 'projects remove-iam-policy-binding' "$dir/logs/gcloud.calls" || true)
  # The whole condition, and the whole of what makes it match: gcloud matches a
  # removal on title AND expression, and the expression has to be the one the add
  # wrote — the extraction anchor and not the project-ID form, which matches
  # nothing (finding (a)).
  expect_contains "$removes" "--role=roles/secretmanager.admin --condition=title=chk510-admin-${stamp},expression=resource.name.extract(\"/secrets/{secret}\").startsWith(\"${prefix_u}\")" "the removal form: the conditioned admin grant's whole condition"
  expect_contains "$removes" "--role=roles/secretmanager.viewer --condition=title=chk510-viewer-${stamp},expression=resource.name.extract(\"/secrets/{secret}\").startsWith(\"${prefix_u}\")" "the removal form: the conditioned viewer grant's whole condition"
  expect_contains "$removes" "--role=roles/secretmanager.admin --condition=None" "the removal form: the unconditioned grant, named with None"
  [ "$(printf '%s\n' "$removes" | grep -c .)" = 3 ] || fail "the removal form: expected exactly three project removals, got: ${removes}"
}

# condition_with_the_project_id: a copy of the check whose condition is anchored
# at the project ID — the form finding (a) is about, and the one this check wrote
# before it was fixed. The copy is the whole script with ONE function redefined,
# because a later definition wins: everything up to the script's own last line
# (which runs main), the replacement, and then that line.
condition_with_the_project_id() { # condition_with_the_project_id COPY
  head -n -1 "$script" >"$1"
  cat >>"$1" <<'ID_FORM_EOF'

condition_expression() {
  printf 'resource.name.startsWith("projects/%s/secrets/%s") || resource.name.startsWith("projects/%s/secrets/%s")' \
    "$P" "$PREFIX_U" "$P" "$PREFIX_P"
}
ID_FORM_EOF
  printf 'main "$@"\n' >>"$1"
}

test_a_condition_written_with_the_project_id_is_not_measured() {
  # A condition anchored at projects/<project-ID>/secrets/… matches NOTHING: a
  # secret's resource.name carries the project NUMBER (the same trap
  # deploy/terraform/gcp/modules/installation/deployer.tf records). Written that
  # way, every row the grant feeds times out — and the dangerous reading is the
  # other one, that the condition does not apply to versions.add at all and U3 is
  # answered no, which would send the owner back to DS2.2 with a live defect that
  # is this check's own. So the run has to end as one that could NOT measure, with
  # the number trap named and the expression it wrote printed, and never with the
  # finding's own words.
  local dir=$work/id-form copy=$work/id-form.sh saved=$script
  prepare "$dir"
  condition_with_the_project_id "$copy"
  script=$copy
  run_check "$dir"
  script=$saved
  expect_status "$status" 2 "a condition anchored at the project ID"
  expect_contains "$out" "FACT condition-version-add-allow: FAIL" "a condition anchored at the project ID: the allow row"
  expect_contains "$out" "never admitted versions.add" "a condition anchored at the project ID: the row says what happened"
  expect_contains "$out" "carries the project NUMBER" "a condition anchored at the project ID: the trap is named"
  expect_contains "$out" "projects/fugaro-dev/secrets/fugaro_u_" "a condition anchored at the project ID: the expression this run wrote is printed"
  expect_contains "$out" "not measured: the conditioned allow row never showed the grant live" "a condition anchored at the project ID: the rows that depend on it"
  expect_absent "$out" "DID NOT REFUSE" "a condition anchored at the project ID: U3's finding is not claimed"
  expect_contains "$err" "could NOT measure" "a condition anchored at the project ID: the exit is a not-measured"
  assert_clean "$dir" "a condition anchored at the project ID"
}

test_no_human_output_carries_the_token() {
  # The check's own promise, and the reason the xtrace guard exists: nothing it
  # prints may carry the token it mints. Read from a run that went all the way
  # through, block and progress alike.
  local dir=$work/token
  prepare "$dir"
  run_check "$dir"
  expect_absent "$out$err" "fake-token-for-" "the token never reaches the output"
  expect_absent "$out$err" "Authorization: Bearer" "no authorization header is printed"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  assert_no_writes "$dir" "bash -x"
}

test_a_foreign_installation_is_refused() {
  local dir=$work/foreign
  prepare "$dir"
  run_check "$dir" FAKE_MODE=other-project
  expect_status "$status" 1 "another installation"
  expect_contains "$err" "not the fugaro-dev installation" "another installation: the guard"
  assert_no_writes "$dir" "another installation"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  printf 'check510-owner@belong.example\n' >"$dir/state/account"
  run_check "$dir"
  expect_status "$status" 1 "an account naming belong"
  expect_contains "$err" "never run there" "belong: the refusal"
  assert_no_writes "$dir" "belong"
}

test_no_account_is_refused() {
  local dir=$work/noaccount
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-account
  expect_status "$status" 2 "no account configured"
  expect_contains "$err" "no account" "no account: the message"
  assert_no_writes "$dir" "no account"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

test_a_container_already_there_stops_the_check() {
  # The check never adopts and never overwrites: a container under one of its own
  # names is a hard stop BEFORE anything is written.
  local dir=$work/adopt
  prepare "$dir"
  : >"$dir/state/secrets/${probe_n}"
  run_check "$dir"
  expect_status "$status" 1 "a container that is already there"
  expect_contains "$err" "already exists in fugaro-dev" "a container that is already there: the message"
  expect_contains "$err" "never adopts" "a container that is already there: why it stops"
  assert_no_writes "$dir" "a container that is already there"
  # The pre-existing container is the test's, so the check must leave it exactly
  # where it was.
  [ -e "$dir/state/secrets/${probe_n}" ] || fail "a container that is already there: the check deleted it"
}

test_an_account_already_there_stops_the_check() {
  local dir=$work/adopt-sa
  prepare "$dir"
  : >"$dir/state/sa/chk510-${stamp}"
  run_check "$dir"
  expect_status "$status" 1 "an account that is already there"
  expect_contains "$err" "already exists in fugaro-dev" "an account that is already there: the message"
  assert_no_writes "$dir" "an account that is already there"
}

test_a_condition_ignored_on_add_is_a_failure() {
  # U3's finding, if it were one: the condition admits versions.add on a name it
  # does not name, so the fence R3 and D1931 lean on is not a fence.
  local dir=$work/ignored-add
  prepare "$dir"
  run_check "$dir" FAKE_MODE=condition-ignored-on-add
  expect_status "$status" 1 "a condition IAM ignores for versions.add"
  expect_contains "$out" "FACT condition-version-add-deny: FAIL" "a condition IAM ignores for versions.add: the row"
  expect_contains "$out" "DID NOT REFUSE" "a condition IAM ignores for versions.add: what it means"
  expect_contains "$out" "returns to the owner" "a condition IAM ignores for versions.add: what follows"
  assert_clean "$dir" "a condition IAM ignores for versions.add"
}

test_a_condition_ignored_on_delete_is_a_failure() {
  local dir=$work/ignored-delete
  prepare "$dir"
  run_check "$dir" FAKE_MODE=condition-ignored-on-delete
  expect_status "$status" 1 "a condition IAM ignores for secrets.delete"
  expect_contains "$out" "FACT condition-delete-deny: FAIL" "a condition IAM ignores for secrets.delete: the row"
  expect_contains "$out" "an offboarding delete is not fenced" "a condition IAM ignores for secrets.delete: what it means"
  assert_clean "$dir" "a condition IAM ignores for secrets.delete"
}

test_a_delete_the_condition_admits_is_a_failure_when_the_name_stays() {
  # The status and the reading disagreeing is a finding about the check, and it
  # is reported as one rather than as a pass.
  local dir=$work/delete-lies
  prepare "$dir"
  run_check "$dir" FAKE_MODE=delete-leaves-the-name
  expect_status "$status" 1 "a delete that did not delete"
  expect_contains "$out" "is STILL THERE" "a delete that did not delete: the row"
  assert_clean "$dir" "a delete that did not delete"
}

test_a_delete_refused_with_something_other_than_403_is_a_failure() {
  local dir=$work/delete-500
  prepare "$dir"
  run_check "$dir" FAKE_MODE=deny-delete-500
  expect_status "$status" 1 "a refusal that was not a 403"
  expect_contains "$out" "FACT condition-delete-deny: FAIL" "a refusal that was not a 403: the row"
  expect_contains "$out" "did NOT measure the condition" "a refusal that was not a 403: why"
}

test_a_conditioned_create_that_is_admitted_is_a_failure() {
  # F30 is the reason DS2.2 grants create unconditioned. If a conditioned create
  # were admitted, the plan's own reason for that would be gone.
  local dir=$work/create-cond
  prepare "$dir"
  run_check "$dir" FAKE_MODE=create-conditioned-admitted
  expect_status "$status" 1 "a conditioned create that was admitted"
  expect_contains "$out" "FACT create-conditioned: FAIL" "a conditioned create that was admitted: the row"
  expect_contains "$out" "Report this reading to the owner" "a conditioned create that was admitted: what follows"
  assert_clean "$dir" "a conditioned create that was admitted"
}

test_a_create_that_is_not_admitted_unconditioned_is_unmeasured() {
  local dir=$work/create-no
  prepare "$dir"
  run_check "$dir" FAKE_MODE=create-not-unconditioned
  expect_status "$status" 2 "an unconditioned create that is refused"
  expect_contains "$out" "FACT create-unconditioned: FAIL" "an unconditioned create that is refused: the row"
  expect_contains "$err" "could NOT measure" "an unconditioned create that is refused: the exit is a not-measured"
  assert_clean "$dir" "an unconditioned create that is refused"
}

test_a_conditioned_list_that_admits_something_is_a_failure() {
  local dir=$work/list
  prepare "$dir"
  run_check "$dir" FAKE_MODE=list-admits-some
  expect_status "$status" 1 "a conditioned list that listed"
  expect_contains "$out" "FACT list-condition-admits-nothing: FAIL" "a conditioned list that listed: the row"
  expect_contains "$out" "returns to the owner" "a conditioned list that listed: what follows"
}

test_a_grant_that_never_propagates_is_not_measured() {
  # A refusal beside a grant nobody has shown to be live is not the condition
  # refusing, so every row that depends on it is reported as not measured and
  # the check ends as one that could not measure. The cleanup still runs.
  local dir=$work/noprop
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-propagation
  expect_status "$status" 2 "a grant that never became visible"
  expect_contains "$out" "FACT condition-version-add-allow: FAIL" "a grant that never became visible: the allow row"
  expect_contains "$out" "never admitted versions.add" "a grant that never became visible: the reading"
  expect_contains "$out" "not measured: the conditioned allow row never showed the grant live" "a grant that never became visible: the rows that depend on it"
  expect_contains "$err" "could NOT measure" "a grant that never became visible: the exit"
  assert_clean "$dir" "a grant that never became visible"
  [ ! -e "$scratch" ] || fail "a grant that never became visible: the scratch directory is still there"
}

test_the_length_bisection_reads_the_limit() {
  # The limit is measured rather than assumed: with the fake admitting twenty
  # characters and refusing twenty-one, the row has to name twenty — and every
  # container the bisection made has to be gone again. The bisection starts at
  # the stamped prefix's own length, so every probe it writes carries the stamp.
  local dir=$work/length line
  prepare "$dir"
  run_check "$dir" FAKE_MAX_ID_LENGTH=20 CHECK510_LENGTH_MAX_PROBE=32
  expect_status "$status" 0 "the length bisection"
  expect_contains "$out" "FACT secret-id-length: PASS" "the length bisection: the row"
  expect_contains "$out" "the longest admitted secret ID is 20 characters and 21 was refused" "the length bisection: the limit it read"
  # The detail beside "was refused" is the REFUSAL's own body. The bisection's
  # last call is usually an admitted one, so a row that reads the body of the
  # last call puts a created container's name under a sentence claiming a
  # refusal — and this row is one the owner pastes into the register.
  line=$(printf '%s\n' "$out" | grep '^FACT secret-id-length:' | head -n 1)
  expect_contains "$line" "Secret ID must be no more than 20 characters long" "the length bisection: the refusal's own message"
  expect_absent "$line" '"name":"projects/' "the length bisection: no admitted create's body under the refusal"
  # Every probe is the check's own stamped name: an unstamped `l`/`l…aaa` shape
  # is a name a previous run could have left behind, whose 409 would then read as
  # the limit.
  expect_contains "$(cat "$dir/logs/curl.calls")" "secretId=chk510l-${stamp}-" "the length bisection: the probes carry this run's stamp"
  assert_clean "$dir" "the length bisection"
}

test_a_length_above_the_probe_is_unmeasured() {
  local dir=$work/length-high
  prepare "$dir"
  run_check "$dir" FAKE_MAX_ID_LENGTH=1000 CHECK510_LENGTH_MAX_PROBE=32
  expect_status "$status" 2 "a limit above the probe"
  expect_contains "$out" "the limit is above CHECK510_LENGTH_MAX_PROBE=32" "a limit above the probe: the row says what to raise"
  assert_clean "$dir" "a limit above the probe"
}

test_a_probe_of_no_more_than_the_checks_prefix_is_unmeasured() {
  # CHECK510_LENGTH_MAX_PROBE is a knob, and one set at or below the length of
  # the check's own stamped prefix leaves the bisection with no range at all:
  # that is a not-measured, never a limit read off an empty search.
  local dir=$work/length-low
  prepare "$dir"
  run_check "$dir" CHECK510_LENGTH_MAX_PROBE=8
  expect_status "$status" 2 "a probe length below the check's prefix"
  expect_contains "$out" "is not above the 16-character stamped probe" "a probe length below the check's prefix: the row says what to raise"
  assert_clean "$dir" "a probe length below the check's prefix"
}

test_a_length_probe_refused_for_another_reason_is_not_measured() {
  # Finding (b): a create refused for a reason that is NOT the length limit — the
  # 409 for a name that already exists, which a midpoint probe can hit when an
  # earlier run's restore failed — must not be read as the limit refusing, which
  # would PASS the row with a limit nobody measured. The bisection between 16 and
  # 64 characters asks for a 28-character name of its own, so a case can put a
  # container under exactly that name and read what the row does with the 409.
  local dir=$work/length-409
  prepare "$dir"
  : >"$dir/state/secrets/chk510l-${stamp}-aaaaaaaaaaaa"
  run_check "$dir" FAKE_MAX_ID_LENGTH=30 CHECK510_LENGTH_MAX_PROBE=64
  expect_status "$status" 2 "a length probe refused for another reason"
  expect_contains "$out" "FACT secret-id-length: FAIL" "a length probe refused for another reason: the row"
  expect_contains "$out" "answered 409 and not 200 or 400" "a length probe refused for another reason: the status it read"
  expect_contains "$out" "is not the length limit" "a length probe refused for another reason: why it stopped"
  expect_absent "$out" "FACT secret-id-length: PASS" "a length probe refused for another reason: no limit is claimed"
  expect_contains "$err" "could NOT measure" "a length probe refused for another reason: the exit is a not-measured"
  # The probes carry this run's own stamp: a name a previous run could have left
  # behind is a name whose 409 would look like a limit.
  expect_contains "$(cat "$dir/logs/curl.calls")" "secretId=chk510l-${stamp}-aaaaaaaaaaaaaaaa" "a length probe refused for another reason: the probes carry this run's stamp"
  # The container the case put there is the case's, and the check's own admitted
  # probe is gone: the row stopped the bisection, not the restore.
  [ -e "$dir/state/secrets/chk510l-${stamp}-aaaaaaaaaaaa" ] || fail "a length probe refused for another reason: the check deleted the container that was already there"
  expect_empty "$(cd "$dir/state/secrets" && ls -1 | grep -v "^chk510l-${stamp}-aaaaaaaaaaaa$" || true)" "a length probe refused for another reason: the check's admitted probe is gone"
}

test_a_container_the_restore_cannot_delete_is_a_failure() {
  local dir=$work/restore-secret
  prepare "$dir"
  run_check "$dir" FAKE_MODE=delete-does-nothing
  expect_status "$status" 1 "a container the restore could not delete"
  expect_contains "$out" "FACT restore: FAIL" "a container the restore could not delete: the row"
  expect_contains "$out" "delete them yourself" "a container the restore could not delete: what to do"
}

test_a_binding_the_restore_cannot_remove_is_a_failure() {
  local dir=$work/restore-binding
  prepare "$dir"
  run_check "$dir" FAKE_MODE=binding-remove-fails
  expect_status "$status" 1 "a binding the restore could not remove"
  expect_contains "$out" "FACT restore: FAIL" "a binding the restore could not remove: the row"
  expect_contains "$out" "binding(serviceAccount:chk510-${stamp}@fugaro-dev.iam.gserviceaccount.com" "a binding the restore could not remove: the row names it"
}

test_an_account_that_cannot_be_created_is_not_measured() {
  local dir=$work/sa-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=sa-create-fails
  expect_status "$status" 2 "an account that could not be created"
  expect_contains "$err" "could not be created" "an account that could not be created: the message"
  assert_clean "$dir" "an account that could not be created"
}

test_a_token_that_cannot_be_minted_stops_the_check() {
  local dir=$work/no-token
  prepare "$dir"
  run_check "$dir" FAKE_MODE=token-fails
  expect_status "$status" 2 "a token that could not be minted"
  expect_contains "$err" "could not mint an access token" "a token that could not be minted: the message"
  # Nothing is measured on the account's behalf, so nothing of the check's is
  # created: the probes are made after the token, not before it.
  expect_absent "$(cat "$dir/logs/gcloud.calls")" " secrets create " "a token that could not be minted: no container is created"
  expect_absent "$(cat "$dir/logs/curl.calls")" ":addVersion" "a token that could not be minted: no call is made"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone (a solo rerun of one that
# looked flaky). Neither set: all of them run.
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

t test_help
t test_dry_run
t test_happy_path
t test_the_roles_it_grants_are_a_closed_set_of_predefined_roles
t test_the_bindings_are_removed_with_the_whole_condition
t test_a_condition_written_with_the_project_id_is_not_measured
t test_no_human_output_carries_the_token
t test_xtrace_refused
t test_a_foreign_installation_is_refused
t test_belong_is_refused_by_name
t test_no_account_is_refused
t test_an_unknown_argument_is_refused
t test_a_container_already_there_stops_the_check
t test_an_account_already_there_stops_the_check
t test_a_condition_ignored_on_add_is_a_failure
t test_a_condition_ignored_on_delete_is_a_failure
t test_a_delete_the_condition_admits_is_a_failure_when_the_name_stays
t test_a_delete_refused_with_something_other_than_403_is_a_failure
t test_a_conditioned_create_that_is_admitted_is_a_failure
t test_a_create_that_is_not_admitted_unconditioned_is_unmeasured
t test_a_conditioned_list_that_admits_something_is_a_failure
t test_a_grant_that_never_propagates_is_not_measured
t test_the_length_bisection_reads_the_limit
t test_a_length_above_the_probe_is_unmeasured
t test_a_probe_of_no_more_than_the_checks_prefix_is_unmeasured
t test_a_length_probe_refused_for_another_reason_is_not_measured
t test_a_container_the_restore_cannot_delete_is_a_failure
t test_a_binding_the_restore_cannot_remove_is_a_failure
t test_an_account_that_cannot_be_created_is_not_measured
t test_a_token_that_cannot_be_minted_stops_the_check

if [ "$failures" -ne 0 ]; then
  echo "check510_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check510_test.sh: ok"
