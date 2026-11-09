# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

variable "project" {
  description = "The dedicated Fugaro project the owner operates, the same one keyring/ applied to."
  type        = string
}

variable "region" {
  description = "The region both services run in."
  type        = string

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]+$", var.region))
    error_message = "region must be a region name such as us-east5."
  }
}

variable "keyring" {
  description = "The key ring keyring/ created, which the signer role is granted on."
  type        = string
  default     = "fugaro-licence"
}

variable "signer_role" {
  description = "The custom signer role keyring/ created, named here so the grant and the role are one id."
  type        = string
  default     = "fugaroKeySigner"
}

variable "intake_account" {
  description = "The intake's service account id."
  type        = string
  default     = "fugaro-key-intake"
}

variable "minter_account" {
  description = "The minter's service account id."
  type        = string
  default     = "fugaro-key-minter"
}

variable "intake_service" {
  description = "The intake's Cloud Run service name."
  type        = string
  default     = "fugaro-key-intake"
}

variable "minter_service" {
  description = "The minter's Cloud Run service name."
  type        = string
  default     = "fugaro-key-minter"
}

variable "intake_image" {
  description = "The intake's image, built from ee/keyservice (S3.2) and pushed by the deploy (S3.7). No default: a service pointing at an image nobody built would be a revision that never becomes ready."
  type        = string
}

variable "minter_image" {
  description = "The minter's image, built from the same source (S3.2)."
  type        = string
}

variable "manage_apis" {
  description = "Whether this root enables the three APIs it needs. Off for a project whose owner enables them out of band."
  type        = bool
  default     = true
}
