# The runs bucket. The bootstrap created it; fugaro init imports it. Every
# setting here matches what the bootstrap set, so adopting it changes
# nothing. It holds runs, caches, locks and build records, so it is never
# destroyed and never versioned (the lifecycle rules delete old runs and
# caches for good).
resource "google_storage_bucket" "runs" {
  project  = var.project
  name     = var.runs_bucket
  location = upper(var.region)

  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels                      = { fugaro = "managed" }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      age            = var.bucket_lifecycle.runs_days
      matches_prefix = ["runs/"]
    }
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      days_since_custom_time = var.bucket_lifecycle.cache_custom_time_days
      matches_prefix         = ["cache/"]
    }
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      age            = var.bucket_lifecycle.cache_age_days
      matches_prefix = ["cache/"]
    }
  }

  # builds/ has no rule: a build record must outlive any age.

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}
