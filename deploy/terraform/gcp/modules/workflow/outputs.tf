output "job" {
  description = "The job's name, or null while it isn't deployed."
  value       = var.workflow.deploy_job ? google_cloud_run_v2_job.this[0].name : null
}

output "service_account" {
  description = "The job account's email."
  value       = google_service_account.job.email
}

output "service_account_id" {
  description = "The job account's ID."
  value       = google_service_account.job.account_id
}

output "secret_refs" {
  description = "The secret each of the job's secret env vars references, by env var, or null while the job isn't deployed."
  value = var.workflow.deploy_job ? {
    for e in google_cloud_run_v2_job.this[0].template[0].template[0].containers[0].env :
    e.name => one(one(e.value_source).secret_key_ref).secret if length(e.value_source) > 0
  } : null
}

output "deletion_protection" {
  description = "Whether the job is protected from deletion, or null while it isn't deployed."
  value       = var.workflow.deploy_job ? google_cloud_run_v2_job.this[0].deletion_protection : null
}
