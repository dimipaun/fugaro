# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The root key project: the key ring and the one Ed25519 root key that signs the
# served key set (mobile identity plan task A4; the spec's "Assertion signing
# keys"; D2792 as amended by O7 and O15; Check 737).
#
# **A project of its own, and the owner's alone.** This root is not A3's identity
# root and shares nothing with it: the identity project runs a service and holds a
# key its own service account signs assertions with, while this project holds one
# key that no service and no `fugaro` command can reach. Splitting them is the
# point of O7 — the key that says *which* signing keys are real must not live
# where a deploy, a role or a compromised service account can reach a signing
# call. The owner creates the project (Check 737) and applies this root by hand:
# it is not embedded in the binary, so no installation can plan or apply it.
#
# **The private half is generated inside Cloud KMS and read out by nothing.** The
# key is ASYMMETRIC_SIGN with EC_SIGN_ED25519, created by the key store's own API:
# there is no import job here and therefore no file, fixture or machine that ever
# carried the private half, and Cloud KMS offers no export of one. What leaves
# this project is the public half, read by the signing command to be pinned into a
# build (P8) and served at the keys URL (.claude/rules/ee.md R5).
#
# **Signing is granted to one member and to no service account.** The grant is on
# the key and not on the ring or the project — a ring-level grant reaches every key
# the ring gains, and a project-level one reaches every key the project ever holds
# — and the custom role is the two permissions the ceremony needs: sign, and read
# the public half to print and pin. `var.owner_member` is the separate owner
# identity O15 asks for, it has no default and its validation refuses a service
# account, so "no service account can sign" is a rule the apply enforces rather
# than a sentence this comment makes. The owner's everyday login is deliberately
# not it: Check 737's reading is that the laptop's default login is refused.
#
# **Rotation is a new key version, and a deliberate one.** Every version of the
# key inherits the key's IAM, so a rotation is `cryptoKeyVersions.create` plus one
# signed set naming the new version (`-key-version`), with the version the build
# pinned still verifying the sets signed under it. There is no `rotation_period`:
# an automatic rotation would add a version nobody published, and the ceremony
# exists to make the new version and its published set one deliberate act (O7,
# O18).

terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
  }

  # The state goes in the owner's own bucket, named on the command line:
  # `terraform init -backend-config=bucket=<name>`. A key ring cannot be
  # re-created and the root key's versions are what every build's pinned public
  # half verifies against, so its state does not belong on one machine.
  backend "gcs" {}
}

provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

# Enabling an API is free and using it may be billed; nothing here disables one.
# The project is the owner's and holds nothing else, so this root manages the APIs
# it needs rather than leaving the runbook's reader a `gcloud services enable` to
# remember. There is no run, storage, Firebase or database API: this project runs
# nothing and serves nothing.
resource "google_project_service" "this" {
  for_each = var.manage_apis ? toset([
    "cloudkms.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "iam.googleapis.com",
  ]) : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}

resource "google_kms_key_ring" "root" {
  project  = var.project
  name     = var.keyring
  location = var.region

  depends_on = [google_project_service.this]
}

# The one root key. What the tree cannot assert is that the API offers this pair:
# the provider types `version_template.algorithm` as a plain string, so
# `terraform validate` accepts any value and Check 737's apply against the real
# API is the measurement. SOFTWARE and not HSM because Ed25519 is offered at
# SOFTWARE, and the level is stated rather than left to a default that would
# refuse the algorithm.
resource "google_kms_crypto_key" "root" {
  key_ring = google_kms_key_ring.root.id
  name     = var.key

  purpose = "ASYMMETRIC_SIGN"

  version_template {
    algorithm        = "EC_SIGN_ED25519"
    protection_level = "SOFTWARE"
  }

  # A destroy takes the key id out of the ring, and every published key set and
  # every build's pinned root public key is verified against a version of this
  # key: retiring a root is a new key and a re-pin, never an apply's side effect.
  lifecycle {
    prevent_destroy = true
  }
}

# The whole of what the signing ceremony may do with the key: sign, and read the
# public half to print it and pin it. The owner administers the project with their
# own roles and does not need them here, and a predefined role would be wider —
# signerVerifier also verifies, and owner reaches the ring itself, which is how a
# default login would sign.
resource "google_project_iam_custom_role" "root_signer" {
  project     = var.project
  role_id     = var.signer_role
  title       = "Fugaro identity root key signer"
  description = "Signs the served key set with the root key project's root key and reads its public half to pin. The separate owner identity is its only holder (A4, O7, O15, Check 737)."
  permissions = ["cloudkms.cryptoKeyVersions.useToSign", "cloudkms.cryptoKeyVersions.viewPublicKey"]

  depends_on = [google_project_service.this]
}

# The signing grant, made on the key and held by exactly one member. This is the
# resource a *second* member would be added to by mistake, and Check 737's restore
# paragraph is about it: the reading's own criterion is that one member can sign.
resource "google_kms_crypto_key_iam_member" "owner_signs" {
  crypto_key_id = google_kms_crypto_key.root.id
  role          = google_project_iam_custom_role.root_signer.id
  member        = var.owner_member
}

variable "project" {
  description = "The root key project's id, which the owner creates before this root is applied (Check 737). It is a project of its own, holding no service, and this root creates no project and links billing for none."
  type        = string
}

variable "region" {
  description = "The region the key ring is created in. A key ring cannot move after creation and a signature is made where the key is, so this is the key's home rather than wherever a caller runs."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as europe-west1."
  }
}

variable "owner_member" {
  description = "The separate owner identity that may sign with the root key (O15), in Cloud IAM's member form: `user:owner@example.com` or `group:owners@example.com`. It is the owner's own identity and not the login their laptop defaults to, and it is not a service account."
  type        = string

  # No default, and the reason is the finding Check 737 is built around: a
  # project-level default would be a signing grant made by an apply nobody
  # thought about, and an empty one would grant signing to nobody while looking
  # applied. A service account here would be a robot that can sign a key set.
  validation {
    condition     = can(regex("^(user|group):", var.owner_member))
    error_message = "owner_member must be a user: or group: member, and never a serviceAccount, a domain, or a federated principal: signing the root key is a person's act at their own terminal (O15)."
  }
}

variable "keyring" {
  description = "The name of the key ring the root key is created in."
  type        = string
  default     = "fugaro-root"
}

variable "key" {
  description = "The name of the root key inside the ring. Versions of it are what sign, and P8 pins version 1's public half."
  type        = string
  default     = "fugaro-identity-root"
}

variable "signer_role" {
  description = "The custom role the owner identity is granted on the root key."
  type        = string
  default     = "fugaroRootKeySigner"
}

variable "manage_apis" {
  description = "Whether this root enables the APIs it needs. Off for a project whose owner enables them out of band."
  type        = bool
  default     = true
}

output "key_version" {
  description = "The root key version the signing command is run with (`-key-version`) until the first deliberate rotation, which is a new version of the same key plus one signed set naming it (O7)."
  value       = "${google_kms_crypto_key.root.id}/cryptoVersions/1"
}

output "root_key" {
  description = "The root key's resource id, which Check 737's read of whether the laptop's default login can sign with it is run against."
  value       = google_kms_crypto_key.root.id
}
