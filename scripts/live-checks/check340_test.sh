#!/usr/bin/env bash
# Tests for scripts/live-checks/check340.sh, against a fake gcloud on PATH:
# nothing here reaches the network, there is no cloud project and no bucket, and
# no credential is read, made or printed.
#
# The fake models the one thing this check is about. It holds a bucket with the
# objects the run seeds, IAM bindings whose conditions it really evaluates, and a
# service account that really exists and really goes away when it is deleted — so
# a listing is allowed or refused by the clause the run's own condition carries,
# against the attribute form the case says the bucket carries (relative, full
# path, or neither). Everything is recorded, including the condition text the
# script wrote into the JSON it hands to `--condition-from-file`, which is what
# pins the two clauses of R12 and their `||`.
#
# Usage: bash scripts/live-checks/check340_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check340.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK340_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|list-prefix|restore):' | sed 's/^/ ... last run: /')"
}

# The bucket and project a case runs against. Neither names `belong`, which is
# the installation this check must never touch.
PROJECT=prj-sandbox-a
BUCKET=bkt-sandbox-a

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(project bucket belong bucket-reachable account seed
  relative-lists-runs relative-lists-reports relative-reads-runs
  relative-root-refused relative-tokens-read-refused
  relative-tokens-list-refused relative-cache-list-refused
  full-lists-runs full-lists-reports full-reads-runs
  full-root-refused full-tokens-read-refused
  full-tokens-list-refused full-cache-list-refused
  list-prefix restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin" "$1/state/objects"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check340.sh's test.
#
# It models what the check reads and writes and nothing else:
#   * a bucket with the objects the run seeds (cp writes them, rm -r removes
#     them, ls lists them and refuses a prefix that holds none);
#   * bucket bindings whose conditions it REALLY evaluates, against the form of
#     the objectListPrefix attribute that $FAKE340_DIR/form names (relative, full
#     or none) — so a listing is allowed only when a granted clause the run wrote
#     itself covers it, and a read only when a granted resource.name clause
#     does;
#   * a service account that exists once the run creates it and is gone once it
#     deletes it.
# The owner's own identity (no --impersonate-service-account) is allowed
# everything unless FAKE340_UNREACHABLE is set. Every call is recorded on the
# first line, before anything else, so a dry run that made no call at all is
# provable rather than argued.
set -euo pipefail
dir=${FAKE340_DIR:?FAKE340_DIR is not set}
printf 'gcloud %s\n' "$*" >>"$dir/calls"

# The whole command line, kept: the loop below takes the arguments apart, and
# the dispatch after it reads the verbs out of this instead.
argv=("$@")
account=${FAKE340_ACCOUNT:-owner@sandbox-a.example}

unknown() {
  printf 'fake gcloud: unexpected call: %s\n' "$*" >&2
  exit 64
}
# criteria_refusal PERMISSION: one of the four refusals the criteria read, and
# the one place FAKE340_REFUSE_ERROR applies. A refusal that did not ANSWER — a
# 5xx gcloud did not retry, an expired session, a killed request — is what that
# knob models: the command fails without refusing anything, which says nothing
# about the clause and must not be read as the criterion holding. The knob is
# deliberately not inside `deny`: the propagation and read-back refusals are
# other cases' subjects, and this one is about the criteria alone.
criteria_refusal() { # permission
  if [ -n "${FAKE340_REFUSE_ERROR:-}" ]; then
    printf 'ERROR: (gcloud.storage) HTTPError 500: Backend Error\n' >&2
    exit 1
  fi
  deny "$1"
}

deny() { # permission
  # FAKE340_DENY_PERM makes the refusal name a permission of another kind, which
  # is what a refusal for a reason other than the clause reads like. It applies
  # while the run is READING the criteria and not after the bucket has accepted a
  # removal: the wait for that removal reads a denial for what it is — the
  # permission the removed clause carried — and a case about the criteria's
  # refusals is not a case about that wait.
  perm=$1
  if [ -n "${FAKE340_DENY_PERM:-}" ] && [ ! -f "$dir/removed" ]; then
    perm=$FAKE340_DENY_PERM
  fi
  printf 'ERROR: (gcloud.storage) HTTPError 403: %s does not have %s access to the Google Cloud Storage object.\n' "$impersonate" "$perm" >&2
  if [ -n "${FAKE340_MULTILINE_DENY:-}" ]; then
    # A refusal whose real reason is on a LATER line: the first line names the
    # permission, and the second says why. A quote that took only the first line
    # lost the second, which is what the owner's failed reports/ and tokens/
    # listings looked like behind the impersonation warning.
    printf 'ERROR: (gcloud.storage) the request was refused before the clause was evaluated, and this second line is the one a first-line-only quote hid.\n' >&2
  fi
  exit 1
}

impersonate=""
condition_file=""
member=""
role=""
urls=()
# The verb words come first and are not arguments: `storage ls URL` has two,
# `storage buckets add-iam-policy-binding URL` and `iam service-accounts create
# ID` three. Everything after them is either a flag or a positional.
case "${argv[1]:-}" in
buckets | service-accounts) verbs=3 ;;
*) verbs=2 ;;
esac
seen=0
while [ $# -gt 0 ]; do
  seen=$((seen + 1))
  if [ "$seen" -le "$verbs" ]; then
    shift
    continue
  fi
  case "$1" in
  --impersonate-service-account=*) impersonate=${1#*=} ;;
  --condition-from-file=*) condition_file=${1#*=} ;;
  --member=*) member=${1#*=} ;;
  --role=*) role=${1#*=} ;;
  -*) : ;;
  *) urls+=("$1") ;;
  esac
  shift
done

# gcloud prints this on EVERY impersonated call, before the call's own answer,
# whether it worked or not — so it is the first line a first-line-only quote
# would report, hiding the ERROR behind it. The fake prints it for real, which is
# what makes the "quote the whole error, drop the warning" pin a reading.
if [ -n "$impersonate" ]; then
  printf 'WARNING: This command is using service account impersonation. All API calls will be executed as [%s].\n' "$impersonate" >&2
fi

# The two clause kinds, as R12 writes them: the listing half's value out of
# api.getAttribute('storage.googleapis.com/objectListPrefix', '') and the read
# half's out of resource.name. A clause of the other kind is absent from the
# expression and extracts as the empty string, which is what makes a binding that
# carries only one of the two a denial for the other request.
list_clause() { printf '%s' "$1" | sed -n 's/.*objectListPrefix[^"]*startsWith("\([^"]*\)").*/\1/p'; }
read_clause() { printf '%s' "$1" | sed -n 's/.*resource\.name\.startsWith("\([^"]*\)").*/\1/p'; }

# bindings_in_force: the bindings the bucket acts on. FAKE340_REMOVE_LATE=N
# models IAM's asynchronous REMOVAL (the same V5 the check's grant retry is
# about): a removed binding is still enforced while $dir/pending-ttl, decremented
# once per request of the account's, is above zero. Without the knob a removal
# takes effect at once, which is the fake applying the bucket's own semantics
# instantly and not what a real bucket does.
bindings_in_force() {
  if [ -f "$dir/bindings" ]; then cat "$dir/bindings"; fi
  if [ -f "$dir/pending-ttl" ] && [ "$(cat "$dir/pending-ttl" 2>/dev/null || echo 0)" -gt 0 ]; then
    cat "$dir/pending" 2>/dev/null || true
  fi
}
tick_pending() {
  local left
  [ -f "$dir/pending-ttl" ] || return 0
  left=$(cat "$dir/pending-ttl" 2>/dev/null || echo 0)
  left=$((left - 1))
  if [ "$left" -le 0 ]; then
    rm -f "$dir/pending-ttl" "$dir/pending"
    return 0
  fi
  printf '%s\n' "$left" >"$dir/pending-ttl"
}
# readback_denied: a read-back that cannot be READ — the operator's own identity
# lost the permission, or the call did not come back. FAKE340_READBACK_DENIED
# turns every owner-identity read-back into an error, but only once something of
# the run's has been removed, so the guards and the criteria above are unaffected.
readback_denied() {
  [ -n "${FAKE340_READBACK_DENIED:-}" ] && [ -f "$dir/removed" ]
}

allowed_list() { # ATTR
  local attr=$1 bmember brole bexpr btitle candidate
  while IFS=$'\t' read -r bmember brole bexpr btitle; do
    [ -n "$bexpr" ] || continue
    candidate=$(list_clause "$bexpr")
    [ -n "$candidate" ] || continue
    case "$attr" in "$candidate"*) return 0 ;; esac
  done < <(bindings_in_force)
  return 1
}
allowed_read() { # OBJECT
  local object=$1 bmember brole bexpr btitle candidate
  while IFS=$'\t' read -r bmember brole bexpr btitle; do
    [ -n "$bexpr" ] || continue
    candidate=$(read_clause "$bexpr")
    [ -n "$candidate" ] || continue
    case "$object" in "$candidate"*) return 0 ;; esac
  done < <(bindings_in_force)
  return 1
}

case "${argv[0]:-}" in
auth)
  case "${argv[1]:-}" in
  list) printf '%s\n' "$account" ;;
  *) unknown "${argv[@]}" ;;
  esac
  ;;
storage)
  case "${argv[1]:-}" in
  ls)
    url=${urls[0]:-}
    [ -n "$url" ] || unknown "${argv[@]}"
    bare=${url#gs://}
    bucket=${bare%%/*}
    case "$bare" in
    */*) prefix=${bare#*/} ;;
    *) prefix="" ;;
    esac
    if [ -n "$impersonate" ] && [ -n "${FAKE340_FLAKY:-}" ]; then
      # A grant that has not taken effect yet: the first FAKE340_FLAKY listings
      # are refused whatever the condition says, which is what a real fresh
      # binding does and what the check's retry exists for.
      attempts=$(cat "$dir/flaky" 2>/dev/null || echo 0)
      attempts=$((attempts + 1))
      printf '%s\n' "$attempts" >"$dir/flaky"
      if [ "$attempts" -le "$FAKE340_FLAKY" ]; then
        deny storage.objects.list
      fi
    fi
    if [ -n "$impersonate" ]; then
      if [ -n "${FAKE340_LS_ERROR_AFTER_REMOVE:-}" ] && [ -f "$dir/removed" ]; then
        # A call that does not ANSWER, once the bucket has accepted a removal:
        # the session expired, gcloud did not retry a 5xx, the request was
        # killed. It is not a permission refusal, so it says nothing about
        # whether the removed binding is still in force -- which is what makes
        # it the error a wait for the removal must not read as "gone".
        printf 'ERROR: (gcloud.storage.ls) HTTPError 500: Backend Error\n' >&2
        exit 1
      fi
      tick_pending
      # FAKE340_ROOT_OK and FAKE340_WIDE are the two ways a grant can be wider
      # than R12's: the first lets the root listing through alone, the second
      # lets the account list anywhere, which is what an unconditioned binding
      # would do. WIDE reaches only while the account holds a binding at all: it
      # is a binding of this run's that is wider than R12's, and a removed one
      # grants nothing.
      if [ -z "$prefix" ] && [ -n "${FAKE340_ROOT_OK:-}" ]; then
        :
      elif [ -n "${FAKE340_WIDE:-}" ] && [ -s "$dir/bindings" ]; then
        :
      else
        case "$(cat "$dir/form" 2>/dev/null || echo none)" in
        relative) attr=$prefix ;;
        full) attr="projects/_/buckets/$bucket/objects/$prefix" ;;
        *) attr='__no-form-carries-this__' ;;
        esac
        allowed_list "$attr" || criteria_refusal storage.objects.list
      fi
    elif [ -n "${FAKE340_UNREACHABLE:-}" ] || readback_denied; then
      deny storage.objects.list
    fi
    found=0
    while IFS= read -r f; do
      rel=${f#"$dir/objects/"}
      case "$rel" in
      "$prefix"*) found=1; printf 'gs://%s/%s\n' "$bucket" "$rel" ;;
      esac
    done < <(find "$dir/objects" -type f | sort)
    # An empty bucket answers an empty listing; a prefix that holds nothing is
    # an error, which is how a real `ls` reads and what the Restore's read-back
    # counts on.
    if [ "$found" -eq 0 ] && [ -n "$prefix" ]; then
      printf 'ERROR: (gcloud.storage.ls) One or more URLs matched no objects.\n' >&2
      exit 1
    fi
    ;;
  cat)
    url=${urls[0]:-}
    [ -n "$url" ] || unknown "${argv[@]}"
    bare=${url#gs://}
    bucket=${bare%%/*}
    path=${bare#*/}
    if [ -n "$impersonate" ] && [ -n "${FAKE340_FLAKY_READ:-}" ]; then
      # The read half of a grant that has not taken effect yet: its own counter,
      # so a case can make the READ arrive late while the listings answer at
      # once — which is what a single-attempt read would take for a refusal.
      attempts=$(cat "$dir/flaky-read" 2>/dev/null || echo 0)
      attempts=$((attempts + 1))
      printf '%s\n' "$attempts" >"$dir/flaky-read"
      if [ "$attempts" -le "$FAKE340_FLAKY_READ" ]; then
        deny storage.objects.get
      fi
    fi
    if [ -n "$impersonate" ]; then
      allowed_read "projects/_/buckets/$bucket/objects/$path" || criteria_refusal storage.objects.get
    fi
    if [ ! -f "$dir/objects/$path" ]; then
      printf 'ERROR: (gcloud.storage.cat) HTTPError 404: No such object: %s\n' "$url" >&2
      exit 1
    fi
    cat "$dir/objects/$path"
    ;;
  cp)
    src=${urls[0]:-}
    dst=${urls[1]:-}
    [ -n "$src" ] && [ -n "$dst" ] || unknown "${argv[@]}"
    bare=${dst#gs://}
    path=${bare#*/}
    mkdir -p "$dir/objects/$(dirname "$path")"
    cp "$src" "$dir/objects/$path"
    ;;
  rm)
    if [ ${#urls[@]} -gt 0 ]; then
      for url in "${urls[@]}"; do
        if [ -n "${FAKE340_STUCK:-}" ]; then
          continue
        fi
        bare=${url#gs://}
        prefix=${bare#*/}
        while IFS= read -r f; do
          rel=${f#"$dir/objects/"}
          case "$rel" in
          "$prefix"*) rm -f "$f" ;;
          esac
        done < <(find "$dir/objects" -type f | sort)
      done
    fi
    ;;
  buckets)
    case "${argv[2]:-}" in
    add-iam-policy-binding)
      [ -n "$condition_file" ] || unknown "${argv[@]}"
      if [ -n "${FAKE340_GRANT_REFUSED:-}" ]; then
        # The grant itself refused — not the listing. A run that swallows this
        # reads every listing below as "this form does not work" and answers
        # "NEITHER form listed", which is a verdict about the attribute drawn
        # from a permission error. FAKE340_GRANT_REFUSED=second refuses only the
        # reports/ binding, so the runs/ one is really on the bucket and the
        # Restore has something of this run's to remove.
        if [ "${FAKE340_GRANT_REFUSED}" != second ] || [ "$role" = roles/storage.objectUser ]; then
          printf 'ERROR: (gcloud.storage.buckets.add-iam-policy-binding) PERMISSION_DENIED: %s does not have storage.buckets.setIamPolicy access to the bucket.\n' "$account" >&2
          exit 1
        fi
      fi
      # python3's json, so a condition file the script escaped wrongly fails
      # here rather than passing a fake that skimmed it.
      expression=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["expression"])' "$condition_file")
      title=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["title"])' "$condition_file")
      # Kept as well as bound: a binding goes away again (between the two forms,
      # and in the Restore), and what this check GRANTED is what a test asserts
      # on -- R12's two clauses and their || -- so the text has to survive the
      # run that wrote it.
      printf '%s\t%s\t%s\t%s\n' "$member" "$role" "$expression" "$title" >>"$dir/grants"
      if [ -n "${FAKE340_REPORTS_BINDING_MISSING:-}" ] && [ "$role" = roles/storage.objectUser ]; then
        # The grant call ANSWERED that it landed, but the bucket policy it is
        # read back from does not name it: the reports/ listing then fails while
        # the policy says the binding was never there -- the reading a row has to
        # tell apart from a clause that does not match.
        :
      else
        printf '%s\t%s\t%s\t%s\n' "$member" "$role" "$expression" "$title" >>"$dir/bindings"
      fi
      printf 'bindings:\n'
      ;;
    remove-iam-policy-binding)
      if [ -n "${FAKE340_REMOVE_REFUSED:-}" ]; then
        # The teardown refused: the previous form's conditioned binding stays on
        # the bucket, which is how the next form would list through the EARLIER
        # clause and the check would answer the wrong form. =once refuses only the
        # first removal, which is what a transient refusal looks like and what the
        # Restore's retry exists for.
        attempts=$(cat "$dir/remove-attempts" 2>/dev/null || echo 0)
        attempts=$((attempts + 1))
        printf '%s\n' "$attempts" >"$dir/remove-attempts"
        if [ "${FAKE340_REMOVE_REFUSED}" != once ] || [ "$attempts" -le 1 ]; then
          printf 'ERROR: (gcloud.storage.buckets.remove-iam-policy-binding) PERMISSION_DENIED: %s does not have storage.buckets.setIamPolicy access to the bucket.\n' "$account" >&2
          exit 1
        fi
      fi
      # --all: the member's binding goes whatever condition it carries, which is
      # what lets a Restore that does not know which form was granted clean up.
      tmp=$dir/bindings.$$
      : >"$tmp"
      while IFS=$'\t' read -r bmember brole bexpr btitle; do
        [ -n "$bmember" ] || continue
        if [ "$bmember" = "$member" ] && [ "$brole" = "$role" ]; then
          : >"$dir/removed"
          if [ -n "${FAKE340_REMOVE_LATE:-}" ]; then
            printf '%s\t%s\t%s\t%s\n' "$bmember" "$brole" "$bexpr" "$btitle" >>"$dir/pending"
            printf '%s\n' "$FAKE340_REMOVE_LATE" >"$dir/pending-ttl"
          fi
          continue
        fi
        printf '%s\t%s\t%s\t%s\n' "$bmember" "$brole" "$bexpr" "$btitle" >>"$tmp"
      done <"$dir/bindings"
      mv "$tmp" "$dir/bindings"
      ;;
    get-iam-policy)
      if readback_denied; then
        deny storage.buckets.getIamPolicy
      fi
      printf 'bindings:\n'
      while IFS=$'\t' read -r bmember brole bexpr btitle; do
        [ -n "$bmember" ] || continue
        printf -- '- members:\n  - %s\n  role: %s\n' "$bmember" "$brole"
      done <"$dir/bindings"
      ;;
    *) unknown "${argv[@]}" ;;
    esac
    ;;
  *) unknown "${argv[@]}" ;;
  esac
  ;;
iam)
  case "${argv[1]:-}" in
  service-accounts)
    email=${urls[0]:-}
    case "${argv[2]:-}" in
    create)
      id=${urls[0]:-}
      [ -n "$id" ] || unknown "${argv[@]}"
      if [ -f "$dir/accounts/$id" ]; then
        printf 'ERROR: (gcloud.iam.service-accounts.create) AlreadyExists: service account %s already exists\n' "$id" >&2
        exit 1
      fi
      mkdir -p "$dir/accounts"
      : >"$dir/accounts/$id"
      printf 'Created service account [%s].\n' "$id"
      ;;
    describe)
      id=${email%%@*}
      if [ -n "${FAKE340_SA_DESCRIBE_DENIED:-}" ]; then
        # The owner's own first run: the identity could create and impersonate
        # the account but was refused iam.serviceAccounts.get on it, so a
        # describe is not the account being absent -- it is the account not
        # being readable.
        printf 'ERROR: (gcloud.iam.service-accounts.describe) PERMISSION_DENIED: %s does not have iam.serviceAccounts.get access to the service account %s.\n' "$account" "$email" >&2
        exit 1
      fi
      if readback_denied; then
        deny iam.serviceAccounts.get
      fi
      if [ -n "${FAKE340_SA_EXISTS:-}" ] || [ -f "$dir/accounts/$id" ]; then
        printf 'displayName: Check 340 throwaway\nemail: %s\n' "$email"
      else
        printf 'ERROR: (gcloud.iam.service-accounts.describe) NOT_FOUND: Service account %s not found\n' "$email" >&2
        exit 1
      fi
      ;;
    delete)
      id=${email%%@*}
      if [ -n "${FAKE340_DELETE_REFUSED:-}" ]; then
        # The delete the Restore's evidence rests on, refused: this run cannot
        # confirm the account is gone, which is a FAILed row and never a pass.
        printf 'ERROR: (gcloud.iam.service-accounts.delete) PERMISSION_DENIED: %s does not have iam.serviceAccounts.delete access to the service account %s.\n' "$account" "$email" >&2
        exit 1
      fi
      if [ -f "$dir/accounts/$id" ]; then
        rm -f "$dir/accounts/$id"
        printf 'deleted service account [%s]\n' "$email"
      else
        printf 'ERROR: (gcloud.iam.service-accounts.delete) NOT_FOUND: Service account %s not found\n' "$email" >&2
        exit 1
      fi
      ;;
    add-iam-policy-binding)
      printf '%s|%s\n' "$member" "$role" >>"$dir/sa-grants"
      ;;
    remove-iam-policy-binding)
      tmp=$dir/sa-grants.$$
      : >"$tmp"
      while IFS='|' read -r gmember grole; do
        [ -n "$gmember" ] || continue
        if [ "$gmember" = "$member" ] && [ "$grole" = "$role" ]; then continue; fi
        printf '%s|%s\n' "$gmember" "$grole" >>"$tmp"
      done <"$dir/sa-grants"
      mv "$tmp" "$dir/sa-grants"
      ;;
    *) unknown "${argv[@]}" ;;
    esac
    ;;
  *) unknown "${argv[@]}" ;;
  esac
  ;;
*) unknown "${argv[@]}" ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# ------------------------------------------------------------------- the cases
# setup_case NAME FORM — a fresh case: the fakes, an empty bucket, no bindings
# and no account, and the form of the objectListPrefix attribute the bucket
# carries (relative, full, or none for neither, which is what a wrong form
# reads as).
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state/objects" "$dir/state/accounts" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  printf '%s\n' "$2" >"$dir/state/form"
  : >"$dir/state/bindings"
  : >"$dir/state/grants"
  : >"$dir/state/sa-grants"
  status=0
  out=""
  RUN340_ARGS=()
  RUN340_ENV=()
}

# run340 [ARGS...] — runs the check on the case at hand. RUN340_ARGS adds
# arguments, RUN340_ENV names environment variables (NAME=value), and the
# propagation wait is a second by default so a case about a listing that must
# fail does not sit through the real 150s one.
run340() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE340_DIR="$dir/state" \
    CHECK340_PROPAGATION_SECONDS="${PROPAGATION:-1}" CHECK340_POLL_SECONDS=0 \
    PATH="$dir/bin:$PATH" \
    ${RUN340_ENV[@]+"${RUN340_ENV[@]}"} \
    bash "$script" ${RUN340_ARGS[@]+"${RUN340_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN340_ARGS=()
  RUN340_ENV=()
  PROPAGATION=1
}
run340_default() { run340 --project "$PROJECT" --bucket "$BUCKET" "$@"; }

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 340 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
}

expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a gcloud call was made: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}

# expect_bucket_clean WHAT — the Restore really did take everything this run
# made away: no seeded object, no binding, no impersonation grant and no account.
expect_bucket_clean() {
  local what=$1 left
  left=$(find "$dir/state/objects" -type f | tr '\n' ' ')
  [ -z "$left" ] || fail "$what: seeded objects were left in the bucket: $left"
  [ ! -s "$dir/state/bindings" ] || fail "$what: the bucket still holds a binding: $(tr '\n' ';' <"$dir/state/bindings")"
  [ ! -s "$dir/state/sa-grants" ] || fail "$what: the impersonation grant is still there: $(cat "$dir/state/sa-grants")"
  left=$(find "$dir/state/accounts" -type f | tr '\n' ' ')
  [ -z "$left" ] || fail "$what: the service account was left behind: $left"
}

# expect_granted WHAT NEEDLE — the condition text the run handed to
# --condition-from-file, as the run wrote it. This is what R12's Terraform has to
# reproduce, so it is asserted rather than inferred from a listing that passed.
expect_granted() { # what needle
  expect_contains "$(cat "$dir/state/grants")" "$2" "$1"
}
expect_not_granted() { # what needle
  expect_absent "$(cat "$dir/state/grants")" "$2" "$1"
}

scratch_of() { # dir
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

expect_scratch_gone() { # what
  local scratch
  scratch=$(scratch_of "$dir")
  if [ -z "$scratch" ]; then
    fail "$1: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch survived the run"
  [ -z "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ] || fail "$1: files were left in TMPDIR: $(find "$dir/tmp" -mindepth 1)"
  return 0
}

# ------------------------------------------------------------------- the tests
# The answer R12 waits for, on the form the design expects to be the one: the
# attribute carries the request's own prefix.
test_the_relative_form_passes() {
  setup_case relative relative
  run340_default
  expect_status "$status" 0 "relative form"
  expect_block_keys "relative form"
  expect_contains "$out" "CHECK 340 RESULT END (exit 0)" "relative form: the end line"
  expect_contains "$out" "FACT belong: PASS — " "relative form: the belong guard"
  expect_contains "$out" "FACT bucket-reachable: PASS — " "relative form: the bucket was reached"
  expect_contains "$out" "FACT account: PASS — " "relative form: the account is this run's own"
  expect_contains "$out" "FACT seed: PASS — " "relative form: the four objects"
  expect_contains "$out" "FACT relative-lists-runs: PASS — " "relative form: the runs listing"
  expect_contains "$out" "FACT relative-lists-reports: PASS — " "relative form: the reports listing"
  expect_contains "$out" "FACT relative-reads-runs: PASS — " "relative form: the runs read"
  expect_contains "$out" "FACT relative-root-refused: PASS — " "relative form: the root listing is refused"
  expect_contains "$out" "FACT relative-tokens-read-refused: PASS — " "relative form: tokens/ cannot be read"
  expect_contains "$out" "FACT relative-tokens-list-refused: PASS — " "relative form: tokens/ cannot be listed"
  expect_contains "$out" "FACT relative-cache-list-refused: PASS — " "relative form: cache/ cannot be listed"
  expect_contains "$out" "FACT full-lists-runs: INFO — not tried" "relative form: the form that was not reached"
  expect_contains "$out" "FACT list-prefix: PASS — " "relative form: the answer"
  expect_contains "$out" "the RELATIVE form passed all seven criteria" "relative form: which form the answer is"
  expect_contains "$out" "THE LIST PREFIX R12 MUST USE: runs/ on the runs binding" "relative form: the line to copy back"
  expect_contains "$out" "FACT restore: PASS — " "relative form: the Restore"
  # R12's two clauses, in one expression, and never joined by &&.
  expect_granted "relative form: the runs binding's two clauses" 'resource.name.startsWith("projects/_/buckets/bkt-sandbox-a/objects/runs/")'
  expect_granted "relative form: the listing half is the request's own prefix" "objectListPrefix', '').startsWith(\"runs/\")"
  expect_granted "relative form: the reports binding" "objectListPrefix', '').startsWith(\"reports/\")"
  expect_granted "relative form: the two clause kinds are joined by ||" ' || '
  expect_not_granted "relative form: nothing is joined by &&" ' && '
  expect_contains "$(cat "$dir/state/grants")" "roles/storage.objectViewer" "relative form: the runs binding's role"
  expect_contains "$(cat "$dir/state/grants")" "roles/storage.objectUser" "relative form: the reports binding's role"
  # The account and the impersonation grant come back off the account itself.
  expect_contains "$(cat "$dir/state/calls")" "gcloud iam service-accounts delete" "relative form: the account is deleted"
  expect_contains "$(cat "$dir/state/calls")" "roles/iam.serviceAccountTokenCreator" "relative form: the impersonation grant is made and removed"
  expect_bucket_clean "relative form"
  expect_scratch_gone "relative form"
}

# The other answer: the attribute carries the bucket's object path. R12's
# builder has to write the full string then, and the check says which.
test_the_full_path_form_passes() {
  setup_case full full
  run340_default
  expect_status "$status" 0 "full path form"
  expect_block_keys "full path form"
  expect_contains "$out" "FACT relative-lists-runs: FAIL — " "full path form: the relative form does not list"
  expect_contains "$out" "FACT relative-lists-reports: FAIL — " "full path form: nor the reports listing"
  expect_contains "$out" "FACT full-lists-runs: PASS — " "full path form: the full path form lists"
  expect_contains "$out" "FACT full-lists-reports: PASS — " "full path form: the reports listing"
  expect_contains "$out" "FACT full-reads-runs: PASS — " "full path form: the runs read"
  expect_contains "$out" "FACT full-root-refused: PASS — " "full path form: the root listing is refused"
  expect_contains "$out" "FACT full-tokens-list-refused: PASS — " "full path form: tokens/ cannot be listed"
  expect_contains "$out" "FACT list-prefix: PASS — " "full path form: the answer"
  expect_contains "$out" "the FULL PATH form passed all seven criteria" "full path form: which form the answer is"
  expect_contains "$out" "THE LIST PREFIX R12 MUST USE: projects/_/buckets/bkt-sandbox-a/objects/runs/" \
    "full path form: the line to copy back"
  expect_contains "$out" "CHECK 340 RESULT END (exit 0)" "full path form: the end line"
  expect_granted "full path form: the granted listing clause" '.startsWith("projects/_/buckets/bkt-sandbox-a/objects/reports/")'
  expect_bucket_clean "full path form"
  expect_scratch_gone "full path form"
}

# Neither form lists: the one reading that must stop R12's Terraform. The exit
# is 1 and not 2 — the check DID measure, and the answer is no — and the Restore
# still has to run.
test_neither_form_listing_is_a_fail() {
  setup_case none none
  run340_default
  expect_status "$status" 1 "neither form"
  expect_block_keys "neither form"
  expect_contains "$out" "FACT relative-lists-runs: FAIL — " "neither form: the relative form"
  expect_contains "$out" "FACT full-lists-runs: FAIL — " "neither form: the full path form"
  # The refusal's own words are in the row: a detail that reads "(gcloud said: )"
  # is a failure recorded without the reason it failed.
  expect_contains "$out" "gcloud said: ERROR:" "neither form: the row carries gcloud's own words"
  expect_contains "$out" "storage.objects.list" "neither form: and the permission the refusal names"
  expect_absent "$out" "(gcloud said: )" "neither form: no row quotes an empty error"
  # A failed reports/ listing has to say whether the binding is even there: here
  # it landed (the fake bound it), so the failure is the clause not matching the
  # request and not a grant that never arrived.
  expect_contains "$out" "IS in the bucket policy read back now" "neither form: the row says the reports binding landed"
  expect_absent "$out" "using service account impersonation" "neither form: and the impersonation warning is not the quoted reason"
  expect_contains "$out" "FACT list-prefix: FAIL — " "neither form: the answer"
  expect_contains "$out" "NEITHER form listed" "neither form: the row says which reading it is"
  expect_contains "$out" "must NOT land" "neither form: it says what R12 has to do"
  expect_contains "$out" "CHECK 340 RESULT END (exit 1)" "neither form: the end line"
  expect_contains "$out" "FACT restore: PASS — " "neither form: the Restore still ran"
  expect_bucket_clean "neither form"
  expect_scratch_gone "neither form"
}

# The whole error, not its first line. gcloud prints the service-account
# impersonation WARNING first on every impersonated call, so a row that quoted
# only the first line reported the warning as the reason and hid the ERROR behind
# it — which is how the owner's failed reports/ and tokens/ listings came back
# with nothing to read. The row has to carry every line of the call's own answer.
test_a_multi_line_error_is_quoted_in_full() {
  setup_case multiline none
  RUN340_ENV=(FAKE340_MULTILINE_DENY=yes)
  run340_default
  expect_status "$status" 1 "multi-line error"
  expect_contains "$out" "FACT relative-lists-runs: FAIL — " "multi-line error: the listing fails"
  expect_contains "$out" "this second line is the one a first-line-only quote hid" \
    "multi-line error: the line after the first is quoted"
  expect_contains "$out" "storage.objects.list" "multi-line error: and the permission is still named"
  expect_absent "$out" "using service account impersonation" "multi-line error: the impersonation warning is dropped"
  expect_contains "$out" " | " "multi-line error: the lines are joined, not lost"
  expect_bucket_clean "multi-line error"
}

# A reports/ listing that fails while the binding is NOT in the bucket policy is
# a grant that never landed, not a clause that does not match — and the two must
# be told apart, because only the second is a reading about the attribute. The
# row says which, read back with the owner's identity (the account cannot read
# the policy it is the subject of).
test_a_reports_listing_that_fails_names_the_binding() {
  setup_case reports-binding-missing relative
  RUN340_ENV=(FAKE340_REPORTS_BINDING_MISSING=yes)
  run340_default
  expect_status "$status" 1 "reports binding missing"
  expect_contains "$out" "FACT relative-lists-reports: FAIL — " "reports binding missing: the reports listing fails"
  expect_contains "$out" "the roles/storage.objectUser binding for " "reports binding missing: the row names the binding it read back"
  expect_contains "$out" "is NOT in the bucket policy read back now, so it never landed" \
    "reports binding missing: and says it never landed"
  # The read-back is the owner's, not the account's: it is the policy's own
  # contents, and the account holds no permission to read them.
  policy_call=$(grep 'get-iam-policy' "$dir/state/calls")
  expect_contains "$policy_call" "gcloud storage buckets get-iam-policy" "reports binding missing: the policy was read back"
  expect_absent "$policy_call" "--impersonate" "reports binding missing: with your own identity"
  expect_bucket_clean "reports binding missing"
}

# A grant wider than R12's two prefixes: the account can list anywhere. Every
# refusal criterion is the failure then, and no form may be reported as the
# answer — the whole point of the two narrow prefixes is that tokens/ and cache/
# stay out of reach.
test_a_grant_wider_than_the_two_prefixes_is_a_fail() {
  setup_case wide relative
  RUN340_ENV=(FAKE340_WIDE=yes)
  run340_default
  expect_status "$status" 1 "wide grant"
  expect_contains "$out" "FACT relative-root-refused: FAIL — " "wide grant: the root listing was allowed"
  expect_contains "$out" "FACT relative-tokens-list-refused: FAIL — " "wide grant: tokens/ could be listed"
  expect_contains "$out" "FACT relative-cache-list-refused: FAIL — " "wide grant: cache/ could be listed"
  expect_contains "$out" "FACT relative-lists-runs: PASS — " "wide grant: the listing the grant was for still worked"
  expect_contains "$out" "FACT list-prefix: FAIL — " "wide grant: no form is the answer"
  expect_contains "$out" "wider than R12's" "wide grant: the row says what the failure is"
  expect_bucket_clean "wide grant"
}

# The root listing on its own: the criterion the other six cannot see. A form
# that lists runs/ and reports/ but opens the bucket root is still wrong.
test_a_root_listing_that_is_allowed_is_a_fail() {
  setup_case root relative
  RUN340_ENV=(FAKE340_ROOT_OK=yes)
  run340_default
  expect_status "$status" 1 "root listing allowed"
  expect_contains "$out" "FACT relative-root-refused: FAIL — " "root listing allowed: the root row fails"
  expect_contains "$out" "FACT relative-lists-runs: PASS — " "root listing allowed: the other rows still passed"
  expect_contains "$out" "FACT list-prefix: FAIL — " "root listing allowed: no form is the answer"
  expect_bucket_clean "root listing allowed"
}

# The teardown is not conditional on the answer: a run whose criteria FAILed has
# deleted the account, the bindings and the objects all the same. This case also
# pins the verdict logic — the form itself passed, and the run still exits 1,
# because a Restore that did not finish is not a pass.
test_a_restore_that_could_not_delete_is_a_fail() {
  setup_case stuck relative
  RUN340_ENV=(FAKE340_STUCK=yes)
  run340_default
  expect_status "$status" 1 "stuck Restore"
  expect_contains "$out" "FACT relative-lists-runs: PASS — " "stuck Restore: the form itself passed"
  expect_contains "$out" "FACT list-prefix: PASS — " "stuck Restore: and the answer was reached"
  expect_contains "$out" "FACT restore: FAIL — " "stuck Restore: the Restore row fails"
  expect_contains "$out" "Still there:" "stuck Restore: the row says what is still there"
  expect_absent "$out" "Not readable" "stuck Restore: and nothing is recorded as unreadable, because every read-back answered"
  expect_contains "$out" "CHECK 340 RESULT END (exit 1)" "stuck Restore: the block does not read as a pass"
  [ -n "$(find "$dir/state/objects" -type f)" ] || fail "stuck Restore: the case did not leave the seeded objects behind"
  expect_contains "$out" "the slug " "stuck Restore: the row names what to delete by hand"
}

# A grant that takes effect late must not be read as a refusal: this is the one
# reading that would pick the WRONG form, so a listing that must succeed is
# retried. The fake refuses the first two attempts.
test_a_grant_that_arrives_late_is_not_a_refusal() {
  setup_case late relative
  PROPAGATION=10
  RUN340_ENV=(FAKE340_FLAKY=2)
  run340_default
  expect_status "$status" 0 "late grant"
  expect_contains "$out" "FACT relative-lists-runs: PASS — " "late grant: the listing was waited for"
  expect_contains "$out" "FACT list-prefix: PASS — " "late grant: the answer is the relative form"
  expect_bucket_clean "late grant"
}

# The owner's own installation, refused by name in the project and in the
# bucket — before a single call, so nothing is created and nothing is read.
test_belong_is_refused_in_both_places() {
  setup_case belong-project relative
  run340 --project belong-sandbox --bucket "$BUCKET"
  expect_status "$status" 2 "belong project"
  expect_contains "$out" "FACT belong: FAIL — " "belong project: the row fails"
  expect_contains "$(cat "$dir/err")" "belong" "belong project: the guard names it"
  expect_no_calls "belong project"
  expect_bucket_clean "belong project"

  setup_case belong-bucket relative
  run340 --project "$PROJECT" --bucket gs://belong-runs
  expect_status "$status" 2 "belong bucket"
  expect_contains "$out" "FACT belong: FAIL — " "belong bucket: the row fails"
  expect_contains "$(cat "$dir/err")" "belong" "belong bucket: the guard names it"
  expect_no_calls "belong bucket"
  expect_bucket_clean "belong bucket"
}

# The dry run: every command a real run would make, and not one call. It is not
# a verdict, so its block carries only what it settled — the two arguments, the
# belong guard and itself — and it claims no list prefix.
test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry relative
  run340_default --dry-run
  expect_status "$status" 0 "dry run"
  expect_no_calls "dry run"
  expect_contains "$out" "FACT dry-run: PASS — " "dry run: the row"
  expect_contains "$out" "FACT project: PASS — " "dry run: the project"
  expect_contains "$out" "FACT bucket: PASS — " "dry run: the bucket"
  expect_contains "$out" "FACT belong: PASS — " "dry run: the belong guard still runs"
  expect_absent "$out" "FACT list-prefix:" "dry run: no answer is claimed"
  expect_absent "$out" "THE LIST PREFIX R12 MUST USE" "dry run: and none is printed"
  expect_contains "$(cat "$dir/err")" "dry run: NOTHING below was run" "dry run: it says so"
  expect_contains "$(cat "$dir/err")" "add-iam-policy-binding" "dry run: the plan carries the bindings"
  expect_contains "$(cat "$dir/err")" "--condition-from-file" "dry run: and the condition file"
  expect_contains "$(cat "$dir/err")" "storage cp" "dry run: and the seeded objects"
  expect_contains "$(cat "$dir/err")" "remove-iam-policy-binding" "dry run: and the Restore's removes"
  expect_bucket_clean "dry run"
  expect_scratch_gone "dry run"
}

# A dry run refuses `belong` too: the guard is not something a flag turns off.
test_a_dry_run_still_refuses_belong() {
  setup_case drybelong relative
  run340 --project "$PROJECT" --bucket belong --dry-run
  expect_status "$status" 2 "dry run belong"
  expect_contains "$out" "FACT belong: FAIL — " "dry run belong: the row fails"
  expect_no_calls "dry run belong"
}

# A bucket that cannot be reached is a stop, not a verdict: every reading below
# would be a refusal, and neither of the two forms would be the reason.
test_a_bucket_that_cannot_be_reached_stops() {
  setup_case unreachable relative
  RUN340_ENV=(FAKE340_UNREACHABLE=yes)
  run340_default
  expect_status "$status" 2 "unreachable bucket"
  # The guard RAN and decided, so its row carries the decision and not the
  # "not reached" the block reserves for a step that was never measured.
  expect_contains "$out" "FACT bucket-reachable: FAIL — gs://bkt-sandbox-a/ is not listable by your own identity" "unreachable bucket: the row carries the guard's own reason"
  expect_absent "$out" "FACT bucket-reachable: FAIL — not reached" "unreachable bucket: the row is not the unmeasured fallback"
  expect_contains "$out" "FACT aborted: FAIL — " "unreachable bucket: the stop is recorded"
  expect_contains "$out" "FACT relative-lists-runs: FAIL — not reached" "unreachable bucket: nothing was measured"
  expect_contains "$out" "FACT list-prefix: FAIL — not reached" "unreachable bucket: no answer is given"
  # The guard's own read of the bucket is the one call a run that stops here
  # makes; nothing was created or written anywhere.
  expect_absent "$(cat "$dir/state/calls")" "service-accounts create" "unreachable bucket: no account was made"
  expect_absent "$(cat "$dir/state/calls")" "add-iam-policy-binding" "unreachable bucket: no binding was granted"
  expect_absent "$(cat "$dir/state/calls")" "storage cp" "unreachable bucket: no object was seeded"
  expect_contains "$(cat "$dir/state/calls")" "storage ls --project=prj-sandbox-a gs://bkt-sandbox-a/" "unreachable bucket: the bucket was read once"
  expect_bucket_clean "unreachable bucket"
}

# An account of the check's own name that is already there: the check never
# adopts it, and it stops before creating anything.
test_an_account_that_already_exists_stops() {
  setup_case existing relative
  RUN340_ENV=(FAKE340_SA_EXISTS=yes)
  run340_default
  expect_status "$status" 2 "account already there"
  expect_contains "$out" "FACT account: FAIL — the name this run drew" "account already there: the row carries the guard's own reason"
  expect_absent "$out" "FACT account: FAIL — not reached" "account already there: the row is not the unmeasured fallback"
  expect_contains "$out" "already exists" "account already there: the row says why it stops"
  expect_absent "$(cat "$dir/state/calls")" "service-accounts create" "account already there: nothing was created"
  expect_bucket_clean "account already there"
}

# A describe that is REFUSED is not the name being free. The owner's first run
# could create and impersonate the throwaway account but was refused
# iam.serviceAccounts.get on it, and reading that refusal as "absent" is how a
# name somebody else holds would be adopted. The row says could-not-read-back,
# the run goes on, and the create is what refuses a name that is really taken.
test_a_denied_describe_is_could_not_read_back() {
  setup_case describe-denied relative
  RUN340_ENV=(FAKE340_SA_DESCRIBE_DENIED=yes)
  run340_default
  expect_status "$status" 0 "denied describe"
  expect_contains "$out" "FACT account: INFO — could not read back" "denied describe: the row says it could not be read back"
  expect_absent "$out" "FACT account: PASS" "denied describe: the row is not a pass"
  expect_absent "$out" "is absent from" "denied describe: and never says the name is free"
  expect_contains "$out" "iam.serviceAccounts.get" "denied describe: the row carries the refusal"
  expect_contains "$out" "FACT list-prefix: PASS — " "denied describe: the run still answers the question"
  # The Restore's evidence is the delete's own answer, not a describe the
  # identity may not read.
  expect_contains "$out" "FACT restore: PASS — " "denied describe: the Restore still confirms"
  expect_contains "$out" "the delete call answered" "denied describe: and says which evidence it used"
  expect_bucket_clean "denied describe"
  expect_scratch_gone "denied describe"
}

# The arguments: both are required, an unknown one is refused, and --help prints
# the usage without touching anything.
test_usage_and_arguments() {
  setup_case usage relative
  run340
  expect_status "$status" 2 "no arguments"
  expect_contains "$(cat "$dir/err")" "required" "no arguments: an argument is required"
  expect_no_calls "no arguments"

  run340 --bucket "$BUCKET"
  expect_status "$status" 2 "no --project"
  expect_contains "$(cat "$dir/err")" "--project is required" "no --project: the refusal"
  expect_no_calls "no --project"

  run340 --project "$PROJECT"
  expect_status "$status" 2 "no --bucket"
  expect_contains "$(cat "$dir/err")" "--bucket is required" "no --bucket: the refusal"
  expect_contains "$(cat "$dir/err")" "SANDBOX" "no --bucket: it says which bucket it has to be"
  expect_no_calls "no --bucket"

  run340 --project "$PROJECT" --bucket "$BUCKET" --bucket-name x
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$(cat "$dir/err")" "unknown argument" "an unknown argument: the refusal"
  expect_no_calls "an unknown argument"

  run340 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "--project" "--help: the usage names the flags"
  expect_contains "$out" "--bucket" "--help: and the bucket"
  expect_absent "$out" "CHECK 340 RESULT" "--help: no block is printed"
  expect_no_calls "--help"
}

# A grant that is REFUSED is not a verdict: the run could not tell. Every listing
# below it would be refused for a reason that has nothing to do with the
# attribute, and answering "NEITHER form listed, R12's Terraform must NOT land"
# from a permission error is the most damaging thing this check could say. The
# exit is 2, the aborted row says why, and the Restore still runs.
test_a_refused_grant_is_could_not_tell() {
  setup_case refused relative
  RUN340_ENV=(FAKE340_GRANT_REFUSED=yes)
  run340_default
  expect_status "$status" 2 "refused grant"
  expect_contains "$out" "FACT aborted: FAIL — " "refused grant: the abort is recorded"
  expect_contains "$out" "the bucket refused the roles/storage.objectViewer binding" "refused grant: the row names the grant that was refused"
  expect_contains "$out" "FACT list-prefix: FAIL — not reached" "refused grant: no answer is given about the attribute"
  expect_absent "$out" "NEITHER form listed" "refused grant: the run does not claim neither form lists"
  expect_absent "$out" "must NOT land" "refused grant: and does not stop the plan on a permission error"
  expect_contains "$out" "CHECK 340 RESULT END (exit 2)" "refused grant: the block ends as could-not-tell"
  expect_contains "$(cat "$dir/state/calls")" "add-iam-policy-binding" "refused grant: the grant was attempted"
  # The teardown is not conditional on the run having got anywhere: the account
  # it made is deleted, and the bucket it touched holds nothing of this run's.
  expect_contains "$(cat "$dir/state/calls")" "gcloud iam service-accounts delete" "refused grant: the account is deleted"
  expect_contains "$out" "FACT restore: PASS — " "refused grant: the Restore ran"
  expect_bucket_clean "refused grant"
  expect_scratch_gone "refused grant"
}

# The read half arrives late, like the listing half: a single-attempt read would
# record the right form as a failure, so the read that must succeed is retried
# too. The fake refuses the first two reads and answers the listings at once.
test_a_read_that_arrives_late_is_not_a_refusal() {
  setup_case late-read relative
  PROPAGATION=10
  RUN340_ENV=(FAKE340_FLAKY_READ=2)
  run340_default
  expect_status "$status" 0 "late read"
  expect_contains "$out" "FACT relative-reads-runs: PASS — " "late read: the read was waited for"
  expect_contains "$out" "FACT list-prefix: PASS — " "late read: the answer is the relative form"
  expect_bucket_clean "late read"
}

# And when nothing under a form holds, the four refusals are NOT read as passes:
# a "refused" here could be this same grant not being in effect yet, so the rows
# say they were not read. "The grant is narrow" is then an observation the run
# did not make rather than one it assumed.
test_refusals_are_not_read_when_nothing_held() {
  setup_case no-read none
  PROPAGATION=1
  RUN340_ENV=(FAKE340_FLAKY_READ=999)
  run340_default
  expect_status "$status" 1 "nothing held"
  expect_contains "$out" "FACT relative-reads-runs: FAIL — " "nothing held: the read is the failure"
  expect_contains "$out" "FACT relative-lists-runs: FAIL — " "nothing held: and so is the listing"
  expect_contains "$out" "FACT relative-root-refused: INFO — not read" "nothing held: the refusals are not read"
  expect_absent "$out" "FACT relative-root-refused: PASS" "nothing held: and are not recorded as passes"
  expect_absent "$out" "FACT full-tokens-list-refused: PASS" "nothing held: not under either form"
  expect_contains "$out" "FACT list-prefix: FAIL — " "nothing held: neither form listed"
  expect_bucket_clean "nothing held"
}

# The first of the two grants lands and the second is refused: the runs/ binding
# is really on the bucket, and the Restore still has to take it off. What is
# asserted is the bucket policy itself — a Restore that ran only when BOTH grants
# were made would leave the first one behind, which is somebody else's bucket.
test_a_half_granted_pair_is_still_torn_down() {
  setup_case half-granted relative
  RUN340_ENV=(FAKE340_GRANT_REFUSED=second)
  run340_default
  expect_status "$status" 2 "half-granted pair"
  expect_contains "$out" "the bucket refused the roles/storage.objectUser binding" "half-granted pair: the row names the grant that was refused"
  expect_contains "$out" "FACT list-prefix: FAIL — not reached" "half-granted pair: no answer is given"
  expect_contains "$(cat "$dir/state/grants")" "roles/storage.objectViewer" "half-granted pair: the first binding really was granted"
  expect_bucket_clean "half-granted pair"
  expect_scratch_gone "half-granted pair"
}

# A refusal that names a permission of another kind: the row says so, because an
# account holding the two conditioned object roles holds no bucket-level
# permission, and a command asking for one is refused whatever the clause says.
# Read as "this form does not work", that refusal would pick the wrong form.
test_a_refusal_of_another_kind_is_flagged() {
  setup_case other-permission none
  RUN340_ENV=(FAKE340_DENY_PERM=storage.buckets.get)
  run340_default
  # The listings and the read say what the refusal was of another kind, and the
  # four refusals are not read as held: a denial naming storage.buckets.get is
  # not a denial of the clause, so the run stops (2) rather than answering a form
  # on a criterion it never observed.
  expect_status "$status" 2 "another permission"
  expect_contains "$out" "NOTE: the refusal names storage.buckets.get" "another permission: the row says the refusal is of another kind"
  expect_contains "$out" "could not be read as a refusal" "another permission: the refusals are not taken as readings"
  expect_absent "$out" "FACT relative-root-refused: PASS" "another permission: and no refusal row is a pass"
  expect_bucket_clean "another permission"
}

# A refusal that did not ANSWER is not a criterion that held. A 5xx gcloud did
# not retry, an expired session or a killed request all make the command fail
# without refusing anything — and reading that as "the listing is refused" is how
# a criterion nobody observed becomes a PASS, which names a form R12 must not
# write. The run stops instead: exit 2, no form printed as the answer, and the
# criterion's row not reached.
test_a_refusal_that_did_not_answer_is_not_a_pass() {
  setup_case refusal-error relative
  RUN340_ENV=(FAKE340_REFUSE_ERROR=yes)
  run340_default
  expect_status "$status" 2 "refusal error"
  expect_contains "$out" "FACT aborted: FAIL — the root listing of gs://$BUCKET/ could not be read as a refusal" "refusal error: the run says what it could not read"
  expect_contains "$out" "the call failed without naming storage.objects.list" "refusal error: the row names the permission the criterion needed"
  expect_contains "$out" "FACT relative-root-refused: FAIL — not reached" "refusal error: the criterion is not recorded at all"
  expect_absent "$out" "FACT relative-root-refused: PASS" "refusal error: and not as a criterion that held"
  expect_absent "$out" "THE LIST PREFIX R12 MUST USE" "refusal error: no form is printed as the answer"
  expect_bucket_clean "refusal error"
  expect_scratch_gone "refusal error"
}

# A removal that takes effect LATE, between the two forms. A grant propagates
# asynchronously and so does its removal, and both forms speak for the same
# throwaway account: if the relative form failed for a reason of its own while
# its binding was still in force, the full-path form would list and read through
# the EARLIER clause, pass, and be answered as the form the attribute carries —
# the one answer this check must never produce. The fake keeps a removed binding
# in force for a few requests (FAKE340_REMOVE_LATE) and refuses every read
# (FAKE340_FLAKY_READ), so the relative form fails on its read and the second
# form is reached — which is the situation the wait exists for. What the run
# settles here is not the point; what it does NOT settle is.
test_the_second_form_waits_for_the_first_bindings_to_be_gone() {
  setup_case lagging-removal relative
  PROPAGATION=2
  RUN340_ENV=(FAKE340_FLAKY_READ=999 FAKE340_REMOVE_LATE=3)
  run340_default
  expect_status "$status" 1 "lagging removal"
  expect_contains "$out" "FACT relative-reads-runs: FAIL — " "lagging removal: the relative form failed on its read"
  expect_absent "$out" "the FULL PATH form passed" "lagging removal: the second form is not answered through the first form's clause"
  expect_absent "$out" "THE LIST PREFIX R12 MUST USE: projects/_/buckets/" "lagging removal: and the full path is not printed as the answer"
  expect_contains "$out" "FACT full-lists-runs: FAIL — " "lagging removal: the second form really was tried, and refused"
  # The read-back the wait is made of: at least one listing as the account, of
  # the prefix the first form's clause covered, sits between the first removal
  # and the second form's grant. Without the wait there is nothing between them,
  # and the second form's listing is answered by the binding the bucket had not
  # taken off yet.
  between=$(awk '/remove-iam-policy-binding/ {seen=1; next} /add-iam-policy-binding/ {if (seen) exit} seen' "$dir/state/calls")
  expect_contains "$between" "storage ls" "lagging removal: the second form waits for the removal to take effect"
  expect_contains "$between" "--impersonate-service-account" "lagging removal: and reads that as the account and not as the owner"
  expect_bucket_clean "lagging removal"
}

# The same wait, with a bucket that never gets there: the removal is still in
# force when the window closes, so the second form is never granted and the run
# stops as could-not-tell. A verdict here would be an answer about the attribute
# drawn from a bucket that has not finished applying a removal.
test_a_removal_that_never_takes_effect_stops() {
  setup_case removal-stuck relative
  PROPAGATION=1
  RUN340_ENV=(FAKE340_FLAKY_READ=999 FAKE340_REMOVE_LATE=999)
  run340_default
  expect_status "$status" 2 "stuck removal"
  expect_contains "$out" "FACT aborted: FAIL — " "stuck removal: the abort is recorded"
  expect_contains "$out" "still in effect" "stuck removal: the row names what was still in effect"
  expect_contains "$out" "the next form would list and read through the EARLIER clause" "stuck removal: and why that is not an answer"
  expect_contains "$out" "FACT full-lists-runs: FAIL — not reached" "stuck removal: the second form was never tried"
  expect_absent "$out" "THE LIST PREFIX R12 MUST USE" "stuck removal: and no form is answered"
  expect_contains "$out" "FACT restore: PASS — " "stuck removal: what the run did make was still torn down"
  expect_bucket_clean "stuck removal"
}

# A listing that FAILS without naming storage.objects.list is a call that did not
# answer, not a binding that is gone: an expired session, a 5xx gcloud did not
# retry, a killed request. Read as "the removal has taken effect", it grants the
# next form while the earlier clause may still be in force — the one wrong answer
# this check must never produce — so it is retried like a listing that still
# succeeds, and a wait that never sees a real refusal stops as could-not-tell.
# Only a refusal naming the permission the removed clause carried is the reading
# that says the earlier binding is gone.
test_a_listing_that_fails_for_another_reason_does_not_end_the_wait() {
  setup_case ls-error relative
  PROPAGATION=2
  RUN340_ENV=(FAKE340_FLAKY_READ=999 FAKE340_REMOVE_LATE=999 FAKE340_LS_ERROR_AFTER_REMOVE=yes)
  run340_default
  expect_status "$status" 2 "listing error"
  expect_contains "$out" "FACT aborted: FAIL — " "listing error: the abort is recorded"
  expect_contains "$out" "kept failing without naming storage.objects.list" "listing error: the row says what the failure was not"
  expect_contains "$out" "HTTPError 500" "listing error: and quotes the call that did not answer"
  expect_contains "$out" "FACT full-lists-runs: FAIL — not reached" "listing error: the second form was never granted"
  expect_absent "$out" "FACT full-lists-runs: PASS" "listing error: so it cannot list through the earlier clause"
  expect_absent "$out" "THE LIST PREFIX R12 MUST USE" "listing error: and no form is answered"
  expect_bucket_clean "listing error"
}

# The Restore's PASS row claims the bucket policy was read back and no longer
# names the account, so a run whose FIRST form passed the criteria must still
# make that read: remove_bindings has already cleared BINDINGS_GRANTED by then,
# and a read-back hung off that flag printed the sentence on a run that never
# called get-iam-policy at all — a claim about a bucket whose policy nothing had
# looked at.
test_a_passing_run_reads_the_policy_back() {
  setup_case policy-read relative
  run340_default
  expect_status "$status" 0 "policy read-back"
  expect_contains "$out" "FACT restore: PASS — " "policy read-back: the Restore"
  expect_contains "$out" "the policy no longer names the account" "policy read-back: the row makes the claim"
  expect_contains "$(cat "$dir/state/calls")" "gcloud storage buckets get-iam-policy" \
    "policy read-back: and the call the claim rests on was made"
  # The read has to come AFTER the removals, which is the order the claim is
  # about: a policy read before the binding went would answer the old state.
  after=$(awk '/remove-iam-policy-binding/ {seen=1} seen' "$dir/state/calls")
  expect_contains "$after" "gcloud storage buckets get-iam-policy" "policy read-back: after the removals, not before"
  expect_bucket_clean "policy read-back"
}

# A Restore that cannot READ what it deleted must not read as a pass. The four
# prefixes and the bucket policy are read back to make the PASS row a reading
# rather than a claim, and an error from either — a 403, a dropped call — says
# this run cannot tell, not that the thing is gone. The fake denies exactly those
# reads, and only once something of the run's has been removed, which is when the
# policy read-back matters most. The account is not one of them: its evidence is
# the delete call's own answer, which a denied describe cannot take away.
test_a_restore_that_cannot_read_back_does_not_pass() {
  setup_case readback-denied relative
  RUN340_ENV=(FAKE340_READBACK_DENIED=yes)
  run340_default
  expect_status "$status" 1 "unreadable Restore"
  expect_contains "$out" "FACT relative-lists-runs: PASS — " "unreadable Restore: the form itself passed"
  expect_contains "$out" "FACT list-prefix: PASS — " "unreadable Restore: and the answer was reached"
  expect_contains "$out" "FACT restore: FAIL — " "unreadable Restore: the Restore row fails"
  expect_contains "$out" "Not readable, so not confirmed:" "unreadable Restore: the row says what could not be read back"
  expect_contains "$out" "the listing could not be read back" "unreadable Restore: the four prefixes are named"
  expect_contains "$out" "the bucket policy (it could not be read" "unreadable Restore: and so is the policy read-back"
  expect_absent "$out" "were read back: the policy no longer names the account" "unreadable Restore: the pass sentence is not printed on reads that did not answer"
  expect_contains "$out" "CHECK 340 RESULT END (exit 1)" "unreadable Restore: the block does not read as a pass"
  expect_bucket_clean "unreadable Restore"
}

# A delete the bucket refuses is the evidence failing, so the account is not
# confirmed gone and the Restore row FAILs: the delete's own answer is what the
# PASS row rests on, and a refusal is not that answer.
test_a_denied_delete_is_not_a_restore_pass() {
  setup_case delete-denied relative
  RUN340_ENV=(FAKE340_DELETE_REFUSED=yes)
  run340_default
  expect_status "$status" 1 "denied delete"
  expect_contains "$out" "FACT list-prefix: PASS — " "denied delete: the form itself passed"
  expect_contains "$out" "FACT restore: FAIL — " "denied delete: the Restore row fails"
  expect_contains "$out" "the delete call was refused or did not answer" "denied delete: the row says which evidence failed"
  expect_contains "$out" "iam.serviceAccounts.delete" "denied delete: and carries the refusal"
  expect_absent "$out" "the policy no longer names the account" "denied delete: the pass sentence is not printed"
  [ -n "$(find "$dir/state/accounts" -type f)" ] || fail "denied delete: the case did not leave the account behind"
}

# The policy read-back is the one the PASS row makes a claim about ("The bucket
# policy no longer names the account"), so a policy that cannot be READ must not
# be read as one that does not name it. Here the bucket refused the removal
# between the two forms — which is what leaves the Restore's own remove and its
# read-back to do the work — and then refuses to be read back.
test_a_restore_that_cannot_read_the_policy_does_not_pass() {
  setup_case policy-readback-denied relative
  RUN340_ENV=(FAKE340_REMOVE_REFUSED=once FAKE340_READBACK_DENIED=yes)
  run340_default
  expect_status "$status" 2 "unreadable policy"
  expect_contains "$out" "FACT restore: FAIL — " "unreadable policy: the Restore row fails"
  expect_contains "$out" "the bucket policy (it could not be read" "unreadable policy: the policy read-back is named"
  expect_absent "$out" "the policy no longer names the account" "unreadable policy: the pass sentence is not printed on a policy nothing read"
  expect_contains "$out" "CHECK 340 RESULT END (exit 2)" "unreadable policy: the aborted run does not read as a pass"
  expect_bucket_clean "unreadable policy"
}

# A removal the bucket refuses, between the two forms: the previous form's
# conditioned binding is still on the bucket, so the full-path attempt would list
# through the RELATIVE clause and the check would answer the wrong form. It stops
# as could-not-tell instead, and the Restore's own read of the bucket policy is
# what says whether it could take the binding off in the end — here the fake
# refuses that too, so the Restore row FAILs and says what is still there.
test_a_refused_removal_stops_instead_of_answering_the_wrong_form() {
  setup_case remove-refused relative
  RUN340_ENV=(FAKE340_REMOVE_REFUSED=yes)
  run340_default
  expect_status "$status" 2 "refused removal"
  expect_contains "$out" "FACT aborted: FAIL — " "refused removal: the abort is recorded"
  expect_contains "$out" "the bucket refused to remove the roles/storage.objectViewer binding" "refused removal: the row names the removal that was refused"
  expect_absent "$out" "FACT full-lists-runs: PASS" "refused removal: the next form is not tried through the earlier clause"
  expect_absent "$out" "THE LIST PREFIX R12 MUST USE" "refused removal: and no form is answered"
  expect_contains "$out" "FACT list-prefix: FAIL — not reached" "refused removal: no answer is given"
  [ -s "$dir/state/bindings" ] || fail "refused removal: the case did not leave the first form's binding on the bucket"
  expect_contains "$out" "FACT restore: FAIL — " "refused removal: the Restore says the binding is still there"
  expect_contains "$out" "the run's own bucket bindings" "refused removal: and names what to remove by hand"
}

# The same refusal, with the removal working again by the time the Restore runs:
# the trap retries what the form could not do, and the read-back is what makes
# the Restore a pass rather than a claim. The fake refuses the removal only the
# first time it is asked, which is what a transient refusal looks like.
test_a_refused_removal_the_restore_can_still_do() {
  setup_case remove-once relative
  RUN340_ENV=(FAKE340_REMOVE_REFUSED=once)
  run340_default
  expect_status "$status" 2 "refused removal once"
  expect_contains "$out" "the bucket refused to remove the roles/storage.objectViewer binding" "refused removal once: the run stops on it"
  expect_contains "$out" "FACT restore: PASS — " "refused removal once: the Restore took it off"
  expect_bucket_clean "refused removal once"
}

# A bucket named with its gs:// prefix and a trailing slash is the same bucket:
# the two arguments are the ones a person types.
test_a_gs_prefix_and_a_trailing_slash_are_accepted() {
  setup_case typed relative
  run340 --project "$PROJECT" --bucket "gs://$BUCKET/"
  expect_status "$status" 0 "gs:// and a trailing slash"
  expect_contains "$out" "FACT bucket: PASS — $BUCKET " "gs:// and a trailing slash: the bucket is normalized"
  expect_contains "$out" "FACT list-prefix: PASS — " "gs:// and a trailing slash: the run still answers"
  expect_bucket_clean "gs:// and a trailing slash"
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

t test_the_relative_form_passes
t test_the_full_path_form_passes
t test_neither_form_listing_is_a_fail
t test_a_multi_line_error_is_quoted_in_full
t test_a_reports_listing_that_fails_names_the_binding
t test_a_grant_wider_than_the_two_prefixes_is_a_fail
t test_a_root_listing_that_is_allowed_is_a_fail
t test_a_restore_that_could_not_delete_is_a_fail
t test_a_passing_run_reads_the_policy_back
t test_a_restore_that_cannot_read_back_does_not_pass
t test_a_denied_delete_is_not_a_restore_pass
t test_a_restore_that_cannot_read_the_policy_does_not_pass
t test_the_second_form_waits_for_the_first_bindings_to_be_gone
t test_a_removal_that_never_takes_effect_stops
t test_a_listing_that_fails_for_another_reason_does_not_end_the_wait
t test_a_grant_that_arrives_late_is_not_a_refusal
t test_a_read_that_arrives_late_is_not_a_refusal
t test_refusals_are_not_read_when_nothing_held
t test_a_refused_grant_is_could_not_tell
t test_a_half_granted_pair_is_still_torn_down
t test_a_refusal_of_another_kind_is_flagged
t test_a_refusal_that_did_not_answer_is_not_a_pass
t test_a_refused_removal_stops_instead_of_answering_the_wrong_form
t test_a_refused_removal_the_restore_can_still_do
t test_belong_is_refused_in_both_places
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_a_bucket_that_cannot_be_reached_stops
t test_an_account_that_already_exists_stops
t test_a_denied_describe_is_could_not_read_back
t test_usage_and_arguments
t test_a_gs_prefix_and_a_trailing_slash_are_accepted

if [ "$failures" -ne 0 ]; then
  echo "check340_test: $failures failure(s)" >&2
  exit 1
fi
echo "check340_test: all tests passed" >&2
