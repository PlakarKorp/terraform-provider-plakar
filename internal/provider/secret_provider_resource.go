// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// secretProviderResource manages a secret provider: the external secret
// manager (Vault, a cloud secret manager) that other objects' fields can point
// into instead of holding a value. Its own credential is the one secret that
// cannot be delegated, so it is meant to travel write-only.
type secretProviderResource struct {
	client *client.Client
}

type secretProviderModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Integration     types.String `tfsdk:"integration"`
	Protocol        types.String `tfsdk:"protocol"`
	Fields          types.Map    `tfsdk:"fields"`
	FieldsWO        types.Map    `tfsdk:"fields_wo"`
	FieldsWOVersion types.Int64  `tfsdk:"fields_wo_version"`
}

func NewSecretProviderResource() resource.Resource {
	return &secretProviderResource{}
}

func (r *secretProviderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret_provider"
}

func (r *secretProviderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Plakar secret provider: an external secret manager that " +
			"store, connector and inventory credentials can be resolved from at " +
			"run time, so their values never reach Plakar's database or " +
			"Terraform state. Pass the secret provider's own credential in " +
			"fields_wo, which Terraform 1.11 and later never write to state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the secret provider, unique in the organization.",
			},
			"integration": schema.StringAttribute{
				Required:    true,
				Description: "Name of the installed integration backing the secret provider, e.g. vault.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"protocol": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Protocol spoken to the secret manager. Defaults to the " +
					"one the integration declares for secret providers; unlike " +
					"stores it does not share the integration's name (vault " +
					"speaks vault-sm).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"fields": schema.MapAttribute{
				Required:    true,
				ElementType: types.StringType,
				Sensitive:   true,
				Description: "Integration-specific configuration that may live in " +
					"state, e.g. url for vault. Only the keys declared here are " +
					"managed; anything else set server-side keeps its value.",
			},
			"fields_wo": schema.MapAttribute{
				Optional:    true,
				WriteOnly:   true,
				Sensitive:   true,
				ElementType: types.StringType,
				Description: "Integration-specific configuration that never lands " +
					"in state, e.g. token for vault. Requires Terraform 1.11 or " +
					"later. Its values are sent at creation and then only when " +
					"fields_wo_version changes: Terraform cannot compare a " +
					"write-only value with anything, so the version is what " +
					"rotates it. A key may not appear in both fields and fields_wo. " +
					"A key removed from fields_wo keeps its server-side value, as " +
					"for fields. An import cannot recover it: after one, keep fields_wo in the " +
					"configuration and bump fields_wo_version to send it again.",
			},
			"fields_wo_version": schema.Int64Attribute{
				Optional: true,
				Description: "Change this to send fields_wo again, for instance " +
					"after rotating the credential it carries.",
			},
		},
	}
}

func (r *secretProviderResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig refuses a key that is set both in fields and in fields_wo.
// Both maps feed the same server-side field map, so one value would silently
// win over the other.
func (r *secretProviderResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config secretProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(checkFieldForms(
		fieldForm{"fields", config.Fields},
		fieldForm{"fields_wo", config.FieldsWO},
	)...)
}

// fieldForm is one attribute that contributes keys to a server-side field map.
type fieldForm struct {
	attribute string
	keys      types.Map
}

// checkFieldForms reports every key that more than one form sets. A form whose
// keys are not known yet says nothing, and is skipped.
func checkFieldForms(forms ...fieldForm) diag.Diagnostics {
	var diags diag.Diagnostics
	seen := map[string]string{}
	for _, f := range forms {
		if f.keys.IsNull() || f.keys.IsUnknown() {
			continue
		}
		for k := range f.keys.Elements() {
			if first, ok := seen[k]; ok {
				diags.AddAttributeError(path.Root(f.attribute).AtMapKey(k),
					"field set twice",
					fmt.Sprintf("%q is set in both %s and %s; set it in one of them.", k, first, f.attribute))
				continue
			}
			seen[k] = f.attribute
		}
	}
	return diags
}

// secretProviderProtocol picks the protocol to use: the configured one,
// checked against what the integration declares, or the integration's only
// secret provider protocol.
func (r *secretProviderResource) secretProviderProtocol(integration *client.Integration, configured string) (*client.IntegrationProtocol, error) {
	protocols, err := r.client.IntegrationProtocols(integration.ID, "secret_provider")
	if err != nil {
		return nil, err
	}
	schemes := make([]string, 0, len(protocols))
	for i := range protocols {
		if configured != "" && protocols[i].Scheme == configured {
			return &protocols[i], nil
		}
		schemes = append(schemes, protocols[i].Scheme)
	}
	switch {
	case configured != "":
		return nil, fmt.Errorf("integration %q has no secret provider protocol %q; it declares %v", integration.Name, configured, schemes)
	case len(protocols) == 1:
		return &protocols[0], nil
	case len(protocols) == 0:
		return nil, fmt.Errorf("integration %q cannot back a secret provider", integration.Name)
	default:
		return nil, fmt.Errorf("integration %q declares several secret provider protocols %v; set protocol", integration.Name, schemes)
	}
}

// checkRequiredFields reports the keys a protocol requires that neither fields
// nor fields_wo set, before the server refuses the whole request with a
// message that names none of them.
func checkRequiredFields(protocol *client.IntegrationProtocol, fields map[string]client.Field, diags *diag.Diagnostics) {
	for _, f := range protocol.Fields {
		if _, ok := fields[f.Name]; f.Required && !ok {
			diags.AddAttributeError(path.Root("fields"), "missing secret provider field",
				fmt.Sprintf("protocol %s requires %q; set it in fields, or in fields_wo if it is a credential.", protocol.Scheme, f.Name))
		}
	}
}

// secretProviderFields writes the declared plain fields and, when send is
// set, the write-only ones over a field map.
func secretProviderFields(ctx context.Context, into map[string]client.Field, plain, writeOnly types.Map, send bool, diags *diag.Diagnostics) {
	// ElementsAs replaces the target map rather than adding to it, so each
	// form is decoded on its own.
	forms := []types.Map{plain}
	if send && !writeOnly.IsNull() && !writeOnly.IsUnknown() {
		forms = append(forms, writeOnly)
	}
	for _, form := range forms {
		values := map[string]string{}
		diags.Append(form.ElementsAs(ctx, &values, false)...)
		for k, v := range values {
			into[k] = client.Field{Value: v}
		}
	}
}

func (r *secretProviderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan secretProviderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	// Write-only values are never in the plan, only in the configuration.
	var writeOnly types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("fields_wo"), &writeOnly)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integration, err := r.client.IntegrationByName(plan.Integration.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("resolving integration", err.Error())
		return
	}
	protocol, err := r.secretProviderProtocol(integration, plan.Protocol.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("resolving protocol", err.Error())
		return
	}

	sreq := &client.SecretProviderRequest{
		Name:     plan.Name.ValueString(),
		Protocol: protocol.Scheme,
		Fields:   map[string]client.Field{},
	}
	sreq.Integration.ID = integration.ID
	secretProviderFields(ctx, sreq.Fields, plan.Fields, writeOnly, true, &resp.Diagnostics)
	checkRequiredFields(protocol, sreq.Fields, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateSecretProvider(sreq)
	if err != nil {
		resp.Diagnostics.AddError("creating secret provider", err.Error())
		return
	}

	// Record the id before the read-back, so a transient failure there cannot
	// orphan the secret provider and duplicate it on the next apply.
	plan.ID = types.StringValue(created.ID)
	plan.Protocol = types.StringValue(protocol.Scheme)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sp, err := r.client.GetSecretProvider(created.ID)
	if err != nil {
		resp.Diagnostics.AddError("reading secret provider after create", err.Error())
		return
	}
	r.refresh(ctx, &plan, sp, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// refresh maps a read secret provider back onto the model. The read carries
// every field in clear, the credential included, so only the keys declared in
// fields are copied back; after an import, the keys the integration does not
// mark sensitive. fields_wo stays null: it is never read back.
func (r *secretProviderResource) refresh(ctx context.Context, m *secretProviderModel, sp *client.SecretProvider, diags *diag.Diagnostics) {
	m.Name = types.StringValue(sp.Name)
	m.Protocol = types.StringValue(sp.Protocol)
	if sp.Integration.Name != "" {
		m.Integration = types.StringValue(sp.Integration.Name)
	}

	values := map[string]string{}
	if m.Fields.IsNull() || m.Fields.IsUnknown() {
		// Import path: adopt what is safe to hold in state.
		protocols, err := r.client.IntegrationProtocols(sp.Integration.ID, "secret_provider")
		if err != nil {
			diags.AddError("reading the integration's fields", err.Error())
			return
		}
		sensitive := map[string]bool{}
		for _, p := range protocols {
			if p.Scheme != sp.Protocol {
				continue
			}
			for _, f := range p.Fields {
				sensitive[f.Name] = f.Sensitive
			}
		}
		for k, f := range sp.Fields {
			// A key the integration does not describe is treated as sensitive.
			if s, known := sensitive[k]; known && !s {
				values[k] = f.Value
			}
		}
	} else {
		diags.Append(m.Fields.ElementsAs(ctx, &values, false)...)
		for k := range values {
			if f, ok := sp.Fields[k]; ok {
				values[k] = f.Value
			}
		}
	}
	mv, d := types.MapValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	m.Fields = mv
	m.FieldsWO = types.MapNull(types.StringType)
}

func (r *secretProviderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretProviderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sp, err := r.client.GetSecretProvider(state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("reading secret provider", err.Error())
		return
	}

	r.refresh(ctx, &state, sp, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *secretProviderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state secretProviderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	var writeOnly types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("fields_wo"), &writeOnly)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetSecretProvider(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("reading secret provider before update", err.Error())
		return
	}

	// v1 update is a PUT that replaces the whole field map, and a field left
	// out is deleted: start from the current map, so the credential and every
	// key we do not manage survive, and write the plan over it. The write-only
	// values only go out when their version moved.
	sreq := &client.SecretProviderRequest{
		Name:     plan.Name.ValueString(),
		Protocol: current.Protocol,
		Fields:   map[string]client.Field{},
	}
	sreq.Integration.ID = current.Integration.ID
	if !plan.Protocol.IsNull() && !plan.Protocol.IsUnknown() && plan.Protocol.ValueString() != current.Protocol {
		// Same check as at creation: an unknown protocol is a 500 otherwise.
		integration := &client.Integration{ID: current.Integration.ID, Name: current.Integration.Name}
		protocol, err := r.secretProviderProtocol(integration, plan.Protocol.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("resolving protocol", err.Error())
			return
		}
		sreq.Protocol = protocol.Scheme
	}
	for k, v := range current.Fields {
		sreq.Fields[k] = v
	}
	send := !plan.FieldsWOVersion.Equal(state.FieldsWOVersion)
	secretProviderFields(ctx, sreq.Fields, plan.Fields, writeOnly, send, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateSecretProvider(state.ID.ValueString(), sreq); err != nil {
		resp.Diagnostics.AddError("updating secret provider", err.Error())
		return
	}

	plan.ID = state.ID
	if plan.Protocol.IsUnknown() || plan.Protocol.IsNull() {
		plan.Protocol = types.StringValue(sreq.Protocol)
	}
	plan.FieldsWO = types.MapNull(types.StringType)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *secretProviderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretProviderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSecretProvider(state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("deleting secret provider", err.Error())
	}
}

func (r *secretProviderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
