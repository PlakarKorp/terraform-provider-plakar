# <id> of the installed version, as GET /api/v1/integrations/installed lists it.
# An integration already installed must be imported before it is managed, or
# a destroy would uninstall what Terraform never installed.
terraform import 'plakar_integration.this["vault"]' 5d2e8f1a-3c4b-4a7d-9e6f-1b2c3d4e5f60
