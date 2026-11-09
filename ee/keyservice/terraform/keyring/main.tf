# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The key-ring root: the dedicated project's KMS key ring and the two signing
# keys the service mints with (S3.5, docs/design/key-signing-service.md §2-§3).
#
# It is a root of its own, applied before the services one, because the keys
# outlive the services: S3.8 applies this root on the project the owner created
# and publishes both public halves, which is what hand minting (S2.10) stands on
# while the entity does not exist, and services/ waits for S3.7. Two states also
# mean an apply of the services can never destroy a signing key: destroy one and
# every key it signed stops verifying in every install holding its public half,
# with no revocation to notice it by.
#
# Nothing here is reachable from a `fugaro` command: deploy/terraform is
# embedded in the binary and this tree is not (§3.4), so an installation can
# neither plan nor apply it.

terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
  }

  # The four roots under deploy/terraform state in a GCS bucket and this one
  # does too, in the owner's own: `terraform init -backend-config=bucket=<name>`
  # names it. The keys are the one thing in this service that cannot be
  # re-created, so their state does not belong on one machine.
  backend "gcs" {}
}

# The project is always explicit and API quota is billed to it, as in the
# installation roots.
provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

# Enabling an API is free and using it may be billed; nothing here disables one
# (deploy/terraform's apis.tf rule). The project holds this service and nothing
# else, so this root manages the APIs it needs rather than leaving the runbook's
# reader a `gcloud services enable` to remember.
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

# One key ring for both keys: production and staging are two key ids in one ring
# (D459(a)), so one grant on the ring reaches exactly the two of them.
resource "google_kms_key_ring" "signing" {
  project  = var.project
  name     = var.keyring
  location = var.region

  depends_on = [google_project_service.this]
}

# The production and staging signing keys. ASYMMETRIC_SIGN with EC_SIGN_ED25519
# because that is the algorithm the three verifiers verify (S2.1): the private
# half is created inside the key store, is signed with through the API, and is
# read out by nothing — not by the service, not by the owner, not by an agent.
# Ed25519 exists at the SOFTWARE protection level only, so the level is stated
# here rather than left to a default that would refuse the algorithm; this is
# what closes UNVERIFIED U9, and a store that offered no Ed25519 would have been
# this slice's red step to report rather than a file to keep.
resource "google_kms_crypto_key" "signing" {
  for_each = var.keys

  key_ring = google_kms_key_ring.signing.id
  name     = each.value

  purpose = "ASYMMETRIC_SIGN"

  version_template {
    algorithm        = "EC_SIGN_ED25519"
    protection_level = "SOFTWARE"
  }

  # A signature's private half cannot be re-created and a destroy takes the key
  # id out of the ring: rotation is a new id published in all three verifiers
  # (§11), never an apply's side effect. Removing a key is therefore a deliberate
  # edit of this file, which is also the moment the previous id's last key has to
  # have passed its grace.
  lifecycle {
    prevent_destroy = true
  }
}

# The signer role, and the whole of what signing takes: one permission, granted
# at the key ring to the minter's account alone (services/main.tf, Check 174). A
# predefined role would carry more than signing — cloudkms.signerVerifier also
# reads the public key and verifies — and §3.3's rule is the narrowest grant
# that mints.
resource "google_project_iam_custom_role" "signer" {
  project     = var.project
  role_id     = var.signer_role
  title       = "Fugaro key signer"
  description = "Signs licence keys through the key ring's KMS keys. The minter's service account is its only holder (S3.5, Check 174)."
  permissions = ["cloudkms.cryptoKeyVersions.useToSign"]

  depends_on = [google_project_service.this]
}
