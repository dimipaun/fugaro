# The installation root: the resources every repository shares. fugaro update
# writes it into a workdir with terraform.tfvars.json and a backend config
# naming the state bucket, and plans and applies it there.
terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
  }

  backend "gcs" {}
}

# The project is always explicit, and API quota is billed to it, as it is
# with user credentials and a quota project.
provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

module "installation" {
  source = "../../modules/installation"

  project                   = var.project
  fugaro_project            = var.fugaro_project
  region                    = var.region
  runs_bucket               = var.runs_bucket
  state_bucket              = var.state_bucket
  names                     = var.names
  log_bucket_description    = var.log_bucket_description
  bucket_lifecycle          = var.bucket_lifecycle
  enable_vertex             = var.enable_vertex
  manage_apis               = var.manage_apis
  launchers                 = var.launchers
  operators                 = var.operators
  launcher_bucket_condition = var.launcher_bucket_condition
  budget                    = var.budget
  alert_email               = var.alert_email
  registry_cleanup          = var.registry_cleanup
  adopt_legacy_registry     = var.adopt_legacy_registry
  log_isolation             = var.log_isolation
  enable_budget             = var.enable_budget
  history                   = var.history
  coordinator               = var.coordinator
  mcp_server                = var.mcp_server
  catalog_check             = var.catalog_check
  credentials_service       = var.credentials_service
  project_secrets           = var.project_secrets
  managed_deployer_scoped   = var.managed_deployer_scoped
}

# The shared project jobs (design docs/design/project-centric.md §4.1): one
# Cloud Run job and one job service account per (profile, provider,
# account), from the local config's profiles. The key is absent for an
# installation that names no profile, and the module then creates nothing.
#
# They belong to the installation, not to a repository root: a project job
# serves every repository admitted to its profile, and its account must
# outlive any one repository's root. fugaro update computes every name, env
# and condition of each entry (internal/infra.ProjectJobs); nothing here
# derives one.
module "project" {
  source   = "../../modules/project"
  for_each = var.project_jobs

  project         = var.project
  fugaro_project  = var.fugaro_project
  region          = var.region
  job             = each.value
  runs_bucket     = var.runs_bucket
  secrets         = module.installation.project_secrets
  job_runner_role = module.installation.role_ids.job_runner
  launchers       = var.launchers
  operators       = var.operators

  # The runs bucket arrives as a name, and every other grant in this tree
  # references the bucket resource itself — which is what orders them. On a
  # fresh installation the bucket is created in this same apply, and a
  # bucket grant created before it would fail with a 404, so this module
  # waits for the installation that creates it (internal/cli's init note:
  # "the apply may just have created the runs bucket").
  depends_on = [module.installation]
}

# The review-only variants of those jobs (design docs/cloud-senior-review,
# CSR7): one job and one job service account per (profile, provider, account)
# the coding set deploys, named with credsvc's review suffix. They are the
# containers the project's own senior review runs on, so they hold no
# credential of the installation's: their secret_env is empty and the
# installation's secret containers are deliberately NOT handed to this module
# (`secrets = {}` below), which is the O1 ruling of 2026-11-01 as a plan-level
# fact — a variant offered no container cannot be granted one, whatever a later
# edit writes into project_review_jobs. What such a job is given instead is the
# credentials service's address in its own environment (internal/infra's
# ProjectReviewJobs), from which it fetches the run's read-only installation
# token by name.
#
# A module instance of its own rather than entries in the coding set: the
# coding set is the account axis every consumer computes the job set with, and
# the credentials service's admitted accounts are derived from that axis rather
# than from a second job set (credsvc.ReviewJobAccountsFor). ProjectJobs and
# ProjectReviewJobs are both computed by fugaro update, and nothing here derives
# a name, an env or a condition.
module "project_review" {
  source   = "../../modules/project"
  for_each = var.project_review_jobs

  project         = var.project
  fugaro_project  = var.fugaro_project
  region          = var.region
  job             = each.value
  runs_bucket     = var.runs_bucket
  secrets         = {}
  job_runner_role = module.installation.role_ids.job_runner
  launchers       = var.launchers
  operators       = var.operators

  # As module.project above: the bucket grant waits for the installation that
  # creates the bucket it names.
  depends_on = [module.installation]
}
