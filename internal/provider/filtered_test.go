// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// What a read that filters sensitive values does to state: a declared key
// the server filtered keeps its state value, a declared plain key refreshes,
// and an import adopts the plain keys only.
func TestRefreshFacetKeepsFilteredFields(t *testing.T) {
	ctx := context.Background()
	conn := &client.Connector{Name: "c", Protocol: "s3", Fields: map[string]client.Field{
		"passphrase": {Filtered: true},
		"root":       {Value: "/server"},
	}}

	m := connectorModel{Fields: fieldsMap(t, map[string]string{"passphrase": "secret", "root": "/state"})}
	var diags diag.Diagnostics
	refreshFacetFromConnector(ctx, m.facet(), conn, &diags)
	if diags.HasError() {
		t.Fatalf("refresh: %v", diags)
	}
	fields := map[string]string{}
	m.Fields.ElementsAs(ctx, &fields, false)
	if fields["passphrase"] != "secret" || fields["root"] != "/server" {
		t.Errorf("fields after refresh %v, want the state passphrase and the server root", fields)
	}

	imported := connectorModel{Fields: types.MapNull(types.StringType)}
	refreshFacetFromConnector(ctx, imported.facet(), conn, &diags)
	if diags.HasError() {
		t.Fatalf("import refresh: %v", diags)
	}
	fields = map[string]string{}
	imported.Fields.ElementsAs(ctx, &fields, false)
	if _, ok := fields["passphrase"]; ok || fields["root"] != "/server" {
		t.Errorf("import adopted %v, want root alone", fields)
	}
}

// An update merged over a filtered read sends the filtered keys back as
// filtered, which keeps their stored values, and the declared keys as
// values.
func TestMergeConnectorRequestResendsFiltered(t *testing.T) {
	ctx := context.Background()
	current := &client.Connector{Protocol: "s3", Fields: map[string]client.Field{
		"passphrase":        {Filtered: true},
		"secret_access_key": {Filtered: true},
		"root":              {Value: "/old"},
	}}
	m := connectorModel{Fields: fieldsMap(t, map[string]string{"passphrase": "secret", "root": "/new"})}
	var diags diag.Diagnostics
	req := mergeConnectorRequest(ctx, m.facet(), current, "store", &diags)
	if diags.HasError() {
		t.Fatalf("merge: %v", diags)
	}
	if f := req.Fields["secret_access_key"]; !f.Filtered || f.Value != "" {
		t.Errorf("an undeclared filtered key goes out as %+v, want filtered", f)
	}
	if f := req.Fields["passphrase"]; f.Filtered || f.Value != "secret" {
		t.Errorf("a declared key goes out as %+v, want its value", f)
	}
	if f := req.Fields["root"]; f.Filtered || f.Value != "/new" {
		t.Errorf("root goes out as %+v", f)
	}
}

// The same for an inventory block: a filtered credential keeps its state
// value, a plain setting refreshes.
func TestRefreshFromInventoryKeepsFiltered(t *testing.T) {
	m := inventoryModel{AWS: &inventoryAWSModel{
		CredentialsType: types.StringValue("access_key"), Region: types.StringValue("eu-west-1"),
		AccessKey: types.StringValue("AKIA"), SecretAccessKey: types.StringValue("secret"),
	}}
	m.refreshFromInventory(&client.Inventory{ID: "inv", Name: "a", Type: "aws", AWS: &client.AWSInventoryConfig{
		CredentialsType: "access_key", Region: "eu-west-3",
		AccessKey: client.Field{Filtered: true}, SecretAccessKey: client.Field{Filtered: true},
	}})
	if m.AWS.AccessKey.ValueString() != "AKIA" || m.AWS.SecretAccessKey.ValueString() != "secret" {
		t.Errorf("credentials after refresh %v / %v, want the state values", m.AWS.AccessKey, m.AWS.SecretAccessKey)
	}
	if m.AWS.Region.ValueString() != "eu-west-3" {
		t.Errorf("region after refresh %v, want the server's", m.AWS.Region)
	}
	imported := inventoryModel{}
	imported.refreshFromInventory(&client.Inventory{ID: "inv", Type: "aws", AWS: &client.AWSInventoryConfig{
		AccessKey: client.Field{Filtered: true}, Region: "eu-west-3",
	}})
	if !imported.AWS.AccessKey.IsNull() {
		t.Errorf("import adopted a filtered credential: %v", imported.AWS.AccessKey)
	}
}
