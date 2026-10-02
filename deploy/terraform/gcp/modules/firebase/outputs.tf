output "rtdb_url" {
  description = "The default Realtime Database's URL. fugaro init copies it to the config and the history job."
  value       = google_firebase_database_instance.this.database_url
}

output "firebase_api_key" {
  description = "The restricted web API key. It isn't a secret: it names no one and may call only Identity Toolkit and Secure Token."
  value       = nonsensitive(google_apikeys_key.web.key_string)
}

output "token_signer" {
  description = "The signer account's email, which the launcher's signJwt call names."
  value       = google_service_account.signer.email
}

output "firebase_project" {
  description = "The Firebase project's ID."
  value       = var.project
}
