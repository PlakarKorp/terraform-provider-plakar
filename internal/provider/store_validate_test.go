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

func secretFieldsMap(t *testing.T, refs map[string][2]string) types.Map {
	t.Helper()
	elems := map[string]attr.Value{}
	for k, r := range refs {
		elems[k] = types.ObjectValueMust(secretFieldAttrTypes, map[string]attr.Value{
			"provider": types.StringValue(r[0]),
			"path":     types.StringValue(r[1]),
		})
	}
	return types.MapValueMust(types.ObjectType{AttrTypes: secretFieldAttrTypes}, elems)
}

// A passphrase resolved from a secret provider satisfies both halves of the
// check: its value only exists on the server, at run time.
func TestCheckConfiguredPassphrase_secretFields(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fields       types.Map
		secretFields types.Map
		wantErr      bool
	}{
		{"passphrase as a reference", fieldsMap(t, map[string]string{"root": "/x"}),
			secretFieldsMap(t, map[string][2]string{"passphrase": {"sp", "secret/x#passphrase"}}), false},
		{"other references only", fieldsMap(t, map[string]string{"root": "/x"}),
			secretFieldsMap(t, map[string][2]string{"access_key": {"sp", "secret/x#ak"}}), true},
		{"unknown references defer to apply", fieldsMap(t, map[string]string{"root": "/x"}),
			types.MapUnknown(types.ObjectType{AttrTypes: secretFieldAttrTypes}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diags := checkConfiguredPassphrase(&storeModel{Fields: tc.fields, SecretFields: tc.secretFields, Initialize: types.BoolValue(true)})
			if got := diags.HasError(); got != tc.wantErr {
				t.Fatalf("HasError() = %v, want %v (%v)", got, tc.wantErr, diags)
			}
			var applied diag.Diagnostics
			plan := storeModel{Fields: tc.fields, SecretFields: tc.secretFields}
			validatePassphrase(context.Background(), &plan, &applied)
			if got := applied.HasError(); got != tc.wantErr {
				t.Fatalf("apply-time HasError() = %v, want %v (%v)", got, tc.wantErr, applied)
			}
		})
	}
}

func TestCheckSecretFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refs    types.Map
		wantErr bool
	}{
		{"complete", secretFieldsMap(t, map[string][2]string{"passphrase": {"sp", "secret/x#passphrase"}}), false},
		{"empty path", secretFieldsMap(t, map[string][2]string{"passphrase": {"sp", ""}}), true},
		{"blank path", secretFieldsMap(t, map[string][2]string{"passphrase": {"sp", "  "}}), true},
		{"empty provider", secretFieldsMap(t, map[string][2]string{"passphrase": {"", "secret/x#passphrase"}}), true},
		{"null", types.MapNull(types.ObjectType{AttrTypes: secretFieldAttrTypes}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkSecretFields(tc.refs).HasError(); got != tc.wantErr {
				t.Fatalf("HasError() = %v, want %v", got, tc.wantErr)
			}
		})
	}
}

// One key in fields and in secret_fields: the two would race for the same
// server-side field, so it is refused at plan time.
func TestCheckFieldForms_secretFields(t *testing.T) {
	diags := checkFieldForms(
		fieldForm{"fields", fieldsMap(t, map[string]string{"passphrase": "x"})},
		fieldForm{"secret_fields", secretFieldsMap(t, map[string][2]string{"passphrase": {"sp", "secret/x#p"}})},
	)
	if !diags.HasError() {
		t.Fatal("a key set in both fields and secret_fields was accepted")
	}
}

// A write-only passphrase satisfies the check, and is held to non-empty like
// a plain one: at plan time when it is known, at apply time in any case.
func TestCheckConfiguredPassphrase_writeOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		writeOnly types.Map
		wantErr   bool
	}{
		{"write-only passphrase", fieldsMap(t, map[string]string{"passphrase": "correcthorsebatterystaple"}), false},
		{"empty write-only passphrase", fieldsMap(t, map[string]string{"passphrase": " "}), true},
		{"write-only, but no passphrase", fieldsMap(t, map[string]string{"region": "fr-par"}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := storeModel{Fields: fieldsMap(t, map[string]string{"root": "/x"}), FieldsWO: tc.writeOnly, Initialize: types.BoolValue(true)}
			if got := checkConfiguredPassphrase(&model).HasError(); got != tc.wantErr {
				t.Fatalf("HasError() = %v, want %v", got, tc.wantErr)
			}
			var applied diag.Diagnostics
			validatePassphrase(context.Background(), &model, &applied)
			if got := applied.HasError(); got != tc.wantErr {
				t.Fatalf("apply-time HasError() = %v, want %v", got, tc.wantErr)
			}
		})
	}
}

// What an update sends of fields_wo: everything on a version bump, otherwise
// only the keys that just moved in from another form.
func TestWriteOnlyFieldsToSend(t *testing.T) {
	ctx := context.Background()
	config := fieldsMap(t, map[string]string{"passphrase": "p", "region": "fr-par"})
	for _, tc := range []struct {
		name  string
		all   bool
		prior []types.Map
		want  []string
	}{
		{"version bumped", true, nil, []string{"passphrase", "region"}},
		{"nothing moved", false, []types.Map{fieldsMap(t, map[string]string{"root": "/x"})}, nil},
		{"region moved from fields", false, []types.Map{fieldsMap(t, map[string]string{"region": "nl-ams"})}, []string{"region"}},
		{"region moved from secret_fields", false, []types.Map{
			types.MapNull(types.StringType),
			secretFieldsMap(t, map[string][2]string{"region": {"sp", "secret/x#region"}}),
		}, []string{"region"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			got := writeOnlyFieldsToSend(ctx, config, tc.all, tc.prior, &diags)
			if len(got) != len(tc.want) {
				t.Fatalf("sends %d keys, want %v", len(got), tc.want)
			}
			for _, k := range tc.want {
				if _, ok := got[k]; !ok {
					t.Errorf("does not send %s", k)
				}
			}
		})
	}
}
