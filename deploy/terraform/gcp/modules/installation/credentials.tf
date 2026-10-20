# The credentials service (D146): the one place a model credential lives once
# an installation opts in. It is Fugaro's own infrastructure, in the
# installation root — `fugaro managed apply` never deploys it (Global
# Constraint 7) — and it is OFF by default: with var.credentials_service null
# this file creates nothing, and every job keeps the model-secret accessors
# it has today (D147).
#
# Q6 (the owner's ruling, 2026-10-19). The service's reach in Secret Manager
# is ONE secret container and ONE accessor grant on it (Global Constraint 12):
# every credential it needs is one YAML document, read in one go by the loader
# (C1.11), and there is no per-credential grant and no `condition` over the
# model secrets — no secretmanager.googleapis.com/secretId filter, no
# name-prefix fence. TestCredentialsServiceGrantsAreTheOneBundle below is what
# keeps that true.

locals {
  # The whole file is gated on the one opt-in: null creates nothing, and a
  # single boolean keeps the five resources of this file in step.
  credentials_service_on = var.credentials_service != null
}

# The service's own account. It reads the bundle and nothing else: no project
# role, no bucket, and — the rule history.tf states — nobody is granted actAs
# on it, operators included, because anyone who could deploy a revision as it
# could read every model credential the installation holds.
resource "google_service_account" "credentials" {
  count = local.credentials_service_on ? 1 : 0

  project      = var.project
  account_id   = var.credentials_service.account_id
  display_name = "Fugaro credentials service"

  lifecycle {
    precondition {
      condition     = var.credentials_service != null
      error_message = "the credentials service's account needs var.credentials_service"
    }
  }

  depends_on = [google_project_service.this]
}

# The one container, and the only Secret Manager secret this service ever
# reads. Terraform creates the container and never a version: the document is
# written by the owner-run `fugaro creds set` (C1.25) with the owner's own
# identity, so no credential value ever reaches Terraform or its state
# (TestNoSecretVersionsInTerraform). A container with no document yet is fine:
# the loader fails closed on a missing or unparseable bundle (C1.11), so the
# service answers a named refusal rather than an empty credential set.
#
# The labels are `fugaro=managed` and the installation's name, and
# deliberately NOT fugaro_secret: that label is what `fugaro init`'s discovery
# and `fugaro secrets ls --project-wide` read to decide which *logical secret*
# of the project's secret set a container holds (internal/infra/discover.go's
# liveProjectSecrets, internal/cli/doctor.go), and this container is not one of
# those — it is one document with one writer. Carrying it would make discovery
# union the container into repo.project_secrets beside this resource, and the
# apply would then plan the same secret ID twice. The two ownership marks are
# what make it recognizably this installation's.
#
# prevent_destroy is secrets.tf's rule for the same reason: a config edit that
# drops the container must be a hard plan failure, not a deletion of every
# credential in it.
resource "google_secret_manager_secret" "credentials_bundle" {
  count = local.credentials_service_on ? 1 : 0

  project   = var.project
  secret_id = var.credentials_service.bundle_secret
  labels = {
    fugaro         = "managed"
    fugaro_project = var.fugaro_project
  }

  replication {
    auto {}
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}

# The ONE grant (Q6): roles/secretmanager.secretAccessor, to the service's own
# account, on that one container. No condition block at all — a condition here
# would be the name-prefix fence the ruling replaced — and no second member
# anywhere. The member is the account this module creates, which cover.go's
# grant rules already bound.
resource "google_secret_manager_secret_iam_member" "credentials_bundle" {
  count = local.credentials_service_on ? 1 : 0

  project   = var.project
  secret_id = google_secret_manager_secret.credentials_bundle[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.credentials[0].member
}

# The service itself. It requires IAM: nothing here grants roles/run.invoker to
# allUsers or allAuthenticatedUsers, and invoker_iam_disabled is set to false
# explicitly rather than left to the provider's default, so the front end
# refuses an unauthenticated request before the container runs and a later
# change of that default cannot open the service silently. The ingress is ALL
# because the caller is a Cloud Run *job* of this project, which reaches the
# service over its public run.app URL (a job is not on a VPC network); the
# fence is the IAM check, not the network — the only member is each job
# account's own invoker grant (modules/workflow/sa.tf).
#
# The image is a variable (C1.20's init stage fills it with the released
# image's reference, as it fills var.history.image), and no command or args are
# set: the image's own entrypoint is the service (C1.14). The timeout is the
# longest Cloud Run allows, because a proxied streaming call is long-lived
# (Check 91 measures what a Cloud Run front end does with one).
resource "google_cloud_run_v2_service" "credentials" {
  count = local.credentials_service_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.credentials_service.service
  labels = {
    fugaro      = "managed"
    fugaro_role = "credentials"
  }
  ingress = "INGRESS_TRAFFIC_ALL"

  # IAM is required, stated rather than implied: this is the fence every
  # caller passes through, and the service's own ID-token check (Q6) is
  # layered on top of it, never instead of it.
  invoker_iam_disabled = false

  # The service holds no data: the one thing worth keeping is the bundle
  # container, and that one carries prevent_destroy above. Turning the opt-in
  # off is therefore a removal a plan may make, not a refusal.
  deletion_protection = false

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "credentials"
    }
    service_account = google_service_account.credentials[0].email
    timeout         = "3600s"

    containers {
      image = var.credentials_service.image

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # The service's identity, and no credential: it reads the one bundle
      # secret as its own account, by the name it derives from FUGARO_PROJECT
      # (the project-wide container of the logical name "creds-bundle",
      # internal/backend/gcp's ProjectSecretID — the same value
      # var.credentials_service.bundle_secret carries, which is what the
      # container above is created with), so no secret ID and no value is ever
      # an environment variable here.
      env {
        name  = "FUGARO_PROJECT"
        value = var.fugaro_project
      }
      env {
        name  = "FUGARO_GCP_PROJECT"
        value = var.project
      }
      env {
        name  = "FUGARO_REGION"
        value = var.region
      }
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.credentials_bundle]
}
