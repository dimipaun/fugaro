# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

variable "project" {
  description = "The dedicated Fugaro project the owner operates, the same one keyring/ applied to."
  type        = string
}

variable "environment" {
  description = "The environment this apply is for: with var.keyring it names the ring the signer grant is made on, and the key id inside it (D459). One apply, one environment."
  type        = string

  # The same list keyring/'s `keys` map names, written where a person reading this
  # root sees it: the two roots have to admit one set of environments, or one of
  # them plans against a ring the other never creates. TestRingHoldsOneEnvironmentsKey
  # holds the two together.
  validation {
    condition     = contains(["production", "staging"], var.environment)
    error_message = "environment must be production or staging."
  }
}

variable "region" {
  description = "The region both services run in, the key rings' location and the Firestore database's."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
  }
}

variable "keyring" {
  description = "The key ring's name stem keyring/ created this environment's ring with; the grant is made on this name and the environment joined by a dash."
  type        = string
  default     = "fugaro-licence"
}

variable "signer_role" {
  description = "The custom signer role keyring/ created, named here so the grant and the role are one id."
  type        = string
  default     = "fugaroKeySigner"
}

variable "keys" {
  description = "The environments the service mints for and each one's key id inside this environment's ring — keyring/'s own map, and the same default, because the two roots have to agree on the id the minter signs with: this one writes the pair into FUGARO_KEYS. An installation that moved a key id in keyring/ sets it here too, or the minter signs with a key name the ring does not hold."
  type        = map(string)

  default = {
    production = "fugaro-licence-production"
    staging    = "fugaro-licence-staging"
  }

  validation {
    condition     = contains(keys(var.keys), var.environment) && length(trimspace(var.keys[var.environment])) > 0
    error_message = "keys must name this apply's environment with the key id its ring holds: an apply that named no key for the environment it is deploying would mint in one it signed for in none."
  }
}

variable "api_account" {
  description = "The API's service account id."
  type        = string
  default     = "fugaro-portal-api"
}

variable "minter_account" {
  description = "The minter's service account id."
  type        = string
  default     = "fugaro-key-minter"
}

variable "scheduler_account" {
  description = "The service account Cloud Scheduler mints its OIDC tokens for. It is the account the API's sign-in check verifies, and it holds no IAM role in this project: the scheduler calls a public service and the API's own check is the fence (P1.3, F33)."
  type        = string
  default     = "fugaro-portal-scheduler"
}

variable "api_service" {
  description = "The API's Cloud Run service name."
  type        = string
  default     = "fugaro-portal-api"
}

variable "minter_service" {
  description = "The minter's Cloud Run service name."
  type        = string
  default     = "fugaro-key-minter"
}

variable "api_image" {
  description = "The API's image, built from ee/keyservice (S3.2) and pushed by the deploy (P9.2, S3.7). No default: a service pointing at an image nobody built would be a revision that never becomes ready."
  type        = string
}

variable "minter_image" {
  description = "The minter's image, built from the same source (S3.2)."
  type        = string
}

variable "handover_key" {
  description = "The handover ring's name and the name of the encryption key inside it: the key id is the ring's path plus this name, so one literal names both (D2182)."
  type        = string
  default     = "fugaro-portal-handover"
}

variable "handover_role" {
  description = "The custom role the API's account is granted on the handover ring: encrypt and decrypt, and nothing else (D2182)."
  type        = string
  default     = "fugaroHandoverSealer"
}

variable "statement_job" {
  description = "The monthly statement job's name."
  type        = string
  default     = "fugaro-portal-statements"
}

variable "reminder_job" {
  description = "The daily reminder job's name."
  type        = string
  default     = "fugaro-portal-reminders"
}

variable "statement_job_path" {
  description = "The path the statement job posts to on the API. The route is P6.3's, so this is the one place the two meet."
  type        = string
  default     = "/api/statements/run"
}

variable "reminder_job_path" {
  description = "The path the reminder job posts to on the API. The route is P7.1's."
  type        = string
  default     = "/api/reminders/run"
}

# The two jobs' pauses are inputs rather than literals in scheduler.tf, so that
# the apply which ships a route is also the apply that starts its job: a literal
# can only be changed from outside Terraform, and the next apply — a re-run, an
# image change, any infra change at all — writes the literal back and pauses the
# job again, mid-month and without a word. One variable per job rather than one
# for both, because the two routes ship in different tasks (P6.3 and P7.1) and a
# single switch would either mail reminders before their route exists or hold the
# statements back until it does.
variable "statements_paused" {
  description = "Whether the monthly statement job is created paused. Its route is P6.3's, so the default keeps it off; the deploy that ships the route turns it on by setting this false in the same apply."
  type        = bool
  default     = true
}

variable "reminders_paused" {
  description = "Whether the daily reminder job is created paused. Its route is P7.1's, and the same switch turns it on."
  type        = bool
  default     = true
}

variable "secrets" {
  description = "The secrets the services read, keyed by the name the deploy knows them by and valued with their Secret Manager ids. This root creates the empty containers; the deploy fills them (P9.2, S3.7). P1.7's config reads one by role, and main.tf's `secret_roles` is where a role and a container of this map meet. The four below are the ones the API cannot start without; an error DSN is P1.6's own and is off until an entry for it is added here (`sentry_dsn = \"<container>\"`), because naming the role for a container nobody filled is a revision that refuses at startup rather than a deployment that reports nowhere."
  type        = map(string)

  default = {
    paddle_api_key = "fugaro-portal-paddle-api-key"
    paddle_webhook = "fugaro-portal-paddle-webhook-secret"
    admin_secret   = "fugaro-portal-admin-secret"
    resend_api_key = "fugaro-portal-resend-api-key"
  }
}

# The API's environment, one variable per value the deployment decides. P1.7's
# program reads every one of them and refuses to start without one, and refuses
# a FUGARO_ variable it does not read (ee/keyservice/config.go); main.tf writes
# them into the container's env, and
# TestTheServicesSetEveryVariableTheProgramsRead reads that env against that
# file's own list, so a variable added to one side and not the other fails there
# rather than in a revision that never becomes ready.
#
# None has a default, and that is the decision rather than an omission: each of
# these is a fact about THIS deployment — the origin its mailed links point at,
# what it sells and caps, who its licensor is, which price it charges — and a
# default here would be a value nobody chose that an apply runs with. The other
# values the programs read are not variables at all: they are this root's own
# (the project, the environment, the scheduler's account, the sealer key, the
# minter's URL, the secret containers), and main.tf derives them.
variable "portal_url" {
  description = "The portal's own origin, which P1.7's Config.URL is: a mailed invitation or verification link is built by joining a path to it (invites.go's portalOrigin refuses anything else), and it is the site the SPA is served from (P0.1's app.fugaro.dev, or the staging host)."
  type        = string

  validation {
    condition     = can(regex("^https://[^/]+$", var.portal_url))
    error_message = "portal_url must be an https origin such as https://app.fugaro.dev, with no trailing slash and no path: the deployment joins a path to it, so a value with a path of its own puts every mailed link somewhere else."
  }
}

variable "plans" {
  description = "The JSON document FUGARO_PLANS is: the plans the routes mint under (free_seat, open_source, paid), the plans the page shows, and D454's per-tier mint caps — ee/keyservice/config.go's deployPlans decodes it strictly and refuses a document that names no plan or a cap that is not a rate. One document because the fields are one decision: what this deployment sells and what it allows."
  type        = string

  validation {
    condition     = can(jsondecode(var.plans))
    error_message = "plans must be one JSON object: the deployment's config decodes it as one document, and a value that is not JSON is a revision that does not start."
  }
}

variable "free_mail_domains" {
  description = "The domains §4.1's free-mail check refuses a free seat at, joined into FUGARO_FREE_MAIL_DOMAINS. D453 (where the list lives) is open: until its task pins one under ee/keyservice/, the deployment states it, and the list is what an applicant at a free provider is measured against."
  type        = list(string)

  validation {
    condition     = length(var.free_mail_domains) > 0
    error_message = "free_mail_domains must name at least one domain: the API refuses a variable set to nothing, so an empty list is a revision that does not start rather than a deployment with no free-mail check."
  }
}

variable "verification_ttl" {
  description = "How long a mailed verification link is good for (P1.7's Config.VerificationTTL), as a Go duration such as 72h: the token is single-use and expires (§4.1), and this is the expiry."
  type        = string

  validation {
    condition     = can(regex("^[0-9]+(\\.[0-9]+)?(ns|us|ms|s|m|h)", var.verification_ttl))
    error_message = "verification_ttl must be a Go duration such as 72h or 30m."
  }
}

variable "code_hosts" {
  description = "The code hosts §4.2's open-source check reads a named repository through, joined into FUGARO_CODE_HOSTS: the domains a repository URL is read against, so a URL at a host not named here is not one this deployment can check."
  type        = list(string)

  validation {
    condition     = length(var.code_hosts) > 0
    error_message = "code_hosts must name at least one host: the API refuses a variable set to nothing, and an empty list would leave every open-source application uncheckable."
  }
}

variable "max_repositories" {
  description = "The most repositories one open-source application may name (P1.7's Config.MaxRepositories): it is the deployment's own cap on the free route, and the API refuses a request that names more."
  type        = number

  validation {
    condition     = var.max_repositories >= 1 && floor(var.max_repositories) == var.max_repositories
    error_message = "max_repositories must be a whole number of at least one: a cap of none is not a cap, and the field is read as an integer."
  }
}

variable "throttle" {
  description = "The API's request rate per address and per IP, written `<requests>/<window>` (P1.7's Config.Throttle): 60/1h is sixty requests in an hour. It is a spelling of its own rather than a JSON document because a rate is one line an operator reads."
  type        = string

  validation {
    condition     = can(regex("^[1-9][0-9]*/[0-9]+(ns|us|ms|s|m|h)", var.throttle))
    error_message = "throttle must be a rate such as 60/1h: a count, a slash and a Go duration."
  }
}

variable "invite_rate" {
  description = "The API's own rate for invitations, written `<requests>/<window>` (P1.7's Config.InviteRate), the shape of throttle above: it is the limit a reseller's invitations are held to."
  type        = string

  validation {
    condition     = can(regex("^[1-9][0-9]*/[0-9]+(ns|us|ms|s|m|h)", var.invite_rate))
    error_message = "invite_rate must be a rate such as 20/1h: a count, a slash and a Go duration."
  }
}

variable "admin_account" {
  description = "The address the Licensor's admin page belongs to (§6, P1.7's Config.AdminAccount): the account whose session the page reads, so it is the deployment's own rather than a literal here."
  type        = string

  validation {
    condition     = can(regex("^[^@[:space:]]+@[^@[:space:]]+$", var.admin_account))
    error_message = "admin_account must be an email address such as owner@fugaro.dev: it is compared with the address a signed-in account has, and anything else compares with nothing."
  }
}

variable "admin_token_ttl" {
  description = "How long the admin page's own tokens are good for (P1.7's Config.AdminTokenTTL), as a Go duration: the page's requests carry a token signed with the admin secret, and this is its expiry."
  type        = string

  validation {
    condition     = can(regex("^[0-9]+(\\.[0-9]+)?(ns|us|ms|s|m|h)", var.admin_token_ttl))
    error_message = "admin_token_ttl must be a Go duration such as 15m."
  }
}

variable "paddle_price_id" {
  description = "The price the portal sells, in the owner's Paddle account (P0.2): the id a checkout names (P1.7's Config.PaddlePriceID), which is a value only that account has."
  type        = string

  validation {
    condition     = length(trimspace(var.paddle_price_id)) > 0
    error_message = "paddle_price_id must name the price this deployment sells: an empty one is a checkout that names nothing, and the API refuses a variable set to nothing."
  }
}

variable "tenants_collection" {
  description = "The document collection the organisations' lists are filed under (P1.7's Config.TenantsCollection, invites.go's WireOpsLists): it is part of the store's shape, so it is stated once here rather than written into two places."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_]*$", var.tenants_collection))
    error_message = "tenants_collection must be a Firestore collection name."
  }
}

variable "terms_version" {
  description = "The terms' version this deployment is on (P1.7's Config.TermsVersion): what a sign-up records as accepted (§P0.3 publishes the text, and this is the version beside it). It moves when counsel publishes a new one, which is why it is a value here."
  type        = string

  validation {
    condition     = length(trimspace(var.terms_version)) > 0
    error_message = "terms_version must name the version a sign-up records: the API refuses a variable set to nothing."
  }
}

variable "mail_from" {
  description = "The address the deployment's mail goes out as (P1.7's Config.MailFrom): the sender Resend has verified for the portal's domain, and resend.go refuses a mail path with none."
  type        = string

  validation {
    condition     = can(regex("^[^@[:space:]]+@[^@[:space:]]+$", var.mail_from))
    error_message = "mail_from must be an email address such as keys@fugaro.dev: it is the sender a provider will only accept for a domain it has verified."
  }
}

variable "notify_address" {
  description = "Where the owner is told a request is waiting in the manual queue (§4.5, P1.7's Config.NotifyAddress): the deployment's own address, because the person reading it is the licensor of this installation."
  type        = string

  validation {
    condition     = can(regex("^[^@[:space:]]+@[^@[:space:]]+$", var.notify_address))
    error_message = "notify_address must be an email address such as owner@fugaro.dev."
  }
}

variable "network" {
  description = "The network the API's egress runs over. It is this root's own rather than the project's default, for the reason the compute module's is: a default network arrives with its own firewall rules and with subnets nobody here reviewed."
  type        = string
  default     = "fugaro-portal-network"
}

variable "subnetwork" {
  description = "The subnet the API's revisions take their egress address from."
  type        = string
  default     = "fugaro-portal-subnet"
}

variable "subnet_cidr" {
  description = "The subnet's range. A /20 is small for the two services this project holds (Direct VPC egress consumes one address per revision instance) and wide enough that a scale-out never runs out of them."
  type        = string
  default     = "10.132.0.0/20"

  validation {
    condition     = can(cidrhost(var.subnet_cidr, 0))
    error_message = "subnet_cidr must be an IPv4 range such as 10.132.0.0/20."
  }
}

variable "router" {
  description = "The Cloud Router the NAT is created on. Named here rather than left to the provider, because the router is what a plan's review screen shows the NAT hanging from."
  type        = string
  default     = "fugaro-portal-router"
}

variable "nat" {
  description = "The Cloud NAT that carries the API's internet traffic. Paddle's API and Resend's are not Google's, so private Google access does not reach them, and an all-traffic egress with no NAT is an API that can reach the minter and nothing else."
  type        = string
  default     = "fugaro-portal-nat"
}

variable "manage_apis" {
  description = "Whether this root enables the APIs it needs. Off for a project whose owner enables them out of band."
  type        = bool
  default     = true
}
