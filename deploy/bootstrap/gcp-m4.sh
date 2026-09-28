#!/usr/bin/env bash
# gcp-m4.sh — THROWAWAY bootstrap for Fugaro M4 live testing. M5's Terraform
# module (design §8) replaces every resource it creates. Dry run by default:
# each command is printed with a leading "+" and nothing but the read-only
# `fugaro gcp job-spec` runs. --apply runs one step, after its ⚠ CONFIRM has
# been confirmed: typed at the terminal, or with --yes, which an agent
# driving the script passes only after the user confirms that step.
#
# usage: gcp-m4.sh [--apply [--yes]] STEP [--all] [--secrets]
#
# Run `config` first: it writes the local config that binds this machine to
# PROJECT, and every other step refuses unless that config exists and names
# the same PROJECT, REGION and BUCKET.
#
# Environment: PROJECT, REGION, BUCKET, REPO (owner/name), WORKFLOW,
# CHECKOUT (the target repository's checkout), FUGARO (default fugaro),
# HEAVY (optionally wraps the Docker commands, for example a lock script; by
# default they run directly), FUGARO_SRC (this checkout), and for the config
# step REPOS ("owner/name:branch:workflow[:provider] ...", provider bitbucket
# or github, default bitbucket), BASE_IMAGE and FORCE.
set -euo pipefail

# gcloud must never ask anything (such as "enable this API?"); a prompt's
# default answer is taken instead, which for API enablement is no.
export CLOUDSDK_CORE_DISABLE_PROMPTS=1

STEPS="config apis bucket registry build-sa job-sa secrets secrets-access base image job teardown teardown-all"

usage() {
  echo "usage: gcp-m4.sh [--apply [--yes]] config|apis|bucket|registry|build-sa|job-sa|secrets|secrets-access|base|image|job|teardown [--secrets]|teardown-all --all" >&2
  exit 2
}

APPLY=0
YES=0
ALL=0
SECRETS=0
STEP=
for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    --yes) YES=1 ;;
    --all) ALL=1 ;;
    --secrets) SECRETS=1 ;;
    -*) usage ;;
    *) [ -z "$STEP" ] || usage; STEP=$arg ;;
  esac
done
case "$STEP" in ""|*[!a-z-]*) usage ;; esac
case " $STEPS " in *" $STEP "*) ;; *) usage ;; esac
FUGARO=${FUGARO:-fugaro}
# HEAVY optionally wraps Docker-heavy commands (a local lock script); by default they run directly.
HEAVY=${HEAVY:-env}

die() { echo "gcp-m4.sh: $*" >&2; exit 1; }

# need_project dies unless every gcloud argv names the project explicitly.
need_project() {
  local a
  for a in "$@"; do [ "$a" != --project ] || return 0; done
  die "internal error: gcloud call without --project: $*"
}

# run prints a command and, with --apply, runs it. Every gcloud call must
# name the project explicitly, never fall back to gcloud's active one.
run() {
  if [ "$1" = gcloud ]; then need_project "$@"; fi
  printf '+ %s\n' "$*"
  if [ "$APPLY" = 1 ]; then "$@"; fi
}

is_not_found() {
  case "$1" in *NOT_FOUND*|*"not found"*|*"Cannot find"*) return 0 ;; esac
  return 1
}

# run_gone_ok is run for a removal: with --apply, "not found" means it is
# already gone, which is fine. Any other failure stops the step.
run_gone_ok() {
  need_project "$@"
  printf '+ %s\n' "$*"
  [ "$APPLY" = 1 ] || return 0
  local err
  if err=$("$@" 2>&1 >/dev/null); then return 0; fi
  if is_not_found "$err"; then echo "= already gone"; return 0; fi
  printf '%s\n' "$err" >&2
  die "$2 $3 $4 failed"
}

# exists DESCRIBE-ARGV: with --apply, runs the read-only describe and
# succeeds if the resource exists, fails if it is NOT_FOUND, and dies on
# anything else (such as PERMISSION_DENIED). In a dry run it fails, so a
# create is printed; deletes are guarded with `gone` instead.
exists() {
  [ "$APPLY" = 1 ] || return 1
  need_project "$@"
  local err
  if err=$("$@" 2>&1 >/dev/null); then return 0; fi
  is_not_found "$err" && return 1
  printf '%s\n' "$err" >&2
  die "$* failed"
}

# gone DESCRIBE-ARGV: with --apply, true when the resource no longer exists
# (and says so); in a dry run always false, so the delete is printed.
gone() {
  [ "$APPLY" = 1 ] || return 1
  if exists "$@"; then return 1; fi
  echo "= already gone, skipped: $*"
  return 0
}

# confirm prints the step's ⚠ banner and, with --apply, requires --yes or
# the project ID typed at the terminal.
confirm() {
  printf '⚠ CONFIRM (project %s): %s\n' "$PROJECT" "$*"
  [ "$APPLY" = 1 ] || return 0
  [ "$YES" = 1 ] && return 0
  [ -t 0 ] || die "--apply needs --yes when stdin is not a terminal; nothing done"
  local answer
  printf 'Type the project ID (%s) to go ahead: ' "$PROJECT" >&2
  read -r answer
  [ "$answer" = "$PROJECT" ] || die "not confirmed; nothing done"
}

# The display name a job's service account gets. Service accounts carry no
# labels, so this is how a reused or deleted account is recognized as this
# repository's and workflow's. It comes from `spec sa-display-name` (set by
# each step that calls this), job-spec being its one definition; it names
# the repository by its slug, which, like the fugaro_repo label, includes
# the provider kind.
job_sa_display() {
  [ -n "${sa_display:-}" ] || die "internal error: job_sa_display before sa_display is set"
  echo "$sa_display"
}

# check_sa_owner SA: with --apply, dies unless the existing account SA has
# this repository's and workflow's display name.
check_sa_owner() {
  [ "$APPLY" = 1 ] || return 0
  local dn
  dn=$(gcloud iam service-accounts describe "$1" --project "$PROJECT" --format 'value(displayName)')
  [ "$dn" = "$(job_sa_display)" ] || die "service account $1 is \"$dn\", not \"$(job_sa_display)\"; refusing"
}

# check_job_owner JOB: with --apply, dies unless the existing job JOB is
# labelled fugaro=managed and with this repository and workflow.
check_job_owner() {
  [ "$APPLY" = 1 ] || return 0
  local mgd got gotwf
  mgd=$(gcloud run jobs describe "$1" --project "$PROJECT" --region "$REGION" --format 'value(metadata.labels.fugaro)')
  got=$(gcloud run jobs describe "$1" --project "$PROJECT" --region "$REGION" --format 'value(metadata.labels.fugaro_repo)')
  gotwf=$(gcloud run jobs describe "$1" --project "$PROJECT" --region "$REGION" --format 'value(metadata.labels.fugaro_workflow)')
  [ "$mgd" = managed ] && [ "$got" = "$repo_label" ] && [ "$gotwf" = "$WORKFLOW" ] ||
    die "job $1 is labelled fugaro=${mgd:-none} fugaro_repo=${got:-none} fugaro_workflow=${gotwf:-none}, not fugaro=managed $repo_label $WORKFLOW; refusing"
}

# check_secret_labels ID: dies unless the existing secret ID carries the
# labels fugaro secrets set gives it: fugaro=managed, fugaro_repo=$repo_label
# and fugaro_secret=<its logical name>, which job-spec's secret-names maps
# the ID to. A secret of another repository, or of another logical name,
# under a colliding ID must never be granted to this repository's service
# accounts or deleted.
check_secret_labels() {
  local mgd got name want
  mgd=$(gcloud secrets describe "$1" --project "$PROJECT" --format 'value(labels.fugaro)')
  got=$(gcloud secrets describe "$1" --project "$PROJECT" --format 'value(labels.fugaro_repo)')
  name=$(gcloud secrets describe "$1" --project "$PROJECT" --format 'value(labels.fugaro_secret)')
  want=$(spec secret-names | sed -n "s/^\(.*\)=$1\$/\1/p" | head -n 1)
  [ -n "$want" ] || die "secret $1 is not one of $REPO's secrets; refusing"
  [ "$mgd" = managed ] && [ "$got" = "$repo_label" ] && [ "$name" = "$want" ] ||
    die "secret $1 is labelled fugaro=${mgd:-none} fugaro_repo=${got:-none} fugaro_secret=${name:-none}, not fugaro=managed $repo_label $want; refusing"
}

# check_secret_owner ID: with --apply, dies unless secret ID exists and
# passes check_secret_labels.
check_secret_owner() {
  [ "$APPLY" = 1 ] || return 0
  exists gcloud secrets describe "$1" --project "$PROJECT" ||
    die "secret $1 does not exist; store it first (gcp-m4.sh secrets prints the commands)"
  check_secret_labels "$1"
}

# bucket_name_ok: dies unless BUCKET starts with fugaro-runs-. The live
# tests refuse any other runs bucket, and a bucket can't be renamed, so the
# name is checked before one is written into the config or created.
bucket_name_ok() {
  case "$BUCKET" in
    fugaro-runs-?*) ;;
    *) die "BUCKET=$BUCKET must start with fugaro-runs- (the live tests refuse any other runs bucket, and a bucket can't be renamed); refusing" ;;
  esac
}

need() {
  local v
  for v in "$@"; do [ -n "${!v:-}" ] || die "set $v"; done
}

# spec FIELD: one value of `fugaro gcp job-spec` for REPO/WORKFLOW (read-only).
# Callers assign it to a variable first, so a failure stops the step under
# set -e instead of leaving an empty name in a gcloud argument.
spec() { (cd "$CHECKOUT" && "$FUGARO" gcp job-spec --repo "$REPO" --workflow "$WORKFLOW" --field "$1"); }

# wait_sa waits, with --apply, for a new service account to become visible
# to IAM, which is eventually consistent.
wait_sa() {
  [ "$APPLY" = 1 ] || return 0
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if gcloud iam service-accounts describe "$1" --project "$PROJECT" >/dev/null 2>&1; then return 0; fi
    echo "waiting for $1 ($i)" >&2
    sleep 3
  done
  die "service account $1 did not appear"
}

# pin_bucket dies unless gs://$BUCKET belongs to $PROJECT. Bucket names are
# global, so --project does not scope a gs:// operation; this read-only
# check does, before every bucket write, IAM change or deletion.
pin_bucket() {
  if [ "$APPLY" != 1 ]; then
    echo "= with --apply: check that gs://$BUCKET belongs to $PROJECT"
    return 0
  fi
  local pn bn
  pn=$(gcloud projects describe "$PROJECT" --project "$PROJECT" --format 'value(projectNumber)')
  bn=$(gcloud storage buckets describe "gs://$BUCKET" --project "$PROJECT" --raw --format 'value(projectNumber)')
  [ -n "$pn" ] && [ "$pn" = "$bn" ] || die "gs://$BUCKET belongs to project number ${bn:-unknown}, not $PROJECT ($pn); refusing"
}

# The shared resources' ownership marks. The bucket and the registry get the
# label fugaro=managed when this script creates them, and fugaro-build this
# display name (service accounts carry no labels). A resource under one of
# these names without its mark is someone else's: it is never adopted,
# rewritten (the bucket's lifecycle), granted on or deleted.
BUILD_SA_DISPLAY="Fugaro image builds (M4 bootstrap)"

# own_bucket: pin_bucket, and with --apply die unless gs://$BUCKET carries
# the label fugaro=managed.
own_bucket() {
  pin_bucket
  if [ "$APPLY" != 1 ]; then
    echo "= with --apply: check that gs://$BUCKET is labelled fugaro=managed"
    return 0
  fi
  local got
  got=$(gcloud storage buckets describe "gs://$BUCKET" --project "$PROJECT" --raw --format 'value(labels.fugaro)')
  [ "$got" = managed ] || die "gs://$BUCKET is labelled fugaro=${got:-none}, not fugaro=managed, so this script did not create it; refusing. \
If it is this script's (its create succeeded but the label update did not), label it: gcloud storage buckets update gs://$BUCKET --update-labels fugaro=managed --project $PROJECT"
}

# own_registry: with --apply, dies unless repository fugaro in $REGION
# carries the label fugaro=managed.
own_registry() {
  if [ "$APPLY" != 1 ]; then
    echo "= with --apply: check that repository fugaro is labelled fugaro=managed"
    return 0
  fi
  local got
  got=$(gcloud artifacts repositories describe fugaro --location "$REGION" --project "$PROJECT" --format 'value(labels.fugaro)')
  [ "$got" = managed ] || die "Artifact Registry repository fugaro is labelled fugaro=${got:-none}, not fugaro=managed, so this script did not create it; refusing. \
If it is this script's (an earlier bootstrap created it without the label), label it: gcloud artifacts repositories update fugaro --location $REGION --update-labels fugaro=managed --project $PROJECT"
}

# own_build_sa: with --apply, dies unless $BUILD_SA has BUILD_SA_DISPLAY.
own_build_sa() {
  if [ "$APPLY" != 1 ]; then
    echo "= with --apply: check that $BUILD_SA is \"$BUILD_SA_DISPLAY\""
    return 0
  fi
  local dn
  dn=$(gcloud iam service-accounts describe "$BUILD_SA" --project "$PROJECT" --format 'value(displayName)')
  [ "$dn" = "$BUILD_SA_DISPLAY" ] || die "service account $BUILD_SA is \"$dn\", not \"$BUILD_SA_DISPLAY\", so this script did not create it; refusing"
}

# The local config, read without a YAML parser: top-level scalars and
# build.service_account, in the forms the config step writes. Anything it
# cannot read comes back empty, and the guard then refuses.
cfgfile=${FUGARO_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/config.yaml}
cfg_get() {
  awk -v k="$1" 'index($0, k ":") == 1 { v = substr($0, length(k) + 2); sub(/[[:space:]]+#.*$/, "", v); gsub(/^[[:space:]]+|[[:space:]]+$/, "", v); print v; exit }' "$cfgfile" | tr -d "\"'"
}
cfg_build_sa() {
  awk '
    /^build:/ { inb = 1; if (match($0, /service_account:[[:space:]]*[^,} ]+/)) { v = substr($0, RSTART, RLENGTH); sub(/service_account:[[:space:]]*/, "", v); print v; exit } next }
    inb && /^[[:space:]]+service_account:/ { v = $0; sub(/^[[:space:]]+service_account:[[:space:]]*/, "", v); sub(/[[:space:]]+#.*$/, "", v); print v; exit }
    inb && /^[^[:space:]]/ { inb = 0 }' "$cfgfile" | tr -d "\"'"
}

need PROJECT
[[ "$PROJECT" =~ ^[a-z][a-z0-9-]{4,28}[a-z0-9]$ ]] || die "PROJECT=$PROJECT is not a GCP project ID; refusing"
[ -z "${REGION:-}" ] || [[ "$REGION" =~ ^[a-z]+-[a-z]+[0-9]+$ ]] || die "REGION=$REGION is not a region; refusing"

# The project guard: every step but config acts only on the project, region
# and bucket the local config names.
BUILD_SA=
if [ "$STEP" != config ]; then
  [ -f "$cfgfile" ] || die "no local config at $cfgfile; run the config step first (refusing)"
  cfgproject=$(cfg_get project)
  [ -n "$cfgproject" ] || die "$cfgfile has no project:; refusing"
  [ "$cfgproject" = "$PROJECT" ] || die "PROJECT=$PROJECT but $cfgfile says $cfgproject; refusing"
  if [ -n "${REGION:-}" ]; then
    cfgregion=$(cfg_get region)
    [ "$cfgregion" = "$REGION" ] || die "REGION=$REGION but $cfgfile says ${cfgregion:-nothing}; refusing"
  fi
  if [ -n "${BUCKET:-}" ]; then
    cfgbucket=$(cfg_get runs_bucket)
    if [ -z "$cfgbucket" ]; then cfgbucket=$(cfg_get bucket_url | sed -n 's#^gs://\([^/?]*\).*#\1#p'); fi
    [ "$cfgbucket" = "$BUCKET" ] || die "BUCKET=$BUCKET but $cfgfile says ${cfgbucket:-nothing}; refusing"
  fi
  BUILD_SA=$(cfg_build_sa)
  # The script only ever creates fugaro-build; any other account is not its to grant or delete.
  [ -z "$BUILD_SA" ] || [ "$BUILD_SA" = "fugaro-build@$PROJECT.iam.gserviceaccount.com" ] ||
    die "$cfgfile names build service account $BUILD_SA, not fugaro-build@$PROJECT.iam.gserviceaccount.com; refusing"
fi
need_build_sa() { [ -n "$BUILD_SA" ] || die "$cfgfile has no build.service_account; rerun the config step"; }

case "$STEP" in
  config)
    need REGION BUCKET REPOS
    bucket_name_ok
    path=$cfgfile
    user=$(git config user.email || true)
    [ -n "$user" ] || die "git config user.email is not set"
    body=$(mktemp)
    trap 'rm -f "$body"' EXIT
    {
      echo "version: 1"
      echo "project: $PROJECT"
      echo "region: $REGION"
      echo "runs_bucket: $BUCKET"
      echo "registry: $REGION-docker.pkg.dev/$PROJECT/fugaro"
      if [ -n "${BASE_IMAGE:-}" ]; then echo "base_image: $BASE_IMAGE"; fi
      echo "build: { service_account: fugaro-build@$PROJECT.iam.gserviceaccount.com }"
      echo "user: $user"
      echo "repos:"
      for r in $REPOS; do
        IFS=: read -r name branch wf provider extra <<<"$r"
        [ -n "$name" ] && [ -n "$branch" ] && [ -n "$wf" ] && [ -z "${extra:-}" ] ||
          die "REPOS entry $r is not owner/name:branch:workflow[:provider]"
        # The provider kind is part of the repository's slug; `fugaro run`
        # reads it from here (it must agree with the checkout's git.provider).
        provider=${provider:-bitbucket}
        case "$provider" in bitbucket|github) ;; *) die "REPOS entry $r: provider $provider is not bitbucket or github" ;; esac
        echo "  $name: { provider: $provider, base_branch: $branch, workflows: [$wf] }"
      done
    } > "$body"
    cat "$body"
    if [ -e "$path" ]; then
      diff -u "$path" "$body" || true
      [ "${FORCE:-}" = 1 ] || die "$path exists; rerun with FORCE=1 to replace it"
    fi
    confirm "writes $path, which binds every later step to project $PROJECT, region $REGION and bucket gs://$BUCKET (local only, free)"
    printf '+ write %s\n' "$path"
    if [ "$APPLY" = 1 ]; then mkdir -p "$(dirname "$path")"; install -m 0600 "$body" "$path"; fi
    ;;
  apis)
    confirm "enables the Run, Storage, Secret Manager, Artifact Registry, Cloud Build, Logging and IAM APIs in $PROJECT (free to enable, billable once used)"
    run gcloud services enable run.googleapis.com storage.googleapis.com secretmanager.googleapis.com \
      artifactregistry.googleapis.com cloudbuild.googleapis.com logging.googleapis.com iam.googleapis.com --project "$PROJECT"
    ;;
  bucket)
    need REGION BUCKET
    bucket_name_ok
    confirm "creates bucket gs://$BUCKET in $REGION with lifecycle rules: runs/ deleted after 90 days, cache/ 30 days after its custom time or 180 days after creation (storage billed per GB-month)"
    if exists gcloud storage buckets describe "gs://$BUCKET" --project "$PROJECT" --format 'value(name)'; then
      own_bucket
      loc=$(gcloud storage buckets describe "gs://$BUCKET" --project "$PROJECT" --raw --format 'value(location)')
      [ "$(echo "$loc" | tr '[:upper:]' '[:lower:]')" = "$REGION" ] || die "gs://$BUCKET is in $loc, not $REGION; refusing"
      echo "= gs://$BUCKET exists in $PROJECT, $REGION and is labelled fugaro=managed; create skipped"
    else
      # buckets create takes no labels; the update below sets it, with the lifecycle.
      run gcloud storage buckets create "gs://$BUCKET" --project "$PROJECT" --location "$REGION" \
        --uniform-bucket-level-access --public-access-prevention
      pin_bucket
    fi
    lifecycle=$(mktemp)
    trap 'rm -f "$lifecycle"' EXIT
    cat > "$lifecycle" <<'JSON'
{"rule": [
  {"action": {"type": "Delete"}, "condition": {"age": 90, "matchesPrefix": ["runs/"]}},
  {"action": {"type": "Delete"}, "condition": {"daysSinceCustomTime": 30, "matchesPrefix": ["cache/"]}},
  {"action": {"type": "Delete"}, "condition": {"age": 180, "matchesPrefix": ["cache/"]}}
]}
JSON
    run gcloud storage buckets update "gs://$BUCKET" --project "$PROJECT" --update-labels fugaro=managed --lifecycle-file="$lifecycle"
    ;;
  registry)
    need REGION
    confirm "creates Artifact Registry repository fugaro (docker) in $REGION (storage billed per GB-month)"
    if exists gcloud artifacts repositories describe fugaro --location "$REGION" --project "$PROJECT"; then
      own_registry
      echo "= repository fugaro exists and is labelled fugaro=managed; create skipped"
    else
      run gcloud artifacts repositories create fugaro --repository-format docker --location "$REGION" --labels fugaro=managed --project "$PROJECT"
    fi
    ;;
  build-sa)
    need REGION
    need_build_sa
    confirm "creates service account $BUILD_SA and grants it artifactregistry.writer on repository fugaro and logging.logWriter on $PROJECT"
    if [ "$APPLY" = 1 ] && ! exists gcloud artifacts repositories describe fugaro --location "$REGION" --project "$PROJECT"; then
      die "Artifact Registry repository fugaro does not exist in $REGION; run the registry step first"
    fi
    own_registry
    if exists gcloud iam service-accounts describe "$BUILD_SA" --project "$PROJECT"; then
      own_build_sa
      echo "= $BUILD_SA exists and is \"$BUILD_SA_DISPLAY\"; create skipped"
    else
      run gcloud iam service-accounts create fugaro-build --project "$PROJECT" --display-name "$BUILD_SA_DISPLAY"
      wait_sa "$BUILD_SA"
    fi
    run gcloud artifacts repositories add-iam-policy-binding fugaro --location "$REGION" --project "$PROJECT" \
      --member "serviceAccount:$BUILD_SA" --role roles/artifactregistry.writer
    run gcloud projects add-iam-policy-binding "$PROJECT" --project "$PROJECT" \
      --member "serviceAccount:$BUILD_SA" --role roles/logging.logWriter --condition None
    ;;
  job-sa)
    need BUCKET REPO WORKFLOW CHECKOUT
    sa_id=$(spec sa-id)
    sa=$(spec sa)
    cond=$(spec bucket-condition)
    sa_display=$(spec sa-display-name)
    confirm "creates service account $sa and grants it storage.objectUser on gs://$BUCKET, limited to its runs/, cache/ and locks/ prefixes"
    own_bucket
    if exists gcloud iam service-accounts describe "$sa" --project "$PROJECT"; then
      check_sa_owner "$sa"
      echo "= $sa exists and is $(job_sa_display); create skipped"
    else
      run gcloud iam service-accounts create "$sa_id" --project "$PROJECT" --display-name "$(job_sa_display)"
      wait_sa "$sa"
    fi
    run gcloud storage buckets add-iam-policy-binding "gs://$BUCKET" --member "serviceAccount:$sa" \
      --role roles/storage.objectUser --condition "expression=$cond,title=fugaro-$sa_id" --project "$PROJECT"
    ;;
  secrets)
    need REPO WORKFLOW CHECKOUT
    names=$(spec secret-names)
    echo "Create each secret with fugaro secrets set; the value comes from stdin or a hidden prompt, never argv:"
    while IFS='=' read -r name id; do
      if [ "$name" = claude-oauth-token ]; then
        echo "  (you, in your own terminal) claude setup-token, then: $FUGARO secrets set $name --repo $REPO   # $id"
      else
        echo "  $FUGARO secrets set $name --repo $REPO < <file holding the value>   # $id"
      fi
    done <<<"$names"
    ;;
  secrets-access)
    need REPO WORKFLOW CHECKOUT
    need_build_sa
    sa=$(spec sa)
    ids=$(spec secret-ids)
    build_ids=$(spec build-secret-ids)
    repo_label=$(spec repo-label)
    confirm "grants secretmanager.secretAccessor to $sa on $(echo "$ids" | tr '\n' ' ')and to $BUILD_SA on $(echo "$build_ids" | tr '\n' ' ')"
    # Every secret, and fugaro-build, is checked before anything is granted.
    own_build_sa
    if [ "$APPLY" = 1 ]; then
      for id in $ids $build_ids; do check_secret_owner "$id"; done
    else
      echo "= with --apply: check that each secret exists and is labelled fugaro_repo=$repo_label first"
    fi
    for id in $ids; do
      run gcloud secrets add-iam-policy-binding "$id" --project "$PROJECT" --member "serviceAccount:$sa" --role roles/secretmanager.secretAccessor
    done
    for id in $build_ids; do
      run gcloud secrets add-iam-policy-binding "$id" --project "$PROJECT" --member "serviceAccount:$BUILD_SA" --role roles/secretmanager.secretAccessor
    done
    ;;
  base)
    need REGION FUGARO_SRC
    rev=$(git -C "$FUGARO_SRC" rev-parse --short HEAD)
    tag="$REGION-docker.pkg.dev/$PROJECT/fugaro/fugaro-web-node:dev-$rev"
    confirm "builds the web-node base from $FUGARO_SRC with local Docker and pushes $tag (about 1.5 GB of registry storage); gcloud auth configure-docker adds $REGION-docker.pkg.dev to your ~/.docker/config.json"
    run "$HEAVY" sh "$FUGARO_SRC/images/build-base.sh" web-node "$tag"
    run gcloud auth configure-docker "$REGION-docker.pkg.dev" --quiet --project "$PROJECT"
    run "$HEAVY" docker push "$tag"
    # config rewrites the whole file, so the command carries everything it
    # needs; REPOS must be the same list as before.
    echo "set base_image: $tag in the local config, with the same REPOS as before:"
    echo "  PROJECT=$PROJECT REGION=$REGION BUCKET=${BUCKET:-<bucket>} REPOS='${REPOS:-<as before>}' BASE_IMAGE=$tag FORCE=1 gcp-m4.sh --apply config"
    ;;
  image)
    need REPO WORKFLOW CHECKOUT
    confirm "submits a Cloud Build in $PROJECT for $REPO/$WORKFLOW (billed per build-minute, E2_HIGHCPU_8)"
    ( cd "$CHECKOUT" && run "$FUGARO" image build --repo "$REPO" --workflow "$WORKFLOW" --json )
    ;;
  job)
    need REGION REPO WORKFLOW CHECKOUT
    job=$(spec job)
    img=$(spec image)
    sa=$(spec sa)
    cpu=$(spec cpu)
    mem=$(spec memory)
    timeout=$(spec task-timeout)
    envs=$(spec env)
    secrets=$(spec secrets)
    labels=$(spec labels)
    repo_label=$(spec repo-label)
    confirm "creates or updates Cloud Run job $job in $REGION (billed per execution second)"
    if exists gcloud run jobs describe "$job" --project "$PROJECT" --region "$REGION"; then
      check_job_owner "$job"
    elif [ "$APPLY" != 1 ]; then
      echo "= with --apply: if $job exists, check its fugaro_repo and fugaro_workflow labels first"
    fi
    run gcloud run jobs deploy "$job" --project "$PROJECT" --region "$REGION" \
      --image "$img:latest" --service-account "$sa" \
      --cpu "$cpu" --memory "$mem" --task-timeout "${timeout}s" \
      --max-retries 0 --tasks 1 --labels "$labels" --set-env-vars "$envs" --set-secrets "$secrets"
    ;;
  teardown)
    need REGION BUCKET REPO WORKFLOW CHECKOUT
    job=$(spec job)
    sa=$(spec sa)
    sa_id=$(spec sa-id)
    cond=$(spec bucket-condition)
    ids=$(spec secret-ids)
    repo_label=$(spec repo-label)
    sa_display=$(spec sa-display-name)
    job_checked=0
    sa_checked=0
    idlist=$(echo "$ids" | tr '\n' ' ')
    if [ "$SECRETS" = 1 ]; then
      what="DELETES secrets ${idlist}(with every binding on them; refused while another job of $REPO exists)"
    else
      what="removes $sa's accessor binding on secrets ${idlist}(the secrets and $BUILD_SA's bindings are kept; --secrets deletes them)"
    fi
    confirm "in $PROJECT, DELETES Cloud Run job $job and service account $sa with its conditional binding on gs://$BUCKET, and $what. The bucket, the registry and fugaro-build are kept."

    # Ownership: names can collide across repositories, so the labels decide.
    if [ "$APPLY" = 1 ]; then
      if exists gcloud storage buckets describe "gs://$BUCKET" --project "$PROJECT" --format 'value(name)'; then own_bucket; fi
      if exists gcloud run jobs describe "$job" --project "$PROJECT" --region "$REGION"; then
        check_job_owner "$job"
        job_checked=1
      fi
      if exists gcloud iam service-accounts describe "$sa" --project "$PROJECT"; then
        check_sa_owner "$sa"
        sa_checked=1
      fi
      for id in $ids; do
        if exists gcloud secrets describe "$id" --project "$PROJECT"; then
          check_secret_labels "$id"
        fi
      done
      if [ "$SECRETS" = 1 ]; then
        # On its own line, so a failing list stops the step (set -e) rather
        # than reading as "no other jobs".
        jobs=$(gcloud run jobs list --project "$PROJECT" --region "$REGION" --filter "metadata.labels.fugaro_repo=$repo_label" --format 'value(metadata.name)')
        others=$(printf '%s\n' "$jobs" | grep -vxF -e "$job" -e '' || true)
        [ -z "$others" ] || die "other jobs of $REPO still use its secrets ($(echo "$others" | tr '\n' ' ')); refusing --secrets"
      fi
    else
      echo "= with --apply: check the labels of $job and each secret, and the display name of $sa, first"
    fi

    # Only what the checks above recognized as this repository's is deleted.
    if ! gone gcloud run jobs describe "$job" --project "$PROJECT" --region "$REGION"; then
      [ "$APPLY" != 1 ] || [ "$job_checked" = 1 ] || die "job $job appeared after its label check; refusing"
      run gcloud run jobs delete "$job" --project "$PROJECT" --region "$REGION" --quiet
    fi
    if ! gone gcloud storage buckets describe "gs://$BUCKET" --project "$PROJECT" --format 'value(name)'; then
      pin_bucket
      run_gone_ok gcloud storage buckets remove-iam-policy-binding "gs://$BUCKET" --member "serviceAccount:$sa" \
        --role roles/storage.objectUser --condition "expression=$cond,title=fugaro-$sa_id" --project "$PROJECT"
    fi
    for id in $ids; do
      gone gcloud secrets describe "$id" --project "$PROJECT" && continue
      if [ "$SECRETS" = 1 ]; then
        run gcloud secrets delete "$id" --project "$PROJECT" --quiet
      else
        run_gone_ok gcloud secrets remove-iam-policy-binding "$id" --project "$PROJECT" \
          --member "serviceAccount:$sa" --role roles/secretmanager.secretAccessor
      fi
    done
    if ! gone gcloud iam service-accounts describe "$sa" --project "$PROJECT"; then
      [ "$APPLY" != 1 ] || [ "$sa_checked" = 1 ] || die "service account $sa appeared after its owner check; refusing"
      run gcloud iam service-accounts delete "$sa" --project "$PROJECT" --quiet
    fi
    ;;
  teardown-all)
    need REGION BUCKET
    [ "$ALL" = 1 ] || die "teardown-all deletes the shared resources; pass --all"
    need_build_sa
    confirm "in $PROJECT, DELETES bucket gs://$BUCKET with every run and cache, Artifact Registry repository fugaro in $REGION with every image, $BUILD_SA's logging.logWriter binding on $PROJECT, and service account $BUILD_SA. APIs stay enabled. It refuses while a fugaro job exists in $REGION, the only region the local config runs jobs in."
    if [ "$APPLY" = 1 ]; then
      left=$(gcloud run jobs list --project "$PROJECT" --region "$REGION" --filter 'metadata.name~^fugaro- OR metadata.labels.fugaro=managed' --format 'value(metadata.name)')
      [ -z "$left" ] || die "fugaro jobs still exist ($(echo "$left" | tr '\n' ' ')); run teardown for each repository first"
    fi
    # Every shared resource that exists is checked before anything is deleted.
    if exists gcloud storage buckets describe "gs://$BUCKET" --project "$PROJECT" --format 'value(name)'; then own_bucket; fi
    if exists gcloud artifacts repositories describe fugaro --location "$REGION" --project "$PROJECT"; then own_registry; fi
    if exists gcloud iam service-accounts describe "$BUILD_SA" --project "$PROJECT"; then own_build_sa; fi
    [ "$APPLY" = 1 ] || echo "= with --apply: check the bucket's and the registry's fugaro=managed label, and $BUILD_SA's display name, first"
    if ! gone gcloud storage buckets describe "gs://$BUCKET" --project "$PROJECT" --format 'value(name)'; then
      pin_bucket
      run gcloud storage rm --recursive "gs://$BUCKET" --project "$PROJECT"
    fi
    gone gcloud artifacts repositories describe fugaro --location "$REGION" --project "$PROJECT" ||
      run gcloud artifacts repositories delete fugaro --location "$REGION" --project "$PROJECT" --quiet
    run_gone_ok gcloud projects remove-iam-policy-binding "$PROJECT" --project "$PROJECT" \
      --member "serviceAccount:$BUILD_SA" --role roles/logging.logWriter --condition None
    gone gcloud iam service-accounts describe "$BUILD_SA" --project "$PROJECT" ||
      run gcloud iam service-accounts delete "$BUILD_SA" --project "$PROJECT" --quiet
    ;;
  *)
    usage
    ;;
esac
