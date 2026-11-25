# The same inputs as the firebase module, which checks their shapes. See its
# variables.tf for what each one means. There is no remote state to read:
# fugaro update passes the installation's outputs as tfvars.

variable "project" {
  type = string
}

variable "database_region" {
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

variable "skip_apis" {
  type     = list(string)
  default  = []
  nullable = false
}

# Whether the FP's Firestore (default) database is there, as `fugaro update`
# reads it before this root is applied; the module's variables.tf states what it
# gates (the run index's composite indexes, RI2) and why its default is false.
variable "firestore_database_exists" {
  type     = bool
  default  = false
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

variable "viewers" {
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

variable "coordinator_account" {
  type    = string
  default = ""
}

# The issuer's own account and the switch on the people's row
# (launcher-does-not-mint plan task LM11): `fugaro update` writes the
# credentials service's account here once the installation root has created it,
# and leaves the switch on until the plan that moves the grant (LM14). The
# module's variables.tf states what each one does; the defaults are what an
# installation that has not been updated carries, and they plan exactly what
# this root planned before this change.
variable "issuer_account" {
  type    = string
  default = ""
}

variable "people_minter" {
  type     = bool
  default  = true
  nullable = false
}

# The public mobile service's runtime account (mobile plan task P5; D2782 as
# amended by F31, O1, O8): the account the installation root creates, handed
# over here because the signer it signs AS is created here. It is empty in every
# tfvars `fugaro update` writes — the service is off until a released image runs
# its mode — and the module then creates neither the device-only signer nor a
# grant naming anyone.
variable "mobile_account" {
  type    = string
  default = ""
}

variable "workspace_service_account" {
  type    = string
  default = ""
}
