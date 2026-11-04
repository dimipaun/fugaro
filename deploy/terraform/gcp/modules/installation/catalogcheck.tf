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
# the spec in its environment and the six grants below.
#
# Nothing else in the installation changes for it, and it exists only when
# fugaro update's tfvars carry catalog_check — an installation whose project
# publishes no catalog plans exactly what it planned before the job existed.

# Its own log lines (the alert's one channel): Cloud Run needs
# logging.logEntries.create, so this one is a project-level member and there is
# no narrower scope for it. It is the same member each repository's build
# account holds (modules/repo/build.tf), not a wider role.
resource "google_project_iam_member" "catalog_check_log_writer" {
  count = var.catalog_check == null ? 0 : 1

  project = var.project
  role    = "roles/logging.logWriter"
  member  = google_service_account.catalog_check[0].member
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
# scheduler region and the URI names the job's own. It is created PAUSED and
# nothing resumes it but the run that finds every catalog entry has a build
# record: a first run would otherwise submit a build for every entry whose
# record it cannot read, which is the whole catalog.
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
