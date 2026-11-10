# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The services root: the two Cloud Run services T13's split is (S3.5,
# docs/design/key-signing-service.md §3). The intake is the internet-facing
# half — the forms, the merchant's events and the admin page — and the minter is
# the one service that signs, with no public invoker: only the intake's account
# may call it, and the call carries an ID token Cloud Run verifies (D450(a)).
#
# The identities are the point rather than the services: the intake's account
# holds no KMS role at all, so the internet-facing process cannot sign even if
# its code is fully in an attacker's hands, and the minter's account holds the
# signer role on the key ring and nothing else — no store, no mail path, no
# merchant secret (§3.3). Everything else either process holds is a secret the
# deploy (S3.7) mounts, and none of it is created here.
#
# Applied after keyring/ and only once the entity exists (S3.7): the design's
# step 3. The key ring is read through a data source, so this root fails to plan
# while S3.8's apply has not run — the order the runbook puts the two in.

terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
  }

  backend "gcs" {}
}

provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

resource "google_project_service" "this" {
  for_each = var.manage_apis ? toset([
    "run.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "iam.googleapis.com",
  ]) : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}

data "google_kms_key_ring" "signing" {
  project  = var.project
  name     = var.keyring
  location = var.region
}

resource "google_service_account" "intake" {
  project      = var.project
  account_id   = var.intake_account
  display_name = "Fugaro key intake"

  description = "The internet-facing intake: the forms, the merchant's events and the admin page. Holds no KMS role and reaches the minter only through it (Check 174)."

  depends_on = [google_project_service.this]
}

resource "google_service_account" "minter" {
  project      = var.project
  account_id   = var.minter_account
  display_name = "Fugaro key minter"

  description = "The one identity that signs: the signer role on the key ring, and read access to nothing an applicant typed."

  depends_on = [google_project_service.this]
}

# The intake is reachable by everyone, because that is who calls it: a person's
# browser posts a form, the merchant posts an event, the owner opens the admin
# page. IAM is required and stated rather than left to the provider's default,
# so a later change of that default cannot open or close the service silently;
# the public binding is beside it, where a plan's review screen finds it.
resource "google_cloud_run_v2_service" "intake" {
  project  = var.project
  location = var.region
  name     = var.intake_service
  ingress  = "INGRESS_TRAFFIC_ALL"

  invoker_iam_disabled = false
  deletion_protection  = false

  template {
    service_account = google_service_account.intake.email

    # Scale to zero: a public endpoint nobody is calling costs nothing, and a
    # cold start is what a caller waits for.
    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.intake_image

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
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
}

# The minter: no public invoker, and no allUsers binding anywhere in this file's
# grants. Its ingress is stated as ALL for the same reason the intake's is — the
# fence is the IAM check the platform runs before the container, and a
# service-to-service call over a run.app URL leaves the network an
# internal-only ingress would be testing.
resource "google_cloud_run_v2_service" "minter" {
  project  = var.project
  location = var.region
  name     = var.minter_service
  ingress  = "INGRESS_TRAFFIC_ALL"

  invoker_iam_disabled = false
  deletion_protection  = false

  template {
    service_account = google_service_account.minter.email

    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.minter_image

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
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
}

# The intake alone is public: roles/run.invoker to allUsers, on the intake and
# on nothing else in this file.
resource "google_cloud_run_v2_service_iam_member" "intake_public" {
  project  = var.project
  location = google_cloud_run_v2_service.intake.location
  name     = google_cloud_run_v2_service.intake.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# Only the intake's account may call the minter, which is what makes the minter
# non-public without a second front door: the ID token the call carries is
# minted for this account and the platform checks it before the container runs.
resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter" {
  project  = var.project
  location = google_cloud_run_v2_service.minter.location
  name     = google_cloud_run_v2_service.minter.name
  role     = "roles/run.invoker"
  member   = google_service_account.intake.member
}

# The signing grant, and the whole of it: the signer role, at the key ring
# (keyring/main.tf creates it), to one account. Any second member here is a
# second identity that can mint a licence key, and Check 174 measures that there
# is not one.
resource "google_kms_key_ring_iam_member" "minter_signs" {
  key_ring_id = data.google_kms_key_ring.signing.id
  role        = "projects/${var.project}/roles/${var.signer_role}"
  member      = google_service_account.minter.member
}
