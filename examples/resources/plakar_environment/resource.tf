# A name of your own, next to the defaults every organization starts with.
resource "plakar_environment" "preprod" {
  name = "preprod"
}

# A default is managed only once imported: creating it would be refused. Once
# managed, destroying the resource deletes it from the organization.
import {
  to = plakar_environment.production
  id = "production"
}

resource "plakar_environment" "production" {
  name = "production"
}
