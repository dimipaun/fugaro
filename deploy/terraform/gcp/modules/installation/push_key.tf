# The push sender's relay identity (mobile implementation plan task P5.2.7,
# restated 2026-11-25 for the relay; Q7 of docs/mobile/implementation-plan.md):
# ONE Ed25519 key in the installation's project whose private half never leaves
# Cloud KMS, and the one grant that lets the sender sign with it.
#
# WHY A KEY AND NOT A DOWNLOADABLE CREDENTIAL. The publisher's relay has to know
# that a message really comes from this installation, and the thing that says so
# must be an identity nothing else can present. Q7 answered on 2026-11-21 that
# the sender reaches the publisher's relay and not FCM directly; what the relay
# asks for is a signed request, and the signature is made here. A token in a
# Secret Manager container beside this key would be a copy: readable by whoever
# reaches the container, storable anywhere, and revocable only by rotating it
# everywhere at once. The private half of this key exists in the managed store
# and nowhere else — not in a fixture, not in a state file, not in a process's
# memory — and disabling the key version is what revokes it. This is O17's
# sentence for the consumer's key (mailbox_key.tf), applied to the sender's.
#
# ONE KEY, ONE SIGNER, AND THE SIGNER IS THE SENDER. The sender of this plan is
# the installation's history job (history.tf; P5.2.6's sweeper), so the one grant
# is made to that account and to no other, and the binding sits in history.tf
# beside that account's other grants rather than here: a grant written beside
# this key is one no reader of that file's list would look for. What the design
# refuses by this and not by a rule is (A), a run signing its own pushes: a run
# account that could sign could name any repository in a title, and a push is
# read by a person as the installation speaking. So the grant's role is a custom
# role holding cloudkms.cryptoKeyVersions.useToSign alone — the nearest
# predefined role (roles/cloudkms.signerVerifier) carries the verify half and the
# metadata reads beside it — and internal/infra/tf's RolePermissions pins that
# list while TestRolePinsAreTheModules holds the two to each other.
#
# WHY A SECOND KEY RATHER THAN THE CONSUMER'S. mailbox_key.tf's key is the
# installation's to its own phones; this one is the installation's to a party
# outside it. They are read by different verifiers, revoked for different
# reasons, and a relay that must stop trusting this installation is not a reason
# to invalidate what every paired phone pinned. Two keys, two roles, two
# bindings, and neither reaches the other's key: internal/infra/tf's
# pushSignerGrant refuses a grant of this role on another key, in another
# project, under a condition, or to an account that is not this sender's.
#
# THE SWITCH IS THE SENDER'S. The sender exists only where enable_budget is on
# (history.tf's account, job and schedule are all counted on it), so the key, the
# role and the binding are counted on the same switch and an installation that
# sweeps nothing plans exactly what it planned before this file existed — which
# is what every golden but installation-budget-job holds, and what a
# previous-version installation converges to on the update that carries this file
# (docs/design/update-in-flight.md).
#
# NOTHING HERE SENDS ANYTHING, and nothing here is FCM. This file is the
# identity and the reach to use it; the code that calls the relay is the
# sender's, and the relay itself is the publisher's. No FCM resource, no FCM API
# and no firebasecloudmessaging permission is declared in this tree by this task:
# a sender that reached FCM directly would be Q7's other answer, and the plan
# left it to the owner.
#
# NOTHING HERE HOLDS DATA EXCEPT THE KEY ITSELF, and the key is one whose loss
# cannot be repaired: destroy it and the relay stops accepting this
# installation's messages, with no error anywhere in the installation until
# someone notices the pushes stopped. So it carries prevent_destroy
# (deploy/terraform's TestPreventDestroy), and internal/infra/tf classifies
# google_kms_crypto_key as data-bearing (D1551), which is what refuses a plan
# that would delete or replace it.
#
# TURNING THE SWITCH OFF IS NOT A PLAN THIS ROOT MAY MAKE, mailbox_key.tf's
# lesson: enable_budget off drops this key's count to zero and the key carries
# prevent_destroy, so Terraform refuses the WHOLE plan ("Instance cannot be
# destroyed") and applies nothing — the ring, the role and history.tf's binding
# cannot be removed in that plan either. Taking the key out for good is two owner
# steps, in this order:
#
#   1. terraform state rm 'module.installation.google_kms_crypto_key.push_sender[0]'
#      — forgets the key. It deletes nothing in GCP, destroys no material and
#      leaves the relay's trust in this installation exactly as it was. The
#      address is the installation ROOT's (the resource is inside
#      `module "installation"`, so the bare address names nothing in that state
#      and `terraform state rm` refuses it), and the single quotes are: `[0]` is
#      a glob to zsh, which would refuse the command before terraform ran.
#   2. an ordinary plan and apply with enable_budget off, which removes the ring,
#      the role and the binding and touches no key.
#
# A plan that typed that removal without these steps would meet D1551's guard
# instead, which asks its owner by name (tf.Guard) — but it never gets the
# chance, because Terraform refuses first.
#
# THE API IS ENABLED HERE OR NOT AT ALL (apis.tf): a key cannot be created in a
# project whose Cloud KMS API is off, and enabling it is free.

locals {
  # The sender's own switch: the history job is the sender (P5.2.6), and an
  # installation with no sweeper has nothing to send with.
  push_key_on = var.enable_budget

  # The key ring's and the key's names, written out rather than derived from
  # var.fugaro_project, for mailbox_key.tf's reason: internal/infra/tf names this
  # key by these literals (PushKeyRingName, PushCryptoKeyName), and a name
  # derived from a variable is a name a hand-edited tfvars moves — a moved name
  # is a second key beside the one the relay was told to trust.
  push_key_ring   = "fugaro-push"
  push_crypto_key = "fugaro-push-sender"

  # The key's own resource name, which history.tf's signing grant writes as a
  # literal for that file's launcher row's reason — a reference to a resource's
  # computed name reaches the plan as UNKNOWN, and the cover could then admit a
  # grant of this role on any key of the project — so the literal is composed
  # once here and pinned by internal/infra's TestPushKeyNamesAreTheGoSpellings.
  push_crypto_key_id = "projects/${var.project}/locations/${var.region}/keyRings/${local.push_key_ring}/cryptoKeys/${local.push_crypto_key}"
}

# The ring the key lives in. A ring holds no material and cannot be deleted once
# it holds a key, which is why nothing here carries a lifecycle setting: the
# thing worth keeping is the key below, and it refuses its own removal.
resource "google_kms_key_ring" "push" {
  count = local.push_key_on ? 1 : 0

  project  = var.project
  name     = local.push_key_ring
  location = var.region

  depends_on = [google_project_service.this]
}

# The key itself: ONE version, Ed25519, asymmetric signing, and a private half
# that cannot be exported. version_template is what creates the first version, so
# no google_kms_crypto_key_version block stands beside it and no version is ever
# created by hand.
#
# SOFTWARE and not HSM, and it is not a preference: Cloud KMS offers Ed25519 at
# SOFTWARE alone — HSM does not carry the algorithm. What the protection level
# buys the relay is the same either way: the material is in the managed store,
# the API is the only way to use it, and the account's useToSign is the only
# grant that reaches it.
resource "google_kms_crypto_key" "push_sender" {
  count = local.push_key_on ? 1 : 0

  # No project argument: the crypto key's project is the key ring's, which the
  # provider takes from key_ring's own id (the resource has no project field at
  # this pin), and the ring above names it.
  name     = local.push_crypto_key
  key_ring = google_kms_key_ring.push[0].id
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

# The sender's role: cloudkms.cryptoKeyVersions.useToSign, and nothing else. It
# is the project's role rather than a key-level binding of a wider one, so that
# the reach a plan grants is a permission list internal/infra/tf pins by role ID
# and not an inherited role's whole surface.
resource "google_project_iam_custom_role" "push_signer" {
  count = local.push_key_on ? 1 : 0

  project     = var.project
  role_id     = "fugaroPushSigner"
  title       = "Fugaro push sender"
  description = "Signs the installation's push requests with its sender key and does nothing else with it: the private half is in Cloud KMS and this is the account's only way to reach it."
  permissions = ["cloudkms.cryptoKeyVersions.useToSign"]

  depends_on = [google_project_service.this]
}
