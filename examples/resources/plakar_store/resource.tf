resource "plakar_store" "offsite" {
  name        = "Offsite S3"
  integration = "s3"
  resource    = "Ample Sky"
  environment = "production"
  fields = {
    passphrase        = var.repo_passphrase
    access_key        = var.s3_access_key
    secret_access_key = var.s3_secret_key
    root              = "/backups"
  }
}

# The same store with its secrets resolved from Vault each time Plakar needs
# them: Plakar and Terraform state only hold the paths.
resource "plakar_store" "offsite_vault" {
  name        = "Offsite S3 (Vault)"
  integration = "s3"
  resource    = "Ample Sky"
  environment = "production"
  fields = {
    root = "/backups"
  }
  secret_fields = {
    passphrase        = { provider = plakar_secret_provider.vault.id, path = "secret/backups/offsite#passphrase" }
    access_key        = { provider = plakar_secret_provider.vault.id, path = "secret/backups/offsite#access_key" }
    secret_access_key = { provider = plakar_secret_provider.vault.id, path = "secret/backups/offsite#secret_access_key" }
  }
}
