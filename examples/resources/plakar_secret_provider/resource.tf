# The token never lands in state: pass it through an ephemeral variable, and
# bump fields_wo_version whenever it is rotated.
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

# Scaleway Secret Manager: an API key limited to SecretManagerReadOnly and
# SecretManagerSecretAccess on the secrets' project. A reference to it reads
# "<secret id>#<key>", the secret's value being a JSON object.
variable "scw_secret_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_secret_provider" "scaleway" {
  name        = "Scaleway Secret Manager"
  integration = "scaleway"
  fields = {
    scw_region = "fr-par"
  }
  fields_wo = {
    scw_secret_key = var.scw_secret_key
  }
  fields_wo_version = 1
}

# The access key id is not a secret: a variable, not a placeholder.
variable "scw_access_key" {
  type = string
}

resource "plakar_inventory" "scaleway" {
  name = "Scaleway"
  type = "scaleway"
  scaleway {
    project_id = "11111111-2222-3333-4444-555555555555"
    access_key = var.scw_access_key
    secret_key_secret = {
      provider = plakar_secret_provider.scaleway.id
      path     = "66666666-7777-8888-9999-000000000000#secret_key"
    }
  }
}
