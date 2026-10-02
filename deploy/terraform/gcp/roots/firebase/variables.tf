# The same inputs as the firebase module, which checks their shapes. See its
# variables.tf for what each one means. There is no remote state to read:
# fugaro init passes the installation's outputs as tfvars.

variable "project" {
  type = string
}

variable "fugaro_project" {
  type = string
}

variable "names" {
  type = object({
    signer_account_id = string
    minter_role_id    = string
    api_key           = string
  })
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

variable "admins" {
  type     = list(string)
  default  = []
  nullable = false
}

variable "budget_admins" {
  type     = list(string)
  default  = []
  nullable = false
}

variable "history_account" {
  type = string
}
