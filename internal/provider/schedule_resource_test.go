// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// The issue's headline: a backup schedule and a prune schedule carrying the
// retention rule, wired to resources declared in the same config.
func TestAccSchedule_lifecycle(t *testing.T) {
	base := `
data "plakar_connector" "src" {
  name = "S3 Source"
  type = "source"
}

resource "plakar_store" "lab" {
  name        = "TF schedule lab store"
  integration = "s3"
  resource    = "Silent Frost"
  environment = "production"
  fields = {
    passphrase             = "correcthorsebatterystaple"
    access_key             = "minioadmin"
    secret_access_key      = "minioadmin"
    port                   = "9000"
    root                   = "/tf-acc-schedule"
    storage_class          = "STANDARD"
    use_tls                = "false"
    tls_insecure_no_verify = "false"
    virtual_host           = "false"
  }
}
`
	config := func(periodicity int, retention string) string {
		return base + fmt.Sprintf(`
resource "plakar_schedule" "nightly" {
  name      = "TF nightly backup"
  type      = "backup"
  origin_id = data.plakar_connector.src.id
  target_id = plakar_store.lab.id
  labels    = ["nightly", "tf"]

  rule {
    periodicity = %d
  }
}

resource "plakar_schedule" "retention" {
  name      = "TF retention policy"
  type      = "prune"
  origin_id = plakar_store.lab.id
  group_by  = "dataset"
  retention = {
%s
  }

  rule {
    periodicity = 86400
  }
}
`, periodicity, retention)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(3600, "    day = 7\n    per_day = 1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("plakar_schedule.nightly", "id"),
					resource.TestCheckResourceAttr("plakar_schedule.nightly", "enabled", "true"),
					resource.TestCheckResourceAttr("plakar_schedule.nightly", "rule.0.periodicity", "3600"),
					resource.TestCheckResourceAttrSet("plakar_schedule.nightly", "rule.0.id"),
					resource.TestCheckResourceAttr("plakar_schedule.retention", "retention.day", "7"),
					resource.TestCheckResourceAttr("plakar_schedule.retention", "group_by", "dataset"),
				),
			},
			{
				Config:   config(3600, "    day = 7\n    per_day = 1"),
				PlanOnly: true,
			},
			{
				// Tighten the cadence and the rule: both in-place.
				Config: config(1800, "    day = 3\n    per_day = 1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_schedule.nightly", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("plakar_schedule.retention", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("plakar_schedule.nightly", "rule.0.periodicity", "1800"),
					resource.TestCheckResourceAttr("plakar_schedule.retention", "retention.day", "3"),
				),
			},
			{
				ResourceName:      "plakar_schedule.nightly",
				ImportState:       true,
				ImportStateVerify: true,
				// Not echoed by the API.
				ImportStateVerifyIgnore: []string{"name", "description"},
			},
		},
	})
}
