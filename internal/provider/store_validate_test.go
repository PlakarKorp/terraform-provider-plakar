// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The framework only calls ValidateConfig on a resource that announces it, so
// the hook silently never runs if this assertion stops holding.
var _ resource.ResourceWithValidateConfig = &storeResource{}

func fieldsMap(t *testing.T, kv map[string]string) types.Map {
	t.Helper()
	m, diags := types.MapValueFrom(context.Background(), types.StringType, kv)
	if diags.HasError() {
		t.Fatalf("building fields map: %v", diags)
	}
	return m
}

// The plan-time half: what the configuration alone can settle.
func TestCheckConfiguredPassphrase(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fields     types.Map
		initialize types.Bool
		wantErr    bool
	}{
		{"a passphrase is enough", fieldsMap(t, map[string]string{"passphrase": "correcthorsebatterystaple", "root": "/x"}), types.BoolValue(true), false},
		{"no passphrase key", fieldsMap(t, map[string]string{"root": "/x"}), types.BoolValue(true), true},
		{"empty passphrase", fieldsMap(t, map[string]string{"passphrase": ""}), types.BoolValue(true), true},
		{"blank passphrase", fieldsMap(t, map[string]string{"passphrase": "   "}), types.BoolValue(true), true},
		{"no fields at all", types.MapNull(types.StringType), types.BoolValue(true), true},
		// initialize is Optional+Computed with a true default, so an unset one
		// still means the storage gets initialized here.
		{"initialize unset still requires one", fieldsMap(t, map[string]string{"root": "/x"}), types.BoolNull(), true},
		{"initialize = false opts out", fieldsMap(t, map[string]string{"root": "/x"}), types.BoolValue(false), false},
		{"unknown fields defer to apply", types.MapUnknown(types.StringType), types.BoolValue(true), false},
		{"unknown passphrase defers to apply", types.MapValueMust(types.StringType, map[string]attr.Value{
			"passphrase": types.StringUnknown(),
		}), types.BoolValue(true), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diags := checkConfiguredPassphrase(&storeModel{Fields: tc.fields, Initialize: tc.initialize})
			if got := diags.HasError(); got != tc.wantErr {
				t.Fatalf("HasError() = %v, want %v (%v)", got, tc.wantErr, diags)
			}
		})
	}
}

// The apply-time half: a passphrase that was unknown at plan time and resolves
// to nothing is exactly the store this guard exists to refuse.
func TestValidatePassphrase(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fields  map[string]string
		wantErr bool
	}{
		{"resolved to a value", map[string]string{"passphrase": "s3cret"}, false},
		{"resolved to empty", map[string]string{"passphrase": ""}, true},
		{"resolved to blanks", map[string]string{"passphrase": "\t \n"}, true},
		{"never set", map[string]string{"root": "/x"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := storeModel{Fields: fieldsMap(t, tc.fields)}
			var diags diag.Diagnostics
			validatePassphrase(context.Background(), &plan, &diags)
			if got := diags.HasError(); got != tc.wantErr {
				t.Fatalf("HasError() = %v, want %v (%v)", got, tc.wantErr, diags)
			}
		})
	}
}
