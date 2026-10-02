# fugaro init reads these with terraform output -json and passes them to
# each repository root.

output "project_name" {
  value = module.installation.project_name
}

output "runs_bucket" {
  value = module.installation.runs_bucket
}

output "registry_host" {
  value = module.installation.registry_host
}

output "base_registry" {
  value = module.installation.base_registry
}

output "legacy_registry" {
  value = module.installation.legacy_registry
}

output "scheduler_service_account" {
  value = module.installation.scheduler_service_account
}

output "role_ids" {
  value = module.installation.role_ids
}

output "launchers" {
  value = module.installation.launchers
}

output "operators" {
  value = module.installation.operators
}

output "log_view" {
  value = module.installation.log_view
}

output "registry_cleanup_dry_run" {
  value = module.installation.registry_cleanup_dry_run
}

output "history_service_account" {
  value = module.installation.history_service_account
}

output "history_job" {
  value = module.installation.history_job
}
