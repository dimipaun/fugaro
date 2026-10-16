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
