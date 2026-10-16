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
    # role carrying its six permissions.
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
}

variable "operators" {
  description = "The installation's operators: the members who apply this root. They get actAs on the preload account and nothing else here — creating the rebuild's Scheduler job, whose OAuth token is minted from that account, needs it, and the same grant the installation module makes on its scheduler account."
  type        = set(string)
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
  description = "The installation's pinned base image, the thing the preload bakes into the boot image. Its repository is registry_repository."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9.-]+-docker\\.pkg\\.dev/[a-z][a-z0-9-]{4,28}[a-z0-9]/[a-z][a-z0-9-]{2,62}/.+$", var.base_image))
    error_message = "base_image must be a pinned Artifact Registry image path: <region>-docker.pkg.dev/<project>/<repository>/<image>:<version>."
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

variable "build_submitter_role" {
  description = "The installation's build-submitter custom role id, which is what lets an account create a Cloud Build (cloudbuild.builds.create/get). The scheduled rebuild's account holds it and nothing else on Cloud Build."
  type        = string
}
