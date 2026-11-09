# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# What S3.8's next step reads: the key names whose public halves are published
# into the CLI, the console and the app (S2.9) while the service does not exist
# yet, and the ring the services root grants the signer role on.
output "key_ring" {
  description = "The key ring's resource name, as services/ references it."
  value       = google_kms_key_ring.signing.id
}

output "keys" {
  description = "Each environment's key id, keyed the way var.keys is."
  value       = { for env, key in google_kms_crypto_key.signing : env => key.id }
}

output "signer_role" {
  description = "The custom signer role's resource name, which services/ grants to the minter's account."
  value       = google_project_iam_custom_role.signer.name
}
