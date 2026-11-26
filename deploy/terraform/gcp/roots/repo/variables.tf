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
    ghcr_registry             = string
    shared_registry           = string
    scheduler_service_account = string
    role_ids = object({
      job_runner      = string
      build_submitter = string
      tag_mover       = string
    })
    launchers = list(string)
    operators = list(string)
    # The installation's credentials service (D146, D147), absent while it has
    # not opted in and while this repository is handed no address at all
    # (infra.CredentialsServiceAddress): its Cloud Run service name, its region
    # and the email of its own account. This root passes it to the workflow
    # module, which grants each job account roles/run.invoker on it, and — with
    # the fact below — drops that job's model-secret accessors and grants the
    # service's account secretAccessor on each secret the job mounts (D2074).
    # `account` is optional so a repository root planned against an
    # installation whose outputs predate it plans what it planned before; the
    # object names it, because an object-typed variable drops an attribute it
    # does not name.
    credentials_service = optional(object({
      name    = string
      region  = string
      account = optional(string, "")
    }))
    # Whether that service serves THIS repository's credentials
    # (infra.CredentialsServiceServes), which is the half every drop is keyed
    # on: false for the one repository handed the address without being served,
    # a `vertex` one, whose runs fetch workflow secrets through the service and
    # give up none of their own credentials (D3326 as amended by F17). Optional
    # and TRUE, so a tfvars written before this input existed — which carried
    # credentials_service only for a repository the switch served — plans
    # exactly what it planned.
    credentials_service_serves = optional(bool, true)
    # The project's catalog check job account (D1148), its email, or "" on an
    # installation whose project publishes no catalog. modules/repo grants it
    # actAs on this repository's build account from here.
    catalog_check_service_account = optional(string, "")
  })
}

variable "repo" {
  type = object({
    name     = string
    provider = string
    slug     = string
    label    = string
    secrets  = map(string)
    # The project-wide secret containers Readiness resolved for this
    # repository (logical name -> gcp.ProjectSecretID). Optional: a
    # repository that shares nothing carries no such key at all, and the
    # module's own default is an empty map. This attribute list is what
    # lets the value through: an object-typed variable drops any key it
    # does not name.
    project_secrets = optional(map(string), {})
    # Whether the published node admits this repository with `proxy: true`
    # (D500, D502), which the module turns into the drop of the job's git
    # credential accessor and mount. Optional and false: an installation that
    # never proxies a repository, and every tfvars written before the fact
    # existed, plans exactly what it planned.
    bitbucket_proxied = optional(bool, false)
    # Which of the model credentials the credentials service serves this
    # repository's runs (D2072, G17): `all`, `anthropic` or `none`, computed by
    # Go (infra.ModelSecretsServed) and passed to every workflow of this
    # repository. Optional and `all`: a tfvars written before the input existed
    # — every golden one — plans exactly what it planned, which is the drop
    # every switched repository's root has made since the opt-in.
    model_secrets_served = optional(string, "all")
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
      token_condition = object({
        title      = string
        expression = string
      })
      vertex     = bool
      deploy_job = bool
      # Whether this is a mixed stage account's own variant job (B0's one mix,
      # docs/mixed-credentials/spec.md §11): Go's WorkflowSpec.StageAccount,
      # which the workflow module reads as local.is_stage_account_token to
      # leave this job's Claude OAuth mount where B2.7's flip takes the model
      # mounts away. Optional and false, so a tfvars written before the fact
      # existed plans exactly what it planned.
      stage_account = optional(bool, false)
      # The workflow's OWN secrets whose values the credentials service fetches
      # for this job (B3.8, S3): Go's WorkflowSpec.VaultSecretNames. The
      # workflow module drops each of them from the job's accessors and mounts
      # and keeps the credentials service's own grant on their containers.
      # Optional and empty, so a tfvars written before the flip existed plans
      # exactly what it planned.
      vault_secret_names = optional(list(string), [])
    }))
  })
}

variable "allow_job_delete" {
  description = "Lowers the jobs' deletion protection, for offboarding (fugaro update --allow-job-delete)."
  type        = bool
  default     = false
  nullable    = false
}

# The GitHub App ID isn't a secret. The jobs and the check get it through
# their env; it is here only so fugaro update can read it back as an output,
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
