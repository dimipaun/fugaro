# The project's build identity (B5, D2301): ONE service account per
# installation, in the installation's project, that a catalog build runs as.
#
# Why a project account and not a repository one. Until B6 every build ran as
# its repository's own build account (gcp.BuildServiceAccountID), created by
# the repository's root and destroyed with it. A catalog build bakes an entry
# of the project's published catalog, and that entry may name repositories
# whose root is gone (M2 retires them), so the account it runs as cannot be
# one of theirs: it is the project's, created by the same apply that turns the
# credentials service on, and it outlives every repository the catalog bakes
# (design §"one project identity", D2301).
#
# What it holds HERE, and what it deliberately does not. Every grant below is
# one of the per-repository build account's (modules/repo/build.tf and
# registry.tf, whose comments this file repeats the reasons of), moved onto the
# one account a catalog build now runs as — no more, and each of them as narrow
# as the grant it replaces:
#
#   - the invoker bit on the credentials service, because the credential a
#     build runs on is minted by that service's build route and Cloud Run
#     refuses a caller at IAM before the container decides the class (iam.tf's
#     verifier grant is the same fact);
#   - the writer and tag-mover roles on the ONE shared registry, which are the
#     reach a catalog build's own push needs (modules/repo/registry.tf's
#     build_shared and build_shared_tag_mover);
#   - the READER on the two registries a build pulls from — fugaro-base and the
#     ghcr remote — because a build pulls as the account it runs as: its own
#     step images are the base images in fugaro-base, and the derived build's
#     step image and the FROM it builds on come through the ghcr remote (design
#     §6, and the smoke stage of a D2305 entry pulls its pinned base);
#   - objectViewer on the copied catalog definitions, objectUser on the
#     repositories' build records and one secretAccessor per repository's
#     provider credential, which are the three reads and the one write a
#     catalog build's own steps make as the account they run as — the pin step
#     describes the definition, the record step writes the record the check
#     reads back, and the clone step's credential is resolved by Cloud Build's
#     AvailableSecrets as the build's account. Their targets are
#     var.catalog_check's repositories and the conditions that input carries:
#     the repositories whose catalog entries this project builds, which is
#     nobody else's.
#   - roles/logging.logWriter at the project, without which a build that names
#     a user-specified service account is refused or loses its step logs
#     (modules/repo/build.tf's `build` row held it too). It is the ONE
#     project-level role here, and the narrowest form the reach has: Cloud
#     Logging offers the write per project, and what it grants is appending
#     entries.
#
# The one grant below that is not the account's own reach is the operators'
# actAs on it (google_service_account_iam_member.operator_build_identity): a
# build a person submits has to name the account it runs as, and Cloud Build
# refuses a caller without iam.serviceAccounts.actAs on that account — the
# same grant modules/repo/build.tf's operator_build made per repository.
#
# Nothing wider than that. No role on any repository's own registry, no other
# project-level role, no bucket-wide prefix, no secret beyond the credentials a
# build of this project's catalog clones with: the records grants are one
# repository's builds/<slug>/catalog/ each and never a project-wide builds/
# one, which would reach every repository's check state and the definitions
# beside them; the definitions read is objectViewer and stays read-only.
#
# The ID is a LITERAL, which is the load-bearing half of the answer, exactly
# as verifier.tf's own comment says of the verifier: a module that knows
# var.project and an account ID fixed in the source makes the address a fact of
# the CONFIGURATION rather than of the apply —
# `<id>@<project>.iam.gserviceaccount.com` — computable before anything is
# planned (infra.BuildIdentityAccountID, the same spelling internal/infra/tf's
# cover reads a member of the grant below against, and the one B6 hands
# CatalogBuildSpec). A derived ID would be an address nobody could name until
# the apply had run, which is what the rendering below is the other half of.
resource "google_service_account" "build_identity" {
  count = local.credentials_service_on ? 1 : 0

  project      = var.project
  account_id   = "fugaro-build-identity"
  display_name = "Fugaro catalog build identity"

  depends_on = [google_project_service.this]
}

# The identity's one grant: it may call the credentials service, which is what
# mints the narrowed token a catalog build reads its repositories with (B1,
# D2301). Cloud Run grants the invoker per service and not per route, so this
# is also the bit that lets the identity reach every other route at IAM — and
# the class check inside the process is what decides what it is served there:
# its address is on the build class and on no other list, so every route but
# the build's refuses it (credsvc.NewAuthenticator, and the plan's rule "the
# narrowing lives in the route and in the generated step, never in the
# identity's grants").
#
# It is gated on the service for the reason the account is: with the service
# off there is no container to invoke, and the module is the switch that says
# whether an installation runs the build route at all.
resource "google_cloud_run_v2_service_iam_member" "credentials_build_identity" {
  count = local.credentials_service_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.credentials_service.service
  role     = "roles/run.invoker"
  member   = google_service_account.build_identity[0].member

  depends_on = [google_cloud_run_v2_service.credentials]
}

# What a catalog build's own push needs, on the installation's ONE shared
# registry: the writer role, which is where every catalog image lands
# (D1152), and the tag mover beside it — writer lacks tags.delete, and a
# catalog build's promote moves the moving latest and its content tag, which
# is the definition's own sha256 and so is moved again by every rebuild of an
# unchanged entry (PIC3's tag step). Both are the grants the per-repository
# build account held on this registry and on no other repository
# (modules/repo/registry.tf), moved here with the account they now belong to
# (B6): after M2 retires every repository root, an entry may name repositories
# whose build account is gone, and a build that pushed as one of those would
# push as an account that no longer exists.
#
# The tag mover is spelled as the literal path of the installation's own role
# and not read from google_project_iam_custom_role.tag_mover.name, for the
# reason credentials.tf's bindings give: the role resource's name attribute is
# computed, so a binding through it reaches the plan as an unknown role and
# internal/infra/tf's cover could not say which role the plan grants.
resource "google_artifact_registry_repository_iam_member" "build_identity_shared_writer" {
  count = local.credentials_service_on ? 1 : 0

  project    = var.project
  location   = google_artifact_registry_repository.shared.location
  repository = google_artifact_registry_repository.shared.repository_id
  role       = "roles/artifactregistry.writer"
  member     = google_service_account.build_identity[0].member
}

resource "google_artifact_registry_repository_iam_member" "build_identity_shared_tag_mover" {
  count = local.credentials_service_on ? 1 : 0

  project    = var.project
  location   = google_artifact_registry_repository.shared.location
  repository = google_artifact_registry_repository.shared.repository_id
  role       = "projects/${var.project}/roles/fugaroTagMover"
  member     = google_service_account.build_identity[0].member
}

# The two registries a build READS, on the same account. A Cloud Build step
# pulls its image as the build's service account, so a catalog build pulls
# these as the identity: the generated credential step's own image is the
# installation's base image, the derived build's step image and the FROM it
# builds on come through the ghcr remote (design §6), and a D2305 entry's
# smoke stage runs `docker pull` of the base it pinned. Without the reader the
# first step of a catalog build fails to start the container it runs.
#
# Reader, and no more, on each: neither registry is written by a build. Only
# operators write fugaro-base (iam.tf's base_writer, and the image every
# repository's credential step runs comes from there), and the ghcr remote is
# a pull-through proxy nothing writes. These are the per-repository build
# account's own two readers (modules/repo/build.tf's build_base and
# build_ghcr), moved here with the account a catalog build now runs as (B6):
# after M2 retires every repository root, a build that pulled as one of those
# accounts would pull as an account that no longer exists.
resource "google_artifact_registry_repository_iam_member" "build_identity_base_reader" {
  count = local.credentials_service_on ? 1 : 0

  project    = var.project
  location   = google_artifact_registry_repository.base.location
  repository = google_artifact_registry_repository.base.repository_id
  role       = "roles/artifactregistry.reader"
  member     = google_service_account.build_identity[0].member
}

resource "google_artifact_registry_repository_iam_member" "build_identity_ghcr_reader" {
  count = local.credentials_service_on ? 1 : 0

  project    = var.project
  location   = google_artifact_registry_repository.ghcr.location
  repository = google_artifact_registry_repository.ghcr.repository_id
  role       = "roles/artifactregistry.reader"
  member     = google_service_account.build_identity[0].member
}

# The rest of a catalog build's own steps, which stopped being a repository
# build account's when B6 moved the build onto this identity: the copied
# definitions a build reads and the record it writes. Without them every catalog
# build — the operator's and the nightly check's alike — fails on the first step
# that reaches one of them, because those steps run as the identity and it holds
# nothing of the runs bucket. The third thing that account held, the provider
# credential the build cloned with, is not this account's any more and not any
# account's: the build fetches it from the credentials service (IS8, D3330).
#
# Both the set of repositories and the two conditions are the catalog check's
# own input (var.catalog_check), and that is the honest carrier rather than a
# second list beside it: its repositories are exactly the repositories whose
# catalog entries this project builds, which is exactly whose definitions a
# build reads and whose record it writes. A repository the catalog does not
# build needs neither, and a repository added to the catalog is a tfvars change
# the same `fugaro update` that re-renders the check's grants applies.
#
# The definitions read is objectViewer and not objectUser, for the reason
# catalogcheck.tf's own grant gives: a build that could rewrite a definition
# could make every repository run an image of its choosing. The expression is
# the input's byte for byte (gcp.CatalogReadCondition); the title is the one
# gcp builds for THIS account (gcp.CatalogReadConditionTitle), because the
# condition an input carries is titled for the check job's account and a grant
# is read by whose it is.
resource "google_storage_bucket_iam_member" "build_identity_definitions" {
  count = local.credentials_service_on && var.catalog_check != null ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.build_identity[0].member

  condition {
    title      = "fugaro-catalog-read-${google_service_account.build_identity[0].account_id}"
    expression = var.catalog_check.definitions_condition.expression
  }
}

# The build record the catalog check reads back: one repository's
# builds/<slug>/catalog/ and nothing else, under that repository's own
# condition byte for byte (gcp.CatalogRecordsCondition) — the grant
# catalogcheck.tf makes to the check job, made to the account that now writes
# the record instead. One grant per repository, so the identity's reach is the
# union of the repositories this project's catalog builds and never a
# project-wide builds/ prefix, which would reach every other repository's
# records, its check state and the definitions beside them.
resource "google_storage_bucket_iam_member" "build_identity_records" {
  for_each = local.credentials_service_on && var.catalog_check != null ? { for r in var.catalog_check.repositories : r.slug => r } : {}

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectUser"
  member = google_service_account.build_identity[0].member

  condition {
    title      = each.value.records_condition.title
    expression = each.value.records_condition.expression
  }
}

# No provider credential, and that is the point of the account (IS8, D3330):
# a catalog entry's build used to mount the repository's git credential as its
# AvailableSecrets, and Cloud Build resolves those as the account the build runs
# as, so the identity held modules/repo/build.tf's own build grant one account
# over. The build asks the installation's credentials service for that
# credential now, so there is no container for this account to read and no
# accessor on one: the accessor that stood here until this release is gone, and
# what a build of an entry clones with is the service's answer and not a
# permission of the account it runs as.

# A build that names a user-specified service account is written to Cloud
# Logging as that account, and one without roles/logging.logWriter either
# has its build refused or loses every step log (catalog.go submits with
# Logging: CLOUD_LOGGING_ONLY). The per-repository build account this
# identity replaces held the role at the project (modules/repo/build.tf's
# `build` row, log_writer), and a catalog build cannot run without it: the
# whole point of a nightly rebuild is the log an operator reads when it
# fails.
#
# Project level, and that is the narrowest form the role has: Cloud
# Logging's write permission is not offered per log or per stream, and the
# only reach it gives is appending log entries (no read, no actAs, no way
# to a role, no storage permission), so it is not a path to anything the
# identity does not already write.
resource "google_project_iam_member" "build_identity_log_writer" {
  count = local.credentials_service_on ? 1 : 0

  project = var.project
  role    = "roles/logging.logWriter"
  member  = google_service_account.build_identity[0].member
}

# The operators' actAs on the identity, which is what makes a manual
# `fugaro image build <entry>` possible: the CLI submits the build as the
# identity (internal/cli/image_build_catalog.go names BuildIdentityEmail),
# and Cloud Build requires the caller to hold iam.serviceAccounts.actAs on
# the account the build runs as. Until B6 that grant was per repository
# (modules/repo/build.tf's operator_build, on that repository's own build
# account) and dies with the root at M2; without it here every operator's
# catalog build is refused with PERMISSION_DENIED at CreateBuild, and the
# nightly check's is the only one that could run.
#
# The account is a LITERAL path and not google_service_account
# .build_identity[0].name, for the reason catalogcheck.tf's own actAs gives:
# a grant whose account is computed is one internal/infra/tf's cover refuses
# to read.
resource "google_service_account_iam_member" "operator_build_identity" {
  for_each = local.credentials_service_on ? toset(var.operators) : toset([])

  service_account_id = "projects/${var.project}/serviceAccounts/fugaro-build-identity@${var.project}.iam.gserviceaccount.com"
  role               = "roles/iam.serviceAccountUser"
  member             = each.value

  depends_on = [google_service_account.build_identity]
}
