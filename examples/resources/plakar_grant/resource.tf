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
