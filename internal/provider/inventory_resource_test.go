// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The self-managed story end to end: an inventory declared as code, a
// resource declared in it, and the data-source lookup resolving the
// inventory by name. Every step ends with an implicit plan that must be
// empty.
func TestAccInventory_selfManaged(t *testing.T) {
	config := func(tag string) string {
		return fmt.Sprintf(`
resource "plakar_inventory" "fleet" {
  name = "TF acceptance fleet"
  type = "self-managed"
}

resource "plakar_inventory_resource" "db1" {
  inventory_id = plakar_inventory.fleet.id
  urn          = "urn:tf-acc:database/db1"
  name         = "TF acceptance db1"
  class        = "database"
  subclass     = "postgres"
  endpoints    = ["db1.internal", "10.0.0.12"]
  tags         = [%q]
}

data "plakar_inventory" "fleet" {
  name = plakar_inventory.fleet.name
}
`, tag)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("bronze"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("plakar_inventory.fleet", "id"),
					resource.TestCheckResourceAttr("plakar_inventory.fleet", "type", "self-managed"),
					resource.TestCheckResourceAttrSet("plakar_inventory_resource.db1", "id"),
					resource.TestCheckResourceAttr("plakar_inventory_resource.db1", "class", "database"),
					resource.TestCheckResourceAttr("plakar_inventory_resource.db1", "endpoints.#", "2"),
					resource.TestCheckResourceAttr("plakar_inventory_resource.db1", "locked", "false"),
					resource.TestCheckResourceAttrPair(
						"data.plakar_inventory.fleet", "id", "plakar_inventory.fleet", "id"),
				),
			},
			{
				// Same config again: the plan must be empty, and stay empty.
				Config:   config("bronze"),
				PlanOnly: true,
			},
			{
				// One tag changes: an in-place update, never a replace.
				Config: config("silver"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_inventory_resource.db1", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("plakar_inventory_resource.db1", "tags.0", "silver"),
			},
			{
				ResourceName:      "plakar_inventory.fleet",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "plakar_inventory_resource.db1",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources["plakar_inventory_resource.db1"]
					if !ok {
						return "", fmt.Errorf("plakar_inventory_resource.db1 not in state")
					}
					return rs.Primary.Attributes["inventory_id"] + "/" + rs.Primary.ID, nil
				},
			},
		},
	})
}

// A provider-backed inventory: the typed configuration block, echoed unmasked
// by the API, must hold refresh-no-diff, and a region change must update in
// place. The credentials are fakes — creation only stores them; a sync would
// fail, and no sync runs here.
func TestAccInventory_aws(t *testing.T) {
	config := func(region string) string {
		return fmt.Sprintf(`
resource "plakar_inventory" "aws" {
  name = "TF acceptance aws"
  type = "aws"

  aws {
    credentials_type  = "access_key"
    access_key        = "AKIAFAKEFAKEFAKEFAKE"
    secret_access_key = "fakefakefakefakefakefakefakefakefakefake"
    region            = %q
  }
}
`, region)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("eu-west-1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("plakar_inventory.aws", "id"),
					resource.TestCheckResourceAttr("plakar_inventory.aws", "aws.region", "eu-west-1"),
				),
			},
			{
				Config:   config("eu-west-1"),
				PlanOnly: true,
			},
			{
				Config: config("eu-west-3"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_inventory.aws", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("plakar_inventory.aws", "aws.region", "eu-west-3"),
			},
			{
				ResourceName:      "plakar_inventory.aws",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
