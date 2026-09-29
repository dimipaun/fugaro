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

# Cloud Monitoring allows a log-match condition only alone in its policy,
# so each kind of failure has its own policy, emailing the same channel.
locals {
  image_alerts = {
    # The check's own line for each workflow whose rebuild or check failed.
    # The check logs exactly those at ERROR: a failed check, a failed
    # rebuild it backs off from, and a rebuild it submits after a failed
    # one. Cloud Run takes the line's severity field as the entry's
    # severity.
    check = {
      display_name = "Fugaro image check failed"
      condition    = "An image rebuild or check failed"
      filter       = "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarochk-\" AND jsonPayload.event=\"image-check\" AND severity>=ERROR"
    }
    # A check job execution that failed before it could log its decision,
    # read from the Cloud Run system log (the exclusion keeps it in
    # _Default). The query language has no prefix function; an anchored
    # regex is its prefix match.
    job = {
      display_name = "Fugaro image check job failed"
      condition    = "An image check job failed"
      filter       = "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarochk-\" AND logName:\"run.googleapis.com%2Fvarlog%2Fsystem\" AND severity>=ERROR"
    }
  }
}

resource "google_monitoring_alert_policy" "image" {
  for_each = var.alert_email == null ? {} : local.image_alerts

  project      = var.project
  display_name = each.value.display_name
  combiner     = "OR"

  conditions {
    display_name = each.value.condition
    condition_matched_log {
      filter = each.value.filter
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
