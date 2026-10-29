#!/bin/bash
# Regenerates internal/infra/tf/testdata/golden/*.plan.json: real
# `terraform show -json` plans of the three roots (the Firebase root in the
# same-project layout, and the installation root with the account axis's
# tfvars and with managed_deployer_scoped set) (fresh creates), cut down to
# their resource_changes, plus repo-scope-move.plan.json, the one golden made
# in two steps from a synthetic prior state (see below). Needs terraform, jq
# and the google provider already in a plugin cache directory (the one fugaro
# keeps: ~/.cache/fugaro/terraform-plugins).
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
# installation's own project as the Firebase project (skip_apis set);
# installation-accounts is the installation root with the account axis's
# tfvars, so that root has a plan holding an account job (its token mount and
# the secret grant beside it) beside the no-account one every installation
# already deploys; installation-managed is the installation root with
# managed_deployer_scoped set: the managed deployer's scoped role and its
# conditioned binding, which are off by default (0.8.6) and so absent from
# installation.plan.json, are still planned and covered there;
# installation-credentials is the installation root with the credentials
# service ON, so the service, its account, its one bundle container and its
# one accessor grant are planned and covered, and its SEVEN environment
# variables are read off a real plan (C1.14: the module's own names and
# nothing else — TestGoldenCredentialsServiceCarriesItsSevenNames reads the
# values back against the fixture). installation-coordinator and
# firebase-coordinator are the two roots with the coordinator's opt-in ON
# (coordinator plan P5): its account, the account's two reads, its job and its
# schedule, and the Firebase root's database grant to it. The opt-in is off by
# default, so they are goldens of their own and every other golden here plans
# no coordinator at all.
for pair in installation:installation installation-accounts:installation installation-managed:installation installation-credentials:installation installation-coordinator:installation repo:repo firebase:firebase firebase-same-project:firebase firebase-coordinator:firebase; do
  r=${pair%%:*}
  root=${pair##*:}
  (
    cd "$tmp/gcp/roots/$root"
    case "$r" in
      firebase-same-project) cp "$repo/internal/infra/testdata/firebase-same-project.tfvars.json" terraform.tfvars.json ;;
      installation) cp "$repo/internal/infra/testdata/installation-budget-job.tfvars.json" terraform.tfvars.json ;;
      installation-accounts) cp "$repo/internal/infra/testdata/installation-accounts.tfvars.json" terraform.tfvars.json ;;
      installation-managed) jq '. + {managed_deployer_scoped: true}' "$repo/internal/infra/testdata/installation-budget-job.tfvars.json" > terraform.tfvars.json ;;
      installation-credentials) cp "$repo/internal/infra/testdata/installation-credentials.tfvars.json" terraform.tfvars.json ;;
      installation-coordinator) cp "$repo/internal/infra/testdata/installation-coordinator.tfvars.json" terraform.tfvars.json ;;
      firebase-coordinator) cp "$repo/internal/infra/testdata/firebase-coordinator.tfvars.json" terraform.tfvars.json ;;
    esac
    sed -i.bak '/backend "gcs" {}/d' main.tf
    "${tfenv[@]}" terraform init -backend=false >/dev/null
    "${tfenv[@]}" terraform plan -out=plan.bin >/dev/null
    "${tfenv[@]}" terraform show -json plan.bin | jq -c '{format_version,terraform_version,resource_changes}' > "$golden/$r.plan.json"
  )
done

# repo-scope-move.plan.json is the one golden that needs a PRIOR state. It is
# design docs/design/project-secrets.md §6 step 4: a secret name that stops
# being repo-only and becomes project-shared (the repo copy has no version,
# the project copy is resolved), so its grants move to the project's container
# while the repository's own container is kept. Only a plan with a state to
# diff against shows either half of that, and every other golden here is a
# fresh create. Two real steps, offline throughout:
#
# 1. state_from_plan turns the repo root's own fresh plan (the repo-only
#    config, bitbucket-oauth.tfvars.json) into the state a real apply of it
#    would have written: the plan's own values, plus, for each attribute the
#    fresh plan left unknown, a value of the schema's own type. That is the
#    same "this already exists" claim adopt_variant makes and no more — the
#    diff in step 2 is computed for real. The schema types matter: a state
#    file that gives a string where a map or a block belongs is refused
#    outright ("missing expected {", "a number is required"). Values are
#    otherwise absent from a fresh plan, so unknown attributes carry the
#    placeholder fugaro-synthetic-state-value, which is why the committed
#    golden's `before` values are not a real state's.
# 2. terraform plan -refresh=false -state=<that state> against the
#    project_secrets-bearing tfvars (roots/repo/tests/testdata/
#    project-secrets.tfvars.json, the same fixture repo.tftest.hcl uses)
#    reads nothing from the provider, so the no-network guarantee holds.
cat > "$tmp/state_from_plan.jq" <<'JQ'
def walkmod($addr):
  ( (.resources // []) | map({ m: $addr, r: . }) )
  + ( (.child_modules // []) | map(walkmod(.address)) | add // [] );

def idxkey:
  ( [ .address | capture("\\[(?<i>[^\\]]*)\\]$")? ] | .[0] ) as $i
  | if $i == null then null
    elif ($i.i | test("^[0-9]+$")) then ($i.i | tonumber)
    else ($i.i | ltrimstr("\"") | rtrimstr("\"")) end;

def ph: "fugaro-synthetic-state-value";

def phtype($t):
  if $t == "string" then ph
  elif $t == "bool" then false
  elif $t == "number" then 0
  elif ($t | type) == "array" then
    (if $t[0] == "map" then {} elif ($t[0] == "list" or $t[0] == "set") then [] else ph end)
  else ph end;

# $n is the schema node: {a: type} for an attribute, {b: block} for a block.
def fill($u; $v; $n):
  if $u == true then
    (if $v == null then (if $n.a then phtype($n.a) elif $n.b then [] else ph end) else $v end)
  elif ($u | type) == "object" and (($v | type) == "object" or $v == null) then
    $n.b as $b
    | reduce ($u | keys_unsorted[]) as $k (($v // {});
        .[$k] = fill($u[$k]; ($v // {})[$k];
          (if $b != null and $b.block_types[$k] != null then { b: $b.block_types[$k].block }
           elif $b != null and $b.attributes[$k] != null then { a: $b.attributes[$k].type }
           else null end)))
  elif ($u | type) == "array" and (($v | type) == "array" or $v == null) then
    if ($u | length) == 0 then ($v // [])
    elif (($v // []) | length) == ($u | length)
    then [ range(0; ($u | length)) | fill($u[.]; ($v // [])[.]; $n) ]
    else ($v // []) end
  else $v end;

# A service account's name is derived, and the build account's grant is
# validated against it as an IAM member string, so a bare placeholder there is
# refused: derive it the way the provider does.
def fixup($rt; $a):
  if $rt == "google_service_account" and $a.name == ph and $a.email != null
  then $a + { name: "projects/\($a.project)/serviceAccounts/\($a.email)" }
  else $a end;

# An IAM member's service_account_id is that derived name, which the account
# resource above already carries: resolve it the same way the config does, or
# the member shows a replace that has nothing to do with this scenario.
def sapfix($res):
  ( [ $res[] | select(.type == "google_service_account")
      | { key: (.module // ""), value: .instances[0].attributes.name } ] | from_entries ) as $sas
  | $res
  | map(if .type == "google_service_account_iam_member" then
          .module as $m
          | .instances |= map(.attributes.service_account_id =
              (if .attributes.service_account_id == ph then ($sas[$m // ""] // ph)
               else .attributes.service_account_id end))
        else . end);

( .resource_changes | map({ key: .address, value: .change.after_unknown }) | from_entries ) as $au
| ( [ $schema[0].provider_schemas[].resource_schemas ] | add ) as $rs
| { version: 4,
    terraform_version: .terraform_version,
    serial: 1,
    lineage: "00000000-0000-0000-0000-000000000000",
    outputs: {},
    resources:
      ( .planned_values.root_module | walkmod(null) )
      | group_by([.m, .r.type, .r.name])
      | map( .[0].r.type as $rt
             | { mode: .[0].r.mode,
                 type: .[0].r.type,
                 name: .[0].r.name,
                 provider: "provider[\"\(.[0].r.provider_name)\"]",
                 instances: ( map( .r as $r
                                   | { schema_version: ($r.schema_version // 0),
                                       attributes: ( fill($au[$r.address]; $r.values; { b: $rs[$rt].block }) | fixup($rt; .) ),
                                       sensitive_attributes: [] }
                                   + ( ($r | idxkey) as $k | if $k == null then {} else { index_key: $k } end )) ) }
               + ( if .[0].m == null then {} else { module: .[0].m } end )
           )
      | sapfix(.)
  }
JQ
(
  cd "$tmp/gcp/roots/repo"
  cp "$repo/deploy/terraform/gcp/roots/repo/tests/testdata/bitbucket-oauth.tfvars.json" terraform.tfvars.json
  "${tfenv[@]}" terraform init -backend=false >/dev/null
  "${tfenv[@]}" terraform plan -out=scope1.bin >/dev/null
  "${tfenv[@]}" terraform show -json scope1.bin > "$tmp/scope1.json"
  "${tfenv[@]}" terraform providers schema -json > "$tmp/schema.json"
  jq -c --slurpfile schema "$tmp/schema.json" -f "$tmp/state_from_plan.jq" "$tmp/scope1.json" > "$tmp/scope1.state.json"
  cp "$repo/deploy/terraform/gcp/roots/repo/tests/testdata/project-secrets.tfvars.json" terraform.tfvars.json
  "${tfenv[@]}" terraform plan -refresh=false -state="$tmp/scope1.state.json" -out=scope2.bin >/dev/null
  "${tfenv[@]}" terraform show -json scope2.bin | jq -c '{format_version,terraform_version,resource_changes}' > "$golden/repo-scope-move.plan.json"
)

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
  if (.address | startswith("module.firebase.google_service_account_iam_member.minter[")) then
    .change.after.role = "projects/aurora-fp/roles/fugaroTokenMinter"
    | .change.after.service_account_id = "projects/aurora-fp/serviceAccounts/fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
    | del(.change.after_unknown.role, .change.after_unknown.service_account_id)
  else . end'
echo "wrote $golden/*.plan.json"
