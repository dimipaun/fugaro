# The same inputs as the installation module, which checks their shapes.
# See its variables.tf for what each one means.

variable "project" {
  type = string
}

variable "region" {
  type = string
}

variable "runs_bucket" {
  type = string
}

variable "state_bucket" {
  type = string
}

variable "names" {
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
}

variable "bucket_lifecycle" {
  type = object({
    runs_days              = optional(number, 90)
    cache_custom_time_days = optional(number, 30)
    cache_age_days         = optional(number, 180)
  })
  default  = {}
  nullable = false
}

variable "enable_vertex" {
  type     = bool
  default  = false
  nullable = false
}

variable "manage_apis" {
  type     = bool
  default  = true
  nullable = false
}

variable "launchers" {
  type     = list(string)
  default  = []
  nullable = false
}

variable "operators" {
  type     = list(string)
  default  = []
  nullable = false
}

variable "budget" {
  type = object({
    billing_account = string
    amount          = number
    currency_code   = string
    thresholds      = optional(list(number), [0.5, 0.9, 1.0])
  })
  default = null
}

variable "alert_email" {
  type    = string
  default = null
}

variable "registry_cleanup" {
  type = object({
    enabled        = optional(bool, true)
    dry_run        = optional(bool, true)
    untagged_days  = optional(number, 14)
    candidate_days = optional(number, 2)
    keep_versions  = optional(number, 3)
  })
  default  = {}
  nullable = false
}

variable "adopt_legacy_registry" {
  type     = bool
  default  = false
  nullable = false
}
