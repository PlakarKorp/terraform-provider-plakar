// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

func configureClient(providerData any, diags interface {
	AddError(summary, detail string)
}) *client.Client {
	if providerData == nil {
		return nil
	}
	c, ok := providerData.(*client.Client)
	if !ok {
		diags.AddError("unexpected provider data", fmt.Sprintf("got %T", providerData))
		return nil
	}
	return c
}

// --- plakar_resource ---------------------------------------------------------

type resourceDataSource struct{ client *client.Client }

type resourceDataModel struct {
	Ref   types.String `tfsdk:"ref"`
	URNID types.String `tfsdk:"urn_id"`
	URN   types.String `tfsdk:"urn"`
	Name  types.String `tfsdk:"name"`
}

func NewResourceDataSource() datasource.DataSource { return &resourceDataSource{} }

func (d *resourceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource"
}

func (d *resourceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An inventory resource, looked up by URN or name.",
		Attributes: map[string]schema.Attribute{
			"ref": schema.StringAttribute{
				Required:    true,
				Description: "URN or name of the resource. Names must be unique to resolve; use the URN to disambiguate.",
			},
			"urn_id": schema.StringAttribute{Computed: true},
			"urn":    schema.StringAttribute{Computed: true},
			"name":   schema.StringAttribute{Computed: true},
		},
	}
}

func (d *resourceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *resourceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config resourceDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := d.client.ResourceRef(config.Ref.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("looking up resource", err.Error())
		return
	}
	config.URNID = types.StringValue(res.URNID)
	config.URN = types.StringValue(res.URN)
	config.Name = types.StringValue(res.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// --- plakar_integration ------------------------------------------------------

type integrationDataSource struct{ client *client.Client }

type integrationDataModel struct {
	Name types.String `tfsdk:"name"`
	ID   types.String `tfsdk:"id"`
}

func NewIntegrationDataSource() datasource.DataSource { return &integrationDataSource{} }

func (d *integrationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_integration"
}

func (d *integrationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An installed integration, looked up by name.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Required: true},
			"id":   schema.StringAttribute{Computed: true},
		},
	}
}

func (d *integrationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *integrationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config integrationDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	integration, err := d.client.IntegrationByName(config.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("looking up integration", err.Error())
		return
	}
	config.ID = types.StringValue(integration.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// --- plakar_store / plakar_connector lookups ---------------------------------

// connectorLookupDataSource serves both read-only lookups: a store by name,
// or a connector by type and name. No fields are exposed — referencing an
// existing object needs its identity, not its credentials.
type connectorLookupDataSource struct {
	client    *client.Client
	kindFixed string // "store" for plakar_store, "" for plakar_connector
}

type connectorLookupModel struct {
	Name        types.String `tfsdk:"name"`
	Type        types.String `tfsdk:"type"`
	ID          types.String `tfsdk:"id"`
	Protocol    types.String `tfsdk:"protocol"`
	Environment types.String `tfsdk:"environment"`
	URNID       types.String `tfsdk:"urn_id"`
}

func NewStoreDataSource() datasource.DataSource {
	return &connectorLookupDataSource{kindFixed: "store"}
}

func NewConnectorDataSource() datasource.DataSource {
	return &connectorLookupDataSource{}
}

func (d *connectorLookupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	if d.kindFixed == "store" {
		resp.TypeName = req.ProviderTypeName + "_store"
	} else {
		resp.TypeName = req.ProviderTypeName + "_connector"
	}
}

func (d *connectorLookupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"name":        schema.StringAttribute{Required: true},
		"id":          schema.StringAttribute{Computed: true},
		"protocol":    schema.StringAttribute{Computed: true},
		"environment": schema.StringAttribute{Computed: true},
		"urn_id":      schema.StringAttribute{Computed: true},
	}
	description := "A store, looked up by name."
	if d.kindFixed == "" {
		attrs["type"] = schema.StringAttribute{
			Required:    true,
			Description: "source or destination.",
		}
		description = "A source or destination connector, looked up by type and name."
	} else {
		attrs["type"] = schema.StringAttribute{Computed: true}
	}
	resp.Schema = schema.Schema{Description: description, Attributes: attrs}
}

func (d *connectorLookupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *connectorLookupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config connectorLookupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	kind := d.kindFixed
	if kind == "" {
		kind = config.Type.ValueString()
	}
	conn, err := d.client.FindConnector(kind, config.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("looking up connector", err.Error())
		return
	}
	if conn == nil {
		resp.Diagnostics.AddError("connector not found",
			fmt.Sprintf("no %s named %q is visible — an empty result can also mean "+
				"the API key's account holds no grant in the organization", kind, config.Name.ValueString()))
		return
	}
	config.ID = types.StringValue(conn.ID)
	config.Type = types.StringValue(conn.Type)
	config.Protocol = types.StringValue(conn.Protocol)
	config.Environment = types.StringValue(conn.Environment)
	if conn.Resource != nil {
		config.URNID = types.StringValue(conn.Resource.URNID)
	} else {
		config.URNID = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
