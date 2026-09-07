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

// memberResource manages one membership: a person or a service account
// belonging to an organization. A membership carries no permission — what a
// member may do is a grant, managed with plakar_grant. There is no membership
// update, so every attribute replaces; removing the member removes the
// membership, not the person's account.
type memberResource struct {
	client *client.Client
}

type memberModel struct {
	ID                types.String `tfsdk:"id"`
	OrganizationID    types.String `tfsdk:"organization_id"`
	Email             types.String `tfsdk:"email"`
	Name              types.String `tfsdk:"name"`
	Service           types.Bool   `tfsdk:"service"`
	Account           types.String `tfsdk:"account"`
	AccountCreated    types.Bool   `tfsdk:"account_created"`
	GeneratedPassword types.String `tfsdk:"generated_password"`
}

func NewMemberResource() resource.Resource {
	return &memberResource{}
}

func (r *memberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (r *memberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A membership — a person or a service account belonging to an " +
			"organization. Adding a person goes through the admin invitation " +
			"path: a brand-new address gets an account with a one-time generated " +
			"password (in generated_password, shown once and kept in state — " +
			"treat state accordingly); an address that already has an account " +
			"simply gains the membership. A membership carries no permission; " +
			"what a member may do is a plakar_grant. Destroying the resource " +
			"removes the membership, not the person's account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "User id of the member.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organization_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Id of the organization. Defaults to the provider's organization.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"email": schema.StringAttribute{
				Optional:    true,
				Description: "Email address of the person. Required for a person; a service account has none.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Optional: true,
				Description: "Display name. Required for a service account, which has " +
					"no address and is identified by it — changing it replaces. For " +
					"a person it is decoration applied only when the account is " +
					"created; an existing account keeps its own name, so it is " +
					"neither refreshed nor sent again — changing it only updates state.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIf(
						func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
							var service types.Bool
							resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("service"), &service)...)
							resp.RequiresReplace = service.ValueBool()
						},
						"Replaces only a service account, whose name is its identity.",
						"Replaces only a service account, whose name is its identity.",
					),
				},
			},
			"service": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Add an application user rather than a person: no email, no interactive login.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"account": schema.StringAttribute{
				Computed:      true,
				Description:   "Login identifier of the membership: <prefix>/<email>, or a bare email at the deployment root.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"account_created": schema.BoolAttribute{
				Computed:      true,
				Description:   "Whether a brand-new account was registered for the address at creation.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"generated_password": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				Description: "The one-time must-change password the server minted for " +
					"a brand-new account, kept from creation; null when the address " +
					"already had one. A service account's is inert — an application " +
					"user has no interactive login.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *memberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig holds the person/service split to one story: a person is
// keyed by email, a service account by name and nothing else.
func (r *memberResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config memberModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	service := config.Service.ValueBool()
	if config.Service.IsUnknown() {
		return
	}
	switch {
	case service && !config.Email.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("email"),
			"a service account has no email",
			"its address is minted server-side — name it instead")
	case service && config.Name.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("name"),
			"a service account needs a name",
			"it has no address, so the name is its identity")
	case !service && config.Email.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("email"),
			"a person needs an email",
			"set email, or service = true with a name for an application user")
	}
}

func (r *memberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan memberModel
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

	invite, err := r.client.InviteMember(orgID,
		plan.Email.ValueString(), plan.Name.ValueString(), plan.Service.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("adding member", err.Error())
		return
	}

	plan.ID = types.StringValue(invite.UserID)
	plan.OrganizationID = types.StringValue(orgID)
	plan.Account = types.StringValue(invite.Account)
	plan.AccountCreated = types.BoolValue(invite.AccountCreated)
	if invite.GeneratedPassword != nil {
		plan.GeneratedPassword = types.StringValue(*invite.GeneratedPassword)
	} else {
		plan.GeneratedPassword = types.StringNull()
	}
	// A service account's address is minted server-side; Accepted.Email is the
	// only place to learn it, but email stays the practitioner's input — a
	// person's key, a service account's null.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *memberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state memberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := r.client.GetMember(state.OrganizationID.ValueString(), state.ID.ValueString())
	if err != nil {
		// A 404 here is the organization itself gone: the membership went
		// with it, and the plan should offer recreation, not wedge.
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("reading member", err.Error())
		return
	}
	if member == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.Service = types.BoolValue(member.IsService)
	state.Account = stringOrNull(member.Account, state.Account)
	// A service account's synthesized address never enters state — email is a
	// person's key and stays null for an application user. A person's email is
	// adopted only on import (null state): the server lowercases addresses, so
	// refreshing an existing value would turn a capital letter in the config
	// into a perpetual replace. The name refreshes only for a service account,
	// where it is the identity; for a person it was creation-time decoration an
	// existing account ignores.
	if member.IsService {
		state.Name = stringOrNull(member.Name, state.Name)
	} else if state.Email.IsNull() && member.Email != "" {
		state.Email = types.StringValue(member.Email)
	}
	// account_created and generated_password are creation-time facts the API
	// never echoes again: state keeps them as they were.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update runs only for a person's name — creation-time decoration the server
// holds no copy of, so there is nothing to send: the plan is adopted into
// state as-is. Every other change replaces.
func (r *memberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *memberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state memberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteMember(state.OrganizationID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("removing member", err.Error())
	}
}

// ImportState takes "<organization_id>/<user_id>": a membership is only
// addressable through its organization.
func (r *memberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("unexpected import id",
			fmt.Sprintf("expected <organization_id>/<user_id>, got %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
