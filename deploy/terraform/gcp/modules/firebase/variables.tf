# Every name and member arrives from fugaro init's terraform.tfvars.json,
# which reads the installation's outputs and the GCP project's IAM policy.
# The validation blocks check shapes only; nothing here derives a name.

variable "project" {
  description = "The Firebase project's ID (the FP, a GCP project of its own, which the user created and linked to billing). Everything here lives in it."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project))
    error_message = "project must be a project ID: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }
}

variable "fugaro_project" {
  description = "The Fugaro project's name. It is in the signer's description; the RTDB's /fugaro/project holds it too, written by fugaro init."
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

variable "launchers" {
  description = "The installation's launchers: members who start runs. Each gets the RTDB viewer role and the minter role on the signer."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.launchers : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "launchers must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "operators" {
  description = "The installation's operators, who get the same as launchers."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.operators : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "operators must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "admins" {
  description = "The GCP project's roles/owner and roles/editor members, which fugaro init --firebase reads from the project's IAM policy. They become budget admins (D6): roles/firebasedatabase.admin."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.admins : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "admins must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "budget_admins" {
  description = "Further budget admins, from terraform.budget_admins (fugaro init --budget-admin): roles/firebasedatabase.admin."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition     = alltrue([for m in var.budget_admins : can(regex("^(user|group|serviceAccount|domain):[^ ]+$", m))])
    error_message = "budget_admins must be IAM members: user:, group:, serviceAccount: or domain: followed by the address."
  }
}

variable "history_account" {
  description = "The email of the history job's account, which the installation root created (the first of init --firebase's three applies) so it exists before it is granted anything. It sweeps the RTDB registry and deletes expired run users."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\\.iam\\.gserviceaccount\\.com$", var.history_account))
    error_message = "history_account must be a service account email, <id>@<project>.iam.gserviceaccount.com."
  }
}
