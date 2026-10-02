# The history job: a scheduled, admin-privileged Cloud Run job that sweeps
# the run registry of crashed runs (fugaro budget history --sweep, D12). It
# is the one exception to "no budget service". The account is created in the
# first of init --firebase's applies, so the Firebase root can grant it
# roles on the FP; the job and its Scheduler job arrive in the third, when
# the FP's outputs exist (history.deploy_job).

locals {
  deploy_history = var.enable_budget && try(var.history.deploy_job, false)
}

resource "google_service_account" "history" {
  count = var.enable_budget ? 1 : 0

  project      = var.project
  account_id   = var.history.account_id
  display_name = "Fugaro history"

  lifecycle {
    precondition {
      condition     = var.history != null
      error_message = "enable_budget needs history"
    }
  }

  depends_on = [google_project_service.this]
}

# The sweep's cross-check lists the project's Cloud Run executions, as fugaro
# ls does: fugaroLauncher holds run.executions.list and read access to jobs.
# On the project, nothing on the FP (the Firebase root grants those).
resource "google_project_iam_member" "history_launcher" {
  count = var.enable_budget ? 1 : 0

  project = var.project
  role    = google_project_iam_custom_role.launcher.name
  member  = google_service_account.history[0].member
}

# Deploying the job acts as its account.
resource "google_service_account_iam_member" "history_user" {
  for_each = var.enable_budget ? toset(var.operators) : toset([])

  service_account_id = google_service_account.history[0].name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}

resource "google_cloud_run_v2_job" "history" {
  count = local.deploy_history ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.history.job
  labels = {
    fugaro      = "managed"
    fugaro_role = "history"
  }
  deletion_protection = true

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "history"
    }
    task_count = 1

    template {
      max_retries           = 0
      timeout               = "600s"
      service_account       = google_service_account.history[0].email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image   = var.history.image
        command = ["fugaro"]
        args    = ["budget", "history", "--sweep"]

        resources {
          limits = {
            cpu    = "1"
            memory = "512Mi"
          }
        }

        # The one Terraform-defined job environment (R10). No credential:
        # the job's identity is its account.
        env {
          name  = "FUGARO_PROJECT"
          value = var.fugaro_project
        }
        env {
          name  = "FUGARO_GCP_PROJECT"
          value = var.project
        }
        env {
          name  = "FUGARO_REGION"
          value = var.region
        }
        env {
          name  = "FUGARO_FIREBASE_PROJECT"
          value = var.history.firebase_project
        }
        env {
          name  = "FUGARO_RTDB_URL"
          value = var.history.rtdb_url
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  depends_on = [google_project_iam_member.history_launcher]
}

# roles/run.invoker holds run.jobs.run, which Scheduler's call needs, on
# this job only.
resource "google_cloud_run_v2_job_iam_member" "history_invoker" {
  count = local.deploy_history ? 1 : 0

  project  = var.project
  location = google_cloud_run_v2_job.history[0].location
  name     = google_cloud_run_v2_job.history[0].name
  role     = "roles/run.invoker"
  member   = google_service_account.scheduler.member
}

# The sweep, every 15 minutes (D12). There is no rollover job: that, and
# Firestore, arrive with M9d. Scheduler isn't offered in every Cloud Run
# region, so it runs in the scheduler region and the URI names the job's own.
resource "google_cloud_scheduler_job" "history_sweep" {
  count = local.deploy_history ? 1 : 0

  project   = var.project
  region    = var.history.scheduler_region
  name      = var.history.scheduler_job
  schedule  = "*/15 * * * *"
  time_zone = "Etc/UTC"

  # A failed sweep logs its failure; the next one is the retry.
  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.history[0].location}/jobs/${google_cloud_run_v2_job.history[0].name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.history_invoker]
}
