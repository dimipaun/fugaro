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
# ls does. It gets a role of its own with just that (and the job reads the
# listing joins), not fugaroLauncher, which also cancels executions and reads
# secret metadata. On the project, nothing on the FP (the Firebase root
# grants those).
resource "google_project_iam_custom_role" "history" {
  count = var.enable_budget ? 1 : 0

  project = var.project
  role_id = "fugaroHistory"
  title   = "Fugaro history sweeper"
  permissions = [
    "run.executions.list",
    "run.executions.get",
    "run.jobs.get",
    "run.jobs.list",
    "run.operations.get",
  ]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_member" "history_launcher" {
  count = var.enable_budget ? 1 : 0

  project = var.project
  role    = google_project_iam_custom_role.history[0].name
  member  = google_service_account.history[0].member
}

# Nobody is granted actAs on this account, operators included: it holds
# firebasedatabase.admin on the FP, so anyone who could deploy a job as it
# could lift caps and kill switches (design D6). Executing the existing job
# needs no actAs; only the applier that creates or updates the job acts as it.

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
        command = ["/usr/local/bin/fugaro"]
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
        # M9d: the rollover reads result.json objects in the runs bucket and
        # writes the FP's (default) Firestore database. No credential.
        env {
          name  = "FUGARO_RUNS_BUCKET"
          value = var.runs_bucket
        }
        env {
          name  = "FUGARO_FIRESTORE_DB"
          value = "(default)"
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

# The rollover reads the runs' result.json objects (the notional and compute
# columns of a day's record come from there). objectViewer, on this bucket
# only and with no condition (unverified assumption A3), never objectAdmin:
# the job account has no write access to the runs bucket. Granted whenever
# the account exists (the first apply), like its other project roles.
resource "google_storage_bucket_iam_member" "history_runs_reader" {
  count = var.enable_budget ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.history[0].member
}

# The sweep, every 15 minutes (D12). The rollover is its own Scheduler job
# below. Scheduler isn't offered in every Cloud Run
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

# The rollover, once a day at 00:30 UTC (M9d H7): the same Cloud Run job, run
# with its args replaced by --rollover through the run API's overrides. The
# sweep stays liveness only. Unverified assumption A4: that jobs.run accepts
# overrides.containerOverrides[].args; the test asserts exactly this body, and
# the live runbook proves it. Overrides replace the container's args whole,
# so they repeat "budget history". The job's timeout (600s) applies. Retry
# 0: a failed rollover logs, and the next day's run (idempotent, with its
# backfill window) covers the gap.
resource "google_cloud_scheduler_job" "history_rollover" {
  count = local.deploy_history ? 1 : 0

  project   = var.project
  region    = var.history.scheduler_region
  name      = var.history.rollover_scheduler_job
  schedule  = "30 0 * * *"
  time_zone = "Etc/UTC"

  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.history[0].location}/jobs/${google_cloud_run_v2_job.history[0].name}:run"
    headers     = { "Content-Type" = "application/json" }
    body = base64encode(jsonencode({
      overrides = {
        containerOverrides = [{ args = ["budget", "history", "--rollover"] }]
      }
    }))

    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.history_invoker]
}
