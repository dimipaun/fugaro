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
# older images of this module's own prefix and label. What the name CANNOT
# carry is Google's side of it: the pin does not move when the base family
# rolls, so the release an image was built from is recorded in its
# base-release label (preload.sh) and the rebuild reads it back together with
# the image's age -- an image that is current and inside
# preload_refresh_days is the only one it leaves alone, which is what the
# schedule is for (design §1.2, C19: a Batch image's supported lifetime can
# end 60 days after its starting date).
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
        # How old the image this build owns may be before the build replaces
        # it, in days. It is the one number that keeps the image inside the
        # 60-day lifetime Batch documents; the family roll is the other reason
        # a rebuild becomes a replace.
        "REFRESH_DAYS=${var.preload_refresh_days}",

        # The builder VM is placed on the same network and subnetwork as the
        # job's VMs, and on no other: the NAT that gives a no-external-IP VM
        # its egress is on those two, so a builder placed anywhere else would
        # be a VM with no egress at all.
        "NETWORK=${var.network}",
        "SUBNETWORK=${var.subnetwork}",
        "REGISTRY_HOST=${var.region}-docker.pkg.dev",
        # The builder VM's own identity: a run-class account, whose reader
        # grant lets the pull happen. It is passed as the EMAIL a VM's
        # --service-account takes; run_accounts holds IAM members.
        "RUN_ACCOUNT=${trimprefix(sort(tolist(var.run_accounts))[0], "serviceAccount:")}",
        "MACHINE_TYPE=${var.builder_machine_type}",
      ]
    }]
  }
}

# The rebuild, on a schedule. It calls the Cloud Build API as the preload
# account, which is what the build-submitter role it holds is for, and the
# API is Google's own: there is no trigger to host and no repository to
# connect. The call itself is retried (the schedule is weekly, and a rebuild
# that could not replace an image a run's VM still held would otherwise wait
# a week for its next attempt), and the script decides what to do from the
# image the API describes: an image that is already there, current and inside
# preload_refresh_days costs one API call and nothing else, and anything else
# is built and replaces it.
resource "google_cloud_scheduler_job" "preload_rebuild" {
  project     = var.project
  name        = var.names.preload_scheduler_job
  region      = var.region
  schedule    = var.preload_schedule
  time_zone   = "Etc/UTC"
  description = "Rebuilds fugaro's preloaded Batch boot image: a Batch image's supported lifetime can end 60 days after its starting date (design compute-backend §1.2)."

  # A build that could not finish -- a pull that never printed its marker, an
  # image a run's VM still holds -- leaves what is there in place, so retrying
  # it within the hour is safe and is what keeps the image inside its window.
  retry_config {
    retry_count          = 3
    min_backoff_duration = "600s"
    max_backoff_duration = "3600s"
  }

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
