# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The identity project's signing key: the key ring the assertion signer signs
# with, and the one Ed25519 key inside it (D2781, D2792, O7).
#
# The key ring is a root of its own in the key service's tree for a reason this
# root does not share: there, the keys outlive the services that sign with them
# and two states mean an apply of the services can never destroy one. Here the
# ring and the service are one state, and what protects the key is its lifecycle
# below rather than a separate apply.
#
# ASYMMETRIC_SIGN with EC_SIGN_ED25519, created by the key store's own API: the
# private half is generated inside Cloud KMS, is signed with through the API, and
# is read out by nothing — not by the service, not by the owner, not by an agent
# (.claude/rules/ee.md). Ed25519 is offered at SOFTWARE and not at HSM, so the
# level is stated rather than left to a default that would refuse the algorithm.
# What the tree cannot assert is that the API offers the pair: the provider types
# `version_template.algorithm` as a plain string, so `terraform validate` accepts
# any value and Check 730's apply against the real API is the measurement.

resource "google_kms_key_ring" "signing" {
  project  = var.project
  name     = var.keyring
  location = var.region

  depends_on = [google_project_service.this]
}

# The one key in the ring, and the one the signer signs with. Its name comes from
# a variable rather than a literal because a verifier that is handed the key id
# (P8 pins the public half of it) reads the same value this apply creates, and a
# second spelling of it here is a rotation that reaches one of the two.
resource "google_kms_crypto_key" "signing" {
  key_ring = google_kms_key_ring.signing.id
  name     = var.key

  purpose = "ASYMMETRIC_SIGN"

  version_template {
    algorithm        = "EC_SIGN_ED25519"
    protection_level = "SOFTWARE"
  }

  # Every installation verifies assertions against this key's public half, and a
  # destroy takes the key id out of the ring: rotation is a new key id published
  # in the served key set (A2, A4, Check 737), never an apply's side effect, so
  # removing a key is a deliberate edit of this file.
  lifecycle {
    prevent_destroy = true
  }
}

# The signer's whole reach of the key ring: sign, and read the public half to
# publish it. useToSign is what Cloud KMS checks on an asymmetricSign call and
# viewPublicKey is what A2's JWKS is built from; the service verifies nothing and
# administers nothing, so a predefined role would be wider than the two — the
# signerVerifier role also verifies, and owner and editor reach the ring itself.
resource "google_project_iam_custom_role" "signer" {
  project     = var.project
  role_id     = var.signer_role
  title       = "Fugaro identity assertion signer"
  description = "Signs project-bound assertions with the identity project's key ring and reads the public half to publish it. The signer service's account is its only holder (A3, O7, Check 730)."
  permissions = ["cloudkms.cryptoKeyVersions.useToSign", "cloudkms.cryptoKeyVersions.viewPublicKey"]

  depends_on = [google_project_service.this]
}
