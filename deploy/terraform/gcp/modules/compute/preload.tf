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
# The name carries a hash of the installation's pinned base image, and it
# names an image FAMILY rather than one image: the runs boot
# projects/<project>/global/images/family/<this name> (Task 7's job
# allocation policy), and every build makes a new GENERATION inside that
# family. That is what makes a rebuild safe -- a family keeps serving its
# previous generation until the new one exists, and switches over the moment
# it does, so no build ever leaves the installation without an image
# (cloud.google.com/compute/docs/images/image-families-best-practices,
# checked 2026-10-16; preload.sh). A new pin is a new family, and the build
# deletes the older generations of this module's own prefix and label, and the
# whole family of a pin that has moved on.
#
# What the name CANNOT carry is Google's side of it: the pin does not move
# when the base family rolls, so the release a generation was built from is
# recorded in its base-release label (preload.sh) and the rebuild reads it back
# together with the generation's age -- one that is current and inside
# preload_refresh_days is the only one it leaves alone, which is what the
# schedule is for (design §1.2, C19: a Batch image's supported lifetime can
# end 60 days after its starting date).
locals {
  preload_prefix = var.names.preload_image
  preload_family = "${local.preload_prefix}-${substr(sha256(var.base_image), 0, 8)}"

  # The label every generation this build makes records its socket gid under,
  # and the one name for it on this side: the build's substitution below and
  # the output the stage reads back are both this local, so the writer and the
  # reader cannot drift. The VALUE is not here and cannot be: the gid belongs
  # to the boot image the build makes, out of band, so the build reads it off
  # the builder VM and labels the image with it (preload.sh), and a rebuild
  # records it again (design §2.2, C2: a run reaches the socket through
  # `--group-add <gid>`, so a rebuilt image whose gid moved leaves a recorded
  # one stale).
  socket_gid_label = "socket-gid"

  # The script, as Cloud Build will read it. The build config is
  # substitution-processed BEFORE any step runs: every `$VAR` in it is read as
  # a substitution, and one that is not a built-in makes the API reject the
  # whole build with INVALID_ARGUMENT -- while `$$` is the literal `$`
  # (cloud.google.com/build/docs/configuring-builds/substitute-variable-values,
  # checked 2026-10-16: "To use an expression starting with $ you must use
  # $$", and `$FOO` is invalid since it is not a built-in substitution).
  # preload.sh is shell, so it is full of $PROJECT, ${...:?} and $(...) that
  # are NOT Cloud Build's; every `$` is therefore doubled here, and the step
  # receives the script unchanged. Without this the build never starts.
  preload_script = replace(file("${path.module}/preload.sh"), "$", "$$")

  # The build's own spec. It names the builder account and the Cloud Build
  # regions the installation is in, and it is one step: a script, not a
  # series of inline commands, because the procedure has ordering in it.
  preload_build = {
    serviceAccount = local.preload_email
    timeout        = "1800s"
    # The socket gid's label name, as a BUILD SUBSTITUTION rather than a value
    # written into the step: user-defined substitutions (a leading `_`) are
    # what the build config may name, and one that is declared here and used
    # below is substituted before any step runs, exactly as the built-ins are
    # (cloud.google.com/build/docs/configuring-builds/substitute-variable-values,
    # checked 2026-10-16). A `$NAME` that is not declared makes the API reject
    # the whole build (INVALID_ARGUMENT), which is why this local is the only
    # place the name is written.
    substitutions = { _SOCKET_GID_LABEL = local.socket_gid_label }
    # CLOUD_LOGGING_ONLY, not CLOUD_LOGGING: with a user-specified service
    # account the build's logs must go to a Cloud Storage bucket or a Cloud
    # Logging bucket in the project, and the value that names the latter is
    # CLOUD_LOGGING_ONLY. CLOUD_LOGGING is not a value of this enum at all
    # (cloud.google.com/build/docs/securing-builds/configure-user-specified-service-accounts,
    # checked 2026-10-16); the preload account holds roles/logging.logWriter
    # (iam.tf), which is what writing them takes.
    options = { logging = "CLOUD_LOGGING_ONLY" }
    steps = [{
      name       = "gcr.io/google.com/cloudsdktool/cloud-sdk:slim"
      entrypoint = "bash"
      args       = ["-c", local.preload_script]
      env = [
        "PROJECT=${var.project}",
        "ZONE=${var.preload_zone}",
        # The FAMILY the runs boot, not an image name: the name of a
        # generation inside it is the build's own business (preload.sh).
        "IMAGE_FAMILY=${local.preload_family}",
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
        # The host REGISTRY_HOST is checked against in variables.tf: it is the
        # region's, and a base_image pinned from another region's host is
        # refused there rather than pulled here.
        "REGISTRY_HOST=${var.region}-docker.pkg.dev",
        # The builder VM's own identity: a run-class account, whose reader
        # grant lets the pull happen. It is passed as the EMAIL a VM's
        # --service-account takes; run_accounts holds IAM members.
        "RUN_ACCOUNT=${trimprefix(sort(tolist(var.run_accounts))[0], "serviceAccount:")}",
        "MACHINE_TYPE=${var.builder_machine_type}",
        # The label the image is labelled with, from the substitution above.
        # preload.sh refuses to run without it and never spells it itself.
        # `$${` is HCL's escape: terraform writes the literal
        # `${_SOCKET_GID_LABEL}` into the build config, which is what Cloud
        # Build substitutes -- `${` alone would be terraform reading a variable
        # that does not exist (terraform validate refuses it).
        "SOCKET_GID_LABEL=$${_SOCKET_GID_LABEL}",
      ]
    }]
  }
}

# The rebuild, on a schedule. It calls the Cloud Build API as the preload
# account, which is what the build-submitter role it holds is for, and the
# API is Google's own: there is no trigger to host and no repository to
# connect. The call itself is retried (the schedule is weekly, and a rebuild
# that failed on a transient -- a pull that never finished, an API error --
# would otherwise wait a week for its next attempt), and the script decides
# what to do from the family the API describes: a newest generation that is
# current and inside preload_refresh_days costs one API call and nothing
# else, and anything else is built beside it, in the same family.
resource "google_cloud_scheduler_job" "preload_rebuild" {
  project     = var.project
  name        = var.names.preload_scheduler_job
  # The scheduler region, not the installation's: Cloud Scheduler does not
  # serve every Cloud Run region, and this module's documented installation
  # (us-east5) is one it does not. The call below is to a global API
  # (cloudbuild.googleapis.com), so nothing here names a location and the job
  # is complete wherever it lives.
  region      = var.scheduler_region
  schedule    = var.preload_schedule
  time_zone   = "Etc/UTC"
  description = "Rebuilds fugaro's preloaded Batch boot image: a Batch image's supported lifetime can end 60 days after its starting date (design compute-backend §1.2)."

  # A build that could not finish -- a pull that never printed its marker, an
  # API error mid-rebuild -- leaves the family's previous generation serving,
  # so retrying it within the hour is safe and is what keeps the image inside
  # its window.
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
