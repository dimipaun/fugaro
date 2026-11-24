# Every name arrives from fugaro update's terraform.tfvars.json. The
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
  description = "The Terraform state bucket, which fugaro setup creates and Terraform doesn't manage. Operators get objectAdmin on it."
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
  description = "The log bucket's description, which is its ownership mark: a log bucket carries no labels, so fugaro update adopts an existing one only when it carries this description."
  type        = string

  validation {
    condition     = length(trimspace(var.log_bucket_description)) > 0
    error_message = "log_bucket_description must not be empty: it is the log bucket's ownership mark."
  }
}

variable "log_bucket_location" {
  description = "Where the run log bucket is: the installation's region where Cloud Logging offers a bucket there, else the place's multi-region (us, eu), else global (place.For, D3082). fugaro update writes it, and on an installation that has a bucket already it is the location discovery read, because a bucket's location is permanent: the resource below ignores the field, so this decides what is CREATED and never moves what exists (R5)."
  type        = string
  # `global`, which is where every installation made before this mapping has
  # its bucket and what the module wrote before the variable existed: the root
  # always names a location (fugaro update writes place.For's answer), and this
  # default is what a direct caller of the module gets instead of a plan that
  # refuses for a variable it never had.
  default = "global"

  validation {
    condition     = can(regex("^[a-z]+(-[a-z]+[0-9]+)?$", var.log_bucket_location))
    error_message = "log_bucket_location must be a log bucket location: a region such as us-east5, the multi-region us or eu, or global."
  }
}

variable "bucket_lifecycle" {
  description = "The runs bucket's lifecycle, in days: the three rules the bootstrap set, the one the bucket's versioning adds (a noncurrent version under runs/, cache/, locks/, tokens/ or issues/ is purged this many days after it stops being current), the one the stored reports get (reports/ is deleted this many days after an object is created, which is E3's 18 months held in whole days, as the other members are), and the one a person's message to a running run gets (tokens/…/messages/NNNN.msg.json is deleted this many days after it is written, F11's 7, which is the owner's answer O1)."
  type = object({
    runs_days              = optional(number, 90)
    cache_custom_time_days = optional(number, 30)
    cache_age_days         = optional(number, 180)
    noncurrent_days        = optional(number, 7)
    reports_days           = optional(number, 548)
    messages_days          = optional(number, 7)
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
  description = "Members who onboard repositories, such as user:someone@example.com. The user: members are also the people the credentials service's read mint admits, and the only ones it may be invoked by."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.operators : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "operators must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "launcher_accounts" {
  description = "The launcher class (D1938, F25): the bare email addresses a Google ID token's email claim carries, one per person who may ask the secrets intake or the credentials service for something. `fugaro update` derives it from the same roster list the invoker grants are made from (internal/infra/launchers.go), so the class and the grant name the same people (F31, F36). Empty by default, and the two services render the environment entry only when it is non-empty: an installation whose roster names no launcher starts with a class that admits nobody (fail closed) rather than refusing to bind a port. It never holds a member spelling: a group or a domain is not an address an ID token carries, and the class check refuses one."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for a in var.launcher_accounts : can(regex("^[^@: ]+@[^@ ]+$", a))])
    error_message = "launcher_accounts is the bare email addresses an ID token's email claim carries (someone@example.com), never the member spelling the launchers variable takes (user:someone@example.com): the process compares the claim against these values, and a prefixed one matches nobody."
  }
}

# The viewer list (RS4 of docs/roles-scopes/implementation-plan.md, roles.md
# §4): members who read and do nothing else. The grammar is the Firebase
# module's rather than the two lists above: the same list is handed to the
# Firebase root, where a domain member is refused, so accepting one here would
# be a list the two roots disagree about — an installation with a Firebase
# project could not apply it. A viewer list is a read of every run and every
# run's logs, which is not a grant a whole domain gets by one line.
variable "viewers" {
  description = "Members who read and nothing else: the runs bucket, the project's jobs and runs, the isolated log view, the database and the spend history. No launch, no cancel, no write."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.viewers : can(regex("^(user|group|serviceAccount):[^ *]+$", m))])
    error_message = "viewers must be IAM members: user:, group: or serviceAccount: followed by one address (no domain: and no wildcard: the same list is handed to the Firebase root, where a broader member is refused)."
  }
}

variable "launcher_bucket_condition" {
  description = "The condition of the launchers' objectUser grant on the runs bucket: runs/, tokens/ and issues/ of that bucket only (gcp.LauncherBucketCondition). Passed byte for byte, so every launcher shares one binding."
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
  description = "Whether to adopt the bootstrap's legacy registry. fugaro update sets it when it finds one carrying the fugaro=managed mark."
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

variable "catalog_check" {
  description = "The project's catalog check job (PIC4, D1148 task (ii)), or null on an installation whose project publishes no catalog: the one scheduled Cloud Run job that runs `fugaro image check --job --catalog` over every entry of the project's image catalog and submits at most one build per entry that fires, each build running as the build account of the repository the entry belongs to (modules/repo/catalogcheck.tf grants the actAs). Its job name must start with fugarochk-: fugaro runs ls and max_parallel count the fugaro- jobs, and the alert policies match ^fugarochk- (alert.tf). env is internal/infra's, and carries FUGARO_CATALOG_CHECK_SPEC — the whole run's assignment as JSON, which the job refuses to start without — beside the platform variables every job has. The two IAM conditions are the input's (the definitions read under builds/catalog/, internal/backend/gcp's gcp.CatalogReadCondition, and one records condition per repository, gcp.CatalogRecordsCondition): what makes a grant narrow here is its condition, and it is the bytes the plan review reads. paused is the schedule's own state and defaults to true, so a project that publishes a catalog deploys the schedule stopped and the installation that wants the nightly run writes a false. firebase_project is the installation's Firebase project and not this one on a two-project installation: it is where the job's build leases are written and where its own database role is created and bound (catalogcheck.tf, D1554), and it is unset on an installation with no Firebase backend, where the job submits its builds unleased."
  type = object({
    account_id       = string
    job              = string
    image            = string
    scheduler_job    = string
    scheduler_region = string
    schedule         = optional(string, "17 4 * * *")
    paused           = optional(bool, true)
    firebase_project = optional(string)
    env              = map(string)
    definitions_condition = object({
      title      = string
      expression = string
    })
    repositories = list(object({
      slug = string
      records_condition = object({
        title      = string
        expression = string
      })
      # The repository's provider credential (internal/infra's
      # CatalogCheckRepository.GitSecretID), which the project's build identity
      # holds one secretAccessor on: a build of one of this repository's
      # entries runs as the identity now (B6, D2301) and reads that container
      # through AvailableSecrets, so the reach the per-repository build account
      # held (modules/repo/build.tf's own build grant) has to be the
      # identity's. Optional, and absent for a repository whose credential this
      # installation cannot name, where no build of its entries is submitted.
      git_secret_id = optional(string)
      # The repository's own container, when it holds a live version and
      # git_secret_id names the project's copy: a build resolves the own copy
      # first, so the identity is granted it too, as a second grant beside the
      # first rather than a change of it.
      own_git_secret_id = optional(string)
    }))
  })
  default = null

  validation {
    condition = var.catalog_check == null ? true : (
      # Pinned to the one ID the plan review knows as the catalog check's
      # account (tf.CatalogCheckAccountID), rather than to the shape every
      # service account ID has: an ID the review reads as one of the project's
      # job accounts (fugaro-<readable>-<8 hex>, tf's jobAccount) would be a
      # member every toAccounts row accepts, which covers the repository root's
      # roles/iam.serviceAccountUser on a build account — the one grant of this
      # account that IS in a repository, and the one the owner's typed
      # confirmation exists to ask about.
      var.catalog_check.account_id == "fugaro-catalog-check" &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.catalog_check.job)) &&
      startswith(var.catalog_check.job, "fugarochk-") &&
      can(regex("^[a-zA-Z][a-zA-Z0-9_-]{0,62}$", var.catalog_check.scheduler_job)) &&
      can(regex("^[a-z]+-[a-z]+[0-9]+$", var.catalog_check.scheduler_region)) &&
      length(var.catalog_check.schedule) > 0 &&
      length(var.catalog_check.image) > 0 &&
      length(var.catalog_check.repositories) > 0 &&
      alltrue([for r in var.catalog_check.repositories : can(regex("^[a-z0-9-]+$", r.slug))]) &&
      alltrue([for r in var.catalog_check.repositories :
        length(r.records_condition.title) > 0 && length(r.records_condition.expression) > 0
      ]) &&
      length(var.catalog_check.definitions_condition.title) > 0 &&
      length(var.catalog_check.definitions_condition.expression) > 0
    )
    error_message = "catalog_check needs the service account ID fugaro-catalog-check — the one internal/infra/tf reads as this job's own kind of principal, so a job-account-shaped ID would be covered by the rows that accept those — an image, a job name that starts with fugarochk- (ls counts the fugaro- ones as workflow jobs, and the alert policies match ^fugarochk-), a Scheduler job name and region, and at least one repository with a slug and the two IAM conditions."
  }

  # firebase_project names a project the job's lease role is created in and
  # bound on (catalogcheck.tf), so it has to be a project ID and not something
  # that only reads like one. Unset is an installation with no Firebase
  # backend, where the job takes no lease and neither resource is planned; ""
  # is refused rather than read as unset, because a hand-edited tfvars that
  # wrote it would otherwise silently drop the grant an installation with a
  # database needs.
  validation {
    condition     = var.catalog_check == null ? true : (try(var.catalog_check.firebase_project, null) == null || can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.catalog_check.firebase_project)))
    error_message = "catalog_check.firebase_project must be a GCP project ID, or absent on an installation with no Firebase backend: it is the project the job's build leases are written into and where its own database role is created (catalogcheck.tf, D1554)."
  }

  # The job's whole assignment arrives in its environment: internal/cli's mode
  # refuses to start without it, so an installation that deployed the job
  # without the variable would deploy a job that can only fail, once a night,
  # unnoticed until the alert fires.
  validation {
    condition     = var.catalog_check == null ? true : contains(keys(var.catalog_check.env), "FUGARO_CATALOG_CHECK_SPEC")
    error_message = "catalog_check.env must carry FUGARO_CATALOG_CHECK_SPEC: it is the whole run's assignment (fugaro image check --job --catalog refuses to start without it)."
  }
}

variable "reports" {
  description = "The project's reports job (slice P, ruling E1), or null on an installation that has not asked for one: the one scheduled Cloud Run job that runs `fugaro runs reports --job` and writes, into reports/ of the runs bucket, one report per repository and period and the project's aggregate beside them, keeping 18 months of them (bucket.tf's reports_days, E3). Its job name must start with fugarorpt-: fugaro runs ls and max_parallel count the fugaro- jobs, and the alert policy matches ^fugarorpt- (reports.tf). env is internal/infra's, and carries FUGARO_REPORTS_JOB_SPEC — the assignment the job refuses to start without — beside the platform variables every job has. The two IAM conditions are the input's (the runs/ read under which the job lists and reads a repository's records, internal/backend/gcp's gcp.ReportsReadCondition, and the reports/ write, gcp.ReportsWriteCondition): what makes a grant narrow here is its condition, and it is the bytes the plan review reads. paused is the schedule's own state and defaults to true, so the job ships stopped and the installation that wants the nightly run writes jobs.reports.paused = false in its project layer. account_id is the one ID internal/infra/tf reads as this job's own kind of principal, so it is pinned to that literal and not to the shape a service account ID has."
  type = object({
    account_id       = string
    job              = string
    image            = string
    scheduler_job    = string
    scheduler_region = string
    schedule         = optional(string, "37 5 * * *")
    paused           = optional(bool, true)
    env              = map(string)
    read_condition = object({
      title      = string
      expression = string
    })
    write_condition = object({
      title      = string
      expression = string
    })
  })
  default = null

  validation {
    condition = var.reports == null ? true : (
      # Pinned to the one ID the plan review knows as the reports job's account
      # (tf.ReportsAccountID), for the catalog check's reason: an ID the review
      # reads as one of the project's job accounts (fugaro-<readable>-<8 hex>,
      # tf's jobAccount) would be a member every toAccounts row accepts, and the
      # two rows this account's own kind opens are narrowed by the conditions
      # below alone.
      var.reports.account_id == "fugaro-reports" &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.reports.job)) &&
      startswith(var.reports.job, "fugarorpt-") &&
      can(regex("^[a-zA-Z][a-zA-Z0-9_-]{0,62}$", var.reports.scheduler_job)) &&
      can(regex("^[a-z]+-[a-z]+[0-9]+$", var.reports.scheduler_region)) &&
      length(var.reports.schedule) > 0 &&
      length(var.reports.image) > 0 &&
      length(var.reports.read_condition.title) > 0 &&
      length(var.reports.read_condition.expression) > 0 &&
      length(var.reports.write_condition.title) > 0 &&
      length(var.reports.write_condition.expression) > 0
    )
    error_message = "reports needs the service account ID fugaro-reports — the one internal/infra/tf reads as this job's own kind of principal, so a job-account-shaped ID would be covered by the rows that accept those — an image, a job name that starts with fugarorpt- (ls counts the fugaro- ones as workflow jobs, and the alert policy matches ^fugarorpt-), a Scheduler job name and region, and the two IAM conditions."
  }

  # The job's whole assignment arrives in its environment: the mode refuses to
  # start without it, so an installation that deployed the job without the
  # variable would deploy a job that can only fail, once a night, unnoticed
  # until the alert fires.
  validation {
    condition     = var.reports == null ? true : contains(keys(var.reports.env), "FUGARO_REPORTS_JOB_SPEC")
    error_message = "reports.env must carry FUGARO_REPORTS_JOB_SPEC: it is the whole run's assignment (fugaro runs reports --job refuses to start without it)."
  }
}

variable "licence" {
  description = "The installation's licence renewal job (plan K4.2 of docs/licence-key-v2), or null on an installation that has no coordinator: the scheduled Cloud Run job that runs `fugaro licence renew --job`, whose CODE is the Subscription Edition's (ee/register/licence_renew.go). It is created UNDER the coordinator's own condition and not a switch of its own (licence.tf reads local.deploy_coordinator): its image is the coordinator's, so an installation that has not opted into the coordinator has no tagged image to run. Its job name must not start with fugaro-, which fugaro runs ls and max_parallel count as workflow jobs. account_id is the one ID internal/infra/tf reads as this job's own kind of principal, so it is pinned to that literal; renewal_secret is the ID of the Secret Manager container the job reads and writes, derived by internal/infra.LicenceRenewalSecretID because a container ID with a hash in it cannot be computed in Terraform. The container is created EMPTY and nothing here ever reads a key (D524): an installation that buys no licence holds a job that no-ops and says so, and `fugaro licence set` is what writes the first version (K3.2). The two IAM conditions are the input's (the three-object read the job finds its installation by, internal/backend/gcp's gcp.LicenceReadCondition, and the licence object's write, gcp.LicenceWriteCondition): what makes a grant narrow here is its condition, and it is the bytes the plan review reads."
  type = object({
    account_id       = string
    job              = string
    image            = string
    scheduler_job    = string
    scheduler_region = string
    env              = map(string)
    read_condition = object({
      title      = string
      expression = string
    })
    write_condition = object({
      title      = string
      expression = string
    })
    renewal_secret = string
  })
  default = null

  validation {
    condition = var.licence == null ? true : (
      # Pinned to the one ID the plan review knows as the licence job's account
      # (tf.LicenceAccountID), for the reports account's reason: an ID the review
      # reads as one of the project's job accounts (fugaro-<readable>-<8 hex>,
      # tf's jobAccount) would be a member every toAccounts row accepts, and the
      # three rows this account's own kind opens are narrowed by the conditions
      # and the container below alone.
      var.licence.account_id == "fugaro-licence" &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.licence.job)) &&
      !startswith(var.licence.job, "fugaro-") &&
      can(regex("^[a-zA-Z][a-zA-Z0-9_-]{0,62}$", var.licence.scheduler_job)) &&
      can(regex("^[a-z]+-[a-z]+[0-9]+$", var.licence.scheduler_region)) &&
      length(var.licence.image) > 0 &&
      length(var.licence.renewal_secret) > 0 &&
      length(var.licence.read_condition.title) > 0 &&
      length(var.licence.read_condition.expression) > 0 &&
      length(var.licence.write_condition.title) > 0 &&
      length(var.licence.write_condition.expression) > 0
    )
    error_message = "licence needs the service account ID fugaro-licence — the one internal/infra/tf reads as this job's own kind of principal, so a job-account-shaped ID would be covered by the rows that accept those — an image, a job name that doesn't start with fugaro- (ls counts those), a Scheduler job name and region, the renewal container's ID and the two IAM conditions."
  }

  # The job finds WHICH installation it renews for from its environment: it is a
  # Cloud Run container with no checkout and no config file, so FUGARO_PROJECT
  # and FUGARO_GCP_PROJECT are the whole of what names the project whose
  # published config it reads (internal/cli's selectWith), and the job's own
  # preconditions in licence.tf check the same two against this root's.
  validation {
    condition     = var.licence == null ? true : contains(keys(var.licence.env), "FUGARO_PROJECT") && contains(keys(var.licence.env), "FUGARO_GCP_PROJECT")
    error_message = "licence.env must carry FUGARO_PROJECT and FUGARO_GCP_PROJECT: a container has no checkout and no config file, so those two names are how the job finds the installation whose licence it renews."
  }
}

variable "coordinator" {
  description = "The coordinator's aggregation job (P5, D194), used only with enable_budget: the scheduled sweep that re-reads each repository's runs and pull requests and writes the view. Its job name must not start with fugaro-, which fugaro runs ls and max_parallel count as workflow jobs. deploy_job stays false in the first apply and is set once the Firebase root has output rtdb_url (the third apply), the job's image exists and the credentials its repositories need are recorded: a GitHub App ID where it reconciles a GitHub repository (the sweep mints its own read-only installation token from the App), and the credentials service where it reconciles a Bitbucket one (the sweep mints a read token there — F24)."
  type = object({
    account_id       = string
    job              = string
    image            = string
    scheduler_job    = string
    scheduler_region = string
    app_key_secret   = optional(string, "github-app-key")
    github_app_id    = optional(string, "")
    repos = optional(list(object({
      provider = string
      repo     = string
    })), [])
    deploy_job       = optional(bool, false)
    firebase_project = optional(string)
    rtdb_url         = optional(string)
  })
  default = null

  validation {
    condition = var.coordinator == null ? true : (
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.coordinator.account_id)) &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.coordinator.job)) &&
      !startswith(var.coordinator.job, "fugaro-") &&
      can(regex("^[a-zA-Z][a-zA-Z0-9_-]{0,62}$", var.coordinator.scheduler_job)) &&
      can(regex("^[a-z]+-[a-z]+[0-9]+$", var.coordinator.scheduler_region)) &&
      length(var.coordinator.image) > 0
    )
    error_message = "coordinator needs a service account ID, a job name that doesn't start with fugaro- (ls counts those), a Scheduler job name and region, and an image."
  }

  validation {
    condition = var.coordinator == null ? true : (!var.coordinator.deploy_job || (
      try(var.coordinator.firebase_project, null) != null && try(var.coordinator.rtdb_url, null) != null &&
      can(regex("^https://[a-z0-9.-]+$", coalesce(var.coordinator.rtdb_url, "-"))) &&
      # The App ID is required where the sweep reads GitHub, and only there
      # (F24): its GitHub path mints the coordinator's own read-only
      # installation token from the App, while its Bitbucket path mints a read
      # token at the credentials service (BB5) — so an installation whose
      # repositories are all Bitbucket has no App to name and is not a
      # deployment that is wrong. alltrue over an empty list is true, so that
      # clause on its own would admit a coordinator with no repository at all:
      # the length check beside it is what refuses that, and the two are one
      # guard.
      (var.coordinator.github_app_id != "" || alltrue([for r in var.coordinator.repos : r.provider != "github"])) &&
      length(var.coordinator.repos) > 0
    ))
    error_message = "coordinator.deploy_job needs the Firebase root's firebase_project and rtdb_url (an https://<instance>.firebasedatabase.app or .firebaseio.com URL, without a path), at least one repository to reconcile, and — unless every one of those repositories is on bitbucket, which the sweep reads through the credentials service — the App ID its read mints its own token with."
  }
}

variable "mcp_server" {
  description = "The coordinator's front door: its own service account, its public Cloud Run service, the one endpoint a standard MCP client reaches without a Google identity (D920), and the resource the coordinator's OAuth endpoints answer on (D931's tools, M5's authorization server). OFF by default (null) — the whole switch D934 asks for: an installation that has not asked for a front door plans exactly what it planned before, one that opted into the coordinator for its read-only view included, and a public write endpoint (spawn_task spends money) is never a side effect of an upgrade. account_id is the front door's own service account, which is deliberately not the coordinator's: the sweep's account holds the App key and the whole database, and an identity holding either behind a public endpoint is what spec.md §8's 'there is no credential to reach' forbids. service is the Cloud Run service's own name; image is the coordinator's image, which the module reads out of var.coordinator (one CLI binary serves the sweep and the front door). The account's grants are the runs bucket's read (D932) and, in mcpauth_store.tf, the one binding on the store the token records live in (D935) — the bucket fugaro-mcpauth-<project>, whose one member this account is. One environment variable is declared here and it is M12's: FUGARO_MCP_FRONT_DOOR, written on the front door's own container under this variable and on nothing else, carrying the switch the process reads (D934: its presence is the door being on) and the door's configuration beside it: the origin this service answers on, and the installation's two member lists reduced to the `user:` entries D933 reads. No secret is declared here."
  type = object({
    account_id = string
    service    = string
    image      = string
  })
  default = null

  validation {
    condition = var.mcp_server == null ? true : (
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.mcp_server.account_id)) &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.mcp_server.service)) &&
      length(var.mcp_server.image) > 0
    )
    error_message = "mcp_server needs a service account ID, a Cloud Run service name (lower-case letters, digits and dashes) and the image it runs."
  }

  # The front door is the coordinator's own service, so it needs the
  # coordinator: there is no state for it to answer about on an installation
  # without one. Reading the other variable here is deliberate (Terraform 1.9's
  # cross-variable validation): the alternative is an opt-in that plans nothing
  # and says nothing, which is how an owner ends up believing a front door
  # exists.
  validation {
    condition     = var.mcp_server == null || var.coordinator != null
    error_message = "mcp_server needs var.coordinator: the front door is the coordinator's own service, and an installation that has not opted into the coordinator has no state for one to serve."
  }
}

variable "project_secrets" {
  description = "Project-wide secret containers: logical name -> secret ID (gcp.ProjectSecretID), fugaro update creates/adopts each; values are never set by Terraform."
  type        = map(string)
  default     = {}
  nullable    = false
}

variable "credentials_service" {
  description = "The credentials service (D146, D147): the installation's own Cloud Run service that alone holds the model credentials. OFF by default (null) — with it off this module creates nothing, every job keeps its model-secret accessors and no run reaches a service. image is the released service image, pinned BY DIGEST and never by tag (C1.20's init stage fills the verified digest, as it fills history.image; the validation below refuses a tag, because the account this service runs as reads every credential in the bundle and an operator may push a tag to the installation's own registry); bundle_secret is the Secret Manager ID of the one bundle container the service reads — the project-wide container of the logical name \"creds-bundle\" (internal/backend/gcp's ProjectSecretID, CredsBundleSecretID once the loader of C1.11 declares it), which C1.20's stage fills from that one function so the container created here and the name the loader reads cannot drift. It is created here and written only by the owner-run fugaro creds set (C1.25). firebase_project, rtdb_url and job_accounts are the rest of what the process is configured with (C1.14's serve, whose whole environment is this module's env blocks): the Firebase project a run's identity is verified against and the database it reads, and the installation's per-repository jobs as {provider, repo, workflow} triples, from which the service derives the job service accounts it binds a run to, and review_job_accounts, the review-only variants of the installation's project jobs as {profile, provider, account} triples (the same axis, one row per variant, carrying the value of the account axis the variant's calls are served by) — empty for an installation that deploys no variant, whose service admits no review caller. url is the service's OWN URL — the audience its callers' Google ID tokens are minted for — and it is optional because Cloud Run assigns it: no expression in this file can read google_cloud_run_v2_service.credentials[0].uri from inside that resource (a self-referential block), so the first apply creates the revision with credentials.tf's unassigned placeholder (an .invalid host no ID token is ever minted for: the revision starts, is healthy and admits nobody) until the apply that carries the URL the output reads back (C1.20's stage fills it, the same way history.deploy_job and its rtdb_url are filled on a later apply). Both unset states are the fail-closed direction: a service that guessed its own audience would accept a token minted for another service, and one that refused to start would fail the very apply that produced the URL it needs. verifier_accounts are the identities that may ask the verify route whether a verdict's signature is this service's (C3.9g1, D1068), each a bare address the Google ID token's email claim carries; it is optional, its environment entry is always rendered, and the three states are the whole of the rule: unset means the installation's own verifier account — the one this module creates, and the default an installation gets without naming anything — an explicit [] means nobody (the owner's clear), and a written list is rendered exactly as it is, with the account never added to it. The process reads the entry as an optional value, so a service whose list is empty starts with an empty verifier list rather than refusing to bind a port. signer_account, roster_service_accounts and implicit_admins are the identity half's three names (LM12 of docs/launcher-does-not-mint, under the names LM22 declared on the process's side, D3121, D3123, F29/R16), and they are members of this block rather than variables of their own because every name this service is configured with is one of this block's — a block beside it would be a second place an installation's service is written from, and it would exist in states this one does not. signer_account is the ONE bare address of the account a run's identity is signed as: the runs' signer the installation's Firebase root creates, which the update hands over once that root has — empty is the whole of the other state, an installation whose issuer is not named yet, whose service must start with the entry absent (the identity routes answer the contract's \"not served\" until I1 wires them, R7). roster_service_accounts are the roster's `serviceAccount:` entries as bare addresses (D3123): the class a person's list cannot hold, because the address presents a service account's token rather than a person's, and the accounts iam.tf makes the invoker grant for. implicit_admins are the implicit admins of the installation (F29, R16) as bare addresses, which the update reads out of the machine's own terraform.budget_admins — the same list the CLI's launch gate judges by — so that the issuer's scope check gives the gate's answer and an owner is not refused. All three are read by the process as OPTIONAL values, so an installation whose block carries none of them starts the service it always did — an empty class, or no signer and no issuer — rather than refusing to bind a port, and each is rendered only where it has something to carry."
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
    review_job_accounts = optional(list(object({
      profile  = string
      provider = string
      account  = string
    })), [])
    verifier_accounts       = optional(list(string))
    url                     = optional(string)
    signer_account          = optional(string, "")
    roster_service_accounts = optional(list(string), [])
    implicit_admins         = optional(list(string), [])
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
  #
  # review_job_accounts is held to the same rule for the reason the revision
  # refuses a row it cannot serve a call by (credsvc's AuthConfig: a review job
  # account that names no value on the account axis is a pairing that could only
  # refuse):
  # every row needs all three fields. The list itself may be EMPTY, which is an
  # installation that deploys no review variant and a service that admits no
  # review caller — the fail-closed state, and not the empty coding table above.
  validation {
    condition = var.credentials_service == null ? true : (
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.credentials_service.firebase_project)) &&
      can(regex("^https://[a-z0-9.-]+$", var.credentials_service.rtdb_url)) &&
      length(var.credentials_service.job_accounts) > 0 &&
      alltrue([for j in var.credentials_service.job_accounts :
        j.provider != "" && j.repo != "" && j.workflow != ""
      ]) &&
      alltrue([for r in var.credentials_service.review_job_accounts :
        r.profile != "" && r.provider != "" && r.account != ""
      ]) &&
      (try(var.credentials_service.url, null) == null || can(regex("^https://[a-z0-9.-]+$", var.credentials_service.url)))
    )
    error_message = "credentials_service needs the Firebase project a run's identity is verified against (the shape of a project ID), the installation's realtime database URL (https://<database>.firebasedatabase.app or .firebaseio.com, without a path — the same rule history.rtdb_url is held to), at least one job as {provider, repo, workflow} with all three set (the service binds a run to a repository through them, and an empty table refuses every caller), every review job as {profile, provider, account} with all three set (the service derives the review job's account from them and serves a review call by the account the row names), and, once it is known, its own URL in the same https://<host> form."
  }

  # The identity half's three (LM12) are BARE ADDRESSES — the shape
  # var.launcher_accounts is held to, and for its reason: the process compares
  # each against the `email` claim of a caller's own Google ID token, so an IAM
  # member spelling (`user:someone@example.com`) matches nobody, and a group or
  # a domain holds no address at all (D3123: `group:` entries are deliberately
  # not on any of the three). Empty is the one state they share with the
  # optional lists above — the installation whose roster names no service
  # account, whose configuration names no implicit admin, and whose runs'
  # signer the update has not handed over — and a value that is present and is
  # not an address is a deployment that is wrong rather than one the service
  # discovers at its first call. The refusal is by variable and never by value:
  # these are accounts, and the environment is the process's own.
  validation {
    condition = var.credentials_service == null ? true : (
      can(regex("^([^@: ]+@[^@ ]+)?$", var.credentials_service.signer_account)) &&
      alltrue([for a in var.credentials_service.roster_service_accounts : can(regex("^[^@: ]+@[^@ ]+$", a))]) &&
      alltrue([for a in var.credentials_service.implicit_admins : can(regex("^[^@: ]+@[^@ ]+$", a))])
    )
    error_message = "credentials_service's identity names are bare email addresses, the spelling a Google ID token's email claim carries (someone@example.com) and never the member spelling the roster takes (user:someone@example.com); signer_account is either such an address or empty, which is an installation whose runs' signer the update has not handed over yet and whose service carries no issuer."
  }
}

variable "enable_mobile_service" {
  description = "The public mobile service (mobile plan task P5; D2782 as amended by F31, O8): the second Cloud Run service of the credentials service's own image, which a paired phone reaches — beside the private service, which stays private. It is OFF, and it stays off until a released credentials image can be started in the mode this module starts it in. mobile_service.tf writes that argv (credentials serve --mobile, internal/cli/credentials_serve.go); internal/cli's own test runs it through the real command tree and fails while the two disagree, because a container whose command line the binary refuses is never ready, and the apply that created it — an update of an installation whose runs are doing nothing wrong — fails. What turns it on is the release that lands the mode (I1), and that release passes the runtime account to the Firebase root as mobile_account in the same change: the account's one signJwt is written where its signer is created (modules/firebase/iam.tf), because `fugaro update` applies THIS root before the Firebase stage and a grant naming an account the Firebase root has not created yet fails the apply (internal/cli/init_stages.go). A bool rather than a block of its own: the service carries no configuration this block could hold — every name it is configured with is var.credentials_service's own member, and it exists exactly when that block does."
  type        = bool
  default     = false

  validation {
    condition     = !var.enable_mobile_service || var.credentials_service != null
    error_message = "enable_mobile_service needs var.credentials_service: the mobile service is that service's image, its account, and the whole of what it is configured with is that block's members — with the block null the switch turns nothing on, and an installation whose tfvars name it is one whose two halves were hand-edited apart."
  }
}

variable "intake" {
  description = "The secrets intake (D1931, DS2.2): the installation's own Cloud Run service that alone creates, versions and deletes the dynamic per-secret containers — fugaro_u_<sub>_<name> and fugaro_p_<name>, the internal/secretname grammar — and the identity the credentials service reads them back through a prefix-conditioned accessor for. OFF by default (null): with it off this module creates no account, no role and no service, and no container of the dynamic class is ever written. It is on for an installation whose owner has the feature, and turning it off again removes a service and an account and touches no container (nothing here reads a value, so nothing here carries a prevent_destroy). account_id and service are the account's ID and the Cloud Run service's name; image is the released image, pinned BY DIGEST and never by a tag — the same rule and the same reason as credentials_service.image, because this process runs as the account that writes an installation's credentials and the repository grants roles/artifactregistry.writer to every operator (iam.tf); url is the service's OWN URL, the audience a caller's Google ID token is minted for, and it is optional because Cloud Run assigns it — no expression can read google_cloud_run_v2_service.intake[0].uri from inside that resource, so the first apply creates the revision with intake.tf's unassigned placeholder (an .invalid host no ID token is ever minted for: the revision starts, is healthy and admits nobody) until the apply that carries the URL this module's output publishes. people is the intake's SECOND class list (WC3, R5): the people of a workspace's roster, whose tokens live in THIS installation's containers (D3143), as the bare addresses a Google ID token's email claim holds. It is held to var.launcher_accounts' own rule (the same `^[^@: ]+@[^@ ]+$` shape, the same 512-byte bound and the same service-account refusal), because the two lists merge into ONE class list and ONE invoker set (intake.tf's locals): each entry here is spelled into the grant as the member spelling (the module prefixes each one with user:), so an address already carrying a colon is not an address this list can carry — it is a member IAM has no principal for and a claim no token holds, and the same value reaching the class list through the launchers' half is refused by launcher_accounts today. The renderer drops all three shapes for that reason (internal/infra's IntakePeople) and leaves the person off rather than write a value the plan refuses, which is internal/access's own rule for the same kind of value (its admin list refuses a colon-bearing entry and the derivation drops the person). It is a member of THIS block and of no other variable, and that is load bearing rather than tidy: the installation's `launchers` list is also the workspace project's credentials service invoker set (iam.tf, local.launcher_invoker_members), so a roster person added there would be admitted to a service this task does not widen (F10). The intake's invoker set and the class its process reads are the two together — var.launcher_accounts and this list (intake.tf's own locals) — and this list is empty (the default) for every installation that is not a workspace's project, where `fugaro update` renders it from the roster on the workspace owner's own run."
  type = object({
    account_id = string
    service    = string
    image      = string
    url        = optional(string)
    people     = optional(list(string), [])
  })
  default = null

  # The image is a DIGEST and never a tag, held to credentials.tf's own rule
  # and for its reason: an operator can push a tag, and the code this account
  # runs is the code that writes an installation's dynamic credentials.
  validation {
    condition = var.intake == null ? true : (
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.intake.account_id)) &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.intake.service)) &&
      can(regex("^[A-Za-z0-9][A-Za-z0-9._/@:-]*@sha256:[0-9a-f]{64}$", var.intake.image)) &&
      (var.intake.url == null || can(regex("^https://[a-z0-9.-]+$", var.intake.url))) &&
      alltrue([for p in var.intake.people : can(regex("^[^@: ]+@[^@ ]+$", p)) && length(p) <= 512 && !endswith(lower(p), ".gserviceaccount.com")])
    )
    error_message = "intake needs a service account ID (6-30 characters of a-z, 0-9 and -, starting with a letter), a Cloud Run service name, an image pinned by digest (an image reference ending in @sha256: and 64 lower-case hex digits — never a tag: an operator can push a tag), and, once the URL is known, its own URL in the https://<host> form (no path); people is a list of BARE email addresses — the class is matched to a Google ID token's email claim, so a group or a domain entry is one no token can carry — and each entry is held to launcher_accounts' own shape (someone@example.com: one @, no colon and no space in the local part, no @ in the domain), because the two lists are one class list and the grant is this entry's member spelling (intake.tf prefixes each with user:): an address carrying a colon is the member spelling itself and not a bare address, so it is refused here rather than spelled into a grant with no principal. A service account's address, which is a job's rather than a person's, is refused in ANY case, and so is an address past 512 bytes — the test folds a value first and internal/credsvc's foldLauncherAccounts REFUSES both shapes outright, so an entry of either is a revision that exits on start (WC3). The three are exactly the shapes internal/infra's IntakePeople leaves off the list it renders from a workspace's roster, so the roster can never be written into a value this refuses."
  }
}

variable "voice_service" {
  description = "The voice hop (P5.3.3, design docs/mobile/design/voice-evaluation-switch.md §4.4): the installation's own Cloud Run service that a paired phone calls before its speech reaches a provider. It verifies the phone's Firebase Auth ID token for a device uid of D165's `m~` family, checks that the device's node config/admin/devices/$uid still exists (D166; revocation is the node's deletion) and enforces the project's voice policy node. OFF by default (null), and the block itself IS the opt-in: `fugaro update` writes and carries the other two services' blocks on every installation, and it never writes this one — an installation that never wrote it gets no hop, no account and no invoker binding, and is exactly what it was before the hop existed. account_id is the account's ID, fixed by internal/infra's VoiceAccountID rather than a per-installation choice (the cover classifies the state-read grant's member by that one literal, internal/infra/tf's isVoiceMember, and `fugaro update` re-composes a lost block from it); service is the Cloud Run service's name, and it IS the installation's own — the name is a force-new attribute of the Cloud Run service, so an installation that renamed it keeps its name through every later update; image is the released image, pinned BY DIGEST and never by a tag — the same rule and the same reason as credentials_service.image, because this account reads the whole database instance and the repository grants roles/artifactregistry.writer to every operator (iam.tf). What the process is CONFIGURED with is not here: the Firebase project whose ID tokens it verifies and the database whose device nodes and policy it reads are credentials_service's own two members (firebase_project, rtdb_url), which voice.tf reads, because the two blocks describe one installation and a second copy could be pulled apart by a hand edit. There is deliberately no url member either, and that is the one real difference from credentials_service and intake: this service's audience is the Firebase project and not its own URL, so nothing has to read a URL back from this module's output and no second apply exists for it."
  type = object({
    account_id = string
    service    = string
    image      = string
  })
  default = null

  # The image is a DIGEST and never a tag, held to credentials.tf's own rule
  # and for its reason: an operator can push a tag, and the account this
  # process runs as holds the instance-wide read of the installation's
  # database.
  #
  # The account ID is a literal and not a pattern, unlike the other two
  # services' (D1931's intake admits any valid account ID and any Cloud Run
  # name): internal/infra creates this account under one ID and re-composes a
  # lost block from it (internal/cli/init.go's keepVoiceHop), and the cover
  # classifies the account's one project grant by that same ID
  # (internal/infra/tf's isVoiceMember), so a second ID here would be an account
  # whose state-read binding the cover does not know and a hop the next `fugaro
  # update` would create twice.
  validation {
    condition = var.voice_service == null ? true : (
      var.voice_service.account_id == "fugaro-voice" &&
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.voice_service.service)) &&
      can(regex("^[A-Za-z0-9][A-Za-z0-9._/@:-]*@sha256:[0-9a-f]{64}$", var.voice_service.image))
    )
    error_message = "voice_service needs the account ID fugaro-voice — the ID internal/infra creates the account under and the cover classifies its state-read grant by, so it is not a per-installation choice — a Cloud Run service name, and an image pinned by digest (an image reference ending in @sha256: and 64 lower-case hex digits — never a tag: an operator can push a tag)."
  }
}

variable "managed_deployer_scoped" {
  description = "Whether to create the managed deployer's seven conditioned roles (fugaroDeployerStorage, Secret, Account, ApiKey, Repository, RunJob, SchedulerJob) and their seven conditioned bindings. OFF by default, and set by fugaro for an installation that uses managed projects (the module's variable is written from the project's own creation fact, internal/cli/init.go's installRoot): managed projects ship in 0.10.0. What is NOT settled is whether the fence works: the redesigned fmgd- prefix fence has not been measured live yet (docs/managed-deployer-fence/spec.md, Check 68 of the live checklist), so an apply that creates these bindings is the first thing to find out whether IAM accepts them. Before 0.10.0 this flag created one scoped role under one condition, and that condition could not be expressed as a GCP IAM condition at all (17 logical operators where IAM allows 12, resource.name.contains() is not a supported function, and resource.name is not supported for Artifact Registry repositories, Cloud Run jobs, Cloud Scheduler jobs, service accounts or API keys), so setIamPolicy would have rejected it and stopped the whole apply."
  type        = bool
  default     = false
  nullable    = false
}

variable "orchestrator" {
  description = "The resident orchestrator (mobile plan tasks P5.1b and P5.1c; docs/mobile/design/always-on-orchestrator.md sections 4.1 and 4.3): one service account per installation with exactly the grants the design's table lists, the Cloud Run service that account runs, the Cloud Scheduler tick that wakes it, and the environment that service's one process is configured by. OFF by default (null) — an installation that has not opted in plans exactly what it planned before this file existed, which is what makes the update carrying it a no-op for every installation that has not asked for a resident orchestrator. account_id is the account's ID, fixed by internal/infra's OrchestratorAccountID so that `fugaro doctor` can find the account by the name the design gives it. firebase_project and signer_account_id are the token-minter row's two names and are on this variable rather than derived from var.coordinator: that row is bound on the FIREBASE root's signer account, in that root's project, where the fugaroTokenMinter role is declared (modules/firebase/iam.tf), and the ID is the one internal/infra hands the Firebase root (FirebaseVars' SignerAccountID, the firebase module's names.signer_account_id), so this file composes the signer's address from the same ID the account is created with, the way scheduler.tf composes its own account's. service is the Cloud Run service's name; image is the released image pinned BY DIGEST and never by a tag, for credentials_service.image's and intake.image's reason; rtdb_url is the database the process reads this installation's node out of, and it is a field of its own because the service's own Firebase project and the database's URL are two names the same installation computes in two roots. scheduler_job and scheduler_region are the tick's own two names, scheduler_region separately because Cloud Scheduler is not offered in every Cloud Run region (history.tf's and coordinator.tf's rule). The writer is `fugaro update --firebase-project <gcp-project-id> --orchestrator` (internal/cli's ensureOrchestratorBlock through internal/infra's Orchestrator and SetOrchestrator), which derives every field from what the installation already holds and never from the operator, and which runs at the stage that has the Firebase outputs — after the Firebase root, so this root's token-minter binding is created against a signer that exists (orchestrator.tf states the ordering). The block is written once and carried by every later run (internal/infra's CarriedOrchestrator, which moves only the image onto the release's own pin), so a run that is not asked for the orchestrator composes nothing here."
  type = object({
    account_id        = string
    firebase_project  = string
    signer_account_id = string
    service           = string
    image             = string
    rtdb_url          = string
    scheduler_job     = string
    scheduler_region  = string
  })
  default = null

  validation {
    condition = var.orchestrator == null ? true : (
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.orchestrator.account_id)) &&
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.orchestrator.firebase_project)) &&
      can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.orchestrator.signer_account_id))
    )
    error_message = "orchestrator needs a service account ID (6-30 characters of a-z, 0-9 and -, starting with a letter) for the installation's own account, the Firebase project the signer account lives in, and the signer account's own ID on that project — the token-minter grant is bound on that account, in that project, which is where the role it names exists."
  }

  # The service, its pin and the tick, held to intake's rules and for their
  # reasons: an operator can push a tag, so the image is a digest; the service
  # name is a Cloud Run name; the database URL is the https://<host> form the
  # process addresses (no path, no port), which is also what keeps the name it
  # is composed from a host rather than anything a caller could paste; and the
  # tick's two names are a Scheduler job name and a Scheduler region, the
  # region being a field of its own because Scheduler is not offered in every
  # Cloud Run region.
  validation {
    condition = var.orchestrator == null ? true : (
      can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.orchestrator.service)) &&
      can(regex("^[A-Za-z0-9][A-Za-z0-9._/@:-]*@sha256:[0-9a-f]{64}$", var.orchestrator.image)) &&
      can(regex("^https://[a-z0-9.-]+$", var.orchestrator.rtdb_url)) &&
      can(regex("^[a-zA-Z][a-zA-Z0-9_-]{0,62}$", var.orchestrator.scheduler_job)) &&
      can(regex("^[a-z]+-[a-z]+[0-9]+$", var.orchestrator.scheduler_region))
    )
    error_message = "orchestrator needs a Cloud Run service name, an image pinned by digest (an image reference ending in @sha256: and 64 lower-case hex digits — never a tag: an operator can push a tag), the database URL the process reads this installation's node out of in the https://<host> form (no path), and the tick's Scheduler job name and region."
  }
}

# The switch on the minter rows that are not the issuer's (launcher-does-not-mint
# plan task LM12, D3121, R8), and it is the FIREBASE root's own switch read here
# under the same name: in that root it gates the people's row — the launchers'
# and the operators' fugaroTokenMinter grant on the runs' signer (LM11) — and in
# this one it gates the resident orchestrator's row on that same signer
# (orchestrator.tf's orchestrator_token_minter). ONE name in both roots, because
# the plan that moves the grant to the credentials service turns both off at
# once (LM14: `fugaro update` applies both roots with the switch off once the
# service has proved it signs as this signer, and the people's row and the
# orchestrator's are gone together).
#
# ON, and the default is the whole of what makes the update carrying this change
# a plan with nothing new in it: an installation that has not been asked to move
# its grant keeps minting the way it always did. What it never gates is the
# ISSUER's row (modules/firebase/iam.tf's issuer_minter): the plan that turns
# this switch off is the plan that moves the grant TO the credentials service,
# and a switch that also took that row away would leave nobody able to mint a
# run's identity — the failure R8 exists to avoid.
variable "people_minter" {
  description = "Whether the minter rows that are not the issuer's are still planned: the resident orchestrator's on the runs' signer here, and the launchers' and the operators' in the Firebase root, which carries the same switch under this name. On until `fugaro update` moves the grant to the credentials service, which turns it off in the one plan that removes those rows."
  type        = bool
  default     = true
  nullable    = false
}
