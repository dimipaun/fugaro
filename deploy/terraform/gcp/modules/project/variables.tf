# One shared project job (design docs/design/project-centric.md §4.1): the
# installation root calls this module once per (profile, provider, account)
# with its slice of terraform.tfvars.json's project_jobs. The validation
# blocks check shapes only; nothing here derives a name, and nothing here
# reads a repository — a project job serves every repository admitted to its
# profile, so the repository arrives at launch, never at deploy.

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
  description = "The region the job runs in."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
  }
}

variable "job" {
  description = "The job, account and grants of one (profile, provider, account): one entry of the installation tfvars' project_jobs, keyed by its Cloud Run job name."
  type = object({
    profile  = string
    provider = string
    account  = string
    job      = string
    service_account = object({
      account_id   = string
      display_name = string
    })
    image          = string
    cpu            = string
    memory         = string
    task_timeout_s = number
    env            = map(string)
    secret_env     = map(string) # env var → logical secret name
    bucket_condition = object({
      title      = string
      expression = string
    })
    token_condition = object({
      title      = string
      expression = string
    })
    vertex = bool
  })

  validation {
    condition     = can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.job.job))
    error_message = "job.job must be a job name: 1-63 characters of a-z, 0-9 and -, starting with a letter."
  }

  validation {
    condition     = can(regex("^fugaro-p-[a-z0-9-]+$", var.job.job)) && length(var.job.profile) > 0 && length(var.job.provider) > 0
    error_message = "job.job must be a project job name (fugaro-p-<profile>-<provider>-<account>-<hash>), with its profile and provider set."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.job.service_account.account_id)) && length(var.job.service_account.display_name) > 0
    error_message = "job.service_account needs an account ID (6-30 characters of a-z, 0-9 and -, starting with a letter) and a display name."
  }

  validation {
    condition     = var.job.task_timeout_s >= 1 && floor(var.job.task_timeout_s) == var.job.task_timeout_s
    error_message = "job.task_timeout_s must be a whole number of seconds, at least 1."
  }

  validation {
    condition     = length(var.job.image) > 0 && length(var.job.cpu) > 0 && length(var.job.memory) > 0
    error_message = "job.image, cpu and memory must be set."
  }

  validation {
    condition     = length(var.job.bucket_condition.title) > 0 && length(var.job.bucket_condition.title) <= 100 && length(var.job.bucket_condition.expression) > 0
    error_message = "job.bucket_condition needs a title of 1-100 characters and an expression."
  }

  # The job's read-only grant on the runs bucket's budget token objects
  # (gcp.ProjectTokenBucketCondition): the whole of tokens/ of this bucket,
  # in one clause, with no slug — a project job serves every repository
  # admitted to its profile, so the prefix is as narrow as the grant can be,
  # and tokens/ is not one of the job's objectUser prefixes
  # (docs/design/budget-token-channel.md §7.2).
  validation {
    condition = (
      length(var.job.token_condition.title) > 0 && length(var.job.token_condition.title) <= 100 &&
      can(regex("^resource\\.name\\.startsWith\\(\"projects/_/buckets/[a-z0-9._-]+/objects/tokens/\"\\)$", var.job.token_condition.expression))
    )
    error_message = "job.token_condition must be one resource.name.startsWith clause on tokens/ of the runs bucket (gcp.ProjectTokenBucketCondition)."
  }

  validation {
    condition     = length(setintersection(keys(var.job.env), keys(var.job.secret_env))) == 0
    error_message = "job.env and job.secret_env must not set the same variable."
  }

  # The account a job runs as is part of its name, and the name is what the
  # runner matches (there is no parse): a job whose account is not the one it
  # was built for would run every repository admitted to its profile as the
  # wrong Claude account.
  validation {
    condition     = length(var.job.account) == 0 || can(regex("^[a-z](?:[a-z0-9]|-[a-z0-9]){0,30}$", var.job.account))
    error_message = "job.account must be empty (a job that runs as no Claude account) or a Claude account name."
  }
}

variable "secrets" {
  description = "The project-wide secret containers by logical name (secret ID), from the installation module. Every value of job.secret_env must be a key of it."
  type        = map(string)

  validation {
    condition     = alltrue([for name in values(var.job.secret_env) : contains(keys(var.secrets), name)])
    error_message = "every logical name in job.secret_env must be a project-wide secret container of this installation."
  }
}

variable "runs_bucket" {
  description = "The runs bucket's name."
  type        = string

  validation {
    condition     = can(regex("^fugaro-runs-[a-z0-9._-]*[a-z0-9]$", var.runs_bucket)) && length(var.runs_bucket) <= 63
    error_message = "runs_bucket must start with fugaro-runs- and be a bucket name of at most 63 characters."
  }
}

variable "job_runner_role" {
  description = "The installation's fugaroJobRunner role, as its full name projects/<project>/roles/<id>."
  type        = string

  validation {
    condition     = can(regex("^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/roles/[a-zA-Z0-9_.]{3,64}$", var.job_runner_role))
    error_message = "job_runner_role must be a custom role's full name, projects/<project>/roles/<id>."
  }
}

variable "launchers" {
  description = "Members who launch runs; each gets job_runner_role on the job."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.launchers : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "launchers must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "operators" {
  description = "Members who onboard repositories; each may act as the job account, which deploying the job needs."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.operators : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "operators must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "credentials_service" {
  description = "The installation's credentials service (design docs/credentials-service, D146, D147), null when the installation has not opted in: its name, its region and the model secrets it holds, by logical name. With it on, this job's account loses its accessor grant on those secrets — no job account of the installation holds a model credential any more, and the service's own account is the only identity that reads one (D143, Check 94)."
  type = object({
    name    = string
    region  = string
    secrets = list(string)
  })
  default = null
}
