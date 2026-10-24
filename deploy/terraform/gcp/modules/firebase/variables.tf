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

variable "coordinator_account" {
  description = "The email of the coordinator's writer account (D194): the aggregation job's own, which the installation root creates (P5) as it creates the history account's. When set, it is granted roles/firebasedatabase.admin here to write the coordinator's subtree under the rules' floor. Empty, the default until P5 creates the account, grants nothing: IAM refuses a member that does not exist."
  type        = string
  default     = ""

  validation {
    condition     = var.coordinator_account == "" || can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\\.iam\\.gserviceaccount\\.com$", var.coordinator_account))
    error_message = "coordinator_account must be empty or a service account email, <id>@<project>.iam.gserviceaccount.com."
  }
}
