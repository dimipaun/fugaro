# Every name and member arrives from fugaro init's terraform.tfvars.json.
# The validation blocks check shapes only; nothing here derives a name. The
# module is the Batch backend's installation-level provisioning (design
# docs/design/compute-backend.md §3.2, §3.7, §8.1 task 6): the two APIs, the
# service accounts and their IAM, the NAT the no-external-IP VMs egress
# through, and the preloaded boot image the runs start from.

variable "project" {
  description = "The project ID."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project))
    error_message = "project must be a project ID: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }
}

variable "region" {
  description = "The installation's region: the jobs', the subnetwork's and the preload builder's."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
  }
}

# Cloud Scheduler is NOT offered in every Cloud Run region, and the two are not
# the same list: us-east5 -- the region this module's own variables block, its
# tfvars testdata and the documented sandbox all use -- is a Cloud Run region
# Cloud Scheduler does not serve (internal/backend/gcp's SchedulerRegions, and
# names.go's nearestSchedulerRegion maps it to us-east4; docs/gcp-setup.md says
# it too). So the module's Scheduler jobs live HERE and not in var.region, and
# their HTTP targets name the Cloud Run job's own location, which stays
# var.region: a Scheduler job can start a job in another region, since its URI
# carries that location. A job created in var.region on such an installation
# fails the apply outright, which is the one failure mode that leaves the
# thing the job exists for undone.
#
# The caller resolves it, because Terraform cannot: fugaro init reads the
# installation's scheduler_region when it names one and
# internal/backend/gcp's SchedulerRegion otherwise -- the same choice the
# history job and the daily image check already make
# (internal/infra/spec.go's check, firebase.go's History) -- and passes it
# here (internal/infra/compute.go's ComputeSpec.SchedulerRegion). It is an
# input rather than a default for that reason, and the shape check below is
# the same one those two make of it.
variable "scheduler_region" {
  description = "Where the module's Cloud Scheduler jobs live: the installation's scheduler_region, or the location Cloud Scheduler serves nearest var.region (internal/backend/gcp's SchedulerRegion). It is not var.region whenever Cloud Scheduler does not serve that region."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.scheduler_region))
    error_message = "scheduler_region must be a Cloud Scheduler location such as us-east4."
  }
}

# Batch REFUSES noExternalIpAddress unless the interface names BOTH a network
# and a subnetwork (measured 2026-10-15, Check 57a; design §1.7, §7.2). That
# is why both are required variables here and why neither has a "default"
# fallback: a NAT provisioned on a network the job is not on is a job with no
# egress, which reads as a Fugaro failure and is a setup error.
variable "network" {
  description = "The VPC network the job's VMs are placed on, in its full form: projects/<project>/global/networks/<network>."
  type        = string

  validation {
    condition     = can(regex("^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/global/networks/[a-zA-Z0-9_-]+$", var.network))
    error_message = "network must be projects/<project>/global/networks/<network>: Batch refuses noExternalIpAddress without an explicit network."
  }
}

variable "subnetwork" {
  description = "The subnetwork those VMs are placed in, in its full form: projects/<project>/regions/<region>/subnetworks/<subnetwork>."
  type        = string

  validation {
    condition     = can(regex("^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/regions/[a-z]+-[a-z]+[0-9]+/subnetworks/[a-zA-Z0-9_-]+$", var.subnetwork))
    error_message = "subnetwork must be projects/<project>/regions/<region>/subnetworks/<subnetwork>: Batch refuses noExternalIpAddress without an explicit subnetwork."
  }
}

variable "names" {
  description = "The Batch backend's singleton names. The module creates these and only these."
  type = object({
    # The prefix every job this backend launches carries. The launcher's
    # jobsEditor Condition is narrowed to it (§3.7).
    job_prefix = string
    # The reaper: the account the scheduled sweeper runs as, and the custom
    # role carrying its five permissions (Task 9: never six -- batch.jobs.cancel
    # is not a permission Google defines).
    reaper_account_id = string
    reaper_role_id    = string
    # The preload builder: the account the scheduled rebuild runs as, and the
    # custom role carrying what the build needs.
    preload_account_id    = string
    preload_role_id       = string
    preload_image         = string
    preload_scheduler_job = string
  })

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.names.reaper_account_id)) && can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.names.preload_account_id))
    error_message = "the account ids must be service account ids: 6-30 characters of a-z, 0-9 and -."
  }

  validation {
    condition     = can(regex("^[a-zA-Z][a-zA-Z0-9_]{2,63}$", var.names.reaper_role_id)) && can(regex("^[a-zA-Z][a-zA-Z0-9_]{2,63}$", var.names.preload_role_id))
    error_message = "the role ids must be custom role ids: 3-64 characters of letters, digits and _, starting with a letter."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]*$", var.names.job_prefix))
    error_message = "job_prefix must be a lowercase job-name prefix."
  }
}

variable "project_number" {
  description = "The project's number. It names Google's own service agents (Cloud Build's, Cloud Scheduler's), which carry no project ID in their addresses."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{6,20}$", var.project_number))
    error_message = "project_number must be a project number."
  }
}

variable "manage_apis" {
  description = "Whether this root enables the two APIs. Off, the project is left to be managed elsewhere (init --skip-apis)."
  type        = bool
  default     = true
}

variable "launchers" {
  description = "The IAM members who may launch a run on this backend: they get Batch Job Editor on Fugaro-shaped jobs only, and actAs on the run-class accounts."
  type        = set(string)

  # People, or the groups that stand for them -- never a whole domain, and
  # never a wildcard. What a launcher is given here is much wider than what a
  # launcher holds on the Cloud Run backend (one job's runWithOverrides, with
  # the image, command and environment fixed by the job): Batch Job Editor on
  # Fugaro-shaped jobs is the power to create a job with ANY image, command
  # and secretVariables, on any machine type, running as any account it holds
  # actAs on, and the condition narrows the job's NAME only (§3.7). So a
  # `domain:example.com` member -- every account in that domain, including
  # ones created after this apply, with no membership list to review -- would
  # hand all of that to the whole domain, which is not what "the people who
  # may launch a run" means. The installation module accepts domain: launchers
  # because its grant is the runs bucket's objectUser; this one is not that
  # grant, so it refuses rather than widening.
  validation {
    condition     = alltrue([for m in var.launchers : can(regex("^(user|group):[^@[:space:]]+@[^@[:space:]]+$", m))])
    error_message = "launchers must be people: user:<email>, or group:<email> for a managed set of them. A domain: member (or a wildcard, or allUsers) would give Batch Job Editor on every Fugaro-shaped job -- and actAs on every run-class account -- to every account it covers."
  }
}

variable "operators" {
  description = "The installation's operators: the members who apply this root. They get actAs on the preload account and nothing else here — creating the rebuild's Scheduler job, whose OAuth token is minted from that account, needs it, and the same grant the installation module makes on its scheduler account."
  type        = set(string)

  # One principal with an address each: a user, a group or a service account,
  # NEVER a domain: member, allUsers or allAuthenticatedUsers. Each value gets
  # actAs on the preload account (iam.tf's preload_operator), and the preload
  # account can act as every run account and create instances, so an operator
  # can run code as a run account through one build and one VM. A domain:
  # member would hand that to every account in the domain -- including ones
  # created after this apply, with no membership list to review -- which is
  # why this is narrower than the installation module's operators (whose
  # grants are not this one) and refuses as the launchers' validation does.
  # infra.ComputeVars refuses the same members before a plan, and the stage's
  # grant rules (tf.Cover) refuse a domain: grant once more. A malformed value
  # is refused here rather than accepted at plan time and rejected at apply.
  validation {
    condition     = alltrue([for m in var.operators : can(regex("^(user|group|serviceAccount):[^@[:space:]]+@[^@[:space:]]+$", m))])
    error_message = "operators must be single principals: user:<email>, group:<email> or serviceAccount:<email>. A domain: member (or allUsers, or allAuthenticatedUsers) would give actAs on the preload account -- which acts as every run-class account and creates instances -- to every account it covers."
  }
}

variable "run_accounts" {
  description = "The run-class service accounts, as IAM members (serviceAccount:<email>): the accounts a job's VMs run as (§3.2). They get the run-class project roles and the base registry reader grant, and the launchers get actAs on them -- on these accounts and no others, since the grant is the cross product of this set with the launchers."
  type        = set(string)

  validation {
    condition     = length(var.run_accounts) > 0
    error_message = "run_accounts must name at least one run-class account: the design forbids the default Compute Engine service account, and with no account here nothing a job runs as could report its state."
  }

  validation {
    condition     = alltrue([for a in var.run_accounts : can(regex("^serviceAccount:[^@[:space:]]+@[^@[:space:]]+$", a))])
    error_message = "run_accounts must be IAM members: serviceAccount:<email> (the email is what a VM's --service-account takes, and the member is what a project role takes)."
  }
}

variable "registry_repository" {
  description = "The Artifact Registry repository the run accounts read the base image from. WHICH repository is the installation's own answer and is read out of the image pin: on 2026-10-15 it was the remote fugaro-ghcr, not the fugaro-base constant (§7.2)."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,62}$", var.registry_repository))
    error_message = "registry_repository must be a repository id."
  }
}

variable "base_image" {
  description = "The installation's pinned base image, the thing the preload bakes into the boot image. Its host, project and repository are the installation's own and are checked against region, project and registry_repository below."
  type        = string

  # The path's last part is an alphabet, not `.+`: this string is interpolated
  # into the builder's startup script inside single quotes (preload.sh's
  # `docker pull '${BASE_IMAGE}'`), and a quote or a `$(...)` in it would end
  # that word and run whatever followed -- on a VM that runs as a run-class
  # account with --scopes=cloud-platform. What an image reference may contain
  # is narrow (lower-case path components, a tag or a digest), so the alphabet
  # below is the whole of it: letters, digits, `.`, `_`, `/`, `@`, `:` and `-`.
  validation {
    condition     = can(regex("^[a-z0-9.-]+-docker\\.pkg\\.dev/[a-z][a-z0-9-]{4,28}[a-z0-9]/[a-z][a-z0-9-]{2,62}/[A-Za-z0-9][A-Za-z0-9._/@:-]*$", var.base_image))
    error_message = "base_image must be a pinned Artifact Registry image path: <region>-docker.pkg.dev/<project>/<repository>/<image>:<version>. The image part may hold only letters, digits, '.', '_', '/', '@', ':' and '-' -- it is interpolated into the builder VM's startup script, so a quote or a shell metacharacter in it would run on that VM."
  }

  # The image the preload pulls must be one a run could pull, which is what
  # makes the three parts of the path this installation's own: the run
  # accounts' reader grant is on registry_repository, in region, in project
  # (iam.tf's run_registry_reader), and the builder VM's
  # docker-credential-gcr is configured for "<region>-docker.pkg.dev" and no
  # other host (preload.tf's REGISTRY_HOST, which was built from var.region
  # before this check existed). A pin whose host, project or repository
  # disagrees with those is refused at plan time instead of failing the build
  # that pulls it, and the message names the three variables rather than a
  # regex.
  validation {
    condition     = can(regex("^${var.region}-docker\\.pkg\\.dev/${var.project}/${var.registry_repository}/", var.base_image))
    error_message = "base_image must be in this installation's own registry: its host must be <region>-docker.pkg.dev for this root's region, its project must be this root's project, and its repository must be registry_repository. The run accounts' reader grant is on that one repository, in that region, in that project (iam.tf), so an image pinned from anywhere else is one no run could pull."
  }
}

variable "base_project" {
  description = "The project the COS base family lives in. It is GOOGLE's project, not the installation's: measured 2026-10-15, batch-cos resolves to batch-custom-image/batch-cos-stable-official, and an installation project has no such family (§7.2)."
  type        = string
  default     = "batch-custom-image"
}

variable "base_family" {
  description = "The COS base image family the preload image is built on."
  type        = string
  default     = "batch-cos-stable-official"
}

variable "preload_zone" {
  description = "The zone the preload builder VM is created in. Batch places a job's VMs in any zone of the region, so this is the build's own zone, not the runs'."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+-[a-z]$", var.preload_zone))
    error_message = "preload_zone must be a zone name such as us-east5-b."
  }
}

variable "builder_machine_type" {
  description = "The machine type the preload builder VM uses. It only pulls an image, so it needs no more than a small general-purpose VM (57a-3b used e2-medium)."
  type        = string
  default     = "e2-medium"
}

variable "preload_schedule" {
  description = "The cron schedule the preload image is rebuilt on. A Batch image's supported lifetime can end 60 days after its starting date, so the image is rebuilt rather than built once (§1.2, C19)."
  type        = string
  default     = "0 3 * * 1"
}

variable "preload_refresh_days" {
  description = "How old the preloaded image may be before a scheduled rebuild replaces it. A Batch custom image's supported lifetime can end 60 days after its starting date, so the window must be inside that (§1.2, C19); the schedule is weekly, so an image is replaced at the first weekly run past the window. The family's name is the pin's hash and does not move when Google's family rolls, so this window -- not the name -- is what keeps an image from outliving its support."
  type        = number
  default     = 30

  validation {
    condition     = var.preload_refresh_days > 0 && var.preload_refresh_days < 60
    error_message = "preload_refresh_days must be between 1 and 59: Batch documents a custom image's supported lifetime as ending 60 days after its starting date, so a longer window would let the image go unsupported before anything replaced it."
  }
}

variable "build_submitter_role" {
  description = "The installation's build-submitter custom role id, which is what lets an account create a Cloud Build (cloudbuild.builds.create/get). The scheduled rebuild's account holds it and nothing else on Cloud Build."
  type        = string
}

variable "reaper_refuse_projects" {
  description = "Further GCP project ids the reaper's scheduled sweep must never touch, on top of the ones it refuses whatever it is told (internal/backend/batch's outOfBoundsFloor). They reach the job's environment as FUGARO_REAPER_REFUSE_PROJECTS (reaper.tf), which is the variable internal/cli reads for both of its paths, so an installation's list is written once and holds whether the sweep runs from the job or from a shell."
  type        = list(string)
  default     = []

  # The module checks the SHAPE only, as it does for every other name here;
  # what the list MEANS is the engine's (internal/backend/batch's
  # outOfBounds), which matches an entry exactly and case-insensitively. A
  # malformed entry is refused rather than dropped, which is the same choice
  # internal/cli's reaperRefuseProjects makes and for the same reason: a
  # project an operator named and the reaper could not read is a project swept
  # as if the list did not exist.
  validation {
    condition     = alltrue([for p in var.reaper_refuse_projects : can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", p))])
    error_message = "reaper_refuse_projects must be GCP project ids: 6-30 characters of a-z, 0-9 and -, starting with a letter and ending with a letter or a digit."
  }
}
