// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"
	"regexp"

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

// classificationResource manages one name in an organization's environments
// or data classes. The two lists behave alike in v1, so one implementation
// serves both, told apart by kind. A name is its own identity: there is no
// update, renaming replaces.
type classificationResource struct {
	client *client.Client
	kind   client.Classification
	// typeName and noun drive the type name and the user-facing wording.
	typeName string
	noun     string
	defaults string
}

type classificationModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	Name           types.String `tfsdk:"name"`
}

func NewEnvironmentResource() resource.Resource {
	return &classificationResource{
		kind:     client.Environments,
		typeName: "_environment",
		noun:     "environment",
		defaults: "integration, production, staging and testing",
	}
}

func NewDataClassResource() resource.Resource {
	return &classificationResource{
		kind:     client.DataClasses,
		typeName: "_data_class",
		noun:     "data class",
		defaults: "personal_data, sensitive_personal_data, health_records, cardholder_data, " +
			"financial_records, employee_records, audit_logs, intellectual_property, " +
			"business_data and system_state",
	}
}

func (r *classificationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + r.typeName
}

func (r *classificationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: fmt.Sprintf("An organization %[1]s, one name in the list the "+
			"Classification page shows. New organizations come with %[2]s; "+
			"those can be deleted like any other, so to manage one, import it "+
			"first: creating a name that already exists is an error, because "+
			"the server would answer as if it had created it and a later destroy "+
			"would delete a value Terraform never made. Names are kept exactly as "+
			"written, case and spaces included, so Prod and prod are two %[1]ss. "+
			"There is no update: changing the name replaces. Destroying the "+
			"resource removes the name for the whole organization. An import "+
			"takes the name, in the provider's organization, or "+
			"<organization_id>/<name> for another one: importing by the name alone "+
			"a %[1]s whose configuration sets organization_id to another "+
			"organization plans a replacement, which would delete the name from the "+
			"provider's organization.", r.noun, r.defaults),
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   fmt.Sprintf("The %s name.", r.noun),
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
			"name": schema.StringAttribute{
				Required: true,
				// The server accepts an empty name but offers no route to
				// delete it, so it is refused here.
				Description: fmt.Sprintf("Name of the %s. Changing it replaces.", r.noun),
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *classificationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// orgID is the configured organization, else the provider's.
func (r *classificationResource) orgID(configured types.String) (string, error) {
	if id := configured.ValueString(); id != "" {
		return id, nil
	}
	return r.client.OrgID()
}

func (r *classificationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan classificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID, err := r.orgID(plan.OrganizationID)
	if err != nil {
		resp.Diagnostics.AddError("resolving the provider organization", err.Error())
		return
	}
	name := plan.Name.ValueString()

	// The create route is an upsert: it never reports that the name was
	// already there, so the check has to be made first.
	exists, err := r.client.HasClassification(orgID, r.kind, name)
	if err != nil {
		resp.Diagnostics.AddError("listing "+r.noun+"s", err.Error())
		return
	}
	if exists {
		resp.Diagnostics.AddAttributeError(path.Root("name"),
			r.noun+" already exists",
			fmt.Sprintf("organization %s already has the %s %q; import it "+
				"(terraform import <address> %q) to manage it", orgID, r.noun, name, name))
		return
	}

	if err := r.client.CreateClassification(orgID, r.kind, name); err != nil {
		resp.Diagnostics.AddError("creating "+r.noun, err.Error())
		return
	}

	plan.ID = types.StringValue(name)
	plan.OrganizationID = types.StringValue(orgID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *classificationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state classificationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An import knows only the name; the organization is then the provider's.
	orgID, err := r.orgID(state.OrganizationID)
	if err != nil {
		resp.Diagnostics.AddError("resolving the provider organization", err.Error())
		return
	}
	exists, err := r.client.HasClassification(orgID, r.kind, state.Name.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("listing "+r.noun+"s", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = state.Name
	state.OrganizationID = types.StringValue(orgID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update never runs: every attribute replaces.
func (r *classificationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan classificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *classificationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state classificationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteClassification(state.OrganizationID.ValueString(), r.kind, state.Name.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("deleting "+r.noun, err.Error())
	}
}

// importOrganization is an organization id ahead of a classification name in
// an import id: "<organization_id>/<name>".
var importOrganization = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})/(.+)$`)

// ImportState takes the name, in the provider's organization, or
// "<organization_id>/<name>" for another one. A name may hold a slash, so the
// prefix is only read as an organization when it is shaped like an id.
func (r *classificationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	name := req.ID
	if m := importOrganization.FindStringSubmatch(req.ID); m != nil {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), m[1])...)
		name = m[2]
	}
	if name == "" {
		resp.Diagnostics.AddError("unexpected import id", "expected the "+r.noun+" name, or <organization_id>/<name>")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), name)...)
}
