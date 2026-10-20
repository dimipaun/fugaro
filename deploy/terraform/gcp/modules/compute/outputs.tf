# What fugaro init reads back: the names a Batch job's definition needs (the
# preloaded boot image) and the ones an operator or the doctor check looks
# for (the accounts, the router and NAT).

output "reaper_account" {
  value = google_service_account.reaper.email
}

output "reaper_role" {
  value = google_project_iam_custom_role.reaper.name
}

# The preloaded boot image a job's VM boots: the FAMILY the pin derives, not
# one image, so what a job names is
# projects/<project>/global/images/family/<this> and the scheduled rebuild can
# create a new generation inside it without the job's reference changing
# (preload.tf, preload.sh). The name is the family and not a full path because
# the project is the caller's, and it is what the stage reports.
output "preload_image" {
  value = local.preload_family
}

# The label every generation in the preloaded family records its socket gid
# under (preload.sh, preload.tf). It is the name and not the value, and that is
# not a shortfall of this output: the value is a fact about the image the build
# makes out of band, so no resource here holds it and terraform's refresh
# covers none of it -- a `data` source reading the family would fail the very
# apply this module exists to complete, because the family has no image until
# the scheduled build has run once. What the stage needs to read the gid back
# is therefore this key beside `preload_image` (the family), and the read
# itself is the owner's live check (Check 57b) and `doctor`'s.
output "preload_socket_gid_label" {
  value = local.socket_gid_label
}

output "preload_account" {
  value = google_service_account.preload.email
}

output "preload_scheduler_job" {
  value = google_cloud_scheduler_job.preload_rebuild.name
}

output "router" {
  value = google_compute_router.this.name
}

output "nat" {
  value = google_compute_router_nat.this.name
}

output "network" {
  value = var.network
}

output "subnetwork" {
  value = var.subnetwork
}
