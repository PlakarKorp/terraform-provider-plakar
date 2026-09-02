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
```

The provider authenticates with an API key belonging to a service account
(`pcp_ak_...`); the key is bound to one organization, and `organization_id`
re-scopes when the account is a member of another. Connector fields are
sensitive and land in Terraform state — treat state accordingly.

Destroying a `plakar_store` removes the store from Plakar; data in the
underlying storage is not touched.

## Development

Acceptance tests run against a live dev stack:

```sh
TF_ACC=1 PLAKAR_API_URL=http://localhost:8080 PLAKAR_API_KEY=pcp_ak_... go test ./internal/...
```

## License

ISC, like Plakar itself.
