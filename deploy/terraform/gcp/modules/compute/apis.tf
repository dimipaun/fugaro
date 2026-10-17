# The two APIs this backend needs. Both are OFF on a fresh project (measured
# 2026-10-15, Check 57a step 0a; design §1.9), and enabling Compute has a
# side effect the stage has to expect rather than be surprised by: it creates
# the default network and the default Compute Engine service account.
#
# containerfilesystem.googleapis.com is NOT here, deliberately. It is the
# prerequisite of image STREAMING, and the design takes the preloaded boot
# image instead (C4: the preload measured 66 s against streaming's 97 s, and
# streaming makes Batch ignore the runnable's `options` field — measured — so
# the `--group-add <socket gid>` recipe this backend needs would not reach the
# container). Enabling an API a run never uses is a grant with no reader;
# it stays a prerequisite of the streaming path the design does not build
# (§1.2, §8.1 task 6).
locals {
  apis = toset([
    "compute.googleapis.com",
    "batch.googleapis.com",
  ])
}

resource "google_project_service" "this" {
  for_each = var.manage_apis ? local.apis : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}
