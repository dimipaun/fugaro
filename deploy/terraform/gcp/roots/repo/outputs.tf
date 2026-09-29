# fugaro init --repo reads these with terraform output -json.

output "jobs" {
  description = "Each workflow's job name, or null while its job isn't deployed."
  value       = module.repo.jobs
}

output "service_accounts" {
  description = "Each workflow's job account email."
  value       = module.repo.service_accounts
}

output "build_service_account" {
  description = "The build account's email."
  value       = module.repo.build_service_account
}

output "registry" {
  description = "The repository's image registry ID."
  value       = module.repo.registry
}

output "check_job" {
  description = "The check job's name, or null when every workflow's check is off or the check job isn't deployed yet."
  value       = module.repo.check_job
}

output "github_app_id" {
  description = "The GitHub App's ID, or null for a Bitbucket repository."
  value       = var.github_app_id
}
