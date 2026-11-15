# The runs bucket. The bootstrap created it; fugaro update imports it. It holds
# runs, caches, locks and build records, so it is never destroyed.
#
# It is VERSIONED, and versioning is the managed audit record's own control
# (docs/no-prompt-policy/reconciliation.md, F15 and T2's ruling of 2026-11-07):
# under D1545 the record stands in for the person who would have read the plan,
# and an unversioned bucket let whoever applied erase it in place. Versioning
# turns a deletion into a noncurrent version — the hardening D1545 chose, choice
# (a) — but it does not separate the writer from the eraser: the apply runs as
# the operator, who holds objectAdmin here and can delete every generation. What
# it buys is that erasure needs that role. The lifecycle rules below delete
# noncurrent versions under every prefix the rules above empty — runs/, cache/,
# locks/, tokens/, issues/ and reports/ — so the bucket does not grow without
# bound, while fugaro/ and builds/ have none: that is the half the record
# depends on.
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

  # The stored reports (slice P, ruling E3): a report of a repository and a
  # period, and the project's year roll-up beside them, are kept 18 months —
  # long enough that the policy review reads a whole year back and the one
  # before it — and then deleted. reports_days is internal/infra's 548, in
  # whole days as this variable's other members are. The age is from creation
  # and not from a custom time: the job writes each report once and overwrites
  # it with the same content on a rerun, so a report cannot be kept alive by
  # being rewritten.
  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      age            = var.bucket_lifecycle.reports_days
      matches_prefix = ["reports/"]
    }
  }

  # builds/ has no rule: a build record must outlive any age.

  # A versioned bucket keeps every deleted object as a noncurrent version, so
  # every prefix the rules above delete in has to be purged too — without one the
  # bucket grows for ever, and a report the 18-month rule deleted would stay
  # readable as the version it left. That is runs/, cache/ and reports/ by age,
  # and locks/, tokens/ and issues/, which a run's lock release, a token's
  # expiry and a closed issue empty. fugaro/ and builds/ stay out: the record
  # and a build are never purged.
  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      days_since_noncurrent_time = var.bucket_lifecycle.noncurrent_days
      matches_prefix             = ["runs/", "cache/", "locks/", "tokens/", "issues/", "reports/"]
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
