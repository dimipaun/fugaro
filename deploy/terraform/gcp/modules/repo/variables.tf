# Every name arrives from fugaro init --repo's terraform.tfvars.json, and the
# installation's outputs arrive in it too, under installation. The
# validation blocks check shapes only; nothing here derives a name.

variable "project" {
  description = "The project ID."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project))
    error_message = "project must be a project ID: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }
}

variable "region" {
  description = "The installation's region, for the registry and the jobs."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
  }
}

variable "installation" {
  description = "The installation root's outputs that a repository needs. role_ids are full role names, projects/<project>/roles/<id>."
  type = object({
    runs_bucket               = string
    registry_host             = string
    base_registry             = string
    scheduler_service_account = string
    role_ids = object({
      job_runner      = string
      build_submitter = string
      tag_mover       = string
    })
    launchers = list(string)
    operators = list(string)
  })

  validation {
    condition     = can(regex("^fugaro-runs-[a-z0-9._-]*[a-z0-9]$", var.installation.runs_bucket)) && length(var.installation.runs_bucket) <= 63
    error_message = "installation.runs_bucket must start with fugaro-runs- and be a bucket name of at most 63 characters."
  }

  validation {
    condition     = can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.installation.base_registry))
    error_message = "installation.base_registry must be a registry ID."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z0-9.-]+\\.iam\\.gserviceaccount\\.com$", var.installation.scheduler_service_account))
    error_message = "installation.scheduler_service_account must be a service account email."
  }

  validation {
    condition = alltrue([for r in values(var.installation.role_ids) :
    can(regex("^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/roles/[a-zA-Z0-9_.]{3,64}$", r))])
    error_message = "installation.role_ids must be custom roles' full names, projects/<project>/roles/<id>."
  }

  validation {
    condition = alltrue([for m in concat(var.installation.launchers, var.installation.operators) :
    can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "installation.launchers and operators must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "repo" {
  description = "One repository: its secrets, registry, build account, check job and workflows."
  type = object({
    name     = string
    provider = string
    slug     = string
    label    = string
    secrets  = map(string) # logical name → secret ID
    registry = object({
      repository_id   = string
      cleanup_dry_run = bool
    })
    build_service_account = object({
      account_id   = string
      display_name = string
    })
    build_secrets = list(string) # logical names
    build_bucket_condition = object({
      title      = string
      expression = string
    })
    # null when every workflow has rebuild.check: off.
    check = object({
      job              = string
      image            = string
      scheduler_job    = string
      scheduler_region = string
      schedule         = string
      paused           = bool
      deploy_job       = bool
      env              = map(string)
      secret_env       = map(string) # env var → logical secret name
    })
    workflows = map(object({
      job = string
      service_account = object({
        account_id   = string
        display_name = string
      })
      image          = string
      cpu            = string
      memory         = string
      task_timeout_s = number
      env            = map(string)
      secret_env     = map(string)
      bucket_condition = object({
        title      = string
        expression = string
      })
      vertex     = bool
      deploy_job = bool
    }))
  })

  validation {
    condition     = contains(["bitbucket", "github"], var.repo.provider)
    error_message = "repo.provider must be bitbucket or github."
  }

  validation {
    condition     = alltrue([for v in [var.repo.label, var.repo.slug] : can(regex("^[a-z0-9_-]{1,63}$", v))])
    error_message = "repo.label and repo.slug must be label values: 1-63 characters of a-z, 0-9, _ and -."
  }

  validation {
    condition = alltrue([for k, id in var.repo.secrets :
    can(regex("^[a-z0-9_-]{1,63}$", k)) && can(regex("^[A-Za-z0-9_-]{1,255}$", id))])
    error_message = "repo.secrets must map logical names (label values) to secret IDs (1-255 characters of letters, digits, _ and -)."
  }

  validation {
    condition = alltrue(concat(
      [for s in var.repo.build_secrets : contains(keys(var.repo.secrets), s)],
      [for s in values(try(var.repo.check.secret_env, {})) : contains(keys(var.repo.secrets), s)],
      flatten([for w in values(var.repo.workflows) : [for s in values(w.secret_env) : contains(keys(var.repo.secrets), s)]]),
    ))
    error_message = "every secret that build_secrets, check.secret_env and the workflows' secret_env name must be one of repo.secrets."
  }

  validation {
    condition     = can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.repo.registry.repository_id))
    error_message = "repo.registry.repository_id must be a registry ID: 1-63 characters of a-z, 0-9 and -, starting with a letter."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.repo.build_service_account.account_id)) && length(var.repo.build_service_account.display_name) > 0
    error_message = "repo.build_service_account needs an account ID (6-30 characters of a-z, 0-9 and -, starting with a letter) and a display name."
  }

  validation {
    condition     = length(var.repo.build_bucket_condition.title) > 0 && length(var.repo.build_bucket_condition.title) <= 100 && length(var.repo.build_bucket_condition.expression) > 0
    error_message = "repo.build_bucket_condition needs a title of 1-100 characters and an expression."
  }

  validation {
    condition = var.repo.check == null ? true : (
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.repo.check.job)) &&
      can(regex("^[a-zA-Z0-9_-]{1,500}$", var.repo.check.scheduler_job)) &&
      can(regex("^[a-z]+-[a-z]+[0-9]+$", var.repo.check.scheduler_region)) &&
      can(regex("^[^ ]+ [^ ]+ [^ ]+ [^ ]+ [^ ]+$", var.repo.check.schedule)) &&
      length(var.repo.check.image) > 0
    )
    error_message = "repo.check needs a job name, a Scheduler job name, a scheduler region, a five-field schedule and an image."
  }

  validation {
    condition     = alltrue([for k in keys(var.repo.workflows) : can(regex("^[a-z0-9_-]{1,63}$", k))])
    error_message = "repo.workflows' names must be label values: 1-63 characters of a-z, 0-9, _ and -."
  }
}

variable "allow_job_delete" {
  description = "Lowers the jobs' deletion protection, the check job's included. Only an explicit offboarding apply sets it."
  type        = bool
  default     = false
  nullable    = false
}
