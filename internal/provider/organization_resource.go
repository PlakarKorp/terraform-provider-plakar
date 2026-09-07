// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// organizationResource manages one organization: a tenant, or a perimeter
// nested under one. v1 has no organization update route — the name is the
// key and everything else is set at creation — so every attribute replaces.
type organizationResource struct {
	client *client.Client
}

type organizationModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Type     types.String `tfsdk:"type"`
	ParentID types.String `tfsdk:"parent_id"`
	Info     types.Map    `tfsdk:"info"`
}

func NewOrganizationResource() resource.Resource {
	return &organizationResource{}
}

func (r *organizationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (r *organizationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An organization — a tenant, or a perimeter nested under one. " +
			"Membership is managed with plakar_member, permissions with " +
			"plakar_grant. v1 has no organization update, so every change " +
			"replaces — and destroying the resource deletes the tenant and " +
			"everything scoped to it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the organization.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("enterprise"),
				Description: "Organization type. Only enterprise organizations can " +
					"be created as sub-organizations today.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"parent_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Id of the parent organization. Defaults to the provider's organization.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"info": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Free-form key/value information attached at creation.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *organizationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// refreshFromOrganization folds the server's view back into the model.
func (m *organizationModel) refreshFromOrganization(ctx context.Context, org *client.Organization, resp interface {
	AddError(summary, detail string)
}) {
	m.ID = types.StringValue(org.ID)
	m.Name = types.StringValue(org.Name)
	m.Type = types.StringValue(org.Type)
	if org.ParentID != nil {
		m.ParentID = types.StringValue(*org.ParentID)
	} else {
		m.ParentID = types.StringNull()
	}
	// info refreshes only when the configuration manages it: info carries
	// RequiresReplace, so adopting a server-populated value under a null config
	// would plan the tenant's destruction — a blast radius no drift report is
	// worth.
	if !m.Info.IsNull() {
		info := org.Info
		if info == nil {
			info = map[string]string{}
		}
		v, d := types.MapValueFrom(ctx, types.StringType, info)
		if d.HasError() {
			resp.AddError("refreshing organization info", fmt.Sprintf("%v", d.Errors()))
			return
		}
		m.Info = v
	}
}

func (r *organizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan organizationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parentID := plan.ParentID.ValueString()
	if parentID == "" {
		org, err := r.client.OrgID()
		if err != nil {
			resp.Diagnostics.AddError("resolving the provider organization", err.Error())
			return
		}
		parentID = org
	}

	creq := &client.OrganizationRequest{
		Name:     plan.Name.ValueString(),
		Type:     plan.Type.ValueString(),
		ParentID: parentID,
		Info:     map[string]string{},
	}
	if !plan.Info.IsNull() && !plan.Info.IsUnknown() {
		resp.Diagnostics.Append(plan.Info.ElementsAs(ctx, &creq.Info, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	created, err := r.client.CreateOrganization(creq)
	if err != nil {
		resp.Diagnostics.AddError("creating organization", err.Error())
		return
	}

	plan.refreshFromOrganization(ctx, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *organizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state organizationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	org, err := r.client.GetOrganization(state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("reading organization", err.Error())
		return
	}

	state.refreshFromOrganization(ctx, org, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update never runs: every attribute requires replacement. The framework
// still wants the method.
func (r *organizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("organization update is not supported",
		"v1 has no organization update route; every change replaces")
}

func (r *organizationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state organizationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteOrganization(state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("deleting organization", err.Error())
	}
}

func (r *organizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
