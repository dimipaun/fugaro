# One (repository, workflow). The repository module calls this once per
# workflow with its slice of fugaro init --repo's terraform.tfvars.json. The
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
  description = "The region the job runs in."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
  }
}

variable "name" {
  description = "The workflow's name, the value of the job's fugaro_workflow label."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9_-]{1,63}$", var.name))
    error_message = "name must be a label value: 1-63 characters of a-z, 0-9, _ and -."
  }
}

variable "repo_label" {
  description = "The repository's label value, for the job's fugaro_repo label."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9_-]{1,63}$", var.repo_label))
    error_message = "repo_label must be a label value: 1-63 characters of a-z, 0-9, _ and -."
  }
}

variable "workflow" {
  description = "The workflow's job, account and grants: one entry of the tfvars' repo.workflows."
  type = object({
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
    secret_env     = map(string) # env var → logical secret name
    bucket_condition = object({
      title      = string
      expression = string
    })
    vertex     = bool
    deploy_job = bool
  })

  validation {
    condition     = can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.workflow.job))
    error_message = "workflow.job must be a job name: 1-63 characters of a-z, 0-9 and -, starting with a letter."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.workflow.service_account.account_id)) && length(var.workflow.service_account.display_name) > 0
    error_message = "workflow.service_account needs an account ID (6-30 characters of a-z, 0-9 and -, starting with a letter) and a display name."
  }

  validation {
    condition     = var.workflow.task_timeout_s >= 1 && floor(var.workflow.task_timeout_s) == var.workflow.task_timeout_s
    error_message = "workflow.task_timeout_s must be a whole number of seconds, at least 1."
  }

  validation {
    condition     = length(var.workflow.image) > 0 && length(var.workflow.cpu) > 0 && length(var.workflow.memory) > 0
    error_message = "workflow.image, cpu and memory must be set."
  }

  validation {
    condition     = length(var.workflow.bucket_condition.title) > 0 && length(var.workflow.bucket_condition.title) <= 100 && length(var.workflow.bucket_condition.expression) > 0
    error_message = "workflow.bucket_condition needs a title of 1-100 characters and an expression."
  }

  validation {
    condition     = length(setintersection(keys(var.workflow.env), keys(var.workflow.secret_env))) == 0
    error_message = "workflow.env and workflow.secret_env must not set the same variable."
  }
}

variable "secrets" {
  description = "The repository's secret IDs by logical name. The repository module passes its secret resources' IDs, so every grant here waits for its secret."
  type        = map(string)
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

variable "allow_job_delete" {
  description = "Lowers the job's deletion protection. Only an explicit offboarding apply sets it."
  type        = bool
  default     = false
  nullable    = false
}
