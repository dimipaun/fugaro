# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# What S3.8's next step reads: the key name whose public half is published into
# the CLI, the console and the app (S2.9) while the service does not exist yet, and
# the ring the services root grants the signer role on.
output "key_ring" {
  description = "This environment's key ring resource name, as services/ references it."
  value       = google_kms_key_ring.signing.id
}

output "key" {
  description = "This environment's key id, the one `keys` names for the environment this apply carried."
  value       = google_kms_crypto_key.signing.id
}

output "signer_role" {
  description = "The custom signer role's resource name, which services/ grants to the minter's account."
  value       = google_project_iam_custom_role.signer.name
}
