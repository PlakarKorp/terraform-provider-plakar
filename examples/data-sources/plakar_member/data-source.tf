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
