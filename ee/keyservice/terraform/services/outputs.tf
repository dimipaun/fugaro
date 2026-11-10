# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# What the deploy (S3.7) wires up: where the site's forms post and where the
# merchant's webhook points, the URL the intake calls the minter on, and the two
# accounts a secret is mounted on.
output "intake_url" {
  description = "The intake's URL: the site's form action and the merchant's webhook endpoint."
  value       = google_cloud_run_v2_service.intake.uri
}

output "minter_url" {
  description = "The minter's URL, reached by the intake's account alone."
  value       = google_cloud_run_v2_service.minter.uri
}

output "intake_account" {
  description = "The intake's account address, which the minter's invoker grant names."
  value       = google_service_account.intake.email
}

output "minter_account" {
  description = "The minter's account address, the one identity in the signer role."
  value       = google_service_account.minter.email
}
