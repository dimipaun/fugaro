# The legacy registry holds the images the bootstrap built. It is declared
# only when fugaro update adopts one, so a fresh project never gets an empty
# one. Nothing writes it after migration, and it has no cleanup policy: its
# images are what a rollback runs.
resource "google_artifact_registry_repository" "legacy" {
  count = var.adopt_legacy_registry ? 1 : 0

  project       = var.project
  location      = var.region
  repository_id = var.names.legacy_registry
  format        = "DOCKER"
  labels        = { fugaro = "managed" }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}

# The base images, which only operators push. Cleanup deletes versions, not
# tags, and a keep policy wins over a delete, so no version tagged latest or
# dev-* (and none of the newest few) can be deleted.
resource "google_artifact_registry_repository" "base" {
  project       = var.project
  location      = var.region
  repository_id = var.names.base_registry
  format        = "DOCKER"
  labels        = { fugaro = "managed" }

  cleanup_policy_dry_run = var.registry_cleanup.dry_run

  dynamic "cleanup_policies" {
    for_each = var.registry_cleanup.enabled ? [1] : []
    content {
      id     = "keep-tagged"
      action = "KEEP"
      condition {
        tag_state    = "TAGGED"
        tag_prefixes = ["dev-", "latest"]
      }
    }
  }

  dynamic "cleanup_policies" {
    for_each = var.registry_cleanup.enabled ? [1] : []
    content {
      id     = "keep-recent"
      action = "KEEP"
      most_recent_versions {
        keep_count = var.registry_cleanup.keep_versions
      }
    }
  }

  dynamic "cleanup_policies" {
    for_each = var.registry_cleanup.enabled ? [1] : []
    content {
      id     = "delete-untagged"
      action = "DELETE"
      condition {
        tag_state  = "UNTAGGED"
        older_than = "${var.registry_cleanup.untagged_days * 86400}s"
      }
    }
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}

# The public Fugaro images from ghcr.io, cached for this installation's
# builds and jobs. It is a remote (pull-through) repository, not a copy:
# nothing writes it, the packages are public so it is unauthenticated, and
# it carries no cleanup policies — an eviction during an upstream outage
# would turn a warm pull into a 504 (design registry-remote-repository.md
# §2.3, §7). Upstream validation is left at its default (on), so a typo'd
# upstream URI fails the apply rather than a job's first pull (§2.3).
resource "google_artifact_registry_repository" "ghcr" {
  project       = var.project
  location      = var.region
  repository_id = var.names.ghcr_registry
  description   = "Public Fugaro images from ghcr.io, cached for this installation's builds and jobs"
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"
  labels        = { fugaro = "managed" }
  remote_repository_config {
    description = "ghcr.io"
    docker_repository {
      custom_repository {
        uri = "https://ghcr.io"
      }
    }
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}
