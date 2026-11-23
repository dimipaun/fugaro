#!/usr/bin/env bash
# Build, deploy and seed the Fugaro portal in one project: the SPA to its Hosting
# site, the Firestore rules the database's clients are denied by, the API and the
# key minter to Cloud Run, and optionally the operations organisation's first
# invitation.
#
# Usage: scripts/deploy-portal.sh [--project <id>] [--region <region>]
#                                 [--config <path>] [--image-repository <repo>]
#                                 [--seed-operations <address>] [--dry-run]
#
#   --project <id>       the project the portal runs in (default: fugaro-prod, D2194).
#                        The staging installation is fugaro-staging, and it has a
#                        Hosting site of its own (P0.1): a site id is globally
#                        unique, so `fugaro-portal` belongs to fugaro-prod and
#                        fugaro-staging holds `fugaro-portal-staging`.
#   --region <region>    the region the two Cloud Run services and the build run in
#                        (default: us-east5, the region both configurations'
#                        /api/** rewrite names). The rewrite is where the SPA
#                        sends every call, so a value the --config does not name
#                        is refused rather than deployed to.
#   --config <path>      the Firebase configuration -- the Hosting site and the
#                        Firestore rules -- relative to the repository root. The
#                        default is the file the project's own site is in:
#                        ee/portal/firebase.staging.json for fugaro-staging, and
#                        ee/portal/firebase.json (the `fugaro-portal` site) for
#                        every other project. A path given here wins over that
#                        default, and one naming the other project's site is
#                        refused rather than deployed to. The portal has its own
#                        file: the root firebase.json belongs to the website, and
#                        this script never touches it.
#   --image-repository <repo>
#                        where Cloud Build pushes the two images (default:
#                        <region>-docker.pkg.dev/<project>/fugaro-portal).
#   --seed-operations <address>
#                        after the deploy, write the operations organisation and
#                        invite <address> as its first owner, by executing the
#                        seed job the services root declares (var.seed_job: the
#                        API's image in the API's own environment) with
#                        `--args=seed-operations,<address>`. Once per
#                        installation: the seed itself refuses a second time.
#   --dry-run            install if needed and build the SPA, then print the cloud
#                        commands instead of running them. Nothing is deployed and
#                        nothing is seeded.
#
# OWNER-RUN. This deploys the service that signs licence keys and the site that
# sells them; no coding agent deploys or operates it, and the script refuses a
# session that is one (R5 as amended by D440). The images are P1.7's, built by
# Cloud Build from ee/keyservice/cloudbuild.yaml; the two services are P1.5's
# Terraform (ee/keyservice/terraform/services), whose defaults the names below
# follow. The build definition and the two programs the images run are P1.7's,
# and none of the three is in the tree yet: the script refuses a tree that does
# not hold them rather than building the SPA and failing at its first cloud
# command with the site half-deployed.
#
# WHO OWNS THE RUNNING IMAGE. The Terraform owns both services and writes each
# one's image from var.api_image and var.minter_image, ignoring only `client` and
# `client_version`: the next apply of that root, for any reason and with whatever
# images it is given, puts both services back on the images in its own variables
# and the revision this script deployed stops serving. So the apply that decides
# the image has to be the one that carries it, and the line to run after a deploy
# is printed by the deploy itself: re-apply ee/keyservice/terraform/services with
# -var api_image and -var minter_image set to the two images just pushed, or the
# root has to stop managing template[0].containers[0].image (a change in that
# root, not in this script). Until one of the two is done, what is running and
# what Terraform would apply are the same only until that apply. The seed job
# below is that root's as well and takes var.api_image, so the one value covers
# what it runs too.
#
# WHY THE SEED IS A JOB AND NOT THIS SHELL. Seeding runs the API program's one
# subcommand, and that program builds the service the API serves from before it
# seeds: its own configuration, its own secrets, its own store, all read out of
# the environment a revision runs in and all required -- twenty-three FUGARO_
# variables and five Secret Manager containers, every one of them refused when it
# is missing (ee/keyservice/config.go). An operator's own credentials carry the
# project and nothing else, so a terminal cannot start the seed at all: it refuses
# before it writes anything, and it refuses after the site and both services are
# already deployed. The seed is therefore the services root's job (var.seed_job),
# which carries that environment because the root that declares the API declares
# it too, and this script only revises the job to the image just pushed and
# executes it with the address. That job is the root's, so the apply that created
# it has to have run -- P0.4's services apply, before this script in the plan's own
# order.
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
DEFAULT_SITE=fugaro-portal
# The staging installation: its own project, its own Hosting site, and so its own
# configuration. The two site ids are globally unique, which is exactly why the
# two projects cannot share one (P0.1): `fugaro-portal` is fugaro-prod's.
STAGING_PROJECT=fugaro-staging
STAGING_CONFIG=ee/portal/firebase.staging.json
STAGING_SITE=fugaro-portal-staging
# The seed's Cloud Run job, which ee/keyservice/terraform/services declares and
# names by its own default: the deploy-executed job and the applied one are one
# resource, and a name spelled differently in the two places is an execution of
# nothing.
DEFAULT_SEED_JOB=fugaro-portal-seed

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
config_given=false
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
      config_given=true
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

# Which configuration the deploy reads follows the project, unless --config named
# one. A site id is globally unique, so the two projects' sites are two different
# ids and the two files differ in exactly that: the staging project deployed
# against the prod file would be a deploy Firebase refuses, or one that landed
# this project's bundle on a site it does not own.
if [[ $config_given == false && $project == "$STAGING_PROJECT" ]]; then
  config=$STAGING_CONFIG
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
[[ -f $config ]] || die "the Firebase configuration is missing: $config"

# The cloud half is P1.7's -- the build definition and the two programs the
# images run -- and none of it is in the tree yet. It is checked here, before
# anything is built, because the deploy reaches it after the SPA build and a
# missing file found there is a site half-deployed: P1.7 is not a dependency this
# task can wait for, and a run that cannot work is one that says so at once.
for path in ee/keyservice/cloudbuild.yaml ee/keyservice/cmd/portal-api ee/keyservice/cmd/minter; do
  [[ -e $path ]] ||
    die "$path is missing: the images this deploys are P1.7's (ee/keyservice/cloudbuild.yaml and the two programs under ee/keyservice/cmd), and P1.7 has not landed yet. Nothing was built, deployed or seeded."
done

# The region is not the operator's to pick freely: the SPA reaches the API
# through the /api/** rewrite in the configuration above, so a service deployed
# anywhere else is one that rewrite cannot reach. The configuration names
# exactly one region -- the run rewrite's -- and the flag has to agree with it.
# It is read as text rather than parsed, so this needs no tool beyond the five
# the rest of the script requires.
config_regions=$(grep -o '"region"[[:space:]]*:[[:space:]]*"[^"]*"' "$config" | sed 's/.*"\([^"]*\)"$/\1/' || true)
if [[ -z $config_regions || $config_regions == *$'\n'* ]]; then
  die "$config does not name exactly one region: its /api/** rewrite's is the one the SPA reaches the API through, and this deploy has to run where it points"
fi
[[ $region == "$config_regions" ]] ||
  die "--region $region is not the region $config's /api/** rewrite names ($config_regions): the SPA reaches the API through that rewrite, so a service deployed elsewhere is one it cannot reach"

# The site is not the operator's to mix up either, and the two projects are the
# only pair that can be: each one's site id is written here, and the file a deploy
# reads has to name that id. A --config naming the other project's site is the
# deploy that puts one project's bundle on the other's domain -- the risk F40 and
# the preview workflow's gate are about -- and Firebase would answer it with a
# permission error at best, after the SPA was built and both images pushed. Read
# as text, like the region, so this needs no tool beyond the five below.
case $project in
  "$STAGING_PROJECT") project_site=$STAGING_SITE ;;
  "$DEFAULT_PROJECT") project_site=$DEFAULT_SITE ;;
  *) project_site= ;;
esac
if [[ -n $project_site ]]; then
  config_sites=$(grep -o '"site"[[:space:]]*:[[:space:]]*"[^"]*"' "$config" | sed 's/.*"\([^"]*\)"$/\1/' || true)
  [[ $config_sites == "$project_site" ]] ||
    die "$config names the Hosting site '${config_sites:-none}' and $project's own is $project_site: site ids are globally unique, so $DEFAULT_PROJECT holds $DEFAULT_SITE and $STAGING_PROJECT holds $STAGING_SITE, and one project's bundle cannot be deployed to the other's site. Pass --config $STAGING_CONFIG for $STAGING_PROJECT (its default) or --config $DEFAULT_CONFIG for $DEFAULT_PROJECT."
fi

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

# The tag names a commit, and Cloud Build reads the working tree: a tree with
# uncommitted changes is uploaded under HEAD's tag, so the image would be one the
# tag does not describe and nobody can rebuild from it. The services are then
# revised to it silently, which is the one thing a deploy cannot say afterwards.
if [[ -n $(git status --porcelain) ]]; then
  die "the working tree has uncommitted changes, and Cloud Build uploads the tree rather than the commit: an image tagged $tag would be one $tag does not describe. Commit or stash first."
fi

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

# The two names the run deploys are the two the build has to push, and the build
# is told them rather than asked to guess: a manual submit leaves $SHORT_SHA
# empty, and a --image-repository override reaches this script alone, so a build
# that named its own images would leave the two deploys below naming an image
# nothing pushed -- or, worse, a tag that already exists, which is the previous
# image still serving under the new revision's name. ee/keyservice/cloudbuild.yaml
# (P1.7) has to push $_REPOSITORY/fugaro-portal-api:$_TAG and
# $_REPOSITORY/fugaro-key-minter:$_TAG, and nothing else with those tags.
run_or_print gcloud builds submit --config ee/keyservice/cloudbuild.yaml \
  --substitutions="_REPOSITORY=$image_base,_TAG=$tag" \
  --project "$project" --region "$region" .

run_or_print gcloud run deploy fugaro-portal-api \
  --image "$image_base/fugaro-portal-api:$tag" --project "$project" --region "$region"
run_or_print gcloud run deploy fugaro-key-minter \
  --image "$image_base/fugaro-key-minter:$tag" --project "$project" --region "$region"

# The rules travel with the site they fence: the database the Terraform creates
# -- `portal`, named in the entry this configuration deploys them under, because
# a rules deploy names the database it applies to and a bare object is
# `(default)`, which this project has not got -- is deployed with the denial its
# clients are held to (ee/portal/firestore.rules, P1.5), and a site deployed
# without it is one whose store the next database creation decides the rules for.
run_or_print firebase deploy --only hosting,firestore:rules --project "$project" --config "$config"

if [[ -n $seed_address ]]; then
  # The dry run prints the two commands instead, so this line -- which says the
  # seed is happening -- is the real run's alone.
  [[ $dry_run == true ]] || echo "deploy-portal: seeding the operations organisation for $seed_address"
  # The job is the services root's and the image is this script's, so it is
  # revised to the image just pushed before it runs: the root writes the job's
  # image from var.api_image, which is still the previous image until an apply
  # carries the new one, and the seed is one execution against a first install --
  # which revision ran it is not something anybody can read afterwards.
  run_or_print gcloud run jobs update "$DEFAULT_SEED_JOB" \
    --image "$image_base/fugaro-portal-api:$tag" --project "$project" --region "$region"
  # --wait is what makes this the deploy's own step rather than a submission: the
  # execution's exit status is this line's, so a seed that refused is a deploy
  # that failed and says so.
  run_or_print gcloud run jobs execute "$DEFAULT_SEED_JOB" \
    --args="seed-operations,$seed_address" --project "$project" --region "$region" --wait
fi

if [[ $dry_run == true ]]; then
  echo "deploy-portal: dry run: nothing was deployed"
else
  echo "deploy-portal: done: $project is running the portal"
  # The services are Terraform's and the images are this script's, so an apply of
  # that root writes its own var.api_image and var.minter_image over what was just
  # deployed (see the header). Naming the two values while the owner is still at
  # the terminal is what makes that a step rather than a surprise.
  echo "deploy-portal: re-apply ee/keyservice/terraform/services with -var api_image=$image_base/fugaro-portal-api:$tag -var minter_image=$image_base/fugaro-key-minter:$tag, or its next apply puts the previous two images back"
fi
