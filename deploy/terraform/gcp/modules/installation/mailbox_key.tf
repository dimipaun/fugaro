# The consumer's signing key (mobile-identity-pairing plan task J6; O17 of
# docs/mobile-identity-pairing/reconciliation.md): ONE Ed25519 key in the
# installation's project whose private half never leaves Cloud KMS, and the one
# grant that lets the mailbox consumer sign with it.
#
# WHY A MANAGED KEY AT ALL, AND WHY IT IS THE ONLY COPY. O6 replaced the
# mailbox's symmetric tag with a per-message signature: the phone verifies the
# consumer's replies and heartbeats with a consumer public key it pinned at
# pairing, so the consumer must hold a private key. A key file on the
# consumer's host is a key that can be copied, backed up, mailed and read by
# anything that reaches the disk, and there is no revocation for it beyond
# re-pairing every phone. O17 puts the private half in Cloud KMS instead,
# non-exportable: the signature is an API call made as the consumer's own
# account, no copy of the material exists — not in a fixture, not in a state
# file, not in a process's memory — and disabling the key version is what
# revokes it. internal/mailbox's KMSSigner is the Go half of that sentence: it
# holds a key version's NAME and calls the signing API, and nothing in this
# tree holds private key material.
#
# ONE KEY, ONE SIGNER, AND THE SIGNER IS THE CONSUMER. The mailbox consumer of
# this plan is the installation's resident orchestrator (P5.1; docs/mobile/
# design/always-on-orchestrator.md §4.2-§4.3, which the reconciliation pins —
# "it signs its replies through J6's KMS signer"), so the one grant is made to
# that account and to no other. The binding is NOT in this file: section 4.3's
# table is orchestrator.tf's alone (orchestrator_test.go's
# TestOrchestratorIsNeverALauncher refuses the account's address as a member in
# any other file, and is right to — a grant written beside this key is one no
# reader of that table would look for), so the grant lives there under
# orchestrator_mailbox_signer and this file holds the key, the ring and the
# role it signs with. It is a custom role rather than a predefined one for the
# reason ee/keyservice/terraform/keyring's signer role gives: the nearest
# predefined role carries the verify half and the metadata reads beside
# useToSign (roles/cloudkms.signerVerifier), and this account signs and does
# nothing else with the key. The role holds cloudkms.cryptoKeyVersions.useToSign
# alone; RolePermissions pins the list in internal/infra/tf/cover.go and
# TestRolePinsAreTheModules holds the two to each other, so a later revision
# that widened the signer's reach would have to widen this file and that pin in
# the same change.
#
# THE SWITCH, AND WHY THE KEY IS NOT BEHIND THE ORCHESTRATOR'S ALONE. The key
# belongs to an installation that has either end of the conversation: the
# public mobile service (mobile_service.tf), whose pairing response carries
# C1's SessionResponse.ConsumerPublicKey and whose pairing QR carries that
# key's fingerprint (O16), or the resident orchestrator, which IS the consumer
# and signs with it. An installation with neither pairs no phone and signs
# nothing, so it plans exactly what it planned before this file existed — which
# is what every golden but installation-mobile holds, and what a
# previous-version installation converges to on the update that carries this
# file (docs/design/update-in-flight.md). An installation that has the pairing
# front end and no consumer yet is a real state — the mobile service's own
# switch is what brings the front end up, and the owner's go for the consumer
# is P5.1's — and it gets the key with the role created beside it and no
# binding: nothing signs, and the key exists for the phone to pin once a
# consumer is opted into. That is why the key and the role are counted on
# local.mailbox_key_on and the binding, in orchestrator.tf, on
# local.orchestrator_on. Whether the owner's own role may sign it is U12 of
# the reconciliation and is deliberately not answered here.
#
# NOTHING HERE HOLDS DATA EXCEPT THE KEY ITSELF, and the key is the one
# resource of this tree whose loss cannot be repaired: destroy it and every
# phone that pinned its public half verifies nothing the installation ever
# signs again, with no error anywhere until someone re-pairs by hand. So it
# carries prevent_destroy (deploy/terraform's TestPreventDestroy), and
# internal/infra/tf classifies google_kms_crypto_key as data-bearing (D1551),
# which is what refuses a plan that would delete or replace it. Turning the
# switch off removes the role and the binding and leaves the key: a removal of
# it asks its owner by name (tf.Guard).
#
# THE API IS ENABLED HERE OR NOT AT ALL (apis.tf): a key cannot be created in a
# project whose Cloud KMS API is off, and enabling it is free.

locals {
  # Both switches, for the reason the header gives: the key is the pairing front
  # end's and the consumer's, so either one of them being on is enough, and an
  # installation that has neither plans nothing here.
  mailbox_key_on = local.mobile_service_on || local.orchestrator_on

  # The key ring's and the key's names, written out rather than derived from
  # var.fugaro_project: the pair is the installation's one signing key, and
  # internal/mailbox's KMSSigner and the update step that publishes the public
  # half both name it by these literals (ConsumerKeyRingName,
  # ConsumerCryptoKeyPath). A name derived from a variable is a name a
  # hand-edited tfvars moves, and a moved name is a second key beside the one
  # every paired phone pinned.
  mailbox_key_ring   = "fugaro-mailbox"
  mailbox_crypto_key = "fugaro-mailbox-consumer"

  # The key's own resource name, which orchestrator.tf's signing grant writes
  # as a literal for that file's launcher row's reason — a reference to a
  # resource's computed name reaches the plan as UNKNOWN, and the cover could
  # then admit a grant of this role on any key of the project — so the literal
  # is composed once here from the two names above and pinned by
  # internal/infra's TestMailboxKeyNamesAreTheGoSpellings.
  mailbox_crypto_key_id = "projects/${var.project}/locations/${var.region}/keyRings/${local.mailbox_key_ring}/cryptoKeys/${local.mailbox_crypto_key}"
}

# The ring the key lives in. A ring holds no material and cannot be deleted
# once it holds a key, which is why nothing here carries a lifecycle setting:
# the thing worth keeping is the key below, and it refuses its own removal.
resource "google_kms_key_ring" "mailbox" {
  count = local.mailbox_key_on ? 1 : 0

  project  = var.project
  name     = local.mailbox_key_ring
  location = var.region

  depends_on = [google_project_service.this]
}

# The key itself: ONE version, Ed25519, asymmetric signing, and a private half
# that cannot be exported — the whole of O17's first sentence. version_template
# is what creates the first version, so no google_kms_crypto_key_version block
# stands beside it and no version is ever created by hand.
#
# SOFTWARE and not HSM, and it is not a preference: Cloud KMS offers Ed25519 at
# SOFTWARE alone — HSM does not carry the algorithm — which is the fact
# ee/keyservice/terraform/keyring's own key states. What the protection level
# buys the phone is the same either way: the material is in the managed store,
# the API is the only way to use it, and the account's useToSign is the only
# grant that reaches it.
resource "google_kms_crypto_key" "mailbox_consumer" {
  count = local.mailbox_key_on ? 1 : 0

  # No project argument: the crypto key's project is the key ring's, which the
  # provider takes from key_ring's own id (the resource has no project field at
  # this pin), and the ring below names it.
  name     = local.mailbox_crypto_key
  key_ring = google_kms_key_ring.mailbox[0].id
  purpose  = "ASYMMETRIC_SIGN"

  version_template {
    algorithm        = "EC_SIGN_ED25519"
    protection_level = "SOFTWARE"
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}

# The signer's role: cloudkms.cryptoKeyVersions.useToSign, and nothing else.
# It is the project's role rather than a key-level binding of a wider one, so
# that the reach a plan grants is a permission list internal/infra/tf pins by
# role ID and not an inherited role's whole surface.
resource "google_project_iam_custom_role" "mailbox_signer" {
  count = local.mailbox_key_on ? 1 : 0

  project     = var.project
  role_id     = "fugaroMailboxSigner"
  title       = "Fugaro mailbox signer"
  description = "Signs with the installation's consumer key and does nothing else with it: the private half is in Cloud KMS and this is the account's only way to reach it."
  permissions = ["cloudkms.cryptoKeyVersions.useToSign"]

  depends_on = [google_project_service.this]
}
