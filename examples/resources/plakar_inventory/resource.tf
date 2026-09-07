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
