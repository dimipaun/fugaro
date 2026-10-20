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

locals {
  # D145: the model credentials, by the variable each one is mounted as. The
  # three names are internal/config's (ReservedSecrets' Claude OAuth token and
  # Anthropic key, and ProviderKeyEnvPrefix), which is also where
  # config.ReservedEnvPrefixes refuses a workflow secret any variable that
  # could impersonate one — so a workflow's secret_env entry names a model
  # credential exactly when its variable is one of these. The git credential
  # is deliberately not here: D145 leaves Bitbucket alone and moves the
  # GitHub App key in C2.3, so both keep the accessor they have today.
  # TestModelSecretEnvsMatchTheCredentialsService pins these literals to Go.
  model_secret_env_names  = ["ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"]
  model_secret_env_prefix = "FUGARO_PROVIDER_KEY_"

  # One entry per mount, so the drop below is per mount and never per name.
  is_model_secret_env = {
    for env, logical in var.workflow.secret_env :
    env => contains(local.model_secret_env_names, env) || startswith(env, local.model_secret_env_prefix)
  }

  # The secrets the job's own account may read, by logical name: every secret
  # the job mounts while the credentials service is off — D147's "with it off
  # nothing changes", and the same set (and the same instance keys) as before
  # this file knew about the service — and, once it is on, every mounted
  # secret that is not a model credential (the service holds those, C1.11, and
  # C1.20 drops the job's mount of each). One member per mounted secret, never
  # the project's, never another workflow's.
  job_secret_names = toset([
    for env, logical in var.workflow.secret_env :
    logical if var.credentials_service == null || !local.is_model_secret_env[env]
  ])
}

# Each secret the job mounts and may read, at the secret level, never the
# project's.
resource "google_secret_manager_secret_iam_member" "job" {
  for_each = local.job_secret_names

  project   = var.project
  secret_id = var.secrets[each.value]
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.job.member
}

# The credentials service, once the installation has one (D135): the job's
# account may invoke it, and nothing else may — one member, the job's own
# account, roles/run.invoker, on that one service. It is the runner's only way
# to reach a model credential, and an unreachable service halts the run rather
# than falling back (D143).
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
