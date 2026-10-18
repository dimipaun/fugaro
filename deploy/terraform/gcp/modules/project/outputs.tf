output "job" {
  description = "The Cloud Run job's name."
  value       = google_cloud_run_v2_job.this.name
}

output "service_account" {
  description = "The job account's email. A run's identity is this account, which is why the package has one per (profile, provider, account)."
  value       = google_service_account.job.email
}
