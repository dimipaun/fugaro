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
# What it holds HERE, and what it deliberately does not. Five grants, and no
# sixth: the invoker bit on the credentials service, because the credential a
# build runs on is minted by that service's build route and Cloud Run refuses a
# caller at IAM before the container decides the class (iam.tf's verifier grant
# is the same fact); the writer and tag-mover roles on the ONE shared registry,
# below, which are the reach a catalog build's own push needs and the two roles
# the per-repository build account held there (modules/repo/registry.tf's
# build_shared and build_shared_tag_mover, whose comments this file repeats the
# reasons of); and the READER on the two registries a build pulls from —
# fugaro-base and the ghcr remote — because a build pulls as the account it
# runs as: its own step images are the base images in fugaro-base, and the
# derived build's step image and the FROM it builds on come through the ghcr
# remote (design §6, and the smoke stage of a D2305 entry pulls its pinned
# base). The per-repository build account held exactly those two readers for
# exactly that reason (modules/repo/build.tf's build_base and build_ghcr), and
# this file carries them over with the rest of the account's reach a catalog
# build needs.
#
# Nothing wider than the five (B6's acceptance): no role on any repository's
# own registry, no project role, no bucket, no secret. That the identity can
# read the two registries every build reads is not a widening beyond what a
# build does; a role anywhere else would be.
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
