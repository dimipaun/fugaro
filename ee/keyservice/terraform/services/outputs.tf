# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# What the deploy (P9.2, S3.7) wires up: where the SPA and the merchant's webhook
# point, the URL the API calls the minter on, the key id the API seals a handover
# with, and the accounts a secret is mounted on.
output "api_url" {
  description = "The API's URL: the SPA's calls and the merchant's webhook endpoint."
  value       = google_cloud_run_v2_service.api.uri
}

output "minter_url" {
  description = "The minter's URL, reached by the API's account alone."
  value       = google_cloud_run_v2_service.minter.uri
}

output "api_account" {
  description = "The API's account address, which the minter's invoker grant and the handover grant name."
  value       = google_service_account.api.email
}

output "minter_account" {
  description = "The minter's account address, the one identity in the signer role."
  value       = google_service_account.minter.email
}

output "scheduler_account" {
  description = "The account the scheduled calls' OIDC tokens are minted for, which the API verifies."
  value       = google_service_account.scheduler.email
}

output "handover_key" {
  description = "The handover key's id, which the API is configured with."
  value       = google_kms_crypto_key.handover.id
}
