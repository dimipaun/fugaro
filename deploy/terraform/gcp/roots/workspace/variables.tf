# The workspace root's inputs, written by fugaro update into
# terraform.tfvars.json. The shapes are checked here; nothing derives a name.

variable "project" {
  description = "The workspace's own project: the GCP project the workspace is named after, which holds the workspace's Firebase, its database and its service. It is the project the workspace owner's `fugaro setup --name <workspace>` created."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project))
    error_message = "project must be a project ID: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }
}

variable "workspace" {
  description = "The workspace's name: the Fugaro project name of the installation that publishes the record, and the last segment of the path the record lives at (workspaces/<name>). It reuses the project-name grammar (config.ProjectNameRE), because a workspace is named the way a project is."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$", var.workspace))
    error_message = "workspace must be a project name: 1-40 characters of a-z, 0-9 and -, starting and ending with a letter or digit."
  }
}

variable "names" {
  description = "The singleton names of the workspace's own project."
  type = object({
    workspace_account_id = string
  })

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.names.workspace_account_id))
    error_message = "names.workspace_account_id must be a service account ID: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }
}
