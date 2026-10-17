#!/usr/bin/env bash
# Build, check and deploy the website (website/) to Firebase Hosting.
#
# Usage: scripts/deploy-website.sh [--project <id>] [--channel <name>] [--dry-run]
#
#   --project <id>    the website's Firebase project (default: fugaro-prod, the
#                     project the owner created for the site only, D91). Never an
#                     installation's project.
#   --channel <name>  deploy to a Firebase Hosting preview channel instead of live.
#   --dry-run         install if needed, build and run the site's gates, then print
#                     the deploy command instead of running it. Nothing is deployed.
#
# Authentication is whatever `firebase login` the operator already did. This script
# never reads, prints or takes a token or a key, and passes none to the CLI.
# The hosting configuration is firebase.json at the repository root; there is no
# .firebaserc, so the project is always named on the command line.
set -euo pipefail

DEFAULT_PROJECT=fugaro-prod

usage() {
  sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
}

die() {
  echo "deploy-website: $*" >&2
  exit 1
}

project=$DEFAULT_PROJECT
channel=
dry_run=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --project)
      [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "--project needs a project id"
      project=$2
      shift 2
      ;;
    --channel)
      [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "--channel needs a channel name"
      channel=$2
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
if [[ -n $channel ]]; then
  [[ $channel =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$ ]] || die "not a valid channel name: $channel"
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
site=$root/website

for tool in node npm firebase; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed or not on PATH"
done
[[ -f $root/firebase.json ]] || die "firebase.json is missing at the repository root"

echo "deploy-website: project $project${channel:+, preview channel $channel}"
if [[ $dry_run == true ]]; then
  echo "deploy-website: dry run: nothing will be deployed"
fi

cd "$site"
if [[ ! -d node_modules ]]; then
  echo "deploy-website: installing the pinned dependency tree (npm ci)"
  npm ci
fi

# The site's gate: the build, then every website/test/*.mjs.
echo "deploy-website: building and checking (npm run check)"
npm run check

[[ -d $site/dist ]] || die "website/dist was not produced by the build"
[[ -n $(ls -A "$site/dist") ]] || die "website/dist is empty"

cd "$root"
if [[ -n $channel ]]; then
  cmd=(firebase hosting:channel:deploy "$channel" --project "$project")
else
  cmd=(firebase deploy --only hosting --project "$project")
fi

if [[ $dry_run == true ]]; then
  echo "deploy-website: would run: ${cmd[*]}"
  exit 0
fi

echo "deploy-website: running: ${cmd[*]}"
"${cmd[@]}"
