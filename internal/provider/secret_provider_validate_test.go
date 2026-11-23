// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// The framework only calls ValidateConfig on a resource that announces it, so
// the hook silently never runs if this assertion stops holding.
var _ resource.ResourceWithValidateConfig = &secretProviderResource{}

// A declared key the server filtered keeps its state value on refresh; a
// declared key it sent in clear takes the server's.
func TestSecretProviderRefreshKeepsFiltered(t *testing.T) {
	ctx := context.Background()
	m := secretProviderModel{Fields: types.MapValueMust(types.StringType, map[string]attr.Value{
		"url":   types.StringValue("http://state:8200"),
		"token": types.StringValue("secret"),
	})}
	sp := &client.SecretProvider{Name: "sp", Protocol: "vault-sm", Fields: map[string]client.Field{
		"url":   {Value: "http://server:8200"},
		"token": {Filtered: true},
	}}
	var diags diag.Diagnostics
	(&secretProviderResource{}).refresh(ctx, &m, sp, &diags)
	if diags.HasError() {
		t.Fatalf("refresh: %v", diags)
	}
	fields := map[string]string{}
	m.Fields.ElementsAs(ctx, &fields, false)
	if fields["token"] != "secret" || fields["url"] != "http://server:8200" {
		t.Errorf("fields after refresh %v, want the state token and the server url", fields)
	}
}

func TestCheckFieldForms(t *testing.T) {
	m := func(keys ...string) types.Map {
		elems := map[string]attr.Value{}
		for _, k := range keys {
			elems[k] = types.StringValue("x")
		}
		return types.MapValueMust(types.StringType, elems)
	}
	cases := []struct {
		name  string
		forms []fieldForm
		want  []string
	}{
		{"disjoint", []fieldForm{{"fields", m("url")}, {"fields_wo", m("token")}}, nil},
		{"null form", []fieldForm{{"fields", m("url")}, {"fields_wo", types.MapNull(types.StringType)}}, nil},
		{"unknown form", []fieldForm{{"fields", m("url")}, {"fields_wo", types.MapUnknown(types.StringType)}}, nil},
		{"same key twice", []fieldForm{{"fields", m("url", "token")}, {"fields_wo", m("token")}},
			[]string{`"token" is set in both fields and fields_wo`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := checkFieldForms(tc.forms...)
			if len(diags) != len(tc.want) {
				t.Fatalf("got %d diagnostics, want %d: %v", len(diags), len(tc.want), diags)
			}
			for i, w := range tc.want {
				if !strings.Contains(diags[i].Detail(), w) {
					t.Errorf("diagnostic %d is %q, want it to mention %q", i, diags[i].Detail(), w)
				}
			}
		})
	}
}

func TestCheckRequiredFields(t *testing.T) {
	var protocol client.IntegrationProtocol
	if err := json.Unmarshal([]byte(`{"scheme": "vault-sm", "fields": [
		{"name": "url", "required": true},
		{"name": "token", "required": false, "sensitive": true}]}`), &protocol); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		fields  map[string]client.Field
		wantErr bool
	}{
		{"required key set", map[string]client.Field{"url": {Value: "http://vault:8200"}}, false},
		{"optional key missing", map[string]client.Field{"url": {Value: "http://vault:8200"}}, false},
		{"required key missing", map[string]client.Field{"token": {Value: "t"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			checkRequiredFields(&protocol, tc.fields, &diags)
			if got := diags.HasError(); got != tc.wantErr {
				t.Fatalf("HasError() = %v, want %v (%v)", got, tc.wantErr, diags)
			}
		})
	}
}
