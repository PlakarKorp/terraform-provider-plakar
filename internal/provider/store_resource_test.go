// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
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

// A store whose passphrase and S3 keys are resolved from Vault. Creating it
// initializes the storage, which only works if Plakar resolved all three, so
// the first step alone proves the references reach the server intact; every
// later step searches state and plan for the secret values behind them. The
// last steps move fields between forms behind Terraform's back and in the
// configuration, and import the store back.
func TestAccStore_secretFields(t *testing.T) {
	suffix := acctest.RandString(8)
	storeName := "TF acceptance store " + suffix
	spName := "TF acceptance vault " + suffix
	vaultPath := os.Getenv("PLAKAR_ACC_VAULT_PATH")
	ref := func(key string) string { return vaultPath + "#" + key }
	config := func(accessKeyPath, extraFields, extraSecretFields string) string {
		return testAccSecretFieldsBase(suffix) + fmt.Sprintf(`
resource "plakar_store" "lab" {
  name        = %q
  integration = "s3"
  resource    = plakar_inventory_resource.bucket.urn
  fields = {
    root = "/tf-acc-%s"
    %s
  }
  secret_fields = {
    passphrase        = { provider = plakar_secret_provider.vault.id, path = %q }
    access_key        = { provider = plakar_secret_provider.vault.id, path = %q }
    secret_access_key = { provider = plakar_secret_provider.vault.id, path = %q }
    %s
  }
}
`, storeName, suffix, extraFields, ref("passphrase"), accessKeyPath, ref("secret_access_key"), extraSecretFields)
	}
	base := config(ref("access_key"), `storage_class = "STANDARD"`, "")
	noSecret := []statecheck.StateCheck{expectNoValueInState(testAccCanaries()...)}
	update := func(extra ...plancheck.PlanCheck) resource.ConfigPlanChecks {
		return resource.ConfigPlanChecks{PreApply: append([]plancheck.PlanCheck{
			plancheck.ExpectResourceAction("plakar_store.lab", plancheck.ResourceActionUpdate),
			expectNoValueInPlan(testAccCanaries()...),
		}, extra...)}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccSecretFieldsPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		Steps: []resource.TestStep{
			{
				Config:          base,
				ConfigVariables: testAccSecretFieldsVariables(),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{expectNoValueInPlan(testAccCanaries()...)},
				},
				ConfigStateChecks: noSecret,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("plakar_store.lab", "secret_fields.passphrase.path", ref("passphrase")),
					resource.TestCheckResourceAttrPair("plakar_store.lab", "secret_fields.passphrase.provider",
						"plakar_secret_provider.vault", "id"),
					resource.TestCheckNoResourceAttr("plakar_store.lab", "fields.passphrase"),
					testAccCheckConnectorField("plakar_store.lab", "passphrase", "plakar_secret_provider.vault", ref("passphrase")),
					testAccCheckConnectorField("plakar_store.lab", "secret_access_key", "plakar_secret_provider.vault", ref("secret_access_key")),
					testAccCheckConnectorField("plakar_store.lab", "storage_class", "", ""),
				),
			},
			{
				Config:          base,
				ConfigVariables: testAccSecretFieldsVariables(),
				PlanOnly:        true,
			},
			// A new path is an in-place update, never a replace.
			{
				Config:            config(ref("access_key_moved"), `storage_class = "STANDARD"`, ""),
				ConfigVariables:   testAccSecretFieldsVariables(),
				ConfigPlanChecks:  update(),
				ConfigStateChecks: noSecret,
				Check:             testAccCheckConnectorField("plakar_store.lab", "access_key", "plakar_secret_provider.vault", ref("access_key_moved")),
			},
			// A plain field turned into a reference, then back.
			{
				Config: config(ref("access_key"), "",
					fmt.Sprintf(`storage_class = { provider = plakar_secret_provider.vault.id, path = %q }`, ref("storage_class"))),
				ConfigVariables:   testAccSecretFieldsVariables(),
				ConfigPlanChecks:  update(),
				ConfigStateChecks: noSecret,
				Check:             testAccCheckConnectorField("plakar_store.lab", "storage_class", "plakar_secret_provider.vault", ref("storage_class")),
			},
			{
				Config:            base,
				ConfigVariables:   testAccSecretFieldsVariables(),
				ConfigPlanChecks:  update(),
				ConfigStateChecks: noSecret,
				Check:             testAccCheckConnectorField("plakar_store.lab", "storage_class", "", ""),
			},
			// Someone turns a plain field into a reference in the UI: the plan
			// shows it rather than an empty diff, and applying restores it.
			{
				PreConfig:          testAccSetConnectorField(t, "store", storeName, "storage_class", ref("storage_class"), spName),
				Config:             base,
				ConfigVariables:    testAccSecretFieldsVariables(),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:          base,
				ConfigVariables: testAccSecretFieldsVariables(),
				Check:           testAccCheckConnectorField("plakar_store.lab", "storage_class", "", ""),
			},
			// And the other way round: a reference turned into a plain value.
			{
				PreConfig:          testAccSetConnectorField(t, "store", storeName, "access_key", "AKIA-NOT-A-REFERENCE", ""),
				Config:             base,
				ConfigVariables:    testAccSecretFieldsVariables(),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:            base,
				ConfigVariables:   testAccSecretFieldsVariables(),
				ConfigStateChecks: noSecret,
				Check:             testAccCheckConnectorField("plakar_store.lab", "access_key", "plakar_secret_provider.vault", ref("access_key")),
			},
			// A reference set outside Terraform on a key the configuration
			// does not declare survives an update of another field.
			{
				PreConfig:         testAccSetConnectorField(t, "store", storeName, "region", ref("region"), spName),
				Config:            config(ref("access_key"), `storage_class = "STANDARD_IA"`, ""),
				ConfigVariables:   testAccSecretFieldsVariables(),
				ConfigPlanChecks:  update(),
				ConfigStateChecks: noSecret,
				Check:             testAccCheckConnectorField("plakar_store.lab", "region", "plakar_secret_provider.vault", ref("region")),
			},
			// Import rebuilds every reference as a reference, never as a plain
			// value; the undeclared one included, since import adopts the map.
			{
				ResourceName:            "plakar_store.lab",
				ConfigVariables:         testAccSecretFieldsVariables(),
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"resource", "compression", "fields", "secret_fields.%", "secret_fields.region"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					for _, is := range states {
						if is.Ephemeral.Type != "plakar_store" {
							continue
						}
						for _, key := range []string{"passphrase", "access_key", "secret_access_key", "region"} {
							if _, ok := is.Attributes["fields."+key]; ok {
								return fmt.Errorf("import adopted %s as a plain value", key)
							}
							if is.Attributes["secret_fields."+key+".path"] == "" {
								return fmt.Errorf("import did not adopt %s as a reference", key)
							}
						}
					}
					return testAccCheckImportHoldsNone(testAccCanaries()...)(states)
				},
			},
		},
	})
}

// A store whose passphrase is write-only: sent at creation, which the
// storage's initialization proves, and never in state or a plan. The steps
// then walk what write-only means for an update: another field changes and
// the passphrase stays; a key leaves fields_wo and keeps its server value; a
// key moves between plain, write-only and reference in every direction that
// involves write-only, without a version bump; a bumped version rotates a
// value; a new value without a bump plans nothing.
func TestAccStore_writeOnlyFields(t *testing.T) {
	suffix := acctest.RandString(8)
	passphrase := "plakar-acc-" + acctest.RandString(24)
	vaultPath := os.Getenv("PLAKAR_ACC_VAULT_PATH")
	ref := func(key string) string { return vaultPath + "#" + key }
	hcl := func(fields, writeOnly, secretFields string, version int) string {
		return testAccSecretFieldsBase(suffix) + fmt.Sprintf(`
variable "passphrase" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_store" "lab" {
  name        = "TF acceptance store wo %s"
  integration = "s3"
  resource    = plakar_inventory_resource.bucket.urn
  fields = {
    root = "/tf-acc-wo-%s"
    %s
  }
  fields_wo = {
    passphrase = var.passphrase
    %s
  }
  fields_wo_version = %d
  secret_fields = {
    access_key        = { provider = plakar_secret_provider.vault.id, path = %q }
    secret_access_key = { provider = plakar_secret_provider.vault.id, path = %q }
    %s
  }
}
`, suffix, suffix, fields, writeOnly, version, ref("access_key"), ref("secret_access_key"), secretFields)
	}
	variables := testAccSecretFieldsVariables()
	variables["passphrase"] = config.StringVariable(passphrase)
	canaries := append(testAccCanaries(), passphrase)
	noSecret := []statecheck.StateCheck{expectNoValueInState(canaries...)}
	update := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
		plancheck.ExpectResourceAction("plakar_store.lab", plancheck.ResourceActionUpdate),
		expectNoValueInPlan(canaries...),
	}}
	step := func(cfg string, checks ...resource.TestCheckFunc) resource.TestStep {
		return resource.TestStep{
			Config: cfg, ConfigVariables: variables, ConfigPlanChecks: update, ConfigStateChecks: noSecret,
			Check: resource.ComposeAggregateTestCheckFunc(append(checks,
				testAccCheckConnectorFieldValue("plakar_store.lab", "passphrase", passphrase))...),
		}
	}
	regionRef := fmt.Sprintf(`region = { provider = plakar_secret_provider.vault.id, path = %q }`, ref("region"))

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccSecretFieldsPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		Steps: []resource.TestStep{
			{
				Config:          hcl(`storage_class = "STANDARD"`, `region = "fr-par"`, "", 1),
				ConfigVariables: variables,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{expectNoValueInPlan(canaries...)},
				},
				ConfigStateChecks: noSecret,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("plakar_store.lab", "fields_wo"),
					resource.TestCheckNoResourceAttr("plakar_store.lab", "fields.passphrase"),
					testAccCheckConnectorFieldValue("plakar_store.lab", "passphrase", passphrase),
					testAccCheckConnectorFieldValue("plakar_store.lab", "region", "fr-par"),
				),
			},
			{
				Config:          hcl(`storage_class = "STANDARD"`, `region = "fr-par"`, "", 1),
				ConfigVariables: variables,
				PlanOnly:        true,
			},
			// Another field changes: the passphrase is not in the plan, and
			// stays what it was on the server.
			step(hcl(`storage_class = "STANDARD_IA"`, `region = "fr-par"`, "", 1),
				testAccCheckConnectorFieldValue("plakar_store.lab", "storage_class", "STANDARD_IA")),
			// A key removed from fields_wo keeps its server-side value.
			step(hcl(`storage_class = "STANDARD"`, "", "", 1),
				testAccCheckConnectorFieldValue("plakar_store.lab", "region", "fr-par")),
			// Plain to write-only, no version bump: the moved key is sent.
			step(hcl("", `storage_class = "STANDARD_IA"`, "", 1),
				testAccCheckConnectorFieldValue("plakar_store.lab", "storage_class", "STANDARD_IA")),
			// Write-only to plain.
			step(hcl(`storage_class = "STANDARD"`, "", "", 1),
				testAccCheckConnectorFieldValue("plakar_store.lab", "storage_class", "STANDARD")),
			// The documented rotation: a new value and a bumped version.
			step(hcl(`storage_class = "STANDARD"`, `region = "nl-ams"`, "", 2),
				testAccCheckConnectorFieldValue("plakar_store.lab", "region", "nl-ams")),
			// Write-only to reference, then reference to write-only, again
			// without a version bump.
			step(hcl(`storage_class = "STANDARD"`, "", regionRef, 2),
				testAccCheckConnectorField("plakar_store.lab", "region", "plakar_secret_provider.vault", ref("region"))),
			step(hcl(`storage_class = "STANDARD"`, `region = "fr-par"`, "", 2),
				testAccCheckConnectorFieldValue("plakar_store.lab", "region", "fr-par")),
			// A new write-only value without a bump plans nothing: Terraform
			// cannot see it, and the documentation says so.
			{
				Config:          hcl(`storage_class = "STANDARD"`, `region = "pl-waw"`, "", 2),
				ConfigVariables: variables,
				PlanOnly:        true,
			},
			// Import cannot recover write-only values, and must not adopt the
			// passphrase as a plain value either: it is sensitive.
			{
				ResourceName:      "plakar_store.lab",
				ConfigVariables:   variables,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"resource", "compression", "fields",
					"fields_wo", "fields_wo_version"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					for _, is := range states {
						if _, ok := is.Attributes["fields.passphrase"]; ok && is.Ephemeral.Type == "plakar_store" {
							return fmt.Errorf("import adopted the passphrase as a plain value")
						}
					}
					return testAccCheckImportHoldsNone(canaries...)(states)
				},
			},
		},
	})
}

// A store first made in the web interface, its storage initialized there:
// initialize = false is the value to use. An import reads initialize as
// true, the default, so the next plan turns it to false, an update that must
// apply cleanly and leave the plan empty.
func TestAccStore_importThenNoInitialize(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test, set TF_ACC")
	}
	testAccPreCheck(t)
	name := "TF acceptance store made elsewhere " + acctest.RandString(8)
	var id string
	// The import reads the resource back as its URN: the configuration
	// names it the same way, or the plan would replace the store.
	res, err := testAccClient().ResourceRef("Silent Frost")
	if err != nil {
		t.Fatalf("resolving resource: %v", err)
	}
	config := fmt.Sprintf(`
resource "plakar_store" "elsewhere" {
  name        = %q
  integration = "s3"
  resource    = %q
  initialize  = false
  fields = {
    root = "/tf-acc-store-elsewhere"
  }
}
`, name, res.URN)
	makeStore := func() {
		c := testAccClient()
		integration, err := c.IntegrationByName("s3")
		if err != nil {
			t.Fatalf("resolving integration: %v", err)
		}
		req := &client.ConnectorRequest{Name: name, URNID: res.URNID, Protocol: "s3", Type: "store",
			Fields: map[string]client.Field{"root": {Value: "/tf-acc-store-elsewhere"}}, DataClasses: []string{}}
		req.Integration.ID = integration.ID
		conn, err := c.CreateConnector(req)
		if err != nil {
			t.Fatalf("creating store: %v", err)
		}
		id = conn.ID
		t.Cleanup(func() { _ = c.DeleteConnector(conn.ID) })
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:          makeStore,
				Config:             config,
				ResourceName:       "plakar_store.elsewhere",
				ImportState:        true,
				ImportStatePersist: true,
				ImportStateIdFunc:  func(*terraform.State) (string, error) { return id, nil },
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if got := states[0].Attributes["initialize"]; got != "true" {
						return fmt.Errorf("imported initialize = %q, want true", got)
					}
					return nil
				},
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("plakar_store.elsewhere", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("plakar_store.elsewhere", "initialize", "false"),
			},
		},
	})
}
