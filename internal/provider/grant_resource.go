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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// grantResource manages one grant: a (subject, role) pair on an organization.
// A member can hold several; this manages exactly the pair the configuration
// names. The role updates in place; the subject is the grant's identity, so
// changing it replaces.
type grantResource struct {
	client *client.Client
}

type grantModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	SubjectID      types.String `tfsdk:"subject_id"`
	Role           types.String `tfsdk:"role"`
}

func NewGrantResource() resource.Resource {
	return &grantResource{}
}

func (r *grantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_grant"
}

func (r *grantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A grant — a role held by a member of an organization. The " +
			"subject must already be a member (see plakar_member); the role " +
			"names come from the server's catalogue, the standard tier being " +
			"owner, administrator, operator and auditor.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				Description: "Id of the grant. A role change replaces the row " +
					"server-side, so the id changes with it.",
			},
			"organization_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Id of the organization the grant lives in. Defaults to the provider's organization.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"subject_id": schema.StringAttribute{
				Required:    true,
				Description: "User id of the member holding the grant, e.g. a plakar_member's id.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"role": schema.StringAttribute{
				Required:    true,
				Description: "Name of the role, as the catalogue spells it, e.g. operator.",
			},
		},
	}
}

func (r *grantResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan marks the id unknown when the role changes: the server updates a
// grant by replacing the row, so the id does not survive. A computed
// attribute otherwise carries its prior state into the plan, and the new id
// coming back would be an inconsistent result.
func (r *grantResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return // creation or destruction: nothing to carry over
	}
	var plan, state grantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Role.Equal(state.Role) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	}
}

func (r *grantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan grantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := plan.OrganizationID.ValueString()
	if orgID == "" {
		org, err := r.client.OrgID()
		if err != nil {
			resp.Diagnostics.AddError("resolving the provider organization", err.Error())
			return
		}
		orgID = org
	}

	created, err := r.client.CreateGrant(orgID, plan.SubjectID.ValueString(), plan.Role.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("creating grant", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	plan.OrganizationID = types.StringValue(orgID)
	plan.SubjectID = types.StringValue(created.Subject.ID)
	plan.Role = types.StringValue(created.Role.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *grantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state grantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	grant, err := r.client.GetGrant(state.OrganizationID.ValueString(), state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("reading grant", err.Error())
		return
	}

	state.SubjectID = types.StringValue(grant.Subject.ID)
	state.Role = types.StringValue(grant.Role.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *grantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state grantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateGrant(state.OrganizationID.ValueString(),
		state.ID.ValueString(), plan.Role.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("updating grant", err.Error())
		return
	}

	// The server replaces the row: the grant lives on under a new id.
	plan.ID = types.StringValue(updated.ID)
	plan.OrganizationID = state.OrganizationID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *grantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state grantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteGrant(state.OrganizationID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("revoking grant", err.Error())
	}
}

// ImportState takes "<organization_id>/<grant_id>": a grant is only
// addressable through its organization.
func (r *grantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("unexpected import id",
			fmt.Sprintf("expected <organization_id>/<grant_id>, got %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
