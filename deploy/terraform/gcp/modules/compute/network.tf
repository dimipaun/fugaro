# Egress for VMs that have no external IP (design §1.7, C7). Both fields of
# the job's network interface are named explicitly — measured 2026-10-15,
# Batch REFUSES noExternalIpAddress unless BOTH the network and the subnetwork
# are (§7.2) — and the NAT below is what makes "no external IP" survivable at
# all: without it the VM can reach neither Google's APIs nor GitHub nor a
# model provider, and every run would fail for a setup reason.
#
# Private Google Access is not a substitute: it reaches only Google's own
# APIs, and a Fugaro run's egress is mostly elsewhere (docs checked
# 2026-10-15, §1.7). So NAT is what this module provisions.
#
# The router and the NAT are created on the network and subnetwork the caller
# names, and destroyed with this root — never adopted from, and never a reason
# to touch, a NAT someone else made: a project's own NAT is not this one. They
# carry no labels, and cannot: the provider's google_compute_router takes no
# labels argument (terraform validate, 2026-10-16) and google_compute_router_nat
# takes none either. Their names are the mark — `fugaro-router` and `fugaro-nat`,
# from the same singleton set as the accounts and roles — and a project that
# already has one of those names fails the apply rather than being adopted.
# No ingress to a run's VM (design §3.2, §3.7). A run VM is a Docker HOST --
# its container talks to the host's daemon over the socket, and the host has
# the run's secrets in its environment -- and the installation's network is
# not a boundary: a default network arrives with `default-allow-internal`
# (priority 65534, 10.128.0.0/9, all protocols), so without this rule any VM
# in the project could open every port on a run's VM. This rule matches
# first, and it is what makes "the VMs are not a boundary" a statement about
# egress and about the Docker host, not about who can connect to one.
#
# Targeted by SERVICE ACCOUNT rather than by a network tag: the tag would have
# to be written by the launcher when it creates the job (Task 7), and a rule
# that silently matches nothing is worse than no rule. Every Batch job this
# backend creates runs as one of var.run_accounts (batch/run.go's
# AllocationPolicy.ServiceAccount), so the target set is exactly the runs'
# VMs, and the preload builder VM -- which boots as one of them too -- is
# covered as well, which is right: it needs no inbound traffic either (its
# startup script's output is read over the Compute API, not the network).
#
# What is denied is every inbound connection, from anywhere, at priority 1000
# (lower number wins, so it beats default-allow-internal and
# default-allow-ssh alike). UNVERIFIED live: no Batch document this run could
# read states whether a job's VM needs any inbound path for the Batch agent's
# task reporting -- everything the agent does is outbound by design, and
# 57b-1 (a real run that reaches a terminal state) is where that is
# confirmed; if it does need one, this rule narrows to the network's internal
# ranges (10.128.0.0/9) rather than being dropped, since that is the range
# default-allow-internal opens and the one the review is about.
#
# What this does NOT isolate, precisely: (a) egress is untouched, so a run
# still reaches the internet through the NAT and can still open connections
# TO other VMs of the project -- this rule stops them reaching it, not it
# reaching them; (b) the metadata server is link-local and is not subject to
# VPC firewall rules, so a run VM still reads its own metadata (which is how
# it gets its service account's token) -- that is by design, not a gap; (c)
# instances on the network that do not run as one of var.run_accounts are
# not covered, the project's other VMs being the project's own business; (d)
# the run VM's own Docker host remains the trust boundary inside the VM
# (design §2.x): anything that reaches the host's daemon controls the host.
resource "google_compute_firewall" "run_deny_ingress" {
  project   = var.project
  name      = "fugaro-run-deny-ingress"
  network   = var.network
  direction = "INGRESS"
  priority  = 1000

  deny {
    protocol = "all"
  }

  source_ranges           = ["0.0.0.0/0"]
  target_service_accounts = [for a in var.run_accounts : trimprefix(a, "serviceAccount:")]

  depends_on = [google_project_service.this]
}

resource "google_compute_router" "this" {
  project = var.project
  name    = "fugaro-router"
  region  = var.region
  network = var.network

  depends_on = [google_project_service.this]
}

resource "google_compute_router_nat" "this" {
  project = var.project
  name    = "fugaro-nat"
  router  = google_compute_router.this.name
  region  = var.region

  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "LIST_OF_SUBNETWORKS"

  # Only the subnetwork the job's VMs are placed in: an installation's NAT
  # exists for its runs, and a sibling subnetwork's traffic is not its to
  # translate.
  subnetwork {
    name                    = var.subnetwork
    source_ip_ranges_to_nat = ["ALL_IP_RANGES"]
  }

  log_config {
    enable = true
    filter = "ERRORS_ONLY"
  }
}
