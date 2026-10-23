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
# profiles) does not hold for the bucket, and the owner ruled the question on
# 2026-10-17 — the installation-wide reach is ACCEPTED, on the terms
# SECURITY.md states. A profile segment in the object layout is a later
# breaking change, not phase 3's.
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

# The runs bucket's budget token objects, the whole of tokens/, read-only
# (gcp.ProjectTokenBucketCondition, plan task 3.2 step 2; budget-token-channel
# §7.2). The widened objectUser grant above is on runs/, cache/ and locks/ and
# never covers tokens/; this one is objectViewer, so the account can read a
# queued run's sealed token and can neither delete nor replace it, and what it
# reads is ciphertext — sealed under a key only the execution its launch minted
# it for receives — so the reach is over bytes, not over tokens. The widening
# is the same one D4 documents for the other three prefixes, for the same
# reason: one job serves every repository admitted to its profile, so its token
# read cannot name one repository's prefix.
resource "google_storage_bucket_iam_member" "job_tokens" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectViewer"
  member = google_service_account.job.member

  condition {
    title      = var.job.token_condition.title
    expression = var.job.token_condition.expression
  }

  lifecycle {
    precondition {
      condition     = startswith(var.job.token_condition.expression, "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/tokens/") && !strcontains(var.job.token_condition.expression, "||")
      error_message = "job.token_condition must name tokens/ of this runs bucket, in one clause (gcp.ProjectTokenBucketCondition)."
    }
  }
}

# Each project-wide secret the profile mounts, at the secret level, never the
# project's. The value is the logical name, so the grant waits for the
# container the installation module creates or adopts.
#
# One grant per secret the profile names, and none by name prefix: the
# design's §4.5 runtime fetch, which would grant a prefix of Secret Manager
# names through an IAM condition, is not adopted. That decision was taken
# while Check 52's U3 was unanswered and is unchanged now that it is answered
# — RUN 2026-10-16, the condition *is* honoured for
# secretmanager.versions.access — so the prefix form is expressible and
# nothing here depends on it either way.
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
