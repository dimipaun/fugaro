output "jobs" {
  description = "Each workflow's job name, or null while its job isn't deployed."
  value       = { for k, w in module.workflow : k => w.job }
}

output "service_accounts" {
  description = "Each workflow's job account email."
  value       = { for k, w in module.workflow : k => w.service_account }
}

output "build_service_account" {
  description = "The build account's email."
  value       = google_service_account.build.email
}

output "registry" {
  description = "The repository's image registry ID."
  value       = google_artifact_registry_repository.images.repository_id
}

output "check_job" {
  description = "The check job's name, or null when every workflow's check is off or the check job isn't deployed yet."
  value       = local.deploy_check ? google_cloud_run_v2_job.check[0].name : null
}
