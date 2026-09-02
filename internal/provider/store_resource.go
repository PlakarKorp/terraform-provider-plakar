// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// storeResource manages a Plakar store: a connector of type store, with the
// underlying storage initialized at creation.
type storeResource struct {
	client *client.Client
}

type storeModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Integration types.String `tfsdk:"integration"`
	Protocol    types.String `tfsdk:"protocol"`
	Resource    types.String `tfsdk:"resource"`
	URNID       types.String `tfsdk:"urn_id"`
	Fields      types.Map    `tfsdk:"fields"`
	Environment types.String `tfsdk:"environment"`
	DataClasses types.List   `tfsdk:"data_classes"`
	Temperature types.String `tfsdk:"temperature"`
	Initialize  types.Bool   `tfsdk:"initialize"`
	Compression types.String `tfsdk:"compression"`
}

func NewStoreResource() resource.Resource {
	return &storeResource{}
}

func (r *storeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_store"
}

func (r *storeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Plakar store — where backup data lands. Destroying the " +
			"resource removes the store from Plakar; data in the underlying " +
			"storage is not touched.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the store, unique among stores in the organization.",
			},
			"integration": schema.StringAttribute{
				Required:    true,
				Description: "Name of the installed integration backing the store, e.g. s3.",
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
				Description: "URN or name of the inventory resource the store attaches to.",
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
				Description: "Data classes the store accepts.",
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
			"initialize": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Initialize the underlying storage at creation. Never re-runs on update.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"compression": schema.StringAttribute{
				Optional:    true,
				Description: "Compression for the store at initialization (GZIP, LZ4, ZSTD). Unset keeps the engine's default.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *storeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *storeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan storeModel
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

	creq := connectorRequestFromModel(ctx, &plan, "store", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	creq.URNID = res.URNID
	creq.Integration.ID = integration.ID
	if creq.Protocol == "" {
		// For the standard integrations the protocol carries the same name;
		// spelling it out is only needed when they differ.
		creq.Protocol = integration.Name
	}

	created, err := r.client.CreateConnector(creq)
	if err != nil {
		resp.Diagnostics.AddError("creating store", err.Error())
		return
	}

	if plan.Initialize.ValueBool() {
		if err := r.client.InitializeStore(created.ID, plan.Compression.ValueString()); err != nil {
			resp.Diagnostics.AddError("initializing store",
				fmt.Sprintf("the connector %s was created but its storage failed to initialize: %s", created.ID, err))
			return
		}
	}

	// Read back rather than trusting the plan: the server computes values the
	// config never named (temperature, for one), and state has to carry them
	// or every following plan reports phantom drift.
	plan.ID = types.StringValue(created.ID)
	conn, err := r.client.GetConnector(created.ID)
	if err != nil {
		resp.Diagnostics.AddError("reading store after create", err.Error())
		return
	}
	refreshModelFromConnector(ctx, &plan, conn, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state storeModel
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
		resp.Diagnostics.AddError("reading store", err.Error())
		return
	}

	refreshModelFromConnector(ctx, &state, conn, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *storeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state storeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetConnector(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("reading store before update", err.Error())
		return
	}

	// v1 update is a full-body POST: merge the plan over the connector's
	// current state so attributes we do not manage keep their values.
	creq := mergeConnectorRequest(ctx, &plan, current, "store", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateConnector(state.ID.ValueString(), creq); err != nil {
		resp.Diagnostics.AddError("updating store", err.Error())
		return
	}

	plan.ID = state.ID
	plan.URNID = state.URNID
	if plan.Protocol.IsUnknown() || plan.Protocol.IsNull() {
		plan.Protocol = state.Protocol
	}
	plan.Initialize = state.Initialize
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state storeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteConnector(state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("deleting store", err.Error())
	}
}

func (r *storeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
