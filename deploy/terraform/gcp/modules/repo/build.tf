# The repository's build account: fugaro image build and the daily check
# submit builds as it, and the check job runs as it. Every grant is a
# member resource, scoped to this repository where the resource allows.

resource "google_service_account" "build" {
  project      = var.project
  account_id   = var.repo.build_service_account.account_id
  display_name = var.repo.build_service_account.display_name
}

# The provider credential and the workflows' build secrets, each at the
# secret level.
resource "google_secret_manager_secret_iam_member" "build" {
  for_each = toset(var.repo.build_secrets)

  project   = var.project
  secret_id = google_secret_manager_secret.this[each.value].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.build.member
}

# Writer on its own registry only; no admin role is ever granted.
resource "google_artifact_registry_repository_iam_member" "build_images" {
  project    = var.project
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.repository_id
  role       = "roles/artifactregistry.writer"
  member     = google_service_account.build.member
}

# The installation's tag mover role (tags.delete only), on its own registry
# only: writer lacks tags.delete, which the build's promote needs to move an
# existing :latest, and its untag to remove the candidate tag.
resource "google_artifact_registry_repository_iam_member" "build_tag_mover" {
  project    = var.project
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.repository_id
  role       = var.installation.role_ids.tag_mover
  member     = google_service_account.build.member
}

# Only operators write the base images, which every repository's build
# runs while holding that repository's credential.
resource "google_artifact_registry_repository_iam_member" "build_base" {
  project    = var.project
  location   = var.region
  repository = var.installation.base_registry
  role       = "roles/artifactregistry.reader"
  member     = google_service_account.build.member
}

resource "google_project_iam_member" "build" {
  for_each = {
    log_writer      = "roles/logging.logWriter"
    build_submitter = var.installation.role_ids.build_submitter
  }

  project = var.project
  role    = each.value
  member  = google_service_account.build.member
}

# A build that names this account needs actAs on it, and on nothing else.
resource "google_service_account_iam_member" "build_self" {
  service_account_id = google_service_account.build.name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.build.member
}

# Operators submit the repository's builds, so they act as its account,
# which amounts to reading its secrets.
resource "google_service_account_iam_member" "operator_build" {
  for_each = toset(var.installation.operators)

  service_account_id = google_service_account.build.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}

# builds/<slug>/ only: the build records and the check's decisions. The
# condition is the input byte for byte.
resource "google_storage_bucket_iam_member" "build" {
  bucket = var.installation.runs_bucket
  role   = "roles/storage.objectUser"
  member = google_service_account.build.member

  condition {
    title      = var.repo.build_bucket_condition.title
    expression = var.repo.build_bucket_condition.expression
  }
}
