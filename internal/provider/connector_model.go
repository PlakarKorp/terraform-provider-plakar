// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// connectorFacet is the connector-shaped slice both the store and connector
// models expose, so the request-building and refresh logic exists once.
type connectorFacet struct {
	Name            *types.String
	Integration     *types.String
	Protocol        *types.String
	Resource        *types.String
	URNID           *types.String
	Fields          *types.Map
	SecretFields    *types.Map
	FieldsWO        *types.Map
	FieldsWOVersion *types.Int64
	Environment     *types.String
	DataClasses     *types.List
	Temperature     *types.String
}

func (m *storeModel) facet() *connectorFacet {
	return &connectorFacet{
		Name: &m.Name, Integration: &m.Integration, Protocol: &m.Protocol,
		Resource: &m.Resource, URNID: &m.URNID, Fields: &m.Fields,
		SecretFields: &m.SecretFields, FieldsWO: &m.FieldsWO,
		FieldsWOVersion: &m.FieldsWOVersion,
		Environment:     &m.Environment, DataClasses: &m.DataClasses,
		Temperature: &m.Temperature,
	}
}

// secretFieldModel is one field resolved from a secret provider at run time:
// where the secret lives, never its value.
type secretFieldModel struct {
	Provider types.String `tfsdk:"provider"`
	Path     types.String `tfsdk:"path"`
}

var secretFieldAttrTypes = map[string]attr.Type{
	"provider": types.StringType,
	"path":     types.StringType,
}

// secretFieldsAttribute is the secret_fields schema the store and connector
// share. Nothing in it is a secret, so it is not marked sensitive: a plan
// shows which path each field points to.
func secretFieldsAttribute() schema.MapNestedAttribute {
	return schema.MapNestedAttribute{
		Optional: true,
		Description: "Fields resolved from a secret provider each time Plakar " +
			"needs them, instead of being stored: Plakar and Terraform state " +
			"only hold the path. Keyed like fields; a key may not appear in both.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"provider": schema.StringAttribute{
					Required:    true,
					Description: "Id of the plakar_secret_provider holding the secret.",
				},
				"path": schema.StringAttribute{
					Required: true,
					Description: "Where the secret lives in the provider. For Vault: " +
						"{secret_engine}/{path}#{field}, e.g. secret/prod/s3#secret_key.",
				},
			},
		},
	}
}

// writeOnlyFieldsAttributes is the fields_wo and fields_wo_version pair the
// store, connector and secret provider share; example names a key that
// belongs there.
func writeOnlyFieldsAttributes(example string) (schema.MapAttribute, schema.Int64Attribute) {
	return schema.MapAttribute{
			Optional:    true,
			WriteOnly:   true,
			Sensitive:   true,
			ElementType: types.StringType,
			Description: "Integration-specific configuration that never lands in " +
				"state, e.g. " + example + ". Requires Terraform 1.11 or later. " +
				"Its values are sent at creation, when fields_wo_version changes, " +
				"and for a key that has just moved here from another form: " +
				"Terraform cannot compare a write-only value with anything, so " +
				"the version is what rotates it. A key may be set in one form " +
				"only. A key removed from fields_wo keeps its server-side value, " +
				"as for fields. An import cannot recover it: after one, keep " +
				"fields_wo in the configuration and bump fields_wo_version to " +
				"send it again.",
		}, schema.Int64Attribute{
			Optional: true,
			Description: "Change this to send fields_wo again, for instance " +
				"after rotating a credential it carries.",
		}
}

// writeOnlyFieldsToSend picks the write-only values a request sends: all of
// them at creation and when fields_wo_version moved, otherwise only those
// whose key was set in another form before, so a field moved into fields_wo
// does not keep its old form server-side. Anything else keeps its
// server-side value, the one sent last.
func writeOnlyFieldsToSend(ctx context.Context, config types.Map, all bool, priorForms []types.Map, diags *diag.Diagnostics) map[string]string {
	send := map[string]string{}
	if config.IsNull() || config.IsUnknown() {
		return send
	}
	values := map[string]string{}
	diags.Append(config.ElementsAs(ctx, &values, false)...)
	if all {
		return values
	}
	for k, v := range values {
		for _, form := range priorForms {
			if form.IsNull() || form.IsUnknown() {
				continue
			}
			if _, ok := form.Elements()[k]; ok {
				send[k] = v
			}
		}
	}
	return send
}

// checkSecretFields refuses a reference with an empty path or provider: the
// server would take it, and fail only when a task needs the secret.
func checkSecretFields(m types.Map) diag.Diagnostics {
	var diags diag.Diagnostics
	if m.IsNull() || m.IsUnknown() {
		return diags
	}
	for k, v := range m.Elements() {
		obj, ok := v.(types.Object)
		if !ok || obj.IsNull() || obj.IsUnknown() {
			continue
		}
		for _, name := range []string{"provider", "path"} {
			s, ok := obj.Attributes()[name].(types.String)
			if ok && !s.IsUnknown() && !s.IsNull() && strings.TrimSpace(s.ValueString()) == "" {
				diags.AddAttributeError(path.Root("secret_fields").AtMapKey(k).AtName(name),
					"empty secret field "+name,
					fmt.Sprintf("secret_fields.%s needs a non-empty %s.", k, name))
			}
		}
	}
	return diags
}

// writeSecretFields adds the declared references to a request's field map.
func writeSecretFields(ctx context.Context, into map[string]client.Field, m types.Map, diags *diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return
	}
	refs := map[string]secretFieldModel{}
	diags.Append(m.ElementsAs(ctx, &refs, false)...)
	for k, ref := range refs {
		into[k] = client.Field{
			Value:    ref.Path.ValueString(),
			Provider: &client.FieldProvider{ID: ref.Provider.ValueString()},
		}
	}
}

// connectorRequestFromFacet builds a create request from the plan and the
// write-only values, which only the configuration carries. The caller fills
// URNID and Integration.ID from the resolved lookups.
func connectorRequestFromFacet(ctx context.Context, m *connectorFacet, kind string, writeOnly map[string]string, diags *diag.Diagnostics) *client.ConnectorRequest {
	req := &client.ConnectorRequest{
		Name:        m.Name.ValueString(),
		Type:        kind,
		Protocol:    m.Protocol.ValueString(),
		Environment: m.Environment.ValueString(),
		Temperature: m.Temperature.ValueString(),
		Fields:      map[string]client.Field{},
		DataClasses: []string{},
	}
	fields := map[string]string{}
	diags.Append(m.Fields.ElementsAs(ctx, &fields, false)...)
	for k, v := range fields {
		req.Fields[k] = client.Field{Value: v}
	}
	writeSecretFields(ctx, req.Fields, *m.SecretFields, diags)
	for k, v := range writeOnly {
		req.Fields[k] = client.Field{Value: v}
	}
	if !m.DataClasses.IsNull() && !m.DataClasses.IsUnknown() {
		diags.Append(m.DataClasses.ElementsAs(ctx, &req.DataClasses, false)...)
	}
	return req
}

// mergeConnectorRequest builds v1's full-body update: the connector's current
// state, with the plan's managed values and the write-only values to send
// written over it. Fields the plan does not name keep their server-side
// values.
func mergeConnectorRequest(ctx context.Context, m *connectorFacet, current *client.Connector, kind string, writeOnly map[string]string, diags *diag.Diagnostics) *client.ConnectorRequest {
	req := &client.ConnectorRequest{
		Name:        m.Name.ValueString(),
		Type:        kind,
		Protocol:    current.Protocol,
		Environment: current.Environment,
		Temperature: current.Temperature,
		Fields:      map[string]client.Field{},
		Endpoints:   current.Endpoints,
		DataClasses: current.DataClasses,
	}
	req.Integration.ID = current.Integration.ID
	if current.Resource != nil {
		req.URNID = current.Resource.URNID
	}
	if req.DataClasses == nil {
		req.DataClasses = []string{}
	}
	for k, v := range current.Fields {
		req.Fields[k] = v
	}

	if !m.Protocol.IsNull() && !m.Protocol.IsUnknown() {
		req.Protocol = m.Protocol.ValueString()
	}
	if !m.Environment.IsNull() {
		req.Environment = m.Environment.ValueString()
	}
	if !m.Temperature.IsNull() && !m.Temperature.IsUnknown() {
		req.Temperature = m.Temperature.ValueString()
	}
	if !m.DataClasses.IsNull() && !m.DataClasses.IsUnknown() {
		dcs := []string{}
		diags.Append(m.DataClasses.ElementsAs(ctx, &dcs, false)...)
		req.DataClasses = dcs
	}
	fields := map[string]string{}
	diags.Append(m.Fields.ElementsAs(ctx, &fields, false)...)
	for k, v := range fields {
		req.Fields[k] = client.Field{Value: v}
	}
	writeSecretFields(ctx, req.Fields, *m.SecretFields, diags)
	for k, v := range writeOnly {
		req.Fields[k] = client.Field{Value: v}
	}
	return req
}

// refreshFacetFromConnector maps a read connector back onto the model. Only
// the field keys the practitioner declared are refreshed — the rest of the
// server-side map is not our contract. Attributes that are null in state stay
// null when the server reports an empty value, so optional attributes do not
// oscillate between null and "".
//
// sensitive is only read on the import path, and comes from
// sensitiveFieldNames.
func refreshFacetFromConnector(ctx context.Context, m *connectorFacet, conn *client.Connector, sensitive map[string]bool, diags *diag.Diagnostics) {
	*m.Name = types.StringValue(conn.Name)
	// Write-only values are never read back: the server holds them in clear.
	*m.FieldsWO = types.MapNull(types.StringType)
	*m.Protocol = types.StringValue(conn.Protocol)
	if conn.Integration.Name != "" {
		*m.Integration = types.StringValue(conn.Integration.Name)
	}
	if conn.Resource != nil {
		*m.URNID = types.StringValue(conn.Resource.URNID)
		// `resource` is practitioner input (URN or name); after an import the
		// state has nothing better than the URN.
		if m.Resource.IsNull() {
			*m.Resource = types.StringValue(conn.Resource.URN)
		}
	}

	if !(m.Environment.IsNull() && conn.Environment == "") {
		*m.Environment = stringOrNull(conn.Environment, *m.Environment)
	}
	// temperature is Optional+Computed (the server assigns one when the config
	// does not): always adopt the server value.
	if conn.Temperature != "" {
		*m.Temperature = types.StringValue(conn.Temperature)
	} else {
		*m.Temperature = types.StringNull()
	}

	if !m.DataClasses.IsNull() || len(conn.DataClasses) > 0 {
		dcs := conn.DataClasses
		if dcs == nil {
			dcs = []string{}
		}
		list, d := types.ListValueFrom(ctx, types.StringType, dcs)
		diags.Append(d...)
		*m.DataClasses = list
	}

	if m.Fields.IsNull() || m.Fields.IsUnknown() {
		// Import path: adopt the server-side map, each field in the form the
		// server holds it. A reference adopted as a plain value would go back
		// out without its provider on the next update, and v1 would silently
		// turn the path into the field's value. A plain value is adopted only
		// when the integration describes its key as not sensitive: one it
		// marks sensitive, or does not describe at all, may be a secret, or
		// one the configuration passes write-only, so it stays server-side,
		// kept by the update's merge.
		all := map[string]string{}
		refs := map[string]secretFieldModel{}
		for k, f := range conn.Fields {
			if f.Provider != nil {
				refs[k] = secretFieldModel{Provider: types.StringValue(f.Provider.ID), Path: types.StringValue(f.Value)}
			} else if s, known := sensitive[k]; known && !s {
				all[k] = f.Value
			}
		}
		mv, d := types.MapValueFrom(ctx, types.StringType, all)
		diags.Append(d...)
		*m.Fields = mv
		*m.SecretFields = types.MapNull(types.ObjectType{AttrTypes: secretFieldAttrTypes})
		if len(refs) > 0 {
			rv, d := types.MapValueFrom(ctx, types.ObjectType{AttrTypes: secretFieldAttrTypes}, refs)
			diags.Append(d...)
			*m.SecretFields = rv
		}
		return
	}
	// A declared plain field the server holds as a reference refreshes to its
	// path, which differs from the configured value, so the plan shows the
	// field instead of the next update erasing the reference unseen.
	declared := map[string]string{}
	diags.Append(m.Fields.ElementsAs(ctx, &declared, false)...)
	for k := range declared {
		if f, ok := conn.Fields[k]; ok {
			declared[k] = f.Value
		}
	}
	mv, d := types.MapValueFrom(ctx, types.StringType, declared)
	diags.Append(d...)
	*m.Fields = mv

	if m.SecretFields.IsNull() || m.SecretFields.IsUnknown() {
		return
	}
	// A declared reference the server holds as a plain value refreshes to an
	// empty one rather than to that value, which is the secret itself; one the
	// server no longer has drops out. Either way the plan puts it back.
	refs := map[string]secretFieldModel{}
	diags.Append(m.SecretFields.ElementsAs(ctx, &refs, false)...)
	for k := range refs {
		f, ok := conn.Fields[k]
		switch {
		case !ok:
			delete(refs, k)
		case f.Provider == nil:
			refs[k] = secretFieldModel{Provider: types.StringValue(""), Path: types.StringValue("")}
		default:
			refs[k] = secretFieldModel{Provider: types.StringValue(f.Provider.ID), Path: types.StringValue(f.Value)}
		}
	}
	rv, d := types.MapValueFrom(ctx, types.ObjectType{AttrTypes: secretFieldAttrTypes}, refs)
	diags.Append(d...)
	*m.SecretFields = rv
}

// sensitiveFieldNames maps each key the connector's integration declares for
// its kind of use to whether it is sensitive. A key missing from the map is
// one the integration does not describe.
func sensitiveFieldNames(c *client.Client, conn *client.Connector) (map[string]bool, error) {
	protocols, err := c.IntegrationProtocols(conn.Integration.ID, conn.Type)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, p := range protocols {
		if p.Scheme != conn.Protocol {
			continue
		}
		for _, f := range p.Fields {
			names[f.Name] = f.Sensitive
		}
	}
	return names, nil
}

func stringOrNull(server string, prior types.String) types.String {
	if server == "" && prior.IsNull() {
		return prior
	}
	return types.StringValue(server)
}
