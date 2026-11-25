// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The framework only calls ValidateConfig on a resource that announces it, so
// the hook silently never runs if this assertion stops holding.
var _ resource.ResourceWithValidateConfig = &inventoryResource{}

// A credential is set in exactly one form: plain, reference or write-only.
func TestCredentialValidate(t *testing.T) {
	ref := func(provider, path string) types.Object {
		return types.ObjectValueMust(secretFieldAttrTypes, map[string]attr.Value{
			"provider": types.StringValue(provider),
			"path":     types.StringValue(path),
		})
	}
	noRef := types.ObjectNull(secretFieldAttrTypes)
	for _, tc := range []struct {
		name     string
		plain    types.String
		ref      types.Object
		wo       types.String
		required bool
		wantErr  bool
	}{
		{"plain", types.StringValue("k"), noRef, types.StringNull(), true, false},
		{"reference", types.StringNull(), ref("sp", "secret/x#k"), types.StringNull(), true, false},
		{"write-only", types.StringNull(), noRef, types.StringValue("k"), true, false},
		{"unknown write-only", types.StringNull(), noRef, types.StringUnknown(), true, false},
		{"none, required", types.StringNull(), noRef, types.StringNull(), true, true},
		{"none, optional", types.StringNull(), noRef, types.StringNull(), false, false},
		{"plain and reference", types.StringValue("k"), ref("sp", "secret/x#k"), types.StringNull(), true, true},
		{"reference and write-only", types.StringNull(), ref("sp", "secret/x#k"), types.StringValue("k"), true, true},
		{"empty path", types.StringNull(), ref("sp", " "), types.StringNull(), true, true},
		{"empty provider", types.StringNull(), ref("", "secret/x#k"), types.StringNull(), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := types.Int64Null()
			c := credential{&tc.plain, &tc.ref, &tc.wo, &version}
			var diags diag.Diagnostics
			c.validate("scaleway", "secret_key", tc.required, &diags)
			if got := diags.HasError(); got != tc.wantErr {
				t.Fatalf("HasError() = %v, want %v (%v)", got, tc.wantErr, diags)
			}
		})
	}
}
