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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// inventoryResource manages one inventory: the census of resources Plakar
// watches over, either discovered from a provider account (aws, ovh,
// scaleway, gcp, vmware, k8s) or declared by hand (self-managed).
type inventoryResource struct {
	client *client.Client
}

type inventoryAWSModel struct {
	CredentialsType types.String `tfsdk:"credentials_type"`
	AccessKey       types.String `tfsdk:"access_key"`
	SecretAccessKey types.String `tfsdk:"secret_access_key"`
	Region          types.String `tfsdk:"region"`
}

type inventoryOVHModel struct {
	ApplicationKey    types.String `tfsdk:"application_key"`
	ApplicationSecret types.String `tfsdk:"application_secret"`
	ConsumerKey       types.String `tfsdk:"consumer_key"`
	Endpoint          types.String `tfsdk:"endpoint"`
}

type inventoryScalewayModel struct {
	ProjectID types.String `tfsdk:"project_id"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
}

type inventoryGCPModel struct {
	ProjectID          types.String `tfsdk:"project_id"`
	ServiceAccountJSON types.String `tfsdk:"service_account_json"`
}

type inventoryVMWareModel struct {
	Server        types.String `tfsdk:"server"`
	Username      types.String `tfsdk:"username"`
	Password      types.String `tfsdk:"password"`
	TLSSkipVerify types.Bool   `tfsdk:"tls_skip_verify"`
	TLSCABundle   types.String `tfsdk:"tls_ca_bundle"`
}

type inventoryK8SModel struct {
	Kubeconfig types.String `tfsdk:"kubeconfig"`
}

type inventoryModel struct {
	ID       types.String            `tfsdk:"id"`
	Name     types.String            `tfsdk:"name"`
	Type     types.String            `tfsdk:"type"`
	AWS      *inventoryAWSModel      `tfsdk:"aws"`
	OVH      *inventoryOVHModel      `tfsdk:"ovh"`
	Scaleway *inventoryScalewayModel `tfsdk:"scaleway"`
	GCP      *inventoryGCPModel      `tfsdk:"gcp"`
	VMWare   *inventoryVMWareModel   `tfsdk:"vmware"`
	K8S      *inventoryK8SModel      `tfsdk:"k8s"`
}

func NewInventoryResource() resource.Resource {
	return &inventoryResource{}
}

func (r *inventoryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inventory"
}

func (r *inventoryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An inventory — the census of resources Plakar watches over. " +
			"Provider-backed inventories (aws, ovh, scaleway, gcp, vmware, k8s) " +
			"discover their resources from the account they are configured " +
			"against; a self-managed inventory holds resources declared by hand, " +
			"e.g. with plakar_inventory_resource.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the inventory.",
			},
			"type": schema.StringAttribute{
				Required: true,
				Description: "What backs the inventory: aws, ovh, scaleway, gcp, " +
					"vmware, k8s or self-managed. Must match the configuration " +
					"block, one of which is required for every type but self-managed.",
				Validators: []validator.String{
					stringvalidator.OneOf("aws", "ovh", "scaleway", "gcp", "vmware", "k8s", "self-managed"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
		Blocks: map[string]schema.Block{
			"aws": schema.SingleNestedBlock{
				Description: "Configuration of an aws inventory.",
				Attributes: map[string]schema.Attribute{
					"credentials_type": schema.StringAttribute{
						Optional:    true,
						Description: "How to authenticate: iam or access_key.",
						Validators: []validator.String{
							stringvalidator.OneOf("iam", "access_key"),
						},
					},
					"access_key": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Access key id, when credentials_type is access_key.",
					},
					"secret_access_key": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Secret access key, when credentials_type is access_key.",
					},
					"region": schema.StringAttribute{
						Optional:    true,
						Description: "AWS region the discovery runs against.",
					},
				},
			},
			"ovh": schema.SingleNestedBlock{
				Description: "Configuration of an ovh inventory.",
				Attributes: map[string]schema.Attribute{
					"application_key":    schema.StringAttribute{Optional: true, Sensitive: true},
					"application_secret": schema.StringAttribute{Optional: true, Sensitive: true},
					"consumer_key":       schema.StringAttribute{Optional: true, Sensitive: true},
					"endpoint": schema.StringAttribute{
						Optional:    true,
						Description: "OVH API endpoint, e.g. ovh-eu.",
					},
				},
			},
			"scaleway": schema.SingleNestedBlock{
				Description: "Configuration of a scaleway inventory.",
				Attributes: map[string]schema.Attribute{
					"project_id": schema.StringAttribute{Optional: true},
					"access_key": schema.StringAttribute{Optional: true, Sensitive: true},
					"secret_key": schema.StringAttribute{Optional: true, Sensitive: true},
				},
			},
			"gcp": schema.SingleNestedBlock{
				Description: "Configuration of a gcp inventory.",
				Attributes: map[string]schema.Attribute{
					"project_id": schema.StringAttribute{Optional: true},
					"service_account_json": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Service account key, as JSON. Unset falls back to ambient credentials.",
					},
				},
			},
			"vmware": schema.SingleNestedBlock{
				Description: "Configuration of a vmware inventory.",
				Attributes: map[string]schema.Attribute{
					"server":   schema.StringAttribute{Optional: true},
					"username": schema.StringAttribute{Optional: true},
					"password": schema.StringAttribute{Optional: true, Sensitive: true},
					"tls_skip_verify": schema.BoolAttribute{
						Optional:    true,
						Computed:    true,
						Default:     booldefault.StaticBool(false),
						Description: "Skip TLS verification of the vSphere server.",
					},
					"tls_ca_bundle": schema.StringAttribute{
						Optional:    true,
						Description: "CA bundle to verify the vSphere server against, PEM.",
					},
				},
			},
			"k8s": schema.SingleNestedBlock{
				Description: "Configuration of a k8s inventory.",
				Attributes: map[string]schema.Attribute{
					"kubeconfig": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Kubeconfig granting access to the cluster. Unset falls back to in-cluster credentials.",
					},
				},
			},
		},
	}
}

func (r *inventoryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig holds the type attribute and the configuration blocks to one
// story: exactly the block the type names, none for self-managed.
func (r *inventoryResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config inventoryModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	blocks := map[string]bool{
		"aws":      config.AWS != nil,
		"ovh":      config.OVH != nil,
		"scaleway": config.Scaleway != nil,
		"gcp":      config.GCP != nil,
		"vmware":   config.VMWare != nil,
		"k8s":      config.K8S != nil,
	}
	typ := config.Type.ValueString()
	if typ == "" { // unknown during validation, e.g. computed from elsewhere
		return
	}
	// A self-managed inventory has no block of its own, so the mismatch case
	// below already covers every stray block under it.
	for name, set := range blocks {
		switch {
		case name == typ && !set:
			resp.Diagnostics.AddAttributeError(path.Root("type"),
				"missing configuration block",
				fmt.Sprintf("an inventory of type %q needs a %q configuration block", typ, typ))
		case name != typ && set:
			resp.Diagnostics.AddAttributeError(path.Root(name),
				"configuration block does not match type",
				fmt.Sprintf("the inventory is of type %q; remove the %q block or change the type", typ, name))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// The server requires these per type; catching them here makes the miss a
	// plan-time error instead of an apply-time 400. They cannot be Required in
	// the schema: the framework enforces required nested attributes even when
	// the enclosing block is absent, which would demand every block at once.
	// An unknown value is a value; only a genuinely unset one is an error.
	require := func(block string, fields map[string]types.String) {
		for name, v := range fields {
			if v.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root(block).AtName(name),
					"missing required field",
					fmt.Sprintf("a %s inventory needs %s.%s", typ, block, name))
			}
		}
	}
	switch typ {
	case "aws":
		require("aws", map[string]types.String{
			"credentials_type": config.AWS.CredentialsType,
			"region":           config.AWS.Region,
		})
		if config.AWS.CredentialsType.ValueString() == "access_key" {
			require("aws", map[string]types.String{
				"access_key":        config.AWS.AccessKey,
				"secret_access_key": config.AWS.SecretAccessKey,
			})
		}
	case "ovh":
		require("ovh", map[string]types.String{
			"application_key":    config.OVH.ApplicationKey,
			"application_secret": config.OVH.ApplicationSecret,
			"consumer_key":       config.OVH.ConsumerKey,
			"endpoint":           config.OVH.Endpoint,
		})
	case "scaleway":
		require("scaleway", map[string]types.String{
			"project_id": config.Scaleway.ProjectID,
			"access_key": config.Scaleway.AccessKey,
			"secret_key": config.Scaleway.SecretKey,
		})
	case "gcp":
		require("gcp", map[string]types.String{
			"project_id": config.GCP.ProjectID,
		})
	case "vmware":
		require("vmware", map[string]types.String{
			"server":   config.VMWare.Server,
			"username": config.VMWare.Username,
			"password": config.VMWare.Password,
		})
	}
}

// field wraps a string attribute into the wire's {value} shape.
func field(v types.String) client.Field {
	return client.Field{Value: v.ValueString()}
}

func (m *inventoryModel) toRequest() *client.InventoryRequest {
	req := &client.InventoryRequest{
		Name: m.Name.ValueString(),
		Type: m.Type.ValueString(),
	}
	switch {
	case m.AWS != nil:
		req.AWS = &client.AWSInventoryConfig{
			CredentialsType: m.AWS.CredentialsType.ValueString(),
			AccessKey:       field(m.AWS.AccessKey),
			SecretAccessKey: field(m.AWS.SecretAccessKey),
			Region:          m.AWS.Region.ValueString(),
		}
	case m.OVH != nil:
		req.OVH = &client.OVHInventoryConfig{
			ApplicationKey:    field(m.OVH.ApplicationKey),
			ApplicationSecret: field(m.OVH.ApplicationSecret),
			ConsumerKey:       field(m.OVH.ConsumerKey),
			Endpoint:          m.OVH.Endpoint.ValueString(),
		}
	case m.Scaleway != nil:
		req.Scaleway = &client.ScalewayInventoryConfig{
			ProjectID: field(m.Scaleway.ProjectID),
			AccessKey: field(m.Scaleway.AccessKey),
			SecretKey: field(m.Scaleway.SecretKey),
		}
	case m.GCP != nil:
		req.GCP = &client.GCPInventoryConfig{
			ProjectID:          field(m.GCP.ProjectID),
			ServiceAccountJSON: field(m.GCP.ServiceAccountJSON),
		}
	case m.VMWare != nil:
		skip := "false"
		if m.VMWare.TLSSkipVerify.ValueBool() {
			skip = "true"
		}
		req.VMWare = &client.VMWareInventoryConfig{
			Server:        field(m.VMWare.Server),
			Username:      field(m.VMWare.Username),
			Password:      field(m.VMWare.Password),
			TLSSkipVerify: skip,
			TLSCABundle:   field(m.VMWare.TLSCABundle),
		}
	case m.K8S != nil:
		req.K8S = &client.K8SInventoryConfig{
			Kubeconfig: field(m.K8S.Kubeconfig),
		}
	}
	return req
}

// refreshFromInventory folds the server's view back into the model. Only the
// block matching the inventory's type is touched; the server echoes
// configuration values unmasked, which is what makes drift visible.
func (m *inventoryModel) refreshFromInventory(inv *client.Inventory) {
	m.ID = types.StringValue(inv.ID)
	m.Name = types.StringValue(inv.Name)
	m.Type = types.StringValue(inv.Type)
	switch {
	case inv.AWS != nil:
		if m.AWS == nil {
			m.AWS = &inventoryAWSModel{}
		}
		m.AWS.CredentialsType = stringOrNull(inv.AWS.CredentialsType, m.AWS.CredentialsType)
		m.AWS.AccessKey = stringOrNull(inv.AWS.AccessKey.Value, m.AWS.AccessKey)
		m.AWS.SecretAccessKey = stringOrNull(inv.AWS.SecretAccessKey.Value, m.AWS.SecretAccessKey)
		m.AWS.Region = stringOrNull(inv.AWS.Region, m.AWS.Region)
	case inv.OVH != nil:
		if m.OVH == nil {
			m.OVH = &inventoryOVHModel{}
		}
		m.OVH.ApplicationKey = stringOrNull(inv.OVH.ApplicationKey.Value, m.OVH.ApplicationKey)
		m.OVH.ApplicationSecret = stringOrNull(inv.OVH.ApplicationSecret.Value, m.OVH.ApplicationSecret)
		m.OVH.ConsumerKey = stringOrNull(inv.OVH.ConsumerKey.Value, m.OVH.ConsumerKey)
		m.OVH.Endpoint = stringOrNull(inv.OVH.Endpoint, m.OVH.Endpoint)
	case inv.Scaleway != nil:
		if m.Scaleway == nil {
			m.Scaleway = &inventoryScalewayModel{}
		}
		m.Scaleway.ProjectID = stringOrNull(inv.Scaleway.ProjectID.Value, m.Scaleway.ProjectID)
		m.Scaleway.AccessKey = stringOrNull(inv.Scaleway.AccessKey.Value, m.Scaleway.AccessKey)
		m.Scaleway.SecretKey = stringOrNull(inv.Scaleway.SecretKey.Value, m.Scaleway.SecretKey)
	case inv.GCP != nil:
		if m.GCP == nil {
			m.GCP = &inventoryGCPModel{}
		}
		m.GCP.ProjectID = stringOrNull(inv.GCP.ProjectID.Value, m.GCP.ProjectID)
		m.GCP.ServiceAccountJSON = stringOrNull(inv.GCP.ServiceAccountJSON.Value, m.GCP.ServiceAccountJSON)
	case inv.VMWare != nil:
		if m.VMWare == nil {
			m.VMWare = &inventoryVMWareModel{}
		}
		m.VMWare.Server = stringOrNull(inv.VMWare.Server.Value, m.VMWare.Server)
		m.VMWare.Username = stringOrNull(inv.VMWare.Username.Value, m.VMWare.Username)
		m.VMWare.Password = stringOrNull(inv.VMWare.Password.Value, m.VMWare.Password)
		m.VMWare.TLSSkipVerify = types.BoolValue(inv.VMWare.TLSSkipVerify == "true")
		m.VMWare.TLSCABundle = stringOrNull(inv.VMWare.TLSCABundle.Value, m.VMWare.TLSCABundle)
	case inv.K8S != nil:
		if m.K8S == nil {
			m.K8S = &inventoryK8SModel{}
		}
		m.K8S.Kubeconfig = stringOrNull(inv.K8S.Kubeconfig.Value, m.K8S.Kubeconfig)
	}
}

func (r *inventoryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan inventoryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateInventory(plan.toRequest())
	if err != nil {
		resp.Diagnostics.AddError("creating inventory", err.Error())
		return
	}

	// The inventory now exists: record its id before the read-back, so a
	// transient failure there cannot orphan it and duplicate it on the next
	// apply.
	plan.ID = types.StringValue(created.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read back rather than trusting the plan, the same as every resource
	// here: state carries what the server holds.
	inv, err := r.client.GetInventory(created.ID)
	if err != nil {
		resp.Diagnostics.AddError("reading inventory after create", err.Error())
		return
	}
	plan.refreshFromInventory(inv)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inventoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state inventoryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inv, err := r.client.GetInventory(state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("reading inventory", err.Error())
		return
	}

	state.refreshFromInventory(inv)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *inventoryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state inventoryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.UpdateInventory(state.ID.ValueString(), plan.toRequest()); err != nil {
		resp.Diagnostics.AddError("updating inventory", err.Error())
		return
	}

	inv, err := r.client.GetInventory(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("reading inventory after update", err.Error())
		return
	}
	plan.refreshFromInventory(inv)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inventoryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inventoryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteInventory(state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("deleting inventory", err.Error())
	}
}

func (r *inventoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
