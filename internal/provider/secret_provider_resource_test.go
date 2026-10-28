// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"encoding/json"
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

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// The secret provider lifecycle, with its credential write-only. The API reads
// the credential back in clear, so the test's real question is whether it
// ever reaches state or a plan: the token is a fresh canary, searched for in
// the serialized state and plan at every step, and checked on the server to
// prove it was sent at all. Creating a secret provider does not contact the
// secret manager, so no Vault is needed; PLAKAR_ACC_VAULT_URL only makes the
// url realistic.
func TestAccSecretProvider_lifecycle(t *testing.T) {
	first := "plakar-acc-" + acctest.RandString(24)
	rotated := "plakar-acc-" + acctest.RandString(24)
	ignored := "plakar-acc-" + acctest.RandString(24)
	url := os.Getenv("PLAKAR_ACC_VAULT_URL")
	if url == "" {
		url = "http://vault.invalid:8200"
	}
	// The token travels the way a practitioner's would: an ephemeral
	// variable, which no plan or state records. A literal would be copied into
	// the plan's configuration and prove nothing about the provider.
	hcl := func(name string, version int) string {
		return fmt.Sprintf(`
variable "token" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_secret_provider" "lab" {
  name        = %q
  integration = "vault"
  fields = {
    url = %q
  }
  fields_wo = {
    token = var.token
  }
  fields_wo_version = %d
}
`, name, url, version)
	}
	token := func(v string) config.Variables {
		return config.Variables{"token": config.StringVariable(v)}
	}
	noCanary := func(values ...string) []statecheck.StateCheck {
		return []statecheck.StateCheck{expectNoValueInState(values...)}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		CheckDestroy: testAccCheckSecretProviderDestroyed,
		Steps: []resource.TestStep{
			{
				Config:          hcl("TF acceptance secret provider", 1),
				ConfigVariables: token(first),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{expectNoValueInPlan(first)},
				},
				ConfigStateChecks: noCanary(first),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("plakar_secret_provider.lab", "id"),
					resource.TestCheckResourceAttr("plakar_secret_provider.lab", "protocol", "vault-sm"),
					resource.TestCheckResourceAttr("plakar_secret_provider.lab", "fields.url", url),
					resource.TestCheckNoResourceAttr("plakar_secret_provider.lab", "fields_wo"),
					testAccCheckSecretProviderField("plakar_secret_provider.lab", "token", first),
				),
			},
			{
				Config:          hcl("TF acceptance secret provider", 1),
				ConfigVariables: token(first),
				PlanOnly:        true,
			},
			// A rename without a version change carries no token from the
			// configuration; the update sends back the one the server holds,
			// since v1's PUT deletes a field left out. It has to survive.
			{
				Config:          hcl("TF acceptance secret provider renamed", 1),
				ConfigVariables: token(first),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_secret_provider.lab", plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: noCanary(first),
				Check:             testAccCheckSecretProviderField("plakar_secret_provider.lab", "token", first),
			},
			// The documented rotation: bump the version.
			{
				Config:          hcl("TF acceptance secret provider renamed", 2),
				ConfigVariables: token(rotated),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_secret_provider.lab", plancheck.ResourceActionUpdate),
						expectNoValueInPlan(first, rotated),
					},
				},
				ConfigStateChecks: noCanary(first, rotated),
				Check:             testAccCheckSecretProviderField("plakar_secret_provider.lab", "token", rotated),
			},
			// A new write-only value without a version bump plans nothing.
			{
				Config:          hcl("TF acceptance secret provider renamed", 2),
				ConfigVariables: token(ignored),
				PlanOnly:        true,
			},
			{
				ConfigVariables: token(rotated),
				Config: hcl("TF acceptance secret provider renamed", 2) + `
data "plakar_secret_provider" "lab" {
  name = plakar_secret_provider.lab.name
}
`,
				ConfigStateChecks: noCanary(first, rotated),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.plakar_secret_provider.lab", "id",
						"plakar_secret_provider.lab", "id"),
					resource.TestCheckResourceAttr("data.plakar_secret_provider.lab", "integration", "vault"),
					resource.TestCheckResourceAttr("data.plakar_secret_provider.lab", "protocol", "vault-sm"),
				),
			},
			// Import adopts the integration's non-sensitive keys only.
			{
				ResourceName:            "plakar_secret_provider.lab",
				ConfigVariables:         token(rotated),
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"fields_wo", "fields_wo_version"},
				ImportStateCheck:        testAccCheckImportHoldsNone(first, rotated),
			},
		},
	})
}

// Looking up a secret provider that does not exist is an error naming it, not
// an empty result a configuration would carry on with.
func TestAccSecretProvider_dataSourceNotFound(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "plakar_secret_provider" "missing" {
  name = "TF acceptance no such secret provider %s"
}
`, acctest.RandString(8)),
				ExpectError: regexp.MustCompile(`secret provider not found`),
			},
		},
	})
}

// The canary check has to be able to fail: with the token in fields, where it
// does belong to state, the same check must report it.
func TestAccSecretProvider_canaryCatchesLeak(t *testing.T) {
	token := "plakar-acc-" + acctest.RandString(24)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSecretProviderDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "plakar_secret_provider" "leaky" {
  name        = "TF acceptance leaky secret provider"
  integration = "vault"
  fields = {
    url   = "http://vault.invalid:8200"
    token = %q
  }
}
`, token),
				ConfigStateChecks: []statecheck.StateCheck{expectNoValueInState(token)},
				ExpectError:       regexp.MustCompile(`state holds a secret value`),
			},
		},
	})
}

// testAccCheckSecretProviderField compares a field's value on the server with
// the expected one. The message never carries either value.
func testAccCheckSecretProviderField(name, key, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not in state", name)
		}
		sp, err := testAccClient().GetSecretProvider(rs.Primary.ID)
		if err != nil {
			return err
		}
		if sp.Fields[key].Value != want {
			return fmt.Errorf("%s: the server's %s is not the configured one", name, key)
		}
		return nil
	}
}

func testAccCheckSecretProviderDestroyed(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "plakar_secret_provider" {
			continue
		}
		_, err := testAccClient().GetSecretProvider(rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("secret provider %s still exists", rs.Primary.ID)
		}
		if !client.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func testAccCheckImportHoldsNone(values ...string) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		for _, is := range states {
			for k, v := range is.Attributes {
				for _, value := range values {
					if strings.Contains(v, value) {
						return fmt.Errorf("the imported state holds a secret value in %s", k)
					}
				}
			}
		}
		return nil
	}
}

// expectNoValueInState and expectNoValueInPlan search the serialized state and
// plan, as `terraform show -json` renders them, for values that must never be
// there. A failure names where, never what.
func expectNoValueInState(values ...string) statecheck.StateCheck {
	return noValueCheck{values: values}
}

func expectNoValueInPlan(values ...string) plancheck.PlanCheck {
	return noValueCheck{values: values}
}

type noValueCheck struct{ values []string }

func (c noValueCheck) CheckState(_ context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	resp.Error = c.search("state", req.State)
}

func (c noValueCheck) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	resp.Error = c.search("plan", req.Plan)
}

func (c noValueCheck) search(what string, doc any) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	for _, v := range c.values {
		if strings.Contains(string(raw), v) {
			return fmt.Errorf("the %s holds a secret value", what)
		}
	}
	return nil
}

// testAccSecretFieldsPreCheck asks for what resolving secrets for real needs:
// a Vault the API can reach and a token to it, a KV path holding passphrase,
// access_key and secret_access_key, the S3 location (endpoint/bucket) those
// keys open, and the secret values themselves, comma-separated, which the
// tests search state and plans for.
func testAccSecretFieldsPreCheck(t *testing.T) {
	t.Helper()
	testAccPreCheck(t)
	for _, v := range []string{"PLAKAR_ACC_VAULT_URL", "PLAKAR_ACC_VAULT_TOKEN", "PLAKAR_ACC_VAULT_PATH",
		"PLAKAR_ACC_S3_LOCATION", "PLAKAR_ACC_CANARIES"} {
		if os.Getenv(v) == "" {
			t.Fatalf("%s must be set for secret field acceptance tests", v)
		}
	}
}

// testAccCanaries is every value a secret-field test must never find in state
// or a plan: the secrets behind the paths, and the secret provider's token.
func testAccCanaries() []string {
	values := []string{os.Getenv("PLAKAR_ACC_VAULT_TOKEN")}
	for _, v := range strings.Split(os.Getenv("PLAKAR_ACC_CANARIES"), ",") {
		if v != "" {
			values = append(values, v)
		}
	}
	return values
}

// testAccSecretFieldsBase declares what a store or connector resolving its
// secrets needs around it: the secret provider, and an inventory resource
// whose endpoint is the bucket. The token arrives through an ephemeral
// variable, like a practitioner's would.
func testAccSecretFieldsBase(suffix string) string {
	return fmt.Sprintf(`
variable "token" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "plakar_secret_provider" "vault" {
  name        = "TF acceptance vault %[1]s"
  integration = "vault"
  fields = {
    url = %[2]q
  }
  fields_wo = {
    token = var.token
  }
  fields_wo_version = 1
}

resource "plakar_inventory" "objects" {
  name = "TF acceptance objects %[1]s"
  type = "self-managed"
}

resource "plakar_inventory_resource" "bucket" {
  inventory_id = plakar_inventory.objects.id
  urn          = "urn:tf-acc:object-storage/%[1]s"
  name         = "TF acceptance bucket %[1]s"
  class        = "object-storage"
  subclass     = "s3"
  endpoints    = [%[3]q]
}
`, suffix, os.Getenv("PLAKAR_ACC_VAULT_URL"), os.Getenv("PLAKAR_ACC_S3_LOCATION"))
}

func testAccSecretFieldsVariables() config.Variables {
	return config.Variables{"token": config.StringVariable(os.Getenv("PLAKAR_ACC_VAULT_TOKEN"))}
}

// testAccCheckConnectorField checks how the server holds one field: as a
// reference to the named secret provider at path, or, with an empty path, as
// a plain value. The message never carries a value.
func testAccCheckConnectorField(name, key, spName, wantPath string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not in state", name)
		}
		conn, err := testAccClient().GetConnector(rs.Primary.ID)
		if err != nil {
			return err
		}
		f, ok := conn.Fields[key]
		if !ok {
			return fmt.Errorf("%s: the server has no field %s", name, key)
		}
		if wantPath == "" {
			if f.Provider != nil {
				return fmt.Errorf("%s: the server holds %s as a reference, want a plain value", name, key)
			}
			return nil
		}
		sp, ok := s.RootModule().Resources[spName]
		if !ok {
			return fmt.Errorf("%s not in state", spName)
		}
		if f.Provider == nil || f.Provider.ID != sp.Primary.ID {
			return fmt.Errorf("%s: the server does not hold %s as a reference to %s", name, key, spName)
		}
		if f.Value != wantPath {
			return fmt.Errorf("%s: the server's %s points to %q, want %q", name, key, f.Value, wantPath)
		}
		return nil
	}
}

// testAccSetConnectorField rewrites one field on the server behind
// Terraform's back, the way a change made in the UI would: a reference to the
// named secret provider, or a plain value when spName is empty. Both objects
// are found by name, a PreConfig having no state to read ids from.
func testAccSetConnectorField(t *testing.T, kind, name, key, value, spName string) func() {
	return func() {
		c := testAccClient()
		found, err := c.FindConnector(kind, name)
		if err != nil || found == nil {
			t.Fatalf("finding %s %q: %v", kind, name, err)
		}
		conn, err := c.GetConnector(found.ID)
		if err != nil {
			t.Fatal(err)
		}
		req := &client.ConnectorRequest{
			Name: conn.Name, Type: conn.Type, Protocol: conn.Protocol, Fields: conn.Fields,
			Endpoints: conn.Endpoints, DataClasses: conn.DataClasses, Environment: conn.Environment,
			Temperature: conn.Temperature,
		}
		if req.DataClasses == nil {
			req.DataClasses = []string{}
		}
		req.Integration.ID = conn.Integration.ID
		if conn.Resource != nil {
			req.URNID = conn.Resource.URNID
		}
		f := client.Field{Value: value}
		if spName != "" {
			sp, err := c.FindSecretProvider(spName)
			if err != nil || sp == nil {
				t.Fatalf("finding secret provider %q: %v", spName, err)
			}
			f.Provider = &struct {
				ID string `json:"id"`
			}{ID: sp.ID}
		}
		req.Fields[key] = f
		if err := c.UpdateConnector(conn.ID, req); err != nil {
			t.Fatal(err)
		}
	}
}
