locals {
  # Every secret a workflow may mount, by logical name: the repository's own
  # containers, overlaid with the project-wide ones Readiness resolved for
  # this repository (repo.project_secrets, design docs/project-secrets/spec.md
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

  # The installation's credentials service, when this repository is handed its
  # address with it (D146, D147; infra.CredentialsServiceAddress): the workflow
  # module grants each job's account roles/run.invoker on it, and, with the
  # fact below, drops that job's model-secret accessors. Null on an
  # installation that has not opted in and on a repository the switch holds off
  # that is no `vertex` one, and then nothing changes.
  credentials_service = var.installation.credentials_service

  # Whether that service serves THIS repository's credentials, which is what
  # the workflow module's drop reads (D3326, F17): false for a `vertex`
  # repository, handed the address with nothing of its job's taken away.
  credentials_service_serves = var.installation.credentials_service_serves

  # Which model credentials each job of THIS repository gives its accessor up
  # for (D2072, G17), from the repository's own answer: the workflow module
  # reads it for the model half of the drop, and it is a separate value from
  # the service above because the two come apart — a root applied before
  # B2.7's flip carries `none` for an `api-key` or `oauth` repository and keeps
  # the mount and the accessor of a job the launcher already hands the service's
  # address, and `all` is what a tfvars written before the input existed means.
  model_secrets_served = var.repo.model_secrets_served

  # The per-repository fact (D500, D502), passed to every workflow of this
  # repository: each of a proxied repository's jobs gives the credential up,
  # and no job of any other repository does (G9). Nothing derives it yet — the
  # published node is the one source of whether a repository is proxied (G15),
  # and the derivation is B1.8b's, in internal/infra — so every tfvars sent
  # today omits the key, which means false.
  bitbucket_proxied = var.repo.bitbucket_proxied
}
