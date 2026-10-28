// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestAccConnector_lifecycle(t *testing.T) {
	config := func(environment string) string {
		return fmt.Sprintf(`
resource "plakar_connector" "lab" {
  name        = "TF acceptance source"
  type        = "source"
  integration = "s3"
  resource    = "Novel Core"
  environment = %q
  fields = {
    passphrase        = "correcthorsebatterystaple"
    access_key        = "minioadmin"
    secret_access_key = "minioadmin"
    port              = "9000"
    root              = "/source"
  }
}
`, environment)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("production"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("plakar_connector.lab", "id"),
					resource.TestCheckResourceAttr("plakar_connector.lab", "type", "source"),
					resource.TestCheckResourceAttr("plakar_connector.lab", "protocol", "s3"),
				),
			},
			{
				Config:   config("production"),
				PlanOnly: true,
			},
			{
				Config: config("staging"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_connector.lab", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("plakar_connector.lab", "environment", "staging"),
			},
			{
				ResourceName:            "plakar_connector.lab",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"resource", "fields"},
			},
		},
	})
}

// The four lookups, against the dev stack's seeded objects.
func TestAccDataSources(t *testing.T) {
	config := `
data "plakar_resource" "frost" {
  ref = "Silent Frost"
}

data "plakar_integration" "s3" {
  name = "s3"
}

data "plakar_store" "seeded" {
  name = "S3 Store"
}

data "plakar_connector" "seeded" {
  name = "S3 Source"
  type = "source"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.plakar_resource.frost", "urn_id"),
					resource.TestCheckResourceAttr("data.plakar_resource.frost", "name", "Silent Frost"),
					resource.TestCheckResourceAttrSet("data.plakar_integration.s3", "id"),
					resource.TestCheckResourceAttrSet("data.plakar_store.seeded", "id"),
					resource.TestCheckResourceAttr("data.plakar_store.seeded", "type", "store"),
					resource.TestCheckResourceAttrSet("data.plakar_connector.seeded", "id"),
					resource.TestCheckResourceAttr("data.plakar_connector.seeded", "protocol", "s3"),
				),
			},
		},
	})
}

// A source connector reading its S3 keys from Vault: the server holds the
// references, the next plan is empty, and import rebuilds them as references.
func TestAccConnector_secretFields(t *testing.T) {
	suffix := acctest.RandString(8)
	ref := func(key string) string { return os.Getenv("PLAKAR_ACC_VAULT_PATH") + "#" + key }
	config := testAccSecretFieldsBase(suffix) + fmt.Sprintf(`
resource "plakar_connector" "lab" {
  name        = "TF acceptance source %s"
  type        = "source"
  integration = "s3"
  resource    = plakar_inventory_resource.bucket.urn
  fields = {
    root = "/"
  }
  secret_fields = {
    access_key        = { provider = plakar_secret_provider.vault.id, path = %q }
    secret_access_key = { provider = plakar_secret_provider.vault.id, path = %q }
  }
}
`, suffix, ref("access_key"), ref("secret_access_key"))

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccSecretFieldsPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		Steps: []resource.TestStep{
			{
				Config:          config,
				ConfigVariables: testAccSecretFieldsVariables(),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{expectNoValueInPlan(testAccCanaries()...)},
				},
				ConfigStateChecks: []statecheck.StateCheck{expectNoValueInState(testAccCanaries()...)},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("plakar_connector.lab", "secret_fields.access_key.path", ref("access_key")),
					testAccCheckConnectorField("plakar_connector.lab", "access_key", "plakar_secret_provider.vault", ref("access_key")),
					testAccCheckConnectorField("plakar_connector.lab", "secret_access_key", "plakar_secret_provider.vault", ref("secret_access_key")),
				),
			},
			{
				Config:          config,
				ConfigVariables: testAccSecretFieldsVariables(),
				PlanOnly:        true,
			},
			{
				ResourceName:            "plakar_connector.lab",
				ConfigVariables:         testAccSecretFieldsVariables(),
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"resource", "fields"},
				ImportStateCheck:        testAccCheckImportHoldsNone(testAccCanaries()...),
			},
		},
	})
}

// A source connector whose secret key is write-only: on the server, never in
// state or a plan, and not adopted back by an import.
func TestAccConnector_writeOnlyFields(t *testing.T) {
	suffix := acctest.RandString(8)
	secret := "plakar-acc-" + acctest.RandString(24)
	hcl := testAccSecretFieldsBase(suffix) + fmt.Sprintf(`
variable "secret_access_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_connector" "lab" {
  name        = "TF acceptance source wo %s"
  type        = "source"
  integration = "s3"
  resource    = plakar_inventory_resource.bucket.urn
  fields = {
    root = "/"
  }
  secret_fields = {
    access_key = { provider = plakar_secret_provider.vault.id, path = %q }
  }
  fields_wo = {
    secret_access_key = var.secret_access_key
  }
  fields_wo_version = 1
}
`, suffix, os.Getenv("PLAKAR_ACC_VAULT_PATH")+"#access_key")
	variables := testAccSecretFieldsVariables()
	variables["secret_access_key"] = config.StringVariable(secret)
	canaries := append(testAccCanaries(), secret)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccSecretFieldsPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		Steps: []resource.TestStep{
			{
				Config:          hcl,
				ConfigVariables: variables,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{expectNoValueInPlan(canaries...)},
				},
				ConfigStateChecks: []statecheck.StateCheck{expectNoValueInState(canaries...)},
				Check:             testAccCheckConnectorFieldValue("plakar_connector.lab", "secret_access_key", secret),
			},
			{
				Config:          hcl,
				ConfigVariables: variables,
				PlanOnly:        true,
			},
			{
				ResourceName:            "plakar_connector.lab",
				ConfigVariables:         variables,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"resource", "fields", "fields_wo", "fields_wo_version"},
				ImportStateCheck:        testAccCheckImportHoldsNone(canaries...),
			},
		},
	})
}
