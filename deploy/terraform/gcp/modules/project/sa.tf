# The project job's service account and everything it is granted: one per
# (profile, provider, account), because the secrets a job mounts are readable
# by every run in that job (design §4.1, §7).

resource "google_service_account" "job" {
  project      = var.project
  account_id   = var.job.service_account.account_id
  display_name = var.job.service_account.display_name
}

# Deploying or updating the job acts as its account.
resource "google_service_account_iam_member" "operator" {
  for_each = toset(var.operators)

  service_account_id = google_service_account.job.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}

# The runs bucket, conditioned on runs/, cache/ and locks/ — the whole
# prefixes, with no slug (gcp.ProjectBucketCondition, plan task 3.2 step 2).
#
# This is a widening of the 0.7.0 property that a job account's objects are
# its own repository's: one project job serves every repository admitted to
# its profile, so the object it writes is no longer inside one repository's
# prefix. SECURITY.md and docs/design/bucket-iam.md state the regression in
# full (D4), including another queued run's budget token (D13, accepted
# 2026-10-15) and another run's launch.json/result.json (task 3.4). Every
# project job gets this same condition whatever its profile, so a profile
# separates secrets, not bucket objects: the mitigation D4 named (separate
# profiles) does not hold for the bucket and is open for the owner to re-rule.
#
# The condition is the input byte for byte: one that differs is a different
# binding, and the review screen (internal/infra/tf) accepts it only for an
# account this module creates. The bootstrap set no description, so none is
# set here.
resource "google_storage_bucket_iam_member" "job" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectUser"
  member = google_service_account.job.member

  condition {
    title      = var.job.bucket_condition.title
    expression = var.job.bucket_condition.expression
  }
}

# Each project-wide secret the profile mounts, at the secret level, never the
# project's. The value is the logical name, so the grant waits for the
# container the installation module creates or adopts.
#
# One grant per secret the profile names, and none by name prefix: the
# design's §4.5 runtime fetch, which would grant a prefix of Secret Manager
# names through an IAM condition, is not adopted (Check 52's U3 is unanswered,
# so nothing here depends on it).
resource "google_secret_manager_secret_iam_member" "job" {
  for_each = toset(values(var.job.secret_env))

  project   = var.project
  secret_id = var.secrets[each.value]
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.job.member
}

# Only a profile whose runs authenticate through Vertex AI.
resource "google_project_iam_member" "vertex" {
  count = var.job.vertex ? 1 : 0

  project = var.project
  role    = "roles/aiplatform.user"
  member  = google_service_account.job.member
}
