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
  labels                      = { fugaro = "managed", fugaro_project = var.fugaro_project }

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

# The project marker. Launchers hold objectAdmin on the runs bucket, which
# can't read a bucket label, so every Fugaro command reads this object to
# check that its project config points at the project's own installation.
# No job account can write it (the jobs' grants cover runs/, cache/ and
# locks/; builds cover builds/). It is a safety label, not a boundary.
resource "google_storage_bucket_object" "project_marker" {
  bucket       = google_storage_bucket.runs.name
  name         = "fugaro/project.json"
  content_type = "application/json"
  content = jsonencode({
    version     = 1
    name        = var.fugaro_project
    gcp_project = var.project
  })
}
