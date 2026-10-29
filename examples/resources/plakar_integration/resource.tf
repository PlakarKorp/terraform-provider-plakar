# Integrations belong to the whole deployment: the API key needs the
# platform-admin role, and destroying one uninstalls it for every
# organization. Pin version for a deterministic install; left out, the
# catalog's highest version is installed once and never upgraded by a plan.
variable "integrations" {
  type = map(string)
  default = {
    s3       = "v1.1.10"
    scaleway = "v1.1.13"
    vault    = "v1.1.12"
  }
}

resource "plakar_integration" "this" {
  for_each = var.integrations
  name     = each.key
  version  = each.value
}
