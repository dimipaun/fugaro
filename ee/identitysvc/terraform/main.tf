# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The identity project (D2781): the assertion signer as a Cloud Run service, the
# bucket the owner publishes the root-signed key set from and the service reads it
# out of, and the alert that says when the newest set is close to expiring (O7,
# O18). The key ring and the signer role are keyring.tf's, and the Firebase Auth
# side is auth.tf's.
#
# Nothing here is reachable from a `fugaro` command: deploy/terraform is embedded
# in the binary and this tree is not, so an installation can neither plan nor
# apply it. The owner applies it — Check 730 — and the key ceremony that follows
# is theirs alone (.claude/rules/ee.md, `.claude/rules/terraform.md`).

terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
    # google_firebase_project, which links the project the owner created to
    # Firebase, exists only in the beta provider (deploy/terraform/gcp/modules/
    # firebase uses the same pair).
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "= 8.4.0"
    }
  }

  # The state goes in the owner's own bucket, named on the command line:
  # `terraform init -backend-config=bucket=<name>`. The key ring cannot be
  # re-created, so its state does not belong on one machine.
  backend "gcs" {}
}

provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

provider "google-beta" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

# Enabling an API is free and using it may be billed; nothing here disables one.
# The project is the owner's and holds nothing else, so this root manages the APIs
# it needs rather than leaving the runbook's reader a `gcloud services enable` to
# remember.
resource "google_project_service" "this" {
  for_each = var.manage_apis ? toset([
    "cloudkms.googleapis.com",
    "run.googleapis.com",
    "storage.googleapis.com",
    "monitoring.googleapis.com",
    "firebase.googleapis.com",
    "identitytoolkit.googleapis.com",
    # Realtime Database is a product of its own: linking the project to
    # Firebase (auth.tf) does not turn this API on, and the paired list's
    # instance (pairedlist.tf) is the first thing in this project to need it.
    "firebasedatabase.googleapis.com",
    "iam.googleapis.com",
    "cloudresourcemanager.googleapis.com",
  ]) : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}

# The identity the service runs as, and the only holder of the signer role
# (keyring.tf): a Cloud Run service runs as its template's account, so a service
# that ran as another identity would be that identity, and the role would reach
# wherever its code runs.
resource "google_service_account" "signer" {
  project      = var.project
  account_id   = var.signer_account
  display_name = "Fugaro identity assertion signer"

  depends_on = [google_project_service.this]
}

# The signer role, granted at the ring and to this account alone. A grant made on
# the key rather than the ring would be the same reach today and a different one
# the moment a second key exists, and a grant made at the project would reach
# every key the project ever holds.
resource "google_kms_key_ring_iam_member" "signer_signs" {
  key_ring_id = google_kms_key_ring.signing.id
  role        = google_project_iam_custom_role.signer.id
  member      = google_service_account.signer.member
}

# Where the owner publishes the root-signed key set (A4) and A2 serves it from.
# The name is derived from the project id because a bucket name is globally
# unique, and a literal here would collide with whoever else chose it.
#
# The bucket is uniform-access and never public: the set it carries is public
# material, but the object list, the versions and the IAM of a public bucket are
# not, and a document that is fetched by URL is served by the service rather than
# read out of the bucket by a stranger.
resource "google_storage_bucket" "keyset" {
  project  = var.project
  name     = "${var.project}-keyset"
  location = var.region

  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  # A release pins its `seq` floor against one document (P8), so the version that
  # was signed is worth being able to see and put back after a bad write.
  versioning {
    enabled = true
  }

  # The bucket holds the key set every install fetches keys from, and emptying it
  # is a step a person takes on purpose rather than an apply's side effect.
  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}

# The service's read, and the bucket's only grant. The owner writes the signed
# document with their own credentials — a project-level role this root does not
# write — so there is no writer here, and the service writes nothing at all:
# objectViewer is the read and no write, made on the bucket the resource above
# creates.
resource "google_storage_bucket_iam_member" "signer_reads" {
  bucket = google_storage_bucket.keyset.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.signer.member
}

# The signer itself. Its whole configuration is the five values below: the key
# version it signs with, the bucket and object the signed set is read from, the
# path it serves the JWKS on, and the window inside which it warns that the set is
# about to expire. The program reads every one of them before it serves, so a
# revision missing one refuses at startup rather than signing with a default
# nobody chose.
#
# No scaling block: the signer holds no state — Cloud KMS signs and the bucket is
# read — so the platform's own scaling is the right one and nothing here counts in
# process memory.
resource "google_cloud_run_v2_service" "signer" {
  project  = var.project
  name     = var.service
  location = var.region

  # The app is what asks for an assertion (spec step 3), so the service is reached
  # over the internet; `invoker_iam_disabled` stays false and the code verifies the
  # caller's Firebase ID token (A1). A phone cannot be handed an IAM principal, so
  # the invoker grant below is allUsers and the ID token is the control.
  ingress              = "INGRESS_TRAFFIC_ALL"
  invoker_iam_disabled = false
  deletion_protection  = false

  template {
    service_account = google_service_account.signer.email

    containers {
      image = var.image

      # The key resource's own name, so that the revision waits for the key to
      # exist and the version's name is written once. Cloud KMS spells the
      # segment `cryptoKeyVersions`, and a path assembled from the variables is
      # a second spelling of the key's name and no edge to the key itself.
      env {
        name  = "FUGARO_SIGNING_KEY"
        value = "${google_kms_crypto_key.signing.id}/cryptoKeyVersions/1"
      }
      env {
        name  = "FUGARO_KEYSET_BUCKET"
        value = google_storage_bucket.keyset.name
      }
      env {
        name  = "FUGARO_KEYSET_OBJECT"
        value = var.keyset_object
      }
      env {
        name  = "FUGARO_JWKS_PATH"
        value = var.jwks_path
      }
      env {
        name  = "FUGARO_KEYSET_ALERT_DAYS"
        value = var.keyset_alert_days
      }
    }
  }

  depends_on = [
    google_kms_key_ring_iam_member.signer_signs,
    google_storage_bucket_iam_member.signer_reads,
  ]
}

resource "google_cloud_run_v2_service_iam_member" "signer_public" {
  project  = var.project
  location = google_cloud_run_v2_service.signer.location
  name     = google_cloud_run_v2_service.signer.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# The project's alert address. A project that names none gets no channel and no
# policy, as the licence service's alert does.
resource "google_monitoring_notification_channel" "email" {
  count = var.alert_email == null ? 0 : 1

  project      = var.project
  display_name = "Fugaro identity alerts"
  type         = "email"

  labels = {
    email_address = var.alert_email
  }
}

# A signed key set is valid 180 days (O18) and a pinned key ends with the set that
# omits it, so a set that lapses while nobody is looking is every project refusing
# new pairings and restores. Terraform cannot know when the owner signed the set,
# so the signer writes one warning line when the newest one is inside
# FUGARO_KEYSET_ALERT_DAYS of its `exp`, and this policy is what makes that line
# reach a person (O7, Check 737).
resource "google_monitoring_alert_policy" "keyset_expiry" {
  count = var.alert_email == null ? 0 : 1

  project      = var.project
  display_name = "Fugaro identity key set expiring"
  combiner     = "OR"

  conditions {
    display_name = "The newest signed key set is inside its warning window"
    condition_matched_log {
      filter = "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${var.service}\" AND jsonPayload.event=\"keyset_expiring\" AND severity>=WARNING"
    }
  }

  notification_channels = [google_monitoring_notification_channel.email[0].id]

  # Log-match conditions require a notification rate limit.
  alert_strategy {
    notification_rate_limit {
      period = "3600s"
    }
    auto_close = "604800s"
  }
}

variable "project" {
  description = "The identity project's id, which the owner creates before this root is applied (Check 730). This root never creates a project and links billing for none."
  type        = string
}

variable "region" {
  description = "The region the key ring, the bucket and the service are created in. A key ring cannot move after creation, so this is the keys' home rather than wherever the service is cheapest to run."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
  }
}

variable "image" {
  description = "The signer's container image, built and pushed by the release that ships A1 and A2. It is an input because an apply rewrites the revision's image and a value written here would be a second place the release's tag lives."
  type        = string
}

variable "keyring" {
  description = "The name of the key ring the assertion signing key is created in."
  type        = string
  default     = "fugaro-identity"
}

variable "key" {
  description = "The name of the signing key inside the ring. The signer signs with version 1 of it, and P8 pins its public half."
  type        = string
  default     = "fugaro-identity-assertion"
}

variable "signer_role" {
  description = "The custom role the signer's account is granted on the key ring."
  type        = string
  default     = "fugaroIdentitySigner"
}

variable "signer_account" {
  description = "The account id of the service account the signer service runs as."
  type        = string
  default     = "fugaro-identity-signer"
}

variable "service" {
  description = "The Cloud Run service's name, which the expiry alert's filter matches by."
  type        = string
  default     = "fugaro-identity"
}

variable "keyset_object" {
  description = "The object the owner writes the root-signed key set to and the service serves it from (A4, A2)."
  type        = string
  default     = "keyset.json"
}

variable "jwks_path" {
  description = "The path the service serves the JWKS and the signed key set on. The URL the release pins is the service's own URI with this path appended (P8)."
  type        = string
  default     = "/keys"
}

variable "keyset_alert_days" {
  description = "How many days before the newest signed key set expires the signer warns. A signed set is valid 180 days (O18) and the owner runs the signing command inside this window (O7)."
  type        = number
  default     = 30
}

variable "alert_email" {
  description = "The address the expiry alert is sent to. Null creates no channel and no policy, so a project that names no address is a project with no alert rather than one that notifies nobody."
  type        = string
  default     = null
}

variable "manage_apis" {
  description = "Whether this root enables the APIs it needs. Off for a project whose owner enables them out of band."
  type        = bool
  default     = true
}

# The JWKS URL, and the whole of what P8 pins: the issuer, the URL, the root public
# key and the signing keys of the release. The URL is the service's assigned URI
# rather than a form written here, because Cloud Run answers with its own
# `<service>-<hash>-<code>` host and a hand-written one is a URL nobody applied.
output "jwks_url" {
  description = "The URL the assertion verifier fetches the root-signed key set and the JWKS from (P1, P8)."
  value       = "${google_cloud_run_v2_service.signer.uri}${var.jwks_path}"
}
