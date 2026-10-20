# fugaro init reads these with terraform output -json: the preloaded boot
# image a Batch job's definition names, and the accounts and network the
# stage reports (design §8.1 task 6).

# The image FAMILY a job's VM boots, as projects/<project>/global/images/
# family/<preload_image>: Task 7 names it in the job's allocation policy, and
# the weekly rebuild adds generations to it without that reference moving.
output "preload_image" {
  value = module.compute.preload_image
}

# The label each generation in that family records its socket gid under, the
# module's output passed through unchanged (the root is a thin thing: see
# TestComputeOutputsCoverTheRoot). It travels beside the family because that is
# what reading the gid takes: the family names the image and this names the
# label on it.
output "preload_socket_gid_label" {
  value = module.compute.preload_socket_gid_label
}

output "reaper_account" {
  value = module.compute.reaper_account
}

output "reaper_role" {
  value = module.compute.reaper_role
}

output "preload_account" {
  value = module.compute.preload_account
}

output "preload_scheduler_job" {
  value = module.compute.preload_scheduler_job
}

output "router" {
  value = module.compute.router
}

output "nat" {
  value = module.compute.nat
}

output "network" {
  value = module.compute.network
}

output "subnetwork" {
  value = module.compute.subnetwork
}
