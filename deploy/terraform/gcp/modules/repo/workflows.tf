locals {
  # Every secret a workflow may mount, by logical name: the repository's own
  # containers, overlaid with the project-wide ones Readiness resolved for
  # this repository (repo.project_secrets, design docs/design/project-secrets.md
  # §5). Readiness only resolves a project copy for a name whose repo copy has
  # no enabled version, so a name in both maps is exactly the "the repo
  # override is gone" case and the project value is the right one; merge()
  # preferring the second map is what makes that true.
  secrets = merge({ for k, s in google_secret_manager_secret.this : k => s.secret_id }, var.repo.project_secrets)
}

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
  secrets          = local.secrets
  runs_bucket      = var.installation.runs_bucket
  job_runner_role  = var.installation.role_ids.job_runner
  launchers        = var.installation.launchers
  operators        = var.installation.operators
  allow_job_delete = var.allow_job_delete

  # The installation's credentials service, when it has one (D146, D147): the
  # workflow module grants each job's account roles/run.invoker on it and drops
  # that job's model-secret accessors. Null on an installation that has not
  # opted in, which is every installation today, and then nothing changes.
  credentials_service = var.installation.credentials_service
}
