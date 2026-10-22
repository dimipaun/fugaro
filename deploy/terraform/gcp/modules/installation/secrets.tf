# The installation's project-wide secret containers (design
# docs/design/project-secrets.md §4). They hold no value here: values are
# added with fugaro secrets set NAME --project-wide, so none ever reaches
# Terraform or its state. The labels are the marks that command sets and
# that fugaro update's discovery checks before adopting one.
#
# for_each is var.project_secrets, which fugaro update computes and unions with
# every correctly-labelled container that already exists live: a config edit
# can therefore only ever add a key, never drop one, since prevent_destroy
# turns a dropped key into a hard terraform plan failure (N3).
resource "google_secret_manager_secret" "project" {
  for_each = var.project_secrets

  project   = var.project
  secret_id = each.value
  labels = {
    fugaro         = "managed"
    fugaro_project = var.fugaro_project
    fugaro_secret  = each.key
  }

  # The adopted secrets are replicated automatically; any other replication
  # would replace them, which deletes every version first.
  replication {
    auto {}
  }

  lifecycle {
    prevent_destroy = true
  }
}

locals {
  # Every (secret, operator) pair, for the operator grants below.
  operator_project_secrets = { for p in setproduct(keys(var.project_secrets), var.operators) : "${p[0]} ${p[1]}" => { secret = p[0], member = p[1] } }
}

# Operators store values and read the secrets' metadata, never their values.
resource "google_secret_manager_secret_iam_member" "operator" {
  for_each = merge(
    { for k, v in local.operator_project_secrets : "${k} secretVersionAdder" => merge(v, { role = "roles/secretmanager.secretVersionAdder" }) },
    { for k, v in local.operator_project_secrets : "${k} viewer" => merge(v, { role = "roles/secretmanager.viewer" }) },
  )

  project   = var.project
  secret_id = google_secret_manager_secret.project[each.value.secret].secret_id
  role      = each.value.role
  member    = each.value.member
}
