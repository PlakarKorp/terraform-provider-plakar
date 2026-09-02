// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
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
