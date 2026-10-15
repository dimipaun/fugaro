# What fugaro init reads back: the names a Batch job's definition needs (the
# preloaded boot image) and the ones an operator or the doctor check looks
# for (the accounts, the router and NAT).

output "reaper_account" {
  value = google_service_account.reaper.email
}

output "reaper_role" {
  value = google_project_iam_custom_role.reaper.name
}

output "preload_image" {
  value = local.preload_image
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
