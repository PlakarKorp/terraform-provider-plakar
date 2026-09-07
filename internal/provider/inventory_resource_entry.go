// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"
	"strings"

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

// inventoryResourceEntry manages one resource declared in a self-managed
// inventory: the fleet, as code. Provider-backed inventories discover their
// resources and refuse hand-declared ones.
type inventoryResourceEntry struct {
	client *client.Client
}

type inventoryResourceEntryModel struct {
	ID                   types.String `tfsdk:"id"`
	InventoryID          types.String `tfsdk:"inventory_id"`
	URN                  types.String `tfsdk:"urn"`
	Name                 types.String `tfsdk:"name"`
	Class                types.String `tfsdk:"class"`
	Subclass             types.String `tfsdk:"subclass"`
	Service              types.String `tfsdk:"service"`
	Endpoints            types.List   `tfsdk:"endpoints"`
	Tags                 types.List   `tfsdk:"tags"`
	ExcludedFromCoverage types.Bool   `tfsdk:"excluded_from_coverage"`
	Locked               types.Bool   `tfsdk:"locked"`
}

func NewInventoryResourceEntry() resource.Resource {
	return &inventoryResourceEntry{}
}

func (r *inventoryResourceEntry) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inventory_resource"
}

func (r *inventoryResourceEntry) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A resource declared in a self-managed inventory — a machine, " +
			"database or share Plakar should know about. Connectors attach to it " +
			"by URN, e.g. through the plakar_resource data source.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Id of the resource's URN, the handle connectors attach to.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"inventory_id": schema.StringAttribute{
				Required:    true,
				Description: "Id of the self-managed inventory the resource lives in.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"urn": schema.StringAttribute{
				Required:    true,
				Description: "URN identifying the resource, unique within the inventory.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Human name of the resource.",
			},
			"class": schema.StringAttribute{
				Required:    true,
				Description: "Resource class, e.g. compute, database, storage.",
			},
			"subclass": schema.StringAttribute{
				Optional:    true,
				Description: "Finer class, e.g. vm, postgres.",
			},
			"service": schema.StringAttribute{
				Optional:    true,
				Description: "Service label the resource belongs to.",
			},
			"endpoints": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Addresses of the resource — hostnames or IPs; the kind is derived server-side.",
			},
			"tags": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Free-form tags.",
			},
			"excluded_from_coverage": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Leave the resource out of coverage accounting.",
			},
			"locked": schema.BoolAttribute{
				Computed: true,
				Description: "Whether the resource is locked — no task may use a " +
					"connector configured against it. Placed and lifted outside " +
					"Terraform, by an operator.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *inventoryResourceEntry) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m *inventoryResourceEntryModel) toWire(ctx context.Context, diags interface {
	AddError(summary, detail string)
}) *client.InventoryResource {
	res := &client.InventoryResource{
		URN:                  m.URN.ValueString(),
		Name:                 m.Name.ValueString(),
		Class:                m.Class.ValueString(),
		SubClass:             m.Subclass.ValueString(),
		Service:              m.Service.ValueString(),
		ExcludedFromCoverage: m.ExcludedFromCoverage.ValueBool(),
	}
	if !m.Endpoints.IsNull() && !m.Endpoints.IsUnknown() {
		var endpoints []string
		if d := m.Endpoints.ElementsAs(ctx, &endpoints, false); d.HasError() {
			diags.AddError("reading endpoints", fmt.Sprintf("%v", d.Errors()))
			return nil
		}
		for _, e := range endpoints {
			res.Endpoints = append(res.Endpoints, client.InventoryResourceEndpoint{Endpoint: e})
		}
	}
	if !m.Tags.IsNull() && !m.Tags.IsUnknown() {
		if d := m.Tags.ElementsAs(ctx, &res.Tags, false); d.HasError() {
			diags.AddError("reading tags", fmt.Sprintf("%v", d.Errors()))
			return nil
		}
	}
	return res
}

// refreshFromWire folds the server's view back into the model. Lists the
// config left null stay null when the server holds nothing, rather than
// flipping to empty and reporting phantom drift.
func (m *inventoryResourceEntryModel) refreshFromWire(ctx context.Context, res *client.InventoryResource) []error {
	m.ID = types.StringValue(res.URNID)
	m.URN = types.StringValue(res.URN)
	m.Name = types.StringValue(res.Name)
	m.Class = types.StringValue(res.Class)
	m.Subclass = syncString(m.Subclass, res.SubClass)
	m.Service = syncString(m.Service, res.Service)
	m.ExcludedFromCoverage = types.BoolValue(res.ExcludedFromCoverage)
	m.Locked = types.BoolValue(res.Locked)

	var errs []error
	endpoints := make([]string, 0, len(res.Endpoints))
	for _, e := range res.Endpoints {
		endpoints = append(endpoints, e.Endpoint)
	}
	if len(endpoints) > 0 || !m.Endpoints.IsNull() {
		v, d := types.ListValueFrom(ctx, types.StringType, endpoints)
		if d.HasError() {
			errs = append(errs, fmt.Errorf("endpoints: %v", d.Errors()))
		}
		m.Endpoints = v
	}
	if len(res.Tags) > 0 || !m.Tags.IsNull() {
		v, d := types.ListValueFrom(ctx, types.StringType, res.Tags)
		if d.HasError() {
			errs = append(errs, fmt.Errorf("tags: %v", d.Errors()))
		}
		m.Tags = v
	}
	return errs
}

func (r *inventoryResourceEntry) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan inventoryResourceEntryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	wire := plan.toWire(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateInventoryResource(plan.InventoryID.ValueString(), wire)
	if err != nil {
		resp.Diagnostics.AddError("creating inventory resource", err.Error())
		return
	}

	// The create response does not echo endpoints; read back for the
	// canonical state.
	res, err := r.client.GetInventoryResource(plan.InventoryID.ValueString(), created.URNID)
	if err != nil {
		resp.Diagnostics.AddError("reading inventory resource after create", err.Error())
		return
	}
	for _, e := range plan.refreshFromWire(ctx, res) {
		resp.Diagnostics.AddError("refreshing inventory resource", e.Error())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inventoryResourceEntry) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state inventoryResourceEntryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.GetInventoryResource(state.InventoryID.ValueString(), state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("reading inventory resource", err.Error())
		return
	}

	for _, e := range state.refreshFromWire(ctx, res) {
		resp.Diagnostics.AddError("refreshing inventory resource", e.Error())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *inventoryResourceEntry) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state inventoryResourceEntryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	wire := plan.toWire(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateInventoryResource(state.InventoryID.ValueString(), state.ID.ValueString(), wire); err != nil {
		resp.Diagnostics.AddError("updating inventory resource", err.Error())
		return
	}

	res, err := r.client.GetInventoryResource(state.InventoryID.ValueString(), state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("reading inventory resource after update", err.Error())
		return
	}
	plan.InventoryID = state.InventoryID
	for _, e := range plan.refreshFromWire(ctx, res) {
		resp.Diagnostics.AddError("refreshing inventory resource", e.Error())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inventoryResourceEntry) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inventoryResourceEntryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteInventoryResource(state.InventoryID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("deleting inventory resource", err.Error())
	}
}

// ImportState takes "<inventory_id>/<urn_id>": a resource is only addressable
// through its inventory.
func (r *inventoryResourceEntry) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("unexpected import id",
			fmt.Sprintf("expected <inventory_id>/<urn_id>, got %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("inventory_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
