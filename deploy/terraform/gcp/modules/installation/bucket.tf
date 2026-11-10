# The runs bucket. The bootstrap created it; fugaro update imports it. It holds
# runs, caches, locks and build records, so it is never destroyed.
#
# It is VERSIONED, and versioning is the managed audit record's own control
# (docs/no-prompt-policy/reconciliation.md, F15 and T2's ruling of 2026-11-07):
# under D1545 the record of a managed apply is what stands in for the person who
# would have read the plan, and an unversioned bucket made it erasable by
# whoever applied — the record's writer and its eraser were the same identity.
# A versioned bucket turns a deletion into a noncurrent version that only an
# operator holding the object's generation can remove for good, which is the
# hardening D1545 chose (choice (a)). The lifecycle rules below delete noncurrent
# versions under runs/ and cache/ so the bucket does not grow without bound;
# fugaro/ (the marker, the owner's delete list and the audit record) and builds/
# have no noncurrent rule at all, which is the half the record depends on.
resource "google_storage_bucket" "runs" {
  project  = var.project
  name     = var.runs_bucket
  location = upper(var.region)

  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels                      = { fugaro = "managed", fugaro_project = var.fugaro_project }

  versioning {
    enabled = true
  }

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

  # A versioned bucket keeps every deleted object as a noncurrent version, so
  # the two prefixes the rules above delete in volume need one that purges
  # those too — without it the bucket grows for ever. The prefix list is what
  # keeps this rule away from fugaro/ and builds/: the managed audit record and
  # a build record are the two things here that nobody purges.
  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      days_since_noncurrent_time = var.bucket_lifecycle.noncurrent_days
      matches_prefix             = ["runs/", "cache/"]
    }
  }

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
