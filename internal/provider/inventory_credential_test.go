// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// cred is a credential in one form: "plain", "ref", "wo" (version v) or
// "none".
func cred(form, value string, v int64) credential {
	plain, ref, wo, ver := types.StringNull(), types.ObjectNull(secretFieldAttrTypes), types.StringNull(), types.Int64Null()
	switch form {
	case "plain":
		plain = types.StringValue(value)
	case "ref":
		ref = types.ObjectValueMust(secretFieldAttrTypes, map[string]attr.Value{
			"provider": types.StringValue("sp"), "path": types.StringValue(value)})
	case "wo":
		wo, ver = types.StringValue(value), types.Int64Value(v)
	}
	return credential{&plain, &ref, &wo, &ver}
}

// What an inventory update sends for a credential, the configuration in one
// form and the prior state possibly in another: a write-only value goes out
// at creation, on a new version and on a change of form, and otherwise as
// the field the server holds, since the update replaces the whole body.
func TestCredentialWire(t *testing.T) {
	ref := &client.FieldProvider{ID: "sp"}
	server := client.Field{Value: "held"}
	for _, tc := range []struct {
		name    string
		config  credential
		prior   *credential
		current client.Field
		want    client.Field
	}{
		{"plain", cred("plain", "AK", 0), nil, client.Field{}, client.Field{Value: "AK"}},
		{"reference", cred("ref", "kv/x#k", 0), nil, client.Field{}, client.Field{Value: "kv/x#k", Provider: ref}},
		{"write-only at creation", cred("wo", "new", 1), nil, client.Field{}, client.Field{Value: "new"}},
		{"write-only, same version", cred("wo", "new", 1), ptr(cred("wo", "", 1)), server, server},
		{"write-only, new version", cred("wo", "new", 2), ptr(cred("wo", "", 1)), server, client.Field{Value: "new"}},
		{"write-only, was plain", cred("wo", "new", 1), ptr(cred("plain", "old", 0)), server, client.Field{Value: "new"}},
		{"write-only, was a reference", cred("wo", "new", 1), ptr(cred("ref", "kv/x#k", 0)), server, client.Field{Value: "new"}},
		{"write-only, server holds a reference", cred("wo", "new", 1), ptr(cred("wo", "", 1)),
			client.Field{Value: "kv/x#k", Provider: ref}, client.Field{Value: "new"}},
		{"unset", cred("none", "", 0), nil, server, client.Field{}},
	} {
		got := tc.config.wire(tc.prior, tc.current)
		if got.Value != tc.want.Value || (got.Provider == nil) != (tc.want.Provider == nil) ||
			(got.Provider != nil && got.Provider.ID != tc.want.Provider.ID) {
			t.Errorf("%s: sent %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func ptr(c credential) *credential { return &c }

// How the server's field folds back into the form the configuration uses:
// never a plain secret where the configuration did not put one.
func TestCredentialRefresh(t *testing.T) {
	ref := &client.FieldProvider{ID: "sp"}
	for _, tc := range []struct {
		name     string
		form     string
		server   client.Field
		plain    string // "" for null
		refPath  string // "" for null; "-" for an emptied reference
		woIsNull bool
	}{
		{"server reference, configuration plain", "plain", client.Field{Value: "kv/x#k", Provider: ref}, "kv/x#k", "", true},
		{"server reference, configuration reference", "ref", client.Field{Value: "kv/y#k", Provider: ref}, "", "kv/y#k", true},
		{"server reference, configuration write-only", "wo", client.Field{Value: "kv/x#k", Provider: ref}, "", "kv/x#k", true},
		{"server plain, configuration plain", "plain", client.Field{Value: "AK2"}, "AK2", "", true},
		{"server plain, configuration reference", "ref", client.Field{Value: "AK2"}, "", "-", true},
		{"server plain, configuration write-only", "wo", client.Field{Value: "secret"}, "", "", true},
	} {
		c := cred(tc.form, "old", 1)
		c.refresh(tc.server)
		if got := c.Plain.ValueString(); got != tc.plain || c.Plain.IsNull() != (tc.plain == "") {
			t.Errorf("%s: plain %v, want %q", tc.name, *c.Plain, tc.plain)
		}
		switch tc.refPath {
		case "":
			if !c.Ref.IsNull() {
				t.Errorf("%s: reference %v, want null", tc.name, *c.Ref)
			}
		default:
			want := tc.refPath
			if want == "-" {
				want = ""
			}
			p, _ := c.Ref.Attributes()["path"].(types.String)
			if c.Ref.IsNull() || p.ValueString() != want {
				t.Errorf("%s: reference %v, want path %q", tc.name, *c.Ref, want)
			}
		}
		if c.WO.IsNull() != tc.woIsNull {
			t.Errorf("%s: write-only %v", tc.name, *c.WO)
		}
	}
}
