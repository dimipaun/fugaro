# The repository's own image registry. Its build account is its only
# writer, so another repository's leaked build token can't overwrite this
# one's images.
#
# Cleanup deletes versions, not tags, and a keep policy wins over a delete,
# so no version tagged latest (and none of the 3 newest) is ever deleted.
# The build's removal of the candidate- tag after promoting is best-effort
# and normally refused, so the candidate- rule is what clears old candidates
# once they are neither latest nor among the newest.
resource "google_artifact_registry_repository" "images" {
  project       = var.project
  location      = var.region
  repository_id = var.repo.registry.repository_id
  format        = "DOCKER"
  labels = {
    fugaro      = "managed"
    fugaro_repo = var.repo.label
  }

  cleanup_policy_dry_run = var.repo.registry.cleanup_dry_run

  cleanup_policies {
    id     = "keep-latest"
    action = "KEEP"
    condition {
      tag_state    = "TAGGED"
      tag_prefixes = ["latest"]
    }
  }

  cleanup_policies {
    id     = "keep-recent"
    action = "KEEP"
    most_recent_versions {
      keep_count = 3
    }
  }

  cleanup_policies {
    id     = "delete-untagged"
    action = "DELETE"
    condition {
      tag_state  = "UNTAGGED"
      older_than = "${14 * 86400}s"
    }
  }

  cleanup_policies {
    id     = "delete-candidates"
    action = "DELETE"
    condition {
      tag_state    = "TAGGED"
      tag_prefixes = ["candidate-"]
      older_than   = "${2 * 86400}s"
    }
  }

  lifecycle {
    prevent_destroy = true
  }
}

# Operators pull the repository's images, to inspect or smoke them by hand.
resource "google_artifact_registry_repository_iam_member" "operator_images" {
  for_each = toset(var.installation.operators)

  project    = var.project
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.repository_id
  role       = "roles/artifactregistry.reader"
  member     = each.value
}
