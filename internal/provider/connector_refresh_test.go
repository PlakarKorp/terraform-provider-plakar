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

// What an import adopts of a connector's fields: references as references,
// plain values only for keys the integration describes as not sensitive. A
// sensitive key, or one the integration does not describe, stays server-side.
func TestRefreshFacetImportAdoptsDescribedPlainKeys(t *testing.T) {
	ctx := context.Background()
	m := connectorModel{
		Name: types.StringNull(), Integration: types.StringNull(), Protocol: types.StringNull(),
		Resource: types.StringNull(), URNID: types.StringNull(),
		Fields:       types.MapNull(types.StringType),
		SecretFields: types.MapNull(types.ObjectType{AttrTypes: secretFieldAttrTypes}),
		FieldsWO:     types.MapNull(types.StringType), FieldsWOVersion: types.Int64Null(),
		Environment: types.StringNull(), DataClasses: types.ListNull(types.StringType),
		Temperature: types.StringNull(),
	}
	conn := &client.Connector{Name: "c", Protocol: "s3", Fields: map[string]client.Field{
		"root":              {Value: "/"},
		"secret_access_key": {Value: "plain secret"},
		"undescribed":       {Value: "maybe secret"},
		"access_key":        {Value: "kv/media#access_key", Provider: &client.FieldProvider{ID: "sp"}},
	}}
	sensitive := map[string]bool{"root": false, "secret_access_key": true, "access_key": false}
	var diags diag.Diagnostics
	refreshFacetFromConnector(ctx, m.facet(), conn, sensitive, &diags)
	if diags.HasError() {
		t.Fatalf("refresh: %v", diags)
	}
	fields := map[string]string{}
	m.Fields.ElementsAs(ctx, &fields, false)
	if len(fields) != 1 || fields["root"] != "/" {
		t.Errorf("fields %v, want root alone", fields)
	}
	refs := map[string]secretFieldModel{}
	m.SecretFields.ElementsAs(ctx, &refs, false)
	if len(refs) != 1 || refs["access_key"].Path.ValueString() != "kv/media#access_key" || refs["access_key"].Provider.ValueString() != "sp" {
		t.Errorf("secret_fields %v, want access_key as a reference", refs)
	}
}
