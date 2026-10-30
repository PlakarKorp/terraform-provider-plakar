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
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Type            types.String `tfsdk:"type"`
	Integration     types.String `tfsdk:"integration"`
	Protocol        types.String `tfsdk:"protocol"`
	Resource        types.String `tfsdk:"resource"`
	URNID           types.String `tfsdk:"urn_id"`
	Fields          types.Map    `tfsdk:"fields"`
	SecretFields    types.Map    `tfsdk:"secret_fields"`
	FieldsWO        types.Map    `tfsdk:"fields_wo"`
	FieldsWOVersion types.Int64  `tfsdk:"fields_wo_version"`
	Environment     types.String `tfsdk:"environment"`
	DataClasses     types.List   `tfsdk:"data_classes"`
	Temperature     types.String `tfsdk:"temperature"`
}

func (m *connectorModel) facet() *connectorFacet {
	return &connectorFacet{
		Name: &m.Name, Integration: &m.Integration, Protocol: &m.Protocol,
		Resource: &m.Resource, URNID: &m.URNID, Fields: &m.Fields,
		SecretFields: &m.SecretFields, FieldsWO: &m.FieldsWO,
		FieldsWOVersion: &m.FieldsWOVersion,
		Environment:     &m.Environment, DataClasses: &m.DataClasses,
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
	fieldsWO, fieldsWOVersion := writeOnlyFieldsAttributes("secret_access_key")
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
				Optional: true,
				Description: "URN or name of the inventory resource the connector attaches to; " +
					"required to create one. Terraform sees no link between this string and " +
					"the inventory: for a resource that a plakar_inventory with sync = true " +
					"discovers in the same apply, add depends_on = [plakar_inventory.<name>], " +
					"or the connector may be created before the sync and not find its " +
					"resource. A connector whose resource was deleted afterwards, by hand or " +
					"by a sync after it disappeared from the cloud, is left without one, and " +
					"the Control Plane refuses any change to it but a new resource: a " +
					"refresh clears resource in state, an import leaves it unset. With " +
					"resource unset in the configuration too, the connector plans no " +
					"change and can be destroyed; with resource set, the connector is " +
					"attached to that resource in place, e.g. once it reappeared under the " +
					"same URN. On a connector that has its resource, changing resource " +
					"replaces the connector, and unsetting it is refused.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIf(
						func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
							// A connector without a resource takes one in place.
							resp.RequiresReplace = !req.StateValue.IsNull()
						},
						"Changing the resource of a connector that has one replaces it.",
						"Changing the resource of a connector that has one replaces it.",
					),
				},
			},
			"urn_id": schema.StringAttribute{
				Computed:      true,
				Description:   "Resolved id of the inventory resource; null once it no longer exists.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fields": schema.MapAttribute{
				Required:    true,
				ElementType: types.StringType,
				Sensitive:   true,
				Description: "Integration-specific configuration. Only the keys " +
					"declared here are managed; anything else set server-side keeps " +
					"its value. An import adopts only the keys the integration does " +
					"not mark sensitive; declare the others again, in any form.",
			},
			"secret_fields":     secretFieldsAttribute(),
			"fields_wo":         fieldsWO,
			"fields_wo_version": fieldsWOVersion,
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

// ModifyPlan holds a connector to what the Control Plane accepts: every
// connector write carries an inventory resource (urn_id), so a connector is
// created with one, keeps one, and, once its resource is gone, takes no
// change but a new resource, which attaches it in place.
func (r *connectorResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroy
	}
	var plan connectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.State.Raw.IsNull() {
		if plan.Resource.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("resource"), "missing resource",
				"a connector is created on an inventory resource: set resource to its URN or name")
		}
		return
	}
	var state connectorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || plan.Resource.IsUnknown() {
		return
	}
	orphan := state.URNID.IsNull()
	switch {
	case !state.Resource.IsNull() && plan.Resource.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("resource"), "resource cannot be unset",
			"the Control Plane keeps a connector on an inventory resource: set resource to "+
				"another one, which replaces the connector, or remove the connector")
	case orphan && plan.Resource.IsNull() && !req.Plan.Raw.Equal(req.State.Raw):
		resp.Diagnostics.AddAttributeError(path.Root("resource"), "connector without inventory resource",
			fmt.Sprintf("the inventory resource of connector %q no longer exists, and the Control "+
				"Plane refuses any change to a connector without one: set resource to a "+
				"resource that exists, which attaches the connector to it, or remove the "+
				"connector", state.Name.ValueString()))
	case orphan && !plan.Resource.IsNull():
		// The update resolves the resource and attaches the connector to it.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("urn_id"), types.StringUnknown())...)
	}
}

// ValidateConfig refuses a field set both as a value and as a reference, and a
// reference with nothing to resolve.
func (r *connectorResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config connectorModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(checkFieldForms(
		fieldForm{"fields", config.Fields},
		fieldForm{"secret_fields", config.SecretFields},
		fieldForm{"fields_wo", config.FieldsWO},
	)...)
	resp.Diagnostics.Append(checkSecretFields(config.SecretFields)...)
}

func (r *connectorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	// Write-only values are never in the plan, only in the configuration.
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("fields_wo"), &plan.FieldsWO)...)
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
	writeOnly := writeOnlyFieldsToSend(ctx, plan.FieldsWO, true, nil, &resp.Diagnostics)
	creq := connectorRequestFromFacet(ctx, plan.facet(), kind, writeOnly, &resp.Diagnostics)
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

	// The connector now exists: record its id before the read-back, so a
	// transient failure there cannot orphan it and duplicate it on the next
	// apply. The other computed attributes must be known too for the state
	// to be savable.
	plan.ID = types.StringValue(created.ID)
	plan.FieldsWO = types.MapNull(types.StringType)
	plan.URNID = types.StringValue(res.URNID)
	plan.Protocol = types.StringValue(creq.Protocol)
	if plan.Temperature.IsUnknown() {
		plan.Temperature = stringOrNull(created.Temperature, types.StringNull())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn, err := r.client.GetConnector(created.ID)
	if err != nil {
		resp.Diagnostics.AddError("reading connector after create", err.Error())
		return
	}
	refreshFacetFromConnector(ctx, plan.facet(), conn, nil, &resp.Diagnostics)
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
	var sensitive map[string]bool
	if state.Fields.IsNull() {
		if sensitive, err = sensitiveFieldNames(r.client, conn); err != nil {
			resp.Diagnostics.AddError("reading the integration's fields", err.Error())
			return
		}
	}
	refreshFacetFromConnector(ctx, state.facet(), conn, sensitive, &resp.Diagnostics)
	if conn.Resource == nil {
		// Its resource was deleted, or pruned by a sync: the connector now
		// takes no change but a new resource. Cleared in state, a resource
		// still in the configuration plans the update that attaches the
		// connector to it again (see ModifyPlan and Update).
		if !state.Resource.IsNull() {
			resp.Diagnostics.AddWarning("connector without inventory resource",
				fmt.Sprintf("the inventory resource %q of connector %q no longer exists; the "+
					"plan attaches the connector to the resource its configuration names, "+
					"which has to exist when it applies", state.Resource.ValueString(), conn.Name))
		}
		state.URNID = types.StringNull()
		state.Resource = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *connectorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state connectorModel
	var configWO types.Map
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("fields_wo"), &configWO)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetConnector(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("reading connector before update", err.Error())
		return
	}

	writeOnly := writeOnlyFieldsToSend(ctx, configWO, !plan.FieldsWOVersion.Equal(state.FieldsWOVersion),
		[]types.Map{state.Fields, state.SecretFields}, &resp.Diagnostics)
	creq := mergeConnectorRequest(ctx, plan.facet(), current, plan.Type.ValueString(), writeOnly, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	urnID := state.URNID
	switch {
	case current.Resource != nil:
		// The connector keeps its urn_id server-side, and a resource
		// recreated under the same URN gets the same one back: the
		// connector may be attached again before Terraform says anything.
		urnID = types.StringValue(current.Resource.URNID)
	case !plan.Resource.IsNull():
		// A connector without its resource takes the one the plan names:
		// the Control Plane accepts an update that carries a resource that
		// exists.
		res, err := r.client.ResourceRef(plan.Resource.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("resolving resource", err.Error())
			return
		}
		creq.URNID = res.URNID
		urnID = types.StringValue(res.URNID)
	}
	if err := r.client.UpdateConnector(state.ID.ValueString(), creq); err != nil {
		resp.Diagnostics.AddError("updating connector", err.Error())
		return
	}

	plan.ID = state.ID
	plan.URNID = urnID
	if plan.Protocol.IsUnknown() || plan.Protocol.IsNull() {
		plan.Protocol = state.Protocol
	}
	if plan.Temperature.IsUnknown() {
		plan.Temperature = state.Temperature
	}
	plan.FieldsWO = types.MapNull(types.StringType)
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
