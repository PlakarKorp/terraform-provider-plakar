resource "plakar_inventory" "fleet" {
  name = "Fleet"
  type = "self-managed"
}

resource "plakar_inventory_resource" "db1" {
  inventory_id = plakar_inventory.fleet.id
  urn          = "urn:fleet:database/db1"
  name         = "db1"
  class        = "database"
  subclass     = "postgres"
  endpoints    = ["db1.internal"]
  tags         = ["production"]
}

# Connectors attach to the resource by URN.
resource "plakar_connector" "db1_dump" {
  name        = "db1 nightly dump"
  type        = "source"
  integration = "postgres"
  resource    = plakar_inventory_resource.db1.urn

  fields = {
    connection_string = var.db1_connection_string
  }
}
