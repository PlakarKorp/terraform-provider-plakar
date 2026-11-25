# The organization and the members the grants below apply to (see
# plakar_organization and plakar_member), declared here so the example stands
# alone.
resource "plakar_organization" "lyon" {
  name = "Lyon"
}

resource "plakar_member" "alice" {
  organization_id = plakar_organization.lyon.id
  email           = "alice@example.com"
  name            = "Alice"
}

resource "plakar_member" "nightly" {
  organization_id = plakar_organization.lyon.id
  name            = "nightly-automation"
  service         = true
}

# What a member may do is a grant: one (subject, role) pair.
resource "plakar_grant" "alice_audits" {
  organization_id = plakar_organization.lyon.id
  subject_id      = plakar_member.alice.id
  role            = "auditor"
}

resource "plakar_grant" "nightly_runs" {
  organization_id = plakar_organization.lyon.id
  subject_id      = plakar_member.nightly.id
  role            = "operator"
}
