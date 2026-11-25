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

variable "members" {
  description = "The workspace owner's own member table, as the invoke grants are made from: one entry per member, keyed by the member's Fugaro project name, each carrying the member's job service accounts — the identities the workspace's service resolves a caller by. An empty map (the default) makes no invoke grant, and the owner's `fugaro update` is what converges a member added or removed."
  type = map(object({
    job_service_accounts = list(string)
  }))
  default = {}

  validation {
    condition = alltrue([
      for m in values(var.members) : alltrue([
        for sa in m.job_service_accounts : can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\\.iam\\.gserviceaccount\\.com$", sa))
      ])
    ])
    error_message = "members.*.job_service_accounts must be IAM service account addresses (<id>@<project>.iam.gserviceaccount.com): the invoke grant is bound to the address itself, so an entry that is not one would grant roles/run.invoker on the workspace's own service to whatever was written there."
  }
}

variable "people" {
  description = "The workspace roster's people: the addresses of the workspace roster's `human` entries, one invoke grant on the workspace's own credentials service per address (R6, D2971). fugaro update renders them from the workspace's own roster node (workspaceRosters/<name>). An empty list (the default) makes no person's invoke grant, and a person the roster no longer names is a grant deleted and nothing else."
  type        = list(string)
  default     = []

  # A roster row is a person or a machine by its kind AND by its address (WC2's
  # review): the member below is rendered from the address alone, and IAM refuses
  # a `user:` member that is a machine's — so a row marked a `human` at a service
  # account's address fails at apply, in the API's own words about the member and
  # not in a line an owner can trace to the roster row that caused it. The class
  # is refused here instead: `<id>@<project>.iam.gserviceaccount.com` is the one
  # spelling a machine's address has — the same one `members` above holds a job
  # account to — and a machine the workspace admits is admitted as a member,
  # through the invoker grant of that variable and never as a person of this one.
  validation {
    condition = alltrue([
      for p in var.people :
      can(regex("^[^@[:space:]]+@[^@[:space:]]+$", p)) && !can(regex("\\.iam\\.gserviceaccount\\.com$", p))
    ])
    error_message = "people must be the workspace roster's own people (a person's email address, and never a machine's): the invoke grant is bound to the address itself as `user:<address>`, so an entry that is not an address would grant roles/run.invoker on the workspace's own service to whatever was written there, and a service account's address (`<id>@<project>.iam.gserviceaccount.com`) is the roster's `sa` kind, whose callers are the members' own job accounts — IAM refuses a `user:` member that is a machine."
  }
}

variable "credentials_service" {
  description = "The workspace's own credentials service (D2980): the same image an installation's service runs, in the workspace's own project, told what it is by its environment. Null — the default — creates no service, no bundle accessor and no invoke grant, which is the state of a workspace whose owner has not deployed it yet. The image's host, project and repository are checked against region, project and the remote repository below."
  type = object({
    service          = string
    image            = string
    region           = string
    bundle_secret    = string
    firebase_project = string
    rtdb_url         = string
    url              = optional(string)
  })
  default = null

  validation {
    condition     = var.credentials_service == null || can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.credentials_service.service))
    error_message = "credentials_service.service must be a Cloud Run service name: 6-30 characters of a-z, 0-9 and -, starting with a letter."
  }

  # The image is held to the shape an installation's own credentials service is
  # deployed with, because it is that same image running the same code (D2980)
  # and every instance of it reads every model credential the workspace holds:
  #
  #   <region>-docker.pkg.dev/<project>/fugaro-ghcr/<owner>/fugaro-<kind>:<version>@sha256:<64 hex>
  #
  # The ONE part this root cannot state is the owner: it is a fact of the
  # machine's recorded config (localcfg's image_source), no variable here carries
  # it, and internal/infra/tf's credentialsImageOK — the rule the root's cover
  # applies to the plan before this file is ever read — is where it is checked.
  # Everything a tfvars could otherwise choose is stated here: another project's
  # registry, another registry host, the project's own writable repositories
  # (fugaro-base, which every operator holds artifactregistry.writer on, so a
  # digest they pushed and named there is theirs to choose), a bare tag or a
  # namespace that is not a fugaro-<kind> release. See
  # modules/compute/variables.tf's base_image, which is the same rule for the
  # preload pin and the same reason for the same variables.
  validation {
    condition = var.credentials_service == null || can(regex(
      "^${var.credentials_service.region}-docker\\.pkg\\.dev/${var.project}/fugaro-ghcr/[^/]+/fugaro-[a-z0-9]+(?:-[a-z0-9]+)*:[0-9]+\\.[0-9]+\\.[0-9]+@sha256:[0-9a-f]{64}$",
      var.credentials_service.image,
    ))
    error_message = "credentials_service.image must be this installation's released pin served through the remote repository: <region>-docker.pkg.dev/<project>/fugaro-ghcr/<owner>/fugaro-<kind>:<version>@sha256:<64 lower-case hex digits>, with the host and project this root's own region and project. A tag is mutable, another registry or another repository means the image is somebody else's or an operator's own push, and every instance of this service reads every model credential the workspace holds."
  }

  validation {
    condition     = var.credentials_service == null || can(regex("^[a-z]+-[a-z]+[0-9]+$", var.credentials_service.region))
    error_message = "credentials_service.region must be a region name such as us-east5."
  }
}
