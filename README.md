# Terraform Provider — plakarkorp/plakar

Manage [Plakar](https://plakar.io) backup configuration as code: organizations,
members, grants, inventories, stores, connectors and schedules as versioned
Terraform resources, provisioned and evolved declaratively.

```hcl
terraform {
  required_providers {
    plakar = {
      source = "plakarkorp/plakar"
    }
  }
}

provider "plakar" {
  api_url = "https://plakar.example.com" # or PLAKAR_API_URL
  api_key = var.plakar_api_key           # or PLAKAR_API_KEY
}

resource "plakar_store" "offsite" {
  name        = "Offsite S3"
  integration = "s3"
  resource    = "Ample Sky" # inventory resource, by URN or name
  environment = "production"
  fields = {
    passphrase        = var.repo_passphrase
    access_key        = var.s3_access_key
    secret_access_key = var.s3_secret_key
    root              = "/backups"
  }
}

data "plakar_connector" "db" {
  name = "Production DB"
  type = "source"
}

resource "plakar_schedule" "nightly" {
  name      = "Nightly database backup"
  type      = "backup"
  origin_id = data.plakar_connector.db.id
  target_id = plakar_store.offsite.id
  labels    = ["nightly"]

  rule {
    periodicity = 86400 # seconds
  }
}

resource "plakar_schedule" "retention" {
  name      = "Retention policy"
  type      = "prune"
  origin_id = plakar_store.offsite.id
  group_by  = "dataset" # the rule holds per source, not store-wide
  retention = {
    day       = 7
    per_day   = 1
    month     = 12
    per_month = 1
  }

  rule {
    periodicity = 86400
  }
}
```

## Resources and data sources

| Name | Purpose |
| --- | --- |
| `plakar_store` (resource) | A store — where backup data lands; storage initialized on creation |
| `plakar_connector` (resource) | A source or destination connector |
| `plakar_schedule` (resource) | A scheduled backup, prune, sync or check; a scheduled prune carries the retention rule |
| `plakar_inventory` (resource) | An inventory — provider-backed (aws, ovh, scaleway, gcp, vmware, k8s) or self-managed |
| `plakar_inventory_resource` (resource) | A resource declared in a self-managed inventory — the fleet, as code |
| `plakar_organization` (resource) | A tenant, or a perimeter nested under one — destroying it deletes the tenant |
| `plakar_member` (resource) | A person or service account belonging to an organization; carries no permission |
| `plakar_grant` (resource) | A role held by a member — one (subject, role) pair |
| `plakar_store` / `plakar_connector` (data) | Look up existing stores and connectors by name — identity only, no credentials |
| `plakar_inventory` (data) | An inventory, by name — identity only |
| `plakar_organization` (data) | An organization, by name across the provider organization's subtree |
| `plakar_member` (data) | A member, by email (a person) or name (a service account) |
| `plakar_resource` (data) | An inventory resource, by URN or name |
| `plakar_integration` (data) | An installed integration, by name |

All resources import by their id: `terraform import plakar_store.offsite <uuid>`
(`plakar_inventory_resource` by `<inventory_id>/<urn_id>`).

A deployment's tenants, their people and their permissions are three separate
facts, declared separately — an organization nests under another, a membership
carries no permission, and a grant is one (subject, role) pair:

```hcl
resource "plakar_organization" "lyon" {
  name = "Lyon"
}

resource "plakar_organization" "lyon_production" {
  name      = "Lyon production"
  parent_id = plakar_organization.lyon.id
}

resource "plakar_member" "alice" {
  organization_id = plakar_organization.lyon.id
  email           = "alice@lyon.example"
  name            = "Alice"
}

# A service account for automation: no email, no interactive login; its API
# key is minted in the Plakar UI.
resource "plakar_member" "nightly" {
  organization_id = plakar_organization.lyon_production.id
  name            = "nightly-backups"
  service         = true
}

resource "plakar_grant" "alice_owns_lyon" {
  organization_id = plakar_organization.lyon.id
  subject_id      = plakar_member.alice.id
  role            = "owner"
}

resource "plakar_grant" "nightly_operates" {
  organization_id = plakar_organization.lyon_production.id
  subject_id      = plakar_member.nightly.id
  role            = "operator"
}

# A brand-new address gets an account with a one-time generated password —
# sensitive, kept in state, shown by `terraform output`.
output "initial_passwords" {
  sensitive = true
  value     = { alice = plakar_member.alice.generated_password }
}
```

A self-managed inventory closes the loop from machine to backup. Inventories,
stores, connectors and schedules are read through the badge's organization, so
a tenant's fleet is declared through a provider alias re-scoped to it
(`provider "plakar" { alias = "lyon", organization_id = ... }`) — the API key
stays the root's, the badge does the traveling:

```hcl
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
}

resource "plakar_connector" "db1_dump" {
  name        = "db1 dump"
  type        = "source"
  integration = "postgres"
  resource    = plakar_inventory_resource.db1.urn
  fields = {
    connection_string = var.db1_connection_string
  }
}
```

## Authentication

The provider authenticates with an API key belonging to a service account
(`pcp_ak_...`). The key is bound to one organization; `organization_id`
re-scopes when the account is a member of another. Connector fields are
sensitive and land in Terraform state — treat state accordingly.

Destroying a `plakar_store` removes the store from Plakar; data in the
underlying storage is not touched.

## Development

Acceptance tests run against a live dev stack:

```sh
TF_ACC=1 PLAKAR_API_URL=http://localhost:8080 PLAKAR_API_KEY=pcp_ak_... go test ./internal/...
```

Docs are generated with tfplugindocs; releases are goreleaser-built and
GPG-signed for the Terraform registry (see `.goreleaser.yml`).

## License

ISC, like Plakar itself.
