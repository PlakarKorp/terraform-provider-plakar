# The organization the members below belong to, looked up by name so the
# example stands alone.
data "plakar_organization" "lyon" {
  name = "Lyon"
}

# A person, by email.
data "plakar_member" "alice" {
  organization_id = data.plakar_organization.lyon.id
  email           = "alice@example.com"
}

# A service account, by name.
data "plakar_member" "nightly" {
  organization_id = data.plakar_organization.lyon.id
  name            = "nightly-automation"
}
