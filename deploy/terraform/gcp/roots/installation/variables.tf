# The same inputs as the installation module, which checks their shapes.
# See its variables.tf for what each one means.

variable "project" {
  type = string
}

variable "fugaro_project" {
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
    ghcr_registry                = string
    scheduler_service_account_id = string
    role_ids = object({
      launcher        = string
      job_runner      = string
      build_submitter = string
      tag_mover       = string
    })
    log = object({
      bucket    = string
      view      = string
      sink      = string
      exclusion = string
    })
  })
}

variable "log_bucket_description" {
  type = string
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

variable "launcher_bucket_condition" {
  type = object({
    title      = string
    expression = string
  })
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
    enabled       = optional(bool, true)
    dry_run       = optional(bool, true)
    untagged_days = optional(number, 14)
    keep_versions = optional(number, 3)
  })
  default  = {}
  nullable = false
}

variable "adopt_legacy_registry" {
  type     = bool
  default  = false
  nullable = false
}

variable "log_isolation" {
  type     = bool
  default  = true
  nullable = false
}

variable "enable_budget" {
  type     = bool
  default  = false
  nullable = false
}

variable "managed_deployer_scoped" {
  # The managed deployer's seven scoped roles and their seven conditioned
  # bindings — the 0.9.6 redesign of the fence. Before 0.9.6 this flag created
  # one scoped role under one condition, and that condition could not be
  # expressed as a GCP IAM condition at all. Off by default, and fugaro init
  # never sets it: the gate has never been flipped. An installation that does
  # not name it plans exactly as before, minus those fourteen resources. The
  # module's own variable (../../modules/installation/variables.tf) says why
  # there are seven, and what the redesign changed.
  type     = bool
  default  = false
  nullable = false
}

variable "history" {
  type = object({
    account_id       = string
    job              = string
    image            = string
    scheduler_job    = string
    scheduler_region = string
    deploy_job       = optional(bool, false)
    firebase_project = optional(string)
    rtdb_url         = optional(string)
  })
  default = null
}

variable "project_secrets" {
  type = map(string)
  # The project-wide secret containers (logical name -> secret ID) the
  # installation module creates or adopts; empty for an installation with
  # none, which is every installation until fugaro secrets set
  # NAME --project-wide has something to store.
  default  = {}
  nullable = false
}

variable "project_jobs" {
  description = "The shared project jobs (design project-centric §4.1), by Cloud Run job name: one entry per (profile, provider, account), each the module's own object type. fugaro init writes it from the local config's profiles; empty for an installation that names none, which creates no project job at all."
  type = map(object({
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
    secret_env     = map(string)
    bucket_condition = object({
      title      = string
      expression = string
    })
    token_condition = object({
      title      = string
      expression = string
    })
    vertex = bool
  }))
  default  = {}
  nullable = false
}
