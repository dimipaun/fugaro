#!/usr/bin/env bash
# Build, deploy and seed the Fugaro portal in one project: the SPA to its Hosting
# site, the API and the key minter to Cloud Run, and optionally the operations
# organisation's first invitation.
#
# Usage: scripts/deploy-portal.sh [--project <id>] [--region <region>]
#                                 [--config <path>] [--image-repository <repo>]
#                                 [--seed-operations <address>] [--dry-run]
#
#   --project <id>       the project the portal runs in (default: fugaro-prod, D2194).
#                        The staging installation is fugaro-staging.
#   --region <region>    the region the two Cloud Run services and the build run in
#                        (default: us-east5, the region ee/portal/firebase.json's
#                        /api/** rewrite names).
#   --config <path>      the Hosting configuration, relative to the repository root
#                        (default: ee/portal/firebase.json). The portal has its own:
#                        the root firebase.json belongs to the website, and this
#                        script never touches it.
#   --image-repository <repo>
#                        where Cloud Build pushes the two images (default:
#                        <region>-docker.pkg.dev/<project>/fugaro-portal).
#   --seed-operations <address>
#                        after the deploy, write the operations organisation and
#                        invite <address> as its first owner, by running the API
#                        program's one-time seed-operations subcommand with the
#                        operator's own credentials. Once per installation: the
#                        subcommand itself refuses a second time.
#   --dry-run            install if needed and build the SPA, then print the cloud
#                        commands instead of running them. Nothing is deployed and
#                        nothing is seeded.
#
# OWNER-RUN. This deploys the service that signs licence keys and the site that
# sells them; no coding agent deploys or operates it, and the script refuses a
# session that is one (R5 as amended by D440). The images are P1.7's, built by
# Cloud Build from ee/keyservice/cloudbuild.yaml; the two services are that task's
# Terraform (ee/keyservice/terraform/services), whose defaults the names below
# follow.
#
# Authentication is whatever `gcloud auth login` and `firebase login` the operator
# already did. This script never reads, prints or takes a token or a key, and
# passes none to any tool.
#
# The bundle's Firebase configuration comes from the environment: the four
# VITE_FIREBASE_* values are read and refused when missing, because a bundle
# without them is a portal nobody can sign into. VITE_SENTRY_DSN,
# VITE_PORTAL_ENVIRONMENT and VITE_PORTAL_RELEASE are passed through when set.
set -euo pipefail

DEFAULT_PROJECT=fugaro-prod
DEFAULT_REGION=us-east5
DEFAULT_CONFIG=ee/portal/firebase.json

usage() {
  awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
}

die() {
  echo "deploy-portal: $*" >&2
  exit 1
}

# An agent's session must not deploy the portal: this is the one script in the
# repository that ships the licence-key service, and R5 (as amended by D440) puts
# it in the owner's hands only.
for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
  if [[ -n ${!name:-} ]]; then
    die "refusing to run in a coding agent's session ($name is set). The portal is owner-run: deploy it yourself, from your own shell."
  fi
done

project=$DEFAULT_PROJECT
region=$DEFAULT_REGION
config=$DEFAULT_CONFIG
image_repository=
seed_address=
dry_run=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --project)
      [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "--project needs a project id"
      project=$2
      shift 2
      ;;
    --region)
      [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "--region needs a region"
      region=$2
      shift 2
      ;;
    --config)
      [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "--config needs a path"
      config=$2
      shift 2
      ;;
    --image-repository)
      [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "--image-repository needs a repository"
      image_repository=$2
      shift 2
      ;;
    --seed-operations)
      [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "--seed-operations needs the address to invite"
      seed_address=$2
      shift 2
      ;;
    --dry-run)
      dry_run=true
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      die "unknown argument: $1"
      ;;
  esac
done

# A Google Cloud project id: 6-30 chars, lowercase letters, digits and hyphens,
# starting with a letter and not ending with a hyphen.
[[ $project =~ ^[a-z][a-z0-9-]{4,28}[a-z0-9]$ ]] || die "not a valid project id: $project"
[[ $region =~ ^[a-z]+-[a-z]+[0-9]+$ ]] || die "not a valid region: $region"

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
[[ -f $config ]] || die "the Hosting configuration is missing: $config"

for tool in node npm gcloud firebase git; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed or not on PATH"
done

missing=()
for name in VITE_FIREBASE_API_KEY VITE_FIREBASE_AUTH_DOMAIN VITE_FIREBASE_PROJECT_ID VITE_FIREBASE_APP_ID; do
  [[ -n ${!name:-} ]] || missing+=("$name")
done
if [[ ${#missing[@]} -gt 0 ]]; then
  die "the bundle's Firebase configuration is missing: ${missing[*]}. A bundle without it is a portal nobody can sign into: export them and run again."
fi

echo "deploy-portal: project $project, region $region, config $config"
if [[ $dry_run == true ]]; then
  echo "deploy-portal: dry run: nothing will be built in the cloud, deployed or seeded"
fi

image_base=${image_repository:-$region-docker.pkg.dev/$project/fugaro-portal}
tag=$(git rev-parse --short HEAD)

echo "deploy-portal: building the SPA (ee/portal)"
(
  cd "$root/ee/portal"
  if [[ ! -d node_modules ]]; then
    echo "deploy-portal: installing the pinned dependency tree (npm ci)"
    npm ci
  fi
  npm run build
)
[[ -n $(ls -A "$root/ee/portal/dist" 2>/dev/null) ]] || die "ee/portal/dist was not produced by the build"

# run_or_print CMD...: the whole of the cloud side, so that --dry-run prints the
# same commands a real run executes, in the same order.
run_or_print() {
  if [[ $dry_run == true ]]; then
    echo "deploy-portal: would run: $*"
    return 0
  fi
  echo "deploy-portal: running: $*"
  "$@"
}

run_or_print gcloud builds submit --config ee/keyservice/cloudbuild.yaml \
  --project "$project" --region "$region" .

run_or_print gcloud run deploy fugaro-portal-api \
  --image "$image_base/fugaro-portal-api:$tag" --project "$project" --region "$region"
run_or_print gcloud run deploy fugaro-key-minter \
  --image "$image_base/fugaro-key-minter:$tag" --project "$project" --region "$region"

run_or_print firebase deploy --only hosting --project "$project" --config "$config"

if [[ -n $seed_address ]]; then
  if [[ $dry_run == true ]]; then
    echo "deploy-portal: would run: GOOGLE_CLOUD_PROJECT=$project go run ./ee/keyservice/cmd/portal-api seed-operations $seed_address"
  else
    echo "deploy-portal: seeding the operations organisation for $seed_address"
    GOOGLE_CLOUD_PROJECT=$project go run ./ee/keyservice/cmd/portal-api seed-operations "$seed_address"
  fi
fi

if [[ $dry_run == true ]]; then
  echo "deploy-portal: dry run: nothing was deployed"
else
  echo "deploy-portal: done: $project is running the portal"
fi
