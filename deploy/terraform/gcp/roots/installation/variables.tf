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

variable "log_bucket_location" {
  type = string
}

variable "bucket_lifecycle" {
  type = object({
    runs_days              = optional(number, 90)
    cache_custom_time_days = optional(number, 30)
    cache_age_days         = optional(number, 180)
    reports_days           = optional(number, 548)
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

variable "launcher_accounts" {
  type     = list(string)
  default  = []
  nullable = false
}

variable "viewers" {
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
  # bindings — the 0.10.0 redesign of the fence. Before 0.10.0 this flag created
  # one scoped role under one condition, and that condition could not be
  # expressed as a GCP IAM condition at all. Off by default, and set by fugaro
  # for an installation that uses managed projects (the project's own creation
  # fact is what decides it): managed projects ship in 0.10.0, and the fence's
  # live measurement (Check 68) follows that release. An installation that does
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

variable "catalog_check" {
  description = "The project's catalog check job (PIC4, D1148 task (ii)), or null when the project publishes no catalog: modules/installation/catalogcheck.tf and its variable of the same name. fugaro update writes it; it is passed through unchanged, and the module validates it."
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
      git_secret_id     = optional(string)
      own_git_secret_id = optional(string)
    }))
  })
  default = null
}

variable "reports" {
  description = "The project's reports job (E4), or null when fugaro update deploys none: modules/installation/reports.tf and its variable of the same name. fugaro update writes it; it is passed through unchanged, and the module validates it."
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
}

variable "licence" {
  description = "The installation's licence renewal job (plan K4.2 of docs/licence-key-v2), or null when fugaro update deploys none: modules/installation/licence.tf and its variable of the same name. fugaro update writes it; it is passed through unchanged, and the module validates it. The module reads the coordinator's own condition, so this variable is a passthrough and not a switch: an installation that deploys no coordinator plans no job whatever this carries."
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
}

variable "coordinator" {
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
}

variable "mcp_server" {
  type = object({
    account_id = string
    service    = string
    image      = string
  })
  # The coordinator's front door (D920, D934), OFF by default and separate from
  # each of the coordinator's own opt-ins: null creates nothing, so an
  # installation that never asked plans exactly what it planned before, and one
  # that opted into the coordinator for its read-only view keeps its private
  # aggregation job and acquires no public endpoint. account_id is the front
  # door's own account — not the coordinator's, whose sweep holds the App key
  # and the whole database — service is the Cloud Run service's name and image
  # is the coordinator's image, one CLI binary serving the sweep and the front
  # door; all three are computed by internal/infra.MCPServer (the module's type
  # is the same object, and its validations are the ones that hold them to
  # their shape).
  default = null
}

variable "intake" {
  type = object({
    account_id = string
    service    = string
    image      = string
    url        = optional(string)
    # people is the intake's second class list (WC3, R5): the workspace
    # roster's people, for an installation that is a workspace's project, and
    # empty for every other one (`fugaro update` renders it from the roster).
    # The root passes this block to the module whole (main.tf), so a member
    # added here is a member the module can read; the module's own type and
    # validation are what hold it to its shape.
    people = optional(list(string), [])
  })
  default = null
}

# The voice hop (P5.3.3, design docs/mobile/design/voice-evaluation-switch.md
# §4.4): the third service this installation MAY run, beside the credentials
# service and the intake, and the one a phone calls. account_id is its own
# account's ID, fixed by internal/infra's VoiceAccountID rather than a choice of
# the installation (the module's validation accepts no other, and the cover
# classifies the account's database grant by that literal); service is the Cloud
# Run service's name and IS the installation's own, since a name is a force-new
# attribute of the service; image is the released binary's digest pin. The block
# IS the opt-in, and the installation writes it:
# `fugaro update` deploys the other two services on every installation and never
# writes this one, so an installation that never wrote the block gets no hop at
# all — no account, no service and no invoker binding — and an installation that
# did keeps it across updates, with its image moved onto each release's pin
# beside the two services it shares that pin with. The hop's configuration is
# the credentials service's block's: the Firebase project whose ID tokens it
# verifies and the database whose device nodes and policy it reads are
# credentials_service's own members (firebase_project, rtdb_url), which
# modules/installation/voice.tf reads rather than repeating here. There is
# deliberately no url member and no fill step: the audience a device's token
# must carry is that Firebase project and not the service's own URL. Absent, the
# module creates nothing, which is every installation until it asks for the hop;
# the type is the module's, and its validations are the ones that hold the three
# to their shape.
variable "voice_service" {
  type = object({
    account_id = string
    service    = string
    image      = string
  })
  default = null
}

# The public mobile service's switch (mobile plan task P5; D2782 as amended by
# F31, O8). OFF, and it stays off until a released credentials image runs the
# mode the module starts that service in: `fugaro update` writes no key for it,
# so an installation gets it only from a hand-edited tfvars — which is what this
# root has to declare it for, and what internal/infra's own test holds the
# binary to (it writes no such key, so nothing this release does turns it on).
# The module's variable carries the reasoning; the release that lands the mode
# sets it here and hands the account to the Firebase root in the same change.
variable "enable_mobile_service" {
  type     = bool
  default  = false
  nullable = false
}

variable "credentials_service" {
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
  # The credentials service (D146, D147): every `fugaro update` writes this
  # block (DON1, the owner's ruling of 2026-11-05), so the service is part of
  # the installation the run applies rather than an opt-in. Absent, the module
  # creates nothing and every job keeps its model-secret accessors — which is
  # an installation that has not been updated yet, and one whose config names
  # no Firebase backend or no repository job, which the service cannot be
  # deployed into at all. bundle_secret is the one container the service
  # reads — the project-wide container of the logical name "creds-bundle"
  # (internal/backend/gcp's ProjectSecretID), which the update's own deploy
  # writes so this variable and the loader's own derivation (C1.11) name one
  # secret.
  # firebase_project, rtdb_url and job_accounts are the rest of the service's
  # environment (C1.14: the Firebase project, the database, and the jobs whose
  # accounts may call it), review_job_accounts the review-only variants of
  # those project jobs — the review class's admitted set, one {profile,
  # provider, account} row per variant, empty for an installation that deploys
  # none — url is the service's own URL — the audience its
  # callers' tokens are minted for — which Cloud Run assigns, so the module
  # carries its unassigned placeholder until the apply after the one that
  # creates the service (the module's own output reads the URL back, and the
  # run that deploys the service applies again in the same run; the type is
  # the module's, and its validations are the ones that hold these four to
  # their shape). verifier_accounts and the identity half's three —
  # signer_account, roster_service_accounts and implicit_admins, which LM12 of
  # docs/launcher-does-not-mint adds under the names LM22 declared on the
  # process's side (D3121, D3123, F29/R16) — are the OPTIONAL members. The
  # module renders each of those three only where it has something to carry,
  # and verifier_accounts' entry is always rendered (its unset state is the
  # installation's own verifier account), so an installation that names none of
  # them starts the service it always did: the same names it always had, an
  # empty verifier list or the account this module creates, and no signer and
  # no issuer (C3.9g1, D1068). The module's own description is where each
  # member's shape and unset state are written; this root declares the types
  # and adds no validation of its own.
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
  description = "The shared project jobs (design project-centric §4.1), by Cloud Run job name: one entry per (profile, provider, account), each the module's own object type. fugaro update writes it from the local config's profiles; empty for an installation that names none, which creates no project job at all."
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
    # The installation's credentials service, or null for a profile it does
    # not serve (B5.2): the module grants such a job's account
    # roles/run.invoker on it, and changes nothing else about the job.
    credentials_service = optional(object({
      name   = string
      region = string
    }))
  }))
  default  = {}
  nullable = false
}

variable "project_review_jobs" {
  description = "The review-only variants of those jobs (design docs/cloud-senior-review, CSR7), by Cloud Run job name: one entry per (profile, provider, account), the module's own object type, with an empty secret_env. Deployed as a module instance of its own, with no secret container handed in. fugaro update writes it from the same profiles; empty for an installation that names none."
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
    # Null for every variant (B5.2): the module's object type is one type, so
    # the attribute is here, and internal/infra's reviewVariant sets none — a
    # variant is pointed at the service by its own deployment's environment,
    # and no variant account carries the roles/run.invoker grant sa.tf makes
    # from a value here.
    credentials_service = optional(object({
      name   = string
      region = string
    }))
  }))
  default  = {}
  nullable = false
}

variable "orchestrator" {
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
  # The resident orchestrator (mobile plan tasks P5.1b and P5.1c), OFF by
  # default: null creates nothing, so an installation that has not opted in
  # plans exactly what it planned before the module file existed, and every
  # `fugaro update` that carries it is a no-op for that installation. The
  # eight fields are the account and the two names of its token-minter row, the
  # Cloud Run service and the pin it runs, the database that service reads this
  # installation's node out of, and the tick's Scheduler job name and region.
  # The module's type is this same object and its validations are the ones that
  # hold them to their shape, so this root declares the value and decides
  # nothing about it. internal/infra composes the block (Orchestrator) for the
  # one door that writes it — `fugaro update`, for an installation that has the
  # credentials service and the Firebase backend — and every run after it
  # leaves the key exactly as it found it.
  default = null
}

# The switch on the minter rows that are not the issuer's (launcher-does-not-mint
# plan task LM12, D3121, R8), and it is the FIREBASE root's own switch under the
# same name: there it gates the people's row — the launchers' and the operators'
# fugaroTokenMinter grant on the runs' signer (LM11) — and here the resident
# orchestrator's row on that same signer, in the module's orchestrator.tf. One
# name in both roots, because the plan that moves the grant to the credentials
# service turns both off at once (LM14: `fugaro update` applies both roots with
# the switch off once the service has proved it signs as this signer, and the
# two rows are gone together). ON is the whole of what makes the update carrying
# this change a plan with nothing new in it, and what it never gates is the
# ISSUER's row: that row is what keeps the installation able to mint a run's
# identity while the grant moves (R8).
variable "people_minter" {
  type     = bool
  default  = true
  nullable = false
}
