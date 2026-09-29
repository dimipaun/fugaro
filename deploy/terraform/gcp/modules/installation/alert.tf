# An optional email alert when an image rebuild or check fails, so a broken
# nightly check never goes unnoticed.
resource "google_monitoring_notification_channel" "email" {
  count = var.alert_email == null ? 0 : 1

  project      = var.project
  display_name = "Fugaro alerts"
  type         = "email"
  labels       = { email_address = var.alert_email }

  depends_on = [google_project_service.this]
}

resource "google_monitoring_alert_policy" "image" {
  count = var.alert_email == null ? 0 : 1

  project      = var.project
  display_name = "Fugaro image check failed"
  combiner     = "OR"

  # The check's own line for each workflow whose rebuild or check failed.
  conditions {
    display_name = "An image rebuild or check failed"
    condition_matched_log {
      filter = "jsonPayload.event=\"image-check\" AND jsonPayload.decision=(\"rebuild-failed\" OR \"check-failed\")"
    }
  }

  # A check job execution that failed before it could log its decision,
  # read from the Cloud Run system log (the exclusion keeps it in _Default).
  # The query language has no prefix function; an anchored regex is its
  # prefix match.
  conditions {
    display_name = "An image check job failed"
    condition_matched_log {
      filter = "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarochk-\" AND logName:\"run.googleapis.com%2Fvarlog%2Fsystem\" AND severity>=ERROR"
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
