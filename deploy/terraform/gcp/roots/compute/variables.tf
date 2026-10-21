# The same inputs as the compute module, which checks their shapes. See its
# variables.tf for what each one means and why it is required.

variable "project" {
  type = string
}

variable "project_number" {
  type = string
}

variable "region" {
  type = string
}

# Where the module's Cloud Scheduler jobs live, which is not var.region
# whenever Cloud Scheduler does not serve that region: us-east5, the
# documented installation, is one it does not, and a Scheduler job created
# there fails the apply. fugaro init resolves it (the installation's
# scheduler_region, or internal/backend/gcp's SchedulerRegion) and writes it
# here; the module's variables.tf has the whole of why.
variable "scheduler_region" {
  type = string
}

variable "network" {
  type = string
}

variable "subnetwork" {
  type = string
}

variable "names" {
  type = object({
    job_prefix            = string
    reaper_account_id     = string
    reaper_role_id        = string
    preload_account_id    = string
    preload_role_id       = string
    preload_image         = string
    preload_scheduler_job = string
  })
}

variable "manage_apis" {
  type    = bool
  default = true
}

variable "launchers" {
  type = set(string)
}

variable "operators" {
  type = set(string)
}

variable "run_accounts" {
  type = set(string)
}

variable "registry_repository" {
  type = string
}

variable "base_image" {
  type = string
}

variable "base_project" {
  type    = string
  default = "batch-custom-image"
}

variable "base_family" {
  type    = string
  default = "batch-cos-stable-official"
}

variable "preload_zone" {
  type = string
}

variable "preload_schedule" {
  type    = string
  default = "0 3 * * 1"
}

variable "preload_refresh_days" {
  type    = number
  default = 30
}

variable "builder_machine_type" {
  type    = string
  default = "e2-medium"
}

variable "build_submitter_role" {
  type = string
}

variable "reaper_refuse_projects" {
  type    = list(string)
  default = []
}
