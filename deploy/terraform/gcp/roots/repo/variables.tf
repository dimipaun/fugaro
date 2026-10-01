# The same inputs as the repo module, which checks their shapes. See its
# variables.tf for what each one means.

variable "project" {
  type = string
}

variable "fugaro_project" {
  type = string
}

variable "region" {
  type = string
}

variable "installation" {
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
}

variable "repo" {
  type = object({
    name     = string
    provider = string
    slug     = string
    label    = string
    secrets  = map(string)
    registry = object({
      repository_id   = string
      cleanup_dry_run = bool
    })
    build_service_account = object({
      account_id   = string
      display_name = string
    })
    build_secrets = list(string)
    build_bucket_condition = object({
      title      = string
      expression = string
    })
    check = object({
      job              = string
      image            = string
      scheduler_job    = string
      scheduler_region = string
      schedule         = string
      paused           = bool
      deploy_job       = bool
      env              = map(string)
      secret_env       = map(string)
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
}

variable "allow_job_delete" {
  description = "Lowers the jobs' deletion protection, for offboarding (fugaro init --repo --allow-job-delete)."
  type        = bool
  default     = false
  nullable    = false
}

# The GitHub App ID isn't a secret. The jobs and the check get it through
# their env; it is here only so init --repo can read it back as an output,
# even when every workflow's check is off.
variable "github_app_id" {
  description = "The GitHub App's ID, or null for a Bitbucket repository."
  type        = string
  default     = null

  validation {
    condition     = var.github_app_id == null ? true : can(regex("^[0-9]{1,20}$", var.github_app_id))
    error_message = "github_app_id must be 1-20 digits."
  }
}
