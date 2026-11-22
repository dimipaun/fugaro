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
  log_bucket_location       = var.log_bucket_location
  bucket_lifecycle          = var.bucket_lifecycle
  enable_vertex             = var.enable_vertex
  manage_apis               = var.manage_apis
  launchers                 = var.launchers
  launcher_accounts         = var.launcher_accounts
  operators                 = var.operators
  viewers                   = var.viewers
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
  reports                   = var.reports
  licence                   = var.licence
  credentials_service       = var.credentials_service
  enable_mobile_service     = var.enable_mobile_service
  intake                    = var.intake
  voice_service             = var.voice_service
  project_secrets           = var.project_secrets
  managed_deployer_scoped   = var.managed_deployer_scoped
  orchestrator              = var.orchestrator
  people_minter             = var.people_minter
}

# The shared project jobs (design docs/project-centric/spec.md §4.1): one
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

# The resident orchestrator's launch grant (mobile plan task P5.1b; the
# design's section 4.3, the run.jobs.run and run.jobs.runWithOverrides row).
# It is the only row of that table this root can carry and the installation
# module cannot: the account starts the installation's own project jobs, which
# this root creates and the module never sees. The role is the same
# names.role_ids.job_runner the project module hands each job's launchers
# above, so the two grants of that role name one role.
#
# The two attributes are written out rather than read off the resources they
# name, and for the reason orchestrator.tf's header gives at length:
# module.installation.role_ids.job_runner and module.installation.orchestrator
# are both COMPUTED (a custom role's name and a service account's email), so
# reading them would reach the plan as unknown, and internal/infra/tf's cover
# reads a grant's role and its principal off the plan — unknown, the role is one
# no reader can name and the member is one whose kind is unknowable, which is
# what the design's eleventh never-rule exists to keep this account out of. The
# role is the ID literal and not `${var.names.role_ids.job_runner}` for the
# reason state_read.tf's and orchestrator.tf's own role literals give: a
# project's custom role named in a binding is named by its ID, and
# internal/infra's TestOrchestratorGrantsAreTheDesignsTableRows pins the literal
# to the role_id of the role the same plan creates, so a drift between this
# spelling and the tfvars' fails rather than binding a role that does not exist.
#
# for_each is empty for an installation that has not opted in, which is what
# keeps the whole table off: a null var.orchestrator creates no account, no
# binding in this file and nothing in the module, so the plan of an
# installation that never asked for a resident orchestrator is byte for byte
# the plan it had before this block existed.
#
# Nothing narrower than the job is offered, and nothing wider: the design's
# table grants this role on the installation's jobs and the account may start
# any of them, which is what a launch is — the env, the mounts and the
# credential are the job's own. What the role withholds is the job's
# lifecycle: it can neither create, update nor delete one, so the account
# cannot change what a launch would run.
resource "google_cloud_run_v2_job_iam_member" "orchestrator_runner" {
  for_each = var.orchestrator == null ? {} : var.project_jobs

  project  = var.project
  location = var.region
  name     = module.project[each.key].job
  role     = "projects/${var.project}/roles/fugaroJobRunner"
  member   = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [module.installation]
}
