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
