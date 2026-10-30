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

variable "aws_access_key" {
  type = string
}

variable "aws_secret_access_key" {
  type      = string
  sensitive = true
}

# The fleet declared by hand: a self-managed inventory carries no
# configuration, its resources are declared with plakar_inventory_resource.
resource "plakar_inventory" "fleet" {
  name = "Fleet"
  type = "self-managed"
}

# A provider-backed inventory discovers its resources from the account it is
# configured against.
resource "plakar_inventory" "aws" {
  name = "AWS production"
  type = "aws"

  aws {
    credentials_type  = "access_key"
    access_key        = var.aws_access_key
    secret_access_key = var.aws_secret_access_key
    region            = "eu-west-1"
  }
}

# A scaleway inventory keeps its keys out of state: the access key resolved
# from Vault, the secret key write-only. Each credential takes one of three
# forms: <name>, <name>_secret or <name>_wo. Other inventory types take
# plain values only for now.
variable "scw_secret_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_inventory" "scaleway" {
  name = "Scaleway production"
  type = "scaleway"

  scaleway {
    project_id            = "11111111-2222-3333-4444-555555555555"
    access_key_secret     = { provider = plakar_secret_provider.vault.id, path = "secret/inventories/scaleway#access_key" }
    secret_key_wo         = var.scw_secret_key
    secret_key_wo_version = 1
  }
}
