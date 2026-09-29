# The daily image check: a Cloud Run job that runs fugaro image check --job
# from the base image, as the build account, and a Scheduler job that
# starts it. Its name starts with fugarochk-, so fugaro ls and max_parallel,
# which count the fugaro- jobs, ignore it. There is none when every
# workflow has rebuild.check: off.
#
# The job mounts the provider credential at latest, which Cloud Run checks
# when it creates the job, so fugaro init --repo deploys it (with its
# invoker grant and Scheduler job) only once the credential has a version,
# or when the job already exists: check.deploy_job is that gate.

locals {
  deploy_check = var.repo.check == null ? false : var.repo.check.deploy_job
}

resource "google_cloud_run_v2_job" "check" {
  count = local.deploy_check ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.repo.check.job
  labels = {
    fugaro      = "managed"
    fugaro_repo = var.repo.label
    fugaro_role = "check"
  }
  deletion_protection = !var.allow_job_delete

  template {
    # Only the template's labels reach the job's log entries, which the log
    # sink and exclusion select on; they are the same marks.
    labels = {
      fugaro      = "managed"
      fugaro_repo = var.repo.label
      fugaro_role = "check"
    }
    task_count = 1

    template {
      max_retries           = 0
      timeout               = "900s"
      service_account       = google_service_account.build.email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image   = var.repo.check.image
        command = ["fugaro"]
        args    = ["image", "check", "--job"]

        resources {
          limits = {
            cpu    = "1"
            memory = "2Gi"
          }
        }

        dynamic "env" {
          for_each = var.repo.check.env
          content {
            name  = env.key
            value = env.value
          }
        }

        dynamic "env" {
          for_each = var.repo.check.secret_env
          content {
            name = env.key
            value_source {
              secret_key_ref {
                secret  = google_secret_manager_secret.this[env.value].secret_id
                version = "latest"
              }
            }
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.build]
}

# roles/run.invoker holds run.jobs.run, which Scheduler's call needs, on
# this job only.
resource "google_cloud_run_v2_job_iam_member" "check_invoker" {
  count = local.deploy_check ? 1 : 0

  project  = var.project
  location = google_cloud_run_v2_job.check[0].location
  name     = google_cloud_run_v2_job.check[0].name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${var.installation.scheduler_service_account}"
}

# Scheduler isn't offered in every Cloud Run region, so it runs in the
# scheduler region, and the URI names the job's own region. It is paused
# until every checked workflow has a build record, so a first check can't
# start a billable build that moves a live image unasked.
resource "google_cloud_scheduler_job" "check" {
  count = local.deploy_check ? 1 : 0

  project   = var.project
  region    = var.repo.check.scheduler_region
  name      = var.repo.check.scheduler_job
  schedule  = var.repo.check.schedule
  time_zone = "Etc/UTC"
  paused    = var.repo.check.paused

  # A failed check logs its failure; the next day's check is the retry.
  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.check[0].location}/jobs/${google_cloud_run_v2_job.check[0].name}:run"

    oauth_token {
      service_account_email = var.installation.scheduler_service_account
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.check_invoker]
}
