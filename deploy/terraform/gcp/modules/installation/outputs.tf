output "project_name" {
  description = "The Fugaro project's name. fugaro init --repo copies it to each repository's jobs as FUGARO_PROJECT."
  value       = var.fugaro_project
}

output "runs_bucket" {
  description = "The runs bucket's name."
  value       = google_storage_bucket.runs.name
}

output "registry_host" {
  description = "The Artifact Registry host and project path, <region>-docker.pkg.dev/<project>."
  value       = "${var.region}-docker.pkg.dev/${var.project}"
}

output "base_registry" {
  description = "The base images' registry ID."
  value       = google_artifact_registry_repository.base.repository_id
}

output "legacy_registry" {
  description = "The adopted legacy registry's ID, or null when none was adopted."
  value       = var.adopt_legacy_registry ? google_artifact_registry_repository.legacy[0].repository_id : null
}

output "scheduler_service_account" {
  description = "The email of the account Cloud Scheduler starts the image checks as."
  value       = google_service_account.scheduler.email
}

output "role_ids" {
  description = "The custom roles' full names (projects/<project>/roles/<id>)."
  value = {
    launcher        = google_project_iam_custom_role.launcher.name
    job_runner      = google_project_iam_custom_role.job_runner.name
    build_submitter = google_project_iam_custom_role.build_submitter.name
    tag_mover       = google_project_iam_custom_role.tag_mover.name
  }
}

output "launchers" {
  description = "The members who launch and watch runs."
  value       = var.launchers
}

output "operators" {
  description = "The members who onboard repositories."
  value       = var.operators
}

output "log_view" {
  description = "The log view Fugaro's job logs are read through, or null without log isolation."
  value       = var.log_isolation ? "${local.log_bucket_path}/views/${google_logging_log_view.runs[0].name}" : null
}

output "registry_cleanup_dry_run" {
  description = "Whether registry cleanup only logs what it would delete. fugaro init --repo copies it to each repository's registry."
  value       = var.registry_cleanup.dry_run
}

output "history_service_account" {
  description = "The history job's account email, or null without enable_budget. fugaro init --firebase passes it to the Firebase root, which grants it its roles on the FP."
  value       = var.enable_budget ? google_service_account.history[0].email : null
}

output "history_job" {
  description = "The history job's name, or null while it isn't deployed."
  value       = local.deploy_history ? google_cloud_run_v2_job.history[0].name : null
}
