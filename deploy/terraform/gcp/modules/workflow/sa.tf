# The job's service account and everything it is granted. These exist even
# when the job itself isn't deployed yet, so its secrets and image can be
# prepared first.

resource "google_service_account" "job" {
  project      = var.project
  account_id   = var.workflow.service_account.account_id
  display_name = var.workflow.service_account.display_name
}

# Deploying or updating the job acts as its account.
resource "google_service_account_iam_member" "operator" {
  for_each = toset(var.operators)

  service_account_id = google_service_account.job.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}

# The runs bucket, conditioned on the repository's runs/, cache/ and locks/
# prefixes. The condition is the input byte for byte: one that differs is a
# different binding, which would sit next to the bootstrap's. The bootstrap
# set no description, so none is set here.
resource "google_storage_bucket_iam_member" "job" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectUser"
  member = google_service_account.job.member

  condition {
    title      = var.workflow.bucket_condition.title
    expression = var.workflow.bucket_condition.expression
  }
}

# The repository's own budget token objects, tokens/<slug>/, read-only: the
# launcher writes the sealed token there and the run reads it, and a run can
# neither delete nor replace a queued run's token (objectViewer has no
# delete or create; docs/design/budget-token-channel.md section 7.2, D16).
# The runs/, cache/ and locks/ grant above never covers tokens/. The
# precondition re-checks the bucket and the prefix, beside the variable's own
# validation of the shape and the slug.
resource "google_storage_bucket_iam_member" "job_tokens" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectViewer"
  member = google_service_account.job.member

  condition {
    title      = var.workflow.token_condition.title
    expression = var.workflow.token_condition.expression
  }

  lifecycle {
    precondition {
      condition     = startswith(var.workflow.token_condition.expression, "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/tokens/") && !strcontains(var.workflow.token_condition.expression, "||")
      error_message = "workflow.token_condition must name tokens/ of this runs bucket, in one clause (gcp.TokenBucketCondition)."
    }
  }
}

# Each secret the job mounts, at the secret level, never the project's.
# The model secrets are dropped when the installation has the credentials
# service on: the service's account is the identity that reads them
# (D143, D146), and a job that kept an accessor grant could read a model
# credential straight out of Secret Manager, past the cascade and past the
# accounting. The drop is by logical name, so a repository's own copy of a
# model secret loses its grant too. Everything else the workflow mounts —
# the provider credential, a repository's own secrets — is granted as before.
resource "google_secret_manager_secret_iam_member" "job" {
  for_each = toset([
    for name in values(var.workflow.secret_env) :
    name if var.credentials_service == null || !contains(var.credentials_service.secrets, name)
  ])

  project   = var.project
  secret_id = var.secrets[each.value]
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.job.member
}

# The job account's way into the cascade: run.invoker on the credentials
# service, and nothing more. The service's ingress requires IAM
# (installation/credentials.tf), so this grant is the only door, and it is
# one per job account of this installation's repositories: a run reaches the
# cascade with its own identity (D135). Created only with the opt-in — a
# grant naming a service that does not exist fails the apply — and never
# granted to a person.
resource "google_cloud_run_v2_service_iam_member" "credentials" {
  count = var.credentials_service == null ? 0 : 1

  project  = var.project
  location = var.credentials_service.region
  name     = var.credentials_service.name
  role     = "roles/run.invoker"
  member   = google_service_account.job.member
}

# Only a workflow whose agent authenticates through Vertex AI.
resource "google_project_iam_member" "vertex" {
  count = var.workflow.vertex ? 1 : 0

  project = var.project
  role    = "roles/aiplatform.user"
  member  = google_service_account.job.member
}
