// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// Both lists, end to end: create, an empty plan, a rename that replaces,
// import by name, and a name deleted behind Terraform's back coming back.
// The names carry what the server keeps as written (case, an accent, a
// space, a slash, a percent sign), so a refresh that rewrote any of them, or
// a delete that addressed another name, would show as a diff.
func TestAccClassification_lifecycle(t *testing.T) {
	suffix := acctest.RandString(8)
	env := "plakar-acc Pré/Prod%20" + suffix
	class := "Plakar-Acc_" + suffix
	renamed := "plakar-acc-renamed-" + suffix
	config := func(env, class string) string {
		return fmt.Sprintf(`
resource "plakar_environment" "test" {
  name = %q
}

resource "plakar_data_class" "test" {
  name = %q
}
`, env, class)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckClassificationDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config(env, class),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("plakar_environment.test", "id", env),
					resource.TestCheckResourceAttrSet("plakar_environment.test", "organization_id"),
					testAccCheckClassification(client.Environments, env, true),
					testAccCheckClassification(client.DataClasses, class, true),
				),
			},
			{
				Config:   config(env, class),
				PlanOnly: true,
			},
			{
				// No update route: a new name is a new entry, the old one goes.
				Config: config(env, renamed),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_data_class.test",
							plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckClassification(client.DataClasses, class, false),
					testAccCheckClassification(client.DataClasses, renamed, true),
				),
			},
			{
				ResourceName:      "plakar_environment.test",
				ImportState:       true,
				ImportStateId:     env,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "plakar_data_class.test",
				ImportState:       true,
				ImportStateId:     renamed,
				ImportStateVerify: true,
			},
			{
				// Deleted by hand: the next plan creates it again.
				PreConfig: func() {
					c := testAccClient()
					org, err := c.OrgID()
					if err == nil {
						err = c.DeleteClassification(org, client.Environments, env)
					}
					if err != nil {
						t.Fatalf("deleting the environment by hand: %v", err)
					}
				},
				Config: config(env, renamed),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_environment.test",
							plancheck.ResourceActionCreate),
					},
				},
				Check: testAccCheckClassification(client.Environments, env, true),
			},
		},
	})
}

// A name the organization already has, a default here, is refused rather
// than adopted, and is still there afterwards. Importing a name that does not
// exist is an error, and an empty name is refused at plan.
func TestAccClassification_existingAndAbsent(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			testAccCheckClassification(client.Environments, "production", true),
			testAccCheckClassification(client.DataClasses, "personal_data", true),
		),
		Steps: []resource.TestStep{
			{
				Config: `
resource "plakar_environment" "test" {
  name = "production"
}
`,
				ExpectError: regexp.MustCompile(`already has the environment\s+"production"`),
			},
			{
				Config: `
resource "plakar_data_class" "test" {
  name = "personal_data"
}
`,
				ExpectError: regexp.MustCompile(`already has the data class\s+"personal_data"`),
			},
			{
				Config: `
resource "plakar_environment" "test" {
  name = "plakar-acc-absent"
}
`,
				ResourceName:  "plakar_environment.test",
				ImportState:   true,
				ImportStateId: "plakar-acc-absent-" + acctest.RandString(8),
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
			{
				Config: `
resource "plakar_environment" "test" {
  name = ""
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`length must be at least 1`),
			},
		},
	})
}

func testAccCheckClassification(kind client.Classification, name string, want bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c := testAccClient()
		org, err := c.OrgID()
		if err != nil {
			return err
		}
		got, err := c.HasClassification(org, kind, name)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%q present: %t, want %t", name, got, want)
		}
		return nil
	}
}

func testAccCheckClassificationDestroyed(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		var kind client.Classification
		switch rs.Type {
		case "plakar_environment":
			kind = client.Environments
		case "plakar_data_class":
			kind = client.DataClasses
		default:
			continue
		}
		if err := testAccCheckClassification(kind, rs.Primary.ID, false)(s); err != nil {
			return fmt.Errorf("%s still exists after destroy: %w", rs.Type, err)
		}
	}
	return nil
}

// Names the server keeps as written: two that differ only by case are two
// entries, a percent sequence is not decoded, and a 256-character name, which
// the search route fails on, still reads back, imports and plans empty.
func TestAccClassification_namesKeptAsWritten(t *testing.T) {
	suffix := acctest.RandString(8)
	upper := "Plakar-Acc-" + suffix + "-EU"
	lower := "plakar-acc-" + suffix + "-eu"
	percent := "plakar-acc%20" + suffix
	long := "plakar-acc-" + suffix + "-"
	long += strings.Repeat("x", 256-len(long))
	config := fmt.Sprintf(`
resource "plakar_environment" "upper" {
  name = %q
}

resource "plakar_environment" "lower" {
  name = %q
}

resource "plakar_data_class" "percent" {
  name = %q
}

resource "plakar_data_class" "long" {
  name = %q
}
`, upper, lower, percent, long)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckClassificationDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckClassification(client.Environments, upper, true),
					testAccCheckClassification(client.Environments, lower, true),
					testAccCheckClassification(client.DataClasses, percent, true),
					testAccCheckClassification(client.DataClasses, "plakar-acc "+suffix, false),
					testAccCheckClassification(client.DataClasses, long, true),
				),
			},
			{
				Config:   config,
				PlanOnly: true,
			},
			{
				ResourceName:      "plakar_data_class.long",
				ImportState:       true,
				ImportStateId:     long,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "plakar_environment.lower",
				ImportState:       true,
				ImportStateId:     lower,
				ImportStateVerify: true,
			},
			{
				Config:   config,
				PlanOnly: true,
			},
		},
	})
}
