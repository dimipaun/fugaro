# Every name arrives from fugaro update's terraform.tfvars.json, and the
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

variable "fugaro_project" {
  description = "The Fugaro project's name (not the GCP project ID, which is `project`). Every job's FUGARO_PROJECT must equal it, and its FUGARO_GCP_PROJECT must equal `project`."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$", var.fugaro_project))
    error_message = "fugaro_project must be a project name: 1-40 characters of a-z, 0-9 and -, starting and ending with a letter or digit."
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
    # not opted in: its Cloud Run service name, its region and the email of its
    # own account, which the workflow module turns into each job account's
    # roles/run.invoker grant, the drop of that job's model-secret accessors,
    # and the service account's own secretAccessor on each secret the job mounts
    # (D2074, from `account`). This module reads the first two for the build
    # account's own invoker grant (B4.4): one switch, so a build is admitted to
    # the service on exactly the installations whose jobs are.
    credentials_service = optional(object({
      name    = string
      region  = string
      account = optional(string, "")
    }))
    # Whether that service serves THIS repository's credentials
    # (infra.CredentialsServiceServes), which is the half every drop below is
    # keyed on: the job's model-secret accessors and mounts and the build
    # account's own accessor list go, and the service's account is granted each
    # container the jobs still mount. Optional and TRUE, so a tfvars written
    # before this input existed — which carried credentials_service only for a
    # repository the switch served — plans exactly what it planned, and FALSE
    # is the one repository handed the address without being served: a
    # `vertex` one (D3326 as amended by F17), whose runs fetch workflow secrets
    # through the service and give up nothing.
    credentials_service_serves = optional(bool, true)
    # The project's catalog check job account (D1148), its email, or "" on an
    # installation whose project publishes no catalog. It is what
    # catalogcheck.tf grants actAs on this repository's build account to, so a
    # scheduled catalog rebuild of one of this repository's entries runs as
    # this repository and not as the project.
    catalog_check_service_account = optional(string, "")
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
    condition     = can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.installation.ghcr_registry))
    error_message = "installation.ghcr_registry must be a registry ID."
  }

  validation {
    condition     = can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.installation.shared_registry))
    error_message = "installation.shared_registry must be a registry ID."
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
    # The names Readiness resolved to a project-wide container, by logical
    # name (design docs/project-secrets/spec.md §5): each ID is an
    # already-existing secret of the installation root, never one this module
    # creates. Local configs without a project copy of anything send none.
    project_secrets = optional(map(string), {})
    # Whether the published node admits this repository with `proxy: true`
    # (D500, D502): the one condition a job gives up its git credential's
    # accessor and mount under. Optional and false, so a tfvars written before
    # the fact existed — every golden one — plans exactly what it planned, and
    # false is what an installation that never proxies a repository sends.
    bitbucket_proxied = optional(bool, false)
    # Which of the model credentials the credentials service serves this
    # repository's runs (D2072, G17), passed down to every workflow: `all`,
    # `anthropic` or `none`. Optional and `all`, so a tfvars written before the
    # fact existed plans exactly what it planned.
    model_secrets_served = optional(string, "all")
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
      [for s in var.repo.build_secrets : contains(keys(var.repo.secrets), s) || contains(keys(var.repo.project_secrets), s)],
      [for s in values(try(var.repo.check.secret_env, {})) : contains(keys(var.repo.secrets), s) || contains(keys(var.repo.project_secrets), s)],
      flatten([for w in values(var.repo.workflows) : [for s in values(w.secret_env) : contains(keys(var.repo.secrets), s) || contains(keys(var.repo.project_secrets), s)]]),
    ))
    error_message = "every secret that build_secrets, check.secret_env and the workflows' secret_env name must be one of repo.secrets or repo.project_secrets."
  }

  validation {
    condition = alltrue([for k, id in var.repo.project_secrets :
    can(regex("^[a-z0-9_-]{1,63}$", k)) && can(regex("^[A-Za-z0-9_-]{1,255}$", id))])
    error_message = "repo.project_secrets must map logical names (label values) to secret IDs (1-255 characters of letters, digits, _ and -)."
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
