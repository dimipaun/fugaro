# The preloaded boot image, and its rebuild (design §1.2, C4, C19).
#
# The image itself is built by preload.sh and NOT managed here, deliberately:
# a Batch image's build is "boot a VM, pull into its Docker data root, stop
# it, image its disk", and no Terraform resource expresses the wait in the
# middle of that — an image created from a disk whose pull has not finished
# is an image with nothing in it, and the runs would silently pay the pull
# the preload exists to remove. So this module owns the PIPELINE and the
# account it runs as, and the build is a script with a marker on the serial
# port to wait for (57a-3b's procedure, measured).
#
# The name carries a hash of the installation's pinned base image, so the
# boot image is the one that contains the image a run pulls: one image per
# pinned version. A new pin makes a new name, and the build deletes the
# older images of this module's own prefix and label.
locals {
  preload_prefix = var.names.preload_image
  preload_image  = "${local.preload_prefix}-${substr(sha256(var.base_image), 0, 8)}"

  # The build's own spec. It names the builder account and the Cloud Build
  # regions the installation is in, and it is one step: a script, not a
  # series of inline commands, because the procedure has ordering in it.
  preload_build = {
    serviceAccount = local.preload_email
    timeout        = "1800s"
    options        = { logging = "CLOUD_LOGGING" }
    steps = [{
      name       = "gcr.io/google.com/cloudsdktool/cloud-sdk:slim"
      entrypoint = "bash"
      args       = ["-c", file("${path.module}/preload.sh")]
      env = [
        "PROJECT=${var.project}",
        "ZONE=${var.preload_zone}",
        "IMAGE=${local.preload_image}",
        "IMAGE_PREFIX=${local.preload_prefix}",
        "BASE_IMAGE=${var.base_image}",
        "BASE_PROJECT=${var.base_project}",
        "BASE_FAMILY=${var.base_family}",
        "REGISTRY_HOST=${var.region}-docker.pkg.dev",
        "RUN_ACCOUNT=${sort(tolist(var.run_accounts))[0]}",
        "MACHINE_TYPE=${var.builder_machine_type}",
      ]
    }]
  }
}

# The rebuild, on a schedule. It calls the Cloud Build API as the preload
# account, which is what the build-submitter role it holds is for, and the
# API is Google's own: there is no trigger to host and no repository to
# connect. Cloud Scheduler retries a failed call itself, and the script is
# idempotent, so a rebuild of an image that is already there costs one API
# call and nothing else.
resource "google_cloud_scheduler_job" "preload_rebuild" {
  project   = var.project
  name      = var.names.preload_scheduler_job
  region    = var.region
  schedule  = var.preload_schedule
  time_zone = "Etc/UTC"
  description = "Rebuilds fugaro's preloaded Batch boot image: a Batch image's supported lifetime can end 60 days after its starting date (design compute-backend §1.2)."

  http_target {
    http_method = "POST"
    uri         = "https://cloudbuild.googleapis.com/v1/projects/${var.project}/builds"
    body        = base64encode(jsonencode(local.preload_build))

    headers = {
      "Content-Type" = "application/json"
    }

    oauth_token {
      service_account_email = local.preload_email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [
    google_project_service.this,
    google_project_iam_member.preload_build_submitter,
    google_service_account_iam_member.preload_scheduler_agent,
  ]
}
