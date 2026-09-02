// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// connectorRequestFromModel builds a create request from the plan. The caller
// fills URNID and Integration.ID from the resolved lookups.
func connectorRequestFromModel(ctx context.Context, m *storeModel, kind string, diags *diag.Diagnostics) *client.ConnectorRequest {
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
	if !m.DataClasses.IsNull() && !m.DataClasses.IsUnknown() {
		diags.Append(m.DataClasses.ElementsAs(ctx, &req.DataClasses, false)...)
	}
	return req
}

// mergeConnectorRequest builds v1's full-body update: the connector's current
// state, with the plan's managed values written over it. Fields the plan does
// not name keep their server-side values.
func mergeConnectorRequest(ctx context.Context, m *storeModel, current *client.Connector, kind string, diags *diag.Diagnostics) *client.ConnectorRequest {
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
	if !m.Temperature.IsNull() {
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
	return req
}

// refreshModelFromConnector maps a read connector back onto the model. Only
// the field keys the practitioner declared are refreshed — the rest of the
// server-side map is not our contract. Attributes that are null in state stay
// null when the server reports an empty value, so optional attributes do not
// oscillate between null and "".
func refreshModelFromConnector(ctx context.Context, m *storeModel, conn *client.Connector, diags *diag.Diagnostics) {
	m.Name = types.StringValue(conn.Name)
	m.Protocol = types.StringValue(conn.Protocol)
	if conn.Integration.Name != "" {
		m.Integration = types.StringValue(conn.Integration.Name)
	}
	if conn.Resource != nil {
		m.URNID = types.StringValue(conn.Resource.URNID)
		// `resource` is practitioner input (URN or name); after an import the
		// state has nothing better than the URN.
		if m.Resource.IsNull() {
			m.Resource = types.StringValue(conn.Resource.URN)
		}
	}

	if !(m.Environment.IsNull() && conn.Environment == "") {
		m.Environment = stringOrNull(conn.Environment, m.Environment)
	}
	// temperature is Optional+Computed (the server assigns one when the config
	// does not): always adopt the server value.
	if conn.Temperature != "" {
		m.Temperature = types.StringValue(conn.Temperature)
	} else {
		m.Temperature = types.StringNull()
	}

	if !m.DataClasses.IsNull() || len(conn.DataClasses) > 0 {
		dcs := conn.DataClasses
		if dcs == nil {
			dcs = []string{}
		}
		list, d := types.ListValueFrom(ctx, types.StringType, dcs)
		diags.Append(d...)
		m.DataClasses = list
	}

	if m.Fields.IsNull() || m.Fields.IsUnknown() {
		// Import path: adopt the full server-side map.
		all := map[string]string{}
		for k, f := range conn.Fields {
			all[k] = f.Value
		}
		mv, d := types.MapValueFrom(ctx, types.StringType, all)
		diags.Append(d...)
		m.Fields = mv
		return
	}
	declared := map[string]string{}
	diags.Append(m.Fields.ElementsAs(ctx, &declared, false)...)
	for k := range declared {
		if f, ok := conn.Fields[k]; ok {
			declared[k] = f.Value
		}
	}
	mv, d := types.MapValueFrom(ctx, types.StringType, declared)
	diags.Append(d...)
	m.Fields = mv
}

func stringOrNull(server string, prior types.String) types.String {
	if server == "" && prior.IsNull() {
		return prior
	}
	return types.StringValue(server)
}
