// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

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

// integrationResource installs one integration from the catalog and keeps the
// installed version current. Integrations belong to the deployment, not to an
// organization: installing and uninstalling need the platform-admin (or
// superuser) role, and an uninstall takes the integration away from every
// organization.
type integrationResource struct {
	client *client.Client
}

type integrationModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Version types.String `tfsdk:"version"`
}

func NewIntegrationResource() resource.Resource {
	return &integrationResource{}
}

func (r *integrationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_integration"
}

func (r *integrationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An integration installed from the catalog, made the current " +
			"version of its name. Integrations belong to the whole deployment, " +
			"not to an organization: managing them needs an API key whose " +
			"service account holds the platform-admin role, and destroying the " +
			"resource uninstalls the version in state for every organization, " +
			"including one imported rather than installed by Terraform; other " +
			"installed versions of the same integration, such as the one a " +
			"version change left behind, stay installed. The " +
			"server uninstalls an integration a connector still uses without " +
			"complaint, and leaves the connector in " +
			"place; within one configuration, Terraform destroys those first. " +
			"Changing " +
			"version installs that version and makes it current; the previous " +
			"one stays installed, no longer current. With version set, a version " +
			"made current by hand is put back on the next apply; without, it " +
			"is adopted.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Id of the installed version.",
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the integration in the catalog, e.g. scaleway.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"version": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Version to install, e.g. v1.1.13. Pinning it is the " +
					"deterministic choice. Left out, the highest version the catalog " +
					"offers is installed, in semantic-version order: the catalog " +
					"marks no version as stable, so that may be a prerelease. The " +
					"version chosen is kept in state and never upgraded by a later " +
					"plan; set version to move to another one.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *integrationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// install checks the name and version against the catalog, since the install
// route answers either one unknown with a bare 500, then installs.
func (r *integrationResource) install(plan *integrationModel) (string, string, error) {
	name := plan.Name.ValueString()
	versions, err := r.client.CatalogVersions(name)
	if err != nil {
		return "", "", fmt.Errorf("reading the catalog: %w", err)
	}
	if len(versions) == 0 {
		return "", "", fmt.Errorf("the catalog has no integration named %q", name)
	}
	version := plan.Version.ValueString()
	if plan.Version.IsNull() || plan.Version.IsUnknown() {
		if version, err = client.LatestVersion(versions); err != nil {
			return "", "", err
		}
	} else if !slices.Contains(versions, version) {
		return "", "", fmt.Errorf("the catalog has no version %q of %q; it offers %s",
			version, name, strings.Join(versions, ", "))
	}
	id, err := r.client.InstallIntegration(name, version)
	if err != nil {
		return "", "", err
	}
	return id, version, nil
}

func (r *integrationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan integrationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, version, err := r.install(&plan)
	if err != nil {
		resp.Diagnostics.AddError("installing integration", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	plan.Version = types.StringValue(version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *integrationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state integrationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An import knows only the id; a GET by id still answers for an
	// uninstalled version, so it only serves to learn the name.
	if state.Name.IsNull() {
		got, err := r.client.GetInstalledIntegration(state.ID.ValueString())
		if err != nil {
			if client.IsNotFound(err) {
				resp.State.RemoveResource(ctx)
				return
			}
			resp.Diagnostics.AddError("reading integration", err.Error())
			return
		}
		state.Name = types.StringValue(got.Name)
	}

	installed, err := r.client.InstalledIntegrations(state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("listing installed integrations", err.Error())
		return
	}
	// What runs is the current version, whichever it is: another version
	// made current by hand lands in state, so a pinned version plans the
	// update that puts it back. With none current, the integration is
	// uninstalled and the plan installs it again.
	var found *client.InstalledIntegration
	for i := range installed {
		if installed[i].IsCurrent {
			found = &installed[i]
		}
	}
	if found == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(found.ID)
	state.Name = types.StringValue(found.Name)
	state.Version = types.StringValue(found.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update runs only for a version change, older or newer alike: that version
// is installed and made current, under its own id.
func (r *integrationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state integrationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Version.Equal(state.Version) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}
	id, version, err := r.install(&plan)
	if err != nil {
		resp.Diagnostics.AddError("installing integration", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	plan.Version = types.StringValue(version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *integrationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state integrationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.UninstallIntegration(state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("uninstalling integration", err.Error())
	}
}

func (r *integrationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
