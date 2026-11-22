# Every name and member arrives from fugaro update's terraform.tfvars.json,
# which reads the installation's outputs and the GCP project's IAM policy.
# The validation blocks check shapes only; nothing here derives a name.

variable "project" {
  description = "The Firebase project's ID (the FP, which the user created and linked to billing). It is either a GCP project of its own or the installation's own project; everything here lives in it."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project))
    error_message = "project must be a project ID: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }
}

# Where a NEW Realtime Database instance is created: the installation's region
# mapped to the nearest of the three locations Google offers (internal/place,
# D3089), computed by fugaro update and written into the tfvars. It is an
# input rather than a constant because the mapping's answer differs by
# installation; the three values are the whole of what Google offers, so
# anything else is a mistyped tfvars and is refused here rather than by the
# provider. An instance that already exists ignores this: the resource's
# region is a creation-time field and the module's ignore_changes keeps the one
# it has (F8, R4), which is what makes an installation whose mapping has since
# changed plan no change at all.
variable "database_region" {
  description = "The Realtime Database location for a NEW default instance: us-central1, europe-west1 or asia-southeast1, the mapping's answer for the installation's region. An instance that already exists keeps its own location."
  type        = string

  validation {
    condition     = contains(["us-central1", "europe-west1", "asia-southeast1"], var.database_region)
    error_message = "database_region must be one of the three Realtime Database locations: us-central1, europe-west1 or asia-southeast1."
  }
}

variable "fugaro_project" {
  description = "The Fugaro project's name. It is in the signer's description; the RTDB's /fugaro/project holds it too, written by fugaro update."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$", var.fugaro_project))
    error_message = "fugaro_project must be a project name: 1-40 characters of a-z, 0-9 and -, starting and ending with a letter or digit."
  }
}

variable "names" {
  description = "The singleton names in the FP."
  type = object({
    signer_account_id = string
    minter_role_id    = string
    api_key           = string
  })

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.names.signer_account_id))
    error_message = "names.signer_account_id must be a service account ID: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }

  validation {
    condition     = can(regex("^[a-zA-Z0-9_.]{3,64}$", var.names.minter_role_id))
    error_message = "names.minter_role_id must be a custom role ID: 3-64 characters of letters, digits, _ and ."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{0,61}[a-z0-9]$", var.names.api_key))
    error_message = "names.api_key must be an API key ID: 2-63 characters of a-z, 0-9 and -, starting with a letter."
  }
}

variable "manage_apis" {
  description = "Whether to enable the APIs the FP uses. Set it to false when they are managed elsewhere."
  type        = bool
  default     = true
  nullable    = false
}

# Whether the FP's Firestore (default) database is there, which `fugaro update`
# reads before this root is applied (internal/infra's FirestoreStep.
# DatabaseExists) and passes here. It gates the run index's composite indexes
# (firestore_indexes.tf, RI2): an index can only be created under a database
# that exists, and `fugaro update` ensures that database as the LAST step of the
# stage that applies this root (D3027) — so a fresh installation plans none of
# them here and gains them on the next update. False is the default because it
# is the answer that cannot fail: a plan carrying no index is applied whatever
# the project holds.
variable "firestore_database_exists" {
  description = "Whether the FP's Firestore (default) database is there, as fugaro update read it before this root was applied. It gates the run index's composite indexes: an index under a database that does not exist is refused, and fugaro update ensures that database after this root."
  type        = bool
  default     = false
  nullable    = false
}

variable "skip_apis" {
  description = "APIs this root does not enable because the installation root, in the same GCP project, does (set by fugaro update when the FP is the installation's own project; empty with two projects)."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for a in var.skip_apis : can(regex("^[a-z0-9]+\\.googleapis\\.com$", a))])
    error_message = "skip_apis must be service names such as iam.googleapis.com."
  }
}

variable "launchers" {
  description = "The installation's launchers: members who start runs. Each gets the RTDB viewer role and the minter role on the signer."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.launchers : can(regex("^(user|group|serviceAccount):[^ *]+$", m))])
    error_message = "launchers must be IAM members: user:, group: or serviceAccount: followed by one address (no domain: and no wildcard: these members get roles on the FP)."
  }
}

variable "operators" {
  description = "The installation's operators, who get the same as launchers."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.operators : can(regex("^(user|group|serviceAccount):[^ *]+$", m))])
    error_message = "operators must be IAM members: user:, group: or serviceAccount: followed by one address (no domain: and no wildcard: these members get roles on the FP)."
  }
}

# The switch on the people's row (launcher-does-not-mint plan task LM11,
# D3121): whether the launchers' and operators' fugaroTokenMinter row on the
# runs' signer is still planned. ON, and that default is what makes the first
# update carrying this change a plan with nothing new in it; `fugaro update`
# turns it off in the one plan that removes the row, once the credentials
# service has proved it signs as this signer (LM14, R8). It gates the people's
# row alone — the issuer's below is not this switch's, and neither is the
# viewers': a viewer never held a way to mint an identity.
variable "people_minter" {
  description = "Whether launchers and operators hold the minter row on the runs' signer. On until `fugaro update` moves the grant to the issuer, which turns it off in the plan that removes the row."
  type        = bool
  default     = true
  nullable    = false
}

# The viewer list (RS4 of docs/roles-scopes/implementation-plan.md, roles.md
# §4): the members who read the database and the spend history and nothing
# else. They are the one list besides the two above that is handed to this
# root, and they are deliberately NOT part of local.people: the token-minter
# grant is that local's, and a viewer who could mint a token for any identity
# in the project would hold by claim everything this list withholds (roles.md
# §4, F44).
variable "viewers" {
  description = "Members who read and nothing else: roles/firebasedatabase.viewer for the database and roles/datastore.viewer for the spend history. No minter, no admin, no write."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.viewers : can(regex("^(user|group|serviceAccount):[^ *]+$", m))])
    error_message = "viewers must be IAM members: user:, group: or serviceAccount: followed by one address (no domain: and no wildcard: these members get roles on the FP)."
  }
}

variable "admins" {
  description = "The GCP project's roles/owner and roles/editor members, which fugaro update reads from the project's IAM policy. They become budget admins (D6): roles/firebasedatabase.admin."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.admins : can(regex("^(user|group|serviceAccount):[^ *]+$", m))])
    error_message = "admins must be IAM members: user:, group: or serviceAccount: followed by one address (no domain: and no wildcard: these members get roles on the FP)."
  }
}

variable "budget_admins" {
  description = "Further budget admins, from terraform.budget_admins (fugaro update --budget-admin): roles/firebasedatabase.admin."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.budget_admins : can(regex("^(user|group|serviceAccount):[^ *]+$", m))])
    error_message = "budget_admins must be IAM members: user:, group: or serviceAccount: followed by one address (no domain: and no wildcard: these members get roles on the FP)."
  }
}

variable "history_account" {
  description = "The email of the history job's account, which the installation root created (the first of fugaro update's three applies) so it exists before it is granted anything. It sweeps the RTDB registry and deletes expired run users."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\\.iam\\.gserviceaccount\\.com$", var.history_account))
    error_message = "history_account must be a service account email, <id>@<project>.iam.gserviceaccount.com."
  }
}

variable "mobile_account" {
  description = "The email of the public mobile service's runtime account (mobile plan task P5; D2782 as amended by F31, O1, O8): the account the installation root creates (modules/installation/mobile_service.tf) and the service runs as, handed over here because this is the root that creates the account it SIGN AS. When set, iam.tf creates the device-only signer account and grants this account iam.serviceAccounts.signJwt on it — the one grant, in the one plan, that makes a custom token a phone can redeem. Empty, which is every installation until the credentials image runs the mode the module starts that service in, creates neither the signer nor a grant naming anyone: IAM refuses a member that does not exist, and the installation root is applied BEFORE this one on every `fugaro update` (internal/cli/init_stages.go), so a grant left there would be a plan that fails the first update that carried it."
  type        = string
  default     = ""

  validation {
    condition     = var.mobile_account == "" || can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\\.iam\\.gserviceaccount\\.com$", var.mobile_account))
    error_message = "mobile_account must be empty or a service account email, <id>@<project>.iam.gserviceaccount.com."
  }
}

variable "coordinator_account" {
  description = "The email of the coordinator's writer account (D194): the aggregation job's own, which the installation root creates (P5) as it creates the history account's. When set, it is granted roles/firebasedatabase.admin here to write the coordinator's subtree under the rules' floor. Empty, the default until P5 creates the account, grants nothing: IAM refuses a member that does not exist."
  type        = string
  default     = ""

  validation {
    condition     = var.coordinator_account == "" || can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\\.iam\\.gserviceaccount\\.com$", var.coordinator_account))
    error_message = "coordinator_account must be empty or a service account email, <id>@<project>.iam.gserviceaccount.com."
  }
}

# The issuer's own account (launcher-does-not-mint plan task LM11, D3121, F7,
# F8): the credentials service's runtime account, which the installation root
# creates (modules/installation/credentials.tf) and `fugaro update` hands over
# here because THIS is the root that owns the account it signs AS — the runs'
# signer. When set, iam.tf plans iam.serviceAccounts.signJwt on that signer for
# it: the one row that lets a run's identity be minted without a launcher's
# machine, and the row that takes the people's grant's place (the spec's ask 1).
# Empty — the default of every installation until LM13's update passes the
# account once the installation root has created it — plans no row at all: IAM
# refuses a member that does not exist, and an installation whose issuer is not
# named yet must keep exactly the plan it had.
variable "issuer_account" {
  description = "The email of the credentials service's account, which mints the runs' custom tokens from the release that moves the grant. When set, it holds iam.serviceAccounts.signJwt on the runs' signer here. Empty names no account and plans no row: IAM refuses a member that does not exist."
  type        = string
  default     = ""

  validation {
    condition     = var.issuer_account == "" || can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\\.iam\\.gserviceaccount\\.com$", var.issuer_account))
    error_message = "issuer_account must be empty or a service account email, <id>@<project>.iam.gserviceaccount.com."
  }
}
