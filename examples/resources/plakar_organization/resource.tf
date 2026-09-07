# A tenant under the provider's organization.
resource "plakar_organization" "lyon" {
  name = "Lyon"
}

# A perimeter nested under it.
resource "plakar_organization" "lyon_production" {
  name      = "Lyon production"
  parent_id = plakar_organization.lyon.id
}
