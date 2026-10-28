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
