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
    # fugaro secrets ls: the project's secret list and each secret's
    # versions. Metadata only: reading a value needs secretAccessor, which
    # only the jobs and build accounts hold, per secret.
    "secretmanager.secrets.list",
    "secretmanager.versions.list",
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

# Granted to each repository's build account on that repository's own
# registry only, by the repository module. Moving an existing :latest
# (gcloud artifacts docker tags add) deletes the old tag first, and writer
# lacks tags.delete. No version or package delete: a build can remove a
# tag, never an image.
resource "google_project_iam_custom_role" "tag_mover" {
  project     = var.project
  role_id     = var.names.role_ids.tag_mover
  title       = "Fugaro tag mover"
  permissions = ["artifactregistry.tags.delete"]

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

# The runs bucket (design bucket-iam.md §3). Operators read and write all of
# it: fugaro/ (the project layer, the shared config, recipes, the marker),
# builds/, cache/, locks/ and runs/.
resource "google_storage_bucket_iam_member" "runs" {
  for_each = toset(var.operators)

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectAdmin"
  member = each.value
}

locals {
  # A launcher who is also an operator needs nothing more (H4).
  launchers_only = setsubtract(toset(var.launchers), toset(var.operators))
}

# Launchers read the whole bucket. Listing runs/ needs storage.objects.list,
# which a condition on an object's name can't grant, so this one has none.
resource "google_storage_bucket_iam_member" "runs_reader" {
  for_each = local.launchers_only

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = each.value
}

# Launchers write runs/ only. objectUser, not objectCreator: the launch
# claim's takeover overwrites and its release deletes. The condition is the
# input byte for byte, the same for every launcher, so IAM keeps them in one
# binding; no description, like the job accounts' conditions.
resource "google_storage_bucket_iam_member" "runs_launcher" {
  for_each = local.launchers_only

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectUser"
  member = each.value

  condition {
    title      = var.launcher_bucket_condition.title
    expression = var.launcher_bucket_condition.expression
  }

  lifecycle {
    precondition {
      condition     = var.launcher_bucket_condition.title == "fugaro-launchers-runs" && var.launcher_bucket_condition.expression == "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/runs/\")"
      error_message = "launcher_bucket_condition must be runs/ of the runs bucket, titled fugaro-launchers-runs (gcp.LauncherBucketCondition)."
    }
  }
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
