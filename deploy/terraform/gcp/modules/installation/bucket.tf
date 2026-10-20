# The runs bucket. The bootstrap created it; fugaro update imports it. Every
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

  # A run's sealed Firebase token object (tokens/<slug>/<run>/budget-token) is
  # read, never deleted, by the job (its grant on tokens/ is read-only), and
  # the token in it is dead after an hour; the object goes after a day.
  # runs/ stays in the rule for the plaintext objects launchers before the
  # seal left there (docs/design/budget-token-channel.md section 7.2).
  dynamic "lifecycle_rule" {
    for_each = var.enable_budget ? [1] : []
    content {
      action {
        type = "Delete"
      }
      condition {
        age            = 1
        matches_prefix = ["runs/", "tokens/"]
        matches_suffix = ["/budget-token"]
      }
    }
  }

  # builds/ has no rule: a build record must outlive any age.

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}

# The project marker. A launcher's own grant can't read a bucket label, so
# every Fugaro command reads this object to check that its project config
# points at the project's own installation. No job account can write it
# (the jobs' grants cover runs/, cache/ and locks/; builds cover builds/),
# and since 0.7.0 no launcher either (bucket-iam.md): only operators, and
# project-level storage writers.
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
