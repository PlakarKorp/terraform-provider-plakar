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

// The tenant story end to end: an organization, a person and a service
// account belonging to it, a grant on the service account — and the two
// lookups resolving what the resources created. Every step ends with an
// implicit plan that must be empty.
func TestAccOrganization_lifecycle(t *testing.T) {
	config := func(role string) string {
		return fmt.Sprintf(`
resource "plakar_organization" "tenant" {
  name = "TF acceptance tenant"
  info = { managed = "terraform" }
}

resource "plakar_member" "alice" {
  organization_id = plakar_organization.tenant.id
  email           = "tf-acc-alice@example.invalid"
  name            = "Alice"
}

resource "plakar_member" "bot" {
  organization_id = plakar_organization.tenant.id
  name            = "tf-acc-bot"
  service         = true
}

resource "plakar_grant" "bot_runs" {
  organization_id = plakar_organization.tenant.id
  subject_id      = plakar_member.bot.id
  role            = %q
}

data "plakar_organization" "tenant" {
  name = plakar_organization.tenant.name
}

data "plakar_member" "bot" {
  organization_id = plakar_organization.tenant.id
  name            = plakar_member.bot.name
}
`, role)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("auditor"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("plakar_organization.tenant", "id"),
					resource.TestCheckResourceAttr("plakar_organization.tenant", "type", "enterprise"),
					resource.TestCheckResourceAttrSet("plakar_organization.tenant", "parent_id"),
					resource.TestCheckResourceAttrSet("plakar_member.alice", "account"),
					resource.TestCheckResourceAttr("plakar_member.bot", "service", "true"),
					resource.TestCheckResourceAttr("plakar_grant.bot_runs", "role", "auditor"),
					resource.TestCheckResourceAttrPair(
						"data.plakar_organization.tenant", "id", "plakar_organization.tenant", "id"),
					resource.TestCheckResourceAttrPair(
						"data.plakar_member.bot", "id", "plakar_member.bot", "id"),
				),
			},
			{
				// Same config again: the plan must be empty, and stay empty.
				Config:   config("auditor"),
				PlanOnly: true,
			},
			{
				// The role changes: an in-place update on the grant, never a replace.
				Config: config("operator"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_grant.bot_runs", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("plakar_grant.bot_runs", "role", "operator"),
			},
			{
				ResourceName:      "plakar_organization.tenant",
				ImportState:       true,
				ImportStateVerify: true,
				// info refreshes only when the config manages it (it carries
				// RequiresReplace), so an import adopts none.
				ImportStateVerifyIgnore: []string{"info"},
			},
			{
				ResourceName:      "plakar_member.bot",
				ImportState:       true,
				ImportStateVerify: true,
				// Creation-time facts the API never echoes again.
				ImportStateVerifyIgnore: []string{"account_created", "generated_password"},
				ImportStateIdFunc:       importByOrgAndID("plakar_member.bot"),
			},
			{
				ResourceName:            "plakar_member.alice",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"account_created", "generated_password", "name"},
				ImportStateIdFunc:       importByOrgAndID("plakar_member.alice"),
			},
			{
				ResourceName:      "plakar_grant.bot_runs",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: importByOrgAndID("plakar_grant.bot_runs"),
			},
		},
	})
}

// importByOrgAndID builds the "<organization_id>/<id>" import address the
// organization-scoped resources use.
func importByOrgAndID(addr string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[addr]
		if !ok {
			return "", fmt.Errorf("%s not in state", addr)
		}
		return rs.Primary.Attributes["organization_id"] + "/" + rs.Primary.ID, nil
	}
}
