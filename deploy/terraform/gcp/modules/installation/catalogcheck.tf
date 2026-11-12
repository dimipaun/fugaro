# The project's catalog check job (D1148): ONE Cloud Run job, scheduled, that
# runs `fugaro image check --job --catalog` and decides every entry of the
# project's image catalog in one pass, submitting at most one Cloud Build per
# entry that fires. Its name starts with fugarochk-, so fugaro runs ls and
# max_parallel, which count the fugaro- jobs, ignore it, like the repository
# check jobs.
#
# The account is the project's own and is NOT a repository's build account.
# Every build the job submits runs as the build account of the repository the
# entry belongs to, through the actAs grant the repository module makes on it
# (modules/repo/catalogcheck.tf); this account never builds anything itself, so
# it holds no registry role, no secret accessor and no key. Its whole reach is
# the spec in its environment, the five grants below and, in each repository
# root, the actAs on that repository's build account. One of the five is in the
# Firebase project and not this one — the database role its build leases are
# written with (D1554) — and it is created and bound here rather than in the
# Firebase root, because it is this job's reach and the account is this file's.
#
# No log writer grant: this installation's other jobs — the coordinator's and
# the history job's — hold none, and the log lines the alert policies match
# are Cloud Run's own (alert.tf). The builds this job submits run as the build
# accounts, which hold roles/logging.logWriter where a build's lines need it
# (modules/repo/build.tf).
#
# Nothing else in the installation changes for it, and it exists only when
# fugaro update's tfvars carry catalog_check — an installation whose project
# publishes no catalog plans exactly what it planned before the job existed.

locals {
  # The lease, and with it the two resources below, exists exactly when the job
  # does and the installation has a Firebase project to write it into: no
  # catalog_check is no job (an installation whose project publishes no
  # catalog), and an unset firebase_project is an installation with no budget
  # backend, where a role granted on a project nobody named would be a grant on
  # a guess (the variable's validation refuses a value that is not a project
  # ID, so unset is the only empty case).
  catalog_lease_on = var.catalog_check != null && var.catalog_check.firebase_project != null
}

# The builds it submits: cloudbuild.builds.create to submit one and
# cloudbuild.builds.get to read its status, which is the custom role each
# repository's build account holds for the same two operations
# (modules/repo/build.tf). Cloud Build offers no narrower scope than the
# project for either permission. The role is a literal path, for the reason
# iam.tf's launcher grant gives: a project grant whose role is computed cannot
# be told from one of the deployer's at plan time.
resource "google_project_iam_member" "catalog_check_build_submitter" {
  count = var.catalog_check == null ? 0 : 1

  project = var.project
  role    = "projects/${var.project}/roles/fugaroBuildSubmitter"
  member  = google_service_account.catalog_check[0].member

  depends_on = [google_project_iam_custom_role.build_submitter]
}

resource "google_service_account" "catalog_check" {
  count = var.catalog_check == null ? 0 : 1

  project      = var.project
  account_id   = var.catalog_check.account_id
  display_name = "Fugaro catalog check"

  depends_on = [google_project_service.this]
}

# The copied definitions (builds/catalog/), read-only: the definition a build
# of an entry reads, which is the same project-wide copy every build account
# reads (modules/repo/build.tf's catalog_read). objectViewer and not
# objectUser, for the reason that grant gives: a job that could rewrite a
# definition could make every repository run an image of its choosing. The
# condition is the input byte for byte (gcp.CatalogReadCondition), and
# internal/infra/tf refuses the binding if it changes.
resource "google_storage_bucket_iam_member" "catalog_check_definitions" {
  count = var.catalog_check == null ? 0 : 1

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.catalog_check[0].member

  condition {
    title      = var.catalog_check.definitions_condition.title
    expression = var.catalog_check.definitions_condition.expression
  }
}

# One repository's build records and check state, inside the repository's own
# prefix: builds/<slug>/catalog/ and nothing else — never
# builds/<slug>/<workflow>/, which is the repository's daily check's. objectUser
# is the whole of it: the check reads the record a build wrote and writes its
# own check.json beside it. The condition is the input byte for byte
# (gcp.CatalogRecordsCondition), one member per repository, so no entry of one
# repository can reach another's state.
resource "google_storage_bucket_iam_member" "catalog_check_records" {
  for_each = var.catalog_check == null ? {} : { for r in var.catalog_check.repositories : r.slug => r }

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectUser"
  member = google_service_account.catalog_check[0].member

  condition {
    title      = each.value.records_condition.title
    expression = each.value.records_condition.expression
  }
}

# The job's own argv is the mode task (i) added: --catalog without --job is
# refused, and so is either outside a Cloud Run job. The spec arrives in
# FUGARO_CATALOG_CHECK_SPEC, and its environment carries the platform
# variables the repository check job's does (catalog_check.env, written by
# internal/infra). It mounts no secret: the build it submits reads the git
# credential as the repository's build account, and this job reads none.
resource "google_cloud_run_v2_job" "catalog_check" {
  count = var.catalog_check == null ? 0 : 1

  project  = var.project
  location = var.region
  name     = var.catalog_check.job
  labels = {
    fugaro      = "managed"
    fugaro_role = "catalog-check"
  }
  deletion_protection = true

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "catalog-check"
    }
    task_count = 1

    template {
      max_retries = 0
      # Longer than a repository check's 900s: one run reads every entry's
      # definition, record and check state and submits a build per entry that
      # fires, where the repository job does that for one repository's
      # workflows.
      timeout               = "1800s"
      service_account       = google_service_account.catalog_check[0].email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image   = var.catalog_check.image
        command = ["fugaro"]
        args    = ["image", "check", "--job", "--catalog"]

        resources {
          limits = {
            cpu    = "1"
            memory = "2Gi"
          }
        }

        dynamic "env" {
          for_each = var.catalog_check.env
          content {
            name  = env.key
            value = env.value
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]

    # Both sides are generated by the same Go code, so this checks for drift
    # (a hand-edited tfvars), not for an attacker. A precondition fails the
    # plan; a check block would only warn.
    precondition {
      condition     = lookup(var.catalog_check.env, "FUGARO_PROJECT", "") == var.fugaro_project
      error_message = "the job's FUGARO_PROJECT must be the Fugaro project ${var.fugaro_project}; run fugaro update"
    }
    precondition {
      condition     = lookup(var.catalog_check.env, "FUGARO_GCP_PROJECT", "") == var.project
      error_message = "the job's FUGARO_GCP_PROJECT must be the GCP project ${var.project}; run fugaro update"
    }
  }
}

# roles/run.invoker holds run.jobs.run, which Scheduler's call needs, on this
# job only, held by the installation's scheduler account — the same grant and
# the same reason as the repository check job's (modules/repo/check.tf).
resource "google_cloud_run_v2_job_iam_member" "catalog_check_invoker" {
  count = var.catalog_check == null ? 0 : 1

  project  = var.project
  location = google_cloud_run_v2_job.catalog_check[0].location
  name     = google_cloud_run_v2_job.catalog_check[0].name
  role     = "roles/run.invoker"
  member   = google_service_account.scheduler.member
}

# Scheduler isn't offered in every Cloud Run region, so it runs in the
# scheduler region and the URI names the job's own. paused is the input's and
# its default is true: a project that publishes a catalog deploys the schedule
# stopped, and the installation that wants the nightly run writes a false
# (fugaro update). Terraform applies that value as written; nothing here
# resumes the schedule on its own.
resource "google_cloud_scheduler_job" "catalog_check" {
  count = var.catalog_check == null ? 0 : 1

  project   = var.project
  region    = var.catalog_check.scheduler_region
  name      = var.catalog_check.scheduler_job
  schedule  = var.catalog_check.schedule
  time_zone = "Etc/UTC"
  paused    = var.catalog_check.paused

  # A failed check logs its failure; the next night's check is the retry.
  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.catalog_check[0].location}/jobs/${google_cloud_run_v2_job.catalog_check[0].name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.catalog_check_invoker]
}

# The job's reach over the Firebase database, which is the only thing outside
# this project it touches (D1554).
#
# Why it needs one at all: each build the job submits reserves its estimated
# cost before Cloud Build is asked for it, and the reservation is written into
# buildleases/<slug>/<id> (internal/budget/lease_build.go). That family is
# denied whole to every client token — no token in this installation writes a
# lease, and the rules' template says so — so the writer is an IAM identity and
# nothing else: the account's access token carries the permissions its
# principal holds, and those are what this role is. It is the same mechanism
# the credentials service writes its own nodes with (credentials.tf).
#
# Why these two permissions and not roles/firebasedatabase.admin, which is
# what the coordinator's and the history job's accounts hold: Realtime Database
# IAM has no path-level scope, so a binding reaches the whole instance, and
# what the role says is which OPERATIONS the account may make. Read at its real
# size rather than by the code that uses it: the two data permissions read and
# write EVERY node of the database — the published policy and the caps it is
# held to included — and the installation's rules do not stand in the way,
# because IAM bypasses them (SECURITY.md states the same pair for the
# credentials service's account). The job's code touches the leases and the
# day's counters it owns and nothing else, and that is a property of the code
# and not of the grant. What the role does keep out is the instance lifecycle.
# firebasedatabase.instances.get is the only read there is (the reclaim of a
# lease a dead CLI left, the day's counters and the database's own mark) and
# firebasedatabase.instances.update the only write (RTDB has no separate
# create or delete of a node). admin adds list and the instance LIFECYCLE —
# create, delete, disable, reenable, undelete — which would let a scheduled
# job nobody is watching delete the installation's database, or lift the caps
# it is held to. This role is exactly the credentials service's two
# permissions and lives in the same project; the difference is the account and
# the reason, and the two roles stay two so that neither account's reach can
# grow by the other's needs (internal/infra/tf's RolePermissions pins both).
#
# It is created in the Firebase project and not in var.project, which is the
# two-project shape's whole point: the account is this project's, the database
# is the Firebase root's (D6), and the binding is cross-project IAM exactly as
# the credentials service's database role is. An installation that has no
# Firebase backend writes no firebase_project and gets neither resource: there
# is no database to grant on, and the job submits its builds unleased, saying
# so in one line (internal/cli's openCatalogJobLease).
#
# The role is spelled literally in the binding, for the reason
# credentials.tf's own states: google_project_iam_custom_role's name attribute
# is computed, so a binding through it reaches the plan as an unknown role and
# internal/infra/tf's cover could not say which role the confirmation covers.
# var.catalog_check.firebase_project is known at plan time (the variable's
# validation), so the literal is.
#
# Neither resource is on internal/infra/tf's tables, and that is the design's
# own rule for this account rather than an omission: the catalog check job is a
# kind of principal of its own (toCatalogCheck), so every grant to it is a plan
# the review did not announce — the update that first carries this one asks its
# owner, and a coding agent's session is refused — which is what keeps a grant
# to this account from being covered silently by a row (cover.go's
# CatalogCheckAccountID).
resource "google_project_iam_custom_role" "catalog_lease" {
  count = local.catalog_lease_on ? 1 : 0

  project = var.catalog_check.firebase_project
  role_id = "fugaroCatalogLease"
  title   = "Fugaro catalog check (database data only)"
  permissions = [
    "firebasedatabase.instances.get",    # read: the leases left behind, the day's counters and the database's mark
    "firebasedatabase.instances.update", # write: the build leases this job takes and settles
  ]
}

resource "google_project_iam_member" "catalog_lease" {
  count = local.catalog_lease_on ? 1 : 0

  project = var.catalog_check.firebase_project
  role    = "projects/${var.catalog_check.firebase_project}/roles/fugaroCatalogLease"
  member  = google_service_account.catalog_check[0].member

  depends_on = [google_project_iam_custom_role.catalog_lease]
}
