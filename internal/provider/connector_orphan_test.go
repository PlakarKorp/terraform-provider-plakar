// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// testAccOrphanConnector makes, through the API, a connector whose inventory
// resource is then deleted: what a resource removed by hand, or pruned by a
// sync after it left the cloud, leaves behind. It sets *id, and removes the
// inventory and the connector when the test ends.
func testAccOrphanConnector(t *testing.T, name string, id *string) func() {
	return func() {
		c := testAccClient()
		inv, err := c.CreateInventory(&client.InventoryRequest{Name: name, Type: "self-managed"})
		if err != nil {
			t.Fatalf("creating inventory: %v", err)
		}
		t.Cleanup(func() { _ = c.DeleteInventory(inv.ID) })
		res, err := c.CreateInventoryResource(inv.ID, &client.InventoryResourceRequest{
			URN: "urn:tf-acc:orphan/" + name, Name: name, Class: "object-storage", SubClass: "s3",
			Tags: []string{}, Endpoints: []client.InventoryResourceEndpoint{{Endpoint: "orphan.invalid"}},
		})
		if err != nil {
			t.Fatalf("creating inventory resource: %v", err)
		}
		integration, err := c.IntegrationByName("s3")
		if err != nil {
			t.Fatalf("resolving integration: %v", err)
		}
		req := &client.ConnectorRequest{Name: name, URNID: res.URNID, Protocol: "s3", Type: "source",
			Fields: map[string]client.Field{"root": {Value: "/"}}, DataClasses: []string{}}
		req.Integration.ID = integration.ID
		conn, err := c.CreateConnector(req)
		if err != nil {
			t.Fatalf("creating connector: %v", err)
		}
		*id = conn.ID
		t.Cleanup(func() { _ = c.DeleteConnector(conn.ID) })
		if err := c.DeleteInventoryResource(inv.ID, res.URNID); err != nil {
			t.Fatalf("deleting inventory resource: %v", err)
		}
	}
}

// A connector is created with an inventory resource. One whose resource is
// gone imports with resource unset and an empty plan; any change but a new
// resource is refused at plan, a new resource attaches it in place, and
// resource then cannot be unset.
func TestAccConnector_withoutResource(t *testing.T) {
	suffix := acctest.RandString(8)
	name := "TF acceptance orphan " + suffix
	var id string
	hcl := func(connName, resourceLine string) string {
		return fmt.Sprintf(`
resource "plakar_inventory" "hand" {
  name = "TF acceptance orphan hand %[1]s"
  type = "self-managed"
}

resource "plakar_inventory_resource" "bucket" {
  inventory_id = plakar_inventory.hand.id
  urn          = "urn:tf-acc:orphan-target/%[1]s"
  name         = "TF acceptance orphan target %[1]s"
  class        = "object-storage"
  subclass     = "s3"
  endpoints    = ["target.invalid"]
}

resource "plakar_connector" "orphan" {
  name        = %[2]q
  type        = "source"
  integration = "s3"
  %[3]s
  fields = {
    root = "/"
  }
}
`, suffix, connName, resourceLine)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		CheckDestroy:             testAccCheckDestroyed,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      hcl(name, ""),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`a\s+connector\s+is\s+created\s+on\s+an\s+inventory\s+resource`),
			},
			{
				PreConfig:          testAccOrphanConnector(t, name, &id),
				Config:             hcl(name, ""),
				ResourceName:       "plakar_connector.orphan",
				ImportState:        true,
				ImportStatePersist: true,
				ImportStateIdFunc:  func(*terraform.State) (string, error) { return id, nil },
			},
			{
				Config: hcl(name, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("plakar_connector.orphan", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("plakar_connector.orphan", "resource"),
					resource.TestCheckNoResourceAttr("plakar_connector.orphan", "urn_id"),
				),
			},
			{
				Config:      hcl(name+" renamed", ""),
				ExpectError: regexp.MustCompile(`no\s+longer\s+exists,\s+and\s+the\s+Control\s+Plane\s+refuses\s+any\s+change`),
			},
			{
				Config: hcl(name, "resource    = plakar_inventory_resource.bucket.urn"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("plakar_connector.orphan", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttrSet("plakar_connector.orphan", "urn_id"),
			},
			{
				Config:      hcl(name, ""),
				ExpectError: regexp.MustCompile(`resource\s+cannot\s+be\s+unset`),
			},
		},
	})
}

// A managed connector whose resource is deleted behind Terraform's back, then
// recreated under the same URN by the same configuration: the refresh clears
// the connector's resource, and the apply that recreates the resource
// attaches the connector to it again, in place.
func TestAccConnector_resourceComesBack(t *testing.T) {
	suffix := acctest.RandString(8)
	urn := "urn:tf-acc:comes-back/" + suffix
	config := fmt.Sprintf(`
resource "plakar_inventory" "hand" {
  name = "TF acceptance comes back %[1]s"
  type = "self-managed"
}

resource "plakar_inventory_resource" "bucket" {
  inventory_id = plakar_inventory.hand.id
  urn          = %[2]q
  name         = "TF acceptance comes back %[1]s"
  class        = "object-storage"
  subclass     = "s3"
  endpoints    = ["back.invalid"]
}

resource "plakar_connector" "managed" {
  name        = "TF acceptance comes back %[1]s"
  type        = "source"
  integration = "s3"
  resource    = plakar_inventory_resource.bucket.urn
  fields = {
    root = "/"
  }
}
`, suffix, urn)
	var connID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		CheckDestroy:             testAccCheckDestroyed,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					connID = s.RootModule().Resources["plakar_connector.managed"].Primary.ID
					return nil
				},
			},
			{
				// The resource deleted by hand: the connector loses it.
				PreConfig: func() {
					c := testAccClient()
					res, err := c.ResourceRef(urn)
					if err == nil {
						inv, ferr := c.FindInventory("TF acceptance comes back " + suffix)
						if ferr != nil || inv == nil {
							t.Fatalf("finding inventory: %v", ferr)
						}
						err = c.DeleteInventoryResource(inv.ID, res.URNID)
					}
					if err != nil {
						t.Fatalf("deleting the resource by hand: %v", err)
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("plakar_inventory_resource.bucket", plancheck.ResourceActionCreate),
					plancheck.ExpectResourceAction("plakar_connector.managed", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("plakar_connector.managed", "urn_id",
						"plakar_inventory_resource.bucket", "id"),
					func(s *terraform.State) error {
						id := s.RootModule().Resources["plakar_connector.managed"].Primary.ID
						if id != connID {
							return fmt.Errorf("connector replaced: %s, was %s", id, connID)
						}
						conn, err := testAccClient().GetConnector(id)
						if err != nil {
							return err
						}
						if conn.Resource == nil || conn.Resource.URN != urn {
							return fmt.Errorf("connector not attached to %s: %+v", urn, conn.Resource)
						}
						return nil
					},
				),
			},
		},
	})
}
