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

  validation {
    condition = alltrue([for id in [var.names.legacy_registry, var.names.base_registry, var.names.ghcr_registry] :
    can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", id))])
    error_message = "names.legacy_registry, names.base_registry and names.ghcr_registry must be registry IDs: 1-63 characters of a-z, 0-9 and -, starting with a letter."
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

variable "launcher_bucket_condition" {
  description = "The condition of the launchers' objectUser grant on the runs bucket: runs/ and tokens/ of that bucket only (gcp.LauncherBucketCondition). Passed byte for byte, so every launcher shares one binding."
  type = object({
    title      = string
    expression = string
  })

  validation {
    condition     = startswith(var.launcher_bucket_condition.expression, "resource.name.startsWith(\"projects/_/buckets/") && length(var.launcher_bucket_condition.title) > 0
    error_message = "launcher_bucket_condition needs a title and a resource.name.startsWith expression on a bucket."
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
  description = "The history job (it sweeps the run registry of crashed runs), used only with enable_budget. Its job name must not start with fugaro-, which fugaro runs ls and max_parallel count as workflow jobs. deploy_job stays false in the first apply and is set once the Firebase root has output rtdb_url (the third apply), when the job's image exists."
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

variable "project_secrets" {
  description = "Project-wide secret containers: logical name -> secret ID (gcp.ProjectSecretID), fugaro init creates/adopts each; values are never set by Terraform."
  type        = map(string)
  default     = {}
  nullable    = false
}

variable "credentials_service" {
  description = "The credentials service (D146, D147): the installation's own Cloud Run service that alone holds the model credentials. OFF by default (null) — with it off this module creates nothing, every job keeps its model-secret accessors and no run reaches a service. image is the released service image, pinned BY DIGEST and never by tag (C1.20's init stage fills the verified digest, as it fills history.image; the validation below refuses a tag, because the account this service runs as reads every credential in the bundle and an operator may push a tag to the installation's own registry); bundle_secret is the Secret Manager ID of the one bundle container the service reads — the project-wide container of the logical name \"creds-bundle\" (internal/backend/gcp's ProjectSecretID, CredsBundleSecretID once the loader of C1.11 declares it), which C1.20's stage fills from that one function so the container created here and the name the loader reads cannot drift. It is created here and written only by the owner-run fugaro creds set (C1.25). firebase_project, rtdb_url and job_accounts are the rest of what the process is configured with (C1.14's serve, whose whole environment is this module's env blocks): the Firebase project a run's identity is verified against and the database it reads, and the installation's per-repository jobs as {provider, repo, workflow} triples, from which the service derives the job service accounts it binds a run to. url is the service's OWN URL — the audience its callers' Google ID tokens are minted for — and it is optional because Cloud Run assigns it: no expression in this file can read google_cloud_run_v2_service.credentials[0].uri from inside that resource (a self-referential block), so the first apply creates the revision with credentials.tf's unassigned placeholder (an .invalid host no ID token is ever minted for: the revision starts, is healthy and admits nobody) until the apply that carries the URL the output reads back (C1.20's stage fills it, the same way history.deploy_job and its rtdb_url are filled on a later apply). Both unset states are the fail-closed direction: a service that guessed its own audience would accept a token minted for another service, and one that refused to start would fail the very apply that produced the URL it needs."
  type = object({
    account_id       = string
    service          = string
    image            = string
    bundle_secret    = string
    firebase_project = string
    rtdb_url         = string
    job_accounts = list(object({
      provider = string
      repo     = string
      workflow = string
    }))
    url = optional(string)
  })
  default = null

  # The image is a DIGEST and never a tag, and that is a security rule rather
  # than a preference. The service runs as the account that reads every
  # credential in the bundle, and the repository the image is pushed to grants
  # roles/artifactregistry.writer to every operator (iam.tf's operators
  # binding on fugaro-base): a mutable tag such as
  # `fugaro-base/fugaro-credentials:latest` would let any operator push the
  # code that the next owner apply runs as that account — the boundary D145
  # exists to keep ("operators add, never read"). A digest cannot be
  # rewritten, so the revision an apply creates runs the bytes the owner
  # verified. C1.20's init stage fills the verified digest (init already reads
  # verified digests back through the ghcr proxy), and the tag form is refused
  # at plan time rather than trusted.
  validation {
    condition = var.credentials_service == null ? true : (
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.credentials_service.account_id)) &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.credentials_service.service)) &&
      can(regex("^[A-Za-z0-9][A-Za-z0-9._/@:-]*@sha256:[0-9a-f]{64}$", var.credentials_service.image)) &&
      can(regex("^[a-zA-Z0-9_-]{1,255}$", var.credentials_service.bundle_secret))
    )
    error_message = "credentials_service needs a service account ID (6-30 characters of a-z, 0-9 and -, starting with a letter), a Cloud Run service name, an image pinned by digest (an image reference ending in @sha256: and 64 lower-case hex digits — never a tag: an operator can push a tag), and the bundle secret's Secret Manager ID (letters, digits, - and _)."
  }

  # The four facts the process is configured with (C1.14), held to their shape
  # at plan time rather than left to the revision that would refuse them: a
  # service whose environment is wrong does not serve at all, so an installation
  # that planned one would halt every run that reached it (D143). The two
  # project-shaped names are the same regex the account ID above is held to
  # (Firebase project IDs have the same shape), the URLs are history.tf's own
  # rule (https://<host>, no path), and the job table must not be empty: the
  # service refuses a table that binds no run to a repository (an empty one is
  # a service an operator would read as healthy while every run halted), so a
  # plan that carries one is refused here instead of at the revision.
  validation {
    condition = var.credentials_service == null ? true : (
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.credentials_service.firebase_project)) &&
      can(regex("^https://[a-z0-9.-]+$", var.credentials_service.rtdb_url)) &&
      length(var.credentials_service.job_accounts) > 0 &&
      alltrue([for j in var.credentials_service.job_accounts :
        j.provider != "" && j.repo != "" && j.workflow != ""
      ]) &&
      (try(var.credentials_service.url, null) == null || can(regex("^https://[a-z0-9.-]+$", var.credentials_service.url)))
    )
    error_message = "credentials_service needs the Firebase project a run's identity is verified against (the shape of a project ID), the installation's realtime database URL (https://<database>.firebasedatabase.app or .firebaseio.com, without a path — the same rule history.rtdb_url is held to), at least one job as {provider, repo, workflow} with all three set (the service binds a run to a repository through them, and an empty table refuses every caller), and, once it is known, its own URL in the same https://<host> form."
  }
}

variable "managed_deployer_scoped" {
  description = "Whether to create the managed deployer's seven conditioned roles (fugaroDeployerStorage, Secret, Account, ApiKey, Repository, RunJob, SchedulerJob) and their seven conditioned bindings. OFF by default, and fugaro init never sets it: the redesigned fmgd- prefix fence has not been measured live yet (docs/design/managed-deployer-fence.md, Check 68 of the live checklist). Before 0.10.0 this flag created one scoped role under one condition, and that condition could not be expressed as a GCP IAM condition at all (17 logical operators where IAM allows 12, resource.name.contains() is not a supported function, and resource.name is not supported for Artifact Registry repositories, Cloud Run jobs, Cloud Scheduler jobs, service accounts or API keys), so setIamPolicy would have rejected it and stopped the whole apply. Managed projects stay disabled until the redesigned fence is measured live."
  type        = bool
  default     = false
  nullable    = false
}
