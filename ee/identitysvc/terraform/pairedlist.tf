# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The encrypted paired-list store's cloud side (mobile identity plan task K1,
# D2796, F39, O16): the identity project's own Realtime Database, the symmetric
# Cloud KMS key the list is sealed under, and the one grant that reaches it.
#
# **What is kept here is ciphertext.** Each person's list is one node under
# `paired/<uid>` holding a sealed document and nothing else, sealed with the key
# below through KMS's encrypt and decrypt calls with the uid as the call's
# additional authenticated data (pairedlist.go). No project id, endpoint or name
# is readable in a dump of the tree.
#
# **The database is created locked, and that is the client rule.** Firebase
# creates an instance whose rules grant nothing, and rtdb.rules.json beside this
# directory — `.read` and `.write` false at the root, granting nothing anywhere
# — is the reviewed statement of that rule. **Nothing in this tree applies it,
# and that is a deferral rather than a step** (recorded here, K1, and in the
# pull request's own deferred list): no Terraform resource deploys Realtime
# Database rules — they are published over the database's own REST API
# (`/.settings/rules`, internal/rtdb's PutRules) — no command of the CLI
# publishes them, and Check 730 (docs/gcp-live-checklist.md) is a read-only check
# by design, so it carries no publish step either. What denies a client today is
# the instance's own Firebase default, which is the same document; a publish and
# a read-back are a step the owner takes until one is wired somewhere that runs
# for them. What reads and writes a node is the signer account below, which holds
# a database role and therefore passes the rules rather than being governed by
# them; a phone holds a Firebase ID token and the project's API key and is
# refused. That asymmetry is the acceptance's "only the function decrypts and
# only for its owner": the function is the only reader, and it serves the uid its
# caller's verified ID token attests.

# The identity project's Realtime Database instance. `DEFAULT_DATABASE` and the
# `<project>-default-rtdb` id are what Firebase itself names the one instance a
# project has, and the region is a creation-time field: an instance cannot move,
# so a region that is already right is one an input may never change, and the
# field is ignored rather than planned into a replacement.
#
# The list is the only thing this database holds, and it is the reason the
# identity project is a runtime dependency of pairing and restore and of nothing
# else (F2, D2798).
resource "google_firebase_database_instance" "pairedlist" {
  provider = google-beta

  project       = var.project
  region        = var.database_region
  instance_id   = "${var.project}-default-rtdb"
  type          = "DEFAULT_DATABASE"
  desired_state = "ACTIVE"

  lifecycle {
    prevent_destroy = true
    ignore_changes  = [region]
  }

  # Both, and not the Firebase link alone: creating an instance needs
  # firebasedatabase.googleapis.com, which main.tf enables (a project linked to
  # Firebase has not necessarily ever used Realtime Database, and the apply is
  # what fails with SERVICE_DISABLED when it has not).
  depends_on = [google_firebase_project.this, google_project_service.this]
}

# The list's key ring, and deliberately a second ring beside the signing one
# (keyring.tf). Cloud KMS grants the sealer role at the ring below, so one ring
# would be a ring where the service's sealing grant and its signing grant sit
# together; two rings are two reaches that can be read apart, which is the same
# reason the key service's sealer is a different ring from its signer (D2182).
resource "google_kms_key_ring" "pairedlist" {
  project  = var.project
  name     = var.pairedlist_keyring
  location = var.region

  depends_on = [google_project_service.this]
}

# The one symmetric key the lists are sealed under. ENCRYPT_DECRYPT with
# GOOGLE_SYMMETRIC_ENCRYPTION: the material is generated inside Cloud KMS, is
# used through the API and is read out by nothing — not by the service, not by
# the owner, not by an agent (.claude/rules/ee.md). No asymmetric key can take
# additional authenticated data in this shape, which is why the uid's binding is
# this key's and not a signature's.
#
# The rotation period is here rather than left out: a key that is never rotated
# is a key whose compromise reaches every list ever sealed under it, and KMS
# re-wraps the old versions on its own so a rotation needs nothing of this
# service.
resource "google_kms_crypto_key" "pairedlist" {
  key_ring        = google_kms_key_ring.pairedlist.id
  name            = var.pairedlist_key
  purpose         = "ENCRYPT_DECRYPT"
  rotation_period = var.pairedlist_rotation

  version_template {
    algorithm        = "GOOGLE_SYMMETRIC_ENCRYPTION"
    protection_level = "SOFTWARE"
  }

  # Every person's list is sealed under this key, so removing it is a
  # deliberate edit of this file and never an apply's side effect.
  lifecycle {
    prevent_destroy = true
  }
}

# The service's whole reach of the list's ring: seal and open. It signs nothing
# with it and administers nothing — the signer role's permissions
# (keyring.tf) do nothing on a symmetric key, and this role's do nothing on an
# asymmetric one, so the separation is enforced by the permissions as well as by
# the rings.
resource "google_project_iam_custom_role" "pairedlist_sealer" {
  project     = var.project
  role_id     = var.pairedlist_sealer_role
  title       = "Fugaro identity paired-list sealer"
  description = "Seals and opens the mobile app's paired list under the identity project's symmetric key. The signer service's account is its only holder (K1, D2796)."
  permissions = ["cloudkms.cryptoKeyVersions.useToEncrypt", "cloudkms.cryptoKeyVersions.useToDecrypt"]

  depends_on = [google_project_service.this]
}

# The grant, made at the ring and to the service's own account, exactly as the
# signing role is (main.tf): a grant at the project would reach every key the
# project ever holds, and the sealing key is one key.
resource "google_kms_key_ring_iam_member" "signer_seals" {
  key_ring_id = google_kms_key_ring.pairedlist.id
  role        = google_project_iam_custom_role.pairedlist_sealer.id
  member      = google_service_account.signer.member
}

# The service's read and write of the database the list lives in, and the other
# half of "only the function decrypts and only for its owner": the rules above
# deny every Firebase ID token, and an account that holds the database's data
# permissions is not governed by the rules at all — which is the asymmetry, not
# an outage. Realtime Database has no per-instance IAM resource in Terraform, so
# the grant is made at the project; the identity project has exactly one database
# and it is the one above.
#
# The role is a custom one carrying the database's two DATA permissions and
# nothing else, never `roles/firebasedatabase.admin`: that predefined role adds
# the instance lifecycle — create, delete, disable, reenable, undelete — beside
# the list's read and write, so the account would be able to delete the store of
# every person's paired list, and this account is the one the internet reaches
# through the service's own public invoker. It is the same pair, for the same
# reason, that the credentials service's database role is
# (`fugaroCredentialsDatabase`, SECURITY.md and internal/infra/tf/cover.go's
# CredentialsDatabaseRole, D6), and the same reason a predefined role is refused
# here (`.claude/rules/terraform.md`). `instances.get` is the read of any node of
# the instance and `instances.update` the write of any node of it: Realtime
# Database IAM has no path-level scope, so the store's reach of one node per
# person is the code's and not this role's, and the sealing key above is what
# makes the tree a ciphertext to whoever holds it.
resource "google_project_iam_custom_role" "pairedlist_database" {
  project     = var.project
  role_id     = var.pairedlist_database_role
  title       = "Fugaro identity paired-list database"
  description = "Reads and writes the nodes of the identity project's Realtime Database that hold the mobile app's sealed paired lists, and does nothing else with the instance. The signer service's account is its only holder (K1, D2796)."
  permissions = ["firebasedatabase.instances.get", "firebasedatabase.instances.update"]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_member" "signer_reaches_pairedlist" {
  project = var.project
  role    = google_project_iam_custom_role.pairedlist_database.id
  member  = google_service_account.signer.member
}

variable "database_region" {
  description = "The region the identity project's Realtime Database instance is created in. Realtime Database offers a short list of locations (us-central1, us-east1, us-east4, us-west1, europe-west1, europe-west2, asia-southeast1 at the time of writing) and an instance cannot move after creation, so this is the database's home rather than a preference. It is separate from var.region because the key ring's region and the database's are two different lists."
  type        = string
  default     = "us-central1"

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.database_region))
    error_message = "database_region must be a region name such as us-central1. The provider types it as a plain string, so only the API refuses a location Realtime Database does not offer."
  }
}

variable "pairedlist_keyring" {
  description = "The name of the key ring the paired list's symmetric key is created in. It is a ring of its own and not the signing one (keyring.tf), so the sealing grant and the signing grant are two reaches."
  type        = string
  default     = "fugaro-identity-pairedlist"
}

variable "pairedlist_key" {
  description = "The name of the symmetric key inside that ring, for the lists the app syncs. The service seals and opens through the key, which picks its primary version."
  type        = string
  default     = "fugaro-paired-list"
}

variable "pairedlist_sealer_role" {
  description = "The custom role the signer's account is granted on the paired-list key ring."
  type        = string
  default     = "fugaroIdentityPairedListSealer"
}

variable "pairedlist_database_role" {
  description = "The custom role the signer's account is granted at the project for the paired-list database. It carries the instance's two data permissions and not the predefined admin role, whose instance lifecycle would let the account delete the store of every person's list."
  type        = string
  default     = "fugaroIdentityPairedListDatabase"
}

variable "pairedlist_rotation" {
  description = "How often Cloud KMS rotates the paired-list key. A rotation re-wraps the versions already in use, so nothing of this service changes when one happens, and a key that is never rotated is one whose compromise reaches every list ever sealed under it."
  type        = string
  default     = "7776000s" # 90 days, the period the plan's defaults take for rotation (O4).

  validation {
    condition     = can(regex("^[0-9]+s$", var.pairedlist_rotation))
    error_message = "pairedlist_rotation must be a whole number of seconds, such as 7776000s."
  }
}

# What the deployment needs from this file: the database the lists live in and
# the key they are sealed under. Both are outputs rather than values written
# into the service's template here, because the service resource is main.tf's
# and a second place the same name lives is a second place a deploy can be
# wrong. Whoever mounts the route into the program (the same wiring A2's routes
# wait on) reads these two and passes them as the program's own configuration.
output "pairedlist_database_url" {
  description = "The identity project's Realtime Database URL the paired-list store reads and writes (K1)."
  value       = google_firebase_database_instance.pairedlist.database_url
}

output "pairedlist_key" {
  description = "The resource name of the symmetric key the paired list is sealed under, as the store's sealing call names it (K1)."
  value       = google_kms_crypto_key.pairedlist.id
}
