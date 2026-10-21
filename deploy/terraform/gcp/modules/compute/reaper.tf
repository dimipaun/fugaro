# The reaper's invocation (design compute-backend §3.4, §8.1 Task 11): the
# Cloud Run job that runs `fugaro reaper`, and the Scheduler job that starts it
# once a ReapMargin.
#
# Why this file exists at all: internal/backend/batch's Reaper is a library
# with no process of its own. Task 5 built the queue deadline, the stuck-job
# and stuck-DELETION_IN_PROGRESS paths and the instance pass, tested against
# the Batch and Compute fakes -- and until something runs it on a schedule, a
# run whose VM outlives its job leaks, which is the failure §3.4 is about. This
# is the process. It follows modules/installation/history.tf, this repository's
# precedent for a scheduled internal command (a Cloud Run job whose args are
# the command, and a Scheduler job on `*/15` calling it), with one difference
# the compute module forces: that module has a scheduler ACCOUNT of its own and
# this one has none (its only scheduled identity is the preload builder's), so
# the Scheduler job calls the reaper job AS THE REAPER ACCOUNT and the invoker
# grant below names that account. There is no second identity here to be the
# invoker: the account that runs the sweep is the account the schedule runs as.
#
# The cadence is not arbitrary, and `*/15` is not a preference: ReapMargin is
# 15 minutes (internal/backend/batch/reap.go), so a run that should have ended
# is swept at most one margin later. A slower schedule would leave a stuck
# job's VM alive for its own period PLUS the margin, which is the wait the
# margin exists to bound. The same cadence as the installation's own history
# sweep, for the same kind of reason: it is the interval the thing it bounds
# already uses.
#
# What this pass CANNOT do is narrower than what it can reach, and IAM is why:
# the five permissions iam.tf grants it (batch.jobs.list/get/delete,
# compute.instances.list/delete) are project-wide, because IAM cannot express
# "only jobs labelled fugaro_run", so the account can reach every Batch job and
# every VM in the project. What makes that safe is on the other side -- the
# code refuses anything it cannot explain (§3.7, SECURITY.md's reaper
# paragraph: an unlabelled job, a VM whose fugaro_run label does not match a
# run, anything outside this project and region) -- and the account holds
# nothing that can create anything, so a mistake here ends resources rather
# than starting them.
#
# Two executions of this job can overlap -- Cloud Scheduler starts one on its
# tick and an operator can start another by hand, a pass that outran its period
# has the next queued behind it, and nothing here serialises them -- and that
# is harmless by construction, which is worth saying because it is the kind of
# thing a reader assumes a lock is needed for. Every action rests on state the
# pass re-reads at the moment it acts (deleteQueued reads the job back before
# it deletes), the deletes are idempotent (a resource someone else already
# deleted answers 404, which the pass reads as success), and a refusal is not
# an action. Two passes over one project delete the same things once and refuse
# the same things twice (internal/backend/batch's TestReaperIsIdempotent and
# its toleration of a delete of something already gone). A lock would be a
# second thing that can fail, and the failure it would prevent is a second 404.
#
# An installation whose base image predates the release that added `fugaro
# reaper` gets a job that fails on every tick: the CLI's own unknown-command
# message, or a container that cannot start at all if the image carries no
# /usr/local/bin/fugaro. That is the one failure mode of this resource an
# operator meets without a run of their own, and it is reported ONCE per tick
# and not retried inside one (max_retries = 0 on the task, retry_count = 0 on
# the schedule): the log holds one clear line per fifteen minutes rather than a
# storm, and the fix is to move the installation's pin forward. There is no
# check to add before the container runs -- Cloud Run jobs' probes are HTTP,
# TCP and gRPC, with no probe that runs a command -- so the container's own
# start IS the check, and the command is the thing being checked.
#
# The APIs: this is the module's FIRST Cloud Run resource (its only other
# scheduled resource, preload.tf's rebuild, is a Scheduler job), so it is the
# one place the module leans on `run.googleapis.com` -- and on
# `cloudscheduler.googleapis.com`, which preload.tf already leaned on -- being
# enabled in the project. Neither is in this module's apis.tf, which names the
# two APIs of the backend itself (compute and batch) and is deliberate about
# what it adds: this root is applied to the installation's own project, whose
# root enables both (modules/installation/apis.tf), so the dependency is
# satisfied by the installation the module is for. It is written down here
# rather than assumed because apis.tf is not this task's file to change.

locals {
  # The Cloud Run job's name and the Scheduler job's, and they are spelled
  # differently on purpose.
  #
  # The JOB's does not start with `fugaro-`, for the reason
  # internal/infra/firebase.go's HistoryJob gives: `fugaro ls` and max_parallel
  # count the `fugaro-` Cloud Run jobs as a workflow's, and this is not one
  # (check.tf and history.tf make the same choice: fugarochk-, fugarohist, and
  # the installation module validates the history job's name against exactly
  # this).
  #
  # The SCHEDULER job's does, as this module's preload rebuild and the
  # installation's history rollover do (fugaro-preload-rebuild,
  # fugaro-history-rollover): nothing counts a Cloud Scheduler job's name --
  # there is no `fugaro ls` for schedules -- and the prefix is what makes the
  # installation's schedules read as Fugaro's in the console.
  #
  # What a reader greps for on the job's own resources is the label, and it is
  # `fugaro_role = "reaper"` below (beside `fugaro = "managed"`, which every
  # Fugaro resource carries). There is no `fugaro_reaper` label anywhere: the
  # role is a VALUE of fugaro_role.
  reaper_job           = "fugaroreap"
  reaper_scheduler_job = "fugaro-reaper-sweep"
}

# The job that runs the sweep. `args` are the command -- the command the task
# adds to internal/cli, whose own environment path (reaper.go's reaperEnvOptions)
# is what reads the variables below, because a Cloud Run job has no project
# config file, no checkout and no FUGARO_PROJECT to find one with. The first two
# are the whole of the job's configuration, and they are the ones Terraform
# knows: this module is the Batch provisioning, so its project and region ARE
# the installation's Batch configuration (the command refuses to run where it is
# told neither that nor a config's batch: block).
#
# The timeouts a sweeper judges by are NOT here, by construction: they are the
# installation's own task timeouts, which live in its project config and in no
# Terraform resource. A pass that runs from this environment therefore derives
# its bound from a run's own ceiling instead (backend.MaxRunTime), which is the
# same deadline an installation with no batch.task_timeout derives anyway, and
# can only ever be the less patient of the two -- never a deadline a live run
# could still be inside. The two can never disagree in the other direction
# either: localcfg refuses a batch.task_timeout above backend.MaxRunTime, since
# a run can never be given more than the ceiling.
#
# The image is the installation's pinned base image: the same one the preload
# bakes into the boot image, and the one the module already reasons about (its
# host, project and repository are checked against this root's own in
# variables.tf). It is the image that carries `/usr/local/bin/fugaro`
# (images/base/Dockerfile). The reaper account holds no
# artifactregistry.reader, deliberately, and needs none: a Cloud Run job's
# image is pulled by Cloud Run's own infrastructure, not by the account the
# container runs as -- that account decides what the container may do once it
# is running, and this one's five permissions are the reaper's whole surface
# (iam.tf). The installation already runs such a job: modules/installation/
# history.tf's sweep runs the base registry's own history image as an account
# whose only grant is the fugaroHistory custom role, and it pulls. The reader
# grants repo/build.tf gives the build account (build_base, build_ghcr) are the
# build's own, which pulls its FROM images with its own credential, and are no
# precedent for this job either way.
resource "google_cloud_run_v2_job" "reaper" {
  project  = var.project
  location = var.region
  name     = local.reaper_job

  labels = {
    fugaro      = "managed"
    fugaro_role = "reaper"
  }

  # The janitor is not deletable by a hand edit of the tfvars: a plan that
  # destroyed this job would leave leaked VMs with nothing to sweep them, and
  # an operator who means it changes this line (history.tf sets the same).
  deletion_protection = true

  template {
    # Only the template's labels reach the job's log entries; they are the same
    # marks, and `fugaro_role = "reaper"` is what makes the reaper's report
    # findable in Cloud Logging beside the runs it swept (57b-11 measures it).
    labels = {
      fugaro      = "managed"
      fugaro_role = "reaper"
    }
    task_count = 1

    template {
      # A failed pass logs its failure and the next one, a margin later, is the
      # retry: the sweep is idempotent by construction (every deletion rests on
      # the state it re-reads), so a retry adds nothing but a second chance to
      # delete the same thing.
      max_retries = 0
      # Ten minutes, inside the fifteen-minute cadence: a pass that outran its
      # own period would have the next one queued behind it, and there is
      # nothing in a sweep that needs longer (the lists are one page each at
      # any installation's size, and the deletes are answered in seconds).
      timeout         = "600s"
      service_account = google_service_account.reaper.email
      # The second generation execution environment, which every other Cloud Run
      # job in the tree sets (installation/history.tf, project/job.tf,
      # repo/check.tf, workflow/job.tf) and the repo root's own test pins as the
      # intended shape. It is written out here rather than left to the provider's
      # default because the default is the FIRST generation sandbox: this job
      # would then be the only one in the installation running a different
      # runtime from every other job, and the one job that does that is the one
      # that runs unattended, on a schedule, holding delete permissions. An
      # operator's policies and tooling are built around the shape the other
      # jobs have.
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image   = var.base_image
        command = ["/usr/local/bin/fugaro"]
        args    = ["reaper"]

        resources {
          limits = {
            cpu    = "1"
            memory = "512Mi"
          }
        }

        # The job's whole configuration, and one variable more than the history
        # job carries: no credential, because the job's identity is its
        # account, and no FUGARO_PROJECT, because this module is not told the
        # Fugaro project's name and the reaper has no config file to find one
        # with. The three below are always there; the fourth, at the end, only
        # where the installation names projects its sweep must never touch.
        env {
          name  = "FUGARO_GCP_PROJECT"
          value = var.project
        }
        env {
          name  = "FUGARO_REGION"
          value = var.region
        }
        # The marker that says this IS the reaper's own job. The two variables
        # above are the HISTORY job's -- an operator who followed that job's
        # documentation has both in their shell -- so a pass that deleted from
        # them alone would sweep a live project from a command line by
        # accident. internal/cli's environment path (reaperEnvOptions) deletes
        # only where this marker is set, and is a dry run otherwise, so the
        # marker is what makes this environment a configuration a pass may act
        # on. It is a marker and not a credential: its value is a constant, and
        # what it asserts is only "I am the job reaper.tf creates".
        env {
          name  = "FUGARO_REAPER_JOB"
          value = "1"
        }
        # The installation's own never-touch list, when it has one: the projects
        # this sweep refuses BY NAME on top of the ones the engine refuses
        # whatever it is told (internal/backend/batch's outOfBoundsFloor). It is
        # the same variable internal/cli reads for both of its paths, so a list
        # an operator declared for `fugaro reaper` holds for the unattended
        # sweep too -- which is the whole point of wiring it here: a list the
        # job cannot carry is a list that stops holding the moment the sweep
        # runs from the schedule instead of a shell.
        #
        # What makes it safe to take the value from a tfvars is that it can only
        # ADD: the engine puts its own two ids first and refuses them whatever
        # this variable says (outOfBoundsProjects), so nothing an installation
        # writes here takes a refusal off the list. An entry that is not a
        # project id is refused by the variable's own validation rather than
        # dropped, because a project named and not refused is a project swept as
        # if the list did not exist.
        #
        # Absent rather than set to the empty string when the list is empty, so
        # an installation that names none has the same environment it had before
        # this variable existed: internal/cli reads an unset variable and an
        # empty one alike (reaperRefuseProjects), and the three variables above
        # stay the whole of such a job's configuration.
        dynamic "env" {
          for_each = length(var.reaper_refuse_projects) > 0 ? [join(",", var.reaper_refuse_projects)] : []
          content {
            name  = "FUGARO_REAPER_REFUSE_PROJECTS"
            value = env.value
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  # The account's permissions first: a job whose account holds nothing yet
  # would start and be refused by both APIs (history.tf orders the same way).
  depends_on = [google_project_iam_member.reaper]
}

# roles/run.invoker holds run.jobs.run, which is what starting this job takes
# -- on THIS job and no other, and held by the reaper account, which is the
# account the Scheduler job below mints its OAuth token from. `roles/run.invoker`
# and not a job_runner custom role (history.tf's shape): that role exists there
# because the rollover calls `:run` with overrides, which needs
# run.jobs.runWithOverrides; this job is always started with no overrides, so
# the predefined role that holds exactly run.jobs.run is the narrower one.
resource "google_cloud_run_v2_job_iam_member" "reaper_runner" {
  project  = var.project
  location = google_cloud_run_v2_job.reaper.location
  name     = google_cloud_run_v2_job.reaper.name
  role     = "roles/run.invoker"
  member   = google_service_account.reaper.member
}

# Cloud Scheduler mints the OAuth token with which the sweep is started, from
# the reaper account: the scheduler's own agent needs actAs on that account for
# it (the same grant, the same reason, as preload_scheduler_agent above and the
# installation module's own scheduler account).
resource "google_service_account_iam_member" "reaper_scheduler_agent" {
  service_account_id = google_service_account.reaper.name
  role               = "roles/iam.serviceAccountUser"
  member             = local.cloud_scheduler_agent
}

# The installation's operators, who apply this root, and the one thing they
# need on the reaper account: actAs, because the Scheduler job below carries an
# oauth_token block naming that account and Google requires the caller who
# creates such a job to hold actAs on the account the token is minted from
# (cloud.google.com/scheduler/docs/http-target-auth, checked 2026-10-15). It is
# the same role, for the same reason, that the installation module grants its
# operators on its scheduler account (modules/installation/scheduler.tf's
# scheduler_user) and that iam.tf's preload_operator grants them on the preload
# account -- so it is kept rather than narrowed to an actAs-only custom role of
# this module's own.
#
# What it is exactly: the PREDEFINED roles/iam.serviceAccountUser, on THIS ONE
# account and never a role on the project. That role is actAs plus get and list
# on the account, and what an operator can reach through it is bounded by what
# the reaper account itself holds -- iam.tf's fugaroReaper role, the five
# permissions that delete Batch jobs and Compute instances and do nothing else.
# It is not the wider grant iam.tf's preload_operator note describes (actAs on
# an account that can act as every run account and create instances): the
# reaper account can act as nothing and create nothing.
resource "google_service_account_iam_member" "reaper_operator" {
  for_each = var.operators

  service_account_id = google_service_account.reaper.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}

# The schedule: every fifteen minutes, which is ReapMargin -- a run that should
# have ended is swept at most one margin late (this file's header).
#
# Not paused, and that is the one place this differs from the history
# rollover's Scheduler job, which is created paused because its first run
# PRUNES data an operator should look at first. A sweep deletes only what the
# deadlines say is over: a job queued past a run's own ceiling plus the
# launcher's slack, a job Batch itself reports as stuck, and a VM whose run has
# no job at all and whose own age is past the queue deadline plus a whole run
# budget. There is nothing for an operator to review first that 57b-10 and
# 57b-11 do not measure on a sandbox.
#
# The region is the SCHEDULER region, not this root's -- and that is not a
# detail of this file, it is the difference between a sweep that runs and one
# that never exists. Cloud Scheduler does not serve every Cloud Run region, and
# the region this module's own variables block, its tfvars testdata and the
# documented sandbox all use (us-east5) is one it does not serve
# (internal/backend/gcp's SchedulerRegions, names.go's nearestSchedulerRegion,
# docs/gcp-setup.md). A Scheduler job created in a region Scheduler does not
# serve fails the APPLY, so on such an installation this job -- and, before it,
# preload.tf's rebuild, which had the same omission -- would not exist at all,
# and the leak this task closes would be the one nobody sweeps. The URI below
# is unaffected by the split: it names the Cloud Run job's own location, which
# is var.region, and a Scheduler job in another location can start it.
#
# The two are usually the same and the caller resolves the difference
# (var.scheduler_region, which internal/infra/compute.go's ComputeSpec fills
# from the installation's own scheduler_region or gcp.SchedulerRegion), the
# same way the installation's history sweep and the daily image check already
# do.
resource "google_cloud_scheduler_job" "reaper_sweep" {
  project     = var.project
  region      = var.scheduler_region
  name        = local.reaper_scheduler_job
  schedule    = "*/15 * * * *"
  time_zone   = "Etc/UTC"
  description = "Starts the reaper's sweep: the Batch jobs and VMs a run has leaked, deleted a margin after they should have ended (design compute-backend §3.4)."

  # A failed sweep logs its failure; the next one, a margin later, is the
  # retry.
  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.reaper.location}/jobs/${google_cloud_run_v2_job.reaper.name}:run"

    oauth_token {
      service_account_email = google_service_account.reaper.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  # The invoker grant, and the actAs grant it mints the token with, both before
  # the schedule that needs them (check.tf orders the same way).
  depends_on = [
    google_cloud_run_v2_job_iam_member.reaper_runner,
    google_service_account_iam_member.reaper_scheduler_agent,
  ]
}
