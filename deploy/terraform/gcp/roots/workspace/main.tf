# The workspace root: the workspace's OWN service in the workspace's own
# project (docs/workspaces/implementation-plan.md W3, spec.md §2, §5, §9), and
# the SA fallback's IAM account (WU3 of
# docs/workspace-projects-users/implementation-plan.md, D2803).
#
# A workspace has a project of its own — the project the workspace is named
# after, whose config carries the owner's half of the membership (D1290, D1291)
# — and its Firebase, its Realtime Database, its signer and the owner's own
# people reach it through the installation's roots, exactly as any project's do:
# `fugaro setup --name <workspace>` created that project and `fugaro update`
# converges it. What no other root creates is the workspace's SERVICE: the
# account the workspace's own bundle runs as (W6), which reads the record at
# workspaces/<name>, keeps the admitted-token hashes (D1296) and meters the
# calls the workspace's credentials pay for (W7) — all of it over IAM, because
# the database's rules deny every client (internal/budget/rules/workspace.go).
#
# So this root creates the account, the one role that names its reach, the ONE
# grant that binds them, the SA fallback's account with no role of its own, and
# — once the workspace's owner has a member table and an image to deploy
# (var.credentials_service) — the workspace's own credentials service, the ONE
# accessor on the workspace's own bundle container, the ONE conditioned accessor
# that reads a person's own Claude token (R6, D2971), and one invoke grant per
# member job account and per person of the workspace's roster. That "nothing
# else" is the security property, not a
# simplification: spec §9 has a workspace never holding another project's runs,
# buckets, jobs or repositories, and the whole point of D1290's answer is that
# the workspace's project is nobody's member. A job, a runs bucket or a
# repository deployed here would be a workspace whose compromise reaches a
# member's work, and an installation's converge would be the thing that put it
# there.
#
# The service (D2980) is the same image and the same code an installation's
# credentials service runs, in the workspace's own project, told what it is by
# its environment alone: it reads the ONE bundle container this project holds,
# serves a member's runs the model and forge routes and refuses every other
# (R14), and is invoked by a member's own job accounts and by nobody else. It
# runs as the account above, which is what makes the account's reach the whole of
# the workspace's model access — one database grant for the record and the meter,
# one accessor on the bundle, and one conditioned accessor on a person's own
# token (R6).
#
# The person's own token is why the roster's people are here at all (WC2): the
# workspace's credentials half serves a person's Claude OAuth token, so the
# person invokes the service and the account reads exactly that person's
# container and no other secret of the project (R6, D2971). The people come from
# the workspace's own roster node, rendered into var.people by `fugaro update`.
#
# fugaro update writes this root's terraform.tfvars.json into a workdir of its
# own (internal/infra's WorkspaceWorkdir, state fugaro/workspace) and plans and
# applies it there, the way it does the Firebase root's. The provider
# enablement the workspace's sign-in needs is NOT here: Identity Platform is
# initialized with no provider by `fugaro setup`'s own step, which is right for
# a member's project and wrong for a workspace's, so the workspace's providers
# are an owner-run step listed in the release notes and checked in
# docs/gcp-live-checklist.md (W3, W9).
terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
  }

  backend "gcs" {}
}

# The workspace's own project, and API quota is billed to it, as it is with user
# credentials and a quota project. It is the project the workspace is named
# after.
provider "google" {
  project               = var.project
  billing_project       = var.project
  user_project_override = true
}

locals {
  # The whole of the service half of this root hangs on the one variable: null
  # creates no service, no bundle accessor and no invoke grant, and a single
  # boolean keeps the three of them in step.
  credentials_service_on = var.credentials_service != null

  # The member table's job accounts, flattened: one invoke grant per account,
  # and the map's keys are the members' names — which is what makes an account
  # that leaves the table a grant that is deleted and nothing else (F25).
  member_job_accounts = toset(flatten([for m in values(var.members) : m.job_service_accounts]))

  # The roster's people, as a set: one invoke grant per person's address, and a
  # person the roster no longer names is a grant that is deleted and nothing
  # else (F25, R6). It is a set rather than the list, so a roster that names the
  # same address twice cannot make terraform bind one address twice.
  roster_people = toset(var.people)

  # The URL a revision carries on the apply that CREATES the service: Cloud Run
  # assigns the real one, nothing here may read the service's own uri from inside
  # its resource, and the update reads it back from this root's output and writes
  # it in a second apply. Fail-closed either way, and it is a name nothing
  # resolves rather than a URL that could belong to someone else.
  credentials_service_url_unassigned = "https://workspace-credentials-url-unassigned.invalid"
}

# The workspace's own service: the identity the workspace's bundle runs as. It
# is a Fugaro account of THIS project and of no other, so a member's project
# never holds it and a member holds no grant on this one (spec §5).
resource "google_service_account" "workspace" {
  project      = var.project
  account_id   = var.names.workspace_account_id
  display_name = "Fugaro workspace service"
  description  = "The workspace ${var.workspace}'s own service: it reads the workspace record at workspaces/${var.workspace}, keeps the admitted-token hashes and meters the calls the workspace's credentials pay for."
}

# What that account reaches, as a role of its own: the database's two DATA
# permissions and nothing else.
#
# It is a custom role and not roles/firebasedatabase.admin for the reason
# .claude/rules/terraform.md gives — "list the exact permissions and use the
# narrowest custom role" — and for the reading SECURITY.md makes of the
# credentials service's own role: a Realtime Database IAM grant has no
# path-level scope and no condition can fence one (spec §8), so a grant here is
# the whole database whatever it is called, and the only narrower thing left to
# choose is WHICH OPERATIONS it allows. admin adds the instance lifecycle —
# instances.list, create, delete, disable, reenable, undelete — on top of the
# two the workspace's own service uses; nothing in this design creates, deletes
# or disables a database, and the identity that holds the workspace's record and
# its admitted-token hashes must not be the one that can destroy the database
# they live in.
#
# get is the read (the record at workspaces/<name>, the admitted-token hashes,
# the member ledgers the alarm compares) and update the write, because a
# Realtime Database node has no separate create or delete and the meter is one
# transaction that reserves the room and one that settles what the call cost
# (W7). The same pair is what the credentials service's database role holds
# (deploy/terraform/gcp/modules/installation/credentials.tf), which is the
# reviewed precedent for it. A third permission is a decision a person makes
# here AND in internal/infra/tf/workspace.go together: the plan cover pins this
# id and these two permissions (WorkspaceNotCovered).
resource "google_project_iam_custom_role" "workspace_database" {
  project     = var.project
  role_id     = "fugaroWorkspaceDatabase"
  title       = "Fugaro workspace service (database, data only)"
  description = "The workspace ${var.workspace}'s own service's reach over the workspace's own database: the instance's two data permissions, and no lifecycle permission."
  permissions = [
    "firebasedatabase.instances.get",
    "firebasedatabase.instances.update",
  ]
}

# The one grant: a member resource and never a binding, so nothing here takes
# access away from anyone else.
#
# The role is spelled as the literal path projects/<project>/roles/<id> and not
# as the role resource's own name attribute, because a grant whose role is
# computed cannot be read out of a plan — internal/infra/tf's cover reads this
# one, and a role it cannot read is a role it cannot hold to the id above. The
# member is google_service_account.workspace.member, which IS the
# serviceAccount:<address> string IAM wants, rather than a hand-built one that
# could drift from it.
#
# It is bound to THIS project's account and to no other member: the workspace's
# own service is the only identity that reaches the workspace's own database,
# and a plan granting the role to anything else is refused by internal/infra/tf
# (WorkspaceNotCovered), which is where the decision is enforced rather than
# here.
resource "google_project_iam_member" "workspace_database" {
  project    = var.project
  role       = "projects/${var.project}/roles/fugaroWorkspaceDatabase"
  member     = google_service_account.workspace.member
  depends_on = [google_project_iam_custom_role.workspace_database]
}

# The SA fallback's identity (D2803, WU3): the account a member's owner grants
# roles/owner on their project — and on their Firebase project when it is a
# separate one — when there is no organization to move the project into, and
# which the workspace's admins only ever IMPERSONATE (WU20).
#
# Two accounts, never one (F20). This one holds roles/owner on every linked
# member; the account above reads the workspace's own database and the members'
# ledgers. Neither is the credentials service's account, which holds no write on
# a member at all (D1299).
#
# The root creates it and grants it NOTHING. Its token creators are the
# workspace admins, written live by `workspace user add --admin` (D2818), so a
# grant of this account in a plan would be a role no `fugaro update` asked for —
# and internal/infra/tf's WorkspaceNotCovered refuses any grant of this root
# whose member is not the service account above.
#
# NO KEY IS WRITTEN HERE, and this comment is what "no key" means: the root
# writes none, which is not the claim that none can exist. The account lives in
# a project Fugaro does not own alone, and two paths make one anyway —
# iam.serviceAccounts.actAs, which roles/editor and roles/iam.serviceAccountUser
# carry, and iam.serviceAccountKeys.create, which roles/editor resolves to. The
# organization policy constraints/iam.disableServiceAccountKeyCreation is the
# only control that closes the second; WU11's `status` takes up both. The tftest
# case beside this root says the same, and
# TestWorkspaceRootMakesNoKeyForTheIAMAccount reads this file for the first half.
#
# The id is a literal rather than a var.names field, as the role's is, so the
# plan carries the name internal/infra/tf pins (WorkspaceIAMAccountID) and no
# value could differ from it.
resource "google_service_account" "workspace_iam" {
  project      = var.project
  account_id   = "fugaro-workspace-iam"
  display_name = "Fugaro workspace IAM"
  description  = "The workspace ${var.workspace}'s IAM account: the identity a member's owner grants roles/owner on their project when the workspace has no folder (D2803). The workspace's admins impersonate it, and Terraform writes no key for it."
}

# The workspace's own credentials service (D2980): the same image an
# installation's credentials service runs, the same code, one process, and the
# workspace's half of it switched on by its environment alone
# (FUGARO_WORKSPACE_MODE, var.workspace). It is a service of the workspace's own
# project, running as the account above, and it is gated on
# var.credentials_service: with that unset this file creates the account, the
# role and its grant and nothing else.
#
# What it reads is the ONE bundle container of THIS project (the accessor
# below), and what it serves is a member's runs: the model routes and the forge
# routes a run calls, with every other route — review signing and verification,
# slips, workflow secrets, build tokens, the read mints, the merge, label and
# issue routes — refused in workspace mode with a typed refusal, a member's run
# keeping its own project's service for those (R14, D2988).
#
# The three numbers below are R9's and they are written out literally rather
# than left to the provider: a maximum of ten instances, a concurrency of forty
# per instance and a minimum of zero at BOTH levels. The provider computes the
# service-level one otherwise, and the managed gate prices a Cloud Run service by
# its minimum instance count — an attribute terraform computes is unknown at plan
# time, and a service whose floor cannot be read is refused as unpriceable. Zero
# is also the shape this service has: nothing keeps an instance warm, and a
# member's run pays a cold start rather than the workspace paying around the
# clock for one.
#
# deletion_protection stays false, so an owner who removes the service from the
# tfvars can remove it; what is worth keeping is the bundle container, and that
# one is not created here at all.
resource "google_cloud_run_v2_service" "workspace" {
  count = local.credentials_service_on ? 1 : 0

  project  = var.project
  location = var.credentials_service.region
  name     = var.credentials_service.service
  labels = {
    fugaro      = "managed"
    fugaro_role = "workspace"
  }
  ingress = "INGRESS_TRAFFIC_ALL"

  # IAM is required, stated rather than implied: this is the fence every caller
  # passes through, and the process's own ID-token check is layered on top of it,
  # never instead of it. The members' invoke grants below are what admits a
  # caller through it.
  invoker_iam_disabled = false

  scaling {
    scaling_mode       = "AUTOMATIC"
    min_instance_count = 0
  }

  deletion_protection = false

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "workspace"
    }
    service_account = google_service_account.workspace.email

    # A model call is a stream and a git push is a long upload, so the revision
    # holds a request for an hour, the way the installation's service does.
    timeout = "3600s"

    scaling {
      min_instance_count = 0
      max_instance_count = 10
    }

    max_instance_request_concurrency = 40

    containers {
      image = var.credentials_service.image

      # The port the container listens on, and its name is what Cloud Run reads:
      # `h2c` is HTTP/2 cleartext, and it is not optional here. The front end in
      # front of the container buffers an HTTP/1 request body and refuses one a
      # large git pack exceeds (413, the owner's Check 190); a member's run
      # reaches this service for its git traffic too (the forge routes of R14),
      # so the pack has to reach the process over HTTP/2 — which the process
      # itself speaks on its one listener. 8080 is Cloud Run's own default and
      # the number the process falls back to when PORT is unset.
      ports {
        name           = "h2c"
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # The whole of this process's configuration, and the workspace's service
      # reads the same six names an installation's does (the values differ only
      # in which project they name) plus the one that switches its workspace half
      # on. No credential is among them, and no secret ID either: the service
      # reads its bundle as the account above, by the name it derives from the
      # workspace's own name — the same derivation
      # var.credentials_service.bundle_secret carries, which is what the accessor
      # below is granted on — so nothing in this environment is a value a
      # credential could be read out of.
      #
      # internal/infra/tf's WorkspaceServiceEnv is the allow-list that refuses a
      # container setting anything else at plan time, and these seven names are
      # exactly it.
      env {
        name  = "FUGARO_WORKSPACE_MODE"
        value = var.workspace
      }
      env {
        name  = "FUGARO_PROJECT"
        value = var.workspace
      }
      env {
        name  = "FUGARO_GCP_PROJECT"
        value = var.project
      }
      env {
        name  = "FUGARO_REGION"
        value = var.credentials_service.region
      }
      env {
        name  = "FUGARO_RTDB_URL"
        value = var.credentials_service.rtdb_url
      }
      env {
        name  = "FUGARO_FIREBASE_PROJECT"
        value = var.credentials_service.firebase_project
      }
      # The service's own URL: the audience a member's run mints its Google ID
      # token for, and the one thing that binds such a token to THIS service.
      # Cloud Run assigns it, so the update that creates the service writes the
      # placeholder and reads the real URL back from this root's own output on the
      # apply after it. Fail-closed either way: a revision that guessed its own
      # audience would accept a token minted for another service, and one that
      # served with no audience would halt every call while looking healthy.
      env {
        name  = "FUGARO_CREDENTIALS_SERVICE_URL"
        value = coalesce(var.credentials_service.url, local.credentials_service_url_unassigned)
      }
    }
  }
}

# The ONE accessor this service has in Secret Manager (F4, Q6): the workspace's
# own bundle container, read by the workspace's own account, and nothing else.
# One container and one grant, unconditioned and per-project rather than
# per-credential, because every model credential the workspace holds is one YAML
# document the loader reads in one go (the same rule the installation's service
# follows).
#
# The container's ID is a variable and not a literal here, and it is the value
# the workspace's own installation already created: a workspace's own project IS
# an installation named after the workspace, so the container its credentials
# service writes is the container this service reads
# (internal/credsvc/WorkspaceBundleSecretID). A second derivation written here
# would be a second container.
resource "google_secret_manager_secret_iam_member" "workspace_bundle" {
  count = local.credentials_service_on ? 1 : 0

  project   = var.project
  secret_id = var.credentials_service.bundle_secret
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.workspace.member
}

# One invoke grant per member job account (F17): a member's own run reaches the
# workspace's service with the identity its launcher already gives it, which is
# the identity the service resolves a caller by, and it reaches that service and
# no other.
#
# It is a member resource and never a binding, so a member leaving the table
# deletes its grant and nobody else's: `fugaro update` converges that with no
# operator repair (F25), and internal/infra/tf's cover admits that one deletion
# and no other.
resource "google_cloud_run_v2_service_iam_member" "workspace_invoker" {
  for_each = local.credentials_service_on ? local.member_job_accounts : toset([])

  project  = var.project
  location = var.credentials_service.region
  name     = var.credentials_service.service
  role     = "roles/run.invoker"
  member   = "serviceAccount:${each.value}"

  depends_on = [google_cloud_run_v2_service.workspace]
}

# One invoke grant per person of the workspace's roster (R6, D2971): a person
# signs in and reaches the workspace's own service with their own Google
# identity, which is what lets the workspace's credentials half serve them, and
# they reach that service and no other.
#
# It is a member resource and never a binding, so a person the roster drops
# deletes their grant and nobody else's: `fugaro update` converges that with no
# operator repair (F25), and internal/infra/tf's cover admits that deletion and
# the members' and no other. The member is the ADDRESS itself — `user:<address>`
# — and never a service account: a roster's `sa` entries are the members' job
# accounts, which the invoker grant above is bound to, and the root's own
# validation refuses an entry that is not an address and one in that class — a
# machine's own address — because IAM refuses a `user:` member that is a machine.
resource "google_cloud_run_v2_service_iam_member" "workspace_person_invoker" {
  for_each = local.credentials_service_on ? local.roster_people : toset([])

  project  = var.project
  location = var.credentials_service.region
  name     = var.credentials_service.service
  role     = "roles/run.invoker"
  member   = "user:${each.value}"

  depends_on = [google_cloud_run_v2_service.workspace]
}

# The ONE conditioned accessor the workspace's own account holds (R6, D2971): a
# person's own Claude OAuth token, and no other secret of the workspace's
# project.
#
# It is per-project and conditioned, and both halves are the point. The
# container is one per person and its name is the verified token's own `sub`
# (`fugaro_u_<sub>_claude-oauth-token`, internal/secretname), which no plan
# knows — so a per-container grant cannot be written here at all — and the
# condition is what narrows a project-wide one to exactly the class the
# workspace's service serves: the per-person prefix AND the name that ends in
# the token's own. The prefix alone would hand the workspace's account every
# personal secret a person keeps in the workspace's project, and the name alone
# would reach a container of another project's naming. It carries the intake's
# own extraction template and deliberately not its expression (intake.tf), which
# admits the shared `fugaro_p_` class too: the workspace serves no member through
# one — what is shared is a shared entry (D2976) — and the workspace's account
# must hold none of it.
#
# internal/infra/tf's cover holds the title and the expression to
# gcp.WorkspacePersonTokenConditionTitle and gcp.WorkspacePersonTokenCondition()
# byte for byte, and refuses a wider one (WorkspaceNotCovered).
resource "google_project_iam_member" "workspace_person_token" {
  count = local.credentials_service_on ? 1 : 0

  project = var.project
  role    = "roles/secretmanager.secretAccessor"
  member  = google_service_account.workspace.member

  condition {
    title      = "fugaro-workspace-person-token"
    expression = "resource.name.extract(\"/secrets/{secret}\").startsWith(\"fugaro_u_\") && resource.name.extract(\"/secrets/{secret}\").endsWith(\"_claude-oauth-token\")"
  }
}
