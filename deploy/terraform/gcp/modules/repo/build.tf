# The repository's build account: fugaro image build and the daily check
# submit builds as it, and the check job runs as it. Every grant is a
# member resource, scoped to this repository where the resource allows.

locals {
  # The classes of secret the credentials service SERVES to this repository's
  # builds (B4.9, the S4 flip; D2076, D2077): a class named here is served, so
  # a switched repository's build request names none of that class's secrets
  # and its build account keeps no secretAccessor on them — the credential step
  # asks the installation's credentials service for the git credential and,
  # with it, for the workflow's own build secrets (B4.5's fetch).
  #
  # The members are config.BuildSecretClassGit and
  # config.BuildSecretClassBuild, and this literal is held to
  # config.BuildServedSecretClasses by deploy/terraform's
  # TestBuildServedClassesMatchTheModule, the way the module's other flip lists
  # are held to theirs: Terraform cannot ask Go.
  #
  # It is FILLED, which is the flip itself, and it carries both classes since
  # the flip's second commit filled it. The empty list is the inert state, and
  # it is what the release before this one planned: with it empty both gates
  # below are false whatever Go wrote, so this file plans exactly what it
  # planned before it knew about the service. Filled, what it moves is one
  # accessor list and one check mount, and only for a repository the service
  # serves — the gate below reads credentials_service_serves, which is false
  # for every repository the block is absent for and for the one handed the
  # address without being served, a `vertex` one (G9, D147, D3326) — whose
  # build fetches both credentials at the address Go writes into its request
  # (B4.5, D2076).
  build_served_secret_classes = ["git", "build"]

  # The build's own mounts leave with the class "build" (D2077): the build
  # account's accessor list, the request's availableSecrets and the build
  # step's secrets all go together, and the request carries no secret ID the
  # account may no longer read.
  build_mounts_served = var.installation.credentials_service != null && var.installation.credentials_service_serves && contains(local.build_served_secret_classes, "build")

  # The check job's mount leaves with EITHER class: the job runs as the build
  # account and mounts the repository's git credential (the class "git"), and
  # that credential's accessor is the build account's own, which the class
  # "build" takes away with the rest of the list — a job that mounted a secret
  # its account may not read is a job Cloud Run refuses to create (C2.3).
  check_mount_served = var.installation.credentials_service != null && var.installation.credentials_service_serves && length(local.build_served_secret_classes) > 0
}

resource "google_service_account" "build" {
  project      = var.project
  account_id   = var.repo.build_service_account.account_id
  display_name = var.repo.build_service_account.display_name
}

# The provider credential and the workflows' build secrets, each at the
# secret level. local.secrets, not google_secret_manager_secret.this, so a
# name Readiness resolved to a project-wide container grants the build
# account access to that same container (design §5).
#
# The list goes whole once the service serves this repository's builds (B4.9,
# D2076, D2077): the request names none of these secrets any more, so an
# accessor kept here would be one nothing reads, and the check job's own mount
# leaves with it (check.tf).
resource "google_secret_manager_secret_iam_member" "build" {
  for_each = toset(local.build_mounts_served ? [] : var.repo.build_secrets)

  project   = var.project
  secret_id = local.secrets[each.value]
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

# The remote repository holds the ghcr.io images the derived build pulls
# through it: its own step image and the FROM it builds on (design §6). It
# is a reader there like it is on fugaro-base, which stays for custom
# bases (design §5.2).
resource "google_artifact_registry_repository_iam_member" "build_ghcr" {
  project    = var.project
  location   = var.region
  repository = var.installation.ghcr_registry
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

# The installation's credentials service, once it has one (D2076, D2077): the
# build account may invoke it, and this module's own files give no other Cloud
# Run service a member — one member, the build account, roles/run.invoker, on
# that one service. B4.5 moves the build's git credential and its secret fetch
# there, and an account that cannot invoke the service is a build whose clone
# step has no credential left at all.
#
# Inert until B4.5 reads from it: today the credential comes from the secret
# accessor above and from the request's own secrets, so this grant changes
# nothing a build does. Which of the accounts allowed to call the service are
# served, and with what, is the service's own ID-token check (Q6): IAM says who
# may call it, the service says who is answered.
#
# It is granted on the DROP and not on the address (credentials_service_serves):
# a build fetches the credentials from the service exactly where this file took
# its accessors away, so the one repository handed the address without a drop —
# a `vertex` one — plans no grant here and its build reads what it mounts
# (D3326). The workflow module's own invoker grant reads the address instead,
# which is that repository's whole reason for carrying the block.
resource "google_cloud_run_v2_service_iam_member" "build_credentials" {
  count = var.installation.credentials_service == null || !var.installation.credentials_service_serves ? 0 : 1

  project  = var.project
  location = var.installation.credentials_service.region
  name     = var.installation.credentials_service.name
  role     = "roles/run.invoker"
  member   = google_service_account.build.member
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

# The copied catalog definitions (builds/catalog/), read-only: a build reads
# the definition PIC3 copied for the entry it is building, and never writes
# one. The condition is computed here from the bucket's own name rather than
# passed in, because it has no slug: every repository's build account reads
# the same project-wide copy, so the prefix is the constant
# gcp.CatalogBucketPrefix and the clause gcp.CatalogReadCondition builds (the
# roots/repo test pins this expression, and internal/infra/tf refuses the
# binding if it changes). objectViewer and not objectUser is the property
# that matters: a build account that could write a definition could make
# every other repository's jobs run an image of its choosing.
resource "google_storage_bucket_iam_member" "catalog_read" {
  bucket = var.installation.runs_bucket
  role   = "roles/storage.objectViewer"
  member = google_service_account.build.member

  condition {
    title      = "fugaro-catalog-read-${google_service_account.build.account_id}"
    expression = "resource.name.startsWith(\"projects/_/buckets/${var.installation.runs_bucket}/objects/builds/catalog/\")"
  }
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
