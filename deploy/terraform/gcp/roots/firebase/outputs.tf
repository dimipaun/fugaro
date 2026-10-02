# fugaro init --firebase reads these with terraform output -json and passes
# them to the installation root, the config and the jobs.

output "rtdb_url" {
  value = module.firebase.rtdb_url
}

output "firebase_api_key" {
  value = module.firebase.firebase_api_key
}

output "token_signer" {
  value = module.firebase.token_signer
}

output "firebase_project" {
  value = module.firebase.firebase_project
}
