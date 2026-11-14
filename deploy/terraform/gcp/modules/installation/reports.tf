# The project's reports job (slice P, ruling E1): ONE Cloud Run job, scheduled,
# that runs `fugaro runs reports --job` and writes one report per repository and
# period and the project's aggregate beside them, into reports/ of the runs
# bucket. Its name starts with fugarorpt-, for the reason fugarochk- exists:
# `fugaro runs ls` and max_parallel count the fugaro- jobs as workflow jobs, and
# the alert policies match the prefix.
#
# The account is the project's own — one per installation, not one per
# repository — and it is the ONE writer of reports/ (ruling 3): no launcher's
# write condition reaches that prefix and no job account holds it. What it
# reads is runs/, every repository's run records, which is what the reports are
# computed from; a person reads a stored report through the bucket-wide
# objectViewer the launchers already hold, and nothing new is granted to a
# person for this job to exist.
#
# It holds no project role, mounts no secret and carries no git or model
# credential: its whole reach is the two conditioned grants below and the spec
# in its environment. Nothing else in the installation changes for it, and it
# exists only when fugaro update's tfvars carry reports — an installation that
# asked for no reports plans exactly what it planned before the job existed.

# The account the job runs as and the two grants are written for.
resource "google_service_account" "reports" {
  count = var.reports == null ? 0 : 1

  project      = var.project
  account_id   = var.reports.account_id
  display_name = "Fugaro reports"

  depends_on = [google_project_service.this]
}

# Every repository's run records, read-only: runs/ of the runs bucket, which is
# what a report summarises. The condition is the input byte for byte
# (gcp.ReportsReadCondition) — the objects under runs/ joined by " || " to the
# listing of runs/ itself — and internal/infra/tf refuses the binding if it
# changes: a condition carrying only the object clause grants no listing at all,
# a List request naming no object, and one carrying only the listing clause
# cannot read a record. The widened bucket-wide viewer a listing without this
# mechanism would need is NOT planned: it would also read tokens/ and cache/.
resource "google_storage_bucket_iam_member" "reports_read" {
  count = var.reports == null ? 0 : 1

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.reports[0].member

  condition {
    title      = var.reports.read_condition.title
    expression = var.reports.read_condition.expression
  }
}

# reports/ and nothing else, read-write: the stored report of a repository and
# a period and the project's aggregate, which the job writes once and overwrites
# with the same content on a rerun, reads back for a later period's report and
# the year's roll-up, and leaves to the bucket's lifecycle (bucket.tf, E3).
# objectUser is the whole of the prefix and the account is its only holder
# (ruling 3): objectViewer would leave the job unable to write its own report,
# and a second writer would be a plan a launcher's or a job's identity could
# reach. The condition is the input byte for byte (gcp.ReportsWriteCondition).
resource "google_storage_bucket_iam_member" "reports_write" {
  count = var.reports == null ? 0 : 1

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectUser"
  member = google_service_account.reports[0].member

  condition {
    title      = var.reports.write_condition.title
    expression = var.reports.write_condition.expression
  }
}

# The job's own argv is R11's mode half: --job without `runs reports` is
# refused, and so is the mode outside a Cloud Run job. The spec arrives in
# FUGARO_REPORTS_JOB_SPEC, and its environment carries the platform variables
# every job has (reports.env, written by internal/infra).
resource "google_cloud_run_v2_job" "reports" {
  count = var.reports == null ? 0 : 1

  project  = var.project
  location = var.region
  name     = var.reports.job
  labels = {
    fugaro      = "managed"
    fugaro_role = "reports"
  }
  deletion_protection = true

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "reports"
    }
    task_count = 1

    template {
      max_retries = 0
      # A repository check's 900s is not enough: one run reads every
      # repository's records for the period and writes the report of each one,
      # where the check does that for one repository.
      timeout               = "1800s"
      service_account       = google_service_account.reports[0].email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image   = var.reports.image
        command = ["fugaro"]
        args    = ["runs", "reports", "--job"]

        resources {
          limits = {
            cpu    = "1"
            memory = "2Gi"
          }
        }

        dynamic "env" {
          for_each = var.reports.env
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
      condition     = lookup(var.reports.env, "FUGARO_PROJECT", "") == var.fugaro_project
      error_message = "the job's FUGARO_PROJECT must be the Fugaro project ${var.fugaro_project}; run fugaro update"
    }
    precondition {
      condition     = lookup(var.reports.env, "FUGARO_GCP_PROJECT", "") == var.project
      error_message = "the job's FUGARO_GCP_PROJECT must be the GCP project ${var.project}; run fugaro update"
    }
  }
}

# roles/run.invoker holds run.jobs.run, which Scheduler's call needs, on this
# job only, held by the installation's scheduler account — the same grant and
# the same reason as the repository check job's (modules/repo/check.tf).
resource "google_cloud_run_v2_job_iam_member" "reports_invoker" {
  count = var.reports == null ? 0 : 1

  project  = var.project
  location = google_cloud_run_v2_job.reports[0].location
  name     = google_cloud_run_v2_job.reports[0].name
  role     = "roles/run.invoker"
  member   = google_service_account.scheduler.member
}

# Scheduler isn't offered in every Cloud Run region, so it runs in the
# scheduler region and the URI names the job's own. paused is the input's and
# its default is true: the job ships stopped, and the installation that wants
# the nightly run writes jobs.reports.paused = false in its project layer, which
# `fugaro update` renders into this variable. Terraform applies that value as
# written; nothing here resumes the schedule on its own, and a `gcloud scheduler
# jobs resume` would be reverted by the next update.
resource "google_cloud_scheduler_job" "reports" {
  count = var.reports == null ? 0 : 1

  project   = var.project
  region    = var.reports.scheduler_region
  name      = var.reports.scheduler_job
  schedule  = var.reports.schedule
  time_zone = "Etc/UTC"
  paused    = var.reports.paused

  # A failed run logs its failure; the next night's is the retry.
  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.reports[0].location}/jobs/${google_cloud_run_v2_job.reports[0].name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.reports_invoker]
}

# The job's own alert, and its own policy rather than a third entry of
# alert.tf's image_alerts: that map's policies are the image checks', and the
# one that watches a job execution fail matches the fugarochk- prefix, so a
# reports execution that died before it could log its decision would be silent.
# Nothing about an alert raises the deployment's cost or its reach, and a
# project that named no alert address gets no policy (the channel is the one
# alert.tf's variable creates).
resource "google_monitoring_alert_policy" "reports" {
  count = var.reports == null || var.alert_email == null ? 0 : 1

  project      = var.project
  display_name = "Fugaro reports failed"
  combiner     = "OR"

  conditions {
    display_name = "A reports run failed"
    # One condition and not the image check's two: both the failure the job
    # logs itself and the execution Cloud Run records as having died are
    # severity ERROR on this job's resource, so one filter reaches both.
    condition_matched_log {
      filter = "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarorpt-\" AND severity>=ERROR"
    }
  }

  notification_channels = [google_monitoring_notification_channel.email[0].id]

  # Log-match conditions require a notification rate limit.
  alert_strategy {
    notification_rate_limit {
      period = "3600s"
    }
    auto_close = "604800s"
  }
}
