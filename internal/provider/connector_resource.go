// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// connectorResource manages a source or destination connector. Stores have
// their own resource, plakar_store, which also initializes the storage.
type connectorResource struct {
	client *client.Client
}

type connectorModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Type        types.String `tfsdk:"type"`
	Integration types.String `tfsdk:"integration"`
	Protocol    types.String `tfsdk:"protocol"`
	Resource    types.String `tfsdk:"resource"`
	URNID       types.String `tfsdk:"urn_id"`
	Fields      types.Map    `tfsdk:"fields"`
	Environment types.String `tfsdk:"environment"`
	DataClasses types.List   `tfsdk:"data_classes"`
	Temperature types.String `tfsdk:"temperature"`
}

func (m *connectorModel) facet() *connectorFacet {
	return &connectorFacet{
		Name: &m.Name, Integration: &m.Integration, Protocol: &m.Protocol,
		Resource: &m.Resource, URNID: &m.URNID, Fields: &m.Fields,
		Environment: &m.Environment, DataClasses: &m.DataClasses,
		Temperature: &m.Temperature,
	}
}

func NewConnectorResource() resource.Resource {
	return &connectorResource{}
}

func (r *connectorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connector"
}

func (r *connectorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Plakar source or destination connector. For stores, use plakar_store.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the connector, unique per connector type in the organization.",
			},
			"type": schema.StringAttribute{
				Required:    true,
				Description: "What the connector is used for: source or destination.",
				Validators: []validator.String{
					stringvalidator.OneOf("source", "destination"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"integration": schema.StringAttribute{
				Required:    true,
				Description: "Name of the installed integration backing the connector, e.g. s3 or sftp.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"protocol": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Protocol spoken to the resource. Defaults to the " +
					"integration name, which matches for the standard integrations.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"resource": schema.StringAttribute{
				Required:    true,
				Description: "URN or name of the inventory resource the connector attaches to.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"urn_id": schema.StringAttribute{
				Computed:      true,
				Description:   "Resolved id of the inventory resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fields": schema.MapAttribute{
				Required:    true,
				ElementType: types.StringType,
				Sensitive:   true,
				Description: "Integration-specific configuration. Only the keys " +
					"declared here are managed; anything else set server-side keeps " +
					"its value.",
			},
			"environment": schema.StringAttribute{
				Optional:    true,
				Description: "Environment label, e.g. production.",
			},
			"data_classes": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Data classes the connector carries.",
			},
			"temperature": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Storage temperature. Computed by the server when " +
					"not set.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *connectorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *connectorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integration, err := r.client.IntegrationByName(plan.Integration.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("resolving integration", err.Error())
		return
	}
	res, err := r.client.ResourceRef(plan.Resource.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("resolving resource", err.Error())
		return
	}

	kind := plan.Type.ValueString()
	creq := connectorRequestFromFacet(ctx, plan.facet(), kind, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	creq.URNID = res.URNID
	creq.Integration.ID = integration.ID
	if creq.Protocol == "" {
		creq.Protocol = integration.Name
	}

	created, err := r.client.CreateConnector(creq)
	if err != nil {
		resp.Diagnostics.AddError("creating connector", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	conn, err := r.client.GetConnector(created.ID)
	if err != nil {
		resp.Diagnostics.AddError("reading connector after create", err.Error())
		return
	}
	refreshFacetFromConnector(ctx, plan.facet(), conn, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state connectorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn, err := r.client.GetConnector(state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("reading connector", err.Error())
		return
	}

	if conn.Type != "" {
		state.Type = types.StringValue(conn.Type)
	}
	refreshFacetFromConnector(ctx, state.facet(), conn, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *connectorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state connectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetConnector(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("reading connector before update", err.Error())
		return
	}

	creq := mergeConnectorRequest(ctx, plan.facet(), current, plan.Type.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateConnector(state.ID.ValueString(), creq); err != nil {
		resp.Diagnostics.AddError("updating connector", err.Error())
		return
	}

	plan.ID = state.ID
	plan.URNID = state.URNID
	if plan.Protocol.IsUnknown() || plan.Protocol.IsNull() {
		plan.Protocol = state.Protocol
	}
	if plan.Temperature.IsUnknown() {
		plan.Temperature = state.Temperature
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state connectorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteConnector(state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("deleting connector", err.Error())
	}
}

func (r *connectorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
