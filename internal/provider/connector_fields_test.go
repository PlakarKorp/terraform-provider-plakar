// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// The acceptance helpers the store and connector tests share for fields
// that come from a secret provider or travel write-only.

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
			f.Provider = &client.FieldProvider{ID: sp.ID}
		}
		req.Fields[key] = f
		if err := c.UpdateConnector(conn.ID, req); err != nil {
			t.Fatal(err)
		}
	}
}

// testAccCheckConnectorFieldValue compares a plain field on the server with
// the expected value. The message never carries either value.
func testAccCheckConnectorFieldValue(name, key, want string) resource.TestCheckFunc {
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
		switch {
		case !ok:
			return fmt.Errorf("%s: the server has no field %s", name, key)
		case f.Provider != nil:
			return fmt.Errorf("%s: the server holds %s as a reference, want a plain value", name, key)
		case f.Value != want:
			return fmt.Errorf("%s: the server's %s is not the expected value", name, key)
		}
		return nil
	}
}

// testAccInventoryScalewayField reads one field of a scaleway inventory's
// configuration from the server.
func testAccInventoryScalewayField(s *terraform.State, name, key string) (client.Field, error) {
	rs, ok := s.RootModule().Resources[name]
	if !ok {
		return client.Field{}, fmt.Errorf("%s not in state", name)
	}
	inv, err := testAccClient().GetInventory(rs.Primary.ID)
	if err != nil {
		return client.Field{}, err
	}
	if inv.Scaleway == nil {
		return client.Field{}, fmt.Errorf("%s: the server has no scaleway configuration", name)
	}
	switch key {
	case "scw_access_key":
		return inv.Scaleway.AccessKey, nil
	case "scw_secret_key":
		return inv.Scaleway.SecretKey, nil
	}
	return client.Field{}, fmt.Errorf("unknown scaleway field %s", key)
}

// testAccCheckInventoryField checks that a scaleway inventory field is a
// reference to the named secret provider at path.
func testAccCheckInventoryField(name, key, spName, wantPath string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		f, err := testAccInventoryScalewayField(s, name, key)
		if err != nil {
			return err
		}
		sp, ok := s.RootModule().Resources[spName]
		if !ok {
			return fmt.Errorf("%s not in state", spName)
		}
		if f.Provider == nil || f.Provider.ID != sp.Primary.ID || f.Value != wantPath {
			return fmt.Errorf("%s: the server does not hold %s as a reference to %s at %q", name, key, spName, wantPath)
		}
		return nil
	}
}

// testAccCheckInventoryFieldValue checks a plain scaleway inventory field
// against the expected value. The message never carries either value.
func testAccCheckInventoryFieldValue(name, key, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		f, err := testAccInventoryScalewayField(s, name, key)
		if err != nil {
			return err
		}
		if f.Provider != nil || f.Value != want {
			return fmt.Errorf("%s: the server's %s is not the expected plain value", name, key)
		}
		return nil
	}
}
