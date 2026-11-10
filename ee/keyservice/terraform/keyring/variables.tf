# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

variable "project" {
  description = "The dedicated Fugaro project the owner operates (S3.8 creates it; this root never does)."
  type        = string
}

variable "region" {
  description = "The key ring's location. A key ring cannot move after creation, and the services that sign with it run where they are cheapest to run, so this names the keys' home rather than the service's."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
  }
}

variable "keyring" {
  description = "The key ring's name: one ring holds both keys (D459(a))."
  type        = string
  default     = "fugaro-licence"
}

variable "keys" {
  description = "The environments the service mints for, and each one's key id. The id names the environment (S2.1, §2), so a production verifier refuses a staging key rather than half-accepting it."
  type        = map(string)

  default = {
    production = "fugaro-licence-production"
    staging    = "fugaro-licence-staging"
  }
}

variable "signer_role" {
  description = "The custom role the minter's account is granted on the key ring. It is this root's because the ring it is granted on is, and services/ names the same id to grant it."
  type        = string
  default     = "fugaroKeySigner"
}

variable "manage_apis" {
  description = "Whether this root enables the three APIs it needs. Off for a project whose owner enables them out of band."
  type        = bool
  default     = true
}
