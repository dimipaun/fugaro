# The job's service account and everything it is granted. These exist even
# when the job itself isn't deployed yet, so its secrets and image can be
# prepared first.

resource "google_service_account" "job" {
  project      = var.project
  account_id   = var.workflow.service_account.account_id
  display_name = var.workflow.service_account.display_name
}

# Deploying or updating the job acts as its account.
resource "google_service_account_iam_member" "operator" {
  for_each = toset(var.operators)

  service_account_id = google_service_account.job.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}

# The runs bucket, conditioned on the repository's runs/, cache/ and locks/
# prefixes. The condition is the input byte for byte: one that differs is a
# different binding, which would sit next to the bootstrap's. The bootstrap
# set no description, so none is set here.
resource "google_storage_bucket_iam_member" "job" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectUser"
  member = google_service_account.job.member

  condition {
    title      = var.workflow.bucket_condition.title
    expression = var.workflow.bucket_condition.expression
  }
}

# The repository's own budget token objects, tokens/<slug>/, read-only: the
# launcher writes the sealed token there and the run reads it, and a run can
# neither delete nor replace a queued run's token (objectViewer has no
# delete or create; docs/budget-token-channel/spec.md section 7.2, D16).
# The runs/, cache/ and locks/ grant above never covers tokens/. The
# precondition re-checks the bucket and the prefix, beside the variable's own
# validation of the shape and the slug.
resource "google_storage_bucket_iam_member" "job_tokens" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectViewer"
  member = google_service_account.job.member

  condition {
    title      = var.workflow.token_condition.title
    expression = var.workflow.token_condition.expression
  }

  lifecycle {
    precondition {
      condition     = startswith(var.workflow.token_condition.expression, "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/tokens/") && !strcontains(var.workflow.token_condition.expression, "||")
      error_message = "workflow.token_condition must name tokens/ of this runs bucket, in one clause (gcp.TokenBucketCondition)."
    }
  }
}

locals {
  # D145: the credentials the credentials service holds once an installation
  # opts in, and which the job's own account therefore keeps no accessor on.
  # There are three families, and they are identified differently:
  #
  #   * the MODEL credentials, by the VARIABLE each one is mounted as. The
  #     three names are internal/config's (ReservedSecrets' Claude OAuth token
  #     and Anthropic key, and ProviderKeyEnvPrefix), which is also where
  #     config.ReservedEnvPrefixes refuses a workflow secret any variable that
  #     could impersonate one — so a workflow's secret_env entry names a model
  #     credential exactly when its variable is one of these. A provider's key
  #     has no fixed logical name (it is whatever the owner's local config
  #     calls it), so the variable is the only thing that identifies it.
  #   * the GITHUB APP'S PRIVATE KEY, by its LOGICAL NAME. It is a reserved
  #     name with exactly one mount, so the name identifies it — and matching
  #     the name is what leaves Bitbucket's token and every workflow's own
  #     secret alone. D145 leaves Bitbucket untouched: a Bitbucket credential
  #     is a repository access token, issued for one repository, which no
  #     service can mint per job (F7).
  #   * a PROXIED REPOSITORY'S GIT CREDENTIAL, by its LOGICAL NAME too, through
  #     local.is_proxied_git below. That one is held for ONE repository rather
  #     than for the installation, which is why it has a list of its own
  #     (proxied_git_secret_names) and a per-repository input beside it (G9,
  #     G15).
  #
  # TestServiceHeldSecretsMatchTheCredentialsService pins these four literals
  # and both expressions below to Go, and
  # TestProxiedGitSecretNamesMatchTheModule pins the proxied list, the local it
  # feeds and the third clause both expressions carry.
  model_secret_env_names  = ["ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"]
  model_secret_env_prefix = "FUGARO_PROVIDER_KEY_"

  # The Claude account token's variable, named a second time because the one
  # exemption in the model clauses below (is_stage_account_token) has to read
  # it: Go exempts the mount by that variable too (dropModelMounts' anthropic
  # arm), and the guard test holds this literal to
  # config.ReservedSecrets[config.ClaudeOAuthToken] so the two spellings of it
  # cannot drift.
  claude_oauth_token_env = "CLAUDE_CODE_OAUTH_TOKEN"

  # The App key's name, and it is EMPTY until the release that also stops the
  # job reading the key (C2.2, review finding F1): config.ServiceHeldSecretNames
  # is the switch, this literal is held to it by the guard test, and Go's
  # internal/infra mirrors the same list. With the name in it and the runner
  # still reading FUGARO_GITHUB_APP_PRIVATE_KEY, this module would take the
  # job's accessor away from a container that still mounts the key — a job
  # Cloud Run refuses to create, so every GitHub run of an installation that
  # opted in would fail. Until then the job keeps the accessor and the mount,
  # and a GitHub run works exactly as it did before this file knew about the
  # service.
  service_held_secret_names = []

  # The Bitbucket token's own flip list (B1.8, D500): the logical names of the
  # git credentials the credentials service brokers for a repository the
  # published node admits with `proxy: true`. A list of its own, beside
  # service_held_secret_names, because its condition is the repository's rather
  # than the installation's (G15): a name here drops the accessor and the mount
  # of a PROXIED repository's job alone, and every other Bitbucket repository —
  # of an installation that opted in or not — keeps both (G9).
  #
  # It is FILLED, which is the flip itself (B1.13 part two, D500): the flip
  # ships in 0.26.0, the release after the proxy chain's and after the one
  # that went out without it (the owner released the hold on 2026-11-16). No
  # version gate holds this fill — it fires wherever the two inputs above
  # hold — and `fugaro update` itself refuses the drop until the bundle holds
  # the token (O8).
  # config.ProxiedGitSecretNames is the switch, this literal is held to it by
  # deploy/terraform's TestProxiedGitSecretNamesMatchTheModule, and the two are
  # filled in one commit. It moves nothing on its own: the repository's own
  # input (bitbucket_proxied, false for every repository the published node does
  # not admit with `proxy: true`) and the installation's opt-in gate every
  # clause below, and the runner reads no token on a proxied repository (B1.7),
  # so the accessor and the mount go together with nothing left reading them.
  proxied_git_secret_names = ["bitbucket-token"]

  # The classes of secret the credentials service FETCHES for this job (B3.8,
  # the S3 flip; D2072's shape, G17): a class named here is served by the
  # service rather than mounted by the job, so this repository's job gives up
  # the accessor and the mount of every secret of that class and the service is
  # given its own accessor on each container instead (service_secret_names).
  #
  # The class is config.VaultSecretClassWorkflow — a workflow's OWN `secrets:`,
  # which is the one class D1267's ruling puts on this path — and this literal
  # is held to config.VaultFetchedSecretClasses by deploy/terraform's
  # TestVaultFetchedClassesMatchTheModule, the way the two lists above are.
  #
  # It carries "workflow" since the flip's second commit filled it, and the
  # inert state is the empty list: with it empty local.vault_secret_names is the
  # empty list whatever Go wrote, so the two clauses below drop nothing and
  # service_secret_names is byte for byte the set B3.3 gave it. Filling it moves
  # nothing on its own either: the names it moves are Go's per-workflow answer
  # (var.workflow.vault_secret_names, empty for every repository the switch
  # holds off), so a repository whose own switch is off keeps every accessor and
  # every mount (G9, D147).
  vault_fetched_secret_classes = ["workflow"]

  # The workflow's own secrets whose values the credentials service fetches for
  # THIS job, from Go's own answer (WorkflowSpec.VaultSecretNames, written into
  # the tfvars as var.workflow.vault_secret_names): the same names Go's
  # dropVaultMounts took off the job's spec, and no others. Read through the
  # class list above so that the two halves of the flip — which class is
  # fetched, and which names of it this workflow declares — are one rule, and
  # an empty class list leaves this variable unread.
  vault_secret_names = contains(local.vault_fetched_secret_classes, "workflow") ? var.workflow.vault_secret_names : []

  # One entry per mount: this mount is a proxied git credential exactly when
  # the node admits the repository AND its logical name is one the list names.
  # Both halves are needed — the list alone would take the token from a
  # repository nobody proxied, the input alone from every Bitbucket repository
  # of an installation that opted in — and both clauses below read this one
  # local, so the two cannot drift apart.
  is_proxied_git = {
    for env, logical in var.workflow.secret_env :
    env => var.bitbucket_proxied && contains(local.proxied_git_secret_names, logical)
  }

  # One entry per mount: this mount is the Claude account token on a mixed STAGE
  # ACCOUNT's own variant job — the one mount B2.7's flip leaves standing. An
  # api-key repository may declare stage keys (B0's ruling of 2026-11-04,
  # docs/mixed-credentials/spec.md §11), and its `review`, `adversarial` and
  # `triage` stages then go out on a job of that account's own, mounting
  # CLAUDE_CODE_OAUTH_TOKEN so the RUNNER can hand the account's token straight
  # to Anthropic. The run's gateway never sees those calls, so the credentials
  # service has no call of theirs to serve, and taking the mount would leave the
  # stage with a variable nothing mounted — Check 290's failure. The fact comes
  # from Go (var.workflow.stage_account, WorkflowSpec.StageAccount), which is
  # also what exempts the mount there: the two halves are ONE rule, because a
  # job whose account keeps an accessor the mount no longer has — or the
  # reverse — is a job Cloud Run refuses to create (C1.20). Every other job of
  # the repository, the base variant included, carries false and gives the
  # token up.
  is_stage_account_token = {
    for env, logical in var.workflow.secret_env :
    env => var.workflow.stage_account && env == local.claude_oauth_token_env
  }

  # The named model credentials the input says the service serves THIS workflow
  # (the Claude account token and the Anthropic key), with the one exception
  # above. Read apart from the provider-key clause so that the exception has one
  # place to live and the arms below read one answer per mount.
  is_run_model_secret = {
    for env, logical in var.workflow.secret_env :
    env => var.model_secrets_served != "none" && contains(local.model_secret_env_names, env) && !(var.model_secrets_served == "anthropic" && local.is_stage_account_token[env])
  }

  # One entry per mount: this mount is a MODEL credential the credentials
  # service serves — the first of the three families above — exactly when the
  # input says so. The input is Go's answer for this repository
  # (infra.ModelSecretsServed, written into the repository root's tfvars as
  # model_secrets_served) and it has three arms, because the two halves of the
  # family are served at different times (D2072, G17):
  #
  #   * `all` — the cascade: the service holds the account's token AND every
  #     provider key, which is the state every switched repository was in
  #     before B2.3, and the arms a tfvars written then means by saying
  #     nothing (the variable's own default).
  #   * `anthropic` — an `api-key` or `oauth` repository the switch admits
  #     AND config.ServiceServedAuths carries (B2.3's switch, B2.7's flip):
  #     the service serves its runs the Anthropic key or the account's token,
  #     and it serves them NOTHING else — the run may still reach a provider of
  #     its own on a FUGARO_PROVIDER_KEY_ the job mounts, so that accessor
  #     stays with the job (R6, D1634). config.ServiceServedAuths is the list
  #     that decides which auths get it, and a name in it is a name the
  #     service's own cascade and pool serve a model call under. Its one
  #     exception is a mixed stage account's own job, whose Claude OAuth mount
  #     is the STAGE's credential and not the run's (is_run_model_secret,
  #     is_stage_account_token): that mount, and only it, stays.
  #   * `none` — a repository the switch holds off: every mount stays, and with
  #     it every accessor. That is an `api-key` or `oauth` repository on a
  #     project whose mode is off or whose root was applied while its record
  #     said so, a `vertex` repository, and a root whose tfvars was written
  #     before B2.7's flip — the state this arm leaves untouched.
  #
  # A repository the switch does not admit is `none` by the same input, and so
  # the model clauses of the drop below never fire for it — its job keeps the
  # mounts an earlier release gave it (D147's "with it off nothing changes").
  is_served_model_secret = {
    for env, logical in var.workflow.secret_env :
    env => local.is_run_model_secret[env] || (var.model_secrets_served == "all" && startswith(env, local.model_secret_env_prefix))
  }

  # Whether the service above serves THIS repository's credentials, and so
  # whether every clause below that takes something away from the job fires at
  # all (infra.CredentialsServiceServes, written into the repository root's
  # tfvars as credentials_service_serves). It is read APART from the variable
  # being set, because the two say different things and come apart for exactly
  # one repository: a `vertex` one is handed the address — an invoker grant, and
  # the launcher's FUGARO_CREDENTIALS_SERVICE_URL — while the service serves it
  # no credential of its own, so nothing of its job's goes (D3326 as amended by
  # F17). A tfvars written before this input existed carried
  # credentials_service only for a repository the switch served and planned
  # every drop with it, which is why the default is true.
  service_serves = var.credentials_service != null && var.credentials_service_serves

  # Whether this repository is handed the service's ADDRESS at all — the
  # variable being set, whatever it serves (IS6). It is the gate of the
  # FETCHED class alone, and it is wider than local.service_serves for exactly
  # the one repository IS9 already separated: a `vertex` repository is handed
  # the address and served no credential of its own, so nothing of its job's
  # goes for the service to hand back (D3326/F17) — and the workflow secrets
  # the flip took off its job DO go, because the run fetches those from the
  # service over that same address, whatever the repository's model path is.
  # The flip is the one class whose drop is the address's and not the service's.
  service_addressed = var.credentials_service != null

  # One entry per mount, so the drop below is per mount and never per name.
  is_service_held = {
    for env, logical in var.workflow.secret_env :
    env => local.is_served_model_secret[env] || contains(local.service_held_secret_names, logical) || local.is_proxied_git[env]
  }

  # The secrets the job's own account may read, by logical name: every secret
  # the job mounts while the credentials service is off — D147's "with it off
  # nothing changes", and the same set (and the same instance keys) as before
  # this file knew about the service — and, once it is on, every mounted
  # secret that is not one the service holds (C1.11's bundle holds the model
  # credentials, C2.1 mints installation tokens from the App key, and
  # C1.20/C2.2 drop the job's mount of each). One member per mounted secret,
  # never the project's, never another workflow's.
  #
  # The fetched class is the third reason a name leaves this set (B3.8), read
  # off local.vault_secret_names rather than off the mounts: a name the service
  # fetches is not one this job mounts at all — Go's dropVaultMounts takes it
  # off the spec the tfvars were built from — so the clause is the rule written
  # down where the accessor is decided, exactly as job_mounts' App-key clause
  # is, and a tfvars that still names one of them cannot be applied into a job
  # that reads it.
  #
  # The two clauses carry different gates, which is the one thing IS6 changes
  # here. The service-held one is gated on local.service_serves and not on the
  # block alone (IS9): a `vertex` repository is handed the address and served
  # nothing, so its job keeps every accessor (D3326, F17). The fetched one is
  # gated on the address (local.service_addressed), because that drop is the
  # run's fetch and not the service's bundle: the same `vertex` job gives up
  # its workflow secrets' accessors and mounts, and reads their values from the
  # service its own account may invoke. Each clause can only turn a drop off,
  # and each does so exactly where the half it belongs to is not granted.
  job_secret_names = toset([
    for env, logical in var.workflow.secret_env :
    logical if !(local.service_serves && local.is_service_held[env]) && !(local.service_addressed && contains(local.vault_secret_names, logical))
  ])

  # The secrets the job's CONTAINER still declares, by the variable each is
  # mounted as: every mount while the credentials service is off — byte for
  # byte the map the module declared before it knew about the service (D147) —
  # and, once it is on, every mount except the GitHub App's private key. A
  # mount the job's account has no accessor on is not a weaker mount, it is a
  # job Cloud Run refuses to create, so the mount has to go with the accessor
  # (C2.3, review finding F1). Go's internal/infra drops the same mount from
  # the spec this module's tfvars are generated from, which is the drop that
  # fires in practice; this local is the rule written down where the accessor
  # is decided, so a tfvars that still names the key cannot be applied into a
  # job with no way to read it.
  #
  # Only the App key goes here, and not the model credentials the accessor
  # drop above covers as well, because the two are removed by different tasks
  # at different times: C1.18 drops the job's accessor on the model
  # credentials and C1.20 the mount, together with the runner's switch to
  # reading them from the service (C1.15) — a mount this module removed for a
  # runner that cannot read the service yet would break every run. The App
  # key needs no such pairing: no run reads it at all once C2.1/C2.2 make the
  # service mint the installation tokens, so C2.3 drops accessor and mount
  # together. service_held_secret_names is the App key alone, which is
  # exactly the difference; is_service_held would take the model mounts too.
  #
  # Like service_held_secret_names, this is inert while the list is empty
  # (review finding F1): with the opt-in on and the list filled, the App key's
  # mount goes in the same plan as its accessor.
  #
  # Both lists are read here, and the proxied one only for the repository the
  # node admitted: a proxied repository's git credential is mounted no more
  # than the App key is, which is the half of the drop the runner's own read
  # cannot replace — a mount the account cannot resolve is a job Cloud Run
  # refuses to create (B1.8, G9).
  #
  # The check job is not this module: it runs as the build account, which
  # keeps its accessor on the App key and clones with it (D148, F3). And the
  # last clause is the fetched class (B3.8): a mount the service fetches is not
  # this job's, whatever the tfvars' secret_env still says (Go has already
  # dropped it from the spec, so the clause bites only on a hand-written
  # tfvars), because the runner reads that value from the service at bootstrap
  # (B3.4) rather than from its own environment.
  #
  # The gates are job_secret_names' — local.service_serves for the two halves
  # the service holds, local.service_addressed for the fetched class — and for
  # that local's reason: a mount the job's account has no accessor on is a job
  # Cloud Run refuses to create, so each mount goes exactly where its own
  # accessor went (IS6, IS9, D3326).
  job_mounts = {
    for env, logical in var.workflow.secret_env :
    env => logical if !(local.service_serves && (contains(local.service_held_secret_names, logical) || local.is_proxied_git[env])) && !(local.service_addressed && contains(local.vault_secret_names, logical))
  }

  # The secrets the CREDENTIALS SERVICE's own account may read, by logical name:
  # one per container the job mounts, which is the set job_mounts above decides
  # (D2074) and nothing else. The service is what a run asks for a
  # workflow secret at fetch time (B3.4), so it has to be able to read the
  # container it is asked for — and the job's own accessor is neither moved nor
  # widened by it: two accounts, two grants, one container.
  #
  # EVERY mount, and not only the secrets the repository declares under its own
  # `secrets:` — this job's git credential and, while their mounts last, its
  # model credentials are in the set too. That breadth is D2074's own wording
  # ("one for each secret the job mounts, beside the job's own; a guard test
  # holds the two sets equal"), and the set is derived rather than filtered for
  # that reason: a narrower one would be a second, hand-kept list beside the
  # mounts, which is the drift the guard exists to refuse. What narrows it is
  # the FLIP: the plan that takes a mount away (C2.2's App key, C1.20's model
  # mounts, B1.13's proxied token) empties that entry here in the same apply.
  #
  # B3.8's fetched class used to be the one flip that did not narrow it — the
  # container stayed in the set after its mount went, on the reasoning that a
  # workflow secret's value is read from THIS job's container when the run
  # fetches it (D2074, B3.4). IS6 takes it out: the container a run's fetch
  # reads is no longer the job's mount at all, it is the installation's own
  # copy of that name in the fugaro_p_ grammar, which the service already holds
  # a prefix-wide grant on (D3323, modules/installation/credentials.tf). A
  # per-container grant on the retired copy is one no run asks for, and the
  # copy is what IS13 is taking out of the state and deleting; local
  # vault_secret_names is therefore NOT added back below, and the set is the
  # mounts alone again.
  #
  # toset, not the list of mounts: two variables may mount the same logical
  # name, and a repeated name in a for_each is an error rather than a second
  # binding.
  #
  # Empty while the installation has no service (D147), empty while the service
  # serves THIS repository no credential of its own (local.service_serves — a
  # `vertex` repository is handed the address and no drop, D3326/F17), and
  # empty while the service has no account to grant it to — an installation's
  # outputs carry one only from the release that added it, and a repository
  # root planned against an older installation must plan exactly what it
  # planned (the same inertness CatalogCheckServiceAccount's empty value buys).
  # An empty for_each creates no resource at all, so nothing reads
  # var.credentials_service.account there.
  #
  # try, and not `var.credentials_service == null || var.credentials_service.account == ""`:
  # Terraform's `||` does not short-circuit before 1.12, and these modules
  # require 1.9 (versions.tf), so the `||` form reads an attribute of a null
  # value and fails the plan of every repository root whose switch is off. try
  # answers "" for a null object and for one that carries no account at all —
  # the second is how deploy/terraform's proxied_git_test evaluates this file
  # with a stub of only name and region.
  service_secret_names = local.service_serves && try(var.credentials_service.account, "") != "" ? toset(values(local.job_mounts)) : toset([])
}

# Each secret the job mounts and may read, at the secret level, never the
# project's.
resource "google_secret_manager_secret_iam_member" "job" {
  for_each = local.job_secret_names

  project   = var.project
  secret_id = var.secrets[each.value]
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.job.member
}

# Each secret the job mounts, for the credentials service's own account
# (D2074). It is the value a run will be handed through the service, so the
# service reads the container the JOB's mount names — the whole reason the two
# sets are held equal by TestServiceWorkflowSecretGrantsMatchTheJobsMounts:
# a grant here on a container no job mounts is a credential the service may
# read and no run asked for, and a mount with no grant here is a fetch that
# fails for a run the release meant to serve.
#
# The reach of each grant is the container the job's own env names, and that is
# every mount of that job: the git credential, and its model credentials until
# C1.20 drops their mounts, are in the set with the repository's own secrets.
# D2074 rules the set to be the mounts — "one for each secret the job mounts,
# beside the job's own; a guard test holds the two sets equal" — so this is the
# decision and not an accident, and no run uses the reach while the mount is
# still on (a mounted value always wins, B3.8). A narrower set would be a second
# list beside the mounts, and nothing in a plan could keep the two in step.
#
# It is not a widening of the job's own accessor above into the service's, and
# not a grant the service needs to broker anything else: the bundle's own
# container is modules/installation/credentials.tf's one grant, and this
# principal holds no project-level accessor (a project-level one cannot tell a
# workflow secret's container from the git token's or the model secrets' — the
# names are the same shape, dynamic-delivery F6 — which is D2074's rejected
# alternative (b)).
resource "google_secret_manager_secret_iam_member" "service" {
  for_each = local.service_secret_names

  project   = var.project
  secret_id = var.secrets[each.value]
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${var.credentials_service.account}"
}

# The credentials service, once the installation has one and this repository is
# handed its address (D135): the job's account may invoke it, and nothing else
# may — one member, the job's own account, roles/run.invoker, on that one
# service. It is the runner's only way to reach a model credential, and an
# unreachable service halts the run rather than falling back (D143). It is
# granted on the ADDRESS and not on the drop (local.service_serves): a `vertex`
# repository's runs fetch their workflow secrets here (D3326) and read no
# credential of the service's, so its account is granted the call and nothing
# of its job's is taken away (F17).
resource "google_cloud_run_v2_service_iam_member" "credentials" {
  count = var.credentials_service == null ? 0 : 1

  project  = var.project
  location = var.credentials_service.region
  name     = var.credentials_service.name
  role     = "roles/run.invoker"
  member   = google_service_account.job.member
}

# The release-1 Vertex grant, and the one grant of this file CS11 plans from
# more than the repository's own file: var.workflow.vertex is the repository
# REACHING Vertex (its `agent.auth: vertex`, or a chain of the project's routing
# table that may serve it naming Vertex) AND the project's budget mode allowing
# a job the permission (infra.VertexGrantForJob) — so a job's account is planned
# no aiplatform permission at all on an enforcing project, where a `vertex` run
# is refused at bootstrap and no run in flight can be holding it, and keeps it
# on an `observe` project until release 2 (X8). The chain-serving model call is
# the credentials service's from now on (D3174) and its reach is the one
# permission of its own custom role (modules/installation/credentials.tf);
# roles/aiplatform.user is what a job holds while a chain's model still goes out
# as the job, which is the path X8 retires.
resource "google_project_iam_member" "vertex" {
  count = var.workflow.vertex ? 1 : 0

  project = var.project
  role    = "roles/aiplatform.user"
  member  = google_service_account.job.member
}
