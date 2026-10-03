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

variable "fugaro_project" {
  description = "The Fugaro project's name (not the GCP project ID, which is `project`). It labels the runs bucket and is written to the bucket's fugaro/project.json; it never changes."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$", var.fugaro_project))
    error_message = "fugaro_project must be a project name: 1-40 characters of a-z, 0-9 and -, starting and ending with a letter or digit."
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
      tag_mover       = string
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

variable "log_bucket_description" {
  description = "The log bucket's description, which is its ownership mark: a log bucket carries no labels, so fugaro init adopts an existing one only when it carries this description."
  type        = string

  validation {
    condition     = length(trimspace(var.log_bucket_description)) > 0
    error_message = "log_bucket_description must not be empty: it is the log bucket's ownership mark."
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
  description = "Artifact Registry cleanup. dry_run only logs what would be deleted; turn it off once the audit logs show no latest or dev- version would go. untagged_days and keep_versions apply to fugaro-base only; dry_run (exported as registry_cleanup_dry_run) is the only setting every repository's registry shares, and those keep 3 versions and delete untagged ones after 14 days."
  type = object({
    enabled       = optional(bool, true)
    dry_run       = optional(bool, true)
    untagged_days = optional(number, 14)
    keep_versions = optional(number, 3)
  })
  default  = {}
  nullable = false

  validation {
    condition = alltrue([for n in [var.registry_cleanup.untagged_days, var.registry_cleanup.keep_versions] :
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

variable "log_isolation" {
  description = "Whether Fugaro jobs' logs go to their own log bucket, readable only through its view, and stay out of _Default."
  type        = bool
  default     = true
  nullable    = false
}

variable "enable_budget" {
  description = "Whether the installation has budget enforcement: the history account, which exists before the Firebase root grants it anything, and, once history.deploy_job is set, the history job and its sweep Scheduler job."
  type        = bool
  default     = false
  nullable    = false
}

variable "history" {
  description = "The history job (it sweeps the run registry of crashed runs), used only with enable_budget. Its job name must not start with fugaro-, which fugaro ls and max_parallel count as workflow jobs. deploy_job stays false in the first apply and is set once the Firebase root has output rtdb_url (the third apply), when the job's image exists."
  type = object({
    account_id             = string
    job                    = string
    image                  = string
    scheduler_job          = string
    scheduler_region       = string
    rollover_scheduler_job = optional(string, "fugaro-history-rollover")
    deploy_job             = optional(bool, false)
    firebase_project       = optional(string)
    rtdb_url               = optional(string)
  })
  default = null

  validation {
    condition = var.history == null ? true : (
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.history.account_id)) &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.history.job)) &&
      !startswith(var.history.job, "fugaro-") &&
      can(regex("^[a-zA-Z][a-zA-Z0-9_-]{0,62}$", var.history.scheduler_job)) &&
      can(regex("^[a-zA-Z][a-zA-Z0-9_-]{0,62}$", var.history.rollover_scheduler_job)) &&
      var.history.rollover_scheduler_job != var.history.scheduler_job &&
      can(regex("^[a-z]+-[a-z]+[0-9]+$", var.history.scheduler_region)) &&
      length(var.history.image) > 0
    )
    error_message = "history needs a service account ID, a job name that doesn't start with fugaro- (ls counts those), a Scheduler job name and region, and an image."
  }

  validation {
    condition = var.history == null ? true : (!var.history.deploy_job || (
      try(var.history.firebase_project, null) != null && try(var.history.rtdb_url, null) != null &&
      can(regex("^https://[a-z0-9.-]+$", coalesce(var.history.rtdb_url, "-")))
    ))
    error_message = "history.deploy_job needs the Firebase root's firebase_project and rtdb_url (an https://<instance>.firebasedatabase.app or .firebaseio.com URL, without a path)."
  }
}
