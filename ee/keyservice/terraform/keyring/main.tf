# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The key-ring root: the KMS key ring of the environment this apply carries, and
# the one signing key the service mints with (S3.5,
# docs/design/key-signing-service.md §2-§3, D459 decided by D2194).
#
# It is a root of its own, applied before the services one, because the keys
# outlive the services: S3.8 applies this root on the project the owner created
# and publishes the public half, which is what hand minting (S2.10) stands on
# while the entity does not exist, and services/ waits for S3.7. Two states also
# mean an apply of the services can never destroy a signing key: destroy one and
# every key it signed stops verifying in every install holding its public half,
# with no revocation to notice it by.
#
# ONE ENVIRONMENT PER APPLY, and one key id per ring. The environment is a
# required input and it decides both the ring's name and the key created inside
# it, so production's ring holds the production key and staging's the staging key:
# the signer role services/ grants at the ring then reaches exactly the one, and a
# key id is never created in the other environment's ring. The ring's name carries
# the environment because a plan's review screen is where a person tells the two
# applies apart, and one ring name in both is a ring an apply silently adds a
# second key to.
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
# (deploy/terraform's apis.tf rule). The project holds the signer and the portal
# (D2194), so this root manages the APIs it needs rather than leaving the
# runbook's reader a `gcloud services enable` to remember.
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

# The environment's key ring: `var.keyring` is the name stem both roots hold and
# `var.environment` is what this apply is for, so one ring holds one environment's
# key. The name is derived rather than written because a ring cannot be renamed
# after creation: a literal here would be a second place the environments are
# listed, beside the map the key ids come from.
resource "google_kms_key_ring" "signing" {
  project  = var.project
  name     = "${var.keyring}-${var.environment}"
  location = var.region

  depends_on = [google_project_service.this]
}

# The environment's signing key, and the only key in this ring. ASYMMETRIC_SIGN
# with EC_SIGN_ED25519 because that is the algorithm the three verifiers verify
# (S2.1): the private half is created inside the key store, is signed with through
# the API, and is read out by nothing — not by the service, not by the owner, not
# by an agent. Ed25519 is offered at SOFTWARE and not at HSM, so the level is
# stated rather than left to a default that would refuse the algorithm.
#
# The id is `keys`' entry for this apply's environment, so the names the three
# verifiers publish are that map's values and the key is created once. There is no
# for_each: a block ranging over the map would put both environments' keys in one
# ring, which is the state D459 decides against, and the signer role — granted at
# the ring — could not tell the two apart.
#
# Whether Cloud KMS offers that pair is not something this tree can assert:
# hashicorp/google types version_template.algorithm as a plain string, so
# `terraform validate` accepts any value and the apply against the real API is
# the measurement. The provider's own algorithm list carries EC_SIGN_ED25519 at
# the SOFTWARE protection level of elliptic-curve signing, which is the reading
# U9 waits on rather than a schema's word; the row that records it belongs to
# the plan's reconciliation, not to this root.
resource "google_kms_crypto_key" "signing" {
  key_ring = google_kms_key_ring.signing.id
  name     = var.keys[var.environment]

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
# at this environment's key ring to the minter's account alone (services/main.tf,
# Check 174). A predefined role would carry more than signing —
# cloudkms.signerVerifier also reads the public key and verifies — and §3.3's rule
# is the narrowest grant that mints.
resource "google_project_iam_custom_role" "signer" {
  project     = var.project
  role_id     = var.signer_role
  title       = "Fugaro key signer"
  description = "Signs licence keys through the key ring's KMS keys. The minter's service account is its only holder (S3.5, Check 174)."
  permissions = ["cloudkms.cryptoKeyVersions.useToSign"]

  depends_on = [google_project_service.this]
}
