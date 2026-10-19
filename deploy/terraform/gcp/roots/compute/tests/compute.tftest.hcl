# Plans the compute root against a mock provider: no credentials, no network
# beyond the provider download. The names are the ones fugaro init writes into
# terraform.tfvars.json (internal/infra/compute.go), and the base pin is the
# shape 57a measured on 2026-10-15 (a pinned Artifact Registry path through the
# installation's remote repository).
#
# An assertion can't reach into a child module's resources, so the first run
# plans the root (its wiring, through the outputs) and the others plan the
# module itself.

# A computed attribute is unknown during a plan unless a run overrides it, and
# the mock's own invented value is a random string -- which is not an IAM
# member or an account name, so the provider's validation fails the plan for
# reasons that have nothing to do with this module. The runs below therefore
# override the accounts and roles they read, with the values the module would
# have produced (the same shape modules/installation's tests use).
mock_provider "google" {}

variables {
  project        = "proj-1234"
  project_number = "123456789012"
  region         = "us-east5"
  network        = "projects/proj-1234/global/networks/default"
  subnetwork     = "projects/proj-1234/regions/us-east5/subnetworks/default"
  names = {
    job_prefix            = "fugaro-"
    reaper_account_id     = "fugaro-reaper"
    reaper_role_id        = "fugaroReaper"
    preload_account_id    = "fugaro-preload"
    preload_role_id       = "fugaroPreloadBuilder"
    preload_image         = "fugaro-preload"
    preload_scheduler_job = "fugaro-preload-rebuild"
  }
  launchers            = ["user:launcher@example.com"]
  operators            = ["user:operator@example.com"]
  run_accounts         = ["serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"]
  registry_repository  = "fugaro-ghcr"
  base_image           = "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/acme/fugaro-base:1.2.3"
  base_project         = "batch-custom-image"
  base_family          = "batch-cos-stable-official"
  preload_zone         = "us-east5-a"
  preload_schedule     = "0 3 * * 1"
  builder_machine_type = "e2-medium"
  build_submitter_role = "projects/proj-1234/roles/fugaroBuildSubmitter"
}

# The root is a thin thing: every output is the module's, so a name the stage
# reads back is a name the module created. The mocked accounts are given the
# values the module would have produced, so the outputs can be read exactly.
run "root_passes_inputs" {
  command = plan

  override_resource {
    target          = module.compute.google_service_account.reaper
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-reaper@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-reaper@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-reaper@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = module.compute.google_service_account.preload
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-preload@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-preload@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-preload@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = module.compute.google_compute_router.this
    override_during = plan
    values = {
      name = "fugaro-router"
    }
  }
  override_resource {
    target          = module.compute.google_compute_router_nat.this
    override_during = plan
    values = {
      name = "fugaro-nat"
    }
  }
  override_resource {
    target          = module.compute.google_cloud_scheduler_job.preload_rebuild
    override_during = plan
    values = {
      name = "fugaro-preload-rebuild"
    }
  }

  assert {
    condition     = output.preload_image == "fugaro-preload-a0b6aca0"
    error_message = "the root must output the preloaded image's family name, which carries a hash of the pinned base image"
  }
  assert {
    condition     = output.reaper_account == "fugaro-reaper@proj-1234.iam.gserviceaccount.com"
    error_message = "the root must pass project and names to the module: the reaper is in the installation's project"
  }
  assert {
    condition     = output.preload_account == "fugaro-preload@proj-1234.iam.gserviceaccount.com"
    error_message = "the root must output the preload account the rebuild runs as"
  }
  assert {
    condition     = output.preload_scheduler_job == "fugaro-preload-rebuild"
    error_message = "the root must output the rebuild's Scheduler job, which the stage starts once"
  }
  assert {
    condition     = output.router == "fugaro-router" && output.nat == "fugaro-nat"
    error_message = "the root must output the router and the NAT the no-external-IP VMs egress through"
  }
  assert {
    condition     = output.network == "projects/proj-1234/global/networks/default" && output.subnetwork == "projects/proj-1234/regions/us-east5/subnetworks/default"
    error_message = "the root must output the network and subnetwork the jobs are placed on"
  }
}

# The reaper's role is the FIVE permissions the sweeper's code uses, and the
# reaper holds it and nothing else (design §3.7; the sixth was batch.jobs.cancel
# until Task 9, which is not a permission Google defines and made the role
# uncreatable).
run "TestReaperIsExactlyFive" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  override_resource {
    target          = google_service_account.reaper
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-reaper@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-reaper@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.reaper
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroReaper"
    }
  }

  assert {
    condition = google_project_iam_custom_role.reaper.permissions == toset([
      "batch.jobs.list",
      "batch.jobs.get",
      "batch.jobs.delete",
      "compute.instances.list",
      "compute.instances.delete",
    ])
    error_message = "fugaroReaper must hold exactly the sweeper's five permissions, never batch.jobsEditor or compute.instanceAdmin.v1, and never batch.jobs.cancel, which Google does not define"
  }
  assert {
    condition     = google_project_iam_custom_role.reaper.role_id == "fugaroReaper" && google_project_iam_custom_role.reaper.title == "Fugaro reaper"
    error_message = "the reaper role's ID is the input"
  }
  assert {
    condition     = google_project_iam_member.reaper.role == "projects/proj-1234/roles/fugaroReaper" && google_project_iam_member.reaper.project == "proj-1234"
    error_message = "the reaper is granted its own custom role on the project, never a predefined one"
  }
  assert {
    condition     = google_project_iam_member.reaper.member == "serviceAccount:fugaro-reaper@proj-1234.iam.gserviceaccount.com"
    error_message = "the reaper's role is held by the reaper account, and by no other member"
  }
  assert {
    condition     = google_service_account.reaper.account_id == "fugaro-reaper" && google_service_account.reaper.display_name == "Fugaro reaper"
    error_message = "the reaper is fugaro-reaper, its display name being the ownership mark"
  }
}

# The launcher's Batch Job Editor is narrowed by 57a-9's Condition, and actAs
# is on the run accounts only -- the cross product of the launchers and those
# accounts, never a role on the project (design §3.7).
run "TestLauncherIsConditionedAndActsAsTheRunAccounts" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  assert {
    condition = alltrue([
      for m in google_project_iam_member.launcher_jobs_editor :
      m.role == "roles/batch.jobsEditor" && m.member == "user:launcher@example.com" &&
      length(m.condition) == 1 &&
      m.condition[0].expression == "resource.name.startsWith(\"projects/proj-1234/locations/us-east5/jobs/fugaro-\")"
    ])
    error_message = "the launcher's Batch Job Editor must be conditioned on the Fugaro job-name prefix, in this project and region"
  }
  assert {
    condition = keys(google_service_account_iam_member.launcher_user) == [
      "user:launcher@example.com|serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"
    ]
    error_message = "actAs is the cross product of the launchers and the run accounts"
  }
  assert {
    condition = alltrue([
      for m in google_service_account_iam_member.launcher_user :
      m.role == "roles/iam.serviceAccountUser" &&
      m.service_account_id == "projects/proj-1234/serviceAccounts/fugaro-acme-web@proj-1234.iam.gserviceaccount.com"
    ])
    error_message = "the launcher holds actAs on the run account itself, never on the project"
  }
  assert {
    condition     = !contains(keys(google_service_account_iam_member.launcher_user), "user:operator@example.com")
    error_message = "an operator is not a launcher: the two sets are separate inputs"
  }
}

# The run-class accounts hold the two roles Batch requires of a job's service
# account, plus the reader grant on the ONE repository the pin names -- the
# remote fugaro-ghcr on 2026-10-15, never the fugaro-base constant (§7.2).
run "TestRunAccountsHoldTheRunRoles" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  assert {
    condition = keys(google_project_iam_member.run_agent_reporter) == [
      "serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"
    ]
    error_message = "the run accounts get agentReporter, without which a job never reports task state"
  }
  assert {
    condition     = google_project_iam_member.run_agent_reporter["serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"].role == "roles/batch.agentReporter"
    error_message = "the run account's reporting role is roles/batch.agentReporter on the project"
  }
  assert {
    condition     = google_project_iam_member.run_log_writer["serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"].role == "roles/logging.logWriter"
    error_message = "the run accounts write their logs"
  }
  assert {
    condition     = google_artifact_registry_repository_iam_member.run_registry_reader["serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"].repository == "fugaro-ghcr"
    error_message = "the reader grant goes on the repository the pin names, never on a constant"
  }
}

# The preload build runs as the preload account, not as Cloud Build's own
# agent, so the preload account is who holds actAs on the run accounts the
# builder VM names (design §3.7).
run "TestThePreloadBuildActsAsTheRunAccountsOnly" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  override_resource {
    target          = google_service_account.preload
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-preload@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-preload@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition = keys(google_service_account_iam_member.preload_run_account_user) == [
      "serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"
    ]
    error_message = "the preload account holds actAs on each run account, and on no other"
  }
  assert {
    condition = alltrue([
      for m in google_service_account_iam_member.preload_run_account_user :
      m.member == "serviceAccount:fugaro-preload@proj-1234.iam.gserviceaccount.com" && m.role == "roles/iam.serviceAccountUser"
    ])
    error_message = "actAs on a run account is held by the preload account, never by the Cloud Build agent"
  }
  assert {
    condition     = google_service_account_iam_member.preload_cloud_build_agent.member == "serviceAccount:service-123456789012@gcp-sa-cloudbuild.iam.gserviceaccount.com"
    error_message = "Cloud Build's own agent holds actAs on the PRELOAD account, which is what running the build as it needs. The principal is the SERVICE AGENT, service-<number>@gcp-sa-cloudbuild.iam.gserviceaccount.com, not service-<number>@cloudbuild.gserviceaccount.com, which is no principal at all, and not the bare <number>@cloudbuild.gserviceaccount.com, which is the legacy default BUILD account."
  }
  # The build's submitter and its identity are the same account, and Google's
  # note on that case is that the account "must still have the
  # iam.serviceAccounts.actAs permission" -- which a service account does not
  # hold on itself by default (cloud.google.com/build/docs/cloud-build-service-account,
  # checked 2026-10-16).
  assert {
    condition     = google_service_account_iam_member.preload_self_user.member == "serviceAccount:fugaro-preload@proj-1234.iam.gserviceaccount.com" && google_service_account_iam_member.preload_self_user.role == "roles/iam.serviceAccountUser"
    error_message = "the preload account holds actAs on itself: the scheduled call and the build it creates are the same account, and without it both fail with PERMISSION_DENIED before any step runs"
  }
  assert {
    condition     = google_service_account_iam_member.preload_scheduler_agent.member == "serviceAccount:service-123456789012@gcp-sa-cloudscheduler.iam.gserviceaccount.com"
    error_message = "Cloud Scheduler's agent holds actAs on the preload account, which is what minting the job's OAuth token needs"
  }
  assert {
    condition = keys(google_service_account_iam_member.preload_operator) == [
      "user:operator@example.com"
    ]
    error_message = "the installation's operators hold actAs on the preload account: applying this root creates the Scheduler job whose token is minted from it"
  }
}

# No external IP is survivable only because the NAT is there, on the network
# and subnetwork the job's VMs are placed on -- both named, because Batch
# refuses noExternalIpAddress without both (§1.7, §7.2).
run "TestTheNATIsOnTheJobsNetwork" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  override_resource {
    target          = google_compute_router.this
    override_during = plan
    values = {
      name = "fugaro-router"
    }
  }

  assert {
    condition     = google_compute_router.this.network == "projects/proj-1234/global/networks/default" && google_compute_router.this.name == "fugaro-router"
    error_message = "the router is on the network the caller named"
  }
  assert {
    condition     = google_compute_router_nat.this.nat_ip_allocate_option == "AUTO_ONLY" && google_compute_router_nat.this.source_subnetwork_ip_ranges_to_nat == "LIST_OF_SUBNETWORKS"
    error_message = "the NAT allocates its own addresses and translates one subnetwork"
  }
  assert {
    condition = [
      for s in google_compute_router_nat.this.subnetwork : s.name
    ] == ["projects/proj-1234/regions/us-east5/subnetworks/default"]
    error_message = "the NAT translates the subnetwork the job's VMs are on, and no other"
  }
  assert {
    condition     = google_compute_router_nat.this.router == google_compute_router.this.name && google_compute_router_nat.this.region == "us-east5"
    error_message = "the NAT hangs off this router, in the installation's region"
  }
}

# A run's VM is a Docker HOST, and a default network arrives with
# default-allow-internal (priority 65534, 10.128.0.0/9, all protocols): without
# this rule any VM in the project could open every port on a run's VM, which
# §3.2's control does not assume (§3.2, §3.7).
run "TestTheRunVMsTakeNoIngress" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  assert {
    condition     = google_compute_firewall.run_deny_ingress.direction == "INGRESS" && google_compute_firewall.run_deny_ingress.priority == 1000
    error_message = "the run VMs' rule is an INGRESS rule at priority 1000, which is a lower number than default-allow-internal's 65534 and therefore wins"
  }
  assert {
    condition     = [for d in google_compute_firewall.run_deny_ingress.deny : d.protocol] == ["all"]
    error_message = "every inbound protocol is denied, not just ssh: what the rule closes is every port on a run's VM"
  }
  assert {
    condition     = google_compute_firewall.run_deny_ingress.network == "projects/proj-1234/global/networks/default"
    error_message = "the rule is on the network the job's VMs are placed on"
  }
  assert {
    condition = google_compute_firewall.run_deny_ingress.target_service_accounts == toset([
      "fugaro-acme-web@proj-1234.iam.gserviceaccount.com"
    ])
    error_message = "the rule is targeted by the run accounts themselves: a network tag would have to be written by the launcher (Task 7), and a rule that silently matches nothing is worse than no rule"
  }
  assert {
    condition     = google_compute_firewall.run_deny_ingress.source_ranges == toset(["0.0.0.0/0"])
    error_message = "the rule denies ingress from EVERY source. A narrower source range is not tuning: it stops the deny overriding the project's other allow rules (a default-allow-ssh or a project-level allow a launcher added), which is the whole of what makes a run's VM unreachable, so narrowing it to 10.128.0.0/9 is the §3.2 decision the module's comment reserves for 57b-1 -- and it is made by editing this assertion, deliberately"
  }
}

# The two APIs, and NOT containerfilesystem: it is the prerequisite of image
# STREAMING, and the design takes the preloaded boot image instead (§1.2).
run "TestTheAPIsAreTheTwoAndNoMore" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  assert {
    condition     = keys(google_project_service.this) == ["batch.googleapis.com", "compute.googleapis.com"]
    error_message = "the module enables Compute and Batch, and nothing else"
  }
  assert {
    condition     = !contains(keys(google_project_service.this), "containerfilesystem.googleapis.com")
    error_message = "containerfilesystem is image streaming's prerequisite, and the design does not stream"
  }
}

# The preload is a scheduled Cloud Build, not a one-off: a Batch image's
# supported lifetime can end 60 days after its starting date (§1.2, C19). The
# job calls the Cloud Build API as the preload account.
run "TestThePreloadRebuildIsScheduled" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  assert {
    condition     = google_cloud_scheduler_job.preload_rebuild.name == "fugaro-preload-rebuild" && google_cloud_scheduler_job.preload_rebuild.region == "us-east5"
    error_message = "the rebuild's job name and region are the inputs"
  }
  assert {
    condition     = google_cloud_scheduler_job.preload_rebuild.schedule == "0 3 * * 1" && google_cloud_scheduler_job.preload_rebuild.time_zone == "Etc/UTC"
    error_message = "the rebuild runs on the schedule the caller named"
  }
  assert {
    condition     = google_cloud_scheduler_job.preload_rebuild.http_target[0].http_method == "POST" && google_cloud_scheduler_job.preload_rebuild.http_target[0].uri == "https://cloudbuild.googleapis.com/v1/projects/proj-1234/builds"
    error_message = "the job POSTs a build to the Cloud Build API: there is no trigger to host"
  }
  assert {
    condition     = google_cloud_scheduler_job.preload_rebuild.http_target[0].oauth_token[0].service_account_email == "fugaro-preload@proj-1234.iam.gserviceaccount.com"
    error_message = "the build is started as the preload account, not as the default Cloud Build account"
  }
  assert {
    condition     = google_project_iam_member.preload_build_submitter.role == "projects/proj-1234/roles/fugaroBuildSubmitter"
    error_message = "creating a build needs the installation's own build-submitter role, passed by reference"
  }
  # The rebuild is not a no-op on an image that is already current, so the
  # window the script replaces a stale one inside has to reach it: the build's
  # own spec carries it, and the job retries a build that could not finish (a
  # pull that failed, an API error) rather than waiting a week for its next
  # attempt.
  assert {
    condition     = strcontains(base64decode(google_cloud_scheduler_job.preload_rebuild.http_target[0].body), "REFRESH_DAYS=30")
    error_message = "the build's spec does not carry the refresh window to preload.sh: the scheduled rebuild could never replace a stale image"
  }
  # CLOUD_LOGGING_ONLY, not CLOUD_LOGGING: with a user-specified service
  # account the logs go to a Cloud Storage or Cloud Logging bucket in the
  # project, and the latter is CLOUD_LOGGING_ONLY. CLOUD_LOGGING is not a value
  # of the enum at all, and the API rejects the build for it
  # (cloud.google.com/build/docs/securing-builds/configure-user-specified-service-accounts,
  # checked 2026-10-16).
  assert {
    condition     = strcontains(base64decode(google_cloud_scheduler_job.preload_rebuild.http_target[0].body), "\"logging\":\"CLOUD_LOGGING_ONLY\"")
    error_message = "the build's logging option must be CLOUD_LOGGING_ONLY: the build runs as a user-specified service account, and CLOUD_LOGGING is not a value of that enum"
  }
  # The whole build config is substitution-processed before any step runs, so
  # every `$` in the inlined script has to be doubled: one unescaped `$VAR`
  # that is not a built-in substitution makes the API reject the build with
  # INVALID_ARGUMENT, and the build never starts
  # (cloud.google.com/build/docs/configuring-builds/substitute-variable-values,
  # checked 2026-10-16).
  assert {
    condition     = strcontains(base64decode(google_cloud_scheduler_job.preload_rebuild.http_target[0].body), "$$${IMAGE_FAMILY:?}")
    error_message = "preload.sh reaches the build with an unescaped $: every $ in the inlined script must be $$ in the build config, or Cloud Build reads it as a substitution and rejects the build"
  }
  assert {
    condition     = !strcontains(base64decode(google_cloud_scheduler_job.preload_rebuild.http_target[0].body), "\"$PROJECT\"")
    error_message = "the build config carries a bare $PROJECT: Cloud Build would read it as its own substitution"
  }
  assert {
    condition     = google_cloud_scheduler_job.preload_rebuild.retry_config[0].retry_count == 3
    error_message = "the scheduled rebuild has no retry, so a refused replacement would wait for the next week"
  }
}

# The shapes the module refuses, refused: Batch refuses noExternalIpAddress
# without both a network and a subnetwork, and a run account set that is empty
# is a backend whose jobs could never report (§1.7, §3.2).
run "a_missing_subnetwork_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    subnetwork = ""
  }

  expect_failures = [var.subnetwork]
}

run "no_run_account_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    run_accounts = []
  }

  expect_failures = [var.run_accounts]
}

# The image the preload pulls has to be one a run could pull: the run accounts'
# reader grant is on registry_repository, in region, in project (iam.tf), so a
# pin whose host, project or repository disagrees with those is refused at plan
# time instead of failing the build that pulls it. This one is the same image
# one region over -- the shape a copy-paste between installations produces.
run "a_base_image_from_another_registry_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    base_image = "us-east1-docker.pkg.dev/proj-1234/fugaro-ghcr/acme/fugaro-base:1.2.3"
  }

  expect_failures = [var.base_image]
}

# And the repository in the path is registry_repository, not a sibling in the
# same project and region: the reader grant is on exactly one repository.
run "a_base_image_from_another_repository_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    base_image = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/acme/fugaro-base:1.2.3"
  }

  expect_failures = [var.base_image]
}

# The image part of the pin is interpolated into the builder VM's startup
# script inside single quotes (preload.sh's `docker pull '${BASE_IMAGE}'`), on
# a VM that runs as a run-class account with --scopes=cloud-platform: a pin
# carrying a quote would end that word and run whatever followed it. The
# alphabet the first validation allows is what refuses it -- this pin is in
# this installation's own registry, so only the alphabet can.
run "a_base_image_with_a_shell_metacharacter_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    base_image = "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/acme/fugaro-base:1.2.3'; echo not-an-image; echo '"
  }

  expect_failures = [var.base_image]
}

# A launcher gets Batch Job Editor on Fugaro-shaped jobs and actAs on every
# run-class account -- far wider than a launcher's grant on the Cloud Run
# backend -- so the member has to be people, or a group of them. A domain:
# member covers every account in the domain, including ones created after this
# apply, and there is no membership list to review; the module refuses it
# instead of widening both grants to it.
run "a_domain_launcher_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    launchers = ["domain:example.com"]
  }

  expect_failures = [var.launchers]
}

# operators is the other member-carrying variable, and it carries the same
# shape rule the installation module validates its own list against: each value
# becomes a project-level IAM member on the preload account (iam.tf's
# preload_operator), so a bare address with no user:/group: prefix is refused
# here rather than accepted and rejected by the IAM API at apply.
run "a_malformed_operator_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    operators = ["someone@example.com"]
  }

  expect_failures = [var.operators]
}

# Every operator gets actAs on the preload account, and the preload account can
# act as every run account and create instances: a domain: member would hand
# that to every account in the domain, including ones created after this
# apply, and allUsers / allAuthenticatedUsers to anyone at all. They are
# refused as the launchers' are.
run "a_domain_operator_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    operators = ["domain:example.com"]
  }

  expect_failures = [var.operators]
}

run "an_all_users_operator_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    operators = ["allUsers"]
  }

  expect_failures = [var.operators]
}

run "an_all_authenticated_users_operator_is_refused" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    operators = ["allAuthenticatedUsers"]
  }

  expect_failures = [var.operators]
}

# An operator may be a service account -- the installation's own operators
# can be one, such as a CI identity that applies the roots -- since it is one
# principal with an address, not a set nobody reviews.
run "a_service_account_operator_is_accepted" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    operators = ["serviceAccount:ci@proj-1234.iam.gserviceaccount.com"]
  }
}

# A group is people with a membership list someone manages, and it is what the
# installation module already grants the runs bucket's objectUser to, so it is
# accepted here: refusing it would make a supported installation's launchers
# unable to provision the backend they run on.
run "a_group_launcher_is_accepted" {
  command = plan

  module {
    source = "../../modules/compute"
  }

  variables {
    launchers = ["group:launchers@example.com"]
  }
}
