# Terraform Provider — plakarkorp/plakar

Manage [Plakar](https://plakar.io) backup configuration as code: stores,
connectors and schedules as versioned Terraform resources, provisioned and
evolved declaratively.

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
| `plakar_store` / `plakar_connector` (data) | Look up existing stores and connectors by name — identity only, no credentials |
| `plakar_resource` (data) | An inventory resource, by URN or name |
| `plakar_integration` (data) | An installed integration, by name |

All resources import by their id: `terraform import plakar_store.offsite <uuid>`.

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
