# A person: the invitation is auto-accepted; a brand-new address gets an
# account whose one-time password lands in generated_password (state!).
resource "plakar_member" "alice" {
  organization_id = plakar_organization.lyon.id
  email           = "alice@example.com"
  name            = "Alice"
}

# A service account for automation: no email, no interactive login. Mint its
# API key in the Plakar UI.
resource "plakar_member" "nightly" {
  organization_id = plakar_organization.lyon.id
  name            = "nightly-automation"
  service         = true
}
