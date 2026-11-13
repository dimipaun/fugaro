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

variable "secrets" {
  description = "The secrets the services read, keyed by the name the deploy knows them by and valued with their Secret Manager ids. This root creates the empty containers; the deploy fills them (P9.2, S3.7)."
  type        = map(string)

  default = {
    paddle_api_key = "fugaro-portal-paddle-api-key"
    paddle_webhook = "fugaro-portal-paddle-webhook-secret"
    resend_api_key = "fugaro-portal-resend-api-key"
    sentry_dsn     = "fugaro-portal-sentry-dsn"
  }
}

variable "manage_apis" {
  description = "Whether this root enables the APIs it needs. Off for a project whose owner enables them out of band."
  type        = bool
  default     = true
}
