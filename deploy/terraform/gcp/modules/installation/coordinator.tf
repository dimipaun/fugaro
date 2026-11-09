# The coordinator's aggregation job (coordinator plan P5, D194): a scheduled
# Cloud Run job that re-reads GitHub through the project-wide read (P2),
# recomputes the repository views (P1) and writes them into the coordinator's
# subtree (P3), recording what it could not reconcile instead of half a view.
# It is the deterministic half of the coordinator, and the coordinator account
# below is the only identity that writes the coordinator's state — the front
# door at the bottom of this file has an account of its own, and that one
# writes nothing anywhere.
#
# None of this exists unless the owner opts in (fugaro update
# --firebase-project <id> --coordinator); var.coordinator is null otherwise.
#
# The account is created in the first of fugaro update's applies, so the
# Firebase root can grant it roles/firebasedatabase.admin on the FP — that
# root grants nothing to an account that does not exist yet (P3). The job, the
# App key's grant and the schedule arrive in the third, once the FP's outputs
# exist (coordinator.deploy_job), the way the history job's do.
#
# What this account may write is not IAM's to say: firebasedatabase.admin is a
# project-wide role (IAM cannot express a path, D194), so the fence is
# ee/coordinator's own subtree check (Store's ErrOutsideSubtree) and the
# rules' floor. Everything else here is the narrowest grant the job needs:
# objectViewer on the runs bucket, secretAccessor on the App key's one
# container, and the job_runner role on this job alone.
#
# The front door's own account is at the bottom of this file, and the two are
# deliberately not the same one: its comment says what the sweep holds that a
# public endpoint must not reach.

locals {
  # coordinator is the owner's opt-in, and it is the whole switch: fugaro
  # writes the block only after fugaro update --firebase-project --coordinator
  # (recorded as terraform.coordinator in the local config), so an
  # installation that never asked — every installation by default — plans
  # exactly what it planned before, and the Firebase root's own gate is the
  # same fact (its coordinator_account is empty). The history job hangs off
  # enable_budget alone because every budget backend has one; the coordinator
  # needs both, and it is internal/cli's initFirebase that refuses the one
  # combination this cannot see (an opt-in with no account in the
  # installation's outputs).
  coordinator_on     = var.enable_budget && var.coordinator != null
  deploy_coordinator = local.coordinator_on && try(var.coordinator.deploy_job, false)

  # Whether this installation's sweep reads a GitHub repository at all, which is
  # the whole of whether it needs the App credential (F24). The sweep's GitHub
  # path mints the coordinator's own read-only installation token from the App
  # key, and its Bitbucket path (BB5) mints a read token at the credentials
  # service instead — so an installation whose repositories are all Bitbucket
  # has nothing to sign with and no container to mount.
  #
  # It is asked of the repositories rather than of the config: a repository the
  # local config records no provider for is not in var.coordinator.repos
  # (internal/infra's CoordinatorRepos), and one that is in it names its
  # provider, so "is there a github entry" is exactly "does the sweep read
  # GitHub".
  coordinator_github = local.deploy_coordinator && anytrue([
    for r in var.coordinator.repos : r.provider == "github"
  ])

  # The front door's own switch (D934), and the only thing that creates the
  # account and the service below: var.mcp_server is null on every installation
  # that has not asked for one, and the coordinator's opt-in is required beside
  # it because a front door serves the coordinator's state and there is nothing
  # for one to answer about on an installation that has no coordinator. The
  # variable's own validation refuses the one combination this local would
  # hide: an opt-in with no coordinator, which would otherwise plan nothing and
  # say nothing.
  mcp_server_on = var.mcp_server != null && local.coordinator_on
}

resource "google_service_account" "coordinator" {
  count = local.coordinator_on ? 1 : 0

  project      = var.project
  account_id   = var.coordinator.account_id
  display_name = "Fugaro coordinator"

  depends_on = [google_project_service.this]
}

# Nobody is granted actAs on this account, operators included: it holds
# firebasedatabase.admin on the FP, so anyone who could deploy a job as it
# could rewrite every view the coordinator holds (design D6, the history
# account's comment). Executing the existing job needs no actAs; only the
# applier that creates or updates the job acts as it.

# The runs bucket, read-only: a sweep reads every run a repository holds, which
# is what the view is computed from. objectViewer on this bucket and never
# objectAdmin, with no condition (unverified assumption A3, the history
# account's grant): the coordinator writes no object. Granted whenever the
# account exists, like the account itself.
resource "google_storage_bucket_iam_member" "coordinator_runs_reader" {
  count = local.coordinator_on ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.coordinator[0].member
}

# The App key, and the job is its only reader: the sweep mints the
# coordinator's own read-only installation token from it (P2), so this grant
# arrives with the job and not with the account. It is granted at the secret
# level and to this one account — never project-wide, never to a group — and
# the container's own env entry is what names the container, one grant and one
# mount, as the project module's jobs have it.
#
# It arrives with the job AND with a GitHub repository to read (F24): a sweep
# whose repositories are all Bitbucket reads them through the credentials
# service (BB5) and never touches the App, so an installation that has one is
# not an installation that must hold the other. The precondition below is the
# other half of that count and not a contradiction of it: it is evaluated only
# where an instance exists, which is exactly where an App key is needed.
resource "google_secret_manager_secret_iam_member" "coordinator_app_key" {
  count = local.coordinator_github ? 1 : 0

  project   = var.project
  secret_id = var.project_secrets[var.coordinator.app_key_secret]
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.coordinator[0].member

  lifecycle {
    precondition {
      condition     = contains(keys(var.project_secrets), var.coordinator.app_key_secret)
      error_message = "coordinator.app_key_secret names a project-wide secret container this installation does not create: project_secrets comes from the local config's own provider secrets (fugaro update's discovery unions the live ones in), so an installation whose config records no GitHub provider has no container to grant."
    }
  }
}

resource "google_cloud_run_v2_job" "coordinator" {
  count = local.deploy_coordinator ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.coordinator.job
  labels = {
    fugaro      = "managed"
    fugaro_role = "coordinator"
  }
  deletion_protection = true

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "coordinator"
    }
    task_count = 1

    template {
      max_retries           = 0
      timeout               = "600s"
      service_account       = google_service_account.coordinator[0].email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image   = var.coordinator.image
        command = ["/usr/local/bin/fugaro"]
        args    = ["coordinator", "sweep"]

        resources {
          limits = {
            cpu    = "1"
            memory = "512Mi"
          }
        }

        # The job's whole configuration is the environment
        # ee/coordinator/cli/coordinator.go's sweep reads: the project, the Firebase
        # project and database it writes, the runs bucket it reads, the
        # repositories one tick reconciles, and — each only where the sweep
        # reads that provider — the App credential and the credentials
        # service's URL.
        env {
          name  = "FUGARO_PROJECT"
          value = var.fugaro_project
        }
        env {
          name  = "FUGARO_FIREBASE_PROJECT"
          value = var.coordinator.firebase_project
        }
        env {
          name  = "FUGARO_RTDB_URL"
          value = var.coordinator.rtdb_url
        }
        env {
          name  = "FUGARO_RUNS_BUCKET"
          value = var.runs_bucket
        }
        # The repositories one tick reconciles, as the JSON list the command
        # decodes ([{"provider":"github","repo":"owner/name"}, ...]): the
        # installation's own, because the job has no checkout to read a
        # provider out of, and a repository the config does not name a
        # provider for is not in it.
        env {
          name  = "FUGARO_COORDINATOR_REPOS"
          value = jsonencode(var.coordinator.repos)
        }
        # The credentials service's own URL (BB5, F23), and deliberately NOT
        # FUGARO_CREDENTIALS_SERVICE_URL: the sweep's bitbucket opener reads
        # this one and nothing else, so the run path's variable cannot move the
        # root a token is minted at, and a job that has one and not the other is
        # a job whose Bitbucket reads are its own. It is rendered only when the
        # service is on, and its value is the service's — the same coalesce the
        # service's own environment reads, so an installation that has not read
        # its URL back yet hands the sweep the same placeholder it hands the
        # service. Without it, a Bitbucket repository stays StaleUnsupported
        # (G8): the sweep never guesses a service.
        dynamic "env" {
          for_each = local.credentials_service_on ? [coalesce(var.credentials_service.url, local.credentials_service_url_unassigned)] : []
          content {
            name  = "FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL"
            value = env.value
          }
        }
        # The App credential, and only where the sweep reads GitHub (F24): a
        # Bitbucket-only installation deploys the job without either entry, and
        # the sweep reads its repositories through the service above.
        dynamic "env" {
          for_each = local.coordinator_github ? [var.coordinator.github_app_id] : []
          content {
            name  = "FUGARO_GITHUB_APP_ID"
            value = env.value
          }
        }
        # The one credential, and the only place it ever appears: a
        # secret_key_ref, so no plan, no `terraform show` and no job describe
        # carries the App's private key.
        dynamic "env" {
          for_each = local.coordinator_github ? [var.coordinator.app_key_secret] : []
          content {
            name = "FUGARO_GITHUB_APP_PRIVATE_KEY"
            value_source {
              secret_key_ref {
                secret  = var.project_secrets[env.value]
                version = "latest"
              }
            }
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.coordinator_app_key]
}

# fugaroJobRunner (run.jobs.run) on this job only, for the scheduler account
# below: the same role and the same job-scoped grant the history job's sweep
# has, and never roles/run.invoker, which the history root's comment explains
# (a body with overrides needs runWithOverrides, and this tree grants one role
# for both).
resource "google_cloud_run_v2_job_iam_member" "coordinator_runner" {
  count = local.deploy_coordinator ? 1 : 0

  project  = var.project
  location = google_cloud_run_v2_job.coordinator[0].location
  name     = google_cloud_run_v2_job.coordinator[0].name
  role     = google_project_iam_custom_role.job_runner.name
  member   = google_service_account.scheduler.member
}

# The sweep, every 15 minutes: the coordinator is early warning (spec §6), and
# a tick is cheap — one read of each repository's runs and of the pull requests
# they name. Scheduler isn't offered in every Cloud Run region, so it runs in
# the scheduler region and the URI names the job's own.
#
# A failed sweep logs its failure and the next tick is the retry (retry_count
# 0, the history sweep's rule): the job's own timeout is 600s and the code
# stops a tick at 8 minutes (internal/cli's coordinatorSweepTimeout), so a
# sweep that ran out of time ends cleanly and the next one continues rather
# than two passes of one repository running at once.
resource "google_cloud_scheduler_job" "coordinator_sweep" {
  count = local.deploy_coordinator ? 1 : 0

  project   = var.project
  region    = var.coordinator.scheduler_region
  name      = var.coordinator.scheduler_job
  schedule  = "*/15 * * * *"
  time_zone = "Etc/UTC"

  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.coordinator[0].location}/jobs/${google_cloud_run_v2_job.coordinator[0].name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.coordinator_runner]
}

# The front door's own account, and the whole of why it is not the sweep's
# (M4's "its own service account"): the sweep's account holds the GitHub App's
# private key, a Bitbucket read at the credentials service and
# firebasedatabase.admin over the FP, and an identity holding any of those
# behind a public endpoint would make spec.md §8's "there is no credential to
# reach" false the day the front door is turned on. This one holds one grant —
# the runs bucket's read, below — and nothing else in this tree or the Firebase
# root grants it anything: no secret, no database, no actAs, which is the
# narrowest account the design names for M4.
#
# It is created with the front door's own opt-in and not with the coordinator's:
# an installation that opted into the coordinator for its read-only view
# acquires no account at all, which is the same rule the service and the
# binding follow.
#
# The database half of what a front door needs is NOT here and deliberately so:
# the store binding is D935, whose bucket and one binding live in
# mcpauth_store.tf — beside the bucket they bind rather than in this file,
# which names this account exactly twice (the runs bucket's read below, and the
# service's own identity) and must keep doing so. What this account reaches is
# therefore the runs bucket (D932) and the store's objects (D935), and nothing
# else.
resource "google_service_account" "mcp_server" {
  count = local.mcp_server_on ? 1 : 0

  project      = var.project
  account_id   = var.mcp_server.account_id
  display_name = "Fugaro MCP server"

  depends_on = [google_project_service.this]
}

# The runs bucket, read-only, and the account's one grant: a run's recorded
# artefacts — its logs and its diff — are what the tools behind this port
# answer with (D932), and the answer the owner did not choose (streaming Cloud
# Logging live) is the reason it is the whole of the read: objectViewer on this
# bucket reaches no log line outside a run, and no other object in the project.
#
# objectViewer and never objectAdmin, with no condition (unverified assumption
# A3, the sweep account's grant): the front door writes no object, and the
# bucket's root is something no read can change.
resource "google_storage_bucket_iam_member" "mcp_server_runs_reader" {
  count = local.mcp_server_on ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.mcp_server[0].member
}

# Nobody is granted actAs on this account either, operators included, for the
# reason the sweep account's own comment gives: whoever could deploy a revision
# as it would serve the front door's answers — and every run artefact behind
# them — as the installation's own identity.

# The coordinator's front door (D920, D934): the one endpoint a standard MCP
# client reaches without a Google identity. It is a Cloud Run SERVICE and not
# the job above — the job is a scheduled, finite sweep, and this one is
# resident, answerable at any moment, and reachable by everyone on the network
# (spec.md §8, "Who can call the front door": there is no network gate, no VPN,
# no source allowlist and no mutual TLS, and the handler is the only gate).
#
# OFF by default. The whole switch is var.mcp_server, and no CLI writer sets it
# yet: `fugaro update` has no --mcp-server flag on this branch, so the variable
# arrives only from a tfvars a later stage writes, and the release that turns
# the front door on adds the flag (M5's) and its line to the release notes'
# ### For operators section (D934). The account above and this service exist
# only for an installation whose owner asked: one that opted into the
# coordinator for its read-only view acquires neither, and one that never asked
# plans exactly what it planned before this file existed.
#
# What the front door is NOT is what makes it safe to be public. It carries no
# model credential and no forge credential — no secret_key_ref, no value_source,
# no credential-shaped environment name, which
# coordinator_service_test.go's TestCoordinatorServiceIsPublicAndCarriesNoCredential
# holds against this very block — and the identity behind the port is an
# account whose whole reach is the runs bucket's read, so a stolen access token
# reaches the tools its scopes name and nothing else (spec.md §8, "What a
# stolen access token reaches": there is no credential to reach).
#
# M9's launch grant is deliberately absent, and the absence is the design:
# spawn_task is the one privilege this work adds, it belongs to M9's own
# change, and until it lands nothing here can start a run. When it lands it is
# a grant on the project's own job, added here (the shared-files table), and
# this comment is what says the service it is granted for already exists.
resource "google_cloud_run_v2_service" "coordinator_front_door" {
  count = local.mcp_server_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.mcp_server.service
  labels = {
    fugaro      = "managed"
    fugaro_role = "coordinator-mcp"
  }

  # The public internet, because that is who calls it: an MCP client reaches
  # the endpoint over its run.app URL, and INGRESS_TRAFFIC_INTERNAL_ONLY would
  # refuse every caller there is. The fence is not the network — it is the
  # bearer token the handler checks, and the audit record every refusal and
  # every call writes (M10) — which is why the ingress is stated here rather
  # than left to the provider's default.
  ingress = "INGRESS_TRAFFIC_ALL"

  # IAM is required, and the public binding below is the grant D920 decided:
  # stated explicitly rather than left to the provider's default, so the front
  # end checks IAM before the container runs and a later change of that default
  # cannot open the service silently (credentials.tf's rule for the same flag).
  # The binding is visible in the plan, which is the point — a public endpoint
  # is a deliberate act and internal/infra/tf's cover refuses an allUsers
  # member by name, so the update that creates this asks its own confirmation.
  invoker_iam_disabled = false

  # The service holds no data of its own: every token record, client
  # registration and audit line lives in the mcpauth subtree (D922), which is a
  # subtree of the database rather than a resource of this plan, so no apply can
  # remove it with this flag. Turning the front door off is therefore one
  # ordinary apply with var.mcp_server unset, and deletion_protection stays
  # false so that apply can do its work.
  deletion_protection = false

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "coordinator-mcp"
    }

    # The front door's OWN account, never the default compute account and never
    # the sweep's (the account's comment above): a revision reaching a
    # credential through its identity is the one thing a public endpoint must
    # not do, and the sweep's identity reaches the App key and the whole
    # database. Nobody is granted actAs on this one either, so no person and no
    # deployer can run a revision as it.
    service_account = google_service_account.mcp_server[0].email

    # Scale to zero (D196's own shape): a public endpoint nobody calls costs
    # nothing, and a cold start is what a caller waits for rather than what the
    # installation pays for around the clock. The concurrency and rate limits
    # that bound a caller's cost are the front door's own and are enforced at
    # M9's single launch point, not here.
    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.mcp_server.image

      # One CLI binary serves the aggregation job and the front door
      # (internal/infra's Coordinator reads the same history image), so the
      # front door names the command it runs rather than an image built for
      # it: the resident server, which answers on Cloud Run's PORT. Without
      # the pair below the image's own default entry point would run
      # `fugaro version` and exit — a revision that never becomes ready, and
      # an apply that fails after the owner asked for a front door.
      command = ["/usr/local/bin/fugaro"]
      args    = ["coordinator", "serve"]

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # The state the front door serves, in the names the sweep already reads:
      # the project, the Firebase project and database the coordinator's
      # subtrees live in, and the runs bucket a run's artefacts (its logs and
      # its diff) are read from (D932). None of them is a credential, no entry
      # here has a value_source, and naming the database is not a grant on it:
      # what this account may read there is D935's store binding, which is the
      # one binding mcpauth_store.tf writes, so the front door reaches the runs
      # bucket and the store's own bucket and nothing else. The store's URL is
      # deliberately not an entry yet: the process that opens it (M5) names the
      # bucket it was handed, and the name it must be handed is the installation
      # output mcpauth_store_bucket.
      env {
        name  = "FUGARO_PROJECT"
        value = var.fugaro_project
      }
      env {
        name  = "FUGARO_FIREBASE_PROJECT"
        value = var.coordinator.firebase_project
      }
      env {
        name  = "FUGARO_RTDB_URL"
        value = var.coordinator.rtdb_url
      }
      env {
        name  = "FUGARO_RUNS_BUCKET"
        value = var.runs_bucket
      }
      # The repositories the front door may answer about, in the same JSON
      # shape the sweep's own FUGARO_COORDINATOR_REPOS carries: a token's
      # repository filter is applied on top of this list, never instead of it,
      # so naming a repository the installation does not hold is refused
      # rather than resolved.
      env {
        name  = "FUGARO_COORDINATOR_REPOS"
        value = jsonencode(var.coordinator.repos)
      }

      # The port is left unnamed, and that is the HTTP/1.1 default: Cloud Run's
      # own is 8080, which is also the number the process falls back to when
      # PORT is unset. credentials.tf's comment (and the local above it) says
      # why the h2c name exists there and why it must follow the image; the
      # front door's transport is decided by M5's server, and a name this
      # release cannot read out of an image would be a claim about a process
      # nobody has written yet.
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }
}

# D920's public invoker binding, and the whole of the endpoint's reachability:
# roles/run.invoker to allUsers, on this service and no other. It is written
# beside the service rather than inside it because that is where a plan's
# review screen can see it, and internal/infra/tf's cover refuses an allUsers
# member by name — the member kind the table's row for this type deliberately
# does not carry — so the update that carries this plan stops and asks the
# owner to confirm it in so many words.
#
# Every unauthenticated path this opens is a metadata document or an
# authorization endpoint with no work behind it, and the MCP endpoint itself
# answers 401 to a request with no token (M5, spec.md §8): the invoker grant
# admits a caller to the CONTAINER, and the handler is still the gate.
resource "google_cloud_run_v2_service_iam_member" "coordinator_front_door_public" {
  count = local.mcp_server_on ? 1 : 0

  project  = var.project
  location = google_cloud_run_v2_service.coordinator_front_door[0].location
  name     = google_cloud_run_v2_service.coordinator_front_door[0].name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
