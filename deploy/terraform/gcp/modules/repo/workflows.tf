# One job, job account and set of grants per workflow. Each workflow gets
# the IDs of the repository's secret resources, so its grants on a secret
# wait for the secret itself.
module "workflow" {
  source   = "../workflow"
  for_each = var.repo.workflows

  project          = var.project
  fugaro_project   = var.fugaro_project
  region           = var.region
  name             = each.key
  repo_label       = var.repo.label
  workflow         = each.value
  secrets          = { for k, s in google_secret_manager_secret.this : k => s.secret_id }
  runs_bucket      = var.installation.runs_bucket
  job_runner_role  = var.installation.role_ids.job_runner
  launchers        = var.installation.launchers
  operators        = var.installation.operators
  allow_job_delete = var.allow_job_delete
}
