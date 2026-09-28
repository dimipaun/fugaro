#!/usr/bin/env bash
# gcp-m4.sh — THROWAWAY bootstrap for Fugaro M4 live testing. M5's Terraform
# module (design §8) replaces every resource it creates. Dry run by default:
# each command is printed with a leading "+" and nothing but the read-only
# `fugaro gcp job-spec` runs. --apply runs one step, after its ⚠ CONFIRM has
# been confirmed: typed at the terminal, or with --yes, which the controller
# passes only after the user confirms that step.
#
# usage: gcp-m4.sh [--apply [--yes]] STEP [--all]
#
# Environment: PROJECT, REGION, BUCKET, REPO (owner/name), WORKFLOW,
# CHECKOUT (the target repository's checkout), FUGARO (default fugaro),
# HEAVY (the Docker lock), FUGARO_SRC (this checkout), and for the config
# step REPOS ("owner/name:branch:workflow ..."), BASE_IMAGE and FORCE.
set -euo pipefail

usage() {
  echo "usage: gcp-m4.sh [--apply [--yes]] apis|bucket|registry|build-sa|config|job-sa|secrets|secrets-access|base|image|job|teardown|teardown-all --all" >&2
  exit 2
}

APPLY=0
YES=0
ALL=0
STEP=
for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    --yes) YES=1 ;;
    --all) ALL=1 ;;
    -*) usage ;;
    *) [ -z "$STEP" ] || usage; STEP=$arg ;;
  esac
done
FUGARO=${FUGARO:-fugaro}
HEAVY=${HEAVY:-/Users/dimi/git.lattica/Fugaro/.superpowers/heavy.sh}

die() { echo "gcp-m4.sh: $*" >&2; exit 1; }

# run prints a command and, with --apply, runs it. Every gcloud call must
# name the project explicitly, never fall back to gcloud's active one.
run() {
  if [ "$1" = gcloud ]; then
    local a ok=0
    for a in "$@"; do [ "$a" != --project ] || ok=1; done
    [ "$ok" = 1 ] || die "internal error: gcloud call without --project: $*"
  fi
  printf '+ %s\n' "$*"
  if [ "$APPLY" = 1 ]; then "$@"; fi
}

# confirm prints the step's ⚠ banner and, with --apply, requires --yes or
# the project ID typed at the terminal.
confirm() {
  printf '⚠ CONFIRM (project %s): %s\n' "${PROJECT:-unset}" "$*"
  [ "$APPLY" = 1 ] || return 0
  [ "$YES" = 1 ] && return 0
  [ -t 0 ] || die "--apply needs --yes when stdin is not a terminal; nothing done"
  local answer
  printf 'Type the project ID (%s) to go ahead: ' "$PROJECT" >&2
  read -r answer
  [ "$answer" = "$PROJECT" ] || die "not confirmed; nothing done"
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

BUILD_SA=
if [ -n "${PROJECT:-}" ]; then BUILD_SA="fugaro-build@$PROJECT.iam.gserviceaccount.com"; fi

# The project guard: never act on a project other than the local config's.
cfgfile=${FUGARO_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/config.yaml}
if [ -n "${PROJECT:-}" ] && [ -f "$cfgfile" ]; then
  cfgproject=$(sed -n -e '/^project:/{' -e 's/^project:[[:space:]]*//' -e 's/[[:space:]]*#.*$//' -e 's/^["'"'"']//' -e 's/["'"'"']$//' -e 'p' -e '}' "$cfgfile" | head -n 1)
  if [ -n "$cfgproject" ] && [ "$cfgproject" != "$PROJECT" ]; then
    die "PROJECT=$PROJECT but $cfgfile says $cfgproject; refusing"
  fi
fi

case "$STEP" in
  apis)
    need PROJECT
    confirm "enables the Run, Storage, Secret Manager, Artifact Registry, Cloud Build, Logging and IAM APIs in $PROJECT (free to enable, billable once used)"
    run gcloud services enable run.googleapis.com storage.googleapis.com secretmanager.googleapis.com \
      artifactregistry.googleapis.com cloudbuild.googleapis.com logging.googleapis.com iam.googleapis.com --project "$PROJECT"
    ;;
  bucket)
    need PROJECT REGION BUCKET
    confirm "creates bucket gs://$BUCKET in $REGION with lifecycle rules: runs/ deleted after 90 days, cache/ 30 days after its custom time or 180 days after creation (storage billed per GB-month)"
    run gcloud storage buckets create "gs://$BUCKET" --project "$PROJECT" --location "$REGION" \
      --uniform-bucket-level-access --public-access-prevention
    lifecycle=$(mktemp)
    trap 'rm -f "$lifecycle"' EXIT
    cat > "$lifecycle" <<'JSON'
{"rule": [
  {"action": {"type": "Delete"}, "condition": {"age": 90, "matchesPrefix": ["runs/"]}},
  {"action": {"type": "Delete"}, "condition": {"daysSinceCustomTime": 30, "matchesPrefix": ["cache/"]}},
  {"action": {"type": "Delete"}, "condition": {"age": 180, "matchesPrefix": ["cache/"]}}
]}
JSON
    run gcloud storage buckets update "gs://$BUCKET" --project "$PROJECT" --lifecycle-file="$lifecycle"
    ;;
  registry)
    need PROJECT REGION
    confirm "creates Artifact Registry repository fugaro (docker) in $REGION (storage billed per GB-month)"
    run gcloud artifacts repositories create fugaro --repository-format docker --location "$REGION" --project "$PROJECT"
    ;;
  build-sa)
    need PROJECT REGION
    confirm "creates service account $BUILD_SA and grants it artifactregistry.writer on repository fugaro and logging.logWriter on $PROJECT"
    run gcloud iam service-accounts create fugaro-build --project "$PROJECT" --display-name "Fugaro image builds (M4 bootstrap)"
    wait_sa "$BUILD_SA"
    run gcloud artifacts repositories add-iam-policy-binding fugaro --location "$REGION" --project "$PROJECT" \
      --member "serviceAccount:$BUILD_SA" --role roles/artifactregistry.writer
    run gcloud projects add-iam-policy-binding "$PROJECT" --project "$PROJECT" \
      --member "serviceAccount:$BUILD_SA" --role roles/logging.logWriter --condition None
    ;;
  config)
    need PROJECT REGION BUCKET REPOS
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
      echo "build: { service_account: $BUILD_SA }"
      echo "user: $user"
      echo "repos:"
      for r in $REPOS; do
        IFS=: read -r name branch wf <<<"$r"
        [ -n "$name" ] && [ -n "$branch" ] && [ -n "$wf" ] || die "REPOS entry $r is not owner/name:branch:workflow"
        echo "  $name: { base_branch: $branch, workflows: [$wf] }"
      done
    } > "$body"
    cat "$body"
    if [ -e "$path" ]; then
      diff -u "$path" "$body" || true
      [ "${FORCE:-}" = 1 ] || die "$path exists; rerun with FORCE=1 to replace it"
    fi
    printf '+ write %s\n' "$path"
    if [ "$APPLY" = 1 ]; then mkdir -p "$(dirname "$path")"; install -m 0600 "$body" "$path"; fi
    ;;
  job-sa)
    need PROJECT BUCKET REPO WORKFLOW CHECKOUT
    sa_id=$(spec sa-id)
    sa=$(spec sa)
    cond=$(spec bucket-condition)
    confirm "creates service account $sa and grants it storage.objectUser on gs://$BUCKET, limited to its runs/, cache/ and locks/ prefixes"
    run gcloud iam service-accounts create "$sa_id" --project "$PROJECT" --display-name "Fugaro job $REPO $WORKFLOW (M4 bootstrap)"
    wait_sa "$sa"
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
    need PROJECT REPO WORKFLOW CHECKOUT
    sa=$(spec sa)
    ids=$(spec secret-ids)
    build_ids=$(spec build-secret-ids)
    confirm "grants secretmanager.secretAccessor to $sa on $(echo "$ids" | tr '\n' ' ')and to $BUILD_SA on $(echo "$build_ids" | tr '\n' ' ')"
    for id in $ids; do
      run gcloud secrets add-iam-policy-binding "$id" --project "$PROJECT" --member "serviceAccount:$sa" --role roles/secretmanager.secretAccessor
    done
    for id in $build_ids; do
      run gcloud secrets add-iam-policy-binding "$id" --project "$PROJECT" --member "serviceAccount:$BUILD_SA" --role roles/secretmanager.secretAccessor
    done
    ;;
  base)
    need PROJECT REGION FUGARO_SRC
    rev=$(git -C "$FUGARO_SRC" rev-parse --short HEAD)
    tag="$REGION-docker.pkg.dev/$PROJECT/fugaro/fugaro-web-node:dev-$rev"
    confirm "builds the web-node base from $FUGARO_SRC with local Docker and pushes $tag (about 1.5 GB of registry storage)"
    run "$HEAVY" sh "$FUGARO_SRC/images/build-base.sh" web-node "$tag"
    run gcloud auth configure-docker "$REGION-docker.pkg.dev" --quiet --project "$PROJECT"
    run "$HEAVY" docker push "$tag"
    echo "set base_image: $tag in the local config (BASE_IMAGE=$tag FORCE=1 gcp-m4.sh --apply config)"
    ;;
  image)
    need PROJECT REPO WORKFLOW CHECKOUT
    confirm "submits a Cloud Build in $PROJECT for $REPO/$WORKFLOW (billed per build-minute, E2_HIGHCPU_8)"
    ( cd "$CHECKOUT" && run "$FUGARO" image build --repo "$REPO" --workflow "$WORKFLOW" --json )
    ;;
  job)
    need PROJECT REGION REPO WORKFLOW CHECKOUT
    job=$(spec job)
    img=$(spec image)
    sa=$(spec sa)
    cpu=$(spec cpu)
    mem=$(spec memory)
    timeout=$(spec task-timeout)
    envs=$(spec env)
    secrets=$(spec secrets)
    confirm "creates or updates Cloud Run job $job in $REGION (billed per execution second)"
    run gcloud run jobs deploy "$job" --project "$PROJECT" --region "$REGION" \
      --image "$img:latest" --service-account "$sa" \
      --cpu "$cpu" --memory "$mem" --task-timeout "${timeout}s" \
      --max-retries 0 --tasks 1 --set-env-vars "$envs" --set-secrets "$secrets"
    ;;
  teardown)
    need PROJECT REGION BUCKET REPO WORKFLOW CHECKOUT
    job=$(spec job)
    sa=$(spec sa)
    sa_id=$(spec sa-id)
    cond=$(spec bucket-condition)
    ids=$(spec secret-ids)
    confirm "in $PROJECT, DELETES Cloud Run job $job, service account $sa and its conditional binding on gs://$BUCKET, and secrets $(echo "$ids" | tr '\n' ' ')with every accessor binding on them, including $BUILD_SA's. The secrets are $REPO's, shared by its other workflows. The bucket, the registry and $BUILD_SA are kept."
    run gcloud run jobs delete "$job" --project "$PROJECT" --region "$REGION" --quiet
    run gcloud storage buckets remove-iam-policy-binding "gs://$BUCKET" --member "serviceAccount:$sa" \
      --role roles/storage.objectUser --condition "expression=$cond,title=fugaro-$sa_id" --project "$PROJECT"
    for id in $ids; do run gcloud secrets delete "$id" --project "$PROJECT" --quiet; done
    run gcloud iam service-accounts delete "$sa" --project "$PROJECT" --quiet
    ;;
  teardown-all)
    need PROJECT REGION BUCKET
    [ "$ALL" = 1 ] || die "teardown-all deletes the shared resources; pass --all"
    if [ "$APPLY" = 1 ]; then
      left=$(gcloud run jobs list --project "$PROJECT" --region "$REGION" --filter 'metadata.name~^fugaro-' --format 'value(metadata.name)')
      [ -z "$left" ] || die "fugaro-* jobs still exist ($(echo "$left" | tr '\n' ' ')); run teardown for each repository first"
    fi
    confirm "in $PROJECT, DELETES bucket gs://$BUCKET with every run and cache, Artifact Registry repository fugaro in $REGION with every image, and service account $BUILD_SA. APIs stay enabled."
    run gcloud storage rm --recursive "gs://$BUCKET" --project "$PROJECT"
    run gcloud artifacts repositories delete fugaro --location "$REGION" --project "$PROJECT" --quiet
    run gcloud iam service-accounts delete "$BUILD_SA" --project "$PROJECT" --quiet
    ;;
  *)
    usage
    ;;
esac
