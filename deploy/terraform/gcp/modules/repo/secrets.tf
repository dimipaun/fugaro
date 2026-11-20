# The repository's secret containers. They hold no value here: values are
# added with fugaro secrets set, so none ever reaches Terraform or its
# state. The labels are the marks fugaro secrets set checks.
resource "google_secret_manager_secret" "this" {
  for_each = var.repo.secrets

  project   = var.project
  secret_id = each.value
  labels = {
    fugaro        = "managed"
    fugaro_repo   = var.repo.label
    fugaro_secret = each.key
  }

  # Replicated in the installation's region, and the replication of one that
  # exists ignored: a container's replication cannot be changed after it is
  # created (Secret Manager refuses the update), and any other declaration would
  # replace the container, which deletes every version in it first — which is
  # what an adopted container, replicated automatically like every container
  # created before this declaration, would meet. modules/installation's
  # secrets.tf states the whole of the reasoning; this is the repository's own
  # containers carrying the same declaration for the same reason (D3082, F9,
  # R5).
  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  lifecycle {
    prevent_destroy = true
    ignore_changes  = [replication]
  }
}

locals {
  # Every (secret, operator) pair, for the operator grants below.
  operator_secrets = { for p in setproduct(keys(var.repo.secrets), var.installation.operators) : "${p[0]} ${p[1]}" => { secret = p[0], member = p[1] } }
}

# Operators store values and read the secrets' metadata, never their values.
resource "google_secret_manager_secret_iam_member" "operator" {
  for_each = merge(
    { for k, v in local.operator_secrets : "${k} secretVersionAdder" => merge(v, { role = "roles/secretmanager.secretVersionAdder" }) },
    { for k, v in local.operator_secrets : "${k} viewer" => merge(v, { role = "roles/secretmanager.viewer" }) },
  )

  project   = var.project
  secret_id = google_secret_manager_secret.this[each.value.secret].secret_id
  role      = each.value.role
  member    = each.value.member
}
