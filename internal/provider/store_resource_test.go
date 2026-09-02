// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"plakar": providerserver.NewProtocol6WithError(New("test")()),
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	for _, v := range []string{"PLAKAR_API_URL", "PLAKAR_API_KEY"} {
		if os.Getenv(v) == "" {
			t.Fatalf("%s must be set for acceptance tests", v)
		}
	}
}

// The store lifecycle, and with it step 1's real question: whether v1's
// full-body POST updates and server-side churn coexist with refresh-no-diff.
// Every step ends with an implicit plan that must be empty.
func TestAccStore_lifecycle(t *testing.T) {
	config := func(storageClass string) string {
		return fmt.Sprintf(`
resource "plakar_store" "lab" {
  name        = "TF acceptance store"
  integration = "s3"
  resource    = "Silent Frost"
  environment = "production"
  compression = "LZ4"
  fields = {
    passphrase             = "correcthorsebatterystaple"
    access_key             = "minioadmin"
    secret_access_key      = "minioadmin"
    port                   = "9000"
    root                   = "/tf-acc-store"
    storage_class          = %q
    use_tls                = "false"
    tls_insecure_no_verify = "false"
    virtual_host           = "false"
  }
}
`, storageClass)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("STANDARD"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("plakar_store.lab", "id"),
					resource.TestCheckResourceAttrSet("plakar_store.lab", "urn_id"),
					resource.TestCheckResourceAttr("plakar_store.lab", "protocol", "s3"),
					resource.TestCheckResourceAttr("plakar_store.lab", "fields.storage_class", "STANDARD"),
				),
			},
			{
				// Same config again: the plan must be empty, and stay empty.
				Config:   config("STANDARD"),
				PlanOnly: true,
			},
			{
				// One field changes: an in-place update, never a replace.
				Config: config("STANDARD_IA"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_store.lab", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("plakar_store.lab", "fields.storage_class", "STANDARD_IA"),
			},
			{
				ResourceName:      "plakar_store.lab",
				ImportState:       true,
				ImportStateVerify: true,
				// Creation-time inputs the API does not echo, and the fields
				// map, which import adopts in full while the config declares
				// a subset.
				ImportStateVerifyIgnore: []string{"resource", "initialize", "compression", "fields"},
			},
		},
	})
}
