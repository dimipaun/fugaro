#!/bin/bash
# Regenerates internal/infra/tf/testdata/golden/*.plan.json: real
# `terraform show -json` plans of the three roots (and of the Firebase root in
# the same-project layout) (fresh creates), cut down to
# their resource_changes. Needs terraform, jq and the google provider already
# in a plugin cache directory (the one fugaro keeps: ~/.cache/fugaro/terraform-plugins).
# It plans with a fake access token and an unreachable HTTPS proxy, so no
# network call can leave the machine: a fresh create plan reads nothing from
# Google. The inputs are the golden tfvars the Go and Terraform tests already use.
# Usage: scripts/gen-golden-plans.sh [plugin-cache-dir]
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
cache=${1:-$HOME/.cache/fugaro/terraform-plugins}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp -r "$repo/deploy/terraform/gcp" "$tmp/gcp"
cat > "$tmp/rc" <<RC
provider_installation {
  filesystem_mirror { path = "$cache" }
}
RC
cp "$repo/internal/infra/testdata/installation-budget-job.tfvars.json" "$tmp/gcp/roots/installation/terraform.tfvars.json"
cp "$repo/internal/infra/testdata/firebase.tfvars.json" "$tmp/gcp/roots/firebase/terraform.tfvars.json"
cp "$repo/deploy/terraform/gcp/roots/repo/tests/testdata/github-vertex.tfvars.json" "$tmp/gcp/roots/repo/terraform.tfvars.json"
# Terraform runs under env -i: nothing of the caller's environment (GOOGLE_*,
# TF_VAR_*, TF_CLI_ARGS*, impersonation, credentials) reaches it. Only PATH, a
# throwaway HOME, the TF_* it needs, a fake token and a dead proxy.
tfenv=(env -i "PATH=$PATH" "HOME=$tmp/home" "TF_CLI_CONFIG_FILE=$tmp/rc" TF_IN_AUTOMATION=1 TF_INPUT=0 CHECKPOINT_DISABLE=1
  GOOGLE_OAUTH_ACCESS_TOKEN=fake
  HTTPS_PROXY=http://127.0.0.1:9 HTTP_PROXY=http://127.0.0.1:9 https_proxy=http://127.0.0.1:9 http_proxy=http://127.0.0.1:9
  ALL_PROXY=http://127.0.0.1:9 all_proxy=http://127.0.0.1:9 NO_PROXY= no_proxy=)
mkdir -p "$tmp/home"
# adopt_variant derives an import variant of a golden plan with jq alone (a real
# plan with import blocks makes the provider read the live resource, which needs
# network and credentials, so none can be made here). Each resource in the
# address->ID map goes from a fresh create to what Terraform emits for a
# matching import: actions ["no-op"], before equal to after, after_unknown
# empty (nothing is unknown once the object exists) and importing {id}, the
# shape internal/infra/tf/testdata/plan_import.json has from real terraform
# (TestAdoptVariantShapeMatchesRealTerraform pins it). An optional jq filter
# runs on the other changes afterwards, for values that stop being unknown once
# the imported object exists (a binding's role read from an imported role).
# Usage: adopt_variant src.plan.json dst.plan.json '{"address":"id",...}' ['filter']
adopt_variant() {
  jq -c --argjson ids "$3" '
    .resource_changes |= map(
      if $ids[.address] != null then
        $ids[.address] as $id
        | .change |= (.actions = ["no-op"] | .before = .after | .after_unknown = {} | .importing = {id: $id})
      else ('"${4:-.}"') end)' "$1" > "$2"
}
golden=$repo/internal/infra/tf/testdata/golden

# name:root pairs. firebase-same-project is the Firebase root with the
# installation's own project as the Firebase project (skip_apis set).
for pair in installation:installation repo:repo firebase:firebase firebase-same-project:firebase; do
  r=${pair%%:*}
  root=${pair##*:}
  (
    cd "$tmp/gcp/roots/$root"
    if [ "$r" = firebase-same-project ]; then
      cp "$repo/internal/infra/testdata/firebase-same-project.tfvars.json" terraform.tfvars.json
    fi
    sed -i.bak '/backend "gcs" {}/d' main.tf
    "${tfenv[@]}" terraform init -backend=false >/dev/null
    "${tfenv[@]}" terraform plan -out=plan.bin >/dev/null
    "${tfenv[@]}" terraform show -json plan.bin | jq -c '{format_version,terraform_version,resource_changes}' > "$golden/$r.plan.json"
  )
done
adopt_variant "$golden/installation.plan.json" "$golden/installation-adopt.plan.json" '{
  "module.installation.google_storage_bucket.runs": "proj-1234/fugaro-runs-proj-1234",
  "module.installation.google_project_iam_custom_role.launcher": "projects/proj-1234/roles/fugaroLauncher"}'
adopt_variant "$golden/repo.plan.json" "$golden/repo-adopt.plan.json" '{
  "module.repo.google_secret_manager_secret.this[\"github-app-key\"]": "projects/proj-1234/secrets/fugaro-acme-webapp-github-app-key-35b331db19bcc682",
  "module.repo.module.workflow[\"api\"].google_service_account.job": "projects/proj-1234/serviceAccounts/fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com"}'
adopt_variant "$golden/firebase.plan.json" "$golden/firebase-adopt.plan.json" '{
  "module.firebase.google_firebase_database_instance.this": "projects/aurora-fp/locations/us-central1/instances/aurora-fp-default-rtdb",
  "module.firebase.google_apikeys_key.web": "projects/aurora-fp/locations/global/keys/fugaro-web",
  "module.firebase.google_service_account.signer": "projects/aurora-fp/serviceAccounts/fugaro-token-signer@aurora-fp.iam.gserviceaccount.com",
  "module.firebase.google_project_iam_custom_role.token_minter": "projects/aurora-fp/roles/fugaroTokenMinter"}' '
  if .type == "google_service_account_iam_member" then
    .change.after.role = "projects/aurora-fp/roles/fugaroTokenMinter"
    | .change.after.service_account_id = "projects/aurora-fp/serviceAccounts/fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
    | del(.change.after_unknown.role, .change.after_unknown.service_account_id)
  else . end'
echo "wrote $golden/*.plan.json"
