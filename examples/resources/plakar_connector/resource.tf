# The Vault secret provider the references below read from (see
# plakar_secret_provider), declared here so the example stands alone.
variable "vault_token" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_secret_provider" "vault" {
  name        = "Vault"
  integration = "vault"
  fields = {
    url = "https://vault.example.com:8200"
  }
  fields_wo = {
    token = var.vault_token
  }
  fields_wo_version = 1
}

# The access key id is not a secret; its secret half goes write-only below.
variable "media_access_key" {
  type = string
}

resource "plakar_connector" "web" {
  name        = "Web tier"
  type        = "source"
  integration = "sftp"
  resource    = "urn:res-grateful-cascade"
  environment = "production"
  fields = {
    username = "tunnel"
    root     = "/home/tunnel/data"
    port     = "2222"
  }
}

resource "plakar_connector" "bucket" {
  name        = "Media bucket"
  type        = "source"
  integration = "s3"
  resource    = "urn:res-quiet-harbor"
  fields = {
    root = "/"
  }
  secret_fields = {
    access_key        = { provider = plakar_secret_provider.vault.id, path = "secret/sources/media#access_key" }
    secret_access_key = { provider = plakar_secret_provider.vault.id, path = "secret/sources/media#secret_access_key" }
  }
}

variable "media_secret_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_connector" "bucket_write_only" {
  name        = "Media bucket (write-only)"
  type        = "source"
  integration = "s3"
  resource    = "urn:res-quiet-harbor"
  fields = {
    root       = "/"
    access_key = var.media_access_key
  }
  fields_wo = {
    secret_access_key = var.media_secret_key
  }
  fields_wo_version = 1
}
