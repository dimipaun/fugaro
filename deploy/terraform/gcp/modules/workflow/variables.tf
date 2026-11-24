# One (repository, workflow). The repository module calls this once per
# workflow with its slice of fugaro update's terraform.tfvars.json. The
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
    token_condition = object({
      title      = string
      expression = string
    })
    vertex     = bool
    deploy_job = bool
    # Whether this is a mixed stage account's own variant job (B0's one mix,
    # docs/mixed-credentials/spec.md §11): Go's WorkflowSpec.StageAccount,
    # the one thing that exempts this job's Claude OAuth mount from the
    # credentials service's drop (local.is_stage_account_token, B2.7).
    # Optional and false, so a tfvars written before the fact existed plans
    # exactly what it planned.
    stage_account = optional(bool, false)
    # The workflow's OWN secrets whose values the credentials service fetches
    # for this job (B3.8, S3): Go's WorkflowSpec.VaultSecretNames. Each is
    # dropped from this job's accessors and mounts and added to the service's
    # own set, which is what makes the run's fetch possible. Optional and
    # empty, so a tfvars written before the flip existed plans exactly what it
    # planned.
    vault_secret_names = optional(list(string), [])
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

  # The job's read-only grant on its own repository's budget token objects
  # (gcp.TokenBucketCondition): exactly one tokens/<slug>/ clause, so the
  # grant can never be widened past tokens/ or to another prefix, and the slug
  # is the one the job's own runs/ condition names, so a job never reads
  # another repository's tokens (docs/budget-token-channel/spec.md §7.2).
  validation {
    condition = (
      length(var.workflow.token_condition.title) > 0 && length(var.workflow.token_condition.title) <= 100 &&
      can(regex("^resource\\.name\\.startsWith\\(\"projects/_/buckets/[a-z0-9._-]+/objects/tokens/[a-z0-9][a-z0-9_-]*/\"\\)$", var.workflow.token_condition.expression)) &&
      try(regex("/objects/tokens/([a-z0-9][a-z0-9_-]*)/", var.workflow.token_condition.expression)[0] == regex("/objects/runs/([a-z0-9][a-z0-9_-]*)/", var.workflow.bucket_condition.expression)[0], false)
    )
    error_message = "workflow.token_condition must be one resource.name.startsWith clause on tokens/<slug>/ of the runs bucket, for the slug the job's own runs/ condition names (gcp.TokenBucketCondition)."
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

variable "credentials_service" {
  description = "The installation's credentials service (D146, D147): its Cloud Run service name, its region and the email of its own account, or null while the installation has not opted in. Null is what keeps the job's model-secret accessors and creates no invoker grant; set, it is what drops them (the service holds those credentials), grants the job's account roles/run.invoker on it (D135) and grants the service's account a secretAccessor on each secret the job mounts (D2074) — that last one only when account is set, which it is not in an installation applied before this input existed."
  type = object({
    name   = string
    region = string
    # The service's own account (D2074). Empty, not required: an installation
    # whose outputs predate this input plans no new grant, exactly as it did
    # before, and the next `fugaro update` fills it in.
    account = optional(string, "")
  })
  default = null

  validation {
    condition = var.credentials_service == null ? true : (
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.credentials_service.name)) &&
      can(regex("^[a-z]+-[a-z]+[0-9]+$", var.credentials_service.region)) &&
      (var.credentials_service.account == "" || can(regex("^[a-z0-9-]+@[a-z0-9-]+\\.iam\\.gserviceaccount\\.com$", var.credentials_service.account)))
    )
    error_message = "credentials_service needs a Cloud Run service name, a region such as us-east5, and, when it carries one, its account as an email (name@project.iam.gserviceaccount.com)."
  }
}

variable "bitbucket_proxied" {
  description = "Whether the published node admits this repository with `proxy: true` (D500, D502), which is the one condition a git credential leaves the job under: a proxied repository's job gives up the secretAccessor on and the mount of every name in proxied_git_secret_names (sa.tf). False for every other repository, of an installation that opted in or not, which keeps both (G9, G15)."
  type        = bool
  default     = false
  nullable    = false
}

variable "model_secrets_served" {
  description = "Which of the model credentials the credentials service serves THIS repository's runs (D2072, G17), which is what the job's account keeps its secretAccessor on: `all` for a cascade repository the switch admits, `anthropic` for an `api-key` or `oauth` one it admits and the service holds the credential of, `none` for every other. Go writes it per repository (infra.ModelSecretsServed, RepoSpec.ModelSecretsServed), and the default is `all` because that is what a tfvars written before the input existed means: every root applied then planned with the model clauses of the drop in force, and only a switched repository has them read at all."
  type        = string
  default     = "all"
  nullable    = false

  validation {
    condition     = contains(["all", "anthropic", "none"], var.model_secrets_served)
    error_message = "model_secrets_served must be one of \"all\", \"anthropic\" or \"none\" (config.ModelSecretsServedValues)."
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
