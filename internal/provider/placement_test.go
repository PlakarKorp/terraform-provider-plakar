// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Data classes belong to sources and a temperature to stores: anywhere else
// the Control Plane answers a 400 without a reason, so the plan refuses them.
func TestCheckPlacement(t *testing.T) {
	classes := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("personal_data")})
	none := types.ListNull(types.StringType)
	empty := types.ListValueMust(types.StringType, []attr.Value{})
	for _, tc := range []struct {
		name        string
		kind        string
		dataClasses types.List
		temperature types.String
		wantErr     bool
	}{
		{"source with data classes", "source", classes, types.StringNull(), false},
		{"destination with data classes", "destination", classes, types.StringNull(), true},
		{"store with data classes", "store", classes, types.StringNull(), true},
		{"store with an empty list", "store", empty, types.StringNull(), false},
		{"store with unknown data classes", "store", types.ListUnknown(types.StringType), types.StringNull(), false},
		{"store with a temperature", "store", none, types.StringValue("hot"), false},
		{"source with a temperature", "source", none, types.StringValue("hot"), true},
		{"destination with a temperature", "destination", none, types.StringValue("cold"), true},
		{"source with an unknown temperature", "source", none, types.StringUnknown(), false},
		{"source with neither", "source", none, types.StringNull(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diags := checkPlacement(tc.kind, tc.dataClasses, tc.temperature)
			if got := diags.HasError(); got != tc.wantErr {
				t.Fatalf("HasError() = %v, want %v (%v)", got, tc.wantErr, diags)
			}
		})
	}
}
