# Every grant is a member resource, never a binding or policy, so nothing
# here takes away access someone else granted.

resource "google_project_iam_custom_role" "launcher" {
  project = var.project
  role_id = var.names.role_ids.launcher
  title   = "Fugaro launcher"
  permissions = [
    "run.jobs.get",
    "run.jobs.list",
    "run.executions.get",
    "run.executions.list",
    "run.executions.cancel",
    "run.operations.get",
  ]

  depends_on = [google_project_service.this]
}

# Granted on each workflow job, by the repository module.
resource "google_project_iam_custom_role" "job_runner" {
  project     = var.project
  role_id     = var.names.role_ids.job_runner
  title       = "Fugaro job runner"
  permissions = ["run.jobs.run", "run.jobs.runWithOverrides"]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_custom_role" "build_submitter" {
  project     = var.project
  role_id     = var.names.role_ids.build_submitter
  title       = "Fugaro build submitter"
  permissions = ["cloudbuild.builds.create", "cloudbuild.builds.get"]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_member" "launcher" {
  for_each = toset(var.launchers)

  project = var.project
  role    = google_project_iam_custom_role.launcher.name
  member  = each.value
}

# Operators submit image builds while onboarding.
resource "google_project_iam_member" "operator_build_submitter" {
  for_each = toset(var.operators)

  project = var.project
  role    = google_project_iam_custom_role.build_submitter.name
  member  = each.value
}

# The runs bucket holds transcripts and caches, so only launchers and
# operators read it, at the bucket level.
resource "google_storage_bucket_iam_member" "runs" {
  for_each = toset(concat(var.launchers, var.operators))

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectAdmin"
  member = each.value
}

# The state bucket isn't managed here (fugaro init creates it before any
# Terraform runs), but operators added later need to read and lock the state.
resource "google_storage_bucket_iam_member" "state" {
  for_each = toset(var.operators)

  bucket = var.state_bucket
  role   = "roles/storage.objectAdmin"
  member = each.value
}

# Only operators write the base images. Build accounts get reader, granted
# by the repository module.
resource "google_artifact_registry_repository_iam_member" "base_writer" {
  for_each = toset(var.operators)

  project    = var.project
  location   = google_artifact_registry_repository.base.location
  repository = google_artifact_registry_repository.base.name
  role       = "roles/artifactregistry.writer"
  member     = each.value
}
