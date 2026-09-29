# Every name arrives from fugaro init's terraform.tfvars.json. The
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
  description = "The installation's region, for the runs bucket, the registries and the jobs."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
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

variable "state_bucket" {
  description = "The Terraform state bucket, which fugaro init creates and Terraform doesn't manage. Operators get objectAdmin on it."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$", var.state_bucket))
    error_message = "state_bucket must be a bucket name."
  }
}

variable "names" {
  description = "The installation's singleton names."
  type = object({
    legacy_registry              = string
    base_registry                = string
    scheduler_service_account_id = string
    role_ids = object({
      launcher        = string
      job_runner      = string
      build_submitter = string
    })
    log = object({
      bucket    = string
      view      = string
      sink      = string
      exclusion = string
    })
  })

  validation {
    condition = alltrue([for id in [var.names.legacy_registry, var.names.base_registry] :
    can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", id))])
    error_message = "names.legacy_registry and names.base_registry must be registry IDs: 1-63 characters of a-z, 0-9 and -, starting with a letter."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.names.scheduler_service_account_id))
    error_message = "names.scheduler_service_account_id must be a service account ID: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }

  validation {
    condition = alltrue([for id in values(var.names.role_ids) :
    can(regex("^[a-zA-Z0-9_.]{3,64}$", id))])
    error_message = "names.role_ids must be custom role IDs: 3-64 characters of letters, digits, _ and ."
  }

  validation {
    condition = alltrue([for id in values(var.names.log) :
    can(regex("^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$", id))])
    error_message = "names.log must hold log bucket, view, sink and exclusion IDs: at most 100 characters of letters, digits, _, . and -."
  }
}

variable "bucket_lifecycle" {
  description = "The runs bucket's lifecycle, in days: the same three rules the bootstrap set."
  type = object({
    runs_days              = optional(number, 90)
    cache_custom_time_days = optional(number, 30)
    cache_age_days         = optional(number, 180)
  })
  default  = {}
  nullable = false

  validation {
    condition     = alltrue([for d in values(var.bucket_lifecycle) : d >= 1 && floor(d) == d])
    error_message = "bucket_lifecycle's days must be whole numbers of at least 1."
  }
}

variable "enable_vertex" {
  description = "Whether to enable the Vertex AI API, for workflows with agent.auth: vertex."
  type        = bool
  default     = false
  nullable    = false
}

variable "manage_apis" {
  description = "Whether to enable the APIs Fugaro uses. Set it to false when the project's APIs are managed elsewhere."
  type        = bool
  default     = true
  nullable    = false
}

variable "launchers" {
  description = "Members who launch and watch runs, such as user:someone@example.com."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.launchers : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "launchers must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "operators" {
  description = "Members who onboard repositories, such as user:someone@example.com."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.operators : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "operators must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "budget" {
  description = "An optional budget on the project, which needs billing-account permissions. thresholds are fractions of amount."
  type = object({
    billing_account = string
    amount          = number
    currency_code   = string
    thresholds      = optional(list(number), [0.5, 0.9, 1.0])
  })
  default = null

  validation {
    condition = var.budget == null ? true : (
      can(regex("^[0-9A-F]{6}-[0-9A-F]{6}-[0-9A-F]{6}$", var.budget.billing_account)) &&
      var.budget.amount > 0 && floor(var.budget.amount) == var.budget.amount &&
      can(regex("^[A-Z]{3}$", var.budget.currency_code)) &&
      length(var.budget.thresholds) > 0 &&
      alltrue([for t in var.budget.thresholds : t > 0])
    )
    error_message = "budget needs a billing account ID (XXXXXX-XXXXXX-XXXXXX), a whole amount above 0, a 3-letter currency code, and thresholds above 0."
  }
}

variable "alert_email" {
  description = "An optional address to email when an image rebuild or check fails."
  type        = string
  default     = null

  validation {
    condition     = var.alert_email == null ? true : can(regex("^[^@ ]+@[^@ ]+\\.[^@ ]+$", var.alert_email))
    error_message = "alert_email must be an email address."
  }
}

variable "registry_cleanup" {
  description = "Artifact Registry cleanup. dry_run only logs what would be deleted; turn it off once the audit logs show no latest or dev- version would go. It drives every repository's registry too."
  type = object({
    enabled        = optional(bool, true)
    dry_run        = optional(bool, true)
    untagged_days  = optional(number, 14)
    candidate_days = optional(number, 2)
    keep_versions  = optional(number, 3)
  })
  default  = {}
  nullable = false

  validation {
    condition = alltrue([for n in [var.registry_cleanup.untagged_days, var.registry_cleanup.candidate_days, var.registry_cleanup.keep_versions] :
    n >= 1 && floor(n) == n])
    error_message = "registry_cleanup's days and keep_versions must be whole numbers of at least 1."
  }
}

variable "adopt_legacy_registry" {
  description = "Whether to adopt the bootstrap's legacy registry. fugaro init sets it when it finds one carrying the fugaro=managed mark."
  type        = bool
  default     = false
  nullable    = false
}
