// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
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

// A scaleway inventory with its keys out of state: references first, then the
// secret key write-only. The inventory update is a full-body POST with no
// merge, so the step that only changes project_id is the one that matters: a
// write-only key that is not sent again must go out as the server's, not
// empty. Import must rebuild the references and leave the write-only key
// server-side.
func TestAccInventory_scalewaySecrets(t *testing.T) {
	suffix := acctest.RandString(8)
	first := "plakar-acc-" + acctest.RandString(24)
	rotated := "plakar-acc-" + acctest.RandString(24)
	ref := func(key string) string { return os.Getenv("PLAKAR_ACC_VAULT_PATH") + "#" + key }
	hcl := func(projectID, secretKey string) string {
		return testAccSecretFieldsBase(suffix) + fmt.Sprintf(`
variable "secret_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_inventory" "scw" {
  name = "TF acceptance scaleway %s"
  type = "scaleway"
  scaleway {
    project_id        = %q
    access_key_secret = { provider = plakar_secret_provider.vault.id, path = %q }
    %s
  }
}
`, suffix, projectID, ref("access_key"), secretKey)
	}
	secretRef := fmt.Sprintf(`secret_key_secret = { provider = plakar_secret_provider.vault.id, path = %q }`, ref("secret_access_key"))
	writeOnly := func(version int) string {
		return fmt.Sprintf("secret_key_wo = var.secret_key\n    secret_key_wo_version = %d", version)
	}
	project := "00000000-0000-0000-0000-000000000001"
	variables := func(secret string) config.Variables {
		v := testAccSecretFieldsVariables()
		v["secret_key"] = config.StringVariable(secret)
		return v
	}
	canaries := append(testAccCanaries(), first, rotated)
	noSecret := []statecheck.StateCheck{expectNoValueInState(canaries...)}
	update := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
		plancheck.ExpectResourceAction("plakar_inventory.scw", plancheck.ResourceActionUpdate),
		expectNoValueInPlan(canaries...),
	}}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccSecretFieldsPreCheck(t) },
		CheckDestroy:             testAccCheckDestroyed,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		Steps: []resource.TestStep{
			{
				Config:          hcl(project, secretRef),
				ConfigVariables: variables(first),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{expectNoValueInPlan(canaries...)},
				},
				ConfigStateChecks: noSecret,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("plakar_inventory.scw", "scaleway.secret_key_secret.path", ref("secret_access_key")),
					resource.TestCheckNoResourceAttr("plakar_inventory.scw", "scaleway.secret_key"),
					testAccCheckInventoryField("plakar_inventory.scw", "scw_secret_key", "plakar_secret_provider.vault", ref("secret_access_key")),
					testAccCheckInventoryField("plakar_inventory.scw", "scw_access_key", "plakar_secret_provider.vault", ref("access_key")),
				),
			},
			{
				Config:          hcl(project, secretRef),
				ConfigVariables: variables(first),
				PlanOnly:        true,
			},
			// Reference to write-only, without a version bump.
			{
				Config: hcl(project, writeOnly(1)), ConfigVariables: variables(first),
				ConfigPlanChecks: update, ConfigStateChecks: noSecret,
				Check: testAccCheckInventoryFieldValue("plakar_inventory.scw", "scw_secret_key", first),
			},
			{
				Config:          hcl(project, writeOnly(1)),
				ConfigVariables: variables(first),
				PlanOnly:        true,
			},
			// Only project_id changes: the write-only key is not sent, and
			// must survive the full-body update.
			{
				Config: hcl("00000000-0000-0000-0000-000000000002", writeOnly(1)), ConfigVariables: variables(first),
				ConfigPlanChecks: update, ConfigStateChecks: noSecret,
				Check: testAccCheckInventoryFieldValue("plakar_inventory.scw", "scw_secret_key", first),
			},
			// Rotation: a new value and a bumped version.
			{
				Config: hcl(project, writeOnly(2)), ConfigVariables: variables(rotated),
				ConfigPlanChecks: update, ConfigStateChecks: noSecret,
				Check: testAccCheckInventoryFieldValue("plakar_inventory.scw", "scw_secret_key", rotated),
			},
			// Import: the access key comes back as a reference, the
			// write-only secret key stays on the server.
			{
				ResourceName:            "plakar_inventory.scw",
				ConfigVariables:         variables(rotated),
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"scaleway.secret_key_wo", "scaleway.secret_key_wo_version"},
				ImportStateCheck:        testAccCheckImportHoldsNone(canaries...),
			},
			// Write-only back to a reference.
			{
				Config: hcl(project, secretRef), ConfigVariables: variables(rotated),
				ConfigPlanChecks: update, ConfigStateChecks: noSecret,
				Check: testAccCheckInventoryField("plakar_inventory.scw", "scw_secret_key", "plakar_secret_provider.vault", ref("secret_access_key")),
			},
		},
	})
}

// testAccScalewaySecretManagerPreCheck asks for what resolving a key from
// Scaleway Secret Manager needs: the Scaleway project the inventory reads, an
// access key of its own, the id of a Secret Manager secret whose value is a
// JSON object holding that access key's secret under "secret_key", the secret
// key the secret provider reads Secret Manager with, and the values that must
// never reach state, comma-separated.
func testAccScalewaySecretManagerPreCheck(t *testing.T) {
	t.Helper()
	testAccSecretFieldsPreCheck(t)
	for _, v := range []string{"PLAKAR_ACC_SCW_PROJECT_ID", "PLAKAR_ACC_SCW_ACCESS_KEY",
		"PLAKAR_ACC_SCW_SECRET_ID", "PLAKAR_ACC_SCW_SM_SECRET_KEY", "PLAKAR_ACC_SCW_CANARIES"} {
		if os.Getenv(v) == "" {
			t.Fatalf("%s must be set for Scaleway Secret Manager acceptance tests", v)
		}
	}
}

// A scaleway inventory whose secret key lives in Scaleway Secret Manager. A
// reference is "<secret id>#<key>", the secret's value a JSON object. The
// server only proves it resolved the key when the inventory synchronizes, so
// every step that changes where the key comes from ends with a sync. The
// secret provider is renamed without its key being sent again, and the key is
// moved to Vault and back, both in place.
func TestAccInventory_scalewaySecretManager(t *testing.T) {
	suffix := acctest.RandString(8)
	ref := os.Getenv("PLAKAR_ACC_SCW_SECRET_ID") + "#secret_key"
	vaultRef := os.Getenv("PLAKAR_ACC_VAULT_PATH") + "#secret_access_key"
	hcl := func(spName, provider, path string) string {
		return testAccSecretFieldsBase(suffix) + fmt.Sprintf(`
variable "scw_sm_secret_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_secret_provider" "scw" {
  name        = %q
  integration = "scaleway"
  fields = {
    scw_region = "fr-par"
  }
  fields_wo = {
    scw_secret_key = var.scw_sm_secret_key
  }
  fields_wo_version = 1
}

resource "plakar_inventory" "scw" {
  name = "TF acceptance scaleway secret manager %s"
  type = "scaleway"
  scaleway {
    project_id        = %q
    access_key        = %q
    secret_key_secret = { provider = %s, path = %q }
  }
}
`, spName, suffix, os.Getenv("PLAKAR_ACC_SCW_PROJECT_ID"), os.Getenv("PLAKAR_ACC_SCW_ACCESS_KEY"), provider, path)
	}
	spName := "TF acceptance scaleway secret manager " + suffix
	scw := "plakar_secret_provider.scw.id"
	vault := "plakar_secret_provider.vault.id"
	variables := testAccSecretFieldsVariables()
	variables["scw_sm_secret_key"] = config.StringVariable(os.Getenv("PLAKAR_ACC_SCW_SM_SECRET_KEY"))
	canaries := append(testAccCanaries(), os.Getenv("PLAKAR_ACC_SCW_SM_SECRET_KEY"))
	for _, v := range strings.Split(os.Getenv("PLAKAR_ACC_SCW_CANARIES"), ",") {
		if v != "" {
			canaries = append(canaries, v)
		}
	}
	noSecret := []statecheck.StateCheck{expectNoValueInState(canaries...)}
	update := func(address string) resource.ConfigPlanChecks {
		return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
			expectNoValueInPlan(canaries...),
		}}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccScalewaySecretManagerPreCheck(t) },
		CheckDestroy:             testAccCheckDestroyed,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		Steps: []resource.TestStep{
			{
				Config:          hcl(spName, scw, ref),
				ConfigVariables: variables,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{expectNoValueInPlan(canaries...)},
				},
				ConfigStateChecks: noSecret,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("plakar_secret_provider.scw", "protocol", "scaleway-secret"),
					testAccCheckSecretProviderField("plakar_secret_provider.scw", "scw_secret_key", os.Getenv("PLAKAR_ACC_SCW_SM_SECRET_KEY")),
					testAccCheckInventoryField("plakar_inventory.scw", "scw_secret_key", "plakar_secret_provider.scw", ref),
					testAccCheckInventorySyncs("plakar_inventory.scw"),
				),
			},
			{
				Config:          hcl(spName, scw, ref),
				ConfigVariables: variables,
				PlanOnly:        true,
			},
			// A rename sends the secret provider again without its key: the
			// server's must survive, and still open Secret Manager.
			{
				Config: hcl(spName+" renamed", scw, ref), ConfigVariables: variables,
				ConfigPlanChecks: update("plakar_secret_provider.scw"), ConfigStateChecks: noSecret,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckSecretProviderField("plakar_secret_provider.scw", "scw_secret_key", os.Getenv("PLAKAR_ACC_SCW_SM_SECRET_KEY")),
					testAccCheckInventorySyncs("plakar_inventory.scw"),
				),
			},
			// Scaleway Secret Manager to Vault, then back: in place both ways.
			{
				Config: hcl(spName+" renamed", vault, vaultRef), ConfigVariables: variables,
				ConfigPlanChecks: update("plakar_inventory.scw"), ConfigStateChecks: noSecret,
				Check: testAccCheckInventoryField("plakar_inventory.scw", "scw_secret_key", "plakar_secret_provider.vault", vaultRef),
			},
			{
				Config: hcl(spName+" renamed", scw, ref), ConfigVariables: variables,
				ConfigPlanChecks: update("plakar_inventory.scw"), ConfigStateChecks: noSecret,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckInventoryField("plakar_inventory.scw", "scw_secret_key", "plakar_secret_provider.scw", ref),
					testAccCheckInventorySyncs("plakar_inventory.scw"),
				),
			},
			{
				ResourceName:            "plakar_secret_provider.scw",
				ConfigVariables:         variables,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"fields_wo", "fields_wo_version"},
				ImportStateCheck:        testAccCheckImportHoldsNone(canaries...),
			},
			// A plain credential is never adopted on import, so the access
			// key stays out; the secret key comes back as its reference.
			{
				ResourceName:            "plakar_inventory.scw",
				ConfigVariables:         variables,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"scaleway.access_key"},
				ImportStateCheck:        testAccCheckImportHoldsNone(canaries...),
			},
		},
	})
}

// The Scaleway canary check has to be able to fail: with a canary as a
// plain secret key, where it does belong to state, the same check must
// report it.
func TestAccInventory_scalewayCanaryCatchesLeak(t *testing.T) {
	canary := strings.Split(os.Getenv("PLAKAR_ACC_SCW_CANARIES"), ",")[0]
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccScalewaySecretManagerPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "plakar_inventory" "leaky" {
  name = "TF acceptance leaky scaleway %s"
  type = "scaleway"
  scaleway {
    project_id = %q
    access_key = %q
    secret_key = %q
  }
}
`, acctest.RandString(8), os.Getenv("PLAKAR_ACC_SCW_PROJECT_ID"), os.Getenv("PLAKAR_ACC_SCW_ACCESS_KEY"), canary),
				ConfigStateChecks: []statecheck.StateCheck{expectNoValueInState(canary)},
				ExpectError:       regexp.MustCompile(`state holds a secret value`),
			},
		},
	})
}

// testAccCheckInventorySyncs asks the server to synchronize the inventory,
// which is when it resolves the credentials: success proves the referenced
// key was read and accepted by the cloud. The message carries the server's
// error, which names no secret.
func testAccCheckInventorySyncs(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not in state", name)
		}
		var res struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := testAccClient().Do("GET", "/api/v1/inventories/"+rs.Primary.ID+"/sync",
			nil, nil, &res); err != nil {
			return err
		}
		if !res.OK {
			return fmt.Errorf("%s did not synchronize: %s", name, res.Error)
		}
		return nil
	}
}
