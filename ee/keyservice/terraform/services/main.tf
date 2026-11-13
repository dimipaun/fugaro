# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The services root: the portal's two Cloud Run services and everything one
# project holds for them (D2194, S3.5, docs/design/key-signing-service.md §3). The
# API is the internet-facing half — the SPA's calls, the merchant's events, the
# scheduler's monthly and daily runs — and the minter is the one service that
# signs: only the API's account may call it, and the call carries an ID token
# Cloud Run verifies (D450(a)).
#
# The identities are the point rather than the services: the API's account holds
# nothing that signs, so the internet-facing process cannot sign even if its code
# is fully in an attacker's hands, and the minter's account holds the signer role
# on the key ring and nothing else — no store, no mail path, no merchant secret
# (§3.3, Check 571). What the API's account holds is one list: the portal's own
# documents, the authenticated call to the minter, and the handover ring it seals
# and opens a reveal with (D2182). Everything else either process holds is a
# secret this root creates an empty container for and the deploy (P9.2, S3.7)
# fills.
#
# Every one of these lives in the signer's own project (D2194): the fence between
# two projects is gone, so what stands between the internet and a signature is the
# IAM check on the minter, its internal ingress, and the one permission the signer
# role carries. Check 571 is the list a person reads to see that, and the tests in
# ../infra_test.go are the machine's half of it.
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
    "firestore.googleapis.com",
    "secretmanager.googleapis.com",
    "cloudscheduler.googleapis.com",
    "cloudkms.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "iam.googleapis.com",
  ]) : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}

# The environment's key ring, the one keyring/'s apply of the same environment
# created: the name is derived from the same two variables there and here, so an
# apply of this root against the other environment's ring is a plan that fails
# rather than a grant on a ring nobody reviewed.
data "google_kms_key_ring" "signing" {
  project  = var.project
  name     = "${var.keyring}-${var.environment}"
  location = var.region
}

# The portal's one document store: its own native Firestore database, separate
# from the installation's realtime database (spec §4, D2188). The rules it is
# deployed with deny every client and live beside the SPA
# (ee/portal/firestore.rules), so the documents are reachable through the API's
# account and through nothing else.
resource "google_firestore_database" "portal" {
  project     = var.project
  name        = "portal"
  location_id = var.region
  type        = "FIRESTORE_NATIVE"

  depends_on = [google_project_service.this]
}

# The secrets' empty containers, and nothing more: the deploy (P9.2, S3.7) adds
# the versions, because a value that reaches this repository reaches every clone of
# it. Creating the containers here is what makes the deployment a step that fills
# named holes rather than one that invents names, and an empty container is a
# finding a person can see on a plan.
resource "google_secret_manager_secret" "this" {
  for_each = var.secrets

  project   = var.project
  secret_id = each.value

  replication {
    auto {}
  }

  labels = {
    fugaro = "portal"
  }

  depends_on = [google_project_service.this]
}

resource "google_service_account" "api" {
  project      = var.project
  account_id   = var.api_account
  display_name = "Fugaro portal API"

  description = "The portal's API: the SPA's calls, the merchant's events and the scheduled runs. Holds nothing that signs, and reaches the signer only through the minter (Check 571)."

  depends_on = [google_project_service.this]
}

resource "google_service_account" "minter" {
  project      = var.project
  account_id   = var.minter_account
  display_name = "Fugaro key minter"

  description = "The one identity that signs: the signer role on the key ring, and read access to nothing an applicant typed."

  depends_on = [google_project_service.this]
}

# The API is reachable by everyone, because that is who calls it: the SPA in a
# person's browser, the merchant's webhook, the scheduler. IAM is required and
# stated rather than left to the provider's default, so a later change of that
# default cannot open or close the service silently; the public binding is beside
# it, where a plan's review screen finds it.
resource "google_cloud_run_v2_service" "api" {
  project  = var.project
  location = var.region
  name     = var.api_service
  ingress  = "INGRESS_TRAFFIC_ALL"

  invoker_iam_disabled = false
  deletion_protection  = false

  template {
    service_account = google_service_account.api.email

    # Scale to zero: a public endpoint nobody is calling costs nothing, and a
    # cold start is what a caller waits for.
    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.api_image

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

# The minter: no public invoker, and its ingress internal. The fence is the IAM
# check the platform runs before the container, and the ingress is what keeps the
# call the grant allows the only one the network delivers — an internal-only
# service accepts traffic from its own project, which is where D2194 puts the API.
resource "google_cloud_run_v2_service" "minter" {
  project  = var.project
  location = var.region
  name     = var.minter_service
  ingress  = "INGRESS_TRAFFIC_INTERNAL_ONLY"

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

# The API alone is public: roles/run.invoker to allUsers, on the API and on
# nothing else in this file.
resource "google_cloud_run_v2_service_iam_member" "api_public" {
  project  = var.project
  location = google_cloud_run_v2_service.api.location
  name     = google_cloud_run_v2_service.api.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# Only the API's account may call the minter, which is what makes the minter
# non-public without a second front door: the ID token the call carries is minted
# for this account and the platform checks it before the container runs.
resource "google_cloud_run_v2_service_iam_member" "api_invokes_minter" {
  project  = var.project
  location = google_cloud_run_v2_service.minter.location
  name     = google_cloud_run_v2_service.minter.name
  role     = "roles/run.invoker"
  member   = google_service_account.api.member
}

# The signing grant, and the whole of it: the signer role, at this environment's
# key ring (keyring/ creates it), to one account. Any second member here is a
# second identity that can mint a licence key, and Check 571 measures that there
# is not one.
resource "google_kms_key_ring_iam_member" "minter_signs" {
  key_ring_id = data.google_kms_key_ring.signing.id
  role        = "projects/${var.project}/roles/${var.signer_role}"
  member      = google_service_account.minter.member
}

# The document store, and the one identity that reaches it: every organisation's
# records, billing and reveal bundles are written by the API through its own
# account, and the minter — which holds the signer role and nothing else — reads
# none of it (§3.3, Check 571). The SPA reads the same documents through the API
# and never through the client SDK (D2188).
resource "google_project_iam_member" "api_datastore" {
  project = var.project
  role    = "roles/datastore.user"
  member  = google_service_account.api.member
}

# The reveal handover's key: the API seals the bundle it stores and opens it once,
# 24 hours later, and nothing else in this project may use the key (D2182,
# Check 571). ENCRYPT_DECRYPT with the symmetric algorithm, because this key seals
# and opens and produces no signature at all — the signing is the minter's, and the
# signer role, granted at a RING, reaches no key in this one.
#
# The ring is its own rather than the licence ring's, for that same reason: a ring
# holding both an encryption key and a signing key is a ring where the one grant
# that mints reaches the sealer too. Ring and key share `var.handover_key`, so the
# key id the deploy hands the API is this ring's path plus that name.
resource "google_kms_key_ring" "handover" {
  project  = var.project
  name     = var.handover_key
  location = var.region

  depends_on = [google_project_service.this]
}

resource "google_kms_crypto_key" "handover" {
  key_ring = google_kms_key_ring.handover.id
  name     = var.handover_key

  purpose = "ENCRYPT_DECRYPT"

  version_template {
    algorithm = "GOOGLE_SYMMETRIC_ENCRYPTION"
  }
}

# The sealer role, and the whole of what sealing takes: the API encrypts a
# handover and decrypts it once, and signs nothing anywhere (D2182).
resource "google_project_iam_custom_role" "handover" {
  project     = var.project
  role_id     = var.handover_role
  title       = "Fugaro handover sealer"
  description = "Encrypts and decrypts a reveal handover. The API's service account is its only holder (D2182, Check 571)."
  permissions = ["cloudkms.cryptoKeyVersions.useToEncrypt", "cloudkms.cryptoKeyVersions.useToDecrypt"]

  depends_on = [google_project_service.this]
}

# The API's grant on the handover ring, and the whole of it: one member. A second
# member here is a second identity that can open every sealed reveal in the store,
# which is the one thing the encryption is for.
resource "google_kms_key_ring_iam_member" "api_seals" {
  key_ring_id = google_kms_key_ring.handover.id
  role        = "projects/${var.project}/roles/${var.handover_role}"
  member      = google_service_account.api.member
}
