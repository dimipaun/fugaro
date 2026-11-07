#!/usr/bin/env bash
# Check 340 — the prefix-limited listing grant (R12's, and it gates R12), as ONE
# script YOU run.
#
# It settles `<list prefix>` — the one value R12's Terraform waits for: whether
# the `storage.googleapis.com/objectListPrefix` request attribute carries the
# request's own prefix (`runs/`) or the full
# `projects/_/buckets/<bucket>/objects/runs/` path. `docs/design/bucket-iam.md`
# §4 (the H3 veto) and its §11 item **V7** record the attribute as unverified,
# and no code in this repo reads it: grepping the tree finds it in the design
# alone. R12 writes one conditioned binding per prefix for the reports job, each
# carrying the `resource.name` half (the reads) and the listing half (the
# listing), and the two clause kinds are joined by `||` — never by `&&`, which
# matches nothing. The listing half's scope is `runs/` and `reports/`, and the
# wrong form of the attribute is a grant that lists nothing, which is why this
# check runs BEFORE that Terraform lands.
#
# What it asserts, as the throwaway account, under each candidate form:
# `runs/<slug>/` lists, `reports/` lists, one `runs/` object reads, the root
# listing is refused, a `tokens/` read is refused, and a `tokens/` and a
# `cache/` listing are refused. The form under which all seven hold is the one
# R12's builder must write, and the block names it in one line. **If neither form
# lists, R12's Terraform must not land and the plan stops and asks** (ruling E4);
# the bucket-wide viewer a listing would otherwise need is deliberately NOT a
# fallback here, because it reads `tokens/` and `cache/` too.
#
# A grant the bucket refuses is NOT a form that does not list: the run stops with
# exit 2 and an `aborted` row rather than reading a permission error as the
# attribute's own answer, because every reading under a grant that never landed
# would be a refusal for a reason that has nothing to do with the clause. The
# four refusals are read the same way round: a command that fails is a criterion
# only when the failure IS the refusal — a denial naming the permission the
# clause would have carried — so a call that did not answer (a 5xx gcloud did
# not retry, an expired session, a killed request, a denial of another kind)
# stops the run rather than being recorded as a criterion that held, which would
# name a form R12 must not write on a reading nobody took. A
# removal the bucket refuses, and a removal that has not TAKEN EFFECT yet between
# the two forms, stop it the same way: both forms speak for the same throwaway
# account, so the full-path form granted while the relative binding is still in
# force would list and read through the earlier clause and be answered as the
# form the attribute carries — the one answer this check must never produce.
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 340".
#
# Usage:      bash scripts/live-checks/check340.sh --project <project> --bucket <bucket>
#             --project is the project the bucket lives in (and the project the
#             throwaway account is created in); --bucket is a SANDBOX bucket,
#             with or without the `gs://` prefix. Both are required: the check
#             grants IAM on the bucket and creates an account in the project,
#             so it never reads either from a config that may name another.
#             `--dry-run` prints every command a real run would make and runs
#             nothing at all.
# Needs:      bash, mktemp, sed and gcloud on PATH. Nothing is installed and
#             nothing is fetched; if one of those is missing the script stops
#             with one line naming it.
# Where:      a SANDBOX installation only, and NEVER the installation named
#             `belong`: that name is refused in the project and in the bucket
#             before anything is created, and the refusal is what the check's own
#             test pins. The bucket is read back with YOUR own identity first, so
#             a mistyped bucket is a stop rather than a reading.
# Creates:    one throwaway service account in the project
#             (`fugaro-chk340-<hex>`, absent before the run and refused if it is
#             already there), the two conditioned bucket bindings of R12 for it —
#             `roles/storage.objectViewer` with the `runs/` clause and
#             `roles/storage.objectUser` with the `reports/` clause — the
#             `roles/iam.serviceAccountTokenCreator` grant the impersonation
#             needs for YOUR account on that account alone, and four objects of
#             this check's own under `runs/<slug>/`, `reports/<slug>/`,
#             `tokens/<slug>/` and `cache/<slug>/`. Every one of them is deleted
#             by the Restore, which runs from the exit trap too.
# Costs:      nothing billable and no model call: a handful of IAM writes and
#             four small objects in a bucket.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C or on a failed assertion: the four
#             seeded prefixes are removed, both bindings are removed with `--all`
#             (so it does not matter which form was granted when the run
#             stopped), your impersonation grant goes, and the service account is
#             deleted — bindings first, because after the account is deleted its
#             members read `deleted:serviceAccount:…` and the removes fail. The
#             Restore reads all four prefixes back afterwards, and — whenever it
#             made an account — the bucket policy and the account too, and says
#             what it found: the policy read is what the PASS row's own sentence
#             rests on, so it is not skipped when an earlier step already removed
#             the bindings. Anything that did not go, and anything it could not
#             read back at all, is a FAILed row, not a silent pass. The scratch
#             directory is removed with the run.
# Output:     progress on stderr, ONE block on stdout headed CHECK 340 RESULT,
#             and one line above it naming the `<list prefix>` R12 must use, to
#             copy back to the coordinator. Exit 0 when a form passed every
#             criterion AND the Restore confirmed it deleted everything this
#             check made, 1 when none did — or when one did and the Restore could
#             not confirm what it deleted, which the block's `restore` row names
#             and which you then have to finish by hand — and 2 when the run
#             could not tell (a guard stopped it, a grant or a removal the bucket
#             refused, a refusal that came back as a call that did not answer, a
#             removal that had not taken effect in time, a tool is missing, an
#             internal error ended it).
#
# No credential is handled anywhere in it: the account this check creates holds
# no key and is granted nothing outside the bucket's two prefixes, every read is
# made by impersonating it, and the object it seeds under `tokens/` is a marker
# this check itself wrote — so nothing here is worth hiding from a `bash -x`, and
# no secret-shaped value is read, printed, traced or pasted.
#
# CHECK340_PROPAGATION_SECONDS and CHECK340_POLL_SECONDS exist for this script's
# own test: they shorten the wait for a fresh grant to take effect, and neither
# can point the check at another project or another bucket.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by step_verdict once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
# The line the block prints above its header: the answer R12's builder needs.
ANSWER=""
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as an answer about the listing attribute for a
# run that measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the listing attribute"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 340 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 340 needs bash"
  need mktemp
  need sed "the condition file the two bindings carry is JSON, and the expression's own quotes are escaped into it with sed"
  need gcloud "every call this check makes is a gcloud one: the two bucket bindings, the throwaway account, the four seeded objects, the read-back, and the reads as that account"
}

# How long a just-made grant is given to take effect before a listing that must
# succeed is recorded as a failure, and how long between attempts. IAM grants and
# their conditions propagate asynchronously (§11 item V5), and reading a fresh
# grant as a refusal would pick the WRONG form of the attribute — which is the
# one thing this check exists to get right.
PROPAGATION_SECONDS=${CHECK340_PROPAGATION_SECONDS:-150}
POLL_SECONDS=${CHECK340_POLL_SECONDS:-10}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk340-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""
# This run's own names, the records the Restore reads rather than assumes, and
# the two answers the steps fill in.
SA_ID=""
SA_EMAIL=""
SLUG=""
RUN=""
OWNER=""
ACCOUNT_MADE=no
GRANT_MADE=no
BINDINGS_GRANTED=no
SEEDED=no
PASSED_FORM=""
# What the form just tried left behind: try_form's answer, and never its exit
# status — see try_form for why it is not called from a condition.
FORM_OK=no
HELPED=no

# rand_hex: four hex digits from bash's own $RANDOM, so this run's names are its
# own without another tool.
rand_hex() { printf '%04x' "$RANDOM"; }

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk340.XXXXXXXX") || die "Check 340 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  COND_RUNS="$SCRATCH/condition-runs.json"
  COND_REPORTS="$SCRATCH/condition-reports.json"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 340 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  # A name of this run's own: the account is the one thing a stopped run could
  # leave behind, and a unique one is never a name an earlier run — or the
  # installation — already holds. That is also what makes "this account can reach
  # nothing" a fresh reading: no grant of an earlier run's is still in effect for
  # it.
  SA_ID="fugaro-chk340-$(rand_hex)$(rand_hex)"
  SA_EMAIL="$SA_ID@$P.iam.gserviceaccount.com"
  # The slug and the run id are the check's own too, so the four seeded objects
  # land inside the real prefixes without touching a repository's objects, and
  # the Restore deletes exactly `runs/<slug>/`, `reports/<slug>/`,
  # `tokens/<slug>/` and `cache/<slug>/`.
  SLUG="chk340-$(rand_hex)$(rand_hex)"
  RUN="chk340-$(rand_hex)$(rand_hex)"
  YEAR=$(date -u +%Y)
  PERIOD=$(date -u +%Y-%m)
  RUN_OBJ="runs/$SLUG/$RUN/result.json"
  REPORT_OBJ="reports/$SLUG/$YEAR/$PERIOD.md"
  TOKEN_OBJ="tokens/$SLUG/$RUN/budget-token"
  CACHE_OBJ="cache/$SLUG/chk340/build.tar.zst"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  say "this run's own names: the account $SA_EMAIL, and the four prefixes runs/$SLUG/, reports/$SLUG/, tokens/$SLUG/ and cache/$SLUG/"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  restore_run
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before the
  # block is printed.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# --------------------------------------------------------------- the Restore
# The Restore deletes what this check created and reads it back afterwards. It
# runs from the exit trap, so it has to tolerate a run that stopped anywhere: the
# seeded prefixes are removed wholesale, which is safe whatever was copied, and
# every command is guarded — a delete that fails must not end the shell before
# the block is printed.
restore_run() {
  if [ "$ACCOUNT_MADE" != yes ] && [ "$SEEDED" != yes ] && [ "$BINDINGS_GRANTED" != yes ]; then
    fact restore PASS "nothing of this check's was created (no object, no account, no binding), so there is nothing to delete"
    return 0
  fi
  say "Restore: deleting what this check created"
  local bad="" unknown="" out="" prefix
  # 1. The four seeded prefixes. `rm -r` names this run's own slug, so no other
  #    repository's or run's objects are ever in its path.
  if [ "$SEEDED" = yes ]; then
    gcloud storage rm -r --project="$P" \
      "gs://$B/runs/$SLUG/" "gs://$B/reports/$SLUG/" \
      "gs://$B/tokens/$SLUG/" "gs://$B/cache/$SLUG/" >&2 || true
    for prefix in "runs/$SLUG/" "reports/$SLUG/" "tokens/$SLUG/" "cache/$SLUG/"; do
      # Only the empty listing counts as gone. Any other error is recorded as
      # not-read rather than as deleted: a 403, a dropped connection or a
      # refused read tells this run nothing about the objects, and reading it as
      # "gone" would print a PASS on the teardown of a bucket that may still
      # hold them.
      if out=$(gcloud storage ls --recursive --project="$P" "gs://$B/$prefix" 2>&1); then
        bad="$bad $prefix"
      elif ! printf '%s' "$out" | grep -qiE 'matched no objects|not found|404'; then
        unknown="$unknown $prefix (the listing could not be read back: $(first_line "$out"))"
      fi
    done
  fi
  # 2. Both bucket bindings, with `--all`: no condition is named, so it does not
  #    matter which candidate form was granted when the run stopped. The removes
  #    are still keyed on BINDINGS_GRANTED — a form that PASSED took its own off
  #    through remove_bindings, which clears the flag, and a flag still set is
  #    the one case where this run has not already tried. The READ-BACK below is
  #    keyed on the ACCOUNT instead: it is what the PASS row's own sentence
  #    ("the policy no longer names the account") rests on, and reading the
  #    policy only while the flag happened to be set is how a kept run of the
  #    relative form could print that sentence without one call that read it.
  if [ "$ACCOUNT_MADE" = yes ]; then
    if [ "$BINDINGS_GRANTED" = yes ]; then
      local role
      for role in roles/storage.objectViewer roles/storage.objectUser; do
        gcloud storage buckets remove-iam-policy-binding "gs://$B" --project="$P" \
          --member="serviceAccount:$SA_EMAIL" --role="$role" --all >&2 || true
      done
    fi
    # The policy is read into a variable rather than piped into `grep`: a pipe
    # would make a refused or unreadable policy indistinguishable from one that
    # does not name the account, and the PASS row below asserts the account is
    # not in it.
    if out=$(gcloud storage buckets get-iam-policy "gs://$B" --project="$P" 2>&1); then
      if printf '%s' "$out" | grep -q "$SA_EMAIL"; then
        bad="$bad the run's own bucket bindings"
      fi
    else
      unknown="$unknown the bucket policy (it could not be read to check for them: $(first_line "$out"))"
    fi
  fi
  # 3. Your impersonation grant, then the account itself.
  if [ "$GRANT_MADE" = yes ] && [ -n "$OWNER" ]; then
    gcloud iam service-accounts remove-iam-policy-binding "$SA_EMAIL" --project="$P" \
      --member="user:$OWNER" --role=roles/iam.serviceAccountTokenCreator --all >&2 || true
  fi
  if [ "$ACCOUNT_MADE" = yes ]; then
    gcloud iam service-accounts delete "$SA_EMAIL" --project="$P" --quiet >&2 || true
    # NOT_FOUND is the delete having worked; anything else is this run not being
    # able to tell, and not-telling is recorded as a FAIL, never as gone.
    if out=$(gcloud iam service-accounts describe "$SA_EMAIL" --project="$P" 2>&1); then
      bad="$bad the service account $SA_EMAIL"
    elif ! printf '%s' "$out" | grep -qiE 'not found|404'; then
      unknown="$unknown the service account $SA_EMAIL (it could not be read back to check: $(first_line "$out"))"
    fi
  fi
  if [ -n "$bad" ] || [ -n "$unknown" ]; then
    fact restore FAIL "this check's Restore could not confirm that it deleted everything.${bad:+ Still there:$bad.}${unknown:+ Not readable, so not confirmed:$unknown.} Do this by hand before running any other check on this bucket: the four seeded prefixes are under the slug $SLUG, and the account is $SA_EMAIL, holding a conditioned objectViewer on runs/ and a conditioned objectUser on reports/. Whatever is not named here was deleted"
    return 0
  fi
  # The PASS row says only what was READ: an account this run made is described
  # and its policy read above (a read that failed is a FAILed row, not this one),
  # while a run that never got as far as an account has no binding and no policy
  # clause of its own to read back, and saying otherwise is the claim this row
  # exists to stop being a claim.
  local read_back
  if [ "$ACCOUNT_MADE" = yes ]; then
    read_back=", and all four prefixes, the bucket policy and the account were read back: the policy no longer names the account"
  else
    read_back=", and all four prefixes were read back (this run never made an account, so it left no binding and no policy clause of its own behind)"
  fi
  fact restore PASS "this check's own four prefixes (runs/$SLUG/, reports/$SLUG/, tokens/$SLUG/, cache/$SLUG/), its two conditioned bindings and its service account $SA_EMAIL are gone${read_back}"
}

# ------------------------------------------------------------------ the guards
# guard_belong: the installation named `belong` is the owner's own, and this
# check grants IAM on a bucket and writes objects into it — so the name is
# refused in the project and in the bucket, before anything is created. It is a
# substring test on purpose: a project or bucket that merely contains the name is
# refused too, and the message says which one matched.
guard_belong() {
  local name
  for name in "$P" "$B"; do
    case "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" in
    *belong*)
      fact belong FAIL "'$name' names the owner's own installation: Check 340 creates an account, grants bucket IAM and writes four objects, and it is a SANDBOX check"
      stop "the name '$name' matches the installation 'belong', which this check must never touch"
      ;;
    esac
  done
  fact belong PASS "neither the project ($P) nor the bucket ($B) names the owner's own installation (nothing typed contains 'belong')"
}

# guard_bucket_reachable: the bucket is read back with YOUR identity first. A
# mistyped or unreadable bucket answers 403 to every step below, and a bucket-wide
# denial is not what this check measures — it would read as "both forms are
# refused" and the run would end with a verdict about a bucket that is not there.
guard_bucket_reachable() {
  local out
  if ! out=$(gcloud storage ls --project="$P" "gs://$B/" 2>&1); then
    # The FAIL is recorded before the stop: this guard RAN and decided, and the
    # block's "not reached" row is reserved for a step that was never measured.
    fact bucket-reachable FAIL "gs://$B/ is not listable by your own identity, so this check has nothing to measure: every reading below would be a refusal about a bucket that is not there rather than about the listing attribute (gcloud's own line: $(first_line "$out")). Check the bucket name, and that you are signed in as an operator of $P"
    stop "gs://$B/ is not listable by your own identity, so this check has nothing to measure (gcloud's own line is above it). Check the bucket name, and that you are signed in as an operator of $P"
  fi
  fact bucket-reachable PASS "gs://$B/ is there and listable by your own identity, so every reading below is about the throwaway account and not about a bucket that cannot be reached ($(printf '%s' "$out" | grep -c . || true) entries at the root)"
}

guard_account_absent() {
  if gcloud iam service-accounts describe "$SA_EMAIL" --project="$P" >/dev/null 2>&1; then
    fact account FAIL "the name this run drew, $SA_EMAIL, is already taken in $P: Check 340 never adopts an account it did not create, so it stops rather than reading an account whose grants are somebody else's. Delete that account and its bindings, or run the check again, which picks a fresh name"
    stop "$SA_EMAIL already exists in $P. Check 340 never adopts an account it did not create: delete it and its bindings, or run the check again, which picks a fresh name"
  fi
  fact account PASS "the throwaway account $SA_EMAIL is absent from $P, so the one this run makes is its own"
}

# ------------------------------------------------------------- the definitions
# The two clause kinds, byte for byte the shapes R12 writes. `clause_read` is
# `gcp.projectBucketPrefixClause`'s expression (internal/backend/gcp/names.go),
# and `clause_list` is the listing half whose value form this check settles:
# `startsWith("<list prefix>")` over the request attribute, with the empty-string
# default the `api.getAttribute` call needs for a request that carries no
# attribute at all.
clause_read() { printf 'resource.name.startsWith("projects/_/buckets/%s/objects/%s/")' "$B" "$1"; }
clause_list() {
  printf "api.getAttribute('storage.googleapis.com/objectListPrefix', '').startsWith(\"%s\")" "$1"
}

# The two candidate forms of `<list prefix>`, each reached through its own
# function so that a form is a name here and nothing else.
relative_prefix() { printf '%s/' "$1"; }
full_prefix() { printf 'projects/_/buckets/%s/objects/%s/' "$B" "$1"; }

# condition_json FILE EXPRESSION TITLE — the condition as the JSON
# `--condition-from-file` reads. A file rather than `--condition KEY=VALUE`
# because the listing clause carries a comma inside `api.getAttribute(name, '')`
# and gcloud splits a KEY=VALUE list on commas (its documented way out is a
# custom delimiter, which a file makes unnecessary). The expression keeps the
# product's own double-quoted bytes, escaped into the JSON here rather than
# switched to single quotes.
condition_json() {
  local file=$1 expression=$2 title=$3 escaped
  escaped=$(printf '%s' "$expression" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')
  printf '{"title": "%s", "expression": "%s"}\n' "$title" "$escaped" >"$file"
}

# grant_bindings LIST_RUNS LIST_REPORTS — R12's two conditioned bindings for the
# throwaway account, one per prefix, never one binding carrying two prefixes: the
# objectViewer on `runs/` carries the read clause and the listing clause, and so
# does the objectUser on `reports/`, and in each the two clause kinds are joined
# by `||`. A conjunction matches nothing — a `get` carries no `objectListPrefix`
# and a `list` names no object — which is why a `&&` shape fails every request
# this check makes.
grant_bindings() {
  local lruns=$1 lreports=$2
  condition_json "$COND_RUNS" "$(clause_read runs) || $(clause_list "$lruns")" "fugaro-chk340-runs"
  condition_json "$COND_REPORTS" "$(clause_read reports) || $(clause_list "$lreports")" "fugaro-chk340-reports"
  say "granting roles/storage.objectViewer on runs/ and roles/storage.objectUser on reports/, each with its own listing clause"
  # Recorded BEFORE the first grant: a first binding that landed and a second
  # that did not still leaves a binding of this run's on the bucket, and the
  # Restore has to remove it. Setting this after both calls would leave exactly
  # that case behind.
  BINDINGS_GRANTED=yes
  # A grant that is REFUSED is a could-not-tell and never a verdict: the run has
  # measured nothing about the attribute, and every reading below it would be a
  # refusal for a reason that is not the listing clause. It stops here, by name,
  # and the Restore still runs from the exit trap.
  if ! gcloud storage buckets add-iam-policy-binding "gs://$B" --project="$P" \
    --member="serviceAccount:$SA_EMAIL" --role=roles/storage.objectViewer \
    --condition-from-file="$COND_RUNS" >&2; then
    stop "the bucket refused the roles/storage.objectViewer binding on the runs/ prefix (gcloud's own words are the line above), so nothing below it was measured: the account has no grant to read with, and a refusal recorded under it would say nothing about the listing attribute"
  fi
  if ! gcloud storage buckets add-iam-policy-binding "gs://$B" --project="$P" \
    --member="serviceAccount:$SA_EMAIL" --role=roles/storage.objectUser \
    --condition-from-file="$COND_REPORTS" >&2; then
    stop "the bucket refused the roles/storage.objectUser binding on the reports/ prefix (gcloud's own words are the line above), so nothing below it was measured: the account has no grant to read with, and a refusal recorded under it would say nothing about the listing attribute"
  fi
}

# remove_bindings: both of them, and whichever condition they carry. A REFUSED
# removal is a stop and not a `|| true`: a binding still on the bucket is a grant
# the next form would list through, and this check would answer the wrong form
# with no row saying a teardown had failed. It stops as could-not-tell and leaves
# BINDINGS_GRANTED set, so the trap's Restore retries and reads the bucket policy
# back into its own FAIL row. A removal that SUCCEEDS is not the same thing as a
# removal that has taken effect — wait_for_removal, below, is what holds the next
# form back until it has.
remove_bindings() {
  local role
  for role in roles/storage.objectViewer roles/storage.objectUser; do
    if ! gcloud storage buckets remove-iam-policy-binding "gs://$B" --project="$P" \
      --member="serviceAccount:$SA_EMAIL" --role="$role" --all >&2; then
      stop "the bucket refused to remove the $role binding this form granted (gcloud's own words are the line above), so the next form would list through THIS form's clause and the answer would name the wrong form. The Restore below tries again and reads the bucket policy back"
    fi
  done
  BINDINGS_GRANTED=no
}

# ------------------------------------------------------------- the criteria
# Every read below is made by IMPERSONATING the throwaway account, so what it
# answers is that account's reach and never yours.

LAST_ERROR=""
LIST_OUT=""
list_as_sa() { # list_as_sa URL — the recursive listing, as the account
  gcloud storage ls --recursive --project="$P" \
    --impersonate-service-account="$SA_EMAIL" "$1" 2>&1
}

# cat_as_sa URL — the object's bytes go to /dev/null (the seeded marker is this
# check's own, and nothing a grant reads is worth printing anyway) while gcloud's
# own words reach the caller, which is what must_refuse records, and the exit
# status is gcloud's.
cat_as_sa() {
  gcloud storage cat --project="$P" \
    --impersonate-service-account="$SA_EMAIL" "$1" >/dev/null
}

# poll_list URL: the listing, retried for up to PROPAGATION_SECONDS. A grant that
# has not taken effect yet answers 403, and a 403 read as a refusal would fail
# the form that is really the right one — so a listing that MUST succeed is
# waited for. The listing is left in LIST_OUT on success and the last refusal's
# own words in LAST_ERROR, both in the CALLER's shell: this is never called
# through `$( )`, because a command substitution runs in a subshell and an
# assignment made in one dies with it — which is how a FAIL whose detail is
# supposed to quote gcloud ends up quoting nothing.
poll_list() {
  local url=$1 out deadline
  LIST_OUT=""
  LAST_ERROR=""
  deadline=$((SECONDS + PROPAGATION_SECONDS))
  while :; do
    if out=$(list_as_sa "$url"); then
      LIST_OUT=$out
      return 0
    fi
    LAST_ERROR=$out
    if [ "$SECONDS" -ge "$deadline" ]; then return 1; fi
    sleep "$POLL_SECONDS"
  done
}

# poll_read URL: the read half of the same grant, waited for the same way. It has
# to be waited for as well: the read clause and the listing clause ride the same
# fresh binding, and a read refused because the grant has not arrived yet is the
# same wrong answer a listing refused too early would be.
poll_read() {
  local url=$1 err="$SCRATCH/last-error" deadline
  LAST_ERROR=""
  deadline=$((SECONDS + PROPAGATION_SECONDS))
  while :; do
    if cat_as_sa "$url" 2>"$err"; then
      return 0
    fi
    LAST_ERROR=$(cat "$err" 2>/dev/null || true)
    if [ "$SECONDS" -ge "$deadline" ]; then return 1; fi
    sleep "$POLL_SECONDS"
  done
}

# wait_for_removal: hold the second form back until the first form's bindings are
# really gone. A grant propagates asynchronously (§11 V5) and so does its
# REMOVAL, and both forms speak for the same throwaway account: between the
# remove and the bucket catching up, the previous form's clauses are still in
# force. The relative form can fail for a reason that has nothing to do with the
# attribute — a criterion read past its ${PROPAGATION_SECONDS}s window — and then
# the full-path form would list and read through the STILL-ACTIVE relative
# clause, pass, and be answered as the form the attribute carries. That is the
# one answer this check must never produce, so the next form is not granted until
# this account is REFUSED the listing the previous form allowed AND refused for
# that reason: the failure has to be a permission denial NAMING
# storage.objects.list, the permission the clause just removed carried. A failure
# that names it for another permission, or names no permission at all — a session
# that expired, a 5xx gcloud did not retry, a killed call, and gcloud's own
# command prefix (`(gcloud.storage.ls)`, whose dots read like a permission) — is
# a call that did not answer rather than a binding that is gone, and reading it
# as gone is the same wrong answer from the other side: it is retried like a
# listing that still succeeds, and the stop below quotes it. A bucket that never
# gets there, and a call that never answers, are a stop (exit 2), never a
# verdict — the same rule a grant that never arrives follows.
wait_for_removal() {
  local deadline out named still_lists=no last=""
  deadline=$((SECONDS + PROPAGATION_SECONDS))
  while :; do
    if out=$(list_as_sa "gs://$B/runs/$SLUG/"); then
      still_lists=yes
    else
      named=$(missing_permission "$out")
      case "$named" in
      *storage.objects.list*) return 0 ;;
      esac
      still_lists=no
      last=$out
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      if [ "$still_lists" = yes ]; then
        stop "the bindings the form just tried are still in effect ${PROPAGATION_SECONDS}s after the bucket accepted their removal: the bucket still lists runs/$SLUG/ to the throwaway account, so the next form would list and read through the EARLIER clause and this check would answer the wrong form. Nothing was measured under the next form; wait for the removal to propagate and run the check again"
      fi
      stop "the removal of the bindings the form just tried could not be CONFIRMED in ${PROPAGATION_SECONDS}s: the account's listing of runs/$SLUG/ kept failing without naming storage.objects.list, the permission the removed clause carried (gcloud last said: $(first_line "$last")) — that is a call that did not answer, not a binding that is gone, and nothing this check reads can tell the two apart. Nothing was measured under the next form; check that gcloud can reach the project and run the check again"
    fi
    sleep "$POLL_SECONDS"
  done
}

first_line() { printf '%s\n' "$1" | grep -m1 . | cut -c1-300 || true; }

# missing_permission REFUSAL — the storage permissions gcloud's own refusal
# names, when it names any. It is here because a refusal that names something
# OTHER than storage.objects.list is not this check's answer: an account holding
# the two conditioned object roles holds no bucket-level permission, so a command
# that asks for one is refused whatever the listing clause says, and that reading
# must not be taken for "this form does not work".
missing_permission() {
  printf '%s' "$1" | grep -oE 'storage\.[a-zA-Z.]+' | sort -u | tr '\n' ' ' || true
}

must_list() { # must_list KEY URL WANT
  local key=$1 url=$2 want=$3 named extra=""
  if ! poll_list "$url"; then
    named=$(missing_permission "$LAST_ERROR")
    case "$named" in
    "" | *storage.objects.list*) ;;
    *) extra=" — NOTE: the refusal names ${named}and not storage.objects.list, so this listing was refused for a reason other than the clause this check settles: read it before taking it as 'this form does not work'" ;;
    esac
    fact "$key" FAIL "the account could not list $url: the prefix is not covered by this form's listing clause, or the grant had not taken effect within ${PROPAGATION_SECONDS}s (gcloud said: $(first_line "$LAST_ERROR"))${extra}"
    return 1
  fi
  case "$LIST_OUT" in
  *"$want"*)
    fact "$key" PASS "the account listed $url and the listing names $want, so this form's listing clause covers it"
    return 0
    ;;
  *)
    fact "$key" FAIL "the account listed $url but the listing does not name $want (it answered: $(first_line "$LIST_OUT"))"
    return 1
    ;;
  esac
}

# must_read KEY URL: the read half of the same grant, and it is retried too.
must_read() {
  local key=$1 url=$2 named extra=""
  if poll_read "$url"; then
    fact "$key" PASS "the account read $url, so this form's resource.name clause covers the reads the reports job makes"
    return 0
  fi
  named=$(missing_permission "$LAST_ERROR")
  case "$named" in
  "" | *storage.objects.get*) ;;
  *) extra=" — NOTE: the refusal names ${named}and not storage.objects.get, so this read was refused for a reason other than the clause this check settles: read it before taking it as 'this form does not work'" ;;
  esac
  fact "$key" FAIL "the account could not read $url within ${PROPAGATION_SECONDS}s, which its resource.name clause has to cover: the reports job reads the records it summarises, and a listing half that cannot read an object is half a grant (gcloud said: $(first_line "$LAST_ERROR"))${extra}"
  return 1
}

# must_refuse KEY WHAT PERMISSION CMD...: the four refusals. A refusal is the
# criterion, so a command that SUCCEEDS here is the failure — and its own words
# are what the detail carries, because a grant that is too wide has to say what
# it opened.
#
# A command that FAILS is not the reading yet: the failure has to BE the refusal,
# a denial naming the permission the missing clause would have carried. A call
# that did not answer — a 5xx gcloud did not retry, an expired session, a killed
# request, a denial naming another permission — says nothing about the clause, and
# recording it as "refused" would report a criterion that held when it was never
# observed. Those stop the run instead, which is the same reading the wait between
# the two forms gives a listing that did not answer.
#
# These are read after a listing that must succeed already did, so "refused" can
# never be the grant simply not being in effect yet: by this point the account has
# listed, under this form, with the same two bindings.
must_refuse() {
  local key=$1 what=$2 perm=$3 out named
  shift 3
  if out=$("$@" 2>&1); then
    fact "$key" FAIL "$what was ALLOWED to the account under this form, which is wider than R12's two prefixes: a grant that opens it is not the grant the reports job is written against (what came back: $(first_line "$out"))"
    return 1
  fi
  named=$(missing_permission "$out")
  case "$named" in
  *"$perm"*) ;;
  *) stop "$what could not be read as a refusal: the call failed without naming $perm, the permission the clause this criterion is about would have carried, so it is a call that did not answer — an expired session, a 5xx gcloud did not retry, a killed request or a denial naming another permission all read like this, and none of them is a reading about this form's clause (gcloud said: $(first_line "$out")). The form is neither PASSing nor failing here: nothing was measured under it, so run the check again" ;;
  esac
  fact "$key" PASS "$what is refused to the account under this form, and refused for the reason the criterion is about — the denial names $perm (gcloud: $(first_line "$out"))"
  return 0
}

# ------------------------------------------------------------------- the steps
step_owner() {
  OWNER=$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | head -1)
  if [ -z "$OWNER" ]; then
    stop "no active gcloud account, so there is nobody to give the impersonation grant to and nobody to impersonate as. Run 'gcloud auth login' first"
  fi
  say "your own account, which the impersonation grant is made to: $OWNER"
}

step_account() {
  say "creating the throwaway account $SA_EMAIL"
  gcloud iam service-accounts create "$SA_ID" --project="$P" \
    --display-name="Check 340 throwaway (safe to delete)" >&2
  ACCOUNT_MADE=yes
  # The impersonation grant goes to YOU on this account alone, and the Restore
  # removes it: an installation's own grants do not give operators TokenCreator on
  # an account, and this check impersonates nothing else.
  gcloud iam service-accounts add-iam-policy-binding "$SA_EMAIL" --project="$P" \
    --member="user:$OWNER" --role=roles/iam.serviceAccountTokenCreator >&2
  GRANT_MADE=yes
}

# step_seed: four small objects of this check's own, inside the four real
# prefixes, written with YOUR identity. They are what makes the listings mean
# something — a listing of an empty prefix proves nothing — and each is named so
# that the Restore deletes exactly this run's slug.
step_seed() {
  printf '{"check": "340", "kind": "a seeded run record"}\n' >"$SCRATCH/runs.json"
  printf '# a seeded report\n' >"$SCRATCH/reports.md"
  printf 'chk340-token-marker\n' >"$SCRATCH/budget-token"
  printf 'chk340-cache-marker\n' >"$SCRATCH/cache-blob"
  # Recorded BEFORE the first copy: a copy that fails half way still leaves
  # objects the Restore has to remove, and it removes the four prefixes wholesale.
  SEEDED=yes
  say "seeding four objects of this check's own under runs/, reports/, tokens/ and cache/"
  gcloud storage cp --project="$P" "$SCRATCH/runs.json" "gs://$B/$RUN_OBJ" >&2
  gcloud storage cp --project="$P" "$SCRATCH/reports.md" "gs://$B/$REPORT_OBJ" >&2
  gcloud storage cp --project="$P" "$SCRATCH/budget-token" "gs://$B/$TOKEN_OBJ" >&2
  gcloud storage cp --project="$P" "$SCRATCH/cache-blob" "gs://$B/$CACHE_OBJ" >&2
  fact seed PASS "four objects of this check's own are in the bucket: gs://$B/$RUN_OBJ, gs://$B/$REPORT_OBJ, gs://$B/$TOKEN_OBJ and gs://$B/$CACHE_OBJ. The Restore deletes the four slug prefixes, so nothing else in the bucket is named"
}

# try_form HOW LIST_RUNS LIST_REPORTS — grant this form's two bindings, run the
# seven criteria as the account, and take the bindings away again. The result is
# left in FORM_OK, and NOT in the exit status: this is called from an `if`
# NOWHERE, because a function called as a condition has `set -e` and the ERR trap
# switched off for its whole dynamic extent — and the two bindings it GRANTS are
# commands that must not be swallowed. A refused
# `add-iam-policy-binding` swallowed here would leave every reading below refused
# for a reason that has nothing to do with the attribute, and the run would then
# answer "NEITHER form listed, R12's Terraform must NOT land" — the strongest
# claim this check can make — when it should say "I could not tell" (exit 2).
# The criteria calls below do handle their own status, so they are the only place
# where a failure is a reading rather than an error.
try_form() {
  local how=$1 lruns=$2 lreports=$3 ok=yes held=no key
  FORM_OK=no
  say "form '$how': the listing clauses name \"$lruns\" and \"$lreports\""
  grant_bindings "$lruns" "$lreports"
  # Order matters: the two listings and the read that MUST succeed are polled,
  # and the four refusals come after, so a "refused" reading cannot be a grant
  # that has not arrived yet.
  if ! must_list "$how-lists-runs" "gs://$B/runs/$SLUG/" "runs/$SLUG/"; then ok=no; else held=yes; fi
  if ! must_list "$how-lists-reports" "gs://$B/reports/" "reports/$SLUG/"; then ok=no; else held=yes; fi
  if ! must_read "$how-reads-runs" "gs://$B/$RUN_OBJ"; then ok=no; else held=yes; fi
  if [ "$held" = yes ]; then
    # The root listing is the one the two prefixes must NOT reach: a listing of
    # `gs://$B/` carries an empty prefix (or the bucket's own path), and neither
    # candidate form may match it.
    if ! must_refuse "$how-root-refused" "the root listing of gs://$B/" storage.objects.list \
      list_as_sa "gs://$B/"; then ok=no; fi
    if ! must_refuse "$how-tokens-read-refused" "the read of the seeded tokens/ object gs://$B/$TOKEN_OBJ" storage.objects.get \
      cat_as_sa "gs://$B/$TOKEN_OBJ"; then ok=no; fi
    if ! must_refuse "$how-tokens-list-refused" "the listing of gs://$B/tokens/" storage.objects.list \
      list_as_sa "gs://$B/tokens/"; then ok=no; fi
    if ! must_refuse "$how-cache-list-refused" "the listing of gs://$B/cache/" storage.objects.list \
      list_as_sa "gs://$B/cache/"; then ok=no; fi
  else
    # Nothing under this form held, so the grant is either not in effect or not
    # the grant this form describes — and a "refused" read now would be the
    # former read as the latter. The four rows are recorded as not read rather
    # than as passes, which is what keeps "the grant is narrow" an observation
    # and not an assumption.
    for key in root-refused tokens-read-refused tokens-list-refused cache-list-refused; do
      fact "$how-$key" INFO "not read: neither listing and not the read held under this form, so a refusal here could be the fresh grant not being in effect yet rather than the clause refusing it — the form's own failure is the reading, and this row is not one"
    done
  fi
  remove_bindings
  if [ "$ok" = yes ]; then FORM_OK=yes; fi
  return 0
}

# not_tried HOW: the form that was not reached, recorded rather than left reading
# "not reached". A run stops at the form that works, and the other form is what
# the difference between the two answers is — not a second reading to make.
not_tried() {
  local how=$1 key
  for key in lists-runs lists-reports reads-runs root-refused tokens-read-refused tokens-list-refused cache-list-refused; do
    fact "$how-$key" INFO "not tried: the form tried before this one passed every criterion, and this check stops at the form that works"
  done
}

step_forms() {
  local passed=""
  FORM_OK=no
  # Neither call sits under an `if`: see try_form. Both always succeed, and the
  # answer is the FORM_OK they leave behind.
  try_form relative "$(relative_prefix runs)" "$(relative_prefix reports)"
  if [ "$FORM_OK" = yes ]; then
    passed=relative
    not_tried full
  else
    # The relative form's bindings were removed at the end of try_form, and a
    # removal propagates like a grant: the second form is granted only once this
    # account is refused the listing the first form allowed. See
    # wait_for_removal.
    wait_for_removal
    try_form full "$(full_prefix runs)" "$(full_prefix reports)"
    if [ "$FORM_OK" = yes ]; then passed=full; fi
  fi
  PASSED_FORM=$passed
}

# step_verdict: the answer, in one row and one line. Everything R12's builder
# needs is which of the two forms the attribute carries; the exact string is
# printed above the block as well, so a paste-back carries it even to a reader
# who skims the facts.
step_verdict() {
  case "$PASSED_FORM" in
  relative)
    ANSWER="runs/ on the runs binding and reports/ on the reports binding (the request's own prefix, not the bucket's object path)"
    fact list-prefix PASS "the RELATIVE form passed all seven criteria, so the attribute carries the request's own prefix: R12's builder must pass \"runs/\" for the runs binding and \"reports/\" for the reports binding, each in api.getAttribute('storage.googleapis.com/objectListPrefix', '').startsWith(\"<prefix>/\")"
    ;;
  full)
    ANSWER="projects/_/buckets/$B/objects/runs/ on the runs binding and projects/_/buckets/$B/objects/reports/ on the reports binding (the bucket's object path, not the request's own prefix)"
    fact list-prefix PASS "the FULL PATH form passed all seven criteria, so the attribute carries the bucket's object path: R12's builder must pass \"projects/_/buckets/$B/objects/runs/\" for the runs binding and \"projects/_/buckets/$B/objects/reports/\" for the reports binding, each in api.getAttribute('storage.googleapis.com/objectListPrefix', '').startsWith(\"<prefix>\")"
    ;;
  *)
    fact list-prefix FAIL "NEITHER form listed: under both, the account could not list runs/$SLUG/ or reports/, with the root listing and the tokens/ and cache/ listings refused. R12's Terraform must NOT land on this reading — the plan stops and asks the owner (ruling E4) — and the bucket-wide objectViewer a listing would otherwise need is deliberately not planned here, because it would also read tokens/ and cache/. Read the two forms' rows above: a listing that failed only after ${PROPAGATION_SECONDS}s of retries says the attribute does not carry that form, and a refusal that was ALLOWED says the grant is wider than R12's two prefixes"
    ;;
  esac
  REACHED_END=yes
  if [ -n "$ANSWER" ]; then
    owner "the <list prefix> R12 must use: $ANSWER"
  else
    owner "no form listed: R12's Terraform must not land — the block says which of the seven criteria failed under each"
  fi
  case "$PASSED_FORM" in
  "") exit 1 ;;
  *) exit 0 ;;
  esac
}

# print_plan: every command a real run would make, in order, and its Restore's.
# Printed by --dry-run INSTEAD of running anything: this check's own test pins
# that a dry run makes no gcloud call at all, so the plan names the shape of each
# command and never evaluates one.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [network, reads] gcloud storage ls --project=$P gs://$B/ — the bucket, with YOUR identity (a bucket that cannot be reached is a stop)"
  say "  [network, writes] gcloud iam service-accounts create ${SA_ID%????}XXXX --project=$P --display-name='Check 340 throwaway (safe to delete)' — the suffix is drawn per run"
  say "  [network, writes] gcloud iam service-accounts add-iam-policy-binding $SA_EMAIL --project=$P --member=user:<you> --role=roles/iam.serviceAccountTokenCreator"
  say "  [local, writes] the four seeded files in \$SCRATCH"
  say "  [network, writes] gcloud storage cp --project=$P <file> gs://$B/$RUN_OBJ, and the same for gs://$B/$REPORT_OBJ, gs://$B/$TOKEN_OBJ and gs://$B/$CACHE_OBJ"
  say "  [network, writes] for the form \"relative\" first, and for \"full\" only if that one fails: the two JSON condition files, then"
  say "      gcloud storage buckets add-iam-policy-binding gs://$B --project=$P --member=serviceAccount:$SA_EMAIL --role=roles/storage.objectViewer --condition-from-file=<the runs/ condition>"
  say "      gcloud storage buckets add-iam-policy-binding gs://$B --project=$P --member=serviceAccount:$SA_EMAIL --role=roles/storage.objectUser --condition-from-file=<the reports/ condition>"
  say "    the first carrying $(clause_read runs) || $(clause_list "runs/") and the second $(clause_read reports) || $(clause_list "reports/") under the relative form, and the same two with projects/_/buckets/$B/objects/runs/ and .../objects/reports/ under the full one"
  say "  [network, reads, as the account] gcloud storage ls --recursive --impersonate-service-account=$SA_EMAIL --project=$P gs://$B/runs/$SLUG/ and gs://$B/reports/ (must list, and the listing must name the seeded object; retried for up to ${PROPAGATION_SECONDS}s for the grant to take effect); gcloud storage cat of the seeded runs/ object (must read, and retried the same way); then, only if one of those three held, the four refusals: ls gs://$B/, cat the seeded tokens/ object, ls gs://$B/tokens/ and ls gs://$B/cache/ (each has to be REFUSED, and refused by a denial naming storage.objects.list or storage.objects.get as the clause it is about would carry; a call that did not answer stops the run instead of counting as one of them)"
  say "  [network, writes] gcloud storage buckets remove-iam-policy-binding ... --all for both roles, between the two forms and again in the Restore"
  say "  [network, reads, as the account] between the two forms only: ls --impersonate-service-account=$SA_EMAIL --project=$P gs://$B/runs/$SLUG/, retried until the account is REFUSED it by a denial naming storage.objects.list, the permission the removed clause carried (the removal has taken effect, so the next form cannot read through the earlier clause), or for up to ${PROPAGATION_SECONDS}s — a failure that names no permission, or another one, is a call that did not answer and is retried like a listing that still succeeds, and a wait that never sees the denial is a stop rather than a verdict"
  say "and its Restore (also from the exit trap) would run:"
  say "  [network, deletes] gcloud storage rm -r --project=$P gs://$B/runs/$SLUG/ gs://$B/reports/$SLUG/ gs://$B/tokens/$SLUG/ gs://$B/cache/$SLUG/"
  say "  [network, deletes] gcloud storage buckets remove-iam-policy-binding gs://$B --member=serviceAccount:$SA_EMAIL --role=roles/storage.objectViewer --all, and the same for roles/storage.objectUser"
  say "  [network, deletes] gcloud iam service-accounts remove-iam-policy-binding $SA_EMAIL --project=$P --member=user:<you> --role=roles/iam.serviceAccountTokenCreator --all"
  say "  [network, deletes] gcloud iam service-accounts delete $SA_EMAIL --project=$P --quiet"
  say "  [network, reads] the four lists, and — whenever this run made an account — the bucket policy and the account describe, that read it all back as gone (only an empty listing and a NOT_FOUND count as gone; any other error is a FAILed Restore row, not a silent pass)"
  say "Nothing was created, granted, written or deleted: this dry run made no gcloud call at all."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project bucket belong bucket-reachable account seed
  relative-lists-runs relative-lists-reports relative-reads-runs
  relative-root-refused relative-tokens-read-refused
  relative-tokens-list-refused relative-cache-list-refused
  full-lists-runs full-lists-reports full-reads-runs
  full-root-refused full-tokens-read-refused
  full-tokens-list-refused full-cache-list-refused
  list-prefix restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 340 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 340 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and made nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable, a
  # bad substitution), and a block that then read as a finished run would offer a
  # FAILed list-prefix row as an answer about the attribute when the steps that
  # would have filled it never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the listing attribute"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cloud state of this check's to undo"
  elif ! printf '%s' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|FAIL|the Restore recorded nothing, so this run's account, its two conditioned bindings and its four seeded prefixes may still be there. Remove them by hand: the account is $SA_EMAIL, and the prefixes are runs/, reports/, tokens/ and cache/ under the slug $SLUG"
  fi
  # The verdict is taken from the facts, not from the exit path: an exit 0 beside
  # a FAILed list-prefix row, or beside a Restore that did not finish, would read
  # as a pass. The rows of the form that did NOT work are deliberately not part of
  # this — a form that lists nothing is what the other form's PASS is measured
  # against, and the block says which is which.
  if [ "$status" -eq 0 ] && printf '%s\n' "$facts" | grep -qE '^(list-prefix|restore)\|FAIL\|'; then
    status=1
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    if [ -n "$ANSWER" ]; then
      printf '\nTHE LIST PREFIX R12 MUST USE: %s\n' "$ANSWER" >&3
    fi
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check340.sh --project <project> --bucket <bucket> [--dry-run]"

parse_args() {
  P=""
  B=""
  DRY_RUN=${CHECK340_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a project ID. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --bucket)
      [ $# -ge 2 ] || die "--bucket needs a bucket name (with or without gs://). usage: $USAGE"
      B=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  case "$B" in gs://*) B=${B#gs://} ;; esac
  case "$B" in */) B=${B%/} ;; esac
  [ -n "$B" ] || die "--bucket is required, and it has to be a SANDBOX bucket: this check grants IAM on it, writes four objects into it and deletes them again. usage: $USAGE"
  [ -n "$P" ] || die "--project is required, and it has to be the project that bucket lives in: this check creates a service account there, and it never reads the project from a config that may name another. usage: $USAGE"
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  fact project PASS "$P (typed by the owner, and the project the throwaway account is made in)"
  fact bucket PASS "$B (typed by the owner, and the bucket the two bindings and the four seeded objects are on)"
  guard_belong
  if [ -n "$DRY_RUN" ]; then
    fact dry-run PASS "guards and the plan only: nothing was created, granted, written or deleted, and no gcloud call was made at all"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the two arguments, the belong guard, and the dry-run row itself. It reached
    # its end -- the plan IS the end of a dry run -- so the exit trap must not
    # read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(project bucket belong dry-run)
    return 0
  fi
  guard_bucket_reachable
  guard_account_absent
  step_owner
  step_account
  step_seed
  step_forms
  step_verdict
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
